package catalog

import (
	"testing"
	"time"
)

func TestCrossesAny_NoOverlap(t *testing.T) {
	day := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	rule := TimeWindow{Start: "08:00", End: "09:00"}
	downs := []TimeRange{
		{From: day.Add(14 * time.Hour), Duration: 30 * time.Minute},
	}
	if crossesAny(day, rule, downs) {
		t.Errorf("morning rule should not cross afternoon down")
	}
}

func TestCrossesAny_FullOverlap(t *testing.T) {
	day := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	rule := TimeWindow{Start: "08:00", End: "10:00"}
	downs := []TimeRange{
		{From: day.Add(8*time.Hour + 30*time.Minute), Duration: 15 * time.Minute},
	}
	if !crossesAny(day, rule, downs) {
		t.Errorf("down inside rule window should cross")
	}
}

func TestCrossesAny_EdgeTouching(t *testing.T) {
	day := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	rule := TimeWindow{Start: "08:00", End: "09:00"}
	// Down ends exactly when rule starts — not a real overlap.
	downs := []TimeRange{
		{From: day.Add(7 * time.Hour), Duration: 60 * time.Minute},
	}
	if crossesAny(day, rule, downs) {
		t.Errorf("touching endpoint should not count as cross")
	}
}

func TestCrossesAny_EmptyDowns(t *testing.T) {
	day := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	rule := TimeWindow{Start: "08:00", End: "09:00"}
	if crossesAny(day, rule, nil) {
		t.Errorf("empty downs should never cross")
	}
}

func TestStationFailures_ListForDate_NoFailures(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewStationFailures(pool)

	farPast := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	res, err := repo.ListForDate(ctx, farPast, 60)
	if err != nil {
		t.Fatalf("ListForDate: %v", err)
	}
	if len(res.Stations) != 0 {
		t.Errorf("expected 0 stations, got %d", len(res.Stations))
	}
	if res.Summary.StationsWithFailure != 0 {
		t.Errorf("expected 0 stations_with_failure, got %d", res.Summary.StationsWithFailure)
	}
}
