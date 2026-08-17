package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/accounts"
	"github.com/wncservices/domestique/apps/api/internal/fitcourse"
	"github.com/wncservices/domestique/apps/api/internal/gpx"
	"github.com/wncservices/domestique/apps/api/internal/model"
)

// aTestFIT builds a real, decodable FIT course — handleWahooRouteImport
// runs fitcourse.Decode on whatever a route's file downloads to, so a fake
// upstream needs to hand back genuine FIT bytes, not a placeholder.
func aTestFIT(t *testing.T, name string) []byte {
	t.Helper()
	points := []gpx.Point{
		{Lat: 50.7920, Lon: 2.8180, Ele: 42, HasEle: true},
		{Lat: 50.7982, Lon: 2.8344, Ele: 128, HasEle: true},
		{Lat: 50.8007, Lon: 2.8437, Ele: 139, HasEle: true},
	}
	raw, err := fitcourse.Encode(points, fitcourse.Options{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestWahooRouteEndpointsRequireRider(t *testing.T) {
	h := newWahooHarness(t, true)
	h.connect("wilant", "cyclists")
	h.upstream.addRoute(map[string]any{
		"id": 1, "name": "Kemmelberg", "distance": 5500.0, "ascent": 100.0,
	}, aTestFIT(t, "Kemmelberg"))

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/wahoo/routes"},
		{http.MethodGet, "/api/wahoo/routes/duplicates"},
		{http.MethodPost, "/api/wahoo/routes/import"},
		{http.MethodDelete, "/api/wahoo/routes/1"},
	} {
		resp := h.as("guest", "guests", tc.method, tc.path)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403", tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestWahooRouteListWithoutAConnectionIsEmpty(t *testing.T) {
	h := newWahooHarness(t, true)

	resp := h.as("wilant", "cyclists", http.MethodGet, "/api/wahoo/routes")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("routes = %v, want an empty list with no connection", out)
	}
}

func TestWahooRouteImportCreatesARouteAndRecordsSyncState(t *testing.T) {
	h := newWahooHarness(t, true)
	h.connect("wilant", "cyclists")
	h.upstream.addRoute(map[string]any{
		"id": 1, "name": "Kemmelberg", "external_id": "", "distance": 5500.0, "ascent": 100.0,
		"start_lat": 50.79, "start_lng": 2.81,
	}, aTestFIT(t, "Kemmelberg"))

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/wahoo/routes/import", `{"routeIds":["1"]}`)
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
	if len(result.Imported) != 1 {
		t.Fatalf("result = %+v, want one route imported", result)
	}

	routes, _, err := h.db.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("library has %d routes, want 1", len(routes))
	}
	route := routes[0]
	if route.Name != "Kemmelberg" {
		t.Errorf("name = %q", route.Name)
	}
	hasOrigin, hasID := false, false
	for _, tag := range route.Tags {
		if tag == "wahoo" {
			hasOrigin = true
		}
		if tag == "wahoo:1" {
			hasID = true
		}
	}
	if !hasOrigin || !hasID {
		t.Errorf("tags = %v, want wahoo and wahoo:1", route.Tags)
	}

	entries, err := h.store.ForAccount(t.Context(), accounts.ID(model.ProviderWahoo, "wilant"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("sync state = %+v, want exactly one entry", entries)
	}
	for _, e := range entries {
		if e.RemoteID != "1" || e.Slug != route.Slug {
			t.Fatalf("sync state entry = %+v, want remote 1 for route %q", e, route.Slug)
		}
	}
}

// Re-selecting an id already recorded as synced must heal (or no-op) rather
// than create a second copy — the same contract
// TestGarminCourseImportHealsMissingTagsWithoutDuplicating checks for Garmin.
func TestWahooRouteImportIsIdempotent(t *testing.T) {
	h := newWahooHarness(t, true)
	h.connect("wilant", "cyclists")
	h.upstream.addRoute(map[string]any{
		"id": 1, "name": "Kemmelberg", "distance": 5500.0, "ascent": 100.0,
	}, aTestFIT(t, "Kemmelberg"))

	first := h.as("wilant", "cyclists", http.MethodPost, "/api/wahoo/routes/import", `{"routeIds":["1"]}`)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first import status = %d", first.StatusCode)
	}

	second := h.as("wilant", "cyclists", http.MethodPost, "/api/wahoo/routes/import", `{"routeIds":["1"]}`)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second import status = %d", second.StatusCode)
	}
	var result struct {
		Imported []string          `json:"imported"`
		Skipped  map[string]string `json:"skipped"`
	}
	if err := json.NewDecoder(second.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 1 {
		t.Fatalf("re-import result = %+v, want it still reported as imported (healed)", result)
	}

	routes, _, err := h.db.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("library has %d routes after re-import, want still 1 (no duplicate)", len(routes))
	}
}

func TestWahooRouteImportSkipsAnIDNotOnTheAccount(t *testing.T) {
	h := newWahooHarness(t, true)
	h.connect("wilant", "cyclists")

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/wahoo/routes/import", `{"routeIds":["999"]}`)
	var result struct {
		Imported []string          `json:"imported"`
		Skipped  map[string]string `json:"skipped"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 0 || result.Skipped["999"] == "" {
		t.Fatalf("result = %+v, want 999 skipped with a reason", result)
	}
}

func TestWahooRouteImportRequiresAConnection(t *testing.T) {
	h := newWahooHarness(t, true)

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/wahoo/routes/import", `{"routeIds":["1"]}`)
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412 without a Wahoo connection", resp.StatusCode)
	}
}

func TestWahooRouteDuplicatesGroupsBySameNameAndDistance(t *testing.T) {
	h := newWahooHarness(t, true)
	h.connect("wilant", "cyclists")
	h.upstream.addRoute(map[string]any{"id": 1, "name": "Kemmelberg", "distance": 5500.0}, aTestFIT(t, "Kemmelberg"))
	h.upstream.addRoute(map[string]any{"id": 2, "name": "kemmelberg", "distance": 5520.0}, aTestFIT(t, "Kemmelberg"))
	h.upstream.addRoute(map[string]any{"id": 3, "name": "Different ride", "distance": 20000.0}, aTestFIT(t, "Different"))

	resp := h.as("wilant", "cyclists", http.MethodGet, "/api/wahoo/routes/duplicates")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var groups []struct {
		Name   string `json:"name"`
		Routes []struct {
			ID string `json:"id"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&groups); err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Routes) != 2 {
		t.Fatalf("groups = %+v, want one group of two repeated routes", groups)
	}
}

func TestWahooRouteDuplicatesWithoutAConnectionIsEmpty(t *testing.T) {
	h := newWahooHarness(t, true)

	resp := h.as("wilant", "cyclists", http.MethodGet, "/api/wahoo/routes/duplicates")
	var groups []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&groups); err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Fatalf("groups = %v, want empty with no connection", groups)
	}
}

func TestWahooRouteDeleteRemovesFromWahoo(t *testing.T) {
	h := newWahooHarness(t, true)
	h.connect("wilant", "cyclists")

	resp := h.as("wilant", "cyclists", http.MethodDelete, "/api/wahoo/routes/5")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(h.upstream.deletedRoutes) != 1 || h.upstream.deletedRoutes[0] != "5" {
		t.Fatalf("deletedRoutes = %v, want [5]", h.upstream.deletedRoutes)
	}
}

func TestWahooRouteDeleteRequiresAConnection(t *testing.T) {
	h := newWahooHarness(t, true)

	resp := h.as("wilant", "cyclists", http.MethodDelete, "/api/wahoo/routes/5")
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412 without a Wahoo connection", resp.StatusCode)
	}
}
