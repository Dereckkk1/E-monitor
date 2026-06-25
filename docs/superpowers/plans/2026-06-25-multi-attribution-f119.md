# Multi-atribuição (F-119) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Uma veiculação física passa a contar para **todas** as campanhas que rodam o mesmo áudio na emissora, sem tocar no subsistema frágil de desambiguação/audit/evidência.

**Architecture:** `detections` continua 1 linha por tocada física (dona de evidência/audit/dedup/retração, intocada). Uma tabela nova `detection_campaigns` carrega N projeções por campanha (campaign_id + commercial_id + category). Uma view de compat `detection_attributions` minimiza o churn das leituras. Fan-out atrás da flag `MULTI_ATTRIBUTION`.

**Tech Stack:** Go (pgx), PostgreSQL 16 (tabelas particionadas por RANGE), golang-migrate, NATS.

**Spec:** [docs/superpowers/specs/2026-06-25-multi-attribution-f119-design.md](2026-06-25-multi-attribution-f119-design.md)

---

## File Structure

- `migrations/0040_detection_campaigns.up.sql` / `.down.sql` — tabela `detection_campaigns` (+ partições + índices), view `detection_attributions`, rewrite `daily_play_summary`, backfill 1:1.
- `workers/internal/catalog/detection_campaigns.go` — repo da projeção: `InsertProjections`, `RecategorizeForCampaignMaterial`.
- `workers/internal/catalog/detection_campaigns_test.go` — testes do repo (DB-gated).
- `workers/internal/evidence/attribution.go` — `resolveAllAttributions` (novo, ao lado do `resolveAttribution` existente).
- `workers/internal/evidence/attribution_test.go` — teste do fan-out resolver (DB-gated).
- `workers/internal/evidence/service.go` — escrever projeções após `detections.Create`, gated por `multiAttribution`.
- `workers/cmd/api/main.go` — ler `MULTI_ATTRIBUTION` e injetar no `evidence.NewService`.
- `workers/internal/catalog/detections.go`, `insights.go`, `live_map.go`, `management_overview.go` — leituras por-campanha → view; KPIs cross-campanha → contagem por tocada.
- `workers/internal/catalog/distribution_rules.go` — recategorizador escreve em `detection_campaigns.category`.
- `workers/internal/catalog/multi_attribution_test.go` — teste end-to-end (1 tocada → 2 campanhas).
- `workers/internal/catalog/detection_consistency_test.go` — estender p/ projeções.
- Docs: `detection-count-consistency.md`, `version-disambiguation.md`, `evidence-audit.md`, `follow-ups-fase2.md`, novo `docs/features/multi-attribution.md`.

**Convenção de teste:** os testes que tocam o banco são DB-gated por `TEST_DATABASE_URL` (padrão do repo, ver `detection_consistency_test.go`). Rode com `cd workers && TEST_DATABASE_URL=... go test ./internal/...`.

---

## Fase 1 — Modelo de dados (flag OFF = comportamento idêntico)

### Task 1: Migration — tabela `detection_campaigns` + partições + índices

**Files:**
- Create: `migrations/0040_detection_campaigns.up.sql`
- Create: `migrations/0040_detection_campaigns.down.sql`

- [ ] **Step 1: Escrever a `.up.sql` (parte 1 — tabela)**

```sql
BEGIN;

CREATE TABLE detection_campaigns (
    detection_id  UUID        NOT NULL,
    detected_at   TIMESTAMPTZ NOT NULL,
    campaign_id   UUID        NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    commercial_id UUID        NOT NULL,
    category      TEXT        NOT NULL DEFAULT 'orphan'
                  CHECK (category IN ('in_slot','out_slot','out_date','orphan')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (detection_id, detected_at, campaign_id),
    FOREIGN KEY (detection_id, detected_at)
        REFERENCES detections(id, detected_at) ON DELETE CASCADE
) PARTITION BY RANGE (detected_at);

CREATE INDEX idx_detcamp_campaign_time   ON detection_campaigns (campaign_id, detected_at DESC);
CREATE INDEX idx_detcamp_commercial_time ON detection_campaigns (commercial_id, detected_at DESC);
```

