package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

// TestMaskSecret covers the helper that builds the safe-to-expose DTO.
func TestMaskSecret(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"abc", "..."},                   // <= 4 chars
		{"abcd", "..."},                  // exactly 4 chars
		{"abcde", "abcd..."},             // > 4 chars
		{"this-is-a-very-strong-secret", "this..."},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := maskSecret(c.in); got != c.want {
				t.Fatalf("maskSecret(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestToDTO_Defaults_NilCfg verifies the function tolerates a nil config and
// returns the canonical default subscription set.
func TestToDTO_NilCfg(t *testing.T) {
	dto := toDTO(nil)
	if len(dto.Events) != 1 || dto.Events[0] != "detection.confirmed" {
		t.Fatalf("default events = %v, want [detection.confirmed]", dto.Events)
	}
	if dto.URL != "" || dto.Enabled || dto.HasSecret {
		t.Fatalf("nil cfg dto has unexpected fields set: %+v", dto)
	}
}

// TestToDTO_FromConfig confirms field mapping and that the secret is masked.
func TestToDTO_FromConfig(t *testing.T) {
	cfg := &catalog.WebhookConfig{
		URL:     "https://example.com/hook",
		Secret:  "super-strong-secret-32-chars",
		Enabled: true,
		Events:  []string{"detection.confirmed", "detection.retracted"},
	}
	dto := toDTO(cfg)
	if dto.URL != cfg.URL {
		t.Fatalf("URL = %q", dto.URL)
	}
	if !dto.HasSecret {
		t.Fatalf("HasSecret = false; expected true for a present secret")
	}
	if !dto.Enabled {
		t.Fatalf("Enabled = false")
	}
	if dto.SecretMasked == cfg.Secret {
		t.Fatalf("SecretMasked must not be the raw secret")
	}
	if !strings.HasPrefix(dto.SecretMasked, "supe") || !strings.HasSuffix(dto.SecretMasked, "...") {
		t.Fatalf("SecretMasked = %q; want supe...", dto.SecretMasked)
	}
}

// TestValidEvent enumerates all currently-accepted event identifiers and
// confirms unknown ones are rejected.
func TestValidEvent(t *testing.T) {
	for _, e := range []string{"detection.confirmed", "detection.retracted", "webhook.test", "*"} {
		if !validEvent(e) {
			t.Errorf("validEvent(%q) = false, want true", e)
		}
	}
	for _, e := range []string{"", "DETECTION.CONFIRMED", "unknown.event", "detection.*"} {
		if validEvent(e) {
			t.Errorf("validEvent(%q) = true, want false", e)
		}
	}
}

// TestWebhooks_GetConfig_InvalidID returns 400 before touching the repo.
func TestWebhooks_GetConfig_InvalidID(t *testing.T) {
	h := &WebhooksHandler{}
	r := chi.NewRouter()
	r.Get("/clients/{id}/webhook", h.GetConfig)

	req := httptest.NewRequest(http.MethodGet, "/clients/garbage/webhook", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestWebhooks_PatchConfig_InvalidID short-circuits with 400.
func TestWebhooks_PatchConfig_InvalidID(t *testing.T) {
	h := &WebhooksHandler{}
	r := chi.NewRouter()
	r.Patch("/clients/{id}/webhook", h.PatchConfig)

	req := httptest.NewRequest(http.MethodPatch, "/clients/not-uuid/webhook",
		strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestWebhooks_PatchConfig_BadJSON rejects malformed body with 400.
func TestWebhooks_PatchConfig_BadJSON(t *testing.T) {
	h := &WebhooksHandler{}
	r := chi.NewRouter()
	r.Patch("/clients/{id}/webhook", h.PatchConfig)

	id := uuid.New()
	req := httptest.NewRequest(http.MethodPatch, "/clients/"+id.String()+"/webhook",
		strings.NewReader(`{not json`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestWebhooks_PatchConfig_BadURL rejects unparseable URLs with 400.
func TestWebhooks_PatchConfig_BadURL(t *testing.T) {
	h := &WebhooksHandler{}
	r := chi.NewRouter()
	r.Patch("/clients/{id}/webhook", h.PatchConfig)

	id := uuid.New()
	body, _ := json.Marshal(map[string]any{"webhook_url": "::::not a url::::"})
	req := httptest.NewRequest(http.MethodPatch, "/clients/"+id.String()+"/webhook",
		strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestWebhooks_PatchConfig_RejectsHTTPInProd verifies that the production
// guard refuses plaintext URLs (HMAC-signed payload would leak in transit).
func TestWebhooks_PatchConfig_RejectsHTTPInProd(t *testing.T) {
	t.Setenv("RADIOCHECK_ENV", "production")
	h := &WebhooksHandler{}
	r := chi.NewRouter()
	r.Patch("/clients/{id}/webhook", h.PatchConfig)

	id := uuid.New()
	body, _ := json.Marshal(map[string]any{"webhook_url": "http://api.example.com/hook"})
	req := httptest.NewRequest(http.MethodPatch, "/clients/"+id.String()+"/webhook",
		strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "http URLs are not allowed") {
		t.Fatalf("body should mention http URLs: %s", rec.Body.String())
	}
}

// TestWebhooks_PatchConfig_ShortSecret rejects secrets shorter than 16 bytes.
func TestWebhooks_PatchConfig_ShortSecret(t *testing.T) {
	t.Setenv("RADIOCHECK_ENV", "production")
	h := &WebhooksHandler{}
	r := chi.NewRouter()
	r.Patch("/clients/{id}/webhook", h.PatchConfig)

	id := uuid.New()
	body, _ := json.Marshal(map[string]any{"webhook_secret": "tooShort"})
	req := httptest.NewRequest(http.MethodPatch, "/clients/"+id.String()+"/webhook",
		strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "16 characters") {
		t.Fatalf("body should mention the 16-char minimum: %s", rec.Body.String())
	}
}

// TestWebhooks_PatchConfig_UnknownEvent rejects subscriptions to unknown
// event types with 400.
func TestWebhooks_PatchConfig_UnknownEvent(t *testing.T) {
	t.Setenv("RADIOCHECK_ENV", "production")
	h := &WebhooksHandler{}
	r := chi.NewRouter()
	r.Patch("/clients/{id}/webhook", h.PatchConfig)

	id := uuid.New()
	body, _ := json.Marshal(map[string]any{
		"webhook_events": []string{"detection.confirmed", "totally.bogus"},
	})
	req := httptest.NewRequest(http.MethodPatch, "/clients/"+id.String()+"/webhook",
		strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "totally.bogus") {
		t.Fatalf("body should name the unknown event: %s", rec.Body.String())
	}
}

// TestWebhooks_ListDeliveries_InvalidID short-circuits with 400.
func TestWebhooks_ListDeliveries_InvalidID(t *testing.T) {
	h := &WebhooksHandler{}
	r := chi.NewRouter()
	r.Get("/clients/{id}/webhook-deliveries", h.ListDeliveries)

	req := httptest.NewRequest(http.MethodGet, "/clients/garbage/webhook-deliveries", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestWebhooks_SendTest_NoOutboxReturns503 verifies the documented 503 path
// when the outbox is unconfigured.
func TestWebhooks_SendTest_NoOutboxReturns503(t *testing.T) {
	h := &WebhooksHandler{}
	r := chi.NewRouter()
	r.Post("/clients/{id}/webhook-test", h.SendTest)

	id := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/clients/"+id.String()+"/webhook-test", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// TestClients_Create_BadJSON rejects malformed body with 400.
func TestClients_Create_BadJSON(t *testing.T) {
	h := &ClientsHandler{}
	req := httptest.NewRequest(http.MethodPost, "/clients", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestClients_Create_MissingName rejects empty name with 400.
func TestClients_Create_MissingName(t *testing.T) {
	h := &ClientsHandler{}
	req := httptest.NewRequest(http.MethodPost, "/clients", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestClients_Delete_InvalidID short-circuits with 400.
func TestClients_Delete_InvalidID(t *testing.T) {
	h := &ClientsHandler{}
	r := chi.NewRouter()
	r.Delete("/clients/{id}", h.Delete)

	req := httptest.NewRequest(http.MethodDelete, "/clients/garbage", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestClients_Update_InvalidID short-circuits with 400.
func TestClients_Update_InvalidID(t *testing.T) {
	h := &ClientsHandler{}
	r := chi.NewRouter()
	r.Patch("/clients/{id}", h.Update)

	req := httptest.NewRequest(http.MethodPatch, "/clients/garbage",
		strings.NewReader(`{"name":"x"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestClients_Update_BadJSON rejects malformed body with 400.
func TestClients_Update_BadJSON(t *testing.T) {
	h := &ClientsHandler{}
	r := chi.NewRouter()
	r.Patch("/clients/{id}", h.Update)

	id := uuid.New()
	req := httptest.NewRequest(http.MethodPatch, "/clients/"+id.String(),
		strings.NewReader(`{not-json`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestClients_Update_MissingName rejects empty name with 400 even on valid id.
func TestClients_Update_MissingName(t *testing.T) {
	h := &ClientsHandler{}
	r := chi.NewRouter()
	r.Patch("/clients/{id}", h.Update)

	id := uuid.New()
	req := httptest.NewRequest(http.MethodPatch, "/clients/"+id.String(),
		strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
