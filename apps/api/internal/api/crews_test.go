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
	"github.com/wncservices/domestique/apps/api/internal/crew"
	"github.com/wncservices/domestique/apps/api/internal/source"
)

type crewHarness struct {
	t      *testing.T
	client *http.Client
	base   string
}

func newCrewHarness(t *testing.T) *crewHarness {
	t.Helper()

	db, err := source.OpenDB(filepath.Join(t.TempDir(), "routes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	crewStore, err := crew.UseDB(db.Conn(), db.DSN())
	if err != nil {
		t.Fatal(err)
	}

	authenticator, err := auth.New(auth.Config{
		Mode:  auth.ModeProxy,
		Roles: auth.RoleMapping{Admin: []string{"admins"}, Rider: []string{"cyclists"}, Viewer: []string{"guests"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := &api.Server{Auth: authenticator, Crew: crewStore}
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)

	return &crewHarness{t: t, client: server.Client(), base: server.URL}
}

func (h *crewHarness) as(user, groups, method, path, body string) *http.Response {
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

type crewDTOOut struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Owner            string `json:"owner"`
	Mine             bool   `json:"mine"`
	MembershipStatus string `json:"membershipStatus"`
	MemberCount      int    `json:"memberCount"`
	AutoShare        bool   `json:"autoShare"`
	Members          []struct {
		Rider  string `json:"rider"`
		Status string `json:"status"`
	} `json:"members"`
}

func decodeCrew(t *testing.T, resp *http.Response) crewDTOOut {
	t.Helper()
	var out crewDTOOut
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCreateCrewEnrollsTheOwner(t *testing.T) {
	h := newCrewHarness(t)

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/crews", `{"name":"Sunday Club"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	c := decodeCrew(t, resp)
	if c.ID != "crew:sunday-club" {
		t.Errorf("id = %q", c.ID)
	}
	if c.Owner != "wilant" || !c.Mine {
		t.Errorf("owner = %q, mine = %v", c.Owner, c.Mine)
	}
	if c.MembershipStatus != "approved" || c.MemberCount != 1 {
		t.Errorf("membershipStatus = %q, memberCount = %d, want approved/1", c.MembershipStatus, c.MemberCount)
	}
	if len(c.Members) != 1 || c.Members[0].Rider != "wilant" {
		t.Errorf("members = %v", c.Members)
	}
}

func TestListCrewsReportsEachViewersOwnStatus(t *testing.T) {
	h := newCrewHarness(t)
	h.as("wilant", "cyclists", http.MethodPost, "/api/crews", `{"name":"Family"}`)

	// A rider who hasn't touched the crew sees "none", no members leaked.
	resp := h.as("other", "cyclists", http.MethodGet, "/api/crews", "")
	var list []crewDTOOut
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d crews, want 1", len(list))
	}
	if list[0].MembershipStatus != "none" || list[0].Mine {
		t.Errorf("membershipStatus = %q, mine = %v, want none/false", list[0].MembershipStatus, list[0].Mine)
	}
	if list[0].Members != nil {
		t.Error("members leaked to a non-owner")
	}
}

func TestJoinApproveRemoveFlow(t *testing.T) {
	h := newCrewHarness(t)
	created := decodeCrew(t, h.as("wilant", "cyclists", http.MethodPost, "/api/crews", `{"name":"Family"}`))

	joinResp := h.as("other", "cyclists", http.MethodPost, "/api/crews/"+created.ID+"/join", "")
	if joinResp.StatusCode != http.StatusOK {
		t.Fatalf("join status = %d, want 200", joinResp.StatusCode)
	}
	joined := decodeCrew(t, joinResp)
	if joined.MembershipStatus != "pending" {
		t.Fatalf("membershipStatus = %q, want pending", joined.MembershipStatus)
	}

	// Joining twice is a conflict, not a second row.
	if resp := h.as("other", "cyclists", http.MethodPost, "/api/crews/"+created.ID+"/join", ""); resp.StatusCode != http.StatusConflict {
		t.Errorf("second join status = %d, want 409", resp.StatusCode)
	}

	// A non-owner cannot approve.
	if resp := h.as("someone-else", "cyclists", http.MethodPut, "/api/crews/"+created.ID+"/members/other", ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-owner approve status = %d, want 403", resp.StatusCode)
	}

	approveResp := h.as("wilant", "cyclists", http.MethodPut, "/api/crews/"+created.ID+"/members/other", "")
	if approveResp.StatusCode != http.StatusOK {
		t.Fatalf("approve status = %d, want 200", approveResp.StatusCode)
	}
	approved := decodeCrew(t, approveResp)
	if approved.MemberCount != 2 {
		t.Errorf("memberCount = %d, want 2", approved.MemberCount)
	}

	// The member can leave on their own — no ownership check needed for self.
	if resp := h.as("other", "cyclists", http.MethodDelete, "/api/crews/"+created.ID+"/members/other", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("self-leave status = %d, want 200", resp.StatusCode)
	}

	var list []crewDTOOut
	listResp := h.as("wilant", "cyclists", http.MethodGet, "/api/crews", "")
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if list[0].MemberCount != 1 {
		t.Errorf("memberCount after leaving = %d, want 1", list[0].MemberCount)
	}
}

// A rider removing someone else's membership needs to be the crew's owner
// or an admin — the same ownership rule accounts and routes already keep.
// The owner's other route into a crew: adding someone directly, without
// that rider ever having requested to join.
func TestOwnerCanAddMemberDirectly(t *testing.T) {
	h := newCrewHarness(t)
	created := decodeCrew(t, h.as("wilant", "cyclists", http.MethodPost, "/api/crews", `{"name":"Family"}`))

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/crews/"+created.ID+"/members", `{"rider":"other"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add status = %d, want 200", resp.StatusCode)
	}
	added := decodeCrew(t, resp)
	if added.MemberCount != 2 {
		t.Errorf("memberCount = %d, want 2", added.MemberCount)
	}

	// The added rider sees themselves as approved immediately — no join,
	// no wait.
	list := h.as("other", "cyclists", http.MethodGet, "/api/crews", "")
	var out []crewDTOOut
	if err := json.NewDecoder(list.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out[0].MembershipStatus != "approved" {
		t.Errorf("membershipStatus = %q, want approved", out[0].MembershipStatus)
	}
}

func TestOnlyOwnerOrAdminCanAddMember(t *testing.T) {
	h := newCrewHarness(t)
	created := decodeCrew(t, h.as("wilant", "cyclists", http.MethodPost, "/api/crews", `{"name":"Family"}`))

	resp := h.as("someone-else", "cyclists", http.MethodPost, "/api/crews/"+created.ID+"/members", `{"rider":"other"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-owner add status = %d, want 403", resp.StatusCode)
	}

	adminResp := h.as("boss", "admins", http.MethodPost, "/api/crews/"+created.ID+"/members", `{"rider":"other"}`)
	if adminResp.StatusCode != http.StatusOK {
		t.Fatalf("admin add status = %d, want 200", adminResp.StatusCode)
	}
}

func TestAddingAnAlreadyApprovedMemberIsAConflict(t *testing.T) {
	h := newCrewHarness(t)
	created := decodeCrew(t, h.as("wilant", "cyclists", http.MethodPost, "/api/crews", `{"name":"Family"}`))
	h.as("wilant", "cyclists", http.MethodPost, "/api/crews/"+created.ID+"/members", `{"rider":"other"}`)

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/crews/"+created.ID+"/members", `{"rider":"other"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

// Adding a rider who already has a pending request approves it in one
// step, instead of forcing the owner to deny and re-add.
func TestAddingAPendingRiderApprovesThem(t *testing.T) {
	h := newCrewHarness(t)
	created := decodeCrew(t, h.as("wilant", "cyclists", http.MethodPost, "/api/crews", `{"name":"Family"}`))
	h.as("other", "cyclists", http.MethodPost, "/api/crews/"+created.ID+"/join", "")

	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/crews/"+created.ID+"/members", `{"rider":"other"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add status = %d, want 200", resp.StatusCode)
	}
	added := decodeCrew(t, resp)
	if added.MemberCount != 2 {
		t.Errorf("memberCount = %d, want 2", added.MemberCount)
	}
}

func TestAddMemberToNonexistentCrewIs404(t *testing.T) {
	h := newCrewHarness(t)
	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/crews/crew:does-not-exist/members", `{"rider":"other"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestNonOwnerCannotRemoveSomeoneElse(t *testing.T) {
	h := newCrewHarness(t)
	created := decodeCrew(t, h.as("wilant", "cyclists", http.MethodPost, "/api/crews", `{"name":"Family"}`))
	h.as("other", "cyclists", http.MethodPost, "/api/crews/"+created.ID+"/join", "")
	h.as("wilant", "cyclists", http.MethodPut, "/api/crews/"+created.ID+"/members/other", "")

	resp := h.as("random", "cyclists", http.MethodDelete, "/api/crews/"+created.ID+"/members/other", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	// An admin may, though.
	adminResp := h.as("boss", "admins", http.MethodDelete, "/api/crews/"+created.ID+"/members/other", "")
	if adminResp.StatusCode != http.StatusOK {
		t.Fatalf("admin remove status = %d, want 200", adminResp.StatusCode)
	}
}

func TestDeleteCrewIsOwnerOrAdminOnly(t *testing.T) {
	h := newCrewHarness(t)
	created := decodeCrew(t, h.as("wilant", "cyclists", http.MethodPost, "/api/crews", `{"name":"Family"}`))

	if resp := h.as("other", "cyclists", http.MethodDelete, "/api/crews/"+created.ID, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-owner delete status = %d, want 403", resp.StatusCode)
	}

	resp := h.as("wilant", "cyclists", http.MethodDelete, "/api/crews/"+created.ID, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner delete status = %d, want 200", resp.StatusCode)
	}

	if resp := h.as("wilant", "cyclists", http.MethodDelete, "/api/crews/"+created.ID, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleting again status = %d, want 404", resp.StatusCode)
	}
}

func TestOnlyOwnerOrAdminCanSetAutoShare(t *testing.T) {
	h := newCrewHarness(t)
	created := decodeCrew(t, h.as("wilant", "cyclists", http.MethodPost, "/api/crews", `{"name":"Family"}`))
	if created.AutoShare {
		t.Fatalf("autoShare = true on create, want false")
	}

	if resp := h.as("other", "cyclists", http.MethodPatch, "/api/crews/"+created.ID, `{"autoShare":true}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-owner status = %d, want 403", resp.StatusCode)
	}

	resp := h.as("wilant", "cyclists", http.MethodPatch, "/api/crews/"+created.ID, `{"autoShare":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner status = %d, want 200", resp.StatusCode)
	}
	if updated := decodeCrew(t, resp); !updated.AutoShare {
		t.Errorf("autoShare = false after enabling, want true")
	}

	// An admin may too, and can turn it back off.
	adminResp := h.as("boss", "admins", http.MethodPatch, "/api/crews/"+created.ID, `{"autoShare":false}`)
	if adminResp.StatusCode != http.StatusOK {
		t.Fatalf("admin status = %d, want 200", adminResp.StatusCode)
	}
	if updated := decodeCrew(t, adminResp); updated.AutoShare {
		t.Errorf("autoShare = true after admin disabled it, want false")
	}
}

func TestSetAutoShareOnNonexistentCrewIs404(t *testing.T) {
	h := newCrewHarness(t)
	resp := h.as("wilant", "cyclists", http.MethodPatch, "/api/crews/crew:does-not-exist", `{"autoShare":true}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestJoinNonexistentCrewIs404(t *testing.T) {
	h := newCrewHarness(t)
	resp := h.as("wilant", "cyclists", http.MethodPost, "/api/crews/crew:does-not-exist/join", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// A viewer may look, not touch — crews are a rider-level feature.
func TestCrewEndpointsNeedRiderPermission(t *testing.T) {
	h := newCrewHarness(t)
	created := decodeCrew(t, h.as("wilant", "cyclists", http.MethodPost, "/api/crews", `{"name":"Family"}`))

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/crews", `{"name":"x"}`},
		{http.MethodGet, "/api/crews", ""},
		{http.MethodPost, "/api/crews/" + created.ID + "/join", ""},
		{http.MethodDelete, "/api/crews/" + created.ID, ""},
		{http.MethodPatch, "/api/crews/" + created.ID, `{"autoShare":true}`},
		{http.MethodPost, "/api/crews/" + created.ID + "/members", `{"rider":"other"}`},
		{http.MethodPut, "/api/crews/" + created.ID + "/members/wilant", ""},
	} {
		resp := h.as("guest", "guests", tc.method, tc.path, tc.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403", tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestCrewHandlersSurviveNoCrewStore(t *testing.T) {
	srv := &api.Server{Auth: noAuth(t)}
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)

	resp, err := server.Client().Get(server.URL + "/api/crews")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412", resp.StatusCode)
	}
}
