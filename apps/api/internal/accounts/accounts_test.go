package accounts

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/model"
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
	if _, err := db.Conn().Exec(`DELETE FROM accounts`); err != nil {
		t.Fatal(err)
	}
	return store
}

func sqliteStore(t *testing.T) *Store {
	return openStore(t, filepath.Join(t.TempDir(), "accounts.db"))
}

func postgresStore(t *testing.T) *Store {
	dsn := os.Getenv(postgresEnv)
	if dsn == "" {
		t.Skipf("set %s to a PostgreSQL DSN to run this", postgresEnv)
	}
	return openStore(t, dsn)
}

func TestAccountsEachEngine(t *testing.T) {
	for engine, open := range map[string]func(*testing.T) *Store{
		"sqlite":   sqliteStore,
		"postgres": postgresStore,
	} {
		t.Run(engine, func(t *testing.T) {
			t.Run("link and read back", func(t *testing.T) {
				store := open(t)

				account, err := store.Link(model.ProviderGarmin, "one", "")
				if err != nil {
					t.Fatalf("link: %v", err)
				}
				if account.ID != "garmin:one" {
					t.Errorf("id = %q, want garmin:one", account.ID)
				}
				// A label nobody supplied should still read well in a menu.
				if account.Label == "" {
					t.Error("no label was derived")
				}

				linked, err := store.List()
				if err != nil {
					t.Fatal(err)
				}
				if len(linked) != 1 || linked[0].Rider != "one" {
					t.Errorf("list = %+v", linked)
				}
			})

			// One head unit per rider per provider: two rows would both claim
			// the same device, and the sync state is keyed by that id.
			t.Run("no duplicates", func(t *testing.T) {
				store := open(t)

				if _, err := store.Link(model.ProviderGarmin, "one", ""); err != nil {
					t.Fatal(err)
				}
				_, err := store.Link(model.ProviderGarmin, "one", "")
				if !errors.Is(err, ErrExists) {
					t.Errorf("second link: err = %v, want ErrExists", err)
				}

				// A different rider, or a different provider, is fine.
				if _, err := store.Link(model.ProviderGarmin, "two", ""); err != nil {
					t.Errorf("another rider: %v", err)
				}
				if _, err := store.Link(model.ProviderWahoo, "one", ""); err != nil {
					t.Errorf("another provider: %v", err)
				}
			})

			t.Run("unlink", func(t *testing.T) {
				store := open(t)

				account, err := store.Link(model.ProviderWahoo, "one", "")
				if err != nil {
					t.Fatal(err)
				}
				if err := store.Unlink(account.ID); err != nil {
					t.Fatalf("unlink: %v", err)
				}
				if err := store.Unlink(account.ID); !errors.Is(err, ErrNotFound) {
					t.Errorf("second unlink: err = %v, want ErrNotFound", err)
				}
				if _, err := store.Get(account.ID); !errors.Is(err, ErrNotFound) {
					t.Errorf("get after unlink: err = %v, want ErrNotFound", err)
				}
			})

			t.Run("relabel", func(t *testing.T) {
				store := open(t)

				account, err := store.Link(model.ProviderGarmin, "one", "Old name")
				if err != nil {
					t.Fatal(err)
				}
				updated, err := store.Relabel(account.ID, "Edge 1040")
				if err != nil {
					t.Fatal(err)
				}
				if updated.Label != "Edge 1040" {
					t.Errorf("label = %q", updated.Label)
				}
				if _, err := store.Relabel(account.ID, "  "); err == nil {
					t.Error("an empty label was accepted")
				}
			})
		})
	}
}

// The rider comes from an Authelia username and ends up in an id used across
// the API, so it has to survive a URL.
func TestLinkRejectsUnusableRiders(t *testing.T) {
	store := sqliteStore(t)

	for _, rider := range []string{"", "   ", "with space", "with/slash", "with?query"} {
		if _, err := store.Link(model.ProviderGarmin, rider, ""); err == nil {
			t.Errorf("rider %q was accepted", rider)
		}
	}
}

// A `|` is not a character a Remote-User header from Authelia ever carries,
// but it is exactly how Auth0's default sub is shaped — "auth0|<hex id>" —
// since that issuer's database connection does not populate
// preferred_username at all. Rejecting it would work fine right up until
// somebody linked an account under mode: oidc.
func TestLinkAcceptsAnOIDCSubShape(t *testing.T) {
	store := sqliteStore(t)
	if _, err := store.Link(model.ProviderGarmin, "auth0|64f2a1b2c3d4e5f6", ""); err != nil {
		t.Errorf("an Auth0-shaped rider was rejected: %v", err)
	}
}

func TestLinkRejectsUnknownProvider(t *testing.T) {
	store := sqliteStore(t)
	if _, err := store.Link(model.Provider("strava"), "one", ""); err == nil {
		t.Error("unknown provider accepted")
	}
}

// The id is what sync state is keyed by, so its shape matters.
func TestIDIsStable(t *testing.T) {
	if got := ID(model.ProviderGarmin, "Wilant"); got != "garmin:wilant" {
		t.Errorf("ID = %q, want it lowercased", got)
	}
	if got := ID(model.ProviderWahoo, " one "); got != "wahoo:one" {
		t.Errorf("ID = %q, want it trimmed", got)
	}
}
