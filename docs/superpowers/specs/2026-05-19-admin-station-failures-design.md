---
status: implementado
ultima-verificacao: 2026-05-19
codigo-relacionado:
  - workers/internal/api/handlers/admin_station_failures.go (novo)
  - workers/internal/catalog/station_failures.go (novo)
  - workers/internal/api/router.go
  - workers/internal/catalog/health_events.go
  - workers/internal/catalog/distribution_rules.go
  - workers/internal/catalog/daily_summary.go
  - workers/internal/api/handlers/campaigns.go
  - migrations/0017_distribution_plan.up.sql
  - frontend/src/pages/AdminStationFailuresPage.jsx (novo)
  - frontend/src/pages/AdminStationFailuresPage.css (novo)
  - frontend/src/components/StationFailureCard.jsx (novo)
  - frontend/src/components/CampaignDeficitChip.jsx (novo)
  - frontend/src/api/hooks.js
  - frontend/src/components/Sidebar.jsx
  - frontend/src/App.jsx
  - frontend/src/pages/CampaignsPage.jsx
---

# Admin — Emissoras com falha (`/admin/station-failures`)

## Contexto

O admin do Radiocheck hoje tem três telas pra investigar problemas operacionais:

- `/admin/overview` — pulso AGORA da infra/workers/streams (tempo real, sem histórico)
- `/admin/monitoring` — tráfego HTTP por rota (telemetria de API, não de stream)
- `/monitoring` — uptime das emissoras (histórico, mas centrado em saúde, não em impacto comercial)

Falta uma visão que cruze **falha de captação** (stream down, worker travado) com **impacto na campanha** (slots agendados que não tocaram). Esse cruzamento é hoje feito manualmente: o admin abre `/monitoring` pra ver quais rádios caíram, anota IDs, e depois vai em `/campaigns` filtrar uma a uma pra ver quem foi afetado.

Esta tela materializa esse cruzamento: lista as emissoras que tiveram problema em um dia (default = ontem) e, embaixo de cada uma, as campanhas cujos slots foram perdidos por causa do problema. Cada campanha é um link direto pra `/campaigns?campaign=<id>` — a campanha aparece já filtrada/expandida na tela de campanhas.

## Quem usa

Admin only. Frontend gate via `<RequireRole roles={['admin']}>` em `App.jsx`. Backend gate via `auth.RequireRole("admin")` em `router.go` (mesmo padrão de `/admin/overview` e `/admin/monitoring`).

Sidebar: novo item dentro do grupo "Administração", abaixo de `Monitoramento` e acima de `Visão geral` (ou onde fizer sentido na ordem visual).

## O que conta como "falha"

Duas causas distintas, que podem coexistir numa mesma emissora num mesmo dia:

1. **`stream-down`** — derivado de `stream_health_events`. Janela `[event_at, event_at + duration_seconds]` em que o stream esteve fora.
2. **`silent-gap`** — derivado de `daily_play_summary.deficit > 0` em uma `(campaign, type, station, for_date)` SEM down event explicando. Significa que o slot era esperado, não tocou, e o stream parecia estar no ar — provavelmente worker travado, problema de codec, ou janela de override que ninguém respeitou.

Worker travado/ausente como categoria separada **não** entra. A motivação: a tela é centrada em IMPACTO, não em causa-raiz. Se o worker travou mas não houve campanha agendada naquele horário, ninguém foi afetado — não vira linha aqui. Pra ver workers travados sem impacto, o operador continua usando `/admin/overview`. Essa separação evita duplicar responsabilidade com `/admin/overview` (que já lista workers em estado degradado AGORA).

## Endpoint

```
GET /v1/internal/admin/station-failures?date=YYYY-MM-DD&min_down_seconds=60
```

Parâmetros:

| Param | Default | Limites |
|-------|---------|---------|
| `date` | ontem (timezone do servidor) | hoje - 90d ≤ date ≤ hoje |
| `min_down_seconds` | 60 | 0–3600 |

`date` fora dos limites → 400. Não suportar > hoje (intuitivo) nem < hoje-90d (eventos podem ter caído de partição, comportamento confuso de "alguns dados sumiram").

Resposta:

