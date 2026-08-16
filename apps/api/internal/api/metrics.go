package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/wncservices/domestique/apps/api/internal/model"
)

// Package-level, registered against the default registry: one process, one
// set of metrics, no reason to thread a registry through Server for this.
var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "domestique_http_requests_total",
		Help: "HTTP requests by method and status.",
	}, []string{"method", "status"})

	// No path label, deliberately: several routes carry a route slug or
	// account id in the path (/api/routes/{slug}, /api/accounts/{id}), and
	// labeling by raw path would give each distinct route its own metrics
	// series forever — unbounded cardinality that grows with the library,
	// not with the code. method+status stays bounded by the handful of
	// verbs and status codes this API actually returns.
	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "domestique_http_request_duration_seconds",
		Help: "HTTP request duration by method and status.",
	}, []string{"method", "status"})

	// The two metrics docs/plan.md's own "Phase 6" describes: a staleness
	// gauge and a per-account error counter, so an alert can catch "pushes
	// stopped" instead of a rider noticing a route missing at the start of
	// a ride. Labeled by account (already "<provider>:<rider>", the same
	// id used everywhere else in this codebase) rather than splitting
	// provider/rider into two labels — one id an operator already
	// recognizes, not two they have to mentally rejoin.
	pushLastSuccessTimestamp = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "domestique_push_last_success_timestamp_seconds",
		Help: "Unix time of the last successful push per account, by op (create/update/delete).",
	}, []string{"account", "op"})

	pushErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "domestique_push_errors_total",
		Help: "Failed push attempts per account.",
	}, []string{"account", "op"})
)

// metricsHandler is /api/metrics itself — exempted from auth in authenticate,
// the same way /api/health and /api/config already are: a scraper has no
// rider identity to present.
func metricsHandler() http.Handler {
	return promhttp.Handler()
}

// instrument records the two HTTP metrics above around every request. Wraps
// outside authenticate/logRequests so a request that never reaches a route
// (an unmatched path, a panic recovered elsewhere) still counts — the same
// reasoning logRequests already applies by sitting outermost.
func instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		status := strconv.Itoa(rec.status)
		httpRequestsTotal.WithLabelValues(r.Method, status).Inc()
		httpRequestDuration.WithLabelValues(r.Method, status).Observe(time.Since(started).Seconds())
	})
}

// statusRecorder captures the status code a handler wrote — plain
// http.ResponseWriter has no way to ask afterward, since WriteHeader only
// sends it, it does not store it anywhere the caller can read back.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// recordPushResult is sync.Apply's onResult callback for handlePush — the
// seam that lets internal/sync stay pure and unaware that metrics exist at
// all, per its own package doc. Noop items are skipped: they mean nothing
// changed, not that a push succeeded or failed.
func (s *Server) recordPushResult(item model.PlanItem, err error) {
	if item.Op == model.OpNoop {
		return
	}
	op := string(item.Op)
	if err != nil {
		pushErrorsTotal.WithLabelValues(item.AccountID, op).Inc()
		return
	}
	pushLastSuccessTimestamp.WithLabelValues(item.AccountID, op).SetToCurrentTime()
}
