package garmin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient wires a Client to srv with a pre-loaded bearer so these tests
// do not need to stand up a full OAuth exchange endpoint.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := New()
	c.APIBase = srv.URL
	c.SetConsumer(testKey, testSecret)
	c.bearer = "test-bearer"
	c.bearerTill = time.Now().Add(time.Hour)
	return c
}

// Connect's real answer to /import: the file was parsed, the name was read
// out of it, and nothing was saved.
const parsedCourse = `{"courseId":null,"courseName":"Abdij van Vlierbeek",` +
	`"description":null,"openStreetMap":false,"matchedToSegments":false,` +
	`"userProfilePk":null,"userGroupPk":null}`

// Importing is two calls. The first parses, the second saves — and only the
// second has an id. Believing the first was a whole library of routes that
// looked uploaded and were not.
func TestImportSavesTheParsedCourse(t *testing.T) {
	var savedBody string
	var paths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")

		if strings.HasSuffix(r.URL.Path, "/import") {
			io.WriteString(w, parsedCourse)
			return
		}
		body, _ := io.ReadAll(r.Body)
		savedBody = string(body)
		io.WriteString(w, `{"courseId":987654,"courseName":"Abdij van Vlierbeek"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)

	id, err := c.ImportCourse("ride.fit", []byte("FIT-BYTES"))
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if id != "987654" {
		t.Errorf("id = %q, want the saved course's", id)
	}
	if len(paths) != 2 {
		t.Fatalf("made %d calls (%v), want import then save", len(paths), paths)
	}
	if !strings.HasSuffix(paths[0], "/import") || strings.HasSuffix(paths[1], "/import") {
		t.Errorf("calls were %v, want import first then the service", paths)
	}
	// coursePrivacy is injected (Garmin rejects the DTO without it); all other
	// fields come from what /import returned.
	if !strings.Contains(savedBody, `"coursePrivacy":2`) {
		t.Errorf("saved %q, want coursePrivacy injected", savedBody)
	}
	if !strings.Contains(savedBody, `"courseName":"Abdij van Vlierbeek"`) {
		t.Errorf("saved %q, want the parsed course fields preserved", savedBody)
	}
}

// If Connect ever folds the two calls into one, taking the id it gives is
// both correct and one fewer request.
func TestImportStopsWhenTheParseAlreadyHasAnID(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `{"courseId":123}`)
	}))
	defer srv.Close()

	id, err := newTestClient(t, srv).ImportCourse("ride.fit", []byte("FIT"))
	if err != nil {
		t.Fatal(err)
	}
	if id != "123" || calls != 1 {
		t.Errorf("id = %q after %d calls, want 123 after 1", id, calls)
	}
}

// A save that fails must say so rather than report a course nobody has.
func TestImportReportsAFailedSave(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/import") {
			io.WriteString(w, parsedCourse)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"message":"nope"}`)
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).ImportCourse("ride.fit", []byte("FIT")); err == nil {
		t.Fatal("a failed save reported success")
	} else if !strings.Contains(err.Error(), "saving the course") {
		t.Errorf("err = %v, want it to name the save", err)
	}
}

// An expired session at the save step is the same problem as at any other,
// and must read the same way.
func TestImportReportsARefusedSessionOnSave(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/import") {
			io.WriteString(w, parsedCourse)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).ImportCourse("ride.fit", []byte("FIT"))
	if err == nil || !strings.Contains(err.Error(), "sign in again") {
		t.Errorf("err = %v, want it to say to sign in again", err)
	}
}

// /import never populates coursePrivacy; the save endpoint rejects anything
// outside {1,2,4}. The fix is to inject 2 (Private) before posting.
func TestSaveInjectsPrivacyWhenMissing(t *testing.T) {
	var savedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/import") {
			io.WriteString(w, parsedCourse)
			return
		}
		b, _ := io.ReadAll(r.Body)
		savedBody = string(b)
		io.WriteString(w, `{"courseId":1}`)
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).ImportCourse("r.fit", []byte("FIT")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(savedBody, `"coursePrivacy":2`) {
		t.Errorf("saved body = %s, want coursePrivacy:2", savedBody)
	}
}

func TestWithPrivacyKeepsAValidValue(t *testing.T) {
	for _, v := range []int{1, 2, 4} {
		in := []byte(fmt.Sprintf(`{"courseId":null,"coursePrivacy":%d}`, v))
		out, err := withPrivacy(in)
		if err != nil {
			t.Fatalf("privacy %d: %v", v, err)
		}
		if !strings.Contains(string(out), fmt.Sprintf(`"coursePrivacy":%d`, v)) {
			t.Errorf("privacy %d was changed: %s", v, out)
		}
	}
}

