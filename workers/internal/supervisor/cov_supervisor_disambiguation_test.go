package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/db"
	"radiocheck/internal/events"
	"radiocheck/internal/ingestor"
	"radiocheck/internal/match"
	"radiocheck/internal/metrics"
)

// covSDisambScene é o cenário base do §18.2.2: uma emissora, uma campanha ativa
// e materiais prontos de durações diferentes do MESMO cliente.
type covSDisambScene struct {
	*covSFix
	station uuid.UUID
	camp    uuid.UUID
	t0      time.Time
}

func covSNewDisambScene(t *testing.T) *covSDisambScene {
	t.Helper()
	f := covSNewFix(t)
	st := f.station()
	camp := f.campaign("ativa", -1, 30, st)
	return &covSDisambScene{
		covSFix: f, station: st, camp: camp,
		t0: time.Date(2026, 6, 9, 12, 2, 22, 0, time.UTC),
	}
}

// mat cria um material pronto de `dur` segundos, devolvendo (id, short_id).
func (sc *covSDisambScene) mat(dur float64) (uuid.UUID, int32) {
	return sc.material(sc.camp, dur, sc.station)
}

// detect monta a confirmação do worker: broadcastStart = at (a janela de
// evidência começa 60s antes), com a confiança dada.
func (sc *covSDisambScene) detect(short int32, at time.Time, conf float64) (match.ConfirmedDetection, ingestor.DetectionEvent) {
	ev := ingestor.DetectionEvent{
		StationID:           sc.station.String(),
		CommercialShortID:   short,
		DetectedAt:          at.UTC().Format(time.RFC3339),
		Confidence:          conf,
		EvidenceWindowStart: at.Add(-60 * time.Second).UTC().Format(time.RFC3339),
		EvidenceWindowEnd:   at.Add(60 * time.Second).UTC().Format(time.RFC3339),
		HashCount:           42,
	}
	det := match.ConfirmedDetection{
		CommercialShortID: short, StationID: sc.station.String(),
		DetectedAt: at, FirstMatchAt: at, Confidence: conf,
	}
	return det, ev
}

func (sc *covSDisambScene) submit(short int32, at time.Time, conf float64) {
	det, ev := sc.detect(short, at, conf)
	sc.s.SubmitDetection(sc.ctx, det, ev)
}

// confirmed / retracted devolvem o que chegou ao barramento (após flush).
func (sc *covSDisambScene) confirmed() []ingestor.DetectionEvent {
	sc.t.Helper()
	require.NoError(sc.t, sc.nc.Flush())
	var out []ingestor.DetectionEvent
	for _, m := range sc.srv.Messages(events.SubjectDetectionConfirmed) {
		var ev ingestor.DetectionEvent
		require.NoError(sc.t, json.Unmarshal(m.Data, &ev))
		out = append(out, ev)
	}
	return out
}

func (sc *covSDisambScene) retracted() []RetractedEvent {
	sc.t.Helper()
	require.NoError(sc.t, sc.nc.Flush())
	var out []RetractedEvent
	for _, m := range sc.srv.Messages(events.SubjectDetectionRetracted) {
		var ev RetractedEvent
		require.NoError(sc.t, json.Unmarshal(m.Data, &ev))
		out = append(out, ev)
	}
	return out
}

