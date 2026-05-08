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
// changing it has to read the docs/worker-commercial-reconciler.md note about
// the trade-off (DB load vs. detection-loss window).
func TestReconcileInterval_Constant(t *testing.T) {
	if reconcileInterval.Seconds() != 30 {
		t.Fatalf("reconcileInterval = %v, want 30s — see docs/worker-commercial-reconciler.md", reconcileInterval)
	}
}
