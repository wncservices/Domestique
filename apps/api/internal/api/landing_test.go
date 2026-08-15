package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wncservices/domestique/apps/api/internal/auth"
)

// The apex serves the logged-out page; the app host serves the app. Getting
// this backwards would either hide the app behind a marketing page or show a
// signed-in rider the sales pitch.
func TestLandingIsServedOnlyOnTheApex(t *testing.T) {
	web := fstest.MapFS{
		"index.html":    {Data: []byte("<html>APP</html>")},
		"landing.html":  {Data: []byte("<html>LANDING</html>")},
		"assets/app.js": {Data: []byte("<html>ASSET</html>")},
	}

	for _, tc := range []struct {
		name, landingHost, host, path, want string
	}{
		{"apex gets the landing page", "domestique.dev", "domestique.dev", "/", "LANDING"},
		{"app host gets the app", "domestique.dev", "app.domestique.dev", "/", "APP"},
		{"host matching ignores case", "domestique.dev", "Domestique.Dev", "/", "LANDING"},
		{"a port on the host is ignored", "domestique.dev", "domestique.dev:8080", "/", "LANDING"},
		// Every path on the apex is the landing page, not just the root.
		// This used to serve the app, on the theory that only the front door
		// needed the logged-out page — which meant domestique.dev/settings
		// rendered the application UI on the one host that is deliberately
		// public and carries no forwardAuth. Nothing behind it was reachable,
		// since every API call arrives there without Remote-User and is
		// refused, but a logged-out page that is only logged-out at "/" is
		// not a logged-out page.
		{"a deep link on the apex is still the landing page",
			"domestique.dev", "domestique.dev", "/settings", "LANDING"},
		{"an unknown deep link too",
			"domestique.dev", "domestique.dev", "/routes/some-ride", "LANDING"},
		// Real files are still themselves: both pages load /assets/... from
		// the same build, so the apex must serve them rather than answering
		// every request with HTML.
		{"assets are served on the apex", "domestique.dev", "domestique.dev",
			"/assets/app.js", "ASSET"},
		{"assets are served on the app host", "domestique.dev", "app.domestique.dev",
			"/assets/app.js", "ASSET"},
		// A laptop configures no landing host and must still get the app.
		{"unset serves the app to everyone", "", "domestique.dev", "/", "APP"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &Server{WebFS: web, LandingHost: tc.landingHost}

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			srv.spaHandler().ServeHTTP(rec, req)

			if got := rec.Body.String(); got != "<html>"+tc.want+"</html>" {
				t.Errorf("served %q, want %s", got, tc.want)
			}
		})
	}
}

// An unbuilt or older frontend has no landing.html. Serving the app is a far
// better failure than 404-ing the front door.
func TestMissingLandingFallsBackToTheApp(t *testing.T) {
	web := fstest.MapFS{"index.html": {Data: []byte("<html>APP</html>")}}
	srv := &Server{WebFS: web, LandingHost: "domestique.dev"}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "domestique.dev"
	rec := httptest.NewRecorder()
	srv.spaHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "<html>APP</html>" {
		t.Errorf("status %d body %q, want the app", rec.Code, rec.Body.String())
	}
}

// http.FileServer publishes a directory index for any directory it is asked
// for, so /assets/ answered with a list of every file in the build — on the
// public host too. Nothing secret is in those names, but nobody asked this
// server to publish an index, and a directory is not a page.
func TestDirectoriesAreNotListed(t *testing.T) {
	web := fstest.MapFS{
		"index.html":    {Data: []byte("<html>APP</html>")},
		"landing.html":  {Data: []byte("<html>LANDING</html>")},
		"assets/app.js": {Data: []byte("<html>ASSET</html>")},
	}

	for _, tc := range []struct{ name, host, path, want string }{
		{"apex", "domestique.dev", "/assets/", "LANDING"},
		{"app host", "app.domestique.dev", "/assets/", "APP"},
		{"without the trailing slash", "app.domestique.dev", "/assets", "APP"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &Server{WebFS: web, LandingHost: "domestique.dev"}

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			srv.spaHandler().ServeHTTP(rec, req)

			body := rec.Body.String()
			if strings.Contains(body, "app.js") {
				t.Errorf("the directory was listed: %q", body)
			}
			if body != "<html>"+tc.want+"</html>" {
				t.Errorf("served %q, want %s", body, tc.want)
			}
		})
	}
}

