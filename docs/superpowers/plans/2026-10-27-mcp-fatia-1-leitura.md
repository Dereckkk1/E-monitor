# MCP do E-monitor — Fatia 1 (Leitura) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Dar ao MCP do E-monitor as 26 tools de leitura da spec §6 "Fatia 1" (clientes, campanhas, grade, plano do dia, materiais, emissoras, veiculações, usuários, integrações, notificações), cada uma devolvendo os MESMOS números que a tela, provado por teste de paridade contra o handler HTTP.

**Architecture:** As tools moram em `workers/internal/mcp/tools_<familia>.go` e chamam os repositórios do `catalog`/`users`/`welcome` que os handlers HTTP já chamam, reunidos numa struct `Leitura` montada no `NovoModulo` e passada por `ConfigServidor`. Onde a leitura de que a tool precisa só existe como SQL dentro de um handler (entregas de webhook, chaves de API), ela é extraída para o `catalog` antes e o handler passa a chamá-la. Onde nenhuma rota lê o dado (ponte `hub_id` do cliente, campanha por código do hub, classe Anatel e cidades no alcance de UMA emissora, projeções de uma veiculação), o `catalog` ganha uma função de leitura nova — leitura de coluna, nunca regra. O MCP só traduz argumentos, pagina e molda a resposta no contrato (§3.3).

**Tech Stack:** Go 1.26, go-sdk v1.8.0 (`github.com/modelcontextprotocol/go-sdk/mcp`), pgx v5, chi v5, testify.

**Spec:** [docs/superpowers/specs/2026-10-27-mcp-server-design.md](../specs/2026-10-27-mcp-server-design.md) §3 (regra de ouro), §4.4 (resultado), §6 "Fatia 1", §7.
**Contrato:** `hub/docs/architecture/mcp-ecossistema.md` (em `origin/main` do hub) §3.2–§3.4 e §3.6.
**Handoff que originou este plano:** [HANDOFF-2026-10-27-mcp.md](../HANDOFF-2026-10-27-mcp.md).

---

## Decisões deste plano (o dono aprova junto com o plano)

| # | Decisão | Por quê |
|---|---|---|
| F1-1 | **`plano_do_dia` devolve os ingredientes autoritativos**, não o "progresso por faixa". Devolve: o saldo da célula (`daily_play_summary_for`, o mesmo da grade), de onde vem a meta (ajuste do dia × regras × sem meta), as faixas que valem no dia (decididas por `categorizer.RuleCoversDay`, que é a MESMA função que o `Settle` usa para somar a meta), as que não valem e por quê, e as veiculações do dia com a categoria do servidor. **Não** devolve "quantas tocadas caíram em cada faixa". | Hoje o "plano do dia" do `DayDetailModal` é calculado inteiro no JavaScript (`buildDayPlan`, `DayDetailModal.jsx:124`) — não há função Go equivalente. O "progresso por faixa" é uma heurística só de tela (credita a tocada à janela mais curta e, no empate, à que começa antes) que o próprio doc `detections-day-plan.md` diz não reconciliar com o saldo. Portar essa heurística para Go criaria um terceiro motor de categoria (além de `Settle` e `recatClassifiedCTE`). O saldo, a meta e a categoria de cada tocada já vêm do servidor. |
| F1-2 | **Resposta em português, por whitelist de campos** (`campanha: {id, nome}`, `inicio`, `fim`, `estado`…) em vez de repassar as structs do `catalog`. | Contrato §3.2/§3.3 e a Fatia 0 (`quem_sou_eu` já é em português). A whitelist garante que campo técnico ou segredo nunca vaze se a struct do `catalog` ganhar um campo: `stream_url`, `master_storage_path`, `master_sha256`, `evidence_key`, `webhook_secret` (nem o prefixo), `key_hash`, token/link de boas-vindas (o link mostra a senha inicial em claro). |
| F1-3 | **Paginação no MCP**: listas que o repositório devolve inteiras e são pequenas (campanhas, regras, preços, materiais de um cliente, tipos) são paginadas em memória (`paginar`); listas que o repositório já pagina por página (clientes, usuários, emissoras, veiculações) usam `buscarJanela`, que traduz `limit/offset` em no máximo duas páginas. Nenhum repositório muda de comportamento. | Contrato §3.3 exige `limit/offset` com `total` real. Mudar a paginação dos repositórios mexeria nas telas. |
| F1-4 | **Leituras novas no `catalog`, sem regra:** `Clients.HubIDs`, `Clients.ListWebhookDeliveries`, `Clients.ListAPIKeys`, `Campaigns.ListByHubCode`, `Campaigns.CancelledAtFor`, `Stations.Anatel`, `Stations.CoverageCities`, `Detections.Attributions`, filtros opcionais `StationID/MaterialID/Categories` no `ListPagedFilter`, `catalog.TodaySaoPaulo`, `catalog.CPMDaCampanha`, e `categorizer.RuleCoversDay` exportada. Os dois handlers com SQL embutido (`webhook-deliveries`, `api-keys`) passam a chamar o `catalog` (mesma resposta). | Regra de ouro (spec §3): a tool chama a mesma função que o handler. Onde não há função, cria-se uma no `catalog` e o handler passa a usá-la. |
| F1-5 | **`CPMDaCampanha` sai do frontend para o `catalog`.** Hoje o CPM de `/campaigns` é calculado no navegador (`(investido + bonificação) ÷ impactos × 1000`, ou o `fixed_cpm`). O `financeiro_campanhas` precisa devolvê-lo pronto (as instruções mandam o modelo nunca recalcular). | A fórmula é a mesma de `insights.go:1167`; o teste trava a bonificação no numerador (a regressão de −6,5% já aconteceu uma vez, `campaign-fixed-cpm.md`). |
| F1-6 | **Canceladas:** `listar_campanhas` as deixa de fora por padrão (`incluir_canceladas` ou `estados: ["cancelada"]` as traz); quando aparecem, vem a ressalva "só histórico, déficit congelado em `cancelada_em`". `financeiro_campanhas` exige ids explícitos (1 a 100) e marca as canceladas. | `docs/features/cancelled-campaign-handling.md`: cancelada fora de seletores e KPIs, marcada no histórico. |
| F1-7 | **Paridade contra fixtures no banco de teste**, não contra o restore de prod: o container `docker-postgres-1` (restore de prod, memória `local-db-has-prod-restore`) não existe mais desde o reset do Docker. Cada tool que espelha uma tela tem um teste que semeia o cenário, chama o handler HTTP com claims de admin e a tool, e compara os números. A conferência contra dado real fica para o roteiro de deploy (Task 18). | Spec §7 pede paridade "contra o banco de dev (restore de prod)"; sem o restore, fixtures determinísticas são o que dá para rodar a cada commit. |
| F1-8 | **Erros:** entrada inválida → `"<campo> inválido … [entrada_invalida]"`; inexistente → `"<coisa> não encontrada [nao_encontrado]"`; ambíguo → `[ambiguo]`; erro Go → log + `"erro interno do E-monitor … [interno]"`. Nunca o `err.Error()` cru. | Handoff: o SDK repassaria `SQLSTATE …` ao modelo. |

**Fora desta fatia:** deep-link de relatório (`/insights?…`, `/detections?…` — Fatia 2), tools de relatório, qualquer escrita. O único link aqui é o de tela que já existe sem filtro (`/campaigns/:id/edit`, `/detections/:id`).

---

## Antes de começar — leia

- `CLAUDE.md` da raiz: regras 4.8, 5 (nunca `npm install` no `frontend/` — esta fatia não toca o frontend) e 6.x.
- **Árvore compartilhada.** A pasta principal `C:\Users\marke\Desktop\Programas\E-Series\E-monitor` é usada por outras sessões (em 2026-10-27 estava em `feat/username-login`). **Trabalhe só no worktree `C:\tmp\emonitor-mcp`** (branch `feat/mcp-server`). Nunca rode `git checkout`/`git switch` na pasta principal. Nunca `git stash` sem tag.
- **Banco de teste:** um por agente, no container `rc-mcp-test-pg` (porta 55440). Rode SEMPRE `go test -p 1` com `TEST_DATABASE_URL` apontando para o SEU banco. O guard (`dbtest.GuardOrSkip`) recusa banco com mais de 50 emissoras ou 10 clientes: os helpers deste plano truncam o que semeiam.
- **CRLF:** `gofmt -l` acusa quase tudo por causa do autocrlf. Rode `gofmt -l` só nos arquivos que você criou/tocou.
- Handlers e repositórios citados aqui são idênticos no `master` e na `feat/mcp-server`; os números de linha do `router.go` diferem (+10 na branch).

## Mapa de arquivos

| Arquivo | Responsabilidade |
|---|---|
| `workers/internal/mcp/toolkit.go` | (modifica) paginação (`Paginacao`, `lista`, `paginar`, `buscarJanela`, `mapear`), `falhaInterna`, `ok`/`recusa`/`recusaEntrada`/`naoEncontrado`, `ferramentaLeitura`, `ref`, datas, fuso SP |
| `workers/internal/mcp/leitura.go` | (cria) struct `Leitura` com os repositórios; `NovaLeitura(pool)`; helpers de nomes (`nomesClientes`, `nomesEmissoras`, `nomesTipos`, `nomesMateriais`, `todasCampanhas`) |
| `workers/internal/mcp/servidor.go` | (modifica) `ConfigServidor{Versao, Portal, Leitura, Log}`, `novoServidor` registra as famílias, `ToolsEsperadas` |
| `workers/internal/mcp/modulo.go` | (modifica) monta `Leitura` e passa `Portal`/`Log` |
| `workers/internal/mcp/instrucoes.go` | (modifica) linhas "Listas" e "Pontes" |
| `workers/internal/mcp/tools_clientes.go` | `listar_clientes`, `obter_cliente`, `pmm_target_cliente` |
| `workers/internal/mcp/tools_campanhas.go` | `listar_campanhas`, `obter_campanha`, `financeiro_campanhas`, `listar_materiais_campanha`, `listar_regras_distribuicao`, `listar_overrides`, `listar_precos_campanha` |
| `workers/internal/mcp/tools_grade.go` | `grade_campanha`, `plano_do_dia` |
| `workers/internal/mcp/tools_materiais.go` | `listar_materiais`, `obter_material`, `listar_tipos_material` |
| `workers/internal/mcp/tools_emissoras.go` | `buscar_emissoras`, `obter_emissora` |
| `workers/internal/mcp/tools_veiculacoes.go` | `listar_veiculacoes`, `obter_veiculacao`, `veiculacoes_por_material` |
| `workers/internal/mcp/tools_usuarios.go` | `listar_usuarios`, `obter_usuario` |
| `workers/internal/mcp/tools_integracoes.go` | `obter_webhook_cliente`, `listar_entregas_webhook`, `listar_chaves_api`, `minhas_notificacoes` |
| `workers/internal/mcp/ferramentas_test.go` | (cria) `chamarNoServidor`, `chamarTool`, `chamarToolErro`, `chamarHandler`, `cfgTeste` |
| `workers/internal/mcp/toolkit_test.go` | (cria) unidade da paginação e do `falhaInterna` |
| `workers/internal/mcp/tools_*_test.go` | testes de comportamento e paridade de cada família |
| `workers/internal/mcp/mcptest/catalogo.go` | (cria) `DBCatalogo` e fixtures exportadas (cliente, emissora, tipo, material, campanha, regra, ajuste, preço, veiculação) |
| `workers/internal/catalog/hoje_sp.go` (+`_test`) | `TodaySaoPaulo` |
| `workers/internal/catalog/cpm.go` (+`_test`) | `CPMDaCampanha` |
| `workers/internal/catalog/mcp_leituras.go` (+`_test`) | `Clients.HubIDs`, `Campaigns.ListByHubCode`, `Campaigns.CancelledAtFor`, `Stations.Anatel`, `Stations.CoverageCities`, `Detections.Attributions` |
| `workers/internal/catalog/clients_integracoes.go` (+`_test`) | `Clients.ListWebhookDeliveries`, `Clients.ListAPIKeys` |
| `workers/internal/catalog/detections.go` | (modifica) filtros `StationID`, `MaterialID`, `Categories` no `ListPaged` |
| `workers/internal/categorizer/categorizer.go` (+`_test`) | `RuleCoversDay` exportada |
| `workers/internal/api/handlers/insights.go` | `todaySaoPaulo` delega a `catalog.TodaySaoPaulo` |
| `workers/internal/api/handlers/webhooks.go`, `apikeys.go` | passam a chamar o `catalog` |
| `docs/features/mcp.md`, `docs/operations/mcp.md` | tabela de tools, como testar |
| `docs/superpowers/HANDOFF-AAAA-MM-DD-mcp-fatia-2.md` | handoff da Fatia 2 |

**Lotes para o subagent-driven-development:** A = Tasks 0–5 (fundação e leituras do `catalog`); B = Tasks 6–9 (clientes, campanhas, grade); C = Tasks 10–14 (materiais, emissoras, veiculações, usuários, integrações); D = Tasks 15–18 (instruções, desempenho, docs, verificação). Revisão de aderência + qualidade ao fim de cada lote; revisão final da branch no fim do D.

---

### Task 0: Ambiente — worktree e banco de teste

**Files:** nenhum

- [ ] **Step 1: Confira o worktree e a branch**

```bash
cd C:/tmp/emonitor-mcp && git branch --show-current && git status --short
```
Expected: `feat/mcp-server` e árvore limpa. Se o worktree não existir: `git -C C:/Users/marke/Desktop/Programas/E-Series/E-monitor worktree add C:/tmp/emonitor-mcp feat/mcp-server`.

- [ ] **Step 2: Container de teste de pé**

```bash
docker ps --filter name=rc-mcp-test-pg --format '{{.Names}} {{.Status}}'
```
Expected: `rc-mcp-test-pg Up …`. Se não aparecer, siga "Ambiente de teste" do handoff (`docs/superpowers/HANDOFF-2026-10-27-mcp.md`) para recriar e migrar.

- [ ] **Step 3: Crie o seu banco a partir do template migrado**

```bash
docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -c "CREATE DATABASE radiocheck_mcp_test_f1 TEMPLATE radiocheck_mcp_test_d2;"
```
Expected: `CREATE DATABASE` (se já existir, siga). Daqui em diante, todo comando de teste usa:
```bash
export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f1?sslmode=disable"
```
Subagentes em paralelo usam bancos próprios (`…_f1a`, `…_f1b`, criados igual).

- [ ] **Step 4: Linha de base verde**

```bash
cd C:/tmp/emonitor-mcp/workers && go build ./... && go test -p 1 ./internal/mcp/... 2>&1 | tail -5
```
Expected: `ok` em todos os pacotes de `internal/mcp`.

---

### Task 1: Toolkit — paginação, falha interna e registro de leitura

**Files:**
- Modify: `workers/internal/mcp/toolkit.go`
- Create: `workers/internal/mcp/toolkit_test.go`
- Create: `workers/internal/mcp/ferramentas_test.go`
- Modify: `workers/internal/mcp/servidor.go` (só `ConfigServidor` e seus métodos)

- [ ] **Step 1: Escreva o teste da paginação e do erro interno**

`workers/internal/mcp/toolkit_test.go`:
```go
package mcp

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestPaginacao_Normalizar(t *testing.T) {
	cases := []struct{ in Paginacao; limit, offset int }{
		{Paginacao{}, 25, 0},
		{Paginacao{Limit: 500, Offset: -3}, 100, 0},
		{Paginacao{Limit: 10, Offset: 999_999_999}, 10, offsetTeto},
	}
	for _, c := range cases {
		l, o := c.in.normalizar()
		require.Equal(t, c.limit, l)
		require.Equal(t, c.offset, o)
	}
}

func TestPaginar_TotalRealEHasMore(t *testing.T) {
	todos := []int{1, 2, 3, 4, 5}
	l := paginar(todos, Paginacao{Limit: 2, Offset: 2})
	require.Equal(t, []int{3, 4}, l.Items)
	require.Equal(t, pagina{Total: 5, Limit: 2, Offset: 2, Returned: 2, HasMore: true}, l.Page)

	fim := paginar(todos, Paginacao{Limit: 2, Offset: 9})
	require.Empty(t, fim.Items)
	require.NotNil(t, fim.Items, "items vazio é [], nunca null")
	require.False(t, fim.Page.HasMore)
	require.Equal(t, 5, fim.Page.Total)
}

// fonte paginada que imita os repositórios com COUNT(*) OVER (): total 0
// numa página além do fim.
func fontePaginada(todos []int) func(page, size int) ([]int, int, error) {
	return func(page, size int) ([]int, int, error) {
		ini := (page - 1) * size
		if ini >= len(todos) {
			return nil, 0, nil
		}
		return todos[ini:min(ini+size, len(todos))], len(todos), nil
	}
}

func TestBuscarJanela_OffsetForaDoMultiplo(t *testing.T) {
	todos := []int{1, 2, 3, 4, 5, 6, 7}
	l, err := buscarJanela(Paginacao{Limit: 3, Offset: 2}, fontePaginada(todos))
	require.NoError(t, err)
	require.Equal(t, []int{3, 4, 5}, l.Items)
	require.Equal(t, 7, l.Page.Total)
	require.True(t, l.Page.HasMore)
}

func TestBuscarJanela_AlemDoFimTrazTotalReal(t *testing.T) {
	l, err := buscarJanela(Paginacao{Limit: 3, Offset: 30}, fontePaginada([]int{1, 2, 3, 4}))
	require.NoError(t, err)
	require.Empty(t, l.Items)
	require.Equal(t, 4, l.Page.Total, "total real mesmo com a página vazia")
}

func TestBuscarJanela_PropagaErro(t *testing.T) {
	_, err := buscarJanela(Paginacao{}, func(int, int) ([]int, int, error) { return nil, 0, errors.New("x") })
	require.Error(t, err)
}

// O erro Go de uma tool nunca chega ao modelo: vira "[interno]" e vai pro log.
func TestFerramentaLeitura_ErroInternoNaoVaza(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "1"}, nil)
	ferramentaLeitura(s, ConfigServidor{Leitura: &Leitura{}}, "quebra", "teste",
		func(context.Context, *Leitura, struct{}) (*sdk.CallToolResult, error) {
			return nil, errors.New(`ERROR: relation "x" does not exist (SQLSTATE 42P01)`)
		})
	texto, erro := chamarNoServidor(t, s, "quebra", map[string]any{})
	require.True(t, erro)
	require.Contains(t, texto, "[interno]")
	require.NotContains(t, texto, "SQLSTATE")
}

func TestFerramentaLeitura_SemLeituraFalhaFechado(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "1"}, nil)
	ferramentaLeitura(s, ConfigServidor{}, "sem", "teste",
		func(context.Context, *Leitura, struct{}) (*sdk.CallToolResult, error) {
			t.Fatal("não pode chegar aqui sem Leitura")
			return nil, nil
		})
	texto, erro := chamarNoServidor(t, s, "sem", map[string]any{})
	require.True(t, erro)
	require.Contains(t, texto, "[interno]")
}
```

`workers/internal/mcp/ferramentas_test.go` (helpers de todos os testes de tool):
```go
package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
)

// chamarNoServidor liga um cliente MCP em memória ao servidor e chama a tool.
// Devolve o texto do primeiro conteúdo e se veio isError.
func chamarNoServidor(t *testing.T, s *sdk.Server, nome string, args any) (string, bool) {
	t.Helper()
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	_, err := s.Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "teste", Version: "1"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { cs.Close() })
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: nome, Arguments: args})
	require.NoError(t, err)
	require.NotEmpty(t, res.Content)
	return res.Content[0].(*sdk.TextContent).Text, res.IsError
}

// cfgTeste é o ConfigServidor das tools de leitura sobre o banco de teste.
func cfgTeste(pool *pgxpool.Pool) ConfigServidor {
	return ConfigServidor{Versao: "teste", Portal: "https://portal.teste", Leitura: NovaLeitura(pool)}
}

// chamarTool chama a tool do servidor real e devolve o JSON do resultado.
// Falha o teste se vier isError.
func chamarTool(t *testing.T, cfg ConfigServidor, nome string, args any) map[string]any {
	t.Helper()
	texto, erro := chamarNoServidor(t, novoServidor(cfg, principalTeste), nome, args)
	require.False(t, erro, "%s devolveu erro: %s", nome, texto)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(texto), &out), texto)
	return out
}

// chamarToolErro espera isError e devolve o texto.
func chamarToolErro(t *testing.T, cfg ConfigServidor, nome string, args any) string {
	t.Helper()
	texto, erro := chamarNoServidor(t, novoServidor(cfg, principalTeste), nome, args)
	require.True(t, erro, "%s deveria falhar, devolveu: %s", nome, texto)
	return texto
}

// chamarHandler chama um handler HTTP como um admin chamaria pela tela:
// claims de admin no contexto e os parâmetros de rota do chi. Exige 200.
func chamarHandler(t *testing.T, h http.HandlerFunc, alvo string, rota map[string]string, admin uuid.UUID) []byte {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, alvo, nil)
	rctx := chi.NewRouteContext()
	for k, v := range rota {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.WithClaims(ctx, &auth.Claims{UserID: admin, Role: "admin"})
	rec := httptest.NewRecorder()
	h(rec, req.WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return rec.Body.Bytes()
}

// itens devolve out["items"] como lista de objetos.
func itens(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	bruto, ok := out["items"].([]any)
	require.True(t, ok, "sem items: %v", out)
	res := make([]map[string]any, len(bruto))
	for i, v := range bruto {
		res[i] = v.(map[string]any)
	}
	return res
}

// pag devolve out["page"].
func pag(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	p, ok := out["page"].(map[string]any)
	require.True(t, ok, "sem page: %v", out)
	return p
}

// ids extrai o campo "id" (ou o id de um ref em campo) de cada item.
func ids(t *testing.T, xs []map[string]any, campo string) []string {
	t.Helper()
	out := make([]string, len(xs))
	for i, x := range xs {
		if campo == "id" {
			out[i] = x["id"].(string)
			continue
		}
		out[i] = x[campo].(map[string]any)["id"].(string)
	}
	return out
}
```

- [ ] **Step 2: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test ./internal/mcp/ -run 'TestPaginacao|TestPaginar|TestBuscarJanela|TestFerramentaLeitura' 2>&1 | head -20
```
Expected: FAIL de compilação (`undefined: Paginacao`, `undefined: Leitura`, `undefined: NovaLeitura`…).

- [ ] **Step 3: Implemente o toolkit**

Acrescente ao fim de `workers/internal/mcp/toolkit.go` (e ajuste o bloco de imports para incluir `context`, `fmt`, `time`, `github.com/google/uuid` e `go.uber.org/zap`):
```go
// ── Listas (contrato §3.3) ──────────────────────────────────────────────

const (
	limitePadrao = 25
	limiteTeto   = 100
	// offsetTeto: offset sem teto deixaria o modelo pedir OFFSET 10^9 ao banco
	// (lição do hub, offsetSchema sem limite).
	offsetTeto = 100_000
)

// Paginacao entra embutida no argumento de toda tool de lista.
type Paginacao struct {
	Limit  int `json:"limit,omitempty" jsonschema:"Quantos itens devolver: padrão 25, máximo 100."`
	Offset int `json:"offset,omitempty" jsonschema:"Quantos itens pular (padrão 0). Página seguinte: offset + page.returned, enquanto page.has_more for true."`
}

func (p Paginacao) normalizar() (limit, offset int) {
	limit = p.Limit
	if limit <= 0 {
		limit = limitePadrao
	}
	if limit > limiteTeto {
		limit = limiteTeto
	}
	offset = max(p.Offset, 0)
	if offset > offsetTeto {
		offset = offsetTeto
	}
	return limit, offset
}

type pagina struct {
	Total    int  `json:"total"`
	Limit    int  `json:"limit"`
	Offset   int  `json:"offset"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"has_more"`
}

// lista é o envelope de toda lista: {items, page, ressalvas?}.
type lista[T any] struct {
	Items     []T      `json:"items"`
	Page      pagina   `json:"page"`
	Ressalvas []string `json:"ressalvas,omitempty"`
}

func novaLista[T any](items []T, total, limit, offset int) lista[T] {
	if items == nil {
		items = []T{}
	}
	return lista[T]{Items: items, Page: pagina{
		Total: total, Limit: limit, Offset: offset, Returned: len(items),
		HasMore: offset+len(items) < total,
	}}
}

// paginar fatia uma lista que já está inteira em memória.
func paginar[T any](todos []T, p Paginacao) lista[T] {
	limit, offset := p.normalizar()
	ini := min(offset, len(todos))
	fim := min(ini+limit, len(todos))
	return novaLista(todos[ini:fim], len(todos), limit, offset)
}

// listaInteira: lista que cabe numa página só (limit nunca fica em 0).
func listaInteira[T any](todos []T) lista[T] {
	return novaLista(todos, len(todos), max(len(todos), 1), 0)
}

// buscarJanela traduz limit/offset para um repositório que pagina por página
// (page 1-based, size). Offset fora do múltiplo de limit lê duas páginas.
// Repositórios que contam com COUNT(*) OVER () devolvem total 0 numa página
// além do fim; aí relê a primeira linha só para ter o total real.
func buscarJanela[T any](p Paginacao, buscar func(page, size int) ([]T, int, error)) (lista[T], error) {
	limit, offset := p.normalizar()
	pg := offset/limit + 1
	itens, total, err := buscar(pg, limit)
	if err != nil {
		return lista[T]{}, err
	}
	inicio := offset % limit
	if inicio > 0 && len(itens) == limit {
		mais, t2, err := buscar(pg+1, limit)
		if err != nil {
			return lista[T]{}, err
		}
		itens = append(itens, mais...)
		total = max(total, t2)
	}
	inicio = min(inicio, len(itens))
	janela := itens[inicio:min(inicio+limit, len(itens))]
	if len(janela) == 0 && offset > 0 {
		if _, total, err = buscar(1, 1); err != nil {
			return lista[T]{}, err
		}
	}
	return novaLista(janela, total, limit, offset), nil
}

// mapear troca o tipo dos itens mantendo page e ressalvas.
func mapear[A, B any](l lista[A], f func(A) B) lista[B] {
	out := make([]B, len(l.Items))
	for i, a := range l.Items {
		out[i] = f(a)
	}
	return lista[B]{Items: out, Page: l.Page, Ressalvas: l.Ressalvas}
}

// ── Referências, datas e fuso ───────────────────────────────────────────

// ref: todo id vem com o nome (contrato §3.2), para o modelo não precisar de
// uma segunda chamada.
type ref struct {
	ID   string `json:"id"`
	Nome string `json:"nome"`
}

func refDe(id uuid.UUID, nome string) ref { return ref{ID: id.String(), Nome: nome} }

// refs monta refs na ordem dos ids; id sem nome conhecido vem com nome "".
func refs(ids []uuid.UUID, nomes map[uuid.UUID]string) []ref {
	out := make([]ref, len(ids))
	for i, id := range ids {
		out[i] = refDe(id, nomes[id])
	}
	return out
}

// fusoSP: America/Sao_Paulo, com fallback UTC-3 se faltar tzdata (o Brasil
// não tem horário de verão desde 2019) — o mesmo do handlers/insights.go.
var fusoSP = func() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return time.FixedZone("BRT", -3*3600)
	}
	return loc
}()

// dia formata uma data de calendário (DATE do Postgres chega como meia-noite UTC).
func dia(t time.Time) string { return t.Format("2006-01-02") }

// instante formata um timestamptz em ISO 8601 UTC (contrato §3.3).
func instante(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func instanteOpc(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := instante(*t)
	return &s
}

// lerDia lê AAAA-MM-DD como data de calendário (meia-noite UTC, igual ao DATE do pgx).
func lerDia(s string) (time.Time, bool) {
	d, err := time.Parse("2006-01-02", s)
	return d, err == nil
}

// limitesDoDiaSP: [00:00, 23:59:59.999] do dia d em America/Sao_Paulo —
// o mesmo recorte que o DayDetailModal manda ao GET /detections.
func limitesDoDiaSP(d time.Time) (time.Time, time.Time) {
	ini := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, fusoSP)
	return ini, ini.AddDate(0, 0, 1).Add(-time.Millisecond)
}

// ── Registro e respostas das tools de leitura ───────────────────────────

// ok e recusa são as formas de dois valores de resultado/falha, para as
// funções registradas por ferramentaLeitura.
func ok(v any) (*sdk.CallToolResult, error) {
	r, _, err := resultado(v)
	return r, err
}

func recusa(mensagem, codigo string) (*sdk.CallToolResult, error) {
	r, _, _ := falha(mensagem, codigo)
	return r, nil
}

func recusaEntrada(campo string) (*sdk.CallToolResult, error) {
	return recusa(fmt.Sprintf("%s inválido: passe o valor exato que outra tool devolveu (nunca invente; pergunte ao usuário)", campo), "entrada_invalida")
}

func naoEncontrado(oQue string) (*sdk.CallToolResult, error) {
	return recusa(oQue+" não encontrado no E-monitor", "nao_encontrado")
}

// falhaInterna loga o erro e devolve uma falha genérica: o SDK repassaria
// err.Error() cru ao modelo (SQLSTATE, nome de tabela).
func falhaInterna(log *zap.Logger, tool string, err error) (*sdk.CallToolResult, any, error) {
	log.Error("mcp: falha interna numa tool", zap.String("tool", tool), zap.Error(err))
	return falha("erro interno do E-monitor; tente de novo e, se persistir, avise o suporte", "interno")
}

// ferramentaLeitura registra uma tool de leitura: anotações de leitura,
// falha fechada sem Leitura configurada e nenhum erro Go chegando ao modelo.
func ferramentaLeitura[In any](s *sdk.Server, cfg ConfigServidor, nome, descricao string,
	fn func(ctx context.Context, L *Leitura, in In) (*sdk.CallToolResult, error)) {
	sdk.AddTool(s, &sdk.Tool{Name: nome, Description: descricao, Annotations: anotacoesLeitura()},
		func(ctx context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, any, error) {
			if cfg.Leitura == nil {
				return falhaInterna(cfg.log(), nome, fmt.Errorf("mcp: Leitura não configurada"))
			}
			res, err := fn(ctx, cfg.Leitura, in)
			if err != nil {
				return falhaInterna(cfg.log(), nome, err)
			}
			return res, nil, nil
		})
}
```

Em `workers/internal/mcp/servidor.go`, troque `ConfigServidor` por (e acrescente `go.uber.org/zap` aos imports):
```go
type ConfigServidor struct {
	Versao string
	// Portal: origem do portal (NOTIFICATIONS_BASE_URL) para os links de tela.
	Portal string
	// Leitura: os repositórios das tools de leitura. nil no teste de
	// inventário, que só lista as tools (a chamada falha fechada).
	Leitura *Leitura
	Log     *zap.Logger
}

