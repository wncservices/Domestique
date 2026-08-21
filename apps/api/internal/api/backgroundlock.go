package api

import (
	"context"
	"database/sql"

	"github.com/wncservices/domestique/apps/api/internal/source"
)

// backgroundSyncLockKey guards every unattended path that imports from a
// provider or pushes to a device — AutoImportTick's own push-after-import
// and autoSyncIfEnabled's push-on-edit share one key because the thing
// being protected against is the same either way: two of these running at
// once, reaching a head unit twice. One replica normally makes that
// impossible on its own, but it stops being impossible the moment two pods
// are briefly alive together — the ordinary rolling-update overlap
// domestique-chart's own strategy: comment already accepts as a narrow,
// stated risk, and, at much greater length, Argo Rollouts blue-green's
// prePromotionAnalysis/postPromotionAnalysis window and scale-down grace
// period, where the outgoing pod keeps running (and keeps ticking its own
// 30-minute loop) well after the new one has already gone active.
const backgroundSyncLockKey = "domestique:background-sync"

// dbConn returns the raw connection backing s.Source, or nil if there isn't
// one — a fake Library in a test, for instance. withDBLock treats nil the
// same as "locking isn't available here": run unprotected rather than skip.
func (s *Server) dbConn() *sql.DB {
	db, ok := s.Source.(*source.DB)
	if !ok {
		return nil
	}
	return db.Conn()
}

// withDBLock runs fn only while holding a Postgres advisory lock scoped to
// a single transaction — held for exactly as long as that transaction stays
// open, released the moment it commits or rolls back, visible across every
// connection to the same database regardless of which pod opened it. That
// last part is the whole point: two pods sharing one Postgres instance (the
// two live during a blue-green promotion, or the brief overlap an ordinary
// rolling update already allows) see the same lock.
//
// Fails open, not closed, at every step — no connection, a begin/query
// error, or an engine that doesn't understand this SQL (SQLite, the laptop
// path, single-process by construction and so with nothing to protect
// against here) all just run fn unprotected, exactly today's behavior
// before this existed, rather than silently stopping auto-sync over an
// unrelated locking problem. Only an actually-held competing lock skips fn.
func withDBLock(ctx context.Context, db *sql.DB, key string, fn func()) {
	if db == nil {
		fn()
		return
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		fn()
		return
	}

	var locked bool
	if err := tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock(hashtext($1))", key).Scan(&locked); err != nil {
		_ = tx.Rollback()
		fn()
		return
	}
	if !locked {
		_ = tx.Rollback()
		return
	}

	defer func() { _ = tx.Commit() }()
	fn()
}
