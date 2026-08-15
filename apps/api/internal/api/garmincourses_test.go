package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/garmin"
	"github.com/wncservices/domestique/apps/api/internal/source"
	"github.com/wncservices/domestique/apps/api/internal/state"
)

func (h *connectHarness) connectGarmin(rider string) {
	h.t.Helper()
	h.as(rider, "cyclists", http.MethodPost, "/api/garmin/connection",
		`{"email":"r@example.com","password":"pw"}`)
}

func TestGarminCourseListShowsWhatIsOnTheAccount(t *testing.T) {
	h := newConnectHarness(t, true)
	h.connectGarmin("wilant")
	h.garmin.listCourses = []garmin.Course{
		{ID: "1", Name: "Kemmelberg Loop", DistanceM: 42000, AscentM: 500, ActivityType: "cycling"},
		{ID: "2", Name: "Flat Coast Ride", DistanceM: 30000, AscentM: 50, ActivityType: "cycling"},
	}

	resp := h.as("wilant", "cyclists", http.MethodGet, "/api/garmin/courses", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got []struct {
		ID                string  `json:"id"`
		Name              string  `json:"name"`
		DistanceM         float64 `json:"distanceM"`
		Imported          bool    `json:"imported"`
		PossibleDuplicate string  `json:"possibleDuplicate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Kemmelberg Loop" || got[1].Name != "Flat Coast Ride" {
		t.Fatalf("courses = %+v, want both", got)
	}
	for _, c := range got {
		if c.Imported || c.PossibleDuplicate != "" {
			t.Errorf("%s: imported=%v duplicate=%q, want neither — nothing in the library yet",
				c.Name, c.Imported, c.PossibleDuplicate)
		}
	}
}

// A course this app already pushed to the account is flagged Imported —
// exact match on the id this app itself recorded, no heuristic involved.
func TestGarminCourseListFlagsAlreadyTrackedCoursesAsImported(t *testing.T) {
	h := newConnectHarness(t, true)
	h.connectGarmin("wilant")
	h.garmin.listCourses = []garmin.Course{
		{ID: "999", Name: "Already Pushed", DistanceM: 10000, ActivityType: "cycling"},
	}
	if err := h.store.Record(state.Entry{
		AccountID: "garmin:wilant", Slug: "already-pushed", RemoteID: "999",
		ContentHash: "irrelevant-here", UpdatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	resp := h.as("wilant", "cyclists", http.MethodGet, "/api/garmin/courses", "")
	var got []struct {
		ID       string `json:"id"`
		Imported bool   `json:"imported"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Imported {
		t.Errorf("courses = %+v, want the tracked one flagged imported", got)
	}
}

// A course that was never pushed by this app, but looks like a route already
// in the library by distance and start point, is flagged as a possible
// duplicate — a hint, not something silently hidden.
func TestGarminCourseListFlagsLikelyDuplicatesByDistanceAndStartPoint(t *testing.T) {
	h := newConnectHarness(t, true)
	h.connectGarmin("wilant")

	route, err := h.db.Create(source.CreateRequest{
		Filename: "kemmelberg-loop.gpx", Name: "Kemmelberg Loop", GPX: exampleGPX(t),
	})
	if err != nil {
		t.Fatal(err)
	}

	h.garmin.listCourses = []garmin.Course{
		// Same ride, close enough to count: distance within 2%, start point
		// within the tolerance — a real Garmin re-encoding would not match
		// this app's own ContentHash exactly, which is the whole reason this
		// is a heuristic rather than an exact check.
		{
			ID: "1", Name: "Kemmelberg (from my Edge)",
			DistanceM: route.Stats.DistanceM * 1.005,
			StartLat:  route.Stats.StartLat, StartLng: route.Stats.StartLng,
			ActivityType: "cycling",
		},
		// A genuinely different ride: same start point, very different
		// distance — must not be flagged.
		{
			ID: "2", Name: "Short loop from the same car park",
			DistanceM: route.Stats.DistanceM * 3,
			StartLat:  route.Stats.StartLat, StartLng: route.Stats.StartLng,
			ActivityType: "cycling",
		},
	}

	resp := h.as("wilant", "cyclists", http.MethodGet, "/api/garmin/courses", "")
	var got []struct {
		ID                string `json:"id"`
		PossibleDuplicate string `json:"possibleDuplicate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d courses, want 2", len(got))
	}
	if got[0].PossibleDuplicate == "" {
		t.Error("the close-distance, same-start-point course was not flagged")
	}
	if got[1].PossibleDuplicate != "" {
		t.Errorf("the very-different-distance course was flagged: %q", got[1].PossibleDuplicate)
	}
}

func TestGarminCourseListWithoutAConnectionIsEmpty(t *testing.T) {
	h := newConnectHarness(t, true)

	resp := h.as("wilant", "cyclists", http.MethodGet, "/api/garmin/courses", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("courses = %+v, want none", got)
	}
}

func TestGarminCourseImportCreatesRoutes(t *testing.T) {
	h := newConnectHarness(t, true)
	h.connectGarmin("wilant")
	h.garmin.listCourses = []garmin.Course{
		{ID: "1", Name: "Kemmelberg Loop", DistanceM: 42000, ActivityType: "cycling"},
	}
	h.garmin.gpxByID = map[string][]byte{"1": exampleGPX(t)}

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/garmin/courses/import", `{"courseIds":["1"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result struct {
		Imported []string          `json:"imported"`
		Skipped  map[string]string `json:"skipped"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 1 || result.Imported[0] != "1" {
		t.Fatalf("imported = %+v, want [\"1\"]", result.Imported)
	}
	if len(result.Skipped) != 0 {
		t.Errorf("skipped = %+v, want none", result.Skipped)
	}

	routes, _, err := h.db.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Name != "Kemmelberg Loop" {
		t.Errorf("library = %+v, want the imported route", routes)
	}
}

func TestGarminCourseImportSkipsIDsNotOnTheAccount(t *testing.T) {
	h := newConnectHarness(t, true)
	h.connectGarmin("wilant")
	h.garmin.listCourses = []garmin.Course{{ID: "1", Name: "Real Course", ActivityType: "cycling"}}
	h.garmin.gpxByID = map[string][]byte{"1": exampleGPX(t)}

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/garmin/courses/import",
		`{"courseIds":["1","not-a-real-id"]}`)
	var result struct {
		Imported []string          `json:"imported"`
		Skipped  map[string]string `json:"skipped"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 1 {
		t.Errorf("imported = %+v, want exactly the real one", result.Imported)
	}
	if reason, ok := result.Skipped["not-a-real-id"]; !ok || reason == "" {
		t.Errorf("skipped = %+v, want a reason for the unknown id", result.Skipped)
	}
}

// One course's download failing must not stop the others in the same batch.
func TestGarminCourseImportIsolatesPerCourseFailures(t *testing.T) {
	h := newConnectHarness(t, true)
	h.connectGarmin("wilant")
	h.garmin.listCourses = []garmin.Course{
		{ID: "1", Name: "Good Course", ActivityType: "cycling"},
		{ID: "2", Name: "Bad Course", ActivityType: "cycling"},
	}
	h.garmin.gpxByID = map[string][]byte{"1": exampleGPX(t)}
	h.garmin.downloadGPXErr = nil // per-call errors aren't modeled by the fake; simulate via empty GPX for "2"

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/garmin/courses/import",
		`{"courseIds":["1","2"]}`)
	var result struct {
		Imported []string          `json:"imported"`
		Skipped  map[string]string `json:"skipped"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 1 || result.Imported[0] != "1" {
		t.Errorf("imported = %+v, want just the good course", result.Imported)
	}
	if _, ok := result.Skipped["2"]; !ok {
		t.Errorf("skipped = %+v, want course 2 to have failed (empty GPX)", result.Skipped)
	}
}

func TestGarminCourseImportRequiresAConnection(t *testing.T) {
	h := newConnectHarness(t, true)

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/garmin/courses/import", `{"courseIds":["1"]}`)
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("status = %d, want 412 (not connected)", resp.StatusCode)
	}
}

func TestGarminCourseImportRequiresAtLeastOneID(t *testing.T) {
	h := newConnectHarness(t, true)
	h.connectGarmin("wilant")

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/garmin/courses/import", `{"courseIds":[]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestGarminCourseListFailureIsUpstream(t *testing.T) {
	h := newConnectHarness(t, true)
	h.connectGarmin("wilant")
	h.garmin.listCoursesErr = errors.New("garmin: the course list returned 404")

	resp := h.as("wilant", "cyclists", http.MethodGet, "/api/garmin/courses", "")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}
