package settings

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

func TestSetThenGet(t *testing.T) {
	store, _ := newStore(t, newBox(t))

	if err := store.Set("garmin.key", "value-1", "Wilant"); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("garmin.key")
	if err != nil {
		t.Fatal(err)
	}
	if got != "value-1" {
		t.Errorf("got %q, want value-1", got)
	}

	meta, err := store.Describe("garmin.key")
	if err != nil {
		t.Fatal(err)
	}
	if meta.UpdatedBy != "wilant" {
		t.Errorf("updatedBy = %q, want it lowercased", meta.UpdatedBy)
	}
	if meta.UpdatedAt.IsZero() {
		t.Error("updatedAt is zero")
	}
}

// The reason this package exists rather than a plain table: what it holds is
// a credential.
func TestValuesAreNotStoredInClear(t *testing.T) {
	store, db := newStore(t, newBox(t))
	if err := store.Set("garmin.key", "super-secret-consumer", ""); err != nil {
		t.Fatal(err)
	}

	var stored []byte
	if err := db.Conn().QueryRow(`SELECT value FROM settings WHERE name = 'garmin.key'`).
		Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("super-secret-consumer")) {
		t.Fatal("the value is in the database in clear")
	}
}

func TestSetReplaces(t *testing.T) {
	store, db := newStore(t, newBox(t))
	if err := store.Set("k", "one", "a"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("k", "two", "b"); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("rows = %d, want 1", count)
	}
	if got, _ := store.Get("k"); got != "two" {
		t.Errorf("got %q, want the replacement", got)
	}
}

func TestWithoutAKeyNothingIsStored(t *testing.T) {
	store, db := newStore(t, nil)

	if store.CanStore() {
		t.Error("CanStore is true without a key")
	}
	if err := store.Set("k", "v", ""); !errors.Is(err, secrets.ErrNoKey) {
		t.Errorf("Set error = %v, want ErrNoKey", err)
	}

	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("rows = %d, want 0", count)
	}
}

// A rotated encryption key must say so rather than hand back nonsense.
func TestWrongKeyReportsAndSaysWhatToDo(t *testing.T) {
	store, db := newStore(t, newBox(t))
	if err := store.Set("k", "v", ""); err != nil {
		t.Fatal(err)
	}

	rotated, err := UseDB(db.Conn(), db.DSN(), newBox(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = rotated.Get("k")
	if err == nil {
		t.Fatal("a value opened with the wrong key")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("set it again")) {
		t.Errorf("error = %q, want it to say what to do", err)
	}

	// Describe still works, so the UI can show that something is there.
	if _, err := rotated.Describe("k"); err != nil {
		t.Errorf("Describe failed with the wrong key: %v", err)
	}
}

// A nil store is a valid configuration; handlers ask it questions anyway.
func TestNilStoreIsUsable(t *testing.T) {
	var store *Store

	if store.CanStore() {
		t.Error("a nil store says it can store")
	}
	if _, err := store.Describe("k"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Describe = %v, want ErrNotFound", err)
	}
	if err := store.Delete("k"); err != nil {
		t.Errorf("Delete = %v, want nil", err)
	}
}

func TestGetUnknownAndDelete(t *testing.T) {
	store, _ := newStore(t, newBox(t))

	if _, err := store.Get("nothing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
	if err := store.Delete("nothing"); err != nil {
		t.Errorf("Delete of an absent setting errored: %v", err)
	}

	if err := store.Set("k", "v", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func TestSetRejectsEmptyNameOrValue(t *testing.T) {
	store, _ := newStore(t, newBox(t))

	if err := store.Set("  ", "v", ""); err == nil {
		t.Error("a setting was stored with no name")
	}
	// An empty value would read back as "configured" while signing nothing.
	if err := store.Set("k", "", ""); err == nil {
		t.Error("an empty value was stored")
	}
}

// PostgreSQL differs in the blob column type and the upsert.
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
	if _, err := db.Conn().Exec(`DROP TABLE IF EXISTS settings`); err != nil {
		t.Fatal(err)
	}

	store, err := UseDB(db.Conn(), db.DSN(), newBox(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Set("k", "one", "wilant"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("k", "two", "wilant"); err != nil {
		t.Fatalf("upsert failed on postgres: %v", err)
	}
	if got, _ := store.Get("k"); got != "two" {
		t.Errorf("got %q, want two", got)
	}
}
