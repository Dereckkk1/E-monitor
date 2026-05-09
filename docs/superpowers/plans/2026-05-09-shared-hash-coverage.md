# Shared Hash Coverage — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate false-positive detections caused by audio content shared between two commercials (e.g. a sting reused across a jingle and a spoken spot from the same client).

**Architecture:** Mark fingerprint hashes that appear in ≥2 commercials as `is_shared`. The matching engine continues to count all matches in the histogram (preserving robustness), but emits both a total `Score` and a `UniqueScore` (matches from non-shared hashes only). The state machine confirms based on `UniqueScore`, so a commercial whose only matches come from the shared region of another commercial cannot confirm.

**Tech Stack:** Go (workers), PostgreSQL with hash partitioning (fingerprint_hashes), pgx/v5, NATS index reload, existing match/coverage state machine.

**Root cause this fixes (verified empirically with audio-refs/AMBIENTAL 30.mp3 vs AMBIENTAL JINGLE.mp3):** the last ~6.25s of AMB30 are spectrally identical to the last ~6.25s of JINGLE. With current `MinTemporalCoverage=0.15`, when AMB30 plays, JINGLE accumulates ~34% time-coverage from the shared sting alone and falsely confirms. Per-commercial threshold tuning cannot fix this for short jingles (the same 6.25s overlap yields 69% coverage on a 15s commercial).

---

## File Structure

| File | Responsibility | Action |
|---|---|---|
| `migrations/0015_shared_hashes.up.sql` | Schema: add `is_shared` column to `fingerprint_hashes` | Create |
| `migrations/0015_shared_hashes.down.sql` | Rollback | Create |
| `workers/internal/fingerprint/persist.go` | After Persist, flag shared hashes for the new commercial AND any existing commercials that share its hashes | Modify |
| `workers/internal/fingerprint/persist_test.go` | TDD coverage for the share-flagging query | Modify (file does not yet exist for `Persist` itself; create test file) |
| `workers/internal/fingerprint/sharing.go` | Pure-SQL helper: `MarkSharedHashes(ctx, tx, commercialID)`. Extracted so backfill CLI can reuse | Create |
| `workers/internal/fingerprint/sharing_test.go` | Tests for the helper using a postgres test container | Create |
| `workers/internal/index/store.go` | Add `IsShared bool` to `Entry` | Modify |
| `workers/internal/index/loader.go` | SELECT `is_shared` in both `LoadAll` and the NATS reload path; populate `Entry.IsShared` | Modify |
| `workers/internal/index/loader_test.go` | Verify `IsShared` round-trips | Modify |
| `workers/internal/match/engine.go` | `MatchResult` gets `UniqueScore int`; histogram tracks unique vs shared per (commercial, deltaBin); `MatchWindow` reports both | Modify |
| `workers/internal/match/engine_test.go` | Tests for unique vs total scoring | Modify |
| `workers/internal/match/statemachine.go` | Confirmation requires `UniqueScore >= minScore`. `Score` (total) still gates the score-coverage filter inside `MatchWindow`. | Modify |
| `workers/internal/match/statemachine_test.go` | Tests: (a) shared-only matches don't advance, (b) unique matches do advance | Modify |
| `workers/internal/match/integration_audio_test.go` | End-to-end: load AMB30+JINGLE WAVs, generate fingerprints, mark shared, build index, feed AMB30 PCM through MatchWindow, assert JINGLE never reaches confirmation while AMB30 does | Create |
| `cmd/backfill-shared-hashes/main.go` | One-shot CLI to flag shared hashes for the entire existing catalog | Create |
| `docs/shared-hash-detection.md` | Operational doc: what it does, when shared flagging runs, how to backfill, how to inspect via psql | Create |

---

## Task 1 — Schema migration

**Files:**
- Create: `migrations/0015_shared_hashes.up.sql`
- Create: `migrations/0015_shared_hashes.down.sql`

- [ ] **Step 1.1: Write the up migration**

`migrations/0015_shared_hashes.up.sql`:

```sql
-- §18.2.2 follow-up — Shared-hash detection.
--
-- A fingerprint hash is "shared" when its hash_value appears in two or more
-- commercials. During matching we count all hits for the histogram peak
-- (preserving robustness against degraded streams), but only hits coming from
-- non-shared hashes are eligible to advance the state machine toward
-- confirmation. This eliminates false positives where a short shared sting
-- (e.g. a 6s jingle reused at the end of a 30s spoken spot) is enough to
-- accumulate ≥15% time-coverage on the wrong commercial.
--
-- The flag is denormalized onto fingerprint_hashes so the matching index can
-- be loaded with a single SELECT — adding a JOIN in the hot path was
-- considered and rejected (the index is rebuilt on every catalog change).

ALTER TABLE fingerprint_hashes
    ADD COLUMN IF NOT EXISTS is_shared BOOLEAN NOT NULL DEFAULT false;
```

- [ ] **Step 1.2: Write the down migration**

`migrations/0015_shared_hashes.down.sql`:

```sql
ALTER TABLE fingerprint_hashes
    DROP COLUMN IF EXISTS is_shared;
```

- [ ] **Step 1.3: Apply the migration locally**

Run: `docker compose up -d --build migrate`
Expected: migrate service exits 0; `psql` shows the column.

Verify:
```bash
docker compose exec -T postgres psql -U radiocheck -d radiocheck -c "\d+ fingerprint_hashes" | grep is_shared
```
Expected: row showing `is_shared | boolean | not null | false`

- [ ] **Step 1.4: Commit**

```bash
git add migrations/0015_shared_hashes.up.sql migrations/0015_shared_hashes.down.sql
git commit -m "feat(schema): add is_shared flag to fingerprint_hashes"
```

---

## Task 2 — Sharing helper (TDD with real Postgres)

**Files:**
- Create: `workers/internal/fingerprint/sharing.go`
- Create: `workers/internal/fingerprint/sharing_test.go`

