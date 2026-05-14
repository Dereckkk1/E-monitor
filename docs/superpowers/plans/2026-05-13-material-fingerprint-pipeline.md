# Material Fingerprint Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the wizard-uploaded materials reach `fingerprint_status='ready'`, enter the matching index, and produce real detections without breaking the existing `commercials`-based pipeline.

**Architecture:** Extend the existing pipeline to be polymorphic over `commercials` and `materials`. The Python fingerprint daemon, Go index loader, sharing subscriber, evidence service, webhook deliverer, supervisor, and reconciler all gain a "materials path" alongside the existing "commercials path". A migration unifies the `short_id` sequence so the matcher index stays globally unique, and removes the `detections.commercial_id` FK that would otherwise reject material-only detections. Campaign attribution for material detections is resolved at write-time via `campaign_materials` joined with stationID + date.

**Tech Stack:** Go (workers, api), Python (asyncio fingerprint daemon), PostgreSQL (pgx + asyncpg), NATS messaging, Docker Compose. Tests: `go test ./...` for Go side, `pytest` for Python side.

---

## Context — Why This Exists

Migration 0016 introduced `materials` as a client-scoped, multi-campaign reusable catalog table. Existing commercials were backfilled into `materials` with the same UUID, but **the detection pipeline (fingerprint daemon → index loader → matcher → evidence writer → webhook) only understands `commercials`**.

Symptom reported 2026-05-13: a material uploaded via the wizard's Step 3 stays at `fingerprint_status='pending'` forever. Database evidence:

| fingerprint_status | count | has_commercial |
|--------------------|-------|----------------|
| `ready` | 9 | 100% (backfilled by migration 0016) |
| `pending` | 3 | 0% (new uploads via wizard) |

Root cause: `fingerprint/fingerprint/main.py:37` only handles `payload["commercial_id"]`. New materials are published with `payload["material_id"]` → KeyError → silently dropped.

But fixing just the daemon is insufficient — the rest of the pipeline (4 more components) would also need to handle materials, or the material reaches `ready` but never produces a detection.

This plan covers the full chain.

---

## Critical Findings From Blast-Radius Audit (4 parallel agents)

| Component | Current state | Material-aware? |
|-----------|---------------|-----------------|
| `fingerprint_hashes` schema | UUID column, **no FK**, partitioned by hash(UUID) | ✅ Already UUID-agnostic |
| Python `broadcast_sim.py`, `generator.py` | Generic audio → hashes | ✅ No commercial coupling |
| Python `persistence.py`, `main.py` | Hard-coded `commercials` table + `commercial_id` payload key | ❌ Needs dual-mode |
| Go `index/loader.go` `LoadAll` + `Subscribe` | INNER JOIN commercials → drops materials | ❌ Needs UNION |
| Go `sharing/subscriber.go` | Queries commercials only | ❌ Needs dual-lookup |
| Go `evidence/service.go:152` | `SELECT commercials WHERE short_id=$1` → on failure inserts NULL → FK violation | ❌ Needs dual-lookup + campaign disambiguation |
| Go `webhook/deliverer.go:136,222` | Same short_id → commercials pattern | ❌ Needs dual-lookup |
| Go `supervisor/supervisor.go:260` `startStationWorker` | Reads `ListReadyByCampaignsForStation` (commercials.campaign_id + target_stations) | ❌ Needs union with materials path |
| Go `supervisor/reconcile.go:99-123` | Same query as supervisor | ❌ Needs same extension (else drift every 30s) |
| Go `supervisor/disambiguation.go LookupForDedup` | commercials JOIN campaigns | ❌ Needs dual-lookup |
| `detections.commercial_id` FK | `REFERENCES commercials(id) NOT NULL` | ❌ Must DROP FK |
| `commercials.short_id` vs `materials.short_id` sequences | Separate `SERIAL` per table → collision possible | ❌ Must unify |
| Downstream detection list queries (5 places) | LEFT JOIN — survive NULL commercial | ✅ Already safe |
| `daily_play_summary` view | Already uses materials LEFT JOIN | ✅ Already safe |
| Frontend `commercial_name` field | Coalesced to empty string | ✅ Already safe |

**Implications:** 8 production source files + 1 migration + 2-3 test files. No worktree-blocking conflicts. The fingerprint_hashes UUID-agnostic schema is a critical asset — no schema change needed there.

---

## Architectural Decisions

**ADR-1: Polymorphic short_id (unified sequence) instead of "shadow commercial on link".**

Rejected approach: insert a `commercials` row whenever a material is linked to a campaign.

Why rejected: `commercials.campaign_id` is single-valued and NOT NULL. A material reused across N campaigns would either need N commercial rows with different UUIDs (breaks the "same UUID" guarantee from migration 0016) or a single row that lies about which campaign owns it. F-90 in `follow-ups-fase2.md` already plans to deprecate `commercials.campaign_id` — going the other direction is wrong.

Chosen: extend the pipeline to handle either table at every hop. Unify `short_id` via a shared sequence so the in-memory matcher never has collisions.

**ADR-2: Campaign attribution for material detections via `campaign_materials.target_stations + station_id + detected_at::date`.**

At detection-write time, the engine has: `material_short_id`, `station_id`, `detected_at`. We resolve `campaign_id` by:

```sql
SELECT cm.campaign_id
FROM materials m
JOIN campaign_materials cm ON cm.material_id = m.id
JOIN campaigns ca ON ca.id = cm.campaign_id
WHERE m.short_id = $1
  AND $2 = ANY(cm.target_stations)
  AND ca.status IN ('programada','ativa')
  AND $3::date BETWEEN ca.start_date AND ca.end_date
ORDER BY cm.added_at DESC
LIMIT 1
```

Conflict rule when a material is in multiple overlapping campaigns on the same station: pick the most recently added link. This is a deliberately simple rule. Multi-campaign attribution (one detection → multiple campaigns) is **out of scope** here and is a follow-up (call it F-119). Document the limitation in `docs/material-library.md`.

**ADR-3: Drop the `detections.commercial_id` FK; keep the column NOT NULL and the index, but make it polymorphic across `commercials.id` and `materials.id`.**

The column name `commercial_id` stays for backward compat. Code that reads it must handle the case where the UUID is a material id. The pre-existing LEFT JOINs in `detections.go` already do.

**ADR-4: No changes to the matcher engine itself.** The matcher operates on `short_id` only. Once the index has material hashes keyed by `materials.short_id`, the matcher emits matches as before.

---

## File Structure

### New files
- `migrations/0021_unify_short_id_drop_detections_fk.up.sql` — schema migration
- `migrations/0021_unify_short_id_drop_detections_fk.down.sql` — rollback
- `fingerprint/tests/test_persistence_materials.py` — Python tests for dual-mode persistence
- `workers/internal/catalog/materials_catalog_test.go` — Go tests for material catalog helpers
- `scripts/reprocess-pending-materials.sh` — operational task to drain stuck materials
- `docs/material-fingerprint-pipeline.md` — operator-facing documentation (new feature doc, NOT in plano_implementacao.md per CLAUDE.md §2)

### Modified files
- `fingerprint/fingerprint/persistence.py` — add `fetch_material`, `mark_material_status`
- `fingerprint/fingerprint/main.py` — dispatch on `material_id` vs `commercial_id` in payload
- `workers/internal/index/loader.go` — UNION query in `LoadAll` and `Subscribe`
- `workers/internal/sharing/subscriber.go` — dual lookup
- `workers/internal/evidence/service.go` — dual lookup + campaign disambiguation
- `workers/internal/webhook/deliverer.go` — dual lookup for confirmed+retracted
- `workers/internal/supervisor/supervisor.go` — extend `startStationWorker` to include materials
- `workers/internal/supervisor/reconcile.go` — extend reconciler to include materials
- `workers/internal/supervisor/disambiguation.go` — `LookupForDedup` dual lookup
- `workers/internal/catalog/materials.go` — add `ListReadyByCampaignsForStation` (materials variant)
- `workers/internal/catalog/commercials.go:196` — `LookupForDedup` extend to materials
- `docs/follow-ups-fase2.md` — register F-119 (multi-campaign attribution follow-up)

