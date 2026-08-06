package match

import (
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/metrics"
)

// State represents the detection phase.
type State int

const (
	StateIdle      State = iota
	StateDetecting State = iota
	// StateUncertain is entered from Detecting when coverage is in an
	// ambiguous zone (borderline coverage, or high score with low coverage).
	// It awaits neural verification via ResolveNeural() to either confirm or
	// drop the detection. Tick() expires the state if no resolution arrives.
	StateUncertain State = iota
	StateCooldown  State = iota
)

// ConfirmedDetection is emitted when the state machine confirms a detection.
//
// HashCount is the cumulative UniqueScore credited across windows during the
// Detecting phase (sum of unique hashes hits that drove the confirmation).
// TemporalCoverage is the same value as Confidence for now (both come from
// CoverageWindow.Coverage()) but is exposed separately so downstream consumers
// can persist them in their schema slots without code re-using one as the
// other. FirstOffsetFrames is the alignment offset captured when the state
// machine first transitioned Idle → Detecting; OffsetFrames carries the offset
// at confirmation. VariantID/RateID record which fingerprint variant produced
// the confirming match (always 0/0 today; reserved for §9.7 multi-rate).
type ConfirmedDetection struct {
	CommercialShortID int32
	StationID         string // passed in at construction
	DetectedAt        time.Time
	FirstMatchAt      time.Time // when the detecting phase started (≈ commercial start)
	OffsetFrames      int
	FirstOffsetFrames int
	Confidence        float64 // coverage at confirmation time
	TemporalCoverage  float64
	HashCount         int
	VariantID         uint8
	RateID            uint8
}

// StateMachine tracks detection state for one commercial on one station.
type StateMachine struct {
	stationID         string
	commercialShortID int32
	state             State
	coverage          *CoverageWindow
	firstMatchAt      time.Time
	log               *zap.Logger

	// StateDetecting tracking
	detectingWindows  int
	cumulativeHashes  int   // sum of UniqueScore credited while Detecting
	firstOffsetFrames int   // OffsetFrames at Idle → Detecting transition
	lastVariantID     uint8 // VariantID of the most recent confirming hit
	lastRateID        uint8 // RateID of the most recent confirming hit

	// StateUncertain tracking (fase2 neural verification)
	uncertainWindows      int
	uncertainOffsetFrames int

	// Configuration
	minScore            int           // minimum MatchResult.Score to count as a hit
	minTemporalCoverage float64       // minimum Coverage() (time elapsed / commercial duration) to confirm
	confirmTimeout      time.Duration // max time in Detecting before reset (no confirm)
	cooldownDuration    time.Duration
	cooldownUntil       time.Time
	totalFrames         int // duração do comercial em frames (p/ heurística de re-veiculação)

	// cooldownReairSeen evita logar a mesma re-veiculação suspeita várias
	// vezes dentro de um único período de cooldown (a métrica conta todas).
	cooldownReairSeen bool

	// shortSingleWindowFactor > 0 liga a confirmação em UMA janela para
	// material curto (< shortMaterialMaxSeconds). 0 = desligado, que é o
	// comportamento histórico. Ver EnableShortSingleWindow.
	shortSingleWindowFactor float64
}

// shortMaterialMaxSeconds — acima disto o material tem janelas de análise de
// sobra e a regra de janela única não se aplica. Alinhado com o
// MinShareableDurationSeconds do sharing: <10s é a classe estruturalmente
// frágil (fora da defesa de shared-hash e com 1-2 janelas úteis só).
const shortMaterialMaxSeconds = 10.0

// EnableShortSingleWindow liga a confirmação em UMA janela para material curto.
//
// Motivação (incidente 2026-07-24): um material de 5,7s tem no máximo 1-2
// janelas úteis (janela 4s, hop 2s) e a state machine só confirma na SEGUNDA
// janela qualificada. Em stream comprimido as janelas parciais desabam abaixo
// do gate, sobra uma, e a veiculação some sem deixar row nem log. Varredura de
// fase sobre as censuras reais: a regra de 2 janelas salva 12-37% dos
// alinhamentos possíveis; com esta regra, 75-87%.
//
// factor é o multiplicador sobre minScore que a janela única precisa atingir.
// Com minScore=19 (calibrado em prod) e factor 2.5 o piso fica em 48, contra
// um ruído máximo medido de 12-13 — ~3,7× de margem. O audit §9.9 continua
// como segunda barreira: re-checa o clipe contra o master antes de publicar.
func (sm *StateMachine) EnableShortSingleWindow(factor float64) {
	sm.shortSingleWindowFactor = factor
}

