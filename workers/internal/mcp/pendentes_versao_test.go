package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/acoes"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/mcp/tokens"
)

// A aprovação carrega a versão (tentativa) do pedido que a pessoa leu. Sem ela, 400: o único cliente
// é a tela /pendentes, que sempre a manda, e "aprovar a vigente" seria aprovar às cegas.
func TestPendentesHTTP_AprovarSemAVersaoDa400(t *testing.T) {
	sonda := &sondaEscrita{}
	b := novaBancadaPendentesHTTP(t, sonda)
	pend := b.pedido(t, "t_crit", `{"valor":"a"}`)
	url := "/mcp/pendentes/" + pend.ID.String() + "/aprovar"

	for _, corpo := range []string{``, `{}`, `{"lixo":true}`, `{"tentativa":null}`, `{"tentativa":-1}`, `{"tentativa":"0"}`, `{"tentativa":0.5}`, `nao-e-json`} {
		rec, m := b.fazerCorpo(http.MethodPost, url, b.jwtDono, corpo)
		require.Equal(t, http.StatusBadRequest, rec.Code, "corpo %q: %s", corpo, rec.Body.String())
		require.Equal(t, "entrada_invalida", m["error"], corpo)
	}
	require.Equal(t, 0, sonda.execucoes)
	got := b.estado(t, pend.ID)
	require.Equal(t, "pendente", got.Estado)
	require.Equal(t, 0, got.Tentativa, "o 400 não gasta tentativa")
}

// Duas abas abertas com o mesmo pedido: a A aprova e leva o 422 (o resumo mudou, o servidor grava o
// novo); a B ainda mostra o resumo velho. Aprovar da B é 409 pendente_mudou e NADA executa. Só quem
// leu o resumo novo (a tentativa que o 422 devolveu) aprova.
func TestPendentesHTTP_AprovarComAVersaoVelhaDa409PendenteMudouENadaExecuta(t *testing.T) {
	sonda := &sondaEscrita{}
	b := novaBancadaPendentesHTTP(t, sonda)
	// o resumo gravado difere do de verdade ("vai gravar abc"): a primeira aprovação dá preview_desatualizado
	pend := b.pedidoComPrevia(t, "t_crit", `{"valor":"abc"}`, previa{Resumo: "vai gravar abc", Efeitos: []string{"Afeta 1 usuário"}})
	url := "/mcp/pendentes/" + pend.ID.String() + "/aprovar"

	_, lista := b.fazer(http.MethodGet, "/mcp/pendentes", b.jwtDono)
	require.EqualValues(t, 0, lista["items"].([]any)[0].(map[string]any)["tentativa"], "a lista mostra a versão")

	// aba A
	rec, m := b.fazerCorpo(http.MethodPost, url, b.jwtDono, `{"tentativa":0}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	require.Equal(t, "preview_desatualizado", m["error"])
	p422 := m["pendente"].(map[string]any)
	require.EqualValues(t, 1, p422["tentativa"], "o 422 leva a versão nova junto com o resumo novo")
	require.Equal(t, 0, sonda.execucoes)

	// aba B, ainda com a versão 0
	rec, m = b.fazerCorpo(http.MethodPost, url, b.jwtDono, `{"tentativa":0}`)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Equal(t, "pendente_mudou", m["error"])
	require.Equal(t, msgPendenteMudou, m["mensagem"])
	require.Equal(t, 0, sonda.execucoes, "o resumo que a B mostrava nunca foi aprovado")
	got := b.estado(t, pend.ID)
	require.Equal(t, "pendente", got.Estado)
	require.Equal(t, 1, got.Tentativa, "o 409 não gasta tentativa")

	// versão que nunca existiu (adivinhada): idem
	rec, m = b.fazerCorpo(http.MethodPost, url, b.jwtDono, `{"tentativa":9}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "pendente_mudou", m["error"])

	// aba A, com o resumo novo: executa e a resposta 200 leva a tentativa final
	rec, m = b.fazerCorpo(http.MethodPost, url, b.jwtDono, `{"tentativa":1}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, sonda.execucoes)
	require.EqualValues(t, 2, m["pendente"].(map[string]any)["tentativa"])

	// executado: a versão velha agora é 409 de estado, não de versão
	rec, m = b.fazerCorpo(http.MethodPost, url, b.jwtDono, `{"tentativa":0}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "pendente_executada", m["error"])
	require.Equal(t, 1, sonda.execucoes)
}

