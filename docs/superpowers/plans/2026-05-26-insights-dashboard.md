# Dashboard de Veiculação (`/insights`) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Adicionar uma tela única `/insights` que consolida KPIs e gráficos demográficos por cliente × campanha(s) × período × emissoras, acessível a admin e cliente (este último limitado às próprias campanhas), com exportação para PNG e PDF.

**Architecture:** Backend Go expõe `GET /api/v1/insights` que devolve um payload agregado (1 round-trip ao banco com 5 SELECTs auxiliares). Frontend React (React Query) consome esse payload e renderiza com Recharts. Anti-oracle no backend via `auth.ClientScopeFromContext()`.

**Tech Stack:** Go (chi, pgx), React 19, React Query 5, Recharts (nova dep), html2canvas (nova dep), jsPDF (existente). Reusa `RSelect`, `SmartImage`, `ConfirmProvider`, padrões existentes do projeto.

**Reference Spec:** [docs/superpowers/specs/2026-05-26-insights-dashboard-design.md](../specs/2026-05-26-insights-dashboard-design.md) — leia antes de começar. As fórmulas em §4.4 são autoridade.

---

## File Structure

**Novos arquivos:**

| Path | Responsabilidade |
|---|---|
| `workers/internal/catalog/insights.go` | Repo SQL: 1 função pública `Compute(ctx, params)` que orquestra 5 helpers privados (campanhas-em-escopo, agregados por categoria/demografia, investimento, buckets temporais) |
| `workers/internal/catalog/insights_test.go` | Testes do repo com seed de DB |
| `workers/internal/api/handlers/insights.go` | Handler HTTP: parsing de query, role-gating, chamada do repo, serialização JSON |
| `workers/internal/api/handlers/insights_test.go` | Testes do handler com fake repo |
| `frontend/src/pages/InsightsPage.jsx` | Página principal — layout 3 rows |
| `frontend/src/pages/InsightsPage.css` | Estilos da página (prefix `.in-`) |
| `frontend/src/components/insights/FiltersBar.jsx` | Barra de filtros (cliente, campanhas, período, emissoras, export) |
| `frontend/src/components/insights/KpiCards.jsx` | Row 1 — 5 cards de KPI |
| `frontend/src/components/insights/InvestmentToggleCard.jsx` | Card 4 com toggle Contratado/Executado |
| `frontend/src/components/insights/GenderCard.jsx` | Card 5 — barra horizontal stacked M/F |
| `frontend/src/components/insights/ClassPyramidChart.jsx` | Gráfico 1 — pirâmide de classe social |
| `frontend/src/components/insights/AgeRangeChart.jsx` | Gráfico 2 — faixa etária |
| `frontend/src/components/insights/BroadcastShareChart.jsx` | Gráfico 3 — donut de % veiculações |
| `frontend/src/components/insights/DailySummaryChart.jsx` | Gráfico 4 — bucket diário/mensal |
| `frontend/src/components/insights/CustomTooltip.jsx` | Tooltip glassmorphism compartilhado |
| `frontend/src/components/insights/EmptyTutorial.jsx` | Empty state "Tutorial Estilizado" |
| `frontend/src/utils/exportInsights.js` | Export PNG (html2canvas) + PDF (jsPDF) |
| `docs/features/insights-dashboard.md` | Doc operacional |

**Modificações:**

| Path | Mudança |
|---|---|
| `workers/internal/api/router.go` | Adicionar rota `GET /insights` no Subgrupo A (viewer) |
| `workers/cmd/api/main.go` | Wire `Insights: handlers.NewInsightsHandler(...)` em Deps |
| `frontend/package.json` | + `recharts`, + `html2canvas` |
| `frontend/src/App.jsx` | + `<Route path="/insights" element={<InsightsPage />} />` |
| `frontend/src/components/Sidebar.jsx` | + Link "Dashboard" no grupo Veiculação (Admin **e** Client navs) |
| `frontend/src/api/hooks.js` | + `useInsights(params)` |
| `docs/README.md` | + linha na seção "Quando você for mexer em..." apontando pro doc novo |

---

### Task 1: Adicionar dependências Recharts e html2canvas

**Files:**
- Modify: `frontend/package.json`

- [ ] **Step 1: Instalar as libs**

```bash
cd frontend && npm install recharts html2canvas
```

Esperado: `package.json` ganha `"recharts": "^2.x"` e `"html2canvas": "^1.4.x"`. `package-lock.json` atualizado.

- [ ] **Step 2: Smoke check de instalação**

```bash
cd frontend && node -e "console.log(require('recharts').BarChart && require('html2canvas') ? 'ok' : 'fail')"
```

Esperado: imprime `ok`.

- [ ] **Step 3: Commit**

```bash
git add frontend/package.json frontend/package-lock.json
git commit -m "chore(frontend): add recharts + html2canvas for /insights dashboard"
```

---

### Task 2: Backend — Schema dos tipos do payload

Define os structs Go que serão serializados como JSON. Sem SQL ainda, só os tipos. Permite que handler e repo sejam codados em paralelo.

**Files:**
- Create: `workers/internal/catalog/insights.go`

- [ ] **Step 1: Criar arquivo com structs**

```go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Insights agrupa SQL de agregação para o dashboard /insights.
type Insights struct {
	pool *pgxpool.Pool
}

func NewInsights(pool *pgxpool.Pool) *Insights {
	return &Insights{pool: pool}
}

// InsightsParams são os filtros validados aplicados ao cálculo.
type InsightsParams struct {
	ClientID    uuid.UUID
	CampaignIDs []uuid.UUID
	From        time.Time // inclusive (date-only, UTC 00:00)
	To          time.Time // inclusive (date-only, UTC 23:59:59)
	StationIDs  []uuid.UUID // vazio = todas as estações das campanhas
}

// InsightsPayload é o response completo do endpoint.
type InsightsPayload struct {
	Period               PeriodSpec               `json:"period"`
	Campaigns            []CampaignBrief          `json:"campaigns"`
	KPIs                 InsightsKPIs             `json:"kpis"`
	ClassPyramid         ClassPyramidData         `json:"class_pyramid"`
	AgeRanges            AgeRangesData            `json:"age_ranges"`
	VeiculacoesBreakdown VeiculacoesBreakdownData `json:"veiculacoes_breakdown"`
	Buckets              []BucketRow              `json:"buckets"`
}

type PeriodSpec struct {
	From        string `json:"from"` // YYYY-MM-DD
	To          string `json:"to"`
	Granularity string `json:"granularity"` // "day" | "month"
}

type CampaignBrief struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	StartDate string    `json:"start_date"` // YYYY-MM-DD
	EndDate   string    `json:"end_date"`
}

type InsightsKPIs struct {
	Impactos         int64        `json:"impactos"`
	VeiculacoesTotal int64        `json:"veiculacoes_total"`
	StationsCount    int          `json:"stations_count"`
	StationsWithPMM  int          `json:"stations_with_pmm"`
	CPM              float64      `json:"cpm"`
	Bonificacao      BonificacaoK `json:"bonificacao"`
	Investido        InvestidoK   `json:"investido"`
	Gender           GenderK      `json:"gender"`
}

type BonificacaoK struct {
	Valor float64 `json:"valor"`
	Count int64   `json:"count"`
}

type InvestidoK struct {
	Contratado float64 `json:"contratado"`
	Executado  float64 `json:"executado"`
}

type GenderK struct {
	M int64 `json:"m"`
	F int64 `json:"f"`
}

type ClassPyramidData struct {
	AB int64 `json:"ab"`
	C  int64 `json:"c"`
	DE int64 `json:"de"`
}

type AgeRangesData struct {
	R18_24  int64 `json:"r18_24"`
	R25_49  int64 `json:"r25_49"`
	R50Plus int64 `json:"r50_plus"`
}

type VeiculacoesBreakdownData struct {
	InSlot       int64 `json:"in_slot"`
	OutSlot      int64 `json:"out_slot"`
	OutDate      int64 `json:"out_date"`
	ExtrasOrphan int64 `json:"extras_orphan"`
}

type BucketRow struct {
	Bucket     string `json:"bucket"` // "YYYY-MM-DD" diário ou "YYYY-MM" mensal
	Programado int    `json:"programado"`
	InSlot     int    `json:"in_slot"`
	OutSlot    int    `json:"out_slot"`
	OutDate    int    `json:"out_date"`
	Deficit    int    `json:"deficit"`
	Extras     int    `json:"extras"`
}

// Compute é o entry-point do repo. Devolve o payload completo.
func (r *Insights) Compute(ctx context.Context, p InsightsParams) (*InsightsPayload, error) {
	return nil, nil // implementado nas tasks 3-7
}
```

- [ ] **Step 2: Build check**

```bash
cd workers && go build ./internal/catalog/...
```

Esperado: build limpo, zero output.

- [ ] **Step 3: Commit**

```bash
git add workers/internal/catalog/insights.go
git commit -m "feat(insights): add repo struct skeleton + payload types"
```

---

### Task 3: Backend — Validar campanhas (anti-oracle) + buscar metadata

Garante que toda campaign_id pedida pertence ao client_id (admin pode passar qualquer client; cliente é forçado pelo JWT no handler). Devolve as campanhas com nome/datas para o payload.

**Files:**
- Modify: `workers/internal/catalog/insights.go`
- Create: `workers/internal/catalog/insights_test.go`

- [ ] **Step 1: Escrever o teste primeiro**

```go
package catalog

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Helper compartilhado para criar pool de teste — espelha o padrão dos
// outros *_test.go no pacote (ver e.g. campaigns_test.go).
//
// Convenção: teste pula com t.Skip se TEST_DATABASE_URL não estiver setada.

func TestInsights_FetchCampaigns_RejectsCrossClient(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := NewInsights(pool)
	clientA := seedClient(t, pool, "Cliente A")
	clientB := seedClient(t, pool, "Cliente B")
	campA := seedCampaign(t, pool, clientA, "2026-06-01", "2026-06-30")
	campB := seedCampaign(t, pool, clientB, "2026-06-01", "2026-06-30")

	// Cliente A pedindo campanha do cliente B → erro
	_, err := repo.fetchCampaigns(context.Background(), clientA, []uuid.UUID{campA, campB})
	if err == nil {
		t.Fatal("expected error when requesting cross-client campaign, got nil")
	}
}

func TestInsights_FetchCampaigns_ReturnsBriefs(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := NewInsights(pool)
	client := seedClient(t, pool, "Cliente X")
	c1 := seedCampaign(t, pool, client, "2026-06-01", "2026-06-30")
	c2 := seedCampaign(t, pool, client, "2026-07-01", "2026-07-31")

	briefs, err := repo.fetchCampaigns(context.Background(), client, []uuid.UUID{c1, c2})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(briefs) != 2 {
		t.Fatalf("got %d briefs, want 2", len(briefs))
	}
	if briefs[0].StartDate != "2026-06-01" {
		t.Fatalf("expected 2026-06-01, got %q", briefs[0].StartDate)
	}
}
```

Nota: `testPool`, `seedClient`, `seedCampaign` já existem em outros `*_test.go` do pacote (ver `campaigns_test.go` ou `detections_test.go`). Se não existirem como helpers compartilhados, copie inline e marque para refatorar depois.

- [ ] **Step 2: Rodar o teste e verificar que falha**

```bash
cd workers && TEST_DATABASE_URL=$DATABASE_URL go test ./internal/catalog/ -run TestInsights_FetchCampaigns -v
```

Esperado: FAIL com "undefined: fetchCampaigns" ou similar.

- [ ] **Step 3: Implementar `fetchCampaigns`**

```go
// Em insights.go — adicionar abaixo de Compute():

func (r *Insights) fetchCampaigns(ctx context.Context, clientID uuid.UUID, ids []uuid.UUID) ([]CampaignBrief, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, start_date::text, end_date::text
		FROM campaigns
		WHERE id = ANY($1::uuid[]) AND client_id = $2
		ORDER BY start_date ASC
	`, ids, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CampaignBrief
	for rows.Next() {
		var b CampaignBrief
		if err := rows.Scan(&b.ID, &b.Name, &b.StartDate, &b.EndDate); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) != len(ids) {
		return nil, fmt.Errorf("insights: %d campaigns requested, %d found for client (cross-client or invalid id)", len(ids), len(out))
	}
	return out, nil
}
```

Adicione `"fmt"` ao bloco de imports.

- [ ] **Step 4: Rodar testes — devem passar**

```bash
cd workers && TEST_DATABASE_URL=$DATABASE_URL go test ./internal/catalog/ -run TestInsights_FetchCampaigns -v
```

Esperado: PASS em ambos.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/insights.go workers/internal/catalog/insights_test.go
git commit -m "feat(insights): fetch campaigns with anti-oracle ownership check"
```

---

### Task 4: Backend — Agregação principal (impactos, demografia, breakdown)

Calcula em **uma única query SQL** com CTE: total de detecções por estação × categoria, multiplicado pelo PMM e % do perfil de cada estação. Devolve impactos totais, gender split, class pyramid, age ranges e veiculações breakdown.

**Files:**
- Modify: `workers/internal/catalog/insights.go`
- Modify: `workers/internal/catalog/insights_test.go`

- [ ] **Step 1: Escrever testes**

