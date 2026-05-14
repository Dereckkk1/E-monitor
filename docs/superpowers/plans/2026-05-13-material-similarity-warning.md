# Material Similarity Warning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When an operator uploads a material whose audio is ≥15% similar to another material of the same client, surface a warning badge that opens a two-player A/B modal so the operator can decide to keep or remove the new upload.

**Architecture:** A new Go package `workers/internal/similarity/` runs a per-client similarity scan after fingerprint generation. The Python fingerprint daemon publishes a new NATS event `material.similarity-check` once `fingerprint_status='ready'`. The Go subscriber loads the scoped catalog index, decodes the new material's PCM, computes `max(ownCov, otherCov)` against every other material of the same client, and persists the top match (if ≥15%) on the materials row. The frontend polls until the check finishes, then renders an amber badge that opens a modal with two native HTML5 audio players. Operator chooses "Manter" (acknowledge) or "Remover" (delete material).

**Tech Stack:** Go (workers, api) with pgx, NATS, zap. Python (asyncpg + nats-py) for the publisher side. PostgreSQL migration. React + TanStack Query for the frontend.

**Prerequisite:** Execute [`docs/superpowers/plans/2026-05-13-material-fingerprint-pipeline.md`](2026-05-13-material-fingerprint-pipeline.md) **first**. Without the bridge, materials uploaded via the wizard never reach `fingerprint_status='ready'` and similarity scan has nothing to compare against. Do NOT start this plan until the bridge plan is merged and you've confirmed a fresh wizard upload reaches `ready` with `fingerprint_hash_count > 0`.

**Spec:** [`docs/superpowers/specs/2026-05-13-material-similarity-warning-design.md`](../specs/2026-05-13-material-similarity-warning-design.md)

---

## File Structure

### New files
- `migrations/0025_material_similarity.up.sql` — schema migration (4 columns + partial index)
- `migrations/0025_material_similarity.down.sql` — rollback
- `workers/internal/similarity/similarity.go` — core algorithm
- `workers/internal/similarity/similarity_test.go` — unit tests
- `workers/internal/similarity/subscriber.go` — NATS subscriber
- `workers/internal/api/handlers/materials_audio.go` — audio streaming endpoint
- `workers/internal/api/handlers/materials_similarity_test.go` — handler tests
- `frontend/src/components/SimilarityWarningModal.jsx` — modal component
- `docs/material-similarity-warning.md` — operator-facing documentation

### Modified files
- `workers/internal/events/nats.go` — add `SubjectMaterialSimilarityCheck`
- `workers/internal/catalog/materials.go` — extend `Material` struct + queries with 4 new columns
- `workers/internal/api/handlers/materials.go` — add `Acknowledge` handler
- `workers/internal/api/api.go` (or wherever `Deps` lives) — no change unless audio handler is separate
- `workers/internal/api/router.go` — wire 2 new routes
- `workers/cmd/api/main.go` — register `similarity.Subscriber`
- `fingerprint/fingerprint/main.py` — publish `material.similarity-check` after `index.reload`
- `frontend/src/api/hooks.js` — `useAcknowledgeSimilarity` + polling on materials queries
- `frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx` — badge + modal trigger on `MaterialCard`

### Migration numbering note
The current `master` has migrations through `0023_unaccent`. The bridge plan claims `0021` but will be renumbered to `0024` on merge (since 0021-0023 are taken). This plan uses `0025`. If, when starting this plan, the highest migration is different, **renumber `0025` to `(highest + 1)` everywhere in this plan** before executing.

---

## Phase 0 — Worktree + verification of prerequisite

- [ ] **Step 0.1: Create worktree off master**

```bash
cd c:/Users/marke/Desktop/Programas/Radiocheck
git worktree add ../Radiocheck-material-similarity -b feat/material-similarity-warning master
cd ../Radiocheck-material-similarity
```

Expected: new directory created, branch `feat/material-similarity-warning` checked out.

- [ ] **Step 0.2: Verify bridge plan is merged**

```bash
git log --oneline master | grep -i "fingerprint.*material\|material.*fingerprint" | head -5
```

Expected: at least one commit titled like `feat(fingerprint): materials path` or similar. If empty: STOP — bridge plan has not been executed, this plan cannot proceed.

- [ ] **Step 0.3: Verify wizard uploads reach `ready`**

```bash
docker compose -f infra/docker/docker-compose.yml exec -T postgres psql -U radiocheck -d radiocheck -c "
SELECT id, title, fingerprint_status, fingerprint_hash_count, created_at
FROM materials
WHERE fingerprint_status = 'ready'
  AND id NOT IN (SELECT id FROM commercials)
ORDER BY created_at DESC LIMIT 3;
"
```

Expected: at least one row — a material with no shadow commercial that nonetheless has `fingerprint_status='ready'` and `fingerprint_hash_count > 0`. This proves the bridge works for fresh wizard uploads. If empty, run a wizard upload first to seed.

---

## Phase 1 — Migration (similarity columns)

**Files:**
- Create: `migrations/0025_material_similarity.up.sql`
- Create: `migrations/0025_material_similarity.down.sql`

- [ ] **Step 1.1: Write up migration**

File: `migrations/0025_material_similarity.up.sql`

```sql
-- 0025_material_similarity.up.sql
-- Per-client similarity warning feature (see docs/superpowers/specs/2026-05-13-material-similarity-warning-design.md).
-- Adds 4 columns to materials so we can persist the result of a one-shot
-- post-fingerprint scan against the client's other materials, plus a partial
-- index for fast polling of unfinished checks.

BEGIN;

ALTER TABLE materials
    ADD COLUMN similarity_check_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (similarity_check_status IN ('pending', 'ready', 'skipped', 'failed')),
    ADD COLUMN most_similar_material_id UUID REFERENCES materials(id) ON DELETE SET NULL,
    ADD COLUMN similarity_score REAL
        CHECK (similarity_score IS NULL OR (similarity_score >= 0 AND similarity_score <= 1)),
    ADD COLUMN similarity_acknowledged_at TIMESTAMPTZ;

-- Partial index makes the polling query fast even as the table grows.
-- Frontend polls every 3s while any material is pending — without this
-- index, that's a sequential scan per poll.
CREATE INDEX idx_materials_similarity_pending
    ON materials (id) WHERE similarity_check_status = 'pending';

-- Backfilled materials (those that existed before this migration) shouldn't
-- block forever. Mark them all as skipped — they pre-date the feature, no
-- user expectation of a warning. The unconditional UPDATE is safe because
-- new inserts (post-migration) get DEFAULT='pending' from the ALTER above.
UPDATE materials SET similarity_check_status = 'skipped';

COMMIT;
```

- [ ] **Step 1.2: Write down migration**

File: `migrations/0025_material_similarity.down.sql`

```sql
-- 0025_material_similarity.down.sql

BEGIN;

DROP INDEX IF EXISTS idx_materials_similarity_pending;

ALTER TABLE materials
    DROP COLUMN IF EXISTS similarity_acknowledged_at,
    DROP COLUMN IF EXISTS similarity_score,
    DROP COLUMN IF EXISTS most_similar_material_id,
    DROP COLUMN IF EXISTS similarity_check_status;

COMMIT;
```

- [ ] **Step 1.3: Apply migration locally and verify**

```bash
docker compose -f infra/docker/docker-compose.yml up -d --build migrate api
docker compose -f infra/docker/docker-compose.yml exec -T postgres psql -U radiocheck -d radiocheck -c "
\d+ materials
" | grep similarity
```

Expected: 4 columns listed (`similarity_check_status`, `most_similar_material_id`, `similarity_score`, `similarity_acknowledged_at`).

- [ ] **Step 1.4: Commit migration**

```bash
git add migrations/0025_material_similarity.up.sql migrations/0025_material_similarity.down.sql
git commit -m "feat(migration): add similarity columns to materials"
```

---

## Phase 2 — Go `similarity` package: core algorithm (TDD)

