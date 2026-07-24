---
status: implementado
ultima-verificacao: 2026-07-24
codigo-relacionado:
  - migrations/0054_client_station_pmm.up.sql
  - migrations/0055_client_target_label.up.sql
  - workers/internal/catalog/client_station_pmm.go
  - workers/internal/catalog/clients.go
  - workers/internal/api/handlers/clients.go
  - workers/internal/api/handlers/client_target_pmm.go
  - workers/internal/catalog/insights.go
  - workers/internal/catalog/campaigns.go
  - workers/internal/catalog/detections.go
  - workers/internal/api/handlers/reports.go
  - frontend/src/pages/ClientTargetPmmPage.jsx
  - frontend/src/pages/ClientsPage.jsx
  - frontend/src/components/insights/KpiCards.jsx
  - frontend/src/utils/pdfReport.js
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

## Rótulo do público-alvo — `clients.target_label`

Os números acima respondem "quantos do meu público", mas não dizem **qual** público. `clients.target_label` (migration **0055**) é um texto livre por cliente — ex.: `"Homens 25-49, classe AB"` — que as telas usam como **sufixo descritivo** dos rótulos existentes: "Impactos no target" vira "Impactos no target (Homens 25-49, classe AB)".

- Coluna `TEXT` **nullable, sem default**: `NULL` = cliente sem rótulo → as telas continuam dizendo apenas "no target". Nenhum cliente existente ganha rótulo com a migration.
- Sem versionamento temporal, igual ao `pmm_target`: corrigir o rótulo muda a legenda de relatórios passados.
- Gravado pelos endpoints de cliente já existentes (`POST`/`PUT /v1/internal/clients`), campo `target_label` no corpo. O handler apara espaços, colapsa `""` em `NULL` e recusa acima de **200 caracteres** (a UI sugere 60). O DDL não tem `CHECK` — o limite mora onde a mensagem de erro é acionável.
- A tag JSON é `target_label` **sem `omitempty`** em toda a superfície (`Client`, `SummaryResponse.Client`, `InsightsPayload`): o frontend precisa do `null` explícito para distinguir "sem rótulo" de string vazia — mesma convenção do `pmm_target`.

### Onde o rótulo entra — e onde NÃO entra

| Superfície | Comportamento |
|---|---|
| `GET/POST/PUT /v1/internal/clients` | `target_label` no payload do cliente |
| `GET /reports/campaigns/{id}/summary` (JSON do PDF) | `client.target_label` |
| CSV **consolidado** de campanha | cabeçalhos viram `PMM no target (rótulo)` / `Impactos no target (rótulo)` |
| `GET /v1/internal/insights` | `target_label` no topo do payload — **só** quando todas as campanhas filtradas são de **um único** cliente **e** ele tem rótulo; filtro multi-cliente devolve `null` |
| CSV **detalhado** (`/detections/export`) | **fica sem rótulo, de propósito** |
| `/clients` (modal de cliente) | campo "Público-alvo (target)", texto livre, `maxLength=60` (o backend aceita 200); vazio grava `NULL` |
| `/insights` (cards) | 2ª linha sob "Impactos no target" / "CPM no target" com o rótulo, truncada com reticências (`title` traz o texto inteiro) |
| `/detections` (grid) | **só no tooltip** da pill teal; o label visível fica curto (coluna de 180px) |
| `/reports/airtime` | **só no `title`** da pill teal |
| `/campaigns` (bloco financeiro) | **só nos tooltips** de "CPM no target" e "Impactos no target" |
| PDF de campanha | linha "Público-alvo: …" no **cabeçalho**, sob "cliente · período" (**não** no KPI, ver abaixo) |

A regra do `/insights` existe porque `impactos_target` ali é uma **soma sobre campanhas de clientes potencialmente diferentes**: rotular esse número com o target de um dos clientes seria mentira. O SQL é um `CASE WHEN count(DISTINCT client_id) = 1 THEN max(target_label) END`.

Nas telas o rótulo é **sempre sufixo, nunca gatilho**: o que decide se o bloco "no target" aparece continua sendo `stations_with_target > 0`. Cliente com rótulo e sem PMM cadastrado não ganha nada; cliente com PMM e sem rótulo vê exatamente a tela de antes.

Onde o espaço é apertado (pills de `/detections` e `/reports/airtime`, rótulos do bloco financeiro de `/campaigns`) o rótulo entra **só no tooltip** — texto visível ali é medido em poucos caracteres e um rótulo de 60 quebraria o layout. Nos cards de `/insights` ele ganha uma **segunda linha** em corpo menor em vez de virar sufixo na mesma linha: as trilhas do grid têm 180px, e um sufixo inline quebraria em 2-3 linhas empurrando o valor e desalinhando as alturas dos cards da mesma linha. Truncado com reticências, o texto completo fica no `title`.

