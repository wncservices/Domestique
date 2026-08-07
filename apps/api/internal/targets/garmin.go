package targets

import (
	"errors"

	"github.com/wncservices/domestique/apps/api/internal/model"
)

// Garmin is the Garmin Connect adapter — Phase 3, not implemented yet.
//
// There is no self-serve Garmin API. The official Courses API is part of the
// Connect Developer Program (commercial partners only), so this adapter is
// planned against the unofficial Connect web session:
//
//   - auth: the Garmin SSO flow (SSO + MFA; tokens last roughly a year, then a
//     manual re-auth is needed — surface that as a metric rather than
//     discovering it mid-ride). The Python `garth` library is the clearest
//     reference implementation of the handshake.
//   - upload: the call Connect's own Training -> Courses -> Import button makes.
//     Confirm the exact course-service path with devtools before wiring it; it
//     is undocumented and moves.
//
// This is grey-area and can break on any Garmin deploy. Acceptable for two
// personal accounts, not for anything shared more widely.
type Garmin struct {
	Account model.Account
}

var errGarminUnimplemented = errors.New(
	"garmin push is Phase 3: verify the course-service endpoint and wire the SSO flow first")

func (g *Garmin) Create(model.Route) (string, error)         { return "", errGarminUnimplemented }
func (g *Garmin) Update(string, model.Route) (string, error) { return "", errGarminUnimplemented }
func (g *Garmin) Delete(string) error                        { return errGarminUnimplemented }
