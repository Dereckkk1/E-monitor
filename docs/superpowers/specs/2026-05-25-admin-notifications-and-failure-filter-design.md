# Sininho de notificações + filtro de /station-failures — Spec

**Status:** Aprovado (2026-05-25)
**Autor:** Dereck + Claude (brainstorm)
**Escopo:** Frontend + Backend (1 migration nova). Duas mudanças relacionadas ("menos ruído sobre falhas").

## 1. Problema

Hoje o admin não tem uma forma rápida de saber "tem campanha com problema?" sem entrar manualmente em `/admin/campaign-failures` ou `/admin/station-failures`. E quando entra em `/admin/station-failures`, a página inclui emissoras que só tiveram downtime mesmo sem nenhuma inserção perdida — ruído visual para quem está investigando falhas que importam.

**O que queremos:**
1. **Sininho de notificação no `/dashboard`** mostrando campanhas que tiveram déficit nos últimos 7 dias, com leitura individual persistida por usuário.
2. **Filtrar `/admin/station-failures`** pra mostrar apenas emissoras com déficit > 0, removendo as que só tiveram downtime sem perda de inserção.

## 2. Decisões fechadas (brainstorm 2026-05-25)

- **Audiência:** só admin vê o sininho. Cliente não.
- **Conteúdo:** todas as campanhas com déficit nos últimos 7 dias, **incluindo as bonificadas** (não filtramos por bonificação — é a operação que decide se já está resolvido).
- **Granularidade:** 1 notificação por **(campanha, dia)**. Campanha que falhou em 3 dias = 3 notificações.
- **Read-state:** persistido no backend (nova tabela `notification_reads`).
- **Quando marca como lida:** apenas no click do item ou no "Marcar todas como lidas". Abrir o popover NÃO marca.
- **Janela:** últimos 7 dias, não-configurável por ora.
- **Polling:** React Query `refetchInterval: 60_000` (1 min).

## 3. Não-objetivos

- **Sem notificação para clientes.** Quando/se quisermos no futuro, é um novo escopo.
- **Sem realtime (WebSocket / SSE).** Polling 1 min é suficiente.
- **Sem agrupamento "Campanha X falhou em 3 dias".** Cada dia é seu próprio item.
- **Sem filtros configuráveis na UI** (período, tipo, severidade). Inbox simples.
- **Sem outros tipos de notificação ainda.** O schema é generalizável (`notification_key TEXT`), mas a única fonte hoje é `campaign_failure`.
- **Sem retenção/limpeza** da tabela `notification_reads`. Crescimento desprezível (uns poucos por dia por admin) — deixar pra depois.

## 4. Arquitetura

### 4.1. Modelo de dados (nova migration)

```sql
-- 0032_notification_reads.up.sql
CREATE TABLE notification_reads (
    user_id          UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notification_key TEXT         NOT NULL,
    read_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, notification_key)
);

CREATE INDEX idx_notification_reads_user ON notification_reads(user_id);
```

`notification_key` é um identificador determinístico do "evento" — para campaign-failures vai ser:

```
campaign_failure:{campaign_uuid}:{YYYY-MM-DD}
```

A PK composta `(user_id, notification_key)` torna o upsert idempotente. Sem trigger, sem race conditions reais.

### 4.2. Endpoints (backend)

| Método | Rota | Auth | O que faz |
|--------|------|------|-----------|
| GET    | `/v1/internal/admin/notifications`           | admin | Retorna lista de notifications dos últimos 7 dias com `read_at` por item (NULL se não lido). |
| POST   | `/v1/internal/admin/notifications/mark-read` | admin | Body `{keys: ["campaign_failure:...:..."]}`. Upsert múltiplo na tabela. Retorna `{marked: N}`. |
| POST   | `/v1/internal/admin/notifications/mark-all-read` | admin | Sem body. Server-side deriva os keys da janela atual (últimos 7 dias) e marca todos. Retorna `{marked: N}`. |

#### Shape da response do GET

