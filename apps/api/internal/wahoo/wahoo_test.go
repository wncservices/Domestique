package wahoo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	c := New(Config{ClientID: "client-id", ClientSecret: "client-secret", RedirectURL: "https://app.example.test/wahoo/callback"})
	c.APIBase = server.URL
	return c
}

func TestAuthCodeURL(t *testing.T) {
	c := New(Config{ClientID: "abc", ClientSecret: "shh", RedirectURL: "https://app.example.test/wahoo/callback"})
	c.APIBase = "https://api.wahooligan.com"

	got, err := url.Parse(c.AuthCodeURL("the-state"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Scheme+"://"+got.Host+got.Path != "https://api.wahooligan.com/oauth/authorize" {
		t.Fatalf("unexpected endpoint: %s", got)
	}
	q := got.Query()
	if q.Get("client_id") != "abc" {
		t.Fatalf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "https://app.example.test/wahoo/callback" {
		t.Fatalf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if q.Get("response_type") != "code" {
		t.Fatalf("response_type = %q", q.Get("response_type"))
	}
	if q.Get("state") != "the-state" {
		t.Fatalf("state = %q", q.Get("state"))
	}
	if q.Get("scope") != "user_read routes_read routes_write" {
		t.Fatalf("scope = %q", q.Get("scope"))
	}
	if q.Get("code_challenge") != "" || q.Get("code_challenge_method") != "" {
		t.Fatalf("PKCE params present on a confidential-client request: %s", got.RawQuery)
	}
}

func TestExchange(t *testing.T) {
	var gotForm url.Values
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		gotForm = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "refresh_token": "rt", "expires_in": 3600, "scope": "user_read",
		})
	})

	session, err := c.Exchange(t.Context(), "the-code")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if session.AccessToken != "at" || session.RefreshToken != "rt" {
		t.Fatalf("session = %+v", session)
	}
	if session.Expired() {
		t.Fatalf("a freshly issued token reports expired")
	}
	if gotForm.Get("grant_type") != "authorization_code" {
		t.Fatalf("grant_type = %q", gotForm.Get("grant_type"))
	}
	if gotForm.Get("code") != "the-code" {
		t.Fatalf("code = %q", gotForm.Get("code"))
	}
	if gotForm.Get("client_secret") != "client-secret" {
		t.Fatalf("client_secret = %q", gotForm.Get("client_secret"))
	}
	if gotForm.Get("code_verifier") != "" {
		t.Fatalf("code_verifier present on a confidential-client exchange")
	}
}

func TestRefreshKeepsExistingTokenWhenNoneIsReturned(t *testing.T) {
	var gotGrantType string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotGrantType = r.PostForm.Get("grant_type")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-at", "expires_in": 3600})
	})

	session, err := c.Refresh(t.Context(), "old-rt")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if gotGrantType != "refresh_token" {
		t.Fatalf("grant_type = %q", gotGrantType)
	}
	if session.RefreshToken != "old-rt" {
		t.Fatalf("refresh token = %q, want the original kept", session.RefreshToken)
	}
}

func TestExchangeSurfacesAnUpstreamError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "invalid_grant", "error_description": "code expired",
		})
	})

	_, err := c.Exchange(t.Context(), "stale-code")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("error = %v, want it to name invalid_grant", err)
	}
}

func TestExchangeRejectsAnUnreadableResponse(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not json</html>"))
	})

	if _, err := c.Exchange(t.Context(), "code"); err == nil {
		t.Fatal("expected an error for an unreadable token response")
	}
}

func TestMe(t *testing.T) {
	var gotAuth string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/user" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(Profile{ID: 42, Email: "rider@example.test", First: "Ada", Last: "Lovelace"})
	})

	profile, err := c.Me(t.Context(), "at")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if gotAuth != "Bearer at" {
		t.Fatalf("Authorization header = %q", gotAuth)
	}
	if profile.DisplayName() != "Ada Lovelace" {
		t.Fatalf("display name = %q", profile.DisplayName())
	}
}

func TestProfileDisplayNameFallsBackToEmail(t *testing.T) {
	p := Profile{Email: "rider@example.test"}
	if p.DisplayName() != "rider@example.test" {
		t.Fatalf("display name = %q, want the email", p.DisplayName())
	}
}

func TestMeSurfacesAnUpstreamFailure(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"missing user_read scope"}`))
	})

	if _, err := c.Me(t.Context(), "at"); err == nil {
		t.Fatal("expected an error")
	}
}