No **PDF de campanha** o rótulo NÃO entra no KPI "Impactos no target": aquele box usa auto-fit de fonte (`fitFontSize`) e o rótulo sozinho já encosta no piso de 5,5pt com 5 boxes na página. Ele vai para o **hero/cabeçalho**, numa linha "Público-alvo: …" sob "cliente · período"; o card do hero cresce de 36mm para 44mm nesse caso e `drawHero` passou a **devolver a base do card**, com o `kpiY` derivado dela (`heroBottom + 7,5`) em vez do literal `76`. Sem rótulo a conta dá exatamente 76 e o PDF sai idêntico ao de antes.

Pelo mesmo motivo o CSV detalhado não recebe sufixo: `campaign_id` é **opcional** naquele export, então as linhas podem cobrir campanhas de vários clientes, cada um com o seu target — um rótulo único no cabeçalho estaria errado para parte das linhas. O CSV consolidado é sempre de **uma** campanha, logo de um cliente só, e aí o sufixo é seguro.

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

### Base de contagem — `/campaigns` e `/insights` CONVERGEM (desde 2026-07-24)

> **Atualização 2026-07-24:** `/campaigns` e `/insights` passaram a usar a **base única
> `in_slot + bonus`** via o helper compartilhado `financialBaseCTE` — ver
> [shared-financial-base.md](../architecture/shared-financial-base.md). Para o **mesmo
> período/filtro** os dois **batem** (impactos, impactos no target, investido, CPM no
> target), garantido por construção e travado pelo teste `TestFinancialParity_CampaignsVsInsights`.
> O `/insights` abre no mês corrente por default e o `/campaigns` ganhou um seletor de
> período (default mês atual, com preset "Acumulado"); para comparar, use a **mesma janela**
> nas duas telas.

Ainda **não** convergem com estas superfícies (base própria, por design):
- **a grade de `/detections`** conta **Σ `in_slot`**;
- **exportáveis** (CSV/PDF de campanha e de grade) mantêm as bases documentadas nas
  armadilhas #4/#5 abaixo.

Histórico da divergência (anterior à unificação): [detection-count-consistency.md](../architecture/detection-count-consistency.md).

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

**3. Arredondamento: tudo arredonda, e tem que continuar assim.**
`stations.pmm` é `numeric(10,2)`, então `pmm × count` quase nunca é inteiro. Os três pontos de cálculo **arredondam**, de propósito:
- CSV consolidado (`reports.go`): `fmt.Sprintf("%.0f", pmm*count)`.
- Total do PDF (`Summary.Totals.Impactos`, Go): `int64(math.Round(pmm * count))`.
- Coluna "Impactos" da tabela do PDF (`pdfReport.js`, JS): `Math.round(pmm*count)`.

O total do PDF **truncava** originalmente, o que fazia o KPI do topo ficar até 1×N **abaixo da soma da sua própria coluna, no mesmo documento**. Corrigido em `0c22def`. Se mexer em qualquer um dos três, mantenha o arredondamento nos outros dois. Impactos **no target** não sofre disso: `pmm_target` é `INTEGER`, o produto é exato.

**4. "Impactos" tem DUAS definições nos exportáveis — decisão consciente do dono (2026-07-21).**
- PDF de campanha e CSV consolidado: `pmm × count`, onde `count` são **todas** as categorias aprovadas (in_slot + out_slot + out_date + bônus).
- CSV e PDF de **grade**: `pmm × Σ in_slot`.

O mesmo cliente, na mesma campanha, vê números diferentes conforme o relatório que baixar. **Isso não é bug.** É a mesma regra de "cada superfície espelha a base da sua tela de origem" descrita acima: o relatório de grade espelha a grade de `/detections` (que mostra `in_slot` na pill), e o PDF espelha o conjunto aprovado. Alternativa avaliada e **recusada**: unificar numa base só — quebraria a coerência entre cada relatório e a tela que o gerou.

**4b. A coluna "Impactos" do CSV de grade É somável.**
Ela é rateada **por material** (`pmm × in_slot daquele material naquela emissora`), não repetida por linha. Arrastar a coluna no Excel dá o total certo. Isso foi corrigido em `ddfff07` — a primeira versão emitia o valor de nível-emissora em cada linha de material, o que **triplicava** a soma numa emissora com 3 materiais. Se mexer no `buildGridReportModel`, o modelo mantém os dois níveis de propósito: `byStation[].impactos` (nível emissora, alimenta a nota do PDF e a pill do `StationTotalCell`) e o valor por linha do CSV. Não colapse os dois.

**5. O PDF de campanha tem menos detalhe que o CSV consolidado — decisão consciente do dono (2026-07-21).**
O CSV traz as 4 colunas por **material × emissora**; no PDF só a tabela **"Por emissora"** ganhou as colunas. Motivo técnico: na tabela de detalhe do PDF sobram 42mm para a coluna "Material", e as duas colunas novas a deixariam com ~8mm (ou ~26mm mesmo encolhendo o resto) — títulos como "VERISURE CARVÃO 30S" não caberiam. Alternativas avaliadas e **adiadas**: virar aquela seção para paisagem (267mm úteis) ou trocar as 4 colunas de status por 1 de Impactos. Se a paridade PDF↔CSV virar demanda, a paisagem é o caminho.

**6. `stations_count` de `/insights` precisou de `DISTINCT`.**
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
