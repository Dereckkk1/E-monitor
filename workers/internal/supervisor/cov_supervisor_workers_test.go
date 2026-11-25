package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/db"
	"radiocheck/internal/events"
	"radiocheck/internal/metrics"
	"radiocheck/internal/segments"
)

// reloadIDs devolve os ids publicados em index.reload (chave informada).
func (f *covSFix) reloadIDs(key string) []string {
	f.t.Helper()
	require.NoError(f.t, f.nc.Flush())
	var out []string
	for _, m := range f.srv.Messages(events.SubjectIndexReload) {
		var p map[string]string
		require.NoError(f.t, json.Unmarshal(m.Data, &p))
		if id, ok := p[key]; ok {
			out = append(out, id)
		}
	}
	return out
}

func TestCovS_New_InitialisesStateAndMaps(t *testing.T) {
	f := covSNewFix(t)

	s := f.s
	require.NotNil(t, s.workers, "mapa nil = panic no primeiro startStationWorker")
	require.NotNil(t, s.lastStallRestart)
	require.NotNil(t, s.stallRestartCounts)
	require.NotNil(t, s.connectFailures)
	require.NotNil(t, s.dedupSuppressions)
	require.NotNil(t, s.dedupBuffer)
	require.Equal(t, dedupBufferRetention, s.dedupBuffer.MaxAge())
	require.False(t, s.disambigConfidenceAware)
	require.Nil(t, s.lifecycle, "lifecycle é opcional: só nasce em StartLifecycle")

	s.SetShortSingleWindowFactor(2.5)
	require.Equal(t, 2.5, s.shortSingleWindowFactor)
}

// Start: ativa a campanha, dispara index.reload do material pronto, sobe UM
// worker por emissora-alvo com a lista de materiais/URL/limiar certos e marca a
// emissora como monitorada.
func TestCovS_Start_ActivatesCampaignAndStartsWorkersPerStation(t *testing.T) {
	f := covSNewFix(t)
	stA, stB := f.station(), f.station()
	camp := f.campaign("programada", -1, 10, stA, stB)
	matID, matShort := f.material(camp, 30, stA, stB)
	_, err := f.pool.Exec(f.ctx, `UPDATE station_thresholds SET min_hashes = 9 WHERE station_id = $1`, stA)
	require.NoError(t, err)

	require.NoError(t, f.s.Start(camp))

	require.Equal(t, "ativa", f.campaignStatus(camp))
	require.Equal(t, "active", f.monitoring(stA))
	require.Equal(t, "active", f.monitoring(stB))
	for _, st := range []uuid.UUID{stA, stB} {
		w := f.worker(st)
		require.NotNil(t, w, "worker da emissora %s", st)
		require.Equal(t, []int32{matShort}, w.CommercialShortIDs())
		var url string
		require.NoError(t, f.pool.QueryRow(f.ctx, `SELECT stream_url FROM stations WHERE id=$1`, st).Scan(&url))
		require.Equal(t, url, w.StreamURL())
		require.Equal(t, float64(1), testutil.ToFloat64(metrics.WorkerCommercials.WithLabelValues(st.String())))
	}
	require.EqualValues(t, 9, f.worker(stA).Threshold(), "limiar vem de station_thresholds")
	require.EqualValues(t, 5, f.worker(stB).Threshold(), "default do calibrador")
	require.Equal(t, float64(9), testutil.ToFloat64(metrics.StationThreshold.WithLabelValues(stA.String())))
	require.Equal(t, []string{matID.String()}, f.reloadIDs("material_id"), "um index.reload para o material pronto")
	require.NotNil(t, f.s.workers[stA].refreshNow, "canal de refresh de limiar criado")
	require.False(t, f.s.workers[stA].startedAt.IsZero())
}

