package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
	"radiocheck/internal/audit"
	"radiocheck/internal/catalog"
	"radiocheck/internal/events"
	"radiocheck/internal/metrics"
	"radiocheck/internal/observability"
	"radiocheck/internal/segments"
	"radiocheck/internal/storage"
)

type detectionEvent struct {
	StationID           string  `json:"station_id"`
	CommercialShortID   int32   `json:"commercial_short_id"`
	DetectedAt          string  `json:"detected_at"`
	OffsetFrames        int     `json:"offset_frames"`
	Confidence          float64 `json:"confidence"`
	EvidenceWindowStart string  `json:"evidence_window_start"`
	EvidenceWindowEnd   string  `json:"evidence_window_end"`
	HashCount           int32   `json:"hash_count"`
	TemporalCoverage    float64 `json:"temporal_coverage"`
	MatchStartOffsetMs  int32   `json:"match_start_offset_ms"`
	MatchEndOffsetMs    int32   `json:"match_end_offset_ms"`
	VariantUsed         int16   `json:"variant_used"`
	RateUsed            int16   `json:"rate_used"`
}

// Service listens for confirmed detections, extracts audio evidence from the
// per-station segment directory ffmpeg writes to, uploads it to S3, and
// persists detection records to Postgres.
type Service struct {
	db         *pgxpool.Pool
	store      *storage.Client
	nc         *nats.Conn
	detections *catalog.Detections
	auditor    *audit.Auditor // §9.9 — nil disables the pre-upload audit
	log        *zap.Logger
	mu         sync.RWMutex
	// segmentDirs maps station UUIDs to the absolute filesystem directory
	// where ffmpeg is dropping ADTS-AAC segment files. Populated by the
	// supervisor via Register / Unregister as workers come up and down.
	segmentDirs map[uuid.UUID]string
}

// NewService constructs a ready-to-use evidence Service. Pass auditor=nil to
// disable §9.9 audit (e.g. in tests, or as the AUDIT_ENABLED=false kill switch).
func NewService(
	db *pgxpool.Pool,
	store *storage.Client,
	nc *nats.Conn,
	detections *catalog.Detections,
	auditor *audit.Auditor,
	log *zap.Logger,
) *Service {
	return &Service{
		db:          db,
		store:       store,
		nc:          nc,
		detections:  detections,
		auditor:     auditor,
		log:         log,
		segmentDirs: make(map[uuid.UUID]string),
	}
}

// Register associates the on-disk segment directory of a station so that
// evidence can be extracted when a detection is confirmed.
func (s *Service) Register(stationID uuid.UUID, dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.segmentDirs[stationID] = dir
}

// Unregister removes the segment directory association for a station.
func (s *Service) Unregister(stationID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.segmentDirs, stationID)
}

// Subscribe begins consuming "detections.confirmed" NATS messages.
// The returned subscription can be used to unsubscribe when shutting down.
func (s *Service) Subscribe(ctx context.Context) (*nats.Subscription, error) {
	sub, err := s.nc.Subscribe(events.SubjectDetectionConfirmed, func(msg *nats.Msg) {
		s.handle(msg)
	})
	if err != nil {
		return nil, fmt.Errorf("evidence: subscribe: %w", err)
	}
	return sub, nil
}

