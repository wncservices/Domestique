// Package api serves the JSON API behind the web UI, and the built frontend
// alongside it.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wncservices/domestique/apps/api/internal/accounts"
	"github.com/wncservices/domestique/apps/api/internal/auth"
	"github.com/wncservices/domestique/apps/api/internal/config"
	"github.com/wncservices/domestique/apps/api/internal/fitcourse"
	"github.com/wncservices/domestique/apps/api/internal/gpx"
	"github.com/wncservices/domestique/apps/api/internal/komootlink"
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
	Source   source.Library
	Config   *config.Config
	Store    state.Store
	Accounts *accounts.Store
	Auth     *auth.Authenticator
	Log      *slog.Logger
	// Komoot imports routes from a Komoot account. Nil disables the feature.
	Komoot KomootImporter

	// KomootLinks holds each rider's own Komoot connection, made through the
	// UI. Nil disables connecting, but not the environment-configured client.
	KomootLinks *komootlink.Store

	// Connector signs riders in to Komoot and resumes their stored sessions.
	Connector KomootConnector

	// KomootEnabled is what the operator asked for, which is not the same as
	// what they got: the config can turn Komoot on while the credentials are
	// missing, leaving Komoot nil. Keeping both apart lets the UI say "set
	// KOMOOT_EMAIL" instead of hiding a feature somebody deliberately enabled.
	KomootEnabled bool
	// WebFS is the built frontend. Nil serves an API-only server.
	WebFS fs.FS

	// LandingHost is the hostname that gets the logged-out page instead of
	// the app — the apex, while the app itself lives behind Authelia on a
	// subdomain. Empty serves the app to everyone, which is what a laptop
	// wants.
	//
	// One deployment serving both is deliberate: the landing page is three
	// screens of static content and does not earn a service of its own. If it
	// ever does, this is the seam to split on.
	LandingHost string
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
	mux.HandleFunc("GET /api/me", s.handleMe)
	mux.HandleFunc("GET /api/accounts", s.handleAccounts)
	mux.HandleFunc("POST /api/accounts", s.handleLinkAccount)
	mux.HandleFunc("DELETE /api/accounts/{id}", s.handleUnlinkAccount)
	mux.HandleFunc("GET /api/routes", s.handleRoutes)
	mux.HandleFunc("GET /api/plan", s.handlePlan)
	mux.HandleFunc("POST /api/push", s.handlePush)

	// The wildcard has to be last in a Go mux pattern, hence /api/tracks/<slug>
	// rather than /api/routes/<slug>/track.
	mux.HandleFunc("GET /api/tracks/{slug...}", s.handleTrack)
	mux.HandleFunc("GET /api/gpx/{slug...}", s.handleDownload)
	mux.HandleFunc("GET /api/fit/{slug...}", s.handleDownloadFIT)

	mux.HandleFunc("POST /api/routes", s.handleUpload)
	mux.HandleFunc("PATCH /api/routes/{slug...}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/routes/{slug...}", s.handleDelete)

	mux.HandleFunc("GET /api/komoot/connection", s.handleKomootConnection)
	mux.HandleFunc("POST /api/komoot/connection", s.handleKomootConnect)
	mux.HandleFunc("DELETE /api/komoot/connection", s.handleKomootDisconnect)
	mux.HandleFunc("GET /api/komoot/tours", s.handleKomootTours)
	mux.HandleFunc("POST /api/komoot/import", s.handleKomootImport)

	// Anything else under /api is a 404 in JSON, not the SPA shell.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "no such endpoint: " + r.Method + " " + r.URL.Path,
		})
	})

	if s.WebFS != nil {
		mux.Handle("/", s.spaHandler())
	}
	return logRequests(s.logger(), s.authenticate(mux))
}

