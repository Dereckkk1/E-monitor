# Modal de resumo de falhas do dia anterior — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Modal admin-only que aparece 1x/dia/usuário no primeiro load do app, resume as campanhas que falharam ontem (com contagem de emissoras por campanha) e leva pra `/admin/station-failures`.

**Architecture:** 2 endpoints novos finos (`GET`/`POST ack`) que reusam `catalog.CampaignFailures.ListForDate(ontem)` pros dados e a tabela `notification_reads` pra persistir "visto por usuário" (zero migration). No frontend, um `<DailyFailuresModal/>` montado uma vez no `AppShell`, auto-gateado em admin, que busca o digest e renderiza só se houver falhas não-vistas.

**Tech Stack:** Go (chi, pgx), React (React Query, react-router), CSS tokens existentes do design system.

**Spec:** [docs/superpowers/specs/2026-06-18-daily-failures-digest-modal-design.md](../specs/2026-06-18-daily-failures-digest-modal-design.md)

---

## File Structure

**Backend (Go, `workers/`):**
- Create: `internal/api/handlers/admin_daily_failures_digest.go` — handler novo + tipos de response + reducer puro `digestFromDaily` + interfaces de repo.
- Create: `internal/api/handlers/admin_daily_failures_digest_test.go` — testes do reducer e do handler (com fakes, sem DB).
- Modify: `internal/catalog/notifications.go` — adiciona método `HasRead`.
- Modify: `internal/api/router.go` — registra as 2 rotas no grupo admin.
- Modify: `internal/api/router.go` (struct `Deps`) — novo campo `DailyFailuresDigest`.
- Modify: `cmd/api/main.go` — instancia e injeta o handler.

**Frontend (React, `frontend/`):**
- Modify: `src/api/hooks.js` — `useDailyFailuresDigest` + `useAckDailyFailuresDigest`.
- Create: `src/components/DailyFailuresModal.jsx` — o componente.
- Create: `src/components/DailyFailuresModal.css` — estilos (reusa tokens).
- Modify: `src/App.jsx` — monta `<DailyFailuresModal/>` no `AppShell`.

**Docs:**
- Create: `docs/features/daily-failures-digest-modal.md`.
- Modify: `CLAUDE.md` — linha no mapa de consulta.

**Padrões já existentes que este plano espelha (não reinventar):**
- Handler com fake repo + `userIDFromReq` + `writeJSON` + guard `Repo == nil`: ver `internal/api/handlers/admin_campaign_failures.go` e `admin_notifications.go`.
- Cálculo de "ontem": `time.Now().AddDate(0, 0, -1).Format("2006-01-02")` (igual a `CampaignFailuresHandler.GetList`).
- Hooks React Query: ver `useNotifications`/`useMarkNotificationsRead` em `src/api/hooks.js`.
- Modal via `createPortal` + classes `.confirm-backdrop`/`.btn`: ver `src/components/ConfirmModal.jsx`.

> **Nota sobre testes de frontend:** o `frontend/` **não tem** vitest/jest (só `dev`/`build`/`lint`/`preview`). Logo, a verificação das tasks de frontend é `npm run lint` + `npm run build` + checagem manual descrita. Não criar arquivos `*.test.jsx`.

> **Nota sobre `cd` no Windows:** os comandos Go assumem que você está em `workers/`. Os comandos npm assumem `frontend/`. Rode `cd` uma vez por sessão de terminal.

---

## Task 1: Backend — tipos de response + reducer puro `digestFromDaily`

**Files:**
- Create: `workers/internal/api/handlers/admin_daily_failures_digest.go`
- Test: `workers/internal/api/handlers/admin_daily_failures_digest_test.go`

- [ ] **Step 1: Escrever o teste que falha (reducer puro)**

Cria `workers/internal/api/handlers/admin_daily_failures_digest_test.go`:

