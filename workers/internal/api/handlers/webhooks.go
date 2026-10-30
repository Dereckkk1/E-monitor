// Package handlers — webhooks.go: HTTP surface for the webhook subsystem
// (§13.1.4). Wired into the internal router under /v1/internal/clients/{id}.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/acoes"
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

// servico monta as ações de webhook sobre o repositório e a outbox deste handler.
// A outbox só entra quando existe (um *webhook.Outbox nil dentro da interface
// passaria no `!= nil`).
func (h *WebhooksHandler) servico() *acoes.Servico {
	s := &acoes.Servico{Clientes: h.Clients}
	if h.Outbox != nil {
		s.Webhooks = h.Outbox
	}
	return s
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
	// Validação (URL, SSRF, segredo, eventos) e gravação moram em
	// acoes.EditarWebhook — o MCP chama a mesma função.
	cfg, err := h.servico().EditarWebhook(r.Context(), id, in)
	if err != nil {
		if !escreveRecusa(w, err) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, toDTO(cfg))
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

	out, _, err := h.Clients.ListWebhookDeliveries(r.Context(), id, status, limit, 0)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// SendTest handles POST /v1/internal/clients/{id}/webhook-test. Enqueues a
// `webhook.test` event so the operator can validate the receiver without
// waiting for a real detection.
func (h *WebhooksHandler) SendTest(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.servico().TestarWebhook(r.Context(), id); err != nil {
		if errors.Is(err, acoes.ErrWebhooksIndisponivel) {
			http.Error(w, "outbox not configured", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "enqueue failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}
