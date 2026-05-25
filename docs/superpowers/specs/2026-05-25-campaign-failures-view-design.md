---
status: planejado
ultima-verificacao: 2026-05-25
codigo-relacionado:
  - workers/internal/catalog/campaign_failures.go (novo)
  - workers/internal/api/handlers/admin_campaign_failures.go (novo)
  - workers/internal/api/router.go
  - workers/cmd/api/main.go
  - frontend/src/api/hooks.js
  - frontend/src/pages/AdminStationFailuresPage.jsx
  - frontend/src/pages/AdminStationFailuresPage.css
  - frontend/src/components/CampaignFailureCard.jsx (novo)
  - frontend/src/components/CampaignFailureRow.jsx (novo)
  - frontend/src/components/CampaignFailureDrawer.jsx (novo)
  - frontend/src/utils/pdfCampaignFailure.js (novo)
---

# Admin — Visão "Por campanha" em `/admin/station-failures`

## Contexto

A página `/admin/station-failures` hoje responde "que emissoras caíram ontem e que campanhas foram afetadas" — visão **station-first**, útil pra ops investigar causa. Falta o complemento: a diretora executiva precisa abrir uma tela e ver **"que campanhas tiveram falha ontem"** pra cobrar as emissoras envolvidas. Cobertura station-first não atende — ela quer pivotar a leitura **por campanha** (1 campanha = 1 card, com lista das emissoras que falharam dentro).

Esse fluxo já é resolvido hoje pelo app externo `Relatório Campanha` ([CLAUDE.md](../../../../../Reports/relatório campanha/CLAUDE.md)), que consome a API da Audiency. O objetivo deste spec é **internalizar** essa visão sem depender de fornecedor externo, espelhando UX e semânticas onde possível, mas usando o nosso `daily_play_summary` como fonte de verdade.

## Escopo

**Inclui:**

1. **Toggle no header** de `/admin/station-failures`: "Por emissora" (atual, intocada) | "Por campanha" (novo).
2. **Sub-modo "Falhas de [data]"** — campanhas com déficit no dia escolhido pelo date picker já existente. Layout: grid de cards (1 campanha por card, emissoras dentro).
3. **Sub-modo "Por Campanha (histórico)"** — tabela compacta de todas as campanhas ativas/programadas/concluídas com falha em qualquer dia da vigência. Ordenada por nº de emissoras com falha. Paginação simples (50 por página).
4. **Drill-in (drawer lateral)** ao clicar num card/linha — mostra o histórico completo de falhas daquela campanha (todas emissoras × dias com falha + chips). Botão **Baixar PDF de cobrança**.
5. **PDF de cobrança por campanha** — A4, foco em falhas (não nos números globais da campanha). Gerado no browser via jsPDF, mesmo design system do `pdfReport.js` atual.

**Não inclui (intencionalmente fora):**

- Substituir o modo "Por emissora" — ele continua intocado.
- PDF agregado do dia (todas as campanhas que falharam ontem). Pode ser follow-up.
- Persistir "compensações" como entidades — `extras` é calculado on-the-fly.
- Envio de PDF por email/webhook/WhatsApp.
- Tocar em viewer/operator — admin-only como o pai.
- Substituir o botão "Relatórios" de `/campaigns` — esse PDF é genérico (prestação de contas pro cliente). O PDF de cobrança serve a outro propósito (cobrar emissora).

## Quem usa

Admin only. Frontend gate via `<RequireRole roles={['admin']}>` (mesma rota — o gate já existe). Backend gate via `auth.RequireRole("admin")` no novo handler.

Premissa: a diretora executiva (e quem mais cobrar emissoras) já tem ou ganhará user com role `admin` no sistema. Sem novo papel/escopo.

## O que conta como "falha"

Uma `(campaign_id, station_id, for_date)` aparece como falha se `daily_play_summary.deficit > 0` nesse trio. Critério único — não usa `stream_health_events` aqui (que é causa-raiz, não impacto). Razão: o modo "Por emissora" já cruza causa e impacto; este modo é puramente sobre **slots prometidos × veiculados**, na perspectiva contratual da campanha.

