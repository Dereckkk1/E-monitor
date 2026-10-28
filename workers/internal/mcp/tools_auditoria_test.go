package mcp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/auditoria"
	"radiocheck/internal/mcp/mcptest"
)

func TestAuditoriaMCP_CursorETotalNulo(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`) // o mcptest não trunca o audit_log
	require.NoError(t, err)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	alvo := uuid.New()
	require.NoError(t, auditoria.Registrar(ctx, pool, admin, "mcp.token.criado", "mcp_token", &alvo, map[string]any{"nome": "laptop"}))
	require.NoError(t, auditoria.Registrar(ctx, pool, admin, "mcp.token.revogado", "mcp_token", nil, nil))
	require.NoError(t, auditoria.RegistrarSistema(ctx, pool, "revoga-mcp", "mcp.token.revogado", "mcp_token", nil, map[string]any{"quantidade": 2}))
	_, err = pool.Exec(ctx, `UPDATE audit_log SET created_at = now() - interval '1 hour' WHERE action = 'mcp.token.criado'`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE audit_log SET created_at = now() - interval '2 hours' WHERE actor_type = 'cli'`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO audit_log (actor_type, action, resource) VALUES ('system', 'client.hub_link', 'client')`)
	require.NoError(t, err)

	cfg := cfgTeste(pool)
	p1 := chamarTool(t, cfg, "auditoria_mcp", map[string]any{"limit": 1})
	pg := pag(t, p1)
	require.Contains(t, pg, "total")
	require.Nil(t, pg["total"], "coleção que só cresce: total null (não contado), nunca 0")
	require.Equal(t, true, pg["has_more"])
	require.EqualValues(t, 1, pg["returned"])
	require.Equal(t, "mcp.token.revogado", itens(t, p1)[0]["acao"])
	require.Equal(t, admin.String(), itens(t, p1)[0]["ator"].(map[string]any)["id"])
	require.Nil(t, itens(t, p1)[0]["recurso_id"])
	require.Equal(t, map[string]any{}, itens(t, p1)[0]["detalhes"])
	cur := pg["next_cursor"].(string)

	p2 := chamarTool(t, cfg, "auditoria_mcp", map[string]any{"limit": 1, "cursor": cur})
	e := itens(t, p2)[0]
	require.Equal(t, "mcp.token.criado", e["acao"])
	require.Equal(t, "laptop", e["detalhes"].(map[string]any)["nome"])
	require.NotContains(t, e["detalhes"], "origem")
	require.Equal(t, alvo.String(), e["recurso_id"])
	require.Equal(t, "Pessoa de Teste", e["ator"].(map[string]any)["nome"])

	p3 := chamarTool(t, cfg, "auditoria_mcp", map[string]any{"limit": 1, "cursor": pag(t, p2)["next_cursor"]})
	e = itens(t, p3)[0]
	require.Nil(t, e["ator"], "CLI na VM não tem usuário do portal")
	require.Equal(t, "cli", e["tipo_ator"])
	require.Equal(t, "revoga-mcp", e["detalhes"].(map[string]any)["pelo"])
	require.Equal(t, false, pag(t, p3)["has_more"], "client.hub_link não é do MCP")
	require.Nil(t, pag(t, p3)["next_cursor"])

	so := chamarTool(t, cfg, "auditoria_mcp", map[string]any{"usuario_id": admin.String()})
	require.Len(t, itens(t, so), 2)
	require.Len(t, itens(t, chamarTool(t, cfg, "auditoria_mcp", map[string]any{"acao": "mcp.token.cri"})), 1)
	require.EqualValues(t, tetoPaginaAuditoria, pag(t, chamarTool(t, cfg, "auditoria_mcp", map[string]any{"limit": 1000}))["limit"])

	require.Contains(t, chamarToolErro(t, cfg, "auditoria_mcp", map[string]any{"cursor": "lixo"}), "[entrada_invalida]")
	require.Contains(t, chamarToolErro(t, cfg, "auditoria_mcp", map[string]any{"usuario_id": "x"}), "[entrada_invalida]")
}

// Pior caso de uma página: o teto de eventos, cada um com detalhes logo
// abaixo do teto de bytes (acima ele seria resumido), e ator com nome/email
// longos (alvo < 60 KB).
func TestAuditoriaMCP_PiorCasoCabe(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	_, err = pool.Exec(ctx, `UPDATE users SET name = $2, email = $3 WHERE id = $1`, admin,
		strings.Repeat("Nome Completo ", 4), strings.Repeat("a", 40)+"@agencia-de-publicidade.com.br")
	require.NoError(t, err)
	for i := range tetoPaginaAuditoria + 1 {
		require.NoError(t, auditoria.Registrar(ctx, pool, admin, "mcp.oauth.cliente_confidencial_criado", "mcp_oauth_client", nil,
			map[string]any{"client_id": uuid.NewString(), "cliente": fmt.Sprintf("Integração Do Hub De Clientes Da Agência %03d", i),
				"alvo_user_id": uuid.NewString(), "alvo_email": strings.Repeat("b", 40) + "@cliente.com.br", "quantidade": i,
				"observacao": strings.Repeat("ç", 800)})) // ~1,6 KB em UTF-8: o evento inteiro fica logo abaixo de 2 KB
	}
	out := chamarTool(t, cfgTeste(pool), "auditoria_mcp", map[string]any{"limit": 100})
	require.Len(t, itens(t, out), tetoPaginaAuditoria)
	require.EqualValues(t, tetoPaginaAuditoria, pag(t, out)["limit"], "limit acima do teto é rebaixado e ecoado")
	require.Equal(t, true, pag(t, out)["has_more"])
	require.NotContains(t, out, "ressalvas", "nenhum evento passou do teto: os detalhes saem inteiros")
	n := tamanhoDoResultado(t, out)
	t.Logf("auditoria_mcp: %d eventos com detalhes perto de 2 KB = %d bytes", tetoPaginaAuditoria, n)
	require.Less(t, n, 60_000)
}

// Detalhes acima de 2 KB saem resumidos (com ressalva); segredo nunca sai.
func TestAuditoriaMCP_DetalhesGrandesESegredos(t *testing.T) {
	ctx, pool := mcptest.DBCatalogo(t)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)
	admin := mcptest.Usuario(t, ctx, pool, "admin", true)
	require.NoError(t, auditoria.Registrar(ctx, pool, admin, "mcp.grande", "mcp_token", nil,
		map[string]any{"texto": strings.Repeat("x", 3000), "nome": "laptop"}))
	require.NoError(t, auditoria.Registrar(ctx, pool, admin, "mcp.pequeno", "mcp_token", nil,
		map[string]any{"nome": "laptop", "token": "SEGREDO-TOKEN"}))
	cfg := cfgTeste(pool)

	texto := textoTool(t, cfg, "auditoria_mcp", map[string]any{})
	require.NotContains(t, texto, "SEGREDO-TOKEN")
	require.NotContains(t, texto, strings.Repeat("x", 100))
	out := chamarTool(t, cfg, "auditoria_mcp", map[string]any{})
	porAcao := map[string]map[string]any{}
	for _, e := range itens(t, out) {
		porAcao[e["acao"].(string)] = e["detalhes"].(map[string]any)
	}
	require.Equal(t, map[string]any{"nome": "laptop", "token": "[omitido]"}, porAcao["mcp.pequeno"])
	require.Len(t, porAcao["mcp.grande"], 1)
	require.Contains(t, porAcao["mcp.grande"]["_resumo"], "chaves: nome, texto")
	require.Equal(t, []any{ressalvaDetalhesResumidos}, out["ressalvas"])

	// Sem evento grande, sem a ressalva.
	so := chamarTool(t, cfg, "auditoria_mcp", map[string]any{"acao": "mcp.pequeno"})
	require.NotContains(t, so, "ressalvas")
}