### Untouched (deliberately)
- `workers/internal/ingestor/*.go` — matcher engine is short_id-only, no changes needed
- `frontend/src/**` — already shows `commercial_name` with COALESCE, no changes
- `workers/internal/catalog/detections.go` LEFT JOIN queries — already correct for polymorphism
- `migrations/0019_*` `daily_play_summary` view — already material-aware
- `broadcast_sim.py`, `generator.py` — generic, no commercial coupling

---

## Risk Register

| Risk | Severity | Mitigation |
|------|----------|------------|
| Dropping FK on detections.commercial_id allows orphan inserts | Medium | App-side check before insert: both `commercials.id` and `materials.id` lookups; fail loudly if neither matches. Add `CHECK` constraint via separate migration if needed in a follow-up. |
| Short_id collision when both sequences are unified retroactively | High → Eliminated | Migration sets new sequence to GREATEST(MAX(commercials), MAX(materials)) + 1. Existing rows untouched. |
| Material in multiple overlapping campaigns produces single attribution | Medium | Document as known limitation (F-119). The `ORDER BY cm.added_at DESC LIMIT 1` rule is deterministic and operators can reorder by un/re-linking. |
| Python daemon publishes wrong payload key on `index.reload` → Go subscriber drops | Medium | Both republished payloads carry `material_id` OR `commercial_id`; both Go subscribers handle both. Add a unit test in both layers. |
| Worker reconciler restarts workers because expected list now includes materials but loaded list doesn't | Low | Supervisor's `startStationWorker` is updated to load materials too. Reconciler comparison uses union from both tables. Both ship in the same commit. |
| Migration on prod blocks `commercials.short_id` write traffic during ALTER | Low | `ALTER TABLE ... ALTER COLUMN short_id SET DEFAULT nextval(...)` is metadata-only (no rewrite). Sequence creation is fast. Run during low-traffic window anyway. |
| Reprocess script republishes for an already-`ready` material | Low | Script filters `WHERE fingerprint_status IN ('pending','failed')`. Daemon's `mark_status('generating')` is idempotent on re-run. |
| Index loader UNION query is slow at scale (catalog grows past ~100k hashes) | Low | The catalog today is tiny (~50k hashes total). EXPLAIN ANALYZE before merging; partial indexes on fingerprint_status already exist. Monitor `radiocheck_index_load_duration_seconds` post-deploy. |
| Two-pump fingerprint regeneration for backfilled materials (they exist in both tables) | Low | Materials query filters `m.id NOT IN (SELECT id FROM commercials)` so backfilled rows go through the commercials path only. |

---

## Implementation Phases

Phases are sequential **per file** but Go-side tasks are independent of Python-side tasks until task 14 (integration test). Tasks within a phase can be batched into one commit if trivially small (per CLAUDE.md "frequent commits" guidance, but small not micro).

---

### Phase 0: Worktree + dev environment prep

- [ ] **Step 0.1: Create worktree**

```bash
cd c:/Users/marke/Desktop/Programas/Radiocheck
git worktree add ../Radiocheck-materials-fingerprint -b feat/materials-fingerprint-pipeline master
cd ../Radiocheck-materials-fingerprint
```

Expected: new directory created at `../Radiocheck-materials-fingerprint` with a branch `feat/materials-fingerprint-pipeline` checked out.

- [ ] **Step 0.2: Confirm local stack is up**

```bash
docker compose -f infra/docker/docker-compose.yml ps --status running
```

Expected: rows for `postgres`, `api`, `nats`, `fingerprint`, `minio` showing "running". If any are down: `docker compose -f infra/docker/docker-compose.yml up -d` first.

- [ ] **Step 0.3: Snapshot the failing state for later verification**

```bash
docker compose -f infra/docker/docker-compose.yml exec -T postgres psql -U radiocheck -d radiocheck -c "
  SELECT id, title, fingerprint_status, fingerprint_hash_count, created_at
  FROM materials
  WHERE fingerprint_status != 'ready'
  ORDER BY created_at DESC;
"
```

Expected: 3 rows with `pending` status, all created after 2026-05-12. Save the IDs — they're the verification targets for Phase 8.

---

### Phase 1: Migration (sequence unification + FK removal)

**Files:**
- Create: `migrations/0021_unify_short_id_drop_detections_fk.up.sql`
- Create: `migrations/0021_unify_short_id_drop_detections_fk.down.sql`

- [ ] **Step 1.1: Read the existing migration runner convention**

```bash
ls migrations/ | tail -5
```

Expected: highest numbered migration is `0020_*` or similar. Use the next number.

- [ ] **Step 1.2: Write the up migration**

File: `migrations/0021_unify_short_id_drop_detections_fk.up.sql`

```sql
-- 0021_unify_short_id_drop_detections_fk.up.sql
-- Two changes that unlock the materials fingerprint pipeline (see
-- docs/superpowers/plans/2026-05-13-material-fingerprint-pipeline.md):
--
--   1. Unify short_id sequence across commercials + materials. Both tables
--      currently have independent SERIAL sequences. Once materials enter
--      the in-memory matcher index alongside commercials, two rows with the
--      same short_id (one in each table) would collide. We move both to a
--      single shared sequence, seeded above current MAX.
--
--   2. Drop the detections.commercial_id FK to commercials. The column stays
--      NOT NULL and indexed; it just becomes polymorphic — pointing at
--      commercials.id OR materials.id. Downstream LEFT JOINs already handle
--      missing commercial rows gracefully (see catalog/detections.go).

BEGIN;

-- ────── 1. Shared sequence ──────

CREATE SEQUENCE catalog_short_id_seq AS INTEGER;

-- Seed above MAX of both existing sequences so no future nextval()
-- collides with an existing short_id.
SELECT setval(
    'catalog_short_id_seq',
    GREATEST(
        COALESCE((SELECT MAX(short_id) FROM commercials), 0),
        COALESCE((SELECT MAX(short_id) FROM materials), 0)
    )
);

-- Repoint both tables to the shared sequence. Existing rows are not
-- rewritten — only the DEFAULT changes, plus we orphan the old per-table
-- sequences (kept around for rollback, dropped in a future cleanup).
ALTER TABLE commercials
    ALTER COLUMN short_id SET DEFAULT nextval('catalog_short_id_seq');

ALTER TABLE materials
    ALTER COLUMN short_id SET DEFAULT nextval('catalog_short_id_seq');

-- The old SERIAL-generated sequences (commercials_short_id_seq,
-- materials_short_id_seq) are intentionally NOT dropped: they remain owned
-- by the columns and would only matter if the down migration runs. The
-- ownership rebinding happens via SET DEFAULT above.

-- ────── 2. Drop detections.commercial_id FK ──────

-- The constraint name is auto-generated by Postgres. We look it up via
-- information_schema to avoid hard-coding a name that might differ between
-- environments (e.g. test fixture vs prod backup-restore).
DO $$
DECLARE
    fk_name TEXT;
BEGIN
    SELECT tc.constraint_name INTO fk_name
    FROM information_schema.table_constraints tc
    JOIN information_schema.key_column_usage kcu
      ON tc.constraint_name = kcu.constraint_name
    WHERE tc.table_name = 'detections'
      AND tc.constraint_type = 'FOREIGN KEY'
      AND kcu.column_name = 'commercial_id';

    IF fk_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE detections DROP CONSTRAINT %I', fk_name);
    END IF;
END $$;

-- An index already exists on detections.commercial_id via the table layout
-- (see migration 0001); we keep it for lookup performance regardless of FK.

COMMIT;
```

- [ ] **Step 1.3: Write the down migration**

