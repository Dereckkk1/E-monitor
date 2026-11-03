package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/acoes"
	"radiocheck/internal/auth"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/mcp/tokens"
)

// bancadaPendentesHTTP: as rotas montadas como o MontarAdmin faz, sobre a
// bancada de aprovação. O dono é o principalTeste; "outro" é um segundo admin.
type bancadaPendentesHTTP struct {
	*bancadaAprovacao
	r        chi.Router
	jwtDono  string
	outro    uuid.UUID
	jwtOutro string
	sonda    *sondaEscrita
}

func novaBancadaPendentesHTTP(t *testing.T, sonda *sondaEscrita) *bancadaPendentesHTTP {
	t.Helper()
	t.Setenv("JWT_SECRET", mcptest.SegredoTeste)
	b := novaBancadaAprovacao(t, sonda.escrita("t_crit", Critico, false))
	h := &PendentesHTTP{Tokens: tokens.NovoStore(b.pool, []byte(mcptest.SegredoTeste)), Pendentes: b.E.Pendentes, Aprovacoes: b.apr, Log: zap.NewNop()}
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireJWT, auth.RequireRole("admin"))
		r.Get("/mcp/pendentes", h.Listar)
		r.Post("/mcp/pendentes/{id}/aprovar", h.Aprovar)
		r.Post("/mcp/pendentes/{id}/recusar", h.Recusar)
	})
	dono, err := auth.IssueToken(principalTeste.UserID, "admin")
	require.NoError(t, err)
	outro := mcptest.Usuario(t, b.ctx, b.pool, "admin", true)
	jo, err := auth.IssueToken(outro, "admin")
	require.NoError(t, err)
	return &bancadaPendentesHTTP{bancadaAprovacao: b, r: r, jwtDono: dono, outro: outro, jwtOutro: jo, sonda: sonda}
}

func (b *bancadaPendentesHTTP) fazer(metodo, caminho, jwt string) (*httptest.ResponseRecorder, map[string]any) {
	req := httptest.NewRequest(metodo, caminho, strings.NewReader(`{"lixo":true}`))
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	rec := httptest.NewRecorder()
	b.r.ServeHTTP(rec, req)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec, m
}

