package fingerprint

import "testing"

// TestPickPeaks_FindsSineTone verifies that a synthetic sine wave produces
// at least a handful of peaks (one anchor per frame, roughly).
func TestPickPeaks_FindsSineTone(t *testing.T) {
	pcm := makeSineWave(1500.0, SampleRate, 5*SampleRate) // 5 s tone
	spec := STFT(pcm)
	peaks := PickPeaks(spec)

	if len(peaks) == 0 {
		t.Fatal("PickPeaks returned no peaks for a clean sine — STFT/threshold may be misconfigured")
	}

	// All peaks should be on or near the same frequency bin (the sine bin).
	// We assert that >70% of the peaks land in the bin with the most hits.
	binCounts := make(map[int]int)
	for _, p := range peaks {
		binCounts[p[1]]++
	}
	mostHits := 0
	for _, c := range binCounts {
		if c > mostHits {
			mostHits = c
		}
	}
	ratio := float64(mostHits) / float64(len(peaks))
	if ratio < 0.7 {
		t.Errorf("sine peaks scattered: dominant bin holds %.0f%% of peaks, want ≥70%%", ratio*100)
	}
}

// TestPickPeaks_EmptySpectrogram ensures we don't panic on empty input.
func TestPickPeaks_EmptySpectrogram(t *testing.T) {
	if peaks := PickPeaks(nil); peaks != nil {
		t.Errorf("PickPeaks(nil) = %v, want nil", peaks)
	}
}
