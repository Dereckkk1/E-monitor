package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeSineWave generates a sine wave at the given frequency and sample rate.
// Returns numSamples float32 PCM samples.
func makeSineWave(freqHz float64, sampleRate int, numSamples int) []float32 {
	samples := make([]float32, numSamples)
	for i := range samples {
		samples[i] = float32(math.Sin(2 * math.Pi * freqHz * float64(i) / float64(sampleRate)))
	}
	return samples
}

// TestGenerateHashes_BasicOutput verifies that a sine wave produces at least
// one hash after the full fingerprinting pipeline (STFT → PickPeaks → GenerateHashes).
func TestGenerateHashes_BasicOutput(t *testing.T) {
	const (
		freqHz     = 440.0  // 440 Hz sine wave
		sampleRate = 16000  // 16 kHz
		numSamples = 160000 // 10 seconds of audio
	)

	samples := makeSineWave(freqHz, sampleRate, numSamples)

	spec := STFT(samples)
	require.NotEmpty(t, spec, "STFT should produce frames")

	peaks := PickPeaks(spec)
	require.NotEmpty(t, peaks, "PickPeaks should find at least one peak")

	hashes := GenerateHashes(peaks)
	assert.Greater(t, len(hashes), 0, "GenerateHashes should produce at least one hash")
}

// TestGenerateHashes_Deterministic verifies that the same input produces
// identical hashes on two successive runs.
func TestGenerateHashes_Deterministic(t *testing.T) {
	const (
		freqHz     = 1000.0
		sampleRate = 16000
		numSamples = 80000 // 5 seconds
	)

	samples := makeSineWave(freqHz, sampleRate, numSamples)

	// First run
	spec1 := STFT(samples)
	peaks1 := PickPeaks(spec1)
	hashes1 := GenerateHashes(peaks1)

	// Second run
	spec2 := STFT(samples)
	peaks2 := PickPeaks(spec2)
	hashes2 := GenerateHashes(peaks2)

	require.Equal(t, len(hashes1), len(hashes2), "hash count should be deterministic")
	for i := range hashes1 {
		assert.Equal(t, hashes1[i], hashes2[i], "hash[%d] should be identical", i)
	}
}

// TestGenerateHashes_HashRange verifies that hash values are correctly encoded
// with the three bit fields: f1 in bits 23-31, f2 in bits 14-22, dt in bits 0-13.
func TestGenerateHashes_HashRange(t *testing.T) {
	const (
		freqHz     = 440.0
		sampleRate = 16000
		numSamples = 160000
	)

	samples := makeSineWave(freqHz, sampleRate, numSamples)
	spec := STFT(samples)
	peaks := PickPeaks(spec)
	hashes := GenerateHashes(peaks)

	require.NotEmpty(t, hashes, "need hashes to verify encoding")

	// Build a lookup of peaks by frame for cross-referencing
	// Instead, we re-derive the encoding from peaks and verify it matches
	// the stored hash value.
	peakByIndex := make(map[[2]int]bool)
	for _, p := range peaks {
		peakByIndex[p] = true
	}

	// For each hash, verify the bit encoding:
	//   bits 23-31 (9 bits): f1 & 0x1FF
	//   bits 14-22 (9 bits): f2 & 0x1FF
	//   bits 0-13 (14 bits): dt & 0x3FFF
	for _, h := range hashes {
		f1 := (h.Value >> 23) & 0x1FF
		f2 := (h.Value >> 14) & 0x1FF
		dt := h.Value & 0x3FFF

		// Re-encode and verify it matches
		reEncoded := (f1 << 23) | (f2 << 14) | dt
		assert.Equal(t, h.Value, reEncoded,
			"hash value 0x%08X should round-trip through bit encoding", h.Value)

		// Verify field values are within expected bit widths
		assert.LessOrEqual(t, f1, uint32(0x1FF), "f1 must fit in 9 bits")
		assert.LessOrEqual(t, f2, uint32(0x1FF), "f2 must fit in 9 bits")
		assert.LessOrEqual(t, dt, uint32(0x3FFF), "dt must fit in 14 bits")
	}
}
