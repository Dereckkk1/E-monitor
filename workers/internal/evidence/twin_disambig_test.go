package evidence

import (
	"testing"

	"github.com/google/uuid"
)

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

func TestPickTwinAction(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()

	t.Run("nenhum eval -> keep", func(t *testing.T) {
		act, w := pickTwinAction(nil)
		if act != verdictKeep || w != nil {
			t.Fatalf("got %v/%v, want keep/nil", act, w)
		}
	})

	t.Run("todos keep -> keep", func(t *testing.T) {
		act, w := pickTwinAction([]twinEval{
			{twinID: a, verdict: verdictKeep},
			{twinID: b, verdict: verdictKeep},
		})
		if act != verdictKeep || w != nil {
			t.Fatalf("got %v/%v, want keep/nil", act, w)
		}
	})

	t.Run("um ambiguous, nenhum reattribute -> ambiguous", func(t *testing.T) {
		act, w := pickTwinAction([]twinEval{
			{twinID: a, verdict: verdictKeep},
			{twinID: b, verdict: verdictAmbiguous},
		})
		if act != verdictAmbiguous || w != nil {
			t.Fatalf("got %v/%v, want ambiguous/nil", act, w)
		}
	})

	t.Run("um reattribute vence sobre ambiguous", func(t *testing.T) {
		act, w := pickTwinAction([]twinEval{
			{twinID: a, verdict: verdictAmbiguous},
			{twinID: b, discCov: 0.7, verdict: verdictReattribute},
		})
		if act != verdictReattribute || w == nil || w.twinID != b {
			t.Fatalf("got %v/%v, want reattribute/b", act, w)
		}
	})

	t.Run("dois reattribute -> maior discCov", func(t *testing.T) {
		act, w := pickTwinAction([]twinEval{
			{twinID: a, discCov: 0.55, verdict: verdictReattribute},
			{twinID: b, discCov: 0.90, verdict: verdictReattribute},
			{twinID: c, discCov: 0.20, verdict: verdictKeep},
		})
		if act != verdictReattribute || w == nil || w.twinID != b {
			t.Fatalf("got %v/%v, want reattribute/b (maior discCov)", act, w)
		}
	})
}
