// Package audit implements §9.9 — Audit de Evidência Pré-Persist.
//
// The auditor replays the fingerprint matching algorithm against the master
// attributed to a confirmed detection, using the PCM of the saved evidence
// clip (not the live stream). It guarantees the invariant: "if we ship an
// evidence clip as proof of a veiculação, that clip must contain the master."
//
// Failures here mean the live matcher confirmed a detection whose saved
// evidence does not actually match the master — caused by a wrong evidence
// window, mis-attribution between commercial cuts, mis-calibrated per-station
// thresholds, or segment-extraction picking the wrong moment.
package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"radiocheck/internal/match"
	"radiocheck/pkg/audio"
)

// Production-default thresholds. Espelham o matching live (state machine
// minTemporalCoverage = 0.15) para que o audit não rejeite detecções que o
// produtor live já tinha aceitado — esse é o threshold do §9.3, não 0.4.
// Bug histórico do incidente 2026-05-17: defaults começaram em 0.4 (audit
// ~2.6× mais rigoroso que o live), rejeitando detecções legítimas em massa.
const (
	DefaultMinScore    = 5
	DefaultMinCoverage = 0.15
)

// Result is the outcome of one audit run.
type Result struct {
	Passed       bool
	Score        int     // peak count in the winning histogram bin
	Coverage     float64 // distinct master frames in winning bin / total master frames
	VariantID    uint8
	RateID       uint8
	DeltaBin     int
	MasterHashes int           // total entries loaded from fingerprint_hashes for this master
	QueryHashes  int           // hashes generated from evidence PCM
	Duration     time.Duration // wall time of the audit run
}

// Auditor verifies post-hoc that a saved evidence clip contains the master
// attributed to it.
type Auditor struct {
	db          *pgxpool.Pool
	log         *zap.Logger
	minScore    int
	minCoverage float64
}

// NewAuditor returns an Auditor wired to the database. Pass 0 for either
// threshold to use the §9.3 production defaults.
func NewAuditor(db *pgxpool.Pool, log *zap.Logger, minScore int, minCoverage float64) *Auditor {
	if minScore <= 0 {
		minScore = DefaultMinScore
	}
	if minCoverage <= 0 {
		minCoverage = DefaultMinCoverage
	}
	return &Auditor{db: db, log: log, minScore: minScore, minCoverage: minCoverage}
}

// AuditEvidence runs the audit against the master identified by commercialID
// (uuid in either commercials.id or materials.id — the row in fingerprint_hashes
// uses the same id space). Returns Passed=true iff peak score >= minScore AND
// peak coverage >= minCoverage. Returns error only on infrastructure failures.
func (a *Auditor) AuditEvidence(ctx context.Context, commercialID uuid.UUID, pcm []float32) (*Result, error) {
	start := time.Now()

	byHash, totalFramesByVR, err := a.loadMasterHashes(ctx, commercialID)
	if err != nil {
		return nil, fmt.Errorf("load master hashes: %w", err)
	}
	masterHashCount := 0
	for _, e := range byHash {
		masterHashCount += len(e)
	}
	if masterHashCount == 0 {
		return nil, fmt.Errorf("no master hashes for commercial_id=%s", commercialID)
	}

	queryHashes := PCMToHashes(pcm)
	res := runMatch(queryHashes, byHash, totalFramesByVR, a.minScore, a.minCoverage)
	res.MasterHashes = masterHashCount
	res.QueryHashes = len(queryHashes)
	res.Duration = time.Since(start)
	return res, nil
}

// PCMToHashes runs the worker's preprocess→STFT→peaks→hash pipeline. Exposed
// so callers (and tests) can split decoding from matching.
func PCMToHashes(pcm []float32) []audio.Hash {
	filtered := audio.ApplyHighPass(pcm, 100.0, 16000)
	normalized := audio.NormalizeRMS(filtered, -20.0)
	spec := audio.STFT(normalized)
	peaks := audio.PickPeaks(spec)
	return audio.GenerateHashes(peaks)
}

type vrKey struct {
	variant uint8
	rate    uint8
}

