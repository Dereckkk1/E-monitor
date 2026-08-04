package auth

// apikey_scope_test.go — regression coverage for the external client API
// (/v1/*) tenant isolation. Before the 2026-07-21 security audit fix, the
// API-key-authenticated routes reused the internal handlers, which resolve the
// tenant via ClientScopesFromContext (JWT claims). An API-key request carries no
// JWT claims, so ClientScopesFromContext returned nil ("see everything"),
// letting any client's key read every client's detections (BOLA / CWE-639).
//
// APIKeyViewerScope closes that gap by turning the key's client_id into
// synthetic viewer claims, so the exact same, already-tested scope enforcement
// runs on the external surface.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// ctxWithClientID mirrors what APIKeyMiddleware records after a successful
// key lookup (clientIDKey = client_id::text). White-box test: clientIDKey is
// unexported, so this lives in package auth.
func ctxWithClientID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, clientIDKey, id)
}

// TestAPIKeyViewerScope_ScopesToKeyClient asserts that after the middleware
// runs, a downstream handler resolving the tenant via ClientScopesFromContext
// sees the API key's own client as a portfolio of exactly one — never nil
// (which would leak all tenants).
func TestAPIKeyViewerScope_ScopesToKeyClient(t *testing.T) {
	client := uuid.New()

	var resolved []uuid.UUID
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		resolved = ClientScopesFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/v1/detections", nil)
	req = req.WithContext(ctxWithClientID(req.Context(), client.String()))
	rr := httptest.NewRecorder()

	APIKeyViewerScope(next).ServeHTTP(rr, req)

	if !called {
		t.Fatalf("next handler was not called (status %d)", rr.Code)
	}
	if resolved == nil {
		t.Fatalf("ClientScopesFromContext returned nil — request is unscoped (BOLA)")
	}
	if len(resolved) != 1 {
		t.Fatalf("expected a single-client portfolio, got %d clients: %v", len(resolved), resolved)
	}
	if resolved[0] != client {
		t.Fatalf("scoped to wrong client: got %s, want %s", resolved[0], client)
	}
}

// TestAPIKeyViewerScope_FailClosedWithoutClientID asserts that a request that
// somehow reaches the middleware without a client_id in context is rejected
// (401), never passed through unscoped.
func TestAPIKeyViewerScope_FailClosedWithoutClientID(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/v1/detections", nil)
	rr := httptest.NewRecorder()

	APIKeyViewerScope(next).ServeHTTP(rr, req)

	if called {
		t.Fatalf("next handler should NOT run without a client_id (unscoped request)")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 fail-closed, got %d", rr.Code)
	}
}

// TestAPIKeyViewerScope_FailClosedInvalidClientID asserts a malformed client_id
// is rejected rather than silently dropped to nil scope.
func TestAPIKeyViewerScope_FailClosedInvalidClientID(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/v1/detections", nil)
	req = req.WithContext(ctxWithClientID(req.Context(), "not-a-uuid"))
	rr := httptest.NewRecorder()

	APIKeyViewerScope(next).ServeHTTP(rr, req)

	if called {
		t.Fatalf("next handler should NOT run with a malformed client_id")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 fail-closed, got %d", rr.Code)
	}
}
