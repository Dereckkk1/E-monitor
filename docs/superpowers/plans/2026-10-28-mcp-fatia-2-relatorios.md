# MCP do E-monitor — Fatia 2 (Relatórios) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Dar ao MCP do E-monitor as 17 tools de relatório da spec §6 "Fatia 2" (insights, visão gerencial, data e hora, resumo e exportações de campanha, mapa de cobertura, as 5 de falhas, assertividade, as 3 de pós-venda e a auditoria do MCP), cada uma com o MESMO número da tela (teste de paridade contra o handler HTTP) e um link para a tela já filtrada; fazer as telas que ainda não leem filtro da URL passarem a ler; e passar na avaliação de 10 perguntas do mcp-builder com ≥ 9/10.

**Architecture:** Igual à Fatia 1. As tools moram em `workers/internal/mcp/tools_<familia>.go` e chamam os repositórios que os handlers chamam (`catalog.Insights.Compute`, `catalog.ManagementOverview.Get`, `assertiveness.Repo.Get`, `catalog.Detections.ListPaged/Aggregate*`, `catalog.LiveMap.Get/Coverage`, `catalog.StationFailures`, `catalog.CampaignFailures`, `catalog.FailuresDaily`, `postsale.Repo`), reunidos na struct `Leitura`. As duas contas que hoje moram em handler (totais do resumo do relatório de campanha; "monitorando agora" da Visão Gerencial) são extraídas antes para uma função que o handler e a tool chamam. Todo relatório devolve `links.tela` com os filtros na query string; o frontend passa a ler esses filtros ao montar em `/insights`, `/management`, `/live-map` e em mais três parâmetros de `/admin/station-failures`. A avaliação roda sobre uma semente SQL determinística, por um CLI de desenvolvimento que chama o servidor MCP em memória.

**Tech Stack:** Go 1.26, go-sdk v1.8.0 (`github.com/modelcontextprotocol/go-sdk/mcp`), pgx v5, chi v5, testify; React 18 + react-router (frontend, sem dependência nova); `node --test` para os utilitários do frontend.

**Spec:** [docs/superpowers/specs/2026-10-27-mcp-server-design.md](../specs/2026-10-27-mcp-server-design.md) §3 (regra de ouro), §4.4 (resultado), §4.5 (deep-links), §6 "Fatia 2", §7.
**Contrato:** `hub/docs/architecture/mcp-ecossistema.md` (em `origin/main` do hub) §3.3 (listas e cursor), §3.6 (o que nunca sai).
**Plano anterior (padrão de tasks e testes):** [2026-10-27-mcp-fatia-1-leitura.md](2026-10-27-mcp-fatia-1-leitura.md). **Handoff:** [HANDOFF-2026-10-28-mcp-fatia-2.md](../HANDOFF-2026-10-28-mcp-fatia-2.md).

---

## Decisões deste plano (o dono aprova junto com o plano)

| # | Decisão | Por quê |
|---|---|---|
| F2-1 | **Nenhum link direto de arquivo.** Toda tool de relatório devolve `links: {tela, como_baixar?}`. `tela` é a rota do portal com os filtros na query; `como_baixar` diz qual botão apertar ("Relatórios → CSV Consolidado"). `exportar_consolidado` e `exportar_veiculacoes` devolvem o link da tela + o número de linhas que o arquivo vai ter (provado por teste contra o CSV real), não uma URL do CSV. | As rotas de CSV (`/reports/campaigns/{id}/consolidated.csv`, `/detections/export`) só aceitam `Authorization: Bearer` (o frontend baixa por `fetch` + blob). Um link direto dá 401 no navegador; um link com token seria URL assinada, que o contrato §3.6 e a D7 proíbem. |
| F2-2 | **Telas que passam a ler filtro da URL (só frontend, sem efeito colateral):** `/insights` (`client_id`, `campaigns`, `from`, `to`, `stations`, `materials`), `/management` (`client_id`, `campaigns`, `status`, `from`, `to`), `/live-map` (`client_id`, `campaigns`) e, em `/admin/station-failures`, além dos `view`/`date`/`campaign` que já existem: `sub=historical`, `min_down` e `from`/`to` da aba "Por dia". Os nomes são os da API (inglês), como os que `/reports/airtime` e `/admin/station-failures` já leem. A leitura é só no primeiro render; depois a tela segue com o estado local (como `/admin/station-failures` já faz). `/reports/airtime` já lê tudo e não muda. `/detections` **não** muda: o detalhado e o consolidado são baixados do menu "Relatórios" de `/reports/airtime`, que herda o período da tela. | Spec §4.5. Um parser puro (`utils/filtrosDaUrl.js`) com teste `node --test` concentra a validação; as páginas só o chamam no `useState` inicial. |
| F2-3 | **Teto de saída em toda lista interna:** listas dentro de um item (quedas e campanhas de uma emissora com falha, emissoras de uma campanha com falha, linhas de checking de um pós-venda, cidades de uma emissora no mapa) saem cortadas em 20 com o `*_total` real e uma ressalva; a lista principal é paginada. `visao_gerencial` **não** devolve a lista de emissoras do mapa nem o feed ao vivo (milhares de linhas sem filtro): devolve os KPIs e `emissoras_no_mapa`. `resumo_relatorio_campanha` **não** devolve `by_material_station` (é o conteúdo do CSV consolidado, materiais × emissoras): devolve totais, por material e a lista paginada por emissora. | Lição da Fatia 1 (commit `2414725`): o cliente MCP corta a saída em ~25k tokens e `paginar` só limita a lista principal. |
| F2-4 | **"Monitorando agora" entra no MCP pelo supervisor.** O MCP roda dentro da API (D1), no mesmo processo do supervisor; `DepsModulo` ganha `Workers` e a `Leitura` repassa. A conta "emissoras monitoradas ∩ workers ativos" sai do handler para `supervisor.ContarAtivas`, chamada pelos dois. Sem supervisor (CLI de avaliação, testes), `emissoras_ao_vivo` vem `null` (não medido), nunca 0. | Regra de ouro: hoje a conta está inline no handler (`management_overview.go:98-112`). |
| F2-5 | **Datas padrão em America/Sao_Paulo, sempre ecoadas no resultado.** `insights_campanha` sem período = mês corrente; `visao_gerencial` = 01/01 até hoje; falhas sem `data` = ontem; `falhas_por_dia` = últimos 30 dias. A tela usa `time.Now()` do processo (UTC no container) para os mesmos padrões; a diferença só aparece entre 21h e 0h de SP e não chega ao usuário porque o resultado diz o período usado e o link leva as datas explícitas. As validações de limite (90 dias, futuro, 92 dias de faixa) são as do handler, copiadas como validação de entrada. `Leitura.Agora` é injetável para os testes. | Contrato: período `YYYY-MM-DD` de calendário; "o servidor soma" exige que o modelo saiba qual janela foi somada. |
| F2-6 | **Duas extrações para fora do handler** (refactor sem mudança de comportamento, travado por teste de golden JSON do handler antes de mexer): (a) `catalog.TotaisDoResumo` e `catalog.ImpactosDaEmissora` (os totais e o arredondamento `math.Round(pmm × impact_count)` de `reports.go:257-280`); (b) `supervisor.ContarAtivas` (F2-4). E uma leitura nova sem regra: `catalog.PrecoDeTabela(precos, campanha, emissora, tipo)`, o mesmo lookup que `AirtimeDetectionRow.jsx:36-45` faz no navegador (consolidado → sem valor unitário; por inserção → `unit_value` do tipo). | Spec §3: a tool não reimplementa conta. O `relatorio_data_hora` precisa do valor da linha pronto (as instruções mandam nunca recalcular). |
| F2-7 | **Pós-venda por whitelist, sem nada que abra o documento:** nunca sai o `token` do destinatário (é a credencial do link público `/pos-venda/:token`, sem expiração), nunca o link público, nunca chave S3; `attachments_url` sai só como esquema+host (pode ser link "qualquer pessoa com o link" do Drive), como o webhook da Fatia 1. Rascunho não tem números congelados: `obter_pos_venda` devolve `numeros: null` + ressalva (o `Preview` recalcularia tudo e exige o `postsale.Service` com S3 e e-mail). Emails dos destinatários saem (o admin os vê na tela; não são segredo). | Contrato §3.6 e `docs/features/post-sale.md` (o documento é congelado; o token é o acesso). |
| F2-8 | **`auditoria_mcp` lista só o que o MCP gravou** (`metadata->>'origem' = 'mcp'`), por cursor, `page.total: null`, mais recente primeiro. Leitura nova em `mcp/auditoria` (`Listar`), não no `catalog`: a tabela só tem escritores do MCP e do `client.hub_link`. Sem migration: o índice de `created_at` atende o volume atual. | Contrato §3.3: cursor com `total: null` só em coleção que só cresce. |
| F2-9 | **Avaliação sem chave da API da Anthropic** (não há `ANTHROPIC_API_KEY` nesta máquina): semente SQL determinística (`workers/internal/mcp/avaliacao/semente.sql`, datas relativas a `CURRENT_DATE` onde a tool tem janela móvel), um teste de gabarito que prova cada resposta chamando as tools (`TestAvaliacao_Gabarito`), e um CLI de desenvolvimento (`go run ./internal/mcp/avaliacao/cli`) que lista as tools e chama uma tool no servidor em memória. Um subagente sem acesso ao código nem ao banco responde as 10 perguntas só pelo CLI; a nota é a contagem de respostas iguais ao gabarito. O CLI fica fora do `workers.Dockerfile` de propósito (não é CLI de prod; regra 6.7 vale para `cmd/*`). Se o dono quiser o `evaluation.py` oficial do mcp-builder, o mesmo XML serve (exige a chave e a API local de pé). | Spec §7 pede ≥ 9/10 "só leitura, várias chamadas, resposta única verificável, estável". |
| F2-10 | **`resumo_diario_falhas` não devolve `seen`** (estado de UI por pessoa) e nunca marca "visto" (o `POST /ack` é escrita e fica de fora). | Tool de leitura sem efeito colateral. |
| F2-11 | **`relatorio_data_hora` exige UM cliente e o período completo**, como a tela (`/reports/airtime` só busca com `from` e `to`). Campanha de outro cliente na lista → `[entrada_invalida]` com os nomes. | `docs/features/airtime-report.md`: o relatório é por cliente; pricing por (campanha, emissora). |

**Fora desta fatia:** qualquer escrita (inclusive o `ack` do digest), geração de PDF/CSV no servidor, a tela `/pendentes`, MCP Apps.

---

## Antes de começar — leia

- `CLAUDE.md` da raiz: regras 4.8, **5 (nunca `npm install` no `frontend/`; esta fatia usa só `npm ci`, que não reescreve o lockfile)** e 6.x.
- **Árvore compartilhada.** Trabalhe só no worktree `C:\tmp\emonitor-mcp` (branch `feat/mcp-server`). Nunca `git checkout`/`git switch` na pasta principal. Nunca `git stash` sem tag. Nada de push.
- **Banco de teste:** um por agente, no container `rc-mcp-test-pg` (porta 55440), criado do template `radiocheck_mcp_test_d2`. `export TEST_DATABASE_URL=…` em TODA chamada de Bash (sem ela os testes de banco dão SKIP e parecem verdes). Sempre `go test -p 1`. Rode `./internal/mcp/...` ANTES de `./internal/catalog/` (o catalog deixa clientes e o guard passa a barrar o MCP).
- **CRLF:** `gofmt -l` só nos arquivos que você tocou.
- **`ToolsEsperadas` é em ORDEM ALFABÉTICA** (é a ordem do `tools/list` do SDK). Toda task que cria tool acrescenta o nome na posição alfabética **no mesmo commit**. A lista final está na Task 14.
- **Padrões da Fatia 1 que valem aqui** (leia o código antes de escrever): `ferramentaLeitura`, `ok`/`recusa`/`recusaEntrada`/`naoEncontrado`, `paginar`/`buscarJanela`/`mapear`/`listaInteira`, `ref`/`refDe`, `lerCampanha`, `lerCampanhas`, `lerPeriodoSP`, `recusaCampoDoPeriodo`, `todasCampanhas`, `soEsquemaEHost`, `tetoEmissorasPorItem`, `ressalvaCancelada`, `ressalvaBaseFinanceira`, `ressalvaVeiculacoes`, `veiculacaoDe`; nos testes, `mcptest.DBCatalogo` + fixtures, `chamarTool`, `chamarToolErro`, `chamarHandler`, `itens`, `pag`, `cfgTeste`.
- **Teste de paridade é obrigatório** em toda tool que espelha uma tela: semeia, chama o handler HTTP com claims de admin (`chamarHandler`) e a tool, e compara os números campo a campo.

## Mapa de arquivos

| Arquivo | Responsabilidade |
|---|---|
| `workers/internal/mcp/toolkit_relatorios.go` (+`_test`) | (cria) `links`, `linkTela`, `csvIDs`, `cortar`, `tetoPorItem`, lista por cursor (`paginaCursor`, `listaCursor`), `hojeSP`, `ontemSP`, `mesCorrenteSP` |
| `workers/internal/mcp/leitura.go` | (modifica) repositórios de relatório, `Workers`, `Pool`, `Agora` |
| `workers/internal/mcp/modulo.go`, `workers/cmd/api/main.go` | (modifica) `DepsModulo.Workers` ← supervisor |
| `workers/internal/mcp/servidor.go` | (modifica) registra as famílias novas, `ToolsEsperadas`, `NovoServidorLocal` |
| `workers/internal/mcp/tools_insights.go` (+`_test`) | `insights_campanha` |
| `workers/internal/mcp/tools_gerencial.go` (+`_test`) | `visao_gerencial`, `assertividade` |
| `workers/internal/mcp/tools_relatorios.go` (+`_test`) | `relatorio_data_hora`, `resumo_relatorio_campanha`, `exportar_consolidado`, `exportar_veiculacoes` |
| `workers/internal/mcp/tools_mapa.go` (+`_test`) | `mapa_cobertura_campanha` |
| `workers/internal/mcp/tools_falhas.go` (+`_test`) | `falhas_emissoras`, `falhas_campanhas`, `obter_falhas_campanha`, `falhas_por_dia`, `resumo_diario_falhas` |
| `workers/internal/mcp/tools_posvenda.go` (+`_test`) | `listar_pos_vendas`, `obter_pos_venda`, `destinatarios_pos_venda` |
| `workers/internal/mcp/tools_auditoria.go` (+`_test`) | `auditoria_mcp` |
| `workers/internal/mcp/auditoria/auditoria.go` (+`_test`) | (modifica) `Listar` por cursor |
| `workers/internal/mcp/mcptest/relatorios.go` | (cria) fixtures `PMMTarget`, `Queda`, `Cobertura`, `Assertividade`, `PosVenda` |
| `workers/internal/catalog/resumo_relatorio.go` (+`_test`) | (cria) `ResumoTotais`, `TotaisDoResumo`, `ImpactosDaEmissora` |
| `workers/internal/catalog/preco_tabela.go` (+`_test`) | (cria) `PrecoDeTabela` |
| `workers/internal/api/handlers/reports.go` (+`reports_summary_test.go`) | (modifica) `Summary` chama `catalog.TotaisDoResumo` |
| `workers/internal/supervisor/contar_ativas.go` (+`_test`) | (cria) `ContarAtivas` |
| `workers/internal/api/handlers/management_overview.go` | (modifica) chama `supervisor.ContarAtivas` |
| `workers/internal/mcp/avaliacao/semente.sql`, `perguntas.xml`, `gabarito_test.go`, `cli/main.go` | (cria) avaliação mcp-builder |
| `frontend/src/utils/filtrosDaUrl.js` (+`.test.mjs`) | (cria) parser dos filtros da URL |
| `frontend/src/pages/InsightsPage.jsx`, `ManagementPage.jsx`, `LiveMapPage.jsx`, `AdminStationFailuresPage.jsx`, `components/FailuresDailyView.jsx` | (modifica) leem os filtros no primeiro render |
| `docs/features/mcp.md`, `docs/operations/mcp.md`, `docs/README.md` | tabela de tools, deep-links, avaliação |
| `docs/superpowers/HANDOFF-AAAA-MM-DD-mcp-fatia-3.md` | handoff da Fatia 3 |

**Lotes para o subagent-driven-development:** A = Tasks 0–3 (ambiente, toolkit, `Leitura`/fixtures, extrações); B = Tasks 4–8 (insights, gerencial, data e hora/resumo/exportações, mapa); C = Tasks 9–12 (falhas, pós-venda, auditoria); D = Tasks 13–18 (frontend, instruções/inventário/desempenho, avaliação, docs, handoff, verificação). Revisão de aderência + qualidade ao fim de cada lote; revisão final da branch no fim do D. Tasks dentro de um lote mexem em `servidor.go` e `leitura.go`: rode-as em sequência, não em paralelo.

---

### Task 0: Ambiente — worktree, banco de teste e partições

**Files:** nenhum (só o banco de teste)

- [ ] **Step 1: Confira o worktree e a branch**

```bash
cd C:/tmp/emonitor-mcp && git branch --show-current && git status --short
```
Expected: `feat/mcp-server` e árvore limpa.

- [ ] **Step 2: Container de teste de pé**

```bash
docker ps --filter name=rc-mcp-test-pg --format '{{.Names}} {{.Status}}'
```
Expected: `rc-mcp-test-pg Up …`. Se não aparecer, siga "Ambiente de teste" do handoff da Fatia 2.

- [ ] **Step 3: Partições de `stream_health_events` de 2026_01 a 2026_09 no template**

O template só tem `stream_health_2026_10` em diante. As tools de falhas trabalham com "ontem"/"últimos 30 dias" e com até 90 dias para trás: sem as partições de 2026, uma fixture `mcptest.Queda` num dia de setembro dá `no partition of relation`.

```bash
for m in 01 02 03 04 05 06 07 08 09; do
  n=$(printf '%02d' $((10#$m + 1)))
  fim="2026-$n-01"; [ "$m" = "12" ] && fim="2027-01-01"
  docker exec rc-mcp-test-pg psql -U radiocheck -d radiocheck_mcp_test_d2 -c \
    "CREATE TABLE IF NOT EXISTS stream_health_2026_$m PARTITION OF stream_health_events FOR VALUES FROM ('2026-$m-01') TO ('$fim');"
done
docker exec rc-mcp-test-pg psql -U radiocheck -d radiocheck_mcp_test_d2 -At -c \
  "SELECT count(*) FROM pg_inherits i JOIN pg_class p ON p.oid=i.inhparent WHERE p.relname='stream_health_events'"
```
Expected: `CREATE TABLE` nove vezes e a contagem final 28 (19 que já existiam + 9).

- [ ] **Step 4: Crie o seu banco a partir do template**

```bash
docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -c "CREATE DATABASE radiocheck_mcp_test_f2 TEMPLATE radiocheck_mcp_test_d2;"
```
Daqui em diante, todo comando de teste começa com:
```bash
export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable"
```
Subagentes em paralelo usam bancos próprios (`…_f2a`, `…_f2b`, criados igual). Se o guard passar a recusar o banco (mais de 10 clientes / 50 emissoras), apague e recrie do template.

- [ ] **Step 5: Linha de base verde**

```bash
cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go build ./... && go test -p 1 ./internal/mcp/... 2>&1 | tail -6
```
Expected: `ok` em todos os pacotes de `internal/mcp` (timeout do Bash 600000 ms; leva ~2 min).

---

### Task 1: Toolkit de relatório — links, teto por item, cursor e datas de SP

**Files:**
- Create: `workers/internal/mcp/toolkit_relatorios.go`
- Test: `workers/internal/mcp/toolkit_relatorios_test.go`

- [ ] **Step 1: Escreva o teste que falha**

```go
package mcp

import (
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestLinkTela_FiltrosNaQuery(t *testing.T) {
	cfg := ConfigServidor{Portal: "https://portal.teste/"}
	q := url.Values{}
	q.Set("client_id", "c1")
	q.Set("campaigns", "a,b")
	require.Equal(t, "https://portal.teste/insights?campaigns=a%2Cb&client_id=c1", *cfg.linkTela("/insights", q))
	require.Equal(t, "https://portal.teste/management", *cfg.linkTela("/management", nil))
	require.Nil(t, ConfigServidor{}.linkTela("/insights", q), "sem portal configurado o link é null")
}

func TestCsvIDs(t *testing.T) {
	a, b := uuid.MustParse("00000000-0000-0000-0000-00000000000a"), uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	require.Equal(t, a.String()+","+b.String(), csvIDs([]uuid.UUID{a, b}))
	require.Equal(t, "", csvIDs(nil))
}

func TestCortar_DevolveTotalReal(t *testing.T) {
	xs, total := cortar([]int{1, 2, 3}, 2)
	require.Equal(t, []int{1, 2}, xs)
	require.Equal(t, 3, total)
	vazio, total := cortar[int](nil, 20)
	require.NotNil(t, vazio, "lista vazia sai [] e não null")
	require.Equal(t, 0, total)
}

func TestListaCursor_TotalNulo(t *testing.T) {
	l := novaListaCursor([]string{"a", "b"}, 2, "prox")
	require.Nil(t, l.Page.Total)
	require.True(t, l.Page.HasMore)
	require.Equal(t, "prox", *l.Page.NextCursor)
	fim := novaListaCursor([]string{}, 25, "")
	require.False(t, fim.Page.HasMore)
	require.Nil(t, fim.Page.NextCursor)
}

func TestDatasDeSP(t *testing.T) {
	// 2026-10-28 01:30 UTC ainda é 27/10 em São Paulo.
	agora := time.Date(2026, 10, 28, 1, 30, 0, 0, time.UTC)
	require.Equal(t, "2026-10-27", dia(hojeSP(agora)))
	require.Equal(t, "2026-10-26", dia(ontemSP(agora)))
	ini, fim := mesCorrenteSP(agora)
	require.Equal(t, "2026-10-01", dia(ini))
	require.Equal(t, "2026-10-31", dia(fim))
}
```

- [ ] **Step 2: Rode e veja falhar**

Run: `cd C:/tmp/emonitor-mcp/workers && go test ./internal/mcp/ -run 'TestLinkTela|TestCsvIDs|TestCortar|TestListaCursor|TestDatasDeSP' 2>&1 | tail -5`
Expected: FAIL de compilação (`undefined: linkTela` etc.).

- [ ] **Step 3: Implemente**

```go
package mcp

import (
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// links é o bloco de deep-link de toda tool de relatório (spec §4.5, D7).
// Nunca há link direto de arquivo: o download do portal exige o login da
// pessoa no cabeçalho (JWT), e um link com credencial seria URL assinada.
type links struct {
	Tela       *string `json:"tela"`
	ComoBaixar string  `json:"como_baixar,omitempty"`
}

// linkTela: a rota do portal com os filtros na query (nil sem portal configurado).
func (c ConfigServidor) linkTela(caminho string, q url.Values) *string {
	if len(q) == 0 {
		return c.link(caminho)
	}
	return c.link(caminho + "?" + q.Encode())
}

// csvIDs: o formato das listas de id na query das telas ("a,b,c").
func csvIDs(ids []uuid.UUID) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = id.String()
	}
	return strings.Join(s, ",")
}

// tetoPorItem: listas DENTRO de um item (quedas, campanhas, emissoras,
// cidades, linhas de checking) saem cortadas aqui, com o total real ao lado —
// o cliente MCP corta a saída em ~25k tokens e paginar só limita a lista
// principal (lição do commit 2414725).
const tetoPorItem = tetoEmissorasPorItem

// cortar devolve no máximo n itens e o total real; nunca nil (sai [] no JSON).
func cortar[T any](xs []T, n int) ([]T, int) {
	if xs == nil {
		xs = []T{}
	}
	if len(xs) > n {
		return xs[:n], len(xs)
	}
	return xs, len(xs)
}

// ── Lista por cursor (contrato §3.3: coleção que só cresce) ────────────────

type paginaCursor struct {
	// Total é sempre null: "não contado", nunca zero.
	Total      *int    `json:"total"`
	Limit      int     `json:"limit"`
	Returned   int     `json:"returned"`
	HasMore    bool    `json:"has_more"`
	NextCursor *string `json:"next_cursor"`
}

type listaCursor[T any] struct {
	Items     []T          `json:"items"`
	Page      paginaCursor `json:"page"`
	Ressalvas []string     `json:"ressalvas,omitempty"`
}

// novaListaCursor: proximo vazio = fim da coleção.
func novaListaCursor[T any](items []T, limit int, proximo string) listaCursor[T] {
	if items == nil {
		items = []T{}
	}
	p := paginaCursor{Limit: limit, Returned: len(items), HasMore: proximo != ""}
	if proximo != "" {
		p.NextCursor = &proximo
	}
	return listaCursor[T]{Items: items, Page: p}
}

// ── Datas de calendário em America/Sao_Paulo ──────────────────────────────

// hojeSP: o dia de hoje em SP como data de calendário (meia-noite UTC, como o DATE do pgx).
func hojeSP(agora time.Time) time.Time {
	t := agora.In(fusoSP)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func ontemSP(agora time.Time) time.Time { return hojeSP(agora).AddDate(0, 0, -1) }

// mesCorrenteSP: primeiro e último dia do mês corrente em SP.
func mesCorrenteSP(agora time.Time) (time.Time, time.Time) {
	h := hojeSP(agora)
	ini := time.Date(h.Year(), h.Month(), 1, 0, 0, 0, 0, time.UTC)
	return ini, ini.AddDate(0, 1, -1)
}
```

- [ ] **Step 4: Rode e veja passar**

Run: `cd C:/tmp/emonitor-mcp/workers && go test ./internal/mcp/ -run 'TestLinkTela|TestCsvIDs|TestCortar|TestListaCursor|TestDatasDeSP' -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: 5 `--- PASS` e `ok`.

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/mcp/toolkit_relatorios*.go
git add workers/internal/mcp/toolkit_relatorios.go workers/internal/mcp/toolkit_relatorios_test.go
git commit -m "feat(mcp): toolkit de relatório — deep-link, teto por item, cursor e datas de SP

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
Expected: `gofmt -l` sem saída; commit criado.

---

### Task 2: `Leitura` com os repositórios de relatório, supervisor e fixtures

**Files:**
- Create: `workers/internal/supervisor/contar_ativas.go`, `workers/internal/supervisor/contar_ativas_test.go`
- Modify: `workers/internal/api/handlers/management_overview.go:98-112`
- Modify: `workers/internal/mcp/leitura.go`, `workers/internal/mcp/modulo.go`, `workers/cmd/api/main.go` (bloco `mcp.NovoModulo`, ~linha 693)
- Create: `workers/internal/mcp/mcptest/relatorios.go`
- Test: `workers/internal/mcp/leitura_test.go` (acrescenta)

- [ ] **Step 1: Teste do `ContarAtivas` (falha)**

```go
package supervisor

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestContarAtivas_SoMonitoradasComWorkerAtivo(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	st := []WorkerStatus{
		{StationID: a.String(), Active: true},
		{StationID: b.String(), Active: false},
		{StationID: uuid.NewString(), Active: true}, // worker de emissora fora do escopo
	}
	require.Equal(t, 1, ContarAtivas(st, []uuid.UUID{a, b, c}))
	require.Equal(t, 0, ContarAtivas(nil, []uuid.UUID{a}))
}
```

Run: `cd C:/tmp/emonitor-mcp/workers && go test ./internal/supervisor/ -run TestContarAtivas 2>&1 | tail -3`
Expected: FAIL (`undefined: ContarAtivas`).

- [ ] **Step 2: Implemente `ContarAtivas` e faça o handler chamá-la**

`workers/internal/supervisor/contar_ativas.go`:
```go
package supervisor

import "github.com/google/uuid"

// ContarAtivas: quantas emissoras de ids têm worker ativo agora — o
// "monitorando agora" da Visão Gerencial. O handler HTTP e o MCP chamam esta
// função; a coluna stations.health_status não é populada, então a fonte é o
// supervisor (mesma da /operations).
func ContarAtivas(statuses []WorkerStatus, ids []uuid.UUID) int {
	ativas := make(map[string]bool, len(statuses))
	for _, ws := range statuses {
		if ws.Active {
			ativas[ws.StationID] = true
		}
	}
	n := 0
	for _, id := range ids {
		if ativas[id.String()] {
			n++
		}
	}
	return n
}
```

Em `workers/internal/api/handlers/management_overview.go`, troque o bloco `if h.Workers != nil { … }` (linhas ~98-112, o que monta `active` e conta `live`) por:
```go
	if h.Workers != nil {
		out.KPIs.StationsLive = supervisor.ContarAtivas(h.Workers.WorkerStatuses(), out.MonitoredStationIDs)
	}
```
e acrescente `"radiocheck/internal/supervisor"` aos imports (o pacote `handlers` já o importa em `health.go`, então não há ciclo).

Run: `cd C:/tmp/emonitor-mcp/workers && go test ./internal/supervisor/ -run TestContarAtivas && go test ./internal/api/handlers/ -run TestMgmtHandler 2>&1 | tail -3`
Expected: `ok` nos dois (o `TestMgmtHandler_StationsLive_FromSupervisor` continua verde: é a trava do refactor).

- [ ] **Step 3: Teste da `Leitura` nova (falha)**

Acrescente a `workers/internal/mcp/leitura_test.go`:
```go
func TestNovaLeitura_RepositoriosDeRelatorio(t *testing.T) {
	L := NovaLeitura(nil)
	require.NotNil(t, L.Insights)
	require.NotNil(t, L.Gerencial)
	require.NotNil(t, L.Assertividade)
	require.NotNil(t, L.MapaAoVivo)
	require.NotNil(t, L.FalhasEmissoras)
	require.NotNil(t, L.FalhasCampanhas)
	require.NotNil(t, L.FalhasPorDia)
	require.NotNil(t, L.PosVenda)
	require.Nil(t, L.Workers, "sem supervisor: emissoras_ao_vivo sai null")
	fixo := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	L.Agora = func() time.Time { return fixo }
	require.Equal(t, fixo, L.agora())
	require.WithinDuration(t, time.Now(), (&Leitura{}).agora(), time.Minute)
}
```
(acrescente `"time"` aos imports do arquivo se faltar).

Run: `cd C:/tmp/emonitor-mcp/workers && go test ./internal/mcp/ -run TestNovaLeitura_RepositoriosDeRelatorio 2>&1 | tail -3`
Expected: FAIL de compilação.

- [ ] **Step 4: Implemente na `Leitura`, no módulo e no `main.go`**

Em `workers/internal/mcp/leitura.go`, acrescente os imports `"time"`, `"radiocheck/internal/assertiveness"`, `"radiocheck/internal/postsale"`, `"radiocheck/internal/supervisor"`, e os campos ao fim da struct `Leitura`:
```go
	Insights        *catalog.Insights
	Gerencial       *catalog.ManagementOverview
	Assertividade   *assertiveness.Repo
	MapaAoVivo      *catalog.LiveMap
	FalhasEmissoras *catalog.StationFailures
	FalhasCampanhas *catalog.CampaignFailures
	FalhasPorDia    *catalog.FailuresDaily
	PosVenda        *postsale.Repo
	// Pool: leitura do audit_log pelo auditoria_mcp (o pacote auditoria não tem repositório).
	Pool *pgxpool.Pool
	// Workers: o supervisor da API, para "monitorando agora". nil fora da API
	// (CLI de avaliação, testes): o número sai null, nunca 0.
	Workers ListaWorkers
	// Agora: relógio injetável (padrões de data e janela da assertividade).
	Agora func() time.Time
