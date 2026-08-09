package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/api"
	"github.com/wncservices/domestique/apps/api/internal/auth"
	"github.com/wncservices/domestique/apps/api/internal/config"
	"github.com/wncservices/domestique/apps/api/internal/komoot"
	"github.com/wncservices/domestique/apps/api/internal/komootlink"
	"github.com/wncservices/domestique/apps/api/internal/secrets"
	"github.com/wncservices/domestique/apps/api/internal/source"
	"github.com/wncservices/domestique/apps/api/internal/state"
)

// fakeConnector records what it was asked to sign in with, so a test can
// assert the password went to Komoot and nowhere else.
type fakeConnector struct {
	email    string
	password string
	fail     bool

	resumedUser  string
	resumedToken string
}

type fakeImporter struct{ tours []komoot.Tour }

func (f *fakeImporter) Tours(bool) ([]komoot.Tour, error) { return f.tours, nil }
func (f *fakeImporter) GPX(string) ([]byte, error)        { return []byte("<gpx/>"), nil }

func (c *fakeConnector) Connect(email, password string) (api.KomootImporter, api.KomootSession, error) {
	c.email, c.password = email, password
	if c.fail {
		return nil, api.KomootSession{}, http.ErrNotSupported
	}
	return &fakeImporter{}, api.KomootSession{
		UserID:      "komoot-user-1",
		Token:       "komoot-token-1",
		DisplayName: "Wilant N",
	}, nil
}

func (c *fakeConnector) Resume(userID, token string) api.KomootImporter {
	c.resumedUser, c.resumedToken = userID, token
	return &fakeImporter{tours: []komoot.Tour{{ID: "42", Name: "A tour"}}}
}

type connectHarness struct {
	t         *testing.T
	client    *http.Client
	base      string
	links     *komootlink.Store
	connector *fakeConnector
	db        *source.DB
}

