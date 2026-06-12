package match

import (
	"fmt"
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

// BenchmarkWindowPath_NewOnePass_ScalableIndex tests the matcher with varying
// numbers of commercials in the index (1, 10, 50, 117) to measure how lookup/histogram
// scale with index size.
//
// Run:
//
//	go test -run=^$ -bench='BenchmarkWindowPath_NewOnePass_ScalableIndex' -benchmem -benchtime=3s ./internal/match/
//
// Expected behavior: should scale roughly linearly or sub-linearly with the number
// of commercials, since the lookup (map) and histogram operations are O(#hashes),
// not O(#commercials).
func BenchmarkWindowPath_NewOnePass_ScalableIndex(b *testing.B) {
	commercialCounts := []int{1, 10, 50, 117}

	for _, numCommericals := range commercialCounts {
		b.Run(fmt.Sprintf("%dCommericals", numCommericals), func(b *testing.B) {
			const sampleRate = 16000
			const numSamples = 64000 // 4s window
			samples := makeSineWave(880.0, sampleRate, numSamples)
			filtered := audio.ApplyHighPass(samples, 100.0, sampleRate)
			normalized := audio.NormalizeRMS(filtered, -20.0)
			refHashes := audio.GenerateHashes(audio.PickPeaks(audio.STFT(normalized)))

			// Build index with numCommericals copies of the same hashes (with different shortIDs)
			idx := make(index.Index)
			for commercialID := int32(0); commercialID < int32(numCommericals); commercialID++ {
				for _, h := range refHashes {
					idx[h.Value] = append(idx[h.Value], index.Entry{
						CommercialShortID: commercialID,
						TimeFrame:         int32(h.TimeFrame),
					})
				}
			}
			store := index.New()
			store.Swap(idx)

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = MatchWindowDetailed(samples, store, 5, 0.05)
			}
		})
	}
}
