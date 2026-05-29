# Visão Gerencial Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Nova tela admin-only `/management` ("Visão Gerencial") com mapa cross-campanha pulsando, feed global ao vivo, 4 KPIs e filtros opcionais (cliente/campanhas/período/status), servida por um endpoint agregado novo.

**Architecture:** Espelha o par `LiveMap`/`Insights` já existentes. Backend: repo `catalog.ManagementOverview` (3 queries num payload só) + handler `ManagementOverviewHandler` (interface mockável) registrado no grupo **admin/operator** do router (sem scope de viewer). Frontend: página React reusando `BrazilMap`, `RSelect`, `StationAvatar` e a linha de feed do `/live-map` (extraída para componente compartilhado), com `react-query` (refetch 20s + placeholderData).

**Tech Stack:** Go 1.x (chi, pgx/v5), Postgres; React + Vite, react-query, d3-geo (BrazilMap), html2canvas.

**Convenção de escopo vs. período (importante):** Os filtros cliente/campanhas/status + **sobreposição de período** definem *quais campanhas* entram na visão. Sobre essas campanhas: o **mapa** mostra as emissoras-alvo (pulso = `health_status` atual) e o **feed** mostra as últimas detecções (sempre "agora", sem corte por data). O recorte de **período** só limita os **KPIs históricos** (`airings_total`). Default de período = ano corrente (01/01 → hoje).

---

## File Structure

**Backend (Go):**
- Create `workers/internal/catalog/management_overview.go` — repo: tipos + `Get()` com 3 queries (KPIs, stations, recent detections).
- Create `workers/internal/api/handlers/management_overview.go` — handler + interface `ManagementOverviewRepo` + parse de params.
- Create `workers/internal/api/handlers/management_overview_test.go` — testes de handler (param parsing, defaults, passthrough, erros).
- Modify `workers/internal/api/router.go` — campo `ManagementOverview` em `Deps` + rota no subgrupo B (admin/operator).
- Modify `workers/cmd/api/main.go` — wiring da dependência.

**Frontend (React):**
- Create `frontend/src/components/LiveAiringRow.jsx` — extrai `LiveAiringRow` + `FeedSkeleton` + helpers de formatação do `LiveMapPage` (DRY; reuso entre `/live-map` e `/management`).
- Modify `frontend/src/pages/LiveMapPage.jsx` — passa a importar o componente extraído (sem mudança de comportamento).
- Modify `frontend/src/api/hooks.js` — `useManagementOverview(filters)`.
- Create `frontend/src/pages/ManagementPage.jsx` — a página.
- Create `frontend/src/pages/ManagementPage.css` — estilos.
- Modify `frontend/src/components/Sidebar.jsx` — ícone + item "Visão Gerencial" na seção Administração.
- Modify `frontend/src/App.jsx` — rota `/management` com `RequireRole roles={['admin']}`.

**Docs:**
- Create `docs/features/management-overview.md` — doc operacional (header YAML).
- Modify `CLAUDE.md` — linha no mapa de consulta.

---

## Task 1: Backend — repo `catalog.ManagementOverview`

Espelha `workers/internal/catalog/live_map.go` (que não tem teste de catálogo — o teste automatizado fica no handler, Task 2). Verificação desta task é compilar.

**Files:**
- Create: `workers/internal/catalog/management_overview.go`

- [ ] **Step 1: Escrever o arquivo do repo**

