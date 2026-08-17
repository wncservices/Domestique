// Acceptance tests: every backend endpoint, driven over real HTTP against a
// real server, in both source modes.
//
// These are deliberately black-box (package api_test): they exercise what a
// browser or a script would actually get, including status codes, headers and
// JSON shapes. Unit tests live alongside the code they cover; this file is the
// contract.
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wncservices/domestique/apps/api/internal/accounts"
	"github.com/wncservices/domestique/apps/api/internal/api"
	"github.com/wncservices/domestique/apps/api/internal/config"
	"github.com/wncservices/domestique/apps/api/internal/crew"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/source"
	"github.com/wncservices/domestique/apps/api/internal/state"
	"github.com/wncservices/domestique/apps/api/internal/targets"
)

// ---------- harness ----------

type harness struct {
	t      *testing.T
	client *http.Client
	base   string
	store  state.Store
	source *source.DB
	// pushed records what the fake adapters were asked to do.
	pushed *fakeLedger
}

type fakeLedger struct {
	creates []string
	updates []string
	deletes []string
	// failOn makes the adapter for this account id fail every call.
	failOn string
}

type fakeTarget struct {
	account model.Account
	ledger  *fakeLedger
}

func (f *fakeTarget) Create(_ context.Context, route model.Route) (string, error) {
	if f.account.ID == f.ledger.failOn {
		return "", fmt.Errorf("provider is having a bad day")
	}
	f.ledger.creates = append(f.ledger.creates, f.account.ID+":"+route.Slug)
	return "remote-" + route.Slug, nil
}

func (f *fakeTarget) Update(_ context.Context, remoteID string, route model.Route) (string, error) {
	if f.account.ID == f.ledger.failOn {
		return "", fmt.Errorf("provider is having a bad day")
	}
	f.ledger.updates = append(f.ledger.updates, f.account.ID+":"+route.Slug)
	return remoteID, nil
}

func (f *fakeTarget) Delete(context.Context, string) error {
	if f.account.ID == f.ledger.failOn {
		return fmt.Errorf("provider is having a bad day")
	}
	f.ledger.deletes = append(f.ledger.deletes, f.account.ID)
	return nil
}

// seedAccounts links two head units the way riders would through the UI.
func seedAccounts(t *testing.T, db *source.DB) *accounts.Store {
	t.Helper()

	store, err := accounts.UseDB(db.Conn(), db.DSN())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []struct {
		provider model.Provider
		rider    string
	}{
		{model.ProviderGarmin, "one"},
		{model.ProviderWahoo, "two"},
	} {
		if _, err := store.Link(a.provider, a.rider, ""); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// seedCrews sets up the crews these tests share a route through. Every
// request in this harness resolves to rider "local" (mode: none's
// LocalIdentity), so "local" is what has to belong to a crew for an
// uploaded route's targets to validate at write time and resolve to
// riders "one"/"two"'s linked accounts at plan time — a route can no
// longer name a raw account id directly.
//
//	crew:shared  — one, two — the old "reaches every linked account" default
//	crew:soloone — one only — narrows a push to garmin:one specifically
//	crew:solotwo — two only — narrows a push to wahoo:two specifically
func seedCrews(t *testing.T, db *source.DB) *crew.Store {
	t.Helper()

	store, err := crew.UseDB(db.Conn(), db.DSN())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name    string
		members []string
	}{
		{"Shared", []string{"one", "two"}},
		{"SoloOne", []string{"one"}},
		{"SoloTwo", []string{"two"}},
	} {
		created, err := store.Create(context.Background(), c.name, "local")
		if err != nil {
			t.Fatal(err)
		}
		for _, rider := range c.members {
			if _, err := store.RequestJoin(context.Background(), created.ID, rider); err != nil {
				t.Fatal(err)
			}
			if err := store.Approve(context.Background(), created.ID, rider, "local"); err != nil {
				t.Fatal(err)
			}
		}
	}
	return store
}

// newHarness starts a server over real HTTP against a fresh database.
func newHarness(t *testing.T) *harness {
	t.Helper()

	src, err := source.OpenDB(filepath.Join(t.TempDir(), "routes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })

	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	ledger := &fakeLedger{}
	srv := &api.Server{
		Source:   src,
		Store:    store,
		Accounts: seedAccounts(t, src),
		Crew:     seedCrews(t, src),
		Config:   &config.Config{},
		// A minimal SPA, so the fallback behaviour is covered too.
		WebFS: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>app</html>")}},
		TargetFactory: func(account model.Account) (targets.Target, error) {
			return &fakeTarget{account: account, ledger: ledger}, nil
		},
	}

	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)

	return &harness{
		t:      t,
		client: server.Client(),
		base:   server.URL,
		store:  store,
		source: src,
		pushed: ledger,
	}
}

func (h *harness) do(method, path string, body io.Reader, contentType string) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(method, h.base+path, body)
	if err != nil {
		h.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (h *harness) get(path string) *http.Response { return h.do(http.MethodGet, path, nil, "") }

func (h *harness) decode(resp *http.Response, into any) {
	h.t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		h.t.Fatalf("decode %s: %v", resp.Request.URL.Path, err)
	}
}

func (h *harness) expectStatus(resp *http.Response, want int) {
	h.t.Helper()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("%s %s: status = %d, want %d (body: %s)",
			resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, want, truncate(body))
	}
}

// upload posts a multipart GPX the way the browser does.
func (h *harness) upload(fields map[string]string, gpx []byte, filename string) *http.Response {
	h.t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			h.t.Fatal(err)
		}
	}
	if gpx != nil {
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			h.t.Fatal(err)
		}
		if _, err := part.Write(gpx); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		h.t.Fatal(err)
	}

	return h.do(http.MethodPost, "/api/routes", &buf, writer.FormDataContentType())
}