File: `migrations/0021_unify_short_id_drop_detections_fk.down.sql`

```sql
-- 0021_unify_short_id_drop_detections_fk.down.sql
-- Reverse of 0021. Restores the FK and reverts both tables to their original
-- per-table SERIAL sequences. Existing short_id values are preserved.

BEGIN;

-- ────── 1. Restore per-table sequence defaults ──────

ALTER TABLE commercials
    ALTER COLUMN short_id SET DEFAULT nextval('commercials_short_id_seq');

ALTER TABLE materials
    ALTER COLUMN short_id SET DEFAULT nextval('materials_short_id_seq');

-- Sync the per-table sequences forward so subsequent inserts don't reuse
-- a value that may have been issued from the shared sequence while it was
-- active. setval(seq, max) makes the NEXT nextval() return max+1.
SELECT setval(
    pg_get_serial_sequence('commercials', 'short_id'),
    COALESCE((SELECT MAX(short_id) FROM commercials), 0)
);

SELECT setval(
    pg_get_serial_sequence('materials', 'short_id'),
    COALESCE((SELECT MAX(short_id) FROM materials), 0)
);

DROP SEQUENCE catalog_short_id_seq;

-- ────── 2. Re-add detections.commercial_id FK ──────

-- This will FAIL if there are detections with commercial_id pointing at
-- a materials.id that doesn't also exist in commercials. Operators should
-- delete or migrate those rows before running the down.
ALTER TABLE detections
    ADD CONSTRAINT detections_commercial_id_fkey
    FOREIGN KEY (commercial_id) REFERENCES commercials(id);

COMMIT;
```

- [ ] **Step 1.4: Apply the migration locally**

```bash
docker compose -f infra/docker/docker-compose.yml restart migrate
docker compose -f infra/docker/docker-compose.yml logs migrate --tail=20
```

Expected: log line "applied 0021_unify_short_id_drop_detections_fk".

- [ ] **Step 1.5: Verify schema state**

```bash
docker compose -f infra/docker/docker-compose.yml exec -T postgres psql -U radiocheck -d radiocheck -c "
  SELECT pg_get_serial_sequence('commercials','short_id') AS com_seq,
         pg_get_serial_sequence('materials','short_id')   AS mat_seq;
  SELECT conname FROM pg_constraint
  WHERE conrelid = 'detections'::regclass AND contype = 'f';
"
```

Expected: both `com_seq` and `mat_seq` show some sequence (the column-bound sequences still exist), AND the DEFAULT for both columns is `nextval('catalog_short_id_seq'::regclass)` (verify with `\d commercials` and `\d materials`). The FK constraint list for detections should NOT include `commercial_id`.

- [ ] **Step 1.6: Commit**

```bash
git add migrations/0021_unify_short_id_drop_detections_fk.up.sql \
        migrations/0021_unify_short_id_drop_detections_fk.down.sql
git commit -m "migrate(0021): unify short_id seq + drop detections.commercial_id FK

Prepares the schema for the materials fingerprint pipeline:
- Shared sequence catalog_short_id_seq seeded above MAX of both tables
- commercials.short_id and materials.short_id now both DEFAULT to it
- detections.commercial_id FK to commercials dropped (column stays NOT NULL,
  now polymorphic across commercials and materials)

See docs/superpowers/plans/2026-05-13-material-fingerprint-pipeline.md
"
```

---

### Phase 2: Python daemon — dual-mode persistence

**Files:**
- Modify: `fingerprint/fingerprint/persistence.py`
- Create: `fingerprint/tests/test_persistence_materials.py`

- [ ] **Step 2.1: Write a failing test for fetch_material**

File: `fingerprint/tests/test_persistence_materials.py`

```python
"""
Tests for the materials-table variants of persistence helpers. Uses a
mock asyncpg pool with the same shape as the real one so we don't need a
live database for unit tests; integration tests against the real DB
happen via docker-compose in Phase 8.
"""

import pytest
from unittest.mock import AsyncMock, MagicMock

from fingerprint.persistence import (
    fetch_material,
    mark_material_status,
)


@pytest.fixture
def mock_pool():
    """An asyncpg.Pool stand-in with controllable fetchrow/execute."""
    pool = MagicMock()
    conn = AsyncMock()
    # async with pool.acquire() as conn:  pattern
    cm = AsyncMock()
    cm.__aenter__.return_value = conn
    cm.__aexit__.return_value = None
    pool.acquire = MagicMock(return_value=cm)
    return pool, conn


@pytest.mark.asyncio
async def test_fetch_material_returns_dict(mock_pool):
    pool, conn = mock_pool
    conn.fetchrow.return_value = {
        "id": "00000000-0000-0000-0000-000000000001",
        "master_storage_path": "/data/masters/abc.mp3",
        "duration_seconds": 30.0,
    }
    result = await fetch_material(pool, "00000000-0000-0000-0000-000000000001")
    assert result["master_storage_path"] == "/data/masters/abc.mp3"
    # Confirm the query targets the materials table, not commercials
    sql_used = conn.fetchrow.call_args[0][0]
    assert "FROM materials" in sql_used
    assert "FROM commercials" not in sql_used


@pytest.mark.asyncio
async def test_fetch_material_raises_when_missing(mock_pool):
    pool, conn = mock_pool
    conn.fetchrow.return_value = None
    with pytest.raises(LookupError, match="material .* not found"):
        await fetch_material(pool, "00000000-0000-0000-0000-000000000099")


@pytest.mark.asyncio
async def test_mark_material_status_ready_writes_hash_count(mock_pool):
    pool, conn = mock_pool
    await mark_material_status(pool, "uuid-x", "ready", hash_count=42)
    assert conn.execute.called
    sql_used = conn.execute.call_args[0][0]
    assert "UPDATE materials" in sql_used
    assert "fingerprint_hash_count" in sql_used


@pytest.mark.asyncio
async def test_mark_material_status_pending_skips_hash_count(mock_pool):
    pool, conn = mock_pool
    await mark_material_status(pool, "uuid-x", "generating")
    sql_used = conn.execute.call_args[0][0]
    assert "UPDATE materials" in sql_used
    assert "fingerprint_hash_count" not in sql_used
```

- [ ] **Step 2.2: Run the failing test**

```bash
cd fingerprint
python -m pytest tests/test_persistence_materials.py -v
```

Expected: ImportError or AttributeError — functions don't exist yet.

- [ ] **Step 2.3: Implement `fetch_material` and `mark_material_status`**

File: `fingerprint/fingerprint/persistence.py` — append after the existing `mark_status` function:

```python
async def fetch_material(pool: asyncpg.Pool, material_id: str) -> dict:
    async with pool.acquire() as conn:
        row = await conn.fetchrow(
            """
            SELECT id, master_storage_path, duration_seconds
            FROM materials
            WHERE id = $1
            """,
            material_id,
        )
    if row is None:
        raise LookupError(f"material {material_id} not found")
    return dict(row)


async def mark_material_status(pool: asyncpg.Pool, material_id: str, status: str,
                                hash_count: int | None = None) -> None:
    async with pool.acquire() as conn:
        if status == "ready":
            await conn.execute(
                """
                UPDATE materials
                SET fingerprint_status = $2,
                    fingerprint_generated_at = NOW(),
                    fingerprint_hash_count = $3,
                    updated_at = NOW()
                WHERE id = $1
                """,
                material_id, status, hash_count,
            )
        else:
            await conn.execute(
                """
                UPDATE materials
                SET fingerprint_status = $2,
                    updated_at = NOW()
                WHERE id = $1
                """,
                material_id, status,
            )
```

- [ ] **Step 2.4: Re-run tests, expect green**

```bash
python -m pytest tests/test_persistence_materials.py -v
```

Expected: 4 passing tests.

- [ ] **Step 2.5: Commit**

