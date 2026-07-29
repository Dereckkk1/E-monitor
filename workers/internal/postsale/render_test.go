package postsale

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderEmail(t *testing.T) {
	subject, html, text := renderEmail(emailData{
		Name:       "Ana",
		ClientName: "Engie",
		Link:       "https://e-monitor.online/pos-venda/tok123",
		BaseURL:    "https://e-monitor.online",
		Campaigns: []emailCampaign{
			{Name: "249 ENGIE | PLANO PAE", Period: "01/06/2026 a 31/07/2026"},
			{Name: "250 ENGIE | INSTITUCIONAL", Period: "01/07/2026 a 31/07/2026"},
		},
	})

	require.Equal(t, "Ana, seu pós-venda da Engie está pronto", subject)
	require.Contains(t, html, "https://e-monitor.online/pos-venda/tok123")
	require.Contains(t, html, "249 ENGIE | PLANO PAE")
	require.Contains(t, html, "250 ENGIE | INSTITUCIONAL")
	require.Contains(t, html, "Olá, Ana")
	require.Contains(t, text, "https://e-monitor.online/pos-venda/tok123")
	require.NotEmpty(t, strings.TrimSpace(text))
}

// Sem nome, a saudação não pode virar "Olá, !".
func TestRenderEmail_SemNome(t *testing.T) {
	subject, html, text := renderEmail(emailData{
		ClientName: "Engie",
		Link:       "https://x/y",
	})
	require.Equal(t, "Seu pós-venda da Engie está pronto", subject)
	require.NotContains(t, html, "Olá, !")
	require.NotContains(t, text, "Olá, !")
	require.Contains(t, html, "Olá!")
}

// O corpo HTML nunca pode sair vazio: é o que o cliente lê.
func TestRenderEmail_HTMLNaoVazio(t *testing.T) {
	_, html, _ := renderEmail(emailData{ClientName: "X", Link: "https://x/y"})
	require.Greater(t, len(html), 500, "template HTML não renderizou")
}
