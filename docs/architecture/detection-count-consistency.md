---
status: implementado
ultima-verificacao: 2026-08-17
codigo-relacionado:
  - workers/internal/catalog/detection_filter.go
  - workers/internal/catalog/distribution_rules.go
  - migrations/0065_quota_aware_summary.up.sql
  - workers/internal/catalog/detections.go
  - workers/internal/catalog/insights.go
  - workers/internal/catalog/live_map.go
  - workers/internal/catalog/management_overview.go
  - workers/internal/catalog/detection_consistency_test.go
  - migrations/0029_daily_summary_exclude_audit_rejected.up.sql
---

# Consistência de contagem de veiculações (conjunto "aprovado")

Define o **conjunto único de detecções que pode ser contado ou listado em
qualquer número de veiculação visível ao usuário** e cataloga toda query do
sistema que toca esse número, com seu veredito de conformidade.

> **Origem (2026-06-19).** Operador reportou: em `/detections`, campanha ASAAS
> na 90fm, a **modal** de um dia mostrava **4** veiculações do corte de 15s mas
> a **célula da grid** mostrava **2** (déficit falso). Causa: cada tela filtrava
> as detecções por um critério diferente. A mesma veiculação aparecia como
> número diferente em lugares diferentes. Pedido: *"não posso ter uma parte do
> sistema mostrando que tal emissora veiculou 3, outra dizendo que foi 5, e
> outra dizendo 2 — padronize e documente."*

---

## O conjunto "aprovado"

Uma detecção **conta** como veiculação para o usuário quando satisfaz os **três**:

```sql
retracted_at IS NULL            -- não retratada pela desambiguação §18.2.2
AND ignored_at  IS NULL         -- não ignorada por admin ("Desconsiderar")
AND evidence_status <> 'audit_rejected'  -- não rejeitada pelo audit §9.9
```

Cada predicado remove um tipo de "veiculação que não aconteceu de fato como
atribuída":

| Predicado | Remove | Por quê não conta |
|-----------|--------|-------------------|
| `retracted_at IS NULL` | Retratadas | A desambiguação de versão (§18.2.2) decidiu que outra detecção representa o mesmo evento no ar — contar as duas duplica. |
| `ignored_at IS NULL` | Ignoradas | Admin marcou manualmente "Desconsiderar" na detail page; some dos agregados de propósito. |
| `evidence_status <> 'audit_rejected'` | Rejeitadas pelo audit | O clipe salvo não bate com o master atribuído (§9.9) — falso positivo forense. |

## Fonte única de verdade

O fragmento vive em **uma** constante, não duplicado por query:

- **`catalog.ApprovedDetectionsFilter`** ([detection_filter.go](../../workers/internal/catalog/detection_filter.go))
  — string SQL com os 3 predicados, alias `d`. Sem bind params (não desloca `$N`).
- **View `daily_play_summary`** ([migration 0029](../../migrations/0029_daily_summary_exclude_audit_rejected.up.sql))
  — embute o MESMO filtro no CTE `actual`. Toda query que lê a view **herda** o
  conjunto e **não** deve reaplicá-lo.

**Regra para código novo:** se você lê `detections` direto pra um número
visível ao usuário, alias a tabela como `d` e concatene
`" AND " + catalog.ApprovedDetectionsFilter` no `WHERE`. Se lê via
`daily_play_summary`, não faça nada — já vem filtrado.

---

## Matriz de conformidade (auditoria 2026-06-19)

Toda query que conta/lista `detections` para exibição. `CANÔNICA` = aplica os 3;
`VIA-VIEW` = lê `daily_play_summary` (herda); `EXCEÇÃO` = não filtra de propósito
(ver §Exceções).

### Lê `detections` direto

