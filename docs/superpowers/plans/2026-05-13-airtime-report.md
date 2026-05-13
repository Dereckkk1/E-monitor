# Relatório Data e Hora — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Construir nova tela `/reports/airtime` com lista cronológica paginada de veiculações + painel "Total por áudio" + export CSV, conforme [spec](../specs/2026-05-13-airtime-report-design.md).

**Architecture:** Backend Go (chi) ganha 3 endpoints (lista paginada gated em `page`, agregado por material, export CSV admin-only). Frontend React adiciona uma página com split master-detail, reusando `AudioPlayer`, `SmartImage`, `RSelect`, `BadgePill`, `tokenize` e padrões já estabelecidos. PMM/custo são calculados client-side reutilizando a lógica do `DistributionGrid`.

**Tech Stack:** Go 1.22 + chi v5 + pgx v5 (backend), React 19 + Vite + react-query v5 + react-router v6 + react-select v5 (frontend). Sem TDD no frontend (não há framework de teste); verificação por `npm run lint` + `npm run build` + smoke manual. TDD no backend via `go test ./...`.

---

## File Structure

### Backend (Go)

- **Modify** `workers/internal/catalog/detections.go` — adicionar `ListPagedFilter`, método `ListPaged`, método `AggregateByMaterial`, método `ExportRows` (streaming-friendly iterator).
- **Modify** `workers/internal/api/handlers/detections.go` — adicionar handlers `ListPaged` (executa quando `page` query param presente), `AggregateByMaterial`, `Export`.
- **Modify** `workers/internal/api/router.go` — registrar rotas novas: `GET /detections/aggregate-by-material` (público dentro do `/v1/internal`), `GET /detections/export` (admin only).
- **Modify** `workers/internal/catalog/detections_test.go` — testes de paginação, busca, agregado.
- **Modify** `workers/internal/api/handlers/detections_test.go` — testes de validação de query params dos endpoints novos.

### Frontend (React)

- **Modify** `frontend/src/api/hooks.js` — adicionar `useDetectionsPaged`, `useMaterialAggregate`, `exportDetectionsCsv` (função imperativa).
- **Create** `frontend/src/pages/AirtimeReportPage.jsx` — página principal.
- **Create** `frontend/src/pages/AirtimeReportPage.css` — estilos da página.
- **Create** `frontend/src/components/AirtimeFiltersBar.jsx` — barra de filtros sticky.
- **Create** `frontend/src/components/AirtimeDetectionRow.jsx` — card de uma veiculação.
- **Create** `frontend/src/components/AirtimeMaterialPanel.jsx` — painel "Total por áudio".
- **Create** `frontend/src/components/AirtimePaginator.jsx` — paginador numerado.
- **Create** `frontend/src/components/AirtimeGhostPreview.jsx` — ghost preview para empty states.
- **Modify** `frontend/src/components/Sidebar.jsx` — adicionar `IconClock` + entrada no nav admin e cliente.
- **Modify** `frontend/src/App.jsx` — registrar rota `/reports/airtime`.

### Docs

- **Create** `docs/airtime-report.md` — documentação operacional pós-implementação.

---

## Phase 1 — Backend (paginação)

### Task 1: Repository paginado em `catalog.Detections`

**Files:**
- Modify: `workers/internal/catalog/detections.go`
- Test: `workers/internal/catalog/detections_test.go`

- [ ] **Step 1: Adicionar tipos e método `ListPaged` em `catalog/detections.go`**

Adicionar **logo após** o tipo `ListFilter` existente (linha ~269), preservando o método `List` antigo para não quebrar o `DayDetailModal`:

```go
// ListPagedFilter mirrors ListFilter but with offset/limit semantics suited
// for page-based pagination and an optional case/accent-insensitive search
// over station/material/type/client text fields.
type ListPagedFilter struct {
	CampaignID *uuid.UUID
	StartDate  *time.Time
	EndDate    *time.Time
	Q          string // free text; empty disables the filter
	Sort       string // "detected_at_desc" (default) | "detected_at_asc"
	Page       int    // 1-based
	PageSize   int    // 1..200
}

// ListPagedResult is the wire format returned to the frontend. Total is a
// separate count(*) so the paginator can render "X of N" + last-page jump.
type ListPagedResult struct {
	Data       []DetectionEnriched `json:"data"`
	Page       int                 `json:"page"`
	PageSize   int                 `json:"page_size"`
	Total      int                 `json:"total"`
	TotalPages int                 `json:"total_pages"`
}

// DetectionEnriched extends Detection with the joined columns the airtime
// report card needs in one round-trip. Adding new fields is safe — JSON
// decoders ignore unknown keys, and the airtime-report card consumes a
// dedicated hook (useDetectionsPaged) that knows the shape.
type DetectionEnriched struct {
	Detection
	StationFrequencyMHz *float64 `json:"station_frequency_mhz,omitempty"`
	StationBand         *string  `json:"station_band,omitempty"`
	StationCity         *string  `json:"station_city,omitempty"`
	StationState        *string  `json:"station_state,omitempty"`
	StationLogo         *string  `json:"station_logo,omitempty"`
	StationPMM          *float64 `json:"station_pmm,omitempty"`
	MaterialDurationSec *int32   `json:"material_duration_sec,omitempty"`
	MaterialTypeName    *string  `json:"material_type_name,omitempty"`
	MaterialTypeColor   *string  `json:"material_type_color,omitempty"`
	ClientID            *uuid.UUID `json:"client_id,omitempty"`
	ClientName          *string  `json:"client_name,omitempty"`
}

// ListPaged is the cronological detection list backing /reports/airtime.
// Performs a single query with COUNT(*) OVER () for total — fine up to
// ~1M rows per campaign/period; if that becomes a hotspot we can split
// into two queries with a planner-friendlier shape.
func (d *Detections) ListPaged(ctx context.Context, f ListPagedFilter) (*ListPagedResult, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 200 {
		f.PageSize = 10
	}
	order := "DESC"
	if f.Sort == "detected_at_asc" {
		order = "ASC"
	}

	q := strings.TrimSpace(f.Q)
	var qPattern any = nil
	if q != "" {
		// Normaliza tokens com unaccent + lower; cada token vira um LIKE
		// independente combinados por AND no WHERE. Limita a 4 tokens pra
		// manter o plan simples.
		toks := strings.Fields(q)
		if len(toks) > 4 {
			toks = toks[:4]
		}
		qPattern = toks
	}

	sql := `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), d.commercial_id, COALESCE(c.title, ''),
		       d.campaign_id, d.detected_at,
		       d.match_start_offset_ms, d.match_end_offset_ms, d.confidence, d.hash_count,
		       d.temporal_coverage, d.variant_used, d.rate_used,
		       d.evidence_status, d.evidence_key, d.evidence_size_bytes, d.category,
		       m.type_id, d.retracted_at, d.ignored_at, d.ignored_by,
		       d.manual_at, d.manual_by, d.manual_note, d.created_at,
		       s.frequency_mhz, s.band, s.city, s.state, s.logo, s.pmm,
		       m.duration_sec, mt.name, mt.color,
		       cmp.client_id, cli.name,
		       COUNT(*) OVER () AS total
		FROM detections d
		LEFT JOIN stations s        ON s.id = d.station_id
		LEFT JOIN commercials c     ON c.id = d.commercial_id
		LEFT JOIN materials m       ON m.id = d.commercial_id
		LEFT JOIN material_types mt ON mt.id = m.type_id
		LEFT JOIN campaigns cmp     ON cmp.id = d.campaign_id
		LEFT JOIN clients cli       ON cli.id = cmp.client_id
		WHERE ($1::uuid IS NULL OR d.campaign_id = $1)
		  AND ($2::timestamptz IS NULL OR d.detected_at >= $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at <= $3)
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		  AND ($4::text[] IS NULL OR (
		      SELECT bool_and(
		          unaccent(lower(
		              COALESCE(s.name,'') || ' ' || COALESCE(s.city,'') || ' ' ||
		              COALESCE(s.state,'') || ' ' || COALESCE(s.band,'') || ' ' ||
		              COALESCE(s.frequency_mhz::text,'') || ' ' ||
		              COALESCE(c.title,'') || ' ' || COALESCE(mt.name,'') || ' ' ||
		              COALESCE(cli.name,'')
		          )) LIKE '%' || unaccent(lower(tok)) || '%'
		      )
		      FROM unnest($4::text[]) AS tok
		  ))
		ORDER BY d.detected_at ` + order + `
		LIMIT $5 OFFSET $6`

	offset := (f.Page - 1) * f.PageSize
	rows, err := d.pool.Query(ctx, sql,
		f.CampaignID, f.StartDate, f.EndDate, qPattern, f.PageSize, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		out   []DetectionEnriched
		total int
	)
	for rows.Next() {
		var det DetectionEnriched
		if err := rows.Scan(&det.ID, &det.StationID, &det.StationName, &det.CommercialID, &det.CommercialName,
			&det.CampaignID, &det.DetectedAt, &det.MatchStartOffsetMs, &det.MatchEndOffsetMs,
			&det.Confidence, &det.HashCount, &det.TemporalCoverage, &det.VariantUsed,
			&det.RateUsed, &det.EvidenceStatus, &det.EvidenceKey,
			&det.EvidenceSizeBytes, &det.Category, &det.TypeID, &det.RetractedAt,
			&det.IgnoredAt, &det.IgnoredBy,
			&det.ManualAt, &det.ManualBy, &det.ManualNote, &det.CreatedAt,
			&det.StationFrequencyMHz, &det.StationBand, &det.StationCity, &det.StationState,
			&det.StationLogo, &det.StationPMM,
			&det.MaterialDurationSec, &det.MaterialTypeName, &det.MaterialTypeColor,
			&det.ClientID, &det.ClientName,
			&total); err != nil {
			return nil, err
		}
		out = append(out, det)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	totalPages := (total + f.PageSize - 1) / f.PageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if out == nil {
		out = []DetectionEnriched{}
	}
	return &ListPagedResult{
		Data:       out,
		Page:       f.Page,
		PageSize:   f.PageSize,
		Total:      total,
		TotalPages: totalPages,
	}, nil
}
```

- [ ] **Step 2: Confirmar dependência `unaccent`**

Verificar se a extensão `unaccent` já está habilitada no banco. Rodar:

```bash
docker compose -f infra/docker/docker-compose.yml exec postgres \
  psql -U radiocheck -d radiocheck -c "SELECT extname FROM pg_extension WHERE extname='unaccent';"
```

Esperado: linha com `unaccent`. Se NÃO existir, criar migration nova `migrations/0023_unaccent.up.sql`:

```sql
CREATE EXTENSION IF NOT EXISTS unaccent;
```

E `migrations/0023_unaccent.down.sql`:

```sql
DROP EXTENSION IF EXISTS unaccent;
```

- [ ] **Step 3: Adicionar índice de suporte (se ainda não existir)**

Verificar índice em `detections(campaign_id, detected_at DESC) WHERE ignored_at IS NULL`:

```bash
docker compose -f infra/docker/docker-compose.yml exec postgres \
  psql -U radiocheck -d radiocheck -c "\d detections" | grep -i "idx\|index"
```

Se não houver, criar `migrations/0024_detections_paged_index.up.sql`:

```sql
CREATE INDEX IF NOT EXISTS idx_detections_campaign_detected_at_desc
  ON detections (campaign_id, detected_at DESC)
  WHERE ignored_at IS NULL AND retracted_at IS NULL;
```

E `migrations/0024_detections_paged_index.down.sql`:

```sql
DROP INDEX IF EXISTS idx_detections_campaign_detected_at_desc;
```

- [ ] **Step 4: Escrever teste do `ListPaged`**

Adicionar no fim de `workers/internal/catalog/detections_test.go`:

```go
func TestDetections_ListPaged_BasicPaging(t *testing.T) {
	pool := newTestPool(t) // helper já existente no pacote
	defer pool.Close()
	repo := NewDetections(pool)

	// Seed: 25 detections numa campanha, distribuídas em 25 dias.
	campaign := seedCampaignWithMaterial(t, pool, 25)
	for i := 0; i < 25; i++ {
		insertDetection(t, pool, campaign, time.Now().Add(-time.Duration(i)*time.Hour))
	}

	res, err := repo.ListPaged(context.Background(), ListPagedFilter{
		CampaignID: &campaign.ID, Page: 1, PageSize: 10,
	})
	if err != nil { t.Fatal(err) }
	if len(res.Data) != 10 { t.Errorf("data len = %d, want 10", len(res.Data)) }
	if res.Total != 25 { t.Errorf("total = %d, want 25", res.Total) }
	if res.TotalPages != 3 { t.Errorf("total_pages = %d, want 3", res.TotalPages) }

	res2, _ := repo.ListPaged(context.Background(), ListPagedFilter{
		CampaignID: &campaign.ID, Page: 3, PageSize: 10,
	})
	if len(res2.Data) != 5 { t.Errorf("last page len = %d, want 5", len(res2.Data)) }
}

func TestDetections_ListPaged_QFilter(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	repo := NewDetections(pool)

	campaign := seedCampaignWithMaterialNamed(t, pool, "Cha cha cha 30s")
	insertDetection(t, pool, campaign, time.Now())

	// Match: "cha" deve achar
	res, _ := repo.ListPaged(context.Background(), ListPagedFilter{
		CampaignID: &campaign.ID, Q: "cha", Page: 1, PageSize: 10,
	})
	if res.Total != 1 { t.Errorf("q='cha' total = %d, want 1", res.Total) }

	// No match: "xyz" deve voltar zero
	res2, _ := repo.ListPaged(context.Background(), ListPagedFilter{
		CampaignID: &campaign.ID, Q: "xyz", Page: 1, PageSize: 10,
	})
	if res2.Total != 0 { t.Errorf("q='xyz' total = %d, want 0", res2.Total) }
}

func TestDetections_ListPaged_IgnoredExcluded(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	repo := NewDetections(pool)

	campaign := seedCampaignWithMaterial(t, pool, 1)
	d1 := insertDetection(t, pool, campaign, time.Now())
	insertDetection(t, pool, campaign, time.Now())
	if err := repo.Ignore(context.Background(), d1, uuid.New()); err != nil {
		t.Fatal(err)
	}

	res, _ := repo.ListPaged(context.Background(), ListPagedFilter{
		CampaignID: &campaign.ID, Page: 1, PageSize: 10,
	})
	if res.Total != 1 { t.Errorf("total = %d, want 1 (ignored excluded)", res.Total) }
}
```

> Se os helpers `newTestPool`, `seedCampaignWithMaterial`, `insertDetection` ainda não existirem no `_test.go` do pacote, abrir o arquivo, ver o que já é usado pelos testes existentes (`TestDetections_Create*`), e seguir o mesmo padrão. Não inventar uma nova infraestrutura de teste.

- [ ] **Step 5: Rodar testes — devem falhar com compilação OK + assertions vermelhas**

```bash
cd workers && go test ./internal/catalog/ -run TestDetections_ListPaged -v
```

Esperado: 3 testes, todos passam após Step 1 (já que código está implementado). Se algum falhar, ler erro e corrigir o SQL/parsing antes de seguir.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/catalog/detections_test.go migrations/0023_unaccent.up.sql migrations/0023_unaccent.down.sql migrations/0024_detections_paged_index.up.sql migrations/0024_detections_paged_index.down.sql
git commit -m "$(cat <<'EOF'
feat(catalog): paginated detections list with q-search

Adds Detections.ListPaged returning {data, page, page_size, total,
total_pages} with optional case/accent-insensitive q-search over
station/material/type/client text fields. Excludes ignored + retracted
rows. Includes enrichment columns (station logo/pmm/freq, material
duration/type/color, client name) so the airtime report card needs only
one round-trip. New index supports the common (campaign_id, detected_at
DESC) scan.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Handler `ListPaged` no `/v1/internal/detections`

**Files:**
- Modify: `workers/internal/api/handlers/detections.go`
- Modify: `workers/internal/api/handlers/detections_test.go`

- [ ] **Step 1: Estender o handler `List` existente pra entrar no caminho paginado quando `page` for passado**

No arquivo `workers/internal/api/handlers/detections.go`, **substituir** o método `List` por:

```go
func (h *DetectionsHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// Quando ?page=N é passado, entramos no caminho paginado novo. Sem page,
	// preservamos o comportamento antigo (offset/limit, array dentro de
	// {data: [...]}) que o DayDetailModal já consome.
	if q.Get("page") != "" {
		h.listPaged(w, r)
		return
	}

	f := catalog.ListFilter{}
	if v := q.Get("campaign_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil { http.Error(w, "invalid campaign_id", 400); return }
		f.CampaignID = &id
	}
	if v := q.Get("station_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil { http.Error(w, "invalid station_id", 400); return }
		f.StationID = &id
	}
	if v := q.Get("start_date"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil { http.Error(w, "invalid start_date (use RFC3339)", 400); return }
		f.StartDate = &t
	}
	if v := q.Get("end_date"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil { http.Error(w, "invalid end_date (use RFC3339)", 400); return }
		f.EndDate = &t
	}
	if v := q.Get("limit"); v != "" {
		n, _ := strconv.Atoi(v)
		f.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, _ := strconv.Atoi(v)
		f.Offset = n
	}
	items, err := h.Repo.List(r.Context(), f)
	if err != nil { http.Error(w, "internal error", 500); return }
	writeJSON(w, 200, map[string]any{"data": items})
}

// listPaged é o caminho ativado quando ?page=N é passado. Aceita também
// campaign_id, start_date, end_date (RFC3339), q (search), page_size (1..200,
// default 10), sort ("detected_at_desc" default, "detected_at_asc").
func (h *DetectionsHandler) listPaged(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := catalog.ListPagedFilter{}

	if v := q.Get("campaign_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil { http.Error(w, "invalid campaign_id", 400); return }
		f.CampaignID = &id
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil { http.Error(w, "invalid from (use RFC3339)", 400); return }
		f.StartDate = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil { http.Error(w, "invalid to (use RFC3339)", 400); return }
		f.EndDate = &t
	}
	if v := q.Get("q"); v != "" {
		f.Q = v
	}
	if v := q.Get("sort"); v != "" {
		f.Sort = v
	}
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 { http.Error(w, "invalid page (>=1)", 400); return }
		f.Page = n
	}
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			http.Error(w, "invalid page_size (1..200)", 400); return
		}
		f.PageSize = n
	}

	res, err := h.Repo.ListPaged(r.Context(), f)
	if err != nil { http.Error(w, "internal error", 500); return }
	writeJSON(w, 200, res)
}
```

- [ ] **Step 2: Adicionar testes de validação dos query params novos**

Adicionar no fim de `workers/internal/api/handlers/detections_test.go`:

```go
func TestDetectionsHandler_List_Paged_BadPage(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/detections", h.List)

	req := httptest.NewRequest("GET", "/detections?page=0", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for page=0", rr.Code)
	}
}

func TestDetectionsHandler_List_Paged_BadPageSize(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/detections", h.List)

	req := httptest.NewRequest("GET", "/detections?page=1&page_size=999", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for page_size=999", rr.Code)
	}
}

func TestDetectionsHandler_List_Paged_BadFromDate(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/detections", h.List)

	req := httptest.NewRequest("GET", "/detections?page=1&from=not-rfc3339", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for bad from", rr.Code)
	}
}
```

- [ ] **Step 3: Rodar testes**

```bash
cd workers && go test ./internal/api/handlers/ -run TestDetectionsHandler_List_Paged -v
```

Esperado: 3 testes passam.

- [ ] **Step 4: Smoke test manual no docker compose local**

```bash
docker compose -f infra/docker/docker-compose.yml up -d --build api
# Get a JWT (use bootstrap admin or via login endpoint):
TOKEN="<seu admin JWT>"
curl -s "http://localhost:8080/v1/internal/detections?page=1&page_size=10&campaign_id=<algum_id>" \
  -H "Authorization: Bearer $TOKEN" | jq '.total, .total_pages, (.data | length)'
```

Esperado: 3 números (total numérico, total_pages, e o tamanho do array ≤ 10).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/detections.go workers/internal/api/handlers/detections_test.go
git commit -m "$(cat <<'EOF'
feat(api): paginated /detections gated on ?page= query param

Existing /detections behavior is unchanged when ?page= is absent
(preserves DayDetailModal). When ?page=N is passed, returns
{data, page, page_size, total, total_pages} with optional q-search,
sort, and from/to (RFC3339).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: `AggregateByMaterial` no repository

**Files:**
- Modify: `workers/internal/catalog/detections.go`
- Modify: `workers/internal/catalog/detections_test.go`

- [ ] **Step 1: Adicionar tipo + método no repository**

Adicionar em `workers/internal/catalog/detections.go` logo após `ListPaged`:

```go
type MaterialAggregateRow struct {
	MaterialID          uuid.UUID `json:"material_id"`
	MaterialTitle       string    `json:"material_title"`
	MaterialDurationSec *int32    `json:"material_duration_sec,omitempty"`
	MaterialTypeID      *uuid.UUID `json:"material_type_id,omitempty"`
	MaterialTypeName    *string   `json:"material_type_name,omitempty"`
	MaterialTypeColor   *string   `json:"material_type_color,omitempty"`
	Count               int       `json:"count"`
}

type MaterialAggregateResult struct {
	Data               []MaterialAggregateRow `json:"data"`
	TotalDetections    int                    `json:"total_detections"`
	DistinctMaterials  int                    `json:"distinct_materials"`
}

type AggregateFilter struct {
	CampaignID uuid.UUID
	StartDate  *time.Time
	EndDate    *time.Time
	Q          string
}

// AggregateByMaterial counts non-ignored, non-retracted detections grouped by
// material for the airtime-report sidebar panel. Honours the same q-search as
// ListPaged so the panel stays in sync with the filtered list.
func (d *Detections) AggregateByMaterial(ctx context.Context, f AggregateFilter) (*MaterialAggregateResult, error) {
	var qPattern any = nil
	if q := strings.TrimSpace(f.Q); q != "" {
		toks := strings.Fields(q)
		if len(toks) > 4 { toks = toks[:4] }
		qPattern = toks
	}

	rows, err := d.pool.Query(ctx, `
		SELECT d.commercial_id, COALESCE(c.title, ''),
		       m.duration_sec, m.type_id, mt.name, mt.color,
		       COUNT(*) AS cnt
		FROM detections d
		LEFT JOIN commercials c     ON c.id = d.commercial_id
		LEFT JOIN materials m       ON m.id = d.commercial_id
		LEFT JOIN material_types mt ON mt.id = m.type_id
		LEFT JOIN stations s        ON s.id = d.station_id
		LEFT JOIN campaigns cmp     ON cmp.id = d.campaign_id
		LEFT JOIN clients cli       ON cli.id = cmp.client_id
		WHERE d.campaign_id = $1
		  AND ($2::timestamptz IS NULL OR d.detected_at >= $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at <= $3)
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		  AND ($4::text[] IS NULL OR (
		      SELECT bool_and(
		          unaccent(lower(
		              COALESCE(s.name,'') || ' ' || COALESCE(s.city,'') || ' ' ||
		              COALESCE(s.state,'') || ' ' || COALESCE(s.band,'') || ' ' ||
		              COALESCE(s.frequency_mhz::text,'') || ' ' ||
		              COALESCE(c.title,'') || ' ' || COALESCE(mt.name,'') || ' ' ||
		              COALESCE(cli.name,'')
		          )) LIKE '%' || unaccent(lower(tok)) || '%'
		      )
		      FROM unnest($4::text[]) AS tok
		  ))
		GROUP BY d.commercial_id, c.title, m.duration_sec, m.type_id, mt.name, mt.color
		ORDER BY cnt DESC, c.title ASC`,
		f.CampaignID, f.StartDate, f.EndDate, qPattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		out   []MaterialAggregateRow
		total int
	)
	for rows.Next() {
		var r MaterialAggregateRow
		if err := rows.Scan(&r.MaterialID, &r.MaterialTitle,
			&r.MaterialDurationSec, &r.MaterialTypeID, &r.MaterialTypeName, &r.MaterialTypeColor,
			&r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
		total += r.Count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []MaterialAggregateRow{}
	}
	return &MaterialAggregateResult{
		Data:              out,
		TotalDetections:   total,
		DistinctMaterials: len(out),
	}, nil
}
```

- [ ] **Step 2: Teste do agregado**

Adicionar em `workers/internal/catalog/detections_test.go`:

```go
func TestDetections_AggregateByMaterial(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	repo := NewDetections(pool)

	campaign := seedCampaignWithMaterial(t, pool, 1)
	matA := campaign.MaterialIDs[0] // helper retorna múltiplos materiais
	// Seed: 5 detections de matA + 2 de matB.
	for i := 0; i < 5; i++ { insertDetectionFor(t, pool, campaign, matA, time.Now()) }
	matB := seedExtraMaterial(t, pool, campaign)
	for i := 0; i < 2; i++ { insertDetectionFor(t, pool, campaign, matB, time.Now()) }

	res, err := repo.AggregateByMaterial(context.Background(), AggregateFilter{
		CampaignID: campaign.ID,
	})
	if err != nil { t.Fatal(err) }
	if res.TotalDetections != 7 { t.Errorf("total = %d, want 7", res.TotalDetections) }
	if res.DistinctMaterials != 2 { t.Errorf("distinct = %d, want 2", res.DistinctMaterials) }
	if res.Data[0].Count != 5 { t.Errorf("top row count = %d, want 5", res.Data[0].Count) }
}
```

