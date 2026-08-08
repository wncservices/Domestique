package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/wncservices/domestique/apps/api/internal/auth"
	"github.com/wncservices/domestique/apps/api/internal/komoot"
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
	if s.Komoot == nil {
		s.komootDisabled(w)
		return
	}

	tours, err := s.Komoot.Tours(s.Config.Komoot.IncludeRecorded)
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
	if s.Komoot == nil {
		s.komootDisabled(w)
		return
	}

	writable, ok := source.AsWritable(s.Source)
	if !ok {
		s.readOnly(w)
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

	tours, err := s.Komoot.Tours(s.Config.Komoot.IncludeRecorded)
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

	for _, id := range body.TourIDs {
		tour, known := byID[id]
		if !known {
			result.Skipped[id] = "not in this Komoot account"
			continue
		}
		// Re-importing would create a duplicate route, and the rider would
		// have to work out which of the two their device is following.
		if existing[id] {
			result.Skipped[id] = "already imported"
			continue
		}

		raw, err := s.Komoot.GPX(id)
		if err != nil {
			// One bad tour must not abandon the rest of the batch.
			result.Skipped[id] = err.Error()
			continue
		}

		if _, err := writable.Create(source.CreateRequest{
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

func (s *Server) komootDisabled(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotImplemented, map[string]string{
		"error": "Komoot import is not configured — set komoot.enabled and " +
			"provide KOMOOT_EMAIL and KOMOOT_PASSWORD",
	})
}
