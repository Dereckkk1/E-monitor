package match

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestIsPossibleReair cobre o detector de "re-veiculação engolida pelo
// cooldown" (T8-A do plano de remediação 2026-06-12). Matches em cooldown são
// normais para a CAUDA da veiculação recém-confirmada (offsets crescentes);
// o sinal de uma NOVA veiculação é offset no início do comercial chegando na
// metade final do cooldown.
func TestIsPossibleReair(t *testing.T) {
	const totalFrames = 234 // ~30s de comercial
	cooldown := 35 * time.Second
	base := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	sm := NewStateMachine("station-x", 42, totalFrames, 5, 0.15, 30*time.Second, cooldown, zap.NewNop())
	sm.state = StateCooldown
	sm.cooldownUntil = base.Add(cooldown)

	early := base.Add(5 * time.Second)   // primeira metade do cooldown
	late := base.Add(30 * time.Second)   // metade final do cooldown
	startOffset := totalFrames / 10      // offset no início do comercial
	tailOffset := totalFrames * 3 / 4    // offset na cauda (continuação normal)

	cases := []struct {
		name   string
		result MatchResult
		now    time.Time
		want   bool
	}{
		{"cauda da veiculação atual (offset alto, cedo)", MatchResult{UniqueScore: 10, OffsetFrames: tailOffset}, early, false},
		{"cauda tardia (offset alto, tarde)", MatchResult{UniqueScore: 10, OffsetFrames: tailOffset}, late, false},
		{"início cedo demais (provável eco da mesma)", MatchResult{UniqueScore: 10, OffsetFrames: startOffset}, early, false},
		{"REINÍCIO na metade final = possível re-veiculação", MatchResult{UniqueScore: 10, OffsetFrames: startOffset}, late, true},
		{"score fraco não conta", MatchResult{UniqueScore: 2, OffsetFrames: startOffset}, late, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sm.isPossibleReair(c.result, c.now); got != c.want {
				t.Errorf("isPossibleReair(offset=%d, now=+%s) = %v, want %v",
					c.result.OffsetFrames, c.now.Sub(base), got, c.want)
			}
		})
	}
}
