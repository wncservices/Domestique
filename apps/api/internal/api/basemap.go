package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/wncservices/domestique/apps/api/internal/auth"
	"github.com/wncservices/domestique/apps/api/internal/basemap"
)

type basemapUpdateDTO struct {
	// Available is whether this deployment can trigger an update at all —
	// false on a laptop, or any deployment without the tiles component and
	// its RBAC wired up (basemapUpdate.enabled in domestique-chart). False
	// hides the form rather than offering a 403, same idiom as
	// garminConsumerDTO.CanManage.
	Available bool `json:"available"`
	// CanManage is whether *this caller* may trigger an update — admin.
	CanManage   bool   `json:"canManage"`
	Unavailable string `json:"unavailable,omitempty"`

	HasRun      bool    `json:"hasRun"`
	Status      string  `json:"status,omitempty"`
	West        float64 `json:"west,omitempty"`
	South       float64 `json:"south,omitempty"`
	East        float64 `json:"east,omitempty"`
	North       float64 `json:"north,omitempty"`
	MaxZoom     int     `json:"maxZoom,omitempty"`
	BuildDate   string  `json:"buildDate,omitempty"`
	Error       string  `json:"error,omitempty"`
	SizeBytes   int64   `json:"sizeBytes,omitempty"`
	RequestedBy string  `json:"requestedBy,omitempty"`
	CreatedAt   string  `json:"createdAt,omitempty"`
	CompletedAt string  `json:"completedAt,omitempty"`
}

// basemapDTOFor reports the latest update's state for this caller,
// refreshing it against the live Job first if it was last seen running —
// polled on demand rather than by a background goroutine, so there is
// nothing to keep running between requests nobody is looking at.
func (s *Server) basemapDTOFor(r *http.Request) basemapUpdateDTO {
	dto := basemapUpdateDTO{Available: s.Basemap != nil && s.BasemapJobs != nil}

	id := auth.FromContext(r.Context())
	if !id.Role.Can(auth.PermManageSettings) {
		// Not an error worth explaining, same reasoning as
		// garminConsumerDTOFor: a rider has no business here.
		return dto
	}
	dto.CanManage = dto.Available
	if !dto.Available {
		dto.Unavailable = "this deployment has no basemap update Job configured"
		return dto
	}

	rec, err := s.Basemap.Latest()
	if err != nil {
		// basemap.ErrNoRecord means nobody has ever triggered one —
		// Available/CanManage true, HasRun false is the whole story.
		return dto
	}

	if rec.Status == basemapStatusPending || rec.Status == basemapStatusRunning {
		if outcome, err := s.BasemapJobs.Outcome(r.Context(), rec.JobName); err == nil && outcome.Done {
			if outcome.Succeeded {
				_ = s.Basemap.MarkSucceeded(rec.ID, outcome.SizeBytes)
			} else {
				_ = s.Basemap.MarkFailed(rec.ID, outcome.Message)
			}
			// Re-read rather than mutate rec in place: Mark* stamps
			// completedAt server-side, and the DTO below should report
			// exactly what is now stored, not a guess at it.
			if refreshed, err := s.Basemap.Latest(); err == nil {
				rec = refreshed
			}
		}
	}

	dto.HasRun = true
	dto.Status = string(rec.Status)
	dto.West, dto.South, dto.East, dto.North = rec.BBox.West, rec.BBox.South, rec.BBox.East, rec.BBox.North
	dto.MaxZoom = rec.MaxZoom
	dto.BuildDate = rec.BuildDate
	dto.Error = rec.Error
	dto.SizeBytes = rec.SizeBytes
	dto.RequestedBy = rec.RequestedBy
	dto.CreatedAt = rec.CreatedAt.Format(time.RFC3339)
	if rec.CompletedAt != nil {
		dto.CompletedAt = rec.CompletedAt.Format(time.RFC3339)
	}
	return dto
}

// Local aliases so this file does not need "basemap." on every status
// comparison — the exported constants still live in the basemap package,
// this just shortens the reads above.
const (
	basemapStatusPending = basemap.StatusPending
	basemapStatusRunning = basemap.StatusRunning
)

// handleBasemap reports the latest update's status.
func (s *Server) handleBasemap(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermManageSettings) {
		return
	}
	writeJSON(w, http.StatusOK, s.basemapDTOFor(r))
}

// handleBasemapUpdate triggers a new update.
func (s *Server) handleBasemapUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.require(w, r, auth.PermManageSettings) {
		return
	}
	if s.Basemap == nil || s.BasemapJobs == nil {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{
			"error": "this deployment has no basemap update Job configured",
		})
		return
	}

	var body struct {
		West, South, East, North float64
		MaxZoom                  int `json:"maxZoom"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	bbox := basemap.BBox{West: body.West, South: body.South, East: body.East, North: body.North}
	if err := bbox.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := basemap.ValidateMaxZoom(body.MaxZoom); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	admin := auth.FromContext(r.Context()).User
	buildDate := time.Now().UTC().Format("20060102")

	id, err := s.Basemap.Create(bbox, body.MaxZoom, buildDate, admin)
	if err != nil {
		s.logger().Error("recording basemap update failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not record the update"})
		return
	}

	jobName, err := s.BasemapJobs.Trigger(r.Context(), bbox, body.MaxZoom, buildDate)
	if err != nil {
		s.logger().Error("triggering basemap update job failed", "err", err)
		_ = s.Basemap.MarkFailed(id, err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "the cluster refused the update job: " + err.Error(),
		})
		return
	}
	if err := s.Basemap.SetJobName(id, jobName); err != nil {
		s.logger().Error("recording basemap job name failed", "err", err)
	}

	s.logger().Info("basemap update triggered", "by", admin, "job", jobName,
		"bbox", bbox, "maxZoom", body.MaxZoom)
	writeJSON(w, http.StatusAccepted, s.basemapDTOFor(r))
}
