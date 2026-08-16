// Package wahoo drives the OAuth2 authorization-code exchange against
// Wahoo's Cloud API (https://cloud-api.wahooligan.com/).
//
// Unlike Garmin and Komoot, which speak an undocumented sign-in flow this
// app has to reverse-engineer, Wahoo's API is a documented, ordinary OAuth2
// confidential-client flow: authorize, exchange a code for a token pair,
// refresh when it expires. No PKCE — this app is registered confidential
// (Wahoo's own registration reserves PKCE for apps that cannot hold a
// client secret), and the secret sent in Exchange/Refresh is what protects
// the code exchange instead. No dependency beyond the standard library: a
// plain form-encoded POST is small enough that pulling in
// golang.org/x/oauth2 for it would cost more than it saves, the same
// reasoning internal/oidcflow's own doc comment gives for hand-rolling its
// token exchange — and this package needs less than that one does, since
// there is no discovery document, no JWKS, and no ID token to verify.
package wahoo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	apiBase = "https://api.wahooligan.com"

	// scopes is fixed, not operator-configurable: exactly what this app
	// functionally needs and what was registered with Wahoo. user_read is
	// mandatory regardless — Wahoo 403s any token that lacks it.
	scopes = "user_read routes_read routes_write"

	defaultTimeout = 30 * time.Second
)

// Config is this deployment's Wahoo app registration.
type Config struct {
	ClientID     string
	ClientSecret string
	// RedirectURL must equal, exactly, what is registered with Wahoo.
	RedirectURL string
}

// Session is what an authorization produced, on its way to being stored —
// the same role garmin.Session plays for Garmin's OAuth1 pair, just OAuth2.
type Session struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scope        string    `json:"scope"`
}

// Expired reports whether the access token needs a refresh before use.
func (s Session) Expired() bool { return time.Now().After(s.ExpiresAt) }

// Profile is the authenticated rider's own Wahoo account — enough to show
// whose connection this is.
type Profile struct {
	ID    int    `json:"id"`
	Email string `json:"email"`
	First string `json:"first"`
	Last  string `json:"last"`
}

// DisplayName is what the UI shows for a connection. Wahoo has no single
// display-name field, so this is First and Last joined, falling back to the
// email when a rider left their name blank.
func (p Profile) DisplayName() string {
	name := strings.TrimSpace(strings.TrimSpace(p.First) + " " + strings.TrimSpace(p.Last))
	if name == "" {
		return p.Email
	}
	return name
}

// Client drives one deployment's authorization-code flow.
type Client struct {
	HTTP *http.Client
	cfg  Config

	// APIBase, overridable so tests do not touch Wahoo.
	APIBase string
}

// New builds a Client. cfg's fields must all be non-empty — the caller (this
// app's own startup wiring) already knows whether Wahoo is configured at
// all and leaves Client nil rather than building one that would fail on
// first use.
func New(cfg Config) *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout:   defaultTimeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
		cfg:     cfg,
		APIBase: apiBase,
	}
}

// AuthCodeURL builds the redirect to Wahoo's authorization endpoint.
func (c *Client) AuthCodeURL(state string) string {
	v := url.Values{
		"client_id":     {c.cfg.ClientID},
		"redirect_uri":  {c.cfg.RedirectURL},
		"response_type": {"code"},
		"scope":         {scopes},
		"state":         {state},
	}
	return c.APIBase + "/oauth/authorize?" + v.Encode()
}

// Exchange trades an authorization code for a session.
func (c *Client) Exchange(ctx context.Context, code string) (Session, error) {
	return c.tokenRequest(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.cfg.RedirectURL},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
	})
}

// Refresh trades a refresh token for a new session, once a stored one has
// expired. Wahoo may or may not return a new refresh_token in the
// response; when it doesn't, the caller keeps using the one it already
// has — the grant only replaces what it actually returned.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (Session, error) {
	session, err := c.tokenRequest(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
	})
	if err != nil {
		return Session{}, err
	}
	if session.RefreshToken == "" {
		session.RefreshToken = refreshToken
	}
	return session, nil
}

// tokenResponse is the token endpoint's JSON shape.
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (c *Client) tokenRequest(ctx context.Context, form url.Values) (Session, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIBase+"/oauth/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return Session{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Session{}, fmt.Errorf("wahoo: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Session{}, fmt.Errorf("wahoo: reading token response: %w", err)
	}

	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return Session{}, fmt.Errorf("wahoo: unreadable token response (status %d): %s",
			resp.StatusCode, snippet(body))
	}
	if tok.Error != "" {
		return Session{}, fmt.Errorf("wahoo: %s: %s", tok.Error, tok.ErrorDescription)
	}
	if resp.StatusCode != http.StatusOK {
		return Session{}, fmt.Errorf("wahoo: token endpoint returned %d: %s", resp.StatusCode, snippet(body))
	}
	if tok.AccessToken == "" {
		return Session{}, errors.New("wahoo: token response carried no access_token")
	}

	return Session{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
		Scope:        tok.Scope,
	}, nil
}

// Me fetches the authenticated rider's own profile — called once at connect
// time so the stored connection has an email and display name to show, the
// same information Garmin's login response already carries inline and
// Wahoo's token response does not.
func (c *Client) Me(ctx context.Context, accessToken string) (Profile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIBase+"/v1/user", nil)
	if err != nil {
		return Profile{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Profile{}, fmt.Errorf("wahoo: profile request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Profile{}, fmt.Errorf("wahoo: reading profile response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Profile{}, fmt.Errorf("wahoo: profile endpoint returned %d: %s", resp.StatusCode, snippet(body))
	}

	var p Profile
	if err := json.Unmarshal(body, &p); err != nil {
		return Profile{}, fmt.Errorf("wahoo: unreadable profile response: %s", snippet(body))
	}
	return p, nil
}

func snippet(raw []byte) string {
	const limit = 300
	text := strings.TrimSpace(string(raw))
	if len(text) > limit {
		return text[:limit] + "…"
	}
	return text
}
