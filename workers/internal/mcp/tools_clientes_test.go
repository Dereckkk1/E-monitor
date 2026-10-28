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

func TestListarClientes_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	a := mcptest.Cliente(t, ctx, pool, "Alfa")
	mcptest.Cliente(t, ctx, pool, "Beta")
	g := mcptest.Cliente(t, ctx, pool, "Gama")
	_, err := pool.Exec(ctx, `UPDATE clients SET is_active = false WHERE id = $1`, g)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE clients SET hub_id = 'hub-alfa' WHERE id = $1`, a)
	require.NoError(t, err)

	h := &handlers.ClientsHandler{Repo: catalog.NewClients(pool)}
	for _, inativos := range []bool{false, true} {
		alvo := "/clients?page=1&page_size=25"
		if inativos {
			alvo += "&include_inactive=1"
		}
		var tela struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			Total int `json:"total"`
		}
		require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, alvo, nil, admin), &tela))
		out := chamarTool(t, cfgTeste(pool), "listar_clientes", map[string]any{"incluir_inativos": inativos})
		var idsTela []string
		for _, c := range tela.Data {
			idsTela = append(idsTela, c.ID)
		}
		require.Equal(t, idsTela, ids(t, itens(t, out), "id"), "inativos=%v", inativos)
		require.EqualValues(t, tela.Total, pag(t, out)["total"])
	}

	out := chamarTool(t, cfgTeste(pool), "listar_clientes", map[string]any{"busca": "alfa"})
	require.Equal(t, "hub-alfa", itens(t, out)[0]["hub_id"])
}

func TestListarClientes_OffsetForaDoMultiplo(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	for _, n := range []string{"Alfa", "Beta", "Delta", "Epsilon", "Gama"} {
		mcptest.Cliente(t, ctx, pool, n)
	}
	out := chamarTool(t, cfgTeste(pool), "listar_clientes", map[string]any{"limit": 2, "offset": 3})
	var nomes []string
	for _, c := range itens(t, out) {
		nomes = append(nomes, c["nome"].(string))
	}
	require.Equal(t, []string{"Epsilon", "Gama"}, nomes)
	require.EqualValues(t, 5, pag(t, out)["total"])
	require.Equal(t, false, pag(t, out)["has_more"])
}

func TestObterCliente(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente Completo")
	_, err := pool.Exec(ctx, `UPDATE clients SET hub_id = 'hub-x' WHERE id = $1`, cli)
	require.NoError(t, err)
	est := mcptest.Emissora(t, ctx, pool, "Rádio", 0)
	tipo := mcptest.TipoMaterial(t, ctx, pool, "Spot")
	mcptest.Material(t, ctx, pool, cli, tipo, "Spot A")
	mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", est)
	_, err = pool.Exec(ctx, `
		INSERT INTO users (email, password_hash, role, name, is_active, client_id)
		VALUES ('viewer@mcp.teste', 'x', 'viewer', 'Pessoa do Cliente', true, $1)`, cli)
	require.NoError(t, err)

	out := chamarTool(t, cfgTeste(pool), "obter_cliente", map[string]any{"cliente_id": cli.String()})
	require.Equal(t, "hub-x", out["cliente"].(map[string]any)["hub_id"])
	require.Equal(t, map[string]any{"campanhas": 1.0, "materiais": 1.0, "usuarios": 1.0}, out["vinculos"])
	u := out["usuarios"].([]any)[0].(map[string]any)
	require.Equal(t, "cliente", u["papel"], "viewer aparece como cliente, o vocabulário da tela")
	require.Equal(t, "ativo", u["situacao"])

	// Usuário de agência: cliente primário é outro, o vínculo com este vem só de user_clients.
	outro := mcptest.Cliente(t, ctx, pool, "Outro Cliente")
	var agenciaID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role, name, is_active, client_id)
		VALUES ('agencia@mcp.teste', 'x', 'viewer', 'Pessoa da Agência', true, $1) RETURNING id`, outro).Scan(&agenciaID))
	_, err = pool.Exec(ctx, `INSERT INTO user_clients (user_id, client_id) VALUES ($1, $2)`, agenciaID, cli)
	require.NoError(t, err)
	comAgencia := chamarTool(t, cfgTeste(pool), "obter_cliente", map[string]any{"cliente_id": cli.String()})
	var nomes []string
	for _, p := range comAgencia["usuarios"].([]any) {
		nomes = append(nomes, p.(map[string]any)["nome"].(string))
	}
	require.Contains(t, nomes, "Pessoa da Agência", "vinculada só por user_clients")
	require.Contains(t, nomes, "Pessoa do Cliente")

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_cliente", map[string]any{"cliente_id": "nao-e-uuid"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_cliente",
		map[string]any{"cliente_id": "00000000-0000-0000-0000-000000000001"}), "[nao_encontrado]")
}

func TestPMMTargetCliente_ParidadeComATela(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente PMM")
	e1 := mcptest.Emissora(t, ctx, pool, "Rádio Um", 1000)
	e2 := mcptest.Emissora(t, ctx, pool, "Rádio Dois", 2000)
	mcptest.Campanha(t, ctx, pool, cli, "Camp", "2026-10-01", "2026-10-31", e1, e2)
	_, err := pool.Exec(ctx, `INSERT INTO client_station_pmm (client_id, station_id, pmm_target) VALUES ($1, $2, 500)`, cli, e1)
	require.NoError(t, err)

	h := &handlers.ClientTargetPmmHandler{Repo: catalog.NewClientStationPMM(pool)}
	var tela struct {
		Data []struct {
			StationID string `json:"station_id"`
			PMMTarget *int   `json:"pmm_target"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/clients/x/target-pmm",
		map[string]string{"clientID": cli.String()}, admin), &tela))

	out := chamarTool(t, cfgTeste(pool), "pmm_target_cliente", map[string]any{"cliente_id": cli.String()})
	linhas := itens(t, out)
	require.Len(t, linhas, len(tela.Data))
	for i, l := range linhas {
		require.Equal(t, tela.Data[i].StationID, l["emissora"].(map[string]any)["id"])
		if tela.Data[i].PMMTarget == nil {
			require.Nil(t, l["pmm_target"], "sem linha = null, nunca zero")
		} else {
			require.EqualValues(t, *tela.Data[i].PMMTarget, l["pmm_target"])
		}
	}
	require.NotEmpty(t, out["ressalvas"])
}
