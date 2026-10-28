package mcp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// insightsDaTela chama o GET /insights como o admin chamaria.
func insightsDaTela(t *testing.T, h *handlers.InsightsHandler, admin, cli uuid.UUID, query string) catalog.InsightsPayload {
	t.Helper()
	var tela catalog.InsightsPayload
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/insights?client_id="+cli.String()+"&"+query, nil, admin), &tela))
	return tela
}

// conferirInsights compara campo a campo o resultado da tool com o payload da tela.
func conferirInsights(t *testing.T, tela catalog.InsightsPayload, out map[string]any) {
	t.Helper()
	require.EqualValues(t, tela.KPIs.Impactos, out["impactos"])
	require.EqualValues(t, tela.KPIs.StationsWithTarget, out["emissoras_com_target"])
	require.EqualValues(t, tela.KPIs.VeiculacoesTotal, out["veiculacoes"])
	require.EqualValues(t, tela.KPIs.StationsCount, out["emissoras"])
	require.EqualValues(t, tela.KPIs.StationsWithPMM, out["emissoras_com_pmm"])
	require.EqualValues(t, tela.KPIs.Investido.Executado, out["investido"], "o card Investido mostra o executado")
	require.Equal(t, tela.Consolidated, out["consolidado"])
	if tela.KPIs.Impactos > 0 {
		require.EqualValues(t, tela.KPIs.CPM, out["cpm"])
	}
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
		require.EqualValues(t, b.OutSlot, p["out_slot"])
		require.EqualValues(t, b.OutDate, p["out_date"])
		require.EqualValues(t, b.Extras, p["bonus"])
		require.EqualValues(t, b.Deficit, p["deficit"])
	}
}

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
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "out_slot", mcptest.Quando("2026-10-11", "23:30"))

	h := handlers.NewInsightsHandler(catalog.NewInsights(pool))
	tela := insightsDaTela(t, h, admin, cli, "campaigns="+camp.String()+"&from=2026-10-01&to=2026-10-31")

	out := chamarTool(t, cfgTeste(pool), "insights_campanha", map[string]any{
		"cliente_id": cli.String(), "campanha_ids": []string{camp.String()}, "de": "2026-10-01", "ate": "2026-10-31"})
	conferirInsights(t, tela, out)
	require.EqualValues(t, tela.KPIs.ImpactosTarget, out["impactos_target"])
	require.EqualValues(t, tela.KPIs.CPMTarget, out["cpm_target"])
	bon := out["bonificacao"].(map[string]any)
	require.EqualValues(t, tela.KPIs.Bonificacao.Valor, bon["valor"])
	require.EqualValues(t, tela.KPIs.Bonificacao.Count, bon["insercoes"])
	require.Equal(t, map[string]any{"de": "2026-10-01", "ate": "2026-10-31", "granularidade": "dia"}, out["periodo"])
	// Conferência independente da base in_slot + bonus:
	require.EqualValues(t, 3000, out["impactos"])
	require.EqualValues(t, 750, out["impactos_target"])
	require.EqualValues(t, 200, out["investido"], "100 × 2 in_slot: a paridade não pode ser 0 = 0")
	require.EqualValues(t, 100, bon["valor"])
	_, temContratado := out["investido"].(map[string]any)
	require.False(t, temContratado, "contratado não está em card nenhum: só o executado sai")
	require.Equal(t, "https://portal.teste/insights?campaigns="+camp.String()+"&client_id="+cli.String()+"&from=2026-10-01&to=2026-10-31",
		out["links"].(map[string]any)["tela"])
	require.NotEmpty(t, out["ressalvas"])
	require.NotContains(t, out["ressalvas"], ressalvaConsolidado)
}