```bash
git add fingerprint/fingerprint/persistence.py fingerprint/tests/test_persistence_materials.py
git commit -m "feat(fingerprint): fetch_material + mark_material_status

Dual-mode persistence helpers for the new materials table, mirroring
the existing fetch_commercial / mark_status. No changes to the existing
helpers — both paths coexist.
"
```

---

### Phase 3: Python daemon — dispatch on payload shape

**Files:**
- Modify: `fingerprint/fingerprint/main.py`
- Modify: `fingerprint/tests/test_persistence_materials.py` (add dispatch test)

- [ ] **Step 3.1: Write a failing test for the dispatcher**

Append to `fingerprint/tests/test_persistence_materials.py`:

```python
import json
from unittest.mock import patch
from fingerprint.main import handle_generate


@pytest.mark.asyncio
async def test_handle_generate_routes_material_payload(mock_pool, tmp_path):
    pool, conn = mock_pool
    # 1. fetch_material returns a valid row
    audio_file = tmp_path / "test.mp3"
    audio_file.write_bytes(b"fake")
    conn.fetchrow.return_value = {
        "id": "mat-uuid",
        "master_storage_path": str(audio_file),
        "duration_seconds": 30.0,
    }
    nc = AsyncMock()
    msg = MagicMock()
    msg.data = json.dumps({"material_id": "mat-uuid"}).encode()

    # Patch heavy audio work — we only care that the right path is chosen.
    with patch("fingerprint.main.simulate_variants", return_value={0: b"x"}), \
         patch("fingerprint.main.generate_fingerprint", return_value=[(1, 0)]), \
         patch("fingerprint.main.write_hashes", new_callable=AsyncMock):
        await handle_generate(msg, pool, nc)

    # Confirm UPDATE materials was called (mark_material_status), not commercials
    update_sqls = [c[0][0] for c in conn.execute.call_args_list]
    assert any("UPDATE materials" in s for s in update_sqls)
    assert not any("UPDATE commercials" in s for s in update_sqls)

    # Confirm republished payload uses material_id
    publish_payloads = [c[0][1] for c in nc.publish.call_args_list]
    decoded = [json.loads(p.decode()) for p in publish_payloads]
    assert any("material_id" in d for d in decoded)
```

- [ ] **Step 3.2: Run, confirm failure**

```bash
python -m pytest tests/test_persistence_materials.py::test_handle_generate_routes_material_payload -v
```

Expected: KeyError on `payload["commercial_id"]` — the current dispatcher only handles that key.

- [ ] **Step 3.3: Rewrite `handle_generate` for polymorphic dispatch**

File: `fingerprint/fingerprint/main.py` — replace the existing `handle_generate` function (lines 34-88) with:

```python
async def handle_generate(msg, pool: asyncpg.Pool, nc: nats.NATS):
    try:
        payload = json.loads(msg.data.decode())
    except Exception as e:
        log.error("invalid JSON payload: %s", e)
        return

    # Support both legacy commercial_id and new material_id payloads.
    if "material_id" in payload:
        entity_id = payload["material_id"]
        entity_kind = "material"
        fetcher = fetch_material
        marker = mark_material_status
        reload_key = "material_id"
    elif "commercial_id" in payload:
        entity_id = payload["commercial_id"]
        entity_kind = "commercial"
        fetcher = fetch_commercial
        marker = mark_status
        reload_key = "commercial_id"
    else:
        log.error("payload missing both material_id and commercial_id: %s", payload)
        return

    log.info("processing %s_id=%s", entity_kind, entity_id)

    try:
        entity = await fetcher(pool, entity_id)
    except Exception as e:
        log.error("fetch_%s failed: %s", entity_kind, e)
        return

    master_path = entity["master_storage_path"]
    if not os.path.isfile(master_path):
        log.error("master file not found: %s", master_path)
        await marker(pool, entity_id, "failed")
        return

    await marker(pool, entity_id, "generating")

    try:
        variants = simulate_variants(master_path)
    except Exception as e:
        log.exception("broadcast_sim failed: %s", e)
        await marker(pool, entity_id, "failed")
        return

    total_hashes = 0
    try:
        for variant_id, audio in variants.items():
            hashes = generate_fingerprint(audio)
            await write_hashes(pool, entity_id, variant_id, rate_id=0, hashes=hashes)
            total_hashes += len(hashes)
    except Exception as e:
        log.exception("generate/write failed: %s", e)
        await marker(pool, entity_id, "failed")
        return

    await marker(pool, entity_id, "ready", hash_count=total_hashes)

    # Republish both the index reload and the shared-scan trigger using the
    # SAME key the upstream sent (material_id or commercial_id). Go-side
    # subscribers handle both shapes (see workers/internal/index/loader.go
    # and workers/internal/sharing/subscriber.go).
    reload_payload = json.dumps({reload_key: entity_id}).encode()
    await nc.publish(SUBJECT_INDEX_RELOAD, reload_payload)
    await nc.publish(SUBJECT_SHARED_SCAN, reload_payload)

    log.info("done %s_id=%s hashes=%d", entity_kind, entity_id, total_hashes)
```

Add to imports at the top:

```python
from fingerprint.persistence import (
    fetch_commercial,
    fetch_material,
    write_hashes,
    mark_status,
    mark_material_status,
)
```

- [ ] **Step 3.4: Run tests**

```bash
python -m pytest tests/ -v
```

Expected: all tests pass, including the new dispatch test.

- [ ] **Step 3.5: Commit**

```bash
git add fingerprint/fingerprint/main.py fingerprint/tests/test_persistence_materials.py
git commit -m "feat(fingerprint): dispatch generate on material_id or commercial_id

Payloads carrying material_id route to fetch_material/mark_material_status.
Payloads with commercial_id keep the old path. Republished index.reload
and fingerprint.shared-scan use the same key the upstream sent so Go
subscribers can react correctly.
"
```

---

### Phase 4: Go index loader — union materials path

**Files:**
- Modify: `workers/internal/index/loader.go`

- [ ] **Step 4.1: Read the existing LoadAll for grounding**

```bash
sed -n '55,100p' workers/internal/index/loader.go
```

You should see the SELECT joining `fingerprint_hashes` × `commercials` × `campaigns`. Hold this in mind for the change.

- [ ] **Step 4.2: Look for an existing unit test against `LoadAll`**

```bash
ls workers/internal/index/
```

Expected: probably `loader.go` only, no test. We'll add tests in Phase 9 once full local docker integration runs.

- [ ] **Step 4.3: Modify `LoadAll` query to UNION materials**

File: `workers/internal/index/loader.go` — replace the SQL block in `LoadAll` (around lines 57-65):

```go
	rows, err := l.db.Query(ctx, `
		-- Path 1: commercials (legacy + backfilled).
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id, fh.is_shared, c.short_id
		FROM fingerprint_hashes fh
		JOIN commercials c  ON c.id  = fh.commercial_id
		JOIN campaigns   ca ON ca.id = c.campaign_id
		WHERE c.fingerprint_status = 'ready'
		  AND ca.status IN `+indexEligibleStatuses+`

		UNION ALL

		-- Path 2: materials (new uploads from the campaign wizard, plus
		-- legacy materials linked via campaign_materials). We exclude any
		-- material whose UUID also exists in commercials to avoid duplicate
		-- entries — those are handled by Path 1.
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id, fh.is_shared, m.short_id
		FROM fingerprint_hashes fh
		JOIN materials m ON m.id = fh.commercial_id
		WHERE m.fingerprint_status = 'ready'
		  AND m.id NOT IN (SELECT id FROM commercials)
		  AND EXISTS (
		      SELECT 1 FROM campaign_materials cm
		      JOIN campaigns ca ON ca.id = cm.campaign_id
		      WHERE cm.material_id = m.id
		        AND ca.status IN `+indexEligibleStatuses+`
		  )
	`)
```

