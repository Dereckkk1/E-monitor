package fingerprint

import (
	"math"
	"testing"
)

// makeSineWave generates a sine wave at freqHz, sampleRate, for n samples.
func makeSineWave(freqHz float64, sampleRate, n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(math.Sin(2 * math.Pi * freqHz * float64(i) / float64(sampleRate)))
	}
	return out
}

// TestSTFT_PeakOnExpectedBin verifies that a clean sine wave concentrates
// energy in the bin closest to its frequency. The bin width is
// SampleRate / WindowSize ≈ 3.9 Hz at the configured constants. A 1 kHz tone
// should land at bin ≈ 256 (1000 / 3.9), which after the FreqMinBin offset
// becomes 256 - FreqMinBin in the trimmed spectrogram.
func TestSTFT_PeakOnExpectedBin(t *testing.T) {
	const (
		freqHz = 1000.0
		nSec   = 2
	)
	pcm := makeSineWave(freqHz, SampleRate, nSec*SampleRate)
	spec := STFT(pcm)
	if len(spec) == 0 {
		t.Fatal("STFT returned empty spectrogram")
	}

	// Bin width.
	binWidth := float64(SampleRate) / float64(WindowSize)
	expectedBin := int(math.Round(freqHz/binWidth)) - FreqMinBin
	if expectedBin < 0 || expectedBin >= len(spec[0]) {
		t.Fatalf("expectedBin %d outside trimmed range [0,%d)", expectedBin, len(spec[0]))
	}

	// Use a middle frame to skip windowing transients.
	frame := spec[len(spec)/2]
	maxBin := 0
	for b := range frame {
		if frame[b] > frame[maxBin] {
			maxBin = b
		}
	}

	// Allow ±2 bins (~8 Hz) of tolerance for windowing leakage.
	if abs(maxBin-expectedBin) > 2 {
		t.Errorf("STFT peak at bin %d, expected %d (±2)", maxBin, expectedBin)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
