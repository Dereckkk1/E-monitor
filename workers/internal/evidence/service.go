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
	db                 *pgxpool.Pool
	store              *storage.Client
	nc                 *nats.Conn
	detections         *catalog.Detections
	detectionCampaigns *catalog.DetectionCampaigns // F-119: projeções por campanha
	auditor            *audit.Auditor              // §9.9 — nil disables the pre-upload audit
	// multiAttribution liga o fan-out F-119: uma tocada física vira N projeções
	// (uma por campanha que roda o mesmo áudio na emissora). OFF = só a projeção
	// canônica (1:1), comportamento idêntico ao pré-F-119. Flag MULTI_ATTRIBUTION.
	multiAttribution bool
	// disambigByCoverage gates the coverage-based version disambiguation
	// correction (§18.2.2 v2). When false, runAuditOrReject never looks at
	// sibling cuts and behaviour is identical to pre-fix. Flipped via the
	// DISAMBIG_BY_COVERAGE env var in cmd/api/main.go.
	disambigByCoverage bool
	// disambigTwin liga a desambiguação por trecho discriminante de gêmeos
	// acústicos de MESMA duração (spec 2026-07-01). Roda no pass-path DEPOIS do
	// reattributeByCoverage, só quando ele NÃO agiu. Flag DISAMBIG_TWIN_DISCRIMINATIVE.
	disambigTwin bool
	// twinRetractAmbiguous decide o que fazer quando o sinal discriminante é
	// fraco/confuso (verdictAmbiguous): true → retrata (MarkAmbiguous, vai pra
	// revisão); false (DEFAULT) → NÃO retrata, preserva a atribuição atual. Sem a
	// fila de revisão (Plano 2), retrair sub-contaria a tocada — então o default é
	// não-retrair (preserva contagem; só reatribui em sinal CLARO). Ligar só depois
	// do Plano 2, via DISAMBIG_TWIN_RETRACT_AMBIGUOUS. Validado no dry-run 2026-07-02
	// (trio 45/46/48 sai lamacento por window-straddle → não deve sumir da contagem).
	twinRetractAmbiguous bool
	log                  *zap.Logger
	mu           sync.RWMutex
	// segmentDirs maps station UUIDs to the absolute filesystem directory
	// where ffmpeg is dropping ADTS-AAC segment files. Populated by the
	// supervisor via Register / Unregister as workers come up and down.
	segmentDirs map[uuid.UUID]string
}

