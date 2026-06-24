# F-124 — Reatribuição de corte suprimido no reject-path (§18.2.2 v2c) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Recuperar veiculações de corte curto (ex.: 15s) que a desambiguação v1 SUPRIMIU (sem deixar row) quando o irmão longo (30s) confirmou primeiro e depois reprovou no audit §9.9 — reatribuindo a row rejeitada in-place pro corte que o clipe realmente cobre.

**Architecture:** No reject-path do `evidence.Service`, depois do `RestoreDisplacedShorterCut` (v2b) não achar nada pra restaurar, mede a cobertura do MESMO clipe contra os masters irmãos do cliente (`FindCutWithSiblings` + `chooseByCoverage`, do v2). Se um irmão cobre acima da margem (1.5×) e NÃO tem row na janela, reatribui a row rejeitada pro irmão (status `missing`, conta como veiculação). Se o irmão tem row retraída `available`, des-retrata (generaliza o v2b pra qualquer duração). Gated na `DISAMBIG_BY_COVERAGE` (já ON em prod). Sem migration, sem publish especulativo, sem corrida.

**Tech Stack:** Go 1.22, pgx v5, Postgres 16 (tabela `detections` particionada por `detected_at`), Prometheus client, testes DB-gated via `TEST_DATABASE_URL`.

---

## File Structure

| Arquivo | Responsabilidade | Ação |
|---|---|---|
| `workers/internal/catalog/detections.go` | `SiblingDetectionRow`, `FindSiblingDetectionInWindow`, `ClearRetraction`, `ReattributeRejectedDetection`, `ErrReattributeNoRow` | Modificar (add) |
| `workers/internal/catalog/detection_restore_test.go` | testes DB-gated dos 3 helpers novos | Modificar (add) |
| `workers/internal/evidence/reject_recovery.go` | `RejectRecoveryAction`, `decideRejectRecovery` (puro), `recoverRejectedByCoverage` (orquestra) | Criar |
| `workers/internal/evidence/reject_recovery_test.go` | teste puro de `decideRejectRecovery` | Criar |
| `workers/internal/evidence/service.go` | wiring no reject-path de `runAuditOrReject` | Modificar (`service.go:491-503`) |
| `workers/internal/metrics/metrics.go` | doc do label `reattributed_on_reject` | Modificar (`metrics.go:204`) |
| `workers/internal/webhook/...` (Task 7, separável) | eventos corretivos | Modificar |
| `docs/architecture/version-disambiguation.md` / `docs/roadmap/follow-ups-fase2.md` | doc §v2c + marcar F-124 | Modificar |

**Invariantes (guard-rails de incidente — ver spec §4):**
- **G1 (06-09):** toda resolução `short_id↔id` via `commercials ∪ materials`.
- **G2 (05-09/05-22):** só reatribui se um irmão cobre ≥ `coverageMargin` (1.5×); senão fica `audit_rejected`.
- **G3 (05-17):** só seta status válido (`missing`); `UPDATE` com guarda `evidence_status='audit_rejected'` + checagem de `RowsAffected` → falha mantém rejeitado, nunca orfana.

---

## Task 1: `FindSiblingDetectionInWindow` — achar row existente do corte vencedor

**Files:**
- Modify: `workers/internal/catalog/detections.go` (adicionar struct + método ao final do arquivo)
- Test: `workers/internal/catalog/detection_restore_test.go` (adicionar)

- [ ] **Step 1: Escrever o teste que falha**

Adicionar ao fim de `detection_restore_test.go`:

```go
// shortIDOf lê o short_id (SERIAL) de um material já semeado.
func shortIDOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, materialID uuid.UUID) int32 {
	t.Helper()
	var sid int32
	if err := pool.QueryRow(ctx, `SELECT short_id FROM materials WHERE id = $1`, materialID).Scan(&sid); err != nil {
		t.Fatalf("read short_id: %v", err)
	}
	return sid
}

func TestFindSiblingDetectionInWindow(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewDetections(pool)

	client := insSeedClient(t, ctx, pool, "FindSibCo")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	st := insSeedStationNoProfile(t, ctx, pool, "RadioFindSib")
	m15 := restSeedMaterial(t, ctx, pool, client, "FSpot15", 15)
	sid15 := shortIDOf(t, ctx, pool, m15)
	cov := 0.66

	t.Run("acha a row do corte na janela e reporta estado retraido+available", func(t *testing.T) {
		ts := time.Date(2026, 6, 13, 15, 0, 0, 0, time.UTC)
		det15 := restSeedDet(t, ctx, pool, camp, m15, st, ts.Add(-5*time.Second), true, "available", &cov)

		got, err := repo.FindSiblingDetectionInWindow(ctx, sid15, st, ts, 60)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if got == nil || got.ID != det15 {
			t.Fatalf("achou %v, queria %v", got, det15)
		}
		if !got.Retracted || got.EvidenceStatus != "available" {
			t.Errorf("estado errado: retracted=%v status=%q", got.Retracted, got.EvidenceStatus)
		}
	})

	t.Run("retorna nil quando nao ha row do corte na janela", func(t *testing.T) {
		ts := time.Date(2026, 6, 14, 15, 0, 0, 0, time.UTC)
		restSeedDet(t, ctx, pool, camp, m15, st, ts.Add(-120*time.Second), true, "available", &cov)

		got, err := repo.FindSiblingDetectionInWindow(ctx, sid15, st, ts, 60)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if got != nil {
			t.Fatalf("nao deveria achar (fora da janela), achou %v", got.ID)
		}
	})
}
```