// Preço consolidado (modo fornecedor): o investido é o acumulado mensal, a
// bonificação segue a regra de exibição da tela (showBonificacaoDe).
func TestInsightsCampanha_ConsolidadoParidadeEBonificacao(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	cons := mcptest.Emissora(t, ctx, pool, "Rádio Consolidada", 1000)
	porIns := mcptest.Emissora(t, ctx, pool, "Rádio Por Inserção", 500)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", cons, porIns)
	mcptest.Regra(t, ctx, pool, camp, tipo, cons, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 1)
	mcptest.Regra(t, ctx, pool, camp, tipo, porIns, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 1)
	mcptest.PrecoConsolidado(t, ctx, pool, camp, cons, 3000)
	mcptest.Preco(t, ctx, pool, camp, porIns, tipo, 100)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, cons, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, cons, "bonus", mcptest.Quando("2026-10-10", "09:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, porIns, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, porIns, "bonus", mcptest.Quando("2026-10-10", "09:00"))
	h := handlers.NewInsightsHandler(catalog.NewInsights(pool))
	cfg := cfgTeste(pool)

	// Mista (as duas emissoras): bonificação = só a parcela por inserção.
	tela := insightsDaTela(t, h, admin, cli, "campaigns="+camp.String()+"&from=2026-10-01&to=2026-10-31")
	require.True(t, tela.Consolidated)
	out := chamarTool(t, cfg, "insights_campanha", map[string]any{
		"cliente_id": cli.String(), "campanha_ids": []string{camp.String()}, "de": "2026-10-01", "ate": "2026-10-31"})
	conferirInsights(t, tela, out)
	require.NotZero(t, tela.KPIs.Investido.Executado)
	bon := out["bonificacao"].(map[string]any)
	require.EqualValues(t, tela.KPIs.Bonificacao.Valor, bon["valor"])
	require.EqualValues(t, 100, bon["valor"], "só o bônus da por inserção tem preço")
	require.EqualValues(t, 2, out["por_categoria"].(map[string]any)["bonus"], "o bônus da consolidada segue contado")
	require.Contains(t, out["ressalvas"], ressalvaConsolidado)
	require.Contains(t, out["ressalvas"], ressalvaBonificacaoMista)

	// Só a consolidada: a tela esconde o card (o zero ali é "sem preço").
	tela = insightsDaTela(t, h, admin, cli, "campaigns="+camp.String()+"&from=2026-10-01&to=2026-10-31&stations="+cons.String())
	require.True(t, tela.Consolidated)
	require.Zero(t, tela.KPIs.Bonificacao.Valor)
	out = chamarTool(t, cfg, "insights_campanha", map[string]any{"cliente_id": cli.String(), "campanha_ids": []string{camp.String()},
		"de": "2026-10-01", "ate": "2026-10-31", "emissora_ids": []string{cons.String()}})
	conferirInsights(t, tela, out)
	b, existe := out["bonificacao"]
	require.True(t, existe)
	require.Nil(t, b)
	require.EqualValues(t, 1, out["por_categoria"].(map[string]any)["bonus"])
	require.Contains(t, out["ressalvas"], ressalvaBonificacaoSemPreco)
	require.NotContains(t, out["ressalvas"], ressalvaBonificacaoMista)
}

// O "hoje" do acúmulo mensal do consolidado vem do relógio injetável (L.Agora).
func TestInsightsCampanha_HojeDoRelogioInjetavel(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	cons := mcptest.Emissora(t, ctx, pool, "Rádio Consolidada", 1000)
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-12-31", cons)
	mcptest.PrecoConsolidado(t, ctx, pool, camp, cons, 1000)
	cfg := cfgTeste(pool)
	args := map[string]any{"cliente_id": cli.String(), "campanha_ids": []string{camp.String()}, "de": "2026-10-01", "ate": "2026-12-31"}

	cfg.Leitura.Agora = func() time.Time { return time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) }
	require.EqualValues(t, 1000, chamarTool(t, cfg, "insights_campanha", args)["investido"], "1 mês iniciado")
	cfg.Leitura.Agora = func() time.Time { return time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC) }
	require.EqualValues(t, 3000, chamarTool(t, cfg, "insights_campanha", args)["investido"], "3 meses iniciados")
}

// Filtro de material: rateio dos valores em R$, marcado.
func TestInsightsCampanha_RateioPorMaterial(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	a := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	b := mcptest.Material(t, ctx, pool, cli, tipo, "Spot B")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 4)
	mcptest.Preco(t, ctx, pool, camp, est, tipo, 100)
	mcptest.Veiculacao(t, ctx, pool, camp, a, est, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, b, est, "in_slot", mcptest.Quando("2026-10-10", "09:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, b, est, "in_slot", mcptest.Quando("2026-10-10", "10:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, b, est, "in_slot", mcptest.Quando("2026-10-10", "11:00"))

	h := handlers.NewInsightsHandler(catalog.NewInsights(pool))
	tela := insightsDaTela(t, h, admin, cli, "campaigns="+camp.String()+"&from=2026-10-01&to=2026-10-31&materials="+a.String())
	require.True(t, tela.MaterialProrated)
	out := chamarTool(t, cfgTeste(pool), "insights_campanha", map[string]any{"cliente_id": cli.String(), "campanha_ids": []string{camp.String()},
		"de": "2026-10-01", "ate": "2026-10-31", "material_ids": []string{a.String()}})
	conferirInsights(t, tela, out)
	require.Equal(t, true, out["rateado_por_material"])
	require.EqualValues(t, tela.MaterialShare, out["participacao_material"])
	require.EqualValues(t, 0.25, out["participacao_material"], "1 de 4 veiculações")
	require.EqualValues(t, 1000, out["impactos"], "impactos do material são exatos")
	require.Contains(t, out["ressalvas"], ressalvaRateio)
	require.Contains(t, out["links"].(map[string]any)["tela"], "materials="+a.String())
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
	require.NotNil(t, out["bonificacao"], "sem consolidada o card aparece, mesmo zerado")
	require.NotNil(t, out["serie"], "serie sai [] e não null")
}

