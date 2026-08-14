package garmin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
)

// coursePath is the course service. Connect's own Training → Courses → Import
// reaches it as `https://connect.garmin.com/gc-api/course-service/course`; the
// `/gc-api` prefix is the web app's proxy onto the same service, and with an
// OAuth2 bearer the service is reached directly on connectapi.
//
// Importing is **two calls**, which is not obvious and cost a full library's
// worth of failed pushes to discover:
//
//  1. POST /import with the file. This only *parses* it. The response is a
//     course object with the name read out of the file and `courseId: null` —
//     nothing has been saved, and the 200 says nothing about whether it will
//     be.
//  2. POST that object back to the service. This is what creates the course,
//     and this is the response that carries the id.
//
// Undocumented and liable to move. When it does, the failure is one push
// returning an error — the route stays in the library and nothing else stops.
const coursePath = "/course-service/course"

// ImportCourse uploads a course file and returns Garmin's id for it.
//
// The file may be FIT, GPX or TCX as far as Connect is concerned. Domestique
// sends FIT: a GPX navigates as a breadcrumb line with nothing said at a
// junction, and a FIT course can carry turn cues.
func (c *Client) ImportCourse(filename string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("garmin: refusing to upload an empty course")
	}
	if filename == "" {
		filename = "course.fit"
	}

	bearer, err := c.bearerToken()
	if err != nil {
		return "", err
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := form.Close(); err != nil {
		return "", err
	}

	raw, status, err := c.do(http.MethodPost, c.APIBase+coursePath+"/import", &body,
		form.FormDataContentType(),
		header{"Authorization", "Bearer " + bearer},
		// Connect's services answer HTML to a browser-shaped request and JSON
		// to this one; both headers are what its own web app sends.
		header{"Accept", "application/json"},
		header{"X-Requested-With", "XMLHttpRequest"},
	)
	if err != nil {
		return "", err
	}

	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return "", errors.New("garmin: the session was refused — sign in again")
	case status >= 300:
		return "", fmt.Errorf("garmin: course import returned %d: %s", status, snippet(raw))
	}

	// A parse that already carries an id would mean Connect had folded the
	// two calls into one. It does not today, but taking the id when it is
	// there costs nothing and means this keeps working if that changes.
	if id, err := courseID(raw); err == nil {
		return id, nil
	}
	return c.saveCourse(raw)
}

// saveCourse turns a parsed course into a saved one.
//
// The body is what /import handed back with one addition: /import never sets
// coursePrivacy, but the save endpoint rejects the DTO without a valid value.
func (c *Client) saveCourse(parsed []byte) (string, error) {
	bearer, err := c.bearerToken()
	if err != nil {
		return "", err
	}

	body, err := withPrivacy(parsed)
	if err != nil {
		return "", err
	}

	raw, status, err := c.do(http.MethodPost, c.APIBase+coursePath, bytes.NewReader(body),
		"application/json",
		header{"Authorization", "Bearer " + bearer},
		header{"Accept", "application/json"},
		header{"X-Requested-With", "XMLHttpRequest"},
	)
	if err != nil {
		return "", err
	}

	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return "", errors.New("garmin: the session was refused — sign in again")
	case status >= 300:
		// Not snippet(): this body is a Bean Validation dump that names every
		// field of the DTO, which is the only description of that shape that
		// exists anywhere. Truncating it at 200 characters is what made the
		// privacy field a guess rather than a lookup.
		return "", fmt.Errorf("garmin: saving the course returned %d: %s", status, longSnippet(raw))
	}

	id, err := courseID(raw)
	if err != nil {
		return "", fmt.Errorf("garmin: the course was saved but no id came back: %w", err)
	}
	return id, nil
}

// DeleteCourse removes a course from the account.
func (c *Client) DeleteCourse(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("garmin: no course id to delete")
	}

	bearer, err := c.bearerToken()
	if err != nil {
		return err
	}

	raw, status, err := c.do(http.MethodDelete, c.APIBase+coursePath+"/"+id, nil, "",
		header{"Authorization", "Bearer " + bearer},
		header{"Accept", "application/json"},
		header{"X-Requested-With", "XMLHttpRequest"},
	)
	if err != nil {
		return err
	}

	switch {
	case status == http.StatusNotFound:
		// Already gone is the outcome the caller wanted.
		return nil
	case status >= 300:
		return fmt.Errorf("garmin: deleting course %s returned %d: %s", id, status, snippet(raw))
	}
	return nil
}

