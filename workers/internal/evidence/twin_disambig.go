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

// twinDiscFloor / twinDiscMargin: limiares da regra discriminante. Conservadores
// (favorecem manter/ambíguo). A separação real entre gêmeos é enorme (o clipe
// cobre ~1.0 da região única do que tocou e ~0 da do outro), então margin=1.5 é
// folgado; calibrar em sombra antes de ligar a flag DISAMBIG_TWIN_DISCRIMINATIVE.
const (
	twinDiscFloor  = 0.15
	twinDiscMargin = 1.5
)

type twinVerdict int

const (
	verdictAmbiguous   twinVerdict = iota
	verdictKeep                    // current attribution (self) is right
	verdictReattribute             // the twin is who actually played
)

// chooseTwinByDiscriminative decides between the attributed material (self) and a
// twin by the clip's coverage OF EACH ONE'S DISCRIMINATIVE REGION (the frames
// that separate the twins). Only called when full-coverage tied AND durations are
// ~equal (the spec's gate — the one case coverage/duration can't resolve).
//   - if the higher discriminative coverage < floor -> ambiguous (the signature
//     didn't survive, or the pair has no separating region: disc frames=0 -> 0/0
//     -> 0 < floor).
//   - if one beats the other by at least `margin` -> that one wins.
//   - near-tie above the floor -> ambiguous (never guess).
func chooseTwinByDiscriminative(discSelf, discTwin, floor, margin float64) twinVerdict {
	hi, hiIsTwin := discSelf, false
	if discTwin > discSelf {
		hi, hiIsTwin = discTwin, true
	}
	if hi < floor {
		return verdictAmbiguous
	}
	lo := discTwin
	if hiIsTwin {
		lo = discSelf
	}
	// Near-tie when the winner doesn't clear the loser by at least `margin`.
	// Uses a small epsilon so that winning by *exactly* the margin resolves
	// (float64 makes e.g. 0.20*1.5 == 0.30000000000000004 > 0.30, which would
	// otherwise misfire the tie guard at the exact boundary).
	const eps = 1e-9
	if lo > 0 && hi < lo*margin-eps {
		return verdictAmbiguous // near-tie — not confident enough
	}
	if hiIsTwin {
		return verdictReattribute
	}
	return verdictKeep
}

// twinEval é o resultado da avaliação de UM gêmeo contra a detecção atribuída.
type twinEval struct {
	twinID      uuid.UUID
	twinShortID int32
	discCov     float64 // cobertura do clipe na região discriminante do gêmeo (sinal de decisão)
	fullCov     float64 // cobertura-cheia do clipe vs o master do gêmeo (pra SetAuditCoverage ao reatribuir)
	verdict     twinVerdict
}

// pickTwinAction agrega os vereditos por-gêmeo numa ÚNICA ação pra a detecção:
//   - se ALGUM gêmeo diz reattribute → reatribui pro de MAIOR discCov (o clipe
//     contém a assinatura de no máximo um gêmeo, então o de maior cobertura
//     discriminante é quem tocou);
//   - senão, se algum é ambiguous → ambiguous;
//   - senão → keep.
//
// Pura/testável. O ponteiro devolvido aponta pra dentro de `evals` (não mutar
// depois).
func pickTwinAction(evals []twinEval) (twinVerdict, *twinEval) {
	var winner *twinEval
	anyAmbiguous := false
	for i := range evals {
		switch evals[i].verdict {
		case verdictReattribute:
			if winner == nil || evals[i].discCov > winner.discCov {
				winner = &evals[i]
			}
		case verdictAmbiguous:
			anyAmbiguous = true
		}
	}
	if winner != nil {
		return verdictReattribute, winner
	}
	if anyAmbiguous {
		return verdictAmbiguous, nil
	}
	return verdictKeep, nil
}

