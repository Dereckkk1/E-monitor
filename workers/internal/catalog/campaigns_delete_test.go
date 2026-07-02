package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
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
		[]Projection{{CampaignID: campB.ID, CommercialID: matB.ID, Category: "orphan"}}))

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
		[]Projection{{CampaignID: camp.ID, CommercialID: mat.ID, Category: "orphan"}}))

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