```go
package handlers

import (
	"testing"

	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

func TestDigestFromDaily(t *testing.T) {
	idA, idB := uuid.New(), uuid.New()
	res := &catalog.DailyResult{
		Mode: "by_date",
		Date: "2026-06-17",
		Summary: catalog.CampaignDailySummary{
			Campaigns: 2, Stations: 3, TotalDeficit: 9,
		},
		Campaigns: []catalog.CampaignDailyFailure{
			{
				Campaign: catalog.CampaignInfo{
					ID: idA, Name: "Verão 2026",
					ClientName: "Cliente A", ClientLogoURL: "logoA",
				},
				Stations: []catalog.CampaignFailureStation{{}, {}}, // 2 emissoras
			},
			{
				Campaign: catalog.CampaignInfo{
					ID: idB, Name: "Liquida Inverno",
					ClientName: "Cliente B", ClientLogoURL: "",
				},
				Stations: []catalog.CampaignFailureStation{{}}, // 1 emissora
			},
		},
	}

	out := digestFromDaily(res, true)

	if out.Date != "2026-06-17" {
		t.Errorf("Date = %q, want 2026-06-17", out.Date)
	}
	if !out.Seen {
		t.Error("Seen = false, want true")
	}
	if out.Summary.Campaigns != 2 || out.Summary.Stations != 3 {
		t.Errorf("Summary = %+v, want {Campaigns:2 Stations:3}", out.Summary)
	}
	if len(out.Campaigns) != 2 {
		t.Fatalf("len(Campaigns) = %d, want 2", len(out.Campaigns))
	}
	if out.Campaigns[0].ID != idA || out.Campaigns[0].Name != "Verão 2026" {
		t.Errorf("Campaigns[0] id/name wrong: %+v", out.Campaigns[0])
	}
	if out.Campaigns[0].ClientName != "Cliente A" || out.Campaigns[0].ClientLogoURL != "logoA" {
		t.Errorf("Campaigns[0] client wrong: %+v", out.Campaigns[0])
	}
	if out.Campaigns[0].StationsFailed != 2 {
		t.Errorf("Campaigns[0].StationsFailed = %d, want 2", out.Campaigns[0].StationsFailed)
	}
	if out.Campaigns[1].StationsFailed != 1 {
		t.Errorf("Campaigns[1].StationsFailed = %d, want 1", out.Campaigns[1].StationsFailed)
	}
}

func TestDigestFromDaily_Empty(t *testing.T) {
	res := &catalog.DailyResult{
		Mode: "by_date", Date: "2026-06-17",
		Summary:   catalog.CampaignDailySummary{},
		Campaigns: []catalog.CampaignDailyFailure{},
	}
	out := digestFromDaily(res, false)
	if out.Campaigns == nil {
		t.Error("Campaigns is nil, want non-nil empty slice (JSON [])")
	}
	if len(out.Campaigns) != 0 {
		t.Errorf("len(Campaigns) = %d, want 0", len(out.Campaigns))
	}
}
```

- [ ] **Step 2: Rodar o teste e confirmar que falha (não compila)**

Run: `cd workers && go test ./internal/api/handlers/ -run TestDigestFromDaily -v`
Expected: FAIL — `undefined: digestFromDaily` / tipos não existem.

- [ ] **Step 3: Criar o arquivo com tipos + reducer**

Cria `workers/internal/api/handlers/admin_daily_failures_digest.go`:

```go
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
)

// ─── Response types (forma leve do digest) ───────────────────────────

type digestCampaign struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	ClientName     string    `json:"client_name"`
	ClientLogoURL  string    `json:"client_logo_url"`
	StationsFailed int       `json:"stations_failed"`
}

type digestSummary struct {
	Campaigns int `json:"campaigns"`
	Stations  int `json:"stations"`
}

type digestResponse struct {
	Date      string           `json:"date"`
	Seen      bool             `json:"seen"`
	Summary   digestSummary    `json:"summary"`
	Campaigns []digestCampaign `json:"campaigns"`
}

// digestFromDaily reduz o DailyResult completo (do catalog) para a forma
// leve da modal: por campanha só nome/cliente/contagem-de-emissoras.
// Sempre devolve Campaigns como slice não-nil (JSON "[]", nunca null).
func digestFromDaily(res *catalog.DailyResult, seen bool) digestResponse {
	out := digestResponse{
		Date:      res.Date,
		Seen:      seen,
		Summary:   digestSummary{Campaigns: res.Summary.Campaigns, Stations: res.Summary.Stations},
		Campaigns: []digestCampaign{},
	}
	for _, c := range res.Campaigns {
		out.Campaigns = append(out.Campaigns, digestCampaign{
			ID:             c.Campaign.ID,
			Name:           c.Campaign.Name,
			ClientName:     c.Campaign.ClientName,
			ClientLogoURL:  c.Campaign.ClientLogoURL,
			StationsFailed: len(c.Stations),
		})
	}
	return out
}

// ─── Chave de "visto" (reusa notification_reads) ─────────────────────

const digestKeyPrefix = "daily_failures_digest:"

func digestKey(dayStr string) string { return digestKeyPrefix + dayStr }

// yesterdayLocal devolve "ontem" no fuso local, igual ao default de
// CampaignFailuresHandler.GetList. Centralizado pra GET e Ack usarem a
// mesma data.
func yesterdayLocal() time.Time { return time.Now().AddDate(0, 0, -1) }

// ─── Repos (interfaces estreitas pra testar sem DB) ──────────────────

// DigestCampaignsRepo é satisfeito por *catalog.CampaignFailures.
type DigestCampaignsRepo interface {
	ListForDate(ctx context.Context, day time.Time) (*catalog.DailyResult, error)
}

// DigestSeenRepo é satisfeito por *catalog.Notifications (após Task 3
// adicionar HasRead).
type DigestSeenRepo interface {
	HasRead(ctx context.Context, userID uuid.UUID, key string) (bool, error)
	MarkRead(ctx context.Context, userID uuid.UUID, keys []string) (int, error)
}

// DailyFailuresDigestHandler powers /v1/internal/admin/daily-failures-digest.
// Auth: admin-only (montado no grupo admin do router, junto do sininho).
type DailyFailuresDigestHandler struct {
	Campaigns DigestCampaignsRepo
	Seen      DigestSeenRepo
	Log       *zap.Logger
}

func (h *DailyFailuresDigestHandler) logErr(msg string, err error) {
	if h.Log != nil {
		h.Log.Error(msg, zap.Error(err))
	}
}

// Get e Ack são implementados na Task 2.
var _ = http.StatusOK // mantém net/http importado até a Task 2
```