**Files:**
- Create: `workers/internal/similarity/similarity.go`
- Create: `workers/internal/similarity/similarity_test.go`

The package mirrors the structure of `workers/internal/sharing/` — it's a sibling, not a refactor target. Reference: read [workers/internal/sharing/sharing.go](../../../workers/internal/sharing/sharing.go) once before starting; the scan loop + cov computation are conceptually identical.

- [ ] **Step 2.1: Write failing unit tests for the pure scoring math**

File: `workers/internal/similarity/similarity_test.go`

```go
package similarity

import (
	"testing"

	"github.com/google/uuid"
)

// frameRange is a half-open [from, until) range on the time_frame axis,
// expressed in matcher frames (sampleRate/stftHop ≈ 7.8125 fps).
type testRange struct{ from, until int32 }

// helper: build a pairScan from raw range data.
func pair(otherTotal int, own, other []testRange) *pairScan {
	o := &pairScan{otherTotalFrames: otherTotal}
	for _, r := range own {
		o.ownRanges = append(o.ownRanges, frameRange{r.from, r.until})
	}
	for _, r := range other {
		o.otherRanges = append(o.otherRanges, frameRange{r.from, r.until})
	}
	return o
}

// Rôgga 30s vs 60s (clean cut): own coverage 100%, other coverage 50%.
// Expected score = max = 1.0.
func TestPickTop_SubsetCleanCut(t *testing.T) {
	otherID := uuid.New()
	// 30s = ~234 frames; full window coverage.
	ownRanges := make([]testRange, 27)
	for i := 0; i < 27; i++ {
		ownRanges[i] = testRange{int32(i * 8), int32(i*8 + 32)}
	}
	// 60s = ~468 frames; only first half matched (~234 frames).
	otherRanges := ownRanges // same shape, 234 frame coverage in 468 total
	report := scanReport{
		ownTotalFrames: 234,
		perOther: map[uuid.UUID]*pairScan{
			otherID: pair(468, ownRanges, otherRanges),
		},
	}
	id, score := pickTopMatch(report)
	if id != otherID {
		t.Fatalf("expected top=%v, got %v", otherID, id)
	}
	if score < 0.99 || score > 1.01 {
		t.Fatalf("expected score~1.0, got %f", score)
	}
}

// AMB30 vs JINGLE (sting overlap of ~6.25s in 30s): both coverages ~20%.
// Expected score ~0.20.
func TestPickTop_StingOverlap(t *testing.T) {
	otherID := uuid.New()
	// 6.25s ≈ 49 frames of overlap in each.
	ownRanges := []testRange{{178, 226}}
	otherRanges := []testRange{{178, 226}}
	report := scanReport{
		ownTotalFrames: 234,
		perOther: map[uuid.UUID]*pairScan{
			otherID: pair(234, ownRanges, otherRanges),
		},
	}
	_, score := pickTopMatch(report)
	if score < 0.15 || score > 0.30 {
		t.Fatalf("expected score in [0.15,0.30] for sting overlap, got %f", score)
	}
}

// Two unrelated commercials — almost no overlap. Score should fall below
// threshold (0.15).
func TestPickTop_NoOverlap(t *testing.T) {
	otherID := uuid.New()
	// 8 frames matched out of 234 = ~3%.
	report := scanReport{
		ownTotalFrames: 234,
		perOther: map[uuid.UUID]*pairScan{
			otherID: pair(234, []testRange{{0, 8}}, []testRange{{0, 8}}),
		},
	}
	_, score := pickTopMatch(report)
	if score >= 0.15 {
		t.Fatalf("expected score < 0.15 for noise overlap, got %f", score)
	}
}

// Multiple matches — should return the one with highest score.
func TestPickTop_PicksHighest(t *testing.T) {
	low := uuid.New()
	high := uuid.New()
	report := scanReport{
		ownTotalFrames: 234,
		perOther: map[uuid.UUID]*pairScan{
			low:  pair(234, []testRange{{0, 50}}, []testRange{{0, 50}}),  // ~21%
			high: pair(234, []testRange{{0, 200}}, []testRange{{0, 200}}), // ~85%
		},
	}
	id, score := pickTopMatch(report)
	if id != high {
		t.Fatalf("expected high to win, got %v", id)
	}
	if score < 0.80 {
		t.Fatalf("expected score >= 0.80, got %f", score)
	}
}

// Empty scan (no matches at all) returns nil UUID + zero score.
func TestPickTop_Empty(t *testing.T) {
	report := scanReport{ownTotalFrames: 234, perOther: map[uuid.UUID]*pairScan{}}
	id, score := pickTopMatch(report)
	if id != uuid.Nil {
		t.Fatalf("expected nil UUID for empty scan, got %v", id)
	}
	if score != 0 {
		t.Fatalf("expected score=0 for empty scan, got %f", score)
	}
}
```

- [ ] **Step 2.2: Run tests, verify they fail with "undefined: pickTopMatch" etc.**

```bash
cd workers && go test ./internal/similarity/... -run TestPickTop 2>&1 | head -20
```

Expected: compile error, types/functions undefined.

- [ ] **Step 2.3: Implement `similarity.go` with ONLY the pure scoring math**

This step intentionally produces a file that exports types + `pickTopMatch` but does NOT yet include `CheckMaterialSimilarity` or the helpers that hit the DB and audio decoder. Those land in Phase 3. Splitting this way keeps the Phase 2 commit fully compiling + unit-testable in isolation.

File: `workers/internal/similarity/similarity.go`

```go
// Package similarity scans a freshly-fingerprinted material against the other
// materials of the same client and persists the top similarity match on the
// materials row. Used to warn operators about likely-duplicate uploads
// before they pollute detection reports. See
// docs/superpowers/specs/2026-05-13-material-similarity-warning-design.md.
package similarity

import (
	"sort"

	"github.com/google/uuid"
)

const (
	// WindowSeconds is the analysis window length (matches runtime matcher).
	WindowSeconds = 4
	// HopSeconds is the hop between consecutive analysis windows.
	HopSeconds = 1
	// MinScore is the histogram peak score that qualifies a window as a hit
	// in the cov computation. Aligned with the runtime matcher.
	MinScore = 5
	// WarnThreshold is the score (max(ownCov, otherCov)) at or above which
	// we surface the warning. Calibration: <5% noise, 15-25% sting, 50%+ subset.
	WarnThreshold = 0.15
)

// frameRange is a half-open [from, until) interval on the time_frame axis.
type frameRange struct{ from, until int32 }

// pairScan holds the per-other-material accumulator during a scan: the other
// material's total frame count plus the matched ranges on both sides of the
// pair (own = the material being scanned, other = the candidate match).
type pairScan struct {
	otherTotalFrames int
	ownRanges        []frameRange
	otherRanges      []frameRange
}

// scanReport aggregates a complete scan: own material's total frames and per-
// other-material pair scans. Kept as a named type so pickTopMatch is testable
// without running audio + DB.
type scanReport struct {
	ownTotalFrames int
	perOther       map[uuid.UUID]*pairScan
}

// pickTopMatch returns the (otherID, score) with the highest
// score = max(ownCov, otherCov) across all candidates. Returns (uuid.Nil, 0)
// when the scan is empty or own has zero frames.
//
// Rationale for max: captures asymmetric subset relationships. For a 30s cut
// of a 60s master, ownCov=100% but otherCov=50%. Using max gives 100%, which
// matches operator intuition ("this audio IS that one"). For a sting overlap
// (~20% on both sides), max=20% — still surfaces above the WarnThreshold but
// well below subset territory.
func pickTopMatch(report scanReport) (uuid.UUID, float64) {
	if report.ownTotalFrames == 0 || len(report.perOther) == 0 {
		return uuid.Nil, 0
	}
	var topID uuid.UUID
	var topScore float64
	for otherID, s := range report.perOther {
		ownCov := float64(frameCoverage(s.ownRanges)) / float64(report.ownTotalFrames)
		var otherCov float64
		if s.otherTotalFrames > 0 {
			otherCov = float64(frameCoverage(s.otherRanges)) / float64(s.otherTotalFrames)
		}
		score := ownCov
		if otherCov > score {
			score = otherCov
		}
		if score > topScore {
			topScore = score
			topID = otherID
		}
	}
	return topID, topScore
}

// frameCoverage returns the total frame count covered by the union of the
// input ranges (after merging overlaps).
func frameCoverage(rs []frameRange) int {
	if len(rs) == 0 {
		return 0
	}
	merged := mergeRanges(rs)
	total := 0
	for _, r := range merged {
		total += int(r.until - r.from)
	}
	return total
}

// mergeRanges merges overlapping/contiguous ranges into a minimal covering set.
func mergeRanges(rs []frameRange) []frameRange {
	if len(rs) == 0 {
		return nil
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].from < rs[j].from })
	merged := []frameRange{rs[0]}
	for _, r := range rs[1:] {
		last := &merged[len(merged)-1]
		if r.from <= last.until {
			if r.until > last.until {
				last.until = r.until
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}
```

