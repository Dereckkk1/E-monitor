package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestComputeSummary_NoEvents(t *testing.T) {
	id := uuid.New()
	got := computeSummary(id, nil, 7, float64(7*24*60*60))
	if got.UptimePct != 100.0 {
		t.Errorf("want 100%% uptime, got %.2f", got.UptimePct)
	}
	if got.IncidentCount != 0 {
		t.Errorf("want 0 incidents, got %d", got.IncidentCount)
	}
	if len(got.DailySummary) != 7 {
		t.Errorf("want 7 daily entries, got %d", len(got.DailySummary))
	}
}

func TestComputeSummary_WithDowntime(t *testing.T) {
	id := uuid.New()
	dur := 3600 // 1 hour down
	events := []HealthEvent{
		{EventType: "down", EventAt: time.Now().Add(-2 * time.Hour), DurationSeconds: &dur},
		{EventType: "up", EventAt: time.Now().Add(-1 * time.Hour)},
	}
	periodSec := float64(7 * 24 * 60 * 60)
	got := computeSummary(id, events, 7, periodSec)

	want := (1 - float64(dur)/periodSec) * 100
	if diff := got.UptimePct - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("want uptime %.4f%%, got %.4f%%", want, got.UptimePct)
	}
	if got.IncidentCount != 1 {
		t.Errorf("want 1 incident, got %d", got.IncidentCount)
	}
	if got.LastIncidentAt == nil {
		t.Error("want non-nil LastIncidentAt")
	}
}

func TestBuildDailySummary_AllUp(t *testing.T) {
	daily := buildDailySummary(nil, 3)
	if len(daily) != 3 {
		t.Fatalf("want 3 entries, got %d", len(daily))
	}
	for _, d := range daily {
		if d.UptimePct != 100.0 {
			t.Errorf("want 100%% uptime for %s, got %.2f", d.Date, d.UptimePct)
		}
	}
}

func TestBuildDailySummary_WithDowntime(t *testing.T) {
	dur := 43200 // 12 hours down on today
	today := time.Now().UTC().Format("2006-01-02")
	events := []HealthEvent{
		{EventType: "down", EventAt: time.Now().Add(-13 * time.Hour), DurationSeconds: &dur},
	}
	daily := buildDailySummary(events, 3)

	var todayEntry *DailySummary
	for i := range daily {
		if daily[i].Date == today {
			todayEntry = &daily[i]
		}
	}
	if todayEntry == nil {
		t.Fatal("today not in daily summary")
	}
	want := (1 - float64(dur)/86400.0) * 100
	if diff := todayEntry.UptimePct - want; diff > 0.1 || diff < -0.1 {
		t.Errorf("want today uptime %.2f%%, got %.2f%%", want, todayEntry.UptimePct)
	}
}
