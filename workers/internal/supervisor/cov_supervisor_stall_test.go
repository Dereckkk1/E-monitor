package supervisor

import (
	"context"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/ingestor"
	"radiocheck/internal/metrics"
)

// O watchdog usa um ticker fixo de 30s (const no código de produção), então este
// teste precisa esperar o primeiro tick de verdade (~31s). Duas emissoras são
// vigiadas ao mesmo tempo para pagar essa espera uma vez só:
//
//   - A: já produziu PCM e parou há 2 min (flavor A) → reinício imediato, sem backoff;
//   - B: nunca conectou desde que subiu (flavor B) → disjuntor: o worker morre, fica
//     um placeholder de backoff e o contador de falhas consecutivas sobe.
//
// Em ambos a queda é gravada ANTES de matar o worker e o contador cumulativo de
// stall restarts aparece em WorkerStatuses. Outros três vigiados provam o que o
// watchdog NÃO faz: C está saudável (PCM fresco) e D está estagnado mas dentro do
// cooldown de 2 min — nenhum dos dois é tocado — e E é uma emissora que sumiu do
// banco, cujo respawn falha e é logado.
func TestCovS_RunStallWatchdog_RestartsBothStallFlavors(t *testing.T) {
	if testing.Short() {
		t.Skip("espera o ticker de 30s do watchdog")
	}
	f := covSNewFix(t)
	stA, stB := f.station(), f.station()
	now := time.Now()

	newWatched := func(st uuid.UUID, lastPCM time.Time) (*ingestor.Worker, context.Context, context.CancelFunc) {
		w := ingestor.NewWorker(ingestor.WorkerConfig{StationID: st}, nil, nil, zap.NewNop())
		if !lastPCM.IsZero() {
			w.UpdateLastPCMAt(lastPCM)
		}
		ctx, cancel := context.WithCancel(context.Background())
		f.s.mu.Lock()
		f.s.workers[st] = &workerEntry{worker: w, cancel: cancel, startedAt: now.Add(-10 * time.Minute)}
		f.s.mu.Unlock()
		return w, ctx, cancel
	}
	// C e D são iniciados primeiro: seus ticks acontecem antes dos de A/B, então
	// quando os efeitos de A/B aparecem, C e D já passaram por um tick.
	stC, stD, stE := f.station(), f.station(), uuid.New() // E não existe no banco
	wC, ctxC, cancelC := newWatched(stC, now)
	wD, ctxD, cancelD := newWatched(stD, now.Add(-2*time.Minute))
	f.s.mu.Lock()
	f.s.lastStallRestart[stD] = now // reiniciado agora há pouco → dentro do cooldown
	f.s.mu.Unlock()
	go f.s.runStallWatchdog(ctxC, stC, wC, cancelC)
	go f.s.runStallWatchdog(ctxD, stD, wD, cancelD)
	wA, ctxA, cancelA := newWatched(stA, now.Add(-2*time.Minute))
	wB, ctxB, cancelB := newWatched(stB, time.Time{})
	wE, ctxE, cancelE := newWatched(stE, now.Add(-2*time.Minute))
	go f.s.runStallWatchdog(ctxA, stA, wA, cancelA)
	go f.s.runStallWatchdog(ctxB, stB, wB, cancelB)
	go f.s.runStallWatchdog(ctxE, stE, wE, cancelE)

	restarts := func(st uuid.UUID) uint32 {
		f.s.mu.Lock()
		defer f.s.mu.Unlock()
		return f.s.stallRestartCounts[st]
	}
	require.Eventually(t, func() bool { return restarts(stA) == 1 && restarts(stB) == 1 && restarts(stE) == 1 },
		2*time.Minute, 100*time.Millisecond, "os watchdogs estagnados deveriam disparar no primeiro tick de 30s")

	// O contador sobe ANTES de o watchdog gravar a queda e matar o worker, então
	// cada efeito é aguardado individualmente (nada de assumir a ordem).
	f.wait(func() bool { return ctxA.Err() != nil && ctxB.Err() != nil }, "os workers estagnados deveriam ser cancelados")
	f.wait(func() bool {
		f.s.mu.Lock()
		defer f.s.mu.Unlock()
		e := f.s.workers[stB]
		return e != nil && e.worker == nil
	}, "o placeholder de backoff deveria ser parqueado para B")
	f.wait(func() bool { return testutil.ToFloat64(metrics.WorkerConnectBackoff.WithLabelValues(stB.String())) > 0 },
		"o gauge de backoff de B deveria ser publicado")

	// Flavor A: worker novo no lugar do estagnado, sem backoff.
	f.wait(func() bool { w := f.worker(stA); return w != nil && w != wA }, "o watchdog deveria subir um worker novo para a emissora A")
	f.s.mu.Lock()
	require.Zero(t, f.s.connectFailures[stA], "flavor A não alimenta o disjuntor")
	f.s.mu.Unlock()

	// Flavor B: placeholder parqueado, 1 falha consecutiva, backoff de 1min±20%.
	f.s.mu.Lock()
	placeholder := f.s.workers[stB]
	failures := f.s.connectFailures[stB]
	f.s.mu.Unlock()
	require.NotNil(t, placeholder)
	require.Nil(t, placeholder.worker, "durante o backoff não há worker rodando")
	require.EqualValues(t, 1, failures)
	require.Equal(t, map[uuid.UUID]uint32{stB: 1}, f.s.BackoffStations())
	backoff := testutil.ToFloat64(metrics.WorkerConnectBackoff.WithLabelValues(stB.String()))
	require.GreaterOrEqual(t, backoff, 48.0)
	require.LessOrEqual(t, backoff, 72.0)
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.WorkerStallRestarts.WithLabelValues(stB.String())))

	// O contador cumulativo sobrevive à recriação do worker de A.
	var statusA *WorkerStatus
	for _, st := range f.s.WorkerStatuses() {
		if st.StationID == stA.String() {
			cp := st
			statusA = &cp
		}
	}
	require.NotNil(t, statusA)
	require.EqualValues(t, 1, statusA.StallRestarts)

	// Saudável (C) e em cooldown (D): nada de restart, contexto vivo, worker intacto.
	for name, c := range map[string]struct {
		st  uuid.UUID
		ctx context.Context
		w   *ingestor.Worker
	}{"saudável": {stC, ctxC, wC}, "em cooldown": {stD, ctxD, wD}} {
		require.NoError(t, c.ctx.Err(), name)
		require.Zero(t, restarts(c.st), name)
		require.Same(t, c.w, f.worker(c.st), name)
	}

	// E: o respawn falhou (emissora inexistente) e foi logado; sem worker no mapa.
	f.wait(func() bool { return f.logs.FilterMessage("supervisor: stall restart failed").Len() == 1 },
		"respawn de emissora inexistente deveria ser logado")
	require.False(t, f.hasEntry(stE))

	// A queda foi gravada nas duas emissoras (idempotente: exatamente 1 cada).
	for _, st := range []uuid.UUID{stA, stB} {
		var n int
		require.NoError(t, f.pool.QueryRow(f.ctx,
			`SELECT count(*) FROM stream_health_events WHERE station_id=$1 AND event_type='down'`, st).Scan(&n))
		require.Equal(t, 1, n, "down event gravado antes de matar o worker (%s)", st)
	}
}

