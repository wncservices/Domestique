package garmin

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// courseFake serves the endpoint captured from Connect's own Courses → Import.
func courseFake(t *testing.T, status int, response string) (*Client, *courseRecord) {
	t.Helper()
	rec := &courseRecord{}

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth-service/oauth/exchange/user/2.0", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"access_token":"bearer-1","expires_in":3600}`)
	})
	mux.HandleFunc("/course-service/course/import", func(w http.ResponseWriter, r *http.Request) {
		rec.calls++
		rec.method = r.Method
		rec.auth = r.Header.Get("Authorization")
		rec.accept = r.Header.Get("Accept")
		rec.contentType = r.Header.Get("Content-Type")

		file, headers, err := r.FormFile("file")
		if err != nil {
			t.Errorf("no `file` part in the upload: %v", err)
		} else {
			defer file.Close()
			rec.filename = headers.Filename
			buf := new(bytes.Buffer)
			if _, err := buf.ReadFrom(file); err != nil {
				t.Fatal(err)
			}
			rec.body = buf.Bytes()
		}

		w.WriteHeader(status)
		fmt.Fprint(w, response)
	})
	mux.HandleFunc("/course-service/course/", func(w http.ResponseWriter, r *http.Request) {
		rec.deleted = strings.TrimPrefix(r.URL.Path, "/course-service/course/")
		rec.method = r.Method
		w.WriteHeader(status)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	c := New()
	c.APIBase = server.URL
	c.SetConsumer(testKey, testSecret)
	c.Resume(Session{OAuth1Token: "tok-1", OAuth1Secret: "sec-1"})
	return c, rec
}

type courseRecord struct {
	calls       int
	method      string
	auth        string
	accept      string
	contentType string
	filename    string
	body        []byte
	deleted     string
}

func TestImportCourseSendsTheFileAsConnectDoes(t *testing.T) {
	c, rec := courseFake(t, http.StatusOK, `{"courseId":123456}`)

	id, err := c.ImportCourse(t.Context(), "kemmelberg-loop.fit", []byte("FIT-BYTES"))
	if err != nil {
		t.Fatalf("ImportCourse: %v", err)
	}
	if id != "123456" {
		t.Errorf("id = %q, want 123456", id)
	}

	if rec.method != http.MethodPost {
		t.Errorf("method = %s, want POST", rec.method)
	}
	if !strings.HasPrefix(rec.contentType, "multipart/form-data") {
		t.Errorf("content type = %q, want multipart/form-data", rec.contentType)
	}
	if rec.accept != "application/json" {
		t.Errorf("accept = %q; Connect answers HTML without it", rec.accept)
	}
	if rec.auth != "Bearer bearer-1" {
		t.Errorf("authorization = %q, want the OAuth2 bearer", rec.auth)
	}
	if rec.filename != "kemmelberg-loop.fit" {
		t.Errorf("filename = %q", rec.filename)
	}
	if string(rec.body) != "FIT-BYTES" {
		t.Errorf("body = %q, want the file unchanged", rec.body)
	}
}

// The id's spelling is not documented. Accepting the plausible ones costs
// nothing; pinning one and being wrong loses the id of a course that was in
// fact created, leaving a duplicate on the next push.
func TestImportCourseReadsTheIdHoweverItIsSpelled(t *testing.T) {
	for _, body := range []string{
		`{"courseId":123456}`, `{"id":123456}`, `{"coursePk":123456}`, `{"courseId":"123456"}`,
	} {
		c, _ := courseFake(t, http.StatusOK, body)
		id, err := c.ImportCourse(t.Context(), "c.fit", []byte("x"))
		if err != nil {
			t.Errorf("%s: %v", body, err)
			continue
		}
		if id != "123456" {
			t.Errorf("%s gave id %q, want 123456", body, id)
		}
	}
}

func TestImportCourseWithoutAnIdIsAnError(t *testing.T) {
	c, _ := courseFake(t, http.StatusOK, `{"messages":["ok"]}`)
	if _, err := c.ImportCourse(t.Context(), "c.fit", []byte("x")); err == nil {
		t.Error("a response with no id was accepted")
	}
}

func TestImportCourseRefusesAnEmptyFile(t *testing.T) {
	c, rec := courseFake(t, http.StatusOK, `{"courseId":1}`)
	if _, err := c.ImportCourse(t.Context(), "c.fit", nil); err == nil {
		t.Error("an empty course was uploaded")
	}
	if rec.calls != 0 {
		t.Error("an empty upload still reached Garmin")
	}
}

func TestImportCourseSurfacesRejection(t *testing.T) {
	c, _ := courseFake(t, http.StatusUnauthorized, `{"error":"nope"}`)
	_, err := c.ImportCourse(t.Context(), "c.fit", []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "sign in again") {
		t.Errorf("error = %v, want it to say the session was refused", err)
	}
}

func TestDeleteCourse(t *testing.T) {
	c, rec := courseFake(t, http.StatusOK, "")
	if err := c.DeleteCourse(t.Context(), "123456"); err != nil {
		t.Fatal(err)
	}
	if rec.deleted != "123456" || rec.method != http.MethodDelete {
		t.Errorf("deleted %q with %s, want 123456 with DELETE", rec.deleted, rec.method)
	}
}

// Deleting something already gone is the state the caller asked for.
func TestDeleteCourseToleratesAMissingCourse(t *testing.T) {
	c, _ := courseFake(t, http.StatusNotFound, "")
	if err := c.DeleteCourse(t.Context(), "123456"); err != nil {
		t.Errorf("deleting an absent course errored: %v", err)
	}
}

func TestDeleteCourseNeedsAnId(t *testing.T) {
	c, _ := courseFake(t, http.StatusOK, "")
	if err := c.DeleteCourse(t.Context(), "  "); err == nil {
		t.Error("an empty id was accepted")
	}
}

// Every request carries a credential, so a host we were not configured for
// must be refused before it is sent — not diagnosed from the access log of
// whoever received the token.
func TestRequestsToAnotherHostAreRefused(t *testing.T) {
	c, _ := courseFake(t, http.StatusOK, `{"courseId":1}`)

	// Not by reassigning a base — that would make the host configured, which
	// is the whole point of the check. A URL that is simply somewhere else.
	for _, elsewhere := range []string{
		"https://evil.example.com/course-service/course/import",
		"http://169.254.169.254/latest/meta-data/",
		"https://sso.garmin.com.evil.example.com/sso/signin",
	} {
		_, _, err := c.do(t.Context(), http.MethodGet, elsewhere, nil, "")
		if err == nil {
			t.Errorf("%s was allowed", elsewhere)
			continue
		}
		if !strings.Contains(err.Error(), "not a configured host") {
			t.Errorf("%s gave %v, want the refusal", elsewhere, err)
		}
	}
}

func TestConfiguredHostsAreAllowed(t *testing.T) {
	c, _ := courseFake(t, http.StatusOK, `{"courseId":1}`)
	for _, base := range []string{c.SSOBase, c.APIBase, c.WebBase} {
		if base == "" {
			continue
		}
		if err := c.allowedHost(base + "/anything"); err != nil {
			t.Errorf("configured base %q was refused: %v", base, err)
		}
	}
}