func TestInsightsCampanha_CanceladaComoHistorico(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31")
	mcptest.EstadoCampanha(t, ctx, pool, camp, "cancelada")
	out := chamarTool(t, cfgTeste(pool), "insights_campanha", map[string]any{
		"cliente_id": cli.String(), "campanha_ids": []string{camp.String()}, "de": "2026-10-01", "ate": "2026-10-31"})
	require.Contains(t, out["ressalvas"], ressalvaCanceladaRelatorio)
	require.NotContains(t, out["ressalvas"], ressalvaCancelada)
}

func TestInsightsCampanha_Recusas(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	a := mcptest.Cliente(t, ctx, pool, "A")
	b := mcptest.Cliente(t, ctx, pool, "B")
	campB := mcptest.Campanha(t, ctx, pool, b, "De B", "2026-10-01", "2026-10-31")
	cfg := cfgTeste(pool)
	cfg.Leitura.Agora = func() time.Time { return time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) }
	require.Contains(t, chamarToolErro(t, cfg, "insights_campanha", map[string]any{"cliente_id": a.String(), "campanha_ids": []string{}}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "insights_campanha", map[string]any{
		"cliente_id": a.String(), "campanha_ids": []string{campB.String()}, "de": "2026-10-01", "ate": "2026-10-31"}), "não são do cliente")
	require.Contains(t, chamarToolErro(t, cfg, "insights_campanha", map[string]any{
		"cliente_id": b.String(), "campanha_ids": []string{campB.String()}, "de": "2026-10-31", "ate": "2026-10-01"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "insights_campanha", map[string]any{
		"cliente_id": b.String(), "campanha_ids": []string{campB.String()}, "de": "01/10/2026"}), "[entrada_invalida]")
	// Um lado só, com o padrão do outro invertendo a janela: a mensagem diz o padrão.
	msg := chamarToolErro(t, cfg, "insights_campanha", map[string]any{
		"cliente_id": b.String(), "campanha_ids": []string{campB.String()}, "de": "2026-11-05"})
	require.Contains(t, msg, "o padrão dele é 2026-10-31")
	// Acima do teto: diz qual é o teto, não "inválido".
	muitas := make([]string, 51)
	for i := range muitas {
		muitas[i] = uuid.NewString()
	}
	msg = chamarToolErro(t, cfg, "insights_campanha", map[string]any{"cliente_id": b.String(), "campanha_ids": muitas})
	require.Contains(t, msg, "no máximo 50")
	require.NotContains(t, msg, "inválido")
}

// Janela maior que a vigência: a tocada fora da vigência (out_date) aparece na
// série e no breakdown, e a série bate com a tela também nessa categoria.
func TestInsightsCampanha_SerieComOutDate(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-15", est)
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-15", 127, "06:00", "22:00", 1)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "out_date", mcptest.Quando("2026-10-20", "08:00"))

	h := handlers.NewInsightsHandler(catalog.NewInsights(pool))
	tela := insightsDaTela(t, h, admin, cli, "campaigns="+camp.String()+"&from=2026-10-01&to=2026-10-31")
	out := chamarTool(t, cfgTeste(pool), "insights_campanha", map[string]any{
		"cliente_id": cli.String(), "campanha_ids": []string{camp.String()}, "de": "2026-10-01", "ate": "2026-10-31"})
	conferirInsights(t, tela, out)
	require.EqualValues(t, 1, out["por_categoria"].(map[string]any)["out_date"])
	comOutDate := 0
	for _, p := range out["serie"].([]any) {
		if p.(map[string]any)["out_date"].(float64) > 0 {
			comOutDate++
		}
	}
	require.Equal(t, 1, comOutDate, "a série tem o ponto do dia 20 com out_date")
	require.EqualValues(t, 1000, out["impactos"], "out_date não é impacto")
}