// Start é idempotente e junta TODOS os materiais prontos de todas as campanhas
// ativas da emissora — inclusive commercial legado.
func TestCovS_Start_ReplacesWorkerAndMergesActiveCampaigns(t *testing.T) {
	f := covSNewFix(t)
	st := f.station()
	campA := f.campaign("programada", -1, 10, st)
	_, shortA := f.material(campA, 30, st)
	_, comShort := f.commercial(campA, 15, st)
	campB := f.campaign("programada", -1, 10, st)
	_, shortB := f.material(campB, 60, st)

	require.NoError(t, f.s.Start(campA))
	first := f.worker(st)
	require.NotNil(t, first)
	require.ElementsMatch(t, []int32{shortA, comShort}, first.CommercialShortIDs())

	require.NoError(t, f.s.Start(campB))
	second := f.worker(st)
	require.NotSame(t, first, second, "Start substitui o worker existente")
	require.ElementsMatch(t, []int32{shortA, comShort, shortB}, second.CommercialShortIDs(),
		"o novo worker enxerga os spots das duas campanhas ativas")
}

func TestCovS_Start_UnknownCampaign_WrapsSentinel(t *testing.T) {
	f := covSNewFix(t)

	err := f.s.Start(uuid.New())

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrCampaignNotFound))
}

// Falha de uma emissora (inexistente) não aborta a campanha: loga e segue.
func TestCovS_Start_UnknownStation_IsLoggedNotFatal(t *testing.T) {
	f := covSNewFix(t)
	good := f.station()
	ghost := uuid.New()
	camp := f.campaign("programada", -1, 10, ghost, good)

	require.NoError(t, f.s.Start(camp))

	require.NotNil(t, f.worker(good))
	require.False(t, f.hasEntry(ghost))
	require.Equal(t, 1, f.logs.FilterMessage("supervisor: failed to start worker for station").Len())
}

func TestCovS_StartStationWorker_Errors(t *testing.T) {
	t.Run("emissora inexistente", func(t *testing.T) {
		f := covSNewFix(t)
		err := f.s.startStationWorker(f.ctx, uuid.New())
		require.Error(t, err)
		require.Contains(t, err.Error(), "get station")
	})
	t.Run("raiz de segmentos nao e diretorio", func(t *testing.T) {
		f := covSNewFix(t)
		st := f.station()
		blocker := filepath.Join(t.TempDir(), "arquivo")
		require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
		f.s.segmentsRoot = blocker // MkdirAll(<arquivo>/<station>) tem de falhar

		err := f.s.startStationWorker(f.ctx, st)

		require.Error(t, err)
		require.Contains(t, err.Error(), "mkdir segments dir")
		require.False(t, f.hasEntry(st), "nada pode ficar registrado quando o diretório não sobe")
	})
}

// O worker nasce com um diretório de segmentos próprio da emissora e, com o
// ffmpeg indisponível (PATH sem binários), OnStreamDown abre um único down event
// por queda (idempotente via lastDownID).
func TestCovS_StartedWorker_ReportsStreamDownOnce(t *testing.T) {
	f := covSNewFix(t)
	st := f.station()
	camp := f.campaign("ativa", -1, 10, st)
	f.material(camp, 30, st)

	require.NoError(t, f.s.startStationWorker(f.ctx, st))

	countDown := func() (n int) {
		require.NoError(t, f.pool.QueryRow(f.ctx,
			`SELECT count(*) FROM stream_health_events WHERE station_id=$1 AND event_type='down'`, st).Scan(&n))
		return n
	}
	f.wait(func() bool { return countDown() == 1 }, "OnStreamDown deveria abrir 1 down event")
	f.wait(func() bool {
		f.s.mu.Lock()
		defer f.s.mu.Unlock()
		return f.s.workers[st].lastDownID != nil
	}, "o entry deve carimbar lastDownID (idempotência da queda)")
	info, err := os.Stat(segments.DirFor(f.s.segmentsRoot, st))
	require.NoError(t, err, "o diretório de segmentos da emissora é criado antes do ffmpeg subir")
	require.True(t, info.IsDir())
}