The column projection is identical between the two SELECTs (the matcher consumes `short_id` regardless of source). UNION ALL is safe because Path 1 and Path 2 cover disjoint material sets via the `m.id NOT IN (SELECT id FROM commercials)` filter.

- [ ] **Step 4.4: Modify `Subscribe` (per-entity reload) to handle both payload keys**

In the same file, around lines 102-200 (the `Subscribe` method), replace `reloadPayload` and the body of the subscription callback:

```go
// reloadPayload accepts both legacy commercial_id and new material_id
// keys. Exactly one is expected to be non-empty per message; if both are
// present, commercial_id takes precedence (matches Python daemon behavior).
type reloadPayload struct {
	CommercialID string `json:"commercial_id,omitempty"`
	MaterialID   string `json:"material_id,omitempty"`
}
```

Then in the callback body, replace the existing per-commercial query with a dispatch:

```go
		var payload reloadPayload
		if err := json.Unmarshal(msg.Data, &payload); err != nil {
			l.log.Warn("index.reload: invalid JSON payload",
				zap.Error(err),
				zap.ByteString("data", msg.Data),
			)
			return
		}
		if payload.CommercialID == "" && payload.MaterialID == "" {
			l.log.Warn("index.reload: payload missing both commercial_id and material_id")
			return
		}

		var entityID string
		var shortID int32
		var err error
		if payload.CommercialID != "" {
			entityID = payload.CommercialID
			err = l.db.QueryRow(ctx, `
				SELECT c.short_id FROM commercials c
				JOIN campaigns ca ON ca.id = c.campaign_id
				WHERE c.id = $1
				  AND c.fingerprint_status = 'ready'
				  AND ca.status IN `+indexEligibleStatuses+`
			`, entityID).Scan(&shortID)
		} else {
			entityID = payload.MaterialID
			err = l.db.QueryRow(ctx, `
				SELECT m.short_id FROM materials m
				WHERE m.id = $1
				  AND m.fingerprint_status = 'ready'
				  AND m.id NOT IN (SELECT id FROM commercials)
				  AND EXISTS (
				      SELECT 1 FROM campaign_materials cm
				      JOIN campaigns ca ON ca.id = cm.campaign_id
				      WHERE cm.material_id = m.id
				        AND ca.status IN `+indexEligibleStatuses+`
				  )
			`, entityID).Scan(&shortID)
		}
		if err != nil {
			l.log.Warn("index.reload: entity not eligible or not found",
				zap.String("entity_id", entityID),
				zap.String("kind", map[bool]string{true: "commercial", false: "material"}[payload.CommercialID != ""]),
				zap.Error(err),
			)
			return
		}

		// Fetch fingerprint hashes by UUID (no FK; same UUID works either way).
		rows, err := l.db.Query(ctx, `
			SELECT hash_value, time_frame, variant_id, rate_id, is_shared
			FROM fingerprint_hashes
			WHERE commercial_id = $1
		`, entityID)
		// ... (rest of the existing body unchanged, using shortID and entityID as before)
```

Reuse the rest of the existing callback (the hash collection loop). The variable previously named `payload.CommercialID` is now `entityID`.

- [ ] **Step 4.5: Compile**

```bash
cd workers && go build ./...
```

Expected: clean build, no errors.

- [ ] **Step 4.6: Run existing tests to catch regressions**

```bash
go test ./internal/index/... ./internal/catalog/...
```

Expected: all pass. If any fail, fix before proceeding.

- [ ] **Step 4.7: Commit**

```bash
git add workers/internal/index/loader.go
git commit -m "feat(index): UNION query loads commercials + materials

LoadAll and Subscribe now accept both entity kinds. Materials are
filtered to those NOT also present in commercials (backfilled rows go
through the commercials path) and require at least one linked campaign
in 'programada' or 'ativa' status.
"
```

---

### Phase 5: Go sharing subscriber — dual lookup

**Files:**
- Modify: `workers/internal/sharing/subscriber.go`

- [ ] **Step 5.1: Update the payload struct + lookup**

File: `workers/internal/sharing/subscriber.go` — change the `payload` struct and the lookup logic:

```go
// payload accepts both commercial_id and material_id keys. The Python
// daemon picks the one matching what the API published upstream.
type payload struct {
	CommercialID string `json:"commercial_id,omitempty"`
	MaterialID   string `json:"material_id,omitempty"`
}
```

In the subscription callback (after the JSON unmarshal):

```go
		var entityID uuid.UUID
		var masterPath string
		var status string
		var lookupErr error

		if p.CommercialID != "" {
			entityID, lookupErr = uuid.Parse(p.CommercialID)
			if lookupErr == nil {
				lookupErr = s.pool.QueryRow(bgCtx,
					`SELECT master_storage_path, fingerprint_status FROM commercials WHERE id = $1`,
					entityID,
				).Scan(&masterPath, &status)
			}
		} else if p.MaterialID != "" {
			entityID, lookupErr = uuid.Parse(p.MaterialID)
			if lookupErr == nil {
				lookupErr = s.pool.QueryRow(bgCtx,
					`SELECT master_storage_path, fingerprint_status FROM materials WHERE id = $1`,
					entityID,
				).Scan(&masterPath, &status)
			}
		} else {
			s.log.Warn("shared-scan: payload missing both keys")
			return
		}

		if lookupErr != nil {
			s.log.Warn("shared-scan: lookup failed",
				zap.String("entity_id", entityID.String()),
				zap.Error(lookupErr),
			)
			return
		}
```

The downstream `MarkSharedHashes(bgCtx, s.pool, entityID, masterPath)` call works as before — the function operates on `fingerprint_hashes.commercial_id` which is UUID-agnostic.

After flagging, republish with the same key shape:

```go
		var reload []byte
		if p.CommercialID != "" {
			reload, _ = json.Marshal(payload{CommercialID: entityID.String()})
		} else {
			reload, _ = json.Marshal(payload{MaterialID: entityID.String()})
		}
		if err := s.nc.Publish(events.SubjectIndexReload, reload); err != nil {
			// ... existing error handling unchanged
		}
```

- [ ] **Step 5.2: Check `MarkSharedHashes` signature**

```bash
grep -n "func MarkSharedHashes" workers/internal/sharing/*.go
```

Expected: signature is `MarkSharedHashes(ctx, pool, id uuid.UUID, masterPath string)`. Confirm it only reads/writes by UUID — no commercial-specific JOINs inside. If it does have a commercial-specific JOIN, capture the file:line and add a follow-up task (likely a few more SQL tweaks).

- [ ] **Step 5.3: Build + test**

```bash
cd workers && go build ./... && go test ./internal/sharing/...
```

Expected: clean.

- [ ] **Step 5.4: Commit**

```bash
git add workers/internal/sharing/subscriber.go
git commit -m "feat(sharing): dual-lookup subscriber for commercials + materials"
```

---

### Phase 6: Go supervisor + reconciler + catalog — list materials too

**Files:**
- Modify: `workers/internal/catalog/materials.go` (add `ListReadyByCampaignsForStation`)
- Modify: `workers/internal/supervisor/supervisor.go`
- Modify: `workers/internal/supervisor/reconcile.go`
- Modify: `workers/internal/supervisor/disambiguation.go` (LookupForDedup)

- [ ] **Step 6.1: Add a `ListReadyByCampaignsForStation` for materials**

File: `workers/internal/catalog/materials.go` — append a new method on the `Materials` repo (placement: after the existing `ListByClient`):

