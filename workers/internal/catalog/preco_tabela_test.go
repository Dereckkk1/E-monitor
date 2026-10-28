package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPrecoDeTabela(t *testing.T) {
	camp, e1, e2, e3, spot, outro := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cem := 5000.0
	precos := []StationPricing{
		{CampaignID: camp, StationID: e1, Mode: PricingModePerInsertion, PerType: []TypePricing{{TypeID: spot, UnitValue: 150}}},
		{CampaignID: camp, StationID: e2, Mode: PricingModeConsolidated, ConsolidatedValue: &cem},
	}
	v, modo := PrecoDeTabela(precos, camp, e1, &spot)
	require.Equal(t, 150.0, *v)
	require.Equal(t, PricingModePerInsertion, modo)
	v, modo = PrecoDeTabela(precos, camp, e1, &outro)
	require.Nil(t, v, "tipo sem preço na emissora")
	require.Equal(t, PricingModePerInsertion, modo)
	v, modo = PrecoDeTabela(precos, camp, e2, &spot)
	require.Nil(t, v, "consolidado não tem valor por inserção")
	require.Equal(t, PricingModeConsolidated, modo)
	v, modo = PrecoDeTabela(precos, camp, e3, &spot)
	require.Nil(t, v)
	require.Equal(t, "", modo, "emissora sem linha de preço")
	v, _ = PrecoDeTabela(precos, camp, e1, nil)
	require.Nil(t, v, "material sem tipo")
	v, modo = PrecoDeTabela(precos, uuid.New(), e1, &spot)
	require.Nil(t, v, "mesma emissora em outra campanha não herda o preço")
	require.Equal(t, "", modo)
}
