package audio

import "testing"

// BenchmarkSTFT measures the per-call cost of the full STFT over a 4s window
// of audio (64k samples at 16 kHz) — the exact shape used by the live
// matcher inside ingestor.runPCMReader. Run with:
//
//	go test -run=^$ -bench=BenchmarkSTFT -benchmem -benchtime=5s ./pkg/audio/
//
// Before the FFT-pool refactor every call allocated a fresh fourier.FFT
// plan (precomputed twiddle factors) plus a 4096-float64 windowed buffer
// plus a re-derived Hann window. After the refactor only the per-frame
// log-magnitude slices are allocated; FFT plan + scratch buffer are pooled
// and the Hann window is a package-level constant.
func BenchmarkSTFT(b *testing.B) {
	samples := goldenSignal(16000, 64000) // 4s window

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = STFT(samples)
	}
}

// BenchmarkSTFT_Parallel runs STFT concurrently from -cpu workers, modelling
// the actual production load of N ingestor workers analysing windows in
// parallel. Pool contention shows up here if the pool is mis-sized.
func BenchmarkSTFT_Parallel(b *testing.B) {
	samples := goldenSignal(16000, 64000) // 4s window

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = STFT(samples)
		}
	})
}
