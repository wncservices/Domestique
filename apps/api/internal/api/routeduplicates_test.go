package api

import (
	"testing"

	"github.com/wncservices/domestique/apps/api/internal/model"
)

func routeWith(name string, distanceM float64) model.Route {
	return model.Route{
		RouteMeta: model.RouteMeta{Name: name},
		Slug:      name,
		Stats:     model.RouteStats{DistanceM: distanceM},
	}
}

func identityDTO(rt model.Route) routeDTO { return routeDTO{Slug: rt.Slug, Name: rt.Name} }

// The real shape this was built for: the same real ride imported more than
// once — Garmin sync-back run before sync_state was recorded correctly, an
// identity split re-creating a "new" account Garmin already had a course
// from, a plain re-upload. Distance alone, not content_hash: Garmin
// re-encodes a GPX slightly differently on every download, so the same
// ride imported twice from Garmin does not reliably hash the same even
// though its name and distance do.
func TestGroupDuplicateRoutesBySameNameAndDistance(t *testing.T) {
	routes := []model.Route{
		routeWith("Kemmelberg Loop", 42000),
		// Same name, distance within tolerance, not identical — a
		// re-encoded copy of the same ride, not a coincidence.
		routeWith("Kemmelberg Loop", 42050),
		// Unrelated route: no repeat, must not appear in any group.
		routeWith("Flat Coast Ride", 30000),
		// Same name as the pair above, genuinely different distance — a
		// coincidence of naming, not a duplicate.
		routeWith("Kemmelberg Loop", 12000),
	}

	groups := groupDuplicateRoutes(routes, identityDTO)
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want exactly one (the two 42km Kemmelberg routes)", groups)
	}
	if len(groups[0].Routes) != 2 {
		t.Errorf("group members = %+v, want 2", groups[0].Routes)
	}
}

func TestGroupDuplicateRoutesOmitsGroupsOfOne(t *testing.T) {
	routes := []model.Route{
		routeWith("Solo Ride", 20000),
		routeWith("Another Solo Ride", 25000),
	}
	if groups := groupDuplicateRoutes(routes, identityDTO); len(groups) != 0 {
		t.Errorf("groups = %+v, want none — nothing here repeats", groups)
	}
}

func TestGroupDuplicateRoutesIsCaseInsensitiveOnName(t *testing.T) {
	routes := []model.Route{
		routeWith("Kemmelberg Loop", 42000),
		routeWith("KEMMELBERG LOOP", 42010),
	}
	if groups := groupDuplicateRoutes(routes, identityDTO); len(groups) != 1 {
		t.Errorf("groups = %+v, want the case-differing pair grouped together", groups)
	}
}
