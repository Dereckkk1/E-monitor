package acoes

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"radiocheck/internal/catalog"
	"radiocheck/internal/webhook"
)

// ErrWebhooksIndisponivel: o Servico não tem outbox de webhook (TestarWebhook
// não tem como enfileirar). Não é Recusa — é configuração da instalação; a rota
// HTTP responde 503.
var ErrWebhooksIndisponivel = errors.New("outbox de webhook não configurada")

// EventoWebhookValido diz se e é um tipo de evento que o cliente pode assinar.
func EventoWebhookValido(e string) bool {
	switch e {
	case "detection.confirmed", "detection.retracted", "webhook.test", "*":
		return true
	}
	return false
}

// ValidarWebhook confere a configuração parcial do webhook: URL http(s) válida
// (http só em desenvolvimento e em loopback), fora de espaço privado (SSRF),
// segredo com pelo menos 16 caracteres e eventos conhecidos. Os campos ausentes
// não são conferidos. As mensagens são as da rota PATCH, que as devolve como
// 400 com o mesmo texto.
func ValidarWebhook(ctx context.Context, in catalog.UpdateWebhookInput) error {
	if in.URL != nil && *in.URL != "" {
		parsed, err := url.ParseRequestURI(*in.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return entradaInvalida("webhook_url must be a valid http(s) URL")
		}
		// HTTPS é exigido por padrão. http:// só vale com RADIOCHECK_ENV=development
		// E host de loopback (localhost, 127.0.0.1, ::1), para teste de integração
		// local: qualquer outro caso exporia em texto puro o payload assinado
		// (com os metadados da detecção).
		if !webhook.AllowInsecureURL(parsed) {
			return entradaInvalida("http URLs are not allowed in production; set RADIOCHECK_ENV=development for local testing")
		}
		// Guarda de SSRF: recusa URL que resolve para espaço privado, loopback ou
		// link-local. Em desenvolvimento o validador é no-op.
		if err := webhook.ValidateURLForSSRF(ctx, *in.URL); err != nil {
			return entradaInvalida("webhook_url rejected: " + err.Error())
		}
	}
	if in.Secret != nil && *in.Secret != "" && len(*in.Secret) < 16 {
		return entradaInvalida("webhook_secret must be at least 16 characters")
	}
	for _, e := range in.Events {
		if !EventoWebhookValido(e) {
			return entradaInvalida("unknown event: " + e)
		}
	}
	return nil
}

// EditarWebhook valida e grava a configuração parcial do webhook do cliente (os
// campos nil não mudam) e devolve a configuração resultante. Cliente
// inexistente: nao_encontrado.
func (s *Servico) EditarWebhook(ctx context.Context, cliente uuid.UUID, in catalog.UpdateWebhookInput) (*catalog.WebhookConfig, error) {
	if err := ValidarWebhook(ctx, in); err != nil {
		return nil, err
	}
	cfg, err := s.Clientes.UpdateWebhookConfig(ctx, cliente, in)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, naoEncontrado("not found")
		}
		return nil, err
	}
	return cfg, nil
}

// TestarWebhook enfileira um evento webhook.test para o cliente, para validar o
// receptor sem esperar uma detecção real. A outbox não enfileira nada se o
// cliente não tem URL, está desabilitado ou não assinou o evento (no-op, sem
// erro). Sem outbox no Servico: ErrWebhooksIndisponivel.
func (s *Servico) TestarWebhook(ctx context.Context, cliente uuid.UUID) error {
	if s.Webhooks == nil {
		return ErrWebhooksIndisponivel
	}
	return s.Webhooks.Enqueue(ctx, &webhook.Event{
		Type:     webhook.EventWebhookTest,
		ClientID: cliente,
		Body: map[string]any{
			"message":     "This is a test webhook from Radiocheck.",
			"sent_at":     time.Now().UTC().Format(time.RFC3339),
			"environment": "internal",
		},
	})
}