```go
func TestInsights_AggregateCore_Impactos(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := NewInsights(pool)
	client := seedClient(t, pool, "X")
	camp := seedCampaign(t, pool, client, "2026-06-01", "2026-06-30")

	// Estação com PMM=1000, 60%M / 40%F, AB=20% C=50% DE=30%,
	// idades 30/50/20%.
	st := seedStationWithProfile(t, pool, "Rádio X", 1000,
		0.60, 0.40, 0.20, 0.50, 0.30, 0.30, 0.50, 0.20)

	// 5 detections in_slot
	for i := 0; i < 5; i++ {
		seedDetection(t, pool, camp, st, "in_slot", "2026-06-15")
	}
	// 2 out_slot
	for i := 0; i < 2; i++ {
		seedDetection(t, pool, camp, st, "out_slot", "2026-06-16")
	}
	// 1 orphan
	seedDetection(t, pool, camp, st, "orphan", "2026-06-17")

	from, _ := time.Parse("2006-01-02", "2026-06-01")
	to, _ := time.Parse("2006-01-02", "2026-06-30")
	core, err := repo.aggregateCore(context.Background(), InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp}, From: from, To: to,
	})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	// 8 detections × 1000 = 8000 impactos
	if core.Impactos != 8000 {
		t.Fatalf("impactos = %d, want 8000", core.Impactos)
	}
	// Gender M = 8000 × 0.60 = 4800
	if core.Gender.M != 4800 {
		t.Fatalf("gender.m = %d, want 4800", core.Gender.M)
	}
	if core.Gender.F != 3200 {
		t.Fatalf("gender.f = %d, want 3200", core.Gender.F)
	}
	// Breakdown
	if core.Breakdown.InSlot != 5 {
		t.Fatalf("in_slot = %d, want 5", core.Breakdown.InSlot)
	}
	if core.Breakdown.OutSlot != 2 {
		t.Fatalf("out_slot = %d, want 2", core.Breakdown.OutSlot)
	}
	if core.Breakdown.ExtrasOrphan != 1 {
		t.Fatalf("extras_orphan = %d, want 1", core.Breakdown.ExtrasOrphan)
	}
}

func TestInsights_AggregateCore_StationWithoutPMM_ExcludedFromImpactos(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := NewInsights(pool)
	client := seedClient(t, pool, "X")
	camp := seedCampaign(t, pool, client, "2026-06-01", "2026-06-30")

	stOK := seedStationWithProfile(t, pool, "OK", 1000, 0.5, 0.5, 0.3, 0.4, 0.3, 0.3, 0.4, 0.3)
	stNoPMM := seedStationNoProfile(t, pool, "SemPerfil")

	seedDetection(t, pool, camp, stOK, "in_slot", "2026-06-15")
	seedDetection(t, pool, camp, stNoPMM, "in_slot", "2026-06-15") // não soma em impactos
	seedDetection(t, pool, camp, stNoPMM, "in_slot", "2026-06-16") // mas conta como veiculação

	from, _ := time.Parse("2006-01-02", "2026-06-01")
	to, _ := time.Parse("2006-01-02", "2026-06-30")
	core, _ := repo.aggregateCore(context.Background(), InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp}, From: from, To: to,
	})

	if core.Impactos != 1000 { // só o stOK contribui
		t.Fatalf("impactos = %d, want 1000", core.Impactos)
	}
	if core.VeiculacoesTotal != 3 { // todas contam
		t.Fatalf("veiculacoes = %d, want 3", core.VeiculacoesTotal)
	}
	if core.StationsCount != 2 {
		t.Fatalf("stations = %d, want 2", core.StationsCount)
	}
	if core.StationsWithPMM != 1 {
		t.Fatalf("stations_with_pmm = %d, want 1", core.StationsWithPMM)
	}
}
```

Helpers de seed `seedStationWithProfile`, `seedStationNoProfile`, `seedDetection` — se não existirem, criar inline neste arquivo. Estrutura sugerida:

```go
// seedStationWithProfile cria uma estação com PMM e perfil demográfico completo.
// Os valores devem somar 1.0 dentro de cada dimensão.
func seedStationWithProfile(t *testing.T, pool *pgxpool.Pool, name string, pmm float64,
	maleP, femaleP, abP, cP, deP, r18P, r25P, r50P float64) uuid.UUID {
	t.Helper()
	id := uuid.New()
	meta := fmt.Sprintf(`{
	  "audience_profile": {
	    "gender":      {"male": %f, "female": %f},
	    "socialClass": {"classeAB": %f, "classeC": %f, "classeDE": %f},
	    "ageRanges":   {"range18to24": %f, "range25to49": %f, "range50plus": %f}
	  }
	}`, maleP, femaleP, abP, cP, deP, r18P, r25P, r50P)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO stations(id, name, band, stream_url, pmm, meta)
		VALUES ($1, $2, 'FM', 'http://test/'||$1, $3, $4::jsonb)
	`, id, name, pmm, meta)
	if err != nil {
		t.Fatalf("seed station: %v", err)
	}
	return id
}
```

- [ ] **Step 2: Verificar que os testes falham**

```bash
cd workers && TEST_DATABASE_URL=$DATABASE_URL go test ./internal/catalog/ -run TestInsights_AggregateCore -v
```

Esperado: FAIL com "undefined: aggregateCore".

- [ ] **Step 3: Implementar `aggregateCore`**

Adicione ao `insights.go`:

```go
// coreAggregates é o resultado interno usado pelo Compute().
type coreAggregates struct {
	Impactos         int64
	VeiculacoesTotal int64
	StationsCount    int
	StationsWithPMM  int
	Gender           GenderK
	Class            ClassPyramidData
	Ages             AgeRangesData
	Breakdown        VeiculacoesBreakdownData
}

func (r *Insights) aggregateCore(ctx context.Context, p InsightsParams) (*coreAggregates, error) {
	// Categoria 'orphan' agrupa com extras no breakdown. 'in_slot' / 'out_slot' /
	// 'out_date' são as três categorias da pipeline (ver distribution-rules.md).
	row := r.pool.QueryRow(ctx, `
		WITH filt AS (
		    SELECT d.id, d.station_id, d.category
		    FROM detections d
		    WHERE d.campaign_id = ANY($1::uuid[])
		      AND d.detected_at::date BETWEEN $2 AND $3
		      AND ($4::uuid[] = '{}' OR d.station_id = ANY($4::uuid[]))
		),
		per_station AS (
		    SELECT f.station_id,
		           COUNT(*) AS det_count,
		           SUM(CASE WHEN f.category='in_slot'  THEN 1 ELSE 0 END) AS in_slot_n,
		           SUM(CASE WHEN f.category='out_slot' THEN 1 ELSE 0 END) AS out_slot_n,
		           SUM(CASE WHEN f.category='out_date' THEN 1 ELSE 0 END) AS out_date_n,
		           SUM(CASE WHEN f.category='orphan'   THEN 1 ELSE 0 END) AS orphan_n
		    FROM filt f
		    GROUP BY f.station_id
		),
		joined AS (
		    SELECT ps.*,
		           s.pmm,
		           (s.meta->'audience_profile'->'gender'      ->>'male')::float    AS male_p,
		           (s.meta->'audience_profile'->'gender'      ->>'female')::float  AS female_p,
		           (s.meta->'audience_profile'->'socialClass' ->>'classeAB')::float AS ab_p,
		           (s.meta->'audience_profile'->'socialClass' ->>'classeC')::float  AS c_p,
		           (s.meta->'audience_profile'->'socialClass' ->>'classeDE')::float AS de_p,
		           (s.meta->'audience_profile'->'ageRanges'   ->>'range18to24')::float AS r18_p,
		           (s.meta->'audience_profile'->'ageRanges'   ->>'range25to49')::float AS r25_p,
		           (s.meta->'audience_profile'->'ageRanges'   ->>'range50plus')::float AS r50_p
		    FROM per_station ps
		    JOIN stations s ON s.id = ps.station_id
		)
		SELECT
		    COALESCE(SUM(det_count), 0)::bigint AS veic_total,
		    COUNT(*)                            AS stations_count,
		    COUNT(*) FILTER (WHERE pmm IS NOT NULL) AS stations_with_pmm,
		    -- impactos: só estações com PMM somam
		    COALESCE(SUM(det_count * pmm) FILTER (WHERE pmm IS NOT NULL), 0)::bigint AS impactos,
		    -- gender: precisa PMM E perfil
		    COALESCE(SUM(det_count * pmm * male_p)   FILTER (WHERE pmm IS NOT NULL AND male_p IS NOT NULL),  0)::bigint AS gender_m,
		    COALESCE(SUM(det_count * pmm * female_p) FILTER (WHERE pmm IS NOT NULL AND female_p IS NOT NULL), 0)::bigint AS gender_f,
		    COALESCE(SUM(det_count * pmm * ab_p)   FILTER (WHERE pmm IS NOT NULL AND ab_p IS NOT NULL),  0)::bigint AS cls_ab,
		    COALESCE(SUM(det_count * pmm * c_p)    FILTER (WHERE pmm IS NOT NULL AND c_p IS NOT NULL),   0)::bigint AS cls_c,
		    COALESCE(SUM(det_count * pmm * de_p)   FILTER (WHERE pmm IS NOT NULL AND de_p IS NOT NULL),  0)::bigint AS cls_de,
		    COALESCE(SUM(det_count * pmm * r18_p)  FILTER (WHERE pmm IS NOT NULL AND r18_p IS NOT NULL), 0)::bigint AS age_18,
		    COALESCE(SUM(det_count * pmm * r25_p)  FILTER (WHERE pmm IS NOT NULL AND r25_p IS NOT NULL), 0)::bigint AS age_25,
		    COALESCE(SUM(det_count * pmm * r50_p)  FILTER (WHERE pmm IS NOT NULL AND r50_p IS NOT NULL), 0)::bigint AS age_50,
		    COALESCE(SUM(in_slot_n),  0)::bigint AS sum_in,
		    COALESCE(SUM(out_slot_n), 0)::bigint AS sum_out,
		    COALESCE(SUM(out_date_n), 0)::bigint AS sum_outdate,
		    COALESCE(SUM(orphan_n),   0)::bigint AS sum_orphan
		FROM joined
	`, p.CampaignIDs, p.From, p.To, p.StationIDs)

	out := &coreAggregates{}
	if err := row.Scan(
		&out.VeiculacoesTotal, &out.StationsCount, &out.StationsWithPMM,
		&out.Impactos,
		&out.Gender.M, &out.Gender.F,
		&out.Class.AB, &out.Class.C, &out.Class.DE,
		&out.Ages.R18_24, &out.Ages.R25_49, &out.Ages.R50Plus,
		&out.Breakdown.InSlot, &out.Breakdown.OutSlot, &out.Breakdown.OutDate, &out.Breakdown.ExtrasOrphan,
	); err != nil {
		return nil, err
	}
	return out, nil
}
```

Importante: a query usa `$4::uuid[] = '{}'` para distinguir "sem filtro de estação" de "filtro vazio" — quando `p.StationIDs == nil`, pgx envia `{}` (array vazio).

- [ ] **Step 4: Rodar testes — devem passar**

```bash
cd workers && TEST_DATABASE_URL=$DATABASE_URL go test ./internal/catalog/ -run TestInsights_AggregateCore -v
```

Se falhar com "type mismatch" no scan de `FILTER (WHERE ...)`, troque `COALESCE(..., 0)` por `COALESCE(..., 0::bigint)` ou faça o cast no nível externo.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/insights.go workers/internal/catalog/insights_test.go
git commit -m "feat(insights): aggregate impactos, demographics and category breakdown"
```

---

### Task 5: Backend — Investimento (contratado + executado) e bonificação

Calcula investido contratado / executado e bonificação. Lê `campaigns_pricing` (mode + valor) e cruza com counts do `aggregateCore`.

**Files:**
- Modify: `workers/internal/catalog/insights.go`
- Modify: `workers/internal/catalog/insights_test.go`

- [ ] **Step 1: Confirmar schema de `campaigns_pricing`**

```bash
cd workers && psql $DATABASE_URL -c "\\d campaigns_pricing"
```

Esperado: tabela com colunas `campaign_id, mode (varchar), consolidated_value (numeric), price_per_insertion (numeric)`. Se nomes forem diferentes, ajustar SQL abaixo.

- [ ] **Step 2: Escrever teste**

```go
func TestInsights_AggregateInvestment_PerInsertion(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := NewInsights(pool)
	client := seedClient(t, pool, "X")
	camp := seedCampaign(t, pool, client, "2026-06-01", "2026-06-30")
	seedPricing(t, pool, camp, "per_insertion", 0, 50.0) // R$ 50 por inserção
	st := seedStationWithProfile(t, pool, "X", 1000, 0.5,0.5, 0.3,0.4,0.3, 0.3,0.4,0.3)

	// 4 in_slot + 2 out_slot + 3 orphan
	for i := 0; i < 4; i++ { seedDetection(t, pool, camp, st, "in_slot", "2026-06-10") }
	for i := 0; i < 2; i++ { seedDetection(t, pool, camp, st, "out_slot", "2026-06-11") }
	for i := 0; i < 3; i++ { seedDetection(t, pool, camp, st, "orphan", "2026-06-12") }

	// Programado total na campanha = 10 (seed via distribution_rules)
	seedDistributionRule(t, pool, camp, st, "2026-06-01", "2026-06-30", 0b1111111, "00:00", "23:59", 1)

	from, _ := time.Parse("2006-01-02", "2026-06-01")
	to, _ := time.Parse("2006-01-02", "2026-06-30")
	inv, bon, err := repo.aggregateInvestment(context.Background(), InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp}, From: from, To: to,
	})
	if err != nil { t.Fatalf("%v", err) }

	// Contratado per_insertion = price × programado_no_overlap
	// programado = 1 play/dia × 30 dias = 30 → 30 × R$50 = R$1500
	if inv.Contratado != 1500.0 {
		t.Fatalf("contratado = %v, want 1500", inv.Contratado)
	}
	// Executado per_insertion = price × (in_slot + out_slot) = 6 × 50 = 300
	if inv.Executado != 300.0 {
		t.Fatalf("executado = %v, want 300", inv.Executado)
	}
	// Bonificação = orphan count × price = 3 × 50 = 150
	if bon.Valor != 150.0 {
		t.Fatalf("bonificacao valor = %v, want 150", bon.Valor)
	}
	if bon.Count != 3 {
		t.Fatalf("bonificacao count = %d, want 3", bon.Count)
	}
}
```

`seedPricing` e `seedDistributionRule`: copiar dos helpers de testes existentes (procurar em `campaigns_pricing_test.go` e `distribution_rules_test.go`). Se inexistentes, inline:

