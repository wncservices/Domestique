package source

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, so the image stays scratch-ish

	"github.com/wncservices/domestique/apps/api/internal/gpx"
	"github.com/wncservices/domestique/apps/api/internal/model"
)

// maxGPXBytes caps an upload. Real routes are a few hundred kB; anything past
// this is a mistake or an attack, and we would rather say so than buffer it.
const maxGPXBytes = 20 << 20 // 20 MiB

const schema = `
CREATE TABLE IF NOT EXISTS routes (
    slug         TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    tags         TEXT NOT NULL DEFAULT '',
    targets      TEXT,
    enabled      INTEGER NOT NULL DEFAULT 1,
    gpx          BLOB NOT NULL,
    distance_m   REAL NOT NULL,
    ascent_m     REAL NOT NULL,
    start_lat    REAL NOT NULL,
    start_lng    REAL NOT NULL,
    point_count  INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    uploaded_by  TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
`

// DB stores routes as rows, with the GPX itself as a blob. This is the mode
// where riders upload through the web UI instead of committing files.
type DB struct {
	db  *sql.DB
	dsn string
}

// OpenDB opens (and migrates) a SQLite database. The DSN is a file path;
// ":memory:" works for tests.
func OpenDB(dsn string) (*DB, error) {
	// Create the parent directory: the obvious DSN is ./data/domestique.db,
	// and SQLite will not make the directory itself.
	if dir := filepath.Dir(dsn); dir != "" && dir != "." && !strings.Contains(dsn, ":memory:") {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create %s: %w", dir, err)
		}
	}

	// WAL keeps a reader (the UI) from blocking a writer (an upload).
	db, err := sql.Open("sqlite", dsn+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("open %s: %w", dsn, err)
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("migrate %s: %w", dsn, err)
	}
	return &DB{db: db, dsn: dsn}, nil
}

func (d *DB) Close() error     { return d.db.Close() }
func (d *DB) Describe() string { return "database " + d.dsn }

func (d *DB) List() ([]model.Route, []string, error) {
	rows, err := d.db.Query(`
        SELECT slug, name, description, tags, targets, enabled,
               distance_m, ascent_m, start_lat, start_lng, point_count,
               content_hash, updated_at, uploaded_by
        FROM routes WHERE enabled = 1 ORDER BY slug`)
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

func (d *DB) Track(slug string) ([]gpx.Point, error) {
	raw, err := d.GPX(slug)
	if err != nil {
		return nil, err
	}
	return gpx.ParsePoints(raw)
}

func (d *DB) GPX(slug string) ([]byte, error) {
	var raw []byte
	err := d.db.QueryRow(`SELECT gpx FROM routes WHERE slug = ?`, slug).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return raw, err
}

func (d *DB) Create(req CreateRequest) (model.Route, error) {
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

	slug, err := d.uniqueSlug(Slugify(name))
	if err != nil {
		return model.Route{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = d.db.Exec(`
        INSERT INTO routes (slug, name, description, tags, targets, enabled, gpx,
                            distance_m, ascent_m, start_lat, start_lng, point_count,
                            content_hash, uploaded_by, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		slug, name, req.Descript, joinList(req.Tags), nullableList(req.Targets), req.GPX,
		stats.DistanceM, stats.AscentM, stats.StartLat, stats.StartLng, stats.PointCount,
		gpx.ContentHash(points, name, req.Descript), req.UploadedBy, now, now)
	if err != nil {
		return model.Route{}, err
	}

	return d.get(slug)
}

func (d *DB) Update(slug string, req UpdateRequest) (model.Route, error) {
	current, err := d.get(slug)
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

	now := time.Now().UTC().Format(time.RFC3339)

	if req.GPX != nil {
		points, stats, err := analyse(req.GPX)
		if err != nil {
			return model.Route{}, err
		}
		current.Stats = stats
		current.ContentHash = gpx.ContentHash(points, current.Name, current.Description)
		_, err = d.db.Exec(`
            UPDATE routes SET name=?, description=?, tags=?, targets=?, enabled=?, gpx=?,
                   distance_m=?, ascent_m=?, start_lat=?, start_lng=?, point_count=?,
                   content_hash=?, updated_at=?
            WHERE slug=?`,
			current.Name, current.Description, joinList(current.Tags),
			nullableList(current.Targets), current.IsEnabled(), req.GPX,
			stats.DistanceM, stats.AscentM, stats.StartLat, stats.StartLng, stats.PointCount,
			current.ContentHash, now, slug)
		if err != nil {
			return model.Route{}, err
		}
		return d.get(slug)
	}

	// Metadata-only edit. The name feeds the content hash — a rename is a real
	// change as far as the providers are concerned — so recompute it.
	points, err := d.Track(slug)
	if err != nil {
		return model.Route{}, err
	}
	current.ContentHash = gpx.ContentHash(points, current.Name, current.Description)

	_, err = d.db.Exec(`
        UPDATE routes SET name=?, description=?, tags=?, targets=?, enabled=?,
               content_hash=?, updated_at=?
        WHERE slug=?`,
		current.Name, current.Description, joinList(current.Tags),
		nullableList(current.Targets), current.IsEnabled(),
		current.ContentHash, now, slug)
	if err != nil {
		return model.Route{}, err
	}
	return d.get(slug)
}

func (d *DB) Delete(slug string) error {
	result, err := d.db.Exec(`DELETE FROM routes WHERE slug = ?`, slug)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) get(slug string) (model.Route, error) {
	var (
		route   model.Route
		tags    string
		targets sql.NullString
		enabled bool
	)
	err := d.db.QueryRow(`
        SELECT slug, name, description, tags, targets, enabled,
               distance_m, ascent_m, start_lat, start_lng, point_count,
               content_hash, updated_at, uploaded_by
        FROM routes WHERE slug = ?`, slug).Scan(
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
func (d *DB) uniqueSlug(base string) (string, error) {
	if base == "" {
		base = "route"
	}
	candidate := base
	for attempt := 2; attempt < 1000; attempt++ {
		var exists int
		err := d.db.QueryRow(`SELECT COUNT(1) FROM routes WHERE slug = ?`, candidate).Scan(&exists)
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