Campanhas com `status = 'cancelada'` são excluídas. Programadas/ativas/concluídas entram.

## Conceito de "bonificada"

Inspirado no app externo, mas adaptado ao nosso modelo. A view `daily_play_summary` já expõe (vide [migrations/0019_rules_by_type.up.sql](../../../migrations/0019_rules_by_type.up.sql)):

- `expected` — quantos plays a rule pedia
- `in_slot` — quantos rodaram dentro da janela esperada
- `out_slot` — rodou no dia, fora da janela (já mitiga deficit na própria view)
- `out_date` — rodou fora do range da campanha (Bônus na UI atual)
- `bonus` — surplus de `in_slot` acima de `expected` + plays orphan
- `deficit = max(0, expected - in_slot - out_slot)`

Definimos, **por (station, campaign) agregado em TODOS os dias da vigência**:

```
programmed = SUM(expected)
identified = SUM(in_slot)
deficit    = SUM(deficit)                                   -- já mitigado por out_slot
extras     = SUM(out_slot) + SUM(out_date) + SUM(bonus)     -- plays não strict in-slot
is_bonified = (extras >= deficit) AND (extras > 0)
```

Interpretação: se a emissora tocou o spot um número de vezes suficiente em janelas não-canônicas (fora do horário, ou após o range) pra cobrir o déficit, marca-se como "falhou, bonificada" (roxo). Caso contrário, exibe "faltam N" (vermelho).

**Caveat consciente:** em campanha ainda ativa, `is_bonified` é provisório — amanhã pode aparecer mais deficit. O drawer mostra um aviso quando `campaign.status = 'ativa'`: *"Bonificação considera execuções extras até [timestamp]. Pode mudar até o fim da campanha."* Pra `concluida`, sem aviso.

## Endpoint

```
GET /v1/internal/admin/campaign-failures?date=YYYY-MM-DD       # modo dia
GET /v1/internal/admin/campaign-failures?mode=historical       # modo histórico
GET /v1/internal/admin/campaign-failures/{id}                  # drill-in
```

Auth: `admin` em todos. Resposta JSON, 200 OK no caso normal.

### Modo dia

Parâmetros:

| Param | Default | Limites |
|-------|---------|---------|
| `date` | ontem (timezone do servidor) | `today-90d ≤ date ≤ today` (fora → 400) |

Resposta:

```json
{
  "mode": "by_date",
  "date": "2026-05-24",
  "summary": {
    "campaigns": 7,
    "stations": 22,
    "total_deficit": 41
  },
  "campaigns": [
    {
      "campaign": {
        "id": "...", "name": "Campanha XPTO",
        "client_id": "...", "client_name": "ACME",
        "client_logo_url": "...",
        "agency": "Agência YZ",
        "start_date": "2026-05-01", "end_date": "2026-05-31",
        "status": "ativa"
      },
      "stations": [
        {
          "station": { "id": "...", "name": "Rádio X", "dial": "102.7 FM", "city": "São Paulo", "logo_url": "..." },
          "programmed": 30, "identified": 24, "deficit": 6, "extras": 2,
          "is_bonified": false,
          "failure_days_on_date": ["2026-05-24"]
        }
      ]
    }
  ]
}
```

Critério de inclusão: campanha aparece se tem **alguma** `(station, for_date=date)` com `deficit > 0` na view. As `stations[]` dentro são só as que falharam **no dia escolhido**. Mas `programmed/identified/deficit/extras` por station são totais **da campanha inteira**, não só do dia — porque é o que define bonificada.

Ordenação:
- Campanhas: `stations.length DESC, client_name ASC`.
- Stations dentro: `deficit DESC, station.name ASC`.

### Modo histórico

Sem `date`. Lista campanhas com qualquer `deficit > 0` em qualquer dia da vigência. Estrutura mais enxuta — só sumário; detalhe vem do drill-in:

```json
{
  "mode": "historical",
  "summary": { "campaigns": 38, "total_failure_days": 412 },
  "campaigns": [
    {
      "campaign": {
        "id": "...", "name": "...", "client_name": "...",
        "client_logo_url": "...", "agency": "...",
        "start_date": "...", "end_date": "...", "status": "ativa"
      },
      "stations_with_failure": 12,
      "total_failure_days": 28,
      "total_deficit": 76,
      "is_fully_bonified": false
    }
  ]
}
```

Paginação: `?page=N&page_size=50` (default 1, 50). Limite máximo 200 por página. Sem cursor — campanhas não são "stream" infinito.

Ordenação: `stations_with_failure DESC, total_deficit DESC, name ASC`.

`is_fully_bonified` no nível da campanha = todas as stations com falha estão bonificadas. Pode aparecer como sinalização opcional na UI, mas o card/linha não some — a diretora precisa ver mesmo bonificadas pra auditar.

### Drill-in (`/{id}`)

Devolve o detalhe de uma campanha — todas as emissoras que tiveram qualquer falha em qualquer dia da vigência:

```json
{
  "campaign": { /* mesmo shape do modo dia */ },
  "summary": {
    "stations_with_failure": 12,
    "total_failure_days": 28,
    "total_deficit": 76
  },
  "stations": [
    {
      "station": { /* mesmo shape */ },
      "programmed": 90, "identified": 78, "deficit": 12, "extras": 14,
      "is_bonified": true,
      "failure_days": ["2026-05-10", "2026-05-13", "2026-05-24"]
    }
  ]
}
```

404 se a campanha não existe ou está cancelada. Sem filtro por escopo de cliente (admin-only — vê tudo).

Ordenação stations: `failure_days.length DESC, station.name ASC` (espelha o externo).

## Queries SQL

3 queries SQL no modo dia, 2 no histórico, 2 no drill-in. Todas batem na view `daily_play_summary` — sem JOIN com `detections` diretamente.

### Modo dia (Q1 → Q3)

```sql
-- Q1: campanhas com déficit no dia (lista de IDs + dados básicos)
SELECT DISTINCT c.id, c.name, c.start_date, c.end_date, c.status,
       cl.id, cl.name, c.agency
FROM daily_play_summary dps
JOIN campaigns c ON c.id = dps.campaign_id
LEFT JOIN clients cl ON cl.id = c.client_id
WHERE dps.for_date = $1::date
  AND dps.deficit > 0
  AND c.status != 'cancelada'
ORDER BY c.id;

-- Q2: stations que falharam NO DIA escolhido, por campanha (lista)
SELECT dps.campaign_id, dps.station_id,
       s.name, s.band, s.frequency_mhz, s.city, s.logo_url
FROM daily_play_summary dps
JOIN stations s ON s.id = dps.station_id
WHERE dps.for_date = $1::date
  AND dps.deficit > 0
  AND dps.campaign_id = ANY($2::uuid[])
ORDER BY dps.campaign_id, dps.deficit DESC, s.name ASC;

-- Q3: agregação CAMPANHA INTEIRA por (station, campaign) — pra programmed/identified/deficit/extras
SELECT dps.campaign_id, dps.station_id,
       SUM(dps.expected)::int  AS programmed,
       SUM(dps.in_slot)::int   AS identified,
       SUM(dps.deficit)::int   AS deficit,
       (SUM(dps.out_slot) + SUM(dps.out_date) + SUM(dps.bonus))::int AS extras
FROM daily_play_summary dps
WHERE dps.campaign_id = ANY($1::uuid[])
  AND dps.station_id  = ANY($2::uuid[])
GROUP BY dps.campaign_id, dps.station_id;
```

Cruzamento em Go: mapeia Q1 (campanhas), itera stations de Q2, anexa agregados de Q3.

### Modo histórico

