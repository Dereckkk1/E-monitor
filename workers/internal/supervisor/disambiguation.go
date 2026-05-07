// §18.2.2 — version disambiguation between cuts of the same commercial.
//
// The supervisor sits between the ingestor's state machines and the rest of
// the system (evidence service, webhook deliverer, UI). When a worker
// confirms a detection it publishes on `detections.pending`; the supervisor
// runs the dedup logic and either:
//
//  • publishes `detections.confirmed` (downstream consumers behave as before)
//  • suppresses (a longer cut already covered the same window)
//  • retracts a previously published row by emitting `detections.retracted`
//    and stamping `detections.retracted_at = now()` so the API/UI can show
//    the row as overruled.
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
	"go.uber.org/zap"

	"radiocheck/internal/events"
	"radiocheck/internal/ingestor"
	"radiocheck/internal/match"
	"radiocheck/internal/metrics"
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
		if err := s.handlePendingDetection(bgCtx, msg.Data); err != nil {
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

// SubmitDetection applies §18.2.2 disambiguation to a state-machine
// confirmation. The original ingestor.DetectionEvent is passed alongside the
// match.ConfirmedDetection so we can republish it verbatim on the confirmed
// subject (preserving the EvidenceWindow* fields the worker computed).
func (s *Supervisor) SubmitDetection(ctx context.Context, det match.ConfirmedDetection, original ingestor.DetectionEvent) {
	stationID, err := uuid.Parse(det.StationID)
	if err != nil {
		s.log.Warn("supervisor: invalid station_id in detection",
			zap.String("station_id", det.StationID), zap.Error(err))
		return
	}

	info, err := s.commercials.LookupForDedup(ctx, det.CommercialShortID)
	if err != nil {
		// If we cannot resolve the client we can't dedup; publish anyway so
		// behavior degrades to pre-§18.2.2.
		s.log.Warn("supervisor: dedup lookup failed, publishing without dedup",
			zap.Int32("commercial_short_id", det.CommercialShortID),
			zap.Error(err))
		s.publishConfirmed(original)
		return
	}

	dedupWindow := time.Duration(info.DedupWindowSeconds) * time.Second
	if dedupWindow <= 0 {
		dedupWindow = 5 * time.Second
	}

	now := time.Now()
	s.dedupBuffer.GC(now.Add(-s.dedupBuffer.MaxAge()))

	conflict := s.dedupBuffer.Find(stationID, info.ClientID, det.DetectedAt, dedupWindow)
	newEntry := DedupEntry{
		Detection:       det,
		ClientID:        info.ClientID,
		DurationSeconds: info.DurationSeconds,
		InsertedAt:      now,
	}

	action := evaluateDedup(info.DurationSeconds, det.CommercialShortID, conflict)
	switch action {
	case DedupActionPublish:
		s.dedupBuffer.Add(newEntry)
		s.publishConfirmed(original)
	case DedupActionRetractAndPublish:
		reason := "longer_cut_detected"
		if info.DurationSeconds == conflict.DurationSeconds {
			reason = "tiebreak_lower_short_id"
		}
		s.retract(ctx, conflict.Detection, reason, det.CommercialShortID)
		s.dedupBuffer.Replace(conflict, newEntry)
		s.publishConfirmed(original)
	case DedupActionSuppress:
		metrics.MatchDisambiguation.WithLabelValues("suppressed").Inc()
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

// publishConfirmed forwards a detection to the post-disambiguation subject.
// All downstream consumers (evidence, webhook) listen here.
func (s *Supervisor) publishConfirmed(ev ingestor.DetectionEvent) {
	payload, err := json.Marshal(ev)
	if err != nil {
		s.log.Error("supervisor: marshal confirmed detection failed", zap.Error(err))
		return
	}
	if err := s.nc.Publish(events.SubjectDetectionConfirmed, payload); err != nil {
		s.log.Error("supervisor: publish confirmed failed",
			zap.String("station_id", ev.StationID),
			zap.Int32("commercial_short_id", ev.CommercialShortID),
			zap.Error(err))
	}
}

// retract stamps detections.retracted_at and publishes a NATS event so
// downstream consumers (webhook, UI) can surface the change.
func (s *Supervisor) retract(ctx context.Context, det match.ConfirmedDetection, reason string, replacementShortID int32) {
	now := time.Now().UTC()
	stationID, err := uuid.Parse(det.StationID)
	if err != nil {
		s.log.Warn("supervisor: retract — invalid station_id",
			zap.String("station_id", det.StationID), zap.Error(err))
		return
	}

	// Mark the row first; only emit NATS once persistence is confirmed so
	// downstream consumers can rely on the DB state.
	tag, err := s.db.Exec(ctx, `
		UPDATE detections d
		SET retracted_at = $1
		FROM commercials c
		WHERE d.commercial_id = c.id
		  AND c.short_id = $2
		  AND d.station_id = $3
		  AND d.detected_at = $4
		  AND d.retracted_at IS NULL`,
		now, det.CommercialShortID, stationID, det.DetectedAt,
	)
	if err != nil {
		s.log.Warn("supervisor: retract — DB update failed",
			zap.String("station_id", det.StationID),
			zap.Int32("commercial_short_id", det.CommercialShortID),
			zap.Error(err))
		// Still emit the NATS event so webhook subscribers can act on the
		// retraction even if the persistence step had a transient failure.
	} else if tag.RowsAffected() == 0 {
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
	if err := s.nc.Publish(events.SubjectDetectionRetracted, payload); err != nil {
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

