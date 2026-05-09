package segments

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDirFor_FlatLayout(t *testing.T) {
	st := uuid.New()
	got := DirFor("/data/segments", st)
	want := filepath.Join("/data/segments", st.String())
	if got != want {
		t.Fatalf("DirFor = %q, want %q", got, want)
	}
}

func TestFFmpegOutputPattern_StrftimeFriendly(t *testing.T) {
	st := uuid.New()
	got := FFmpegOutputPattern("/data/segments", st)
	want := filepath.Join("/data/segments", st.String(), "%Y%m%d-%H%M%S.aac")
	if got != want {
		t.Fatalf("FFmpegOutputPattern = %q, want %q", got, want)
	}
}

// TestListInRange_PicksOverlappingSegments seeds a temp dir with three
// segment-named files and asserts only the overlapping ones come back, in
// chronological order. The search includes a one-segment widening on the left
// so a file that started just before `from` is still picked up.
func TestListInRange_PicksOverlappingSegments(t *testing.T) {
	dir := t.TempDir()

	mk := func(start time.Time) string {
		path := filepath.Join(dir, start.Format(fileNameLayout)+fileExt)
		writeEmpty(t, path)
		return path
	}

	base := time.Date(2026, 5, 9, 12, 30, 0, 0, time.Local)
	mk(base)                                  // 12:30:00 (covers 12:30:00..12:30:30)
	mk(base.Add(30 * time.Second))            // 12:30:30
	mk(base.Add(60 * time.Second))            // 12:31:00
	mk(base.Add(120 * time.Second))           // 12:32:00 (well after window)
	writeEmpty(t, filepath.Join(dir, "stray.txt"))
	writeEmpty(t, filepath.Join(dir, "20260509-bad.aac")) // unparseable

	from := base.Add(15 * time.Second) // 12:30:15
	to := base.Add(75 * time.Second)   // 12:31:15

	got, err := listInRange(dir, from, to)
	if err != nil {
		t.Fatalf("listInRange: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d files, want 3 (12:30:00, 12:30:30, 12:31:00); got=%v", len(got), got)
	}

	// Chronological order check.
	prev, _ := parseFileStart(got[0])
	for _, p := range got[1:] {
		t2, _ := parseFileStart(p)
		if !t2.After(prev) {
			t.Fatalf("not chronological: %s before %s", p, prev)
		}
		prev = t2
	}
}

// TestCoverageStats_DetectsGap seeds two segments with a missing window
// between them and asserts coverage drops to ~2/3 with Partial=true.
func TestCoverageStats_DetectsGap(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 5, 9, 12, 0, 0, 0, time.Local)

	a := filepath.Join(dir, base.Format(fileNameLayout)+fileExt)                          // 12:00:00..12:00:30
	c := filepath.Join(dir, base.Add(60*time.Second).Format(fileNameLayout)+fileExt)      // 12:01:00..12:01:30
	writeEmpty(t, a)
	writeEmpty(t, c)
	// Skipped on purpose: 12:00:30..12:01:00 — the gap.

	from := base                          // 12:00:00
	to := base.Add(90 * time.Second)      // 12:01:30

	files := []string{a, c}
	frac, partial := coverageStats(files, from, to)
	if !partial {
		t.Fatalf("partial = false, want true (gap of 30s in 90s window)")
	}
	if frac < 0.55 || frac > 0.7 {
		t.Fatalf("CoveredFraction = %.2f, want roughly 0.66 (60s of 90s)", frac)
	}
}

// TestExtract_NoSegments returns ErrNoSegments cleanly.
func TestExtract_NoSegments(t *testing.T) {
	dir := t.TempDir()
	from := time.Now().Add(-time.Hour)
	to := from.Add(60 * time.Second)
	_, err := Extract(dir, from, to)
	if err != ErrNoSegments {
		t.Fatalf("err = %v, want ErrNoSegments", err)
	}
}

// TestExtract_RejectsInvertedRange validates the from < to invariant.
func TestExtract_RejectsInvertedRange(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	if _, err := Extract(dir, now, now.Add(-time.Second)); err == nil {
		t.Fatalf("expected error for inverted range, got nil")
	}
	if _, err := Extract(dir, now, now); err == nil {
		t.Fatalf("expected error for zero-length range, got nil")
	}
}

func writeEmpty(t *testing.T, path string) {
	t.Helper()
	if err := touch(path); err != nil {
		t.Fatalf("touch %s: %v", path, err)
	}
}
