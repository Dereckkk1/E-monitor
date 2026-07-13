---
status: implementado
ultima-verificacao: 2026-07-13
codigo-relacionado:
  - migrations/0051_suggestions.up.sql
  - workers/internal/catalog/suggestions.go
  - workers/internal/api/handlers/suggestions.go
  - workers/internal/api/router.go
  - workers/internal/config/config.go
  - frontend/src/pages/AdminSuggestionsPage.jsx
  - frontend/src/pages/suggestions/
  - frontend/src/pages/suggestions/AttachmentImage.jsx
  - frontend/src/api/hooks.js
  - frontend/src/components/Sidebar.jsx
---

# Sugestões — Central de demandas interna

Quando o Dereck fala de **"sugestões"**, é esta tela: `/admin/suggestions`. Uma
central onde os admins mandam demandas detalhadas (o que querem na plataforma) e
o **dev** (`tatico3@hubradios.com`) gerencia tudo — triage, status, prioridade,
feedback, conversa — sem sair da plataforma. Substitui o "abrir outro software"
pra organizar pedido.

Design/blueprint: [spec](../superpowers/specs/2026-07-09-suggestions-board-design.md) ·
[plano](../superpowers/plans/2026-07-09-suggestions-board.md).

## Personas (uma rota, duas faces)

Item **"Sugestões"** na sidebar (bloco Administração, `RequireRole admin`). O
conteúdo ramifica por identidade — mas o **gating de dados é imposto no
servidor**, não no front:

| Persona | Quem | Vê |
|---|---|---|
| **Autor** | qualquer admin/operator que **não** é o dev | *Minhas Sugestões*: cria, acompanha status, lê o feedback do dev, conversa na thread — só as **próprias**. |
| **Dev (Central de Comando)** | e-mail == `SUGGESTIONS_DEV_EMAIL` | Tudo de todos: lista densa + board, KPIs, triage inline, drawer de detalhe com prioridade real/esforço/notas privadas/feedback, cria demandas próprias. |

### Gating (por que é seguro)
- Todas as rotas ficam sob `RequireRole("admin","operator")` — nenhum viewer/cliente entra.
- O **JWT não carrega e-mail** (só UserID/Role/ClientID). Então `SuggestionsHandler.isDev()` carrega o usuário do banco e compara o e-mail (case-insensitive) com `SUGGESTIONS_DEV_EMAIL`.
- `GET /suggestions`: dev → todas; autor → forçado a `created_by = <caller>` no servidor (`ListSuggestionsFilter.OnlyAuthorID`). O autor **nunca** amplia o escopo.
- `GET/PATCH /suggestions/{id}`: autor só acessa as próprias (403 senão); o PATCH de gestão é **dev-only**.
- `dev_notes` (notas privadas) é **zerado** antes de serializar pra um autor — nunca vaza.

## Configuração (defaults funcionam sem env)

| Env | Onde | Default |
|---|---|---|
| `SUGGESTIONS_DEV_EMAIL` | backend (`config.go`) | `tatico3@hubradios.com` |
| `VITE_SUGGESTIONS_DEV_EMAIL` | frontend (build) | `tatico3@hubradios.com` |

Ambos caem no default — a feature funciona sem tocar em env em prod. Pra trocar
o dev, setar os dois (backend decide o acesso real; o frontend só decide qual UI
renderizar).

## Modelo de dados (migration `0051_suggestions`)

Cinco tabelas plain (não particionadas):

