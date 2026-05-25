---
status: implementado
ultima-verificacao: 2026-05-25
codigo-relacionado:
  - workers/internal/catalog/station_failures.go
  - workers/internal/api/handlers/admin_station_failures.go
  - workers/internal/api/router.go
  - workers/cmd/api/main.go
  - frontend/src/pages/AdminStationFailuresPage.jsx
  - frontend/src/pages/AdminStationFailuresPage.css
  - frontend/src/components/StationFailureCard.jsx
  - frontend/src/components/CampaignDeficitChip.jsx
  - frontend/src/pages/CampaignsPage.jsx
  - frontend/src/api/hooks.js
  - frontend/src/components/Sidebar.jsx
  - frontend/src/App.jsx
---

# Admin → Emissoras com falha (`/admin/station-failures`)

Lista as emissoras que tiveram falha em um dia (default ontem) e as campanhas cujos slots foram perdidos. Cada campanha é um link direto pra `/campaigns?campaign=<id>` — abre filtrada na tela de campanhas.

## Por que existe

Antes desta tela, cruzar "rádios que caíram" com "campanhas afetadas" era trabalho manual: abria `/monitoring`, anotava IDs, ia em `/campaigns` filtrar uma a uma. Esta página resolve o cruzamento em um único endpoint admin-only.

Funcionalmente complementa o `/admin/overview` (que mostra estado *agora* dos workers/streams) e `/monitoring` (uptime histórico genérico) — esta tela é centrada em **impacto comercial** de um dia específico.

## Quem pode ver

Admin only.

- **Rota frontend** gated via `<RequireRole roles={['admin']}>` em `App.jsx`
- **Endpoint backend** gated via `auth.RequireRole("admin")` em `router.go`

Sidebar: novo item "Falhas por emissora" dentro do grupo Administração, entre `/admin/monitoring` e `/admin/users`.

## O que conta como "falha"

| Tipo | Origem | Quando aparece |
|------|--------|----------------|
| `stream-down` | `stream_health_events.event_type='down'` no dia | Janela do down event cruza a faixa horária `[time_start, time_end]` de alguma regra da campanha |
| `silent-gap`  | `daily_play_summary.deficit > 0` sem down explicando | Slot esperado, não tocou, stream parecia ok (provável worker travado, codec, override que ninguém respeitou) |

Worker travado **sem campanha agendada** não aparece. Sem impacto, sem linha. Pra ver workers degradados agora, o operador continua usando `/admin/overview`.

## Endpoint

```
GET /v1/internal/admin/station-failures?date=YYYY-MM-DD&min_down_seconds=60
```

Parâmetros:

| Param | Default | Limites |
|-------|---------|---------|
| `date` | ontem | `today-90d ≤ date ≤ today`. Fora disso → 400 |
| `min_down_seconds` | 60 | 0–3600. Fora da faixa → ignora (mantém default) |

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
      "station": { "id": "...", "name": "Rádio X", "dial": "102.7 FM", "city": "São Paulo", "logo_url": "..." },
      "incidents": [{ "type": "stream-down", "event_at": "...", "duration_seconds": 1247 }],
      "total_down_seconds": 1260,
      "has_silent_gap": false,
      "campaigns": [
        {
          "campaign_id": "...", "campaign_name": "...", "client_name": "...",
          "expected": 8, "delivered": 5, "deficit": 3,
          "affected_by": ["stream-down"]
        }
      ]
    }
  ]
}
```

Ordenação: stations por `total_down_seconds desc`, depois `affected_campaigns desc`, depois nome asc. Campanhas dentro de cada station por `deficit desc`.

## Algoritmo

3 queries SQL + cruzamento de janelas em Go:

1. **Q1** — Stations com problema: `stream_health_events` (down ≥ minDown) UNION `daily_play_summary` (deficit > 0). Devolve totals.
2. **Q2** — Incidents (down events) por station no dia. Calcula `duration_seconds` retroativamente para events abertos via `NOW() - event_at`.
3. **Q3** — Campanhas com deficit no dia + janelas de regra `HH:MM/HH:MM` agregadas por (station, campaign). Usa subquery na lista de `distribution_rules` filtrando por weekday_mask + date range.

Pós-processamento (puro Go): pra cada `(station, campaign)`, `crossesAny(rule_windows, down_ranges)` decide se a causa foi `stream-down` ou `silent-gap`. Pode marcar ambos quando uma janela cruza e outra não.

`crossesAny` é puro (testado isoladamente em `station_failures_test.go`) e usa "strict overlap": touching endpoints (`down ends 08:00` vs `rule starts 08:00`) **não** contam como cross.

## Deep-link `?campaign=<id>` em `/campaigns`

Backend: `GET /v1/internal/campaigns?id=<uuid>` (extensão de `Campaigns.ListPaged`). Quando `id` vem setado, ignora `q` e `competence` e devolve no máximo 1 campanha.

Frontend: `useSearchParams` lê o param em `CampaignsPage.jsx`, passa pro `useCampaignsPaged({ id })`. Quando filtrado, renderiza um banner sticky rosa com nome da campanha e botão `×` pra limpar (`setSearchParams({})`).

## Performance

3 queries SQL. ~50ms típico, <500ms no pior dia. Sem cache (staleTime 60s no React Query). Sem polling — admin investigando sob demanda.

A subquery de `rule_windows` (Q3) lê `distribution_rules` 1× por (station, campaign) afetado. Em dias com 100+ stations afetando 5 campanhas cada → ~500 lookups indexados por `(campaign_id)`. Quando virar gargalo, dá pra materializar o cross em CTE.

## Edge cases mapeados

| Caso | Comportamento |
|------|---------------|
| `date` > hoje | 400 `invalid date: future not supported` |
| `date` < hoje - 90d | 400 `invalid date: older than 90 days` |
| `date` malformado | 400 `invalid date: expected YYYY-MM-DD` |
| Zero stations com falha | Empty state "Tudo no ar" com shadow-UI |
| Station com falha mas zero campanhas afetadas | Card aparece; bloco "Campanhas afetadas" some; mensagem inline "nenhuma campanha tinha slot nesse intervalo" |
| Campanha deletada após a falha | Chip renderiza normalmente até backend devolver 404 no `?id=` — banner mostra "campanha específica" |
| Down event ainda aberto (sem duration_seconds) | Duration calculada como `NOW() - event_at` |
| Logo da station ausente | `StationAvatar` cai no fallback de iniciais coloridas |

## Não cobre (escopo intencionalmente fora)

- Worker travado *sem* campanha agendada — usar `/admin/overview`
- Range de múltiplos dias — só dia único
- **Perspectiva campaign-first** — coberto pelo modo "Por campanha" da mesma página, ver [admin-campaign-failures.md](admin-campaign-failures.md)
- Exportação CSV/PDF no modo "Por emissora" — fora de escopo (o modo "Por campanha" tem PDF de cobrança)
- Alerta proativo (webhook/email) — pull-only
- Histórico de PCM por minuto — não persistimos; daí a heurística "silent-gap"

## Spec arquitetural

[docs/superpowers/specs/2026-05-19-admin-station-failures-design.md](../superpowers/specs/2026-05-19-admin-station-failures-design.md)

## Mudança 2026-05-25: filtro por déficit

A listing de `/admin/station-failures` deixou de incluir emissoras com **só downtime** (sem `daily_play_summary.deficit > 0` naquele dia). Era ruído visual — operador via rádios que caíram fora de qualquer janela programada e não tinham impacto. Detalhe no spec [`2026-05-25-admin-notifications-and-failure-filter-design.md`](../superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md) §4.5.

Estações com deficit continuam aparecendo, com info de downtime quando aplicável.
