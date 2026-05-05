package match

import (
	"radiocheck/internal/index"
	"radiocheck/pkg/audio"
)

// Constantes do engine (§9.3 do plano).
const (
	DeltaBinSize      = 2   // quantiza delta em bins de 2 frames (~256ms)
	MinWindowCoverage = 0.4 // fração mínima de hashes que precisa ter match
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
	OffsetFrames      int   // estimated offset of detection within the commercial
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
//  5. If peak count >= threshold AND >= MinWindowCoverage fraction of hashes, append a MatchResult
//
// Returns all MatchResults with score >= threshold (may be empty).
func MatchWindow(samples []float32, store *index.Store, threshold int) []MatchResult {
	// Step 1: Preprocess
	filtered := audio.ApplyHighPass(samples, 100.0, 16000)
	normalized := audio.NormalizeRMS(filtered, -20.0)

	// Step 2: STFT → PickPeaks → GenerateHashes
	spec := audio.STFT(normalized)
	peaks := audio.PickPeaks(spec)
	hashes := audio.GenerateHashes(peaks)

	totalHashes := len(hashes)
	if totalHashes == 0 {
		return nil
	}

	// Step 3: Build delta histogram grouped by (commercial, variant, rate, deltaBin).
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

	// Step 4: For each commercial, find the variant/rate with the highest score.
	type bestEntry struct {
		count     int
		variantID uint8
		rateID    uint8
		deltaBin  int
	}
	best := make(map[int32]bestEntry)
	for k, count := range histogram {
		if cur, ok := best[k.commercialID]; !ok || count > cur.count {
			best[k.commercialID] = bestEntry{count, k.variantID, k.rateID, k.deltaBin}
		}
	}

	// Step 5: Filter by threshold and MinWindowCoverage.
	minHits := int(float64(totalHashes) * MinWindowCoverage)

	var results []MatchResult
	for id, b := range best {
		if b.count >= threshold && b.count >= minHits {
			results = append(results, MatchResult{
				CommercialShortID: id,
				VariantID:         b.variantID,
				RateID:            b.rateID,
				Score:             b.count,
				OffsetFrames:      b.deltaBin * DeltaBinSize,
			})
		}
	}

	return results
}
