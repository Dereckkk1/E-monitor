package api

// router_hubcodes_test.go — a rota GET /v1/internal/hub-codes/{code} (spec do
// hub 2026-09-18 §4.3) montada no router DE VERDADE.
//
// ⚠️ Usa `NewRouter`, e não um router espelho como o `minimalAdminRouter` deste
// pacote: metade do que esta rota precisa provar é sobre ONDE ela está montada
// — fora do `/hub` guardado por chave de plataforma, dentro do grupo com JWT, e
// no par de papéis que cadastra campanha. Um espelho provaria o espelho.

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
)

func routerComHubCodes() http.Handler {
	// Hub vazio de propósito: `Configured()` falso faz o handler responder 503
	// sem tocar a rede. É o que separa "a rota existe e me deixou entrar" de
	// "a rota não existe" — um 404 e um 403 se parecem demais.
	return NewRouter(Deps{HubCodes: &handlers.HubCodesHandler{}})
}

func TestHubCodesRotaExigeJWT(t *testing.T) {
	require.Equal(t, http.StatusUnauthorized,
		do(t, routerComHubCodes(), http.MethodGet, "/v1/internal/hub-codes/EH-7K4M2X", ""))
}

/*
⚠️ Viewer NÃO entra, e isto não é zelo de papel: a resposta diz o nome da
campanha e o nome do cliente de QUALQUER código que se adivinhe. É um oráculo
sobre o hub inteiro, e viewer é o papel do cliente externo — ele veria o nome da
campanha do concorrente.
*/
func TestHubCodesRotaRecusaViewer(t *testing.T) {
	require.Equal(t, http.StatusForbidden,
		do(t, routerComHubCodes(), http.MethodGet, "/v1/internal/hub-codes/EH-7K4M2X", issue(t, "viewer")))
}

/*
⚠️ O teste que prova que a ROTA EXISTE.

Sem ele, os dois de cima passariam igual com a rota inexistente — o 401 vem do
middleware, que roda antes de o chi descobrir que não há rota, e o 403 idem. O
503 só pode vir do `HubCodesHandler`: ele é a prova de que a requisição chegou
ao handler, e portanto de que o `r.Get("/hub-codes/{code}")` está no lugar certo
do router.
*/
func TestHubCodesRotaDeixaAdminEOperadorEntrarem(t *testing.T) {
	for _, papel := range []string{"admin", "operator"} {
		codigo := do(t, routerComHubCodes(), http.MethodGet,
			"/v1/internal/hub-codes/EH-7K4M2X", issue(t, papel))
		require.Equalf(t, http.StatusServiceUnavailable, codigo,
			"papel %s: 404 aqui significa rota não montada; 403, papel errado", papel)
	}
}

// A rota é registrada só quando a dependência existe, como todas as outras
// deste router. Sem isto, um `NewRouter` sem HubCodes panicaria no primeiro
// acesso em vez de devolver 404.
func TestHubCodesRotaNaoExisteSemADependencia(t *testing.T) {
	require.Equal(t, http.StatusNotFound,
		do(t, NewRouter(Deps{}), http.MethodGet, "/v1/internal/hub-codes/EH-7K4M2X", issue(t, "admin")))
}
