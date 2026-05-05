package platform

import (
	"context"
	"encoding/json"
	"time"
)

type Query struct {
	Keywords    string
	Description string
	MinPrice    *float64
	MaxPrice    *float64
	Currency    string
	Location    *string
}

type Listing struct {
	ExternalID  string
	Platform    string
	Title       string
	Description string
	Price       *float64
	Currency    string
	URL         string
	ImageURLs   []string
	EndTime     *time.Time
	Condition   string
	Location    string
	RawData     json.RawMessage
}

type Platform interface {
	Name() string
	Search(ctx context.Context, q Query) ([]Listing, error)
	GetListing(ctx context.Context, externalID string) (*Listing, error)
}
