package similarity

import "testing"

// Ranges contíguos/sobrepostos no eixo own fundem num único trecho, convertido
// pra segundos. 0–47 frames * 2048/16000 = 0–6.016s.
func TestMergeRegionsSec_MergesContiguous(t *testing.T) {
	rs := []frameRange{{0, 31}, {8, 39}, {16, 47}}
	segs := mergeRegionsSec(rs, 10000)
	if len(segs) != 1 {
		t.Fatalf("esperava 1 trecho, veio %d: %+v", len(segs), segs)
	}
	if segs[0][0] != 0 {
		t.Fatalf("início errado: %+v", segs[0])
	}
	if s := segs[0][1]; s < 5.9 || s > 6.1 {
		t.Fatalf("fim em segundos errado: %v", s)
	}
}

// Trechos que estouram a duração do material são cortados na borda.
func TestMergeRegionsSec_ClampsToDuration(t *testing.T) {
	// material de 100 frames (=12.8s); range vai até 200 → corta em 100.
	segs := mergeRegionsSec([]frameRange{{80, 200}}, 100)
	if len(segs) != 1 {
		t.Fatalf("esperava 1 trecho, veio %d", len(segs))
	}
	if want := 100 * framesToSec; segs[0][1] != want {
		t.Fatalf("fim deveria ser %.3fs (borda do material), veio %v", want, segs[0][1])
	}
}

// Trechos inteiramente fora do material (alinhamento fantasma) são descartados.
func TestMergeRegionsSec_DropsOutOfBounds(t *testing.T) {
	segs := mergeRegionsSec([]frameRange{{400, 431}}, 300)
	if len(segs) != 0 {
		t.Fatalf("esperava 0 trechos (fora dos limites), veio %d: %+v", len(segs), segs)
	}
}

// Início negativo é clampado em 0.
func TestMergeRegionsSec_ClampsStartToZero(t *testing.T) {
	segs := mergeRegionsSec([]frameRange{{-10, 31}}, 10000)
	if len(segs) != 1 || segs[0][0] != 0 {
		t.Fatalf("esperava início clampado em 0: %+v", segs)
	}
}

// Sem ranges → lista vazia (não nil), pra serializar como [].
func TestMergeRegionsSec_Empty(t *testing.T) {
	segs := mergeRegionsSec(nil, 100)
	if segs == nil || len(segs) != 0 {
		t.Fatalf("esperava lista vazia não-nil, veio %+v", segs)
	}
}
