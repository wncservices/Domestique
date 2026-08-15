package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"

	"github.com/wncservices/domestique/apps/api/internal/accounts"
	"github.com/wncservices/domestique/apps/api/internal/auth"
	"github.com/wncservices/domestique/apps/api/internal/garmin"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/source"
)

type garminCourseDTO struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	DistanceM    float64 `json:"distanceM"`
	AscentM      float64 `json:"ascentM"`
	ActivityType string  `json:"activityType"`
	CreatedAt    string  `json:"createdAt,omitempty"`
	// Imported is true when this exact course is already tracked as
	// something this app pushed to this account (sync_state.remote_id) —
	// the same certainty Komoot's tag-based check has.
	Imported bool `json:"imported"`
	// PossibleDuplicate names a library route that looks like the same ride
	// by distance and start point, when one exists. A hint, not a
	// certainty: Garmin re-encodes track points its own way, so an exact
	// content match is not realistic here the way Komoot's tag check is —
	// the rider decides, this only flags it worth a look.
	PossibleDuplicate string `json:"possibleDuplicate,omitempty"`
}

type garminCourseImportResult struct {
	Imported []string          `json:"imported"`
	Skipped  map[string]string `json:"skipped"`
}

// garminSessionFor reads and decodes the caller's own stored Garmin session.
// ok is false whenever there is simply nothing to act on — not connected, an
// unreadable session — which every caller here treats the same way Devices
// already does: an empty result, not an error screen.
func (s *Server) garminSessionFor(r *http.Request) (rider string, session garmin.Session, ok bool) {
	if s.Garmin == nil || s.Links == nil {
		return "", garmin.Session{}, false
	}
	rider = auth.FromContext(r.Context()).User
	if rider == "" {
		return "", garmin.Session{}, false
	}
	_, secret, err := s.Links.Secret(garminProvider, rider)
	if err != nil {
		return "", garmin.Session{}, false
	}
	if err := json.Unmarshal([]byte(secret), &session); err != nil {
		s.logger().Warn("stored garmin session is unreadable", "rider", rider, "err", err)
		return "", garmin.Session{}, false
	}
	return rider, session, true
}

