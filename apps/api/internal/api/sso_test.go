package api_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wncservices/domestique/apps/api/internal/auth"
	"github.com/wncservices/domestique/apps/api/internal/oidcflow"
	"github.com/wncservices/domestique/apps/api/internal/secrets"
	"github.com/wncservices/domestique/apps/api/internal/sessions"
	"github.com/wncservices/domestique/apps/api/internal/source"

	"github.com/wncservices/domestique/apps/api/internal/api"
)

// --- a minimal, genuinely working fake issuer ---
//
// Same shape as internal/oidcflow's own fake, unexported there so it cannot
// be reused across packages: go-oidc verifies signatures for real against
// whatever JWKS its discovery document advertises, so a canned JSON blob
// would not exercise anything.

type fakeIssuer struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	claims map[string]any
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIssuer{key: key, kid: "test-key"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 f.server.URL,
			"authorization_endpoint": f.server.URL + "/authorize",
			"token_endpoint":         f.server.URL + "/token",
			"jwks_uri":               f.server.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{
			map[string]any{
				"kty": "RSA", "kid": f.kid, "use": "sig", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.PublicKey.E)).Bytes()),
			},
		}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		raw, err := signJWT(f.key, f.kid, f.claims)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": raw})
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeIssuer) setClaims(nonce string, groups []string) {
	now := time.Now()
	f.claims = map[string]any{
		"iss": f.server.URL, "sub": "auth0|wilant", "aud": "domestique-test",
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nonce": nonce,
		"preferred_username": "wilant", "groups": groups,
	}
}

func signJWT(key *rsa.PrivateKey, kid string, claims map[string]any) (string, error) {
	h, err := b64JSON(map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid})
	if err != nil {
		return "", err
	}
	p, err := b64JSON(claims)
	if err != nil {
		return "", err
	}
	signingInput := h + "." + p
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func b64JSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// --- acceptance harness: the real HTTP surface, cookies and all ---

type ssoHarness struct {
	t      *testing.T
	client *http.Client
	base   string
	issuer *fakeIssuer
}

