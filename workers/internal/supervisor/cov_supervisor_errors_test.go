package supervisor

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/db"
	"radiocheck/internal/match"
)

// matchDetection monta uma confirmação mínima (emissora + short_id).
func matchDetection(station uuid.UUID, short int32) match.ConfirmedDetection {
	return match.ConfirmedDetection{StationID: station.String(), CommercialShortID: short}
}

// covSClosedDB devolve um pool já fechado (toda query falha).
func covSClosedDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := db.New(context.Background(), url, zap.NewNop())
	require.NoError(t, err)
	pool.Close()
	return pool
}

// Erro de banco (≠ "não existe") ao carregar a campanha NÃO vira
// ErrCampaignNotFound: sobe embrulhado com o prefixo da operação.
func TestCovS_CampaignOperations_DatabaseErrorIsWrappedNotSentinel(t *testing.T) {
	closed := catalog.NewCampaigns(covSClosedDB(t))
	s := &Supervisor{campaigns: closed, log: zap.NewNop()}
	id := uuid.New()

	cases := []struct {
		name   string
		err    error
		prefix string
	}{
		{"Start", s.Start(id), "supervisor: get campaign"},
		{"Pause", s.Pause(id), "supervisor: get campaign"},
		{"Reload", s.Reload(id), "supervisor.Reload: get campaign"},
		{"UpdateStations", s.UpdateStations(id, nil), "supervisor.UpdateStations: get campaign"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Error(t, c.err)
			require.Contains(t, c.err.Error(), c.prefix)
			require.NotErrorIs(t, c.err, ErrCampaignNotFound, "falha de banco não é 'campanha inexistente'")
		})
	}
}

func TestCovS_RecordStreamDownSync_DatabaseFailure_LeavesEntryUntouched(t *testing.T) {
	log, logs := covSObservedLogger()
	st := uuid.New()
	s := &Supervisor{
		healthEvents: catalog.NewHealthEvents(covSClosedDB(t)),
		log:          log,
		workers:      map[uuid.UUID]*workerEntry{st: {}},
	}

	s.recordStreamDownSync(st)

	require.Equal(t, 1, logs.FilterMessage("supervisor: record down failed").Len())
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Nil(t, s.workers[st].lastDownID, "sem down gravado, nada é carimbado no entry")
}

// publishIndexReloadForCampaign é fire-and-forget: falha de NATS ou de listagem
// só vira warn (o reconcile periódico é a rede de segurança).
func TestCovS_PublishIndexReload_FailuresAreLogged(t *testing.T) {
	f := covSNewFix(t)
	st := f.station()
	camp := f.campaign("ativa", -1, 10, st)
	f.material(camp, 30, st)
	f.commercial(camp, 15, st)

	t.Run("barramento fechado", func(t *testing.T) {
		f.nc.Close()
		f.s.publishIndexReloadForCampaign(f.ctx, camp)
		require.Equal(t, 1, f.logs.FilterMessage("supervisor: index reload publish (material) failed").Len())
		require.Equal(t, 1, f.logs.FilterMessage("supervisor: index reload publish (commercial) failed").Len())
	})
	t.Run("listagem de materiais falha", func(t *testing.T) {
		f.s.materials = catalog.NewMaterials(covSClosedDB(t))
		f.s.publishIndexReloadForCampaign(f.ctx, camp)
		require.Equal(t, 1, f.logs.FilterMessage("supervisor: list ready materials for index reload failed").Len())
	})
}

// A gravação forense da supressão é best-effort: se falhar, a decisão do dedup
// (suprimir) continua valendo e nada é publicado.
func TestCovS_SubmitDetection_SuppressionRecordFailure_StillSuppresses(t *testing.T) {
	sc := covSNewDisambScene(t)
	_, short60 := sc.mat(60)
	_, short15 := sc.mat(15)
	sc.s.dedupSuppressions = catalog.NewDedupSuppressions(covSClosedDB(t))

	sc.submit(short60, sc.t0, 0.8)
	sc.submit(short15, sc.t0.Add(2*time.Second), 0.7)

	require.Len(t, sc.confirmed(), 1)
	require.Equal(t, 1, sc.logs.FilterMessage("supervisor: record dedup suppression failed").Len())
}

// Retratar sem barramento: loga a falha de publicação e não propaga pânico.
func TestCovS_Retract_PublishFailure_IsLogged(t *testing.T) {
	sc := covSNewDisambScene(t)
	_, short30 := sc.mat(30)
	det, _ := sc.detect(short30, sc.t0, 0.9)
	s2 := &Supervisor{db: sc.pool, nc: nil, log: sc.s.log}

	require.NotPanics(t, func() { s2.retract(sc.ctx, det, "longer_cut_detected", 9) })

	require.Equal(t, 1, sc.logs.FilterMessage("supervisor: publish retracted failed").Len())
}

// startStationWorker devolve (e não registra nada) quando uma leitura do
// catálogo falha — cada uma com a mensagem da etapa que quebrou.
func TestCovS_StartStationWorker_CatalogReadFailures(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(s *Supervisor, pool *pgxpool.Pool)
		want   string
	}{
		{"campanhas ativas", func(s *Supervisor, p *pgxpool.Pool) { s.campaigns = catalog.NewCampaigns(p) }, "active campaigns for station"},
		{"commercials prontos", func(s *Supervisor, p *pgxpool.Pool) { s.commercials = catalog.NewCommercials(p) }, "list ready commercials"},
		{"materiais prontos", func(s *Supervisor, p *pgxpool.Pool) { s.materials = catalog.NewMaterials(p) }, "list ready materials"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := covSNewFix(t)
			st := f.station()
			camp := f.campaign("ativa", -1, 10, st) // precisa de campanha ativa pra chegar nas listagens
			f.material(camp, 30, st)
			c.break_(f.s, covSClosedDB(t))

			err := f.s.startStationWorker(f.ctx, st)

			require.Error(t, err)
			require.Contains(t, err.Error(), c.want)
			require.False(t, f.hasEntry(st), "nada é registrado quando a leitura falha")
		})
	}
}

