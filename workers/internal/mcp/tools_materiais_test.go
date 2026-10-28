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

func TestListarMateriais_ParidadeSemCaminhoDoArquivo(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mcptest.Material(t, ctx, pool, cli, tipo, "Verão 30s")
	mcptest.Material(t, ctx, pool, cli, tipo, "Verão 15s")
	mcptest.Material(t, ctx, pool, cli, tipo, "Inverno 30s")

	h := &handlers.MaterialsHandler{Repo: catalog.NewMaterials(pool)}
	var tela struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.ListByClient, "/x?q=ver", map[string]string{"clientID": cli.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "listar_materiais", map[string]any{"cliente_id": cli.String(), "busca": "ver"})
	var idsTela []string
	for _, m := range tela.Data {
		idsTela = append(idsTela, m.ID)
	}
	require.Equal(t, idsTela, ids(t, itens(t, out), "id"))
	require.Equal(t, tipo.String(), itens(t, out)[0]["tipo"].(map[string]any)["id"])

	texto, _ := chamarNoServidor(t, novoServidor(cfgTeste(pool), principalTeste), "listar_materiais", map[string]any{"cliente_id": cli.String()})
	require.NotContains(t, texto, "/tmp/mcp-", "caminho do master nunca sai")
	require.NotContains(t, texto, "master_sha256")
}

func TestObterMaterial_SimilaridadeECampanhas(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 0)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	original := mcptest.Material(t, ctx, pool, cli, tipo, "Original")
	copia := mcptest.Material(t, ctx, pool, cli, tipo, "Cópia")
	camp := mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	mcptest.Vincular(t, ctx, pool, camp, copia, est)
	_, err := pool.Exec(ctx, `
		UPDATE materials SET similarity_check_status = 'ready', most_similar_material_id = $1,
		       similarity_score = 0.8, script = 'Leia devagar'
		WHERE id = $2`, original, copia)
	require.NoError(t, err)

	out := chamarTool(t, cfgTeste(pool), "obter_material", map[string]any{"material_id": copia.String()})
	m := out["material"].(map[string]any)
	sim := m["similaridade"].(map[string]any)
	require.Equal(t, "Original", sim["parecido_com"].(map[string]any)["nome"])
	require.Equal(t, "Leia devagar", out["roteiro"])
	require.Equal(t, camp.String(), out["campanhas"].([]any)[0].(map[string]any)["campanha"].(map[string]any)["id"])
	require.NotEmpty(t, out["ressalvas"], "≥ 50% e não reconhecido: avisa")

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_material",
		map[string]any{"material_id": uuid.NewString()}), "[nao_encontrado]")
}

func TestListarTiposMaterial_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	h := &handlers.MaterialTypesHandler{Repo: catalog.NewMaterialTypes(pool)}
	var tela []catalog.MaterialType
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/material-types", nil, admin), &tela))
	out := chamarTool(t, cfgTeste(pool), "listar_tipos_material", map[string]any{"limit": 100})
	require.EqualValues(t, len(tela), pag(t, out)["total"])
	require.Equal(t, tela[0].Name, itens(t, out)[0]["nome"])
}
