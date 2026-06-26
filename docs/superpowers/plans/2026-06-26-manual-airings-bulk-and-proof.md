# Veiculações manuais em lote + comprovante PDF + censura tardia — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Permitir inserir N veiculações manuais de uma vez (com PDF comprovante opcional de lote e áudio opcional por linha), subir a censura (áudio) depois em `/detections/:id` para qualquer detecção sem áudio, e renomear o status `paused` de emissora para "Sem campanha ativa".

**Architecture:** Tabela de lote `manual_proof_batches` (1 PDF → N detecções, materiais mistos) + FK `detections.proof_batch_id`. Três endpoints admin novos (`POST /detections/manual/batch`, `POST /detections/{id}/evidence`, `GET /detections/{id}/proof/url`) que reusam o categorizer, a validação material×emissora×campanha, a projeção `detection_campaigns` (F-119), `UpdateEvidence` e o storage S3 já existentes. Frontend: form multi-linha na `DayDetailModal` e card PDF + uploader de censura na `DetectionDetailPage`, craft com `/impeccable`.

**Tech Stack:** Go (chi, pgx), PostgreSQL (golang-migrate), React (Vite, TanStack Query, axios), S3/MinIO.

**Spec:** [docs/superpowers/specs/2026-06-26-manual-airings-bulk-and-proof-design.md](../specs/2026-06-26-manual-airings-bulk-and-proof-design.md)

---

## File Structure

| Arquivo | Responsabilidade |
|---------|------------------|
| `migrations/0042_manual_proof_batches.{up,down}.sql` (criar) | Tabela `manual_proof_batches` + coluna `detections.proof_batch_id` + índice |
| `workers/internal/catalog/manual_batches.go` (criar) | Types do batch + `CreateManualBatch`, `ValidateBatchLinks`, `ProofKeyForDetection` (métodos em `*Detections`) |
| `workers/internal/catalog/manual_batches_test.go` (criar) | Testes de repo (integração contra `TEST_DATABASE_URL`) |
| `workers/internal/catalog/detections.go` (modificar) | Campo `Detection.ProofBatchID` + `d.proof_batch_id` no `Get` |
| `workers/internal/api/handlers/detections_manual_batch.go` (criar) | Handlers `CreateManualBatch`, `UploadEvidence`, `ProofURL` |
| `workers/internal/api/router.go` (modificar) | 3 rotas novas (grupo admin) |
| `frontend/src/api/hooks.js` (modificar) | `useCreateManualBatchDetection`, `useUploadDetectionEvidence` |
| `frontend/src/components/DayDetailModal.jsx` (modificar) | Form multi-linha + PDF (craft `/impeccable`) |
| `frontend/src/pages/DetectionDetailPage.jsx` (modificar) | Card PDF + uploader censura + badge "aguardando censura" (craft `/impeccable`) |
| `frontend/src/pages/StationsPage.jsx` (modificar) | Rótulo `paused` → "Sem campanha ativa" |
| `docs/features/manual-airings-bulk-and-proof.md` (criar) + `CLAUDE.md` + `docs/README.md` (modificar) | Documentação + índices |

**Ordem de execução:** o item trivial (Task 1) primeiro; depois migração (Task 2) — necessária antes dos testes de repo, que rodam contra um DB com as migrations aplicadas; backend (Tasks 3–7); frontend (Tasks 8–10); docs (Task 11).

> **Nota sobre testes Go:** os testes de repo usam `newTestDB(t)` (helper em `testhelpers_test.go`) que pula se `TEST_DATABASE_URL` não estiver setado. **Aplique a migração 0042 nesse banco antes de rodar.** Se `TEST_DATABASE_URL` não existir no ambiente, os testes fazem skip (não falham) — nesse caso valide via build + verificação manual local.

---

## Task 1: `/stations` — rótulo `paused` → "Sem campanha ativa"

**Files:**
- Modify: `frontend/src/pages/StationsPage.jsx:17`

Mudança trivial, frontend-only. Confirmado no backend: `monitoring_status='paused'` é setado exclusivamente quando a emissora não tem campanha ativa (supervisor) e emissora nova nasce `paused` — não existe "pausada manualmente".

- [ ] **Step 1: Trocar o label**

Em `frontend/src/pages/StationsPage.jsx`, no objeto `STATUS_META` (linha 14-19), trocar a linha do `paused`:

```jsx
const STATUS_META = {
  active:      { label: 'Ativa',                cls: 'badge-success' },
  calibrating: { label: 'Calibrando',           cls: 'badge-warning' },
  paused:      { label: 'Sem campanha ativa',   cls: 'badge-neutral' },
  error:       { label: 'Erro',                 cls: 'badge-danger'  },
}
```

- [ ] **Step 2: Verificar build do frontend**

Run: `cd frontend && npm run build`
Expected: build conclui sem erro. (Não tocar em `package*.json` — regra 5 do CLAUDE.md.)

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/StationsPage.jsx
git commit -m "fix(stations): rótulo 'Pausada' -> 'Sem campanha ativa' (sem campanha vinculada)"
```

---

## Task 2: Migração 0042 — tabela de lote + coluna FK

**Files:**
- Create: `migrations/0042_manual_proof_batches.up.sql`
- Create: `migrations/0042_manual_proof_batches.down.sql`

Migração **aditiva** (sem backfill sobre dados existentes) → segura, não cai na regra 4.8.

- [ ] **Step 1: Escrever o `up`**

Criar `migrations/0042_manual_proof_batches.up.sql`:

```sql
-- 0042_manual_proof_batches.up.sql
-- Veiculações manuais em lote + comprovante PDF.
-- Spec: docs/superpowers/specs/2026-06-26-manual-airings-bulk-and-proof-design.md
--
-- Um PDF comprovante (1 linha aqui) cobre N veiculações manuais (materiais
-- mistos). Cada detecção do lote referencia o batch via detections.proof_batch_id.
-- A censura (áudio) continua nas colunas evidence_* de detections (subida depois).

BEGIN;

