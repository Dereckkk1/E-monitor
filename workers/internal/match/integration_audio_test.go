//go:build integration

// Integration test against the real master files in audio-refs/. Decodes the
// MP3s through the same fingerprint pipeline used in production, builds an
// in-memory index that mirrors what the SQL is_shared flag will produce, and
// drives the matcher window-by-window over the AMB30 PCM to assert that:
//
//   1. AMB30 confirms during AMB30 playback (regression — excluding shared
//      hashes from coverage must not break legitimate detection).
//   2. JINGLE never confirms during AMB30 playback, even though the last
//      ~6.25s of AMB30 audio is spectrally identical to the last ~6.25s of
//      JINGLE and would historically drive enough coverage to false-confirm.
//
// Run: go test -tags integration ./internal/match/ -run TestSharedHash -v

package match

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/fingerprint"
	"radiocheck/internal/index"
)

// repoRoot walks up the directory tree until it finds the audio-refs/
// directory, returning that ancestor.
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

// audioRefsScenario builds the in-memory fingerprint index for both AMB30 and
// JINGLE with shared-hash flagging applied. Returns the populated store, the
// hash counts (for diagnostic logging), and the per-commercial total-frames
// values that the state machine needs.
type audioRefsScenario struct {
	store             *index.Store
	amb30Hashes       []fingerprint.Hash
	jingleHashes      []fingerprint.Hash
	sharedValueCount  int
	amb30TotalFrames  int
	jingleTotalFrames int
	amb30DurationSec  float64
	jingleDurationSec float64
}

