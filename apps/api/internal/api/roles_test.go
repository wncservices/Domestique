// Acceptance tests for authentication and roles, over real HTTP.
//
// These are the tests that decide whether the app is safe to expose. They
// check what a *client* can actually do, not what the auth package believes.
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/accounts"
	"github.com/wncservices/domestique/apps/api/internal/api"
	"github.com/wncservices/domestique/apps/api/internal/auth"
	"github.com/wncservices/domestique/apps/api/internal/config"
	"github.com/wncservices/domestique/apps/api/internal/komoot"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/source"
	"github.com/wncservices/domestique/apps/api/internal/state"
	"github.com/wncservices/domestique/apps/api/internal/targets"
)

// authHarness serves a database library behind proxy-mode auth.
type authHarness struct {
	t      *testing.T
	client *http.Client
	base   string
	src    *source.DB
}

func newAuthHarness(t *testing.T, komootClient api.KomootImporter) *authHarness {
	t.Helper()

	db, err := source.OpenDB(filepath.Join(t.TempDir(), "routes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	authenticator, err := auth.New(auth.Config{
		Mode: auth.ModeProxy,
		Roles: auth.RoleMapping{
			Admin:  []string{"domestique-admins"},
			Rider:  []string{"cyclists"},
			Viewer: []string{"guests"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := &api.Server{
		Source:   db,
		Store:    store,
		Auth:     authenticator,
		Komoot:   komootClient,
		Accounts: seedRoleAccounts(t, db),
		Config: &config.Config{
			Komoot: config.KomootConfig{Enabled: komootClient != nil},
		},
		TargetFactory: func(model.Account) (targets.Target, error) {
			return stubTarget{}, nil
		},
	}

	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)

	return &authHarness{t: t, client: server.Client(), base: server.URL, src: db}
}

// seedRoleAccounts links one head unit, owned by "wilant".
func seedRoleAccounts(t *testing.T, db *source.DB) *accounts.Store {
	t.Helper()

	store, err := accounts.UseDB(db.Conn(), db.DSN())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Link(model.ProviderGarmin, "wilant", ""); err != nil {
		t.Fatal(err)
	}
	return store
}

type stubTarget struct{}

func (stubTarget) Create(context.Context, model.Route) (string, error) { return "remote-1", nil }
func (stubTarget) Update(context.Context, string, model.Route) (string, error) {
	return "remote-1", nil
}
func (stubTarget) Delete(context.Context, string) error { return nil }

// as issues a request as a user in the given groups.
func (h *authHarness) as(user, groups, method, path string, body string) *http.Response {
	h.t.Helper()

	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}

	req, err := http.NewRequest(method, h.base+path, reader)
	if err != nil {
		h.t.Fatal(err)
	}
	if user != "" {
		req.Header.Set(auth.HeaderUser, user)
		req.Header.Set(auth.HeaderGroups, groups)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (h *authHarness) seedRoute(t *testing.T, name, owner string) model.Route {
	t.Helper()
	route, err := h.src.Create(t.Context(), source.CreateRequest{
		Name:       name,
		GPX:        []byte(seedGPX),
		UploadedBy: owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	return route
}

// multipartUpload builds a browser-shaped upload body.
func multipartUpload(t *testing.T, fields map[string]string, gpxBody []byte, filename string) (*bytes.Buffer, string) {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(gpxBody); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, writer.FormDataContentType()
}

const seedGPX = `<?xml version="1.0"?>
<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><trkseg>
<trkpt lat="50.79" lon="2.81"><ele>42</ele></trkpt>
<trkpt lat="50.80" lon="2.84"><ele>128</ele></trkpt>
</trkseg></trk></gpx>`

// ---------- authentication ----------

// With auth on, an unauthenticated caller gets nothing from the API.
func TestUnauthenticatedIsRejected(t *testing.T) {
	h := newAuthHarness(t, nil)

	for _, path := range []string{"/api/routes", "/api/plan", "/api/accounts"} {
		resp := h.as("", "", http.MethodGet, path, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", path, resp.StatusCode)
		}
	}
}

// The health endpoint must stay open, or a liveness probe needs credentials.
func TestHealthIsUnauthenticated(t *testing.T) {
	h := newAuthHarness(t, nil)
	if resp := h.as("", "", http.MethodGet, "/api/health", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("health: status = %d, want 200", resp.StatusCode)
	}
}

func TestMeReportsRoleAndPermissions(t *testing.T) {
	h := newAuthHarness(t, nil)

	for _, tc := range []struct {
		groups   string
		wantRole string
		canPush  bool
	}{
		{"domestique-admins", "admin", true},
		{"cyclists", "rider", true},
		{"guests", "viewer", false},
		{"unmapped", "viewer", false},
	} {
		resp := h.as("someone", tc.groups, http.MethodGet, "/api/me", "")
		var me struct {
			Role        string   `json:"role"`
			Permissions []string `json:"permissions"`
			User        string   `json:"user"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
			t.Fatal(err)
		}

		if me.Role != tc.wantRole {
			t.Errorf("groups %q: role = %q, want %q", tc.groups, me.Role, tc.wantRole)
		}
		canPush := false
		for _, p := range me.Permissions {
			if p == string(auth.PermPush) {
				canPush = true
			}
		}
		if canPush != tc.canPush {
			t.Errorf("groups %q: push permission = %v, want %v", tc.groups, canPush, tc.canPush)
		}
	}
}

// ---------- role enforcement ----------

func TestViewerIsReadOnly(t *testing.T) {
	h := newAuthHarness(t, nil)
	route := h.seedRoute(t, "Someone's route", "wilant")

	// Reading is allowed.
	if resp := h.as("guest", "guests", http.MethodGet, "/api/routes", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("viewer cannot read routes: %d", resp.StatusCode)
	}
	if resp := h.as("guest", "guests", http.MethodGet, "/api/gpx/"+route.Slug, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("viewer cannot download GPX: %d", resp.StatusCode)
	}

	// Everything that changes something is not.
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/push", ""},
		{http.MethodDelete, "/api/routes/" + route.Slug, ""},
		{http.MethodPatch, "/api/routes/" + route.Slug, `{"name":"nope"}`},
		{http.MethodPost, "/api/routes", ""},
	} {
		resp := h.as("guest", "guests", tc.method, tc.path, tc.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403 for a viewer", tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestRiderCanPushAndUpload(t *testing.T) {
	h := newAuthHarness(t, nil)

	if resp := h.as("wilant", "cyclists", http.MethodPost, "/api/push", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("rider cannot push: %d", resp.StatusCode)
	}
}

// The heart of the ownership rule: a rider may not touch someone else's route,
// but an admin may.
func TestRouteOwnership(t *testing.T) {
	h := newAuthHarness(t, nil)
	theirs := h.seedRoute(t, "Friend's route", "friend")
	mine := h.seedRoute(t, "My route", "wilant")

	resp := h.as("wilant", "cyclists", http.MethodDelete, "/api/routes/"+theirs.Slug, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("rider deleted another rider's route: status = %d", resp.StatusCode)
	}

	resp = h.as("wilant", "cyclists", http.MethodPatch, "/api/routes/"+theirs.Slug, `{"name":"hijacked"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("rider edited another rider's route: status = %d", resp.StatusCode)
	}

	resp = h.as("wilant", "cyclists", http.MethodDelete, "/api/routes/"+mine.Slug, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("rider cannot delete their own route: status = %d", resp.StatusCode)
	}

	resp = h.as("boss", "domestique-admins", http.MethodDelete, "/api/routes/"+theirs.Slug, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("admin cannot delete another rider's route: status = %d", resp.StatusCode)
	}
}

// Ownership comes from the session, never the form — otherwise a rider could
// upload as someone else and put the route beyond their own reach.
func TestUploadOwnershipComesFromIdentity(t *testing.T) {
	h := newAuthHarness(t, nil)

	body, contentType := multipartUpload(t, map[string]string{
		"name":       "Sneaky",
		"uploadedBy": "someone-else",
	}, []byte(seedGPX), "route.gpx")

	req, err := http.NewRequest(http.MethodPost, h.base+"/api/routes", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set(auth.HeaderUser, "wilant")
	req.Header.Set(auth.HeaderGroups, "cyclists")

	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload failed: %d", resp.StatusCode)
	}

	var created struct {
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	// The uploader owns it, so they can delete it.
	del := h.as("wilant", "cyclists", http.MethodDelete, "/api/routes/"+created.Slug, "")
	if del.StatusCode != http.StatusNoContent {
		t.Errorf("uploader cannot delete their own upload (status %d) — "+
			"the form's uploadedBy was trusted over the session", del.StatusCode)
	}
}

// ---------- komoot ----------

type fakeKomoot struct {
	tours []komoot.Tour
	err   error
}

func (f fakeKomoot) Tours(context.Context, bool) ([]komoot.Tour, error) {
	return f.tours, f.err
}

func (f fakeKomoot) GPX(_ context.Context, id string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []byte(seedGPX), nil
}

func TestKomootRequiresRider(t *testing.T) {
	h := newAuthHarness(t, fakeKomoot{tours: []komoot.Tour{{ID: "1", Name: "A loop"}}})

	if resp := h.as("guest", "guests", http.MethodGet, "/api/komoot/tours", ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("viewer listed Komoot tours: %d", resp.StatusCode)
	}
	if resp := h.as("wilant", "cyclists", http.MethodGet, "/api/komoot/tours", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("rider cannot list Komoot tours: %d", resp.StatusCode)
	}
}

func TestKomootImportIsIdempotent(t *testing.T) {
	h := newAuthHarness(t, fakeKomoot{tours: []komoot.Tour{
		{ID: "42", Name: "Kemmelberg via Komoot", Type: komoot.TypePlanned},
	}})

	first := h.as("wilant", "cyclists", http.MethodPost, "/api/komoot/import", `{"tourIds":["42"]}`)
	var result struct {
		Imported []string          `json:"imported"`
		Skipped  map[string]string `json:"skipped"`
	}
	if err := json.NewDecoder(first.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 1 {
		t.Fatalf("first import = %+v, want one route", result)
	}

	// Importing again must not duplicate: a rider would not know which copy
	// their device is following.
	second := h.as("wilant", "cyclists", http.MethodPost, "/api/komoot/import", `{"tourIds":["42"]}`)
	result.Imported = nil
	if err := json.NewDecoder(second.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 0 {
		t.Errorf("re-import created a duplicate: %+v", result)
	}
	if result.Skipped["42"] == "" {
		t.Error("re-import gave no reason for skipping")
	}
}

// Komoot's API is undocumented and can vanish. That must read as an upstream
// failure, not as this app being broken.
func TestKomootUpstreamFailureIsBadGateway(t *testing.T) {
	h := newAuthHarness(t, fakeKomoot{err: fmt.Errorf("komoot: could not decode response")})

	resp := h.as("wilant", "cyclists", http.MethodGet, "/api/komoot/tours", "")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

func TestKomootDisabledWhenNotConfigured(t *testing.T) {
	h := newAuthHarness(t, nil)

	resp := h.as("wilant", "cyclists", http.MethodGet, "/api/komoot/tours", "")
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501 when Komoot is not configured", resp.StatusCode)
	}
}

// ---------- linking head units ----------

// An account is yours because you linked it. The rider comes from the session,
// never the body — otherwise someone could create an account they cannot then
// unlink, or plant one on somebody else.
func TestLinkingUsesTheSessionRider(t *testing.T) {
	h := newAuthHarness(t, nil)

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/accounts",
		`{"provider":"wahoo","rider":"somebody-else"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for linking as another rider", resp.StatusCode)
	}

	resp = h.as("wilant", "cyclists", http.MethodPost, "/api/accounts", `{"provider":"wahoo"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("linking own account: status = %d", resp.StatusCode)
	}

	var created struct {
		ID    string `json:"id"`
		Rider string `json:"rider"`
		Mine  bool   `json:"mine"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Rider != "wilant" || created.ID != "wahoo:wilant" {
		t.Errorf("created = %+v, want it owned by the session user", created)
	}
	if !created.Mine {
		t.Error("the linker cannot manage what they just linked")
	}
}

func TestLinkingTwiceIsAConflict(t *testing.T) {
	h := newAuthHarness(t, nil)

	if resp := h.as("wilant", "cyclists", http.MethodPost, "/api/accounts",
		`{"provider":"wahoo"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first link: %d", resp.StatusCode)
	}
	// One head unit per rider per provider; a duplicate would give two rows
	// claiming the same device.
	if resp := h.as("wilant", "cyclists", http.MethodPost, "/api/accounts",
		`{"provider":"wahoo"}`); resp.StatusCode != http.StatusConflict {
		t.Errorf("second link: status = %d, want 409", resp.StatusCode)
	}
}

func TestUnlinkingRespectsOwnership(t *testing.T) {
	h := newAuthHarness(t, nil)

	// The harness links garmin:wilant. Another rider must not remove it.
	if resp := h.as("friend", "cyclists", http.MethodDelete,
		"/api/accounts/garmin:wilant", ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("another rider unlinked it: status = %d, want 403", resp.StatusCode)
	}

	// An admin may.
	if resp := h.as("boss", "domestique-admins", http.MethodDelete,
		"/api/accounts/garmin:wilant", ""); resp.StatusCode != http.StatusNoContent {
		t.Errorf("admin unlink: status = %d, want 204", resp.StatusCode)
	}
}

func TestViewerCannotLink(t *testing.T) {
	h := newAuthHarness(t, nil)

	if resp := h.as("guest", "guests", http.MethodPost, "/api/accounts",
		`{"provider":"garmin"}`); resp.StatusCode != http.StatusForbidden {
		t.Errorf("viewer linked an account: status = %d, want 403", resp.StatusCode)
	}
}

func TestUnlinkingSomethingMissing(t *testing.T) {
	h := newAuthHarness(t, nil)

	if resp := h.as("wilant", "cyclists", http.MethodDelete,
		"/api/accounts/garmin:nobody", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// A route with no targets of its own goes to every linked head unit, so
// linking one immediately gives the routes somewhere to go.
// Linking a new account must not silently expand what an already-existing
// route reaches — that was the exact gap crews exist to close. A route
// with no explicit targets reaches only its owner's own accounts,
// regardless of who else links something afterwards; reaching a stranger's
// account now requires that stranger to be an approved member of a crew
// the route was deliberately shared to.
func TestLinkingANewAccountDoesNotExpandExistingRoutes(t *testing.T) {
	h := newAuthHarness(t, nil)
	h.seedRoute(t, "Private route", "wilant")

	before := h.routeTargets(t)
	if len(before) != 1 || before[0] != "garmin:wilant" {
		t.Fatalf("targets = %v, want only garmin:wilant", before)
	}

	if resp := h.as("friend", "cyclists", http.MethodPost, "/api/accounts",
		`{"provider":"wahoo"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("link: %d", resp.StatusCode)
	}

	after := h.routeTargets(t)
	if len(after) != 1 || after[0] != "garmin:wilant" {
		t.Errorf("targets = %v after a stranger linked an account, want unchanged at [garmin:wilant]", after)
	}
}

// routeTargets reads what the first route currently resolves to —
// SyncState, not Targets: Targets holds the crew ids a route names, not
// the accounts it actually reaches (see config.TargetsFor).
func (h *authHarness) routeTargets(t *testing.T) []string {
	t.Helper()

	resp := h.as("wilant", "cyclists", http.MethodGet, "/api/routes", "")
	var library struct {
		Routes []struct {
			SyncState []struct {
				AccountID string `json:"accountId"`
			} `json:"syncState"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&library); err != nil {
		t.Fatal(err)
	}
	if len(library.Routes) == 0 {
		return nil
	}
	out := make([]string, 0, len(library.Routes[0].SyncState))
	for _, s := range library.Routes[0].SyncState {
		out = append(out, s.AccountID)
	}
	return out
}

// /api/config's Source names the database host and port — internal cluster
// topology, not a rider's business, even though it carries no password (see
// dbx.Redact). Only an admin should see it at all.
func TestConfigSourceIsAdminOnly(t *testing.T) {
	h := newAuthHarness(t, nil)

	configFor := func(user, groups string) string {
		t.Helper()
		resp := h.as(user, groups, http.MethodGet, "/api/config", "")
		var body struct {
			Source string `json:"source"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body.Source
	}

	if got := configFor("wilant", "domestique-admins"); got == "" {
		t.Error("admin got no source, want the database description")
	}
	if got := configFor("wilant", "cyclists"); got != "" {
		t.Errorf("rider got source %q, want it withheld", got)
	}
	if got := configFor("guest", "guests"); got != "" {
		t.Errorf("viewer got source %q, want it withheld", got)
	}
}