- [ ] **Step 2: Adicionar criação de partições espelhando `detections`**

Ler o DO-block de partição em `migrations/0001_initial.up.sql:120-130` (o `EXECUTE format('CREATE TABLE detections_%s PARTITION OF detections ...')`). Replicar o MESMO range loop para `detection_campaigns`, gerando `detection_campaigns_%s PARTITION OF detection_campaigns FOR VALUES FROM (...) TO (...)` para cada partição existente de `detections`. Anexar ao `.up.sql`. Fechar com `COMMIT;`.

> Se houver job/função que cria partições futuras de `detections`, estendê-lo para criar a irmã de `detection_campaigns` (mesma migration ou nota no §Docs). Verificar com: `grep -rn "PARTITION OF detections" migrations/ workers/`.

- [ ] **Step 3: Escrever a `.down.sql`**

```sql
BEGIN;
DROP TABLE IF EXISTS detection_campaigns CASCADE;
COMMIT;
```

- [ ] **Step 4: Testar migrate up/down local**

Run: `cd workers && migrate -path ../migrations -database "$TEST_DATABASE_URL" up && migrate -path ../migrations -database "$TEST_DATABASE_URL" down 1 && migrate -path ../migrations -database "$TEST_DATABASE_URL" up`
Expected: sem erro; `\d detection_campaigns` mostra a tabela particionada e os 2 índices.

- [ ] **Step 5: Commit**

```bash
git add migrations/0040_detection_campaigns.up.sql migrations/0040_detection_campaigns.down.sql
git commit -m "feat(detections): tabela detection_campaigns (F-119 fase 1, schema)"
```

### Task 2: Migration — view `detection_attributions` + rewrite `daily_play_summary` + backfill

**Files:**
- Modify: `migrations/0040_detection_campaigns.up.sql` (anexar antes do `COMMIT`)

- [ ] **Step 1: Backfill 1:1 das projeções (antes do COMMIT)**

```sql
INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
SELECT id, detected_at, campaign_id, commercial_id, category
FROM detections
ON CONFLICT DO NOTHING;
```

- [ ] **Step 2: View de compatibilidade**

```sql
CREATE VIEW detection_attributions AS
SELECT d.id, d.station_id, d.detected_at, d.evidence_status, d.evidence_key,
       d.retracted_at, d.ignored_at, d.confidence, d.hash_count, d.audit_coverage,
       d.match_start_offset_ms, d.match_end_offset_ms, d.temporal_coverage,
       d.variant_used, d.rate_used, d.created_at,
       dc.campaign_id, dc.commercial_id, dc.category
FROM detections d
JOIN detection_campaigns dc
  ON dc.detection_id = d.id AND dc.detected_at = d.detected_at;
```

- [ ] **Step 3: Rewrite `daily_play_summary` lendo `detection_campaigns`**

Ler a definição **atual** da view em `migrations/0029_daily_summary_exclude_audit_rejected.up.sql` (é a canônica — embute `retracted_at IS NULL AND ignored_at IS NULL AND evidence_status <> 'audit_rejected'`). Reescrever **só o CTE `actual`** para agregar a projeção, mantendo o resto idêntico:

```sql
DROP VIEW IF EXISTS daily_play_summary;
CREATE VIEW daily_play_summary AS
WITH expected AS ( /* ...idêntico ao 0029... */ ),
expected_with_override AS ( /* ...idêntico ao 0029... */ ),
actual AS (
    SELECT
        dc.campaign_id,
        m.type_id,
        d.station_id,
        date_trunc('day', d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS for_date,
        COUNT(*) FILTER (WHERE dc.category = 'in_slot')::int  AS in_slot,
        COUNT(*) FILTER (WHERE dc.category = 'out_slot')::int AS out_slot,
        COUNT(*) FILTER (WHERE dc.category = 'out_date')::int AS out_date,
        COUNT(*) FILTER (WHERE dc.category = 'orphan')::int   AS orphan
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    JOIN materials   m ON m.id = dc.commercial_id
    WHERE d.retracted_at IS NULL
      AND d.ignored_at  IS NULL
      AND d.evidence_status <> 'audit_rejected'
      AND m.type_id IS NOT NULL
    GROUP BY dc.campaign_id, m.type_id, d.station_id, for_date
)
SELECT /* ...bloco final idêntico ao 0029... */ ;
```

