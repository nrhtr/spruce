package gumtree

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/nrhtr/spruce/internal/platform"
)

const baseURL = "https://www.gumtree.com.au"

type Platform struct {
	httpClient *http.Client
	chromePath string
}

func New() *Platform {
	return &Platform{
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
		chromePath: os.Getenv("CHROMIUM_PATH"),
	}
}

func (p *Platform) Name() string { return "gumtree" }

func (p *Platform) get(ctx context.Context, rawURL string) (*goquery.Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-AU,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")

	jitter := time.Duration(rand.Intn(2000)+1000) * time.Millisecond
	time.Sleep(jitter)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gumtree get %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("gumtree: blocked (status %d) — site may require headless browser", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gumtree %s: status %d: %s", rawURL, resp.StatusCode, b[:min(len(b), 200)])
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	// Detect CAPTCHA or redirect-to-login pages.
	if isCaptchaPage(doc) {
		return nil, fmt.Errorf("gumtree: CAPTCHA detected — set CHROMIUM_PATH for headless fallback")
	}

	return doc, nil
}

func isCaptchaPage(doc *goquery.Document) bool {
	text := strings.ToLower(doc.Find("title").Text())
	return strings.Contains(text, "captcha") || strings.Contains(text, "verify") ||
		doc.Find("form[action*='captcha']").Length() > 0
}

func (p *Platform) Search(ctx context.Context, q platform.Query) ([]platform.Listing, error) {
	searchURL := fmt.Sprintf("%s/s-all-jobs/k0?q=%s&sort=date",
		baseURL, url.QueryEscape(q.Keywords))

	doc, err := p.get(ctx, searchURL)
	if err != nil {
		return nil, err
	}

	var listings []platform.Listing
	base, _ := url.Parse(baseURL)

	// Gumtree listing cards — selectors may need updating if site changes.
	doc.Find("article.user-ad-collection-new-design-ad, li.user-ad-row, [data-testid='listing-ad']").Each(func(_ int, s *goquery.Selection) {
		l := platform.Listing{
			Platform: "gumtree",
		}

		// URL.
		href, _ := s.Find("a[href]").First().Attr("href")
		if href == "" {
			return
		}
		ref, err := url.Parse(href)
		if err != nil {
			return
		}
		l.URL = base.ResolveReference(ref).String()

		// External ID from URL path (last numeric segment).
		parts := strings.Split(strings.TrimRight(l.URL, "/"), "/")
		for i := len(parts) - 1; i >= 0; i-- {
			if _, err := strconv.ParseInt(parts[i], 10, 64); err == nil {
				l.ExternalID = parts[i]
				break
			}
		}
		if l.ExternalID == "" {
			l.ExternalID = parts[len(parts)-1]
		}

		// Title.
		l.Title = strings.TrimSpace(s.Find("[class*='title'], h2, h3").First().Text())

		// Price.
		priceText := strings.TrimSpace(s.Find("[class*='price'], [data-testid='price']").First().Text())
		if price := parseAUPrice(priceText); price != nil {
			l.Price = price
			l.Currency = "AUD"
		}

		// Location.
		l.Location = strings.TrimSpace(s.Find("[class*='location'], [class*='suburb']").First().Text())

		// Image.
		imgSrc, _ := s.Find("img").First().Attr("src")
		if imgSrc == "" {
			imgSrc, _ = s.Find("img").First().Attr("data-src")
		}
		if imgSrc != "" {
			l.ImageURLs = []string{imgSrc}
		}

		raw, _ := json.Marshal(map[string]string{
			"source_url": l.URL,
			"raw_price":  priceText,
		})
		l.RawData = json.RawMessage(raw)

		listings = append(listings, l)
	})

	return listings, nil
}

func (p *Platform) GetListing(ctx context.Context, externalID string) (*platform.Listing, error) {
	// Gumtree listing URLs include slugs; we can't reconstruct from ID alone.
	// Return nil to indicate we should rely on stored URL instead.
	return nil, fmt.Errorf("gumtree: GetListing not supported; use stored URL")
}

func parseAUPrice(s string) *float64 {
	s = strings.NewReplacer("$", "", ",", "", " ", "", "AU", "").Replace(strings.TrimSpace(s))
	if s == "" || strings.EqualFold(s, "please contact") || strings.EqualFold(s, "swap/trade") {
		return nil
	}
	// Handle ranges like "100 - 200" — take lower bound.
	if idx := strings.Index(s, "-"); idx > 0 {
		s = s[:idx]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &f
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
