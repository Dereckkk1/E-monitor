package campaignalerts

import (
	"testing"
	"time"

	"radiocheck/internal/calendar"
)

func ts(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.ParseInLocation("2006-01-02 15:04", s, calendar.BR)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm
}

// TestMergeIntervals cobre o merge de downs sobrepostos/aninhados — o
// histórico real da Jovem Pan (incidente 2026-06-12) tinha eventos down que
// se sobrepunham; somar cru contaria horas em dobro.
func TestMergeIntervals(t *testing.T) {
	in := []interval{
		{ts(t, "2026-06-11 10:00"), ts(t, "2026-06-11 12:00")}, // 2h
		{ts(t, "2026-06-11 11:00"), ts(t, "2026-06-11 13:00")}, // sobrepõe -> estende até 13h
		{ts(t, "2026-06-11 15:00"), ts(t, "2026-06-11 15:30")}, // separado, 30min
		{ts(t, "2026-06-11 15:10"), ts(t, "2026-06-11 15:20")}, // aninhado, não soma
	}
	got := mergeIntervals(in)
	if len(got) != 2 {
		t.Fatalf("esperava 2 intervalos após merge, got %d: %v", len(got), got)
	}
	total := time.Duration(0)
	for _, iv := range got {
		total += iv.end.Sub(iv.start)
	}
	if want := 3*time.Hour + 30*time.Minute; total != want {
		t.Errorf("downtime total = %s, want %s", total, want)
	}
}

// TestSplitByCivilDay: um down que atravessa a meia-noite conta nas duas datas.
func TestSplitByCivilDay(t *testing.T) {
	iv := interval{ts(t, "2026-06-13 22:00"), ts(t, "2026-06-14 03:00")} // sáb 22h -> dom 3h
	parts := splitByCivilDay(iv)
	if len(parts) != 2 {
		t.Fatalf("esperava 2 partes, got %d", len(parts))
	}
	if d := parts[0].iv.end.Sub(parts[0].iv.start); d != 2*time.Hour {
		t.Errorf("parte do sábado = %s, want 2h", d)
	}
	if d := parts[1].iv.end.Sub(parts[1].iv.start); d != 3*time.Hour {
		t.Errorf("parte do domingo = %s, want 3h", d)
	}
	if parts[0].day.Day() != 13 || parts[1].day.Day() != 14 {
		t.Errorf("dias errados: %v / %v", parts[0].day, parts[1].day)
	}
}
