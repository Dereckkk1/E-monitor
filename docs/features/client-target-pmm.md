---
status: implementado
ultima-verificacao: 2026-07-21
codigo-relacionado:
  - migrations/0054_client_station_pmm.up.sql
  - workers/internal/catalog/client_station_pmm.go
  - workers/internal/api/handlers/client_target_pmm.go
  - workers/internal/catalog/insights.go
  - workers/internal/catalog/campaigns.go
  - workers/internal/catalog/detections.go
  - workers/internal/api/handlers/reports.go
  - frontend/src/pages/ClientTargetPmmPage.jsx
  - frontend/src/utils/targetPmmPaste.js
---

# PMM no target por cliente

`stations.pmm` é a audiência **total** da emissora: um número global, igual para todo mundo. O **PMM no target** é a audiência **dentro do público-alvo de um cliente específico** naquela emissora — um inteiro por par (cliente, emissora). Serve para responder "quantas pessoas do meu público a campanha impactou", em vez de "quantas pessoas ouviram".

A partir dele saem duas métricas derivadas que aparecem em toda superfície de veiculação: **impactos no target** (`veiculações × pmm_target`) e **CPM no target** (`investido ÷ impactos_no_target × 1000`).

## Modelo

`client_station_pmm` (migration 0054):

```sql
client_id  UUID    NOT NULL REFERENCES clients(id)  ON DELETE CASCADE
station_id UUID    NOT NULL REFERENCES stations(id) ON DELETE CASCADE
pmm_target INTEGER NOT NULL CHECK (pmm_target >= 0)
PRIMARY KEY (client_id, station_id)
```

Índice extra em `station_id` (lookup reverso + suporte aos `LEFT JOIN` das agregações). Trigger `trg_cspmm_touch` reusa a `touch_updated_at()` que já existia desde a 0022.

**Granularidade é (cliente × emissora), não (campanha × emissora)** — o público-alvo é uma propriedade do cliente e vale para todas as campanhas dele. Cadastrar por campanha multiplicaria o trabalho de cadastro por N e criaria divergência entre campanhas do mesmo anunciante.

### Regra de resolução canônica

```
target da linha = client_station_pmm[campanha.client_id, station_id]
```

Vale em **toda** superfície, porque toda tela de veiculação parte de uma campanha (e, portanto, de um cliente). Em **multi-atribuição** ([multi-attribution.md](multi-attribution.md)), cada projeção em `detection_attributions` traz seu próprio `campaign_id` → cada atribuição resolve o target pelo cliente **dela**, que é o comportamento correto: a mesma tocada física pode valer 12.000 impactos no target do cliente A e 300 no do cliente B.

Não existe fragmento SQL compartilhado (ao contrário do `catalog.ApprovedDetectionsFilter`): cada consumidor chega na campanha por um alias diferente (`cmp` em `detections.go`, `cc` em `campaigns.go`, a CTE `per_station` em `insights.go`), então um literal comum não encaixaria em nenhum sem renomear queries estáveis. O join é escrito à mão, sempre nesta forma — o comentário-cabeçalho de `client_station_pmm.go` é a documentação canônica disso:

```sql
LEFT JOIN client_station_pmm cst
       ON cst.client_id = <alias da campanha>.client_id
      AND cst.station_id = <alias da emissora>.id
```

### Semântica de ausência — o ponto mais importante

| Estado | Significado | Efeito |
|---|---|---|
| **sem linha** | não cadastrado | emissora fica **fora** do total de impactos no target **e** fora do contador "X de Y emissoras com target" |
| **`pmm_target = 0`** | cadastrado com valor zero | **conta** no contador e soma **zero** impactos |

Por isso a coluna é `NOT NULL` sem `DEFAULT`: "não cadastrado" é modelado pela **ausência da linha**, nunca por `NULL` ou `0`. Na UI, apagar o campo (ou o botão de limpar) **remove a linha**; digitar `0` grava zero.

Consequência prática: um cliente com 40 emissoras-alvo e 3 cadastradas mostra impactos no target só das 3 — o número é honesto sobre a cobertura do cadastro, e o contador "3 de 40" fica visível ao lado dele em todas as telas que mostram o bloco.