// syncHarness is a database library with two head units linked and one route
// in it — everything sync needs. The fs harness deliberately has no accounts,
// because a directory library has no database to link against.
func syncHarness(t *testing.T) (*harness, routeDTO) {
	t.Helper()
	h := newHarness(t)
	return h, h.uploadExample("Kemmelberg Loop")
}

// uploadExample shares the route to crew:shared — one, two — so a route
// with no targets of its own reaches every linked account, the closest
// equivalent to the old nil-target default now that nil means "the
// owner's own accounts only" (see config.TargetsFor). Every request in
// this harness resolves to rider "local", which owns no linked account of
// its own — without an explicit crew, an uploaded route would reach
// nobody.
func (h *harness) uploadExample(name string) routeDTO {
	h.t.Helper()
	resp := h.upload(map[string]string{"name": name, "targets": "crew:shared"}, exampleGPX(h.t), "route.gpx")
	h.expectStatus(resp, http.StatusCreated)
	var route routeDTO
	h.decode(resp, &route)
	return route
}

func exampleGPX(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "kemmelberg-loop.gpx"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func truncate(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return strings.TrimSpace(string(b))
}

// Mirrors of the API's JSON, declared here on purpose: if the server changes
// shape, these tests should notice.
type routeDTO struct {
	Slug           string   `json:"slug"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Tags           []string `json:"tags"`
	DistanceM      float64  `json:"distanceM"`
	AscentM        float64  `json:"ascentM"`
	PointCount     int      `json:"pointCount"`
	ContentHash    string   `json:"contentHash"`
	Origin         string   `json:"origin"`
	Targets        []string `json:"targets"`
	UnknownTargets []string `json:"unknownTargets"`
	SyncState      []struct {
		AccountID string `json:"accountId"`
		Status    string `json:"status"`
		RemoteID  string `json:"remoteId"`
	} `json:"syncState"`
}

type libraryDTO struct {
	Routes   []routeDTO `json:"routes"`
	Problems []string   `json:"problems"`
}

type planDTO struct {
	Items []struct {
		Op        string `json:"op"`
		AccountID string `json:"accountId"`
		Slug      string `json:"slug"`
		Reason    string `json:"reason"`
	} `json:"items"`
	InSync   int      `json:"inSync"`
	Problems []string `json:"problems"`
}

type pushDTO struct {
	Applied  int      `json:"applied"`
	Failures []string `json:"failures"`
}

// ---------- read endpoints ----------

func TestHealth(t *testing.T) {
	h := newHarness(t)
	resp := h.get("/api/health")
	h.expectStatus(resp, http.StatusOK)

	var body map[string]string
	h.decode(resp, &body)
	if body["status"] != "ok" {
		t.Errorf("status = %q, want ok", body["status"])
	}
}

func TestConfigEndpoint(t *testing.T) {
	h := newHarness(t)
	resp := h.get("/api/config")
	h.expectStatus(resp, http.StatusOK)

	var body struct {
		Source string `json:"source"`
		Komoot string `json:"komoot"`
	}
	h.decode(resp, &body)

	// The library names its engine, so the UI and the logs say which one is in
	// use — sqlite on a laptop, postgres in the cluster.
	if !strings.HasPrefix(body.Source, "sqlite database") {
		t.Errorf("source = %q, want it to name the engine", body.Source)
	}
	// The frontend decides whether to render the Komoot panel from this, so an
	// empty string here hides a feature rather than merely losing a label.
	if body.Komoot != "disabled" {
		t.Errorf("komoot = %q, want disabled", body.Komoot)
	}
}

func TestAccountsEndpoint(t *testing.T) {
	h := newHarness(t)
	resp := h.get("/api/accounts")
	h.expectStatus(resp, http.StatusOK)

	var accounts []struct {
		ID          string `json:"id"`
		Provider    string `json:"provider"`
		Label       string `json:"label"`
		Implemented bool   `json:"implemented"`
	}
	h.decode(resp, &accounts)

	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want the two that were linked", len(accounts))
	}
	// The UI shows "adapter not wired up" from this, so it has to say what is
	// true of each provider — both push for real now.
	for _, account := range accounts {
		if !account.Implemented {
			t.Errorf("%s reports implemented=false, want true", account.ID)
		}
		if account.Label == "" {
			t.Errorf("%s has no label", account.ID)
		}
	}
}

// An account's label is set once, at link time, from the provider's own
// session (see duplicateRiders' doc comment) — not typed by a rider. Two
// different riders' accounts sharing one is the real-world signal this
// deployment's own history produced: the same physical Garmin login, linked
// twice because an OIDC login resolved to a rider string not yet recognised
// as an existing person.
func TestAccountsEndpointFlagsPossibleDuplicatesByMatchingLabel(t *testing.T) {
	h := newHarness(t)

	acctStore, err := accounts.UseDB(h.source.Conn(), h.source.DSN())
	if err != nil {
		t.Fatal(err)
	}
	// seedAccounts links model.ProviderGarmin for rider "one" with an empty
	// label, which defaults to "one's Garmin". A second rider linking the
	// same real Garmin account gets its own display name as a label — here,
	// deliberately the same string, simulating that shared real account.
	if _, err := acctStore.Link(model.ProviderGarmin, "duplicate-of-one", "one's Garmin"); err != nil {
		t.Fatal(err)
	}
	// A Wahoo account with a label that happens to match nothing else must
	// not be flagged — different provider, no real collision.
	if _, err := acctStore.Link(model.ProviderWahoo, "three", "one's Garmin"); err != nil {
		t.Fatal(err)
	}

	resp := h.get("/api/accounts")
	h.expectStatus(resp, http.StatusOK)

	var out []struct {
		Rider               string   `json:"rider"`
		Provider            string   `json:"provider"`
		Label               string   `json:"label"`
		PossibleDuplicateOf []string `json:"possibleDuplicateOf"`
	}
	h.decode(resp, &out)

	byRider := map[string][]string{}
	for _, a := range out {
		byRider[a.Rider] = a.PossibleDuplicateOf
	}

	if got := byRider["one"]; len(got) != 1 || got[0] != "duplicate-of-one" {
		t.Errorf("one's duplicates = %v, want [duplicate-of-one]", got)
	}
	if got := byRider["duplicate-of-one"]; len(got) != 1 || got[0] != "one" {
		t.Errorf("duplicate-of-one's duplicates = %v, want [one]", got)
	}
	// "three" shares the label string with "one" and "duplicate-of-one" but
	// on a different provider (wahoo, not garmin) — must not be flagged.
	if got := byRider["three"]; len(got) != 0 {
		t.Errorf("three's duplicates = %v, want none (different provider)", got)
	}
}

// The real, end-to-end wiring for the grouping logic
// routeduplicates_test.go proves in isolation: two uploads of the same GPX
// under the same name (what a repeated Garmin sync-back import produces)
// come back grouped; a route with nothing repeating it does not appear at
// all.
func TestRouteDuplicatesEndpointGroupsRepeatedImports(t *testing.T) {
	h := newHarness(t)
	h.uploadExample("Kemmelberg Loop")
	h.uploadExample("Kemmelberg Loop")
	h.uploadExample("Solo Ride")

	resp := h.get("/api/routes/duplicates")
	h.expectStatus(resp, http.StatusOK)

	var groups []struct {
		Name   string     `json:"name"`
		Routes []routeDTO `json:"routes"`
	}
	h.decode(resp, &groups)
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want exactly one", groups)
	}
	if groups[0].Name != "Kemmelberg Loop" || len(groups[0].Routes) != 2 {
		t.Errorf("group = %+v, want the two Kemmelberg Loop uploads", groups[0])
	}
}

func TestRoutesEndpointOnEmptyDatabase(t *testing.T) {
	h := newHarness(t)
	resp := h.get("/api/routes")
	h.expectStatus(resp, http.StatusOK)

	var library libraryDTO
	h.decode(resp, &library)

	// Empty lists must be [] not null, or the frontend has to null-check.
	if library.Routes == nil || library.Problems == nil {
		t.Fatalf("null arrays in response: %+v", library)
	}
	if len(library.Routes) != 0 {
		t.Errorf("got %d routes on a fresh database", len(library.Routes))
	}
}

func TestRoutesEndpointReportsStatsAndTargets(t *testing.T) {
	h, _ := syncHarness(t)
	var library libraryDTO
	h.decode(h.get("/api/routes"), &library)

	if len(library.Routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(library.Routes))
	}
	route := library.Routes[0]

	if route.Slug != "kemmelberg-loop" {
		t.Errorf("slug = %q", route.Slug)
	}
	if route.DistanceM < 5000 || route.DistanceM > 6000 {
		t.Errorf("distance = %.0f m, want ~5570", route.DistanceM)
	}
	if route.AscentM == 0 || route.PointCount == 0 || route.ContentHash == "" {
		t.Errorf("derived fields missing: %+v", route)
	}
	// Targets holds the crew named at write time (see uploadExample), not
	// the accounts it resolves to — that resolved reach is SyncState.
	if len(route.Targets) != 1 || route.Targets[0] != "crew:shared" {
		t.Errorf("targets = %v, want [crew:shared]", route.Targets)
	}
	if len(route.SyncState) != 2 {
		t.Fatalf("syncState = %v, want one per resolved account", route.SyncState)
	}
	for _, status := range route.SyncState {
		if status.Status != "pending" {
			t.Errorf("%s: status = %q, want pending on a fresh state",
				status.AccountID, status.Status)
		}
	}
}

func TestTrackEndpoint(t *testing.T) {
	h, _ := syncHarness(t)
	resp := h.get("/api/tracks/kemmelberg-loop")
	h.expectStatus(resp, http.StatusOK)

	var body struct {
		Slug   string       `json:"slug"`
		Points [][2]float64 `json:"points"`
	}
	h.decode(resp, &body)

	if body.Slug != "kemmelberg-loop" {
		t.Errorf("slug = %q", body.Slug)
	}
	if len(body.Points) < 2 {
		t.Fatalf("got %d points, want the whole track", len(body.Points))
	}
	if lat := body.Points[0][0]; lat < 50 || lat > 51 {
		t.Errorf("first point looks wrong: %v", body.Points[0])
	}
}

func TestTrackEndpointMissingRoute(t *testing.T) {
	h := newHarness(t)
	h.expectStatus(h.get("/api/tracks/no-such-route"), http.StatusNotFound)
}

func TestGPXDownload(t *testing.T) {
	h, _ := syncHarness(t)
	resp := h.get("/api/gpx/kemmelberg-loop")
	h.expectStatus(resp, http.StatusOK)

	if ct := resp.Header.Get("Content-Type"); ct != "application/gpx+xml" {
		t.Errorf("content-type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "kemmelberg-loop.gpx") {
		t.Errorf("content-disposition = %q, want a .gpx filename", cd)
	}

	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("<trkpt")) {
		t.Errorf("downloaded file is not a GPX: %s", truncate(body))
	}
}

func TestGPXDownloadMissingRoute(t *testing.T) {
	h := newHarness(t)
	h.expectStatus(h.get("/api/gpx/no-such-route"), http.StatusNotFound)
}

// ---------- plan and push ----------

func TestPlanThenPushThenPlanIsEmpty(t *testing.T) {
	h, _ := syncHarness(t)

	var before planDTO
	h.decode(h.get("/api/plan"), &before)
	if len(before.Items) != 2 {
		t.Fatalf("plan = %+v, want one create per account", before.Items)
	}
	for _, item := range before.Items {
		if item.Op != "create" {
			t.Errorf("%s: op = %q, want create", item.AccountID, item.Op)
		}
	}
	if before.InSync != 0 {
		t.Errorf("inSync = %d, want 0", before.InSync)
	}

	resp := h.do(http.MethodPost, "/api/push", nil, "")
	h.expectStatus(resp, http.StatusOK)

	var push pushDTO
	h.decode(resp, &push)
	if push.Applied != 2 || len(push.Failures) != 0 {
		t.Fatalf("push = %+v, want 2 applied and no failures", push)
	}
	if len(h.pushed.creates) != 2 {
		t.Errorf("adapters saw %v, want two creates", h.pushed.creates)
	}

	// The whole point of recording state: a second run is a no-op.
	var after planDTO
	h.decode(h.get("/api/plan"), &after)
	if len(after.Items) != 0 {
		t.Fatalf("re-plan after a push = %+v, want nothing", after.Items)
	}
	if after.InSync != 2 {
		t.Errorf("inSync = %d, want 2", after.InSync)
	}

	// And the route now reports itself synced, with a remote id.
	var library libraryDTO
	h.decode(h.get("/api/routes"), &library)
	for _, status := range library.Routes[0].SyncState {
		if status.Status != "synced" {
			t.Errorf("%s: status = %q, want synced", status.AccountID, status.Status)
		}
		if status.RemoteID == "" {
			t.Errorf("%s: no remote id recorded", status.AccountID)
		}
	}
}

// A rider can narrow a push to specific plan items — the other account's
// change is computed but left untouched, exactly as if it had never been
// pushed at all.
func TestPushCanBeLimitedToASelection(t *testing.T) {
	h, route := syncHarness(t)

	body := fmt.Sprintf(`{"items":[{"accountId":"garmin:one","slug":%q}]}`, route.Slug)
	resp := h.do(http.MethodPost, "/api/push", strings.NewReader(body), "application/json")
	h.expectStatus(resp, http.StatusOK)

	var push pushDTO
	h.decode(resp, &push)
	if push.Applied != 1 || len(push.Failures) != 0 {
		t.Fatalf("push = %+v, want 1 applied and no failures", push)
	}
	if len(h.pushed.creates) != 1 {
		t.Errorf("adapters saw %v, want exactly one create", h.pushed.creates)
	}

	var library libraryDTO
	h.decode(h.get("/api/routes"), &library)
	for _, status := range library.Routes[0].SyncState {
		want := "pending"
		if status.AccountID == "garmin:one" {
			want = "synced"
		}
		if status.Status != want {
			t.Errorf("%s: status = %q, want %q", status.AccountID, status.Status, want)
		}
	}

	// A second push with no body catches the rest, matching the old
	// push-everything behaviour.
	resp = h.do(http.MethodPost, "/api/push", nil, "")
	h.expectStatus(resp, http.StatusOK)
	h.decode(resp, &push)
	if push.Applied != 1 {
		t.Fatalf("second push = %+v, want 1 applied (the remaining account)", push)
	}
}

// An empty selection ({"items":[]}) behaves like no body at all, not like
// "push nothing" — a client that always sends the field should not have its
// pushes silently no-op.
func TestPushWithEmptySelectionPushesEverything(t *testing.T) {
	h, _ := syncHarness(t)

	resp := h.do(http.MethodPost, "/api/push", strings.NewReader(`{"items":[]}`), "application/json")
	h.expectStatus(resp, http.StatusOK)

	var push pushDTO
	h.decode(resp, &push)
	if push.Applied != 2 {
		t.Fatalf("push = %+v, want 2 applied", push)
	}
}

// One provider failing must not stop the other rider's routes going out.
func TestPushReportsPerAccountFailures(t *testing.T) {
	h, _ := syncHarness(t)
	h.pushed.failOn = "wahoo:two"

	resp := h.do(http.MethodPost, "/api/push", nil, "")
	h.expectStatus(resp, http.StatusOK)

	var push pushDTO
	h.decode(resp, &push)

	if push.Applied != 1 {
		t.Errorf("applied = %d, want 1 (the healthy account)", push.Applied)
	}
	if len(push.Failures) != 1 {
		t.Fatalf("failures = %v, want exactly one", push.Failures)
	}
	if !strings.Contains(push.Failures[0], "wahoo:two") {
		t.Errorf("failure does not name the account: %q", push.Failures[0])
	}

	// The failed account must still be pending, so the next push retries it.
	var library libraryDTO
	h.decode(h.get("/api/routes"), &library)
	for _, status := range library.Routes[0].SyncState {
		want := "synced"
		if status.AccountID == "wahoo:two" {
			want = "pending"
		}
		if status.Status != want {
			t.Errorf("%s: status = %q, want %q", status.AccountID, status.Status, want)
		}
	}
}

func TestPushWithNothingToDo(t *testing.T) {
	h := newHarness(t) // empty library

	resp := h.do(http.MethodPost, "/api/push", nil, "")
	h.expectStatus(resp, http.StatusOK)

	var push pushDTO
	h.decode(resp, &push)
	if push.Applied != 0 || len(push.Failures) != 0 {
		t.Errorf("push on an empty library = %+v", push)
	}
}

// ---------- uploads ----------

func TestUploadLifecycle(t *testing.T) {
	h := newHarness(t)

	resp := h.upload(map[string]string{
		"name":        "Kemmelberg Loop",
		"description": "Cobbles and regret",
		"tags":        "gravel, hills",
		"targets":     "crew:soloone",
		"uploadedBy":  "wilant",
	}, exampleGPX(t), "kemmelberg.gpx")
	h.expectStatus(resp, http.StatusCreated)

	var created routeDTO
	h.decode(resp, &created)

	if created.Slug != "kemmelberg-loop" {
		t.Errorf("slug = %q", created.Slug)
	}
	if created.Description != "Cobbles and regret" {
		t.Errorf("description = %q", created.Description)
	}
	if len(created.Tags) != 2 || created.Tags[0] != "gravel" {
		t.Errorf("tags = %v, want [gravel hills]", created.Tags)
	}
	// Targets holds the crew id named at write time, not the accounts it
	// resolves to — that resolved reach is what the plan assertion below
	// checks.
	if len(created.Targets) != 1 || created.Targets[0] != "crew:soloone" {
		t.Errorf("targets = %v, want only crew:soloone", created.Targets)
	}
	if created.DistanceM == 0 || created.ContentHash == "" {
		t.Errorf("stats not derived on upload: %+v", created)
	}
	if created.Origin != "database" {
		t.Errorf("origin = %q, want database", created.Origin)
	}

	// It shows up in the library, and only plans for the account it named.
	var library libraryDTO
	h.decode(h.get("/api/routes"), &library)
	if len(library.Routes) != 1 {
		t.Fatalf("got %d routes after upload", len(library.Routes))
	}

	var plan planDTO
	h.decode(h.get("/api/plan"), &plan)
	if len(plan.Items) != 1 {
		t.Fatalf("plan = %+v, want a single create", plan.Items)
	}
	if plan.Items[0].AccountID != "garmin:one" {
		t.Errorf("planned for %s; per-route targets were ignored", plan.Items[0].AccountID)
	}
}

func TestUploadDerivesNameFromFilename(t *testing.T) {
	h := newHarness(t)

	resp := h.upload(nil, exampleGPX(t), "mont-ventoux.gpx")
	h.expectStatus(resp, http.StatusCreated)

	var created routeDTO
	h.decode(resp, &created)
	if created.Name != "Mont Ventoux" {
		t.Errorf("name = %q, want it derived from the filename", created.Name)
	}
	if created.Slug != "mont-ventoux" {
		t.Errorf("slug = %q", created.Slug)
	}
}

func TestUploadRejectsBadInput(t *testing.T) {
	h := newHarness(t)

	t.Run("no file", func(t *testing.T) {
		h.expectStatus(h.upload(map[string]string{"name": "x"}, nil, ""), http.StatusBadRequest)
	})

	t.Run("not a gpx", func(t *testing.T) {
		resp := h.upload(nil, []byte("just some text"), "notes.txt")
		h.expectStatus(resp, http.StatusBadRequest)

		var body map[string]string
		h.decode(resp, &body)
		if body["error"] == "" {
			t.Error("no error message for the caller to show")
		}
	})

	t.Run("single point", func(t *testing.T) {
		gpx := []byte(`<gpx version="1.1"><trk><trkseg>` +
			`<trkpt lat="50" lon="3"/></trkseg></trk></gpx>`)
		h.expectStatus(h.upload(nil, gpx, "short.gpx"), http.StatusBadRequest)
	})

	t.Run("not multipart", func(t *testing.T) {
		resp := h.do(http.MethodPost, "/api/routes",
			strings.NewReader(`{"name":"x"}`), "application/json")
		h.expectStatus(resp, http.StatusBadRequest)
	})
}

func TestUploadDisambiguatesSlugs(t *testing.T) {
	h := newHarness(t)

	first := h.uploadExample("Kemmelberg Loop")
	second := h.uploadExample("Kemmelberg Loop")

	if first.Slug == second.Slug {
		t.Fatalf("both uploads got slug %q", first.Slug)
	}
	if second.Slug != "kemmelberg-loop-2" {
		t.Errorf("second slug = %q, want kemmelberg-loop-2", second.Slug)
	}
}

// ---------- edits ----------

func TestPatchRenameMakesRouteStale(t *testing.T) {
	h := newHarness(t)
	route := h.uploadExample("Before")

	// Get it synced first.
	h.do(http.MethodPost, "/api/push", nil, "")

	resp := h.do(http.MethodPatch, "/api/routes/"+route.Slug,
		strings.NewReader(`{"name":"After"}`), "application/json")
	h.expectStatus(resp, http.StatusOK)

	var patched routeDTO
	h.decode(resp, &patched)
	if patched.Name != "After" {
		t.Errorf("name = %q", patched.Name)
	}
	// Providers display the name, so a rename has to reach them.
	if patched.ContentHash == route.ContentHash {
		t.Fatal("content hash unchanged after a rename; it would never sync")
	}

	var plan planDTO
	h.decode(h.get("/api/plan"), &plan)
	if len(plan.Items) == 0 {
		t.Fatal("rename produced no plan items")
	}
	for _, item := range plan.Items {
		if item.Op != "update" {
			t.Errorf("op = %q, want update after a rename", item.Op)
		}
	}
}

func TestPatchDisablingRouteQueuesDeletes(t *testing.T) {
	h := newHarness(t)
	route := h.uploadExample("Temporary")
	h.do(http.MethodPost, "/api/push", nil, "")

	resp := h.do(http.MethodPatch, "/api/routes/"+route.Slug,
		strings.NewReader(`{"enabled":false}`), "application/json")
	h.expectStatus(resp, http.StatusOK)

	var library libraryDTO
	h.decode(h.get("/api/routes"), &library)
	if len(library.Routes) != 0 {
		t.Errorf("disabled route still listed: %+v", library.Routes)
	}

	var plan planDTO
	h.decode(h.get("/api/plan"), &plan)
	if len(plan.Items) != 2 {
		t.Fatalf("plan = %+v, want a delete per account", plan.Items)
	}
	for _, item := range plan.Items {
		if item.Op != "delete" {
			t.Errorf("op = %q, want delete", item.Op)
		}
	}
}

func TestPatchRetargetsRoute(t *testing.T) {
	h := newHarness(t)
	route := h.uploadExample("Shared")

	resp := h.do(http.MethodPatch, "/api/routes/"+route.Slug,
		strings.NewReader(`{"targets":["crew:solotwo"]}`), "application/json")
	h.expectStatus(resp, http.StatusOK)

	var plan planDTO
	h.decode(h.get("/api/plan"), &plan)
	for _, item := range plan.Items {
		if item.AccountID != "wahoo:two" {
			t.Errorf("still planning for %s after retargeting", item.AccountID)
		}
	}
}

func TestPatchMissingRoute(t *testing.T) {
	h := newHarness(t)
	resp := h.do(http.MethodPatch, "/api/routes/nope",
		strings.NewReader(`{"name":"x"}`), "application/json")
	h.expectStatus(resp, http.StatusNotFound)
}

func TestPatchRejectsMalformedJSON(t *testing.T) {
	h := newHarness(t)
	route := h.uploadExample("Fine")
	resp := h.do(http.MethodPatch, "/api/routes/"+route.Slug,
		strings.NewReader(`{not json`), "application/json")
	h.expectStatus(resp, http.StatusBadRequest)
}

// ---------- deletes ----------

func TestDeleteRemovesRouteAndQueuesRemoteDeletes(t *testing.T) {
	h := newHarness(t)
	route := h.uploadExample("Doomed")
	h.do(http.MethodPost, "/api/push", nil, "")

	h.expectStatus(h.do(http.MethodDelete, "/api/routes/"+route.Slug, nil, ""),
		http.StatusNoContent)

	var library libraryDTO
	h.decode(h.get("/api/routes"), &library)
	if len(library.Routes) != 0 {
		t.Errorf("route still listed after delete")
	}

	// Deleting locally must also take it off the devices.
	var plan planDTO
	h.decode(h.get("/api/plan"), &plan)
	if len(plan.Items) != 2 {
		t.Fatalf("plan = %+v, want a delete per account", plan.Items)
	}

	resp := h.do(http.MethodPost, "/api/push", nil, "")
	var push pushDTO
	h.decode(resp, &push)
	if len(h.pushed.deletes) != 2 {
		t.Errorf("adapters saw deletes %v, want two", h.pushed.deletes)
	}

	// Once removed everywhere, there is nothing left to do.
	var after planDTO
	h.decode(h.get("/api/plan"), &after)
	if len(after.Items) != 0 {
		t.Errorf("plan after cleanup = %+v, want empty", after.Items)
	}
}

func TestDeleteMissingRoute(t *testing.T) {
	h := newHarness(t)
	h.expectStatus(h.do(http.MethodDelete, "/api/routes/nope", nil, ""), http.StatusNotFound)
}

// ---------- routing and safety ----------

func TestUnknownAPIPathReturnsJSON404(t *testing.T) {
	h := newHarness(t)
	resp := h.get("/api/does-not-exist")
	h.expectStatus(resp, http.StatusNotFound)

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want JSON — HTML here is unusable by a client", ct)
	}
}

func TestSPAFallbackServesTheApp(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{"/", "/some/client/route"} {
		resp := h.get(path)
		h.expectStatus(resp, http.StatusOK)
		body, _ := io.ReadAll(resp.Body)
		if !bytes.Contains(body, []byte("<html>app</html>")) {
			t.Errorf("%s: served %q, want the SPA shell", path, truncate(body))
		}
	}
}

// Slugs arrive from the URL, so traversal must not escape the library root.
func TestPathTraversalIsRefused(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{
		"/api/gpx/%2e%2e%2f%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"/api/tracks/%2e%2e%2f%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"/api/gpx/kemmelberg-loop%2f%2e%2e%2f%2e%2e%2f%2e%2e%2fetc%2fpasswd",
	} {
		resp := h.get(path)
		body, _ := io.ReadAll(resp.Body)
		if bytes.Contains(body, []byte("root:")) {
			t.Fatalf("%s served /etc/passwd", path)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, resp.StatusCode)
		}
	}
}

// An upload with no explicit target choice defaults to whatever auto-share
// crews the uploader currently belongs to, instead of reaching nobody but
// their own accounts.
func TestUploadDefaultsToAutoShareCrews(t *testing.T) {
	h := newHarness(t)

	h.expectStatus(h.do(http.MethodPatch, "/api/crews/crew:soloone",
		strings.NewReader(`{"autoShare":true}`), "application/json"), http.StatusOK)

	resp := h.upload(map[string]string{"name": "No Choice Made"}, exampleGPX(t), "no-choice.gpx")
	h.expectStatus(resp, http.StatusCreated)

	var created routeDTO
	h.decode(resp, &created)
	if len(created.Targets) != 1 || created.Targets[0] != "crew:soloone" {
		t.Errorf("targets = %v, want only crew:soloone", created.Targets)
	}
}

// Auto-share only fills in a default — it never overrides an explicit
// choice the uploader actually made.
func TestExplicitTargetsOverrideAutoShare(t *testing.T) {
	h := newHarness(t)

	h.expectStatus(h.do(http.MethodPatch, "/api/crews/crew:soloone",
		strings.NewReader(`{"autoShare":true}`), "application/json"), http.StatusOK)

	resp := h.upload(map[string]string{
		"name":    "Deliberate Choice",
		"targets": "crew:solotwo",
	}, exampleGPX(t), "deliberate.gpx")
	h.expectStatus(resp, http.StatusCreated)

	var created routeDTO
	h.decode(resp, &created)
	if len(created.Targets) != 1 || created.Targets[0] != "crew:solotwo" {
		t.Errorf("targets = %v, want only the explicitly chosen crew:solotwo", created.Targets)
	}
}

// A route can no longer be created naming a target its owner does not
// belong to — write-time validation rejects it outright now, rather than
// accepting it and leaving it silently unsynced the way a typo'd raw
// account id used to.
func TestUploadRejectsAnUnknownTarget(t *testing.T) {
	h := newHarness(t)

	resp := h.upload(map[string]string{
		"name":    "Typo",
		"targets": "crew:does-not-exist",
	}, exampleGPX(t), "typo.gpx")
	h.expectStatus(resp, http.StatusBadRequest)

	var library libraryDTO
	h.decode(h.get("/api/routes"), &library)
	if len(library.Routes) != 0 {
		t.Errorf("a route with an invalid target was still created: %+v", library.Routes)
	}
}

// A crew reference that was valid when a route was shared to it can go
// stale later — the crew gets deleted — without the route itself ever
// being touched. UnknownTargets is what tells a rider their route quietly
// stopped syncing anywhere, rather than leaving it silent — the same
// contract an unlinked account used to give before crews existed.
func TestUnknownTargetsSurfaceAfterACrewIsDeleted(t *testing.T) {
	h := newHarness(t)

	resp := h.upload(map[string]string{
		"name":    "Once Shared",
		"targets": "crew:soloone",
	}, exampleGPX(t), "once-shared.gpx")
	h.expectStatus(resp, http.StatusCreated)

	var created routeDTO
	h.decode(resp, &created)
	if len(created.UnknownTargets) != 0 {
		t.Fatalf("unknownTargets = %v before the crew was touched, want none", created.UnknownTargets)
	}

	// The crew's own owner (rider "local" in this harness) deletes it —
	// nothing about the route changes.
	h.expectStatus(h.do(http.MethodDelete, "/api/crews/crew:soloone", nil, ""), http.StatusOK)

	var library libraryDTO
	h.decode(h.get("/api/routes"), &library)
	if len(library.Routes) != 1 {
		t.Fatal("route missing")
	}
	if len(library.Routes[0].UnknownTargets) != 1 || library.Routes[0].UnknownTargets[0] != "crew:soloone" {
		t.Errorf("unknownTargets = %v, want [crew:soloone] once its crew is gone",
			library.Routes[0].UnknownTargets)
	}

	// And it stops reaching anyone — crew:soloone was the only thing this
	// route named, and it resolved to nothing but garmin:one.
	var plan planDTO
	h.decode(h.get("/api/plan"), &plan)
	for _, item := range plan.Items {
		if item.AccountID == "garmin:one" {
			t.Errorf("still planning for garmin:one after crew:soloone was deleted")
		}
	}
}
