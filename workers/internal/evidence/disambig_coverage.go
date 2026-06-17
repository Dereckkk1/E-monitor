package evidence

// disambig_coverage decide qual CORTE (15s vs 30s do mesmo cliente) realmente
// tocou, usando a cobertura do clipe de evidência contra cada master — não a
// duração. Veja §18.2.2 e o plano version-disambiguation-by-coverage.
//
// Contexto provado (teste cego, 2026-06-16/17): quando o 15s toca, ele cobre o
// pedaço compartilhado do master de 30s o suficiente pro state machine do 30s
// também confirmar. A duração ("o mais longo ganha") então atribui errado. Mas a
// cobertura do CLIPE separa limpo (4-5×): o clipe de 15s cobre ~0.70 do master de
// 15s e só ~0.15 do de 30s; o de 30s, ~0.60 do de 30s e ~0.15 do de 15s.

// CutCoverage descreve um corte candidato e a cobertura do clipe de evidência
// contra o master DESTE corte (medida pelo §9.9 audit).
type CutCoverage struct {
	ShortID         int32
	DurationSeconds int
	Coverage        float64 // cobertura do clipe contra ESTE master (0..1)
}

// coverageMargin: o corte de MAIOR cobertura só sobrepõe o critério de duração se
// cobrir pelo menos esta fração a mais que o outro. Abaixo disso (quase-empate,
// ex.: um 30s real que cobre os dois quase igual) cai pro mais longo, como antes —
// falha-segura pro comportamento §18.2.2 original. Folgado vs a margem provada
// (4-5×); 1.5 é seguro e tunável.
const coverageMargin = 1.5

// chooseByCoverage devolve o short_id do corte que tocou: o de MAIOR cobertura do
// clipe, se a vantagem passar de coverageMargin; senão, o de maior duração
// (desempate estável pelo menor short_id), preservando §18.2.2 original.
func chooseByCoverage(a, b CutCoverage) int32 {
	hi, lo := a, b
	if b.Coverage > a.Coverage {
		hi, lo = b, a
	}
	if lo.Coverage <= 0 || hi.Coverage >= lo.Coverage*coverageMargin {
		return hi.ShortID
	}
	// Quase-empate → duração (e menor short_id no empate exato).
	if a.DurationSeconds != b.DurationSeconds {
		if a.DurationSeconds > b.DurationSeconds {
			return a.ShortID
		}
		return b.ShortID
	}
	if a.ShortID < b.ShortID {
		return a.ShortID
	}
	return b.ShortID
}