// disambiguateTwin resolve a atribuição entre gêmeos acústicos de mesma duração
// (o único caso que cobertura-cheia/duração não separam — §18.2.2 deixa passar).
// Mede a cobertura do clipe NA REGIÃO DISCRIMINANTE de cada gêmeo e:
//   - reattribute → re-aponta a row pro gêmeo que tocou (COM co-fire guard);
//   - ambiguous   → MarkAmbiguous (retrata, vai pra revisão);
//   - keep        → nada.
//
// Best-effort: nunca aborta o upload; toda falha loga e sai. Roda no pass-path só
// quando `reattributeByCoverage` NÃO agiu (gate na Task 9) — então a row ainda
// está atribuída a attributedID e não-retraída. Fonte dos gêmeos = tabela
// material_twin_discriminative (só pares populados, já de mesma duração).
func (s *Service) disambiguateTwin(
	ctx context.Context,
	detectionID uuid.UUID,
	detectedAt time.Time,
	stationID, attributedID uuid.UUID,
	pcm []float32,
) {
	twinRepo := catalog.NewTwinDiscriminative(s.db)
	twins, err := twinRepo.ListForMaterial(ctx, attributedID)
	if err != nil {
		s.log.Warn("evidence: twin — falha ao listar gêmeos",
			zap.String("detection_id", detectionID.String()), zap.Error(err))
		return
	}
	if len(twins) == 0 {
		return // não é caso de gêmeo
	}

	auditSelf, err := s.auditor.AuditEvidence(ctx, attributedID, pcm)
	if err != nil {
		s.log.Warn("evidence: twin — audit do próprio material falhou",
			zap.String("detection_id", detectionID.String()), zap.Error(err))
		return
	}

	var evals []twinEval
	for _, tw := range twins {
		// discSelf = tw.Disc (self vs este gêmeo, da row material→twin).
		discTwin, _, gerr := twinRepo.Get(ctx, tw.TwinID, attributedID)
		if gerr != nil {
			s.log.Warn("evidence: twin — disc reversa não carregou",
				zap.String("twin_id", tw.TwinID.String()), zap.Error(gerr))
			continue
		}
		auditTwin, aerr := s.auditor.AuditEvidence(ctx, tw.TwinID, pcm)
		if aerr != nil {
			s.log.Warn("evidence: twin — audit do gêmeo falhou",
				zap.String("twin_id", tw.TwinID.String()), zap.Error(aerr))
			continue
		}
		covSelf := audit.CoverageOnFrames(auditSelf.CoveredFrames, tw.Disc)
		covTwin := audit.CoverageOnFrames(auditTwin.CoveredFrames, discTwin)
		evals = append(evals, twinEval{
			twinID:      tw.TwinID,
			twinShortID: tw.TwinShortID,
			discCov:     covTwin,
			fullCov:     auditTwin.Coverage,
			verdict:     chooseTwinByDiscriminative(covSelf, covTwin, twinDiscFloor, twinDiscMargin),
		})
	}
	if len(evals) == 0 {
		return
	}

	action, winner := pickTwinAction(evals)
	switch action {
	case verdictReattribute:
		s.reattributeTwinWithCofireGuard(ctx, detectionID, detectedAt, stationID, winner.twinShortID, winner.fullCov)
	case verdictAmbiguous:
		if err := s.detections.MarkAmbiguous(ctx, detectionID, detectedAt); err != nil {
			s.log.Warn("evidence: twin — MarkAmbiguous falhou",
				zap.String("detection_id", detectionID.String()), zap.Error(err))
			return
		}
		metrics.MatchDisambiguation.WithLabelValues("ambiguous_by_discriminative").Inc()
		s.log.Info("evidence: detecção marcada ambígua (gêmeos indistinguíveis pelo trecho discriminante)",
			zap.String("detection_id", detectionID.String()))
	case verdictKeep:
		metrics.MatchDisambiguation.WithLabelValues("kept_by_discriminative").Inc()
	}
}

