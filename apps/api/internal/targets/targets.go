// Package targets holds the provider adapters.
//
// Each target owns one account. Adapters are intentionally dumb: the diff
// engine decides what to do, the adapter only knows how to talk to its provider.
package targets

import (
	"fmt"

	"github.com/wncservices/domestique/apps/api/internal/model"
)

// Target is one rider's account on one provider.
type Target interface {
	// Create pushes a new route and returns the provider's id for it.
	Create(route model.Route) (string, error)
	// Update replaces an existing route and returns the (possibly new) id.
	Update(remoteID string, route model.Route) (string, error)
	// Delete removes a route from the account.
	Delete(remoteID string) error
}

// Implemented reports whether pushes to a provider actually work yet, so the
// UI can say "not wired up" rather than offering a push that always fails.
// Flip these as Phases 3 and 4 land; see garmin.go and wahoo.go.
func Implemented(p model.Provider) bool {
	switch p {
	case model.ProviderGarmin:
		return false
	case model.ProviderWahoo:
		return false
	default:
		return false
	}
}

// Build returns the adapter for an account.
func Build(account model.Account) (Target, error) {
	switch account.Provider {
	case model.ProviderGarmin:
		return &Garmin{Account: account}, nil
	case model.ProviderWahoo:
		return &Wahoo{Account: account}, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", account.Provider)
	}
}
