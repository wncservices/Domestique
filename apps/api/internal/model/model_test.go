package model

import "testing"

// EnvPrefix names the env vars a rider's credentials arrive in, so it has to
// be a legal identifier whatever the rider called themselves.
func TestEnvPrefix(t *testing.T) {
	for _, tc := range []struct {
		account Account
		want    string
	}{
		{Account{Provider: ProviderGarmin, Rider: "wilant"}, "GARMIN_WILANT"},
		{Account{Provider: ProviderWahoo, Rider: "friend"}, "WAHOO_FRIEND"},
		{Account{Provider: ProviderGarmin, Rider: "jan-piet"}, "GARMIN_JAN_PIET"},
		{Account{Provider: ProviderWahoo, Rider: "rider 2"}, "WAHOO_RIDER_2"},
	} {
		if got := tc.account.EnvPrefix(); got != tc.want {
			t.Errorf("%s/%s: prefix = %q, want %q",
				tc.account.Provider, tc.account.Rider, got, tc.want)
		}
	}
}

// An absent `enabled` means yes: a bare GPX drop should sync.
func TestIsEnabledDefaultsToTrue(t *testing.T) {
	if !(RouteMeta{}).IsEnabled() {
		t.Error("a route with no `enabled` field is not enabled")
	}

	no := false
	if (RouteMeta{Enabled: &no}).IsEnabled() {
		t.Error("enabled: false was ignored")
	}

	yes := true
	if !(RouteMeta{Enabled: &yes}).IsEnabled() {
		t.Error("enabled: true was ignored")
	}
}

func TestPlanChangesExcludesNoops(t *testing.T) {
	plan := Plan{Items: []PlanItem{
		{Op: OpCreate, Slug: "a"},
		{Op: OpNoop, Slug: "b"},
		{Op: OpUpdate, Slug: "c"},
		{Op: OpNoop, Slug: "d"},
		{Op: OpDelete, Slug: "e"},
	}}

	changes := plan.Changes()
	if len(changes) != 3 {
		t.Fatalf("got %d changes, want 3", len(changes))
	}
	for _, item := range changes {
		if item.Op == OpNoop {
			t.Errorf("%s is a noop but was returned as a change", item.Slug)
		}
	}
}

func TestPlanSelectNarrowsToTheGivenKeys(t *testing.T) {
	plan := Plan{Items: []PlanItem{
		{Op: OpCreate, AccountID: "garmin:one", Slug: "a"},
		{Op: OpCreate, AccountID: "wahoo:two", Slug: "a"},
		{Op: OpUpdate, AccountID: "garmin:one", Slug: "b"},
	}}

	got := plan.Select(map[PlanKey]bool{{AccountID: "garmin:one", Slug: "a"}: true})
	if len(got.Items) != 1 || got.Items[0].AccountID != "garmin:one" || got.Items[0].Slug != "a" {
		t.Fatalf("Select = %+v, want only garmin:one/a", got.Items)
	}
}

// A nil or empty set means "everything" — the shape sent by a client that
// predates selection, and by the frontend's own push-all button.
func TestPlanSelectWithNoKeysIsANoop(t *testing.T) {
	plan := Plan{Items: []PlanItem{
		{Op: OpCreate, AccountID: "garmin:one", Slug: "a"},
		{Op: OpCreate, AccountID: "wahoo:two", Slug: "a"},
	}}

	if got := plan.Select(nil); len(got.Items) != len(plan.Items) {
		t.Fatalf("Select(nil) = %+v, want the whole plan unchanged", got.Items)
	}
	if got := plan.Select(map[PlanKey]bool{}); len(got.Items) != len(plan.Items) {
		t.Fatalf("Select({}) = %+v, want the whole plan unchanged", got.Items)
	}
}
