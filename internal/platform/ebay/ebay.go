package ebay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nrhtr/darkly/internal/platform"
)

const (
	tokenURL  = "https://api.ebay.com/identity/v1/oauth2/token"
	searchURL = "https://api.ebay.com/buy/browse/v1/item_summary/search"
	itemURL   = "https://api.ebay.com/buy/browse/v1/item/"
)

type Platform struct {
	clientID     string
	clientSecret string
	marketplace  string
	httpClient   *http.Client

	tokenMu   sync.Mutex
	token     string
	tokenExp  time.Time
}

func New(clientID, clientSecret, marketplace string) *Platform {
	return &Platform{
		clientID:     clientID,
		clientSecret: clientSecret,
		marketplace:  marketplace,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (p *Platform) Name() string { return "ebay" }

func (p *Platform) getToken(ctx context.Context) (string, error) {
	p.tokenMu.Lock()
	defer p.tokenMu.Unlock()

	if time.Now().Before(p.tokenExp.Add(-60 * time.Second)) {
		return p.token, nil
	}

	creds := base64.StdEncoding.EncodeToString([]byte(p.clientID + ":" + p.clientSecret))
	body := url.Values{}
	body.Set("grant_type", "client_credentials")
	body.Set("scope", "https://api.ebay.com/oauth/api_scope")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+creds)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ebay token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ebay token error %d: %s", resp.StatusCode, b)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("ebay token decode: %w", err)
	}

	p.token = tok.AccessToken
	p.tokenExp = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return p.token, nil
}

type searchResponse struct {
	ItemSummaries []itemSummary `json:"itemSummaries"`
}

type itemSummary struct {
	ItemID       string `json:"itemId"`
	Title        string `json:"title"`
	ItemWebURL   string `json:"itemWebUrl"`
	ShortDesc    string `json:"shortDescription"`
	Condition    string `json:"condition"`
	ItemEndDate  string `json:"itemEndDate"`
	Price        struct {
		Value    string `json:"value"`
		Currency string `json:"currency"`
	} `json:"price"`
	Image struct {
		ImageURL string `json:"imageUrl"`
	} `json:"image"`
	ItemLocation struct {
		City            string `json:"city"`
		StateOrProvince string `json:"stateOrProvince"`
		Country         string `json:"country"`
	} `json:"itemLocation"`
}

func (p *Platform) Search(ctx context.Context, q platform.Query) ([]platform.Listing, error) {
	if p.clientID == "" {
		return nil, fmt.Errorf("ebay: no credentials configured (set DARKLY_EBAY_CLIENT_ID / DARKLY_EBAY_CLIENT_SECRET)")
	}

	token, err := p.getToken(ctx)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("q", q.Keywords)
	params.Set("limit", "50")
	params.Set("sort", "newlyListed")

	var filters []string
	if q.MinPrice != nil && q.MaxPrice != nil {
		filters = append(filters, fmt.Sprintf("price:[%.2f..%.2f]", *q.MinPrice, *q.MaxPrice))
	} else if q.MinPrice != nil {
		filters = append(filters, fmt.Sprintf("price:[%.2f..]", *q.MinPrice))
	} else if q.MaxPrice != nil {
		filters = append(filters, fmt.Sprintf("price:[..%.2f]", *q.MaxPrice))
	}
	cur := q.Currency
	if cur == "" {
		cur = "AUD"
	}
	filters = append(filters, "priceCurrency:"+cur)
	if len(filters) > 0 {
		params.Set("filter", strings.Join(filters, ","))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-EBAY-C-MARKETPLACE-ID", p.marketplace)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ebay search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ebay search error %d: %s", resp.StatusCode, b)
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var sr searchResponse
	if err := json.Unmarshal(rawBody, &sr); err != nil {
		return nil, fmt.Errorf("ebay search decode: %w", err)
	}

	listings := make([]platform.Listing, 0, len(sr.ItemSummaries))
	for _, item := range sr.ItemSummaries {
		l := platform.Listing{
			ExternalID:  item.ItemID,
			Platform:    "ebay",
			Title:       item.Title,
			Description: item.ShortDesc,
			Currency:    item.Price.Currency,
			URL:         item.ItemWebURL,
			Condition:   item.Condition,
			Location:    strings.TrimSpace(item.ItemLocation.City + " " + item.ItemLocation.StateOrProvince + " " + item.ItemLocation.Country),
		}

		if item.Price.Value != "" {
			var price float64
			fmt.Sscanf(item.Price.Value, "%f", &price)
			l.Price = &price
		}

		if item.Image.ImageURL != "" {
			l.ImageURLs = []string{item.Image.ImageURL}
		}

		if item.ItemEndDate != "" {
			t, err := time.Parse(time.RFC3339, item.ItemEndDate)
			if err == nil {
				l.EndTime = &t
			}
		}

		raw, _ := json.Marshal(item)
		l.RawData = json.RawMessage(raw)
		listings = append(listings, l)
	}

	return listings, nil
}

func (p *Platform) GetListing(ctx context.Context, externalID string) (*platform.Listing, error) {
	token, err := p.getToken(ctx)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, itemURL+url.PathEscape(externalID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-EBAY-C-MARKETPLACE-ID", p.marketplace)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ebay get listing: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ebay get listing error %d: %s", resp.StatusCode, b)
	}

	var item itemSummary
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, err
	}

	l := platform.Listing{
		ExternalID: item.ItemID,
		Platform:   "ebay",
		Title:      item.Title,
		URL:        item.ItemWebURL,
		Currency:   item.Price.Currency,
	}
	if item.Price.Value != "" {
		var price float64
		fmt.Sscanf(item.Price.Value, "%f", &price)
		l.Price = &price
	}
	raw, _ := json.Marshal(item)
	l.RawData = json.RawMessage(raw)
	return &l, nil
}