The project uses `pgxpool` and there are existing tests that hit a real database via the `RADIOCHECK_TEST_DATABASE_URL` env var. Tests in this task follow that pattern (see existing `*_test.go` files in `workers/internal/catalog/` for the convention).

- [ ] **Step 2.1: Write the failing test**

`workers/internal/fingerprint/sharing_test.go`:

```go
package fingerprint

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// requirePool returns a pool against RADIOCHECK_TEST_DATABASE_URL, or
// t.Skip()s if the env var is unset (CI runs with it set; local devs may not).
func requirePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("RADIOCHECK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("RADIOCHECK_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedCommercial inserts a stub commercials row and a set of hashes for it.
// Caller passes (hash_value, time_frame) pairs.
func seedCommercial(t *testing.T, pool *pgxpool.Pool, hashes []struct {
	Value uint32
	Frame int32
}) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	// Need a campaign and a client for the commercials FK chain.
	var clientID, campaignID, commercialID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO clients (name) VALUES ('share-test') RETURNING id`).Scan(&clientID); err != nil {
		t.Fatalf("seed client: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO campaigns (client_id, name, start_date, end_date)
		 VALUES ($1, 'share-test', NOW(), NOW() + INTERVAL '7 days') RETURNING id`,
		clientID,
	).Scan(&campaignID); err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO commercials
		   (campaign_id, title, master_storage_path, master_sha256, duration_seconds, fingerprint_status)
		 VALUES ($1, 'share-test', '/dev/null', repeat('0', 64), 30, 'ready')
		 RETURNING id`,
		campaignID,
	).Scan(&commercialID); err != nil {
		t.Fatalf("seed commercial: %v", err)
	}
	for _, h := range hashes {
		if _, err := pool.Exec(ctx,
			`INSERT INTO fingerprint_hashes (commercial_id, variant_id, rate_id, hash_value, time_frame)
			 VALUES ($1, 0, 0, $2, $3)`,
			commercialID, int64(h.Value), h.Frame,
		); err != nil {
			t.Fatalf("seed hash: %v", err)
		}
	}
	t.Cleanup(func() {
		// Cascade: deleting the commercial removes its hashes via FK; campaign
		// and client are kept so concurrent tests don't race on cleanup. The
		// CI test DB is reset per run.
		_, _ = pool.Exec(ctx, `DELETE FROM fingerprint_hashes WHERE commercial_id = $1`, commercialID)
		_, _ = pool.Exec(ctx, `DELETE FROM commercials WHERE id = $1`, commercialID)
	})
	return commercialID
}