- **`suggestions`** — núcleo. `ref_num SERIAL UNIQUE` (número humano #42), `created_by`, `title`, `description`, `type` (`bug|melhoria|feature|duvida`), `target_screen`, `requester_priority` (`baixa|media|alta`), `status` (`nova|em_analise|aceita|em_progresso|concluida|recusada`, default `nova`), `dev_priority` (`urgente|alta|media|baixa`), `effort` (`P|M|G`), `dev_feedback` (o autor lê), `dev_notes` (privado do dev), `awaiting_author` (flag "bola com o autor"), `resolved_at`, timestamps.
- **`suggestion_comments`** — thread (autor ⇄ dev).
- **`suggestion_attachments`** — imagens no S3 (`suggestion_id` + `comment_id` nullable). O `<img>` do front NÃO usa a presigned direto (ver **Exibição das imagens** abaixo) — busca os bytes pelo proxy `GET /suggestions/attachments/{aid}`.
- **`suggestion_events`** — timeline (`created|status_changed|priority_changed|feedback_given|attachment_added|reopened`).
- **`suggestion_reads`** — `last_read_at` por (user, suggestion) → badges de não-lido.

## Ciclo de status

`nova → em_analise → aceita → em_progresso → concluida` (ou `recusada` a qualquer
momento, com motivo no `dev_feedback`). `resolved_at` é setado ao concluir/recusar
e limpo no `reopened`. `awaiting_author` é flag ortogonal ao status.

Prioridade dupla: o autor pede (`requester_priority`); o dev define a real
(`dev_priority`) na triage.

## Endpoints (`/v1/internal`)

Todos sob `RequireRole("admin","operator")`; gating fino no handler.

| Método | Rota | Quem | Nota |
|---|---|---|---|
| `POST` | `/suggestions` | admin | cria (retorna `ref_num`) |
| `GET` | `/suggestions` | admin | dev=todas (`status,type,priority,author_id,q,sort`); autor=próprias. Enriquece com `created_by_name`, `comment_count`, `unread` |
| `GET` | `/suggestions/{id}` | autor/dev | detalhe: campos + `comments`+`attachments`(presigned `url`)+`events`; `dev_notes` só p/ dev; dispara MarkRead |
| `PATCH` | `/suggestions/{id}` | **dev** | status/dev_priority/effort/dev_feedback/dev_notes/awaiting_author; gera events; seta resolved_at |
| `POST` | `/suggestions/{id}/comments` | autor/dev | limpa `awaiting_author` se o autor responde |
| `POST` | `/suggestions/{id}/attachments` | autor(own)/dev | multipart campo **`file`** (+`comment_id`), MIME imagem allow-list, ~10MB → S3 |
| `POST` | `/suggestions/{id}/read` | autor/dev | upsert last_read |
| `GET` | `/suggestions/attachments/{aid}` | autor/dev | **proxy dos bytes** (JWT) → é isto que o `<img>` usa. Ver nota abaixo |
| `GET` | `/suggestions/attachments/{aid}/url` | autor/dev | presigned GET 5min (legado — não usado pelo front; sofre o bloqueio de loopback) |
| `GET` | `/suggestions/summary` | **dev** | `by_status`, `oldest_open`, `resolved_this_month` (KPIs) |
| `GET` | `/suggestions/unread-count` | admin | `{count}` (badge da sidebar; escopo por persona) |

`unread` (linha e badge) tem semântica por persona: **dev** = qualquer
comment/event novo desde o `last_read`; **autor** = atividade de **outros** (dev)
nas suas, desde o `last_read`.

## Frontend

- `frontend/src/pages/AdminSuggestionsPage.jsx` ramifica em `<CentralDeComando/>` (dev) ou `<MinhasSugestoes/>` (autor).
- `frontend/src/pages/suggestions/` — componentes: `CentralDeComando`, `MinhasSugestoes`, `SuggestionDetail` (drawer), `SuggestionCreateModal`, `SuggestionThread`, `SuggestionPills`, `AttachmentLightbox`, `AttachmentImage` (busca o blob autenticado — ver **Exibição das imagens**), `ClipboardPasteZone` (colar print com Ctrl+V), `EmptyState` (ghost preview), `Skeletons` (shimmer), `icons.jsx` (SVG inline — sem lib nova, ver CLAUDE.md §5), `constants.js`, `utils.js`.

## Exibição das imagens (por que proxy, não presigned)

O upload salva a imagem no bucket (MinIO `radiocheck-evidence/suggestions/...`) —
isso sempre funcionou. O que quebrava era a **exibição**: o handler devolvia uma
URL **pré-assinada** e o front jogava no `<img src>`. Em prod o host do MinIO
assado na presigned é `http://localhost:9000` (`S3_PUBLIC_ENDPOINT`, ver
[deploy.md](../operations/deploy.md)), que o navegador do usuário **não
alcança** → `ERR_CONNECTION_REFUSED` / bloqueio de loopback. As imagens
apareciam quebradas, com o `alt` "anexo" (incidente reportado 2026-07-13).

É o **mesmo problema** que o áudio de evidência já tinha resolvido em 2026-07-03
([evidence-presigned-urls.md](evidence-presigned-urls.md)). A correção espelha
a mesma decisão: **proxiar os bytes pela API**.

- Backend: `SuggestionsHandler.ProxyAttachment` (`GET /suggestions/attachments/{aid}`)
  faz o mesmo gating owner-or-dev do `AttachmentURL` e streama o objeto com
  `http.ServeContent` (cópia do `DetectionsHandler.Evidence`).
- Frontend: `AttachmentImage` busca `GET /suggestions/attachments/{aid}` com
  `responseType: 'blob'` (axios anexa o JWT), gera um `blob:` object URL e o usa
  no `<img>`, revogando no unmount. Usado nos 3 pontos de render (thumb da
  descrição, thumb da thread, lightbox).
- Hooks em `api/hooks.js` (`useSuggestions`, `useSuggestion`, `useCreateSuggestion`, `useUpdateSuggestion`, `useAddSuggestionComment`, `useUploadSuggestionAttachment`, `useMarkSuggestionRead`, `useSuggestionsSummary`, `useSuggestionsUnread`, `useSuggestionAttachmentURL`).
- Sidebar: `IconSuggestions` + badge de não-lido (`useSuggestionsUnread`).
- Visual seguindo o `DESIGN.md` (E-radios): "pink is the new blue" (`#E81E75`), micro-interações (lift + borda rosa no hover, aura de foco), empty state como ghost/shadow-UI, skeletons no lugar de spinner.

## Decisões / notas

- **Admin-only nesta versão** (sem cliente/viewer). Não há fundação pra cliente — se um dia precisar, é refactor consciente.
- **E-mail do dev por env**, não chumbado no código (testável, troca sem recompilar). Default seguro.
- **`ref_num`** cosmético, ajuda a referenciar ("sugestão #42").
- Notificação v1 = badge in-app. E-mail/sininho ficaram **fora do escopo** (plugável depois, reusa o SMTP existente).
- Sem CLI novo → sem alteração no `workers.Dockerfile`.
