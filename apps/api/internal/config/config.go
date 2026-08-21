// Package config holds what a process needs before it can reach anything:
// where the database is, and how to recognise a user.
//
// Nothing else. Routes live in the database, head units are linked through the
// UI, and every credential comes from the environment. If something belongs to
// a person or a device, it does not belong here.
package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/wncservices/domestique/apps/api/internal/auth"
	"github.com/wncservices/domestique/apps/api/internal/crew"
	"github.com/wncservices/domestique/apps/api/internal/model"
)

// SourceConfig is where the route library lives.
//
// One field, because there is one kind of library: a database. A postgres://
// URL means PostgreSQL, anything else is a SQLite file path.
type SourceConfig struct {
	DSN string `yaml:"dsn,omitempty"`
}

// KomootConfig enables importing routes from a Komoot account.
//
// The credentials are NOT here: they come from KOMOOT_EMAIL and
// KOMOOT_PASSWORD in the environment, sourced from Vault in a cluster. This
// file is committed-adjacent and must never hold a password.
type KomootConfig struct {
	Enabled bool `yaml:"enabled"`
	// IncludeRecorded also imports rides you have ridden, not just routes
	// you have plotted. Usually not what you want on a head unit.
	IncludeRecorded bool `yaml:"include_recorded,omitempty"`
}

// WahooConfig is how riders connect their own Wahoo account.
//
// The client id/secret are NOT here, same rule as Komoot's credentials:
// WAHOO_CLIENT_ID and WAHOO_CLIENT_SECRET come from the environment,
// sourced from Vault in a cluster. RedirectURL is not a secret — it must
// equal, exactly, what is registered with Wahoo — so it belongs in a config
// file meant to be readable, the same way auth.oidc.redirect_url does.
type WahooConfig struct {
	RedirectURL string `yaml:"redirect_url,omitempty"`
}

// Config is domestique.yaml.
//
// Deliberately small. Accounts are not here: they are linked by riders through
// the UI and live in the database, so there is no second place where somebody's
// devices are written down and no way for the two to disagree. What is left is
// what a process needs before it can reach anything — where the database is,
// and how to recognise a user.
// WebConfig is how the frontend is served.
type WebConfig struct {
	// LandingHost gets the logged-out page instead of the app — the apex,
	// while the app lives behind the proxy on a subdomain. Empty serves the
	// app to everyone, which is what a laptop wants.
	LandingHost string `yaml:"landing_host,omitempty"`
}

type Config struct {
	Source SourceConfig `yaml:"source"`
	Web    WebConfig    `yaml:"web"`
	Auth   auth.Config  `yaml:"auth"`
	Komoot KomootConfig `yaml:"komoot"`
	Wahoo  WahooConfig  `yaml:"wahoo"`
}

// DefaultDSN is where a database library lives unless configured otherwise.
const DefaultDSN = "data/domestique.db"

// EnvSourceDSN overrides source.dsn from the environment, so a password never
// has to be written into the config file.
const EnvSourceDSN = "DOMESTIQUE_SOURCE_DSN"

// Load reads a config file. A missing file is not an error: the defaults (a
// database library, no accounts) are enough to start uploading routes.
func Load(path string) (*Config, error) {
	cfg := &Config{Source: SourceConfig{DSN: DefaultDSN}}

	// #nosec G304 -- the config path is operator configuration, not user input.
	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		// No file is fine. Fall through: the environment may still configure
		// the source, which is the normal case in a container.
	case err != nil:
		return nil, err
	default:
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("%s: invalid YAML: %w", path, err)
		}
	}

	cfg.applyDefaults()
	return cfg, cfg.Validate()
}

// applyDefaults fills in what the file left out and lets the environment
// override the source.
//
// This runs whether or not a config file exists. It used to run only after a
// successful read, which meant DOMESTIQUE_SOURCE_DSN was ignored in a
// container with no config file — precisely the case it is for.
func (c *Config) applyDefaults() {
	// A PostgreSQL DSN carries a password, so a deployment supplies it through
	// the environment rather than writing it into a config file that wants to
	// be readable. Same Vault -> envFrom path as every other credential.
	if dsn := os.Getenv(EnvSourceDSN); dsn != "" {
		c.Source.DSN = dsn
	}
	if c.Source.DSN == "" {
		c.Source.DSN = DefaultDSN
	}
}

// Validate checks a config for self-consistency. It is exported because CLI
// flags can rewrite the source after Load.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Source.DSN) == "" {
		return fmt.Errorf("source.dsn is empty: nowhere to keep routes")
	}

	// Surfaces a bad auth config at startup rather than on the first request.
	if _, err := auth.New(c.Auth); err != nil {
		return err
	}
	return nil
}