// pedidoDe cria um pedido pendente de outro usuário (semeia o token dele: FK).
func (b *bancadaPendentesHTTP) pedidoDe(t *testing.T, dono uuid.UUID, args string) Pendente {
	t.Helper()
	semearPrincipal(t, b.ctx, b.pool, &Principal{UserID: dono, TokenID: uuid.New()})
	var tok uuid.UUID
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT id FROM mcp_tokens WHERE user_id = $1 LIMIT 1`, dono).Scan(&tok))
	p, _, err := b.E.Pendentes.Criar(b.ctx, NovaPendente{UserID: dono, TokenID: tok, Tool: "t_crit",
		Args: []byte(args), Motivo: "m", Preview: previa{Resumo: "r"}}, b.agora)
	require.NoError(t, err)
	return p
}

var chavesProibidasPendente = []string{"args", "resultado", "token_id", "user_id", "tentativa"}

type erroTexto string

func (e erroTexto) Error() string { return string(e) }

// 1
func TestPendentesHTTP_ListaSoOsAbertosDoDono(t *testing.T) {
	b := novaBancadaPendentesHTTP(t, &sondaEscrita{})
	aberto := b.pedido(t, "t_crit", `{"valor":"a"}`)
	feito := b.pedido(t, "t_crit", `{"valor":"b"}`)
	_, err := b.apr.Aprovar(b.ctx, feito.ID, b.ator())
	require.NoError(t, err)
	alheio := b.pedidoDe(t, b.outro, `{"valor":"c"}`)

	rec, m := b.fazer(http.MethodGet, "/mcp/pendentes", b.jwtDono)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	itens := m["items"].([]any)
	require.Len(t, itens, 1)
	it := itens[0].(map[string]any)
	require.Equal(t, aberto.ID.String(), it["id"])
	require.Equal(t, "pendente", it["estado"])
	require.Equal(t, "t_crit", it["tool"])
	for _, k := range chavesProibidasPendente {
		require.NotContains(t, it, k)
	}
	require.Nil(t, m["destaque"])
	require.NotContains(t, rec.Body.String(), alheio.ID.String())
}

// 2
func TestPendentesHTTP_DestaqueSoDoDono(t *testing.T) {
	b := novaBancadaPendentesHTTP(t, &sondaEscrita{})
	feito := b.pedido(t, "t_crit", `{"valor":"b"}`)
	_, err := b.apr.Aprovar(b.ctx, feito.ID, b.ator())
	require.NoError(t, err)
	alheio := b.pedidoDe(t, b.outro, `{"valor":"c"}`)

	_, m := b.fazer(http.MethodGet, "/mcp/pendentes?id="+feito.ID.String(), b.jwtDono)
	d := m["destaque"].(map[string]any)
	require.Equal(t, feito.ID.String(), d["id"])
	require.Equal(t, "executada", d["estado"])
	require.NotNil(t, d["executada_em"])
	for _, k := range chavesProibidasPendente {
		require.NotContains(t, d, k)
	}
	for _, id := range []string{alheio.ID.String(), "lixo", uuid.NewString()} {
		rec, m := b.fazer(http.MethodGet, "/mcp/pendentes?id="+id, b.jwtDono)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, m, "destaque")
		require.Nil(t, m["destaque"], id)
	}
}

// 3
func TestPendentesHTTP_AprovarExecutaDepoisConflitaEOutrosDao404Identico(t *testing.T) {
	sonda := &sondaEscrita{}
	b := novaBancadaPendentesHTTP(t, sonda)
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)
	alheio := b.pedidoDe(t, b.outro, `{"valor":"c"}`)

	rec, m := b.fazer(http.MethodPost, "/mcp/pendentes/"+pend.ID.String()+"/aprovar", b.jwtDono)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "executada", m["pendente"].(map[string]any)["estado"])
	require.JSONEq(t, `{"valor":"abc"}`, mustJSON(t, m["resultado"]))
	require.Equal(t, 1, sonda.execucoes)

	rec, m = b.fazer(http.MethodPost, "/mcp/pendentes/"+pend.ID.String()+"/aprovar", b.jwtDono)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "pendente_executada", m["error"])
	require.Equal(t, "Este pedido já foi executado.", m["mensagem"])
	require.Equal(t, 1, sonda.execucoes)

	var corpos []string
	for _, id := range []string{alheio.ID.String(), uuid.NewString(), "lixo"} {
		rec, _ := b.fazer(http.MethodPost, "/mcp/pendentes/"+id+"/aprovar", b.jwtDono)
		require.Equal(t, http.StatusNotFound, rec.Code, id)
		corpos = append(corpos, rec.Body.String())
	}
	require.Equal(t, `{"error":"nao_encontrado","mensagem":"Pedido não encontrado."}`+"\n", corpos[0])
	require.Equal(t, corpos[0], corpos[1])
	require.Equal(t, corpos[0], corpos[2])
	require.Equal(t, 1, sonda.execucoes, "o pedido alheio não pode ter executado")
}

// 4
func TestPendentesHTTP_RecusaDeNegocioDa422EContinuaAprovavel_ErroInternoDa500(t *testing.T) {
	sonda := &sondaEscrita{erroExec: &acoes.Recusa{Codigo: "conflito", Mensagem: "Mudou no meio."}}
	b := novaBancadaPendentesHTTP(t, sonda)
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)
	url := "/mcp/pendentes/" + pend.ID.String() + "/aprovar"

	rec, m := b.fazer(http.MethodPost, url, b.jwtDono)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	require.Equal(t, "conflito", m["error"])
	require.Equal(t, "Mudou no meio.", m["mensagem"])
	require.Equal(t, "pendente", b.estado(t, pend.ID).Estado)

	sonda.erroExec = nil // aprovável de novo
	rec, _ = b.fazer(http.MethodPost, url, b.jwtDono)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	sonda.erroExec = erroTexto("pq: relation secreta does not exist")
	outro := b.pedido(t, "t_crit", `{"valor":"zzz"}`)
	rec, m = b.fazer(http.MethodPost, "/mcp/pendentes/"+outro.ID.String()+"/aprovar", b.jwtDono)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "interrompida", m["error"])
	require.Equal(t, msgInterrompida, m["mensagem"])
	require.NotContains(t, rec.Body.String(), "secreta")
}

// 5
func TestPendentesHTTP_RecusarEExpirada(t *testing.T) {
	b := novaBancadaPendentesHTTP(t, &sondaEscrita{})
	pend := b.pedido(t, "t_crit", `{"valor":"a"}`)
	rec, m := b.fazer(http.MethodPost, "/mcp/pendentes/"+pend.ID.String()+"/recusar", b.jwtDono)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	p := m["pendente"].(map[string]any)
	require.Equal(t, "recusada", p["estado"])
	require.NotNil(t, p["recusada_em"])
	require.NotContains(t, m, "resultado")
	require.Equal(t, 0, b.sonda.execucoes)

	rec, m = b.fazer(http.MethodPost, "/mcp/pendentes/"+pend.ID.String()+"/recusar", b.jwtDono)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "pendente_recusada", m["error"])

	velho := b.pedido(t, "t_crit", `{"valor":"velho"}`)
	_, err := b.pool.Exec(b.ctx, `UPDATE mcp_acoes_pendentes SET expira_em = $2 WHERE id = $1`, velho.ID, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	for _, acao := range []string{"recusar", "aprovar"} {
		rec, m = b.fazer(http.MethodPost, "/mcp/pendentes/"+velho.ID.String()+"/"+acao, b.jwtDono)
		require.Equal(t, http.StatusConflict, rec.Code, acao)
		require.Equal(t, "pendente_expirada", m["error"])
		require.Equal(t, "Este pedido expirou (24 h). Peça de novo à IA.", m["mensagem"])
	}
}

// 6
func TestPendentesHTTP_AdminRebaixadoDa403(t *testing.T) {
	b := novaBancadaPendentesHTTP(t, &sondaEscrita{})
	pend := b.pedido(t, "t_crit", `{"valor":"a"}`)
	_, err := b.pool.Exec(b.ctx, `UPDATE users SET role = 'operator' WHERE id = $1`, principalTeste.UserID)
	require.NoError(t, err)
	for _, c := range [][2]string{
		{http.MethodGet, "/mcp/pendentes"},
		{http.MethodPost, "/mcp/pendentes/" + pend.ID.String() + "/aprovar"},
		{http.MethodPost, "/mcp/pendentes/" + pend.ID.String() + "/recusar"},
	} {
		rec, _ := b.fazer(c[0], c[1], b.jwtDono)
		require.Equal(t, http.StatusForbidden, rec.Code, c[1])
	}
	require.Equal(t, 0, b.sonda.execucoes)
	require.Equal(t, "pendente", b.estado(t, pend.ID).Estado)
}

func TestPendentesHTTP_SemExecutorDa503(t *testing.T) {
	b := novaBancadaPendentesHTTP(t, &sondaEscrita{})
	h := &PendentesHTTP{Tokens: tokens.NovoStore(b.pool, []byte(mcptest.SegredoTeste)), Pendentes: b.E.Pendentes, Log: zap.NewNop()}
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", uuid.NewString())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.WithClaims(ctx, &auth.Claims{UserID: principalTeste.UserID, Role: "admin"})
	rec := httptest.NewRecorder()
	h.Aprovar(rec, req.WithContext(ctx))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), `"indisponivel"`)
}

// A: conferência que cai por erro inesperado = 503 conferencia_falhou, nada foi
// feito e o pedido segue aprovável.
func TestPendentesHTTP_ConferenciaQueCaiDa503EContinuaAprovavel(t *testing.T) {
	sonda := &sondaEscrita{erroPreview: erroTexto("pq: relation secreta does not exist")}
	b := novaBancadaPendentesHTTP(t, sonda)
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)
	rec, m := b.fazer(http.MethodPost, "/mcp/pendentes/"+pend.ID.String()+"/aprovar", b.jwtDono)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	require.Equal(t, "conferencia_falhou", m["error"])
	require.Equal(t, msgConferenciaFalhou, m["mensagem"])
	require.NotContains(t, rec.Body.String(), "secreta")
	require.Equal(t, 0, sonda.execucoes)
	require.Equal(t, "pendente", b.estado(t, pend.ID).Estado)
}

// M7: resultado que não serializa nunca vira 200 vazio: a resposta é JSON
// válido com resultado null e o pedido fecha executada.
func TestPendentesHTTP_ResultadoQueNaoSerializaDa200ComNull(t *testing.T) {
	sonda := &sondaEscrita{resultado: map[string]any{"c": make(chan int)}}
	b := novaBancadaPendentesHTTP(t, sonda)
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)
	rec, m := b.fazer(http.MethodPost, "/mcp/pendentes/"+pend.ID.String()+"/aprovar", b.jwtDono)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, rec.Body.String())
	require.Contains(t, m, "resultado")
	require.Nil(t, m["resultado"])
	require.Equal(t, "executada", m["pendente"].(map[string]any)["estado"])
	require.NotNil(t, m["pendente"].(map[string]any)["executada_em"])
}

// A resposta do /aprovar leva as ressalvas da execução ao lado do resultado
// (sempre um array, [] quando não há); o dto do pedido também as traz (ver o teste de recarga).
func TestPendentesHTTP_AprovarDevolveAsRessalvasDaExecucao(t *testing.T) {
	sonda := &sondaEscrita{ressalvas: []string{"o email não saiu: a pessoa não tem a senha"}}
	b := novaBancadaPendentesHTTP(t, sonda)
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)
	rec, m := b.fazer(http.MethodPost, "/mcp/pendentes/"+pend.ID.String()+"/aprovar", b.jwtDono)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []any{"o email não saiu: a pessoa não tem a senha"}, m["ressalvas"])
	require.Equal(t, "executada", m["pendente"].(map[string]any)["estado"])

	sonda.ressalvas = nil
	outro := b.pedido(t, "t_crit", `{"valor":"zzz"}`)
	rec, m = b.fazer(http.MethodPost, "/mcp/pendentes/"+outro.ID.String()+"/aprovar", b.jwtDono)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"ressalvas":[]`, "sem ressalva é um array vazio, não null nem ausente")
}