func (c ConfigServidor) log() *zap.Logger {
	if c.Log == nil {
		return zap.NewNop()
	}
	return c.Log
}

// link devolve a URL de uma tela do portal, ou nil se a origem não estiver
// configurada (null = não se aplica).
func (c ConfigServidor) link(caminho string) *string {
	if c.Portal == "" {
		return nil
	}
	u := strings.TrimRight(c.Portal, "/") + caminho
	return &u
}
```
(`strings` entra nos imports de `servidor.go`.)

Crie `workers/internal/mcp/leitura.go` — por ora só a struct vazia e o construtor que a Task 2 completa:
```go
package mcp

import "github.com/jackc/pgx/v5/pgxpool"

// Leitura reúne os repositórios que as tools de leitura chamam — os MESMOS
// que os handlers HTTP usam (cmd/api/main.go). Nenhuma regra mora aqui.
type Leitura struct{}

func NovaLeitura(pool *pgxpool.Pool) *Leitura { return &Leitura{} }
```

- [ ] **Step 4: Rode e veja passar**

```bash
cd C:/tmp/emonitor-mcp/workers && go vet ./internal/mcp/ && go test ./internal/mcp/ -run 'TestPaginacao|TestPaginar|TestBuscarJanela|TestFerramentaLeitura|TestInventario' -v 2>&1 | grep -E '^(=== RUN|--- |ok|FAIL)'
```
Expected: todos `--- PASS`, `ok`.

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/mcp/toolkit.go workers/internal/mcp/toolkit_test.go workers/internal/mcp/ferramentas_test.go workers/internal/mcp/leitura.go workers/internal/mcp/servidor.go
git add workers/internal/mcp/ && git commit -m "feat(mcp): envelope de lista, falha interna e registro de tools de leitura"
```
(O `gofmt -l` não pode listar nenhum dos arquivos acima. Termine toda mensagem de commit com a linha `Co-Authored-By` do system-reminder.)

---

### Task 2: `Leitura`, montagem no módulo e fixtures de catálogo

**Files:**
- Modify: `workers/internal/mcp/leitura.go`
- Modify: `workers/internal/mcp/modulo.go:34-45`
- Create: `workers/internal/mcp/mcptest/catalogo.go`
- Create: `workers/internal/mcp/leitura_test.go`

- [ ] **Step 1: Teste que a Leitura monta e que as fixtures funcionam**

`workers/internal/mcp/leitura_test.go`:
```go
package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

func TestNovaLeitura_TodosOsRepositorios(t *testing.T) {
	L := NovaLeitura(nil)
	require.NotNil(t, L.Clientes)
	require.NotNil(t, L.PMMTarget)
	require.NotNil(t, L.Campanhas)
	require.NotNil(t, L.MateriaisCampanha)
	require.NotNil(t, L.Regras)
	require.NotNil(t, L.Ajustes)
	require.NotNil(t, L.Precos)
	require.NotNil(t, L.Materiais)
	require.NotNil(t, L.TiposMaterial)
	require.NotNil(t, L.Emissoras)
	require.NotNil(t, L.Veiculacoes)
	require.NotNil(t, L.Grade)
	require.NotNil(t, L.Notificacoes)
	require.NotNil(t, L.Usuarios)
	require.NotNil(t, L.BoasVindas)
}

func TestFixturesDeCatalogo(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Fixture")
	est := mcptest.Emissora(t, ctx, pool, "Rádio Fixture", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot fixture")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Campanha Fixture", "2026-10-01", "2026-10-31", est)
	mcptest.Vincular(t, ctx, pool, camp, mat, est)
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 2)
	mcptest.Preco(t, ctx, pool, camp, est, tipo, 100)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "10:00"))

	L := NovaLeitura(pool)
	rows, err := L.Grade.ListByCampaign(ctx, camp, mcptest.Dia("2026-10-10"), mcptest.Dia("2026-10-10"))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 2, rows[0].Expected)
	require.EqualValues(t, 1, rows[0].InSlot)
}
```

- [ ] **Step 2: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/mcp/ -run 'TestNovaLeitura|TestFixturesDeCatalogo' 2>&1 | head
```
Expected: FAIL de compilação (`L.Clientes undefined`, `mcptest.DBCatalogo undefined`).

- [ ] **Step 3: Implemente a Leitura e os helpers de nomes**

`workers/internal/mcp/leitura.go` (substitui o conteúdo):
```go
package mcp

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/catalog"
	"radiocheck/internal/users"
	"radiocheck/internal/welcome"
)

// Leitura reúne os repositórios que as tools de leitura chamam — os MESMOS
// que os handlers HTTP usam (cmd/api/main.go). Nenhuma regra de negócio mora
// aqui nem nas tools: elas traduzem argumentos, chamam o repositório e moldam
// a resposta no contrato.
type Leitura struct {
	Clientes          *catalog.Clients
	PMMTarget         *catalog.ClientStationPMM
	Campanhas         *catalog.Campaigns
	MateriaisCampanha *catalog.CampaignMaterials
	Regras            *catalog.DistributionRules
	Ajustes           *catalog.DistributionOverrides
	Precos            *catalog.Pricing
	Materiais         *catalog.Materials
	TiposMaterial     *catalog.MaterialTypes
	Emissoras         *catalog.Stations
	Veiculacoes       *catalog.Detections
	Grade             *catalog.DailySummaryRepo
	Notificacoes      *catalog.Notifications
	Usuarios          *users.Repo
	BoasVindas        *welcome.Repo
}

func NovaLeitura(pool *pgxpool.Pool) *Leitura {
	return &Leitura{
		Clientes:          catalog.NewClients(pool),
		PMMTarget:         catalog.NewClientStationPMM(pool),
		Campanhas:         catalog.NewCampaigns(pool),
		MateriaisCampanha: catalog.NewCampaignMaterials(pool),
		Regras:            catalog.NewDistributionRules(pool),
		Ajustes:           catalog.NewDistributionOverrides(pool),
		Precos:            catalog.NewPricing(pool),
		Materiais:         catalog.NewMaterials(pool),
		TiposMaterial:     catalog.NewMaterialTypes(pool),
		Emissoras:         catalog.NewStations(pool),
		Veiculacoes:       catalog.NewDetections(pool),
		Grade:             catalog.NewDailySummary(pool),
		Notificacoes:      catalog.NewNotifications(pool),
		Usuarios:          users.NewRepo(pool),
		BoasVindas:        welcome.NewRepo(pool),
	}
}

// nomesClientes: id → nome de todos os clientes (uma consulta; são dezenas).
func nomesClientes(ctx context.Context, L *Leitura) (map[uuid.UUID]string, error) {
	cs, err := L.Clientes.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]string, len(cs))
	for _, c := range cs {
		out[c.ID] = c.Name
	}
	return out, nil
}

// nomesEmissoras: id → nome, pelo mesmo Stations.List da tela (filtro ids,
// no máximo 500 por chamada — o teto do handler).
func nomesEmissoras(ctx context.Context, L *Leitura, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	for len(ids) > 0 {
		n := min(500, len(ids))
		res, err := L.Emissoras.List(ctx, catalog.ListInput{IDs: ids[:n]})
		if err != nil {
			return nil, err
		}
		for _, s := range res.Data {
			out[s.ID] = s.Name
		}
		ids = ids[n:]
	}
	return out, nil
}

// nomesTipos: id → nome de todos os tipos de material.
func nomesTipos(ctx context.Context, L *Leitura) (map[uuid.UUID]string, error) {
	ts, err := L.TiposMaterial.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]string, len(ts))
	for _, t := range ts {
		out[t.ID] = t.Name
	}
	return out, nil
}

// nomesMateriais: id → título dos materiais de um cliente.
func nomesMateriais(ctx context.Context, L *Leitura, cliente uuid.UUID) (map[uuid.UUID]string, error) {
	ms, err := L.Materiais.ListByClient(ctx, cliente, "")
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]string, len(ms))
	for _, m := range ms {
		out[m.ID] = m.Title
	}
	return out, nil
}

// todasCampanhas: id → campanha (todas, inclusive canceladas; são centenas).
func todasCampanhas(ctx context.Context, L *Leitura) (map[uuid.UUID]catalog.Campaign, error) {
	cs, err := L.Campanhas.ListFiltered(ctx, nil, nil)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]catalog.Campaign, len(cs))
	for _, c := range cs {
		out[c.ID] = c
	}
	return out, nil
}
```

Em `workers/internal/mcp/modulo.go`, dentro de `NovoModulo`, troque a linha do `mcp:` por:
```go
	cfg := ConfigServidor{Versao: d.Versao, Portal: d.Portal, Leitura: NovaLeitura(d.Pool), Log: d.Log}
	return &Modulo{
		mcp:    SoPost(a.Middleware(NovoHandlerMCP(cfg))),
		oauth:  o,
		tokens: &TokensHTTP{Tokens: st, DB: d.Pool, Log: d.Log},
	}
```

- [ ] **Step 4: Implemente as fixtures**

`workers/internal/mcp/mcptest/catalogo.go`:
```go
package mcptest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
)

// DBCatalogo é o DB() que também zera as emissoras: os testes de tool semeiam
// catálogo e o guard do dbtest recusa banco com mais de 50 emissoras.
// TRUNCATE de clients (no DB) já leva campanhas, materiais e veiculações.
func DBCatalogo(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx, pool := DB(t)
	_, err := pool.Exec(ctx, `TRUNCATE stations RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
	return ctx, pool
}

// Dia lê AAAA-MM-DD como data de calendário (meia-noite UTC, como o DATE do pgx).
func Dia(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

// Quando monta um instante em America/Sao_Paulo (dia AAAA-MM-DD, hora HH:MM).
func Quando(diaISO, hora string) time.Time {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		loc = time.FixedZone("BRT", -3*3600)
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", diaISO+" "+hora, loc)
	if err != nil {
		panic(err)
	}
	return t
}

func Cliente(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nome string) uuid.UUID {
	t.Helper()
	c, err := catalog.NewClients(pool).Create(ctx, catalog.CreateClientInput{Name: nome})
	require.NoError(t, err)
	return c.ID
}

// Emissora cria uma emissora FM com PMM (pmm 0 = sem PMM).
func Emissora(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nome string, pmm float64) uuid.UUID {
	t.Helper()
	s, err := catalog.NewStations(pool).Create(ctx, catalog.CreateStationInput{
		Name: nome, Band: "FM", StreamURL: "http://teste.mcp/" + uuid.NewString(),
	})
	require.NoError(t, err)
	if pmm > 0 {
		_, err = pool.Exec(ctx, `UPDATE stations SET pmm = $1 WHERE id = $2`, pmm, s.ID)
		require.NoError(t, err)
	}
	return s.ID
}

// TipoMaterial cria um tipo com nome único (a tabela não é truncada).
func TipoMaterial(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nome string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO material_types (id, name, color) VALUES ($1, $2, '#3b82f6')`,
		id, nome+" "+id.String()[:8])
	require.NoError(t, err)
	return id
}

func Material(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cliente, tipo uuid.UUID, titulo string) uuid.UUID {
	t.Helper()
	m, err := catalog.NewMaterials(pool).Create(ctx, catalog.CreateMaterialInput{
		ClientID: cliente, Title: titulo, TypeID: &tipo, DurationSeconds: 30,
		MasterStoragePath: "/tmp/mcp-" + titulo, MasterSHA256: "mcp-" + uuid.NewString(),
	})
	require.NoError(t, err)
	return m.ID
}

// Campanha cria a campanha (estado = default da coluna) com as emissoras-alvo.
func Campanha(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cliente uuid.UUID, nome, inicio, fim string, emissoras ...uuid.UUID) uuid.UUID {
	t.Helper()
	if emissoras == nil {
		emissoras = []uuid.UUID{} // target_stations é NOT NULL
	}
	c, err := catalog.NewCampaigns(pool).Create(ctx, catalog.CreateCampaignInput{
		ClientID: cliente, Name: nome, StartDate: Dia(inicio), EndDate: Dia(fim), TargetStations: emissoras,
	})
	require.NoError(t, err)
	return c.ID
}

// EstadoCampanha força o estado; "cancelada" grava cancelled_at = now().
func EstadoCampanha(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campanha uuid.UUID, estado string) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		UPDATE campaigns SET status = $2,
		       cancelled_at = CASE WHEN $2 = 'cancelada' THEN now() ELSE cancelled_at END
		WHERE id = $1`, campanha, estado)
	require.NoError(t, err)
}

func Vincular(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campanha, material uuid.UUID, emissoras ...uuid.UUID) {
	t.Helper()
	if emissoras == nil {
		emissoras = []uuid.UUID{}
	}
	require.NoError(t, catalog.NewCampaignMaterials(pool).Link(ctx, campanha, material, emissoras))
}

// Regra cria uma regra geral (todos os materiais do tipo) numa emissora.
// mascara: bit 0 = domingo … bit 6 = sábado (127 = todos os dias, 62 = seg a sex).
func Regra(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campanha, tipo, emissora uuid.UUID,
	inicio, fim string, mascara int16, faixaIni, faixaFim string, porDia int16) uuid.UUID {
	t.Helper()
	r, err := catalog.NewDistributionRules(pool).Create(ctx, catalog.CreateDistributionRuleInput{
		CampaignID: campanha, TypeID: tipo, StationIDs: []uuid.UUID{emissora}, MaterialIDs: []uuid.UUID{},
		Name: "Regra " + faixaIni, StartDate: Dia(inicio), EndDate: Dia(fim), WeekdayMask: mascara,
		TimeStart: faixaIni, TimeEnd: faixaFim, PlaysPerDay: porDia,
	})
	require.NoError(t, err)
	return r.ID
}

// Ajuste cria o ajuste do dia (distribution_override) de uma célula.
func Ajuste(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campanha, tipo, emissora uuid.UUID,
	data string, esperadas int16, faixaIni, faixaFim string) {
	t.Helper()
	require.NoError(t, catalog.NewDistributionOverrides(pool).Upsert(ctx, catalog.UpsertOverrideInput{
		CampaignID: campanha, TypeID: tipo, StationID: emissora, ForDate: Dia(data),
		PlaysExpected: esperadas, TimeStart: faixaIni, TimeEnd: faixaFim,
	}))
}

// Preco cria preço por inserção (unitário por tipo) numa emissora.
func Preco(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campanha, emissora, tipo uuid.UUID, unitario float64) {
	t.Helper()
	_, err := catalog.NewPricing(pool).Upsert(ctx, catalog.UpsertInput{
		CampaignID: campanha, StationID: emissora, Mode: catalog.PricingModePerInsertion,
		PerType: []catalog.TypePricing{{TypeID: tipo, UnitValue: unitario}},
	})
	require.NoError(t, err)
}

// Veiculacao insere uma tocada com categoria explícita + a projeção canônica
// (detection_campaigns), sem passar pelo categorizador — o mesmo atalho do
// insSeedDetection do catalog.
func Veiculacao(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campanha, material, emissora uuid.UUID,
	categoria string, quando time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used, category)
		VALUES ($1, $2, $3, $4, 0, 30000, 0.95, 100, 0.85, 0, 0, $5)
		RETURNING id`, emissora, material, campanha, quando, categoria).Scan(&id))
	_, err := pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, $5)`, id, quando, campanha, material, categoria)
	require.NoError(t, err)
	return id
}

// Projecao acrescenta uma atribuição extra (multi-atribuição) a uma tocada.
func Projecao(t *testing.T, ctx context.Context, pool *pgxpool.Pool, veiculacao uuid.UUID, quando time.Time,
	campanha, material uuid.UUID, categoria string) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, $5)`, veiculacao, quando, campanha, material, categoria)
	require.NoError(t, err)
}
```

- [ ] **Step 5: Rode e veja passar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/mcp/... 2>&1 | tail -5
```
Expected: `ok` em `internal/mcp`, `internal/mcp/oauth`, `internal/mcp/tokens` (com `TEST_DATABASE_URL` setada; sem ela, os testes de banco dão skip).

- [ ] **Step 6: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add workers/internal/mcp/ && git commit -m "feat(mcp): Leitura com os repositórios das telas e fixtures de catálogo"
```

---

### Task 3: Leituras do `catalog` — hoje em SP, CPM, ponte do hub e cancelamento

**Files:**
- Create: `workers/internal/catalog/hoje_sp.go`, `workers/internal/catalog/hoje_sp_test.go`
- Create: `workers/internal/catalog/cpm.go`, `workers/internal/catalog/cpm_test.go`
- Create: `workers/internal/catalog/mcp_leituras.go`, `workers/internal/catalog/mcp_leituras_test.go`
- Modify: `workers/internal/api/handlers/insights.go:190-196`

- [ ] **Step 1: Testes**

`workers/internal/catalog/hoje_sp_test.go`:
```go
package catalog

import (
	"testing"
	"time"
)

func TestTodaySaoPaulo_DiaDeSaoPauloEmMeiaNoiteUTC(t *testing.T) {
	got := TodaySaoPaulo()
	if got.Location() != time.UTC || got.Hour() != 0 || got.Minute() != 0 {
		t.Fatalf("quer meia-noite UTC, veio %v", got)
	}
	agora := time.Now().In(spLoc)
	if got.Year() != agora.Year() || got.Month() != agora.Month() || got.Day() != agora.Day() {
		t.Fatalf("dia %v ≠ hoje em SP %v", got, agora)
	}
}
```

`workers/internal/catalog/cpm_test.go`:
```go
package catalog

import "testing"

func TestCPMDaCampanha(t *testing.T) {
	fixo := 42.0
	cases := []struct {
		nome string
		f    CampaignFinancials
		want *float64
	}{
		// A bonificação está nos impactos, então TEM de estar no numerador
		// (docs/features/campaign-fixed-cpm.md). (200 + 100) / 3000 × 1000 = 100.
		{"dinâmico com bonificação", CampaignFinancials{TotalInvested: 200, TotalBonusValue: 100, TotalAudience: 3000}, cpmPtr(100.0)},
		{"fixo vence", CampaignFinancials{TotalInvested: 200, TotalAudience: 3000, FixedCPM: &fixo}, &fixo},
		{"sem impactos e sem fixo = não se aplica", CampaignFinancials{TotalInvested: 200}, nil},
	}
	for _, c := range cases {
		got := CPMDaCampanha(c.f)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Fatalf("%s: quer %v, veio %v", c.nome, cpmDeref(c.want), cpmDeref(got))
		}
	}
}

// cpmPtr/cpmDeref: o pacote já tem ptr(string) e deref(*string) noutros testes.
func cpmPtr(f float64) *float64 { return &f }

func cpmDeref(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}
```

`workers/internal/catalog/mcp_leituras_test.go`:
```go
package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestClients_HubIDs(t *testing.T) {
	ctx, pool := newTestDB(t)
	com := insSeedClient(t, ctx, pool, "Com ponte "+uuid.NewString()[:6])
	sem := insSeedClient(t, ctx, pool, "Sem ponte "+uuid.NewString()[:6])
	hubID := "hub-" + uuid.NewString()
	_, err := pool.Exec(ctx, `UPDATE clients SET hub_id = $1 WHERE id = $2`, hubID, com)
	require.NoError(t, err)

	got, err := NewClients(pool).HubIDs(ctx, []uuid.UUID{com, sem})
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]string{com: hubID}, got)

	vazio, err := NewClients(pool).HubIDs(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, vazio)
}

func TestCampaigns_ListByHubCodeECancelledAtFor(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Hub code "+uuid.NewString()[:6])
	a := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	b := insSeedCampaign(t, ctx, pool, cli, "2026-11-01", "2026-11-30")
	c := insSeedCampaign(t, ctx, pool, cli, "2026-12-01", "2026-12-31")
	_, err := pool.Exec(ctx, `UPDATE campaigns SET hub_code = 'EH-2346AB' WHERE id = ANY($1)`, []uuid.UUID{a, b})
	require.NoError(t, err)

	repo := NewCampaigns(pool)
	got, err := repo.ListByHubCode(ctx, "EH-2346AB")
	require.NoError(t, err)
	require.Len(t, got, 2, "o código não é único: dois PIs podem dividi-lo")
	require.Equal(t, b, got[0].ID, "mais nova primeiro")

	nenhuma, err := repo.ListByHubCode(ctx, "EH-ZZZZZZ")
	require.NoError(t, err)
	require.Empty(t, nenhuma)

	_, err = pool.Exec(ctx, `UPDATE campaigns SET status = 'cancelada', cancelled_at = now() WHERE id = $1`, c)
	require.NoError(t, err)
	canc, err := repo.CancelledAtFor(ctx, []uuid.UUID{a, c})
	require.NoError(t, err)
	require.Len(t, canc, 1)
	require.WithinDuration(t, time.Now(), canc[c], time.Minute)
}
```

- [ ] **Step 2: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/catalog/ -run 'TestTodaySaoPaulo|TestCPMDaCampanha|TestClients_HubIDs|TestCampaigns_ListByHubCode' 2>&1 | head
```
Expected: FAIL de compilação (`undefined: TodaySaoPaulo`, `CPMDaCampanha`, `HubIDs`, `ListByHubCode`, `CancelledAtFor`).

- [ ] **Step 3: Implemente**

`workers/internal/catalog/hoje_sp.go`:
```go
package catalog

import "time"

// TodaySaoPaulo devolve o dia corrente em America/Sao_Paulo como date-only
// (meia-noite UTC daquele dia de calendário). É o "hoje" do consolidado
// mensal em /insights e /campaigns/financials — e do MCP, que tem de dar o
// mesmo número que as telas.
func TodaySaoPaulo() time.Time {
	n := time.Now().In(spLoc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}
```

`workers/internal/catalog/cpm.go`:
```go
package catalog

// CPMDaCampanha é o CPM que /campaigns mostra para uma linha de
// CampaignFinancials: o fixed_cpm quando existe; senão o dinâmico,
// (investido + bonificação) ÷ impactos × 1000. A bonificação está nos
// impactos (base in_slot + bonus), então tem de estar no numerador — a mesma
// fórmula de insights.go (perCPM). Até 2026-10 esta conta só existia no
// navegador; o MCP precisa dela pronta (o modelo nunca recalcula).
// nil = sem impactos e sem CPM fixo: não se aplica, não é zero.
func CPMDaCampanha(f CampaignFinancials) *float64 {
	if f.FixedCPM != nil {
		v := *f.FixedCPM
		return &v
	}
	if f.TotalAudience <= 0 {
		return nil
	}
	v := (f.TotalInvested + f.TotalBonusValue) / f.TotalAudience * 1000
	return &v
}
```

`workers/internal/catalog/mcp_leituras.go`:
```go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Leituras que só o MCP usa hoje (spec 2026-10-27 §6 Fatia 1). São leituras
// de coluna, sem regra de negócio: a tela não as tem porque não mostra o dado
// (ou o mostra por outro caminho).

// HubIDs devolve o hub_id (ponte com a Central de Clientes, migration 0068)
// dos clientes pedidos. Cliente sem ponte não aparece no mapa.
func (c *Clients) HubIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.pool.Query(ctx,
		`SELECT id, hub_id FROM clients WHERE id = ANY($1) AND hub_id IS NOT NULL`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var hub string
		if err := rows.Scan(&id, &hub); err != nil {
			return nil, err
		}
		out[id] = hub
	}
	return out, rows.Err()
}

// ListByHubCode devolve as campanhas com este código do hub, da mais nova
// para a mais velha. O código NÃO é único (dois PIs podem dividi-lo — decisão
// 5 da spec do hub 2026-09-18), por isso é lista. code tem de vir canônico
// (hub.NormalizaCodigo), como é gravado.
func (c *Campaigns) ListByHubCode(ctx context.Context, code string) ([]Campaign, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id, client_id, name, start_date, end_date, status, target_stations,
		       fixed_cpm, hub_code, hub_notified_at, created_at, updated_at
		FROM campaigns WHERE hub_code = $1
		ORDER BY start_date DESC, id`, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Campaign
	for rows.Next() {
		var camp Campaign
		if err := rows.Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate, &camp.EndDate,
			&camp.Status, &camp.TargetStations, &camp.FixedCPM, &camp.HubCode, &camp.HubNotifiedAt,
			&camp.CreatedAt, &camp.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, camp)
	}
	return out, rows.Err()
}

// CancelledAtFor devolve cancelled_at (migration 0044) das campanhas
// canceladas entre ids. Campaign não carrega a coluna; quem precisa dizer
// "déficit congelado em" pergunta aqui.
func (c *Campaigns) CancelledAtFor(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]time.Time, error) {
	out := map[uuid.UUID]time.Time{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.pool.Query(ctx, `
		SELECT id, cancelled_at FROM campaigns
		WHERE id = ANY($1) AND status = 'cancelada' AND cancelled_at IS NOT NULL`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var em time.Time
		if err := rows.Scan(&id, &em); err != nil {
			return nil, err
		}
		out[id] = em
	}
	return out, rows.Err()
}
```

Em `workers/internal/api/handlers/insights.go`, troque o corpo de `todaySaoPaulo` (mantendo o comentário) por:
```go
func todaySaoPaulo() time.Time {
	return catalog.TodaySaoPaulo()
}
```
(Se `spLocation` ficar sem uso no pacote `handlers`, o `go vet` não reclama de variável de pacote; não a apague — outros arquivos podem usá-la. Confirme com `grep -rn spLocation workers/internal/api/handlers/`.)

- [ ] **Step 4: Rode e veja passar**

```bash
cd C:/tmp/emonitor-mcp/workers && go vet ./internal/catalog/ ./internal/api/handlers/ && go test -p 1 ./internal/catalog/ -run 'TestTodaySaoPaulo|TestCPMDaCampanha|TestClients_HubIDs|TestCampaigns_ListByHubCode|TestCampaigns_Financials|TestInsights_FinancialBase' -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```
Expected: todos PASS (os de `Financials`/`FinancialBase` já existiam e continuam verdes).

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add workers/internal/catalog/ workers/internal/api/handlers/insights.go && git commit -m "feat(catalog): leituras para o MCP — hoje em SP, CPM da campanha, ponte do hub e cancelamento"
```

---

### Task 4: Leituras do `catalog` — Anatel, cidades no alcance, atribuições e filtros de veiculação

**Files:**
- Modify: `workers/internal/catalog/mcp_leituras.go`, `workers/internal/catalog/mcp_leituras_test.go`
- Modify: `workers/internal/catalog/detections.go` (`ListPagedFilter` ~L1215 e `ListPaged` ~L1281)

- [ ] **Step 1: Testes**

Acrescente a `workers/internal/catalog/mcp_leituras_test.go`:
```go
func TestStations_AnatelECoverageCities(t *testing.T) {
	ctx, pool := newTestDB(t)
	st := insSeedStationNoProfile(t, ctx, pool, "Anatel "+uuid.NewString()[:6])
	repo := NewStations(pool)

	vazio, err := repo.Anatel(ctx, st)
	require.NoError(t, err)
	require.Nil(t, vazio.Class, "sem cruzamento: tudo null, nunca zero")
	require.Nil(t, vazio.CoverageKm)

	_, err = pool.Exec(ctx, `
		UPDATE stations SET anatel_class = 'A4', anatel_coverage_km = 24.0, anatel_reach_km = 36.0,
		       anatel_match_tier = 'exact', anatel_plan_city = 'Goiânia', anatel_plan_state = 'GO',
		       anatel_matched_at = now()
		WHERE id = $1`, st)
	require.NoError(t, err)
	a, err := repo.Anatel(ctx, st)
	require.NoError(t, err)
	require.Equal(t, "A4", *a.Class)
	require.InDelta(t, 24.0, *a.CoverageKm, 0.001)
	require.InDelta(t, 36.0, *a.ReachKm, 0.001)
	require.Equal(t, "exact", *a.MatchTier)

	_, err = repo.Anatel(ctx, uuid.New())
	require.ErrorIs(t, err, pgx.ErrNoRows)

	_, err = pool.Exec(ctx, `
		INSERT INTO station_coverage_cities (station_id, ibge_code, city, state, distance_km, is_home, latitude, longitude)
		VALUES ($1, 5208707, 'Goiânia', 'GO', 0, true, -16.68, -49.25),
		       ($1, 5201405, 'Aparecida de Goiânia', 'GO', 18.2, false, -16.82, -49.24)`, st)
	require.NoError(t, err)
	cs, err := repo.CoverageCities(ctx, st)
	require.NoError(t, err)
	require.Len(t, cs, 2)
	require.Equal(t, "Goiânia", cs[0].City, "da mais perto para a mais longe")
	require.True(t, cs[0].IsHome)
}

func TestDetections_Attributions(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Atrib "+uuid.NewString()[:6])
	st := insSeedStationNoProfile(t, ctx, pool, "Atrib "+uuid.NewString()[:6])
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "atrib")
	a := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	b := insSeedCampaign(t, ctx, pool, cli, "2026-10-02", "2026-10-31")
	insSeedDetection(t, ctx, pool, a, mat, st, "in_slot", "2026-10-10")
	var det uuid.UUID
	var quando time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id, detected_at FROM detections WHERE campaign_id = $1`, a).Scan(&det, &quando))
	_, err := pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'bonus')`, det, quando, b, mat)
	require.NoError(t, err)

	got, err := NewDetections(pool).Attributions(ctx, det)
	require.NoError(t, err)
	require.Len(t, got, 2)
	cats := map[uuid.UUID]string{got[0].CampaignID: got[0].Category, got[1].CampaignID: got[1].Category}
	require.Equal(t, map[uuid.UUID]string{a: "in_slot", b: "bonus"}, cats)
	require.Equal(t, "atrib", got[0].CommercialName)
}

func TestDetections_ListPaged_FiltrosDoMCP(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Filtros "+uuid.NewString()[:6])
	st1 := insSeedStationNoProfile(t, ctx, pool, "F1 "+uuid.NewString()[:6])
	st2 := insSeedStationNoProfile(t, ctx, pool, "F2 "+uuid.NewString()[:6])
	_, mat1 := insSeedTypeAndMaterial(t, ctx, pool, cli, "f-mat1")
	_, mat2 := insSeedTypeAndMaterial(t, ctx, pool, cli, "f-mat2")
	camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	insSeedDetection(t, ctx, pool, camp, mat1, st1, "in_slot", "2026-10-10")
	insSeedDetection(t, ctx, pool, camp, mat1, st2, "orphan", "2026-10-11")
	insSeedDetection(t, ctx, pool, camp, mat2, st1, "out_slot", "2026-10-12")
	repo := NewDetections(pool)
	base := ListPagedFilter{CampaignIDs: []uuid.UUID{camp}, Page: 1, PageSize: 50}

	f := base
	f.StationID = &st1
	r, err := repo.ListPaged(ctx, f)
	require.NoError(t, err)
	require.Equal(t, 2, r.Total)

	f = base
	f.MaterialID = &mat1
	r, err = repo.ListPaged(ctx, f)
	require.NoError(t, err)
	require.Equal(t, 2, r.Total)

	f = base
	f.Categories = []string{"bonus", "orphan"}
	r, err = repo.ListPaged(ctx, f)
	require.NoError(t, err)
	require.Equal(t, 1, r.Total)

	r, err = repo.ListPaged(ctx, base)
	require.NoError(t, err)
	require.Equal(t, 3, r.Total, "sem os filtros novos, nada muda para a tela")
}
```
(Acrescente `"github.com/jackc/pgx/v5"` aos imports do arquivo de teste.)

