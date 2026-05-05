package match

import (
	"math"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/index"
	"radiocheck/pkg/audio"
)

// makeSineWave generates a sine wave at the given frequency and sample rate.
func makeSineWave(freqHz float64, sampleRate int, numSamples int) []float32 {
	samples := make([]float32, numSamples)
	for i := range samples {
		samples[i] = float32(math.Sin(2 * math.Pi * freqHz * float64(i) / float64(sampleRate)))
	}
	return samples
}

// buildIndexFromHashes builds an index.Store populated with the given hashes
// assigned to commercialShortID.
func buildIndexFromHashes(hashes []audio.Hash, commercialShortID int32) *index.Store {
	idx := make(index.Index)
	for _, h := range hashes {
		idx[h.Value] = append(idx[h.Value], index.Entry{
			CommercialShortID: commercialShortID,
			TimeFrame:         int32(h.TimeFrame),
		})
	}
	store := index.New()
	store.Swap(idx)
	return store
}

// TestMatchWindow_NoMatch verifies that random noise produces no results at
// threshold=5 — random collisions are unlikely to accumulate 5+ matching deltas.
func TestMatchWindow_NoMatch(t *testing.T) {
	const numSamples = 160000 // 10 seconds at 16kHz

	// White noise
	rng := rand.New(rand.NewSource(42))
	samples := make([]float32, numSamples)
	for i := range samples {
		samples[i] = rng.Float32()*2 - 1 // [-1, 1)
	}

	// Build an index from a completely different (deterministic) signal so that
	// the noise window is unlikely to score >= 5 against it.
	refSamples := makeSineWave(440.0, 16000, numSamples)
	refFiltered := audio.ApplyHighPass(refSamples, 100.0, 16000)
	refNorm := audio.NormalizeRMS(refFiltered, -20.0)
	refHashes := audio.GenerateHashes(audio.PickPeaks(audio.STFT(refNorm)))

	store := buildIndexFromHashes(refHashes, 1)

	results := MatchWindow(samples, store, 5)
	assert.Empty(t, results, "random noise should not match a deterministic reference signal at threshold=5")
}

// TestMatchWindow_SelfMatch verifies that matching a signal against an index
// built from that same signal returns at least one result with score >= threshold.
func TestMatchWindow_SelfMatch(t *testing.T) {
	const (
		freqHz     = 880.0
		sampleRate = 16000
		numSamples = 160000 // 10 seconds
		shortID    = int32(7)
		threshold  = 5
	)

	samples := makeSineWave(freqHz, sampleRate, numSamples)

	// Preprocess the same way MatchWindow will, to get the reference hashes.
	filtered := audio.ApplyHighPass(samples, 100.0, sampleRate)
	normalized := audio.NormalizeRMS(filtered, -20.0)
	refHashes := audio.GenerateHashes(audio.PickPeaks(audio.STFT(normalized)))
	require.NotEmpty(t, refHashes, "reference signal must produce hashes")

	store := buildIndexFromHashes(refHashes, shortID)

	results := MatchWindow(samples, store, threshold)
	require.NotEmpty(t, results, "self-match should return at least one result")

	// At least one result should correspond to the registered commercial.
	found := false
	for _, r := range results {
		if r.CommercialShortID == shortID {
			found = true
			assert.GreaterOrEqual(t, r.Score, threshold,
				"score should be >= threshold for self-match")
		}
	}
	assert.True(t, found, "self-match result must contain the registered commercial short ID")
}

// TestMatchWindow_EmptyIndex verifies that an empty index always returns no results.
func TestMatchWindow_EmptyIndex(t *testing.T) {
	samples := makeSineWave(440.0, 16000, 160000)
	store := index.New() // empty index

	results := MatchWindow(samples, store, 5)
	assert.Empty(t, results, "empty index should always produce 0 results")
}
