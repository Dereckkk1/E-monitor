# Zona morta do material legado reaproveitado — Plano de Implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fazer `campaign_materials` ser a fonte-da-verdade do matching (índice + carga do worker + atribuição), com `commercials.campaign_id` demovido a fallback legado, para que um material reaproveitado pela biblioteca volte a detectar e seja atribuído à campanha correta.

**Architecture:** Mudança puramente de lógica de query em Go (sem migration). Quatro pontos: `index/loader.go` (LoadAll + Subscribe), `catalog/materials.go` + `catalog/commercials.go` (carga do worker), `evidence/service.go` (ordem da atribuição, extraída pra função testável). Regra única: rota `materials`/`campaign_materials` é primária e não exclui backfill; rota `commercials` é fallback só pra spots sem linha em `materials`.

**Tech Stack:** Go, pgx/v5, Postgres. Testes de integração gateados por `TEST_DATABASE_URL` (skip quando ausente), padrão `newTestDB(t)`.

**Spec:** [docs/superpowers/specs/2026-06-03-reused-material-dead-zone-design.md](../specs/2026-06-03-reused-material-dead-zone-design.md)

---

## File Structure

- `workers/internal/index/loader.go` — modificar `LoadAll` (Path A/B) e o branch de material do `Subscribe`.
- `workers/internal/index/testhelpers_test.go` — **criar** helper `newTestDB` + builders raw-SQL pro cenário backfill.
- `workers/internal/index/loader_reuse_test.go` — **criar** teste de integração do `LoadAll`.
- `workers/internal/catalog/materials.go` — remover exclusão `NOT IN commercials` em `ListReadyByCampaignsForStation`.
- `workers/internal/catalog/commercials.go` — adicionar `id NOT IN (SELECT id FROM materials)` em `ListReadyByCampaignsForStation`.
- `workers/internal/catalog/reuse_dead_zone_test.go` — **criar** testes de carga do worker (materials + commercials).
- `workers/internal/evidence/attribution.go` — **criar** `resolveAttribution` (extração do inline de `service.go`).
- `workers/internal/evidence/service.go` — chamar `resolveAttribution` no `handle`.
- `workers/internal/evidence/attribution_test.go` — **criar** teste de atribuição.

---

## Task 1: Helper de teste do pacote `index`

**Files:**
- Create: `workers/internal/index/testhelpers_test.go`

- [ ] **Step 1: Criar o helper**

