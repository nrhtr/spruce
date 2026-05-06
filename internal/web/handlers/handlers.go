package handlers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/nrhtr/spruce/internal/config"
	dbgen "github.com/nrhtr/spruce/internal/db/generated"
	"github.com/nrhtr/spruce/internal/notifier"
	"github.com/nrhtr/spruce/internal/scanner"
	"github.com/nrhtr/spruce/internal/web"
)

type Handler struct {
	db      *sql.DB
	queries *dbgen.Queries
	scanner *scanner.Scanner
	log     *slog.Logger
	tmpls   map[string]*template.Template
	loc     *time.Location
	cfg     *config.Config
}

func New(db *sql.DB, queries *dbgen.Queries, scnr *scanner.Scanner, log *slog.Logger, loc *time.Location, cfg *config.Config) (*Handler, error) {
	tmpls, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	return &Handler{
		db:      db,
		queries: queries,
		scanner: scnr,
		log:     log,
		tmpls:   tmpls,
		loc:     loc,
		cfg:     cfg,
	}, nil
}

func parseTemplates() (map[string]*template.Template, error) {
	funcs := template.FuncMap{
		"scoreColor": func(score float64) string {
			switch {
			case score >= 8:
				return "text-green-600"
			case score >= 6:
				return "text-yellow-600"
			default:
				return "text-red-500"
			}
		},
		"formatPrice": func(price sql.NullFloat64, currency string) string {
			if !price.Valid {
				return ""
			}
			return notifier.FormatPrice(price, currency)
		},
		"formatPrice2": func(price float64, currency string) string {
			return notifier.FormatPrice(sql.NullFloat64{Float64: price, Valid: true}, currency)
		},
		"formatTime": func(epoch int64) string {
			return time.Unix(epoch, 0).Format("2 Jan 06 3:04pm")
		},
		"inc":      func(i int) int { return i + 1 },
		"dec":      func(i int) int { return i - 1 },
		"proxyURL": ProxyURL,
	}

	sub, err := fs.Sub(web.TemplatesFS, "templates")
	if err != nil {
		return nil, err
	}

	pages := map[string][]string{
		"dashboard":      {"base.html", "dashboard.html"},
		"searches":       {"base.html", "searches.html"},
		"search_form":    {"base.html", "search_form.html"},
		"listings":       {"base.html", "listings.html"},
		"listing_detail": {"base.html", "listing_detail.html"},
		"bids":           {"base.html", "bids.html"},
		"scan_runs":      {"base.html", "scan_runs.html"},
		"login":          {"base.html", "login.html"},
	}

	tmpls := make(map[string]*template.Template, len(pages))
	for name, files := range pages {
		t, err := template.New("").Funcs(funcs).ParseFS(sub, files...)
		if err != nil {
			return nil, err
		}
		tmpls[name] = t
	}
	return tmpls, nil
}

func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	tmpl, ok := h.tmpls[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		h.log.Error("render template", "name", name, "error", err)
	}
}

// Dashboard

type DashboardStats struct {
	ActiveSearches int64
	TotalListings  int64
	NewToday       int64
	UpcomingCount  int
}

