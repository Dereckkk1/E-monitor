package audio

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"sync"
	"testing"
)

// goldenSignal returns a deterministic synthetic signal — three pure tones
// at 500 / 1500 / 3000 Hz with a slow amplitude envelope. Anything that
// changes the math of STFT/PickPeaks/GenerateHashes will change the
// resulting hash digest captured below.
func goldenSignal(sampleRate, numSamples int) []float32 {
	samples := make([]float32, numSamples)
	freqs := []float64{500, 1500, 3000}
	for i := range samples {
		t := float64(i) / float64(sampleRate)
		env := 0.5 + 0.5*math.Sin(2*math.Pi*0.5*t) // 0.5 Hz amplitude envelope
		var v float64
		for _, f := range freqs {
			v += math.Sin(2 * math.Pi * f * t)
		}
		samples[i] = float32(env * v / float64(len(freqs)))
	}
	return samples
}

// hashesDigest serialises the slice of audio.Hash values into bytes and
// returns the hex-encoded SHA-256. Any change to count, ordering, or value
// of generated hashes — i.e. any change to the matching math — flips this
// digest. The test below pins the expected digest so future refactors of
// STFT (e.g. FFT plan reuse, pooled buffers) must preserve the math bit-
// exact.
func hashesDigest(hs []Hash) string {
	h := sha256.New()
	buf := make([]byte, 8)
	for _, hash := range hs {
		binary.LittleEndian.PutUint32(buf[0:4], hash.Value)
		binary.LittleEndian.PutUint32(buf[4:8], uint32(hash.TimeFrame))
		h.Write(buf)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestSTFT_RegressionDigest is a golden test for the full fingerprint
// pipeline. Captured once against the current STFT implementation; subsequent
// optimisations (FFT plan reuse, hann precompute, buffer pooling) must
// preserve the exact hash output. If this test fails, the refactor changed
// the matching math — investigate before merging.
func TestSTFT_RegressionDigest(t *testing.T) {
	const (
		sampleRate = 16000
		numSamples = 80000 // 5s
	)

	samples := goldenSignal(sampleRate, numSamples)

	spec := STFT(samples)
	peaks := PickPeaks(spec)
	hashes := GenerateHashes(peaks)

	if len(hashes) == 0 {
		t.Fatal("golden signal produced zero hashes — input is degenerate")
	}

	digest := hashesDigest(hashes)
	t.Logf("golden digest: %s (n_frames=%d n_peaks=%d n_hashes=%d)",
		digest, len(spec), len(peaks), len(hashes))

	// Captured against the original per-call fourier.NewFFT implementation
	// on 2026-05-19. The FFT-pool refactor (same date) must preserve this.
	// If you legitimately change matching math (window size, hop, bin range,
	// hash encoding) you must regenerate this digest and call it out in the
	// PR — every existing fingerprint in the catalog becomes incomparable.
	const wantDigest = "78df72a7eb65e73cc3692d71fdbb0a91545219b80981bf208f3abf6a1c0d905b"
	if digest != wantDigest {
		t.Fatalf("STFT pipeline digest drift — matching math changed\nwant: %s\ngot:  %s",
			wantDigest, digest)
	}
}

// TestSTFT_ConcurrentSameInput asserts that running STFT from many goroutines
// against the same input produces byte-identical output. This is the
// regression guard for the planned sync.Pool of fourier.FFT plans — if the
// pooled state leaks between callers, this test fails immediately.
func TestSTFT_ConcurrentSameInput(t *testing.T) {
	samples := goldenSignal(16000, 32000) // 2s

	// Baseline computed serially.
	wantSpec := STFT(samples)
	wantPeaks := PickPeaks(wantSpec)
	wantHashes := GenerateHashes(wantPeaks)
	wantDigest := hashesDigest(wantHashes)

	const goroutines = 16
	const iterations = 8

	var wg sync.WaitGroup
	errs := make(chan string, goroutines*iterations)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				spec := STFT(samples)
				peaks := PickPeaks(spec)
				hashes := GenerateHashes(peaks)
				if d := hashesDigest(hashes); d != wantDigest {
					errs <- d
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)

	for d := range errs {
		t.Fatalf("concurrent STFT diverged from baseline\nwant: %s\ngot:  %s", wantDigest, d)
	}
}
