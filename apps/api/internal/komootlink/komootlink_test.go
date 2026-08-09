package komootlink

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/secrets"
	"github.com/wncservices/domestique/apps/api/internal/source"
)

const postgresEnv = "DOMESTIQUE_TEST_POSTGRES"

func newStore(t *testing.T, box *secrets.Box) (*Store, *source.DB) {
	t.Helper()

	db, err := source.OpenDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	store, err := UseDB(db.Conn(), db.DSN(), box)
	if err != nil {
		t.Fatal(err)
	}
	return store, db
}

func newBox(t *testing.T) *secrets.Box {
	t.Helper()
	key, err := secrets.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(key)
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func TestSaveThenGetAndCredentials(t *testing.T) {
	store, _ := newStore(t, newBox(t))

	link, err := store.Save("Wilant", "rider@example.com", "Wilant N", "user-1", "tok-abc")
	if err != nil {
		t.Fatal(err)
	}
	// The rider is the key, so it is normalised on the way in — otherwise
	// "Wilant" and "wilant" become two connections for one person.
	if link.Rider != "wilant" {
		t.Errorf("rider = %q, want it lowercased", link.Rider)
	}

	got, err := store.Get("wilant")
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "rider@example.com" || got.DisplayName != "Wilant N" {
		t.Errorf("got %+v, want the saved details", got)
	}

	userID, token, err := store.Credentials("WILANT")
	if err != nil {
		t.Fatal(err)
	}
	if userID != "user-1" || token != "tok-abc" {
		t.Errorf("credentials = %q/%q, want user-1/tok-abc", userID, token)
	}
}

// The point of the package. A token in the database has to be unreadable
// without the key, or encrypting it achieved nothing.
func TestTokenIsNotStoredInClear(t *testing.T) {
	store, db := newStore(t, newBox(t))

	if _, err := store.Save("wilant", "r@example.com", "", "user-1", "super-secret-token"); err != nil {
		t.Fatal(err)
	}

	var stored []byte
	if err := db.Conn().QueryRow(`SELECT token FROM komoot_links WHERE rider = 'wilant'`).
		Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("super-secret-token")) {
		t.Fatal("the token is in the database in clear")
	}
}

func TestReconnectReplacesRatherThanDuplicates(t *testing.T) {
	store, db := newStore(t, newBox(t))

	if _, err := store.Save("wilant", "old@example.com", "", "user-1", "old-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save("wilant", "new@example.com", "", "user-2", "new-token"); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM komoot_links`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("rows = %d, want 1: reconnecting replaces", count)
	}

	_, token, err := store.Credentials("wilant")
	if err != nil {
		t.Fatal(err)
	}
	if token != "new-token" {
		t.Errorf("token = %q, want the reconnected one", token)
	}
}

func TestWithoutAKeyNothingIsStored(t *testing.T) {
	store, db := newStore(t, nil)

	if store.CanStore() {
		t.Error("CanStore is true without a key")
	}
	if _, err := store.Save("wilant", "r@example.com", "", "user-1", "tok"); !errors.Is(err, secrets.ErrNoKey) {
		t.Errorf("Save error = %v, want ErrNoKey", err)
	}

	// Nothing partial was written on the way to refusing.
	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM komoot_links`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("rows = %d, want 0", count)
	}
}

// Removing the key must not break reading what is already there — the rider
// should see their connection and be told to reconnect, not meet a crash.
func TestKeyRemovedStillReadsForDisplay(t *testing.T) {
	box := newBox(t)
	store, db := newStore(t, box)
	if _, err := store.Save("wilant", "r@example.com", "Name", "user-1", "tok"); err != nil {
		t.Fatal(err)
	}

	keyless, err := UseDB(db.Conn(), db.DSN(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keyless.Get("wilant"); err != nil {
		t.Errorf("Get failed without a key: %v", err)
	}
	if _, _, err := keyless.Credentials("wilant"); !errors.Is(err, secrets.ErrNoKey) {
		t.Errorf("Credentials error = %v, want ErrNoKey", err)
	}
}

// A key that is not the one the token was sealed with is a real operational
// case (a rotated Secret). It must say so rather than hand back nonsense.
func TestWrongKeyReportsAndSuggestsReconnecting(t *testing.T) {
	store, db := newStore(t, newBox(t))
	if _, err := store.Save("wilant", "r@example.com", "", "user-1", "tok"); err != nil {
		t.Fatal(err)
	}

	rotated, err := UseDB(db.Conn(), db.DSN(), newBox(t))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = rotated.Credentials("wilant")
	if err == nil {
		t.Fatal("a token opened with the wrong key")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("reconnect")) {
		t.Errorf("error = %q, want it to say what to do", err)
	}
}

func TestGetAndDeleteForUnknownRider(t *testing.T) {
	store, _ := newStore(t, newBox(t))

	if _, err := store.Get("nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get error = %v, want ErrNotFound", err)
	}
	// Deleting what is not there is what the caller asked for.
	if err := store.Delete("nobody"); err != nil {
		t.Errorf("Delete of an absent rider errored: %v", err)
	}
}

func TestDelete(t *testing.T) {
	store, _ := newStore(t, newBox(t))
	if _, err := store.Save("wilant", "r@example.com", "", "user-1", "tok"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("wilant"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("wilant"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func TestSaveRejectsAnEmptyRider(t *testing.T) {
	store, _ := newStore(t, newBox(t))
	if _, err := store.Save("  ", "r@example.com", "", "user-1", "tok"); err == nil {
		t.Error("a connection was saved with no owner")
	}
}

// PostgreSQL differs from SQLite in the two places this package touches: the
// blob column type and the upsert. Both are covered above; run them again for
// real when a server is available.
func TestPostgres(t *testing.T) {
	dsn := os.Getenv(postgresEnv)
	if dsn == "" {
		t.Skipf("set %s to a PostgreSQL DSN to run this", postgresEnv)
	}

	db, err := source.OpenDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Conn().Exec(`DROP TABLE IF EXISTS komoot_links`); err != nil {
		t.Fatal(err)
	}

	store, err := UseDB(db.Conn(), db.DSN(), newBox(t))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Save("wilant", "r@example.com", "Name", "user-1", "tok-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save("wilant", "r@example.com", "Name", "user-2", "tok-2"); err != nil {
		t.Fatalf("upsert failed on postgres: %v", err)
	}

	userID, token, err := store.Credentials("wilant")
	if err != nil {
		t.Fatal(err)
	}
	if userID != "user-2" || token != "tok-2" {
		t.Errorf("credentials = %q/%q, want user-2/tok-2", userID, token)
	}
}