| Arquivo · função | Tela | Antes | Agora |
|---|---|---|---|
| `detections.go` · `List` | **modal /detections** | ❌ faltava `retracted`+`ignored` | ✅ CANÔNICA |
| `detections.go` · `ListPaged` | /reports/airtime (lista) | ✅ CANÔNICA | ✅ CANÔNICA |
| `detections.go` · `IterateForExport` | export CSV | ✅ CANÔNICA | ✅ CANÔNICA |
| `detections.go` · `AggregateByMaterial` | painel material (airtime) | ✅ CANÔNICA | ✅ CANÔNICA |
| `detections.go` · `AggregateByMaterialStation` | CSV consolidado | ✅ CANÔNICA | ✅ CANÔNICA |
| `detections.go` · `AggregateByStation` | PDF por emissora | ✅ CANÔNICA | ✅ CANÔNICA |
| `insights.go` · `aggregateCore` | **/insights** KPIs/impactos/demografia | ❌ faltava `ignored` | ✅ CANÔNICA |
| `insights.go` · `aggregateBuckets` (extras/bonus) | **/insights** série temporal (extras) | ❌ faltava `ignored`+`audit_rejected` | ✅ CANÔNICA |
| `insights.go` · `computeCPM` (impactos) | **/insights** CPM/investimento | ❌ faltava `ignored`+`audit_rejected` | ✅ CANÔNICA |
| `live_map.go` · `queryStations` (MAX) | **/live-map** "última veiculação" | ❌ faltava `retracted` | ✅ CANÔNICA |
| `live_map.go` · `queryRecentDetections` | /live-map feed | ✅ CANÔNICA | ✅ CANÔNICA |
| `management_overview.go` · `queryKPIs` | /management KPIs | ✅ CANÔNICA | ✅ CANÔNICA |
| `management_overview.go` · `queryStations` (MAX) | **/management** "última veiculação" | ❌ faltava `retracted` | ✅ CANÔNICA |
| `management_overview.go` · `queryRecentDetections` | /management feed | ✅ CANÔNICA | ✅ CANÔNICA |

### Lê via `daily_play_summary` (herda canônico)

`daily_summary.go · ListByCampaign` (grid de /detections, /materials, wizard),
`station_failures.go · ListForDate`, `campaign_failures.go · ListForDate /
ListHistorical / Get` (/admin/station-failures por emissora e por campanha),
`notifications.go · List / MarkAllReadInWindow` (sininho + digest),
`insights.go · aggregateInvestment` + `aggregateBuckets` (CTE `agg`) +
`computeCPM` (subqueries de executado).

---

## Exceções deliberadas

Estas **não** aplicam o filtro, por design. Comentadas no código pra ninguém
"consertar" por engano.

| Local | Conta cru porque… |
|-------|-------------------|
| `detections.go · Get` (/detections/:id) | Detalhe forense de **uma** linha. O operador precisa abrir uma veiculação retratada/ignorada/rejeitada pra inspecionar; a página renderiza os badges de estado. Não é agregado contável. |
| `system_health.go · summarizeDataPipeline` (`LastDetectionAt`, `Detections1h`) | Contadores de **liveness** do pipeline ("o matcher está produzindo saída?"). Uma detecção retratada ainda prova que o pipeline está vivo. Não é tally de veiculação por emissora. |
| `system_health.go · AuditRejected7d` | Métrica **inversa** proposital (`evidence_status = 'audit_rejected'`) pra visibilidade operacional (incidente 2026-06-12). |
| `commercials.go` (delete guard `COUNT(*) ... WHERE commercial_id`) | Guard de integridade referencial — bloqueia delete se existir **qualquer** linha (mesmo retratada). É "há linhas?", não "quantas veiculações?". |

> **Deixou de ser exceção em 2026-08-17:** `distribution_rules.go · recategorizeScope /
> RecategorizeForMaterial` (e o reconciler `projrecon`) **aplicam** o filtro canônico
> desde o fechamento por cota. Tinha que mudar: com cota, uma tocada retratada
> contada no SQL ocuparia vaga que o motor Go (`categorizer.Settle`, alimentado por
> `loadCellDayPlays`) não conta — os dois motores divergiriam em toda célula-dia que
> tivesse uma. Efeito: a categoria da linha **não-aprovada não é mais reescrita** pelo
> recat; ela é regravada quando a linha volta ao conjunto (o refechamento da célula).

