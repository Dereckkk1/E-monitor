// Package segments owns the on-disk evidence audio store: ffmpeg writes
// continuous ADTS-AAC segments to a per-station directory, and this package
// knows how to (a) name those directories, (b) discover segment files that
// cover a requested wall-clock range, and (c) concatenate + trim them into
// the exact slice of audio the evidence pipeline needs.
//
// Why disk and not an in-memory ring?
//
// The previous design (ByteRing keyed on time.Now()) lost audio whenever the
// upstream ffmpeg process reconnected, the worker restarted, or wall-clock
// drift accumulated between PCM matching and AAC arrival timestamps. Disk
// segments anchor the timeline to ffmpeg's stream-time PTS (via -strftime
// applied at file rotation), survive process restarts, and let an operator
// `ls` or `ffplay` to inspect what was on the air at any moment in the last
// retention window. See docs/evidence-segments.md.
package segments

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SegmentDuration is the wall-clock length of a single segment file. ffmpeg
// is configured to rotate at this cadence, aligned to wall-clock multiples
// (-segment_atclocktime 1) so segment boundaries are predictable.
const SegmentDuration = 30 * time.Second

// fileExt is the on-disk extension for ADTS-AAC segments.
const fileExt = ".aac"

// fileNameLayout is the strftime pattern ffmpeg uses for segment filenames.
// We choose a layout that is naturally sortable when listed lexicographically
// — chronological order matches sort.Strings.
const fileNameLayout = "20060102-150405"

// DirFor returns the per-station directory where ffmpeg writes segments.
// Callers must MkdirAll before launching ffmpeg.
func DirFor(root string, stationID uuid.UUID) string {
	return filepath.Join(root, stationID.String())
}

// FFmpegOutputPattern returns the absolute path with the strftime pattern
// ffmpeg expects for `-strftime 1` — i.e. with %Y%m%d-%H%M%S not yet
// expanded. It is the single source of truth for the on-disk layout.
func FFmpegOutputPattern(root string, stationID uuid.UUID) string {
	return filepath.Join(DirFor(root, stationID), "%Y%m%d-%H%M%S"+fileExt)
}

// ExtractResult carries the bytes plus a partial-coverage flag so the
// evidence service can mark detections whose underlying segments have gaps.
type ExtractResult struct {
	// Data is the concatenated, trimmed ADTS-AAC audio for the requested
	// [from, to] range. Empty when no covering segments existed.
	Data []byte
	// Partial is true when one or more SegmentDuration windows inside
	// [from, to] had no segment file on disk. The Data still represents
	// the audio that *was* available; the operator/UI should surface the
	// gap so the detection isn't silently mis-labelled as fully captured.
	Partial bool
	// CoveredFraction is the fraction of (to - from) that is actually
	// represented in Data, in [0, 1]. 1.0 means the entire requested range
	// was on disk; 0.0 means nothing was available.
	CoveredFraction float64
}

// ErrNoSegments is returned by Extract when not a single segment file
// overlapping [from, to] exists.
var ErrNoSegments = errors.New("segments: no files cover requested range")

// Extract assembles the ADTS-AAC audio for [from, to] from the segment files
// in dir. It lists files whose filename-encoded start time intersects the
// requested range, concatenates them as raw bytes (legal for ADTS — frames
// are self-delimiting), and trims to the exact range with ffmpeg.
//
// Returns ErrNoSegments if no segment files exist in the requested range.
// On gap-only-partial coverage, Result.Partial is true and CoveredFraction
// reflects how much of the requested duration is in Data.
func Extract(dir string, from, to time.Time) (ExtractResult, error) {
	if !to.After(from) {
		return ExtractResult{}, fmt.Errorf("segments: extract requires from < to (got from=%s to=%s)", from, to)
	}

	files, err := listInRange(dir, from, to)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("segments: list dir %s: %w", dir, err)
	}
	if len(files) == 0 {
		return ExtractResult{}, ErrNoSegments
	}

	// Concatenate the raw ADTS bytes. ADTS frames are self-delimiting, so
	// `cat`-style concat produces a valid stream provided every segment
	// shares the same sample-rate/channel config (they do — ffmpeg `-c copy`
	// passes the original AAC frames through unchanged).
	concatPath, err := concatRawAAC(files)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("segments: concat: %w", err)
	}
	defer os.Remove(concatPath)

	// Trim to exact [from, to] using ffmpeg bitstream-level seek/duration.
	// First file's start time is the time-zero reference for the concat.
	earliestStart, err := parseFileStart(files[0])
	if err != nil {
		return ExtractResult{}, fmt.Errorf("segments: parse earliest start: %w", err)
	}

	ssOffset := from.Sub(earliestStart)
	if ssOffset < 0 {
		ssOffset = 0
	}
	requestedDur := to.Sub(from)

	trimmed, err := trimAAC(concatPath, ssOffset, requestedDur)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("segments: trim: %w", err)
	}

	// Compute coverage fraction by counting how many full SegmentDuration
	// windows of the requested range have a corresponding file on disk. A
	// missing window between two existing ones is what we call a gap.
	covered, partial := coverageStats(files, from, to)

	return ExtractResult{
		Data:            trimmed,
		Partial:         partial,
		CoveredFraction: covered,
	}, nil
}

