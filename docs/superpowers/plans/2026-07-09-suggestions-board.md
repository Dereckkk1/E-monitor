# Central de Sugestões — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Tela "Sugestões" na sidebar (admin-only) onde admins mandam demandas detalhadas (tela-alvo, imagens, prioridade, thread) e o dev (`SUGGESTIONS_DEV_EMAIL`) gerencia tudo numa central de comando.

**Architecture:** Full-stack no monorepo. Backend Go (chi + pgx cru) espelhando `catalog/materials.go` + `handlers/`; migration `0051` com 5 tabelas plain. Frontend React (react-query + axios) com uma página que ramifica por identidade em duas personas. Imagens no S3/MinIO (reuso do `storage.Client`). Visual final pelas skills /impeccable + /pro-system-ui.

**Tech Stack:** Go 1.26, chi/v5, pgx/v5, aws-sdk-go-v2 S3; React + Vite, react-query, axios, react-select; PostgreSQL; golang-migrate.

**Spec:** [docs/superpowers/specs/2026-07-09-suggestions-board-design.md](../specs/2026-07-09-suggestions-board-design.md)

---

## File Structure

**Criar:**
- `migrations/0051_suggestions.up.sql` / `.down.sql` — 5 tabelas.
- `workers/internal/catalog/suggestions.go` — repo (tipos + SQL).
- `workers/internal/catalog/suggestions_test.go` — testes de repo.
- `workers/internal/api/handlers/suggestions.go` — handler HTTP + gating + upload.
- `workers/internal/api/handlers/suggestions_test.go` — testes de gating.
- `frontend/src/pages/AdminSuggestionsPage.jsx` (+ `.css`) — página + persona branch.
- `frontend/src/pages/suggestions/` — componentes (`CentralDeComando.jsx`, `MinhasSugestoes.jsx`, `SuggestionDetail.jsx`, `SuggestionCreateModal.jsx`, `SuggestionThread.jsx`, `SuggestionPills.jsx`, `AttachmentLightbox.jsx`, `ClipboardPasteZone.jsx`).
- `docs/features/suggestions-board.md` — doc operacional.

**Modificar:**
- `workers/internal/config/config.go` — env `SUGGESTIONS_DEV_EMAIL`.
- `workers/internal/api/router.go` — `Deps.Suggestions` + grupo de rotas.
- `workers/cmd/api/main.go` — construir repo/handler + injetar.
- `frontend/src/api/hooks.js` — hooks novos.
- `frontend/src/components/Sidebar.jsx` — item + ícone + badge.
- `frontend/src/App.jsx` — import + rota.
- `CLAUDE.md` — linha no mapa de consulta.

---

## Phase A — Backend

### Task 1: Migration 0051 (5 tabelas)

**Files:** Create `migrations/0051_suggestions.up.sql`, `migrations/0051_suggestions.down.sql`

- [ ] **Step 1: Escrever a migration up**

