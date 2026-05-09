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

	results := MatchWindow(samples, store, 5, 0.4)
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

	results := MatchWindow(samples, store, threshold, 0.4)
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

	results := MatchWindow(samples, store, 5, 0.4)
	assert.Empty(t, results, "empty index should always produce 0 results")
}

// TestMatchWindow_UniqueScoreExcludesSharedHits seeds a store where commercial 1
// owns a set of unique hashes plus a set of shared hashes (the same hash_value
// appears under commercial 2 with IsShared=true on both sides). When the live
// audio matches both pools, MatchResult.Score should count every hit but
// MatchResult.UniqueScore should drop the shared ones. This is the engine-side
// guarantee that the state machine relies on to suppress false-positive
// confirmations of a commercial whose only matches come from a sting it shares
// with the commercial actually playing.
func TestMatchWindow_UniqueScoreExcludesSharedHits(t *testing.T) {
	const (
		freqHz     = 770.0
		sampleRate = 16000
		numSamples = 160000
		ownerID    = int32(101)
		otherID    = int32(202)
		threshold  = 5
	)

	samples := makeSineWave(freqHz, sampleRate, numSamples)
	filtered := audio.ApplyHighPass(samples, 100.0, sampleRate)
	normalized := audio.NormalizeRMS(filtered, -20.0)
	refHashes := audio.GenerateHashes(audio.PickPeaks(audio.STFT(normalized)))
	require.NotEmpty(t, refHashes, "reference signal must produce hashes")

	// Build the index by hand: every reference hash gets two postings —
	// one for ownerID with IsShared=true, one for otherID with IsShared=true
	// (i.e. the hash is shared between two commercials). Half the hashes get
	// an EXTRA owner-only posting flagged IsShared=false, simulating a portion
	// of the master that is unique to ownerID.
	idx := make(index.Index)
	for i, h := range refHashes {
		idx[h.Value] = append(idx[h.Value],
			index.Entry{CommercialShortID: ownerID, TimeFrame: int32(h.TimeFrame), IsShared: true},
			index.Entry{CommercialShortID: otherID, TimeFrame: int32(h.TimeFrame), IsShared: true},
		)
		if i%2 == 0 {
			idx[h.Value] = append(idx[h.Value],
				index.Entry{CommercialShortID: ownerID, TimeFrame: int32(h.TimeFrame), IsShared: false},
			)
		}
	}
	store := index.New()
	store.Swap(idx)

	results := MatchWindow(samples, store, threshold, 0.0)
	require.NotEmpty(t, results, "self-match should produce results for both commercials")

	var owner, other *MatchResult
	for i := range results {
		switch results[i].CommercialShortID {
		case ownerID:
			owner = &results[i]
		case otherID:
			other = &results[i]
		}
	}
	require.NotNil(t, owner, "owner commercial must be among results")
	require.NotNil(t, other, "other commercial must be among results")

	// Owner has shared + unique postings → UniqueScore must be >0 and < Score.
	assert.Greater(t, owner.UniqueScore, 0,
		"owner has unique-flagged postings; UniqueScore must be positive")
	assert.Less(t, owner.UniqueScore, owner.Score,
		"owner's UniqueScore must drop the shared hits below the total Score")

	// Other has only shared postings → UniqueScore must be exactly 0.
	assert.Equal(t, 0, other.UniqueScore,
		"other commercial has only shared postings; UniqueScore must be 0")
	assert.Greater(t, other.Score, 0,
		"other commercial still contributes to Score (the histogram peak survives)")
}

// TestMatchWindow_DynamicThreshold confirms that callers can drive MatchWindow
// with thresholds resolved at runtime (the supervisor reads station_thresholds
// every 5 min and pushes the new value into a *atomic.Int32 the worker reads
// on every tick). This test mirrors that pattern: the same window passes at a
// permissive threshold but is rejected at a stricter one — i.e. raising the
// threshold mid-flight will silence false positives without restarting.
func TestMatchWindow_DynamicThreshold(t *testing.T) {
	const (
		freqHz     = 880.0
		sampleRate = 16000
		numSamples = 160000
		shortID    = int32(11)
	)

	samples := makeSineWave(freqHz, sampleRate, numSamples)

	filtered := audio.ApplyHighPass(samples, 100.0, sampleRate)
	normalized := audio.NormalizeRMS(filtered, -20.0)
	refHashes := audio.GenerateHashes(audio.PickPeaks(audio.STFT(normalized)))
	require.NotEmpty(t, refHashes)
	store := buildIndexFromHashes(refHashes, shortID)

	// Permissive threshold: self-match passes.
	low := MatchWindow(samples, store, 5, 0.4)
	require.NotEmpty(t, low, "self-match at threshold=5 should produce results")
	peakScore := 0
	for _, r := range low {
		if r.Score > peakScore {
			peakScore = r.Score
		}
	}
	require.Greater(t, peakScore, 5)

	// Strict threshold above the observed peak: no results.
	high := MatchWindow(samples, store, peakScore+1, 0.4)
	assert.Empty(t, high, "threshold above peak score must reject all matches")
}
