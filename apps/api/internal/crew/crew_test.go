package crew

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/source"
)

const postgresEnv = "DOMESTIQUE_TEST_POSTGRES"

func openStore(t *testing.T, dsn string) *Store {
	t.Helper()

	db, err := source.OpenDB(dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	t.Cleanup(func() { db.Close() })

	store, err := UseDB(db.Conn(), db.DSN())
	if err != nil {
		t.Fatal(err)
	}
	// Postgres tests share a database; start clean.
	if _, err := db.Conn().Exec(`DELETE FROM crew_members`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn().Exec(`DELETE FROM crews`); err != nil {
		t.Fatal(err)
	}
	return store
}

func sqliteStore(t *testing.T) *Store {
	return openStore(t, filepath.Join(t.TempDir(), "crew.db"))
}

func postgresStore(t *testing.T) *Store {
	dsn := os.Getenv(postgresEnv)
	if dsn == "" {
		t.Skipf("set %s to a PostgreSQL DSN to run this", postgresEnv)
	}
	return openStore(t, dsn)
}

func TestCrewEachEngine(t *testing.T) {
	for engine, open := range map[string]func(*testing.T) *Store{
		"sqlite":   sqliteStore,
		"postgres": postgresStore,
	} {
		t.Run(engine, func(t *testing.T) {
			t.Run("create enrolls the owner as an approved member", func(t *testing.T) {
				store := open(t)
				ctx := t.Context()

				c, err := store.Create(ctx, "Sunday Club", "wilant")
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if c.ID != "crew:sunday-club" {
					t.Fatalf("id = %q, want crew:sunday-club", c.ID)
				}
				if c.Owner != "wilant" {
					t.Fatalf("owner = %q, want wilant", c.Owner)
				}

				snap, err := store.Snapshot(ctx)
				if err != nil {
					t.Fatalf("snapshot: %v", err)
				}
				if !snap.ApprovedRiders.Has(c.ID, "wilant") {
					t.Fatalf("owner is not an approved member: %v", snap.ApprovedRiders)
				}
			})

			t.Run("colliding names get a disambiguating suffix", func(t *testing.T) {
				store := open(t)
				ctx := t.Context()

				first, err := store.Create(ctx, "Sunday Club", "wilant")
				if err != nil {
					t.Fatalf("create first: %v", err)
				}
				second, err := store.Create(ctx, "Sunday Club", "someone-else")
				if err != nil {
					t.Fatalf("create second: %v", err)
				}
				if first.ID == second.ID {
					t.Fatalf("both crews got id %q", first.ID)
				}
				if second.ID != "crew:sunday-club-2" {
					t.Fatalf("second id = %q, want crew:sunday-club-2", second.ID)
				}
			})

			t.Run("join, approve, and the member becomes approved", func(t *testing.T) {
				store := open(t)
				ctx := t.Context()

				c, err := store.Create(ctx, "Family", "wilant")
				if err != nil {
					t.Fatalf("create: %v", err)
				}

				if _, err := store.RequestJoin(ctx, c.ID, "family-member"); err != nil {
					t.Fatalf("request join: %v", err)
				}

				snap, err := store.Snapshot(ctx)
				if err != nil {
					t.Fatalf("snapshot: %v", err)
				}
				if snap.ApprovedRiders.Has(c.ID, "family-member") {
					t.Fatalf("pending request already counted as approved")
				}

				if err := store.Approve(ctx, c.ID, "family-member", "wilant"); err != nil {
					t.Fatalf("approve: %v", err)
				}

				snap, err = store.Snapshot(ctx)
				if err != nil {
					t.Fatalf("snapshot after approve: %v", err)
				}
				if !snap.ApprovedRiders.Has(c.ID, "family-member") {
					t.Fatalf("approved member missing from snapshot: %v", snap.ApprovedRiders)
				}
			})

			t.Run("requesting to join twice is an error, not a second row", func(t *testing.T) {
				store := open(t)
				ctx := t.Context()

				c, err := store.Create(ctx, "Family", "wilant")
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if _, err := store.RequestJoin(ctx, c.ID, "rider"); err != nil {
					t.Fatalf("first request: %v", err)
				}
				if _, err := store.RequestJoin(ctx, c.ID, "rider"); !errors.Is(err, ErrAlreadyMember) {
					t.Fatalf("second request: err = %v, want ErrAlreadyMember", err)
				}
			})

			t.Run("deny removes a pending request and it can be requested again", func(t *testing.T) {
				store := open(t)
				ctx := t.Context()

				c, err := store.Create(ctx, "Family", "wilant")
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if _, err := store.RequestJoin(ctx, c.ID, "rider"); err != nil {
					t.Fatalf("request: %v", err)
				}
				if err := store.Deny(ctx, c.ID, "rider"); err != nil {
					t.Fatalf("deny: %v", err)
				}
				if _, err := store.RequestJoin(ctx, c.ID, "rider"); err != nil {
					t.Fatalf("request after deny: %v", err)
				}
			})

			t.Run("remove takes an approved member out live", func(t *testing.T) {
				store := open(t)
				ctx := t.Context()

				c, err := store.Create(ctx, "Family", "wilant")
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if _, err := store.RequestJoin(ctx, c.ID, "rider"); err != nil {
					t.Fatalf("request: %v", err)
				}
				if err := store.Approve(ctx, c.ID, "rider", "wilant"); err != nil {
					t.Fatalf("approve: %v", err)
				}
				if err := store.Remove(ctx, c.ID, "rider"); err != nil {
					t.Fatalf("remove: %v", err)
				}

				snap, err := store.Snapshot(ctx)
				if err != nil {
					t.Fatalf("snapshot: %v", err)
				}
				if snap.ApprovedRiders.Has(c.ID, "rider") {
					t.Fatalf("removed member still approved: %v", snap.ApprovedRiders)
				}
			})

			t.Run("delete cascades membership", func(t *testing.T) {
				store := open(t)
				ctx := t.Context()

				c, err := store.Create(ctx, "Family", "wilant")
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if err := store.Delete(ctx, c.ID); err != nil {
					t.Fatalf("delete: %v", err)
				}
				if _, err := store.Get(ctx, c.ID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
				}

				members, err := store.Members(ctx, c.ID)
				if err != nil {
					t.Fatalf("members after delete: %v", err)
				}
				if len(members) != 0 {
					t.Fatalf("members survived crew delete: %v", members)
				}
			})

			t.Run("add member enrolls a rider as approved with no request first", func(t *testing.T) {
				store := open(t)
				ctx := t.Context()

				c, err := store.Create(ctx, "Family", "wilant")
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if _, err := store.AddMember(ctx, c.ID, "rider", "wilant"); err != nil {
					t.Fatalf("add member: %v", err)
				}

				snap, err := store.Snapshot(ctx)
				if err != nil {
					t.Fatalf("snapshot: %v", err)
				}
				if !snap.ApprovedRiders.Has(c.ID, "rider") {
					t.Fatalf("added member missing from snapshot: %v", snap.ApprovedRiders)
				}
			})

			t.Run("add member approves an existing pending request rather than erroring", func(t *testing.T) {
				store := open(t)
				ctx := t.Context()

				c, err := store.Create(ctx, "Family", "wilant")
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if _, err := store.RequestJoin(ctx, c.ID, "rider"); err != nil {
					t.Fatalf("request join: %v", err)
				}
				if _, err := store.AddMember(ctx, c.ID, "rider", "wilant"); err != nil {
					t.Fatalf("add member: %v", err)
				}

				snap, err := store.Snapshot(ctx)
				if err != nil {
					t.Fatalf("snapshot: %v", err)
				}
				if !snap.ApprovedRiders.Has(c.ID, "rider") {
					t.Fatalf("pending request was not approved by add member: %v", snap.ApprovedRiders)
				}
			})

			t.Run("add member is an error for a rider already approved", func(t *testing.T) {
				store := open(t)
				ctx := t.Context()

				c, err := store.Create(ctx, "Family", "wilant")
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if _, err := store.AddMember(ctx, c.ID, "rider", "wilant"); err != nil {
					t.Fatalf("first add: %v", err)
				}
				if _, err := store.AddMember(ctx, c.ID, "rider", "wilant"); !errors.Is(err, ErrAlreadyMember) {
					t.Fatalf("second add: err = %v, want ErrAlreadyMember", err)
				}
			})

			t.Run("auto-share defaults off, and AutoShareCrewsFor only counts approved members of an auto-share crew", func(t *testing.T) {
				store := open(t)
				ctx := t.Context()

				c, err := store.Create(ctx, "Family", "wilant")
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if c.AutoShare {
					t.Fatalf("auto_share = true, want false by default")
				}

				if _, err := store.AddMember(ctx, c.ID, "rider", "wilant"); err != nil {
					t.Fatalf("add member: %v", err)
				}

				snap, err := store.Snapshot(ctx)
				if err != nil {
					t.Fatalf("snapshot: %v", err)
				}
				if got := snap.AutoShareCrewsFor("rider"); len(got) != 0 {
					t.Fatalf("AutoShareCrewsFor before turning it on = %v, want none", got)
				}

				if err := store.SetAutoShare(ctx, c.ID, true); err != nil {
					t.Fatalf("set auto-share: %v", err)
				}
				snap, err = store.Snapshot(ctx)
				if err != nil {
					t.Fatalf("snapshot after enabling: %v", err)
				}
				if got := snap.AutoShareCrewsFor("rider"); len(got) != 1 || got[0] != c.ID {
					t.Fatalf("AutoShareCrewsFor(rider) = %v, want [%s]", got, c.ID)
				}
				// A rider who never joined never gets this crew as a default,
				// auto-share or not.
				if got := snap.AutoShareCrewsFor("stranger"); len(got) != 0 {
					t.Fatalf("AutoShareCrewsFor(stranger) = %v, want none", got)
				}
			})

			t.Run("set auto-share on a nonexistent crew is not found", func(t *testing.T) {
				store := open(t)
				ctx := t.Context()

				if err := store.SetAutoShare(ctx, "crew:does-not-exist", true); !errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v, want ErrNotFound", err)
				}
			})

			t.Run("approving a rider with no request is not found", func(t *testing.T) {
				store := open(t)
				ctx := t.Context()

				c, err := store.Create(ctx, "Family", "wilant")
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if err := store.Approve(ctx, c.ID, "nobody", "wilant"); !errors.Is(err, ErrNotFound) {
					t.Fatalf("approve: err = %v, want ErrNotFound", err)
				}
			})

			// Found live: a route's owner and a crew's membership record for
			// that same real rider, written through two different paths
			// (a typed CLI --owner flag vs. a signed-in session), ended up
			// with different casing — "Wilant" vs "wilant". Has used to be an
			// exact string match, so config.TargetsFor silently decided that
			// rider had no crews at all, dropping the route from its own
			// owner's push targets with nothing to explain why.
			t.Run("membership is case-insensitive, both at write time and at read time", func(t *testing.T) {
				store := open(t)
				ctx := t.Context()

				// AddMember and Create both normalize whatever casing they
				// are given...
				c, err := store.Create(ctx, "Sunday Club", "  Wilant  ")
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if c.Owner != "wilant" {
					t.Fatalf("owner = %q, want normalized to wilant", c.Owner)
				}
				if _, err := store.AddMember(ctx, c.ID, "TIEBE", "wilant"); err != nil {
					t.Fatalf("add member: %v", err)
				}

				snap, err := store.Snapshot(ctx)
				if err != nil {
					t.Fatalf("snapshot: %v", err)
				}
				if got := snap.ApprovedRiders[c.ID]; len(got) != 2 || got[0] != "wilant" || got[1] != "tiebe" {
					t.Fatalf("members = %v, want [wilant tiebe] stored lowercase", got)
				}

				// ...and Has still matches even when the *caller* passes a
				// differently-cased string than what ended up stored — the
				// case a rider identifier reaching this package from
				// somewhere other than a normalized write (existing data
				// from before this fix, most concretely) still has to work.
				if !snap.ApprovedRiders.Has(c.ID, "Wilant") {
					t.Error(`Has(id, "Wilant") = false, want true (case-insensitive)`)
				}
				if !snap.ApprovedRiders.Has(c.ID, "tiebe") {
					t.Error(`Has(id, "tiebe") = false, want true`)
				}
				if snap.ApprovedRiders.Has(c.ID, "stranger") {
					t.Error(`Has(id, "stranger") = true, want false`)
				}
			})
		})
	}
}