```go
func seedPricing(t *testing.T, pool *pgxpool.Pool, campaign uuid.UUID, mode string, consolidated, perIns float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO campaigns_pricing(campaign_id, mode, consolidated_value, price_per_insertion)
		VALUES ($1, $2, NULLIF($3, 0), NULLIF($4, 0))
	`, campaign, mode, consolidated, perIns)
	if err != nil { t.Fatalf("seed pricing: %v", err) }
}
```

- [ ] **Step 3: Rodar e ver falhar**

```bash
cd workers && go test ./internal/catalog/ -run TestInsights_AggregateInvestment -v
```

Esperado: FAIL — função não existe.

- [ ] **Step 4: Implementar `aggregateInvestment`**

```go
func (r *Insights) aggregateInvestment(ctx context.Context, p InsightsParams) (InvestidoK, BonificacaoK, error) {
	// Estratégia: para cada campanha, calculamos contratado/executado/bonificação
	// usando o modo de pricing e o overlap entre filtro e janela da campanha.
	// Programado no overlap = sum(plays_per_day × days_matching_weekday_in_overlap)
	// das distribution_rules da campanha. Executado = count(detections in_slot+out_slot).
	//
	// Para consolidated: contratado = consolidated_value × (days_overlap / days_total_camp).
	rows, err := r.pool.Query(ctx, `
		WITH camp AS (
		    SELECT c.id, c.start_date, c.end_date,
		           cp.mode, cp.consolidated_value, cp.price_per_insertion
		    FROM campaigns c
		    LEFT JOIN campaigns_pricing cp ON cp.campaign_id = c.id
		    WHERE c.id = ANY($1::uuid[])
		),
		overlap AS (
		    SELECT c.id,
		           GREATEST(c.start_date, $2::date) AS o_start,
		           LEAST(c.end_date,   $3::date) AS o_end,
		           (c.end_date - c.start_date + 1) AS camp_days,
		           c.mode, c.consolidated_value, c.price_per_insertion
		    FROM camp c
		),
		programmed AS (
		    -- Soma de plays por dia válido no overlap × weekday_mask × stations filter
		    SELECT dr.campaign_id,
		           COALESCE(SUM(dr.plays_per_day *
		                    (SELECT COUNT(*) FROM generate_series(
		                        GREATEST(dr.start_date, ov.o_start),
		                        LEAST(dr.end_date, ov.o_end),
		                        interval '1 day'
		                    ) g
		                    WHERE (dr.weekday_mask >> EXTRACT(DOW FROM g)::int) & 1 = 1
		                      AND ($4::uuid[] = '{}' OR dr.station_ids && $4::uuid[])
		                    )
		           ), 0)::bigint AS planned_count
		    FROM distribution_rules dr
		    JOIN overlap ov ON ov.id = dr.campaign_id
		    GROUP BY dr.campaign_id
		),
		executed AS (
		    SELECT d.campaign_id,
		           COUNT(*) FILTER (WHERE d.category IN ('in_slot','out_slot'))::bigint AS exec_count,
		           COUNT(*) FILTER (WHERE d.category = 'orphan')::bigint AS orphan_count
		    FROM detections d
		    WHERE d.campaign_id = ANY($1::uuid[])
		      AND d.detected_at::date BETWEEN $2 AND $3
		      AND ($4::uuid[] = '{}' OR d.station_id = ANY($4::uuid[]))
		    GROUP BY d.campaign_id
		)
		SELECT ov.id,
		       ov.mode,
		       COALESCE(ov.consolidated_value, 0),
		       COALESCE(ov.price_per_insertion, 0),
		       COALESCE(pr.planned_count, 0),
		       COALESCE(ex.exec_count, 0),
		       COALESCE(ex.orphan_count, 0),
		       (ov.o_end - ov.o_start + 1) AS days_overlap,
		       ov.camp_days
		FROM overlap ov
		LEFT JOIN programmed pr ON pr.campaign_id = ov.id
		LEFT JOIN executed ex   ON ex.campaign_id = ov.id
	`, p.CampaignIDs, p.From, p.To, p.StationIDs)
	if err != nil {
		return InvestidoK{}, BonificacaoK{}, err
	}
	defer rows.Close()

	var inv InvestidoK
	var bon BonificacaoK
	for rows.Next() {
		var (
			id          uuid.UUID
			mode        sql.NullString
			consVal     float64
			priceIns    float64
			planned     int64
			execCount   int64
			orphanCount int64
			daysOverlap int
			campDays    int
		)
		if err := rows.Scan(&id, &mode, &consVal, &priceIns, &planned, &execCount, &orphanCount, &daysOverlap, &campDays); err != nil {
			return InvestidoK{}, BonificacaoK{}, err
		}

		switch mode.String {
		case "per_insertion":
			inv.Contratado += priceIns * float64(planned)
			inv.Executado  += priceIns * float64(execCount)
			bon.Valor      += priceIns * float64(orphanCount)
		case "consolidated":
			if campDays > 0 {
				frac := float64(daysOverlap) / float64(campDays)
				inv.Contratado += consVal * frac
			}
			if planned > 0 {
				inv.Executado += consVal * (float64(execCount) / float64(planned))
			}
			if planned > 0 {
				unit := consVal / float64(planned) // bonificação avalia por inserção avg
				bon.Valor += unit * float64(orphanCount)
			}
		}
		bon.Count += orphanCount
	}
	return inv, bon, rows.Err()
}
```

Adicione `"database/sql"` ao import.

- [ ] **Step 5: Rodar testes — devem passar**

```bash
cd workers && go test ./internal/catalog/ -run TestInsights_AggregateInvestment -v
```

- [ ] **Step 6: Commit**

```bash
git add workers/internal/catalog/insights.go workers/internal/catalog/insights_test.go
git commit -m "feat(insights): compute investido (contratado/executado) and bonificação"
```

---

### Task 6: Backend — Buckets diário/mensal

Gera a série temporal do gráfico 4 com 6 colunas por bucket: programado, in_slot, out_slot, out_date, deficit, extras. Granularidade decidida automaticamente.

**Files:**
- Modify: `workers/internal/catalog/insights.go`
- Modify: `workers/internal/catalog/insights_test.go`

- [ ] **Step 1: Escrever teste**

```go
func TestInsights_AggregateBuckets_DailyGranularity(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := NewInsights(pool)
	client := seedClient(t, pool, "X")
	camp := seedCampaign(t, pool, client, "2026-06-01", "2026-06-30")
	st := seedStationWithProfile(t, pool, "X", 1000, 0.5,0.5, 0.3,0.4,0.3, 0.3,0.4,0.3)

	// programado = 1 play/dia
	seedDistributionRule(t, pool, camp, st, "2026-06-01", "2026-06-30", 0b1111111, "00:00", "23:59", 1)
	// no dia 06-10: 2 in_slot (= 1 acima do programado, mas conta como in_slot)
	seedDetection(t, pool, camp, st, "in_slot", "2026-06-10")
	seedDetection(t, pool, camp, st, "in_slot", "2026-06-10")
	// no dia 06-11: 1 out_slot
	seedDetection(t, pool, camp, st, "out_slot", "2026-06-11")
	// no dia 06-12: 1 orphan
	seedDetection(t, pool, camp, st, "orphan", "2026-06-12")

	from, _ := time.Parse("2006-01-02", "2026-06-10")
	to, _ := time.Parse("2006-01-02", "2026-06-12")
	buckets, gran, err := repo.aggregateBuckets(context.Background(), InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp}, From: from, To: to,
	})
	if err != nil { t.Fatalf("%v", err) }
	if gran != "day" { t.Fatalf("gran = %q, want day", gran) }
	if len(buckets) != 3 { t.Fatalf("buckets = %d, want 3", len(buckets)) }

	// dia 10: programado=1, in_slot=2, deficit=max(0, 1-2-0)=0, extras=0
	if buckets[0].Bucket != "2026-06-10" || buckets[0].InSlot != 2 || buckets[0].Programado != 1 || buckets[0].Deficit != 0 {
		t.Fatalf("day 10: %+v", buckets[0])
	}
	// dia 11: programado=1, out_slot=1, deficit=max(0, 1-0-1)=0
	if buckets[1].Bucket != "2026-06-11" || buckets[1].OutSlot != 1 {
		t.Fatalf("day 11: %+v", buckets[1])
	}
	// dia 12: programado=1, extras=1 (orphan), deficit=1 (nada in/out)
	if buckets[2].Extras != 1 || buckets[2].Deficit != 1 {
		t.Fatalf("day 12: %+v", buckets[2])
	}
}

func TestInsights_AggregateBuckets_MonthlyGranularity(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := NewInsights(pool)
	client := seedClient(t, pool, "X")
	camp := seedCampaign(t, pool, client, "2026-01-01", "2026-12-31")

	from, _ := time.Parse("2006-01-02", "2026-01-01")
	to, _ := time.Parse("2006-01-02", "2026-04-30")
	_, gran, err := repo.aggregateBuckets(context.Background(), InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp}, From: from, To: to,
	})
	if err != nil { t.Fatalf("%v", err) }
	if gran != "month" { t.Fatalf("gran = %q, want month (period > 31 days)", gran) }
}
```

- [ ] **Step 2: Rodar e ver falhar**

```bash
cd workers && go test ./internal/catalog/ -run TestInsights_AggregateBuckets -v
```

- [ ] **Step 3: Implementar `aggregateBuckets`**

```go
func (r *Insights) aggregateBuckets(ctx context.Context, p InsightsParams) ([]BucketRow, string, error) {
	days := int(p.To.Sub(p.From).Hours()/24) + 1
	gran := "day"
	if days > 31 {
		gran = "month"
	}

	// Programado por bucket via expansão de distribution_rules.
	// Executado por bucket via grouping de detections.
	// Une por LEFT JOIN.
	bucketExpr := "g::date::text" // diário: YYYY-MM-DD
	if gran == "month" {
		bucketExpr = "to_char(g, 'YYYY-MM')"
	}

	query := fmt.Sprintf(`
		WITH days AS (
		    SELECT generate_series($2::date, $3::date, interval '1 day') AS g
		),
		planned AS (
		    SELECT %s AS bucket, COUNT(*)::int AS programado
		    FROM days
		    JOIN distribution_rules dr ON dr.campaign_id = ANY($1::uuid[])
		      AND days.g::date BETWEEN dr.start_date AND dr.end_date
		      AND ((dr.weekday_mask >> EXTRACT(DOW FROM days.g)::int) & 1) = 1
		      AND ($4::uuid[] = '{}' OR dr.station_ids && $4::uuid[])
		    GROUP BY 1
		),
		actual AS (
		    SELECT %s AS bucket,
		           COUNT(*) FILTER (WHERE d.category='in_slot')::int  AS in_slot,
		           COUNT(*) FILTER (WHERE d.category='out_slot')::int AS out_slot,
		           COUNT(*) FILTER (WHERE d.category='out_date')::int AS out_date,
		           COUNT(*) FILTER (WHERE d.category='orphan')::int   AS extras
		    FROM detections d
		    JOIN days ON days.g::date = d.detected_at::date
		    WHERE d.campaign_id = ANY($1::uuid[])
		      AND ($4::uuid[] = '{}' OR d.station_id = ANY($4::uuid[]))
		    GROUP BY 1
		),
		buckets AS (
		    SELECT %s AS bucket FROM days GROUP BY 1
		)
		SELECT b.bucket,
		       COALESCE(p.programado, 0),
		       COALESCE(a.in_slot, 0),
		       COALESCE(a.out_slot, 0),
		       COALESCE(a.out_date, 0),
		       GREATEST(0, COALESCE(p.programado,0) - COALESCE(a.in_slot,0) - COALESCE(a.out_slot,0)),
		       COALESCE(a.extras, 0)
		FROM buckets b
		LEFT JOIN planned p ON p.bucket = b.bucket
		LEFT JOIN actual  a ON a.bucket = b.bucket
		ORDER BY b.bucket
	`, bucketExpr, bucketExpr, bucketExpr)

	rows, err := r.pool.Query(ctx, query, p.CampaignIDs, p.From, p.To, p.StationIDs)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var out []BucketRow
	for rows.Next() {
		var b BucketRow
		if err := rows.Scan(&b.Bucket, &b.Programado, &b.InSlot, &b.OutSlot, &b.OutDate, &b.Deficit, &b.Extras); err != nil {
			return nil, "", err
		}
		out = append(out, b)
	}
	return out, gran, rows.Err()
}
```

**Atenção SQL:** o CTE `planned` no código acima usa `COUNT(*)::int`, mas a tabela `distribution_rules` tem `plays_per_day` que pode ser >1. **Troque** para `COALESCE(SUM(dr.plays_per_day), 0)::int` no CTE `planned` antes de rodar os testes:

```sql
planned AS (
    SELECT %s AS bucket, COALESCE(SUM(dr.plays_per_day), 0)::int AS programado
    FROM days
    JOIN distribution_rules dr ON dr.campaign_id = ANY($1::uuid[])
      AND days.g::date BETWEEN dr.start_date AND dr.end_date
      AND ((dr.weekday_mask >> EXTRACT(DOW FROM days.g)::int) & 1) = 1
      AND ($4::uuid[] = '{}' OR dr.station_ids && $4::uuid[])
    GROUP BY 1
),
```

- [ ] **Step 4: Rodar testes — devem passar**

```bash
cd workers && go test ./internal/catalog/ -run TestInsights_AggregateBuckets -v
```

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/insights.go workers/internal/catalog/insights_test.go
git commit -m "feat(insights): aggregate daily/monthly buckets with planned vs executed"
```

---

### Task 7: Backend — Compose final no `Compute()`

Junta `fetchCampaigns + aggregateCore + aggregateInvestment + aggregateBuckets` no entry-point. Calcula CPM.

**Files:**
- Modify: `workers/internal/catalog/insights.go`
- Modify: `workers/internal/catalog/insights_test.go`

- [ ] **Step 1: Teste end-to-end**