// Down events zumbis (mais de um aberto) são fechados na partida; falha ao
// fechar só vira warn e o worker sobe mesmo assim.
func TestCovS_StartStationWorker_StartupRecovery(t *testing.T) {
	t.Run("fecha down events zumbis", func(t *testing.T) {
		f := covSNewFix(t)
		st := f.station()
		camp := f.campaign("ativa", -1, 10, st)
		f.material(camp, 30, st)
		for i := 0; i < 3; i++ {
			_, _, err := f.s.healthEvents.RecordDown(f.ctx, st)
			require.NoError(t, err)
		}

		require.NoError(t, f.s.startStationWorker(f.ctx, st))

		var open int
		require.NoError(t, f.pool.QueryRow(f.ctx,
			`SELECT count(*) FROM stream_health_events
			  WHERE station_id=$1 AND event_type='down' AND duration_seconds IS NULL`, st).Scan(&open))
		require.LessOrEqual(t, open, 1, "invariante: no máximo 1 down aberto por emissora")
		require.Equal(t, 1, f.logs.FilterMessage("supervisor: startup recovery — closed orphan down events").Len())
	})
	t.Run("falha ao fechar zumbis nao impede o worker", func(t *testing.T) {
		f := covSNewFix(t)
		st := f.station()
		camp := f.campaign("ativa", -1, 10, st)
		f.material(camp, 30, st)
		f.s.healthEvents = catalog.NewHealthEvents(covSClosedDB(t))

		require.NoError(t, f.s.startStationWorker(f.ctx, st))

		require.NotNil(t, f.worker(st))
		require.Equal(t, 1, f.logs.FilterMessage("supervisor: startup recovery — failed to close orphaned downs").Len())
	})
}

// UpdateStations: falha ao subir o worker de uma emissora (aqui, inexistente)
// é logada e não aborta a atualização do restante.
func TestCovS_UpdateStations_WorkerStartFailureIsLoggedNotFatal(t *testing.T) {
	f := covSNewFix(t)
	good := f.station()
	ghost := uuid.New()
	camp := f.campaign("ativa", -1, 10, good)
	f.material(camp, 30, good)

	require.NoError(t, f.s.UpdateStations(camp, []uuid.UUID{ghost, good}))

	require.NotNil(t, f.worker(good))
	require.False(t, f.hasEntry(ghost))
	require.Equal(t, 1, f.logs.FilterMessage("supervisor.UpdateStations: start worker failed").Len())
}

// Reconciler da frota: falha ao sincronizar monitoring_status é só warn.
func TestCovS_ReconcileFleetOnce_StatusSyncFailureIsLogged(t *testing.T) {
	f := covSNewFix(t)
	f.s.stations = catalog.NewStations(covSClosedDB(t))

	prev := map[uuid.UUID]struct{}{}
	got := f.s.reconcileFleetOnce(f.ctx, prev, func(context.Context, uuid.UUID) error { return nil })

	require.NotNil(t, got)
	require.Equal(t, 1, f.logs.FilterMessage("supervisor.fleet: monitoring_status sync failed").Len())
}

// O rebuild disparado pela deriva pode falhar (aqui, sem diretório de
// segmentos): vira log de erro e o reconciler já retornou true.
func TestCovS_ReconcileOnce_RebuildFailureIsLogged(t *testing.T) {
	sc := covSNewReconcileScene(t, "", nil)
	blocker := t.TempDir() + "/arquivo"
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
	sc.s.segmentsRoot = blocker

	require.True(t, sc.s.reconcileOnce(sc.ctx, sc.station, sc.station.String()))

	sc.wait(func() bool { return sc.logs.FilterMessage("supervisor.reconcile: rebuild failed").Len() == 1 },
		"o erro do rebuild deveria ser logado")
	require.False(t, sc.hasEntry(sc.station), "sem worker novo e o antigo já saiu do mapa")
}

// DedupBuffer.Add/Replace carimbam InsertedAt quando o chamador não informa.
func TestCovS_DedupBuffer_StampsInsertedAtWhenZero(t *testing.T) {
	b := NewDedupBuffer(time.Minute)
	station, client := uuid.New(), uuid.New()
	mk := func(short int32) DedupEntry {
		return DedupEntry{ClientID: client, DurationSeconds: 30,
			Detection: matchDetection(station, short)}
	}
	before := time.Now()

	b.Add(mk(1))
	got := b.Find(station, client, time.Time{}, 30)
	require.NotNil(t, got)
	require.False(t, got.InsertedAt.Before(before), "Add carimba InsertedAt=now")

	old := *got
	require.True(t, b.Replace(&old, mk(2)))
	got = b.Find(station, client, time.Time{}, 30)
	require.EqualValues(t, 2, got.Detection.CommercialShortID)
	require.False(t, got.InsertedAt.Before(before), "Replace também carimba InsertedAt")
	require.Equal(t, 1, b.Len())
}
