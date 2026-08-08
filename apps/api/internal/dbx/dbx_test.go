package dbx

import (
	"strings"
	"testing"
)

func TestForPicksTheEngine(t *testing.T) {
	for dsn, want := range map[string]string{
		"postgres://user:pass@host:5432/domestique":   "postgres",
		"postgresql://user@host/domestique":           "postgres",
		"POSTGRES://user@host/domestique":             "postgres",
		"host=db.internal dbname=domestique user=app": "postgres",
		"data/domestique.db":                          "sqlite",
		"/var/lib/domestique/routes.db":               "sqlite",
		":memory:":                                    "sqlite",
		"./relative.db":                               "sqlite",
	} {
		got, err := For(dsn)
		if err != nil {
			t.Errorf("For(%q): %v", dsn, err)
			continue
		}
		if got.Name != want {
			t.Errorf("For(%q) = %q, want %q", dsn, got.Name, want)
		}
	}
}

func TestForRejectsEmpty(t *testing.T) {
	if _, err := For("   "); err == nil {
		t.Error("empty DSN accepted")
	}
}

// Queries are written once with `?`; PostgreSQL needs them numbered.
func TestRebind(t *testing.T) {
	query := `SELECT a FROM t WHERE b = ? AND c = ? AND d = ?`

	if got := SQLite.Rebind(query); got != query {
		t.Errorf("sqlite rewrote the query: %q", got)
	}

	want := `SELECT a FROM t WHERE b = $1 AND c = $2 AND d = $3`
	if got := Postgres.Rebind(query); got != want {
		t.Errorf("postgres rebind = %q, want %q", got, want)
	}
}

func TestRebindCountsPastNine(t *testing.T) {
	// Ten placeholders: $10 must not come out as $1 followed by a 0.
	query := strings.Repeat("?,", 10)
	got := Postgres.Rebind(query)
	if !strings.Contains(got, "$10") {
		t.Errorf("rebind = %q, want it to reach $10", got)
	}
}

// The DSN is the one piece of config that routinely carries a credential, and
// it reaches both the log and the web UI.
func TestRedactHidesThePassword(t *testing.T) {
	for dsn, mustNotContain := range map[string]string{
		"postgres://domestique:hunter2@db.internal:5432/domestique": "hunter2",
		"postgresql://user:s3cr3t@host/db?sslmode=require":          "s3cr3t",
		"host=db dbname=domestique user=app password=hunter2":       "hunter2",
	} {
		got := Redact(dsn)
		if strings.Contains(got, mustNotContain) {
			t.Errorf("Redact(%q) leaked the password: %q", dsn, got)
		}
		// Still useful afterwards — the host is how you tell instances apart.
		if !strings.Contains(got, "db") {
			t.Errorf("Redact(%q) = %q, want the host still visible", dsn, got)
		}
	}
}

func TestRedactLeavesHarmlessDSNsAlone(t *testing.T) {
	for _, dsn := range []string{
		"data/domestique.db",
		"postgres://user@host/db",
	} {
		if got := Redact(dsn); got != dsn {
			t.Errorf("Redact(%q) = %q, want it unchanged", dsn, got)
		}
	}
}