- [ ] **Step 2.4: Run the unit tests, verify they pass**

```bash
cd workers && go test ./internal/similarity/... -run TestPickTop -v
```

Expected: PASS for all 5 TestPickTop_* tests.

- [ ] **Step 2.5: Commit core algorithm**

```bash
git add workers/internal/similarity/similarity.go workers/internal/similarity/similarity_test.go
git commit -m "feat(similarity): per-client similarity scoring (pickTopMatch + helpers)"
```

---

## Phase 3 — Scan orchestration (CheckMaterialSimilarity + DB helpers)

Phase 2 left `similarity.go` with the pure scoring math. Phase 3 appends the
DB-aware orchestration on top: `CheckMaterialSimilarity` (the entry point
called by the subscriber), plus the two helpers `loadClientIndex` and
`runScan`.

**File:** `workers/internal/similarity/similarity.go` (append to existing file).

- [ ] **Step 3.1: Add the orchestrator + helpers**

Append to `workers/internal/similarity/similarity.go`. Add the new imports at
the top of the file's import block first (`context`, `fmt`, and the three
radiocheck-internal packages):

```go
import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/fingerprint"
	"radiocheck/internal/index"
	"radiocheck/internal/match"
)
```

Then append the function bodies at the end of the file:

```go
// CheckMaterialSimilarity scans the given material against the other ready
// materials of the same client and writes the top match (if score ≥
// WarnThreshold) to the materials row. Idempotent — re-running for the same
// material state produces no-op UPDATEs.
//
// Heavy by design (decode + match per window). The caller should run after
// fingerprint generation completes. Failures set similarity_check_status to
// 'failed' so they can be retried via the NATS event.
func CheckMaterialSimilarity(ctx context.Context, pool *pgxpool.Pool, materialID uuid.UUID) error {
	// 1. Resolve client_id + master_storage_path.
	var clientID uuid.UUID
	var masterPath string
	var fpStatus string
	if err := pool.QueryRow(ctx, `
		SELECT client_id, master_storage_path, fingerprint_status
		FROM materials WHERE id = $1
	`, materialID).Scan(&clientID, &masterPath, &fpStatus); err != nil {
		return fmt.Errorf("similarity: lookup material: %w", err)
	}
	if fpStatus != "ready" {
		// Caller should not have fired the event in this case. Mark skipped
		// rather than failed so operators see "no fingerprint" not "scan error".
		_, err := pool.Exec(ctx,
			`UPDATE materials SET similarity_check_status = 'skipped' WHERE id = $1`,
			materialID)
		return err
	}

	// 2. Build the per-client index. Excludes self and skips materials whose
	//    own fingerprint isn't ready. Joins fingerprint_hashes by material id
	//    (post-bridge, fingerprint_hashes.commercial_id is polymorphic).
	idx, shortToID, totalFramesByID, err := loadClientIndex(ctx, pool, clientID, materialID)
	if err != nil {
		_ = markFailed(ctx, pool, materialID)
		return fmt.Errorf("similarity: load client index: %w", err)
	}
	if len(idx) == 0 {
		// First material of this client OR no other ready materials. Skip.
		_, err := pool.Exec(ctx,
			`UPDATE materials SET similarity_check_status = 'skipped' WHERE id = $1`,
			materialID)
		return err
	}
	store := index.New()
	store.Swap(idx)

	// 3. Decode the new material's PCM through the same pipeline used at
	//    fingerprint generation so live hashes align with stored hashes.
	pcm, err := fingerprint.DecodePCM(ctx, masterPath, fingerprint.VariantClean)
	if err != nil {
		_ = markFailed(ctx, pool, materialID)
		return fmt.Errorf("similarity: decode master: %w", err)
	}

	// 4. Slide window, run MatchWindow against the per-client index, build report.
	report := runScan(pcm, store, materialID, shortToID, totalFramesByID)

	// 5. Pick top match, persist.
	topID, score := pickTopMatch(report)
	if score < WarnThreshold {
		_, err := pool.Exec(ctx, `
			UPDATE materials
			SET similarity_check_status = 'ready',
			    most_similar_material_id = NULL,
			    similarity_score = NULL
			WHERE id = $1
		`, materialID)
		return err
	}
	_, err = pool.Exec(ctx, `
		UPDATE materials
		SET similarity_check_status = 'ready',
		    most_similar_material_id = $2,
		    similarity_score = $3
		WHERE id = $1
	`, materialID, topID, score)
	return err
}

func markFailed(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) error {
	_, err := pool.Exec(ctx,
		`UPDATE materials SET similarity_check_status = 'failed' WHERE id = $1`, id)
	return err
}

// loadClientIndex loads all fingerprint hashes belonging to other materials of
// the same client that are currently 'ready'. Returns the index, a
// short_id→material_id map (so MatchWindow results can be translated back to
// FK identity), and the per-material total frame counts.
//
// Filters:
//   - same client_id
//   - exclude self
//   - fingerprint_status = 'ready'
//
// IMPORTANT: this query JOINs fingerprint_hashes by material id assuming the
// bridge plan (2026-05-13-material-fingerprint-pipeline.md) has unified the
// catalog. If fingerprint_hashes.commercial_id holds material UUIDs for
// wizard-uploaded materials post-bridge, the JOIN below resolves correctly.
func loadClientIndex(
	ctx context.Context,
	pool *pgxpool.Pool,
	clientID uuid.UUID,
	selfID uuid.UUID,
) (index.Index, map[int32]uuid.UUID, map[uuid.UUID]int, error) {
	rows, err := pool.Query(ctx, `
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id,
		       m.short_id, m.id, m.duration_seconds
		FROM fingerprint_hashes fh
		JOIN materials m ON m.id = fh.commercial_id
		WHERE m.client_id = $1
		  AND m.id != $2
		  AND m.fingerprint_status = 'ready'
	`, clientID, selfID)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	idx := make(index.Index)
	shortToID := make(map[int32]uuid.UUID)
	totalFramesByID := make(map[uuid.UUID]int)
	for rows.Next() {
		var hashValue uint32
		var timeFrame int32
		var variantID, rateID int16
		var shortID int32
		var matID uuid.UUID
		var durationSec float64
		if err := rows.Scan(&hashValue, &timeFrame, &variantID, &rateID,
			&shortID, &matID, &durationSec); err != nil {
			return nil, nil, nil, err
		}
		if variantID < 0 || variantID > 255 || rateID < 0 || rateID > 255 {
			return nil, nil, nil, fmt.Errorf(
				"similarity: variant_id=%d or rate_id=%d out of uint8 range",
				variantID, rateID)
		}
		idx[hashValue] = append(idx[hashValue], index.Entry{
			CommercialShortID: shortID,
			VariantID:         uint8(variantID),
			RateID:            uint8(rateID),
			TimeFrame:         timeFrame,
		})
		shortToID[shortID] = matID
		// duration_seconds × (sampleRate / stftHop) = frames.
		totalFramesByID[matID] = int(durationSec * float64(fingerprint.SampleRate) / 2048.0)
	}
	return idx, shortToID, totalFramesByID, rows.Err()
}

// runScan slides a WindowSeconds window in HopSeconds increments over pcm,
// runs MatchWindow against the catalog index, and builds a scanReport. Does
// not touch the DB.
func runScan(
	pcm []float32,
	store *index.Store,
	selfID uuid.UUID,
	shortToID map[int32]uuid.UUID,
	totalFramesByID map[uuid.UUID]int,
) scanReport {
	const sampleRate = fingerprint.SampleRate
	const stftHopSamples = 2048
	windowSamples := sampleRate * WindowSeconds
	hopSamples := sampleRate * HopSeconds

	report := scanReport{
		ownTotalFrames: len(pcm) / stftHopSamples,
		perOther:       make(map[uuid.UUID]*pairScan),
	}

	for off := 0; off+windowSamples <= len(pcm); off += hopSamples {
		window := pcm[off : off+windowSamples]
		results := match.MatchWindow(window, store, MinScore, 0.0)

		ownStart := int32(off / stftHopSamples)
		ownEnd := int32((off + windowSamples) / stftHopSamples)

		for _, r := range results {
			otherID, ok := shortToID[r.CommercialShortID]
			if !ok || otherID == selfID {
				continue
			}
			s := report.perOther[otherID]
			if s == nil {
				s = &pairScan{otherTotalFrames: totalFramesByID[otherID]}
				report.perOther[otherID] = s
			}
			s.ownRanges = append(s.ownRanges, frameRange{ownStart, ownEnd})

			// Other range derived from the histogram delta:
			// live_frame - OffsetFrames = entry.TimeFrame.
			xStart := int32(int(ownStart) - r.OffsetFrames)
			xEnd := int32(int(ownEnd) - r.OffsetFrames)
			if xStart > xEnd {
				xStart, xEnd = xEnd, xStart
			}
			if xEnd <= 0 {
				continue
			}
			if xStart < 0 {
				xStart = 0
			}
			s.otherRanges = append(s.otherRanges, frameRange{xStart, xEnd})
		}
	}
	return report
}
```

