package source

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel/attribute"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL, for the deployed instance
	_ "modernc.org/sqlite"             // SQLite, pure Go: no cgo, so the image stays small

	"github.com/wncservices/domestique/apps/api/internal/dbx"
	"github.com/wncservices/domestique/apps/api/internal/gpx"
	"github.com/wncservices/domestique/apps/api/internal/model"
)

// maxGPXBytes caps an upload. Real routes are a few hundred kB; anything past
// this is a mistake or an attack, and we would rather say so than buffer it.
const maxGPXBytes = 20 << 20 // 20 MiB

// routesSchema is the route table, in whichever types the engine uses.
func routesSchema(d dbx.Dialect) string {
	return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS routes (
    slug         TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    tags         TEXT NOT NULL DEFAULT '',
    targets      TEXT,
    enabled      %s NOT NULL DEFAULT TRUE,
    gpx          %s NOT NULL,
    distance_m   DOUBLE PRECISION NOT NULL,
    ascent_m     DOUBLE PRECISION NOT NULL,
    start_lat    DOUBLE PRECISION NOT NULL,
    start_lng    DOUBLE PRECISION NOT NULL,
    point_count  INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    uploaded_by  TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);`, d.Boolean, d.Blob)
}

// DB stores routes as rows, with the GPX itself as a blob. This is the mode
// where riders upload through the web UI instead of committing files.
type DB struct {
	db      *sql.DB
	dsn     string
	dialect dbx.Dialect
}

// OpenDB opens (and migrates) a route database.
//
// The DSN decides the engine: a postgres:// URL (or a key=value connection
// string) means PostgreSQL, anything else is a SQLite file path. ":memory:"
// works for tests.
func OpenDB(dsn string) (*DB, error) {
	d, err := dbx.For(dsn)
	if err != nil {
		return nil, err
	}

	connString := dsn
	if d.Name == dbx.SQLite.Name {
		// Create the parent directory: the obvious DSN is ./data/domestique.db,
		// and SQLite will not make the directory itself.
		// #nosec G703 -- the DSN is operator configuration (a flag, the config
		// file, or DOMESTIQUE_SOURCE_DSN), never user input.
		if dir := filepath.Dir(dsn); dir != "" && dir != "." && !strings.Contains(dsn, ":memory:") {
			if mkErr := os.MkdirAll(dir, 0o750); mkErr != nil {
				return nil, fmt.Errorf("create %s: %w", dir, mkErr)
			}
		}
		// WAL keeps a reader (the UI) from blocking a writer (an upload).
		connString = dsn + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	}

	db, err := otelsql.Open(d.Driver, connString, otelsql.WithAttributes(attribute.String("db.system", d.Name)))
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("open %s: %w", dbx.Redact(dsn), err)
	}
	if _, err := db.Exec(routesSchema(d)); err != nil {
		return nil, fmt.Errorf("migrate %s: %w", dbx.Redact(dsn), err)
	}

	return &DB{db: db, dsn: dsn, dialect: d}, nil
}

// Conn exposes the underlying connection so the sync state can share it
// rather than opening a second one to the same database.
func (d *DB) Conn() *sql.DB { return d.db }

// DSN is the connection string this was opened with. It carries a password;
// use dbx.Redact before showing it to anyone.
func (d *DB) DSN() string { return d.dsn }

// query rewrites the `?` placeholders for the engine in use.
func (d *DB) query(q string) string { return d.dialect.Rebind(q) }

func (d *DB) Close() error { return d.db.Close() }

// Describe names the source for humans. The DSN can carry a password, so it is
// redacted — this string reaches the log and the web UI.
func (d *DB) Describe() string {
	return fmt.Sprintf("%s database %s", d.dialect.Name, dbx.Redact(d.dsn))
}

func (d *DB) List(ctx context.Context) ([]model.Route, []string, error) {
	rows, err := d.db.QueryContext(ctx, d.query(`
        SELECT slug, name, description, tags, targets, enabled,
               distance_m, ascent_m, start_lat, start_lng, point_count,
               content_hash, updated_at, uploaded_by
        FROM routes WHERE enabled = TRUE ORDER BY slug`))
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	var routes []model.Route
	for rows.Next() {
		var (
			route   model.Route
			tags    string
			targets sql.NullString
			enabled bool
		)
		if err := rows.Scan(
			&route.Slug, &route.Name, &route.Description, &tags, &targets, &enabled,
			&route.Stats.DistanceM, &route.Stats.AscentM,
			&route.Stats.StartLat, &route.Stats.StartLng, &route.Stats.PointCount,
			&route.ContentHash, &route.UpdatedAt, &route.Owner,
		); err != nil {
			return nil, nil, err
		}
		route.Tags = splitList(tags)
		route.Enabled = &enabled
		if targets.Valid {
			list := splitList(targets.String)
			route.Targets = &list
		}
		route.Origin = "database"
		routes = append(routes, route)
	}
	return routes, nil, rows.Err()
}

func (d *DB) Track(ctx context.Context, slug string) ([]gpx.Point, error) {
	raw, err := d.GPX(ctx, slug)
	if err != nil {
		return nil, err
	}
	return gpx.ParsePoints(raw)
}

func (d *DB) GPX(ctx context.Context, slug string) ([]byte, error) {
	var raw []byte
	err := d.db.QueryRowContext(ctx, d.query(`SELECT gpx FROM routes WHERE slug = ?`), slug).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return raw, err
}

func (d *DB) Create(ctx context.Context, req CreateRequest) (model.Route, error) {
	points, stats, err := analyse(req.GPX)
	if err != nil {
		return model.Route{}, err
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = Titleize(strings.TrimSuffix(filepath.Base(req.Filename), filepath.Ext(req.Filename)))
	}
	if name == "" {
		name = "Untitled route"
	}

	slug, err := d.uniqueSlug(ctx, Slugify(name))
	if err != nil {
		return model.Route{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = d.db.ExecContext(ctx, d.query(`
        INSERT INTO routes (slug, name, description, tags, targets, enabled, gpx,
                            distance_m, ascent_m, start_lat, start_lng, point_count,
                            content_hash, uploaded_by, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, TRUE, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		slug, name, req.Descript, joinList(req.Tags), nullableList(req.Targets), req.GPX,
		stats.DistanceM, stats.AscentM, stats.StartLat, stats.StartLng, stats.PointCount,
		gpx.ContentHash(points, name, req.Descript), req.UploadedBy, now, now)
	if err != nil {
		return model.Route{}, err
	}

	return d.get(ctx, slug)
}