// handle processes a single detections.confirmed message.
//
// Flow:
//  1. Insert detection right away with evidence_status='pending' so the API
//     can surface it immediately.
//  2. Spawn a goroutine that waits until the requested evidence window is
//     fully captured by the ring buffer, then extracts, encodes, uploads and
//     updates the detection row with the final status.
//
// Errors are logged but never panic.
func (s *Service) handle(msg *nats.Msg) {
	// Extract trace-context from the supervisor's NATS header so the entire
	// evidence pipeline (DB insert, ffmpeg encode, S3 upload) chains under
	// the same trace as the originating detection window.
	ctx, span := observability.StartConsumerSpan(context.Background(), msg, "evidence.process_detection")
	defer span.End()

	var ev detectionEvent
	if err := json.Unmarshal(msg.Data, &ev); err != nil {
		s.log.Warn("evidence: unmarshal failed", zap.Error(err))
		return
	}

	stationID, err := uuid.Parse(ev.StationID)
	if err != nil {
		s.log.Warn("evidence: invalid station_id", zap.String("station_id", ev.StationID), zap.Error(err))
		return
	}
	span.SetAttributes(
		attribute.String("station_id", ev.StationID),
		attribute.Int("commercial_short_id", int(ev.CommercialShortID)),
	)
	detectedAt, err := time.Parse(time.RFC3339, ev.DetectedAt)
	if err != nil {
		s.log.Warn("evidence: invalid detected_at", zap.String("detected_at", ev.DetectedAt), zap.Error(err))
		return
	}
	windowStart, err := time.Parse(time.RFC3339, ev.EvidenceWindowStart)
	if err != nil {
		s.log.Warn("evidence: invalid evidence_window_start", zap.Error(err))
		return
	}
	windowEnd, err := time.Parse(time.RFC3339, ev.EvidenceWindowEnd)
	if err != nil {
		s.log.Warn("evidence: invalid evidence_window_end", zap.Error(err))
		return
	}

	// Resolve which commercial/campaign this detection belongs to.
	// campaign_materials is authoritative: resolveAttribution prefers the
	// active/programada campaign link that targets this station on detectedAt,
	// and only falls back to the legacy commercials.campaign_id for pure-legacy
	// rows. Pre-fix the order was inverted, so a backfilled material reused in a
	// new campaign was attributed to the stale (often concluded) campaign its
	// commercial row still points at. Multi-attribution is a future feature (F-119).
	commercialID, campaignID, lookupErr := resolveAttribution(
		ctx, s.db, ev.CommercialShortID, stationID, detectedAt)

	if lookupErr != nil {
		// Drop the detection — the old code fell through with uuid.Nil
		// and relied on the FK violation to mask the error, but
		// migration 0024 dropped that FK. The column is NOT NULL, so
		// inserting uuid.Nil would either succeed with garbage or fail
		// the NOT NULL constraint. Either way, dropping is correct
		// here: the commercial/material is gone or was never there.
		s.log.Warn("evidence: failed to resolve short_id to commercial or material",
			zap.Int32("short_id", ev.CommercialShortID),
			zap.String("station_id", stationID.String()),
			zap.Error(lookupErr),
		)
		return
	}

	// TemporalCoverage is the same value as Confidence today (both come from
	// CoverageWindow.Coverage()), but it round-trips on its own JSON tag so
	// the schema column reflects what the worker actually sent rather than a
	// silent re-use of Confidence.
	det, err := s.detections.Create(ctx, catalog.CreateDetectionInput{
		StationID:          stationID,
		CommercialID:       commercialID,
		CampaignID:         campaignID,
		DetectedAt:         detectedAt,
		MatchStartOffsetMs: ev.MatchStartOffsetMs,
		MatchEndOffsetMs:   ev.MatchEndOffsetMs,
		Confidence:         ev.Confidence,
		HashCount:          ev.HashCount,
		TemporalCoverage:   ev.TemporalCoverage,
		VariantUsed:        ev.VariantUsed,
		RateUsed:           ev.RateUsed,
	})
	if err != nil {
		s.log.Error("evidence: db insert failed", zap.Error(err))
		return
	}

	s.log.Info("evidence: detection persisted (pending)",
		zap.String("detection_id", det.ID.String()),
		zap.String("station_id", stationID.String()),
		zap.Time("window_end", windowEnd),
	)

	// Async: wait for the window to be fully captured, then extract & upload.
	// Capture the trace context so child spans (extract/encode/upload) chain
	// under the original detection trace even though we drop the parent span
	// before returning from handle().
	go s.processEvidence(ctx, det.ID, det.DetectedAt, stationID, commercialID, windowStart, windowEnd)
}