```sql
-- migrations/0051_suggestions.up.sql
BEGIN;

CREATE TABLE suggestions (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    ref_num             BIGINT GENERATED ALWAYS AS IDENTITY,
    created_by          UUID REFERENCES users(id) ON DELETE SET NULL,
    title               TEXT NOT NULL,
    description         TEXT NOT NULL,
    type                TEXT NOT NULL CHECK (type IN ('bug','melhoria','feature','duvida')),
    target_screen       TEXT,
    requester_priority  TEXT NOT NULL CHECK (requester_priority IN ('baixa','media','alta')),
    status              TEXT NOT NULL DEFAULT 'nova'
                        CHECK (status IN ('nova','em_analise','aceita','em_progresso','concluida','recusada')),
    dev_priority        TEXT CHECK (dev_priority IN ('urgente','alta','media','baixa')),
    effort              TEXT CHECK (effort IN ('P','M','G')),
    dev_feedback        TEXT,
    dev_notes           TEXT,
    awaiting_author     BOOLEAN NOT NULL DEFAULT false,
    resolved_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX suggestions_ref_num_idx ON suggestions (ref_num);
CREATE INDEX suggestions_created_by_idx ON suggestions (created_by);
CREATE INDEX suggestions_status_idx ON suggestions (status);
CREATE INDEX suggestions_updated_at_idx ON suggestions (updated_at DESC);

CREATE TABLE suggestion_comments (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    suggestion_id  UUID NOT NULL REFERENCES suggestions(id) ON DELETE CASCADE,
    author_id      UUID REFERENCES users(id) ON DELETE SET NULL,
    body           TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX suggestion_comments_sid_idx ON suggestion_comments (suggestion_id, created_at);

CREATE TABLE suggestion_attachments (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    suggestion_id  UUID NOT NULL REFERENCES suggestions(id) ON DELETE CASCADE,
    comment_id     UUID REFERENCES suggestion_comments(id) ON DELETE CASCADE,
    storage_key    TEXT NOT NULL,
    content_type   TEXT NOT NULL,
    size_bytes     BIGINT NOT NULL,
    uploaded_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX suggestion_attachments_sid_idx ON suggestion_attachments (suggestion_id);

CREATE TABLE suggestion_events (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    suggestion_id  UUID NOT NULL REFERENCES suggestions(id) ON DELETE CASCADE,
    actor_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    event_type     TEXT NOT NULL CHECK (event_type IN
                   ('created','status_changed','priority_changed','feedback_given','commented','attachment_added','reopened')),
    from_value     TEXT,
    to_value       TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX suggestion_events_sid_idx ON suggestion_events (suggestion_id, created_at);

CREATE TABLE suggestion_reads (
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    suggestion_id  UUID NOT NULL REFERENCES suggestions(id) ON DELETE CASCADE,
    last_read_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, suggestion_id)
);

COMMIT;
```

- [ ] **Step 2: Escrever a migration down**

```sql
-- migrations/0051_suggestions.down.sql
BEGIN;
DROP TABLE IF EXISTS suggestion_reads;
DROP TABLE IF EXISTS suggestion_events;
DROP TABLE IF EXISTS suggestion_attachments;
DROP TABLE IF EXISTS suggestion_comments;
DROP TABLE IF EXISTS suggestions;
COMMIT;
```

- [ ] **Step 3: Testar up/down num Postgres descartável** (ver memória `test-db-native-pg-shadows-docker`: usar `rc-test-pg` em 15432, NÃO o 5432 nativo). Aplicar `migrate up` até 0051 e `down 1`, confirmar sem erro e schema limpo.

- [ ] **Step 4: Commit** — `git add migrations/0051_* && git commit -m "feat(suggestions): migration 0051 (5 tabelas)"`

---

### Task 2: Config — env do dev-email

**Files:** Modify `workers/internal/config/config.go`

- [ ] **Step 1:** adicionar campo `SuggestionsDevEmail string` na struct de config, lendo `SUGGESTIONS_DEV_EMAIL` com default `"tatico3@hubradios.com"` (seguir o padrão dos outros campos com default no arquivo). 
- [ ] **Step 2:** `cd workers && go build ./internal/config` → PASS.
- [ ] **Step 3: Commit** — `git commit -am "feat(suggestions): env SUGGESTIONS_DEV_EMAIL"`

---

### Task 3: Repo `catalog/suggestions.go`

**Files:** Create `workers/internal/catalog/suggestions.go`, `workers/internal/catalog/suggestions_test.go`

Espelhar `catalog/materials.go` (struct wrapping `*pgxpool.Pool`, `NewSuggestions(pool)`, coluna const, `scanSuggestion`).

- [ ] **Step 1: Tipos + construtor.**

```go
package catalog

// Suggestion, SuggestionComment, SuggestionAttachment, SuggestionEvent structs
// com json:"snake_case" tags; DevNotes string `json:"dev_notes,omitempty"` (omitido p/ autor no handler).
type Suggestions struct{ pool *pgxpool.Pool }
func NewSuggestions(pool *pgxpool.Pool) *Suggestions { return &Suggestions{pool: pool} }
```

