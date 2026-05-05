package buyee

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/nrhtr/darkly/internal/platform"
)

const baseURL = "https://buyee.jp"

type Platform struct {
	httpClient *http.Client
}

func New() *Platform {
	return &Platform{
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (p *Platform) Name() string { return "buyee" }

func (p *Platform) get(ctx context.Context, rawURL string) (*goquery.Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	// Polite delay.
	jitter := time.Duration(rand.Intn(1500)+500) * time.Millisecond
	time.Sleep(jitter)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("buyee get %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("buyee %s: status %d: %s", rawURL, resp.StatusCode, b[:min(len(b), 200)])
	}

	return goquery.NewDocumentFromReader(resp.Body)
}

func (p *Platform) Search(ctx context.Context, q platform.Query) ([]platform.Listing, error) {
	searchURL := fmt.Sprintf("%s/item/search/query/%s?translationType=1",
		baseURL, url.PathEscape(q.Keywords))

	if q.MinPrice != nil {
		searchURL += fmt.Sprintf("&price_min=%.0f", *q.MinPrice)
	}
	if q.MaxPrice != nil {
		searchURL += fmt.Sprintf("&price_max=%.0f", *q.MaxPrice)
	}

	doc, err := p.get(ctx, searchURL)
	if err != nil {
		return nil, err
	}

	var listings []platform.Listing

	doc.Find(".g-item-list .g-item, .itemList .item, [class*='itemCard'], [class*='g-item']").Each(func(_ int, s *goquery.Selection) {
		l := platform.Listing{
			Platform: "buyee",
		}

		// URL and external ID.
		href, _ := s.Find("a").First().Attr("href")
		if href == "" {
			href, _ = s.Attr("href")
		}
		if href != "" {
			if !strings.HasPrefix(href, "http") {
				href = baseURL + href
			}
			l.URL = href
			// Extract external ID from path like /item/yahoo/auction/xxxxxx
			parts := strings.Split(strings.TrimRight(href, "/"), "/")
			if len(parts) > 0 {
				l.ExternalID = parts[len(parts)-1]
			}
		}

		if l.ExternalID == "" {
			return
		}

		// Title.
		l.Title = strings.TrimSpace(s.Find("[class*='name'], [class*='title'], .g-item-name").First().Text())
		if l.Title == "" {
			l.Title = strings.TrimSpace(s.Find("a").First().Text())
		}

		// Price (JPY).
		priceText := strings.TrimSpace(s.Find("[class*='price']").First().Text())
		priceText = strings.NewReplacer("¥", "", ",", "", " ", "").Replace(priceText)
		if f, err := strconv.ParseFloat(priceText, 64); err == nil {
			l.Price = &f
			l.Currency = "JPY"
		}

		// Image.
		imgSrc, _ := s.Find("img").First().Attr("src")
		if imgSrc != "" {
			l.ImageURLs = []string{imgSrc}
		}

		// End time (best-effort).
		endText := strings.TrimSpace(s.Find("[class*='time'], [class*='end']").First().Text())
		if t := parseJPTime(endText); t != nil {
			l.EndTime = t
		}

		raw, _ := json.Marshal(map[string]string{
			"raw_title":  l.Title,
			"raw_price":  priceText,
			"source_url": l.URL,
		})
		l.RawData = json.RawMessage(raw)

		listings = append(listings, l)
	})

	return listings, nil
}

func (p *Platform) GetListing(ctx context.Context, externalID string) (*platform.Listing, error) {
	listingURL := fmt.Sprintf("%s/item/yahoo/auction/%s", baseURL, externalID)
	doc, err := p.get(ctx, listingURL)
	if err != nil {
		return nil, err
	}

	l := &platform.Listing{
		ExternalID: externalID,
		Platform:   "buyee",
		URL:        listingURL,
	}

	l.Title = strings.TrimSpace(doc.Find("h1, [class*='title']").First().Text())
	l.Description = strings.TrimSpace(doc.Find("[class*='description'], [class*='detail']").First().Text())

	priceText := strings.TrimSpace(doc.Find("[class*='price']").First().Text())
	priceText = strings.NewReplacer("¥", "", ",", "", " ", "").Replace(priceText)
	if f, err := strconv.ParseFloat(priceText, 64); err == nil {
		l.Price = &f
		l.Currency = "JPY"
	}

	doc.Find("img[src*='auctions']").Each(func(_ int, s *goquery.Selection) {
		if src, ok := s.Attr("src"); ok {
			l.ImageURLs = append(l.ImageURLs, src)
		}
	})

	raw, _ := json.Marshal(map[string]string{"source_url": listingURL})
	l.RawData = json.RawMessage(raw)

	return l, nil
}

func parseJPTime(s string) *time.Time {
	formats := []string{
		"2006-01-02 15:04",
		"01/02 15:04",
		"1/2 15:04",
	}
	s = strings.TrimSpace(s)
	for _, f := range formats {
		t, err := time.ParseInLocation(f, s, time.FixedZone("JST", 9*3600))
		if err == nil {
			// If no year, assume current or next year.
			if t.Year() == 0 {
				now := time.Now()
				t = t.AddDate(now.Year(), 0, 0)
				if t.Before(now) {
					t = t.AddDate(1, 0, 0)
				}
			}
			return &t
		}
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