```
mais, depois da struct:
```go
// ListaWorkers é o que a Visão Gerencial lê do supervisor (*supervisor.Supervisor satisfaz).
type ListaWorkers interface {
	WorkerStatuses() []supervisor.WorkerStatus
}

func (L *Leitura) agora() time.Time {
	if L.Agora != nil {
		return L.Agora()
	}
	return time.Now()
}
```
e no `NovaLeitura`:
```go
		Insights:        catalog.NewInsights(pool),
		Gerencial:       catalog.NewManagementOverview(pool),
		Assertividade:   assertiveness.New(pool),
		MapaAoVivo:      catalog.NewLiveMap(pool),
		FalhasEmissoras: catalog.NewStationFailures(pool),
		FalhasCampanhas: catalog.NewCampaignFailures(pool),
		FalhasPorDia:    catalog.NewFailuresDaily(pool),
		PosVenda:        postsale.NewRepo(pool),
		Pool:            pool,
```
Confira antes que nenhum desses pacotes importa `internal/mcp` (`grep -rn '"radiocheck/internal/mcp' workers/internal/{assertiveness,postsale,supervisor,catalog}` sem saída) — senão há ciclo.

Em `workers/internal/mcp/modulo.go`: acrescente a `DepsModulo` o campo
```go
	// Workers: o supervisor (para "monitorando agora" da visao_gerencial). Pode ser nil.
	Workers ListaWorkers
```
e troque a linha do `cfg` por:
```go
	L := NovaLeitura(d.Pool)
	L.Workers = d.Workers
	cfg := ConfigServidor{Versao: d.Versao, Portal: d.Portal, Leitura: L, Log: d.Log}
```

Em `workers/cmd/api/main.go`, no `mcp.NovoModulo(mcp.DepsModulo{…})` (~linha 693), acrescente `Workers: sup,`. O `sup` é criado sempre na linha ~300 (`sup := supervisor.New(…)`), nunca nil — confira; se em algum caminho ele puder ser nil, passe o campo só quando `sup != nil` (uma interface com ponteiro nil dentro não é nil e daria panic no `WorkerStatuses`).

- [ ] **Step 5: Fixtures de relatório**

`workers/internal/mcp/mcptest/relatorios.go`:
```go
package mcptest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/postsale"
)

// PMMTarget grava o PMM no target do cliente numa emissora.
func PMMTarget(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cliente, emissora uuid.UUID, pmm int) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO client_station_pmm (client_id, station_id, pmm_target) VALUES ($1, $2, $3)`,
		cliente, emissora, pmm)
	require.NoError(t, err)
}

// Queda grava um evento de stream fora do ar (o que as telas de falha cruzam).
func Queda(t *testing.T, ctx context.Context, pool *pgxpool.Pool, emissora uuid.UUID, inicio time.Time, duracaoSeg int) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO stream_health_events (station_id, event_type, event_at, duration_seconds)
		VALUES ($1, 'down', $2, $3)`, emissora, inicio, duracaoSeg)
	require.NoError(t, err)
}

// Cobertura dá à emissora coordenada, raio Anatel e cidades no alcance.
// contorno e alcance em km; cidades = nome → distância (km). A primeira é a sede.
func Cobertura(t *testing.T, ctx context.Context, pool *pgxpool.Pool, emissora uuid.UUID, classe string,
	contorno, alcance float64, cidades ...CidadeAlcance) {
	t.Helper()
	_, err := pool.Exec(ctx, `UPDATE stations SET latitude = -23.5, longitude = -46.6, city = 'Sede', state = 'SP',
		anatel_class = $2, anatel_coverage_km = $3, anatel_reach_km = $4 WHERE id = $1`,
		emissora, classe, contorno, alcance)
	require.NoError(t, err)
	for i, c := range cidades {
		_, err = pool.Exec(ctx, `INSERT INTO station_coverage_cities
			(station_id, ibge_code, city, state, distance_km, is_home, latitude, longitude)
			VALUES ($1, $2, $3, 'SP', $4, $5, -23.5, -46.6)`, emissora, c.IBGE, c.Nome, c.Km, i == 0)
		require.NoError(t, err)
	}
}

type CidadeAlcance struct {
	IBGE int
	Nome string
	Km   float64
}

// Assertividade grava o fato diário da assertividade (o card lê um SUM dele).
func Assertividade(t *testing.T, ctx context.Context, pool *pgxpool.Pool, dia time.Time, campanha, emissora, material uuid.UUID,
	auto, miss, semFingerprint int) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO assertiveness_daily
		(for_date, campaign_id, station_id, commercial_id, auto, miss, x_no_fingerprint)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, dia, campanha, emissora, material, auto, miss, semFingerprint)
	require.NoError(t, err)
}

