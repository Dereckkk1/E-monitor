//go:build integration

// Diagnostic for the second AMBIENTAL JINGLE incident: verifies whether the
// captured evidence actually contains a sequential playback of the master
// (= real detection), or whether the matcher fired on scattered spectral
// coincidences (= false positive).

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

func TestDiagnose_AmbientalJingle2(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	root := repoRoot(t)
	masterPath := filepath.Join(root, "audio-refs", "AMBIENTAL JINGLE (1).mp3")
	evidencePath := filepath.Join(root, "audio-refs", "veiculacao-aa0fbd27-c19f-4cee-9a88-dcb0f4484257.m4a")

	// Reproduce Python's broadcast_sim variant 0 path: acompressor + AAC 96k
	// re-encode/decode, then run the matcher's own preprocessing (highpass100
	// + RMS normalize) on the resulting PCM to mirror what fingerprint/
	// generator.py does. The Go fingerprint.GenerateForVariant(VariantClean)
	// uses loudnorm and produces a different hash set from the Python pipeline,
	// which is why an earlier reproduction showed zero hits even though
	// production matched (variant_used=0).
	const variant0Master = "C:/tmp/audio-fp3/master-v0.m4a"
	masterRes, err := fingerprint.GenerateForVariant(ctx, variant0Master, fingerprint.VariantClean)
	if err != nil {
		t.Fatalf("fingerprint master: %v", err)
	}
	t.Logf("master (variant-0 reencoded): %.2fs, %d hashes", masterRes.DurationSec, len(masterRes.Hashes))
	_ = masterPath

	idx := make(index.Index)
	for _, h := range masterRes.Hashes {
		idx[h.Value] = append(idx[h.Value], index.Entry{
			CommercialShortID: 1,
			TimeFrame:         int32(h.TimeFrame),
		})
	}
	store := index.New()
	store.Swap(idx)

	pcm, err := fingerprint.DecodePCM(ctx, evidencePath, fingerprint.VariantClean)
	if err != nil {
		t.Fatalf("decode evidence: %v", err)
	}
	t.Logf("evidence pcm: %.2fs", float64(len(pcm))/16000.0)

	const (
		windowSamples = 16000 * 4
		hopSamples    = 16000 * 1
	)
	type hit struct {
		offSec       float64
		score        int
		offsetFrames int
	}
	var hits []hit
	for off := 0; off+windowSamples <= len(pcm); off += hopSamples {
		w := pcm[off : off+windowSamples]
		results := MatchWindow(w, store, 5, 0.0)
		for _, r := range results {
			hits = append(hits, hit{float64(off) / 16000.0, r.Score, r.OffsetFrames})
		}
	}
	t.Logf("hits with score >= 5: %d", len(hits))

	if len(hits) == 0 {
		t.Logf("VEREDICTO: zero hits — master não está no áudio (provável bug de captura/falso positivo)")
		return
	}

	for i, h := range hits {
		if i >= 30 {
			t.Logf("  ... +%d more", len(hits)-30)
			break
		}
		masterTimeAtThisWindow := h.offSec - float64(h.offsetFrames)*2048.0/16000.0
		t.Logf("  evidence t=%6.2fs  score=%3d  → master_t=%6.2fs",
			h.offSec, h.score, masterTimeAtThisWindow)
	}

	// Histogram of delta_bins. A real sequential playback should show ONE
	// dominant bin in many consecutive windows. A false positive shows
	// scattered bins.
	binFreq := make(map[int]int)
	for _, h := range hits {
		binFreq[h.offsetFrames/DeltaBinSize]++
	}
	type kv struct{ bin, count int }
	var bins []kv
	for b, c := range binFreq {
		bins = append(bins, kv{b, c})
	}
	sort.Slice(bins, func(i, j int) bool { return bins[i].count > bins[j].count })

	t.Logf("distinct delta_bins: %d", len(bins))
	for i, b := range bins {
		if i >= 5 {
			break
		}
		t.Logf("  delta_bin=%+d → %d windows (%.1f%%)",
			b.bin, b.count, 100*float64(b.count)/float64(len(hits)))
	}
	if len(bins) > 0 {
		topPct := 100 * float64(bins[0].count) / float64(len(hits))
		if topPct >= 60 {
			t.Logf("VEREDICTO: %.0f%% das janelas no mesmo delta_bin → áudio do master tocou de verdade", topPct)
		} else {
			t.Logf("VEREDICTO: top delta_bin só %.0f%% → matches espalhados, FALSO POSITIVO de coincidência espectral", topPct)
		}
	}
}
