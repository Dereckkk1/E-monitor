package api

// router_authz_test.go — verifies that role-gated mutation endpoints
// (webhook config / webhook test / campaign lifecycle transitions) are
// admin-only after the security review of 2026-05-07.
//
// The webhook + campaign mutations were previously open to operator+admin.
// Until per-client tenancy (operator <-> client mapping) is implemented,
// admin is the safe default for cross-client mutation endpoints.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/auth"
)

// minimalAdminRouter mirrors the structure under /v1/internal/clients/{id}
// and /v1/internal/campaigns/{id} from NewRouter, but using stub handlers
// so we don't need a DB connection to assert authz behaviour.
func minimalAdminRouter() http.Handler {
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	r := chi.NewRouter()
	r.Route("/v1/internal", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireJWT)
			r.Use(auth.RequireRole("admin", "operator"))

			// Reads stay open to operator+admin.
			r.Get("/clients/{id}/webhook", stub)
			r.Get("/clients/{id}/webhook-deliveries", stub)

			// Mutations: admin-only.
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole("admin"))
				r.Patch("/clients/{id}/webhook", stub)
				r.Post("/clients/{id}/webhook-test", stub)
				r.Post("/campaigns/{id}/cancel", stub)
				r.Put("/campaigns/{id}/start", stub)
				r.Put("/campaigns/{id}/pause", stub)
			})
		})
	})
	return r
}

func issue(t *testing.T, role string) string {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	tok, err := auth.IssueToken(uuid.New(), role)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}

func do(t *testing.T, h http.Handler, method, path, token string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}

// TestMutations_RequireAdmin asserts that webhook config/test and campaign
// lifecycle transitions return 403 to operator JWTs and 204 to admin JWTs.
func TestMutations_RequireAdmin(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := minimalAdminRouter()
	op := issue(t, "operator")
	ad := issue(t, "admin")

	id := uuid.New().String()
	cases := []struct {
		method, path string
	}{
		{"PATCH", "/v1/internal/clients/" + id + "/webhook"},
		{"POST", "/v1/internal/clients/" + id + "/webhook-test"},
		{"POST", "/v1/internal/campaigns/" + id + "/cancel"},
		{"PUT", "/v1/internal/campaigns/" + id + "/start"},
		{"PUT", "/v1/internal/campaigns/" + id + "/pause"},
	}
	for _, c := range cases {
		t.Run("operator-blocked "+c.method+" "+c.path, func(t *testing.T) {
			if got := do(t, h, c.method, c.path, op); got != http.StatusForbidden {
				t.Fatalf("operator should get 403, got %d", got)
			}
		})
		t.Run("admin-allowed "+c.method+" "+c.path, func(t *testing.T) {
			if got := do(t, h, c.method, c.path, ad); got != http.StatusNoContent {
				t.Fatalf("admin should get 204, got %d", got)
			}
		})
	}
}

// TestReads_OpenToOperator confirms the reads remain accessible to operator
// JWTs (not regressed alongside the mutation lock-down).
func TestReads_OpenToOperator(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := minimalAdminRouter()
	op := issue(t, "operator")

	id := uuid.New().String()
	for _, p := range []string{
		"/v1/internal/clients/" + id + "/webhook",
		"/v1/internal/clients/" + id + "/webhook-deliveries",
	} {
		if got := do(t, h, "GET", p, op); got != http.StatusNoContent {
			t.Errorf("operator GET %s: got %d, want 204", p, got)
		}
	}
}

// TestUnauthenticated_Rejected confirms the JWT layer still kicks in before
// role checks (no token = 401, not 403).
func TestUnauthenticated_Rejected(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := minimalAdminRouter()
	id := uuid.New().String()
	if got := do(t, h, "PATCH", "/v1/internal/clients/"+id+"/webhook", ""); got != http.StatusUnauthorized {
		t.Fatalf("no token = expected 401, got %d", got)
	}
}
