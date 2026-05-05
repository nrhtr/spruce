package notifier

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/nrhtr/darkly/internal/config"
	dbgen "github.com/nrhtr/darkly/internal/db/generated"
)

type Notifier struct {
	queries   *dbgen.Queries
	cfg       *config.Config
	emailTmpl *template.Template
	log       *slog.Logger
	loc       *time.Location
}

type DigestData struct {
	Date    string
	Searches []SearchDigest
}

type SearchDigest struct {
	Name        string
	NewCount    int
	TopListings []ListingView
}

type ListingView struct {
	Title     string
	URL       string
	Platform  string
	Price     string
	Score     float64
	Reasoning string
	EndTime   string
	EndingSoon bool
	Condition string
	Location  string
	ImageURL  string
}

func New(queries *dbgen.Queries, cfg *config.Config, log *slog.Logger) *Notifier {
	loc, err := time.LoadLocation(cfg.DigestTZ)
	if err != nil {
		loc = time.UTC
	}
	return &Notifier{
		queries: queries,
		cfg:     cfg,
		log:     log,
		loc:     loc,
	}
}

func (n *Notifier) SetTemplates(tmpl *template.Template) {
	n.emailTmpl = tmpl
}

func (n *Notifier) SendDigest(ctx context.Context) error {
	if n.cfg.EmailTo == "" {
		n.log.Warn("no email recipient configured, skipping digest")
		return nil
	}

	lastSentAt := int64(0)
	row, err := n.queries.GetLastNotificationSentAt(ctx, "digest")
	if err == nil {
		lastSentAt = row
	}
	since := time.Unix(lastSentAt, 0)

	searches, err := n.queries.ListActiveSearches(ctx)
	if err != nil {
		return fmt.Errorf("list searches: %w", err)
	}

	data := DigestData{
		Date: time.Now().In(n.loc).Format("Monday, 2 January 2006"),
	}

	for _, s := range searches {
		newListings, err := n.queries.ListNewSince(ctx, dbgen.ListNewSinceParams{
			SearchID:  s.ID,
			FirstSeen: since.Unix(),
		})
		if err != nil {
			n.log.Error("list new since", "search_id", s.ID, "error", err)
			continue
		}

		topEvals, err := n.queries.ListEvaluationsBySearch(ctx, dbgen.ListEvaluationsBySearchParams{
			SearchID: s.ID,
			Score:    6.0,
			Limit:    10,
		})
		if err != nil {
			n.log.Error("list top evals", "search_id", s.ID, "error", err)
		}

		sd := SearchDigest{
			Name:     s.Name,
			NewCount: len(newListings),
		}

		for _, e := range topEvals {
			lv := ListingView{
				Title:     e.Title,
				URL:       e.Url,
				Platform:  e.Platform,
				Score:     e.Score,
				Reasoning: e.Reasoning,
				Condition: e.Condition,
				Location:  e.Location,
			}
			if e.Price.Valid {
				lv.Price = formatPrice(e.Price.Float64, e.Currency)
			}
			if e.EndTime.Valid {
				t := time.Unix(e.EndTime.Int64, 0).In(n.loc)
				lv.EndTime = t.Format("Mon 2 Jan 3:04pm")
				lv.EndingSoon = time.Until(t) < 24*time.Hour
			}
			var imgURLs []string
			if err := json.Unmarshal([]byte(e.ImageUrls), &imgURLs); err == nil && len(imgURLs) > 0 {
				lv.ImageURL = imgURLs[0]
			}
			sd.TopListings = append(sd.TopListings, lv)
		}

		data.Searches = append(data.Searches, sd)
	}

	subject := fmt.Sprintf("Darkly Digest — %s", data.Date)
	body, err := n.renderTemplate("email/digest.html", data)
	if err != nil {
		return fmt.Errorf("render digest: %w", err)
	}

	if err := n.send(ctx, subject, body); err != nil {
		return fmt.Errorf("send digest: %w", err)
	}

	listingIDs := "[]"
	if _, err := n.queries.CreateEmailNotification(ctx, dbgen.CreateEmailNotificationParams{
		Kind:       "digest",
		Subject:    subject,
		BodyHtml:   body,
		ListingIds: listingIDs,
	}); err != nil {
		n.log.Error("record notification", "error", err)
	}

	n.log.Info("digest sent", "to", n.cfg.EmailTo)
	return nil
}