```json
{
  "date": "2026-05-18",
  "summary": {
    "stations_with_failure": 12,
    "total_down_seconds": 15120,
    "affected_campaigns": 18
  },
  "stations": [
    {
      "station": {
        "id": "...",
        "name": "Rádio Globo FM",
        "dial": "102.7 FM",
        "city": "São Paulo",
        "logo": "Rádios 2_Images/globo-fm.png"
      },
      "incidents": [
        {
          "type": "stream-down",
          "event_at": "2026-05-18T14:30:00-03:00",
          "duration_seconds": 1247
        }
      ],
      "total_down_seconds": 1260,
      "has_silent_gap": false,
      "campaigns": [
        {
          "campaign_id": "...",
          "campaign_name": "Coca-Cola Verão 2026",
          "client_name": "Coca-Cola",
          "expected": 8,
          "delivered": 5,
          "deficit": 3,
          "affected_by": ["stream-down"]
        },
        {
          "campaign_id": "...",
          "campaign_name": "Itaú Cartão Black",
          "client_name": "Itaú",
          "expected": 4,
          "delivered": 2,
          "deficit": 2,
          "affected_by": ["stream-down"]
        }
      ]
    }
  ]
}
```

Ordenação: `stations` ordenadas por `total_down_seconds` desc, depois por `affected_campaigns` count desc, depois por nome asc.

## Algoritmo

Pseudocódigo SQL/Go, resolvido em **3 queries** (sem N+1):

### Query 1 — stations com problema

```sql
WITH down_aggr AS (
  SELECT station_id,
         SUM(COALESCE(duration_seconds, EXTRACT(EPOCH FROM (NOW() - event_at))::int)) AS down_sec
  FROM stream_health_events
  WHERE event_type = 'down'
    AND event_at >= $1::date AND event_at < ($1::date + INTERVAL '1 day')
  GROUP BY station_id
  HAVING SUM(COALESCE(duration_seconds, EXTRACT(EPOCH FROM (NOW() - event_at))::int)) >= $2
),
deficit_aggr AS (
  SELECT station_id, COUNT(DISTINCT campaign_id) AS aff_camp
  FROM daily_play_summary
  WHERE for_date = $1::date AND deficit > 0
  GROUP BY station_id
)
SELECT s.id, s.name, s.frequency_mhz, s.band, s.city, s.logo,
       COALESCE(d.down_sec, 0) AS down_sec,
       COALESCE(df.aff_camp, 0) AS aff_camp
FROM stations s
LEFT JOIN down_aggr d   ON d.station_id = s.id
LEFT JOIN deficit_aggr df ON df.station_id = s.id
WHERE d.station_id IS NOT NULL OR df.station_id IS NOT NULL
ORDER BY down_sec DESC, aff_camp DESC, s.name ASC;
```

### Query 2 — incidents por station

```sql
SELECT station_id, event_at,
       COALESCE(duration_seconds, EXTRACT(EPOCH FROM (NOW() - event_at))::int) AS dur
FROM stream_health_events
WHERE event_type = 'down'
  AND event_at >= $1::date AND event_at < ($1::date + INTERVAL '1 day')
  AND station_id = ANY($2::uuid[])
ORDER BY station_id, event_at ASC;
```

Onde `$2` = lista de station_ids da Query 1.

### Query 3 — campanhas com deficit + cruzamento temporal

`daily_play_summary` agrupa por (campaign_id, **type_id**, station_id, for_date). Para a UI agregamos por campanha (somando os tipos), e olhamos as janelas de regra da campanha+station naquele dia.

```sql
SELECT dps.station_id, dps.campaign_id,
       c.name AS campaign_name, cl.name AS client_name,
       SUM(dps.expected) AS expected,
       SUM(dps.in_slot)  AS delivered,
       SUM(dps.deficit)  AS deficit,
       (
         SELECT array_agg(to_char(dr.time_start, 'HH24:MI') || '/' ||
                          to_char(dr.time_end,   'HH24:MI'))
         FROM distribution_rules dr
         WHERE dr.campaign_id = dps.campaign_id
           AND dps.station_id = ANY(dr.station_ids)
           AND dr.start_date <= $1::date AND dr.end_date >= $1::date
           AND (1 << EXTRACT(DOW FROM $1::date)::int) & dr.weekday_mask != 0
       ) AS rule_windows
FROM daily_play_summary dps
JOIN campaigns c ON c.id = dps.campaign_id
JOIN clients cl ON cl.id = c.client_id
WHERE dps.for_date = $1::date
  AND dps.deficit > 0
  AND dps.station_id = ANY($2::uuid[])
GROUP BY dps.station_id, dps.campaign_id, c.name, cl.name
ORDER BY dps.station_id, deficit DESC;
```