// handleGarminCourseList lists what is already on the caller's Garmin
// account, for syncing back into the library and spotting duplicates —
// distinct from handleGarminDevices, which lists head units, not routes.
func (s *Server) handleGarminCourseList(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermGarminSync) {
		return
	}

	rider, session, ok := s.garminSessionFor(r)
	if !ok {
		writeJSON(w, http.StatusOK, []garminCourseDTO{})
		return
	}

	consumer, _ := s.garminConsumer()
	courses, err := s.Garmin.ListCourses(consumer, session)
	if err != nil {
		s.logger().Warn("garmin course list failed", "rider", rider, "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "Garmin would not list the courses on this account just now.",
		})
		return
	}

	routes, _, err := s.Source.List()
	if err != nil {
		s.fail(w, err)
		return
	}
	tracked := s.garminTrackedRemoteIDs(rider)

	out := make([]garminCourseDTO, 0, len(courses))
	for _, c := range courses {
		out = append(out, garminCourseDTO{
			ID: c.ID, Name: c.Name, DistanceM: c.DistanceM, AscentM: c.AscentM,
			ActivityType:      c.ActivityType,
			CreatedAt:         formatTime(c.CreatedAt),
			Imported:          tracked[c.ID],
			PossibleDuplicate: possibleDuplicateOf(c, routes),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// garminTrackedRemoteIDs is the set of Garmin course ids this app has
// already recorded pushing to the caller's own linked account — the
// exact-match half of dedup, free of any heuristic.
func (s *Server) garminTrackedRemoteIDs(rider string) map[string]bool {
	out := map[string]bool{}
	entries, err := s.Store.ForAccount(accounts.ID(model.ProviderGarmin, rider))
	if err != nil {
		// Not fatal to listing: worst case some already-tracked courses show
		// up without the Imported flag, which just makes them look like
		// ordinary un-imported ones.
		s.logger().Warn("could not read garmin sync state for dedup", "rider", rider, "err", err)
		return out
	}
	for _, e := range entries {
		if e.RemoteID != "" {
			out[e.RemoteID] = true
		}
	}
	return out
}

// possibleDuplicateOf flags a library route that looks like the same ride as
// a Garmin course — distance within 2% (or 100m, whichever is more
// forgiving) and a start point within roughly 100m. Both loose on purpose:
// this is a hint for a rider to glance at, not a filter that silently hides
// anything, and Garmin's own re-encoding of a track's points means an exact
// match is never the realistic bar here.
func possibleDuplicateOf(course garmin.Course, routes []model.Route) string {
	const startPointToleranceDeg = 0.001 // roughly 100m at these latitudes
	for _, route := range routes {
		distDelta := math.Abs(route.Stats.DistanceM - course.DistanceM)
		distOK := distDelta <= 100 || distDelta <= route.Stats.DistanceM*0.02
		latOK := math.Abs(route.Stats.StartLat-course.StartLat) <= startPointToleranceDeg
		lngOK := math.Abs(route.Stats.StartLng-course.StartLng) <= startPointToleranceDeg
		if distOK && latOK && lngOK {
			return fmt.Sprintf("looks like %q already in the library (similar distance and start point)", route.Name)
		}
	}
	return ""
}

// handleGarminCourseImport pulls selected Garmin courses into the library.
func (s *Server) handleGarminCourseImport(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermGarminSync) {
		return
	}

	var body struct {
		CourseIDs []string `json:"courseIds"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(body.CourseIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no courseIds given"})
		return
	}

	identity := auth.FromContext(r.Context())
	rider, session, ok := s.garminSessionFor(r)
	if !ok {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{
			"error": "Not connected to Garmin — connect your account in Settings",
		})
		return
	}
	consumer, _ := s.garminConsumer()

	// Re-listed rather than trusting names from the request body: the
	// courses' own names are the authoritative ones, the same reason
	// handleKomootImport re-fetches tours rather than trusting the client.
	courses, err := s.Garmin.ListCourses(consumer, session)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "Garmin would not list the courses on this account just now.",
		})
		return
	}
	byID := map[string]garmin.Course{}
	for _, c := range courses {
		byID[c.ID] = c
	}

	result := garminCourseImportResult{Imported: []string{}, Skipped: map[string]string{}}

	wanted := make([]string, 0, len(body.CourseIDs))
	for _, id := range body.CourseIDs {
		if _, ok := byID[id]; !ok {
			result.Skipped[id] = "not on this Garmin account"
			continue
		}
		wanted = append(wanted, id)
	}

	// Downloaded a few at a time, same reasoning and the same bound as
	// Komoot's import: sequential would mean thirty waits end to end with
	// no response byte written until the last one finished, and this is
	// somebody's personal account on an undocumented API, not a service to
	// saturate.
	const parallel = 4
	downloads := fetchGPX(s.Garmin, consumer, session, wanted, parallel)

	for _, id := range wanted {
		got := downloads[id]
		if got.err != nil {
			result.Skipped[id] = got.err.Error()
			continue
		}
		course := byID[id]

		if _, err := s.Source.Create(source.CreateRequest{
			Filename:   course.Name + ".gpx",
			Name:       course.Name,
			Descript:   fmt.Sprintf("Synced back from Garmin (course %s)", id),
			UploadedBy: identity.User,
			GPX:        got.gpx,
		}); err != nil {
			result.Skipped[id] = err.Error()
			continue
		}
		result.Imported = append(result.Imported, id)
	}

	s.logger().Info("garmin course sync-back finished",
		"user", identity.User, "rider", rider, "imported", len(result.Imported), "skipped", len(result.Skipped))
	writeJSON(w, http.StatusOK, result)
}

type gpxDownload struct {
	gpx []byte
	err error
}

// fetchGPX downloads several courses at once, bounded by parallel — same
// shape as komoot.go's fetchTours.
func fetchGPX(connector GarminConnector, consumer GarminConsumer, session garmin.Session, ids []string, parallel int) map[string]gpxDownload {
	out := make(map[string]gpxDownload, len(ids))
	if len(ids) == 0 {
		return out
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, parallel)

	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			gpx, err := connector.DownloadGPX(consumer, session, id)

			mu.Lock()
			out[id] = gpxDownload{gpx: gpx, err: err}
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return out
}
