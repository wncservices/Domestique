package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL
	_ "modernc.org/sqlite"             // SQLite

	"github.com/wncservices/domestique/apps/api/internal/dbx"
)

// DBStore keeps sync state in a database table.
//
// This is the same information the JSON file held — which route is on which
// account, under which remote id, at which content hash — but somewhere a
// deployment already has. With routes in PostgreSQL, a file store meant also
// mounting a volume for one small file, and losing that volume meant pushing
// every route to every device again.
//
// The table lives alongside the routes when both share a DSN, which is the
// normal arrangement.
type DBStore struct {
	db      *sql.DB
	dsn     string
	dialect dbx.Dialect
	// ownsDB is false when the connection was handed in, in which case closing
	// it is somebody else's job.
	ownsDB bool
}

func stateSchema() string {
	return `
CREATE TABLE IF NOT EXISTS sync_state (
    account_id   TEXT NOT NULL,
    slug         TEXT NOT NULL,
    remote_id    TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    name         TEXT NOT NULL DEFAULT '',
    updated_at   TEXT NOT NULL,
    PRIMARY KEY (account_id, slug)
);`
}

// OpenDB opens a database and prepares the state table.
func OpenDB(dsn string) (*DBStore, error) {
	d, err := dbx.For(dsn)
	if err != nil {
		return nil, err
	}

	connString := dsn
	if d.Name == dbx.SQLite.Name {
		connString = dsn + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	}

	db, err := sql.Open(d.Driver, connString)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("open state database %s: %w", dbx.Redact(dsn), err)
	}

	store := &DBStore{db: db, dsn: dsn, dialect: d, ownsDB: true}
	if err := store.migrate(); err != nil {
		return nil, err
	}
	return store, nil
}

// UseDB puts the state table in a database somebody else already opened —
// normally the one holding the routes, so a deployment needs exactly one.
func UseDB(db *sql.DB, dsn string) (*DBStore, error) {
	d, err := dbx.For(dsn)
	if err != nil {
		return nil, err
	}

	store := &DBStore{db: db, dsn: dsn, dialect: d}
	if err := store.migrate(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *DBStore) migrate() error {
	if _, err := s.db.Exec(stateSchema()); err != nil {
		return fmt.Errorf("migrate state table in %s: %w", dbx.Redact(s.dsn), err)
	}
	return nil
}

// Close releases the connection, when this store opened it.
func (s *DBStore) Close() error {
	if !s.ownsDB {
		return nil
	}
	return s.db.Close()
}

// Describe names the store for humans, with the password removed.
func (s *DBStore) Describe() string {
	return fmt.Sprintf("%s database %s", s.dialect.Name, dbx.Redact(s.dsn))
}

func (s *DBStore) All(ctx context.Context) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT account_id, slug, remote_id, content_hash, name, updated_at
        FROM sync_state ORDER BY account_id, slug`)
	if err != nil {
		return nil, fmt.Errorf("read sync state: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.AccountID, &e.Slug, &e.RemoteID,
			&e.ContentHash, &e.Name, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("read sync state: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read sync state: %w", err)
	}
	return out, nil
}

func (s *DBStore) ForAccount(ctx context.Context, accountID string) (map[string]Entry, error) {
	entries, err := s.All(ctx)
	if err != nil {
		return nil, err
	}

	out := map[string]Entry{}
	for _, e := range entries {
		if e.AccountID == accountID {
			out[e.Slug] = e
		}
	}
	return out, nil
}

func (s *DBStore) Record(ctx context.Context, e Entry) error {
	e.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(ctx, s.dialect.Rebind(`
        INSERT INTO sync_state (account_id, slug, remote_id, content_hash, name, updated_at)
        VALUES (?, ?, ?, ?, ?, ?)
        ON CONFLICT (account_id, slug) DO UPDATE SET
            remote_id = excluded.remote_id,
            content_hash = excluded.content_hash,
            name = excluded.name,
            updated_at = excluded.updated_at`),
		e.AccountID, e.Slug, e.RemoteID, e.ContentHash, e.Name, e.UpdatedAt)
	return err
}

func (s *DBStore) Forget(ctx context.Context, accountID, slug string) error {
	_, err := s.db.ExecContext(ctx,
		s.dialect.Rebind(`DELETE FROM sync_state WHERE account_id = ? AND slug = ?`),
		accountID, slug)
	return err
}