> Copiar `expected`, `expected_with_override` e o `SELECT` final **verbatim** do 0029 (não reinventar). Só o `actual` muda: `detections d` → `detection_campaigns dc JOIN detections d`, e `d.category`/`d.campaign_id` → `dc.category`/`dc.campaign_id`. O gate aprovado agora vem do JOIN em `detections`.

- [ ] **Step 4: Re-testar migrate up + igualdade do backfill**

Run: `cd workers && migrate -path ../migrations -database "$TEST_DATABASE_URL" up`
Verificação (seed algumas detections antes, ou usar uma cópia): a contagem por `(campaign_id, station_id, for_date)` em `daily_play_summary` tem que ser **idêntica** antes/depois da migration (backfill 1:1 não muda número). Query de sanidade:

```sql
SELECT campaign_id, station_id, for_date, in_slot, out_slot, expected
FROM daily_play_summary ORDER BY 1,2,3 LIMIT 20;
```

- [ ] **Step 5: Commit**

```bash
git add migrations/0040_detection_campaigns.up.sql
git commit -m "feat(detections): view detection_attributions + daily_play_summary sobre projeções + backfill 1:1 (F-119)"
```

### Task 3: Shadow test do backfill contra dados de prod (regra 4.8)

**Files:** nenhum (procedimento)

- [ ] **Step 1: Rodar o shadow migration test**

Seguir `docs/operations/migrations.md §Testar migration contra dados de prod`: subir postgres descartável, restaurar dump de prod, `migrate up` com a 0040. Confirmar que (a) o backfill não falha e (b) `SELECT count(*) FROM detection_campaigns` == `SELECT count(*) FROM detections`.
Expected: sucesso; counts iguais. Se falhar, NÃO prosseguir pro deploy.

- [ ] **Step 2: Sem commit** (é validação operacional). Registrar o resultado no PR.

---

## Fase 2 — Leituras sobre o novo modelo (ainda 1:1, idêntico)

### Task 4: Refactor das leituras por-campanha `detections.go` → view

**Files:**
- Modify: `workers/internal/catalog/detections.go` (funções `List`, `ListPaged`, `IterateForExport`, `AggregateByMaterial`, `AggregateByMaterialStation`, `AggregateByStation`)

**Transformação canônica (aplicar em cada função):** trocar `FROM detections d` por `FROM detection_attributions d`. O `ApprovedDetectionsFilter` e todos os `WHERE d.campaign_id = $X`, `d.commercial_id`, `d.category`, `d.station_id` continuam válidos (a view expõe esses campos). NÃO mudar binds/params.

- [ ] **Step 1: Escrever/estender o teste de regressão**

Em `detection_consistency_test.go` já há seed (3 aprovadas + 1 retratada + 1 ignorada + 1 rejeitada). Garantir que ele exercita `Detections.List`, `aggregateCore` e a view e exige `3`. Rodar ANTES da mudança pra ter baseline verde:
Run: `cd workers && go test ./internal/catalog -run TestDetectionConsistency -v`
Expected: PASS (baseline).

- [ ] **Step 2: Aplicar a troca `FROM detections d` → `FROM detection_attributions d`**

Em cada uma das 6 funções acima, localizar a cláusula `FROM detections d` (algumas têm JOIN a `materials`/`stations` — manter) e trocar a tabela base pela view. Exemplo (`List`, ~detections.go:651):

