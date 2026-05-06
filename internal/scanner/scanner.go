package scanner

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	dbgen "github.com/nrhtr/spruce/internal/db/generated"
	"github.com/nrhtr/spruce/internal/evaluator"
	"github.com/nrhtr/spruce/internal/platform"
)

type Scanner struct {
	db        *sql.DB
	queries   *dbgen.Queries
	platforms map[string]platform.Platform
	evaluator *evaluator.Evaluator
	log       *slog.Logger
	mu        sync.Mutex
}

func New(db *sql.DB, queries *dbgen.Queries, platforms []platform.Platform, eval *evaluator.Evaluator, log *slog.Logger) *Scanner {
	pm := make(map[string]platform.Platform, len(platforms))
	for _, p := range platforms {
		pm[p.Name()] = p
	}
	s := &Scanner{
		db:        db,
		queries:   queries,
		platforms: pm,
		evaluator: eval,
		log:       log,
	}
	s.cleanupStaleRuns()
	return s
}

// cleanupStaleRuns marks any scan_runs left in 'running' state as failed.
// These are orphans from a previous process that was killed mid-scan.
func (s *Scanner) cleanupStaleRuns() {
	res, err := s.queries.FailStaleRuns(context.Background())
	if err != nil {
		s.log.Warn("cleanup stale scan runs", "error", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.log.Warn("marked stale scan runs as failed", "count", n)
	}
}

func (s *Scanner) RunAll(ctx context.Context) {
	if !s.mu.TryLock() {
		s.log.Warn("scan already in progress, skipping")
		return
	}
	defer s.mu.Unlock()

	searches, err := s.queries.ListActiveSearches(ctx)
	if err != nil {
		s.log.Error("list searches", "error", err)
		return
	}
	for _, search := range searches {
		if err := s.RunSearch(ctx, search); err != nil {
			s.log.Error("scan search", "search_id", search.ID, "error", err)
		}
	}
}

func (s *Scanner) RunSearch(ctx context.Context, search dbgen.Search) error {
	var platformNames []string
	if err := json.Unmarshal([]byte(search.Platforms), &platformNames); err != nil {
		return err
	}

	q := platform.Query{
		Keywords: search.Keywords,
	}
	if search.MinPrice.Valid {
		v := search.MinPrice.Float64
		q.MinPrice = &v
	}
	if search.MaxPrice.Valid {
		v := search.MaxPrice.Float64
		q.MaxPrice = &v
	}
	q.Currency = search.Currency
	if search.Location.Valid {
		v := search.Location.String
		q.Location = &v
	}

	for _, name := range platformNames {
		p, ok := s.platforms[name]
		if !ok {
			continue
		}
		if err := s.runPlatformSearch(ctx, search, p, q); err != nil {
			s.log.Error("platform scan", "platform", name, "search_id", search.ID, "error", err)
		}
	}
	return nil
}

func (s *Scanner) runPlatformSearch(ctx context.Context, search dbgen.Search, p platform.Platform, q platform.Query) error {
	run, err := s.queries.CreateScanRun(ctx, dbgen.CreateScanRunParams{
		SearchID: search.ID,
		Platform: p.Name(),
	})
	if err != nil {
		return err
	}

	listings, scanErr := p.Search(ctx, q)

	errStr := ""
	status := "completed"
	if scanErr != nil {
		s.log.Warn("platform search error", "platform", p.Name(), "error", scanErr)
		errStr = scanErr.Error()
		status = "failed"
	}

	var newItems int64
	var evalTargets []struct {
		listing dbgen.Listing
		isNew   bool
	}

	for _, l := range listings {
		imageJSON, _ := json.Marshal(l.ImageURLs)
		raw := l.RawData
		if raw == nil {
			raw = json.RawMessage("{}")
		}

		var endTime sql.NullInt64
		if l.EndTime != nil {
			endTime = sql.NullInt64{Int64: l.EndTime.Unix(), Valid: true}
		}
		var price sql.NullFloat64
		if l.Price != nil {
			price = sql.NullFloat64{Float64: *l.Price, Valid: true}
		}

		upserted, err := s.queries.UpsertListing(ctx, dbgen.UpsertListingParams{
			ExternalID:  l.ExternalID,
			Platform:    l.Platform,
			Title:       l.Title,
			Description: l.Description,
			Price:       price,
			Currency:    l.Currency,
			Url:         l.URL,
			ImageUrls:   string(imageJSON),
			EndTime:     endTime,
			Condition:   l.Condition,
			Location:    l.Location,
			RawData:     string(raw),
			Status:      "active",
		})
		if err != nil {
			s.log.Error("upsert listing", "error", err, "external_id", l.ExternalID)
			continue
		}

		if err := s.queries.LinkListingToSearch(ctx, dbgen.LinkListingToSearchParams{
			SearchID:  search.ID,
			ListingID: upserted.ID,
		}); err != nil {
			// Conflict means already linked — not an error.
			s.log.Debug("link listing (already exists?)", "error", err)
		}

		// A listing is "new" if first_seen and last_seen are within 10 seconds of each other.
		isNew := upserted.LastSeen-upserted.FirstSeen < 10
		if isNew {
			newItems++
		}
		evalTargets = append(evalTargets, struct {
			listing dbgen.Listing
			isNew   bool
		}{upserted, isNew})
	}

	// Fetch full image set for new listings from the detail page.
	for _, t := range evalTargets {
		if !t.isNew {
			continue
		}
		detail, err := p.GetListing(ctx, t.listing.ExternalID)
		if err != nil {
			s.log.Warn("fetch listing detail", "listing_id", t.listing.ID, "error", err)
			continue
		}
		if len(detail.ImageURLs) > 1 {
			imageJSON, _ := json.Marshal(detail.ImageURLs)
			if err := s.queries.UpdateListingImages(ctx, dbgen.UpdateListingImagesParams{
				ImageUrls: string(imageJSON),
				ID:        t.listing.ID,
			}); err != nil {
				s.log.Warn("update listing images", "listing_id", t.listing.ID, "error", err)
			} else {
				s.log.Debug("updated images", "listing_id", t.listing.ID, "count", len(detail.ImageURLs))
			}
		}
	}

	// Evaluate new listings with Claude.
	for _, t := range evalTargets {
		if !t.isNew {
			continue
		}
		pl := listingToplatform(t.listing)
		result, err := s.evaluator.Evaluate(ctx, pl, search)
		if err != nil {
			s.log.Warn("evaluate listing", "listing_id", t.listing.ID, "error", err)
			continue
		}
		if _, err := s.queries.UpsertEvaluation(ctx, dbgen.UpsertEvaluationParams{
			ListingID: t.listing.ID,
			SearchID:  search.ID,
			Score:     result.Score,
			Reasoning: result.Reasoning,
			ModelUsed: s.evaluator.Model(),
		}); err != nil {
			s.log.Error("upsert evaluation", "error", err)
		}
	}

	if _, err := s.queries.FinishScanRun(ctx, dbgen.FinishScanRunParams{
		NewItems: newItems,
		Errors:   errStr,
		Status:   status,
		ID:       run.ID,
	}); err != nil {
		s.log.Error("finish scan run", "error", err)
	}

	s.log.Info("scan complete", "platform", p.Name(), "search", search.Name, "new", newItems, "status", status)
	return nil
}

func listingToplatform(l dbgen.Listing) platform.Listing {
	pl := platform.Listing{
		ExternalID:  l.ExternalID,
		Platform:    l.Platform,
		Title:       l.Title,
		Description: l.Description,
		Currency:    l.Currency,
		URL:         l.Url,
		Condition:   l.Condition,
		Location:    l.Location,
	}
	if l.Price.Valid {
		v := l.Price.Float64
		pl.Price = &v
	}
	if l.EndTime.Valid {
		t := time.Unix(l.EndTime.Int64, 0)
		pl.EndTime = &t
	}
	var imageURLs []string
	json.Unmarshal([]byte(l.ImageUrls), &imageURLs)
	pl.ImageURLs = imageURLs
	return pl
}
