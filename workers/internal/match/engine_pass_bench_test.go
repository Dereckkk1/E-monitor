package match

import (
	"testing"

	"radiocheck/internal/index"
	"radiocheck/pkg/audio"
)

// The two benchmarks below model the worker's per-window cost BEFORE and AFTER
// the single-pass refactor.
//
//	BenchmarkWindowPath_OldTwoPass  — MatchWindow + ScanScores (two buildHistogram passes,
//	                                  the path runPCMReader took on every no-detection window)
//	BenchmarkWindowPath_NewOnePass  — MatchWindowDetailed (one buildHistogram pass)
//
// Run:
//
//	go test -run=^$ -bench=BenchmarkWindowPath -benchmem -benchtime=3s ./internal/match/
//
// The delta between the two is the CPU the live worker stops spending per
// window (×117 workers × 0.5 Hz). Expect NewOnePass ≈ half of OldTwoPass.
func benchSetup(b *testing.B) ([]float32, *index.Store) {
	b.Helper()
	const sampleRate = 16000
	const numSamples = 64000 // 4s window — exactly what runPCMReader feeds
	samples := makeSineWave(880.0, sampleRate, numSamples)
	filtered := audio.ApplyHighPass(samples, 100.0, sampleRate)
	normalized := audio.NormalizeRMS(filtered, -20.0)
	refHashes := audio.GenerateHashes(audio.PickPeaks(audio.STFT(normalized)))
	store := buildIndexFromHashes(refHashes, 7)
	return samples, store
}

func BenchmarkWindowPath_OldTwoPass(b *testing.B) {
	samples, store := benchSetup(b)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		results := MatchWindow(samples, store, 5, 0.05)
		if len(results) == 0 {
			_ = ScanScores(samples, store)
		} else {
			// noise-sample path also re-scanned on the worker's 1-in-5 windows;
			// the common case (no detection) is the empty branch above.
			_ = ScanScores(samples, store)
		}
	}
}

func BenchmarkWindowPath_NewOnePass(b *testing.B) {
	samples, store := benchSetup(b)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = MatchWindowDetailed(samples, store, 5, 0.05)
	}
}
