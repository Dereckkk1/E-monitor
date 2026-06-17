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

// Coberturas MEDIDAS no áudio real (audio-refs/), pelo audit Go de prod
// (cmd/audit-extent, runMatch) em 2026-06-17. Cada censura real do ASAAS foi
// re-fingerprintada contra os DOIS masters (78 = PLATAFORMA 30s, 77 = SPOT 15s).
// Os nomes dos arquivos estão TROCADOS (confirmado no teste cego): censura_15s
// é a veiculação de 30s; censura_30s é a de 15s. Por isso a asserção usa o corte
// REAL, não o nome. Margem medida ~4-5× — bem acima de coverageMargin (1.5).
// Cross-check: o diag Python multi-variante deu 0.597/0.147 e 0.106/0.707
// (mesma direção, mesma margem). Este teste trava o sinal contra regressão.
func TestChooseByCoverage_RealASAASAuditMeasurements(t *testing.T) {
	// Veiculação real de 30s (arquivo censura_15s.mp3): cobre 78 a 0.600, 77 a 0.138.
	if got := chooseByCoverage(
		CutCoverage{ShortID: 78, DurationSeconds: 30, Coverage: 0.600},
		CutCoverage{ShortID: 77, DurationSeconds: 15, Coverage: 0.138},
	); got != 78 {
		t.Fatalf("30s airing: winner=%d want 78 (covers 0.600 vs 0.138)", got)
	}
	// Veiculação real de 15s (arquivo censura_30s.mp3): cobre 78 a 0.132, 77 a 0.664.
	if got := chooseByCoverage(
		CutCoverage{ShortID: 78, DurationSeconds: 30, Coverage: 0.132},
		CutCoverage{ShortID: 77, DurationSeconds: 15, Coverage: 0.664},
	); got != 77 {
		t.Fatalf("15s airing: winner=%d want 77 (covers 0.664 vs 0.132)", got)
	}
}
