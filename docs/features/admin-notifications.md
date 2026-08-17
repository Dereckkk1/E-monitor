---
status: implementado
ultima-verificacao: 2026-08-17
codigo-relacionado:
  - frontend/src/components/NotificationBell.jsx
  - frontend/src/components/NotificationPopover.jsx
  - frontend/src/components/ClientAvatar.jsx
  - frontend/src/pages/DashboardPage.jsx
  - workers/internal/api/handlers/admin_notifications.go
  - workers/internal/catalog/notifications.go
  - migrations/0032_notification_reads.up.sql
---

# Sininho de notificações (admin)

> Spec: [`docs/superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md`](../superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md)

Sininho no header do `/dashboard` (admin-only) com inbox das campanhas que tiveram déficit nos últimos 7 dias.

## Comportamento

- **Quem vê:** só admin. Cliente não vê.
- **O que entra:** uma notificação por `(campanha, dia)` quando `daily_play_summary.deficit > 0` para aquela combinação. Campanhas com `status = 'cancelada'` são filtradas.
  - Desde 2026-08-17 `deficit = max(0, expected − in_slot)`: veicular **fora da faixa não fecha mais a obrigação** ([quota-aware-categorization.md](quota-aware-categorization.md)), então dias "tocou tudo no horário errado" passam a gerar notificação. Volume esperado maior.
- **Janela:** últimos 7 dias **fechados** (`CURRENT_DATE - INTERVAL '7 days'` até `CURRENT_DATE - 1`). O dia corrente é excluído porque uma campanha não pode ser considerada "falha" enquanto o dia ainda não acabou.
- **Polling:** React Query `refetchInterval: 60_000` (1 min).
- **Mark-as-read:** persistido por usuário em `notification_reads`. Marcar acontece no click do item OU em "Marcar todas como lidas" — abrir o popover NÃO marca.

## Endpoints

| Método | Rota | O que faz |
|--------|------|-----------|
| GET    | `/v1/internal/admin/notifications`             | Lista até 50 itens, com `read_at` por item e `unread_count`. |
| POST   | `/v1/internal/admin/notifications/mark-read`   | Body `{keys: [...]}`. Upsert idempotente. Cap de 200 keys. |
| POST   | `/v1/internal/admin/notifications/mark-all-read` | Sem body. Server-side deriva os keys da janela. |

## `notification_key` shape

```
campaign_failure:{campaign_uuid}:{YYYY-MM-DD}
```

Determinístico. Idempotente em upsert via PK composta `(user_id, notification_key)`.

## Deep-link

Click no item navega pra:

```
/admin/station-failures?view=by_campaign&date=YYYY-MM-DD&campaign=<uuid>
```

A page lê os query params no mount, setta o estado e abre o drawer. Limpa a URL em seguida.

## Limitações conhecidas

- Sem retenção/limpeza automática de `notification_reads`. Crescimento desprezível (~5 reads/dia/admin × admins).
- Sem paginação no GET (limite 50). Improvável estourar em 7 dias.
- Sem outros kinds de notificação ainda. O schema é genérico (`notification_key TEXT`).
- Sem realtime/WebSocket. Polling 1 min é suficiente.
- Cliente não tem sininho. Quando/se quisermos, é novo escopo.
