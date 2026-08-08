// Acceptance tests for the CLI: every command, driven through run() the way
// the shell drives it, against real files in a temp directory.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/accounts"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/source"
)

const testConfig = `
source:
  kind: fs
  path: ./routes
`

const exampleGPX = `<?xml version="1.0" encoding="UTF-8"?>
<gpx version="1.1" creator="test" xmlns="http://www.topografix.com/GPX/1/1">
  <trk><trkseg>
    <trkpt lat="50.7920" lon="2.8180"><ele>42.0</ele></trkpt>
    <trkpt lat="50.7982" lon="2.8344"><ele>128.0</ele></trkpt>
    <trkpt lat="50.8007" lon="2.8437"><ele>139.0</ele></trkpt>
  </trkseg></trk>
</gpx>`

// workspace builds a temp directory with a config and one route, and makes it
// the working directory so the CLI's relative defaults apply.
func workspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	write(t, filepath.Join(dir, "domestique.yaml"), testConfig)
	routeDir := filepath.Join(dir, "routes", "kemmelberg-loop")
	if err := os.MkdirAll(routeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(routeDir, "route.gpx"), exampleGPX)
	write(t, filepath.Join(routeDir, "route.yaml"), "name: Kemmelberg Loop\n")

	t.Chdir(dir)
	return dir
}

