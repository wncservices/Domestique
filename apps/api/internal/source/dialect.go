package source

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// dialect is the handful of places SQLite and PostgreSQL disagree.
//
// Two engines rather than one because they serve different jobs: SQLite is a
// file, which is right for a laptop or a single container with a volume;
// PostgreSQL is what this actually runs against in the cluster, where a
// database already exists and a PersistentVolume for one file would be silly.
//
// Everything outside this file is written once and works on both.
type dialect struct {
	name string
	// driver is the database/sql driver name.
	driver string
	// blob is the column type for the GPX bytes.
	blob string
	// boolean is the column type for a true/false column.
	boolean string
	// placeholder renders the nth (1-based) bind parameter.
	placeholder func(n int) string
}

var (
	sqliteDialect = dialect{
		name:        "sqlite",
		driver:      "sqlite",
		blob:        "BLOB",
		boolean:     "INTEGER",
		placeholder: func(int) string { return "?" },
	}

	postgresDialect = dialect{
		name:        "postgres",
		driver:      "pgx",
		blob:        "BYTEA",
		boolean:     "BOOLEAN",
		placeholder: func(n int) string { return "$" + strconv.Itoa(n) },
	}
)

// rebind turns the `?` placeholders the queries are written with into whatever
// the dialect wants. Writing every query twice would be worse.
func (d dialect) rebind(query string) string {
	if d.placeholder(1) == "?" {
		return query
	}

	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString(d.placeholder(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// schema is the table definition, in whichever types the dialect uses.
func (d dialect) schema() string {
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
);`, d.boolean, d.blob)
}

// dialectFor picks an engine from the DSN.
//
// A postgres:// or postgresql:// URL means PostgreSQL; anything else is taken
// as a SQLite file path, which keeps `--db ./data/domestique.db` working.
func dialectFor(dsn string) (dialect, error) {
	trimmed := strings.TrimSpace(dsn)
	if trimmed == "" {
		return dialect{}, fmt.Errorf("empty database DSN")
	}

	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "postgres://"), strings.HasPrefix(lower, "postgresql://"):
		if _, err := url.Parse(trimmed); err != nil {
			return dialect{}, fmt.Errorf("unparseable PostgreSQL DSN: %w", err)
		}
		return postgresDialect, nil

	// A key=value connection string is the other way PostgreSQL is written.
	case strings.Contains(lower, "host=") && strings.Contains(lower, "dbname="):
		return postgresDialect, nil

	default:
		return sqliteDialect, nil
	}
}

// redactDSN removes the password before a DSN reaches a log line or the UI.
//
// The DSN is the one piece of config that routinely carries a credential, and
// Describe() puts it on screen.
func redactDSN(dsn string) string {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.User == nil {
		// Not a URL, or no credentials in it. Fall back to stripping a
		// key=value password if there is one.
		return redactKeyValuePassword(dsn)
	}

	if _, hasPassword := parsed.User.Password(); hasPassword {
		parsed.User = url.UserPassword(parsed.User.Username(), "xxxxx")
	}
	return parsed.String()
}

func redactKeyValuePassword(dsn string) string {
	fields := strings.Fields(dsn)
	for i, field := range fields {
		if strings.HasPrefix(strings.ToLower(field), "password=") {
			fields[i] = "password=xxxxx"
		}
	}
	return strings.Join(fields, " ")
}