// processEvidence sleeps until the evidence window is fully captured by the
// ring buffer, then extracts the audio, encodes it to m4a, uploads to S3 and
// updates the detection row with the final evidence status.
//
// parentCtx carries only the trace context inherited from handle(); the
// originating span is already closed by the time this function runs. We
// detach the cancellation chain so a slow encode is not killed when the
// inbound request finishes, but keep the trace IDs so spans nest correctly.
func (s *Service) processEvidence(
	parentCtx context.Context,
	detectionID uuid.UUID,
	detectedAt time.Time,
	stationID uuid.UUID,
	commercialID uuid.UUID,
	windowStart, windowEnd time.Time,
) {
	ctx := context.Background()
	// Re-link to the parent trace without inheriting cancellation. trace.
	// SpanContextFromContext is the cheapest way; observability.Tracer.Start
	// already picks it up via OTel context.
	ctx = traceContextFromParent(parentCtx, ctx)

	// Span covers the full async path so trace shows exact sleep / S3 / DB
	// timings.
	ctx, span := observability.Tracer().Start(ctx, "evidence.process_async")
	span.SetAttributes(
		attribute.String("detection_id", detectionID.String()),
		attribute.String("station_id", stationID.String()),
	)
	defer span.End()

	// Wait until ffmpeg has had time to flush the segment that contains
	// windowEnd. SegmentDuration + a couple of seconds of margin is enough:
	// the segment muxer rotates on wall-clock boundaries, so windowEnd is
	// guaranteed to be on disk by then.
	if delay := time.Until(windowEnd) + segments.SegmentDuration + 2*time.Second; delay > 0 {
		time.Sleep(delay)
	}

	s.mu.RLock()
	dir := s.segmentDirs[stationID]
	s.mu.RUnlock()

	if dir == "" {
		span.SetStatus(codes.Error, "no segment dir")
		s.markFailed(ctx, detectionID, detectedAt, "no segment dir")
		return
	}

	_, extractSpan := observability.Tracer().Start(ctx, "evidence.extract_segments")
	res, err := segments.Extract(dir, windowStart, windowEnd)
	if err != nil {
		extractSpan.RecordError(err)
		extractSpan.SetStatus(codes.Error, err.Error())
		extractSpan.End()
		s.log.Error("evidence: segment extract failed",
			zap.String("detection_id", detectionID.String()),
			zap.String("dir", dir),
			zap.Time("from", windowStart),
			zap.Time("to", windowEnd),
			zap.Error(err),
		)
		s.markFailed(ctx, detectionID, detectedAt, "segment extract failed")
		return
	}
	extractSpan.SetAttributes(
		attribute.Int("bytes", len(res.Data)),
		attribute.Float64("covered_fraction", res.CoveredFraction),
		attribute.Bool("partial", res.Partial),
	)
	extractSpan.End()
	if len(res.Data) == 0 {
		span.SetStatus(codes.Error, "segment extract empty")
		s.markFailed(ctx, detectionID, detectedAt, "segment extract empty")
		return
	}
	if res.Partial {
		s.log.Warn("evidence: partial segment coverage",
			zap.String("detection_id", detectionID.String()),
			zap.Float64("covered_fraction", res.CoveredFraction),
		)
	}
	aacData := res.Data

	// §9.9 — Audit de Evidência Pré-Persist. Decode the extracted AAC to PCM
	// and replay fingerprint matching against the attributed master. If the
	// audit fails the saved clip does not actually contain the master we said
	// it does — mark the row audit_rejected, skip upload, surface metric.
	if s.auditor != nil {
		if s.runAuditOrReject(ctx, detectionID, detectedAt, commercialID, aacData) {
			// runAuditOrReject already marked the detection and logged.
			span.SetStatus(codes.Error, "audit_rejected")
			return
		}
	}

	_, encodeSpan := observability.Tracer().Start(ctx, "evidence.ffmpeg_encode")
	m4aData, err := encodeToM4A(aacData, detectionID, detectedAt)
	encodeSpan.SetAttributes(attribute.Int("bytes_in", len(aacData)), attribute.Int("bytes_out", len(m4aData)))
	if err != nil {
		encodeSpan.RecordError(err)
		encodeSpan.SetStatus(codes.Error, err.Error())
		encodeSpan.End()
		s.log.Error("evidence encoding failed", zap.String("detection_id", detectionID.String()), zap.Error(err))
		s.markFailed(ctx, detectionID, detectedAt, "encode failed")
		return
	}
	encodeSpan.End()

	key := fmt.Sprintf("evidences/%s/%s/%s/%s/%s.m4a",
		detectedAt.Format("2006"), detectedAt.Format("01"), detectedAt.Format("02"),
		stationID, detectionID,
	)

	uploadCtx, uploadSpan := observability.Tracer().Start(ctx, "evidence.s3_upload")
	uploadSpan.SetAttributes(attribute.String("s3.key", key), attribute.Int("bytes", len(m4aData)))
	if err := s.store.Put(uploadCtx, key, bytes.NewReader(m4aData), "video/mp4"); err != nil {
		uploadSpan.RecordError(err)
		uploadSpan.SetStatus(codes.Error, err.Error())
		uploadSpan.End()
		s.log.Error("evidence upload failed", zap.String("detection_id", detectionID.String()), zap.Error(err))
		s.markFailed(ctx, detectionID, detectedAt, "upload failed")
		return
	}
	uploadSpan.End()

	persistCtx, persistSpan := observability.Tracer().Start(ctx, "evidence.persist_path")
	if err := s.detections.UpdateEvidence(persistCtx, detectionID, detectedAt, "available", key, int64(len(m4aData))); err != nil {
		persistSpan.RecordError(err)
		persistSpan.SetStatus(codes.Error, err.Error())
		persistSpan.End()
		s.log.Error("evidence: update evidence failed", zap.String("detection_id", detectionID.String()), zap.Error(err))
		return
	}
	persistSpan.End()

	s.log.Info("evidence: ready",
		zap.String("detection_id", detectionID.String()),
		zap.Int("bytes", len(m4aData)),
	)
}