```json
{
  "items": [
    {
      "key": "campaign_failure:7f5b...:2026-05-24",
      "kind": "campaign_failure",
      "campaign_id": "7f5b...",
      "campaign_name": "Promoção Dia das Mães",
      "client_id": "...",
      "client_name": "Acme",
      "client_logo_url": "https://...",
      "occurred_on": "2026-05-24",
      "read_at": null
    }
  ],
  "unread_count": 5
}
```

Ordenação: `occurred_on DESC, campaign_name ASC`. Limite hard de 50 itens (sem paginação por ora — em 7 dias é improvável passar disso pro nosso volume).

### 4.3. Source de dados das notificações

O backend deriva a lista direto do `daily_play_summary`, sem materializar:

```sql
SELECT
    'campaign_failure:' || c.id::text || ':' || dps.for_date::text AS key,
    c.id AS campaign_id, c.name AS campaign_name,
    cl.id AS client_id, COALESCE(cl.name, '—') AS client_name,
    COALESCE(cl.logo_url, '') AS client_logo_url,
    dps.for_date AS occurred_on,
    nr.read_at
FROM daily_play_summary dps
JOIN campaigns c ON c.id = dps.campaign_id
LEFT JOIN clients cl ON cl.id = c.client_id
LEFT JOIN notification_reads nr
    ON nr.user_id = $1
   AND nr.notification_key =
       'campaign_failure:' || c.id::text || ':' || dps.for_date::text
WHERE dps.for_date >= (CURRENT_DATE - INTERVAL '7 days')
  AND dps.for_date <= CURRENT_DATE
  AND dps.deficit > 0
  AND c.status != 'cancelada'
GROUP BY c.id, c.name, cl.id, cl.name, cl.logo_url, dps.for_date, nr.read_at
ORDER BY dps.for_date DESC, c.name ASC
LIMIT 50;
```

O `GROUP BY` colapsa múltiplas estações que falharam no mesmo (campanha, dia) numa única notificação. `unread_count` é `COUNT(*) WHERE read_at IS NULL` aplicado sobre a mesma query.

### 4.4. Frontend

#### Componente: `<NotificationBell />`

Arquivo novo: `frontend/src/components/NotificationBell.jsx`

Renderizado no header do `/dashboard` (e potencialmente em outros lugares do shell admin — mas começa só no dashboard). Botão circular ~36px com ícone de sininho. Quando `unread_count > 0`, badge vermelho com o número (cap em "9+").

Click toggla popover.

#### Componente: `<NotificationPopover />`

Popover ~360px, ancorado abaixo-direita do sininho. Layout:

```
┌─ Notificações ───────── Marcar todas como lidas ─┐
│                                                  │
│  [logo]  Campanha "Promoção Dia das Mães"   ●   │
│          Acme · ontem (24/05)                    │
│  ────────────────────────────────────────────    │
│  [logo]  Campanha "Black Friday"                 │
│          Acme · ontem (24/05)                    │
│  ────────────────────────────────────────────    │
│  [logo]  Campanha "Lançamento"              ●   │
│          Beta · há 2 dias (23/05)                │
│                                                  │
├──────────────────────── Ver tudo → ──────────────┤
└──────────────────────────────────────────────────┘
```

- Bullet "●" (cor `var(--c-action)` ou `var(--c-danger)`) só pra não-lidas.
- Logo do cliente: usa `<img src={client_logo_url}>` se houver, fallback inicial do nome em um círculo cinza (mesmo padrão de `<StationAvatar>` mas pra cliente).
- Texto da data: relativo + absoluto entre parênteses. "hoje", "ontem", "há N dias" + (DD/MM).
- Click no item:
  1. Chama `POST /admin/notifications/mark-read` com `{keys: [item.key]}`
  2. Navega pra `/admin/campaign-failures?campaign={campaign_id}&date={occurred_on}`
- Link "Marcar todas como lidas" no header: chama `POST /admin/notifications/mark-all-read`.
- Link "Ver tudo →" no footer: navega pra `/admin/campaign-failures`.
- Empty state: ícone sininho cinza + "Nada por aqui — campanhas estão em dia."

#### Roteamento — query params em `/admin/campaign-failures`

