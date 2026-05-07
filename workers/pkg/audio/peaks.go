package audio

import (
	"sort"
)

// Peak finder constants.
// neighborFrames/neighborBins are the half-sizes (radius) of the neighbourhood window.
// Python uses maximum_filter with footprint shape (17,17), so radius = (17-1)/2 = 8.
const (
	neighborFrames          = 8
	neighborBins            = 8
	PeakAmplitudePercentile = 80 // reject peaks below the 80th percentile of magnitudes
)

// percentile computes the pth percentile of a float32 slice (0-100).
func percentile(values []float32, p float64) float32 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float32, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx := int(float64(len(sorted)-1) * p / 100.0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// PickPeaks finds local maxima in the spectrogram using a sliding maximum filter.
// Returns a list of (frame, bin) pairs that are local maxima.
// A cell (f, b) is a peak if its value equals the max in a neighborhood of
// ±neighborFrames, ±neighborBins AND exceeds the PeakAmplitudePercentile threshold.
func PickPeaks(spec [][]float32) [][2]int {
	if len(spec) == 0 {
		return nil
	}

	numFrames := len(spec)
	numBins := len(spec[0])

	// Compute the amplitude percentile threshold
	var allValues []float32
	for f := 0; f < numFrames; f++ {
		for b := 0; b < numBins; b++ {
			if spec[f][b] > 0 {
				allValues = append(allValues, spec[f][b])
			}
		}
	}
	threshold := percentile(allValues, float64(PeakAmplitudePercentile))

	var peaks [][2]int

	for f := 0; f < numFrames; f++ {
		for b := 0; b < numBins; b++ {
			val := spec[f][b]

			// Skip silence and values below threshold
			if val == 0 || val <= threshold {
				continue
			}

			// Compute neighborhood bounds (clamped to valid indices)
			fMin := f - neighborFrames
			if fMin < 0 {
				fMin = 0
			}
			fMax := f + neighborFrames
			if fMax >= numFrames {
				fMax = numFrames - 1
			}
			bMin := b - neighborBins
			if bMin < 0 {
				bMin = 0
			}
			bMax := b + neighborBins
			if bMax >= numBins {
				bMax = numBins - 1
			}

			// Find the local maximum in the neighborhood
			localMax := float32(0)
			for ff := fMin; ff <= fMax; ff++ {
				for bb := bMin; bb <= bMax; bb++ {
					if spec[ff][bb] > localMax {
						localMax = spec[ff][bb]
					}
				}
			}

			// A cell is a peak if its value equals the neighborhood maximum
			if val >= localMax {
				peaks = append(peaks, [2]int{f, b})
			}
		}
	}

	return peaks
}
