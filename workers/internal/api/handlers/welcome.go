package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/auth"
	"radiocheck/internal/welcome"
)

// WelcomeHandler serve o convite de boas-vindas.
//
// Duas superfícies bem diferentes convivem aqui:
//   - Resolve: PÚBLICO, sem JWT. É o que a página /boasvindas/:token consome.
//   - Revoke: admin-only, corta um link vazado.
//
// Ver docs/features/welcome-onboarding.md.
type WelcomeHandler struct {
	svc *welcome.Service
}

func NewWelcomeHandler(svc *welcome.Service) *WelcomeHandler {
	return &WelcomeHandler{svc: svc}
}

// Resolve devolve nome, email, senha inicial e contexto pra renderizar a
// página pública de boas-vindas.
//
// GET /v1/internal/public/welcome/{token}
//
// Token inválido, revogado ou de usuário excluído → 404. Nunca 401: um 401
// faria o interceptor do axios limpar a sessão e redirecionar pro /login
// (frontend/src/api/client.js), quebrando uma página que por definição é
// visitada sem sessão.
func (h *WelcomeHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "not_found", http.StatusNotFound)
		return
	}
	res, err := h.svc.Resolve(r.Context(), token)
	if err != nil {
		switch {
		case errors.Is(err, welcome.ErrNotFound):
			http.Error(w, "not_found", http.StatusNotFound)
		case errors.Is(err, welcome.ErrDisabled):
			http.Error(w, "welcome_disabled", http.StatusServiceUnavailable)
		default:
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	// Dado sensível (senha em claro): fora de qualquer cache intermediário.
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, res)
}

// Revoke invalida um convite: apaga a senha cifrada e carimba revoked_at.
// A partir daí o link responde 404. É o único jeito de cortar um convite,
// já que ele não expira por tempo.
//
// POST /v1/internal/admin/welcome-invites/{id}/revoke
func (h *WelcomeHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid_id", http.StatusBadRequest)
		return
	}
	var by uuid.UUID
	if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
		by = claims.UserID
	}
	if err := h.svc.Repo().Revoke(r.Context(), id, by); err != nil {
		if errors.Is(err, welcome.ErrNotFound) {
			http.Error(w, "not_found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
