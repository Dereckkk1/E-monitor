package similarity

import "encoding/json"

// framesToSec converte frames do matcher (stftHop=2048 @ 16kHz) em segundos.
const framesToSec = 2048.0 / 16000.0

// overlapJSON é o conteúdo de materials.similarity_segments. Tudo em segundos.
// own_segments / other_segments são os trechos iguais já fundidos em cada eixo
// (a timeline desenha um por faixa); own_cov/other_cov são consistentes com
// eles.
type overlapJSON struct {
	OwnCov        float64      `json:"own_cov"`
	OtherCov      float64      `json:"other_cov"`
	OwnDuration   float64      `json:"own_duration"`
	OtherDuration float64      `json:"other_duration"`
	OwnSegments   [][2]float64 `json:"own_segments"`
	OtherSegments [][2]float64 `json:"other_segments"`
}

// buildOverlapJSON monta o JSON persistido a partir dos trechos já fundidos
// (em segundos) de cada eixo.
func buildOverlapJSON(ownCov, otherCov, ownDur, otherDur float64, ownSegs, otherSegs [][2]float64) json.RawMessage {
	if ownSegs == nil {
		ownSegs = [][2]float64{}
	}
	if otherSegs == nil {
		otherSegs = [][2]float64{}
	}
	out := overlapJSON{
		OwnCov:        ownCov,
		OtherCov:      otherCov,
		OwnDuration:   ownDur,
		OtherDuration: otherDur,
		OwnSegments:   ownSegs,
		OtherSegments: otherSegs,
	}
	b, _ := json.Marshal(out) // overlapJSON é sempre serializável
	return b
}

// coverages recalcula ownCov/otherCov do top match (mesma fórmula do
// pickTopMatch), clampados em [0,1].
func coverages(s *pairScan, ownTotalFrames int) (ownCov, otherCov float64) {
	if ownTotalFrames > 0 {
		ownCov = float64(frameCoverage(s.ownRanges)) / float64(ownTotalFrames)
	}
	if s.otherTotalFrames > 0 {
		otherCov = float64(frameCoverage(s.otherRanges)) / float64(s.otherTotalFrames)
	}
	if ownCov > 1.0 {
		ownCov = 1.0
	}
	if otherCov > 1.0 {
		otherCov = 1.0
	}
	return ownCov, otherCov
}
