package targets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wncservices/domestique/apps/api/internal/fitcourse"
	"github.com/wncservices/domestique/apps/api/internal/gpx"
	"github.com/wncservices/domestique/apps/api/internal/model"
)

// WahooRoute is what Create/Update need to build the Wahoo API request.
// Wahoo's Cloud API takes structured fields alongside the FIT file
// (external_id, provider_updated_at, distance, ascent, ...) rather than
// Garmin's plain filename+bytes import, so the adapter builds this itself
// from the route's own metadata and stats — the resolved client stays as
// decoupled from model.Route as Courses keeps internal/garmin, it only ever
// sees this struct.
type WahooRoute struct {
	ExternalID  string
	Name        string
	Description string
	UpdatedAt   time.Time
	DistanceM   float64
	AscentM     float64
	StartLat    float64
	StartLng    float64
	Filename    string
	FIT         []byte
	// Sport is model.Sport's plain-string value — see wahoo.RouteRequest's
	// own field of the same name for what it decides on Wahoo's side.
	Sport string
}

// WahooRoutes is the slice of a Wahoo client a push needs.
//
// Wahoo's Cloud API is a real create/update/delete resource
// (POST/PUT/DELETE /v1/routes) with a response id, unlike Garmin's
// import-only course service — hence a richer interface than Courses, not a
// simplification of it.
type WahooRoutes interface {
	// CreateRoute pushes a new route and returns the provider's id.
	CreateRoute(ctx context.Context, route WahooRoute) (string, error)
	// UpdateRoute replaces an existing route's file and metadata in place —
	// a real PUT, not an import-then-delete dance.
	UpdateRoute(ctx context.Context, id string, route WahooRoute) (string, error)
	// DeleteRoute removes a route from the account.
	DeleteRoute(ctx context.Context, id string) error
}

// Wahoo pushes routes to one rider's Wahoo Cloud API account.
//
// FIT rather than GPX, for the same reason Garmin gets one: Wahoo's own API
// documents route[file] as "Base64 encoded FIT file" and will not take
// anything else.
type Wahoo struct {
	Account model.Account

	// Track returns the route's points. The adapter builds the file itself,
	// same reasoning as Garmin's own Track field.
	Track func(ctx context.Context, slug string) ([]gpx.Point, error)
	// Routes resolves the signed-in Wahoo client for a rider. Takes a
	// context, unlike Garmin's Courses field: resolving a Wahoo session can
	// mean refreshing an expired access token, a real network call this
	// factory function makes on the caller's behalf.
	Routes func(ctx context.Context, rider string) (WahooRoutes, error)
	// Log receives what is worth knowing and not worth failing over. Nil is
	// fine.
	Log func(msg string, args ...any)
}

var errWahooNotWired = errors.New(
	"wahoo push needs a connected account: connect Wahoo in Settings")

// Create pushes the route as a new Wahoo route.
func (w *Wahoo) Create(ctx context.Context, route model.Route) (string, error) {
	client, req, err := w.prepare(ctx, route)
	if err != nil {
		return "", err
	}
	return client.CreateRoute(ctx, req)
}

// Update replaces the route's file and metadata on Wahoo.
func (w *Wahoo) Update(ctx context.Context, remoteID string, route model.Route) (string, error) {
	client, req, err := w.prepare(ctx, route)
	if err != nil {
		return "", err
	}
	return client.UpdateRoute(ctx, remoteID, req)
}

// Delete removes the route from the account.
func (w *Wahoo) Delete(ctx context.Context, remoteID string) error {
	client, err := w.client(ctx)
	if err != nil {
		return err
	}
	return client.DeleteRoute(ctx, remoteID)
}

// prepare resolves the client and renders the route as a Wahoo route
// request — the FIT file plus the metadata Wahoo requires alongside it.
func (w *Wahoo) prepare(ctx context.Context, route model.Route) (WahooRoutes, WahooRoute, error) {
	client, err := w.client(ctx)
	if err != nil {
		return nil, WahooRoute{}, err
	}
	if w.Track == nil {
		return nil, WahooRoute{}, errWahooNotWired
	}

	points, err := w.Track(ctx, route.Slug)
	if err != nil {
		return nil, WahooRoute{}, fmt.Errorf("reading the track for %s: %w", route.Slug, err)
	}

	fit, err := fitcourse.Encode(points, fitcourse.Options{
		Name:  route.Name,
		Sport: fitcourse.SportFromString(string(route.EffectiveSport())),
	})
	if err != nil {
		return nil, WahooRoute{}, fmt.Errorf("building a course file for %s: %w", route.Slug, err)
	}

	// UpdatedAt is what Wahoo calls provider_updated_at: when this route
	// last changed on our side, since we are the "external" system from
	// Wahoo's point of view. A route with no parseable timestamp still has
	// to push with something, so this falls back to now rather than
	// failing the push over a display field.
	updatedAt := time.Now().UTC()
	if parsed, err := time.Parse(time.RFC3339, route.UpdatedAt); err == nil {
		updatedAt = parsed
	}

	return client, WahooRoute{
		// external_id is the library slug, so a rebuilt state file can be
		// reconciled against what the account already holds.
		ExternalID:  route.Slug,
		Name:        route.Name,
		Description: route.Description,
		UpdatedAt:   updatedAt,
		DistanceM:   route.Stats.DistanceM,
		AscentM:     route.Stats.AscentM,
		StartLat:    route.Stats.StartLat,
		StartLng:    route.Stats.StartLng,
		Filename:    wahooFilename(route),
		FIT:         fit,
		Sport:       string(route.EffectiveSport()),
	}, nil
}

func (w *Wahoo) client(ctx context.Context) (WahooRoutes, error) {
	if w.Routes == nil {
		return nil, errWahooNotWired
	}
	client, err := w.Routes(ctx, w.Account.Rider)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errWahooNotWired
	}
	return client, nil
}

func wahooFilename(route model.Route) string {
	if route.Slug == "" {
		return "route.fit"
	}
	return route.Slug + ".fit"
}