func (n *Notifier) CheckUrgent(ctx context.Context) error {
	if n.cfg.EmailTo == "" {
		return nil
	}

	threshold := sql.NullInt64{Int64: time.Now().Add(n.cfg.UrgentThreshold).Unix(), Valid: true}
	endingSoon, err := n.queries.ListEndingSoon(ctx, threshold)
	if err != nil {
		return err
	}
	if len(endingSoon) == 0 {
		return nil
	}

	// Check if we've sent an urgent email recently (within 2h).
	lastSentAt := int64(0)
	row, err := n.queries.GetLastNotificationSentAt(ctx, "urgent")
	if err == nil {
		lastSentAt = row
	}
	if time.Since(time.Unix(lastSentAt, 0)) < 2*time.Hour {
		return nil
	}

	// Filter to only listings with no bids.
	var actionable []dbgen.Listing
	for _, l := range endingSoon {
		count, err := n.queries.HasBidForListing(ctx, l.ID)
		if err == nil && count == 0 {
			actionable = append(actionable, l)
		}
	}
	if len(actionable) == 0 {
		return nil
	}

	type urgentData struct {
		Listings []ListingView
	}

	data := urgentData{}
	for _, l := range actionable {
		lv := ListingView{
			Title:    l.Title,
			URL:      l.Url,
			Platform: l.Platform,
			Location: l.Location,
		}
		if l.Price.Valid {
			lv.Price = formatPrice(l.Price.Float64, l.Currency)
		}
		if l.EndTime.Valid {
			t := time.Unix(l.EndTime.Int64, 0).In(n.loc)
			lv.EndTime = t.Format("Mon 2 Jan 3:04pm")
			lv.EndingSoon = true
		}
		data.Listings = append(data.Listings, lv)
	}

	subject := fmt.Sprintf("⚠️ Darkly Alert — %d auction(s) ending soon", len(actionable))
	body, err := n.renderTemplate("email/urgent.html", data)
	if err != nil {
		return fmt.Errorf("render urgent: %w", err)
	}

	if err := n.send(ctx, subject, body); err != nil {
		return fmt.Errorf("send urgent: %w", err)
	}

	if _, err := n.queries.CreateEmailNotification(ctx, dbgen.CreateEmailNotificationParams{
		Kind:       "urgent",
		Subject:    subject,
		BodyHtml:   body,
		ListingIds: "[]",
	}); err != nil {
		n.log.Error("record urgent notification", "error", err)
	}

	n.log.Info("urgent alert sent", "count", len(actionable))
	return nil
}

func (n *Notifier) send(ctx context.Context, subject, htmlBody string) error {
	msg := fmt.Sprintf("To: %s\r\nFrom: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		n.cfg.EmailTo, n.cfg.EmailFrom, subject, htmlBody)

	cmd := exec.CommandContext(ctx, "/usr/sbin/sendmail", "-t")
	cmd.Stdin = strings.NewReader(msg)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sendmail: %w: %s", err, stderr.String())
	}
	return nil
}

func (n *Notifier) renderTemplate(name string, data any) (string, error) {
	if n.emailTmpl == nil {
		return fmt.Sprintf("<html><body><pre>%v</pre></body></html>", data), nil
	}
	var buf bytes.Buffer
	if err := n.emailTmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func formatPrice(price float64, currency string) string {
	switch currency {
	case "AUD":
		return fmt.Sprintf("A$%.2f", price)
	case "JPY":
		return fmt.Sprintf("¥%.0f", price)
	case "USD":
		return fmt.Sprintf("US$%.2f", price)
	case "GBP":
		return fmt.Sprintf("£%.2f", price)
	default:
		return fmt.Sprintf("%.2f %s", price, currency)
	}
}

// formatPriceFromNullable is exported for use in templates.
func FormatPrice(price sql.NullFloat64, currency string) string {
	if !price.Valid {
		return ""
	}
	return formatPrice(price.Float64, currency)
}
