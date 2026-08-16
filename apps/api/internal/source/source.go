// Package source is the route library: routes as rows, the GPX itself as a
// blob in the row.
//
// There is one implementation, and deliberately so. An earlier version could
// also read a directory of GPX files kept under git, which sounded appealing —
// review and history for free — but it meant a second storage model that could
// not do half of what the database one could: no uploads, no Komoot import,
// nowhere to link a head unit, nowhere to keep sync state. Every feature had
// to ask which kind of library it was talking to, and the answer decided
// whether the feature existed.
//
// So: a database, PostgreSQL or SQLite. Routes get in by upload or import.
package source

import (
	"context"
	"errors"

	"github.com/wncservices/domestique/apps/api/internal/gpx"
	"github.com/wncservices/domestique/apps/api/internal/model"
)

// ErrNotFound is returned for a slug the library does not hold.
var ErrNotFound = errors.New("no such route")

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
	// UploadedBy records which rider added the route.
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

// Library is what the rest of the app talks to. The one implementation is DB;
// the interface exists so tests can substitute something simpler.
type Library interface {
	Describe() string
	List(ctx context.Context) ([]model.Route, []string, error)
	Track(ctx context.Context, slug string) ([]gpx.Point, error)
	GPX(ctx context.Context, slug string) ([]byte, error)
	Create(ctx context.Context, req CreateRequest) (model.Route, error)
	Update(ctx context.Context, slug string, req UpdateRequest) (model.Route, error)
	Delete(ctx context.Context, slug string) error
}
