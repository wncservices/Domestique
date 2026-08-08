package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempState(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state.json")
}

// mustAll and mustForAccount keep the tests readable now that the reads can
// fail. A failure here is a broken test, not a case under test.
func mustAll(t *testing.T, store Store) []Entry {
	t.Helper()
	entries, err := store.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	return entries
}

func mustForAccount(t *testing.T, store Store, accountID string) map[string]Entry {
	t.Helper()
	entries, err := store.ForAccount(accountID)
	if err != nil {
		t.Fatalf("ForAccount(%s): %v", accountID, err)
	}
	return entries
}

func TestOpenCreatesEmptyStore(t *testing.T) {
	store, err := Open(tempState(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if entries := mustAll(t, store); len(entries) != 0 {
		t.Errorf("fresh store has %d entries", len(entries))
	}
}

// Losing state means re-uploading every route, so it has to survive a restart.
func TestStateSurvivesReopen(t *testing.T) {
	path := tempState(t)

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(Entry{
		AccountID: "garmin:wilant", Slug: "kemmelberg-loop",
		RemoteID: "remote-1", ContentHash: "abc", Name: "Kemmelberg Loop",
	}); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	entries := mustAll(t, reopened)
	if len(entries) != 1 {
		t.Fatalf("got %d entries after reopen, want 1", len(entries))
	}
	if entries[0].RemoteID != "remote-1" || entries[0].ContentHash != "abc" {
		t.Errorf("entry came back wrong: %+v", entries[0])
	}
	if entries[0].UpdatedAt == "" {
		t.Error("no timestamp recorded")
	}
}

func TestRecordUpdatesInPlace(t *testing.T) {
	store, err := Open(tempState(t))
	if err != nil {
		t.Fatal(err)
	}

	base := Entry{AccountID: "garmin:wilant", Slug: "loop", RemoteID: "r1", ContentHash: "v1"}
	if err := store.Record(base); err != nil {
		t.Fatal(err)
	}

	updated := base
	updated.ContentHash = "v2"
	if err := store.Record(updated); err != nil {
		t.Fatal(err)
	}

	entries := mustAll(t, store)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want the row replaced not duplicated", len(entries))
	}
	if entries[0].ContentHash != "v2" {
		t.Errorf("hash = %q, want v2", entries[0].ContentHash)
	}
}

// The same route on two accounts is two independent rows: one provider
// failing must not mark the route synced everywhere.
func TestForAccountIsolatesAccounts(t *testing.T) {
	store, err := Open(tempState(t))
	if err != nil {
		t.Fatal(err)
	}

	for _, account := range []string{"garmin:wilant", "wahoo:friend"} {
		if err := store.Record(Entry{
			AccountID: account, Slug: "loop",
			RemoteID: "remote-" + account, ContentHash: "abc",
		}); err != nil {
			t.Fatal(err)
		}
	}

	garmin := mustForAccount(t, store, "garmin:wilant")
	if len(garmin) != 1 || garmin["loop"].RemoteID != "remote-garmin:wilant" {
		t.Errorf("garmin view = %+v", garmin)
	}
	if got := mustForAccount(t, store, "nobody"); len(got) != 0 {
		t.Errorf("unknown account returned %d entries", len(got))
	}

	if err := store.Forget("garmin:wilant", "loop"); err != nil {
		t.Fatal(err)
	}
	if len(mustForAccount(t, store, "garmin:wilant")) != 0 {
		t.Error("forget did not remove the entry")
	}
	if len(mustForAccount(t, store, "wahoo:friend")) != 1 {
		t.Error("forget removed the other account's entry too")
	}
}

func TestForgetUnknownEntryIsHarmless(t *testing.T) {
	store, err := Open(tempState(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Forget("garmin:wilant", "never-existed"); err != nil {
		t.Errorf("forget on a missing entry returned %v", err)
	}
}

func TestAllIsSortedForStableOutput(t *testing.T) {
	store, err := Open(tempState(t))
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range []Entry{
		{AccountID: "wahoo:friend", Slug: "zebra"},
		{AccountID: "garmin:wilant", Slug: "beta"},
		{AccountID: "garmin:wilant", Slug: "alpha"},
	} {
		if err := store.Record(e); err != nil {
			t.Fatal(err)
		}
	}

	entries := mustAll(t, store)
	want := []string{"garmin:wilant/alpha", "garmin:wilant/beta", "wahoo:friend/zebra"}
	for i, entry := range entries {
		if got := entry.AccountID + "/" + entry.Slug; got != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got, want[i])
		}
	}
}

// A truncated or hand-edited file must fail loudly. Silently starting empty
// would re-upload every route to every device.
func TestCorruptStateFileIsAnError(t *testing.T) {
	path := tempState(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Open(path)
	if err == nil {
		t.Fatal("corrupt state file opened cleanly")
	}
	if !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestOpenCreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "state.json")

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(Entry{AccountID: "a", Slug: "b"}); err != nil {
		t.Fatalf("record into a nested path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("state file not written: %v", err)
	}
}
