package supervisor

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ── escrita no banco rejeitada (trigger no Postgres de teste) ───────────────
//
// Cada teste instala um trigger que faz o UPDATE de UMA coluna de UMA linha
// falhar, e roda o código de produção inteiro contra o Postgres real. Assim
// exercitamos os ramos "a escrita falhou" sem mockar nenhum repositório.

// Start aborta ANTES de subir qualquer worker se não conseguir marcar a
// campanha como ativa (os workers dependem desse status).
func TestCovS_Start_CampaignStatusWriteRejected_AbortsBeforeWorkers(t *testing.T) {
	f := covSNewFix(t)
	st := f.station()
	camp := f.campaign("programada", -1, 10, st)
	f.material(camp, 30, st)
	f.blockUpdate("campaigns", "status", camp)

	err := f.s.Start(camp)

	require.Error(t, err)
	require.Contains(t, err.Error(), "supervisor: update campaign status")
	require.False(t, f.hasEntry(st), "nenhum worker sobe sem a campanha ativa")
	require.Equal(t, "programada", f.campaignStatus(camp))
}

// Pause: o status não gravou → erro; os workers já tinham sido parados.
func TestCovS_Pause_CampaignStatusWriteRejected_ReturnsError(t *testing.T) {
	f := covSNewFix(t)
	st := f.station()
	camp := f.campaign("programada", -1, 10, st)
	f.material(camp, 30, st)
	require.NoError(t, f.s.Start(camp))
	f.waitWorker(st)
	f.blockUpdate("campaigns", "status", camp)

	err := f.s.Pause(camp)

	require.Error(t, err)
	require.Contains(t, err.Error(), "supervisor: update campaign status")
	require.Equal(t, "ativa", f.campaignStatus(camp), "o status continua ativa")
	require.False(t, f.hasEntry(st), "os workers foram parados antes da tentativa de gravar o status")
}

func TestCovS_UpdateStations_TargetListWriteRejected_ReturnsError(t *testing.T) {
	f := covSNewFix(t)
	a, b := f.station(), f.station()
	camp := f.campaign("ativa", -1, 10, a)
	f.blockUpdate("campaigns", "target_stations", camp)

	err := f.s.UpdateStations(camp, []uuid.UUID{b})

	require.Error(t, err)
	require.Contains(t, err.Error(), "update target_stations")
	got, gerr := f.s.campaigns.Get(f.ctx, camp)
	require.NoError(t, gerr)
	require.Equal(t, []uuid.UUID{a}, got.TargetStations, "a lista antiga permanece")
}

// RestoreActive: falha ao subir UMA campanha é logada e não impede as demais.
func TestCovS_RestoreActive_CampaignStartFailureIsLogged(t *testing.T) {
	f := covSNewFix(t)
	bad, good := f.station(), f.station()
	campBad := f.campaign("ativa", -1, 10, bad)
	campGood := f.campaign("ativa", -1, 10, good)
	f.material(campBad, 30, bad)
	f.material(campGood, 30, good)
	f.blockUpdate("campaigns", "status", campBad)

	require.NoError(t, f.s.RestoreActive(f.ctx))

	require.NotNil(t, f.worker(good))
	require.False(t, f.hasEntry(bad))
	require.Equal(t, 1, f.logs.FilterMessage("supervisor.RestoreActive: failed to start campaign").Len())
}

// Reload: emissora que não sobe (inexistente) é warn; as outras seguem.
func TestCovS_Reload_WorkerRestartFailureIsLogged(t *testing.T) {
	f := covSNewFix(t)
	good := f.station()
	camp := f.campaign("ativa", -1, 10, uuid.New(), good)
	f.material(camp, 30, good)

	require.NoError(t, f.s.Reload(camp))

	require.NotNil(t, f.worker(good))
	require.Equal(t, 1, f.logs.FilterMessage("supervisor.Reload: failed to restart worker").Len())
}

