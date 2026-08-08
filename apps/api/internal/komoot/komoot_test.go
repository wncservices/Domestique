package komoot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/gpx"
)

// Fixture credentials. They are invented, but a secret scanner cannot know
// that, so they are marked explicitly rather than excluding test files from
// scanning altogether — real secrets in tests are worth catching.
const (
	testEmail    = "rider@example.invalid"
	testPassword = "fixture-not-a-real-password" // gitleaks:allow
	testUserID   = "user-123"
	testToken    = "fixture-not-a-real-token" // gitleaks:allow
)

// fakeKomoot stands in for the undocumented API. Because that API can change
// without notice, these tests pin the shapes we depend on: if Komoot moves,
// the client fails loudly here rather than silently importing nothing.
func fakeKomoot(t *testing.T) (*Client, *httptest.Server) {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("/v006/account/email/", func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != testEmail || pass != testPassword {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"username": testUserID,
			"password": testToken,
			"user":     map[string]string{"displayname": "Wilant"},
		})
	})

	mux.HandleFunc("/v007/users/user-123/tours/", func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		if user != testUserID || pass != testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// First page links to a second, to exercise pagination.
		if r.URL.Query().Get("page") == "1" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"_embedded": map[string]any{"tours": []map[string]any{{
					"id": 3, "name": "Second page loop", "type": TypePlanned,
					"sport": "racebike", "distance": 42000.0, "elevation_up": 300.0,
					"changed_at": "2026-08-01T10:00:00Z",
				}}},
				"_links": map[string]any{},
			})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"_embedded": map[string]any{"tours": []map[string]any{
				{
					"id": 1, "name": "Kemmelberg Loop", "type": TypePlanned,
					"sport": "racebike", "distance": 55000.0, "elevation_up": 620.0,
					"changed_at": "2026-08-05T09:30:00Z",
				},
				{
					"id": 2, "name": "Tuesday ride", "type": TypeRecorded,
					"sport": "racebike", "distance": 31000.0, "elevation_up": 120.0,
					"changed_at": "2026-08-04T18:00:00Z",
				},
			}},
			"_links": map[string]any{
				"next": map[string]string{
					"href": "http://" + r.Host + "/v007/users/user-123/tours/?page=1",
				},
			},
		})
	})

	mux.HandleFunc("/v007/tours/1", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "coordinate_array" {
			t.Errorf("missing format=coordinate_array: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "Kemmelberg Loop",
			"_embedded": map[string]any{"coordinates": map[string]any{
				"items": [][]float64{
					{50.7920, 2.8180, 42, 0},
					{50.7982, 2.8344, 128, 600},
					{50.8007, 2.8437, 139, 1200},
				},
			}},
		})
	})

	mux.HandleFunc("/v007/tours/empty", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":      "Nothing here",
			"_embedded": map[string]any{"coordinates": map[string]any{"items": [][]float64{}}},
		})
	})

	mux.HandleFunc("/v007/tours/moved", func(w http.ResponseWriter, _ *http.Request) {
		// What a relocated undocumented endpoint actually returns.
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>Not here any more</body></html>"))
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	c := New()
	c.BaseV6 = server.URL + "/v006"
	c.BaseV7 = server.URL + "/v007"
	return c, server
}

func TestLoginExchangesPasswordForToken(t *testing.T) {
	c, _ := fakeKomoot(t)

	if err := c.Login(testEmail, testPassword); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if c.userID != testUserID || c.token != testToken {
		t.Errorf("credentials not stored: %q / %q", c.userID, c.token)
	}
	if c.DisplayName() != "Wilant" {
		t.Errorf("display name = %q", c.DisplayName())
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	c, _ := fakeKomoot(t)

	err := c.Login(testEmail, "wrong")
	if err == nil {
		t.Fatal("bad password accepted")
	}
	// The message has to point at the cause; this is the most common failure.
	if !strings.Contains(err.Error(), "credentials") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestLoginRequiresBothFields(t *testing.T) {
	c, _ := fakeKomoot(t)
	if err := c.Login("", testPassword); err == nil {
		t.Error("empty email accepted")
	}
	if err := c.Login(testEmail, ""); err == nil {
		t.Error("empty password accepted")
	}
}

func TestToursFiltersRecordedRides(t *testing.T) {
	c, _ := fakeKomoot(t)
	if err := c.Login(testEmail, testPassword); err != nil {
		t.Fatal(err)
	}

	tours, err := c.Tours(false)
	if err != nil {
		t.Fatalf("Tours: %v", err)
	}

	for _, tour := range tours {
		if tour.Type == TypeRecorded {
			t.Errorf("recorded ride %q leaked into the route list", tour.Name)
		}
	}
	// Two planned tours across two pages; the recorded one is filtered out.
	if len(tours) != 2 || tours[0].Name != "Kemmelberg Loop" {
		t.Fatalf("tours = %+v, want both planned ones", tours)
	}
	if tours[1].Name != "Second page loop" {
		t.Errorf("pagination did not reach page 2: %+v", tours)
	}
	if tours[0].DistanceM != 55000 || tours[0].AscentM != 620 {
		t.Errorf("metrics lost: %+v", tours[0])
	}
	if tours[0].ChangedAt.IsZero() {
		t.Error("changed_at not parsed")
	}
}

func TestToursCanIncludeRecordedRides(t *testing.T) {
	c, _ := fakeKomoot(t)
	if err := c.Login(testEmail, testPassword); err != nil {
		t.Fatal(err)
	}

	tours, err := c.Tours(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(tours) != 3 {
		t.Fatalf("got %d tours, want all three including the recorded ride", len(tours))
	}
}

func TestToursRequiresLogin(t *testing.T) {
	c, _ := fakeKomoot(t)
	if _, err := c.Tours(false); err == nil {
		t.Fatal("Tours worked without logging in")
	}
}

// The GPX this produces has to survive the same parser the rest of the app
// uses — otherwise an import lands routes nothing else can read.
func TestGPXIsParsableByOurOwnReader(t *testing.T) {
	c, _ := fakeKomoot(t)
	if err := c.Login(testEmail, testPassword); err != nil {
		t.Fatal(err)
	}

	raw, err := c.GPX("1")
	if err != nil {
		t.Fatalf("GPX: %v", err)
	}

	points, err := gpx.ParsePoints(raw)
	if err != nil {
		t.Fatalf("our parser rejected Komoot output: %v\n%s", err, raw)
	}
	if len(points) != 3 {
		t.Fatalf("got %d points, want 3", len(points))
	}
	if points[0].Lat != 50.7920 || points[0].Lon != 2.8180 {
		t.Errorf("first point = %+v", points[0])
	}
	if !points[0].HasEle || points[0].Ele != 42 {
		t.Errorf("elevation lost: %+v", points[0])
	}

	stats := gpx.ComputeStats(points)
	if stats.DistanceM == 0 {
		t.Error("distance came out zero")
	}
}

func TestGPXRejectsEmptyTour(t *testing.T) {
	c, _ := fakeKomoot(t)
	if err := c.Login(testEmail, testPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GPX("empty"); err == nil {
		t.Fatal("a tour with no coordinates produced a GPX")
	}
}

// If the undocumented API moves, we get HTML. Say so clearly rather than
// failing with a JSON decode error nobody can act on.
func TestMovedEndpointGivesAReadableError(t *testing.T) {
	c, _ := fakeKomoot(t)
	if err := c.Login(testEmail, testPassword); err != nil {
		t.Fatal(err)
	}

	_, err := c.GPX("moved")
	if err == nil {
		t.Fatal("HTML response accepted")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("error should say the response was undecodable: %v", err)
	}
}

// Pagination follows a URL out of the response body, and every request carries
// the account's credentials. Following that link off-host would hand those
// credentials to whoever wrote the response.
func TestPaginationWillNotFollowAnotherHost(t *testing.T) {
	var attackerCalled bool
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerCalled = true
		if _, _, ok := r.BasicAuth(); ok {
			t.Error("credentials were sent to the attacker's host")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"_embedded": map[string]any{}})
	}))
	defer attacker.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/v007/users/user-123/tours/", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"_embedded": map[string]any{"tours": []map[string]any{{
				"id": 1, "name": "Real tour", "type": TypePlanned,
			}}},
			// The hostile part: a next link pointing somewhere else entirely.
			"_links": map[string]any{
				"next": map[string]string{"href": attacker.URL + "/steal"},
			},
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	c := New()
	c.BaseV6 = server.URL + "/v006"
	c.BaseV7 = server.URL + "/v007"
	c.LoginWithToken(testUserID, testToken)

	_, err := c.Tours(false)
	if err == nil {
		t.Fatal("off-host pagination link was followed without complaint")
	}
	if attackerCalled {
		t.Fatal("request was actually sent to the attacker's host")
	}
	if !strings.Contains(err.Error(), "not a configured Komoot host") {
		t.Errorf("unclear error: %v", err)
	}
}

func TestRequestsToOtherHostsAreRefused(t *testing.T) {
	c := New()
	c.BaseV6 = "https://api.komoot.de/v006"
	c.BaseV7 = "https://api.komoot.de/v007"

	for _, raw := range []string{
		"http://169.254.169.254/latest/meta-data/", // cloud metadata
		"http://localhost:8080/api/routes",         // the app itself
		"https://api.komoot.de.evil.example/v007/", // suffix trick
		"http://api.komoot.de/v007/",               // downgraded scheme
	} {
		if err := c.allowedHost(raw); err == nil {
			t.Errorf("allowed %s", raw)
		}
	}

	if err := c.allowedHost("https://api.komoot.de/v007/tours/1"); err != nil {
		t.Errorf("refused a legitimate URL: %v", err)
	}
}

func TestTourPlannedHelper(t *testing.T) {
	if !(Tour{Type: TypePlanned}).Planned() {
		t.Error("planned tour not recognised")
	}
	if (Tour{Type: TypeRecorded}).Planned() {
		t.Error("recorded ride reported as planned")
	}
}
