package evidence

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/audit"
	"radiocheck/internal/catalog"
	"radiocheck/internal/metrics"
)

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
//
// COMPARTILHADA: além do reject-path, o co-fire guard do PASS-path
// (reattributeByCoverage) também a usa. No pass-path `self` está APROVADA, então
// os ramos Skip/Restore retratam `self` em vez de só pular.
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

// recoverRejWindowSeconds é a janela de veiculação (±s) pra casar o corte irmão.
const recoverRejWindowSeconds = 60

// recoverRejectedByCoverage roda no reject-path do §9.9 (gated DISAMBIG_BY_COVERAGE)
// DEPOIS de RestoreDisplacedShorterCut não restaurar nada. Mede a cobertura do
// MESMO clipe (pcm) contra os irmãos do cliente; se um cobre acima da margem
// (chooseByCoverage, ~1.5×) e:
//   - não tem row (suprimido pela v1)  -> reatribui a row rejeitada pro irmão;
//   - tem row retraída+available       -> des-retrata (restore de qualquer duração);
//   - tem row presente (já contada)    -> não mexe.
//
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
	// Piso ABSOLUTO além da margem relativa (1.5×): no reject-path a cobertura do
	// corte rejeitado é baixa por definição, então um clipe degradado/ruído/master
	// stale pode bater a margem relativa contra um irmão que ele também mal cobre.
	// Exigir que o vencedor cubra ≥ o piso do próprio audit garante que o clipe
	// REALMENTE casa o irmão antes de reatribuir (G2 — nunca chuta). O caso real
	// (15s ~0.79) passa folgado; ruído (~0.08) não. Só no reject-path; o passed-path
	// v2 fica intocado.
	if best.Coverage < audit.DefaultMinCoverage {
		s.log.Info("evidence: reject-recovery — winner coverage below audit floor, leaving rejected",
			zap.String("detection_id", detectionID.String()),
			zap.Int32("winner_short_id", best.ShortID),
			zap.Float64("winner_coverage", best.Coverage),
			zap.Float64("min_coverage", audit.DefaultMinCoverage))
		return
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
