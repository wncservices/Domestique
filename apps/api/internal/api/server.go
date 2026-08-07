// Package api serves the JSON API behind the web UI, and the built frontend
// alongside it.
//
// What the API allows depends on the route source. A filesystem library is
// read-only — routes are added by committing them to the routes repo. A
// database library accepts uploads, and then the write endpoints appear.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wncservices/domestique/apps/api/internal/config"
	"github.com/wncservices/domestique/apps/api/internal/model"
	"github.com/wncservices/domestique/apps/api/internal/source"
	"github.com/wncservices/domestique/apps/api/internal/state"
	syncer "github.com/wncservices/domestique/apps/api/internal/sync"
	"github.com/wncservices/domestique/apps/api/internal/targets"
)

// maxUploadBytes bounds a multipart upload before it is read into memory.
const maxUploadBytes = 20 << 20 // 20 MiB

// Server holds the request-scoped dependencies.
type Server struct {
	Source source.Source
	Config *config.Config
	Store  state.Store
	Log    *slog.Logger
	// WebFS is the built frontend. Nil serves an API-only server.
	WebFS fs.FS
	// TargetFactory builds the provider adapter for an account. Nil uses the
	// real ones; tests substitute fakes, since the real adapters are stubs and
	// a successful push would otherwise be unreachable.
	TargetFactory func(model.Account) (targets.Target, error)

	// pushMu serialises pushes: two concurrent reconciles against the same
	// account would race on remote ids and on the state file.
	pushMu sync.Mutex
}

// Handler returns the fully wired HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/accounts", s.handleAccounts)
	mux.HandleFunc("GET /api/routes", s.handleRoutes)
	mux.HandleFunc("GET /api/plan", s.handlePlan)
	mux.HandleFunc("POST /api/push", s.handlePush)

	// Slugs contain slashes in a filesystem library, so the wildcard has to be
	// last — hence /api/tracks/<slug> rather than /api/routes/<slug>/track.
	mux.HandleFunc("GET /api/tracks/{slug...}", s.handleTrack)
	mux.HandleFunc("GET /api/gpx/{slug...}", s.handleDownload)

	// Write endpoints are always registered, even against a read-only source:
	// they answer 405 with an explanation. Leaving them unregistered would let
	// the SPA fallback answer 200 with HTML, which no client can interpret.
	mux.HandleFunc("POST /api/routes", s.handleUpload)
	mux.HandleFunc("PATCH /api/routes/{slug...}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/routes/{slug...}", s.handleDelete)

	// Anything else under /api is a 404 in JSON, not the SPA shell.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "no such endpoint: " + r.Method + " " + r.URL.Path,
		})
	})

	if s.WebFS != nil {
		mux.Handle("/", s.spaHandler())
	}
	return logRequests(s.logger(), mux)
}

// ---------- payloads ----------

type configDTO struct {
	Source string `json:"source"`
	// Writable tells the UI whether to offer uploads, or to explain that
	// routes arrive by commit.
	Writable bool `json:"writable"`
}

type accountDTO struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Rider    string `json:"rider"`
	Label    string `json:"label"`
	// Implemented reports whether pushes to this provider actually work yet.
	Implemented bool `json:"implemented"`
}

type routeDTO struct {
	Slug           string       `json:"slug"`
	Name           string       `json:"name"`
	Description    string       `json:"description"`
	Tags           []string     `json:"tags"`
	DistanceM      float64      `json:"distanceM"`
	AscentM        float64      `json:"ascentM"`
	StartLat       float64      `json:"startLat"`
	StartLng       float64      `json:"startLng"`
	PointCount     int          `json:"pointCount"`
	ContentHash    string       `json:"contentHash"`
	Origin         string       `json:"origin"`
	UpdatedAt      string       `json:"updatedAt"`
	Targets        []string     `json:"targets"`
	UnknownTargets []string     `json:"unknownTargets"`
	SyncState      []syncStatus `json:"syncState"`
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

// ---------- read handlers ----------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	_, writable := source.AsWritable(s.Source)
	writeJSON(w, http.StatusOK, configDTO{Source: s.Source.Describe(), Writable: writable})
}

func (s *Server) handleAccounts(w http.ResponseWriter, _ *http.Request) {
	out := make([]accountDTO, 0, len(s.Config.Accounts))
	for _, a := range s.Config.Accounts {
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
	routes, problems, err := s.Source.List()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, libraryResponse{
		Routes:   s.toRouteDTOs(routes),
		Problems: orEmpty(problems),
	})
}

