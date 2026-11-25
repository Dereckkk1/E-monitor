package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCovC_ValidatePricingInput(t *testing.T) {
	tp := []TypePricing{{TypeID: uuid.New(), UnitValue: 10}}
	tests := []struct {
		name string
		in   UpsertInput
		want error
	}{
		{"consolidado ok", UpsertInput{Mode: PricingModeConsolidated, ConsolidatedValue: covCF(1000)}, nil},
		{"consolidado zero é válido", UpsertInput{Mode: PricingModeConsolidated, ConsolidatedValue: covCF(0)}, nil},
		{"consolidado sem valor", UpsertInput{Mode: PricingModeConsolidated}, ErrPricingConsolidatedRequiresValue},
		{"consolidado negativo", UpsertInput{Mode: PricingModeConsolidated, ConsolidatedValue: covCF(-0.01)}, ErrPricingNegativeValue},
		{"consolidado com per_type", UpsertInput{Mode: PricingModeConsolidated, ConsolidatedValue: covCF(5), PerType: tp}, ErrPricingMixedFields},
		{"per_insertion ok", UpsertInput{Mode: PricingModePerInsertion, PerType: tp}, nil},
		{"per_insertion com valor consolidado", UpsertInput{Mode: PricingModePerInsertion, ConsolidatedValue: covCF(5), PerType: tp}, ErrPricingMixedFields},
		{"per_insertion sem tipos", UpsertInput{Mode: PricingModePerInsertion}, ErrPricingPerInsertionRequiresTypes},
		{"per_insertion valor unitário negativo", UpsertInput{Mode: PricingModePerInsertion,
			PerType: []TypePricing{{TypeID: uuid.New(), UnitValue: -1}}}, ErrPricingNegativeValue},
		{"modo desconhecido", UpsertInput{Mode: "gratis"}, ErrPricingInvalidMode},
		{"modo vazio", UpsertInput{}, ErrPricingInvalidMode},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePricingInput(tc.in)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestCovC_Pricing_UpsertEListByCampaign(t *testing.T) {
	w := covCSeedWorld(t, "pricing-upsert")
	repo := NewPricing(w.pool)
	st2 := covCSeedStation(t, w, "Rádio Teste FM Dois")
	type2 := covCSeedType(t, w, "covc-pricing-t2")

	// Campanha sem preço: slice não-nil e vazio.
	got, err := repo.ListByCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got)

	cons, err := repo.Upsert(w.ctx, UpsertInput{
		CampaignID: w.camp, StationID: w.station,
		Mode: PricingModeConsolidated, ConsolidatedValue: covCF(1500),
	})
	require.NoError(t, err)
	assert.Equal(t, PricingModeConsolidated, cons.Mode)
	require.NotNil(t, cons.ConsolidatedValue)
	assert.InDelta(t, 1500, *cons.ConsolidatedValue, 0.001)
	assert.Empty(t, cons.PerType)

	per, err := repo.Upsert(w.ctx, UpsertInput{
		CampaignID: w.camp, StationID: st2, Mode: PricingModePerInsertion,
		PerType: []TypePricing{{TypeID: w.typeID, UnitValue: 12.5}, {TypeID: type2, UnitValue: 7}},
	})
	require.NoError(t, err)
	assert.Equal(t, PricingModePerInsertion, per.Mode)
	assert.Nil(t, per.ConsolidatedValue)
	require.Len(t, per.PerType, 2)

	list, err := repo.ListByCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	require.Len(t, list, 2)
	byStation := map[uuid.UUID]StationPricing{}
	for _, sp := range list {
		byStation[sp.StationID] = sp
	}
	assert.Equal(t, PricingModeConsolidated, byStation[w.station].Mode)
	assert.Empty(t, byStation[w.station].PerType)
	perRow := byStation[st2]
	assert.Equal(t, PricingModePerInsertion, perRow.Mode)
	units := map[uuid.UUID]float64{}
	for _, p := range perRow.PerType {
		units[p.TypeID] = p.UnitValue
	}
	assert.InDelta(t, 12.5, units[w.typeID], 0.001)
	assert.InDelta(t, 7, units[type2], 0.001)

	// Preço de outra campanha não vaza para esta.
	other, err := repo.ListByCampaign(w.ctx, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, other)
}

func TestCovC_Pricing_UpsertSubstituiDetalheEMudaModo(t *testing.T) {
	w := covCSeedWorld(t, "pricing-replace")
	repo := NewPricing(w.pool)
	type2 := covCSeedType(t, w, "covc-pricing-replace-t2")

	_, err := repo.Upsert(w.ctx, UpsertInput{
		CampaignID: w.camp, StationID: w.station, Mode: PricingModePerInsertion,
		PerType: []TypePricing{{TypeID: w.typeID, UnitValue: 10}, {TypeID: type2, UnitValue: 20}},
	})
	require.NoError(t, err)

	// Reenviar com um tipo só: o detalhe é substituído, não mesclado.
	got, err := repo.Upsert(w.ctx, UpsertInput{
		CampaignID: w.camp, StationID: w.station, Mode: PricingModePerInsertion,
		PerType: []TypePricing{{TypeID: type2, UnitValue: 33}},
	})
	require.NoError(t, err)
	require.Len(t, got.PerType, 1)
	assert.Equal(t, type2, got.PerType[0].TypeID)
	assert.InDelta(t, 33, got.PerType[0].UnitValue, 0.001)

	// Trocar para consolidado limpa o detalhe por tipo.
	got, err = repo.Upsert(w.ctx, UpsertInput{
		CampaignID: w.camp, StationID: w.station, Mode: PricingModeConsolidated,
		ConsolidatedValue: covCF(900),
	})
	require.NoError(t, err)
	assert.Equal(t, PricingModeConsolidated, got.Mode)
	assert.Empty(t, got.PerType)

	var n int
	require.NoError(t, w.pool.QueryRow(w.ctx,
		`SELECT count(*) FROM campaign_station_type_pricing WHERE campaign_id=$1 AND station_id=$2`,
		w.camp, w.station).Scan(&n))
	assert.Zero(t, n, "detalhe por tipo deve sumir ao virar consolidado")
}

func TestCovC_Pricing_UpsertValidaAntesDoBanco(t *testing.T) {
	w := covCSeedWorld(t, "pricing-invalid")
	repo := NewPricing(w.pool)

	_, err := repo.Upsert(w.ctx, UpsertInput{CampaignID: w.camp, StationID: w.station, Mode: "xpto"})
	require.ErrorIs(t, err, ErrPricingInvalidMode)

	_, err = repo.Upsert(w.ctx, UpsertInput{CampaignID: w.camp, StationID: w.station,
		Mode: PricingModeConsolidated, ConsolidatedValue: covCF(-5)})
	require.ErrorIs(t, err, ErrPricingNegativeValue)

	list, err := repo.ListByCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	assert.Empty(t, list, "entrada inválida não grava nada")
}

func TestCovC_Pricing_UpsertLoteAtomico(t *testing.T) {
	w := covCSeedWorld(t, "pricing-lote")
	repo := NewPricing(w.pool)
	st2 := covCSeedStation(t, w, "Rádio Teste FM Lote")

	// Um item inválido derruba o lote inteiro ANTES de abrir a transação.
	_, err := repo.UpsertLote(w.ctx, w.camp, []EntradaPreco{
		{StationID: w.station, Mode: PricingModeConsolidated, ConsolidatedValue: covCF(100)},
		{StationID: st2, Mode: PricingModePerInsertion}, // sem tipos
	})
	require.ErrorIs(t, err, ErrPricingPerInsertionRequiresTypes)
	list, err := repo.ListByCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	assert.Empty(t, list)

	// Falha no meio da gravação (tipo inexistente → FK) desfaz o lote todo.
	_, err = repo.UpsertLote(w.ctx, w.camp, []EntradaPreco{
		{StationID: w.station, Mode: PricingModeConsolidated, ConsolidatedValue: covCF(100)},
		{StationID: st2, Mode: PricingModePerInsertion,
			PerType: []TypePricing{{TypeID: uuid.New(), UnitValue: 1}}},
	})
	require.Error(t, err)
	list, err = repo.ListByCampaign(w.ctx, w.camp)
	require.NoError(t, err)
	assert.Empty(t, list, "transação com erro não deixa preço pela metade")

	// Lote válido: resultado vem na ORDEM DE ENTRADA, não na de station_id.
	out, err := repo.UpsertLote(w.ctx, w.camp, []EntradaPreco{
		{StationID: st2, Mode: PricingModePerInsertion, PerType: []TypePricing{{TypeID: w.typeID, UnitValue: 4}}},
		{StationID: w.station, Mode: PricingModeConsolidated, ConsolidatedValue: covCF(250)},
	})
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, st2, out[0].StationID)
	assert.Equal(t, w.station, out[1].StationID)
	require.Len(t, out[0].PerType, 1)
	assert.InDelta(t, 4, out[0].PerType[0].UnitValue, 0.001)

	// Lote vazio é válido e não devolve nada.
	empty, err := repo.UpsertLote(w.ctx, w.camp, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestCovC_Pricing_GetByCampaignStation(t *testing.T) {
	w := covCSeedWorld(t, "pricing-get")
	repo := NewPricing(w.pool)
	st2 := covCSeedStation(t, w, "Rádio Teste FM Get")

	_, err := repo.GetByCampaignStation(w.ctx, w.camp, w.station)
	require.ErrorIs(t, err, pgx.ErrNoRows, "sem preço cadastrado → ErrNoRows")

	_, err = repo.Upsert(w.ctx, UpsertInput{CampaignID: w.camp, StationID: w.station,
		Mode: PricingModeConsolidated, ConsolidatedValue: covCF(777)})
	require.NoError(t, err)
	got, err := repo.GetByCampaignStation(w.ctx, w.camp, w.station)
	require.NoError(t, err)
	assert.Equal(t, PricingModeConsolidated, got.Mode)
	assert.InDelta(t, 777, *got.ConsolidatedValue, 0.001)
	assert.NotNil(t, got.PerType)
	assert.Empty(t, got.PerType)

	_, err = repo.Upsert(w.ctx, UpsertInput{CampaignID: w.camp, StationID: st2,
		Mode: PricingModePerInsertion, PerType: []TypePricing{{TypeID: w.typeID, UnitValue: 9}}})
	require.NoError(t, err)
	got, err = repo.GetByCampaignStation(w.ctx, w.camp, st2)
	require.NoError(t, err)
	assert.Equal(t, PricingModePerInsertion, got.Mode)
	assert.Nil(t, got.ConsolidatedValue)
	require.Len(t, got.PerType, 1)
	assert.Equal(t, w.typeID, got.PerType[0].TypeID)
	assert.InDelta(t, 9, got.PerType[0].UnitValue, 0.001)
}

func TestCovC_Pricing_Delete(t *testing.T) {
	w := covCSeedWorld(t, "pricing-delete")
	repo := NewPricing(w.pool)

	_, err := repo.Upsert(w.ctx, UpsertInput{CampaignID: w.camp, StationID: w.station,
		Mode: PricingModePerInsertion, PerType: []TypePricing{{TypeID: w.typeID, UnitValue: 3}}})
	require.NoError(t, err)

	require.NoError(t, repo.Delete(w.ctx, w.camp, w.station))
	_, err = repo.GetByCampaignStation(w.ctx, w.camp, w.station)
	require.ErrorIs(t, err, pgx.ErrNoRows)

	var n int
	require.NoError(t, w.pool.QueryRow(w.ctx,
		`SELECT count(*) FROM campaign_station_type_pricing WHERE campaign_id=$1 AND station_id=$2`,
		w.camp, w.station).Scan(&n))
	assert.Zero(t, n, "cascade apaga o detalhe por tipo")

	// Apagar o que não existe não é erro (idempotente).
	require.NoError(t, repo.Delete(w.ctx, w.camp, w.station))
}

func TestCovC_Pricing_ErroDeBancoPropaga(t *testing.T) {
	w := covCSeedWorld(t, "pricing-dberr")
	repo := NewPricing(w.pool)
	ctx := covCCancelled()

	_, err := repo.ListByCampaign(ctx, w.camp)
	require.Error(t, err)
	_, err = repo.GetByCampaignStation(ctx, w.camp, w.station)
	require.Error(t, err)
	require.Error(t, repo.Delete(ctx, w.camp, w.station))
	_, err = repo.UpsertLote(ctx, w.camp, []EntradaPreco{
		{StationID: w.station, Mode: PricingModeConsolidated, ConsolidatedValue: covCF(1)}})
	require.Error(t, err)
}
