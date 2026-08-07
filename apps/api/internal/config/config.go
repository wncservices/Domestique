// Package config holds the app's own configuration: which accounts exist and
// where routes come from.
//
// This is deliberately separate from the route library. The app is generic and
// open source; the routes are personal data that lives somewhere else — a
// private git repo, or a database. Nothing here is a secret: account ids and
// labels only. Credentials come from the environment.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/wncservices/domestique/apps/api/internal/auth"
	"github.com/wncservices/domestique/apps/api/internal/model"
)

// SourceKind selects where routes are read from.
type SourceKind string

const (
	// SourceFS reads a directory of GPX files — typically a checkout of a
	// separate, private routes repo.
	SourceFS SourceKind = "fs"
	// SourceDB stores GPX blobs in a database and accepts uploads.
	SourceDB SourceKind = "db"
)

// SourceConfig describes where routes live.
type SourceConfig struct {
	Kind SourceKind `yaml:"kind"`
	// Path is the library directory when Kind is fs.
	Path string `yaml:"path,omitempty"`
	// DSN is the database connection string when Kind is db.
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

// Config is domestique.yaml.
type Config struct {
	Accounts       []model.Account `yaml:"accounts"`
	DefaultTargets []string        `yaml:"default_targets"`
	Source         SourceConfig    `yaml:"source"`
	Auth           auth.Config     `yaml:"auth"`
	Komoot         KomootConfig    `yaml:"komoot"`
}

// DefaultDSN is where a database library lives unless configured otherwise.
const DefaultDSN = "data/domestique.db"

// Load reads a config file. A missing file is not an error: the defaults (a
// database library, no accounts) are enough to start uploading routes.
func Load(path string) (*Config, error) {
	cfg := &Config{Source: SourceConfig{Kind: SourceDB, DSN: DefaultDSN}}

	// #nosec G304 -- the config path is operator configuration, not user input.
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("%s: invalid YAML: %w", path, err)
	}

	if cfg.Source.Kind == "" {
		cfg.Source.Kind = SourceDB
	}
	if cfg.Source.Kind == SourceFS && cfg.Source.Path == "" {
		cfg.Source.Path = "routes"
	}
	if cfg.Source.Kind == SourceDB && cfg.Source.DSN == "" {
		cfg.Source.DSN = DefaultDSN
	}
	return cfg, cfg.Validate()
}

// Validate checks a config for self-consistency. It is exported because CLI
// flags can rewrite the source after Load, and an unchecked override would
// otherwise silently fall back to a filesystem source.
func (c *Config) Validate() error {
	seen := map[string]bool{}
	for _, a := range c.Accounts {
		if a.ID == "" || a.Rider == "" {
			return fmt.Errorf("account %q: id and rider are both required", a.ID)
		}
		if seen[a.ID] {
			return fmt.Errorf("duplicate account id %q", a.ID)
		}
		seen[a.ID] = true
	}
	for _, target := range c.DefaultTargets {
		if !seen[target] {
			return fmt.Errorf("default_targets names unknown account %q", target)
		}
	}
	switch c.Source.Kind {
	case SourceFS, SourceDB:
	default:
		return fmt.Errorf("unknown source kind %q (want fs or db)", c.Source.Kind)
	}

	// Surfaces a bad auth config at startup rather than on the first request.
	if _, err := auth.New(c.Auth); err != nil {
		return err
	}
	return nil
}

// Account looks up a configured account by id.
func (c *Config) Account(id string) (model.Account, bool) {
	for _, a := range c.Accounts {
		if a.ID == id {
			return a, true
		}
	}
	return model.Account{}, false
}

// TargetsFor returns the accounts a route should be pushed to. A route that
// does not name targets inherits the library default.
func (c *Config) TargetsFor(r model.Route) []string {
	if r.Targets != nil {
		return *r.Targets
	}
	return c.DefaultTargets
}

// UnknownTargets reports targets that name accounts which do not exist, so the
// UI can surface a typo instead of silently never syncing a route.
func (c *Config) UnknownTargets(r model.Route) []string {
	var unknown []string
	for _, target := range c.TargetsFor(r) {
		if _, ok := c.Account(target); !ok {
			unknown = append(unknown, target)
		}
	}
	return unknown
}
