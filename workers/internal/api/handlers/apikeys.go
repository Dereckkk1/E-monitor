package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

type APIKeysHandler struct{ db *pgxpool.Pool }

func NewAPIKeysHandler(db *pgxpool.Pool) *APIKeysHandler { return &APIKeysHandler{db: db} }

func (h *APIKeysHandler) List(w http.ResponseWriter, r *http.Request) {
	// Id malformado sempre deu 500 (o pgx recusava o texto como uuid); mantém.
	clientID, err := uuid.Parse(chi.URLParam(r, "clientID"))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	infos, err := catalog.NewClients(h.db).ListAPIKeys(r.Context(), clientID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	keys := make([]map[string]interface{}, 0, len(infos))
	for _, k := range infos {
		keys = append(keys, map[string]interface{}{
			"id": k.ID.String(), "key_masked": k.KeyMasked,
			"created_at": k.CreatedAt, "last_used_at": k.LastUsedAt,
		})
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
