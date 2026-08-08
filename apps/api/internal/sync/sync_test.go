package sync

import (
	"path/filepath"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/config"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/source"
	"github.com/wncservices/domestique/apps/api/internal/state"
)

func testConfig() *config.Config {
	return &config.Config{
		Accounts: []model.Account{
			{ID: "garmin:wilant", Provider: model.ProviderGarmin, Rider: "wilant"},
			{ID: "wahoo:friend", Provider: model.ProviderWahoo, Rider: "friend"},
		},
		DefaultTargets: []string{"garmin:wilant", "wahoo:friend"},
	}
}

func testRoutes(t *testing.T) []model.Route {
	t.Helper()
	src, err := source.NewFS(filepath.Join("testdata", "routes"))
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	routes, problems, err := src.List()
	if err != nil {
		t.Fatalf("list routes: %v", err)
	}
	if len(problems) > 0 {
		t.Fatalf("example library has problems: %v", problems)
	}
	if len(routes) == 0 {
		t.Fatal("no routes in testdata")
	}
	return routes
}

func newStore(t *testing.T) state.Store {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestBuildPlanCreatesUnsyncedRoutes(t *testing.T) {
	routes, cfg := testRoutes(t), testConfig()
	plan := mustPlan(t, routes, cfg, newStore(t))

	changes := plan.Changes()
	if want := len(routes) * len(cfg.Accounts); len(changes) != want {
		t.Fatalf("got %d changes, want %d (one per route/account pair)", len(changes), want)
	}
	for _, item := range changes {
		if item.Op != model.OpCreate {
			t.Errorf("%s: op = %s, want create on an empty state", item.Slug, item.Op)
		}
	}
}

func TestBuildPlanIsIdempotent(t *testing.T) {
	routes, cfg, store := testRoutes(t), testConfig(), newStore(t)

	for _, item := range mustPlan(t, routes, cfg, store).Changes() {
		if err := store.Record(state.Entry{
			AccountID:   item.AccountID,
			Slug:        item.Slug,
			RemoteID:    "remote-" + item.Slug,
			ContentHash: item.Route.ContentHash,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if changes := mustPlan(t, routes, cfg, store).Changes(); len(changes) != 0 {
		t.Fatalf("re-plan after a full push produced %d changes, want 0", len(changes))
	}
}

func TestBuildPlanDeletesRoutesDroppedFromLibrary(t *testing.T) {
	routes, cfg, store := testRoutes(t), testConfig(), newStore(t)

	if err := store.Record(state.Entry{
		AccountID:   cfg.Accounts[0].ID,
		Slug:        "gone-from-repo",
		RemoteID:    "remote-123",
		ContentHash: "stale",
	}); err != nil {
		t.Fatal(err)
	}

	var deletes int
	for _, item := range mustPlan(t, routes, cfg, store).Changes() {
		if item.Op == model.OpDelete {
			deletes++
			if item.RemoteID != "remote-123" {
				t.Errorf("delete carried remote id %q, want remote-123", item.RemoteID)
			}
		}
	}
	if deletes != 1 {
		t.Fatalf("got %d deletes, want 1", deletes)
	}
}

func TestBuildPlanUpdatesChangedRoutes(t *testing.T) {
	routes, cfg, store := testRoutes(t), testConfig(), newStore(t)

	route := routes[0]
	account := cfg.Accounts[0].ID
	if err := store.Record(state.Entry{
		AccountID:   account,
		Slug:        route.Slug,
		RemoteID:    "remote-abc",
		ContentHash: "hash-from-an-older-version",
	}); err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, item := range mustPlan(t, routes, cfg, store).Changes() {
		if item.Slug == route.Slug && item.AccountID == account {
			found = true
			if item.Op != model.OpUpdate {
				t.Errorf("op = %s, want update", item.Op)
			}
			if item.RemoteID != "remote-abc" {
				t.Errorf("update lost the remote id: %q", item.RemoteID)
			}
		}
	}
	if !found {
		t.Fatal("changed route produced no plan item")
	}
}

// A route that names its own targets must not be pushed anywhere else — this
// is what keeps one rider's private routes off the other's head unit.
func TestBuildPlanHonoursPerRouteTargets(t *testing.T) {
	routes, cfg, store := testRoutes(t), testConfig(), newStore(t)

	only := []string{"garmin:wilant"}
	routes[0].Targets = &only

	for _, item := range mustPlan(t, routes, cfg, store).Changes() {
		if item.Slug == routes[0].Slug && item.AccountID != "garmin:wilant" {
			t.Errorf("targeted route planned for %s as well", item.AccountID)
		}
	}
}