func setupAudioRefsScenario(t *testing.T, ctx context.Context) audioRefsScenario {
	t.Helper()
	root := repoRoot(t)
	amb30Path := filepath.Join(root, "audio-refs", "AMBIENTAL 30.mp3")
	jinglePath := filepath.Join(root, "audio-refs", "AMBIENTAL JINGLE.mp3")

	amb30Result, err := fingerprint.GenerateForVariant(ctx, amb30Path, fingerprint.VariantClean)
	if err != nil {
		t.Fatalf("fingerprint AMB30: %v", err)
	}
	jingleResult, err := fingerprint.GenerateForVariant(ctx, jinglePath, fingerprint.VariantClean)
	if err != nil {
		t.Fatalf("fingerprint JINGLE: %v", err)
	}

	const (
		amb30ShortID  int32 = 1
		jingleShortID int32 = 2
	)

	// Build an UNFLAGGED index first so we can run the matcher against it
	// to detect shared regions.
	idx := make(index.Index)
	for _, h := range amb30Result.Hashes {
		idx[h.Value] = append(idx[h.Value], index.Entry{
			CommercialShortID: amb30ShortID,
			TimeFrame:         int32(h.TimeFrame),
		})
	}
	for _, h := range jingleResult.Hashes {
		idx[h.Value] = append(idx[h.Value], index.Entry{
			CommercialShortID: jingleShortID,
			TimeFrame:         int32(h.TimeFrame),
		})
	}
	store := index.New()
	store.Swap(idx)

	// Detect shared regions per-commercial. For each commercial Y, decode its
	// PCM and run MatchWindow against the unflagged index. Any 4s window that
	// scores above the share threshold for *some other* commercial X means
	// there's audio overlap between Y and X. We record:
	//   - Y's time-frame range covered by the window (marks Y's hashes there
	//     as shared)
	//   - X's time-frame range, derived from the histogram delta (marks X's
	//     hashes there as shared)
	// This is the same algorithm we'll port into Persist's post-insert hook.
	// Threshold mirrors the runtime matcher's minScore so we mark every region
	// the runtime would treat as a match, not just the obvious peaks.
	const shareThreshold = 5

	type sharedRange struct {
		commercialID          int32
		fromFrame, untilFrame int32
	}
	var sharedRanges []sharedRange

	scan := func(path string, ownID int32) {
		pcm, err := fingerprint.DecodePCM(ctx, path, fingerprint.VariantClean)
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		const windowSamples = 16000 * 4
		const hopSamples = 16000 * 1
		for off := 0; off+windowSamples <= len(pcm); off += hopSamples {
			window := pcm[off : off+windowSamples]
			results := MatchWindow(window, store, shareThreshold, 0.0)
			ownStartFrame := int32(off / 2048)
			ownEndFrame := int32((off + windowSamples) / 2048)
			for _, r := range results {
				if r.CommercialShortID == ownID {
					continue // self — don't flag against itself
				}
				if r.Score < shareThreshold {
					continue
				}
				// Y's range: this window in Y's PCM.
				sharedRanges = append(sharedRanges, sharedRange{
					commercialID: ownID,
					fromFrame:    ownStartFrame,
					untilFrame:   ownEndFrame,
				})
				// X's range: derived from delta. live_time_frame -
				// entry.TimeFrame = OffsetFrames, so entry.TimeFrame =
				// live_time_frame - OffsetFrames. The window covers live
				// frames [ownStartFrame, ownEndFrame].
				xStart := int32(int(ownStartFrame) - r.OffsetFrames)
				xEnd := int32(int(ownEndFrame) - r.OffsetFrames)
				if xStart > xEnd {
					xStart, xEnd = xEnd, xStart
				}
				sharedRanges = append(sharedRanges, sharedRange{
					commercialID: r.CommercialShortID,
					fromFrame:    xStart,
					untilFrame:   xEnd,
				})
			}
		}
	}
	scan(amb30Path, amb30ShortID)
	scan(jinglePath, jingleShortID)

	// Build final index with IsShared filled in based on accumulated ranges.
	inSharedRange := func(commercialID int32, frame int32) bool {
		for _, sr := range sharedRanges {
			if sr.commercialID == commercialID && frame >= sr.fromFrame && frame < sr.untilFrame {
				return true
			}
		}
		return false
	}

	finalIdx := make(index.Index)
	for _, h := range amb30Result.Hashes {
		finalIdx[h.Value] = append(finalIdx[h.Value], index.Entry{
			CommercialShortID: amb30ShortID,
			TimeFrame:         int32(h.TimeFrame),
			IsShared:          inSharedRange(amb30ShortID, int32(h.TimeFrame)),
		})
	}
	for _, h := range jingleResult.Hashes {
		finalIdx[h.Value] = append(finalIdx[h.Value], index.Entry{
			CommercialShortID: jingleShortID,
			TimeFrame:         int32(h.TimeFrame),
			IsShared:          inSharedRange(jingleShortID, int32(h.TimeFrame)),
		})
	}
	finalStore := index.New()
	finalStore.Swap(finalIdx)

	// Diagnostic count of hashes flagged.
	flagged := 0
	for _, entries := range finalIdx {
		for _, e := range entries {
			if e.IsShared {
				flagged++
			}
		}
	}

	return audioRefsScenario{
		store:             finalStore,
		amb30Hashes:       amb30Result.Hashes,
		jingleHashes:      jingleResult.Hashes,
		sharedValueCount:  flagged,
		amb30TotalFrames:  amb30Result.Frames,
		jingleTotalFrames: jingleResult.Frames,
		amb30DurationSec:  amb30Result.DurationSec,
		jingleDurationSec: jingleResult.DurationSec,
	}
}

