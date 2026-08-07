// Package api serves the JSON API behind the web UI, and the built frontend
// alongside it.
//
// The API is read-mostly on purpose: the route library is a git repo, so
// creating and deleting routes happens through commits, not through this
// server. The one write endpoint is POST /api/push, which reconciles what the
// library already says into the riders' accounts.
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wncservices/domestique/apps/api/internal/library"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/state"
	syncer "github.com/wncservices/domestique/apps/api/internal/sync"
	"github.com/wncservices/domestique/apps/api/internal/targets"
)

// Server holds the request-scoped dependencies.
type Server struct {
	LibraryPath string
	Store       state.Store
	Log         *slog.Logger
	// WebFS is the built frontend. Nil serves an API-only server.
	WebFS fs.FS

	// pushMu serialises pushes: two concurrent reconciles against the same
	// account would race on remote ids and on the state file.
	pushMu sync.Mutex
}

// Handler returns the fully wired HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/accounts", s.handleAccounts)
	mux.HandleFunc("GET /api/routes", s.handleRoutes)
	// Slugs contain slashes, so the wildcard has to be last — hence /api/tracks/
	// rather than /api/routes/{slug}/track.
	mux.HandleFunc("GET /api/tracks/{slug...}", s.handleTrack)
	mux.HandleFunc("GET /api/plan", s.handlePlan)
	mux.HandleFunc("POST /api/push", s.handlePush)

	if s.WebFS != nil {
		mux.Handle("/", s.spaHandler())
	}
	return logRequests(s.logger(), mux)
}

// ---------- payloads ----------

type accountDTO struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Rider    string `json:"rider"`
	Label    string `json:"label"`
	// Implemented reports whether pushes to this provider actually work yet.
	Implemented bool `json:"implemented"`
}

type routeDTO struct {
	Slug        string       `json:"slug"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Tags        []string     `json:"tags"`
	DistanceM   float64      `json:"distanceM"`
	AscentM     float64      `json:"ascentM"`
	StartLat    float64      `json:"startLat"`
	StartLng    float64      `json:"startLng"`
	PointCount  int          `json:"pointCount"`
	ContentHash string       `json:"contentHash"`
	Targets     []string     `json:"targets"`
	SyncState   []syncStatus `json:"syncState"`
}

type syncStatus struct {
	AccountID string `json:"accountId"`
	Status    string `json:"status"` // synced | pending | stale
	RemoteID  string `json:"remoteId,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type planItemDTO struct {
	Op        string `json:"op"`
	AccountID string `json:"accountId"`
	Slug      string `json:"slug"`
	Reason    string `json:"reason"`
}

type libraryResponse struct {
	Routes   []routeDTO `json:"routes"`
	Problems []string   `json:"problems"`
}

type planResponse struct {
	Items    []planItemDTO `json:"items"`
	InSync   int           `json:"inSync"`
	Problems []string      `json:"problems"`
}

type pushResponse struct {
	Applied  int           `json:"applied"`
	Failures []string      `json:"failures"`
	Items    []planItemDTO `json:"items"`
}