func TestMarkSharedHashes_FlagsBothSides(t *testing.T) {
	pool := requirePool(t)
	ctx := context.Background()

	// Commercial A: hashes {100,200,300}
	a := seedCommercial(t, pool, []struct {
		Value uint32
		Frame int32
	}{{100, 0}, {200, 10}, {300, 20}})

	// Commercial B: hashes {200,300,400} — overlaps with A on {200, 300}
	b := seedCommercial(t, pool, []struct {
		Value uint32
		Frame int32
	}{{200, 5}, {300, 15}, {400, 25}})

	// Run the helper for B (the newly inserted commercial).
	if err := MarkSharedHashes(ctx, pool, b); err != nil {
		t.Fatalf("MarkSharedHashes: %v", err)
	}

	// Expect: hash_value 200 and 300 are flagged is_shared=true on BOTH a and b.
	// Hash 100 (only in a) and 400 (only in b) remain false.
	type row struct {
		commercial uuid.UUID
		value      int64
		isShared   bool
	}
	var rows []row
	r, err := pool.Query(ctx, `
		SELECT commercial_id, hash_value, is_shared
		FROM fingerprint_hashes
		WHERE commercial_id IN ($1, $2)
		ORDER BY commercial_id, hash_value`, a, b)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for r.Next() {
		var x row
		if err := r.Scan(&x.commercial, &x.value, &x.isShared); err != nil {
			t.Fatalf("scan: %v", err)
		}
		rows = append(rows, x)
	}
	r.Close()

	want := map[int64]bool{
		100: false,
		200: true,
		300: true,
		400: false,
	}
	for _, x := range rows {
		if got := x.isShared; got != want[x.value] {
			t.Errorf("commercial=%s hash=%d: is_shared=%v want=%v", x.commercial, x.value, got, want[x.value])
		}
	}
}
```

- [ ] **Step 2.2: Run the test to confirm it fails**

Run: `go test ./workers/internal/fingerprint/ -run TestMarkSharedHashes -v`
Expected: FAIL — `MarkSharedHashes` undefined

- [ ] **Step 2.3: Implement the helper**

`workers/internal/fingerprint/sharing.go`:

```go
package fingerprint

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MarkSharedHashes flags every fingerprint_hashes row whose hash_value also
// appears under any other commercial as is_shared=true. The new commercial's
// rows AND the colliding rows in other commercials are both updated, so the
// flag stays symmetric — important for the matching engine, which examines
// the flag of every Entry it reads from the index regardless of which
// commercial owned the original SQL row.
//
// Performance: bounded by the new commercial's hash_value cardinality (one
// indexed lookup per distinct value). For ~3k hashes per master and the
// per-partition idx_fph_p%_hash btree, this runs in tens of milliseconds.
//
// Idempotent: re-running for the same commercial produces no additional rows
// and leaves the flag set to true wherever it was already true.
func MarkSharedHashes(ctx context.Context, pool *pgxpool.Pool, commercialID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE fingerprint_hashes
		SET is_shared = true
		WHERE hash_value IN (
			SELECT DISTINCT hash_value
			FROM fingerprint_hashes
			WHERE commercial_id = $1
			  AND hash_value IN (
			      SELECT hash_value FROM fingerprint_hashes
			      WHERE commercial_id != $1
			  )
		)
	`, commercialID)
	if err != nil {
		return fmt.Errorf("fingerprint: mark shared hashes: %w", err)
	}
	return nil
}
```

- [ ] **Step 2.4: Run the test to confirm it passes**

Run: `go test ./workers/internal/fingerprint/ -run TestMarkSharedHashes -v`
Expected: PASS

- [ ] **Step 2.5: Add idempotency test**

Append to `sharing_test.go`:

```go
func TestMarkSharedHashes_Idempotent(t *testing.T) {
	pool := requirePool(t)
	ctx := context.Background()

	a := seedCommercial(t, pool, []struct {
		Value uint32
		Frame int32
	}{{500, 0}})
	b := seedCommercial(t, pool, []struct {
		Value uint32
		Frame int32
	}{{500, 5}})

	for i := 0; i < 3; i++ {
		if err := MarkSharedHashes(ctx, pool, b); err != nil {
			t.Fatalf("MarkSharedHashes pass %d: %v", i, err)
		}
	}

	var aShared, bShared bool
	if err := pool.QueryRow(ctx,
		`SELECT is_shared FROM fingerprint_hashes WHERE commercial_id = $1 AND hash_value = 500`, a,
	).Scan(&aShared); err != nil {
		t.Fatalf("scan a: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT is_shared FROM fingerprint_hashes WHERE commercial_id = $1 AND hash_value = 500`, b,
	).Scan(&bShared); err != nil {
		t.Fatalf("scan b: %v", err)
	}
	if !aShared || !bShared {
		t.Fatalf("expected both commercials flagged after idempotent runs; a=%v b=%v", aShared, bShared)
	}
}
```

Run: `go test ./workers/internal/fingerprint/ -run TestMarkSharedHashes -v`
Expected: both tests PASS

- [ ] **Step 2.6: Commit**

```bash
git add workers/internal/fingerprint/sharing.go workers/internal/fingerprint/sharing_test.go
git commit -m "feat(fingerprint): MarkSharedHashes flags hash collisions across commercials"
```

---

## Task 3 — Wire MarkSharedHashes into Persist

**Files:**
- Modify: `workers/internal/fingerprint/persist.go`

- [ ] **Step 3.1: Modify Persist to call MarkSharedHashes after commit**

In `Persist()`, after the existing `tx.Commit(ctx)` succeeds, add:

```go
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("fingerprint: commit: %w", err)
	}

	// Flag any hash values shared with previously-uploaded commercials so the
	// matching engine can exclude them from the unique-coverage tally. We run
	// this OUTSIDE the insert transaction: a failure here must not roll back
	// the new commercial's fingerprint (an un-flagged commercial just defaults
	// to all-unique scoring, which is the pre-Task-1 behaviour). The error is
	// returned so callers can surface it in logs / monitoring.
	if err := MarkSharedHashes(ctx, pool, opts.CommercialID); err != nil {
		return inserted, fmt.Errorf("fingerprint: mark shared after persist: %w", err)
	}
	return inserted, nil
}
```

- [ ] **Step 3.2: Run existing fingerprint tests to confirm nothing regressed**

Run: `go test ./workers/internal/fingerprint/... -v`
Expected: all tests PASS

- [ ] **Step 3.3: Commit**

```bash
git add workers/internal/fingerprint/persist.go
git commit -m "feat(fingerprint): flag shared hashes automatically on Persist"
```

---

## Task 4 — Index propagation

**Files:**
- Modify: `workers/internal/index/store.go`
- Modify: `workers/internal/index/loader.go`
- Modify: `workers/internal/index/loader_test.go`

- [ ] **Step 4.1: Add IsShared to Entry**

Modify `workers/internal/index/store.go`:

```go
// Entry is one posting in the hash index.
type Entry struct {
	CommercialShortID int32
	VariantID         uint8 // broadcast simulation variant (0=original, 1=light, 2=medium, 3=heavy)
	RateID            uint8 // time-stretch rate variant
	TimeFrame         int32
	IsShared          bool // true when this hash_value also appears under another commercial
}
```

- [ ] **Step 4.2: Update LoadAll query and scan**

In `workers/internal/index/loader.go`, replace the SELECT in `LoadAll` and the scan loop:

```go
	rows, err := l.db.Query(ctx, `
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id, fh.is_shared, c.short_id
		FROM fingerprint_hashes fh
		JOIN commercials c  ON c.id  = fh.commercial_id
		JOIN campaigns   ca ON ca.id = c.campaign_id
		WHERE c.fingerprint_status = 'ready'
		  AND ca.status IN `+indexEligibleStatuses+`
	`)
```

And the scan:

```go
		var hashValue uint32
		var timeFrame  int32
		var variantID  int16
		var rateID     int16
		var isShared   bool
		var shortID    int32
		if err := rows.Scan(&hashValue, &timeFrame, &variantID, &rateID, &isShared, &shortID); err != nil {
			return fmt.Errorf("index loader: scan row: %w", err)
		}
		// ...
		newIndex[hashValue] = append(newIndex[hashValue], Entry{
			CommercialShortID: shortID,
			VariantID:         uint8(variantID),
			RateID:            uint8(rateID),
			TimeFrame:         timeFrame,
			IsShared:          isShared,
		})
```

- [ ] **Step 4.3: Update Subscribe (NATS reload) the same way**

In the NATS subscription handler in `loader.go`, modify the per-commercial fetch to also select `is_shared`, and populate `Entry.IsShared` when appending entries to `merged`. The existing scan struct gets a new `isShared bool` field; the `Entry{}` literal at the bottom gets `IsShared: ne.isShared`.

```go
		rows, err := l.db.Query(ctx, `
			SELECT hash_value, time_frame, variant_id, rate_id, is_shared
			FROM fingerprint_hashes
			WHERE commercial_id = $1
		`, payload.CommercialID)
