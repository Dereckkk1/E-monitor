package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/db"
	"radiocheck/internal/ingestor"
	"radiocheck/internal/metrics"
)

// covSClosedCampaigns devolve um repositório sobre um pool já fechado: toda
// query nele falha, o que permite exercitar os ramos de erro sem derrubar o banco.
func covSClosedCampaigns(t *testing.T) *catalog.Campaigns {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := db.New(context.Background(), url, zap.NewNop())
	require.NoError(t, err)
	pool.Close()
	return catalog.NewCampaigns(pool)
}

// ── NATSEventBus ────────────────────────────────────────────────────────────

func TestCovS_NATSEventBus_PublishesCampaignEventsWithIDAndTimestamp(t *testing.T) {
	srv := covSStartNATS(t)
	nc := covSConnect(t, srv)
	bus := NewNATSEventBus(nc, zap.NewNop())
	require.IsType(t, &NATSEventBus{}, bus, "com conexão real não pode cair no noop")
	id := uuid.New()

	require.NoError(t, bus.PublishActivated(id))
	require.NoError(t, bus.PublishEnded(id))
	require.NoError(t, nc.Flush())

	for subject, msgs := range map[string][]covSNATSMsg{
		SubjectCampaignActivated: srv.Messages(SubjectCampaignActivated),
		SubjectCampaignEnded:     srv.Messages(SubjectCampaignEnded),
	} {
		require.Len(t, msgs, 1, subject)
		var p lifecyclePayload
		require.NoError(t, json.Unmarshal(msgs[0].Data, &p), subject)
		require.Equal(t, id.String(), p.CampaignID, subject)
		require.WithinDuration(t, time.Now(), p.At, time.Minute, subject)
	}
}

func TestCovS_NATSEventBus_PublishOnClosedConnectionFails(t *testing.T) {
	srv := covSStartNATS(t)
	nc := covSConnect(t, srv)
	bus := NewNATSEventBus(nc, zap.NewNop())
	nc.Close()

	require.Error(t, bus.PublishActivated(uuid.New()))
	require.Error(t, bus.PublishEnded(uuid.New()))
}

// ── LifecycleScheduler ──────────────────────────────────────────────────────

type covSTransitions struct {
	mu                       sync.Mutex
	activated, ended, demote []uuid.UUID
}

func (c *covSTransitions) hook(dst *[]uuid.UUID) LifecycleAction {
	return func(_ context.Context, id uuid.UUID) {
		c.mu.Lock()
		*dst = append(*dst, id)
		c.mu.Unlock()
	}
}

func (c *covSTransitions) has(src *[]uuid.UUID, id uuid.UUID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, x := range *src {
		if x == id {
			return true
		}
	}
	return false
}

