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

func mkOverride(plays int16, ts, te string) *Override {
	parseTime := func(hhmm string) time.Time {
		t, _ := time.Parse("15:04", hhmm)
		return t
	}
	return &Override{
		PlaysExpected: plays,
		TimeStart:     parseTime(ts),
		TimeEnd:       parseTime(te),
	}
}

func TestCategorize_OutDate(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// Detection em 1º de julho — fora da campanha
	got := Categorize(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC), cmp, nil, nil)
	if got != "out_date" {
		t.Errorf("got %q, want out_date", got)
	}
}

func TestCategorize_Orphan_NoRules(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	got := Categorize(time.Date(2026, 6, 15, 10, 0, 0, 0, saoPaulo), cmp, nil, nil)
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
	got := Categorize(det, cmp, rules, nil)
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
	// Detection às 14:00 BRT (17:00 UTC) — fora da faixa (>>15min de folga)
	det := time.Date(2026, 6, 10, 17, 0, 0, 0, time.UTC)
	got := Categorize(det, cmp, rules, nil)
	if got != "out_slot" {
		t.Errorf("got %q, want out_slot", got)
	}
}

func TestCategorize_InSlot_ToleranceBefore(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 62, "06:00", "19:00", 3)}
	// 05:45 BRT — exatamente no limite da tolerância → in_slot
	det := time.Date(2026, 6, 10, 5, 45, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, rules, nil); got != "in_slot" {
		t.Errorf("05:45 com rule 06:00-19:00: got %q, want in_slot", got)
	}
	// 05:44 BRT — 16 min antes, fora da tolerância → out_slot
	det = time.Date(2026, 6, 10, 5, 44, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, rules, nil); got != "out_slot" {
		t.Errorf("05:44 com rule 06:00-19:00: got %q, want out_slot", got)
	}
}

func TestCategorize_InSlot_ToleranceAfter(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 62, "06:00", "19:00", 3)}
	// 19:15 BRT — exatamente no limite → in_slot
	det := time.Date(2026, 6, 10, 19, 15, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, rules, nil); got != "in_slot" {
		t.Errorf("19:15 com rule 06:00-19:00: got %q, want in_slot", got)
	}
	// 19:16 BRT — 16 min depois → out_slot
	det = time.Date(2026, 6, 10, 19, 16, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, rules, nil); got != "out_slot" {
		t.Errorf("19:16 com rule 06:00-19:00: got %q, want out_slot", got)
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
	got := Categorize(det, cmp, rules, nil)
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
	got := Categorize(det, cmp, rules, nil)
	if got != "in_slot" {
		t.Errorf("got %q, want in_slot (08:00 é início da faixa)", got)
	}
	// 10:00:00 BRT — também in_slot (boundary inclusive)
	det2 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	got2 := Categorize(det2, cmp, rules, nil)
	if got2 != "in_slot" {
		t.Errorf("got %q, want in_slot (10:00 é fim da faixa)", got2)
	}
}

// ─── Override tests (migration 0031) ──────────────────────────────────────

func TestCategorize_Override_InSlot(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	ov := mkOverride(2, "14:00", "16:00")
	det := time.Date(2026, 6, 10, 14, 30, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, nil, ov); got != "in_slot" {
		t.Errorf("got %q, want in_slot (detection na faixa do override)", got)
	}
}

func TestCategorize_Override_OutSlot_OutsideWindow(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	ov := mkOverride(2, "14:00", "16:00")
	// 09:00 — fora da faixa do override (>>15min folga)
	det := time.Date(2026, 6, 10, 9, 0, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, nil, ov); got != "out_slot" {
		t.Errorf("got %q, want out_slot", got)
	}
}

func TestCategorize_Override_IgnoresRules(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// Rule 08:00-10:00 cobriria a detection às 09:00, mas o override
	// (14:00-16:00) deve mandar — rule é IGNORADA.
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	ov := mkOverride(2, "14:00", "16:00")
	det := time.Date(2026, 6, 10, 9, 0, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, rules, ov); got != "out_slot" {
		t.Errorf("got %q, want out_slot (override deve sobrepor rule)", got)
	}
}

func TestCategorize_Override_CountZero_AlwaysOutSlot(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// count=0 + faixa "14:00-16:00" — faixa é inerte. Qualquer detection
	// no dia → out_slot, mesmo dentro da faixa.
	ov := mkOverride(0, "14:00", "16:00")
	det := time.Date(2026, 6, 10, 15, 0, 0, 0, saoPaulo) // dentro da faixa
	if got := Categorize(det, cmp, nil, ov); got != "out_slot" {
		t.Errorf("count=0 dentro da faixa: got %q, want out_slot", got)
	}
}

func TestCategorize_Override_ToleranceBoundary(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	ov := mkOverride(1, "10:00", "12:00")
	// 09:45 — exatamente 15min antes → in_slot
	det := time.Date(2026, 6, 10, 9, 45, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, nil, ov); got != "in_slot" {
		t.Errorf("09:45 override 10:00-12:00: got %q, want in_slot", got)
	}
	// 09:44 — 16min antes → out_slot
	det = time.Date(2026, 6, 10, 9, 44, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, nil, ov); got != "out_slot" {
		t.Errorf("09:44 override 10:00-12:00: got %q, want out_slot", got)
	}
}

func TestCategorize_Override_OutOfDate_StillOutDate(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	ov := mkOverride(1, "10:00", "12:00")
	// Detection em 1º de julho — fora da campanha. out_date manda sobre override.
	det := time.Date(2026, 7, 1, 11, 0, 0, 0, saoPaulo)
	if got := Categorize(det, cmp, nil, ov); got != "out_date" {
		t.Errorf("got %q, want out_date (range da campanha tem prioridade)", got)
	}
}

var _ = uuid.UUID{} // suppress unused import if categorizer doesn't import uuid