```sql
-- Q1: campanhas com qualquer falha + sumário por campanha
SELECT c.id, c.name, c.start_date, c.end_date, c.status, c.agency,
       cl.id, cl.name, cl.logo_url,
       COUNT(DISTINCT dps.station_id) FILTER (WHERE dps.deficit > 0) AS stations_with_failure,
       COUNT(DISTINCT (dps.station_id, dps.for_date)) FILTER (WHERE dps.deficit > 0) AS total_failure_days,
       SUM(dps.deficit)::int AS total_deficit,
       SUM(dps.out_slot + dps.out_date + dps.bonus)::int AS total_extras
FROM daily_play_summary dps
JOIN campaigns c ON c.id = dps.campaign_id
LEFT JOIN clients cl ON cl.id = c.client_id
WHERE c.status != 'cancelada'
GROUP BY c.id, cl.id, cl.name, cl.logo_url
HAVING SUM(dps.deficit) > 0
ORDER BY stations_with_failure DESC, total_deficit DESC, c.name ASC
LIMIT $1 OFFSET $2;

-- Q2: COUNT(*) pra paginação
SELECT COUNT(*) FROM (subquery acima sem LIMIT/OFFSET);
```

`is_fully_bonified` calculado em Go a partir de `total_extras >= total_deficit && total_extras > 0`.

### Drill-in

```sql
-- Q1: dados da campanha
SELECT c.id, c.name, c.start_date, c.end_date, c.status, c.agency,
       cl.id, cl.name, cl.logo_url
FROM campaigns c
LEFT JOIN clients cl ON cl.id = c.client_id
WHERE c.id = $1 AND c.status != 'cancelada';

-- Q2: stations × failure_days agregados na campanha inteira
SELECT dps.station_id,
       s.name, s.band, s.frequency_mhz, s.city, s.logo_url,
       SUM(dps.expected)::int  AS programmed,
       SUM(dps.in_slot)::int   AS identified,
       SUM(dps.deficit)::int   AS deficit,
       (SUM(dps.out_slot) + SUM(dps.out_date) + SUM(dps.bonus))::int AS extras,
       array_agg(DISTINCT dps.for_date::text ORDER BY dps.for_date::text)
         FILTER (WHERE dps.deficit > 0) AS failure_days
FROM daily_play_summary dps
JOIN stations s ON s.id = dps.station_id
WHERE dps.campaign_id = $1
GROUP BY dps.station_id, s.name, s.band, s.frequency_mhz, s.city, s.logo_url
HAVING COUNT(*) FILTER (WHERE dps.deficit > 0) > 0
ORDER BY COUNT(*) FILTER (WHERE dps.deficit > 0) DESC, s.name ASC;
```

## Performance esperada

A view `daily_play_summary` é uma view não materializada, calculada on-the-fly. Em campanhas com 50 emissoras × 30 dias × 5 materiais, são ~7500 linhas — trivial. Para histórico com 100+ campanhas ativas no sistema, talvez ~750k linhas no agregado — ainda razoável (PostgreSQL com índices em `detections.campaign_id`, `detections.station_id`, `detections.detected_at` materializa bem).

Sem cache. Polling não — admin investiga sob demanda. `staleTime: 60s` no React Query.

Se virar gargalo: materializar a view (refresh a cada 5min via cron, igual o lifecycle scheduler) ou criar tabela de snapshot histórico. Fora de escopo agora.

## Frontend

### `AdminStationFailuresPage.jsx` — mudanças

Adicionar **header toggle** entre o date picker e o conteúdo:

```
[Por emissora] [Por campanha]    [date picker] [min_down filter quando 'Por emissora']
```

State local (`viewMode = 'by_station' | 'by_campaign'`). Sem mudança de URL pra manter shareability simples; o usuário sempre cai em "Por emissora" ao abrir (default existente).

Em "Por campanha", aparecem **dois sub-tabs** dentro do conteúdo:

```
[Falhas de DD/MM]    [Por Campanha (histórico)]
```

Estes sim podem refletir no URL via search params (`?view=campaigns&sub=daily` / `?sub=historical`), porque o histórico pode ser longo e a diretora pode querer compartilhar uma página específica.

