package postsale

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// O payload é lido por um frontend que não tem acesso ao struct, e por
// pós-vendas JÁ ENVIADOS cujo JSON está congelado no banco. Este teste trava
// os nomes de campo: renomear um deles quebra a página de todo mundo que já
// recebeu o link.
func TestPayload_ContratoJSON(t *testing.T) {
	p := Payload{
		Version:     PayloadVersion,
		Client:      ClientBrief{Name: "Engie"},
		Title:       "Pós-venda · Engie",
		PeriodLabel: "Junho e Julho de 2026",
		Campaigns: []CampaignBlock{{
			Name:        "249 ENGIE",
			Status:      "concluida",
			PeriodLabel: "01/06/2026 a 31/07/2026 · 61 dias",
			KPIs: BlockKPIs{
				ValorEntregue: 2712.50,
				Impactos:      115532,
				CPM:           23.48,
			},
			ConformingCount: 23,
		}},
	}
	b, err := json.Marshal(p)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, float64(1), got["version"])
	require.Contains(t, got, "client")
	require.Contains(t, got, "campaigns")
	require.Contains(t, got, "footer")

	camp := got["campaigns"].([]any)[0].(map[string]any)
	require.Equal(t, "01/06/2026 a 31/07/2026 · 61 dias", camp["period_label"])
	require.Equal(t, float64(23), camp["conforming_count"])
	require.Equal(t, "concluida", camp["status"])

	kpis := camp["kpis"].(map[string]any)
	require.Equal(t, 2712.50, kpis["valor_entregue"])
	require.Equal(t, float64(115532), kpis["impactos"])
	require.Equal(t, 23.48, kpis["cpm"])
}

// checking_rows nunca pode serializar como null: o frontend faz .filter() em
// cima e um null viraria TypeError na página do cliente.
func TestPayload_CheckingRowsNuncaNull(t *testing.T) {
	b, err := json.Marshal(CampaignBlock{Name: "X"})
	require.NoError(t, err)
	require.Contains(t, string(b), `"checking_rows":[]`)
}
