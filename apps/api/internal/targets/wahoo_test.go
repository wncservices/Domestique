package targets

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wncservices/domestique/apps/api/internal/gpx"
	"github.com/wncservices/domestique/apps/api/internal/model"
)

// fakeWahooRoutes records what reached the provider.
type fakeWahooRoutes struct {
	created []WahooRoute
	updated []WahooRoute
	deleted []string

	createID  string
	updateID  string
	createErr error
	updateErr error
	deleteErr error
}

func (f *fakeWahooRoutes) CreateRoute(_ context.Context, route WahooRoute) (string, error) {
	f.created = append(f.created, route)
	if f.createErr != nil {
		return "", f.createErr
	}
	if f.createID != "" {
		return f.createID, nil
	}
	return "route-1", nil
}

func (f *fakeWahooRoutes) UpdateRoute(_ context.Context, id string, route WahooRoute) (string, error) {
	f.updated = append(f.updated, route)
	if f.updateErr != nil {
		return "", f.updateErr
	}
	if f.updateID != "" {
		return f.updateID, nil
	}
	return id, nil
}

func (f *fakeWahooRoutes) DeleteRoute(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return f.deleteErr
}

func aWahoo(routes *fakeWahooRoutes) *Wahoo {
	return &Wahoo{
		Account: model.Account{ID: "wahoo:wilant", Provider: model.ProviderWahoo, Rider: "wilant"},
		Track:   func(context.Context, string) ([]gpx.Point, error) { return aTrack(), nil },
		Routes:  func(context.Context, string) (WahooRoutes, error) { return routes, nil },
	}
}

func aWahooRoute() model.Route {
	r := model.Route{Slug: "kluisbergen"}
	r.Name = "Kluisbergen"
	r.Description = "A short climb"
	r.UpdatedAt = "2026-01-02T03:04:05Z"
	r.Stats.DistanceM = 12345
	r.Stats.AscentM = 250
	r.Stats.StartLat = 50.85
	r.Stats.StartLng = 4.35
	return r
}

func TestWahooCreatePushesAFITRouteWithMetadata(t *testing.T) {
	routes := &fakeWahooRoutes{}
	id, err := aWahoo(routes).Create(t.Context(), aWahooRoute())
	if err != nil {
		t.Fatal(err)
	}
	if id != "route-1" {
		t.Errorf("id = %q, want the provider's", id)
	}
	if len(routes.created) != 1 {
		t.Fatalf("created %d routes, want 1", len(routes.created))
	}

	got := routes.created[0]
	if got.ExternalID != "kluisbergen" {
		t.Errorf("external id = %q, want the route's slug", got.ExternalID)
	}
	if got.Name != "Kluisbergen" || got.Description != "A short climb" {
		t.Errorf("name/description = %q/%q", got.Name, got.Description)
	}
	if got.DistanceM != 12345 || got.AscentM != 250 {
		t.Errorf("distance/ascent = %v/%v", got.DistanceM, got.AscentM)
	}
	if got.StartLat != 50.85 || got.StartLng != 4.35 {
		t.Errorf("start lat/lng = %v/%v", got.StartLat, got.StartLng)
	}
	want, _ := time.Parse(time.RFC3339, "2026-01-02T03:04:05Z")
	if !got.UpdatedAt.Equal(want) {
		t.Errorf("updatedAt = %v, want %v (parsed from route.UpdatedAt)", got.UpdatedAt, want)
	}
	if !strings.HasSuffix(got.Filename, ".fit") {
		t.Errorf("filename = %q", got.Filename)
	}
	// FIT files carry ".FIT" in the header's data type field — same check
	// garmin_test.go uses to confirm this isn't a bare GPX.
	if !strings.Contains(string(got.FIT[:12]), ".FIT") {
		t.Errorf("what was built is not a FIT file: %q", got.FIT[:12])
	}
}

func TestWahooCreateFallsBackToNowForAnUnparseableTimestamp(t *testing.T) {
	routes := &fakeWahooRoutes{}
	route := aWahooRoute()
	route.UpdatedAt = "not-a-timestamp"

	before := time.Now().UTC()
	if _, err := aWahoo(routes).Create(t.Context(), route); err != nil {
		t.Fatal(err)
	}
	got := routes.created[0].UpdatedAt
	if got.Before(before) {
		t.Errorf("updatedAt = %v, want a fallback to roughly now (after %v)", got, before)
	}
}

// Wahoo's API is a real REST resource — Update is one PUT, not Garmin's
// import-then-delete dance.
func TestWahooUpdateIsOnePUTNoDelete(t *testing.T) {
	routes := &fakeWahooRoutes{updateID: "route-1"}
	id, err := aWahoo(routes).Update(t.Context(), "route-1", aWahooRoute())
	if err != nil {
		t.Fatal(err)
	}
	if id != "route-1" {
		t.Errorf("id = %q", id)
	}
	if len(routes.updated) != 1 {
		t.Fatalf("updated %d routes, want 1", len(routes.updated))
	}
	if len(routes.deleted) != 0 {
		t.Errorf("deleted %d routes, want 0 — Wahoo has a real PUT", len(routes.deleted))
	}
}

func TestWahooDelete(t *testing.T) {
	routes := &fakeWahooRoutes{}
	if err := aWahoo(routes).Delete(t.Context(), "route-1"); err != nil {
		t.Fatal(err)
	}
	if len(routes.deleted) != 1 || routes.deleted[0] != "route-1" {
		t.Errorf("deleted = %v, want [route-1]", routes.deleted)
	}
}

func TestWahooCreateSurfacesAResolverFailure(t *testing.T) {
	w := &Wahoo{
		Account: model.Account{Rider: "wilant"},
		Track:   func(context.Context, string) ([]gpx.Point, error) { return aTrack(), nil },
		Routes:  func(context.Context, string) (WahooRoutes, error) { return nil, errWahooNotWired },
	}
	if _, err := w.Create(t.Context(), aWahooRoute()); err == nil {
		t.Fatal("expected an error when Routes fails to resolve")
	}
}

func TestWahooNotWiredWithoutARoutesResolver(t *testing.T) {
	w := &Wahoo{Account: model.Account{Rider: "wilant"}}
	if _, err := w.Create(t.Context(), aWahooRoute()); err == nil {
		t.Fatal("expected an error with no Routes resolver at all")
	}
}
