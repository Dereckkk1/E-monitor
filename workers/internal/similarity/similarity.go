// Package similarity scans a freshly-fingerprinted material against the other
// materials of the same client and persists the top similarity match on the
// materials row. Used to warn operators about likely-duplicate uploads
// before they pollute detection reports. See
// docs/superpowers/specs/2026-05-13-material-similarity-warning-design.md.
package similarity

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/fingerprint"
	"radiocheck/internal/index"
	"radiocheck/internal/match"
)

const (
	// WindowSeconds is the analysis window length (matches runtime matcher).
	WindowSeconds = 4
	// HopSeconds is the hop between consecutive analysis windows.
	HopSeconds = 1
	// MinScore is the histogram peak score that qualifies a window as a hit
	// in the cov computation. Aligned with the runtime matcher.
	MinScore = 5
	// MinScoreCoverage is the per-window floor: the histogram peak must reach
	// this FRACTION of the window's total hashes for the window to count as a
	// hit. The runtime ingestor uses 0.02 for exactly this reason; this scan
	// historically passed 0.0, which disabled the guard.
	//
	// Why it matters: spectrally dense audio (e.g. a full 3-minute song, ~350
	// hashes/s) saturates the ~16-bit effective hash space, so ~8% of hash
	// VALUES coincide between two unrelated tracks by pure chance. With a 4s
	// window holding ~1400 hashes, those random collisions routinely pile 5+
	// into the same delta bin — clearing MinScore=5 — so ~1/5 of windows
	// false-hit. The coverage formula then paints 4s per hit, inflating two
	// completely different songs to >50% and tripping the blocking modal.
	// Requiring the peak to be ≥2% of the window (≈28 hashes for music) keeps
	// the spurious peaks (~5) out while real subsets (peak = a large fraction
	// of the window) sail through. See similarity_test.go (dense-audio case).
	MinScoreCoverage = 0.02
	// PersistThreshold is the floor at/above which we PERSIST a match plus its
	// connected segments. Below it the match is noise and the row is cleared.
	// The blocking modal still uses WarnThreshold (0.50) — decided on the
	// frontend; between 0.25 and 0.50 the frontend shows a non-blocking
	// heads-up with the same timeline.
	PersistThreshold = 0.25
	// WarnThreshold is the score (max(ownCov, otherCov)) at or above which
	// we surface the blocking decision modal at upload time. Calibration:
	// <5% noise, 15-25% sting (intentional reuse of a vinheta — not blocking),
	// 50%+ subset / near-duplicate. At 50% we are confident the operator is
	// uploading material that overlaps the existing catalog enough to warrant
	// a hard decision (manter os dois vs remover o novo).
	WarnThreshold = 0.50
)

// frameRange is a half-open [from, until) interval on the time_frame axis.
type frameRange struct{ from, until int32 }

// pairScan holds the per-other-material accumulator during a scan: the other
// material's total frame count plus the matched ranges on both sides of the
// pair (own = the material being scanned, other = the candidate match).
type pairScan struct {
	otherTotalFrames int
	ownRanges        []frameRange
	otherRanges      []frameRange
	windows          []matchedWindow // janelas casadas (own + offset) p/ segmentos
}

// scanReport aggregates a complete scan: own material's total frames and per-
// other-material pair scans. Kept as a named type so pickTopMatch is testable
// without running audio + DB.
type scanReport struct {
	ownTotalFrames int
	perOther       map[uuid.UUID]*pairScan
}

// pickTopMatch returns the (otherID, score) with the highest
// score = max(ownCov, otherCov) across all candidates. Returns (uuid.Nil, 0)
// when the scan is empty or own has zero frames.
//
// Rationale for max: captures asymmetric subset relationships. For a 30s cut
// of a 60s master, ownCov=100% but otherCov=50%. Using max gives 100%, which
// matches operator intuition ("this audio IS that one"). For a sting overlap
// (~20% on both sides), max=20% — still surfaces above the WarnThreshold but
// well below subset territory.
func pickTopMatch(report scanReport) (uuid.UUID, float64) {
	if report.ownTotalFrames == 0 || len(report.perOther) == 0 {
		return uuid.Nil, 0
	}
	var topID uuid.UUID
	var topScore float64
	for otherID, s := range report.perOther {
		ownCov := float64(frameCoverage(s.ownRanges)) / float64(report.ownTotalFrames)
		var otherCov float64
		if s.otherTotalFrames > 0 {
			otherCov = float64(frameCoverage(s.otherRanges)) / float64(s.otherTotalFrames)
		}
		// Clamp at 1.0: window-based hits can extend past the nominal frame
		// total when the last window straddles the end of the material.
		if ownCov > 1.0 {
			ownCov = 1.0
		}
		if otherCov > 1.0 {
			otherCov = 1.0
		}
		score := ownCov
		if otherCov > score {
			score = otherCov
		}
		if score > topScore {
			topScore = score
			topID = otherID
		}
	}
	return topID, topScore
}

