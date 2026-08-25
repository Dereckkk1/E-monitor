package assertiveness

import (
	"testing"
	"time"

	"radiocheck/internal/calendar"
)

func at(y int, m time.Month, d, h int) time.Time {
	return time.Date(y, m, d, h, 0, 0, 0, calendar.BR)
}

func day(t time.Time) string { return t.In(calendar.BR).Format("2006-01-02") }

func TestClosedMonth(t *testing.T) {
	cases := []struct {
		name           string
		now            time.Time
		wantFrom, want string
	}{
		{"meio do mes", at(2026, time.August, 7, 10), "2026-07-01", "2026-07-31"},
		// Dia 1º ainda aponta pro mês anterior — se apontasse pro corrente, o
		// card mostraria um mês com zero manuais digitadas.
		{"primeiro dia do mes", at(2026, time.August, 1, 0), "2026-07-01", "2026-07-31"},
		{"ultimo dia do mes", at(2026, time.August, 31, 23), "2026-07-01", "2026-07-31"},
		{"virada de ano", at(2026, time.January, 5, 12), "2025-12-01", "2025-12-31"},
		{"mes anterior curto", at(2026, time.March, 10, 12), "2026-02-01", "2026-02-28"},
		{"fevereiro bissexto", at(2028, time.March, 10, 12), "2028-02-01", "2028-02-29"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			from, to := ClosedMonth(c.now)
			if day(from) != c.wantFrom || day(to) != c.want {
				t.Errorf("ClosedMonth(%s) = %s..%s, quero %s..%s",
					day(c.now), day(from), day(to), c.wantFrom, c.want)
			}
		})
	}
}

// A janela do job tem que cobrir INTEIROS o mês fechado e o anterior a ele —
// são os dois que o card lê (valor + tendência). Uma janela de "últimos N dias"
// quebraria na virada: em 31/08, 45 dias atrás é 17/07 e julho ficaria pela
// metade.
func TestWindowCobreOsDoisMesesQueOCardLe(t *testing.T) {
	for _, now := range []time.Time{
		at(2026, time.August, 1, 0),
		at(2026, time.August, 15, 12),
		at(2026, time.August, 31, 23),
		at(2026, time.January, 31, 23),
		at(2026, time.March, 1, 0),
	} {
		from, to := Window(now)
		curFrom, curTo := ClosedMonth(now)
		prevFrom := curFrom.AddDate(0, -1, 0)

		if from.After(prevFrom) {
			t.Errorf("now=%s: janela comeca em %s, depois do mes de comparacao (%s)",
				day(now), day(from), day(prevFrom))
		}
		if to.Before(curTo) {
			t.Errorf("now=%s: janela termina em %s, antes do fim do mes fechado (%s)",
				day(now), day(to), day(curTo))
		}
	}
}