// driveMatcher decodes the file at `path` and feeds 4-second windows through
// MatchWindow at a 1-second hop, updating two state machines (one per
// commercial) and returning their confirmation counts.
func driveMatcher(
	t *testing.T,
	ctx context.Context,
	path string,
	store *index.Store,
	amb30TotalFrames, jingleTotalFrames int,
) (confirmedAMB30, confirmedJINGLE int, jingleScores, jingleUniqueScores []int) {
	t.Helper()

	pcm, err := fingerprint.DecodePCM(ctx, path, fingerprint.VariantClean)
	if err != nil {
		t.Fatalf("decode pcm %s: %v", path, err)
	}
	const (
		windowSamples = 16000 * 4 // 4-second analysis window
		hopSamples    = 16000 * 1 // 1-second hop
		minScore      = 5
		minCoverage   = 0.15
	)

	logger := zap.NewNop()
	smAMB30 := NewStateMachine("station-test", 1, amb30TotalFrames,
		minScore, minCoverage, 30*time.Second, 5*time.Second, logger)
	smJingle := NewStateMachine("station-test", 2, jingleTotalFrames,
		minScore, minCoverage, 30*time.Second, 5*time.Second, logger)

	startTime := time.Now()
	for off := 0; off+windowSamples <= len(pcm); off += hopSamples {
		window := pcm[off : off+windowSamples]
		// fingerprint.DecodePCM already returns float32 in [-1, 1] range,
		// so the slice can be handed to MatchWindow as-is.
		results := MatchWindow(window, store, minScore, 0.02)
		now := startTime.Add(time.Duration(off/16000) * time.Second)

		var amb, jin *MatchResult
		for i := range results {
			switch results[i].CommercialShortID {
			case 1:
				amb = &results[i]
			case 2:
				jin = &results[i]
			}
		}

		if amb != nil {
			if cd := smAMB30.Update(*amb, now); cd != nil {
				confirmedAMB30++
			}
		} else {
			// Tick is needed even when no result arrived for this commercial
			// so confirmTimeout/cooldown progress.
			smAMB30.Tick(now)
		}
		if jin != nil {
			jingleScores = append(jingleScores, jin.Score)
			jingleUniqueScores = append(jingleUniqueScores, jin.UniqueScore)
			if cd := smJingle.Update(*jin, now); cd != nil {
				confirmedJINGLE++
			}
		} else {
			smJingle.Tick(now)
		}
	}
	return confirmedAMB30, confirmedJINGLE, jingleScores, jingleUniqueScores
}

// TestSharedHash_AMB30_DoesNotFalseConfirmJINGLE is the primary regression: with
// shared-hash flagging in place, playing AMB30 must NOT cause a JINGLE
// confirmation even though the two masters share a sting at the end.
func TestSharedHash_AMB30_DoesNotFalseConfirmJINGLE(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sc := setupAudioRefsScenario(t, ctx)
	t.Logf("AMB30 hashes: %d, JINGLE hashes: %d, hashes flagged shared (across both): %d",
		len(sc.amb30Hashes), len(sc.jingleHashes), sc.sharedValueCount)
	if sc.sharedValueCount == 0 {
		t.Fatalf("setup produced 0 shared hashes; the false-positive scenario can't be reproduced — investigate audio refs")
	}

	root := repoRoot(t)
	amb30Path := filepath.Join(root, "audio-refs", "AMBIENTAL 30.mp3")

	confirmedAMB30, confirmedJINGLE, jingleScores, jingleUniqueScores :=
		driveMatcher(t, ctx, amb30Path, sc.store, sc.amb30TotalFrames, sc.jingleTotalFrames)

	t.Logf("AMB30 confirmations during AMB30 playback: %d", confirmedAMB30)
	t.Logf("JINGLE confirmations during AMB30 playback: %d (must be 0)", confirmedJINGLE)
	t.Logf("JINGLE per-window Score samples (max 10): %v", first10(jingleScores))
	t.Logf("JINGLE per-window UniqueScore samples (max 10): %v", first10(jingleUniqueScores))

	if confirmedJINGLE != 0 {
		t.Errorf("JINGLE was falsely confirmed %d times during AMB30 playback", confirmedJINGLE)
	}
	if confirmedAMB30 == 0 {
		t.Errorf("AMB30 should confirm at least once during its own playback")
	}
}

// TestSharedHash_JINGLE_StillConfirmsItself is the regression check for the
// other direction: removing shared hits from coverage must not block a real
// JINGLE detection. When JINGLE plays, JINGLE confirms.
func TestSharedHash_JINGLE_StillConfirmsItself(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sc := setupAudioRefsScenario(t, ctx)
	root := repoRoot(t)
	jinglePath := filepath.Join(root, "audio-refs", "AMBIENTAL JINGLE.mp3")

	confirmedAMB30, confirmedJINGLE, _, _ :=
		driveMatcher(t, ctx, jinglePath, sc.store, sc.amb30TotalFrames, sc.jingleTotalFrames)

	t.Logf("AMB30 confirmations during JINGLE playback: %d", confirmedAMB30)
	t.Logf("JINGLE confirmations during JINGLE playback: %d (must be ≥1)", confirmedJINGLE)

	if confirmedJINGLE == 0 {
		t.Errorf("JINGLE should confirm during its own playback; shared-hash filtering must not block legitimate matches")
	}
}

func first10(xs []int) []int {
	if len(xs) <= 10 {
		return xs
	}
	return xs[:10]
}
