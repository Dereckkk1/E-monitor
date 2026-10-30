package handlers

// webhooks_acoes_lock_test.go — TRAVA o que as rotas de configuração e teste de
// webhook fazem hoje (status, DTO com segredo mascarado, linha no banco, linha
// na outbox), ANTES de a lógica sair do handler para internal/acoes (Fatia 4,
// Task 14). Roda com as regras de produção (RADIOCHECK_ENV vazio); a URL usa IP
// público literal para a conferência de SSRF não depender de DNS.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/webhook"
)

type cenarioWebhook struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	cliente uuid.UUID
	r       chi.Router
}

func novoCenarioWebhook(t *testing.T) *cenarioWebhook {
	t.Helper()
	t.Setenv("RADIOCHECK_ENV", "")
	ctx, pool := mcptest.DBCatalogo(t)
	clients := catalog.NewClients(pool)
	h := NewWebhooksHandler(pool, clients, webhook.NewOutbox(pool, clients, zap.NewNop()))
	r := chi.NewRouter()
	r.Patch("/clients/{id}/webhook", h.PatchConfig)
	r.Post("/clients/{id}/webhook-test", h.SendTest)
	return &cenarioWebhook{ctx: ctx, pool: pool, cliente: mcptest.Cliente(t, ctx, pool, "Cliente Webhook"), r: r}
}

func (c *cenarioWebhook) chamar(metodo, alvo, corpo string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c.r.ServeHTTP(rec, httptest.NewRequest(metodo, alvo, strings.NewReader(corpo)))
	return rec
}

const corpoWebhookOk = `{"webhook_url":"https://8.8.8.8/hook","webhook_secret":"segredo-com-16-chars!","webhook_enabled":true,"webhook_events":["detection.confirmed","webhook.test"]}`

func TestWebhookHTTP_PatchGravaEMascaraSegredo(t *testing.T) {
	c := novoCenarioWebhook(t)
	rec := c.chamar(http.MethodPatch, "/clients/"+c.cliente.String()+"/webhook", corpoWebhookOk)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var dto map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.Equal(t, "https://8.8.8.8/hook", dto["webhook_url"])
	require.Equal(t, true, dto["webhook_enabled"])
	require.Equal(t, true, dto["has_secret"])
	require.Equal(t, []any{"detection.confirmed", "webhook.test"}, dto["webhook_events"])
	require.NotContains(t, rec.Body.String(), "segredo-com-16-chars!", "o segredo nunca volta inteiro")
	require.Equal(t, "segr...", dto["webhook_secret_masked"])

	var url, segredo string
	var ativo bool
	var eventos []string
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT webhook_url, webhook_secret, webhook_enabled, webhook_events FROM clients WHERE id=$1`, c.cliente,
	).Scan(&url, &segredo, &ativo, &eventos))
	require.Equal(t, "https://8.8.8.8/hook", url)
	require.Equal(t, "segredo-com-16-chars!", segredo)
	require.True(t, ativo)
	require.Equal(t, []string{"detection.confirmed", "webhook.test"}, eventos)
}

func TestWebhookHTTP_PatchParcialNaoMexeNoResto(t *testing.T) {
	c := novoCenarioWebhook(t)
	require.Equal(t, http.StatusOK, c.chamar(http.MethodPatch, "/clients/"+c.cliente.String()+"/webhook", corpoWebhookOk).Code)
	rec := c.chamar(http.MethodPatch, "/clients/"+c.cliente.String()+"/webhook", `{"webhook_enabled":false}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var dto map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.Equal(t, false, dto["webhook_enabled"])
	require.Equal(t, "https://8.8.8.8/hook", dto["webhook_url"])
}

func TestWebhookHTTP_Patch404(t *testing.T) {
	c := novoCenarioWebhook(t)
	rec := c.chamar(http.MethodPatch, "/clients/"+uuid.NewString()+"/webhook", corpoWebhookOk)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "not found")
}

func TestWebhookHTTP_PatchRecusasComTexto(t *testing.T) {
	c := novoCenarioWebhook(t)
	alvo := "/clients/" + c.cliente.String() + "/webhook"
	for corpo, texto := range map[string]string{
		`{"webhook_url":"ftp://x"}`:                               "webhook_url must be a valid http(s) URL",
		`{"webhook_url":"http://8.8.8.8/h"}`:                      "http URLs are not allowed in production",
		`{"webhook_url":"https://10.0.0.5/h"}`:                    "webhook_url rejected: ",
		`{"webhook_secret":"curto"}`:                              "webhook_secret must be at least 16 characters",
		`{"webhook_events":["detection.confirmed","coisa.rara"]}`: "unknown event: coisa.rara",
	} {
		rec := c.chamar(http.MethodPatch, alvo, corpo)
		require.Equal(t, http.StatusBadRequest, rec.Code, corpo)
		require.Contains(t, rec.Body.String(), texto, corpo)
	}
}

func (c *cenarioWebhook) entregas(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, c.pool.QueryRow(c.ctx,
		`SELECT COUNT(*) FROM webhook_deliveries WHERE client_id=$1 AND event_type='webhook.test'`, c.cliente).Scan(&n))
	return n
}

func TestWebhookHTTP_SendTest202EnfileiraEntrega(t *testing.T) {
	c := novoCenarioWebhook(t)
	require.Equal(t, http.StatusOK, c.chamar(http.MethodPatch, "/clients/"+c.cliente.String()+"/webhook", corpoWebhookOk).Code)

	rec := c.chamar(http.MethodPost, "/clients/"+c.cliente.String()+"/webhook-test", "")
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"queued":true}`, rec.Body.String())
	require.Equal(t, 1, c.entregas(t))
}

func TestWebhookHTTP_SendTestSemConfigNaoEnfileiraMas202(t *testing.T) {
	c := novoCenarioWebhook(t)
	rec := c.chamar(http.MethodPost, "/clients/"+c.cliente.String()+"/webhook-test", "")
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	require.Equal(t, 0, c.entregas(t), "cliente sem URL/habilitação: a outbox é no-op")
}

func TestWebhookHTTP_SendTestIDInvalido400(t *testing.T) {
	c := novoCenarioWebhook(t)
	rec := c.chamar(http.MethodPost, "/clients/xyz/webhook-test", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
