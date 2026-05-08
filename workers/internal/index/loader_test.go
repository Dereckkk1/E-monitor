package index

import (
	"strings"
	"testing"
)

// TestIndexEligibleStatuses pins the set of campaign statuses whose
// commercials are loaded into the in-memory matching index. The choice has
// non-obvious load-bearing implications and was tightened post-2026-05-08
// (the previous 'ativa'-only filter caused a race between fingerprint
// completion and the lifecycle scheduler's programada→ativa transition).
//
// If you ever need to change this — go read docs/worker-commercial-reconciler.md
// first, then update the constant and this test together.
func TestIndexEligibleStatuses(t *testing.T) {
	want := []string{"'programada'", "'ativa'"}
	for _, s := range want {
		if !strings.Contains(indexEligibleStatuses, s) {
			t.Errorf("indexEligibleStatuses missing %s — read docs/worker-commercial-reconciler.md before changing", s)
		}
	}
	// Terminal statuses MUST NOT be eligible — they would just bloat the
	// index for commercials no worker will ever match against.
	for _, s := range []string{"'concluida'", "'cancelada'"} {
		if strings.Contains(indexEligibleStatuses, s) {
			t.Errorf("indexEligibleStatuses must NOT include terminal status %s", s)
		}
	}
}
