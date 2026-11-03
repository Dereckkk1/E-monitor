package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// TestCampaigns_Delete_BlockedWhenDetectionCarriesOtherCampaignProjection:
// com MULTI_ATTRIBUTION, uma tocada física (detections) é "dona" de UMA
// campanha (campaign_id) mas carrega projeções fan-out de OUTRAS campanhas em
// detection_campaigns. Campaigns.Delete faz DELETE FROM detections WHERE
// campaign_id=$1 e o CASCADE (FK detection_campaigns→detections) varre também
// as projeções das irmãs — perda irreversível de histórico alheio (audit C1).
// O guard deve BLOQUEAR o delete nesse caso (igual Clients.Delete → 409).
func TestCampaigns_Delete_BlockedWhenDetectionCarriesOtherCampaignProjection(t *testing.T) {
	ctx, pool := newTestDB(t)
	campaigns := NewCampaigns(pool)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "del-guard-cli"})
	require.NoError(t, err)
	campA, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "del-guard-A", ClientID: cli.ID,
		StartDate: time.Now().AddDate(0, 0, -7), EndDate: time.Now().AddDate(0, 0, 30),
	})
	require.NoError(t, err)
	campB, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "del-guard-B", ClientID: cli.ID,
		StartDate: time.Now().AddDate(0, 0, -7), EndDate: time.Now().AddDate(0, 0, 30),
	})
	require.NoError(t, err)
	matA, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "del-guard-A", DurationSeconds: 30,
		MasterStoragePath: "/tmp/a", MasterSHA256: "del-guard-a-" + uuid.NewString(),
	})
	require.NoError(t, err)
	matB, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "del-guard-B", DurationSeconds: 30,
		MasterStoragePath: "/tmp/b", MasterSHA256: "del-guard-b-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Del Guard FM", Band: "FM", StreamURL: "http://example.com/delguard",
	})
	require.NoError(t, err)

	// Tocada física DONA da campanha A.
	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: matA.ID, CampaignID: campA.ID,
		DetectedAt: time.Now(), MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)
	// Projeção fan-out da campanha B na MESMA tocada física.
	require.NoError(t, NewDetectionCampaigns(pool).InsertProjections(ctx, det.ID, det.DetectedAt,
		[]Projection{{CampaignID: campB.ID, CommercialID: matB.ID, Category: "bonus"}}))

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detection_campaigns WHERE campaign_id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM detections WHERE campaign_id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id IN ($1,$2)`, matA.ID, matB.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	// Deletar A destruiria a projeção da B via CASCADE → deve ser bloqueado.
	err = campaigns.Delete(ctx, campA.ID)
	require.ErrorIs(t, err, ErrCampaignHasForeignProjections)

	var aExists bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM campaigns WHERE id=$1)`, campA.ID).Scan(&aExists))
	require.True(t, aExists, "campanha A não deve ter sido deletada")
	var bProj int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM detection_campaigns WHERE campaign_id=$1`, campB.ID).Scan(&bProj))
	require.Equal(t, 1, bProj, "projeção da campanha B deve permanecer intacta")
}

// TestCampaigns_Delete_AllowedWhenNoForeignProjections: o guard não pode
// bloquear demais — uma campanha cujas tocadas só carregam a projeção dela
// mesma (nenhuma projeção de outra campanha) deve deletar normalmente.
func TestCampaigns_Delete_AllowedWhenNoForeignProjections(t *testing.T) {
	ctx, pool := newTestDB(t)
	campaigns := NewCampaigns(pool)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "del-ok-cli"})
	require.NoError(t, err)
	camp, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "del-ok", ClientID: cli.ID,
		StartDate: time.Now().AddDate(0, 0, -7), EndDate: time.Now().AddDate(0, 0, 30),
	})
	require.NoError(t, err)
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "del-ok", DurationSeconds: 30,
		MasterStoragePath: "/tmp/ok", MasterSHA256: "del-ok-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Del OK FM", Band: "FM", StreamURL: "http://example.com/delok",
	})
	require.NoError(t, err)
	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: camp.ID,
		DetectedAt: time.Now(), MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)
	// Só a projeção da própria campanha (base).
	require.NoError(t, NewDetectionCampaigns(pool).InsertProjections(ctx, det.ID, det.DetectedAt,
		[]Projection{{CampaignID: camp.ID, CommercialID: mat.ID, Category: "bonus"}}))

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detection_campaigns WHERE campaign_id = $1`, camp.ID)
		pool.Exec(ctx, `DELETE FROM detections WHERE campaign_id = $1`, camp.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, mat.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, camp.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	require.NoError(t, campaigns.Delete(ctx, camp.ID), "delete sem projeção alheia deve funcionar")
	var exists bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM campaigns WHERE id=$1)`, camp.ID).Scan(&exists))
	require.False(t, exists, "campanha deve ter sido deletada")
}

// seedHistoricoDeCampanha cria o histórico pedido em campanha camp (a campanha, o material e a
// emissora já existem). Devolve a tocada, quando o histórico é uma.
func seedHistoricoDeCampanha(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tipo string, camp, outra, cli, mat, est uuid.UUID) {
	t.Helper()
	quando := time.Now()
	switch tipo {
	case "veiculação":
		insSeedDetection(t, ctx, pool, camp, mat, est, "in_slot", quando.Format("2006-01-02"))
	case "projeção":
		// Tocada de OUTRA campanha que carrega a projeção desta.
		var det uuid.UUID
		var detectedAt time.Time
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at, match_start_offset_ms, match_end_offset_ms,
			                        confidence, hash_count, temporal_coverage, variant_used, rate_used, category)
			VALUES ($1, $2, $3, $4, 0, 30000, 0.95, 100, 0.85, 0, 0, 'in_slot') RETURNING id, detected_at`,
			est, mat, outra, quando).Scan(&det, &detectedAt))
		_, err := pool.Exec(ctx, `INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
			VALUES ($1, $2, $3, $4, 'bonus')`, det, detectedAt, camp, mat)
		require.NoError(t, err)
	case "comprovante manual":
		_, err := pool.Exec(ctx, `INSERT INTO manual_proof_batches (campaign_id, station_id, proof_pdf_key, proof_pdf_size) VALUES ($1,$2,'k',1)`, camp, est)
		require.NoError(t, err)
	case "pós-venda":
		var rep uuid.UUID
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO post_sale_reports (client_id, title) VALUES ($1, 'Fechamento') RETURNING id`, cli).Scan(&rep))
		_, err := pool.Exec(ctx, `INSERT INTO post_sale_report_campaigns (report_id, campaign_id, period_from, period_to) VALUES ($1,$2,'2026-10-01','2026-10-31')`, rep, camp)
		require.NoError(t, err)
	default:
		t.Fatalf("histórico desconhecido: %s", tipo)
	}
}

// DeleteSemHistorico confere o histórico DENTRO da transação do delete: com qualquer dos quatro
// tipos o delete aborta com ErrCampaignHasHistory e NADA é apagado (nem a campanha, nem as
// tocadas); sem histórico apaga como o Delete.
func TestCampaigns_DeleteSemHistorico_RecusaQuemTemHistoricoEApagaQuemNao(t *testing.T) {
	ctx, pool := newTestDB(t)
	campaigns := NewCampaigns(pool)
	cli := insSeedClient(t, ctx, pool, "dsh-cli-"+uuid.NewString()[:6])
	est := insSeedStationNoProfile(t, ctx, pool, "DSH FM "+uuid.NewString()[:6])
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "dsh-"+uuid.NewString()[:6])

	for _, tipo := range []string{"veiculação", "projeção", "comprovante manual", "pós-venda"} {
		t.Run(tipo, func(t *testing.T) {
			camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
			outra := insSeedCampaign(t, ctx, pool, cli, "2026-11-01", "2026-11-30")
			seedHistoricoDeCampanha(t, ctx, pool, tipo, camp, outra, cli, mat, est)
			t.Cleanup(func() {
				pool.Exec(ctx, `DELETE FROM post_sale_reports WHERE client_id = $1`, cli)
				pool.Exec(ctx, `DELETE FROM manual_proof_batches WHERE campaign_id IN ($1,$2)`, camp, outra)
				pool.Exec(ctx, `DELETE FROM detections WHERE campaign_id IN ($1,$2)`, camp, outra)
			})
			var antes int
			require.NoError(t, pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM detections WHERE campaign_id IN ($1,$2)) +
				(SELECT count(*) FROM detection_campaigns WHERE campaign_id IN ($1,$2))`, camp, outra).Scan(&antes))

			err := campaigns.DeleteSemHistorico(ctx, camp)

			require.ErrorIs(t, err, ErrCampaignHasHistory)
			var existe bool
			require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM campaigns WHERE id = $1)`, camp).Scan(&existe))
			require.True(t, existe, "a campanha com histórico não pode ser apagada")
			var depois int
			require.NoError(t, pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM detections WHERE campaign_id IN ($1,$2)) +
				(SELECT count(*) FROM detection_campaigns WHERE campaign_id IN ($1,$2))`, camp, outra).Scan(&depois))
			require.Equal(t, antes, depois, "nenhuma veiculação foi tocada")
		})
	}

	t.Run("sem histórico apaga", func(t *testing.T) {
		camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
		require.NoError(t, campaigns.DeleteSemHistorico(ctx, camp))
		var existe bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM campaigns WHERE id = $1)`, camp).Scan(&existe))
		require.False(t, existe)
	})
	t.Run("campanha que não existe", func(t *testing.T) {
		require.ErrorIs(t, campaigns.DeleteSemHistorico(ctx, uuid.New()), pgx.ErrNoRows)
	})
}

// A corrida que a conferência prévia não pega: uma veiculação está sendo persistida (INSERT numa
// transação aberta, ainda sem commit) quando o delete começa. O FOR UPDATE da campanha espera esse
// INSERT (que tomou FOR KEY SHARE pela FK), e quando ele commita o EXISTS enxerga a veiculação: o
// delete aborta e a veiculação SOBREVIVE. Sem a trava, o DELETE FROM detections a varreria.
func TestCampaigns_DeleteSemHistorico_EsperaAVeiculacaoEmVooEPreservaOHistorico(t *testing.T) {
	ctx, pool := newTestDB(t)
	campaigns := NewCampaigns(pool)
	cli := insSeedClient(t, ctx, pool, "dsh-voo-"+uuid.NewString()[:6])
	camp := insSeedCampaign(t, ctx, pool, cli, "2026-10-01", "2026-10-31")
	est := insSeedStationNoProfile(t, ctx, pool, "DSH Voo FM "+uuid.NewString()[:6])
	_, mat := insSeedTypeAndMaterial(t, ctx, pool, cli, "dsh-voo-"+uuid.NewString()[:6])

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at, match_start_offset_ms, match_end_offset_ms,
		                        confidence, hash_count, temporal_coverage, variant_used, rate_used, category)
		VALUES ($1, $2, $3, now(), 0, 30000, 0.95, 100, 0.85, 0, 0, 'in_slot')`, est, mat, camp)
	require.NoError(t, err)

	feito := make(chan error, 1)
	go func() { feito <- campaigns.DeleteSemHistorico(ctx, camp) }()
	select {
	case err := <-feito:
		t.Fatalf("o delete não podia terminar com a veiculação em voo: %v", err)
	case <-time.After(500 * time.Millisecond):
	}

	require.NoError(t, tx.Commit(ctx))
	select {
	case err := <-feito:
		require.ErrorIs(t, err, ErrCampaignHasHistory)
	case <-time.After(10 * time.Second):
		t.Fatal("o delete não destravou depois do commit")
	}
	var dets int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM detections WHERE campaign_id = $1`, camp).Scan(&dets))
	require.Equal(t, 1, dets, "a veiculação persistida no meio sobrevive")
	var existe bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM campaigns WHERE id = $1)`, camp).Scan(&existe))
	require.True(t, existe)
}
