package audio

import (
	"math"
	"sync"

	"gonum.org/v1/gonum/dsp/fourier"
)

// Constants matching the Python generator
const (
	WindowSize = 4096
	HopSize    = 2048
	FreqMinBin = 25
	FreqMaxBin = 1024
)

// hannWin is the Hann window precomputed once at package init. The window is
// read-only after init, so safe to share across goroutines. Saves ~WindowSize
// math.Cos calls per STFT invocation (previously hannWindow() was rebuilt
// inside every call).
var hannWin = func() []float64 {
	w := make([]float64, WindowSize)
	for i := 0; i < WindowSize; i++ {
		w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(WindowSize-1)))
	}
	return w
}()

// stftScratch is the per-call mutable state: a fourier.FFT plan (twiddle
// factors etc.) plus the windowed-input buffer. Both are expensive to
// construct (especially the FFT plan) and were being rebuilt on every STFT
// call before the pool was introduced. Pooling cuts hot-path allocations
// from ~2 per call to ~0 amortised.
type stftScratch struct {
	fft      *fourier.FFT
	windowed []float64
	// coeffs is reused as the destination for fft.Coefficients on every
	// frame. gonum sizes the output at WindowSize/2 + 1 complex128s; passing
	// a pre-sized slice prevents per-frame allocation of ~32 KB.
	coeffs []complex128
}

var stftPool = sync.Pool{
	New: func() any {
		return &stftScratch{
			fft:      fourier.NewFFT(WindowSize),
			windowed: make([]float64, WindowSize),
			coeffs:   make([]complex128, WindowSize/2+1),
		}
	},
}

// STFT computes the Short-Time Fourier Transform on PCM samples.
// Returns a 2D slice [frame][bin] of log-magnitude values.
// Uses a Hann window. Only bins [FreqMinBin, FreqMaxBin) are returned per frame.
// Frames shorter than WindowSize are zero-padded.
//
// Concurrency: safe to call from many goroutines simultaneously. Each call
// borrows a private *fourier.FFT + scratch buffer from a sync.Pool; nothing
// is shared across concurrent invocations.
func STFT(samples []float32) [][]float32 {
	if len(samples) == 0 {
		return nil
	}

	scratch := stftPool.Get().(*stftScratch)
	defer stftPool.Put(scratch)
	windowed := scratch.windowed
	fft := scratch.fft

	numBins := FreqMaxBin - FreqMinBin
	var frames [][]float32

	for offset := 0; offset < len(samples); offset += HopSize {
		// Extract window and zero-pad if needed
		for i := 0; i < WindowSize; i++ {
			idx := offset + i
			if idx < len(samples) {
				windowed[i] = float64(samples[idx]) * hannWin[i]
			} else {
				windowed[i] = 0.0
			}
		}

		// Compute FFT coefficients into the pooled scratch slice — gonum
		// reuses our backing array when dst has the right length.
		coeffs := fft.Coefficients(scratch.coeffs, windowed)

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
