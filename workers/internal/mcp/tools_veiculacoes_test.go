package mcp

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/catalog"
	"radiocheck/internal/mcp/mcptest"
)

func TestListarVeiculacoes_ParidadeEFiltros(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", e1, e2)
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e1, "orphan", mcptest.Quando("2026-10-10", "09:00"))
	mcptest.Veiculacao(t, ctx, pool, camp, mat, e2, "out_slot", mcptest.Quando("2026-10-11", "23:00"))
	ignorada := mcptest.Veiculacao(t, ctx, pool, camp, mat, e2, "in_slot", mcptest.Quando("2026-10-12", "10:00"))
	_, err := pool.Exec(ctx, `UPDATE detections SET ignored_at = now() WHERE id = $1`, ignorada)
	require.NoError(t, err)

	h := &handlers.DetectionsHandler{Repo: catalog.NewDetections(pool)}
	var tela catalog.ListPagedResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/detections?page=1&page_size=25&campaigns="+camp.String(), nil, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_veiculacoes", map[string]any{"campanha_ids": []string{camp.String()}})
	var idsTela []string
	for _, d := range tela.Data {
		idsTela = append(idsTela, d.ID.String())
	}
	require.Equal(t, idsTela, ids(t, itens(t, out), "id"))
	require.EqualValues(t, tela.Total, pag(t, out)["total"])
	require.EqualValues(t, 3, pag(t, out)["total"], "a ignorada não conta")

	bonus := chamarTool(t, cfgTeste(pool), "listar_veiculacoes",
		map[string]any{"campanha_ids": []string{camp.String()}, "categorias": []string{"bonus"}})
	require.EqualValues(t, 1, pag(t, bonus)["total"], "bonus inclui o nome antigo orphan")
	require.Equal(t, "bonus", itens(t, bonus)[0]["categoria"])

	doE2 := chamarTool(t, cfgTeste(pool), "listar_veiculacoes",
		map[string]any{"campanha_ids": []string{camp.String()}, "emissora_id": e2.String()})
	require.EqualValues(t, 1, pag(t, doE2)["total"])

	periodo := chamarTool(t, cfgTeste(pool), "listar_veiculacoes", map[string]any{"de": "2026-10-11", "ate": "2026-10-11"})
	require.EqualValues(t, 1, pag(t, periodo)["total"], "o dia é o de America/Sao_Paulo")

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_veiculacoes", map[string]any{}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_veiculacoes",
		map[string]any{"campanha_ids": []string{camp.String()}, "categorias": []string{"orphan"}}), "[entrada_invalida]")
}

func TestObterVeiculacao_AtribuicoesEAprovacao(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	a := mcptest.Campanha(t, ctx, pool, cli, "Campanha A", "2026-10-01", "2026-10-31", est)
	b := mcptest.Campanha(t, ctx, pool, cli, "Campanha B", "2026-10-01", "2026-10-31", est)
	quando := mcptest.Quando("2026-10-10", "08:00")
	det := mcptest.Veiculacao(t, ctx, pool, a, mat, est, "in_slot", quando)
	mcptest.Projecao(t, ctx, pool, det, quando, b, mat, "bonus")

	h := &handlers.DetectionsHandler{Repo: catalog.NewDetections(pool)}
	var tela catalog.Detection
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/x", map[string]string{"id": det.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "obter_veiculacao", map[string]any{"veiculacao_id": det.String()})
	require.Equal(t, tela.Category, out["categoria"])
	require.Equal(t, tela.CampaignID.String(), out["campanha"].(map[string]any)["id"])
	require.EqualValues(t, tela.Confidence, out["confianca"])
	require.Len(t, out["atribuicoes"].([]any), 2)
	require.Equal(t, true, out["aprovada"])
	require.Equal(t, "https://portal.teste/detections/"+det.String(), out["links"].(map[string]any)["ouvir"])

	_, err := pool.Exec(ctx, `UPDATE detections SET ignored_at = now() WHERE id = $1`, det)
	require.NoError(t, err)
	ign := chamarTool(t, cfgTeste(pool), "obter_veiculacao", map[string]any{"veiculacao_id": det.String()})
	require.Equal(t, false, ign["aprovada"])
	require.NotEmpty(t, ign["ressalvas"])

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_veiculacao",
		map[string]any{"veiculacao_id": uuid.NewString()}), "[nao_encontrado]")
}