A page `/admin/campaign-failures` precisa aceitar `?campaign=<uuid>&date=<YYYY-MM-DD>`. Quando presente:
- Seleciona a tab "Por data" e setta a data
- Abre o drawer/drill-in da campanha pré-marcada

Se a page atual não suporta esses query params, adicionamos. (Inspeção runtime para confirmar.)

#### Hooks novos em `api/hooks.js`

```js
export function useNotifications() {
  return useQuery({
    queryKey: ['admin', 'notifications'],
    queryFn: () => api.get('/admin/notifications').then(r => r.data),
    refetchInterval: 60_000,
    staleTime: 30_000,
  })
}

export function useMarkNotificationsRead() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ keys }) =>
      api.post('/admin/notifications/mark-read', { keys }).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'notifications'] }),
  })
}

export function useMarkAllNotificationsRead() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () =>
      api.post('/admin/notifications/mark-all-read').then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'notifications'] }),
  })
}
```

### 4.5. Filtro /admin/station-failures (mudança cirúrgica)

`workers/internal/catalog/station_failures.go`, `ListForDate`, query principal:

```diff
- WHERE (d.station_id IS NOT NULL OR df.station_id IS NOT NULL)
+ WHERE df.station_id IS NOT NULL
```

Efeito: emissora que SÓ teve downtime (sem deficit naquele dia) some da lista. Emissora que teve deficit continua aparecendo com seu downtime quando aplicável (operador vê "rádio caiu E perdeu inserções"). `Summary.StationsWithFailure` e `Summary.TotalDownSeconds` se autocorrigem porque agregam do `result.Stations` filtrado.

Tests em `station_failures_test.go` precisam ajustar:
- Fixture com estação só-downtime: esperado agora é **ausência** dela do output.
- Adicionar caso explícito "estação com downtime mas sem deficit → não aparece".
- Manter caso "estação com deficit (com OU sem downtime) → aparece".

## 5. Comportamento end-to-end (cenário canônico)

1. Admin loga, vai pra `/dashboard`.
2. ✅ Sininho aparece no canto superior direito com badge "5" (5 notificações não-lidas).
3. Click no sininho → popover abre listando 5 + 2 já lidas (total 7) dos últimos 7 dias.
4. Click na primeira ("Promoção Dia das Mães, 24/05") →
   - Notificação é marcada como lida no backend.
   - Navega pra `/admin/campaign-failures?campaign=<id>&date=2026-05-24`.
   - Drill-in da campanha abre direto.
5. Volta pro `/dashboard` → sininho mostra "4" (uma foi lida).
6. Reabre popover → primeira item agora sem bullet (lida), demais ainda com bullet.
7. Click em "Marcar todas como lidas" → badge some, todas perdem bullet.
8. Outro admin loga em outra máquina → vê o estado dele (independente — read state é por user_id).

## 6. Edge cases

### 6.1. Notificação some entre poll e click
Admin abre popover. Logo depois (em background) a query roda e a notificação não está mais na janela de 7 dias (passou de janela). Click ainda funciona — o backend simplesmente registra o read mesmo se o item não está mais visível. Não tem dano.

### 6.2. Campanha cancelada após emitir notificação
Notificação some do GET (`WHERE c.status != 'cancelada'`). Read state existente fica órfão na tabela — sem impacto (chave nunca mais é consultada).

### 6.3. Read em concorrência (dois cliques rápidos)
PK `(user_id, notification_key)` com `ON CONFLICT DO NOTHING` no upsert resolve. Sem race.

### 6.4. Múltiplas estações falharam no mesmo (campanha, dia)
A query do GET faz `GROUP BY` collapsando, então é 1 notificação só. Drill-in mostra todas as estações na visão de campaign-failures (já existe).

### 6.5. Admin com 0 campanhas com falha
Sem badge. Popover ao abrir mostra empty state.

### 6.6. Backend retorna 50 itens (limit)
Frontend mostra os 50. Footer "Ver tudo →" continua redirecionando pra view completa. Não temos paginação aqui — se passar de 50 num período de 7 dias, é tempo de revisitar.

## 7. Validação / verificações pré-implementação