- [ ] **Step 2: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/catalog/ -run 'TestStations_Anatel|TestDetections_Attributions|TestDetections_ListPaged_FiltrosDoMCP' 2>&1 | head
```
Expected: FAIL de compilação (`repo.Anatel undefined`, `Attributions undefined`, `f.StationID undefined`).

- [ ] **Step 3: Implemente**

Acrescente a `workers/internal/catalog/mcp_leituras.go`:
```go
// StationAnatel é o cruzamento da emissora com os planos da Anatel
// (migration 0066, docs/features/anatel-station-class-coverage.md).
// Ponteiro nil = indeterminado, NUNCA zero (AM não tem raio por classe).
type StationAnatel struct {
	Class      *string
	CoverageKm *float64 // contorno protegido (normativo)
	ReachKm    *float64 // transbordo = contorno × 1,5 (gera as cidades no alcance)
	MatchTier  *string  // exact | geo | radcom
	PlanCity   *string
	PlanState  *string
	MatchedAt  *time.Time
}

// Anatel lê o cruzamento Anatel de uma emissora. pgx.ErrNoRows se a emissora
// não existe.
func (s *Stations) Anatel(ctx context.Context, id uuid.UUID) (*StationAnatel, error) {
	var a StationAnatel
	err := s.pool.QueryRow(ctx, `
		SELECT anatel_class, anatel_coverage_km::float8, anatel_reach_km::float8,
		       anatel_match_tier, anatel_plan_city, anatel_plan_state, anatel_matched_at
		FROM stations WHERE id = $1`, id).
		Scan(&a.Class, &a.CoverageKm, &a.ReachKm, &a.MatchTier, &a.PlanCity, &a.PlanState, &a.MatchedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// CoverageCities: as cidades no alcance (transbordo) de UMA emissora, da mais
// perto para a mais longe. A mesma tabela que o /live-map lê por campanha
// (LiveMap.Coverage).
func (s *Stations) CoverageCities(ctx context.Context, id uuid.UUID) ([]CoverageCity, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT station_id, ibge_code, city, state, distance_km::float8, is_home,
		       latitude::float8, longitude::float8
		FROM station_coverage_cities WHERE station_id = $1
		ORDER BY distance_km, city`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CoverageCity{}
	for rows.Next() {
		var c CoverageCity
		if err := rows.Scan(&c.StationID, &c.IBGECode, &c.City, &c.State, &c.DistanceKm,
			&c.IsHome, &c.Latitude, &c.Longitude); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DetectionAttribution é uma projeção da tocada (detection_campaigns, F-119):
// a tocada conta para cada campanha com a própria categoria.
type DetectionAttribution struct {
	CampaignID     uuid.UUID
	CampaignName   string
	CommercialID   uuid.UUID
	CommercialName string
	Category       string
}

// Attributions lista as projeções de uma tocada, por nome de campanha.
// Get devolve só a canônica (detections.campaign_id).
func (d *Detections) Attributions(ctx context.Context, id uuid.UUID) ([]DetectionAttribution, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT dc.campaign_id, COALESCE(cmp.name, ''), dc.commercial_id,
		       COALESCE(m.title, c.title, ''), dc.category
		FROM detection_campaigns dc
		LEFT JOIN campaigns cmp ON cmp.id = dc.campaign_id
		LEFT JOIN materials m   ON m.id = dc.commercial_id
		LEFT JOIN commercials c ON c.id = dc.commercial_id
		WHERE dc.detection_id = $1
		ORDER BY cmp.name, dc.campaign_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DetectionAttribution{}
	for rows.Next() {
		var a DetectionAttribution
		if err := rows.Scan(&a.CampaignID, &a.CampaignName, &a.CommercialID, &a.CommercialName, &a.Category); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
```

Em `workers/internal/catalog/detections.go`, acrescente ao fim de `ListPagedFilter` (antes do `}`):
```go
	// Filtros do MCP (listar_veiculacoes, spec 2026-10-27 §6). nil/vazio =
	// sem filtro; a tela não os manda, então nada muda para ela.
	StationID  *uuid.UUID
	MaterialID *uuid.UUID
	Categories []string
```
No SQL de `ListPaged`, logo depois da linha `AND ($7::uuid[] IS NULL OR cmp.client_id = ANY($7))`, acrescente:
```sql
		  AND ($8::uuid IS NULL OR d.station_id = $8)
		  AND ($9::uuid IS NULL OR d.commercial_id = $9)
		  AND ($10::text[] IS NULL OR d.category = ANY($10))
```
E troque a chamada `d.pool.Query(ctx, sql, …)` por:
```go
	var cats any // nil → NULL: sem filtro de categoria
	if len(f.Categories) > 0 {
		cats = f.Categories
	}
	rows, err := d.pool.Query(ctx, sql,
		f.CampaignIDs, f.StartDate, f.EndDate, qTokens, f.PageSize, offset, f.ClientIDs,
		f.StationID, f.MaterialID, cats)
```

- [ ] **Step 4: Rode e veja passar (inclusive os testes antigos de ListPaged)**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/catalog/ -run 'TestStations_Anatel|TestDetections_Attributions|TestDetections_ListPaged|TestDetections_AggregateByMaterial' -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```
Expected: todos PASS.

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add workers/internal/catalog/ && git commit -m "feat(catalog): leituras para o MCP — Anatel, cidades no alcance, atribuições e filtros de veiculação"
```

---

### Task 5: Extrair o SQL dos handlers de integração e exportar `RuleCoversDay`

**Files:**
- Create: `workers/internal/catalog/clients_integracoes.go`, `workers/internal/catalog/clients_integracoes_test.go`
- Modify: `workers/internal/api/handlers/webhooks.go:160-219` (`ListDeliveries`)
- Modify: `workers/internal/api/handlers/apikeys.go:13-47` (`List`)
- Modify: `workers/internal/categorizer/categorizer.go` (perto da L338), `workers/internal/categorizer/categorizer_test.go`

- [ ] **Step 1: Testes**

`workers/internal/catalog/clients_integracoes_test.go`:
```go
package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestClients_ListWebhookDeliveries(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Webhook "+uuid.NewString()[:6])
	for i, st := range []string{"delivered", "failed", "delivered"} {
		_, err := pool.Exec(ctx, `
			INSERT INTO webhook_deliveries (client_id, event_type, payload, status, attempt_count, next_attempt_at, created_at)
			VALUES ($1, 'detection.confirmed', '{}'::jsonb, $2, 1, now(), now() - make_interval(mins => $3))`,
			cli, st, i)
		require.NoError(t, err)
	}
	repo := NewClients(pool)

	todas, total, err := repo.ListWebhookDeliveries(ctx, cli, "", 2, 0)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, todas, 2)
	require.True(t, todas[0].CreatedAt.After(todas[1].CreatedAt), "mais nova primeiro")

	resto, _, err := repo.ListWebhookDeliveries(ctx, cli, "", 2, 2)
	require.NoError(t, err)
	require.Len(t, resto, 1)

	falhas, total, err := repo.ListWebhookDeliveries(ctx, cli, "failed", 50, 0)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, "failed", falhas[0].Status)
}

func TestClients_ListAPIKeys(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Chaves "+uuid.NewString()[:6])
	// key_hash é CHAR(64) UNIQUE (migration 0007): hashes de 64 hex distintos.
	ativa := "abcdef01" + strings.Repeat("a", 56)
	revogada := "ffffffff" + strings.Repeat("b", 56)
	_, err := pool.Exec(ctx, `INSERT INTO api_keys (client_id, key_hash) VALUES ($1, $2), ($1, $3)`, cli, ativa, revogada)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE api_keys SET revoked_at = now() WHERE key_hash LIKE 'ffff%'`)
	require.NoError(t, err)

	got, err := NewClients(pool).ListAPIKeys(ctx, cli)
	require.NoError(t, err)
	require.Len(t, got, 1, "revogada fica de fora")
	require.Equal(t, "abcdef01...", got[0].KeyMasked, "só os 8 primeiros hex do HASH")
	require.Nil(t, got[0].LastUsedAt)
}
```
(Imports do arquivo: `strings`, `testing`, `github.com/google/uuid`, `github.com/stretchr/testify/require`. As colunas `NOT NULL` sem default de `webhook_deliveries` são `client_id`, `event_type` e `payload` — todas no INSERT.)

Acrescente a `workers/internal/categorizer/categorizer_test.go` (ou a `settle_test.go`, o que estiver em `package categorizer`):
```go
func TestRuleCoversDay_Exportada(t *testing.T) {
	r := Rule{
		StartDate:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, // seg a sex
	}
	if !RuleCoversDay(r, time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)) { // terça
		t.Fatal("terça dentro da vigência tem de valer")
	}
	if RuleCoversDay(r, time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)) { // domingo
		t.Fatal("domingo fora da máscara não vale")
	}
	if RuleCoversDay(r, time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) { // segunda depois do fim
		t.Fatal("depois do fim não vale")
	}
}
```

- [ ] **Step 2: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/catalog/ -run 'TestClients_ListWebhookDeliveries|TestClients_ListAPIKeys' 2>&1 | head -5; go test ./internal/categorizer/ -run TestRuleCoversDay 2>&1 | head -5
```
Expected: FAIL de compilação nos dois.

- [ ] **Step 3: Implemente o `catalog` e o categorizer**

`workers/internal/catalog/clients_integracoes.go`:
```go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// WebhookDelivery é uma entrega de webhook (migration 0012) SEM payload nem
// response_body: o payload traz dado de detecção e o response_body é conteúdo
// do sistema do cliente — nenhum dos dois sai pela API.
type WebhookDelivery struct {
	ID            uuid.UUID  `json:"id"`
	EventType     string     `json:"event_type"`
	Status        string     `json:"status"`
	AttemptCount  int        `json:"attempt_count"`
	ResponseCode  *int       `json:"response_code,omitempty"`
	LastError     *string    `json:"last_error,omitempty"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ListWebhookDeliveries: entregas do cliente, da mais nova para a mais velha.
// status "" = todas. total = quantas existem com o filtro (a tela ignora; o MCP
// pagina com ele). Página além do fim devolve total 0 (COUNT(*) OVER ()).
func (c *Clients) ListWebhookDeliveries(ctx context.Context, clientID uuid.UUID, status string, limit, offset int) ([]WebhookDelivery, int, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id, event_type, status, attempt_count, response_code, last_error,
		       next_attempt_at, delivered_at, created_at, updated_at, COUNT(*) OVER ()
		FROM webhook_deliveries
		WHERE client_id = $1
		  AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4`, clientID, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]WebhookDelivery, 0)
	total := 0
	for rows.Next() {
		var it WebhookDelivery
		if err := rows.Scan(&it.ID, &it.EventType, &it.Status, &it.AttemptCount,
			&it.ResponseCode, &it.LastError, &it.NextAttemptAt, &it.DeliveredAt,
			&it.CreatedAt, &it.UpdatedAt, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// APIKeyInfo é o metadado de uma chave de API ativa. KeyMasked são os 8
// primeiros hex do SHA-256 da chave (nunca a chave, que só existe na resposta
// da criação).
type APIKeyInfo struct {
	ID         uuid.UUID
	KeyMasked  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// ListAPIKeys: chaves não revogadas do cliente, da mais nova para a mais velha.
func (c *Clients) ListAPIKeys(ctx context.Context, clientID uuid.UUID) ([]APIKeyInfo, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT id, LEFT(key_hash, 8) || '...', created_at, last_used_at
		FROM api_keys WHERE client_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKeyInfo{}
	for rows.Next() {
		var k APIKeyInfo
		if err := rows.Scan(&k.ID, &k.KeyMasked, &k.CreatedAt, &k.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
```

Em `workers/internal/categorizer/categorizer.go`, logo acima de `func ruleCoversDay`:
```go
// RuleCoversDay diz se a regra vale no dia (vigência + dia da semana), com o
// dia lido em America/Sao_Paulo. É a mesma decisão que Settle usa para somar
// a meta do dia — exportada para o MCP (plano_do_dia) não reimplementá-la.
func RuleCoversDay(r Rule, day time.Time) bool { return ruleCoversDay(r, dateOnlySP(day)) }
```

- [ ] **Step 4: Faça os handlers chamarem o `catalog` (mesma resposta)**

Em `workers/internal/api/handlers/webhooks.go`, no `ListDeliveries`, troque tudo da linha `q := \`` até o `writeJSON(...)` final por:
```go
	out, _, err := h.Clients.ListWebhookDeliveries(r.Context(), id, status, limit, 0)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
```
(`WebhookDelivery` tem as mesmas tags JSON da struct `item` local, que some. Se `time` ficar sem uso no arquivo, o compilador avisa: remova o import só nesse caso.)

Em `workers/internal/api/handlers/apikeys.go`, troque o corpo de `List` por:
```go
func (h *APIKeysHandler) List(w http.ResponseWriter, r *http.Request) {
	// Id malformado sempre deu 500 (o pgx recusava o texto como uuid); mantém.
	clientID, err := uuid.Parse(chi.URLParam(r, "clientID"))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	infos, err := catalog.NewClients(h.db).ListAPIKeys(r.Context(), clientID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	keys := make([]map[string]interface{}, 0, len(infos))
	for _, k := range infos {
		keys = append(keys, map[string]interface{}{
			"id": k.ID.String(), "key_masked": k.KeyMasked,
			"created_at": k.CreatedAt, "last_used_at": k.LastUsedAt,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(keys)
}
```
(Acrescente `"radiocheck/internal/catalog"` aos imports de `apikeys.go`. `last_used_at` nil serializa `null`, como antes.)

- [ ] **Step 5: Rode tudo que toca esses arquivos**

```bash
cd C:/tmp/emonitor-mcp/workers && go vet ./internal/catalog/ ./internal/api/... ./internal/categorizer/ && go test ./internal/categorizer/ && go test -p 1 ./internal/catalog/ -run 'TestClients_' && go test -p 1 ./internal/api/handlers/ -run 'TestWebhooks|TestMaskSecret|TestToDTO|TestNewAPIKeysHandler' 2>&1 | tail -5
```
Expected: `ok` nos quatro.

- [ ] **Step 6: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add workers/internal/catalog/ workers/internal/api/handlers/webhooks.go workers/internal/api/handlers/apikeys.go workers/internal/categorizer/ && git commit -m "refactor(catalog): entregas de webhook e chaves de API saem do handler; RuleCoversDay exportada"
```

---

### Task 6: Clientes — `listar_clientes`, `obter_cliente`, `pmm_target_cliente`

**Files:**
- Create: `workers/internal/mcp/tools_clientes.go`, `workers/internal/mcp/tools_clientes_test.go`
- Create: `workers/internal/mcp/tools_usuarios.go` (só os helpers `papelDe`/`situacaoDe`; a Task 13 põe as tools)
- Modify: `workers/internal/mcp/servidor.go` (`ToolsEsperadas`, `novoServidor`)

**Fonte (regra de ouro):** `GET /clients` modo paginado → `Clients.ListPaged` (`handlers/clients.go:36`); `GET /clients/{id}/target-pmm` → `ClientStationPMM.ListForClient` (`handlers/client_target_pmm.go:25`). Não há `GET /clients/{id}`: `obter_cliente` usa `Clients.Get` + `CountDependents` (o que o `Delete` usa) + `users.Repo.List{ClientID}` (o filtro de cliente de `/admin/users`, que casa vínculo secundário da carteira).

- [ ] **Step 1: Testes**

`workers/internal/mcp/tools_clientes_test.go`:
```go
package mcp

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func TestListarClientes_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	a := mcptest.Cliente(t, ctx, pool, "Alfa")
	mcptest.Cliente(t, ctx, pool, "Beta")
	g := mcptest.Cliente(t, ctx, pool, "Gama")
	_, err := pool.Exec(ctx, `UPDATE clients SET is_active = false WHERE id = $1`, g)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE clients SET hub_id = 'hub-alfa' WHERE id = $1`, a)
	require.NoError(t, err)

	h := &handlers.ClientsHandler{Repo: catalog.NewClients(pool)}
	for _, inativos := range []bool{false, true} {
		alvo := "/clients?page=1&page_size=25"
		if inativos {
			alvo += "&include_inactive=1"
		}
		var tela struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			Total int `json:"total"`
		}
		require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, alvo, nil, admin), &tela))
		out := chamarTool(t, cfgTeste(pool), "listar_clientes", map[string]any{"incluir_inativos": inativos})
		var idsTela []string
		for _, c := range tela.Data {
			idsTela = append(idsTela, c.ID)
		}
		require.Equal(t, idsTela, ids(t, itens(t, out), "id"), "inativos=%v", inativos)
		require.EqualValues(t, tela.Total, pag(t, out)["total"])
	}

	out := chamarTool(t, cfgTeste(pool), "listar_clientes", map[string]any{"busca": "alfa"})
	require.Equal(t, "hub-alfa", itens(t, out)[0]["hub_id"])
}

func TestListarClientes_OffsetForaDoMultiplo(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	for _, n := range []string{"Alfa", "Beta", "Delta", "Epsilon", "Gama"} {
		mcptest.Cliente(t, ctx, pool, n)
	}
	out := chamarTool(t, cfgTeste(pool), "listar_clientes", map[string]any{"limit": 2, "offset": 3})
	var nomes []string
	for _, c := range itens(t, out) {
		nomes = append(nomes, c["nome"].(string))
	}
	require.Equal(t, []string{"Epsilon", "Gama"}, nomes)
	require.EqualValues(t, 5, pag(t, out)["total"])
	require.Equal(t, false, pag(t, out)["has_more"])
}

func TestObterCliente(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Completo")
	_, err := pool.Exec(ctx, `UPDATE clients SET hub_id = 'hub-x' WHERE id = $1`, cli)
	require.NoError(t, err)
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 0)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	_, err = pool.Exec(ctx, `
		INSERT INTO users (email, password_hash, role, name, is_active, client_id)
		VALUES ('viewer@mcp.teste', 'x', 'viewer', 'Pessoa do Cliente', true, $1)`, cli)
	require.NoError(t, err)

	out := chamarTool(t, cfgTeste(pool), "obter_cliente", map[string]any{"cliente_id": cli.String()})
	require.Equal(t, "hub-x", out["cliente"].(map[string]any)["hub_id"])
	require.Equal(t, map[string]any{"campanhas": 1.0, "materiais": 1.0, "usuarios": 1.0}, out["vinculos"])
	u := out["usuarios"].([]any)[0].(map[string]any)
	require.Equal(t, "cliente", u["papel"], "viewer aparece como cliente, o vocabulário da tela")
	require.Equal(t, "ativo", u["situacao"])

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_cliente", map[string]any{"cliente_id": "nao-e-uuid"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_cliente",
		map[string]any{"cliente_id": "00000000-0000-0000-0000-000000000001"}), "[nao_encontrado]")
}

func TestPMMTargetCliente_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente PMM")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 2000)
	mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", e1, e2)
	_, err := pool.Exec(ctx, `INSERT INTO client_station_pmm (client_id, station_id, pmm_target) VALUES ($1, $2, 500)`, cli, e1)
	require.NoError(t, err)

	h := &handlers.ClientTargetPmmHandler{Repo: catalog.NewClientStationPMM(pool)}
	var tela struct {
		Data []struct {
			StationID string `json:"station_id"`
			PMMTarget *int   `json:"pmm_target"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/clients/x/target-pmm",
		map[string]string{"clientID": cli.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "pmm_target_cliente", map[string]any{"cliente_id": cli.String()})
	linhas := itens(t, out)
	require.Len(t, linhas, len(tela.Data))
	for i, l := range linhas {
		require.Equal(t, tela.Data[i].StationID, l["emissora"].(map[string]any)["id"])
		if tela.Data[i].PMMTarget == nil {
			require.Nil(t, l["pmm_target"], "sem linha = null, nunca zero")
		} else {
			require.EqualValues(t, *tela.Data[i].PMMTarget, l["pmm_target"])
		}
	}
	require.NotEmpty(t, out["ressalvas"])
}
```

- [ ] **Step 2: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/mcp/ -run 'TestListarClientes|TestObterCliente|TestPMMTarget' 2>&1 | head
```
Expected: FAIL — a tool `listar_clientes` não existe (o SDK devolve erro de tool desconhecida).

- [ ] **Step 3: Implemente**

`workers/internal/mcp/tools_usuarios.go`:
```go
package mcp

import "radiocheck/internal/users"

// papelDe traduz o papel do banco para o vocabulário da tela: viewer é "cliente".
func papelDe(role string) string {
	if role == "viewer" {
		return "cliente"
	}
	return role
}

// situacaoDe: apagado (soft delete) > inativo > ativo.
func situacaoDe(u users.User) string {
	switch {
	case u.DeletedAt != nil:
		return "apagado"
	case !u.IsActive:
		return "inativo"
	default:
		return "ativo"
	}
}
```

`workers/internal/mcp/tools_clientes.go`:
```go
package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
	"radiocheck/internal/users"
)

type clienteDTO struct {
	ID           string  `json:"id"`
	Nome         string  `json:"nome"`
	Ativo        bool    `json:"ativo"`
	HubID        *string `json:"hub_id"`
	CNPJ         *string `json:"cnpj"`
	Cidade       *string `json:"cidade"`
	UF           *string `json:"uf"`
	ContatoNome  *string `json:"contato_nome"`
	ContatoEmail *string `json:"contato_email"`
	Telefone     *string `json:"telefone"`
	RotuloTarget *string `json:"rotulo_target"`
	CriadoEm     string  `json:"criado_em"`
}

func clienteDe(c catalog.Client, hubIDs map[uuid.UUID]string) clienteDTO {
	d := clienteDTO{
		ID: c.ID.String(), Nome: c.Name, Ativo: c.IsActive, CNPJ: c.CNPJ, Cidade: c.City, UF: c.State,
		ContatoNome: c.ContactName, ContatoEmail: c.ContactEmail, Telefone: c.Phone,
		RotuloTarget: c.TargetLabel, CriadoEm: instante(c.CreatedAt),
	}
	if h, ok := hubIDs[c.ID]; ok {
		d.HubID = &h
	}
	return d
}

type entradaListarClientes struct {
	Busca           string `json:"busca,omitempty" jsonschema:"Texto livre: nome, cidade, UF, CNPJ ou contato. Ignora acento e caixa."`
	IncluirInativos bool   `json:"incluir_inativos,omitempty" jsonschema:"true para trazer também os clientes desativados (padrão: só ativos)."`
	Paginacao
}

type entradaCliente struct {
	ClienteID string `json:"cliente_id" jsonschema:"Id (uuid) do cliente no E-monitor, vindo de listar_clientes. Nunca invente: pergunte ao usuário."`
}

type entradaClientePaginada struct {
	ClienteID string `json:"cliente_id" jsonschema:"Id (uuid) do cliente no E-monitor, vindo de listar_clientes. Nunca invente: pergunte ao usuário."`
	Paginacao
}

type vinculosCliente struct {
	Campanhas int `json:"campanhas"`
	Materiais int `json:"materiais"`
	Usuarios  int `json:"usuarios"`
}

type pessoaDaCarteira struct {
	ID       string `json:"id"`
	Nome     string `json:"nome"`
	Email    string `json:"email"`
	Papel    string `json:"papel"`
	Situacao string `json:"situacao"`
}

type saidaObterCliente struct {
	Cliente   clienteDTO         `json:"cliente"`
	Vinculos  vinculosCliente    `json:"vinculos"`
	Usuarios  []pessoaDaCarteira `json:"usuarios"`
	Ressalvas []string           `json:"ressalvas,omitempty"`
}

type linhaPMMTarget struct {
	Emissora      ref      `json:"emissora"`
	Banda         *string  `json:"banda"`
	FrequenciaMHz *float64 `json:"frequencia_mhz"`
	Cidade        *string  `json:"cidade"`
	UF            *string  `json:"uf"`
	PMM           *float64 `json:"pmm"`
	PMMTarget     *int     `json:"pmm_target"`
}

// tetoCarteira: quantas pessoas obter_cliente lista (o resto, listar_usuarios).
const tetoCarteira = 100

const ressalvaPMMTarget = "pmm_target null = a emissora não tem linha de PMM no target deste cliente: ela fica FORA do total de impactos no target e do contador de emissoras com target (não é zero). pmm_target 0 conta e soma nada."

// lerCliente valida o id e confirma que o cliente existe; rec != nil é a recusa pronta.
func lerCliente(ctx context.Context, L *Leitura, s string) (*catalog.Client, *sdk.CallToolResult, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		rec, _ := recusaEntrada("cliente_id")
		return nil, rec, nil
	}
	c, err := L.Clientes.Get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		rec, _ := naoEncontrado("cliente")
		return nil, rec, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return c, nil, nil
}

func registrarClientes(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "listar_clientes",
		"Lista os clientes (anunciantes) do E-monitor em ordem de nome: id, nome, se está ativo, CNPJ, cidade/UF, contato e hub_id "+
			"(o id do mesmo cliente no hub). Use para achar o cliente_id que as outras tools pedem. Por padrão só os ativos; "+
			"incluir_inativos traz os desativados. Lista paginada: siga page.has_more.",
		func(ctx context.Context, L *Leitura, in entradaListarClientes) (*sdk.CallToolResult, error) {
			pg, err := buscarJanela(in.Paginacao, func(page, size int) ([]catalog.Client, int, error) {
				return L.Clientes.ListPaged(ctx, in.Busca, page, size, in.IncluirInativos)
			})
			if err != nil {
				return nil, err
			}
			ids := make([]uuid.UUID, len(pg.Items))
			for i, c := range pg.Items {
				ids[i] = c.ID
			}
			hub, err := L.Clientes.HubIDs(ctx, ids)
			if err != nil {
				return nil, err
			}
			return ok(mapear(pg, func(c catalog.Client) clienteDTO { return clienteDe(c, hub) }))
		})

	ferramentaLeitura(s, cfg, "obter_cliente",
		"Devolve o cadastro de um cliente (com hub_id, a ponte para o hub), quantas campanhas, materiais e usuários estão "+
			"vinculados a ele e as pessoas da carteira (papel e situação). Um usuário de agência pode ter vários clientes: ele "+
			"aparece em cada um. Nunca invente o cliente_id: pegue de listar_clientes ou pergunte.",
		func(ctx context.Context, L *Leitura, in entradaCliente) (*sdk.CallToolResult, error) {
			c, rec, err := lerCliente(ctx, L, in.ClienteID)
			if rec != nil || err != nil {
				return rec, err
			}
			hub, err := L.Clientes.HubIDs(ctx, []uuid.UUID{c.ID})
			if err != nil {
				return nil, err
			}
			dep, err := L.Clientes.CountDependents(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			pessoas, total, err := L.Usuarios.List(ctx, users.ListInput{ClientID: &c.ID, Status: "all", PageSize: tetoCarteira})
			if err != nil {
				return nil, err
			}
			out := saidaObterCliente{
				Cliente:  clienteDe(*c, hub),
				Vinculos: vinculosCliente{Campanhas: dep.Campaigns, Materiais: dep.Materials, Usuarios: dep.Users},
				Usuarios: make([]pessoaDaCarteira, 0, len(pessoas)),
			}
			for _, u := range pessoas {
				out.Usuarios = append(out.Usuarios, pessoaDaCarteira{
					ID: u.ID.String(), Nome: u.Name, Email: u.Email, Papel: papelDe(u.Role), Situacao: situacaoDe(u),
				})
			}
			if total > len(pessoas) {
				out.Ressalvas = append(out.Ressalvas, fmt.Sprintf(
					"mostrando %d de %d usuários; listar_usuarios com cliente_id traz todos", len(pessoas), total))
			}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "pmm_target_cliente",
		"Lista as emissoras das campanhas de um cliente com o PMM geral e o PMM no target do cliente (pmm_target), base dos "+
			"impactos no target. A armadilha: pmm_target null NÃO é zero — a emissora fica fora do total. Nunca invente o "+
			"cliente_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaClientePaginada) (*sdk.CallToolResult, error) {
			c, rec, err := lerCliente(ctx, L, in.ClienteID)
			if rec != nil || err != nil {
				return rec, err
			}
			rows, err := L.PMMTarget.ListForClient(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			linhas := make([]linhaPMMTarget, len(rows))
			for i, r := range rows {
				linhas[i] = linhaPMMTarget{
					Emissora: refDe(r.StationID, r.Name), Banda: r.Band, FrequenciaMHz: r.FrequencyMHz,
					Cidade: r.City, UF: r.State, PMM: r.PMM, PMMTarget: r.PMMTarget,
				}
			}
			l := paginar(linhas, in.Paginacao)
			l.Ressalvas = []string{ressalvaPMMTarget}
			return ok(l)
		})
}
```

Em `workers/internal/mcp/servidor.go`: acrescente `"listar_clientes", "obter_cliente", "pmm_target_cliente",` a `ToolsEsperadas` (depois de `"quem_sou_eu"`) e `registrarClientes(s, cfg)` em `novoServidor` (depois de `registrarSistema(s, p)`).

- [ ] **Step 4: Rode e veja passar**

