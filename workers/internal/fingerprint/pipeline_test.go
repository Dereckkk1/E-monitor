package fingerprint

import (
	"math"
	"testing"
)

// TestEntropyStats checks the basic shape of entropyStats: a perfectly
// uniform distribution over N hashes gives log2(N) bits, a single-value
// distribution gives 0 bits.
func TestEntropyStats(t *testing.T) {
	// Single value → 0 entropy.
	one := []Hash{{Value: 1, TimeFrame: 0}, {Value: 1, TimeFrame: 1}, {Value: 1, TimeFrame: 2}}
	u, e := entropyStats(one)
	if u != 1 {
		t.Errorf("unique=%d, want 1", u)
	}
	if e != 0 {
		t.Errorf("entropy=%v, want 0", e)
	}

	// Four uniformly distributed values → 2 bits.
	four := []Hash{
		{Value: 1}, {Value: 2}, {Value: 3}, {Value: 4},
		{Value: 1}, {Value: 2}, {Value: 3}, {Value: 4},
	}
	u, e = entropyStats(four)
	if u != 4 {
		t.Errorf("unique=%d, want 4", u)
	}
	if math.Abs(e-2.0) > 1e-9 {
		t.Errorf("entropy=%v, want 2.0", e)
	}

	// Empty input.
	u, e = entropyStats(nil)
	if u != 0 || e != 0 {
		t.Errorf("entropyStats(nil) = (%d,%v), want (0,0)", u, e)
	}
}

// TestValidate_LowDensityWarns ensures Validate flags a low hashes/s rate.
func TestValidate_LowDensityWarns(t *testing.T) {
	r := Result{
		HashesPerSec:   5.0, // way below the 30/s minimum
		HashEntropyEst: 12.0,
	}
	v := Validate(r)
	if v.HashesPerSecondOK {
		t.Error("expected HashesPerSecondOK to be false")
	}
	if !v.EntropyOK {
		t.Error("expected EntropyOK to be true at 12 bits")
	}
	if len(v.Warnings) == 0 {
		t.Error("expected at least one warning")
	}
}

// TestValidate_LowEntropyWarns ensures Validate flags a degenerate
// hash distribution.
func TestValidate_LowEntropyWarns(t *testing.T) {
	r := Result{
		HashesPerSec:   100.0,
		HashEntropyEst: 2.0,
	}
	v := Validate(r)
	if v.EntropyOK {
		t.Error("expected EntropyOK to be false at 2 bits")
	}
}