// Falha ao gravar monitoring_status é SEMPRE só warn: o worker/pausa em si vale.
func TestCovS_MonitoringStatusWriteRejected_IsOnlyWarned(t *testing.T) {
	t.Run("Start", func(t *testing.T) {
		f := covSNewFix(t)
		st := f.station()
		camp := f.campaign("programada", -1, 10, st)
		f.material(camp, 30, st)
		f.blockUpdate("stations", "monitoring_status", st)

		require.NoError(t, f.s.Start(camp))

		require.NotNil(t, f.worker(st))
		require.Equal(t, 1, f.logs.FilterMessage("supervisor: update station monitoring_status failed").Len())
	})
	t.Run("Pause", func(t *testing.T) {
		f := covSNewFix(t)
		st := f.station()
		camp := f.campaign("programada", -1, 10, st)
		f.material(camp, 30, st)
		require.NoError(t, f.s.Start(camp))
		f.waitWorker(st)
		f.blockUpdate("stations", "monitoring_status", st)

		require.NoError(t, f.s.Pause(camp))

		require.False(t, f.hasEntry(st))
		require.Equal(t, "cancelada", f.campaignStatus(camp))
		require.Equal(t, 1, f.logs.FilterMessage("supervisor: update station monitoring_status failed").Len())
	})
	t.Run("StopWorkersForCampaign", func(t *testing.T) {
		f := covSNewFix(t)
		st := f.station()
		camp := f.campaign("programada", -1, 10, st)
		f.material(camp, 30, st)
		require.NoError(t, f.s.Start(camp))
		f.waitWorker(st)
		require.NoError(t, f.s.campaigns.UpdateStatus(f.ctx, camp, "concluida"))
		f.blockUpdate("stations", "monitoring_status", st)

		f.s.StopWorkersForCampaign(camp)

		require.False(t, f.hasEntry(st))
		require.Equal(t, 1, f.logs.FilterMessage("supervisor.stopWorkersForCampaign: update station monitoring_status failed").Len())
	})
	t.Run("UpdateStations", func(t *testing.T) {
		f := covSNewFix(t)
		st := f.station()
		camp := f.campaign("ativa", -1, 10, st)
		f.material(camp, 30, st)
		f.blockUpdate("stations", "monitoring_status", st)

		require.NoError(t, f.s.UpdateStations(camp, []uuid.UUID{st}))

		require.NotNil(t, f.worker(st))
		require.Equal(t, 1, f.logs.FilterMessage("supervisor.UpdateStations: update station monitoring_status failed").Len())
	})
	t.Run("reconciler de worker orfao", func(t *testing.T) {
		sc := covSNewReconcileScene(t, "", nil)
		_, err := sc.pool.Exec(sc.ctx, `UPDATE campaigns SET status='programada' WHERE id=$1`, sc.camp)
		require.NoError(t, err)
		sc.blockUpdate("stations", "monitoring_status", sc.station)

		require.True(t, sc.s.reconcileOnce(sc.ctx, sc.station, sc.station.String()))

		require.False(t, sc.hasEntry(sc.station))
		require.Equal(t, 1, sc.logs.FilterMessage("supervisor.reconcile: update station monitoring_status failed").Len())
	})
}

// StartLifecycle: o callback de ativação loga falha do Start (campanha sumiu
// entre a promoção e a partida) e o scheduler sem repositório de campanhas
// termina com erro logado — nenhum dos dois derruba o processo.
func TestCovS_StartLifecycle_FailuresAreLogged(t *testing.T) {
	t.Run("auto-start falha", func(t *testing.T) {
		f := covSNewFix(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.s.StartLifecycle(ctx)
		require.NotNil(t, f.s.lifecycle)

		f.s.lifecycle.OnActivated(ctx, uuid.New()) // campanha inexistente

		require.Equal(t, 1, f.logs.FilterMessage("lifecycle: auto-start failed").Len())
	})
	t.Run("scheduler sem campaigns", func(t *testing.T) {
		f := covSNewFix(t)
		f.s.campaigns = nil

		f.s.StartLifecycle(context.Background())

		f.wait(func() bool { return f.logs.FilterMessage("lifecycle scheduler exited with error").Len() == 1 },
			"Run deveria devolver erro e o supervisor logá-lo")
	})
}