// confirm monta a ConfirmedDetection, arma o cooldown e zera o estado. `path`
// identifica qual caminho confirmou (two_windows | short_single_window) e serve
// pra separar os dois na sombra do rollout.
func (sm *StateMachine) confirm(result MatchResult, now time.Time, path string) *ConfirmedDetection {
	confidence := sm.coverage.Coverage()
	detection := &ConfirmedDetection{
		CommercialShortID: sm.commercialShortID,
		StationID:         sm.stationID,
		DetectedAt:        now,
		FirstMatchAt:      sm.firstMatchAt,
		OffsetFrames:      result.OffsetFrames,
		FirstOffsetFrames: sm.firstOffsetFrames,
		Confidence:        confidence,
		TemporalCoverage:  confidence,
		HashCount:         sm.cumulativeHashes,
		VariantID:         sm.lastVariantID,
		RateID:            sm.lastRateID,
	}
	sm.log.Info("detection confirmed",
		zap.String("stationID", sm.stationID),
		zap.Int32("commercialShortID", sm.commercialShortID),
		zap.Float64("confidence", confidence),
		zap.Int("hashCount", sm.cumulativeHashes),
		zap.String("path", path),
	)
	sm.coverage.Reset()
	sm.detectingWindows = 0
	sm.cumulativeHashes = 0
	sm.state = StateCooldown
	sm.cooldownUntil = now.Add(sm.cooldownDuration)
	sm.cooldownReairSeen = false
	return detection
}

// shortSingleWindowConfirms decide se esta única janela já basta para confirmar.
func (sm *StateMachine) shortSingleWindowConfirms(uniqueScore int) bool {
	if sm.shortSingleWindowFactor <= 0 {
		return false
	}
	if float64(sm.totalFrames)/framesPerSecond >= shortMaterialMaxSeconds {
		return false
	}
	return float64(uniqueScore) >= sm.shortSingleWindowFactor*float64(sm.minScore)
}

// NewStateMachine creates a new StateMachine for tracking one commercial on one station.
// minTemporalCoverage is the fraction of the commercial's duration that must
// elapse between the first and most-recent sustained match before a detection
// is confirmed. This is the primary false-positive defense: random audio cannot
// sustain delta-aligned hits over a meaningful fraction of a commercial.
func NewStateMachine(
	stationID string,
	commercialShortID int32,
	totalFrames int,
	minScore int,
	minTemporalCoverage float64,
	confirmTimeout time.Duration,
	cooldownDuration time.Duration,
	log *zap.Logger,
) *StateMachine {
	return &StateMachine{
		stationID:           stationID,
		commercialShortID:   commercialShortID,
		state:               StateIdle,
		coverage:            NewCoverageWindow(totalFrames),
		minScore:            minScore,
		minTemporalCoverage: minTemporalCoverage,
		confirmTimeout:      confirmTimeout,
		cooldownDuration:    cooldownDuration,
		totalFrames:         totalFrames,
		log:                 log,
	}
}

// isPossibleReair detecta a assinatura de uma re-veiculação engolida pelo
// cooldown (T8-A, plano de remediação 2026-06-12). Matches em cooldown são
// esperados para a cauda da veiculação recém-confirmada — esses têm offset
// CRESCENTE (partes finais do comercial). Uma NOVA veiculação aparece como
// offset no primeiro quarto do comercial chegando na metade FINAL do
// cooldown. Instrumentação apenas: a Onda 2 decide se vale re-armar.
func (sm *StateMachine) isPossibleReair(result MatchResult, now time.Time) bool {
	if result.UniqueScore < sm.minScore {
		return false
	}
	if result.OffsetFrames >= sm.totalFrames/4 {
		return false // continuação da veiculação atual, não reinício
	}
	cooldownMidpoint := sm.cooldownUntil.Add(-sm.cooldownDuration / 2)
	return now.After(cooldownMidpoint)
}

