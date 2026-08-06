package match

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Confirmação em UMA janela para material curto (incidente 2026-07-24).
//
// Um material de 5,7s tem no máximo 1-2 janelas de análise úteis (janela 4s,
// hop 2s), e a state machine só confirma na SEGUNDA janela qualificada. Quando
// o stream é comprimido, as janelas parciais desabam abaixo do gate e sobra
// UMA — a veiculação some sem deixar rastro.
//
// Varredura de fase medida com as censuras reais (audio-refs, 2026-08-06):
// a regra atual salva 12-37% dos alinhamentos possíveis; exigindo score alto
// numa única janela, sobe para 75-87%. Ver docs/features/short-material-single-window.md.
//
// O piso é o multiplicador sobre minScore. Com minScore=19 (calibrado em prod)
// e fator 2.5 → 48, contra um ruído máximo medido de 12-13. O audit §9.9 segue
// como segunda barreira: re-checa o clipe contra o master antes de publicar.

const shortFrames = 43 // 5,7s a 7.8125 frames/s — o PULSO MILIUM
const longFrames = 234 // ~30s — um spot comum

func TestShortSingleWindow_ConfirmsOnFirstStrongWindow(t *testing.T) {
	sm := newTestStateMachine(shortFrames, 19, 0.15)
	sm.EnableShortSingleWindow(2.5) // piso = 48

	// Score 76 — o medido na censura WhatsApp, que hoje se perde.
	det := sm.Update(MatchResult{UniqueScore: 76, Score: 76, OffsetFrames: 4}, time.Now())

	require.NotNil(t, det, "material curto com score muito alto deve confirmar em 1 janela")
	assert.Equal(t, int32(42), det.CommercialShortID)
	assert.Equal(t, 76, det.HashCount)
	assert.Greater(t, det.Confidence, 0.0)
}

func TestShortSingleWindow_WaitsWhenScoreIsOrdinary(t *testing.T) {
	sm := newTestStateMachine(shortFrames, 19, 0.15)
	sm.EnableShortSingleWindow(2.5) // piso = 48

	// 33 passa o minScore (19) mas não chega no piso da regra — comportamento
	// antigo: entra em Detecting e espera a 2ª janela.
	det := sm.Update(MatchResult{UniqueScore: 33, Score: 33, OffsetFrames: 4}, time.Now())
	assert.Nil(t, det, "score comum não pode confirmar em 1 janela")
	assert.Equal(t, StateDetecting, sm.State())
}

func TestShortSingleWindow_IgnoresLongMaterial(t *testing.T) {
	sm := newTestStateMachine(longFrames, 19, 0.15)
	sm.EnableShortSingleWindow(2.5)

	// Spot de 30s tem janelas de sobra; a regra NÃO se aplica a ele.
	det := sm.Update(MatchResult{UniqueScore: 300, Score: 300, OffsetFrames: 4}, time.Now())
	assert.Nil(t, det, "material longo deve seguir exigindo 2 janelas")
	assert.Equal(t, StateDetecting, sm.State())
}

func TestShortSingleWindow_DisabledByDefault(t *testing.T) {
	sm := newTestStateMachine(shortFrames, 19, 0.15)
	// sem EnableShortSingleWindow → flag OFF, comportamento idêntico ao de hoje

	det := sm.Update(MatchResult{UniqueScore: 76, Score: 76, OffsetFrames: 4}, time.Now())
	assert.Nil(t, det, "com a flag desligada nada pode mudar")
	assert.Equal(t, StateDetecting, sm.State())
}

func TestShortSingleWindow_GoesToCooldownAfterConfirming(t *testing.T) {
	sm := newTestStateMachine(shortFrames, 19, 0.15)
	sm.EnableShortSingleWindow(2.5)
	now := time.Now()

	det := sm.Update(MatchResult{UniqueScore: 76, Score: 76, OffsetFrames: 4}, now)
	require.NotNil(t, det)
	assert.Equal(t, StateCooldown, sm.State(), "confirmação tem que armar o cooldown")

	// A cauda da mesma veiculação não pode gerar segunda detecção.
	det2 := sm.Update(MatchResult{UniqueScore: 76, Score: 76, OffsetFrames: 20}, now.Add(time.Second))
	assert.Nil(t, det2, "cooldown deve engolir a cauda da mesma tocada")
}
