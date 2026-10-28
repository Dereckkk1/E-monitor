package mcp

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func TestInsightsCampanha_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 2)
	mcptest.Preco(t, ctx, pool, camp, est, tipo, 100)
	mcptest.PMMTarget(t, ctx, pool, cli, est, 250)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "09:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "out_slot", mcptest.Quando("2026-10-10", "23:30"))

	h := handlers.NewInsightsHandler(catalog.NewInsights(pool))
	var tela catalog.InsightsPayload
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get,
		"/insights?client_id="+cli.String()+"&campaigns="+camp.String()+"&from=2026-10-01&to=2026-10-31", nil, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "insights_campanha", map[string]any{
		"cliente_id": cli.String(), "campanha_ids": []string{camp.String()}, "de": "2026-10-01", "ate": "2026-10-31"})
	require.EqualValues(t, tela.KPIs.Impactos, out["impactos"])
	require.EqualValues(t, tela.KPIs.ImpactosTarget, out["impactos_target"])
	require.EqualValues(t, tela.KPIs.StationsWithTarget, out["emissoras_com_target"])
	require.EqualValues(t, tela.KPIs.VeiculacoesTotal, out["veiculacoes"])
	require.EqualValues(t, tela.KPIs.StationsCount, out["emissoras"])
	require.EqualValues(t, tela.KPIs.StationsWithPMM, out["emissoras_com_pmm"])
	inv := out["investido"].(map[string]any)
	require.EqualValues(t, tela.KPIs.Investido.Executado, inv["executado"])
	require.EqualValues(t, tela.KPIs.Investido.Contratado, inv["contratado"])
	bon := out["bonificacao"].(map[string]any)
	require.EqualValues(t, tela.KPIs.Bonificacao.Valor, bon["valor"])
	require.EqualValues(t, tela.KPIs.Bonificacao.Count, bon["insercoes"])
	require.EqualValues(t, tela.KPIs.CPM, out["cpm"])
	require.EqualValues(t, tela.KPIs.CPMTarget, out["cpm_target"])
	cat := out["por_categoria"].(map[string]any)
	require.EqualValues(t, tela.VeiculacoesBreakdown.InSlot, cat["in_slot"])
	require.EqualValues(t, tela.VeiculacoesBreakdown.ExtrasOrphan, cat["bonus"])
	require.EqualValues(t, tela.VeiculacoesBreakdown.OutSlot, cat["out_slot"])
	require.EqualValues(t, tela.VeiculacoesBreakdown.OutDate, cat["out_date"])
	gen := out["genero"].(map[string]any)
	require.EqualValues(t, tela.KPIs.Gender.M, gen["m"])
	require.EqualValues(t, tela.KPIs.Gender.F, gen["f"])
	serie := out["serie"].([]any)
	require.Len(t, serie, len(tela.Buckets))
	for i, b := range tela.Buckets {
		p := serie[i].(map[string]any)
		require.Equal(t, b.Bucket, p["periodo"])
		require.EqualValues(t, b.Programado, p["programado"])
		require.EqualValues(t, b.InSlot, p["in_slot"])
		require.EqualValues(t, b.Extras, p["bonus"])
		require.EqualValues(t, b.Deficit, p["deficit"])
	}
	require.Equal(t, map[string]any{"de": "2026-10-01", "ate": "2026-10-31", "granularidade": "dia"}, out["periodo"])
	// Conferência independente da base in_slot + bonus:
	require.EqualValues(t, 3000, out["impactos"])
	require.EqualValues(t, 750, out["impactos_target"])
	require.EqualValues(t, 200, inv["executado"], "100 × 2 in_slot: a paridade não pode ser 0 = 0")
	require.EqualValues(t, 100, bon["valor"])
	require.Equal(t, "https://portal.teste/insights?campaigns="+camp.String()+"&client_id="+cli.String()+"&from=2026-10-01&to=2026-10-31",
		out["links"].(map[string]any)["tela"])
	require.NotEmpty(t, out["ressalvas"])
}

// Sem PMM no target e sem veiculação: os "não se aplica" saem null, nunca 0.
func TestInsightsCampanha_NulosNaoViramZero(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	out := chamarTool(t, cfgTeste(pool), "insights_campanha", map[string]any{
		"cliente_id": cli.String(), "campanha_ids": []string{camp.String()}, "de": "2026-10-01", "ate": "2026-10-31"})
	require.Nil(t, out["impactos_target"])
	require.Nil(t, out["cpm"])
	require.Nil(t, out["cpm_target"])
	require.Nil(t, out["participacao_material"])
	require.NotNil(t, out["serie"], "serie sai [] e não null")
}

func TestInsightsCampanha_Recusas(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	a := mcptest.Cliente(t, ctx, pool, "A")
	b := mcptest.Cliente(t, ctx, pool, "B")
	campB := mcptest.Campanha(t, ctx, pool, b, "De B", "2026-10-01", "2026-10-31")
	cfg := cfgTeste(pool)
	require.Contains(t, chamarToolErro(t, cfg, "insights_campanha", map[string]any{"cliente_id": a.String(), "campanha_ids": []string{}}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "insights_campanha", map[string]any{
		"cliente_id": a.String(), "campanha_ids": []string{campB.String()}, "de": "2026-10-01", "ate": "2026-10-31"}), "não são do cliente")
	require.Contains(t, chamarToolErro(t, cfg, "insights_campanha", map[string]any{
		"cliente_id": b.String(), "campanha_ids": []string{campB.String()}, "de": "2026-10-31", "ate": "2026-10-01"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "insights_campanha", map[string]any{
		"cliente_id": b.String(), "campanha_ids": []string{campB.String()}, "de": "01/10/2026"}), "[entrada_invalida]")
}
