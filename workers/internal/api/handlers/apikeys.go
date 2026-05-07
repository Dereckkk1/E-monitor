package handlers

import (
	"encoding/json"
	"net/http"
	"net/url"

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

func (h *APIKeysHandler) SetWebhook(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "clientID")
	var body struct {
		WebhookURL string `json:"webhook_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.WebhookURL != "" {
		parsed, err := url.ParseRequestURI(body.WebhookURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			http.Error(w, "invalid webhook_url: must be http or https", http.StatusBadRequest)
			return
		}
	}
	// Generate a fresh random secret for this webhook
	webhookSecret, _ := auth.GenerateAPIKey()
	_, err := h.db.Exec(r.Context(), `
		UPDATE clients SET webhook_url = $2, webhook_secret = $3 WHERE id = $1
	`, clientID, body.WebhookURL, webhookSecret)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"webhook_secret": webhookSecret})
}