// Update processes one MatchResult for this commercial.
// Returns a *ConfirmedDetection if the state just transitioned to Confirmed,
// or nil otherwise.
// After confirmation, resets to Idle automatically.
func (sm *StateMachine) Update(result MatchResult, now time.Time) *ConfirmedDetection {
	// The state machine credits coverage strictly from UniqueScore — hits that
	// came from hashes flagged as shared with another commercial don't count.
	// This is the false-positive defense for cases like the AMB30/JINGLE
	// pair: when commercial A plays and its end-sting matches commercial B,
	// B accumulates Score from the shared hashes only. UniqueScore stays at 0
	// and B never advances out of Idle. Total Score is left for the engine's
	// own minScoreCoverage filter and for diagnostics.
	switch sm.state {
	case StateIdle:
		if result.UniqueScore >= sm.minScore {
			sm.state = StateDetecting
			sm.firstMatchAt = now
			sm.detectingWindows = 0
			sm.cumulativeHashes = result.UniqueScore
			sm.firstOffsetFrames = result.OffsetFrames
			sm.lastVariantID = result.VariantID
			sm.lastRateID = result.RateID
			sm.coverage.Add(result.OffsetFrames, now)
			sm.log.Info("detecting started",
				zap.String("stationID", sm.stationID),
				zap.Int32("commercialShortID", sm.commercialShortID),
				zap.Int("score", result.Score),
				zap.Int("uniqueScore", result.UniqueScore),
				zap.Int("offsetFrames", result.OffsetFrames),
			)

			// Material curto com score muito acima do piso: esta janela já é
			// evidência suficiente. Sem isto a tocada depende de uma SEGUNDA
			// janela qualificada que, em stream comprimido, frequentemente não
			// existe — e a veiculação se perde sem row nem log.
			if sm.shortSingleWindowConfirms(result.UniqueScore) {
				metrics.MatchShortSingleWindow.Inc()
				return sm.confirm(result, now, "short_single_window")
			}
		}

	case StateDetecting:
		sm.detectingWindows++
		if result.UniqueScore >= sm.minScore {
			sm.cumulativeHashes += result.UniqueScore
			sm.lastVariantID = result.VariantID
			sm.lastRateID = result.RateID
			sm.coverage.Add(result.OffsetFrames, now)
			if sm.coverage.Coverage() >= sm.minTemporalCoverage {
				return sm.confirm(result, now, "two_windows")
			}

			// Transition to StateUncertain if coverage is in the ambiguous zone.
			// Two paths: (1) borderline coverage ≥0.4 but below threshold, or
			// (2) high score (3× threshold) with low coverage ≥0.2. The latter catches
			// noise-disrupted matches where the neural verifier can disambiguate.
			// Requires at least 3 windows processed.
			cov := sm.coverage.Coverage()
			highScore := result.UniqueScore >= 3*sm.minScore
			covPath := cov >= 0.4 && cov < sm.minTemporalCoverage
			scorePath := highScore && cov >= 0.2 && cov < sm.minTemporalCoverage
			if sm.detectingWindows >= 3 && (covPath || scorePath) {
				sm.state = StateUncertain
				sm.uncertainWindows = 0
				sm.uncertainOffsetFrames = result.OffsetFrames
				sm.log.Info("detection uncertain, awaiting neural resolution",
					zap.String("stationID", sm.stationID),
					zap.Int32("commercialShortID", sm.commercialShortID),
					zap.Float64("coverage", cov),
					zap.Int("score", result.Score),
				)
				return nil
			}
		}

	case StateUncertain:
		// Expiry is handled by Tick(); Update() only processes neural resolution
		// via ResolveNeural(). Discard any non-neural match results here.
		return nil

	case StateCooldown:
		// Matches during cooldown are discarded to prevent duplicate detections.
		// Instrumentação T8-A: conta o padrão de re-veiculação engolida (mesma
		// faixa 2× no mesmo break). Métrica decide se a Onda 2 implementa
		// re-arm; o log sai uma vez por cooldown pra não inundar.
		if sm.isPossibleReair(result, now) {
			metrics.CooldownPossibleReairTotal.WithLabelValues(sm.stationID).Inc()
			if !sm.cooldownReairSeen {
				sm.cooldownReairSeen = true
				sm.log.Info("possible re-airing swallowed by cooldown",
					zap.String("stationID", sm.stationID),
					zap.Int32("commercialShortID", sm.commercialShortID),
					zap.Int("offsetFrames", result.OffsetFrames),
					zap.Int("uniqueScore", result.UniqueScore),
					zap.Time("cooldownUntil", sm.cooldownUntil),
				)
			}
		}
	}

	return nil
}