```go
package index

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"radiocheck/internal/db"
)

// newTestDB connects to TEST_DATABASE_URL, skipping when unset. Mirrors
// catalog/testhelpers_test.go.
func newTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	return ctx, pool
}

// seedBackfillReuse builds the dead-zone scenario directly via SQL so that the
// commercial and material share one UUID (what migration 0016's backfill
// produced). Returns the shared entity UUID and its short_id.
//
//   - commercial row: campaign_id = concludedCampaign (status 'concluida')
//   - material row:   same UUID, fingerprint_status 'ready'
//   - campaign_materials: links the material to activeCampaign targeting station
//   - fingerprint_hashes: nHashes rows under the shared UUID
//
// All rows are removed on t.Cleanup.
func seedBackfillReuse(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	concludedCampaign, activeCampaign, station uuid.UUID, nHashes int) (uuid.UUID, int32) {
	t.Helper()
	id := uuid.New()
	var shortID int32
	err := pool.QueryRow(ctx, `
		INSERT INTO commercials (id, campaign_id, title, duration_seconds,
		                         master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, 'reuse-spot', 30, '/tmp/r.mp3', 'sha-'||$1::text, 'ready')
		RETURNING short_id`, id, concludedCampaign).Scan(&shortID)
	require.NoError(t, err)

	// Material shares the UUID and short_id (the 0016 backfill behavior).
	_, err = pool.Exec(ctx, `
		INSERT INTO materials (id, short_id, client_id, title, duration_seconds,
		                       master_storage_path, master_sha256, fingerprint_status)
		SELECT $1, $2, ca.client_id, 'reuse-spot', 30, '/tmp/r.mp3', 'sha-'||$1::text, 'ready'
		FROM campaigns ca WHERE ca.id = $3`, id, shortID, activeCampaign)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, ARRAY[$3]::uuid[])`, activeCampaign, id, station)
	require.NoError(t, err)

	for i := 0; i < nHashes; i++ {
		_, err = pool.Exec(ctx, `
			INSERT INTO fingerprint_hashes (commercial_id, variant_id, rate_id, hash_value, time_frame, is_shared)
			VALUES ($1, 0, 0, $2, $3, false)`, id, int64(1000+i), i)
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM fingerprint_hashes WHERE commercial_id = $1`, id)
		pool.Exec(ctx, `DELETE FROM campaign_materials WHERE material_id = $1`, id)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, id)
		pool.Exec(ctx, `DELETE FROM commercials WHERE id = $1`, id)
	})
	return id, shortID
}

// seedCampaign inserts a campaign with explicit status/dates and returns its ID.
func seedCampaign(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	clientID uuid.UUID, status string, start, end string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'reuse-camp-'||$2, $3::date, $4::date, $2)
		RETURNING id`, clientID, status, start, end).Scan(&id)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, id) })
	return id
}
```

- [ ] **Step 2: Compila**

Run: `cd workers && go build ./internal/index/...`
Expected: sem erros (o arquivo `_test.go` não entra no build normal, mas `go vet ./internal/index/...` valida).

Run: `cd workers && go vet ./internal/index/...`
Expected: sem erros.

- [ ] **Step 3: Commit**

```bash
git add workers/internal/index/testhelpers_test.go
git commit -m "test(index): harness do cenário backfill reaproveitado"
```

---

## Task 2: Teste de integração do `LoadAll` (RED)

**Files:**
- Create: `workers/internal/index/loader_reuse_test.go`

- [ ] **Step 1: Escrever o teste que falha**

```go
package index

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestLoadAll_ReusedBackfillMaterial reproduces the prod dead zone: a backfilled
// material (UUID also in commercials, commercial.campaign_id = a CONCLUDED
// campaign) reused via campaign_materials in an ACTIVE campaign. Before the fix
// its hashes land in neither index path; after, they load exactly once.
func TestLoadAll_ReusedBackfillMaterial(t *testing.T) {
	ctx, pool := newTestDB(t)

	var clientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('reuse-client') RETURNING id`).Scan(&clientID))
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID) })

	concluded := seedCampaign(t, ctx, pool, clientID, "concluida", "2026-05-01", "2026-05-31")
	active := seedCampaign(t, ctx, pool, clientID, "ativa", "2026-06-01", "2026-07-31")
	station := uuid.New()

	_, shortID := seedBackfillReuse(t, ctx, pool, concluded, active, station, 5)

	store := NewStore()
	loader := NewLoader(store, pool, nil, zap.NewNop())
	require.NoError(t, loader.LoadAll(ctx))

	// Count entries carrying our short_id across the whole index.
	got := 0
	for _, entries := range store.Load() {
		for _, e := range entries {
			if e.CommercialShortID == shortID {
				got++
			}
		}
	}
	require.Equal(t, 5, got, "expected the 5 reused-material hashes in the index exactly once each")
}
```

- [ ] **Step 2: Rodar e confirmar que FALHA**

Run: `cd workers && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/index/ -run TestLoadAll_ReusedBackfillMaterial -v`
Expected: FAIL — `expected ... got 0` (zona morta: nenhum caminho carrega).
(Se `TEST_DATABASE_URL` não setado: SKIP — então setar antes de validar de verdade.)

- [ ] **Step 3: Commit do teste RED**

```bash
git add workers/internal/index/loader_reuse_test.go
git commit -m "test(index): RED — material reaproveitado some do índice (zona morta)"
```

---

## Task 3: Corrigir `LoadAll` e `Subscribe` (GREEN)

**Files:**
- Modify: `workers/internal/index/loader.go`

- [ ] **Step 1: Reescrever a query do `LoadAll`**

Substituir o bloco `l.db.Query(ctx, ` ... `)` dentro de `LoadAll` (as duas SELECTs unidas por `UNION ALL`) por:

```go
	rows, err := l.db.Query(ctx, `
		-- Path A (primary): materials linked via campaign_materials to an
		-- active/programada campaign. Covers fresh uploads AND backfilled
		-- materials reused across campaigns. campaign_materials is the source
		-- of truth — no "NOT IN commercials" exclusion here.
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id, fh.is_shared, m.short_id
		FROM fingerprint_hashes fh
		JOIN materials m ON m.id = fh.commercial_id
		WHERE m.fingerprint_status = 'ready'
		  AND EXISTS (
		      SELECT 1 FROM campaign_materials cm
		      JOIN campaigns ca ON ca.id = cm.campaign_id
		      WHERE cm.material_id = m.id
		        AND ca.status IN `+indexEligibleStatuses+`
		  )

		UNION ALL

		-- Path B (fallback): pure-legacy commercials with NO material row,
		-- gated on their own campaign status. Backfilled commercials (id in
		-- materials) are handled by Path A; excluding them keeps each short_id
		-- in the index exactly once.
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id, fh.is_shared, c.short_id
		FROM fingerprint_hashes fh
		JOIN commercials c  ON c.id  = fh.commercial_id
		JOIN campaigns   ca ON ca.id = c.campaign_id
		WHERE c.fingerprint_status = 'ready'
		  AND ca.status IN `+indexEligibleStatuses+`
		  AND c.id NOT IN (SELECT id FROM materials)
	`)
```

- [ ] **Step 2: Ajustar o branch de material do `Subscribe`**

No `Subscribe`, na query do branch `else` (material), remover a linha `AND m.id NOT IN (SELECT id FROM commercials)`. A query fica:

```go
			err = l.db.QueryRow(ctx, `
				SELECT m.short_id FROM materials m
				WHERE m.id = $1
				  AND m.fingerprint_status = 'ready'
				  AND EXISTS (
				      SELECT 1 FROM campaign_materials cm
				      JOIN campaigns ca ON ca.id = cm.campaign_id
				      WHERE cm.material_id = m.id
				        AND ca.status IN `+indexEligibleStatuses+`
				  )
			`, entityID).Scan(&shortID)
```

- [ ] **Step 3: Atualizar o comentário-doc do `LoadAll`**

Trocar o comentário acima de `func (l *Loader) LoadAll` para refletir Path A/B (materials primário, commercials fallback legado). Texto:

```go
// LoadAll loads every matchable spot's fingerprints into the index via a single
// UNION query. Path A is primary: materials linked via campaign_materials to an
// active/programada campaign (fresh + backfilled, reused or not). Path B is a
// fallback for pure-legacy commercials that have no material row. It always
// swaps a non-nil index — even when no rows are found. Called once at startup.
```

- [ ] **Step 4: Rodar o teste e confirmar GREEN**

Run: `cd workers && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/index/ -run TestLoadAll_ReusedBackfillMaterial -v`
Expected: PASS.

- [ ] **Step 5: Rodar a suíte do pacote (não quebrar `TestIndexEligibleStatuses` nem outros)**

Run: `cd workers && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/index/ -v`
Expected: PASS (ou SKIP nos DB-backed se sem env). `TestIndexEligibleStatuses` PASS.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/index/loader.go
git commit -m "fix(index): material reaproveitado entra no índice (Path A materials, Path B commercials legado)"
```

---

## Task 4: Carga do worker — materials + commercials (RED→GREEN)

**Files:**
- Create: `workers/internal/catalog/reuse_dead_zone_test.go`
- Modify: `workers/internal/catalog/materials.go:188-196`
- Modify: `workers/internal/catalog/commercials.go:154-160`

- [ ] **Step 1: Escrever o teste que falha**

```go
package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestWorkerLoad_ReusedBackfillMaterial: a backfilled material (UUID in both
// tables; commercial.campaign_id = concluded) reused via campaign_materials in
// an active campaign that targets the station must be returned by the materials
// worker-load path, and the commercials path must NOT double-count it.
func TestWorkerLoad_ReusedBackfillMaterial(t *testing.T) {
	ctx, pool := newTestDB(t)

	var clientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('reuse-wl-client') RETURNING id`).Scan(&clientID))
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID) })

	var concluded, active uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'wl-concl', '2026-05-01', '2026-05-31', 'concluida') RETURNING id`,
		clientID).Scan(&concluded))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'wl-active', '2026-06-01', '2026-07-31', 'ativa') RETURNING id`,
		clientID).Scan(&active))
	station := uuid.New()
	id := uuid.New()
	var shortID int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO commercials (id, campaign_id, title, duration_seconds,
		                         master_storage_path, master_sha256, fingerprint_status, target_stations)
		VALUES ($1, $2, 'wl-spot', 30, '/tmp/w.mp3', 'sha-'||$1::text, 'ready', ARRAY[$3]::uuid[])
		RETURNING short_id`, id, concluded, station).Scan(&shortID))
	_, err := pool.Exec(ctx, `
		INSERT INTO materials (id, short_id, client_id, title, duration_seconds,
		                       master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, $3, 'wl-spot', 30, '/tmp/w.mp3', 'sha-'||$1::text, 'ready')`,
		id, shortID, clientID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, ARRAY[$3]::uuid[])`, active, id, station)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaign_materials WHERE material_id = $1`, id)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, id)
		pool.Exec(ctx, `DELETE FROM commercials WHERE id = $1`, id)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, concluded, active)
	})

	activeIDs := []uuid.UUID{active}

	mats, err := NewMaterials(pool).ListReadyByCampaignsForStation(ctx, activeIDs, station)
	require.NoError(t, err)
	require.Len(t, mats, 1, "materials path must return the reused backfill material")
	require.Equal(t, shortID, mats[0].ShortID)

	// commercials path is gated on the ACTIVE campaign id; the backfill's
	// commercial belongs to the concluded campaign, so it returns nothing here.
	coms, err := NewCommercials(pool).ListReadyByCampaignsForStation(ctx, activeIDs, station)
	require.NoError(t, err)
	require.Len(t, coms, 0, "commercials path must not return a backfill (now id NOT IN materials, and wrong campaign)")
}
```

- [ ] **Step 2: Rodar e confirmar FALHA**

Run: `cd workers && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/catalog/ -run TestWorkerLoad_ReusedBackfillMaterial -v`
Expected: FAIL — `materials path must return ...` (len 0, pois hoje exclui backfill).

- [ ] **Step 3: Corrigir `materials.go`**

Em `ListReadyByCampaignsForStation`, remover a linha `AND mat.id NOT IN (SELECT id FROM commercials)`. A query fica:

```go
	rows, err := m.pool.Query(ctx, `
		SELECT DISTINCT mat.id, mat.short_id, mat.duration_seconds
		FROM materials mat
		JOIN campaign_materials cm ON cm.material_id = mat.id
		WHERE cm.campaign_id = ANY($1)
		  AND mat.fingerprint_status = 'ready'
		  AND $2 = ANY(cm.target_stations)`,
		campaignIDs, stationID)
