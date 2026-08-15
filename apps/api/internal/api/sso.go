package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/wncservices/domestique/apps/api/internal/accounts"
	"github.com/wncservices/domestique/apps/api/internal/auth"
	"github.com/wncservices/domestique/apps/api/internal/oidcflow"
	"github.com/wncservices/domestique/apps/api/internal/secrets"
)

// sessionTTL is fixed rather than configurable. There is no refresh-token
// handling in this design — the RP session is a server-side cookie with its
// own lifetime, independent of the ID token's — so the TTL is purely "how
// long before a rider clicks through the issuer again", not a security
// boundary. 30 days matches this app's general convenience-over-paranoia
// posture elsewhere (ModeNone is the local admin by default, default_role is
// a permissive fallback). A configurable session_ttl is a reasonable later
// addition; not worth designing speculatively now.
const sessionTTL = 30 * 24 * time.Hour

// oidcState is what /sso/login seals into auth.OIDCStateCookie and
// /sso/callback opens: everything needed to complete one specific
// authorization request and nothing that would still matter if it were
// replayed a second time (Create's session token is separate).
type oidcState struct {
	State    string    `json:"state"`
	Nonce    string    `json:"nonce"`
	Verifier string    `json:"verifier"`
	ReturnTo string    `json:"returnTo,omitempty"`
	IssuedAt time.Time `json:"issuedAt"`
}

// oidcReady reports whether these endpoints have anything to do. All three
// 404 rather than error when OIDC is not this deployment's mode — the same
// shape as any feature nobody asked for, not a broken one.
func (s *Server) oidcReady() bool {
	return s.authenticator().Mode() == auth.ModeOIDC && s.OIDC != nil
}

func (s *Server) oidcNotConfigured(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{
		"error": "this deployment is not running auth.mode: oidc",
	})
}

// handleSSOLogin starts the flow: generates state/nonce/PKCE, seals them
// into a short-lived cookie, and redirects to the issuer.
func (s *Server) handleSSOLogin(w http.ResponseWriter, r *http.Request) {
	if !s.oidcReady() {
		s.oidcNotConfigured(w)
		return
	}
	if s.Box == nil {
		// Unlike Komoot/Garmin sign-in, this cannot degrade to "the button is
		// just hidden" — there is nowhere safe to put the state, and without
		// it the callback cannot be trusted at all.
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "this deployment cannot start a sign-in: no encryption key — set " + secrets.EnvKey,
		})
		return
	}

	returnTo, err := safeReturnTo(r.URL.Query().Get("return_to"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	state, err := oidcflow.NewState()
	if err != nil {
		s.fail(w, err)
		return
	}
	nonce, err := oidcflow.NewNonce()
	if err != nil {
		s.fail(w, err)
		return
	}
	verifier, err := oidcflow.NewPKCEVerifier()
	if err != nil {
		s.fail(w, err)
		return
	}

	if err := s.setStateCookie(w, r, oidcState{
		State: state, Nonce: nonce, Verifier: verifier, ReturnTo: returnTo, IssuedAt: time.Now().UTC(),
	}); err != nil {
		s.fail(w, err)
		return
	}

	redirectURI := s.authenticator().OIDC().RedirectURL
	authURL := s.OIDC.AuthCodeURL(state, nonce, oidcflow.PKCEChallenge(verifier), redirectURI)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleSSOCallback verifies what the issuer sent back and, if it checks
// out, signs the rider in.
func (s *Server) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	if !s.oidcReady() {
		s.oidcNotConfigured(w)
		return
	}

	st, err := s.openStateCookie(r)
	// The state cookie is single-use regardless of outcome: a failed
	// callback must not leave a cookie a client could replay.
	s.clearStateCookie(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "missing or expired sign-in — start over at /sso/login",
		})
		return
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		desc := r.URL.Query().Get("error_description")
		s.logger().Warn("oidc callback reported by issuer", "error", errParam, "description", desc)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "sign-in was not completed: " + errParam,
		})
		return
	}

	if got := r.URL.Query().Get("state"); got == "" || got != st.State {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "state did not match — start over"})
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the issuer sent no code"})
		return
	}

	redirectURI := s.authenticator().OIDC().RedirectURL
	rawIDToken, err := s.OIDC.Exchange(r.Context(), code, st.Verifier, redirectURI)
	if err != nil {
		s.logger().Warn("oidc token exchange failed", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "the identity provider did not accept the sign-in",
		})
		return
	}

	idToken, err := s.OIDC.VerifyIDToken(r.Context(), rawIDToken)
	if err != nil {
		s.logger().Warn("oidc id token failed verification", "err", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "the sign-in could not be verified"})
		return
	}
	if idToken.Nonce != st.Nonce {
		// go-oidc verifies signature/issuer/audience/expiry but deliberately
		// leaves nonce to the caller — only the caller knows what it asked
		// for. This is that check.
		s.logger().Warn("oidc nonce mismatch")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "the sign-in could not be verified"})
		return
	}

	identity, err := s.identityFromToken(idToken)
	if err != nil {
		s.logger().Warn("oidc token carried no usable identity", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "the identity provider did not identify who signed in",
		})
		return
	}

	token, expiresAt, err := s.Sessions.Create(identity, sessionTTL)
	if err != nil {
		s.fail(w, err)
		return
	}
	// #nosec G124 -- Secure, HttpOnly and SameSite are all set; gosec wants
	// Secure to be the literal `true` and cannot see through requestIsHTTPS,
	// which is conditional on purpose — see its own doc comment.
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})

	s.logger().Info("oidc login", "user", identity.User, "groups", identity.Groups)

	returnTo := st.ReturnTo
	if returnTo == "" {
		returnTo = "/"
	}
	http.Redirect(w, r, returnTo, http.StatusFound)
}

