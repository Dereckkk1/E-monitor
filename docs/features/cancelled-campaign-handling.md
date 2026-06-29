---
status: implementado
ultima-verificacao: 2026-06-29
codigo-relacionado:
  - workers/internal/catalog/station_failures.go
  - workers/internal/catalog/live_map.go
  - workers/internal/catalog/management_overview.go
  - workers/internal/catalog/insights.go
  - workers/internal/catalog/daily_summary.go
  - workers/internal/catalog/campaigns.go
  - migrations/0044_campaign_cancelled_at.up.sql
  - frontend/src/pages/DetectionsPage.jsx
  - frontend/src/pages/MaterialsPage.jsx
  - frontend/src/pages/AirtimeReportPage.jsx
  - frontend/src/components/AirtimeFiltersBar.jsx
  - frontend/src/components/insights/FiltersBar.jsx
  - frontend/src/pages/LiveMapPage.jsx
  - frontend/src/pages/DashboardPage.jsx
---

# Tratamento de campanhas canceladas (exclusão de superfícies operacionais)

Campanha `cancelada` é terminal (ver [campaign-lifecycle.md](../architecture/campaign-lifecycle.md)):
foi encerrada manualmente antes do `end_date`. Antes desta entrega, o estado
`cancelada` vazava em vários lugares onde deveria ser tratado como morto —
seletores de campanha, telas "ao vivo", contadores e até **cobrança de slots
perdidos**. Esta doc descreve a política aplicada e onde ela age.

## Política

Decisão de produto (2026-06-29): **"manter e marcar"**.

- **Seletores e telas operacionais / "ao vivo":** campanha cancelada **sai**.
  Não é oferecida em dropdowns de nova seleção, não pulsa no mapa ao vivo, não
  entra em KPIs de operação corrente, não gera cobrança de inserções perdidas.
- **Telas históricas / relatórios:** as veiculações que ocorreram **antes** do
  cancelamento **continuam contando** (aconteceram de verdade), mas a campanha
  aparece **marcada como cancelada**, e o programado/déficit **congela na data
  do cancelamento** (dias após não geram obrigação).
- **Admin de gestão de campanhas (`/campaigns`):** continua mostrando canceladas
  com badge vermelho — é a tela de gestão, mostrar é o esperado.
- **Deep-link histórico** (`?campaign_id=` de uma cancelada em /detections,
  /materials, /reports/airtime): **continua abrindo** — cancelada ≠ deletada.
  O exemplo de exceção é `/live-map`, que é "ao vivo" e responde 404.

`concluida` (campanha que rodou normalmente até o fim) **não** é afetada por
esta política — só `cancelada`. A única tela onde `concluida` poderia também
ser excluída (mapa "ao vivo") foi deixada como está por decisão explícita.

## O que mudou, por superfície

### Backend (Go)

| Local | Antes | Agora |
| ----- | ----- | ----- |
| `station_failures.go` (ListForDate — `/admin/station-failures` "por data", modal de resumo diário, PDF de cobrança) | Contava cancelada como "slots perdidos" → cobrança de campanha cancelada | `c.status <> 'cancelada'` no CTE `deficit_aggr` (com join em campaigns) e na query 3. Espelha `campaign_failures.go`. |
| `live_map.go` (`Get`) | Campanha cancelada abria normal (emissoras pulsando + feed "ao vivo") | Resolve `status` junto do `client_id`; `cancelada` → `ErrCampaignNotFound` (404). `concluida` segue acessível. |
| `management_overview.go` (`mgmtScopedCTE` + feed) | Sem filtro de status no default → canceladas entravam em KPIs/mapa/feed | `AND ($3 <> '' OR status <> 'cancelada')`: no escopo default canceladas saem; filtro explícito `status=cancelada` ainda funciona para inspeção. |
| `insights.go` (`CampaignBrief` / `fetchCampaigns`) | Brief não trazia status | Expõe `status` para o frontend marcar a campanha no relatório. Contagem histórica **mantida** (decisão "manter"). |
| `daily_summary.go` (`ListByCampaign`) | Grid mostrava programado/déficit para dias **após** o cancelamento | Congela: join em campaigns + `for_date <= cancelled_at` para canceladas. |
| `campaigns.go` (`CancelCampaign`) | `status='cancelada', updated_at=now()` | `+ cancelled_at = now()` |

### Migration `0044_campaign_cancelled_at`

Coluna **aditiva e nullable** `campaigns.cancelled_at timestamptz`. Backfill
best-effort das já canceladas a partir de `updated_at` (instante do cancelamento;
`cancelada` é terminal então raramente foi tocado depois). Usada só para congelar
o grid histórico — nunca afeta cobrança (canceladas já saem das telas de falha).

### Frontend (React)

| Local | Mudança |
| ----- | ------- |
| DetectionsPage / MaterialsPage / AirtimeFiltersBar | Cancelada fora do dropdown e do contador "vigentes"; **resolvível via `allCampaignOptions`** para o deep-link histórico não quebrar; valor selecionado de uma cancelada mostra sufixo "(cancelada)". |
| AirtimeReportPage | `campaignsInCompetence` (contador) exclui cancelada. |
| insights/FiltersBar | Multi-select não oferece cancelada para nova seleção, mas mantém visível+contando se já selecionada (deep-link/estado), rotulada "(cancelada)" — concilia Grupo 1 (não oferecer) + "manter e marcar". |
| LiveMapPage | Cancelada fora do seletor de mapa ao vivo (backend também 404). |
| DashboardPage (cliente) | "Concluídas recentes" só `concluida`; "Campanhas no total", barra de distribuição e guards de vazio excluem cancelada. |
| pdfReport.js | Já marcava status na capa (incl. "Cancelada") — sem mudança. |

## O que **já estava** correto (não mexido)

Índice de matching (`index/loader.go` — `('programada','ativa')`), workers /
supervisor / reconciler (rodam só para `ativa`), e-mails de alerta
(`campaignalerts`), sino de notificações admin (`notifications.go`),
`/admin/campaign-failures` (`campaign_failures.go`), webhooks, calibração,
wizard de campanha. Detalhe relevante: cancelar uma campanha **em execução** não
despeja na hora os hashes dela do índice em memória, mas isso é **inofensivo** —
o worker só alimenta as state machines dos `CommercialShortIDs` desejados
(vindos de `ActiveCampaignsForStation`, que filtra `status='ativa'`), então um
match contra resíduo no índice global é ignorado; o reconciler remove o short_id
em ≤30s.

## Como verificar

- **station-failures:** cancelar uma campanha com déficit no dia → não deve mais
  aparecer em `/admin/station-failures` nem na cobrança (igual a
  `/admin/campaign-failures`, que já a escondia).
- **live-map:** abrir `/live-map?campaign_id=<cancelada>` → 404 (anti-oracle).
- **management:** sem filtro de status, KPIs/mapa/feed não contam canceladas;
  filtrar `status=cancelada` explicitamente ainda as mostra.
- **seletores:** dropdowns de /detections, /materials, /reports/airtime,
  /insights, /live-map não listam canceladas; deep-link a uma cancelada
  (exceto live-map) ainda abre, marcada "(cancelada)".
- **freeze:** grid daily-summary de uma cancelada não mostra déficit em dias
  após `cancelled_at`.
