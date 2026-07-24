---
status: implementado
ultima-verificacao: 2026-07-24
codigo-relacionado:
  - workers/internal/catalog/financial_base.go
  - workers/internal/catalog/campaigns.go
  - workers/internal/catalog/insights.go
  - workers/internal/catalog/financial_parity_test.go
  - migrations/0052_daily_play_summary_fn.up.sql
---

# Base financeira compartilhada (`financialBaseCTE`)

`/campaigns` (bloco financeiro) e `/insights` (KPIs) mostram os mesmos números —
**Impactos**, **Impactos no target**, **Investido/Executado**, **CPM no target** — para
o mesmo período/filtro. Isso é garantido porque as duas telas **embutem o mesmo CTE**:
`financialBaseCTE`, em [financial_base.go](../../workers/internal/catalog/financial_base.go).

## Por que existe

Antes de 2026-07-24 as duas telas tinham **cálculos independentes** que derivaram no
tempo: o "Modelo B" de investido foi aplicado só no `/insights`, a base de contagem era
diferente (`/insights` contava todas as detecções aprovadas; `/campaigns`, `in_slot+bonus`),
e a janela era diferente (`/insights` no mês corrente, `/campaigns` na campanha inteira).
Resultado: os números não batiam e ninguém conseguia reconciliar. A causa raiz era
estrutural — **dois caminhos de código para a mesma pergunta**. A solução foi um único
primitivo compartilhado.

## O que o helper devolve

`financialBaseCTE(campaignsP, clientP, stationsP, fromP, toP, todayP)` é um **builder de
string SQL** (no estilo de `monthsElapsedSQL`/`ApprovedDetectionsFilter` — sem migration).
Devolve uma cadeia de CTEs terminando em `fin_base`, uma linha por **(campanha, emissora)**:

| coluna | significado |
|---|---|
| `campaign_id`, `station_id`, `client_id` | chave |
| `plays` | **base A** = `Σ (in_slot + bonus)` na janela, de `daily_play_summary_for` |
| `invested` | per_insertion `Σ unit_value × (in_slot+bonus)`; consolidado `consolidated_value × meses_no_período` |

Os consumidores fazem os joins de `pmm`/`pmm_target`/demografia por cima — o helper
centraliza só o que estava divergindo (`plays` e `invested`).

### Contrato dos parâmetros

- **params são placeholders** (`"$1"`…); o consumidor controla a numeração.
- `todayP` **deve** ser ligado via `orMaxDate(today)` — um `today` zero colapsaria o
  investido consolidado para 0.
- `campaignsP` uuid[]: `NULL` = todas as campanhas; slice vazio = nenhuma. `clientP` uuid:
  `NULL` = todos os clientes. `stationsP` uuid[]: `'{}'` = todas as emissoras.
- Uso: `/campaigns` passa `campaignsP=NULL, clientP=escopo` (todas as campanhas do cliente);
  `/insights` passa `campaignsP=lista, clientP=NULL`.

## Consumidores

- **`Campaigns.FinancialsByCampaign`** ([campaigns.go](../../workers/internal/catalog/campaigns.go)) — agrega `fin_base` por campanha: `Σ plays×pmm`, `Σ plays×pmm_target`, `Σ invested`, `stations_with_target`.
- **`Insights.aggregateCore`** ([insights.go](../../workers/internal/catalog/insights.go)) — soma os KPIs **e** faz o join demográfico (gênero/classe/idade) sobre o **mesmo** `plays×pmm`. `Executado = Σ fin_base.invested`.

## Consequências (aceitas, por design)

1. **Pricing-driven.** `fin_base` parte de `campaign_station_pricing` — emissora sem
   pricing não entra (nem no `/campaigns` nem no `/insights`).
2. **Material sem `type_id`** não entra (a view `daily_play_summary` é chaveada por `type_id`).
3. **Bônus aparece duas vezes no `/insights`** para campanha per_insertion: dentro do
   Investido (base A inclui bônus) **e** no card "Bonificação" (Modelo B, intocado). Os dois
   cards deixaram de ser somáveis — a Bonificação virou métrica informativa isolada. O
   `/campaigns` (referência) não tem esse card. Decisão registrada na spec §8.
4. `plays` conta todos os tipos; `invested` só tipos com pricing (INNER JOIN) — assimetria
   intencional (entrega crua × dinheiro pricing-driven).

## O guard anti-regressão

[`TestFinancialParity_CampaignsVsInsights`](../../workers/internal/catalog/financial_parity_test.go)
roda os **dois** caminhos para o mesmo cenário/janela e falha se divergirem. Cobre três
modos de falha: divergência unilateral (um lado passa a contar `out_slot`), divergência
conjunta (magnitudes absolutas), e campanha ausente (vacuidade). Sempre que mexer em
`financialBaseCTE` ou em qualquer um dos consumidores, este teste tem que continuar verde.

## Follow-ups conhecidos

- `computeCPM` (caminho de `fixed_cpm`, média ponderada) ainda usa a base pré-A por-campanha
  para o **peso** do CPM regular (não afeta impactos/impactos_target/cpm_target/executado, que
  já são base A). Ver `// TODO(base-A):` em `insights.go`.
- Exportáveis (CSV/PDF) ainda têm base própria — fora do escopo desta unificação.
- Arredondamento: `/campaigns` devolve audiência como `float8` (o frontend arredonda);
  `/insights` como `bigint` (arredonda no SQL). Com `pmm` decimal pode haver diferença de
  ±1 na exibição — não afeta a base, é polimento futuro se virar demanda.
