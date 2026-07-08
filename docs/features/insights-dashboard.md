---
status: implementado
ultima-verificacao: 2026-07-08
codigo-relacionado:
  - workers/internal/catalog/insights.go
  - workers/internal/catalog/insights_test.go
  - workers/internal/api/handlers/insights.go
  - workers/internal/api/handlers/insights_test.go
  - workers/internal/api/router.go
  - workers/cmd/api/main.go
  - frontend/src/pages/InsightsPage.jsx
  - frontend/src/pages/InsightsPage.css
  - frontend/src/components/insights/
  - frontend/src/utils/exportInsights.js
  - frontend/src/api/hooks.js (useInsights)
  - frontend/src/components/Sidebar.jsx
  - frontend/src/App.jsx
---

# Dashboard de Veiculação (`/insights`)

Tela única que consolida KPIs e gráficos demográficos por cliente × campanha(s) × período × emissoras. Substitui o PDF manual que o time comercial montava ao fim de cada campanha. Exporta como PNG ou PDF.

Spec original: [docs/superpowers/specs/2026-05-26-insights-dashboard-design.md](../superpowers/specs/2026-05-26-insights-dashboard-design.md).
Plano de implementação: [docs/superpowers/plans/2026-05-26-insights-dashboard.md](../superpowers/plans/2026-05-26-insights-dashboard.md).

## Quem vê o quê

| Role | Cliente | Campanhas | Emissoras |
|---|---|---|---|
| **admin** | escolhe um na barra de filtros | as do cliente escolhido | as de interseção das campanhas escolhidas |
| **cliente** (viewer) | chip read-only com o próprio nome — sem dropdown | só as suas (forçado pelo JWT scope no backend) | só as das suas campanhas |

O backend resolve isso via `auth.ClientScopeFromContext(r.Context())`. Quando o scope é não-nil (role viewer), `client_id` da query é ignorado e a tentativa de pedir campanhas de outro cliente → 403 ("forbidden") com erro contendo "cross-client" no log.

## Endpoint

`GET /api/v1/internal/insights?client_id=<uuid>&campaigns=<csv>&from=<YYYY-MM-DD>&to=<YYYY-MM-DD>&stations=<csv>`

| Param | Obrigatório? | Default | Notas |
|---|---|---|---|
| `client_id` | sim p/ admin; ignorado p/ viewer | — | Viewer é forçado pelo JWT |
| `campaigns` | sim | — | CSV de uuids, 1 ≤ N ≤ 50 |
| `from` / `to` | não | mês corrente | YYYY-MM-DD |
| `stations` | não | todas | CSV de uuids; vazio = todas |

Resposta: ver `catalog.InsightsPayload` — KPIs, class_pyramid, age_ranges, veiculacoes_breakdown, buckets.

## Fórmulas (autoridade é a spec; cópia rápida aqui)

| Métrica | Como é calculada |
|---|---|
| **Impactos** | `Σ_estação (detections_count × PMM)`. Estação sem PMM → não soma (mas conta em `stations_count`) |
| **Impactos por gênero** | `Σ (count × PMM × gender_pct / 100)` (percentuais em escala 0-100 no `stations.metadata.audience_profile`) |
| **CPM** | Padrão: `(investido_executado / impactos) × 1000`. Guard pra impactos=0 → CPM=0. Override por `campaigns.fixed_cpm` quando setado: média ponderada por impactos do `COALESCE(fixed_cpm, dynamic_cpm)` de cada campanha — ver [campaign-fixed-cpm.md](campaign-fixed-cpm.md). Como usa `investido_executado`, herda o comportamento proporcional consolidado abaixo |
| **Bonificação** | Soma do valor das veiculações "bonus" da view `daily_play_summary` (orphan + in_slot acima do expected). Valor é `unit_value × bonus_count` em modo per_insertion; em consolidated é `cv × bonus_na_janela / plano_da_campanha_INTEIRA` (mesma taxa estável por inserção do investido) |
| **Investido contratado** | `consolidated`: `cv × overlap_days/total_days`. `per_insertion`: `Σ_type (unit_value × expected_count)`. (Não é exibido em nenhum card hoje) |
| **Investido executado** | **Se QUALQUER emissora da seleção é `consolidated`** (regra do fornecedor): valor **total contratado, FIXO** = `Σ (consolidated_value + valor cheio das por-inserção)`, não varia com o período; e a Bonificação some. **100% `per_insertion`**: `Σ_type (unit_value × (in_slot+out_slot))` (por veiculação). **Ver §"Consolidado: total fixo estilo fornecedor"** |
| **Buckets — programado** | `SUM(expected)` da view daily_play_summary |
| **Buckets — déficit** | `max(0, expected - in_slot - out_slot)` |
| **Buckets — extras** | `count(detections WHERE category='orphan')` (NÃO inclui in_slot-acima-de-expected, pra evitar double-count no gráfico) |

