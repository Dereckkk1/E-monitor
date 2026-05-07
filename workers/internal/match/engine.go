package match

import (
	"radiocheck/internal/index"
	"radiocheck/pkg/audio"
)

// Constantes do engine (§9.3 do plano).
const (
	DeltaBinSize = 2 // quantiza delta em bins de 2 frames (~256ms)
)

// histKey agrupa por (comercial, variante, rate, bin de delta).
type histKey struct {
	commercialID int32
	variantID    uint8
	rateID       uint8
	deltaBin     int
}

// MatchResult is returned when a commercial scores above the threshold.
type MatchResult struct {
	CommercialShortID int32
	VariantID         uint8 // variant that produced the best score
	RateID            uint8 // rate variant that produced the best score
	Score             int   // histogram peak count
	TotalHashes       int   // total hashes in the live window (for coverage ratio)
	OffsetFrames      int   // estimated offset of detection within the commercial
}

// buildHistogram is the shared inner loop: preprocess → STFT → peaks → hashes → histogram.
// Returns the per-commercial best scores and the total number of live hashes generated.
func buildHistogram(samples []float32, store *index.Store) (best map[int32]struct {
	count     int
	variantID uint8
	rateID    uint8
	deltaBin  int
}, totalHashes int) {
	filtered := audio.ApplyHighPass(samples, 100.0, 16000)
	normalized := audio.NormalizeRMS(filtered, -20.0)
	spec := audio.STFT(normalized)
	peaks := audio.PickPeaks(spec)
	hashes := audio.GenerateHashes(peaks)

	totalHashes = len(hashes)
	if totalHashes == 0 {
		return nil, 0
	}

	histogram := make(map[histKey]int)
	for _, h := range hashes {
		for _, entry := range store.Lookup(h.Value) {
			delta := h.TimeFrame - int(entry.TimeFrame)
			k := histKey{
				commercialID: entry.CommercialShortID,
				variantID:    entry.VariantID,
				rateID:       entry.RateID,
				deltaBin:     delta / DeltaBinSize,
			}
			histogram[k]++
		}
	}

	type entry struct {
		count     int
		variantID uint8
		rateID    uint8
		deltaBin  int
	}
	best = make(map[int32]struct {
		count     int
		variantID uint8
		rateID    uint8
		deltaBin  int
	})
	for k, count := range histogram {
		if cur, ok := best[k.commercialID]; !ok || count > cur.count {
			best[k.commercialID] = struct {
				count     int
				variantID uint8
				rateID    uint8
				deltaBin  int
			}{count, k.variantID, k.rateID, k.deltaBin}
		}
	}
	return best, totalHashes
}

// MatchWindow fingerprints one PCM window and looks up matches in the index.
//
// Algorithm (§9.3 of the plan):
//  1. Preprocess: ApplyHighPass at 100Hz / 16kHz, then NormalizeRMS to -20dB
//  2. STFT → PickPeaks → GenerateHashes
//  3. For each hash, look up index.Lookup(hash.Value):
//     for each Entry in the result:
//       delta = hash.TimeFrame - entry.TimeFrame
//       increment histogram[histKey{commercialID, variantID, rateID, delta/DeltaBinSize}]
//  4. For each commercial, find the (variant, rate, deltaBin) with the highest count
//  5. If peak count >= threshold AND >= minScoreCoverage fraction of hashes, append a MatchResult
//
// minScoreCoverage is the score/totalHashes ratio threshold for THIS window only.
// It is unrelated to the temporal coverage check performed by the state machine.
// Returns all MatchResults satisfying both filters (may be empty).
func MatchWindow(samples []float32, store *index.Store, threshold int, minScoreCoverage float64) []MatchResult {
	best, totalHashes := buildHistogram(samples, store)
	if totalHashes == 0 {
		return nil
	}

	minHits := int(float64(totalHashes) * minScoreCoverage)

	var results []MatchResult
	for id, b := range best {
		if b.count >= threshold && b.count >= minHits {
			results = append(results, MatchResult{
				CommercialShortID: id,
				VariantID:         b.variantID,
				RateID:            b.rateID,
				Score:             b.count,
				TotalHashes:       totalHashes,
				OffsetFrames:      b.deltaBin * DeltaBinSize,
			})
		}
	}

	return results
}

// ScanScores returns the raw best score for every commercial that has at least
// one matching hash in the window, without applying any threshold. Used for
// diagnostics and threshold calibration.
func ScanScores(samples []float32, store *index.Store) map[int32]int {
	best, _ := buildHistogram(samples, store)
	out := make(map[int32]int, len(best))
	for id, b := range best {
		out[id] = b.count
	}
	return out
}