// Run faz o primeiro scan na hora e depois a cada intervalo: programada vencida
// → ativa, ativa vencida → concluida, ativa antes do início → programada. Cada
// transição vai pro barramento e pro callback; os gauges refletem o banco; e o
// cancelamento do contexto encerra o loop sem erro.
func TestCovS_LifecycleScheduler_Run_TransitionsCallbacksBusAndGauges(t *testing.T) {
	f := covSNewFix(t)
	stA, stE, stD := f.station(), f.station(), f.station()
	campA := f.campaign("programada", -2, 10, stA)
	campE := f.campaign("ativa", -10, -3, stE)
	campD := f.campaign("ativa", 5, 15, stD)
	bus := &mockBus{}
	sched := NewLifecycleScheduler(f.pool, f.s.campaigns, bus, f.s.log)
	sched.SetInterval(20 * time.Millisecond)
	var tr covSTransitions
	sched.OnActivated = tr.hook(&tr.activated)
	sched.OnEnded = tr.hook(&tr.ended)
	sched.OnDemoted = tr.hook(&tr.demote)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()

	f.wait(func() bool {
		return tr.has(&tr.activated, campA) && tr.has(&tr.ended, campE) && tr.has(&tr.demote, campD)
	}, "as três transições deveriam ter sido processadas no primeiro scan")
	require.Equal(t, "ativa", f.campaignStatus(campA))
	require.Equal(t, "concluida", f.campaignStatus(campE))
	require.Equal(t, "programada", f.campaignStatus(campD))
	activated, ended := bus.snapshot()
	require.Contains(t, activated, campA)
	require.Contains(t, ended, campE)
	require.NotContains(t, activated, campD, "demote não publica evento no barramento")

	// Um scan posterior (disparado pelo ticker, não pelo scan inicial) também
	// promove: cria outra campanha vencida com o loop já rodando.
	campF := f.campaign("programada", -1, 10, f.station())
	f.wait(func() bool { return tr.has(&tr.activated, campF) }, "o ticker deveria promover a campanha criada depois do 1º scan")

	f.wait(func() bool {
		counts, err := f.s.campaigns.CountByStatus(f.ctx)
		if err != nil {
			return false
		}
		for _, st := range []string{"programada", "ativa", "concluida", "cancelada"} {
			if testutil.ToFloat64(metrics.CampaignsByStatus.WithLabelValues(st)) != float64(counts[st]) {
				return false
			}
		}
		return true
	}, "gauge campaigns_by_status deve refletir o banco")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err, "cancelar o contexto encerra o loop sem erro")
	case <-time.After(10 * time.Second):
		t.Fatal("Run não parou após cancelar o contexto")
	}
	require.Equal(t, 1, f.logs.FilterMessage("lifecycle scheduler stopped").Len())
}

// Falha ao publicar no barramento é só warn: o callback in-process roda mesmo assim.
func TestCovS_LifecycleScheduler_Tick_BusFailureStillRunsCallbacks(t *testing.T) {
	cases := []struct {
		failOn string
		status string
		start  int
		end    int
		hook   string
		warn   string
	}{
		{"activated", "programada", -2, 10, "activated", "lifecycle: publish activated failed"},
		{"ended", "ativa", -10, -3, "ended", "lifecycle: publish ended failed"},
	}
	for _, c := range cases {
		t.Run(c.failOn, func(t *testing.T) {
			f := covSNewFix(t)
			camp := f.campaign(c.status, c.start, c.end, f.station())
			sched := NewLifecycleScheduler(f.pool, f.s.campaigns, &mockBus{failOn: c.failOn}, f.s.log)
			var tr covSTransitions
			sched.OnActivated = tr.hook(&tr.activated)
			sched.OnEnded = tr.hook(&tr.ended)

			sched.tick(f.ctx)

			if c.hook == "activated" {
				require.True(t, tr.has(&tr.activated, camp))
			} else {
				require.True(t, tr.has(&tr.ended, camp))
			}
			require.Equal(t, 1, f.logs.FilterMessage(c.warn).Len())
		})
	}
}

func TestCovS_LifecycleScheduler_DatabaseFailuresAreLoggedNotFatal(t *testing.T) {
	closed := covSClosedCampaigns(t)
	log, logs := covSObservedLogger()
	sched := NewLifecycleScheduler(nil, closed, &mockBus{}, log)

	require.NotPanics(t, func() {
		sched.tick(context.Background())
		sched.refreshGauges(context.Background())
	})

	require.Equal(t, 1, logs.FilterMessage("lifecycle scheduler: promote failed").Len())
	require.Equal(t, 1, logs.FilterMessage("lifecycle: count by status failed").Len())
}