// privacyPrivate is Garmin's id for a course only its owner can see.
//
// The service enumerates 1=Public, 2=Private, 4=Group. Private is the right
// default for somebody else's route library: a push should not publish a
// rider's routes, and making a private course public later is a click, while
// the reverse is a course strangers already saw.
const privacyPrivate = 2

// privacyFields are the names the DTO might carry the privacy under.
//
// Only one of these is real, and it is not yet known which: the service is
// undocumented, `coursePrivacy` alone was rejected, and the 400 reports a
// class-level constraint that names the whole DTO rather than the field.
// Setting all of them is safe — the same rejection proved unknown fields are
// ignored rather than refused — and it is one deploy instead of three.
//
// **When a push succeeds, come back and cut this to the one that worked.**
// A list of guesses is a reasonable way to find an answer and a bad thing to
// leave in place: the next person cannot tell which name matters.
var privacyFields = []string{
	// Nested {typeId, typeKey}, the shape Connect uses for activity privacy.
	"privacyRule",
	"coursePrivacyRule",
	// Plain ids.
	"coursePrivacy",
	"privacyType",
}

// withPrivacy gives the parsed DTO a privacy the save endpoint will accept.
//
// /import never sets one, and the save rejects the course without it:
//
//	'createCourse.arg3' Course privacy can be 1-Public, 2-Private or 4-Group
func withPrivacy(parsed []byte) ([]byte, error) {
	var dto map[string]any
	if err := json.Unmarshal(parsed, &dto); err != nil {
		return nil, fmt.Errorf("garmin: unreadable course response: %s", snippet(parsed))
	}

	nested := map[string]any{"typeId": privacyPrivate, "typeKey": "private"}
	for _, field := range privacyFields {
		if valid(dto[field]) {
			// Connect already said what it wants; do not argue with it.
			continue
		}
		switch field {
		case "privacyRule", "coursePrivacyRule":
			dto[field] = nested
		default:
			dto[field] = privacyPrivate
		}
	}

	out, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("garmin: could not re-encode course: %w", err)
	}
	return out, nil
}

// valid reports whether a privacy value is already one the service accepts,
// in either the plain or the nested shape.
func valid(value any) bool {
	switch v := value.(type) {
	case float64:
		return v == 1 || v == 2 || v == 4
	case map[string]any:
		id, ok := v["typeId"].(float64)
		return ok && (id == 1 || id == 2 || id == 4)
	}
	return false
}

// courseID digs the new course's id out of the response.
//
// The response shape is not documented and has more than one plausible
// spelling, so rather than pin one and break on the next deploy, look for the
// ones Connect is known to use and say clearly when none is present.
func courseID(raw []byte) (string, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", fmt.Errorf("garmin: unreadable course response: %s", snippet(raw))
	}

	for _, key := range []string{"courseId", "id", "coursePk"} {
		switch value := body[key].(type) {
		case string:
			if value != "" {
				return value, nil
			}
		case float64:
			// Every JSON number decodes as float64; course ids are integers.
			return fmt.Sprintf("%.0f", value), nil
		}
	}
	return "", fmt.Errorf("garmin: the course was accepted but the response named no id: %s", snippet(raw))
}

// longSnippet keeps far more of a body than snippet, for the one case where
// the body is the documentation: a validation failure from an undocumented
// service lists the fields it was given, and nothing else does.
func longSnippet(raw []byte) string {
	const limit = 2000
	text := strings.TrimSpace(string(raw))
	if len(text) > limit {
		return text[:limit] + "…"
	}
	return text
}

// snippet keeps an error readable when the body is an HTML error page.
func snippet(raw []byte) string {
	const limit = 200
	text := strings.TrimSpace(string(raw))
	if len(text) > limit {
		return text[:limit] + "…"
	}
	return text
}