### Sem versionamento histórico

Não há `valid_from`/`valid_to`. Corrigir um valor **muda relatórios de meses anteriores** — exatamente como `stations.pmm` faz hoje. Decisão explícita: versionar exigiria carregar a data de cada tocada em todo join de leitura (5 consumidores) e resolveria um problema que o PMM global já tem sem causar dor. Se um dia versionarmos, os dois têm que ser versionados juntos.

## API

### `GET /v1/internal/clients/{clientID}/target-pmm`

Registrado no **subgrupo A** do router (viewer-friendly), porque a grid de `/detections` e o `/insights` do cliente precisam do mapa para renderizar os impactos no target. Scope-check dentro do handler: viewer pedindo **outro** cliente recebe **404**, não 403 (anti-oracle — não confirma a existência do id; mesmo padrão de `/campaigns/{id}`).

Devolve `{ "data": [...] }` com as **emissoras-alvo das campanhas do cliente** — união dos `campaigns.target_stations`, **não** o catálogo inteiro de emissoras:

```json
{ "station_id": "...", "short_id": 412, "name": "Jovem Pan", "band": "FM",
  "frequency_mhz": 100.9, "city": "Uberaba", "state": "MG",
  "pmm": 18500.0, "pmm_target": 12500 }
```

`pmm_target` é `null` quando não cadastrado (a tela precisa da linha mesmo assim, para oferecer o input vazio). Campanhas **canceladas** entram normalmente na união: esta é uma tela de cadastro, não operacional — a regra de [cancelled-campaign-handling.md](cancelled-campaign-handling.md) não se aplica.

### `PUT /v1/internal/clients/{clientID}/target-pmm`

Admin/operator-only (**subgrupo B**). Registrado com `r.Put(...)` direto, **não** `r.Route()` — pelo mesmo motivo dos writes de `/clients`: um `r.Route()` aqui mascararia o `GET` do subgrupo A.

```json
{ "entries": [ { "station_id": "...", "pmm_target": 12500 },
               { "station_id": "...", "pmm_target": null } ] }
```

- Bulk upsert **transacional** (`BulkUpsert`): valor → `INSERT ... ON CONFLICT DO UPDATE`; `null` → `DELETE` (volta a "não cadastrado"). Idempotente.
- Resposta: `{ "updated": N, "deleted": M }`.
- `pmm_target` negativo → 400. Mais de **5000 entries** → 400: a UI manda só as linhas alteradas, então um lote gigante indica bug de cliente, não uso legítimo.

## Cadastro — `/clients/:id/target-pmm`

Rota admin (`RequireRole roles={['admin']}`), acessível pelo ícone de alvo na linha do cliente em `/clients`.

- Tabela emissora × PMM da emissora × input de PMM no target, com filtro por nome/cidade/UF e contador "N de M emissoras com PMM no target" que **inclui o rascunho não salvo**.
- O input aceita **só dígitos** (`onlyDigits`). Isso não é cosmético: `Number('abc')` = `NaN` viraria `null` no JSON e **apagaria a linha silenciosamente**.
- O `PUT` manda **só o que mudou** (`dirtyEntries` compara rascunho × servidor) — evita reescrever 200 linhas por causa de uma.
- Emissora que **sai** do `target_stations` some da tela, mas a linha **não é apagada**: se voltar para o target, o valor reaparece.

### "Colar planilha"

Modal que aceita duas colunas vindas do Excel (TAB), `;` ou `,` + espaço. Vírgula sozinha **não** separa — ela é decimal e aparece dentro do dial (`88,3`). Parser em `frontend/src/utils/targetPmmPaste.js`, deliberadamente **puro** (sem React, sem rede) para rodar no runner nativo do Node (`node --test frontend/src/utils/targetPmmPaste.test.mjs`) sem instalar dependência de frontend — instalar dep no Windows poda o lockfile e quebra o Cloudflare Pages (regra 5 do `CLAUDE.md`).

