# Sininho admin + filtro station-failures — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sininho de notificações no `/dashboard` (admin) listando campanhas com déficit nos últimos 7 dias com read-state persistido, e filtrar `/admin/station-failures` pra deixar de listar emissoras só-downtime.

**Architecture:** Migration nova (`0032_notification_reads`) com tabela `(user_id, notification_key)`. Repo de notificações deriva direto do `daily_play_summary` (sem materializar) + LEFT JOIN com `notification_reads`. 3 endpoints admin novos. Frontend tem `<NotificationBell>` no header do dashboard + `<NotificationPopover>` ancorado. Filtro de station-failures é mudança de 1 linha no WHERE da query SQL.

**Tech Stack:** Go 1.x (pgx, chi router, JWT auth via `auth.ClaimsFromContext`), React 19 + Vite + React Query v5, PostgreSQL. Sem testes automatizados de frontend (convenção do projeto).

**Spec:** [`docs/superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md`](../specs/2026-05-25-admin-notifications-and-failure-filter-design.md)

---

## Pré-requisitos

Em terminais separados, dev server e API rodando:

```bash
# terminal 1
cd frontend && npm run dev

# terminal 2 (após cada mudança no backend)
docker compose -f infra/docker/docker-compose.yml build api
docker compose -f infra/docker/docker-compose.yml up -d --force-recreate --no-deps api
```

Pra rodar migrations em dev:

```bash
docker compose -f infra/docker/docker-compose.yml exec api migrate -path /migrations -database "$DATABASE_URL" up
```

Pra testes Go que requerem DB:

```bash
cd workers
TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:5432/radiocheck?sslmode=disable" go test ./internal/catalog/...
```

(Tests sem `TEST_DATABASE_URL` skipam automaticamente os que precisam de DB — `testhelpers_test.go:19`.)

---

## Phase 1 — Filtrar /admin/station-failures (só deficit)

### Task 1: SQL filter + atualizar testes

**Files:**
- Modify: `workers/internal/catalog/station_failures.go` (linha ~149, query da `ListForDate`)
- Modify: `workers/internal/catalog/station_failures_test.go` (adicionar caso explícito)

- [ ] **Step 1: Mudar o WHERE da query principal**

Em `station_failures.go`, achar:

```go
WHERE (d.station_id IS NOT NULL OR df.station_id IS NOT NULL)
ORDER BY COALESCE(d.down_sec, 0) DESC, COALESCE(df.aff_camp, 0) DESC, s.name ASC`,
```

Trocar por:

```go
WHERE df.station_id IS NOT NULL
ORDER BY COALESCE(d.down_sec, 0) DESC, COALESCE(df.aff_camp, 0) DESC, s.name ASC`,
```

Comentário acima da query (procurar e atualizar se houver):

Adicionar comentário antes do `rows, err := r.pool.Query(...`:

```go
	// Spec 2026-05-25: só listamos estações com deficit > 0. Estações com
	// downtime mas sem inserção perdida (nenhuma campanha programada na
	// janela do down) somem da lista — eram ruído pro operador. Estações
	// com deficit + downtime continuam mostrando ambos os números.
```

- [ ] **Step 2: Atualizar o doc comment do método `ListForDate`**

Achar (linha ~113):

```go
// ListForDate returns all stations that had a failure (stream-down OR
// silent-gap) on the given local-day, with the campaigns whose slots were
// lost. minDownSeconds filters out tiny down events (default 60).
```

Trocar por:

```go
// ListForDate returns stations that lost campaign inserções (deficit > 0)
// on the given local-day, with the campaigns whose slots were lost.
// Stations with only downtime (no deficit) are excluded — they were noise
// for the operator (spec 2026-05-25). minDownSeconds still filters tiny
// down events (default 60) when computing the down_sec of qualifying
// stations.
```

- [ ] **Step 3: Adicionar caso de teste explícito**

Em `station_failures_test.go`, adicionar no fim do arquivo:

```go
// Regression: post spec 2026-05-25, estações com só downtime (sem deficit)
// não devem aparecer no listing. As com deficit continuam aparecendo, com
// ou sem downtime.
func TestStationFailures_ListForDate_OnlyDowntime_Excluded(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewStationFailures(pool)

	// Janela de teste: data bem antiga que não terá fixtures de prod
	// interferindo. O teste passa por construção (sem inserts) se a query
	// retornar 0 estações, o que é o comportamento esperado da migração
	// inicial (fixtures triviais).
	farPast := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	res, err := repo.ListForDate(ctx, farPast, 60)
	if err != nil {
		t.Fatalf("ListForDate: %v", err)
	}
	if len(res.Stations) != 0 {
		t.Errorf("expected 0 stations in far past, got %d", len(res.Stations))
	}
}
```

> **Nota:** o codebase não tem fixture-seeding sofisticado nos testes de repo; o que existe é majoritariamente `farPast → expect 0` e tests de funções puras (`crossesAny`). Esse teste é a regressão mínima — confirma que a query não quebra. A validação real do filtro acontece no smoke manual (Phase 5).

- [ ] **Step 4: Build + rodar testes**

```bash
cd workers
go build ./...
TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:5432/radiocheck?sslmode=disable" go test ./internal/catalog/ -run TestStationFailures -v
```

Esperado:
- `go build` sem erro.
- Tests passam (ou skipam se sem DB).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/station_failures.go workers/internal/catalog/station_failures_test.go
git commit -m "fix(station-failures): listar só emissoras com déficit

Estações que só tiveram downtime (sem inserção perdida) saíam no listing
do /admin/station-failures como ruído. Filtra agora pra exigir deficit > 0;
estações com deficit + downtime continuam mostrando ambos os números."
```

---

## Phase 2 — Backend de notificações

### Task 2: Migration 0032

**Files:**
- Create: `migrations/0032_notification_reads.up.sql`
- Create: `migrations/0032_notification_reads.down.sql`

- [ ] **Step 1: Criar a migration up**

`migrations/0032_notification_reads.up.sql`:

```sql
-- 0032_notification_reads.up.sql
-- Per-user read-state de notificações (sininho do /dashboard admin).
-- Spec: docs/superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md

BEGIN;

CREATE TABLE notification_reads (
    user_id          UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notification_key TEXT         NOT NULL,
    read_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, notification_key)
);

CREATE INDEX idx_notification_reads_user ON notification_reads(user_id);

COMMIT;
```

- [ ] **Step 2: Criar a migration down**

`migrations/0032_notification_reads.down.sql`:

```sql
BEGIN;
DROP TABLE IF EXISTS notification_reads;
COMMIT;
```

- [ ] **Step 3: Aplicar a migration em dev**

```bash
docker compose -f infra/docker/docker-compose.yml exec api migrate -path /migrations -database "$DATABASE_URL" up
```

Confirmar:

```bash
docker compose -f infra/docker/docker-compose.yml exec postgres psql -U radiocheck -d radiocheck -c "\d notification_reads"
```

Esperado: tabela existe com 3 colunas + PK composta + índice.

- [ ] **Step 4: Commit**

```bash
git add migrations/0032_notification_reads.up.sql migrations/0032_notification_reads.down.sql
git commit -m "feat(db): migration 0032_notification_reads

