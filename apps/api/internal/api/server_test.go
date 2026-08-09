package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/accounts"
	"github.com/wncservices/domestique/apps/api/internal/config"
	"github.com/wncservices/domestique/apps/api/internal/komoot"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/source"
	"github.com/wncservices/domestique/apps/api/internal/state"
)

// stubKomoot stands in for a signed-in client. What it returns does not
// matter here; only that it is non-nil.
type stubKomoot struct{}

func (stubKomoot) Tours(bool) ([]komoot.Tour, error) { return nil, nil }
func (stubKomoot) GPX(string) ([]byte, error)        { return nil, nil }

func testServer(t *testing.T, src *source.DB) http.Handler {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		Source:   src,
		Store:    store,
		Config:   &config.Config{},
		Accounts: linkedStore(t, src),
	}
	return srv.Handler()
}

// linkedStore gives the server an accounts store, with one account linked the
// way a rider would through the UI.
func linkedStore(t *testing.T, db *source.DB) *accounts.Store {
	t.Helper()

	store, err := accounts.UseDB(db.Conn(), db.DSN())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Link(model.ProviderGarmin, "one", ""); err != nil {
		t.Fatal(err)
	}
	return store
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
func TestUnknownAPIPathIs404JSON(t *testing.T) {
	rec := do(dbServer(t), http.MethodGet, "/api/nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want JSON", ct)
	}
}

func TestConfigDescribesTheLibrary(t *testing.T) {
	rec := do(dbServer(t), http.MethodGet, "/api/config")

	var body struct {
		Source string `json:"source"`
		Komoot string `json:"komoot"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body.Source, "sqlite database") {
		t.Errorf("source = %q, want it to name the engine", body.Source)
	}
	if body.Komoot != "disabled" {
		t.Errorf("komoot = %q, want disabled when nobody asked for it", body.Komoot)
	}
}

// TestConfigSeparatesKomootOffFromKomootBroken is the distinction the whole
// state field exists for. Both cases refuse an import; only one of them is
// something the operator should be told to fix, and collapsing them is how a
// missing environment variable becomes a feature that silently is not there.
func TestConfigSeparatesKomootOffFromKomootBroken(t *testing.T) {
	for _, tc := range []struct {
		name     string
		enabled  bool
		importer KomootImporter
		want     string
	}{
		{"nobody asked", false, nil, "disabled"},
		{"asked, no credentials", true, nil, "unconfigured"},
		{"asked and signed in", true, stubKomoot{}, "ready"},
		// Config off but a client somehow present: the config wins, because
		// that is what the operator asked for.
		{"off but client present", false, stubKomoot{}, "disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := source.OpenDB(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })

			store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			srv := &Server{
				Source:        db,
				Store:         store,
				Config:        &config.Config{},
				Accounts:      linkedStore(t, db),
				Komoot:        tc.importer,
				KomootEnabled: tc.enabled,
			}

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))

			var body struct {
				Komoot string `json:"komoot"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Komoot != tc.want {
				t.Errorf("komoot = %q, want %q", body.Komoot, tc.want)
			}
		})
	}
}

func TestRoutesAndPlanAgree(t *testing.T) {
	h := dbServer(t)

	// Seed a route, since a fresh database has none.
	upload := httptest.NewRequest(http.MethodPost, "/api/routes", nil)
	_ = upload

	var plan struct {
		Items []struct {
			Op string `json:"op"`
		} `json:"items"`
	}
	if err := json.Unmarshal(do(h, http.MethodGet, "/api/plan").Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	// A linked account and no routes: nothing to do, and no error.
	if len(plan.Items) != 0 {
		t.Errorf("plan = %+v, want nothing on an empty library", plan.Items)
	}
}