- [ ] **Step 2: Rodar o teste pra ver falhar**

Run: `cd workers && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/catalog/ -run TestFindSiblingDetectionInWindow -v`
Expected: FAIL com `repo.FindSiblingDetectionInWindow undefined`.

- [ ] **Step 3: Implementar o método**

Adicionar em `detections.go` (após `RestoreDisplacedShorterCut`):

```go
// SiblingDetectionRow é uma row de detection de um corte irmão encontrada na
// janela da veiculação — usada pelo reject-path (§18.2.2 v2c) pra decidir entre
// restaurar uma row retraída, pular (já contada) ou reatribuir a row rejeitada.
type SiblingDetectionRow struct {
	ID             uuid.UUID
	DetectedAt     time.Time
	Retracted      bool
	EvidenceStatus string
}

// FindSiblingDetectionInWindow devolve a row de detection MAIS PRÓXIMA do corte
// `shortID` na mesma emissora dentro de ±windowSeconds de `detectedAt`, ou nil se
// não houver. Resolução de short_id é POLIMÓRFICA (commercials ∪ materials) — sem
// isso um corte do material library casaria zero rows (regressão do incidente
// 2026-06-09). detected_at fica no WHERE pra partition pruning.
func (d *Detections) FindSiblingDetectionInWindow(ctx context.Context,
	shortID int32, stationID uuid.UUID, detectedAt time.Time, windowSeconds int,
) (*SiblingDetectionRow, error) {
	var row SiblingDetectionRow
	err := d.pool.QueryRow(ctx, `
		SELECT d.id, d.detected_at, (d.retracted_at IS NOT NULL), d.evidence_status
		FROM detections d
		WHERE d.station_id = $2
		  AND d.commercial_id IN (
		      SELECT id FROM commercials WHERE short_id = $1
		      UNION
		      SELECT id FROM materials   WHERE short_id = $1
		  )
		  AND d.detected_at BETWEEN $3 - ($4 * interval '1 second')
		                        AND $3 + ($4 * interval '1 second')
		ORDER BY abs(extract(epoch FROM d.detected_at - $3)) ASC
		LIMIT 1`,
		shortID, stationID, detectedAt, windowSeconds,
	).Scan(&row.ID, &row.DetectedAt, &row.Retracted, &row.EvidenceStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}
```

- [ ] **Step 4: Rodar o teste pra ver passar**

Run: `cd workers && go test ./internal/catalog/ -run TestFindSiblingDetectionInWindow -v`
Expected: PASS (2 subtestes).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/catalog/detection_restore_test.go
git commit -m "feat(catalog): FindSiblingDetectionInWindow para o reject-path v2c (resolucao polimorfica)"
```

---

## Task 2: `ClearRetraction` — des-retratar uma row específica

**Files:**
- Modify: `workers/internal/catalog/detections.go`
- Test: `workers/internal/catalog/detection_restore_test.go`

- [ ] **Step 1: Escrever o teste que falha**

```go
func TestClearRetraction(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewDetections(pool)

	client := insSeedClient(t, ctx, pool, "ClearCo")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	st := insSeedStationNoProfile(t, ctx, pool, "RadioClear")
	m15 := restSeedMaterial(t, ctx, pool, client, "CSpot15", 15)
	cov := 0.7
	ts := time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)
	det := restSeedDet(t, ctx, pool, camp, m15, st, ts, true, "available", &cov)

	if err := repo.ClearRetraction(ctx, det, ts); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if detIsRetracted(t, ctx, pool, det) {
		t.Error("row deveria estar des-retratada")
	}
}
```

- [ ] **Step 2: Rodar pra ver falhar**

Run: `cd workers && go test ./internal/catalog/ -run TestClearRetraction -v`
Expected: FAIL com `repo.ClearRetraction undefined`.

- [ ] **Step 3: Implementar**

```go
// ClearRetraction des-retrata uma detection (retracted_at = NULL). Usada pelo
// reject-path v2c pra restaurar o corte irmão deslocado de QUALQUER duração
// (o RestoreDisplacedShorterCut só cobre o estritamente mais curto). detected_at
// no WHERE pra partition pruning.
func (d *Detections) ClearRetraction(ctx context.Context, id uuid.UUID, detectedAt time.Time) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE detections SET retracted_at = NULL WHERE id = $1 AND detected_at = $2`,
		id, detectedAt)
	return err
}
```