```

Atualizar o doc-comment da função, trocando a frase sobre exclusão de backfill por:

```go
// ListReadyByCampaignsForStation returns ready materials linked (via
// campaign_materials) to any of the given campaigns AND including the given
// station in that link's target_stations. campaign_materials is authoritative,
// so backfilled materials (UUID also in commercials) reused in a new campaign
// ARE returned here — the commercials path only covers pure-legacy rows.
```

- [ ] **Step 4: Corrigir `commercials.go`**

Em `ListReadyByCampaignsForStation`, adicionar `AND id NOT IN (SELECT id FROM materials)`. A query fica:

```go
	rows, err := c.pool.Query(ctx, `
		SELECT `+commercialColumns+`
		FROM commercials
		WHERE campaign_id = ANY($1)
		  AND fingerprint_status = 'ready'
		  AND $2 = ANY(target_stations)
		  AND id NOT IN (SELECT id FROM materials)`,
		campaignIDs, stationID)
```

Atualizar o doc-comment acrescentando: `Backfilled commercials (id present in materials) are excluded — those load via the materials path to keep each short_id once.`

- [ ] **Step 5: Rodar e confirmar GREEN**

Run: `cd workers && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/catalog/ -run TestWorkerLoad_ReusedBackfillMaterial -v`
Expected: PASS.

- [ ] **Step 6: Suíte do catalog (regressão)**

Run: `cd workers && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/catalog/ -v`
Expected: PASS / SKIP. Nada vermelho.

- [ ] **Step 7: Commit**

```bash
git add workers/internal/catalog/materials.go workers/internal/catalog/commercials.go workers/internal/catalog/reuse_dead_zone_test.go
git commit -m "fix(catalog): worker carrega material backfill reaproveitado; commercials vira legado-puro"
```

---

## Task 5: Atribuição — extrair `resolveAttribution` e inverter ordem (RED→GREEN)

**Files:**
- Create: `workers/internal/evidence/attribution.go`
- Create: `workers/internal/evidence/attribution_test.go`
- Modify: `workers/internal/evidence/service.go:159-199`

- [ ] **Step 1: Criar `attribution.go`**

```go
package evidence

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// resolveAttribution maps a confirmed detection's short_id to the
// (commercialID, campaignID) it should be recorded under.
//
// campaign_materials is the source of truth: first look for a material linked to
// an active/programada campaign that targets this station and whose date range
// contains detectedAt (most recently added link wins). Only when the short_id
// has no such material link do we fall back to the legacy commercials.campaign_id.
//
// This ordering ensures a backfilled material reused in a new campaign is
// attributed to the campaign it actually runs in now — not the (possibly
// concluded) campaign its legacy commercial row still points at. Pre-fix the
// order was inverted, so reused backfills were attributed to the stale campaign.
func resolveAttribution(ctx context.Context, db *pgxpool.Pool, shortID int32,
	stationID uuid.UUID, detectedAt time.Time) (commercialID, campaignID uuid.UUID, err error) {
	err = db.QueryRow(ctx, `
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
	`, shortID, stationID, detectedAt).Scan(&commercialID, &campaignID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = db.QueryRow(ctx,
			`SELECT c.id, c.campaign_id FROM commercials c WHERE c.short_id = $1 AND c.fingerprint_status = 'ready' LIMIT 1`,
			shortID,
		).Scan(&commercialID, &campaignID)
	}
	return commercialID, campaignID, err
}
```

- [ ] **Step 2: Escrever o teste (RED)**

```go
package evidence

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"radiocheck/internal/db"
)

func attrTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	return ctx, pool
}

// TestResolveAttribution_ReusedBackfillPrefersActiveCampaign: the legacy
// commercial points to a concluded campaign, but the live campaign_materials
// link points to an active one. Attribution must pick the ACTIVE campaign.
func TestResolveAttribution_ReusedBackfillPrefersActiveCampaign(t *testing.T) {
	ctx, pool := attrTestDB(t)

	var clientID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('attr-client') RETURNING id`).Scan(&clientID))
	var concluded, active uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'attr-concl', '2026-05-01', '2026-05-31', 'concluida') RETURNING id`,
		clientID).Scan(&concluded))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'attr-active', '2026-06-01', '2026-07-31', 'ativa') RETURNING id`,
		clientID).Scan(&active))
	station := uuid.New()
	id := uuid.New()
	var shortID int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO commercials (id, campaign_id, title, duration_seconds,
		                         master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, 'attr-spot', 30, '/tmp/a.mp3', 'sha-'||$1::text, 'ready')
		RETURNING short_id`, id, concluded).Scan(&shortID))
	_, err := pool.Exec(ctx, `
		INSERT INTO materials (id, short_id, client_id, title, duration_seconds,
		                       master_storage_path, master_sha256, fingerprint_status)
		VALUES ($1, $2, $3, 'attr-spot', 30, '/tmp/a.mp3', 'sha-'||$1::text, 'ready')`,
		id, shortID, clientID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, ARRAY[$3]::uuid[])`, active, id, station)
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaign_materials WHERE material_id = $1`, id)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, id)
		pool.Exec(ctx, `DELETE FROM commercials WHERE id = $1`, id)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, concluded, active)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID)
	})

	detectedAt := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	gotCom, gotCamp, err := resolveAttribution(ctx, pool, shortID, station, detectedAt)
	require.NoError(t, err)
	require.Equal(t, id, gotCom)
	require.Equal(t, active, gotCamp, "must attribute to the active campaign, not the concluded one")
}
```

- [ ] **Step 3: Rodar e confirmar (GREEN já, pois `resolveAttribution` nasce certo)**

Run: `cd workers && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/evidence/ -run TestResolveAttribution -v`
Expected: PASS. (Este teste valida a função nova diretamente; o RED conceitual era o comportamento inline antigo, que o Step 4 remove.)

- [ ] **Step 4: Trocar o inline do `service.go` pela função**

Em `handle`, substituir o bloco das linhas ~159–199 (declaração de `commercialID, campaignID`, as duas `QueryRow` e o `if errors.Is(...)`) por:

```go
		// Resolve which commercial/campaign this detection belongs to.
		// campaign_materials is authoritative (see resolveAttribution); the
		// legacy commercials.campaign_id is only a fallback for pure-legacy rows.
		commercialID, campaignID, lookupErr := resolveAttribution(
			ctx, s.db, ev.CommercialShortID, stationID, detectedAt)
		if lookupErr != nil {
			// Drop the detection — the short_id resolves to neither a live
			// material link nor a legacy commercial. The campaign_id column is
			// NOT NULL, so there is nothing valid to persist.
			s.log.Warn("evidence: failed to resolve short_id to commercial or material",
				zap.Int32("short_id", ev.CommercialShortID),
				zap.String("station_id", stationID.String()),
				zap.Error(lookupErr),
			)
			return
		}
