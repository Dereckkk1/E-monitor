package campaignalerts

import (
	"testing"
	"time"

	"radiocheck/internal/calendar"
)

func at(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.ParseInLocation("2006-01-02 15:04", s, calendar.BR)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm
}

func TestShouldRun(t *testing.T) {
	sendHour := 8
	cases := []struct {
		name string
		now  string
		want bool
	}{
		{"sexta 08:30 roda", "2026-06-12 08:30", true},
		{"sexta 07:59 antes da hora", "2026-06-12 07:59", false},
		{"sabado nunca", "2026-06-13 10:00", false},
		{"domingo nunca", "2026-06-14 10:00", false},
		{"segunda 09:00 roda", "2026-06-08 09:00", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldRun(at(t, c.now), sendHour); got != c.want {
				t.Errorf("shouldRun(%s) = %v, want %v", c.now, got, c.want)
			}
		})
	}
}