// O estado mudou desde o pedido: 422 preview_desatualizado, nada executa, e a lista de /pendentes
// passa a mostrar o resumo NOVO — a pessoa confere e aprova de novo.
func TestPendentesHTTP_ResumoDesatualizadoDa422EAListaMostraOResumoNovo(t *testing.T) {
	sonda := &sondaEscrita{}
	b := novaBancadaPendentesHTTP(t, sonda)
	pend := b.pedidoComPrevia(t, "t_crit", `{"valor":"abc"}`, previa{Resumo: "vai gravar abc", Efeitos: []string{"Afeta 1 usuário"}})
	url := "/mcp/pendentes/" + pend.ID.String() + "/aprovar"

	rec, m := b.fazer(http.MethodPost, url, b.jwtDono) // o resumo de verdade ("vai gravar abc", sem efeito) difere do gravado
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	require.Equal(t, "preview_desatualizado", m["error"])
	require.Equal(t, msgPreviewDesatualizado, m["mensagem"])
	require.Equal(t, 0, sonda.execucoes)

	_, lista := b.fazer(http.MethodGet, "/mcp/pendentes?id="+pend.ID.String(), b.jwtDono)
	d := lista["destaque"].(map[string]any)
	require.Equal(t, "pendente", d["estado"])
	require.Equal(t, msgPreviewDesatualizado, d["erro"])
	pv := d["preview"].(map[string]any)
	require.Equal(t, "vai gravar abc", pv["resumo"])
	require.NotContains(t, pv, "efeitos", "o resumo gravado é o novo: sem o efeito velho")

	rec, _ = b.fazer(http.MethodPost, url, b.jwtDono)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, sonda.execucoes)
}

