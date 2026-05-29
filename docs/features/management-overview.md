---
status: implementado
ultima-verificacao: 2026-05-29
codigo-relacionado:
  - workers/internal/catalog/management_overview.go
  - workers/internal/api/handlers/management_overview.go
  - workers/internal/api/router.go
  - frontend/src/pages/ManagementPage.jsx
  - frontend/src/pages/ManagementPage.css
  - frontend/src/components/LiveAiringRow.jsx
  - frontend/src/api/hooks.js
---

# Visão Gerencial (/management)

Painel **admin/operator-only** com uma visão da operação inteira da plataforma —
todas as campanhas, de todos os clientes. Diferente do `/live-map` (que exige
Cliente → Campanha antes de mostrar algo), abre **já trazendo tudo** e oferece
filtros opcionais.

## Acesso

Admin/operator. Rota `/management` gateada por `RequireRole roles={['admin']}` no
frontend; endpoint registrado no subgrupo admin/operator do router (sem scope de
viewer). Não aparece no `ClientNav`.

## Layout

Filtros no topo + split de duas colunas: KPIs empilhados à esquerda; mapa do
Brasil (pulsando) + feed global ao vivo à direita. Reusa `BrazilMap`,
`RSelect` e a linha de feed compartilhada (`components/LiveAiringRow.jsx`,
extraída do `/live-map`).

## KPIs

- **Emissoras monitoradas:** emissoras-alvo distintas das campanhas no recorte.
- **Monitorando agora:** subconjunto com `health_status='ok'` neste instante
  (sempre tempo real).
- **Materiais monitorados:** materiais distintos vinculados (`campaign_materials`).
- **Veiculações no período:** detecções confirmadas no período (+ "hoje").

## Escopo vs. período

Os filtros (cliente/campanhas/status + sobreposição de período) definem **quais
campanhas** entram. Sobre elas, o **mapa** e o **feed** são sempre "agora"; o
**período** só limita `airings_total`. Default de período = ano corrente.

## Fonte de dados

`GET /v1/internal/management-overview` com params opcionais `client_id`,
`campaigns` (csv), `status`, `from`, `to`. Repo `catalog.ManagementOverview`
roda 3 queries (KPIs, stations, recent detections) sobre uma CTE `scoped`
comum. Frontend: `useManagementOverview` (react-query, refetch 20s,
`placeholderData`).

## Performance

É a consulta mais pesada do sistema. V1 = queries diretas sobre os índices
existentes. Medir antes de otimizar; se necessário, cache curto ou tabela de
agregação (não pré-otimizado).