- [ ] **Step 3: Rodar teste**

```bash
cd workers && go test ./internal/catalog/ -run TestDetections_AggregateByMaterial -v
```

Esperado: passa.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/catalog/detections_test.go
git commit -m "$(cat <<'EOF'
feat(catalog): aggregate detections by material for airtime report

AggregateByMaterial returns {data, total_detections, distinct_materials}
grouped by commercial_id, ordered by count DESC. Honours the same q-search
as ListPaged so the sidebar panel stays consistent with the list.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Handler `/detections/aggregate-by-material` + rota

**Files:**
- Modify: `workers/internal/api/handlers/detections.go`
- Modify: `workers/internal/api/router.go`
- Modify: `workers/internal/api/handlers/detections_test.go`

- [ ] **Step 1: Adicionar handler**

Adicionar em `workers/internal/api/handlers/detections.go` antes de `DailySummary`:

```go
func (h *DetectionsHandler) AggregateByMaterial(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cidStr := q.Get("campaign_id")
	if cidStr == "" {
		http.Error(w, "campaign_id required", http.StatusBadRequest)
		return
	}
	cid, err := uuid.Parse(cidStr)
	if err != nil {
		http.Error(w, "invalid campaign_id", http.StatusBadRequest)
		return
	}
	f := catalog.AggregateFilter{CampaignID: cid}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil { http.Error(w, "invalid from (use RFC3339)", 400); return }
		f.StartDate = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil { http.Error(w, "invalid to (use RFC3339)", 400); return }
		f.EndDate = &t
	}
	if v := q.Get("q"); v != "" {
		f.Q = v
	}
	res, err := h.Repo.AggregateByMaterial(r.Context(), f)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
```

- [ ] **Step 2: Registrar rota**

Em `workers/internal/api/router.go`, dentro de `r.Route("/detections", ...)` (linha ~211), **antes** do bloco admin-only, adicionar:

```go
r.Get("/aggregate-by-material", d.Detections.AggregateByMaterial)
```

Resultado esperado da rota final:
```go
r.Route("/detections", func(r chi.Router) {
    r.Get("/", d.Detections.List)
    r.Get("/aggregate-by-material", d.Detections.AggregateByMaterial)  // <— novo
    r.Get("/{id}", d.Detections.Get)
    r.Get("/{id}/evidence", d.Detections.Evidence)
    r.Get("/{id}/evidence/url", d.Detections.EvidenceURL)
    // admin group ...
})
```

> Importante: registrar **antes** de `/{id}` pra chi não consumir "aggregate-by-material" como id.

- [ ] **Step 3: Teste de validação**

Adicionar em `workers/internal/api/handlers/detections_test.go`:

```go
func TestDetectionsHandler_Aggregate_RequiresCampaignID(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/aggregate-by-material", h.AggregateByMaterial)

	req := httptest.NewRequest("GET", "/aggregate-by-material", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestDetectionsHandler_Aggregate_BadCampaignID(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/aggregate-by-material", h.AggregateByMaterial)

	req := httptest.NewRequest("GET", "/aggregate-by-material?campaign_id=not-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}
```

- [ ] **Step 4: Rodar + smoke local**

```bash
cd workers && go test ./internal/api/handlers/ -run TestDetectionsHandler_Aggregate -v
# Smoke:
curl -s "http://localhost:8080/v1/internal/detections/aggregate-by-material?campaign_id=<id>" \
  -H "Authorization: Bearer $TOKEN" | jq '.total_detections, (.data | length)'
```

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/detections.go workers/internal/api/router.go workers/internal/api/handlers/detections_test.go
git commit -m "$(cat <<'EOF'
feat(api): GET /detections/aggregate-by-material

Feeds the airtime-report sidebar panel. Requires campaign_id, accepts
from/to (RFC3339) and q (same semantics as the paginated list).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Handler `/detections/export` (admin only CSV)

**Files:**
- Modify: `workers/internal/api/handlers/detections.go`
- Modify: `workers/internal/api/router.go`
- Modify: `workers/internal/api/handlers/detections_test.go`

- [ ] **Step 1: Adicionar método streaming no repository**

Em `workers/internal/catalog/detections.go` (após `AggregateByMaterial`):

```go
// IterateForExport streams enriched detections without paging, invoking the
// callback once per row. Stops if cb returns an error. Uses the same WHERE
// clause as ListPaged so filters/q behave identically.
func (d *Detections) IterateForExport(ctx context.Context, f ListPagedFilter,
	cb func(DetectionEnriched) error) error {
	var qPattern any = nil
	if q := strings.TrimSpace(f.Q); q != "" {
		toks := strings.Fields(q)
		if len(toks) > 4 { toks = toks[:4] }
		qPattern = toks
	}
	order := "DESC"
	if f.Sort == "detected_at_asc" { order = "ASC" }

	rows, err := d.pool.Query(ctx, `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), d.commercial_id, COALESCE(c.title, ''),
		       d.campaign_id, d.detected_at,
		       d.match_start_offset_ms, d.match_end_offset_ms, d.confidence, d.hash_count,
		       d.temporal_coverage, d.variant_used, d.rate_used,
		       d.evidence_status, d.evidence_key, d.evidence_size_bytes, d.category,
		       m.type_id, d.retracted_at, d.ignored_at, d.ignored_by,
		       d.manual_at, d.manual_by, d.manual_note, d.created_at,
		       s.frequency_mhz, s.band, s.city, s.state, s.logo, s.pmm,
		       m.duration_sec, mt.name, mt.color,
		       cmp.client_id, cli.name
		FROM detections d
		LEFT JOIN stations s        ON s.id = d.station_id
		LEFT JOIN commercials c     ON c.id = d.commercial_id
		LEFT JOIN materials m       ON m.id = d.commercial_id
		LEFT JOIN material_types mt ON mt.id = m.type_id
		LEFT JOIN campaigns cmp     ON cmp.id = d.campaign_id
		LEFT JOIN clients cli       ON cli.id = cmp.client_id
		WHERE ($1::uuid IS NULL OR d.campaign_id = $1)
		  AND ($2::timestamptz IS NULL OR d.detected_at >= $2)
		  AND ($3::timestamptz IS NULL OR d.detected_at <= $3)
		  AND d.ignored_at IS NULL
		  AND d.retracted_at IS NULL
		  AND ($4::text[] IS NULL OR (
		      SELECT bool_and(
		          unaccent(lower(
		              COALESCE(s.name,'') || ' ' || COALESCE(s.city,'') || ' ' ||
		              COALESCE(s.state,'') || ' ' || COALESCE(s.band,'') || ' ' ||
		              COALESCE(s.frequency_mhz::text,'') || ' ' ||
		              COALESCE(c.title,'') || ' ' || COALESCE(mt.name,'') || ' ' ||
		              COALESCE(cli.name,'')
		          )) LIKE '%' || unaccent(lower(tok)) || '%'
		      )
		      FROM unnest($4::text[]) AS tok
		  ))
		ORDER BY d.detected_at ` + order,
		f.CampaignID, f.StartDate, f.EndDate, qPattern)
	if err != nil { return err }
	defer rows.Close()

	for rows.Next() {
		var det DetectionEnriched
		if err := rows.Scan(&det.ID, &det.StationID, &det.StationName, &det.CommercialID, &det.CommercialName,
			&det.CampaignID, &det.DetectedAt, &det.MatchStartOffsetMs, &det.MatchEndOffsetMs,
			&det.Confidence, &det.HashCount, &det.TemporalCoverage, &det.VariantUsed,
			&det.RateUsed, &det.EvidenceStatus, &det.EvidenceKey,
			&det.EvidenceSizeBytes, &det.Category, &det.TypeID, &det.RetractedAt,
			&det.IgnoredAt, &det.IgnoredBy,
			&det.ManualAt, &det.ManualBy, &det.ManualNote, &det.CreatedAt,
			&det.StationFrequencyMHz, &det.StationBand, &det.StationCity, &det.StationState,
			&det.StationLogo, &det.StationPMM,
			&det.MaterialDurationSec, &det.MaterialTypeName, &det.MaterialTypeColor,
			&det.ClientID, &det.ClientName); err != nil {
			return err
		}
		if err := cb(det); err != nil { return err }
	}
	return rows.Err()
}
```

- [ ] **Step 2: Adicionar handler `Export`**

Em `workers/internal/api/handlers/detections.go`:

```go
// Export writes detections as CSV (semicolon-separated, BOM-prefixed UTF-8 so
// Excel pt-BR opens it correctly). Admin-only — gated at the router. Uses
// IterateForExport so memory stays bounded regardless of total row count.
func (h *DetectionsHandler) Export(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := catalog.ListPagedFilter{}

	if v := q.Get("campaign_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil { http.Error(w, "invalid campaign_id", 400); return }
		f.CampaignID = &id
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil { http.Error(w, "invalid from (use RFC3339)", 400); return }
		f.StartDate = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil { http.Error(w, "invalid to (use RFC3339)", 400); return }
		f.EndDate = &t
	}
	if v := q.Get("q"); v != "" { f.Q = v }
	if v := q.Get("sort"); v != "" { f.Sort = v }

	filename := fmt.Sprintf("veiculacoes_%s.csv", time.Now().Format("20060102_150405"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)

	// BOM pra Excel pt-BR detectar UTF-8.
	w.Write([]byte{0xEF, 0xBB, 0xBF})

	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.Write([]string{
		"Data", "Hora", "Emissora", "Frequência", "Banda", "Cidade", "UF",
		"Material", "Duração (s)", "Tipo", "Cliente", "PMM", "Categoria",
	})

	err := h.Repo.IterateForExport(r.Context(), f, func(d catalog.DetectionEnriched) error {
		loc, _ := time.LoadLocation("America/Sao_Paulo")
		t := d.DetectedAt.In(loc)
		freq := ""
		if d.StationFrequencyMHz != nil {
			freq = strings.ReplaceAll(fmt.Sprintf("%.1f", *d.StationFrequencyMHz), ".", ",")
		}
		band := strOrEmpty(d.StationBand)
		city := strOrEmpty(d.StationCity)
		state := strOrEmpty(d.StationState)
		typeName := strOrEmpty(d.MaterialTypeName)
		client := strOrEmpty(d.ClientName)
		pmm := ""
		if d.StationPMM != nil {
			pmm = strings.ReplaceAll(fmt.Sprintf("%.0f", *d.StationPMM), ".", ",")
		}
		dur := ""
		if d.MaterialDurationSec != nil {
			dur = fmt.Sprintf("%d", *d.MaterialDurationSec)
		}
		return cw.Write([]string{
			t.Format("02/01/2006"),
			t.Format("15:04:05"),
			d.StationName,
			freq,
			band,
			city,
			state,
			d.CommercialName,
			dur,
			typeName,
			client,
			pmm,
			d.Category,
		})
	})

	cw.Flush()
	if err != nil {
		// Stream já começou; só logar — não dá pra mudar status.
		return
	}
}

func strOrEmpty(s *string) string {
	if s == nil { return "" }
	return *s
}
```

E adicionar import no topo do arquivo:

```go
import (
    // ... existentes
    "encoding/csv"
)
```

- [ ] **Step 3: Registrar rota admin-only**

Em `workers/internal/api/router.go`, **dentro** do bloco `r.Group(func(r chi.Router) { r.Use(auth.RequireRole("admin")) ... })` que está dentro de `r.Route("/detections", ...)`, adicionar:

```go
r.Get("/export", d.Detections.Export)
```

- [ ] **Step 4: Teste de validação**

```go
func TestDetectionsHandler_Export_BadCampaignID(t *testing.T) {
	h := &DetectionsHandler{}
	r := chi.NewRouter()
	r.Get("/export", h.Export)

	req := httptest.NewRequest("GET", "/export?campaign_id=not-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}
```

- [ ] **Step 5: Rodar + smoke**

```bash
cd workers && go test ./internal/api/handlers/ -run TestDetectionsHandler_Export -v
# Smoke (admin token):
curl -s "http://localhost:8080/v1/internal/detections/export?campaign_id=<id>" \
  -H "Authorization: Bearer $TOKEN" -o veicula.csv
head -3 veicula.csv  # deve mostrar BOM + cabeçalho ; primeira linha
```

- [ ] **Step 6: Commit**

