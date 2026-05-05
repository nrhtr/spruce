package facebook

import (
	"context"
	"errors"

	"github.com/nrhtr/darkly/internal/platform"
)

// Platform is a stub. Facebook Marketplace has no public API.
// Scraping requires authenticated sessions, violates Meta ToS,
// and results in rapid account bans. This type satisfies the
// Platform interface but always returns an explanatory error.
type Platform struct{}

func New() *Platform { return &Platform{} }

func (p *Platform) Name() string { return "facebook" }

func (p *Platform) Search(_ context.Context, _ platform.Query) ([]platform.Listing, error) {
	return nil, errors.New("facebook marketplace: disabled — no public API; scraping violates Meta ToS")
}

func (p *Platform) GetListing(_ context.Context, _ string) (*platform.Listing, error) {
	return nil, errors.New("facebook marketplace: disabled")
}
