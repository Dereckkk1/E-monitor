package fingerprint

import "radiocheck/pkg/audio"

// Re-exports of constants and helpers from pkg/audio so callers (the CLI,
// future API endpoints, tests) don't need to import both packages just to
// reach STFT. The actual implementation lives in pkg/audio/stft.go.
const (
	WindowSize = audio.WindowSize
	HopSize    = audio.HopSize
	FreqMinBin = audio.FreqMinBin
	FreqMaxBin = audio.FreqMaxBin
)

// STFT computes the Short-Time Fourier Transform on PCM samples and returns
// the log-magnitude spectrogram trimmed to [FreqMinBin, FreqMaxBin).
// Thin alias around audio.STFT to keep the fingerprint package self-contained
// at the call sites.
func STFT(samples []float32) [][]float32 {
	return audio.STFT(samples)
}
