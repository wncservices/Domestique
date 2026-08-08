package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/model"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "domestique.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A missing config is normal: the defaults are enough to start uploading.
// The default is the database, so a fresh install has somewhere to put routes
// without anyone creating a directory first.
func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing config should not be an error: %v", err)
	}
	if cfg.Source.Kind != SourceDB {
		t.Errorf("kind = %q, want db", cfg.Source.Kind)
	}
	if cfg.Source.DSN != DefaultDSN {
		t.Errorf("dsn = %q, want %q", cfg.Source.DSN, DefaultDSN)
	}
	// Authentication is off by default: a fresh checkout runs on a laptop.
	if cfg.Auth.Mode != "" {
		t.Errorf("auth mode = %q, want unset (none)", cfg.Auth.Mode)
	}
}

// An fs source still defaults its path, for anyone who sets only the kind.
func TestFSSourceDefaultsItsPath(t *testing.T) {
	cfg, err := Load(writeConfig(t, "source:\n  kind: fs\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source.Path != "routes" {
		t.Errorf("path = %q, want routes", cfg.Source.Path)
	}
}

func TestLoadParsesSource(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
source:
  kind: db
  dsn: ./data/routes.db
`))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Source.Kind != SourceDB || cfg.Source.DSN != "./data/routes.db" {
		t.Errorf("source = %+v", cfg.Source)
	}
}

func TestLoadRejectsBadConfigs(t *testing.T) {
	for name, body := range map[string]string{
		"invalid yaml": "source: [oops",
		"unknown source kind": `
source:
  kind: carrier-pigeon
`,
		"required group with no auth": `
auth:
  mode: none
  required_group: riders
`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, body)); err == nil {
				t.Error("expected an error, got none")
			}
		})
	}
}

// Accounts are linked through the UI and live in the database. Anything in the
// config file naming them would be a second source of truth.
func TestConfigHasNoAccounts(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
accounts:
  - id: garmin:someone
    provider: garmin
    rider: someone
default_targets: [garmin:someone]
source:
  kind: db
  dsn: x.db
`))
	if err != nil {
		t.Fatalf("stray keys should be ignored, not fatal: %v", err)
	}
	// The point: nothing above reaches the app.
	if cfg.Source.DSN != "x.db" {
		t.Errorf("source = %+v", cfg.Source)
	}
}

// CLI flags rewrite the source after Load, so Validate has to be callable
// again — an unchecked override used to fall back to a filesystem source.
func TestValidateCatchesOverriddenSource(t *testing.T) {
	cfg := &Config{Source: SourceConfig{Kind: SourceFS, Path: "routes"}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cfg.Source.Kind = "carrier-pigeon"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("unknown source kind accepted")
	}
	if !strings.Contains(err.Error(), "carrier-pigeon") {
		t.Errorf("error does not name the bad kind: %v", err)
	}
}

func TestTargetsForDefaultsToEveryLinkedAccount(t *testing.T) {
	linked := []model.Account{
		{ID: "garmin:one", Provider: model.ProviderGarmin, Rider: "one"},
		{ID: "wahoo:two", Provider: model.ProviderWahoo, Rider: "two"},
	}

	// No targets named: every linked account, which is the useful default for
	// a library two people share.
	if got := TargetsFor(model.Route{}, linked); len(got) != 2 {
		t.Errorf("targets = %v, want both linked accounts", got)
	}

	// Targets named: exactly those. This is what keeps one rider's private
	// routes off the other's head unit.
	only := []string{"garmin:one"}
	route := model.Route{RouteMeta: model.RouteMeta{Targets: &only}}
	if got := TargetsFor(route, linked); len(got) != 1 || got[0] != "garmin:one" {
		t.Errorf("targets = %v, want [garmin:one]", got)
	}

	// An explicitly empty list means nowhere, not "everywhere".
	none := []string{}
	route = model.Route{RouteMeta: model.RouteMeta{Targets: &none}}
	if got := TargetsFor(route, linked); len(got) != 0 {
		t.Errorf("targets = %v, want none", got)
	}

	// And with nothing linked there is nowhere to push, which is honest
	// rather than an error.
	if got := TargetsFor(model.Route{}, nil); len(got) != 0 {
		t.Errorf("targets = %v, want none when nothing is linked", got)
	}
}

func TestUnknownTargetsAreReported(t *testing.T) {
	linked := []model.Account{{ID: "garmin:one", Provider: model.ProviderGarmin, Rider: "one"}}

	typo := []string{"garmin:onee"}
	route := model.Route{RouteMeta: model.RouteMeta{Targets: &typo}}
	if unknown := UnknownTargets(route, linked); len(unknown) != 1 {
		t.Errorf("unknown = %v, want the typo flagged", unknown)
	}

	good := []string{"garmin:one"}
	route = model.Route{RouteMeta: model.RouteMeta{Targets: &good}}
	if unknown := UnknownTargets(route, linked); len(unknown) != 0 {
		t.Errorf("unknown = %v, want none", unknown)
	}

	// A route with no targets inherits the linked set, so nothing is unknown.
	if unknown := UnknownTargets(model.Route{}, linked); len(unknown) != 0 {
		t.Errorf("unknown = %v, want none", unknown)
	}
}
