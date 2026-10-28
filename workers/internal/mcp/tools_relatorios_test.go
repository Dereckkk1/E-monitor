package mcp

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

// periodoRFC: o recorte de [de 00:00, ate 23:59:59.999] em SP, como a tela manda.
func periodoRFC(de, ate string) string {
	ini, _ := limitesDoDiaSP(mcptest.Dia(de))
	_, fim := limitesDoDiaSP(mcptest.Dia(ate))
	return "from=" + url.QueryEscape(ini.Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(fim.Format(time.RFC3339Nano))
}

func TestRelatorioDataHora_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	semPreco := mcptest.Emissora(t, ctx, pool, "Rádio Sem Preço", 0)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est, semPreco)
	mcptest.Preco(t, ctx, pool, camp, est, tipo, 150)
	mcptest.PMMTarget(t, ctx, pool, cli, est, 250)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "out_slot", mcptest.Quando("2026-10-11", "23:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, semPreco, "bonus", mcptest.Quando("2026-10-12", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-11-01", "08:00")) // fora do período

	h := &handlers.DetectionsHandler{Repo: catalog.NewDetections(pool)}
	var tela catalog.ListPagedResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/detections?page=1&page_size=100&sort=detected_at_asc&campaigns="+camp.String()+
		"&"+periodoRFC("2026-10-01", "2026-10-31"), nil, admin), &tela))
	require.Equal(t, 3, tela.Total)

	out := chamarTool(t, cfgTeste(pool), "relatorio_data_hora", map[string]any{
		"cliente_id": cli.String(), "campanha_ids": []string{camp.String()}, "de": "2026-10-01", "ate": "2026-10-31", "mais_antigas_primeiro": true})
	require.EqualValues(t, tela.Total, pag(t, out)["total"])
	xs := itens(t, out)
	require.Len(t, xs, len(tela.Data))
	for i, d := range tela.Data {
		require.Equal(t, d.ID.String(), xs[i]["id"])
		require.Equal(t, d.Category, xs[i]["categoria"])
	}
	require.EqualValues(t, *tela.Data[0].StationPMM, xs[0]["pmm"])
	require.EqualValues(t, *tela.Data[0].StationPMMTarget, xs[0]["pmm_target"])
	require.EqualValues(t, 150, xs[0]["valor_unitario_tabela"])
	require.Equal(t, "por_insercao", xs[0]["modo_preco"])
	require.EqualValues(t, 150, xs[1]["valor_unitario_tabela"], "a tela mostra o valor de tabela em qualquer categoria")
	require.Nil(t, xs[2]["valor_unitario_tabela"], "emissora sem linha de preço")
	require.Nil(t, xs[2]["modo_preco"], "sem preço = null (o 'none' da tela)")
	require.Nil(t, xs[2]["pmm_target"])
	tela0 := out["links"].(map[string]any)["tela"].(string)
	require.Contains(t, tela0, "/reports/airtime?")
	require.Contains(t, tela0, "competence=2026-10")
	require.Contains(t, tela0, "from=2026-10-01")
	require.Contains(t, tela0, "to=2026-10-31")
	require.Contains(t, tela0, "client_id="+cli.String())
}

func TestRelatorioDataHora_UmClienteEPeriodoCompleto(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	a := mcptest.Cliente(t, ctx, pool, "A")
	b := mcptest.Cliente(t, ctx, pool, "B")
	campA := mcptest.Campanha(t, ctx, pool, a, "Camp A", "2026-10-01", "2026-10-31")
	campB := mcptest.Campanha(t, ctx, pool, b, "Camp B", "2026-10-01", "2026-10-31")
	cfg := cfgTeste(pool)
	require.Contains(t, chamarToolErro(t, cfg, "relatorio_data_hora", map[string]any{
		"cliente_id": a.String(), "campanha_ids": []string{campA.String(), campB.String()}, "de": "2026-10-01", "ate": "2026-10-31"}), "Camp B")
	// de e ate são obrigatórios no esquema (o SDK recusa antes da tool) e, vazios, na tool.
	require.Contains(t, chamarToolErro(t, cfg, "relatorio_data_hora", map[string]any{
		"cliente_id": a.String(), "campanha_ids": []string{campA.String()}, "de": "2026-10-01"}), "ate")
	require.Contains(t, chamarToolErro(t, cfg, "relatorio_data_hora", map[string]any{
		"cliente_id": a.String(), "campanha_ids": []string{campA.String()}, "de": "2026-10-01", "ate": ""}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "relatorio_data_hora", map[string]any{
		"cliente_id": a.String(), "campanha_ids": []string{campA.String()}, "de": "2026-10-31", "ate": "2026-10-01"}), "[entrada_invalida]")
}
