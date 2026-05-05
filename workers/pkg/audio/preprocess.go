package audio

import "math"

// ApplyHighPass applies a 1st-order IIR high-pass filter at cutoffHz.
// The formula matches the Python implementation:
//
//	alpha = RC / (RC + dt)   where RC = 1/(2*pi*cutoffHz), dt = 1/sampleRate
//
// Returns a new slice; input is not modified.
func ApplyHighPass(samples []float32, cutoffHz float64, sampleRate int) []float32 {
	if len(samples) == 0 {
		return []float32{}
	}

	RC := 1.0 / (2 * math.Pi * cutoffHz)
	dt := 1.0 / float64(sampleRate)
	alpha := RC / (RC + dt)

	out := make([]float32, len(samples))
	out[0] = samples[0]
	for i := 1; i < len(samples); i++ {
		out[i] = float32(alpha) * (out[i-1] + samples[i] - samples[i-1])
	}
	return out
}

// NormalizeRMS scales samples so their RMS equals 10^(targetDB/20).
// If the input RMS is 0 (silence), returns the input unchanged.
// Returns a new slice; input is not modified.
func NormalizeRMS(samples []float32, targetDB float64) []float32 {
	if len(samples) == 0 {
		return []float32{}
	}

	var sumSq float64
	for _, s := range samples {
		sumSq += float64(s) * float64(s)
	}
	rms := math.Sqrt(sumSq / float64(len(samples)))

	if rms == 0 {
		out := make([]float32, len(samples))
		copy(out, samples)
		return out
	}

	target := math.Pow(10, targetDB/20)
	scale := target / rms

	out := make([]float32, len(samples))
	for i, s := range samples {
		out[i] = s * float32(scale)
	}
	return out
}