type ListingRow struct {
	dbgen.Listing
	Price      sql.NullFloat64
	EvalScore  float64
	EndingSoon bool
	EndTime    sql.NullInt64
}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	activeSearches, _ := h.queries.CountActiveSearches(ctx)
	totalListings, _ := h.queries.CountTotalListings(ctx)
	todayStart := time.Now().Truncate(24 * time.Hour).Unix()
	newToday, _ := h.queries.CountNewToday(ctx, todayStart)

	endThreshold := sql.NullInt64{Int64: time.Now().Add(24 * time.Hour).Unix(), Valid: true}
	upcomingEndings, _ := h.queries.ListEndingSoon(ctx, endThreshold)

	recentListings, _ := h.queries.ListRecentListings(ctx, 10)

	type dashListing struct {
		dbgen.Listing
		Price      string
		Score      float64
		EndTime    string
		EndingSoon bool
	}

	var topListings []dashListing
	for _, l := range recentListings {
		listing, score := convertRecentRow(l)
		dl := dashListing{Listing: listing, Score: score}
		if l.Price.Valid {
			dl.Price = notifier.FormatPrice(l.Price, l.Currency)
		}
		if l.EndTime.Valid {
			t := time.Unix(l.EndTime.Int64, 0).In(h.loc)
			dl.EndTime = t.Format("2 Jan 3:04pm")
			dl.EndingSoon = time.Until(t) < 24*time.Hour
		}
		topListings = append(topListings, dl)
	}

	type upcomingListing struct {
		dbgen.Listing
		Price   string
		EndTime string
	}
	var upcoming []upcomingListing
	for _, l := range upcomingEndings {
		ul := upcomingListing{Listing: l}
		if l.Price.Valid {
			ul.Price = notifier.FormatPrice(l.Price, l.Currency)
		}
		if l.EndTime.Valid {
			t := time.Unix(l.EndTime.Int64, 0).In(h.loc)
			ul.EndTime = t.Format("Mon 2 Jan 3:04pm")
		}
		upcoming = append(upcoming, ul)
	}

	h.render(w, "dashboard", map[string]any{
		"Stats": DashboardStats{
			ActiveSearches: activeSearches,
			TotalListings:  totalListings,
			NewToday:       newToday,
			UpcomingCount:  len(upcomingEndings),
		},
		"TopListings":     topListings,
		"UpcomingEndings": upcoming,
	})
}

// Searches

type platformOption struct {
	Name     string
	Label    string
	Checked  bool
	Disabled bool
}

var allPlatforms = []platformOption{
	{Name: "ebay", Label: "eBay"},
	{Name: "buyee", Label: "Buyee (Yahoo Japan)"},
	{Name: "gumtree", Label: "Gumtree AU"},
	{Name: "facebook", Label: "Facebook", Disabled: true},
}

type searchView struct {
	dbgen.Search
	PlatformList []string
	ListingCount int64
	IsScanning   bool
}

func (h *Handler) buildSearchViews(ctx context.Context) ([]searchView, error) {
	searches, err := h.queries.ListAllSearches(ctx)
	if err != nil {
		return nil, err
	}
	runningIDs, _ := h.queries.ListRunningSearchIDs(ctx)
	scanning := make(map[int64]bool, len(runningIDs))
	for _, id := range runningIDs {
		scanning[id] = true
	}
	var views []searchView
	for _, s := range searches {
		var pl []string
		json.Unmarshal([]byte(s.Platforms), &pl)
		count, _ := h.queries.CountListingsBySearch(ctx, s.ID)
		views = append(views, searchView{Search: s, PlatformList: pl, ListingCount: count, IsScanning: scanning[s.ID]})
	}
	return views, nil
}

func (h *Handler) ListSearches(w http.ResponseWriter, r *http.Request) {
	views, err := h.buildSearchViews(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "searches", map[string]any{"Searches": views})
}

func (h *Handler) SearchesPartial(w http.ResponseWriter, r *http.Request) {
	views, _ := h.buildSearchViews(r.Context())
	tmpl, ok := h.tmpls["searches"]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.ExecuteTemplate(w, "searches_rows", map[string]any{"Searches": views})
}

func (h *Handler) NewSearchForm(w http.ResponseWriter, r *http.Request) {
	h.render(w, "search_form", map[string]any{
		"AllPlatforms": allPlatforms,
	})
}

