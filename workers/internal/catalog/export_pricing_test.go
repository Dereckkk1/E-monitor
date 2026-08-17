package catalog

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestIterateForExport_ShortIDEPrecoUnitario prova que as duas colunas novas do
// CSV detalhado ("Identificador" e "Preço") chegam preenchidas — e que o preço
// só existe quando o pricing da emissora está em modo per_insertion.
func TestIterateForExport_ShortIDEPrecoUnitario(t *testing.T) {
	ctx, pool, campID, matID, statID := seedAirtimeFixture(t, "ExportPricing")

	// O material precisa de um tipo: o preço unitário é por TIPO de material.
	typeID := seedType(t, ctx, pool, "Spot 30s ExportPricing")
	_, err := pool.Exec(ctx, `UPDATE materials SET type_id = $2 WHERE id = $1`, matID, typeID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_station_pricing (campaign_id, station_id, mode)
		VALUES ($1, $2, 'per_insertion')`, campID, statID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO campaign_station_type_pricing (campaign_id, station_id, type_id, unit_value)
		VALUES ($1, $2, $3, 6.00)`, campID, statID, typeID)
	require.NoError(t, err)

	detectedAt := time.Now().Add(-1 * time.Hour)
	dets := NewDetections(pool)
	_, err = dets.Create(ctx, CreateDetectionInput{
		StationID: statID, CommercialID: matID, CampaignID: campID,
		DetectedAt: detectedAt, Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
	})
	require.NoError(t, err)

	collect := func() []DetectionEnriched {
		var got []DetectionEnriched
		require.NoError(t, dets.IterateForExport(ctx,
			ListPagedFilter{CampaignID: &campID},
			func(d DetectionEnriched) error { got = append(got, d); return nil }))
		return got
	}

	got := collect()
	require.Len(t, got, 1)
	require.NotNil(t, got[0].StationShortID, "Identificador não pode vir vazio")
	require.Greater(t, *got[0].StationShortID, int32(0))
	require.NotNil(t, got[0].UnitPrice, "per_insertion tem valor unitário")
	require.InDelta(t, 6.00, *got[0].UnitPrice, 0.001)

	// Em modo consolidated não existe valor por inserção: o CASE da query tem
	// que devolver NULL mesmo com a linha de type_pricing ainda na tabela.
	_, err = pool.Exec(ctx, `
		UPDATE campaign_station_pricing SET mode = 'consolidated', consolidated_value = 100
		WHERE campaign_id = $1 AND station_id = $2`, campID, statID)
	require.NoError(t, err)

	got = collect()
	require.Len(t, got, 1)
	require.Nil(t, got[0].UnitPrice, "consolidated não tem preço por inserção")
}