// Tick checks if the detecting phase has timed out, cooldown has expired,
// or if StateUncertain has lingered too long without neural resolution.
// Call once per window.
func (sm *StateMachine) Tick(now time.Time) {
	switch sm.state {
	case StateDetecting:
		if now.Sub(sm.firstMatchAt) > sm.confirmTimeout {
			sm.log.Info("detection timed out, resetting to idle",
				zap.String("stationID", sm.stationID),
				zap.Int32("commercialShortID", sm.commercialShortID),
			)
			sm.coverage.Reset()
			sm.detectingWindows = 0
			sm.cumulativeHashes = 0
			sm.state = StateIdle
		}
	case StateUncertain:
		sm.uncertainWindows++
		if sm.uncertainWindows >= 3 {
			sm.log.Info("uncertain detection expired without neural resolution",
				zap.String("stationID", sm.stationID),
				zap.Int32("commercialShortID", sm.commercialShortID),
			)
			sm.coverage.Reset()
			sm.cumulativeHashes = 0
			sm.state = StateIdle
		}
	case StateCooldown:
		if now.After(sm.cooldownUntil) {
			sm.log.Info("cooldown expired, back to idle",
				zap.String("stationID", sm.stationID),
				zap.Int32("commercialShortID", sm.commercialShortID),
			)
			sm.state = StateIdle
		}
	}
}

// State returns the current state.
func (sm *StateMachine) State() State {
	return sm.state
}

// IsUncertain returns true when the state machine is in the StateUncertain state,
// waiting for neural verification to decide the detection.
func (sm *StateMachine) IsUncertain() bool { return sm.state == StateUncertain }

// UncertainOffset returns the OffsetFrames recorded when the machine entered StateUncertain.
func (sm *StateMachine) UncertainOffset() int { return sm.uncertainOffsetFrames }

// ResolveNeural resolves an uncertain detection based on neural cosine similarity score.
// Returns a *ConfirmedDetection if similarity >= 0.85, otherwise resets to Idle.
// If called outside StateUncertain, returns nil without side effects.
func (sm *StateMachine) ResolveNeural(similarity float64, now time.Time) *ConfirmedDetection {
	if sm.state != StateUncertain {
		return nil
	}
	if similarity >= 0.85 {
		confidence := sm.coverage.Coverage()
		detection := &ConfirmedDetection{
			CommercialShortID: sm.commercialShortID,
			StationID:         sm.stationID,
			DetectedAt:        now,
			FirstMatchAt:      sm.firstMatchAt,
			OffsetFrames:      sm.uncertainOffsetFrames,
			FirstOffsetFrames: sm.firstOffsetFrames,
			Confidence:        confidence,
			TemporalCoverage:  confidence,
			HashCount:         sm.cumulativeHashes,
			VariantID:         sm.lastVariantID,
			RateID:            sm.lastRateID,
		}
		sm.log.Info("uncertain detection confirmed via neural",
			zap.String("stationID", sm.stationID),
			zap.Int32("commercialShortID", sm.commercialShortID),
			zap.Float64("similarity", similarity),
			zap.Float64("confidence", confidence),
			zap.Int("hashCount", sm.cumulativeHashes),
		)
		sm.coverage.Reset()
		sm.detectingWindows = 0
		sm.cumulativeHashes = 0
		sm.uncertainWindows = 0
		sm.state = StateCooldown
		sm.cooldownUntil = now.Add(sm.cooldownDuration)
		return detection
	}
	sm.log.Info("uncertain detection rejected by neural",
		zap.String("stationID", sm.stationID),
		zap.Int32("commercialShortID", sm.commercialShortID),
		zap.Float64("similarity", similarity),
	)
	sm.coverage.Reset()
	sm.detectingWindows = 0
	sm.cumulativeHashes = 0
	sm.uncertainWindows = 0
	sm.state = StateIdle
	return nil
}