```go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ManagementOverview agrega a "Visão Gerencial" (/management): uma visão
// cross-campanha/cross-cliente da operação inteira. Admin-only — o gating é
// feito no router (subgrupo admin/operator), por isso o repo não recebe scope.
//
// Convenção: os filtros (client/campaigns/status + sobreposição de período)
// definem QUAIS campanhas entram na visão. Sobre essas campanhas o mapa mostra
// as emissoras-alvo (pulso = health_status atual) e o feed as últimas
// detecções (sempre "agora"). O recorte de período só limita os KPIs
// históricos (airings_total). Reusa os tipos LiveStation/LiveDetection do
// live_map.go.
type ManagementOverview struct {
	pool *pgxpool.Pool
}

func NewManagementOverview(pool *pgxpool.Pool) *ManagementOverview {
	return &ManagementOverview{pool: pool}
}

// ManagementParams são os filtros opcionais já validados.
// ClientID nil = todos os clientes. CampaignIDs vazio = todas as campanhas.
// Status "" = todos os status. From/To delimitam o período (KPIs históricos).
type ManagementParams struct {
	ClientID    *uuid.UUID
	CampaignIDs []uuid.UUID
	Status      string
	From        time.Time
	To          time.Time
}

// ManagementKPIs são os 4 números (+ contexto) da coluna esquerda da tela.
type ManagementKPIs struct {
	StationsMonitored  int   `json:"stations_monitored"`  // distintas no período
	StationsLive       int   `json:"stations_live"`       // health_status='ok' agora
	StatesCount        int   `json:"states_count"`        // UFs distintas
	MaterialsMonitored int   `json:"materials_monitored"` // materiais vinculados
	CampaignsCount     int   `json:"campaigns_count"`     // campanhas no recorte
	AiringsTotal       int64 `json:"airings_total"`       // veiculações no período
	AiringsToday       int64 `json:"airings_today"`       // veiculações hoje
}

type ManagementResult struct {
	KPIs             ManagementKPIs  `json:"kpis"`
	Stations         []LiveStation   `json:"stations"`
	RecentDetections []LiveDetection `json:"recent_detections"`
}

// scopeArgs monta os 5 args compartilhados pelas três queries, na ordem
// $1=client_id (nullable), $2=campaign_ids, $3=status, $4=from, $5=to.
func (m *ManagementOverview) scopeArgs(p ManagementParams) []any {
	ids := p.CampaignIDs
	if ids == nil {
		ids = []uuid.UUID{}
	}
	return []any{p.ClientID, ids, p.Status, p.From, p.To}
}

func (m *ManagementOverview) Get(ctx context.Context, p ManagementParams) (ManagementResult, error) {
	var res ManagementResult

	kpis, err := m.queryKPIs(ctx, p)
	if err != nil {
		return res, err
	}
	res.KPIs = kpis

	stations, err := m.queryStations(ctx, p)
	if err != nil {
		return res, err
	}
	res.Stations = stations

	dets, err := m.queryRecentDetections(ctx, p)
	if err != nil {
		return res, err
	}
	res.RecentDetections = dets
	return res, nil
}

// scoped CTE: campanhas que casam com os filtros + sobreposição de período.
const mgmtScopedCTE = `
	WITH scoped AS (
	    SELECT id, target_stations
	    FROM campaigns
	    WHERE ($1::uuid IS NULL OR client_id = $1)
	      AND ($2::uuid[] = '{}' OR id = ANY($2))
	      AND ($3 = '' OR status = $3)
	      AND start_date <= $5::date
	      AND end_date   >= $4::date
	),
	mon_stations AS (
	    SELECT DISTINCT st AS station_id
	    FROM scoped, unnest(target_stations) AS st
	)`

func (m *ManagementOverview) queryKPIs(ctx context.Context, p ManagementParams) (ManagementKPIs, error) {
	var k ManagementKPIs
	err := m.pool.QueryRow(ctx, mgmtScopedCTE+`
		, joined AS (
		    SELECT s.id, s.state, s.health_status
		    FROM mon_stations ms JOIN stations s ON s.id = ms.station_id
		)
		SELECT
		  (SELECT COUNT(*) FROM joined)                                              AS stations_monitored,
		  (SELECT COUNT(*) FROM joined WHERE health_status = 'ok')                   AS stations_live,
		  (SELECT COUNT(DISTINCT state) FROM joined WHERE state IS NOT NULL)         AS states_count,
		  (SELECT COUNT(*) FROM scoped)                                              AS campaigns_count,
		  (SELECT COUNT(DISTINCT cm.material_id) FROM campaign_materials cm
		         WHERE cm.campaign_id IN (SELECT id FROM scoped))                    AS materials_monitored,
		  (SELECT COUNT(*) FROM detections d
		         WHERE d.campaign_id IN (SELECT id FROM scoped)
		           AND d.evidence_status <> 'audit_rejected'
		           AND d.ignored_at IS NULL AND d.retracted_at IS NULL
		           AND d.detected_at::date BETWEEN $4::date AND $5::date)            AS airings_total,
		  (SELECT COUNT(*) FROM detections d
		         WHERE d.campaign_id IN (SELECT id FROM scoped)
		           AND d.evidence_status <> 'audit_rejected'
		           AND d.ignored_at IS NULL AND d.retracted_at IS NULL
		           AND (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
		               = (now() AT TIME ZONE 'America/Sao_Paulo')::date)            AS airings_today
	`, m.scopeArgs(p)...).Scan(
		&k.StationsMonitored, &k.StationsLive, &k.StatesCount,
		&k.CampaignsCount, &k.MaterialsMonitored, &k.AiringsTotal, &k.AiringsToday,
	)
	return k, err
}

func (m *ManagementOverview) queryStations(ctx context.Context, p ManagementParams) ([]LiveStation, error) {
	rows, err := m.pool.Query(ctx, mgmtScopedCTE+`
		SELECT s.id, s.name, s.band, s.frequency_mhz, s.city, s.state,
		       s.latitude, s.longitude, s.health_status,
		       (SELECT MAX(d.detected_at)
		          FROM detections d
		         WHERE d.station_id = s.id
		           AND d.campaign_id IN (SELECT id FROM scoped)
		           AND d.evidence_status <> 'audit_rejected'
		           AND d.ignored_at IS NULL) AS last_detection_at
		FROM stations s
		JOIN mon_stations ms ON ms.station_id = s.id
		WHERE s.latitude IS NOT NULL AND s.longitude IS NOT NULL
		ORDER BY s.name
	`, m.scopeArgs(p)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LiveStation
	for rows.Next() {
		var st LiveStation
		if err := rows.Scan(&st.ID, &st.Name, &st.Band, &st.FrequencyMHz,
			&st.City, &st.State, &st.Latitude, &st.Longitude,
			&st.HealthStatus, &st.LastDetectionAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (m *ManagementOverview) queryRecentDetections(ctx context.Context, p ManagementParams) ([]LiveDetection, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), s.logo_url,
		       COALESCE(s.band, ''), s.frequency_mhz, s.city, s.state,
		       d.detected_at, d.commercial_id, COALESCE(m.title, c.title, ''), cli.name,
		       d.evidence_status
		FROM detections d
		LEFT JOIN stations s    ON s.id = d.station_id
		LEFT JOIN commercials c ON c.id = d.commercial_id
		LEFT JOIN materials m   ON m.id = d.commercial_id
		LEFT JOIN campaigns cmp ON cmp.id = d.campaign_id
		LEFT JOIN clients cli   ON cli.id = cmp.client_id
		WHERE d.campaign_id IN (
		    SELECT id FROM campaigns
		    WHERE ($1::uuid IS NULL OR client_id = $1)
		      AND ($2::uuid[] = '{}' OR id = ANY($2))
		      AND ($3 = '' OR status = $3)
		      AND start_date <= $5::date
		      AND end_date   >= $4::date
		)
		  AND d.evidence_status <> 'audit_rejected'
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		ORDER BY d.detected_at DESC
		LIMIT 50
	`, m.scopeArgs(p)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LiveDetection
	for rows.Next() {
		var d LiveDetection
		if err := rows.Scan(&d.ID, &d.StationID, &d.StationName, &d.StationLogoURL,
			&d.Band, &d.FrequencyMHz, &d.City, &d.State,
			&d.DetectedAt, &d.CommercialID, &d.CommercialName, &d.ClientName,
			&d.EvidenceStatus); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
```

- [ ] **Step 2: Compilar**

Run: `cd workers && go build ./...`
Expected: build sem erros.

- [ ] **Step 3: Commit**

```bash
git add workers/internal/catalog/management_overview.go
git commit -m "feat(catalog): repo ManagementOverview (KPIs + stations + feed cross-campanha)"
```

---

## Task 2: Backend — handler + testes (TDD)

**Files:**
- Create: `workers/internal/api/handlers/management_overview.go`
- Test: `workers/internal/api/handlers/management_overview_test.go`

> Reusa `parseUUIDList` e `parseDateOr` já existentes em `handlers/insights.go` (mesmo pacote) — não reimplementar.

- [ ] **Step 1: Escrever os testes (falhando)**

```go
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"radiocheck/internal/catalog"
)

type fakeMgmtRepo struct {
	got    catalog.ManagementParams
	called bool
	result catalog.ManagementResult
	err    error
}

func (f *fakeMgmtRepo) Get(ctx context.Context, p catalog.ManagementParams) (catalog.ManagementResult, error) {
	f.called = true
	f.got = p
	return f.result, f.err
}

func newMgmtReq(query string) *http.Request {
	return httptest.NewRequest("GET", "/management-overview"+query, nil)
}

func TestMgmtHandler_Defaults(t *testing.T) {
	fake := &fakeMgmtRepo{}
	h := &ManagementOverviewHandler{Repo: fake}
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq(""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !fake.called {
		t.Fatal("repo.Get não foi chamado")
	}
	if fake.got.ClientID != nil {
		t.Errorf("ClientID = %v, want nil", fake.got.ClientID)
	}
	if len(fake.got.CampaignIDs) != 0 {
		t.Errorf("CampaignIDs = %v, want vazio", fake.got.CampaignIDs)
	}
	if fake.got.Status != "" {
		t.Errorf("Status = %q, want vazio", fake.got.Status)
	}
	now := time.Now().UTC()
	if fake.got.From.Year() != now.Year() || fake.got.From.Month() != time.January || fake.got.From.Day() != 1 {
		t.Errorf("From = %v, want 01/01 do ano corrente", fake.got.From)
	}
}

func TestMgmtHandler_ParsesFilters(t *testing.T) {
	fake := &fakeMgmtRepo{}
	h := &ManagementOverviewHandler{Repo: fake}
	cid := uuid.New()
	camp := uuid.New()
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq("?client_id="+cid.String()+"&campaigns="+camp.String()+"&status=ativa&from=2026-02-01&to=2026-03-01"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if fake.got.ClientID == nil || *fake.got.ClientID != cid {
		t.Errorf("ClientID = %v, want %v", fake.got.ClientID, cid)
	}
	if len(fake.got.CampaignIDs) != 1 || fake.got.CampaignIDs[0] != camp {
		t.Errorf("CampaignIDs = %v, want [%v]", fake.got.CampaignIDs, camp)
	}
	if fake.got.Status != "ativa" {
		t.Errorf("Status = %q, want ativa", fake.got.Status)
	}
}

func TestMgmtHandler_InvalidClientID_400(t *testing.T) {
	h := &ManagementOverviewHandler{Repo: &fakeMgmtRepo{}}
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq("?client_id=not-uuid"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestMgmtHandler_InvalidStatus_400(t *testing.T) {
	h := &ManagementOverviewHandler{Repo: &fakeMgmtRepo{}}
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq("?status=bogus"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestMgmtHandler_ToBeforeFrom_400(t *testing.T) {
	h := &ManagementOverviewHandler{Repo: &fakeMgmtRepo{}}
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq("?from=2026-03-01&to=2026-02-01"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestMgmtHandler_RepoError_500(t *testing.T) {
	h := &ManagementOverviewHandler{Repo: &fakeMgmtRepo{err: context.DeadlineExceeded}}
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq(""))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
}

func TestMgmtHandler_JSONShape(t *testing.T) {
	fake := &fakeMgmtRepo{result: catalog.ManagementResult{
		KPIs:             catalog.ManagementKPIs{StationsMonitored: 3, StationsLive: 2},
		Stations:         []catalog.LiveStation{{ID: uuid.New(), Name: "40 Graus", Band: "FM"}},
		RecentDetections: []catalog.LiveDetection{{ID: uuid.New(), StationName: "40 Graus", CommercialName: "PILECCO"}},
	}}
	h := &ManagementOverviewHandler{Repo: fake}
	rr := httptest.NewRecorder()
	h.Get(rr, newMgmtReq(""))

	var body struct {
		KPIs             map[string]any   `json:"kpis"`
		Stations         []map[string]any `json:"stations"`
		RecentDetections []map[string]any `json:"recent_detections"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.KPIs["stations_monitored"].(float64) != 3 {
		t.Errorf("stations_monitored = %v, want 3", body.KPIs["stations_monitored"])
	}
	if len(body.Stations) != 1 || len(body.RecentDetections) != 1 {
		t.Fatalf("stations=%d recent=%d, want 1/1", len(body.Stations), len(body.RecentDetections))
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd workers && go test ./internal/api/handlers/ -run TestMgmtHandler -v`
Expected: FAIL — `undefined: ManagementOverviewHandler`.

- [ ] **Step 3: Escrever o handler**

```go
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"radiocheck/internal/catalog"
)

// ManagementOverviewRepo é a dependência mínima do handler (mockável em teste).
type ManagementOverviewRepo interface {
	Get(ctx context.Context, p catalog.ManagementParams) (catalog.ManagementResult, error)
}

type ManagementOverviewHandler struct {
	Repo ManagementOverviewRepo
}

func NewManagementOverviewHandler(repo ManagementOverviewRepo) *ManagementOverviewHandler {
	return &ManagementOverviewHandler{Repo: repo}
}

var mgmtValidStatus = map[string]bool{
	"programada": true, "ativa": true, "concluida": true, "cancelada": true,
}

// Get GET /management-overview — visão da operação inteira. Admin/operator-only
// (gating no router; sem scope de viewer). Todos os params são opcionais:
//   - client_id (uuid): default = todos os clientes
//   - campaigns (csv de uuids): default = todas
//   - status (programada|ativa|concluida|cancelada): default = todos
//   - from, to (YYYY-MM-DD): default = ano corrente (01/01 → hoje)
func (h *ManagementOverviewHandler) Get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var clientID *uuid.UUID
	if cid := q.Get("client_id"); cid != "" {
		parsed, err := uuid.Parse(cid)
		if err != nil {
			http.Error(w, "invalid client_id", http.StatusBadRequest)
			return
		}
		clientID = &parsed
	}

	camps, err := parseUUIDList(q.Get("campaigns"))
	if err != nil {
		http.Error(w, "invalid campaigns: "+err.Error(), http.StatusBadRequest)
		return
	}
	if camps == nil {
		camps = []uuid.UUID{}
	}
	if len(camps) > 200 {
		http.Error(w, "campaigns max=200", http.StatusBadRequest)
		return
	}

	status := q.Get("status")
	if status != "" && !mgmtValidStatus[status] {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	defaultFrom := time.Date(now.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	from := parseDateOr(q.Get("from"), defaultFrom)
	to := parseDateOr(q.Get("to"), now)
	if to.Before(from) {
		http.Error(w, "to must be on or after from", http.StatusBadRequest)
		return
	}

	out, err := h.Repo.Get(r.Context(), catalog.ManagementParams{
		ClientID:    clientID,
		CampaignIDs: camps,
		Status:      status,
		From:        from,
		To:          to,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd workers && go test ./internal/api/handlers/ -run TestMgmtHandler -v`
Expected: PASS (todos os 7).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/management_overview.go workers/internal/api/handlers/management_overview_test.go
git commit -m "feat(api): handler ManagementOverview + testes (param parsing/defaults/erros)"
```

---

## Task 3: Backend — wiring (Deps + router + main)

**Files:**
- Modify: `workers/internal/api/router.go` (campo em `Deps` ~linha 56; rota no subgrupo B ~linha 220+)
- Modify: `workers/cmd/api/main.go` (Deps ~linha 341)

- [ ] **Step 1: Adicionar campo em `Deps`**

Em `workers/internal/api/router.go`, logo após a linha `LiveMap *handlers.LiveMapHandler` (linha 56), inserir:

```go
	LiveMap               *handlers.LiveMapHandler
	ManagementOverview    *handlers.ManagementOverviewHandler
```

- [ ] **Step 2: Registrar a rota no subgrupo admin/operator**

Em `workers/internal/api/router.go`, dentro do **Subgrupo B** (`r.Use(auth.RequireRole("admin", "operator"))`, abre na linha 219), logo após o bloco de `r.Post("/stations", ...)` (depois da linha 230), inserir:

```go
				// Visão Gerencial — painel da operação inteira (cross-campanha,
				// cross-cliente). Admin/operator-only (sem scope de viewer): por
				// isso fica aqui, não no subgrupo A. Doc:
				// docs/features/management-overview.md.
				if d.ManagementOverview != nil {
					r.Get("/management-overview", d.ManagementOverview.Get)
				}
```

- [ ] **Step 3: Wiring no main**

Em `workers/cmd/api/main.go`, na struct `deps := api.Deps{...}`, logo após a linha `LiveMap: handlers.NewLiveMapHandler(catalog.NewLiveMap(pool)),` (linha 341), inserir:

```go
		LiveMap:               handlers.NewLiveMapHandler(catalog.NewLiveMap(pool)),
		ManagementOverview:    handlers.NewManagementOverviewHandler(catalog.NewManagementOverview(pool)),
```

- [ ] **Step 4: Compilar + testes do pacote**

Run: `cd workers && go build ./... && go test ./internal/api/...`
Expected: build ok; testes passam.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/router.go workers/cmd/api/main.go
git commit -m "feat(api): registra /management-overview no grupo admin/operator + wiring"
```

---

## Task 4: Frontend — extrair `LiveAiringRow` para componente compartilhado

Refactor sem mudança de comportamento: a linha do feed (e o skeleton + helpers) hoje vivem dentro de `LiveMapPage.jsx`. Extrair para reuso em `/management`.

**Files:**
- Create: `frontend/src/components/LiveAiringRow.jsx`
- Modify: `frontend/src/pages/LiveMapPage.jsx`

- [ ] **Step 1: Criar o componente compartilhado**

Criar `frontend/src/components/LiveAiringRow.jsx` movendo o que hoje está em `LiveMapPage.jsx`: os helpers `pad2`, `fmtDate`, `fmtTime`, `freqStr`, o componente `LiveAiringRow` e o `FeedSkeleton`. Conteúdo completo:

```jsx
import { useCallback, useEffect, useRef, useState } from 'react'
import StationAvatar from './StationAvatar'
import AudioPlayer from './AudioPlayer'
import api from '../api/client'
import { materialColor } from '../utils/materialColor'

export function pad2(n) { return String(n).padStart(2, '0') }
export function fmtDate(iso) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${pad2(d.getDate())}/${pad2(d.getMonth() + 1)}/${d.getFullYear()}`
}
export function fmtTime(iso) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`
}
export function freqStr(d) {
  return d.frequency_mhz != null ? String(d.frequency_mhz).replace('.', ',') : null
}

/* Linha do feed "Últimas Veiculações": avatar, data/hora, station + freq +
 * cidade/UF, material com cor + cliente, e player de áudio inline (lazy-load
 * do blob via /detections/{id}/evidence). */
export function LiveAiringRow({ detection, isPlaying, onPlayRequest, onPlayClose }) {
  const place = [detection.city, detection.state].filter(Boolean).join(' / ')
  const freq = freqStr(detection)
  const matColor = materialColor(detection.commercial_id)

  const [loading, setLoading] = useState(false)
  const [blobUrl, setBlobUrl] = useState(null)
  const blobRef = useRef(null)

  useEffect(() => () => {
    if (blobRef.current) URL.revokeObjectURL(blobRef.current)
  }, [])

  const ensureBlob = useCallback(async () => {
    if (blobRef.current) return blobRef.current
    const resp = await api.get(`/detections/${detection.id}/evidence`, { responseType: 'blob' })
    const url = URL.createObjectURL(resp.data)
    blobRef.current = url
    setBlobUrl(url)
    return url
  }, [detection.id])

  const hasAudio = detection.evidence_status === 'available'

  async function handlePlayClick() {
    if (loading || !hasAudio) return
    if (isPlaying) { onPlayClose(); return }
    setLoading(true)
    try {
      await ensureBlob()
      onPlayRequest(detection.id)
    } catch {
      // silencioso — usuário pode tentar de novo
    } finally {
      setLoading(false)
    }
  }

  return (
    <article
      className={'la-row' + (isPlaying ? ' la-row--playing' : '')}
      style={{ '--material-color': matColor }}
    >
      <button
        type="button"
        className="la-row-play"
        onClick={handlePlayClick}
        disabled={!hasAudio || loading}
        title={!hasAudio ? 'Sem áudio disponível' : (isPlaying ? 'Pausar' : 'Reproduzir')}
        aria-label={isPlaying ? 'Pausar' : `Reproduzir veiculação de ${fmtTime(detection.detected_at)} na ${detection.station_name}`}
      >
        {loading ? (
          <span className="la-row-spinner" aria-hidden />
        ) : isPlaying ? (
          <svg width="14" height="14" viewBox="0 0 14 14" fill="currentColor" aria-hidden>
            <rect x="3" y="2" width="3" height="10" rx="1" />
            <rect x="8" y="2" width="3" height="10" rx="1" />
          </svg>
        ) : (
          <svg width="14" height="14" viewBox="0 0 14 14" fill="currentColor" aria-hidden>
            <path d="M3.5 2.5v9l8-4.5z" />
          </svg>
        )}
      </button>

      <div className="la-row-time-block">
        <span className="la-row-date">{fmtDate(detection.detected_at)}</span>
        <span className="la-row-time">{fmtTime(detection.detected_at)}</span>
      </div>

      <div className="la-row-station">
        <StationAvatar
          station={{ name: detection.station_name, logo_url: detection.station_logo_url }}
          size={36}
        />
        <div className="la-row-station-text">
          <span className="la-row-station-name">
            {detection.station_name}
            {freq && <span className="la-row-station-freq"> · {detection.band} ({freq})</span>}
          </span>
          {place && <span className="la-row-station-place">{place}</span>}
        </div>
      </div>

      <div className="la-row-material">
        <span className="la-row-material-name" style={{ color: matColor }} title={detection.commercial_name}>
          {detection.commercial_name}
        </span>
        {detection.client_name && (
          <span className="la-row-material-client">{detection.client_name}</span>
        )}
      </div>

      <span className="la-row-stripe" aria-hidden />

      {isPlaying && blobUrl && (
        <div className="la-row-player">
          <AudioPlayer
            src={blobUrl}
            isPlaying={isPlaying}
            onPlay={() => onPlayRequest(detection.id)}
            onPause={() => onPlayClose()}
          />
        </div>
      )}
    </article>
  )
}

/* Skeleton shape-matched do feed. */
export function FeedSkeleton() {
  const rows = [0, 1, 2, 3, 4, 5]
  return (
    <div className="la-list">
      {rows.map((i) => (
        <div className="la-row la-row--skel" key={i}>
          <span className="la-skel la-skel-circle" style={{ width: 30, height: 30 }} />
          <div className="la-row-time-block">
            <span className="la-skel" style={{ width: 62, height: 11 }} />
            <span className="la-skel" style={{ width: 52, height: 14, marginTop: 4 }} />
          </div>
          <div className="la-row-station">
            <span className="la-skel la-skel-circle" style={{ width: 36, height: 36 }} />
            <div className="la-row-station-text">
              <span className="la-skel" style={{ width: '78%', height: 13 }} />
              <span className="la-skel" style={{ width: '46%', height: 11, marginTop: 4 }} />
            </div>
          </div>
          <div className="la-row-material">
            <span className="la-skel" style={{ width: '70%', height: 13 }} />
            <span className="la-skel" style={{ width: '40%', height: 11, marginTop: 4 }} />
          </div>
        </div>
      ))}
    </div>
  )
}
```

- [ ] **Step 2: Refatorar `LiveMapPage.jsx` para importar o componente**

Em `frontend/src/pages/LiveMapPage.jsx`:

1. Remover os imports agora cobertos pelo componente extraído **se não forem mais usados no arquivo** — atenção: `StationAvatar`, `AudioPlayer`, `api`, `materialColor` ainda podem ser usados em outros pontos do arquivo (ex.: `GHOST_FEED`/`EmptyTutorial` usa `LiveAiringRow`). Manter os que continuam referenciados.
2. Remover as definições locais de `pad2`, `fmtDate`, `fmtTime`, `freqStr`, `LiveAiringRow` e `FeedSkeleton` (linhas ~54-218).
3. Adicionar o import no topo:

```jsx
import { LiveAiringRow, FeedSkeleton } from '../components/LiveAiringRow'
```

Verificar que `useCallback`/`useRef` ainda são usados no arquivo; se ficarem órfãos após remover `LiveAiringRow`, removê-los do import de `react`. (O componente `LiveMapPage` usa `useState`, `useEffect`, `useMemo`, `useRef` para `mapRef` — `useRef` permanece; `useCallback` provavelmente fica órfão → remover.)

- [ ] **Step 3: Build do frontend**

Run: `cd frontend && npm run build`
Expected: build sem erros (sem "is not defined", sem import quebrado).

> Não rodar `npm install`/`npm i` — ver CLAUDE.md §5 (poda o lockfile no Windows e quebra o CF Pages). Só `npm run build`.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/LiveAiringRow.jsx frontend/src/pages/LiveMapPage.jsx
git commit -m "refactor(frontend): extrai LiveAiringRow/FeedSkeleton p/ componente compartilhado"
```

---

## Task 5: Frontend — hook `useManagementOverview`

**Files:**
- Modify: `frontend/src/api/hooks.js` (logo após `useLiveMap`, ~linha 537)

- [ ] **Step 1: Adicionar o hook**

Em `frontend/src/api/hooks.js`, após o fechamento de `useLiveMap` (linha 537), inserir:

```js
export function useManagementOverview({ clientId, campaignIds, status, from, to } = {}) {
  const params = {}
  if (clientId) params.client_id = clientId
  if (campaignIds && campaignIds.length) params.campaigns = campaignIds.join(',')
  if (status) params.status = status
  if (from) params.from = from
  if (to) params.to = to
  return useQuery({
    queryKey: ['management-overview', clientId || null, (campaignIds || []).join(','), status || '', from || '', to || ''],
    queryFn: () => api.get('/management-overview', { params }).then(r => r.data),
    refetchInterval: 20_000,
    placeholderData: (prev) => prev,
  })
}
```

- [ ] **Step 2: Build**

Run: `cd frontend && npm run build`
Expected: build ok.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/hooks.js
git commit -m "feat(frontend): hook useManagementOverview (filtros opcionais + refetch 20s)"
```

---

## Task 6: Frontend — CSS da página

**Files:**
- Create: `frontend/src/pages/ManagementPage.css`

> Reaproveita classes do feed do `/live-map` (`la-row`, `la-list`, `la-skel`, `lm-live`, etc.) que já existem em `LiveMapPage.css` e são globais (importadas pela página). A `ManagementPage` importa **as duas** folhas (`LiveMapPage.css` para o feed + `ManagementPage.css` para o resto), evitando duplicar o estilo do feed.

- [ ] **Step 1: Criar o CSS**

Criar `frontend/src/pages/ManagementPage.css`:

```css
.mg-page { padding: 24px 28px 48px; max-width: 1440px; margin: 0 auto; }

.mg-header { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 18px; }
.mg-title { font-family: 'Space Grotesk', sans-serif; font-weight: 700; font-size: 26px; letter-spacing: -0.02em; color: var(--color-gray-900, #06055B); margin: 0; }
.mg-live { display: inline-flex; align-items: center; gap: 8px; font-size: 13px; color: var(--color-tertiary-500, #E81E75); font-weight: 600; }
.mg-live-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--color-tertiary-500, #E81E75); box-shadow: 0 0 0 0 rgba(232,30,117,.5); animation: mg-pulse 1.8s infinite; }
.mg-refreshing { width: 12px; height: 12px; border-radius: 50%; border: 2px solid rgba(232,30,117,.25); border-top-color: var(--color-tertiary-500, #E81E75); animation: mg-spin .8s linear infinite; }
@keyframes mg-pulse { 70% { box-shadow: 0 0 0 7px rgba(232,30,117,0); } 100% { box-shadow: 0 0 0 0 rgba(232,30,117,0); } }
@keyframes mg-spin { to { transform: rotate(360deg); } }

/* Filtros */
.mg-filters { display: flex; gap: 12px; flex-wrap: wrap; background: var(--color-white, #fff); border: 1px solid var(--color-gray-200, #e2e8f0); border-radius: 16px; padding: 14px 16px; margin-bottom: 16px; box-shadow: 0 1px 2px rgba(0,0,0,.04); }
.mg-filter { flex: 1; min-width: 180px; display: flex; flex-direction: column; gap: 5px; }
.mg-filter--narrow { flex: 0 0 180px; }
.mg-filter-label { font-size: 11px; text-transform: uppercase; letter-spacing: .04em; color: var(--color-gray-500, #94a3b8); font-weight: 600; }

/* Grid principal */
.mg-grid { display: grid; grid-template-columns: 240px 1fr; gap: 16px; align-items: start; }
@media (max-width: 900px) { .mg-grid { grid-template-columns: 1fr; } }

/* KPIs */
.mg-kpis { display: flex; flex-direction: column; gap: 12px; }
@media (max-width: 900px) { .mg-kpis { flex-direction: row; flex-wrap: wrap; } .mg-kpi { flex: 1 1 160px; } }
.mg-kpi { background: var(--color-white, #fff); border: 1px solid var(--color-gray-200, #e2e8f0); border-radius: 16px; padding: 16px; box-shadow: 0 1px 2px rgba(0,0,0,.04); position: relative; overflow: hidden; transition: transform .15s ease, box-shadow .15s ease, border-color .15s ease; }
.mg-kpi:hover { transform: translateY(-2px); box-shadow: 0 8px 22px rgba(0,0,0,.08); border-color: var(--color-tertiary-300, #f6a8cd); }
.mg-kpi-ic { position: absolute; top: 14px; right: 14px; width: 30px; height: 30px; border-radius: 9px; background: #fbe3ee; display: flex; align-items: center; justify-content: center; color: var(--color-tertiary-500, #E81E75); }
.mg-kpi-n { font-family: 'Space Grotesk', sans-serif; font-weight: 700; font-size: 28px; line-height: 1; color: var(--color-gray-900, #06055B); }
.mg-kpi--live .mg-kpi-n { color: var(--color-tertiary-500, #E81E75); }
.mg-kpi-l { font-size: 12px; color: var(--color-gray-600, #475569); margin-top: 6px; font-weight: 600; }
.mg-kpi-s { font-size: 11px; color: var(--color-gray-500, #94a3b8); margin-top: 6px; }

/* Coluna direita: mapa + feed */
.mg-right { display: flex; flex-direction: column; gap: 16px; min-width: 0; }
.mg-card { background: var(--color-white, #fff); border: 1px solid var(--color-gray-200, #e2e8f0); border-radius: 16px; padding: 14px 16px; box-shadow: 0 1px 2px rgba(0,0,0,.04); }
.mg-card-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 10px; }
.mg-card-title { font-size: 14px; font-weight: 600; color: var(--color-gray-900, #06055B); }
.mg-card-meta { display: inline-flex; align-items: center; gap: 12px; font-size: 12px; color: var(--color-gray-500, #94a3b8); }

.mg-map-dl { display: inline-flex; align-items: center; gap: 5px; background: transparent; border: 1px solid var(--color-gray-200, #e2e8f0); border-radius: 999px; padding: 4px 10px; font-size: 11px; color: var(--color-gray-700, #334155); cursor: pointer; transition: border-color .15s, color .15s; }
.mg-map-dl:hover { border-color: var(--color-tertiary-400, #ee7eb5); color: var(--color-tertiary-500, #E81E75); }
.mg-map-dl:disabled { opacity: .5; cursor: default; }

.mg-map-skeleton { height: 360px; border-radius: 12px; overflow: hidden; }
.mg-map-skeleton-shape { width: 100%; height: 100%; display: block; }
.mg-map-empty { position: relative; }
.mg-map-empty-msg { display: block; text-align: center; font-size: 12px; color: var(--color-gray-500, #94a3b8); margin-top: 8px; }

.mg-feed-count { background: var(--color-gray-100, #f1f5f9); color: var(--color-gray-600, #475569); border-radius: 999px; padding: 2px 9px; font-size: 11px; font-weight: 600; }
.mg-feed-empty { text-align: center; color: var(--color-gray-500, #94a3b8); font-size: 13px; padding: 28px 0; }

/* Estados */
.mg-error { display: flex; flex-direction: column; align-items: center; gap: 12px; padding: 60px 0; color: var(--color-gray-500, #94a3b8); }
.mg-error-title { font-weight: 600; color: var(--color-gray-700, #334155); }

@media (prefers-reduced-motion: reduce) {
  .mg-live-dot, .mg-refreshing { animation: none; }
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/pages/ManagementPage.css
git commit -m "feat(frontend): estilos da Visão Gerencial"
```

---

## Task 7: Frontend — página `ManagementPage.jsx`

**Files:**
- Create: `frontend/src/pages/ManagementPage.jsx`

- [ ] **Step 1: Criar a página**

Criar `frontend/src/pages/ManagementPage.jsx`:

```jsx
import { useMemo, useRef, useState } from 'react'
import html2canvas from 'html2canvas'
import RSelect from '../components/RSelect'
import BrazilMap from '../components/BrazilMap'
import { LiveAiringRow, FeedSkeleton } from '../components/LiveAiringRow'
import { useClients, useCampaignsPaged, useManagementOverview } from '../api/hooks'
import { safeLogoUrl } from '../utils/logoUrl'
import './LiveMapPage.css'
import './ManagementPage.css'

const STATUS_OPTS = [
  { value: 'ativa', label: 'Ativa' },
  { value: 'programada', label: 'Programada' },
  { value: 'concluida', label: 'Concluída' },
  { value: 'cancelada', label: 'Cancelada' },
]

function ClientMiniAvatar({ name = '', logo = null, size = 22 }) {
  const [imgErr, setImgErr] = useState(false)
  const safe = safeLogoUrl(logo)
  if (safe && !imgErr) {
    return (
      <img src={safe} alt={name} width={size} height={size}
        className="lm-client-avatar lm-client-avatar--img"
        style={{ width: size, height: size }} onError={() => setImgErr(true)} />
    )
  }
  const initials = name.trim().split(/\s+/).slice(0, 2).map(w => w[0]).join('').toUpperCase() || '?'
  return (
    <div className="lm-client-avatar lm-client-avatar--fb"
      style={{ width: size, height: size, fontSize: Math.round(size * 0.42) }}>
      {initials}
    </div>
  )
}

function formatClientOption(opt, { context }) {
  const size = context === 'value' ? 18 : 22
  return (
    <div className="lm-client-option">
      <ClientMiniAvatar name={opt.label} logo={opt.raw?.logo_url} size={size} />
      <span className="lm-client-option-label">{opt.label}</span>
    </div>
  )
}

function nf(n) { return new Intl.NumberFormat('pt-BR').format(n ?? 0) }

function KpiCard({ value, label, sub, live, icon }) {
  return (
    <div className={'mg-kpi' + (live ? ' mg-kpi--live' : '')}>
      <span className="mg-kpi-ic" aria-hidden>{icon}</span>
      <div className="mg-kpi-n">{value}</div>
      <div className="mg-kpi-l">{label}</div>
      {sub && <div className="mg-kpi-s">{sub}</div>}
    </div>
  )
}

const ICON_MAP = (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M8 14s5-4.5 5-8A5 5 0 0 0 3 6c0 3.5 5 8 5 8z" /><circle cx="8" cy="6" r="1.75" /></svg>
)
const ICON_LIVE = (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="8" cy="8" r="2" /><path d="M5.2 5.2a4 4 0 0 0 0 5.6M10.8 5.2a4 4 0 0 1 0 5.6" /></svg>
)
const ICON_MAT = (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M6 13V4l7-1.2V11" /><circle cx="4" cy="13" r="2" /><circle cx="11" cy="11" r="2" /></svg>
)
const ICON_AIR = (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><circle cx="8" cy="8" r="6" /><path d="M5.5 8l1.5 1.5L10.5 6" /></svg>
)

export default function ManagementPage() {
  const [clientId, setClientId] = useState(null)
  const [campaignIds, setCampaignIds] = useState([])
  const [status, setStatus] = useState(null)
  const [playingId, setPlayingId] = useState(null)
  const [downloading, setDownloading] = useState(false)
  const mapRef = useRef(null)

  // Período default = ano corrente (01/01 → hoje). Datas em ISO YYYY-MM-DD.
  const year = new Date().getFullYear()
  const [from, setFrom] = useState(`${year}-01-01`)
  const [to, setTo] = useState(() => {
    const d = new Date()
    const p = (n) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
  })

  const clientsQ = useClients()
  const clientOpts = useMemo(
    () => (clientsQ.data || []).map(c => ({ value: c.id, label: c.name, raw: c })),
    [clientsQ.data],
  )

  const campaignsQ = useCampaignsPaged({ page: 1, pageSize: 200 })
  const allCampaigns = useMemo(() => campaignsQ.data?.data || [], [campaignsQ.data])
  const campOpts = useMemo(() => {
    const rows = clientId ? allCampaigns.filter(c => c.client_id === clientId) : allCampaigns
    return rows.map(c => ({ value: c.id, label: c.name }))
  }, [allCampaigns, clientId])

  const { data, isLoading, isError, isFetching, refetch } = useManagementOverview({
    clientId, campaignIds, status, from, to,
  })

  const kpis = data?.kpis ?? {}
  const stations = useMemo(() => data?.stations ?? [], [data])
  const detections = useMemo(() => data?.recent_detections ?? [], [data])
  const livePct = kpis.stations_monitored
    ? Math.round((kpis.stations_live / kpis.stations_monitored) * 100)
    : 0

  async function handleDownloadMap() {
    const el = mapRef.current
    if (!el || downloading) return
    setDownloading(true)
    try {
      const canvas = await html2canvas(el, { backgroundColor: '#ffffff', scale: 2, logging: false })
      canvas.toBlob((blob) => {
        if (!blob) return
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        const ts = new Date().toISOString().slice(0, 10)
        a.download = `visao-gerencial-${ts}.png`
        document.body.appendChild(a)
        a.click()
        document.body.removeChild(a)
        setTimeout(() => URL.revokeObjectURL(url), 1500)
      }, 'image/png')
    } finally {
      setDownloading(false)
    }
  }

  return (
    <div className="mg-page">
      <header className="mg-header">
        <h1 className="mg-title">Visão Gerencial</h1>
        {!isLoading && !isError && (
          <span className="mg-live">
            <span className="mg-live-dot" />
            ao vivo · atualiza a cada 20s
            {isFetching && <span className="mg-refreshing" aria-label="atualizando" />}
          </span>
        )}
      </header>

      <div className="mg-filters">
        <div className="mg-filter">
          <label className="mg-filter-label">Cliente</label>
          <RSelect
            options={clientOpts}
            value={clientOpts.find(o => o.value === clientId) || null}
            onChange={opt => { setClientId(opt?.value || null); setCampaignIds([]) }}
            placeholder="Todos os clientes"
            isLoading={clientsQ.isPending}
            isClearable
            formatOptionLabel={formatClientOption}
          />
        </div>
        <div className="mg-filter">
          <label className="mg-filter-label">Campanhas</label>
          <RSelect
            options={campOpts}
            value={campOpts.filter(o => campaignIds.includes(o.value))}
            onChange={opts => setCampaignIds((opts || []).map(o => o.value))}
            placeholder="Todas as campanhas"
            isLoading={campaignsQ.isPending}
            isMulti
            isClearable
          />
        </div>
        <div className="mg-filter mg-filter--narrow">
          <label className="mg-filter-label">De</label>
          <input type="date" className="field" value={from} max={to} onChange={e => setFrom(e.target.value)} />
        </div>
        <div className="mg-filter mg-filter--narrow">
          <label className="mg-filter-label">Até</label>
          <input type="date" className="field" value={to} min={from} onChange={e => setTo(e.target.value)} />
        </div>
        <div className="mg-filter mg-filter--narrow">
          <label className="mg-filter-label">Status</label>
          <RSelect
            options={STATUS_OPTS}
            value={STATUS_OPTS.find(o => o.value === status) || null}
            onChange={opt => setStatus(opt?.value || null)}
            placeholder="Todos"
            isClearable
          />
        </div>
      </div>

      {isError && !data ? (
        <div className="mg-error">
          <svg viewBox="0 0 24 24" width="38" height="38" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"><path d="M12 9v4M12 17h.01" /><path d="M10.3 3.9 2.4 18a2 2 0 0 0 1.7 3h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" /></svg>
          <p className="mg-error-title">Não foi possível carregar a visão gerencial</p>
          <button className="btn btn-secondary" onClick={() => refetch()}>Tentar de novo</button>
        </div>
      ) : (
        <div className="mg-grid">
          <div className="mg-kpis">
            <KpiCard icon={ICON_MAP} value={isLoading ? '—' : nf(kpis.stations_monitored)}
              label="Emissoras monitoradas"
              sub={isLoading ? '' : `no período · ${nf(kpis.states_count)} estado${kpis.states_count === 1 ? '' : 's'}`} />
            <KpiCard icon={ICON_LIVE} live value={isLoading ? '—' : nf(kpis.stations_live)}
              label="Monitorando agora"
              sub={isLoading ? '' : `worker saudável · ${livePct}% online`} />
            <KpiCard icon={ICON_MAT} value={isLoading ? '—' : nf(kpis.materials_monitored)}
              label="Materiais monitorados"
              sub={isLoading ? '' : `em ${nf(kpis.campaigns_count)} campanha${kpis.campaigns_count === 1 ? '' : 's'}`} />
            <KpiCard icon={ICON_AIR} value={isLoading ? '—' : nf(kpis.airings_total)}
              label="Veiculações no período"
              sub={isLoading ? '' : `+${nf(kpis.airings_today)} hoje`} />
          </div>

          <div className="mg-right">
            <section className="mg-card">
              <div className="mg-card-head">
                <span className="mg-card-title">Emissoras monitoradas</span>
                <span className="mg-card-meta">
                  {!isLoading && stations.length > 0 && (
                    <span>{nf(kpis.stations_live)} ao vivo</span>
                  )}
                  {!isLoading && stations.length > 0 && (
                    <button type="button" className="mg-map-dl" onClick={handleDownloadMap} disabled={downloading}
                      title="Baixar imagem do mapa" aria-label="Baixar imagem do mapa">
                      {downloading ? <span className="la-row-spinner" aria-hidden /> : (
                        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden><path d="M8 2v8M4.5 6.5L8 10l3.5-3.5" /><path d="M3 12v1.5A1.5 1.5 0 0 0 4.5 15h7a1.5 1.5 0 0 0 1.5-1.5V12" /></svg>
                      )}
                      <span>Baixar</span>
                    </button>
                  )}
                </span>
              </div>
              {isLoading ? (
                <div className="mg-map-skeleton"><div className="la-skel mg-map-skeleton-shape" /></div>
              ) : stations.length === 0 ? (
                <div className="mg-map-empty" ref={mapRef}>
                  <BrazilMap stations={[]} />
                  <span className="mg-map-empty-msg">Nenhuma emissora monitorada no filtro/período selecionado.</span>
                </div>
              ) : (
                <div ref={mapRef}><BrazilMap stations={stations} /></div>
              )}
            </section>

            <section className="mg-card">
              <div className="mg-card-head">
                <span className="mg-card-title">Veiculações ao vivo · todas as campanhas</span>
                {!isLoading && <span className="mg-feed-count">{detections.length}</span>}
              </div>
              {isLoading ? (
                <FeedSkeleton />
              ) : detections.length === 0 ? (
                <div className="mg-feed-empty">Nenhuma veiculação recente no recorte selecionado.</div>
              ) : (
                <div className="la-list la-stagger">
                  {detections.map(d => (
                    <LiveAiringRow key={d.id} detection={d}
                      isPlaying={playingId === d.id}
                      onPlayRequest={(id) => setPlayingId(id)}
                      onPlayClose={() => setPlayingId(null)} />
                  ))}
                </div>
              )}
            </section>
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Build**

Run: `cd frontend && npm run build`
Expected: build ok.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/ManagementPage.jsx
git commit -m "feat(frontend): página Visão Gerencial (/management) — KPIs + mapa + feed"
```

---

## Task 8: Frontend — rota + item na sidebar

**Files:**
- Modify: `frontend/src/App.jsx` (import + rota)
- Modify: `frontend/src/components/Sidebar.jsx` (ícone + item)

- [ ] **Step 1: Import + rota no `App.jsx`**

Em `frontend/src/App.jsx`, junto aos outros imports de páginas (perto da linha 32, `import LiveMapPage from './pages/LiveMapPage'`):

```jsx
import ManagementPage from './pages/ManagementPage'
```

E entre as rotas de admin (após `/admin/overview`, linha 130), adicionar:

```jsx
            <Route path="/management" element={
              <RequireRole roles={['admin']}><ManagementPage /></RequireRole>
            } />
```

- [ ] **Step 2: Ícone + item na sidebar**

Em `frontend/src/components/Sidebar.jsx`, adicionar o ícone junto aos outros (ex.: após `IconAdminOverview`, linha 113):

```jsx
function IconManagement() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M8 14s5-4.5 5-8A5 5 0 0 0 3 6c0 3.5 5 8 5 8z" />
      <circle cx="8" cy="6" r="1.75" />
      <path d="M2 2.5l1.6 1.6M13.4 2.5l-1.6 1.6" strokeOpacity="0.5" />
    </svg>
  )
}
```

E no `AdminNav`, na seção **Administração**, logo após o item "Visão geral" (`/admin/overview`, linha 213):

```jsx
      <SidebarLink to="/management"      icon={<IconManagement />}      onClose={onClose}>Visão Gerencial</SidebarLink>
```

(NÃO adicionar ao `ClientNav` — a tela é admin-only.)

- [ ] **Step 3: Build**

Run: `cd frontend && npm run build`
Expected: build ok.

- [ ] **Step 4: Verificação manual (opcional, recomendado)**

Subir o frontend (`cd frontend && npm run dev`), logar como admin, abrir `/management`. Conferir: abre sem exigir seleção (traz tudo do ano), KPIs preenchidos, mapa pulsa, feed corre, filtros funcionam (cliente filtra campanhas; período recalcula KPIs; status filtra). Logar como cliente → item não aparece na sidebar e `/management` redireciona/bloqueia.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/App.jsx frontend/src/components/Sidebar.jsx
git commit -m "feat(frontend): rota /management + item Visão Gerencial na sidebar admin"
```

---

## Task 9: Documentação

**Files:**
- Create: `docs/features/management-overview.md`
- Modify: `CLAUDE.md` (mapa de consulta)

- [ ] **Step 1: Criar o doc da feature**

Criar `docs/features/management-overview.md`:

```markdown
---
status: implementado
ultima-verificacao: 2026-05-29
codigo-relacionado:
  - workers/internal/catalog/management_overview.go
  - workers/internal/api/handlers/management_overview.go
  - workers/internal/api/router.go
  - frontend/src/pages/ManagementPage.jsx
  - frontend/src/pages/ManagementPage.css
  - frontend/src/components/LiveAiringRow.jsx
  - frontend/src/api/hooks.js
---

# Visão Gerencial (/management)

Painel **admin/operator-only** com uma visão da operação inteira da plataforma —
todas as campanhas, de todos os clientes. Diferente do `/live-map` (que exige
Cliente → Campanha antes de mostrar algo), abre **já trazendo tudo** e oferece
filtros opcionais.

## Acesso

Admin/operator. Rota `/management` gateada por `RequireRole roles={['admin']}` no
frontend; endpoint registrado no subgrupo admin/operator do router (sem scope de
viewer). Não aparece no `ClientNav`.

## Layout

Filtros no topo + split de duas colunas: KPIs empilhados à esquerda; mapa do
Brasil (pulsando) + feed global ao vivo à direita. Reusa `BrazilMap`,
`RSelect` e a linha de feed compartilhada (`components/LiveAiringRow.jsx`,
extraída do `/live-map`).

## KPIs

- **Emissoras monitoradas:** emissoras-alvo distintas das campanhas no recorte.
- **Monitorando agora:** subconjunto com `health_status='ok'` neste instante
  (sempre tempo real).
- **Materiais monitorados:** materiais distintos vinculados (`campaign_materials`).
- **Veiculações no período:** detecções confirmadas no período (+ "hoje").

## Escopo vs. período

Os filtros (cliente/campanhas/status + sobreposição de período) definem **quais
campanhas** entram. Sobre elas, o **mapa** e o **feed** são sempre "agora"; o
**período** só limita `airings_total`. Default de período = ano corrente.

## Fonte de dados

`GET /v1/internal/management-overview` com params opcionais `client_id`,
`campaigns` (csv), `status`, `from`, `to`. Repo `catalog.ManagementOverview`
roda 3 queries (KPIs, stations, recent detections) sobre uma CTE `scoped`
comum. Frontend: `useManagementOverview` (react-query, refetch 20s,
`placeholderData`).

## Performance

É a consulta mais pesada do sistema. V1 = queries diretas sobre os índices
existentes. Medir antes de otimizar; se necessário, cache curto ou tabela de
agregação (não pré-otimizado).
```

- [ ] **Step 2: Adicionar linha no mapa de consulta do `CLAUDE.md`**

Em `CLAUDE.md`, na tabela "Mapa de consulta", após a linha do `admin-system-overview`, inserir:

```markdown
| Visão Gerencial `/management` (painel admin da operação inteira — KPIs cross-campanha + mapa + feed global ao vivo, filtros opcionais) | [docs/features/management-overview.md](docs/features/management-overview.md) |
```

- [ ] **Step 3: Commit**

```bash
git add docs/features/management-overview.md CLAUDE.md
git commit -m "docs: feature Visão Gerencial + entrada no mapa de consulta"
```

---

## Verificação final

- [ ] `cd workers && go build ./... && go test ./...` — backend compila e testes passam.
- [ ] `cd frontend && npm run build` — frontend builda (sem `npm install`; ver CLAUDE.md §5).
- [ ] `git show master:frontend/package-lock.json | grep -c emnapi` vs `grep -c emnapi frontend/package-lock.json` — número **não caiu** (não tocamos o lockfile; checagem de segurança).
- [ ] Manual: admin abre `/management` e vê tudo do ano sem selecionar nada; filtros funcionam; cliente não vê o item nem acessa a rota.

## Notas de escopo (do spec)

- Sem charts (decisão explícita).
- Cliente não acessa.
- Sem reconhecimento de música/transcrição/over-the-air (Não Objetivos §1.3).
- Sem pré-otimização de performance.