// Recuperação de partida: um down aberto deixado por um crash é adotado pelo
// entry novo (lastDownID/lastDownAt) em vez de abrir outro.
func TestCovS_StartStationWorker_AdoptsOpenDownEvent(t *testing.T) {
	f := covSNewFix(t)
	st := f.station()
	camp := f.campaign("ativa", -1, 10, st)
	f.material(camp, 30, st)
	id, _, err := f.s.healthEvents.RecordDown(f.ctx, st)
	require.NoError(t, err)

	require.NoError(t, f.s.startStationWorker(f.ctx, st))

	f.s.mu.Lock()
	e := f.s.workers[st]
	require.NotNil(t, e.lastDownID, "o down aberto deve ser adotado")
	require.Equal(t, id, *e.lastDownID)
	f.s.mu.Unlock()
	require.Equal(t, 1, f.logs.FilterMessage("supervisor: startup recovery — found open down event").Len())
}

func TestCovS_Pause_StopsOnlyUncoveredStationsAndCancelsCampaign(t *testing.T) {
	f := covSNewFix(t)
	shared, solo := f.station(), f.station()
	campA := f.campaign("programada", -1, 10, shared, solo)
	f.material(campA, 30, shared, solo)
	campB := f.campaign("programada", -1, 10, shared)
	f.material(campB, 30, shared)
	require.NoError(t, f.s.Start(campA))
	require.NoError(t, f.s.Start(campB))
	f.waitWorker(shared)
	f.waitWorker(solo)
	f.s.mu.Lock()
	f.s.connectFailures[solo] = 4
	f.s.mu.Unlock()

	require.NoError(t, f.s.Pause(campA))

	require.Equal(t, "cancelada", f.campaignStatus(campA))
	require.True(t, f.hasEntry(shared), "emissora ainda coberta pela campanha B mantém o worker")
	require.Equal(t, "active", f.monitoring(shared))
	require.False(t, f.hasEntry(solo), "emissora só da campanha A perde o worker")
	require.Equal(t, "paused", f.monitoring(solo))
	f.s.mu.Lock()
	_, hadFailures := f.s.connectFailures[solo]
	f.s.mu.Unlock()
	require.False(t, hadFailures, "pausar zera o breaker de conexão da emissora")

	require.NoError(t, f.s.Pause(campB))
	require.False(t, f.hasEntry(shared))
	require.Equal(t, "paused", f.monitoring(shared))
}

func TestCovS_Pause_UnknownCampaign_WrapsSentinel(t *testing.T) {
	f := covSNewFix(t)
	err := f.s.Pause(uuid.New())
	require.True(t, errors.Is(err, ErrCampaignNotFound), "%v", err)
}

// Pause também derruba o placeholder de backoff (worker==nil) e não mexe no
// contador de workers ativos por causa dele.
func TestCovS_Pause_RemovesBackoffPlaceholder(t *testing.T) {
	f := covSNewFix(t)
	st := f.station()
	camp := f.campaign("ativa", -1, 10, st)
	cancelled := false
	f.s.mu.Lock()
	f.s.workers[st] = &workerEntry{cancel: func() { cancelled = true }}
	f.s.connectFailures[st] = 2
	f.s.mu.Unlock()

	require.NoError(t, f.s.Pause(camp))

	require.True(t, cancelled, "o respawn pendente do backoff é abortado")
	require.False(t, f.hasEntry(st))
	require.Empty(t, f.s.BackoffStations())
}