> Nota: o `var _ = http.StatusOK` é um stub temporário só pra `net/http` não ficar "imported and not used" entre a Task 1 e a Task 2. A Task 2 remove essa linha ao adicionar os métodos que usam `http`.

- [ ] **Step 4: Rodar o teste e confirmar que passa**

Run: `cd workers && go test ./internal/api/handlers/ -run TestDigestFromDaily -v`
Expected: PASS (ambos `TestDigestFromDaily` e `TestDigestFromDaily_Empty`).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/admin_daily_failures_digest.go workers/internal/api/handlers/admin_daily_failures_digest_test.go
git commit -m "feat(failures): tipos + reducer puro do digest diário de falhas"
```

---

## Task 2: Backend — handler `Get` e `Ack` (com fakes, sem DB)

**Files:**
- Modify: `workers/internal/api/handlers/admin_daily_failures_digest.go`
- Test: `workers/internal/api/handlers/admin_daily_failures_digest_test.go`

- [ ] **Step 1: Adicionar os testes de handler (que falham)**

Acrescenta ao final de `admin_daily_failures_digest_test.go` (mantém o `package handlers` e os imports já existentes; adiciona os imports novos no bloco de import no topo: `context`, `encoding/json`, `net/http`, `net/http/httptest`, `time`, e `radiocheck/internal/auth`):

```go
// ─── Fakes ───────────────────────────────────────────────────────────

type fakeDigestCampaigns struct {
	res    *catalog.DailyResult
	err    error
	gotDay time.Time
}

func (f *fakeDigestCampaigns) ListForDate(ctx context.Context, day time.Time) (*catalog.DailyResult, error) {
	f.gotDay = day
	return f.res, f.err
}

type fakeDigestSeen struct {
	seen       bool
	markedUser uuid.UUID
	markedKeys []string
}

func (f *fakeDigestSeen) HasRead(ctx context.Context, userID uuid.UUID, key string) (bool, error) {
	return f.seen, nil
}

func (f *fakeDigestSeen) MarkRead(ctx context.Context, userID uuid.UUID, keys []string) (int, error) {
	f.markedUser = userID
	f.markedKeys = append(f.markedKeys, keys...)
	return len(keys), nil
}

func reqWithAdmin(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	return r.WithContext(auth.WithClaims(r.Context(), &auth.Claims{
		UserID: uuid.New(), Role: "admin",
	}))
}

func sampleDaily() *catalog.DailyResult {
	return &catalog.DailyResult{
		Mode: "by_date", Date: "2026-06-17",
		Summary: catalog.CampaignDailySummary{Campaigns: 1, Stations: 2},
		Campaigns: []catalog.CampaignDailyFailure{
			{
				Campaign: catalog.CampaignInfo{ID: uuid.New(), Name: "Verão", ClientName: "Cli"},
				Stations: []catalog.CampaignFailureStation{{}, {}},
			},
		},
	}
}

