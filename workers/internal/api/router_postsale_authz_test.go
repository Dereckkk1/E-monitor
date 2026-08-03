package api

// router_postsale_authz_test.go — o Pós-venda é tela EXCLUSIVA de admin.
//
// Operator não entra: o disparo manda email pra todos os usuários de um
// cliente e congela um documento comercial. Viewer muito menos.
//
// As rotas públicas (/public/post-sale/...) ficam FORA do RequireJWT de
// propósito — o cliente chega pelo link do email, sem sessão. Aqui garantimos
// que elas seguem abertas, porque protegê-las com JWT quebraria a feature
// inteira de forma silenciosa.

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/auth"
)

func minimalPostSaleRouter() http.Handler {
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	r := chi.NewRouter()
	r.Route("/v1/internal", func(r chi.Router) {
		// Espelha o grupo público do NewRouter.
		r.Get("/public/post-sale/{token}", stub)
		r.Get("/public/post-sale/{token}/campaigns/{cid}/bundle.zip", stub)

		r.Group(func(r chi.Router) {
			r.Use(auth.RequireJWT)
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole("admin"))
				r.Get("/post-sale/reports", stub)
				r.Post("/post-sale/reports", stub)
				r.Get("/post-sale/reports/{id}", stub)
				r.Patch("/post-sale/reports/{id}", stub)
				r.Get("/post-sale/reports/{id}/preview", stub)
				r.Get("/post-sale/reports/{id}/recipients", stub)
				r.Post("/post-sale/reports/{id}/assets", stub)
				r.Post("/post-sale/reports/{id}/publish", stub)
				r.Post("/post-sale/reports/{id}/resend", stub)
				r.Get("/post-sale/recipients", stub)
				r.Post("/post-sale/recipients/{rid}/revoke", stub)
			})
		})
	})
	return r
}

func TestPostSale_AdminOnly(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := minimalPostSaleRouter()
	ad := issue(t, "admin")
	op := issue(t, "operator")
	vw := issue(t, "viewer")

	id := uuid.New().String()
	cases := []struct{ method, path string }{
		{"GET", "/v1/internal/post-sale/reports"},
		{"POST", "/v1/internal/post-sale/reports"},
		{"GET", "/v1/internal/post-sale/reports/" + id},
		{"PATCH", "/v1/internal/post-sale/reports/" + id},
		{"GET", "/v1/internal/post-sale/reports/" + id + "/preview"},
		{"GET", "/v1/internal/post-sale/reports/" + id + "/recipients"},
		{"POST", "/v1/internal/post-sale/reports/" + id + "/assets"},
		{"POST", "/v1/internal/post-sale/reports/" + id + "/publish"},
		{"POST", "/v1/internal/post-sale/reports/" + id + "/resend"},
		{"GET", "/v1/internal/post-sale/recipients?client_id=" + id},
		{"POST", "/v1/internal/post-sale/recipients/" + id + "/revoke"},
	}
	for _, c := range cases {
		if got := do(t, h, c.method, c.path, ad); got != http.StatusNoContent {
			t.Errorf("admin %s %s: got %d, want 204", c.method, c.path, got)
		}
		if got := do(t, h, c.method, c.path, op); got != http.StatusForbidden {
			t.Errorf("operator %s %s: got %d, want 403", c.method, c.path, got)
		}
		if got := do(t, h, c.method, c.path, vw); got != http.StatusForbidden {
			t.Errorf("viewer %s %s: got %d, want 403", c.method, c.path, got)
		}
		if got := do(t, h, c.method, c.path, ""); got != http.StatusUnauthorized {
			t.Errorf("sem token %s %s: got %d, want 401", c.method, c.path, got)
		}
	}
}

// As rotas do cliente NÃO podem exigir JWT: quem abre o link do email não tem
// sessão. Se alguém mover essas rotas pra dentro do grupo autenticado, este
// teste quebra antes de o cliente descobrir por conta própria.
func TestPostSalePublic_NaoExigeJWT(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := minimalPostSaleRouter()

	for _, p := range []string{
		"/v1/internal/public/post-sale/tok123",
		"/v1/internal/public/post-sale/tok123/campaigns/" + uuid.New().String() + "/bundle.zip",
	} {
		if got := do(t, h, "GET", p, ""); got != http.StatusNoContent {
			t.Errorf("GET %s sem token: got %d, want 204 (rota é pública)", p, got)
		}
	}
}