Tabela per-user de notification reads, com PK composta (user_id, key)
pra upserts idempotentes. Base do sininho do /dashboard admin."
```

---

### Task 3: Catalog repo (`catalog/notifications.go`)

**Files:**
- Create: `workers/internal/catalog/notifications.go`

- [ ] **Step 1: Criar o arquivo com structs e construtor**

`workers/internal/catalog/notifications.go`:

```go
package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Notification é um item do sininho. Por enquanto só o kind
// "campaign_failure" existe — schema é genérico pra crescimento futuro.
type Notification struct {
	Key           string     `json:"key"`
	Kind          string     `json:"kind"`
	CampaignID    uuid.UUID  `json:"campaign_id"`
	CampaignName  string     `json:"campaign_name"`
	ClientID      uuid.UUID  `json:"client_id"`
	ClientName    string     `json:"client_name"`
	ClientLogoURL string     `json:"client_logo_url"`
	OccurredOn    string     `json:"occurred_on"` // YYYY-MM-DD
	ReadAt        *time.Time `json:"read_at"`
}

type NotificationsResult struct {
	Items       []Notification `json:"items"`
	UnreadCount int            `json:"unread_count"`
}

type Notifications struct {
	pool *pgxpool.Pool
}

func NewNotifications(pool *pgxpool.Pool) *Notifications {
	return &Notifications{pool: pool}
}
```

- [ ] **Step 2: Adicionar `List` (lista da janela de 7 dias com read state)**

Append ao arquivo:

```go
// List retorna notificações dos últimos 7 dias (dias civis), com read_at
// preenchido pra cada item que o user já leu. Ordem: data mais recente
// primeiro, alfabético por campanha como tiebreaker. Limite hard de 50
// itens — improvável estourar em 7 dias.
//
// Source: daily_play_summary.deficit > 0, agrupado por (campaign, day).
// Campanhas com status='cancelada' são excluídas. Campanhas bonificadas
// continuam aparecendo (não filtramos — operador decide).
func (n *Notifications) List(ctx context.Context, userID uuid.UUID) (*NotificationsResult, error) {
	rows, err := n.pool.Query(ctx, `
SELECT
    'campaign_failure:' || c.id::text || ':' || dps.for_date::text AS key,
    c.id, c.name,
    cl.id, COALESCE(cl.name, '—'), COALESCE(cl.logo_url, ''),
    dps.for_date,
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
LIMIT 50`, userID)
	if err != nil {
		return nil, fmt.Errorf("notifications.List query: %w", err)
	}
	defer rows.Close()

	result := &NotificationsResult{Items: []Notification{}}
	for rows.Next() {
		var n Notification
		var occurred time.Time
		var readAt *time.Time
		if err := rows.Scan(
			&n.Key, &n.CampaignID, &n.CampaignName,
			&n.ClientID, &n.ClientName, &n.ClientLogoURL,
			&occurred, &readAt,
		); err != nil {
			return nil, fmt.Errorf("notifications.List scan: %w", err)
		}
		n.Kind = "campaign_failure"
		n.OccurredOn = occurred.Format("2006-01-02")
		n.ReadAt = readAt
		if readAt == nil {
			result.UnreadCount++
		}
		result.Items = append(result.Items, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notifications.List rows: %w", err)
	}
	return result, nil
}
```

- [ ] **Step 3: Adicionar `MarkRead` (upsert múltiplo)**

Append:

```go
// MarkRead faz upsert de (user_id, key, NOW()) pra cada key. Idempotente
// graças ao ON CONFLICT DO NOTHING. Retorna quantos foram efetivamente
// inseridos (zero quando todos já estavam marcados).
func (n *Notifications) MarkRead(ctx context.Context, userID uuid.UUID, keys []string) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	tag, err := n.pool.Exec(ctx, `
INSERT INTO notification_reads (user_id, notification_key)
SELECT $1, k
FROM UNNEST($2::text[]) AS k
ON CONFLICT (user_id, notification_key) DO NOTHING`, userID, keys)
	if err != nil {
		return 0, fmt.Errorf("notifications.MarkRead: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
```

- [ ] **Step 4: Adicionar `MarkAllReadInWindow` (deriva keys server-side)**

Append:

```go
// MarkAllReadInWindow deriva os keys da janela atual (mesma da List) e
// faz upsert pra todos. Não confia em lista vinda do cliente — evita o
// case de "marquei tudo via um endpoint cego" deixando reads órfãos pro
// resto da eternidade.
func (n *Notifications) MarkAllReadInWindow(ctx context.Context, userID uuid.UUID) (int, error) {
	tag, err := n.pool.Exec(ctx, `
INSERT INTO notification_reads (user_id, notification_key)
SELECT $1, 'campaign_failure:' || c.id::text || ':' || dps.for_date::text
FROM daily_play_summary dps
JOIN campaigns c ON c.id = dps.campaign_id
WHERE dps.for_date >= (CURRENT_DATE - INTERVAL '7 days')
  AND dps.for_date <= CURRENT_DATE
  AND dps.deficit > 0
  AND c.status != 'cancelada'
GROUP BY c.id, dps.for_date
ON CONFLICT (user_id, notification_key) DO NOTHING`, userID)
	if err != nil {
		return 0, fmt.Errorf("notifications.MarkAllReadInWindow: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
```

- [ ] **Step 5: Build**

```bash
cd workers && go build ./...
```

Esperado: sem erro.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/catalog/notifications.go
git commit -m "feat(catalog): Notifications repo (List + MarkRead + MarkAllReadInWindow)

List deriva de daily_play_summary.deficit > 0 nos últimos 7 dias, com
LEFT JOIN em notification_reads pra trazer read_at. MarkRead faz upsert
idempotente; MarkAllReadInWindow deriva os keys server-side pra evitar
inserções órfãs."
```

---

### Task 4: Tests do catalog repo

**Files:**
- Create: `workers/internal/catalog/notifications_test.go`

- [ ] **Step 1: Criar tests básicos (skip sem DB)**

`workers/internal/catalog/notifications_test.go`:

```go
package catalog

import (
	"testing"

	"github.com/google/uuid"
)

func TestNotifications_List_EmptyForUnknownUser(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewNotifications(pool)

	// UUID aleatório → não existe na users table, sem reads, sem matches.
	// Mesmo que haja failures recentes em prod, este user vê tudo como
	// não-lido — mas só checamos que não dá erro e retorna estrutura
	// não-nula.
	someUser := uuid.New()
	res, err := repo.List(ctx, someUser)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.Items == nil {
		t.Error("expected non-nil Items (even when empty)")
	}
	// UnreadCount não pode ser negativo
	if res.UnreadCount < 0 {
		t.Errorf("UnreadCount negativo: %d", res.UnreadCount)
	}
}

func TestNotifications_MarkRead_EmptyKeysIsNoop(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewNotifications(pool)

	someUser := uuid.New()
	n, err := repo.MarkRead(ctx, someUser, nil)
	if err != nil {
		t.Fatalf("MarkRead nil: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows on empty input, got %d", n)
	}

	n, err = repo.MarkRead(ctx, someUser, []string{})
	if err != nil {
		t.Fatalf("MarkRead empty: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows on empty slice, got %d", n)
	}
}

func TestNotifications_MarkRead_Idempotent(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewNotifications(pool)

	// Tem que existir um usuário real pra FK. Pegamos qualquer um.
	var userID uuid.UUID
	err := pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID)
	if err != nil {
		t.Skipf("no users in DB to FK against: %v", err)
	}

	key := "test_kind:test:" + uuid.NewString()

	// Cleanup
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx,
			`DELETE FROM notification_reads WHERE user_id = $1 AND notification_key = $2`,
			userID, key,
		)
	})

	// Primeira chamada: insere 1
	n, err := repo.MarkRead(ctx, userID, []string{key})
	if err != nil {
		t.Fatalf("first MarkRead: %v", err)
	}
	if n != 1 {
		t.Errorf("first call: expected 1 inserted, got %d", n)
	}

	// Segunda chamada com a mesma key: insere 0 (ON CONFLICT)
	n, err = repo.MarkRead(ctx, userID, []string{key})
	if err != nil {
		t.Fatalf("second MarkRead: %v", err)
	}
	if n != 0 {
		t.Errorf("second call: expected 0 inserted (conflict), got %d", n)
	}
}
```

- [ ] **Step 2: Rodar os tests**

```bash
cd workers
TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:5432/radiocheck?sslmode=disable" go test ./internal/catalog/ -run TestNotifications -v
```

Esperado: 3 tests passam (ou skipam se sem DB / sem users).

- [ ] **Step 3: Commit**

```bash
git add workers/internal/catalog/notifications_test.go
git commit -m "test(catalog): Notifications List + MarkRead

Cobre: List não panica pra user desconhecido; MarkRead([]) é noop;
MarkRead é idempotente em chamadas repetidas da mesma key."
```

---

### Task 5: HTTP handler

**Files:**
- Create: `workers/internal/api/handlers/admin_notifications.go`

- [ ] **Step 1: Criar arquivo com struct + interface**

`workers/internal/api/handlers/admin_notifications.go`:

```go
package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

// NotificationsRepo abstrai o catalog repo pra teste.
type NotificationsRepo interface {
	List(ctx context.Context, userID uuid.UUID) (*catalog.NotificationsResult, error)
	MarkRead(ctx context.Context, userID uuid.UUID, keys []string) (int, error)
	MarkAllReadInWindow(ctx context.Context, userID uuid.UUID) (int, error)
}

// NotificationsHandler powers /v1/internal/admin/notifications.
type NotificationsHandler struct {
	Repo NotificationsRepo
	Log  *slog.Logger
}
```

- [ ] **Step 2: Adicionar handler `List`**

Append:

```go
// List serves GET /admin/notifications.
func (h *NotificationsHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromCtx(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if h.Repo == nil {
		// Test harness sem DB — retorna vazio em vez de panicar.
		writeJSON(w, &catalog.NotificationsResult{Items: []catalog.Notification{}})
		return
	}
	res, err := h.Repo.List(r.Context(), userID)
	if err != nil {
		if h.Log != nil {
			h.Log.Error("notifications.list_failed", "error", err)
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, res)
}
```

- [ ] **Step 3: Adicionar handler `MarkRead`**

Append:

```go
type markReadReq struct {
	Keys []string `json:"keys"`
}

// MarkRead serves POST /admin/notifications/mark-read.
// Body: {"keys": ["campaign_failure:...:..."]}
func (h *NotificationsHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromCtx(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body markReadReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	// Cap defensivo: ninguém deveria marcar > 200 de uma vez.
	if len(body.Keys) > 200 {
		http.Error(w, "too many keys", http.StatusBadRequest)
		return
	}
	if h.Repo == nil {
		writeJSON(w, map[string]int{"marked": 0})
		return
	}
	n, err := h.Repo.MarkRead(r.Context(), userID, body.Keys)
	if err != nil {
		if h.Log != nil {
			h.Log.Error("notifications.mark_read_failed", "error", err)
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]int{"marked": n})
}
```

- [ ] **Step 4: Adicionar handler `MarkAllRead`**

Append:

```go
// MarkAllRead serves POST /admin/notifications/mark-all-read.
// No body. Server-side deriva os keys da janela atual.
func (h *NotificationsHandler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromCtx(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if h.Repo == nil {
		writeJSON(w, map[string]int{"marked": 0})
		return
	}
	n, err := h.Repo.MarkAllReadInWindow(r.Context(), userID)
	if err != nil {
		if h.Log != nil {
			h.Log.Error("notifications.mark_all_read_failed", "error", err)
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]int{"marked": n})
}
```

- [ ] **Step 5: Helpers `userIDFromCtx` e `writeJSON` — verificar se já existem**

```bash
grep -rn "func userIDFromCtx\|func writeJSON" workers/internal/api/handlers/ | head -5
```

Se **`writeJSON` já existe** (provavelmente sim — é comum), use o existente; senão adicione no fim do arquivo:

```go
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
```

Se **`userIDFromCtx` NÃO existe**, adicione no fim do arquivo:

```go
// userIDFromCtx extrai o UserID do JWT do request. Retorna ok=false
// quando não há claims (não deveria acontecer dentro de RequireJWT, mas
// é defensivo pra evitar zero-uuid passar adiante).
func userIDFromCtx(r *http.Request) (uuid.UUID, bool) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok || claims == nil {
		return uuid.Nil, false
	}
	if claims.UserID == uuid.Nil {
		return uuid.Nil, false
	}
	return claims.UserID, true
}
```

Se já existe `userIDFromCtx` em outro handler com assinatura compatível, importe ou só remova esse bloco — não duplique. Verifique:

```bash
grep -rn "claims.UserID" workers/internal/api/handlers/ | head -10
```

- [ ] **Step 6: Build**

```bash
cd workers && go build ./...
```

Esperado: sem erro.

- [ ] **Step 7: Commit**

```bash
git add workers/internal/api/handlers/admin_notifications.go
git commit -m "feat(api): NotificationsHandler (List + MarkRead + MarkAllRead)

3 endpoints admin: GET para listar; POST mark-read com lista de keys;
POST mark-all-read deriva os keys server-side. Cap defensivo de 200
keys por request. Nil-guard repo pra harness de teste."
```

---

### Task 6: Tests do handler (sem DB)

**Files:**
- Create: `workers/internal/api/handlers/admin_notifications_test.go`

- [ ] **Step 1: Criar testes de validação**

`workers/internal/api/handlers/admin_notifications_test.go`:

```go
package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

// withClaims monta um request com auth.Claims fake injetadas, equivalente
// ao que o middleware RequireJWT faria.
func withClaims(r *http.Request, userID uuid.UUID, role string) *http.Request {
	// Replicamos o keying interno do pacote auth via context.WithValue
	// somente pra testes do handler — não tem outra forma sem expor.
	// Se isso quebrar (chave privada), o teste vai mostrar como "unauthorized"
	// e dá pra adaptar pra usar middleware real.
	claims := &auth.Claims{UserID: userID, Role: role}
	// Como ClaimsFromContext lê da chave interna, precisamos usar uma
	// helper exposta. Se não existir, este teste vai precisar refatorar o
	// pacote auth pra expor `auth.WithClaims(ctx, *Claims)`. Por ora
	// chamamos uma helper inexistente — adiciona-se em auth/middleware.go
	// se necessário.
	ctx := auth.WithClaims(r.Context(), claims)
	return r.WithContext(ctx)
}

func TestNotificationsHandler_List_NilRepo_ReturnsEmpty(t *testing.T) {
	h := &NotificationsHandler{}

	req := httptest.NewRequest("GET", "/", nil)
	req = withClaims(req, uuid.New(), "admin")
	rr := httptest.NewRecorder()

	h.List(rr, req)
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"items"`) {
		t.Errorf("expected items in response, got: %s", rr.Body.String())
	}
}

func TestNotificationsHandler_List_NoClaims_Unauthorized(t *testing.T) {
	h := &NotificationsHandler{}

	req := httptest.NewRequest("GET", "/", nil)
	// Sem claims — handler tem que rejeitar.
	rr := httptest.NewRecorder()

	h.List(rr, req)
	if rr.Code != 401 {
		t.Errorf("want 401, got %d", rr.Code)
	}
}

func TestNotificationsHandler_MarkRead_BadJSON(t *testing.T) {
	h := &NotificationsHandler{}
	req := httptest.NewRequest("POST", "/", bytes.NewReader([]byte("not json")))
	req = withClaims(req, uuid.New(), "admin")
	rr := httptest.NewRecorder()

	h.MarkRead(rr, req)
	if rr.Code != 400 {
		t.Errorf("want 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestNotificationsHandler_MarkRead_TooManyKeys(t *testing.T) {
	h := &NotificationsHandler{}
	// Body com 201 keys
	keys := make([]string, 201)
	for i := range keys {
		keys[i] = "k"
	}
	body := `{"keys":[`
	for i, k := range keys {
		if i > 0 {
			body += ","
		}
		body += `"` + k + `"`
	}
	body += `]}`

	req := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(body)))
	req = withClaims(req, uuid.New(), "admin")
	rr := httptest.NewRecorder()

	h.MarkRead(rr, req)
	if rr.Code != 400 {
		t.Errorf("want 400 for >200 keys, got %d", rr.Code)
	}
}

// Garante que o handler compila e que a interface bate com o repo real.
// Sem isso, mudanças no shape da NotificationsRepo silenciosamente
// quebrariam só em runtime.
func TestNotificationsHandler_RepoInterface(t *testing.T) {
	_ = context.Background()
	var _ NotificationsRepo = (*catalog.Notifications)(nil)
}
```

- [ ] **Step 2: Verificar se `auth.WithClaims` existe — adicionar se não**

```bash
grep -n "WithClaims" workers/internal/auth/middleware.go
```

Se não existir, adicione em `workers/internal/auth/middleware.go` (no fim do arquivo):

```go
// WithClaims é a contraparte exportada de ClaimsFromContext, usada por
// tests de handler que precisam injetar claims sem passar pelo middleware
// JWT real. Em código de produção, RequireJWT é o único setter.
func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}
```

- [ ] **Step 3: Rodar os tests**

```bash
cd workers
go test ./internal/api/handlers/ -run TestNotificationsHandler -v
go test ./internal/auth/ -v
```

Esperado: todos os tests passam.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/api/handlers/admin_notifications_test.go workers/internal/auth/middleware.go
git commit -m "test(api): NotificationsHandler validation + interface check

- WithClaims helper adicionada no pacote auth pra permitir injetar
  claims em tests sem usar middleware real.
- Tests cobrem: nil-repo retorna vazio; sem claims → 401; JSON inválido
  → 400; >200 keys → 400; interface NotificationsRepo casa com o repo
  real do catalog."
```

---

### Task 7: Wire handler em main.go e router

**Files:**
- Modify: `workers/internal/api/router.go` (struct `Deps` + rotas)
- Modify: `workers/cmd/api/main.go` (construir handler)

- [ ] **Step 1: Adicionar campo no `Deps` do router**

Em `workers/internal/api/router.go`, achar:

```go
	Reports               *handlers.ReportsHandler
```

Adicionar imediatamente abaixo:

```go
	Reports               *handlers.ReportsHandler
	Notifications         *handlers.NotificationsHandler
```

- [ ] **Step 2: Registrar as 3 rotas**

Achar o bloco do `CampaignFailures` (linha ~377):

```go
				if d.CampaignFailures != nil {
					r.Group(func(r chi.Router) {
						r.Use(auth.RequireRole("admin"))
						r.Get("/admin/campaign-failures", d.CampaignFailures.GetList)
						r.Get("/admin/campaign-failures/{id}", d.CampaignFailures.GetByID)
					})
				}
```

Inserir DEPOIS desse bloco (antes do `}) // end admin/operator group`):

```go
				// /admin/notifications — sininho do dashboard admin.
				// Spec: docs/superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md
				if d.Notifications != nil {
					r.Group(func(r chi.Router) {
						r.Use(auth.RequireRole("admin"))
						r.Get("/admin/notifications", d.Notifications.List)
						r.Post("/admin/notifications/mark-read", d.Notifications.MarkRead)
						r.Post("/admin/notifications/mark-all-read", d.Notifications.MarkAllRead)
					})
				}
```

- [ ] **Step 3: Construir handler em main.go**

Em `workers/cmd/api/main.go`, achar:

```go
		CampaignFailures: &handlers.CampaignFailuresHandler{
			Repo: catalog.NewCampaignFailures(pool),
			Log:  logger,
		},
```

Adicionar DEPOIS desse bloco:

```go
		CampaignFailures: &handlers.CampaignFailuresHandler{
			Repo: catalog.NewCampaignFailures(pool),
			Log:  logger,
		},
		Notifications: &handlers.NotificationsHandler{
			Repo: catalog.NewNotifications(pool),
			Log:  logger,
		},
```

- [ ] **Step 4: Build + rebuild da API container**

```bash
cd workers && go build ./...
cd ..
docker compose -f infra/docker/docker-compose.yml build api
docker compose -f infra/docker/docker-compose.yml up -d --force-recreate --no-deps api
docker compose -f infra/docker/docker-compose.yml logs --tail=30 api
```

Esperado: api sobe sem erros, sem 500 nos logs.

- [ ] **Step 5: Verificação via curl**

```bash
# Pegue um JWT admin (do localStorage no browser, ou via /v1/auth/login)
TOKEN="<seu jwt admin>"

# List (espera 200 com items array)
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/v1/internal/admin/notifications | jq

# Mark-read com keys vazio (espera marked=0)
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"keys":[]}' http://localhost:8080/v1/internal/admin/notifications/mark-read | jq

# Mark-all (espera marked=N)
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/v1/internal/admin/notifications/mark-all-read | jq
```

- [ ] **Step 6: Commit**

```bash
git add workers/internal/api/router.go workers/cmd/api/main.go
git commit -m "feat(api): wire NotificationsHandler em router + main

3 rotas admin novas (GET list, POST mark-read, POST mark-all-read),
todas atrás de RequireRole('admin')."
```

---

## Phase 3 — Frontend (hooks + componentes + dashboard wiring)

### Task 8: Hooks novos em `api/hooks.js`

**Files:**
- Modify: `frontend/src/api/hooks.js` (adicionar 3 hooks)

- [ ] **Step 1: Localizar onde os hooks vivem**

```bash
grep -n "useCampaignFailures\|useQueryClient" frontend/src/api/hooks.js | head -5
```

- [ ] **Step 2: Adicionar os 3 hooks**

No fim do arquivo (após o último export), adicionar:

```js
// ─── Notificações (sininho /dashboard admin) ────────────────────────
// Spec: docs/superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md

export function useNotifications({ enabled = true } = {}) {
  return useQuery({
    queryKey: ['admin', 'notifications'],
    queryFn: () => api.get('/admin/notifications').then(r => r.data),
    refetchInterval: 60_000,
    staleTime: 30_000,
    enabled,
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

Se `useQueryClient`, `useMutation` ou `useQuery` não estiverem importados no topo do arquivo, adicione ao import existente de `@tanstack/react-query`.

- [ ] **Step 3: Verificação via console do browser**

Abra `/dashboard` no browser. Console DevTools:

```js
fetch('/v1/internal/admin/notifications', {
  headers: { Authorization: 'Bearer ' + localStorage.getItem('jwt') }
}).then(r => r.json()).then(console.log)
```

Esperado: `{items: [...], unread_count: N}`.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/api/hooks.js
git commit -m "feat(hooks): useNotifications + mark-read + mark-all-read

Polling de 60s no useNotifications. Mutations invalidam a query pra
refletir o read-state imediatamente."
```

---

### Task 9: `<ClientAvatar />` componente

**Files:**
- Create: `frontend/src/components/ClientAvatar.jsx`

- [ ] **Step 1: Verificar se já existe**

```bash
ls frontend/src/components/ClientAvatar.jsx 2>/dev/null
```

Se existir, pular essa task. Se não:

- [ ] **Step 2: Criar o componente**

`frontend/src/components/ClientAvatar.jsx`:

```jsx
/**
 * Avatar de cliente: se tem logo_url, mostra <img>; senão, mostra um
 * círculo cinza com a inicial do nome em branco. Usado no sininho de
 * notificações e onde mais precisar.
 *
 * Props:
 *  - client: { id, name, logo_url? }
 *  - size: pixels (default 32)
 */
export default function ClientAvatar({ client, size = 32 }) {
  const url = client?.logo_url || client?.client_logo_url
  const name = client?.name || client?.client_name || '?'
  const initial = name.trim().charAt(0).toUpperCase() || '?'

  const baseStyle = {
    width: size, height: size,
    borderRadius: '50%',
    flexShrink: 0,
    display: 'inline-flex',
    alignItems: 'center', justifyContent: 'center',
    overflow: 'hidden',
    background: 'var(--c-surface-2, #e2e8f0)',
    color: '#475569',
    fontWeight: 700,
    fontSize: Math.max(10, Math.floor(size * 0.42)),
    fontFamily: 'var(--font-heading)',
  }

  if (url) {
    return (
      <span style={baseStyle}>
        <img
          src={url}
          alt={name}
          style={{ width: '100%', height: '100%', objectFit: 'cover' }}
          onError={(e) => { e.currentTarget.style.display = 'none' }}
        />
      </span>
    )
  }

  return <span style={baseStyle} title={name}>{initial}</span>
}
```

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/ClientAvatar.jsx
git commit -m "feat(components): ClientAvatar com fallback de inicial

Logo redondo do cliente quando há logo_url, senão círculo cinza com a
inicial do nome. Tolera onError do <img> sumindo silenciosamente."
```

---

### Task 10: `<NotificationPopover />` componente

**Files:**
- Create: `frontend/src/components/NotificationPopover.jsx`

- [ ] **Step 1: Criar o componente**

`frontend/src/components/NotificationPopover.jsx`:

```jsx
import { useNavigate } from 'react-router-dom'
import { useNotifications, useMarkNotificationsRead, useMarkAllNotificationsRead } from '../api/hooks'
import ClientAvatar from './ClientAvatar'

/**
 * Popover do sininho. Lista até 50 notificações da janela de 7 dias.
 *
 * Spec: docs/superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md §4.4
 *
 * Props:
 *  - open: bool
 *  - onClose: () => void
 */
export default function NotificationPopover({ open, onClose }) {
  const navigate = useNavigate()
  const { data, isLoading } = useNotifications({ enabled: open })
  const markRead = useMarkNotificationsRead()
  const markAll  = useMarkAllNotificationsRead()

  if (!open) return null

  const items = data?.items ?? []
  const hasUnread = (data?.unread_count ?? 0) > 0

  function handleItemClick(item) {
    // Marca lido (otimismo via invalidação no onSuccess) e navega.
    if (!item.read_at) {
      markRead.mutate({ keys: [item.key] })
    }
    const params = new URLSearchParams({
      view: 'by_campaign',
      date: item.occurred_on,
      campaign: item.campaign_id,
    })
    navigate(`/admin/station-failures?${params.toString()}`)
    onClose()
  }

  function handleViewAll() {
    navigate('/admin/station-failures?view=by_campaign')
    onClose()
  }

  return (
    <>
      {/* Backdrop invisível pra fechar no click fora */}
      <div
        onClick={onClose}
        style={{
          position: 'fixed', inset: 0, zIndex: 60, background: 'transparent',
        }}
      />
      <div
        role="dialog"
        aria-label="Notificações"
        style={{
          position: 'absolute', top: 'calc(100% + 8px)', right: 0,
          width: 360, maxHeight: 480,
          background: 'var(--c-surface, #fff)',
          border: '1px solid var(--c-border, #e2e8f0)',
          borderRadius: 12,
          boxShadow: '0 12px 36px -8px rgba(15,23,42,0.25)',
          zIndex: 70,
          display: 'flex', flexDirection: 'column',
          overflow: 'hidden',
        }}
      >
        {/* Header */}
        <div style={{
          padding: '12px 16px',
          borderBottom: '1px solid var(--c-border, #e2e8f0)',
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          gap: 12,
        }}>
          <span style={{
            fontFamily: 'var(--font-heading)', fontWeight: 700,
            fontSize: 14, color: 'var(--c-text, #0f172a)',
          }}>
            Notificações
          </span>
          {hasUnread && (
            <button
              type="button"
              onClick={() => markAll.mutate()}
              disabled={markAll.isPending}
              style={{
                background: 'transparent', border: 0,
                color: 'var(--c-action, #e81e75)',
                fontSize: 11, fontWeight: 600,
                cursor: markAll.isPending ? 'wait' : 'pointer',
                fontFamily: 'var(--font-heading)',
              }}
            >
              Marcar todas como lidas
            </button>
          )}
        </div>

        {/* List */}
        <div style={{ flex: 1, overflowY: 'auto' }}>
          {isLoading && (
            <div style={{
              padding: '20px 16px',
              color: 'var(--c-text-3, #94a3b8)', fontSize: 12,
              textAlign: 'center',
            }}>
              Carregando…
            </div>
          )}
          {!isLoading && items.length === 0 && (
            <EmptyState />
          )}
          {!isLoading && items.length > 0 && items.map(item => (
            <NotificationRow
              key={item.key}
              item={item}
              onClick={() => handleItemClick(item)}
            />
          ))}
        </div>

        {/* Footer */}
        <div style={{
          padding: '10px 16px',
          borderTop: '1px solid var(--c-border, #e2e8f0)',
          display: 'flex', justifyContent: 'flex-end',
        }}>
          <button
            type="button"
            onClick={handleViewAll}
            style={{
              background: 'transparent', border: 0,
              color: 'var(--c-text-2, #475569)',
              fontSize: 12, fontWeight: 600,
              cursor: 'pointer',
              fontFamily: 'var(--font-heading)',
            }}
          >
            Ver tudo →
          </button>
        </div>
      </div>
    </>
  )
}

function NotificationRow({ item, onClick }) {
  const isUnread = !item.read_at
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        width: '100%',
        padding: '12px 16px',
        background: isUnread ? 'rgba(232,30,117,0.04)' : 'transparent',
        border: 0, borderBottom: '1px solid var(--c-border, #f1f5f9)',
        cursor: 'pointer',
        display: 'flex', alignItems: 'flex-start', gap: 12,
        textAlign: 'left',
        transition: 'background 100ms',
      }}
      onMouseEnter={e => {
        e.currentTarget.style.background = 'var(--c-bg, #f8fafc)'
      }}
      onMouseLeave={e => {
        e.currentTarget.style.background = isUnread ? 'rgba(232,30,117,0.04)' : 'transparent'
      }}
    >
      <ClientAvatar client={item} size={32} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{
          fontSize: 13, fontWeight: 600,
          color: 'var(--c-text, #0f172a)',
          fontFamily: 'var(--font-heading)',
          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
        }}>
          Campanha "{item.campaign_name}"
        </div>
        <div style={{
          marginTop: 2,
          fontSize: 11, color: 'var(--c-text-2, #475569)',
          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
        }}>
          {item.client_name} · {relativeDateLabel(item.occurred_on)}
        </div>
      </div>
      {isUnread && (
        <span
          aria-hidden="true"
          style={{
            width: 8, height: 8, borderRadius: '50%',
            background: 'var(--c-action, #e81e75)',
            flexShrink: 0, marginTop: 6,
          }}
        />
      )}
    </button>
  )
}

