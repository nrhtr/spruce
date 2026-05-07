package handlers

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nrhtr/spruce/internal/config"
	appdb "github.com/nrhtr/spruce/internal/db"
	dbgen "github.com/nrhtr/spruce/internal/db/generated"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	sqlDB, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := appdb.Migrate(sqlDB); err != nil {
		t.Fatal(err)
	}
	return &Handler{
		db:      sqlDB,
		queries: dbgen.New(sqlDB),
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		loc:     time.UTC,
		cfg:     &config.Config{},
	}
}

// TestFetchListings_DeletedSearchExcluded verifies that listings belonging only
// to a hard-deleted search do not appear in the unfiltered view.
func TestFetchListings_DeletedSearchExcluded(t *testing.T) {
	h := newTestHandler(t)
	ctx := context.Background()

	s1, err := h.queries.CreateSearch(ctx, dbgen.CreateSearchParams{
		Name: "Active Search", Keywords: "keyboard", Platforms: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := h.queries.CreateSearch(ctx, dbgen.CreateSearchParams{
		Name: "To Delete", Keywords: "laptop", Platforms: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := h.db.ExecContext(ctx,
		`INSERT INTO listings (external_id, platform, title, url, image_urls) VALUES ('L1','ebay','Keyboard','http://a','[]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.ExecContext(ctx,
		`INSERT INTO listings (external_id, platform, title, url, image_urls) VALUES ('L2','ebay','Laptop','http://b','[]')`); err != nil {
		t.Fatal(err)
	}

	var l1id, l2id int64
	h.db.QueryRowContext(ctx, `SELECT id FROM listings WHERE external_id = 'L1'`).Scan(&l1id)
	h.db.QueryRowContext(ctx, `SELECT id FROM listings WHERE external_id = 'L2'`).Scan(&l2id)

	h.db.ExecContext(ctx, `INSERT INTO search_listings (search_id, listing_id) VALUES (?, ?)`, s1.ID, l1id)
	h.db.ExecContext(ctx, `INSERT INTO search_listings (search_id, listing_id) VALUES (?, ?)`, s2.ID, l2id)

	// Delete s2 — cascades search_listings row for L2.
	if err := h.queries.DeleteSearch(ctx, s2.ID); err != nil {
		t.Fatal(err)
	}

	rows, total, err := h.fetchListings(ctx, listingsFilter{Page: 1, ShowMuted: true})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("unfiltered: expected 1 listing, got %d", total)
	}
	if len(rows) != 1 || rows[0].ExternalID != "L1" {
		t.Errorf("unfiltered: expected L1, got %v", rows)
	}
}

// TestFetchListings_ActiveSearchVisible verifies that listings from an active
// search appear in both the unfiltered and search-filtered views.
func TestFetchListings_ActiveSearchVisible(t *testing.T) {
	h := newTestHandler(t)
	ctx := context.Background()

	s, err := h.queries.CreateSearch(ctx, dbgen.CreateSearchParams{
		Name: "Active", Keywords: "keyboard", Platforms: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := h.db.ExecContext(ctx,
		`INSERT INTO listings (external_id, platform, title, url, image_urls) VALUES ('L1','ebay','Keyboard','http://a','[]')`); err != nil {
		t.Fatal(err)
	}
	var lid int64
	h.db.QueryRowContext(ctx, `SELECT id FROM listings WHERE external_id = 'L1'`).Scan(&lid)
	h.db.ExecContext(ctx, `INSERT INTO search_listings (search_id, listing_id) VALUES (?, ?)`, s.ID, lid)

	// Unfiltered.
	rows, total, err := h.fetchListings(ctx, listingsFilter{Page: 1, ShowMuted: true})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("unfiltered: expected 1 listing, got %d", total)
	}

	// Filtered by search.
	rows, total, err = h.fetchListings(ctx, listingsFilter{Page: 1, ShowMuted: true, SearchID: s.ID})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("filtered: expected 1 listing, got %d", total)
	}
}
