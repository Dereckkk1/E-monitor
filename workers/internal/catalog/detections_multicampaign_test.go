package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// seedSecondCampaign cria uma segunda campanha (mesmo cliente da fixture) com
// material próprio, pra provar a seleção múltipla de /reports/airtime.
func seedSecondCampaign(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campID uuid.UUID, label string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	clientID := uuid.MustParse(mustClientIDFromCampaign(t, pool, campID))

	cmp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C2-airtime-" + label, ClientID: clientID,
		StartDate: time.Now().AddDate(0, 0, -7),
		EndDate:   time.Now().AddDate(0, 0, 30),
	})
	require.NoError(t, err, "seed campanha 2")

	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: clientID, Title: "M2-" + label, DurationSeconds: 15,
		MasterStoragePath: "/tmp", MasterSHA256: "airtime2-" + label,
	})
	require.NoError(t, err, "seed material 2")

	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detections WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
	})
	return cmp.ID, mat.ID
}

// TestDetections_ListPaged_MultiplasCampanhas prova o que /reports/airtime
// passou a precisar: uma lista só com a UNIÃO das campanhas selecionadas, e
// cada linha dizendo de qual campanha veio (campaign_name).
func TestDetections_ListPaged_MultiplasCampanhas(t *testing.T) {
	ctx, pool, campA, matA, statID := seedAirtimeFixture(t, "MultiCampanha")
	dets := NewDetections(pool)
	campB, matB := seedSecondCampaign(t, ctx, pool, campA, "MultiCampanha")

	seed := func(campID, matID uuid.UUID, n int, offset time.Duration) {
		t.Helper()
		for i := 0; i < n; i++ {
			_, err := dets.Create(ctx, CreateDetectionInput{
				StationID: statID, CommercialID: matID, CampaignID: campID,
				DetectedAt:         time.Now().Add(offset - time.Duration(i)*time.Hour),
				MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
				Confidence: 0.95, HashCount: 100, TemporalCoverage: 0.85,
			})
			require.NoErrorf(t, err, "seed detecção %d", i)
		}
	}
	seed(campA, matA, 3, 0)
	seed(campB, matB, 2, -10*time.Minute)

	// União das duas.
	res, err := dets.ListPaged(ctx, ListPagedFilter{
		CampaignIDs: []uuid.UUID{campA, campB}, Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	require.Equal(t, 5, res.Total, "total tem que ser a soma das duas campanhas")

	byCampaign := map[uuid.UUID]int{}
	for _, d := range res.Data {
		byCampaign[d.CampaignID]++
		require.NotNil(t, d.CampaignName,
			"a linha precisa carregar o nome da campanha — é o que a lista mostra com seleção múltipla")
		require.NotEmpty(t, *d.CampaignName)
	}
	require.Equal(t, 3, byCampaign[campA])
	require.Equal(t, 2, byCampaign[campB])

	// Uma só continua recortando.
	only, err := dets.ListPaged(ctx, ListPagedFilter{
		CampaignIDs: []uuid.UUID{campB}, Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	require.Equal(t, 2, only.Total)

	// Slice vazio ≠ nil: seleção vazia não pode virar "lista inteira".
	none, err := dets.ListPaged(ctx, ListPagedFilter{
		CampaignIDs: []uuid.UUID{}, Page: 1, PageSize: 50,
	})
	require.NoError(t, err)
	require.Equal(t, 0, none.Total)
}

// TestDetections_AggregateByMaterial_MultiplasCampanhas cobre o painel lateral
// sob seleção múltipla (soma as campanhas) e o recorte por carteira do viewer,
// que agora é filtro SQL em vez de checagem campanha a campanha no handler.
func TestDetections_AggregateByMaterial_MultiplasCampanhas(t *testing.T) {
	ctx, pool, campA, matA, statID := seedAirtimeFixture(t, "AggMulti")
	dets := NewDetections(pool)
	campB, matB := seedSecondCampaign(t, ctx, pool, campA, "AggMulti")

	for i := 0; i < 4; i++ {
		_, err := dets.Create(ctx, CreateDetectionInput{
			StationID: statID, CommercialID: matA, CampaignID: campA,
			DetectedAt:         time.Now().Add(-time.Duration(i) * time.Minute),
			MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
			Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
		})
		require.NoError(t, err)
	}
	for i := 0; i < 2; i++ {
		_, err := dets.Create(ctx, CreateDetectionInput{
			StationID: statID, CommercialID: matB, CampaignID: campB,
			DetectedAt:         time.Now().Add(-time.Duration(30+i) * time.Minute),
			MatchStartOffsetMs: 0, MatchEndOffsetMs: 15000,
			Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
		})
		require.NoError(t, err)
	}

	res, err := dets.AggregateByMaterial(ctx, AggregateFilter{
		CampaignIDs: []uuid.UUID{campA, campB},
	})
	require.NoError(t, err)
	require.Equal(t, 6, res.TotalDetections)
	require.Equal(t, 2, res.DistinctMaterials)

	// Carteira do dono: enxerga tudo.
	clientID := uuid.MustParse(mustClientIDFromCampaign(t, pool, campA))
	mine, err := dets.AggregateByMaterial(ctx, AggregateFilter{
		CampaignIDs: []uuid.UUID{campA, campB},
		ClientIDs:   []uuid.UUID{clientID},
	})
	require.NoError(t, err)
	require.Equal(t, 6, mine.TotalDetections)

	// Carteira de outro cliente: campanha alheia não soma nada.
	other, err := dets.AggregateByMaterial(ctx, AggregateFilter{
		CampaignIDs: []uuid.UUID{campA, campB},
		ClientIDs:   []uuid.UUID{uuid.New()},
	})
	require.NoError(t, err)
	require.Equal(t, 0, other.TotalDetections)
	require.Empty(t, other.Data)
}
