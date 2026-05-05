package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestApplyHighPass_DCRemoval verifies that a constant DC signal converges
// toward 0 after high-pass filtering. A 100 Hz cutoff with a 16000 Hz sample
// rate should drive the DC component to near-zero within one second of audio.
func TestApplyHighPass_DCRemoval(t *testing.T) {
	const (
		sampleRate = 16000
		cutoffHz   = 100.0
		numSamples = sampleRate // 1 second
	)

	// Constant DC signal (all 1.0).
	input := make([]float32, numSamples)
	for i := range input {
		input[i] = 1.0
	}

	out := ApplyHighPass(input, cutoffHz, sampleRate)

	// The last few samples should be very close to zero.
	for _, v := range out[numSamples-10:] {
		assert.Less(t, math.Abs(float64(v)), 0.01,
			"expected DC signal to be removed; got %v near end of output", v)
	}
}

// TestApplyHighPass_PreservesLength verifies that the output slice has the
// same length as the input.
func TestApplyHighPass_PreservesLength(t *testing.T) {
	input := make([]float32, 512)
	for i := range input {
		input[i] = float32(i)
	}

	out := ApplyHighPass(input, 100.0, 16000)
	assert.Equal(t, len(input), len(out))
}

// TestApplyHighPass_EmptyInput verifies that an empty input returns an empty
// (non-nil) slice without panicking.
func TestApplyHighPass_EmptyInput(t *testing.T) {
	out := ApplyHighPass([]float32{}, 100.0, 16000)
	assert.NotNil(t, out)
	assert.Equal(t, 0, len(out))
}

// TestNormalizeRMS_TargetLevel generates a non-trivial signal, normalizes it
// to -20 dB, and checks that the resulting RMS is within 1% of the target.
func TestNormalizeRMS_TargetLevel(t *testing.T) {
	const (
		numSamples = 4096
		targetDB   = -20.0
	)

	// Simple sine wave as test signal.
	input := make([]float32, numSamples)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * float64(i) / 100.0))
	}

	out := NormalizeRMS(input, targetDB)

	// Compute resulting RMS.
	var sumSq float64
	for _, s := range out {
		sumSq += float64(s) * float64(s)
	}
	rms := math.Sqrt(sumSq / float64(len(out)))

	target := math.Pow(10, targetDB/20)
	assert.InDelta(t, target, rms, target*0.01, // within 1%
		"RMS after normalization should be close to target")
}

// TestNormalizeRMS_SilenceUnchanged verifies that an all-zero input does not
// panic and returns an all-zero output.
func TestNormalizeRMS_SilenceUnchanged(t *testing.T) {
	input := make([]float32, 256)
	out := NormalizeRMS(input, -20.0)

	assert.Equal(t, len(input), len(out))
	for _, v := range out {
		assert.Equal(t, float32(0), v, "silence should remain silence")
	}
}

// TestNormalizeRMS_PreservesLength verifies that the output slice has the
// same length as the input.
func TestNormalizeRMS_PreservesLength(t *testing.T) {
	input := make([]float32, 1024)
	for i := range input {
		input[i] = float32(i) * 0.001
	}

	out := NormalizeRMS(input, -20.0)
	assert.Equal(t, len(input), len(out))
}
