package targets

import (
	"errors"

	"github.com/wncservices/domestique/apps/api/internal/model"
)

// Wahoo is the Wahoo Cloud API adapter — Phase 4, not implemented yet.
//
// Unlike Garmin, the API is documented and clean (https://cloud-api.wahooligan.com/):
//
//	POST   /v1/routes          scope routes_write
//	PUT    /v1/routes/:id      scope routes_write
//	DELETE /v1/routes/:id      scope routes_write
//	GET    /v1/routes[/:id]    scope routes_read
//
// Create requires: route[file] (base64-encoded FIT, not GPX),
// route[external_id], route[provider_updated_at], route[name],
// route[workout_type_family_id], route[start_lat], route[start_lng],
// route[distance], route[ascent].
//
// Two things gate this adapter:
//
//  1. API access is approval-gated — no self-serve client id/secret. Request it
//     at developers.wahooligan.com/cloud before building anything here.
//  2. FIT generation (Phase 2) must land first; the API will not take a GPX.
//
// external_id should be the library slug, so a rebuilt state file can be
// reconciled against what the account already holds.
type Wahoo struct {
	Account model.Account
}

var errWahooUnimplemented = errors.New(
	"wahoo push is Phase 4: needs an approved API client and FIT conversion (Phase 2)")

func (w *Wahoo) Create(model.Route) (string, error)         { return "", errWahooUnimplemented }
func (w *Wahoo) Update(string, model.Route) (string, error) { return "", errWahooUnimplemented }
func (w *Wahoo) Delete(string) error                        { return errWahooUnimplemented }