```bash
cd C:/tmp/emonitor-mcp/workers && go vet ./internal/mcp/ && go test -p 1 ./internal/mcp/ -run 'TestListarClientes|TestObterCliente|TestPMMTarget|TestInventario|TestServidor' -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```
Expected: todos PASS (`TestInventario` e `TestServidor_ListaNaSpec20260728` conferem a nova `ToolsEsperadas`).

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add workers/internal/mcp/ && git commit -m "feat(mcp): tools de clientes — listar, obter (com carteira e ponte) e PMM no target"
```

---

### Task 7: Campanhas — `listar_campanhas`, `obter_campanha`, `financeiro_campanhas`

**Files:**
- Create: `workers/internal/mcp/tools_campanhas.go`, `workers/internal/mcp/tools_campanhas_test.go`
- Modify: `workers/internal/mcp/servidor.go`

**Fonte:** `GET /campaigns` → `Campaigns.ListPaged`/`ListFiltered` (`handlers/campaigns.go:56`, mesma ordenação nos dois); `GET /campaigns/{id}` → `Campaigns.Get`; código do hub → `Campaigns.ListByHubCode` (Task 3) depois de `hub.NormalizaCodigo`; `GET /campaigns/financials` → `Campaigns.FinancialsByCampaign(…, catalog.TodaySaoPaulo())` + `catalog.CPMDaCampanha` (Task 3).

- [ ] **Step 1: Testes**

`workers/internal/mcp/tools_campanhas_test.go`:
```go
package mcp

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func TestListarCampanhas_CanceladaForaPorPadraoEParidade(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente A")
	outro := mcptest.Cliente(t, ctx, pool, "Cliente B")
	ativa := mcptest.Campanha(t, ctx, pool, cli, "Ativa", "2026-10-01", "2026-10-31")
	mcptest.EstadoCampanha(t, ctx, pool, ativa, "ativa")
	prog := mcptest.Campanha(t, ctx, pool, cli, "Programada", "2026-11-01", "2026-11-30")
	mcptest.EstadoCampanha(t, ctx, pool, prog, "programada")
	canc := mcptest.Campanha(t, ctx, pool, cli, "Cancelada", "2026-09-01", "2026-09-30")
	mcptest.EstadoCampanha(t, ctx, pool, canc, "cancelada")
	conc := mcptest.Campanha(t, ctx, pool, outro, "Concluída", "2026-08-01", "2026-08-31")
	mcptest.EstadoCampanha(t, ctx, pool, conc, "concluida")

	h := &handlers.CampaignsHandler{Repo: catalog.NewCampaigns(pool)}
	var tela struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/campaigns?status=programada,ativa,concluida", nil, admin), &tela))
	var idsTela []string
	for _, c := range tela.Data {
		idsTela = append(idsTela, c.ID)
	}
	out := chamarTool(t, cfgTeste(pool), "listar_campanhas", map[string]any{})
	require.Equal(t, idsTela, ids(t, itens(t, out), "id"), "mesmo conjunto e mesma ordem da tela, sem a cancelada")
	require.Nil(t, out["ressalvas"])

	so := chamarTool(t, cfgTeste(pool), "listar_campanhas", map[string]any{"estados": []string{"cancelada"}})
	require.Equal(t, []string{canc.String()}, ids(t, itens(t, so), "id"))
	require.NotNil(t, itens(t, so)[0]["cancelada_em"])
	require.NotEmpty(t, so["ressalvas"])

	doCliente := chamarTool(t, cfgTeste(pool), "listar_campanhas", map[string]any{"cliente_id": outro.String()})
	require.Equal(t, []string{conc.String()}, ids(t, itens(t, doCliente), "id"))
	require.Equal(t, "Cliente B", itens(t, doCliente)[0]["cliente"].(map[string]any)["nome"])

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_campanhas", map[string]any{"competencia": "10/2026"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_campanhas", map[string]any{"estados": []string{"rodando"}}), "[entrada_invalida]")
}

func TestObterCampanha_PorIdEPorCodigo(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 0)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Com código", "2026-10-01", "2026-10-31", e1)
	mcptest.Vincular(t, ctx, pool, camp, mat, e1)
	_, err := pool.Exec(ctx, `UPDATE campaigns SET hub_code = 'EH-2346AB' WHERE id = $1`, camp)
	require.NoError(t, err)

	out := chamarTool(t, cfgTeste(pool), "obter_campanha", map[string]any{"campanha_id": camp.String()})
	c := out["campanha"].(map[string]any)
	require.Equal(t, "Com código", c["nome"])
	require.EqualValues(t, 1, c["materiais"])
	require.Equal(t, "Rádio Um", out["emissoras"].([]any)[0].(map[string]any)["nome"])
	require.Equal(t, "https://portal.teste/campaigns/"+camp.String()+"/edit", out["links"].(map[string]any)["tela"])

	porCodigo := chamarTool(t, cfgTeste(pool), "obter_campanha", map[string]any{"codigo": "eh-2346ab"})
	require.Equal(t, camp.String(), porCodigo["campanha"].(map[string]any)["id"], "o código é normalizado como o hub faz")

	outra := mcptest.Campanha(t, ctx, pool, cli, "Mesmo código", "2026-11-01", "2026-11-30")
	_, err = pool.Exec(ctx, `UPDATE campaigns SET hub_code = 'EH-2346AB' WHERE id = $1`, outra)
	require.NoError(t, err)
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_campanha", map[string]any{"codigo": "EH-2346AB"}), "[ambiguo]")

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_campanha", map[string]any{}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_campanha",
		map[string]any{"campanha_id": camp.String(), "codigo": "EH-2346AB"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_campanha", map[string]any{"codigo": "XYZ"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_campanha",
		map[string]any{"campanha_id": uuid.NewString()}), "[nao_encontrado]")
}

func TestFinanceiroCampanhas_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Paga", "2026-10-01", "2026-10-31", est)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 2)
	mcptest.Preco(t, ctx, pool, camp, est, tipo, 100)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "09:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "out_slot", mcptest.Quando("2026-10-10", "23:30"))

	h := &handlers.CampaignsHandler{Repo: catalog.NewCampaigns(pool)}
	var tela []catalog.CampaignFinancials
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Financials, "/campaigns/financials?ids="+camp.String(), nil, admin), &tela))
	require.Len(t, tela, 1)

	out := chamarTool(t, cfgTeste(pool), "financeiro_campanhas", map[string]any{"campanha_ids": []string{camp.String()}})
	l := itens(t, out)[0]
	require.EqualValues(t, tela[0].TotalInvested, l["investido"])
	require.EqualValues(t, tela[0].TotalBonusValue, l["bonificacao"])
	require.EqualValues(t, tela[0].TotalInsertions, l["insercoes"])
	require.EqualValues(t, tela[0].TotalAudience, l["impactos"])
	// Conferência independente da base in_slot + bonus (out_slot não vale nada):
	require.EqualValues(t, 3, l["insercoes"])
	require.EqualValues(t, 200, l["investido"])
	require.EqualValues(t, 100, l["bonificacao"])
	require.EqualValues(t, 3000, l["impactos"])
	require.EqualValues(t, 100, l["cpm"], "(200 + 100) / 3000 × 1000")
	require.NotEmpty(t, out["ressalvas"])

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "financeiro_campanhas", map[string]any{"campanha_ids": []string{}}), "[entrada_invalida]")
}
```

- [ ] **Step 2: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/mcp/ -run 'TestListarCampanhas|TestObterCampanha|TestFinanceiroCampanhas' 2>&1 | head
```
Expected: FAIL (tools inexistentes).

- [ ] **Step 3: Implemente**

`workers/internal/mcp/tools_campanhas.go`:
```go
package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
	"radiocheck/internal/hub"
)

type campanhaDTO struct {
	ID            string   `json:"id"`
	Nome          string   `json:"nome"`
	Cliente       ref      `json:"cliente"`
	Estado        string   `json:"estado"`
	Inicio        string   `json:"inicio"`
	Fim           string   `json:"fim"`
	EmissorasAlvo int      `json:"emissoras_alvo"`
	Materiais     int      `json:"materiais"`
	CPMFixo       *float64 `json:"cpm_fixo"`
	CodigoHub     *string  `json:"codigo_hub"`
	CanceladaEm   *string  `json:"cancelada_em"`
}

func campanhaDe(c catalog.Campaign, clientes map[uuid.UUID]string, canceladas map[uuid.UUID]time.Time) campanhaDTO {
	d := campanhaDTO{
		ID: c.ID.String(), Nome: c.Name, Cliente: refDe(c.ClientID, clientes[c.ClientID]), Estado: c.Status,
		Inicio: dia(c.StartDate), Fim: dia(c.EndDate), EmissorasAlvo: len(c.TargetStations),
		Materiais: c.MaterialCount, CPMFixo: c.FixedCPM, CodigoHub: c.HubCode,
	}
	if em, ok := canceladas[c.ID]; ok {
		s := instante(em)
		d.CanceladaEm = &s
	}
	return d
}

var estadosCampanha = []string{"programada", "ativa", "concluida", "cancelada"}

// tetoCampanhas: a tabela tem centenas de linhas; o corte só existe para a
// consulta nunca ser ilimitada.
const tetoCampanhas = 10_000

const ressalvaCancelada = "Há campanha cancelada no resultado: ela aparece só como histórico — fica fora de KPIs, seletores e cobrança, e o déficit dela está congelado em cancelada_em."

const ressalvaBaseFinanceira = "Inserções, impactos, investido e bonificação usam a base in_slot + bonus (out_slot não vale nada e out_date está fora do período). O consolidado acumula por mês iniciado até hoje (America/Sao_Paulo). impactos_target só soma emissoras com PMM no target do cliente. CPM: o fixo, quando a campanha tem; senão (investido + bonificação) ÷ impactos × 1000."

// lerCampanha valida o id e carrega a campanha; rec != nil é a recusa pronta.
func lerCampanha(ctx context.Context, L *Leitura, s string) (*catalog.Campaign, *sdk.CallToolResult, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		rec, _ := recusaEntrada("campanha_id")
		return nil, rec, nil
	}
	c, err := L.Campanhas.Get(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		rec, _ := naoEncontrado("campanha")
		return nil, rec, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return c, nil, nil
}

type entradaListarCampanhas struct {
	Estados           []string `json:"estados,omitempty" jsonschema:"Filtra por estado: programada, ativa, concluida, cancelada. Vazio = todos menos cancelada."`
	ClienteID         string   `json:"cliente_id,omitempty" jsonschema:"Só as campanhas deste cliente (id de listar_clientes)."`
	Busca             string   `json:"busca,omitempty" jsonschema:"Texto no nome da campanha ou do cliente. Ignora acento e caixa."`
	Competencia       string   `json:"competencia,omitempty" jsonschema:"Mês AAAA-MM: só campanhas cuja vigência cruza esse mês."`
	IncluirCanceladas bool     `json:"incluir_canceladas,omitempty" jsonschema:"true para trazer as canceladas junto (por padrão ficam fora)."`
	Paginacao
}

type entradaObterCampanha struct {
	CampanhaID string `json:"campanha_id,omitempty" jsonschema:"Id (uuid) da campanha no E-monitor. Passe este OU codigo."`
	Codigo     string `json:"codigo,omitempty" jsonschema:"Código da campanha no hub (EH-XXXXXX), o mesmo que o MCP do hub devolve. Passe este OU campanha_id."`
}

type linksTela struct {
	Tela *string `json:"tela"`
}

type saidaObterCampanha struct {
	Campanha  campanhaDTO `json:"campanha"`
	Emissoras []ref       `json:"emissoras"`
	Links     linksTela   `json:"links"`
	Ressalvas []string    `json:"ressalvas,omitempty"`
}

type entradaFinanceiro struct {
	CampanhaIDs []string `json:"campanha_ids" jsonschema:"Ids das campanhas (1 a 100), vindos de listar_campanhas. Nunca invente: pergunte."`
}

type linhaFinanceira struct {
	Campanha           ref      `json:"campanha"`
	Estado             string   `json:"estado"`
	Investido          float64  `json:"investido"`
	Bonificacao        float64  `json:"bonificacao"`
	Insercoes          int      `json:"insercoes"`
	Impactos           float64  `json:"impactos"`
	ImpactosTarget     float64  `json:"impactos_target"`
	EmissorasComTarget int      `json:"emissoras_com_target"`
	CPMFixo            *float64 `json:"cpm_fixo"`
	CPM                *float64 `json:"cpm"`
}

func registrarCampanhas(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "listar_campanhas",
		"Lista as campanhas na ordem da tela /campaigns (ativas, programadas, concluídas): id, nome, cliente, estado, vigência, "+
			"nº de emissoras-alvo e de materiais, CPM fixo e código do hub. Filtros: estados, cliente_id, busca, competencia (AAAA-MM). "+
			"A armadilha: canceladas ficam FORA por padrão (são só histórico); peça incluir_canceladas ou estados=[cancelada]. "+
			"Lista paginada: siga page.has_more.",
		func(ctx context.Context, L *Leitura, in entradaListarCampanhas) (*sdk.CallToolResult, error) {
			var clientes []uuid.UUID // nil = sem filtro; slice vazio filtraria TUDO
			if in.ClienteID != "" {
				id, err := uuid.Parse(in.ClienteID)
				if err != nil {
					return recusaEntrada("cliente_id")
				}
				clientes = []uuid.UUID{id}
			}
			if in.Competencia != "" {
				if _, err := time.Parse("2006-01", in.Competencia); err != nil {
					return recusa("competencia tem de ser AAAA-MM (ex.: 2026-10)", "entrada_invalida")
				}
			}
			for _, e := range in.Estados {
				if !slices.Contains(estadosCampanha, e) {
					return recusa(fmt.Sprintf("estado desconhecido %q: use programada, ativa, concluida ou cancelada", e), "entrada_invalida")
				}
			}
			todas, total, err := L.Campanhas.ListPaged(ctx, in.Busca, in.Competencia, clientes, nil, 1, tetoCampanhas)
			if err != nil {
				return nil, err
			}
			querCancelada := in.IncluirCanceladas || slices.Contains(in.Estados, "cancelada")
			filtradas := make([]catalog.Campaign, 0, len(todas))
			for _, c := range todas {
				if len(in.Estados) > 0 && !slices.Contains(in.Estados, c.Status) {
					continue
				}
				if c.Status == "cancelada" && !querCancelada {
					continue
				}
				filtradas = append(filtradas, c)
			}
			pg := paginar(filtradas, in.Paginacao)
			ids := make([]uuid.UUID, len(pg.Items))
			for i, c := range pg.Items {
				ids[i] = c.ID
			}
			canceladas, err := L.Campanhas.CancelledAtFor(ctx, ids)
			if err != nil {
				return nil, err
			}
			nomes, err := nomesClientes(ctx, L)
			if err != nil {
				return nil, err
			}
			out := mapear(pg, func(c catalog.Campaign) campanhaDTO { return campanhaDe(c, nomes, canceladas) })
			if len(canceladas) > 0 {
				out.Ressalvas = append(out.Ressalvas, ressalvaCancelada)
			}
			if total > tetoCampanhas {
				out.Ressalvas = append(out.Ressalvas, fmt.Sprintf(
					"a busca casou %d campanhas e só as %d primeiras foram consideradas: estreite com cliente_id, competencia ou busca", total, tetoCampanhas))
			}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "obter_campanha",
		"Devolve uma campanha (cliente, estado, vigência, CPM fixo, código do hub, nº de materiais), as emissoras-alvo com nome e "+
			"o link da tela. Aceita campanha_id OU codigo (EH-XXXXXX, o código canônico do hub, a ponte entre os MCPs). Um código "+
			"pode estar em mais de uma campanha: aí a tool lista as candidatas e você pergunta qual. Nunca invente ids.",
		func(ctx context.Context, L *Leitura, in entradaObterCampanha) (*sdk.CallToolResult, error) {
			if (in.CampanhaID == "") == (in.Codigo == "") {
				return recusa("passe campanha_id OU codigo (exatamente um dos dois)", "entrada_invalida")
			}
			var c *catalog.Campaign
			if in.CampanhaID != "" {
				got, rec, err := lerCampanha(ctx, L, in.CampanhaID)
				if rec != nil || err != nil {
					return rec, err
				}
				c = got
			} else {
				cod := hub.NormalizaCodigo(in.Codigo)
				if cod == "" {
					return recusa("codigo não é um código de campanha do hub (formato EH-XXXXXX)", "entrada_invalida")
				}
				achadas, err := L.Campanhas.ListByHubCode(ctx, cod)
				if err != nil {
					return nil, err
				}
				switch len(achadas) {
				case 0:
					return naoEncontrado("campanha com o código " + cod)
				case 1:
					c = &achadas[0]
				default:
					cands := make([]string, len(achadas))
					for i, a := range achadas {
						cands[i] = fmt.Sprintf("%s (id %s, %s)", a.Name, a.ID, a.Status)
					}
					return recusa(fmt.Sprintf("o código %s está em %d campanhas (dois PIs podem dividir um código): %s. Pergunte ao usuário qual e chame de novo com campanha_id",
						cod, len(achadas), strings.Join(cands, "; ")), "ambiguo")
				}
			}
			vinculos, err := L.MateriaisCampanha.ListByCampaign(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			c.MaterialCount = len(vinculos)
			nomes, err := nomesClientes(ctx, L)
			if err != nil {
				return nil, err
			}
			canceladas, err := L.Campanhas.CancelledAtFor(ctx, []uuid.UUID{c.ID})
			if err != nil {
				return nil, err
			}
			emissoras, err := nomesEmissoras(ctx, L, c.TargetStations)
			if err != nil {
				return nil, err
			}
			out := saidaObterCampanha{
				Campanha:  campanhaDe(*c, nomes, canceladas),
				Emissoras: refs(c.TargetStations, emissoras),
				Links:     linksTela{Tela: cfg.link("/campaigns/" + c.ID.String() + "/edit")},
			}
			if c.Status == "cancelada" {
				out.Ressalvas = []string{ressalvaCancelada}
			}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "financeiro_campanhas",
		"Devolve, por campanha, o que a tela /campaigns mostra de dinheiro e audiência: investido executado, bonificação, "+
			"inserções, impactos, impactos no target, emissoras com target e CPM (o fixo, ou o dinâmico já calculado). "+
			"Pede de 1 a 100 campanha_ids (pegue de listar_campanhas). Nunca some nem recalcule: os números vêm prontos.",
		func(ctx context.Context, L *Leitura, in entradaFinanceiro) (*sdk.CallToolResult, error) {
			if len(in.CampanhaIDs) == 0 || len(in.CampanhaIDs) > limiteTeto {
				return recusa("campanha_ids precisa de 1 a 100 ids", "entrada_invalida")
			}
			ids := make([]uuid.UUID, 0, len(in.CampanhaIDs))
			for _, s := range in.CampanhaIDs {
				id, err := uuid.Parse(s)
				if err != nil {
					return recusaEntrada("campanha_ids")
				}
				ids = append(ids, id)
			}
			fins, err := L.Campanhas.FinancialsByCampaign(ctx, nil, ids, catalog.TodaySaoPaulo())
			if err != nil {
				return nil, err
			}
			camps, err := todasCampanhas(ctx, L)
			if err != nil {
				return nil, err
			}
			achou := map[uuid.UUID]bool{}
			cancelada := false
			linhas := make([]linhaFinanceira, 0, len(fins))
			for _, f := range fins {
				c := camps[f.CampaignID]
				achou[f.CampaignID] = true
				cancelada = cancelada || c.Status == "cancelada"
				linhas = append(linhas, linhaFinanceira{
					Campanha: refDe(f.CampaignID, c.Name), Estado: c.Status,
					Investido: f.TotalInvested, Bonificacao: f.TotalBonusValue, Insercoes: f.TotalInsertions,
					Impactos: f.TotalAudience, ImpactosTarget: f.TotalAudienceTarget,
					EmissorasComTarget: f.StationsWithTarget, CPMFixo: f.FixedCPM, CPM: catalog.CPMDaCampanha(f),
				})
			}
			l := listaInteira(linhas)
			l.Ressalvas = []string{ressalvaBaseFinanceira}
			var faltam []string
			for _, id := range ids {
				if !achou[id] {
					faltam = append(faltam, id.String())
				}
			}
			if len(faltam) > 0 {
				l.Ressalvas = append(l.Ressalvas, "sem campanha no E-monitor para: "+strings.Join(faltam, ", "))
			}
			if cancelada {
				l.Ressalvas = append(l.Ressalvas, ressalvaCancelada)
			}
			return ok(l)
		})
}
```

Em `servidor.go`: acrescente `"listar_campanhas", "obter_campanha", "financeiro_campanhas",` a `ToolsEsperadas` (depois de `"pmm_target_cliente"`) e `registrarCampanhas(s, cfg)` depois de `registrarClientes(s, cfg)`.

- [ ] **Step 4: Rode e veja passar**

```bash
cd C:/tmp/emonitor-mcp/workers && go vet ./internal/mcp/ && go test -p 1 ./internal/mcp/ -run 'TestListarCampanhas|TestObterCampanha|TestFinanceiroCampanhas|TestInventario' -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```
Expected: todos PASS. Se `TestFinanceiroCampanhas` divergir da conferência independente (3 / 200 / 100 / 3000), **pare**: a diferença está na base financeira, não no MCP — investigue antes de mexer no teste.

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add workers/internal/mcp/ && git commit -m "feat(mcp): tools de campanha — listar (sem canceladas), obter por id ou código do hub, financeiro"
```

---

### Task 8: Campanhas — materiais, regras, ajustes do dia e preços

**Files:**
- Modify: `workers/internal/mcp/tools_campanhas.go`, `workers/internal/mcp/tools_campanhas_test.go`, `workers/internal/mcp/servidor.go`

**Fonte:** `GET /campaigns/{id}/materials` → `CampaignMaterials.ListByCampaign`; `…/distribution-rules` → `DistributionRules.ListByCampaign`; `…/distribution-overrides?from&to` → `DistributionOverrides.ListByCampaignAndDateRange`; `…/pricing` → `Pricing.ListByCampaign`. Diferença deliberada: para campanha inexistente a tela de admin devolve `[]`; a tool devolve `[nao_encontrado]` (o modelo precisa saber que errou o id).

- [ ] **Step 1: Testes**

Acrescente a `workers/internal/mcp/tools_campanhas_test.go`:
```go
// cenarioCampanha monta uma campanha com material, regra (seg a sex), ajuste e preço.
func cenarioCampanha(t *testing.T) (ctx context.Context, pool *pgxpool.Pool, admin, camp, est, tipo, mat uuid.UUID) {
	t.Helper()
	ctx, pool = mcptest.DBCatalogo(t)
	admin = mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est = mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	tipo = mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat = mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp = mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	mcptest.Vincular(t, ctx, pool, camp, mat, est)
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 62, "06:00", "12:00", 2)
	mcptest.Ajuste(t, ctx, pool, camp, tipo, est, "2026-10-15", 0, "00:00", "23:59")
	mcptest.Preco(t, ctx, pool, camp, est, tipo, 150)
	return
}

func TestListarMateriaisCampanha_ParidadeComATela(t *testing.T) {
	_, pool, admin, camp, est, tipo, mat := cenarioCampanha(t)
	h := &handlers.CampaignMaterialsHandler{Repo: catalog.NewCampaignMaterials(pool), CampaignRepo: catalog.NewCampaigns(pool)}
	var tela []catalog.CampaignMaterial
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.ListByCampaign, "/x", map[string]string{"campaignID": camp.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_materiais_campanha", map[string]any{"campanha_id": camp.String()})
	l := itens(t, out)
	require.Len(t, l, len(tela))
	require.Equal(t, mat.String(), l[0]["material"].(map[string]any)["id"])
	require.Equal(t, "Spot A", l[0]["material"].(map[string]any)["nome"])
	require.Equal(t, tipo.String(), l[0]["tipo"].(map[string]any)["id"])
	require.Equal(t, est.String(), l[0]["emissoras"].([]any)[0].(map[string]any)["id"])

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_materiais_campanha",
		map[string]any{"campanha_id": uuid.NewString()}), "[nao_encontrado]")
}

func TestListarRegrasDistribuicao_ParidadeComATela(t *testing.T) {
	_, pool, admin, camp, _, _, _ := cenarioCampanha(t)
	h := &handlers.DistributionRulesHandler{Repo: catalog.NewDistributionRules(pool), CampaignRepo: catalog.NewCampaigns(pool)}
	var tela []catalog.DistributionRule
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.ListByCampaign, "/x", map[string]string{"campaignID": camp.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_regras_distribuicao", map[string]any{"campanha_id": camp.String()})
	l := itens(t, out)
	require.Len(t, l, len(tela))
	require.Equal(t, tela[0].ID.String(), l[0]["id"])
	require.EqualValues(t, tela[0].PlaysPerDay, l[0]["insercoes_por_dia"])
	require.Equal(t, []any{"seg", "ter", "qua", "qui", "sex"}, l[0]["dias_da_semana"])
	require.Equal(t, map[string]any{"inicio": "06:00", "fim": "12:00"}, l[0]["faixa"])
	require.Empty(t, l[0]["materiais"], "vazio = todos os materiais do tipo")
}

func TestListarOverrides_ParidadeComATela(t *testing.T) {
	_, pool, admin, camp, _, _, _ := cenarioCampanha(t)
	h := &handlers.DistributionOverridesHandler{Repo: catalog.NewDistributionOverrides(pool), Recat: catalog.NewDistributionRules(pool)}
	var tela []catalog.DistributionOverride
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.ListByDateRange, "/x?from=2026-10-01&to=2026-10-31",
		map[string]string{"campaignID": camp.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_overrides",
		map[string]any{"campanha_id": camp.String(), "de": "2026-10-01", "ate": "2026-10-31"})
	l := itens(t, out)
	require.Len(t, l, len(tela))
	require.Equal(t, "2026-10-15", l[0]["data"])
	require.EqualValues(t, 0, l[0]["insercoes_esperadas"])
	require.NotEmpty(t, out["ressalvas"])

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_overrides",
		map[string]any{"campanha_id": camp.String(), "de": "2026-10-31", "ate": "2026-10-01"}), "[entrada_invalida]")
}

func TestListarPrecosCampanha_ParidadeComATela(t *testing.T) {
	_, pool, admin, camp, est, tipo, _ := cenarioCampanha(t)
	h := &handlers.PricingHandler{Repo: catalog.NewPricing(pool), CampaignRepo: catalog.NewCampaigns(pool)}
	var tela []catalog.StationPricing
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.ListByCampaign, "/x", map[string]string{"campaignID": camp.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_precos_campanha", map[string]any{"campanha_id": camp.String()})
	l := itens(t, out)
	require.Len(t, l, len(tela))
	require.Equal(t, est.String(), l[0]["emissora"].(map[string]any)["id"])
	require.Equal(t, "por_insercao", l[0]["modo"])
	pt := l[0]["por_tipo"].([]any)[0].(map[string]any)
	require.Equal(t, tipo.String(), pt["tipo"].(map[string]any)["id"])
	require.EqualValues(t, tela[0].PerType[0].UnitValue, pt["valor_unitario"])
}
```
(Acrescente `"context"` e `"github.com/jackc/pgx/v5/pgxpool"` aos imports do arquivo de teste — o `cenarioCampanha` os usa.)

- [ ] **Step 2: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/mcp/ -run 'TestListarMateriaisCampanha|TestListarRegras|TestListarOverrides|TestListarPrecos' 2>&1 | head
```
Expected: FAIL (tools inexistentes).

- [ ] **Step 3: Implemente**

Acrescente a `workers/internal/mcp/tools_campanhas.go`:
```go
type entradaCampanhaPaginada struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas ou obter_campanha. Nunca invente: pergunte."`
	Paginacao
}

type entradaOverrides struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha, vindo de listar_campanhas ou obter_campanha. Nunca invente: pergunte."`
	De         string `json:"de" jsonschema:"Primeiro dia (AAAA-MM-DD)."`
	Ate        string `json:"ate" jsonschema:"Último dia (AAAA-MM-DD), até 366 dias depois de de."`
	Paginacao
}

type faixaHoraria struct {
	Inicio string `json:"inicio"`
	Fim    string `json:"fim"`
}

type materialDaCampanha struct {
	Material    ref    `json:"material"`
	Tipo        *ref   `json:"tipo"`
	Emissoras   []ref  `json:"emissoras"`
	VinculadoEm string `json:"vinculado_em"`
}

type regraDTO struct {
	ID              string       `json:"id"`
	Nome            string       `json:"nome"`
	Tipo            ref          `json:"tipo"`
	Emissoras       []ref        `json:"emissoras"`
	Materiais       []ref        `json:"materiais"`
	Inicio          string       `json:"inicio"`
	Fim             string       `json:"fim"`
	DiasDaSemana    []string     `json:"dias_da_semana"`
	Faixa           faixaHoraria `json:"faixa"`
	InsercoesPorDia int16        `json:"insercoes_por_dia"`
}

type ajusteDTO struct {
	Data               string       `json:"data"`
	Tipo               ref          `json:"tipo"`
	Emissora           ref          `json:"emissora"`
	InsercoesEsperadas int16        `json:"insercoes_esperadas"`
	Faixa              faixaHoraria `json:"faixa"`
	Motivo             *string      `json:"motivo"`
	CriadoEm           string       `json:"criado_em"`
}

type precoPorTipo struct {
	Tipo          ref     `json:"tipo"`
	ValorUnitario float64 `json:"valor_unitario"`
}

type precoDTO struct {
	Emissora         ref            `json:"emissora"`
	Modo             string         `json:"modo"`
	ValorConsolidado *float64       `json:"valor_consolidado"`
	PorTipo          []precoPorTipo `json:"por_tipo"`
}

var nomesDosDias = [7]string{"dom", "seg", "ter", "qua", "qui", "sex", "sab"}

// diasDaMascara: bit 0 = domingo … bit 6 = sábado (migration 0019).
func diasDaMascara(m int16) []string {
	out := []string{}
	for i, n := range nomesDosDias {
		if m&(1<<i) != 0 {
			out = append(out, n)
		}
	}
	return out
}

// modoDoPreco: vocabulário da tela.
func modoDoPreco(m string) string {
	if m == catalog.PricingModePerInsertion {
		return "por_insercao"
	}
	return "consolidado"
}

const ressalvaRegras = "materiais vazio = a regra vale para todos os materiais do tipo; com materiais, é um recorte só para eles. A meta de uma célula (emissora × tipo × dia) é a soma das regras que valem naquele dia; um ajuste do dia (listar_overrides) substitui as regras da célula."

const ressalvaAjustes = "O ajuste do dia substitui as regras daquela célula: insercoes_esperadas vira a meta e a faixa dele é a única válida. insercoes_esperadas = 0 → toda tocada do dia vira bonus (nunca out_slot)."

const ressalvaPrecos = "Só emissoras com linha aqui entram no investido. Consolidado = valor do contrato da emissora, acumulado por mês iniciado; por inserção = valor unitário × in_slot (e × bonus na bonificação)."

