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

// A missing config is normal: it is enough to browse a library.
func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing config should not be an error: %v", err)
	}
	if cfg.Source.Kind != SourceFS {
		t.Errorf("kind = %q, want fs", cfg.Source.Kind)
	}
	if cfg.Source.Path != "routes" {
		t.Errorf("path = %q, want routes", cfg.Source.Path)
	}
}

func TestLoadParsesAccountsAndSource(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
source:
  kind: db
  dsn: ./data/routes.db
accounts:
  - id: garmin:wilant
    provider: garmin
    rider: wilant
    label: Wilant's Edge
default_targets:
  - garmin:wilant
`))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Source.Kind != SourceDB || cfg.Source.DSN != "./data/routes.db" {
		t.Errorf("source = %+v", cfg.Source)
	}
	if len(cfg.Accounts) != 1 || cfg.Accounts[0].Label != "Wilant's Edge" {
		t.Errorf("accounts = %+v", cfg.Accounts)
	}
}

func TestLoadRejectsBadConfigs(t *testing.T) {
	for name, body := range map[string]string{
		"invalid yaml": "accounts: [oops",
		"duplicate account id": `
accounts:
  - {id: garmin:wilant, provider: garmin, rider: wilant}
  - {id: garmin:wilant, provider: garmin, rider: someone}
`,
		"account without rider": `
accounts:
  - {id: garmin:wilant, provider: garmin}
`,
		"default target names nobody": `
accounts:
  - {id: garmin:wilant, provider: garmin, rider: wilant}
default_targets: [wahoo:ghost]
`,
		"unknown source kind": `
source:
  kind: carrier-pigeon
`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, body)); err == nil {
				t.Error("expected an error, got none")
			}
		})
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

func TestTargetsForFallsBackToDefaults(t *testing.T) {
	cfg := &Config{DefaultTargets: []string{"garmin:wilant", "wahoo:friend"}}

	// No targets named: inherit the defaults.
	if got := cfg.TargetsFor(model.Route{}); len(got) != 2 {
		t.Errorf("targets = %v, want both defaults", got)
	}

	// Targets named: use exactly those. This is what keeps one rider's
	// private routes off the other's head unit.
	only := []string{"garmin:wilant"}
	route := model.Route{RouteMeta: model.RouteMeta{Targets: &only}}
	if got := cfg.TargetsFor(route); len(got) != 1 || got[0] != "garmin:wilant" {
		t.Errorf("targets = %v, want [garmin:wilant]", got)
	}

	// An explicitly empty list means nowhere, not "the defaults".
	none := []string{}
	route = model.Route{RouteMeta: model.RouteMeta{Targets: &none}}
	if got := cfg.TargetsFor(route); len(got) != 0 {
		t.Errorf("targets = %v, want none", got)
	}
}

func TestUnknownTargetsAreReported(t *testing.T) {
	cfg := &Config{
		Accounts: []model.Account{
			{ID: "garmin:wilant", Provider: model.ProviderGarmin, Rider: "wilant"},
		},
	}

	typo := []string{"garmin:wilnat"}
	route := model.Route{RouteMeta: model.RouteMeta{Targets: &typo}}

	unknown := cfg.UnknownTargets(route)
	if len(unknown) != 1 || unknown[0] != "garmin:wilnat" {
		t.Errorf("unknown = %v, want the typo flagged", unknown)
	}

	good := []string{"garmin:wilant"}
	route = model.Route{RouteMeta: model.RouteMeta{Targets: &good}}
	if unknown := cfg.UnknownTargets(route); len(unknown) != 0 {
		t.Errorf("unknown = %v, want none", unknown)
	}
}

func TestAccountLookup(t *testing.T) {
	cfg := &Config{
		Accounts: []model.Account{
			{ID: "wahoo:friend", Provider: model.ProviderWahoo, Rider: "friend"},
		},
	}

	if _, ok := cfg.Account("wahoo:friend"); !ok {
		t.Error("known account not found")
	}
	if _, ok := cfg.Account("garmin:nobody"); ok {
		t.Error("unknown account reported as found")
	}
}