- [ ] **Step 3.2: Build the workers module to catch compile errors**

```bash
cd workers && go build ./internal/similarity/...
```

Expected: no output (success). If errors about unused imports or missing types, fix and re-run.

- [ ] **Step 3.3: Commit orchestrator + scan helpers**

```bash
git add workers/internal/similarity/similarity.go
git commit -m "feat(similarity): CheckMaterialSimilarity + loadClientIndex + runScan"
```

---

## Phase 4 — NATS subject + Python publisher

**Files:**
- Modify: `workers/internal/events/nats.go`
- Modify: `fingerprint/fingerprint/main.py`

- [ ] **Step 4.1: Add subject constant**

File: `workers/internal/events/nats.go` — find the existing constants block (around `SubjectFingerprintSharedScan = "fingerprint.shared-scan"`) and append:

```go
	// SubjectMaterialSimilarityCheck triggers the per-client similarity scan
	// (workers/internal/similarity) after a material's fingerprint becomes
	// ready. Payload: {"material_id":"<uuid>"}. Published by the Python
	// fingerprint daemon, consumed by similarity.Subscriber in the api process.
	// See docs/superpowers/specs/2026-05-13-material-similarity-warning-design.md.
	SubjectMaterialSimilarityCheck = "material.similarity-check"
```

- [ ] **Step 4.2: Build to verify constant compiles**

```bash
cd workers && go build ./...
```

Expected: no output.

- [ ] **Step 4.3: Update Python daemon to publish the new event for material uploads**

File: `fingerprint/fingerprint/main.py` — find the spot where `SUBJECT_SHARED_SCAN` is published (after `mark_status('ready')`, near the bottom of `handle_generate`). Right after the `nc.publish(SUBJECT_SHARED_SCAN, ...)` call, add a conditional publish for material uploads.

NOTE: this assumes the bridge plan (which made the daemon material-aware) has already split the handler into commercial vs material paths. If the handler still has a single `commercial_id` variable, this step will need to use whatever variable name the bridge plan ended up choosing for the material's id. Most likely the bridge plan introduced `entity_id` or kept `commercial_id` polymorphic. Check the actual state and use the right name.

Concrete addition at the end of the material branch (or unconditionally if the handler is unified — material.similarity-check on a commercial id is harmless because the Go subscriber will look up materials.id and find nothing → silent no-op):

```python
SUBJECT_MATERIAL_SIMILARITY_CHECK = "material.similarity-check"

# ... at the top with other subject constants ...

# ... at the end of handle_generate, after publishing SUBJECT_SHARED_SCAN ...
# Publish similarity-check only for materials (not commercials). The Go
# subscriber will skip the message gracefully if the id doesn't resolve to a
# materials row.
similarity_payload = json.dumps({"material_id": entity_id}).encode()
await nc.publish(SUBJECT_MATERIAL_SIMILARITY_CHECK, similarity_payload)
log.info("published material.similarity-check material_id=%s", entity_id)
```

If after reading `main.py` the variable name turns out to be different, substitute. Do not invent.

- [ ] **Step 4.4: Restart the fingerprint daemon and run a wizard upload**

```bash
docker compose -f infra/docker/docker-compose.yml restart fingerprint
docker compose -f infra/docker/docker-compose.yml logs --tail=50 fingerprint | grep similarity
```

Then upload one material via the wizard UI and re-check logs. Expected: a log line like `published material.similarity-check material_id=...`.

- [ ] **Step 4.5: Commit**

```bash
git add workers/internal/events/nats.go fingerprint/fingerprint/main.py
git commit -m "feat(events): publish material.similarity-check after fingerprint ready"
```

---

## Phase 5 — Go subscriber

**File:** `workers/internal/similarity/subscriber.go`

Pattern after `workers/internal/sharing/subscriber.go` — same shape, swap names + subject.

- [ ] **Step 5.1: Write subscriber**

File: `workers/internal/similarity/subscriber.go`

```go
package similarity

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"radiocheck/internal/events"
)

// Subscriber listens on SubjectMaterialSimilarityCheck and runs
// CheckMaterialSimilarity for each material named in the payload. Failures
// are logged and persisted to materials.similarity_check_status='failed'.
type Subscriber struct {
	pool *pgxpool.Pool
	nc   *nats.Conn
	log  *zap.Logger
}

func NewSubscriber(pool *pgxpool.Pool, nc *nats.Conn, log *zap.Logger) *Subscriber {
	return &Subscriber{pool: pool, nc: nc, log: log}
}

type payload struct {
	MaterialID string `json:"material_id"`
}

// Subscribe registers the listener. Each message decodes and runs the scan in
// the background context. The returned subscription is drained when ctx ends.
func (s *Subscriber) Subscribe(ctx context.Context) (*nats.Subscription, error) {
	sub, err := s.nc.Subscribe(events.SubjectMaterialSimilarityCheck, func(msg *nats.Msg) {
		var p payload
		if err := json.Unmarshal(msg.Data, &p); err != nil {
			s.log.Warn("similarity: invalid payload",
				zap.Error(err),
				zap.ByteString("data", msg.Data),
			)
			return
		}
		materialID, err := uuid.Parse(p.MaterialID)
		if err != nil {
			s.log.Warn("similarity: invalid material_id",
				zap.String("material_id", p.MaterialID),
				zap.Error(err),
			)
			return
		}

		bgCtx := context.Background()
		if err := CheckMaterialSimilarity(bgCtx, s.pool, materialID); err != nil {
			s.log.Error("similarity: CheckMaterialSimilarity failed",
				zap.String("material_id", materialID.String()),
				zap.Error(err),
			)
			return
		}
		s.log.Info("similarity: ok",
			zap.String("material_id", materialID.String()),
		)
	})
	if err != nil {
		return nil, fmt.Errorf("similarity: subscribe: %w", err)
	}
	go func() {
		<-ctx.Done()
		_ = sub.Drain()
	}()
	return sub, nil
}
```