// A ação que falhou depois de gravar leva o texto dela à pessoa (500 interrompida), não o genérico.
func TestPendentesHTTP_ErroAposEscritaDa500ComOTextoDaAcao(t *testing.T) {
	const texto = "A campanha foi cancelada, mas NÃO foi apagada: apareceu veiculação nela. Confira em /campaigns."
	sonda := &sondaEscrita{erroExec: &acoes.ErroAposEscrita{Mensagem: texto, Causa: erroTexto("pq: relation secreta does not exist")}}
	b := novaBancadaPendentesHTTP(t, sonda)
	pend := b.pedido(t, "t_crit", `{"valor":"abc"}`)

	rec, m := b.fazer(http.MethodPost, "/mcp/pendentes/"+pend.ID.String()+"/aprovar", b.jwtDono)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	require.Equal(t, "interrompida", m["error"])
	require.Equal(t, texto, m["mensagem"])
	require.NotContains(t, rec.Body.String(), "secreta")
	got := b.estado(t, pend.ID)
	require.Equal(t, "falhou", got.Estado)
	require.Equal(t, texto, *got.Erro)
}

// As ressalvas da execução ficam gravadas e voltam por /pendentes: quem recarrega a página do pedido
// (?id=) lê de novo o "o email não saiu…" — sem isso o aviso só existiria no momento do clique. Na
// lista e no destaque o campo é sempre um array: [] para o pedido que ainda não executou.
func TestPendentesHTTP_RessalvasVoltamNaListaENoDestaque(t *testing.T) {
	sonda := &sondaEscrita{ressalvas: []string{"o email não saiu: a pessoa não tem a senha", "confira com obter_x"}}
	b := novaBancadaPendentesHTTP(t, sonda)
	feito := b.pedido(t, "t_crit", `{"valor":"a"}`)
	aberto := b.pedido(t, "t_crit", `{"valor":"b"}`)
	rec, m := b.fazer(http.MethodPost, "/mcp/pendentes/"+feito.ID.String()+"/aprovar", b.jwtDono)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []any{"o email não saiu: a pessoa não tem a senha", "confira com obter_x"}, m["pendente"].(map[string]any)["ressalvas"])

	rec, m = b.fazer(http.MethodGet, "/mcp/pendentes?id="+feito.ID.String(), b.jwtDono)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	d := m["destaque"].(map[string]any)
	require.Equal(t, "executada", d["estado"])
	require.Equal(t, []any{"o email não saiu: a pessoa não tem a senha", "confira com obter_x"}, d["ressalvas"],
		"recarregar o pedido executado mostra as ressalvas de novo")
	itens := m["items"].([]any)
	require.Len(t, itens, 1, "só o aberto fica na lista")
	require.Equal(t, aberto.ID.String(), itens[0].(map[string]any)["id"])
	require.Equal(t, []any{}, itens[0].(map[string]any)["ressalvas"], "o pedido aberto tem ressalvas: [], não null nem ausente")
	require.Contains(t, rec.Body.String(), `"ressalvas":[]`)
}
