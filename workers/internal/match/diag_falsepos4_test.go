//go:build integration

// Diagnostic for the 2026-05-12 overnight false negatives reported by
// the user: vendor system picked up two plays our system missed.
//   - JINGLE ROGGA VERÃO 30 on 89 FM Joinville around 07:02:43
//   - PULSO SONORO on 105 FM Jaraguá around 06:55:56
//
// Two hypotheses to discriminate:
//   1. Acoustic miss — master simply doesn't match the broadcast capture
//      (signal degradation, station-specific encoding, mismatch).
//   2. Threshold/coverage miss — match exists in audio but state machine
//      doesn't accumulate enough coverage to confirm (e.g. PULSO is only
//      7s long — half a typical analysis window scan).

package match

import (
	"context"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"radiocheck/internal/fingerprint"
	"radiocheck/internal/index"
)

func TestDiagnose_Overnight20260512(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	root := repoRoot(t)

	const (
		jingleShortID int32 = 100
		pulsoShortID  int32 = 101
	)

	jingleV0 := "C:/tmp/audio-fp3/jingle-rogga-v0.m4a"
	pulsoV0 := "C:/tmp/audio-fp3/pulso-v0.m4a"

	jingleRes, err := fingerprint.GenerateForVariant(ctx, jingleV0, fingerprint.VariantClean)
	if err != nil {
		t.Fatalf("fingerprint JINGLE ROGGA: %v", err)
	}
	pulsoRes, err := fingerprint.GenerateForVariant(ctx, pulsoV0, fingerprint.VariantClean)
	if err != nil {
		t.Fatalf("fingerprint PULSO: %v", err)
	}
	t.Logf("JINGLE ROGGA master: %.2fs  %d hashes  %.0f hashes/s",
		jingleRes.DurationSec, len(jingleRes.Hashes), jingleRes.HashesPerSec)
	t.Logf("PULSO master:        %.2fs  %d hashes  %.0f hashes/s",
		pulsoRes.DurationSec, len(pulsoRes.Hashes), pulsoRes.HashesPerSec)

	idx := make(index.Index)
	for _, h := range jingleRes.Hashes {
		idx[h.Value] = append(idx[h.Value], index.Entry{
			CommercialShortID: jingleShortID,
			TimeFrame:         int32(h.TimeFrame),
		})
	}
	for _, h := range pulsoRes.Hashes {
		idx[h.Value] = append(idx[h.Value], index.Entry{
			CommercialShortID: pulsoShortID,
			TimeFrame:         int32(h.TimeFrame),
		})
	}
	store := index.New()
	store.Swap(idx)

	captures := []struct {
		label string
		path  string
	}{
		{"89.5 Joinville @ 07:02:43 (expected: JINGLE ROGGA)",
			filepath.Join(root, "audio-refs", "12_05_2026 -  07_02_43 - 89 - FM (89.5) - Joinville_SC.mp3")},
		{"105.7 Jaraguá @ 06:55:56 (expected: PULSO SONORO)",
			filepath.Join(root, "audio-refs", "12_05_2026 -  06_55_56 - 105 (Guaramirim) - FM (105.7) - Jaraguá do Sul_SC.mp3")},
	}

	for _, cap := range captures {
		t.Run(cap.label, func(t *testing.T) {
			pcm, err := fingerprint.DecodePCM(ctx, cap.path, fingerprint.VariantClean)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			t.Logf("capture pcm: %.2fs (%d samples)", float64(len(pcm))/16000.0, len(pcm))

			const (
				windowSamples = 16000 * 4
				hopSamples    = 16000 * 1
			)
			type hit struct {
				offSec       float64
				shortID      int32
				score        int
				offsetFrames int
			}
			var hits []hit
			perCommercial := map[int32]int{}
			binsByCommercial := map[int32]map[int]int{
				jingleShortID: {},
				pulsoShortID:  {},
			}
			for off := 0; off+windowSamples <= len(pcm); off += hopSamples {
				w := pcm[off : off+windowSamples]
				results := MatchWindow(w, store, 5, 0.0)
				for _, r := range results {
					hits = append(hits, hit{
						offSec:       float64(off) / 16000.0,
						shortID:      r.CommercialShortID,
						score:        r.Score,
						offsetFrames: r.OffsetFrames,
					})
					perCommercial[r.CommercialShortID]++
					binsByCommercial[r.CommercialShortID][r.OffsetFrames/DeltaBinSize]++
				}
			}
			t.Logf("JINGLE ROGGA (short=%d): %d windows ≥5", jingleShortID, perCommercial[jingleShortID])
			t.Logf("PULSO         (short=%d): %d windows ≥5", pulsoShortID, perCommercial[pulsoShortID])

			// Identify dominant delta_bin per commercial. A real continuous
			// playback gives ONE dominant bin; scattered bins = noise.
			for _, sid := range []int32{jingleShortID, pulsoShortID} {
				if perCommercial[sid] == 0 {
					continue
				}
				type kv struct{ bin, count int }
				var bs []kv
				for b, c := range binsByCommercial[sid] {
					bs = append(bs, kv{b, c})
				}
				sort.Slice(bs, func(i, j int) bool { return bs[i].count > bs[j].count })
				top := bs[0]
				topPct := 100.0 * float64(top.count) / float64(perCommercial[sid])
				t.Logf("  short=%d  top delta_bin=%+d  %d/%d windows (%.0f%%)  distinct_bins=%d",
					sid, top.bin, top.count, perCommercial[sid], topPct, len(bs))

				// Print the time-clustered hits for the dominant bin.
				var clustered []hit
				for _, h := range hits {
					if h.shortID == sid && h.offsetFrames/DeltaBinSize == top.bin {
						clustered = append(clustered, h)
					}
				}
				sort.Slice(clustered, func(i, j int) bool { return clustered[i].offSec < clustered[j].offSec })
				if len(clustered) > 0 {
					first := clustered[0].offSec
					last := clustered[len(clustered)-1].offSec
					t.Logf("    dominant bin spans capture t=%.1fs..%.1fs (%.1fs duration)",
						first, last, last-first)
					for i, h := range clustered {
						if i >= 15 {
							t.Logf("    ... +%d more", len(clustered)-15)
							break
						}
						masterT := h.offSec - float64(h.offsetFrames)*2048.0/16000.0
						t.Logf("    capture t=%6.2fs  score=%3d  → master_t=%6.2fs", h.offSec, h.score, masterT)
					}
				}
			}
		})
	}
}