// ---------- handlers ----------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAccounts(w http.ResponseWriter, _ *http.Request) {
	lib, _, err := s.load()
	if err != nil {
		s.fail(w, err)
		return
	}

	out := make([]accountDTO, 0, len(lib.Config.Accounts))
	for _, a := range lib.Config.Accounts {
		out = append(out, accountDTO{
			ID:          a.ID,
			Provider:    string(a.Provider),
			Rider:       a.Rider,
			Label:       a.Label,
			Implemented: targets.Implemented(a.Provider),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRoutes(w http.ResponseWriter, _ *http.Request) {
	lib, problems, err := s.load()
	if err != nil {
		s.fail(w, err)
		return
	}

	routes := make([]routeDTO, 0, len(lib.Routes))
	for _, r := range lib.Routes {
		targetIDs := lib.TargetsFor(r)
		statuses := make([]syncStatus, 0, len(targetIDs))
		for _, id := range targetIDs {
			entry, seen := s.Store.ForAccount(id)[r.Slug]
			switch {
			case !seen:
				statuses = append(statuses, syncStatus{AccountID: id, Status: "pending"})
			case entry.ContentHash != r.ContentHash:
				statuses = append(statuses, syncStatus{
					AccountID: id, Status: "stale",
					RemoteID: entry.RemoteID, UpdatedAt: entry.UpdatedAt,
				})
			default:
				statuses = append(statuses, syncStatus{
					AccountID: id, Status: "synced",
					RemoteID: entry.RemoteID, UpdatedAt: entry.UpdatedAt,
				})
			}
		}

		routes = append(routes, routeDTO{
			Slug:        r.Slug,
			Name:        r.Name(),
			Description: r.Meta.Description,
			Tags:        orEmpty(r.Meta.Tags),
			DistanceM:   r.Stats.DistanceM,
			AscentM:     r.Stats.AscentM,
			StartLat:    r.Stats.StartLat,
			StartLng:    r.Stats.StartLng,
			PointCount:  r.Stats.PointCount,
			ContentHash: r.ContentHash,
			Targets:     orEmpty(targetIDs),
			SyncState:   statuses,
		})
	}

	writeJSON(w, http.StatusOK, libraryResponse{Routes: routes, Problems: orEmpty(problems)})
}

// handleTrack returns the raw coordinates so the UI can draw a route preview
// without shipping a map library or calling out to a tile server.
func (s *Server) handleTrack(w http.ResponseWriter, r *http.Request) {
	lib, _, err := s.load()
	if err != nil {
		s.fail(w, err)
		return
	}

	slug := strings.Trim(r.PathValue("slug"), "/")
	for _, route := range lib.Routes {
		if route.Slug != slug {
			continue
		}
		points, err := library.ReadPoints(route.GPXPath)
		if err != nil {
			s.fail(w, err)
			return
		}
		coords := make([][2]float64, 0, len(points))
		for _, p := range points {
			coords = append(coords, [2]float64{p.Lat, p.Lon})
		}
		writeJSON(w, http.StatusOK, map[string]any{"slug": slug, "points": coords})
		return
	}

	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such route: " + slug})
}

func (s *Server) handlePlan(w http.ResponseWriter, _ *http.Request) {
	lib, problems, err := s.load()
	if err != nil {
		s.fail(w, err)
		return
	}

	plan := syncer.BuildPlan(lib, s.Store)
	changes := plan.Changes()
	writeJSON(w, http.StatusOK, planResponse{
		Items:    toPlanDTOs(changes),
		InSync:   len(plan.Items) - len(changes),
		Problems: orEmpty(problems),
	})
}

func (s *Server) handlePush(w http.ResponseWriter, _ *http.Request) {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()

	lib, _, err := s.load()
	if err != nil {
		s.fail(w, err)
		return
	}

	byAccount := map[string]targets.Target{}
	for _, account := range lib.Config.Accounts {
		target, err := targets.Build(account)
		if err != nil {
			s.fail(w, err)
			return
		}
		byAccount[account.ID] = target
	}

	plan := syncer.BuildPlan(lib, s.Store)
	changes := plan.Changes()
	failures := syncer.Apply(plan, s.Store, byAccount)

	messages := make([]string, 0, len(failures))
	for _, f := range failures {
		messages = append(messages, f.Error())
	}

	s.logger().Info("push finished", "changes", len(changes), "failures", len(failures))
	writeJSON(w, http.StatusOK, pushResponse{
		Applied:  len(changes) - len(failures),
		Failures: messages,
		Items:    toPlanDTOs(changes),
	})
}

// ---------- plumbing ----------

// load re-reads the library on every request. It is a directory of small files
// and a git pull can change it under us at any moment, so caching would mostly
// buy stale answers.
func (s *Server) load() (*library.Library, []string, error) {
	return library.Load(s.LibraryPath)
}

func (s *Server) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.logger().Error("request failed", "err", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

// spaHandler serves the built frontend, falling back to index.html so client
// side routes survive a refresh.
func (s *Server) spaHandler() http.Handler {
	files := http.FileServer(http.FS(s.WebFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(filepath.Clean(r.URL.Path), "/")
		if clean == "." || clean == "" {
			clean = "index.html"
		}
		if _, err := fs.Stat(s.WebFS, clean); errors.Is(err, os.ErrNotExist) {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
}

func toPlanDTOs(items []model.PlanItem) []planItemDTO {
	out := make([]planItemDTO, 0, len(items))
	for _, item := range items {
		out = append(out, planItemDTO{
			Op:        string(item.Op),
			AccountID: item.AccountID,
			Slug:      item.Slug,
			Reason:    item.Reason,
		})
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("encode response", "err", err)
	}
}

// orEmpty keeps JSON arrays as [] rather than null, so the frontend never has
// to null-check a list.
func orEmpty[T any](in []T) []T {
	if in == nil {
		return []T{}
	}
	return in
}

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Debug("request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