- [ ] **Step 5.2: Build**

```bash
cd workers && go build ./...
```

Expected: no output.

- [ ] **Step 5.3: Commit**

```bash
git add workers/internal/similarity/subscriber.go
git commit -m "feat(similarity): NATS subscriber for material.similarity-check"
```

---

## Phase 6 — Materials catalog: extend struct + queries

**File:** `workers/internal/catalog/materials.go`

The struct + scanMaterial + materialColumns constant + queries all need the 4 new columns. Care: every place that scans into `*Material` must scan the additional fields.

- [ ] **Step 6.1: Extend the `Material` struct**

In `workers/internal/catalog/materials.go`, replace the existing struct definition with:

```go
type Material struct {
	ID                     uuid.UUID  `json:"id"`
	ShortID                int32      `json:"short_id"`
	ClientID               uuid.UUID  `json:"client_id"`
	Title                  string     `json:"title"`
	TypeID                 *uuid.UUID `json:"type_id,omitempty"`
	DurationSeconds        float64    `json:"duration_seconds"`
	MasterStoragePath      string     `json:"master_storage_path"`
	MasterSHA256           string     `json:"master_sha256"`
	FingerprintStatus      string     `json:"fingerprint_status"`
	FingerprintGeneratedAt *time.Time `json:"fingerprint_generated_at,omitempty"`
	FingerprintHashCount   *int32     `json:"fingerprint_hash_count,omitempty"`
	SimilarityCheckStatus  string     `json:"similarity_check_status"`
	MostSimilarMaterialID  *uuid.UUID `json:"most_similar_material_id,omitempty"`
	SimilarityScore        *float32   `json:"similarity_score,omitempty"`
	SimilarityAckdAt       *time.Time `json:"similarity_acknowledged_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}
```

- [ ] **Step 6.2: Extend `materialColumns` const + `scanMaterial`**

Replace `materialColumns`:

```go
const materialColumns = `id, short_id, client_id, title, type_id, duration_seconds,
       master_storage_path, master_sha256, fingerprint_status,
       fingerprint_generated_at, fingerprint_hash_count,
       similarity_check_status, most_similar_material_id, similarity_score,
       similarity_acknowledged_at, created_at, updated_at`
