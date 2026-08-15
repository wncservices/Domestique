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
	"github.com/wncservices/domestique/apps/api/internal/auth0mgmt"
	"github.com/wncservices/domestique/apps/api/internal/source"
	"github.com/wncservices/domestique/apps/api/internal/state"
)

// fakePeople is PeopleConnector for tests — no real Auth0 tenant, matching
// every other connector fake in this package.
type fakePeople struct {
	people []auth0mgmt.Person

	invited      []auth0mgmt.Person
	invitedRoles map[string][]string // by email
	invitedEmail []string            // SendInviteEmail's own calls
	setRoles     map[string][]string // by user id, last call wins

	listErr   error
	inviteErr error
	emailErr  error
	rolesErr  error
}

func (f *fakePeople) ListPeople(string, ...string) ([]auth0mgmt.Person, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.people, nil
}

func (f *fakePeople) Invite(email, name string, roleNames []string) (auth0mgmt.Person, error) {
	if f.inviteErr != nil {
		return auth0mgmt.Person{}, f.inviteErr
	}
	if f.invitedRoles == nil {
		f.invitedRoles = map[string][]string{}
	}
	person := auth0mgmt.Person{UserID: "auth0|new-" + email, Email: email, Name: name}
	f.invited = append(f.invited, person)
	f.invitedRoles[email] = roleNames
	return person, nil
}

func (f *fakePeople) SetRoles(userID string, roleNames []string) error {
	if f.rolesErr != nil {
		return f.rolesErr
	}
	if f.setRoles == nil {
		f.setRoles = map[string][]string{}
	}
	f.setRoles[userID] = roleNames
	return nil
}

func (f *fakePeople) SendInviteEmail(email string) error {
	if f.emailErr != nil {
		return f.emailErr
	}
	f.invitedEmail = append(f.invitedEmail, email)
	return nil
}

type peopleHarness struct {
	t      *testing.T
	client *http.Client
	base   string
	people *fakePeople
}

func newPeopleHarness(t *testing.T, people *fakePeople) *peopleHarness {
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
		Mode:          auth.ModeProxy,
		RequiredGroup: "domestique-users",
		Roles: auth.RoleMapping{
			Admin: []string{"domestique-admins"},
			Rider: []string{"cyclists"},
		},
		DefaultRole: "viewer",
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := &api.Server{Source: db, Store: store, Auth: authenticator}
	// Assigned only when non-nil: srv.People is the PeopleConnector
	// interface, and assigning a nil *fakePeople to it would make the
	// interface itself non-nil (a typed nil), defeating the "no connector
	// configured" tests below.
	if people != nil {
		srv.People = people
	}
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)

	return &peopleHarness{t: t, client: server.Client(), base: server.URL, people: people}
}

func (h *peopleHarness) as(user, groups, method, path, body string) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(method, h.base+path, strings.NewReader(body))
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

func TestPeopleListRequiresAdmin(t *testing.T) {
	h := newPeopleHarness(t, &fakePeople{})

	resp := h.as("wilant", "domestique-users,cyclists", http.MethodGet, "/api/people", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("rider status = %d, want 403", resp.StatusCode)
	}

	resp = h.as("wilant", "domestique-users,domestique-admins", http.MethodGet, "/api/people", "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("admin status = %d, want 200", resp.StatusCode)
	}
}

func TestPeopleListResolvesRoleFromAuth0RoleNames(t *testing.T) {
	fake := &fakePeople{people: []auth0mgmt.Person{
		{UserID: "u1", Email: "admin@example.com", Roles: []string{"domestique-users", "domestique-admins"}},
		{UserID: "u2", Email: "rider@example.com", Roles: []string{"domestique-users", "cyclists"}},
		{UserID: "u3", Email: "gateonly@example.com", Roles: []string{"domestique-users"}},
	}}
	h := newPeopleHarness(t, fake)

	resp := h.as("wilant", "domestique-users,domestique-admins", http.MethodGet, "/api/people", "")
	var out []struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	byEmail := map[string]string{}
	for _, p := range out {
		byEmail[p.Email] = p.Role
	}
	if byEmail["admin@example.com"] != "admin" {
		t.Errorf("admin's role = %q", byEmail["admin@example.com"])
	}
	if byEmail["rider@example.com"] != "rider" {
		t.Errorf("rider's role = %q", byEmail["rider@example.com"])
	}
	// Gate-only membership resolves to the configured default_role, exactly
	// what Identify itself would compute at sign-in — not a special case
	// this page invents on its own.
	if byEmail["gateonly@example.com"] != "viewer" {
		t.Errorf("gate-only's role = %q, want viewer", byEmail["gateonly@example.com"])
	}
}

