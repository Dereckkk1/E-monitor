// §18.2.2 — version disambiguation between cuts of the same commercial.
//
// The supervisor sits between the ingestor's state machines and the rest of
// the system (evidence service, webhook deliverer, UI). When a worker
// confirms a detection it publishes on `detections.pending`; the supervisor
// runs the dedup logic and either:
//
//   - publishes `detections.confirmed` (downstream consumers behave as before)
//   - suppresses (a longer cut already covered the same window)
//   - retracts a previously published row by emitting `detections.retracted`
//     and stamping `detections.retracted_at = now()` so the API/UI can show
//     the row as overruled.
//
// The buffer is in-memory; persistence after a supervisor restart is not a
// goal (R-C in the plan: a duplicate per restart is acceptable).
package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/events"
	"radiocheck/internal/ingestor"
	"radiocheck/internal/match"
	"radiocheck/internal/metrics"
	"radiocheck/internal/observability"
)

// RetractedEvent is the payload published on SubjectDetectionRetracted when a
// previously-confirmed detection is overruled by a longer cut.
type RetractedEvent struct {
	StationID         string  `json:"station_id"`
	CommercialShortID int32   `json:"commercial_short_id"`
	DetectedAt        string  `json:"detected_at"` // RFC3339 — identifies the row
	RetractedAt       string  `json:"retracted_at"`
	Reason            string  `json:"reason"`
	Confidence        float64 `json:"confidence"`
}

// SubscribePendingDetections wires the worker → supervisor channel by
// subscribing to SubjectDetectionPending. Each message is a serialized
// ingestor.DetectionEvent (the same shape that used to be published directly
// to SubjectDetectionConfirmed). The supervisor disambiguates and re-emits.
//
// Caller is responsible for draining the subscription on shutdown.
func (s *Supervisor) SubscribePendingDetections(ctx context.Context) (*nats.Subscription, error) {
	sub, err := s.nc.Subscribe(events.SubjectDetectionPending, func(msg *nats.Msg) {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Extract worker-side trace-context from the NATS header (if any) so
		// every supervisor span chains under the original detection trace.
		spanCtx, span := observability.StartConsumerSpan(bgCtx, msg, "supervisor.handle_pending")
		defer span.End()
		if err := s.handlePendingDetection(spanCtx, msg.Data); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			s.log.Warn("supervisor: pending detection handle failed", zap.Error(err))
		}
	})
	if err != nil {
		return nil, fmt.Errorf("supervisor: subscribe pending: %w", err)
	}
	go func() {
		<-ctx.Done()
		_ = sub.Drain()
	}()
	return sub, nil
}

// handlePendingDetection unmarshals a worker confirmation and routes it
// through SubmitDetection.
func (s *Supervisor) handlePendingDetection(ctx context.Context, raw []byte) error {
	var ev ingestor.DetectionEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	detectedAt, err := time.Parse(time.RFC3339, ev.DetectedAt)
	if err != nil {
		return fmt.Errorf("parse detected_at: %w", err)
	}
	det := match.ConfirmedDetection{
		CommercialShortID: ev.CommercialShortID,
		StationID:         ev.StationID,
		DetectedAt:        detectedAt,
		FirstMatchAt:      detectedAt, // best-effort: only DetectedAt round-trips through NATS
		OffsetFrames:      ev.OffsetFrames,
		Confidence:        ev.Confidence,
	}
	s.SubmitDetection(ctx, det, ev)
	return nil
}

// DedupAction is the outcome of evaluateDedup. Decoupled from I/O so the
// decision logic can be unit-tested without NATS or Postgres.
type DedupAction int

const (
	// DedupActionPublish — no conflict, publish the new detection as-is.
	DedupActionPublish DedupAction = iota
	// DedupActionRetractAndPublish — newer cut wins; retract the conflicting
	// entry and publish the new one.
	DedupActionRetractAndPublish
	// DedupActionSuppress — newer cut loses; do not publish, do not retract.
	DedupActionSuppress
)

// evaluateDedup runs the pure decision part of §18.2.2: given a candidate
// detection and an optional conflicting buffer entry, choose what to do.
// Side-effect free; SubmitDetection wraps this with NATS / DB calls.
func evaluateDedup(candidateDuration int, candidateShortID int32, conflict *DedupEntry) DedupAction {
	if conflict == nil {
		return DedupActionPublish
	}
	switch {
	case candidateDuration > conflict.DurationSeconds:
		return DedupActionRetractAndPublish
	case candidateDuration < conflict.DurationSeconds:
		return DedupActionSuppress
	default:
		// Tie-break by smaller short_id (deterministic).
		if candidateShortID < conflict.Detection.CommercialShortID {
			return DedupActionRetractAndPublish
		}
		return DedupActionSuppress
	}
}