```go
// antes
FROM detections d
JOIN ...
WHERE ($1::uuid IS NULL OR d.campaign_id = $1) AND ` + ApprovedDetectionsFilter + ` ...
// depois
FROM detection_attributions d
JOIN ...
WHERE ($1::uuid IS NULL OR d.campaign_id = $1) AND ` + ApprovedDetectionsFilter + ` ...
```

- [ ] **Step 3: Rodar o teste de consistência**

Run: `cd workers && go test ./internal/catalog -run TestDetectionConsistency -v`
Expected: PASS (idêntico ao baseline — backfill 1:1 garante mesmos números).

- [ ] **Step 4: `go build` + vet**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./... && go vet ./internal/catalog`
Expected: limpo (regra 6.1 — cross-compile linux).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/catalog/detection_consistency_test.go
git commit -m "refactor(detections): leituras por-campanha sobre detection_attributions (F-119)"
```

### Task 5: Refactor `insights.go`, `live_map.go`, `management_overview.go` (por-campanha)

**Files:**
- Modify: `workers/internal/catalog/insights.go` (`aggregateCore`, `aggregateBuckets`, `computeCPM`)
- Modify: `workers/internal/catalog/live_map.go` (`queryStations` MAX subquery, `queryRecentDetections`)
- Modify: `workers/internal/catalog/management_overview.go` (`queryStations` MAX subquery, `queryRecentDetections`)

- [ ] **Step 1: Aplicar a mesma troca `FROM detections d` → `FROM detection_attributions d`**

Em cada função listada, trocar a tabela base pela view, preservando filtros/binds. As subqueries `SELECT MAX(d.detected_at) FROM detections d WHERE d.station_id=s.id AND d.campaign_id=$1 AND ` + filtro → idem com a view.

- [ ] **Step 2: `go build` + vet**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./... && go vet ./internal/catalog`
Expected: limpo.

- [ ] **Step 3: Teste de fumaça das telas (DB-gated, se existir; senão manual)**

Run: `cd workers && go test ./internal/catalog -run 'TestInsights|TestLiveMap|TestManagement' -v`
Expected: PASS (ou, se não houver testes, validar manualmente que `/insights`, `/live-map`, `/management` retornam os mesmos números de antes com a flag OFF).

- [ ] **Step 4: Commit**

```bash
git add workers/internal/catalog/insights.go workers/internal/catalog/live_map.go workers/internal/catalog/management_overview.go
git commit -m "refactor(insights/live-map/management): leituras por-campanha sobre detection_attributions (F-119)"
```

### Task 6: KPIs cross-campanha contam a tocada uma vez

**Files:**
- Modify: `workers/internal/catalog/management_overview.go` (`queryKPIs` — `AiringsTotal`, `AiringsToday`)
- Test: `workers/internal/catalog/management_overview_test.go`

- [ ] **Step 1: Escrever o teste do double-count**

```go
func TestManagementKPIs_NoDoubleCountAcrossCampaigns(t *testing.T) {
    // DB-gated. Seed: 1 detection física (1 linha em detections) com 2 projeções
    // em detection_campaigns (campaign A e B, ambas no filtro). Espera AiringsTotal == 1.
    db := testDB(t)
    seedOneAiringTwoCampaigns(t, db) // helper: insere 1 detections + 2 detection_campaigns
    got := queryKPIsAiringsTotal(t, db, scopeBoth)
    if got != 1 {
        t.Fatalf("AiringsTotal=%d, want 1 (uma tocada física conta uma vez)", got)
    }
}
```

- [ ] **Step 2: Rodar — falha**

Run: `cd workers && go test ./internal/catalog -run TestManagementKPIs_NoDoubleCount -v`
Expected: FAIL (`AiringsTotal=2`).

- [ ] **Step 3: Trocar a contagem para a tocada física**

Em `queryKPIs`, as subqueries `AiringsTotal`/`AiringsToday` passam a contar `detections` base (uma linha por tocada) escopadas via EXISTS:

```sql
SELECT COUNT(*) FROM detections d
WHERE d.detected_at::date BETWEEN $4::date AND $5::date
  AND ` + ApprovedDetectionsFilter + `
  AND EXISTS (SELECT 1 FROM detection_campaigns dc
              WHERE dc.detection_id = d.id AND dc.detected_at = d.detected_at
                AND dc.campaign_id IN (SELECT id FROM scoped))
