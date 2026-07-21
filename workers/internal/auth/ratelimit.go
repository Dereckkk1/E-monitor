package auth

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// LoginRateLimiter is a fixed-window, in-memory rate limiter keyed by client
// IP, used to throttle the public login endpoint against brute force /
// credential stuffing (audit 2026-07-21 H-04, CWE-307). It mirrors the
// windowed-counter approach already used by APIKeyMiddleware.checkRate.
//
// In-memory means per-process: with multiple API replicas the effective limit
// is max*replicas. That is an acceptable first line of defence — the goal is to
// kill unbounded hammering, not to be a distributed quota. Stale entries are
// swept opportunistically to bound memory.
type LoginRateLimiter struct {
	mu         sync.Mutex
	windows    map[string]*loginWindow
	max        int
	window     time.Duration
	sweepCount int
}

type loginWindow struct {
	count       int
	windowStart time.Time
}

// NewLoginRateLimiter returns a limiter allowing max attempts per window per
// key. A non-positive max disables limiting (Allow always true).
func NewLoginRateLimiter(max int, window time.Duration) *LoginRateLimiter {
	return &LoginRateLimiter{
		windows: make(map[string]*loginWindow),
		max:     max,
		window:  window,
	}
}

// Allow records an attempt for key and reports whether it is within the limit.
func (l *LoginRateLimiter) Allow(key string) bool {
	if l.max <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	// Opportunistic sweep of expired windows every 1000 calls.
	l.sweepCount++
	if l.sweepCount%1000 == 0 {
		for k, v := range l.windows {
			if time.Since(v.windowStart) > l.window {
				delete(l.windows, k)
			}
		}
	}

	e, ok := l.windows[key]
	if !ok || time.Since(e.windowStart) > l.window {
		l.windows[key] = &loginWindow{count: 1, windowStart: time.Now()}
		return true
	}
	e.count++
	return e.count <= l.max
}

// Middleware throttles requests by client IP, returning 429 with a Retry-After
// header when the limit is exceeded. Keying is by IP (host portion of
// RemoteAddr, which chi's RealIP populates from the trusted proxy).
func (l *LoginRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(clientIPKey(r)) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIPKey extracts the host portion of RemoteAddr, falling back to the raw
// value if it has no port.
func clientIPKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