func registrarCampanhasDetalhe(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "listar_materiais_campanha",
		"Lista os materiais (áudios) vinculados a uma campanha, com o tipo e as emissoras em que cada um pode tocar. "+
			"Use antes de interpretar a grade (que é por tipo, não por material). Nunca invente o campanha_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaCampanhaPaginada) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			vinculos, err := L.MateriaisCampanha.ListByCampaign(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			mats, err := L.Materiais.ListByClient(ctx, c.ClientID, "")
			if err != nil {
				return nil, err
			}
			porID := make(map[uuid.UUID]catalog.Material, len(mats))
			for _, m := range mats {
				porID[m.ID] = m
			}
			tipos, err := nomesTipos(ctx, L)
			if err != nil {
				return nil, err
			}
			var todasEmissoras []uuid.UUID
			for _, v := range vinculos {
				todasEmissoras = append(todasEmissoras, v.TargetStations...)
			}
			emissoras, err := nomesEmissoras(ctx, L, todasEmissoras)
			if err != nil {
				return nil, err
			}
			linhas := make([]materialDaCampanha, len(vinculos))
			for i, v := range vinculos {
				m := porID[v.MaterialID]
				linhas[i] = materialDaCampanha{
					Material: refDe(v.MaterialID, m.Title), Emissoras: refs(v.TargetStations, emissoras),
					VinculadoEm: instante(v.AddedAt),
				}
				if m.TypeID != nil {
					r := refDe(*m.TypeID, tipos[*m.TypeID])
					linhas[i].Tipo = &r
				}
			}
			return ok(paginar(linhas, in.Paginacao))
		})

	ferramentaLeitura(s, cfg, "listar_regras_distribuicao",
		"Lista as regras de distribuição de uma campanha: tipo de material, emissoras, materiais (vazio = todos do tipo), "+
			"vigência, dias da semana, faixa horária e inserções por dia. É a programação que gera a meta da grade. "+
			"Nunca invente o campanha_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaCampanhaPaginada) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			regras, err := L.Regras.ListByCampaign(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			tipos, err := nomesTipos(ctx, L)
			if err != nil {
				return nil, err
			}
			mats, err := nomesMateriais(ctx, L, c.ClientID)
			if err != nil {
				return nil, err
			}
			var todasEmissoras []uuid.UUID
			for _, r := range regras {
				todasEmissoras = append(todasEmissoras, r.StationIDs...)
			}
			emissoras, err := nomesEmissoras(ctx, L, todasEmissoras)
			if err != nil {
				return nil, err
			}
			linhas := make([]regraDTO, len(regras))
			for i, r := range regras {
				linhas[i] = regraDTO{
					ID: r.ID.String(), Nome: r.Name, Tipo: refDe(r.TypeID, tipos[r.TypeID]),
					Emissoras: refs(r.StationIDs, emissoras), Materiais: refs(r.MaterialIDs, mats),
					Inicio: dia(r.StartDate), Fim: dia(r.EndDate), DiasDaSemana: diasDaMascara(r.WeekdayMask),
					Faixa: faixaHoraria{Inicio: r.TimeStart, Fim: r.TimeEnd}, InsercoesPorDia: r.PlaysPerDay,
				}
			}
			l := paginar(linhas, in.Paginacao)
			l.Ressalvas = []string{ressalvaRegras}
			return ok(l)
		})

	ferramentaLeitura(s, cfg, "listar_overrides",
		"Lista os ajustes do dia (overrides) de uma campanha num período: para cada célula (emissora × tipo × dia), a meta e a "+
			"faixa que substituem as regras naquele dia. A armadilha: meta 0 faz toda tocada do dia virar bônus. "+
			"Pede de/ate (AAAA-MM-DD, até 366 dias). Nunca invente o campanha_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaOverrides) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			de, okDe := lerDia(in.De)
			ate, okAte := lerDia(in.Ate)
			if !okDe {
				return recusaEntrada("de")
			}
			if !okAte {
				return recusaEntrada("ate")
			}
			if ate.Before(de) || ate.Sub(de) > 366*24*time.Hour {
				return recusa("período inválido: ate tem de ser >= de e no máximo 366 dias depois", "entrada_invalida")
			}
			ajustes, err := L.Ajustes.ListByCampaignAndDateRange(ctx, c.ID, de, ate)
			if err != nil {
				return nil, err
			}
			tipos, err := nomesTipos(ctx, L)
			if err != nil {
				return nil, err
			}
			var ests []uuid.UUID
			for _, a := range ajustes {
				ests = append(ests, a.StationID)
			}
			emissoras, err := nomesEmissoras(ctx, L, ests)
			if err != nil {
				return nil, err
			}
			linhas := make([]ajusteDTO, len(ajustes))
			for i, a := range ajustes {
				linhas[i] = ajusteDTO{
					Data: dia(a.ForDate), Tipo: refDe(a.TypeID, tipos[a.TypeID]),
					Emissora: refDe(a.StationID, emissoras[a.StationID]), InsercoesEsperadas: a.PlaysExpected,
					Faixa: faixaHoraria{Inicio: a.TimeStart, Fim: a.TimeEnd}, Motivo: a.Reason, CriadoEm: instante(a.CreatedAt),
				}
			}
			l := paginar(linhas, in.Paginacao)
			l.Ressalvas = []string{ressalvaAjustes}
			return ok(l)
		})

	ferramentaLeitura(s, cfg, "listar_precos_campanha",
		"Lista os preços de uma campanha por emissora: modo (consolidado ou por_insercao), valor consolidado ou valor unitário por "+
			"tipo de material. Só emissora com preço entra no investido. Valores em reais. Nunca invente o campanha_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaCampanhaPaginada) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			precos, err := L.Precos.ListByCampaign(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			tipos, err := nomesTipos(ctx, L)
			if err != nil {
				return nil, err
			}
			ests := make([]uuid.UUID, len(precos))
			for i, p := range precos {
				ests[i] = p.StationID
			}
			emissoras, err := nomesEmissoras(ctx, L, ests)
			if err != nil {
				return nil, err
			}
			linhas := make([]precoDTO, len(precos))
			for i, p := range precos {
				pt := make([]precoPorTipo, len(p.PerType))
				for j, tp := range p.PerType {
					pt[j] = precoPorTipo{Tipo: refDe(tp.TypeID, tipos[tp.TypeID]), ValorUnitario: tp.UnitValue}
				}
				linhas[i] = precoDTO{
					Emissora: refDe(p.StationID, emissoras[p.StationID]), Modo: modoDoPreco(p.Mode),
					ValorConsolidado: p.ConsolidatedValue, PorTipo: pt,
				}
			}
			l := paginar(linhas, in.Paginacao)
			l.Ressalvas = []string{ressalvaPrecos}
			return ok(l)
		})
}
```

Em `servidor.go`: acrescente `"listar_materiais_campanha", "listar_regras_distribuicao", "listar_overrides", "listar_precos_campanha",` a `ToolsEsperadas` (depois de `"financeiro_campanhas"`) e `registrarCampanhasDetalhe(s, cfg)` depois de `registrarCampanhas(s, cfg)`.

- [ ] **Step 4: Rode e veja passar**

```bash
cd C:/tmp/emonitor-mcp/workers && go vet ./internal/mcp/ && go test -p 1 ./internal/mcp/ -run 'TestListarMateriaisCampanha|TestListarRegras|TestListarOverrides|TestListarPrecos|TestInventario' -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```
Expected: todos PASS.

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add workers/internal/mcp/ && git commit -m "feat(mcp): tools de campanha — materiais, regras, ajustes do dia e preços"
```

---

### Task 9: Grade — `grade_campanha` e `plano_do_dia`

**Files:**
- Create: `workers/internal/mcp/tools_grade.go`, `workers/internal/mcp/tools_grade_test.go`
- Modify: `workers/internal/mcp/servidor.go`

**Fonte:** `GET /campaigns/{id}/daily-summary` → `DailySummaryRepo.ListByCampaign` (função SQL `daily_play_summary_for`, migration 0065). `plano_do_dia` (decisão F1-1): saldo = a linha da grade; regras = `DistributionRules.ListByCampaign` filtradas por tipo/emissora e decididas por `categorizer.RuleCoversDay` (Task 5); ajuste = `DistributionOverrides.ListByCampaignAndDateRange(dia, dia)`; veiculações = `Detections.List` com o mesmo recorte do `DayDetailModal` (campanha + emissora + dia em SP, conjunto aprovado), filtradas pelo tipo.

- [ ] **Step 1: Testes**

`workers/internal/mcp/tools_grade_test.go`:
```go
package mcp

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func TestGradeCampanha_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", e1, e2)
	mcptest.Regra(t, ctx, pool, camp, tipo, e1, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 2)
	mcptest.Regra(t, ctx, pool, camp, tipo, e2, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 1)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "out_slot", mcptest.Quando("2026-10-10", "23:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e2, "in_slot", mcptest.Quando("2026-10-11", "09:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e2, "bonus", mcptest.Quando("2026-10-11", "10:00"))

	h := &handlers.DetectionsHandler{SummaryRepo: catalog.NewDailySummary(pool), CampaignRepo: catalog.NewCampaigns(pool)}
	var tela []catalog.DailySummaryRow
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.DailySummary, "/x?from=2026-10-10&to=2026-10-11",
		map[string]string{"campaignID": camp.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "grade_campanha",
		map[string]any{"campanha_id": camp.String(), "de": "2026-10-10", "ate": "2026-10-11"})
	celulas := itens(t, out)
	require.Len(t, celulas, len(tela))
	porChave := map[string]catalog.DailySummaryRow{}
	for _, r := range tela {
		porChave[r.StationID.String()+"|"+r.ForDate.Format("2006-01-02")] = r
	}
	var somaInSlot, somaEsperado int
	for _, c := range celulas {
		r := porChave[c["emissora"].(map[string]any)["id"].(string)+"|"+c["data"].(string)]
		require.EqualValues(t, r.Expected, c["esperado"])
		require.EqualValues(t, r.InSlot, c["in_slot"])
		require.EqualValues(t, r.Deficit, c["deficit"])
		require.EqualValues(t, r.Bonus, c["bonus"])
		require.EqualValues(t, r.OutSlot, c["out_slot"])
		somaInSlot += int(r.InSlot)
		somaEsperado += int(r.Expected)
	}
	tot := out["totais"].(map[string]any)
	require.EqualValues(t, somaInSlot, tot["in_slot"])
	require.EqualValues(t, somaEsperado, tot["esperado"])

	so := chamarTool(t, cfgTeste(pool), "grade_campanha",
		map[string]any{"campanha_id": camp.String(), "de": "2026-10-10", "ate": "2026-10-11", "emissora_id": e2.String()})
	require.NotEmpty(t, itens(t, so))
	for _, c := range itens(t, so) {
		require.Equal(t, e2.String(), c["emissora"].(map[string]any)["id"])
	}
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "grade_campanha",
		map[string]any{"campanha_id": camp.String(), "de": "2026-01-01", "ate": "2026-12-31"}), "[entrada_invalida]")
}

func TestPlanoDoDia_FaixasSaldoEVeiculacoes(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	outroTipo := mcptest.TipoMaterial(t, ctx, pool, "Vinheta")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	vinheta := mcptest.Material(t, ctx, pool, cli, outroTipo, "Vinheta")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-11-30", est)
	semana := mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 62, "06:00", "12:00", 2)
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 65, "08:00", "10:00", 1) // sáb e dom
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-11-01", "2026-11-30", 127, "06:00", "22:00", 3) // novembro
	// 2026-10-27 é uma terça.
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-27", "10:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "out_slot", mcptest.Quando("2026-10-27", "23:30"))
	mcptest.Veiculacao(t, ctx, pool, camp, vinheta, est, "bonus", mcptest.Quando("2026-10-27", "11:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-28", "10:00"))

	args := map[string]any{"campanha_id": camp.String(), "emissora_id": est.String(), "tipo_id": tipo.String(), "data": "2026-10-27"}
	out := chamarTool(t, cfgTeste(pool), "plano_do_dia", args)
	require.Equal(t, "regras", out["meta_vem_de"])
	faixas := out["faixas"].([]any)
	require.Len(t, faixas, 1)
	require.Equal(t, semana.String(), faixas[0].(map[string]any)["regra_id"])
	naoValem := out["regras_que_nao_valem"].([]any)
	require.Len(t, naoValem, 2)
	motivos := []string{naoValem[0].(map[string]any)["motivo"].(string), naoValem[1].(map[string]any)["motivo"].(string)}
	require.Contains(t, motivos, "começa em 2026-11-01")

	// Saldo = a mesma linha da grade da tela.
	h := &handlers.DetectionsHandler{SummaryRepo: catalog.NewDailySummary(pool), CampaignRepo: catalog.NewCampaigns(pool)}
	var tela []catalog.DailySummaryRow
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.DailySummary, "/x?from=2026-10-27&to=2026-10-27",
		map[string]string{"campaignID": camp.String()}, admin), &tela))
	var linha catalog.DailySummaryRow
	for _, r := range tela {
		if r.TypeID == tipo && r.StationID == est {
			linha = r
		}
	}
	saldo := out["saldo"].(map[string]any)
	require.EqualValues(t, linha.Expected, saldo["esperado"])
	require.EqualValues(t, linha.InSlot, saldo["in_slot"])
	require.EqualValues(t, linha.OutSlot, saldo["out_slot"])

	// Veiculações: só do dia e só do tipo pedido, da mais cedo para a mais tarde.
	vs := out["veiculacoes"].([]any)
	require.Len(t, vs, 2)
	require.Equal(t, "in_slot", vs[0].(map[string]any)["categoria"])
	require.Equal(t, "out_slot", vs[1].(map[string]any)["categoria"])

	// Com ajuste do dia, a meta vem dele.
	mcptest.Ajuste(t, ctx, pool, camp, tipo, est, "2026-10-27", 0, "00:00", "23:59")
	comAjuste := chamarTool(t, cfgTeste(pool), "plano_do_dia", args)
	require.Equal(t, "ajuste_do_dia", comAjuste["meta_vem_de"])
	require.EqualValues(t, 0, comAjuste["ajuste_do_dia"].(map[string]any)["insercoes_esperadas"])
	require.GreaterOrEqual(t, len(comAjuste["ressalvas"].([]any)), 3)
}
```

- [ ] **Step 2: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/mcp/ -run 'TestGradeCampanha|TestPlanoDoDia' 2>&1 | head
```
Expected: FAIL (tools inexistentes).

- [ ] **Step 3: Implemente**

`workers/internal/mcp/tools_grade.go`:
```go
package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
	"radiocheck/internal/categorizer"
)

type entradaGrade struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha. Nunca invente: pergunte."`
	De         string `json:"de" jsonschema:"Primeiro dia (AAAA-MM-DD, dia de America/Sao_Paulo)."`
	Ate        string `json:"ate" jsonschema:"Último dia (AAAA-MM-DD), no máximo 93 dias depois de de."`
	EmissoraID string `json:"emissora_id,omitempty" jsonschema:"Só as células desta emissora (opcional)."`
	Paginacao
}

type celulaGrade struct {
	Emissora ref    `json:"emissora"`
	Tipo     ref    `json:"tipo"`
	Data     string `json:"data"`
	Esperado int32  `json:"esperado"`
	InSlot   int32  `json:"in_slot"`
	Deficit  int32  `json:"deficit"`
	Bonus    int32  `json:"bonus"`
	OutSlot  int32  `json:"out_slot"`
	OutDate  int32  `json:"out_date"`
}

type totaisGrade struct {
	Esperado int64 `json:"esperado"`
	InSlot   int64 `json:"in_slot"`
	Deficit  int64 `json:"deficit"`
	Bonus    int64 `json:"bonus"`
	OutSlot  int64 `json:"out_slot"`
	OutDate  int64 `json:"out_date"`
}

// saidaGrade é a lista da grade com os totais do período inteiro filtrado.
type saidaGrade struct {
	lista[celulaGrade]
	Totais totaisGrade `json:"totais"`
}

type entradaPlano struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha. Nunca invente: pergunte."`
	EmissoraID string `json:"emissora_id" jsonschema:"Id (uuid) da emissora da célula, vindo de grade_campanha."`
	TipoID     string `json:"tipo_id" jsonschema:"Id (uuid) do tipo de material da célula (a grade é por tipo), vindo de grade_campanha."`
	Data       string `json:"data" jsonschema:"Dia (AAAA-MM-DD) em America/Sao_Paulo."`
}

type saldoDia struct {
	Esperado int32 `json:"esperado"`
	InSlot   int32 `json:"in_slot"`
	Deficit  int32 `json:"deficit"`
	Bonus    int32 `json:"bonus"`
	OutSlot  int32 `json:"out_slot"`
	OutDate  int32 `json:"out_date"`
}

type faixaDoDia struct {
	RegraID         string       `json:"regra_id"`
	Nome            string       `json:"nome"`
	Faixa           faixaHoraria `json:"faixa"`
	InsercoesPorDia int16        `json:"insercoes_por_dia"`
	Materiais       []ref        `json:"materiais"`
}

type regraForaDoDia struct {
	RegraID string `json:"regra_id"`
	Nome    string `json:"nome"`
	Motivo  string `json:"motivo"`
}

type ajusteDoDia struct {
	InsercoesEsperadas int16        `json:"insercoes_esperadas"`
	Faixa              faixaHoraria `json:"faixa"`
	Motivo             *string      `json:"motivo"`
}

type tocadaDoDia struct {
	ID        string `json:"id"`
	Quando    string `json:"quando"`
	Material  ref    `json:"material"`
	Categoria string `json:"categoria"`
	Manual    bool   `json:"manual"`
}

type saidaPlano struct {
	Campanha          ref              `json:"campanha"`
	Emissora          ref              `json:"emissora"`
	Tipo              ref              `json:"tipo"`
	Data              string           `json:"data"`
	MetaVemDe         string           `json:"meta_vem_de"`
	Saldo             *saldoDia        `json:"saldo"`
	AjusteDoDia       *ajusteDoDia     `json:"ajuste_do_dia"`
	Faixas            []faixaDoDia     `json:"faixas"`
	RegrasQueNaoValem []regraForaDoDia `json:"regras_que_nao_valem"`
	Veiculacoes       []tocadaDoDia    `json:"veiculacoes"`
	Ressalvas         []string         `json:"ressalvas"`
}

const ressalvaGrade = "A grade é emissora × TIPO de material × dia (America/Sao_Paulo). esperado = meta do dia; deficit = esperado − in_slot; out_slot não vale nada (não fatura nem abate déficit); bonus é o que passou da cota. Os totais somam o período inteiro filtrado, não só a página."

const ressalvaPlano = "A categoria de cada veiculação é a do servidor (cota da célula-dia: as N primeiras dentro da faixa são in_slot, o resto é bonus; fora da faixa é out_slot enquanto a cota não fecha). Não recalcule: o saldo vem pronto."

// tetoTocadasDoDia: o modal pede 200; o MCP pede o máximo do repositório.
const tetoTocadasDoDia = 1000

// motivoForaDoDia explica por que uma regra da célula não vale no dia. A
// DECISÃO é de categorizer.RuleCoversDay; aqui é só o porquê, para o modelo.
func motivoForaDoDia(r catalog.DistributionRule, d time.Time) string {
	switch dd := dia(d); {
	case dd < dia(r.StartDate):
		return "começa em " + dia(r.StartDate)
	case dd > dia(r.EndDate):
		return "encerrou em " + dia(r.EndDate)
	default:
		return "não vale neste dia da semana (vale: " + strings.Join(diasDaMascara(r.WeekdayMask), ", ") + ")"
	}
}

// categoriaDe: "orphan" é o nome antigo de bonus (renomeada em 2026-08-17).
func categoriaDe(c string) string {
	if c == categorizer.CatOrphan {
		return categorizer.CatBonus
	}
	return c
}

func registrarGrade(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "grade_campanha",
		"Devolve a grade de veiculação de uma campanha (a tela /detections): para cada emissora × tipo de material × dia, "+
			"esperado, in_slot, deficit, bonus, out_slot e out_date, mais os totais do período. A armadilha: a grade é por TIPO, "+
			"não por material, e out_slot não vale nada. Período de até 93 dias. Para entender UMA célula, use plano_do_dia. "+
			"Nunca invente ids. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaGrade) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			de, okDe := lerDia(in.De)
			ate, okAte := lerDia(in.Ate)
			if !okDe {
				return recusaEntrada("de")
			}
			if !okAte {
				return recusaEntrada("ate")
			}
			if ate.Before(de) || ate.Sub(de) > 92*24*time.Hour {
				return recusa("período inválido: ate tem de ser >= de e no máximo 93 dias depois", "entrada_invalida")
			}
			var soEmissora *uuid.UUID
			if in.EmissoraID != "" {
				id, err := uuid.Parse(in.EmissoraID)
				if err != nil {
					return recusaEntrada("emissora_id")
				}
				soEmissora = &id
			}
			rows, err := L.Grade.ListByCampaign(ctx, c.ID, de, ate)
			if err != nil {
				return nil, err
			}
			var ests []uuid.UUID
			for _, r := range rows {
				ests = append(ests, r.StationID)
			}
			emissoras, err := nomesEmissoras(ctx, L, ests)
			if err != nil {
				return nil, err
			}
			tipos, err := nomesTipos(ctx, L)
			if err != nil {
				return nil, err
			}
			var tot totaisGrade
			celulas := make([]celulaGrade, 0, len(rows))
			for _, r := range rows {
				if soEmissora != nil && r.StationID != *soEmissora {
					continue
				}
				tot.Esperado += int64(r.Expected)
				tot.InSlot += int64(r.InSlot)
				tot.Deficit += int64(r.Deficit)
				tot.Bonus += int64(r.Bonus)
				tot.OutSlot += int64(r.OutSlot)
				tot.OutDate += int64(r.OutDate)
				celulas = append(celulas, celulaGrade{
					Emissora: refDe(r.StationID, emissoras[r.StationID]), Tipo: refDe(r.TypeID, tipos[r.TypeID]),
					Data: dia(r.ForDate), Esperado: r.Expected, InSlot: r.InSlot, Deficit: r.Deficit,
					Bonus: r.Bonus, OutSlot: r.OutSlot, OutDate: r.OutDate,
				})
			}
			out := saidaGrade{lista: paginar(celulas, in.Paginacao), Totais: tot}
			out.Ressalvas = []string{ressalvaGrade}
			if c.Status == "cancelada" {
				out.Ressalvas = append(out.Ressalvas, ressalvaCancelada)
			}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "plano_do_dia",
		"Explica UMA célula da grade (campanha × emissora × tipo × dia): o saldo do dia, de onde vem a meta (ajuste do dia, "+
			"regras ou nenhuma), as faixas que valem naquele dia, as regras que não valem e por quê, e as veiculações do dia com "+
			"a categoria do servidor. Use para responder 'por que essa tocada não contou' ou 'por que deu déficit'. "+
			"Os ids vêm de grade_campanha; nunca invente.",
		func(ctx context.Context, L *Leitura, in entradaPlano) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			eid, err := uuid.Parse(in.EmissoraID)
			if err != nil {
				return recusaEntrada("emissora_id")
			}
			tid, err := uuid.Parse(in.TipoID)
			if err != nil {
				return recusaEntrada("tipo_id")
			}
			d, okDia := lerDia(in.Data)
			if !okDia {
				return recusaEntrada("data")
			}
			est, err := L.Emissoras.Get(ctx, eid)
			if errors.Is(err, pgx.ErrNoRows) {
				return naoEncontrado("emissora")
			}
			if err != nil {
				return nil, err
			}
			tipo, err := L.TiposMaterial.Get(ctx, tid)
			if errors.Is(err, pgx.ErrNoRows) {
				return naoEncontrado("tipo de material")
			}
			if err != nil {
				return nil, err
			}
			out := saidaPlano{
				Campanha: refDe(c.ID, c.Name), Emissora: refDe(est.ID, est.Name), Tipo: refDe(tipo.ID, tipo.Name),
				Data: dia(d), Faixas: []faixaDoDia{}, RegrasQueNaoValem: []regraForaDoDia{}, Veiculacoes: []tocadaDoDia{},
				Ressalvas: []string{ressalvaPlano},
			}

			// Saldo: a mesma linha da grade (daily_play_summary_for).
			linhas, err := L.Grade.ListByCampaign(ctx, c.ID, d, d)
			if err != nil {
				return nil, err
			}
			for _, r := range linhas {
				if r.StationID == eid && r.TypeID == tid {
					out.Saldo = &saldoDia{Esperado: r.Expected, InSlot: r.InSlot, Deficit: r.Deficit,
						Bonus: r.Bonus, OutSlot: r.OutSlot, OutDate: r.OutDate}
				}
			}

			// Ajuste do dia: substitui as regras.
			ajustes, err := L.Ajustes.ListByCampaignAndDateRange(ctx, c.ID, d, d)
			if err != nil {
				return nil, err
			}
			for _, a := range ajustes {
				if a.StationID == eid && a.TypeID == tid {
					out.AjusteDoDia = &ajusteDoDia{InsercoesEsperadas: a.PlaysExpected,
						Faixa: faixaHoraria{Inicio: a.TimeStart, Fim: a.TimeEnd}, Motivo: a.Reason}
				}
			}

			// Regras da célula: valem ou não no dia pela MESMA decisão do Settle.
			regras, err := L.Regras.ListByCampaign(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			mats, err := nomesMateriais(ctx, L, c.ClientID)
			if err != nil {
				return nil, err
			}
			for _, r := range regras {
				if r.TypeID != tid || !slices.Contains(r.StationIDs, eid) {
					continue
				}
				if categorizer.RuleCoversDay(categorizer.Rule{StartDate: r.StartDate, EndDate: r.EndDate, WeekdayMask: r.WeekdayMask}, d) {
					out.Faixas = append(out.Faixas, faixaDoDia{
						RegraID: r.ID.String(), Nome: r.Name, Faixa: faixaHoraria{Inicio: r.TimeStart, Fim: r.TimeEnd},
						InsercoesPorDia: r.PlaysPerDay, Materiais: refs(r.MaterialIDs, mats),
					})
				} else {
					out.RegrasQueNaoValem = append(out.RegrasQueNaoValem,
						regraForaDoDia{RegraID: r.ID.String(), Nome: r.Name, Motivo: motivoForaDoDia(r, d)})
				}
			}
			switch {
			case out.AjusteDoDia != nil:
				out.MetaVemDe = "ajuste_do_dia"
				out.Ressalvas = append(out.Ressalvas, "Há ajuste do dia: a meta e a faixa dele substituem as regras listadas em faixas.")
				if out.AjusteDoDia.InsercoesEsperadas == 0 {
					out.Ressalvas = append(out.Ressalvas, "O ajuste zera a meta: toda tocada deste dia é bonus, nunca out_slot.")
				}
			case len(out.Faixas) > 0:
				out.MetaVemDe = "regras"
			default:
				out.MetaVemDe = "sem_meta"
				out.Ressalvas = append(out.Ressalvas, "Nenhuma regra nem ajuste vale neste dia: a meta é 0 e as tocadas entram como bonus.")
			}

			// Veiculações do dia: o recorte do DayDetailModal (conjunto aprovado).
			ini, fim := limitesDoDiaSP(d)
			dets, err := L.Veiculacoes.List(ctx, catalog.ListFilter{
				CampaignID: &c.ID, StationID: &eid, StartDate: &ini, EndDate: &fim, Limit: tetoTocadasDoDia,
			})
			if err != nil {
				return nil, err
			}
			for i := len(dets) - 1; i >= 0; i-- { // List vem da mais nova para a mais velha
				v := dets[i]
				if v.TypeID == nil || *v.TypeID != tid {
					continue
				}
				out.Veiculacoes = append(out.Veiculacoes, tocadaDoDia{
					ID: v.ID.String(), Quando: instante(v.DetectedAt), Material: refDe(v.CommercialID, v.CommercialName),
					Categoria: categoriaDe(v.Category), Manual: v.ManualAt != nil,
				})
			}
			if len(dets) == tetoTocadasDoDia {
				out.Ressalvas = append(out.Ressalvas, fmt.Sprintf(
					"o dia tem mais de %d veiculações nesta emissora; a lista foi cortada (o saldo continua completo)", tetoTocadasDoDia))
			}
			return ok(out)
		})
}
```

Em `servidor.go`: acrescente `"grade_campanha", "plano_do_dia",` a `ToolsEsperadas` (depois de `"listar_precos_campanha"`) e `registrarGrade(s, cfg)` depois de `registrarCampanhasDetalhe(s, cfg)`.

- [ ] **Step 4: Rode e veja passar**

```bash
cd C:/tmp/emonitor-mcp/workers && go vet ./internal/mcp/ && go test -p 1 ./internal/mcp/ -run 'TestGradeCampanha|TestPlanoDoDia|TestInventario' -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```
Expected: todos PASS.

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add workers/internal/mcp/ && git commit -m "feat(mcp): grade da campanha e plano do dia (saldo, meta e faixas do servidor)"
```

---

### Task 10: Materiais — `listar_materiais`, `obter_material`, `listar_tipos_material`

**Files:**
- Create: `workers/internal/mcp/tools_materiais.go`, `workers/internal/mcp/tools_materiais_test.go`
- Modify: `workers/internal/mcp/servidor.go`

**Fonte:** `GET /clients/{id}/materials?q` → `Materials.ListByClient`; `GET /materials/{id}` → `Materials.Get`; campanhas do material → `CampaignMaterials.ListByMaterial` (existe e ninguém chamava); `GET /material-types` → `MaterialTypes.List`. Limiar do aviso de duplicata = `similarity.WarnThreshold` (0,50). **Nunca** sai `master_storage_path` nem `master_sha256` (caminho de servidor e hash do arquivo).

- [ ] **Step 1: Testes**

`workers/internal/mcp/tools_materiais_test.go`:
```go
package mcp

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func TestListarMateriais_ParidadeSemCaminhoDoArquivo(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mcptest.Material(t, ctx, pool, cli, tipo, "Verão 30s")
	mcptest.Material(t, ctx, pool, cli, tipo, "Verão 15s")
	mcptest.Material(t, ctx, pool, cli, tipo, "Inverno 30s")

	h := &handlers.MaterialsHandler{Repo: catalog.NewMaterials(pool)}
	var tela struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.ListByClient, "/x?q=ver", map[string]string{"clientID": cli.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_materiais", map[string]any{"cliente_id": cli.String(), "busca": "ver"})
	var idsTela []string
	for _, m := range tela.Data {
		idsTela = append(idsTela, m.ID)
	}
	require.Equal(t, idsTela, ids(t, itens(t, out), "id"))
	require.Equal(t, tipo.String(), itens(t, out)[0]["tipo"].(map[string]any)["id"])

	texto, _ := chamarNoServidor(t, novoServidor(cfgTeste(pool), principalTeste), "listar_materiais", map[string]any{"cliente_id": cli.String()})
	require.NotContains(t, texto, "/tmp/mcp-", "caminho do master nunca sai")
	require.NotContains(t, texto, "master_sha256")
}

func TestObterMaterial_SimilaridadeECampanhas(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 0)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	original := mcptest.Material(t, ctx, pool, cli, tipo, "Original")
	copia := mcptest.Material(t, ctx, pool, cli, tipo, "Cópia")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	mcptest.Vincular(t, ctx, pool, camp, copia, est)
	_, err := pool.Exec(ctx, `
		UPDATE materials SET similarity_check_status = 'ready', most_similar_material_id = $1,
		       similarity_score = 0.8, script = 'Leia devagar'
		WHERE id = $2`, original, copia)
	require.NoError(t, err)

	out := chamarTool(t, cfgTeste(pool), "obter_material", map[string]any{"material_id": copia.String()})
	m := out["material"].(map[string]any)
	sim := m["similaridade"].(map[string]any)
	require.Equal(t, "Original", sim["parecido_com"].(map[string]any)["nome"])
	require.Equal(t, "Leia devagar", out["roteiro"])
	require.Equal(t, camp.String(), out["campanhas"].([]any)[0].(map[string]any)["campanha"].(map[string]any)["id"])
	require.NotEmpty(t, out["ressalvas"], "≥ 50% e não reconhecido: avisa")

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_material",
		map[string]any{"material_id": uuid.NewString()}), "[nao_encontrado]")
}

func TestListarTiposMaterial_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	h := &handlers.MaterialTypesHandler{Repo: catalog.NewMaterialTypes(pool)}
	var tela []catalog.MaterialType
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/material-types", nil, admin), &tela))
	out := chamarTool(t, cfgTeste(pool), "listar_tipos_material", map[string]any{"limit": 100})
	require.EqualValues(t, len(tela), pag(t, out)["total"])
	require.Equal(t, tela[0].Name, itens(t, out)[0]["nome"])
}
```