CREATE TABLE manual_proof_batches (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    campaign_id    UUID NOT NULL REFERENCES campaigns(id),
    station_id     UUID NOT NULL REFERENCES stations(id),
    proof_pdf_key  TEXT   NOT NULL,
    proof_pdf_size BIGINT NOT NULL,
    note           TEXT,
    created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- detections é particionada por detected_at; FK de tabela particionada -> tabela
-- comum é suportado (PG 12+). Coluna nasce nullable, sem reescrita.
ALTER TABLE detections
    ADD COLUMN IF NOT EXISTS proof_batch_id UUID
        REFERENCES manual_proof_batches(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS detections_proof_batch_idx
    ON detections (proof_batch_id) WHERE proof_batch_id IS NOT NULL;

COMMIT;
```

- [ ] **Step 2: Escrever o `down`**

Criar `migrations/0042_manual_proof_batches.down.sql`:

```sql
-- 0042_manual_proof_batches.down.sql
BEGIN;
DROP INDEX IF EXISTS detections_proof_batch_idx;
ALTER TABLE detections DROP COLUMN IF EXISTS proof_batch_id;
DROP TABLE IF EXISTS manual_proof_batches;
COMMIT;
```

- [ ] **Step 3: Aplicar up/down localmente e confirmar idempotência**

Aplicar contra o DB local de dev (ajuste `DATABASE_URL`). Use o mesmo runner do projeto (golang-migrate). Exemplo:

```bash
# up
migrate -path migrations -database "$DATABASE_URL" up
# verificar a tabela e a coluna
psql "$DATABASE_URL" -c "\d manual_proof_batches"
psql "$DATABASE_URL" -c "SELECT column_name FROM information_schema.columns WHERE table_name='detections' AND column_name='proof_batch_id';"
# down e up de novo (idempotência)
migrate -path migrations -database "$DATABASE_URL" down 1
migrate -path migrations -database "$DATABASE_URL" up
```

Expected: `manual_proof_batches` existe com as 8 colunas; `detections.proof_batch_id` aparece; down remove ambos; up re-aplica sem erro.

> Aplique também a 0042 no `TEST_DATABASE_URL` (mesmo comando com aquela URL) para as Tasks 3–5 poderem rodar os testes.

- [ ] **Step 4: Commit**

```bash
git add migrations/0042_manual_proof_batches.up.sql migrations/0042_manual_proof_batches.down.sql
git commit -m "feat(db): migração 0042 — manual_proof_batches + detections.proof_batch_id"
```

---

## Task 3: `Detection.ProofBatchID` + `Get` scan

**Files:**
- Modify: `workers/internal/catalog/detections.go` (struct `Detection` ~linha 65; método `Get` ~linha 957-976)

- [ ] **Step 1: Adicionar o campo na struct `Detection`**

Em `workers/internal/catalog/detections.go`, logo após o campo `ManualNote` (linha 65), adicionar:

```go
	ManualNote *string    `json:"manual_note,omitempty"`
	// ProofBatchID aponta pro lote de comprovante (manual_proof_batches) quando
	// a veiculação foi criada via "comprovante PDF" em lote. Nil para detecções
	// automáticas e manuais sem comprovante. /detections/:id usa pra mostrar o
	// card "Comprovante (PDF)".
	ProofBatchID *uuid.UUID `json:"proof_batch_id,omitempty"`
```

- [ ] **Step 2: Incluir `d.proof_batch_id` no SELECT + scan do `Get`**

No método `Get` (linha 957-976), adicionar `d.proof_batch_id` ao final do SELECT e `&det.ProofBatchID` ao final do Scan. Resultado:

```go
	err := d.pool.QueryRow(ctx, `
		SELECT d.id, d.station_id, COALESCE(s.name, ''), d.commercial_id, COALESCE(m.title, c.title, ''),
		       d.campaign_id, d.detected_at,
		       d.match_start_offset_ms, d.match_end_offset_ms, d.confidence, d.hash_count,
		       d.temporal_coverage, d.audit_coverage, d.variant_used, d.rate_used,
		       d.evidence_status, d.evidence_key, d.evidence_size_bytes, d.category,
		       m.type_id, d.retracted_at, d.ignored_at, d.ignored_by,
		       d.manual_at, d.manual_by, d.manual_note, m.script, d.created_at,
		       d.proof_batch_id
		FROM detections d
		LEFT JOIN stations s ON s.id = d.station_id
		LEFT JOIN commercials c ON c.id = d.commercial_id
		LEFT JOIN materials m ON m.id = d.commercial_id
		WHERE d.id = $1`, id,
	).Scan(&det.ID, &det.StationID, &det.StationName, &det.CommercialID, &det.CommercialName,
		&det.CampaignID, &det.DetectedAt,
		&det.MatchStartOffsetMs, &det.MatchEndOffsetMs, &det.Confidence, &det.HashCount,
		&det.TemporalCoverage, &det.AuditCoverage, &det.VariantUsed, &det.RateUsed,
		&det.EvidenceStatus, &det.EvidenceKey, &det.EvidenceSizeBytes, &det.Category, &det.TypeID,
		&det.RetractedAt, &det.IgnoredAt, &det.IgnoredBy,
		&det.ManualAt, &det.ManualBy, &det.ManualNote, &det.CommercialScript, &det.CreatedAt,
		&det.ProofBatchID)
```

- [ ] **Step 3: Compilar**

Run: `cd workers && go build ./internal/catalog/`
Expected: compila sem erro.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/catalog/detections.go
git commit -m "feat(catalog): Detection.ProofBatchID + scan no Get"
```

---

## Task 4: Repo `ValidateBatchLinks` (TDD)

**Files:**
- Create: `workers/internal/catalog/manual_batches.go`
- Create: `workers/internal/catalog/manual_batches_test.go`

- [ ] **Step 1: Escrever o teste que falha**

Criar `workers/internal/catalog/manual_batches_test.go`. Usa `seedAirtimeFixture` (helper em `detections_test.go`) + `NewCampaignMaterials(pool).Link(...)` pra criar o vínculo que o batch valida:

```go
package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDetections_ValidateBatchLinks(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "ValidateBatchLinks")
	if err := NewCampaignMaterials(pool).Link(ctx, campID, matID, []uuid.UUID{statID}); err != nil {
		t.Fatalf("link: %v", err)
	}
	dets := NewDetections(pool)

	errs := dets.ValidateBatchLinks(ctx, campID, statID, []ManualBatchEntry{
		{CommercialID: matID, DetectedAt: time.Now()},
	})
	if len(errs) != 0 {
		t.Fatalf("linked material returned errors: %+v", errs)
	}

	errs2 := dets.ValidateBatchLinks(ctx, campID, statID, []ManualBatchEntry{
		{CommercialID: matID, DetectedAt: time.Now()},
		{CommercialID: uuid.New(), DetectedAt: time.Now()},
	})
	if len(errs2) != 1 || errs2[0].Index != 1 {
		t.Fatalf("unlinked material: errs=%+v, want exactly 1 at index 1", errs2)
	}
}
```

- [ ] **Step 2: Rodar — deve falhar por não compilar (tipos/método ausentes)**

Run: `cd workers && go test ./internal/catalog/ -run TestDetections_ValidateBatchLinks`
Expected: FAIL — `undefined: ManualBatchEntry` / `dets.ValidateBatchLinks undefined`.

- [ ] **Step 3: Implementar os tipos + `ValidateBatchLinks`**

Criar `workers/internal/catalog/manual_batches.go`:

```go
package catalog

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ManualBatchEntry é uma linha do lote de veiculações manuais: qual material,
// quando tocou e uma descrição opcional. O áudio (censura) e o PDF do lote são
// tratados fora daqui (no handler, via storage).
type ManualBatchEntry struct {
	CommercialID uuid.UUID
	DetectedAt   time.Time
	Note         string
}

// ManualBatchEntryError aponta uma linha inválida do lote pelo índice (posição
// no array enviado), pro frontend destacar a linha certa.
type ManualBatchEntryError struct {
	Index   int    `json:"index"`
	Message string `json:"message"`
}

// CreateManualBatchInput é o payload do POST /detections/manual/batch. Quando
// ProofBatchID != nil, uma linha em manual_proof_batches é inserida com esse id
// (gerado no handler ANTES do upload do PDF, pra compor a chave S3).
type CreateManualBatchInput struct {
	CampaignID   uuid.UUID
	StationID    uuid.UUID
	ManualBy     uuid.UUID
	BatchNote    string
	ProofBatchID *uuid.UUID
	ProofPDFKey  string
	ProofPDFSize int64
	Entries      []ManualBatchEntry
}

// ValidateBatchLinks confere, linha a linha, se o material está vinculado à
// emissora naquela campanha (campaign_materials.target_stations). Mesmo gate do
// CreateManual single (ErrMaterialNotLinkedToStation). Retorna a lista de linhas
// inválidas pelo índice; vazia = tudo ok. Read-only — não cria nada.
func (d *Detections) ValidateBatchLinks(ctx context.Context, campaignID, stationID uuid.UUID, entries []ManualBatchEntry) []ManualBatchEntryError {
	var errs []ManualBatchEntryError
	for i, e := range entries {
		var linked bool
		err := d.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM campaign_materials
				WHERE campaign_id = $1 AND material_id = $2 AND $3 = ANY(target_stations)
			)`, campaignID, e.CommercialID, stationID).Scan(&linked)
		if err != nil {
			errs = append(errs, ManualBatchEntryError{Index: i, Message: "erro ao validar vínculo"})
			continue
		}
		if !linked {
			errs = append(errs, ManualBatchEntryError{Index: i, Message: "material não vinculado a essa emissora nessa campanha"})
		}
	}
	return errs
}

