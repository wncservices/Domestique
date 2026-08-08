package sync

import (
	"errors"
	"strings"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/config"

	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/state"
	"github.com/wncservices/domestique/apps/api/internal/targets"
)

// fakeTarget records what it was asked to do, and can be told to fail.
type fakeTarget struct {
	creates, updates, deletes []string
	err                       error
}

func (f *fakeTarget) Create(route model.Route) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.creates = append(f.creates, route.Slug)
	return "remote-" + route.Slug, nil
}

func (f *fakeTarget) Update(remoteID string, route model.Route) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.updates = append(f.updates, route.Slug)
	return remoteID, nil
}

func (f *fakeTarget) Delete(remoteID string) error {
	if f.err != nil {
		return f.err
	}
	f.deletes = append(f.deletes, remoteID)
	return nil
}

// forAccount keeps the assertions readable now that the read can fail.
func forAccount(t *testing.T, store state.Store, accountID string) map[string]state.Entry {
	t.Helper()
	entries, err := store.ForAccount(accountID)
	if err != nil {
		t.Fatalf("ForAccount(%s): %v", accountID, err)
	}
	return entries
}

// mustPlan is the same idea for BuildPlan.
func mustPlan(t *testing.T, routes []model.Route, cfg *config.Config, store state.Store) model.Plan {
	t.Helper()
	plan, err := BuildPlan(routes, cfg, store)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	return plan
}

func route(slug, hash string) model.Route {
	return model.Route{
		RouteMeta:   model.RouteMeta{Name: slug},
		Slug:        slug,
		ContentHash: hash,
	}
}

func TestApplyCreatesAndRecordsState(t *testing.T) {
	store, target := newStore(t), &fakeTarget{}
	plan := model.Plan{Items: []model.PlanItem{{
		Op: model.OpCreate, AccountID: "garmin:wilant", Slug: "loop",
		Route: &model.Route{RouteMeta: model.RouteMeta{Name: "Loop"}, Slug: "loop", ContentHash: "v1"},
	}}}

	if failures := Apply(plan, store, map[string]targets.Target{"garmin:wilant": target}); len(failures) != 0 {
		t.Fatalf("failures: %v", failures)
	}

	if len(target.creates) != 1 {
		t.Errorf("adapter saw %v, want one create", target.creates)
	}

	entry, ok := forAccount(t, store, "garmin:wilant")["loop"]
	if !ok {
		t.Fatal("nothing recorded; the next run would create it again")
	}
	if entry.RemoteID != "remote-loop" || entry.ContentHash != "v1" {
		t.Errorf("recorded %+v", entry)
	}
}

func TestApplyUpdateKeepsRemoteID(t *testing.T) {
	store, target := newStore(t), &fakeTarget{}
	plan := model.Plan{Items: []model.PlanItem{{
		Op: model.OpUpdate, AccountID: "garmin:wilant", Slug: "loop",
		RemoteID: "remote-abc",
		Route:    &model.Route{RouteMeta: model.RouteMeta{Name: "Loop"}, Slug: "loop", ContentHash: "v2"},
	}}}

	if failures := Apply(plan, store, map[string]targets.Target{"garmin:wilant": target}); len(failures) != 0 {
		t.Fatalf("failures: %v", failures)
	}

	entry := forAccount(t, store, "garmin:wilant")["loop"]
	if entry.RemoteID != "remote-abc" {
		t.Errorf("remote id = %q, want it preserved across an update", entry.RemoteID)
	}
	if entry.ContentHash != "v2" {
		t.Errorf("hash = %q, want the new one", entry.ContentHash)
	}
}

func TestApplyDeleteForgetsState(t *testing.T) {
	store, target := newStore(t), &fakeTarget{}
	if err := store.Record(state.Entry{
		AccountID: "garmin:wilant", Slug: "loop", RemoteID: "remote-abc", ContentHash: "v1",
	}); err != nil {
		t.Fatal(err)
	}

	plan := model.Plan{Items: []model.PlanItem{{
		Op: model.OpDelete, AccountID: "garmin:wilant", Slug: "loop", RemoteID: "remote-abc",
	}}}

	if failures := Apply(plan, store, map[string]targets.Target{"garmin:wilant": target}); len(failures) != 0 {
		t.Fatalf("failures: %v", failures)
	}
	if len(target.deletes) != 1 {
		t.Errorf("adapter saw %v, want one delete", target.deletes)
	}
	if len(forAccount(t, store, "garmin:wilant")) != 0 {
		t.Error("state kept after a delete; the route would be deleted again forever")
	}
}

// One failing provider must not stop the others, and must not record success.
func TestApplyIsolatesFailures(t *testing.T) {
	store := newStore(t)
	healthy := &fakeTarget{}
	broken := &fakeTarget{err: errors.New("provider exploded")}

	plan := model.Plan{Items: []model.PlanItem{
		{Op: model.OpCreate, AccountID: "garmin:wilant", Slug: "loop", Route: ptr(route("loop", "v1"))},
		{Op: model.OpCreate, AccountID: "wahoo:friend", Slug: "loop", Route: ptr(route("loop", "v1"))},
	}}

	failures := Apply(plan, store, map[string]targets.Target{
		"garmin:wilant": healthy,
		"wahoo:friend":  broken,
	})

	if len(failures) != 1 {
		t.Fatalf("failures = %v, want exactly one", failures)
	}
	if !strings.Contains(failures[0].Error(), "wahoo:friend") {
		t.Errorf("failure does not name the account: %v", failures[0])
	}
	if len(healthy.creates) != 1 {
		t.Error("the healthy account was skipped because the other failed")
	}
	if len(forAccount(t, store, "wahoo:friend")) != 0 {
		t.Error("failed push was recorded as success; it would never be retried")
	}
}

func TestApplyReportsMissingAdapter(t *testing.T) {
	store := newStore(t)
	plan := model.Plan{Items: []model.PlanItem{{
		Op: model.OpCreate, AccountID: "garmin:ghost", Slug: "loop", Route: ptr(route("loop", "v1")),
	}}}

	failures := Apply(plan, store, map[string]targets.Target{})
	if len(failures) != 1 || !strings.Contains(failures[0].Error(), "garmin:ghost") {
		t.Errorf("failures = %v, want one naming the account", failures)
	}
}

func TestApplySkipsNoops(t *testing.T) {
	store, target := newStore(t), &fakeTarget{}
	plan := model.Plan{Items: []model.PlanItem{{
		Op: model.OpNoop, AccountID: "garmin:wilant", Slug: "loop", Route: ptr(route("loop", "v1")),
	}}}

	if failures := Apply(plan, store, map[string]targets.Target{"garmin:wilant": target}); len(failures) != 0 {
		t.Fatalf("failures: %v", failures)
	}
	if len(target.creates)+len(target.updates)+len(target.deletes) != 0 {
		t.Error("an up-to-date route was pushed anyway")
	}
}

func ptr(r model.Route) *model.Route { return &r }
