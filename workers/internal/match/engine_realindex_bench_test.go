package match

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"radiocheck/internal/index"
	"radiocheck/pkg/audio"
)

// engine_realindex_bench_test.go — custo do pass do matcher contra um indice
// REALISTA (N comerciais de ~20s com conteudo de banda larga), nao a senoide de
// 1 comercial do engine_pass_bench_test.go. Mede como o lookup/histograma
// escala com o tamanho do indice — o que governa o custo real em producao.
//
// Run:
//   go test -run='^$' -bench='BenchmarkRealIndexPass' -benchmem -benchtime=2s ./internal/match/

// makeRich gera ~dur amostras de conteudo de banda larga deterministico (8
// senoides + ruido leve) para aproximar a densidade de picos de audio real
// (~2-3 picos/frame), em vez da senoide pura que subconta hashes.
func makeRich(seed int64, n int) []float32 {
	r := rand.New(rand.NewSource(seed))
	freqs := make([]float64, 8)
	for i := range freqs {
		freqs[i] = 150 + r.Float64()*3600
	}
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		t := float64(i) / 16000.0
		var v float64
		for _, f := range freqs {
			v += math.Sin(2 * math.Pi * f * t)
		}
		v = v/8.0 + (r.Float64()*2-1)*0.05
		out[i] = float32(v * 0.5)
	}
	return out
}

func fpLive(samples []float32) []audio.Hash {
	f := audio.ApplyHighPass(samples, 100.0, 16000)
	n := audio.NormalizeRMS(f, -20.0)
	return audio.GenerateHashes(audio.PickPeaks(audio.STFT(n)))
}

func buildRealIndex(numCommercials, samplesPerCommercial int) (*index.Store, int, int) {
	idx := make(index.Index)
	totalEntries := 0
	for c := 0; c < numCommercials; c++ {
		sig := makeRich(int64(c+1), samplesPerCommercial)
		for _, h := range fpLive(sig) {
			idx[h.Value] = append(idx[h.Value], index.Entry{
				CommercialShortID: int32(c),
				TimeFrame:         int32(h.TimeFrame),
			})
			totalEntries++
		}
	}
	store := index.New()
	store.Swap(idx)
	return store, len(idx), totalEntries
}

func benchmarkRealIndex(b *testing.B, numCommercials int) {
	const samplesPerCommercial = 320000 // 20s a 16kHz
	store, distinct, entries := buildRealIndex(numCommercials, samplesPerCommercial)
	// Query: janela de 4s do comercial 0 (casa no indice) — mesmo shape do worker.
	sig0 := makeRich(1, samplesPerCommercial)
	window := sig0[:64000]
	bytesPerEntry := 10 // int32 + uint8 + uint8 + int32 + bool ~ 10-16B; map overhead a parte
	b.Logf("comerciais=%d | hashes distintos=%d | entradas totais=%d | RAM aprox indice=%.1f MB (so postings)",
		numCommercials, distinct, entries, float64(entries*bytesPerEntry)/1e6)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = MatchWindowDetailed(window, store, 5, 0.02)
	}
}

func BenchmarkRealIndexPass(b *testing.B) {
	for _, n := range []int{50, 150, 300} {
		b.Run(fmt.Sprintf("commercials=%d", n), func(b *testing.B) {
			benchmarkRealIndex(b, n)
		})
	}
}