```

Remover os imports agora não usados em `service.go` se aplicável (`errors`/`pgx` podem continuar sendo usados em outros pontos — só remover se `go build` reclamar).

- [ ] **Step 5: Compilar e rodar a suíte do evidence**

Run: `cd workers && go build ./internal/evidence/...`
Expected: sem erros. Se `errors` ou `pgx` ficarem sem uso, remover os imports e rebuildar.

Run: `cd workers && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/evidence/ -v`
Expected: PASS / SKIP. Nada vermelho.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/evidence/attribution.go workers/internal/evidence/attribution_test.go workers/internal/evidence/service.go
git commit -m "fix(evidence): atribui veiculação por campaign_materials primeiro (campanha ativa), commercials vira fallback"
```

---

## Task 6: Verificação global + pre-flight

**Files:** nenhum novo (validação).

- [ ] **Step 1: Build + vet de tudo**

Run: `cd workers && go build ./... && go vet ./...`
Expected: sem erros.

- [ ] **Step 2: Suíte completa**

Run: `cd workers && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./... 2>&1 | tail -40`
Expected: tudo PASS/SKIP, nada FAIL.

- [ ] **Step 3: Salvar o pre-flight de drift como script**

Criar `scripts/preflight-target-stations-drift.sql` com a query da §5 da spec (drift entre `commercials.target_stations` e `campaign_materials.target_stations` em campanhas ativas/programadas). Rodar em prod ANTES do deploy e exigir 0 linhas.