function EmptyState() {
  return (
    <div style={{
      padding: '40px 16px', textAlign: 'center',
      color: 'var(--c-text-3, #94a3b8)',
      fontSize: 12, lineHeight: 1.6,
    }}>
      <svg width="36" height="36" viewBox="0 0 24 24" fill="none"
           stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"
           strokeLinejoin="round" style={{ opacity: 0.6, marginBottom: 8 }}>
        <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" />
        <path d="M13.73 21a2 2 0 0 1-3.46 0" />
      </svg>
      <div>Nada por aqui — campanhas estão em dia.</div>
    </div>
  )
}

// "hoje (24/05)" / "ontem (23/05)" / "há 3 dias (22/05)"
function relativeDateLabel(iso) {
  if (!iso) return ''
  const [y, m, d] = iso.split('-').map(Number)
  const target = new Date(y, m - 1, d)
  target.setHours(0, 0, 0, 0)
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  const diff = Math.round((today - target) / 86400000)
  const dd = String(target.getDate()).padStart(2, '0')
  const mm = String(target.getMonth() + 1).padStart(2, '0')
  const abs = `(${dd}/${mm})`
  if (diff === 0) return `hoje ${abs}`
  if (diff === 1) return `ontem ${abs}`
  if (diff > 1) return `há ${diff} dias ${abs}`
  return `${dd}/${mm}`
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/NotificationPopover.jsx
git commit -m "feat(components): NotificationPopover

Popover ancorado abaixo-direita do bell. Lista com avatar do cliente,
nome da campanha, data relativa+absoluta, bullet pra não-lidas. Header
com 'Marcar todas como lidas'. Footer 'Ver tudo →' navega pra
/admin/station-failures?view=by_campaign. Click no item marca lido e
deep-linka pra drill-in da campanha/data."
```

---

### Task 11: `<NotificationBell />` componente

**Files:**
- Create: `frontend/src/components/NotificationBell.jsx`

- [ ] **Step 1: Criar o componente**

`frontend/src/components/NotificationBell.jsx`:

```jsx
import { useState } from 'react'
import { useNotifications } from '../api/hooks'
import NotificationPopover from './NotificationPopover'

/**
 * Sininho de notificações. Renderizado no header do /dashboard admin.
 *
 * Spec: docs/superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md §4.4
 */
export default function NotificationBell() {
  const [open, setOpen] = useState(false)
  // useNotifications faz polling sempre; o popover fecha mas mantém o
  // count atualizado pro badge.
  const { data } = useNotifications()
  const unread = data?.unread_count ?? 0
  const badgeLabel = unread > 9 ? '9+' : String(unread)

  return (
    <div style={{ position: 'relative' }}>
      <button
        type="button"
        onClick={() => setOpen(v => !v)}
        title="Notificações"
        aria-label={unread > 0 ? `${unread} notificações não-lidas` : 'Notificações'}
        style={{
          width: 36, height: 36,
          borderRadius: '50%',
          background: open ? 'var(--c-surface-2, #e2e8f0)' : 'transparent',
          border: '1px solid var(--c-border, #e2e8f0)',
          color: 'var(--c-text-2, #475569)',
          cursor: 'pointer',
          display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
          position: 'relative',
          transition: 'background 120ms, color 120ms',
        }}
        onMouseEnter={e => {
          if (!open) {
            e.currentTarget.style.background = 'var(--c-bg, #f8fafc)'
            e.currentTarget.style.color = 'var(--c-text, #0f172a)'
          }
        }}
        onMouseLeave={e => {
          if (!open) {
            e.currentTarget.style.background = 'transparent'
            e.currentTarget.style.color = 'var(--c-text-2, #475569)'
          }
        }}
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none"
             stroke="currentColor" strokeWidth="1.75" strokeLinecap="round"
             strokeLinejoin="round">
          <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" />
          <path d="M13.73 21a2 2 0 0 1-3.46 0" />
        </svg>
        {unread > 0 && (
          <span
            aria-hidden="true"
            style={{
              position: 'absolute',
              top: -3, right: -3,
              minWidth: 18, height: 18,
              padding: '0 5px',
              borderRadius: 9,
              background: '#dc2626',
              color: '#fff',
              fontSize: 10, fontWeight: 700,
              fontFamily: 'var(--font-heading)',
              display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
              boxShadow: '0 0 0 2px var(--c-surface, #fff)',
            }}
          >
            {badgeLabel}
          </span>
        )}
      </button>
      <NotificationPopover open={open} onClose={() => setOpen(false)} />
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/NotificationBell.jsx
git commit -m "feat(components): NotificationBell

Botão circular com ícone de sino. Badge vermelho com count quando há
não-lidas (cap em '9+'). Click toggla o popover."
```

---

### Task 12: Mount no /dashboard (admin)

**Files:**
- Modify: `frontend/src/pages/DashboardPage.jsx` (mount do `<NotificationBell>` no header)

- [ ] **Step 1: Identificar onde fica o header do dashboard admin**

```bash
grep -n "isAdmin\|AdminDash\|admin.dash\|dh-header\|dh-hero\|dh-top" frontend/src/pages/DashboardPage.jsx | head -20
```

Localizar visualmente onde o admin dashboard tem um header/hero superior. Há 2 variantes: client e admin.

- [ ] **Step 2: Importar o componente**

No topo de `DashboardPage.jsx`:

```jsx
import NotificationBell from '../components/NotificationBell'
```

- [ ] **Step 3: Renderizar o bell no header admin**

Identifique o ponto adequado no header admin (geralmente um header com nome do usuário ou um cluster top-right) e adicione:

```jsx
{isAdmin && (
  <div style={{
    position: 'absolute',
    top: 16, right: 24,
    zIndex: 10,
  }}>
    <NotificationBell />
  </div>
)}
```

> **Nota sobre posicionamento:** se o header admin já tem um container flex no topo (procure por classes `.dh-*`), prefira colocar o `<NotificationBell />` dentro desse container ao invés de usar `position: absolute`. O absolute acima é fallback caso não haja anchor natural.

- [ ] **Step 4: Verificação no browser**

1. Login como admin
2. `/dashboard` → ✅ sininho no canto superior direito
3. Se há notificações: ✅ badge vermelho com número
4. Click no sino → popover abre
5. Login como cliente (não-admin) → ✅ sininho **não** aparece

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/DashboardPage.jsx
git commit -m "feat(dashboard): mount NotificationBell pra admin

Renderizado só quando isAdmin. Posição top-right do dashboard."
```

---

### Task 13: Deep-link em /admin/station-failures (query params)

**Files:**
- Modify: `frontend/src/pages/AdminStationFailuresPage.jsx` (ler query params + init de state)

- [ ] **Step 1: Importar `useSearchParams`**

No topo de `AdminStationFailuresPage.jsx`:

```jsx
import { Link, useSearchParams } from 'react-router-dom'
```

- [ ] **Step 2: Ler query params no componente**

Achar o início do componente principal (procurar pelo `function AdminStationFailures...` ou similar; provavelmente próximo da linha 300 com `useState`). Antes dos `useState`, adicionar:

```jsx
  const [searchParams, setSearchParams] = useSearchParams()
  const qpView     = searchParams.get('view')      // 'by_campaign' | 'by_station' | null
  const qpDate     = searchParams.get('date')      // YYYY-MM-DD | null
  const qpCampaign = searchParams.get('campaign')  // uuid | null
```

- [ ] **Step 3: Initializar `useState` a partir dos params**

Achar os `useState` no início do componente:

```jsx
  const [date, setDate] = useState(isoYesterday())
  const [minDown, setMinDown] = useState(60)
  const [viewMode, setViewMode] = useState('by_station')
  const [subTab, setSubTab] = useState('daily')
  const [historyPage, setHistoryPage] = useState(1)
  const [drillCampaignId, setDrillCampaignId] = useState(null)
```

Trocar por:

```jsx
  const [date, setDate] = useState(qpDate || isoYesterday())
  const [minDown, setMinDown] = useState(60)
  const [viewMode, setViewMode] = useState(
    qpView === 'by_campaign' ? 'by_campaign' : 'by_station'
  )
  const [subTab, setSubTab] = useState('daily')
  const [historyPage, setHistoryPage] = useState(1)
  const [drillCampaignId, setDrillCampaignId] = useState(qpCampaign || null)
```

- [ ] **Step 4: Limpar os query params depois do mount**

Logo após os `useState`, adicionar:

```jsx
  // Limpa os query params que vieram do deep-link de notificação, pra
  // que navegações subsequentes (mudar de data, fechar drawer) não fiquem
  // referenciando a entrada antiga. Roda 1× no mount quando havia algum.
  useEffect(() => {
    if (qpView || qpDate || qpCampaign) {
      setSearchParams({}, { replace: true })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
```

Garanta `useEffect` no import:

```jsx
import { useMemo, useState, useEffect } from 'react'
```

- [ ] **Step 5: Verificação no browser**

1. Cole no browser: `http://localhost:3000/admin/station-failures?view=by_campaign&date=2026-05-20&campaign=<uuid-real>` (use um UUID de campanha válida)
2. ✅ Aba "Por campanha" abre selecionada
3. ✅ Data 2026-05-20 carregada
4. ✅ Drawer da campanha abre direto
5. URL fica limpa depois do mount (sem os query params)
6. Mudar de data manualmente continua funcionando

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/AdminStationFailuresPage.jsx
git commit -m "feat(station-failures): aceitar deep-link ?view=&date=&campaign=

Permite que o sininho de notificações navegue direto pro drill-in da
campanha+data específica. Query params são lidos no mount inicial e
limpos da URL pra não interferir em navegação subsequente."
```

---

## Phase 4 — Docs

### Task 14: Documentação nova + updates

**Files:**
- Create: `docs/features/admin-notifications.md`
- Modify: `docs/features/admin-station-failures.md` (nota da mudança de filtro)
- Modify: `CLAUDE.md` (linha do mapa de consulta — adicionar a nova feature)

- [ ] **Step 1: Criar `docs/features/admin-notifications.md`**

```markdown
---
status: implementado
ultima-verificacao: 2026-05-25
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
- **Janela:** últimos 7 dias civis (`CURRENT_DATE - INTERVAL '7 days'` até `CURRENT_DATE`).
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
```

- [ ] **Step 2: Atualizar `docs/features/admin-station-failures.md`**

Achar o header e atualizar `ultima-verificacao`:

```bash
grep -n "ultima-verificacao" docs/features/admin-station-failures.md
```

Trocar a linha pra:

```yaml
ultima-verificacao: 2026-05-25
```

Adicionar no fim do arquivo:

```markdown

## Mudança 2026-05-25: filtro por déficit

A listing de `/admin/station-failures` deixou de incluir emissoras com **só downtime** (sem `daily_play_summary.deficit > 0` naquele dia). Era ruído visual — operador via rádios que caíram fora de qualquer janela programada e não tinham impacto. Detalhe no spec [`2026-05-25-admin-notifications-and-failure-filter-design.md`](../superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md) §4.5.

Estações com deficit continuam aparecendo, com info de downtime quando aplicável.
```

- [ ] **Step 3: Atualizar `CLAUDE.md` (mapa de consulta)**

Achar a tabela "Quando você for mexer em…" e adicionar uma linha. Após a linha sobre admin-station-failures, adicionar:

```markdown
| Sininho de notificações (`/dashboard` admin) | [docs/features/admin-notifications.md](docs/features/admin-notifications.md) |
```

- [ ] **Step 4: Commit**

```bash
git add docs/features/admin-notifications.md docs/features/admin-station-failures.md CLAUDE.md
git commit -m "docs: sininho de notificações + nota da mudança em station-failures

Novo: docs/features/admin-notifications.md.
Atualizado: admin-station-failures.md (mudança 2026-05-25) + CLAUDE.md
(linha do mapa de consulta)."
```

---

## Phase 5 — Smoke E2E

### Task 15: Verificação manual ponta-a-ponta

Sem testes automatizados de frontend (convenção do projeto). Use este checklist:

- [ ] **Cenário canônico: notificação → click → drill-in**

1. Login como admin no `/dashboard`.
2. ✅ Sininho aparece no header (top-right).
3. Se há campanhas com déficit nos últimos 7 dias: ✅ badge vermelho com número.
4. Click no sininho → popover abre.
5. ✅ Lista mostra itens com (avatar do cliente, nome da campanha, "Cliente · ontem (DD/MM)", bullet rosa pras não-lidas).
6. Click no primeiro item.
7. ✅ Navega pra `/admin/station-failures?view=by_campaign&date=...&campaign=...`.
8. ✅ Aba "Por campanha" abre selecionada com a data correta.
9. ✅ Drawer da campanha abre direto.
10. Voltar pro `/dashboard`.
11. ✅ Badge diminuiu de 1 (item lido).
12. Reabrir popover → ✅ item visitado sem bullet.

- [ ] **Cenário "Marcar todas como lidas"**

1. Com badge mostrando >0, abre popover.
2. Click em "Marcar todas como lidas" no header do popover.
3. ✅ Badge zera.
4. ✅ Bullets somem da lista.
5. Recarregar página → ✅ badge continua zerado.

- [ ] **Cenário "Cliente não vê o sininho"**

1. Login como usuário cliente (não-admin).
2. `/dashboard` → ✅ NENHUM sininho aparece.

- [ ] **Cenário "Estação só-downtime some de /admin/station-failures"**

1. Login admin → `/admin/station-failures`.
2. Selecione uma data onde antes havia estações com só downtime no listing (consulte git log antes da mudança se precisar).
3. ✅ Essas estações não aparecem mais.
4. Estações com deficit continuam aparecendo.
5. Estação com deficit + downtime: continua mostrando o downtime no card.

- [ ] **Cenário "Persistência entre admins"**

1. Login como admin A → marca tudo como lido → logout.
2. Login como admin B → ✅ vê notificações como NÃO lidas (read state é por user).

- [ ] **Commit (se algum ajuste foi necessário)**

Se algo do smoke falhou e exigiu correção, fix inline e commit. Senão, pular pro fim do plan.

---

## Self-Review

**1. Spec coverage:**
- §2 decisões fechadas → Phase 1+2+3 implementa todas (audience, janela, granularidade, read state, mark behavior, polling) ✓
- §3 não-objetivos → respeitados (sem cliente, sem realtime, sem agrupamento, sem filtros UI, sem outros kinds, sem retenção) ✓
- §4.1 schema → Task 2 ✓
- §4.2 endpoints (GET, POST mark-read, POST mark-all-read) → Tasks 5, 7 ✓
- §4.3 query SQL → Task 3 ✓
- §4.4 NotificationBell + NotificationPopover + hooks → Tasks 8, 10, 11 ✓
- §4.4 roteamento `?campaign=&date=&view=` → Task 13 ✓
- §4.5 filtro station-failures → Task 1 ✓
- §5 cenário canônico → Task 15 smoke ✓
- §6 edge cases → cobertos: 6.1/6.2 pela query estar sempre filtrando status/janela; 6.3 pelo ON CONFLICT; 6.4 pelo GROUP BY; 6.5 pelo EmptyState; 6.6 pelo LIMIT 50 ✓
- §7 verificações → 7.1 Task 13; 7.2 já confirmado (users table existe); 7.3 Task 5 step 5; 7.4 Task 12 step 1 ✓
- §8 aceitação → coberta por Task 15 ✓
- §9 arquivos tocados → bate ✓
- §10 docs → Task 14 ✓

**2. Placeholder scan:** nenhum "TBD" / "TODO" / "fill in". Os "verificar runtime" são tasks ativas com fallback claro (ex: `userIDFromCtx` — grep, se existe usa, senão adiciona).

**3. Type consistency:**
- `NotificationsResult` → mesma shape em catalog repo, handler, hook (`items[]`, `unread_count`) ✓
- `Notification` fields (Key, Kind, CampaignID, CampaignName, ClientID, ClientName, ClientLogoURL, OccurredOn, ReadAt) → Go struct e JSON tags consistentes; frontend usa `item.key`, `item.campaign_name`, `item.campaign_id`, `item.client_name`, `item.client_logo_url`, `item.occurred_on`, `item.read_at` → batem com os JSON tags ✓
- Interface `NotificationsRepo` (List, MarkRead, MarkAllReadInWindow) → bate com `catalog.Notifications` métodos (Task 6 step 1 tem `var _ NotificationsRepo = (*catalog.Notifications)(nil)` pra garantir) ✓
- Query param names (`view`, `date`, `campaign`) → consistentes entre popover (Task 10) e station-failures page (Task 13) ✓