// ─── GET ─────────────────────────────────────────────────────────────

func TestDigest_Get_Unseen(t *testing.T) {
	h := &DailyFailuresDigestHandler{
		Campaigns: &fakeDigestCampaigns{res: sampleDaily()},
		Seen:      &fakeDigestSeen{seen: false},
	}
	w := httptest.NewRecorder()
	h.Get(w, reqWithAdmin("GET", "/admin/daily-failures-digest"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body digestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body.Seen {
		t.Error("Seen = true, want false")
	}
	if len(body.Campaigns) != 1 || body.Campaigns[0].StationsFailed != 2 {
		t.Errorf("campaigns wrong: %+v", body.Campaigns)
	}
}

func TestDigest_Get_Seen(t *testing.T) {
	h := &DailyFailuresDigestHandler{
		Campaigns: &fakeDigestCampaigns{res: sampleDaily()},
		Seen:      &fakeDigestSeen{seen: true},
	}
	w := httptest.NewRecorder()
	h.Get(w, reqWithAdmin("GET", "/admin/daily-failures-digest"))

	var body digestResponse
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !body.Seen {
		t.Error("Seen = false, want true")
	}
}

func TestDigest_Get_Unauthorized(t *testing.T) {
	h := &DailyFailuresDigestHandler{
		Campaigns: &fakeDigestCampaigns{res: sampleDaily()},
		Seen:      &fakeDigestSeen{},
	}
	w := httptest.NewRecorder()
	// httptest.NewRequest sem claims injetadas → userIDFromReq falha.
	h.Get(w, httptest.NewRequest("GET", "/admin/daily-failures-digest", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// ─── ACK ─────────────────────────────────────────────────────────────

func TestDigest_Ack_MarksYesterdayKey(t *testing.T) {
	seen := &fakeDigestSeen{}
	h := &DailyFailuresDigestHandler{
		Campaigns: &fakeDigestCampaigns{res: sampleDaily()},
		Seen:      seen,
	}
	w := httptest.NewRecorder()
	h.Ack(w, reqWithAdmin("POST", "/admin/daily-failures-digest/ack"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	wantKey := "daily_failures_digest:" + time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if len(seen.markedKeys) != 1 || seen.markedKeys[0] != wantKey {
		t.Errorf("markedKeys = %v, want [%s]", seen.markedKeys, wantKey)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `cd workers && go test ./internal/api/handlers/ -run TestDigest_ -v`
Expected: FAIL — `h.Get`/`h.Ack` undefined (e provavelmente erro de import não usado).

- [ ] **Step 3: Implementar `Get` e `Ack`**

Em `admin_daily_failures_digest.go`: **remover** a linha stub `var _ = http.StatusOK` e adicionar os métodos ao final:

```go
// Get serve GET /admin/daily-failures-digest.
// Calcula "ontem", busca as falhas do dia e o flag "seen" do usuário atual.
func (h *DailyFailuresDigestHandler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromReq(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	day := yesterdayLocal()
	dayStr := day.Format("2006-01-02")

	if h.Campaigns == nil {
		// Test harness sem DB — devolve vazio em vez de panicar.
		writeJSON(w, http.StatusOK, digestResponse{Date: dayStr, Campaigns: []digestCampaign{}})
		return
	}

	res, err := h.Campaigns.ListForDate(r.Context(), day)
	if err != nil {
		h.logErr("daily_failures_digest_list_failed", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	seen := false
	if h.Seen != nil {
		s, err := h.Seen.HasRead(r.Context(), userID, digestKey(dayStr))
		if err != nil {
			h.logErr("daily_failures_digest_hasread_failed", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		seen = s
	}

	writeJSON(w, http.StatusOK, digestFromDaily(res, seen))
}

// Ack serve POST /admin/daily-failures-digest/ack.
// Sem body. Servidor deriva "ontem" e marca a chave como lida pro usuário.
func (h *DailyFailuresDigestHandler) Ack(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromReq(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	dayStr := yesterdayLocal().Format("2006-01-02")

	if h.Seen != nil {
		if _, err := h.Seen.MarkRead(r.Context(), userID, []string{digestKey(dayStr)}); err != nil {
			h.logErr("daily_failures_digest_ack_failed", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"acked": true})
}
```

- [ ] **Step 4: Rodar todos os testes do pacote e confirmar que passam**

Run: `cd workers && go test ./internal/api/handlers/ -run 'TestDigest' -v`
Expected: PASS — `TestDigestFromDaily`, `TestDigestFromDaily_Empty`, `TestDigest_Get_Unseen`, `TestDigest_Get_Seen`, `TestDigest_Get_Unauthorized`, `TestDigest_Ack_MarksYesterdayKey`.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/admin_daily_failures_digest.go workers/internal/api/handlers/admin_daily_failures_digest_test.go
git commit -m "feat(failures): handler Get/Ack do digest diário (admin-only)"
```

---

## Task 3: Backend — método `HasRead` no catalog de notificações

**Files:**
- Modify: `workers/internal/catalog/notifications.go`

> Este método é SQL puro (precisa de DB pra teste de integração) — a cobertura
> de comportamento está nas tasks de handler (via fake). Aqui só adicionamos a
> implementação real que satisfaz `DigestSeenRepo`.

- [ ] **Step 1: Adicionar `HasRead`**

Acrescenta ao final de `workers/internal/catalog/notifications.go` (o pacote já importa `context`, `fmt`, `github.com/google/uuid`):

```go
// HasRead reporta se o usuário já marcou aquela notification_key como lida.
// Usado pelo digest diário de falhas pra decidir se a modal já foi vista
// hoje. Chave esperada: "daily_failures_digest:YYYY-MM-DD".
func (n *Notifications) HasRead(ctx context.Context, userID uuid.UUID, key string) (bool, error) {
	var exists bool
	err := n.pool.QueryRow(ctx, `
SELECT EXISTS(
  SELECT 1 FROM notification_reads
  WHERE user_id = $1 AND notification_key = $2
)`, userID, key).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("notifications.HasRead: %w", err)
	}
	return exists, nil
}
```

- [ ] **Step 2: Confirmar que compila**

Run: `cd workers && go build ./...`
Expected: sem erros.

- [ ] **Step 3: Verificar que `*catalog.Notifications` satisfaz `DigestSeenRepo`**

Adiciona uma asserção de tipo em tempo de compilação ao final de `admin_daily_failures_digest.go` (logo após a definição de `DigestSeenRepo`, ou no fim do arquivo):

```go
// Garante em tempo de compilação que o catalog real satisfaz as interfaces.
var (
	_ DigestSeenRepo      = (*catalog.Notifications)(nil)
	_ DigestCampaignsRepo = (*catalog.CampaignFailures)(nil)
)
```

Run: `cd workers && go build ./...`
Expected: sem erros. Se `DigestSeenRepo` não for satisfeito, o build falha aqui — é o objetivo.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/catalog/notifications.go workers/internal/api/handlers/admin_daily_failures_digest.go
git commit -m "feat(failures): catalog.Notifications.HasRead + assert de interfaces"
```

---

## Task 4: Backend — wiring (Deps + router + main.go)

**Files:**
- Modify: `workers/internal/api/router.go` (struct `Deps` + registro de rotas)
- Modify: `workers/cmd/api/main.go`

- [ ] **Step 1: Adicionar o campo na struct `Deps`**

Em `workers/internal/api/router.go`, na struct `Deps`, logo após a linha `Notifications *handlers.NotificationsHandler`:

```go
	Notifications         *handlers.NotificationsHandler
	DailyFailuresDigest   *handlers.DailyFailuresDigestHandler
```

- [ ] **Step 2: Registrar as rotas no grupo admin**

Em `router.go`, logo após o bloco `if d.Notifications != nil { ... }` (que registra `/admin/notifications`), adicionar:

```go
				// /admin/daily-failures-digest — modal de resumo diário de
				// falhas (1x/dia/usuário). Reusa notification_reads pro flag
				// "visto". Doc: docs/features/daily-failures-digest-modal.md
				if d.DailyFailuresDigest != nil {
					r.Group(func(r chi.Router) {
						r.Use(auth.RequireRole("admin"))
						r.Get("/admin/daily-failures-digest", d.DailyFailuresDigest.Get)
						r.Post("/admin/daily-failures-digest/ack", d.DailyFailuresDigest.Ack)
					})
				}
```

- [ ] **Step 3: Instanciar no `main.go`**

Em `workers/cmd/api/main.go`, no literal de `Deps` (ou `handlers...`/`api.Deps{...}`), logo após o bloco:

```go
		Notifications: &handlers.NotificationsHandler{
			Repo: catalog.NewNotifications(pool),
			Log:  logger,
		},
```

adicionar:

```go
		DailyFailuresDigest: &handlers.DailyFailuresDigestHandler{
			Campaigns: catalog.NewCampaignFailures(pool),
			Seen:      catalog.NewNotifications(pool),
			Log:       logger,
		},
```

- [ ] **Step 4: Build + testes do pacote api**

Run: `cd workers && go build ./... && go test ./internal/api/...`
Expected: build sem erros; testes existentes + os novos passam.

- [ ] **Step 5: Vet (sanidade)**

Run: `cd workers && go vet ./internal/api/... ./internal/catalog/...`
Expected: sem warnings.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/api/router.go workers/cmd/api/main.go
git commit -m "feat(failures): registra e injeta o handler do digest diário"
```

---

## Task 5: Frontend — hooks de digest

**Files:**
- Modify: `frontend/src/api/hooks.js`

- [ ] **Step 1: Adicionar os hooks**

Em `frontend/src/api/hooks.js`, logo após o bloco dos hooks de notificações (`useMarkAllNotificationsRead`, ~linha 1012), adicionar:

```js
// ─── Digest diário de falhas (modal admin) ──────────────────────────
// Spec: docs/superpowers/specs/2026-06-18-daily-failures-digest-modal-design.md
// Mesmo gating que useNotifications: enabled em isAdmin (operator==admin).

export function useDailyFailuresDigest({ enabled = true } = {}) {
  return useQuery({
    queryKey: ['admin', 'daily-failures-digest'],
    queryFn: () => api.get('/admin/daily-failures-digest').then(r => r.data),
    staleTime: 5 * 60_000,
    refetchOnWindowFocus: false,
    enabled,
  })
}

export function useAckDailyFailuresDigest() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () =>
      api.post('/admin/daily-failures-digest/ack').then(r => r.data),
    onSuccess: () => {
      // Marca seen localmente pra modal não reabrir sem refetch.
      qc.setQueryData(['admin', 'daily-failures-digest'], (old) =>
        old ? { ...old, seen: true } : old)
    },
  })
}
```

- [ ] **Step 2: Lint**

Run: `cd frontend && npm run lint`
Expected: sem novos erros referentes a `hooks.js`.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/hooks.js
git commit -m "feat(failures): hooks do digest diário de falhas"
```

---

## Task 6: Frontend — componente `<DailyFailuresModal/>` + CSS

**Files:**
- Create: `frontend/src/components/DailyFailuresModal.jsx`
- Create: `frontend/src/components/DailyFailuresModal.css`

- [ ] **Step 1: Criar o CSS**

Cria `frontend/src/components/DailyFailuresModal.css` (reusa `.confirm-backdrop` e `@keyframes confirm-slide-in` do `index.css`, define só o card mais largo + a lista):

```css
/* Modal de resumo diário de falhas. Reusa .confirm-backdrop / .btn do
   index.css; card e lista são próprios. */
.dfm-card {
  background: var(--c-surface);
  border: 1px solid var(--c-border);
  border-radius: 14px;
  box-shadow: 0 24px 64px rgba(6, 5, 91, 0.18), 0 4px 16px rgba(6, 5, 91, 0.10);
  padding: 24px 24px 18px;
  width: 100%;
  max-width: 440px;
  display: flex;
  flex-direction: column;
  gap: 16px;
  animation: confirm-slide-in 150ms cubic-bezier(0.34, 1.56, 0.64, 1);
}

.dfm-header {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.dfm-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 320px;
  overflow-y: auto;
}

.dfm-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 8px;
  border-radius: 10px;
}

.dfm-item:hover {
  background: var(--c-surface-2, #f1f5f9);
}

.dfm-item-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}

.dfm-item-name {
  font-family: var(--font-heading);
  font-size: 14px;
  font-weight: 600;
  color: var(--c-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.dfm-item-client {
  font-family: var(--font-body);
  font-size: 12px;
  color: var(--c-text-2);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.dfm-badge {
  flex-shrink: 0;
  font-family: var(--font-body);
  font-size: 12px;
  font-weight: 700;
  color: var(--c-danger);
  background: rgba(239, 68, 68, 0.08);
  padding: 4px 10px;
  border-radius: 999px;
  white-space: nowrap;
}
```

- [ ] **Step 2: Criar o componente**

Cria `frontend/src/components/DailyFailuresModal.jsx`:

```jsx
import { useEffect } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../contexts/AuthContext'
import { useDailyFailuresDigest, useAckDailyFailuresDigest } from '../api/hooks'
import ClientAvatar from './ClientAvatar'
import './DailyFailuresModal.css'

// 'YYYY-MM-DD' → 'DD/MM'. Sem usar Date pra evitar drift de fuso.
function formatDayBR(iso) {
  const parts = (iso || '').split('-')
  if (parts.length !== 3) return ''
  return `${parts[2]}/${parts[1]}`
}

function plural(n, singular, plural) {
  return n === 1 ? singular : plural
}

// Modal admin-only de resumo das falhas de ontem. Aparece 1x/dia/usuário
// no primeiro load (gatilho global). Marca "visto" só ao interagir
// (fechar / ESC / backdrop / clicar em "Ver falhas").
export default function DailyFailuresModal() {
  const { isAdmin } = useAuth()
  const { data } = useDailyFailuresDigest({ enabled: isAdmin })
  const ack = useAckDailyFailuresDigest()
  const navigate = useNavigate()

  const open = !!(isAdmin && data && !data.seen && data.campaigns?.length > 0)

  useEffect(() => {
    if (!open) return undefined
    function onKey(e) {
      if (e.key === 'Escape') ack.mutate()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // ack é estável (mutation do React Query); incluir `open` basta.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  if (!open) return null

  function dismiss() {
    ack.mutate()
  }

  function goToFailures() {
    ack.mutate()
    navigate(`/admin/station-failures?view=by_campaign&date=${data.date}`)
  }

  const { summary, campaigns } = data

  return createPortal(
    <div className="confirm-backdrop" onClick={dismiss} role="dialog" aria-modal="true">
      <div className="dfm-card" onClick={e => e.stopPropagation()}>
        <div className="dfm-header">
          <p className="confirm-title">Falhas de ontem ({formatDayBR(data.date)})</p>
          <p className="confirm-message">
            {summary.campaigns} {plural(summary.campaigns, 'campanha', 'campanhas')}
            {' · '}
            {summary.stations} {plural(summary.stations, 'emissora', 'emissoras')} com falha
          </p>
        </div>

        <ul className="dfm-list">
          {campaigns.map(c => (
            <li key={c.id} className="dfm-item">
              <ClientAvatar
                client={{ client_name: c.client_name, client_logo_url: c.client_logo_url }}
                size={32}
              />
              <div className="dfm-item-text">
                <span className="dfm-item-name">{c.name}</span>
                <span className="dfm-item-client">{c.client_name}</span>
              </div>
              <span className="dfm-badge">
                {c.stations_failed} {plural(c.stations_failed, 'emissora', 'emissoras')}
              </span>
            </li>
          ))}
        </ul>

        <div className="confirm-actions">
          <button className="btn btn-secondary btn-sm" onClick={dismiss}>
            Fechar
          </button>
          <button className="btn btn-primary btn-sm" onClick={goToFailures}>
            Ver falhas
          </button>
        </div>
      </div>
    </div>,
    document.body
  )
}
```

> **Por que `client={{ client_name, client_logo_url }}` e não `client={c}`:** o `ClientAvatar` usa `client?.name` ANTES de `client?.client_name` pra inicial/alt. Como `c.name` é o nome da **campanha**, passar `c` direto faria o avatar usar a inicial errada. Passamos só os campos de cliente.

- [ ] **Step 3: Lint**

Run: `cd frontend && npm run lint`
Expected: sem novos erros nos 2 arquivos criados.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/DailyFailuresModal.jsx frontend/src/components/DailyFailuresModal.css
git commit -m "feat(failures): componente DailyFailuresModal + estilos"
```

---

## Task 7: Frontend — montar a modal no `AppShell`

**Files:**
- Modify: `frontend/src/App.jsx`

- [ ] **Step 1: Importar o componente**

Em `frontend/src/App.jsx`, junto dos imports de componentes (após `import RadioPlayer from './components/RadioPlayer'`):

```js
import RadioPlayer from './components/RadioPlayer'
import DailyFailuresModal from './components/DailyFailuresModal'
```

- [ ] **Step 2: Montar dentro do `AppShell`**

Em `App.jsx`, dentro de `AppShell`, logo após `<RadioPlayer />`:

```jsx
        {/* Main content */}
        <RadioPlayer />
        {/* Modal de resumo diário de falhas — auto-gateada em admin,
            portaliza pro body. Render aqui (sob RequireAuth) garante que
            só aparece logado. */}
        <DailyFailuresModal />
        <main className="app-content">
```

- [ ] **Step 3: Lint + build**

Run: `cd frontend && npm run lint && npm run build`
Expected: lint sem novos erros; build conclui sem erros.

- [ ] **Step 4: Verificação manual (descrita)**

> Backend rodando local + DB com pelo menos um `daily_play_summary.deficit > 0`
> em ontem. Logar como **admin**:
> 1. Primeiro load → a modal aparece com as campanhas de ontem e contagens.
> 2. Dar refresh sem interagir → a modal **reaparece** (só marca ao interagir).
> 3. Clicar "Ver falhas" → navega pra `/admin/station-failures?view=by_campaign&date=<ontem>` e a modal some.
> 4. Refresh de novo → **não** reaparece (já visto hoje).
> 5. Clicar "Fechar"/ESC/backdrop em outro dia (ou limpando a chave) → também marca visto.
> 6. Logar como **cliente (viewer)** → modal nunca aparece, sem request `/admin/daily-failures-digest`.
> 7. Dia sem falhas → modal não aparece.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/App.jsx
git commit -m "feat(failures): monta DailyFailuresModal no AppShell"
```

---

## Task 8: Documentação

**Files:**
- Create: `docs/features/daily-failures-digest-modal.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Criar o doc da feature**

Cria `docs/features/daily-failures-digest-modal.md`:

```markdown
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
```

- [ ] **Step 2: Adicionar a linha no mapa de consulta do `CLAUDE.md`**

Em `CLAUDE.md`, na tabela "Mapa de consulta", logo após a linha do sininho
(`Sininho de notificações ... admin-notifications.md`), adicionar:

```markdown
| Modal de resumo diário de falhas (admin, 1x/dia/usuário, leva pra /admin/station-failures) | [docs/features/daily-failures-digest-modal.md](docs/features/daily-failures-digest-modal.md) |
```

- [ ] **Step 3: Commit**

```bash
git add docs/features/daily-failures-digest-modal.md CLAUDE.md
git commit -m "docs(failures): documenta a modal de resumo diário de falhas"
```

---

## Self-Review (preenchido na escrita do plano)

**1. Spec coverage:**
- §2 decisões (admin / global / marca-ao-interagir / lista+contagem / botão) → Tasks 2, 6, 7. ✅
- §4.1 endpoints admin-only → Task 4 (router). ✅
- §4.2 GET response → Tasks 1 (reducer) + 2 (handler). ✅
- §4.3 ack deriva data server-side → Task 2 (`Ack` usa `yesterdayLocal`). ✅
- §4.4 chave `daily_failures_digest:DATE` → Task 1 (`digestKey`). ✅
- §4.5 código novo (HasRead, handler, router, main) → Tasks 2/3/4. ✅
- §5.1 hooks → Task 5. ✅
- §5.2 componente (createPortal, classes, render condicional, ações) → Task 6. ✅
- §5.3 montagem no AppShell → Task 7. ✅
- §6 edge cases (vazio / seen / erro / cliente / refresh) → cobertos em Tasks 2 (vazio/seen/401) e 7 (verificação manual cliente/refresh/vazio). ✅
- §8 docs → Task 8. ✅

**2. Placeholder scan:** sem TBD/TODO; todo passo de código tem código completo. ✅

**3. Type consistency:** `digestResponse`/`digestCampaign`/`digestSummary`, `digestFromDaily(res, seen)`, `digestKey(dayStr)`, `yesterdayLocal()`, `DigestCampaignsRepo`/`DigestSeenRepo`, `DailyFailuresDigestHandler{Campaigns, Seen, Log}` consistentes entre Tasks 1–4. Hooks `useDailyFailuresDigest`/`useAckDailyFailuresDigest` e queryKey `['admin','daily-failures-digest']` consistentes entre Tasks 5–6. Campos JSON (`stations_failed`, `client_name`, `client_logo_url`, `date`, `seen`, `summary.campaigns`, `summary.stations`) consistentes entre backend (Task 1) e frontend (Task 6). ✅

**Observações de risco:**
- `auth.WithClaims` e `auth.Claims{UserID, Role}` existem (`internal/auth/middleware.go`). Se o nome divergir no seu checkout, use `auth.ContextWithClaims` (alias em `scope.go`).
- `var _ = http.StatusOK` na Task 1 é stub removido na Task 2 — não esquecer de remover.
```