- [ ] **Step 2: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/mcp/ -run 'TestListarMateriais_|TestObterMaterial|TestListarTiposMaterial' 2>&1 | head
```
Expected: FAIL (tools inexistentes).

- [ ] **Step 3: Implemente**

`workers/internal/mcp/tools_materiais.go`:
```go
package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
	"radiocheck/internal/similarity"
)

type fingerprintDTO struct {
	Status   string  `json:"status"`
	GeradoEm *string `json:"gerado_em"`
	Hashes   *int32  `json:"hashes"`
}

type similaridadeDTO struct {
	Status        string   `json:"status"`
	Score         *float32 `json:"score"`
	ParecidoCom   *ref     `json:"parecido_com"`
	ReconhecidaEm *string  `json:"reconhecida_em"`
}

// materialDTO: whitelist — master_storage_path e master_sha256 nunca saem.
type materialDTO struct {
	ID           string          `json:"id"`
	ShortID      int32           `json:"short_id"`
	Titulo       string          `json:"titulo"`
	Tipo         *ref            `json:"tipo"`
	DuracaoS     float64         `json:"duracao_s"`
	Fingerprint  fingerprintDTO  `json:"fingerprint"`
	Similaridade similaridadeDTO `json:"similaridade"`
	CriadoEm     string          `json:"criado_em"`
}

// materialDe: titulos resolve o nome do material parecido (mesmo cliente).
func materialDe(m catalog.Material, tipos, titulos map[uuid.UUID]string) materialDTO {
	d := materialDTO{
		ID: m.ID.String(), ShortID: m.ShortID, Titulo: m.Title, DuracaoS: m.DurationSeconds,
		Fingerprint: fingerprintDTO{Status: m.FingerprintStatus, GeradoEm: instanteOpc(m.FingerprintGeneratedAt), Hashes: m.FingerprintHashCount},
		Similaridade: similaridadeDTO{Status: m.SimilarityCheckStatus, Score: m.SimilarityScore,
			ReconhecidaEm: instanteOpc(m.SimilarityAckdAt)},
		CriadoEm: instante(m.CreatedAt),
	}
	if m.TypeID != nil {
		r := refDe(*m.TypeID, tipos[*m.TypeID])
		d.Tipo = &r
	}
	if m.MostSimilarMaterialID != nil {
		r := refDe(*m.MostSimilarMaterialID, titulos[*m.MostSimilarMaterialID])
		d.Similaridade.ParecidoCom = &r
	}
	return d
}

type entradaListarMateriais struct {
	ClienteID string `json:"cliente_id" jsonschema:"Id (uuid) do cliente dono dos materiais, vindo de listar_clientes. Nunca invente: pergunte."`
	Busca     string `json:"busca,omitempty" jsonschema:"Trecho do título do material."`
	Paginacao
}

type entradaMaterial struct {
	MaterialID string `json:"material_id" jsonschema:"Id (uuid) do material, vindo de listar_materiais ou listar_materiais_campanha. Nunca invente: pergunte."`
}

type campanhaDoMaterial struct {
	Campanha  ref    `json:"campanha"`
	Estado    string `json:"estado"`
	Emissoras int    `json:"emissoras"`
}

type saidaObterMaterial struct {
	Material  materialDTO          `json:"material"`
	Cliente   ref                  `json:"cliente"`
	Roteiro   *string              `json:"roteiro"`
	Campanhas []campanhaDoMaterial `json:"campanhas"`
	Ressalvas []string             `json:"ressalvas,omitempty"`
}

type tipoMaterialDTO struct {
	ID        string  `json:"id"`
	Nome      string  `json:"nome"`
	Cor       string  `json:"cor"`
	Descricao *string `json:"descricao"`
}

func registrarMateriais(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "listar_materiais",
		"Lista os materiais (áudios de comercial) de um cliente, do mais novo para o mais velho: título, tipo, duração, estado "+
			"do fingerprint (pending, generating, ready, failed — só ready é detectado) e o aviso de similaridade com outro "+
			"material do mesmo cliente. Nunca invente o cliente_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaListarMateriais) (*sdk.CallToolResult, error) {
			c, rec, err := lerCliente(ctx, L, in.ClienteID)
			if rec != nil || err != nil {
				return rec, err
			}
			mats, err := L.Materiais.ListByClient(ctx, c.ID, in.Busca)
			if err != nil {
				return nil, err
			}
			titulos, err := nomesMateriais(ctx, L, c.ID)
			if err != nil {
				return nil, err
			}
			tipos, err := nomesTipos(ctx, L)
			if err != nil {
				return nil, err
			}
			return ok(mapear(paginar(mats, in.Paginacao), func(m catalog.Material) materialDTO {
				return materialDe(m, tipos, titulos)
			}))
		})

	ferramentaLeitura(s, cfg, "obter_material",
		"Devolve um material: título, tipo, duração, roteiro, estado do fingerprint, similaridade (com qual material e quanto) "+
			"e as campanhas em que está vinculado. A armadilha: dois materiais quase iguais na mesma emissora fazem a tocada "+
			"cair na campanha errada — quando a similaridade passa de 50% sem reconhecimento, vem ressalva. Nunca invente o material_id.",
		func(ctx context.Context, L *Leitura, in entradaMaterial) (*sdk.CallToolResult, error) {
			id, err := uuid.Parse(in.MaterialID)
			if err != nil {
				return recusaEntrada("material_id")
			}
			m, err := L.Materiais.Get(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return naoEncontrado("material")
			}
			if err != nil {
				return nil, err
			}
			titulos, err := nomesMateriais(ctx, L, m.ClientID)
			if err != nil {
				return nil, err
			}
			tipos, err := nomesTipos(ctx, L)
			if err != nil {
				return nil, err
			}
			clientes, err := nomesClientes(ctx, L)
			if err != nil {
				return nil, err
			}
			vinculos, err := L.MateriaisCampanha.ListByMaterial(ctx, m.ID)
			if err != nil {
				return nil, err
			}
			camps, err := todasCampanhas(ctx, L)
			if err != nil {
				return nil, err
			}
			out := saidaObterMaterial{
				Material: materialDe(*m, tipos, titulos), Cliente: refDe(m.ClientID, clientes[m.ClientID]),
				Roteiro: m.Script, Campanhas: make([]campanhaDoMaterial, 0, len(vinculos)),
			}
			for _, v := range vinculos {
				c := camps[v.CampaignID]
				out.Campanhas = append(out.Campanhas, campanhaDoMaterial{
					Campanha: refDe(v.CampaignID, c.Name), Estado: c.Status, Emissoras: len(v.TargetStations),
				})
			}
			if m.SimilarityScore != nil && *m.SimilarityScore >= similarity.WarnThreshold && m.SimilarityAckdAt == nil {
				parecido := ""
				if m.MostSimilarMaterialID != nil {
					parecido = titulos[*m.MostSimilarMaterialID]
				}
				out.Ressalvas = append(out.Ressalvas, fmt.Sprintf(
					"Aviso de duplicata não reconhecido: este material é %.0f%% parecido com %q. Os dois na mesma emissora em campanhas diferentes fazem a tocada ser atribuída à campanha errada.",
					*m.SimilarityScore*100, parecido))
			}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "listar_tipos_material",
		"Lista os tipos de material (spot 30s, spot 15s, vinheta, citação…), com id e cor. A grade e as regras de distribuição são "+
			"por TIPO: use o id daqui para ler grade_campanha e plano_do_dia. Lista paginada.",
		func(ctx context.Context, L *Leitura, in Paginacao) (*sdk.CallToolResult, error) {
			ts, err := L.TiposMaterial.List(ctx)
			if err != nil {
				return nil, err
			}
			return ok(mapear(paginar(ts, in), func(t catalog.MaterialType) tipoMaterialDTO {
				return tipoMaterialDTO{ID: t.ID.String(), Nome: t.Name, Cor: t.Color, Descricao: t.Description}
			}))
		})
}
```

Em `servidor.go`: acrescente `"listar_materiais", "obter_material", "listar_tipos_material",` a `ToolsEsperadas` (depois de `"plano_do_dia"`) e `registrarMateriais(s, cfg)` depois de `registrarGrade(s, cfg)`.

> Se `go build` reclamar de ciclo de import ou de peso com `radiocheck/internal/similarity`, confira com `go list -deps ./internal/mcp | grep similarity` — o pacote não importa `internal/mcp`, então não há ciclo. Não copie a constante 0,50 para cá.

- [ ] **Step 4: Rode e veja passar**

```bash
cd C:/tmp/emonitor-mcp/workers && go vet ./internal/mcp/ && go test -p 1 ./internal/mcp/ -run 'TestListarMateriais_|TestObterMaterial|TestListarTiposMaterial|TestInventario' -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```
Expected: todos PASS.

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add workers/internal/mcp/ && git commit -m "feat(mcp): tools de materiais — listar, obter (similaridade e campanhas) e tipos"
```

---

### Task 11: Emissoras — `buscar_emissoras`, `obter_emissora`

**Files:**
- Create: `workers/internal/mcp/tools_emissoras.go`, `workers/internal/mcp/tools_emissoras_test.go`
- Modify: `workers/internal/mcp/servidor.go`

**Fonte:** `GET /stations` → `Stations.List` (busca por tokens AND, campo OR — `station_search.go`); `GET /stations/{id}` → `Stations.Get`; Anatel e cidades no alcance → `Stations.Anatel`/`Stations.CoverageCities` (Task 4); campanhas na emissora → `Campaigns.ListFiltered(["ativa","programada"])` filtradas por `target_stations` (a mesma definição de "contratada" do CTE `contracted` do `Stations.List`). **Nunca** sai `stream_url`, `monitoring_status`, `health_status`, `last_health_check`, `consecutive_failures` nem threshold (não objetivo da spec §1: operação técnica).

- [ ] **Step 1: Testes**

`workers/internal/mcp/tools_emissoras_test.go`:
```go
package mcp

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func TestBuscarEmissoras_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	for _, n := range []string{"Rádio Alfa", "Rádio Beta", "Alfa News", "Gama FM"} {
		mcptest.Emissora(t, ctx, pool, n, 1000)
	}
	h := &handlers.StationsHandler{Repo: catalog.NewStations(pool)}
	var tela catalog.ListOutput
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/stations?q=alfa&page=1&limit=25", nil, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "buscar_emissoras", map[string]any{"busca": "alfa"})
	var idsTela []string
	for _, s := range tela.Data {
		idsTela = append(idsTela, s.ID.String())
	}
	require.Equal(t, idsTela, ids(t, itens(t, out), "id"))
	require.EqualValues(t, tela.Total, pag(t, out)["total"])

	texto, _ := chamarNoServidor(t, novoServidor(cfgTeste(pool), principalTeste), "buscar_emissoras", map[string]any{})
	require.NotContains(t, texto, "teste.mcp/", "stream_url nunca sai")
}

func TestBuscarEmissoras_ContratadaPor(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	dentro := mcptest.Emissora(t, ctx, pool, "Contratada", 0)
	mcptest.Emissora(t, ctx, pool, "Fora", 0)
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", dentro)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")

	out := chamarTool(t, cfgTeste(pool), "buscar_emissoras", map[string]any{"contratada_por": cli.String()})
	require.Equal(t, []string{dentro.String()}, ids(t, itens(t, out), "id"))
	require.Equal(t, true, itens(t, out)[0]["contrato"].(map[string]any)["no_ar"])
}

func TestObterEmissora_AnatelCidadesECampanhas(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio Goiás", 1500)
	_, err := pool.Exec(ctx, `
		UPDATE stations SET anatel_class = 'A4', anatel_coverage_km = 24, anatel_reach_km = 36, anatel_match_tier = 'exact'
		WHERE id = $1`, est)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO station_coverage_cities (station_id, ibge_code, city, state, distance_km, is_home, latitude, longitude)
		VALUES ($1, 5208707, 'Goiânia', 'GO', 0, true, -16.68, -49.25)`, est)
	require.NoError(t, err)
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")

	h := &handlers.StationsHandler{Repo: catalog.NewStations(pool)}
	var tela catalog.Station
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/x", map[string]string{"id": est.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "obter_emissora", map[string]any{"emissora_id": est.String()})
	e := out["emissora"].(map[string]any)
	require.Equal(t, tela.Name, e["nome"])
	require.EqualValues(t, *tela.PMM, e["pmm"])
	a := out["anatel"].(map[string]any)
	require.Equal(t, "A4", a["classe"])
	require.EqualValues(t, 36, a["alcance_km"])
	cid := out["cidades_no_alcance"].(map[string]any)
	require.EqualValues(t, 1, cid["total"])
	require.Equal(t, "Goiânia", cid["itens"].([]any)[0].(map[string]any)["cidade"])
	require.Equal(t, camp.String(), out["campanhas"].([]any)[0].(map[string]any)["campanha"].(map[string]any)["id"])
	require.NotEmpty(t, out["ressalvas"])

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_emissora",
		map[string]any{"emissora_id": uuid.NewString()}), "[nao_encontrado]")
}
```

- [ ] **Step 2: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/mcp/ -run 'TestBuscarEmissoras|TestObterEmissora' 2>&1 | head
```
Expected: FAIL (tools inexistentes).

- [ ] **Step 3: Implemente**

`workers/internal/mcp/tools_emissoras.go`:
```go
package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
)

type contratoDTO struct {
	Campanhas int     `json:"campanhas"`
	NoAr      bool    `json:"no_ar"`
	ComecaEm  *string `json:"comeca_em"`
}

// emissoraDTO: whitelist — stream_url, saúde do stream e threshold nunca saem.
type emissoraDTO struct {
	ID            string       `json:"id"`
	ShortID       int32        `json:"short_id"`
	Nome          string       `json:"nome"`
	Banda         string       `json:"banda"`
	FrequenciaMHz *float64     `json:"frequencia_mhz"`
	Cidade        *string      `json:"cidade"`
	UF            *string      `json:"uf"`
	PMM           *float64     `json:"pmm"`
	Latitude      *float64     `json:"latitude"`
	Longitude     *float64     `json:"longitude"`
	Contrato      *contratoDTO `json:"contrato"`
}

func emissoraDe(s catalog.Station) emissoraDTO {
	d := emissoraDTO{
		ID: s.ID.String(), ShortID: s.ShortID, Nome: s.Name, Banda: s.Band, FrequenciaMHz: s.FrequencyMHz,
		Cidade: s.City, UF: s.State, PMM: s.PMM, Latitude: s.Latitude, Longitude: s.Longitude,
	}
	if s.Contract != nil {
		d.Contrato = &contratoDTO{Campanhas: s.Contract.Campaigns, NoAr: s.Contract.OnAir, ComecaEm: instanteOpc(s.Contract.StartsAt)}
	}
	return d
}

type perfilEmissora struct {
	Categorias      []string                 `json:"categorias"`
	PerfilAudiencia *catalog.AudienceProfile `json:"perfil_audiencia"`
	PopulacaoTotal  *int64                   `json:"populacao_total"`
	Site            *string                  `json:"site"`
	AnoFundacao     *int                     `json:"ano_fundacao"`
	RazaoSocial     *string                  `json:"razao_social"`
	NomeFantasia    *string                  `json:"nome_fantasia"`
	CNPJ            *string                  `json:"cnpj"`
	EmailComercial  *string                  `json:"email_comercial"`
}

type anatelDTO struct {
	Classe              *string  `json:"classe"`
	ContornoProtegidoKm *float64 `json:"contorno_protegido_km"`
	AlcanceKm           *float64 `json:"alcance_km"`
	Procedencia         *string  `json:"procedencia"`
	CidadeDoPlano       *string  `json:"cidade_do_plano"`
	UFDoPlano           *string  `json:"uf_do_plano"`
	CruzadoEm           *string  `json:"cruzado_em"`
}

type cidadeNoAlcance struct {
	Cidade      string  `json:"cidade"`
	UF          string  `json:"uf"`
	IBGE        int     `json:"ibge"`
	DistanciaKm float64 `json:"distancia_km"`
	Sede        bool    `json:"sede"`
}

type cidadesNoAlcance struct {
	Total int               `json:"total"`
	Itens []cidadeNoAlcance `json:"itens"`
}

type campanhaNaEmissora struct {
	Campanha ref    `json:"campanha"`
	Cliente  ref    `json:"cliente"`
	Estado   string `json:"estado"`
	Inicio   string `json:"inicio"`
	Fim      string `json:"fim"`
}

type saidaObterEmissora struct {
	Emissora         emissoraDTO          `json:"emissora"`
	Perfil           *perfilEmissora      `json:"perfil"`
	Anatel           anatelDTO            `json:"anatel"`
	CidadesNoAlcance cidadesNoAlcance     `json:"cidades_no_alcance"`
	Campanhas        []campanhaNaEmissora `json:"campanhas"`
	Ressalvas        []string             `json:"ressalvas"`
}

type entradaBuscarEmissoras struct {
	Busca         string `json:"busca,omitempty" jsonschema:"Palavras (todas precisam casar): nome, cidade, UF, banda ou dial (ex.: 'jovem pan 100.9 sp')."`
	Banda         string `json:"banda,omitempty" jsonschema:"FM ou AM."`
	UF            string `json:"uf,omitempty" jsonschema:"Sigla do estado (ex.: SP)."`
	Cidade        string `json:"cidade,omitempty" jsonschema:"Nome exato da cidade (ignora acento)."`
	ContratadaPor string `json:"contratada_por,omitempty" jsonschema:"Id de cliente: só as emissoras de campanhas ativas ou programadas dele, com o resumo do contrato."`
	Paginacao
}

type entradaEmissora struct {
	EmissoraID string `json:"emissora_id" jsonschema:"Id (uuid) da emissora, vindo de buscar_emissoras. Nunca invente: pergunte."`
}

// tetoCidades: cidades no alcance devolvidas por obter_emissora (o total vem sempre).
const tetoCidades = 100

const ressalvaAnatel = "Anatel: contorno_protegido_km é o raio normativo da classe; alcance_km = contorno × 1,5 (transbordo) e é o raio que gera as cidades no alcance. null = indeterminado, nunca zero (AM não tem raio por classe). Classe C de FM ≠ classe C de AM."

func registrarEmissoras(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "buscar_emissoras",
		"Busca emissoras como a tela /stations: palavras que todas precisam casar (nome, cidade, UF, banda ou dial), mais filtros "+
			"de banda, UF e cidade. contratada_por (id de cliente) restringe às emissoras das campanhas ativas/programadas dele e "+
			"traz o resumo do contrato. Devolve id, nome, banda, dial, cidade/UF e PMM. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaBuscarEmissoras) (*sdk.CallToolResult, error) {
			var contratadas []uuid.UUID
			if in.ContratadaPor != "" {
				id, err := uuid.Parse(in.ContratadaPor)
				if err != nil {
					return recusaEntrada("contratada_por")
				}
				contratadas = []uuid.UUID{id}
			}
			pg, err := buscarJanela(in.Paginacao, func(page, size int) ([]catalog.Station, int, error) {
				res, err := L.Emissoras.List(ctx, catalog.ListInput{
					Q: in.Busca, Band: in.Banda, State: in.UF, City: in.Cidade, ContractedBy: contratadas,
					Page: page, Limit: size,
				})
				return res.Data, int(res.Total), err
			})
			if err != nil {
				return nil, err
			}
			return ok(mapear(pg, emissoraDe))
		})

	ferramentaLeitura(s, cfg, "obter_emissora",
		"Devolve uma emissora: cadastro, perfil (categorias, audiência, site, razão social), a classe Anatel com o contorno "+
			"protegido e o alcance (transbordo), as cidades no alcance (as 100 mais próximas + o total) e as campanhas ativas "+
			"ou programadas nela. A armadilha: raio null é indeterminado, não zero. Nunca invente o emissora_id.",
		func(ctx context.Context, L *Leitura, in entradaEmissora) (*sdk.CallToolResult, error) {
			id, err := uuid.Parse(in.EmissoraID)
			if err != nil {
				return recusaEntrada("emissora_id")
			}
			st, err := L.Emissoras.Get(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return naoEncontrado("emissora")
			}
			if err != nil {
				return nil, err
			}
			an, err := L.Emissoras.Anatel(ctx, id)
			if err != nil {
				return nil, err
			}
			cidades, err := L.Emissoras.CoverageCities(ctx, id)
			if err != nil {
				return nil, err
			}
			ativas, err := L.Campanhas.ListFiltered(ctx, []string{"ativa", "programada"}, nil)
			if err != nil {
				return nil, err
			}
			clientes, err := nomesClientes(ctx, L)
			if err != nil {
				return nil, err
			}
			out := saidaObterEmissora{
				Emissora: emissoraDe(*st),
				Anatel: anatelDTO{
					Classe: an.Class, ContornoProtegidoKm: an.CoverageKm, AlcanceKm: an.ReachKm, Procedencia: an.MatchTier,
					CidadeDoPlano: an.PlanCity, UFDoPlano: an.PlanState, CruzadoEm: instanteOpc(an.MatchedAt),
				},
				CidadesNoAlcance: cidadesNoAlcance{Total: len(cidades), Itens: []cidadeNoAlcance{}},
				Campanhas:        []campanhaNaEmissora{},
				Ressalvas:        []string{ressalvaAnatel},
			}
			if mt := st.Meta; mt != nil {
				out.Perfil = &perfilEmissora{
					Categorias: mt.Categories, PerfilAudiencia: mt.AudienceProfile, PopulacaoTotal: mt.TotalPopulation,
					Site: mt.Website, AnoFundacao: mt.FoundationYear, RazaoSocial: mt.CompanyName,
					NomeFantasia: mt.FantasyName, CNPJ: mt.CNPJ, EmailComercial: mt.CommercialEmail,
				}
			}
			for _, c := range cidades[:min(len(cidades), tetoCidades)] {
				out.CidadesNoAlcance.Itens = append(out.CidadesNoAlcance.Itens, cidadeNoAlcance{
					Cidade: c.City, UF: c.State, IBGE: c.IBGECode, DistanciaKm: c.DistanceKm, Sede: c.IsHome,
				})
			}
			if len(cidades) > tetoCidades {
				out.Ressalvas = append(out.Ressalvas, fmt.Sprintf(
					"mostrando as %d cidades mais próximas de %d no alcance", tetoCidades, len(cidades)))
			}
			for _, c := range ativas {
				if slices.Contains(c.TargetStations, id) {
					out.Campanhas = append(out.Campanhas, campanhaNaEmissora{
						Campanha: refDe(c.ID, c.Name), Cliente: refDe(c.ClientID, clientes[c.ClientID]),
						Estado: c.Status, Inicio: dia(c.StartDate), Fim: dia(c.EndDate),
					})
				}
			}
			return ok(out)
		})
}
```

Em `servidor.go`: acrescente `"buscar_emissoras", "obter_emissora",` a `ToolsEsperadas` (depois de `"listar_tipos_material"`) e `registrarEmissoras(s, cfg)` depois de `registrarMateriais(s, cfg)`.

> `perfil_audiencia` repassa `catalog.AudienceProfile` (chaves `gender`, `ageRanges`, `socialClass` em inglês, percentuais 0–100): é dado aninhado importado do Audiency, e traduzir cada chave seria só ruído. Fica registrado aqui para o revisor não estranhar.

- [ ] **Step 4: Rode e veja passar**

```bash
cd C:/tmp/emonitor-mcp/workers && go vet ./internal/mcp/ && go test -p 1 ./internal/mcp/ -run 'TestBuscarEmissoras|TestObterEmissora|TestInventario' -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```
Expected: todos PASS.

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add workers/internal/mcp/ && git commit -m "feat(mcp): tools de emissoras — busca da tela e ficha com Anatel, alcance e campanhas"
```

---

### Task 12: Veiculações — `listar_veiculacoes`, `obter_veiculacao`, `veiculacoes_por_material`

**Files:**
- Create: `workers/internal/mcp/tools_veiculacoes.go`, `workers/internal/mcp/tools_veiculacoes_test.go`
- Modify: `workers/internal/catalog/mcp_leituras.go`, `workers/internal/catalog/mcp_leituras_test.go` (`Detections.Approved`)
- Modify: `workers/internal/mcp/servidor.go`

**Fonte:** `GET /detections?page=…` → `Detections.ListPaged` (modo paginado, o de `/reports/airtime`; com os filtros da Task 4); `GET /detections/{id}` → `Detections.Get` (canônica, sem filtro de aprovação) + `Detections.Attributions` (Task 4) + `Detections.Approved` (abaixo, que aplica `catalog.ApprovedDetectionsFilter` — a definição canônica de "conta nas telas"); `GET /detections/aggregate-by-material` → `Detections.AggregateByMaterial`.

- [ ] **Step 1: Teste do `Approved` no catalog**

Acrescente a `workers/internal/catalog/mcp_leituras_test.go`:
```go
func TestDetections_Approved(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Aprov "+uuid.NewString()[:6])
	st := insSeedStationNoProfile(t, ctx, pool, "Aprov "+uuid.NewString()[:6])
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "aprov")
	camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	insSeedDetection(t, ctx, pool, camp, mat, st, "in_slot", "2026-10-10")
	var det uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM detections WHERE campaign_id = $1`, camp).Scan(&det))
	repo := NewDetections(pool)

	ok, err := repo.Approved(ctx, det)
	require.NoError(t, err)
	require.True(t, ok)

	_, err = pool.Exec(ctx, `UPDATE detections SET ignored_at = now() WHERE id = $1`, det)
	require.NoError(t, err)
	ok, err = repo.Approved(ctx, det)
	require.NoError(t, err)
	require.False(t, ok, "ignorada sai do conjunto aprovado")
}
```

- [ ] **Step 2: Implemente o `Approved`**

Acrescente a `workers/internal/catalog/mcp_leituras.go`:
```go
// Approved diz se a tocada está no conjunto aprovado — o que conta em toda
// tela (ApprovedDetectionsFilter). Get devolve a tocada mesmo fora dele.
func (d *Detections) Approved(ctx context.Context, id uuid.UUID) (bool, error) {
	var ok bool
	err := d.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM detections d WHERE d.id = $1 AND `+ApprovedDetectionsFilter+`)`, id).Scan(&ok)
	return ok, err
}
```
Rode: `go test -p 1 ./internal/catalog/ -run TestDetections_Approved -v` → PASS.

- [ ] **Step 3: Testes das tools**

`workers/internal/mcp/tools_veiculacoes_test.go`:
```go
package mcp

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func TestListarVeiculacoes_ParidadeEFiltros(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", e1, e2)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "orphan", mcptest.Quando("2026-10-10", "09:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e2, "out_slot", mcptest.Quando("2026-10-11", "23:00"))
	ignorada := mcptest.Veiculacao(t, ctx, pool, camp, mat, e2, "in_slot", mcptest.Quando("2026-10-12", "10:00"))
	_, err := pool.Exec(ctx, `UPDATE detections SET ignored_at = now() WHERE id = $1`, ignorada)
	require.NoError(t, err)

	h := &handlers.DetectionsHandler{Repo: catalog.NewDetections(pool)}
	var tela catalog.ListPagedResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/detections?page=1&page_size=25&campaigns="+camp.String(), nil, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_veiculacoes", map[string]any{"campanha_ids": []string{camp.String()}})
	var idsTela []string
	for _, d := range tela.Data {
		idsTela = append(idsTela, d.ID.String())
	}
	require.Equal(t, idsTela, ids(t, itens(t, out), "id"))
	require.EqualValues(t, tela.Total, pag(t, out)["total"])
	require.EqualValues(t, 3, pag(t, out)["total"], "a ignorada não conta")

	bonus := chamarTool(t, cfgTeste(pool), "listar_veiculacoes",
		map[string]any{"campanha_ids": []string{camp.String()}, "categorias": []string{"bonus"}})
	require.EqualValues(t, 1, pag(t, bonus)["total"], "bonus inclui o nome antigo orphan")
	require.Equal(t, "bonus", itens(t, bonus)[0]["categoria"])

	doE2 := chamarTool(t, cfgTeste(pool), "listar_veiculacoes",
		map[string]any{"campanha_ids": []string{camp.String()}, "emissora_id": e2.String()})
	require.EqualValues(t, 1, pag(t, doE2)["total"])

	periodo := chamarTool(t, cfgTeste(pool), "listar_veiculacoes", map[string]any{"de": "2026-10-11", "ate": "2026-10-11"})
	require.EqualValues(t, 1, pag(t, periodo)["total"], "o dia é o de America/Sao_Paulo")

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_veiculacoes", map[string]any{}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_veiculacoes",
		map[string]any{"campanha_ids": []string{camp.String()}, "categorias": []string{"orphan"}}), "[entrada_invalida]")
}

func TestObterVeiculacao_AtribuicoesEAprovacao(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	a := mcptest.Campanha(t, ctx, pool, cli, "Campanha A", "2026-10-01", "2026-10-31", est)
	b := mcptest.Campanha(t, ctx, pool, cli, "Campanha B", "2026-10-01", "2026-10-31", est)
	quando := mcptest.Quando("2026-10-10", "08:00")
	det := mcptest.Veiculacao(t, ctx, pool, a, mat, est, "in_slot", quando)
	mcptest.Projecao(t, ctx, pool, det, quando, b, mat, "bonus")

	h := &handlers.DetectionsHandler{Repo: catalog.NewDetections(pool)}
	var tela catalog.Detection
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/x", map[string]string{"id": det.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "obter_veiculacao", map[string]any{"veiculacao_id": det.String()})
	require.Equal(t, tela.Category, out["categoria"])
	require.Equal(t, tela.CampaignID.String(), out["campanha"].(map[string]any)["id"])
	require.EqualValues(t, tela.Confidence, out["confianca"])
	require.Len(t, out["atribuicoes"].([]any), 2)
	require.Equal(t, true, out["aprovada"])
	require.Equal(t, "https://portal.teste/detections/"+det.String(), out["links"].(map[string]any)["ouvir"])

	_, err := pool.Exec(ctx, `UPDATE detections SET ignored_at = now() WHERE id = $1`, det)
	require.NoError(t, err)
	ign := chamarTool(t, cfgTeste(pool), "obter_veiculacao", map[string]any{"veiculacao_id": det.String()})
	require.Equal(t, false, ign["aprovada"])
	require.NotEmpty(t, ign["ressalvas"])

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_veiculacao",
		map[string]any{"veiculacao_id": uuid.NewString()}), "[nao_encontrado]")
}