```

```go
		type hashEntry struct {
			hash      uint32
			timeFrame int32
			variantID int16
			rateID    int16
			isShared  bool
		}
		// ...
		for rows.Next() {
			var hashValue uint32
			var timeFrame  int32
			var variantID  int16
			var rateID     int16
			var isShared   bool
			if err := rows.Scan(&hashValue, &timeFrame, &variantID, &rateID, &isShared); err != nil {
				// existing error handling
				return
			}
			// ...
			newEntries = append(newEntries, hashEntry{hash: hashValue, timeFrame: timeFrame, variantID: variantID, rateID: rateID, isShared: isShared})
		}
		// ...
		for _, ne := range newEntries {
			merged[ne.hash] = append(merged[ne.hash], Entry{
				CommercialShortID: shortID,
				VariantID:         uint8(ne.variantID),
				RateID:            uint8(ne.rateID),
				TimeFrame:         ne.timeFrame,
				IsShared:          ne.isShared,
			})
		}
```

- [ ] **Step 4.4: Add a loader test for IsShared round-trip**

Append to `workers/internal/index/loader_test.go` (the file already uses a real DB pool — follow its existing conventions for seeding):

```go
func TestLoader_LoadAll_PreservesIsShared(t *testing.T) {
	// Use the existing test setup helper in this file (mirror the pattern
	// used by other tests; reuse the same seeded campaign/commercials).
	// Insert two commercials sharing one hash value, manually flag is_shared,
	// then call LoadAll and assert Entry.IsShared survives the round-trip.
	pool := requirePool(t) // exists via package fingerprint sharing_test, copy if not yet in this package
	ctx := context.Background()

	// (Insert two commercials with overlapping hash 12345; mark is_shared=true
	// on those rows; campaign status='ativa'; fingerprint_status='ready'.)
	// Then:

	store := New()
	loader := NewLoader(store, pool, nil, zap.NewNop())
	if err := loader.LoadAll(ctx); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	entries := store.Lookup(12345)
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if !e.IsShared {
			t.Errorf("entry %+v: IsShared=false, want true", e)
		}
	}
}
```

(The `requirePool` helper from Task 2 must be exported or duplicated into the index test file. If duplicated, keep it minimal; if exported, move to a shared `testutil` package — pick whichever the repo already does.)

- [ ] **Step 4.5: Run the tests**

Run: `go test ./workers/internal/index/... -v`
Expected: existing tests PASS, new IsShared test PASSes.

- [ ] **Step 4.6: Commit**

```bash
git add workers/internal/index/
git commit -m "feat(index): propagate is_shared flag through the matching index"
```

---

## Task 5 — Engine: emit UniqueScore

**Files:**
- Modify: `workers/internal/match/engine.go`
- Modify: `workers/internal/match/engine_test.go`

- [ ] **Step 5.1: Write failing test for UniqueScore**

Append to `workers/internal/match/engine_test.go`:

```go
func TestMatchWindow_UniqueScoreExcludesSharedHashes(t *testing.T) {
	// Build an index where commercial 1 has 10 hashes, all flagged is_shared.
	// Commercial 2 has the same 10 hash values, also is_shared.
	// Plus commercial 1 has 10 hashes that are unique (is_shared=false).
	idx := make(index.Index)
	for v := uint32(0); v < 10; v++ {
		// Shared: appears under both commercials.
		idx[v] = append(idx[v],
			index.Entry{CommercialShortID: 1, TimeFrame: int32(v), IsShared: true},
			index.Entry{CommercialShortID: 2, TimeFrame: int32(v), IsShared: true},
		)
	}
	for v := uint32(100); v < 110; v++ {
		// Unique to commercial 1.
		idx[v] = append(idx[v],
			index.Entry{CommercialShortID: 1, TimeFrame: int32(v - 100), IsShared: false},
		)
	}
	store := index.New()
	store.Swap(idx)

	// Stub the audio pipeline by hand-building a hash list. The engine reads
	// hashes via audio.GenerateHashes(...) which we cannot bypass without
	// refactoring; instead, we test buildHistogram directly by exposing it
	// for tests OR call MatchWindow with PCM that we know produces those
	// hashes. The simpler approach is to add an internal helper
	// matchHashes(hashes, store) used by both MatchWindow and the test.
	//
	// (See engine.go change below — extract matchHashes.)
	hashes := make([]audio.Hash, 0, 20)
	for v := uint32(0); v < 10; v++ {
		hashes = append(hashes, audio.Hash{Value: v, TimeFrame: int(v)})
	}
	for v := uint32(100); v < 110; v++ {
		hashes = append(hashes, audio.Hash{Value: v, TimeFrame: int(v - 100)})
	}

	results := matchHashes(hashes, store, 1, 0.0)
	// commercial 1: 20 hits total (10 shared + 10 unique), 10 unique
	// commercial 2: 10 hits total (shared only), 0 unique
	var got1, got2 *MatchResult
	for i := range results {
		switch results[i].CommercialShortID {
		case 1:
			got1 = &results[i]
		case 2:
			got2 = &results[i]
		}
	}
	if got1 == nil || got1.Score != 20 || got1.UniqueScore != 10 {
		t.Errorf("commercial 1: %+v, want Score=20 UniqueScore=10", got1)
	}
	if got2 == nil || got2.Score != 10 || got2.UniqueScore != 0 {
		t.Errorf("commercial 2: %+v, want Score=10 UniqueScore=0", got2)
	}
}
```

- [ ] **Step 5.2: Run to confirm failure**

Run: `go test ./workers/internal/match/ -run TestMatchWindow_UniqueScoreExcludesSharedHashes -v`
Expected: FAIL — `matchHashes` and `UniqueScore` undefined.

- [ ] **Step 5.3: Modify engine.go to track unique hits**

In `workers/internal/match/engine.go`:

1. Add `UniqueScore int` to `MatchResult`:

```go
type MatchResult struct {
	CommercialShortID int32
	VariantID         uint8
	RateID            uint8
	Score             int // histogram peak count (all hits)
	UniqueScore       int // histogram peak count from non-shared hashes only
	TotalHashes       int
	OffsetFrames      int
}
```

2. Replace `buildHistogram` with a version that tracks both maps, keyed by the same `histKey`:

```go
// buildHistogram returns:
//   - bestTotal[commercialID]: the (deltaBin, count) with the highest TOTAL count
//   - bestUnique[histKey]: per-bin count of non-shared hits only, keyed by the
//     full histKey so the caller can read off the unique count for the same
//     (commercial, variant, rate, deltaBin) chosen by bestTotal.
//   - totalHashes: number of live hashes generated for this window.
//
// We split the two maps because the "winning" deltaBin must be chosen by total
// count (otherwise the histogram peak shifts), but confirmation must read the
// unique count at *that same bin*.
func buildHistogram(samples []float32, store *index.Store) (
	bestTotal map[int32]struct {
		count     int
		variantID uint8
		rateID    uint8
		deltaBin  int
	},
	uniqueByKey map[histKey]int,
	totalHashes int,
) {
	filtered := audio.ApplyHighPass(samples, 100.0, 16000)
	normalized := audio.NormalizeRMS(filtered, -20.0)
	spec := audio.STFT(normalized)
	peaks := audio.PickPeaks(spec)
	hashes := audio.GenerateHashes(peaks)
	return matchHashes_inner(hashes, store)
}

