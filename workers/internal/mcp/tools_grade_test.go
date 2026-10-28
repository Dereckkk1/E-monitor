package mcp

import (
	"strings"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func TestGradeCampanha_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", e1, e2)
	mcptest.Regra(t, ctx, pool, camp, tipo, e1, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 2)
	mcptest.Regra(t, ctx, pool, camp, tipo, e2, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 1)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "out_slot", mcptest.Quando("2026-10-10", "23:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e2, "in_slot", mcptest.Quando("2026-10-11", "09:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e2, "bonus", mcptest.Quando("2026-10-11", "10:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "out_date", mcptest.Quando("2026-10-10", "09:00"))

	h := &handlers.DetectionsHandler{SummaryRepo: catalog.NewDailySummary(pool), CampaignRepo: catalog.NewCampaigns(pool)}
	var tela []catalog.DailySummaryRow
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.DailySummary, "/x?from=2026-10-10&to=2026-10-11",
		map[string]string{"campaignID": camp.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "grade_campanha",
		map[string]any{"campanha_id": camp.String(), "de": "2026-10-10", "ate": "2026-10-11"})
	celulas := itens(t, out)
	require.Len(t, celulas, len(tela))
	porChave := map[string]catalog.DailySummaryRow{}
	for _, r := range tela {
		porChave[r.StationID.String()+"|"+r.ForDate.Format("2006-01-02")] = r
	}
	var somaInSlot, somaEsperado int
	for _, c := range celulas {
		r := porChave[c["emissora"].(map[string]any)["id"].(string)+"|"+c["data"].(string)]
		require.EqualValues(t, r.Expected, c["esperado"])
		require.EqualValues(t, r.InSlot, c["in_slot"])
		require.EqualValues(t, r.Deficit, c["deficit"])
		require.EqualValues(t, r.Bonus, c["bonus"])
		require.EqualValues(t, r.OutSlot, c["out_slot"])
		require.EqualValues(t, r.OutDate, c["out_date"])
		somaInSlot += int(r.InSlot)
		somaEsperado += int(r.Expected)
	}
	tot := out["totais"].(map[string]any)
	require.EqualValues(t, somaInSlot, tot["in_slot"])
	require.EqualValues(t, somaEsperado, tot["esperado"])

	so := chamarTool(t, cfgTeste(pool), "grade_campanha",
		map[string]any{"campanha_id": camp.String(), "de": "2026-10-10", "ate": "2026-10-11", "emissora_id": e2.String()})
	require.NotEmpty(t, itens(t, so))
	for _, c := range itens(t, so) {
		require.Equal(t, e2.String(), c["emissora"].(map[string]any)["id"])
	}
	var esperadoE2, inSlotE2 float64
	for _, c := range itens(t, so) {
		esperadoE2 += c["esperado"].(float64)
		inSlotE2 += c["in_slot"].(float64)
	}
	totE2 := so["totais"].(map[string]any)
	require.EqualValues(t, esperadoE2, totE2["esperado"], "totais refletem só a emissora filtrada")
	require.EqualValues(t, inSlotE2, totE2["in_slot"])
	require.Less(t, esperadoE2, float64(somaEsperado))
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "grade_campanha",
		map[string]any{"campanha_id": camp.String(), "de": "2026-01-01", "ate": "2026-12-31"}), "[entrada_invalida]")
}

