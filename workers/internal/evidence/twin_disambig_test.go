package evidence

import "testing"

func TestChooseTwinByDiscriminative(t *testing.T) {
	const floor, margin = 0.15, 1.5
	cases := []struct {
		name               string
		discSelf, discTwin float64
		want               twinVerdict
	}{
		{"twin clearly higher -> reattribute", 0.05, 0.80, verdictReattribute},
		{"self clearly higher -> keep", 0.80, 0.05, verdictKeep},
		{"both below floor -> ambiguous", 0.05, 0.08, verdictAmbiguous},
		{"near-tie above floor -> ambiguous", 0.60, 0.55, verdictAmbiguous},
		{"empty disc (frames=0 -> 0/0) -> ambiguous", 0, 0, verdictAmbiguous},
		{"exact tie above floor -> ambiguous", 0.50, 0.50, verdictAmbiguous},
		{"twin wins by exactly margin -> reattribute", 0.20, 0.30, verdictReattribute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chooseTwinByDiscriminative(c.discSelf, c.discTwin, floor, margin)
			if got != c.want {
				t.Fatalf("chooseTwinByDiscriminative(%v,%v) = %v, want %v", c.discSelf, c.discTwin, got, c.want)
			}
		})
	}
}