// traceContextFromParent re-attaches the parent's OTel SpanContext to a fresh
// context.Background(). This lets the async goroutine survive the parent's
// cancellation while keeping the trace continuity.
func traceContextFromParent(parent, child context.Context) context.Context {
	// trace.ContextWithSpanContext is exposed through the trace package; we
	// avoid pulling it just for this and use the observability helper to
	// keep imports minimal.
	return observability.PropagateTraceContext(parent, child)
}

// runAuditOrReject decodes the extracted AAC to PCM, runs §9.9 audit, and on
// failure marks the detection's evidence_status as "audit_rejected" plus emits
// metrics/logs. Returns true when the audit rejected (caller must abort the
// upload path); returns false otherwise (caller continues normally).
//
// Audit infrastructure errors (DB unavailable, ffmpeg failure) are logged but
// treated as "audit could not run" and the upload proceeds — the audit is a
// safety net, not a hard dependency. Those count under result="error".
func (s *Service) runAuditOrReject(
	ctx context.Context,
	detectionID uuid.UUID,
	detectedAt time.Time,
	commercialID uuid.UUID,
	aacData []byte,
) bool {
	auditCtx, auditSpan := observability.Tracer().Start(ctx, "evidence.audit")
	defer auditSpan.End()

	pcm, err := audit.DecodeADTSToPCM(aacData)
	if err != nil {
		auditSpan.RecordError(err)
		auditSpan.SetStatus(codes.Error, "decode failed")
		s.log.Warn("evidence: audit decode failed; proceeding with upload",
			zap.String("detection_id", detectionID.String()),
			zap.Error(err),
		)
		metrics.AuditAttempts.WithLabelValues("error").Inc()
		return false
	}

	result, err := s.auditor.AuditEvidence(auditCtx, commercialID, pcm)
	if err != nil {
		auditSpan.RecordError(err)
		auditSpan.SetStatus(codes.Error, "audit run failed")
		s.log.Warn("evidence: audit run failed; proceeding with upload",
			zap.String("detection_id", detectionID.String()),
			zap.String("commercial_id", commercialID.String()),
			zap.Error(err),
		)
		metrics.AuditAttempts.WithLabelValues("error").Inc()
		return false
	}

	auditSpan.SetAttributes(
		attribute.Int("audit.score", result.Score),
		attribute.Float64("audit.coverage", result.Coverage),
		attribute.Int("audit.variant", int(result.VariantID)),
		attribute.Int("audit.master_hashes", result.MasterHashes),
		attribute.Int("audit.query_hashes", result.QueryHashes),
		attribute.Bool("audit.passed", result.Passed),
	)
	metrics.AuditScore.Observe(float64(result.Score))
	metrics.AuditCoverage.Observe(result.Coverage)
	metrics.AuditDuration.Observe(result.Duration.Seconds())

	if result.Passed {
		metrics.AuditAttempts.WithLabelValues("passed").Inc()
		// Persist the audit coverage so a sibling cut (15s vs 30s of the same
		// client in this break) can later compare how much of each master the
		// clip covered — the basis of the coverage-based disambiguation
		// (§18.2.2 v2). Non-blocking: a failure here must not abort the upload.
		if err := s.detections.SetAuditCoverage(ctx, detectionID, detectedAt, result.Coverage); err != nil {
			s.log.Warn("evidence: failed to persist audit_coverage (non-blocking)",
				zap.String("detection_id", detectionID.String()),
				zap.Error(err),
			)
		}
		s.log.Info("evidence: audit passed",
			zap.String("detection_id", detectionID.String()),
			zap.String("commercial_id", commercialID.String()),
			zap.Int("score", result.Score),
			zap.Float64("coverage", result.Coverage),
			zap.Float64("extent", result.MatchExtent),
			zap.Duration("duration", result.Duration),
		)
		return false
	}

	metrics.AuditAttempts.WithLabelValues("rejected").Inc()
	s.log.Warn("evidence: audit REJECTED — clip does not match master",
		zap.String("detection_id", detectionID.String()),
		zap.String("commercial_id", commercialID.String()),
		zap.Int("score", result.Score),
		zap.Int("min_score", audit.DefaultMinScore),
		zap.Float64("coverage", result.Coverage),
		zap.Float64("min_coverage", audit.DefaultMinCoverage),
		zap.Float64("extent", result.MatchExtent),
		zap.Int("master_hashes", result.MasterHashes),
		zap.Int("query_hashes", result.QueryHashes),
	)
	if err := s.detections.UpdateEvidence(ctx, detectionID, detectedAt, "audit_rejected", "", 0); err != nil {
		s.log.Error("evidence: failed to mark audit_rejected",
			zap.String("detection_id", detectionID.String()),
			zap.Error(err),
		)
	}
	return true
}

