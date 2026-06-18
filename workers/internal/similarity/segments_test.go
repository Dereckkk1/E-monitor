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
	segs := buildSegments(ws, 4)
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
	segs := buildSegments(ws, 4)
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
	segs := buildSegments(ws, 4)
	if len(segs) != 0 {
		t.Fatalf("esperava 0 segmentos (curto), veio %d", len(segs))
	}
}

// other negativo é clampado em 0; se o segmento inteiro é negativo, descarta.
func TestBuildSegments_ClampsOtherToZero(t *testing.T) {
	ws := []matchedWindow{{ownStart: 0, ownEnd: 31, offset: 10}}
	segs := buildSegments(ws, 4)
	if len(segs) != 1 || segs[0].OtherFrom != 0 {
		t.Fatalf("esperava OtherFrom clampado em 0: %+v", segs)
	}
}
