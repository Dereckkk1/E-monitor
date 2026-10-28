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

func TestMapaCoberturaCampanha_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 1000)
	semCoord := mcptest.Emissora(t, ctx, pool, "Rádio Sem Coordenada", 1000)
	mcptest.Cobertura(t, ctx, pool, e1, "A1", 50, 75,
		mcptest.CidadeAlcance{IBGE: 1, Nome: "Sede Um", Km: 0}, mcptest.CidadeAlcance{IBGE: 2, Nome: "Vizinha", Km: 40},
		mcptest.CidadeAlcance{IBGE: 3, Nome: "Longe", Km: 70})
	mcptest.Cobertura(t, ctx, pool, e2, "C", 10, 15,
		mcptest.CidadeAlcance{IBGE: 2, Nome: "Vizinha", Km: 5}) // a mesma cidade por duas emissoras
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", e1, e2, semCoord)
	mcptest.EstadoCampanha(t, ctx, pool, camp, "ativa")

	h := handlers.NewLiveMapHandler(catalog.NewLiveMap(pool))
	var cob catalog.LiveCoverageResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.GetCoverage, "/live-map/coverage?campaigns="+camp.String(), nil, admin), &cob))
	var mapa catalog.LiveMapResult
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.Get, "/live-map?campaigns="+camp.String(), nil, admin), &mapa))

	out := chamarTool(t, cfgTeste(pool), "mapa_cobertura_campanha", map[string]any{"campanha_ids": []string{camp.String()}})
	require.EqualValues(t, len(mapa.Stations), pag(t, out)["total"])
	require.EqualValues(t, 2, pag(t, out)["total"], "a emissora sem coordenada não está no mapa")
	require.EqualValues(t, 3, out["cidades_distintas"], "Vizinha conta uma vez")
	require.EqualValues(t, len(cob.Cities), out["pares_emissora_cidade"])
	xs := itens(t, out)
	for i, s := range mapa.Stations {
		require.Equal(t, s.ID.String(), xs[i]["emissora"].(map[string]any)["id"], "ordem da tela: por nome")
	}
	require.Equal(t, "Rádio Dois", xs[0]["emissora"].(map[string]any)["nome"])
	dois := xs[1]
	require.Equal(t, "A1", dois["classe_anatel"])
	require.EqualValues(t, 50, dois["contorno_protegido_km"])
	require.EqualValues(t, 75, dois["alcance_km"])
	require.EqualValues(t, 3, dois["cidades_total"])
	require.Equal(t, "Sede Um", dois["cidades"].([]any)[0].(map[string]any)["nome"])
	require.Equal(t, true, dois["cidades"].([]any)[0].(map[string]any)["sede"])
	require.Equal(t, "https://portal.teste/live-map?campaigns="+camp.String()+"&client_id="+cli.String(),
		out["links"].(map[string]any)["tela"])
}

// Lista interna de cidades tem teto por item, com o total real e a ressalva.
func TestMapaCoberturaCampanha_CidadesComTeto(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio Grande", 1000)
	var cidades []mcptest.CidadeAlcance
	for i := range tetoPorItem + 5 {
		cidades = append(cidades, mcptest.CidadeAlcance{IBGE: 1000 + i, Nome: fmt.Sprintf("Cidade %02d", i), Km: float64(i)})
	}
	mcptest.Cobertura(t, ctx, pool, est, "E1", 100, 150, cidades...)
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)

	out := chamarTool(t, cfgTeste(pool), "mapa_cobertura_campanha", map[string]any{"campanha_ids": []string{camp.String()}})
	x := itens(t, out)[0]
	require.Len(t, x["cidades"], tetoPorItem)
	require.EqualValues(t, tetoPorItem+5, x["cidades_total"])
	require.EqualValues(t, tetoPorItem+5, out["cidades_distintas"])
	require.Contains(t, out["ressalvas"].([]any)[0], fmt.Sprintf("as %d mais próximas", tetoPorItem))
}

func TestMapaCoberturaCampanha_Recusas(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31")
	mcptest.EstadoCampanha(t, ctx, pool, camp, "cancelada")
	cfg := cfgTeste(pool)
	require.Contains(t, chamarToolErro(t, cfg, "mapa_cobertura_campanha", map[string]any{"campanha_ids": []string{}}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "mapa_cobertura_campanha", map[string]any{"campanha_ids": []string{"x"}}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "mapa_cobertura_campanha", map[string]any{"campanha_ids": []string{uuid.NewString()}}), "[nao_encontrado]")
	require.Contains(t, chamarToolErro(t, cfg, "mapa_cobertura_campanha", map[string]any{"campanha_ids": []string{camp.String()}}), "cancelada")
}