### Componentes novos (`frontend/src/components/`)

- **`CampaignFailureCard.jsx`** — card do grid "Falhas de [data]":
  - Header: logo do cliente (com fallback de iniciais), `client_name` (bold), `campaign.name` (subdued), `agency` (subdued menor).
  - Body: lista de stations que falharam no dia (até 6 visíveis, "+N mais" se >6).
    - Cada row: logo da station + `name` + `city` à esquerda; `identified/programmed · %` à direita; "faltam N" (vermelho) ou "falhou, bonificada" (roxo).
  - Footer: contagem `N emissoras` + botão "Ver detalhes" (abre drawer).
  - Click no card todo → abre drawer.

- **`CampaignFailureRow.jsx`** — linha da tabela "Por Campanha (histórico)":
  - Colunas: Cliente · Campanha (multi-line truncado), Status (chip), N emissoras com falha (number bold), N dias totais, Déficit total, [bonificada-chip se aplicável].
  - Click na linha → abre drawer.

- **`CampaignFailureDrawer.jsx`** — drawer lateral, seguindo o mesmo padrão visual e de close-on-Escape do `HealthDrawer.jsx` ([frontend/src/components/HealthDrawer.jsx](../../../frontend/src/components/HealthDrawer.jsx)); componente novo (sem reuso direto porque o conteúdo difere bastante):
  - Header sticky: logo cliente + nome campanha + período + status chip + botão "✕" e botão "Baixar PDF de cobrança".
  - KPIs: 3 cards (Emissoras com falha · Dias totais · Déficit total).
  - Aviso (só se `campaign.status === 'ativa'`): banner amarelo discreto: *"Campanha ainda ativa — bonificação calculada até [agora]."*
  - Tabela: `Emissora | Programado | Veiculou (%) | Dias com falha (chips DD)`. Cada row: logo station + nome + cidade à esquerda; números numérica à direita; chips dos dias compactos (formato `24` ou `24/05` se cruzar mês).

### Hooks novos (`frontend/src/api/hooks.js`)

```js
useCampaignFailures({ mode, date, page, pageSize })
  // mode === 'by_date'   -> GET /admin/campaign-failures?date=...
  // mode === 'historical'-> GET /admin/campaign-failures?mode=historical&page=...&page_size=...

useCampaignFailureDetail(id)
  // GET /admin/campaign-failures/{id}
```

React Query, `staleTime: 60_000`. Sem polling. Cache key inclui `mode + date + page`.

### PDF de cobrança (`frontend/src/utils/pdfCampaignFailure.js`)

Builder novo, espelhando estilo de `pdfReport.js`:

1. **Header** — logo E-monitor (pré-carregado), barra rosa-action, título "Relatório de Cobrança", subtitle com cliente + nome da campanha + período.
2. **KPIs** — 3 cards (Emissoras com falha · Dias com falha · Déficit total).
3. **Tabela principal**: `Emissora | Cidade | Programado | Veiculou | Dias com falha`. Linhas com `is_bonified` ganham linha extra com badge "Bonificada — extras: N" embaixo do nome.
4. **Footer**: `Gerado por E-monitor · DD/MM/YYYY HH:MM` à esquerda, paginação à direita.

Filename: `cobranca-{slug(campaign.name)}-{YYYYMMDD}.pdf`.

Sem footer/disclaimer obrigando assinatura — é um relatório operacional, não fiscal.

## Edge cases mapeados