func TestPendentesHTTP_ExpoeATentativaNaListaENoDestaque(t *testing.T) {
	b := novaBancadaPendentesHTTP(t, &sondaEscrita{})
	pend := b.pedido(t, "t_crit", `{"valor":"a"}`)
	_, err := b.pool.Exec(b.ctx, `UPDATE mcp_acoes_pendentes SET tentativa = 3 WHERE id = $1`, pend.ID)
	require.NoError(t, err)

	_, m := b.fazer(http.MethodGet, "/mcp/pendentes?id="+pend.ID.String(), b.jwtDono)
	require.EqualValues(t, 3, m["items"].([]any)[0].(map[string]any)["tentativa"])
	require.EqualValues(t, 3, m["destaque"].(map[string]any)["tentativa"])
}

// A conexão de IA que fez o pedido foi revogada, mas o pedido ficou `pendente` (revogação que não
// passou pelo caminho proativo): aprovar é 409 pendente_expirada com o motivo, nada executa, o
// pedido fecha como `expirada` e a auditoria registra.
func TestPendentesHTTP_AprovarPedidoDeConexaoRevogadaDa409ENaoExecuta(t *testing.T) {
	sonda := &sondaEscrita{}
	b := novaBancadaPendentesHTTP(t, sonda)
	pend := b.pedido(t, "t_crit", `{"valor":"a"}`)
	_, err := b.pool.Exec(b.ctx, `UPDATE mcp_tokens SET revoked_at = NOW() WHERE id = $1`, principalTeste.TokenID)
	require.NoError(t, err)

	rec, m := b.fazer(http.MethodPost, "/mcp/pendentes/"+pend.ID.String()+"/aprovar", b.jwtDono)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Equal(t, "pendente_expirada", m["error"])
	require.Equal(t, msgCredencialRevogada, m["mensagem"])
	require.Equal(t, 0, sonda.execucoes)
	got := b.estado(t, pend.ID)
	require.Equal(t, "expirada", got.Estado)
	require.Equal(t, msgCredencialRevogada, *got.Erro)
	require.Equal(t, 0, got.Tentativa)
	require.Equal(t, 0, b.auditorias(t, "mcp.pendente.aprovada"))
	require.Equal(t, "credencial_revogada", b.meta(t, "mcp.pendente.falhou")["codigo"])

	// a lista não o oferece mais; o link dele explica
	_, lista := b.fazer(http.MethodGet, "/mcp/pendentes?id="+pend.ID.String(), b.jwtDono)
	require.Empty(t, lista["items"])
	d := lista["destaque"].(map[string]any)
	require.Equal(t, "expirada", d["estado"])
	require.Equal(t, msgCredencialRevogada, d["erro"])

	// e de novo: o mesmo 409, agora pelo estado
	rec, m = b.fazer(http.MethodPost, "/mcp/pendentes/"+pend.ID.String()+"/aprovar", b.jwtDono)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "pendente_expirada", m["error"])
	require.Equal(t, msgCredencialRevogada, m["mensagem"])
}

// O 422 leva o pedido relido, e a releitura não depende de o pedido HTTP continuar vivo: o
// middleware de timeout cancela o contexto e o cliente só então lê a resposta.
func TestPendentesHTTP_O422ReleOPedidoMesmoComOContextoCancelado(t *testing.T) {
	b := novaBancadaPendentesHTTP(t, &sondaEscrita{})
	pend := b.pedido(t, "t_crit", `{"valor":"a"}`)
	h := &PendentesHTTP{Tokens: tokens.NovoStore(b.pool, []byte(mcptest.SegredoTeste)), Pendentes: b.E.Pendentes, Aprovacoes: b.apr, Log: zap.NewNop()}

	ctx, cancela := context.WithCancel(context.Background())
	cancela()
	req := httptest.NewRequest(http.MethodPost, "/x", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.responderErroPendente(rec, req, pend.ID, principalTeste.UserID, &acoes.Recusa{Codigo: "nao_achada", Mensagem: "O alvo sumiu."})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), `"pendente"`)
	require.Contains(t, rec.Body.String(), pend.ID.String())
}