func TestPlanoDoDia_FaixasSaldoEVeiculacoes(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	outroTipo := mcptest.TipoMaterial(t, ctx, pool, "Vinheta")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	vinheta := mcptest.Material(t, ctx, pool, cli, outroTipo, "Vinheta")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-11-30", est)
	semana := mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 62, "06:00", "12:00", 2)
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 65, "08:00", "10:00", 1)  // sáb e dom
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-11-01", "2026-11-30", 127, "06:00", "22:00", 3) // novembro
	// 2026-10-27 é uma terça.
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-27", "10:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "out_slot", mcptest.Quando("2026-10-27", "23:30"))
	mcptest.Veiculacao(t, ctx, pool, camp, vinheta, est, "bonus", mcptest.Quando("2026-10-27", "11:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-28", "10:00"))

	args := map[string]any{"campanha_id": camp.String(), "emissora_id": est.String(), "tipo_id": tipo.String(), "data": "2026-10-27"}
	out := chamarTool(t, cfgTeste(pool), "plano_do_dia", args)
	require.Equal(t, "regras", out["meta_vem_de"])
	faixas := out["faixas"].([]any)
	require.Len(t, faixas, 1)
	require.Equal(t, semana.String(), faixas[0].(map[string]any)["regra_id"])
	naoValem := out["regras_que_nao_valem"].([]any)
	require.Len(t, naoValem, 2)
	motivos := []string{naoValem[0].(map[string]any)["motivo"].(string), naoValem[1].(map[string]any)["motivo"].(string)}
	require.Contains(t, motivos, "começa em 2026-11-01")

	// Saldo = a mesma linha da grade da tela.
	h := &handlers.DetectionsHandler{SummaryRepo: catalog.NewDailySummary(pool), CampaignRepo: catalog.NewCampaigns(pool)}
	var tela []catalog.DailySummaryRow
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.DailySummary, "/x?from=2026-10-27&to=2026-10-27",
		map[string]string{"campaignID": camp.String()}, admin), &tela))
	var linha catalog.DailySummaryRow
	for _, r := range tela {
		if r.TypeID == tipo && r.StationID == est {
			linha = r
		}
	}
	saldo := out["saldo"].(map[string]any)
	require.EqualValues(t, linha.Expected, saldo["esperado"])
	require.EqualValues(t, linha.InSlot, saldo["in_slot"])
	require.EqualValues(t, linha.OutSlot, saldo["out_slot"])

	// Veiculações: só do dia e só do tipo pedido, da mais cedo para a mais tarde.
	vs := out["veiculacoes"].([]any)
	require.Len(t, vs, 2)
	require.Equal(t, "in_slot", vs[0].(map[string]any)["categoria"])
	require.Equal(t, "out_slot", vs[1].(map[string]any)["categoria"])

	// Com ajuste do dia, a meta vem dele.
	mcptest.Ajuste(t, ctx, pool, camp, tipo, est, "2026-10-27", 0, "00:00", "23:59")
	comAjuste := chamarTool(t, cfgTeste(pool), "plano_do_dia", args)
	require.Equal(t, "ajuste_do_dia", comAjuste["meta_vem_de"])
	require.EqualValues(t, 0, comAjuste["ajuste_do_dia"].(map[string]any)["insercoes_esperadas"])
	require.GreaterOrEqual(t, len(comAjuste["ressalvas"].([]any)), 3)
}

func TestPlanoDoDia_CampanhaCanceladaEDiaSemLinha(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 2)
	_, err := pool.Exec(ctx, `UPDATE campaigns SET status = 'cancelada', cancelled_at = $2 WHERE id = $1`,
		camp, mcptest.Quando("2026-10-10", "12:00"))
	require.NoError(t, err)

	args := map[string]any{"campanha_id": camp.String(), "emissora_id": est.String(), "tipo_id": tipo.String(), "data": "2026-10-20"}
	out := chamarTool(t, cfgTeste(pool), "plano_do_dia", args)
	var texto []string
	for _, r := range out["ressalvas"].([]any) {
		texto = append(texto, r.(string))
	}
	juntas := strings.Join(texto, "\n")
	require.Contains(t, juntas, "cancelada", "ressalva de campanha cancelada")
	require.Contains(t, juntas, "Dia depois do cancelamento")
	require.Nil(t, out["saldo"])
	require.Contains(t, juntas, "A grade não tem linha para esta célula")

	// Antes do cancelamento a ressalva de dia posterior não vale.
	args["data"] = "2026-10-05"
	antes := chamarTool(t, cfgTeste(pool), "plano_do_dia", args)
	for _, r := range antes["ressalvas"].([]any) {
		require.NotContains(t, r.(string), "Dia depois do cancelamento")
	}
}