// linkAccount links a head unit the way the UI would, so the CLI tests have
// something to push to. Accounts live in the database now; nothing in the
// config file names them.
func linkAccount(t *testing.T, dsn, provider, rider string) {
	t.Helper()

	db, err := source.OpenDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store, err := accounts.UseDB(db.Conn(), db.DSN())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Link(model.Provider(provider), rider, ""); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// capture runs the CLI and returns everything it printed to stdout.
func capture(t *testing.T, args ...string) (string, error) {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer

	runErr := run(args)

	writer.Close()
	os.Stdout = original

	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := reader.Read(buf)
		sb.Write(buf[:n])
		if readErr != nil {
			break
		}
	}
	return sb.String(), runErr
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := capture(t, args...)
	if err != nil {
		t.Fatalf("domestique %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func TestCLIHelp(t *testing.T) {
	for _, args := range [][]string{{}, {"help"}, {"--help"}} {
		out := mustRun(t, args...)
		for _, command := range []string{"validate", "plan", "push", "state", "import", "serve"} {
			if !strings.Contains(out, command) {
				t.Errorf("%v: help does not mention %q", args, command)
			}
		}
	}
}

func TestCLIUnknownCommand(t *testing.T) {
	_, err := capture(t, "frobnicate")
	if err == nil {
		t.Fatal("unknown command exited 0")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("error does not name the command: %v", err)
	}
}

func TestCLIValidate(t *testing.T) {
	workspace(t)

	out := mustRun(t, "validate")
	for _, want := range []string{"kemmelberg-loop", "Kemmelberg Loop", "1 route(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("validate output missing %q:\n%s", want, out)
		}
	}
}

// A broken route must be reported without stopping the others.
func TestCLIValidateReportsBrokenRoutes(t *testing.T) {
	dir := workspace(t)

	broken := filepath.Join(dir, "routes", "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(broken, "route.gpx"), "this is not xml")

	out, err := capture(t, "validate")
	if err == nil {
		t.Error("expected a non-zero exit when a route is broken")
	}
	if !strings.Contains(out, "kemmelberg-loop") {
		t.Errorf("the healthy route was dropped:\n%s", out)
	}
}

func TestCLIValidateWithMissingLibrary(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := capture(t, "validate", "--source", "fs", "--library", "./nope"); err == nil {
		t.Fatal("expected an error when the library directory does not exist")
	}
}

// With no config at all the default is a database, which is created on
// demand — so a fresh install works rather than erroring about a missing
// routes directory.
func TestCLIValidateWithNoConfigCreatesDatabase(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	out, err := capture(t, "validate")
	if err != nil {
		t.Fatalf("validate on a fresh directory failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "database") {
		t.Errorf("expected a database source, got:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "data", "domestique.db")); statErr != nil {
		t.Errorf("database not created: %v", statErr)
	}
}

// A directory library has no database, so nothing can be linked and there is
// nowhere to push. Plan says so by having nothing to do.
func TestCLIPlanWithNothingLinked(t *testing.T) {
	workspace(t)

	out := mustRun(t, "plan")
	if !strings.Contains(out, "up to date") && !strings.Contains(out, "0 change") {
		t.Errorf("expected an empty plan with nothing linked:\n%s", out)
	}
}

// With a database and a linked head unit, the same route does produce a plan.
func TestCLIPlanWithALinkedAccount(t *testing.T) {
	dir := workspace(t)
	db := filepath.Join(dir, "data", "routes.db")

	mustRun(t, "import", "--source", "db", "--db", db, "--from", "./routes")
	linkAccount(t, db, "garmin", "one")

	out := mustRun(t, "plan", "--source", "db", "--db", db)
	if !strings.Contains(out, "create") || !strings.Contains(out, "garmin:one") {
		t.Errorf("plan does not target the linked account:\n%s", out)
	}
}

func TestCLIPushDryRunChangesNothing(t *testing.T) {
	dir := workspace(t)
	db := filepath.Join(dir, "data", "routes.db")
	mustRun(t, "import", "--source", "db", "--db", db, "--from", "./routes")
	linkAccount(t, db, "garmin", "one")

	out := mustRun(t, "push", "--source", "db", "--db", db, "--dry-run")
	if !strings.Contains(out, "dry run") {
		t.Errorf("dry run not announced:\n%s", out)
	}

	// And the plan is unchanged afterwards.
	if out := mustRun(t, "plan", "--source", "db", "--db", db); !strings.Contains(out, "1 change(s)") {
		t.Errorf("dry run changed the plan:\n%s", out)
	}
}

// The adapters are stubs, so a real push must fail loudly rather than
// silently recording success.
func TestCLIPushFailsWhileAdaptersAreStubs(t *testing.T) {
	dir := workspace(t)
	db := filepath.Join(dir, "data", "routes.db")
	mustRun(t, "import", "--source", "db", "--db", db, "--from", "./routes")
	linkAccount(t, db, "garmin", "one")

	_, err := capture(t, "push", "--source", "db", "--db", db)
	if err == nil {
		t.Fatal("push reported success with stub adapters")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestCLIStateOnFreshWorkspace(t *testing.T) {
	workspace(t)

	out := mustRun(t, "state")
	if !strings.Contains(out, "nothing has been pushed yet") {
		t.Errorf("state output = %q", out)
	}
}

// state must work even when the source is unreachable — that is when you most
// want to look at it.
func TestCLIStateWithoutASource(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := capture(t, "state"); err != nil {
		t.Fatalf("state failed without a library: %v", err)
	}
}

func TestCLIImportIntoDatabase(t *testing.T) {
	dir := workspace(t)
	db := filepath.Join(dir, "data", "routes.db")

	out := mustRun(t, "import", "--source", "db", "--db", db, "--from", "./routes")
	if !strings.Contains(out, "1 of 1 route(s) imported") {
		t.Errorf("import summary missing:\n%s", out)
	}

	// The database is now a working library.
	out = mustRun(t, "validate", "--source", "db", "--db", db)
	if !strings.Contains(out, "Kemmelberg Loop") {
		t.Errorf("imported route missing from the database:\n%s", out)
	}
	if !strings.Contains(out, "database") {
		t.Errorf("validate did not report the database source:\n%s", out)
	}
}

func TestCLIImportRequiresAWritableTarget(t *testing.T) {
	workspace(t)

	_, err := capture(t, "import", "--from", "./routes")
	if err == nil {
		t.Fatal("import into a read-only fs source succeeded")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("error does not explain why: %v", err)
	}
}

func TestCLIImportRequiresFrom(t *testing.T) {
	dir := workspace(t)
	db := filepath.Join(dir, "data", "routes.db")

	if _, err := capture(t, "import", "--source", "db", "--db", db); err == nil {
		t.Fatal("import without --from succeeded")
	}
}

func TestCLIFlagsOverrideTheConfigFile(t *testing.T) {
	dir := workspace(t)

	other := filepath.Join(dir, "elsewhere", "big-day")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(other, "route.gpx"), exampleGPX)

	out := mustRun(t, "validate", "--library", filepath.Join(dir, "elsewhere"))
	if !strings.Contains(out, "big-day") {
		t.Errorf("--library was ignored:\n%s", out)
	}
	if strings.Contains(out, "kemmelberg-loop") {
		t.Errorf("--library did not replace the configured path:\n%s", out)
	}
}

func TestCLIRejectsUnknownSourceKind(t *testing.T) {
	workspace(t)
	if _, err := capture(t, "validate", "--source", "carrier-pigeon"); err == nil {
		t.Fatal("unknown source kind accepted")
	}
}

// A route naming a target that does not exist would otherwise silently never
// sync anywhere.
func TestCLIValidateFlagsUnknownTargets(t *testing.T) {
	dir := workspace(t)
	write(t, filepath.Join(dir, "routes", "kemmelberg-loop", "route.yaml"),
		"name: Kemmelberg Loop\ntargets:\n  - garmin:wilnat\n")

	out, err := capture(t, "validate")
	if err == nil {
		t.Error("expected a non-zero exit for an unknown target")
	}
	_ = out
}
