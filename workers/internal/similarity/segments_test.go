package similarity

import "testing"

// Janelas contíguas no mesmo offset fundem num único segmento; o lado "other"
// é deslocado pelo offset.
func TestBuildSegments_MergesContiguousSameOffset(t *testing.T) {
	ws := []matchedWindow{
		{ownStart: 0, ownEnd: 31, offset: 0},
		{ownStart: 8, ownEnd: 39, offset: 0},
		{ownStart: 16, ownEnd: 47, offset: 0},
	}
	segs := buildSegments(ws, 4, 10000)
	if len(segs) != 1 {
		t.Fatalf("esperava 1 segmento, veio %d: %+v", len(segs), segs)
	}
	if segs[0].OwnFrom != 0 || segs[0].OwnTo != 47 {
		t.Fatalf("own range errado: %+v", segs[0])
	}
	if segs[0].OtherFrom != 0 || segs[0].OtherTo != 47 {
		t.Fatalf("other range (offset 0) errado: %+v", segs[0])
	}
}

// Offsets diferentes (alinhamentos distintos) viram segmentos separados; o
// lado other = own - offset.
func TestBuildSegments_SeparatesByOffset(t *testing.T) {
	ws := []matchedWindow{
		{ownStart: 0, ownEnd: 31, offset: 0},
		{ownStart: 200, ownEnd: 231, offset: 100},
	}
	segs := buildSegments(ws, 4, 10000)
	if len(segs) != 2 {
		t.Fatalf("esperava 2 segmentos, veio %d", len(segs))
	}
	// ordenado por OwnFrom: [0]=offset0, [1]=offset100
	if segs[1].OtherFrom != 100 || segs[1].OtherTo != 131 {
		t.Fatalf("other do 2o segmento (own-offset) errado: %+v", segs[1])
	}
}

// Segmentos curtos (< minFrames) são descartados como ruído.
func TestBuildSegments_DropsShort(t *testing.T) {
	ws := []matchedWindow{{ownStart: 0, ownEnd: 3, offset: 0}}
	segs := buildSegments(ws, 4, 10000)
	if len(segs) != 0 {
		t.Fatalf("esperava 0 segmentos (curto), veio %d", len(segs))
	}
}

// Segmentos cujo lado `other` cai inteiramente fora do material existente
// (offset fantasma das variantes com ruído) são descartados.
func TestBuildSegments_DropsOutOfBoundsOther(t *testing.T) {
	// own[200,231] com offset -200 → other[400,431], mas o material existente
	// só tem 300 frames → fantasma, descarta.
	ws := []matchedWindow{{ownStart: 200, ownEnd: 231, offset: -200}}
	segs := buildSegments(ws, 4, 300)
	if len(segs) != 0 {
		t.Fatalf("esperava 0 segmentos (other fora dos limites), veio %d: %+v", len(segs), segs)
	}
}

// O lado `other` que estoura a duração é cortado na borda do material.
func TestBuildSegments_ClampsOtherToDuration(t *testing.T) {
	ws := []matchedWindow{{ownStart: 280, ownEnd: 320, offset: 0}}
	segs := buildSegments(ws, 4, 300)
	if len(segs) != 1 || segs[0].OtherTo != 300 {
		t.Fatalf("esperava OtherTo cortado em 300: %+v", segs)
	}
}

// other negativo é clampado em 0; se o segmento inteiro é negativo, descarta.
func TestBuildSegments_ClampsOtherToZero(t *testing.T) {
	ws := []matchedWindow{{ownStart: 0, ownEnd: 31, offset: 10}}
	segs := buildSegments(ws, 4, 10000)
	if len(segs) != 1 || segs[0].OtherFrom != 0 {
		t.Fatalf("esperava OtherFrom clampado em 0: %+v", segs)
	}
}

// runScan deve registrar, por par, as janelas casadas (own + offset) pra
// alimentar buildSegments. Aqui garantimos que o campo existe e acumula.
func TestPairScan_HasWindowsField(t *testing.T) {
	s := &pairScan{}
	s.windows = append(s.windows, matchedWindow{0, 31, 0})
	if len(s.windows) != 1 {
		t.Fatalf("campo windows não acumulou")
	}
}