```go
// ReadyMaterialForWorker holds the per-worker fields needed by the
// supervisor's worker setup. Only the matcher-relevant subset, not the
// full Material struct.
type ReadyMaterialForWorker struct {
	ID              uuid.UUID
	ShortID         int32
	DurationSeconds float64
}

// ListReadyByCampaignsForStation returns ready materials that are linked
// (via campaign_materials) to any of the given campaigns AND that include
// the given station in that link's target_stations array. Backfilled
// materials whose UUID also exists in commercials are EXCLUDED — those go
// through the commercials path. Mirrors commercials.ListReadyByCampaignsForStation.
func (m *Materials) ListReadyByCampaignsForStation(ctx context.Context, campaignIDs []uuid.UUID, stationID uuid.UUID) ([]ReadyMaterialForWorker, error) {
	if len(campaignIDs) == 0 {
		return nil, nil
	}
	rows, err := m.pool.Query(ctx, `
		SELECT DISTINCT mat.id, mat.short_id, mat.duration_seconds
		FROM materials mat
		JOIN campaign_materials cm ON cm.material_id = mat.id
		WHERE cm.campaign_id = ANY($1)
		  AND mat.fingerprint_status = 'ready'
		  AND $2 = ANY(cm.target_stations)
		  AND mat.id NOT IN (SELECT id FROM commercials)`,
		campaignIDs, stationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadyMaterialForWorker
	for rows.Next() {
		var r ReadyMaterialForWorker
		if err := rows.Scan(&r.ID, &r.ShortID, &r.DurationSeconds); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
```

- [ ] **Step 6.2: Wire it into supervisor `startStationWorker`**

File: `workers/internal/supervisor/supervisor.go` — find `startStationWorker` (around line 260). After the `s.commercials.ListReadyByCampaignsForStation(...)` call, add:

```go
	mats, err := s.materials.ListReadyByCampaignsForStation(ctx, activeCampaignIDs, stationID)
	if err != nil {
		return fmt.Errorf("list ready materials: %w", err)
	}
	for _, m := range mats {
		// Convert to the same shape the worker expects. The worker doesn't
		// care whether short_id originated from commercials or materials —
		// it just uses it as the match key.
		shortIDs = append(shortIDs, m.ShortID)
		frames[m.ShortID] = totalFrames(m.DurationSeconds)
	}
```

You'll need to add a `Materials *catalog.Materials` field to the `Supervisor` struct and wire it through `NewSupervisor`. Update the call site in `workers/cmd/api/main.go` accordingly:

```go
sup := supervisor.NewSupervisor(supervisor.Config{
    // ... existing fields ...
    Materials: matsRepo,
})
```

Search the file for how `Commercials` is wired and mirror that exactly.

- [ ] **Step 6.3: Mirror the same change in the reconciler**

File: `workers/internal/supervisor/reconcile.go` — in `reconcileOnce`, after building `wantedIDs` from commercials, append material short_ids:

```go
	mats, err := s.materials.ListReadyByCampaignsForStation(queryCtx, activeIDs, stationID)
	if err != nil {
		s.log.Warn("reconciler: list materials failed",
			zap.String("station_id", stationID.String()),
			zap.Error(err),
		)
	} else {
		for _, m := range mats {
			wantedIDs = append(wantedIDs, m.ShortID)
		}
	}
```

- [ ] **Step 6.4: Extend `LookupForDedup`**

File: `workers/internal/supervisor/disambiguation.go` — `LookupForDedup` currently queries commercials by short_id. Add a fallback to materials:

```go
// Inside LookupForDedup, after the existing commercials query:
if errors.Is(err, pgx.ErrNoRows) {
    // Maybe this short_id is a material — try the materials path.
    err = s.db.QueryRow(ctx, `
        SELECT m.id,
               ca.client_id,
               m.duration_seconds,
               COALESCE(ca.dedup_window_seconds, 5)
        FROM materials m
        JOIN campaign_materials cm ON cm.material_id = m.id
        JOIN campaigns ca           ON ca.id = cm.campaign_id
        WHERE m.short_id = $1
          AND m.fingerprint_status = 'ready'
          AND ca.status IN ('programada','ativa')
        ORDER BY cm.added_at DESC
        LIMIT 1
    `, shortID).Scan(&info.CommercialID, &info.ClientID, &info.DurationSeconds, &info.DedupWindowSeconds)
}
```

Note: `info.CommercialID` here is a polymorphic UUID — it can be a material UUID. The dedup logic downstream only cares about uniqueness, not which table the UUID points to.

- [ ] **Step 6.5: Build + test**

```bash
cd workers && go build ./... && go test ./internal/supervisor/... ./internal/catalog/...
```

Expected: clean. The existing supervisor tests should still pass; if `NewSupervisor` signature changed, fix the test setup.

- [ ] **Step 6.6: Commit**

```bash
git add workers/internal/catalog/materials.go \
        workers/internal/supervisor/supervisor.go \
        workers/internal/supervisor/reconcile.go \
        workers/internal/supervisor/disambiguation.go \
        workers/cmd/api/main.go
git commit -m "feat(supervisor): include materials in worker load + reconcile

Workers now expect material short_ids alongside commercial ones.
Reconciler compares the union. LookupForDedup falls back to materials
when the short_id doesn't resolve in commercials. NewSupervisor takes
a Materials repo argument; main.go updated accordingly.
"
```

---

### Phase 7: Go evidence service + webhook deliverer — dual lookup at detection write

**Files:**
- Modify: `workers/internal/evidence/service.go`
- Modify: `workers/internal/webhook/deliverer.go`

- [ ] **Step 7.1: Evidence service — dual lookup with campaign disambiguation**

File: `workers/internal/evidence/service.go` — around line 152, replace the single commercials query:

```go
var commercialID, campaignID uuid.UUID
detectedAtDate := ev.DetectedAt // time.Time
row := s.db.QueryRow(ctx, `
    SELECT c.id, c.campaign_id
    FROM commercials c
    WHERE c.short_id = $1
      AND c.fingerprint_status = 'ready'
    LIMIT 1
`, ev.CommercialShortID)
err := row.Scan(&commercialID, &campaignID)

if errors.Is(err, pgx.ErrNoRows) {
    // Fall back to materials. campaign_id is derived from the linked
    // campaign that targets this station and is currently active on
    // the date the match fired. If multiple match, pick the most
    // recently added link — multi-attribution is a future feature
    // (follow-up F-119).
    row = s.db.QueryRow(ctx, `
        SELECT m.id, cm.campaign_id
        FROM materials m
        JOIN campaign_materials cm ON cm.material_id = m.id
        JOIN campaigns ca           ON ca.id = cm.campaign_id
        WHERE m.short_id = $1
          AND $2 = ANY(cm.target_stations)
          AND ca.status IN ('programada','ativa')
          AND $3::date BETWEEN ca.start_date AND ca.end_date
        ORDER BY cm.added_at DESC
        LIMIT 1
    `, ev.CommercialShortID, ev.StationID, detectedAtDate)
    err = row.Scan(&commercialID, &campaignID)
}

if err != nil {
    s.log.Warn("evidence: failed to resolve short_id to commercial or material",
        zap.Int32("short_id", ev.CommercialShortID),
        zap.String("station_id", ev.StationID.String()),
        zap.Error(err),
    )
    return // drop the detection — better than inserting uuid.Nil
}
```

Remove the old `commercialID = uuid.Nil` fallback (it never worked anyway — FK violation).

- [ ] **Step 7.2: Webhook deliverer — confirmed payload**

File: `workers/internal/webhook/deliverer.go` — around line 136-143, the `SELECT c.id, c.title, ca.client_id FROM commercials...` query. Wrap it with the same dual-lookup pattern:

```go
var entityID uuid.UUID
var title string
var clientID uuid.UUID
err := d.pool.QueryRow(ctx, `
    SELECT c.id, COALESCE(c.title, ''),
           COALESCE(ca.client_id, '00000000-0000-0000-0000-000000000000'::uuid)
    FROM commercials c
    LEFT JOIN campaigns ca ON ca.id = c.campaign_id
    WHERE c.short_id = $1 AND c.fingerprint_status = 'ready'
    LIMIT 1