```bash
git add workers/internal/api/handlers/detections.go workers/internal/api/router.go workers/internal/catalog/detections.go workers/internal/api/handlers/detections_test.go
git commit -m "$(cat <<'EOF'
feat(api): GET /detections/export — admin-only CSV (semicolon, UTF-8 BOM)

Streams the same filtered detection set as the airtime report into a
semicolon-delimited CSV with Excel-pt-BR-friendly UTF-8 BOM. Bounded
memory via IterateForExport. Gated by RequireRole("admin").

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 2 — Frontend hooks

### Task 6: Hooks `useDetectionsPaged`, `useMaterialAggregate`, `exportDetectionsCsv`

**Files:**
- Modify: `frontend/src/api/hooks.js`

- [ ] **Step 1: Adicionar `useDetectionsPaged`**

Adicionar em `frontend/src/api/hooks.js` (logo após `useDetections`):

```js
// Paginated detections list for /reports/airtime. Returns {data, page,
// page_size, total, total_pages}. Separate hook from useDetections so the
// DayDetailModal (which expects the unpaginated array shape) stays untouched.
export function useDetectionsPaged({
  campaignId, from, to, q = '', sort = 'detected_at_desc',
  page = 1, pageSize = 10,
}) {
  return useQuery({
    queryKey: ['detections-paged', campaignId, from, to, q, sort, page, pageSize],
    queryFn: () => api.get('/detections', {
      params: {
        campaign_id: campaignId,
        from, to,
        q: q || undefined,
        sort, page, page_size: pageSize,
      },
    }).then(r => r.data),
    enabled: !!campaignId && !!from && !!to,
    keepPreviousData: true,
  })
}
```

- [ ] **Step 2: Adicionar `useMaterialAggregate`**

```js
// Material aggregate for the airtime-report sidebar panel.
export function useMaterialAggregate({ campaignId, from, to, q = '' }) {
  return useQuery({
    queryKey: ['material-aggregate', campaignId, from, to, q],
    queryFn: () => api.get('/detections/aggregate-by-material', {
      params: {
        campaign_id: campaignId,
        from, to,
        q: q || undefined,
      },
    }).then(r => r.data),
    enabled: !!campaignId && !!from && !!to,
    keepPreviousData: true,
  })
}
```

- [ ] **Step 3: Adicionar `exportDetectionsCsv` (função imperativa, não hook)**

```js
// Triggers a CSV download via the admin-only export endpoint. Returns a
// Promise that resolves on download trigger, rejects on HTTP error. Caller
// is expected to disable the button + show "Gerando…" while pending.
export async function exportDetectionsCsv({ campaignId, from, to, q = '', sort = 'detected_at_desc' }) {
  const resp = await api.get('/detections/export', {
    params: { campaign_id: campaignId, from, to, q: q || undefined, sort },
    responseType: 'blob',
  })
  const url = URL.createObjectURL(resp.data)
  const a = document.createElement('a')
  a.href = url
  // Filename do servidor pode vir no Content-Disposition; fallback aqui.
  a.download = `veiculacoes_${from}_${to}.csv`
  document.body.appendChild(a)
  a.click()
  a.remove()
  // Revoke imediato é seguro porque o navegador já agendou o download.
  URL.revokeObjectURL(url)
}
```

- [ ] **Step 4: Verificar lint**

```bash
cd frontend && npm run lint
```

Esperado: sem erros novos.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/api/hooks.js
git commit -m "$(cat <<'EOF'
feat(api): hooks for airtime report (paginated + aggregate + CSV)

useDetectionsPaged consumes the new ?page= path on /detections.
useMaterialAggregate consumes /detections/aggregate-by-material.
exportDetectionsCsv triggers the admin-only /detections/export download.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 3 — Frontend components & page

### Task 7: Sidebar — entrada "Relatório Data/Hora"

**Files:**
- Modify: `frontend/src/components/Sidebar.jsx`

- [ ] **Step 1: Adicionar `IconClock`**

Em `frontend/src/components/Sidebar.jsx`, após `IconDetections` (linha ~49):

```jsx
function IconAirtimeReport() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="8" cy="8" r="6.25" />
      <path d="M8 4.5V8l2.25 1.5" />
    </svg>
  )
}
```

- [ ] **Step 2: Adicionar entrada na `AdminNav`**

Logo abaixo de `<SidebarLink to="/detections" ...>Veiculações</SidebarLink>` (linha ~114):

```jsx
<SidebarLink to="/reports/airtime" icon={<IconAirtimeReport />} onClose={onClose}>Relatório Data/Hora</SidebarLink>
```

- [ ] **Step 3: Adicionar entrada na `ClientNav`**

Logo abaixo de `<SidebarLink to="/detections" ...>Veiculações</SidebarLink>` (linha ~125):

```jsx
<SidebarLink to="/reports/airtime" icon={<IconAirtimeReport />} onClose={onClose}>Relatório Data/Hora</SidebarLink>
```

- [ ] **Step 4: Lint**

```bash
cd frontend && npm run lint
```

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/Sidebar.jsx
git commit -m "$(cat <<'EOF'
feat(sidebar): "Relatório Data/Hora" entry for admin and client

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: Componente `AirtimePaginator`

**Files:**
- Create: `frontend/src/components/AirtimePaginator.jsx`

- [ ] **Step 1: Implementar paginador numerado**

```jsx
// Numbered paginator with ellipsis. Always shows first/last + 2 neighbours of
// the current page. Buttons use the system's --color-tertiary-500 for the
// active state, gray-200 borders otherwise. Disables prev/next at the bounds.