func TestCovS_Reload(t *testing.T) {
	t.Run("campanha desconhecida e no-op", func(t *testing.T) {
		f := covSNewFix(t)
		require.NoError(t, f.s.Reload(uuid.New()))
	})
	t.Run("campanha nao ativa e no-op", func(t *testing.T) {
		f := covSNewFix(t)
		st := f.station()
		camp := f.campaign("programada", 5, 20, st)
		f.material(camp, 30, st)

		require.NoError(t, f.s.Reload(camp))

		require.False(t, f.hasEntry(st))
		require.Empty(t, f.reloadIDs("material_id"))
	})
	t.Run("campanha ativa reconstroi workers e republica index.reload", func(t *testing.T) {
		f := covSNewFix(t)
		st := f.station()
		camp := f.campaign("ativa", -1, 10, st)
		matID, shortID := f.material(camp, 30, st)

		require.NoError(t, f.s.Reload(camp))

		w := f.worker(st)
		require.NotNil(t, w)
		require.Equal(t, []int32{shortID}, w.CommercialShortIDs())
		require.Equal(t, []string{matID.String()}, f.reloadIDs("material_id"))
		// um commercial legado pronto também gera index.reload (commercial_id)
		comID, _ := f.commercial(camp, 15, st)
		require.NoError(t, f.s.Reload(camp))
		require.Equal(t, []string{comID.String()}, f.reloadIDs("commercial_id"))
	})
}

func TestCovS_RestoreActive_StartsEveryActiveCampaign(t *testing.T) {
	f := covSNewFix(t)
	st := f.station()
	camp := f.campaign("ativa", -1, 10, st)
	_, shortID := f.material(camp, 30, st)

	require.NoError(t, f.s.RestoreActive(f.ctx))

	w := f.worker(st)
	require.NotNil(t, w, "campanha 'ativa' deve ganhar worker ao reiniciar o processo")
	require.Equal(t, []int32{shortID}, w.CommercialShortIDs())
	require.Equal(t, 1, f.logs.FilterMessage("supervisor.RestoreActive: done").Len())
}

func TestCovS_RestoreActive_QueryError(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := db.New(context.Background(), url, zap.NewNop())
	require.NoError(t, err)
	pool.Close()
	s := &Supervisor{db: pool, log: zap.NewNop()}

	err = s.RestoreActive(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "supervisor.RestoreActive: query")
}

func TestCovS_StopWorkersForCampaign(t *testing.T) {
	t.Run("campanha inexistente so loga", func(t *testing.T) {
		f := covSNewFix(t)
		f.s.StopWorkersForCampaign(uuid.New())
		require.Equal(t, 1, f.logs.FilterMessage("supervisor.stopWorkersForCampaign: get campaign failed").Len())
	})
	t.Run("emissora ainda coberta mantem o worker; descoberta e parada", func(t *testing.T) {
		f := covSNewFix(t)
		covered, free := f.station(), f.station()
		campA := f.campaign("ativa", -1, 10, covered, free)
		f.material(campA, 30, covered, free)
		campB := f.campaign("ativa", -1, 10, covered)
		f.material(campB, 30, covered)
		require.NoError(t, f.s.Start(campA))
		require.NoError(t, f.s.Start(campB))
		// campA conclui por fora do supervisor; o scheduler chama StopWorkersForCampaign
		require.NoError(t, f.s.campaigns.UpdateStatus(f.ctx, campA, "concluida"))

		f.s.StopWorkersForCampaign(campA)

		require.True(t, f.hasEntry(covered))
		require.False(t, f.hasEntry(free))
		require.Equal(t, "paused", f.monitoring(free))
		require.Equal(t, "active", f.monitoring(covered))
	})
}

// UpdateStations: campanha não ativa só grava a lista; ativa reconcilia o
// fleet (remove/mantém/adiciona) sem passar por 'cancelada'.
func TestCovS_UpdateStations_NonActiveCampaign_OnlyUpdatesDB(t *testing.T) {
	f := covSNewFix(t)
	a, b := f.station(), f.station()
	camp := f.campaign("programada", 5, 20, a)

	require.NoError(t, f.s.UpdateStations(camp, []uuid.UUID{b}))

	got, err := f.s.campaigns.Get(f.ctx, camp)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{b}, got.TargetStations)
	require.Equal(t, "programada", got.Status, "status não pode mudar")
	require.False(t, f.hasEntry(a))
	require.False(t, f.hasEntry(b))

	require.NoError(t, f.s.UpdateStations(camp, nil))
	got, err = f.s.campaigns.Get(f.ctx, camp)
	require.NoError(t, err)
	require.Empty(t, got.TargetStations, "nil vira lista vazia")
}