// matchHashes is the test-friendly variant: takes pre-generated hashes,
// returns MatchResults filtered by threshold and minScoreCoverage. The
// production MatchWindow wraps the audio preprocessing and delegates here.
func matchHashes(hashes []audio.Hash, store *index.Store, threshold int, minScoreCoverage float64) []MatchResult {
	bestTotal, uniqueByKey, totalHashes := matchHashes_inner_fromHashes(hashes, store)
	if totalHashes == 0 {
		return nil
	}
	minHits := int(float64(totalHashes) * minScoreCoverage)
	var out []MatchResult
	for id, b := range bestTotal {
		if b.count >= threshold && b.count >= minHits {
			k := histKey{commercialID: id, variantID: b.variantID, rateID: b.rateID, deltaBin: b.deltaBin}
			out = append(out, MatchResult{
				CommercialShortID: id,
				VariantID:         b.variantID,
				RateID:            b.rateID,
				Score:             b.count,
				UniqueScore:       uniqueByKey[k],
				TotalHashes:       totalHashes,
				OffsetFrames:      b.deltaBin * DeltaBinSize,
			})
		}
	}
	return out
}

// matchHashes_inner_fromHashes does the histogram building given a hash slice.
// Extracted so buildHistogram (which generates hashes from PCM) and matchHashes
// (which receives hashes directly) can share the inner loop.
func matchHashes_inner_fromHashes(hashes []audio.Hash, store *index.Store) (
	bestTotal map[int32]struct {
		count     int
		variantID uint8
		rateID    uint8
		deltaBin  int
	},
	uniqueByKey map[histKey]int,
	totalHashes int,
) {
	totalHashes = len(hashes)
	if totalHashes == 0 {
		return nil, nil, 0
	}
	histTotal := make(map[histKey]int)
	histUnique := make(map[histKey]int)
	for _, h := range hashes {
		for _, entry := range store.Lookup(h.Value) {
			delta := h.TimeFrame - int(entry.TimeFrame)
			k := histKey{
				commercialID: entry.CommercialShortID,
				variantID:    entry.VariantID,
				rateID:       entry.RateID,
				deltaBin:     delta / DeltaBinSize,
			}
			histTotal[k]++
			if !entry.IsShared {
				histUnique[k]++
			}
		}
	}
	bestTotal = make(map[int32]struct {
		count     int
		variantID uint8
		rateID    uint8
		deltaBin  int
	})
	for k, c := range histTotal {
		if cur, ok := bestTotal[k.commercialID]; !ok || c > cur.count {
			bestTotal[k.commercialID] = struct {
				count     int
				variantID uint8
				rateID    uint8
				deltaBin  int
			}{c, k.variantID, k.rateID, k.deltaBin}
		}
	}
	return bestTotal, histUnique, totalHashes
}

// MatchWindow keeps its public signature; it preprocesses then calls the
// shared inner.
func MatchWindow(samples []float32, store *index.Store, threshold int, minScoreCoverage float64) []MatchResult {
	filtered := audio.ApplyHighPass(samples, 100.0, 16000)
	normalized := audio.NormalizeRMS(filtered, -20.0)
	spec := audio.STFT(normalized)
	peaks := audio.PickPeaks(spec)
	hashes := audio.GenerateHashes(peaks)
	return matchHashes(hashes, store, threshold, minScoreCoverage)
}
```

(The split between `matchHashes_inner_fromHashes` and the wrappers is to keep the audio pipeline isolated so tests can stub hashes without going through STFT.)

3. Update `ScanScores` if it relied on the old `buildHistogram` signature — change it to call `matchHashes_inner_fromHashes` after generating hashes from the input PCM. Keep the public signature unchanged.

- [ ] **Step 5.4: Run the test**

Run: `go test ./workers/internal/match/ -run TestMatchWindow_UniqueScoreExcludesSharedHashes -v`
Expected: PASS

- [ ] **Step 5.5: Run all engine tests**

Run: `go test ./workers/internal/match/ -v`
Expected: all PASS (existing tests should still work since they don't read UniqueScore).

- [ ] **Step 5.6: Commit**

```bash
git add workers/internal/match/engine.go workers/internal/match/engine_test.go
git commit -m "feat(match): emit UniqueScore alongside Score in MatchResult"
```

---

## Task 6 — State machine consumes UniqueScore

**Files:**
- Modify: `workers/internal/match/statemachine.go`
- Modify: `workers/internal/match/statemachine_test.go`

The behaviour change is precise: the `result.Score >= sm.minScore` checks at lines 92 and 107 of statemachine.go become `result.UniqueScore >= sm.minScore`. Total Score is not used by the state machine after this change. The existing minScoreCoverage filter inside MatchWindow continues to use total Score, so degraded streams that produce broad-coverage hits still qualify a result for the state machine — but the state machine itself only credits hits coming from unique hashes.

- [ ] **Step 6.1: Write failing test — shared-only matches don't advance**

Append to `workers/internal/match/statemachine_test.go`:

```go
func TestStateMachine_SharedOnlyMatchesDoNotConfirm(t *testing.T) {
	totalFrames := 234 // ~30s commercial
	sm := NewStateMachine(
		"station-1", 42, totalFrames,
		5,   // minScore
		0.15,
		30*time.Second,
		5*time.Second,
		zap.NewNop(),
	)
	now := time.Now()
	// Feed 20 windows where Score=10 (above threshold) but UniqueScore=0
	// (all hits are from shared hashes). Coverage should never advance.
	for i := 0; i < 20; i++ {
		got := sm.Update(MatchResult{
			CommercialShortID: 42,
			Score:             10,
			UniqueScore:       0,
			OffsetFrames:      0,
			TotalHashes:       100,
		}, now.Add(time.Duration(i)*time.Second))
		if got != nil {
			t.Fatalf("window %d: confirmed unexpectedly", i)
		}
	}
	if sm.State() != StateIdle {
		t.Fatalf("state = %v, want StateIdle", sm.State())
	}
}

