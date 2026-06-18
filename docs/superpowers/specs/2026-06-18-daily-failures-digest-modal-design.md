# Modal de resumo de falhas do dia anterior — Design

**Data:** 2026-06-18
**Status:** aprovado para implementação
**Autor:** brainstorming Claude + Dereck

## 1. Problema / motivação

O admin precisa de um "briefing" proativo das falhas do dia anterior assim que
abre o sistema. Hoje a informação existe, mas é *pull-only*: ele tem que lembrar
de abrir `/admin/station-failures` ou caçar no sininho. Queremos uma interrupção
controlada — uma modal que aparece **uma vez por dia, por usuário**, resume as
campanhas que falharam ontem e leva pra tela de falhas com um clique.

Não substitui o sininho (inbox passivo de 7 dias) nem a página de falhas — é um
empurrão diário pra garantir que ninguém perca o resumo de ontem.

## 2. Decisões fechadas (do brainstorming)

| Decisão | Escolha |
|---------|---------|
| Audiência | **Só admin.** Cliente nunca vê. |
| Gatilho | **Global**, no primeiro load do app no dia (qualquer página). |
| Marcar como vista | **Só ao interagir** (fechar via X / backdrop / ESC, ou clicar no botão). Refresh antes de interagir reabre. |
| Conteúdo | Lista de campanhas com falha ontem + contagem de emissoras por campanha + resumo no topo. |
| Ação | Botão "Ver falhas" → `/admin/station-failures` (modo "Por campanha", filtrado em ontem). |

## 3. Abordagem escolhida

**Endpoint dedicado fino + reuso de `notification_reads`** (escolhida sobre
"só-frontend + localStorage" porque localStorage é por-browser, não cumpre
"1x por usuário"; e sobre "auto-abrir o sininho" porque a UX pedida é uma modal
agregada por campanha).

Reuso máximo do que já existe:
- **Dados:** `catalog.CampaignFailures.ListForDate(ontem)` — já devolve
  `Summary{Campaigns, Stations}` + campanhas ordenadas por impacto (mais
  emissoras primeiro), cada uma com suas `Stations`. A contagem por campanha é
  `len(stations)`.
- **Persistência de "visto":** tabela `notification_reads` (PK composta
  `(user_id, notification_key)`, chave `TEXT` genérica). **Sem migration nova.**

## 4. Backend

### 4.1 Endpoints (admin-only, montados no grupo admin do router)

```
GET  /v1/internal/admin/daily-failures-digest
POST /v1/internal/admin/daily-failures-digest/ack
```

Gating: `auth.RequireRole("admin")` no `router.go`, espelhando
`campaign-failures`.

### 4.2 GET — response

Calcula `ontem = time.Now().AddDate(0, 0, -1)` em `time.Local` (idêntico ao
default de `CampaignFailuresHandler.GetList`). Chama `ListForDate(ontem)`, reduz
pra forma leve, e adiciona `seen`.

```json
{
  "date": "2026-06-17",
  "seen": false,
  "summary": { "campaigns": 4, "stations": 11 },
  "campaigns": [
    {
      "id": "uuid",
      "name": "Verão 2026",
      "client_name": "Cliente X",
      "client_logo_url": "https://...",
      "stations_failed": 5
    }
  ]
}
```

- `seen` = existe `notification_reads(user_id = atual, notification_key =
  "daily_failures_digest:" + date)`.
- `campaigns` herda a ordenação de `ListForDate` (stations DESC, client_name ASC).
- `stations_failed` = `len(campaign.Stations)`.
- Sem falhas → `campaigns: []`, `summary: {0, 0}` (o frontend decide não mostrar).

### 4.3 POST ack — request/response

Sem body. Servidor deriva `ontem` da mesma forma do GET e marca como visto:

```
notifications.MarkRead(userID, ["daily_failures_digest:" + ontemStr])
```

Resposta: `{ "acked": true }`. Idempotente (o `MarkRead` existente já é
`ON CONFLICT (user_id, notification_key) DO NOTHING`).

Derivar a data no servidor (não aceitar do cliente) evita TZ drift e mantém o
padrão do `MarkAllReadInWindow`.

### 4.4 Chave de "visto"

```
daily_failures_digest:{YYYY-MM-DD}
```

Determinística, uma por usuário/dia. Como "ontem" avança a cada dia do
calendário, isso dá naturalmente o "1x por usuário por dia": no dia D a modal
mostra D-1; ao interagir, grava `daily_failures_digest:D-1`; refetch devolve
`seen:true` → some pelo resto de D. No dia D+1, "ontem" = D, chave nova → mostra
de novo (se houver falhas).

### 4.5 Código novo

- `catalog/notifications.go`: método `HasRead(ctx, userID uuid.UUID, key string)
  (bool, error)` — um `SELECT EXISTS(...)`.
