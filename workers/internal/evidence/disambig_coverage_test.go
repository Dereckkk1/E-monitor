package evidence

import "testing"

// 15s airing: 77 cobre 0.70, 78 cobre 0.15 → 77 (mais coberto) ganha, apesar de
// ser o corte mais curto. É o caso que hoje a desambiguação por duração erra.
func TestChooseByCoverage_ShorterCutWinsWhenItCoversMore(t *testing.T) {
	got := chooseByCoverage(
		CutCoverage{ShortID: 78, DurationSeconds: 30, Coverage: 0.15},
		CutCoverage{ShortID: 77, DurationSeconds: 15, Coverage: 0.70},
	)
	if got != 77 {
		t.Fatalf("winner=%d want 77 (covers more)", got)
	}
}

// 30s airing real: 78 cobre 0.60, 77 cobre 0.15 → 78 ganha (mais coberto E mais longo).
func TestChooseByCoverage_LongerCutWinsWhenItCoversMore(t *testing.T) {
	got := chooseByCoverage(
		CutCoverage{ShortID: 78, DurationSeconds: 30, Coverage: 0.60},
		CutCoverage{ShortID: 77, DurationSeconds: 15, Coverage: 0.15},
	)
	if got != 78 {
		t.Fatalf("winner=%d want 78 (covers more)", got)
	}
}

// Coberturas próximas (dentro da margem) → falha-segura pro comportamento atual:
// o corte mais longo ganha. Evita trocar atribuição num quase-empate.
func TestChooseByCoverage_TieWithinMarginFallsBackToDuration(t *testing.T) {
	got := chooseByCoverage(
		CutCoverage{ShortID: 77, DurationSeconds: 15, Coverage: 0.50},
		CutCoverage{ShortID: 78, DurationSeconds: 30, Coverage: 0.48},
	)
	if got != 78 {
		t.Fatalf("winner=%d want 78 (tie → longer)", got)
	}
}

// Empate de cobertura E de duração → desempate estável pelo menor short_id
// (preserva §18.2.2 original).
func TestChooseByCoverage_ExactTieFallsBackToSmallerShortID(t *testing.T) {
	got := chooseByCoverage(
		CutCoverage{ShortID: 91, DurationSeconds: 30, Coverage: 0.50},
		CutCoverage{ShortID: 90, DurationSeconds: 30, Coverage: 0.50},
	)
	if got != 90 {
		t.Fatalf("winner=%d want 90 (exact tie → smaller short_id)", got)
	}
}

// Irmão com cobertura medida 0 (clipe não contém aquele corte) perde pro corte
// que de fato foi coberto, independentemente da duração.
func TestChooseByCoverage_ZeroCoverageSiblingLoses(t *testing.T) {
	got := chooseByCoverage(
		CutCoverage{ShortID: 78, DurationSeconds: 30, Coverage: 0.0},
		CutCoverage{ShortID: 77, DurationSeconds: 15, Coverage: 0.40},
	)
	if got != 77 {
		t.Fatalf("winner=%d want 77 (sibling with 0 coverage loses)", got)
	}
}