func TestPeopleInviteGrantsGateAndPermissionRoleThenSendsEmail(t *testing.T) {
	fake := &fakePeople{}
	h := newPeopleHarness(t, fake)

	resp := h.as("wilant", "domestique-users,domestique-admins", http.MethodPost, "/api/people",
		`{"email":"New@Example.com","name":"New Rider","role":"rider"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	// Lower-cased and trimmed the same way every other rider-facing email
	// in this codebase is normalized.
	roles := fake.invitedRoles["new@example.com"]
	if len(roles) != 2 || roles[0] != "domestique-users" || roles[1] != "cyclists" {
		t.Errorf("invited roles = %v, want [domestique-users cyclists]", roles)
	}
	if len(fake.invitedEmail) != 1 || fake.invitedEmail[0] != "new@example.com" {
		t.Errorf("invite email sent to %v, want [new@example.com]", fake.invitedEmail)
	}
}

// Inviting as "viewer" grants the gate role and nothing else — there is no
// Auth0 role for "viewer" in this deployment, it is the absence of the
// other two.
func TestPeopleInviteAsViewerGrantsOnlyTheGateRole(t *testing.T) {
	fake := &fakePeople{}
	h := newPeopleHarness(t, fake)

	resp := h.as("wilant", "domestique-users,domestique-admins", http.MethodPost, "/api/people",
		`{"email":"viewer@example.com","role":"viewer"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	roles := fake.invitedRoles["viewer@example.com"]
	if len(roles) != 1 || roles[0] != "domestique-users" {
		t.Errorf("invited roles = %v, want [domestique-users]", roles)
	}
}

func TestPeopleInviteRejectsAnUnknownRole(t *testing.T) {
	h := newPeopleHarness(t, &fakePeople{})

	resp := h.as("wilant", "domestique-users,domestique-admins", http.MethodPost, "/api/people",
		`{"email":"x@example.com","role":"superuser"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// The account and its access already exist by the time the email step can
// fail — that must not look like the whole invite failed, or a retry would
// try (and fail) to create the same account again.
func TestPeopleInviteSurvivesAnEmailFailureAfterCreating(t *testing.T) {
	fake := &fakePeople{emailErr: assertErr}
	h := newPeopleHarness(t, fake)

	resp := h.as("wilant", "domestique-users,domestique-admins", http.MethodPost, "/api/people",
		`{"email":"x@example.com","role":"rider"}`)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (created, email failed)", resp.StatusCode)
	}
	if len(fake.invited) != 1 {
		t.Errorf("invited = %v, want the account still created", fake.invited)
	}
}

func TestPeopleSetRoleRequiresAdmin(t *testing.T) {
	h := newPeopleHarness(t, &fakePeople{})

	resp := h.as("wilant", "domestique-users,cyclists", http.MethodPut, "/api/people/u1/role", `{"role":"admin"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestPeopleSetRoleChangesRoles(t *testing.T) {
	fake := &fakePeople{}
	h := newPeopleHarness(t, fake)

	resp := h.as("wilant", "domestique-users,domestique-admins", http.MethodPut, "/api/people/u1/role", `{"role":"admin"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	roles := fake.setRoles["u1"]
	if len(roles) != 2 || roles[1] != "domestique-admins" {
		t.Errorf("set roles = %v, want [domestique-users domestique-admins]", roles)
	}
}

func TestPeopleEndpointsWithoutAConnectorAreUnavailable(t *testing.T) {
	h := newPeopleHarness(t, nil)

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/people", ""},
		{http.MethodPost, "/api/people", `{"email":"x@example.com","role":"rider"}`},
		{http.MethodPut, "/api/people/u1/role", `{"role":"admin"}`},
	} {
		resp := h.as("wilant", "domestique-users,domestique-admins", tc.method, tc.path, tc.body)
		if resp.StatusCode != http.StatusPreconditionFailed {
			t.Errorf("%s %s status = %d, want 412", tc.method, tc.path, resp.StatusCode)
		}
	}
}

// assertErr is a stand-in error for tests that only care that Invite's
// email step failed, not what the failure was.
var assertErr = &testError{"auth0mgmt: sending the invite email failed"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