func newSSOHarness(t *testing.T) *ssoHarness {
	t.Helper()
	issuer := newFakeIssuer(t)

	key, err := secrets.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(key)
	if err != nil {
		t.Fatal(err)
	}

	db, err := source.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sessionStore, err := sessions.UseDB(db.Conn(), db.DSN(), box)
	if err != nil {
		t.Fatal(err)
	}

	authenticator, err := auth.New(auth.Config{
		Mode:  auth.ModeOIDC,
		Roles: auth.RoleMapping{Admin: []string{"admins"}, Rider: []string{"cyclists"}},
		OIDC: auth.OIDCConfig{
			Issuer: issuer.server.URL, ClientID: "domestique-test",
			RedirectURL: "will be overwritten below",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	authenticator.UseSessions(sessionStore)

	flow, err := oidcflow.New(context.Background(), oidcflow.Config{
		Issuer: issuer.server.URL, ClientID: "domestique-test", ClientSecret: "test-secret",
		Scopes: []string{"openid"},
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := &api.Server{Auth: authenticator, OIDC: flow, Sessions: sessionStore, Box: box}
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)

	// The redirect_uri registered with the (fake) issuer has to be an
	// absolute URL on this harness's own address, decided only once the
	// server is up.
	authenticator2, err := auth.New(auth.Config{
		Mode:  auth.ModeOIDC,
		Roles: auth.RoleMapping{Admin: []string{"admins"}, Rider: []string{"cyclists"}},
		OIDC: auth.OIDCConfig{
			Issuer: issuer.server.URL, ClientID: "domestique-test",
			RedirectURL: server.URL + "/sso/callback",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	authenticator2.UseSessions(sessionStore)
	srv.Auth = authenticator2

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar: jar,
		// Inspect each redirect ourselves rather than following it — the
		// test needs the Location header (to reach the fake issuer, and
		// later to confirm ReturnTo) and the intermediate cookies.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	return &ssoHarness{t: t, client: client, base: server.URL, issuer: issuer}
}

func (h *ssoHarness) get(path string) *http.Response {
	h.t.Helper()
	resp, err := h.client.Get(h.base + path)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (h *ssoHarness) post(path string) *http.Response {
	h.t.Helper()
	resp, err := h.client.Post(h.base+path, "", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// login drives a real /sso/login -> (fake issuer) -> /sso/callback round
// trip using this harness's own cookie jar, exactly as a browser would.
// Returns the callback's response so the caller can assert on where it
// redirected.
func (h *ssoHarness) login(groups []string) *http.Response {
	h.t.Helper()
	loginResp := h.get("/sso/login")
	if loginResp.StatusCode != http.StatusFound {
		h.t.Fatalf("GET /sso/login = %d, want 302", loginResp.StatusCode)
	}
	loc, err := loginResp.Location()
	if err != nil {
		h.t.Fatal(err)
	}
	nonce := loc.Query().Get("nonce")
	state := loc.Query().Get("state")

	h.issuer.setClaims(nonce, groups)
	return h.get("/sso/callback?code=any-code&state=" + state)
}

func meBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSSOLoginRedirectsToTheIssuer(t *testing.T) {
	h := newSSOHarness(t)
	resp := h.get("/sso/login")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc, err := resp.Location()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(loc.String(), h.issuer.server.URL+"/authorize") {
		t.Errorf("redirected to %s, want the issuer's authorize endpoint", loc)
	}
	for _, param := range []string{"state", "nonce", "code_challenge", "client_id"} {
		if loc.Query().Get(param) == "" {
			t.Errorf("authorize URL missing %q: %s", param, loc)
		}
	}
}

// The whole path: login, land back authenticated, /api/me reflects who —
// including a role resolved from the groups the issuer sent, not trusted
// from the token directly.
func TestSSOCallbackSignsTheRiderIn(t *testing.T) {
	h := newSSOHarness(t)
	callback := h.login([]string{"cyclists"})

	if callback.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", callback.StatusCode)
	}
	if loc, _ := callback.Location(); loc == nil || loc.Path != "/" {
		t.Errorf("callback redirected to %v, want the default \"/\"", loc)
	}

	me := meBody(t, h.get("/api/me"))
	if me["authenticated"] != true {
		t.Fatalf("me = %v, want authenticated", me)
	}
	if me["user"] != "wilant" {
		t.Errorf("user = %v", me["user"])
	}
	if me["role"] != "rider" {
		t.Errorf("role = %v, want it resolved from the cyclists group", me["role"])
	}
	if me["authMode"] != "oidc" {
		t.Errorf("authMode = %v", me["authMode"])
	}
}

// return_to carries the rider back to where they started, and only there —
// safeReturnTo is what stands between this and an open redirect.
func TestSSOLoginReturnToIsHonouredAndValidated(t *testing.T) {
	h := newSSOHarness(t)

	good := h.get("/sso/login?return_to=/settings")
	loc, err := good.Location()
	if err != nil {
		t.Fatal(err)
	}
	nonce := loc.Query().Get("nonce")
	h.issuer.setClaims(nonce, nil)
	callback := h.get("/sso/callback?code=x&state=" + loc.Query().Get("state"))
	if got, _ := callback.Location(); got == nil || got.Path != "/settings" {
		t.Errorf("redirected to %v, want /settings", got)
	}

	bad := h.get("/sso/login?return_to=https://evil.example/steal")
	if bad.StatusCode != http.StatusBadRequest {
		t.Errorf("an absolute return_to: status = %d, want 400", bad.StatusCode)
	}
	bad2 := h.get("/sso/login?return_to=//evil.example/steal")
	if bad2.StatusCode != http.StatusBadRequest {
		t.Errorf("a protocol-relative return_to: status = %d, want 400", bad2.StatusCode)
	}
}

func TestSSOCallbackRejectsAMissingStateCookie(t *testing.T) {
	h := newSSOHarness(t)
	// No prior /sso/login on this client: no state cookie exists at all.
	resp := h.get("/sso/callback?code=x&state=anything")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSSOCallbackRejectsAMismatchedState(t *testing.T) {
	h := newSSOHarness(t)
	loginResp := h.get("/sso/login")
	loc, _ := loginResp.Location()
	h.issuer.setClaims(loc.Query().Get("nonce"), nil)

	resp := h.get("/sso/callback?code=x&state=not-the-real-state")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// A token whose nonce does not match what this request actually asked for
// must be rejected even though its signature, issuer, audience and expiry
// are all otherwise fine — this is the one check go-oidc leaves to the
// caller, and it is what stops a token issued for a *different* login
// attempt being replayed into this one.
func TestSSOCallbackRejectsAWrongNonce(t *testing.T) {
	h := newSSOHarness(t)
	loginResp := h.get("/sso/login")
	loc, _ := loginResp.Location()
	state := loc.Query().Get("state")

	h.issuer.setClaims("a-nonce-nobody-asked-for", nil)
	resp := h.get("/sso/callback?code=x&state=" + state)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestSSOLogoutEndsTheSessionForReal(t *testing.T) {
	h := newSSOHarness(t)
	h.login([]string{"cyclists"})

	if me := meBody(t, h.get("/api/me")); me["authenticated"] != true {
		t.Fatal("not signed in after login — test setup is broken")
	}

	logoutResp := h.post("/sso/logout")
	if logoutResp.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", logoutResp.StatusCode)
	}
	var body struct {
		RedirectTo string `json:"redirectTo"`
	}
	if err := json.NewDecoder(logoutResp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	me := meBody(t, h.get("/api/me"))
	if me["authenticated"] != false {
		t.Errorf("me = %v after logout, want anonymous", me)
	}
}

func TestSSOEndpointsAreNotFoundOutsideOIDCMode(t *testing.T) {
	authenticator, err := auth.New(auth.Config{Mode: auth.ModeNone})
	if err != nil {
		t.Fatal(err)
	}
	srv := &api.Server{Auth: authenticator}
	server := httptest.NewServer(srv.Handler())
	defer server.Close()

	for _, path := range []string{"/sso/login", "/sso/callback", "/sso/logout"} {
		method := http.MethodGet
		if path == "/sso/logout" {
			method = http.MethodPost
		}
		req, _ := http.NewRequest(method, server.URL+path, nil)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s in mode none: status = %d, want 404", path, resp.StatusCode)
		}
	}
}

// /api/me being reachable while anonymous is new behavior this PR
// introduces (see the comment on authenticate in server.go) — it was
// entirely unexercised before, since mode: proxy never let an anonymous
// request reach this Go server at all. Direct coverage now, across every
// mode, so a future change cannot quietly re-gate it without a test noticing.
func TestMeIsReachableAnonymouslyInEveryMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  auth.Config
	}{
		{"none", auth.Config{Mode: auth.ModeNone}},
		{"proxy", auth.Config{Mode: auth.ModeProxy}},
		{"oidc", validOIDCAuthConfig(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authenticator, err := auth.New(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			srv := &api.Server{Auth: authenticator}
			server := httptest.NewServer(srv.Handler())
			defer server.Close()

			resp, err := http.Get(server.URL + "/api/me")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 even anonymously", resp.StatusCode)
			}
			me := meBody(t, resp)

			// None of these three requests ever presents a session cookie,
			// so "authenticated" must be false in every one of them —
			// including mode none, where the local-admin identity is not a
			// real login and the UI's "no login required" badge depends on
			// this staying false.
			if me["authenticated"] != false {
				t.Errorf("authenticated = %v, want false (nobody signed in)", me["authenticated"])
			}
		})
	}
}

// The fix for /api/me must not have widened to every route — an anonymous
// visitor to anything else under /api/ in mode oidc is still refused.
func TestOtherRoutesStayGatedWhenMeDoesNot(t *testing.T) {
	authenticator, err := auth.New(validOIDCAuthConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	srv := &api.Server{Auth: authenticator}
	server := httptest.NewServer(srv.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/routes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api/routes anonymously: status = %d, want 401", resp.StatusCode)
	}
}

func validOIDCAuthConfig(t *testing.T) auth.Config {
	t.Helper()
	return auth.Config{
		Mode: auth.ModeOIDC,
		OIDC: auth.OIDCConfig{
			Issuer: "https://idp.example.test/", ClientID: "x", RedirectURL: "https://app.example.test/sso/callback",
		},
	}
}
