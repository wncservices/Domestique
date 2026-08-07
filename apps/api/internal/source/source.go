// Package source is where routes come from.
//
// The app is generic; the routes are personal data. A source is the seam
// between them, so this repository can be open source while the routes live
// somewhere private — a separate git repo checked out next to it, or a
// database that accepts uploads.
//
// Two implementations ship today:
//
//	FS  a directory of GPX files, typically a checkout of a private routes repo.
//	    Read-only: routes are added by committing them.
//	DB  GPX blobs in a database, written through the UI. Read-write.
package source

import (
	"errors"

	"github.com/wncservices/domestique/apps/api/internal/gpx"
	"github.com/wncservices/domestique/apps/api/internal/model"
)

// ErrNotFound is returned for a slug the source does not hold.
var ErrNotFound = errors.New("no such route")

// Source provides routes to sync.
type Source interface {
	// Describe names the source for humans, e.g. "directory ./routes".
	Describe() string
	// List returns every enabled route, plus non-fatal problems: a broken
	// route is reported and skipped so it cannot block the others.
	List() ([]model.Route, []string, error)
	// Track returns the points of one route, for previewing.
	Track(slug string) ([]gpx.Point, error)
	// GPX returns the raw file, for pushing to a provider or downloading.
	GPX(slug string) ([]byte, error)
}

// Writable is implemented by sources that accept uploads. The FS source
// deliberately does not: in a git-backed library, adding a route is a commit.
type Writable interface {
	Source
	// Create stores a new route and returns it. Name may be empty, in which
	// case the source derives one from the GPX or the filename.
	Create(req CreateRequest) (model.Route, error)
	// Update replaces a route's metadata, and its track when GPX is non-nil.
	Update(slug string, req UpdateRequest) (model.Route, error)
	// Delete removes a route.
	Delete(slug string) error
}

// CreateRequest is an upload.
type CreateRequest struct {
	// Filename is the uploaded file's name, used to derive a slug and a
	// fallback title. Optional.
	Filename string
	Name     string
	Descript string
	Tags     []string
	Targets  *[]string
	GPX      []byte
	// UploadedBy records which rider added the route. Display only.
	UploadedBy string
}

// UpdateRequest edits an existing route. Nil fields are left alone.
type UpdateRequest struct {
	Name     *string
	Descript *string
	Tags     *[]string
	Targets  *[]string
	Enabled  *bool
	// GPX replaces the track when non-nil.
	GPX []byte
}

// AsWritable returns the source as a Writable, or false if it is read-only.
func AsWritable(s Source) (Writable, bool) {
	w, ok := s.(Writable)
	return w, ok
}
