package mcp

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func TestListarCampanhas_CanceladaForaPorPadraoEParidade(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente A")
	outro := mcptest.Cliente(t, ctx, pool, "Cliente B")
	ativa := mcptest.Campanha(t, ctx, pool, cli, "Ativa", "2026-10-01", "2026-10-31")
	mcptest.EstadoCampanha(t, ctx, pool, ativa, "ativa")
	prog := mcptest.Campanha(t, ctx, pool, cli, "Programada", "2026-11-01", "2026-11-30")
	mcptest.EstadoCampanha(t, ctx, pool, prog, "programada")
	canc := mcptest.Campanha(t, ctx, pool, cli, "Cancelada", "2026-09-01", "2026-09-30")
	mcptest.EstadoCampanha(t, ctx, pool, canc, "cancelada")
	conc := mcptest.Campanha(t, ctx, pool, outro, "Concluída", "2026-08-01", "2026-08-31")
	mcptest.EstadoCampanha(t, ctx, pool, conc, "concluida")

	h := &handlers.CampaignsHandler{Repo: catalog.NewCampaigns(pool)}
	var tela struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/campaigns?status=programada,ativa,concluida", nil, admin), &tela))
	var idsTela []string
	for _, c := range tela.Data {
		idsTela = append(idsTela, c.ID)
	}
	out := chamarTool(t, cfgTeste(pool), "listar_campanhas", map[string]any{})
	require.Equal(t, idsTela, ids(t, itens(t, out), "id"), "mesmo conjunto e mesma ordem da tela, sem a cancelada")
	require.Nil(t, out["ressalvas"])

	so := chamarTool(t, cfgTeste(pool), "listar_campanhas", map[string]any{"estados": []string{"cancelada"}})
	require.Equal(t, []string{canc.String()}, ids(t, itens(t, so), "id"))
	require.NotNil(t, itens(t, so)[0]["cancelada_em"])
	require.NotEmpty(t, so["ressalvas"])

	doCliente := chamarTool(t, cfgTeste(pool), "listar_campanhas", map[string]any{"cliente_id": outro.String()})
	require.Equal(t, []string{conc.String()}, ids(t, itens(t, doCliente), "id"))
	require.Equal(t, "Cliente B", itens(t, doCliente)[0]["cliente"].(map[string]any)["nome"])

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_campanhas", map[string]any{"competencia": "10/2026"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_campanhas", map[string]any{"estados": []string{"rodando"}}), "[entrada_invalida]")
}

func TestObterCampanha_PorIdEPorCodigo(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 0)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Com código", "2026-10-01", "2026-10-31", e1)
	mcptest.Vincular(t, ctx, pool, camp, mat, e1)
	_, err := pool.Exec(ctx, `UPDATE campaigns SET hub_code = 'EH-2346AB' WHERE id = $1`, camp)
	require.NoError(t, err)

	out := chamarTool(t, cfgTeste(pool), "obter_campanha", map[string]any{"campanha_id": camp.String()})
	c := out["campanha"].(map[string]any)
	require.Equal(t, "Com código", c["nome"])
	require.EqualValues(t, 1, c["materiais"])
	require.Equal(t, "Rádio Um", out["emissoras"].([]any)[0].(map[string]any)["nome"])
	require.Equal(t, "https://portal.teste/campaigns/"+camp.String()+"/edit", out["links"].(map[string]any)["tela"])

	porCodigo := chamarTool(t, cfgTeste(pool), "obter_campanha", map[string]any{"codigo": "eh-2346ab"})
	require.Equal(t, camp.String(), porCodigo["campanha"].(map[string]any)["id"], "o código é normalizado como o hub faz")

	outra := mcptest.Campanha(t, ctx, pool, cli, "Mesmo código", "2026-11-01", "2026-11-30")
	_, err = pool.Exec(ctx, `UPDATE campaigns SET hub_code = 'EH-2346AB' WHERE id = $1`, outra)
	require.NoError(t, err)
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_campanha", map[string]any{"codigo": "EH-2346AB"}), "[ambiguo]")

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_campanha", map[string]any{}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_campanha",
		map[string]any{"campanha_id": camp.String(), "codigo": "EH-2346AB"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_campanha", map[string]any{"codigo": "XYZ"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_campanha",
		map[string]any{"campanha_id": uuid.NewString()}), "[nao_encontrado]")
}

func TestFinanceiroCampanhas_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Paga", "2026-10-01", "2026-10-31", est)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 127, "06:00", "22:00", 2)
	mcptest.Preco(t, ctx, pool, camp, est, tipo, 100)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "09:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "bonus", mcptest.Quando("2026-10-10", "10:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "out_slot", mcptest.Quando("2026-10-10", "23:30"))

	h := &handlers.CampaignsHandler{Repo: catalog.NewCampaigns(pool)}
	var tela []catalog.CampaignFinancials
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Financials, "/campaigns/financials?ids="+camp.String(), nil, admin), &tela))
	require.Len(t, tela, 1)

	out := chamarTool(t, cfgTeste(pool), "financeiro_campanhas", map[string]any{"campanha_ids": []string{camp.String()}})
	l := itens(t, out)[0]
	require.EqualValues(t, tela[0].TotalInvested, l["investido"])
	require.EqualValues(t, tela[0].TotalBonusValue, l["bonificacao"])
	require.EqualValues(t, tela[0].TotalInsertions, l["insercoes"])
	require.EqualValues(t, tela[0].TotalAudience, l["impactos"])
	// Conferência independente da base in_slot + bonus (out_slot não vale nada):
	require.EqualValues(t, 3, l["insercoes"])
	require.EqualValues(t, 200, l["investido"])
	require.EqualValues(t, 100, l["bonificacao"])
	require.EqualValues(t, 3000, l["impactos"])
	require.EqualValues(t, 100, l["cpm"], "(200 + 100) / 3000 × 1000")
	require.NotEmpty(t, out["ressalvas"])

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "financeiro_campanhas", map[string]any{"campanha_ids": []string{}}), "[entrada_invalida]")
}