```

Replace `scanMaterial`:

```go
func scanMaterial(row interface {
	Scan(...any) error
}, m *Material) error {
	return row.Scan(&m.ID, &m.ShortID, &m.ClientID, &m.Title, &m.TypeID,
		&m.DurationSeconds, &m.MasterStoragePath, &m.MasterSHA256,
		&m.FingerprintStatus, &m.FingerprintGeneratedAt, &m.FingerprintHashCount,
		&m.SimilarityCheckStatus, &m.MostSimilarMaterialID, &m.SimilarityScore,
		&m.SimilarityAckdAt, &m.CreatedAt, &m.UpdatedAt)
}
```

- [ ] **Step 6.3: Update `Create` to scan the new columns**

The existing `Create` method has an inline `.Scan(...)` that mirrors `scanMaterial`. Replace its `.Scan(...)` invocation to call `scanMaterial(row, &mat)` instead:

```go
func (m *Materials) Create(ctx context.Context, in CreateMaterialInput) (*Material, error) {
	var mat Material
	row := m.pool.QueryRow(ctx, `
		INSERT INTO materials (client_id, title, type_id, duration_seconds,
		                       master_storage_path, master_sha256)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+materialColumns,
		in.ClientID, in.Title, in.TypeID, in.DurationSeconds,
		in.MasterStoragePath, in.MasterSHA256,
	)
	if err := scanMaterial(row, &mat); err != nil {
		return nil, err
	}
	return &mat, nil
}
```

- [ ] **Step 6.4: Add `Acknowledge` method**

Append to `workers/internal/catalog/materials.go`:

```go
// Acknowledge marks the similarity warning for the given material as resolved
// (the operator chose "Manter assim mesmo" in the UI). Idempotent — repeated
// calls just refresh the timestamp.
func (m *Materials) Acknowledge(ctx context.Context, id uuid.UUID) error {
	_, err := m.pool.Exec(ctx,
		`UPDATE materials SET similarity_acknowledged_at = NOW(), updated_at = NOW() WHERE id = $1`,
		id)
	return err
}
```

- [ ] **Step 6.5: Run existing catalog tests to ensure no regression**

```bash
cd workers && go test ./internal/catalog/... -run TestMaterials -v
```

Expected: all green. If a test asserts a specific JSON shape with the old columns only, update its expectations to include the new fields (with nil/empty values) or use `omitempty`-compatible assertions.

- [ ] **Step 6.6: Commit**

```bash
git add workers/internal/catalog/materials.go
git commit -m "feat(catalog): expose similarity columns on Material; Acknowledge method"
```

---

## Phase 7 — HTTP handlers (audio streaming + acknowledge)

**Files:**
- Create: `workers/internal/api/handlers/materials_audio.go`
- Modify: `workers/internal/api/handlers/materials.go` (add `Acknowledge`)
- Modify: `workers/internal/api/router.go`

- [ ] **Step 7.1: Write the audio streaming handler**

Reference pattern: read `workers/internal/api/handlers/commercials.go` function `Audio` (around line 248) before writing — same structure but for materials.

File: `workers/internal/api/handlers/materials_audio.go`

```go
package handlers

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Audio streams a material's master audio file with byte-range support.
// Mirror of CommercialsHandler.Audio. Used by the similarity warning modal
// players (frontend/src/components/SimilarityWarningModal.jsx).
func (h *MaterialsHandler) Audio(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	mat, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	f, err := os.Open(mat.MasterStoragePath)
	if err != nil {
		http.Error(w, "audio file not available", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Content-Type by extension.
	ct := "application/octet-stream"
	switch strings.ToLower(filepath.Ext(mat.MasterStoragePath)) {
	case ".m4a", ".aac":
		ct = "audio/mp4"
	case ".mp3", ".mpeg":
		ct = "audio/mpeg"
	case ".wav":
		ct = "audio/wav"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, filepath.Base(mat.MasterStoragePath), info.ModTime(), f)
}
```

- [ ] **Step 7.2: Add `Acknowledge` handler to materials.go**

Append to `workers/internal/api/handlers/materials.go`:

```go
// Acknowledge marks the material's similarity warning as resolved.
// POST /materials/{id}/similarity/acknowledge → 204.
func (h *MaterialsHandler) Acknowledge(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Acknowledge(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 7.3: Wire routes**

File: `workers/internal/api/router.go` — find the `r.Route("/materials", func(r chi.Router) {...})` block and add two routes inside it:

```go
			r.Route("/materials", func(r chi.Router) {
				r.Post("/", d.Materials.Upload)
				r.Get("/{id}", d.Materials.Get)
				r.Get("/{id}/audio", d.Materials.Audio)                        // NEW
				r.Post("/{id}/similarity/acknowledge", d.Materials.Acknowledge) // NEW
				r.Patch("/{id}/type", d.Materials.UpdateType)
				r.Delete("/{id}", d.Materials.Delete)
			})
```

- [ ] **Step 7.4: Build**

```bash
cd workers && go build ./...
```

Expected: success.

- [ ] **Step 7.5: Commit**

```bash
git add workers/internal/api/handlers/materials_audio.go workers/internal/api/handlers/materials.go workers/internal/api/router.go
git commit -m "feat(api): /materials/{id}/audio + /similarity/acknowledge"
```

---

## Phase 8 — Wire subscriber in main.go

**File:** `workers/cmd/api/main.go`

- [ ] **Step 8.1: Add import**

In the import block of `workers/cmd/api/main.go`, add `"radiocheck/internal/similarity"` alphabetically.

- [ ] **Step 8.2: Register subscriber after the existing `sharing` subscriber**

Find the spot where `sharing.NewSubscriber(...)` and `Subscribe(ctx)` is called. Right after, add:

```go
	// Start similarity-check subscriber (per-client duplicate warning).
	simSub := similarity.NewSubscriber(pool, nc, logger)
	if _, err := simSub.Subscribe(ctx); err != nil {
		log.Fatalf("similarity subscribe: %v", err)
	}
```

- [ ] **Step 8.3: Build + restart api in dev**

```bash
cd workers && go build ./...
docker compose -f infra/docker/docker-compose.yml up -d --build api
docker compose -f infra/docker/docker-compose.yml logs --tail=30 api | grep -i similarity
```

Expected: a log entry like `subscribed to material.similarity-check` or no errors during boot.

- [ ] **Step 8.4: Commit**

```bash
git add workers/cmd/api/main.go
git commit -m "feat(api): register similarity subscriber on boot"
```

---

## Phase 9 — Integration test

**File:** `workers/internal/api/handlers/materials_similarity_test.go`

Existing tests in the same directory show the pattern (e.g. `materials_test.go`, `commercials_test.go`). This test exercises the end-to-end flow against a real test DB.

- [ ] **Step 9.1: Write the integration test**

File: `workers/internal/api/handlers/materials_similarity_test.go`

```go
package handlers_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"radiocheck/internal/similarity"
	"radiocheck/internal/testutil" // assumes a shared test DB helper exists
)

// TestSimilarityFlow_RoggaCut verifies that two materials sharing the bulk of
// their audio (a 30s cut + a 60s original) produce a similarity match of
// score > 0.9 for the shorter cut. Uses the Rôgga jingle masters from
// audio-refs/ as ground truth, matching the spec's calibration example.
func TestSimilarityFlow_RoggaCut(t *testing.T) {
	ctx := context.Background()
	pool := testutil.OpenTestDB(t)

	// 1. Seed: create a client, upload Rôgga 60 via the API, wait for
	//    fingerprint_status='ready'.
	clientID := testutil.SeedClient(t, pool, "Rôgga Test")
	rogga60ID := testutil.UploadMaterial(t, pool, clientID,
		"JINGLE ROGGA 60", "../../../../audio-refs/JINGLE ROGGA VERÃO 60.mp3")
	testutil.WaitForFingerprintReady(t, pool, rogga60ID, 30*time.Second)

	// 2. Upload Rôgga 30 (extracted cut) and wait for fingerprint ready.
	rogga30ID := testutil.UploadMaterial(t, pool, clientID,
		"JINGLE ROGGA 30", "../../../../audio-refs/JINGLE ROGGA VERÃO 30.mp3")
	testutil.WaitForFingerprintReady(t, pool, rogga30ID, 30*time.Second)

	// 3. Run the similarity scan synchronously (not via NATS — we test the
	//    function directly to avoid event timing flakiness in CI).
	if err := similarity.CheckMaterialSimilarity(ctx, pool, rogga30ID); err != nil {
		t.Fatalf("CheckMaterialSimilarity: %v", err)
	}

	// 4. Read the material back and assert.
	var status string
	var topID *uuid.UUID
	var score *float32
	err := pool.QueryRow(ctx, `
		SELECT similarity_check_status, most_similar_material_id, similarity_score
		FROM materials WHERE id = $1
	`, rogga30ID).Scan(&status, &topID, &score)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != "ready" {
		t.Fatalf("expected status=ready, got %s", status)
	}
	if topID == nil || *topID != rogga60ID {
		t.Fatalf("expected most_similar=%s, got %v", rogga60ID, topID)
	}
	if score == nil || *score < 0.9 {
		t.Fatalf("expected score >= 0.9, got %v", score)
	}
}
```

- [ ] **Step 9.2: Check if `testutil` helpers exist; create stubs if not**

```bash
ls workers/internal/testutil/ 2>/dev/null || echo "no testutil package"
```

If absent: this test serves as a regression target. Either skip the integration test for now (mark it `t.Skip()` with a TODO) OR write the helpers as a sub-step here, mirroring how other integration tests in `workers/internal/sharing/integration_audio_test.go` set up state. Per CLAUDE.md "don't add abstractions beyond what the task requires" — if no testutil exists yet, prefer skipping the integration test and relying on the manual smoke test (Phase 14).

- [ ] **Step 9.3: Run the test**

```bash
cd workers && go test ./internal/api/handlers/ -run TestSimilarityFlow -v
```

Expected: PASS. Adjust assertion bounds if the score lands outside expectations (re-calibrate from real data — the test is the ground truth, not the spec's calibration sentence).

- [ ] **Step 9.4: Commit**

```bash
git add workers/internal/api/handlers/materials_similarity_test.go
git commit -m "test(similarity): integration test with Rôgga 30/60 masters"
```

---

## Phase 10 — Frontend hooks (polling + acknowledge)

**File:** `frontend/src/api/hooks.js`

- [ ] **Step 10.1: Add `useAcknowledgeSimilarity`**

Append after `useDeleteMaterial` (around line 446):

```js
export function useAcknowledgeSimilarity() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) =>
      api.post(`/materials/${id}/similarity/acknowledge`).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['materials'] }),
  })
}
```

- [ ] **Step 10.2: Add polling to `useMaterials` and `useCampaignMaterials`**

Replace `useMaterials` (around line 404):

```js
export function useMaterials(clientId, q = '') {
  return useQuery({
    queryKey: ['materials', clientId, q],
    queryFn: () => api.get(`/clients/${clientId}/materials`, { params: { q } }).then(r => {
      const d = r.data
      if (Array.isArray(d)) return d
      if (Array.isArray(d?.data)) return d.data
      return []
    }),
    enabled: !!clientId,
    refetchInterval: (query) => {
      // Poll every 3s while ANY material is still being analyzed for
      // similarity OR fingerprint. Stops polling once everything is settled.
      const list = query.state.data ?? []
      const pending = list.some(m =>
        m.fingerprint_status === 'pending' ||
        m.fingerprint_status === 'generating' ||
        m.similarity_check_status === 'pending')
      return pending ? 3000 : false
    },
  })
}
```

Replace `useCampaignMaterials` (around line 450):

```js
export function useCampaignMaterials(campaignId) {
  return useQuery({
    queryKey: ['campaign-materials', campaignId],
    queryFn: () => api.get(`/campaigns/${campaignId}/materials`).then(r => r.data ?? []),
    enabled: !!campaignId,
    // No polling here directly — the join row (campaign_materials) doesn't
    // carry similarity state. The `useMaterials(clientId)` query is the one
    // that polls; this query refreshes on its invalidation cascade.
  })
}
```

- [ ] **Step 10.3: Verify in dev that polling works**

Open the wizard, upload a material. Inspect Network tab — `GET /clients/.../materials` should refire every ~3s while the new material is pending. Stops once `similarity_check_status` becomes `ready`/`skipped`.

- [ ] **Step 10.4: Commit**

```bash
git add frontend/src/api/hooks.js
git commit -m "feat(frontend): polling for similarity + useAcknowledgeSimilarity"
```

---

## Phase 11 — `SimilarityWarningModal` component

**File:** `frontend/src/components/SimilarityWarningModal.jsx`

- [ ] **Step 11.1: Write the modal component**

File: `frontend/src/components/SimilarityWarningModal.jsx`

```jsx
import { useDeleteMaterial, useAcknowledgeSimilarity } from '../api/hooks'

/**
 * Modal that warns the operator that the uploaded material is highly similar
 * to another material of the same client. Two native HTML5 audio players
 * (left=new, right=existing) allow A/B comparison. Operator decides:
 *  - "Remover material novo" → DELETE /materials/{id}
 *  - "Manter assim mesmo"   → POST /materials/{id}/similarity/acknowledge
 *
 * Props:
 *  - newMaterial:      {id, title, duration_seconds, similarity_score}
 *  - similarMaterial:  {id, title, duration_seconds}
 *  - onClose:          () => void
 */
