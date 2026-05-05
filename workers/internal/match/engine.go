package match

import (
	"radiocheck/internal/index"
	"radiocheck/pkg/audio"
)

// MatchResult is returned when a commercial scores above the threshold.
type MatchResult struct {
	CommercialShortID int32
	Score             int // histogram peak count
	OffsetFrames      int // estimated offset of detection within the commercial
}

// MatchWindow fingerprints one PCM window and looks up matches in the index.
//
// Algorithm (§9.3 of the plan):
//  1. Preprocess: ApplyHighPass at 100Hz / 16kHz, then NormalizeRMS to -20dB
//  2. STFT → PickPeaks → GenerateHashes
//  3. For each hash, look up index.Lookup(hash.Value):
//     for each Entry in the result:
//       delta = hash.TimeFrame - entry.TimeFrame
//       increment histogram[entry.CommercialShortID][delta]
//  4. For each commercial, find the delta with the highest count (histogram peak)
//  5. If peak count >= threshold, append a MatchResult
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

	if len(hashes) == 0 {
		return nil
	}

	// Step 3: Build delta histogram
	// histogram: commercial_short_id → delta → count
	histogram := make(map[int32]map[int]int)

	for _, h := range hashes {
		entries := store.Lookup(h.Value)
		for _, entry := range entries {
			delta := h.TimeFrame - int(entry.TimeFrame)
			if _, ok := histogram[entry.CommercialShortID]; !ok {
				histogram[entry.CommercialShortID] = make(map[int]int)
			}
			histogram[entry.CommercialShortID][delta]++
		}
	}

	// Step 4 & 5: Find peak delta per commercial and filter by threshold
	var results []MatchResult
	for commercialID, deltaMap := range histogram {
		bestDelta := 0
		bestCount := 0
		for delta, count := range deltaMap {
			if count > bestCount {
				bestCount = count
				bestDelta = delta
			}
		}
		if bestCount >= threshold {
			results = append(results, MatchResult{
				CommercialShortID: commercialID,
				Score:             bestCount,
				OffsetFrames:      bestDelta,
			})
		}
	}

	return results
}