// authenticate resolves the identity once per request and puts it on the
// context. Endpoints then check permissions; this only decides *who* you are,
// not what you may do.
//
// /api/health stays open so a liveness probe does not need credentials.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}

		id := s.authenticator().Identify(r)
		if err := s.authenticator().Authorize(id); err != nil {
			// Only gate the API. The SPA itself must still load, or the
			// browser gets a JSON blob instead of a page explaining itself.
			if strings.HasPrefix(r.URL.Path, "/api/") {
				status := http.StatusUnauthorized
				if errors.Is(err, auth.ErrForbidden) {
					status = http.StatusForbidden
				}
				writeJSON(w, status, map[string]string{"error": err.Error()})
				return
			}
		}

		next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), id)))
	})
}

func (s *Server) authenticator() *auth.Authenticator {
	if s.Auth != nil {
		return s.Auth
	}
	// A server built without an authenticator runs unauthenticated rather
	// than panicking; every caller in this repo sets one.
	a, _ := auth.New(auth.Config{Mode: auth.ModeNone})
	return a
}

// require checks a permission and writes the error itself, returning false
// when the caller should stop.
func (s *Server) require(w http.ResponseWriter, r *http.Request, p auth.Permission) bool {
	id := auth.FromContext(r.Context())
	if id.Role.Can(p) {
		return true
	}

	s.logger().Info("permission denied",
		"user", id.User, "role", id.Role, "permission", p, "path", r.URL.Path)
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error": fmt.Sprintf("your role (%s) does not allow %s", roleLabel(id.Role), p),
	})
	return false
}

func roleLabel(r auth.Role) string {
	if r == auth.RoleNone {
		return "none"
	}
	return string(r)
}

// ---------- payloads ----------

type configDTO struct {
	Source string `json:"source"`
	// Komoot is one of "disabled", "unconfigured" or "ready".
	Komoot string `json:"komoot"`
}

type accountDTO struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Rider    string `json:"rider"`
	Label    string `json:"label"`
	// Implemented reports whether pushes to this provider actually work yet.
	Implemented bool `json:"implemented"`
	// Mine tells the UI whether the viewer may unlink this one.
	Mine bool `json:"mine"`
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
	Owner          string       `json:"owner,omitempty"`
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
	writeJSON(w, http.StatusOK, configDTO{
		Source: s.Source.Describe(),
		Komoot: s.komootState(),
	})
}

// komootState separates "nobody asked for Komoot" from "somebody asked and it
// could not start". Hiding the second looks identical to the first, which is
// how a missing environment variable turns into a feature that silently is
// not there.
func (s *Server) komootState() string {
	switch {
	case !s.KomootEnabled:
		return "disabled"
	case s.Komoot != nil || s.KomootLinks.CanStore():
		// Either the deployment has an account, or a rider can connect their
		// own. Both are usable, and the panel belongs on screen.
		return "ready"
	default:
		return "unconfigured"
	}
}

type meDTO struct {
	Authenticated bool              `json:"authenticated"`
	AuthMode      string            `json:"authMode"`
	User          string            `json:"user,omitempty"`
	Name          string            `json:"name,omitempty"`
	Email         string            `json:"email,omitempty"`
	Groups        []string          `json:"groups"`
	Role          string            `json:"role"`
	Permissions   []auth.Permission `json:"permissions"`
}

