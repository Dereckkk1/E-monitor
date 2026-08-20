package anatel

import (
	"math"
	"testing"
)

// TestFMClassTable trava a Tabela de Requisitos Máximos (Anatel Res. 546/2010).
// Se alguém "arredondar" um valor aqui, a área de cobertura de milhares de
// emissoras muda silenciosamente — daí o teste ser exaustivo.
func TestFMClassTable(t *testing.T) {
	want := []struct {
		class   string
		erpKW   float64
		heightM int
		km      float64
	}{
		{"E1", 100, 600, 78.5},
		{"E2", 75, 450, 67.5},
		{"E3", 60, 300, 54.5},
		{"A1", 50, 150, 38.5},
		{"A2", 30, 150, 35.0},
		{"A3", 15, 150, 30.0},
		{"A4", 5, 150, 24.0},
		{"B1", 3, 90, 16.5},
		{"B2", 1, 90, 12.5},
		{"C", 0.3, 60, 7.5},
	}
	if len(fmClasses) != len(want) {
		t.Fatalf("fmClasses tem %d entradas, esperado %d", len(fmClasses), len(want))
	}
	for _, w := range want {
		got, ok := Spec("FM", w.class)
		if !ok {
			t.Fatalf("classe FM %s ausente", w.class)
		}
		if got.MaxERPkW != w.erpKW || got.RefHeightM != w.heightM || got.CoverageKm != w.km {
			t.Errorf("classe %s = %+v, esperado erp=%v h=%v km=%v", w.class, got, w.erpKW, w.heightM, w.km)
		}
	}
}

// TestFMClassERPMatchesDBk confere a coluna ERP contra a coluna dBk da norma
// (dBk = 10·log10(ERP em kW)). É a checagem que provou que a transcrição da
// tabela está com as colunas na ordem certa.
func TestFMClassERPMatchesDBk(t *testing.T) {
	dbk := map[string]float64{
		"E1": 20.0, "E2": 18.8, "E3": 17.8, "A1": 17.0, "A2": 14.8,
		"A3": 11.8, "A4": 7.0, "B1": 4.8, "B2": 0, "C": -5.2,
	}
	for class, want := range dbk {
		s := fmClasses[class]
		got := 10 * math.Log10(s.MaxERPkW)
		if math.Abs(got-want) > 0.05 {
			t.Errorf("classe %s: ERP %v kW = %.2f dBk, norma diz %.1f dBk", class, s.MaxERPkW, got, want)
		}
	}
}

// TestFMCoverageMonotonic: mais potência nunca pode dar menos alcance.
func TestFMCoverageMonotonic(t *testing.T) {
	order := []string{"C", "B2", "B1", "A4", "A3", "A2", "A1", "E3", "E2", "E1"}
	for i := 1; i < len(order); i++ {
		lo, hi := fmClasses[order[i-1]], fmClasses[order[i]]
		if hi.MaxERPkW <= lo.MaxERPkW {
			t.Errorf("ERP não monotônico: %s(%v) <= %s(%v)", hi.Class, hi.MaxERPkW, lo.Class, lo.MaxERPkW)
		}
		if hi.CoverageKm <= lo.CoverageKm {
			t.Errorf("cobertura não monotônica: %s(%v) <= %s(%v)", hi.Class, hi.CoverageKm, lo.Class, lo.CoverageKm)
		}
	}
}

// TestAMHasNoDistanceCoverage: o contorno protegido de OM é definido em mV/m,
// não em km. Se alguém inventar um raio para AM, este teste quebra.
func TestAMHasNoDistanceCoverage(t *testing.T) {
	for _, class := range []string{"A", "B", "C"} {
		s, ok := Spec("AM", class)
		if !ok {
			t.Fatalf("classe AM %s ausente", class)
		}
		if s.CoverageKm != 0 {
			t.Errorf("classe AM %s tem CoverageKm=%v; OM não tem cobertura definida em distância", class, s.CoverageKm)
		}
		if _, ok := CoverageKm("AM", class); ok {
			t.Errorf("CoverageKm(AM,%s) devolveu ok=true; deveria sinalizar indeterminado", class)
		}
	}
}