func oidcAuthenticator(t *testing.T) *auth.Authenticator {
	t.Helper()
	a, err := auth.New(auth.Config{
		Mode: auth.ModeOIDC,
		OIDC: auth.OIDCConfig{
			Issuer: "https://idp.example.test/", ClientID: "x",
			RedirectURL: "https://app.example.test/sso/callback",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// mode: oidc is the one mode where the app itself decides who gets in — an
// anonymous visitor to the app host is sent to the front door instead of
// the app shell, same as mode: proxy always looked from outside (Traefik
// never let the request arrive at all).
func TestAnonymousOIDCVisitorsAreSentToTheLandingPage(t *testing.T) {
	web := fstest.MapFS{
		"index.html":    {Data: []byte("<html>APP</html>")},
		"landing.html":  {Data: []byte("<html>LANDING</html>")},
		"assets/app.js": {Data: []byte("<html>ASSET</html>")},
	}
	srv := &Server{WebFS: web, LandingHost: "domestique.dev", Auth: oidcAuthenticator(t)}

	for _, tc := range []struct{ name, path string }{
		{"the app shell itself", "/"},
		{"a deep link", "/settings"},
		{"an unknown deep link", "/routes/some-ride"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Host = "app.domestique.dev"
			rec := httptest.NewRecorder()
			srv.spaHandler().ServeHTTP(rec, req)

			if rec.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302", rec.Code)
			}
			if loc := rec.Header().Get("Location"); loc != "https://domestique.dev/" {
				t.Errorf("Location = %q, want https://domestique.dev/", loc)
			}
		})
	}
}

// The redirect must not swallow the app's own assets — the landing page it
// redirects to needs them loadable too.
func TestAnonymousOIDCVisitorsStillGetTheirAssets(t *testing.T) {
	web := fstest.MapFS{
		"index.html":    {Data: []byte("<html>APP</html>")},
		"landing.html":  {Data: []byte("<html>LANDING</html>")},
		"assets/app.js": {Data: []byte("<html>ASSET</html>")},
	}
	srv := &Server{WebFS: web, LandingHost: "domestique.dev", Auth: oidcAuthenticator(t)}

	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	req.Host = "app.domestique.dev"
	rec := httptest.NewRecorder()
	srv.spaHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "<html>ASSET</html>" {
		t.Errorf("status %d body %q, want the asset", rec.Code, rec.Body.String())
	}
}

// A signed-in rider still reaches the app — the redirect is only for nobody
// being signed in at all, not for mode: oidc itself.
func TestAuthenticatedOIDCVisitorsStillGetTheApp(t *testing.T) {
	web := fstest.MapFS{
		"index.html":   {Data: []byte("<html>APP</html>")},
		"landing.html": {Data: []byte("<html>LANDING</html>")},
	}
	srv := &Server{WebFS: web, LandingHost: "domestique.dev", Auth: oidcAuthenticator(t)}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "app.domestique.dev"
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.Identity{User: "wilant"}))
	rec := httptest.NewRecorder()
	srv.spaHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "<html>APP</html>" {
		t.Errorf("status %d body %q, want the app", rec.Code, rec.Body.String())
	}
}

// The rest of this file never configures Auth (authenticator() falls back to
// mode: none) and still expects the app host to serve the app — proof the
// redirect is gated on mode: oidc specifically, not on being anonymous alone.
func TestModeNoneNeverRedirectsTheAppHost(t *testing.T) {
	web := fstest.MapFS{
		"index.html":   {Data: []byte("<html>APP</html>")},
		"landing.html": {Data: []byte("<html>LANDING</html>")},
	}
	srv := &Server{WebFS: web, LandingHost: "domestique.dev"}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "app.domestique.dev"
	rec := httptest.NewRecorder()
	srv.spaHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "<html>APP</html>" {
		t.Errorf("status %d body %q, want the app", rec.Code, rec.Body.String())
	}
}