// listInRange returns the absolute paths of segment files whose [start,
// start+SegmentDuration] interval overlaps [from, to], in chronological order.
// We widen the search by one SegmentDuration on the left so a file that
// started just before `from` but contains content at `from` is included.
func listInRange(dir string, from, to time.Time) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	leftBound := from.Add(-SegmentDuration)

	type candidate struct {
		path  string
		start time.Time
	}
	var cands []candidate
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, fileExt) {
			continue
		}
		base := strings.TrimSuffix(name, fileExt)
		t, err := time.ParseInLocation(fileNameLayout, base, time.Local)
		if err != nil {
			continue // unrelated file; ignore quietly
		}
		// Filter to overlap with [leftBound, to]. The left widening
		// handles the case where `from` falls inside the file that
		// started just before it.
		if t.Before(leftBound) || t.After(to) {
			continue
		}
		cands = append(cands, candidate{
			path:  filepath.Join(dir, name),
			start: t,
		})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].start.Before(cands[j].start) })

	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.path)
	}
	return out, nil
}

// parseFileStart pulls the start time from a segment file path produced by
// the strftime pattern in fileNameLayout.
func parseFileStart(path string) (time.Time, error) {
	base := strings.TrimSuffix(filepath.Base(path), fileExt)
	return time.ParseInLocation(fileNameLayout, base, time.Local)
}

// concatRawAAC writes a single temp file holding the concatenated bytes of
// the input segment files in order. ADTS framing makes this a legal stream;
// no ffmpeg call is needed for the concat step itself.
func concatRawAAC(files []string) (string, error) {
	tmp, err := os.CreateTemp("", "evidence-concat-*.aac")
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			os.Remove(tmp.Name())
			return "", fmt.Errorf("read %s: %w", f, err)
		}
		if _, err := tmp.Write(data); err != nil {
			os.Remove(tmp.Name())
			return "", fmt.Errorf("write concat: %w", err)
		}
	}
	return tmp.Name(), nil
}

// trimAAC runs ffmpeg with bitstream copy + seek/duration to extract exactly
// [ssOffset, ssOffset+duration] from the concatenated input. Frame-level
// (~21ms) precision is acceptable for evidence; demanding sample-precise
// trimming would require a decode/re-encode pass that costs CPU for no
// audible gain.
func trimAAC(input string, ssOffset, duration time.Duration) ([]byte, error) {
	tmpOut, err := os.CreateTemp("", "evidence-trim-*.aac")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpOut.Name())
	tmpOut.Close()

	args := []string{
		"-y",
		"-hide_banner", "-loglevel", "error",
		"-f", "aac",
		"-ss", formatSeconds(ssOffset),
		"-t", formatSeconds(duration),
		"-i", input,
		"-c:a", "copy",
		"-f", "adts",
		tmpOut.Name(),
	}
	cmd := exec.Command("ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, string(out))
	}

	return os.ReadFile(tmpOut.Name())
}

func formatSeconds(d time.Duration) string {
	return fmt.Sprintf("%.3f", d.Seconds())
}

// coverageStats walks the requested [from, to] range in SegmentDuration
// steps and counts how many steps have a corresponding file on disk.
// Returns (covered fraction in [0,1], partial flag).
func coverageStats(files []string, from, to time.Time) (float64, bool) {
	if !to.After(from) {
		return 0, false
	}
	intervals := make([][2]time.Time, 0, len(files))
	for _, f := range files {
		start, err := parseFileStart(f)
		if err != nil {
			continue
		}
		intervals = append(intervals, [2]time.Time{start, start.Add(SegmentDuration)})
	}

	step := time.Second
	total := int(to.Sub(from) / step)
	if total <= 0 {
		return 0, false
	}
	covered := 0
	for i := 0; i < total; i++ {
		t := from.Add(time.Duration(i) * step)
		for _, iv := range intervals {
			if !t.Before(iv[0]) && t.Before(iv[1]) {
				covered++
				break
			}
		}
	}
	frac := float64(covered) / float64(total)
	return frac, frac < 1.0
}
