package similarity

import (
	"math/rand"
	"testing"

	"github.com/google/uuid"

	"radiocheck/internal/fingerprint"
	"radiocheck/internal/index"
	"radiocheck/pkg/audio"
)

// makeDenseNoise builds deterministic broadband noise. Run through the
// fingerprint pipeline it yields ~300+ hashes/s — the same density regime as a
// full-length music track, which is what triggered the false-positive
// similarity bug (two unrelated songs scoring >50%).
func makeDenseNoise(seed int64, seconds int) []float32 {
	r := rand.New(rand.NewSource(seed))
	n := seconds * fingerprint.SampleRate
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(r.Float64()*2 - 1)
	}
	return out
}

func denseHashes(pcm []float32) []audio.Hash {
	filtered := audio.ApplyHighPass(pcm, 100.0, 16000)
	normalized := audio.NormalizeRMS(filtered, -20.0)
	spec := audio.STFT(normalized)
	peaks := audio.PickPeaks(spec)
	return audio.GenerateHashes(peaks)
}

func indexFromHashes(hs []audio.Hash, short int32) index.Index {
	idx := make(index.Index)
	for _, h := range hs {
		idx[h.Value] = append(idx[h.Value], index.Entry{
			CommercialShortID: short,
			TimeFrame:         int32(h.TimeFrame),
		})
	}
	return idx
}

// TestRunScan_DenseUnrelatedAudioStaysBelowThreshold is the regression guard for
// the dense-audio false positive. Two unrelated dense tracks must NOT cross
// WarnThreshold. Before the fix (runScan passed minScoreCoverage=0.0) random
// hash-value collisions piled 5+ into a delta bin in ~1/5 of windows, and the
// coverage formula inflated that to >50%. runScan now passes MinScoreCoverage,
// so the spurious peaks are filtered out.
func TestRunScan_DenseUnrelatedAudioStaysBelowThreshold(t *testing.T) {
	pcmA := makeDenseNoise(1, 90)
	hashesB := denseHashes(makeDenseNoise(2, 90))

	const shortB int32 = 7
	otherID := uuid.New()
	store := index.New()
	store.Swap(indexFromHashes(hashesB, shortB))

	shortToID := map[int32]uuid.UUID{shortB: otherID}
	totalFramesByID := map[uuid.UUID]int{otherID: len(pcmA) / 2048}

	report := runScan(pcmA, store, uuid.New(), shortToID, totalFramesByID)
	_, score := pickTopMatch(report)

	if score >= WarnThreshold {
		t.Fatalf("unrelated dense audio scored %.1f%% (>= WarnThreshold %.0f%%) — "+
			"the per-window coverage guard (MinScoreCoverage) is not filtering "+
			"spurious peaks", score*100, WarnThreshold*100)
	}
	t.Logf("unrelated dense audio score=%.1f%% (correctly below %.0f%%)", score*100, WarnThreshold*100)
}

// TestRunScan_RealSubsetStillMatches proves the coverage guard does not mask
// legitimate near-duplicates: a window-aligned subset (the first 30s of B
// scanned against B's own index) must still score high.
func TestRunScan_RealSubsetStillMatches(t *testing.T) {
	pcmB := makeDenseNoise(2, 90)
	hashesB := denseHashes(pcmB)

	const shortB int32 = 7
	otherID := uuid.New()
	store := index.New()
	store.Swap(indexFromHashes(hashesB, shortB))

	shortToID := map[int32]uuid.UUID{shortB: otherID}
	totalFramesByID := map[uuid.UUID]int{otherID: len(pcmB) / 2048}

	cut := pcmB[30*fingerprint.SampleRate : 60*fingerprint.SampleRate]
	report := runScan(cut, store, uuid.New(), shortToID, totalFramesByID)
	_, score := pickTopMatch(report)

	if score < 0.90 {
		t.Fatalf("a real 30s subset scored only %.1f%% — coverage guard is too "+
			"aggressive and would mask legitimate duplicates", score*100)
	}
	t.Logf("real subset score=%.1f%% (correctly high)", score*100)
}