// confidenceMargin is the coverage gap above which the more-confident cut wins
// the dedup regardless of duration (audit 2026-07-02 A2). Below it, the decision
// falls back to the duration rule (behavior unchanged). CONSERVATIVE default —
// coverage is still wall-clock (audit B1), so this MUST be calibrated against
// the dedup_suppressions shadow data before DISAMBIG_CONFIDENCE_AWARE is trusted
// in prod.
const confidenceMargin = 0.25

// isSuspectSuppression marca uma supressão §18.2.2 que provavelmente matou uma
// veiculação REAL: o corte suprimido estava materialmente mais confiante que o
// mantido (o mantido só false-confirmou a região compartilhada). É a versão
// ESTRITA do predicado do índice parcial idx_dedup_suppressions_suspect
// (migration 0049, que usa só `suppressed > kept`): aqui exigimos a margem do
// confidence-aware pra métrica não disparar em quase-empate. Ou seja, o índice
// cobre um superconjunto do que esta função marca.
func isSuspectSuppression(suppressedConf, keptConf float64) bool {
	return suppressedConf >= keptConf+confidenceMargin
}

// evaluateDedupWithConfidence layers the confidence-aware rule on top of the
// duration-based evaluateDedup. With confidenceAware=false it is EXACTLY
// evaluateDedup (flag OFF → behavior unchanged). With it on, a clear coverage
// gap decides the winner: the cut that actually aired has the higher coverage,
// so a longer cut that only false-confirmed the shared region (90fm/ASAAS: 30s
// cov 0.16) no longer suppresses the real shorter cut (15s cov 0.79). Near-ties
// fall back to duration — never worse than today.
func evaluateDedupWithConfidence(candidateDuration int, candidateShortID int32, candidateConfidence float64, conflict *DedupEntry, confidenceAware bool) DedupAction {
	if conflict == nil {
		return DedupActionPublish
	}
	if confidenceAware {
		gap := candidateConfidence - conflict.Detection.Confidence
		switch {
		case gap >= confidenceMargin:
			// Candidate clearly aired more of itself → it's the real one.
			return DedupActionRetractAndPublish
		case gap <= -confidenceMargin:
			// Kept cut is clearly stronger → candidate is the weak duplicate.
			return DedupActionSuppress
		}
		// Near-tie on confidence → fall through to the duration rule.
	}
	return evaluateDedup(candidateDuration, candidateShortID, conflict)
}

