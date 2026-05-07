package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"radiocheck/internal/auth"
)

type APIKeysHandler struct{ db *pgxpool.Pool }

func NewAPIKeysHandler(db *pgxpool.Pool) *APIKeysHandler { return &APIKeysHandler{db: db} }

func (h *APIKeysHandler) List(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "clientID")
	rows, err := h.db.Query(r.Context(), `
		SELECT id::text, LEFT(key_hash, 8)||'...', created_at, last_used_at
		FROM api_keys WHERE client_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC
	`, clientID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var keys []map[string]interface{}
	for rows.Next() {
		var id, masked string
		var created, lastUsed interface{}
		if err := rows.Scan(&id, &masked, &created, &lastUsed); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		keys = append(keys, map[string]interface{}{"id": id, "key_masked": masked, "created_at": created, "last_used_at": lastUsed})
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if keys == nil {
		keys = []map[string]interface{}{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(keys)
}

func (h *APIKeysHandler) Create(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "clientID")
	raw, hash := auth.GenerateAPIKey()
	id := uuid.New()
	_, err := h.db.Exec(r.Context(), `
		INSERT INTO api_keys (id, client_id, key_hash) VALUES ($1, $2, $3)
	`, id, clientID, hash)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id.String(), "key": raw})
}

func (h *APIKeysHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "keyID")
	_, err := h.db.Exec(r.Context(), `UPDATE api_keys SET revoked_at = NOW() WHERE id = $1`, keyID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Webhook configuration is owned by handlers/webhooks.go (PatchConfig). The
// previous SetWebhook method here was dead code — no router mounted it and
// the frontend never called it. Removed to avoid confusion.