- [ ] **Step 4: Rodar pra ver passar**

Run: `cd workers && go test ./internal/catalog/ -run TestClearRetraction -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/catalog/detection_restore_test.go
git commit -m "feat(catalog): ClearRetraction para des-retratar corte irmao (v2c)"
```

---

## Task 3: `ReattributeRejectedDetection` — reatribuir row rejeitada in-place (atômico, G3)

**Files:**
- Modify: `workers/internal/catalog/detections.go`
- Test: `workers/internal/catalog/detection_restore_test.go`

- [ ] **Step 1: Escrever o teste que falha**

```go
// detRow lê commercial_id, campaign_id, evidence_status de uma detection.
func detRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (uuid.UUID, uuid.UUID, string) {
	t.Helper()
	var cmc, cmp uuid.UUID
	var ev string
	if err := pool.QueryRow(ctx,
		`SELECT commercial_id, campaign_id, evidence_status FROM detections WHERE id = $1`, id,
	).Scan(&cmc, &cmp, &ev); err != nil {
		t.Fatalf("read det row: %v", err)
	}
	return cmc, cmp, ev
}

func TestReattributeRejectedDetection(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewDetections(pool)

	client := insSeedClient(t, ctx, pool, "ReatCo")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	st := insSeedStationNoProfile(t, ctx, pool, "RadioReat")
	m15 := restSeedMaterial(t, ctx, pool, client, "RSpot15", 15)
	m30 := restSeedMaterial(t, ctx, pool, client, "RSpot30", 30)

	t.Run("reatribui a row audit_rejected do 30s pro 15s, status missing", func(t *testing.T) {
		ts := time.Date(2026, 6, 16, 15, 0, 0, 0, time.UTC)
		det := restSeedDet(t, ctx, pool, camp, m30, st, ts, false, "audit_rejected", nil)

		if err := repo.ReattributeRejectedDetection(ctx, det, ts, m15, camp, st, 0.79); err != nil {
			t.Fatalf("reattribute: %v", err)
		}
		cmc, _, ev := detRow(t, ctx, pool, det)
		if cmc != m15 {
			t.Errorf("commercial_id = %v, queria %v", cmc, m15)
		}
		if ev != "missing" {
			t.Errorf("evidence_status = %q, queria \"missing\"", ev)
		}
	})

	t.Run("nao mexe numa row que NAO esta audit_rejected (guard G3)", func(t *testing.T) {
		ts := time.Date(2026, 6, 17, 15, 0, 0, 0, time.UTC)
		det := restSeedDet(t, ctx, pool, camp, m30, st, ts, false, "available", nil)

		err := repo.ReattributeRejectedDetection(ctx, det, ts, m15, camp, st, 0.79)
		if !errors.Is(err, ErrReattributeNoRow) {
			t.Fatalf("queria ErrReattributeNoRow, veio %v", err)
		}
		cmc, _, ev := detRow(t, ctx, pool, det)
		if cmc != m30 || ev != "available" {
			t.Errorf("row nao deveria ter mudado: cmc=%v ev=%q", cmc, ev)
		}
	})
}
```

> Nota: `detection_restore_test.go` já importa `errors`? Se não, adicionar `"errors"` ao import block do arquivo de teste.

- [ ] **Step 2: Rodar pra ver falhar**

Run: `cd workers && go test ./internal/catalog/ -run TestReattributeRejectedDetection -v`
Expected: FAIL com `ReattributeRejectedDetection undefined` / `ErrReattributeNoRow undefined`.

- [ ] **Step 3: Implementar**