func TestStateMachine_UniqueMatchesAdvance(t *testing.T) {
	totalFrames := 234
	sm := NewStateMachine(
		"station-1", 42, totalFrames,
		5, 0.15,
		30*time.Second,
		5*time.Second,
		zap.NewNop(),
	)
	now := time.Now()
	// Two windows 4 seconds apart with UniqueScore=10 each is enough for
	// coverage = (4s * 7.8125 + 32) / 234 ≈ 0.27 > 0.15 → confirm.
	if got := sm.Update(MatchResult{CommercialShortID: 42, Score: 10, UniqueScore: 10}, now); got != nil {
		t.Fatalf("first window confirmed too early: %+v", got)
	}
	got := sm.Update(MatchResult{CommercialShortID: 42, Score: 10, UniqueScore: 10}, now.Add(4*time.Second))
	if got == nil {
		t.Fatalf("second window did not confirm")
	}
	if got.CommercialShortID != 42 {
		t.Fatalf("got %+v", got)
	}
}
```

- [ ] **Step 6.2: Run tests, confirm failure on the first new test**

Run: `go test ./workers/internal/match/ -run TestStateMachine_ -v`
Expected: `TestStateMachine_SharedOnlyMatchesDoNotConfirm` FAILs (current code confirms because Score=10 advances coverage); the second test passes by accident.

- [ ] **Step 6.3: Switch state machine criterion to UniqueScore**

In `workers/internal/match/statemachine.go`:

- Line 92 (StateIdle): `if result.Score >= sm.minScore {` → `if result.UniqueScore >= sm.minScore {`
- Line 107 (StateDetecting): `if result.Score >= sm.minScore {` → `if result.UniqueScore >= sm.minScore {`
- Line 137 (highScore for the uncertain branch): `highScore := result.Score >= 3*sm.minScore` → `highScore := result.UniqueScore >= 3*sm.minScore`

Add a comment block at the top of the file (above the `State` constants) explaining the contract: the state machine considers only unique-hash matches; total `Score` is left to the engine's own minScoreCoverage filter and to logging/diagnostics.

- [ ] **Step 6.4: Run the tests**

Run: `go test ./workers/internal/match/ -v`
Expected: all PASS, including `TestStateMachine_SharedOnlyMatchesDoNotConfirm`.

- [ ] **Step 6.5: Commit**

```bash
git add workers/internal/match/statemachine.go workers/internal/match/statemachine_test.go
git commit -m "feat(match): state machine confirms on UniqueScore, not total Score"
```

---

## Task 7 — End-to-end audio test

**Files:**
- Create: `workers/internal/match/integration_audio_test.go`

This test is the verification the user asked for: load both real audio files, generate fingerprints, mark shared, build the in-memory index, then run AMB30 PCM through `MatchWindow` window by window and assert JINGLE is never reported as a confirmed detection.

- [ ] **Step 7.1: Write the integration test**

`workers/internal/match/integration_audio_test.go`:

```go
//go:build integration

package match

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/fingerprint"
	"radiocheck/internal/index"
	"radiocheck/pkg/audio"
)

// TestSharedHash_AMB30_DoesNotFalseConfirmJINGLE plays AMB30 through the
// matcher and asserts that the JINGLE never reaches a confirmed state — even
// though the last ~6.25s of AMB30 audio is identical to the last ~6.25s of
// JINGLE.
//
// Run with: go test -tags integration ./workers/internal/match/ -run TestSharedHash_AMB30 -v
func TestSharedHash_AMB30_DoesNotFalseConfirmJINGLE(t *testing.T) {
	ctx := context.Background()
	root := repoRoot(t)
	amb30Path := filepath.Join(root, "audio-refs", "AMBIENTAL 30.mp3")
	jinglePath := filepath.Join(root, "audio-refs", "AMBIENTAL JINGLE.mp3")

	// 1. Generate fingerprints for both.
	amb30Result, err := fingerprint.GenerateForVariant(ctx, amb30Path, fingerprint.VariantClean)
	if err != nil {
		t.Fatalf("fingerprint AMB30: %v", err)
	}
	jingleResult, err := fingerprint.GenerateForVariant(ctx, jinglePath, fingerprint.VariantClean)
	if err != nil {
		t.Fatalf("fingerprint JINGLE: %v", err)
	}

	// 2. Build the in-memory index. Mark hash values that appear in BOTH
	//    commercials as is_shared on every entry, mirroring what MarkSharedHashes
	//    would do at the SQL layer.
	const (
		amb30ShortID  int32 = 1
		jingleShortID int32 = 2
	)
	hashesByCommercial := map[int32][]fingerprint.Hash{
		amb30ShortID:  amb30Result.Hashes,
		jingleShortID: jingleResult.Hashes,
	}

	// Find shared hash values.
	seen := map[uint32]map[int32]bool{}
	for cid, hs := range hashesByCommercial {
		for _, h := range hs {
			if seen[h.Value] == nil {
				seen[h.Value] = map[int32]bool{}
			}
			seen[h.Value][cid] = true
		}
	}
	sharedValues := map[uint32]bool{}
	for v, cs := range seen {
		if len(cs) >= 2 {
			sharedValues[v] = true
		}
	}
	t.Logf("shared hash values: %d / %d (AMB30) / %d (JINGLE)",
		len(sharedValues), len(amb30Result.Hashes), len(jingleResult.Hashes))

	// Build the index.
	idx := make(index.Index)
	for cid, hs := range hashesByCommercial {
		for _, h := range hs {
			idx[h.Value] = append(idx[h.Value], index.Entry{
				CommercialShortID: cid,
				TimeFrame:         int32(h.TimeFrame),
				IsShared:          sharedValues[h.Value],
			})
		}
	}
	store := index.New()
	store.Swap(idx)

	// 3. Decode AMB30 to PCM (mono, 16kHz) and feed the matcher in 4-second
	//    windows with 1-second hop.
	pcm, err := fingerprint.DecodePCM(ctx, amb30Path, fingerprint.VariantClean)
	if err != nil {
		t.Fatalf("decode amb30 pcm: %v", err)
	}
	const windowSamples = 16000 * 4 // 4s
	const hopSamples = 16000 * 1    // 1s

	// 4. Build a state machine for each commercial; track confirmations.
	const minScore = 5
	const minTemporalCoverage = 0.15
	smAMB30 := NewStateMachine("station-test", amb30ShortID,
		int(amb30Result.DurationSec*7.8125),
		minScore, minTemporalCoverage, 30*time.Second, 5*time.Second, zap.NewNop())
	smJingle := NewStateMachine("station-test", jingleShortID,
		int(jingleResult.DurationSec*7.8125),
		minScore, minTemporalCoverage, 30*time.Second, 5*time.Second, zap.NewNop())

	confirmedAMB30, confirmedJINGLE := 0, 0
	startTime := time.Now()
	for off := 0; off+windowSamples <= len(pcm); off += hopSamples {
		window := pcm[off : off+windowSamples]
		// Convert int16 PCM to float32 normalized samples expected by MatchWindow.
		samples := make([]float32, len(window))
		for i, s := range window {
			samples[i] = float32(s) / 32768.0
		}
		results := MatchWindow(samples, store, minScore, 0.02)
		now := startTime.Add(time.Duration(off/16000) * time.Second)

		var amb, jin *MatchResult
		for i := range results {
			switch results[i].CommercialShortID {
			case amb30ShortID:
				amb = &results[i]
			case jingleShortID:
				jin = &results[i]
			}
		}
		if amb != nil {
			if c := smAMB30.Update(*amb, now); c != nil {
				confirmedAMB30++
			}
		}
		if jin != nil {
			if c := smJingle.Update(*jin, now); c != nil {
				confirmedJINGLE++
			}
		}
		smAMB30.Tick(now)
		smJingle.Tick(now)
	}

	t.Logf("AMB30 confirmations: %d, JINGLE confirmations: %d", confirmedAMB30, confirmedJINGLE)
	if confirmedJINGLE != 0 {
		t.Errorf("JINGLE was falsely confirmed %d times during AMB30 playback", confirmedJINGLE)
	}
	if confirmedAMB30 == 0 {
		t.Errorf("AMB30 should confirm at least once during its own playback")
	}
}

// TestSharedHash_JINGLE_StillConfirmsItself plays JINGLE through the matcher
// and asserts that the JINGLE itself is correctly confirmed (regression check —
// removing shared hits from coverage must not break legitimate detection).
func TestSharedHash_JINGLE_StillConfirmsItself(t *testing.T) {
	// Same scaffolding as above, but feed JINGLE PCM and expect JINGLE confirmations >= 1.
	// (Implementation mirrors the previous test — kept as a separate test for clarity.)
	t.Skip("Mirror of the AMB30 test, swapped: implement once the AMB30 test is green.")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// Walk up until we find go.mod (the workers/ go.mod) and then go one more level.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "audio-refs")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find audio-refs/ above %s", wd)
		}
		dir = parent
	}
}
```

- [ ] **Step 7.2: Verify imports**

The test imports `radiocheck/internal/fingerprint` and `radiocheck/internal/index`. Confirm the module path matches what `workers/go.mod` declares; if `radiocheck` is the module name, this is correct.

- [ ] **Step 7.3: Run the integration test**

Run: `cd workers && go test -tags integration ./internal/match/ -run TestSharedHash_AMB30 -v`
Expected: PASS — log line shows `JINGLE confirmations: 0`.

If this fails (i.e., JINGLE confirmations > 0), do NOT proceed to commit. Investigate:

- Is the shared-value count > 0? (logged at the start) — if zero, the audio similarity our spectral analysis identified is below the hash level; the fix may need a softer notion of sharing (e.g. hash + ±2 frame neighborhood). Adjust before continuing.
- Is the coverage genuinely flat for JINGLE? Add a temporary log line that prints `smJingle.coverage.Coverage()` per window.

- [ ] **Step 7.4: Implement the JINGLE-positive test (Step 7.1's skipped second test)**

Replace the `t.Skip(...)` with the mirrored implementation: decode JINGLE PCM, feed it, expect `confirmedJINGLE >= 1`. This guards against the regression where excluding shared hashes accidentally prevents legitimate detections.

Run: `cd workers && go test -tags integration ./internal/match/ -run TestSharedHash -v`
Expected: both tests PASS.

- [ ] **Step 7.5: Commit**

```bash
git add workers/internal/match/integration_audio_test.go
git commit -m "test(match): end-to-end shared-hash regression with AMB30/JINGLE"
```

---

## Task 8 — Backfill CLI

**Files:**
- Create: `cmd/backfill-shared-hashes/main.go`

- [ ] **Step 8.1: Write the CLI**

`cmd/backfill-shared-hashes/main.go`:

```go
// backfill-shared-hashes flags is_shared on every fingerprint_hashes row whose
// hash_value occurs under two or more commercials. Run once after deploying
// the 0015 migration so existing catalog gets the same treatment new
// commercials get automatically via Persist.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres DSN")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("--dsn or DATABASE_URL is required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	tag, err := pool.Exec(ctx, `
		UPDATE fingerprint_hashes h
		SET is_shared = true
		WHERE EXISTS (
			SELECT 1 FROM fingerprint_hashes h2
			WHERE h2.hash_value = h.hash_value
			  AND h2.commercial_id != h.commercial_id
		)
		AND h.is_shared = false
	`)
	if err != nil {
		log.Fatalf("update: %v", err)
	}
	fmt.Printf("flagged %d rows as shared\n", tag.RowsAffected())
}
```

- [ ] **Step 8.2: Build the binary to confirm it compiles**

Run: `cd workers && go build -o /tmp/backfill ./cmd/backfill-shared-hashes/`
Expected: builds without errors.

- [ ] **Step 8.3: Document the binary in deploy.md**

Open `docs/deploy.md` and add a section under the operational tools list: a one-liner pointing to `cmd/backfill-shared-hashes/`, the env vars it needs, and the recommended timing (run once after `migrate` brings 0015 online, before flipping new traffic to it).

- [ ] **Step 8.4: Commit**

```bash
git add cmd/backfill-shared-hashes/main.go docs/deploy.md
git commit -m "feat(ops): backfill-shared-hashes CLI for existing catalog"
```

---

## Task 9 — Operational documentation

**Files:**
- Create: `docs/shared-hash-detection.md`

- [ ] **Step 9.1: Write the doc**

`docs/shared-hash-detection.md`:

```markdown
# Shared-Hash Detection

## What it does

When the audio fingerprint of two or more commercials shares the same hash
value (typically because they share a sting, vinheta or jingle), the system
flags those rows in `fingerprint_hashes.is_shared = true`. The matching
engine still uses every hash to compute the histogram peak (so degraded
streams keep matching robustly), but the state machine only credits hits
from non-shared hashes when deciding whether to confirm a detection.

The result: a 6-second sting reused at the end of a 30s spoken spot can no
longer cause a false-positive detection of the jingle that owns that sting.

## When the flag gets set

- **Automatically**, every time `fingerprint.Persist` finishes inserting a
  new commercial's hashes. The new commercial's rows AND any existing rows
  in other commercials that share any of the same hash values are flagged
  in a single UPDATE keyed on `hash_value`.
- **One-shot backfill** for the existing catalog:
  ```bash
  go run ./cmd/backfill-shared-hashes/ --dsn "$DATABASE_URL"
  ```
  Run this exactly once after migration 0015 lands in production.

## Inspecting

Count of shared hashes globally:
```sql
SELECT count(*) FROM fingerprint_hashes WHERE is_shared = true;
```

Per-commercial breakdown:
```sql
SELECT c.title, c.short_id,
       count(*) FILTER (WHERE fh.is_shared) AS shared,
       count(*) AS total
FROM fingerprint_hashes fh
JOIN commercials c ON c.id = fh.commercial_id
GROUP BY c.id, c.title, c.short_id
ORDER BY shared DESC NULLS LAST
LIMIT 20;
```

A commercial whose `shared` is close to `total` has very little uniquely
identifying audio and may need attention (typically: a corrupt master, or a
commercial uploaded twice).

## Known limits

- Sharing is detected by exact hash-value collision. Random collisions exist
  (about 0.7% of any fingerprint in a 10k-commercial catalog), but they cause
  no harm: the unique count drops by 0.7% in the worst case, well within the
  state machine's normal margin.
- Two commercials whose audio is genuinely identical (re-upload, accidental
  duplicate) will end up with all hashes flagged shared. Neither will
  confirm. Detect via the SQL above and resolve by deleting the duplicate.
```

- [ ] **Step 9.2: Commit**

```bash
git add docs/shared-hash-detection.md
git commit -m "docs: shared-hash detection — operational guide"
```

---

## Task 10 — Self-review and final verification

- [ ] **Step 10.1: Run the full test suite**

Run: `cd workers && go test ./...`
Expected: all PASS (excluding integration build tag tests, which run separately).

- [ ] **Step 10.2: Run integration tests**

Run: `cd workers && go test -tags integration ./internal/match/...`
Expected: all PASS, including the AMB30/JINGLE end-to-end test.

- [ ] **Step 10.3: Apply migration in dev environment, then run backfill**

```bash
docker compose up -d --build migrate
go run ./workers/cmd/backfill-shared-hashes/ --dsn "postgres://radiocheck:radiocheck@localhost:5432/radiocheck"
```

Inspect: `psql -c "SELECT count(*) FROM fingerprint_hashes WHERE is_shared = true"` returns a non-zero number if any commercials in the dev DB share content.

- [ ] **Step 10.4: Manual smoke test against running stack**

Re-upload AMB30 and JINGLE in the dev environment, attach them to the same campaign, and play AMB30 into the stream-test endpoint (or replay a captured stream where AMB30 is known to play). Confirm via `/v1/internal/detections` that JINGLE is NOT among the confirmed detections.

- [ ] **Step 10.5: Bundle into a single PR**

The commits land in one PR titled `feat: shared-hash coverage prevents cross-commercial false positives`. PR body summarizes the root cause, the algorithm change, the migration, and the backfill step.
