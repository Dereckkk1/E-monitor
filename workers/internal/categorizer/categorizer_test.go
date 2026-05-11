package categorizer

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// América/São Paulo é onde a campanha "vive" — fixo no plano.
var saoPaulo, _ = time.LoadLocation("America/Sao_Paulo")

func mkRule(startDay, endDay int, mask int16, ts, te string, plays int16) Rule {
	parseTime := func(hhmm string) time.Time {
		t, _ := time.Parse("15:04", hhmm)
		return t
	}
	return Rule{
		StartDate:   time.Date(2026, 6, startDay, 0, 0, 0, 0, saoPaulo),
		EndDate:     time.Date(2026, 6, endDay, 0, 0, 0, 0, saoPaulo),
		WeekdayMask: mask,
		TimeStart:   parseTime(ts),
		TimeEnd:     parseTime(te),
		PlaysPerDay: plays,
	}
}

func TestCategorize_OutDate(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// Detection em 1º de julho — fora da campanha
	got := Categorize(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC), cmp, nil)
	if got != "out_date" {
		t.Errorf("got %q, want out_date", got)
	}
}

func TestCategorize_Orphan_NoRules(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	got := Categorize(time.Date(2026, 6, 15, 10, 0, 0, 0, saoPaulo), cmp, nil)
	if got != "orphan" {
		t.Errorf("got %q, want orphan", got)
	}
}

func TestCategorize_InSlot(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// 10/06/2026 é quarta-feira (DOW=3 → bit 3 → 8)
	// Mask 62 (0111110) = seg-sex
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	// Detection às 09:00 BRT (UTC-3 → 12:00 UTC) numa quarta
	det := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	got := Categorize(det, cmp, rules)
	if got != "in_slot" {
		t.Errorf("got %q, want in_slot", got)
	}
}

func TestCategorize_OutSlot(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	// Detection às 14:00 BRT (17:00 UTC) — fora da faixa
	det := time.Date(2026, 6, 10, 17, 0, 0, 0, time.UTC)
	got := Categorize(det, cmp, rules)
	if got != "out_slot" {
		t.Errorf("got %q, want out_slot", got)
	}
}

func TestCategorize_Orphan_WrongWeekday(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// Mask 62 = seg-sex. Detection num sábado (06/06/2026, DOW=6 → bit 6 = 64)
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	det := time.Date(2026, 6, 6, 12, 0, 0, 0, saoPaulo) // sáb 12:00 BRT
	got := Categorize(det, cmp, rules)
	if got != "orphan" {
		t.Errorf("got %q, want orphan (sábado fora da regra)", got)
	}
}

func TestCategorize_InSlot_BoundaryInclusive(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	// Exatamente 08:00 BRT (11:00 UTC) — deve ser in_slot (boundary inclusive)
	det := time.Date(2026, 6, 10, 11, 0, 0, 0, time.UTC)
	got := Categorize(det, cmp, rules)
	if got != "in_slot" {
		t.Errorf("got %q, want in_slot (08:00 é início da faixa)", got)
	}
	// 10:00:00 BRT — também in_slot (boundary inclusive)
	det2 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	got2 := Categorize(det2, cmp, rules)
	if got2 != "in_slot" {
		t.Errorf("got %q, want in_slot (10:00 é fim da faixa)", got2)
	}
}

var _ = uuid.UUID{} // suppress unused import if categorizer doesn't import uuid
