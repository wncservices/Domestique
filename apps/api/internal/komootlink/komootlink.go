// Package komootlink stores each rider's connection to their Komoot account.
//
// A rider connects through the UI: they type their Komoot email and password,
// the server signs in once, and **the password is discarded**. Komoot's login
// returns a user id and a session token, and the token is what the API
// actually wants afterwards — so that is what is kept, encrypted, and the
// password never reaches the database at all. If the token expires the rider
// reconnects, which is a far better failure than storing a reusable password
// to avoid it.
//
// One connection per rider, keyed to the Authelia username taken from the
// session — the same rule as a linked head unit, for the same reason: letting
// the request body name the rider would let somebody connect an account on
// another rider's behalf.
package komootlink

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wncservices/domestique/apps/api/internal/dbx"
	"github.com/wncservices/domestique/apps/api/internal/secrets"
)

// ErrNotFound means the rider has not connected Komoot.
var ErrNotFound = errors.New("no Komoot connection for that rider")

// Link is one rider's connection. The token is deliberately unexported: it
// leaves this package only through Credentials, so it cannot be serialised
// into an API response by accident.
type Link struct {
	Rider       string
	Email       string
	DisplayName string
	UserID      string
	UpdatedAt   time.Time
}

// Store holds the connections.
type Store struct {
	db      *sql.DB
	dialect dbx.Dialect
	box     *secrets.Box
}

func schema(d dbx.Dialect) string {
	return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS komoot_links (
    rider        TEXT PRIMARY KEY,
    email        TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    user_id      TEXT NOT NULL,
    token        %s NOT NULL,
    updated_at   TEXT NOT NULL
);`, d.Blob)
}

// UseDB puts the table in an already-open database, alongside the routes and
// the accounts.
//
// The box may be nil. Reads still work, so an operator who removes the key
// sees the connections they have rather than a crash — but Credentials and
// Save refuse, which is what keeps a token from ever being written in clear.
func UseDB(db *sql.DB, dsn string, box *secrets.Box) (*Store, error) {
	d, err := dbx.For(dsn)
	if err != nil {
		return nil, err
	}

	store := &Store{db: db, dialect: d, box: box}
	if _, err := db.Exec(schema(d)); err != nil {
		return nil, fmt.Errorf("create komoot_links table: %w", err)
	}
	return store, nil
}

func (s *Store) query(q string) string { return s.dialect.Rebind(q) }

// CanStore reports whether a connection can be saved at all. The UI asks
// before offering the form, so a rider is not invited to type a password that
// cannot be kept.
func (s *Store) CanStore() bool { return s != nil && s.box != nil }

// Save records a connection, replacing any the rider already had.
//
// Reconnecting is the documented fix for an expired token, so this is an
// upsert rather than an error on conflict.
func (s *Store) Save(rider, email, displayName, userID, token string) (Link, error) {
	rider = strings.ToLower(strings.TrimSpace(rider))
	if rider == "" {
		return Link{}, errors.New("no rider: a connection belongs to whoever made it")
	}
	if !s.CanStore() {
		return Link{}, secrets.ErrNoKey
	}
	if userID == "" || token == "" {
		return Link{}, errors.New("komoot login returned no session to store")
	}

	sealed, err := s.box.Seal(token)
	if err != nil {
		return Link{}, err
	}

	now := time.Now().UTC()
	_, err = s.db.Exec(s.query(`
INSERT INTO komoot_links (rider, email, display_name, user_id, token, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (rider) DO UPDATE SET
    email        = excluded.email,
    display_name = excluded.display_name,
    user_id      = excluded.user_id,
    token        = excluded.token,
    updated_at   = excluded.updated_at`),
		rider, strings.TrimSpace(email), strings.TrimSpace(displayName), userID, sealed,
		now.Format(time.RFC3339))
	if err != nil {
		return Link{}, fmt.Errorf("save komoot link: %w", err)
	}

	return Link{
		Rider:       rider,
		Email:       strings.TrimSpace(email),
		DisplayName: strings.TrimSpace(displayName),
		UserID:      userID,
		UpdatedAt:   now,
	}, nil
}

// Get returns a rider's connection without its token.
func (s *Store) Get(rider string) (Link, error) {
	rider = strings.ToLower(strings.TrimSpace(rider))

	var link Link
	var updated string
	err := s.db.QueryRow(s.query(`
SELECT rider, email, display_name, user_id, updated_at
FROM komoot_links WHERE rider = ?`), rider).
		Scan(&link.Rider, &link.Email, &link.DisplayName, &link.UserID, &updated)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Link{}, ErrNotFound
	case err != nil:
		return Link{}, err
	}

	link.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return link, nil
}

// Credentials returns what the Komoot client needs to resume a session.
//
// Separate from Get so that reading a connection for display never decrypts
// anything: the token is fetched only where it is about to be used.
func (s *Store) Credentials(rider string) (userID, token string, err error) {
	rider = strings.ToLower(strings.TrimSpace(rider))
	if !s.CanStore() {
		return "", "", secrets.ErrNoKey
	}

	var sealed []byte
	err = s.db.QueryRow(s.query(`SELECT user_id, token FROM komoot_links WHERE rider = ?`), rider).
		Scan(&userID, &sealed)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", "", ErrNotFound
	case err != nil:
		return "", "", err
	}

	token, err = s.box.Open(sealed)
	if err != nil {
		return "", "", fmt.Errorf("komoot token for %s: %w — reconnect to replace it", rider, err)
	}
	return userID, token, nil
}

// Delete removes a rider's connection. Deleting one that is not there is not
// an error: the caller wanted it gone, and it is.
func (s *Store) Delete(rider string) error {
	rider = strings.ToLower(strings.TrimSpace(rider))
	_, err := s.db.Exec(s.query(`DELETE FROM komoot_links WHERE rider = ?`), rider)
	return err
}
