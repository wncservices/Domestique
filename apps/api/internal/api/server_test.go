package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/config"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/source"
	"github.com/wncservices/domestique/apps/api/internal/state"
)

func testServer(t *testing.T, src source.Source) http.Handler {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		Source: src,
		Store:  store,
		Config: &config.Config{
			Accounts:       []model.Account{{ID: "garmin:wilant", Provider: model.ProviderGarmin, Rider: "wilant"}},
			DefaultTargets: []string{"garmin:wilant"},
		},
	}
	return srv.Handler()
}

func fsServer(t *testing.T) http.Handler {
	t.Helper()
	src, err := source.NewFS(filepath.Join("testdata", "library"))
	if err != nil {
		t.Fatal(err)
	}
	return testServer(t, src)
}

func dbServer(t *testing.T) http.Handler {
	t.Helper()
	db, err := source.OpenDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return testServer(t, db)
}

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// A read-only source must reject writes with a status a client can act on.
// Leaving the routes unregistered made the SPA fallback answer 200 with HTML.
func TestReadOnlySourceRejectsWrites(t *testing.T) {
	h := fsServer(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/routes"},
		{http.MethodDelete, "/api/routes/kemmelberg-loop"},
		{http.MethodPatch, "/api/routes/kemmelberg-loop"},
	} {
		rec := do(h, tc.method, tc.path)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want 405", tc.method, tc.path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s %s: content-type = %q, want JSON", tc.method, tc.path, ct)
		}
	}
}

func TestUnknownAPIPathIs404JSON(t *testing.T) {
	rec := do(fsServer(t), http.MethodGet, "/api/nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want JSON", ct)
	}
}

func TestConfigReportsWritability(t *testing.T) {
	for name, tc := range map[string]struct {
		handler http.Handler
		want    bool
	}{
		"fs": {fsServer(t), false},
		"db": {dbServer(t), true},
	} {
		rec := do(tc.handler, http.MethodGet, "/api/config")
		var body struct {
			Writable bool `json:"writable"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if body.Writable != tc.want {
			t.Errorf("%s: writable = %v, want %v", name, body.Writable, tc.want)
		}
	}
}

// Slugs come straight from the URL, so an encoded traversal must not escape
// the library root.
func TestTraversalIsRejected(t *testing.T) {
	h := fsServer(t)

	for _, path := range []string{
		"/api/gpx/%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"/api/tracks/%2e%2e%2f%2e%2e%2fetc%2fpasswd",
	} {
		rec := do(h, http.MethodGet, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "root:") {
			t.Fatalf("%s: served /etc/passwd", path)
		}
	}
}

func TestRoutesAndPlanAgree(t *testing.T) {
	h := fsServer(t)

	var library struct {
		Routes []struct {
			Slug      string `json:"slug"`
			SyncState []struct {
				Status string `json:"status"`
			} `json:"syncState"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(do(h, http.MethodGet, "/api/routes").Body.Bytes(), &library); err != nil {
		t.Fatal(err)
	}
	if len(library.Routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(library.Routes))
	}
	if got := library.Routes[0].SyncState[0].Status; got != "pending" {
		t.Errorf("status = %q, want pending on a fresh state", got)
	}

	var plan struct {
		Items []struct {
			Op string `json:"op"`
		} `json:"items"`
	}
	if err := json.Unmarshal(do(h, http.MethodGet, "/api/plan").Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Op != "create" {
		t.Errorf("plan = %+v, want a single create", plan.Items)
	}
}