// The save rejects a course with no privacy:
//
//	'createCourse.arg3' Course privacy can be 1-Public, 2-Private or 4-Group
//
// Which field carries it is not known, so every candidate is set. This test
// pins the values rather than the names: whichever one Connect reads, it must
// find Private.
func TestSaveSetsPrivacyOnEveryCandidateField(t *testing.T) {
	var saved map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/import") {
			io.WriteString(w, parsedCourse)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&saved); err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, `{"courseId":1}`)
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).ImportCourse("ride.fit", []byte("FIT")); err != nil {
		t.Fatal(err)
	}

	for _, field := range privacyFields {
		if !valid(saved[field]) {
			t.Errorf("%s = %v, want a privacy the service accepts", field, saved[field])
		}
	}
	// Private, not public: a push must not publish somebody's routes.
	if rule, ok := saved["privacyRule"].(map[string]any); ok {
		if rule["typeId"] != float64(privacyPrivate) {
			t.Errorf("privacyRule = %v, want private", rule)
		}
	}
}

// A course that already carries a privacy keeps it. Connect saying what it
// wants is worth more than this package's default.
func TestSaveKeepsAPrivacyConnectAlreadySet(t *testing.T) {
	var saved map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/import") {
			io.WriteString(w, `{"courseId":null,"courseName":"X","coursePrivacy":1}`)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&saved); err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, `{"courseId":1}`)
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).ImportCourse("ride.fit", []byte("FIT")); err != nil {
		t.Fatal(err)
	}
	if saved["coursePrivacy"] != float64(1) {
		t.Errorf("coursePrivacy = %v, want the value Connect set", saved["coursePrivacy"])
	}
}

// The rejection body names every field of the DTO, and that is the only
// description of the shape that exists. Truncating it is what turned this
// into guesswork.
//
// A real route is hundreds of points: geoPoints alone runs well past 2000
// characters on its own, in front of every other field. 40 repeats — the
// count this test used before — stays under that limit by accident and would
// have passed even without stripping the array. 400 does not, which is the
// scale where the old fixed limit actually failed in production.
func TestSaveFailureKeepsEnoughOfTheBodyToBeUseful(t *testing.T) {
	long := `{"errors":["'createCourse.arg3' Course privacy can be 1-Public, 2-Private or 4-Group ` +
		`(provided value: CourseDTO(geoPoints=[` + strings.Repeat("GeoPointDTO(latitude=50.89, longitude=4.71, elevation=17.2, distance=23.9, timestamp=1155635443), ", 400) +
		`], coursePrivacyTypeId=null, courseName=X))"]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/import") {
			io.WriteString(w, parsedCourse)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, long)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).ImportCourse("ride.fit", []byte("FIT"))
	if err == nil {
		t.Fatal("a rejected save reported success")
	}
	if !strings.Contains(err.Error(), "coursePrivacyTypeId") {
		t.Errorf("the error stops before the field names:\n%v", err)
	}
	if strings.Contains(err.Error(), "latitude=50.89") {
		t.Errorf("the geoPoints dump survived instead of being stripped:\n%v", err)
	}
}

// withoutGeoPoints is what makes the size limit reachable at all, at any
// route length: it removes the array rather than budgeting for it.
func TestWithoutGeoPointsRemovesOnlyTheArray(t *testing.T) {
	in := `CourseDTO(geoPoints=[GeoPointDTO(lat=1), GeoPointDTO(lat=2)], coursePrivacyTypeId=null, courseName=X)`
	got := withoutGeoPoints(in)

	if strings.Contains(got, "GeoPointDTO") {
		t.Errorf("a point survived: %q", got)
	}
	for _, want := range []string{"CourseDTO(geoPoints=[", "coursePrivacyTypeId=null", "courseName=X)"} {
		if !strings.Contains(got, want) {
			t.Errorf("got %q, missing %q", got, want)
		}
	}
}

// A nested bracket inside an entry must not end the scan early — the whole
// array has to be skipped, not just up to its first inner ']'.
func TestWithoutGeoPointsHandlesNestedBrackets(t *testing.T) {
	in := `CourseDTO(geoPoints=[Point(tags=[a, b]), Point(tags=[c])], courseName=X)`
	got := withoutGeoPoints(in)

	if strings.Contains(got, "tags=") {
		t.Errorf("stopped at an inner bracket: %q", got)
	}
	if !strings.Contains(got, "courseName=X)") {
		t.Errorf("dropped what came after the array: %q", got)
	}
}

// Text without a geoPoints array — every response that is not this one
// specific rejection — passes through untouched.
func TestWithoutGeoPointsLeavesOrdinaryTextAlone(t *testing.T) {
	in := `{"courseId":123,"courseName":"X"}`
	if got := withoutGeoPoints(in); got != in {
		t.Errorf("got %q, want it unchanged", got)
	}
}
