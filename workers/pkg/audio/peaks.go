package audio

// Peak finder constants matching Python's maximum_filter with size=(21, 11)
const (
	neighborFrames = 10 // ±10 frames  → window of 21
	neighborBins   = 5  // ±5 bins     → window of 11
)

// PickPeaks finds local maxima in the spectrogram using a sliding maximum filter.
// Returns a list of (frame, bin) pairs that are local maxima.
// A cell (f, b) is a peak if its value equals the max in a neighborhood of
// ±neighborFrames, ±neighborBins (matching Python's maximum_filter size=(21,11)).
func PickPeaks(spec [][]float32) [][2]int {
	if len(spec) == 0 {
		return nil
	}

	numFrames := len(spec)
	numBins := len(spec[0])

	var peaks [][2]int

	for f := 0; f < numFrames; f++ {
		for b := 0; b < numBins; b++ {
			val := spec[f][b]

			// Skip silence
			if val == 0 {
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
