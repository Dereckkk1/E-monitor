package auth

// ratelimit_test.go — TDD coverage for the login rate limiter (audit
// 2026-07-21 H-04). The login endpoint was public with no throttling, leaving
// it open to brute force / credential stuffing (CWE-307).

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestLoginRateLimiter_AllowsUpToMax lets the first `max` attempts through and
// blocks the next one within the same window.
func TestLoginRateLimiter_AllowsUpToMax(t *testing.T) {
	l := NewLoginRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if l.Allow("1.2.3.4") {
		t.Fatalf("4th attempt should be blocked (max=3)")
	}
}

// TestLoginRateLimiter_PerKeyIsolation ensures one IP hitting its limit does
// not throttle a different IP.
func TestLoginRateLimiter_PerKeyIsolation(t *testing.T) {
	l := NewLoginRateLimiter(1, time.Minute)
	if !l.Allow("10.0.0.1") {
		t.Fatalf("first IP first attempt should pass")
	}
	if l.Allow("10.0.0.1") {
		t.Fatalf("first IP second attempt should be blocked")
	}
	if !l.Allow("10.0.0.2") {
		t.Fatalf("second IP should be independent and allowed")
	}
}

// TestLoginRateLimiter_WindowResets confirms the counter frees up once the
// window elapses.
func TestLoginRateLimiter_WindowResets(t *testing.T) {
	l := NewLoginRateLimiter(1, 20*time.Millisecond)
	if !l.Allow("9.9.9.9") {
		t.Fatalf("first attempt should pass")
	}
	if l.Allow("9.9.9.9") {
		t.Fatalf("second attempt within window should be blocked")
	}
	time.Sleep(30 * time.Millisecond)
	if !l.Allow("9.9.9.9") {
		t.Fatalf("attempt after window reset should pass")
	}
}

// TestLoginRateLimit_Middleware429 wires the limiter as HTTP middleware and
// asserts the (max+1)th request from the same client gets 429 while the
// wrapped handler runs for the allowed ones.
func TestLoginRateLimit_Middleware429(t *testing.T) {
	l := NewLoginRateLimiter(2, time.Minute)
	served := 0
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		w.WriteHeader(http.StatusOK)
	}))

	code := func() int {
		req := httptest.NewRequest("POST", "/v1/internal/auth/login", nil)
		req.RemoteAddr = "203.0.113.7:5555"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	if got := code(); got != http.StatusOK {
		t.Fatalf("attempt 1: got %d, want 200", got)
	}
	if got := code(); got != http.StatusOK {
		t.Fatalf("attempt 2: got %d, want 200", got)
	}
	if got := code(); got != http.StatusTooManyRequests {
		t.Fatalf("attempt 3: got %d, want 429", got)
	}
	if served != 2 {
		t.Fatalf("wrapped handler ran %d times, want 2 (3rd throttled)", served)
	}
}
