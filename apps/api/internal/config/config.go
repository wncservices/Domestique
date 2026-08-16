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
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/wncservices/domestique/apps/api/internal/auth"
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
// A route that names targets goes exactly there — that is what keeps one
// rider's private routes off the other's head unit. A route that names none
// goes to every linked account, which is the useful default for a library two
// people share.
func TargetsFor(r model.Route, linked []model.Account) []string {
	if r.Targets != nil {
		return *r.Targets
	}

	out := make([]string, 0, len(linked))
	for _, a := range linked {
		out = append(out, a.ID)
	}
	return out
}

// UnknownTargets reports targets naming accounts that are not linked, so the
// UI can show a route that will never sync rather than leaving it silent.
func UnknownTargets(r model.Route, linked []model.Account) []string {
	if r.Targets == nil {
		return nil
	}

	known := make(map[string]bool, len(linked))
	for _, a := range linked {
		known[a.ID] = true
	}

	var unknown []string
	for _, target := range *r.Targets {
		if !known[target] {
			unknown = append(unknown, target)
		}
	}
	return unknown
}
