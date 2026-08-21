package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wncservices/domestique/apps/api/internal/source"
)

func TestWithDBLockRunsUnprotectedWithNoConnection(t *testing.T) {
	ran := false
	withDBLock(context.Background(), nil, "k", func() { ran = true })
	if !ran {
		t.Fatal("fn did not run with a nil connection")
	}
}

// SQLite doesn't understand pg_try_advisory_xact_lock — this is the fail-open
// path every existing SQLite-backed test (the laptop path) already exercises
// indirectly through AutoImportTick, made explicit here.
func TestWithDBLockRunsUnprotectedOnANonPostgresEngine(t *testing.T) {
	db, err := source.OpenDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ran := false
	withDBLock(context.Background(), db.Conn(), "k", func() { ran = true })
	if !ran {
		t.Fatal("fn did not run against a sqlite connection")
	}
}

// The actual point of withDBLock: a second holder of the same key is skipped
// while the first is still inside fn, and can proceed once the first is
// done — the exact shape of two pods both ticking their own unattended
// import/push pass at once. Needs a real PostgreSQL; see
// internal/source/db_test.go's own postgresEnv for why SQLite can't cover
// this (it has no advisory locks at all).
func TestWithDBLockSerializesConcurrentHoldersOfTheSameKey(t *testing.T) {
	dsn := os.Getenv("DOMESTIQUE_TEST_POSTGRES")
	if dsn == "" {
		t.Skipf("set DOMESTIQUE_TEST_POSTGRES to a PostgreSQL DSN to run this")
	}
	db, err := source.OpenDB(dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	const key = "backgroundlock_test:serialize"

	holding := make(chan struct{})
	release := make(chan struct{})
	firstRan := make(chan struct{})
	go func() {
		withDBLock(context.Background(), db.Conn(), key, func() {
			close(firstRan)
			close(holding)
			<-release
		})
	}()

	select {
	case <-holding:
	case <-time.After(5 * time.Second):
		t.Fatal("first holder never acquired the lock")
	}

	// A second holder arriving while the first is still inside fn must be
	// skipped, not blocked — withDBLock is pg_try_advisory_xact_lock, not a
	// blocking wait, so a second pod's tick simply does nothing this time
	// rather than queueing up behind the first.
	secondRan := false
	withDBLock(context.Background(), db.Conn(), key, func() { secondRan = true })
	if secondRan {
		t.Fatal("second holder ran while the first still held the lock")
	}

	close(release)
	select {
	case <-firstRan:
	case <-time.After(5 * time.Second):
		t.Fatal("first holder's fn never ran")
	}

	// Give the first holder's transaction a moment to actually commit
	// (releasing the lock) after `release` unblocks it, then confirm a
	// third holder can now acquire the same key.
	deadline := time.Now().Add(5 * time.Second)
	for {
		thirdRan := false
		withDBLock(context.Background(), db.Conn(), key, func() { thirdRan = true })
		if thirdRan {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lock was never released after the first holder finished")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
