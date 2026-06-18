---
status: implementado
ultima-verificacao: 2026-06-18
codigo-relacionado:
  - workers/internal/api/handlers/admin_daily_failures_digest.go
  - workers/internal/catalog/notifications.go
  - workers/internal/api/router.go
  - workers/cmd/api/main.go
  - frontend/src/components/DailyFailuresModal.jsx
  - frontend/src/components/DailyFailuresModal.css
  - frontend/src/api/hooks.js
  - frontend/src/App.jsx
---

# Modal de resumo diário de falhas (admin)

> Spec: [`docs/superpowers/specs/2026-06-18-daily-failures-digest-modal-design.md`](../superpowers/specs/2026-06-18-daily-failures-digest-modal-design.md)

Modal admin-only que aparece **1x por dia, por usuário** no primeiro load do
app, resume as campanhas que tiveram falha **ontem** (com a contagem de
emissoras por campanha) e leva pra `/admin/station-failures`.

## Comportamento

- **Quem vê:** admin (operator==admin). Cliente nunca vê — o hook fica
  `enabled:false` e não dispara request.
- **Gatilho:** global, no primeiro load do app no dia (montada no `AppShell`,
  portaliza pro `document.body`). Aparece só se houver falhas de ontem ainda
  não vistas.
- **Marca como visto:** só ao interagir — Fechar / ESC / clicar no backdrop /
  clicar em "Ver falhas". Refresh antes de interagir reabre.
- **Conteúdo:** título "Falhas de ontem (DD/MM)", resumo "X campanhas · Y
  emissoras com falha", e a lista de campanhas com badge de contagem.

## Endpoints

| Método | Rota | O que faz |
|--------|------|-----------|
| GET    | `/v1/internal/admin/daily-failures-digest`       | Falhas de ontem (forma leve) + flag `seen` do usuário. |
| POST   | `/v1/internal/admin/daily-failures-digest/ack`   | Marca como visto (usuário atual + ontem). Sem body. Idempotente. |

Ambos admin-only (`auth.RequireRole("admin")`, mesmo grupo do sininho).

### Response do GET

```json
{
  "date": "2026-06-17",
  "seen": false,
  "summary": { "campaigns": 4, "stations": 11 },
  "campaigns": [
    { "id": "uuid", "name": "Verão 2026", "client_name": "Cliente X",
      "client_logo_url": "https://...", "stations_failed": 5 }
  ]
}
```

## Como funciona

- **Dados:** reusa `catalog.CampaignFailures.ListForDate(ontem)` (mesma fonte do
  modo "Por campanha" de `/admin/station-failures`), reduzido a
  `{campanha, cliente, stations_failed = len(stations)}` por `digestFromDaily`.
- **"Visto por usuário":** reusa a tabela `notification_reads` (chave genérica),
  chave `daily_failures_digest:{YYYY-MM-DD}`. Como "ontem" avança a cada dia, isso
  dá "1x por usuário por dia" naturalmente. **Sem migration nova.**
- A chave do digest **não** polui o sininho: `Notifications.List` só faz JOIN nas
  chaves `campaign_failure:...` que ele mesmo deriva.

## Deep-link do botão

```
/admin/station-failures?view=by_campaign&date=YYYY-MM-DD
```

Mesmo padrão do sininho — a página lê os params no mount.

## Limitações conhecidas

- **Não é backlog:** se o admin não logar num dia, ele vê só o "ontem" do dia em
  que logar (não acumula). O sininho de 7 dias cobre o backlog.
- Sem versão pro cliente, sem envio por email/push, sem "não mostrar mais".
- Sem limpeza de `notification_reads` (crescimento desprezível: ~1 chave de
  digest por admin por dia).