| Caso | Comportamento |
|------|---------------|
| `date` no futuro | 400 `invalid date: future not supported` |
| `date` < hoje-90d | 400 `invalid date: older than 90 days` |
| `date` malformado | 400 `invalid date: expected YYYY-MM-DD` |
| `mode` inválido | 400 |
| Nenhuma campanha falhou no dia | Empty state estilo `/admin/station-failures` (cheery, ícone check) |
| Histórico vazio (sem campanhas com falha) | Empty state diferente: *"Nenhuma campanha tem falha registrada"* |
| Campanha deletada após o drill-in cachear | 404 → drawer mostra "campanha não encontrada", botão fechar |
| Logo de cliente ausente | Fallback de iniciais coloridas (igual `StationAvatar`) |
| `start_date`/`end_date` ausentes (campanha programada sem datas?) | Mostra "Período não definido"; o resto do drawer funciona |
| Campanha com status `cancelada` | Filtrada em todas as queries |
| Campanha 100% bonificada no histórico | Aparece, mas com badge "bonificada" — diretora vê e ignora |
| `is_bonified` muda entre o card e o drawer (race com novo detection) | Tolerável; staleTime de 60s dá margem. Drawer re-fetcha, valor atual ganha |
| Drawer em campanha sem failure_days (raça com cache) | Mostra "sem falhas registradas neste momento", botão fechar |
| Paginação histórica: page além do total | Backend devolve `campaigns: []` e `summary.campaigns: N` correto. Frontend mostra "página vazia, voltar pra início" |

## Plano de implementação resumido

1. **Backend**:
   - `workers/internal/catalog/campaign_failures.go` com 3 métodos: `ListForDate(ctx, date)`, `ListHistorical(ctx, page, pageSize)`, `Get(ctx, id)`.
   - `workers/internal/api/handlers/admin_campaign_failures.go` — 3 handlers fininhos, validação de params, escreve JSON.
   - `workers/internal/api/router.go` — 3 rotas dentro do `RequireRole("admin")` grupo.
   - `workers/cmd/api/main.go` — wiring do repo.
   - Testes: unit em `campaign_failures_test.go` para `IsBonified()` helper; integration test do handler em `admin_campaign_failures_test.go` cobrindo modo dia, histórico, drill-in, 400s, 404, paginação.

2. **Frontend**:
   - `AdminStationFailuresPage.jsx` — adiciona toggle "Por emissora/Por campanha" + sub-tabs no modo campanha.
   - 3 componentes novos: `CampaignFailureCard`, `CampaignFailureRow`, `CampaignFailureDrawer`.
   - `hooks.js` — `useCampaignFailures` e `useCampaignFailureDetail`.
   - `utils/pdfCampaignFailure.js` — builder do PDF.
   - CSS — extends de `AdminStationFailuresPage.css` ou novo arquivo dedicado se ficar grande.

3. **Docs**:
   - Criar `docs/features/admin-campaign-failures.md` (status `implementado`, mesma estrutura de outros features).
   - Atualizar `docs/features/admin-station-failures.md` na seção "Não cobre" pra remover "perspectiva por campanha" (que agora cobrimos).
   - Atualizar o índice `docs/README.md` com link novo.
   - Atualizar `CLAUDE.md` na tabela "Mapa de consulta — quando trabalhar em X" pra incluir esta tela.

4. **Sem migration** — usa view existente.

5. **Sem mudança em viewer/operator** — admin-only.

## Critério de aceitação

- [ ] Toggle "Por emissora / Por campanha" funciona, sem perder estado ao navegar entre eles.
- [ ] Modo dia: campanha que tem `deficit > 0` em alguma station no dia aparece; bonificada renderiza em roxo; deficit cru em vermelho.
- [ ] Modo histórico: paginação 50/página funciona; ordenação default `stations_with_failure DESC`.
- [ ] Drill-in: drawer abre, mostra todas stations com falha, chips dos dias batem com `failure_days` da response.
- [ ] PDF de cobrança baixa, abre em qualquer reader, contém logo + cliente + tabela + chips dos dias.
- [ ] Admin only — viewer/operator recebem 403/redirecionam.
- [ ] Cancelled campaign não aparece em nenhum dos 3 endpoints.
- [ ] Edge cases acima cobertos (testar pelo menos: date inválida, drawer 404, paginação além do total).
- [ ] Documentação `docs/features/admin-campaign-failures.md` criada com `status: implementado` e codigo-relacionado preenchido.
