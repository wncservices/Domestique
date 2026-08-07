// Acceptance tests for the CLI: every command, driven through run() the way
// the shell drives it, against real files in a temp directory.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testConfig = `
source:
  kind: fs
  path: ./routes
accounts:
  - id: garmin:wilant
    provider: garmin
    rider: wilant
    label: Wilant's Edge
  - id: wahoo:friend
    provider: wahoo
    rider: friend
default_targets:
  - garmin:wilant
  - wahoo:friend
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
	for _, want := range []string{"kemmelberg-loop", "Kemmelberg Loop", "1 route(s)", "2 account(s)"} {
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
	if _, err := capture(t, "validate"); err == nil {
		t.Fatal("expected an error when the library does not exist")
	}
}

func TestCLIPlan(t *testing.T) {
	workspace(t)

	out := mustRun(t, "plan")
	if !strings.Contains(out, "create") {
		t.Errorf("plan has no creates:\n%s", out)
	}
	for _, account := range []string{"garmin:wilant", "wahoo:friend"} {
		if !strings.Contains(out, account) {
			t.Errorf("plan does not mention %s:\n%s", account, out)
		}
	}
	if !strings.Contains(out, "2 change(s)") {
		t.Errorf("plan summary missing:\n%s", out)
	}
}

func TestCLIPushDryRunChangesNothing(t *testing.T) {
	dir := workspace(t)

	out := mustRun(t, "push", "--dry-run")
	if !strings.Contains(out, "dry run") {
		t.Errorf("dry run not announced:\n%s", out)
	}

	if _, err := os.Stat(filepath.Join(dir, ".domestique-state.json")); err == nil {
		t.Error("dry run wrote a state file")
	}

	// And the plan is unchanged afterwards.
	if out := mustRun(t, "plan"); !strings.Contains(out, "2 change(s)") {
		t.Errorf("dry run changed the plan:\n%s", out)
	}
}

// The adapters are stubs, so a real push must fail loudly rather than
// silently recording success.
func TestCLIPushFailsWhileAdaptersAreStubs(t *testing.T) {
	workspace(t)

	_, err := capture(t, "push")
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
