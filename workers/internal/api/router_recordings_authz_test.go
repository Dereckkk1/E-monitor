package api

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/auth"
)

// Gravação é tela exclusiva de admin: operator e viewer não entram.
func minimalRecordingsRouter() http.Handler {
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	r := chi.NewRouter()
	r.Route("/v1/internal", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireJWT)
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole("admin"))
				r.Get("/recordings", stub)
				r.Post("/recordings", stub)
				r.Post("/recordings/{id}/cancel", stub)
				r.Delete("/recordings/{id}", stub)
				r.Get("/recordings/{id}/audio", stub)
			})
		})
	})
	return r
}

func TestRecordings_AdminOnly(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := minimalRecordingsRouter()
	id := uuid.NewString()
	routes := []struct{ method, path string }{
		{"GET", "/v1/internal/recordings"},
		{"POST", "/v1/internal/recordings"},
		{"POST", "/v1/internal/recordings/" + id + "/cancel"},
		{"DELETE", "/v1/internal/recordings/" + id},
		{"GET", "/v1/internal/recordings/" + id + "/audio"},
	}
	ad, op, vw := issue(t, "admin"), issue(t, "operator"), issue(t, "viewer")
	for _, rt := range routes {
		if got := do(t, h, rt.method, rt.path, ad); got != http.StatusNoContent {
			t.Errorf("admin %s %s: got %d, want 204", rt.method, rt.path, got)
		}
		if got := do(t, h, rt.method, rt.path, op); got != http.StatusForbidden {
			t.Errorf("operator %s %s: got %d, want 403", rt.method, rt.path, got)
		}
		if got := do(t, h, rt.method, rt.path, vw); got != http.StatusForbidden {
			t.Errorf("viewer %s %s: got %d, want 403", rt.method, rt.path, got)
		}
		if got := do(t, h, rt.method, rt.path, ""); got != http.StatusUnauthorized {
			t.Errorf("sem token %s %s: got %d, want 401", rt.method, rt.path, got)
		}
	}
}