- [ ] **Step 2: Métodos (assinaturas fixas — usar EXATAMENTE estes nomes):**
  - `Create(ctx, CreateSuggestionInput) (*Suggestion, error)` — INSERT ... RETURNING; grava event `created`.
  - `List(ctx, ListSuggestionsFilter) ([]Suggestion, error)` — filtro dinâmico (status/type/priority/author/q); quando `filter.OnlyAuthorID != nil` força `created_by = $n`; ORDER configurável (default `updated_at DESC`).
  - `Get(ctx, id uuid.UUID) (*Suggestion, error)`.
  - `Update(ctx, id, UpdateSuggestionInput) (*Suggestion, error)` — partial update por ponteiros (padrão `users.go:118`); seta `resolved_at` quando status→concluida/recusada; grava events apropriados; `updated_at = NOW()`.
  - `ListComments(ctx, sid) ([]SuggestionComment, error)`; `AddComment(ctx, sid, authorID, body) (*SuggestionComment, error)`.
  - `ListAttachments(ctx, sid) ([]SuggestionAttachment, error)`; `AddAttachment(ctx, in AddAttachmentInput) (*SuggestionAttachment, error)`; `GetAttachment(ctx, id) (*SuggestionAttachment, error)`.
  - `ListEvents(ctx, sid) ([]SuggestionEvent, error)`; `addEvent(ctx, tx/pool, ...)` helper interno.
  - `MarkRead(ctx, userID, sid)` — upsert `ON CONFLICT (user_id, suggestion_id) DO UPDATE`.
  - `Summary(ctx) (SuggestionSummary, error)` — contagem por status + `oldest_open` (min created_at onde status ∉ concluida/recusada).
  - `UnreadCount(ctx, userID uuid.UUID, isDev bool) (int, error)` — dev: sugestões com atividade (comment/event) após last_read; autor: as próprias com atividade do dev após last_read.

- [ ] **Step 3: Testes de repo** (contra `rc-test-pg` — memória `test-db-native-pg-shadows-docker`). Cobrir: Create gera `ref_num` + event; List com `OnlyAuthorID` só devolve do autor; Update concluida seta `resolved_at`; AddComment; UnreadCount cresce após comment de outro e zera após MarkRead.

- [ ] **Step 4:** `cd workers && go test ./internal/catalog -run Suggestion -v` → PASS.
- [ ] **Step 5: Commit.**

---

### Task 4: Handler `handlers/suggestions.go`

**Files:** Create `workers/internal/api/handlers/suggestions.go`, `workers/internal/api/handlers/suggestions_test.go`

- [ ] **Step 1: Struct + gating helper.**

```go
type SuggestionsHandler struct {
    Repo     *catalog.Suggestions
    Users    *users.Repo      // p/ carregar email
    Storage  *storage.Client
    DevEmail string           // config.SuggestionsDevEmail
}

// isDev carrega o email do usuário do JWT (que NÃO traz email) e compara.
func (h *SuggestionsHandler) isDev(ctx context.Context) (bool, uuid.UUID, error) {
    claims, _ := auth.ClaimsFromContext(ctx)
    u, err := h.Users.Get(ctx, claims.UserID)
    if err != nil { return false, uuid.Nil, err }
    return strings.EqualFold(u.Email, h.DevEmail), claims.UserID, nil
}
```

- [ ] **Step 2: Handlers** (usar `writeJSON`; listas em `{"data":[...]}`):
  - `Create` — decode DTO, valida (title/description não-vazios, type/priority no allow-list → 422 com `{"errors":...}`), `Repo.Create`, 201.
  - `List` — `isDev`; se não-dev, força `filter.OnlyAuthorID = &uid`; parse query params; retorna `{"data":..., "unread": {...opcional}}`.
  - `Get` — carrega; se não-dev e `created_by != uid` → 403; monta resposta com comments/attachments(+presigned)/events; **zera `DevNotes` se não-dev**; dispara `MarkRead` (async ok).
  - `Patch` — **dev-only** (senão 403); decode ponteiros; `Repo.Update`.
  - `AddComment` — autor(own)/dev; `Repo.AddComment`; se o autor comentou e `awaiting_author` → limpar (Update).
  - `UploadAttachment` — multipart (`http.MaxBytesReader`, allow-list MIME imagem — espelhar `detections_manual_batch.go`), `Storage.Put` key `suggestions/{sid}/{uuid}.{ext}`, `Repo.AddAttachment`.
  - `AttachmentURL` — autor(own)/dev; `Storage.PresignGet(key, 5*time.Minute)`.
  - `MarkRead` — upsert.
  - `Summary` — **dev-only**.
  - `UnreadCount` — `isDev` → `Repo.UnreadCount(uid, isDev)`.