- **Números em formato BR:** `.` é separador de milhar e some, `,` é decimal e o valor é arredondado. `12.345` → **12345** (doze mil), nunca 12,345.
- **Casamento da emissora, nesta ordem:** `short_id` exato → nome normalizado (sem acento, caixa baixa, espaço colapsado) com candidato **único** → nome + dial (tolerância 0,05 MHz). Nome que bate em mais de uma emissora e não traz dial vira `ambiguous`.
- **Cabeçalho de planilha** é descartado só na linha 1 e só quando a primeira coluna **não** resolve para nenhuma emissora conhecida — senão uma linha de dado real na posição 1 com valor inválido seria engolida em silêncio.
- **Preview obrigatório:** o botão "Aplicar" só habilita depois do "Conferir", e mostra `casadas / ambíguas / não encontradas / valor inválido` com o motivo linha a linha. **Só as casadas** são aplicadas — e aplicadas ao *rascunho*, não ao servidor: ainda é preciso Salvar.

### Guarda de saída — limitação conhecida

`useUnsavedGuard` avisa antes de perder o rascunho. Cobre **F5, fechar a aba e todos os links internos** (intercepta o clique em `a[href]` na fase de captura, antes do `<Link>`). **Não cobre o botão Voltar do browser**: `popstate` não é cancelável, e cobrir isso exigiria migrar a app inteira de `<BrowserRouter>` para `createBrowserRouter` + `RouterProvider` — o `useBlocker` do react-router 6.30 só existe em *data router*. Risco desproporcional por causa de uma tela.

## Onde aparece

Todas as superfícies **escondem o bloco "no target"** quando não há cadastro no escopo — quem não usa a feature vê a tela idêntica à de antes. O gate é sempre `stations_with_target > 0` (ou, no frontend-only, "alguma emissora do recorte tem `pmm_target`").

| Superfície | O que mostra | Base de contagem |
|---|---|---|
| `/insights` | cards **Impactos no target** (com sub "X de Y emissoras") e **CPM no target** | detecções aprovadas |
| `/detections` (grid) | pill **teal** na coluna-total por emissora, abaixo da pill de impactos | Σ `in_slot` |
| `/reports/airtime` | pill **teal** por linha, empilhada na mesma célula do PMM | — (é o `pmm_target` cru, não impactos) |
| `/campaigns` (bloco financeiro) | **Impactos**, **Impactos no target**, **CPM no target** | `in_slot + bonus` |
| CSV detalhado (`/detections/export`) | coluna `PMM no target` | — (valor cru) |
| CSV consolidado (`/reports/consolidated`) | `PMM`, `Impactos`, `PMM no target`, `Impactos no target` | detecções aprovadas |
| PDF de campanha | KPIs `Impactos` / `Impactos no target` + colunas na tabela "Por emissora" | detecções aprovadas |
| CSV/PDF da grade (WYSIWYG de `/detections`) | coluna `Impactos` (sempre) + `Impactos no target` (só com cadastro) | Σ `in_slot` |

### Exceção à regra de esconder

A coluna/linha **"Impactos"** (sem target) é **nova para todos os clientes** em `/campaigns`, no CSV consolidado, no PDF de campanha e no CSV/PDF da grade — pedido explícito do dono. Só o bloco **"no target"** é condicional.

### Base de contagem: cada tela espelha a própria base

`/insights` conta **todas as detecções aprovadas**; `/campaigns` conta **`in_slot + bonus`** (via `daily_play_summary`); a grade conta **Σ `in_slot`**. Essa divergência é **anterior a esta feature** — ver [detection-count-consistency.md](../architecture/detection-count-consistency.md). A regra adotada foi **espelhar a base de cada tela**: impactos e impactos-no-target da MESMA tela usam sempre o mesmo denominador de veiculações, então os dois números são comparáveis entre si. Não tente reconciliar o "Impactos no target" de `/insights` com o de `/campaigns` — eles nunca vão bater, pelo mesmo motivo que os totais de veiculação já não batem.

### CPM no target é sempre dinâmico

```
cpm_target = investido_executado ÷ impactos_no_target × 1000
```