func (h *Handler) CreateSearch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	platforms := r.Form["platforms"]
	platformJSON, _ := json.Marshal(platforms)

	params := dbgen.CreateSearchParams{
		Name:        r.FormValue("name"),
		Keywords:    r.FormValue("keywords"),
		Description: r.FormValue("description"),
		Currency:    r.FormValue("currency"),
		Platforms:   string(platformJSON),
	}
	if v := r.FormValue("min_price"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			params.MinPrice = sql.NullFloat64{Float64: f, Valid: true}
		}
	}
	if v := r.FormValue("max_price"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			params.MaxPrice = sql.NullFloat64{Float64: f, Valid: true}
		}
	}
	if v := r.FormValue("location"); v != "" {
		params.Location = sql.NullString{String: v, Valid: true}
	}

	s, err := h.queries.CreateSearch(r.Context(), params)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	go func() {
		if err := h.scanner.RunSearch(context.Background(), s); err != nil {
			h.log.Error("initial scan", "search_id", s.ID, "error", err)
		}
	}()

	http.Redirect(w, r, "/searches", http.StatusSeeOther)
}

func (h *Handler) EditSearchForm(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	s, err := h.queries.GetSearch(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var checkedPlatforms []string
	json.Unmarshal([]byte(s.Platforms), &checkedPlatforms)
	checkedSet := make(map[string]bool)
	for _, p := range checkedPlatforms {
		checkedSet[p] = true
	}

	platforms := make([]platformOption, len(allPlatforms))
	for i, p := range allPlatforms {
		platforms[i] = p
		platforms[i].Checked = checkedSet[p.Name]
	}

	h.render(w, "search_form", map[string]any{
		"Search":       s,
		"AllPlatforms": platforms,
	})
}

func (h *Handler) UpdateSearch(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	platforms := r.Form["platforms"]
	platformJSON, _ := json.Marshal(platforms)

	params := dbgen.UpdateSearchParams{
		ID:          id,
		Name:        r.FormValue("name"),
		Keywords:    r.FormValue("keywords"),
		Description: r.FormValue("description"),
		Currency:    r.FormValue("currency"),
		Platforms:   string(platformJSON),
		Active:      0,
	}
	if r.FormValue("active") == "1" {
		params.Active = 1
	}
	if v := r.FormValue("min_price"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			params.MinPrice = sql.NullFloat64{Float64: f, Valid: true}
		}
	}
	if v := r.FormValue("max_price"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			params.MaxPrice = sql.NullFloat64{Float64: f, Valid: true}
		}
	}
	if v := r.FormValue("location"); v != "" {
		params.Location = sql.NullString{String: v, Valid: true}
	}

	if _, err := h.queries.UpdateSearch(r.Context(), params); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/searches", http.StatusSeeOther)
}

