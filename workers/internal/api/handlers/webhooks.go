// Package handlers — webhooks.go: HTTP surface for the webhook subsystem
// (§13.1.4). Wired into the internal router under /v1/internal/clients/{id}.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/catalog"
	"radiocheck/internal/webhook"
)

// WebhooksHandler owns the webhook config + deliveries inspector + test
// dispatcher endpoints.
type WebhooksHandler struct {
	DB      *pgxpool.Pool
	Clients *catalog.Clients
	Outbox  *webhook.Outbox
}

// NewWebhooksHandler constructs the handler. Outbox is optional — if nil the
// /webhook-test endpoint will return 503.
func NewWebhooksHandler(db *pgxpool.Pool, clients *catalog.Clients, outbox *webhook.Outbox) *WebhooksHandler {
	return &WebhooksHandler{DB: db, Clients: clients, Outbox: outbox}
}

// webhookConfigDTO is the safe-to-expose representation of WebhookConfig:
// the secret is masked to the first 4 chars + ellipsis.
type webhookConfigDTO struct {
	URL          string   `json:"webhook_url"`
	SecretMasked string   `json:"webhook_secret_masked"`
	Enabled      bool     `json:"webhook_enabled"`
	Events       []string `json:"webhook_events"`
	HasSecret    bool     `json:"has_secret"`
}

func toDTO(cfg *catalog.WebhookConfig) webhookConfigDTO {
	if cfg == nil {
		return webhookConfigDTO{Events: []string{"detection.confirmed"}}
	}
	return webhookConfigDTO{
		URL:          cfg.URL,
		SecretMasked: maskSecret(cfg.Secret),
		Enabled:      cfg.Enabled,
		Events:       cfg.Events,
		HasSecret:    cfg.Secret != "",
	}
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "..."
	}
	return s[:4] + "..."
}

// GetConfig handles GET /v1/internal/clients/{id}/webhook.
func (h *WebhooksHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	cfg, err := h.Clients.GetWebhookConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, toDTO(cfg))
}

// PatchConfig handles PATCH /v1/internal/clients/{id}/webhook. All fields
// optional. Validation: URL must be http/https, secret >= 16 chars, events
// must be a known list.
func (h *WebhooksHandler) PatchConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var in catalog.UpdateWebhookInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if in.URL != nil && *in.URL != "" {
		parsed, err := url.ParseRequestURI(*in.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			http.Error(w, "webhook_url must be a valid http(s) URL", http.StatusBadRequest)
			return
		}
		// HTTPS is required by default. http:// is only accepted when
		// RADIOCHECK_ENV=development AND the host is loopback (localhost,
		// 127.0.0.1, ::1) — for local integration tests. Anything else
		// would expose the HMAC-signed payload (incl. detection metadata)
		// in plaintext on the wire.
		if !webhook.AllowInsecureURL(parsed) {
			http.Error(w,
				"http URLs are not allowed in production; set RADIOCHECK_ENV=development for local testing",
				http.StatusBadRequest)
			return
		}
		// SSRF guard: refuse URLs that resolve to private/loopback/link-local
		// space. In dev mode the validator is a no-op so localhost works.
		if err := webhook.ValidateURLForSSRF(r.Context(), *in.URL); err != nil {
			http.Error(w, "webhook_url rejected: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if in.Secret != nil && *in.Secret != "" && len(*in.Secret) < 16 {
		http.Error(w, "webhook_secret must be at least 16 characters", http.StatusBadRequest)
		return
	}
	if in.Events != nil {
		for _, e := range in.Events {
			if !validEvent(e) {
				http.Error(w, "unknown event: "+e, http.StatusBadRequest)
				return
			}
		}
	}

	cfg, err := h.Clients.UpdateWebhookConfig(r.Context(), id, in)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, toDTO(cfg))
}

func validEvent(e string) bool {
	switch e {
	case "detection.confirmed", "detection.retracted", "webhook.test", "*":
		return true
	}
	return false
}

// ListDeliveries handles GET /v1/internal/clients/{id}/webhook-deliveries.
// Query params: status (filter), limit (default 50, max 200).
func (h *WebhooksHandler) ListDeliveries(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))

	q := `
		SELECT id, event_type, status, attempt_count, response_code, last_error,
		       next_attempt_at, delivered_at, created_at, updated_at
		FROM webhook_deliveries
		WHERE client_id = $1
		  AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC
		LIMIT $3`
	rows, err := h.DB.Query(r.Context(), q, id, status, limit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type item struct {
		ID            uuid.UUID  `json:"id"`
		EventType     string     `json:"event_type"`
		Status        string     `json:"status"`
		AttemptCount  int        `json:"attempt_count"`
		ResponseCode  *int       `json:"response_code,omitempty"`
		LastError     *string    `json:"last_error,omitempty"`
		NextAttemptAt time.Time  `json:"next_attempt_at"`
		DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
		CreatedAt     time.Time  `json:"created_at"`
		UpdatedAt     time.Time  `json:"updated_at"`
	}
	out := make([]item, 0)
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.EventType, &it.Status, &it.AttemptCount,
			&it.ResponseCode, &it.LastError, &it.NextAttemptAt, &it.DeliveredAt,
			&it.CreatedAt, &it.UpdatedAt); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// SendTest handles POST /v1/internal/clients/{id}/webhook-test. Enqueues a
// `webhook.test` event so the operator can validate the receiver without
// waiting for a real detection.
func (h *WebhooksHandler) SendTest(w http.ResponseWriter, r *http.Request) {
	if h.Outbox == nil {
		http.Error(w, "outbox not configured", http.StatusServiceUnavailable)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	body := map[string]any{
		"message":     "This is a test webhook from Radiocheck.",
		"sent_at":     time.Now().UTC().Format(time.RFC3339),
		"environment": "internal",
	}
	if err := h.Outbox.Enqueue(r.Context(), &webhook.Event{
		Type:     webhook.EventWebhookTest,
		ClientID: id,
		Body:     body,
	}); err != nil {
		http.Error(w, "enqueue failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}
