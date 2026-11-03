package mcp

// escrita_ferramentas_test.go — a bancada das tools de escrita (Lote C): banco
// de teste + ConfigServidor com as Escritas ligadas a falsos de supervisor e de
// hub, mais os helpers do protocolo preview → confirmar e da paridade com a
// rota HTTP.
//
// ⚠️ confirmarEExecutar mora aqui e não em mcptest (como o plano previa): ele
// recebe um ConfigServidor, e o mcptest não pode importar o pacote mcp (os
// testes do mcp já importam o mcptest — seria um ciclo).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/acoes"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/mcp/tokens"
)

// bancadaTool é o ambiente de uma tool de escrita: o admin é um USUÁRIO REAL
// (notification_reads tem FK para users) e também o ator das rotas HTTP.
type bancadaTool struct {
	ctx   context.Context
	pool  *pgxpool.Pool
	cfg   ConfigServidor
	E     *Escritas
	sup   *mcptest.SupervisorFalso
	hub   *mcptest.HubFalso
	admin uuid.UUID
	p     *Principal
}

func novaBancadaTool(t *testing.T) *bancadaTool {
	t.Helper()
	ctx, pool := mcptest.DBCatalogo(t)
	// O mcptest não trunca estas duas: cada teste parte do zero.
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `TRUNCATE mcp_confirmacoes_usadas, mcp_acoes_pendentes`)
	require.NoError(t, err)

	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	sup := &mcptest.SupervisorFalso{Campanhas: catalog.NewCampaigns(pool)}
	hubFalso := &mcptest.HubFalso{}
	servico := acoes.NovoServico(pool, sup, hubFalso, nil)
	servico.EmSegundoPlano = mcptest.Sincrono
	leitura := NovaLeitura(pool)
	E := NovasEscritas(pool, []byte(mcptest.SegredoTeste), servico, leitura, nil)
	p := &Principal{UserID: admin, Email: "admin@mcp.teste", Nome: "Admin", TokenID: uuid.New()}
	semearPrincipal(t, ctx, pool, p) // o pedido pendente tem FK para o token
	return &bancadaTool{
		ctx: ctx, pool: pool, E: E, sup: sup, hub: hubFalso, admin: admin,
		cfg: ConfigServidor{Versao: "teste", Portal: "https://portal.teste", Leitura: leitura, Escritas: E},
		p:   p,
	}
}

// chamar chama a tool como o principal da bancada e exige que NÃO seja isError.
func (b *bancadaTool) chamar(t *testing.T, nome string, args any) map[string]any {
	t.Helper()
	texto, erro := chamarNoServidor(t, novoServidor(b.cfg, b.p), nome, args)
	require.False(t, erro, "%s devolveu erro: %s", nome, texto)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(texto), &out), texto)
	return out
}

// chamarComo é o chamar com outro principal (ex.: um segundo admin real).
func (b *bancadaTool) chamarComo(t *testing.T, p *Principal, nome string, args any) map[string]any {
	t.Helper()
	texto, erro := chamarNoServidor(t, novoServidor(b.cfg, p), nome, args)
	require.False(t, erro, "%s devolveu erro: %s", nome, texto)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(texto), &out), texto)
	return out
}

// chamarErro espera isError e devolve o texto.
func (b *bancadaTool) chamarErro(t *testing.T, nome string, args any) string {
	t.Helper()
	texto, erro := chamarNoServidor(t, novoServidor(b.cfg, b.p), nome, args)
	require.True(t, erro, "%s deveria falhar, devolveu: %s", nome, texto)
	return texto
}

// preview chama a tool sem token e exige a resposta de preview (sem executar).
func (b *bancadaTool) preview(t *testing.T, nome string, args map[string]any) map[string]any {
	t.Helper()
	out := b.chamar(t, nome, args)
	require.Equal(t, false, out["executou"], "o preview não executa: %v", out)
	require.NotEmpty(t, out["confirmacao"], "preview sem token: %v", out)
	require.NotNil(t, out["preview"], "sem preview: %v", out)
	return out
}

// confirmarEExecutar faz o protocolo inteiro: preview, lê o token, chama de novo
// com os MESMOS argumentos mais o token. Devolve a resposta da execução.
func (b *bancadaTool) confirmarEExecutar(t *testing.T, nome string, args map[string]any) map[string]any {
	t.Helper()
	pre := b.preview(t, nome, args)
	com := map[string]any{"confirmacao": pre["confirmacao"]}
	for k, v := range args {
		com[k] = v
	}
	out := b.chamar(t, nome, com)
	require.Equal(t, true, out["executou"], "a confirmação deveria executar: %v", out)
	return out
}

