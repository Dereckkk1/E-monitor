package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Paridade Go(insert) × SQL(recat) do caso novo (spec 2026-07-13): material
// carved toca DENTRO do range da regra dele mas em dia sem meta (sábado, regra
// seg-sex) → orphan nas DUAS bordas. O insert-path (categorizer.Categorize) e o
// recat-path (recatClassifyTailSQL) têm que concordar — divergir é bug silencioso.
func TestCarveOut_InPeriodWrongWeekday_Orphan_InsertAndRecat(t *testing.T) {
	ctx, pool := newTestDB(t)
	saoPaulo, err := time.LoadLocation("America/Sao_Paulo")
	require.NoError(t, err)

	// Sábado mais recente <= agora (garante partição do mês existente).
	sat := time.Now().In(saoPaulo)
	for sat.Weekday() != time.Saturday {
		sat = sat.AddDate(0, 0, -1)
	}
	sat = time.Date(sat.Year(), sat.Month(), sat.Day(), 12, 0, 0, 0, saoPaulo)
	rangeStart := sat.AddDate(0, 0, -6) // domingo anterior — cobre o sábado
	rangeEnd := sat.AddDate(0, 0, 6)    // sexta seguinte

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "carve-cli"})
	require.NoError(t, err)
	cmp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "carve-camp", ClientID: cli.ID,
		StartDate: rangeStart, EndDate: rangeEnd, TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	typeID := seedType(t, ctx, pool, "carve-spot")
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "carve-M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/cv", MasterSHA256: "carve-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Carve FM", Band: "FM", StreamURL: "http://example.com/carve",
	})
	require.NoError(t, err)

	// Regra carve-out (material_ids = [mat]) seg-sex (mask 62), faixa ampla.
	rules := NewDistributionRules(pool)
	_, err = rules.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, TypeID: typeID,
		StationIDs:  []uuid.UUID{stat.ID},
		MaterialIDs: []uuid.UUID{mat.ID},
		StartDate:   rangeStart, EndDate: rangeEnd,
		WeekdayMask: 62, TimeStart: "08:00", TimeEnd: "22:00", PlaysPerDay: 5,
	})
	require.NoError(t, err)

	det, err := NewDetections(pool).Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
		DetectedAt: sat, MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detections WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, mat.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})

	catOf := func() (string, string) {
		var base, proj string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT category FROM detections WHERE id=$1 AND detected_at=$2`, det.ID, det.DetectedAt).Scan(&base))
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT category FROM detection_campaigns WHERE detection_id=$1 AND campaign_id=$2`, det.ID, cmp.ID).Scan(&proj))
		return base, proj
	}

	// Insert-path (Go): sábado dentro do range da regra → orphan.
	base, proj := catOf()
	require.Equal(t, "orphan", base, "insert-path (Go) deve dar orphan")
	require.Equal(t, "orphan", proj, "projeção nasce orphan")

	// Recat-path (SQL): recategoriza a campanha → tem que CONTINUAR orphan.
	require.NoError(t, rules.RecategorizeForCampaign(ctx, cmp.ID))
	base, proj = catOf()
	require.Equal(t, "orphan", base, "recat-path (SQL) deve concordar com o insert (orphan)")
	require.Equal(t, "orphan", proj, "projeção recategorizada deve ser orphan")
}