### Pós-processamento em Go

Para cada `(station, campaign)`:

1. Tem incidents na station? Carrega janelas `[event_at, event_at + dur]`.
2. Tem `rule_windows` na campanha?
   - **Cruza com alguma janela de incident** → `affected_by` inclui `"stream-down"`
   - **Não cruza com nenhuma janela** (ou não tem rule) → `affected_by` inclui `"silent-gap"`
3. Se a station só tem campaign com `silent-gap` (sem incidents): `has_silent_gap = true`.

Cruzamento é puro Go (são poucos pares — média < 100 por dia). Não vale a pena fazer em SQL.

## Frontend

### Página

`AdminStationFailuresPage.jsx`:

```
<Page>
  <Header>
    <h1>Emissoras com falha</h1>
    <DatePicker value={date} onChange={setDate} max={today} min={today-90} />
    <Select label="Mín. tempo fora" value={minDown} options={[0, 60, 300, 1800]} />
    <RefreshButton onClick={refetch} />
  </Header>

  <SummaryStrip>
    {summary.stations_with_failure} emissoras · {fmtDuration(summary.total_down_seconds)} fora · {summary.affected_campaigns} campanhas
  </SummaryStrip>

  {isLoading && <StationFailureCardSkeleton count={5} />}
  {isEmpty && <EmptyState />}
  {stations.map(s => <StationFailureCard key={s.station.id} data={s} />)}
</Page>
```

### `StationFailureCard.jsx`

```
<article class="station-failure-card">
  <header>
    <SmartImage src={getAppSheetImageUrl(s.station.logo)} />
    <div>
      <h3>{s.station.name}</h3>
      <p>{s.station.dial} · {s.station.city}</p>
    </div>
    <Badge severity={badgeSeverity(s.total_down_seconds)}>
      {fmtDuration(s.total_down_seconds)} fora
    </Badge>
  </header>

  <p class="incident-summary">
    {s.incidents.length} {pluralize('incidente', s.incidents.length)} ·
    {fmtDuration(s.total_down_seconds)} fora ·
    {s.has_silent_gap && 'silent-gap detectado'}
  </p>

  {s.campaigns.length > 0 && (
    <div class="campaigns-block">
      <h4>Campanhas afetadas</h4>
      <ul>
        {s.campaigns.map(c => <CampaignDeficitChip key={c.campaign_id} data={c} />)}
      </ul>
    </div>
  )}
</article>
```

### `CampaignDeficitChip.jsx`

```
<Link to={`/campaigns?campaign=${c.campaign_id}`} class="campaign-deficit-chip">
  <div class="name-row">
    <strong>{c.campaign_name}</strong>
    <span class="client">{c.client_name}</span>
  </div>
  <div class="numbers-row">
    <span>esperado <b>{c.expected}</b></span>
    <span class="dot">·</span>
    <span>entregue <b>{c.delivered}</b></span>
    <span class="dot">·</span>
    <span class="deficit">faltam <b>{c.deficit}</b></span>
    {c.affected_by.includes('silent-gap') && <Pill tone="warning">silent-gap</Pill>}
  </div>
  <Arrow class="link-arrow" />
</Link>
```

### Visual (design.md §1, §4.1)

- Card: branco, `--radius-xl` (16–24px), `--shadow-sm`. Hover → `translateY(-2px)`, `--shadow-md`, border `--color-tertiary-300`.
- Chip de campanha: card interno com hover próprio (border vira tertiary-300, seta acende em rosa).
- Badge de tempo fora: cor semântica
  - `< 5min` → cinza-warning (gray-100 bg, gray-700 text)
  - `5–30min` → `--color-warning` (laranja)
  - `> 30min` → `--color-danger` (vermelho)