```

- [ ] **Step 4: Rodar — passa**

Run: `cd workers && go test ./internal/catalog -run TestManagementKPIs_NoDoubleCount -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/management_overview.go workers/internal/catalog/management_overview_test.go
git commit -m "fix(management): KPIs cross-campanha contam a tocada física uma vez (F-119)"
```

---

## Fase 3 — Escrita das projeções (canônica primeiro, depois fan-out)

### Task 7: Repo `detection_campaigns` — `InsertProjections`

**Files:**
- Create: `workers/internal/catalog/detection_campaigns.go`
- Test: `workers/internal/catalog/detection_campaigns_test.go`

- [ ] **Step 1: Teste do insert idempotente**

```go
func TestInsertProjections_Idempotent(t *testing.T) {
    db := testDB(t)
    det := seedDetection(t, db) // 1 linha em detections
    repo := catalog.NewDetectionCampaigns(db)
    projs := []catalog.Projection{{CampaignID: det.CampaignID, CommercialID: det.CommercialID, Category: "in_slot"}}
    if err := repo.InsertProjections(ctx, det.ID, det.DetectedAt, projs); err != nil { t.Fatal(err) }
    // re-inserir não duplica
    if err := repo.InsertProjections(ctx, det.ID, det.DetectedAt, projs); err != nil { t.Fatal(err) }
    if n := countProjections(t, db, det.ID); n != 1 {
        t.Fatalf("projections=%d, want 1 (ON CONFLICT DO NOTHING)", n)
    }
}
```

- [ ] **Step 2: Rodar — falha** (`NewDetectionCampaigns` não existe)

Run: `cd workers && go test ./internal/catalog -run TestInsertProjections -v`
Expected: FAIL (compilação).

- [ ] **Step 3: Implementar o repo**

```go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Projection struct {
	CampaignID   uuid.UUID
	CommercialID uuid.UUID
	Category     string
}

type DetectionCampaigns struct{ pool *pgxpool.Pool }

func NewDetectionCampaigns(pool *pgxpool.Pool) *DetectionCampaigns {
	return &DetectionCampaigns{pool: pool}
}