// reattributeTwinWithCofireGuard re-aponta a detecção pro gêmeo vencedor, com o
// MESMO co-fire guard do reattributeByCoverage (regressão 2026-06-30): se o
// vencedor já tem row na janela, retratar `self` em vez de duplicar. Duplica o
// switch inline de propósito — NÃO refatora o caminho provado do
// reattributeByCoverage (risco zero pra prod). A decisão (decideCofireAction) é a
// mesma função pura compartilhada. Reatribui só via ReattributeDetection
// (sincroniza projeção in-tx) + resolveAttribution (só campanha viva).
func (s *Service) reattributeTwinWithCofireGuard(
	ctx context.Context,
	detectionID uuid.UUID,
	detectedAt time.Time,
	stationID uuid.UUID,
	winnerShortID int32,
	winnerCoverage float64,
) {
	existing, ferr := s.detections.FindSiblingDetectionInWindow(ctx, winnerShortID, stationID, detectedAt, recoverRejWindowSeconds)
	if ferr != nil {
		s.log.Warn("evidence: twin co-fire — lookup da row do vencedor falhou",
			zap.String("detection_id", detectionID.String()), zap.Error(ferr))
		return
	}
	switch decideCofireAction(existing) {
	case cofireRestoreThenRetractSelf:
		if cerr := s.detections.ClearRetraction(ctx, existing.ID, existing.DetectedAt); cerr != nil {
			s.log.Warn("evidence: twin co-fire — restaurar vencedor falhou",
				zap.String("detection_id", detectionID.String()), zap.Error(cerr))
			return
		}
		if rerr := s.detections.RetractByID(ctx, detectionID, detectedAt, time.Now().UTC()); rerr != nil {
			s.log.Warn("evidence: twin co-fire — retratar duplicata falhou",
				zap.String("detection_id", detectionID.String()), zap.Error(rerr))
			return
		}
		metrics.MatchDisambiguation.WithLabelValues("duplicate_cofire_retracted").Inc()
		s.log.Info("evidence: twin co-fire — restaurou vencedor, retratou duplicata",
			zap.String("detection_id", detectionID.String()),
			zap.String("winner_detection_id", existing.ID.String()),
			zap.Int32("winner_short_id", winnerShortID))
		return
	case cofireRetractSelf:
		if rerr := s.detections.RetractByID(ctx, detectionID, detectedAt, time.Now().UTC()); rerr != nil {
			s.log.Warn("evidence: twin co-fire — retratar duplicata falhou",
				zap.String("detection_id", detectionID.String()), zap.Error(rerr))
			return
		}
		metrics.MatchDisambiguation.WithLabelValues("duplicate_cofire_retracted").Inc()
		s.log.Info("evidence: twin co-fire — vencedor já conta, retratou duplicata",
			zap.String("detection_id", detectionID.String()),
			zap.String("winner_detection_id", existing.ID.String()),
			zap.Int32("winner_short_id", winnerShortID))
		return
	case cofireReattribute:
		// vencedor sem row (ou presente mas não contado) → fluxo normal abaixo.
	}

	newCommercialID, newCampaignID, err := resolveAttribution(ctx, s.db, winnerShortID, stationID, detectedAt)
	if err != nil {
		s.log.Warn("evidence: twin — vencedor sem campanha viva; deixando como está",
			zap.String("detection_id", detectionID.String()),
			zap.Int32("winner_short_id", winnerShortID), zap.Error(err))
		return
	}
	if err := s.detections.ReattributeDetection(ctx, detectionID, detectedAt, newCommercialID, newCampaignID, stationID); err != nil {
		s.log.Error("evidence: twin — reatribuição falhou",
			zap.String("detection_id", detectionID.String()), zap.Error(err))
		return
	}
	if err := s.detections.SetAuditCoverage(ctx, detectionID, detectedAt, winnerCoverage); err != nil {
		s.log.Warn("evidence: twin — update de audit_coverage falhou (non-blocking)",
			zap.String("detection_id", detectionID.String()), zap.Error(err))
	}
	metrics.MatchDisambiguation.WithLabelValues("reattributed_by_discriminative").Inc()
	s.log.Info("evidence: detecção reatribuída pelo trecho discriminante (gêmeos)",
		zap.String("detection_id", detectionID.String()),
		zap.Int32("to_short_id", winnerShortID),
		zap.Float64("to_coverage", winnerCoverage))
}