// pedePendente chama a tool e exige o ramo crítico (F4-1): executou=false,
// nivel=critico, um pedido aberto gravado para a tool e nenhum token.
func (b *bancadaTool) pedePendente(t *testing.T, nome string, args map[string]any) map[string]any {
	t.Helper()
	out := b.chamar(t, nome, args)
	require.Equal(t, false, out["executou"], "%v", out)
	require.Equal(t, "critico", out["nivel"], "deveria escalar: %v", out)
	require.NotContains(t, out, "confirmacao", "crítico não tem token")
	id, err := uuid.Parse(out["pendente_id"].(string))
	require.NoError(t, err, "pendente_id inválido: %v", out)
	var tool, estado string
	var gravados []byte
	require.NoError(t, b.pool.QueryRow(b.ctx,
		`SELECT tool, estado, args FROM mcp_acoes_pendentes WHERE id = $1 AND estado IN ('pendente','aprovada')`, id).Scan(&tool, &estado, &gravados),
		"o pedido aberto %s não foi gravado", id)
	require.Equal(t, nome, tool)
	// Os args que foram para o JSONB têm de voltar a valer na aprovação, com o
	// In REAL da tool: sem isso, um In que não faz o round trip só estouraria
	// depois, na tela de /pendentes.
	idx, err := indexarEscritas(escritasDe(b.cfg))
	require.NoError(t, err)
	e, ok := idx[nome]
	require.True(t, ok, "a escrita %s não está na lista única", nome)
	require.NoError(t, e.validarArgs(gravados), "os args gravados de %s não passam na revalidação: %s", nome, gravados)
	return out
}

// linhaAuditoria é uma linha do audit_log.
type linhaAuditoria struct {
	Recurso   string
	RecursoID *uuid.UUID
	Ator      uuid.UUID
	Meta      map[string]any
}

func (b *bancadaTool) auditoria(t *testing.T, acao string) []linhaAuditoria {
	t.Helper()
	rows, err := b.pool.Query(b.ctx, `SELECT resource, resource_id, actor_id, metadata FROM audit_log WHERE action = $1 ORDER BY created_at`, acao)
	require.NoError(t, err)
	defer rows.Close()
	var out []linhaAuditoria
	for rows.Next() {
		var l linhaAuditoria
		require.NoError(t, rows.Scan(&l.Recurso, &l.RecursoID, &l.Ator, &l.Meta))
		out = append(out, l)
	}
	require.NoError(t, rows.Err())
	return out
}

// exigeAuditoria confere que existe UMA linha da ação, com o recurso, o ator e o
// token_id certos, e a devolve.
func (b *bancadaTool) exigeAuditoria(t *testing.T, acao, recurso string, recursoID *uuid.UUID) linhaAuditoria {
	t.Helper()
	ls := b.auditoria(t, acao)
	require.Len(t, ls, 1, "linhas de %s", acao)
	l := ls[0]
	require.Equal(t, recurso, l.Recurso)
	if recursoID != nil {
		require.NotNil(t, l.RecursoID)
		require.Equal(t, *recursoID, *l.RecursoID)
	}
	require.Equal(t, b.admin, l.Ator)
	require.Equal(t, "mcp", l.Meta["origem"])
	require.Equal(t, b.p.TokenID.String(), l.Meta["mcp"].(map[string]any)["token_id"])
	return l
}