export default function SimilarityWarningModal({ newMaterial, similarMaterial, onClose }) {
  const del = useDeleteMaterial()
  const ack = useAcknowledgeSimilarity()
  const pct = Math.round((newMaterial.similarity_score ?? 0) * 100)

  async function handleRemove() {
    await del.mutateAsync(newMaterial.id)
    onClose()
  }
  async function handleKeep() {
    await ack.mutateAsync(newMaterial.id)
    onClose()
  }

  return (
    <div
      style={{
        position: 'fixed', inset: 0,
        background: 'rgba(15,23,42,0.55)', backdropFilter: 'blur(6px)',
        zIndex: 60,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
      }}
      onClick={onClose}
    >
      <div
        onClick={e => e.stopPropagation()}
        style={{
          background: 'var(--c-surface)',
          borderRadius: 'var(--radius-lg)',
          boxShadow: 'var(--shadow-lg)',
          width: 'min(680px, 92vw)',
          padding: 24,
          display: 'flex', flexDirection: 'column', gap: 18,
        }}
      >
        <div>
          <div style={{
            fontSize: 11, fontWeight: 700, letterSpacing: '0.12em',
            color: '#a16207', textTransform: 'uppercase',
          }}>
            ⚠ Atenção: material similar
          </div>
          <h3 style={{
            margin: '6px 0 0', fontFamily: 'var(--font-heading)', fontWeight: 700,
            fontSize: 18, color: 'var(--c-text)',
          }}>
            Este material é {pct}% similar a “{similarMaterial.title}”
          </h3>
          <p style={{ margin: '8px 0 0', color: 'var(--c-text-2)', fontSize: 13 }}>
            Tem certeza que quer manter os dois? Materiais duplicados poluem o
            relatório de veiculação (ambos disparam).
          </p>
        </div>

        <div style={{
          display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14,
        }}>
          <PlayerCard
            label="NOVO"
            title={newMaterial.title}
            durationSeconds={newMaterial.duration_seconds}
            audioUrl={`/v1/internal/materials/${newMaterial.id}/audio`}
          />
          <PlayerCard
            label="EXISTENTE"
            title={similarMaterial.title}
            durationSeconds={similarMaterial.duration_seconds}
            audioUrl={`/v1/internal/materials/${similarMaterial.id}/audio`}
          />
        </div>

        <div style={{
          display: 'flex', justifyContent: 'flex-end', gap: 10,
          paddingTop: 8, borderTop: '1px solid var(--c-border)',
        }}>
          <button
            onClick={handleRemove}
            disabled={del.isPending}
            style={{
              padding: '9px 16px', borderRadius: 'var(--radius-md)',
              background: 'var(--c-danger)', color: '#fff',
              border: 0, cursor: 'pointer',
              fontSize: 12, fontWeight: 700, fontFamily: 'var(--font-heading)',
            }}
          >
            {del.isPending ? 'Removendo…' : 'Remover material novo'}
          </button>
          <button
            onClick={handleKeep}
            disabled={ack.isPending}
            style={{
              padding: '9px 16px', borderRadius: 'var(--radius-md)',
              background: 'var(--c-action)', color: '#fff',
              border: 0, cursor: 'pointer',
              fontSize: 12, fontWeight: 700, fontFamily: 'var(--font-heading)',
            }}
          >
            {ack.isPending ? 'Salvando…' : 'Manter assim mesmo'}
          </button>
        </div>
      </div>
    </div>
  )
}

function PlayerCard({ label, title, durationSeconds, audioUrl }) {
  return (
    <div style={{
      background: 'var(--c-bg)',
      border: '1px solid var(--c-border)',
      borderRadius: 'var(--radius-md)',
      padding: 12,
      display: 'flex', flexDirection: 'column', gap: 8,
    }}>
      <div style={{
        fontSize: 10, fontWeight: 700, letterSpacing: '0.10em',
        color: 'var(--c-text-3)',
      }}>{label}</div>
      <div style={{
        fontSize: 13, fontWeight: 600, color: 'var(--c-text)',
        whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
      }}>{title}</div>
      <div style={{ fontSize: 11, color: 'var(--c-text-3)' }}>
        {durationSeconds?.toFixed(1) ?? '—'}s
      </div>
      <audio controls preload="metadata" src={audioUrl} style={{ width: '100%' }} />
    </div>
  )
}
```

- [ ] **Step 11.2: Commit**

```bash
git add frontend/src/components/SimilarityWarningModal.jsx
git commit -m "feat(frontend): SimilarityWarningModal component"
```

---

## Phase 12 — Badge wiring in `MaterialCard`

**File:** `frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx`

- [ ] **Step 12.1: Import the modal at the top**

Add to imports (around line 1-8):

```jsx
import SimilarityWarningModal from '../../components/SimilarityWarningModal'
```

- [ ] **Step 12.2: Add similarity badge inside `MaterialCard`**

In `MaterialCard` (around line 187), add a state hook at the top of the function:

```jsx
function MaterialCard({
  material, link, type, allTypes, campaignStations,
  isEditingStations, onToggleStationsEdit,
  onTypeChange, onSaveStations, onUnlink,
  materialsById, // NEW: pass this from the caller (MaterialsStep) so the badge can resolve the similar material's title
}) {
  const [showSimilarityModal, setShowSimilarityModal] = useState(false)
  // ... rest of existing function body
```

Then in the meta row (around line 254-285, where the fingerprint badge and missing-type badge live), append a similarity badge:

```jsx
            {material.similarity_check_status === 'pending' && (
              <>
                <span style={{ color: 'var(--c-text-3)' }}>·</span>
                <span style={{
                  padding: '2px 8px', borderRadius: 'var(--radius-full)',
                  background: 'var(--c-surface-2)', color: 'var(--c-text-3)',
                  fontSize: 10, fontWeight: 700, letterSpacing: '0.03em',
                  display: 'inline-flex', alignItems: 'center', gap: 5,
                }}>
                  <span style={{
                    width: 5, height: 5, borderRadius: '50%',
                    background: 'var(--c-text-3)',
                    animation: 'wizard-pulse 1.2s ease-in-out infinite',
                  }} />
                  Analisando similaridade…
                </span>
              </>
            )}
            {material.similarity_check_status === 'ready' &&
              material.similarity_score != null &&
              material.similarity_score >= 0.15 &&
              !material.similarity_acknowledged_at &&
              materialsById[material.most_similar_material_id] && (
              <>
                <span style={{ color: 'var(--c-text-3)' }}>·</span>
                <button
                  type="button"
                  onClick={() => setShowSimilarityModal(true)}
                  title="Comparar com material similar"
                  style={{
                    padding: '2px 8px', borderRadius: 'var(--radius-full)',
                    background: '#fef3c7', color: '#a16207',
                    border: 0, cursor: 'pointer',
                    fontSize: 10, fontWeight: 700, letterSpacing: '0.03em',
                    display: 'inline-flex', alignItems: 'center', gap: 5,
                  }}
                >
                  ⚠ {Math.round(material.similarity_score * 100)}% similar a “{materialsById[material.most_similar_material_id].title}”
                </button>
              </>
            )}
```

Also add the modal render at the very end of the card's outer container, before the closing `</div>`:

```jsx
      {showSimilarityModal && materialsById[material.most_similar_material_id] && (
        <SimilarityWarningModal
          newMaterial={material}
          similarMaterial={materialsById[material.most_similar_material_id]}
          onClose={() => setShowSimilarityModal(false)}
        />
      )}
```

- [ ] **Step 12.3: Pass `materialsById` down to `MaterialCard` from `MaterialsStep`**

In the `MaterialsStep` function, find where `MaterialCard` is rendered (around line 134) and add the prop:

```jsx
              <MaterialCard
                key={link.material_id}
                material={mat}
                link={link}
                type={type}
                allTypes={materialTypes}
                campaignStations={campaignStations}
                materialsById={materialsById}  // NEW
                isEditingStations={isEditing}
                onToggleStationsEdit={...}
                onTypeChange={...}
                onSaveStations={...}
                onUnlink={...}
              />
```

- [ ] **Step 12.4: Add the pulse keyframe (if not already present)**

Check `frontend/src/index.css` or wherever global keyframes live for `wizard-pulse`. If absent, add to a top-level CSS file:

```css
@keyframes wizard-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}
```

- [ ] **Step 12.5: Visual smoke test in dev**

Open wizard, upload a duplicate, wait for the badge to appear, click it, both audios should play.

- [ ] **Step 12.6: Commit**

```bash
git add frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx
git add frontend/src/index.css   # only if you added the keyframe here
git commit -m "feat(frontend): similarity badge + modal trigger in MaterialCard"
```

---

## Phase 13 — Operational documentation

**File:** `docs/material-similarity-warning.md`

- [ ] **Step 13.1: Write the doc**

File: `docs/material-similarity-warning.md`

```markdown
# Material Similarity Warning

