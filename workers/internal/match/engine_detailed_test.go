package match

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/index"
	"radiocheck/pkg/audio"
)

// resultsByID converts a []MatchResult into a map keyed by CommercialShortID so
// two result sets can be compared for equality irrespective of slice order.
// MatchWindow builds its slice by ranging over a map, so the order has always
// been nondeterministic; callers never depend on it. Comparing as a set is the
// honest equivalence check.
func resultsByID(rs []MatchResult) map[int32]MatchResult {
	m := make(map[int32]MatchResult, len(rs))
	for _, r := range rs {
		m[r.CommercialShortID] = r
	}
	return m
}

// assertDetailedEquivalent is the heart of the characterization: for the SAME
// (samples, store, threshold, coverage), MatchWindowDetailed must return
//   - a []MatchResult identical (as a set) to MatchWindow's, and
//   - a rawScores map identical to ScanScores's output.
//
// This locks the contract that lets the live worker make ONE buildHistogram
// pass per window instead of the two/three it makes today (MatchWindow +
// ScanScores for the diagnostic + ScanScores for noise sampling).
func assertDetailedEquivalent(t *testing.T, samples []float32, store *index.Store, threshold int, cov float64) {
	t.Helper()

	wantResults := MatchWindow(samples, store, threshold, cov)
	wantScores := ScanScores(samples, store)

	gotResults, gotScores := MatchWindowDetailed(samples, store, threshold, cov)

	assert.True(t, reflect.DeepEqual(resultsByID(wantResults), resultsByID(gotResults)),
		"MatchWindowDetailed results must equal MatchWindow results as a set\nwant=%+v\ngot=%+v",
		wantResults, gotResults)

	// ScanScores returns a non-nil empty map for an empty/no-hit window; the
	// detailed variant may return nil in that case. Both mean "no scores", so
	// normalise empties before comparing.
	if len(wantScores) == 0 && len(gotScores) == 0 {
		return
	}
	assert.True(t, reflect.DeepEqual(wantScores, gotScores),
		"MatchWindowDetailed rawScores must equal ScanScores output\nwant=%+v\ngot=%+v",
		wantScores, gotScores)
}

// TestMatchWindowDetailed_SelfMatch: a signal matched against an index built
// from itself. Results are non-empty and rawScores carries the winning
// commercial's peak count — the exact pair the worker needs in one pass.
func TestMatchWindowDetailed_SelfMatch(t *testing.T) {
	const (
		sampleRate = 16000
		numSamples = 160000
		shortID    = int32(7)
		threshold  = 5
	)
	samples := makeSineWave(880.0, sampleRate, numSamples)
	filtered := audio.ApplyHighPass(samples, 100.0, sampleRate)
	normalized := audio.NormalizeRMS(filtered, -20.0)
	refHashes := audio.GenerateHashes(audio.PickPeaks(audio.STFT(normalized)))
	require.NotEmpty(t, refHashes)
	store := buildIndexFromHashes(refHashes, shortID)

	// Sanity: this scenario must actually exercise the non-empty path, else the
	// test would pass vacuously.
	require.NotEmpty(t, MatchWindow(samples, store, threshold, 0.4),
		"self-match must produce results or the test proves nothing")

	assertDetailedEquivalent(t, samples, store, threshold, 0.4)
}

// TestMatchWindowDetailed_SharedHits reuses the shared-posting index shape so
// the equivalence is proven on results that carry a non-trivial UniqueScore.
func TestMatchWindowDetailed_SharedHits(t *testing.T) {
	const (
		sampleRate = 16000
		numSamples = 160000
		ownerID    = int32(101)
		otherID    = int32(202)
		threshold  = 5
	)
	samples := makeSineWave(770.0, sampleRate, numSamples)
	filtered := audio.ApplyHighPass(samples, 100.0, sampleRate)
	normalized := audio.NormalizeRMS(filtered, -20.0)
	refHashes := audio.GenerateHashes(audio.PickPeaks(audio.STFT(normalized)))
	require.NotEmpty(t, refHashes)

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

	require.NotEmpty(t, MatchWindow(samples, store, threshold, 0.0))
	assertDetailedEquivalent(t, samples, store, threshold, 0.0)
}

// TestMatchWindowDetailed_NoiseEmptyResults exercises the dominant production
// case: nothing is playing, so MatchWindow returns no results, but rawScores
// (the noise/diagnostic signal) must still equal what ScanScores produced.
func TestMatchWindowDetailed_NoiseEmptyResults(t *testing.T) {
	const numSamples = 160000
	rng := rand.New(rand.NewSource(42))
	samples := make([]float32, numSamples)
	for i := range samples {
		samples[i] = rng.Float32()*2 - 1
	}
	refSamples := makeSineWave(440.0, 16000, numSamples)
	refFiltered := audio.ApplyHighPass(refSamples, 100.0, 16000)
	refNorm := audio.NormalizeRMS(refFiltered, -20.0)
	refHashes := audio.GenerateHashes(audio.PickPeaks(audio.STFT(refNorm)))
	store := buildIndexFromHashes(refHashes, 1)

	require.Empty(t, MatchWindow(samples, store, 5, 0.4),
		"noise vs deterministic ref should yield no results (the common case)")
	assertDetailedEquivalent(t, samples, store, 5, 0.4)
}

// TestMatchWindowDetailed_EmptyIndex locks the degenerate path: empty index →
// no results and no scores, with no panic on nil maps.
func TestMatchWindowDetailed_EmptyIndex(t *testing.T) {
	samples := makeSineWave(440.0, 16000, 160000)
	store := index.New()
	assertDetailedEquivalent(t, samples, store, 5, 0.4)
}