- [ ] **Step 3: Testes de gating** (os que mais importam):
  - autor NÃO consegue `Get` sugestão de outro → 403.
  - `dev_notes` ausente no JSON quando o caller é autor; presente quando é dev.
  - `Patch` por não-dev → 403.
  - `List` de autor só traz as próprias.

- [ ] **Step 4:** `go test ./internal/api/handlers -run Suggestion -v` → PASS.
- [ ] **Step 5: Commit.**

---

### Task 5: Wiring (router + main)

**Files:** Modify `workers/internal/api/router.go`, `workers/cmd/api/main.go`

- [ ] **Step 1: `router.go`** — adicionar `Suggestions *handlers.SuggestionsHandler` em `Deps`; dentro do grupo `RequireRole("admin","operator")` registrar (⚠️ não usar `r.Route` que mascare grupos — seguir o comentário do arquivo):

```go
if d.Suggestions != nil {
    r.Post("/suggestions", d.Suggestions.Create)
    r.Get("/suggestions", d.Suggestions.List)
    r.Get("/suggestions/summary", d.Suggestions.Summary)          // handler barra não-dev
    r.Get("/suggestions/unread-count", d.Suggestions.UnreadCount)
    r.Get("/suggestions/{id}", d.Suggestions.Get)
    r.Patch("/suggestions/{id}", d.Suggestions.Patch)              // handler barra não-dev
    r.Post("/suggestions/{id}/comments", d.Suggestions.AddComment)
    r.Post("/suggestions/{id}/attachments", d.Suggestions.UploadAttachment)
    r.Post("/suggestions/{id}/read", d.Suggestions.MarkRead)
    r.Get("/suggestions/attachments/{aid}/url", d.Suggestions.AttachmentURL)
}
```
⚠️ Registrar `/suggestions/summary` e `/unread-count` ANTES de `/suggestions/{id}` (chi casa rota estática antes da param, mas manter a ordem explícita evita surpresa).

- [ ] **Step 2: `main.go`** — `sugRepo := catalog.NewSuggestions(pool)`; injetar `&handlers.SuggestionsHandler{Repo: sugRepo, Users: usersRepo, Storage: s3Client, DevEmail: cfg.SuggestionsDevEmail}` em `Deps`.
- [ ] **Step 3: Cross-compile (CLAUDE.md §6.1):** `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` → PASS.
- [ ] **Step 4: Commit.**

---

## Phase B — Frontend (funcional)

### Task 6: Hooks em `api/hooks.js`

- [ ] **Step 1:** adicionar (espelhar `useStations`/`useCreateStation`):
  `useSuggestions(params)`, `useSuggestion(id)`, `useCreateSuggestion()`, `useUpdateSuggestion()`, `useAddSuggestionComment()`, `useUploadSuggestionAttachment()` (multipart header), `useMarkSuggestionRead()`, `useSuggestionsSummary()`, `useSuggestionsUnread()`. Invalidar queryKeys `['suggestions']` / `['suggestion', id]` no onSuccess.
- [ ] **Step 2: Commit.**

### Task 7: Sidebar + rota

- [ ] **Step 1: `Sidebar.jsx`** — `IconSuggestions()` (SVG no padrão, `className="sidebar-icon"`); dentro de `AdminNav`, bloco Administração, `<SidebarLink to="/admin/suggestions" icon={<IconSuggestions/>}>Sugestões</SidebarLink>` com badge de não-lido (usar `useSuggestionsUnread`).
- [ ] **Step 2: `App.jsx`** — import lazy da página + `<Route path="/admin/suggestions" element={<RequireRole roles={['admin']}><AdminSuggestionsPage/></RequireRole>} />` ANTES do catch-all.
- [ ] **Step 3:** `cd frontend && npm run build` → PASS. Commit.

### Task 8: Página + persona branch + pills

- [ ] **Step 1: `AdminSuggestionsPage.jsx`** — `const devEmail = import.meta.env.VITE_SUGGESTIONS_DEV_EMAIL || 'tatico3@hubradios.com'`; `const isDev = useAuth().user?.email?.toLowerCase() === devEmail.toLowerCase()`; renderiza `<CentralDeComando/>` ou `<MinhasSugestoes/>`.
- [ ] **Step 2: `SuggestionPills.jsx`** — `StatusPill`, `TypePill`, `PriorityPill` (reusar `.badge*`).
- [ ] **Step 3: Commit.**

