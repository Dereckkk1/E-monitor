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

func TestBuscarEmissoras_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	for _, n := range []string{"Rádio Alfa", "Rádio Beta", "Alfa News", "Gama FM"} {
		mcptest.Emissora(t, ctx, pool, n, 1000)
	}
	h := &handlers.StationsHandler{Repo: catalog.NewStations(pool)}
	var tela catalog.ListOutput
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/stations?q=alfa&page=1&limit=25", nil, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "buscar_emissoras", map[string]any{"busca": "alfa"})
	var idsTela []string
	for _, s := range tela.Data {
		idsTela = append(idsTela, s.ID.String())
	}
	require.Equal(t, idsTela, ids(t, itens(t, out), "id"))
	require.EqualValues(t, tela.Total, pag(t, out)["total"])

	texto, _ := chamarNoServidor(t, novoServidor(cfgTeste(pool), principalTeste), "buscar_emissoras", map[string]any{})
	require.NotContains(t, texto, "teste.mcp/", "stream_url nunca sai")
}

func TestBuscarEmissoras_ContratadaPor(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	dentro := mcptest.Emissora(t, ctx, pool, "Contratada", 0)
	mcptest.Emissora(t, ctx, pool, "Fora", 0)
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", dentro)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")

	out := chamarTool(t, cfgTeste(pool), "buscar_emissoras", map[string]any{"contratada_por": cli.String()})
	require.Equal(t, []string{dentro.String()}, ids(t, itens(t, out), "id"))
	require.Equal(t, true, itens(t, out)[0]["contrato"].(map[string]any)["no_ar"])
}

func TestObterEmissora_AnatelCidadesECampanhas(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio Goiás", 1500)
	_, err := pool.Exec(ctx, `
		UPDATE stations SET anatel_class = 'A4', anatel_coverage_km = 24, anatel_reach_km = 36, anatel_match_tier = 'exact'
		WHERE id = $1`, est)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO station_coverage_cities (station_id, ibge_code, city, state, distance_km, is_home, latitude, longitude)
		VALUES ($1, 5208707, 'Goiânia', 'GO', 0, true, -16.68, -49.25)`, est)
	require.NoError(t, err)
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")

	h := &handlers.StationsHandler{Repo: catalog.NewStations(pool)}
	var tela catalog.Station
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/x", map[string]string{"id": est.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "obter_emissora", map[string]any{"emissora_id": est.String()})
	e := out["emissora"].(map[string]any)
	require.Equal(t, tela.Name, e["nome"])
	require.EqualValues(t, *tela.PMM, e["pmm"])
	a := out["anatel"].(map[string]any)
	require.Equal(t, "A4", a["classe"])
	require.EqualValues(t, 36, a["alcance_km"])
	cid := out["cidades_no_alcance"].(map[string]any)
	require.EqualValues(t, 1, cid["total"])
	require.Equal(t, "Goiânia", cid["itens"].([]any)[0].(map[string]any)["cidade"])
	require.Equal(t, camp.String(), out["campanhas"].([]any)[0].(map[string]any)["campanha"].(map[string]any)["id"])
	require.NotEmpty(t, out["ressalvas"])

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_emissora",
		map[string]any{"emissora_id": uuid.NewString()}), "[nao_encontrado]")
}
