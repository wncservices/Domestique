package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func request(remoteAddr string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/routes", nil)
	r.RemoteAddr = remoteAddr
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func mustNew(t *testing.T, cfg Config) *Authenticator {
	t.Helper()
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

// The headers are only meaningful behind the proxy. In ModeNone they must be
// ignored outright: with auth off you are the local user, and nothing a client
// types can change that.
func TestModeNoneIgnoresHeaders(t *testing.T) {
	a := mustNew(t, Config{Mode: ModeNone})

	id := a.Identify(request("10.0.0.5:1234", map[string]string{
		HeaderUser:   "attacker",
		HeaderGroups: "admins",
	}))

	if id.User == "attacker" {
		t.Fatalf("headers were trusted with auth off: %+v", id)
	}
	if id.User != "local" || id.Role != RoleAdmin {
		t.Errorf("identity = %+v, want the local admin", id)
	}
	if err := a.Authorize(id); err != nil {
		t.Errorf("ModeNone should allow access: %v", err)
	}
}

func TestModeProxyReadsHeaders(t *testing.T) {
	a := mustNew(t, Config{Mode: ModeProxy})

	id := a.Identify(request("10.42.0.9:5555", map[string]string{
		HeaderUser:   "wilant",
		HeaderName:   "Wilant Nackaerts",
		HeaderEmail:  "wilant@example.com",
		HeaderGroups: "cyclists, admins",
	}))

	if id.User != "wilant" || id.Name != "Wilant Nackaerts" {
		t.Errorf("identity = %+v", id)
	}
	if len(id.Groups) != 2 || id.Groups[1] != "admins" {
		t.Errorf("groups = %v, want them split and trimmed", id.Groups)
	}
	if id.DisplayName() != "Wilant Nackaerts" {
		t.Errorf("display name = %q", id.DisplayName())
	}
}

// The core of the scheme: headers from anywhere but the proxy are discarded,
// because that is what a spoofing attempt looks like.
func TestUntrustedPeerHeadersAreDiscarded(t *testing.T) {
	a := mustNew(t, Config{Mode: ModeProxy, TrustedProxies: []string{"10.42.0.0/16"}})

	trusted := a.Identify(request("10.42.1.7:1000", map[string]string{HeaderUser: "wilant"}))
	if trusted.User != "wilant" {
		t.Fatalf("trusted peer was not identified: %+v", trusted)
	}

	for _, peer := range []string{"192.168.1.50:1000", "172.17.0.3:1000", "[2001:db8::1]:1000"} {
		spoofed := a.Identify(request(peer, map[string]string{
			HeaderUser:   "attacker",
			HeaderGroups: "admins",
		}))
		if !spoofed.Anonymous() {
			t.Errorf("peer %s: headers trusted, got %+v — spoofing would work", peer, spoofed)
		}
	}
}

func TestTrustedProxyAcceptsBareIP(t *testing.T) {
	a := mustNew(t, Config{Mode: ModeProxy, TrustedProxies: []string{"10.42.0.9"}})

	if id := a.Identify(request("10.42.0.9:1000", map[string]string{HeaderUser: "wilant"})); id.Anonymous() {
		t.Error("bare IP in trusted_proxies did not match")
	}
	if id := a.Identify(request("10.42.0.10:1000", map[string]string{HeaderUser: "x"})); !id.Anonymous() {
		t.Error("a different IP matched a bare-IP entry")
	}
}

func TestAuthorizeRequiresIdentityInProxyMode(t *testing.T) {
	a := mustNew(t, Config{Mode: ModeProxy})

	if err := a.Authorize(Identity{}); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("anonymous in proxy mode: err = %v, want ErrUnauthenticated", err)
	}
	if err := a.Authorize(Identity{User: "wilant"}); err != nil {
		t.Errorf("identified user rejected: %v", err)
	}
}

func TestRequiredGroupIsEnforced(t *testing.T) {
	a := mustNew(t, Config{Mode: ModeProxy, RequiredGroup: "cyclists"})

	if err := a.Authorize(Identity{User: "stranger", Groups: []string{"others"}}); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-member: err = %v, want ErrForbidden", err)
	}
	// Group matching is case-insensitive; Authelia and LDAP disagree on case.
	if err := a.Authorize(Identity{User: "wilant", Groups: []string{"Cyclists"}}); err != nil {
		t.Errorf("member rejected: %v", err)
	}
}

