---
status: implementado
ultima-verificacao: 2026-08-18
codigo-relacionado:
  - frontend/src/pages/AirtimeReportPage.jsx
  - frontend/src/components/AirtimeFiltersBar.jsx
  - frontend/src/components/AirtimeDetectionRow.jsx
  - frontend/src/components/AirtimeMaterialPanel.jsx
  - frontend/src/components/CampaignReportsMenu.jsx
  - frontend/src/utils/campaignRange.js
  - workers/internal/api/handlers/detections.go
  - workers/internal/catalog/detections.go
---

# Relatório Data e Hora (`/reports/airtime`)

Lista cronológica de veiculações — uma linha por tocada, com data/hora,
emissora (logo, dial, praça), PMM, PMM no target, custo por inserção, material
e player do áudio de evidência. À direita, o painel "Total por áudio" resume
quantas vezes cada material tocou no recorte.

É a tela de conferência linha a linha; quem quer a grade programado × tocado
usa [`/detections`](detections-view.md), e quem quer KPIs usa
[`/insights`](insights-dashboard.md).

## Fluxo de filtros — 4 passos

`Cliente → Competência → Campanhas → Período`

| Passo | O que faz |
|-------|-----------|
| 1. Cliente | Primeiro recorte, mesma ordem de `/insights` e `/live-map`. Admin escolhe no select; viewer/agência de 1 cliente vê um chip travado (`.flow-locked-chip`) e não gasta clique. Trocar o cliente zera campanhas e período — a competência sobrevive. |
| 2. Competência | Mês de referência. Filtra o passo 3: só campanhas cuja vigência cruza o mês aparecem. |
| 3. Campanhas | **Seleção múltipla** (`RSelect isMulti`). Canceladas ficam fora do seletor ([regra](cancelled-campaign-handling.md)), mas seguem resolvíveis por deep-link. |
| 4. Período | Nasce na interseção mês ∩ união das vigências; os presets esticam. |

O passo 1 é o que garante que a seleção múltipla **nunca mistura clientes** —
a lista, o painel de materiais e o rótulo de público-alvo (`target_label`)
assumem um cliente só.

### Presets de período com N campanhas

"A vigência" de uma seleção múltipla é a **união** (`min(start_date)`,
`max(end_date)`) — o único intervalo que cobre todas sem esconder tocada de
nenhuma. Vive em [`campaignsUnionRange`](../../frontend/src/utils/campaignRange.js)
e alimenta tanto o preset "Vigência no mês" (mês ∩ união) quanto
"Campanhas inteiras" (união limitada a hoje).

## Estado na URL

`client_id`, `competence`, `campaigns` (CSV de uuids), `from`, `to`, `q`, `page`.

Deep-link antigo com `campaign_id=<uuid>` continua abrindo: vira seleção de 1 e
o cliente é resolvido pela campanha. No primeiro clique de filtro o param
legado é apagado da URL, pra não sobrar duas fontes de verdade.

## Backend

Duas rotas alimentam a tela, e **as duas aceitam `campaigns` (CSV) ou o
`campaign_id` legado** — o parsing é compartilhado em
`handlers.parseCampaignIDs`:

| Rota | Repo | Observação |
|------|------|-----------|
| `GET /detections?page=N` | `catalog.Detections.ListPaged` | `ListPagedFilter.CampaignIDs []uuid.UUID`; WHERE usa `d.campaign_id = ANY($1)`. Devolve `campaign_name` por linha. |
| `GET /detections/aggregate-by-material` | `catalog.Detections.AggregateByMaterial` | `AggregateFilter.CampaignIDs`; exige ao menos 1 campanha. |

**`nil` ≠ slice vazio** nos dois filtros: `nil` significa "sem recorte por
campanha" (vira `NULL` e o `OR` do WHERE passa reto); um slice vazio vira `{}`
e casa zero linhas, que é a leitura correta de "seleção vazia".

### Escopo do viewer

O agregado antes checava a posse da campanha no handler (`CampaignRepo.Get` +
`ScopeAllows`) — com N campanhas isso seria um round-trip por campanha. Agora a
carteira entra como filtro SQL (`cmp.client_id = ANY($5)`), o mesmo que o
`ListPaged` sempre aplicou. Consequência: campanha fora da carteira **não dá
404, simplesmente não soma** — igual a lista já se comportava.

## A linha da veiculação

A sublinha do material mostra o **nome da campanha** (com `title` =
"Cliente · Campanha"), espelhando o feed de `/management`. Antes mostrava o
cliente, que ficou redundante depois do passo 1.

O custo por inserção depende do pricing, que é por **(campanha, emissora)**: o
mapa é indexado por `` `${campaign_id}|${station_id}` `` via
`useCampaignPricingByCampaignStation`. Indexar só por emissora mostraria o
preço da campanha errada quando duas campanhas do mesmo cliente contratam a
mesma rádio com valores diferentes.

## Painel "Total por áudio"

Agrupa **por material**, somando as campanhas selecionadas. Com mais de uma
campanha o cabeçalho marca "· N campanhas" pra que o total não seja lido como
de uma campanha só. Escolha deliberada: quebrar por (material × campanha) foi
avaliado e descartado.

## Relatórios (CSV/PDF)

Relatório é **por campanha** — o backend (`/campaigns/{id}/report/*`) não
combina campanhas. Com 2+ selecionadas, o dropdown do
[`CampaignReportsMenu`](campaign-reports.md) ganha um seletor de campanha
**acima** do seletor de período que ele já tinha (prop `campaignOptions`). Com
0 ou 1 campanha o menu é exatamente o de antes.

Se o usuário remove da tela a campanha escolhida dentro do menu, o menu cai de
volta pra `campaignId` — nunca gera relatório de campanha que saiu da seleção.

## Decisões registradas

- **Sem mistura de clientes.** A alternativa (livre, com "Cliente · Campanha"
  em cada linha) foi descartada em favor do passo de cliente.
- **Sem relatório combinado.** Mudaria o layout do CSV/PDF que o cliente já
  conhece; o custo não se paga.

Spec do desenho:
[`docs/superpowers/specs/2026-08-18-airtime-multi-campaign-design.md`](../superpowers/specs/2026-08-18-airtime-multi-campaign-design.md).