// StartLifecycle liga o scheduler ao supervisor: ativação sobe o worker
// (via Start), conclusão e demote param os workers; segunda chamada é no-op.
func TestCovS_StartLifecycle_WiresActivationEndAndDemotionToWorkers(t *testing.T) {
	f := covSNewFix(t)
	stA, stE, stD := f.station(), f.station(), f.station()
	campA := f.campaign("programada", -2, 10, stA)
	f.material(campA, 30, stA)
	campE := f.campaign("ativa", -10, -3, stE)
	campD := f.campaign("ativa", 5, 15, stD)
	// workers já em execução para as campanhas que vão encerrar / ser rebaixadas
	require.NoError(t, f.s.startStationWorker(f.ctx, stE))
	require.NoError(t, f.s.startStationWorker(f.ctx, stD))
	f.waitWorker(stE)
	f.waitWorker(stD)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f.s.StartLifecycle(ctx)
	first := f.s.lifecycle
	require.NotNil(t, first)
	f.s.StartLifecycle(ctx)
	require.Same(t, first, f.s.lifecycle, "segunda chamada é no-op")

	f.waitWorker(stA)
	f.waitNoEntry(stE)
	f.waitNoEntry(stD)
	require.Equal(t, "ativa", f.campaignStatus(campA))
	require.Equal(t, "concluida", f.campaignStatus(campE))
	require.Equal(t, "programada", f.campaignStatus(campD))
	require.Equal(t, "paused", f.monitoring(stE))
	require.Equal(t, "paused", f.monitoring(stD))
	require.NoError(t, f.nc.Flush())
	require.NotEmpty(t, f.srv.Messages(SubjectCampaignActivated), "a ativação também vai pro barramento NATS")
	require.NotEmpty(t, f.srv.Messages(SubjectCampaignEnded))
}

// ── reconciler da frota ─────────────────────────────────────────────────────

func TestCovS_ReconcileFleetOnce_StartFailureIsRetriedNextPass(t *testing.T) {
	f := covSNewFix(t)
	st := f.station()
	f.campaign("ativa", -1, 10, st)
	calls := 0
	failing := func(context.Context, uuid.UUID) error { calls++; return context.DeadlineExceeded }
	before := testutil.ToFloat64(metrics.FleetReconcileActions.WithLabelValues("start_failed"))

	prev := f.s.reconcileFleetOnce(f.ctx, map[uuid.UUID]struct{}{}, failing)
	require.Equal(t, 0, calls, "1ª passada só registra a falta")
	prev = f.s.reconcileFleetOnce(f.ctx, prev, failing)

	require.Equal(t, 1, calls)
	require.Contains(t, prev, st, "continua faltando para a próxima passada tentar de novo")
	require.Equal(t, before+1, testutil.ToFloat64(metrics.FleetReconcileActions.WithLabelValues("start_failed")))
	require.Equal(t, 1, f.logs.FilterMessage("supervisor.fleet: start missing worker failed; retrying next pass").Len())
}

func TestCovS_ReconcileFleetOnce_QueryErrorKeepsPreviousState(t *testing.T) {
	closed := covSClosedCampaigns(t)
	log, logs := covSObservedLogger()
	s := &Supervisor{campaigns: closed, log: log, workers: map[uuid.UUID]*workerEntry{}}
	prev := map[uuid.UUID]struct{}{uuid.New(): {}}
	called := false

	got := s.reconcileFleetOnce(context.Background(), prev, func(context.Context, uuid.UUID) error { called = true; return nil })

	require.Equal(t, prev, got)
	require.False(t, called, "com a consulta falha nada é iniciado")
	require.Equal(t, 1, logs.FilterMessage("supervisor.fleet: covered stations query failed; skipping pass").Len())
}

