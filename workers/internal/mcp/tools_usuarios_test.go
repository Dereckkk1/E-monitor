package mcp

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/api/handlers"
	"radiocheck/internal/mcp/mcptest"
	"radiocheck/internal/users"
)

func TestListarUsuarios_ParidadeSemTokenDeBoasVindas(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	mcptest.Usuario(t, ctx, pool, "operator", true)
	cli := mcptest.Cliente(t, ctx, pool, "Cliente")
	var viewer uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role, name, is_active, client_id)
		VALUES ('cliente@mcp.teste', 'x', 'viewer', 'Pessoa do Cliente', true, $1) RETURNING id`, cli).Scan(&viewer))
	_, err := pool.Exec(ctx, `
		INSERT INTO user_welcome_invites (user_id, token, email_status, open_count)
		VALUES ($1, 'TOKEN-SECRETO-NAO-PODE-SAIR', 'sent', 2)`, viewer)
	require.NoError(t, err)

	h := handlers.NewUsersHandler(users.NewRepo(pool), nil)
	var tela struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Total int `json:"total"`
	}
	require.NoError(t, json.Unmarshal(chamarHandler(t, h.List, "/admin/users?page=1&page_size=25", nil, admin), &tela))
	out := chamarTool(t, cfgTeste(pool), "listar_usuarios", map[string]any{})
	var idsTela []string
	for _, u := range tela.Data {
		idsTela = append(idsTela, u.ID)
	}
	require.Equal(t, idsTela, ids(t, itens(t, out), "id"))
	require.EqualValues(t, tela.Total, pag(t, out)["total"])

	soClientes := chamarTool(t, cfgTeste(pool), "listar_usuarios", map[string]any{"papel": "cliente"})
	u := itens(t, soClientes)[0]
	require.Equal(t, "cliente", u["papel"])
	require.Equal(t, "Cliente", u["clientes"].([]any)[0].(map[string]any)["nome"])
	bv := u["boas_vindas"].(map[string]any)
	require.Equal(t, "sent", bv["status_email"])
	require.EqualValues(t, 2, bv["aberturas"])

	texto, _ := chamarNoServidor(t, novoServidor(cfgTeste(pool), principalTeste), "listar_usuarios", map[string]any{})
	require.NotContains(t, texto, "TOKEN-SECRETO", "o token de boas-vindas abre a senha inicial: nunca sai")
	require.NotContains(t, texto, "boasvindas")

	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_usuarios", map[string]any{"papel": "dono"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "listar_usuarios", map[string]any{"situacao": "talvez"}), "[entrada_invalida]")
}

func TestObterUsuario(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	op := mcptest.Usuario(t, ctx, pool, "operator", false)
	out := chamarTool(t, cfgTeste(pool), "obter_usuario", map[string]any{"usuario_id": op.String()})
	require.Equal(t, "operator", out["usuario"].(map[string]any)["papel"])
	require.Equal(t, "inativo", out["usuario"].(map[string]any)["situacao"])
	require.Nil(t, out["usuario"].(map[string]any)["nome_de_usuario"], "sem username = null, não string vazia")
	_, err := pool.Exec(ctx, `UPDATE users SET username = 'joao.silva' WHERE id = $1`, op)
	require.NoError(t, err)
	out = chamarTool(t, cfgTeste(pool), "obter_usuario", map[string]any{"usuario_id": op.String()})
	require.Equal(t, "joao.silva", out["usuario"].(map[string]any)["nome_de_usuario"])
	require.Contains(t, chamarToolErro(t, cfgTeste(pool), "obter_usuario",
		map[string]any{"usuario_id": uuid.NewString()}), "[nao_encontrado]")
}