### Por que "extras" no chart difere de "bonificação" no KPI

- **Chart (`extras`):** orphan puro. Mostrar in_slot + extras no mesmo gráfico com extras = bonus seria double-count visual.
- **KPI (`Bonificação`):** bonus completo (orphan + in_slot acima do expected). É a métrica comercial de "mídia ganha".

Decisão deliberada e documentada nos comentários do `aggregateBuckets` em [workers/internal/catalog/insights.go](../../workers/internal/catalog/insights.go).

### Consolidado: total fixo estilo fornecedor

**Regra vigente (2026-07-08, tarde):** se **QUALQUER emissora da seleção** tem pricing `consolidated`, o `/insights` entra em **modo fornecedor**:

- **Investido** = **valor total contratado, FIXO** = `Σ_estação (consolidated_value das consolidadas + unit_value × plano_da_campanha_inteira das por-inserção)`. **Não varia com o período** selecionado (junho, mês todo, 1 dia → sempre o mesmo total). É como o fornecedor mostra e os clientes já estão acostumados.
- **Bonificação**: **some** — o backend zera e o frontend **não renderiza o card** (grid de cards vira 4 colunas). No fornecedor fica zerado.
- **CPM**: usa o `fixed_cpm` da campanha (consolidado sempre tem cadastrado); sem ele, cai no dinâmico `total ÷ impactos × 1000`.
- **Flag `consolidated: true`** no payload dispara o comportamento no frontend.
- **Campanha 100% `per_insertion`**: nada muda — segue por veiculação, com Bonificação.

Implementação: `catalog.Insights.consolidatedSummary` calcula o total + a flag (independente de `from/to`); o `Compute` sobrescreve `inv.Executado` e zera `bon` quando `hasConsolidated`. Frontend: `KpiCards` esconde a Bonificação e `InsightsPage` aplica `in-row--cards--4` quando `data.consolidated`.

> **Nota:** o cálculo **Modelo B (proporcional)** abaixo continua existindo no `aggregateInvestment` (e nos testes diretos), mas é **sobrescrito** pelo total fixo para consolidado no `Compute` — preservado caso a regra mude de novo. Vale hoje só como o número por-veiculação de campanhas `per_insertion`.

<details><summary>Modelo B — proporcional ao período (camada de baixo, sobrescrita em consolidado)</summary>

Em campanha com pricing `consolidated`, o **Investido executado** (do `aggregateInvestment`, hoje sobrescrito) é **proporcional ao período selecionado**:

```
executado = cv × LEAST(1, entregue_na_janela ÷ plano_da_campanha_INTEIRA)
bonus     = cv ×        (excedente_na_janela ÷ plano_da_campanha_INTEIRA)
```

A chave é o **denominador = plano da campanha inteira** (fixo, `SUM(expected)` em `[start_date, end_date]`), **não** o plano da janela. Isso dá:

- **Proporcional:** "de 19/06 a 30/06" mostra a fração do contrato entregue nesse recorte; ampliar o período **soma**. `cv ÷ plano_total` é a taxa estável por inserção.
- **Monotônico / sem deflação por dia futuro:** dia ainda-não-veiculado entrega 0 no numerador, então alargar a janela pra frente nunca faz o número cair (nem precisa de clamp de "hoje").
- **Cap em 100% + bônus:** over-delivery (entregue > plano) capa o Investido no contrato; o excedente aparece **só** na Bonificação, valorizado à mesma taxa por inserção. Fim do double-count.
- **Déficit reduz (correto):** emissora que entregou menos que o plano mostra `< contrato` — reflete a não-entrega.

**Campanha ativa:** enquanto a campanha não termina, "campanha inteira" mostra o **entregue até agora** (não o contrato cheio), completando conforme veicula. É o comportamento por-entrega (não por-tempo) — decisão de negócio registrada na spec.

`per_insertion` é aditivo e não muda (`Σ unit_value × tocadas`). Os dois pontos que replicam a fórmula (`aggregateInvestment` via CTEs `cs_window`/`cs_plan`, e o slow-path de `computeCPM`) usam o mesmo denominador de plano cheio. Spec: [docs/superpowers/specs/2026-07-08-insights-consolidated-period-proportional-design.md](../superpowers/specs/2026-07-08-insights-consolidated-period-proportional-design.md).

