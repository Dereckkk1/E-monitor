package acoes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/webhook"
)

// TestEventoWebhookValido: os identificadores aceitos hoje; o resto é recusado.
func TestEventoWebhookValido(t *testing.T) {
	for _, e := range []string{"detection.confirmed", "detection.retracted", "webhook.test", "*"} {
		require.True(t, EventoWebhookValido(e), e)
	}
	for _, e := range []string{"", "DETECTION.CONFIRMED", "unknown.event", "detection.*"} {
		require.False(t, EventoWebhookValido(e), e)
	}
}

func TestValidarWebhook_Recusas(t *testing.T) {
	t.Setenv("RADIOCHECK_ENV", "")
	p := func(s string) *string { return &s }
	casos := map[string]struct {
		in    catalog.UpdateWebhookInput
		texto string
	}{
		"esquema": {catalog.UpdateWebhookInput{URL: p("ftp://x")}, "webhook_url must be a valid http(s) URL"},
		"http":    {catalog.UpdateWebhookInput{URL: p("http://8.8.8.8/h")}, "http URLs are not allowed in production"},
		"ssrf":    {catalog.UpdateWebhookInput{URL: p("https://10.0.0.5/h")}, "webhook_url rejected: "},
		"segredo": {catalog.UpdateWebhookInput{Secret: p("curto")}, "webhook_secret must be at least 16 characters"},
		"evento":  {catalog.UpdateWebhookInput{Events: []string{"x.y"}}, "unknown event: x.y"},
	}
	for nome, c := range casos {
		r := recusaDe(t, ValidarWebhook(context.Background(), c.in))
		require.Equal(t, CodigoEntradaInvalida, r.Codigo, nome)
		require.True(t, strings.HasPrefix(r.Mensagem, c.texto), "%s: %s", nome, r.Mensagem)
	}
	require.NoError(t, ValidarWebhook(context.Background(), catalog.UpdateWebhookInput{}))
	require.NoError(t, ValidarWebhook(context.Background(), catalog.UpdateWebhookInput{URL: p("https://8.8.8.8/h"), Secret: p("")}))
}

func TestEditarWebhook(t *testing.T) {
	t.Setenv("RADIOCHECK_ENV", "")
	c := novoCenario(t)
	url, seg, on := "https://8.8.8.8/h", "segredo-com-16-chars!", true
	cfg, err := c.srv.EditarWebhook(c.ctx, c.cliente, catalog.UpdateWebhookInput{URL: &url, Secret: &seg, Enabled: &on})
	require.NoError(t, err)
	require.Equal(t, url, cfg.URL)
	require.True(t, cfg.Enabled)

	_, err = c.srv.EditarWebhook(c.ctx, uuid.New(), catalog.UpdateWebhookInput{Enabled: &on})
	require.Equal(t, CodigoNaoEncontrado, recusaDe(t, err).Codigo)

	_, err = c.srv.EditarWebhook(c.ctx, c.cliente, catalog.UpdateWebhookInput{Events: []string{"x"}})
	require.Equal(t, CodigoEntradaInvalida, recusaDe(t, err).Codigo)
}

type outboxFalsa struct {
	eventos []*webhook.Event
	err     error
}

func (o *outboxFalsa) Enqueue(_ context.Context, ev *webhook.Event) error {
	o.eventos = append(o.eventos, ev)
	return o.err
}

func TestTestarWebhook(t *testing.T) {
	var s Servico
	require.True(t, errors.Is(s.TestarWebhook(context.Background(), uuid.New()), ErrWebhooksIndisponivel))

	o := &outboxFalsa{}
	s.Webhooks = o
	id := uuid.New()
	require.NoError(t, s.TestarWebhook(context.Background(), id))
	require.Len(t, o.eventos, 1)
	require.Equal(t, webhook.EventWebhookTest, o.eventos[0].Type)
	require.Equal(t, id, o.eventos[0].ClientID)

	o.err = errors.New("boom")
	require.EqualError(t, s.TestarWebhook(context.Background(), id), "boom")
}
