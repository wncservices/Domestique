package source

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func testFS(t *testing.T) *FS {
	t.Helper()
	src, err := NewFS(filepath.Join("testdata", "library"))
	if err != nil {
		t.Fatalf("open fs source: %v", err)
	}
	return src
}

func TestFSListReadsRoutes(t *testing.T) {
	routes, problems, err := testFS(t).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(routes))
	}
	if routes[0].Slug != "kemmelberg-loop" {
		t.Errorf("slug = %q", routes[0].Slug)
	}
	if routes[0].Stats.DistanceM == 0 {
		t.Error("stats not derived")
	}
}

// Slugs arrive straight from URLs, so a traversal attempt must not reach
// outside the library root.
//
// This test plants a real, readable route.gpx *outside* the library. Without
// it the traversal cases pass for the wrong reason — the source appends
// route.gpx to the slug, so "../../etc/passwd" never names an existing file
// and would 404 even with the guard removed.
func TestFSRefusesPathTraversal(t *testing.T) {
	root := t.TempDir()

	library := filepath.Join(root, "library")
	inside := filepath.Join(library, "public-loop")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join("testdata", "library", "kemmelberg-loop", "route.gpx"),
		filepath.Join(inside, "route.gpx"))

	// A sibling directory the library must not be able to read.
	secret := filepath.Join(root, "secret")
	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join("testdata", "library", "kemmelberg-loop", "route.gpx"),
		filepath.Join(secret, "route.gpx"))

	src, err := NewFS(library)
	if err != nil {
		t.Fatal(err)
	}

	// Sanity: the guard is not simply refusing everything.
	if _, err := src.GPX("public-loop"); err != nil {
		t.Fatalf("legitimate route unreadable: %v", err)
	}

	for _, slug := range []string{
		"../secret",
		"public-loop/../../secret",
		"./../secret",
		filepath.Join(root, "secret"), // absolute
		"..",
	} {
		if _, err := src.GPX(slug); !errors.Is(err, ErrNotFound) {
			t.Errorf("GPX(%q) escaped the library root: err = %v, want ErrNotFound", slug, err)
		}
		if _, err := src.Track(slug); !errors.Is(err, ErrNotFound) {
			t.Errorf("Track(%q) escaped the library root: err = %v, want ErrNotFound", slug, err)
		}
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	raw, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFSIsReadOnly(t *testing.T) {
	if _, ok := AsWritable(testFS(t)); ok {
		t.Error("FS source reports itself writable; uploads would bypass git review")
	}
}

func TestDBIsWritable(t *testing.T) {
	if _, ok := AsWritable(openTestDB(t)); !ok {
		t.Error("DB source reports itself read-only; the UI would hide uploads")
	}
}