// ── callbacks do worker com PCM de verdade (precisa de pipe:3 → só Linux) ───

// Com o ffmpeg falso entregando ~8 ticks de PCM, o worker criado pelo
// supervisor dispara OnStreamUp (fecha o down aberto herdado e grava 'up'),
// o heartbeat (last_health_check) e a amostra de ruído da calibração.
func TestCovS_StartedWorker_PCMFlow_RecordsUpHeartbeatAndNoiseSample(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ExtraFiles (pipe:3) não é suportado no Windows; coberto no CI Linux")
	}
	f := covSNewFix(t)
	covSUseFakeTools(t)
	t.Setenv("COV_S_FFMPEG_PCM_BYTES", strconv.Itoa(64*16384)) // 64 blocos = 262144 amostras ≈ 8 ticks
	st := f.station()
	camp := f.campaign("ativa", -1, 10, st)
	f.material(camp, 30, st)
	downID, _, err := f.s.healthEvents.RecordDown(f.ctx, st)
	require.NoError(t, err)

	require.NoError(t, f.s.startStationWorker(f.ctx, st))

	count := func(sql string) (n int) {
		require.NoError(t, f.pool.QueryRow(f.ctx, sql, st).Scan(&n))
		return n
	}
	f.wait(func() bool {
		return count(`SELECT count(*) FROM stream_health_events WHERE station_id=$1 AND event_type='up'`) == 1
	}, "OnStreamUp deveria gravar o evento 'up'")
	var dur *int
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT duration_seconds FROM stream_health_events WHERE id=$1`, downID).Scan(&dur))
	require.NotNil(t, dur, "o down herdado do crash é fechado com a duração da queda")
	f.wait(func() bool {
		return count(`SELECT count(*) FROM stations WHERE id=$1 AND last_health_check IS NOT NULL`) == 1
	}, "o heartbeat deveria atualizar last_health_check")
	f.wait(func() bool {
		return count(`SELECT COALESCE(array_length(noise_samples,1),0) FROM station_thresholds WHERE station_id=$1`) == 1
	}, "uma amostra de ruído (a cada 5 janelas) deveria ir para a calibração")
	w := f.worker(st)
	require.NotNil(t, w)
	// A amostra de ruído sai na 5ª janela, antes do fim do PCM: espera o leitor
	// terminar de consumir tudo o que o ffmpeg falso escreveu.
	f.wait(func() bool { return w.BytesReceived() >= uint64(64*16384) },
		"a 1ª conexão deveria entregar todo o PCM do ffmpeg falso")
	require.False(t, w.LastPCMAt().IsZero())
}