// handleMe tells the UI who it is talking to and what to show. Without it the
// frontend would have to guess, and would offer buttons that 403.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	id := auth.FromContext(r.Context())
	writeJSON(w, http.StatusOK, meDTO{
		Authenticated: s.authenticator().Enabled(),
		AuthMode:      string(s.authenticator().Mode()),
		User:          id.User,
		Name:          id.Name,
		Email:         id.Email,
		Groups:        orEmpty(id.Groups),
		Role:          roleLabel(id.Role),
		Permissions:   orEmpty(id.Role.Permissions()),
	})
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermReadRoutes) {
		return
	}

	linked, ok := s.linkedAccounts(w)
	if !ok {
		return
	}

	identity := auth.FromContext(r.Context())
	out := make([]accountDTO, 0, len(linked))
	for _, a := range linked {
		out = append(out, accountDTO{
			ID:          a.ID,
			Provider:    string(a.Provider),
			Rider:       a.Rider,
			Label:       a.Label,
			Implemented: targets.Implemented(a.Provider),
			Mine:        identity.CanEditRoute(a.Rider),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleLinkAccount connects a provider for the signed-in rider.
//
// The rider comes from the session, never the request body: an account is
// yours because you linked it, and letting the body say otherwise would let
// someone create an account they cannot then unlink.
func (s *Server) handleLinkAccount(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermManageAccounts) {
		return
	}

	var body struct {
		Provider string `json:"provider"`
		Label    string `json:"label"`
		// Rider is honoured only for an admin linking on someone's behalf.
		Rider string `json:"rider"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	identity := auth.FromContext(r.Context())
	rider := identity.User
	if body.Rider != "" && !strings.EqualFold(body.Rider, identity.User) {
		if !identity.Role.Can(auth.PermEditAny) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "only an admin can link an account for somebody else",
			})
			return
		}
		rider = body.Rider
	}

	account, err := s.Accounts.Link(model.Provider(body.Provider), rider, body.Label)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, accounts.ErrExists) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	s.logger().Info("account linked", "id", account.ID, "by", identity.User)
	writeJSON(w, http.StatusCreated, accountDTO{
		ID:          account.ID,
		Provider:    string(account.Provider),
		Rider:       account.Rider,
		Label:       account.Label,
		Implemented: targets.Implemented(account.Provider),
		Mine:        true,
	})
}

// handleUnlinkAccount removes a linked provider. Riders may unlink their own;
// admins anyone's.
func (s *Server) handleUnlinkAccount(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermManageAccounts) {
		return
	}

	id := cleanSlug(r.PathValue("id"))
	account, err := s.Accounts.Get(id)
	if err != nil {
		s.failAccount(w, err)
		return
	}

	identity := auth.FromContext(r.Context())
	if !identity.CanEditRoute(account.Rider) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "that account belongs to " + account.Rider + "; only they or an admin can unlink it",
		})
		return
	}

	if err := s.Accounts.Unlink(id); err != nil {
		s.failAccount(w, err)
		return
	}

	s.logger().Info("account unlinked", "id", id, "by", identity.User)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) failAccount(w http.ResponseWriter, err error) {
	if errors.Is(err, accounts.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	s.fail(w, err)
}

// linkedAccounts reads the accounts, or writes the error and reports false.
func (s *Server) linkedAccounts(w http.ResponseWriter) ([]model.Account, bool) {
	linked, err := s.Accounts.List()
	if err != nil {
		s.fail(w, err)
		return nil, false
	}
	return linked, true
}

func (s *Server) handleRoutes(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermReadRoutes) {
		return
	}

	routes, problems, err := s.Source.List()
	if err != nil {
		s.fail(w, err)
		return
	}
	linked, ok := s.linkedAccounts(w)
	if !ok {
		return
	}

	writeJSON(w, http.StatusOK, libraryResponse{
		Routes:   s.toRouteDTOs(routes, linked),
		Problems: orEmpty(problems),
	})
}

// handleTrack returns the raw coordinates so the UI can draw a route preview
// without shipping a map library or calling out to a tile server.
func (s *Server) handleTrack(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermReadRoutes) {
		return
	}

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
	if !s.require(w, r, auth.PermReadRoutes) {
		return
	}

	slug := cleanSlug(r.PathValue("slug"))
	raw, err := s.Source.GPX(slug)
	if err != nil {
		s.failLookup(w, err)
		return
	}

	filename := strings.ReplaceAll(slug, "/", "-") + ".gpx"
	w.Header().Set("Content-Type", "application/gpx+xml")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	// The body is a GPX file served as a download, never rendered as a page.
	// nosniff stops a browser deciding otherwise.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// #nosec G705 -- served as an attachment with a fixed content type, not HTML.
	if _, err := w.Write(raw); err != nil {
		s.logger().Error("write gpx", "err", err)
	}
}

// handleDownloadFIT converts a route to a Garmin FIT course on the fly.
//
// Useful on its own — a FIT can be copied to a device over USB — and it is the
// same conversion the Wahoo adapter will use, so being able to download one
// and load it onto a real head unit is how the conversion gets proven.
//
// Turn cues are opt-in with ?cues=1, because they are inferred from the track's
// geometry rather than authored: see fitcourse.DeriveTurns.
func (s *Server) handleDownloadFIT(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermReadRoutes) {
		return
	}

	slug := cleanSlug(r.PathValue("slug"))
	raw, err := s.Source.GPX(slug)
	if err != nil {
		s.failLookup(w, err)
		return
	}

	points, err := gpx.ParsePoints(raw)
	if err != nil {
		s.fail(w, err)
		return
	}

	name := slug
	if routes, _, listErr := s.Source.List(); listErr == nil {
		for _, route := range routes {
			if route.Slug == slug {
				name = route.Name
				break
			}
		}
	}

	fitBytes, err := fitcourse.Encode(points, fitcourse.Options{
		Name:     name,
		TurnCues: r.URL.Query().Get("cues") == "1",
	})
	if err != nil {
		s.fail(w, err)
		return
	}

	filename := strings.ReplaceAll(slug, "/", "-") + ".fit"
	w.Header().Set("Content-Type", "application/vnd.ant.fit")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// #nosec G705 -- binary FIT served as an attachment with a fixed content
	// type and nosniff, never rendered as a page.
	if _, err := w.Write(fitBytes); err != nil {
		s.logger().Error("write fit", "err", err)
	}
}

func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermReadRoutes) {
		return
	}

	routes, problems, err := s.Source.List()
	if err != nil {
		s.fail(w, err)
		return
	}

	linked, ok := s.linkedAccounts(w)
	if !ok {
		return
	}

	plan, err := syncer.BuildPlan(routes, linked, s.Store)
	if err != nil {
		s.fail(w, err)
		return
	}

	changes := plan.Changes()
	writeJSON(w, http.StatusOK, planResponse{
		Items:    toPlanDTOs(changes),
		InSync:   len(plan.Items) - len(changes),
		Problems: orEmpty(problems),
	})
}

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermPush) {
		return
	}

	s.pushMu.Lock()
	defer s.pushMu.Unlock()

	routes, _, err := s.Source.List()
	if err != nil {
		s.fail(w, err)
		return
	}

	linked, ok := s.linkedAccounts(w)
	if !ok {
		return
	}

	build := s.TargetFactory
	if build == nil {
		build = targets.Build
	}

	byAccount := map[string]targets.Target{}
	for _, account := range linked {
		target, err := build(account)
		if err != nil {
			s.fail(w, err)
			return
		}
		byAccount[account.ID] = target
	}

	plan, err := syncer.BuildPlan(routes, linked, s.Store)
	if err != nil {
		s.fail(w, err)
		return
	}

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
	if !s.require(w, r, auth.PermUploadRoute) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	// #nosec G120 -- the body is bounded by MaxBytesReader on the line above.
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
	defer func() { _ = file.Close() }()

	raw, err := io.ReadAll(file)
	if err != nil {
		s.fail(w, err)
		return
	}

	// Ownership comes from the authenticated identity, never from the form:
	// a rider could otherwise upload a route as somebody else and put it
	// beyond their own ability to delete.
	uploader := auth.FromContext(r.Context()).User
	if uploader == "" {
		uploader = r.FormValue("uploadedBy")
	}

	req := source.CreateRequest{
		Filename:   header.Filename,
		Name:       r.FormValue("name"),
		Descript:   r.FormValue("description"),
		Tags:       splitCSV(r.FormValue("tags")),
		UploadedBy: uploader,
		GPX:        raw,
	}
	if targetsField := r.FormValue("targets"); targetsField != "" {
		list := splitCSV(targetsField)
		req.Targets = &list
	}

	route, err := s.Source.Create(req)
	if err != nil {
		// A bad GPX is the caller's problem, not a server fault.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	linked, ok := s.linkedAccounts(w)
	if !ok {
		return
	}

	s.logger().Info("route uploaded", "slug", route.Slug, "by", req.UploadedBy)
	writeJSON(w, http.StatusCreated, s.toRouteDTO(route, linked))
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermEditOwn) {
		return
	}

	slug := cleanSlug(r.PathValue("slug"))
	if !s.mayEdit(w, r, slug) {
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

	route, err := s.Source.Update(slug, source.UpdateRequest{
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
	linked, ok := s.linkedAccounts(w)
	if !ok {
		return
	}

	writeJSON(w, http.StatusOK, s.toRouteDTO(route, linked))
}

// handleDelete removes a route from the source. It deliberately leaves sync
// state alone: the next plan will show a delete against every account that
// still holds it, which is exactly what should happen.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermEditOwn) {
		return
	}

	slug := cleanSlug(r.PathValue("slug"))
	if !s.mayEdit(w, r, slug) {
		return
	}
	if err := s.Source.Delete(slug); err != nil {
		s.failLookup(w, err)
		return
	}

	s.logger().Info("route deleted", "slug", slug)
	w.WriteHeader(http.StatusNoContent)
}

// ---------- plumbing ----------

func (s *Server) toRouteDTOs(routes []model.Route, linked []model.Account) []routeDTO {
	out := make([]routeDTO, 0, len(routes))
	for _, r := range routes {
		out = append(out, s.toRouteDTO(r, linked))
	}
	return out
}

// stateFor reads an account's recorded state, logging and returning nothing on
// failure. Callers use this only to decorate the UI; the plan and the push read
// state properly and refuse to run when it cannot be read.
func (s *Server) stateFor(accountID string) map[string]state.Entry {
	entries, err := s.Store.ForAccount(accountID)
	if err != nil {
		s.logger().Error("could not read sync state", "account", accountID, "err", err)
		return nil
	}
	return entries
}

func (s *Server) toRouteDTO(r model.Route, linked []model.Account) routeDTO {
	targetIDs := config.TargetsFor(r, linked)
	statuses := make([]syncStatus, 0, len(targetIDs))
	for _, id := range targetIDs {
		entry, seen := s.stateFor(id)[r.Slug]
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
		Owner:          r.Owner,
		UpdatedAt:      r.UpdatedAt,
		Targets:        orEmpty(targetIDs),
		UnknownTargets: orEmpty(config.UnknownTargets(r, linked)),
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

// isLandingRequest reports whether this request should get the logged-out
// page rather than the app.
//
// Matched on the Host header, and only for the root path: a request for
// /assets/... on the apex is still a real asset, and the landing page has none
// of its own anyway.
func (s *Server) isLandingRequest(r *http.Request) bool {
	if s.LandingHost == "" {
		return false
	}
	if p := filepath.Clean(r.URL.Path); p != "/" && p != "." {
		return false
	}

	// Host can carry a port, and comparison is case-insensitive.
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.EqualFold(host, s.LandingHost)
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
		if s.isLandingRequest(r) {
			// Falls through to the app when the file is missing, so an
			// unbuilt frontend degrades rather than 404s the front door.
			if _, err := fs.Stat(s.WebFS, "landing.html"); err == nil {
				clean = "landing.html"
				r = r.Clone(r.Context())
				r.URL.Path = "/landing.html"
			}
		}
		if _, err := fs.Stat(s.WebFS, clean); errors.Is(err, os.ErrNotExist) {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
}

// mayEdit enforces ownership: a rider may change what they uploaded, an admin
// anything. It writes the error itself and reports whether to continue.
func (s *Server) mayEdit(w http.ResponseWriter, r *http.Request, slug string) bool {
	id := auth.FromContext(r.Context())
	if id.Role.Can(auth.PermEditAny) {
		return true
	}

	routes, _, err := s.Source.List()
	if err != nil {
		s.fail(w, err)
		return false
	}

	for _, route := range routes {
		if route.Slug != slug {
			continue
		}
		if id.CanEditRoute(route.Owner) {
			return true
		}
		s.logger().Info("edit denied on another rider's route",
			"user", id.User, "slug", slug, "owner", route.Owner)
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "that route belongs to " + route.Owner + "; only they or an admin can change it",
		})
		return false
	}

	// Unknown slug: let the source produce the 404 rather than leaking
	// whether it exists through the permission check.
	return true
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
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
