package similarity

import "encoding/json"

// framesToSec converte frames do matcher (stftHop=2048 @ 16kHz) em segundos.
const framesToSec = 2048.0 / 16000.0

type segmentJSON struct {
	Own   [2]float64 `json:"own"`
	Other [2]float64 `json:"other"`
}

// overlapJSON é o conteúdo de materials.similarity_segments. Tudo em segundos.
type overlapJSON struct {
	OwnCov        float64       `json:"own_cov"`
	OtherCov      float64       `json:"other_cov"`
	OwnDuration   float64       `json:"own_duration"`
	OtherDuration float64       `json:"other_duration"`
	Segments      []segmentJSON `json:"segments"`
}

// buildOverlapJSON monta o JSON persistido, convertendo segmentos de frames
// para segundos.
func buildOverlapJSON(ownCov, otherCov, ownDur, otherDur float64, segs []Segment) json.RawMessage {
	out := overlapJSON{
		OwnCov:        ownCov,
		OtherCov:      otherCov,
		OwnDuration:   ownDur,
		OtherDuration: otherDur,
		Segments:      make([]segmentJSON, 0, len(segs)),
	}
	for _, s := range segs {
		out.Segments = append(out.Segments, segmentJSON{
			Own:   [2]float64{float64(s.OwnFrom) * framesToSec, float64(s.OwnTo) * framesToSec},
			Other: [2]float64{float64(s.OtherFrom) * framesToSec, float64(s.OtherTo) * framesToSec},
		})
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
