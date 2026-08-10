package targets

import (
	"errors"
	"strings"
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/gpx"
	"github.com/wncservices/domestique/apps/api/internal/model"
)

// fakeCourses records what reached the provider.
type fakeCourses struct {
	imported  [][]byte
	filenames []string
	deleted   []string

	importID  string
	importErr error
	deleteErr error
}

func (f *fakeCourses) ImportCourse(filename string, data []byte) (string, error) {
	f.filenames = append(f.filenames, filename)
	f.imported = append(f.imported, data)
	if f.importErr != nil {
		return "", f.importErr
	}
	if f.importID != "" {
		return f.importID, nil
	}
	return "course-1", nil
}

func (f *fakeCourses) DeleteCourse(id string) error {
	f.deleted = append(f.deleted, id)
	return f.deleteErr
}

func aTrack() []gpx.Point {
	return []gpx.Point{
		{Lat: 50.85, Lon: 4.35, Ele: 20},
		{Lat: 50.86, Lon: 4.36, Ele: 25},
		{Lat: 50.87, Lon: 4.37, Ele: 30},
	}
}

func aGarmin(courses *fakeCourses) *Garmin {
	return &Garmin{
		Account: model.Account{ID: "garmin:wilant", Provider: model.ProviderGarmin, Rider: "wilant"},
		Track:   func(string) ([]gpx.Point, error) { return aTrack(), nil },
		Courses: func(string) (Courses, error) { return courses, nil },
	}
}

func aRoute() model.Route {
	r := model.Route{Slug: "kluisbergen"}
	r.Name = "Kluisbergen"
	return r
}

// A course reaches Garmin as FIT, not GPX: a GPX course navigates as a
// breadcrumb line with nothing said at a junction.
func TestCreateUploadsAFITCourse(t *testing.T) {
	courses := &fakeCourses{}
	id, err := aGarmin(courses).Create(aRoute())
	if err != nil {
		t.Fatal(err)
	}
	if id != "course-1" {
		t.Errorf("id = %q, want the provider's", id)
	}
	if len(courses.imported) != 1 {
		t.Fatalf("uploaded %d files, want 1", len(courses.imported))
	}
	// FIT files carry ".FIT" in the header's data type field.
	if !strings.Contains(string(courses.imported[0][:12]), ".FIT") {
		t.Errorf("what was uploaded is not a FIT file: %q", courses.imported[0][:12])
	}
	if !strings.HasSuffix(courses.filenames[0], ".fit") {
		t.Errorf("filename = %q", courses.filenames[0])
	}
}

// Connect has no replace, so an update is an import followed by a delete —
// in that order. Importing first means a failure leaves the rider with the
// course they already had.
func TestUpdateImportsBeforeDeleting(t *testing.T) {
	courses := &fakeCourses{importID: "course-2"}
	id, err := aGarmin(courses).Update("course-1", aRoute())
	if err != nil {
		t.Fatal(err)
	}
	if id != "course-2" {
		t.Errorf("id = %q, want the new course", id)
	}
	if len(courses.imported) != 1 || len(courses.deleted) != 1 {
		t.Fatalf("imported %d, deleted %d, want 1 each", len(courses.imported), len(courses.deleted))
	}
	if courses.deleted[0] != "course-1" {
		t.Errorf("deleted %q, want the old course", courses.deleted[0])
	}
}

// A failed import must not delete anything: the rider keeps the course they
// have, and the unchanged state means the next push tries again.
func TestUpdateKeepsTheOldCourseWhenTheImportFails(t *testing.T) {
	courses := &fakeCourses{importErr: errors.New("garmin: 503")}
	if _, err := aGarmin(courses).Update("course-1", aRoute()); err == nil {
		t.Fatal("a failed import reported success")
	}
	if len(courses.deleted) != 0 {
		t.Errorf("deleted %v after a failed import", courses.deleted)
	}
}

// If the delete fails after a good import, the new id still has to be
// returned: state has to move on, or every later push imports another copy.
// One stale course is the cost, and it is logged.
func TestUpdateReturnsTheNewIDEvenIfTheOldOneSurvives(t *testing.T) {
	courses := &fakeCourses{importID: "course-2", deleteErr: errors.New("garmin: 500")}

	var logged bool
	g := aGarmin(courses)
	g.Log = func(string, ...any) { logged = true }

	id, err := g.Update("course-1", aRoute())
	if err != nil {
		t.Fatalf("a surviving old course failed the push: %v", err)
	}
	if id != "course-2" {
		t.Errorf("id = %q, want the new course", id)
	}
	if !logged {
		t.Error("the stale course was left behind silently")
	}
}

// A rider who has not connected gets a sentence they can act on, not a nil
// dereference and not a provider error.
func TestWithoutASessionTheErrorSaysWhatToDo(t *testing.T) {
	g := &Garmin{Account: model.Account{Rider: "wilant"}}
	_, err := g.Create(aRoute())
	if err == nil || !strings.Contains(err.Error(), "Settings") {
		t.Errorf("err = %v, want it to point at Settings", err)
	}
}

// One account's failure is that account's. Resolving the client per push is
// what keeps a disconnected rider from failing everybody else's.
func TestClientErrorsAreReturnedNotPanicked(t *testing.T) {
	g := aGarmin(&fakeCourses{})
	g.Courses = func(string) (Courses, error) { return nil, errors.New("expired") }

	if _, err := g.Create(aRoute()); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("err = %v, want the resolver's", err)
	}
}

// A track that cannot be read is not a course worth uploading.
func TestATrackThatCannotBeReadFailsThePush(t *testing.T) {
	g := aGarmin(&fakeCourses{})
	g.Track = func(string) ([]gpx.Point, error) { return nil, errors.New("no such route") }

	if _, err := g.Create(aRoute()); err == nil {
		t.Fatal("an unreadable track reported success")
	}
}
