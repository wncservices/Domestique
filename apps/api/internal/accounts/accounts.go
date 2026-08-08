// Package accounts stores the head units routes get pushed to.
//
// An account is not a user. Users come from Authelia and are never stored:
// Remote-User says who you are, Remote-Groups says what you may do, and that
// is the whole story. An account is a *connection to a provider* — a Garmin
// Connect or Wahoo account, with the label shown in the UI and, once the
// adapters exist, the credential to reach it.
//
// Accounts belong to the rider who linked them. The rider is the Authelia
// username, taken from the session at link time rather than configured, so
// there is no second place where somebody's name is written down and no way
// for the two to disagree.
package accounts

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/wncservices/domestique/apps/api/internal/dbx"
	"github.com/wncservices/domestique/apps/api/internal/model"
)

// ErrNotFound is returned for an account id nothing matches.
var ErrNotFound = errors.New("no such account")

// ErrExists is returned when a rider already linked that provider.
var ErrExists = errors.New("that provider is already linked")

// Store holds the linked accounts.
type Store struct {
	db      *sql.DB
	dialect dbx.Dialect
}

func schema() string {
	return `
CREATE TABLE IF NOT EXISTS accounts (
    id         TEXT PRIMARY KEY,
    provider   TEXT NOT NULL,
    rider      TEXT NOT NULL,
    label      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);`
}

// UseDB puts the accounts table in an already-open database — the same one
// holding the routes and the sync state, so a deployment needs exactly one.
func UseDB(db *sql.DB, dsn string) (*Store, error) {
	d, err := dbx.For(dsn)
	if err != nil {
		return nil, err
	}

	store := &Store{db: db, dialect: d}
	if _, err := db.Exec(schema()); err != nil {
		return nil, fmt.Errorf("migrate accounts table: %w", err)
	}
	return store, nil
}

// ID is how an account is named everywhere else: "garmin:wilant".
//
// One account per rider per provider, which is what makes this a safe primary
// key: nobody has two Garmin accounts on one head unit.
func ID(provider model.Provider, rider string) string {
	return fmt.Sprintf("%s:%s", provider, strings.ToLower(strings.TrimSpace(rider)))
}

var riderPattern = regexp.MustCompile(`^[a-zA-Z0-9._@-]+$`)

// Link records a rider's connection to a provider.
func (s *Store) Link(provider model.Provider, rider, label string) (model.Account, error) {
	rider = strings.TrimSpace(rider)
	if rider == "" {
		return model.Account{}, errors.New("accounts: no rider — who is linking this?")
	}
	// The rider comes from Authelia, but it lands in an id used across the
	// API, so keep it to something that survives a URL.
	if !riderPattern.MatchString(rider) {
		return model.Account{}, fmt.Errorf("accounts: rider %q has characters that cannot appear in an id", rider)
	}
	switch provider {
	case model.ProviderGarmin, model.ProviderWahoo:
	default:
		return model.Account{}, fmt.Errorf("accounts: unknown provider %q", provider)
	}

	id := ID(provider, rider)
	if _, err := s.Get(id); err == nil {
		return model.Account{}, fmt.Errorf("%w: %s", ErrExists, id)
	}

	if strings.TrimSpace(label) == "" {
		label = fmt.Sprintf("%s's %s", rider, providerLabel(provider))
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(s.dialect.Rebind(`
        INSERT INTO accounts (id, provider, rider, label, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?)`),
		id, string(provider), rider, label, now, now)
	if err != nil {
		return model.Account{}, err
	}

	return s.Get(id)
}

// Relabel changes the name shown in the UI. Nothing else about an account is
// editable: the provider and rider are what make it that account.
func (s *Store) Relabel(id, label string) (model.Account, error) {
	if strings.TrimSpace(label) == "" {
		return model.Account{}, errors.New("accounts: label cannot be empty")
	}

	result, err := s.db.Exec(
		s.dialect.Rebind(`UPDATE accounts SET label = ?, updated_at = ? WHERE id = ?`),
		label, time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return model.Account{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return model.Account{}, ErrNotFound
	}
	return s.Get(id)
}

// Unlink removes an account.
//
// The sync state for it is left alone deliberately. Re-linking the same
// provider gives the same id, and the recorded remote ids are still true —
// the routes really are still on the device.
func (s *Store) Unlink(id string) error {
	result, err := s.db.Exec(s.dialect.Rebind(`DELETE FROM accounts WHERE id = ?`), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns one account.
func (s *Store) Get(id string) (model.Account, error) {
	var a model.Account
	err := s.db.QueryRow(s.dialect.Rebind(`
        SELECT id, provider, rider, label FROM accounts WHERE id = ?`), id).
		Scan(&a.ID, &a.Provider, &a.Rider, &a.Label)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Account{}, ErrNotFound
	}
	return a, err
}

// List returns every linked account, in a stable order.
func (s *Store) List() ([]model.Account, error) {
	rows, err := s.db.Query(`
        SELECT id, provider, rider, label FROM accounts ORDER BY rider, provider`)
	if err != nil {
		return nil, fmt.Errorf("read accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []model.Account
	for rows.Next() {
		var a model.Account
		if err := rows.Scan(&a.ID, &a.Provider, &a.Rider, &a.Label); err != nil {
			return nil, fmt.Errorf("read accounts: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func providerLabel(p model.Provider) string {
	switch p {
	case model.ProviderGarmin:
		return "Garmin"
	case model.ProviderWahoo:
		return "Wahoo"
	default:
		return string(p)
	}
}