```go
func TestInsights_Compute_EndToEnd(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := NewInsights(pool)
	client := seedClient(t, pool, "X")
	camp := seedCampaign(t, pool, client, "2026-06-01", "2026-06-30")
	seedPricing(t, pool, camp, "per_insertion", 0, 100.0)
	st := seedStationWithProfile(t, pool, "X", 2000, 0.5,0.5, 0.3,0.4,0.3, 0.3,0.4,0.3)
	seedDistributionRule(t, pool, camp, st, "2026-06-01", "2026-06-30", 0b1111111, "00:00", "23:59", 1)
	for i := 0; i < 10; i++ { seedDetection(t, pool, camp, st, "in_slot", "2026-06-15") }

	from, _ := time.Parse("2006-01-02", "2026-06-01")
	to, _ := time.Parse("2006-01-02", "2026-06-30")
	out, err := repo.Compute(context.Background(), InsightsParams{
		ClientID: client, CampaignIDs: []uuid.UUID{camp}, From: from, To: to,
	})
	if err != nil { t.Fatalf("%v", err) }

	if out.KPIs.Impactos != 20000 { t.Fatalf("impactos=%d", out.KPIs.Impactos) }
	if out.KPIs.VeiculacoesTotal != 10 { t.Fatalf("veic=%d", out.KPIs.VeiculacoesTotal) }
	if out.KPIs.Investido.Executado != 1000.0 { t.Fatalf("exec=%v", out.KPIs.Investido.Executado) }
	// CPM = (1000 / 20000) × 1000 = 50.0
	if out.KPIs.CPM < 49.9 || out.KPIs.CPM > 50.1 { t.Fatalf("cpm=%v want 50", out.KPIs.CPM) }
	if out.Period.Granularity != "day" { t.Fatalf("gran=%q", out.Period.Granularity) }
	if len(out.Buckets) != 30 { t.Fatalf("buckets=%d want 30", len(out.Buckets)) }
	if len(out.Campaigns) != 1 { t.Fatalf("camps=%d", len(out.Campaigns)) }
}
```

- [ ] **Step 2: Implementar `Compute`**

Substituir o stub:

```go
func (r *Insights) Compute(ctx context.Context, p InsightsParams) (*InsightsPayload, error) {
	briefs, err := r.fetchCampaigns(ctx, p.ClientID, p.CampaignIDs)
	if err != nil {
		return nil, err
	}
	core, err := r.aggregateCore(ctx, p)
	if err != nil {
		return nil, err
	}
	inv, bon, err := r.aggregateInvestment(ctx, p)
	if err != nil {
		return nil, err
	}
	buckets, gran, err := r.aggregateBuckets(ctx, p)
	if err != nil {
		return nil, err
	}

	cpm := 0.0
	if core.Impactos > 0 {
		cpm = (inv.Executado / float64(core.Impactos)) * 1000.0
	}

	return &InsightsPayload{
		Period: PeriodSpec{
			From:        p.From.Format("2006-01-02"),
			To:          p.To.Format("2006-01-02"),
			Granularity: gran,
		},
		Campaigns: briefs,
		KPIs: InsightsKPIs{
			Impactos:         core.Impactos,
			VeiculacoesTotal: core.VeiculacoesTotal,
			StationsCount:    core.StationsCount,
			StationsWithPMM:  core.StationsWithPMM,
			CPM:              cpm,
			Bonificacao:      bon,
			Investido:        inv,
			Gender:           core.Gender,
		},
		ClassPyramid:         core.Class,
		AgeRanges:            core.Ages,
		VeiculacoesBreakdown: core.Breakdown,
		Buckets:              buckets,
	}, nil
}
```

- [ ] **Step 3: Rodar todos os testes do pacote**

```bash
cd workers && go test ./internal/catalog/ -run TestInsights -v
```

Esperado: todos PASS.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/catalog/insights.go workers/internal/catalog/insights_test.go
git commit -m "feat(insights): compose Compute() entry point with CPM calculation"
```

---

### Task 8: Backend — Handler HTTP

**Files:**
- Create: `workers/internal/api/handlers/insights.go`
- Create: `workers/internal/api/handlers/insights_test.go`
- Modify: `workers/internal/api/router.go`
- Modify: `workers/cmd/api/main.go`

- [ ] **Step 1: Criar handler**

```go
package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

type InsightsRepo interface {
	Compute(ctx context.Context, p catalog.InsightsParams) (*catalog.InsightsPayload, error)
}

type InsightsHandler struct {
	Repo InsightsRepo
}

func NewInsightsHandler(repo InsightsRepo) *InsightsHandler {
	return &InsightsHandler{Repo: repo}
}

// Get GET /insights
//
// Query params:
//   - client_id: uuid (admin obrigatório; cliente é forçado pelo JWT scope)
//   - campaigns: csv de uuids (obrigatório, min 1, max 50)
//   - from, to:  YYYY-MM-DD (opcional, default = mês corrente)
//   - stations:  csv de uuids (opcional)
func (h *InsightsHandler) Get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scope := auth.ClientScopeFromContext(r.Context())

	// client_id
	var clientID uuid.UUID
	if scope != nil {
		clientID = *scope
	} else {
		cid := q.Get("client_id")
		if cid == "" {
			http.Error(w, "client_id required", http.StatusBadRequest)
			return
		}
		parsed, err := uuid.Parse(cid)
		if err != nil {
			http.Error(w, "invalid client_id", http.StatusBadRequest)
			return
		}
		clientID = parsed
	}

	// campaigns
	camps, err := parseUUIDList(q.Get("campaigns"))
	if err != nil || len(camps) == 0 {
		http.Error(w, "campaigns required (csv of uuids, min 1)", http.StatusBadRequest)
		return
	}
	if len(camps) > 50 {
		http.Error(w, "campaigns max=50", http.StatusBadRequest)
		return
	}

	// stations (opcional)
	stations, _ := parseUUIDList(q.Get("stations"))
	if stations == nil {
		stations = []uuid.UUID{} // pgx envia como '{}'
	}

	// from / to (default = mês corrente)
	now := time.Now().UTC()
	defaultFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	defaultTo := defaultFrom.AddDate(0, 1, 0).Add(-time.Second)

	from := parseDateOr(q.Get("from"), defaultFrom)
	to := parseDateOr(q.Get("to"), defaultTo)
	if !to.After(from) {
		http.Error(w, "to must be after from", http.StatusBadRequest)
		return
	}

	out, err := h.Repo.Compute(r.Context(), catalog.InsightsParams{
		ClientID:    clientID,
		CampaignIDs: camps,
		From:        from,
		To:          to,
		StationIDs:  stations,
	})
	if err != nil {
		// Anti-oracle: erro "cross-client" vira 403 pra não vazar.
		if strings.Contains(err.Error(), "cross-client") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func parseUUIDList(s string) ([]uuid.UUID, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]uuid.UUID, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := uuid.Parse(p)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func parseDateOr(s string, def time.Time) time.Time {
	if s == "" {
		return def
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return def
	}
	return t
}
```

- [ ] **Step 2: Teste do handler com fake repo**

```go
package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"radiocheck/internal/catalog"
)

type fakeInsightsRepo struct {
	out  *catalog.InsightsPayload
	err  error
	got  catalog.InsightsParams
	hits int
}

func (f *fakeInsightsRepo) Compute(_ context.Context, p catalog.InsightsParams) (*catalog.InsightsPayload, error) {
	f.got = p
	f.hits++
	return f.out, f.err
}

func TestInsights_Get_RequiresClient(t *testing.T) {
	h := &InsightsHandler{Repo: &fakeInsightsRepo{}}
	req := httptest.NewRequest("GET", "/insights?campaigns="+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rec.Code)
	}
}

func TestInsights_Get_RequiresCampaigns(t *testing.T) {
	h := &InsightsHandler{Repo: &fakeInsightsRepo{}}
	req := httptest.NewRequest("GET", "/insights?client_id="+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rec.Code)
	}
}

func TestInsights_Get_HappyPath(t *testing.T) {
	want := &catalog.InsightsPayload{
		Period: catalog.PeriodSpec{From: "2026-06-01", To: "2026-06-30", Granularity: "day"},
		KPIs:   catalog.InsightsKPIs{Impactos: 12345},
	}
	repo := &fakeInsightsRepo{out: want}
	h := &InsightsHandler{Repo: repo}
	url := "/insights?client_id=" + uuid.NewString() + "&campaigns=" + uuid.NewString()
	req := httptest.NewRequest("GET", url, nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var got catalog.InsightsPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v", err)
	}
	if got.KPIs.Impactos != 12345 {
		t.Fatalf("impactos=%d", got.KPIs.Impactos)
	}
	if repo.hits != 1 {
		t.Fatalf("hits=%d", repo.hits)
	}
}

func TestInsights_Get_DefaultPeriodIsCurrentMonth(t *testing.T) {
	repo := &fakeInsightsRepo{out: &catalog.InsightsPayload{}}
	h := &InsightsHandler{Repo: repo}
	url := "/insights?client_id=" + uuid.NewString() + "&campaigns=" + uuid.NewString()
	req := httptest.NewRequest("GET", url, nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	now := time.Now().UTC()
	wantFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if !repo.got.From.Equal(wantFrom) {
		t.Fatalf("from=%v want %v", repo.got.From, wantFrom)
	}
}
```

- [ ] **Step 3: Rodar testes do handler**

```bash
cd workers && go test ./internal/api/handlers/ -run TestInsights -v
```

Esperado: 4 PASS.

- [ ] **Step 4: Wire no router**

Em [router.go:140-145](workers/internal/api/router.go) — adicionar a rota após `/detections/{id}/evidence/url` (no Subgrupo A, antes do bloco de Reports):

```go
// Insights dashboard — admin vê tudo, cliente vê só o próprio (via JWT scope).
// Espelha o anti-oracle do /detections.
if d.Insights != nil {
    r.Get("/insights", d.Insights.Get)
}
```

E adicionar no struct `Deps` (linha ~54, após `Notifications`):

```go
Notifications  *handlers.NotificationsHandler
Insights       *handlers.InsightsHandler
```

- [ ] **Step 5: Wire no main.go**

Em [workers/cmd/api/main.go:339](workers/cmd/api/main.go) — após o `Reports:` adicionar:

```go
Reports:       &handlers.ReportsHandler{Detections: detections, CampaignRepo: campaigns, Pool: pool},
Notifications: notificationsHandler,
Insights:      handlers.NewInsightsHandler(catalog.NewInsights(pool)),
```

Se a linha de `Notifications` já existir em outra posição, adicione `Insights` em sequência respeitando a ordem do struct. Imports do `catalog` provavelmente já existem.

- [ ] **Step 6: Build + smoke test**

```bash
cd workers && go build ./...
cd workers && go test ./internal/api/handlers/ -run TestInsights -v
```

Esperado: build limpo + testes PASS.

- [ ] **Step 7: Commit**

```bash
git add workers/internal/api/handlers/insights.go workers/internal/api/handlers/insights_test.go workers/internal/api/router.go workers/cmd/api/main.go
git commit -m "feat(insights): HTTP handler + router wiring at GET /v1/internal/insights"
```

---

### Task 9: Frontend — Rota, sidebar, página esqueleto

**Files:**
- Create: `frontend/src/pages/InsightsPage.jsx`
- Create: `frontend/src/pages/InsightsPage.css`
- Modify: `frontend/src/App.jsx`
- Modify: `frontend/src/components/Sidebar.jsx`

- [ ] **Step 1: Página esqueleto**

```jsx
// frontend/src/pages/InsightsPage.jsx
import './InsightsPage.css'
import Footer from '../components/Footer'

export default function InsightsPage() {
  return (
    <div className="in-page">
      <header className="in-header">
        <h1 className="in-title">Dashboard de Veiculação</h1>
      </header>
      <div className="in-body">
        <p>Em construção…</p>
      </div>
      <Footer />
    </div>
  )
}
```

- [ ] **Step 2: CSS esqueleto**

```css
/* frontend/src/pages/InsightsPage.css */
.in-page {
  display: flex;
  flex-direction: column;
  gap: 18px;
  font-family: var(--font-body);
  color: var(--c-text);
  padding: 18px;
}

.in-header {
  display: flex;
  align-items: center;
  gap: 14px;
}

.in-title {
  margin: 0;
  font-family: var(--font-heading);
  font-size: 26px;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--c-text);
}