</details>

## Granularidade automática do gráfico 4

- ≤ 31 dias filtrados → buckets diários (`YYYY-MM-DD`)
- > 31 dias → buckets mensais (`YYYY-MM`)

Decidido no backend (`aggregateBuckets`). Frontend formata o label localmente.

## Exportação PNG / PDF

- **PNG:** `html2canvas` captura `<div ref={dashboardRef} className="in-body">`. Scale 2 (retina). Nome do arquivo: `dashboard-<slug-cliente>-<from>-<to>.png`.
- **PDF:** A4 paisagem, 2 páginas. Capa com logo, nome do cliente, período, contagem de campanhas. Página 2 = PNG do dashboard centralizado.

Reutilizar o padrão de [pdfReport.js](../../frontend/src/utils/pdfReport.js) — mesmo `jsPDF`.

## Como adicionar uma nova métrica

1. Adicionar campo no struct `InsightsKPIs` (ou similar) em [workers/internal/catalog/insights.go](../../workers/internal/catalog/insights.go).
2. Calcular na SQL apropriada (`aggregateCore`, `aggregateInvestment` ou `aggregateBuckets`).
3. Adicionar teste em `insights_test.go` (tem fixtures `insSeedStation`/etc).
4. Atualizar o struct TypeScript implícito no frontend e renderizar (card ou chart).

## Troubleshooting

- **Impactos vêm baixos:** verifique se as estações têm PMM e perfil cadastrado. `psql -c "SELECT name, pmm, metadata->'audience_profile' FROM stations WHERE pmm IS NULL LIMIT 10"`. Edite em `/stations/:id/edit`.
- **Export PNG vazio:** confira se `dashboardRef.current` está mountado (a captura roda em `handleExport*`). Se o usuário clica antes do payload carregar, o ref é válido mas o conteúdo é o empty state — comportamento esperado.
- **PDF cortado:** ajustar `margin` em [exportInsights.js:73](../../frontend/src/utils/exportInsights.js) ou diminuir `scale` do html2canvas (atualmente 2).
- **Performance ruim (>2s):** rodar com tracing habilitado (Jaeger) e identificar o CTE lento. Candidatos: `aggregateCore` se há muitas estações, `aggregateInvestment` se há muitas campanhas. Se passar de 2s P95 em prod, considerar materialized view por mês.
- **Cliente com 50+ campanhas:** o select faz busca local; se ficar lento, virtualizar o `RSelect` ou adicionar busca server-side.

## Limitações conhecidas

- "Extras" no gráfico 4 captura **apenas** `category='orphan'`. Detections `in_slot` acima do expected NÃO viram extras nesse gráfico (continuam em `in_slot`). A bonificação total no KPI usa a definição mais ampla (orphan + in_slot acima).
- Investido em modo `consolidated` prorrateia linearmente por dias (`overlap_days/total_days`), sem considerar distribuição irregular de slots dentro da campanha.
- Materiais sem `type_id` ficam ausentes da view `daily_play_summary` — afeta os buckets do gráfico 4 (não aparecem ali), MAS continuam contando em `aggregateCore` (impactos + breakdown) que lê detections direto.
- Estação sem `audience_profile.gender` (ou `socialClass`, `ageRanges`) → não soma na dimensão correspondente. O card de gênero pode subestimar quando muitas estações estão sem perfil.

## Decisões de modelagem que diferiram do plano original

Durante a implementação foram identificados desvios da spec/plano:

1. **Coluna era `metadata`, não `meta`.** O plano supunha `s.meta`, mas o DB tem `s.metadata` (Go aliasa para `Meta` no struct).
2. **Percentuais 0-100, não 0-1.** Os percentuais em `audience_profile` (gender, social_class, age_ranges) estão em escala 0-100. SQL multiplica por `/100.0` ao aplicar.
3. **Pricing é por (campaign, station), não por campanha.** A tabela existente é `campaign_station_pricing` (modo + consolidated_value) + `campaign_station_type_pricing` (unit_value por type). O plano supunha `campaigns_pricing` (que não existe).
4. **`daily_play_summary` view foi reaproveitada.** O plano expandia `generate_series` + `weekday_mask` manualmente; a view já faz isso e ainda aplica overrides — usamos ela em `aggregateInvestment` e `aggregateBuckets`.

Essas decisões estão refletidas nos commit messages individuais (`git log workers/internal/catalog/insights.go`).
