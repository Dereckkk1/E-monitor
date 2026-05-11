//go:build integration

// Diagnostic for the 2026-05-11 Oeste Capital case: JINGLE was the real play,
// but the disambiguation logic retracted it in favor of AMB30. This test
// confirms acoustically that BOTH veiculacao captures contain JINGLE (the real
// play), and AMB30 only matches via the shared sting tail — proving the AMB30
// detection was the false positive and the JINGLE retraction was wrong.

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

func TestDiagnose_OesteCapital_20260511(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	root := repoRoot(t)

	const (
		amb30ShortID  int32 = 11
		jingleShortID int32 = 12
	)

	// Use the variant-0 reencoded masters (acompressor + AAC 96k) to mirror
	// what the Python broadcast_sim pipeline produces in production.
	amb30v0 := "C:/tmp/audio-fp3/amb30-v0.m4a"
	jingleV0 := "C:/tmp/audio-fp3/jingle-v0.m4a"

	amb30Res, err := fingerprint.GenerateForVariant(ctx, amb30v0, fingerprint.VariantClean)
	if err != nil {
		t.Fatalf("fingerprint AMB30: %v", err)
	}
	jingleRes, err := fingerprint.GenerateForVariant(ctx, jingleV0, fingerprint.VariantClean)
	if err != nil {
		t.Fatalf("fingerprint JINGLE: %v", err)
	}
	t.Logf("AMB30 master:  %.2fs  %d hashes", amb30Res.DurationSec, len(amb30Res.Hashes))
	t.Logf("JINGLE master: %.2fs  %d hashes", jingleRes.DurationSec, len(jingleRes.Hashes))

	idx := make(index.Index)
	for _, h := range amb30Res.Hashes {
		idx[h.Value] = append(idx[h.Value], index.Entry{
			CommercialShortID: amb30ShortID,
			TimeFrame:         int32(h.TimeFrame),
		})
	}
	for _, h := range jingleRes.Hashes {
		idx[h.Value] = append(idx[h.Value], index.Entry{
			CommercialShortID: jingleShortID,
			TimeFrame:         int32(h.TimeFrame),
		})
	}
	store := index.New()
	store.Swap(idx)

	veiculacoes := []struct {
		label string
		path  string
	}{
		{"AMB30 detection capture (272bd95e)",
			filepath.Join(root, "audio-refs", "veiculacao-272bd95e-8669-43c7-9899-33c27029bb9b.m4a")},
		{"JINGLE detection capture (cd4840ff)",
			filepath.Join(root, "audio-refs", "veiculacao-cd4840ff-b4d9-44c9-ac9c-e0be675aee5e.m4a")},
	}

	for _, v := range veiculacoes {
		t.Run(v.label, func(t *testing.T) {
			pcm, err := fingerprint.DecodePCM(ctx, v.path, fingerprint.VariantClean)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			t.Logf("evidence pcm: %.2fs", float64(len(pcm))/16000.0)

			const (
				windowSamples = 16000 * 4
				hopSamples    = 16000 * 1
			)
			hitsByCommercial := map[int32]int{}
			binsByCommercial := map[int32]map[int]int{
				amb30ShortID:  {},
				jingleShortID: {},
			}
			for off := 0; off+windowSamples <= len(pcm); off += hopSamples {
				w := pcm[off : off+windowSamples]
				results := MatchWindow(w, store, 5, 0.0)
				for _, r := range results {
					hitsByCommercial[r.CommercialShortID]++
					binsByCommercial[r.CommercialShortID][r.OffsetFrames/DeltaBinSize]++
				}
			}
			for _, id := range []int32{amb30ShortID, jingleShortID} {
				name := "AMB30"
				if id == jingleShortID {
					name = "JINGLE"
				}
				t.Logf("%s (short=%d): %d hits with score>=5", name, id, hitsByCommercial[id])
				if hitsByCommercial[id] == 0 {
					continue
				}
				type kv struct{ bin, count int }
				var bs []kv
				for b, c := range binsByCommercial[id] {
					bs = append(bs, kv{b, c})
				}
				sort.Slice(bs, func(i, j int) bool { return bs[i].count > bs[j].count })
				top := bs[0]
				topPct := 100.0 * float64(top.count) / float64(hitsByCommercial[id])
				t.Logf("  top delta_bin=%+d  %d/%d windows (%.0f%%)  distinct_bins=%d",
					top.bin, top.count, hitsByCommercial[id], topPct, len(bs))
			}
		})
	}
}
