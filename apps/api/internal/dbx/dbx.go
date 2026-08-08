// Package dbx is the small amount of SQL plumbing shared by everything that
// talks to a database: which engine a DSN means, where the two disagree, and
// how to say a connection string out loud without leaking the password.
//
// Two engines because they serve different jobs. PostgreSQL is what the
// cluster runs — one more database next to the ones already there. SQLite is a
// file, which is right for a laptop.
package dbx

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
type Dialect struct {
	// Name is the engine: "sqlite" or "postgres".
	Name string
	// Driver is the database/sql driver name.
	Driver string
	// Blob is the column type for binary data.
	Blob string
	// Boolean is the column type for a true/false column.
	Boolean string
	// placeholder renders the nth (1-based) bind parameter.
	placeholder func(n int) string
}

var (
	SQLite = Dialect{
		Name:        "sqlite",
		Driver:      "sqlite",
		Blob:        "BLOB",
		Boolean:     "INTEGER",
		placeholder: func(int) string { return "?" },
	}

	Postgres = Dialect{
		Name:        "postgres",
		Driver:      "pgx",
		Blob:        "BYTEA",
		Boolean:     "BOOLEAN",
		placeholder: func(n int) string { return "$" + strconv.Itoa(n) },
	}
)

// Rebind turns the `?` placeholders queries are written with into whatever the
// engine wants. Writing every query twice would be worse.
func (d Dialect) Rebind(query string) string {
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

// For picks an engine from a DSN. A postgres:// or postgresql:// URL (or a
// key=value connection string) means PostgreSQL; anything else is taken as a
// SQLite file path.
func For(dsn string) (Dialect, error) {
	trimmed := strings.TrimSpace(dsn)
	if trimmed == "" {
		return Dialect{}, fmt.Errorf("empty database DSN")
	}

	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "postgres://"), strings.HasPrefix(lower, "postgresql://"):
		if _, err := url.Parse(trimmed); err != nil {
			return Dialect{}, fmt.Errorf("unparseable PostgreSQL DSN: %w", err)
		}
		return Postgres, nil

	// A key=value connection string is the other way PostgreSQL is written.
	case strings.Contains(lower, "host=") && strings.Contains(lower, "dbname="):
		return Postgres, nil

	default:
		return SQLite, nil
	}
}

// Redact removes the password from a DSN before it reaches a log line or the
// UI. The DSN is the one piece of config that routinely carries a credential.
func Redact(dsn string) string {
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
