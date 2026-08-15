package garmin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// listFake serves the endpoints captured from Connect's own Training →
// Courses page: the owner listing and a GPX download.
func listFake(t *testing.T, ownerStatus int, ownerBody string, gpxStatus int, gpxBody string) *Client {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth-service/oauth/exchange/user/2.0", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"access_token":"bearer-1","expires_in":3600}`)
	})
	mux.HandleFunc("/web-gateway/course/owner/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(ownerStatus)
		fmt.Fprint(w, ownerBody)
	})
	mux.HandleFunc("/course-service/course/gpx/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(gpxStatus)
		fmt.Fprint(w, gpxBody)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	c := New()
	c.APIBase = server.URL
	c.SetConsumer(testKey, testSecret)
	c.Resume(Session{OAuth1Token: "tok-1", OAuth1Secret: "sec-1"})
	return c
}

// The real shape, trimmed to what ListCourses actually reads — captured
// against a live account, not guessed.
const ownerResponseFixture = `{"coursesForUser":[
	{
		"courseId": 502255241,
		"courseName": "Tourtje in het Hageland V2.0",
		"distanceInMeters": 49656.52,
		"elevationGainInMeters": 393.12,
		"startLatitude": 50.86387895978987,
		"startLongitude": 4.70188298262656,
		"activityType": {"typeId": 2, "typeKey": "cycling"},
		"createdDate": 1786723336000
	}
]}`

func TestListCoursesParsesTheOwnerResponse(t *testing.T) {
	c := listFake(t, http.StatusOK, ownerResponseFixture, http.StatusOK, "")

	courses, err := c.ListCourses()
	if err != nil {
		t.Fatalf("ListCourses: %v", err)
	}
	if len(courses) != 1 {
		t.Fatalf("got %d courses, want 1", len(courses))
	}

	got := courses[0]
	want := Course{
		ID: "502255241", Name: "Tourtje in het Hageland V2.0",
		DistanceM: 49656.52, AscentM: 393.12,
		StartLat: 50.86387895978987, StartLng: 4.70188298262656,
		ActivityType: "cycling",
	}
	if got.ID != want.ID || got.Name != want.Name || got.DistanceM != want.DistanceM ||
		got.AscentM != want.AscentM || got.StartLat != want.StartLat || got.StartLng != want.StartLng ||
		got.ActivityType != want.ActivityType {
		t.Errorf("course = %+v, want %+v", got, want)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt was not parsed from createdDate")
	}
}

func TestListCoursesSurfacesUnauthorized(t *testing.T) {
	c := listFake(t, http.StatusUnauthorized, "", http.StatusOK, "")

	if _, err := c.ListCourses(); err == nil {
		t.Fatal("want an error when the session is refused")
	}
}

func TestListCoursesRejectsUnreadableJSON(t *testing.T) {
	c := listFake(t, http.StatusOK, "<html>not json</html>", http.StatusOK, "")

	if _, err := c.ListCourses(); err == nil {
		t.Fatal("want an error for a response that is not the expected shape")
	}
}

func TestDownloadGPXReturnsTheBody(t *testing.T) {
	c := listFake(t, http.StatusOK, "", http.StatusOK, "<gpx>track</gpx>")

	raw, err := c.DownloadGPX("502255241")
	if err != nil {
		t.Fatalf("DownloadGPX: %v", err)
	}
	if string(raw) != "<gpx>track</gpx>" {
		t.Errorf("body = %q, want the GPX unchanged", raw)
	}
}

func TestDownloadGPXWithoutAnIdIsAnError(t *testing.T) {
	c := listFake(t, http.StatusOK, "", http.StatusOK, "<gpx/>")

	if _, err := c.DownloadGPX(""); err == nil {
		t.Fatal("want an error for an empty course id")
	}
}

func TestDownloadGPXSurfacesNotFound(t *testing.T) {
	c := listFake(t, http.StatusOK, "", http.StatusNotFound, "")

	if _, err := c.DownloadGPX("no-such-course"); err == nil {
		t.Fatal("want an error when the course does not exist")
	}
}

func TestDownloadGPXRejectsAnEmptyBody(t *testing.T) {
	c := listFake(t, http.StatusOK, "", http.StatusOK, "")

	if _, err := c.DownloadGPX("502255241"); err == nil {
		t.Fatal("want an error for a course that downloaded empty")
	}
}
