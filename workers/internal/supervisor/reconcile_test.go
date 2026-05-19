package supervisor

import (
	"testing"
)

// TestCommercialSetEqual covers the comparator the reconciler uses to decide
// whether the worker's loaded commercial list still matches the DB. Order is
// irrelevant — both lists are produced from independent SQL queries that have
// no ORDER BY guarantee.
func TestCommercialSetEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b []int32
		want bool
	}{
		{"both nil", nil, nil, true},
		{"both empty", []int32{}, []int32{}, true},
		{"nil vs empty", nil, []int32{}, true},
		{"same singleton", []int32{8}, []int32{8}, true},
		{"same pair, different order", []int32{8, 9}, []int32{9, 8}, true},
		{"superset", []int32{8, 9}, []int32{8, 9, 10}, false},
		{"subset", []int32{8, 9, 10}, []int32{8, 9}, false},
		{"disjoint", []int32{8, 9}, []int32{10, 11}, false},
		{"empty vs non-empty", []int32{}, []int32{8}, false},
		{"duplicates collapse", []int32{8, 8, 9}, []int32{8, 9}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := commercialSetEqual(c.a, c.b); got != c.want {
				t.Fatalf("commercialSetEqual(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// TestReconcileInterval_Constant pins the documented cadence so anybody
// changing it has to read the docs/operations/worker-commercial-reconciler.md
// note about the trade-off (DB load vs. detection-loss window).
func TestReconcileInterval_Constant(t *testing.T) {
	if reconcileInterval.Seconds() != 30 {
		t.Fatalf("reconcileInterval = %v, want 30s — see docs/operations/worker-commercial-reconciler.md", reconcileInterval)
	}
}

// TestReconcileReason covers the pure decision function that the worker
// reconciler uses to decide whether (and why) to restart a worker. The
// reason string is also what gets logged when a restart fires, so it must
// remain operator-readable. Empty string means "no restart needed."
func TestReconcileReason(t *testing.T) {
	const urlA = "https://a.example.com/stream"
	const urlB = "https://b.example.com/stream"
	cases := []struct {
		name        string
		currentURL  string
		wantedURL   string
		currentIDs  []int32
		wantedIDs   []int32
		wantRestart bool
	}{
		{
			name:       "nothing changed",
			currentURL: urlA, wantedURL: urlA,
			currentIDs: []int32{8, 9}, wantedIDs: []int32{9, 8},
			wantRestart: false,
		},
		{
			name:       "commercials changed only",
			currentURL: urlA, wantedURL: urlA,
			currentIDs: []int32{8, 9}, wantedIDs: []int32{8, 9, 10},
			wantRestart: true,
		},
		{
			name:       "stream_url changed only",
			currentURL: urlA, wantedURL: urlB,
			currentIDs: []int32{8, 9}, wantedIDs: []int32{8, 9},
			wantRestart: true,
		},
		{
			name:       "both changed",
			currentURL: urlA, wantedURL: urlB,
			currentIDs: []int32{8}, wantedIDs: []int32{8, 9},
			wantRestart: true,
		},
		{
			name:       "wanted url empty, current populated -> NOT a restart",
			currentURL: urlA, wantedURL: "",
			currentIDs: []int32{8}, wantedIDs: []int32{8},
			wantRestart: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := reconcileReason(c.currentURL, c.wantedURL, c.currentIDs, c.wantedIDs)
			gotRestart := got != ""
			if gotRestart != c.wantRestart {
				t.Fatalf("reconcileReason(%q,%q,%v,%v) = %q (restart=%v), want restart=%v",
					c.currentURL, c.wantedURL, c.currentIDs, c.wantedIDs, got, gotRestart, c.wantRestart)
			}
		})
	}
}
