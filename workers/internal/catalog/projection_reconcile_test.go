package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Count acha a divergência; Heal cura; Count volta a zero. Cenário: projeção
// fan-out orphan + regra criada via repo (repo.Create NÃO dispara recat — quem
// dispara é o handler), ou seja, drift real como o do caso COPA.
func TestProjectionDrift_CountAndHeal(t *testing.T) {
	ctx, pool := newTestDB(t)
	saoPaulo, err := time.LoadLocation("America/Sao_Paulo")
	require.NoError(t, err)

	day := time.Now().In(saoPaulo)
	for day.Weekday() != time.Friday {
		day = day.AddDate(0, 0, -1)
	}
	day = time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, saoPaulo)
	rangeStart := day.AddDate(0, 0, -7)
	rangeEnd := day.AddDate(0, 0, 7)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "drift-cli"})
	require.NoError(t, err)
	campaigns := NewCampaigns(pool)
	campA, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "drift-base", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	campB, err := campaigns.Create(ctx, CreateCampaignInput{
		Name: "drift-secundaria", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)

	typeID := seedType(t, ctx, pool, "drift-spot")
	matA, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "drift-A", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/da", MasterSHA256: "drift-" + uuid.NewString(),
	})
	require.NoError(t, err)
	matB, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "drift-B", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/db", MasterSHA256: "drift-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Drift FM", Band: "FM", StreamURL: "http://example.com/drift",
	})
	require.NoError(t, err)

	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: matA.ID, CampaignID: campA.ID,
		DetectedAt: day, MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, 'orphan')`,
		det.ID, det.DetectedAt, campB.ID, matB.ID)
	require.NoError(t, err)

	rules := NewDistributionRules(pool)
	_, err = rules.Create(ctx, CreateDistributionRuleInput{
		CampaignID: campB.ID, TypeID: typeID,
		StationIDs: []uuid.UUID{stat.ID}, MaterialIDs: []uuid.UUID{matB.ID},
		StartDate: rangeStart, EndDate: rangeEnd,
		WeekdayMask: 127, TimeStart: "06:00", TimeEnd: "23:59", PlaysPerDay: 7,
	})
	require.NoError(t, err) // repo.Create não recategoriza → drift instalado

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detections WHERE id = $1`, det.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id IN ($1,$2)`, matA.ID, matB.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id IN ($1,$2)`, campA.ID, campB.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	since := day.Add(-2 * time.Hour)

	// Count: 1 divergência orphan→in_slot na campanha B.
	drifts, err := rules.CountProjectionDrift(ctx, since)
	require.NoError(t, err)
	found := false
	for _, dr := range drifts {
		if dr.CampaignID == campB.ID {
			require.Equal(t, "orphan", dr.From)
			require.Equal(t, "in_slot", dr.To)
			require.GreaterOrEqual(t, dr.N, int64(1))
			found = true
		}
	}
	require.True(t, found, "drift da campanha B tem que aparecer no Count")

	// Heal: cura >= 1 linha; Count da B volta a zero.
	healed, err := rules.HealProjectionDrift(ctx, since)
	require.NoError(t, err)
	require.GreaterOrEqual(t, healed, int64(1))

	drifts, err = rules.CountProjectionDrift(ctx, since)
	require.NoError(t, err)
	for _, dr := range drifts {
		require.NotEqual(t, campB.ID, dr.CampaignID, "depois do Heal a B não pode ter drift")
	}

	var projB string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`,
		det.ID, campB.ID).Scan(&projB))
	require.Equal(t, "in_slot", projB)
}
