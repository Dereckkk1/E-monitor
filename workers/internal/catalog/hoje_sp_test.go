package catalog

import (
	"testing"
	"time"
)

func TestTodaySaoPaulo_DiaDeSaoPauloEmMeiaNoiteUTC(t *testing.T) {
	got := TodaySaoPaulo()
	if got.Location() != time.UTC || got.Hour() != 0 || got.Minute() != 0 {
		t.Fatalf("quer meia-noite UTC, veio %v", got)
	}
	agora := time.Now().In(spLoc)
	if got.Year() != agora.Year() || got.Month() != agora.Month() || got.Day() != agora.Day() {
		t.Fatalf("dia %v ≠ hoje em SP %v", got, agora)
	}
}
