package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/api"
)

// patch is ssoHarness's own client/base/cookies, plus a body — the sso
// harness only ever needed GET and a bodyless POST until now.
func (h *ssoHarness) patch(path, body string) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPatch, h.base+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func jsonBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMeReportsWhetherTheProfileCardHasAnythingToOffer(t *testing.T) {
	fake := &fakePeople{}
	h := newSSOHarness(t, func(s *api.Server) { s.People = fake })
	h.loginWithUser([]string{"cyclists"}, map[string]any{
		"preferred_username": "wilant", "email": "wilant@example.com",
	})

	me := jsonBody(t, h.get("/api/me"))
	if me["canEditName"] != true {
		t.Errorf("canEditName = %v, want true", me["canEditName"])
	}
	if me["canChangePassword"] != true {
		t.Errorf("canChangePassword = %v, want true (a database-connection sub)", me["canChangePassword"])
	}
}

// A People-less deployment (no Management API credentials) has nothing this
// card can act on — same "not available" degradation Komoot/Garmin use for
// their own optional credentials, not a 500.
func TestMeReportsNoProfileEditingWithoutPeopleConfigured(t *testing.T) {
	h := newSSOHarness(t)
	h.login([]string{"cyclists"})

	me := jsonBody(t, h.get("/api/me"))
	if me["canEditName"] != false || me["canChangePassword"] != false {
		t.Errorf("me = %v, want both false with no People connector", me)
	}
}

// A Google-linked identity has no password on this app's side — the sub
// prefix says so before any Management API call is even attempted.
func TestMeReportsNoPasswordChangeForAGoogleIdentity(t *testing.T) {
	fake := &fakePeople{}
	h := newSSOHarness(t, func(s *api.Server) { s.People = fake })
	h.loginWithUser([]string{"cyclists"}, map[string]any{
		"preferred_username": "wilant", "email": "wilant@example.com", "sub": "google-oauth2|123",
	})

	me := jsonBody(t, h.get("/api/me"))
	if me["canEditName"] != true {
		t.Errorf("canEditName = %v, want true (name is provider-agnostic)", me["canEditName"])
	}
	if me["canChangePassword"] != false {
		t.Errorf("canChangePassword = %v, want false for a google-oauth2 identity", me["canChangePassword"])
	}
}

func TestUpdateMeChangesNameAndTheCurrentSession(t *testing.T) {
	fake := &fakePeople{}
	h := newSSOHarness(t, func(s *api.Server) { s.People = fake })
	h.loginWithUser([]string{"cyclists"}, map[string]any{
		"preferred_username": "wilant", "email": "wilant@example.com",
	})

	resp := h.patch("/api/me", `{"name":"Wilant N."}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := jsonBody(t, resp)["name"]; got != "Wilant N." {
		t.Errorf("response name = %v", got)
	}
	if got := fake.updatedName["auth0|64f2a1b2c3d4e5f6"]; got != "Wilant N." {
		t.Errorf("auth0mgmt.UpdateName called with %q, want the new name", got)
	}

	// The session was patched in place — a fresh /api/me on the same
	// cookie reflects the new name without a re-login.
	me := jsonBody(t, h.get("/api/me"))
	if me["name"] != "Wilant N." {
		t.Errorf("me.name after update = %v, want it to have picked up the change", me["name"])
	}
}

func TestUpdateMeRejectsAnEmptyName(t *testing.T) {
	fake := &fakePeople{}
	h := newSSOHarness(t, func(s *api.Server) { s.People = fake })
	h.loginWithUser([]string{"cyclists"}, map[string]any{
		"preferred_username": "wilant", "email": "wilant@example.com",
	})

	resp := h.patch("/api/me", `{"name":"   "}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if len(fake.updatedName) != 0 {
		t.Errorf("UpdateName should not have been called: %v", fake.updatedName)
	}
}

func TestUpdateMeFailsClosedWithoutPeopleConfigured(t *testing.T) {
	h := newSSOHarness(t)
	h.login([]string{"cyclists"})

	resp := h.patch("/api/me", `{"name":"New Name"}`)
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("status = %d, want 412", resp.StatusCode)
	}
}

func TestSelfPasswordResetSendsAuth0sResetEmail(t *testing.T) {
	fake := &fakePeople{}
	h := newSSOHarness(t, func(s *api.Server) { s.People = fake })
	h.loginWithUser([]string{"cyclists"}, map[string]any{
		"preferred_username": "wilant", "email": "wilant@example.com",
	})

	resp := h.post("/api/me/password-reset")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(fake.invitedEmail) != 1 || fake.invitedEmail[0] != "wilant@example.com" {
		t.Errorf("SendInviteEmail calls = %v, want [wilant@example.com]", fake.invitedEmail)
	}
}

// The whole point of gating on the sub prefix: a Google-linked rider must
// never reach SendInviteEmail — Auth0 would happily "reset" a password that
// rider never had, and sending that email would only confuse them.
func TestSelfPasswordResetRefusesAGoogleIdentity(t *testing.T) {
	fake := &fakePeople{}
	h := newSSOHarness(t, func(s *api.Server) { s.People = fake })
	h.loginWithUser([]string{"cyclists"}, map[string]any{
		"preferred_username": "wilant", "email": "wilant@example.com", "sub": "google-oauth2|123",
	})

	resp := h.post("/api/me/password-reset")
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("status = %d, want 412", resp.StatusCode)
	}
	if len(fake.invitedEmail) != 0 {
		t.Errorf("SendInviteEmail should not have been called: %v", fake.invitedEmail)
	}
}

func TestSelfPasswordResetRequiresAnAuthenticatedRider(t *testing.T) {
	h := newSSOHarness(t)
	resp := h.post("/api/me/password-reset")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
