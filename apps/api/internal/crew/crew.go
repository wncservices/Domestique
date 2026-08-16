// Package crew controls who a rider's routes may reach beyond their own
// devices.
//
// Without it, a route with no explicit targets goes to every linked account
// in the deployment (config.TargetsFor's documented default), and nothing
// stops a rider from naming another rider's account directly — there is no
// consent or relationship check anywhere on that path. A crew is that
// relationship: a rider creates one and becomes its owner, other riders
// request to join, and only the owner approves or denies. A route may then
// be shared to a crew the route's owner belongs to, which resolves — at
// push time, not at share time — to every currently approved member's
// accounts. Membership is deliberately not baked into a route when it is
// shared: a member leaving or being removed takes effect on the next push
// with nobody touching the route.
//
// "Crew" rather than "group" on purpose. This codebase already uses "group"
// for Authelia/Auth0 role-mapping groups (see internal/auth), an unrelated
// concept — reusing the word here would read as the same thing in code and
// docs when it is not.
package crew

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/wncservices/domestique/apps/api/internal/dbx"
)

// ErrNotFound is returned for a crew id nothing matches.
var ErrNotFound = errors.New("no such crew")

// ErrAlreadyMember is returned when a rider requests to join a crew they
// already belong to, or already have a pending request for.
var ErrAlreadyMember = errors.New("already a member or already requested")

// MemberStatus is where a rider stands with a crew.
type MemberStatus string

const (
	StatusPending  MemberStatus = "pending"
	StatusApproved MemberStatus = "approved"
)

// Crew is a set of riders who trust each other with their routes.
type Crew struct {
	ID        string
	Name      string
	Owner     string
	CreatedAt string
	UpdatedAt string
}

// Member is one rider's standing with one crew.
type Member struct {
	CrewID      string
	Rider       string
	Status      MemberStatus
	RequestedAt string
	DecidedBy   string
	DecidedAt   string
}

// MemberSet is which riders currently, approvedly, belong to which crews —
// keyed by crew id. It exists as its own type (rather than a bare map) so
// the "is this rider a current member" check has exactly one definition,
// used both when resolving a route's push targets and when validating a
// route's targets at write time.
type MemberSet map[string][]string

// Has reports whether rider is a current, approved member of crewID.
func (m MemberSet) Has(crewID, rider string) bool {
	for _, r := range m[crewID] {
		if r == rider {
			return true
		}
	}
	return false
}

// Snapshot is every crew and its current approved membership, fetched
// together because everywhere either is needed, both are — resolving a
// route's targets needs to know both which crews exist and who is in them.
type Snapshot struct {
	Crews          []Crew
	ApprovedRiders MemberSet
}

// Store holds crews and their membership.
type Store struct {
	db      *sql.DB
	dialect dbx.Dialect
}

func schema() string {
	return `
CREATE TABLE IF NOT EXISTS crews (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    owner      TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS crew_members (
    crew_id      TEXT NOT NULL,
    rider        TEXT NOT NULL,
    status       TEXT NOT NULL,
    requested_at TEXT NOT NULL,
    decided_by   TEXT NOT NULL DEFAULT '',
    decided_at   TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (crew_id, rider)
);`
}

// UseDB puts the crew tables in an already-open database — the same one
// holding the routes, accounts, and sync state, so a deployment needs
// exactly one.
func UseDB(db *sql.DB, dsn string) (*Store, error) {
	d, err := dbx.For(dsn)
	if err != nil {
		return nil, err
	}

	store := &Store{db: db, dialect: d}
	if _, err := db.Exec(schema()); err != nil {
		return nil, fmt.Errorf("migrate crew tables: %w", err)
	}
	return store, nil
}

// idPrefix marks a target as a crew rather than the raw account ids
// Targets held before this package existed. It is what lets a route's
// Targets list hold crew ids in the same field/namespace a legacy account
// id occupies, and lets a resolver tell the two apart with a string check
// rather than a lookup — a stale or foreign account id never starts with
// this prefix, so it can never accidentally resolve as a crew.
const idPrefix = "crew:"

