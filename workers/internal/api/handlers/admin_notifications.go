package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

// NotificationsRepo abstrai o catalog repo pra teste.
type NotificationsRepo interface {
	List(ctx context.Context, userID uuid.UUID) (*catalog.NotificationsResult, error)
	MarkRead(ctx context.Context, userID uuid.UUID, keys []string) (int, error)
	MarkAllReadInWindow(ctx context.Context, userID uuid.UUID) (int, error)
}

// NotificationsHandler powers /v1/internal/admin/notifications.
type NotificationsHandler struct {
	Repo NotificationsRepo
	Log  *slog.Logger
}

// userIDFromReq extrai o UserID das claims do JWT. Retorna ok=false se
// não há claims válidas no contexto — defensivo, normalmente o
// RequireJWT middleware garante a presença.
func userIDFromReq(r *http.Request) (uuid.UUID, bool) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok || claims == nil {
		return uuid.Nil, false
	}
	if claims.UserID == uuid.Nil {
		return uuid.Nil, false
	}
	return claims.UserID, true
}

// List serves GET /admin/notifications.
func (h *NotificationsHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromReq(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if h.Repo == nil {
		// Test harness sem DB — retorna vazio em vez de panicar.
		writeJSON(w, http.StatusOK, &catalog.NotificationsResult{Items: []catalog.Notification{}})
		return
	}
	res, err := h.Repo.List(r.Context(), userID)
	if err != nil {
		if h.Log != nil {
			h.Log.Error("notifications.list_failed", "error", err)
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type markReadReq struct {
	Keys []string `json:"keys"`
}

// MarkRead serves POST /admin/notifications/mark-read.
// Body: {"keys": ["campaign_failure:...:..."]}
func (h *NotificationsHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromReq(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body markReadReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	// Cap defensivo: ninguém deveria marcar > 200 de uma vez.
	if len(body.Keys) > 200 {
		http.Error(w, "too many keys", http.StatusBadRequest)
		return
	}
	if h.Repo == nil {
		writeJSON(w, http.StatusOK, map[string]int{"marked": 0})
		return
	}
	n, err := h.Repo.MarkRead(r.Context(), userID, body.Keys)
	if err != nil {
		if h.Log != nil {
			h.Log.Error("notifications.mark_read_failed", "error", err)
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"marked": n})
}

// MarkAllRead serves POST /admin/notifications/mark-all-read.
// No body. Server-side deriva os keys da janela atual.
func (h *NotificationsHandler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromReq(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if h.Repo == nil {
		writeJSON(w, http.StatusOK, map[string]int{"marked": 0})
		return
	}
	n, err := h.Repo.MarkAllReadInWindow(r.Context(), userID)
	if err != nil {
		if h.Log != nil {
			h.Log.Error("notifications.mark_all_read_failed", "error", err)
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"marked": n})
}
