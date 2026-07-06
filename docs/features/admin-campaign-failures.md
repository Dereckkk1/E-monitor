---
status: implementado
ultima-verificacao: 2026-07-06
codigo-relacionado:
  - workers/internal/catalog/campaign_failures.go
  - workers/internal/catalog/campaign_failures_test.go
  - workers/internal/api/handlers/admin_campaign_failures.go
  - workers/internal/api/handlers/admin_campaign_failures_test.go
  - workers/internal/api/router.go
  - workers/cmd/api/main.go
  - frontend/src/pages/AdminStationFailuresPage.jsx
  - frontend/src/pages/AdminStationFailuresPage.css
  - frontend/src/components/CampaignFailureCard.jsx
  - frontend/src/components/CampaignFailureRow.jsx
  - frontend/src/components/CampaignFailureDrawer.jsx
  - frontend/src/components/CampaignFailureCard.css
  - frontend/src/utils/pdfCampaignFailure.js
  - frontend/src/api/hooks.js
---

# Admin → Falhas "Por campanha" (`/admin/station-failures` modo `Por campanha`)

Visão complementar de [admin-station-failures.md](admin-station-failures.md): mesma rota, mesmo gate de admin, mas pivota a leitura **por campanha** ao invés de por emissora. Internaliza a UX do app externo `Relatório Campanha`.

## Por que existe

A diretora executiva precisa de uma visão diária de **quais campanhas tiveram falha pra cobrar as emissoras envolvidas**. A visão station-first do `/admin/station-failures` (atual) responde a outra pergunta — *quais emissoras caíram e o que isso arrastou*. As duas convivem como dois modos do mesmo painel.

## O que mostra

| Sub-modo | Quando usar | Layout |
|----------|-------------|--------|
| **Falhas de [data]** | Cobrança diária ("o que falhou ontem") | Grid de cards, um por campanha, com emissoras dentro |
| **Por Campanha (histórico)** | Backlog ("o que ainda precisa cobrança") | Tabela paginada, todas campanhas com qualquer falha |

Click em qualquer card/linha → drawer lateral com **todas** as emissoras da campanha que falharam em **qualquer dia já encerrado** da vigência (ver [Horizonte de falha](#horizonte-de-falha-só-dias-já-encerrados)), com chips de dias específicos. Dentro do drawer, botão **"Baixar PDF de cobrança"**.

## Quem pode ver

Admin only. Rota frontend gated via `<RequireRole roles={['admin']}>` (a rota é `/admin/station-failures`, não muda). Endpoints backend gated via `auth.RequireRole("admin")` no router.

## "Bonificada"

Uma emissora é marcada como **bonificada** (roxo) quando o total de execuções extras na campanha cobre o déficit total. Definição:

```
extras  = SUM(out_slot + out_date + bonus)   -- na campanha inteira
deficit = SUM(deficit)                        -- na campanha inteira, da view daily_play_summary
bonified = (extras >= deficit) AND extras > 0 AND deficit > 0
```

A view `daily_play_summary` já expõe todas as colunas necessárias — esta feature não toca em `detections` direto.

**Caveat:** em campanha ainda ativa, `bonified` é provisória — amanhã pode aparecer mais déficit. O drawer mostra banner amarelo discreto avisando.

## Horizonte de falha (só dias já encerrados)

`daily_play_summary` **não é materializada**: ela emite uma linha de déficit pra **cada dia agendado até o `end_date` da regra** (`CROSS JOIN generate_series(start_date, end_date)` na migration 0041). Um dia futuro tem `expected>0` e `in_slot=0` → `deficit=expected` — mas **um dia que ainda não chegou não é falha**.

Por isso toda agregação de falha em `campaign_failures.go` corta em `dps.for_date < (now() AT TIME ZONE 'America/Sao_Paulo')::date` (const `failureHorizonClause`). Vale pras 3 superfícies que somam vários dias:

- **drawer** (`Get`) — `failure_days`, déficit e "dias com falha";
- **histórico** (`ListHistorical` — Q1 e Q2 espelhadas);
- **agregado do card diário** (`ListForDate` Q3 — programado/déficit/extras/bonificada).

As consultas single-day (`ListForDate` Q1/Q2 e o modo *Por emissora* em `station_failures.go`) não precisam do corte: a data já é limitada a ≤ hoje no handler. Regra do produto: **"de ontem pra trás"** — a mesma que o sininho/digest (`notifications.go`) já aplicava com `< CURRENT_DATE`.

> Bug histórico (2026-07-06): sem esse corte, o drawer contava amanhã e todo o resto da vigência como dia com falha (ex.: campanha "aa" mostrava 90 dias/déficit 271 quando o real encerrado era 11 dias/déficit 34). Regressão travada em `TestCampaignFailures_Get_ExcludesFutureDays`.

## Endpoints

```
GET /v1/internal/admin/campaign-failures?date=YYYY-MM-DD       # modo dia (default)
GET /v1/internal/admin/campaign-failures?mode=historical&page=N&page_size=50
GET /v1/internal/admin/campaign-failures/{id}                  # drill-in
```

Auth: admin. Limites de `date`: today-90d a today (fora → 400). Combinar `mode=historical` com `date` → 400. Drill-in com campanha cancelada → 404.

Detalhes de response no spec [2026-05-25-campaign-failures-view-design.md](../superpowers/specs/2026-05-25-campaign-failures-view-design.md).

## Performance

3 queries SQL no modo dia (Q1 campanhas + Q2 stations do dia + Q3 agregados da campanha). 2 queries no histórico (lista paginada + count). 2 no drill-in. Sem cache (staleTime React Query 60s). Sem polling. Se virar gargalo, materializar a view `daily_play_summary` em snapshot diário.

## PDF de cobrança

Gerado no browser via jsPDF + jspdf-autotable em `frontend/src/utils/pdfCampaignFailure.js`. Layout: header E-monitor + cliente + período → 3 KPIs (Emissoras com falha · Dias · Déficit) → tabela `Emissora | Programado | Veiculou | Dias com falha | Status`. Bonificada marcada em roxo. Footer com `Gerado por E-monitor · DD/MM/YYYY HH:MM` + paginação.

Filename: `cobranca-{campanha-slug}-{YYYYMMDD}.pdf`.

Distinguir do PDF gerado pelo botão "Relatórios" em `/campaigns` (ver [campaign-reports.md](campaign-reports.md)): aquele é prestação de contas pro cliente, este é cobrança pra emissora.

## Edge cases mapeados

| Caso | Comportamento |
|------|---------------|
| `date` no futuro | 400 |
| `date` > 90d | 400 |
| `mode=historical` + `date` | 400 |
| Drill-in com campanha cancelada / inexistente | 404 → drawer mostra "campanha não encontrada" |
| Sem falhas no dia | Estado vazio `Nenhuma campanha falhou nesse dia` |
| Histórico vazio | Estado vazio `Nenhuma campanha tem falha registrada` |
| Logo cliente ausente | Fallback de inicial colorida (rosa) |
| Paginação além do total | `campaigns: []` no response, frontend mostra empty |
| Campanha 100% bonificada no histórico | Aparece com chip "100% bonificada" — não some |

## Não cobre (escopo intencionalmente fora)

- PDF agregado do dia (todas as campanhas em um só PDF)
- Envio do PDF por email/webhook
- Modelar "compensações" como entidades persistidas — `extras` é on-the-fly
- Auto-refresh em background — admin investiga sob demanda

## Spec arquitetural

[../superpowers/specs/2026-05-25-campaign-failures-view-design.md](../superpowers/specs/2026-05-25-campaign-failures-view-design.md)