---

## Por que a divergência existia (caso ASAAS)

O caso concreto que disparou tudo: a má-atribuição **15s→30s** (ver
[version-disambiguation.md §18.2.2-v2](version-disambiguation.md)). O v1 retrata
~55% das veiculações do corte 15s (atribui ao 30s), populando `retracted_at`. A
modal (`Detections.List`) listava essas retratadas — e a grid (view) não as
contava → 4 vs 2.

> **Importante:** padronizar a contagem **não corrige** a má-atribuição — só faz
> todas as telas concordarem no número aprovado. E esse número estava **baixo**:
> a auditoria de 2026-06-19 mediu **110 veiculações perdidas em 14 dias** (o 15s
> retratado + o 30s `audit_rejected` → contadas em lugar nenhum), **104
> recuperáveis**. A recuperação é a **§18.2.2-v2b** (un-retract no caminho de
> rejeição + `cmd/backfill-unretract-displaced`), descrita em
> [version-disambiguation.md](version-disambiguation.md). São dois problemas
> distintos: *consistência de exibição* (este doc) e *correção/recuperação de
> atribuição* (v2b).

## Sincronização de categoria

Este doc padroniza **quais linhas contam** (o conjunto aprovado). Um problema
distinto — e complementar — é garantir que, para uma linha que conta, a
**categoria** gravada (`detection_campaigns.category`, o que a view
`daily_play_summary` e a grade exibem como in_slot/out_slot/out_date/bonus)
está sempre certa em relação às regras/overrides vivos da campanha. Ver
[projection-category-invariant.md](projection-category-invariant.md) — inclui
o caso COPA 10/07, onde a categoria de uma projeção fan-out (F-119) ficou
`orphan` (hoje `bonus`) presa porque o recat só escopava a campanha-base da
tocada.

A regra que decide a categoria é o fechamento por cota da célula-dia —
[quota-aware-categorization.md](../features/quota-aware-categorization.md).

## Qual categoria vale dinheiro (base financeira)

Contar as linhas certas não basta: `/campaigns` e `/insights` divergiam também
porque **davam pesos diferentes às categorias**. Desde 2026-08-17 (decisão D3) as
duas compartilham a mesma base:

| Base | Categorias que entram |
|---|---|
| Financeira em `/campaigns` e `/insights` | `in_slot` fatura (**Investido**), `bonus` é entrega gratuita (**Bonificação**). Desde 2026-08-17 as DUAS telas partem assim: `/campaigns` expõe `total_invested` (= `unit × in_slot`) e `total_bonus_value` (= `unit × bonus`) separados, espelhando os dois cards do `/insights`. Impactos e inserções continuam somando `in_slot + bonus` nas duas |
| Déficit (`daily_play_summary.deficit`) | `max(0, expected − in_slot)` |
| Nada | `out_slot` — não fatura, não bonifica e **não abate o déficit** |

Antes, `/insights` somava `in_slot + out_slot` no executado (faturava veiculação
fora do horário comprado) e o `bonus` da view re-somava o excedente que já estava
em `in_slot`. Os números do cliente **caem** — é correção, não regressão.

Contagens brutas continuam existindo e são outra coisa: o PDF de campanha e o CSV
consolidado somam **todas** as categorias aprovadas (ver
[client-target-pmm.md](../features/client-target-pmm.md)). Cada tela espelha a
própria base — não tente reconciliar entre telas sem antes olhar qual base cada
uma usa.

## Validação

- **`detection_consistency_test.go`** (DB-gated, `TEST_DATABASE_URL`): seeda 3
  veiculações aprovadas + 1 retratada + 1 ignorada + 1 rejeitada-pelo-audit
  (mesma emissora/material/dia) e exige que os **três** caminhos —
  `Detections.List` (modal), `aggregateCore` (/insights) e `daily_play_summary`
  (grid) — retornem **3**. Trava a regressão do "3 vs 5 vs 2".
- `go build ./...`, `go vet` dos pacotes alterados: limpos.
