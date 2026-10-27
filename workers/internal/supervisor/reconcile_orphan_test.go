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
	"radiocheck/internal/evidence"
	"radiocheck/internal/ingestor"
)

// reconcileFixture monta um Supervisor com repos reais e UM worker (não
// rodando — só o entry no mapa) para uma emissora recém-criada no DB.
type reconcileFixture struct {
	ctx       context.Context
	pool      *pgxpool.Pool
	s         *Supervisor
	stationID uuid.UUID
	clientID  uuid.UUID
	workerCtx context.Context
}

func newReconcileFixture(t *testing.T) *reconcileFixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })

	st, err := catalog.NewStations(pool).Create(ctx, catalog.CreateStationInput{
		Name:      "reconcile-orphan-" + uuid.NewString(),
		Band:      "FM",
		StreamURL: "http://reconcile-orphan.invalid/stream",
	})
	require.NoError(t, err)
	cli, err := catalog.NewClients(pool).Create(ctx, catalog.CreateClientInput{Name: "reconcile-orphan-" + uuid.NewString()})
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id=$1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id=$1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id=$1`, st.ID)
	})
	_, err = pool.Exec(ctx, `UPDATE stations SET monitoring_status='active' WHERE id=$1`, st.ID)
	require.NoError(t, err)

	workerCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	w := ingestor.NewWorker(ingestor.WorkerConfig{StationID: st.ID, StreamURL: st.StreamURL}, nil, nil, zap.NewNop())

	s := &Supervisor{
		campaigns:   catalog.NewCampaigns(pool),
		stations:    catalog.NewStations(pool),
		commercials: catalog.NewCommercials(pool),
		materials:   catalog.NewMaterials(pool),
		evidence:    &evidence.Service{},
		log:         zap.NewNop(),
		workers: map[uuid.UUID]*workerEntry{
			st.ID: {worker: w, cancel: cancel, startedAt: time.Now()},
		},
		lastStallRestart:   make(map[uuid.UUID]time.Time),
		stallRestartCounts: make(map[uuid.UUID]uint32),
		connectFailures:    make(map[uuid.UUID]uint32),
	}
	return &reconcileFixture{ctx: ctx, pool: pool, s: s, stationID: st.ID, clientID: cli.ID, workerCtx: workerCtx}
}

func (f *reconcileFixture) monitoringStatus(t *testing.T) string {
	t.Helper()
	var ms string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT monitoring_status FROM stations WHERE id=$1`, f.stationID).Scan(&ms))
	return ms
}

// TestReconcileOnce_StopsWorkerWithoutActiveCampaign: quando nenhuma campanha
// 'ativa' cobre mais a emissora (ex.: a campanha voltou pra 'programada'), o
// reconciler tem que PARAR o worker — não recriá-lo com lista de comerciais
// vazia, que deixava o ffmpeg puxando o stream indefinidamente (incidente
// "FSJ - Pedido Jack", 2026-10-01: 239 emissoras monitoradas um dia antes).
func TestReconcileOnce_StopsWorkerWithoutActiveCampaign(t *testing.T) {
	f := newReconcileFixture(t)

	// Só uma campanha programada (início futuro) mira a emissora.
	_, err := f.pool.Exec(f.ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status, target_stations)
		VALUES ($1, $2, CURRENT_DATE + 5, CURRENT_DATE + 20, 'programada', ARRAY[$3::uuid])`,
		f.clientID, "orphan-prog-"+uuid.NewString(), f.stationID)
	require.NoError(t, err)

	stopped := f.s.reconcileOnce(f.ctx, f.stationID, f.stationID.String())

	require.True(t, stopped, "reconcileOnce must report the worker as handled")
	f.s.mu.Lock()
	_, stillThere := f.s.workers[f.stationID]
	f.s.mu.Unlock()
	require.False(t, stillThere, "worker entry must be removed, not rebuilt")
	require.Error(t, f.workerCtx.Err(), "worker context must be cancelled")
	require.Equal(t, "paused", f.monitoringStatus(t))

	// Dá tempo pra um eventual respawn assíncrono aparecer — não pode haver.
	time.Sleep(200 * time.Millisecond)
	f.s.mu.Lock()
	_, respawned := f.s.workers[f.stationID]
	f.s.mu.Unlock()
	require.False(t, respawned, "no worker may be respawned for a station without active campaign")
}

// TestReconcileOnce_KeepsWorkerWithActiveCampaignButNoReadyMaterial: emissora
// coberta por campanha 'ativa' cujo material ainda não tem fingerprint pronto
// tem lista de comerciais vazia, mas o worker deve CONTINUAR — o material
// pode ficar pronto a qualquer momento e o reconciler o carrega em ≤30s.
func TestReconcileOnce_KeepsWorkerWithActiveCampaignButNoReadyMaterial(t *testing.T) {
	f := newReconcileFixture(t)

	_, err := f.pool.Exec(f.ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status, target_stations)
		VALUES ($1, $2, CURRENT_DATE - 1, CURRENT_DATE + 20, 'ativa', ARRAY[$3::uuid])`,
		f.clientID, "orphan-ativa-"+uuid.NewString(), f.stationID)
	require.NoError(t, err)

	restarted := f.s.reconcileOnce(f.ctx, f.stationID, f.stationID.String())

	require.False(t, restarted, "nothing drifted: worker must be kept as-is")
	f.s.mu.Lock()
	_, stillThere := f.s.workers[f.stationID]
	f.s.mu.Unlock()
	require.True(t, stillThere)
	require.NoError(t, f.workerCtx.Err(), "worker context must NOT be cancelled")
	require.Equal(t, "active", f.monitoringStatus(t))
}
