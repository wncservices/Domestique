package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
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