// TargetsFor returns the accounts a route should be pushed to.
//
// Targets holds crew ids, not raw account ids — see internal/crew's package
// doc for why. A route with no targets reaches the owner's own accounts
// only: the useful default now that a library can be shared beyond one
// household, and the fix for what nil used to mean (every linked account,
// system-wide, with no consent from whoever owned the other accounts — see
// internal/crew.go). A route naming crews reaches the owner's own accounts
// plus every account belonging to a rider who is *currently* an approved
// member of one of those crews — resolved fresh on every call, never
// stored, so a membership change takes effect on the very next push without
// anyone touching the route.
func TargetsFor(r model.Route, linked []model.Account, crews crew.Snapshot) []string {
	if r.Targets != nil && len(*r.Targets) == 0 {
		return nil // explicit: nowhere
	}

	own := accountsForRider(r.Owner, linked)
	if r.Targets == nil {
		return own
	}

	set := make(map[string]bool, len(own))
	for _, id := range own {
		set[id] = true
	}
	for _, t := range *r.Targets {
		if !crews.ApprovedRiders.Has(t, r.Owner) {
			// Stale (the crew was deleted or the owner left it), foreign (a
			// crew the owner never belonged to), or a raw account id from
			// before crews existed — none of these ever resolve to a push.
			continue
		}
		for _, rider := range crews.ApprovedRiders[t] {
			for _, id := range accountsForRider(rider, linked) {
				set[id] = true
			}
		}
	}
	return sortedSetKeys(set)
}

// VisibleTo reports whether rider may see r in their own library — their
// own route, or one shared to a crew both the owner and rider currently,
// approvedly, belong to. The same relationship TargetsFor resolves a push
// through, applied to reading instead of pushing: a route that would never
// reach this rider's own devices should not show up in their library
// either. Found live: a rider with no crew membership and nothing shared
// could still see every route in the deployment, owned by riders they had
// no relationship with at all — reading had no analogue of the consent
// crews already require for a push.
//
// An admin is not handled here — every caller checks auth.PermEditAny
// first and skips this entirely for one, the same bypass CanEditRoute
// gives them for writes.
func VisibleTo(r model.Route, rider string, crews crew.Snapshot) bool {
	if strings.EqualFold(r.Owner, rider) {
		return true
	}
	if r.Targets == nil {
		// No explicit sharing choice reaches only the owner's own accounts
		// (see TargetsFor) — reading follows the same default.
		return false
	}
	for _, t := range *r.Targets {
		if crews.ApprovedRiders.Has(t, r.Owner) && crews.ApprovedRiders.Has(t, rider) {
			return true
		}
	}
	return false
}

// AccountVisibleTo reports whether rider may see account — their own, or a
// crew fellow's. Mirrors VisibleTo's own reasoning: sharing any crew with
// the account's owner already means a route can reach it and its sync
// status shows up alongside that route (RouteCard.vue's own accountFor
// lookup), so recognising whose device that is is not new information —
// hiding the label here would not close anything a shared route does not
// already reveal. A rider outside every one of the owner's crews learns
// nothing about it at all — see handleAccounts, the bug this closed: every
// account in the deployment was listed to anyone with routes:read, the
// lowest permission tier there is.
func AccountVisibleTo(account model.Account, rider string, crews crew.Snapshot) bool {
	if strings.EqualFold(account.Rider, rider) {
		return true
	}
	for _, c := range crews.Crews {
		if crews.ApprovedRiders.Has(c.ID, account.Rider) && crews.ApprovedRiders.Has(c.ID, rider) {
			return true
		}
	}
	return false
}

// UnknownTargets reports targets naming a crew the route's owner does not
// currently, approvedly, belong to — a crew since deleted, one the owner
// left, or (from before crews existed) a raw account id — so the UI can
// show a route that will never sync rather than leaving it silent.
func UnknownTargets(r model.Route, crews crew.Snapshot) []string {
	if r.Targets == nil {
		return nil
	}

	var unknown []string
	for _, t := range *r.Targets {
		if !crews.ApprovedRiders.Has(t, r.Owner) {
			unknown = append(unknown, t)
		}
	}
	return unknown
}

// accountsForRider is every linked account belonging to one rider — never
// matching when rider is empty, since a linked account's Rider is never
// empty: an ownerless route resolves to nobody rather than to everybody,
// the safe direction for a route this package cannot attribute to anyone.
func accountsForRider(rider string, linked []model.Account) []string {
	if rider == "" {
		return nil
	}
	var out []string
	for _, a := range linked {
		if strings.EqualFold(a.Rider, rider) {
			out = append(out, a.ID)
		}
	}
	return out
}

func sortedSetKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