```go
// ErrReattributeNoRow sinaliza que a reatribuição não tocou nenhuma row — a
// detection alvo não estava (mais) audit_rejected. O caller trata como no-op
// seguro: a row permanece como estava (guard G3 do incidente 2026-05-17).
var ErrReattributeNoRow = errors.New("reattribute: no audit_rejected row matched")

// ReattributeRejectedDetection re-aponta uma row AUDIT_REJECTED pro corte que o
// clipe realmente cobre (§18.2.2 v2c, caso suprimido sem row irmã). Diferente do
// ReattributeDetection (pass-path), aqui a row vem do reject-path: não teve clipe
// subido (evidence_key vazio), então o status vai pra 'missing' (veiculação
// válida, sem áudio — mesma semântica de detecção manual; CONTA nos agregados).
// Tudo num UPDATE atômico guardado por evidence_status='audit_rejected':
//   - re-categoriza pro novo (campaign, corte, station, dia);
//   - seta commercial_id, campaign_id, category, evidence_status='missing', audit_coverage;
//   - se 0 rows tocadas (já não estava rejeitada), devolve ErrReattributeNoRow e
//     NÃO altera nada (G3 — falha mantém o estado, nunca orfana).
func (d *Detections) ReattributeRejectedDetection(ctx context.Context, detectionID uuid.UUID, detectedAt time.Time,
	newCommercialID, newCampaignID, stationID uuid.UUID, coverage float64) error {
	cat, err := d.categorize(ctx, CreateDetectionInput{
		StationID:    stationID,
		CommercialID: newCommercialID,
		CampaignID:   newCampaignID,
		DetectedAt:   detectedAt,
	})
	if err != nil {
		return err
	}
	tag, err := d.pool.Exec(ctx, `
		UPDATE detections
		SET commercial_id = $3, campaign_id = $4, category = $5,
		    evidence_status = 'missing', audit_coverage = $6
		WHERE id = $1 AND detected_at = $2 AND evidence_status = 'audit_rejected'`,
		detectionID, detectedAt, newCommercialID, newCampaignID, cat, coverage)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrReattributeNoRow
	}
	return nil
}
```

- [ ] **Step 4: Rodar pra ver passar**

Run: `cd workers && go test ./internal/catalog/ -run TestReattributeRejectedDetection -v`
Expected: PASS (2 subtestes).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/catalog/detection_restore_test.go
git commit -m "feat(catalog): ReattributeRejectedDetection atomico (status missing, guard G3)"
```

---

## Task 4: `decideRejectRecovery` — decisão pura do ramo de recuperação

**Files:**
- Create: `workers/internal/evidence/reject_recovery.go`
- Test: `workers/internal/evidence/reject_recovery_test.go`

- [ ] **Step 1: Escrever o teste que falha**

Criar `workers/internal/evidence/reject_recovery_test.go`:

```go
package evidence

import (
	"testing"

	"radiocheck/internal/catalog"
)