### 7.1. `/admin/campaign-failures` aceita query params?
Verificar `AdminCampaignFailuresPage.jsx` (ou nome equivalente). Se não aceita `?campaign=<id>&date=<YYYY-MM-DD>`, adicionar — sem isso, o click no item perde o drill-in direto.

### 7.2. `users` table existe com PK `id UUID`?
Sim — verificado em `migrations/0027_user_management.up.sql` (a migration mais recente que cria `users`). FK do `notification_reads.user_id` aponta pra lá.

### 7.3. Como obter o `user_id` no handler?
Verificar middleware existente. Provável (`req.Context()` → `userID, _ := ctx.Value(authKey).(uuid.UUID)`) — alinhar com o padrão de outros handlers admin.

### 7.4. CSS do header do `/dashboard` comporta o sininho?
Inspecionar `DashboardPage.css` (.dh-* e .cdash-*). O hero do dashboard admin tem espaço no topo direito. Se não tem container apropriado, criar `.dh-bell` co-localizado.

## 8. Aceitação

Feature considerada pronta quando:

1. Admin no `/dashboard` vê o sininho. Cliente não vê.
2. Badge mostra a count correta de não-lidas (validar em 0, 1, 5, 10+).
3. Click no item marca como lido E navega pro drill-in com campanha+data pré-selecionados.
4. "Marcar todas como lidas" zera o badge e some todos os bullets.
5. Polling de 60s atualiza sem flicker (React Query placeholder).
6. Dois browsers do mesmo admin convergem após o poll.
7. Dois admins distintos têm read state independente.
8. Empty state aparece quando não há notificações.
9. `/admin/station-failures` deixa de listar emissora "só-downtime", continua listando emissora "com déficit + downtime" e "com déficit sem downtime".
10. `station_failures_test.go` atualizado e passando.

## 9. Arquivos tocados

### Backend
- `migrations/0032_notification_reads.up.sql` (novo)
- `migrations/0032_notification_reads.down.sql` (novo)
- `workers/internal/catalog/notifications.go` (novo)
- `workers/internal/catalog/notifications_test.go` (novo — opcional, mas recomendado)
- `workers/internal/api/handlers/admin_notifications.go` (novo)
- `workers/internal/api/handlers/admin_notifications_test.go` (novo — recomendado)
- `workers/internal/api/server.go` (ou onde rotas são montadas — adicionar 3 rotas)
- `workers/internal/catalog/station_failures.go` (alterar WHERE)
- `workers/internal/catalog/station_failures_test.go` (atualizar fixtures + casos)

### Frontend
- `frontend/src/components/NotificationBell.jsx` (novo)
- `frontend/src/components/NotificationPopover.jsx` (novo)
- `frontend/src/components/ClientAvatar.jsx` (novo — fallback inicial pro cliente, se não existir já)
- `frontend/src/pages/DashboardPage.jsx` (mount do `<NotificationBell>` no header admin)
- `frontend/src/pages/DashboardPage.css` (estilo `.dh-bell` etc.)
- `frontend/src/pages/AdminCampaignFailuresPage.jsx` (adicionar suporte a `?campaign=&date=` se ainda não suporta)
- `frontend/src/api/hooks.js` (3 hooks novos)

### Docs
- `docs/features/admin-notifications.md` (novo)
- `docs/features/admin-station-failures.md` (atualizar — nota da mudança de filtro)
- `docs/features/admin-campaign-failures.md` (atualizar — query params de deep-link)

## 10. Riscos e mitigações

| Risco | Mitigação |
|-------|-----------|
| Crescimento descontrolado de `notification_reads` | Esperado ~5 reads/dia/admin × admins. Em 1 ano: ~2k linhas. Desprezível. Limpeza pode ser feita depois se necessário. |
| Polling de 60s amplifica QPS | Endpoint é leve (1 query no `daily_play_summary` + 1 LEFT JOIN). Cache do React Query (staleTime 30s) ajuda. |
| Notificação de campanha cancelada confunde operador | WHERE `c.status != 'cancelada'` filtra fora. Read state órfão fica na tabela, sem impacto. |
| Query params na URL de campaign-failures conflitam com filtros existentes | Verificar (§7.1). Adicionar com nome explícito (`?campaign=` em vez de só `?id=`). |
