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

// VisaoWebhook é a configuração de webhook SEM o segredo em claro: o que a rota
// e a tool podem devolver. O segredo só é gravado, nunca relido por quem edita.
type VisaoWebhook struct {
	URL     string
	Ativo   bool
	Eventos []string
	// TemSegredo diz se há segredo gravado; SegredoMascarado são os 4 primeiros
	// caracteres + "..." (ver MascararSegredo).
	TemSegredo       bool
	SegredoMascarado string
}

// MascararSegredo devolve os 4 primeiros caracteres + "..." ("..." se tiver 4 ou
// menos; vazio se não houver segredo).
func MascararSegredo(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "..."
	}
	return s[:4] + "..."
}

// VisaoDaConfigWebhook monta a visão a partir da configuração do banco. cfg nil
// (cliente sem URL) vira a configuração vazia com a assinatura padrão.
func VisaoDaConfigWebhook(cfg *catalog.WebhookConfig) *VisaoWebhook {
	if cfg == nil {
		return &VisaoWebhook{Eventos: []string{"detection.confirmed"}}
	}
	return &VisaoWebhook{
		URL: cfg.URL, Ativo: cfg.Enabled, Eventos: cfg.Events,
		TemSegredo: cfg.Secret != "", SegredoMascarado: MascararSegredo(cfg.Secret),
	}
}

// EditarWebhook valida e grava a configuração parcial do webhook do cliente (os
// campos nil não mudam) e devolve a configuração resultante, sem o segredo em
// claro. Cliente inexistente: nao_encontrado.
func (s *Servico) EditarWebhook(ctx context.Context, cliente uuid.UUID, in catalog.UpdateWebhookInput) (*VisaoWebhook, error) {
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
	return VisaoDaConfigWebhook(cfg), nil
}

// TesteWebhook é o desfecho de TestarWebhook. Enfileirado=false não é erro: o
// cliente não vai receber nada, e Motivo diz por quê. A rota segue respondendo
// 202 {"queued":true} (comportamento da UI); a tool do MCP o trata como recusa.
type TesteWebhook struct {
	Enfileirado bool
	Motivo      string
}

// TestarWebhook enfileira um evento webhook.test para o cliente, para validar o
// receptor sem esperar uma detecção real. Lê a configuração ANTES: cliente
// inexistente é nao_encontrado, e cliente sem URL, desabilitado ou que não
// assinou webhook.test (nem "*") não enfileira nada e diz o motivo. Sem outbox
// no Servico: ErrWebhooksIndisponivel.
func (s *Servico) TestarWebhook(ctx context.Context, cliente uuid.UUID) (TesteWebhook, error) {
	if s.Webhooks == nil {
		return TesteWebhook{}, ErrWebhooksIndisponivel
	}
	cfg, err := s.Clientes.GetWebhookConfig(ctx, cliente)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TesteWebhook{}, naoEncontrado("not found")
		}
		return TesteWebhook{}, err
	}
	if motivo := MotivoDoTesteDescartado(cfg); motivo != "" {
		return TesteWebhook{Motivo: motivo}, nil
	}
	if err := s.Webhooks.Enqueue(ctx, &webhook.Event{
		Type:     webhook.EventWebhookTest,
		ClientID: cliente,
		Body: map[string]any{
			"message":     "This is a test webhook from Radiocheck.",
			"sent_at":     time.Now().UTC().Format(time.RFC3339),
			"environment": "internal",
		},
	}); err != nil {
		return TesteWebhook{}, err
	}
	return TesteWebhook{Enfileirado: true}, nil
}

// MotivoDoTesteDescartado diz por que um evento webhook.test NÃO chegaria ao
// cliente com esta configuração ("" = chegaria). É a regra da outbox
// (Outbox.Enqueue é no-op nestes três casos): cfg nil é o cliente sem URL,
// depois vêm o webhook desligado e a falta de assinatura do evento. Quem decide
// antes de enfileirar (a tool do MCP, no pedido e na aprovação) e TestarWebhook
// usam esta mesma função.
func MotivoDoTesteDescartado(cfg *catalog.WebhookConfig) string {
	switch {
	case cfg == nil:
		return "o cliente não tem URL de webhook configurada"
	case !cfg.Enabled:
		return "o webhook do cliente está desabilitado"
	case !assinaEvento(cfg.Events, string(webhook.EventWebhookTest)):
		return "o cliente não assinou o evento webhook.test"
	}
	return ""
}

// assinaEvento espelha a regra da outbox: filtro vazio assina tudo; "*" também.
func assinaEvento(eventos []string, tipo string) bool {
	if len(eventos) == 0 {
		return true
	}
	for _, e := range eventos {
		if e == tipo || e == "*" {
			return true
		}
	}
	return false
}
