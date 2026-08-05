package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
)

// pmmScopeRequest monta um GET /clients/{clientID}/target-pmm com o param de
// rota e as claims já no contexto.
func pmmScopeRequest(id uuid.UUID, claims *auth.Claims) *http.Request {
	req := httptest.NewRequest("GET", "/clients/"+id.String()+"/target-pmm", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientID", id.String())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	return req.WithContext(auth.ContextWithClaims(ctx, claims))
}

// Cliente fora da carteira leva 404 anti-oracle. O Repo nil prova que o guard
// barra ANTES de qualquer query — se ele deixasse passar, o teste entraria no
// repo e explodiria com nil pointer em vez de responder 404.
func TestClientTargetPmm_ForeignClientIs404(t *testing.T) {
	a, b, foreign := uuid.New(), uuid.New(), uuid.New()
	claims := &auth.Claims{Role: "viewer", ClientID: &a, ClientIDs: []uuid.UUID{a, b}}

	h := &ClientTargetPmmHandler{}
	rec := httptest.NewRecorder()
	h.List(rec, pmmScopeRequest(foreign, claims))

	require.Equal(t, http.StatusNotFound, rec.Code)
}

// Token legado (só client_id) segue autorizando o próprio cliente.
func TestClientTargetPmm_LegacyTokenForeignClientIs404(t *testing.T) {
	own, foreign := uuid.New(), uuid.New()
	claims := &auth.Claims{Role: "viewer", ClientID: &own}

	h := &ClientTargetPmmHandler{}
	rec := httptest.NewRecorder()
	h.List(rec, pmmScopeRequest(foreign, claims))

	require.Equal(t, http.StatusNotFound, rec.Code)
}
