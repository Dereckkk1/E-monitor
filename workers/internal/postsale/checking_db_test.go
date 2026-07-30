package postsale

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStationRows_ClassificaEAgrega(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)

	rows, err := NewRepo(pool).StationRows(ctx, seed.CampaignID,
		date(2026, 6, 1), date(2026, 6, 30))
	require.NoError(t, err)
	require.Len(t, rows, 3)

	byName := map[string]StationRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}

	acima := byName["Radio Acima"]
	require.Equal(t, KindAbove, acima.Kind)
	require.Equal(t, 3, acima.BonusCount)
	require.Equal(t, 3, acima.Extras)
	require.Equal(t, 0, acima.Deficit)
	require.NotNil(t, acima.DeliveryPct)
	require.Equal(t, 100, *acima.DeliveryPct)

	devendo := byName["Radio Devendo"]
	require.Equal(t, KindCompensation, devendo.Kind)
	require.Equal(t, 3, devendo.Deficit)
	require.False(t, devendo.Compensated, "sem extras não há o que compensar")
	require.NotNil(t, devendo.DeliveryPct)
	require.Equal(t, 70, *devendo.DeliveryPct)

	exata := byName["Radio Exata"]
	require.Equal(t, KindConforming, exata.Kind)
	require.Equal(t, 0, exata.Deficit)
	require.Equal(t, 0, exata.Extras)

	// O metadado da emissora vem junto — é o que o card do Checking mostra.
	require.Equal(t, "Passo Fundo", derefStr(acima.City))
	require.Equal(t, "RS", derefStr(acima.State))
	require.Equal(t, "FM", derefStr(acima.Band))
	require.NotNil(t, acima.FrequencyMHz)
	require.InDelta(t, 98.5, *acima.FrequencyMHz, 0.001)
}

// O recorte de período é a razão de existir do pós-venda: pedir uma janela que
// não contém as veiculações tem que devolver déficit, não silêncio.
func TestStationRows_RespeitaOPeriodo(t *testing.T) {
	ctx, pool := newTestDB(t)
	seed := seedScenario(t, ctx, pool)

	// 06 a 30/06: fora da vigência da regra (01–05) e sem nenhuma veiculação.
	rows, err := NewRepo(pool).StationRows(ctx, seed.CampaignID,
		date(2026, 6, 6), date(2026, 6, 30))
	require.NoError(t, err)
	require.Empty(t, rows, "nenhum plano e nenhuma tocada na janela")

	// Só o primeiro dia: expected 2, in_slot 2 nas que entregaram.
	rows, err = NewRepo(pool).StationRows(ctx, seed.CampaignID,
		date(2026, 6, 1), date(2026, 6, 1))
	require.NoError(t, err)
	require.Len(t, rows, 3)
	for _, r := range rows {
		require.Equal(t, 2, r.Programmed, r.Name)
		require.Equal(t, 2, r.Identified, r.Name)
	}
}