func (s *Service) markFailed(ctx context.Context, detectionID uuid.UUID, detectedAt time.Time, reason string) {
	s.log.Warn("evidence: marking failed",
		zap.String("detection_id", detectionID.String()),
		zap.String("reason", reason),
	)
	if err := s.detections.UpdateEvidence(ctx, detectionID, detectedAt, "failed", "", 0); err != nil {
		s.log.Error("evidence: failed-status update failed", zap.Error(err))
	}
}

// encodeToM4A wraps raw ADTS-format AAC bytes in an m4a (MP4) container using
// ffmpeg. Temporary files are created and cleaned up via defer.
func encodeToM4A(aacData []byte, detectionID uuid.UUID, detectedAt time.Time) ([]byte, error) {
	// Write raw ADTS to a temp file.
	tmpIn, err := os.CreateTemp("", "evidence-*.aac")
	if err != nil {
		return nil, fmt.Errorf("create temp input: %w", err)
	}
	defer os.Remove(tmpIn.Name())
	if _, err := tmpIn.Write(aacData); err != nil {
		tmpIn.Close()
		return nil, fmt.Errorf("write temp input: %w", err)
	}
	tmpIn.Close()

	tmpOut, err := os.CreateTemp("", "evidence-*.m4a")
	if err != nil {
		return nil, fmt.Errorf("create temp output: %w", err)
	}
	defer os.Remove(tmpOut.Name())
	tmpOut.Close()

	cmd := exec.Command("ffmpeg", "-y",
		"-f", "aac",
		"-i", tmpIn.Name(),
		"-c:a", "copy",
		"-movflags", "+faststart",
		"-metadata", fmt.Sprintf("title=Evidência %s", detectionID),
		"-metadata", "artist=Sistema de Monitoramento",
		"-metadata", fmt.Sprintf("date=%s", detectedAt.Format(time.RFC3339)),
		tmpOut.Name(),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg encode: %w: %s", err, out)
	}

	return os.ReadFile(tmpOut.Name())
}
