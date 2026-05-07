package fingerprint

import "testing"

// TestGenerateHashes_BitLayout sanity-checks the (f1, f2, dt) packing on a
// hand-crafted peak list. This is the same encoding tested in pkg/audio,
// but we exercise it through the fingerprint-package wrapper to guarantee
// the alias keeps the contract.
func TestGenerateHashes_BitLayout(t *testing.T) {
	peaks := [][2]int{
		{0, 100}, // frame=0, bin=100
		{2, 110}, // dt=2, bin=110 (within target zone)
		{5, 90},  // dt=5, bin=90
	}
	hashes := GenerateHashes(peaks)
	if len(hashes) == 0 {
		t.Fatal("expected hashes from valid peak pairs")
	}
	for _, h := range hashes {
		f1 := (h.Value >> 23) & 0x1FF
		f2 := (h.Value >> 14) & 0x1FF
		dt := h.Value & 0x3FFF
		// Round-trip the encoding to make sure no other bits are set.
		if h.Value != ((f1 << 23) | (f2 << 14) | dt) {
			t.Errorf("hash 0x%08x has stray bits", h.Value)
		}
	}
}

// TestGenerateHashes_EmptyPeaks ensures we tolerate empty input.
func TestGenerateHashes_EmptyPeaks(t *testing.T) {
	if h := GenerateHashes(nil); h != nil {
		t.Errorf("GenerateHashes(nil) = %v, want nil", h)
	}
}