.in-body { display: flex; flex-direction: column; gap: 24px; }
```

- [ ] **Step 3: Wire rota em App.jsx**

Em [frontend/src/App.jsx:84-120](frontend/src/App.jsx) — adicionar import no topo:

```jsx
import InsightsPage from './pages/InsightsPage'
```

E inserir a rota após `/detections`:

```jsx
<Route path="/detections"  element={<DetectionsPage />} />
<Route path="/insights"    element={<InsightsPage />} />
<Route path="/reports/airtime" element={<AirtimeReportPage />} />
```

> **NOTA IMPORTANTE:** NÃO envolva com `<RequireRole roles={['admin']}>`. A página é acessível a admin E cliente — a proteção é feita no backend via `auth.ClientScopeFromContext`.

- [ ] **Step 4: Sidebar — Admin nav**

Em [frontend/src/components/Sidebar.jsx](frontend/src/components/Sidebar.jsx), na função `AdminNav` (grupo "Veiculação"), adicionar **antes** de `/campaigns`:

```jsx
<span className="sidebar-section-label">Veiculação</span>
<SidebarLink to="/insights"        icon={<IconDashboard />}      onClose={onClose}>Dashboard</SidebarLink>
<SidebarLink to="/campaigns"       icon={<IconCampaigns />}      onClose={onClose}>Campanhas</SidebarLink>
```

Reusa `<IconDashboard />` (que já é usado em `/dashboard`). Se preferir um ícone diferente, criar `IconInsights` espelhando `IconAdminOverview` (mesma estrutura SVG, ajustar paths).

- [ ] **Step 5: Sidebar — Client nav**

No mesmo arquivo, na função `ClientNav` (também grupo "Veiculação"), mesma inserção:

```jsx
<span className="sidebar-section-label">Veiculação</span>
<SidebarLink to="/insights"        icon={<IconDashboard />}      onClose={onClose}>Dashboard</SidebarLink>
<SidebarLink to="/campaigns"       icon={<IconCampaigns />}      onClose={onClose}>Campanhas</SidebarLink>
```

- [ ] **Step 6: Verificar no browser**

```bash
cd frontend && npm run dev
```

Acessar `http://localhost:5173/insights` logado como admin → vê "Em construção…". Logado como cliente → idem. Sidebar mostra "Dashboard" no grupo Veiculação para ambos.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/pages/InsightsPage.jsx frontend/src/pages/InsightsPage.css frontend/src/App.jsx frontend/src/components/Sidebar.jsx
git commit -m "feat(insights): scaffold /insights route + sidebar link (admin + client)"
```

---

### Task 10: Frontend — Hook `useInsights`

**Files:**
- Modify: `frontend/src/api/hooks.js`

- [ ] **Step 1: Adicionar hook**

No final de [frontend/src/api/hooks.js](frontend/src/api/hooks.js):

```js
// Insights dashboard payload (/insights tela).
// Backend devolve já agregado — não há paginação.
export function useInsights({ clientId, campaignIds, from, to, stationIds } = {}) {
  const ready = Boolean(clientId) && Array.isArray(campaignIds) && campaignIds.length > 0
  return useQuery({
    enabled: ready,
    queryKey: ['insights', clientId, [...(campaignIds || [])].sort().join(','), from, to, [...(stationIds || [])].sort().join(',')],
    queryFn: () => api.get('/insights', {
      params: {
        client_id: clientId,
        campaigns: campaignIds.join(','),
        from: from || undefined,
        to: to || undefined,
        stations: stationIds && stationIds.length ? stationIds.join(',') : undefined,
      },
    }).then(r => r.data),
    placeholderData: (prev) => prev,
  })
}
```

- [ ] **Step 2: Smoke test no browser**

Em `InsightsPage.jsx`, temporariamente:

```jsx
import { useInsights } from '../api/hooks'
// ...
const fake = useInsights({ clientId: 'algum-uuid-real', campaignIds: ['outro-uuid'] })
console.log('insights', fake.data, fake.error)
```

Verificar no console que o request sai. Reverter a mudança após validar.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/hooks.js
git commit -m "feat(insights): add useInsights React Query hook"
```

---

### Task 11: Frontend — FiltersBar

**Files:**
- Create: `frontend/src/components/insights/FiltersBar.jsx`

- [ ] **Step 1: Criar componente**

```jsx
// frontend/src/components/insights/FiltersBar.jsx
import { useEffect, useMemo } from 'react'
import RSelect from '../RSelect'
import { useAuth } from '../../contexts/AuthContext'
import { useClients, useCampaignsPaged, useStations } from '../../api/hooks'

function todayISO() {
  const d = new Date()
  return new Date(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate())).toISOString().slice(0,10)
}
function firstOfMonthISO() {
  const d = new Date()
  return new Date(Date.UTC(d.getFullYear(), d.getMonth(), 1)).toISOString().slice(0,10)
}
function lastOfMonthISO() {
  const d = new Date()
  return new Date(Date.UTC(d.getFullYear(), d.getMonth()+1, 0)).toISOString().slice(0,10)
}

export default function FiltersBar({ value, onChange, onExportImage, onExportPDF }) {
  const { isAdmin, user } = useAuth()

  // Cliente: admin escolhe; cliente fica preso ao próprio
  const clientsQ = useClients({ enabled: isAdmin })
  const clientOpts = useMemo(() =>
    (clientsQ.data || []).map(c => ({ value: c.id, label: c.name })),
    [clientsQ.data])

  // Campanhas: paged sem filtro de cliente (admin); cliente — mesmo endpoint, backend filtra
  const campaignsQ = useCampaignsPaged({ page: 1, pageSize: 200 })
  const campOpts = useMemo(() => {
    const rows = (campaignsQ.data?.data || []).filter(c =>
      !value.clientId || c.client_id === value.clientId
    )
    return rows.map(c => ({
      value: c.id,
      label: c.name,
      raw: c, // start_date, end_date
    }))
  }, [campaignsQ.data, value.clientId])

  // Emissoras: lista filtrada pelas campanhas selecionadas (intersection)
  const stationsQ = useStations()
  const stationOpts = useMemo(() => {
    if (!value.campaignIds?.length) return []
    const targetSets = (campaignsQ.data?.data || [])
      .filter(c => value.campaignIds.includes(c.id))
      .map(c => new Set(c.target_stations || []))
    if (!targetSets.length) return []
    const intersection = (stationsQ.data || []).filter(s =>
      targetSets.every(set => set.has(s.id))
    )
    return intersection.map(s => ({ value: s.id, label: s.name }))
  }, [campaignsQ.data, stationsQ.data, value.campaignIds])

  // Período completo = min(start) → max(end) das campanhas selecionadas
  const fullRange = useMemo(() => {
    const selected = (campaignsQ.data?.data || []).filter(c => value.campaignIds.includes(c.id))
    if (!selected.length) return null
    const starts = selected.map(c => c.start_date).sort()
    const ends   = selected.map(c => c.end_date).sort()
    return { from: starts[0], to: ends[ends.length - 1] }
  }, [campaignsQ.data, value.campaignIds])

  // Lock no clientId pro role=cliente
  useEffect(() => {
    if (!isAdmin && user?.client_id && value.clientId !== user.client_id) {
      onChange({ ...value, clientId: user.client_id })
    }
  }, [isAdmin, user, value.clientId, onChange, value])

  return (
    <div className="in-filters">
      <div className="in-filters-row">
        {isAdmin ? (
          <div className="in-filter">
            <label className="in-filter-label">Cliente</label>
            <RSelect
              options={clientOpts}
              value={clientOpts.find(o => o.value === value.clientId) || null}
              onChange={opt => onChange({ ...value, clientId: opt?.value || null, campaignIds: [], stationIds: [] })}
              placeholder="Selecione…"
              isLoading={clientsQ.isPending}
              isClearable
            />
          </div>
        ) : (
          <div className="in-filter in-filter--locked">
            <label className="in-filter-label">Cliente</label>
            <div className="in-locked-chip">{user?.client_name || 'Sua conta'}</div>
          </div>
        )}

        <div className="in-filter in-filter--wide">
          <label className="in-filter-label">Campanhas</label>
          <RSelect
            isMulti
            options={campOpts}
            value={campOpts.filter(o => value.campaignIds.includes(o.value))}
            onChange={opts => onChange({ ...value, campaignIds: opts.map(o => o.value) })}
            placeholder="Selecione 1 ou mais…"
            isDisabled={!value.clientId}
            isLoading={campaignsQ.isPending}
          />
        </div>

        <div className="in-filter">
          <label className="in-filter-label">De</label>
          <input
            type="date"
            className="in-date"
            value={value.from || firstOfMonthISO()}
            onChange={e => onChange({ ...value, from: e.target.value })}
          />
        </div>

        <div className="in-filter">
          <label className="in-filter-label">Até</label>
          <input
            type="date"
            className="in-date"
            value={value.to || lastOfMonthISO()}
            onChange={e => onChange({ ...value, to: e.target.value })}
          />
        </div>

        <div className="in-filter">
          <label className="in-filter-label">&nbsp;</label>
          <button
            type="button"
            className="in-chip"
            disabled={!fullRange}
            onClick={() => fullRange && onChange({ ...value, from: fullRange.from, to: fullRange.to })}
            title={fullRange ? `${fullRange.from} → ${fullRange.to}` : 'Selecione campanhas primeiro'}
          >
            Período completo
          </button>
        </div>

        <div className="in-filter in-filter--wide">
          <label className="in-filter-label">Emissoras</label>
          <RSelect
            isMulti
            options={stationOpts}
            value={stationOpts.filter(o => (value.stationIds || []).includes(o.value))}
            onChange={opts => onChange({ ...value, stationIds: opts.map(o => o.value) })}
            placeholder="Todas (padrão)"
            isDisabled={!value.campaignIds?.length}
          />
        </div>
      </div>

      <div className="in-filters-actions">
        <button type="button" className="in-btn-outline" onClick={onExportImage}>↓ Imagem</button>
        <button type="button" className="in-btn-outline" onClick={onExportPDF}>↓ PDF</button>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Adicionar estilos da FiltersBar ao CSS**

Append em `InsightsPage.css`:

```css
.in-filters {
  background: var(--c-surface);
  border: 1px solid var(--c-border);
  border-radius: var(--radius-lg);
  padding: 14px 18px;
  display: flex;
  align-items: flex-end;
  gap: 16px;
  flex-wrap: wrap;
  position: sticky;
  top: 0;
  z-index: 10;
  box-shadow: var(--shadow-sm);
}

.in-filters-row { display: flex; gap: 12px; flex-wrap: wrap; flex: 1; }
.in-filter { display: flex; flex-direction: column; gap: 4px; min-width: 160px; }
.in-filter--wide { min-width: 220px; flex: 1; }
.in-filter--locked .in-locked-chip {
  padding: 8px 12px;
  background: var(--c-action-light, #fce7f3);
  color: var(--c-action);
  border-radius: 999px;
  font-weight: 600;
  font-size: 13px;
}

.in-filter-label {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--c-text-2);
}

.in-date {
  padding: 7px 10px;
  border: 1px solid var(--c-border);
  border-radius: 8px;
  font: inherit;
  background: white;
}
.in-date:focus { outline: none; border-color: var(--c-action); box-shadow: 0 0 0 3px rgba(232,30,117,.1); }

.in-chip {
  padding: 8px 14px;
  border: 1px solid var(--c-border);
  background: white;
  border-radius: 999px;
  cursor: pointer;
  font: inherit;
  font-weight: 600;
  font-size: 13px;
  color: var(--c-text);
}
.in-chip:hover:not(:disabled) { border-color: var(--c-action); color: var(--c-action); }
.in-chip:disabled { opacity: 0.5; cursor: not-allowed; }

.in-filters-actions { display: flex; gap: 10px; align-items: flex-end; }

.in-btn-outline {
  padding: 9px 16px;
  border: 1px solid var(--c-border);
  background: white;
  border-radius: 8px;
  cursor: pointer;
  font: inherit;
  font-weight: 600;
  font-size: 13px;
  color: var(--c-text);
}
.in-btn-outline:hover { border-color: var(--c-action); color: var(--c-action); }
```

- [ ] **Step 3: Wire na página**

Substituir o body da `InsightsPage`:

```jsx
import { useState } from 'react'
import FiltersBar from '../components/insights/FiltersBar'
import { useInsights } from '../api/hooks'
import './InsightsPage.css'
import Footer from '../components/Footer'

function firstOfMonthISO() {
  const d = new Date()
  return new Date(Date.UTC(d.getFullYear(), d.getMonth(), 1)).toISOString().slice(0,10)
}
function lastOfMonthISO() {
  const d = new Date()
  return new Date(Date.UTC(d.getFullYear(), d.getMonth()+1, 0)).toISOString().slice(0,10)
}

export default function InsightsPage() {
  const [filters, setFilters] = useState({
    clientId: null,
    campaignIds: [],
    from: firstOfMonthISO(),
    to: lastOfMonthISO(),
    stationIds: [],
  })

  const { data, isPending, error } = useInsights({
    clientId: filters.clientId,
    campaignIds: filters.campaignIds,
    from: filters.from,
    to: filters.to,
    stationIds: filters.stationIds,
  })

  return (
    <div className="in-page">
      <header className="in-header">
        <h1 className="in-title">Dashboard de Veiculação</h1>
      </header>
      <FiltersBar
        value={filters}
        onChange={setFilters}
        onExportImage={() => {/* Task 17 */}}
        onExportPDF={() => {/* Task 17 */}}
      />
      <div className="in-body">
        {error && <div className="in-error">Erro: {String(error)}</div>}
        {isPending && filters.clientId && filters.campaignIds.length > 0 && (
          <div className="in-loading">Carregando…</div>
        )}
        {data && <pre style={{fontSize:11}}>{JSON.stringify(data, null, 2)}</pre>}
      </div>
      <Footer />
    </div>
  )
}
```

- [ ] **Step 4: Verificar no browser**

```bash
cd frontend && npm run dev
```

Em `/insights`, selecionar cliente → campanhas → ver payload bruto sendo retornado pelo backend. Validar que o filtro de período padrão = mês corrente.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/insights/FiltersBar.jsx frontend/src/pages/InsightsPage.jsx frontend/src/pages/InsightsPage.css
git commit -m "feat(insights): FiltersBar with client/campaigns/period/stations + role-locked client"
```

---

### Task 12: Frontend — KpiCards (Row 1, cards 1, 2, 3)

Implementa Impactos, CPM e Bonificação como cards simples. Cards 4 e 5 vêm depois.

**Files:**
- Create: `frontend/src/components/insights/KpiCards.jsx`

- [ ] **Step 1: Criar componente**

```jsx
// frontend/src/components/insights/KpiCards.jsx
import { MdInsights, MdAttachMoney, MdRedeem } from 'react-icons/md'

const fmtBR = new Intl.NumberFormat('pt-BR')
const fmtCurrency = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })
const fmtCompact = new Intl.NumberFormat('pt-BR', { notation: 'compact', maximumFractionDigits: 1 })

export default function KpiCards({ data }) {
  const k = data?.kpis
  if (!k) return null

  return (
    <>
      <div className="in-card" title="Total de impactos (PMM × inserções) por toda a seleção.">
        <div className="in-card-head">
          <span className="in-card-icon"><MdInsights /></span>
          <span className="in-card-label">Impactos</span>
        </div>
        <div className="in-card-value">{fmtCompact.format(k.impactos)}</div>
        <div className="in-card-sub">
          {fmtBR.format(k.veiculacoes_total)} veiculações · {k.stations_with_pmm} de {k.stations_count} emissoras com perfil
        </div>
      </div>

      <div className="in-card">
        <div className="in-card-head">
          <span className="in-card-icon"><MdAttachMoney /></span>
          <span className="in-card-label">CPM</span>
        </div>
        <div className="in-card-value">{fmtCurrency.format(k.cpm)}</div>
        <div className="in-card-sub">por mil impactos · base = investido executado</div>
      </div>

      <div className="in-card">
        <div className="in-card-head">
          <span className="in-card-icon"><MdRedeem /></span>
          <span className="in-card-label">Bonificação</span>
        </div>
        <div className="in-card-value">{fmtCurrency.format(k.bonificacao.valor)}</div>
        <div className="in-card-sub">{fmtBR.format(k.bonificacao.count)} inserções extras</div>
      </div>
    </>
  )
}
```