`, shortID).Scan(&entityID, &title, &clientID)

if errors.Is(err, pgx.ErrNoRows) {
    err = d.pool.QueryRow(ctx, `
        SELECT m.id, COALESCE(m.title, ''), c.client_id
        FROM materials m
        JOIN campaign_materials cm ON cm.material_id = m.id
        JOIN campaigns ca           ON ca.id = cm.campaign_id
        JOIN clients c              ON c.id = ca.client_id
        WHERE m.short_id = $1 AND m.fingerprint_status = 'ready'
        ORDER BY cm.added_at DESC
        LIMIT 1
    `, shortID).Scan(&entityID, &title, &clientID)
}

if err != nil {
    d.log.Warn("webhook confirmed: failed to resolve short_id",
        zap.Int32("short_id", shortID), zap.Error(err))
    return // skip this webhook
}
```

- [ ] **Step 7.3: Webhook deliverer — retracted payload**

Around line 220-227, mirror the same pattern. The retracted-event lookup query is identical to confirmed except for the event type; apply the same dual-fallback.

- [ ] **Step 7.4: Build + test**

```bash
cd workers && go build ./... && go test ./internal/evidence/... ./internal/webhook/...
```

Expected: clean.

- [ ] **Step 7.5: Commit**

```bash
git add workers/internal/evidence/service.go workers/internal/webhook/deliverer.go
git commit -m "feat(detection): dual-lookup at write-time for materials

Evidence service and webhook deliverer first try commercials by short_id,
then fall back to materials via campaign_materials when no commercial
row exists. Campaign attribution for materials uses the link to a
currently-active campaign targeting the station. Drops the silent
uuid.Nil fallback that was causing FK violations and lost detections.
"
```

---

### Phase 8: Reprocess stuck materials + smoke test

**Files:**
- Create: `scripts/reprocess-pending-materials.sh`

- [ ] **Step 8.1: Write the operational script**

File: `scripts/reprocess-pending-materials.sh`

```bash
#!/usr/bin/env bash
# reprocess-pending-materials.sh
# Republishes fingerprint.generate for every material currently in
# fingerprint_status='pending' or 'failed'. Used after deploying the
# materials fingerprint pipeline to drain the backlog created by the
# pre-fix wizard upload bug.
#
# Idempotent: the daemon's first action is mark_status('generating'),
# so running this twice causes only one set of work to complete.

set -euo pipefail

COMPOSE_FILE="${COMPOSE_FILE:-infra/docker/docker-compose.yml}"
DB_USER="${POSTGRES_USER:-radiocheck}"
DB_NAME="${POSTGRES_DB:-radiocheck}"
NATS_BOX_IMAGE="${NATS_BOX_IMAGE:-natsio/nats-box:latest}"
NATS_URL="${NATS_URL:-nats://nats:4222}"

# Pull the list of material IDs to reprocess.
mapfile -t IDS < <(
    docker compose -f "$COMPOSE_FILE" exec -T postgres \
        psql -U "$DB_USER" -d "$DB_NAME" -tAc "
            SELECT id FROM materials
            WHERE fingerprint_status IN ('pending','failed')
            ORDER BY created_at;
        "
)

if [ "${#IDS[@]}" -eq 0 ]; then
    echo "no pending/failed materials — nothing to do."
    exit 0
fi

echo "reprocessing ${#IDS[@]} material(s):"
printf '  - %s\n' "${IDS[@]}"

# Publish one nats message per material. We piggyback on the same docker
# network the fingerprint daemon is on so nats:4222 resolves.
for id in "${IDS[@]}"; do
    payload=$(printf '{"material_id":"%s"}' "$id")
    docker run --rm --network "$(docker compose -f "$COMPOSE_FILE" \
        config --format json | grep -o '"name": "[^"]*radiocheck_default"' | \
        head -1 | cut -d'"' -f4 || echo radiocheck_default)" \
        "$NATS_BOX_IMAGE" nats pub --server="$NATS_URL" \
        fingerprint.generate "$payload"
done

echo "done. monitor with:"
echo "  docker compose -f $COMPOSE_FILE logs -f fingerprint"
```

Make it executable:

```bash
chmod +x scripts/reprocess-pending-materials.sh
```

- [ ] **Step 8.2: Rebuild + restart the affected services**

```bash
docker compose -f infra/docker/docker-compose.yml build api fingerprint
docker compose -f infra/docker/docker-compose.yml up -d --no-deps --force-recreate api fingerprint
```

Note: `--no-deps` per CLAUDE.md §4.1 to avoid recreating postgres.

- [ ] **Step 8.3: Verify both services are healthy**

```bash
docker compose -f infra/docker/docker-compose.yml ps api fingerprint
docker compose -f infra/docker/docker-compose.yml logs --tail=30 api fingerprint
```

Expected: both running, no panics in logs. The api should log "subscribed to index.reload" and "sharing subscribe ok". The fingerprint daemon should log "subscribed to fingerprint.generate".

- [ ] **Step 8.4: Run the reprocess script**

```bash
./scripts/reprocess-pending-materials.sh
```

Expected output: lists 3 material IDs, then "done." Followed by logs in the fingerprint container showing "processing material_id=..." for each, ending with "done material_id=... hashes=N".

- [ ] **Step 8.5: Confirm all 3 materials reached ready**

```bash
docker compose -f infra/docker/docker-compose.yml exec -T postgres psql -U radiocheck -d radiocheck -c "
  SELECT id, title, fingerprint_status, fingerprint_hash_count
  FROM materials
  WHERE id IN ($(echo "'<id1>','<id2>','<id3>'"));
"
```

Replace `<idN>` with the IDs captured in Step 0.3. Expected: all 3 show `fingerprint_status='ready'` and `fingerprint_hash_count > 0`.

- [ ] **Step 8.6: Confirm the matcher index picked them up**

```bash
docker compose -f infra/docker/docker-compose.yml logs api --tail=200 | grep -E "(index loaded|index.reload)"
```

Expected: log entries `index.reload: reload ok material_id=...` for each material, plus an `index loaded hashes=N` line where N reflects the new total.

- [ ] **Step 8.7: Upload a brand-new material via the wizard (smoke test)**

```
1. Open http://localhost:3000
2. Login
3. Navigate to /campaigns/<existing-campaign-id>/edit
4. Click step "Materiais" → "Adicionar material" → "Upload" tab
5. Drop in a 5–30s WAV/MP3
6. Set title + type
7. Click "Subir e vincular"
8. Wait ~5 seconds, then refresh the materials list
```

Expected: the new card flips from "Pendente" → "Gerando" → "Pronto" within ~10 seconds. The "Pronto" badge appears with a green dot.

- [ ] **Step 8.8: Commit**

```bash
git add scripts/reprocess-pending-materials.sh
git commit -m "ops: reprocess-pending-materials.sh

Drains the fingerprint_status=pending/failed backlog by republishing
fingerprint.generate for each. Idempotent and used right after deploying
the materials fingerprint pipeline.
"
```

---

### Phase 9: Documentation + follow-up registration

**Files:**
- Create: `docs/material-fingerprint-pipeline.md`
- Modify: `docs/material-library.md` (add a "Fingerprint generation" section)
- Modify: `docs/follow-ups-fase2.md` (register F-119)

- [ ] **Step 9.1: Write the operator-facing doc**

File: `docs/material-fingerprint-pipeline.md`