// handleSSOLogout ends the session and tells the frontend where to go next.
//
// A JSON response rather than a redirect: signing out is a same-origin
// fetch, and a fetch cannot itself carry the browser to a cross-origin
// top-level page the way a plain link can. The frontend does that
// navigation; this only says where.
func (s *Server) handleSSOLogout(w http.ResponseWriter, r *http.Request) {
	if !s.oidcReady() {
		s.oidcNotConfigured(w)
		return
	}

	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		if err := s.Sessions.Delete(cookie.Value); err != nil {
			s.logger().Warn("deleting oidc session failed", "err", err)
		}
	}
	// #nosec G124 -- see the identical note above; Secure is intentionally
	// conditional, not a literal true.
	http.SetCookie(w, &http.Cookie{
		Name: auth.SessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: requestIsHTTPS(r), SameSite: http.SameSiteLaxMode,
	})

	redirectTo := s.OIDC.EndSessionURL(requestOrigin(r)+"/", "")
	writeJSON(w, http.StatusOK, map[string]string{"redirectTo": redirectTo})
}

// identityFromToken builds an Identity from a verified ID token's claims.
//
// User is preferred_username, falling back to nickname, falling back to sub.
// Auth0's database connection never populates preferred_username, but does
// send nickname (defaults to the local part of the email, editable per-user
// in the Auth0 dashboard) whenever the "profile" scope is requested — so for
// that issuer this is the readable rider identity, and sub (shaped like
// "auth0|64f2a1b2c3d4e5f6") is only the fallback for an issuer that sends
// neither. nickname is skipped, not just lower-cased, if it does not survive
// RiderPattern (an OIDC nickname can contain spaces or other characters an
// account id and a URL cannot) — falling through to sub rather than handing
// Link a value it will reject one step later.
func (s *Server) identityFromToken(idToken *oidc.IDToken) (auth.Identity, error) {
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return auth.Identity{}, err
	}

	user := strings.ToLower(strings.TrimSpace(stringClaim(claims, "preferred_username")))
	if user == "" {
		if nickname := strings.ToLower(strings.TrimSpace(stringClaim(claims, "nickname"))); accounts.RiderPattern.MatchString(nickname) {
			user = nickname
		}
	}
	if user == "" {
		user = strings.ToLower(strings.TrimSpace(idToken.Subject))
	}
	if user == "" {
		return auth.Identity{}, errors.New("neither preferred_username, nickname nor sub was present")
	}

	// Absent groups claim is not an error — every issuer that sends none
	// (Google, or Auth0 before its groups Action exists) falls through to
	// default_role, which is what that setting is for.
	groupsClaim := s.authenticator().OIDC().GroupsClaim
	return auth.Identity{
		User:   user,
		Name:   strings.TrimSpace(stringClaim(claims, "name")),
		Email:  strings.TrimSpace(stringClaim(claims, "email")),
		Groups: stringSliceClaim(claims, groupsClaim),
	}, nil
}

func stringClaim(claims map[string]any, key string) string {
	s, _ := claims[key].(string)
	return s
}

func stringSliceClaim(claims map[string]any, key string) []string {
	raw, ok := claims[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// setStateCookie seals st and sets it as auth.OIDCStateCookie.
func (s *Server) setStateCookie(w http.ResponseWriter, r *http.Request, st oidcState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	sealed, err := s.Box.Seal(string(raw))
	if err != nil {
		return err
	}
	// #nosec G124 -- see the note on the session cookie above.
	http.SetCookie(w, &http.Cookie{
		Name:     auth.OIDCStateCookie,
		Value:    base64.RawURLEncoding.EncodeToString(sealed),
		Path:     "/sso/",
		MaxAge:   600, // 10 minutes: long enough for a slow login, short enough not to matter if abandoned
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode, // the callback is a cross-site top-level GET; Strict would drop this cookie before the handler ever saw it
	})
	return nil
}

// openStateCookie reads and unseals auth.OIDCStateCookie. Any failure —
// missing cookie, undecodable, tampered, unparseable — is reported
// identically to the caller: the sign-in cannot be trusted, start over.
func (s *Server) openStateCookie(r *http.Request) (oidcState, error) {
	cookie, err := r.Cookie(auth.OIDCStateCookie)
	if err != nil {
		return oidcState{}, err
	}
	sealed, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return oidcState{}, err
	}
	raw, err := s.Box.Open(sealed)
	if err != nil {
		return oidcState{}, err
	}
	var st oidcState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return oidcState{}, err
	}
	return st, nil
}

func (s *Server) clearStateCookie(w http.ResponseWriter, r *http.Request) {
	// #nosec G124 -- see the note on the session cookie above.
	http.SetCookie(w, &http.Cookie{
		Name: auth.OIDCStateCookie, Value: "", Path: "/sso/", MaxAge: -1,
		HttpOnly: true, Secure: requestIsHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
}

// safeReturnTo rejects anything that is not a path on this site. Without
// this, ?return_to= on /sso/login would be an open redirect: the state
// cookie is trusted, and an attacker cannot forge it, but they can still
// hand a victim a login link that legitimately signs them in and then sends
// them somewhere else entirely.
func safeReturnTo(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "", errors.New("return_to must be a path on this site")
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return "", errors.New("return_to must be a path on this site")
	}
	return raw, nil
}

// requestIsHTTPS decides the cookie Secure attribute. Traefik terminates TLS
// in front of the pod and sets X-Forwarded-Proto, so production cookies are
// still Secure; a local dev loop against a test issuer over plain HTTP still
// works without a config flag to remember.
func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// requestOrigin is scheme://host for the request that reached this server —
// used only as the default post-logout landing place, never trusted for
// anything security-relevant (the state cookie is what carries trust here).
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if requestIsHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