// nonSlug matches everything a crew id may not contain. Duplicated from
// source.Slugify's regex rather than imported: source pulls in the whole
// route/GPX/tracing stack for one regexp, which this small package has no
// other reason to depend on.
var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// Create makes a new crew and enrolls its owner as an approved member —
// there is no separate "or you own it" case anywhere membership is
// checked, because the owner already appears in ApprovedRiders like anyone
// else.
func (s *Store) Create(ctx context.Context, name, owner string) (Crew, error) {
	name = strings.TrimSpace(name)
	owner = strings.TrimSpace(owner)
	if name == "" {
		return Crew{}, errors.New("crew: name is required")
	}
	if owner == "" {
		return Crew{}, errors.New("crew: no owner — who is creating this?")
	}

	id, err := s.uniqueID(ctx, slugify(name))
	if err != nil {
		return Crew{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, s.dialect.Rebind(`
        INSERT INTO crews (id, name, owner, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?)`),
		id, name, owner, now, now); err != nil {
		return Crew{}, fmt.Errorf("create crew: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, s.dialect.Rebind(`
        INSERT INTO crew_members (crew_id, rider, status, requested_at, decided_by, decided_at)
        VALUES (?, ?, ?, ?, ?, ?)`),
		id, owner, string(StatusApproved), now, owner, now); err != nil {
		return Crew{}, fmt.Errorf("enroll crew owner: %w", err)
	}

	return s.Get(ctx, id)
}

// uniqueID appends -2, -3, … so two crews with the same name don't collide.
func (s *Store) uniqueID(ctx context.Context, base string) (string, error) {
	if base == "" {
		base = "crew"
	}
	candidate := idPrefix + base
	for attempt := 2; attempt < 1000; attempt++ {
		var exists int
		err := s.db.QueryRowContext(ctx,
			s.dialect.Rebind(`SELECT COUNT(1) FROM crews WHERE id = ?`), candidate).Scan(&exists)
		if err != nil {
			return "", err
		}
		if exists == 0 {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s%s-%d", idPrefix, base, attempt)
	}
	return "", fmt.Errorf("could not find a free id for %q", base)
}

// Get returns one crew.
func (s *Store) Get(ctx context.Context, id string) (Crew, error) {
	var c Crew
	err := s.db.QueryRowContext(ctx, s.dialect.Rebind(`
        SELECT id, name, owner, created_at, updated_at FROM crews WHERE id = ?`), id).
		Scan(&c.ID, &c.Name, &c.Owner, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Crew{}, ErrNotFound
	}
	return c, err
}

// List returns every crew, in a stable order.
func (s *Store) List(ctx context.Context) ([]Crew, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, name, owner, created_at, updated_at FROM crews ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("read crews: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Crew
	for rows.Next() {
		var c Crew
		if err := rows.Scan(&c.ID, &c.Name, &c.Owner, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("read crews: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Snapshot returns every crew and its current approved membership in two
// queries, not one per crew — everywhere a caller needs one, it needs the
// other too (resolving a route's targets, rendering the crews page), so
// this is what both use.
func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	crews, err := s.List(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	rows, err := s.db.QueryContext(ctx, s.dialect.Rebind(`
        SELECT crew_id, rider FROM crew_members WHERE status = ?`), string(StatusApproved))
	if err != nil {
		return Snapshot{}, fmt.Errorf("read crew membership: %w", err)
	}
	defer func() { _ = rows.Close() }()

	approved := MemberSet{}
	for rows.Next() {
		var crewID, rider string
		if err := rows.Scan(&crewID, &rider); err != nil {
			return Snapshot{}, fmt.Errorf("read crew membership: %w", err)
		}
		approved[crewID] = append(approved[crewID], rider)
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, err
	}

	return Snapshot{Crews: crews, ApprovedRiders: approved}, nil
}

// Members returns every rider's standing with a crew — pending and
// approved together, which is what the owner's own view needs.
func (s *Store) Members(ctx context.Context, crewID string) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, s.dialect.Rebind(`
        SELECT crew_id, rider, status, requested_at, decided_by, decided_at
        FROM crew_members WHERE crew_id = ? ORDER BY requested_at`), crewID)
	if err != nil {
		return nil, fmt.Errorf("read crew members: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Member
	for rows.Next() {
		var m Member
		var status string
		if err := rows.Scan(&m.CrewID, &m.Rider, &status, &m.RequestedAt, &m.DecidedBy, &m.DecidedAt); err != nil {
			return nil, fmt.Errorf("read crew members: %w", err)
		}
		m.Status = MemberStatus(status)
		out = append(out, m)
	}
	return out, rows.Err()
}

// RequestJoin records a rider's request to join a crew. It is a no-op
// error, not silent, if the rider already has a pending or approved row —
// callers should not double-request over an existing one.
func (s *Store) RequestJoin(ctx context.Context, crewID, rider string) (Member, error) {
	rider = strings.TrimSpace(rider)
	if rider == "" {
		return Member{}, errors.New("crew: no rider — who is requesting to join?")
	}
	if _, err := s.Get(ctx, crewID); err != nil {
		return Member{}, err
	}

	var exists int
	if err := s.db.QueryRowContext(ctx, s.dialect.Rebind(`
        SELECT COUNT(1) FROM crew_members WHERE crew_id = ? AND rider = ?`),
		crewID, rider).Scan(&exists); err != nil {
		return Member{}, err
	}
	if exists > 0 {
		return Member{}, ErrAlreadyMember
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, s.dialect.Rebind(`
        INSERT INTO crew_members (crew_id, rider, status, requested_at, decided_by, decided_at)
        VALUES (?, ?, ?, ?, '', '')`),
		crewID, rider, string(StatusPending), now); err != nil {
		return Member{}, fmt.Errorf("request to join crew: %w", err)
	}

	return Member{CrewID: crewID, Rider: rider, Status: StatusPending, RequestedAt: now}, nil
}

// Approve grants a pending request, recording who decided it and when.
func (s *Store) Approve(ctx context.Context, crewID, rider, decidedBy string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx, s.dialect.Rebind(`
        UPDATE crew_members SET status = ?, decided_by = ?, decided_at = ?
        WHERE crew_id = ? AND rider = ?`),
		string(StatusApproved), decidedBy, now, crewID, rider)
	if err != nil {
		return fmt.Errorf("approve crew member: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

// Deny removes a pending request. The rider may request again later —
// denying is not a ban, it is declining this particular request.
func (s *Store) Deny(ctx context.Context, crewID, rider string) error {
	return s.delete(ctx, crewID, rider, string(StatusPending))
}

// Remove takes an approved member out of a crew — an owner removing
// someone, or a member leaving on their own, are the same operation from
// the store's point of view; the API layer decides who may call it for
// whom.
func (s *Store) Remove(ctx context.Context, crewID, rider string) error {
	return s.delete(ctx, crewID, rider, string(StatusApproved))
}

func (s *Store) delete(ctx context.Context, crewID, rider, status string) error {
	result, err := s.db.ExecContext(ctx, s.dialect.Rebind(`
        DELETE FROM crew_members WHERE crew_id = ? AND rider = ? AND status = ?`),
		crewID, rider, status)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a crew and its membership. A route that named this crew
// in its targets simply stops resolving anywhere beyond the route owner's
// own accounts on the next push — nothing else to clean up.
func (s *Store) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, s.dialect.Rebind(`DELETE FROM crews WHERE id = ?`), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	if _, err := s.db.ExecContext(ctx, s.dialect.Rebind(`DELETE FROM crew_members WHERE crew_id = ?`), id); err != nil {
		return fmt.Errorf("delete crew membership: %w", err)
	}
	return nil
}
