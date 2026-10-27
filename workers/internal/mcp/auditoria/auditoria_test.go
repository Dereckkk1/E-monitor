package auditoria_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/auditoria"
	"radiocheck/internal/mcp/mcptest"
)

func TestRegistrar_GravaComOrigemMCP(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	ator := mcptest.Usuario(t, ctx, pool, "admin", true)
	alvo := uuid.New()
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)

	require.NoError(t, auditoria.Registrar(ctx, pool, ator, "mcp.token.criado", "mcp_token", &alvo,
		map[string]any{"nome": "notebook"}))

	var acao, recurso, origem, nome string
	var recursoID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT action, resource, resource_id, metadata->>'origem', metadata->>'nome'
		   FROM audit_log WHERE actor_id = $1`, ator).Scan(&acao, &recurso, &recursoID, &origem, &nome))
	require.Equal(t, "mcp.token.criado", acao)
	require.Equal(t, "mcp_token", recurso)
	require.Equal(t, alvo, recursoID)
	require.Equal(t, "mcp", origem)
	require.Equal(t, "notebook", nome)
}

func TestRegistrar_OrigemNaoSobrescrevivel(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	ator := mcptest.Usuario(t, ctx, pool, "admin", true)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)

	require.NoError(t, auditoria.Registrar(ctx, pool, ator, "mcp.x", "mcp_token", nil,
		map[string]any{"origem": "manual"}))

	var origem string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT metadata->>'origem' FROM audit_log WHERE actor_id = $1`, ator).Scan(&origem))
	require.Equal(t, "mcp", origem)
}

func TestRegistrarSistema_SemAtorComPeloEOrigemForcados(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)
	alvo := uuid.New()

	require.NoError(t, auditoria.RegistrarSistema(ctx, pool, "revoga-mcp", "mcp.token.revogado", "mcp_token", &alvo,
		map[string]any{"pelo": "outro", "origem": "manual", "quantidade": 3}))

	var ator *uuid.UUID
	var tipo, acao, recurso, pelo, origem, quantidade string
	var recursoID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT actor_id, actor_type, action, resource, resource_id,
		        metadata->>'pelo', metadata->>'origem', metadata->>'quantidade'
		   FROM audit_log`).Scan(&ator, &tipo, &acao, &recurso, &recursoID, &pelo, &origem, &quantidade))
	require.Nil(t, ator, "CLI na VM não tem usuário do portal")
	require.Equal(t, "cli", tipo)
	require.Equal(t, "mcp.token.revogado", acao)
	require.Equal(t, "mcp_token", recurso)
	require.Equal(t, alvo, recursoID)
	require.Equal(t, "revoga-mcp", pelo, "pelo não é sobrescrevível pelo meta")
	require.Equal(t, "mcp", origem)
	require.Equal(t, "3", quantidade)
}