- Empty state §4.7: ícone grande de "all clear" + título + skeleton de "como apareceria um card de falha" em opacity 0.3.

### Filtros — interação

- **Data picker**: default ontem, mudança dispara refetch (cache key inclui `date`).
- **Mín. tempo fora**: filtro client-side se < 60s (mostra silent-gaps sem down). Threshold ≥ 60s → server-side (`min_down_seconds`).

### Loading skeleton

`StationFailureCardSkeleton` repete 5 cards "vazios" com pulse animation. Mesmo padrão de `CampaignCardSkeleton` do `/campaigns`.

### Refresh

- `useQuery` com `staleTime: 60_000`, `refetchOnWindowFocus: false`. Não é tempo real.
- Botão manual refresh no header.

## Filtro `?campaign=<id>` em `/campaigns`

### Backend

Estender `GET /v1/internal/campaigns/paged` (handler em `workers/internal/api/handlers/campaigns.go`) pra aceitar:

```
?id=<uuid>
```

Quando `id` está presente:
- Ignora `q` e `competence` (filtros incompatíveis).
- Retorna no máximo 1 resultado.
- 404 se a campanha não existir (não 200 com lista vazia — sinaliza erro de deep-link).

### Frontend

`CampaignsPage.jsx`:

1. Importar `useSearchParams` de `react-router-dom`.
2. `const [searchParams, setSearchParams] = useSearchParams()`
3. `const campaignId = searchParams.get('campaign') || undefined`
4. Passar pro hook: `useCampaignsPaged({ ..., id: campaignId })`
5. `useCampaignsPaged` em `hooks.js` aceita `id` e injeta na URL da query.
6. Quando `campaignId` está presente, renderiza um banner sticky acima da lista:

```
[Filtrado: Coca-Cola Verão 2026]  [×]
```

Botão `×` → `setSearchParams({})` (limpa o param, volta à listagem padrão).

7. Auto-expansão: se a campanha vier resolvida, abre automaticamente o painel de detalhes/edição (se já existir esse estado no card) — opcional, depende do estado atual do card. Pode ficar fora desta entrega.

### Caso "campanha removida"

Se a campanha existir na resposta de `station-failures` mas o admin clicar e a campanha tiver sido deletada no meio-tempo:

- Backend devolve 404 em `?id=<uuid>` deletada
- Frontend mostra mensagem "Campanha não encontrada (pode ter sido removida)" + botão "Voltar"

## Edge cases

| Caso | Comportamento |
|------|---------------|
| `date` > hoje | 400 `invalid_date_future` |
| `date` < hoje - 90d | 400 `invalid_date_too_old` |
| Zero stations com falha | Empty state §4.7 + link sutil pra `/admin/overview` |
| Station com falha mas zero campanhas afetadas | Card aparece, bloco "Campanhas afetadas" some, mensagem inline "nenhuma campanha tinha slot nesse intervalo" |
| Campanha deletada após a falha | Chip renderiza mas vira `<span>` (sem link), label "campanha removida" |
| Station deletada (soft-delete) | Pula (não retorna no endpoint) |
| Logo da station ausente | `SmartImage` cai no fallback `MdRadio` automaticamente |
| Cliente deletado da campanha | `client_name` = "—" (não quebra) |
| Down event ainda aberto (sem duration_seconds) | `duration_seconds` calculado como `NOW() - event_at` no SQL |
| Múltiplas down windows + uma silent-gap na mesma station | `affected_by` da campanha pode ter ambos `["stream-down", "silent-gap"]` se houver split (raro) |

## Não cobre (escopo intencionalmente fora)

- **Histórico de worker travado** — não temos persistência de PCM por minuto. Worker travado vira `silent-gap` quando há campanha esperando; sem campanha esperando, não aparece aqui. Pra worker travado AGORA, usar `/admin/overview`.
- **Comparação com fornecedor Audiency** — outra tela.
- **Exportação CSV/PDF** — não pedido. Se vier no futuro, encaixa no header como em `/airtime-report`.
- **Alerta proativo (webhook/email)** — fora de escopo. A tela é pull-only.
- **Janela mensal/semanal** — só dia único nessa entrega. Range opcional fica como follow-up se virar útil.