func TestVeiculacoesPorMaterial_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	m1 := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	m2 := mcptest.Material(t, ctx, pool, cli, tipo, "Spot B")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	for _, h := range []string{"08:00", "09:00", "10:00"} {
		mcptest.Veiculacao(t, ctx, pool, camp, m1, est, "in_slot", mcptest.Quando("2026-10-10", h))
	}
	mcptest.Veiculacao(t, ctx, pool, camp, m2, est, "in_slot", mcptest.Quando("2026-10-10", "11:00"))

	h := &handlers.DetectionsHandler{Repo: catalog.NewDetections(pool)}
	var tela catalog.MaterialAggregateResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.AggregateByMaterial, "/x?campaigns="+camp.String(), nil, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "veiculacoes_por_material", map[string]any{"campanha_ids": []string{camp.String()}})
	require.EqualValues(t, tela.TotalDetections, out["total_veiculacoes"])
	require.EqualValues(t, tela.DistinctMaterials, out["materiais_distintos"])
	l := itens(t, out)
	require.Equal(t, tela.Data[0].MaterialID.String(), l[0]["material"].(map[string]any)["id"])
	require.EqualValues(t, tela.Data[0].Count, l[0]["veiculacoes"])
}
```

- [ ] **Step 4: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/mcp/ -run 'TestListarVeiculacoes|TestObterVeiculacao|TestVeiculacoesPorMaterial' 2>&1 | head
```
Expected: FAIL (tools inexistentes).

- [ ] **Step 5: Implemente**

`workers/internal/mcp/tools_veiculacoes.go`:
```go
package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
	"radiocheck/internal/categorizer"
)

type veiculacaoDTO struct {
	ID        string  `json:"id"`
	Quando    string  `json:"quando"`
	Emissora  ref     `json:"emissora"`
	Material  ref     `json:"material"`
	Tipo      *string `json:"tipo"`
	Campanha  ref     `json:"campanha"`
	Cliente   *ref    `json:"cliente"`
	Categoria string  `json:"categoria"`
	Manual    bool    `json:"manual"`
}

func veiculacaoDe(d catalog.DetectionEnriched) veiculacaoDTO {
	v := veiculacaoDTO{
		ID: d.ID.String(), Quando: instante(d.DetectedAt), Emissora: refDe(d.StationID, d.StationName),
		Material: refDe(d.CommercialID, d.CommercialName), Tipo: d.MaterialTypeName,
		Campanha: ref{ID: d.CampaignID.String()}, Categoria: categoriaDe(d.Category), Manual: d.ManualAt != nil,
	}
	if d.CampaignName != nil {
		v.Campanha.Nome = *d.CampaignName
	}
	if d.ClientID != nil {
		r := ref{ID: d.ClientID.String()}
		if d.ClientName != nil {
			r.Nome = *d.ClientName
		}
		v.Cliente = &r
	}
	return v
}

type entradaListarVeiculacoes struct {
	CampanhaIDs         []string `json:"campanha_ids,omitempty" jsonschema:"Até 50 campanhas (ids de listar_campanhas). Passe campanhas ou o período de/ate."`
	EmissoraID          string   `json:"emissora_id,omitempty" jsonschema:"Só desta emissora."`
	MaterialID          string   `json:"material_id,omitempty" jsonschema:"Só deste material."`
	Categorias          []string `json:"categorias,omitempty" jsonschema:"in_slot, bonus, out_slot, out_date. Vazio = todas."`
	De                  string   `json:"de,omitempty" jsonschema:"Primeiro dia (AAAA-MM-DD, America/Sao_Paulo)."`
	Ate                 string   `json:"ate,omitempty" jsonschema:"Último dia (AAAA-MM-DD, America/Sao_Paulo)."`
	Busca               string   `json:"busca,omitempty" jsonschema:"Até 4 palavras; cada uma tem de aparecer em emissora, cidade, UF, banda, dial, material, tipo ou cliente."`
	MaisAntigasPrimeiro bool     `json:"mais_antigas_primeiro,omitempty" jsonschema:"true para ordem cronológica (padrão: mais recentes primeiro)."`
	Paginacao
}

type entradaVeiculacao struct {
	VeiculacaoID string `json:"veiculacao_id" jsonschema:"Id (uuid) da veiculação, vindo de listar_veiculacoes ou plano_do_dia. Nunca invente: pergunte."`
}

type atribuicaoDTO struct {
	Campanha  ref    `json:"campanha"`
	Material  ref    `json:"material"`
	Categoria string `json:"categoria"`
}

type evidenciaDTO struct {
	Status       string `json:"status"`
	TamanhoBytes *int64 `json:"tamanho_bytes"`
}

type manualDTO struct {
	Em   string  `json:"em"`
	Por  *string `json:"por"`
	Nota *string `json:"nota"`
}

type linksVeiculacao struct {
	Ouvir *string `json:"ouvir"`
}

type saidaObterVeiculacao struct {
	ID                string          `json:"id"`
	Quando            string          `json:"quando"`
	Emissora          ref             `json:"emissora"`
	Material          ref             `json:"material"`
	Campanha          ref             `json:"campanha"`
	Categoria         string          `json:"categoria"`
	Aprovada          bool            `json:"aprovada"`
	Atribuicoes       []atribuicaoDTO `json:"atribuicoes"`
	Confianca         float64         `json:"confianca"`
	CoberturaTemporal *float64        `json:"cobertura_temporal"`
	CoberturaAudit    *float64        `json:"cobertura_audit"`
	Hashes            int32           `json:"hashes"`
	Evidencia         evidenciaDTO    `json:"evidencia"`
	Manual            *manualDTO      `json:"manual"`
	IgnoradaEm        *string         `json:"ignorada_em"`
	RetratadaEm       *string         `json:"retratada_em"`
	Roteiro           *string         `json:"roteiro"`
	Links             linksVeiculacao `json:"links"`
	Ressalvas         []string        `json:"ressalvas,omitempty"`
}

type entradaPorMaterial struct {
	CampanhaIDs []string `json:"campanha_ids" jsonschema:"De 1 a 50 campanhas (ids de listar_campanhas). Nunca invente: pergunte."`
	De          string   `json:"de,omitempty" jsonschema:"Primeiro dia (AAAA-MM-DD, America/Sao_Paulo)."`
	Ate         string   `json:"ate,omitempty" jsonschema:"Último dia (AAAA-MM-DD, America/Sao_Paulo)."`
	Busca       string   `json:"busca,omitempty" jsonschema:"Até 4 palavras, como em listar_veiculacoes."`
	Paginacao
}

type linhaPorMaterial struct {
	Material    ref      `json:"material"`
	ShortID     *int32   `json:"short_id"`
	DuracaoS    *float64 `json:"duracao_s"`
	Tipo        *ref     `json:"tipo"`
	Veiculacoes int      `json:"veiculacoes"`
}

type saidaPorMaterial struct {
	lista[linhaPorMaterial]
	TotalVeiculacoes   int `json:"total_veiculacoes"`
	MateriaisDistintos int `json:"materiais_distintos"`
}

const tetoCampanhasPorConsulta = 50

var categoriasValidas = []string{categorizer.CatInSlot, categorizer.CatBonus, categorizer.CatOutSlot, categorizer.CatOutDate}

const ressalvaVeiculacoes = "Só veiculações aprovadas (fora as retratadas, ignoradas e rejeitadas pelo audit). Uma tocada atribuída a N campanhas aparece N vezes, uma por campanha, cada uma com a própria categoria."

// lerCampanhas valida 0..teto ids; vazio devolve nil (sem filtro).
func lerCampanhas(ss []string) ([]uuid.UUID, bool) {
	if len(ss) > tetoCampanhasPorConsulta {
		return nil, false
	}
	var out []uuid.UUID
	for _, s := range ss {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

// lerPeriodoSP converte de/ate (dias de SP) em instantes; vazio = sem limite.
func lerPeriodoSP(de, ate string) (*time.Time, *time.Time, string) {
	var ini, fim *time.Time
	if de != "" {
		d, ok := lerDia(de)
		if !ok {
			return nil, nil, "de"
		}
		i, _ := limitesDoDiaSP(d)
		ini = &i
	}
	if ate != "" {
		d, ok := lerDia(ate)
		if !ok {
			return nil, nil, "ate"
		}
		_, f := limitesDoDiaSP(d)
		fim = &f
	}
	return ini, fim, ""
}

func registrarVeiculacoes(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "listar_veiculacoes",
		"Lista veiculações (tocadas) como o relatório Data e Hora: quando, emissora, material, tipo, campanha, cliente e "+
			"categoria (in_slot, bonus, out_slot, out_date). Filtros: campanha_ids, emissora_id, material_id, categorias, "+
			"de/ate (dias de America/Sao_Paulo), busca. Pede campanhas ou período. A armadilha: só conta o conjunto aprovado, "+
			"e uma tocada com várias campanhas aparece uma vez por campanha. Lista paginada com total real.",
		func(ctx context.Context, L *Leitura, in entradaListarVeiculacoes) (*sdk.CallToolResult, error) {
			camps, okC := lerCampanhas(in.CampanhaIDs)
			if !okC {
				return recusa("campanha_ids: até 50 ids válidos", "entrada_invalida")
			}
			ini, fim, campoRuim := lerPeriodoSP(in.De, in.Ate)
			if campoRuim != "" {
				return recusaEntrada(campoRuim)
			}
			if len(camps) == 0 && (ini == nil || fim == nil) {
				return recusa("passe campanha_ids ou o período completo (de e ate)", "entrada_invalida")
			}
			f := catalog.ListPagedFilter{CampaignIDs: camps, StartDate: ini, EndDate: fim, Q: in.Busca}
			if in.MaisAntigasPrimeiro {
				f.Sort = "detected_at_asc"
			}
			if in.EmissoraID != "" {
				id, err := uuid.Parse(in.EmissoraID)
				if err != nil {
					return recusaEntrada("emissora_id")
				}
				f.StationID = &id
			}
			if in.MaterialID != "" {
				id, err := uuid.Parse(in.MaterialID)
				if err != nil {
					return recusaEntrada("material_id")
				}
				f.MaterialID = &id
			}
			for _, c := range in.Categorias {
				if !slices.Contains(categoriasValidas, c) {
					return recusa(fmt.Sprintf("categoria desconhecida %q: use in_slot, bonus, out_slot ou out_date", c), "entrada_invalida")
				}
				f.Categories = append(f.Categories, c)
				if c == categorizer.CatBonus {
					f.Categories = append(f.Categories, categorizer.CatOrphan) // nome antigo de bonus
				}
			}
			pg, err := buscarJanela(in.Paginacao, func(page, size int) ([]catalog.DetectionEnriched, int, error) {
				f.Page, f.PageSize = page, size
				res, err := L.Veiculacoes.ListPaged(ctx, f)
				if err != nil {
					return nil, 0, err
				}
				return res.Data, res.Total, nil
			})
			if err != nil {
				return nil, err
			}
			out := mapear(pg, veiculacaoDe)
			out.Ressalvas = []string{ressalvaVeiculacoes}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "obter_veiculacao",
		"Devolve uma veiculação: quando, emissora, material, a campanha e a categoria canônicas, TODAS as campanhas a que ela "+
			"conta (atribuicoes, cada uma com a própria categoria), se está no conjunto aprovado, confiança e cobertura da "+
			"detecção, estado da evidência, se foi lançada à mão, e o link para ouvir no portal (o áudio nunca vem aqui). "+
			"Nunca invente o veiculacao_id.",
		func(ctx context.Context, L *Leitura, in entradaVeiculacao) (*sdk.CallToolResult, error) {
			id, err := uuid.Parse(in.VeiculacaoID)
			if err != nil {
				return recusaEntrada("veiculacao_id")
			}
			d, err := L.Veiculacoes.Get(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return naoEncontrado("veiculação")
			}
			if err != nil {
				return nil, err
			}
			atribs, err := L.Veiculacoes.Attributions(ctx, id)
			if err != nil {
				return nil, err
			}
			aprovada, err := L.Veiculacoes.Approved(ctx, id)
			if err != nil {
				return nil, err
			}
			out := saidaObterVeiculacao{
				ID: d.ID.String(), Quando: instante(d.DetectedAt), Emissora: refDe(d.StationID, d.StationName),
				Material: refDe(d.CommercialID, d.CommercialName), Campanha: ref{ID: d.CampaignID.String()},
				Categoria: categoriaDe(d.Category), Aprovada: aprovada, Atribuicoes: make([]atribuicaoDTO, 0, len(atribs)),
				Confianca: d.Confidence, CoberturaTemporal: d.TemporalCoverage, CoberturaAudit: d.AuditCoverage,
				Hashes: d.HashCount, Evidencia: evidenciaDTO{Status: d.EvidenceStatus, TamanhoBytes: d.EvidenceSizeBytes},
				IgnoradaEm: instanteOpc(d.IgnoredAt), RetratadaEm: instanteOpc(d.RetractedAt), Roteiro: d.CommercialScript,
				Links: linksVeiculacao{Ouvir: cfg.link("/detections/" + d.ID.String())},
			}
			for _, a := range atribs {
				if a.CampaignID == d.CampaignID {
					out.Campanha.Nome = a.CampaignName
				}
				out.Atribuicoes = append(out.Atribuicoes, atribuicaoDTO{
					Campanha: refDe(a.CampaignID, a.CampaignName), Material: refDe(a.CommercialID, a.CommercialName),
					Categoria: categoriaDe(a.Category),
				})
			}
			if d.ManualAt != nil {
				m := &manualDTO{Em: instante(*d.ManualAt), Nota: d.ManualNote}
				if d.ManualBy != nil {
					s := d.ManualBy.String()
					m.Por = &s
				}
				out.Manual = m
			}
			if !aprovada {
				out.Ressalvas = append(out.Ressalvas,
					"Esta veiculação está FORA do conjunto aprovado (ignorada, retratada ou rejeitada pelo audit): não conta em nenhuma tela nem relatório.")
			}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "veiculacoes_por_material",
		"Conta as veiculações por material (as mesmas do relatório Data e Hora, conjunto aprovado) em 1 a 50 campanhas, com "+
			"filtro opcional de período (dias de America/Sao_Paulo) e busca. Devolve cada material com a contagem, mais o total "+
			"e quantos materiais distintos tocaram. Nunca some você: o total vem pronto. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaPorMaterial) (*sdk.CallToolResult, error) {
			camps, okC := lerCampanhas(in.CampanhaIDs)
			if !okC || len(camps) == 0 {
				return recusa("campanha_ids: de 1 a 50 ids válidos", "entrada_invalida")
			}
			ini, fim, campoRuim := lerPeriodoSP(in.De, in.Ate)
			if campoRuim != "" {
				return recusaEntrada(campoRuim)
			}
			res, err := L.Veiculacoes.AggregateByMaterial(ctx, catalog.AggregateFilter{
				CampaignIDs: camps, StartDate: ini, EndDate: fim, Q: in.Busca,
			})
			if err != nil {
				return nil, err
			}
			linhas := make([]linhaPorMaterial, len(res.Data))
			for i, r := range res.Data {
				linhas[i] = linhaPorMaterial{
					Material: refDe(r.MaterialID, r.MaterialTitle), ShortID: r.MaterialShortID,
					DuracaoS: r.MaterialDurationSec, Veiculacoes: r.Count,
				}
				if r.MaterialTypeID != nil {
					t := ref{ID: r.MaterialTypeID.String()}
					if r.MaterialTypeName != nil {
						t.Nome = *r.MaterialTypeName
					}
					linhas[i].Tipo = &t
				}
			}
			out := saidaPorMaterial{lista: paginar(linhas, in.Paginacao),
				TotalVeiculacoes: res.TotalDetections, MateriaisDistintos: res.DistinctMaterials}
			out.Ressalvas = []string{ressalvaVeiculacoes}
			return ok(out)
		})
}
```

Em `servidor.go`: acrescente `"listar_veiculacoes", "obter_veiculacao", "veiculacoes_por_material",` a `ToolsEsperadas` (depois de `"obter_emissora"`) e `registrarVeiculacoes(s, cfg)` depois de `registrarEmissoras(s, cfg)`.

- [ ] **Step 6: Rode e veja passar**

```bash
cd C:/tmp/emonitor-mcp/workers && go vet ./internal/mcp/ ./internal/catalog/ && go test -p 1 ./internal/mcp/ -run 'TestListarVeiculacoes|TestObterVeiculacao|TestVeiculacoesPorMaterial|TestInventario' -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```
Expected: todos PASS. Atenção ao `periodo` do `TestListarVeiculacoes`: 23:00 de 11/10 em SP é 02:00 de 12/10 em UTC — o teste prova que o recorte é por dia de SP; se ele falhar, o erro está em `limitesDoDiaSP`, não no teste.

- [ ] **Step 7: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add workers/internal/mcp/ workers/internal/catalog/ && git commit -m "feat(mcp): tools de veiculação — listar com total real, obter com atribuições e por material"
```

---

### Task 13: Usuários — `listar_usuarios`, `obter_usuario`

**Files:**
- Modify: `workers/internal/mcp/tools_usuarios.go`
- Create: `workers/internal/mcp/tools_usuarios_test.go`
- Modify: `workers/internal/mcp/servidor.go`

**Fonte:** `GET /admin/users` → `users.Repo.List` (`handlers/users.go:80`); `GET /admin/users/{id}` → `users.Repo.Get`; convite de boas-vindas → `welcome.Repo.Latest` (o que o handler usa para `welcome_invites`). **Nunca** sai o `token` nem o `link` do convite: o link `/boasvindas/:token` mostra a senha inicial em claro e não expira. Sai só o estado do convite.

- [ ] **Step 1: Testes**

`workers/internal/mcp/tools_usuarios_test.go`:
```go
package mcp

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/users"
)

func TestListarUsuarios_ParidadeSemTokenDeBoasVindas(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	mcptest.Usuario(t, ctx, pool, "operator", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	var viewer uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role, name, is_active, client_id)
		VALUES ('cliente@mcp.teste', 'x', 'viewer', 'Pessoa do Cliente', true, $1) RETURNING id`, cli).Scan(&viewer))
	_, err := pool.Exec(ctx, `
		INSERT INTO user_welcome_invites (user_id, token, email_status, open_count)
		VALUES ($1, 'TOKEN-SECRETO-NAO-PODE-SAIR', 'sent', 2)`, viewer)
	require.NoError(t, err)

	h := handlers.NewUsersHandler(users.NewRepo(pool), nil)
	var tela struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Total int `json:"total"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/admin/users?page=1&page_size=25", nil, admin), &tela))
	out := chamarTool(t, cfgTeste(pool), "listar_usuarios", map[string]any{})
	var idsTela []string
	for _, u := range tela.Data {
		idsTela = append(idsTela, u.ID)
	}
	require.Equal(t, idsTela, ids(t, itens(t, out), "id"))
	require.EqualValues(t, tela.Total, pag(t, out)["total"])

	soClientes := chamarTool(t, cfgTeste(pool), "listar_usuarios", map[string]any{"papel": "cliente"})
	u := itens(t, soClientes)[0]
	require.Equal(t, "cliente", u["papel"])
	require.Equal(t, "Cliente", u["clientes"].([]any)[0].(map[string]any)["nome"])
	bv := u["boas_vindas"].(map[string]any)
	require.Equal(t, "sent", bv["status_email"])
	require.EqualValues(t, 2, bv["aberturas"])

	texto, _ := chamarNoServidor(t, novoServidor(cfgTeste(pool), principalTeste), "listar_usuarios", map[string]any{})
	require.NotContains(t, texto, "TOKEN-SECRETO", "o token de boas-vindas abre a senha inicial: nunca sai")
	require.NotContains(t, texto, "boasvindas")

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_usuarios", map[string]any{"papel": "dono"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_usuarios", map[string]any{"situacao": "talvez"}), "[entrada_invalida]")
}

func TestObterUsuario(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	op := mcptest.Usuario(t, ctx, pool, "operator", false)
	out := chamarTool(t, cfgTeste(pool), "obter_usuario", map[string]any{"usuario_id": op.String()})
	require.Equal(t, "operator", out["usuario"].(map[string]any)["papel"])
	require.Equal(t, "inativo", out["usuario"].(map[string]any)["situacao"])
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_usuario",
		map[string]any{"usuario_id": uuid.NewString()}), "[nao_encontrado]")
}
```

- [ ] **Step 2: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/mcp/ -run 'TestListarUsuarios|TestObterUsuario' 2>&1 | head
```
Expected: FAIL (tools inexistentes).

- [ ] **Step 3: Implemente**

`workers/internal/mcp/tools_usuarios.go` (substitui o conteúdo da Task 6, mantendo `papelDe` e `situacaoDe`):
```go
package mcp

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/users"
	"radiocheck/internal/welcome"
)

// papelDe traduz o papel do banco para o vocabulário da tela: viewer é "cliente".
func papelDe(role string) string {
	if role == "viewer" {
		return "cliente"
	}
	return role
}

// situacaoDe: apagado (soft delete) > inativo > ativo.
func situacaoDe(u users.User) string {
	switch {
	case u.DeletedAt != nil:
		return "apagado"
	case !u.IsActive:
		return "inativo"
	default:
		return "ativo"
	}
}

// papeisDoFiltro: o mesmo de-para do parseRoleFilter do handler (cliente = viewer).
var papeisDoFiltro = map[string]string{
	"admin": "admin", "operator": "operator", "operador": "operator", "cliente": "viewer", "viewer": "viewer",
}

// situacoesDoFiltro: vocabulário da tool → status do users.Repo.List.
var situacoesDoFiltro = map[string]string{
	"": "", "ativos": "active", "inativos": "inactive", "apagados": "deleted", "todos": "all",
}

// boasVindasDTO: estado do convite. token e link NUNCA (abrem a senha inicial).
type boasVindasDTO struct {
	StatusEmail string  `json:"status_email"`
	AbertoEm    *string `json:"aberto_em"`
	Aberturas   int     `json:"aberturas"`
	RevogadoEm  *string `json:"revogado_em"`
	CriadoEm    string  `json:"criado_em"`
}

type usuarioDTO struct {
	ID             string         `json:"id"`
	Nome           string         `json:"nome"`
	Email          string         `json:"email"`
	Papel          string         `json:"papel"`
	Situacao       string         `json:"situacao"`
	Clientes       []ref          `json:"clientes"`
	UltimoLogin    *string        `json:"ultimo_login"`
	CriadoEm       string         `json:"criado_em"`
	RecebeAlertas  bool           `json:"recebe_alertas"`
	RecebePosVenda bool           `json:"recebe_pos_venda"`
	BoasVindas     *boasVindasDTO `json:"boas_vindas"`
}

func usuarioDe(u users.User, clientes map[uuid.UUID]string, convites map[uuid.UUID]welcome.Invite) usuarioDTO {
	d := usuarioDTO{
		ID: u.ID.String(), Nome: u.Name, Email: u.Email, Papel: papelDe(u.Role), Situacao: situacaoDe(u),
		Clientes: refs(u.ClientIDs, clientes), UltimoLogin: instanteOpc(u.LastLoginAt), CriadoEm: instante(u.CreatedAt),
		RecebeAlertas: u.ReceiveAlertEmails, RecebePosVenda: u.ReceivePostSaleEmails,
	}
	if inv, ok := convites[u.ID]; ok {
		d.BoasVindas = &boasVindasDTO{
			StatusEmail: inv.EmailStatus, AbertoEm: instanteOpc(inv.OpenedAt), Aberturas: inv.OpenCount,
			RevogadoEm: instanteOpc(inv.RevokedAt), CriadoEm: instante(inv.CreatedAt),
		}
	}
	return d
}

type entradaListarUsuarios struct {
	Busca     string `json:"busca,omitempty" jsonschema:"Trecho do nome ou do e-mail."`
	Papel     string `json:"papel,omitempty" jsonschema:"admin, operator ou cliente."`
	ClienteID string `json:"cliente_id,omitempty" jsonschema:"Só quem tem este cliente na carteira (inclusive vínculo secundário de agência)."`
	Situacao  string `json:"situacao,omitempty" jsonschema:"ativos (padrão), inativos, apagados ou todos."`
	Paginacao
}

type entradaUsuario struct {
	UsuarioID string `json:"usuario_id" jsonschema:"Id (uuid) do usuário, vindo de listar_usuarios. Nunca invente: pergunte."`
}

type saidaObterUsuario struct {
	Usuario usuarioDTO `json:"usuario"`
}

func registrarUsuarios(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "listar_usuarios",
		"Lista os usuários do E-monitor (a tela /admin/users), do mais novo para o mais velho: nome, e-mail, papel (admin, "+
			"operator, cliente), situação, clientes da carteira, último login e o estado do convite de boas-vindas (enviado, "+
			"aberto, revogado). Filtros: busca, papel, cliente_id, situacao. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaListarUsuarios) (*sdk.CallToolResult, error) {
			papel, okP := papeisDoFiltro[in.Papel]
			if in.Papel != "" && !okP {
				return recusa("papel desconhecido: use admin, operator ou cliente", "entrada_invalida")
			}
			situacao, okS := situacoesDoFiltro[in.Situacao]
			if !okS {
				return recusa("situacao desconhecida: use ativos, inativos, apagados ou todos", "entrada_invalida")
			}
			var cliente *uuid.UUID
			if in.ClienteID != "" {
				id, err := uuid.Parse(in.ClienteID)
				if err != nil {
					return recusaEntrada("cliente_id")
				}
				cliente = &id
			}
			pg, err := buscarJanela(in.Paginacao, func(page, size int) ([]users.User, int, error) {
				return L.Usuarios.List(ctx, users.ListInput{
					Role: papel, ClientID: cliente, Status: situacao, Q: in.Busca, Page: page, PageSize: size,
				})
			})
			if err != nil {
				return nil, err
			}
			ids := make([]uuid.UUID, len(pg.Items))
			for i, u := range pg.Items {
				ids[i] = u.ID
			}
			convites, err := L.BoasVindas.Latest(ctx, ids)
			if err != nil {
				return nil, err
			}
			clientes, err := nomesClientes(ctx, L)
			if err != nil {
				return nil, err
			}
			return ok(mapear(pg, func(u users.User) usuarioDTO { return usuarioDe(u, clientes, convites) }))
		})

	ferramentaLeitura(s, cfg, "obter_usuario",
		"Devolve um usuário: papel, situação, clientes da carteira, preferências de e-mail, último login e o estado do convite "+
			"de boas-vindas. O link e a senha inicial nunca saem por aqui. Nunca invente o usuario_id.",
		func(ctx context.Context, L *Leitura, in entradaUsuario) (*sdk.CallToolResult, error) {
			id, err := uuid.Parse(in.UsuarioID)
			if err != nil {
				return recusaEntrada("usuario_id")
			}
			u, err := L.Usuarios.Get(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return naoEncontrado("usuário")
			}
			if err != nil {
				return nil, err
			}
			convites, err := L.BoasVindas.Latest(ctx, []uuid.UUID{id})
			if err != nil {
				return nil, err
			}
			clientes, err := nomesClientes(ctx, L)
			if err != nil {
				return nil, err
			}
			return ok(saidaObterUsuario{Usuario: usuarioDe(*u, clientes, convites)})
		})
}
```

Em `servidor.go`: acrescente `"listar_usuarios", "obter_usuario",` a `ToolsEsperadas` (depois de `"veiculacoes_por_material"`) e `registrarUsuarios(s, cfg)` depois de `registrarVeiculacoes(s, cfg)`.

- [ ] **Step 4: Rode e veja passar**