func TestDecideRejectRecovery(t *testing.T) {
	cases := []struct {
		name     string
		existing *catalog.SiblingDetectionRow
		want     RejectRecoveryAction
	}{
		{"sem row do vencedor -> reatribui", nil, RecoveryReattribute},
		{"row retraida+available -> restaura",
			&catalog.SiblingDetectionRow{Retracted: true, EvidenceStatus: "available"}, RecoveryRestore},
		{"row retraida mas audit_rejected -> pula (15s tambem reprovou)",
			&catalog.SiblingDetectionRow{Retracted: true, EvidenceStatus: "audit_rejected"}, RecoverySkip},
		{"row aprovada nao-retraida -> pula (ja contada)",
			&catalog.SiblingDetectionRow{Retracted: false, EvidenceStatus: "available"}, RecoverySkip},
		{"row missing nao-retraida -> pula (ja contada)",
			&catalog.SiblingDetectionRow{Retracted: false, EvidenceStatus: "missing"}, RecoverySkip},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decideRejectRecovery(c.existing); got != c.want {
				t.Errorf("decideRejectRecovery = %v, queria %v", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Rodar pra ver falhar**

Run: `cd workers && go test ./internal/evidence/ -run TestDecideRejectRecovery -v`
Expected: FAIL com `decideRejectRecovery undefined` / `RejectRecoveryAction undefined`.

- [ ] **Step 3: Implementar (só os tipos + a função pura; orquestração vem na Task 5)**

Criar `workers/internal/evidence/reject_recovery.go`:

```go
package evidence

import "radiocheck/internal/catalog"

// RejectRecoveryAction é o que o reject-path v2c faz com um corte rejeitado cujo
// clipe cobre um irmão mais que o corte atribuído (§18.2.2 v2c).
type RejectRecoveryAction int

const (
	// RecoverySkip — o irmão vencedor já tem uma row presente (não-retraída):
	// a veiculação já está contada, não mexe.
	RecoverySkip RejectRecoveryAction = iota
	// RecoveryRestore — o irmão vencedor tem uma row retraída E available (passou
	// no próprio audit): só des-retrata (generaliza o v2b pra qualquer duração).
	RecoveryRestore
	// RecoveryReattribute — o irmão vencedor não tem row (foi SUPRIMIDO pela v1):
	// reatribui a própria row rejeitada in-place pro irmão.
	RecoveryReattribute
)

// decideRejectRecovery escolhe o ramo a partir da row existente do irmão vencedor
// na janela (nil = não existe). Pré-condição do caller: o vencedor != corte
// atribuído e supera a margem de cobertura. Pura/testável.
func decideRejectRecovery(existing *catalog.SiblingDetectionRow) RejectRecoveryAction {
	switch {
	case existing == nil:
		return RecoveryReattribute
	case existing.Retracted && existing.EvidenceStatus == "available":
		return RecoveryRestore
	default:
		return RecoverySkip
	}
}
```

- [ ] **Step 4: Rodar pra ver passar**

Run: `cd workers && go test ./internal/evidence/ -run TestDecideRejectRecovery -v`
Expected: PASS (5 subtestes).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/evidence/reject_recovery.go workers/internal/evidence/reject_recovery_test.go
git commit -m "feat(evidence): decideRejectRecovery (decisao pura do reject-path v2c)"
```

---

## Task 5: `recoverRejectedByCoverage` — orquestração + wiring no reject-path

**Files:**
- Modify: `workers/internal/evidence/reject_recovery.go` (adicionar o método orquestrador)
- Modify: `workers/internal/evidence/service.go:491-503` (chamar quando o restore não achou nada)
- Modify: `workers/internal/metrics/metrics.go:204` (doc do label)

- [ ] **Step 1: Implementar o orquestrador**

Adicionar em `reject_recovery.go` (precisa dos imports; ver bloco completo abaixo):

```go
import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/metrics"
)

// recoverRej_window é a janela de veiculação (±s) pra casar o corte irmão.
const recoverRejWindowSeconds = 60

// recoverRejectedByCoverage roda no reject-path do §9.9 (gated DISAMBIG_BY_COVERAGE)
// DEPOIS de RestoreDisplacedShorterCut não restaurar nada. Mede a cobertura do
// MESMO clipe (pcm) contra os irmãos do cliente; se um cobre acima da margem
// (chooseByCoverage, ~1.5×) e:
//   - não tem row (suprimido pela v1)  -> reatribui a row rejeitada pro irmão;
//   - tem row retraída+available       -> des-retrata (restore de qualquer duração);
//   - tem row presente (já contada)    -> não mexe.
// Best-effort: qualquer falha loga e deixa a row rejeitada (G2/G3 — nunca chuta,
// nunca orfana). Sem publish especulativo, sem corrida.
func (s *Service) recoverRejectedByCoverage(ctx context.Context,
	detectionID uuid.UUID, detectedAt time.Time, stationID, commercialID uuid.UUID,
	rejectedCoverage float64, pcm []float32) {

	self, sibs, err := s.detections.FindCutWithSiblings(ctx, commercialID)
	if err != nil {
		s.log.Warn("evidence: reject-recovery — sibling lookup failed",
			zap.String("detection_id", detectionID.String()), zap.Error(err))
		return
	}
	if len(sibs) == 0 {
		return // sem irmãos (corte legado / sem material) → nada a fazer
	}

	// Mede cobertura do clipe contra cada irmão; o atribuído entra com a cobertura
	// que JÁ reprovou. chooseByCoverage exige margem (G2) — quase-empate cai pra
	// duração e mantém o atribuído.
	best := CutCoverage{ShortID: self.ShortID, DurationSeconds: self.DurationSeconds, Coverage: rejectedCoverage}
	for _, sib := range sibs {
		res, aerr := s.auditor.AuditEvidence(ctx, sib.ID, pcm)
		if aerr != nil {
			s.log.Warn("evidence: reject-recovery — sibling audit failed",
				zap.String("detection_id", detectionID.String()),
				zap.Int32("sibling_short_id", sib.ShortID), zap.Error(aerr))
			continue
		}
		cand := CutCoverage{ShortID: sib.ShortID, DurationSeconds: sib.DurationSeconds, Coverage: res.Coverage}
		if chooseByCoverage(best, cand) == cand.ShortID {
			best = cand
		}
	}
	if best.ShortID == self.ShortID {
		return // nenhum irmão cobre materialmente mais → a rejeição procede (G2)
	}

	existing, err := s.detections.FindSiblingDetectionInWindow(ctx, best.ShortID, stationID, detectedAt, recoverRejWindowSeconds)
	if err != nil {
		s.log.Warn("evidence: reject-recovery — sibling row lookup failed",
			zap.String("detection_id", detectionID.String()), zap.Error(err))
		return
	}

	switch decideRejectRecovery(existing) {
	case RecoveryRestore:
		if err := s.detections.ClearRetraction(ctx, existing.ID, existing.DetectedAt); err != nil {
			s.log.Warn("evidence: reject-recovery — clear retraction failed",
				zap.String("detection_id", detectionID.String()), zap.Error(err))
			return
		}
		metrics.MatchDisambiguation.WithLabelValues("restored_on_reject").Inc()
		s.log.Info("evidence: restored displaced sibling cut (v2c, any-duration)",
			zap.String("rejected_detection_id", detectionID.String()),
			zap.String("restored_detection_id", existing.ID.String()),
			zap.Int32("restored_short_id", best.ShortID))

	case RecoverySkip:
		s.log.Info("evidence: reject-recovery — winner already counted, skipping",
			zap.String("detection_id", detectionID.String()),
			zap.Int32("winner_short_id", best.ShortID))

	case RecoveryReattribute:
		newCommercialID, newCampaignID, rerr := resolveAttribution(ctx, s.db, best.ShortID, stationID, detectedAt)
		if rerr != nil {
			s.log.Warn("evidence: reject-recovery — winner has no live campaign; leaving rejected",
				zap.String("detection_id", detectionID.String()),
				zap.Int32("winner_short_id", best.ShortID), zap.Error(rerr))
			return
		}
		if err := s.detections.ReattributeRejectedDetection(ctx, detectionID, detectedAt,
			newCommercialID, newCampaignID, stationID, best.Coverage); err != nil {
			s.log.Warn("evidence: reject-recovery — reattribute failed; left rejected",
				zap.String("detection_id", detectionID.String()), zap.Error(err))
			return
		}
		metrics.MatchDisambiguation.WithLabelValues("reattributed_on_reject").Inc()
		s.log.Info("evidence: reattributed suppressed cut on reject (§18.2.2 v2c)",
			zap.String("detection_id", detectionID.String()),
			zap.Int32("from_short_id", self.ShortID),
			zap.Int32("to_short_id", best.ShortID),
			zap.Float64("winner_coverage", best.Coverage))
	}
}
```

- [ ] **Step 2: Verificar que compila**

Run: `cd workers && go build ./internal/evidence/`
Expected: sem erros.

- [ ] **Step 3: Wire no reject-path**

Em `service.go`, no bloco do reject-path (`service.go:491-503`), o `else if restoredID != nil` já existe; adicionar um `else` que chama o novo orquestrador quando o restore não achou nada. Substituir:

```go
	if s.disambigByCoverage {
		if restoredID, restoredShort, rerr := s.detections.RestoreDisplacedShorterCut(auditCtx, detectionID, detectedAt, stationID); rerr != nil {
			s.log.Warn("evidence: restore displaced shorter cut failed (non-blocking)",
				zap.String("detection_id", detectionID.String()), zap.Error(rerr))
		} else if restoredID != nil {
			metrics.MatchDisambiguation.WithLabelValues("restored_on_reject").Inc()
			s.log.Info("evidence: restored shorter cut displaced by rejected longer cut (§18.2.2 v2 reject-path)",
				zap.String("rejected_detection_id", detectionID.String()),
				zap.String("restored_detection_id", restoredID.String()),
				zap.Int32("restored_short_id", restoredShort),
			)
		}
	}
```

por:

```go
	if s.disambigByCoverage {
		if restoredID, restoredShort, rerr := s.detections.RestoreDisplacedShorterCut(auditCtx, detectionID, detectedAt, stationID); rerr != nil {
			s.log.Warn("evidence: restore displaced shorter cut failed (non-blocking)",
				zap.String("detection_id", detectionID.String()), zap.Error(rerr))
		} else if restoredID != nil {
			metrics.MatchDisambiguation.WithLabelValues("restored_on_reject").Inc()
			s.log.Info("evidence: restored shorter cut displaced by rejected longer cut (§18.2.2 v2 reject-path)",
				zap.String("rejected_detection_id", detectionID.String()),
				zap.String("restored_detection_id", restoredID.String()),
				zap.Int32("restored_short_id", restoredShort),
			)
		} else {
			// v2c — nada pra restaurar: o corte curto foi SUPRIMIDO pela v1 (sem
			// row) OU deslocado com mesma duração (fora do predicado do v2b).
			// Reatribui a row rejeitada pro irmão que o clipe realmente cobre.
			s.recoverRejectedByCoverage(auditCtx, detectionID, detectedAt, stationID, commercialID, result.Coverage, pcm)
		}
	}
```

- [ ] **Step 4: Atualizar o doc do label da métrica**

Em `metrics.go:204`, trocar o comentário:

```go
	}, []string{"action"}) // suppressed | retracted | reattributed_by_coverage | restored_on_reject
```

por:

```go
	}, []string{"action"}) // suppressed | retracted | reattributed_by_coverage | restored_on_reject | reattributed_on_reject