// HashEntry is a single (variant, rate, time_frame) tuple from the master's
// fingerprint_hashes row. Exported so tests can construct master indices
// without touching the database.
type HashEntry struct {
	VariantID uint8
	RateID    uint8
	TimeFrame int32
}

// hashEntry is the package-internal alias kept for backward source compat with
// loadMasterHashes; new code uses HashEntry.
type hashEntry = HashEntry

type binKey struct {
	variant  uint8
	rate     uint8
	deltaBin int
}

// runMatch is the pure-function core of the audit: given query hashes and a
// pre-loaded master index, produce the histogram peak and coverage. Does not
// touch the database, file system, or clock — safe to unit-test with
// hand-crafted inputs.
func runMatch(
	queryHashes []audio.Hash,
	byHash map[uint32][]HashEntry,
	totalFramesByVR map[vrKey]int,
	minScore int,
	minCoverage float64,
) *Result {
	if len(queryHashes) == 0 {
		return &Result{}
	}

	counts := make(map[binKey]int)
	refTimes := make(map[binKey]map[int32]struct{})

	for _, h := range queryHashes {
		entries := byHash[h.Value]
		if len(entries) == 0 {
			continue
		}
		for _, e := range entries {
			delta := int(e.TimeFrame) - h.TimeFrame
			k := binKey{
				variant:  e.VariantID,
				rate:     e.RateID,
				deltaBin: delta / match.DeltaBinSize,
			}
			counts[k]++
			if refTimes[k] == nil {
				refTimes[k] = make(map[int32]struct{})
			}
			refTimes[k][e.TimeFrame] = struct{}{}
		}
	}

	var bestKey binKey
	bestScore := 0
	for k, c := range counts {
		if c > bestScore {
			bestScore = c
			bestKey = k
		}
	}

	coverage := 0.0
	if bestScore > 0 {
		totalFrames := totalFramesByVR[vrKey{bestKey.variant, bestKey.rate}]
		if totalFrames > 0 {
			coverage = float64(len(refTimes[bestKey])) / float64(totalFrames)
		}
	}

	return &Result{
		Passed:    bestScore >= minScore && coverage >= minCoverage,
		Score:     bestScore,
		Coverage:  coverage,
		VariantID: bestKey.variant,
		RateID:    bestKey.rate,
		DeltaBin:  bestKey.deltaBin,
	}
}

// loadMasterHashes pulls all fingerprint_hashes for the given commercial UUID
// (matches against both commercials and materials id space, which share the
// fingerprint_hashes table). Returns entries grouped by hash_value and total
// frame count per (variant, rate) for coverage normalization.
func (a *Auditor) loadMasterHashes(ctx context.Context, commercialID uuid.UUID) (
	map[uint32][]hashEntry,
	map[vrKey]int,
	error,
) {
	rows, err := a.db.Query(ctx, `
		SELECT hash_value, time_frame, variant_id, rate_id
		FROM fingerprint_hashes
		WHERE commercial_id = $1
	`, commercialID)
	if err != nil {
		return nil, nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	byHash := make(map[uint32][]hashEntry)
	maxFrame := make(map[vrKey]int32)
	for rows.Next() {
		var hashValue uint32
		var timeFrame int32
		var variantID, rateID int16
		if err := rows.Scan(&hashValue, &timeFrame, &variantID, &rateID); err != nil {
			return nil, nil, fmt.Errorf("scan: %w", err)
		}
		if variantID < 0 || variantID > 255 || rateID < 0 || rateID > 255 {
			return nil, nil, fmt.Errorf("variant=%d or rate=%d out of uint8 range", variantID, rateID)
		}
		v := uint8(variantID)
		r := uint8(rateID)
		byHash[hashValue] = append(byHash[hashValue], hashEntry{
			VariantID: v,
			RateID:    r,
			TimeFrame: timeFrame,
		})
		k := vrKey{v, r}
		if timeFrame > maxFrame[k] {
			maxFrame[k] = timeFrame
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate: %w", err)
	}

	totalFrames := make(map[vrKey]int, len(maxFrame))
	for k, v := range maxFrame {
		totalFrames[k] = int(v) + 1
	}
	return byHash, totalFrames, nil
}