func TestVeiculacoesPorMaterial_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	m1 := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	m2 := mcptest.Material(t, ctx, pool, cli, tipo, "Spot B")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	for _, h := range []string{"08:00", "09:00", "10:00"} {
		mcptest.Veiculacao(t, ctx, pool, camp, m1, est, "in_slot", mcptest.Quando("2026-10-10", h))
	}
	mcptest.Veiculacao(t, ctx, pool, camp, m2, est, "in_slot", mcptest.Quando("2026-10-10", "11:00"))

	h := &handlers.DetectionsHandler{Repo: catalog.NewDetections(pool)}
	var tela catalog.MaterialAggregateResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.AggregateByMaterial, "/x?campaigns="+camp.String(), nil, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "veiculacoes_por_material", map[string]any{"campanha_ids": []string{camp.String()}})
	require.EqualValues(t, tela.TotalDetections, out["total_veiculacoes"])
	require.EqualValues(t, tela.DistinctMaterials, out["materiais_distintos"])
	l := itens(t, out)
	require.Equal(t, tela.Data[0].MaterialID.String(), l[0]["material"].(map[string]any)["id"])
	require.EqualValues(t, tela.Data[0].Count, l[0]["veiculacoes"])
}

func TestListarVeiculacoes_PeriodoValidado(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 0)
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-01-01", "2026-12-31", est)
	cfg := cfgTeste(pool)

	// de > ate
	require.Contains(t, chamarToolErro(t, cfg, "listar_veiculacoes", map[string]any{"de": "2026-10-12", "ate": "2026-10-11"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "listar_veiculacoes",
		map[string]any{"campanha_ids": []string{camp.String()}, "de": "2026-10-12", "ate": "2026-10-11"}), "[entrada_invalida]")
	// sem campanha_ids o período é limitado a 93 dias; com campanha_ids não
	require.Contains(t, chamarToolErro(t, cfg, "listar_veiculacoes", map[string]any{"de": "2026-01-01", "ate": "2026-12-31"}), "[entrada_invalida]")
	chamarTool(t, cfg, "listar_veiculacoes", map[string]any{"de": "2026-07-01", "ate": "2026-10-01"})
	chamarTool(t, cfg, "listar_veiculacoes", map[string]any{"campanha_ids": []string{camp.String()}, "de": "2026-01-01", "ate": "2026-12-31"})
}

func TestObterVeiculacao_ManualCanonicaSemProjecaoEDesatualizada(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	autor := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mat := mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Campanha Canônica", "2026-10-01", "2026-10-31", est)
	det := mcptest.Veiculacao(t, ctx, pool, camp, mat, est, "in_slot", mcptest.Quando("2026-10-10", "08:00"))
	_, err := pool.Exec(ctx, `UPDATE detections SET manual_at = now(), manual_by = $2, manual_note = 'lançada à mão' WHERE id = $1`, det, autor)
	require.NoError(t, err)

	out := chamarTool(t, cfgTeste(pool), "obter_veiculacao", map[string]any{"veiculacao_id": det.String()})
	por := out["manual"].(map[string]any)["por"].(map[string]any)
	require.Equal(t, autor.String(), por["id"])
	require.Equal(t, "Pessoa de Teste", por["nome"])
	require.Empty(t, out["ressalvas"])

	// sem projeção da campanha canônica: o nome vem da campanha e a ressalva avisa
	_, err = pool.Exec(ctx, `DELETE FROM detection_campaigns WHERE detection_id = $1`, det)
	require.NoError(t, err)
	out = chamarTool(t, cfgTeste(pool), "obter_veiculacao", map[string]any{"veiculacao_id": det.String()})
	require.Equal(t, "Campanha Canônica", out["campanha"].(map[string]any)["nome"])
	require.Contains(t, out["ressalvas"].([]any)[0], "não tem projeção em detection_campaigns")

	// fora do conjunto aprovado: a categoria pode estar desatualizada
	_, err = pool.Exec(ctx, `UPDATE detections SET retracted_at = now() WHERE id = $1`, det)
	require.NoError(t, err)
	out = chamarTool(t, cfgTeste(pool), "obter_veiculacao", map[string]any{"veiculacao_id": det.String()})
	require.Contains(t, fmt.Sprint(out["ressalvas"]), "desatualizada")
}

func TestVeiculacoesPorMaterial_OrdemEstavel(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 1000)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	for i, titulo := range []string{"A", "B", "C", "D"} {
		m := mcptest.Material(t, ctx, pool, cli, tipo, titulo)
		for j := 0; j <= i%2; j++ { // contagens 1,2,1,2: empates em ambos
			mcptest.Veiculacao(t, ctx, pool, camp, m, est, "in_slot", mcptest.Quando("2026-10-10", fmt.Sprintf("0%d:00", i+1+j*4)))
		}
	}
	out := chamarTool(t, cfgTeste(pool), "veiculacoes_por_material", map[string]any{"campanha_ids": []string{camp.String()}})
	l := itens(t, out)
	require.Len(t, l, 4)
	for i := 1; i < len(l); i++ {
		a, b := l[i-1], l[i]
		ca, cb := a["veiculacoes"].(float64), b["veiculacoes"].(float64)
		require.GreaterOrEqual(t, ca, cb)
		if ca == cb {
			require.Less(t, a["material"].(map[string]any)["id"], b["material"].(map[string]any)["id"], "empate desempata por id")
		}
	}
}
