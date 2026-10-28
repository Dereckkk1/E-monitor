package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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

// cenarioCampanha monta uma campanha com material, regra (seg a sex), ajuste e preço.
func cenarioCampanha(t *testing.T) (ctx context.Context, pool *pgxpool.Pool, admin, camp, est, tipo, mat uuid.UUID) {
	t.Helper()
	ctx, pool = mcptest.DBCatalogo(t)
	admin = mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est = mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	tipo = mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat = mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp = mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	mcptest.Vincular(t, ctx, pool, camp, mat, est)
	mcptest.Regra(t, ctx, pool, camp, tipo, est, "2026-10-01", "2026-10-31", 62, "06:00", "12:00", 2)
	mcptest.Ajuste(t, ctx, pool, camp, tipo, est, "2026-10-15", 0, "00:00", "23:59")
	mcptest.Preco(t, ctx, pool, camp, est, tipo, 150)
	return
}

func TestListarMateriaisCampanha_ParidadeComATela(t *testing.T) {
	_, pool, admin, camp, est, tipo, mat := cenarioCampanha(t)
	h := &handlers.CampaignMaterialsHandler{Repo: catalog.NewCampaignMaterials(pool), CampaignRepo: catalog.NewCampaigns(pool)}
	var tela []catalog.CampaignMaterial
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.ListByCampaign, "/x", map[string]string{"campaignID": camp.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_materiais_campanha", map[string]any{"campanha_id": camp.String()})
	l := itens(t, out)
	require.Len(t, l, len(tela))
	require.Equal(t, mat.String(), l[0]["material"].(map[string]any)["id"])
	require.Equal(t, "Spot A", l[0]["material"].(map[string]any)["nome"])
	require.Equal(t, tipo.String(), l[0]["tipo"].(map[string]any)["id"])
	require.Equal(t, est.String(), l[0]["emissoras"].([]any)[0].(map[string]any)["id"])
	require.EqualValues(t, 1, l[0]["emissoras_total"])
	require.Empty(t, out["ressalvas"], "nada cortado, sem ressalva")

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_materiais_campanha",
		map[string]any{"campanha_id": uuid.NewString()}), "[nao_encontrado]")
}

func TestListarRegrasDistribuicao_ParidadeComATela(t *testing.T) {
	_, pool, admin, camp, _, _, _ := cenarioCampanha(t)
	h := &handlers.DistributionRulesHandler{Repo: catalog.NewDistributionRules(pool), CampaignRepo: catalog.NewCampaigns(pool)}
	var tela []catalog.DistributionRule
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.ListByCampaign, "/x", map[string]string{"campaignID": camp.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_regras_distribuicao", map[string]any{"campanha_id": camp.String()})
	l := itens(t, out)
	require.Len(t, l, len(tela))
	require.Equal(t, tela[0].ID.String(), l[0]["id"])
	require.EqualValues(t, tela[0].PlaysPerDay, l[0]["insercoes_por_dia"])
	require.Equal(t, []any{"seg", "ter", "qua", "qui", "sex"}, l[0]["dias_da_semana"])
	require.Equal(t, map[string]any{"inicio": "06:00", "fim": "12:00"}, l[0]["faixa"])
	require.Empty(t, l[0]["materiais"], "vazio = todos os materiais do tipo")
	require.EqualValues(t, 1, l[0]["emissoras_total"])
	require.Len(t, out["ressalvas"], 1, "só a ressalva das regras")
}

func TestListarOverrides_ParidadeComATela(t *testing.T) {
	_, pool, admin, camp, _, _, _ := cenarioCampanha(t)
	h := &handlers.DistributionOverridesHandler{Repo: catalog.NewDistributionOverrides(pool), Recat: catalog.NewDistributionRules(pool)}
	var tela []catalog.DistributionOverride
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.ListByDateRange, "/x?from=2026-10-01&to=2026-10-31",
		map[string]string{"campaignID": camp.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_overrides",
		map[string]any{"campanha_id": camp.String(), "de": "2026-10-01", "ate": "2026-10-31"})
	l := itens(t, out)
	require.Len(t, l, len(tela))
	require.Equal(t, "2026-10-15", l[0]["data"])
	require.EqualValues(t, 0, l[0]["insercoes_esperadas"])
	require.NotEmpty(t, out["ressalvas"])

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_overrides",
		map[string]any{"campanha_id": camp.String(), "de": "2026-10-31", "ate": "2026-10-01"}), "[entrada_invalida]")
}

