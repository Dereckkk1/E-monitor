package similarity

import "sort"

// matchedWindow é uma janela que casou com o top match: posição no material
// novo (own) + offset de alinhamento, em frames.
type matchedWindow struct{ ownStart, ownEnd, offset int32 }

// Segment é um trecho igual conectado entre o material novo (own) e o
// existente (other), em FRAMES. half-open [from, to).
type Segment struct {
	OwnFrom, OwnTo     int32
	OtherFrom, OtherTo int32
}

// buildSegments agrupa as janelas casadas por offset (cada offset é um
// alinhamento próprio entre os dois materiais), funde janelas contíguas/
// sobrepostas no eixo own (via mergeRanges), e converte cada faixa fundida num
// Segment conectado (other = own - offset). Descarta segmentos com menos de
// minFrames de duração e ordena por OwnFrom.
//
// otherTotalFrames é a duração do material existente (em frames). Segmentos
// cujo lado `other` cai INTEIRAMENTE fora de [0, otherTotalFrames] são
// descartados — são casamentos espúrios contra áudio inexistente (as variantes
// de broadcast-sim com ruído geram alinhamentos fantasma). O lado `other` que
// estoura a duração é cortado na borda.
func buildSegments(windows []matchedWindow, minFrames, otherTotalFrames int32) []Segment {
	byOffset := make(map[int32][]frameRange)
	for _, w := range windows {
		byOffset[w.offset] = append(byOffset[w.offset], frameRange{w.ownStart, w.ownEnd})
	}
	var segs []Segment
	for off, rs := range byOffset {
		for _, r := range mergeRanges(rs) {
			if r.until-r.from < minFrames {
				continue
			}
			otherFrom := r.from - off
			otherTo := r.until - off
			if otherTo <= 0 || otherFrom >= otherTotalFrames {
				continue // fora dos limites do material existente — fantasma
			}
			if otherFrom < 0 {
				otherFrom = 0
			}
			if otherTo > otherTotalFrames {
				otherTo = otherTotalFrames
			}
			segs = append(segs, Segment{r.from, r.until, otherFrom, otherTo})
		}
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].OwnFrom < segs[j].OwnFrom })
	return segs
}