func (h *Handler) DeleteSearch(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.queries.DeleteSearch(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/searches", http.StatusSeeOther)
}

func (h *Handler) TriggerScan(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	s, err := h.queries.GetSearch(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	go func() {
		if err := h.scanner.RunSearch(context.Background(), s); err != nil {
			h.log.Error("manual scan", "search_id", s.ID, "error", err)
		}
	}()
	http.Redirect(w, r, "/searches", http.StatusSeeOther)
}

// Listings

type listingsFilter struct {
	SearchID  int64
	Platform  string
	MinScore  float64
	Page      int
	Sort      string
	Order     string
	ShowMuted bool
}

// listingRow is the view model passed to the listings template.
type listingRow struct {
	dbgen.Listing
	Price      string
	EvalScore  float64
	EndTime    string
	EndingSoon bool
	BidAmount  string // formatted, non-empty if a pending bid exists
	BidResult  string // "pending"|"won"|"lost"|"retracted"|""
}

// fetchListings runs a dynamic SQL query based on the filter and returns rows + total count.
func (h *Handler) fetchListings(ctx context.Context, f listingsFilter) ([]listingRow, int64, error) {
	const pageSize = 50

	// Resolve sort column (whitelist to prevent injection).
	sortCol := "eval_score"
	if f.SearchID == 0 {
		sortCol = "first_seen"
	}
	switch f.Sort {
	case "score":
		sortCol = "eval_score"
	case "price":
		sortCol = "price"
	case "end_time":
		sortCol = "end_time"
	case "status":
		sortCol = "status"
	case "first_seen":
		sortCol = "first_seen"
	}

	dir := "DESC"
	if f.Order == "asc" {
		dir = "ASC"
	}

	offset := int64((f.Page - 1) * pageSize)

	var args []any
	var innerSQL string

	bidSubqueries := `
         COALESCE((SELECT result FROM bids WHERE listing_id = l.id ORDER BY placed_at DESC LIMIT 1), '') AS bid_result,
         COALESCE((SELECT amount FROM bids WHERE listing_id = l.id ORDER BY placed_at DESC LIMIT 1), 0.0) AS bid_amount,
         COALESCE((SELECT currency FROM bids WHERE listing_id = l.id ORDER BY placed_at DESC LIMIT 1), '') AS bid_currency`

	if f.SearchID > 0 {
		innerSQL = `SELECT l.id, l.external_id, l.platform, l.title, l.description, l.price,
         l.currency, l.url, l.image_urls, l.end_time, l.condition, l.location,
         l.raw_data, l.status, l.first_seen, l.last_seen,
         COALESCE(e.score, -1) AS eval_score,` + bidSubqueries + `
  FROM listings l
  JOIN search_listings sl ON sl.listing_id = l.id
  LEFT JOIN evaluations e ON e.listing_id = l.id AND e.search_id = sl.search_id
  WHERE sl.search_id = ?`
		args = append(args, f.SearchID)

		if !f.ShowMuted {
			innerSQL += ` AND l.status != 'muted'`
		}
		if f.Platform != "" {
			innerSQL += ` AND l.platform = ?`
			args = append(args, f.Platform)
		}
	} else {
		innerSQL = `SELECT l.id, l.external_id, l.platform, l.title, l.description, l.price,
         l.currency, l.url, l.image_urls, l.end_time, l.condition, l.location,
         l.raw_data, l.status, l.first_seen, l.last_seen,
         COALESCE(
           (SELECT e.score FROM evaluations e WHERE e.listing_id = l.id ORDER BY e.created_at DESC LIMIT 1),
           -1
         ) AS eval_score,` + bidSubqueries + `
  FROM listings l
  WHERE 1=1`

		if !f.ShowMuted {
			innerSQL += ` AND l.status != 'muted'`
		}
		if f.Platform != "" {
			innerSQL += ` AND l.platform = ?`
			args = append(args, f.Platform)
		}
	}

	// Count query wraps the inner SQL.
	var countArgs []any
	countArgs = append(countArgs, args...)
	countSQL := `SELECT COUNT(*) FROM (` + innerSQL + `) sub`
	if f.MinScore > 0 {
		countSQL += ` WHERE sub.eval_score >= ?`
		countArgs = append(countArgs, f.MinScore)
	}

	var total int64
	if err := h.db.QueryRowContext(ctx, countSQL, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Data query adds WHERE for min score, ORDER BY, LIMIT, OFFSET.
	dataSQL := `SELECT sub.* FROM (` + innerSQL + `) sub`
	dataArgs := append([]any{}, args...)
	if f.MinScore > 0 {
		dataSQL += ` WHERE sub.eval_score >= ?`
		dataArgs = append(dataArgs, f.MinScore)
	}
	dataSQL += ` ORDER BY sub.` + sortCol + ` ` + dir + ` NULLS LAST`
	dataSQL += ` LIMIT 51 OFFSET ?`
	dataArgs = append(dataArgs, offset)

	sqlRows, err := h.db.QueryContext(ctx, dataSQL, dataArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer sqlRows.Close()

	var results []listingRow
	for sqlRows.Next() {
		var (
			id, firstSeen, lastSeen                       int64
			externalID, platform, title, description      string
			currency, url, imageUrls, condition, location string
			rawData, status                               string
			price                                         sql.NullFloat64
			endTime                                       sql.NullInt64
			evalScore                                     float64
			bidResult, bidCurrency                        string
			bidAmount                                     float64
		)
		if err := sqlRows.Scan(
			&id, &externalID, &platform, &title, &description,
			&price, &currency, &url, &imageUrls, &endTime,
			&condition, &location, &rawData, &status,
			&firstSeen, &lastSeen, &evalScore,
			&bidResult, &bidAmount, &bidCurrency,
		); err != nil {
			return nil, 0, err
		}
		listing := dbgen.Listing{
			ID: id, ExternalID: externalID, Platform: platform,
			Title: title, Description: description, Price: price,
			Currency: currency, Url: url, ImageUrls: imageUrls,
			EndTime: endTime, Condition: condition, Location: location,
			RawData: rawData, Status: status, FirstSeen: firstSeen, LastSeen: lastSeen,
		}
		lr := listingRow{Listing: listing, EvalScore: evalScore, BidResult: bidResult}
		if price.Valid {
			lr.Price = notifier.FormatPrice(price, currency)
		}
		if endTime.Valid {
			t := time.Unix(endTime.Int64, 0).In(h.loc)
			lr.EndTime = t.Format("2 Jan 3:04pm")
			lr.EndingSoon = time.Until(t) < 24*time.Hour
		}
		if bidResult == "pending" && bidAmount > 0 {
			lr.BidAmount = notifier.FormatPrice(sql.NullFloat64{Float64: bidAmount, Valid: true}, bidCurrency)
		}
		results = append(results, lr)
	}
	if err := sqlRows.Err(); err != nil {
		return nil, 0, err
	}

	return results, total, nil
}

func (h *Handler) ListListings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	f := listingsFilter{Page: 1}
	q := r.URL.Query()
	if v := q.Get("search_id"); v != "" {
		f.SearchID, _ = strconv.ParseInt(v, 10, 64)
	}
	f.Platform = q.Get("platform")
	if v := q.Get("min_score"); v != "" {
		f.MinScore, _ = strconv.ParseFloat(v, 64)
	}
	if v := q.Get("page"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			f.Page = p
		}
	}
	f.Sort = q.Get("sort")
	f.Order = q.Get("order")
	f.ShowMuted = q.Get("show_muted") == "1"

	const pageSize = 50

	rows, total, err := h.fetchListings(ctx, f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	hasMore := len(rows) > pageSize
	if hasMore {
		rows = rows[:pageSize]
	}

	searches, _ := h.queries.ListAllSearches(ctx)

	qs := buildQueryString(r, "page")
	h.render(w, "listings", map[string]any{
		"Listings":        rows,
		"Searches":        searches,
		"FilterSearchID":  f.SearchID,
		"FilterPlatform":  f.Platform,
		"FilterMinScore":  f.MinScore,
		"FilterSort":      f.Sort,
		"FilterOrder":     f.Order,
		"FilterShowMuted": f.ShowMuted,
		"Page":            f.Page,
		"HasMore":         hasMore,
		"Total":           total,
		"QueryString":     template.URL(qs),
		"SortPrice":       sortURL(r, "price", f.Sort, f.Order),
		"SortScore":       sortURL(r, "score", f.Sort, f.Order),
		"SortEndTime":     sortURL(r, "end_time", f.Sort, f.Order),
		"SortStatus":      sortURL(r, "status", f.Sort, f.Order),
	})
}

func (h *Handler) MuteListing(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.queries.UpdateListingStatus(r.Context(), dbgen.UpdateListingStatusParams{
		Status: "muted",
		ID:     id,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		http.Redirect(w, r, ref, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/listings", http.StatusSeeOther)
}

func (h *Handler) UnmuteListing(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.queries.UpdateListingStatus(r.Context(), dbgen.UpdateListingStatusParams{
		Status: "active",
		ID:     id,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		http.Redirect(w, r, ref, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/listings", http.StatusSeeOther)
}

func (h *Handler) GetListing(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	l, err := h.queries.GetListing(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	bids, _ := h.queries.ListBidsByListing(ctx, id)

	var imageURLs []string
	json.Unmarshal([]byte(l.ImageUrls), &imageURLs)

	var endingSoon bool
	var timeRemaining string
	if l.EndTime.Valid {
		remaining := time.Until(time.Unix(l.EndTime.Int64, 0))
		endingSoon = remaining < 24*time.Hour
		if remaining > 0 {
			timeRemaining = formatDuration(remaining)
		}
	}

	// Try to fetch evaluation for this listing against any search.
	// Use first search that has it.
	var eval *dbgen.Evaluation
	searches, _ := h.queries.ListAllSearches(ctx)
	for _, s := range searches {
		e, err := h.queries.GetEvaluation(ctx, dbgen.GetEvaluationParams{
			ListingID: id,
			SearchID:  s.ID,
		})
		if err == nil {
			eval = &e
			break
		}
	}

	var evalFlags []string
	if eval != nil {
		// Flags are stored in reasoning as a simple prefix pattern.
		// For now we don't store them separately; this is a future enhancement.
	}

	// Compute winning status from bid history vs current price.
	winningStatus := ""
	ourBidAmount := 0.0
	if len(bids) > 0 {
		// Find our highest pending/active bid.
		for _, b := range bids {
			if b.Result == "pending" || b.Result == "" {
				if b.Amount > ourBidAmount {
					ourBidAmount = b.Amount
				}
			}
		}
		if ourBidAmount > 0 && l.Price.Valid {
			if ourBidAmount >= l.Price.Float64 {
				winningStatus = "winning"
			} else {
				winningStatus = "outbid"
			}
		}
	}

	h.render(w, "listing_detail", map[string]any{
		"Listing":       l,
		"Bids":          bids,
		"Evaluation":    eval,
		"EvalFlags":     evalFlags,
		"ImageURLs":     imageURLs,
		"EndingSoon":    endingSoon,
		"TimeRemaining": timeRemaining,
		"BackURL":       r.URL.Query().Get("back"),
		"WinningStatus": winningStatus,
		"OurBid":        ourBidAmount,
	})
}

// Bids

func (h *Handler) CreateBid(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	amount, err := strconv.ParseFloat(r.FormValue("amount"), 64)
	if err != nil || amount <= 0 {
		http.Error(w, "invalid amount", http.StatusBadRequest)
		return
	}

	l, err := h.queries.GetListing(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if _, err := h.queries.CreateBid(r.Context(), dbgen.CreateBidParams{
		ListingID: id,
		Amount:    amount,
		Currency:  l.Currency,
		Notes:     r.FormValue("notes"),
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/listings/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (h *Handler) UpdateBid(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result := r.FormValue("result")
	if _, err := h.queries.UpdateBidResult(r.Context(), dbgen.UpdateBidResultParams{
		Result: result,
		ID:     id,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/bids", http.StatusSeeOther)
}

func (h *Handler) ListBids(w http.ResponseWriter, r *http.Request) {
	bids, err := h.queries.ListAllBids(r.Context(), dbgen.ListAllBidsParams{
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "bids", map[string]any{"Bids": bids})
}

// Scan Runs

type scanRunView struct {
	dbgen.ListScanRunsPagedRow
	Duration string
}

func (h *Handler) ListScanRuns(w http.ResponseWriter, r *http.Request) {
	h.render(w, "scan_runs", h.scanRunData(r))
}

func (h *Handler) ScanRunsPartial(w http.ResponseWriter, r *http.Request) {
	data := h.scanRunData(r)
	tmpl, ok := h.tmpls["scan_runs"]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.ExecuteTemplate(w, "scan_runs_rows", data)
}

const scanRunsPageSize = 25

func (h *Handler) scanRunData(r *http.Request) map[string]any {
	page := 1
	if v := r.URL.Query().Get("page"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			page = p
		}
	}
	offset := int64((page - 1) * scanRunsPageSize)
	runs, _ := h.queries.ListScanRunsPaged(r.Context(), dbgen.ListScanRunsPagedParams{
		Limit:  scanRunsPageSize + 1,
		Offset: offset,
	})
	hasMore := len(runs) > scanRunsPageSize
	if hasMore {
		runs = runs[:scanRunsPageSize]
	}
	var views []scanRunView
	for _, run := range runs {
		v := scanRunView{ListScanRunsPagedRow: run}
		if run.FinishedAt.Valid {
			dur := time.Duration(run.FinishedAt.Int64-run.StartedAt) * time.Second
			v.Duration = dur.String()
		} else {
			v.Duration = "running…"
		}
		views = append(views, v)
	}
	return map[string]any{
		"Runs":    views,
		"Page":    page,
		"HasMore": hasMore,
	}
}

// Helpers

func pathID(r *http.Request, key string) (int64, error) {
	return strconv.ParseInt(r.PathValue(key), 10, 64)
}

func buildQueryString(r *http.Request, exclude ...string) string {
	q := r.URL.Query()
	for _, k := range exclude {
		q.Del(k)
	}
	return q.Encode()
}

// sortURL builds a safe href for a sort column header, toggling direction when
// the column is already active. Returns template.URL to bypass html/template's
// URL normalizer (which would otherwise double-encode the query string).
func sortURL(r *http.Request, col, activeCol, activeOrder string) template.URL {
	q := r.URL.Query()
	q.Del("page")
	dir := "desc"
	if col == activeCol && activeOrder == "desc" {
		dir = "asc"
	}
	q.Set("sort", col)
	q.Set("order", dir)
	return template.URL("?" + q.Encode())
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return strconv.Itoa(h) + "h " + strconv.Itoa(m) + "m"
	}
	return strconv.Itoa(m) + "m"
}

func convertRecentRow(l dbgen.ListRecentListingsRow) (dbgen.Listing, float64) {
	listing := dbgen.Listing{
		ID: l.ID, ExternalID: l.ExternalID, Platform: l.Platform,
		Title: l.Title, Description: l.Description, Price: l.Price,
		Currency: l.Currency, Url: l.Url, ImageUrls: l.ImageUrls,
		EndTime: l.EndTime, Condition: l.Condition, Location: l.Location,
		RawData: l.RawData, Status: l.Status, FirstSeen: l.FirstSeen, LastSeen: l.LastSeen,
	}
	var score float64
	switch v := l.EvalScore.(type) {
	case float64:
		if v > 0 {
			score = v
		}
	case int64:
		if v > 0 {
			score = float64(v)
		}
	}
	return listing, score
}

// EmailTemplates parses and returns the email template set for use by the notifier.
// ProxyImage fetches an external image URL, caches it in SQLite, and serves it.
// Path: /images/{hash} where hash = SHA-256 hex of the original URL (passed as ?url=).
// Templates call proxyURL(rawURL) to get the /images/<hash> path.
func (h *Handler) ProxyImage(w http.ResponseWriter, r *http.Request) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		http.NotFound(w, r)
		return
	}

	// Check cache first.
	row, err := h.queries.GetImageCache(r.Context(), rawURL)
	if err == nil {
		w.Header().Set("Content-Type", row.ContentType)
		w.Header().Set("Cache-Control", "public, max-age=604800")
		w.Write(row.Data)
		return
	}

	// Fetch from origin.
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		http.Error(w, "bad url", http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		http.Error(w, "fetch failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2 MB max
	if err != nil {
		http.Error(w, "read failed", http.StatusBadGateway)
		return
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "image/jpeg"
	}

	// Store in cache (best-effort).
	h.queries.SetImageCache(r.Context(), dbgen.SetImageCacheParams{
		Url: rawURL, Data: data, ContentType: ct,
	})

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Write(data)
}

// ProxyURL returns the local proxy path for an external image URL.
func ProxyURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	h := sha256.Sum256([]byte(rawURL))
	return fmt.Sprintf("/images/%s?url=%s", hex.EncodeToString(h[:]), rawURL)
}

func (h *Handler) EmailTemplates() *template.Template {
	sub, err := fs.Sub(web.TemplatesFS, "templates")
	if err != nil {
		return nil
	}
	t, err := template.New("").ParseFS(sub, "email/*.html")
	if err != nil {
		return nil
	}
	return t
}

func (h *Handler) DebugEmailDigest(w http.ResponseWriter, r *http.Request) {
	tmpl := h.EmailTemplates()
	if tmpl == nil {
		http.Error(w, "email templates unavailable", 500)
		return
	}
	data := map[string]any{
		"Date": "Tuesday, 6 May 2026",
		"Searches": []map[string]any{
			{
				"Name":     "NEC PC8801 Keyboard",
				"NewCount": 3,
				"TopListings": []notifier.ListingView{
					{Title: "NEC PC-8801mkIISR キーボード ジャンク【20", URL: "#", DarklyURL: "#", Platform: "buyee", Price: "¥5,060", Score: 7.5, Reasoning: "Strong match for the PC-8801mkIISR. Junk condition raises questions, but price is very reasonable.", EndTime: "Wed 7 May 8:30pm", EndingSoon: true, Condition: "junk"},
					{Title: "NEC PC-8801 FH FA FE MH MA MA2 MC VA VA2 VA3用 TYPE A キーボード 中古", URL: "#", DarklyURL: "#", Platform: "buyee", Price: "¥19,800", Score: 7.5, Reasoning: "Type A keyboard covering multiple PC-8801 variants. Used condition is preferable to junk.", EndTime: "Thu 8 May 9:30am", Condition: "used"},
					{Title: "希少!! 通電OK NEC PC-8801 キーボード 当時物 昭和レトロ", URL: "#", Platform: "buyee", Price: "¥15,780", Score: 6.5, Reasoning: "Powers on. Original PC-8801 (not mkII), so switch variant uncertain.", EndTime: "Wed 7 May 8:30pm"},
				},
			},
			{
				"Name":     "Vintage Keyboards",
				"NewCount": 1,
				"TopListings": []notifier.ListingView{
					{Title: "IBM Model M Space Saver — Near Mint w/ original box", URL: "#", DarklyURL: "#", Platform: "ebay", Price: "A$280", Score: 8.5, Reasoning: "Near-mint with original box is rare. Price is high but fair for condition. Local Melbourne seller.", Location: "Melbourne, VIC", Condition: "near mint"},
				},
			},
		},
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "digest.html", data); err != nil {
		h.log.Error("debug digest template", "error", err)
	}
}

func (h *Handler) DebugEmailUrgent(w http.ResponseWriter, r *http.Request) {
	tmpl := h.EmailTemplates()
	if tmpl == nil {
		http.Error(w, "email templates unavailable", 500)
		return
	}
	data := map[string]any{
		"Listings": []notifier.ListingView{
			{Title: "NEC PC-8801mkIISR キーボード ジャンク【20", URL: "#", DarklyURL: "#", Platform: "buyee", Price: "¥5,060", EndTime: "Wed 7 May 8:30pm", EndingSoon: true},
			{Title: "希少!! 通電OK NEC PC-8801 キーボード 当時物 昭和レトロ 激レア", URL: "#", Platform: "buyee", Price: "¥15,780", EndTime: "Wed 7 May 8:30pm", EndingSoon: true},
		},
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "urgent.html", data); err != nil {
		h.log.Error("debug urgent template", "error", err)
	}
}