// SubmitDetection applies §18.2.2 disambiguation to a state-machine
// confirmation. The original ingestor.DetectionEvent is passed alongside the
// match.ConfirmedDetection so we can republish it verbatim on the confirmed
// subject (preserving the EvidenceWindow* fields the worker computed).
func (s *Supervisor) SubmitDetection(ctx context.Context, det match.ConfirmedDetection, original ingestor.DetectionEvent) {
	ctx, span := observability.Tracer().Start(ctx, "supervisor.submit_detection") // station_id and commercial_short_id are useful as searchable
	// attributes; detected_at uses RFC3339 so trace UIs render it.

	span.SetAttributes(
		attribute.String("station_id", det.StationID),
		attribute.Int("commercial_short_id", int(det.CommercialShortID)),
		attribute.String("detected_at", det.DetectedAt.UTC().Format(time.RFC3339)),
		attribute.Float64("confidence", det.Confidence),
	)
	defer span.End()

	stationID, err := uuid.Parse(det.StationID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid station_id")
		s.log.Warn("supervisor: invalid station_id in detection",
			zap.String("station_id", det.StationID), zap.Error(err))
		return
	}

	lookupCtx, lookupSpan := observability.Tracer().Start(ctx, "dedup.lookup")
	info, err := s.commercials.LookupForDedup(lookupCtx, det.CommercialShortID)
	if err != nil {
		lookupSpan.RecordError(err)
		lookupSpan.SetStatus(codes.Error, err.Error())
		lookupSpan.End()
		// If we cannot resolve the client we can't dedup; publish anyway so
		// behavior degrades to pre-§18.2.2.
		span.AddEvent("dedup.lookup_failed_publish_anyway")
		s.log.Warn("supervisor: dedup lookup failed, publishing without dedup",
			zap.Int32("commercial_short_id", det.CommercialShortID),
			zap.Error(err))
		s.publishConfirmed(ctx, original)
		return
	}
	lookupSpan.End()

	// campaigns.dedup_window_seconds is superseded by broadcast-window overlap
	// (§18.2.2 fix for misaligned cuts). The column is kept in the schema for
	// now and will be dropped in Fase 3 once no old callers rely on it.

	broadcastStart := computeBroadcastStart(original.EvidenceWindowStart, det.DetectedAt)
	span.SetAttributes(attribute.String("broadcast_start", broadcastStart.UTC().Format(time.RFC3339)))

	now := time.Now()
	s.dedupBuffer.GC(now.Add(-s.dedupBuffer.MaxAge()))

	_, evalSpan := observability.Tracer().Start(ctx, "dedup.evaluate")
	conflict := s.dedupBuffer.Find(stationID, info.ClientID, broadcastStart, info.DurationSeconds)
	newEntry := DedupEntry{
		Detection:       det,
		ClientID:        info.ClientID,
		DurationSeconds: info.DurationSeconds,
		BroadcastStart:  broadcastStart,
		InsertedAt:      now,
	}

	action := evaluateDedupWithConfidence(info.DurationSeconds, det.CommercialShortID, det.Confidence, conflict, s.disambigConfidenceAware)
	evalSpan.SetAttributes(attribute.Int("dedup.action", int(action)))
	evalSpan.End()
	span.SetAttributes(attribute.Int("dedup.action", int(action)))
	switch action {
	case DedupActionPublish:
		s.dedupBuffer.Add(newEntry)
		s.publishConfirmed(ctx, original)
	case DedupActionRetractAndPublish:
		reason := "longer_cut_detected"
		if info.DurationSeconds == conflict.DurationSeconds {
			reason = "tiebreak_lower_short_id"
		}
		s.retract(ctx, conflict.Detection, reason, det.CommercialShortID)
		s.dedupBuffer.Replace(conflict, newEntry)
		s.publishConfirmed(ctx, original)
	case DedupActionSuppress:
		metrics.MatchDisambiguation.WithLabelValues("suppressed").Inc()
		if isSuspectSuppression(det.Confidence, conflict.Detection.Confidence) {
			metrics.MatchDisambiguation.WithLabelValues("suppressed_suspect").Inc()
		}
		reason := "shorter_cut"
		if info.DurationSeconds == conflict.DurationSeconds {
			reason = "tiebreak_lower_short_id"
		}
		// Forense (audit A3): grava a tocada descartada com as DUAS confianças.
		// O sinal `suppressed_confidence > kept_confidence` denuncia uma provável
		// veiculação REAL morta por um corte mais fraco (só false-confirmou a
		// região compartilhada). Best-effort — não bloqueia a decisão.
		if s.dedupSuppressions != nil {
			if err := s.dedupSuppressions.Record(ctx, catalog.DedupSuppression{
				StationID:            stationID,
				SuppressedShortID:    det.CommercialShortID,
				KeptShortID:          conflict.Detection.CommercialShortID,
				SuppressedDuration:   info.DurationSeconds,
				KeptDuration:         conflict.DurationSeconds,
				SuppressedConfidence: det.Confidence,
				KeptConfidence:       conflict.Detection.Confidence,
				BroadcastStart:       broadcastStart,
				DetectedAt:           det.DetectedAt,
				Reason:               reason,
			}); err != nil {
				s.log.Warn("supervisor: record dedup suppression failed",
					zap.String("station_id", det.StationID),
					zap.Int32("suppressed_short_id", det.CommercialShortID),
					zap.Error(err))
			}
		}
		s.log.Info("supervisor: detection suppressed by version disambiguation",
			zap.String("station_id", det.StationID),
			zap.Int32("suppressed_short_id", det.CommercialShortID),
			zap.Int32("kept_short_id", conflict.Detection.CommercialShortID),
			zap.Int("suppressed_duration", info.DurationSeconds),
			zap.Int("kept_duration", conflict.DurationSeconds),
		)
		// TODO §18.2.2 R-B: compare fingerprint hash overlap between
		// `det.CommercialShortID` and `conflict.Detection.CommercialShortID`
		// before suppressing. Cuts < 50% overlap are independent jingles
		// from the same client and both should publish. Deferred to Fase 3.
	}
}

// computeBroadcastStart derives the inferred wall-clock start of the commercial
// on the stream from the evidence window start timestamp carried in the NATS
// event. The worker computes:
//
//	evidenceWindowStart = FirstMatchAt - 4s (analysis window) - 60s (pre-buffer)
//
// so: broadcastStart = evidenceWindowStart + 60s.
//
// Falls back to detectedAt when evidenceWindowStart is absent or unparseable
// (e.g. empty string in tests, legacy events) — preserving pre-fix behaviour
// for that edge case at the cost of possible missed dedup for misaligned cuts.
func computeBroadcastStart(evidenceWindowStart string, fallback time.Time) time.Time {
	if evidenceWindowStart == "" {
		return fallback
	}
	t, err := time.Parse(time.RFC3339, evidenceWindowStart)
	if err != nil {
		return fallback
	}
	return t.Add(60 * time.Second)
}