func TestListarPrecosCampanha_ParidadeComATela(t *testing.T) {
	_, pool, admin, camp, est, tipo, _ := cenarioCampanha(t)
	h := &handlers.PricingHandler{Repo: catalog.NewPricing(pool), CampaignRepo: catalog.NewCampaigns(pool)}
	var tela []catalog.StationPricing
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.ListByCampaign, "/x", map[string]string{"campaignID": camp.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_precos_campanha", map[string]any{"campanha_id": camp.String()})
	l := itens(t, out)
	require.Len(t, l, len(tela))
	require.Equal(t, est.String(), l[0]["emissora"].(map[string]any)["id"])
	require.Equal(t, "por_insercao", l[0]["modo"])
	pt := l[0]["por_tipo"].([]any)[0].(map[string]any)
	require.Equal(t, tipo.String(), pt["tipo"].(map[string]any)["id"])
	require.EqualValues(t, tela[0].PerType[0].UnitValue, pt["valor_unitario"])
}

func TestFinanceiroCampanhas_OrdemDaEntradaSemRepetir(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	a := mcptest.Campanha(t, ctx, pool, cli, "Camp A", "2026-10-01", "2026-10-31")
	b := mcptest.Campanha(t, ctx, pool, cli, "Camp B", "2026-10-01", "2026-10-31")
	fantasma := uuid.New()

	out := chamarTool(t, cfgTeste(pool), "financeiro_campanhas",
		map[string]any{"campanha_ids": []string{b.String(), a.String(), b.String(), fantasma.String()}})
	var ordem []string
	for _, l := range itens(t, out) {
		ordem = append(ordem, l["campanha"].(map[string]any)["id"].(string))
	}
	require.Equal(t, []string{b.String(), a.String()}, ordem, "ordem da entrada, sem repetir")
	require.Contains(t, out["ressalvas"], "sem campanha no E-monitor para: "+fantasma.String())
}

// Campanha grande: a paginação limita itens, não as emissoras dentro de cada
// um; sem o teto, 3 materiais × 300 emissoras estouram o limite de saída do
// cliente MCP.
func TestListas_EmissorasPorItemComTeto(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	// 300 emissoras passam do limite do guard do dbtest: o próximo teste recusaria rodar.
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `TRUNCATE stations RESTART IDENTITY CASCADE`) })
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	ests := make([]uuid.UUID, 300)
	for i := range ests {
		ests[i] = mcptest.Emissora(t, ctx, pool, fmt.Sprintf("Rádio Grande %03d", i), 0)
	}
	camp := mcptest.Campanha(t, ctx, pool, cli, "Grande", "2026-10-01", "2026-10-31", ests...)
	for i := 0; i < 3; i++ {
		mat := mcptest.Material(t, ctx, pool, cli, tipo, fmt.Sprintf("Spot %d", i))
		mcptest.Vincular(t, ctx, pool, camp, mat, ests...)
	}
	for i := 0; i < 2; i++ {
		_, err := catalog.NewDistributionRules(pool).Create(ctx, catalog.CreateDistributionRuleInput{
			CampaignID: camp, TypeID: tipo, StationIDs: ests, MaterialIDs: []uuid.UUID{},
			Name: fmt.Sprintf("Regra %d", i), StartDate: mcptest.Dia("2026-10-01"), EndDate: mcptest.Dia("2026-10-31"),
			WeekdayMask: 62, TimeStart: "06:00", TimeEnd: "12:00", PlaysPerDay: 1,
		})
		require.NoError(t, err)
	}

	cfg := cfgTeste(pool)
	for _, tool := range []string{"listar_materiais_campanha", "listar_regras_distribuicao"} {
		texto, erro := chamarNoServidor(t, novoServidor(cfg, principalTeste), tool, map[string]any{"campanha_id": camp.String()})
		require.False(t, erro, texto)
		require.Less(t, len(texto), 30_000, "%s: resposta grande demais", tool)
		var out map[string]any
		require.NoError(t, json.Unmarshal([]byte(texto), &out))
		l := itens(t, out)
		require.NotEmpty(t, l)
		for _, it := range l {
			require.Len(t, it["emissoras"], tetoEmissorasPorItem)
			require.EqualValues(t, 300, it["emissoras_total"])
			require.NotEmpty(t, it["emissoras"].([]any)[0].(map[string]any)["nome"], "nome resolvido nos refs mantidos")
		}
		require.Contains(t, fmt.Sprint(out["ressalvas"]), "cortadas em 20 por item")
	}

	// obter_campanha segue completa.
	out := chamarTool(t, cfg, "obter_campanha", map[string]any{"campanha_id": camp.String()})
	require.Len(t, out["emissoras"], 300)
}