func TestCovS_StartFleetReconciler_StopsWithContext(t *testing.T) {
	f := covSNewFix(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.s.StartFleetReconciler(ctx)
	cancel() // o ticker de 2min nunca dispara; o contexto encerra a goroutine
	f.s.mu.Lock()
	require.Empty(t, f.s.workers, "nenhum worker é criado fora do tick")
	f.s.mu.Unlock()
}

// ── reconcileOnce: deriva de lista/URL reconstrói; erros mantêm o worker ────

type covSReconcileScene struct {
	*covSFix
	station   uuid.UUID
	camp      uuid.UUID
	matShort  int32
	url       string
	cancelled bool
	old       *ingestor.Worker
}

func covSNewReconcileScene(t *testing.T, workerURL string, workerIDs []int32) *covSReconcileScene {
	t.Helper()
	f := covSNewFix(t)
	sc := &covSReconcileScene{covSFix: f, station: f.station()}
	sc.camp = f.campaign("ativa", -1, 10, sc.station)
	_, sc.matShort = f.material(sc.camp, 30, sc.station)
	require.NoError(t, f.pool.QueryRow(f.ctx, `SELECT stream_url FROM stations WHERE id=$1`, sc.station).Scan(&sc.url))
	if workerURL == "" {
		workerURL = sc.url
	}
	sc.old = ingestor.NewWorker(ingestor.WorkerConfig{
		StationID: sc.station, StreamURL: workerURL, CommercialShortIDs: workerIDs,
	}, nil, nil, zap.NewNop())
	f.s.mu.Lock()
	f.s.workers[sc.station] = &workerEntry{worker: sc.old, cancel: func() { sc.cancelled = true }, startedAt: time.Now()}
	f.s.mu.Unlock()
	return sc
}

func TestCovS_ReconcileOnce_CommercialDrift_RebuildsWorker(t *testing.T) {
	sc := covSNewReconcileScene(t, "", nil) // worker sem spots; o banco quer 1
	label := sc.station.String()
	beforeRestarts := testutil.ToFloat64(metrics.WorkerReconcileRuns.WithLabelValues(label, "restarted"))

	restarted := sc.s.reconcileOnce(sc.ctx, sc.station, label)

	require.True(t, restarted)
	require.True(t, sc.cancelled, "o worker velho é cancelado")
	require.Equal(t, beforeRestarts+1, testutil.ToFloat64(metrics.WorkerReconcileRuns.WithLabelValues(label, "restarted")))
	sc.wait(func() bool { w := sc.worker(sc.station); return w != nil && w != sc.old }, "um worker novo deveria assumir")
	require.Equal(t, []int32{sc.matShort}, sc.worker(sc.station).CommercialShortIDs())
	require.Equal(t, 1, sc.logs.FilterMessage("supervisor.reconcile: drift detected — restarting worker").Len())
	require.Equal(t, "commercial list changed", sc.logs.FilterMessage("supervisor.reconcile: drift detected — restarting worker").All()[0].ContextMap()["reason"])
}

func TestCovS_ReconcileOnce_StreamURLDrift_RebuildsWithNewURL(t *testing.T) {
	sc := covSNewReconcileScene(t, "http://stream.example.com/antiga", nil)
	sc.old = ingestor.NewWorker(ingestor.WorkerConfig{
		StationID: sc.station, StreamURL: "http://stream.example.com/antiga", CommercialShortIDs: []int32{sc.matShort},
	}, nil, nil, zap.NewNop())
	sc.s.mu.Lock()
	sc.s.workers[sc.station].worker = sc.old
	sc.s.mu.Unlock()

	restarted := sc.s.reconcileOnce(sc.ctx, sc.station, sc.station.String())

	require.True(t, restarted)
	sc.wait(func() bool { w := sc.worker(sc.station); return w != nil && w != sc.old }, "um worker novo deveria assumir")
	require.Equal(t, sc.url, sc.worker(sc.station).StreamURL(), "o worker novo usa a URL atual do banco")
	require.Equal(t, "stream_url changed", sc.logs.FilterMessage("supervisor.reconcile: drift detected — restarting worker").All()[0].ContextMap()["reason"])
}

func TestCovS_ReconcileOnce_NoDrift_KeepsWorkerAndCountsUnchanged(t *testing.T) {
	sc := covSNewReconcileScene(t, "", nil)
	// o worker já tem exatamente o spot que o banco quer
	sc.old = ingestor.NewWorker(ingestor.WorkerConfig{
		StationID: sc.station, StreamURL: sc.url, CommercialShortIDs: []int32{sc.matShort},
	}, nil, nil, zap.NewNop())
	sc.s.mu.Lock()
	sc.s.workers[sc.station].worker = sc.old
	sc.s.mu.Unlock()
	label := sc.station.String()

	restarted := sc.s.reconcileOnce(sc.ctx, sc.station, label)

	require.False(t, restarted)
	require.False(t, sc.cancelled)
	require.Same(t, sc.old, sc.worker(sc.station))
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.WorkerReconcileRuns.WithLabelValues(label, "unchanged")))
	require.Equal(t, float64(1), testutil.ToFloat64(metrics.WorkerCommercials.WithLabelValues(label)))
}