func TestCovS_UpdateStations_ActiveCampaign_StopsKeepsAndAddsWorkers(t *testing.T) {
	f := covSNewFix(t)
	removed, kept, added, stillCovered := f.station(), f.station(), f.station(), f.station()
	camp := f.campaign("programada", -1, 10, removed, kept, stillCovered)
	f.material(camp, 30, removed, kept, stillCovered, added)
	other := f.campaign("programada", -1, 10, stillCovered)
	f.material(other, 30, stillCovered)
	require.NoError(t, f.s.Start(camp))
	require.NoError(t, f.s.Start(other))
	f.waitWorker(removed)
	keptBefore := f.waitWorker(kept)
	f.waitWorker(stillCovered)

	require.NoError(t, f.s.UpdateStations(camp, []uuid.UUID{kept, added}))

	got, err := f.s.campaigns.Get(f.ctx, camp)
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{kept, added}, got.TargetStations)
	require.False(t, f.hasEntry(removed), "emissora removida e descoberta perde o worker")
	require.Equal(t, "paused", f.monitoring(removed))
	require.True(t, f.hasEntry(stillCovered), "emissora removida mas coberta por outra campanha mantém o worker")
	require.NotSame(t, keptBefore, f.worker(kept), "emissora mantida é reiniciada para recarregar os spots")
	require.NotNil(t, f.worker(added), "emissora nova ganha worker")
	require.Equal(t, "active", f.monitoring(added))
	require.Equal(t, "active", f.monitoring(kept))
	require.Equal(t, 1, f.logs.FilterMessage("supervisor.UpdateStations: applied").Len())
}

// ── limiar dinâmico (runThresholdRefresher / refreshThresholdOnce) ──────────

func TestCovS_RefreshThreshold_AppliesNewValueToRunningWorker(t *testing.T) {
	f := covSNewFix(t)
	st := f.station()
	camp := f.campaign("programada", -1, 10, st)
	f.material(camp, 30, st)
	require.NoError(t, f.s.Start(camp))
	w := f.waitWorker(st)
	require.EqualValues(t, 5, w.Threshold())
	_, err := f.pool.Exec(f.ctx, `UPDATE station_thresholds SET min_hashes = 17 WHERE station_id = $1`, st)
	require.NoError(t, err)

	require.NoError(t, f.s.RefreshThreshold(st))

	f.wait(func() bool { return w.Threshold() == 17 }, "o refresher deveria aplicar min_hashes=17 sem reiniciar o worker")
	require.Equal(t, float64(17), testutil.ToFloat64(metrics.StationThreshold.WithLabelValues(st.String())))
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.StationThresholdRefreshes.WithLabelValues(st.String(), "updated")))

	// Mesmo valor de novo → "unchanged".
	require.NoError(t, f.s.RefreshThreshold(st))
	f.wait(func() bool {
		return testutil.ToFloat64(metrics.StationThresholdRefreshes.WithLabelValues(st.String(), "unchanged")) >= 1
	}, "segundo refresh com o mesmo valor deve contar como unchanged")
	require.EqualValues(t, 17, w.Threshold())
}

