package welcome

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderWelcome_Client(t *testing.T) {
	subject, html, text := renderWelcome(welcomeData{
		Name:       "Ana",
		FullName:   "Ana Souza",
		Email:      "ana@sofaecia.com.br",
		ClientName: "Sofá & Cia",
		Link:       "https://e-monitor.online/boasvindas/abc123",
		BaseURL:    "https://e-monitor.online",
		IsClient:   true,
	})

	require.Contains(t, subject, "Ana")
	require.NotEmpty(t, html)
	require.NotEmpty(t, text)

	for _, body := range []string{html, text} {
		require.Contains(t, body, "https://e-monitor.online/boasvindas/abc123",
			"o link tem que aparecer nas duas versões — se o HTML for bloqueado, o texto salva")
		require.Contains(t, body, "Ana")
	}

	// O nome do cliente é escapado pelo html/template: & vira &amp;. Confirma
	// que o escape aconteceu (nada de injeção via nome de cliente) e que o
	// texto puro mantém o caractere original.
	require.Contains(t, html, "Sofá &amp; Cia")
	require.Contains(t, text, "Sofá & Cia")

	require.Contains(t, html, "Concluir meu cadastro")
	require.Contains(t, html, "/emidias-logo.png", "rodapé E-Mídias")
	require.NotContains(t, html, "{{", "template não pode vazar delimitador não resolvido")
}

func TestRenderWelcome_Admin(t *testing.T) {
	_, html, text := renderWelcome(welcomeData{
		Name:     "Dereck",
		Email:    "dereck@hubradios.com",
		Link:     "https://e-monitor.online/boasvindas/xyz",
		BaseURL:  "https://e-monitor.online",
		IsClient: false,
	})
	// Admin não recebe a frase de cliente ("cada comercial que for ao ar").
	require.Contains(t, html, "acesso completo à plataforma")
	require.NotContains(t, text, "cada comercial que for ao ar")
	// Sem cliente vinculado, a oração "e vinculada a X" não pode sobrar solta.
	require.NotContains(t, html, "vinculada a </p>")
	require.NotContains(t, text, "vinculada a \n")
}

func TestRenderWelcome_NomeVazio(t *testing.T) {
	// Nome vazio é possível? O handler exige name, mas o template não pode
	// produzir "Boas-vindas, ." se um dia passar vazio.
	subject, html, _ := renderWelcome(welcomeData{
		Email:   "x@y.com",
		Link:    "https://e/boasvindas/t",
		BaseURL: "https://e",
	})
	require.Equal(t, "Seu acesso ao E-monitor está pronto", subject)
	require.Contains(t, html, "Boas-vindas.")
	require.NotContains(t, html, "Boas-vindas, .")
}

func TestRenderWelcome_HTMLEscapaInjecao(t *testing.T) {
	// Nome vem de input do admin. Um <script> ali não pode virar markup ativo
	// no cliente de email de quem recebe.
	_, html, _ := renderWelcome(welcomeData{
		Name:    "<script>alert(1)</script>",
		Email:   "x@y.com",
		Link:    "https://e/boasvindas/t",
		BaseURL: "https://e",
	})
	require.NotContains(t, html, "<script>alert(1)</script>")
	require.True(t, strings.Contains(html, "&lt;script&gt;"),
		"o nome tem que sair escapado")
}
