package supervisor

import (
	"sort"
	"testing"

	"github.com/google/uuid"
)

// TestDiffStations covers the helper that drives Supervisor.UpdateStations.
// We need a partition of:
//   - removed: in old, not in new  → workers may need to be stopped.
//   - kept:    in both             → workers stay; will be restarted to refresh
//     commercial list (the campaign's commercials
//     may have changed too).
//   - added:   in new, not in old  → fresh worker.
//
// Order is irrelevant in the inputs (DB-sourced UUID arrays) but the partitioned
// outputs are sorted to keep tests deterministic.
func TestDiffStations(t *testing.T) {
	a := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	b := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	c := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	d := uuid.MustParse("44444444-4444-4444-4444-444444444444")

	cases := []struct {
		name                             string
		oldS, newS                       []uuid.UUID
		wantRemoved, wantKept, wantAdded []uuid.UUID
	}{
		{
			name:        "all removed",
			oldS:        []uuid.UUID{a, b},
			newS:        []uuid.UUID{},
			wantRemoved: []uuid.UUID{a, b},
		},
		{
			name:      "all added (was empty)",
			oldS:      nil,
			newS:      []uuid.UUID{a, b},
			wantAdded: []uuid.UUID{a, b},
		},
		{
			name:     "no change",
			oldS:     []uuid.UUID{a, b},
			newS:     []uuid.UUID{b, a}, // same set, different order
			wantKept: []uuid.UUID{a, b},
		},
		{
			name:        "swap one",
			oldS:        []uuid.UUID{a, b, c},
			newS:        []uuid.UUID{a, b, d},
			wantRemoved: []uuid.UUID{c},
			wantKept:    []uuid.UUID{a, b},
			wantAdded:   []uuid.UUID{d},
		},
		{
			name:        "all disjoint",
			oldS:        []uuid.UUID{a, b},
			newS:        []uuid.UUID{c, d},
			wantRemoved: []uuid.UUID{a, b},
			wantAdded:   []uuid.UUID{c, d},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			removed, kept, added := diffStations(tc.oldS, tc.newS)
			sortUUIDs(removed)
			sortUUIDs(kept)
			sortUUIDs(added)
			sortUUIDs(tc.wantRemoved)
			sortUUIDs(tc.wantKept)
			sortUUIDs(tc.wantAdded)
			if !uuidSlicesEqual(removed, tc.wantRemoved) {
				t.Errorf("removed = %v, want %v", removed, tc.wantRemoved)
			}
			if !uuidSlicesEqual(kept, tc.wantKept) {
				t.Errorf("kept = %v, want %v", kept, tc.wantKept)
			}
			if !uuidSlicesEqual(added, tc.wantAdded) {
				t.Errorf("added = %v, want %v", added, tc.wantAdded)
			}
		})
	}
}

func sortUUIDs(s []uuid.UUID) {
	sort.Slice(s, func(i, j int) bool { return s[i].String() < s[j].String() })
}

func uuidSlicesEqual(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