```

- [ ] **Step 5: Build + vet de tudo**

Run: `cd workers && go build ./... && go vet ./internal/evidence/ ./internal/catalog/ ./internal/metrics/`
Expected: limpo.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/evidence/reject_recovery.go workers/internal/evidence/service.go workers/internal/metrics/metrics.go
git commit -m "feat(evidence): reatribuir corte suprimido no reject-path (§18.2.2 v2c) + metric reattributed_on_reject"
```

---

## Task 6: Teste de integração DB-gated do orquestrador (caminho sem-row e mesma-duração)

O `recoverRejectedByCoverage` usa o `auditor` (precisa de hashes reais), então testamos os **efeitos de DB** das duas decisões críticas chamando os helpers de catalog na sequência que o orquestrador usa — sem depender de áudio. Isso trava as regressões dos incidentes 05-09 Ep.2 (mesma duração) e o caso suprimido (sem row).

**Files:**
- Test: `workers/internal/catalog/detection_restore_test.go`

- [ ] **Step 1: Escrever o teste que falha (vai passar assim que Tasks 1-3 existirem; é o teste de cenário ponta-a-ponta no catalog)**

```go
// TestRejectRecoveryScenarios trava as duas recuperações que o orquestrador v2c
// dispara, exercitando os helpers de catalog na mesma ordem (sem auditor):
//   A) corte suprimido (sem row) -> ReattributeRejectedDetection re-aponta a row rejeitada.
//   B) irmão deslocado de MESMA duração (Ep.2 05-09) -> FindSiblingDetectionInWindow
//      acha a row retraída+available e ClearRetraction restaura (sem duplicar).
func TestRejectRecoveryScenarios(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewDetections(pool)

	client := insSeedClient(t, ctx, pool, "RecCo")
	camp := insSeedCampaign(t, ctx, pool, client, "2026-06-01", "2026-06-30")
	st := insSeedStationNoProfile(t, ctx, pool, "RadioRec")
	m15 := restSeedMaterial(t, ctx, pool, client, "RecSpot15", 15)
	m30 := restSeedMaterial(t, ctx, pool, client, "RecSpot30", 30)
	mJingle := restSeedMaterial(t, ctx, pool, client, "RecJingle", 30) // mesma duração do m30
	sid15 := shortIDOf(t, ctx, pool, m15)
	sidJingle := shortIDOf(t, ctx, pool, mJingle)
	cov := 0.66

	t.Run("A: 30s rejeitado + 15s sem row -> reatribui (sem row irma)", func(t *testing.T) {
		ts := time.Date(2026, 6, 18, 15, 0, 0, 0, time.UTC)
		det30 := restSeedDet(t, ctx, pool, camp, m30, st, ts, false, "audit_rejected", nil)

		// orquestrador: FindSiblingDetectionInWindow(15s) == nil -> RecoveryReattribute
		existing, err := repo.FindSiblingDetectionInWindow(ctx, sid15, st, ts, 60)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if decideEv := evDecide(existing); decideEv != "reattribute" {
			t.Fatalf("decisao = %s, queria reattribute", decideEv)
		}
		if err := repo.ReattributeRejectedDetection(ctx, det30, ts, m15, camp, st, 0.79); err != nil {
			t.Fatalf("reattribute: %v", err)
		}
		cmc, _, ev := detRow(t, ctx, pool, det30)
		if cmc != m15 || ev != "missing" {
			t.Errorf("row deveria ser 15s/missing, veio cmc=%v ev=%q", cmc, ev)
		}
	})

	t.Run("B: jingle MESMA duracao retraido+available -> restaura, sem duplicar", func(t *testing.T) {
		ts := time.Date(2026, 6, 19, 15, 0, 0, 0, time.UTC)
		det30 := restSeedDet(t, ctx, pool, camp, m30, st, ts, false, "audit_rejected", nil)
		detJingle := restSeedDet(t, ctx, pool, camp, mJingle, st, ts.Add(-5*time.Second), true, "available", &cov)

		// v2b NÃO acharia (mesma duração); v2c acha pela cobertura + janela:
		existing, err := repo.FindSiblingDetectionInWindow(ctx, sidJingle, st, ts, 60)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if existing == nil || existing.ID != detJingle {
			t.Fatalf("achou %v, queria %v", existing, detJingle)
		}
		if evDecide(existing) != "restore" {
			t.Fatalf("decisao = %s, queria restore", evDecide(existing))
		}
		if err := repo.ClearRetraction(ctx, existing.ID, existing.DetectedAt); err != nil {
			t.Fatalf("clear: %v", err)
		}
		if detIsRetracted(t, ctx, pool, detJingle) {
			t.Error("jingle deveria estar des-retratado")
		}
		// o 30s rejeitado NÃO é reatribuído nesse ramo (sem duplicar):
		_, _, ev := detRow(t, ctx, pool, det30)
		if ev != "audit_rejected" {
			t.Errorf("det30 deveria continuar audit_rejected, veio %q", ev)
		}
		_ = sid15
	})
}

// evDecide espelha a decisão pura do pacote evidence (decideRejectRecovery) sem
// importar evidence (evita ciclo): nil->reattribute, retraida+available->restore, senão skip.
func evDecide(existing *SiblingDetectionRow) string {
	switch {
	case existing == nil:
		return "reattribute"
	case existing.Retracted && existing.EvidenceStatus == "available":
		return "restore"
	default:
		return "skip"
	}
}
```