// A required group with no authentication to enforce it is a silent no-op,
// and reads in config as though it protects something. Refuse it.
func TestRequiredGroupWithoutAuthIsRejected(t *testing.T) {
	if _, err := New(Config{Mode: ModeNone, RequiredGroup: "cyclists"}); err == nil {
		t.Fatal("required_group accepted with mode none")
	}
}

// "oidc" used to be the example of an unknown mode this test reached for —
// it is a real one now, so a genuinely unknown string replaces it. Using
// "oidc" here again would keep passing today (an OIDC mode with no oidc:
// block is rejected too, just for a different reason) and silently stop
// testing what its name says the moment that stops being true.
func TestUnknownModeIsRejected(t *testing.T) {
	if _, err := New(Config{Mode: "saml"}); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestBadTrustedProxyIsRejected(t *testing.T) {
	if _, err := New(Config{Mode: ModeProxy, TrustedProxies: []string{"not-an-ip"}}); err == nil {
		t.Fatal("invalid trusted proxy accepted")
	}
}

func TestEmptyModeDefaultsToNone(t *testing.T) {
	a := mustNew(t, Config{})
	if a.Mode() != ModeNone || a.Enabled() {
		t.Errorf("mode = %q, enabled = %v; want none/false", a.Mode(), a.Enabled())
	}
}

// An explicit role grant gets you in on its own.
//
// required_group is there to stop unmapped accounts falling through to
// default_role. Applying it to somebody the operator named in roles.admin
// meant the app worked out you were an admin and then refused you, which is
// the sort of rule that reads as a bug because it is one.
func TestARoleGroupSatisfiesTheRequiredGroup(t *testing.T) {
	a := mustNew(t, Config{
		Mode:          ModeProxy,
		RequiredGroup: "domestique-users",
		Roles:         RoleMapping{Admin: []string{"domestique-admins"}, Rider: []string{"cyclists"}},
	})

	admin := Identity{User: "wilant", Groups: []string{"domestique-admins"}}
	if err := a.Authorize(admin); err != nil {
		t.Errorf("an admin was refused entry: %v", err)
	}
	rider := Identity{User: "someone", Groups: []string{"cyclists"}}
	if err := a.Authorize(rider); err != nil {
		t.Errorf("a mapped rider was refused entry: %v", err)
	}

	// The gate still does its job: an account with no mapped group and no
	// membership would otherwise arrive as a viewer.
	stranger := Identity{User: "stranger", Groups: []string{"some-other-team"}}
	if err := a.Authorize(stranger); !errors.Is(err, ErrForbidden) {
		t.Errorf("unmapped stranger: err = %v, want ErrForbidden", err)
	}

	// And membership alone is still enough, with no role group at all.
	member := Identity{User: "member", Groups: []string{"domestique-users"}}
	if err := a.Authorize(member); err != nil {
		t.Errorf("a member of the required group was refused: %v", err)
	}
}

// validOIDCConfig is a minimal config that passes validatedOIDC, for tests
// that need mode oidc to construct successfully without caring about the
// specifics.
func validOIDCConfig() Config {
	return Config{
		Mode: ModeOIDC,
		OIDC: OIDCConfig{
			Issuer:      "https://idp.example.test/",
			ClientID:    "domestique",
			RedirectURL: "https://app.example.test/sso/callback",
		},
	}
}

func TestOIDCConfigValidation(t *testing.T) {
	base := func() OIDCConfig { return validOIDCConfig().OIDC }

	for _, tc := range []struct {
		name    string
		mutate  func(OIDCConfig) OIDCConfig
		wantErr bool
	}{
		{"valid config accepted", func(c OIDCConfig) OIDCConfig { return c }, false},
		{"missing issuer", func(c OIDCConfig) OIDCConfig { c.Issuer = ""; return c }, true},
		{"issuer with no scheme", func(c OIDCConfig) OIDCConfig { c.Issuer = "idp.example.test"; return c }, true},
		{"issuer is not a URL at all", func(c OIDCConfig) OIDCConfig { c.Issuer = "://not a url"; return c }, true},
		{"missing client_id", func(c OIDCConfig) OIDCConfig { c.ClientID = ""; return c }, true},
		{"missing redirect_url", func(c OIDCConfig) OIDCConfig { c.RedirectURL = ""; return c }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Config{Mode: ModeOIDC, OIDC: tc.mutate(base())})
			if tc.wantErr && err == nil {
				t.Fatal("bad oidc config accepted")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("valid oidc config rejected: %v", err)
			}
		})
	}
}

