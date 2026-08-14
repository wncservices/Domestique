// Package sessions holds a rider's OIDC login: a server-side session behind
// an opaque cookie value, not a JWT in the cookie itself.
//
// The difference matters for one reason — logout. A JWT in a cookie is valid
// until it expires no matter what the app does; "signing out" can only ever
// discard the browser's copy, not end the session, because there is no
// session, only a self-contained token nobody but the issuer can revoke. A
// row in this table is the session, so deleting it is signing out for real.
//
// Sealed with the same key as everything else a rider connects — Komoot,
// Garmin — via internal/secrets, following internal/providerlink and
// internal/settings' shape closely enough to be a copy of it: same nil-safe
// CanStore, same schema-as-constant-per-dialect.
package sessions

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wncservices/domestique/apps/api/internal/auth"
	"github.com/wncservices/domestique/apps/api/internal/dbx"
	"github.com/wncservices/domestique/apps/api/internal/secrets"
)

// Store holds sessions.
type Store struct {
	db      *sql.DB
	dialect dbx.Dialect
	box     *secrets.Box
}

// schema returns the DDL as a constant per engine — see providerlink.schema
// for why this is not one Sprintf: gosec's taint analysis follows a formatted
// string into every later query built from the same dialect.
func schema(d dbx.Dialect) string {
	const sqlite = `
CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,
    identity   BLOB NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);`
	const postgres = `
CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,
    identity   BYTEA NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);`

	if d.Name == dbx.Postgres.Name {
		return postgres
	}
	return sqlite
}

// UseDB puts the table in an already-open database.
//
// The box may be nil, in which case no session can be created — the same
// rule as providerlink and settings, for the same reason: a deployment
// without an encryption key has nowhere safe to put one.
func UseDB(db *sql.DB, dsn string, box *secrets.Box) (*Store, error) {
	d, err := dbx.For(dsn)
	if err != nil {
		return nil, err
	}

	store := &Store{db: db, dialect: d, box: box}
	if _, err := db.Exec(schema(d)); err != nil {
		return nil, fmt.Errorf("create sessions table: %w", err)
	}
	return store, nil
}

// CanStore reports whether a session can be created at all.
//
// Nil-safe on purpose, same rule as providerlink.Store.CanStore and
// settings.Store.CanStore: a Server built before its sessions store exists,
// or in a mode that never needs one, is a valid configuration. Do not
// "simplify" the receiver check away.
func (s *Store) CanStore() bool { return s != nil && s.box != nil }

// storedIdentity is what actually gets sealed — deliberately not the whole
// auth.Identity. Role is recomputed from Groups on every Lookup by
// auth.Authenticator.identifyFromSession, never trusted from storage, so it
// has no business being in the ciphertext in the first place.
type storedIdentity struct {
	User   string   `json:"user"`
	Name   string   `json:"name,omitempty"`
	Email  string   `json:"email,omitempty"`
	Groups []string `json:"groups,omitempty"`
}

// Create issues a session for id and returns the opaque cookie value.
//
// Opportunistically deletes expired rows first, so the table self-prunes
// without a background goroutine — nothing else in this codebase runs a
// ticker, and a personal deployment's session count never justifies adding
// one.
func (s *Store) Create(id auth.Identity, ttl time.Duration) (token string, expiresAt time.Time, err error) {
	if !s.CanStore() {
		return "", time.Time{}, secrets.ErrNoKey
	}
	if id.User == "" {
		return "", time.Time{}, fmt.Errorf("sessions: refusing to create a session for nobody")
	}

	now := time.Now().UTC()
	expiresAt = now.Add(ttl)

	raw, err := json.Marshal(storedIdentity{
		User: id.User, Name: id.Name, Email: id.Email, Groups: id.Groups,
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sessions: encoding identity: %w", err)
	}
	sealed, err := s.box.Seal(string(raw))
	if err != nil {
		return "", time.Time{}, err
	}

	tok, err := newToken()
	if err != nil {
		return "", time.Time{}, err
	}

	// #nosec G701 -- constant statement, bound parameters; see the same
	// pattern and reasoning in settings.Store.Set.
	if _, err := s.db.Exec(s.dialect.Rebind(
		`DELETE FROM sessions WHERE expires_at < ?`), now.Format(time.RFC3339)); err != nil {
		return "", time.Time{}, fmt.Errorf("sessions: pruning expired rows: %w", err)
	}

	// #nosec G701 -- constant statement, bound parameters.
	if _, err := s.db.Exec(s.dialect.Rebind(
		`INSERT INTO sessions (token, identity, created_at, expires_at) VALUES (?, ?, ?, ?)`),
		tok, sealed, now.Format(time.RFC3339), expiresAt.Format(time.RFC3339)); err != nil {
		return "", time.Time{}, fmt.Errorf("sessions: creating session: %w", err)
	}
	return tok, expiresAt, nil
}

// Lookup resolves a session token to who it belongs to. Satisfies
// auth.SessionLookup.
//
// A DB error, a corrupt or tampered ciphertext, and an expired row are all
// just "no session" — Identify has no channel to surface an error through,
// so this can never be allowed to be one the caller has to check. An expired
// row found here is deleted opportunistically rather than left for the next
// Create to clean up, since a token that outlives its own row would
// otherwise keep answering true for as long as nothing else logs in.
func (s *Store) Lookup(token string) (auth.Identity, bool) {
	if !s.CanStore() || token == "" {
		return auth.Identity{}, false
	}

	var sealed []byte
	var expires string
	// #nosec G701 -- constant statement, bound parameter.
	err := s.db.QueryRow(s.dialect.Rebind(
		`SELECT identity, expires_at FROM sessions WHERE token = ?`), token).
		Scan(&sealed, &expires)
	if err != nil {
		return auth.Identity{}, false
	}

	expiresAt, err := time.Parse(time.RFC3339, expires)
	if err != nil || time.Now().UTC().After(expiresAt) {
		_ = s.Delete(token)
		return auth.Identity{}, false
	}

	raw, err := s.box.Open(sealed)
	if err != nil {
		return auth.Identity{}, false
	}
	var stored storedIdentity
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return auth.Identity{}, false
	}
	return auth.Identity{
		User: stored.User, Name: stored.Name, Email: stored.Email, Groups: stored.Groups,
	}, true
}

// Delete ends a session. Deleting one that is not there is not an error —
// same rule as providerlink.Delete and settings.Delete: a rider signing out
// twice, or a client replaying a stale cookie, is not a failure.
func (s *Store) Delete(token string) error {
	if s == nil || token == "" {
		return nil
	}
	// #nosec G701 -- constant statement, bound parameter.
	_, err := s.db.Exec(s.dialect.Rebind(`DELETE FROM sessions WHERE token = ?`), token)
	return err
}

// newToken is 32 random bytes, URL-safe base64 — opaque, unguessable, and
// plain enough to be a cookie value with no further encoding.
func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("sessions: generating token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