// trimToPtr devolve nil quando a string é vazia após trim — pra colunas TEXT
// nullable (manual_note, manual_proof_batches.note) ficarem NULL em vez de ''.
func trimToPtr(s string) *string {
	if t := strings.TrimSpace(s); t != "" {
		return &t
	}
	return nil
}
```

- [ ] **Step 4: Rodar — deve passar**

Run: `cd workers && go test ./internal/catalog/ -run TestDetections_ValidateBatchLinks`
Expected: PASS (ou SKIP se `TEST_DATABASE_URL` não setado — nesse caso confirme com `go build ./internal/catalog/`).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/manual_batches.go workers/internal/catalog/manual_batches_test.go
git commit -m "feat(catalog): ManualBatchEntry + ValidateBatchLinks (gate por linha)"
```

---

## Task 5: Repo `CreateManualBatch` + `ProofKeyForDetection` (TDD)

**Files:**
- Modify: `workers/internal/catalog/manual_batches.go`
- Modify: `workers/internal/catalog/manual_batches_test.go`

- [ ] **Step 1: Escrever os testes que falham**

Adicionar a `workers/internal/catalog/manual_batches_test.go`:

```go
func TestDetections_CreateManualBatch_WithProof(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "CreateManualBatch-proof")
	clientID := uuid.MustParse(mustClientIDFromCampaign(t, pool, campID))
	// Segundo material (materiais mistos no mesmo lote), também vinculado.
	matB, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: clientID, Title: "Batch-B", DurationSeconds: 15,
		MasterStoragePath: "/tmp", MasterSHA256: "batch-b",
	})
	if err != nil {
		t.Fatalf("seed matB: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", matB.ID) })
	if err := NewCampaignMaterials(pool).Link(ctx, campID, matID, []uuid.UUID{statID}); err != nil {
		t.Fatalf("link A: %v", err)
	}
	if err := NewCampaignMaterials(pool).Link(ctx, campID, matB.ID, []uuid.UUID{statID}); err != nil {
		t.Fatalf("link B: %v", err)
	}

	batchID := uuid.New()
	dets := NewDetections(pool)
	out, err := dets.CreateManualBatch(ctx, CreateManualBatchInput{
		CampaignID:   campID,
		StationID:    statID,
		ManualBy:     uuid.New(),
		BatchNote:    "comprovante da emissora 25/06",
		ProofBatchID: &batchID,
		ProofPDFKey:  "proofs/2026/06/25/" + statID.String() + "/" + batchID.String() + ".pdf",
		ProofPDFSize: 12345,
		Entries: []ManualBatchEntry{
			{CommercialID: matID, DetectedAt: time.Now().Add(-2 * time.Hour), Note: "tocada 1"},
			{CommercialID: matB.ID, DetectedAt: time.Now().Add(-1 * time.Hour)},
		},
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE proof_batch_id = $1", batchID)
		pool.Exec(ctx, "DELETE FROM manual_proof_batches WHERE id = $1", batchID)
	})
	if err != nil {
		t.Fatalf("CreateManualBatch: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("created %d detections, want 2", len(out))
	}
	for i, det := range out {
		if det.ProofBatchID == nil || *det.ProofBatchID != batchID {
			t.Errorf("detection %d proof_batch_id = %v, want %s", i, det.ProofBatchID, batchID)
		}
		if det.EvidenceStatus != "missing" {
			t.Errorf("detection %d evidence_status = %q, want missing", i, det.EvidenceStatus)
		}
		if det.ManualAt == nil {
			t.Errorf("detection %d manual_at is nil, want set", i)
		}
	}
	// Projeção detection_campaigns 1:1 criada (a grade lê dela — F-119).
	var projCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM detection_campaigns WHERE campaign_id = $1 AND detection_id = ANY($2)`,
		campID, []uuid.UUID{out[0].ID, out[1].ID}).Scan(&projCount); err != nil {
		t.Fatalf("count projections: %v", err)
	}
	if projCount != 2 {
		t.Errorf("detection_campaigns rows = %d, want 2", projCount)
	}
	// Batch row criado com a chave correta.
	var gotKey string
	if err := pool.QueryRow(ctx,
		`SELECT proof_pdf_key FROM manual_proof_batches WHERE id = $1`, batchID).Scan(&gotKey); err != nil {
		t.Fatalf("read batch: %v", err)
	}
	if gotKey == "" {
		t.Errorf("batch proof_pdf_key empty")
	}
	// ProofKeyForDetection resolve a chave a partir de uma detecção do lote.
	resolved, err := dets.ProofKeyForDetection(ctx, out[0].ID)
	if err != nil {
		t.Fatalf("ProofKeyForDetection: %v", err)
	}
	if resolved != gotKey {
		t.Errorf("ProofKeyForDetection = %q, want %q", resolved, gotKey)
	}
}