// groups_claim defaults to "groups" and "openid" is added to scopes when the
// operator forgot it — both quietly, since the alternative is a confusing
// downstream error from the issuer that looks nothing like "you forgot a
// scope".
func TestOIDCConfigDefaults(t *testing.T) {
	a := mustNew(t, validOIDCConfig())
	got := a.OIDC()

	if got.GroupsClaim != "groups" {
		t.Errorf("GroupsClaim = %q, want the default", got.GroupsClaim)
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != "openid" {
		t.Errorf("Scopes = %v, want [openid] injected", got.Scopes)
	}

	// A groups_claim and scope list the operator did set are respected, not
	// overwritten.
	cfg := validOIDCConfig()
	cfg.OIDC.GroupsClaim = "https://domestique/groups"
	cfg.OIDC.Scopes = []string{"openid", "profile", "email"}
	a2 := mustNew(t, cfg)
	got2 := a2.OIDC()
	if got2.GroupsClaim != "https://domestique/groups" {
		t.Errorf("GroupsClaim = %q, want the operator's own value kept", got2.GroupsClaim)
	}
	if len(got2.Scopes) != 3 {
		t.Errorf("Scopes = %v, want the operator's three kept without a duplicate openid", got2.Scopes)
	}
}

// fakeSessions is a map-backed SessionLookup, for tests that want Identify's
// ModeOIDC branch without a real internal/sessions store.
type fakeSessions map[string]Identity

func (f fakeSessions) Lookup(token string) (Identity, bool) {
	id, ok := f[token]
	return id, ok
}

func TestIdentifyOIDCReadsSessionCookie(t *testing.T) {
	a := mustNew(t, Config{Mode: ModeOIDC, Roles: RoleMapping{Rider: []string{"cyclists"}},
		OIDC: validOIDCConfig().OIDC})
	a.UseSessions(fakeSessions{
		"tok-1": {User: "wilant", Name: "Wilant", Groups: []string{"cyclists"}},
	})

	r := httptest.NewRequest(http.MethodGet, "/api/routes", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-1"})

	id := a.Identify(r)
	if id.User != "wilant" || id.Name != "Wilant" {
		t.Errorf("identity = %+v, want the session's own", id)
	}
	// Role is recomputed from the stored groups, not trusted from storage —
	// same as ModeProxy re-resolving on every request.
	if id.Role != RoleRider {
		t.Errorf("role = %v, want it resolved from Groups via the current role mapping", id.Role)
	}

	// No cookie, or a token nothing recognises: anonymous, not a panic.
	if got := a.Identify(httptest.NewRequest(http.MethodGet, "/api/routes", nil)); !got.Anonymous() {
		t.Errorf("no cookie: identity = %+v, want anonymous", got)
	}
	bad := httptest.NewRequest(http.MethodGet, "/api/routes", nil)
	bad.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "does-not-exist"})
	if got := a.Identify(bad); !got.Anonymous() {
		t.Errorf("unknown token: identity = %+v, want anonymous", got)
	}
}

// A server that has not (yet, or ever) wired a session store must not panic
// on the first ModeOIDC request — nil is a valid state, same rule as
// providerlink/settings' CanStore.
func TestIdentifyOIDCWithoutSessionsIsAnonymous(t *testing.T) {
	a := mustNew(t, validOIDCConfig())

	r := httptest.NewRequest(http.MethodGet, "/api/routes", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok-1"})

	if got := a.Identify(r); !got.Anonymous() {
		t.Errorf("no sessions store: identity = %+v, want anonymous", got)
	}
}

// Authorize takes no mode-specific path for OIDC — it should not need one,
// since it only ever asked "is somebody identified" and "are they in the
// required group", neither of which cares how that identity was resolved.
// These mirror the equivalent ModeProxy tests above.
func TestAuthorizeRequiresIdentityInOIDCMode(t *testing.T) {
	a := mustNew(t, validOIDCConfig())

	if err := a.Authorize(Identity{}); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("anonymous in oidc mode: err = %v, want ErrUnauthenticated", err)
	}
	if err := a.Authorize(Identity{User: "wilant"}); err != nil {
		t.Errorf("identified user rejected: %v", err)
	}
}

func TestRequiredGroupIsEnforcedInOIDCMode(t *testing.T) {
	cfg := validOIDCConfig()
	cfg.RequiredGroup = "cyclists"
	a := mustNew(t, cfg)

	if err := a.Authorize(Identity{User: "stranger", Groups: []string{"others"}}); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-member: err = %v, want ErrForbidden", err)
	}
	if err := a.Authorize(Identity{User: "wilant", Groups: []string{"cyclists"}}); err != nil {
		t.Errorf("member rejected: %v", err)
	}
}