- [ ] **Step 4: Commit**

```bash
git add scripts/preflight-target-stations-drift.sql
git commit -m "chore: pre-flight de drift de target_stations antes do deploy do fix da zona morta"
```

---

## Task 7: Documentação

**Files:**
- Modify: `docs/architecture/material-fingerprint-pipeline.md` (atualizar `ultima-verificacao`, descrever a regra campaign_materials-autoritativa, citar a zona morta resolvida).
- Modify: `docs/roadmap/follow-ups-fase2.md` (registrar o fix + follow-ups fora de escopo: auditar `LookupForDedup`/`disambiguation`/webhooks).

- [ ] **Step 1: Atualizar os docs com o comportamento novo** (texto conforme o estado real do código após Tasks 3–5).

- [ ] **Step 2: Commit**

```bash
git add docs/architecture/material-fingerprint-pipeline.md docs/roadmap/follow-ups-fase2.md
git commit -m "docs: regra campaign_materials-autoritativa e zona morta do material reaproveitado"
```

---

## Self-Review (cobertura da spec)

- §4.1 índice → Task 3 ✔ · §4.2 reload → Task 3 Step 2 ✔ · §4.3 worker → Task 4 ✔ · §4.4 atribuição → Task 5 ✔
- §5 pre-flight → Task 6 Step 3 ✔ · §6 testes (5 casos) → Tasks 2,4,5 (backfill reuse, worker materials+commercials dedup, atribuição) ✔ — **fresco** e **legado puro** cobertos pelas suítes existentes do índice/catalog (regressão verde) + asserts de dedup no LoadAll.
- §7 rollout → Task 6 ✔ · §8 follow-ups → Task 7 ✔
- Sem placeholders; nomes de função (`resolveAttribution`, `ListReadyByCampaignsForStation`, `LoadAll`) consistentes entre tasks.