// handleTrack returns the raw coordinates so the UI can draw a route preview
// without shipping a map library or calling out to a tile server.
func (s *Server) handleTrack(w http.ResponseWriter, r *http.Request) {
	slug := cleanSlug(r.PathValue("slug"))
	points, err := s.Source.Track(slug)
	if err != nil {
		s.failLookup(w, err)
		return
	}

	coords := make([][2]float64, 0, len(points))
	for _, p := range points {
		coords = append(coords, [2]float64{p.Lat, p.Lon})
	}
	writeJSON(w, http.StatusOK, map[string]any{"slug": slug, "points": coords})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	slug := cleanSlug(r.PathValue("slug"))
	raw, err := s.Source.GPX(slug)
	if err != nil {
		s.failLookup(w, err)
		return
	}

	filename := strings.ReplaceAll(slug, "/", "-") + ".gpx"
	w.Header().Set("Content-Type", "application/gpx+xml")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	if _, err := w.Write(raw); err != nil {
		s.logger().Error("write gpx", "err", err)
	}
}

func (s *Server) handlePlan(w http.ResponseWriter, _ *http.Request) {
	routes, problems, err := s.Source.List()
	if err != nil {
		s.fail(w, err)
		return
	}

	plan := syncer.BuildPlan(routes, s.Config, s.Store)
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

	routes, _, err := s.Source.List()
	if err != nil {
		s.fail(w, err)
		return
	}

	build := s.TargetFactory
	if build == nil {
		build = targets.Build
	}

	byAccount := map[string]targets.Target{}
	for _, account := range s.Config.Accounts {
		target, err := build(account)
		if err != nil {
			s.fail(w, err)
			return
		}
		byAccount[account.ID] = target
	}

	plan := syncer.BuildPlan(routes, s.Config, s.Store)
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

// ---------- write handlers (writable sources only) ----------

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	writable, ok := source.AsWritable(s.Source)
	if !ok {
		s.readOnly(w)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "could not read the upload: " + err.Error(),
		})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "expected a GPX file in the `file` field",
		})
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		s.fail(w, err)
		return
	}

	req := source.CreateRequest{
		Filename:   header.Filename,
		Name:       r.FormValue("name"),
		Descript:   r.FormValue("description"),
		Tags:       splitCSV(r.FormValue("tags")),
		UploadedBy: r.FormValue("uploadedBy"),
		GPX:        raw,
	}
	if targetsField := r.FormValue("targets"); targetsField != "" {
		list := splitCSV(targetsField)
		req.Targets = &list
	}

	route, err := writable.Create(req)
	if err != nil {
		// A bad GPX is the caller's problem, not a server fault.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	s.logger().Info("route uploaded", "slug", route.Slug, "by", req.UploadedBy)
	writeJSON(w, http.StatusCreated, s.toRouteDTO(route))
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	writable, ok := source.AsWritable(s.Source)
	if !ok {
		s.readOnly(w)
		return
	}

	var body struct {
		Name        *string   `json:"name"`
		Description *string   `json:"description"`
		Tags        *[]string `json:"tags"`
		Targets     *[]string `json:"targets"`
		Enabled     *bool     `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	route, err := writable.Update(cleanSlug(r.PathValue("slug")), source.UpdateRequest{
		Name:     body.Name,
		Descript: body.Description,
		Tags:     body.Tags,
		Targets:  body.Targets,
		Enabled:  body.Enabled,
	})
	if err != nil {
		s.failLookup(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toRouteDTO(route))
}

// handleDelete removes a route from the source. It deliberately leaves sync
// state alone: the next plan will show a delete against every account that
// still holds it, which is exactly what should happen.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	writable, ok := source.AsWritable(s.Source)
	if !ok {
		s.readOnly(w)
		return
	}

	slug := cleanSlug(r.PathValue("slug"))
	if err := writable.Delete(slug); err != nil {
		s.failLookup(w, err)
		return
	}

	s.logger().Info("route deleted", "slug", slug)
	w.WriteHeader(http.StatusNoContent)
}

// ---------- plumbing ----------

func (s *Server) toRouteDTOs(routes []model.Route) []routeDTO {
	out := make([]routeDTO, 0, len(routes))
	for _, r := range routes {
		out = append(out, s.toRouteDTO(r))
	}
	return out
}

func (s *Server) toRouteDTO(r model.Route) routeDTO {
	targetIDs := s.Config.TargetsFor(r)
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

	return routeDTO{
		Slug:           r.Slug,
		Name:           r.Name,
		Description:    r.Description,
		Tags:           orEmpty(r.Tags),
		DistanceM:      r.Stats.DistanceM,
		AscentM:        r.Stats.AscentM,
		StartLat:       r.Stats.StartLat,
		StartLng:       r.Stats.StartLng,
		PointCount:     r.Stats.PointCount,
		ContentHash:    r.ContentHash,
		Origin:         r.Origin,
		UpdatedAt:      r.UpdatedAt,
		Targets:        orEmpty(targetIDs),
		UnknownTargets: orEmpty(s.Config.UnknownTargets(r)),
		SyncState:      statuses,
	}
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

func (s *Server) failLookup(w http.ResponseWriter, err error) {
	if errors.Is(err, source.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	s.fail(w, err)
}

func (s *Server) readOnly(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
		"error": "this library is read-only — add routes by committing them to the routes repo",
	})
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

func cleanSlug(raw string) string { return strings.Trim(raw, "/") }

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
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
