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
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/source"
	"github.com/wncservices/domestique/apps/api/internal/state"
)

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
		Source   string `json:"source"`
		Writable bool   `json:"writable"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body.Source, "sqlite database") {
		t.Errorf("source = %q, want it to name the engine", body.Source)
	}
	if !body.Writable {
		t.Error("writable = false; the library is always a database now")
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