export default function AirtimePaginator({ page, totalPages, total, pageSize, onChange }) {
  if (totalPages <= 1) {
    return (
      <div className="airtime-paginator">
        <span className="airtime-paginator-info">
          {total} {total === 1 ? 'veiculação' : 'veiculações'}
        </span>
      </div>
    )
  }

  const first = Math.max(1, (page - 1) * pageSize + 1)
  const last  = Math.min(total, page * pageSize)

  // Build the page list: [1, ..., page-1, page, page+1, ..., totalPages]
  const pages = []
  const window = 1
  const set = new Set([1, totalPages, page])
  for (let i = page - window; i <= page + window; i++) {
    if (i >= 1 && i <= totalPages) set.add(i)
  }
  const sorted = Array.from(set).sort((a, b) => a - b)
  for (let i = 0; i < sorted.length; i++) {
    if (i > 0 && sorted[i] > sorted[i - 1] + 1) pages.push('…')
    pages.push(sorted[i])
  }

  return (
    <nav className="airtime-paginator" aria-label="Paginação">
      <span className="airtime-paginator-info">
        Mostrando {first}–{last} de {total} {total === 1 ? 'veiculação' : 'veiculações'}
      </span>
      <div className="airtime-paginator-controls">
        <button
          type="button"
          className="airtime-paginator-btn"
          disabled={page <= 1}
          onClick={() => onChange(page - 1)}
          aria-label="Página anterior"
        >‹</button>
        {pages.map((p, i) =>
          p === '…' ? (
            <span key={`e-${i}`} className="airtime-paginator-ellipsis">…</span>
          ) : (
            <button
              key={p}
              type="button"
              className={'airtime-paginator-btn' + (p === page ? ' active' : '')}
              aria-current={p === page ? 'page' : undefined}
              aria-label={`Página ${p}`}
              onClick={() => onChange(p)}
            >{p}</button>
          )
        )}
        <button
          type="button"
          className="airtime-paginator-btn"
          disabled={page >= totalPages}
          onClick={() => onChange(page + 1)}
          aria-label="Próxima página"
        >›</button>
      </div>
    </nav>
  )
}
```

- [ ] **Step 2: Lint**

```bash
cd frontend && npm run lint
```

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/AirtimePaginator.jsx
git commit -m "$(cat <<'EOF'
feat(airtime): AirtimePaginator with numbered pages + ellipsis

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: Componente `AirtimeDetectionRow`

**Files:**
- Create: `frontend/src/components/AirtimeDetectionRow.jsx`

- [ ] **Step 1: Implementar o card de uma veiculação**

```jsx
import { useState, useRef, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import SmartImage from './SmartImage'
import { getAppSheetImageUrl } from '../utils/imageUtils'
import BadgePill from './BadgePill'
import AudioPlayer from './AudioPlayer'
import api from '../api/client'

const CATEGORY_META = {
  in_slot:  { label: 'Dentro da faixa', variant: 'green' },
  out_slot: { label: 'Fora da faixa',   variant: 'yellow' },
  out_date: { label: 'Fora da data',    variant: 'purple' },
  orphan:   { label: 'Bônus (sem regra)', variant: 'blue' },
}

function fmtDate(iso) {
  const d = new Date(iso)
  return d.toLocaleDateString('pt-BR')
}
function fmtTime(iso) {
  const d = new Date(iso)
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:${String(d.getSeconds()).padStart(2, '0')}`
}
function fmtPMM(n) {
  if (n == null) return null
  return new Intl.NumberFormat('pt-BR', { notation: 'compact', maximumFractionDigits: 1 }).format(n)
}
function fmtCost(n) {
  if (n == null) return null
  return new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' }).format(n)
}

// resolveCost computes the per-detection cost using the campaign pricing
// (one entry per station). For per_insertion mode, returns the unit_value of
// the detection's type. For consolidated mode, returns null + flag so the UI
// can render a "Consolidado" badge instead of a number.
function resolveCost(detection, pricingByStation) {
  const p = pricingByStation?.[detection.station_id]
  if (!p) return { value: null, mode: 'none' }
  if (p.mode === 'consolidated') return { value: null, mode: 'consolidated' }
  if (p.mode === 'per_insertion') {
    const t = (p.per_type ?? []).find(t => t.type_id === detection.type_id)
    return { value: t?.unit_value ?? null, mode: 'per_insertion' }
  }
  return { value: null, mode: 'none' }
}

export default function AirtimeDetectionRow({
  detection,
  pricingByStation,
  isPlaying,
  onPlayRequest,
  onPlayClose,
  highlighted = false,
}) {
  const navigate = useNavigate()
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

  async function handlePlayClick() {
    if (loading) return
    if (isPlaying) {
      onPlayClose()
      return
    }
    setLoading(true)
    try {
      await ensureBlob()
      onPlayRequest(detection.id)
    } catch {
      // Silencioso: usuário clica de novo se quiser tentar.
    } finally {
      setLoading(false)
    }
  }

  const stripe = detection.material_type_color || '#94a3b8'
  const cat = CATEGORY_META[detection.category] ?? { label: detection.category, variant: 'neutral' }
  const cost = resolveCost(detection, pricingByStation)
  const pmm = fmtPMM(detection.station_pmm)
  const noEvidence = detection.evidence_status !== 'available'

  const dial = [
    detection.station_frequency_mhz != null
      ? `${String(detection.station_frequency_mhz).replace('.', ',')} ${detection.station_band ?? ''}`.trim()
      : null,
    [detection.station_city, detection.station_state].filter(Boolean).join('–'),
  ].filter(Boolean).join(' · ')

  return (
    <article
      className={'airtime-row' + (highlighted ? ' airtime-row-highlight' : '') + (isPlaying ? ' airtime-row-playing' : '')}
      aria-labelledby={`airtime-mat-${detection.id}`}
    >
      <div className="airtime-row-stripe" style={{ background: stripe }} aria-hidden />

      <button
        type="button"
        className="airtime-row-play"
        onClick={handlePlayClick}
        disabled={noEvidence}
        title={noEvidence ? 'Sem áudio disponível' : (isPlaying ? 'Pausar' : 'Reproduzir')}
        aria-label={isPlaying ? 'Pausar' : `Reproduzir veiculação de ${fmtTime(detection.detected_at)} na ${detection.station_name}`}
      >
        {loading ? (
          <span className="airtime-row-spinner" aria-hidden />
        ) : isPlaying ? (
          <svg width="14" height="14" viewBox="0 0 14 14" fill="currentColor"><rect x="3" y="2" width="3" height="10" rx="1"/><rect x="8" y="2" width="3" height="10" rx="1"/></svg>
        ) : (
          <svg width="14" height="14" viewBox="0 0 14 14" fill="currentColor"><path d="M3.5 2.5v9l8-4.5z"/></svg>
        )}
      </button>

      <div className="airtime-row-datetime">
        <span className="airtime-row-date">{fmtDate(detection.detected_at)}</span>
        <span className="airtime-row-time">{fmtTime(detection.detected_at)}</span>
      </div>

      <div className="airtime-row-station">
        <SmartImage
          src={getAppSheetImageUrl(detection.station_logo)}
          alt={detection.station_name}
          className="airtime-row-logo"
        />
        <div className="airtime-row-station-text">
          <span className="airtime-row-station-name">{detection.station_name}</span>
          <span className="airtime-row-station-dial">{dial}</span>
        </div>
      </div>

      <div className="airtime-row-metrics">
        <div className="airtime-row-metric">
          <span className="airtime-row-metric-label">PMM</span>
          <span className="airtime-row-metric-value" title={detection.station_pmm != null ? String(Math.round(detection.station_pmm)) : null}>
            {pmm ?? '—'}
          </span>
        </div>
        <div className="airtime-row-metric">
          <span className="airtime-row-metric-label">Custo</span>
          {cost.mode === 'consolidated' ? (
            <span className="airtime-row-cost-badge" title="Plano consolidado — sem custo por inserção">Consolidado</span>
          ) : cost.value != null ? (
            <span className="airtime-row-metric-value airtime-row-cost">{fmtCost(cost.value)}</span>
          ) : (
            <span className="airtime-row-metric-value">—</span>
          )}
        </div>
      </div>

      <button
        type="button"
        className="airtime-row-chevron"
        onClick={() => navigate(`/detections/${detection.id}`)}
        aria-label="Ver detalhes da veiculação"
      >›</button>

      <div className="airtime-row-meta" id={`airtime-mat-${detection.id}`}>
        <span className="airtime-row-dot" style={{ background: stripe }} aria-hidden />
        <span className="airtime-row-material-title">{detection.commercial_name}</span>
        {detection.material_type_name && (
          <>
            <span className="airtime-row-sep">·</span>
            <span className="airtime-row-material-type">{detection.material_type_name}</span>
          </>
        )}
        {detection.client_name && (
          <>
            <span className="airtime-row-sep">·</span>
            <span className="airtime-row-client">{detection.client_name}</span>
          </>
        )}
        <BadgePill variant={cat.variant} className="airtime-row-cat-pill">{cat.label}</BadgePill>
      </div>

      {isPlaying && blobUrl && (
        <div className="airtime-row-player">
          <AudioPlayer src={blobUrl} autoPlay />
        </div>
      )}
    </article>
  )
}
```

- [ ] **Step 2: Lint**

```bash
cd frontend && npm run lint
```

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/AirtimeDetectionRow.jsx
git commit -m "$(cat <<'EOF'
feat(airtime): AirtimeDetectionRow card with inline audio player

Stripe-coloured horizontal card showing datetime, station logo + dial,
material chip, PMM and cost (with "Consolidado" badge for consolidated
pricing). Play button fetches /detections/:id/evidence as a blob and
renders AudioPlayer inline below the card.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: Componente `AirtimeMaterialPanel`

**Files:**
- Create: `frontend/src/components/AirtimeMaterialPanel.jsx`

- [ ] **Step 1: Implementar painel sticky**

```jsx
import { useState } from 'react'

export default function AirtimeMaterialPanel({
  aggregate,            // { data: [{material_id, material_title, material_type_color, count, ...}], total_detections, distinct_materials }
  loading = false,
  highlightedMaterialId = null,
  onHover = () => {},
  onLeave = () => {},
}) {
  const [showAll, setShowAll] = useState(false)

  if (loading) {
    return (
      <aside className="airtime-panel airtime-panel-loading" aria-label="Total de veiculações por material">
        <h3 className="airtime-panel-title">Total por áudio</h3>
        <div className="airtime-panel-divider" />
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className="airtime-panel-row-skeleton">
            <div className="skeleton" style={{ width: '60%', height: 11 }} />
            <div className="skeleton" style={{ width: 30, height: 11 }} />
            <div className="skeleton" style={{ width: '100%', height: 6, marginTop: 6 }} />
          </div>
        ))}
      </aside>
    )
  }

  const items = aggregate?.data ?? []
  const max = items[0]?.count ?? 1
  const visible = showAll ? items : items.slice(0, 8)

  return (
    <aside className="airtime-panel" aria-label="Total de veiculações por material">
      <h3 className="airtime-panel-title">
        Total por áudio
        <span className="airtime-panel-info-icon" title="Considera detecções no período (incluindo fora da faixa/data)">ⓘ</span>
      </h3>
      <div className="airtime-panel-divider" />

      {items.length === 0 ? (
        <div className="airtime-panel-empty">Nenhum material no período.</div>
      ) : (
        <ul className="airtime-panel-list">
          {visible.map(item => {
            const pct = Math.max(2, (item.count / max) * 100) // 2% min for visibility
            const color = item.material_type_color || '#94a3b8'
            const hl = highlightedMaterialId === item.material_id
            return (
              <li
                key={item.material_id}
                className={'airtime-panel-row' + (hl ? ' highlighted' : '')}
                onMouseEnter={() => onHover(item.material_id)}
                onMouseLeave={onLeave}
              >
                <div className="airtime-panel-row-top">
                  <span className="airtime-panel-row-stripe" style={{ background: color }} aria-hidden />
                  <span className="airtime-panel-row-title" title={item.material_title}>
                    {item.material_title}
                  </span>
                  <span className="airtime-panel-row-count">{item.count}</span>
                </div>
                <div className="airtime-panel-row-bar-bg">
                  <div className="airtime-panel-row-bar-fill" style={{ width: `${pct}%`, background: color }} />
                </div>
              </li>
            )
          })}
        </ul>
      )}

      {items.length > 8 && !showAll && (
        <button type="button" className="airtime-panel-more" onClick={() => setShowAll(true)}>
          Ver todos ({items.length})
        </button>
      )}

      <div className="airtime-panel-divider" />
      <div className="airtime-panel-footer">
        <div className="airtime-panel-total-row">
          <span className="airtime-panel-total-label">Total geral</span>
          <span className="airtime-panel-total-value">{aggregate?.total_detections ?? 0}</span>
        </div>
        <div className="airtime-panel-meta">
          Materiais distintos: {aggregate?.distinct_materials ?? 0}
        </div>
      </div>
    </aside>
  )
}
```

- [ ] **Step 2: Lint**

```bash
cd frontend && npm run lint
```

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/AirtimeMaterialPanel.jsx
git commit -m "$(cat <<'EOF'
feat(airtime): AirtimeMaterialPanel — sticky total-by-material sidebar

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: Componente `AirtimeGhostPreview`

**Files:**
- Create: `frontend/src/components/AirtimeGhostPreview.jsx`

- [ ] **Step 1: Implementar ghost preview pra empty states**

```jsx
// Empty-state ghost preview. Renders 3 desaturated airtime-row mockups behind
// a centered CTA card. Caller passes title/description/ctaLabel/onCta to
// customize the prompt. Used for "no campaign" and "no detections" states.

export default function AirtimeGhostPreview({
  title = 'Selecione uma campanha',
  description = 'Escolha uma campanha no filtro acima para ver as veiculações detectadas.',
  ctaLabel = null,
  onCta = null,
  iconPath = (
    <>
      <circle cx="32" cy="32" r="22" stroke="currentColor" strokeWidth="2.5" fill="none" />
      <path d="M32 18v14l9 6" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" fill="none" />
    </>
  ),
}) {
  return (
    <div className="airtime-ghost">
      {[1, 2, 3].map(i => (
        <div key={i} className="airtime-ghost-row">
          <div className="airtime-ghost-row-stripe" />
          <div className="airtime-ghost-row-play" />
          <div className="airtime-ghost-row-datetime">
            <div className="airtime-ghost-bar" style={{ width: 80 }} />
            <div className="airtime-ghost-bar" style={{ width: 56, marginTop: 6 }} />
          </div>
          <div className="airtime-ghost-row-station">
            <div className="airtime-ghost-logo" />
            <div>
              <div className="airtime-ghost-bar" style={{ width: 130 }} />
              <div className="airtime-ghost-bar" style={{ width: 90, marginTop: 6 }} />
            </div>
          </div>
          <div className="airtime-ghost-row-metrics">
            <div className="airtime-ghost-bar" style={{ width: 50, marginBottom: 6 }} />
            <div className="airtime-ghost-bar" style={{ width: 60 }} />
          </div>
        </div>
      ))}

      <div className="airtime-ghost-card">
        <svg className="airtime-ghost-icon" width="64" height="64" viewBox="0 0 64 64" aria-hidden>
          {iconPath}
        </svg>
        <h3 className="airtime-ghost-title">{title}</h3>
        <p className="airtime-ghost-description">{description}</p>
        {ctaLabel && onCta && (
          <button type="button" className="airtime-ghost-cta" onClick={onCta}>{ctaLabel}</button>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Lint + commit**

```bash
cd frontend && npm run lint
git add frontend/src/components/AirtimeGhostPreview.jsx
git commit -m "$(cat <<'EOF'
feat(airtime): AirtimeGhostPreview for empty states

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 12: Componente `AirtimeFiltersBar`

**Files:**
- Create: `frontend/src/components/AirtimeFiltersBar.jsx`

- [ ] **Step 1: Implementar barra de filtros**

```jsx
import { useMemo, useState, useEffect, useRef } from 'react'
import RSelect from './RSelect'
import { useAuth } from '../contexts/AuthContext'

// Helpers
function todayISO() { return new Date().toISOString().slice(0, 10) }
function daysAgoISO(n) { const d = new Date(); d.setDate(d.getDate() - n); return d.toISOString().slice(0, 10) }
function firstOfMonthISO() { const d = new Date(); return new Date(d.getFullYear(), d.getMonth(), 1).toISOString().slice(0, 10) }

function ClientMiniAvatar({ name = '', logo = null, size = 22 }) {
  const [imgError, setImgError] = useState(false)
  if (logo && !imgError) {
    return (
      <img src={logo} alt={name} width={size} height={size}
        style={{ width: size, height: size, borderRadius: 4, objectFit: 'cover',
                 flexShrink: 0, border: '1px solid #e2e8f0', display: 'block' }}
        onError={() => setImgError(true)} />
    )
  }
  const initials = name.trim().split(/\s+/).slice(0, 2).map(w => w[0]).join('').toUpperCase() || '?'
  return (
    <div style={{ width: size, height: size, borderRadius: 4, background: '#fce7f3',
                  color: '#E81E75', fontSize: Math.round(size * 0.42), fontWeight: 700,
                  display: 'flex', alignItems: 'center', justifyContent: 'center',
                  flexShrink: 0, userSelect: 'none' }}>{initials}</div>
  )
}

export default function AirtimeFiltersBar({
  campaigns = [],
  clients = [],
  campaignId,
  from,
  to,
  q,
  onCampaignChange,
  onFromChange,
  onToChange,
  onQChange,
  onExportClick,
  exporting = false,
}) {
  const { isAdmin } = useAuth()
  const clientMap = useMemo(() => {
    const m = new Map()
    clients.forEach(c => m.set(c.id, c))
    return m
  }, [clients])

  const campaignOptions = useMemo(() => campaigns.map(c => {
    const client = clientMap.get(c.client_id) ?? null
    return {
      value: c.id,
      label: c.name,
      clientName: client?.name ?? '',
      clientLogo: client?.logo_url ?? null,
      startDate: c.start_date,
      endDate: c.end_date,
    }
  }), [campaigns, clientMap])

  const selectedCampaignOption = campaignOptions.find(o => o.value === campaignId) ?? null
  const selectedCampaignRaw = campaigns.find(c => c.id === campaignId) ?? null

  function formatCampaignOption(opt, { context }) {
    const size = context === 'value' ? 18 : 22
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
        <ClientMiniAvatar name={opt.clientName} logo={opt.clientLogo} size={size} />
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 5, minWidth: 0, overflow: 'hidden' }}>
          {opt.clientName && (
            <span style={{ fontWeight: 600, color: '#06055B', whiteSpace: 'nowrap', fontSize: 13 }}>
              {opt.clientName}
            </span>
          )}
          {opt.clientName && <span style={{ color: '#cbd5e1', fontSize: 11, flexShrink: 0 }}>|</span>}
          <span style={{ color: '#4b5563', fontSize: 13, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
            {opt.label}
          </span>
        </div>
      </div>
    )
  }

  // Preset detection
  const activePreset = useMemo(() => {
    if (from === daysAgoISO(7) && to === todayISO()) return 'last7'
    if (from === firstOfMonthISO() && to === todayISO()) return 'thisMonth'
    if (selectedCampaignRaw && from === selectedCampaignRaw.start_date) {
      const tEnd = selectedCampaignRaw.end_date && selectedCampaignRaw.end_date < todayISO()
        ? selectedCampaignRaw.end_date
        : todayISO()
      if (to === tEnd) return 'fullCampaign'
    }
    return 'custom'
  }, [from, to, selectedCampaignRaw])

  function applyPreset(p) {
    if (p === 'last7') {
      onFromChange(daysAgoISO(7))
      onToChange(todayISO())
    } else if (p === 'thisMonth') {
      onFromChange(firstOfMonthISO())
      onToChange(todayISO())
    } else if (p === 'fullCampaign' && selectedCampaignRaw) {
      onFromChange(selectedCampaignRaw.start_date)
      const tEnd = selectedCampaignRaw.end_date && selectedCampaignRaw.end_date < todayISO()
        ? selectedCampaignRaw.end_date
        : todayISO()
      onToChange(tEnd)
    }
  }

  // Debounced q
  const [localQ, setLocalQ] = useState(q ?? '')
  const debounceRef = useRef(null)
  useEffect(() => { setLocalQ(q ?? '') }, [q])
  function handleQ(e) {
    const v = e.target.value
    setLocalQ(v)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => onQChange(v), 300)
  }

  const invalidRange = from && to && from > to

  return (
    <div className="airtime-filters">
      <div className="airtime-filters-row">
        <div className="airtime-filters-campaign">
          <label htmlFor="airtime-campaign-select">Campanha</label>
          <RSelect
            inputId="airtime-campaign-select"
            options={campaignOptions}
            value={selectedCampaignOption}
            onChange={opt => onCampaignChange(opt?.value ?? '')}
            formatOptionLabel={formatCampaignOption}
            placeholder="Selecione uma campanha"
            isClearable
          />
        </div>

        <div className="airtime-filters-dates">
          <div className="airtime-filters-date">
            <label htmlFor="airtime-from">De</label>
            <input
              id="airtime-from"
              type="date"
              value={from ?? ''}
              onChange={e => onFromChange(e.target.value)}
              className={'input' + (invalidRange ? ' input-error' : '')}
            />
          </div>
          <div className="airtime-filters-date">
            <label htmlFor="airtime-to">Até</label>
            <input
              id="airtime-to"
              type="date"
              value={to ?? ''}
              onChange={e => onToChange(e.target.value)}
              className={'input' + (invalidRange ? ' input-error' : '')}
            />
          </div>
        </div>

        <div className="airtime-filters-search">
          <span className="airtime-filters-search-icon">
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75">
              <circle cx="7" cy="7" r="5" /><path d="M11 11l3 3" strokeLinecap="round" />
            </svg>
          </span>
          <input
            type="text"
            placeholder="Buscar emissora ou material"
            value={localQ}
            onChange={handleQ}
            className="input airtime-filters-search-input"
          />
        </div>

        {isAdmin && (
          <button
            type="button"
            className="airtime-filters-export"
            onClick={onExportClick}
            disabled={!campaignId || invalidRange || exporting}
            title={!campaignId ? 'Selecione uma campanha' : 'Exportar CSV'}
          >
            {exporting ? 'Gerando…' : '↓ Exportar CSV'}
          </button>
        )}
      </div>

      <div className="airtime-filters-presets">
        {[
          { id: 'last7',        label: 'Últimos 7 dias' },
          { id: 'thisMonth',    label: 'Este mês' },
          { id: 'fullCampaign', label: 'Campanha inteira', disabled: !selectedCampaignRaw },
          { id: 'custom',       label: 'Personalizado', readonly: true },
        ].map(p => (
          <button
            key={p.id}
            type="button"
            className={'airtime-preset-chip' + (activePreset === p.id ? ' active' : '')}
            disabled={p.disabled || p.readonly}
            onClick={() => !p.readonly && applyPreset(p.id)}
          >{p.label}</button>
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Lint + commit**

```bash
cd frontend && npm run lint
git add frontend/src/components/AirtimeFiltersBar.jsx
git commit -m "$(cat <<'EOF'
feat(airtime): AirtimeFiltersBar (campaign + range + presets + search + export)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 13: Página `AirtimeReportPage` (orquestrador)

**Files:**
- Create: `frontend/src/pages/AirtimeReportPage.jsx`

- [ ] **Step 1: Implementar a página**

```jsx
import { useEffect, useMemo, useState } from 'react'
import { useSearchParams, useNavigate } from 'react-router-dom'
import {
  useCampaigns, useClients, useDetectionsPaged, useMaterialAggregate, useCampaignPricing,
  exportDetectionsCsv,
} from '../api/hooks'
import AirtimeFiltersBar from '../components/AirtimeFiltersBar'
import AirtimeDetectionRow from '../components/AirtimeDetectionRow'
import AirtimeMaterialPanel from '../components/AirtimeMaterialPanel'
import AirtimePaginator from '../components/AirtimePaginator'
import AirtimeGhostPreview from '../components/AirtimeGhostPreview'
import './AirtimeReportPage.css'

function todayISO() { return new Date().toISOString().slice(0, 10) }
function daysAgoISO(n) { const d = new Date(); d.setDate(d.getDate() - n); return d.toISOString().slice(0, 10) }
function isoToRFC3339Start(iso) { return new Date(`${iso}T00:00:00.000-03:00`).toISOString() }
function isoToRFC3339End(iso) { return new Date(`${iso}T23:59:59.999-03:00`).toISOString() }

export default function AirtimeReportPage() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const { data: campaigns = [] } = useCampaigns()
  const { data: clients = [] } = useClients()

  // URL is source of truth
  const campaignId = searchParams.get('campaign_id') ?? ''
  const from = searchParams.get('from') ?? daysAgoISO(7)
  const to   = searchParams.get('to')   ?? todayISO()
  const q    = searchParams.get('q')    ?? ''
  const page = parseInt(searchParams.get('page') ?? '1', 10) || 1

  function setParam(key, value) {
    const next = new URLSearchParams(searchParams)
    if (value === '' || value == null) next.delete(key)
    else next.set(key, value)
    setSearchParams(next, { replace: true })
  }

  function setFilters(patch) {
    const next = new URLSearchParams(searchParams)
    Object.entries(patch).forEach(([k, v]) => {
      if (v === '' || v == null) next.delete(k)
      else next.set(k, v)
    })
    setSearchParams(next, { replace: true })
  }

  // Initial URL hydration: when entering with no from/to but a campaign id,
  // default to last 7 days. Setting params with replace avoids extra history.
  useEffect(() => {
    if (!searchParams.get('from') || !searchParams.get('to')) {
      setFilters({ from: daysAgoISO(7), to: todayISO() })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const invalidRange = from && to && from > to

  const fromRFC = useMemo(() => (from && !invalidRange ? isoToRFC3339Start(from) : null), [from, invalidRange])
  const toRFC   = useMemo(() => (to && !invalidRange ? isoToRFC3339End(to)     : null), [to, invalidRange])

  const { data: detResp, isLoading: loadingList, isFetching } = useDetectionsPaged({
    campaignId: campaignId || null,
    from: fromRFC,
    to: toRFC,
    q,
    page,
    pageSize: 10,
  })
  const { data: aggResp, isLoading: loadingAgg } = useMaterialAggregate({
    campaignId: campaignId || null,
    from: fromRFC,
    to: toRFC,
    q,
  })
  const { data: pricingList = [] } = useCampaignPricing(campaignId || null)
  const pricingByStation = useMemo(() => {
    const m = {}
    for (const p of pricingList) m[p.station_id] = p
    return m
  }, [pricingList])

  const detections = detResp?.data ?? []
  const total = detResp?.total ?? 0
  const totalPages = detResp?.total_pages ?? 1

  // Active player (one at a time)
  const [activePlayerId, setActivePlayerId] = useState(null)
  function handlePlayRequest(id) { setActivePlayerId(id) }
  function handlePlayClose() { setActivePlayerId(null) }

  // Cross-highlight from panel hover
  const [highlightedMaterialId, setHighlightedMaterialId] = useState(null)

  // Export
  const [exporting, setExporting] = useState(false)
  async function handleExport() {
    if (!campaignId || invalidRange) return
    setExporting(true)
    try {
      await exportDetectionsCsv({ campaignId, from: fromRFC, to: toRFC, q })
    } catch {
      window.alert('Não foi possível gerar o CSV. Tente novamente.')
    } finally {
      setExporting(false)
    }
  }

  // Reset page when filters change
  function handleCampaignChange(id) {
    setFilters({ campaign_id: id || '', page: '1' })
    setActivePlayerId(null)
  }
  function handleFromChange(v) { setFilters({ from: v, page: '1' }); setActivePlayerId(null) }
  function handleToChange(v)   { setFilters({ to: v, page: '1' }); setActivePlayerId(null) }
  function handleQChange(v)    { setFilters({ q: v || '', page: '1' }); setActivePlayerId(null) }
  function handlePageChange(p) {
    setParam('page', String(p))
    setActivePlayerId(null)
    // Scroll list container to top
    const list = document.querySelector('.airtime-list')
    if (list) list.scrollIntoView({ block: 'start', behavior: 'smooth' })
  }

  const showList = !!campaignId && !invalidRange
  const isLoadingData = showList && (loadingList || isFetching)

  return (
    <div className="airtime-page">
      <div className="page-header">
        <h2>Relatório Data e Hora</h2>
      </div>

      <AirtimeFiltersBar
        campaigns={campaigns}
        clients={clients}
        campaignId={campaignId}
        from={from}
        to={to}
        q={q}
        onCampaignChange={handleCampaignChange}
        onFromChange={handleFromChange}
        onToChange={handleToChange}
        onQChange={handleQChange}
        onExportClick={handleExport}
        exporting={exporting}
      />

      {invalidRange && (
        <div className="airtime-error-card">
          <p>Intervalo inválido. A data inicial precisa ser anterior ou igual à final.</p>
          <button
            type="button"
            className="airtime-error-cta"
            onClick={() => setFilters({ from: daysAgoISO(7), to: todayISO() })}
          >Resetar para últimos 7 dias</button>
        </div>
      )}

      {!showList ? (
        <AirtimeGhostPreview
          title="Selecione uma campanha"
          description="Escolha uma campanha no filtro acima para ver as veiculações detectadas."
        />
      ) : (
        <div className="airtime-split">
          <div className="airtime-list">
            {isLoadingData ? (
              <SkeletonList />
            ) : detections.length === 0 ? (
              <AirtimeGhostPreview
                title="Nenhuma veiculação no período"
                description={`Não encontramos veiculações entre ${from.split('-').reverse().join('/')} e ${to.split('-').reverse().join('/')}.`}
                ctaLabel="Ampliar para últimos 30 dias"
                onCta={() => setFilters({ from: daysAgoISO(30), to: todayISO(), page: '1' })}
              />
            ) : (
              <>
                {detections.map(d => (
                  <AirtimeDetectionRow
                    key={d.id}
                    detection={d}
                    pricingByStation={pricingByStation}
                    isPlaying={activePlayerId === d.id}
                    onPlayRequest={handlePlayRequest}
                    onPlayClose={handlePlayClose}
                    highlighted={highlightedMaterialId === d.commercial_id}
                  />
                ))}
                <AirtimePaginator
                  page={page}
                  totalPages={totalPages}
                  total={total}
                  pageSize={10}
                  onChange={handlePageChange}
                />
              </>
            )}
          </div>

          <div className="airtime-panel-wrap">
            <AirtimeMaterialPanel
              aggregate={aggResp}
              loading={loadingAgg}
              highlightedMaterialId={highlightedMaterialId}
              onHover={setHighlightedMaterialId}
              onLeave={() => setHighlightedMaterialId(null)}
            />
          </div>
        </div>
      )}
    </div>
  )
}

function SkeletonList() {
  return (
    <>
      {Array.from({ length: 10 }).map((_, i) => (
        <div key={i} className="airtime-row airtime-row-skeleton">
          <div className="airtime-row-stripe" />
          <div className="airtime-row-play airtime-skeleton-circle" />
          <div className="airtime-row-datetime">
            <div className="skeleton" style={{ width: 80, height: 11 }} />
            <div className="skeleton" style={{ width: 56, height: 9, marginTop: 6 }} />
          </div>
          <div className="airtime-row-station">
            <div className="airtime-skeleton-circle airtime-row-logo" />
            <div className="airtime-row-station-text">
              <div className="skeleton" style={{ width: 130, height: 11 }} />
              <div className="skeleton" style={{ width: 90, height: 9, marginTop: 6 }} />
            </div>
          </div>
          <div className="airtime-row-metrics">
            <div className="skeleton" style={{ width: 50, height: 9 }} />
            <div className="skeleton" style={{ width: 60, height: 11, marginTop: 4 }} />
          </div>
          <div />
          <div className="airtime-row-meta">
            <div className="skeleton" style={{ width: '60%', height: 11 }} />
          </div>
        </div>
      ))}
    </>
  )
}
```

- [ ] **Step 2: Lint + commit**

```bash
cd frontend && npm run lint
git add frontend/src/pages/AirtimeReportPage.jsx
git commit -m "$(cat <<'EOF'
feat(airtime): AirtimeReportPage orchestrator

URL-driven state (campaign_id, from, to, q, page). Composes
AirtimeFiltersBar + AirtimeDetectionRow list + AirtimeMaterialPanel
+ AirtimePaginator + AirtimeGhostPreview. Handles play exclusivity
(one player at a time), CSV export, cross-highlight, and page reset
when filters change.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 14: CSS `AirtimeReportPage.css`

**Files:**
- Create: `frontend/src/pages/AirtimeReportPage.css`

- [ ] **Step 1: Escrever folha de estilos**

```css
/* ===== AirtimeReportPage ============================================== */
.airtime-page { padding: 0; }

/* --- Filters bar ----------------------------------------------------- */
.airtime-filters {
  position: sticky; top: 0; z-index: 5;
  background: #fff;
  border: 1px solid var(--c-border, #e2e8f0);
  border-radius: 12px;
  padding: 14px 16px;
  margin-bottom: 16px;
  box-shadow: 0 1px 2px rgba(0,0,0,0.03);
}
.airtime-filters-row {
  display: grid;
  grid-template-columns: minmax(220px, 1fr) auto minmax(180px, 220px) auto;
  gap: 14px;
  align-items: end;
}
.airtime-filters-campaign label,
.airtime-filters-date label,
.airtime-filters-search label {
  display: block;
  font-size: 11px;
  color: #64748b;
  font-weight: 600;
  margin-bottom: 4px;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
.airtime-filters-dates {
  display: flex; gap: 10px;
}
.airtime-filters-date input { min-width: 130px; }
.airtime-filters-search {
  position: relative;
  display: flex; align-items: end;
}
.airtime-filters-search-icon {
  position: absolute; left: 10px; bottom: 10px;
  color: #94a3b8; pointer-events: none;
}
.airtime-filters-search-input {
  padding-left: 30px;
  width: 100%;
}
.airtime-filters-export {
  height: 40px;
  padding: 0 16px;
  border: 1px solid #e2e8f0;
  background: #fff;
  color: #06055B;
  font-weight: 600;
  font-size: 13px;
  border-radius: 8px;
  cursor: pointer;
  transition: all 150ms ease;
  white-space: nowrap;
}
.airtime-filters-export:hover:not(:disabled) {
  border-color: var(--color-tertiary-300, #F472B6);
  color: var(--color-tertiary-600, #C4185E);
  transform: translateY(-1px);
}
.airtime-filters-export:disabled { opacity: 0.5; cursor: not-allowed; }

.airtime-filters-presets {
  display: flex; flex-wrap: wrap; gap: 8px;
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid #f1f5f9;
}
.airtime-preset-chip {
  height: 32px;
  padding: 0 14px;
  border-radius: 9999px;
  border: 1px solid transparent;
  background: #f1f5f9;
  color: #64748b;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: all 150ms ease;
}
.airtime-preset-chip:hover:not(:disabled) {
  background: #fce7f3;
  color: var(--color-tertiary-700, #9d174d);
}
.airtime-preset-chip.active {
  background: #fce7f3;
  border-color: var(--color-tertiary-300, #F472B6);
  color: var(--color-tertiary-700, #9d174d);
}
.airtime-preset-chip:disabled { opacity: 0.4; cursor: not-allowed; }

.input-error {
  border-color: #ef4444 !important;
  box-shadow: 0 0 0 3px rgba(239,68,68,0.1);
}

/* --- Split layout --------------------------------------------------- */
.airtime-split {
  display: grid;
  grid-template-columns: 1fr 360px;
  gap: 24px;
  align-items: start;
}
@media (max-width: 1279px) {
  .airtime-split { grid-template-columns: 1fr 320px; gap: 18px; }
}
@media (max-width: 1023px) {
  .airtime-split { grid-template-columns: 1fr; }
  .airtime-panel-wrap { order: -1; }
}

/* --- List + cards --------------------------------------------------- */
.airtime-list { min-width: 0; }

.airtime-row {
  display: grid;
  grid-template-columns: 4px 56px 1fr 260px 120px 28px;
  grid-template-rows: auto auto auto;
  gap: 0 12px;
  align-items: center;
  padding: 14px 16px;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  box-shadow: 0 1px 2px rgba(0,0,0,0.04);
  margin-bottom: 10px;
  transition: transform 200ms cubic-bezier(0.16,1,0.3,1),
              box-shadow 200ms ease, border-color 200ms ease;
  position: relative;
  overflow: hidden;
}
.airtime-row:hover {
  transform: translateY(-2px);
  border-color: var(--color-tertiary-300, #F472B6);
  box-shadow: 0 4px 6px -1px rgba(0,0,0,0.07), 0 2px 4px -2px rgba(0,0,0,0.05);
}
.airtime-row-highlight {
  border-color: var(--color-tertiary-300, #F472B6);
  box-shadow: 0 0 0 1px var(--color-tertiary-300, #F472B6);
}
.airtime-row-playing { border-color: var(--color-tertiary-500, #E81E75); }

.airtime-row-stripe {
  grid-row: 1 / span 3;
  width: 4px; height: 100%;
  position: absolute; left: 0; top: 0;
}

.airtime-row-play {
  width: 40px; height: 40px;
  border-radius: 50%;
  border: none;
  background: var(--color-tertiary-500, #E81E75);
  color: #fff;
  display: flex; align-items: center; justify-content: center;
  cursor: pointer;
  transition: all 150ms ease;
  flex-shrink: 0;
}
.airtime-row-play:hover:not(:disabled) {
  background: var(--color-tertiary-600, #C4185E);
  transform: scale(1.05);
}
.airtime-row-play:disabled { background: #cbd5e1; cursor: not-allowed; }
.airtime-row-spinner {
  width: 14px; height: 14px;
  border: 2px solid rgba(255,255,255,0.4);
  border-top-color: #fff;
  border-radius: 50%;
  animation: airtime-spin 600ms linear infinite;
}
@keyframes airtime-spin { to { transform: rotate(360deg); } }

.airtime-row-datetime { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.airtime-row-date { color: #334155; font-weight: 600; font-size: 13px; }
.airtime-row-time { color: #64748b; font-size: 12px; }

.airtime-row-station {
  display: flex; align-items: center; gap: 10px;
  min-width: 0;
}
.airtime-row-logo {
  width: 40px; height: 40px;
  border-radius: 6px;
  overflow: hidden;
  flex-shrink: 0;
}
.airtime-row-station-text { display: flex; flex-direction: column; gap: 2px; min-width: 0; overflow: hidden; }
.airtime-row-station-name {
  color: #06055B; font-weight: 600; font-size: 13px;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.airtime-row-station-dial {
  color: #64748b; font-size: 11px;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}

.airtime-row-metrics {
  display: flex; flex-direction: column; gap: 6px;
  text-align: right;
}
.airtime-row-metric { display: flex; flex-direction: column; gap: 1px; }
.airtime-row-metric-label {
  color: #64748b; font-size: 10px; text-transform: uppercase; letter-spacing: 0.05em;
  font-weight: 600;
}
.airtime-row-metric-value { color: #06055B; font-weight: 600; font-size: 13px; }
.airtime-row-cost { color: var(--color-tertiary-600, #C4185E); font-weight: 700; }
.airtime-row-cost-badge {
  display: inline-block;
  padding: 2px 8px;
  background: #f1f5f9;
  color: #64748b;
  font-size: 10px;
  font-weight: 600;
  border-radius: 9999px;
  text-transform: uppercase;
  letter-spacing: 0.03em;
}

.airtime-row-chevron {
  width: 28px; height: 28px;
  border: none;
  background: transparent;
  color: #cbd5e1;
  font-size: 22px;
  cursor: pointer;
  transition: color 150ms ease;
}
.airtime-row-chevron:hover { color: #334155; }

.airtime-row-meta {
  grid-column: 2 / -1;
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px solid #f1f5f9;
  display: flex; align-items: center; gap: 8px;
  font-size: 12px;
  flex-wrap: wrap;
}
.airtime-row-dot {
  width: 8px; height: 8px; border-radius: 50%;
  flex-shrink: 0;
}
.airtime-row-material-title { color: #06055B; font-weight: 600; font-size: 13px; }
.airtime-row-material-type, .airtime-row-client { color: #64748b; }
.airtime-row-sep { color: #cbd5e1; }
.airtime-row-cat-pill { margin-left: auto; }

.airtime-row-player {
  grid-column: 1 / -1;
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid #f1f5f9;
}

/* --- Skeleton card -------------------------------------------------- */
.airtime-row-skeleton {
  pointer-events: none;
}
.airtime-skeleton-circle {
  background: #f1f5f9;
  border-radius: 50%;
  animation: airtime-shimmer 1.4s linear infinite;
}
.skeleton {
  background: linear-gradient(90deg, #f1f5f9 0%, #e2e8f0 50%, #f1f5f9 100%);
  background-size: 200% 100%;
  animation: airtime-shimmer 1.4s linear infinite;
  border-radius: 4px;
}
@keyframes airtime-shimmer {
  0%   { background-position: 200% 0; }
  100% { background-position: -200% 0; }
}

/* --- Paginator ------------------------------------------------------ */
.airtime-paginator {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 18px;
  padding: 12px 4px;
  flex-wrap: wrap;
  gap: 12px;
}
.airtime-paginator-info { color: #64748b; font-size: 13px; }
.airtime-paginator-controls { display: flex; gap: 4px; align-items: center; }
.airtime-paginator-btn {
  min-width: 32px; height: 32px;
  padding: 0 10px;
  border: 1px solid #e2e8f0;
  background: #fff;
  color: #334155;
  font-size: 13px; font-weight: 600;
  border-radius: 6px;
  cursor: pointer;
  transition: all 150ms ease;
}
.airtime-paginator-btn:hover:not(:disabled) {
  border-color: var(--color-tertiary-300, #F472B6);
  color: var(--color-tertiary-600, #C4185E);
}
.airtime-paginator-btn.active {
  background: var(--color-tertiary-500, #E81E75);
  border-color: var(--color-tertiary-500, #E81E75);
  color: #fff;
}
.airtime-paginator-btn:disabled { opacity: 0.4; cursor: not-allowed; }
.airtime-paginator-ellipsis { color: #94a3b8; padding: 0 4px; }

/* --- Material panel ------------------------------------------------- */
.airtime-panel-wrap { position: sticky; top: 140px; }
.airtime-panel {
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 16px;
  padding: 18px;
  box-shadow: 0 1px 2px rgba(0,0,0,0.04);
}
.airtime-panel-title {
  margin: 0 0 4px;
  font-size: 14px;
  font-weight: 700;
  color: #06055B;
  display: flex; align-items: center; justify-content: space-between;
}
.airtime-panel-info-icon { color: #94a3b8; cursor: help; font-weight: 400; }
.airtime-panel-divider { border-top: 1px solid #f1f5f9; margin: 12px 0; }
.airtime-panel-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 14px; }
.airtime-panel-row { cursor: default; transition: opacity 150ms ease; }
.airtime-panel-row.highlighted { opacity: 1; }
.airtime-panel-list:hover .airtime-panel-row:not(.highlighted) { opacity: 0.55; }

.airtime-panel-row-top { display: flex; align-items: center; gap: 8px; }
.airtime-panel-row-stripe {
  width: 3px; height: 14px; border-radius: 2px; flex-shrink: 0;
}
.airtime-panel-row-title {
  flex: 1;
  color: #06055B; font-weight: 600; font-size: 12px;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.airtime-panel-row-count {
  color: #06055B; font-weight: 700; font-size: 18px;
  flex-shrink: 0;
}
.airtime-panel-row-bar-bg {
  margin-top: 6px;
  height: 6px;
  background: #f1f5f9;
  border-radius: 3px;
  overflow: hidden;
}
.airtime-panel-row-bar-fill {
  height: 100%;
  border-radius: 3px;
  transition: width 400ms cubic-bezier(0.16,1,0.3,1);
}

.airtime-panel-row-skeleton { display: flex; flex-direction: column; }
.airtime-panel-empty { color: #64748b; font-size: 12px; text-align: center; padding: 16px 0; }
.airtime-panel-more {
  display: block; margin: 12px auto 0;
  background: none; border: none;
  color: var(--color-tertiary-600, #C4185E);
  font-size: 12px; font-weight: 600; cursor: pointer;
}
.airtime-panel-footer {}
.airtime-panel-total-row {
  display: flex; justify-content: space-between; align-items: baseline;
}
.airtime-panel-total-label { color: #334155; font-weight: 600; font-size: 13px; }
.airtime-panel-total-value { color: #06055B; font-weight: 700; font-size: 20px; }
.airtime-panel-meta { color: #64748b; font-size: 11px; margin-top: 4px; }

/* --- Ghost preview ------------------------------------------------- */
.airtime-ghost {
  position: relative;
  padding: 40px 24px;
  min-height: 480px;
}
.airtime-ghost-row {
  display: grid;
  grid-template-columns: 4px 40px 100px 1fr 100px;
  gap: 12px;
  align-items: center;
  padding: 14px 16px;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  margin-bottom: 10px;
  opacity: 0.18;
  filter: blur(0.5px);
}
.airtime-ghost-row-stripe { width: 4px; height: 60px; background: #cbd5e1; border-radius: 2px; }
.airtime-ghost-row-play { width: 40px; height: 40px; background: #e2e8f0; border-radius: 50%; }
.airtime-ghost-row-datetime { display: flex; flex-direction: column; }
.airtime-ghost-row-station { display: flex; gap: 10px; align-items: center; }
.airtime-ghost-logo { width: 40px; height: 40px; background: #e2e8f0; border-radius: 6px; }
.airtime-ghost-bar { height: 11px; background: #e2e8f0; border-radius: 4px; }
.airtime-ghost-row-metrics { display: flex; flex-direction: column; text-align: right; }

.airtime-ghost-card {
  position: absolute;
  top: 50%; left: 50%;
  transform: translate(-50%, -50%);
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 16px;
  box-shadow: 0 20px 25px -5px rgba(0,0,0,0.08), 0 8px 10px -6px rgba(0,0,0,0.04);
  padding: 32px;
  text-align: center;
  max-width: 380px;
  width: calc(100% - 48px);
}
.airtime-ghost-icon { color: #cbd5e1; margin-bottom: 12px; }
.airtime-ghost-title { margin: 0 0 8px; font-size: 18px; font-weight: 700; color: #06055B; }
.airtime-ghost-description { margin: 0 0 16px; font-size: 14px; color: #64748b; line-height: 1.5; }
.airtime-ghost-cta {
  background: var(--color-tertiary-500, #E81E75);
  color: #fff;
  border: none;
  height: 40px;
  padding: 0 18px;
  border-radius: 8px;
  font-weight: 600;
  font-size: 13px;
  cursor: pointer;
  transition: all 150ms ease;
}
.airtime-ghost-cta:hover {
  background: var(--color-tertiary-600, #C4185E);
  transform: translateY(-1px);
}

/* --- Error card --------------------------------------------------- */
.airtime-error-card {
  background: #fef2f2;
  border: 1px solid #fecaca;
  border-radius: 12px;
  padding: 18px;
  display: flex; gap: 14px; align-items: center; justify-content: space-between;
  margin-bottom: 16px;
}
.airtime-error-card p { margin: 0; color: #991b1b; font-size: 14px; }
.airtime-error-cta {
  background: #fff; border: 1px solid #fecaca; color: #991b1b;
  height: 36px; padding: 0 14px; border-radius: 8px;
  font-weight: 600; font-size: 12px; cursor: pointer;
}

/* --- Mobile reshape ------------------------------------------------ */
@media (max-width: 767px) {
  .airtime-filters-row {
    grid-template-columns: 1fr;
    gap: 10px;
  }
  .airtime-filters-dates { flex-direction: row; }
  .airtime-row {
    grid-template-columns: 4px 44px 1fr auto;
    grid-template-rows: auto auto auto auto;
    padding: 12px 14px;
  }
  .airtime-row-datetime { grid-column: 3; grid-row: 1; }
  .airtime-row-station { grid-column: 3; grid-row: 2; margin-top: 6px; }
  .airtime-row-metrics { grid-column: 1 / -1; grid-row: 3; flex-direction: row; justify-content: flex-start; gap: 16px; text-align: left; margin-top: 10px; padding-top: 10px; border-top: 1px solid #f1f5f9; }
  .airtime-row-chevron { grid-column: 4; grid-row: 1; }
  .airtime-row-meta { grid-row: 4; }
  .airtime-row-play { grid-column: 2; grid-row: 1 / span 2; }
}
```

- [ ] **Step 2: Build pra confirmar compilação CSS**

```bash
cd frontend && npm run build
```

Esperado: build bem-sucedido.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/AirtimeReportPage.css
git commit -m "$(cat <<'EOF'
feat(airtime): page styles — split layout, cards, panel, paginator, ghost

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 15: Registrar rota em `App.jsx`

**Files:**
- Modify: `frontend/src/App.jsx`

- [ ] **Step 1: Adicionar import + rota**

Em `frontend/src/App.jsx`, no bloco de imports adicionar:

```jsx
import AirtimeReportPage from './pages/AirtimeReportPage'
```

E dentro do `<Routes>` (linha ~80), antes da catch-all route, adicionar:

```jsx
<Route path="/reports/airtime" element={<AirtimeReportPage />} />
```

- [ ] **Step 2: Build + lint**

```bash
cd frontend && npm run lint && npm run build
```

- [ ] **Step 3: Smoke manual**

```bash
cd frontend && npm run dev
# Navegue para http://localhost:5173/reports/airtime
# Verifique:
#   1. Sidebar mostra "Relatório Data/Hora"
#   2. Página carrega com filtros visíveis e ghost preview
#   3. Selecionar campanha popula a lista
#   4. Trocar de página funciona
#   5. Player inline toca o áudio
#   6. Painel direito mostra "Total por áudio"
#   7. Botão "Exportar CSV" baixa o arquivo (admin)
```

- [ ] **Step 4: Commit**

```bash
git add frontend/src/App.jsx
git commit -m "$(cat <<'EOF'
feat(airtime): wire /reports/airtime route into App

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 4 — Documentação

### Task 16: Doc operacional em `docs/airtime-report.md`

**Files:**
- Create: `docs/airtime-report.md`

- [ ] **Step 1: Escrever doc**

```markdown
# Relatório Data e Hora (`/reports/airtime`)

Lista cronológica paginada de veiculações com player de áudio inline e
totalizador por material. Complementa o calendário de cobertura em
`/detections` — não substitui.

## Acesso

- **Admin**: vê todas as campanhas. Pode exportar CSV.
- **Cliente**: vê apenas campanhas do próprio `client_id` (filtro herdado de
  `useCampaigns()` no backend). Botão de export não aparece.

## Filtros

- **Campanha** (obrigatório): seletor com avatar do cliente + nome da campanha.
- **Período**: dois date pickers (`de`/`até`) com 4 presets (Últimos 7 dias,
  Este mês, Campanha inteira, Personalizado).
- **Busca**: server-side, case/accent-insensitive, debounce 300 ms. Pesquisa
  em emissora (nome/cidade/UF/banda/frequência), material, tipo e cliente.

URL é fonte de verdade: `?campaign_id=…&from=YYYY-MM-DD&to=YYYY-MM-DD&q=…&page=N`.
Deep-links são compartilháveis.

## Card de veiculação

Cada card mostra: stripe colorido do tipo do material, botão play (com
spinner durante fetch do blob de evidência), data/hora, logo + nome +
dial/cidade/UF da emissora, PMM, custo, e meta com material/tipo/cliente
+ pill da categoria.

### Cálculo de PMM

PMM é o campo `stations.pmm`. Exibido em formato compacto
(`Intl.NumberFormat 'pt-BR' notation: 'compact'`) — passe o mouse pra ver
o valor cheio no tooltip.

### Cálculo de custo

Depende do modo de pricing da campanha (`campaign_pricing.mode`):
- **per_insertion**: custo = `per_type[type_id].unit_value`. Exibido em
  `R$ X,XX` (pink-600).
- **consolidated**: sem custo por inserção (o cliente paga plano fechado).
  Aparece como badge cinza "Consolidado" com tooltip explicativo.

### Player de áudio

Click no botão play:
1. Fetch `GET /detections/:id/evidence` (blob).
2. `URL.createObjectURL(blob)` é cacheado no `useRef` por linha.
3. `AudioPlayer` renderiza inline embaixo do card.
4. Apenas um player ativo por vez — abrir outro pausa+fecha o anterior.
5. Blob URLs revogadas no unmount da página.

## Painel "Total por áudio"

Sticky à direita (vira topo em <1024 px). Lista materiais agregados,
ordenados por contagem desc. Cada linha tem barra horizontal escalada
(largura = `count / max * 100%`). Hover destaca os cards correspondentes
na lista (cross-highlight).

Honra o mesmo `q` da lista — busca filtra ambos consistentemente.

Endpoint: `GET /v1/internal/detections/aggregate-by-material?campaign_id=…&from=…&to=…&q=…`

## Export CSV (admin)

`GET /v1/internal/detections/export?campaign_id=…&from=…&to=…&q=…&sort=…`

- Streaming server-side (memória bounded).
- `Content-Type: text/csv; charset=utf-8`, **com BOM** pra Excel pt-BR.
- Separador: `;`.
- Colunas: Data, Hora, Emissora, Frequência, Banda, Cidade, UF, Material,
  Duração (s), Tipo, Cliente, PMM, Categoria.
- Datas em horário de São Paulo (`America/Sao_Paulo`).
- Gated em `auth.RequireRole("admin")`.

## Paginação

- 10 por página, **server-side**.
- Numeradores com ellipsis (`1 … 5 6 7 … 25`).
- Trocar filtros reseta para `page=1`.
- Trocar de página rola pro topo da lista (não da janela — preserva o
  header sticky).

## Endpoints novos

| Método | Path | Acesso | Descrição |
|---|---|---|---|
| GET | `/v1/internal/detections?page=N&page_size=&q=&from=&to=&sort=` | admin/operator | Lista paginada (gated em `page`) |
| GET | `/v1/internal/detections/aggregate-by-material?campaign_id=&from=&to=&q=` | admin/operator | Totalizador por material |
| GET | `/v1/internal/detections/export?campaign_id=&from=&to=&q=&sort=` | admin | CSV streaming |

## Riscos conhecidos

- **Performance da busca**: `unaccent(lower(...))` + `LIKE` em campos
  concatenados é razoável até ~1M linhas/campanha/período. Se virar
  hotspot, considerar `pg_trgm` + índice GIN. Métrica a observar:
  latência p99 do endpoint quando `q` está presente.
- **Export grande**: streaming protege a memória do API, mas pode estourar
  timeout do reverse proxy em campanhas muito grandes. Limite prático
  testado: 100k detecções (~7 MB CSV).
- **Custo "Consolidado"**: usuários acostumados a ver valor por inserção
  podem estranhar o badge. Tooltip explica, mas considerar adicionar
  ajuda visível no header (estilo "saiba mais").

## Diagnóstico

"Por que não vejo a campanha X no select?" → verificar `client_id` da
campanha vs. `claims.ClientID` do JWT do usuário logado.

"Por que o painel mostra menos detecções que a soma das páginas?" → não
deveria; ambos endpoints aplicam os mesmos filtros (campaign, from, to,
q, ignored_at IS NULL, retracted_at IS NULL). Se divergir, abrir issue.

"Por que clicar play falha?" → checar `detection.evidence_status`. Se for
`missing` ou `pending`, o áudio não está disponível ainda (manual upload
falhou, ou o evidence worker ainda não processou).
```

- [ ] **Step 2: Commit**

```bash
git add docs/airtime-report.md
git commit -m "$(cat <<'EOF'
docs: airtime report operational documentation

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-review — coverage da spec

| Seção da spec | Tasks que implementam |
|---|---|
| §3 Rota/Sidebar | Task 7 (sidebar) + Task 15 (rota) |
| §4 Layout split | Task 13 (página) + Task 14 (CSS) |
| §5.1 Seletor campanha | Task 12 (filtros) |
| §5.2 Range datas | Task 12 (filtros) |
| §5.3 Presets | Task 12 (filtros) |
| §5.4 Busca server-side | Task 12 (filtros, debounce) + Task 1 (q SQL) + Task 6 (hook) |
| §5.5 Export CSV admin | Task 5 (handler) + Task 6 (função) + Task 12 (botão) |
| §6 Cards | Task 9 (row) + Task 14 (CSS) |
| §6.4 Player inline | Task 9 (row, ensureBlob) |
| §7 Paginação | Task 1 (SQL) + Task 2 (handler) + Task 8 (componente) |
| §8 Painel total | Task 3 (SQL) + Task 4 (handler) + Task 6 (hook) + Task 10 (componente) |
| §9 Empty/skeleton/error | Task 11 (ghost) + Task 13 (skeleton inline) + CSS |
| §10 Backend endpoints | Tasks 1–5 |
| §11 Frontend arquivos | Tasks 6–15 |
| §12 Responsivo | Task 14 (CSS @media) |
| §13 Acessibilidade | Task 9 (aria-*) + Task 8 (nav role) + Task 10 (aside role) |
| §14 Performance | Task 1 (índice) + Task 6 (keepPreviousData, cache keys) |
| §17 Docs operacional | Task 16 |
