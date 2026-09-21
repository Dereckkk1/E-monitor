package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/hub"
)

// comParamDeRota põe no request o `{param}` que o chi poria, para o handler
// poder ser chamado sem montar o router.
func comParamDeRota(req *http.Request, chave, valor string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(chave, valor)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// getHubCode chama o handler com o `{code}` no lugar onde o chi o põe. O
// handler é testado direto, sem router: o JWT e o papel são portão do
// `router.go`, e montar o router inteiro aqui só acrescentaria banco a um teste
// que não precisa de nenhum.
func getHubCode(t *testing.T, h *HubCodesHandler, code string) *httptest.ResponseRecorder {
	t.Helper()
	// ⚠️ `PathEscape` no ALVO e o código CRU no param: é o que o chi faz de
	// verdade (ele entrega o valor já decodificado), e sem o escape o
	// `httptest.NewRequest` PANICA com qualquer código que tenha espaço —
	// derrubando o binário inteiro de teste, não só este caso.
	req := httptest.NewRequest(http.MethodGet, "/v1/internal/hub-codes/"+url.PathEscape(code), nil)
	req = comParamDeRota(req, "code", code)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	return rec
}

func TestHubCodesRepassaAConferencia(t *testing.T) {
	hb, _ := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub("cliente-1"))

	rec := getHubCode(t, &HubCodesHandler{Hub: hb}, "EH-7K4M2X")

	require.Equal(t, http.StatusOK, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "Verão 2026", out["nome"])
	require.Equal(t, "2026-12-01", out["inicio"])
	require.Equal(t, "2027-02-28", out["fim"])
	cli, _ := out["cliente"].(map[string]any)
	require.Equal(t, "Rôgga", cli["nome"])
	require.Equal(t, "cliente-1", cli["idNaPlataforma"])
}

/*
⚠️ A resposta desce para o NAVEGADOR. Esta rota existe exatamente para a chave da
plataforma não descer junto — se ela vazasse aqui, a rota teria custado trabalho
para piorar o que veio consertar: a mesma chave abre a porta de eventos do hub.

E os ids do hub (`hubCampaignId`, `cliente.id`) também ficam de fora: são
identificadores de outro sistema, a tela não faz nada com eles, e mandá-los
convidaria a próxima pessoa a construir sobre um id que este repositório não
controla.
*/
func TestHubCodesNaoVazaChaveNemIdsDoHub(t *testing.T) {
	hb, _ := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub("cliente-1"))

	rec := getHubCode(t, &HubCodesHandler{Hub: hb}, "EH-7K4M2X")

	corpo := rec.Body.String()
	require.NotContains(t, corpo, "chave")
	require.NotContains(t, corpo, "hubCampaignId")
	require.NotContains(t, corpo, "66f0") // o id da campanha no hub
	require.NotContains(t, corpo, "66e1") // o id do cliente no hub
}

// A forma canônica volta junto para a tela poder mostrar o que vai ser GRAVADO.
// Quem digita escreve `eh7k4m2x`; a coluna guarda `EH-7K4M2X` (§10). Sem
// devolver o canônico, a tela mostraria uma string e o banco guardaria outra.
func TestHubCodesDevolveAFormaCanonica(t *testing.T) {
	hb, _ := hubFalsoParaCodigo(t, http.StatusOK, corpoDoHub("cliente-1"))

	rec := getHubCode(t, &HubCodesHandler{Hub: hb}, "eh7k4m2x")

	require.Equal(t, http.StatusOK, rec.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "EH-7K4M2X", out["codigo"])
}

func TestHubCodes404VeioDoHub(t *testing.T) {
	hb, _ := hubFalsoParaCodigo(t, http.StatusNotFound, `{}`)

	rec := getHubCode(t, &HubCodesHandler{Hub: hb}, "EH-ZZZZZZ")

	// 404 é o ÚNICO vermelho da §4.3: é o caso em que o problema é do que a
	// pessoa digitou. Todo o resto é âmbar.
	require.Equal(t, http.StatusNotFound, rec.Code)
}

/*
⚠️ Texto que não é código nem sai para a rede — o `ConferirCodigo` corta antes.
Num campo que confere ao sair do foco, isso é uma ida ao hub por digitação
abandonada.

⚠️ E o texto inválido NÃO pode ser uma palavra curta: "BANANA" tem seis
caracteres, todos no alfabeto de 30, e é um código VÁLIDO. Este aqui limpa para
16 caracteres, que não é 6 nem 8.
*/
func TestHubCodesTextoQueNaoEhCodigoNaoVaiAoHub(t *testing.T) {
	bateu := make(chan bool, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bateu <- true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rec := getHubCode(t, &HubCodesHandler{Hub: hub.New(srv.URL, "chave")}, "isto nao e um codigo")

	require.Equal(t, http.StatusNotFound, rec.Code)
	select {
	case <-bateu:
		t.Fatal("foi à rede para um texto que nem é código")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHubCodes503QuandoOHubNaoResponde(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // fechado: a conexão é recusada na hora

	rec := getHubCode(t, &HubCodesHandler{Hub: hub.New(srv.URL, "chave")}, "EH-7K4M2X")

	// 503 e não 500: a tela mostra "não deu para conferir agora", em âmbar, e
	// deixa salvar. 500 faria a tela dizer "erro" e a pessoa parar.
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// 401 (chave errada OU produto desmarcado) e 429 (o balde por IP que esta rota
// divide com o job de reemissão) são problema NOSSO, não do que a pessoa
// digitou. Âmbar, não vermelho.
func TestHubCodes401E429ViramAmbar(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusBadGateway} {
		hb, _ := hubFalsoParaCodigo(t, status, `{}`)
		rec := getHubCode(t, &HubCodesHandler{Hub: hb}, "EH-7K4M2X")
		require.Equalf(t, http.StatusServiceUnavailable, rec.Code, "status %d do hub", status)
	}
}

func TestHubCodesSemHubConfigurado(t *testing.T) {
	rec := getHubCode(t, &HubCodesHandler{Hub: hub.New("", "")}, "EH-7K4M2X")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHubCodesHubNilNaoPanica(t *testing.T) {
	rec := getHubCode(t, &HubCodesHandler{Hub: nil}, "EH-7K4M2X")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

/*
⚠️ O `hub.New` fixa `http.Client{Timeout: 10s}` — o mesmo cliente do `Exchange`,
que roda dentro de um login e precisa desse fôlego. Aqui quem espera é uma
pessoa que acabou de sair de um campo: dez segundos de "conferindo…" para chegar
ao mesmo âmbar que daria em dois é tela travada comprando nada.

Este teste é o que impede alguém de apagar o deadline local achando que o do
cliente basta.
*/
func TestHubCodesNaoEsperaOTimeoutDoCliente(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Second) // aceita a conexão e nunca responde
	}))
	defer srv.Close()

	comecou := time.Now()
	rec := getHubCode(t, &HubCodesHandler{Hub: hub.New(srv.URL, "chave")}, "EH-7K4M2X")
	demorou := time.Since(comecou)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Lessf(t, demorou, 5*time.Second,
		"esperou %v — o deadline de %v não está valendo", demorou, prazoDeConferencia)
}
