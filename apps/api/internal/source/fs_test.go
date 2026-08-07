package source

import (
	"errors"
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
func TestFSRefusesPathTraversal(t *testing.T) {
	src := testFS(t)

	for _, slug := range []string{
		"../../../../etc/passwd",
		"..",
		"kemmelberg-loop/../../..",
		"/etc/passwd",
	} {
		if _, err := src.GPX(slug); !errors.Is(err, ErrNotFound) {
			t.Errorf("GPX(%q): err = %v, want ErrNotFound", slug, err)
		}
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