// Falha na leitura mantém o limiar anterior (nunca "rebaixa" pro default).
func TestCovS_RefreshThresholdOnce_LookupError_KeepsPreviousValue(t *testing.T) {
	f := covSNewFix(t)
	st := f.station()
	camp := f.campaign("programada", -1, 10, st)
	f.material(camp, 30, st)
	require.NoError(t, f.s.Start(camp))
	w := f.waitWorker(st)
	w.SetThreshold(23)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	f.s.refreshThresholdOnce(cancelled, st, st.String(), w)

	require.EqualValues(t, 23, w.Threshold())
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.StationThresholdRefreshes.WithLabelValues(st.String(), "error")))
	require.Equal(t, 1, f.logs.FilterMessage("supervisor: threshold refresh failed; keeping previous value").Len())
}

func TestCovS_RefreshThreshold_NoWorkerOrNoChannel(t *testing.T) {
	f := covSNewFix(t)
	st := f.station()
	err := f.s.RefreshThreshold(st)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no worker running")

	f.s.mu.Lock()
	f.s.workers[st] = &workerEntry{} // placeholder sem canal
	f.s.mu.Unlock()
	require.Error(t, f.s.RefreshThreshold(st))
}

// ── stall watchdog: parte determinística (sem esperar o ticker de 30s) ──────

// retireStalledWorker com delay>0 estaciona o placeholder (worker nil, cancel
// do backoff) que Pause/Stop conseguem abortar; sem delay só remove o entry.
func TestCovS_RetireStalledWorker_ParksBackoffPlaceholderOnlyWithDelay(t *testing.T) {
	f := covSNewFix(t)
	withDelay, noDelay := f.station(), f.station()
	for _, id := range []uuid.UUID{withDelay, noDelay} {
		f.s.mu.Lock()
		f.s.workers[id] = &workerEntry{}
		f.s.mu.Unlock()
	}
	workerCancelled := 0
	cancelFn := func() { workerCancelled++ }

	boCtx := f.s.retireStalledWorker(withDelay, cancelFn, time.Minute)
	require.NotNil(t, boCtx, "com backoff o contexto do respawn é devolvido")
	require.NoError(t, boCtx.Err())
	f.s.mu.Lock()
	placeholder := f.s.workers[withDelay]
	f.s.mu.Unlock()
	require.NotNil(t, placeholder)
	require.Nil(t, placeholder.worker, "placeholder não é worker real")
	require.NotNil(t, placeholder.refreshNow)
	placeholder.cancel() // Pause/StopWorkers aborta o respawn pendente
	require.Error(t, boCtx.Err())

	require.Nil(t, f.s.retireStalledWorker(noDelay, cancelFn, 0), "sem backoff não há contexto de espera")
	require.False(t, f.hasEntry(noDelay))
	require.Equal(t, 2, workerCancelled)
	// cada emissora ganhou exatamente 1 down event (gravado antes de matar o worker)
	for _, id := range []uuid.UUID{withDelay, noDelay} {
		var n int
		require.NoError(t, f.pool.QueryRow(f.ctx,
			`SELECT count(*) FROM stream_health_events WHERE station_id=$1 AND event_type='down'`, id).Scan(&n))
		require.Equal(t, 1, n, "a queda é gravada antes de matar o worker")
	}
}

// O reinício preventivo (03h–05h do dia seguinte) é abortado se o worker for
// parado antes: cancelar o contexto do worker encerra a goroutine sem reiniciar nada.
func TestCovS_SchedulePreventiveRestart_ExitsWhenContextCancelled(t *testing.T) {
	f := covSNewFix(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.s.schedulePreventiveRestart(ctx, uuid.New()) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("schedulePreventiveRestart não respeitou o cancelamento do worker")
	}
}

func TestCovS_RunWorkerReconciler_ExitsWhenContextCancelled(t *testing.T) {
	f := covSNewFix(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.s.runWorkerReconciler(ctx, uuid.New()) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runWorkerReconciler não respeitou o cancelamento do worker")
	}
}

func TestCovS_RunStallWatchdog_ExitsWhenContextCancelled(t *testing.T) {
	f := covSNewFix(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.s.runStallWatchdog(ctx, uuid.New(), nil, cancel) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runStallWatchdog não respeitou o cancelamento do worker")
	}
}