// frameCoverage returns the total frame count covered by the union of the
// input ranges (after merging overlaps).
func frameCoverage(rs []frameRange) int {
	if len(rs) == 0 {
		return 0
	}
	merged := mergeRanges(rs)
	total := 0
	for _, r := range merged {
		total += int(r.until - r.from)
	}
	return total
}

// mergeRanges merges overlapping/contiguous ranges into a minimal covering set.
func mergeRanges(rs []frameRange) []frameRange {
	if len(rs) == 0 {
		return nil
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].from < rs[j].from })
	merged := []frameRange{rs[0]}
	for _, r := range rs[1:] {
		last := &merged[len(merged)-1]
		if r.from <= last.until {
			if r.until > last.until {
				last.until = r.until
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

// CheckMaterialSimilarity scans the given material against the other ready
// materials of the same client and writes the top match (if score ≥
// WarnThreshold) to the materials row. Idempotent — re-running for the same
// material state produces no-op UPDATEs.
//
// Heavy by design (decode + match per window). The caller should run after
// fingerprint generation completes. Failures set similarity_check_status to
// 'failed' so they can be retried via the NATS event.
func CheckMaterialSimilarity(ctx context.Context, pool *pgxpool.Pool, materialID uuid.UUID) error {
	// 1. Resolve client_id + master_storage_path + own duration.
	var clientID uuid.UUID
	var masterPath string
	var fpStatus string
	var ownDuration float64
	if err := pool.QueryRow(ctx, `
		SELECT client_id, master_storage_path, fingerprint_status, duration_seconds
		FROM materials WHERE id = $1
	`, materialID).Scan(&clientID, &masterPath, &fpStatus, &ownDuration); err != nil {
		return fmt.Errorf("similarity: lookup material: %w", err)
	}
	if fpStatus != "ready" {
		// Caller should not have fired the event in this case. Mark skipped
		// rather than failed so operators see "no fingerprint" not "scan error".
		_, err := pool.Exec(ctx,
			`UPDATE materials SET similarity_check_status = 'skipped' WHERE id = $1`,
			materialID)
		return err
	}

	// 2. Build the per-client index. Excludes self and skips materials whose
	//    own fingerprint isn't ready. Joins fingerprint_hashes by material id
	//    (post-bridge, fingerprint_hashes.commercial_id is polymorphic).
	idx, shortToID, totalFramesByID, durationByID, err := loadClientIndex(ctx, pool, clientID, materialID)
	if err != nil {
		_ = markFailed(ctx, pool, materialID)
		return fmt.Errorf("similarity: load client index: %w", err)
	}
	if len(idx) == 0 {
		// First material of this client OR no other ready materials. Skip.
		_, err := pool.Exec(ctx,
			`UPDATE materials SET similarity_check_status = 'skipped' WHERE id = $1`,
			materialID)
		return err
	}
	store := index.New()
	store.Swap(idx)

	// 3. Decode the new material's PCM through the same pipeline used at
	//    fingerprint generation so live hashes align with stored hashes.
	pcm, err := fingerprint.DecodePCM(ctx, masterPath, fingerprint.VariantClean)
	if err != nil {
		_ = markFailed(ctx, pool, materialID)
		return fmt.Errorf("similarity: decode master: %w", err)
	}

	// 4. Slide window, run MatchWindow against the per-client index, build report.
	report := runScan(pcm, store, materialID, shortToID, totalFramesByID)

	// 5. Pick top match, persist. Below PersistThreshold (0.25) we treat the
	//    match as noise and clear the row. At/above it we also persist the
	//    connected segments (the timeline data). The blocking decision (≥0.50)
	//    is the frontend's; between 0.25 and 0.50 it shows a non-blocking
	//    heads-up.
	topID, score := pickTopMatch(report)
	if score < PersistThreshold {
		_, err := pool.Exec(ctx, `
			UPDATE materials
			SET similarity_check_status = 'ready',
			    most_similar_material_id = NULL,
			    similarity_score = NULL,
			    similarity_segments = NULL
			WHERE id = $1
		`, materialID)
		return err
	}

	top := report.perOther[topID]
	ownCov, otherCov := coverages(top, report.ownTotalFrames)
	segs := buildSegments(top.windows, 4) // ~0,5s mínimo
	overlap := buildOverlapJSON(ownCov, otherCov, ownDuration, durationByID[topID], segs)

	_, err = pool.Exec(ctx, `
		UPDATE materials
		SET similarity_check_status = 'ready',
		    most_similar_material_id = $2,
		    similarity_score = $3,
		    similarity_segments = $4
		WHERE id = $1
	`, materialID, topID, score, overlap)
	return err
}

func markFailed(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) error {
	_, err := pool.Exec(ctx,
		`UPDATE materials SET similarity_check_status = 'failed' WHERE id = $1`, id)
	return err
}

// loadClientIndex loads all fingerprint hashes belonging to OTHER materials of
// the same client that are currently 'ready'. Returns the index, a
// short_id→material_id map (so MatchWindow results can be translated back to
// FK identity), and the per-material total frame counts.
//
// Filters: same client_id, exclude self, fingerprint_status = 'ready'.
//
// Post-bridge, fingerprint_hashes.commercial_id is polymorphic and holds
// material UUIDs for wizard-uploaded materials, so JOIN materials m ON
// m.id = fh.commercial_id resolves correctly.
func loadClientIndex(
	ctx context.Context,
	pool *pgxpool.Pool,
	clientID uuid.UUID,
	selfID uuid.UUID,
) (index.Index, map[int32]uuid.UUID, map[uuid.UUID]int, map[uuid.UUID]float64, error) {
	rows, err := pool.Query(ctx, `
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id,
		       m.short_id, m.id, m.duration_seconds
		FROM fingerprint_hashes fh
		JOIN materials m ON m.id = fh.commercial_id
		WHERE m.client_id = $1
		  AND m.id != $2
		  AND m.fingerprint_status = 'ready'
	`, clientID, selfID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer rows.Close()

	idx := make(index.Index)
	shortToID := make(map[int32]uuid.UUID)
	totalFramesByID := make(map[uuid.UUID]int)
	durationByID := make(map[uuid.UUID]float64)
	for rows.Next() {
		var hashValue uint32
		var timeFrame int32
		var variantID, rateID int16
		var shortID int32
		var matID uuid.UUID
		var durationSec float64
		if err := rows.Scan(&hashValue, &timeFrame, &variantID, &rateID,
			&shortID, &matID, &durationSec); err != nil {
			return nil, nil, nil, nil, err
		}
		if variantID < 0 || variantID > 255 || rateID < 0 || rateID > 255 {
			return nil, nil, nil, nil, fmt.Errorf(
				"similarity: variant_id=%d or rate_id=%d out of uint8 range",
				variantID, rateID)
		}
		idx[hashValue] = append(idx[hashValue], index.Entry{
			CommercialShortID: shortID,
			VariantID:         uint8(variantID),
			RateID:            uint8(rateID),
			TimeFrame:         timeFrame,
		})
		shortToID[shortID] = matID
		// duration_seconds × (sampleRate / stftHop) = frames.
		totalFramesByID[matID] = int(durationSec * float64(fingerprint.SampleRate) / 2048.0)
		durationByID[matID] = durationSec
	}
	return idx, shortToID, totalFramesByID, durationByID, rows.Err()
}

// runScan slides a WindowSeconds window in HopSeconds increments over pcm,
// runs MatchWindow against the catalog index, and builds a scanReport. Does
// not touch the DB.
func runScan(
	pcm []float32,
	store *index.Store,
	selfID uuid.UUID,
	shortToID map[int32]uuid.UUID,
	totalFramesByID map[uuid.UUID]int,
) scanReport {
	const sampleRate = fingerprint.SampleRate
	const stftHopSamples = 2048
	windowSamples := sampleRate * WindowSeconds
	hopSamples := sampleRate * HopSeconds

	report := scanReport{
		ownTotalFrames: len(pcm) / stftHopSamples,
		perOther:       make(map[uuid.UUID]*pairScan),
	}

	for off := 0; off+windowSamples <= len(pcm); off += hopSamples {
		window := pcm[off : off+windowSamples]
		results := match.MatchWindow(window, store, MinScore, MinScoreCoverage)

		ownStart := int32(off / stftHopSamples)
		ownEnd := int32((off + windowSamples) / stftHopSamples)

		for _, r := range results {
			otherID, ok := shortToID[r.CommercialShortID]
			if !ok || otherID == selfID {
				continue
			}
			s := report.perOther[otherID]
			if s == nil {
				s = &pairScan{otherTotalFrames: totalFramesByID[otherID]}
				report.perOther[otherID] = s
			}
			s.ownRanges = append(s.ownRanges, frameRange{ownStart, ownEnd})
			s.windows = append(s.windows, matchedWindow{ownStart, ownEnd, int32(r.OffsetFrames)})

			// Other range derived from the histogram delta:
			// live_frame - OffsetFrames = entry.TimeFrame.
			xStart := int32(int(ownStart) - r.OffsetFrames)
			xEnd := int32(int(ownEnd) - r.OffsetFrames)
			if xStart > xEnd {
				xStart, xEnd = xEnd, xStart
			}
			if xEnd <= 0 {
				continue
			}
			if xStart < 0 {
				xStart = 0
			}
			s.otherRanges = append(s.otherRanges, frameRange{xStart, xEnd})
		}
	}
	return report
}