**Inclusive em campanha com `fixed_cpm`.** O CPM fixo ([campaign-fixed-cpm.md](campaign-fixed-cpm.md)) é contratado sobre a **audiência total**; aplicá-lo ao recorte de público-alvo produziria um número sem significado comercial. Vale tanto no backend (`insights.Compute`) quanto no frontend (`CampaignFinancials` em `CampaignsPage.jsx`).

## Armadilhas — leia antes de abrir ticket

**1. A coluna "Impactos" do CSV consolidado NÃO reconcilia com o total do PDF — por design.**
O CSV consolidado é **por linha (material × emissora)**, então somar a coluna conta a mesma emissora **uma vez por material**. O total do PDF vem de `by_station` (uma linha por emissora, `ReportsHandler.Summary`) e é o número **correto**. Não "conserte" um para bater com o outro.

**2. `stations_with_target` em `/campaigns` pode contar emissoras que contribuem 0.**
A CTE `target_cov` conta `DISTINCT station_id` de `campaign_station_pricing ⋈ client_station_pmm` — mas uma emissora em modo `per_insertion` **sem preço por tipo cadastrado** é eliminada pela CTE `per_ins` (`JOIN campaign_station_type_pricing`) e não soma audiência. O badge pode dizer "2 de N com target" com o valor vindo de **uma só**. É o mesmo comportamento que `total_audience` já tinha — não é regressão, mas confunde. (A CTE é separada de propósito: somar as contagens de `per_ins` + `consolidated_ins` contaria em dobro emissoras presentes nos dois modos.)

**3. Assimetria de arredondamento entre CSV e PDF.**
`stations.pmm` é `numeric(10,2)`, então `pmm × count` quase nunca é inteiro:
- CSV consolidado (`reports.go`) **arredonda**: `fmt.Sprintf("%.0f", pmm*count)`.
- Total do PDF (`Summary.Totals.Impactos`, Go) **trunca**: `int64(pmm * count)`.
- Coluna "Impactos" da tabela do PDF (`pdfReport.js`, JS) **arredonda**: `Math.round(pmm*count)`.

Podem divergir em até **1 por emissora** — e o KPI "Impactos" do PDF pode ficar até 1×N abaixo da soma da sua própria coluna. Impactos **no target** não sofre disso: `pmm_target` é `INTEGER`, o produto é exato.

**4. O PDF de campanha tem menos detalhe que o CSV consolidado.**
O CSV traz as 4 colunas por **material × emissora**; no PDF só a tabela **"Por emissora"** ganhou as colunas. Decisão de escopo — a área útil da página A4 (180mm) não comporta mais duas colunas na tabela por material.

**5. `stations_count` de `/insights` precisou de `DISTINCT`.**
A CTE `per_station` passou a particionar por `client_id` (para resolver o target), então uma emissora usada por 2 clientes vira 2 linhas. Os contadores viraram `COUNT(DISTINCT station_id)`; as **somas** não foram afetadas. Se você mexer nessa query, mantenha o `DISTINCT`.

## Testes

- `workers/internal/catalog/client_station_pmm_test.go` — `BulkUpsert` (insert/update/delete) e `ZeroIsNotAbsent` (o caso que mais quebra em refactor).
- `workers/internal/catalog/insights_test.go` — `TargetPMM`, `TargetPMM_ZeroIsNotAbsent`, `TargetPMM_MultiClientNoDoubleCount`.
- `frontend/src/utils/targetPmmPaste.test.mjs` — `node --test` (sem dependência de frontend, ver acima): números BR, casamento por short_id/nome/dial, ambiguidade, cabeçalho.

## Referências

- Spec/plano: `docs/superpowers/specs/2026-07-21-client-target-pmm-design.md`, `docs/superpowers/plans/2026-07-21-client-target-pmm.md`.
- [detection-count-consistency.md](../architecture/detection-count-consistency.md) — por que as bases de contagem divergem entre telas.
- [multi-attribution.md](multi-attribution.md) — por que o target resolve pelo cliente de cada projeção.
- [campaign-fixed-cpm.md](campaign-fixed-cpm.md) — por que o CPM fixo não vale no target.
- [campaign-reports.md](campaign-reports.md), [insights-dashboard.md](insights-dashboard.md), [detections-view.md](detections-view.md) — as telas/relatórios afetados.
