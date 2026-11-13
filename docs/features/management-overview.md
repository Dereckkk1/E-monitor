---
status: implementado
ultima-verificacao: 2026-10-28
codigo-relacionado:
  - workers/internal/catalog/management_overview.go
  - workers/internal/api/handlers/management_overview.go
  - workers/internal/api/router.go
  - frontend/src/pages/ManagementPage.jsx
  - frontend/src/pages/ManagementPage.css
  - frontend/src/components/LiveAiringRow.jsx
  - frontend/src/api/hooks.js
  - frontend/src/utils/filtrosDaUrl.js
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

### Tela cheia

Botão **Tela cheia** no header (canto direito, ao lado do indicador "ao vivo").
Liga um estado local `fullscreen` que: (1) aplica a classe `mg-fs` no `<body>`,
escondendo a sidebar global (`.app-sidebar`), a topbar mobile e zerando o padding
do `.app-content`; (2) reorganiza o `.mg-grid` num grid de 2 colunas
(mapa+KPIs | feed), ocupando `100svh` pra mostrar tudo numa tela só, com o feed
rolando internamente. **Esc** sai. É puramente visual — não altera filtros nem
dados. CSS em `ManagementPage.css` (`body.mg-fs`, `.mg-page--fs`).

## KPIs

- **Emissoras monitoradas:** emissoras-alvo distintas das campanhas no recorte.
- **Monitorando agora:** emissoras monitoradas que têm **worker ativo agora** no
  supervisor (`Supervisor.WorkerStatuses()`, `Active=true`) — **mesma fonte da
  `/operations`**. Calculado no handler cruzando `MonitoredStationIDs` (do repo)
  com o snapshot de workers. **Não** usa `stations.health_status` (coluna não
  populada pelo sistema — ver Limitações).
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

## Abrir já filtrado pela URL

Desde 2026-10-28 (MCP Fatia 2), `/management` lê no primeiro render os filtros que `visao_gerencial` e
`assertividade` põem em `links.tela`: `client_id`, `campaigns` (csv), `status`
(`ativa|programada|concluida|cancelada`, os valores do seletor) e `from`/`to` (os dois, `from <= to`;
senão vale o padrão 01/01 até hoje). Validação em `frontend/src/utils/filtrosDaUrl.js`; depois a tela
limpa a URL e segue com o próprio estado.

Campanha que não está na página 1/200 do seletor (uma concluída antiga, por exemplo) é buscada à parte
(`useCampaignsWithSelected` em `api/hooks.js`, via `/campaigns?id=`, no escopo de quem está logado) e entra
nas opções — antes, a tela filtrava por ela com o seletor vazio.

Limitações: a URL é limpa logo no primeiro render, antes de qualquer resposta da API — numa aba com sessão
vencida, o 401 leva ao login e o filtro do link se perde (abra o link de novo depois de entrar). Valor
vazio ou malformado é ignorado em silêncio (vira "sem filtro"); os ids saem em minúsculas.

## Limitações conhecidas

- A coluna `stations.health_status` **não é populada** pelo sistema (só
  `last_health_check` é escrito, pelo supervisor). Por isso "monitorando agora"
  é calculado pelo snapshot de workers do supervisor, não pela coluna. O **pulso
  do Mapa ao Vivo** (`/live-map`) ainda lê `health_status='ok'` e portanto não
  acende — corrigir isso (popular a coluna ou trocar a fonte no mapa) é um
  follow-up separado.

## Performance

É a consulta mais pesada do sistema. V1 = queries diretas sobre os índices
existentes. Medir antes de otimizar; se necessário, cache curto ou tabela de
agregação (não pré-otimizado).