- [ ] **Step 2: CSS dos cards**

Append em `InsightsPage.css`:

```css
.in-row { display: grid; gap: 16px; }
.in-row--cards { grid-template-columns: repeat(5, 1fr); }
.in-row--charts { grid-template-columns: repeat(3, 1fr); }
@media (max-width: 1280px) { .in-row--cards { grid-template-columns: repeat(3, 1fr); } }
@media (max-width: 1024px) { .in-row--charts { grid-template-columns: 1fr; } }
@media (max-width: 720px)  { .in-row--cards  { grid-template-columns: 1fr 1fr; } }

.in-card {
  background: var(--c-surface);
  border: 1px solid var(--c-border);
  border-radius: var(--radius-lg);
  padding: 18px 20px;
  box-shadow: var(--shadow-sm);
  display: flex;
  flex-direction: column;
  gap: 6px;
  transition: transform 200ms, border-color 200ms, box-shadow 200ms;
}
.in-card:hover {
  transform: translateY(-2px);
  border-color: var(--c-action-border, #f9a8d4);
  box-shadow: var(--shadow-md);
}

.in-card-head { display: flex; align-items: center; gap: 8px; }
.in-card-icon {
  width: 32px; height: 32px;
  display: flex; align-items: center; justify-content: center;
  background: rgba(232, 30, 117, 0.08);
  color: var(--c-action);
  border-radius: 999px;
  font-size: 18px;
}
.in-card-label {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--c-text-2);
}
.in-card-value {
  font-family: var(--font-heading);
  font-size: 30px;
  font-weight: 700;
  color: var(--c-text);
  line-height: 1.1;
  letter-spacing: -0.02em;
}
.in-card-sub { font-size: 12px; color: var(--c-text-2); }
```

- [ ] **Step 3: Wire na página**

Em `InsightsPage.jsx`, substituir o `<pre>{JSON…}</pre>` e adicionar:

```jsx
import KpiCards from '../components/insights/KpiCards'
// ...
<div className="in-body">
  {error && <div className="in-error">Erro: {String(error)}</div>}
  {data && (
    <div className="in-row in-row--cards">
      <KpiCards data={data} />
      {/* placeholders para investimento + gênero — próximos passos */}
      <div className="in-card"><em>Investido (em construção)</em></div>
      <div className="in-card"><em>Gênero (em construção)</em></div>
    </div>
  )}
</div>
```

- [ ] **Step 4: Verificar no browser**

Selecionar cliente + campanhas → 3 cards aparecem com valores formatados. Hover anima translateY + borda rosa.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/insights/KpiCards.jsx frontend/src/pages/InsightsPage.jsx frontend/src/pages/InsightsPage.css
git commit -m "feat(insights): KPI cards (Impactos, CPM, Bonificação)"
```

---

### Task 13: Frontend — InvestmentToggleCard (Card 4) e GenderCard (Card 5)

**Files:**
- Create: `frontend/src/components/insights/InvestmentToggleCard.jsx`
- Create: `frontend/src/components/insights/GenderCard.jsx`

- [ ] **Step 1: Card de investimento com toggle**

```jsx
// frontend/src/components/insights/InvestmentToggleCard.jsx
import { useState } from 'react'
import { MdPayments } from 'react-icons/md'

const fmtCurrency = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })

export default function InvestmentToggleCard({ data }) {
  const [mode, setMode] = useState('contratado') // 'contratado' | 'executado'
  const inv = data?.kpis?.investido
  if (!inv) return null

  const value = mode === 'contratado' ? inv.contratado : inv.executado
  const pct = inv.contratado > 0 ? (inv.executado / inv.contratado) * 100 : 0

  return (
    <div className="in-card">
      <div className="in-card-head">
        <span className="in-card-icon"><MdPayments /></span>
        <span className="in-card-label">Investido</span>
        <div className="in-toggle">
          <button
            type="button"
            className={`in-toggle-btn ${mode === 'contratado' ? 'in-toggle-btn--active' : ''}`}
            onClick={() => setMode('contratado')}
          >Contratado</button>
          <button
            type="button"
            className={`in-toggle-btn ${mode === 'executado' ? 'in-toggle-btn--active' : ''}`}
            onClick={() => setMode('executado')}
          >Executado</button>
        </div>
      </div>
      <div className="in-card-value">{fmtCurrency.format(value)}</div>
      <div className="in-card-sub">
        {mode === 'contratado'
          ? `Executado: ${fmtCurrency.format(inv.executado)} (${pct.toFixed(0)}%)`
          : `Contratado: ${fmtCurrency.format(inv.contratado)}`}
      </div>
    </div>
  )
}
```

CSS append:

```css
.in-toggle {
  margin-left: auto;
  display: inline-flex;
  background: var(--c-surface-2);
  border-radius: 999px;
  padding: 2px;
  gap: 2px;
}
.in-toggle-btn {
  padding: 4px 10px;
  border: none;
  background: transparent;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 600;
  color: var(--c-text-2);
  cursor: pointer;
}
.in-toggle-btn--active { background: white; color: var(--c-action); box-shadow: var(--shadow-sm); }
```

- [ ] **Step 2: Card de gênero**

```jsx
// frontend/src/components/insights/GenderCard.jsx
import { MdPeople } from 'react-icons/md'

const fmtCompact = new Intl.NumberFormat('pt-BR', { notation: 'compact', maximumFractionDigits: 1 })