- [ ] **Step 2: Rodar**

Run: `cd workers && go test ./internal/catalog/ -run TestRejectRecoveryScenarios -v`
Expected: PASS (2 subtestes) — depende de Tasks 1-3.

- [ ] **Step 3: Suite completa do catalog + evidence**

Run: `cd workers && go test ./internal/catalog/ ./internal/evidence/ -v`
Expected: tudo PASS (inclui o `TestRestoreDisplacedShorterCut` do v2b — confirma que não quebrou).

- [ ] **Step 4: Commit**

```bash
git add workers/internal/catalog/detection_restore_test.go
git commit -m "test(catalog): cenarios reject-recovery v2c (suprimido sem row + Ep.2 mesma duracao)"
```

---

## Task 7 (separável): eventos corretivos de webhook

> **Pode ser pulado** se nenhum cliente consome webhook hoje (a contagem no DB/UI já fica correta sem isto). Decisão do operador (ver spec §5). Implementar só se houver consumidor.

**Files:**
- Modify: `workers/internal/evidence/reject_recovery.go`

- [ ] **Step 1: Publicar `detection.retracted` do corte errado + `detection.confirmed` do certo**

No ramo `RecoveryReattribute` (e opcionalmente no `RecoveryRestore`), após o sucesso do UPDATE, publicar via `s.nc` nos subjects `events.SubjectDetectionRetracted` (payload `supervisor.RetractedEvent` com `Reason:"reattributed_by_coverage_on_reject"`, `CommercialShortID: self.ShortID`) e `events.SubjectDetectionConfirmed` (payload `ingestor.DetectionEvent` do corte vencedor). Reusar as structs já definidas; marshalar com `json.Marshal` e `observability.PublishWithTracing(ctx, s.nc, subject, payload)` como o supervisor faz em `disambiguation.go:323`.