func TestRadCom(t *testing.T) {
	s, ok := Spec("FM", ClassRadCom)
	if !ok {
		t.Fatal("RADCOM não reconhecida")
	}
	// Lei 9.612/98: 25 W ERP, 30 m, raio máximo de 1 km.
	if s.MaxERPkW != 0.025 || s.RefHeightM != 30 || s.CoverageKm != 1.0 {
		t.Errorf("RADCOM = %+v, esperado 0.025 kW / 30 m / 1.0 km", s)
	}
	km, ok := CoverageKm("FM", ClassRadCom)
	if !ok || km != 1.0 {
		t.Errorf("CoverageKm(RADCOM) = %v,%v; esperado 1.0,true", km, ok)
	}
}

func TestSpecUnknownAndNormalization(t *testing.T) {
	if _, ok := Spec("FM", "Z9"); ok {
		t.Error("classe inexistente devolveu ok=true")
	}
	if _, ok := Spec("TV", "A1"); ok {
		t.Error("banda inexistente devolveu ok=true")
	}
	// classe C existe em FM e em AM, com semânticas diferentes
	if km, ok := CoverageKm("FM", " c "); !ok || km != 7.5 {
		t.Errorf("normalização falhou: CoverageKm(FM,' c ') = %v,%v", km, ok)
	}
	if _, ok := CoverageKm("AM", "C"); ok {
		t.Error("CoverageKm(AM,C) não pode devolver raio")
	}
}

// TestReachKmAppliesTransbordo trava o raio de alcance (contorno + transbordo)
// contra a tabela do E-radios (signalads-frontend/src/pages/Map/index.js), que
// aplica o mesmo +50% sobre a mesma tabela da Res. 546/2010. Se os dois
// sistemas divergirem, a mesma emissora passa a ter dois alcances diferentes.
func TestReachKmAppliesTransbordo(t *testing.T) {
	// metros, como estão no E-radios: base * 1.5
	want := map[string]float64{
		"E1": 78500 * 1.5, "E2": 67500 * 1.5, "E3": 54500 * 1.5,
		"A1": 38500 * 1.5, "A2": 35000 * 1.5, "A3": 30000 * 1.5, "A4": 24000 * 1.5,
		"B1": 16500 * 1.5, "B2": 12500 * 1.5, "C": 7500 * 1.5,
	}
	for class, meters := range want {
		got, ok := ReachKm("FM", class)
		if !ok {
			t.Fatalf("classe %s sem alcance", class)
		}
		if math.Abs(got-meters/1000) > 0.001 {
			t.Errorf("classe %s: alcance %.3f km, E-radios usa %.3f km", class, got, meters/1000)
		}
	}
}

// TestReachAlwaysExceedsCoverage: o transbordo só amplia, nunca encolhe.
func TestReachAlwaysExceedsCoverage(t *testing.T) {
	if TransbordoFactor <= 1 {
		t.Fatalf("TransbordoFactor=%v; abaixo de 1 encolheria a cobertura", TransbordoFactor)
	}
	for class := range fmClasses {
		cov, _ := CoverageKm("FM", class)
		reach, ok := ReachKm("FM", class)
		if !ok || reach <= cov {
			t.Errorf("classe %s: alcance %v não supera o contorno %v", class, reach, cov)
		}
	}
	// RadCom: 1 km de contorno legal → 1,5 km de alcance.
	if reach, ok := ReachKm("FM", ClassRadCom); !ok || math.Abs(reach-1.5) > 0.001 {
		t.Errorf("RADCOM: alcance %v, esperado 1.5", reach)
	}
}

// TestReachUndefinedForAM: sem contorno em distância não há transbordo a
// calcular — não vale multiplicar zero por 1,5 e devolver "0 km de alcance".
func TestReachUndefinedForAM(t *testing.T) {
	for _, class := range []string{"A", "B", "C"} {
		if km, ok := ReachKm("AM", class); ok {
			t.Errorf("ReachKm(AM,%s)=%v,true; OM não tem raio", class, km)
		}
	}
}
