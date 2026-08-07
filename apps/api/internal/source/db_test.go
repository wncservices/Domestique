package source

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := OpenDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func exampleGPX(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "kemmelberg-loop.gpx"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDBCreateDerivesStatsAndSlug(t *testing.T) {
	db := openTestDB(t)

	route, err := db.Create(CreateRequest{
		Filename: "kemmelberg-loop.gpx",
		GPX:      exampleGPX(t),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if route.Slug != "kemmelberg-loop" {
		t.Errorf("slug = %q, want kemmelberg-loop derived from the filename", route.Slug)
	}
	if route.Name != "Kemmelberg Loop" {
		t.Errorf("name = %q, want a titleized filename", route.Name)
	}
	if route.Stats.PointCount == 0 || route.Stats.DistanceM == 0 {
		t.Errorf("stats not derived on upload: %+v", route.Stats)
	}
	if route.ContentHash == "" {
		t.Error("content hash not computed; the diff engine would never see a change")
	}
}

func TestDBCreateRejectsBadGPX(t *testing.T) {
	db := openTestDB(t)

	for name, body := range map[string][]byte{
		"empty":     {},
		"not xml":   []byte("this is not a GPX file"),
		"one point": []byte(`<gpx version="1.1"><trk><trkseg><trkpt lat="50" lon="3"/></trkseg></trk></gpx>`),
	} {
		if _, err := db.Create(CreateRequest{Filename: name + ".gpx", GPX: body}); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

// Two rides up the same hill should both be storable.
func TestDBCreateDisambiguatesSlugs(t *testing.T) {
	db := openTestDB(t)
	raw := exampleGPX(t)

	first, err := db.Create(CreateRequest{Name: "Kemmelberg Loop", GPX: raw})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.Create(CreateRequest{Name: "Kemmelberg Loop", GPX: raw})
	if err != nil {
		t.Fatal(err)
	}

	if first.Slug == second.Slug {
		t.Fatalf("both routes got slug %q", first.Slug)
	}
	if second.Slug != "kemmelberg-loop-2" {
		t.Errorf("second slug = %q, want kemmelberg-loop-2", second.Slug)
	}
}

func TestDBRenameChangesContentHash(t *testing.T) {
	db := openTestDB(t)

	route, err := db.Create(CreateRequest{Name: "Before", GPX: exampleGPX(t)})
	if err != nil {
		t.Fatal(err)
	}

	renamed := "After"
	updated, err := db.Update(route.Slug, UpdateRequest{Name: &renamed})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// The providers show the name, so a rename has to reach them.
	if updated.ContentHash == route.ContentHash {
		t.Error("content hash unchanged after a rename; the rename would never sync")
	}
}

func TestDBListSkipsDisabledRoutes(t *testing.T) {
	db := openTestDB(t)

	route, err := db.Create(CreateRequest{Name: "Hidden", GPX: exampleGPX(t)})
	if err != nil {
		t.Fatal(err)
	}

	disabled := false
	if _, err := db.Update(route.Slug, UpdateRequest{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}

	routes, _, err := db.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 0 {
		t.Fatalf("got %d routes, want 0 — disabled routes must not sync", len(routes))
	}
}

func TestDBDeleteAndMissingLookups(t *testing.T) {
	db := openTestDB(t)

	route, err := db.Create(CreateRequest{Name: "Doomed", GPX: exampleGPX(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(route.Slug); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if err := db.Delete(route.Slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: err = %v, want ErrNotFound", err)
	}
	if _, err := db.GPX(route.Slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("GPX after delete: err = %v, want ErrNotFound", err)
	}
}

func TestDBRoundTripsTargets(t *testing.T) {
	db := openTestDB(t)

	only := []string{"garmin:wilant"}
	route, err := db.Create(CreateRequest{Name: "Private", GPX: exampleGPX(t), Targets: &only})
	if err != nil {
		t.Fatal(err)
	}

	routes, _, err := db.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Targets == nil {
		t.Fatalf("targets lost on the way to the database: %+v", routes)
	}
	if got := *routes[0].Targets; len(got) != 1 || got[0] != "garmin:wilant" {
		t.Errorf("targets = %v, want [garmin:wilant]", got)
	}
	_ = route
}