### Task 9: `MinhasSugestoes` (autor)

- [ ] **Step 1:** lista de cards (`useSuggestions`), botão "Nova sugestão" → `SuggestionCreateModal`, detalhe → `SuggestionDetail` (feedback do dev em destaque + `SuggestionThread`). Empty state.
- [ ] **Step 2: `SuggestionCreateModal.jsx`** — form (título/tipo/tela/descrição/prioridade) + `ClipboardPasteZone`. Submit via `useCreateSuggestion` + upload de anexos.
- [ ] **Step 3:** build + commit.

### Task 10: `CentralDeComando` (dev)

- [ ] **Step 1:** header KPIs (`useSuggestionsSummary`), filtros/busca, lista densa; triage inline (status/prioridade via `RSelect`/menu → `useUpdateSuggestion`); toggle Board (colunas por status); "Nova demanda".
- [ ] **Step 2:** `SuggestionDetail` como drawer com campos de gestão (dev_priority, effort, dev_notes, dev_feedback) + thread + timeline (`ListEvents`).
- [ ] **Step 3:** build + commit.

### Task 11: Thread, lightbox, clipboard, unread

- [ ] **Step 1: `SuggestionThread.jsx`** — lista de comments + caixa de resposta (`useAddSuggestionComment`), anexos inline.
- [ ] **Step 2: `AttachmentLightbox.jsx`** — abre imagem via presigned URL.
- [ ] **Step 3: `ClipboardPasteZone.jsx`** — `onPaste` captura `image/*` do clipboard → vira anexo.
- [ ] **Step 4:** `useMarkSuggestionRead` no open do detalhe; badges de não-lido por card/linha.
- [ ] **Step 5:** build + commit.

---

## Phase C — Visual (skills de design)

### Task 12: Refino visual

- [ ] **Step 1:** invocar **/impeccable** e **/pro-system-ui** sobre a página inteira: hierarquia sem empilhamento, board+lista coexistindo, timeline "contada", micro-motion `#E81E75`, empty states, colar-print polido, zero AI-slop. Respeitar tokens do `index.css`.
- [ ] **Step 2:** build + verificação visual (Playwright/print) + commit.

---

## Phase D — Docs

### Task 13: Documentação

- [ ] **Step 1: `docs/features/suggestions-board.md`** com header YAML (`status: implementado`, `ultima-verificacao: 2026-07-09`, `codigo-relacionado:` migration+repo+handler+página), personas, gating por email, modelo de dados, endpoints, ciclo de status, decisões (env dev-email, YAGNI cliente).
- [ ] **Step 2: `CLAUDE.md`** — linha no mapa de consulta: "Sugestões / central de demandas do dev → docs/features/suggestions-board.md".
- [ ] **Step 3: Commit.**

---

## Phase E — Verify (CLAUDE.md §6)

### Task 14: Prova de que não quebra

- [ ] **Step 1:** `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` → PASS (§6.1).
- [ ] **Step 2:** `cd workers && go test ./...` — distinguir falha nova de flaky conhecido (§6.6: `catalog TestBuildDailySummary_WithDowntime` antes de ~13:00 UTC).
- [ ] **Step 3:** `cd frontend && npm run build` → PASS (sem tocar `package*.json`, §5 não se aplica).
- [ ] **Step 4:** verificação manual do fluxo (criar sugestão como admin → aparece na central do dev → dev muda status/dá feedback → autor vê feedback + thread).
- [ ] **Step 5:** finishing-a-development-branch (merge/PR).

---

## Self-Review (feito)

- **Cobertura do spec:** personas/gating (T3-T5,T8), 5 tabelas (T1), status+prioridade (T1,T3,T10), endpoints (T4-T5), imagens S3 (T4,T11), thread (T4,T11), notificação/unread (T3,T4,T7,T11), frontend 2 personas (T8-T11), visual (T12), docs (T13), deploy checklist (T14). ✔
- **Placeholders:** assinaturas de método fixadas por nome (Task 3/4); DDL completo; sem "TODO". ✔
- **Consistência de tipos:** nomes de método usados igual em repo (T3) e handler (T4); `isDev`, `OnlyAuthorID`, `dev_notes` consistentes. ✔
