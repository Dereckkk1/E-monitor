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
	Score             int   // histogram peak count (all hits, including shared)
	// UniqueScore is the histogram peak count counting only hits from hashes
	// that are NOT flagged as shared with another commercial. The state
	// machine uses this — not Score — to decide confirmation, so a commercial
	// whose only matches come from a sting it shares with the commercial
	// actually playing cannot accumulate enough evidence to confirm.
	UniqueScore  int
	TotalHashes  int // total hashes in the live window (for coverage ratio)
	OffsetFrames int // estimated offset of detection within the commercial
}

// bestEntry is the per-commercial best (count, variant, rate, deltaBin) tuple
// returned by histogramFromHashes. Using a named type keeps the public-facing
// MatchWindow signature short and makes the test helper easier to read.
type bestEntry struct {
	count     int
	variantID uint8
	rateID    uint8
	deltaBin  int
}

// histogramFromHashes is the inner loop: given a slice of hashes already
// extracted from PCM, lookup each against the store and produce two parallel
// histograms — one counting all hits, one counting only hits whose Entry has
// IsShared=false. Splitting at the histogram level (rather than discarding
// shared hashes outright) keeps the deltaBin peak intact, which is what we
// rely on to estimate OffsetFrames; we just read the unique count off the
// same key after the peak is chosen.
//
// Returns:
//   - bestTotal[commercialID]: the (deltaBin, count) with the highest TOTAL
//     count for that commercial. Used to pick the winning bin.
//   - uniqueByKey[histKey]: per-(commercial,variant,rate,deltaBin) count of
//     non-shared hits. Caller looks up the unique count at the same key
//     bestTotal selected.
func histogramFromHashes(hashes []audio.Hash, store *index.Store) (
	bestTotal map[int32]bestEntry,
	uniqueByKey map[histKey]int,
	totalHashes int,
) {
	totalHashes = len(hashes)
	if totalHashes == 0 {
		return nil, nil, 0
	}

	histTotal := make(map[histKey]int)
	uniqueByKey = make(map[histKey]int)
	for _, h := range hashes {
		for _, entry := range store.Lookup(h.Value) {
			delta := h.TimeFrame - int(entry.TimeFrame)
			k := histKey{
				commercialID: entry.CommercialShortID,
				variantID:    entry.VariantID,
				rateID:       entry.RateID,
				deltaBin:     delta / DeltaBinSize,
			}
			histTotal[k]++
			if !entry.IsShared {
				uniqueByKey[k]++
			}
		}
	}

	bestTotal = make(map[int32]bestEntry)
	for k, count := range histTotal {
		if cur, ok := bestTotal[k.commercialID]; !ok || count > cur.count {
			bestTotal[k.commercialID] = bestEntry{count, k.variantID, k.rateID, k.deltaBin}
		}
	}
	return bestTotal, uniqueByKey, totalHashes
}

// buildHistogram preprocesses PCM (HPF → RMS norm → STFT → peaks → hashes)
// and delegates to histogramFromHashes. Kept as a named function so tests
// that operate on PCM (the existing engine_test.go cases) can still reach
// the histogram building without re-implementing the preprocessing.
func buildHistogram(samples []float32, store *index.Store) (
	bestTotal map[int32]bestEntry,
	uniqueByKey map[histKey]int,
	totalHashes int,
) {
	filtered := audio.ApplyHighPass(samples, 100.0, 16000)
	normalized := audio.NormalizeRMS(filtered, -20.0)
	spec := audio.STFT(normalized)
	peaks := audio.PickPeaks(spec)
	hashes := audio.GenerateHashes(peaks)
	return histogramFromHashes(hashes, store)
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
	best, uniqueByKey, totalHashes := buildHistogram(samples, store)
	if totalHashes == 0 {
		return nil
	}

	minHits := int(float64(totalHashes) * minScoreCoverage)

	var results []MatchResult
	for id, b := range best {
		if b.count >= threshold && b.count >= minHits {
			k := histKey{commercialID: id, variantID: b.variantID, rateID: b.rateID, deltaBin: b.deltaBin}
			results = append(results, MatchResult{
				CommercialShortID: id,
				VariantID:         b.variantID,
				RateID:            b.rateID,
				Score:             b.count,
				UniqueScore:       uniqueByKey[k],
				TotalHashes:       totalHashes,
				OffsetFrames:      b.deltaBin * DeltaBinSize,
			})
		}
	}

	return results
}

// ScanScores returns the raw best score for every commercial that has at least
// one matching hash in the window, without applying any threshold. Used for
// diagnostics and threshold calibration. Reports the TOTAL score (shared +
// unique); diagnostics historically did not distinguish, and a parallel
// "unique" calibration tool can be added later if needed.
func ScanScores(samples []float32, store *index.Store) map[int32]int {
	best, _, _ := buildHistogram(samples, store)
	out := make(map[int32]int, len(best))
	for id, b := range best {
		out[id] = b.count
	}
	return out
}
