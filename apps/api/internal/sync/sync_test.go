package sync

import (
	"path/filepath"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/library"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/state"
)

func testLibrary(t *testing.T) *library.Library {
	t.Helper()
	lib, problems, err := library.Load(filepath.Join("testdata", "routes"))
	if err != nil {
		t.Fatalf("load library: %v", err)
	}
	if len(problems) > 0 {
		t.Fatalf("example library has problems: %v", problems)
	}
	return lib
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
	lib := testLibrary(t)
	plan := BuildPlan(lib, newStore(t))

	changes := plan.Changes()
	if len(changes) != len(lib.Routes)*len(lib.Config.Accounts) {
		t.Fatalf("got %d changes, want one per route/account pair", len(changes))
	}
	for _, item := range changes {
		if item.Op != model.OpCreate {
			t.Errorf("%s: op = %s, want create on an empty state", item.Slug, item.Op)
		}
	}
}

func TestBuildPlanIsIdempotent(t *testing.T) {
	lib := testLibrary(t)
	store := newStore(t)

	for _, item := range BuildPlan(lib, store).Changes() {
		if err := store.Record(state.Entry{
			AccountID:   item.AccountID,
			Slug:        item.Slug,
			RemoteID:    "remote-" + item.Slug,
			ContentHash: item.Route.ContentHash,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if changes := BuildPlan(lib, store).Changes(); len(changes) != 0 {
		t.Fatalf("re-plan after a full push produced %d changes, want 0", len(changes))
	}
}

func TestBuildPlanDeletesRoutesDroppedFromLibrary(t *testing.T) {
	lib := testLibrary(t)
	store := newStore(t)

	if err := store.Record(state.Entry{
		AccountID:   lib.Config.Accounts[0].ID,
		Slug:        "example/gone-from-repo",
		RemoteID:    "remote-123",
		ContentHash: "stale",
	}); err != nil {
		t.Fatal(err)
	}

	var deletes int
	for _, item := range BuildPlan(lib, store).Changes() {
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
	lib := testLibrary(t)
	store := newStore(t)

	route := lib.Routes[0]
	account := lib.Config.Accounts[0].ID
	if err := store.Record(state.Entry{
		AccountID:   account,
		Slug:        route.Slug,
		RemoteID:    "remote-abc",
		ContentHash: "hash-from-an-older-version",
	}); err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, item := range BuildPlan(lib, store).Changes() {
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
