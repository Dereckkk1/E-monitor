package audio

import (
	"math"

	"gonum.org/v1/gonum/dsp/fourier"
)

// Constants matching the Python generator
const (
	WindowSize = 4096
	HopSize    = 2048
	FreqMinBin = 25
	FreqMaxBin = 1024
)

// hannWindow returns a precomputed Hann window of length n.
func hannWindow(n int) []float64 {
	w := make([]float64, n)
	for i := 0; i < n; i++ {
		w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n-1)))
	}
	return w
}

// STFT computes the Short-Time Fourier Transform on PCM samples.
// Returns a 2D slice [frame][bin] of log-magnitude values.
// Uses a Hann window. Only bins [FreqMinBin, FreqMaxBin) are returned per frame.
// Frames shorter than WindowSize are zero-padded.
func STFT(samples []float32) [][]float32 {
	if len(samples) == 0 {
		return nil
	}

	hann := hannWindow(WindowSize)
	fft := fourier.NewFFT(WindowSize)
	windowed := make([]float64, WindowSize)

	numBins := FreqMaxBin - FreqMinBin
	var frames [][]float32

	for offset := 0; offset < len(samples); offset += HopSize {
		// Extract window and zero-pad if needed
		for i := 0; i < WindowSize; i++ {
			idx := offset + i
			if idx < len(samples) {
				windowed[i] = float64(samples[idx]) * hann[i]
			} else {
				windowed[i] = 0.0
			}
		}

		// Compute FFT coefficients
		coeffs := fft.Coefficients(nil, windowed)

		// Compute log-magnitude for bins [FreqMinBin, FreqMaxBin)
		frame := make([]float32, numBins)
		for b := FreqMinBin; b < FreqMaxBin; b++ {
			c := coeffs[b]
			mag := math.Sqrt(real(c)*real(c) + imag(c)*imag(c))
			frame[b-FreqMinBin] = float32(math.Log1p(mag))
		}
		frames = append(frames, frame)
	}

	return frames
}
