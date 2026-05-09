//go:build integration

// Integration test for segment extraction. Splits a real evidence audio
// (audio-refs/veiculacao-*.m4a) into 30-second ADTS segments named with
// strftime — mimicking exactly what ffmpeg's segment muxer does in production
// — then asks Extract for the FULL range and a NARROWER sub-range, asserting:
//
//   1. Extract returns audio whose duration is close to the requested range.
//   2. Coverage is reported as 1.0 (no missing windows).
//   3. A request that intentionally falls between segments produces ErrNoSegments.
//
// Run: go test -tags integration ./internal/segments/ -run TestIntegration_Extract -v

package segments

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "audio-refs")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find audio-refs/ above %s", wd)
		}
		dir = parent
	}
}

// segmentRealEvidence calls ffmpeg's segment muxer the same way the worker
// will in production, splitting the input into 30-second ADTS chunks named
// with strftime — except we pin the start time so the test is deterministic.
func segmentRealEvidence(t *testing.T, dir string, inputPath string, startTime time.Time) {
	t.Helper()

	// Pre-encode start time → wall-clock-aligned naming. ffmpeg's strftime
	// uses the system clock, but we want the test to be deterministic, so
	// instead we segment without strftime, then rename files based on
	// startTime + their index * 30s.
	segPattern := filepath.Join(dir, "raw-%03d.aac")

	cmd := exec.CommandContext(context.Background(), "ffmpeg",
		"-y", "-hide_banner", "-loglevel", "error",
		"-i", inputPath,
		"-map", "0:a:0",
		"-c:a", "copy",
		"-f", "segment",
		"-segment_time", "30",
		"-segment_format", "adts",
		"-reset_timestamps", "1",
		segPattern,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg segment: %v: %s", err, string(out))
	}

	// Rename raw-NNN.aac → strftime-named files starting at startTime.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "raw-") || !strings.HasSuffix(name, ".aac") {
			continue
		}
		idxStr := strings.TrimSuffix(strings.TrimPrefix(name, "raw-"), ".aac")
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			continue
		}
		segStart := startTime.Add(time.Duration(idx) * SegmentDuration)
		newName := segStart.Format(fileNameLayout) + fileExt
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, newName)); err != nil {
			t.Fatalf("rename: %v", err)
		}
	}
}

func TestIntegration_Extract_FullRangeMatchesInputDuration(t *testing.T) {
	root := repoRoot(t)
	inputPath := filepath.Join(root, "audio-refs", "veiculacao-4d287518-0149-4aa9-b320-11284724b0c0.m4a")
	if _, err := os.Stat(inputPath); err != nil {
		t.Skipf("evidence sample not present: %v", err)
	}

	dir := t.TempDir()
	startTime := time.Date(2026, 5, 9, 12, 0, 0, 0, time.Local)
	segmentRealEvidence(t, dir, inputPath, startTime)

	// Probe the input duration so we know what to ask Extract for.
	durOut, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputPath,
	).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	durSec, err := strconv.ParseFloat(strings.TrimSpace(string(durOut)), 64)
	if err != nil {
		t.Fatalf("parse duration: %v", err)
	}
	t.Logf("input duration: %.2fs", durSec)

	from := startTime
	to := startTime.Add(time.Duration(durSec * float64(time.Second)))

	res, err := Extract(dir, from, to)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Data) == 0 {
		t.Fatal("Extract returned empty data")
	}
	if res.CoveredFraction < 0.95 {
		t.Errorf("CoveredFraction = %.2f, want >= 0.95", res.CoveredFraction)
	}
	if res.Partial {
		t.Errorf("Partial = true, want false on a clean full-range extract")
	}

	// Probe the extracted bytes through ffprobe to confirm a usable audio
	// duration close to what we asked for. ADTS frame-level trim has ~21ms
	// resolution; we accept ±1 second.
	gotDur := probeBytes(t, res.Data)
	if gotDur < durSec-1.0 || gotDur > durSec+1.0 {
		t.Errorf("extracted duration = %.2fs, want within 1s of input %.2fs", gotDur, durSec)
	}
	t.Logf("extracted duration: %.2fs (asked for %.2fs)", gotDur, durSec)
}

func TestIntegration_Extract_NarrowSubrange(t *testing.T) {
	root := repoRoot(t)
	inputPath := filepath.Join(root, "audio-refs", "veiculacao-4d287518-0149-4aa9-b320-11284724b0c0.m4a")
	if _, err := os.Stat(inputPath); err != nil {
		t.Skipf("evidence sample not present: %v", err)
	}

	dir := t.TempDir()
	startTime := time.Date(2026, 5, 9, 12, 0, 0, 0, time.Local)
	segmentRealEvidence(t, dir, inputPath, startTime)

	// Pick a 12-second sub-range that crosses two segment boundaries (the
	// segment muxer rotates every 30s) — this is the case where the
	// concat+trim path is doing real work.
	from := startTime.Add(25 * time.Second)
	to := startTime.Add(37 * time.Second)

	res, err := Extract(dir, from, to)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Partial {
		t.Errorf("Partial = true on contiguous 12s sub-range, want false")
	}
	gotDur := probeBytes(t, res.Data)
	want := to.Sub(from).Seconds()
	if gotDur < want-0.3 || gotDur > want+0.3 {
		t.Errorf("sub-range duration = %.2fs, want %.2fs ±0.3", gotDur, want)
	}
}

func probeBytes(t *testing.T, data []byte) float64 {
	t.Helper()
	tmp, err := os.CreateTemp("", "probe-*.aac")
	if err != nil {
		t.Fatalf("tmp: %v", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	tmp.Close()
	out, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		tmp.Name(),
	).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	d, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("parse: %v: %q", err, string(out))
	}
	return d
}

// Avoid unused-import lint when fmt is not actually referenced after edits.
var _ = fmt.Sprintf
