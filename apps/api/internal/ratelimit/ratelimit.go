// Package ratelimit throttles how often the same key may act, in memory,
// with no external dependency — the same "the database every deployment
// already has is enough" reasoning internal/crew's own doc comment uses,
// except this needs no store at all.
//
// It exists for the endpoints that proxy a sign-in to a third party
// (Garmin, Komoot) or start this app's own (OIDC): without it, this server
// is an unlimited, authenticated-only credential-stuffing proxy against
// whichever Garmin or Komoot account an attacker points it at, and nothing
// slows down a script hammering /sso/login. A determined attacker with many
// keys — many rider accounts, many source IPs — is not who this stops;
// fixed windows are coarse abuse throttling, not a fairness guarantee.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter allows at most Max calls to Allow for the same key inside any
// Window-long span, using a fixed window per key rather than a sliding one
// or a token bucket — one map lookup, no background goroutine, adequate for
// what this exists to slow down.
type Limiter struct {
	max    int
	window time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	count      int
	windowFrom time.Time
}

// New returns a Limiter permitting max calls per key per window.
func New(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, buckets: map[string]*bucket{}}
}

// Allow reports whether key may proceed right now, recording the attempt if
// so. An empty key always returns true — callers with no key to limit on
// (an anonymous request behind a proxy that stripped its own IP headers,
// say) should fail open rather than accidentally share one global bucket.
func (l *Limiter) Allow(key string) bool {
	if key == "" {
		return true
	}

	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok || now.Sub(b.windowFrom) >= l.window {
		l.buckets[key] = &bucket{count: 1, windowFrom: now}
		l.evictLocked(now)
		return true
	}
	if b.count >= l.max {
		return false
	}
	b.count++
	return true
}

// evictLocked drops buckets whose window closed at least one window ago —
// called only when a fresh bucket is created, so its cost is proportional
// to the number of distinct keys seen recently, not to every Allow call.
// Without it, a key used once (a one-off IP, a rider who signs in and never
// returns) would sit in the map forever.
func (l *Limiter) evictLocked(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.windowFrom) >= 2*l.window {
			delete(l.buckets, k)
		}
	}
}