```bash
cd C:/tmp/emonitor-mcp/workers && go vet ./internal/mcp/ && go test -p 1 ./internal/mcp/ -run 'TestListarUsuarios|TestObterUsuario|TestObterCliente|TestInventario' -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```
Expected: todos PASS.

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add workers/internal/mcp/ && git commit -m "feat(mcp): tools de usuários — listar e obter, com estado do convite e sem o token"
```

---

### Task 14: Integrações e notificações

**Files:**
- Create: `workers/internal/mcp/tools_integracoes.go`, `workers/internal/mcp/tools_integracoes_test.go`
- Modify: `workers/internal/mcp/servidor.go`

**Fonte:** `GET /clients/{id}/webhook` → `Clients.GetWebhookConfig`; `…/webhook-deliveries` → `Clients.ListWebhookDeliveries` (Task 5); `GET /clients/{id}/api-keys` → `Clients.ListAPIKeys` (Task 5); `GET /admin/notifications` → `Notifications.List(ctx, userID)` com o **dono da credencial** (o estado lido/não lido é por pessoa). **Nunca** sai o segredo do webhook — nem o prefixo de 4 letras que a tela mostra.

- [ ] **Step 1: Testes**

`workers/internal/mcp/tools_integracoes_test.go`:
```go
package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func TestObterWebhookCliente_SemSegredo(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	_, err := pool.Exec(ctx, `
		UPDATE clients SET webhook_url = 'https://cliente.example/hook', webhook_secret = 'zq9xSEGREDO123',
		       webhook_enabled = true, webhook_events = ARRAY['detection.confirmed']
		WHERE id = $1`, cli)
	require.NoError(t, err)

	h := handlers.NewWebhooksHandler(pool, catalog.NewClients(pool), nil)
	var tela struct {
		URL       string   `json:"webhook_url"`
		Enabled   bool     `json:"webhook_enabled"`
		Events    []string `json:"webhook_events"`
		HasSecret bool     `json:"has_secret"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.GetConfig, "/x", map[string]string{"id": cli.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "obter_webhook_cliente", map[string]any{"cliente_id": cli.String()})
	require.Equal(t, true, out["configurado"])
	require.Equal(t, tela.URL, out["url"])
	require.Equal(t, tela.Enabled, out["ativo"])
	require.Equal(t, tela.HasSecret, out["tem_segredo"])

	texto, _ := chamarNoServidor(t, novoServidor(cfgTeste(pool), principalTeste), "obter_webhook_cliente", map[string]any{"cliente_id": cli.String()})
	require.NotContains(t, texto, "zq9x", "nem o prefixo do segredo sai")
}

func TestListarEntregasWebhook_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	for i, st := range []string{"delivered", "failed", "delivered"} {
		_, err := pool.Exec(ctx, `
			INSERT INTO webhook_deliveries (client_id, event_type, payload, status, attempt_count, created_at)
			VALUES ($1, 'detection.confirmed', '{"segredo":"payload-nao-sai"}'::jsonb, $2, 1, now() - make_interval(mins => $3))`,
			cli, st, i)
		require.NoError(t, err)
	}
	h := handlers.NewWebhooksHandler(pool, catalog.NewClients(pool), nil)
	var tela struct {
		Data []catalog.WebhookDelivery `json:"data"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.ListDeliveries, "/x", map[string]string{"id": cli.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_entregas_webhook", map[string]any{"cliente_id": cli.String()})
	require.EqualValues(t, len(tela.Data), pag(t, out)["total"])
	require.Equal(t, tela.Data[0].ID.String(), itens(t, out)[0]["id"])

	falhas := chamarTool(t, cfgTeste(pool), "listar_entregas_webhook", map[string]any{"cliente_id": cli.String(), "status": "failed"})
	require.EqualValues(t, 1, pag(t, falhas)["total"])

	texto, _ := chamarNoServidor(t, novoServidor(cfgTeste(pool), principalTeste), "listar_entregas_webhook", map[string]any{"cliente_id": cli.String()})
	require.NotContains(t, texto, "payload-nao-sai")
}

func TestListarChavesAPI_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	_, err := pool.Exec(ctx, `INSERT INTO api_keys (client_id, key_hash) VALUES ($1, repeat('c', 64))`, cli)
	require.NoError(t, err)

	h := handlers.NewAPIKeysHandler(pool)
	var tela []struct {
		ID        string `json:"id"`
		KeyMasked string `json:"key_masked"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/x", map[string]string{"clientID": cli.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_chaves_api", map[string]any{"cliente_id": cli.String()})
	require.Len(t, itens(t, out), len(tela))
	require.Equal(t, tela[0].ID, itens(t, out)[0]["id"])
	require.Equal(t, tela[0].KeyMasked, itens(t, out)[0]["identificador"])
}

func TestMinhasNotificacoes_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 0)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	ini := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	fim := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Com déficit", ini, fim, est)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
	mcptest.Regra(t, ctx, pool, camp, tipo, est, ini, fim, 127, "06:00", "22:00", 1) // nada tocou: déficit todo dia

	h := &handlers.NotificationsHandler{Repo: catalog.NewNotifications(pool)}
	var tela catalog.NotificationsResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/admin/notifications", nil, principalTeste.UserID), &tela))
	require.NotEmpty(t, tela.Items)

	out := chamarTool(t, cfgTeste(pool), "minhas_notificacoes", map[string]any{})
	require.EqualValues(t, tela.UnreadCount, out["nao_lidas"])
	require.Len(t, itens(t, out), len(tela.Items))
	require.Equal(t, tela.Items[0].Key, itens(t, out)[0]["chave"])
}
```

- [ ] **Step 2: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test -p 1 ./internal/mcp/ -run 'TestObterWebhook|TestListarEntregas|TestListarChavesAPI|TestMinhasNotificacoes' 2>&1 | head
```
Expected: FAIL (tools inexistentes).

- [ ] **Step 3: Implemente**

`workers/internal/mcp/tools_integracoes.go`:
```go
package mcp

import (
	"context"
	"slices"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
)

type saidaWebhook struct {
	Cliente     ref      `json:"cliente"`
	Configurado bool     `json:"configurado"`
	URL         *string  `json:"url"`
	Ativo       bool     `json:"ativo"`
	Eventos     []string `json:"eventos"`
	TemSegredo  bool     `json:"tem_segredo"`
	Ressalvas   []string `json:"ressalvas"`
}

type entregaDTO struct {
	ID                 string  `json:"id"`
	Evento             string  `json:"evento"`
	Status             string  `json:"status"`
	Tentativas         int     `json:"tentativas"`
	CodigoResposta     *int    `json:"codigo_resposta"`
	UltimoErro         *string `json:"ultimo_erro"`
	ProximaTentativaEm string  `json:"proxima_tentativa_em"`
	EntregueEm         *string `json:"entregue_em"`
	CriadoEm           string  `json:"criado_em"`
}

type entradaEntregas struct {
	ClienteID string `json:"cliente_id" jsonschema:"Id (uuid) do cliente, vindo de listar_clientes. Nunca invente: pergunte."`
	Status    string `json:"status,omitempty" jsonschema:"pending, delivered, failed ou dead. Vazio = todas."`
	Paginacao
}

type chaveAPIDTO struct {
	ID            string  `json:"id"`
	Identificador string  `json:"identificador"`
	CriadaEm      string  `json:"criada_em"`
	UltimoUsoEm   *string `json:"ultimo_uso_em"`
}

type notificacaoDTO struct {
	Chave    string  `json:"chave"`
	Campanha ref     `json:"campanha"`
	Cliente  ref     `json:"cliente"`
	Dia      string  `json:"dia"`
	LidaEm   *string `json:"lida_em"`
}

type saidaNotificacoes struct {
	lista[notificacaoDTO]
	NaoLidas int `json:"nao_lidas"`
}

var statusDeEntrega = []string{"", "pending", "delivered", "failed", "dead"}

func registrarIntegracoes(s *sdk.Server, cfg ConfigServidor, p *Principal) {
	ferramentaLeitura(s, cfg, "obter_webhook_cliente",
		"Diz se o cliente tem webhook configurado: URL, se está ativo, eventos e se há segredo. O segredo nunca sai (nem parte "+
			"dele); trocar é no portal. Nunca invente o cliente_id.",
		func(ctx context.Context, L *Leitura, in entradaCliente) (*sdk.CallToolResult, error) {
			c, rec, err := lerCliente(ctx, L, in.ClienteID)
			if rec != nil || err != nil {
				return rec, err
			}
			wc, err := L.Clientes.GetWebhookConfig(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			out := saidaWebhook{Cliente: refDe(c.ID, c.Name), Eventos: []string{},
				Ressalvas: []string{"O segredo do webhook nunca sai pelo MCP; configurar, trocar o segredo ou testar é no portal."}}
			if wc != nil {
				url := wc.URL
				out.Configurado, out.URL, out.Ativo, out.Eventos, out.TemSegredo = true, &url, wc.Enabled, wc.Events, wc.Secret != ""
			}
			return ok(out)
		})

	ferramentaLeitura(s, cfg, "listar_entregas_webhook",
		"Lista as entregas de webhook de um cliente, da mais nova para a mais velha: evento, status (pending, delivered, failed, "+
			"dead), tentativas, código de resposta e último erro. Use para diagnosticar 'o sistema do cliente não recebeu'. "+
			"O conteúdo enviado não vem. Nunca invente o cliente_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaEntregas) (*sdk.CallToolResult, error) {
			c, rec, err := lerCliente(ctx, L, in.ClienteID)
			if rec != nil || err != nil {
				return rec, err
			}
			if !slices.Contains(statusDeEntrega, in.Status) {
				return recusa("status desconhecido: use pending, delivered, failed ou dead", "entrada_invalida")
			}
			limit, offset := in.Paginacao.normalizar()
			ents, total, err := L.Clientes.ListWebhookDeliveries(ctx, c.ID, in.Status, limit, offset)
			if err != nil {
				return nil, err
			}
			if len(ents) == 0 && offset > 0 { // COUNT(*) OVER () dá 0 além do fim
				if _, total, err = L.Clientes.ListWebhookDeliveries(ctx, c.ID, in.Status, 1, 0); err != nil {
					return nil, err
				}
			}
			linhas := make([]entregaDTO, len(ents))
			for i, e := range ents {
				linhas[i] = entregaDTO{
					ID: e.ID.String(), Evento: e.EventType, Status: e.Status, Tentativas: e.AttemptCount,
					CodigoResposta: e.ResponseCode, UltimoErro: e.LastError, ProximaTentativaEm: instante(e.NextAttemptAt),
					EntregueEm: instanteOpc(e.DeliveredAt), CriadoEm: instante(e.CreatedAt),
				}
			}
			return ok(novaLista(linhas, total, limit, offset))
		})

	ferramentaLeitura(s, cfg, "listar_chaves_api",
		"Lista as chaves de API ativas de um cliente (a integração /v1 dele): id, identificador (início do hash, nunca a chave), "+
			"criação e último uso. Criar ou revogar é no portal. Nunca invente o cliente_id. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaClientePaginada) (*sdk.CallToolResult, error) {
			c, rec, err := lerCliente(ctx, L, in.ClienteID)
			if rec != nil || err != nil {
				return rec, err
			}
			chaves, err := L.Clientes.ListAPIKeys(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			return ok(mapear(paginar(chaves, in.Paginacao), func(k catalog.APIKeyInfo) chaveAPIDTO {
				return chaveAPIDTO{ID: k.ID.String(), Identificador: k.KeyMasked,
					CriadaEm: instante(k.CreatedAt), UltimoUsoEm: instanteOpc(k.LastUsedAt)}
			}))
		})

	ferramentaLeitura(s, cfg, "minhas_notificacoes",
		"Devolve as notificações do sininho de quem está conectado: campanhas com déficit nos últimos 7 dias (até ontem), "+
			"com cliente, dia e se já foi lida, mais o total de não lidas. Canceladas não entram. Não recebe argumentos.",
		func(ctx context.Context, L *Leitura, _ struct{}) (*sdk.CallToolResult, error) {
			if p == nil {
				return recusa("sem identidade: o pedido não passou pela autenticação do MCP", "sem_identidade")
			}
			res, err := L.Notificacoes.List(ctx, p.UserID)
			if err != nil {
				return nil, err
			}
			linhas := make([]notificacaoDTO, len(res.Items))
			for i, n := range res.Items {
				linhas[i] = notificacaoDTO{
					Chave: n.Key, Campanha: refDe(n.CampaignID, n.CampaignName), Cliente: refDe(n.ClientID, n.ClientName),
					Dia: n.OccurredOn, LidaEm: instanteOpc(n.ReadAt),
				}
			}
			return ok(saidaNotificacoes{lista: listaInteira(linhas), NaoLidas: res.UnreadCount})
		})
}
```

Em `servidor.go`: acrescente `"obter_webhook_cliente", "listar_entregas_webhook", "listar_chaves_api", "minhas_notificacoes",` a `ToolsEsperadas` (depois de `"obter_usuario"`) e `registrarIntegracoes(s, cfg, p)` depois de `registrarUsuarios(s, cfg)`.

- [ ] **Step 4: Rode e veja passar**

```bash
cd C:/tmp/emonitor-mcp/workers && go vet ./internal/mcp/ && go test -p 1 ./internal/mcp/ -run 'TestObterWebhook|TestListarEntregas|TestListarChavesAPI|TestMinhasNotificacoes|TestInventario' -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```
Expected: todos PASS.

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add workers/internal/mcp/ && git commit -m "feat(mcp): tools de integração (webhook, entregas, chaves de API) e notificações"
```

---

### Task 15: Instruções, inventário e desempenho

**Files:**
- Modify: `workers/internal/mcp/instrucoes.go`, `workers/internal/mcp/servidor_test.go`

- [ ] **Step 1: Testes**

Em `workers/internal/mcp/servidor_test.go`, troque `TestInstrucoes_TemGlossario` por:
```go
func TestInstrucoes_TemGlossarioListasEPontes(t *testing.T) {
	require.Contains(t, Instrucoes, "Glossário: veiculação = uma tocada detectada")
	require.Contains(t, Instrucoes, "Impactos = PMM × (in_slot + bonus).")
	require.Contains(t, Instrucoes, "Listas devolvem items e page")
	require.Contains(t, Instrucoes, "obter_cliente devolve hub_id")
	require.Contains(t, Instrucoes, "obter_campanha aceita campanha_id ou codigo")
}
```
E acrescente ao mesmo arquivo:
```go
// Toda tool que pede id obrigatório diz "nunca invente" (contrato §3.2), na
// descrição da tool ou na do parâmetro.
func TestInventario_IdObrigatorioMandaNaoInventar(t *testing.T) {
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	_, err := novoServidor(ConfigServidor{Versao: "teste"}, principalTeste).Connect(ctx, st, nil)
	require.NoError(t, err)
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "inventario", Version: "1"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	for _, tl := range res.Tools {
		bruto, err := json.Marshal(tl.InputSchema)
		require.NoError(t, err)
		var sch struct {
			Required   []string `json:"required"`
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(bruto, &sch))
		for _, campo := range sch.Required {
			if !strings.HasSuffix(campo, "_id") && !strings.HasSuffix(campo, "_ids") {
				continue
			}
			texto := strings.ToLower(tl.Description + " " + sch.Properties[campo].Description)
			require.Contains(t, texto, "invente", "%s.%s: diga ao modelo para nunca inventar o id", tl.Name, campo)
		}
	}
}

// Desempenho (handoff): o SDK monta o Server duas vezes por pedido.
func BenchmarkNovoServidor(b *testing.B) {
	cfg := ConfigServidor{Versao: "bench"}
	for b.Loop() {
		novoServidor(cfg, principalTeste)
	}
}
```

- [ ] **Step 2: Rode e veja falhar**

```bash
cd C:/tmp/emonitor-mcp/workers && go test ./internal/mcp/ -run 'TestInstrucoes|TestInventario' 2>&1 | head
```
Expected: FAIL em `TestInstrucoes_TemGlossarioListasEPontes` (as linhas novas não existem). O `TestInventario_IdObrigatorioMandaNaoInventar` deve passar já; se falhar, corrija a descrição da tool apontada (não o teste).

- [ ] **Step 3: Implemente**

Em `workers/internal/mcp/instrucoes.go`, acrescente estas duas linhas ao texto de `Instrucoes`, logo depois da linha que começa com `O servidor soma;`:
```
Listas devolvem items e page (total, limit, offset, returned, has_more): quando has_more for true, peça a página seguinte com offset = page.offset + page.returned. Nunca conclua um total contando os itens de uma página: use page.total ou os totais que a tool já devolve.
Pontes: obter_cliente devolve hub_id (o id do mesmo cliente no hub); obter_campanha aceita campanha_id ou codigo (o código EH-XXXXXX da campanha no hub). Para ir ao hub ou à Plura, passe esses ids ao MCP de lá.
```
(Atualize também o comentário do topo: o parágrafo de escrita continua valendo só a partir da Fatia 3.)

- [ ] **Step 4: Rode e meça**

```bash
cd C:/tmp/emonitor-mcp/workers && go test ./internal/mcp/ -run 'TestInstrucoes|TestInventario' -v 2>&1 | grep -E '^(--- |ok|FAIL)' && go test ./internal/mcp/ -run '^$' -bench BenchmarkNovoServidor -benchmem 2>&1 | grep -E 'Benchmark|ok'
```
Expected: testes PASS; o benchmark imprime `ns/op`. **Registre o número** na mensagem de commit e no handoff. Se passar de 2 ms/op (4 ms por pedido, já que o SDK monta duas vezes), **não otimize nesta fatia**: registre como follow-up no handoff da Fatia 2 (memoizar o `*sdk.Server` por pedido).

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add workers/internal/mcp/ && git commit -m "feat(mcp): instruções com listas e pontes; inventário exige 'nunca invente' em todo id obrigatório"
```
(Inclua no corpo do commit a linha `BenchmarkNovoServidor: <N> ns/op` medida no Step 4.)

---

### Task 16: Documentação

**Files:**
- Modify: `docs/features/mcp.md`, `docs/operations/mcp.md`

- [ ] **Step 1: `docs/features/mcp.md`**

No header YAML, troque `ultima-verificacao` pela data do dia e acrescente a `codigo-relacionado`:
```yaml
  - workers/internal/mcp/leitura.go
  - workers/internal/mcp/tools_clientes.go
  - workers/internal/mcp/tools_campanhas.go
  - workers/internal/mcp/tools_grade.go
  - workers/internal/mcp/tools_materiais.go
  - workers/internal/mcp/tools_emissoras.go
  - workers/internal/mcp/tools_veiculacoes.go
  - workers/internal/mcp/tools_usuarios.go
  - workers/internal/mcp/tools_integracoes.go
  - workers/internal/catalog/mcp_leituras.go
  - workers/internal/catalog/clients_integracoes.go
```
Na tabela "Estado", troque a linha `| 1-5 | … | planejadas |` por duas:
```markdown
| 1 | Leitura: 26 tools (clientes, campanhas, grade, plano do dia, materiais, emissoras, veiculações, usuários, integrações, notificações) | implementada (branch `feat/mcp-server`, não deployada) |
| 2-5 | Relatórios, escrita, crítico/pendentes, ecossistema | planejadas |
```
Troque a frase `Tools hoje: **só \`quem_sou_eu\`**.` por esta seção:
```markdown
## Tools (27)

Todas de leitura (`readOnlyHint`). Cada uma chama a MESMA função que a tela; a coluna "Fonte" diz qual.
Listas: `{items, page: {total, limit, offset, returned, has_more}}`, `limit` padrão 25 e teto 100.

| Tool | Devolve | Fonte |
|---|---|---|
| `quem_sou_eu` | dono da credencial | — |
| `listar_clientes` | clientes com `hub_id` | `GET /clients` (paginado) → `Clients.ListPaged` |
| `obter_cliente` | cadastro, vínculos, carteira | `Clients.Get`, `CountDependents`, `users.Repo.List{ClientID}` |
| `pmm_target_cliente` | PMM e PMM no target por emissora | `GET /clients/{id}/target-pmm` |
| `listar_campanhas` | campanhas (canceladas fora por padrão) | `GET /campaigns` → `Campaigns.ListPaged` |
| `obter_campanha` | campanha por id ou código do hub | `Campaigns.Get` / `ListByHubCode` |
| `financeiro_campanhas` | investido, bonificação, impactos, CPM | `GET /campaigns/financials` + `CPMDaCampanha` |
| `listar_materiais_campanha` | materiais da campanha | `GET /campaigns/{id}/materials` |
| `listar_regras_distribuicao` | regras | `GET /campaigns/{id}/distribution-rules` |
| `listar_overrides` | ajustes do dia | `GET /campaigns/{id}/distribution-overrides` |
| `listar_precos_campanha` | preços por emissora | `GET /campaigns/{id}/pricing` |
| `grade_campanha` | grade emissora × tipo × dia + totais | `GET /campaigns/{id}/daily-summary` |
| `plano_do_dia` | saldo, meta, faixas e tocadas de UMA célula | grade + regras (`categorizer.RuleCoversDay`) + ajuste + `GET /detections` |
| `listar_materiais` | materiais de um cliente | `GET /clients/{id}/materials` |
| `obter_material` | material, similaridade, campanhas | `GET /materials/{id}` + `CampaignMaterials.ListByMaterial` |
| `listar_tipos_material` | tipos | `GET /material-types` |
| `buscar_emissoras` | emissoras (busca da tela) | `GET /stations` |
| `obter_emissora` | ficha, Anatel, cidades no alcance, campanhas | `GET /stations/{id}` + `Stations.Anatel`/`CoverageCities` |
| `listar_veiculacoes` | veiculações com total real | `GET /detections?page=` → `Detections.ListPaged` |
| `obter_veiculacao` | veiculação, atribuições, aprovação | `GET /detections/{id}` + `Attributions` + `Approved` |
| `veiculacoes_por_material` | contagem por material | `GET /detections/aggregate-by-material` |
| `listar_usuarios` | usuários com estado do convite | `GET /admin/users` |
| `obter_usuario` | usuário | `GET /admin/users/{id}` |
| `obter_webhook_cliente` | configuração (sem segredo) | `GET /clients/{id}/webhook` |
| `listar_entregas_webhook` | entregas | `GET /clients/{id}/webhook-deliveries` |
| `listar_chaves_api` | chaves (só metadados) | `GET /clients/{id}/api-keys` |
| `minhas_notificacoes` | sininho de quem está conectado | `GET /admin/notifications` |

**O que nunca sai** (whitelist nos DTOs): `stream_url` e saúde do stream, `master_storage_path`/`master_sha256`,
`evidence_key`, segredo do webhook (nem o prefixo), `key_hash`, token e link de boas-vindas (o link mostra a
senha inicial). Áudio: só o link `/detections/:id` do portal.

**Decisões da Fatia 1** (plano `docs/superpowers/plans/2026-10-27-mcp-fatia-1-leitura.md`): `plano_do_dia`
devolve os ingredientes autoritativos e **não** o "progresso por faixa" do `DayDetailModal` (heurística só de
tela, em JS); o CPM de `/campaigns` virou `catalog.CPMDaCampanha` (antes só existia no navegador).
```

- [ ] **Step 2: `docs/operations/mcp.md`**

Acrescente, antes da seção de deploy, a seção:
```markdown
## Testar as tools localmente

- Um banco por agente no container `rc-mcp-test-pg` (porta 55440):
  `docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -c "CREATE DATABASE radiocheck_mcp_test_<x> TEMPLATE radiocheck_mcp_test_d2;"`.
- `TEST_DATABASE_URL=postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_<x>?sslmode=disable go test -p 1 ./internal/mcp/...`
- Os testes de tool usam `mcptest.DBCatalogo` (trunca usuários, clientes e emissoras) e as fixtures de
  `internal/mcp/mcptest/catalogo.go`. Cada tool que espelha tela tem um teste `…_ParidadeComATela` que chama
  o handler HTTP com claims de admin e compara os números.
- Paridade contra dado real: no deploy (abaixo), depois do `deploy.sh`, chame pelo Claude Code
  `financeiro_campanhas` e `grade_campanha` de 2 campanhas ativas e compare com `/campaigns` e `/detections`
  no portal. Divergência = bug: não siga para a Fatia 2.
```

- [ ] **Step 3: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add docs/features/mcp.md docs/operations/mcp.md && git commit -m "docs(mcp): tools da Fatia 1 e como testar"
```

---

### Task 17: Memória e handoff da Fatia 2

**Files:**
- Modify: `C:\Users\marke\.claude\projects\c--Users-marke-Desktop-Programas-E-Series-E-monitor\memory\mcp-emonitor-plan.md` (fora do repo)
- Create: `docs/superpowers/HANDOFF-<data>-mcp-fatia-2.md`

- [ ] **Step 1: Atualize a memória**

Leia `mcp-emonitor-plan.md` e reescreva o corpo para dizer: Fatia 0 e Fatia 1 implementadas na `feat/mcp-server` (nº de commits e o hash do topo — pegue com `git -C C:/tmp/emonitor-mcp rev-list --count origin/master..HEAD` e `git -C C:/tmp/emonitor-mcp log -1 --format=%h`), nada deployado; trabalho no worktree `C:\tmp\emonitor-mcp` porque a pasta principal é compartilhada com outras sessões; decisões F1-1 (plano do dia sem progresso por faixa) e F1-5 (CPM no catalog); próximo = Fatia 2 (relatórios). Atualize a linha correspondente em `MEMORY.md` com a data do dia.

- [ ] **Step 2: Escreva o handoff**

Crie `docs/superpowers/HANDOFF-<data>-mcp-fatia-2.md` copiando a estrutura do `HANDOFF-2026-10-27-mcp.md` (bloco "Prompt para colar", Estado, Ambiente de teste, Convenções, Armadilhas, Pendências, Deploy), com:
- **Prompt:** mesmas regras do dono; trabalho no worktree `C:\tmp\emonitor-mcp`; tarefa = plano da Fatia 2 (spec §6 "Fatia 2": `insights_campanha`, `visao_gerencial`, `relatorio_data_hora`, `resumo_relatorio_campanha` + `exportar_consolidado`, `exportar_veiculacoes`, `mapa_cobertura_campanha`, as 5 de falhas, `assertividade`, as 3 de pós-venda, `auditoria_mcp`; telas lendo filtro da URL; 10 perguntas de avaliação mcp-builder ≥ 9/10).
- **Estado:** as 27 tools, nº de commits, o que foi verificado (Task 18) e o `ns/op` do benchmark.
- **Convenções (o que a Fatia 1 criou e a 2 reusa):** `ferramentaLeitura`, `Paginacao`/`paginar`/`buscarJanela`/`mapear`, `ok`/`recusa`/`recusaEntrada`/`naoEncontrado`, `lerCliente`/`lerCampanha`, `nomes*`, `categoriaDe`, `limitesDoDiaSP`, fixtures `mcptest.*`, `chamarTool`/`chamarHandler`/`itens`/`pag`/`ids`; teste de paridade obrigatório; `TestInventario_IdObrigatorioMandaNaoInventar`.
- **Armadilhas novas:** o "progresso por faixa" do `DayDetailModal` é JS (não porte); `ListPaged` de veiculações não usa `ApprovedDetectionsFilter` literal (equivalente na prática; `Approved` usa o literal); `COUNT(*) OVER ()` dá total 0 além do fim (o `buscarJanela` já trata); pasta principal compartilhada — use o worktree; migration `0070` da `feat/username-login` (outra sessão) colide em número com a `0070_mcp_fundacao` — quem mergear por último renumera, e o dono decide.
- **Pendências:** as do handoff anterior que continuam abertas + o follow-up de desempenho se o benchmark pedir.

- [ ] **Step 3: Commit do handoff**

```bash
cd C:/tmp/emonitor-mcp && git add docs/superpowers/HANDOFF-*-mcp-fatia-2.md && git commit -m "docs(mcp): handoff da Fatia 1 para a Fatia 2 (relatórios)"
```

---

### Task 18: Verificação final da fatia

**Files:** nenhum (só verificação)

- [ ] **Step 1: Cross-compile e vet (regra 6.1)**

```bash
cd C:/tmp/emonitor-mcp/workers && go vet ./... && CGO_ENABLED=0 GOOS=linux go build ./...
```
Expected: sem saída de erro.

- [ ] **Step 2: Testes de todo pacote tocado, um por vez**

```bash
cd C:/tmp/emonitor-mcp/workers && for p in ./internal/mcp/... ./internal/catalog/ ./internal/categorizer/ ./internal/api/handlers/ ./internal/users/; do go test -p 1 $p 2>&1 | tail -3; done
```
Expected: `ok` em todos. Falha conhecida e não sua: `internal/catalog TestBuildDailySummary_WithDowntime` antes de ~13:00 UTC (CLAUDE.md 6.6). O guard do `dbtest` pode pular testes destrutivos do `handlers` se o banco tiver dado de outro pacote — rode `internal/api/handlers` num banco recém-criado do template se isso acontecer.

- [ ] **Step 3: Formatação só do que esta fatia tocou**

```bash
cd C:/tmp/emonitor-mcp && git diff --name-only 8215e83 -- '*.go' | xargs gofmt -l
```
Expected: nenhuma linha (lembre do CRLF: só os arquivos desta fatia contam).

- [ ] **Step 4: Nada fora do escopo**

```bash
cd C:/tmp/emonitor-mcp && git diff --stat 8215e83 -- frontend migrations infra
```
Expected: vazio (esta fatia não toca frontend, migrations nem infra; nada de `npm install`).

- [ ] **Step 5: Revisão final da branch**

Despache um revisor (superpowers:code-reviewer) sobre `git diff 8215e83..HEAD`, pedindo: aderência a este plano e à spec §3/§4.4/§6; regra de ouro (nenhuma regra de negócio nova no `internal/mcp`); vazamento de campo sensível; números divergindo da tela; descrição de tool sem armadilha ou sem "nunca invente". Corrija o que for CONFIRMADO, com teste.

- [ ] **Step 6: Mostre ao dono**

Resuma ao Dereck: as 27 tools, as decisões F1-1..F1-8, o resultado da verificação, o `ns/op`, e lembre que **nada foi para o master nem foi pushado** (decisão dele quando o MCP estiver 100%).

---

## Riscos conhecidos

- **Colisão de número de migration.** A `feat/username-login` (outra sessão, pasta principal) criou `migrations/0070_users_username.*`; a `feat/mcp-server` já tem `0070_mcp_fundacao.*`. O golang-migrate não aceita duas versões 0070. Esta fatia não cria migration; quem mergear por último no master precisa renumerar — decisão do dono.
- **Árvore compartilhada.** A pasta principal troca de branch sem aviso (aconteceu em 2026-10-27). Por isso todo comando deste plano roda em `C:\tmp\emonitor-mcp`.
- **Paridade contra dado real.** Sem o restore de prod local, a paridade é provada em fixtures. A conferência com dado real fica para o deploy (Task 16, `docs/operations/mcp.md`).

## Self-review (feito ao escrever)

- **Cobertura da spec §6 "Fatia 1":** todas as 26 tools listadas estão em uma task (6: 3; 7: 3; 8: 4; 9: 2; 10: 3; 11: 2; 12: 3; 13: 2; 14: 4). `obter_veiculacao` traz "link para ouvir" (`/detections/:id`); `obter_campanha` aceita `codigo`; `obter_cliente` devolve `hub_id`; linha de pontes nas instruções (Task 15). "Teste de inventário" (Task 15 + o `TestInventario` existente). Paridade tool × handler em toda tool que espelha tela; as que não espelham uma rota única (`obter_cliente`, `plano_do_dia`, `obter_material`, `obter_emissora`, `obter_usuario`) testam comportamento e, onde há número da tela, o número.
- **Convenções do handoff:** envelope §3.3 (Task 1), `ressalvas` (Task 1 + todas), `falhaInterna` (Task 1), `anotacoesLeitura` via `ferramentaLeitura`, `ToolsEsperadas` no mesmo commit de cada família, `ConfigServidor` ampliado (Task 1/2), desempenho medido (Task 15).
- **Placeholders:** nenhum "TBD"; os únicos valores a preencher na hora são data do dia, nº de commits e `ns/op` (Task 17), que só existem depois de rodar.
- **Tipos e nomes conferidos entre tasks:** `lista`/`paginar`/`buscarJanela`/`mapear`/`listaInteira`/`novaLista` (T1) usados em T6–T14; `lerCliente` (T6) em T10/T14; `lerCampanha` (T7) em T8/T9; `faixaHoraria` e `diasDaMascara` (T8) em T9; `categoriaDe` (T9) em T12; `entradaCliente`/`entradaClientePaginada` (T6) em T14; `papelDe`/`situacaoDe` (T6) em T13; `nomes*`/`todasCampanhas` (T2) em T6–T14; `catalog.ListByHubCode`/`CancelledAtFor`/`HubIDs`/`TodaySaoPaulo`/`CPMDaCampanha` (T3), `Anatel`/`CoverageCities`/`Attributions`/filtros do `ListPaged` (T4), `ListWebhookDeliveries`/`ListAPIKeys`/`RuleCoversDay` (T5), `Approved` (T12).