- [ ] **Step 2: Build + commit**

```bash
cd workers && go build ./...
git add workers/internal/evidence/reject_recovery.go
git commit -m "feat(evidence): webhook corretivo na reatribuicao do reject-path (v2c)"
```

---

## Task 8: Documentação

**Files:**
- Modify: `docs/architecture/version-disambiguation.md` (nova seção §18.2.2-v2c)
- Modify: `docs/roadmap/follow-ups-fase2.md` (marcar F-124 com status)

- [ ] **Step 1: Adicionar §18.2.2-v2c em version-disambiguation.md**

Após a seção `§18.2.2-v2b`, adicionar uma seção descrevendo: o caso de SUPRESSÃO (corte curto confirma depois, sem row), por que o v2b não alcança, o mecanismo (reattribute-on-reject + restore de qualquer duração via `FindSiblingDetectionInWindow`), os guards G1/G2/G3, a métrica `reattributed_on_reject`, e o status `missing` da row recuperada. Atualizar o header `ultima-verificacao` e `codigo-relacionado` (add `reject_recovery.go`).

- [ ] **Step 2: Marcar F-124 em follow-ups-fase2.md**

No item **F-124**, adicionar no fim: `**Status:** resolvido em <sha do merge> (§18.2.2 v2c — reattribute-on-reject).`

- [ ] **Step 3: Commit**

```bash
git add docs/architecture/version-disambiguation.md docs/roadmap/follow-ups-fase2.md
git commit -m "docs(f124): §18.2.2 v2c (reattribute-on-reject) + marcar F-124"
```

---

## Validação final (antes do merge / deploy)

- [ ] `cd workers && go build ./... && go vet ./...` limpos.
- [ ] `cd workers && go test ./internal/catalog/ ./internal/evidence/` verde (inclui regressão do v2b).
- [ ] Revisar o diff completo (`git diff master...HEAD`).
- [ ] **Deploy:** `./scripts/deploy.sh` (shadow migration test roda mesmo sem migration nova; sem custo). Flag `DISAMBIG_BY_COVERAGE` já ON.
- [ ] **Pós-deploy:** métrica `radiocheck_match_disambiguation_total{action="reattributed_on_reject"}` começa a subir; conferir uma veiculação 15s recém-recuperada na modal de `/detections` (status `missing`, conta na grid).
- [ ] **Kill-switch:** se algo estranho, `DISAMBIG_BY_COVERAGE=false` + restart desliga TODO o caminho de cobertura (v2/v2b/v2c juntos).

---

## Critérios de aceite (do spec §10)

- [ ] 15s suprimido cujo 30s irmão reprova passa a contar como **um** 15s (status `missing`), flag ON.
- [ ] Nenhuma veiculação vira **duas** rows aprovadas (precedência restore-antes-de-reatribuir + guarda `evidence_status='audit_rejected'`).
- [ ] 30s legítimo / clipe ruído/stale que reprova **não** é reatribuído (margem 1.5×).
- [ ] Caso 05-09 Ep.2 (mesma duração) recuperado sem duplicar (Task 6 cenário B).
- [ ] Métrica `reattributed_on_reject` exposta e subindo.
- [ ] Flag OFF = comportamento idêntico ao atual.
- [ ] Resolução polimórfica (G1), status válido + escrita guardada (G3) em todos os caminhos.