func (d *DB) Update(ctx context.Context, slug string, req UpdateRequest) (model.Route, error) {
	current, err := d.get(ctx, slug)
	if err != nil {
		return model.Route{}, err
	}

	if req.Name != nil {
		current.Name = strings.TrimSpace(*req.Name)
	}
	if req.Descript != nil {
		current.Description = *req.Descript
	}
	if req.Tags != nil {
		current.Tags = *req.Tags
	}
	if req.Targets != nil {
		current.Targets = req.Targets
	}
	if req.Enabled != nil {
		current.Enabled = req.Enabled
	}
	if req.Owner != nil {
		current.Owner = *req.Owner
	}

	now := time.Now().UTC().Format(time.RFC3339)

	if req.GPX != nil {
		points, stats, err := analyse(req.GPX)
		if err != nil {
			return model.Route{}, err
		}
		current.Stats = stats
		current.ContentHash = gpx.ContentHash(points, current.Name, current.Description)
		_, err = d.db.ExecContext(ctx, d.query(`
            UPDATE routes SET name=?, description=?, tags=?, targets=?, enabled=?, gpx=?,
                   distance_m=?, ascent_m=?, start_lat=?, start_lng=?, point_count=?,
                   content_hash=?, uploaded_by=?, updated_at=?
            WHERE slug=?`),
			current.Name, current.Description, joinList(current.Tags),
			nullableList(current.Targets), current.IsEnabled(), req.GPX,
			stats.DistanceM, stats.AscentM, stats.StartLat, stats.StartLng, stats.PointCount,
			current.ContentHash, current.Owner, now, slug)
		if err != nil {
			return model.Route{}, err
		}
		return d.get(ctx, slug)
	}

	// Metadata-only edit. The name feeds the content hash — a rename is a real
	// change as far as the providers are concerned — so recompute it.
	points, err := d.Track(ctx, slug)
	if err != nil {
		return model.Route{}, err
	}
	current.ContentHash = gpx.ContentHash(points, current.Name, current.Description)

	_, err = d.db.ExecContext(ctx, d.query(`
        UPDATE routes SET name=?, description=?, tags=?, targets=?, enabled=?,
               content_hash=?, uploaded_by=?, updated_at=?
        WHERE slug=?`),
		current.Name, current.Description, joinList(current.Tags),
		nullableList(current.Targets), current.IsEnabled(),
		current.ContentHash, current.Owner, now, slug)
	if err != nil {
		return model.Route{}, err
	}
	return d.get(ctx, slug)
}

