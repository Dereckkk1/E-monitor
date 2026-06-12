package calendar

import (
	"testing"
	"time"
)

// d builds a midnight America/Sao_Paulo date.
func d(t *testing.T, s string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatalf("load loc: %v", err)
	}
	tm, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm
}

func TestInAlertWindow(t *testing.T) {
	// 2026-06-08 is a Monday. Calendar anchor:
	// Mon 06-08, Tue 06-09, Wed 06-10, Thu 06-11, Fri 06-12, Sat 06-13, Sun 06-14, Mon 06-15.
	cases := []struct {
		name          string
		today, target string
		want          bool
	}{
		{"cenario1 sexta->segunda", "2026-06-12", "2026-06-15", true},   // cal 3 (não<3), úteis 1 (≤2)
		{"cenario1 sabado window sab->seg", "2026-06-13", "2026-06-15", true}, // cal 2 <3 — janela ok; o skip é no scheduler
		{"cenario2 quinta->sabado", "2026-06-11", "2026-06-13", true},   // cal 2 <3
		{"cenario3 segunda->terca", "2026-06-08", "2026-06-09", true},   // cal 1 <3
		{"ponte sexta->terca (2 uteis)", "2026-06-12", "2026-06-16", true}, // cal 4, úteis 2 (≤2)
		{"sexta->quarta (3 uteis) fora", "2026-06-12", "2026-06-17", false}, // cal 5, úteis 3 (>2)
		{"alvo hoje", "2026-06-09", "2026-06-09", true},                 // cal 0 <3
		{"alvo passado fora", "2026-06-10", "2026-06-09", false},        // target<today
		{"longe fora", "2026-06-08", "2026-06-30", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := InAlertWindow(d(t, c.today), d(t, c.target))
			if got != c.want {
				t.Errorf("InAlertWindow(%s,%s) = %v, want %v", c.today, c.target, got, c.want)
			}
		})
	}
}

func TestIsBusinessDay(t *testing.T) {
	if IsBusinessDay(d(t, "2026-06-13")) { // sábado
		t.Error("sábado deveria ser não-útil")
	}
	if IsBusinessDay(d(t, "2026-06-14")) { // domingo
		t.Error("domingo deveria ser não-útil")
	}
	if !IsBusinessDay(d(t, "2026-06-12")) { // sexta
		t.Error("sexta deveria ser útil")
	}
}

func TestPreviousBusinessDay(t *testing.T) {
	cases := []struct{ today, want string }{
		{"2026-06-09", "2026-06-08"}, // ter -> seg
		{"2026-06-08", "2026-06-05"}, // seg -> sexta anterior (pula fds)
		{"2026-06-12", "2026-06-11"}, // sex -> qui
	}
	for _, c := range cases {
		if got := PreviousBusinessDay(d(t, c.today)); got.Format("2006-01-02") != c.want {
			t.Errorf("PreviousBusinessDay(%s) = %s, want %s", c.today, got.Format("2006-01-02"), c.want)
		}
	}
}

func TestBRMidnight(t *testing.T) {
	// O civil date 2026-06-12 vira o instante 2026-06-12T00:00 em
	// America/Sao_Paulo (= 03:00 UTC).
	got := BRMidnight(d(t, "2026-06-12"))
	if got.Hour() != 0 || got.Location() != BR {
		t.Errorf("BRMidnight deveria ser meia-noite em BR, got %v", got)
	}
	if utc := got.UTC(); utc.Hour() != 3 {
		t.Errorf("meia-noite BRT = 03:00 UTC, got %v", utc)
	}
}

func TestBusinessDaysUntil(t *testing.T) {
	if got := BusinessDaysUntil(d(t, "2026-06-12"), d(t, "2026-06-15")); got != 1 { // sex->seg
		t.Errorf("sex->seg = %d, want 1", got)
	}
	if got := BusinessDaysUntil(d(t, "2026-06-12"), d(t, "2026-06-16")); got != 2 { // sex->ter
		t.Errorf("sex->ter = %d, want 2", got)
	}
	if got := BusinessDaysUntil(d(t, "2026-06-10"), d(t, "2026-06-09")); got != 0 { // alvo passado
		t.Errorf("passado = %d, want 0", got)
	}
}
