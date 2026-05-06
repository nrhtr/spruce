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
	"github.com/nrhtr/spruce/internal/platform"
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

	doc.Find("li.itemCard").Each(func(_ int, s *goquery.Selection) {
		l := platform.Listing{Platform: "buyee"}

		// URL and external ID from the item name link.
		href, _ := s.Find("div.itemCard__itemName a, div.g-thumbnail__outer a").First().Attr("href")
		if href == "" {
			return
		}
		if !strings.HasPrefix(href, "http") {
			href = baseURL + href
		}
		l.URL = href
		parts := strings.Split(strings.TrimRight(href, "/"), "/")
		l.ExternalID = parts[len(parts)-1]
		if l.ExternalID == "" {
			return
		}

		// Title.
		l.Title = strings.TrimSpace(s.Find("div.itemCard__itemName a").First().Text())

		// Price: first span.g-price = current price, format "6,000 YEN".
		priceText := strings.TrimSpace(s.Find("span.g-price").First().Text())
		if price := parseJPYPrice(priceText); price != nil {
			l.Price = price
			l.Currency = "JPY"
		}

		// Image: data-src on lazy-loaded thumbnail.
		imgSrc, _ := s.Find("img.lazyLoadV2").First().Attr("data-src")
		if imgSrc == "" {
			imgSrc, _ = s.Find("img").First().Attr("src")
		}
		if imgSrc != "" {
			l.ImageURLs = []string{imgSrc}
		}

		// End time from "Time Remaining" info item.
		s.Find("li.itemCard__infoItem").Each(func(_ int, info *goquery.Selection) {
			label := strings.TrimSpace(info.Find("span.g-title").Text())
			if label == "Time Remaining" {
				val := strings.TrimSpace(info.Find("span.g-text").Text())
				if t := parseTimeRemaining(val); t != nil {
					l.EndTime = t
				}
			}
		})

		raw, _ := json.Marshal(map[string]string{
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

// parseJPYPrice parses Buyee price strings like "6,000 YEN" or "13,500 YEN".
func parseJPYPrice(s string) *float64 {
	s = strings.NewReplacer(",", "", " ", "", "YEN", "", "¥", "").Replace(strings.TrimSpace(s))
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &f
}

// parseTimeRemaining converts Buyee "5 day(s)" / "22 hour(s)" to an absolute time.
func parseTimeRemaining(s string) *time.Time {
	s = strings.ToLower(strings.TrimSpace(s))
	var dur time.Duration
	switch {
	case strings.Contains(s, "day"):
		if n, err := strconv.Atoi(strings.Fields(s)[0]); err == nil {
			dur = time.Duration(n) * 24 * time.Hour
		}
	case strings.Contains(s, "hour"):
		if n, err := strconv.Atoi(strings.Fields(s)[0]); err == nil {
			dur = time.Duration(n) * time.Hour
		}
	case strings.Contains(s, "min"):
		if n, err := strconv.Atoi(strings.Fields(s)[0]); err == nil {
			dur = time.Duration(n) * time.Minute
		}
	}
	if dur == 0 {
		return nil
	}
	t := time.Now().Add(dur)
	return &t
}
