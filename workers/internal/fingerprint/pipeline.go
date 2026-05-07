package fingerprint

import (
	"context"
	"fmt"
	"math"
	"time"
)

// Result captures the outcome of running the fingerprint pipeline on a single
// (master, variant) pair.
type Result struct {
	Variant       VariantID
	Hashes        []Hash
	DurationSec   float64
	Frames        int
	Peaks         int
	HashesPerSec  float64
	Elapsed       time.Duration
	UniqueHashes  int     // number of distinct Hash.Value entries
	HashEntropyEst float64 // rough entropy estimate (Shannon, bits) over the hash distribution
}

// Validation describes simple post-generation sanity checks (§7.5).
// We do not fail generation on these — we surface the warnings so the operator
// (or a future automated job) can flag the master for review.
type Validation struct {
	HashesPerSecondOK bool
	EntropyOK         bool
	Warnings          []string
}

// Validate runs the lightweight checks from §7.5: minimum density and
// minimum hash diversity. See docs/fingerprint-pipeline.md for the
// thresholds and what they mean.
//
// TODO §7.5: full validation also re-runs match() against the index and a
// noise corpus. That requires the matching engine and a noise dataset; out
// of scope for this PoC.
func Validate(r Result) Validation {
	v := Validation{HashesPerSecondOK: true, EntropyOK: true}

	const minHashesPerSec = 30.0
	if r.HashesPerSec < minHashesPerSec {
		v.HashesPerSecondOK = false
		v.Warnings = append(v.Warnings,
			fmt.Sprintf("hashes/s = %.1f below minimum %.0f (§7.5)", r.HashesPerSec, minHashesPerSec))
	}

	const minEntropyBits = 8.0
	if r.HashEntropyEst < minEntropyBits {
		v.EntropyOK = false
		v.Warnings = append(v.Warnings,
			fmt.Sprintf("hash entropy = %.2f bits below %.0f — distribution looks degenerate", r.HashEntropyEst, minEntropyBits))
	}
	return v
}

// GenerateForVariant runs decode → STFT → peaks → hashes for a single variant
// and returns the Result. It does NOT touch the database; callers (CLI,
// background workers) decide when and how to persist.
//
// Only VariantClean is implemented today; other variants return
// ErrVariantNotImplemented so callers can iterate without crashing the batch.
func GenerateForVariant(ctx context.Context, inputPath string, variant VariantID) (Result, error) {
	start := time.Now()

	pcm, err := DecodePCM(ctx, inputPath, variant)
	if err != nil {
		return Result{}, err
	}
	durationSec := float64(len(pcm)) / float64(SampleRate)

	spec := STFT(pcm)
	peaks := PickPeaks(spec)
	hashes := GenerateHashes(peaks)

	r := Result{
		Variant:     variant,
		Hashes:      hashes,
		DurationSec: durationSec,
		Frames:      len(spec),
		Peaks:       len(peaks),
		Elapsed:     time.Since(start),
	}
	if durationSec > 0 {
		r.HashesPerSec = float64(len(hashes)) / durationSec
	}
	r.UniqueHashes, r.HashEntropyEst = entropyStats(hashes)
	return r, nil
}

// entropyStats counts unique hash values and computes a Shannon-entropy
// approximation (in bits) over the empirical distribution of Hash.Value.
// A perfectly uniform distribution over N hashes gives log2(N) bits.
func entropyStats(hashes []Hash) (unique int, entropy float64) {
	if len(hashes) == 0 {
		return 0, 0
	}
	counts := make(map[uint32]int, len(hashes))
	for _, h := range hashes {
		counts[h.Value]++
	}
	unique = len(counts)
	total := float64(len(hashes))
	for _, c := range counts {
		p := float64(c) / total
		entropy -= p * math.Log2(p)
	}
	return unique, entropy
}
