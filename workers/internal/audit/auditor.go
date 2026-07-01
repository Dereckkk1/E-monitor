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

// coverageBinRadius is how many neighbour delta bins on each side of the peak
// are unioned when measuring coverage. Playout-speed jitter over a long spot
// drifts the alignment delta, spreading the matched master frames across
// adjacent bins; the single peak bin then under-counts coverage even when the
// whole spot matched (high score). Unioning the peak ±radius reconstructs the
// real coverage without loosening the FP guard — a sting/jingle that only
// matches a few frames stays low-coverage even after the union.
//
// Radius 2 (peak ±2 = 5 bins) recovers the prod failures observed on 30s cuts
// where single-bin coverage bottomed at ~0.046; tune via prod's
// radiocheck_audit_rejected rate if drift on a station exceeds this envelope.
const coverageBinRadius = 2

// coverageBypassScore lets an overwhelming match skip the coverage gate when the
// master has NO shared hashes. Broadcast degradation on long (30s) cuts destroys
// the fingerprint of the quieter parts, leaving only the robust segment to match:
// the score stays huge (50–176) but coverage lands at ~0.09 even after bin-merge.
// For a non-shared master a score this high cannot be coincidence or a shared
// sting — the clip provably contains the spot, so the coverage gate is pure harm.
// Shared masters keep the coverage gate (a sting-only clip scores high on the
// shared frames at low coverage), so this never loosens the §9.9 FP guard.
const coverageBypassScore = 30

// Result is the outcome of one audit run.
type Result struct {
	Passed       bool
	Score        int     // peak count in the winning histogram bin
	Coverage     float64 // distinct master frames in winning bin / total master frames
	MatchExtent  float64 // furthest matched master frame / total — how DEEP into the master the clip reached (distinguishes a 15s cut, which only matches the first half of a 30s master, from a full 30s)
	VariantID    uint8
	RateID       uint8
	DeltaBin     int
	MasterHashes int           // total entries loaded from fingerprint_hashes for this master
	QueryHashes  int           // hashes generated from evidence PCM
	Duration     time.Duration // wall time of the audit run

	CoveredFrames map[int32]bool // distinct master frames matched in the winning bin (peak ± coverageBinRadius); nil when no match
}

// FrameRange is a half-open interval [Lo, Hi) of master frames.
type FrameRange struct{ Lo, Hi int32 }

// CoverageOnFrames returns the fraction of the discriminative frames (the union
// of the given half-open ranges) that appear in `covered` (master frames the
// clip matched in the winning bin). Empty ranges => 0 (twin pair with no
// separating region).
func CoverageOnFrames(covered map[int32]bool, disc []FrameRange) float64 {
	total, hit := 0, 0
	for _, r := range disc {
		for f := r.Lo; f < r.Hi; f++ {
			total++
			if covered[f] {
				hit++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(hit) / float64(total)
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

	byHash, totalFramesByVR, hasShared, err := a.loadMasterHashes(ctx, commercialID)
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
	res := runMatch(queryHashes, byHash, totalFramesByVR, a.minScore, a.minCoverage, hasShared)
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

// MatchHashes runs the audit match of query hashes against a master supplied as
// a flat hash list (variant 0, rate 0), for offline/local diagnostics where the
// master is fingerprinted from a file instead of loaded from the DB. The master
// is treated as non-shared. Returns the same Result as the DB path, incl.
// MatchExtent.
func MatchHashes(queryHashes, masterHashes []audio.Hash) *Result {
	byHash := make(map[uint32][]HashEntry, len(masterHashes))
	maxFrame := int32(-1)
	for _, h := range masterHashes {
		tf := int32(h.TimeFrame)
		byHash[h.Value] = append(byHash[h.Value], HashEntry{TimeFrame: tf})
		if tf > maxFrame {
			maxFrame = tf
		}
	}
	totals := map[vrKey]int{{0, 0}: int(maxFrame) + 1}
	res := runMatch(queryHashes, byHash, totals, DefaultMinScore, DefaultMinCoverage, false)
	res.QueryHashes = len(queryHashes)
	res.MasterHashes = len(masterHashes)
	return res
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
	materialHasShared bool,
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
	matchExtent := 0.0
	var covered map[int32]bool
	if bestScore > 0 {
		totalFrames := totalFramesByVR[vrKey{bestKey.variant, bestKey.rate}]
		if totalFrames > 0 {
			// Union the distinct master frames across the peak bin and its
			// immediate neighbours to tolerate playout-speed drift over long
			// spots (see coverageBinRadius). maxFrame is the furthest matched
			// master frame — how deep into the master the clip reached.
			covered = make(map[int32]bool)
			maxFrame := int32(-1)
			for d := bestKey.deltaBin - coverageBinRadius; d <= bestKey.deltaBin+coverageBinRadius; d++ {
				for f := range refTimes[binKey{bestKey.variant, bestKey.rate, d}] {
					covered[f] = true
					if f > maxFrame {
						maxFrame = f
					}
				}
			}
			coverage = float64(len(covered)) / float64(totalFrames)
			if maxFrame >= 0 {
				matchExtent = float64(maxFrame+1) / float64(totalFrames)
			}
		}
	}

	return &Result{
		Passed:        bestScore >= minScore && (coverage >= minCoverage || (!materialHasShared && bestScore >= coverageBypassScore)),
		Score:         bestScore,
		Coverage:      coverage,
		MatchExtent:   matchExtent,
		VariantID:     bestKey.variant,
		RateID:        bestKey.rate,
		DeltaBin:      bestKey.deltaBin,
		CoveredFrames: covered,
	}
}

// loadMasterHashes pulls all fingerprint_hashes for the given commercial UUID
// (matches against both commercials and materials id space, which share the
// fingerprint_hashes table). Returns entries grouped by hash_value and total
// frame count per (variant, rate) for coverage normalization.
func (a *Auditor) loadMasterHashes(ctx context.Context, commercialID uuid.UUID) (
	map[uint32][]hashEntry,
	map[vrKey]int,
	bool,
	error,
) {
	rows, err := a.db.Query(ctx, `
		SELECT hash_value, time_frame, variant_id, rate_id, is_shared
		FROM fingerprint_hashes
		WHERE commercial_id = $1
	`, commercialID)
	if err != nil {
		return nil, nil, false, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	byHash := make(map[uint32][]hashEntry)
	maxFrame := make(map[vrKey]int32)
	hasShared := false
	for rows.Next() {
		var hashValue uint32
		var timeFrame int32
		var variantID, rateID int16
		var isShared bool
		if err := rows.Scan(&hashValue, &timeFrame, &variantID, &rateID, &isShared); err != nil {
			return nil, nil, false, fmt.Errorf("scan: %w", err)
		}
		if variantID < 0 || variantID > 255 || rateID < 0 || rateID > 255 {
			return nil, nil, false, fmt.Errorf("variant=%d or rate=%d out of uint8 range", variantID, rateID)
		}
		if isShared {
			hasShared = true
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
		return nil, nil, false, fmt.Errorf("iterate: %w", err)
	}

	totalFrames := make(map[vrKey]int, len(maxFrame))
	for k, v := range maxFrame {
		totalFrames[k] = int(v) + 1
	}
	return byHash, totalFrames, hasShared, nil
}
