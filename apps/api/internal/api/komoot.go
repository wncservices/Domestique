package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/wncservices/domestique/apps/api/internal/auth"
	"github.com/wncservices/domestique/apps/api/internal/komoot"
	"github.com/wncservices/domestique/apps/api/internal/secrets"
	"github.com/wncservices/domestique/apps/api/internal/source"
)

// KomootImporter is the slice of the Komoot client this package needs. An
// interface so tests can substitute a fake, and so a broken third-party API
// stays contained behind one seam.
type KomootImporter interface {
	Tours(includeRecorded bool) ([]komoot.Tour, error)
	GPX(tourID string) ([]byte, error)
}

type komootTourDTO struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Sport     string  `json:"sport"`
	DistanceM float64 `json:"distanceM"`
	AscentM   float64 `json:"ascentM"`
	ChangedAt string  `json:"changedAt,omitempty"`
	// Imported reports whether a route with this Komoot id is already here.
	Imported bool `json:"imported"`
}

type komootImportResult struct {
	Imported []string          `json:"imported"`
	Skipped  map[string]string `json:"skipped"`
}

// handleKomootTours lists what could be imported.
func (s *Server) handleKomootTours(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermKomootSync) {
		return
	}
	client := s.komootFor(r)
	if client == nil {
		s.komootDisabled(w)
		return
	}

	tours, err := client.Tours(s.Config.Komoot.IncludeRecorded)
	if err != nil {
		// Komoot's API is undocumented and moves; surface it as an upstream
		// problem rather than a fault in this app.
		s.logger().Warn("komoot tour listing failed", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	existing := s.komootTagIndex()
	out := make([]komootTourDTO, 0, len(tours))
	for _, t := range tours {
		out = append(out, komootTourDTO{
			ID:        t.ID,
			Name:      t.Name,
			Sport:     t.Sport,
			DistanceM: t.DistanceM,
			AscentM:   t.AscentM,
			ChangedAt: formatTime(t.ChangedAt),
			Imported:  existing[t.ID],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleKomootImport pulls selected tours into the library.
func (s *Server) handleKomootImport(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermKomootSync) {
		return
	}
	client := s.komootFor(r)
	if client == nil {
		s.komootDisabled(w)
		return
	}

	var body struct {
		TourIDs []string `json:"tourIds"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(body.TourIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "no tourIds given",
		})
		return
	}

	tours, err := client.Tours(s.Config.Komoot.IncludeRecorded)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	byID := map[string]komoot.Tour{}
	for _, t := range tours {
		byID[t.ID] = t
	}

	identity := auth.FromContext(r.Context())
	existing := s.komootTagIndex()
	result := komootImportResult{Imported: []string{}, Skipped: map[string]string{}}

	// Decide what to fetch before fetching anything, so the slow part is a
	// single pass with nothing else interleaved.
	wanted := make([]string, 0, len(body.TourIDs))
	for _, id := range body.TourIDs {
		switch {
		case !contains(byID, id):
			result.Skipped[id] = "not in this Komoot account"
		case existing[id]:
			// Re-importing would create a duplicate route, and the rider
			// would have to work out which their device is following.
			result.Skipped[id] = "already imported"
		default:
			wanted = append(wanted, id)
		}
	}

	// One round trip to Komoot per tour, and they were sequential: thirty
	// tours meant thirty waits end to end, with no response byte written
	// until the last one finished. Browsers give up on that. Fetching a few
	// at a time turns it into a handful of waits.
	//
	// Small on purpose — this is somebody's personal account on an
	// undocumented API, not a service to saturate.
	const parallel = 4
	downloads := fetchTours(client, wanted, parallel)

	for _, id := range wanted {
		got := downloads[id]
		if got.err != nil {
			// One bad tour must not abandon the rest of the batch.
			result.Skipped[id] = got.err.Error()
			continue
		}
		tour := byID[id]
		raw := got.gpx

		if _, err := s.Source.Create(source.CreateRequest{
			Filename:   tour.Name + ".gpx",
			Name:       tour.Name,
			Descript:   fmt.Sprintf("Imported from Komoot (tour %s)", id),
			Tags:       []string{"komoot", komootTag(id)},
			UploadedBy: identity.User,
			GPX:        raw,
		}); err != nil {
			result.Skipped[id] = err.Error()
			continue
		}
		result.Imported = append(result.Imported, id)
	}

	s.logger().Info("komoot import finished",
		"user", identity.User, "imported", len(result.Imported), "skipped", len(result.Skipped))
	writeJSON(w, http.StatusOK, result)
}

func contains(byID map[string]komoot.Tour, id string) bool {
	_, ok := byID[id]
	return ok
}

type tourDownload struct {
	gpx []byte
	err error
}

// fetchTours downloads several tours at once, bounded by parallel.
//
// Order is irrelevant here — the caller walks its own list afterwards — but
// the results map must be complete, so every id gets an entry even when the
// fetch failed.
func fetchTours(client KomootImporter, ids []string, parallel int) map[string]tourDownload {
	out := make(map[string]tourDownload, len(ids))
	if len(ids) == 0 {
		return out
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, parallel)

	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			gpx, err := client.GPX(id)

			mu.Lock()
			out[id] = tourDownload{gpx: gpx, err: err}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// komootTagIndex maps Komoot tour ids already in the library.
//
// The id is carried as a tag rather than a column so the fs source works the
// same way — a route.yaml can carry `komoot:12345` just as well.
func (s *Server) komootTagIndex() map[string]bool {
	out := map[string]bool{}
	routes, _, err := s.Source.List()
	if err != nil {
		s.logger().Warn("could not index existing komoot imports", "err", err)
		return out
	}
	for _, route := range routes {
		for _, tag := range route.Tags {
			if id, ok := parseKomootTag(tag); ok {
				out[id] = true
			}
		}
	}
	return out
}

const komootTagPrefix = "komoot:"

func komootTag(id string) string { return komootTagPrefix + id }

func parseKomootTag(tag string) (string, bool) {
	if len(tag) > len(komootTagPrefix) && tag[:len(komootTagPrefix)] == komootTagPrefix {
		return tag[len(komootTagPrefix):], true
	}
	return "", false
}

// komootDisabled explains why there is no client for this rider.
//
// Three different situations reach here and they need different answers. The
// message used to name KOMOOT_EMAIL and KOMOOT_PASSWORD in all of them, which
// was true when one deployment-wide account did every import. Riders now sign
// in from Settings, so that advice sends someone to edit a Deployment for a
// problem they can fix in the UI in ten seconds — and on a multi-rider
// deployment there is no environment answer at all, because the credentials
// are per rider.
func (s *Server) komootDisabled(w http.ResponseWriter) {
	switch {
	case !s.KomootEnabled:
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error": "Komoot import is not enabled for this deployment — set komoot.enabled",
		})
	case s.Links.CanStore():
		// The rider can fix this themselves, so this is not a server-side
		// gap: nothing is missing except a sign-in that has not happened yet.
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{
			"error": "Not signed in to Komoot — connect your account in Settings",
		})
	default:
		// No encryption key, so the store refuses to hold a sign-in and the
		// UI route is closed. The environment is genuinely the only way in.
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error": "Komoot is enabled but this deployment cannot store sign-ins — set " +
				secrets.EnvKey + ", or provide KOMOOT_EMAIL and KOMOOT_PASSWORD",
		})
	}
}