func TestDetections_CreateManualBatch_NoProof(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "CreateManualBatch-noproof")
	if err := NewCampaignMaterials(pool).Link(ctx, campID, matID, []uuid.UUID{statID}); err != nil {
		t.Fatalf("link: %v", err)
	}
	dets := NewDetections(pool)
	out, err := dets.CreateManualBatch(ctx, CreateManualBatchInput{
		CampaignID: campID, StationID: statID, ManualBy: uuid.New(),
		Entries: []ManualBatchEntry{{CommercialID: matID, DetectedAt: time.Now()}},
	})
	if err != nil {
		t.Fatalf("CreateManualBatch: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("created %d, want 1", len(out))
	}
	if out[0].ProofBatchID != nil {
		t.Errorf("proof_batch_id = %v, want nil (no PDF)", out[0].ProofBatchID)
	}
	// Sem lote → ProofKeyForDetection devolve pgx.ErrNoRows.
	if _, err := dets.ProofKeyForDetection(ctx, out[0].ID); err == nil {
		t.Errorf("ProofKeyForDetection on no-proof detection: want error, got nil")
	}
}
```

- [ ] **Step 2: Rodar — deve falhar (métodos ausentes)**

Run: `cd workers && go test ./internal/catalog/ -run TestDetections_CreateManualBatch`
Expected: FAIL — `dets.CreateManualBatch undefined` / `dets.ProofKeyForDetection undefined`.

- [ ] **Step 3: Implementar `CreateManualBatch` + `ProofKeyForDetection`**

Adicionar a `workers/internal/catalog/manual_batches.go` (imports já cobrem `context`, `time`, `uuid`; adicionar `"github.com/jackc/pgx/v5"` não é necessário — `ProofKeyForDetection` devolve o erro cru do pgx que o handler compara com `pgx.ErrNoRows`):

```go
// CreateManualBatch insere o lote inteiro numa transação (tudo-ou-nada nos
// inserts). Pré-condição: as linhas já passaram por ValidateBatchLinks (o
// handler garante). Quando ProofBatchID != nil, grava a linha de
// manual_proof_batches ANTES das detecções. Cada detecção:
//   confidence=1.0, hash_count=0, *_offset=0, evidence_status='missing',
//   manual_at=now(), manual_by, manual_note, proof_batch_id.
// Também grava a projeção canônica detection_campaigns (1:1) por linha — sem ela
// a veiculação some da grade que lê detection_campaigns (F-119). O categorizer
// roda igual ao CreateManual/Create. Retorna as detecções criadas (via Get), em
// ordem das entries, pro handler mapear áudios audio_i -> linha i.
func (d *Detections) CreateManualBatch(ctx context.Context, in CreateManualBatchInput) ([]*Detection, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if in.ProofBatchID != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO manual_proof_batches
				(id, campaign_id, station_id, proof_pdf_key, proof_pdf_size, note, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			*in.ProofBatchID, in.CampaignID, in.StationID, in.ProofPDFKey, in.ProofPDFSize,
			trimToPtr(in.BatchNote), in.ManualBy); err != nil {
			return nil, err
		}
	}

	type idAt struct {
		id uuid.UUID
		at time.Time
	}
	created := make([]idAt, 0, len(in.Entries))

	for _, e := range in.Entries {
		cat, err := d.categorize(ctx, CreateDetectionInput{
			StationID:    in.StationID,
			CommercialID: e.CommercialID,
			CampaignID:   in.CampaignID,
			DetectedAt:   e.DetectedAt,
		})
		if err != nil {
			return nil, err
		}

		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO detections (
			    station_id, commercial_id, campaign_id, detected_at,
			    match_start_offset_ms, match_end_offset_ms,
			    confidence, hash_count, category,
			    evidence_status,
			    manual_at, manual_by, manual_note, proof_batch_id
			) VALUES (
			    $1, $2, $3, $4,
			    0, 0,
			    1.0, 0, $5,
			    'missing',
			    now(), $6, $7, $8
			)
			RETURNING id`,
			in.StationID, e.CommercialID, in.CampaignID, e.DetectedAt,
			cat, in.ManualBy, trimToPtr(e.Note), in.ProofBatchID,
		).Scan(&id); err != nil {
			return nil, err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (detection_id, detected_at, campaign_id) DO NOTHING`,
			id, e.DetectedAt, in.CampaignID, e.CommercialID, cat); err != nil {
			return nil, err
		}

		created = append(created, idAt{id: id, at: e.DetectedAt})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	out := make([]*Detection, 0, len(created))
	for _, c := range created {
		det, err := d.Get(ctx, c.id)
		if err != nil {
			return nil, err
		}
		out = append(out, det)
	}
	return out, nil
}

// ProofKeyForDetection resolve a chave S3 do PDF comprovante a partir de uma
// detecção do lote (JOIN manual_proof_batches via proof_batch_id). Devolve
// pgx.ErrNoRows quando a detecção não pertence a nenhum lote (ou não existe) —
// o handler mapeia pra 404.
func (d *Detections) ProofKeyForDetection(ctx context.Context, id uuid.UUID) (string, error) {
	var key string
	err := d.pool.QueryRow(ctx, `
		SELECT b.proof_pdf_key
		FROM detections d
		JOIN manual_proof_batches b ON b.id = d.proof_batch_id
		WHERE d.id = $1`, id).Scan(&key)
	return key, err
}
```

- [ ] **Step 4: Rodar — deve passar**

Run: `cd workers && go test ./internal/catalog/ -run TestDetections_CreateManualBatch`
Expected: PASS (ou SKIP sem `TEST_DATABASE_URL` — então `go build ./internal/catalog/`).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/manual_batches.go workers/internal/catalog/manual_batches_test.go
git commit -m "feat(catalog): CreateManualBatch (tx, projeção F-119) + ProofKeyForDetection"
```

---

## Task 6: Handlers `CreateManualBatch`, `UploadEvidence`, `ProofURL`

**Files:**
- Create: `workers/internal/api/handlers/detections_manual_batch.go`

Reusa `manualAudioMIME` e `manualAudioMaxBytes` (já em `detections.go`, mesmo pacote `handlers`) e `writeJSON`.

- [ ] **Step 1: Escrever os 3 handlers**

Criar `workers/internal/api/handlers/detections_manual_batch.go`:

```go
package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

const manualProofMaxBytes = 25 << 20 // 25 MB

// manualBatchMeta é o JSON no campo `meta` do multipart de CreateManualBatch.
type manualBatchMeta struct {
	CampaignID uuid.UUID `json:"campaign_id"`
	StationID  uuid.UUID `json:"station_id"`
	Note       string    `json:"note"`
	Entries    []struct {
		CommercialID uuid.UUID `json:"commercial_id"`
		DetectedAt   time.Time `json:"detected_at"`
		Note         string    `json:"note"`
	} `json:"entries"`
}

// CreateManualBatch — admin "Adicionar veiculações em lote". Multipart:
//   - meta: JSON {campaign_id, station_id, note, entries:[{commercial_id, detected_at, note}]}
//   - proof: PDF comprovante (opcional) → cria 1 manual_proof_batches; todas as linhas o referenciam
//   - audio_0..audio_{N-1}: censura por linha (opcional), indexado pela posição em entries
//
// Tudo-ou-nada nas LINHAS: se qualquer linha falha validação (sintática ou
// vínculo material×emissora×campanha) → 422 com erros por índice, nada criado.
// Upload de PDF/áudio é NÃO-FATAL: a veiculação é criada mesmo se o upload falhar
// (fica evidence_status='missing'; o PDF, se falhar, deixa as linhas sem lote).
func (h *DetectionsHandler) CreateManualBatch(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Teto generoso de corpo: PDF (25MB) + N áudios (25MB cada). 600MB cobre ~23 áudios.
	r.Body = http.MaxBytesReader(w, r.Body, 600<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "invalid multipart payload", http.StatusBadRequest)
		return
	}

	var meta manualBatchMeta
	if err := json.Unmarshal([]byte(r.FormValue("meta")), &meta); err != nil {
		http.Error(w, "invalid meta json", http.StatusBadRequest)
		return
	}
	if meta.CampaignID == uuid.Nil || meta.StationID == uuid.Nil {
		http.Error(w, "campaign_id and station_id are required", http.StatusBadRequest)
		return
	}
	if len(meta.Entries) == 0 {
		http.Error(w, "entries must not be empty", http.StatusBadRequest)
		return
	}

	entries := make([]catalog.ManualBatchEntry, 0, len(meta.Entries))
	var synErrs []catalog.ManualBatchEntryError
	for i, e := range meta.Entries {
		switch {
		case e.CommercialID == uuid.Nil:
			synErrs = append(synErrs, catalog.ManualBatchEntryError{Index: i, Message: "material obrigatório"})
		case e.DetectedAt.IsZero():
			synErrs = append(synErrs, catalog.ManualBatchEntryError{Index: i, Message: "horário obrigatório"})
		case e.DetectedAt.After(time.Now().Add(5 * time.Minute)):
			synErrs = append(synErrs, catalog.ManualBatchEntryError{Index: i, Message: "horário não pode ser no futuro"})
		default:
			entries = append(entries, catalog.ManualBatchEntry{
				CommercialID: e.CommercialID, DetectedAt: e.DetectedAt, Note: e.Note,
			})
		}
	}
	if len(synErrs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": synErrs})
		return
	}

	// Vínculo: tudo-ou-nada. Nada é criado se qualquer linha falhar.
	if linkErrs := h.Repo.ValidateBatchLinks(r.Context(), meta.CampaignID, meta.StationID, entries); len(linkErrs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": linkErrs})
		return
	}

	var warnings []string

	// PDF comprovante (opcional) — upload ANTES dos inserts (precisamos do batch_id
	// pra compor a chave). Não-fatal: se falhar, criamos as veiculações sem lote.
	var proofBatchID *uuid.UUID
	var proofKey string
	var proofSize int64
	if file, header, ferr := r.FormFile("proof"); ferr == nil {
		defer file.Close()
		if header.Size > manualProofMaxBytes {
			http.Error(w, "PDF acima de 25MB", http.StatusRequestEntityTooLarge)
			return
		}
		ctype := header.Header.Get("Content-Type")
		if !strings.HasPrefix(strings.ToLower(ctype), "application/pdf") {
			http.Error(w, "comprovante precisa ser PDF", http.StatusUnsupportedMediaType)
			return
		}
		if h.Storage != nil {
			bid := uuid.New()
			key := fmt.Sprintf("proofs/%s/%s/%s/%s/%s.pdf",
				entries[0].DetectedAt.UTC().Format("2006"),
				entries[0].DetectedAt.UTC().Format("01"),
				entries[0].DetectedAt.UTC().Format("02"),
				meta.StationID, bid)
			if err := h.Storage.Put(r.Context(), key, file, "application/pdf"); err != nil {
				warnings = append(warnings, "upload do PDF comprovante falhou — veiculações criadas sem comprovante")
			} else {
				proofBatchID = &bid
				proofKey = key
				proofSize = header.Size
			}
		}
	}

	out, err := h.Repo.CreateManualBatch(r.Context(), catalog.CreateManualBatchInput{
		CampaignID:   meta.CampaignID,
		StationID:    meta.StationID,
		ManualBy:     claims.UserID,
		BatchNote:    meta.Note,
		ProofBatchID: proofBatchID,
		ProofPDFKey:  proofKey,
		ProofPDFSize: proofSize,
		Entries:      entries,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Censura por linha (opcional) — audio_i casa com out[i]. Não-fatal.
	// (`header` é *multipart.FileHeader; `file` é multipart.File — upload inline
	// pra não brigar com tipos; cada file é fechado no caminho que o consome.)
	for i, det := range out {
		file, header, ferr := r.FormFile(fmt.Sprintf("audio_%d", i))
		if ferr != nil {
			continue
		}
		ctype := header.Header.Get("Content-Type")
		ext, okExt := manualAudioMIME[strings.ToLower(ctype)]
		if !okExt {
			file.Close()
			warnings = append(warnings, fmt.Sprintf("linha %d: formato de áudio não suportado", i))
			continue
		}
		if header.Size > manualAudioMaxBytes {
			file.Close()
			warnings = append(warnings, fmt.Sprintf("linha %d: áudio acima de 25MB", i))
			continue
		}
		if h.Storage == nil {
			file.Close()
			continue
		}
		key := fmt.Sprintf("evidences/%s/%s/%s/%s/%s.%s",
			det.DetectedAt.UTC().Format("2006"),
			det.DetectedAt.UTC().Format("01"),
			det.DetectedAt.UTC().Format("02"),
			meta.StationID, det.ID, ext)
		if err := h.Storage.Put(r.Context(), key, file, ctype); err != nil {
			file.Close()
			warnings = append(warnings, fmt.Sprintf("linha %d: upload do áudio falhou", i))
			continue
		}
		file.Close()
		if err := h.Repo.UpdateEvidence(r.Context(), det.ID, det.DetectedAt, "available", key, header.Size); err != nil {
			warnings = append(warnings, fmt.Sprintf("linha %d: erro ao gravar evidência", i))
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"batch_id":   proofBatchID,
		"detections": out,
		"warnings":   warnings,
	})
}

E adicionar os outros 2 handlers no mesmo arquivo:

```go
// UploadEvidence — admin "Subir censura (áudio)" em /detections/:id. Anexa o
// áudio a uma detecção que ainda NÃO tem áudio (qualquer detecção: manual, via
// lote, ou automática sem evidência). Multipart, campo `audio`.
func (h *DetectionsHandler) UploadEvidence(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	det, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	// Guard: só quando ainda não há áudio.
	if det.EvidenceStatus == "available" || det.EvidenceKey != nil {
		http.Error(w, "essa veiculação já tem censura", http.StatusConflict)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, manualAudioMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "invalid multipart payload (limite 25MB)", http.StatusBadRequest)
		return
	}
	file, header, ferr := r.FormFile("audio")
	if ferr != nil {
		http.Error(w, "campo 'audio' obrigatório", http.StatusBadRequest)
		return
	}
	defer file.Close()
	ctype := header.Header.Get("Content-Type")
	ext, okExt := manualAudioMIME[strings.ToLower(ctype)]
	if !okExt {
		http.Error(w, "formato de áudio não suportado (use mp3, m4a, wav, aac ou ogg)", http.StatusUnsupportedMediaType)
		return
	}
	if h.Storage == nil {
		http.Error(w, "storage indisponível", http.StatusInternalServerError)
		return
	}
	key := fmt.Sprintf("evidences/%s/%s/%s/%s/%s.%s",
		det.DetectedAt.UTC().Format("2006"),
		det.DetectedAt.UTC().Format("01"),
		det.DetectedAt.UTC().Format("02"),
		det.StationID, det.ID, ext)
	if err := h.Storage.Put(r.Context(), key, file, ctype); err != nil {
		http.Error(w, "falha no upload", http.StatusInternalServerError)
		return
	}
	if err := h.Repo.UpdateEvidence(r.Context(), det.ID, det.DetectedAt, "available", key, header.Size); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	updated, err := h.Repo.Get(r.Context(), det.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// ProofURL — presigned GET do PDF comprovante do lote da detecção. Espelha
// EvidenceURL. 404 quando a detecção não pertence a nenhum lote.
func (h *DetectionsHandler) ProofURL(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	key, err := h.Repo.ProofKeyForDetection(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "sem comprovante", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	const ttl = 5 * time.Minute
	url, expiresAt, err := h.Storage.PresignGet(r.Context(), key, ttl)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        url,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}
```

> O arquivo final tem, nesta ordem: `manualProofMaxBytes`, `manualBatchMeta`, `CreateManualBatch` (com o loop de áudio inline), `UploadEvidence`, `ProofURL`.

- [ ] **Step 2: Compilar o pacote de handlers**

Run: `cd workers && go build ./internal/api/...`
Expected: compila sem erro. Se reclamar de import não usado, ajuste o bloco de imports (mantenha só `encoding/json`, `errors`, `fmt`, `net/http`, `strings`, `time`, `chi`, `uuid`, `pgx`, `auth`, `catalog`).

- [ ] **Step 3: `go vet` no pacote**

Run: `cd workers && go vet ./internal/api/handlers/`
Expected: sem warnings.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/api/handlers/detections_manual_batch.go
git commit -m "feat(api): handlers CreateManualBatch + UploadEvidence + ProofURL"
```

---

## Task 7: Rotas no router

**Files:**
- Modify: `workers/internal/api/router.go:383` (grupo admin)

- [ ] **Step 1: Registrar as 3 rotas**

Em `workers/internal/api/router.go`, dentro do grupo `r.Group(func(r chi.Router) { r.Use(auth.RequireRole("admin")) ... })` (linha 381-390), adicionar logo após a linha `r.Post("/detections/manual", d.Detections.CreateManual)`:

```go
				r.Post("/detections/manual", d.Detections.CreateManual)
				r.Post("/detections/manual/batch", d.Detections.CreateManualBatch)
				r.Post("/detections/{id}/evidence", d.Detections.UploadEvidence)
				r.Get("/detections/{id}/proof/url", d.Detections.ProofURL)
				r.Post("/detections/{id}/ignore", d.Detections.Ignore)
				r.Post("/detections/{id}/restore", d.Detections.Restore)
```

> chi resolve `/detections/manual/batch` (estático) e `/detections/{id}/evidence` (param) por especificidade — sem colisão com o GET `/detections/{id}/evidence` do grupo viewer (método diferente).

- [ ] **Step 2: Build nativo + cross-compile linux (regra 6.1 do CLAUDE.md)**

Run:
```bash
cd workers && go build ./... && CGO_ENABLED=0 GOOS=linux go build ./...
```
Expected: ambos passam (o cross-compile é o que o `workers.Dockerfile` faz no deploy).

- [ ] **Step 3: Rodar a suíte do catalog + handlers (não regredir)**

Run: `cd workers && go test ./internal/catalog/ ./internal/api/...`
Expected: PASS/SKIP. (Conhecida flaky pré-existente: `internal/catalog TestBuildDailySummary_WithDowntime` antes de ~13:00 UTC — regra 6.6; só ignore se você não tocou nesse arquivo.)

- [ ] **Step 4: Commit**

```bash
git add workers/internal/api/router.go
git commit -m "feat(api): rotas /detections/manual/batch, /{id}/evidence, /{id}/proof/url (admin)"
```

---

## Task 8: Hooks do frontend

**Files:**
- Modify: `frontend/src/api/hooks.js` (após `useCreateManualDetection`, ~linha 484)

- [ ] **Step 1: Adicionar os hooks**

Em `frontend/src/api/hooks.js`, após o `useCreateManualDetection` (linha 461-484), adicionar:

```javascript
// Admin-only — cria N veiculações de uma vez. payload:
//   { campaign_id, station_id, note?, entries: [{commercial_id, detected_at, note?}],
//     proof?: File (PDF do lote), audios?: { [entryIndex]: File } }
// Monta multipart: `meta` (JSON), `proof` (PDF opcional), `audio_<i>` por linha
// com áudio. Backend valida vínculo de TODAS as linhas (tudo-ou-nada) e roda o
// categorizador igual à engine. Resposta: { batch_id, detections, warnings }.
export function useCreateManualBatchDetection() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ proof, audios = {}, ...meta }) => {
      const fd = new FormData()
      fd.append('meta', JSON.stringify(meta))
      if (proof) fd.append('proof', proof)
      Object.entries(audios).forEach(([idx, file]) => {
        if (file) fd.append(`audio_${idx}`, file)
      })
      return api.post('/detections/manual/batch', fd, {
        headers: { 'Content-Type': 'multipart/form-data' },
      }).then(r => r.data)
    },
    onSuccess: (data, vars) => {
      qc.invalidateQueries({ queryKey: ['detections'] })
      if (vars?.campaign_id) {
        qc.invalidateQueries({ queryKey: ['daily-summary', vars.campaign_id] })
      }
    },
  })
}

// Admin-only — sobe a censura (áudio) numa detecção existente que ainda não tem
// áudio (POST /detections/:id/evidence, multipart campo `audio`). Usado em
// /detections/:id quando a emissora manda o áudio depois do PDF.
export function useUploadDetectionEvidence() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, audio }) => {
      const fd = new FormData()
      fd.append('audio', audio)
      return api.post(`/detections/${id}/evidence`, fd, {
        headers: { 'Content-Type': 'multipart/form-data' },
      }).then(r => r.data)
    },
    onSuccess: (data, vars) => {
      qc.invalidateQueries({ queryKey: ['detection', vars.id] })
      qc.invalidateQueries({ queryKey: ['detection-evidence-url', vars.id] })
      qc.invalidateQueries({ queryKey: ['detections'] })
    },
  })
}
```

- [ ] **Step 2: Verificar build do frontend**

Run: `cd frontend && npm run build`
Expected: build sem erro. (Sem mexer em `package*.json`.)

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/hooks.js
git commit -m "feat(frontend): hooks useCreateManualBatchDetection + useUploadDetectionEvidence"
```

---

## Task 9: `DayDetailModal` — form multi-linha + PDF (craft `/impeccable`)

**Files:**
- Modify: `frontend/src/components/DayDetailModal.jsx` (substituir o componente `ManualEntryForm`, linhas 316-509; e o import de hook na linha 3)

**Esta task é de craft visual — invoque o skill `/impeccable`** para construir o form com o nível de acabamento do design system existente. O contrato e os critérios de aceite abaixo são obrigatórios; a estética segue o `/impeccable` reusando as peças já presentes no arquivo (`Field`, `StyledInput`, `StyledSelect`, `AudioDropzone`, `GhostButton`, `PrimaryButton`, `Spinner`, `NoMaterialsState`).

**Contrato funcional:**
- Contexto fixo (vindo das props do modal): `campaignId`, `stationId`, `dateISO`, `availableMaterials`, `materialType`, `station`.
- Topo: dropzone opcional **Comprovante (PDF)** — aceita só `application/pdf`, máx 25 MB. Reaproveite o visual do `AudioDropzone` ou crie um irmão `PdfDropzone` (mesmo padrão), com validação de tipo/tamanho análoga ao `pickAudio`.
- Lista de **linhas** (estado: array). Cada linha: `material ▾` (de `availableMaterials`), `horário` (input `type="time"` `step="1"`, default `'12:00'`), `descrição` (texto), e dropzone de **áudio** opcional por linha (reusa `AudioDropzone`). Botão remover linha (some quando só há 1).
- Botão **+ adicionar linha** (começa com 1 linha).
- Submit monta o payload e chama `useCreateManualBatchDetection`:
  ```js
  const entries = rows.map(r => ({
    commercial_id: r.materialId,
    detected_at: new Date(`${dateISO}T${r.time.length === 5 ? r.time + ':00' : r.time}-03:00`).toISOString(),
    note: r.note.trim(),
  }))
  const audios = {}
  rows.forEach((r, i) => { if (r.audio) audios[i] = r.audio })
  await createBatch.mutateAsync({
    campaign_id: campaignId, station_id: stationId,
    note: batchNote.trim(), entries, proof: proofPdf ?? undefined, audios,
  })
  ```
- **Erros por linha:** em `catch`, se `err.response?.status === 422` e `err.response.data?.errors` for um array, destacar cada `{index, message}` na linha correspondente. Demais status: alerta genérico (reaproveite as mensagens do `submit` antigo para 403/415).
- Mantenha o early-return `NoMaterialsState` quando `availableMaterials.length === 0`.
- Atualizar o import na linha 3: trocar `useCreateManualDetection` por `useCreateManualBatchDetection` (ou manter ambos se preferir não remover o antigo; o antigo fica órfão e pode ser limpo depois).

**Critérios de aceite:**
1. Dá pra adicionar/remover linhas; cada linha tem material/horário/descrição/áudio independentes.
2. Anexar um PDF e deixar todos os áudios vazios cria as veiculações com o lote (feedback imediato sem censura).
3. Salvar com 1 linha funciona igual ao fluxo antigo (regressão coberta).
4. Erro de vínculo numa linha aparece destacado naquela linha; nenhuma veiculação é criada.

- [ ] **Step 1: Invocar `/impeccable` e reescrever `ManualEntryForm` como form multi-linha** conforme o contrato acima.
- [ ] **Step 2: Build do frontend** — Run: `cd frontend && npm run build` — Expected: sem erro.
- [ ] **Step 3: Verificação manual** — subir o frontend local, abrir `/detections`, clicar numa célula, abrir o form, adicionar 2 linhas + PDF, salvar, confirmar as 2 veiculações na lista do modal.
- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/DayDetailModal.jsx
git commit -m "feat(detections): form multi-linha de veiculações manuais + PDF comprovante (lote)"
```

---

## Task 10: `DetectionDetailPage` — card PDF + uploader de censura + badge (craft `/impeccable`)

**Files:**
- Modify: `frontend/src/pages/DetectionDetailPage.jsx` (após o bloco `EvidencePanel`/grid, ~linha 642-651; e imports de hooks no topo)

**Craft visual — invoque `/impeccable`.** Contrato e aceite abaixo são obrigatórios; estética segue o design system da página (classes `dd-*`).

**Contrato funcional:**
- **Proof URL query** (espelha `evidenceUrlQuery`, linha 447-454):
  ```js
  const proofUrlQuery = useQuery({
    queryKey: ['detection-proof-url', id],
    queryFn:  () => api.get(`/detections/${id}/proof/url`).then(r => r.data),
    enabled:  !!detection?.proof_batch_id,
    staleTime: 4 * 60 * 1000,
    retry: 1,
  })
  ```
- **Card "Comprovante (PDF)"** (admin-only): renderiza quando `detection.proof_batch_id`. Botão/link "Ver comprovante" que abre `proofUrlQuery.data?.url` em nova aba (`target="_blank" rel="noopener"`). Posicionar perto do `EvidencePanel`.
- **Uploader "Subir censura (áudio)"** (admin-only): renderiza quando a detecção **não tem áudio** — i.e. `detection.evidence_status !== 'available' && !detection.evidence_key`. Usa um file input (aceita o mesmo conjunto MIME do `AudioDropzone`: `audio/mpeg,audio/mp3,audio/mp4,audio/x-m4a,audio/aac,audio/wav,audio/x-wav,audio/ogg`) e `useUploadDetectionEvidence().mutateAsync({ id, audio })`. Em sucesso, a query da detecção é invalidada e o player de áudio passa a aparecer (o `evidenceUrlQuery` re-habilita sozinho quando `evidence_status` vira `'available'`). Tratar `409` ("já tem censura") e `415` ("formato não suportado") com mensagem amigável.
- **Badge "Aguardando censura":** quando `(detection.manual_at || detection.proof_batch_id) && evidence_status` sem áudio, exibir um estado neutro/informativo (não o vermelho de "evidência ausente"). Pode ser um ajuste no `EvidencePanel` ou um ribbon próprio no estilo dos `dd-*-ribbon` existentes.
- Imports: adicionar `useUploadDetectionEvidence` de `../api/hooks` (o `useQuery`, `api`, `useAuth` já estão importados).

**Critérios de aceite:**
1. Detecção criada via lote (com PDF, sem áudio) mostra o card "Comprovante (PDF)" abrindo o PDF, e o badge "Aguardando censura" em vez de erro.
2. Subir um áudio pelo uploader faz o player aparecer sem reload.
3. Subir áudio numa detecção que já tem áudio retorna 409 tratado (mensagem, sem quebrar a página).
4. Detecção automática normal (com áudio) não mostra uploader nem card PDF.

- [ ] **Step 1: Invocar `/impeccable`** e implementar a proof URL query + card PDF + uploader + badge conforme o contrato.
- [ ] **Step 2: Build do frontend** — Run: `cd frontend && npm run build` — Expected: sem erro.
- [ ] **Step 3: Verificação manual** — abrir uma detecção de lote sem áudio: ver card PDF + badge; subir áudio; ver player aparecer.
- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/DetectionDetailPage.jsx
git commit -m "feat(detections): /detections/:id mostra comprovante PDF + upload de censura tardia"
```

---

## Task 11: Documentação + índices

**Files:**
- Create: `docs/features/manual-airings-bulk-and-proof.md`
- Modify: `CLAUDE.md` (mapa de consulta) + `docs/README.md` (índice)

- [ ] **Step 1: Criar o doc da feature**

Criar `docs/features/manual-airings-bulk-and-proof.md` com header YAML (regra do CLAUDE.md) e seções: visão geral, fluxo "PDF primeiro, censura depois", modelo de dados (`manual_proof_batches` + `proof_batch_id`), os 3 endpoints, regras (admin-only, tudo-ou-nada nas linhas, upload não-fatal), e o ajuste de rótulo em `/stations`:

```markdown
---
status: implementado
ultima-verificacao: 2026-06-26
codigo-relacionado:
  - migrations/0042_manual_proof_batches.up.sql
  - workers/internal/catalog/manual_batches.go
  - workers/internal/api/handlers/detections_manual_batch.go
  - workers/internal/api/router.go
  - frontend/src/components/DayDetailModal.jsx
  - frontend/src/pages/DetectionDetailPage.jsx
  - frontend/src/pages/StationsPage.jsx
---

# Veiculações manuais em lote + comprovante PDF + censura tardia

(conteúdo conforme o spec docs/superpowers/specs/2026-06-26-manual-airings-bulk-and-proof-design.md,
adaptado para documentação operacional: o quê, como usar, endpoints, regras.)
```

Preencher o corpo com o conteúdo real (não deixar o parêntese como placeholder) — derive do spec.

- [ ] **Step 2: Atualizar o mapa de consulta no `CLAUDE.md`**

Na tabela "Mapa de consulta — quando trabalhar em X, ver Y", adicionar uma linha:

```markdown
| Veiculações manuais em lote + comprovante PDF (1 PDF→N) + censura tardia (subir áudio depois em /detections/:id) | [docs/features/manual-airings-bulk-and-proof.md](docs/features/manual-airings-bulk-and-proof.md) |
```

- [ ] **Step 3: Atualizar `docs/README.md`**

Adicionar o link da feature nova no índice (seção features).

- [ ] **Step 4: Commit**

```bash
git add docs/features/manual-airings-bulk-and-proof.md CLAUDE.md docs/README.md
git commit -m "docs(feature): veiculações manuais em lote + comprovante PDF + censura tardia"
```

---

## Verificação final (antes de push pra master — regra 6 do CLAUDE.md)

- [ ] **Build cross-compile linux** (o que o deploy faz): `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` — todos os `cmd/*` passam.
- [ ] **Testes Go**: `cd workers && go test ./...` — sem regressão nova (distinga flaky pré-existente, regra 6.6).
- [ ] **Migração**: a 0042 é aditiva; o `shadow_migration_test` do deploy aplica sobre cópia de prod e passa trivialmente. Confirme `up`/`down` local (Task 2 Step 3).
- [ ] **Frontend**: `cd frontend && npm run build` passa. **Não** tocou `package*.json` (regra 5) — se tocou, rode o check do lockfile (`git show master:frontend/package-lock.json | grep -c emnapi` vs atual).
- [ ] **Boot da API**: nenhum nome de métrica novo / nenhum map não-inicializado (regra 6.5) — esta mudança não adiciona métrica nem supervisor state, então OK.

---

## Self-Review (preenchido na escrita do plano)

**Spec coverage:**
- Inserção em lote → Tasks 5, 6, 9. ✓
- Comprovante PDF (lote, materiais mistos) → Tasks 2, 5, 6, 9. ✓
- Censura tardia (qualquer detecção sem áudio) → Tasks 6 (UploadEvidence), 10. ✓
- `/stations` rótulo → Task 1. ✓
- Modelo de dados Abordagem A → Task 2. ✓
- Tudo-ou-nada nas linhas → Task 6 (ValidateBatchLinks antes de criar) + Task 5 (tx). ✓
- Reuso categorizer / projeção F-119 / UpdateEvidence / PresignGet → Tasks 5, 6. ✓
- Docs → Task 11. ✓

**Type consistency:** `ManualBatchEntry`, `ManualBatchEntryError`, `CreateManualBatchInput`, `CreateManualBatch`, `ValidateBatchLinks`, `ProofKeyForDetection`, `Detection.ProofBatchID` — usados consistentemente entre catalog (Tasks 3-5) e handlers (Task 6). Hooks `useCreateManualBatchDetection`/`useUploadDetectionEvidence` consumidos nas Tasks 9-10. Rotas (Task 7) batem com os métodos do handler (Task 6).

**Placeholders:** nenhum. Os blocos de código são finais e completos (stubs didáticos foram removidos numa revisão). O único parêntese a preencher é o corpo do doc da feature na Task 11 Step 1, com instrução explícita de derivar do spec — não é um TODO pendente, é a orientação de conteúdo.