// publishConfirmed forwards a detection to the post-disambiguation subject.
// All downstream consumers (evidence, webhook) listen here.
func (s *Supervisor) publishConfirmed(ctx context.Context, ev ingestor.DetectionEvent) {
	payload, err := json.Marshal(ev)
	if err != nil {
		s.log.Error("supervisor: marshal confirmed detection failed", zap.Error(err))
		return
	}
	if err := observability.PublishWithTracing(ctx, s.nc, events.SubjectDetectionConfirmed, payload); err != nil {
		s.log.Error("supervisor: publish confirmed failed",
			zap.String("station_id", ev.StationID),
			zap.Int32("commercial_short_id", ev.CommercialShortID),
			zap.Error(err))
	}
}

// retract stamps detections.retracted_at and publishes a NATS event so
// downstream consumers (webhook, UI) can surface the change.
func (s *Supervisor) retract(ctx context.Context, det match.ConfirmedDetection, reason string, replacementShortID int32) {
	ctx, span := observability.Tracer().Start(ctx, "detection.retract")
	span.SetAttributes(
		attribute.String("station_id", det.StationID),
		attribute.Int("commercial_short_id", int(det.CommercialShortID)),
		attribute.String("reason", reason),
		attribute.Int("replacement_short_id", int(replacementShortID)),
	)
	defer span.End()
	now := time.Now().UTC()
	stationID, err := uuid.Parse(det.StationID)
	if err != nil {
		s.log.Warn("supervisor: retract — invalid station_id",
			zap.String("station_id", det.StationID), zap.Error(err))
		return
	}

	// Mark the row first; only emit NATS once persistence is confirmed so
	// downstream consumers can rely on the DB state.
	rows, err := s.markDetectionRetracted(ctx, det.CommercialShortID, stationID, det.DetectedAt, now)
	if err != nil {
		s.log.Warn("supervisor: retract — DB update failed",
			zap.String("station_id", det.StationID),
			zap.Int32("commercial_short_id", det.CommercialShortID),
			zap.Error(err))
		// Still emit the NATS event so webhook subscribers can act on the
		// retraction even if the persistence step had a transient failure.
	} else if rows == 0 {
		// The row hasn't been inserted yet (evidence service is async). The
		// retraction event still fires so consumers see the change; the
		// catalog will reflect retracted_at the next time we touch the row,
		// or remain unmarked. Acceptable per spec — this is a best-effort
		// path covered by the docs.
		s.log.Warn("supervisor: retract — no rows updated (evidence write may be in flight)",
			zap.String("station_id", det.StationID),
			zap.Int32("commercial_short_id", det.CommercialShortID),
			zap.Time("detected_at", det.DetectedAt),
		)
	}

	evt := RetractedEvent{
		StationID:         det.StationID,
		CommercialShortID: det.CommercialShortID,
		DetectedAt:        det.DetectedAt.UTC().Format(time.RFC3339),
		RetractedAt:       now.Format(time.RFC3339),
		Reason:            reason,
		Confidence:        det.Confidence,
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		s.log.Error("supervisor: marshal retracted event failed", zap.Error(err))
		return
	}
	if err := observability.PublishWithTracing(ctx, s.nc, events.SubjectDetectionRetracted, payload); err != nil {
		s.log.Error("supervisor: publish retracted failed",
			zap.String("station_id", det.StationID),
			zap.Int32("commercial_short_id", det.CommercialShortID),
			zap.Error(err))
		return
	}

	metrics.MatchDisambiguation.WithLabelValues("retracted").Inc()
	s.log.Info("supervisor: detection retracted by version disambiguation",
		zap.String("station_id", det.StationID),
		zap.Int32("retracted_short_id", det.CommercialShortID),
		zap.Int32("replacement_short_id", replacementShortID),
		zap.String("reason", reason),
	)
}

// markDetectionRetracted stamps retracted_at on the detection identified by
// (commercial short id, station, detected_at) and returns the number of rows
// updated.
//
// detections.commercial_id is POLYMORPHIC: a material UUID for library cuts, a
// commercial UUID for legacy ones (they share one short_id sequence). The short
// id is therefore resolved through commercials UNION materials. The previous
// query JOINed `commercials` only, so for a material cut it matched ZERO rows —
// the shorter cut was never retracted and both cuts stayed `available`. That
// was the prod double-count of 2026-06-09 (ASAAS SPOT 15 counted alongside the
// 30s PLATAFORMA FINANCEIRA); retractions silently stopped working the moment
// the catalog moved from commercials to the material library.
func (s *Supervisor) markDetectionRetracted(ctx context.Context, shortID int32, stationID uuid.UUID, detectedAt, at time.Time) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE detections d
		SET retracted_at = $1
		WHERE d.station_id = $3
		  AND d.detected_at = $4
		  AND d.retracted_at IS NULL
		  AND d.commercial_id IN (
		      SELECT id FROM commercials WHERE short_id = $2
		      UNION
		      SELECT id FROM materials   WHERE short_id = $2
		  )`,
		at, shortID, stationID, detectedAt,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