## Plano de testes

### Backend (Go)

- `station_failures_test.go`:
  - dia sem falha → array vazio, summary zerado
  - station com 1 down event + 1 campanha com deficit → retorna `affected_by: ["stream-down"]`
  - station com deficit sem down → `affected_by: ["silent-gap"]`, `has_silent_gap: true`
  - station com 2 down events que cobrem todo o dia + 1 campanha → cruzamento OK
  - station deletada (soft) não aparece
  - `min_down_seconds=300` filtra incidents curtos
  - `date` futura → 400
  - `date` antiga (-100d) → 400

### Frontend (Vitest + RTL)

- `AdminStationFailuresPage.test.jsx`:
  - renderiza skeleton durante loading
  - renderiza empty state quando `stations.length === 0`
  - renderiza N cards quando há dados
  - mudança de data dispara refetch
  - filtro de `min_down_seconds` client-side funciona
- `CampaignDeficitChip.test.jsx`:
  - clique navega pra `/campaigns?campaign=<id>` (mock de navigate)
  - chip de campanha removida renderiza como span (sem link)
- `CampaignsPage.test.jsx` (regressão):
  - `?campaign=<id>` na URL filtra a lista pra essa campanha
  - banner sticky aparece com nome da campanha
  - clique no `×` limpa o param

### E2E manual

1. Forçar 1 stream-down em station de dev (`./scripts/simulacao-radio.sh stop X` por 2min, depois start)
2. Aguardar 1 hora do dia (ou adiantar relógio)
3. Acessar `/admin/station-failures` no dia seguinte → station aparece
4. Clicar na campanha afetada → CampaignsPage abre filtrada nela
5. Clicar no × do banner → volta à listagem completa

## Métricas / observabilidade

- Endpoint instrumentado pelo middleware padrão de `reqmetrics` (já em prod). Latência da query aparece em `/admin/monitoring`.
- Log estruturado em casos lentos (> 1s): `slog.Warn("station_failures_slow", "date", date, "duration_ms", elapsed)`.
- Sem métrica Prometheus dedicada — endpoint pouco frequente.

## Cronograma estimado

| Etapa | Tempo |
|-------|-------|
| Backend (handler + catalog + testes) | 4h |
| Filtro `?id=` em `/campaigns/paged` | 1h |
| Frontend page + cards + chips + skeleton + empty state | 5h |
| `?campaign=` em `CampaignsPage` + banner sticky | 1h |
| Testes frontend | 2h |
| Doc em `docs/features/admin-station-failures.md` | 30min |
| **Total** | **~13h** |

## Decisões tomadas

1. **Cards (Opção A) sobre tabela ou split** — alinhamento com card-pattern do design.md, melhor escaneabilidade pra typical caseload < 30 stations/dia.
2. **`silent-gap` herdado de deficit sem cruzamento temporal** em vez de coluna separada de "worker travado" — evita duplicar com `/admin/overview` que já lista workers degradados.
3. **`?id=` em `/campaigns/paged`** em vez de novo endpoint `/campaigns/<id>/expanded` — reusa infra existente, evita criar variação de cache.
4. **3 queries em vez de 1 monstro** — clareza > microperformance. Volume baixo.
5. **Sem polling** — admin investigando, não monitorando em tempo real. `/admin/overview` cobre o caso de tempo real.

## Premissas

- A view `daily_play_summary` está atualizada quando o admin acessa. Hoje é recalculada a cada insert/update de detection (categorizer). Sem lag perceptível.
- `stream_health_events` está fechando duração corretamente (incidente 2026-05-18 — Mix 93.70 FM com zumbis — foi corrigido pelo `CloseOrphanedOpenDowns`). Se um down event ficar aberto, a query calcula duration = `NOW() - event_at` (justo: "ainda fora").
- `stations.monitoring_status` não filtra a query — stations com problema mas em pause aparecem (admin precisa ver). Se virar ruído, adicionar filtro `WHERE monitoring_status = 'active'` na CTE de stations.
- Timezone: queries usam timezone do servidor (UTC). Frontend converte pra timezone local do navegador no display. `date` no query param é interpretado como local-date do servidor (consistente com o resto do sistema).