// Worker que sumiu do mapa (Pause/Stop já cuidaram) → nada a reconciliar.
func TestCovS_ReconcileOnce_WorkerAlreadyGone_ReturnsFalse(t *testing.T) {
	sc := covSNewReconcileScene(t, "", nil)
	sc.s.mu.Lock()
	delete(sc.s.workers, sc.station)
	sc.s.mu.Unlock()

	require.False(t, sc.s.reconcileOnce(sc.ctx, sc.station, sc.station.String()))
	require.False(t, sc.cancelled)
}

// Cada falha de leitura mantém o worker (um hiccup do banco não derruba stream saudável).
func TestCovS_ReconcileOnce_ReadFailures_KeepWorker(t *testing.T) {
	t.Run("emissora inexistente", func(t *testing.T) {
		f := covSNewFix(t)
		label := "ghost"
		before := testutil.ToFloat64(metrics.WorkerReconcileRuns.WithLabelValues(label, "error"))
		require.False(t, f.s.reconcileOnce(f.ctx, uuid.New(), label))
		require.Equal(t, before+1, testutil.ToFloat64(metrics.WorkerReconcileRuns.WithLabelValues(label, "error")))
		require.Equal(t, 1, f.logs.FilterMessage("supervisor.reconcile: get station failed; keeping worker").Len())
	})
	t.Run("consulta de campanhas ativas falha", func(t *testing.T) {
		sc := covSNewReconcileScene(t, "", nil)
		sc.s.campaigns = covSClosedCampaigns(t)
		require.False(t, sc.s.reconcileOnce(sc.ctx, sc.station, sc.station.String()))
		require.Equal(t, 1, sc.logs.FilterMessage("supervisor.reconcile: active campaigns query failed; keeping worker").Len())
		require.False(t, sc.cancelled)
	})
	t.Run("listagem de commercials falha", func(t *testing.T) {
		sc := covSNewReconcileScene(t, "", nil)
		sc.s.commercials = covSClosedCommercials(t)
		require.False(t, sc.s.reconcileOnce(sc.ctx, sc.station, sc.station.String()))
		require.Equal(t, 1, sc.logs.FilterMessage("supervisor.reconcile: list commercials failed; keeping worker").Len())
		require.False(t, sc.cancelled)
	})
	t.Run("listagem de materiais falha", func(t *testing.T) {
		sc := covSNewReconcileScene(t, "", nil)
		sc.s.materials = covSClosedMaterials(t)
		require.False(t, sc.s.reconcileOnce(sc.ctx, sc.station, sc.station.String()))
		require.Equal(t, 1, sc.logs.FilterMessage("supervisor.reconcile: list materials failed; keeping worker").Len())
		require.False(t, sc.cancelled)
	})
}

func covSClosedCommercials(t *testing.T) *catalog.Commercials {
	t.Helper()
	pool, err := db.New(context.Background(), os.Getenv("TEST_DATABASE_URL"), zap.NewNop())
	require.NoError(t, err)
	pool.Close()
	return catalog.NewCommercials(pool)
}

func covSClosedMaterials(t *testing.T) *catalog.Materials {
	t.Helper()
	pool, err := db.New(context.Background(), os.Getenv("TEST_DATABASE_URL"), zap.NewNop())
	require.NoError(t, err)
	pool.Close()
	return catalog.NewMaterials(pool)
}

// stopOrphanWorker: contexto já cancelado = a entrada atual é de um worker NOVO,
// que não pertence a este reconciler.
func TestCovS_StopOrphanWorker_CancelledContextLeavesNewerWorker(t *testing.T) {
	sc := covSNewReconcileScene(t, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stopped := sc.s.stopOrphanWorker(ctx, sc.station, sc.station.String())

	require.False(t, stopped)
	require.False(t, sc.cancelled)
	require.True(t, sc.hasEntry(sc.station))
}