// PosVenda cria um pós-venda com um bloco da campanha e os destinatários; enviado
// = true congela o payload (MarkSent) e marca a 1ª pessoa como "abriu".
func PosVenda(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cliente, campanha uuid.UUID, titulo, de, ate string,
	enviado bool, payload *postsale.Payload, pessoas ...postsale.RecipientInput) uuid.UUID {
	t.Helper()
	repo := postsale.NewRepo(pool)
	rep, err := repo.CreateDraft(ctx, postsale.CreateDraftInput{ClientID: cliente, Title: titulo})
	require.NoError(t, err)
	require.NoError(t, repo.ReplaceBlocks(ctx, rep.ID, []postsale.BlockRow{{CampaignID: campanha, From: Dia(de), To: Dia(ate)}}))
	for i := range pessoas {
		if pessoas[i].Token == "" {
			pessoas[i].Token = uuid.NewString() // o repo grava o token que recebe
		}
	}
	if len(pessoas) > 0 {
		dest, err := repo.CreateRecipients(ctx, rep.ID, pessoas)
		require.NoError(t, err)
		if enviado {
			_, err = pool.Exec(ctx, `UPDATE post_sale_report_recipients SET opened_at = now(), open_count = 2, email_status = 'sent' WHERE id = $1`, dest[0].ID)
			require.NoError(t, err)
		}
	}
	if enviado {
		b, err := json.Marshal(payload)
		require.NoError(t, err)
		require.NoError(t, repo.MarkSent(ctx, rep.ID, b))
	}
	return rep.ID
}
```
A tabela de destinatários é `post_sale_report_recipients` e o `CreateRecipients` grava o `Token` que recebe (`repo_crud.go:354-372`), por isso a fixture o preenche. Antes de rodar, confira em `migrations/` se `stations.latitude/longitude/city/state` existem com esses nomes (o `LiveMap.Get` só mostra emissora com coordenada) e ajuste a fixture ao schema real — não o contrário.

- [ ] **Step 6: Rode e veja passar**

```bash
cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go build ./... && go vet ./internal/mcp/... ./internal/supervisor/ && go test -p 1 ./internal/mcp/ -run 'TestNovaLeitura|TestModulo' 2>&1 | tail -3
```
Expected: build e vet limpos; `ok`.

- [ ] **Step 7: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/supervisor/contar_ativas*.go workers/internal/api/handlers/management_overview.go workers/internal/mcp/leitura*.go workers/internal/mcp/modulo.go workers/internal/mcp/mcptest/relatorios.go workers/cmd/api/main.go
git add workers/internal/supervisor/contar_ativas.go workers/internal/supervisor/contar_ativas_test.go workers/internal/api/handlers/management_overview.go workers/internal/mcp/leitura.go workers/internal/mcp/leitura_test.go workers/internal/mcp/modulo.go workers/internal/mcp/mcptest/relatorios.go workers/cmd/api/main.go
git commit -m "feat(mcp): Leitura com os repositórios de relatório e o supervisor; fixtures de relatório

ContarAtivas sai do handler da Visão Gerencial para o supervisor: o handler e
o MCP passam a chamar a mesma função.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Extrações para o `catalog` — totais do resumo e preço de tabela

**Files:**
- Create: `workers/internal/api/handlers/reports_summary_test.go` (trava do refactor, ANTES de mexer)
- Create: `workers/internal/catalog/resumo_relatorio.go`, `workers/internal/catalog/resumo_relatorio_test.go`
- Modify: `workers/internal/api/handlers/reports.go:257-280`
- Create: `workers/internal/catalog/preco_tabela.go`, `workers/internal/catalog/preco_tabela_test.go`

- [ ] **Step 1: Trave o `Summary` atual com um teste de banco (deve PASSAR antes do refactor)**

O `ReportsHandler` não tem teste. Este fixa os totais atuais do `GET /reports/campaigns/{id}/summary`:
```go
package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func TestReportsSummary_TotaisTravados(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000.4)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 0) // sem PMM: fora dos impactos
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", e1, e2)
	mcptest.PMMTarget(t, ctx, pool, cli, e1, 300)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "bonus", mcptest.Quando("2026-10-10", "09:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "out_slot", mcptest.Quando("2026-10-10", "23:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e2, "in_slot", mcptest.Quando("2026-10-11", "08:00"))

	h := &handlers.ReportsHandler{Detections: catalog.NewDetections(pool), CampaignRepo: catalog.NewCampaigns(pool), Pool: pool}
	var r handlers.SummaryResponse
	require.NoError(t, json.Unmarshal(chamarHandlerAdmin(t, h.Summary, "/x", map[string]string{"id": camp.String()}, admin), &r))
	require.Equal(t, 4, r.Totals.Detections)
	require.Equal(t, 2, r.Totals.DistinctStations)
	require.EqualValues(t, 2001, r.Totals.Impactos, "round(1000,4 × 2 [in_slot+bonus]) — out_slot não conta")
	require.EqualValues(t, 600, r.Totals.ImpactosTarget)
	require.Equal(t, 1, r.Totals.StationsWithTarget)
}
```
O `chamarHandler` da Fatia 1 está no pacote `mcp` (não exportado). Se o pacote `handlers_test` ainda não tiver um helper equivalente, crie no próprio arquivo:
```go
func chamarHandlerAdmin(t *testing.T, h http.HandlerFunc, alvo string, rota map[string]string, admin uuid.UUID) []byte {
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
```
(imports: `context`, `net/http`, `net/http/httptest`, `github.com/go-chi/chi/v5`, `github.com/google/uuid`, `radiocheck/internal/auth`). Antes, `grep -rn "func chamarHandlerAdmin\|mcptest\." workers/internal/api/handlers/*_test.go`: se `handlers` (pacote interno) já importar algo que importe `mcptest`, use-o; se `mcptest` importar `handlers` haveria ciclo — não importa (confira com `go vet`).

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/api/handlers/ -run TestReportsSummary_TotaisTravados -v 2>&1 | grep -E '^(--- |ok|FAIL)|Error'`
Expected: **PASS** (é a foto do comportamento atual). Se algum número divergir, o número esperado deste teste está errado — investigue a conta do handler e corrija o TESTE, não o handler, e anote no relatório do lote.

- [ ] **Step 2: Teste unitário das funções novas (falha)**

`workers/internal/catalog/resumo_relatorio_test.go`:
```go
package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTotaisDoResumo_BaseInSlotMaisBonusEArredondamento(t *testing.T) {
	pmm := 1000.4
	alvo := 300
	por := []StationAggregateRow{
		{StationPMM: &pmm, StationPMMTarget: &alvo, Count: 3, ImpactCount: 2},
		{Count: 1, ImpactCount: 1}, // sem PMM: fora dos impactos
	}
	tot := TotaisDoResumo(&MaterialAggregateResult{TotalDetections: 4, DistinctMaterials: 1}, por)
	require.Equal(t, ResumoTotais{Detections: 4, DistinctMaterials: 1, DistinctStations: 2,
		Impactos: 2001, ImpactosTarget: 600, StationsWithTarget: 1}, tot)
	require.EqualValues(t, 2001, *ImpactosDaEmissora(por[0]))
	require.Nil(t, ImpactosDaEmissora(por[1]), "sem PMM = não se aplica, não zero")
}
```

Run: `cd C:/tmp/emonitor-mcp/workers && go test ./internal/catalog/ -run TestTotaisDoResumo 2>&1 | tail -3`
Expected: FAIL (`undefined: TotaisDoResumo`).

- [ ] **Step 3: Implemente e faça o handler chamar**

`workers/internal/catalog/resumo_relatorio.go`:
```go
package catalog

import "math"

// ResumoTotais são os totais do relatório de campanha (PDF e MCP).
type ResumoTotais struct {
	Detections        int
	DistinctMaterials int
	DistinctStations  int
	// Impactos = Σ round(PMM × impact_count) por emissora, impact_count =
	// in_slot + bonus (base canônica — docs/features/client-target-pmm.md).
	Impactos           int64
	ImpactosTarget     int64
	StationsWithTarget int
}

// ImpactosDaEmissora: round(PMM × (in_slot + bonus)). math.Round, não
// truncamento: a coluna "Impactos" do PDF é Math.round no frontend
// (utils/pdfReport.js), e o KPI do topo não pode ficar abaixo da soma da
// própria coluna. nil = emissora sem PMM (fora do total, não zero).
func ImpactosDaEmissora(s StationAggregateRow) *int64 {
	if s.StationPMM == nil {
		return nil
	}
	v := int64(math.Round(*s.StationPMM * float64(s.ImpactCount)))
	return &v
}

// TotaisDoResumo soma por emissora (uma linha por emissora; somar por
// material × emissora contaria a emissora uma vez por material).
func TotaisDoResumo(porMaterial *MaterialAggregateResult, porEmissora []StationAggregateRow) ResumoTotais {
	t := ResumoTotais{
		Detections:        porMaterial.TotalDetections,
		DistinctMaterials: porMaterial.DistinctMaterials,
		DistinctStations:  len(porEmissora),
	}
	for _, s := range porEmissora {
		if v := ImpactosDaEmissora(s); v != nil {
			t.Impactos += *v
		}
		if s.StationPMMTarget != nil {
			t.ImpactosTarget += int64(*s.StationPMMTarget * s.ImpactCount)
			t.StationsWithTarget++
		}
	}
	return t
}
```

Em `workers/internal/api/handlers/reports.go`, troque as atribuições de `resp.Totals.Detections` até o fim do `for _, s := range byStation { … }` (linhas ~257-280, inclusive o comentário longo, que passa a morar no `catalog`) por:
```go
	tot := catalog.TotaisDoResumo(byMaterial, byStation)
	resp.Totals.Detections = tot.Detections
	resp.Totals.DistinctMaterials = tot.DistinctMaterials
	resp.Totals.DistinctStations = tot.DistinctStations
	resp.Totals.Impactos = tot.Impactos
	resp.Totals.ImpactosTarget = tot.ImpactosTarget
	resp.Totals.StationsWithTarget = tot.StationsWithTarget
```
e tire `"math"` dos imports se ficar sem uso.

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test ./internal/catalog/ -run TestTotaisDoResumo && go test -p 1 ./internal/api/handlers/ -run TestReportsSummary_TotaisTravados 2>&1 | tail -3`
Expected: `ok` nos dois — a trava do Step 1 continua verde.

- [ ] **Step 4: `PrecoDeTabela` (teste que falha + implementação)**

`workers/internal/catalog/preco_tabela_test.go`:
```go
package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPrecoDeTabela(t *testing.T) {
	camp, e1, e2, e3, spot, outro := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cem := 5000.0
	precos := []StationPricing{
		{CampaignID: camp, StationID: e1, Mode: PricingModePerInsertion, PerType: []TypePricing{{TypeID: spot, UnitValue: 150}}},
		{CampaignID: camp, StationID: e2, Mode: "consolidated", ConsolidatedValue: &cem},
	}
	v, modo := PrecoDeTabela(precos, camp, e1, &spot)
	require.Equal(t, 150.0, *v)
	require.Equal(t, PricingModePerInsertion, modo)
	v, modo = PrecoDeTabela(precos, camp, e1, &outro)
	require.Nil(t, v, "tipo sem preço na emissora")
	require.Equal(t, PricingModePerInsertion, modo)
	v, modo = PrecoDeTabela(precos, camp, e2, &spot)
	require.Nil(t, v, "consolidado não tem valor por inserção")
	require.Equal(t, "consolidated", modo)
	v, modo = PrecoDeTabela(precos, camp, e3, &spot)
	require.Nil(t, v)
	require.Equal(t, "", modo, "emissora sem linha de preço")
	v, _ = PrecoDeTabela(precos, camp, e1, nil)
	require.Nil(t, v, "material sem tipo")
}
```
Confira o nome da constante do modo consolidado em `catalog/pricing.go` (`PricingModeConsolidated`?) e use-a no teste e no código em vez da string.

`workers/internal/catalog/preco_tabela.go`:
```go
package catalog

import "github.com/google/uuid"

// PrecoDeTabela é o valor unitário de tabela de uma veiculação: o mesmo
// lookup que o relatório Data e Hora faz no navegador
// (frontend/src/components/AirtimeDetectionRow.jsx:36-45). Consolidado não
// tem valor por inserção (nil + modo "consolidated"); por inserção é o
// unit_value do tipo do material na emissora. NÃO é o valor faturado da
// linha: out_slot e out_date não valem nada e bonus é bonificação. modo ""
// = a emissora não tem linha de preço na campanha.
func PrecoDeTabela(precos []StationPricing, campanha, emissora uuid.UUID, tipo *uuid.UUID) (*float64, string) {
	for _, p := range precos {
		if p.CampaignID != campanha || p.StationID != emissora {
			continue
		}
		if p.Mode != PricingModePerInsertion || tipo == nil {
			return nil, p.Mode
		}
		for _, tp := range p.PerType {
			if tp.TypeID == *tipo {
				v := tp.UnitValue
				return &v, p.Mode
			}
		}
		return nil, p.Mode
	}
	return nil, ""
}
```

Run: `cd C:/tmp/emonitor-mcp/workers && go test ./internal/catalog/ -run TestPrecoDeTabela -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/catalog/resumo_relatorio*.go workers/internal/catalog/preco_tabela*.go workers/internal/api/handlers/reports.go workers/internal/api/handlers/reports_summary_test.go
git add workers/internal/catalog/resumo_relatorio.go workers/internal/catalog/resumo_relatorio_test.go workers/internal/catalog/preco_tabela.go workers/internal/catalog/preco_tabela_test.go workers/internal/api/handlers/reports.go workers/internal/api/handlers/reports_summary_test.go
git commit -m "refactor(reports): totais do resumo de campanha no catalog; PrecoDeTabela

TotaisDoResumo/ImpactosDaEmissora saem do handler do summary (mesmo
comportamento, travado por TestReportsSummary_TotaisTravados). PrecoDeTabela
é o lookup que o relatório Data e Hora faz no navegador, para o MCP devolver
o valor pronto.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 4: `insights_campanha`

**Files:**
- Create: `workers/internal/mcp/tools_insights.go`
- Test: `workers/internal/mcp/tools_insights_test.go`
- Modify: `workers/internal/mcp/servidor.go` (registro + `ToolsEsperadas`)

Fonte: `GET /insights` (`handlers/insights.go`) → `catalog.Insights.Compute(ctx, InsightsParams)`. Leia o handler inteiro e `catalog/insights.go:138-276` antes de começar.

- [ ] **Step 1: Teste de paridade (falha)**

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

func TestInsightsCampanha_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 2)
	mcptest.Preco(t, ctx, pool, camp, est, tipo, 100)
	mcptest.PMMTarget(t, ctx, pool, cli, est, 250)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "09:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "out_slot", mcptest.Quando("2026-10-10", "23:30"))

	h := handlers.NewInsightsHandler(catalog.NewInsights(pool))
	var tela catalog.InsightsPayload
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get,
		"/insights?client_id="+cli.String()+"&campaigns="+camp.String()+"&from=2026-10-01&to=2026-10-31", nil, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "insights_campanha", map[string]any{
		"cliente_id": cli.String(), "campanha_ids": []string{camp.String()}, "de": "2026-10-01", "ate": "2026-10-31"})
	require.EqualValues(t, tela.KPIs.Impactos, out["impactos"])
	require.EqualValues(t, tela.KPIs.ImpactosTarget, out["impactos_target"])
	require.EqualValues(t, tela.KPIs.VeiculacoesTotal, out["veiculacoes"])
	inv := out["investido"].(map[string]any)
	require.EqualValues(t, tela.KPIs.Investido.Executado, inv["executado"])
	require.EqualValues(t, tela.KPIs.Investido.Contratado, inv["contratado"])
	require.EqualValues(t, tela.KPIs.Bonificacao.Valor, out["bonificacao"].(map[string]any)["valor"])
	require.EqualValues(t, tela.KPIs.CPM, out["cpm"])
	cat := out["por_categoria"].(map[string]any)
	require.EqualValues(t, tela.VeiculacoesBreakdown.InSlot, cat["in_slot"])
	require.EqualValues(t, tela.VeiculacoesBreakdown.ExtrasOrphan, cat["bonus"])
	require.EqualValues(t, tela.VeiculacoesBreakdown.OutSlot, cat["out_slot"])
	require.Len(t, out["serie"], len(tela.Buckets))
	// Conferência independente da base in_slot + bonus:
	require.EqualValues(t, 3000, out["impactos"])
	require.EqualValues(t, 750, out["impactos_target"])
	require.Equal(t, "https://portal.teste/insights?campaigns="+camp.String()+"&client_id="+cli.String()+"&from=2026-10-01&to=2026-10-31",
		out["links"].(map[string]any)["tela"])
	require.NotEmpty(t, out["ressalvas"])
}

func TestInsightsCampanha_Recusas(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	a := mcptest.Cliente(t, ctx, pool, "A")
	b := mcptest.Cliente(t, ctx, pool, "B")
	campB := mcptest.Campanha(t, ctx, pool, b, "De B", "2026-10-01", "2026-10-31")
	cfg := cfgTeste(pool)
	require.Contains(t, chamarToolErro(t, cfg, "insights_campanha", map[string]any{"cliente_id": a.String(), "campanha_ids": []string{}}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "insights_campanha", map[string]any{
		"cliente_id": a.String(), "campanha_ids": []string{campB.String()}, "de": "2026-10-01", "ate": "2026-10-31"}), "não são do cliente")
	require.Contains(t, chamarToolErro(t, cfg, "insights_campanha", map[string]any{
		"cliente_id": b.String(), "campanha_ids": []string{campB.String()}, "de": "2026-10-31", "ate": "2026-10-01"}), "[entrada_invalida]")
}
```

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run TestInsightsCampanha 2>&1 | tail -4`
Expected: FAIL (`tool "insights_campanha" not found` ou similar).

- [ ] **Step 2: Implemente a tool**

`workers/internal/mcp/tools_insights.go`:
```go
package mcp

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
)

type entradaInsights struct {
	ClienteID   string   `json:"cliente_id" jsonschema:"Id do cliente (de listar_clientes). Todas as campanhas têm de ser dele. Nunca invente: pergunte."`
	CampanhaIDs []string `json:"campanha_ids" jsonschema:"De 1 a 50 campanhas do cliente (de listar_campanhas). Nunca invente: pergunte."`
	De          string   `json:"de,omitempty" jsonschema:"Primeiro dia (AAAA-MM-DD). Padrão: 1º dia do mês corrente em America/Sao_Paulo."`
	Ate         string   `json:"ate,omitempty" jsonschema:"Último dia (AAAA-MM-DD). Padrão: último dia do mês corrente."`
	EmissoraIDs []string `json:"emissora_ids,omitempty" jsonschema:"Só estas emissoras (até 200, ids de buscar_emissoras). Vazio = todas as da campanha."`
	MaterialIDs []string `json:"material_ids,omitempty" jsonschema:"Só estes materiais (até 200). Com filtro, os valores em R$ viram rateio."`
}

type periodoDTO struct {
	De            string `json:"de"`
	Ate           string `json:"ate"`
	Granularidade string `json:"granularidade"`
}

type campanhaInsights struct {
	Campanha ref    `json:"campanha"`
	Inicio   string `json:"inicio"`
	Fim      string `json:"fim"`
	Estado   string `json:"estado"`
}

type pontoInsights struct {
	Periodo    string `json:"periodo"`
	Programado int    `json:"programado"`
	InSlot     int    `json:"in_slot"`
	OutSlot    int    `json:"out_slot"`
	OutDate    int    `json:"out_date"`
	Deficit    int    `json:"deficit"`
	Extras     int    `json:"extras"`
}

type insightsDTO struct {
	Periodo            periodoDTO         `json:"periodo"`
	Campanhas          []campanhaInsights `json:"campanhas"`
	Impactos           int64              `json:"impactos"`
	ImpactosTarget     *int64             `json:"impactos_target"`
	EmissorasComTarget int                `json:"emissoras_com_target"`
	RotuloTarget       *string            `json:"rotulo_target"`
	Veiculacoes        int64              `json:"veiculacoes"`
	PorCategoria       struct {
		InSlot  int64 `json:"in_slot"`
		Bonus   int64 `json:"bonus"`
		OutSlot int64 `json:"out_slot"`
		OutDate int64 `json:"out_date"`
	} `json:"por_categoria"`
	Emissoras       int `json:"emissoras"`
	EmissorasComPMM int `json:"emissoras_com_pmm"`
	Investido       struct {
		Contratado float64 `json:"contratado"`
		Executado  float64 `json:"executado"`
	} `json:"investido"`
	Bonificacao struct {
		Valor     float64 `json:"valor"`
		Insercoes int64   `json:"insercoes"`
	} `json:"bonificacao"`
	CPM                  *float64 `json:"cpm"`
	CPMTarget            *float64 `json:"cpm_target"`
	Consolidado          bool     `json:"consolidado"`
	RateadoPorMaterial   bool     `json:"rateado_por_material"`
	ParticipacaoMaterial *float64 `json:"participacao_material"`
	Genero               struct {
		M int64 `json:"m"`
		F int64 `json:"f"`
	} `json:"genero"`
	Classe struct {
		AB int64 `json:"ab"`
		C  int64 `json:"c"`
		DE int64 `json:"de"`
	} `json:"classe"`
	Idade struct {
		De18a24 int64 `json:"18_24"`
		De25a49 int64 `json:"25_49"`
		De50    int64 `json:"50_mais"`
	} `json:"idade"`
	Serie     []pontoInsights `json:"serie"`
	Links     links           `json:"links"`
	Ressalvas []string        `json:"ressalvas"`
}

const ressalvaInsights = "Impactos = PMM × (in_slot + bonus); out_slot não vale nada e out_date está fora do período. veiculacoes soma as 4 categorias. CPM null = sem impactos no período (não é zero); cpm_target é sempre o dinâmico, mesmo com CPM fixo. impactos_target null = nenhuma emissora com PMM no target do cliente. Demografia só soma emissoras com PMM."

const ressalvaConsolidado = "Há emissora com preço consolidado: investido.contratado é o valor do contrato, acumulado por mês iniciado até hoje (America/Sao_Paulo), não proporcional à janela."

const ressalvaRateio = "Com filtro de material, investido, bonificação, CPM e o programado da série são RATEIO pela participação do material nas veiculações (o contrato não separa valor por material); impactos, veiculações e demografia são exatos."

const ressalvaMudancaDeRegra = "Os números caíram em 2026-08-17 por mudança de regra (o executado deixou de somar out_slot e acabou a dupla contagem do excedente): relatório anterior a essa data não bate com este, e é o antigo que estava errado."

// mudancaDeRegra: o dia em que a categorização por cota entrou (docs/features/insights-dashboard.md).
var mudancaDeRegra = time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)

// lerIDs valida até teto uuids; campo vai na mensagem de erro.
func lerIDs(ss []string, teto int) ([]uuid.UUID, bool) {
	if len(ss) > teto {
		return nil, false
	}
	out := make([]uuid.UUID, 0, len(ss))
	for _, s := range ss {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

// lerDias lê de/ate (AAAA-MM-DD) com padrão; campo inválido volta no 3º valor.
func lerDias(de, ate string, padraoDe, padraoAte time.Time) (time.Time, time.Time, string) {
	ini, fim := padraoDe, padraoAte
	if de != "" {
		d, ok := lerDia(de)
		if !ok {
			return ini, fim, "de"
		}
		ini = d
	}
	if ate != "" {
		d, ok := lerDia(ate)
		if !ok {
			return ini, fim, "ate"
		}
		fim = d
	}
	if fim.Before(ini) {
		return ini, fim, campoPeriodo
	}
	return ini, fim, ""
}

func registrarInsights(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "insights_campanha",
		"Devolve o painel /insights de UM cliente: impactos (e no target), veiculações por categoria, investido "+
			"(contratado e executado), bonificação, CPM, demografia e a série por dia (ou por mês acima de 31 dias), "+
			"mais o link da tela já filtrada. Pede cliente_id e 1 a 50 campanhas dele; período padrão = mês corrente. "+
			"A armadilha: com material_ids, os valores em R$ viram rateio. Nunca some nem recalcule: os números vêm prontos.",
		func(ctx context.Context, L *Leitura, in entradaInsights) (*sdk.CallToolResult, error) {
			cliente, err := uuid.Parse(in.ClienteID)
			if err != nil {
				return recusaEntrada("cliente_id")
			}
			camps, okC := lerIDs(in.CampanhaIDs, 50)
			if !okC || len(camps) == 0 {
				return recusa("campanha_ids precisa de 1 a 50 ids válidos", "entrada_invalida")
			}
			emissoras, okE := lerIDs(in.EmissoraIDs, 200)
			if !okE {
				return recusaEntrada("emissora_ids")
			}
			materiais, okM := lerIDs(in.MaterialIDs, 200)
			if !okM {
				return recusaEntrada("material_ids")
			}
			pIni, pFim := mesCorrenteSP(L.agora())
			de, ate, campo := lerDias(in.De, in.Ate, pIni, pFim)
			if campo != "" {
				return recusaCampoDoPeriodo(campo)
			}
			pay, err := L.Insights.Compute(ctx, catalog.InsightsParams{
				ClientID: cliente, CampaignIDs: camps, From: de, To: ate,
				StationIDs: emissoras, MaterialIDs: materiais, Today: catalog.TodaySaoPaulo(),
			})
			if err != nil {
				// O mesmo teste do handler: o repo marca campanha de outro cliente com "cross-client".
				if strings.Contains(err.Error(), "cross-client") {
					return recusa("há campanha_ids que não são do cliente informado (ou não existem): use listar_campanhas com cliente_id", "entrada_invalida")
				}
				return nil, err
			}
			out := insightsDe(pay)
			q := url.Values{}
			q.Set("client_id", cliente.String())
			q.Set("campaigns", csvIDs(camps))
			q.Set("from", dia(de))
			q.Set("to", dia(ate))
			if len(emissoras) > 0 {
				q.Set("stations", csvIDs(emissoras))
			}
			if len(materiais) > 0 {
				q.Set("materials", csvIDs(materiais))
			}
			out.Links = links{Tela: cfg.linkTela("/insights", q)}
			out.Ressalvas = []string{ressalvaInsights}
			if pay.Consolidated {
				out.Ressalvas = append(out.Ressalvas, ressalvaConsolidado)
			}
			if pay.MaterialProrated {
				out.Ressalvas = append(out.Ressalvas, ressalvaRateio)
			}
			if de.Before(mudancaDeRegra) {
				out.Ressalvas = append(out.Ressalvas, ressalvaMudancaDeRegra)
			}
			if slices.ContainsFunc(pay.Campaigns, func(c catalog.CampaignBrief) bool { return c.Status == "cancelada" }) {
				out.Ressalvas = append(out.Ressalvas, ressalvaCancelada)
			}
			return ok(out)
		})
}

func insightsDe(p *catalog.InsightsPayload) insightsDTO {
	k := p.KPIs
	var o insightsDTO
	o.Periodo = periodoDTO{De: p.Period.From, Ate: p.Period.To, Granularidade: map[string]string{"day": "dia", "month": "mes"}[p.Period.Granularity]}
	o.Campanhas = make([]campanhaInsights, len(p.Campaigns))
	for i, c := range p.Campaigns {
		o.Campanhas[i] = campanhaInsights{Campanha: refDe(c.ID, c.Name), Inicio: c.StartDate, Fim: c.EndDate, Estado: c.Status}
	}
	o.Impactos = k.Impactos
	o.EmissorasComTarget = k.StationsWithTarget
	if k.StationsWithTarget > 0 {
		v := k.ImpactosTarget
		o.ImpactosTarget = &v
	}
	o.RotuloTarget = p.TargetLabel
	o.Veiculacoes = k.VeiculacoesTotal
	o.PorCategoria.InSlot = p.VeiculacoesBreakdown.InSlot
	o.PorCategoria.Bonus = p.VeiculacoesBreakdown.ExtrasOrphan // o nome antigo da categoria bonus
	o.PorCategoria.OutSlot = p.VeiculacoesBreakdown.OutSlot
	o.PorCategoria.OutDate = p.VeiculacoesBreakdown.OutDate
	o.Emissoras = k.StationsCount
	o.EmissorasComPMM = k.StationsWithPMM
	o.Investido.Contratado = k.Investido.Contratado
	o.Investido.Executado = k.Investido.Executado
	o.Bonificacao.Valor = k.Bonificacao.Valor
	o.Bonificacao.Insercoes = k.Bonificacao.Count
	if k.Impactos > 0 {
		v := k.CPM
		o.CPM = &v
	}
	if k.StationsWithTarget > 0 && k.ImpactosTarget > 0 {
		v := k.CPMTarget
		o.CPMTarget = &v
	}
	o.Consolidado = p.Consolidated
	o.RateadoPorMaterial = p.MaterialProrated
	if p.MaterialProrated {
		v := p.MaterialShare
		o.ParticipacaoMaterial = &v
	}
	o.Genero.M, o.Genero.F = k.Gender.M, k.Gender.F
	o.Classe.AB, o.Classe.C, o.Classe.DE = p.ClassPyramid.AB, p.ClassPyramid.C, p.ClassPyramid.DE
	o.Idade.De18a24, o.Idade.De25a49, o.Idade.De50 = p.AgeRanges.R18_24, p.AgeRanges.R25_49, p.AgeRanges.R50Plus
	o.Serie = make([]pontoInsights, len(p.Buckets)) // Buckets pode vir nil: sai []
	for i, b := range p.Buckets {
		o.Serie[i] = pontoInsights{Periodo: b.Bucket, Programado: b.Programado, InSlot: b.InSlot, OutSlot: b.OutSlot,
			OutDate: b.OutDate, Deficit: b.Deficit, Extras: b.Extras}
	}
	return o
}
```
Notas para o implementador:
- `lerIDs` e `lerDias` são genéricos e serão reusados nas Tasks 5–11; ficam aqui porque esta é a primeira tool a usá-los.
- O handler aceita `from`/`to` inválidos em silêncio (cai no padrão); a tool recusa — é validação de entrada, não regra.
- Confira em `catalog/insights.go` o que `BucketRow.Extras` soma (leia `aggregateBuckets`, ~:744). Se for só `bonus`, renomeie o campo DTO para `bonus`; se for a soma de excedentes, mantenha `extras` e acrescente uma frase à `ressalvaInsights` dizendo o que é. Não adivinhe.

Registre em `servidor.go`: `registrarInsights(s, cfg)` depois de `registrarIntegracoes(s, cfg, p)`, e `"insights_campanha"` em `ToolsEsperadas` entre `"grade_campanha"` e `"listar_campanhas"`.

- [ ] **Step 3: Rode e veja passar**

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run 'TestInsightsCampanha|TestInventario|TestServidor' 2>&1 | tail -4`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/mcp/tools_insights*.go workers/internal/mcp/servidor.go
git add workers/internal/mcp/tools_insights.go workers/internal/mcp/tools_insights_test.go workers/internal/mcp/servidor.go
git commit -m "feat(mcp): insights_campanha (painel /insights com link filtrado)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: `visao_gerencial` e `assertividade`

**Files:**
- Create: `workers/internal/mcp/tools_gerencial.go`
- Test: `workers/internal/mcp/tools_gerencial_test.go`
- Modify: `workers/internal/mcp/servidor.go`

Fontes: `GET /management-overview` (`handlers/management_overview.go` → `catalog.ManagementOverview.Get`) e `GET /assertiveness` (`handlers/assertiveness.go` → `assertiveness.Repo.Get(ctx, now, Filter)`).

- [ ] **Step 1: Testes de paridade (falham)**

```go
package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/assertiveness"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/supervisor"
)

type workersFalsos []supervisor.WorkerStatus

func (w workersFalsos) WorkerStatuses() []supervisor.WorkerStatus { return w }

func TestVisaoGerencial_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 1000)
	mcptest.Cobertura(t, ctx, pool, e1, "A1", 50, 75, mcptest.CidadeAlcance{IBGE: 3550308, Nome: "São Paulo", Km: 0})
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", e1, e2)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	workers := workersFalsos{{StationID: e1.String(), Active: true}}

	h := &handlers.ManagementOverviewHandler{Repo: catalog.NewManagementOverview(pool), Workers: workers}
	var tela catalog.ManagementResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/management-overview?client_id="+cli.String()+"&from=2026-01-01&to=2026-10-28", nil, admin), &tela))

	cfg := cfgTeste(pool)
	cfg.Leitura.Workers = workers
	out := chamarTool(t, cfg, "visao_gerencial", map[string]any{"cliente_id": cli.String(), "de": "2026-01-01", "ate": "2026-10-28"})
	k := out["kpis"].(map[string]any)
	require.EqualValues(t, tela.KPIs.StationsMonitored, k["emissoras_monitoradas"])
	require.EqualValues(t, tela.KPIs.StationsLive, k["emissoras_ao_vivo"])
	require.EqualValues(t, 1, k["emissoras_ao_vivo"])
	require.EqualValues(t, tela.KPIs.StatesCount, k["estados"])
	require.EqualValues(t, tela.KPIs.MaterialsMonitored, k["materiais_monitorados"])
	require.EqualValues(t, tela.KPIs.CampaignsCount, k["campanhas"])
	require.EqualValues(t, tela.KPIs.AiringsTotal, k["veiculacoes_no_periodo"])
	require.EqualValues(t, len(tela.Stations), out["emissoras_no_mapa"])
	require.Contains(t, out["links"].(map[string]any)["tela"], "/management?")

	// Sem supervisor (CLI, testes): null, nunca 0.
	semW := chamarTool(t, cfgTeste(pool), "visao_gerencial", map[string]any{"cliente_id": cli.String()})
	require.Nil(t, semW["kpis"].(map[string]any)["emissoras_ao_vivo"])
}

func TestAssertividade_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-08-01", "2026-09-30", est)
	agora := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) // mês fechado = setembro; anterior = agosto
	mcptest.Assertividade(t, ctx, pool, mcptest.Dia("2026-09-10"), camp, est, mat, 195, 5, 3)
	mcptest.Assertividade(t, ctx, pool, mcptest.Dia("2026-08-10"), camp, est, mat, 90, 10, 0)

	h := &handlers.AssertivenessHandler{Repo: assertiveness.New(pool), NowFn: func() time.Time { return agora }}
	var tela assertiveness.Result
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/assertiveness?client_id="+cli.String(), nil, admin), &tela))

	cfg := cfgTeste(pool)
	cfg.Leitura.Agora = func() time.Time { return agora }
	out := chamarTool(t, cfg, "assertividade", map[string]any{"cliente_id": cli.String()})
	m := out["mes_fechado"].(map[string]any)
	require.Equal(t, tela.Current.Month, m["mes"])
	require.EqualValues(t, *tela.Current.Pct, m["percentual"])
	require.EqualValues(t, tela.Current.Auto, m["detectadas_sozinhas"])
	require.EqualValues(t, tela.Current.Miss, m["nao_detectadas"])
	require.EqualValues(t, 97.5, m["percentual"], "195 / (195 + 5)")
	require.EqualValues(t, 3, m["manuais_desculpadas"].(map[string]any)["sem_fingerprint"])
	require.EqualValues(t, *tela.DeltaPP, out["variacao_pp"])
	require.Contains(t, out["ressalvas"].([]any)[0], "volume")
}
```

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run 'TestVisaoGerencial|TestAssertividade' 2>&1 | tail -4`
Expected: FAIL (tools inexistentes).

- [ ] **Step 2: Implemente**

`workers/internal/mcp/tools_gerencial.go`:
```go
package mcp

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/assertiveness"
	"radiocheck/internal/catalog"
	"radiocheck/internal/supervisor"
)

type entradaGerencial struct {
	ClienteID   string   `json:"cliente_id,omitempty" jsonschema:"Só este cliente (de listar_clientes). Vazio = a operação inteira."`
	CampanhaIDs []string `json:"campanha_ids,omitempty" jsonschema:"Só estas campanhas (até 200). Vazio = todas no escopo."`
	Estado      string   `json:"estado,omitempty" jsonschema:"programada, ativa, concluida ou cancelada. Vazio = todos menos cancelada."`
	De          string   `json:"de,omitempty" jsonschema:"Primeiro dia (AAAA-MM-DD). Padrão: 1º de janeiro do ano corrente (America/Sao_Paulo)."`
	Ate         string   `json:"ate,omitempty" jsonschema:"Último dia (AAAA-MM-DD). Padrão: hoje."`
}

type gerencialDTO struct {
	Periodo struct {
		De  string `json:"de"`
		Ate string `json:"ate"`
	} `json:"periodo"`
	KPIs struct {
		EmissorasMonitoradas int   `json:"emissoras_monitoradas"`
		EmissorasAoVivo      *int  `json:"emissoras_ao_vivo"`
		Estados              int   `json:"estados"`
		MateriaisMonitorados int   `json:"materiais_monitorados"`
		Campanhas            int   `json:"campanhas"`
		VeiculacoesNoPeriodo int64 `json:"veiculacoes_no_periodo"`
		VeiculacoesHoje      int64 `json:"veiculacoes_hoje"`
	} `json:"kpis"`
	EmissorasNoMapa int      `json:"emissoras_no_mapa"`
	Links           links    `json:"links"`
	Ressalvas       []string `json:"ressalvas"`
}

const ressalvaGerencial = "Os filtros escolhem QUAIS campanhas entram; o período limita só veiculacoes_no_periodo e a sobreposição das campanhas. Sem estado, canceladas ficam fora. veiculacoes_hoje é o dia corrente, independe do período. emissoras_ao_vivo = monitoradas com worker ativo agora (null = não medido neste ambiente). A lista de emissoras do mapa e o feed ao vivo não vêm aqui: use mapa_cobertura_campanha e listar_veiculacoes."

type entradaAssertividade struct {
	ClienteID   string   `json:"cliente_id,omitempty" jsonschema:"Só este cliente (de listar_clientes). Vazio = a plataforma inteira."`
	CampanhaIDs []string `json:"campanha_ids,omitempty" jsonschema:"Só estas campanhas (até 200)."`
}

type mesAssertividade struct {
	Mes                string   `json:"mes"`
	Percentual         *float64 `json:"percentual"`
	DetectadasSozinhas int64    `json:"detectadas_sozinhas"`
	NaoDetectadas      int64    `json:"nao_detectadas"`
	ManuaisTotal       int64    `json:"manuais_total"`
	ManuaisDesculpadas struct {
		Redigitadas     int64 `json:"redigitadas"`
		SemFingerprint  int64 `json:"sem_fingerprint"`
		StreamForaDoAr  int64 `json:"stream_fora_do_ar"`
		SemMonitoramento int64 `json:"sem_monitoramento"`
	} `json:"manuais_desculpadas"`
}

type assertividadeDTO struct {
	MesFechado  mesAssertividade  `json:"mes_fechado"`
	MesAnterior *mesAssertividade `json:"mes_anterior"`
	VariacaoPP  *float64          `json:"variacao_pp"`
	Links       links             `json:"links"`
	Ressalvas   []string          `json:"ressalvas"`
}

const ressalvaAssertividade = "Repita sempre o volume junto do percentual (ex.: \"97,5% — 5 não detectadas de 200\"): a métrica só enxerga miss REPORTADO (veiculação lançada à mão que o matcher deveria ter pego), então percentual alto com pouco volume não prova nada. A janela é sempre o último mês fechado (America/Sao_Paulo) e não aceita período: o mês em curso mente para cima, porque as manuais chegam depois. percentual null = sem veiculação no mês (não é 0% nem 100%)."

func lerEscopo(clienteID string, campanhaIDs []string) (*uuid.UUID, []uuid.UUID, *sdk.CallToolResult) {
	var cliente *uuid.UUID
	if clienteID != "" {
		id, err := uuid.Parse(clienteID)
		if err != nil {
			r, _ := recusaEntrada("cliente_id")
			return nil, nil, r
		}
		cliente = &id
	}
	camps, okC := lerIDs(campanhaIDs, 200)
	if !okC {
		r, _ := recusa("campanha_ids: até 200 ids válidos", "entrada_invalida")
		return nil, nil, r
	}
	return cliente, camps, nil
}

func queryEscopo(cliente *uuid.UUID, camps []uuid.UUID) url.Values {
	q := url.Values{}
	if cliente != nil {
		q.Set("client_id", cliente.String())
	}
	if len(camps) > 0 {
		q.Set("campaigns", csvIDs(camps))
	}
	return q
}

func registrarGerencial(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "visao_gerencial",
		"Devolve os KPIs da Visão Gerencial (/management) da operação inteira ou de um recorte: emissoras monitoradas e "+
			"ao vivo agora, estados, materiais, campanhas, veiculações no período e hoje, e o link da tela. Todos os filtros "+
			"são opcionais; período padrão = 1º de janeiro até hoje. A armadilha: o período só recorta as veiculações, não as campanhas.",
		func(ctx context.Context, L *Leitura, in entradaGerencial) (*sdk.CallToolResult, error) {
			cliente, camps, rec := lerEscopo(in.ClienteID, in.CampanhaIDs)
			if rec != nil {
				return rec, nil
			}
			if in.Estado != "" && !slices.Contains(estadosCampanha, in.Estado) {
				return recusa(fmt.Sprintf("estado desconhecido %q: use programada, ativa, concluida ou cancelada", in.Estado), "entrada_invalida")
			}
			hoje := hojeSP(L.agora())
			de, ate, campo := lerDias(in.De, in.Ate, time.Date(hoje.Year(), 1, 1, 0, 0, 0, 0, time.UTC), hoje)
			if campo != "" {
				return recusaCampoDoPeriodo(campo)
			}
			res, err := L.Gerencial.Get(ctx, catalog.ManagementParams{ClientID: cliente, CampaignIDs: camps, Status: in.Estado, From: de, To: ate})
			if err != nil {
				return nil, err
			}
			var o gerencialDTO
			o.Periodo.De, o.Periodo.Ate = dia(de), dia(ate)
			k := res.KPIs
			o.KPIs.EmissorasMonitoradas = k.StationsMonitored
			o.KPIs.Estados = k.StatesCount
			o.KPIs.MateriaisMonitorados = k.MaterialsMonitored
			o.KPIs.Campanhas = k.CampaignsCount
			o.KPIs.VeiculacoesNoPeriodo = k.AiringsTotal
			o.KPIs.VeiculacoesHoje = k.AiringsToday
			if L.Workers != nil {
				n := supervisor.ContarAtivas(L.Workers.WorkerStatuses(), res.MonitoredStationIDs)
				o.KPIs.EmissorasAoVivo = &n
			}
			o.EmissorasNoMapa = len(res.Stations)
			q := queryEscopo(cliente, camps)
			if in.Estado != "" {
				q.Set("status", in.Estado)
			}
			q.Set("from", dia(de))
			q.Set("to", dia(ate))
			o.Links = links{Tela: cfg.linkTela("/management", q)}
			o.Ressalvas = []string{ressalvaGerencial}
			if in.Estado == "cancelada" {
				o.Ressalvas = append(o.Ressalvas, ressalvaCancelada)
			}
			return ok(o)
		})

	ferramentaLeitura(s, cfg, "assertividade",
		"Devolve a assertividade da detecção no último mês fechado (e no anterior): percentual de veiculações que o "+
			"matcher pegou sozinho, quantas tiveram de ser lançadas à mão por erro nosso, as manuais desculpadas por motivo, "+
			"a variação em pontos percentuais e o link da Visão Gerencial. Filtros opcionais: cliente_id, campanha_ids. "+
			"A armadilha: não aceita período e o percentual nunca se apresenta sem o volume.",
		func(ctx context.Context, L *Leitura, in entradaAssertividade) (*sdk.CallToolResult, error) {
			cliente, camps, rec := lerEscopo(in.ClienteID, in.CampanhaIDs)
			if rec != nil {
				return rec, nil
			}
			res, err := L.Assertividade.Get(ctx, L.agora(), assertiveness.Filter{ClientID: cliente, CampaignIDs: camps})
			if err != nil {
				return nil, err
			}
			o := assertividadeDTO{MesFechado: mesAssertividadeDe(res.Current), VariacaoPP: res.DeltaPP}
			if res.Previous != nil {
				m := mesAssertividadeDe(*res.Previous)
				o.MesAnterior = &m
			}
			o.Links = links{Tela: cfg.linkTela("/management", queryEscopo(cliente, camps))}
			o.Ressalvas = []string{ressalvaAssertividade}
			return ok(o)
		})
}

func mesAssertividadeDe(s assertiveness.Summary) mesAssertividade {
	m := mesAssertividade{Mes: s.Month, Percentual: s.Pct, DetectadasSozinhas: s.Auto, NaoDetectadas: s.Miss, ManuaisTotal: s.ManualTotal}
	m.ManuaisDesculpadas.Redigitadas = s.Duplicate
	m.ManuaisDesculpadas.SemFingerprint = s.NoFingerprint
	m.ManuaisDesculpadas.StreamForaDoAr = s.StreamDown
	m.ManuaisDesculpadas.SemMonitoramento = s.Unmonitored
	return m
}
```
O teste de assertividade lê `out["ressalvas"].([]any)[0]` contendo "volume": a `ressalvaAssertividade` fica SEMPRE em primeiro.

Registre `registrarGerencial(s, cfg)` depois de `registrarInsights`; em `ToolsEsperadas`, `"assertividade"` vira o PRIMEIRO item (antes de `"buscar_emissoras"`) e `"visao_gerencial"` o último (depois de `"veiculacoes_por_material"`).

- [ ] **Step 3: Rode e veja passar**

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run 'TestVisaoGerencial|TestAssertividade|TestInventario' 2>&1 | tail -4`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/mcp/tools_gerencial*.go workers/internal/mcp/servidor.go
git add workers/internal/mcp/tools_gerencial.go workers/internal/mcp/tools_gerencial_test.go workers/internal/mcp/servidor.go
git commit -m "feat(mcp): visao_gerencial e assertividade

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: `relatorio_data_hora`

**Files:**
- Create: `workers/internal/mcp/tools_relatorios.go`
- Test: `workers/internal/mcp/tools_relatorios_test.go`
- Modify: `workers/internal/mcp/servidor.go`

Fonte: `GET /detections?page=N` (`handlers/detections.go:125-182` → `catalog.Detections.ListPaged`) + o lookup de preço da tela (`catalog.PrecoDeTabela`, Task 3) sobre `catalog.Pricing.ListByCampaign`. Leia `docs/features/airtime-report.md`.

- [ ] **Step 1: Teste de paridade (falha)**

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

func TestRelatorioDataHora_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	mcptest.Preco(t, ctx, pool, camp, est, tipo, 150)
	mcptest.PMMTarget(t, ctx, pool, cli, est, 250)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "out_slot", mcptest.Quando("2026-10-11", "23:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-11-01", "08:00")) // fora do período

	ini, _ := limitesDoDiaSP(mcptest.Dia("2026-10-01"))
	_, fim := limitesDoDiaSP(mcptest.Dia("2026-10-31"))
	h := &handlers.DetectionsHandler{Repo: catalog.NewDetections(pool)}
	var tela catalog.ListPagedResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/detections?page=1&page_size=100&sort=detected_at_asc&campaigns="+camp.String()+
		"&from="+ini.Format(time.RFC3339Nano)+"&to="+fim.Format(time.RFC3339Nano), nil, admin), &tela))
	require.Equal(t, 2, tela.Total)

	out := chamarTool(t, cfgTeste(pool), "relatorio_data_hora", map[string]any{
		"cliente_id": cli.String(), "campanha_ids": []string{camp.String()}, "de": "2026-10-01", "ate": "2026-10-31", "mais_antigas_primeiro": true})
	require.EqualValues(t, tela.Total, pag(t, out)["total"])
	xs := itens(t, out)
	require.Equal(t, ids(t, xs, "id")[0], tela.Data[0].ID.String())
	require.EqualValues(t, *tela.Data[0].StationPMM, xs[0]["pmm"])
	require.EqualValues(t, *tela.Data[0].StationPMMTarget, xs[0]["pmm_target"])
	require.EqualValues(t, 150, xs[0]["valor_unitario_tabela"])
	require.Equal(t, "por_insercao", xs[0]["modo_preco"])
	require.EqualValues(t, 150, xs[1]["valor_unitario_tabela"], "a tela mostra o valor de tabela em qualquer categoria")
	require.Contains(t, out["links"].(map[string]any)["tela"], "/reports/airtime?")
	require.Contains(t, out["links"].(map[string]any)["tela"], "competence=2026-10")
}

func TestRelatorioDataHora_UmClienteEPeriodoCompleto(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	a := mcptest.Cliente(t, ctx, pool, "A")
	b := mcptest.Cliente(t, ctx, pool, "B")
	campA := mcptest.Campanha(t, ctx, pool, a, "Camp A", "2026-10-01", "2026-10-31")
	campB := mcptest.Campanha(t, ctx, pool, b, "Camp B", "2026-10-01", "2026-10-31")
	cfg := cfgTeste(pool)
	require.Contains(t, chamarToolErro(t, cfg, "relatorio_data_hora", map[string]any{
		"cliente_id": a.String(), "campanha_ids": []string{campA.String(), campB.String()}, "de": "2026-10-01", "ate": "2026-10-31"}), "Camp B")
	require.Contains(t, chamarToolErro(t, cfg, "relatorio_data_hora", map[string]any{
		"cliente_id": a.String(), "campanha_ids": []string{campA.String()}, "de": "2026-10-01"}), "[entrada_invalida]")
}
```

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run TestRelatorioDataHora 2>&1 | tail -4`
Expected: FAIL.

- [ ] **Step 2: Implemente**

`workers/internal/mcp/tools_relatorios.go` (este arquivo cresce nas Tasks 6 e 7):
```go
package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
)

type entradaDataHora struct {
	ClienteID           string   `json:"cliente_id" jsonschema:"Id do cliente (de listar_clientes); todas as campanhas têm de ser dele. Nunca invente: pergunte."`
	CampanhaIDs         []string `json:"campanha_ids" jsonschema:"De 1 a 50 campanhas do cliente (de listar_campanhas)."`
	De                  string   `json:"de" jsonschema:"Primeiro dia (AAAA-MM-DD, America/Sao_Paulo). Obrigatório."`
	Ate                 string   `json:"ate" jsonschema:"Último dia (AAAA-MM-DD). Obrigatório."`
	Busca               string   `json:"busca,omitempty" jsonschema:"Até 4 palavras; cada uma tem de aparecer em emissora, cidade, UF, banda, dial, material, tipo ou cliente."`
	MaisAntigasPrimeiro bool     `json:"mais_antigas_primeiro,omitempty" jsonschema:"true para ordem cronológica (padrão: mais recentes primeiro)."`
	Paginacao
}

type linhaDataHora struct {
	veiculacaoDTO
	Cidade              *string  `json:"cidade"`
	UF                  *string  `json:"uf"`
	Banda               *string  `json:"banda"`
	Dial                *float64 `json:"dial"`
	DuracaoSeg          *float64 `json:"duracao_seg"`
	PMM                 *float64 `json:"pmm"`
	PMMTarget           *int     `json:"pmm_target"`
	ValorUnitarioTabela *float64 `json:"valor_unitario_tabela"`
	ModoPreco           *string  `json:"modo_preco"`
}

type listaDataHora struct {
	lista[linhaDataHora]
	Links links `json:"links"`
}

const ressalvaPrecoTabela = "valor_unitario_tabela é o preço de tabela do tipo do material naquela emissora (o mesmo que a tela mostra), NÃO o faturado da linha: out_slot e out_date não valem nada e bonus é bonificação. modo_preco consolidado = a emissora tem valor fechado no contrato, sem valor por inserção; null = sem preço cadastrado. pmm_target null = sem PMM no target do cliente nessa emissora."

var nomeModoPreco = map[string]string{catalog.PricingModePerInsertion: "por_insercao", catalog.PricingModeConsolidated: "consolidado"}

// campanhasDoCliente carrega as campanhas e recusa as de outro cliente (ou inexistentes), com os nomes.
func campanhasDoCliente(ctx context.Context, L *Leitura, cliente uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]catalog.Campaign, *sdk.CallToolResult, error) {
	todas, err := todasCampanhas(ctx, L)
	if err != nil {
		return nil, nil, err
	}
	out := make(map[uuid.UUID]catalog.Campaign, len(ids))
	var alheias []string
	for _, id := range ids {
		c, achou := todas[id]
		switch {
		case !achou:
			alheias = append(alheias, id.String()+" (não existe)")
		case c.ClientID != cliente:
			alheias = append(alheias, c.Name)
		default:
			out[id] = c
		}
	}
	if len(alheias) > 0 {
		r, _ := recusa("estas campanhas não são do cliente informado: "+strings.Join(alheias, ", ")+" — o relatório é de um cliente só", "entrada_invalida")
		return nil, r, nil
	}
	return out, nil, nil
}

// linkDataHora: /reports/airtime só busca com from E to (hooks.js:505); a competência é o mês de de.
func (c ConfigServidor) linkDataHora(cliente uuid.UUID, camps []uuid.UUID, de, ate time.Time) *string {
	q := url.Values{}
	q.Set("client_id", cliente.String())
	q.Set("competence", de.Format("2006-01"))
	q.Set("campaigns", csvIDs(camps))
	q.Set("from", dia(de))
	q.Set("to", dia(ate))
	return c.linkTela("/reports/airtime", q)
}

func registrarRelatorios(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "relatorio_data_hora",
		"Lista as veiculações como o Relatório Data e Hora (/reports/airtime) de UM cliente: quando, emissora (cidade, UF, "+
			"banda, dial), material, duração, campanha, categoria, PMM, PMM no target e o valor unitário de tabela, mais o link "+
			"da tela filtrada. Pede cliente_id, 1 a 50 campanhas dele e o período completo (de e ate). Para o total por material "+
			"use veiculacoes_por_material. Lista paginada com total real.",
		func(ctx context.Context, L *Leitura, in entradaDataHora) (*sdk.CallToolResult, error) {
			cliente, err := uuid.Parse(in.ClienteID)
			if err != nil {
				return recusaEntrada("cliente_id")
			}
			camps, okC := lerIDs(in.CampanhaIDs, 50)
			if !okC || len(camps) == 0 {
				return recusa("campanha_ids precisa de 1 a 50 ids válidos", "entrada_invalida")
			}
			if in.De == "" || in.Ate == "" {
				return recusa("o relatório Data e Hora pede o período completo: passe de e ate (AAAA-MM-DD)", "entrada_invalida")
			}
			ini, fim, campo := lerPeriodoSP(in.De, in.Ate)
			if campo != "" {
				return recusaCampoDoPeriodo(campo)
			}
			if _, rec, err := campanhasDoCliente(ctx, L, cliente, camps); rec != nil || err != nil {
				return rec, err
			}
			precos := map[uuid.UUID][]catalog.StationPricing{}
			for _, c := range camps {
				ps, err := L.Precos.ListByCampaign(ctx, c)
				if err != nil {
					return nil, err
				}
				precos[c] = ps
			}
			f := catalog.ListPagedFilter{CampaignIDs: camps, StartDate: ini, EndDate: fim, Q: in.Busca}
			if in.MaisAntigasPrimeiro {
				f.Sort = "detected_at_asc"
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
			l := mapear(pg, func(d catalog.DetectionEnriched) linhaDataHora {
				v, modo := catalog.PrecoDeTabela(precos[d.CampaignID], d.CampaignID, d.StationID, d.TypeID)
				linha := linhaDataHora{veiculacaoDTO: veiculacaoDe(d), Cidade: d.StationCity, UF: d.StationState, Banda: d.StationBand,
					Dial: d.StationFrequencyMHz, DuracaoSeg: d.MaterialDurationSec, PMM: d.StationPMM, PMMTarget: d.StationPMMTarget,
					ValorUnitarioTabela: v}
				if nome, ok := nomeModoPreco[modo]; ok {
					linha.ModoPreco = &nome
				}
				return linha
			})
			l.Ressalvas = []string{ressalvaVeiculacoes, ressalvaPrecoTabela}
			d0, _ := lerDia(in.De)
			d1, _ := lerDia(in.Ate)
			return ok(listaDataHora{lista: l, Links: links{Tela: cfg.linkDataHora(cliente, camps, d0, d1)}})
		})
}
```
`listaDataHora` embute `lista[linhaDataHora]` anonimamente: o JSON sai `{items, page, ressalvas, links}` — o envelope de lista continua o do contrato, com o link ao lado. Confira com o teste que `out["items"]` e `out["page"]` existem (o `itens`/`pag` do teste já falham se não).

`fmt` pode ficar sem uso nesta task (entra na 7); se o compilador reclamar, só acrescente o import na Task 7.

Registre `registrarRelatorios(s, cfg)` depois de `registrarGerencial`; `"relatorio_data_hora"` em `ToolsEsperadas` entre `"quem_sou_eu"` e `"veiculacoes_por_material"`.

- [ ] **Step 3: Rode e veja passar**

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run 'TestRelatorioDataHora|TestInventario' 2>&1 | tail -4`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/mcp/tools_relatorios*.go workers/internal/mcp/servidor.go
git add workers/internal/mcp/tools_relatorios.go workers/internal/mcp/tools_relatorios_test.go workers/internal/mcp/servidor.go
git commit -m "feat(mcp): relatorio_data_hora (PMM, PMM no target e valor de tabela por linha)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: `resumo_relatorio_campanha`, `exportar_consolidado` e `exportar_veiculacoes`

**Files:**
- Modify: `workers/internal/mcp/tools_relatorios.go`, `workers/internal/mcp/tools_relatorios_test.go`, `workers/internal/mcp/servidor.go`

Fontes: `GET /reports/campaigns/{id}/summary` e `/consolidated.csv` (`handlers/reports.go`), `GET /detections/export` (`handlers/detections.go:613-678`). As três tools usam o período padrão = vigência da campanha (o menu da tela começa no mês corrente; a tool ecoa o período usado e o link o leva explícito).

- [ ] **Step 1: Testes (falham)**

Acrescente a `tools_relatorios_test.go` (imports extra: `bytes`, `encoding/csv`, `strings`):
```go
func cenarioRelatorio(t *testing.T) (ctx context.Context, pool *pgxpool.Pool, admin, cli, camp uuid.UUID) {
	t.Helper()
	ctx, pool = mcptest.DBCatalogo(t)
	admin = mcptest.Usuario(t, ctx, pool, "admin", true)
	cli = mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000.4)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 500)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	m1 := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	m2 := mcptest.Material(t, ctx, pool, cli, tipo, "Spot B")
	camp = mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", e1, e2)
	mcptest.PMMTarget(t, ctx, pool, cli, e1, 300)
	mcptest.Preco(t, ctx, pool, camp, e1, tipo, 100)
	mcptest.Veiculacao(t, ctx, pool, camp, m1, e1, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, m1, e1, "bonus", mcptest.Quando("2026-10-10", "09:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, m2, e1, "out_slot", mcptest.Quando("2026-10-10", "23:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, m2, e2, "in_slot", mcptest.Quando("2026-10-11", "08:00"))
	return
}

func TestResumoRelatorioCampanha_ParidadeComATela(t *testing.T) {
	_, pool, admin, _, camp := cenarioRelatorio(t)
	ini, _ := limitesDoDiaSP(mcptest.Dia("2026-10-01"))
	_, fim := limitesDoDiaSP(mcptest.Dia("2026-10-31"))
	h := &handlers.ReportsHandler{Detections: catalog.NewDetections(pool), CampaignRepo: catalog.NewCampaigns(pool), Pool: pool}
	var tela handlers.SummaryResponse
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Summary, "/x?from="+url.QueryEscape(ini.Format(time.RFC3339Nano))+
		"&to="+url.QueryEscape(fim.Format(time.RFC3339Nano)), map[string]string{"id": camp.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "resumo_relatorio_campanha", map[string]any{"campanha_id": camp.String()})
	tot := out["totais"].(map[string]any)
	require.EqualValues(t, tela.Totals.Detections, tot["veiculacoes"])
	require.EqualValues(t, tela.Totals.DistinctMaterials, tot["materiais"])
	require.EqualValues(t, tela.Totals.DistinctStations, tot["emissoras"])
	require.EqualValues(t, tela.Totals.Impactos, tot["impactos"])
	require.EqualValues(t, tela.Totals.ImpactosTarget, tot["impactos_target"])
	require.EqualValues(t, 2501, tot["impactos"], "round(1000,4 × 2) + 500 × 1")
	require.Len(t, out["por_material"], len(tela.ByMaterial))
	require.EqualValues(t, len(tela.ByStation), pag(t, out)["total"])
	require.Equal(t, map[string]any{"de": "2026-10-01", "ate": "2026-10-31"}, out["periodo"])
}

// linhasCSV conta as linhas de dados de um CSV do portal (BOM, separador ';').
func linhasCSV(t *testing.T, corpo []byte) [][]string {
	t.Helper()
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(corpo, []byte("\xef\xbb\xbf"))))
	r.Comma, r.FieldsPerRecord = ';', -1
	rs, err := r.ReadAll()
	require.NoError(t, err)
	return rs
}

func TestExportarConsolidado_LinhasBatemComOCSV(t *testing.T) {
	_, pool, admin, _, camp := cenarioRelatorio(t)
	ini, _ := limitesDoDiaSP(mcptest.Dia("2026-10-01"))
	_, fim := limitesDoDiaSP(mcptest.Dia("2026-10-31"))
	h := &handlers.ReportsHandler{Detections: catalog.NewDetections(pool), CampaignRepo: catalog.NewCampaigns(pool), Pool: pool}
	rs := linhasCSV(t, chamarHandler(t, h.Consolidated, "/x?from="+url.QueryEscape(ini.Format(time.RFC3339Nano))+
		"&to="+url.QueryEscape(fim.Format(time.RFC3339Nano)), map[string]string{"id": camp.String()}, admin))

	out := chamarTool(t, cfgTeste(pool), "exportar_consolidado", map[string]any{"campanha_id": camp.String()})
	require.EqualValues(t, len(rs)-1, out["linhas"], "linhas de dados = CSV menos o cabeçalho; ajuste se o CSV tiver rodapé")
	l := out["links"].(map[string]any)
	require.Contains(t, l["tela"], "/reports/airtime?")
	require.Contains(t, l["como_baixar"], "CSV Consolidado")
	require.NotContains(t, l["tela"], ".csv", "nunca link direto de arquivo")
}

func TestExportarVeiculacoes_LinhasBatemComOCSV(t *testing.T) {
	_, pool, admin, _, camp := cenarioRelatorio(t)
	ini, _ := limitesDoDiaSP(mcptest.Dia("2026-10-01"))
	_, fim := limitesDoDiaSP(mcptest.Dia("2026-10-31"))
	h := &handlers.DetectionsHandler{Repo: catalog.NewDetections(pool)}
	rs := linhasCSV(t, chamarHandler(t, h.Export, "/x?campaign_id="+camp.String()+"&from="+url.QueryEscape(ini.Format(time.RFC3339Nano))+
		"&to="+url.QueryEscape(fim.Format(time.RFC3339Nano)), nil, admin))

	out := chamarTool(t, cfgTeste(pool), "exportar_veiculacoes", map[string]any{"campanha_id": camp.String()})
	// O CSV detalhado tem rodapé (totais de rádios/UFs e resumo por comercial, reportcsv/footer.go):
	// conte as linhas de dados lendo o formato real e documente a conta aqui.
	require.EqualValues(t, 4, out["linhas"])
	require.Contains(t, out["links"].(map[string]any)["como_baixar"], "CSV Detalhado")
	_ = rs // use rs para provar que o CSV tem as mesmas 4 veiculações (ex.: conte as linhas cujo 1º campo é um identificador)
}
```
Antes de rodar, leia `reportcsv/reportcsv.go` e `reportcsv/footer.go` e transforme o `_ = rs` do último teste numa conta real das linhas de dados do CSV (ex.: as linhas entre o cabeçalho e a primeira linha em branco do rodapé) comparada com `out["linhas"]`. O teste prova que o número que a tool promete é o do arquivo.

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run 'TestResumoRelatorio|TestExportar' 2>&1 | tail -4`
Expected: FAIL (tools inexistentes).

- [ ] **Step 2: Implemente**

Acrescente a `tools_relatorios.go`:
```go
type entradaRelatorioCampanha struct {
	CampanhaID string `json:"campanha_id" jsonschema:"Id (uuid) da campanha, de listar_campanhas ou obter_campanha. Nunca invente: pergunte."`
	De         string `json:"de,omitempty" jsonschema:"Primeiro dia (AAAA-MM-DD, America/Sao_Paulo). Padrão: início da campanha."`
	Ate        string `json:"ate,omitempty" jsonschema:"Último dia (AAAA-MM-DD). Padrão: fim da campanha."`
}

type entradaResumoRelatorio struct {
	entradaRelatorioCampanha
	Paginacao
}

type materialResumo struct {
	Material    ref      `json:"material"`
	IDCurto     *int32   `json:"id_curto"`
	Tipo        *string  `json:"tipo"`
	DuracaoSeg  *float64 `json:"duracao_seg"`
	Veiculacoes int      `json:"veiculacoes"`
}

type emissoraResumo struct {
	Emissora        ref      `json:"emissora"`
	Cidade          *string  `json:"cidade"`
	UF              *string  `json:"uf"`
	Banda           *string  `json:"banda"`
	Dial            *float64 `json:"dial"`
	PMM             *float64 `json:"pmm"`
	PMMTarget       *int     `json:"pmm_target"`
	Veiculacoes     int      `json:"veiculacoes"`
	BaseDeImpactos  int      `json:"base_de_impactos"`
	Impactos        *int64   `json:"impactos"`
}

type resumoDTO struct {
	Campanha     campanhaInsights `json:"campanha"`
	Cliente      ref              `json:"cliente"`
	RotuloTarget *string          `json:"rotulo_target"`
	Periodo      struct {
		De  string `json:"de"`
		Ate string `json:"ate"`
	} `json:"periodo"`
	Totais struct {
		Veiculacoes        int    `json:"veiculacoes"`
		Materiais          int    `json:"materiais"`
		Emissoras          int    `json:"emissoras"`
		Impactos           int64  `json:"impactos"`
		ImpactosTarget     *int64 `json:"impactos_target"`
		EmissorasComTarget int    `json:"emissoras_com_target"`
	} `json:"totais"`
	PorMaterial      []materialResumo `json:"por_material"`
	PorMaterialTotal int              `json:"por_material_total"`
	lista[emissoraResumo]
	Links links `json:"links"`
}

const ressalvaResumo = "É o relatório da campanha (o mesmo do PDF): items é a lista por emissora, paginada. veiculacoes conta todas as categorias; impactos = round(PMM × base_de_impactos), base_de_impactos = in_slot + bonus. impactos null = emissora sem PMM. Sem valor em R$: para dinheiro use financeiro_campanhas ou insights_campanha."

const tetoMateriaisResumo = 50

// periodoDaCampanha: de/ate da entrada, ou a vigência da campanha; devolve os
// instantes de SP (o recorte das consultas) e os dias (o eco e o link).
func periodoDaCampanha(c *catalog.Campaign, de, ate string) (ini, fim *time.Time, d0, d1 time.Time, campo string) {
	d0, d1, campo = lerDias(de, ate, c.StartDate, c.EndDate)
	if campo != "" {
		return nil, nil, d0, d1, campo
	}
	i, _ := limitesDoDiaSP(d0)
	_, f := limitesDoDiaSP(d1)
	return &i, &f, d0, d1, ""
}

func registrarRelatoriosCampanha(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "resumo_relatorio_campanha",
		"Devolve o relatório de UMA campanha (o mesmo que vira PDF no portal): totais de veiculações, materiais, emissoras, "+
			"impactos e impactos no target, a contagem por material e a lista por emissora (paginada, com PMM e impactos), "+
			"mais o link da tela. Período padrão = vigência da campanha. Para baixar o CSV use exportar_consolidado.",
		func(ctx context.Context, L *Leitura, in entradaResumoRelatorio) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			ini, fim, d0, d1, campo := periodoDaCampanha(c, in.De, in.Ate)
			if campo != "" {
				return recusaCampoDoPeriodo(campo)
			}
			cli, err := L.Clientes.Get(ctx, c.ClientID)
			if err != nil {
				return nil, err
			}
			f := catalog.AggregateFilter{CampaignIDs: []uuid.UUID{c.ID}, StartDate: ini, EndDate: fim}
			porMat, err := L.Veiculacoes.AggregateByMaterial(ctx, f)
			if err != nil {
				return nil, err
			}
			porEst, err := L.Veiculacoes.AggregateByStation(ctx, f)
			if err != nil {
				return nil, err
			}
			tot := catalog.TotaisDoResumo(porMat, porEst)
			var o resumoDTO
			o.Campanha = campanhaInsights{Campanha: refDe(c.ID, c.Name), Inicio: dia(c.StartDate), Fim: dia(c.EndDate), Estado: c.Status}
			o.Cliente = refDe(cli.ID, cli.Name)
			o.RotuloTarget = cli.TargetLabel
			o.Periodo.De, o.Periodo.Ate = dia(d0), dia(d1)
			o.Totais.Veiculacoes, o.Totais.Materiais, o.Totais.Emissoras = tot.Detections, tot.DistinctMaterials, tot.DistinctStations
			o.Totais.Impactos, o.Totais.EmissorasComTarget = tot.Impactos, tot.StationsWithTarget
			if tot.StationsWithTarget > 0 {
				v := tot.ImpactosTarget
				o.Totais.ImpactosTarget = &v
			}
			mats, n := cortar(porMat.Data, tetoMateriaisResumo)
			o.PorMaterialTotal = n
			o.PorMaterial = make([]materialResumo, len(mats))
			for i, m := range mats {
				o.PorMaterial[i] = materialResumo{Material: refDe(m.MaterialID, m.MaterialTitle), IDCurto: m.MaterialShortID,
					Tipo: m.MaterialTypeName, DuracaoSeg: m.MaterialDurationSec, Veiculacoes: m.Count}
			}
			o.lista = mapear(paginar(porEst, in.Paginacao), func(s catalog.StationAggregateRow) emissoraResumo {
				return emissoraResumo{Emissora: refDe(s.StationID, s.StationName), Cidade: s.StationCity, UF: s.StationState,
					Banda: s.StationBand, Dial: s.StationFrequencyMHz, PMM: s.StationPMM, PMMTarget: s.StationPMMTarget,
					Veiculacoes: s.Count, BaseDeImpactos: s.ImpactCount, Impactos: catalog.ImpactosDaEmissora(s)}
			})
			o.Ressalvas = []string{ressalvaResumo}
			if n > tetoMateriaisResumo {
				o.Ressalvas = append(o.Ressalvas, fmt.Sprintf("por_material cortado em %d de %d materiais (por_material_total)", tetoMateriaisResumo, n))
			}
			if c.Status == "cancelada" {
				o.Ressalvas = append(o.Ressalvas, ressalvaCancelada)
			}
			o.Links = links{Tela: cfg.linkDataHora(c.ClientID, []uuid.UUID{c.ID}, d0, d1),
				ComoBaixar: "Na tela, botão Relatórios → PDF (o mesmo conteúdo) ou CSV Consolidado."}
			return ok(o)
		})

	ferramentaLeitura(s, cfg, "exportar_consolidado",
		"Prepara o download do CSV Consolidado de UMA campanha (uma linha por material × emissora, com categorias, PMM e "+
			"impactos): devolve quantas linhas o arquivo vai ter, o período e o link da tela onde baixar. O arquivo não vem "+
			"no resultado nem por link direto: o portal exige o login da pessoa. Período padrão = vigência da campanha.",
		func(ctx context.Context, L *Leitura, in entradaRelatorioCampanha) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			ini, fim, d0, d1, campo := periodoDaCampanha(c, in.De, in.Ate)
			if campo != "" {
				return recusaCampoDoPeriodo(campo)
			}
			rows, err := L.Veiculacoes.AggregateByMaterialStation(ctx, catalog.AggregateFilter{CampaignIDs: []uuid.UUID{c.ID}, StartDate: ini, EndDate: fim})
			if err != nil {
				return nil, err
			}
			return ok(exportacaoDTO(c, d0, d1, len(rows), cfg.linkDataHora(c.ClientID, []uuid.UUID{c.ID}, d0, d1),
				"Na tela, botão Relatórios → CSV Consolidado (sai com o período da tela)."))
		})

	ferramentaLeitura(s, cfg, "exportar_veiculacoes",
		"Prepara o download do CSV Detalhado de UMA campanha (uma linha por veiculação: data, hora, rádio, peça, status, "+
			"PMM e preço): devolve quantas linhas o arquivo vai ter, o período e o link da tela onde baixar. O arquivo não vem "+
			"no resultado nem por link direto. Período padrão = vigência da campanha. Para ler as linhas aqui, use relatorio_data_hora.",
		func(ctx context.Context, L *Leitura, in entradaRelatorioCampanha) (*sdk.CallToolResult, error) {
			c, rec, err := lerCampanha(ctx, L, in.CampanhaID)
			if rec != nil || err != nil {
				return rec, err
			}
			ini, fim, d0, d1, campo := periodoDaCampanha(c, in.De, in.Ate)
			if campo != "" {
				return recusaCampoDoPeriodo(campo)
			}
			n, err := contarExportacao(ctx, L, c.ID, ini, fim)
			if err != nil {
				return nil, err
			}
			return ok(exportacaoDTO(c, d0, d1, n, cfg.linkDataHora(c.ClientID, []uuid.UUID{c.ID}, d0, d1),
				"Na tela, botão Relatórios → CSV Detalhado (só admin; sai com o período da tela)."))
		})
}

func exportacaoDTO(c *catalog.Campaign, d0, d1 time.Time, linhas int, tela *string, comoBaixar string) map[string]any {
	return map[string]any{
		"campanha": campanhaInsights{Campanha: refDe(c.ID, c.Name), Inicio: dia(c.StartDate), Fim: dia(c.EndDate), Estado: c.Status},
		"periodo":  map[string]string{"de": dia(d0), "ate": dia(d1)},
		"linhas":   linhas,
		"links":    links{Tela: tela, ComoBaixar: comoBaixar},
	}
}
```
`contarExportacao` precisa contar exatamente as linhas que o `/detections/export` escreve. Antes de escrevê-la, leia `catalog.Detections.IterateForExport` (`catalog/detections.go:1474-1567`): se o filtro dele for o mesmo do `ListPaged` (mesmas exclusões de ignorada/retratada/audit, um registro por atribuição), conte com `ListPaged{CampaignIDs, StartDate, EndDate, Page: 1, PageSize: 1}.Total`:
```go
// contarExportacao: o nº de linhas do CSV Detalhado (o mesmo conjunto do
// IterateForExport, provado por TestExportarVeiculacoes_LinhasBatemComOCSV).
func contarExportacao(ctx context.Context, L *Leitura, campanha uuid.UUID, ini, fim *time.Time) (int, error) {
	res, err := L.Veiculacoes.ListPaged(ctx, catalog.ListPagedFilter{CampaignIDs: []uuid.UUID{campanha}, StartDate: ini, EndDate: fim, Page: 1, PageSize: 1})
	if err != nil {
		return 0, err
	}
	return res.Total, nil
}
```
Se os filtros diferirem, NÃO conte por `ListPaged`: acrescente ao `catalog` uma `Detections.CountForExport(ctx, ExportFilter)` que use o mesmo `WHERE` do `IterateForExport` (extraia o `WHERE` para uma função comum às duas, sem mudar o SQL do export), com teste de catalog. O teste de paridade com o CSV decide.

O handler do resumo e o do consolidado recebem `from`/`to` em RFC3339 (`time.Parse(time.RFC3339, …)` — aceita fração de segundo); o teste usa `url.QueryEscape` por causa do `+`/`-` do fuso. `chamarHandler` exige 200: o `Consolidated` responde 200 com o CSV no corpo.

Registre `registrarRelatoriosCampanha(s, cfg)` depois de `registrarRelatorios`; em `ToolsEsperadas`: `"exportar_consolidado"` e `"exportar_veiculacoes"` entre `"buscar_emissoras"` e `"financeiro_campanhas"`; `"resumo_relatorio_campanha"` entre `"relatorio_data_hora"` e `"veiculacoes_por_material"`.

- [ ] **Step 3: Rode e veja passar**

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run 'TestResumoRelatorio|TestExportar|TestRelatorioDataHora|TestInventario' 2>&1 | tail -4`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/mcp/tools_relatorios*.go workers/internal/mcp/servidor.go
git add workers/internal/mcp/tools_relatorios.go workers/internal/mcp/tools_relatorios_test.go workers/internal/mcp/servidor.go
git commit -m "feat(mcp): resumo_relatorio_campanha, exportar_consolidado e exportar_veiculacoes

As exportações devolvem o nº de linhas do arquivo (provado contra o CSV real) e
o link da tela com o botão; nunca link direto de arquivo.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: `mapa_cobertura_campanha`

**Files:**
- Create: `workers/internal/mcp/tools_mapa.go`
- Test: `workers/internal/mcp/tools_mapa_test.go`
- Modify: `workers/internal/mcp/servidor.go`

Fontes: `GET /live-map` (emissoras com raio Anatel, `catalog.LiveMap.Get`) e `GET /live-map/coverage` (cidades, `catalog.LiveMap.Coverage`) — as duas chamadas que a tela `/live-map` faz.

- [ ] **Step 1: Teste de paridade (falha)**

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

func TestMapaCoberturaCampanha_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 1000)
	mcptest.Cobertura(t, ctx, pool, e1, "A1", 50, 75,
		mcptest.CidadeAlcance{IBGE: 1, Nome: "Sede Um", Km: 0}, mcptest.CidadeAlcance{IBGE: 2, Nome: "Vizinha", Km: 40},
		mcptest.CidadeAlcance{IBGE: 3, Nome: "Longe", Km: 70})
	mcptest.Cobertura(t, ctx, pool, e2, "C", 10, 15,
		mcptest.CidadeAlcance{IBGE: 2, Nome: "Vizinha", Km: 5}) // a mesma cidade por duas emissoras
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", e1, e2)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")

	h := handlers.NewLiveMapHandler(catalog.NewLiveMap(pool))
	var cob catalog.LiveCoverageResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.GetCoverage, "/live-map/coverage?campaigns="+camp.String(), nil, admin), &cob))
	var mapa catalog.LiveMapResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/live-map?campaigns="+camp.String(), nil, admin), &mapa))

	out := chamarTool(t, cfgTeste(pool), "mapa_cobertura_campanha", map[string]any{"campanha_ids": []string{camp.String()}})
	require.EqualValues(t, len(mapa.Stations), pag(t, out)["total"])
	require.EqualValues(t, 3, out["cidades_distintas"], "Vizinha conta uma vez")
	require.EqualValues(t, len(cob.Cities), out["pares_emissora_cidade"])
	um := itens(t, out)[0]
	require.Equal(t, "Rádio Dois", um["emissora"].(map[string]any)["nome"], "ordem da tela: por nome")
	dois := itens(t, out)[1]
	require.EqualValues(t, 50, dois["contorno_protegido_km"])
	require.EqualValues(t, 75, dois["alcance_km"])
	require.EqualValues(t, 3, dois["cidades_total"])
	require.Equal(t, "Sede Um", dois["cidades"].([]any)[0].(map[string]any)["nome"])
	require.Contains(t, out["links"].(map[string]any)["tela"], "/live-map?")
}
```
Confira se `catalog.LiveMapResult` é o nome do tipo de retorno de `LiveMap.Get` (`catalog/live_map.go:117`) e a ordem das emissoras (`ORDER BY s.name`?) antes de fixar a ordem no teste.

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run TestMapaCobertura 2>&1 | tail -4`
Expected: FAIL.

- [ ] **Step 2: Implemente**

`workers/internal/mcp/tools_mapa.go`:
```go
package mcp

import (
	"context"
	"errors"
	"net/url"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
)

type entradaMapa struct {
	CampanhaIDs []string `json:"campanha_ids" jsonschema:"De 1 a 200 campanhas (de listar_campanhas). Canceladas não entram: o mapa é tela ao vivo. Nunca invente: pergunte."`
	Paginacao
}

type cidadeDTO struct {
	Nome        string  `json:"nome"`
	UF          string  `json:"uf"`
	DistanciaKm float64 `json:"distancia_km"`
	Sede        bool    `json:"sede"`
}

type emissoraMapa struct {
	Emissora            ref         `json:"emissora"`
	Cidade              *string     `json:"cidade"`
	UF                  *string     `json:"uf"`
	Banda               string      `json:"banda"`
	Dial                *float64    `json:"dial"`
	ClasseAnatel        *string     `json:"classe_anatel"`
	ContornoProtegidoKm *float64    `json:"contorno_protegido_km"`
	AlcanceKm           *float64    `json:"alcance_km"`
	UltimaVeiculacao    *string     `json:"ultima_veiculacao"`
	Cidades             []cidadeDTO `json:"cidades"`
	CidadesTotal        int         `json:"cidades_total"`
}

type mapaDTO struct {
	CidadesDistintas    int `json:"cidades_distintas"`
	ParesEmissoraCidade int `json:"pares_emissora_cidade"`
	lista[emissoraMapa]
	Links links `json:"links"`
}

const ressalvaMapa = ressalvaAnatel + " cidades de cada emissora: as 20 mais próximas (cidades_total traz o número real; obter_emissora lista até 100). cidades_distintas conta cada município uma vez, mesmo alcançado por várias emissoras. Emissora sem coordenada não aparece na lista, mas as cidades dela entram nos totais."

func registrarMapa(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "mapa_cobertura_campanha",
		"Devolve a cobertura das campanhas como o Mapa ao Vivo (/live-map): por emissora, a classe Anatel, o contorno "+
			"protegido e o alcance (transbordo) em km e as cidades no alcance; no topo, quantos municípios distintos a "+
			"campanha alcança; e o link da tela. Pede 1 a 200 campanha_ids. Lista paginada de emissoras.",
		func(ctx context.Context, L *Leitura, in entradaMapa) (*sdk.CallToolResult, error) {
			camps, okC := lerIDs(in.CampanhaIDs, 200)
			if !okC || len(camps) == 0 {
				return recusa("campanha_ids precisa de 1 a 200 ids válidos", "entrada_invalida")
			}
			mapa, err := L.MapaAoVivo.Get(ctx, camps, nil, catalog.LiveMapOpts{})
			if errors.Is(err, catalog.ErrCampaignNotFound) {
				return naoEncontrado("campanha (inexistente ou cancelada — o mapa ao vivo não mostra canceladas)")
			}
			if err != nil {
				return nil, err
			}
			cob, err := L.MapaAoVivo.Coverage(ctx, camps, nil, catalog.LiveMapOpts{})
			if err != nil {
				return nil, err
			}
			porEmissora := map[uuid.UUID][]catalog.CoverageCity{}
			distintas := map[int]bool{}
			for _, c := range cob.Cities { // já vem por emissora e distância
				porEmissora[c.StationID] = append(porEmissora[c.StationID], c)
				distintas[c.IBGECode] = true
			}
			o := mapaDTO{CidadesDistintas: len(distintas), ParesEmissoraCidade: len(cob.Cities)}
			o.lista = mapear(paginar(mapa.Stations, in.Paginacao), func(s catalog.LiveStation) emissoraMapa {
				cs, n := cortar(porEmissora[s.ID], tetoPorItem)
				e := emissoraMapa{Emissora: refDe(s.ID, s.Name), Cidade: s.City, UF: s.State, Banda: s.Band, Dial: s.FrequencyMHz,
					ClasseAnatel: s.AnatelClass, ContornoProtegidoKm: s.AnatelCoverageKm, AlcanceKm: s.AnatelReachKm,
					UltimaVeiculacao: instanteOpc(s.LastDetectionAt), CidadesTotal: n, Cidades: make([]cidadeDTO, len(cs))}
				for i, c := range cs {
					e.Cidades[i] = cidadeDTO{Nome: c.City, UF: c.State, DistanciaKm: c.DistanceKm, Sede: c.IsHome}
				}
				return e
			})
			o.Ressalvas = []string{ressalvaMapa}
			o.Links = links{Tela: cfg.linkTela("/live-map", queryMapa(ctx, L, camps))}
			return ok(o)
		})
}

// queryMapa: a tela pede o cliente antes das campanhas; o link só leva
// client_id quando todas as campanhas são do mesmo cliente.
func queryMapa(ctx context.Context, L *Leitura, camps []uuid.UUID) url.Values {
	q := url.Values{}
	q.Set("campaigns", csvIDs(camps))
	todas, err := todasCampanhas(ctx, L)
	if err != nil {
		return q
	}
	var cliente uuid.UUID
	for i, id := range camps {
		c := todas[id]
		if i > 0 && c.ClientID != cliente {
			return q
		}
		cliente = c.ClientID
	}
	q.Set("client_id", cliente.String())
	return q
}
```
`ressalvaAnatel` é a constante da Fatia 1 (`tools_emissoras.go:160`). Confira que `LiveStation.City`/`State` são `*string` e `FrequencyMHz` `*float64` (o levantamento diz que sim) e que `LastDetectionAt` é `*time.Time`.

Registre `registrarMapa(s, cfg)` depois de `registrarRelatoriosCampanha`; `"mapa_cobertura_campanha"` em `ToolsEsperadas` entre `"listar_veiculacoes"` e `"minhas_notificacoes"`.

- [ ] **Step 3: Rode e veja passar**

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run 'TestMapaCobertura|TestInventario' 2>&1 | tail -4`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/mcp/tools_mapa*.go workers/internal/mcp/servidor.go
git add workers/internal/mcp/tools_mapa.go workers/internal/mcp/tools_mapa_test.go workers/internal/mcp/servidor.go
git commit -m "feat(mcp): mapa_cobertura_campanha (raio Anatel e cidades no alcance)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 9: Falhas por emissora, por dia e o resumo diário

**Files:**
- Create: `workers/internal/mcp/tools_falhas.go`
- Test: `workers/internal/mcp/tools_falhas_test.go`
- Modify: `workers/internal/mcp/servidor.go`

Fontes: `GET /admin/station-failures` (`catalog.StationFailures.ListForDate(ctx, day, minDown)`), `GET /admin/failures-daily` (`catalog.FailuresDaily.ListDaily(ctx, from, to, minDown)`), `GET /admin/daily-failures-digest` (`catalog.CampaignFailures.ListForDate` + `digestFromDaily`, `admin_daily_failures_digest.go:39-56`). Leia os três handlers e `docs/features/admin-station-failures.md` §"Por campanha" (o déficit desta aba NÃO separa "não tocou" × "fora do horário").

**Datas:** o handler lê a data com `time.ParseInLocation(…, time.Local)` e valida contra o "hoje" local (futuro = 400; mais de 90 dias = 400); o SQL recorta por `$1::date`. A tool faz a MESMA leitura (`lerDiaFalha`), com o padrão "ontem" calculado em America/Sao_Paulo (F2-5) e ecoado em `data`.

- [ ] **Step 1: Testes de paridade (falham)**

```go
package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// cenarioFalha: campanha com meta 2/dia (06–22h) em duas emissoras; no dia d,
// Rádio Um caiu 1 h às 10h e só tocou 1 vez fora da faixa (déficit 2: 1 "fora
// do horário" + 1 "não tocou"); Rádio Dois não tocou nada (déficit 2 absent).
func cenarioFalha(t *testing.T) (ctx context.Context, pool *pgxpool.Pool, admin, camp, e1 uuid.UUID, d string) {
	t.Helper()
	ctx, pool = mcptest.DBCatalogo(t)
	admin = mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 = mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	dd := time.Now().AddDate(0, 0, -2)
	d = dd.Format("2006-01-02")
	ini, fim := dd.AddDate(0, 0, -1).Format("2006-01-02"), dd.AddDate(0, 0, 1).Format("2006-01-02")
	camp = mcptest.Campanha(t, ctx, pool, cli, "Camp", ini, fim, e1, e2)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
	mcptest.Vincular(t, ctx, pool, camp, mat, e1, e2)
	mcptest.Regra(t, ctx, pool, camp, tipo, e1, ini, fim, 127, "06:00", "22:00", 2)
	mcptest.Regra(t, ctx, pool, camp, tipo, e2, ini, fim, 127, "06:00", "22:00", 2)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "out_slot", mcptest.Quando(d, "23:00"))
	mcptest.Queda(t, ctx, pool, e1, mcptest.Quando(d, "10:00"), 3600)
	return
}

func TestFalhasEmissoras_ParidadeComATela(t *testing.T) {
	_, pool, admin, _, e1, d := cenarioFalha(t)
	h := &handlers.StationFailuresHandler{Repo: catalog.NewStationFailures(pool)}
	var tela catalog.Result
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/x?date="+d, nil, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "falhas_emissoras", map[string]any{"data": d})
	r := out["resumo"].(map[string]any)
	require.EqualValues(t, tela.Summary.StationsWithFailure, r["emissoras_com_falha"])
	require.EqualValues(t, tela.Summary.TotalDownSeconds, r["segundos_fora_do_ar"])
	require.EqualValues(t, tela.Summary.AffectedCampaigns, r["campanhas_afetadas"])
	require.EqualValues(t, len(tela.Stations), pag(t, out)["total"])
	um := itens(t, out)[0]
	require.Equal(t, e1.String(), um["emissora"].(map[string]any)["id"], "ordem da tela: mais tempo fora do ar primeiro")
	require.EqualValues(t, 3600, um["segundos_fora_do_ar"])
	c := um["campanhas"].([]any)[0].(map[string]any)
	require.EqualValues(t, 2, c["deficit"])
	require.Contains(t, c["motivos"], "stream-down")
	require.Equal(t, d, out["data"])
	require.Contains(t, out["links"].(map[string]any)["tela"], "view=by_station")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "falhas_emissoras", map[string]any{"data": time.Now().AddDate(0, 0, 3).Format("2006-01-02")}), "futuro")
}

func TestFalhasPorDia_ParidadeComATela(t *testing.T) {
	_, pool, admin, _, _, d := cenarioFalha(t)
	de := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	ate := time.Now().Format("2006-01-02")
	h := &handlers.FailuresDailyHandler{Repo: catalog.NewFailuresDaily(pool)}
	var tela catalog.FailuresDailyResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/x?from="+de+"&to="+ate, nil, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "falhas_por_dia", map[string]any{"de": de, "ate": ate})
	r := out["resumo"].(map[string]any)
	require.EqualValues(t, tela.Summary.DaysWithFailure, r["dias_com_falha"])
	require.EqualValues(t, tela.Summary.PeakStations, r["pico_emissoras"])
	require.Equal(t, tela.Summary.PeakDate, r["dia_do_pico"])
	require.Equal(t, d, r["dia_do_pico"])
	require.EqualValues(t, tela.Summary.TotalDeficit, r["deficit_total"])
	require.EqualValues(t, tela.Summary.AvgStations, r["media_emissoras_por_dia"])
	require.Len(t, itens(t, out), len(tela.Days))
}

func TestResumoDiarioFalhas_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	h := &handlers.DailyFailuresDigestHandler{Campaigns: catalog.NewCampaignFailures(pool), Seen: catalog.NewNotifications(pool)}
	var tela map[string]any
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/x", nil, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "resumo_diario_falhas", map[string]any{})
	require.Equal(t, tela["date"], out["data"])
	require.EqualValues(t, tela["summary"].(map[string]any)["campaigns"], out["campanhas_com_falha"])
	require.EqualValues(t, tela["summary"].(map[string]any)["stations"], out["emissoras_com_falha"])
	require.NotContains(t, out, "seen", "estado de UI por pessoa não sai")
}
```
O teste do digest só compara o envelope num banco sem falha no dia de ontem: semear "ontem" depende do relógio do processo, e o `cenarioFalha` usa anteontem de propósito. A paridade da lista de campanhas do digest com dado fica coberta pelo `TestFalhasCampanhas_ParidadeComATela` (Task 10, mesmo `ListForDate`).

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run 'TestFalhasEmissoras|TestFalhasPorDia|TestResumoDiarioFalhas' 2>&1 | tail -4`
Expected: FAIL.

- [ ] **Step 2: Implemente**

`workers/internal/mcp/tools_falhas.go` (cresce na Task 10):
```go
package mcp

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/catalog"
)

const (
	// tetoDiasFalha / tetoFaixaFalhas: os limites dos handlers de falha
	// (admin_station_failures.go:48, admin_failures_daily.go:17-22).
	tetoDiasFalha   = 90
	tetoFaixaFalhas = 92
	minQuedaPadrao  = 60
)

const ressalvaFalhaSemSplit = "Esta visão mostra o déficit TOTAL (esperado − in_slot). A separação \"não tocou\" × \"fora do horário\" só existe em falhas_campanhas e obter_falhas_campanha: confira lá antes de cobrar uma emissora por ausência. Só entra emissora com déficit em campanha não cancelada; queda sem déficit fica de fora. Tempos em segundos; uma queda ainda aberta cresce até ela fechar."

const ressalvaFalhasPorDia = "Série com todos os dias da faixa (zerados quando não houve falha). campanhas soma pares emissora × campanha, não campanhas distintas. Compare médias por dia, nunca somas de blocos com nº de dias diferente. segundos_fora_do_ar em segundos (a tela plota minutos). A série tem um degrau em 2026-08-17 (out_slot deixou de fechar a obrigação)."

type entradaFalhasEmissoras struct {
	Data           string `json:"data,omitempty" jsonschema:"Dia (AAAA-MM-DD), até 90 dias atrás; nunca futuro. Padrão: ontem (America/Sao_Paulo)."`
	MinQuedaSeg    *int   `json:"min_queda_seg,omitempty" jsonschema:"Ignora quedas que somam menos que isto no dia: 0, 60, 300 ou 1800 s (padrão 60)."`
	Paginacao
}

type quedaDTO struct {
	Inicio     string `json:"inicio"`
	DuracaoSeg int    `json:"duracao_seg"`
}

type campanhaComFalha struct {
	Campanha ref      `json:"campanha"`
	Cliente  string   `json:"cliente"`
	Esperado int      `json:"esperado"`
	Entregue int      `json:"entregue"`
	Deficit  int      `json:"deficit"`
	Motivos  []string `json:"motivos"`
}

type emissoraComFalha struct {
	Emissora              ref                `json:"emissora"`
	Dial                  string             `json:"dial"`
	Cidade                string             `json:"cidade"`
	SegundosForaDoAr      int                `json:"segundos_fora_do_ar"`
	TemLacunaSilenciosa   bool               `json:"tem_lacuna_silenciosa"`
	Quedas                []quedaDTO         `json:"quedas"`
	QuedasTotal           int                `json:"quedas_total"`
	Campanhas             []campanhaComFalha `json:"campanhas"`
	CampanhasTotal        int                `json:"campanhas_total"`
}

type falhasEmissorasDTO struct {
	Data   string `json:"data"`
	Resumo struct {
		EmissorasComFalha int `json:"emissoras_com_falha"`
		SegundosForaDoAr  int `json:"segundos_fora_do_ar"`
		CampanhasAfetadas int `json:"campanhas_afetadas"`
	} `json:"resumo"`
	lista[emissoraComFalha]
	Links links `json:"links"`
}

type entradaFalhasPorDia struct {
	De          string `json:"de,omitempty" jsonschema:"Primeiro dia (AAAA-MM-DD), até 90 dias atrás. Padrão: hoje − 29."`
	Ate         string `json:"ate,omitempty" jsonschema:"Último dia (AAAA-MM-DD). Padrão: hoje; futuro vira hoje. Faixa de até 92 dias."`
	MinQuedaSeg *int   `json:"min_queda_seg,omitempty" jsonschema:"0, 60, 300 ou 1800 s (padrão 60)."`
}

type diaDeFalha struct {
	Data             string `json:"data"`
	Emissoras        int    `json:"emissoras"`
	Campanhas        int    `json:"campanhas"`
	Deficit          int    `json:"deficit"`
	SegundosForaDoAr int    `json:"segundos_fora_do_ar"`
	Quedas           int    `json:"quedas"`
}

type falhasPorDiaDTO struct {
	De     string `json:"de"`
	Ate    string `json:"ate"`
	Resumo struct {
		Dias                 int     `json:"dias"`
		DiasComFalha         int     `json:"dias_com_falha"`
		PicoEmissoras        int     `json:"pico_emissoras"`
		DiaDoPico            *string `json:"dia_do_pico"`
		DeficitTotal         int     `json:"deficit_total"`
		SegundosForaDoAr     int     `json:"segundos_fora_do_ar"`
		MediaEmissorasPorDia float64 `json:"media_emissoras_por_dia"`
	} `json:"resumo"`
	lista[diaDeFalha]
	Links links `json:"links"`
}

// hojeLocal: o "hoje" dos handlers de falha (time.Local do processo).
func hojeLocal(agora time.Time) time.Time {
	t := agora.In(time.Local)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

// lerDiaFalha lê o dia como o handler (time.Local) com padrão = ontem em SP;
// recusa futuro e mais de 90 dias, como o handler.
func lerDiaFalha(s string, agora time.Time) (time.Time, *sdk.CallToolResult) {
	if s == "" {
		s = dia(ontemSP(agora))
	}
	d, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		r, _ := recusa("data tem de ser AAAA-MM-DD", "entrada_invalida")
		return d, r
	}
	hoje := hojeLocal(agora)
	if d.After(hoje) {
		r, _ := recusa("data no futuro: as falhas só existem de hoje para trás", "entrada_invalida")
		return d, r
	}
	if hoje.Sub(d) > tetoDiasFalha*24*time.Hour {
		r, _ := recusa("data com mais de 90 dias: o histórico de falhas vai até 90 dias atrás", "entrada_invalida")
		return d, r
	}
	return d, nil
}

// minQuedasDaTela: os limiares que o seletor de /admin/station-failures oferece
// (MIN_DOWN_OPTIONS). A API aceita 0..3600, mas um valor fora desta lista
// abriria o link na tela com outro limiar — e o número não bateria.
var minQuedasDaTela = []int{0, 60, 300, 1800}

func lerMinQueda(v *int) (int, *sdk.CallToolResult) {
	if v == nil {
		return minQuedaPadrao, nil
	}
	if !slices.Contains(minQuedasDaTela, *v) {
		r, _ := recusa("min_queda_seg tem de ser 0, 60, 300 ou 1800 (os limiares da tela)", "entrada_invalida")
		return 0, r
	}
	return *v, nil
}

func linkFalhas(cfg ConfigServidor, params map[string]string) *string {
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	return cfg.linkTela("/admin/station-failures", q)
}

func registrarFalhas(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "falhas_emissoras",
		"Lista as emissoras que falharam num dia (/admin/station-failures, aba Por emissora): tempo fora do ar, quedas e, "+
			"por campanha afetada, esperado, entregue, déficit e o motivo (stream-down ou silent-gap), mais o link da tela. "+
			"Padrão: ontem. A armadilha: o déficit aqui é o total — o split \"não tocou\" × \"fora do horário\" está em "+
			"falhas_campanhas. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaFalhasEmissoras) (*sdk.CallToolResult, error) {
			d, rec := lerDiaFalha(in.Data, L.agora())
			if rec != nil {
				return rec, nil
			}
			minQ, rec := lerMinQueda(in.MinQuedaSeg)
			if rec != nil {
				return rec, nil
			}
			res, err := L.FalhasEmissoras.ListForDate(ctx, d, minQ)
			if err != nil {
				return nil, err
			}
			var o falhasEmissorasDTO
			o.Data = res.Date
			o.Resumo.EmissorasComFalha = res.Summary.StationsWithFailure
			o.Resumo.SegundosForaDoAr = res.Summary.TotalDownSeconds
			o.Resumo.CampanhasAfetadas = res.Summary.AffectedCampaigns
			o.lista = mapear(paginar(res.Stations, in.Paginacao), func(sf catalog.StationFailure) emissoraComFalha {
				qs, nq := cortar(sf.Incidents, tetoPorItem)
				cs, nc := cortar(sf.Campaigns, tetoPorItem)
				e := emissoraComFalha{Emissora: refDe(sf.Station.ID, sf.Station.Name), Dial: sf.Station.Dial, Cidade: sf.Station.City,
					SegundosForaDoAr: sf.TotalDownSeconds, TemLacunaSilenciosa: sf.HasSilentGap,
					QuedasTotal: nq, CampanhasTotal: nc, Quedas: make([]quedaDTO, len(qs)), Campanhas: make([]campanhaComFalha, len(cs))}
				for i, q := range qs {
					e.Quedas[i] = quedaDTO{Inicio: instante(q.EventAt), DuracaoSeg: q.DurationSeconds}
				}
				for i, c := range cs {
					e.Campanhas[i] = campanhaComFalha{Campanha: refDe(c.CampaignID, c.CampaignName), Cliente: c.ClientName,
						Esperado: c.Expected, Entregue: c.Delivered, Deficit: c.Deficit, Motivos: c.AffectedBy}
				}
				return e
			})
			o.Ressalvas = []string{ressalvaFalhaSemSplit}
			o.Links = links{Tela: linkFalhas(cfg, map[string]string{"view": "by_station", "date": res.Date, "min_down": strconv.Itoa(minQ)})}
			return ok(o)
		})

	ferramentaLeitura(s, cfg, "falhas_por_dia",
		"Devolve a série diária de falhas (/admin/station-failures, aba Por dia): por dia, emissoras com falha, pares "+
			"emissora × campanha, déficit, tempo fora do ar e quedas; no resumo, dias com falha, o pico e a média de "+
			"emissoras por dia. Padrão: últimos 30 dias; faixa de até 92 dias, no máximo 90 dias para trás.",
		func(ctx context.Context, L *Leitura, in entradaFalhasPorDia) (*sdk.CallToolResult, error) {
			minQ, rec := lerMinQueda(in.MinQuedaSeg)
			if rec != nil {
				return rec, nil
			}
			hoje := hojeLocal(L.agora())
			de, ate, campo := lerDias(in.De, in.Ate, hoje.AddDate(0, 0, -29), hoje)
			if campo != "" {
				return recusaCampoDoPeriodo(campo)
			}
			// lerDias devolve meia-noite UTC; os limites do handler são em time.Local.
			de = time.Date(de.Year(), de.Month(), de.Day(), 0, 0, 0, 0, time.Local)
			ate = time.Date(ate.Year(), ate.Month(), ate.Day(), 0, 0, 0, 0, time.Local)
			if ate.After(hoje) {
				ate = hoje // o handler corta o futuro em hoje
			}
			if de.After(ate) {
				return recusa("período inválido: ate tem de ser >= de", "entrada_invalida")
			}
			if hoje.Sub(de) > tetoDiasFalha*24*time.Hour {
				return recusa("de com mais de 90 dias: o histórico de falhas vai até 90 dias atrás", "entrada_invalida")
			}
			if ate.Sub(de) > (tetoFaixaFalhas-1)*24*time.Hour {
				return recusa("faixa de até 92 dias", "entrada_invalida")
			}
			res, err := L.FalhasPorDia.ListDaily(ctx, de, ate, minQ)
			if err != nil {
				return nil, err
			}
			var o falhasPorDiaDTO
			o.De, o.Ate = res.From, res.To
			sm := res.Summary
			o.Resumo.Dias, o.Resumo.DiasComFalha, o.Resumo.PicoEmissoras = sm.DaysTotal, sm.DaysWithFailure, sm.PeakStations
			if sm.PeakDate != "" {
				pd := sm.PeakDate
				o.Resumo.DiaDoPico = &pd
			}
			o.Resumo.DeficitTotal, o.Resumo.SegundosForaDoAr, o.Resumo.MediaEmissorasPorDia = sm.TotalDeficit, sm.TotalDownSeconds, sm.AvgStations
			dias := make([]diaDeFalha, len(res.Days))
			for i, p := range res.Days {
				dias[i] = diaDeFalha{Data: p.Date, Emissoras: p.Stations, Campanhas: p.Campaigns, Deficit: p.Deficit,
					SegundosForaDoAr: p.DownSeconds, Quedas: p.DownEvents}
			}
			o.lista = listaInteira(dias)
			o.Ressalvas = []string{ressalvaFalhasPorDia}
			o.Links = links{Tela: linkFalhas(cfg, map[string]string{"view": "by_day", "from": res.From, "to": res.To, "min_down": strconv.Itoa(minQ)})}
			return ok(o)
		})

	ferramentaLeitura(s, cfg, "resumo_diario_falhas",
		"Devolve o resumo diário de falhas de ontem (o mesmo do aviso que o admin vê ao abrir o portal): quantas campanhas "+
			"e emissoras falharam e, por campanha, quantas emissoras. Não marca nada como visto. Para o detalhe use "+
			"falhas_campanhas (modo dia) ou falhas_emissoras.",
		func(ctx context.Context, L *Leitura, _ struct{}) (*sdk.CallToolResult, error) {
			d, rec := lerDiaFalha("", L.agora())
			if rec != nil {
				return rec, nil
			}
			res, err := L.FalhasCampanhas.ListForDate(ctx, d)
			if err != nil {
				return nil, err
			}
			// O mesmo mapeamento do digestFromDaily (admin_daily_failures_digest.go:39-56).
			type campanhaDigest struct {
				Campanha          ref    `json:"campanha"`
				Cliente           string `json:"cliente"`
				EmissorasComFalha int    `json:"emissoras_com_falha"`
			}
			cs := make([]campanhaDigest, len(res.Campaigns))
			for i, c := range res.Campaigns {
				cs[i] = campanhaDigest{Campanha: refDe(c.Campaign.ID, c.Campaign.Name), Cliente: c.Campaign.ClientName, EmissorasComFalha: len(c.Stations)}
			}
			return ok(map[string]any{
				"data":                res.Date,
				"campanhas_com_falha": res.Summary.Campaigns,
				"emissoras_com_falha": res.Summary.Stations,
				"campanhas":           cs,
				"links":               links{Tela: linkFalhas(cfg, map[string]string{"view": "by_campaign", "date": res.Date})},
			})
		})
}

```
A Task 10 acrescenta `errors`, `fmt` e `github.com/google/uuid` aos imports quando passar a usá-los. Confira se `DailyFailuresDigestHandler` aceita `*catalog.CampaignFailures` e `*catalog.Notifications` direto nos campos `Campaigns`/`Seen` (o levantamento diz que sim, `main.go:660-664`).

Registre `registrarFalhas(s, cfg)` depois de `registrarMapa`; em `ToolsEsperadas`: `"falhas_emissoras"` e `"falhas_por_dia"` entre `"exportar_veiculacoes"` e `"financeiro_campanhas"` (nesta ordem); `"resumo_diario_falhas"` entre `"relatorio_data_hora"` e `"resumo_relatorio_campanha"`.

- [ ] **Step 3: Rode e veja passar**

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run 'TestFalhasEmissoras|TestFalhasPorDia|TestResumoDiarioFalhas|TestInventario' 2>&1 | tail -4`
Expected: `ok`. Se `TestFalhasEmissoras` falhar por fuso (o `$1::date` do SQL depende do TimeZone da sessão do Postgres), não "conserte" a consulta: confira se a tela dá o mesmo resultado no mesmo cenário — a paridade é o critério — e reporte.

- [ ] **Step 4: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/mcp/tools_falhas*.go workers/internal/mcp/servidor.go
git add workers/internal/mcp/tools_falhas.go workers/internal/mcp/tools_falhas_test.go workers/internal/mcp/servidor.go
git commit -m "feat(mcp): falhas_emissoras, falhas_por_dia e resumo_diario_falhas

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: `falhas_campanhas` e `obter_falhas_campanha`

**Files:**
- Modify: `workers/internal/mcp/tools_falhas.go`, `workers/internal/mcp/tools_falhas_test.go`, `workers/internal/mcp/servidor.go`

Fonte: `GET /admin/campaign-failures` (`ListForDate` no modo dia, `ListHistorical(page, size)` no histórico) e `GET /admin/campaign-failures/{id}` (`Get`). Leia `docs/features/admin-campaign-failures.md` (o split `deficit_absent` × `deficit_off_slot`).

- [ ] **Step 1: Testes de paridade (falham)**

Acrescente a `tools_falhas_test.go`:
```go
func TestFalhasCampanhas_ParidadeComATela(t *testing.T) {
	_, pool, admin, camp, _, d := cenarioFalha(t)
	h := &handlers.CampaignFailuresHandler{Repo: catalog.NewCampaignFailures(pool)}
	var tela catalog.DailyResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.GetList, "/x?date="+d, nil, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "falhas_campanhas", map[string]any{"data": d})
	r := out["resumo"].(map[string]any)
	require.EqualValues(t, tela.Summary.Campaigns, r["campanhas"])
	require.EqualValues(t, tela.Summary.TotalDeficit, r["deficit"])
	require.EqualValues(t, tela.Summary.TotalDeficitAbsent, r["deficit_nao_tocou"])
	require.EqualValues(t, tela.Summary.TotalDeficitOffSlot, r["deficit_fora_do_horario"])
	c := itens(t, out)[0]
	require.Equal(t, camp.String(), c["campanha"].(map[string]any)["id"])
	require.EqualValues(t, len(tela.Campaigns[0].Stations), c["emissoras_total"])

	var hist catalog.HistoricalResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.GetList, "/x?mode=historical&page=1&page_size=50", nil, admin), &hist))
	outH := chamarTool(t, cfgTeste(pool), "falhas_campanhas", map[string]any{"modo": "historico"})
	require.EqualValues(t, hist.Total, pag(t, outH)["total"])
	require.EqualValues(t, hist.Campaigns[0].TotalDeficitAbsent, itens(t, outH)[0]["deficit_nao_tocou"])
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "falhas_campanhas", map[string]any{"modo": "historico", "data": d}), "[entrada_invalida]")
}

func TestObterFalhasCampanha_ParidadeComATela(t *testing.T) {
	_, pool, admin, camp, e1, _ := cenarioFalha(t)
	h := &handlers.CampaignFailuresHandler{Repo: catalog.NewCampaignFailures(pool)}
	var tela catalog.DetailResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.GetByID, "/x", map[string]string{"id": camp.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "obter_falhas_campanha", map[string]any{"campanha_id": camp.String()})
	r := out["resumo"].(map[string]any)
	require.EqualValues(t, tela.Summary.TotalDeficit, r["deficit"])
	require.EqualValues(t, tela.Summary.TotalDeficitAbsent, r["deficit_nao_tocou"])
	require.EqualValues(t, tela.Summary.TotalDeficitOffSlot, r["deficit_fora_do_horario"])
	require.EqualValues(t, tela.Summary.TotalFailureDays, r["dias_de_falha"])
	var um map[string]any
	for _, x := range itens(t, out) {
		if x["emissora"].(map[string]any)["id"] == e1.String() {
			um = x
		}
	}
	require.NotNil(t, um)
	require.EqualValues(t, 1, um["deficit_fora_do_horario"], "tocou 1 vez fora da faixa")
	require.EqualValues(t, 1, um["deficit_nao_tocou"])
	require.Contains(t, out["links"].(map[string]any)["tela"], "campaign="+camp.String())
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_falhas_campanha", map[string]any{"campanha_id": uuid.NewString()}), "[nao_encontrado]")
}
```
O cenário tem dias de vigência sem tocada antes e depois de `d`; o horizonte "até ontem" (`failureHorizonClause`) inclui ontem — os números exatos de `deficit` agregados podem passar de 4. Os asserts comparam com a tela; os dois números fixos (`1` e `1` da Rádio Um) só valem se a Rádio Um tiver déficit apenas em `d` e nos dias vizinhos sem tocada: confira o valor da tela e, se o agregado incluir os dias vizinhos (déficit 2 cada, todo "não tocou"), troque os fixos por `tela`-derivados e acrescente um assert independente do split de `d` via `falhas_campanhas` modo dia (`failure_days_on_date`). Não afrouxe o teste para "qualquer número".

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run 'TestFalhasCampanhas|TestObterFalhasCampanha' 2>&1 | tail -4`
Expected: FAIL.

- [ ] **Step 2: Implemente**

Acrescente a `tools_falhas.go` (e tire as duas linhas `var _` da Task 9):
```go
const ressalvaSplit = "deficit = esperado − in_slot (out_slot não fecha a obrigação). deficit_nao_tocou = a emissora não tocou; deficit_fora_do_horario = tocou, mas fora da faixa — só o primeiro é ausência cobrável. extras = out_slot + out_date + bonus; bonificada = extras cobrem o déficit (provisório em campanha ativa). Horizonte: até ontem (America/Sao_Paulo). Canceladas ficam fora."

type entradaFalhasCampanhas struct {
	Modo string `json:"modo,omitempty" jsonschema:"dia (padrão): as campanhas com emissora falhando no dia; historico: todas as campanhas com falha na vigência."`
	Data string `json:"data,omitempty" jsonschema:"Só no modo dia: AAAA-MM-DD, até 90 dias atrás. Padrão: ontem."`
	Paginacao
}

type emissoraDeficit struct {
	Emissora              ref      `json:"emissora"`
	Dial                  string   `json:"dial"`
	Cidade                string   `json:"cidade"`
	Programado            int      `json:"programado"`
	Identificado          int      `json:"identificado"`
	Deficit               int      `json:"deficit"`
	DeficitNaoTocou       int      `json:"deficit_nao_tocou"`
	DeficitForaDoHorario  int      `json:"deficit_fora_do_horario"`
	Extras                int      `json:"extras"`
	Bonificada            bool     `json:"bonificada"`
	DiasDeFalha           []string `json:"dias_de_falha,omitempty"`
}

func emissoraDeficitDe(s catalog.CampaignFailureStation) emissoraDeficit {
	return emissoraDeficit{Emissora: refDe(s.Station.ID, s.Station.Name), Dial: s.Station.Dial, Cidade: s.Station.City,
		Programado: s.Programmed, Identificado: s.Identified, Deficit: s.Deficit, DeficitNaoTocou: s.DeficitAbsent,
		DeficitForaDoHorario: s.DeficitOffSlot, Extras: s.Extras, Bonificada: s.IsBonified, DiasDeFalha: s.FailureDays}
}

type campanhaFalhaDia struct {
	Campanha       ref               `json:"campanha"`
	Cliente        string            `json:"cliente"`
	Estado         string            `json:"estado"`
	Emissoras      []emissoraDeficit `json:"emissoras"`
	EmissorasTotal int               `json:"emissoras_total"`
}

type campanhaFalhaHistorico struct {
	Campanha             ref    `json:"campanha"`
	Cliente              string `json:"cliente"`
	Estado               string `json:"estado"`
	EmissorasComFalha    int    `json:"emissoras_com_falha"`
	DiasDeFalha          int    `json:"dias_de_falha"`
	Deficit              int    `json:"deficit"`
	DeficitNaoTocou      int    `json:"deficit_nao_tocou"`
	DeficitForaDoHorario int    `json:"deficit_fora_do_horario"`
	TotalmenteBonificada bool   `json:"totalmente_bonificada"`
}

// deficitSplit: o déficit e a sua divisão, comum ao modo dia e ao detalhe.
type deficitSplit struct {
	Deficit              int `json:"deficit"`
	DeficitNaoTocou      int `json:"deficit_nao_tocou"`
	DeficitForaDoHorario int `json:"deficit_fora_do_horario"`
}

// Sem omitempty nas contagens: 0 campanhas com falha é zero, não ausência.
type resumoDia struct {
	Campanhas int `json:"campanhas"`
	Emissoras int `json:"emissoras"`
	deficitSplit
}

type resumoDetalhe struct {
	Emissoras   int `json:"emissoras"`
	DiasDeFalha int `json:"dias_de_falha"`
	deficitSplit
}

func registrarFalhasCampanhas(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "falhas_campanhas",
		"Lista as campanhas com falha (/admin/station-failures, aba Por campanha). Modo dia (padrão: ontem): as campanhas "+
			"com emissora falhando no dia, cada uma com as emissoras e o déficit partido em \"não tocou\" × \"fora do horário\" "+
			"(números da vigência até ontem). Modo historico: uma linha por campanha com falha. Para o detalhe de uma, "+
			"obter_falhas_campanha. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaFalhasCampanhas) (*sdk.CallToolResult, error) {
			switch in.Modo {
			case "", "dia":
			case "historico":
				if in.Data != "" {
					return recusa("data só vale no modo dia", "entrada_invalida")
				}
				pg, err := buscarJanela(in.Paginacao, func(page, size int) ([]catalog.CampaignHistoricalRow, int, error) {
					res, err := L.FalhasCampanhas.ListHistorical(ctx, page, size)
					if err != nil {
						return nil, 0, err
					}
					return res.Campaigns, res.Total, nil
				})
				if err != nil {
					return nil, err
				}
				out := mapear(pg, func(r catalog.CampaignHistoricalRow) campanhaFalhaHistorico {
					return campanhaFalhaHistorico{Campanha: refDe(r.Campaign.ID, r.Campaign.Name), Cliente: r.Campaign.ClientName,
						Estado: r.Campaign.Status, EmissorasComFalha: r.StationsWithFailure, DiasDeFalha: r.TotalFailureDays,
						Deficit: r.TotalDeficit, DeficitNaoTocou: r.TotalDeficitAbsent, DeficitForaDoHorario: r.TotalDeficitOffSlot,
						TotalmenteBonificada: r.IsFullyBonified}
				})
				out.Ressalvas = []string{ressalvaSplit}
				return ok(struct {
					lista[campanhaFalhaHistorico]
					Links links `json:"links"`
				}{out, links{Tela: linkFalhas(cfg, map[string]string{"view": "by_campaign", "sub": "historical"})}})
			default:
				return recusa(fmt.Sprintf("modo desconhecido %q: use dia ou historico", in.Modo), "entrada_invalida")
			}
			d, rec := lerDiaFalha(in.Data, L.agora())
			if rec != nil {
				return rec, nil
			}
			res, err := L.FalhasCampanhas.ListForDate(ctx, d)
			if err != nil {
				return nil, err
			}
			sm := res.Summary
			l := mapear(paginar(res.Campaigns, in.Paginacao), func(c catalog.CampaignDailyFailure) campanhaFalhaDia {
				es, n := cortar(c.Stations, tetoPorItem)
				o := campanhaFalhaDia{Campanha: refDe(c.Campaign.ID, c.Campaign.Name), Cliente: c.Campaign.ClientName,
					Estado: c.Campaign.Status, EmissorasTotal: n, Emissoras: make([]emissoraDeficit, len(es))}
				for i, e := range es {
					o.Emissoras[i] = emissoraDeficitDe(e)
				}
				return o
			})
			l.Ressalvas = []string{ressalvaSplit, "Modo dia: entram só as emissoras que falharam no dia, mas os números delas somam a vigência inteira até ontem. emissoras cortadas em 20 por campanha (emissoras_total traz o real; obter_falhas_campanha lista todas)."}
			return ok(struct {
				Data   string    `json:"data"`
				Resumo resumoDia `json:"resumo"`
				lista[campanhaFalhaDia]
				Links links `json:"links"`
			}{res.Date, resumoDia{Campanhas: sm.Campaigns, Emissoras: sm.Stations, deficitSplit: deficitSplit{Deficit: sm.TotalDeficit,
				DeficitNaoTocou: sm.TotalDeficitAbsent, DeficitForaDoHorario: sm.TotalDeficitOffSlot}}, l,
				links{Tela: linkFalhas(cfg, map[string]string{"view": "by_campaign", "date": res.Date})}})
		})

	ferramentaLeitura(s, cfg, "obter_falhas_campanha",
		"Devolve as falhas de UMA campanha na vigência até ontem (o drill-in de /admin/station-failures): por emissora, "+
			"programado, identificado, déficit partido em \"não tocou\" × \"fora do horário\", extras, se está bonificada e "+
			"os dias de falha; e o link que abre o detalhe na tela (de onde sai o PDF de cobrança). Lista paginada de emissoras.",
		func(ctx context.Context, L *Leitura, in entradaCampanhaPaginada) (*sdk.CallToolResult, error) {
			id, err := uuid.Parse(in.CampanhaID)
			if err != nil {
				return recusaEntrada("campanha_id")
			}
			res, err := L.FalhasCampanhas.Get(ctx, id)
			if errors.Is(err, catalog.ErrCampaignNotFound) {
				return naoEncontrado("campanha (inexistente ou cancelada — cancelada não entra em falhas)")
			}
			if err != nil {
				return nil, err
			}
			sm := res.Summary
			l := mapear(paginar(res.Stations, in.Paginacao), emissoraDeficitDe)
			l.Ressalvas = []string{ressalvaSplit}
			if res.Campaign.Status == "ativa" {
				l.Ressalvas = append(l.Ressalvas, "Campanha ativa: os números ainda mudam (o déficit pode ser compensado até o fim da vigência).")
			}
			return ok(struct {
				Campanha campanhaInsights `json:"campanha"`
				Cliente  string           `json:"cliente"`
				Resumo   resumoDetalhe    `json:"resumo"`
				lista[emissoraDeficit]
				Links links `json:"links"`
			}{campanhaInsights{Campanha: refDe(res.Campaign.ID, res.Campaign.Name), Inicio: res.Campaign.StartDate,
				Fim: res.Campaign.EndDate, Estado: res.Campaign.Status}, res.Campaign.ClientName,
				resumoDetalhe{Emissoras: sm.StationsWithFailure, DiasDeFalha: sm.TotalFailureDays, deficitSplit: deficitSplit{
					Deficit: sm.TotalDeficit, DeficitNaoTocou: sm.TotalDeficitAbsent, DeficitForaDoHorario: sm.TotalDeficitOffSlot}}, l,
				links{Tela: linkFalhas(cfg, map[string]string{"view": "by_campaign", "campaign": id.String()}),
					ComoBaixar: "Na tela, o detalhe da campanha abre sozinho; o PDF de cobrança é o botão dele."}})
		})
}
```
Acrescente `"errors"`, `"fmt"` e `"github.com/google/uuid"` aos imports. `deficitSplit` é embutido nos dois resumos: o JSON sai plano (`{campanhas, emissoras, deficit, deficit_nao_tocou, deficit_fora_do_horario}`), que é o que os testes leem.

Registre `registrarFalhasCampanhas(s, cfg)` depois de `registrarFalhas`; em `ToolsEsperadas`: `"falhas_campanhas"` entre `"exportar_veiculacoes"` e `"falhas_emissoras"`; `"obter_falhas_campanha"` entre `"obter_emissora"` e `"obter_material"`.

- [ ] **Step 3: Rode e veja passar**

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run 'TestFalhas|TestObterFalhasCampanha|TestResumoDiarioFalhas|TestInventario' 2>&1 | tail -4`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/mcp/tools_falhas*.go workers/internal/mcp/servidor.go
git add workers/internal/mcp/tools_falhas.go workers/internal/mcp/tools_falhas_test.go workers/internal/mcp/servidor.go
git commit -m "feat(mcp): falhas_campanhas e obter_falhas_campanha (déficit \"não tocou\" × \"fora do horário\")

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Pós-venda — `listar_pos_vendas`, `obter_pos_venda`, `destinatarios_pos_venda`

**Files:**
- Create: `workers/internal/mcp/tools_posvenda.go`
- Test: `workers/internal/mcp/tools_posvenda_test.go`
- Modify: `workers/internal/mcp/servidor.go`

Fontes: `GET /post-sale/reports` (`postsale.Repo.List`), `GET /post-sale/reports/{id}` (`Repo.Get`) + o payload congelado (`Repo.Payload`, o que `/preview` devolve de um enviado), `GET /post-sale/reports/{id}/recipients` e `GET /post-sale/recipients?client_id=` (`Repo.ActiveClientUsers` + `Repo.InternalRecipients`). Leia `docs/features/post-sale.md` e `handlers/post_sale.go` inteiro.

- [ ] **Step 1: Testes (falham)**

```go
package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/postsale"
)

func TestPosVenda_ParidadeESemSegredos(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-09-01", "2026-09-30")
	payload := &postsale.Payload{Version: postsale.PayloadVersion, Title: "Fechamento", Campaigns: []postsale.CampaignBlock{{
		CampaignID: camp, Name: "Camp", PeriodFrom: "2026-09-01", PeriodTo: "2026-09-30",
		KPIs:         postsale.BlockKPIs{ValorEntregue: 12000, Impactos: 300000, CPM: 40, StationsCount: 3},
		CheckingRows: []postsale.StationRow{{Name: "Rádio X", Programmed: 10, Identified: 8, Deficit: 2}}, ConformingCount: 2,
	}}}
	enviado := mcptest.PosVenda(t, ctx, pool, cli, camp, "Fechamento", "2026-09-01", "2026-09-30", true, payload,
		postsale.RecipientInput{Email: "a@cliente.com", Name: "Ana"}, postsale.RecipientInput{Email: "b@cliente.com", Name: "Bia"})
	_ = mcptest.PosVenda(t, ctx, pool, cli, camp, "Rascunho", "2026-09-01", "2026-09-30", false, nil)
	_, err := pool.Exec(ctx, `UPDATE post_sale_reports SET attachments_url = 'https://drive.google.com/d/SEGREDO/view' WHERE id = $1`, enviado)
	require.NoError(t, err)

	h := handlers.NewPostSaleHandler(postsale.New(postsale.Config{Repo: postsale.NewRepo(pool)}), zap.NewNop())
	var tela postsale.ListPage
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/x?per_page=100", nil, admin), &tela))
	cfg := cfgTeste(pool)
	l := chamarTool(t, cfg, "listar_pos_vendas", map[string]any{})
	require.EqualValues(t, tela.Total, pag(t, l)["total"])
	require.EqualValues(t, tela.Counts.Sent, l["contagens"].(map[string]any)["enviados"])

	var rep postsale.Report
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/x", map[string]string{"id": enviado.String()}, admin), &rep))
	texto, erro := chamarNoServidor(t, novoServidor(cfg, principalTeste), "obter_pos_venda", map[string]any{"pos_venda_id": enviado.String()})
	require.False(t, erro, texto)
	for _, r := range rep.Recipients {
		require.NotContains(t, texto, r.Token, "o token é a credencial do link público")
	}
	require.NotContains(t, texto, "/pos-venda/")
	require.NotContains(t, texto, "SEGREDO")
	var o map[string]any
	require.NoError(t, json.Unmarshal([]byte(texto), &o))
	bloco := o["campanhas"].([]any)[0].(map[string]any)
	require.EqualValues(t, 12000, bloco["numeros"].(map[string]any)["valor_entregue"])
	require.EqualValues(t, 2, bloco["emissoras_conformes"])
	require.EqualValues(t, 1, o["destinatarios"].(map[string]any)["abriram"])

	rasc := itens(t, chamarTool(t, cfg, "listar_pos_vendas", map[string]any{"estado": "rascunho"}))[0]
	or := chamarTool(t, cfg, "obter_pos_venda", map[string]any{"pos_venda_id": rasc["id"]})
	require.Nil(t, or["campanhas"].([]any)[0].(map[string]any)["numeros"], "rascunho não tem números congelados")

	d := chamarTool(t, cfg, "destinatarios_pos_venda", map[string]any{"pos_venda_id": enviado.String()})
	require.Len(t, itens(t, d), 2)
	require.NotContains(t, itens(t, d)[0], "token")
	require.Contains(t, chamarToolErro(t, cfg, "destinatarios_pos_venda", map[string]any{}), "[entrada_invalida]")
	require.True(t, strings.HasSuffix(*cfg.link("/admin/pos-venda/"+enviado.String()), enviado.String()))
}
```
Confira `postsale.PayloadVersion` (constante em `payload.go:13`), `ListCounts` (`Sent`/`Draft`/`All`?) e se `postsale.New(Config{Repo})` sem os outros campos não dá panic (o `List`/`Get` só usam `h.svc.Repo()`). Se `New` exigir mais, monte o mínimo que ele pede, nunca S3 real.

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run TestPosVenda 2>&1 | tail -4`
Expected: FAIL.

- [ ] **Step 2: Implemente**

`workers/internal/mcp/tools_posvenda.go`:
```go
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/postsale"
)

var estadosPosVenda = map[string]string{"enviado": "sent", "rascunho": "draft"}
var nomeEstadoPosVenda = map[string]string{"sent": "enviado", "draft": "rascunho"}

const ressalvaPosVenda = "O pós-venda enviado é um documento CONGELADO: os números são os do dia do envio e não mudam se a campanha for recategorizada depois — divergir de insights_campanha é esperado. Rascunho não tem números (numeros: null). O link que o cliente abre e o token dele nunca saem daqui; o anexo sai só como domínio."

type entradaListarPosVendas struct {
	Busca       string `json:"busca,omitempty" jsonschema:"Casa no título ou no nome do cliente."`
	ClienteID   string `json:"cliente_id,omitempty" jsonschema:"Só deste cliente (de listar_clientes)."`
	Competencia string `json:"competencia,omitempty" jsonschema:"AAAA-MM: pós-vendas cujo período cruza o mês."`
	Estado      string `json:"estado,omitempty" jsonschema:"enviado ou rascunho. Vazio = todos."`
	Paginacao
}

type posVendaItem struct {
	ID            string  `json:"id"`
	Titulo        string  `json:"titulo"`
	Cliente       ref     `json:"cliente"`
	Estado        string  `json:"estado"`
	EnviadoEm     *string `json:"enviado_em"`
	CriadoEm      string  `json:"criado_em"`
	Campanhas     int     `json:"campanhas"`
	Destinatarios int     `json:"destinatarios"`
	Abriram       int     `json:"abriram"`
	PeriodoDe     *string `json:"periodo_de"`
	PeriodoAte    *string `json:"periodo_ate"`
	Tela          *string `json:"tela"`
}

type entradaPosVenda struct {
	PosVendaID string `json:"pos_venda_id" jsonschema:"Id do pós-venda (de listar_pos_vendas). Nunca invente: pergunte."`
}

type numerosPosVenda struct {
	ValorEntregue      float64 `json:"valor_entregue"`
	Impactos           int64   `json:"impactos"`
	CPM                float64 `json:"cpm"`
	Bonificacao        float64 `json:"bonificacao"`
	BonificacaoInsercoes int64 `json:"bonificacao_insercoes"`
	Emissoras          int     `json:"emissoras"`
	ImpactosTarget     int64   `json:"impactos_target"`
	CPMTarget          float64 `json:"cpm_target"`
	EmissorasComTarget int     `json:"emissoras_com_target"`
	RotuloTarget       *string `json:"rotulo_target"`
	Consolidado        bool    `json:"consolidado"`
	AjustadoAMao       bool    `json:"ajustado_a_mao"`
}

type checkingDTO struct {
	Emissora     string `json:"emissora"`
	Programado   int    `json:"programado"`
	Identificado int    `json:"identificado"`
	Deficit      int    `json:"deficit"`
	Extras       int    `json:"extras"`
	Situacao     string `json:"situacao"`
	Nota         string `json:"nota,omitempty"`
}

type blocoPosVenda struct {
	Campanha           ref              `json:"campanha"`
	PeriodoDe          string           `json:"periodo_de"`
	PeriodoAte         string           `json:"periodo_ate"`
	Numeros            *numerosPosVenda `json:"numeros"`
	Checking           []checkingDTO    `json:"checking"`
	CheckingTotal      int              `json:"checking_total"`
	EmissorasConformes *int             `json:"emissoras_conformes"`
}

type destinatarioDTO struct {
	Nome        string  `json:"nome"`
	Email       string  `json:"email"`
	StatusEmail string  `json:"status_email"`
	ErroEmail   *string `json:"erro_email"`
	AbertoEm    *string `json:"aberto_em"`
	Aberturas   int     `json:"aberturas"`
	RevogadoEm  *string `json:"revogado_em"`
}

func destinatarioDe(r postsale.Recipient) destinatarioDTO { // whitelist: o Token NUNCA entra
	return destinatarioDTO{Nome: r.Name, Email: r.Email, StatusEmail: r.EmailStatus, ErroEmail: r.EmailError,
		AbertoEm: instanteOpc(r.OpenedAt), Aberturas: r.OpenCount, RevogadoEm: instanteOpc(r.RevokedAt)}
}

func checkingDe(rows []postsale.StationRow) ([]checkingDTO, int) {
	rs, n := cortar(rows, tetoPorItem)
	out := make([]checkingDTO, len(rs))
	for i, r := range rs {
		out[i] = checkingDTO{Emissora: r.Name, Programado: r.Programmed, Identificado: r.Identified, Deficit: r.Deficit,
			Extras: r.Extras, Situacao: string(r.Kind), Nota: r.Note}
	}
	return out, n
}

func registrarPosVenda(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "listar_pos_vendas",
		"Lista os pós-vendas (/admin/pos-venda): título, cliente, estado (enviado/rascunho), datas, nº de campanhas, de "+
			"destinatários e de quem abriu, período e o link da tela de cada um; mais as contagens por estado. Filtros: busca, "+
			"cliente_id, competencia (AAAA-MM), estado. Lista paginada.",
		func(ctx context.Context, L *Leitura, in entradaListarPosVendas) (*sdk.CallToolResult, error) {
			f := postsale.ListFilter{Q: in.Busca, Month: in.Competencia}
			if in.ClienteID != "" {
				id, err := uuid.Parse(in.ClienteID)
				if err != nil {
					return recusaEntrada("cliente_id")
				}
				f.ClientID = &id
			}
			if in.Estado != "" {
				st, ok := estadosPosVenda[in.Estado]
				if !ok {
					return recusa(fmt.Sprintf("estado desconhecido %q: use enviado ou rascunho", in.Estado), "entrada_invalida")
				}
				f.Status = st
			}
			var contagens postsale.ListCounts
			pg, err := buscarJanela(in.Paginacao, func(page, size int) ([]postsale.ListItem, int, error) {
				f.Page, f.PerPage = page, size
				res, err := L.PosVenda.List(ctx, f)
				if err != nil {
					return nil, 0, err
				}
				contagens = res.Counts
				return res.Items, res.Total, nil
			})
			if err != nil {
				return nil, err
			}
			l := mapear(pg, func(it postsale.ListItem) posVendaItem {
				return posVendaItem{ID: it.ID.String(), Titulo: it.Title, Cliente: refDe(it.ClientID, it.ClientName),
					Estado: nomeEstadoPosVenda[it.Status], EnviadoEm: instanteOpc(it.SentAt), CriadoEm: instante(it.CreatedAt),
					Campanhas: it.Campaigns, Destinatarios: it.Recipients, Abriram: it.Opened,
					PeriodoDe: diaOpc(it.PeriodFrom), PeriodoAte: diaOpc(it.PeriodTo), Tela: cfg.link("/admin/pos-venda/" + it.ID.String())}
			})
			return ok(struct {
				lista[posVendaItem]
				Contagens map[string]int `json:"contagens"`
			}{l, map[string]int{"todos": contagens.All, "enviados": contagens.Sent, "rascunhos": contagens.Draft}})
		})

	ferramentaLeitura(s, cfg, "obter_pos_venda",
		"Devolve um pós-venda: cliente, estado, datas, o resumo dos destinatários e, por campanha, o período, os números "+
			"congelados no envio (valor entregue, impactos, CPM, bonificação, target) e o checking das emissoras fora do "+
			"plano; mais o link da tela. Rascunho vem sem números. Para a lista de destinatários, destinatarios_pos_venda.",
		func(ctx context.Context, L *Leitura, in entradaPosVenda) (*sdk.CallToolResult, error) {
			id, err := uuid.Parse(in.PosVendaID)
			if err != nil {
				return recusaEntrada("pos_venda_id")
			}
			rep, err := L.PosVenda.Get(ctx, id)
			if errors.Is(err, postsale.ErrNotFound) {
				return naoEncontrado("pós-venda")
			}
			if err != nil {
				return nil, err
			}
			congelados := map[uuid.UUID]postsale.CampaignBlock{}
			if rep.Status == "sent" {
				raw, err := L.PosVenda.Payload(ctx, id)
				if err != nil {
					return nil, err
				}
				var p postsale.Payload
				if err := json.Unmarshal(raw, &p); err != nil {
					return nil, err
				}
				for _, b := range p.Campaigns {
					congelados[b.CampaignID] = b
				}
			}
			blocos := make([]blocoPosVenda, len(rep.Blocks))
			for i, b := range rep.Blocks {
				bl := blocoPosVenda{Campanha: refDe(b.CampaignID, b.CampaignName), PeriodoDe: dia(b.From), PeriodoAte: dia(b.To)}
				if cb, ok := congelados[b.CampaignID]; ok {
					k := cb.KPIs
					bl.Numeros = &numerosPosVenda{ValorEntregue: k.ValorEntregue, Impactos: k.Impactos, CPM: k.CPM, Bonificacao: k.Bonificacao,
						BonificacaoInsercoes: k.BonificacaoCount, Emissoras: k.StationsCount, ImpactosTarget: k.ImpactosTarget,
						CPMTarget: k.CPMTarget, EmissorasComTarget: k.StationsWithTarget, RotuloTarget: k.TargetLabel,
						Consolidado: k.Consolidated, AjustadoAMao: k.Overridden}
					bl.Checking, bl.CheckingTotal = checkingDe(cb.CheckingRows)
					n := cb.ConformingCount
					bl.EmissorasConformes = &n
				} else {
					bl.Checking, bl.CheckingTotal = checkingDe(b.CheckingRows)
				}
				blocos[i] = bl
			}
			// Chaves sempre presentes: 0 é zero, não ausência.
			dest := map[string]int{"total": len(rep.Recipients), "abriram": 0, "falharam": 0, "revogados": 0}
			for _, r := range rep.Recipients {
				if r.OpenedAt != nil {
					dest["abriram"]++
				}
				if r.EmailStatus == "failed" {
					dest["falharam"]++
				}
				if r.RevokedAt != nil {
					dest["revogados"]++
				}
			}
			var anexo *string
			if rep.AttachmentsURL != "" {
				a := soEsquemaEHost(rep.AttachmentsURL)
				anexo = &a
			}
			return ok(map[string]any{
				"id": rep.ID.String(), "titulo": rep.Title, "cliente": refDe(rep.ClientID, rep.ClientName),
				"estado": nomeEstadoPosVenda[rep.Status], "enviado_em": instanteOpc(rep.SentAt), "criado_em": instante(rep.CreatedAt),
				"mensagem": rep.IntroMessage, "anexo": anexo, "campanhas": blocos, "destinatarios": dest,
				"links":     links{Tela: cfg.link("/admin/pos-venda/" + rep.ID.String())},
				"ressalvas": []string{ressalvaPosVenda, "checking cortado em 20 linhas por campanha (checking_total traz o real)."},
			})
		})

	ferramentaLeitura(s, cfg, "destinatarios_pos_venda",
		"Com pos_venda_id: lista quem recebeu aquele pós-venda (nome, email, status do email, quando abriu, quantas vezes, "+
			"se o link foi revogado). Com cliente_id: quem RECEBERIA um pós-venda novo desse cliente hoje (usuários ativos do "+
			"cliente + admins internos que pediram cópia). Passe exatamente um dos dois.",
		func(ctx context.Context, L *Leitura, in struct {
			PosVendaID string `json:"pos_venda_id,omitempty" jsonschema:"Id do pós-venda (de listar_pos_vendas)."`
			ClienteID  string `json:"cliente_id,omitempty" jsonschema:"Id do cliente (de listar_clientes), para a prévia de quem receberia."`
		}) (*sdk.CallToolResult, error) {
			if (in.PosVendaID == "") == (in.ClienteID == "") {
				return recusa("passe pos_venda_id OU cliente_id (exatamente um dos dois)", "entrada_invalida")
			}
			if in.PosVendaID != "" {
				id, err := uuid.Parse(in.PosVendaID)
				if err != nil {
					return recusaEntrada("pos_venda_id")
				}
				rs, err := L.PosVenda.Recipients(ctx, id)
				if err != nil {
					return nil, err
				}
				if len(rs) == 0 {
					if _, err := L.PosVenda.Get(ctx, id); errors.Is(err, postsale.ErrNotFound) {
						return naoEncontrado("pós-venda")
					}
				}
				ds := make([]destinatarioDTO, len(rs))
				for i, r := range rs {
					ds[i] = destinatarioDe(r)
				}
				return ok(listaInteira(ds))
			}
			cid, err := uuid.Parse(in.ClienteID)
			if err != nil {
				return recusaEntrada("cliente_id")
			}
			if _, rec, err := lerCliente(ctx, L, cid.String()); rec != nil || err != nil {
				return rec, err
			}
			doCliente, err := L.PosVenda.ActiveClientUsers(ctx, cid)
			if err != nil {
				return nil, err
			}
			internos, err := L.PosVenda.InternalRecipients(ctx)
			if err != nil {
				return nil, err
			}
			pessoas := func(xs []postsale.RecipientInput) []map[string]string {
				out := make([]map[string]string, len(xs))
				for i, x := range xs {
					out[i] = map[string]string{"nome": x.Name, "email": x.Email}
				}
				return out
			}
			return ok(map[string]any{"cliente": pessoas(doCliente), "internos": pessoas(internos),
				"ressalvas": []string{"É uma prévia: quem receberia se o pós-venda fosse enviado hoje. internos = admins com cópia de todos os pós-vendas ligada."}})
		})
}
```
O teste chama `destinatarios_pos_venda` com o `pos_venda_id` e espera `itens` (lista): o ramo com `pos_venda_id` devolve `listaInteira`. Confira `postsale.ErrNotFound` (o handler usa em `fail`, `post_sale.go:40-50`) e que `Repo.Recipients` de um id inexistente devolve lista vazia (não erro) — o ramo `len(rs) == 0` trata isso. `RowKind` é string (`above|compensation|conforming`): confira e, se for outro tipo, converta.

O `PosVenda` da Task 2 grava o payload com `Campaigns[].CampaignID = camp`; o bloco congelado casa com o `BlockRow` pelo `CampaignID`.

Registre `registrarPosVenda(s, cfg)` depois de `registrarFalhasCampanhas`; em `ToolsEsperadas`: `"destinatarios_pos_venda"` entre `"buscar_emissoras"` e `"exportar_consolidado"`; `"listar_pos_vendas"` entre `"listar_overrides"` e `"listar_precos_campanha"`; `"obter_pos_venda"` entre `"obter_material"` e `"obter_usuario"`.

- [ ] **Step 3: Rode e veja passar**

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run 'TestPosVenda|TestInventario' 2>&1 | tail -4`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/mcp/tools_posvenda*.go workers/internal/mcp/servidor.go
git add workers/internal/mcp/tools_posvenda.go workers/internal/mcp/tools_posvenda_test.go workers/internal/mcp/servidor.go
git commit -m "feat(mcp): listar_pos_vendas, obter_pos_venda e destinatarios_pos_venda

Números congelados do envio; nunca o token nem o link público do cliente;
anexo só como domínio.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: `auditoria_mcp`

**Files:**
- Modify: `workers/internal/mcp/auditoria/auditoria.go`, `workers/internal/mcp/auditoria/auditoria_test.go`
- Create: `workers/internal/mcp/tools_auditoria.go`, `workers/internal/mcp/tools_auditoria_test.go`
- Modify: `workers/internal/mcp/servidor.go`

- [ ] **Step 1: Teste do `Listar` por cursor (falha)**

Acrescente a `workers/internal/mcp/auditoria/auditoria_test.go` (use o mesmo helper de banco que o arquivo já usa; leia-o antes):
```go
func TestListar_SoMCPMaisRecentePrimeiroComCursor(t *testing.T) {
	ctx, pool := dbDeTeste(t) // o helper que o arquivo já tem
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)
	ator := uuid.New()
	for i := 0; i < 3; i++ {
		require.NoError(t, Registrar(ctx, pool, ator, fmt.Sprintf("mcp.token.criado.%d", i), "mcp_token", nil, map[string]any{"n": i}))
	}
	_, err = pool.Exec(ctx, `INSERT INTO audit_log (actor_type, action, resource) VALUES ('system', 'client.hub_link', 'client')`) // sem origem mcp
	require.NoError(t, err)

	p1, prox, err := Listar(ctx, pool, Filtro{Limite: 2})
	require.NoError(t, err)
	require.Len(t, p1, 2)
	require.Equal(t, "mcp.token.criado.2", p1[0].Acao)
	require.NotEmpty(t, prox)
	p2, prox2, err := Listar(ctx, pool, Filtro{Limite: 2, Cursor: prox})
	require.NoError(t, err)
	require.Len(t, p2, 1)
	require.Equal(t, "mcp.token.criado.0", p2[0].Acao)
	require.Empty(t, prox2)
	_, _, err = Listar(ctx, pool, Filtro{Limite: 2, Cursor: "lixo"})
	require.ErrorIs(t, err, ErrCursorInvalido)
	f, _, err := Listar(ctx, pool, Filtro{Limite: 10, AcaoPrefixo: "mcp.token.criado.1"})
	require.NoError(t, err)
	require.Len(t, f, 1)
}
```
Se as três linhas forem gravadas no mesmo instante (`created_at` igual), a ordem entre elas é decidida pelo `id` (uuid aleatório) e o teste fica instável: grave-as com `created_at` explícito e crescente (`UPDATE audit_log SET created_at = now() - interval 'N seconds' WHERE action = …`) depois do `Registrar`.

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/auditoria/ -run TestListar 2>&1 | tail -3`
Expected: FAIL (`undefined: Listar`).

- [ ] **Step 2: Implemente o `Listar`**

Acrescente a `auditoria.go` (imports: `encoding/base64`, `errors`, `strings`, `time`, `github.com/jackc/pgx/v5`):
```go
// Consultor é o que Listar precisa (pool ou tx).
type Consultor interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

var ErrCursorInvalido = errors.New("auditoria: cursor inválido")

type Filtro struct {
	AcaoPrefixo string     // "" = todas; "mcp.token" casa mcp.token.*
	AtorID      *uuid.UUID // nil = todos
	Cursor      string     // "" = do mais recente
	Limite      int
}

type Evento struct {
	ID        uuid.UUID
	Quando    time.Time
	AtorID    *uuid.UUID
	AtorNome  *string
	AtorEmail *string
	TipoAtor  string
	Acao      string
	Recurso   string
	RecursoID *uuid.UUID
	Detalhes  map[string]any
}

// Listar devolve os eventos gravados pelo MCP (metadata.origem = 'mcp'), do
// mais recente para o mais antigo, por cursor (created_at, id). proximo vazio
// = fim. O id (uuid v4) só desempata instantes iguais.
func Listar(ctx context.Context, db Consultor, f Filtro) (eventos []Evento, proximo string, err error) {
	var antesDe *time.Time
	var antesID *uuid.UUID
	if f.Cursor != "" {
		t, id, ok := lerCursor(f.Cursor)
		if !ok {
			return nil, "", ErrCursorInvalido
		}
		antesDe, antesID = &t, &id
	}
	rows, err := db.Query(ctx, `
		SELECT a.id, a.created_at, a.actor_id, u.name, u.email, a.actor_type, a.action, a.resource, a.resource_id, a.metadata
		FROM audit_log a
		LEFT JOIN users u ON u.id = a.actor_id
		WHERE a.metadata->>'origem' = 'mcp'
		  AND ($1 = '' OR a.action LIKE $1 || '%')
		  AND ($2::uuid IS NULL OR a.actor_id = $2)
		  AND ($3::timestamptz IS NULL OR (a.created_at, a.id) < ($3, $4::uuid))
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT $5`, strings.ReplaceAll(f.AcaoPrefixo, "%", ""), f.AtorID, antesDe, antesID, f.Limite+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var e Evento
		var meta []byte
		if err := rows.Scan(&e.ID, &e.Quando, &e.AtorID, &e.AtorNome, &e.AtorEmail, &e.TipoAtor, &e.Acao, &e.Recurso, &e.RecursoID, &meta); err != nil {
			return nil, "", err
		}
		if len(meta) > 0 {
			if err := json.Unmarshal(meta, &e.Detalhes); err != nil {
				return nil, "", err
			}
			delete(e.Detalhes, "origem")
		}
		eventos = append(eventos, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(eventos) > f.Limite {
		eventos = eventos[:f.Limite]
		u := eventos[len(eventos)-1]
		proximo = base64.RawURLEncoding.EncodeToString([]byte(u.Quando.UTC().Format(time.RFC3339Nano) + "|" + u.ID.String()))
	}
	return eventos, proximo, nil
}

func lerCursor(s string) (time.Time, uuid.UUID, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	partes := strings.SplitN(string(b), "|", 2)
	if len(partes) != 2 {
		return time.Time{}, uuid.Nil, false
	}
	t, err1 := time.Parse(time.RFC3339Nano, partes[0])
	id, err2 := uuid.Parse(partes[1])
	return t, id, err1 == nil && err2 == nil
}
```
O `json` já é importado no arquivo? Se não, acrescente `encoding/json`. Precisão: o Postgres guarda microssegundos e o `RFC3339Nano` do Go os preserva; o `<` da tupla compara exato.

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/auditoria/ 2>&1 | tail -3`
Expected: `ok`.

- [ ] **Step 3: A tool (teste e implementação)**

`workers/internal/mcp/tools_auditoria_test.go`:
```go
package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/auditoria"
	"radiocheck/internal/mcp/mcptest"
)

func TestAuditoriaMCP_CursorETotalNulo(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	require.NoError(t, auditoria.Registrar(ctx, pool, admin, "mcp.token.criado", "mcp_token", nil, map[string]any{"nome": "laptop"}))
	require.NoError(t, auditoria.Registrar(ctx, pool, admin, "mcp.token.revogado", "mcp_token", nil, nil))
	_, err = pool.Exec(ctx, `UPDATE audit_log SET created_at = now() - interval '1 hour' WHERE action = 'mcp.token.criado'`)
	require.NoError(t, err)

	cfg := cfgTeste(pool)
	p1 := chamarTool(t, cfg, "auditoria_mcp", map[string]any{"limit": 1})
	require.Nil(t, p1["page"].(map[string]any)["total"])
	require.Equal(t, "mcp.token.revogado", itens(t, p1)[0]["acao"])
	cur := p1["page"].(map[string]any)["next_cursor"].(string)
	p2 := chamarTool(t, cfg, "auditoria_mcp", map[string]any{"limit": 1, "cursor": cur})
	e := itens(t, p2)[0]
	require.Equal(t, "mcp.token.criado", e["acao"])
	require.Equal(t, "laptop", e["detalhes"].(map[string]any)["nome"])
	require.NotContains(t, e["detalhes"], "origem")
	require.Equal(t, admin.String(), e["ator"].(map[string]any)["id"])
	require.False(t, p2["page"].(map[string]any)["has_more"].(bool))
	require.Contains(t, chamarToolErro(t, cfg, "auditoria_mcp", map[string]any{"cursor": "lixo"}), "[entrada_invalida]")
}
```
Confira se `mcptest.DB` apaga o `audit_log` (o comentário em `mcptest.go:6-7` diz que NÃO): por isso o `DELETE` explícito.

`workers/internal/mcp/tools_auditoria.go`:
```go
package mcp

import (
	"context"
	"errors"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/mcp/auditoria"
)

type entradaAuditoria struct {
	Acao      string `json:"acao,omitempty" jsonschema:"Prefixo da ação (ex.: mcp.token, mcp.oauth). Vazio = todas."`
	UsuarioID string `json:"usuario_id,omitempty" jsonschema:"Só deste usuário (de listar_usuarios)."`
	Cursor    string `json:"cursor,omitempty" jsonschema:"page.next_cursor da página anterior. Vazio = do mais recente."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Quantos eventos: padrão 25, máximo 100."`
}

type eventoDTO struct {
	ID        string         `json:"id"`
	Quando    string         `json:"quando"`
	Ator      *atorDTO       `json:"ator"`
	TipoAtor  string         `json:"tipo_ator"`
	Acao      string         `json:"acao"`
	Recurso   string         `json:"recurso"`
	RecursoID *string        `json:"recurso_id"`
	Detalhes  map[string]any `json:"detalhes"`
}

type atorDTO struct {
	ID    string  `json:"id"`
	Nome  *string `json:"nome"`
	Email *string `json:"email"`
}

func registrarAuditoria(s *sdk.Server, cfg ConfigServidor) {
	ferramentaLeitura(s, cfg, "auditoria_mcp",
		"Lista o que foi registrado pelo MCP do E-monitor (tokens criados/revogados/recusados, autorizações OAuth, clientes "+
			"cadastrados por CLI e, a partir da Fatia 3, as escritas), do mais recente ao mais antigo. Filtros: acao (prefixo), "+
			"usuario_id. Pagina por cursor: page.total vem null (não contado); siga page.next_cursor enquanto has_more for true.",
		func(ctx context.Context, L *Leitura, in entradaAuditoria) (*sdk.CallToolResult, error) {
			limit, _ := Paginacao{Limit: in.Limit}.normalizar()
			f := auditoria.Filtro{AcaoPrefixo: in.Acao, Cursor: in.Cursor, Limite: limit}
			if in.UsuarioID != "" {
				id, err := uuid.Parse(in.UsuarioID)
				if err != nil {
					return recusaEntrada("usuario_id")
				}
				f.AtorID = &id
			}
			evs, prox, err := auditoria.Listar(ctx, L.Pool, f)
			if errors.Is(err, auditoria.ErrCursorInvalido) {
				return recusa("cursor inválido: use o page.next_cursor exato da página anterior, ou nenhum", "entrada_invalida")
			}
			if err != nil {
				return nil, err
			}
			out := make([]eventoDTO, len(evs))
			for i, e := range evs {
				d := eventoDTO{ID: e.ID.String(), Quando: instante(e.Quando), TipoAtor: e.TipoAtor, Acao: e.Acao, Recurso: e.Recurso, Detalhes: e.Detalhes}
				if e.Detalhes == nil {
					d.Detalhes = map[string]any{}
				}
				if e.AtorID != nil {
					d.Ator = &atorDTO{ID: e.AtorID.String(), Nome: e.AtorNome, Email: e.AtorEmail}
				}
				if e.RecursoID != nil {
					s := e.RecursoID.String()
					d.RecursoID = &s
				}
				out[i] = d
			}
			return ok(novaListaCursor(out, limit, prox))
		})
}
```
Registre `registrarAuditoria(s, cfg)` depois de `registrarPosVenda`; `"auditoria_mcp"` em `ToolsEsperadas` entre `"assertividade"` e `"buscar_emissoras"`.

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run 'TestAuditoriaMCP|TestInventario' 2>&1 | tail -4`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/mcp/auditoria/auditoria*.go workers/internal/mcp/tools_auditoria*.go workers/internal/mcp/servidor.go
git add workers/internal/mcp/auditoria/auditoria.go workers/internal/mcp/auditoria/auditoria_test.go workers/internal/mcp/tools_auditoria.go workers/internal/mcp/tools_auditoria_test.go workers/internal/mcp/servidor.go
git commit -m "feat(mcp): auditoria_mcp por cursor (total null)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 13: Frontend — as telas leem os filtros da URL

**Files:**
- Create: `frontend/src/utils/filtrosDaUrl.js`, `frontend/src/utils/filtrosDaUrl.test.mjs`
- Modify: `frontend/src/pages/InsightsPage.jsx` (~:30-50), `frontend/src/pages/ManagementPage.jsx` (~:128-160), `frontend/src/pages/LiveMapPage.jsx` (~:126-131), `frontend/src/pages/AdminStationFailuresPage.jsx` (~:428-455 e o `<FailuresDailyView>` ~:661), `frontend/src/components/FailuresDailyView.jsx` (~:330-342)

**Regra 5 do CLAUDE.md:** NUNCA `npm install`. Esta task não adiciona dependência. Para lint e build, `npm ci` (instala do lockfile sem reescrevê-lo) e confira no fim que `frontend/package-lock.json` não mudou.

- [ ] **Step 1: Teste do parser (falha)**

`frontend/src/utils/filtrosDaUrl.test.mjs`:
```js
// Filtros que as tools do MCP põem na URL das telas (links.tela).
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { lerId, lerIds, lerPeriodo, lerOpcao, lerInteiro, temFiltro } from './filtrosDaUrl.js'

const A = '11111111-1111-4111-8111-111111111111'
const B = '22222222-2222-4222-8222-222222222222'
const sp = (q) => new URLSearchParams(q)

test('ids: só uuid válido passa; lista em csv', () => {
  assert.equal(lerId(sp(`client_id=${A}`), 'client_id'), A)
  assert.equal(lerId(sp('client_id=lixo'), 'client_id'), null)
  assert.equal(lerId(sp(''), 'client_id'), null)
  assert.deepEqual(lerIds(sp(`campaigns=${A},lixo, ${B}`), 'campaigns'), [A, B])
  assert.deepEqual(lerIds(sp(''), 'campaigns'), [])
})

test('período: dia válido e ordem; senão null', () => {
  assert.deepEqual(lerPeriodo(sp('from=2026-10-01&to=2026-10-31')), { from: '2026-10-01', to: '2026-10-31' })
  assert.equal(lerPeriodo(sp('from=2026-10-31&to=2026-10-01')), null, 'to antes de from seria 400 na API')
  assert.equal(lerPeriodo(sp('from=2026-13-01&to=2026-10-31')), null)
  assert.equal(lerPeriodo(sp('from=2026-10-01')), null, 'meio período não vale')
})

test('opção e inteiro só dentro do conjunto aceito pela tela', () => {
  assert.equal(lerOpcao(sp('status=ativa'), 'status', ['ativa', 'concluida']), 'ativa')
  assert.equal(lerOpcao(sp('status=xpto'), 'status', ['ativa']), null)
  assert.equal(lerInteiro(sp('min_down=300'), 'min_down', [0, 60, 300, 1800]), 300)
  assert.equal(lerInteiro(sp('min_down=0'), 'min_down', [0, 60]), 0)
  assert.equal(lerInteiro(sp('min_down=120'), 'min_down', [0, 60]), null)
  assert.equal(lerInteiro(sp(''), 'min_down', [0, 60]), null)
})

test('temFiltro', () => {
  assert.equal(temFiltro(sp('from=2026-10-01'), ['client_id', 'from']), true)
  assert.equal(temFiltro(sp('x=1'), ['client_id', 'from']), false)
})
```

Run: `cd C:/tmp/emonitor-mcp/frontend && node --test src/utils/filtrosDaUrl.test.mjs 2>&1 | tail -5`
Expected: FAIL (`Cannot find module './filtrosDaUrl.js'`).

- [ ] **Step 2: Implemente o parser**

`frontend/src/utils/filtrosDaUrl.js`:
```js
// Leitura dos filtros que chegam na URL — os links que as tools do MCP
// devolvem (links.tela) abrem a tela já filtrada. Só o primeiro render lê a
// URL; depois a tela segue com o próprio estado (como /admin/station-failures
// já fazia). Tudo é validado aqui: um valor torto vira "sem filtro", nunca um
// pedido que a API recusa.

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const DIA = /^(\d{4})-(\d{2})-(\d{2})$/

export function lerId(sp, chave) {
  const v = (sp.get(chave) ?? '').trim()
  return UUID.test(v) ? v : null
}

export function lerIds(sp, chave) {
  return (sp.get(chave) ?? '').split(',').map(s => s.trim()).filter(s => UUID.test(s))
}

function diaValido(v) {
  const m = DIA.exec(v ?? '')
  if (!m) return false
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]))
  return d.getFullYear() === Number(m[1]) && d.getMonth() === Number(m[2]) - 1 && d.getDate() === Number(m[3])
}

// lerPeriodo: { from, to } com os dois dias válidos e from <= to; senão null.
export function lerPeriodo(sp, de = 'from', ate = 'to') {
  const from = sp.get(de), to = sp.get(ate)
  if (!diaValido(from) || !diaValido(to) || from > to) return null
  return { from, to }
}

export function lerOpcao(sp, chave, validas) {
  const v = sp.get(chave)
  return validas.includes(v) ? v : null
}

export function lerInteiro(sp, chave, validos) {
  const bruto = sp.get(chave)
  if (bruto == null || bruto === '') return null
  const n = Number(bruto)
  return validos.includes(n) ? n : null
}

export function temFiltro(sp, chaves) {
  return chaves.some(k => sp.has(k))
}
```

Run: `cd C:/tmp/emonitor-mcp/frontend && node --test src/utils/filtrosDaUrl.test.mjs 2>&1 | tail -5`
Expected: `# pass 4`, `# fail 0`.

- [ ] **Step 3: `/insights` lê `client_id`, `campaigns`, `from`, `to`, `stations`, `materials`**

Em `frontend/src/pages/InsightsPage.jsx`: importe `useSearchParams` (de `react-router-dom`, como as outras páginas) e `{ lerId, lerIds, lerPeriodo, temFiltro }` de `../utils/filtrosDaUrl`; troque o `useState({...})` de `filters` (~:34-41) por:
```jsx
  const [searchParams, setSearchParams] = useSearchParams()
  const [filters, setFilters] = useState(() => {
    // Link de outra tela ou do MCP (links.tela): a tela abre já filtrada.
    const periodo = lerPeriodo(searchParams)
    return {
      clientId: canPickClient ? lerId(searchParams, 'client_id') : (user?.client_id ?? null),
      campaignIds: lerIds(searchParams, 'campaigns'),
      from: periodo?.from ?? firstOfMonthISO(),
      to: periodo?.to ?? lastOfMonthISO(),
      stationIds: lerIds(searchParams, 'stations'),
      materialIds: lerIds(searchParams, 'materials'),
    }
  })
  // Lido uma vez: tira da URL para ela não contradizer o filtro que a pessoa mudar depois.
  useEffect(() => {
    if (temFiltro(searchParams, ['client_id', 'campaigns', 'from', 'to', 'stations', 'materials'])) {
      setSearchParams({}, { replace: true })
    }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps
```
(acrescente `useEffect` ao import do React se faltar). Não mexa no `FiltersBar`: ele reseta campanhas/emissoras só no `onChange` de cliente/campanhas, que o estado inicial não dispara. A campanha que estiver fora da página 1/200 de `useCampaignsPaged` não aparece como chip, mas continua no estado e no fetch — limitação conhecida, registrada no doc (Task 16).

- [ ] **Step 4: `/management` lê `client_id`, `campaigns`, `status`, `from`, `to`**

Em `frontend/src/pages/ManagementPage.jsx` (~:128-160): mesmos imports; troque os `useState` iniciais por:
```jsx
  const [searchParams, setSearchParams] = useSearchParams()
  const [clientId, setClientId] = useState(() => lerId(searchParams, 'client_id'))
  const [campaignIds, setCampaignIds] = useState(() => lerIds(searchParams, 'campaigns'))
  const [status, setStatus] = useState(() => lerOpcao(searchParams, 'status', ['programada', 'ativa', 'concluida', 'cancelada']))
```
e, depois de `defaultFrom`/`defaultTo`:
```jsx
  const periodoDaUrl = useMemo(() => lerPeriodo(searchParams), []) // eslint-disable-line react-hooks/exhaustive-deps
  const [from, setFrom] = useState(periodoDaUrl?.from ?? defaultFrom)
  const [to, setTo] = useState(periodoDaUrl?.to ?? defaultTo)
  useEffect(() => {
    if (temFiltro(searchParams, ['client_id', 'campaigns', 'status', 'from', 'to'])) setSearchParams({}, { replace: true })
  }, []) // eslint-disable-line react-hooks/exhaustive-deps
```
Confira os valores que o seletor de status usa (`programada|ativa|concluida|cancelada`, o `mgmtValidStatus` do handler) e que `setStatus(null)` é o "todos" da tela.

- [ ] **Step 5: `/live-map` lê `client_id` e `campaigns`**

Em `frontend/src/pages/LiveMapPage.jsx` (~:128-131):
```jsx
  const [searchParams, setSearchParams] = useSearchParams()
  const [pickedClientId, setPickedClientId] = useState(() => lerId(searchParams, 'client_id'))
  const [campaignIds, setCampaignIds] = useState(() => lerIds(searchParams, 'campaigns'))
  useEffect(() => {
    if (temFiltro(searchParams, ['client_id', 'campaigns'])) setSearchParams({}, { replace: true })
  }, []) // eslint-disable-line react-hooks/exhaustive-deps
```
O `LiveMapPage` hoje importa só `Link` do router: acrescente `useSearchParams`.

- [ ] **Step 6: `/admin/station-failures` ganha `sub`, `min_down` e o período da aba "Por dia"**

Em `AdminStationFailuresPage.jsx` (~:428-455), ao lado dos `qpView`/`qpDate`/`qpCampaign` que já existem:
```jsx
  const qpSub = searchParams.get('sub')                          // 'historical' | null
  const qpMinDown = lerInteiro(searchParams, 'min_down', MIN_DOWN_OPTIONS.map(o => o.value))
  const qpPeriodo = lerPeriodo(searchParams)                     // aba "Por dia"
```
troque `useState(60)` do `minDown` por `useState(qpMinDown ?? 60)`, `useState('daily')` do `subTab` por `useState(qpSub === 'historical' ? 'historical' : 'daily')`, inclua `qpSub || qpMinDown != null || qpPeriodo` na condição do `useEffect` que limpa a URL, e passe `periodoInicial={qpPeriodo}` ao `<FailuresDailyView … />` (~:661). Confira o formato real de `MIN_DOWN_OPTIONS` (`:13-18`): se for lista de números e não de `{value}`, passe a lista direto. Importe `{ lerInteiro, lerPeriodo }`.

Em `FailuresDailyView.jsx` (~:330-342), aceite a prop e use-a como estado inicial quando cabe no intervalo que a tela aceita (hoje − 90 dias até hoje):
```jsx
export default function FailuresDailyView({ minDownSeconds = 60, onPickDay, periodoInicial = null }) {
  const today = useMemo(() => {
    const n = new Date()
    return new Date(n.getFullYear(), n.getMonth(), n.getDate())
  }, [])
  const presets = useMemo(() => buildPresets(today), [today])
  // Período vindo da URL (link do MCP): vale se couber no intervalo do input manual.
  const inicialValido = periodoInicial
    && periodoInicial.from >= iso(new Date(today.getFullYear(), today.getMonth(), today.getDate() - 90))
    && periodoInicial.to <= iso(today)
    ? periodoInicial : null

  const [presetId, setPresetId] = useState(inicialValido ? 'custom' : 'this-month')
  const [range, setRange] = useState(() => {
    if (inicialValido) return { from: inicialValido.from, to: inicialValido.to }
    const p = buildPresets(today).find(x => x.id === 'this-month')
    return { from: p.from, to: p.to }
  })
```
(o resto do componente não muda). Confira que `iso` está definido ANTES do componente no arquivo (ele é usado em `minDate`); se for definido depois como `function`, o hoisting resolve; se for `const` depois, mova a checagem para dentro do `useState`.

O MCP também restringe `min_queda_seg` aos valores que a tela oferece (0, 60, 300, 1800) — senão o link abriria a tela com outro limiar. Isso já está na Task 9 (`lerMinQueda`).

- [ ] **Step 7: Lint, build e lockfile intacto**

```bash
cd C:/tmp/emonitor-mcp/frontend && npm ci --no-audit --no-fund 2>&1 | tail -2
npx eslint src/utils/filtrosDaUrl.js src/pages/InsightsPage.jsx src/pages/ManagementPage.jsx src/pages/LiveMapPage.jsx src/pages/AdminStationFailuresPage.jsx src/components/FailuresDailyView.jsx
npx vite build 2>&1 | tail -3
node --test src/utils/*.test.mjs 2>&1 | grep -E '^# (pass|fail)'
cd .. && git diff --quiet frontend/package-lock.json && echo LOCK_INTACTO
git show master:frontend/package-lock.json | grep -c emnapi; grep -c emnapi frontend/package-lock.json
```
Expected: eslint sem erro nos arquivos tocados (avisos pré-existentes em outras linhas: anote, não conserte); `vite build` com `built in`; `# fail 0`; `LOCK_INTACTO`; as duas contagens de `emnapi` iguais. Se o `npm ci` falhar no Windows por causa das opcionais de Linux, NÃO rode `npm install`: pare e reporte (regra 5).

- [ ] **Step 8: Commit**

```bash
cd C:/tmp/emonitor-mcp
git add frontend/src/utils/filtrosDaUrl.js frontend/src/utils/filtrosDaUrl.test.mjs frontend/src/pages/InsightsPage.jsx frontend/src/pages/ManagementPage.jsx frontend/src/pages/LiveMapPage.jsx frontend/src/pages/AdminStationFailuresPage.jsx frontend/src/components/FailuresDailyView.jsx
git status --short frontend/ # só esses 7 arquivos; node_modules é gitignored
git commit -m "feat(frontend): /insights, /management, /live-map e falhas abrem já filtrados pela URL

Os links das tools de relatório do MCP (links.tela) passam a abrir a tela no
filtro certo. Leitura só no primeiro render, validada em utils/filtrosDaUrl.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: Instruções, inventário e desempenho

**Files:**
- Modify: `workers/internal/mcp/instrucoes.go`, `workers/internal/mcp/servidor_test.go`, `workers/internal/mcp/servidor.go`

- [ ] **Step 1: Teste das instruções (falha)**

Acrescente a `servidor_test.go`:
```go
func TestInstrucoes_Relatorios(t *testing.T) {
	require.Contains(t, Instrucoes, "links.tela")
	require.Contains(t, Instrucoes, "nunca invente um link de download")
	require.Contains(t, Instrucoes, "não tocou")
	require.Contains(t, Instrucoes, "volume")
}
```
Run: `cd C:/tmp/emonitor-mcp/workers && go test ./internal/mcp/ -run TestInstrucoes_Relatorios 2>&1 | tail -3` — Expected: FAIL.

- [ ] **Step 2: Acrescente a linha "Relatórios" às instruções**

Em `instrucoes.go`, antes da linha "Áudio e arquivos nunca vêm no resultado…", acrescente:
```
Relatórios: as tools de relatório devolvem links.tela (a tela do portal já filtrada) e, quando há arquivo, links.como_baixar com o botão a apertar; CSV e PDF só se baixam no portal, com a pessoa logada — nunca invente um link de download. Falhas: o déficit se divide em "não tocou" (ausência, cobrável) e "fora do horário" (tocou na faixa errada); só falhas_campanhas e obter_falhas_campanha trazem a divisão. Assertividade: apresente o percentual sempre com o volume.
```
(é uma linha nova dentro da constante, no mesmo estilo das outras).

- [ ] **Step 3: `ToolsEsperadas` final (44) e inventário**

Confira que `ToolsEsperadas` está exatamente assim (as tasks anteriores foram inserindo):
```go
var ToolsEsperadas = []string{
	"assertividade",
	"auditoria_mcp",
	"buscar_emissoras",
	"destinatarios_pos_venda",
	"exportar_consolidado",
	"exportar_veiculacoes",
	"falhas_campanhas",
	"falhas_emissoras",
	"falhas_por_dia",
	"financeiro_campanhas",
	"grade_campanha",
	"insights_campanha",
	"listar_campanhas",
	"listar_chaves_api",
	"listar_clientes",
	"listar_entregas_webhook",
	"listar_materiais",
	"listar_materiais_campanha",
	"listar_overrides",
	"listar_pos_vendas",
	"listar_precos_campanha",
	"listar_regras_distribuicao",
	"listar_tipos_material",
	"listar_usuarios",
	"listar_veiculacoes",
	"mapa_cobertura_campanha",
	"minhas_notificacoes",
	"obter_campanha",
	"obter_cliente",
	"obter_emissora",
	"obter_falhas_campanha",
	"obter_material",
	"obter_pos_venda",
	"obter_usuario",
	"obter_veiculacao",
	"obter_webhook_cliente",
	"plano_do_dia",
	"pmm_target_cliente",
	"quem_sou_eu",
	"relatorio_data_hora",
	"resumo_diario_falhas",
	"resumo_relatorio_campanha",
	"veiculacoes_por_material",
	"visao_gerencial",
}
```
Acrescente a `servidor_test.go` uma trava de que a lista está ordenada e tem 44:
```go
func TestToolsEsperadas_OrdemAlfabeticaE44(t *testing.T) {
	require.True(t, slices.IsSorted(ToolsEsperadas))
	require.Len(t, ToolsEsperadas, 44)
}
```
(importe `slices`).

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run 'TestInstrucoes|TestInventario|TestToolsEsperadas' -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: tudo PASS — inclusive `TestInventario_IdObrigatorioMandaNaoInventar` (toda tool nova com `*_id`/`*_ids` obrigatório tem "nunca invente" na descrição ou no parâmetro; se falhar, acrescente a frase no `jsonschema` do campo).

- [ ] **Step 4: Desempenho**

```bash
cd C:/tmp/emonitor-mcp/workers && go test ./internal/mcp/ -run '^$' -bench BenchmarkNovoServidor -benchmem 2>&1 | grep Benchmark
```
Expected: uma linha `BenchmarkNovoServidor-N … ns/op`. Anote o número no relatório (a Fatia 1 mediu 1,84 ms com 27 tools). Se passar de **4 ms/op** (o SDK monta o servidor duas vezes por pedido, então ~8 ms de CPU por chamada), memoize o `*sdk.Server` por pedido: em `NovoHandlerMCP`, guarde num valor de contexto um ponteiro `*sdk.Server` preenchido na primeira chamada do `getServer` e devolvido na segunda (o SDK passa o mesmo `*http.Request` ou um derivado com o mesmo contexto — confira lendo `streamable.go` do go-sdk v1.8.0 antes); com teste que conta as montagens por pedido. Abaixo de 4 ms, não otimize: só registre o número.

- [ ] **Step 5: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/mcp/instrucoes.go workers/internal/mcp/servidor*.go
git add workers/internal/mcp/instrucoes.go workers/internal/mcp/servidor.go workers/internal/mcp/servidor_test.go
git commit -m "feat(mcp): instruções de relatório e inventário das 44 tools

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 15: Avaliação mcp-builder (10 perguntas, ≥ 9/10)

**Files:**
- Create: `workers/internal/mcp/avaliacao/semear.go` (package `avaliacao`)
- Create: `workers/internal/mcp/avaliacao/perguntas.xml`
- Create: `workers/internal/mcp/avaliacao/cli/main.go` (package `main`, só desenvolvimento — fora do `workers.Dockerfile` de propósito)
- Create: `workers/internal/mcp/avaliacao_test.go` (package `mcp`: o gabarito)
- Modify: `workers/internal/mcp/servidor.go` (`NovoServidorLocal`)

As perguntas seguem `reference/evaluation.md` do mcp-builder: só leitura, exigem várias chamadas, resposta única e verificável, estáveis (a semente é recriada no dia da rodada; janelas móveis usam datas relativas a hoje).

- [ ] **Step 1: A semente**

`workers/internal/mcp/avaliacao/semear.go`:
```go
// Package avaliacao semeia o cenário determinístico da avaliação do MCP
// (docs/superpowers/plans/2026-10-28-mcp-fatia-2-relatorios.md, Task 15).
// As perguntas citam NOMES, não ids: os ids nascem aleatórios a cada semente.
package avaliacao

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/auditoria"
	"radiocheck/internal/postsale"
)

const EmailAdmin = "avaliacao@e-monitor.local"

var sp = func() *time.Location {
	l, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return time.FixedZone("BRT", -3*3600)
	}
	return l
}()

type semeador struct {
	ctx  context.Context
	pool *pgxpool.Pool
	err  error
}

func (s *semeador) exec(sql string, args ...any) {
	if s.err == nil {
		_, s.err = s.pool.Exec(s.ctx, sql, args...)
	}
}

func dia(iso string) time.Time { d, _ := time.Parse("2006-01-02", iso); return d }

func quando(d time.Time, hhmm string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04", d.Format("2006-01-02")+" "+hhmm, sp)
	return t
}

// Semear grava o cenário num banco VAZIO (template migrado). Devolve o id do admin.
func Semear(ctx context.Context, pool *pgxpool.Pool, agora time.Time) (uuid.UUID, error) {
	s := &semeador{ctx: ctx, pool: pool}
	hojeSP := agora.In(sp)
	hoje := time.Date(hojeSP.Year(), hojeSP.Month(), hojeSP.Day(), 0, 0, 0, 0, time.UTC)

	var admin uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, role, name, is_active)
		VALUES ($1, 'x', 'admin', 'Avaliação MCP', true) RETURNING id`, EmailAdmin).Scan(&admin); err != nil {
		return uuid.Nil, err
	}

	clientes := catalog.NewClients(pool)
	aurora, err := clientes.Create(ctx, catalog.CreateClientInput{Name: "Aurora Alimentos"})
	if err != nil {
		return uuid.Nil, err
	}
	brisa, err := clientes.Create(ctx, catalog.CreateClientInput{Name: "Brisa Seguros"})
	if err != nil {
		return uuid.Nil, err
	}
	s.exec(`UPDATE clients SET target_label = 'Mulheres 25-49' WHERE id = $1`, aurora.ID)

	emissora := func(nome, banda, cidade, uf string, pmm float64) uuid.UUID {
		if s.err != nil {
			return uuid.Nil
		}
		st, err := catalog.NewStations(pool).Create(ctx, catalog.CreateStationInput{Name: nome, Band: banda, StreamURL: "http://avaliacao/" + uuid.NewString()})
		if err != nil {
			s.err = err
			return uuid.Nil
		}
		s.exec(`UPDATE stations SET pmm = $2, city = $3, state = $4, latitude = -20, longitude = -47 WHERE id = $1`, st.ID, pmm, cidade, uf)
		return st.ID
	}
	atlantica := emissora("Rádio Atlântica FM", "FM", "São Paulo", "SP", 2000)
	serra := emissora("Rádio Serra FM", "FM", "Campinas", "SP", 1000)
	vale := emissora("Rádio Vale AM", "AM", "Uberaba", "MG", 500)
	s.exec(`UPDATE stations SET anatel_class = 'A1', anatel_coverage_km = 40, anatel_reach_km = 60 WHERE id = $1`, atlantica)
	s.exec(`UPDATE stations SET anatel_class = 'B1', anatel_coverage_km = 30, anatel_reach_km = 45 WHERE id = $1`, serra)
	cidade := func(st uuid.UUID, ibge int, nome, uf string, km float64, sede bool) {
		s.exec(`INSERT INTO station_coverage_cities (station_id, ibge_code, city, state, distance_km, is_home, latitude, longitude)
			VALUES ($1, $2, $3, $4, $5, $6, -20, -47)`, st, ibge, nome, uf, km, sede)
	}
	cidade(atlantica, 3550308, "São Paulo", "SP", 0, true)
	cidade(atlantica, 3518800, "Guarulhos", "SP", 18, false)
	cidade(atlantica, 3534401, "Osasco", "SP", 15, false)
	cidade(atlantica, 3525904, "Jundiaí", "SP", 55, false)
	cidade(serra, 3509502, "Campinas", "SP", 0, true)
	cidade(serra, 3556206, "Valinhos", "SP", 12, false)
	cidade(serra, 3519071, "Hortolândia", "SP", 18, false)
	cidade(serra, 3525904, "Jundiaí", "SP", 38, false)
	cidade(vale, 3170107, "Uberaba", "MG", 0, true)

	tipo := uuid.New()
	s.exec(`INSERT INTO material_types (id, name, color) VALUES ($1, 'Spot 30s', '#3b82f6')`, tipo)
	material := func(cli uuid.UUID, titulo string) uuid.UUID {
		if s.err != nil {
			return uuid.Nil
		}
		m, err := catalog.NewMaterials(pool).Create(ctx, catalog.CreateMaterialInput{ClientID: cli, Title: titulo, TypeID: &tipo,
			DurationSeconds: 30, MasterStoragePath: "/tmp/avaliacao-" + titulo, MasterSHA256: "avaliacao-" + uuid.NewString()})
		if err != nil {
			s.err = err
			return uuid.Nil
		}
		return m.ID
	}
	verao := material(aurora.ID, "Aurora Verão 30s")
	promo := material(aurora.ID, "Aurora Promo 30s")
	auto := material(brisa.ID, "Brisa Auto 30s")

	campanha := func(cli uuid.UUID, nome string, ini, fim time.Time, estado string, ests ...uuid.UUID) uuid.UUID {
		if s.err != nil {
			return uuid.Nil
		}
		c, err := catalog.NewCampaigns(pool).Create(ctx, catalog.CreateCampaignInput{ClientID: cli, Name: nome, StartDate: ini, EndDate: fim, TargetStations: ests})
		if err != nil {
			s.err = err
			return uuid.Nil
		}
		s.exec(`UPDATE campaigns SET status = $2 WHERE id = $1`, c.ID, estado)
		return c.ID
	}
	regra := func(camp, est uuid.UUID, ini, fim time.Time) {
		if s.err != nil {
			return
		}
		_, s.err = catalog.NewDistributionRules(pool).Create(ctx, catalog.CreateDistributionRuleInput{CampaignID: camp, TypeID: tipo,
			StationIDs: []uuid.UUID{est}, MaterialIDs: []uuid.UUID{}, Name: "Dia todo até 22h", StartDate: ini, EndDate: fim,
			WeekdayMask: 127, TimeStart: "00:00", TimeEnd: "22:00", PlaysPerDay: 2})
	}
	preco := func(camp, est uuid.UUID, v float64) {
		if s.err != nil {
			return
		}
		_, s.err = catalog.NewPricing(pool).Upsert(ctx, catalog.UpsertInput{CampaignID: camp, StationID: est,
			Mode: catalog.PricingModePerInsertion, PerType: []catalog.TypePricing{{TypeID: tipo, UnitValue: v}}})
	}
	tocada := func(camp, mat, est uuid.UUID, cat string, em time.Time) {
		if s.err != nil {
			return
		}
		var id uuid.UUID
		s.err = pool.QueryRow(ctx, `INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
			match_start_offset_ms, match_end_offset_ms, confidence, hash_count, temporal_coverage, variant_used, rate_used, category)
			VALUES ($1, $2, $3, $4, 0, 30000, 0.95, 100, 0.85, 0, 0, $5) RETURNING id`, est, mat, camp, em, cat).Scan(&id)
		s.exec(`INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
			VALUES ($1, $2, $3, $4, $5)`, id, em, camp, mat, cat)
	}

	// Aurora Setembro (concluída): sem déficit. Atlântica 2/dia de 01 a 05/09 + 2 bônus em 01/09 + 1 fora da faixa em 03/09;
	// Serra 2/dia em 01 e 02/09 com o Promo.
	set := campanha(aurora.ID, "Aurora Setembro", dia("2026-09-01"), dia("2026-09-05"), "concluida", atlantica, serra)
	regra(set, atlantica, dia("2026-09-01"), dia("2026-09-05"))
	regra(set, serra, dia("2026-09-01"), dia("2026-09-02"))
	preco(set, atlantica, 200)
	preco(set, serra, 100)
	s.exec(`INSERT INTO client_station_pmm (client_id, station_id, pmm_target) VALUES ($1, $2, 800)`, aurora.ID, atlantica)
	for d := dia("2026-09-01"); !d.After(dia("2026-09-05")); d = d.AddDate(0, 0, 1) {
		tocada(set, verao, atlantica, "in_slot", quando(d, "08:00"))
		tocada(set, verao, atlantica, "in_slot", quando(d, "09:00"))
	}
	tocada(set, verao, atlantica, "bonus", quando(dia("2026-09-01"), "10:00"))
	tocada(set, verao, atlantica, "bonus", quando(dia("2026-09-01"), "11:00"))
	tocada(set, verao, atlantica, "out_slot", quando(dia("2026-09-03"), "23:00"))
	for _, d := range []time.Time{dia("2026-09-01"), dia("2026-09-02")} {
		tocada(set, promo, serra, "in_slot", quando(d, "08:00"))
		tocada(set, promo, serra, "in_slot", quando(d, "09:00"))
	}

	// Aurora Agora (ativa, hoje−20 a hoje+10): as três tocam 2/dia às 00:01 e 00:02 (sempre no passado),
	// menos no dia D = hoje−3: Serra tocou 1 vez às 23:00 (fora da faixa) e caiu 1h30; Vale não tocou.
	ini, fim := hoje.AddDate(0, 0, -20), hoje.AddDate(0, 0, 10)
	agoraC := campanha(aurora.ID, "Aurora Agora", ini, fim, "ativa", atlantica, serra, vale)
	for _, e := range []uuid.UUID{atlantica, serra, vale} {
		regra(agoraC, e, ini, fim)
	}
	diaD := hoje.AddDate(0, 0, -3)
	for d := ini; !d.After(hoje); d = d.AddDate(0, 0, 1) {
		for _, e := range []uuid.UUID{atlantica, serra, vale} {
			if d.Equal(diaD) && e != atlantica {
				continue
			}
			tocada(agoraC, verao, e, "in_slot", quando(d, "00:01"))
			tocada(agoraC, verao, e, "in_slot", quando(d, "00:02"))
		}
	}
	tocada(agoraC, verao, serra, "out_slot", quando(diaD, "23:00"))
	s.exec(`INSERT INTO stream_health_events (station_id, event_type, event_at, duration_seconds) VALUES ($1, 'down', $2, 5400)`,
		serra, quando(diaD, "10:00"))

	// Brisa Outubro (ativa, sem regra): só tocadas, nenhuma meta — não entra em falhas.
	out := campanha(brisa.ID, "Brisa Outubro", dia("2026-10-01"), dia("2026-10-31"), "ativa", vale)
	tocada(out, auto, vale, "bonus", quando(dia("2026-10-05"), "12:00"))

	// Assertividade da Aurora: mês fechado 95,0% (380 de 400), anterior 90,0%.
	mesFechado := time.Date(hoje.Year(), hoje.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	s.exec(`INSERT INTO assertiveness_daily (for_date, campaign_id, station_id, commercial_id, auto, miss) VALUES ($1, $2, $3, $4, 380, 20)`,
		mesFechado.AddDate(0, 0, 9), agoraC, atlantica, verao)
	s.exec(`INSERT INTO assertiveness_daily (for_date, campaign_id, station_id, commercial_id, auto, miss) VALUES ($1, $2, $3, $4, 90, 10)`,
		mesFechado.AddDate(0, -1, 9), agoraC, atlantica, verao)

	// Pós-venda enviado da Aurora Setembro: 3 destinatários, 2 abriram.
	if s.err == nil {
		s.err = semearPosVenda(ctx, pool, aurora.ID, set)
	}

	// Auditoria: dois tokens do admin; o mais recente é "Notebook do escritório".
	if s.err == nil {
		s.err = auditoria.Registrar(ctx, pool, admin, "mcp.token.criado", "mcp_token", nil, map[string]any{"nome": "Celular", "validade_dias": 90})
	}
	s.exec(`UPDATE audit_log SET created_at = now() - interval '2 hours' WHERE metadata->>'nome' = 'Celular'`)
	if s.err == nil {
		s.err = auditoria.Registrar(ctx, pool, admin, "mcp.token.criado", "mcp_token", nil, map[string]any{"nome": "Notebook do escritório", "validade_dias": 90})
	}
	if s.err != nil {
		return uuid.Nil, fmt.Errorf("avaliacao: semear: %w", s.err)
	}
	return admin, nil
}

func semearPosVenda(ctx context.Context, pool *pgxpool.Pool, cliente, campanha uuid.UUID) error {
	repo := postsale.NewRepo(pool)
	rep, err := repo.CreateDraft(ctx, postsale.CreateDraftInput{ClientID: cliente, Title: "Fechamento Aurora Setembro"})
	if err != nil {
		return err
	}
	if err := repo.ReplaceBlocks(ctx, rep.ID, []postsale.BlockRow{{CampaignID: campanha, From: dia("2026-09-01"), To: dia("2026-09-05")}}); err != nil {
		return err
	}
	dest, err := repo.CreateRecipients(ctx, rep.ID, []postsale.RecipientInput{
		{Email: "ana@aurora.com", Name: "Ana", Token: uuid.NewString()},
		{Email: "bruno@aurora.com", Name: "Bruno", Token: uuid.NewString()},
		{Email: "carla@aurora.com", Name: "Carla", Token: uuid.NewString()},
	})
	if err != nil {
		return err
	}
	for _, d := range dest[:2] {
		if _, err := pool.Exec(ctx, `UPDATE post_sale_report_recipients SET opened_at = now(), open_count = 1, email_status = 'sent' WHERE id = $1`, d.ID); err != nil {
			return err
		}
	}
	b, err := json.Marshal(postsale.Payload{Version: postsale.PayloadVersion, Title: "Fechamento Aurora Setembro", Campaigns: []postsale.CampaignBlock{{
		CampaignID: campanha, Name: "Aurora Setembro", PeriodFrom: "2026-09-01", PeriodTo: "2026-09-05",
		KPIs: postsale.BlockKPIs{ValorEntregue: 3400, Impactos: 28000, CPM: 128.57, StationsCount: 2}, CheckingRows: []postsale.StationRow{}, ConformingCount: 2}}})
	if err != nil {
		return err
	}
	return repo.MarkSent(ctx, rep.ID, b)
}
```
Antes de rodar: confira a coluna `stations.latitude/longitude/city/state` (Task 2 já conferiu para a fixture `Cobertura`), o nome `UpsertInput` do `Pricing` e os campos de `CreateDistributionRuleInput` (são os mesmos que o `mcptest.Regra` usa).

- [ ] **Step 2: As 10 perguntas com o gabarito**

`workers/internal/mcp/avaliacao/perguntas.xml`:
```xml
<evaluation>
  <qa_pair>
    <question>Quantos impactos no público-alvo do cliente a campanha "Aurora Setembro" da Aurora Alimentos entregou em setembro de 2026? Responda só o número inteiro.</question>
    <answer>9600</answer>
  </qa_pair>
  <qa_pair>
    <question>Em quantos estados (UF) ficam as emissoras monitoradas pelas campanhas ATIVAS da Aurora Alimentos neste ano? Responda só o número.</question>
    <answer>2</answer>
  </qa_pair>
  <qa_pair>
    <question>Qual é o valor unitário de tabela, em reais, de uma inserção do spot de 30s na Rádio Atlântica FM dentro da campanha "Aurora Setembro"? Responda só o número, sem "R$".</question>
    <answer>200</answer>
  </qa_pair>
  <qa_pair>
    <question>No relatório da campanha "Aurora Setembro", quantas veiculações teve o material "Aurora Promo 30s"? Responda só o número.</question>
    <answer>4</answer>
  </qa_pair>
  <qa_pair>
    <question>Quantos municípios distintos ficam no alcance das emissoras da campanha "Aurora Agora"? Responda só o número.</question>
    <answer>8</answer>
  </qa_pair>
  <qa_pair>
    <question>Na campanha "Aurora Agora", quantas inserções programadas até ontem faltaram porque a emissora simplesmente não tocou (sem contar as que tocaram fora do horário)? Responda só o número.</question>
    <answer>3</answer>
  </qa_pair>
  <qa_pair>
    <question>Nos últimos 30 dias, no dia com mais emissoras em falha, quantas emissoras falharam? Responda só o número.</question>
    <answer>2</answer>
  </qa_pair>
  <qa_pair>
    <question>Qual foi o percentual de assertividade da detecção da Aurora Alimentos no último mês fechado? Responda só o número com uma casa decimal, usando ponto (ex.: 87.5).</question>
    <answer>95.0</answer>
  </qa_pair>
  <qa_pair>
    <question>Quantos destinatários abriram o pós-venda "Fechamento Aurora Setembro"? Responda só o número.</question>
    <answer>2</answer>
  </qa_pair>
  <qa_pair>
    <question>Qual é o nome do token MCP criado mais recentemente, segundo a auditoria do MCP? Responda só o nome.</question>
    <answer>Notebook do escritório</answer>
  </qa_pair>
</evaluation>
```
Contas do gabarito (para quem revisar): (1) 800 × (10 in_slot + 2 bonus) na Atlântica; a Serra não tem PMM no target. (2) Atlântica e Serra em SP, Vale em MG. (3) preço por inserção da Atlântica. (4) 2 dias × 2 tocadas do Promo na Serra. (5) São Paulo, Guarulhos, Osasco, Jundiaí, Campinas, Valinhos, Hortolândia, Uberaba (Jundiaí alcançada por duas). (6) dia D: Serra 1 "não tocou" (a outra foi fora do horário), Vale 2. (7) só o dia D tem falha, com Serra e Vale. (8) 380 ÷ (380 + 20). (9) Ana e Bruno. (10) o segundo `mcp.token.criado`.

- [ ] **Step 3: O teste de gabarito (falha até as tools estarem certas)**

`workers/internal/mcp/avaliacao_test.go`:
```go
package mcp

import (
	"encoding/xml"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/avaliacao"
	"radiocheck/internal/mcp/mcptest"
)

type perguntasXML struct {
	Pares []struct {
		Pergunta string `xml:"question"`
		Resposta string `xml:"answer"`
	} `xml:"qa_pair"`
}

// TestAvaliacao_Gabarito prova que cada resposta do perguntas.xml sai das
// tools sobre a semente — o gabarito não pode estar errado nem instável.
func TestAvaliacao_Gabarito(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)
	_, err = avaliacao.Semear(ctx, pool, time.Now())
	require.NoError(t, err)
	cfg := cfgTeste(pool)
	raw, err := os.ReadFile("avaliacao/perguntas.xml")
	require.NoError(t, err)
	var px perguntasXML
	require.NoError(t, xml.Unmarshal(raw, &px))
	require.Len(t, px.Pares, 10)

	idPorNome := func(tool string, args map[string]any, nome string) string {
		for _, it := range itens(t, chamarTool(t, cfg, tool, args)) {
			for _, chave := range []string{"nome", "titulo"} {
				if it[chave] == nome {
					return it["id"].(string)
				}
			}
		}
		t.Fatalf("%s: %q não encontrado", tool, nome)
		return ""
	}
	aurora := idPorNome("listar_clientes", map[string]any{"busca": "Aurora"}, "Aurora Alimentos")
	set := idPorNome("listar_campanhas", map[string]any{"cliente_id": aurora, "limit": 100}, "Aurora Setembro")
	agora := idPorNome("listar_campanhas", map[string]any{"cliente_id": aurora, "limit": 100}, "Aurora Agora")
	str := func(v any) string { return fmt.Sprint(v) }

	got := make([]string, 10)
	got[0] = str(chamarTool(t, cfg, "insights_campanha", map[string]any{"cliente_id": aurora, "campanha_ids": []string{set},
		"de": "2026-09-01", "ate": "2026-09-30"})["impactos_target"])
	got[1] = str(chamarTool(t, cfg, "visao_gerencial", map[string]any{"cliente_id": aurora, "estado": "ativa"})["kpis"].(map[string]any)["estados"])
	for _, l := range itens(t, chamarTool(t, cfg, "relatorio_data_hora", map[string]any{"cliente_id": aurora, "campanha_ids": []string{set},
		"de": "2026-09-01", "ate": "2026-09-30"})) {
		if l["emissora"].(map[string]any)["nome"] == "Rádio Atlântica FM" {
			got[2] = str(l["valor_unitario_tabela"])
		}
	}
	for _, m := range chamarTool(t, cfg, "resumo_relatorio_campanha", map[string]any{"campanha_id": set})["por_material"].([]any) {
		if m.(map[string]any)["material"].(map[string]any)["nome"] == "Aurora Promo 30s" {
			got[3] = str(m.(map[string]any)["veiculacoes"])
		}
	}
	got[4] = str(chamarTool(t, cfg, "mapa_cobertura_campanha", map[string]any{"campanha_ids": []string{agora}})["cidades_distintas"])
	got[5] = str(chamarTool(t, cfg, "obter_falhas_campanha", map[string]any{"campanha_id": agora})["resumo"].(map[string]any)["deficit_nao_tocou"])
	got[6] = str(chamarTool(t, cfg, "falhas_por_dia", map[string]any{})["resumo"].(map[string]any)["pico_emissoras"])
	got[7] = fmt.Sprintf("%.1f", chamarTool(t, cfg, "assertividade", map[string]any{"cliente_id": aurora})["mes_fechado"].(map[string]any)["percentual"])
	pv := idPorNome("listar_pos_vendas", map[string]any{}, "Fechamento Aurora Setembro")
	got[8] = str(chamarTool(t, cfg, "obter_pos_venda", map[string]any{"pos_venda_id": pv})["destinatarios"].(map[string]any)["abriram"])
	for _, e := range itens(t, chamarTool(t, cfg, "auditoria_mcp", map[string]any{"acao": "mcp.token.criado", "limit": 1})) {
		got[9] = str(e["detalhes"].(map[string]any)["nome"])
	}
	for i, p := range px.Pares {
		require.Equal(t, p.Resposta, got[i], "pergunta %d: %s", i+1, p.Pergunta)
	}
}
```
`listar_clientes` aceita `busca`? Confira a entrada dela (Fatia 1) e ajuste o argumento; idem `listar_campanhas` (`cliente_id`, `limit`). A Aurora Setembro é `concluida`: `listar_campanhas` sem filtro de estado a traz (só as canceladas ficam de fora).

Run: `cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable" && go test -p 1 ./internal/mcp/ -run TestAvaliacao_Gabarito -v 2>&1 | grep -E '^(--- |ok|FAIL)|Error|pergunta'`
Expected: PASS. Se uma resposta divergir, descubra quem está errado — a conta à mão do Step 2, a semente ou a tool — e conserte a causa. **Nunca** troque o `<answer>` só para o teste passar sem entender a diferença; se a tool estiver certa e a conta errada, corrija a conta comentada no Step 2 junto.

- [ ] **Step 4: `NovoServidorLocal` e o CLI**

Em `servidor.go`:
```go
// NovoServidorLocal expõe o servidor (sem HTTP nem autenticação) ao CLI de
// avaliação, que roda em desenvolvimento contra um banco semeado. Nunca é
// montado numa rota.
func NovoServidorLocal(cfg ConfigServidor, p *Principal) *sdk.Server { return novoServidor(cfg, p) }
```

`workers/internal/mcp/avaliacao/cli/main.go`:
```go
// Command cli roda a avaliação do MCP em desenvolvimento (não vai para a
// imagem de prod — não é um cmd/*):
//
//	DATABASE_URL=… go run ./internal/mcp/avaliacao/cli semear
//	DATABASE_URL=… go run ./internal/mcp/avaliacao/cli tools
//	DATABASE_URL=… go run ./internal/mcp/avaliacao/cli chamar <tool> '<json>'
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"radiocheck/internal/mcp"
	"radiocheck/internal/mcp/avaliacao"
)

func main() {
	if err := rodar(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func rodar(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("uso: cli semear | tools | chamar <tool> '<json>'")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	if args[0] == "semear" {
		_, err := avaliacao.Semear(ctx, pool, time.Now())
		return err
	}
	var p mcp.Principal
	if err := pool.QueryRow(ctx, `SELECT id, email, name FROM users WHERE email = $1`, avaliacao.EmailAdmin).Scan(&p.UserID, &p.Email, &p.Nome); err != nil {
		return fmt.Errorf("rode 'semear' antes: %w", err)
	}
	srv := mcp.NovoServidorLocal(mcp.ConfigServidor{Versao: "avaliacao", Portal: "https://e-monitor.online", Leitura: mcp.NovaLeitura(pool)}, &p)
	ct, st := sdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		return err
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "avaliacao", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		return err
	}
	defer cs.Close()
	switch args[0] {
	case "tools":
		res, err := cs.ListTools(ctx, nil)
		if err != nil {
			return err
		}
		for _, t := range res.Tools {
			esquema, _ := json.Marshal(t.InputSchema)
			fmt.Printf("## %s\n%s\nentrada: %s\n\n", t.Name, t.Description, esquema)
		}
		return nil
	case "chamar":
		if len(args) < 2 {
			return fmt.Errorf("uso: cli chamar <tool> '<json>'")
		}
		var argumentos map[string]any
		if len(args) > 2 {
			if err := json.Unmarshal([]byte(args[2]), &argumentos); err != nil {
				return fmt.Errorf("argumentos não são JSON: %w", err)
			}
		}
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: args[1], Arguments: argumentos})
		if err != nil {
			return err
		}
		if res.IsError {
			fmt.Print("ERRO: ")
		}
		for _, c := range res.Content {
			if tc, ok := c.(*sdk.TextContent); ok {
				fmt.Println(tc.Text)
			}
		}
		return nil
	}
	return fmt.Errorf("comando desconhecido %q", args[0])
}
```
Confira o nome dos campos de `sdk.Tool` (`InputSchema`) no go-sdk v1.8.0.

- [ ] **Step 5: Rode a avaliação**

```bash
docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -c "DROP DATABASE IF EXISTS radiocheck_mcp_avaliacao;" -c "CREATE DATABASE radiocheck_mcp_avaliacao TEMPLATE radiocheck_mcp_test_d2;"
cd C:/tmp/emonitor-mcp/workers && export DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_avaliacao?sslmode=disable"
go run ./internal/mcp/avaliacao/cli semear && go run ./internal/mcp/avaliacao/cli tools | head -5
```
Expected: sem erro; a lista começa por `## assertividade`.

Despache UM subagente (modelo padrão, effort `high`) com este prompt, sem o gabarito e sem acesso declarado ao código:
> Você vai responder 10 perguntas sobre o E-monitor usando SÓ o CLI abaixo, que chama as tools do servidor MCP do E-monitor. Não leia código, não consulte o banco, não abra arquivos do repositório. Comandos (rode no Bash, em `C:/tmp/emonitor-mcp/workers`, sempre com `export DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_avaliacao?sslmode=disable"`): `go run ./internal/mcp/avaliacao/cli tools` (lista as tools, descrições e entradas) e `go run ./internal/mcp/avaliacao/cli chamar <tool> '<json>'`. Leia as descrições antes. Não rode `semear`. Para cada pergunta devolva `N. <resposta>` numa linha, exatamente no formato pedido, e depois um parágrafo curto por pergunta com as tools que chamou e qualquer dificuldade (descrição confusa, erro, resultado ambíguo). Perguntas: <as 10 `<question>` do XML, numeradas>.

Compare cada resposta com o `<answer>` (igualdade após `trim`; para números, `95` = `95.0`). Nota = acertos.
Expected: **≥ 9/10**. Abaixo disso, leia o relato do subagente: o defeito é quase sempre de descrição de tool ou de nome de campo; conserte a tool (com teste), reexecute o gabarito (Step 3) e rode a avaliação de novo com um subagente NOVO. Registre a nota e os tropeços relatados no relatório do lote e em `docs/operations/mcp.md` (Task 16).

- [ ] **Step 6: Commit**

```bash
cd C:/tmp/emonitor-mcp && gofmt -l workers/internal/mcp/avaliacao/semear.go workers/internal/mcp/avaliacao/cli/main.go workers/internal/mcp/avaliacao_test.go workers/internal/mcp/servidor.go
CGO_ENABLED=0 GOOS=linux go -C workers build ./...
git add workers/internal/mcp/avaliacao/ workers/internal/mcp/avaliacao_test.go workers/internal/mcp/servidor.go
git commit -m "test(mcp): avaliação mcp-builder da Fatia 2 (10 perguntas, gabarito provado pelas tools)

Semente determinística, CLI de desenvolvimento (fora da imagem) e teste que
prova cada resposta. Nota da rodada: <N>/10.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
(troque `<N>` pela nota real antes de commitar).

---

### Task 16: Documentação

**Files:**
- Modify: `docs/features/mcp.md`, `docs/operations/mcp.md`, `docs/README.md` (se o índice listar docs de feature)
- Modify: `docs/features/insights-dashboard.md`, `docs/features/management-overview.md`, `docs/features/live-map.md`, `docs/features/admin-station-failures.md` (uma seção curta "Abrir já filtrado pela URL" em cada)

- [ ] **Step 1: `docs/features/mcp.md`**

Atualize o header (`ultima-verificacao: <hoje>`, acrescente em `codigo-relacionado` os arquivos `tools_insights.go`, `tools_gerencial.go`, `tools_relatorios.go`, `tools_mapa.go`, `tools_falhas.go`, `tools_posvenda.go`, `tools_auditoria.go`, `toolkit_relatorios.go`, `avaliacao/`, `frontend/src/utils/filtrosDaUrl.js`). Renomeie a seção "Tools (27)" para "Tools (44)" e acrescente as 17 linhas (tool | o que devolve | fonte | link), no mesmo formato das existentes:

| Tool | Devolve | Fonte | Link |
|---|---|---|---|
| `insights_campanha` | KPIs, por categoria, investido/bonificação, CPM, demografia, série | `catalog.Insights.Compute` (`/insights`) | `/insights?client_id&campaigns&from&to[&stations][&materials]` |
| `visao_gerencial` | KPIs da operação (sem lista de emissoras nem feed) | `catalog.ManagementOverview.Get` + `supervisor.ContarAtivas` | `/management?…` |
| `assertividade` | mês fechado e anterior, variação, manuais desculpadas | `assertiveness.Repo.Get` | `/management?…` |
| `relatorio_data_hora` | veiculações com PMM, PMM no target e valor de tabela | `Detections.ListPaged` + `catalog.PrecoDeTabela` | `/reports/airtime?client_id&competence&campaigns&from&to` |
| `resumo_relatorio_campanha` | totais, por material, por emissora (paginada) | `Detections.AggregateByMaterial/ByStation` + `catalog.TotaisDoResumo` | `/reports/airtime?…` |
| `exportar_consolidado` | nº de linhas do CSV + onde baixar | `Detections.AggregateByMaterialStation` | idem + `como_baixar` |
| `exportar_veiculacoes` | nº de linhas do CSV detalhado + onde baixar | (ver Task 7) | idem + `como_baixar` |
| `mapa_cobertura_campanha` | emissoras com raio Anatel, cidades (20 por emissora), municípios distintos | `LiveMap.Get` + `LiveMap.Coverage` | `/live-map?client_id&campaigns` |
| `falhas_emissoras` | emissoras com falha no dia (déficit total) | `StationFailures.ListForDate` | `/admin/station-failures?view=by_station&date&min_down` |
| `falhas_campanhas` | modo dia/histórico com split não tocou × fora do horário | `CampaignFailures.ListForDate/ListHistorical` | `…?view=by_campaign&date` / `&sub=historical` |
| `obter_falhas_campanha` | drill-in de uma campanha | `CampaignFailures.Get` | `…?view=by_campaign&campaign=<id>` |
| `falhas_por_dia` | série diária + pico e média | `FailuresDaily.ListDaily` | `…?view=by_day&from&to&min_down` |
| `resumo_diario_falhas` | o digest de ontem (sem "visto") | `CampaignFailures.ListForDate` | `…?view=by_campaign&date` |
| `listar_pos_vendas` | pós-vendas + contagens | `postsale.Repo.List` | `/admin/pos-venda/:id` |
| `obter_pos_venda` | números congelados, checking (20), resumo de destinatários | `Repo.Get` + `Repo.Payload` | `/admin/pos-venda/:id` |
| `destinatarios_pos_venda` | quem recebeu / quem receberia (sem token) | `Repo.Recipients`, `ActiveClientUsers`, `InternalRecipients` | — |
| `auditoria_mcp` | eventos do MCP por cursor (`total: null`) | `auditoria.Listar` | — |

Acrescente uma seção "Relatórios: deep-link e downloads" (F2-1, F2-2: por que não há link direto de CSV; quais telas leem quais parâmetros; a leitura é só no primeiro render e a URL é limpa depois) e uma seção "O que nunca sai" (token e link público do pós-venda, chave S3, URL completa de anexo/webhook).

- [ ] **Step 2: `docs/operations/mcp.md`**

Acrescente a seção "Avaliação (mcp-builder)": como recriar o banco `radiocheck_mcp_avaliacao`, `semear`, `tools`, `chamar`, como despachar o avaliador e a nota da última rodada (data + N/10 + tropeços). E, no roteiro de deploy, o item "conferir 2 relatórios com dado real": `insights_campanha` × `/insights` e `obter_falhas_campanha` × o drawer de `/admin/station-failures`, em 2 campanhas ativas.

- [ ] **Step 3: Docs das telas**

Em cada um dos quatro docs de tela, acrescente a seção "Abrir já filtrado pela URL" com os parâmetros que a tela passou a ler (Task 13) e a limitação conhecida de `/insights` (campanha fora da página 1/200 do seletor não aparece como chip, mas filtra). Atualize `ultima-verificacao`.

- [ ] **Step 4: Commit**

```bash
cd C:/tmp/emonitor-mcp && git add docs/features/mcp.md docs/operations/mcp.md docs/README.md docs/features/insights-dashboard.md docs/features/management-overview.md docs/features/live-map.md docs/features/admin-station-failures.md
git commit -m "docs(mcp): Fatia 2 — 17 tools de relatório, deep-links e avaliação

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 17: Memória e handoff da Fatia 3

**Files:**
- Create: `docs/superpowers/HANDOFF-AAAA-MM-DD-mcp-fatia-3.md`
- Modify: `C:\Users\marke\.claude\projects\c--Users-marke-Desktop-Programas-E-Series-E-monitor\memory\mcp-emonitor-plan.md` e a linha dela no `MEMORY.md`

- [ ] **Step 1: Handoff**

No mesmo formato do `HANDOFF-2026-10-28-mcp-fatia-2.md`: prompt para colar (Fatia 3 = escrita `livre` e `confirma`, spec §5 e §6 "Fatia 3": motor de níveis, `mcp_confirmacoes_usadas`, `EscalarPara` com dia anterior a hoje → `critico`, auditoria com `meta.mcp = {token_id}` — o `Principal.TokenID` já existe), estado da branch (contagem de commits com `git rev-list --count origin/master..HEAD`), as 44 tools, o desempenho medido, a nota da avaliação, as decisões F2-1..F2-11 que valem para a 3, as armadilhas novas desta fatia (as que os revisores e a execução acharem) e as pendências (inclusive as herdadas que continuam abertas).

- [ ] **Step 2: Memória**

Atualize `mcp-emonitor-plan.md`: Fatias 0–2 implementadas, 44 tools, nota da avaliação, handoff mais recente, armadilhas novas que valem para fatias futuras (ex.: rotas de CSV só com Bearer → nunca link direto; `postsale.Recipient.Token` sai no JSON do handler; partições de `stream_health_events` no template). Atualize a linha do `MEMORY.md` (data e "próximo = Fatia 3").

- [ ] **Step 3: Commit do handoff**

```bash
cd C:/tmp/emonitor-mcp && git add docs/superpowers/HANDOFF-*-mcp-fatia-3.md
git commit -m "docs(mcp): handoff da Fatia 3

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 18: Verificação final da fatia

**Files:** nenhum

- [ ] **Step 1: Build cruzado, vet e testes de tudo que a fatia tocou**

```bash
cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f2?sslmode=disable"
CGO_ENABLED=0 GOOS=linux go build ./... && go vet ./...
go test -p 1 ./internal/mcp/... 2>&1 | tail -8
go test -p 1 ./internal/supervisor/ ./internal/postsale/ ./internal/assertiveness/ 2>&1 | tail -4
go test -p 1 ./internal/api/handlers/ 2>&1 | tail -4
```
Recrie o banco do template e rode o catalog por último:
```bash
docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -c "DROP DATABASE radiocheck_mcp_test_f2;" -c "CREATE DATABASE radiocheck_mcp_test_f2 TEMPLATE radiocheck_mcp_test_d2;"
go test -p 1 ./internal/catalog/ 2>&1 | grep -E '^(--- FAIL|ok|FAIL)'
```
Expected: build e vet limpos; `ok` em `internal/mcp/...`, `supervisor`, `postsale`, `assertiveness`; em `handlers` e `catalog`, só as falhas pré-existentes listadas no handoff (`TestDetections_ListPaged_IgnoredExcluded`, `TestBuildDailySummary_WithDowntime`, `TestDetections_CreateManualBatch_WithProof`/`NoProof`, `TestMaterialTypes_List`) — confirme que cada falha está num teste que esta fatia não tocou, e que ela também falha no commit anterior à fatia (`git stash` NÃO: use `git worktree add C:/tmp/emonitor-f2-base <hash-antes-da-Task-1>` e rode lá; remova o worktree depois com `git worktree remove`). Se o guard derrubar testes por excesso de clientes/emissoras, é ambiental: refaça o banco.

- [ ] **Step 2: Frontend**

```bash
cd C:/tmp/emonitor-mcp/frontend && node --test src/utils/*.test.mjs 2>&1 | grep -E '^# (pass|fail)' && npx vite build 2>&1 | tail -2
cd .. && git diff --quiet master -- frontend/package-lock.json && echo LOCK_IGUAL_AO_MASTER
```
Expected: `# fail 0`, build ok, `LOCK_IGUAL_AO_MASTER`.

- [ ] **Step 3: Revisão da branch inteira**

Despache um revisor (superpowers:code-reviewer) sobre `git diff <hash-antes-da-Task-1>..HEAD`, com o plano e a spec como referência, pedindo: aderência às decisões F2-1..F2-11; regra de ouro (nenhuma conta de negócio no MCP); paridade de cada tool com a tela; nada sensível no resultado (token, link público, URL completa, chave S3); teto de saída em toda lista interna; desempate de ordenação em toda lista paginada nova; frontend sem efeito colateral. Corrija o que for confirmado, com teste.

- [ ] **Step 4: Estado final**

```bash
cd C:/tmp/emonitor-mcp && git status --short && git log --oneline origin/master..HEAD | wc -l && git branch --show-current
```
Expected: árvore limpa, a contagem de commits da branch e `feat/mcp-server`. **Nada de push nem merge** (regra do dono). Relate ao dono: o que foi feito, a nota da avaliação, o desempenho, o que NÃO foi verificado (conexão real dos 3 clientes, paridade com dado real de prod — só no deploy; leitura dos filtros pela URL conferida só por build/lint/teste do parser, não num navegador logado) e as pendências.

---

## Riscos conhecidos

- **Fuso das falhas.** Os handlers de falha leem a data em `time.Local` e o SQL recorta por `$1::date` na sessão do Postgres; a tool repete isso de propósito (paridade), herdando o comportamento — inclusive o "ontem" UTC do container entre 21h e 0h de SP na tela. Corrigir é mudança de tela, fora desta fatia.
- **Contagem do CSV detalhado.** Se o `IterateForExport` filtrar diferente do `ListPaged`, a Task 7 manda criar `CountForExport` com o mesmo `WHERE`. O teste contra o CSV real decide.
- **`/insights` com campanha fora da página 1/200 do seletor**: filtra, mas o chip não aparece. Documentado; resolver exige mudar o `FiltersBar`.
- **A avaliação roda sobre semente, não sobre dado real**: prova que o modelo consegue usar as tools, não que os números de prod batem — isso fica no roteiro de deploy.

## Self-review (feito ao escrever)

- **Cobertura da spec §6 "Fatia 2":** as 17 tools têm task (4: insights; 5: gerencial, assertividade; 6: data e hora; 7: resumo, 2 exportações; 8: mapa; 9–10: as 5 de falhas; 11: as 3 de pós-venda; 12: auditoria). Telas lendo a URL: Task 13. Avaliação ≥ 9/10: Task 15. Docs `/docs`: Task 16.
- **Regra de ouro:** as duas contas que moravam em handler foram extraídas antes (Tasks 2 e 3), travadas por teste do handler; o lookup de preço do JS foi para o `catalog`.
- **Nomes consistentes entre tasks:** `lerIDs`/`lerDias` (Task 4) reusados nas 5–11; `linkDataHora` (Task 6) reusado na 7; `cortar`/`tetoPorItem`/`links`/`linkTela`/`novaListaCursor` (Task 1) usados nas seguintes; `ToolsEsperadas` final conferido na Task 14 (44).