// newConnectHarness builds a server with Komoot enabled and no environment
// account, which is the deployment this feature is for.
func newConnectHarness(t *testing.T, withKey bool) *connectHarness {
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

	var box *secrets.Box
	if withKey {
		key, err := secrets.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		if box, err = secrets.New(key); err != nil {
			t.Fatal(err)
		}
	}

	links, err := komootlink.UseDB(db.Conn(), db.DSN(), box)
	if err != nil {
		t.Fatal(err)
	}

	authenticator, err := auth.New(auth.Config{
		Mode:  auth.ModeProxy,
		Roles: auth.RoleMapping{Admin: []string{"admins"}, Rider: []string{"cyclists"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	connector := &fakeConnector{}
	srv := &api.Server{
		Source:        db,
		Store:         store,
		Auth:          authenticator,
		Accounts:      seedRoleAccounts(t, db),
		KomootLinks:   links,
		Connector:     connector,
		KomootEnabled: true,
		Config:        &config.Config{Komoot: config.KomootConfig{Enabled: true}},
	}

	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)

	return &connectHarness{t: t, client: server.Client(), base: server.URL,
		links: links, connector: connector, db: db}
}

func (h *connectHarness) as(user, groups, method, path, body string) *http.Response {
	h.t.Helper()

	req, err := http.NewRequest(method, h.base+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Remote-User", user)
	req.Header.Set("Remote-Groups", groups)
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

func decodeConnection(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestConnectStoresTheSessionAndNotThePassword(t *testing.T) {
	h := newConnectHarness(t, true)

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/komoot/connection",
		`{"email":"rider@example.com","password":"hunter2"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body := decodeConnection(t, resp)
	if body["connected"] != true || body["displayName"] != "Wilant N" {
		t.Errorf("body = %v, want a connected account named Wilant N", body)
	}

	// The password reached Komoot...
	if h.connector.password != "hunter2" {
		t.Errorf("connector saw password %q", h.connector.password)
	}

	// ...and nowhere else. This is the reason the feature is built this way.
	var rows int
	if err := h.db.Conn().QueryRow(
		`SELECT COUNT(*) FROM komoot_links WHERE CAST(token AS TEXT) LIKE '%hunter2%'`).
		Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatal("the password appears in the stored token")
	}

	userID, token, err := h.links.Credentials("wilant")
	if err != nil {
		t.Fatal(err)
	}
	if userID != "komoot-user-1" || token != "komoot-token-1" {
		t.Errorf("stored %q/%q, want the session Komoot returned", userID, token)
	}
}

// The rider is whoever is signed in. Letting the body decide would let one
// rider attach an account to another — the same rule as linking a head unit.
func TestConnectIgnoresARiderInTheBody(t *testing.T) {
	h := newConnectHarness(t, true)

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/komoot/connection",
		`{"email":"r@example.com","password":"pw","rider":"someone-else"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if _, err := h.links.Get("wilant"); err != nil {
		t.Errorf("the connection was not stored against the session rider: %v", err)
	}
	if _, err := h.links.Get("someone-else"); err == nil {
		t.Error("the body's rider was honoured")
	}
}

func TestConnectRefusedWithoutAnEncryptionKey(t *testing.T) {
	h := newConnectHarness(t, false)

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/komoot/connection",
		`{"email":"r@example.com","password":"pw"}`)
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412", resp.StatusCode)
	}
	// Refused before signing in: no point sending someone's password to
	// Komoot when the result cannot be kept.
	if h.connector.password != "" {
		t.Error("the password was sent to Komoot despite the refusal")
	}
}

func TestConnectionReportsWhetherConnectingIsPossible(t *testing.T) {
	for _, tc := range []struct {
		name       string
		withKey    bool
		canConnect bool
	}{
		{"with a key", true, true},
		{"without a key", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newConnectHarness(t, tc.withKey)
			body := decodeConnection(t,
				h.as("wilant", "cyclists", http.MethodGet, "/api/komoot/connection", ""))

			if body["canConnect"] != tc.canConnect {
				t.Errorf("canConnect = %v, want %v", body["canConnect"], tc.canConnect)
			}
			if body["connected"] != false {
				t.Errorf("connected = %v, want false before connecting", body["connected"])
			}
		})
	}
}

// A rider's own connection is used for their imports, not somebody else's and
// not the deployment-wide one.
func TestToursUseTheCallersOwnConnection(t *testing.T) {
	h := newConnectHarness(t, true)

	h.as("wilant", "cyclists", http.MethodPost, "/api/komoot/connection",
		`{"email":"r@example.com","password":"pw"}`)

	resp := h.as("wilant", "cyclists", http.MethodGet, "/api/komoot/tours", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if h.connector.resumedToken != "komoot-token-1" {
		t.Errorf("resumed with %q, want the stored token", h.connector.resumedToken)
	}

	// A different rider has connected nothing, and there is no shared
	// account, so there is nothing to import from.
	other := h.as("someone", "cyclists", http.MethodGet, "/api/komoot/tours", "")
	if other.StatusCode != http.StatusNotImplemented {
		t.Errorf("status for an unconnected rider = %d, want 501", other.StatusCode)
	}
}

func TestDisconnectRemovesOnlyTheCallersConnection(t *testing.T) {
	h := newConnectHarness(t, true)

	h.as("wilant", "cyclists", http.MethodPost, "/api/komoot/connection",
		`{"email":"a@example.com","password":"pw"}`)
	h.as("friend", "cyclists", http.MethodPost, "/api/komoot/connection",
		`{"email":"b@example.com","password":"pw"}`)

	if resp := h.as("wilant", "cyclists", http.MethodDelete, "/api/komoot/connection", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if _, err := h.links.Get("wilant"); err == nil {
		t.Error("the caller's connection survived")
	}
	if _, err := h.links.Get("friend"); err != nil {
		t.Errorf("another rider's connection was removed: %v", err)
	}
}

// Komoot rejecting the details must not leave a half-made connection, and the
// response must not echo Komoot's own error, which can contain the request.
func TestFailedLoginStoresNothing(t *testing.T) {
	h := newConnectHarness(t, true)
	h.connector.fail = true

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/komoot/connection",
		`{"email":"r@example.com","password":"wrong"}`)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}

	body := decodeConnection(t, resp)
	if msg, _ := body["error"].(string); strings.Contains(msg, "wrong") {
		t.Errorf("the error echoes the password: %q", msg)
	}
	if _, err := h.links.Get("wilant"); err == nil {
		t.Error("a connection was stored despite the failed login")
	}
}

func TestConnectNeedsTheKomootPermission(t *testing.T) {
	h := newConnectHarness(t, true)

	// A viewer may read routes and nothing else.
	resp := h.as("guest", "", http.MethodPost, "/api/komoot/connection",
		`{"email":"r@example.com","password":"pw"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	if h.connector.password != "" {
		t.Error("a viewer's details were sent to Komoot")
	}
}

func TestConnectRejectsMissingFields(t *testing.T) {
	h := newConnectHarness(t, true)

	for _, body := range []string{`{"email":"","password":"pw"}`, `{"email":"a@b.c","password":""}`, `{`} {
		resp := h.as("wilant", "cyclists", http.MethodPost, "/api/komoot/connection", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q gave status %d, want 400", body, resp.StatusCode)
		}
	}
}