func (b *bancadaTool) totalAuditorias(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT count(*) FROM audit_log`).Scan(&n))
	return n
}

// esquemaDa devolve o JSON Schema de entrada da tool (como o cliente MCP o vê).
func esquemaDa(t *testing.T, cfg ConfigServidor, nome string) map[string]any {
	t.Helper()
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	_, err := novoServidor(cfg, principalTeste).Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "teste", Version: "1"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { cs.Close() })
	lista, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	for _, tl := range lista.Tools {
		if tl.Name == nome {
			bruto, err := json.Marshal(tl.InputSchema)
			require.NoError(t, err)
			var m map[string]any
			require.NoError(t, json.Unmarshal(bruto, &m))
			return m
		}
	}
	t.Fatalf("tool %s não registrada", nome)
	return nil
}

// propriedades devolve o mapa de propriedades do schema de entrada.
func propriedades(esq map[string]any) map[string]any {
	p, _ := esq["properties"].(map[string]any)
	return p
}

// chamarHandlerCorpo chama um handler HTTP como o admin: método e corpo JSON,
// claims de admin e parâmetros de rota do chi. Aceita 200, 201 e 204.
func chamarHandlerCorpo(t *testing.T, h http.HandlerFunc, metodo, alvo string, corpo any, rota map[string]string, admin uuid.UUID) []byte {
	t.Helper()
	var r *bytes.Reader
	if corpo == nil {
		r = bytes.NewReader(nil)
	} else {
		b, err := json.Marshal(corpo)
		require.NoError(t, err)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(metodo, alvo, r)
	rctx := chi.NewRouteContext()
	for k, v := range rota {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.WithClaims(ctx, &auth.Claims{UserID: admin, Role: "admin"})
	rec := httptest.NewRecorder()
	h(rec, req.WithContext(ctx))
	require.Contains(t, []int{http.StatusOK, http.StatusCreated, http.StatusNoContent}, rec.Code, rec.Body.String())
	return rec.Body.Bytes()
}

func ponteiroUUID(id uuid.UUID) *uuid.UUID { return &id }

// donoDoPortal é quem aprova em /pendentes: o dono do pedido logado no portal
// (sessão do portal, sem token do MCP).
func (b *bancadaTool) donoDoPortal() *Principal {
	return &Principal{UserID: b.admin, Email: b.p.Email, Nome: b.p.Nome}
}

// tentarAprovar aprova o pedido pendente como o dono, sobre a lista REAL das
// escritas (escritasDe), e devolve o desfecho sem exigir sucesso: serve aos
// testes que provam a recusa na aprovação.
func (b *bancadaTool) tentarAprovar(t *testing.T, pendenteID string) (Aprovado, error) {
	t.Helper()
	id, err := uuid.Parse(pendenteID)
	require.NoError(t, err, "pendente_id inválido: %q", pendenteID)
	apr, err := NovasAprovacoes(b.cfg)
	require.NoError(t, err)
	return apr.Aprovar(b.ctx, id, b.donoDoPortal())
}

// aprovar aprova o pedido pendente (pendente_id da resposta da tool) e exige que
// a escrita execute. É o caminho de /pendentes de ponta a ponta: o executor acha
// a escrita pelo nome, revalida os args gravados, reroda o Preview e chama o
// Executar.
func (b *bancadaTool) aprovar(t *testing.T, pendenteID string) Aprovado {
	t.Helper()
	ap, err := b.tentarAprovar(t, pendenteID)
	require.NoError(t, err, "a aprovação deveria executar")
	require.Equal(t, "executada", ap.Pendente.Estado)
	return ap
}

// resultadoJSON decodifica o resultado do Executar guardado na aprovação.
func (a Aprovado) resultadoJSON(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(a.ResultadoJSON, &out), string(a.ResultadoJSON))
	return out
}

// poolFechado é um pool já fechado, para simular a leitura que falha DEPOIS de a escrita ter
// gravado (cada repositório da Leitura aponta para ele; os da escrita e do motor seguem no pool vivo).
func (b *bancadaTool) poolFechado(t *testing.T) *pgxpool.Pool {
	t.Helper()
	morto, err := pgxpool.NewWithConfig(b.ctx, b.pool.Config())
	require.NoError(t, err)
	morto.Close()
	return morto
}

// semearStreamSecreto grava em cada emissora uma stream_url inconfundível (o painel de streaming
// tem a credencial na URL) e devolve os textos que NUNCA podem aparecer no que o MCP devolve ou grava.
func semearStreamSecreto(t *testing.T, b *bancadaTool, emissoras ...uuid.UUID) []string {
	t.Helper()
	proibidos := []string{"SEGREDO-DO-STREAM", "painel-secreto.teste"}
	for i, e := range emissoras {
		url := fmt.Sprintf("http://painel-secreto.teste:8000/SEGREDO-DO-STREAM-%d?auth=ZZ%d", i, i)
		tag, err := b.pool.Exec(b.ctx, `UPDATE stations SET stream_url = $2 WHERE id = $1`, e, url)
		require.NoError(t, err)
		require.EqualValues(t, 1, tag.RowsAffected(), "a emissora semeada existe")
		proibidos = append(proibidos, url)
	}
	return proibidos
}

// chavesDoJSON são todas as chaves de objeto do JSON, em qualquer nível.
func chavesDoJSON(t *testing.T, bruto []byte) map[string]bool {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal(bruto, &v), string(bruto))
	chaves := map[string]bool{}
	var desce func(any)
	desce = func(x any) {
		switch y := x.(type) {
		case map[string]any:
			for k, val := range y {
				chaves[k] = true
				desce(val)
			}
		case []any:
			for _, val := range y {
				desce(val)
			}
		}
	}
	desce(v)
	return chaves
}

// exigeSemSegredoNaEscrita é a categoria 6 do contrato das escritas críticas: o resultado da
// aprovação, a resposta do pedido, a linha do pedido (args gravados, preview, resultado, ressalvas)
// e TODA linha do audit_log não têm campo proibido (stream_url, hub_id, senha, token…) nem o texto
// de uma stream_url semeada (proibidos). Quem chama aprova o pedido antes.
func (b *bancadaTool) exigeSemSegredoNaEscrita(t *testing.T, pendenteID string, ap Aprovado, pedido map[string]any, proibidos []string) {
	t.Helper()
	var linha string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT to_jsonb(p)::text FROM mcp_acoes_pendentes p WHERE id = $1`, pendenteID).Scan(&linha))
	jsons := map[string][]byte{
		"resultado da aprovação": ap.ResultadoJSON,
		"resposta do pedido":     []byte(mustJSON(t, pedido)),
		"ressalvas da aprovação": []byte(mustJSON(t, ap.Ressalvas)),
		"linha do pedido":        []byte(linha),
	}
	rows, err := b.pool.Query(b.ctx, `SELECT action || ' ' || metadata::text FROM audit_log ORDER BY created_at`)
	require.NoError(t, err)
	defer rows.Close()
	n := 0
	for rows.Next() {
		var l string
		require.NoError(t, rows.Scan(&l))
		n++
		jsons[fmt.Sprintf("audit_log #%d", n)] = []byte(l[strings.Index(l, " ")+1:])
		require.NotContains(t, l, "stream_url")
	}
	require.NoError(t, rows.Err())
	require.NotZero(t, n, "a aprovação grava auditoria: sem linhas não há o que provar")
	for nome, bruto := range jsons {
		if len(bruto) == 0 || string(bruto) == "null" {
			continue
		}
		chaves := chavesDoJSON(t, bruto)
		for _, proibido := range camposProibidosNaEscrita {
			require.False(t, chaves[proibido], "o campo %q apareceu em %s", proibido, nome)
		}
		for _, texto := range proibidos {
			require.NotContains(t, string(bruto), texto, "a stream_url semeada vazou em "+nome)
		}
	}
}