```markdown
# Pipeline de Fingerprint de Materiais

Quando um material novo é subido via wizard (Step 3), o backend salva a row,
publica `fingerprint.generate` no NATS com `{"material_id": "<uuid>"}`, e
o daemon Python (`fingerprint` service) gera os hashes e atualiza
`materials.fingerprint_status` pra `ready`. Os hashes vão pra `fingerprint_hashes`
usando o UUID do material — a tabela aceita qualquer UUID, sem FK.

## Fluxo

1. **Upload** (POST `/v1/internal/materials`): cria `materials` row, publica NATS.
2. **Daemon Python** (`fingerprint/main.py`): assina `fingerprint.generate`,
   roteia por `material_id` ou `commercial_id`, gera variantes de broadcast,
   escreve hashes, marca `ready`, publica `index.reload` + `fingerprint.shared-scan`.
3. **Index loader** (`workers/internal/index/loader.go`): hot-reload puxa
   os novos hashes pro mapa em memória.
4. **Supervisor + reconciler** (`workers/internal/supervisor/`): adicionam
   o `short_id` do material à lista do worker da estação. Reconciler de 30s
   detecta o drift e restart só se necessário.
5. **Match → detection**: quando o matcher emite uma match, evidence service
   resolve o `short_id` em `commercials` primeiro, depois `materials`
   (via `campaign_materials.target_stations` + data de detecção).

## Atribuição multi-campanha

Um mesmo material pode estar vinculado a N campanhas via `campaign_materials`.
Se uma detecção bate numa estação coberta por mais de uma campanha ativa
do mesmo material, a atribuição vai pra **campanha mais recentemente vinculada**
(`ORDER BY campaign_materials.added_at DESC LIMIT 1`).

Multi-atribuição (uma detecção contar pra múltiplas campanhas
simultaneamente) é follow-up **F-119**.

## Comandos úteis

- **Reprocessar materiais travados em pending/failed:**
  ```bash
  ./scripts/reprocess-pending-materials.sh
  ```

- **Ver hashes de um material:**
  ```sql
  SELECT variant_id, rate_id, COUNT(*) FROM fingerprint_hashes
  WHERE commercial_id = '<material-uuid>'
  GROUP BY variant_id, rate_id;
  ```

- **Ver atribuição que seria escolhida pra uma detecção:**
  ```sql
  SELECT cm.campaign_id, ca.name, cm.added_at
  FROM materials m
  JOIN campaign_materials cm ON cm.material_id = m.id
  JOIN campaigns ca           ON ca.id = cm.campaign_id
  WHERE m.short_id = <short_id>
    AND '<station-uuid>' = ANY(cm.target_stations)
    AND ca.status IN ('programada','ativa')
  ORDER BY cm.added_at DESC;
  ```

## Limitações conhecidas

- F-119: multi-atribuição (uma detecção → várias campanhas)
- F-90: deprecar `commercials.target_stations`, `commercials.campaign_id`
  (pendente de migration). Pipeline atual lê dos dois.
```

- [ ] **Step 9.2: Update `material-library.md`**

Append a section to `docs/material-library.md` after "## Endpoints":

```markdown
## Fingerprint generation

Material upload publishes `fingerprint.generate` to NATS with payload
`{"material_id": "<uuid>"}`. The Python daemon
(`fingerprint/fingerprint/main.py`) consumes it, generates broadcast-sim
variants + hashes, writes to `fingerprint_hashes`, and marks
`materials.fingerprint_status = 'ready'`. Once ready, the in-memory
matcher index hot-reloads to include the new short_id.

See [`docs/material-fingerprint-pipeline.md`](material-fingerprint-pipeline.md)
for the full flow and operational commands.
```

- [ ] **Step 9.3: Register F-119**

Append to `docs/follow-ups-fase2.md` under the appropriate section:

```markdown
- **F-119** — Multi-campaign attribution for materials. When a material is
  linked to N overlapping active campaigns on the same station, the current
  pipeline picks the most recently added link (`ORDER BY campaign_materials.added_at DESC LIMIT 1`).
  A future enhancement should create one detection row per matching campaign
  (or change the schema to support N:M between detection and campaign). See
  ADR-2 in `docs/superpowers/plans/2026-05-13-material-fingerprint-pipeline.md`.
```

- [ ] **Step 9.4: Commit**

```bash
git add docs/material-fingerprint-pipeline.md docs/material-library.md docs/follow-ups-fase2.md
git commit -m "docs: material fingerprint pipeline operator guide + F-119"
```

---

### Phase 10: Integration smoke test in Docker

- [ ] **Step 10.1: Restart full stack to load all changes**

```bash
docker compose -f infra/docker/docker-compose.yml build api fingerprint
docker compose -f infra/docker/docker-compose.yml up -d --no-deps --force-recreate api fingerprint
```

- [ ] **Step 10.2: Run an end-to-end detection test via radio-sim**

```bash
docker compose -f infra/docker/docker-compose.yml exec radio-sim \
    /sim/simulate.sh <station-id> <material-uuid>.mp3
```

Expected: within ~30 seconds, a row appears in `detections` with `commercial_id = <material-uuid>` and the correct `campaign_id`.

- [ ] **Step 10.3: Verify detection write succeeded**

```bash
docker compose -f infra/docker/docker-compose.yml exec -T postgres psql -U radiocheck -d radiocheck -c "
  SELECT d.id, d.commercial_id, d.campaign_id, d.detected_at, d.confidence
  FROM detections d
  ORDER BY d.created_at DESC
  LIMIT 5;
"
```

Expected: most recent rows include the test material's UUID, with a non-null `campaign_id` derived from `campaign_materials`.

- [ ] **Step 10.4: Verify webhook + airtime report show the material**

```bash
curl -H "Authorization: Bearer $TOKEN" \
    "http://localhost:8080/v1/internal/detections?campaign_id=<campaign>&page=1"
```

Expected: detections list includes the test material's title. `commercial_name` is the material's title (via LEFT JOIN to materials in `daily_play_summary`).

- [ ] **Step 10.5: Final commit + branch ready for review**

```bash
git log --oneline master..HEAD
```

Expected: ~9 commits, one per phase. No merge commits.

```bash
git push origin feat/materials-fingerprint-pipeline
```

---

## Verification Checklist (Final)

- [ ] All 3 stuck materials in DB reach `fingerprint_status='ready'` with hash_count > 0
- [ ] A fresh upload via the wizard reaches `ready` within 10 seconds
- [ ] `radiocheck_index_load_duration_seconds` Prometheus metric stays under 1s after deploy (the UNION query shouldn't degrade load time)
- [ ] No regression on existing commercial-upload flow (`POST /commercials`): upload one through the legacy CommercialsHandler and confirm it still reaches ready
- [ ] `docker compose logs api fingerprint --since=10m` shows no ERROR-level lines
- [ ] An end-to-end radio-sim playback produces a detection row in `detections` with `commercial_id` pointing to the material and correct `campaign_id`
- [ ] `worker-commercial-reconciler` metric `radiocheck_worker_reconcile_runs_total` doesn't grow faster than ~2/min/station — if it does, the reconciler is in a restart loop (materials list mismatch)

---

## Rollback Plan

If a critical regression surfaces in prod after deploy:

1. **Don't roll back the migration first.** The schema changes are forward-compatible: existing commercials/materials code paths still work with the unified sequence.
2. Revert the api binary to the previous build:
   ```bash
   docker compose pull api && docker compose up -d --no-deps api
   ```
   (or check out the previous master commit and rebuild)
3. The Python fingerprint daemon's dual-mode dispatch is backward-compatible: `commercial_id` payloads still work, so reverting only the api leaves the daemon in a safe state.
4. Materials uploaded during the broken window remain at `pending` until re-deployed. Run `reprocess-pending-materials.sh` after deploying the fix.
5. If migration rollback is truly necessary (rare): `migrate down` from `0021`. The down migration restores the FK only if all `detections.commercial_id` values still exist in `commercials` — clean any orphan rows first.

---

## What This Plan Does NOT Cover

- Multi-campaign attribution for a single detection (F-119)
- Deprecating `commercials.target_stations` and `commercials.campaign_id` (F-90)
- Migrating existing detections.commercial_id rows to use material UUIDs where applicable
- Updating the legacy `POST /commercials` upload flow (still uses the per-table sequence semantically, but functions correctly thanks to the unified sequence)
- Frontend changes — the existing UI works because of the LEFT JOIN pattern in API responses
