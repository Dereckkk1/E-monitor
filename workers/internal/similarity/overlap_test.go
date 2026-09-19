package similarity

import (
	"encoding/json"
	"testing"
)

func TestBuildOverlapJSON_ShapeAndSegments(t *testing.T) {
	ownSegs := [][2]float64{{0.0, 5.0}, {25.0, 30.0}}
	otherSegs := [][2]float64{{0.0, 5.0}, {55.0, 60.0}}
	raw := buildOverlapJSON(0.33, 0.5, 30.0, 60.0, ownSegs, otherSegs)

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
	if len(got.OwnSegments) != 2 || len(got.OtherSegments) != 2 {
		t.Fatalf("segmentos errados: %+v", got)
	}
	if got.OwnSegments[1][0] != 25.0 || got.OtherSegments[1][1] != 60.0 {
		t.Fatalf("conteúdo dos segmentos errado: %+v", got)
	}
}

// Listas nil viram [] no JSON (não null), pra o frontend sempre iterar.
func TestBuildOverlapJSON_NilBecomesEmptyArray(t *testing.T) {
	raw := buildOverlapJSON(0, 0, 10, 10, nil, nil)
	if got := string(raw); !contains(got, `"own_segments":[]`) || !contains(got, `"other_segments":[]`) {
		t.Fatalf("esperava arrays vazios, veio %s", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
