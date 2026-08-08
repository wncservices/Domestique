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
//
// Deliberately small. Accounts are not here: they are linked by riders through
// the UI and live in the database, so there is no second place where somebody's
// devices are written down and no way for the two to disagree. What is left is
// what a process needs before it can reach anything — where the database is,
// and how to recognise a user.
type Config struct {
	Source SourceConfig `yaml:"source"`
	Auth   auth.Config  `yaml:"auth"`
	Komoot KomootConfig `yaml:"komoot"`
}

// DefaultDSN is where a database library lives unless configured otherwise.
const DefaultDSN = "data/domestique.db"

// EnvSourceDSN overrides source.dsn from the environment, so a password never
// has to be written into the config file.
const EnvSourceDSN = "DOMESTIQUE_SOURCE_DSN"

// Load reads a config file. A missing file is not an error: the defaults (a
// database library, no accounts) are enough to start uploading routes.
func Load(path string) (*Config, error) {
	cfg := &Config{Source: SourceConfig{Kind: SourceDB, DSN: DefaultDSN}}

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
	if c.Source.Kind == "" {
		c.Source.Kind = SourceDB
	}

	// A PostgreSQL DSN carries a password, so a deployment supplies it through
	// the environment rather than writing it into a config file that wants to
	// be readable. Same Vault -> envFrom path as every other credential.
	if dsn := os.Getenv(EnvSourceDSN); dsn != "" {
		c.Source.Kind = SourceDB
		c.Source.DSN = dsn
	}

	if c.Source.Kind == SourceFS && c.Source.Path == "" {
		c.Source.Path = "routes"
	}
	if c.Source.Kind == SourceDB && c.Source.DSN == "" {
		c.Source.DSN = DefaultDSN
	}
}

// Validate checks a config for self-consistency. It is exported because CLI
// flags can rewrite the source after Load, and an unchecked override would
// otherwise silently fall back to a filesystem source.
func (c *Config) Validate() error {
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