export default function GenderCard({ data }) {
  const g = data?.kpis?.gender
  if (!g) return null
  const total = g.m + g.f
  const mPct = total > 0 ? (g.m / total) * 100 : 0
  const fPct = total > 0 ? (g.f / total) * 100 : 0

  return (
    <div className="in-card">
      <div className="in-card-head">
        <span className="in-card-icon"><MdPeople /></span>
        <span className="in-card-label">Gênero (M / F)</span>
      </div>
      <div className="in-bar-stacked">
        <div className="in-bar-stacked-fill in-bar-stacked-fill--m" style={{ width: `${mPct}%` }} />
        <div className="in-bar-stacked-fill in-bar-stacked-fill--f" style={{ width: `${fPct}%` }} />
      </div>
      <div className="in-bar-legend">
        <span><strong>M:</strong> {mPct.toFixed(0)}% · {fmtCompact.format(g.m)}</span>
        <span><strong>F:</strong> {fPct.toFixed(0)}% · {fmtCompact.format(g.f)}</span>
      </div>
    </div>
  )
}
```

CSS append:

```css
.in-bar-stacked {
  display: flex;
  height: 28px;
  border-radius: 8px;
  overflow: hidden;
  background: var(--c-surface-2);
  margin-top: 4px;
}
.in-bar-stacked-fill { height: 100%; transition: width 280ms ease; }
.in-bar-stacked-fill--m { background: linear-gradient(135deg, #ec4899, #db2777); }
.in-bar-stacked-fill--f { background: linear-gradient(135deg, #a78bfa, #8b5cf6); }
.in-bar-legend {
  display: flex;
  gap: 12px;
  justify-content: space-between;
  font-size: 12px;
  color: var(--c-text-2);
}
.in-bar-legend strong { color: var(--c-text); }
```

- [ ] **Step 3: Wire na página**

Em `InsightsPage.jsx`, substituir os placeholders:

```jsx
import InvestmentToggleCard from '../components/insights/InvestmentToggleCard'
import GenderCard from '../components/insights/GenderCard'
// ...
<div className="in-row in-row--cards">
  <KpiCards data={data} />
  <InvestmentToggleCard data={data} />
  <GenderCard data={data} />
</div>
```

- [ ] **Step 4: Verificar**

Tela mostra 5 cards na Row 1. Toggle do investido alterna o valor. Barra de gênero mostra M/F com cores e percentuais corretos.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/insights/InvestmentToggleCard.jsx frontend/src/components/insights/GenderCard.jsx frontend/src/pages/InsightsPage.jsx frontend/src/pages/InsightsPage.css
git commit -m "feat(insights): InvestmentToggleCard + GenderCard (cards 4-5)"
```

---

### Task 14: Frontend — CustomTooltip + ClassPyramidChart + AgeRangeChart

**Files:**
- Create: `frontend/src/components/insights/CustomTooltip.jsx`
- Create: `frontend/src/components/insights/ClassPyramidChart.jsx`
- Create: `frontend/src/components/insights/AgeRangeChart.jsx`

- [ ] **Step 1: Tooltip glassmorphism**

```jsx
// frontend/src/components/insights/CustomTooltip.jsx
const fmtBR = new Intl.NumberFormat('pt-BR')

export default function CustomTooltip({ active, payload, label, formatter }) {
  if (!active || !payload?.length) return null
  return (
    <div className="in-tooltip">
      {label && <div className="in-tooltip-label">{label}</div>}
      <div className="in-tooltip-rows">
        {payload.map((p, i) => (
          <div key={i} className="in-tooltip-row">
            <span className="in-tooltip-dot" style={{ background: p.color }} />
            <span className="in-tooltip-name">{p.name}</span>
            <span className="in-tooltip-value">{formatter ? formatter(p.value) : fmtBR.format(p.value)}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
```

CSS append:

```css
.in-tooltip {
  background: rgba(255,255,255,0.95);
  backdrop-filter: blur(6px);
  border: 1px solid var(--c-border);
  border-radius: var(--radius-lg);
  padding: 10px 12px;
  box-shadow: var(--shadow-md);
  font-size: 12px;
  min-width: 140px;
}
.in-tooltip-label { font-weight: 600; color: var(--c-text); margin-bottom: 6px; }
.in-tooltip-rows { display: flex; flex-direction: column; gap: 4px; }
.in-tooltip-row { display: grid; grid-template-columns: 10px 1fr auto; gap: 8px; align-items: center; }
.in-tooltip-dot { width: 8px; height: 8px; border-radius: 50%; display: inline-block; }
.in-tooltip-name { color: var(--c-text-2); }
.in-tooltip-value { font-weight: 600; color: var(--c-text); }
```

- [ ] **Step 2: ClassPyramidChart**

```jsx
// frontend/src/components/insights/ClassPyramidChart.jsx
import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, Cell } from 'recharts'
import CustomTooltip from './CustomTooltip'

const COLORS = ['#E81E75', '#ec4899', '#f9a8d4'] // tertiary 500 / 400 / 300
const fmtCompact = new Intl.NumberFormat('pt-BR', { notation: 'compact', maximumFractionDigits: 1 })

export default function ClassPyramidChart({ data }) {
  const cp = data?.class_pyramid
  if (!cp) return null
  const rows = [
    { name: 'AB', value: cp.ab },
    { name: 'C',  value: cp.c },
    { name: 'DE', value: cp.de },
  ]
  return (
    <div className="in-chart-card">
      <h3 className="in-chart-title">Pirâmide de classe social</h3>
      <ResponsiveContainer width="100%" height={260}>
        <BarChart data={rows} layout="vertical" margin={{ top: 8, right: 24, bottom: 8, left: 8 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" horizontal={false} />
          <XAxis type="number" tickFormatter={v => fmtCompact.format(v)} tick={{ fontSize: 11, fill: '#6b7280' }} />
          <YAxis type="category" dataKey="name" tick={{ fontSize: 12, fontWeight: 600 }} />
          <Tooltip content={<CustomTooltip formatter={v => fmtCompact.format(v) + ' impactos'} />} />
          <Bar dataKey="value" name="Impactos" radius={[0, 8, 8, 0]}>
            {rows.map((_, i) => <Cell key={i} fill={COLORS[i]} />)}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  )
}
```

- [ ] **Step 3: AgeRangeChart**

```jsx
// frontend/src/components/insights/AgeRangeChart.jsx
import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, Cell } from 'recharts'
import CustomTooltip from './CustomTooltip'

const COLORS = ['#f9a8d4', '#ec4899', '#E81E75']
const fmtCompact = new Intl.NumberFormat('pt-BR', { notation: 'compact', maximumFractionDigits: 1 })

export default function AgeRangeChart({ data }) {
  const a = data?.age_ranges
  if (!a) return null
  const rows = [
    { name: '18-24', value: a.r18_24 },
    { name: '25-49', value: a.r25_49 },
    { name: '50+',   value: a.r50_plus },
  ]
  return (
    <div className="in-chart-card">
      <h3 className="in-chart-title">Faixa etária</h3>
      <ResponsiveContainer width="100%" height={260}>
        <BarChart data={rows} margin={{ top: 8, right: 16, bottom: 8, left: 0 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" vertical={false} />
          <XAxis dataKey="name" tick={{ fontSize: 12, fontWeight: 600 }} />
          <YAxis tickFormatter={v => fmtCompact.format(v)} tick={{ fontSize: 11, fill: '#6b7280' }} />
          <Tooltip content={<CustomTooltip formatter={v => fmtCompact.format(v) + ' impactos'} />} />
          <Bar dataKey="value" name="Impactos" radius={[8, 8, 0, 0]}>
            {rows.map((_, i) => <Cell key={i} fill={COLORS[i]} />)}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  )
}
```

CSS append:

```css
.in-chart-card {
  background: var(--c-surface);
  border: 1px solid var(--c-border);
  border-radius: var(--radius-lg);
  padding: 16px 18px;
  box-shadow: var(--shadow-sm);
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.in-chart-title {
  margin: 0;
  font-family: var(--font-heading);
  font-size: 16px;
  font-weight: 700;
  color: var(--c-text);
  letter-spacing: -0.01em;
}
```

- [ ] **Step 4: Wire na página (Row 2 parcial)**

```jsx
import ClassPyramidChart from '../components/insights/ClassPyramidChart'
import AgeRangeChart from '../components/insights/AgeRangeChart'
// ...
<div className="in-row in-row--charts">
  <ClassPyramidChart data={data} />
  <AgeRangeChart data={data} />
  <div className="in-chart-card"><em>% Veiculações (em construção)</em></div>
</div>
```

- [ ] **Step 5: Verificar**

Dois gráficos de barras aparecem na Row 2 com tooltips formatados.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/insights/CustomTooltip.jsx frontend/src/components/insights/ClassPyramidChart.jsx frontend/src/components/insights/AgeRangeChart.jsx frontend/src/pages/InsightsPage.jsx frontend/src/pages/InsightsPage.css
git commit -m "feat(insights): class pyramid + age range charts with glassmorphism tooltip"
```

---

### Task 15: Frontend — BroadcastShareChart (donut de % veiculações)

**Files:**
- Create: `frontend/src/components/insights/BroadcastShareChart.jsx`

- [ ] **Step 1: Componente**

```jsx
// frontend/src/components/insights/BroadcastShareChart.jsx
import { PieChart, Pie, Cell, Tooltip, Legend, ResponsiveContainer } from 'recharts'

const fmtBR = new Intl.NumberFormat('pt-BR')

const COLORS = {
  inSlot:  '#10b981', // verde
  outSlot: '#f59e0b', // âmbar
  outDate: '#8b5cf6', // roxo
  orphan:  '#3b82f6', // azul
}

export default function BroadcastShareChart({ data }) {
  const b = data?.veiculacoes_breakdown
  if (!b) return null
  const total = b.in_slot + b.out_slot + b.out_date + b.extras_orphan
  const rows = [
    { name: 'Dentro da faixa', value: b.in_slot, fill: COLORS.inSlot },
    { name: 'Fora da faixa',   value: b.out_slot, fill: COLORS.outSlot },
    { name: 'Fora da data',    value: b.out_date, fill: COLORS.outDate },
    { name: 'Extras/orphan',   value: b.extras_orphan, fill: COLORS.orphan },
  ]
  return (
    <div className="in-chart-card">
      <h3 className="in-chart-title">% Veiculações</h3>
      <div style={{ position: 'relative', height: 260 }}>
        <ResponsiveContainer width="100%" height="100%">
          <PieChart>
            <Pie data={rows} dataKey="value" innerRadius={60} outerRadius={92} paddingAngle={2}>
              {rows.map((r, i) => <Cell key={i} fill={r.fill} />)}
            </Pie>
            <Tooltip
              content={({ active, payload }) => {
                if (!active || !payload?.length) return null
                const p = payload[0]
                const pct = total > 0 ? (p.value / total * 100).toFixed(1) : 0
                return (
                  <div className="in-tooltip">
                    <div className="in-tooltip-label">{p.name}</div>
                    <div className="in-tooltip-rows">
                      <div className="in-tooltip-row">
                        <span className="in-tooltip-name">Total</span>
                        <span className="in-tooltip-value">{fmtBR.format(p.value)} ({pct}%)</span>
                      </div>
                    </div>
                  </div>
                )
              }}
            />
            <Legend
              layout="vertical"
              align="right"
              verticalAlign="middle"
              iconType="circle"
              wrapperStyle={{ fontSize: 12 }}
              formatter={(v, e) => {
                const val = e?.payload?.value || 0
                return `${v}: ${fmtBR.format(val)}`
              }}
            />
          </PieChart>
        </ResponsiveContainer>
        <div className="in-donut-center">
          <div className="in-donut-center-value">{fmtBR.format(total)}</div>
          <div className="in-donut-center-label">veiculações</div>
        </div>
      </div>
    </div>
  )
}
```

CSS append:

```css
.in-donut-center {
  position: absolute;
  top: 50%; left: 30%; /* ajustado pq legend tá à direita */
  transform: translate(-50%, -50%);
  text-align: center;
  pointer-events: none;
}
.in-donut-center-value {
  font-family: var(--font-heading);
  font-size: 22px;
  font-weight: 700;
  color: var(--c-text);
}
.in-donut-center-label {
  font-size: 11px;
  color: var(--c-text-2);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
```

- [ ] **Step 2: Wire na página**

Substituir o placeholder:

```jsx
import BroadcastShareChart from '../components/insights/BroadcastShareChart'
// ...
<div className="in-row in-row--charts">
  <ClassPyramidChart data={data} />
  <AgeRangeChart data={data} />
  <BroadcastShareChart data={data} />
</div>
```

- [ ] **Step 3: Verificar**

Donut aparece com 4 slices, legenda à direita, total no centro. Hover mostra tooltip com nome + abs + %.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/insights/BroadcastShareChart.jsx frontend/src/pages/InsightsPage.jsx frontend/src/pages/InsightsPage.css
git commit -m "feat(insights): donut chart of veiculações breakdown (in/out/orphan)"
```

---

### Task 16: Frontend — DailySummaryChart (Row 3 full-width)

**Files:**
- Create: `frontend/src/components/insights/DailySummaryChart.jsx`

- [ ] **Step 1: Componente**

```jsx
// frontend/src/components/insights/DailySummaryChart.jsx
import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, Legend, ResponsiveContainer } from 'recharts'

const fmtBR = new Intl.NumberFormat('pt-BR')
const monthLabel = new Intl.DateTimeFormat('pt-BR', { month: 'short', year: '2-digit' })

const COLORS = {
  programado: '#9ca3af',
  in_slot:    '#10b981',
  out_slot:   '#f59e0b',
  out_date:   '#8b5cf6',
  deficit:    '#ef4444',
  extras:     '#3b82f6',
}

function fmtBucket(b, gran) {
  if (gran === 'month') {
    // 'YYYY-MM' → 'mai./26'
    const [y, m] = b.split('-')
    return monthLabel.format(new Date(Number(y), Number(m) - 1, 1))
  }
  // 'YYYY-MM-DD' → 'DD/MM'
  return b.slice(8, 10) + '/' + b.slice(5, 7)
}

export default function DailySummaryChart({ data }) {
  const gran = data?.period?.granularity || 'day'
  const rows = (data?.buckets || []).map(b => ({
    ...b,
    bucket_label: fmtBucket(b.bucket, gran),
  }))
  if (!rows.length) return null

  return (
    <div className="in-chart-card">
      <h3 className="in-chart-title">
        Resumo {gran === 'month' ? 'mensal' : 'diário'} da campanha
      </h3>
      <ResponsiveContainer width="100%" height={360}>
        <BarChart data={rows} margin={{ top: 8, right: 16, bottom: 24, left: 0 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" vertical={false} />
          <XAxis dataKey="bucket_label" tick={{ fontSize: 11, fill: '#6b7280' }} interval="preserveStartEnd" />
          <YAxis tickFormatter={v => fmtBR.format(v)} tick={{ fontSize: 11, fill: '#6b7280' }} />
          <Tooltip
            cursor={{ fill: 'rgba(232,30,117,0.05)' }}
            content={({ active, payload, label }) => {
              if (!active || !payload?.length) return null
              return (
                <div className="in-tooltip">
                  <div className="in-tooltip-label">{label}</div>
                  <div className="in-tooltip-rows">
                    {payload.map((p, i) => (
                      <div key={i} className="in-tooltip-row">
                        <span className="in-tooltip-dot" style={{ background: p.color }} />
                        <span className="in-tooltip-name">{p.name}</span>
                        <span className="in-tooltip-value">{fmtBR.format(p.value)}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )
            }}
          />
          <Legend wrapperStyle={{ fontSize: 11, paddingTop: 8 }} iconType="circle" />
          <Bar dataKey="programado" name="Programado" fill={COLORS.programado} radius={[4,4,0,0]} />
          <Bar dataKey="in_slot"    name="Dentro da faixa" fill={COLORS.in_slot} radius={[4,4,0,0]} />
          <Bar dataKey="out_slot"   name="Fora da faixa" fill={COLORS.out_slot} radius={[4,4,0,0]} />
          <Bar dataKey="out_date"   name="Fora da data" fill={COLORS.out_date} radius={[4,4,0,0]} />
          <Bar dataKey="deficit"    name="Déficit" fill={COLORS.deficit} radius={[4,4,0,0]} />
          <Bar dataKey="extras"     name="Extras" fill={COLORS.extras} radius={[4,4,0,0]} />
        </BarChart>
      </ResponsiveContainer>
    </div>
  )
}
```

- [ ] **Step 2: Wire na página**

```jsx
import DailySummaryChart from '../components/insights/DailySummaryChart'
// ...
<div className="in-row in-row--charts">
  {/* gráficos 1,2,3 */}
</div>
<div className="in-row">
  <DailySummaryChart data={data} />
</div>
```

- [ ] **Step 3: Verificar**

Row 3 mostra barras agrupadas. Filtros com período > 31 dias mudam o eixo X pra meses automaticamente.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/insights/DailySummaryChart.jsx frontend/src/pages/InsightsPage.jsx
git commit -m "feat(insights): daily/monthly grouped bar chart (Row 3 full-width)"
```

---

### Task 17: Frontend — Empty states (Tutorial Estilizado)

**Files:**
- Create: `frontend/src/components/insights/EmptyTutorial.jsx`

- [ ] **Step 1: Componente**

```jsx
// frontend/src/components/insights/EmptyTutorial.jsx
import { MdInsights, MdLockOutline } from 'react-icons/md'

export default function EmptyTutorial({ variant }) {
  const variants = {
    'no-client': {
      icon: <MdInsights />,
      title: 'Selecione um cliente',
      text: 'Escolha um cliente e até 50 campanhas para visualizar impactos demográficos consolidados.',
    },
    'no-campaigns': {
      icon: <MdInsights />,
      title: 'Selecione 1 ou mais campanhas',
      text: 'O dashboard agrega dados das campanhas selecionadas dentro do período filtrado.',
    },
    'no-data': {
      icon: <MdInsights />,
      title: 'Sem veiculações no período',
      text: 'As campanhas selecionadas não tiveram veiculações dentro do intervalo. Tente ampliar o período.',
    },
    'client-no-campaigns': {
      icon: <MdLockOutline />,
      title: 'Você ainda não tem campanhas',
      text: 'Fale com seu gerente comercial pra liberar acesso a uma campanha.',
    },
  }
  const v = variants[variant] || variants['no-client']

  return (
    <div className="in-empty">
      <div className="in-empty-action">
        <div className="in-empty-icon">{v.icon}</div>
        <h2 className="in-empty-title">{v.title}</h2>
        <p className="in-empty-text">{v.text}</p>
      </div>
      <div className="in-empty-preview" aria-hidden="true">
        <div className="in-empty-card-row">
          <div className="in-empty-card" />
          <div className="in-empty-card" />
          <div className="in-empty-card" />
          <div className="in-empty-card" />
          <div className="in-empty-card" />
        </div>
        <div className="in-empty-chart-row">
          <div className="in-empty-chart" />
          <div className="in-empty-chart" />
          <div className="in-empty-chart" />
        </div>
        <div className="in-empty-chart in-empty-chart--wide" />
      </div>
    </div>
  )
}
```

CSS append:

```css
.in-empty {
  display: grid;
  grid-template-columns: 1fr 1.4fr;
  gap: 40px;
  align-items: center;
  background: var(--c-surface);
  border: 1px solid var(--c-border);
  border-radius: var(--radius-lg);
  padding: 48px;
  min-height: 420px;
}
@media (max-width: 1024px) { .in-empty { grid-template-columns: 1fr; } }

.in-empty-action { display: flex; flex-direction: column; gap: 14px; align-items: flex-start; }
.in-empty-icon {
  width: 72px; height: 72px;
  border-radius: 999px;
  background: rgba(232,30,117,0.1);
  color: var(--c-action);
  display: flex; align-items: center; justify-content: center;
  font-size: 36px;
}
.in-empty-title {
  margin: 0;
  font-family: var(--font-heading);
  font-size: 24px;
  font-weight: 700;
  color: var(--c-text);
  letter-spacing: -0.02em;
}
.in-empty-text { margin: 0; color: var(--c-text-2); max-width: 380px; line-height: 1.5; }

.in-empty-preview {
  opacity: 0.35;
  display: flex; flex-direction: column; gap: 12px;
  pointer-events: none;
}
.in-empty-card-row { display: grid; grid-template-columns: repeat(5, 1fr); gap: 8px; }
.in-empty-card { height: 80px; background: var(--c-surface-2); border-radius: var(--radius-lg); }
.in-empty-chart-row { display: grid; grid-template-columns: repeat(3, 1fr); gap: 8px; }
.in-empty-chart { height: 140px; background: var(--c-surface-2); border-radius: var(--radius-lg); }
.in-empty-chart--wide { height: 160px; background: var(--c-surface-2); border-radius: var(--radius-lg); }
```

- [ ] **Step 2: Wire na página**

```jsx
import EmptyTutorial from '../components/insights/EmptyTutorial'
// ...

const { isAdmin } = useAuth()  // adicione o import: import { useAuth } from '../contexts/AuthContext'

// Lógica de variant:
let emptyVariant = null
if (!filters.clientId) {
  emptyVariant = 'no-client'
} else if (!filters.campaignIds?.length) {
  emptyVariant = isAdmin ? 'no-campaigns' : 'client-no-campaigns'
} else if (data && data.kpis.veiculacoes_total === 0) {
  emptyVariant = 'no-data'
}

return (
  <div className="in-page">
    {/* header + filtersbar … */}
    <div className="in-body">
      {emptyVariant && <EmptyTutorial variant={emptyVariant} />}
      {!emptyVariant && data && (
        <>
          <div className="in-row in-row--cards">…</div>
          <div className="in-row in-row--charts">…</div>
          <div className="in-row"><DailySummaryChart data={data} /></div>
        </>
      )}
    </div>
  </div>
)
```

- [ ] **Step 3: Verificar**

- Admin sem cliente → variant `no-client`
- Admin com cliente, sem campanhas → `no-campaigns`
- Cliente sem campanhas (caso raro) → `client-no-campaigns`
- Filtro retornando vazio → `no-data`

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/insights/EmptyTutorial.jsx frontend/src/pages/InsightsPage.jsx frontend/src/pages/InsightsPage.css
git commit -m "feat(insights): empty states with shadow UI tutorial preview"
```

---

### Task 18: Frontend — Export PNG e PDF

**Files:**
- Create: `frontend/src/utils/exportInsights.js`

- [ ] **Step 1: Util**

```js
// frontend/src/utils/exportInsights.js
import html2canvas from 'html2canvas'
import { jsPDF } from 'jspdf'

function slugify(s) {
  return String(s || 'insights').toLowerCase()
    .normalize('NFD').replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 60) || 'insights'
}

function saveBlob(blob, filename) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url; a.download = filename
  document.body.appendChild(a); a.click(); a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

async function captureCanvas(el) {
  return await html2canvas(el, {
    backgroundColor: '#f8fafc',
    scale: 2,
    useCORS: true,
    logging: false,
  })
}

export async function exportInsightsPNG(el, { clientName, period } = {}) {
  const canvas = await captureCanvas(el)
  canvas.toBlob(blob => {
    saveBlob(blob, `dashboard-${slugify(clientName)}-${period?.from}-${period?.to}.png`)
  }, 'image/png')
}

export async function exportInsightsPDF(el, { clientName, period, campaigns } = {}) {
  const canvas = await captureCanvas(el)
  const imgData = canvas.toDataURL('image/png')

  const doc = new jsPDF({ orientation: 'landscape', unit: 'mm', format: 'a4' })
  const W = doc.internal.pageSize.getWidth()
  const H = doc.internal.pageSize.getHeight()

  // Capa
  doc.setFillColor(248, 250, 252); doc.rect(0, 0, W, H, 'F')
  doc.setTextColor(6, 5, 91)
  doc.setFontSize(22); doc.text('Dashboard de Veiculação', W/2, H/3, { align: 'center' })
  doc.setFontSize(16); doc.text(clientName || '—', W/2, H/3 + 12, { align: 'center' })
  doc.setFontSize(12); doc.setTextColor(75, 85, 99)
  doc.text(`Período: ${period?.from || '—'} → ${period?.to || '—'}`, W/2, H/3 + 24, { align: 'center' })
  if (campaigns?.length) {
    doc.setFontSize(11)
    doc.text(`${campaigns.length} campanha(s) selecionada(s)`, W/2, H/3 + 32, { align: 'center' })
  }
  doc.setFontSize(9)
  doc.text(`Gerado em ${new Date().toLocaleString('pt-BR')}`, W/2, H - 12, { align: 'center' })

  // Página 2 — screenshot
  doc.addPage()
  const imgW = W - 16
  const imgH = (canvas.height / canvas.width) * imgW
  const finalH = Math.min(imgH, H - 16)
  const finalW = finalH < imgH ? (canvas.width / canvas.height) * finalH : imgW
  doc.addImage(imgData, 'PNG', (W - finalW) / 2, (H - finalH) / 2, finalW, finalH)

  doc.save(`dashboard-${slugify(clientName)}-${period?.from}-${period?.to}.pdf`)
}
```

- [ ] **Step 2: Wire na página**

```jsx
import { useRef } from 'react'
import { exportInsightsPNG, exportInsightsPDF } from '../utils/exportInsights'
// ...
const dashboardRef = useRef(null)
const clientName = useMemo(() => {
  if (!filters.clientId) return ''
  // pegar do payload se existir, ou do clients hook
  return data?.campaigns?.[0]?.client_name || ''
}, [data, filters.clientId])

const handleExportImage = async () => {
  if (dashboardRef.current) {
    await exportInsightsPNG(dashboardRef.current, { clientName, period: data?.period })
  }
}
const handleExportPDF = async () => {
  if (dashboardRef.current) {
    await exportInsightsPDF(dashboardRef.current, {
      clientName,
      period: data?.period,
      campaigns: data?.campaigns,
    })
  }
}

return (
  <div className="in-page">
    <header className="in-header">…</header>
    <FiltersBar
      value={filters}
      onChange={setFilters}
      onExportImage={handleExportImage}
      onExportPDF={handleExportPDF}
    />
    <div ref={dashboardRef} className="in-body">…</div>
    <Footer />
  </div>
)
```

> **Nota:** o `useMemo` precisa de `import { useMemo } from 'react'`.

- [ ] **Step 3: Testar manualmente**

- Clicar "↓ Imagem" → baixa PNG do dashboard
- Clicar "↓ PDF" → baixa PDF de 2 páginas (capa + screenshot)
- Conferir que cores, fontes e gráficos aparecem no PNG

- [ ] **Step 4: Commit**

```bash
git add frontend/src/utils/exportInsights.js frontend/src/pages/InsightsPage.jsx
git commit -m "feat(insights): export dashboard as PNG (html2canvas) and PDF (jsPDF)"
```

---

### Task 19: Documentação operacional

**Files:**
- Create: `docs/features/insights-dashboard.md`
- Modify: `docs/README.md`

- [ ] **Step 1: Doc da feature**

Criar com este conteúdo:

```markdown
---
status: implementado
ultima-verificacao: 2026-05-26
codigo-relacionado:
  - workers/internal/catalog/insights.go
  - workers/internal/api/handlers/insights.go
  - workers/internal/api/router.go
  - frontend/src/pages/InsightsPage.jsx
  - frontend/src/components/insights/
  - frontend/src/utils/exportInsights.js
---

# Dashboard de Veiculação (`/insights`)

Tela consolidada com KPIs e gráficos demográficos por cliente × campanha(s) × período × emissoras. Substitui o PDF manual que o time comercial montava ao fim de cada campanha.

## Quem vê

- **Admin:** seleciona cliente → campanhas → tudo
- **Cliente:** vê só as próprias campanhas (anti-oracle via `auth.ClientScopeFromContext`)

## Endpoint

`GET /api/v1/internal/insights?client_id=...&campaigns=uuid,uuid&from=YYYY-MM-DD&to=YYYY-MM-DD&stations=uuid,uuid`

Defaults: `from`/`to` = mês corrente. `stations` opcional (vazio = todas).

## Fórmulas (cópia rápida; autoridade é a spec)

| Métrica | Fórmula |
|---|---|
| Impactos | `Σ_estação (count_detections × PMM)` (todas categorias) |
| Gênero M | `Σ_estação (count × PMM × gender.male_pct)` (estação sem perfil → exclui) |
| CPM | `(investido_executado / impactos) × 1000` |
| Bonificação | `count(orphan) × valor_unitário` |
| Investido contratado (per_insertion) | `price × programmed_count_in_overlap` |
| Investido executado (per_insertion) | `price × (in_slot + out_slot)` |
| Investido contratado (consolidated) | `consolidated × (days_overlap / camp_days)` |
| Déficit (bucket) | `max(0, programado - in_slot - out_slot)` |

## Como adicionar nova métrica

1. Adicionar campo no struct `InsightsKPIs` ou similar em `workers/internal/catalog/insights.go`
2. Calcular na SQL apropriada (provavelmente `aggregateCore` ou `aggregateInvestment`)
3. Adicionar teste em `insights_test.go`
4. Renderizar no front (novo card/gráfico) consumindo do payload

## Troubleshooting

- **"Estação sem PMM" — impactos vêm baixos:** rodar `psql ... -c "SELECT name, pmm, meta IS NOT NULL FROM stations WHERE pmm IS NULL"`. Editar perfil em `/stations/:id/edit`.
- **Export PNG vazio:** verificar que o ref do `in-body` está mountado quando o usuário clica. Se `dashboardRef.current` é null, o handler retorna sem fazer nada.
- **PDF cortado:** ajustar o `finalH = Math.min(imgH, H - 16)` em `exportInsights.js` se a margem ficar curta.
- **Performance ruim (>2s):** rodar com tracing habilitado e verificar tempo de cada CTE em `aggregateCore`. Considerar materialized view por mês.

## Limitações conhecidas

- "Extras" no gráfico 4 só captura `category='orphan'`. Detecções `in_slot` acima do programado para um material específico continuam contando como `in_slot` (não viram extras).
- Investido em modo `consolidated` prorrateia linearmente por dias, sem considerar distribuição irregular de slots dentro da campanha.
- Cliente com 50+ campanhas: o filtro `RSelect` carrega todas. Se virar problema, adicionar busca server-side.
```

- [ ] **Step 2: Atualizar docs/README.md**

Em [docs/README.md](docs/README.md), seção "Mapa de consulta", adicionar linha:

```markdown
| Dashboard de veiculação (`/insights`, admin + cliente, com export PNG/PDF) | [docs/features/insights-dashboard.md](docs/features/insights-dashboard.md) |
```

(inserir em ordem alfabética ou lógica próxima ao item de "campaign-reports").

- [ ] **Step 3: Commit**

```bash
git add docs/features/insights-dashboard.md docs/README.md
git commit -m "docs(insights): operational guide for /insights dashboard"
```

---

### Task 20: Verificação final + testes manuais

- [ ] **Step 1: Rodar TODA a suite de testes**

```bash
cd workers && go test ./... -short
cd ../frontend && npm test -- --run
```

Esperado: tudo PASS. Se algo regrediu, investigar.

- [ ] **Step 2: Testes manuais no browser**

Roteiro:

1. Login como admin. Sidebar mostra "Dashboard" no grupo Veiculação. Click → vai pra `/insights`.
2. Estado inicial: empty `no-client`. Preview decorativo aparece com opacidade.
3. Selecionar cliente → empty muda para `no-campaigns`.
4. Selecionar 1 campanha → vê todos os cards e gráficos. Hover dos cards anima translateY + borda rosa.
5. Clicar toggle "Contratado/Executado" no card de Investido → valor muda.
6. Trocar período pra um intervalo > 31 dias → eixo X do gráfico 4 vira mensal.
7. Selecionar 2-3 campanhas com períodos sobrepostos → dados agregam corretamente.
8. Filtrar por 1 emissora → cards e gráficos recalculam, eliminando contribuições das outras.
9. Botão "Período completo" → seta range = união das campanhas.
10. Logout, login como cliente. Sidebar mostra "Dashboard". `/insights` abre. Cliente está locked (chip read-only no lugar do select). Vê suas campanhas. Tentativa de manipular query string com client_id de outro cliente → 403 do backend.
11. Click "↓ Imagem" → baixa PNG. Abrir, conferir.
12. Click "↓ PDF" → baixa PDF de 2 páginas. Conferir capa e screenshot.

- [ ] **Step 3: Verificar responsividade**

Reduzir janela para 1024px → cards quebram em 3 colunas, gráficos demográficos viram coluna única. Em 720px → cards quebram em 2 colunas. Em mobile (375px) → tudo em coluna única.

- [ ] **Step 4: Smoke do P95 do backend**

```bash
time curl -H "Authorization: Bearer $JWT_ADMIN" \
  "http://localhost:8080/api/v1/internal/insights?client_id=$CID&campaigns=$C1,$C2&from=2026-01-01&to=2026-06-30"
```

Esperado: < 2s em ambiente local com dataset modesto. Se > 2s, abrir o tracing OTel (Jaeger) e identificar o CTE lento.

- [ ] **Step 5: Sanity check git**

```bash
git log --oneline master..HEAD | head -30
git diff --stat master..HEAD
```

Esperado: ~20 commits, alterações em workers/, frontend/, docs/.

- [ ] **Step 6: Commit final (se necessário)**

Se durante os testes manuais aparecerem ajustes pequenos, fazer um commit "polish" agrupando-os.

```bash
git add -A
git commit -m "polish(insights): manual QA fixes"
```

---

## Self-Review do Plan

**1. Spec coverage:**
- ✅ Rota `/insights` (Task 9)
- ✅ Sidebar AdminNav + ClientNav (Task 9)
- ✅ Filtros: cliente, campanhas, período, emissoras, período completo (Task 11)
- ✅ 5 cards de KPI: Impactos / CPM / Bonificação / Investido toggle / Gênero (Tasks 12-13)
- ✅ 3 gráficos demográficos: classe / idade / % veiculações (Tasks 14-15)
- ✅ Gráfico daily/monthly (Task 16)
- ✅ Bucketização automática (Task 6 backend + Task 16 frontend formatter)
- ✅ Export PNG e PDF (Task 18)
- ✅ Empty states "Tutorial Estilizado" (Task 17)
- ✅ Anti-oracle backend (Task 3 anti-cross-client + Task 8 force scope)
- ✅ Doc operacional (Task 19)
- ✅ Atualização docs/README.md (Task 19)

**2. Placeholder scan:** uma ocorrência intencional: `plays_per_day := func() {}` no Task 6 era pra ser placeholder mas foi removida na nota seguinte. Verificado: a versão final do CTE `planned` está completa e correta.

**3. Type consistency:**
- `InsightsParams`: usado em Task 2 (def), Task 3-7 (chamadas), Task 8 (handler) ✓
- `InsightsPayload`: idem ✓
- `BucketRow.Programado` (int) consistente em Task 2, Task 6, Task 16 ✓
- Nomes JSON: `r18_24` no Go vs `r18_24` consumido como `data.age_ranges.r18_24` no Task 14 ✓

**4. Riscos não cobertos:** nenhum dos riscos da §8 da spec ficou sem tratamento — todos têm task associada ou ressalva nas notas inline.

---

## Execution Handoff

**Plan complete and saved to** `docs/superpowers/plans/2026-05-26-insights-dashboard.md`.

Two execution options:

1. **Subagent-Driven (recommended)** — Despacho um subagente fresh por task, com revisão entre tasks. Iteração rápida e cada task tem contexto limpo.

2. **Inline Execution** — Executo as tasks nesta sessão usando `superpowers:executing-plans`, com checkpoints pra revisão.

**Qual abordagem?**
