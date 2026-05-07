package fingerprint

import "radiocheck/pkg/audio"

// PickPeaks finds local maxima in the spectrogram. See pkg/audio/peaks.go for
// the full implementation; this wrapper exists so the fingerprint pipeline
// reads top-to-bottom in a single package.
func PickPeaks(spec [][]float32) [][2]int {
	return audio.PickPeaks(spec)
}