- `api/handlers/admin_daily_failures_digest.go`: handler novo
  `DailyFailuresDigestHandler` com deps:
  - `Campaigns CampaignFailuresRepo` (reusa a interface existente — só usa
    `ListForDate`).
  - `Notifications NotificationsRepo` (estende a interface com `HasRead`;
    `MarkRead` já existe).
  - `Log *zap.Logger`.
  - Métodos `Get` e `Ack`. Padrão de `userIDFromReq`, `writeJSON`, e o guard
    `Repo == nil` (test harness) iguais aos handlers vizinhos.
- `api/router.go`: registrar as 2 rotas no grupo admin.
- `cmd/api/main.go`: instanciar e injetar.

Nenhuma migration. Nenhuma mudança de schema.

### 4.6 Reducer (forma leve)

A redução de `DailyResult` → digest é trivial e fica no handler (ou função pura
testável `digestFromDaily(res *catalog.DailyResult, seen bool) DigestResponse`).
Mantém o handler fino e testável sem DB.

## 5. Frontend

### 5.1 Hooks (`api/hooks.js`)

- `useDailyFailuresDigest()` — React Query GET, `enabled: isAdmin`,
  `staleTime` curto (ex. 5 min), `refetchOnWindowFocus: false`. Chave de query
  `['daily-failures-digest']`.
- `useAckDailyFailuresDigest()` — mutation POST `/ack`; no `onSuccess` faz
  `queryClient.setQueryData(['daily-failures-digest'], old => ({...old, seen:true}))`
  pra não reabrir sem refetch.

### 5.2 Componente `<DailyFailuresModal/>`

- `createPortal` em `document.body`, classes do design system
  (`.confirm-backdrop`, `.confirm-card`, `.btn btn-primary`, `.btn btn-secondary`,
  `.btn-sm`), ESC handler — espelha `ConfirmModal.jsx` pra ficar visualmente
  idêntico. CSS extra (lista rolável, badge de contagem, avatar) num
  `DailyFailuresModal.css` próprio, reaproveitando tokens existentes.
- Renderiza **só se** `isAdmin && data && !data.seen && data.campaigns.length > 0`.
- Layout:
  - Título: **"Falhas de ontem (DD/MM)"**.
  - Subtítulo: **"{campaigns} campanhas · {stations} emissoras com falha"**.
  - Lista rolável (max-height): por campanha → avatar/logo do cliente (fallback de
    inicial colorida, padrão do projeto) + nome da campanha + nome do cliente +
    badge **"N emissoras"**.
  - Rodapé: **"Ver falhas"** (primário) e **"Fechar"** (secundário).
- Ações:
  - **"Ver falhas"** → `ack()` + `navigate('/admin/station-failures?view=by_campaign&date={date}')`
    (mesmo deep-link que o sininho já usa; a página lê os params no mount).
  - **"Fechar" / X / backdrop / ESC** → `ack()` + fecha.
  - Todas as formas de dispensar chamam `ack` (decisão "marca ao interagir").

### 5.3 Montagem

`<DailyFailuresModal/>` montado uma vez dentro do `AppShell` (já sob
`RequireAuth`). Auto-gateia em admin internamente via `useAuth().isAdmin`, então
montar no shell é seguro pra cliente (hook desabilitado → nada renderiza, zero
fetch).

## 6. Edge cases

| Caso | Comportamento |
|------|---------------|
| Sem falhas ontem (`campaigns: []`) | Não renderiza, não acka |
| Já visto hoje (`seen: true`) | Não renderiza |
| Erro no GET | Falha silenciosa, sem modal (feature não-crítica) |
| Cliente logado | Hook `enabled:false`, nunca aparece, nenhum request |
| Admin não logou ontem | Vê só o "ontem" do dia em que logar; não é backlog (o sininho de 7 dias cobre). Limitação intencional |
| Campanha cancelada depois da falha | `ListForDate` já exclui `status='cancelada'` |
| Refresh antes de interagir | Reabre (correto — só marca ao interagir) |

## 7. Testes

- **Go:** `digestFromDaily` puro (reduz + flag seen). Handler `Get`/`Ack` com
  repos fake (sem DB), incluindo: admin sem falhas → `campaigns:[]`; `seen`
  refletindo `HasRead`; `Ack` chamando `MarkRead` com a chave certa; 401 sem
  claims. `HasRead` coberto por teste de integração se houver harness de DB.
- **Frontend:** render condicional (`seen`, vazio, não-admin) e que cada forma de
  dispensar dispara o ack. Conforme o padrão de testes de componente existente
  no projeto (se houver).

## 8. Documentação

- Novo `docs/features/daily-failures-digest-modal.md` com header YAML
  (`status: implementado`, `ultima-verificacao`, `codigo-relacionado`).
- Linha no "Mapa de consulta" do `CLAUDE.md`.
- **Nada** no `plano_implementacao.md` (regra 2 do projeto).

## 9. Fora de escopo (YAGNI)

- Backlog de múltiplos dias na modal.
- Versão pro cliente (destino diferente do botão).
- Envio por email / push.
- Preferência "não mostrar mais".
- Realtime — a modal é decidida no load, não precisa de polling.
