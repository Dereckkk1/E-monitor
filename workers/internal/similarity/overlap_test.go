package similarity

import (
	"encoding/json"
	"testing"
)

func TestBuildOverlapJSON_ShapeAndSeconds(t *testing.T) {
	segs := []Segment{{OwnFrom: 0, OwnTo: 39, OtherFrom: 0, OtherTo: 39}}
	raw := buildOverlapJSON(0.33, 0.5, 30.0, 60.0, segs)

	var got overlapJSON
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json inválido: %v", err)
	}
	if got.OwnCov != 0.33 || got.OtherCov != 0.5 {
		t.Fatalf("cov errado: %+v", got)
	}
	if got.OwnDuration != 30.0 || got.OtherDuration != 60.0 {
		t.Fatalf("duração errada: %+v", got)
	}
	if len(got.Segments) != 1 {
		t.Fatalf("esperava 1 segmento, veio %d", len(got.Segments))
	}
	// 39 frames * 2048 / 16000 = 4.992s
	if s := got.Segments[0].Own[1]; s < 4.9 || s > 5.1 {
		t.Fatalf("own[1] em segundos errado: %v", s)
	}
}