func (d *DB) Delete(ctx context.Context, slug string) error {
	result, err := d.db.ExecContext(ctx, d.query(`DELETE FROM routes WHERE slug = ?`), slug)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) get(ctx context.Context, slug string) (model.Route, error) {
	var (
		route   model.Route
		tags    string
		targets sql.NullString
		enabled bool
	)
	err := d.db.QueryRowContext(ctx, d.query(`
        SELECT slug, name, description, tags, targets, enabled,
               distance_m, ascent_m, start_lat, start_lng, point_count,
               content_hash, updated_at, uploaded_by
        FROM routes WHERE slug = ?`), slug).Scan(
		&route.Slug, &route.Name, &route.Description, &tags, &targets, &enabled,
		&route.Stats.DistanceM, &route.Stats.AscentM,
		&route.Stats.StartLat, &route.Stats.StartLng, &route.Stats.PointCount,
		&route.ContentHash, &route.UpdatedAt, &route.Owner)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Route{}, ErrNotFound
	}
	if err != nil {
		return model.Route{}, err
	}

	route.Tags = splitList(tags)
	route.Enabled = &enabled
	if targets.Valid {
		list := splitList(targets.String)
		route.Targets = &list
	}
	route.Origin = "database"
	return route, nil
}

// uniqueSlug appends -2, -3, … so two rides up the same hill can share a name.
func (d *DB) uniqueSlug(ctx context.Context, base string) (string, error) {
	if base == "" {
		base = "route"
	}
	candidate := base
	for attempt := 2; attempt < 1000; attempt++ {
		var exists int
		err := d.db.QueryRowContext(ctx,
			d.query(`SELECT COUNT(1) FROM routes WHERE slug = ?`), candidate).Scan(&exists)
		if err != nil {
			return "", err
		}
		if exists == 0 {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, attempt)
	}
	return "", fmt.Errorf("could not find a free slug for %q", base)
}

func analyse(raw []byte) ([]gpx.Point, model.RouteStats, error) {
	if len(raw) == 0 {
		return nil, model.RouteStats{}, errors.New("empty GPX upload")
	}
	if len(raw) > maxGPXBytes {
		return nil, model.RouteStats{}, fmt.Errorf("GPX is %d bytes, over the %d byte limit",
			len(raw), maxGPXBytes)
	}
	points, err := gpx.ParsePoints(raw)
	if err != nil {
		return nil, model.RouteStats{}, err
	}
	return points, gpx.ComputeStats(points), nil
}

// Titleize turns a filename stem into a display name:
// "kemmelberg-loop" -> "Kemmelberg Loop".
func Titleize(slug string) string {
	words := strings.Split(strings.ReplaceAll(slug, "_", "-"), "-")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns a display name into a URL-safe slug.
func Slugify(name string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func joinList(items []string) string { return strings.Join(items, ",") }

func nullableList(items *[]string) any {
	if items == nil {
		return nil
	}
	return joinList(*items)
}