func (d *DetectionCampaigns) InsertProjections(ctx context.Context, detectionID uuid.UUID, detectedAt time.Time, projs []Projection) error {
	for _, p := range projs {
		_, err := d.pool.Exec(ctx, `
			INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (detection_id, detected_at, campaign_id) DO NOTHING`,
			detectionID, detectedAt, p.CampaignID, p.CommercialID, p.Category)
		if err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Rodar — passa**

Run: `cd workers && go test ./internal/catalog -run TestInsertProjections -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/detection_campaigns.go workers/internal/catalog/detection_campaigns_test.go
git commit -m "feat(catalog): repo detection_campaigns com InsertProjections idempotente (F-119)"
```

### Task 8: `resolveAllAttributions` (fan-out resolver)

**Files:**
- Modify: `workers/internal/evidence/attribution.go`
- Test: `workers/internal/evidence/attribution_test.go`

- [ ] **Step 1: Teste — 2 campanhas, mesmo master**

```go
func TestResolveAllAttributions_TwoCampaignsSameMaster(t *testing.T) {
    db := testDB(t)
    // seed: 2 materials mesmo master_sha256, cada um linkado a uma campanha ativa
    // targetando a mesma estação no período.
    canonical, station, when := seedDuplicateAcrossCampaigns(t, db)
    got, err := resolveAllAttributions(ctx, db, canonical, station, when)
    if err != nil { t.Fatal(err) }
    if len(got) != 2 {
        t.Fatalf("got %d attributions, want 2 (uma por campanha)", len(got))
    }
}
```

- [ ] **Step 2: Rodar — falha**

Run: `cd workers && go test ./internal/evidence -run TestResolveAllAttributions -v`
Expected: FAIL (função não existe).

- [ ] **Step 3: Implementar (ao lado do `resolveAttribution`)**

```go
// resolveAllAttributions devolve UMA tupla (commercialID, campaignID) por campanha
// ativa/programada que linka, via campaign_materials, um material com o MESMO
// master_sha256 do material canônico, targetando a estação, com detectedAt no
// período. É o fan-out do resolveAttribution: troca o LIMIT 1 por DISTINCT ON
// (campaign_id) com o link mais recente vencendo. Só materials (o caso
// multi-campanha); legados commercials seguem single via resolveAttribution.
func resolveAllAttributions(ctx context.Context, db *pgxpool.Pool, canonicalCommercialID uuid.UUID,
	stationID uuid.UUID, detectedAt time.Time) ([]catalog.Projection, error) {
	rows, err := db.Query(ctx, `
		SELECT DISTINCT ON (cm.campaign_id) cm.campaign_id, m.id
		FROM materials m
		JOIN campaign_materials cm ON cm.material_id = m.id
		JOIN campaigns ca           ON ca.id = cm.campaign_id
		WHERE m.master_sha256 = (SELECT master_sha256 FROM materials WHERE id = $1)
		  AND $2 = ANY(cm.target_stations)
		  AND ca.status IN ('programada','ativa')
		  AND $3::date BETWEEN ca.start_date AND ca.end_date
		ORDER BY cm.campaign_id, cm.added_at DESC
	`, canonicalCommercialID, stationID, detectedAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []catalog.Projection
	for rows.Next() {
		var p catalog.Projection
		if err := rows.Scan(&p.CampaignID, &p.CommercialID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Rodar — passa**

Run: `cd workers && go test ./internal/evidence -run TestResolveAllAttributions -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/evidence/attribution.go workers/internal/evidence/attribution_test.go
git commit -m "feat(evidence): resolveAllAttributions (fan-out por master_sha256) (F-119)"
```

### Task 9: Flag `MULTI_ATTRIBUTION` + escrita das projeções no service

**Files:**
- Modify: `workers/cmd/api/main.go` (ler env, injetar)
- Modify: `workers/internal/evidence/service.go` (campo + escrita pós-Create)

- [ ] **Step 1: Ler a flag e injetar (espelhar `disambigByCoverage`)**

Em `main.go`, após a linha `disambigByCoverage := os.Getenv("DISAMBIG_BY_COVERAGE") == "true"` (~160):

```go
multiAttribution := os.Getenv("MULTI_ATTRIBUTION") == "true"
if multiAttribution {
	logger.Info("F-119 multi-attribution ENABLED (MULTI_ATTRIBUTION=true)")
}
```

Adicionar `multiAttribution` e o repo `catalog.NewDetectionCampaigns(pool)` à assinatura de `evidence.NewService(...)`.

- [ ] **Step 2: Escrever as projeções após `detections.Create`**

Em `service.go`, logo após o `det, err := s.detections.Create(...)` bem-sucedido (~linha 195-207). A categoria de cada projeção reusa o categorizador por-campanha já existente (via `s.detections` — expor um helper `CategorizeFor(campaignID, commercialID, station, detectedAt)` que chama o `categorize()` interno, ou recomputar inline). Lógica:

```go
// Sempre grava a projeção canônica (1:1). Com fan-out, adiciona as demais campanhas.
projs := []catalog.Projection{{CampaignID: campaignID, CommercialID: commercialID, Category: det.Category}}
if s.multiAttribution {
	all, aerr := resolveAllAttributions(ctx, s.db, commercialID, stationID, detectedAt)
	if aerr != nil {
		s.log.Warn("multi-attribution: resolveAll falhou; usando só a canônica", zap.Error(aerr))
	} else {
		seen := map[uuid.UUID]bool{campaignID: true}
		for _, p := range all {
			if seen[p.CampaignID] {
				continue
			}
			cat, cerr := s.detections.CategorizeFor(ctx, p.CampaignID, p.CommercialID, stationID, detectedAt)
			if cerr != nil {
				cat = "orphan"
			}
			projs = append(projs, catalog.Projection{CampaignID: p.CampaignID, CommercialID: p.CommercialID, Category: cat})
			seen[p.CampaignID] = true
		}
	}
}
if perr := s.detectionCampaigns.InsertProjections(ctx, det.ID, det.DetectedAt, projs); perr != nil {
	s.log.Error("multi-attribution: InsertProjections falhou", zap.Error(perr))
}
```

> Expor `CategorizeFor` em `catalog/detections.go` como wrapper público do `categorize()` privado (mesma assinatura de input).

- [ ] **Step 3: `go build` + vet**

Run: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./... && go vet ./internal/evidence ./cmd/api`
Expected: limpo. (Regra 6.5 — garantir que `evidence.NewService` ainda sobe sem panic: `multiAttribution=false` por default.)

- [ ] **Step 4: Commit**

```bash
git add workers/cmd/api/main.go workers/internal/evidence/service.go workers/internal/catalog/detections.go
git commit -m "feat(evidence): escrita de projeções detection_campaigns gated por MULTI_ATTRIBUTION (F-119)"
```

### Task 10: Recategorizador escreve em `detection_campaigns.category`

**Files:**
- Modify: `workers/internal/catalog/distribution_rules.go` (`recategorizeScope` / `RecategorizeForMaterial`)

- [ ] **Step 1: Teste — recategorização atualiza a projeção**

```go
func TestRecategorize_UpdatesProjectionCategory(t *testing.T) {
    db := testDB(t)
    // seed: detection + projeção orphan; cria regra que cobre o horário
    // recategoriza; espera projeção virar in_slot
    seedDetectionWithOrphanProjection(t, db)
    seedRuleCoveringSlot(t, db)
    recategorizeForCampaignMaterial(t, db)
    if cat := projectionCategory(t, db); cat != "in_slot" {
        t.Fatalf("category=%s, want in_slot", cat)
    }
}
```

- [ ] **Step 2: Rodar — falha**; **Step 3:** alterar o `UPDATE detections SET category=...` do recategorizador para também (ou em vez de) atualizar `detection_campaigns SET category=...` no escopo `(campaign_id, commercial→type)`. Manter o update legado de `detections.category` por compat. **Step 4: Rodar — passa.**

Run: `cd workers && go test ./internal/catalog -run TestRecategorize_UpdatesProjection -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/distribution_rules.go workers/internal/catalog/distribution_rules_test.go
git commit -m "feat(catalog): recategorizador atualiza detection_campaigns.category (F-119)"
```

---

## Fase 4 — Validação end-to-end, docs, rollout

### Task 11: Teste end-to-end de multi-atribuição

**Files:**
- Create: `workers/internal/catalog/multi_attribution_test.go`

- [ ] **Step 1: Teste — 1 tocada → 2 campanhas, cada uma vê a sua**

```go
func TestMultiAttribution_OneAiringTwoCampaigns(t *testing.T) {
    db := testDB(t)
    // 1 detection física; 2 projeções: campA (in_slot) e campB (orphan).
    seedAiringTwoProjections(t, db)
    // grade da campA mostra 1 in_slot; grade da campB mostra 1 orphan/bonus.
    if got := dailySummaryInSlot(t, db, campA); got != 1 { t.Fatalf("campA in_slot=%d want 1", got) }
    if got := dailySummaryInSlot(t, db, campB); got != 0 { t.Fatalf("campB in_slot=%d want 0", got) }
    // gate aprovado: retrair a tocada base esconde das DUAS
    retractBaseDetection(t, db)
    if got := dailySummaryInSlot(t, db, campA); got != 0 { t.Fatalf("após retração campA=%d want 0", got) }
}
```

- [ ] **Step 2: Rodar — passa** (a infra das fases 1-3 já suporta)

Run: `cd workers && go test ./internal/catalog -run TestMultiAttribution -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add workers/internal/catalog/multi_attribution_test.go
git commit -m "test(catalog): e2e multi-atribuição 1 tocada -> N campanhas (F-119)"
```

### Task 12: Suite completa + cross-compile + docs

**Files:**
- Modify: `docs/architecture/detection-count-consistency.md`, `docs/architecture/version-disambiguation.md`, `docs/architecture/evidence-audit.md`, `docs/roadmap/follow-ups-fase2.md`
- Create: `docs/features/multi-attribution.md`

- [ ] **Step 1: Rodar a suite inteira**

Run: `cd workers && go test ./... 2>&1 | tail -30`
Expected: PASS (atenção a flaky conhecido `TestBuildDailySummary_WithDowntime` antes de ~13:00 UTC — regra 6.6; confirmar que falha só está em pacote não tocado).

- [ ] **Step 2: Gold standard de build (regra 6.1)**

Run: `docker build -f infra/docker/Dockerfiles/workers.Dockerfile -t rc-verify .` (na raiz)
Expected: build OK.

- [ ] **Step 3: Escrever a doc da feature** `docs/features/multi-attribution.md` com header YAML (`status: implementado`, `codigo-relacionado` apontando os arquivos), explicando o modelo (tocada física × projeções), a flag, e o invariante preservado.

- [ ] **Step 4: Atualizar docs existentes** — `detection-count-consistency.md` (a contagem agora é por projeção, gate na tocada base; KPIs cross-campanha contam a tocada física); `version-disambiguation.md`/§9.8 (semântica de multi-atribuição); `evidence-audit.md` (remover/ajustar a nota "Sem suporte a multi-attribution"); marcar **F-119 como implementado** em `follow-ups-fase2.md`.

- [ ] **Step 5: Commit**

```bash
git add docs/
git commit -m "docs: multi-atribuição F-119 (feature + consistência + version-disambiguation + follow-ups)"
```

### Task 13: Rollout em produção (faseado)

**Files:** nenhum (procedimento; ver `docs/operations/deploy.md` e regra 6)

- [ ] **Step 1: Deploy com flag OFF.** `MULTI_ATTRIBUTION` ausente/`false`. O `deploy.sh` roda o shadow migration test da 0040 (backfill) e aborta se falhar. Após subir: confirmar que `/management`, `/insights`, `/detections` mostram os MESMOS números de antes (backfill 1:1).

- [ ] **Step 2: Validar a equivalência por alguns dias** com a flag OFF (cada tocada = 1 projeção canônica). Monitorar os 2 KPIs cross-campanha (não podem ter mudado).

- [ ] **Step 3: Ligar `MULTI_ATTRIBUTION=true`** no `.env` da VM, recreate só da `api` (`up -d --force-recreate --no-deps api`, regra 4.1). A partir daqui, tocadas em emissoras compartilhadas geram projeções pras N campanhas. Validar no caso UNIUBE (ou um novo) que a 2ª campanha passa a contar e os KPIs cross-campanha **não** inflam.

---

## Self-Review (preenchido)

- **Cobertura do spec:** §4 modelo → Task 1-2; §5 escrita → Task 7-9; §6 leitura → Task 4-6; §recategorização → Task 10; §8 migração/backfill → Task 1-3; §9 rollout → Task 13; §10 testes → Task 11-12; docs → Task 12. ✔
- **Placeholders:** os CTEs `expected`/`expected_with_override`/`SELECT final` são marcados "verbatim do 0029" (instrução precisa de copiar fonte existente, não vago). Helpers de teste (`seed*`, `testDB`) seguem o padrão de `detection_consistency_test.go`. ✔
- **Consistência de tipos:** `catalog.Projection{CampaignID, CommercialID, Category}` usado igual em Task 7/8/9; `InsertProjections(ctx, detectionID, detectedAt, []Projection)` e `resolveAllAttributions(...) []catalog.Projection` batem; `CategorizeFor` exposto em Task 9 e usado lá mesmo. ✔