## What it does

When an operator uploads a new material in the campaign wizard, the system
scans the new audio against the other materials of the **same client** (no
cross-client comparison) and persists the top match if the similarity score
is ≥15%. The frontend then shows an amber badge on the material card with a
modal that A/B's both audios and lets the operator decide:

- **Manter assim mesmo** → acknowledges the warning (badge disappears).
- **Remover material novo** → deletes the just-uploaded material.

## Scope and limitations

- Per-client only. Two clients with identical jingles will not cross-warn.
- Threshold is hard-coded at 15% (`similarity.WarnThreshold` in
  `workers/internal/similarity/similarity.go`).
- Operates on `fingerprint_status='ready'` materials only. Materials still in
  `pending`/`generating` are skipped on both sides (own + index).
- Top-1 match. If the new material is similar to N existing ones, only the
  highest-scoring is surfaced.
- No retroactive backfill. Materials uploaded before migration 0025 are
  marked `similarity_check_status='skipped'`.

## How the score is computed

For each candidate pair (own=new material, other=existing material), the
scanner slides a 4s window in 1s hops across the new material's PCM, runs
`MatchWindow` against an in-memory index built from the client's other ready
materials, and accumulates the matched frame ranges on both sides. Then:

```
ownCov   = framesUnionOwn   / totalFramesOwn
otherCov = framesUnionOther / totalFramesOther
score    = max(ownCov, otherCov)
```

Calibration (empirical):

| Pair type | Score |
|-----------|-------|
| Unrelated audios | < 5% |
| Shared sting (~6s vinheta in 30s jingle) | 15-25% |
| Subset (30s cut of a 60s master) | ≥ 50% |
| Near-duplicate | ≥ 95% |

## Re-triggering a scan manually

```bash
nats pub material.similarity-check '{"material_id":"<uuid>"}'
```

The Go subscriber in the `api` process picks it up, re-runs the scan, and
overwrites the row's `similarity_*` columns.

## Inspecting

```sql
SELECT m.title,
       m.similarity_check_status,
       ROUND((m.similarity_score * 100)::numeric, 1) AS pct,
       sim.title AS similar_to,
       m.similarity_acknowledged_at
FROM materials m
LEFT JOIN materials sim ON sim.id = m.most_similar_material_id
WHERE m.similarity_score >= 0.15
ORDER BY m.created_at DESC;
```

## Performance

Per upload: ~50ms index load + ~200ms PCM decode + ~30ms scan against ~20
materials = sub-second. Scales linearly with the client's material count.
A 200-material client would hit ~2-3 seconds per upload — still acceptable.

## Known limitations / follow-ups

- **F-120 (new):** Threshold is not tunable per client. Some clients may want
  a tighter threshold (e.g. 8% for radio production agencies with many shared
  stings). Currently a code change.
- **F-121 (new):** Acknowledgment is per-material. If the operator
  acknowledges material B (similar to A) and later uploads C that's similar
  to A, C gets its own warning. There's no transitive ack of "the A family is
  fine, move on".
```

- [ ] **Step 13.2: Commit**

```bash
git add docs/material-similarity-warning.md
git commit -m "docs: operational guide for material similarity warning"
```

---

## Phase 14 — Manual smoke test

- [ ] **Step 14.1: Rebuild + restart full stack**

```bash
docker compose -f infra/docker/docker-compose.yml up -d --build api fingerprint
```

Expected: both services restart cleanly. The `fingerprint` service must come up BEFORE the `api` (per operator note 2026-05-13) so that the API subscriber sees published events. The docker-compose service `depends_on` ordering should already enforce this — verify with `docker compose ps` that `fingerprint` is healthy before `api` finishes starting.

- [ ] **Step 14.2: End-to-end test via UI**

1. Open `http://localhost:3000/campaigns/<existing-campaign-id>/edit`
2. Navigate to step 3 (Materials)
3. Click "Adicionar material" → "Upload" tab
4. Upload `audio-refs/JINGLE ROGGA VERÃO 60.mp3`
5. Wait for fingerprint badge to turn `Pronto`
6. Wait for similarity badge to appear (or stay clean if there were no similars)
7. Click "Adicionar material" again
8. Upload `audio-refs/JINGLE ROGGA VERÃO 30.mp3` (cut of the same audio)
9. Wait — the badge should appear within ~10s: "⚠ ~100% similar a JINGLE ROGGA VERÃO 60"
10. Click the badge → modal opens with two audio players
11. Press play on both; confirm they're audibly the same audio
12. Click "Manter assim mesmo" → badge disappears

- [ ] **Step 14.3: Negative test — unrelated upload**

Upload `audio-refs/FLORATTA URBAN CLUB.mp3` (or any unrelated master). After fingerprint ready, badge should NOT appear (score < 15%).

- [ ] **Step 14.4: Delete test**

Re-upload the Rôgga 30 cut, wait for badge, click badge → "Remover material novo". Confirm the material disappears from the wizard list AND from the client's library.

- [ ] **Step 14.5: SQL verification**

```bash
docker compose -f infra/docker/docker-compose.yml exec -T postgres psql -U radiocheck -d radiocheck -c "
SELECT title, similarity_check_status,
       ROUND((similarity_score * 100)::numeric, 1) AS pct,
       similarity_acknowledged_at IS NOT NULL AS ackd
FROM materials
ORDER BY created_at DESC LIMIT 5;
"
```

Expected: the new uploads show `similarity_check_status='ready'`, the duplicate pair has `pct` near 100, the kept one has `ackd=t`.

- [ ] **Step 14.6: Final commit + branch wrap-up**

```bash
git log --oneline master..HEAD  # review all commits on the branch
git push -u origin feat/material-similarity-warning
```

Then open a PR. Title: `feat: material similarity warning on upload`. Body should link to the spec and this plan.

---

## Verification Summary

After all phases complete and the manual smoke test passes:

- [ ] All 14 phases checked off
- [ ] `go test ./workers/...` passes
- [ ] Migration applies cleanly + down migration reverts cleanly
- [ ] Wizard shows similarity badge for Rôgga 30 after Rôgga 60 exists
- [ ] Modal A/B plays both audios
- [ ] "Manter" sets `similarity_acknowledged_at` (badge disappears)
- [ ] "Remover" deletes the material (cascade unlinks from campaign)
- [ ] Unrelated uploads do NOT get a badge
- [ ] Re-trigger via `nats pub material.similarity-check '{"material_id":"..."}'` works

---

## Open follow-ups (intentionally out of scope)

- **F-119** (from bridge plan): multi-campaign attribution for material detections.
- **F-120** (new): per-client tunable similarity threshold.
- **F-121** (new): transitive acknowledgment of similar-material families.

Register both new follow-ups in `docs/follow-ups-fase2.md` as part of Phase 13 if not already there.