// NewService constructs a ready-to-use evidence Service. Pass auditor=nil to
// disable §9.9 audit (e.g. in tests, or as the AUDIT_ENABLED=false kill switch).
// disambigByCoverage=false keeps the pre-§18.2.2-v2 behaviour (no coverage-based
// reattribution); main.go flips it from the DISAMBIG_BY_COVERAGE env var.
func NewService(
	db *pgxpool.Pool,
	store *storage.Client,
	nc *nats.Conn,
	detections *catalog.Detections,
	detectionCampaigns *catalog.DetectionCampaigns,
	auditor *audit.Auditor,
	disambigByCoverage bool,
	disambigTwin bool,
	twinRetractAmbiguous bool,
	multiAttribution bool,
	log *zap.Logger,
) *Service {
	return &Service{
		db:                   db,
		store:                store,
		nc:                   nc,
		detections:           detections,
		detectionCampaigns:   detectionCampaigns,
		auditor:              auditor,
		disambigByCoverage:   disambigByCoverage,
		disambigTwin:         disambigTwin,
		twinRetractAmbiguous: twinRetractAmbiguous,
		multiAttribution:     multiAttribution,
		log:                log,
		segmentDirs:        make(map[uuid.UUID]string),
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

	// F-119: grava as projeções por campanha. Sempre a canônica (1:1, =
	// comportamento pré-F-119). Com MULTI_ATTRIBUTION, faz fan-out pras demais
	// campanhas que rodam o MESMO áudio na emissora. A tocada física (det) e
	// seu fluxo de evidência/audit/dedup ficam INTOCADOS — isto é só atribuição.
	if s.detectionCampaigns != nil {
		projs := []catalog.Projection{{CampaignID: campaignID, CommercialID: commercialID, Category: det.Category}}
		if s.multiAttribution {
			all, aerr := resolveAllAttributions(ctx, s.db, commercialID, stationID, detectedAt)
			if aerr != nil {
				s.log.Warn("evidence: multi-attribution resolveAll falhou; só a canônica",
					zap.String("detection_id", det.ID.String()), zap.Error(aerr))
			} else {
				seen := map[uuid.UUID]bool{campaignID: true}
				for _, p := range all {
					if seen[p.CampaignID] {
						continue
					}
					seen[p.CampaignID] = true
					cat, cerr := s.detections.CategorizeFor(ctx, p.CampaignID, p.CommercialID, stationID, detectedAt)
					if cerr != nil {
						cat = "orphan"
					}
					projs = append(projs, catalog.Projection{CampaignID: p.CampaignID, CommercialID: p.CommercialID, Category: cat})
				}
			}
		}
		if perr := s.detectionCampaigns.InsertProjections(ctx, det.ID, det.DetectedAt, projs); perr != nil {
			s.log.Error("evidence: InsertProjections falhou",
				zap.String("detection_id", det.ID.String()), zap.Error(perr))
		}
	}

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
		if s.runAuditOrReject(ctx, detectionID, detectedAt, stationID, commercialID, aacData) {
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
	stationID uuid.UUID,
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
		// §18.2.2 v2 — coverage-based reattribution (gated). Re-fingerprint THIS
		// clip against the client's sibling cuts; if one covers materially more,
		// the live matcher attributed the wrong cut (the 15s/30s confusion) and
		// we re-point the row. Gated by DISAMBIG_BY_COVERAGE; never blocks upload.
		reattributed := false
		if s.disambigByCoverage {
			reattributed = s.reattributeByCoverage(auditCtx, detectionID, detectedAt, stationID, commercialID, result.Coverage, pcm)
		}
		// Desambiguação de gêmeos acústicos de mesma duração (spec 2026-07-01):
		// pega o caso que o passo de cobertura-cheia NÃO resolve (empate entre
		// gêmeos ~idênticos), medindo a cobertura do trecho discriminante. Só roda
		// se o v2 acima NÃO agiu (não desfaz/duplica a decisão dele). Gated por
		// DISAMBIG_TWIN_DISCRIMINATIVE; best-effort, nunca bloqueia o upload.
		if s.disambigTwin && !reattributed {
			s.disambiguateTwin(auditCtx, detectionID, detectedAt, stationID, commercialID, pcm)
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
	// §18.2.2 v2 — reject-path recovery. A LONGER cut failing the audit almost
	// always means a real SHORTER airing was counted as the longer cut: v1
	// retracted the shorter cut (by duration) and the longer one just failed the
	// §9.9 audit because the clip is the shorter cut's audio. Restore the shorter
	// sibling v1 displaced (it already passed its own audit) so the genuine airing
	// counts. Gated by DISAMBIG_BY_COVERAGE; best-effort, never blocks. The
	// passed-path reattributeByCoverage above only catches misattributions that
	// PASS the audit — this catches the ones that get rejected (the common case
	// in prod: 104/110 measured losses).
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
	return true
}

// reattributeByCoverage implements §18.2.2 v2. After the primary audit passes,
// it re-fingerprints the SAME evidence clip against each sibling cut of the
// client (masters from the catalog — siblings need no detection row). If a
// sibling's coverage beats the attributed cut's by coverageMargin, the live
// matcher picked the wrong cut (the 15s-counted-as-30s bug) and the detection is
// re-pointed at the real cut. Returns true when it reattributed. Best-effort:
// any failure logs and leaves the original attribution untouched.
//
// No speculative publish and no retraction race (cf. the rejected Design A):
// exactly one row exists and it is corrected in place. Empirically the coverage
// margin is ~4-5× on real ASAAS clips (see disambig_coverage_test.go), far above
// coverageMargin (1.5), so a true 30s airing keeps its cut and only genuine
// misattributions flip.
func (s *Service) reattributeByCoverage(
	ctx context.Context,
	detectionID uuid.UUID,
	detectedAt time.Time,
	stationID, commercialID uuid.UUID,
	attributedCoverage float64,
	pcm []float32,
) bool {
	self, sibs, err := s.detections.FindCutWithSiblings(ctx, commercialID)
	if err != nil {
		s.log.Warn("evidence: reattribute — sibling lookup failed",
			zap.String("detection_id", detectionID.String()), zap.Error(err))
		return false
	}
	if len(sibs) == 0 {
		return false // no siblings → nothing to disambiguate
	}

	best := CutCoverage{ShortID: self.ShortID, DurationSeconds: self.DurationSeconds, Coverage: attributedCoverage}
	for _, sib := range sibs {
		res, err := s.auditor.AuditEvidence(ctx, sib.ID, pcm)
		if err != nil {
			s.log.Warn("evidence: reattribute — sibling audit failed",
				zap.String("detection_id", detectionID.String()),
				zap.Int32("sibling_short_id", sib.ShortID), zap.Error(err))
			continue
		}
		cand := CutCoverage{ShortID: sib.ShortID, DurationSeconds: sib.DurationSeconds, Coverage: res.Coverage}
		if chooseByCoverage(best, cand) == cand.ShortID {
			best = cand
		}
	}
	if best.ShortID == self.ShortID {
		return false // attributed cut already wins → leave as-is
	}

	// §18.2.2-v2 co-fire guard. O irmão vencedor pode JÁ ter a tocada própria na
	// janela: quando um material curto (subset de um spot longo) toca, o spot casa
	// fraco na região compartilhada e cairia aqui. Reatribuir criaria contagem
	// dobrada (incidente PILECCO 2026-06-30 — ver
	// docs/superpowers/specs/2026-06-30-cofire-reattribution-dedup-design.md).
	// Reusa o classificador do reject-path; no pass-path `self` está APROVADA, então
	// Skip/Restore retratam `self`.
	existing, ferr := s.detections.FindSiblingDetectionInWindow(ctx, best.ShortID, stationID, detectedAt, recoverRejWindowSeconds)
	if ferr != nil {
		s.log.Warn("evidence: co-fire — winner row lookup failed",
			zap.String("detection_id", detectionID.String()), zap.Error(ferr))
		return false
	}
	switch decideCofireAction(existing) {
	case cofireRestoreThenRetractSelf:
		// v1 deslocou (retraiu) a tocada real do vencedor, que ainda tem evidência
		// available; restaura o vencedor e retrata a duplicata. NB: as duas escritas
		// NÃO são transacionais (Execs separados) — um crash no meio deixa o vencedor
		// restaurado + self vivo (duplicata transitória), varrida pelo reparo
		// keep-max-coverage (spec §6). Consistente com a filosofia in-place do v2.
		if cerr := s.detections.ClearRetraction(ctx, existing.ID, existing.DetectedAt); cerr != nil {
			s.log.Warn("evidence: co-fire — restore winner failed",
				zap.String("detection_id", detectionID.String()), zap.Error(cerr))
			return false
		}
		if rerr := s.detections.RetractByID(ctx, detectionID, detectedAt, time.Now().UTC()); rerr != nil {
			s.log.Warn("evidence: co-fire — retract duplicate failed",
				zap.String("detection_id", detectionID.String()), zap.Error(rerr))
			return false
		}
		metrics.MatchDisambiguation.WithLabelValues("duplicate_cofire_retracted").Inc()
		s.log.Info("evidence: co-fire — restored winner, retracted duplicate (§18.2.2 v2)",
			zap.String("detection_id", detectionID.String()),
			zap.String("winner_detection_id", existing.ID.String()),
			zap.Int32("winner_short_id", best.ShortID))
		return true
	case cofireRetractSelf:
		// Vencedor já conta a veiculação → esta row é a mesma tocada. Retrata.
		if rerr := s.detections.RetractByID(ctx, detectionID, detectedAt, time.Now().UTC()); rerr != nil {
			s.log.Warn("evidence: co-fire — retract duplicate failed",
				zap.String("detection_id", detectionID.String()), zap.Error(rerr))
			return false
		}
		metrics.MatchDisambiguation.WithLabelValues("duplicate_cofire_retracted").Inc()
		s.log.Info("evidence: co-fire — winner already counted, retracted duplicate (§18.2.2 v2)",
			zap.String("detection_id", detectionID.String()),
			zap.String("winner_detection_id", existing.ID.String()),
			zap.Int32("winner_short_id", best.ShortID))
		return true
	case cofireReattribute:
		// Vencedor sem row (15s-contado-como-30s legítimo) OU presente mas NÃO
		// contado (audit_rejected → retratar self zeraria a tocada, M-1). Cai pro
		// fluxo normal abaixo (resolveAttribution + ReattributeDetection).
	}

	// A sibling covers materially more. Resolve its campaign/material for this
	// station+time and re-point the row. If the winner has no live campaign for
	// this station, leave attribution unchanged (don't invent a detection).
	newCommercialID, newCampaignID, err := resolveAttribution(ctx, s.db, best.ShortID, stationID, detectedAt)
	if err != nil {
		s.log.Warn("evidence: reattribute — winner has no resolvable campaign; leaving as-is",
			zap.String("detection_id", detectionID.String()),
			zap.Int32("winner_short_id", best.ShortID), zap.Error(err))
		return false
	}
	if err := s.detections.ReattributeDetection(ctx, detectionID, detectedAt, newCommercialID, newCampaignID, stationID); err != nil {
		s.log.Error("evidence: reattribute — update failed",
			zap.String("detection_id", detectionID.String()), zap.Error(err))
		return false
	}
	// Reflect the corrected coverage on the row.
	if err := s.detections.SetAuditCoverage(ctx, detectionID, detectedAt, best.Coverage); err != nil {
		s.log.Warn("evidence: reattribute — audit_coverage update failed (non-blocking)",
			zap.String("detection_id", detectionID.String()), zap.Error(err))
	}
	metrics.MatchDisambiguation.WithLabelValues("reattributed_by_coverage").Inc()
	s.log.Info("evidence: detection reattributed by coverage (§18.2.2 v2)",
		zap.String("detection_id", detectionID.String()),
		zap.Int32("from_short_id", self.ShortID),
		zap.Float64("from_coverage", attributedCoverage),
		zap.Int32("to_short_id", best.ShortID),
		zap.Float64("to_coverage", best.Coverage),
	)
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