// aprovarPelaRota aprova o pedido pelo handler HTTP REAL de /pendentes (PendentesHTTP.Aprovar,
// sobre as Aprovacoes da lista real das escritas) e devolve o corpo do 200 decodificado:
// {pendente, resultado, ressalvas}. É o que a pessoa vê no portal.
func (b *bancadaTool) aprovarPelaRota(t *testing.T, pendenteID string) map[string]any {
	t.Helper()
	apr, err := NovasAprovacoes(b.cfg)
	require.NoError(t, err)
	h := &PendentesHTTP{Tokens: tokens.NovoStore(b.pool, []byte(mcptest.SegredoTeste)), Pendentes: b.E.Pendentes, Aprovacoes: apr, Log: zap.NewNop()}
	corpo := chamarHandlerCorpo(t, h.Aprovar, http.MethodPost, "/mcp/pendentes/"+pendenteID+"/aprovar", nil, map[string]string{"id": pendenteID}, b.admin)
	var out map[string]any
	require.NoError(t, json.Unmarshal(corpo, &out), string(corpo))
	return out
}

// ressalvasGravadas lê a coluna ressalvas da linha do pedido.
func (b *bancadaTool) ressalvasGravadas(t *testing.T, pendenteID string) []string {
	t.Helper()
	var r []string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT ressalvas FROM mcp_acoes_pendentes WHERE id = $1`, pendenteID).Scan(&r))
	return r
}

// textoDasRessalvas junta as ressalvas para asserções de trecho.
func textoDasRessalvas(r []string) string { return strings.Join(r, "\n") }

// estadoDaPendente lê o estado do pedido no banco (pendente, aprovada, executada…).
func (b *bancadaTool) estadoDaPendente(t *testing.T, pendenteID string) string {
	t.Helper()
	var estado string
	require.NoError(t, b.pool.QueryRow(b.ctx, `SELECT estado FROM mcp_acoes_pendentes WHERE id = $1`, pendenteID).Scan(&estado))
	return estado
}