// seedDetection grava a linha que a evidência escreveria para a tocada.
func (sc *covSDisambScene) seedDetection(matID uuid.UUID, at time.Time) {
	sc.t.Helper()
	_, err := sc.pool.Exec(sc.ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used, category)
		VALUES ($1, $2, $3, $4, 0, 15000, 0.9, 50, 0.5, 0, 0, 'in_slot')`,
		sc.station, matID, sc.camp, at)
	require.NoError(sc.t, err)
}

func (sc *covSDisambScene) retractedAt(at time.Time) *time.Time {
	sc.t.Helper()
	var ts *time.Time
	require.NoError(sc.t, sc.pool.QueryRow(sc.ctx,
		`SELECT retracted_at FROM detections WHERE station_id=$1 AND detected_at=$2`, sc.station, at).Scan(&ts))
	return ts
}

func covSCounter(label string) float64 {
	return testutil.ToFloat64(metrics.MatchDisambiguation.WithLabelValues(label))
}

// Sem conflito: publica verbatim em detections.confirmed e guarda no buffer.
func TestCovS_SubmitDetection_NoConflict_PublishesVerbatim(t *testing.T) {
	sc := covSNewDisambScene(t)
	_, short30 := sc.mat(30)
	det, ev := sc.detect(short30, sc.t0, 0.9)

	sc.s.SubmitDetection(sc.ctx, det, ev)

	got := sc.confirmed()
	require.Equal(t, []ingestor.DetectionEvent{ev}, got, "o evento do worker é republicado sem alteração")
	require.Equal(t, 1, sc.s.dedupBuffer.Len())
	require.Empty(t, sc.retracted())
}

// Corte mais longo da mesma veiculação: retrata o curto (linha + evento) e
// publica o longo.
func TestCovS_SubmitDetection_LongerCutRetractsShorterAndPublishes(t *testing.T) {
	sc := covSNewDisambScene(t)
	mat30, short30 := sc.mat(30)
	_, short60 := sc.mat(60)
	sc.seedDetection(mat30, sc.t0)
	before := covSCounter("retracted")

	sc.submit(short30, sc.t0, 0.9)
	sc.submit(short60, sc.t0.Add(3*time.Second), 0.95)

	conf := sc.confirmed()
	require.Len(t, conf, 2)
	require.Equal(t, short30, conf[0].CommercialShortID)
	require.Equal(t, short60, conf[1].CommercialShortID)
	ret := sc.retracted()
	require.Len(t, ret, 1)
	require.Equal(t, sc.station.String(), ret[0].StationID)
	require.Equal(t, short30, ret[0].CommercialShortID)
	require.Equal(t, sc.t0.Format(time.RFC3339), ret[0].DetectedAt)
	require.Equal(t, "longer_cut_detected", ret[0].Reason)
	require.InDelta(t, 0.9, ret[0].Confidence, 1e-9)
	_, perr := time.Parse(time.RFC3339, ret[0].RetractedAt)
	require.NoError(t, perr)
	require.NotNil(t, sc.retractedAt(sc.t0), "a linha da tocada curta recebe retracted_at")
	require.Equal(t, 1, sc.s.dedupBuffer.Len(), "o buffer passa a guardar só o corte longo")
	require.Equal(t, before+1, covSCounter("retracted"))
}

// Corte mais curto depois do longo: suprimido (nada publicado) e a supressão
// fica gravada na tabela forense com as duas confianças.
func TestCovS_SubmitDetection_ShorterCutIsSuppressedAndRecorded(t *testing.T) {
	sc := covSNewDisambScene(t)
	_, short60 := sc.mat(60)
	_, short15 := sc.mat(15)
	beforeSup, beforeSuspect := covSCounter("suppressed"), covSCounter("suppressed_suspect")

	sc.submit(short60, sc.t0, 0.80)
	sc.submit(short15, sc.t0.Add(5*time.Second), 0.70) // quase empate: não é suspeito

	require.Len(t, sc.confirmed(), 1, "só o corte longo é publicado")
	require.Empty(t, sc.retracted())
	var supShort, keptShort, supDur, keptDur int
	var reason string
	var supConf, keptConf float64
	require.NoError(t, sc.pool.QueryRow(sc.ctx, `
		SELECT suppressed_short_id, kept_short_id, suppressed_duration, kept_duration,
		       suppressed_confidence::float8, kept_confidence::float8, reason
		FROM dedup_suppressions WHERE station_id = $1`, sc.station).
		Scan(&supShort, &keptShort, &supDur, &keptDur, &supConf, &keptConf, &reason))
	require.Equal(t, [4]int{int(short15), int(short60), 15, 60}, [4]int{supShort, keptShort, supDur, keptDur})
	require.InDelta(t, 0.70, supConf, 1e-6)
	require.InDelta(t, 0.80, keptConf, 1e-6)
	require.Equal(t, "shorter_cut", reason)
	require.Equal(t, beforeSup+1, covSCounter("suppressed"))
	require.Equal(t, beforeSuspect, covSCounter("suppressed_suspect"), "diferença < margem não é supressão suspeita")
}

// Supressão em que o corte descartado estava MUITO mais confiante que o mantido
// (provável veiculação real morta) incrementa a métrica "suspect".
func TestCovS_SubmitDetection_SuppressionWithConfidenceGapIsSuspect(t *testing.T) {
	sc := covSNewDisambScene(t)
	_, short30 := sc.mat(30)
	_, short15 := sc.mat(15)
	beforeSuspect := covSCounter("suppressed_suspect")

	sc.submit(short30, sc.t0, 0.20)
	sc.submit(short15, sc.t0.Add(2*time.Second), 0.95) // flag OFF: duração manda → suprime

	require.Len(t, sc.confirmed(), 1)
	require.Equal(t, beforeSuspect+1, covSCounter("suppressed_suspect"))
}

// Com DISAMBIG_CONFIDENCE_AWARE o corte curto de cobertura muito maior vence
// o longo que só false-confirmou a região compartilhada.
func TestCovS_SubmitDetection_ConfidenceAwareLetsStrongShortCutWin(t *testing.T) {
	sc := covSNewDisambScene(t)
	_, short30 := sc.mat(30)
	_, short15 := sc.mat(15)
	sc.s.disambigConfidenceAware = true

	sc.submit(short30, sc.t0, 0.16)
	sc.submit(short15, sc.t0.Add(2*time.Second), 0.79)

	conf := sc.confirmed()
	require.Len(t, conf, 2)
	require.Equal(t, short15, conf[1].CommercialShortID, "o corte de 15s (cobertura 0.79) é o publicado por último")
	ret := sc.retracted()
	require.Len(t, ret, 1)
	require.Equal(t, short30, ret[0].CommercialShortID, "o de 30s (cobertura 0.16) é o retratado")
}

// Empate de duração: o short_id menor vence, nos dois sentidos.
func TestCovS_SubmitDetection_EqualDurationTieBreaksByLowerShortID(t *testing.T) {
	t.Run("candidato com short_id menor retrata o maior", func(t *testing.T) {
		sc := covSNewDisambScene(t)
		_, low := sc.mat(30)
		_, high := sc.mat(30)
		require.Less(t, low, high)

		sc.submit(high, sc.t0, 0.9)
		sc.submit(low, sc.t0.Add(time.Second), 0.9)

		ret := sc.retracted()
		require.Len(t, ret, 1)
		require.Equal(t, high, ret[0].CommercialShortID)
		require.Equal(t, "tiebreak_lower_short_id", ret[0].Reason)
		require.Len(t, sc.confirmed(), 2)
	})
	t.Run("candidato com short_id maior e suprimido", func(t *testing.T) {
		sc := covSNewDisambScene(t)
		_, low := sc.mat(30)
		_, high := sc.mat(30)

		sc.submit(low, sc.t0, 0.9)
		sc.submit(high, sc.t0.Add(time.Second), 0.9)

		require.Len(t, sc.confirmed(), 1)
		require.Empty(t, sc.retracted())
		var reason string
		require.NoError(t, sc.pool.QueryRow(sc.ctx,
			`SELECT reason FROM dedup_suppressions WHERE station_id=$1`, sc.station).Scan(&reason))
		require.Equal(t, "tiebreak_lower_short_id", reason)
	})
}

// Cortes de janelas de ar que NÃO se sobrepõem são tocadas distintas: ambas publicam.
func TestCovS_SubmitDetection_NonOverlappingAirings_BothPublished(t *testing.T) {
	sc := covSNewDisambScene(t)
	_, short30 := sc.mat(30)

	sc.submit(short30, sc.t0, 0.9)
	sc.submit(short30, sc.t0.Add(10*time.Minute), 0.9)

	require.Len(t, sc.confirmed(), 2)
	require.Empty(t, sc.retracted())
	require.Equal(t, 2, sc.s.dedupBuffer.Len())
}

// Sem como resolver o cliente, degrada para o comportamento pré-§18.2.2:
// publica mesmo assim.
func TestCovS_SubmitDetection_LookupFailure_PublishesWithoutDedup(t *testing.T) {
	sc := covSNewDisambScene(t)

	sc.submit(999_999_999, sc.t0, 0.9) // short_id inexistente

	require.Len(t, sc.confirmed(), 1)
	require.Equal(t, 1, sc.logs.FilterMessage("supervisor: dedup lookup failed, publishing without dedup").Len())
	require.Equal(t, 0, sc.s.dedupBuffer.Len(), "sem info do cliente nada vai pro buffer")
}

func TestCovS_SubmitDetection_InvalidStationID_DropsWithWarning(t *testing.T) {
	sc := covSNewDisambScene(t)
	_, short30 := sc.mat(30)
	det, ev := sc.detect(short30, sc.t0, 0.9)
	det.StationID = "não-é-uuid"

	sc.s.SubmitDetection(sc.ctx, det, ev)

	require.Empty(t, sc.confirmed())
	require.Equal(t, 1, sc.logs.FilterMessage("supervisor: invalid station_id in detection").Len())
}

// retract: sem linha ainda (evidência assíncrona) o evento sai assim mesmo.
func TestCovS_Retract_NoRowYet_StillPublishesEvent(t *testing.T) {
	sc := covSNewDisambScene(t)
	_, short30 := sc.mat(30)
	det, _ := sc.detect(short30, sc.t0, 0.9)

	sc.s.retract(sc.ctx, det, "longer_cut_detected", 77)

	require.Equal(t, 1, sc.logs.FilterMessage("supervisor: retract — no rows updated (evidence write may be in flight)").Len())
	ret := sc.retracted()
	require.Len(t, ret, 1)
	require.Equal(t, short30, ret[0].CommercialShortID)
}

func TestCovS_Retract_InvalidStationID_PublishesNothing(t *testing.T) {
	sc := covSNewDisambScene(t)
	sc.s.retract(sc.ctx, match.ConfirmedDetection{StationID: "x", CommercialShortID: 1}, "r", 2)
	require.Empty(t, sc.retracted())
	require.Equal(t, 1, sc.logs.FilterMessage("supervisor: retract — invalid station_id").Len())
}

// Falha no UPDATE do banco não impede o evento (webhooks precisam saber).
func TestCovS_Retract_DBFailure_StillPublishesEvent(t *testing.T) {
	sc := covSNewDisambScene(t)
	_, short30 := sc.mat(30)
	det, _ := sc.detect(short30, sc.t0, 0.9)
	closed, err := db.New(context.Background(), os.Getenv("TEST_DATABASE_URL"), zap.NewNop())
	require.NoError(t, err)
	closed.Close()
	s2 := &Supervisor{db: closed, nc: sc.nc, log: sc.s.log}

	s2.retract(sc.ctx, det, "longer_cut_detected", 77)

	require.Equal(t, 1, sc.logs.FilterMessage("supervisor: retract — DB update failed").Len())
	require.Len(t, sc.retracted(), 1)
}

func TestCovS_MarkDetectionRetracted_QueryError(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	closed, err := db.New(context.Background(), url, zap.NewNop())
	require.NoError(t, err)
	closed.Close()
	s := &Supervisor{db: closed, log: zap.NewNop()}

	n, err := s.markDetectionRetracted(context.Background(), 1, uuid.New(), time.Now(), time.Now())

	require.Error(t, err)
	require.EqualValues(t, 0, n)
}

// Sem conexão NATS, publicar é erro logado — nunca pânico.
func TestCovS_PublishConfirmed_NilConnection_IsLoggedNotFatal(t *testing.T) {
	log, logs := covSObservedLogger()
	s := &Supervisor{log: log}

	require.NotPanics(t, func() {
		s.publishConfirmed(context.Background(), ingestor.DetectionEvent{StationID: "s", CommercialShortID: 3})
	})
	require.Equal(t, 1, logs.FilterMessage("supervisor: publish confirmed failed").Len())
}

// Ponta a ponta pelo barramento: detections.pending → desambiguação →
// detections.confirmed; mensagem inválida só gera warn; cancelar o contexto
// drena a assinatura.
func TestCovS_SubscribePendingDetections_EndToEnd(t *testing.T) {
	sc := covSNewDisambScene(t)
	_, short30 := sc.mat(30)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := sc.s.SubscribePendingDetections(ctx)
	require.NoError(t, err)
	require.NoError(t, sc.nc.Flush())
	_, ev := sc.detect(short30, sc.t0, 0.9)
	payload, err := json.Marshal(ev)
	require.NoError(t, err)

	require.NoError(t, sc.nc.Publish(events.SubjectDetectionPending, []byte("lixo"))) // JSON inválido
	require.NoError(t, sc.nc.Publish(events.SubjectDetectionPending, payload))
	require.NoError(t, sc.nc.Flush())

	sc.wait(func() bool {
		return len(sc.srv.Messages(events.SubjectDetectionConfirmed)) == 1
	}, "a detecção pendente deveria sair em detections.confirmed")
	sc.wait(func() bool {
		return sc.logs.FilterMessage("supervisor: pending detection handle failed").Len() == 1
	}, "mensagem inválida deveria gerar warn")
	require.Equal(t, []ingestor.DetectionEvent{ev}, sc.confirmed())

	cancel()
	sc.wait(func() bool { return !sub.IsValid() }, "cancelar o contexto drena e encerra a assinatura")
}

func TestCovS_SubscribePendingDetections_ClosedConnection_ReturnsError(t *testing.T) {
	srv := covSStartNATS(t)
	nc := covSConnect(t, srv)
	nc.Close()
	s := &Supervisor{nc: nc, log: zap.NewNop()}

	sub, err := s.SubscribePendingDetections(context.Background())

	require.Error(t, err)
	require.Nil(t, sub)
	require.Contains(t, err.Error(), "supervisor: subscribe pending")
}

func TestCovS_HandlePendingDetection_RoutesToSubmit(t *testing.T) {
	sc := covSNewDisambScene(t)
	_, short30 := sc.mat(30)
	_, ev := sc.detect(short30, sc.t0, 0.9)
	raw, err := json.Marshal(ev)
	require.NoError(t, err)

	require.NoError(t, sc.s.handlePendingDetection(sc.ctx, raw))

	require.Equal(t, []ingestor.DetectionEvent{ev}, sc.confirmed())
}
