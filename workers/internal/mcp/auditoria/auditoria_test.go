package auditoria_test

import (
	"fmt"
	"slices"
	"strings"
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

func TestListar_SoMCPMaisRecentePrimeiroComCursor(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)
	ator := mcptest.Usuario(t, ctx, pool, "admin", true)
	outro := mcptest.Usuario(t, ctx, pool, "admin", true)
	for i := 0; i < 3; i++ {
		require.NoError(t, auditoria.Registrar(ctx, pool, ator, fmt.Sprintf("mcp.token.criado.%d", i), "mcp_token", nil, map[string]any{"n": i}))
		// created_at explícito e crescente: instantes iguais fariam a ordem depender do uuid.
		_, err = pool.Exec(ctx, `UPDATE audit_log SET created_at = now() - make_interval(secs => $2) WHERE action = $1`,
			fmt.Sprintf("mcp.token.criado.%d", i), 10-i)
		require.NoError(t, err)
	}
	require.NoError(t, auditoria.Registrar(ctx, pool, outro, "mcp.oauth.autorizado", "mcp_oauth_client", nil, nil))
	_, err = pool.Exec(ctx, `UPDATE audit_log SET created_at = now() - interval '1 hour' WHERE action = 'mcp.oauth.autorizado'`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO audit_log (actor_type, action, resource) VALUES ('system', 'client.hub_link', 'client')`) // sem origem mcp
	require.NoError(t, err)
	repo := auditoria.NovoRepo(pool)

	p1, prox, err := repo.Listar(ctx, auditoria.Filtro{Limite: 2})
	require.NoError(t, err)
	require.Len(t, p1, 2)
	require.Equal(t, "mcp.token.criado.2", p1[0].Acao)
	require.Equal(t, "mcp.token.criado.1", p1[1].Acao)
	require.Equal(t, map[string]any{"n": float64(2)}, p1[0].Detalhes, "origem sai dos detalhes")
	require.Equal(t, ator, *p1[0].AtorID)
	require.Equal(t, "Pessoa de Teste", *p1[0].AtorNome)
	require.NotEmpty(t, prox)
	p2, prox2, err := repo.Listar(ctx, auditoria.Filtro{Limite: 2, Cursor: prox})
	require.NoError(t, err)
	require.Len(t, p2, 2)
	require.Equal(t, "mcp.token.criado.0", p2[0].Acao)
	require.Equal(t, "mcp.oauth.autorizado", p2[1].Acao)
	require.Empty(t, prox2, "fim: client.hub_link não é do MCP")

	for _, lixo := range []string{"lixo", "bm9wZQ", "MjAyNi0xMC0yOFQwMDowMDowMFp8eA"} { // não-base64, sem "|", uuid inválido
		_, _, err = repo.Listar(ctx, auditoria.Filtro{Limite: 2, Cursor: lixo})
		require.ErrorIs(t, err, auditoria.ErrCursorInvalido, lixo)
	}
	f, _, err := repo.Listar(ctx, auditoria.Filtro{Limite: 10, AcaoPrefixo: "mcp.token.criado.1"})
	require.NoError(t, err)
	require.Len(t, f, 1)
	f, _, err = repo.Listar(ctx, auditoria.Filtro{Limite: 10, AcaoPrefixo: "mcp.token"})
	require.NoError(t, err)
	require.Len(t, f, 3)
	f, _, err = repo.Listar(ctx, auditoria.Filtro{Limite: 10, AcaoPrefixo: "mcp_token"}) // _ é literal, não curinga
	require.NoError(t, err)
	require.Empty(t, f)
	f, _, err = repo.Listar(ctx, auditoria.Filtro{Limite: 10, AtorID: &outro})
	require.NoError(t, err)
	require.Len(t, f, 1)
	require.Equal(t, "mcp.oauth.autorizado", f[0].Acao)
	require.Nil(t, f[0].Detalhes["origem"])
}

// Empate de instante: o id desempata, e o cursor não repete nem pula ninguém.
func TestListar_EmpateDeInstanteNaoRepeteNemPula(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)
	ator := mcptest.Usuario(t, ctx, pool, "admin", true)
	for i := 0; i < 5; i++ {
		require.NoError(t, auditoria.Registrar(ctx, pool, ator, fmt.Sprintf("mcp.x.%d", i), "mcp_token", nil, nil))
	}
	_, err = pool.Exec(ctx, `UPDATE audit_log SET created_at = '2026-10-28T12:00:00.123456Z'`)
	require.NoError(t, err)
	repo := auditoria.NovoRepo(pool)
	var vistos []string
	cursor := ""
	for range 10 {
		p, prox, err := repo.Listar(ctx, auditoria.Filtro{Limite: 2, Cursor: cursor})
		require.NoError(t, err)
		for _, e := range p {
			vistos = append(vistos, e.ID.String())
		}
		if prox == "" {
			break
		}
		cursor = prox
	}
	require.Len(t, vistos, 5)
	require.True(t, slices.IsSortedFunc(vistos, func(a, b string) int { return strings.Compare(b, a) }), "id DESC no empate: %v", vistos)
}

// O prefixo é literal: % e \ não viram curinga nem escape (ESCAPE '\' no SQL).
func TestListar_PrefixoLiteral(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)
	ator := mcptest.Usuario(t, ctx, pool, "admin", true)
	require.NoError(t, auditoria.Registrar(ctx, pool, ator, "mcp.token.criado", "mcp_token", nil, nil))
	repo := auditoria.NovoRepo(pool)
	for _, prefixo := range []string{"%", `mcp\`, `mcp\%`, "mcp_token", "_"} {
		f, _, err := repo.Listar(ctx, auditoria.Filtro{Limite: 10, AcaoPrefixo: prefixo})
		require.NoError(t, err, prefixo)
		require.Empty(t, f, "prefixo %q é literal", prefixo)
	}
	f, _, err := repo.Listar(ctx, auditoria.Filtro{Limite: 10, AcaoPrefixo: "mcp."})
	require.NoError(t, err)
	require.Len(t, f, 1)
}

// Chave que parece segredo (em qualquer nível) sai com o valor omitido.
func TestListar_OmiteChavesDeSegredo(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)
	ator := mcptest.Usuario(t, ctx, pool, "admin", true)
	require.NoError(t, auditoria.Registrar(ctx, pool, ator, "mcp.x", "mcp_token", nil, map[string]any{
		"nome": "laptop", "token_claro": "SEGREDO-1", "Client_Secret": "SEGREDO-2", "SENHA": "SEGREDO-3",
		"aninhado": map[string]any{"password": "SEGREDO-4", "ok": 1},
		"lista":    []any{map[string]any{"refresh_token": "SEGREDO-5"}},
	}))
	evs, _, err := auditoria.NovoRepo(pool).Listar(ctx, auditoria.Filtro{Limite: 10})
	require.NoError(t, err)
	require.Len(t, evs, 1)
	d := evs[0].Detalhes
	require.Equal(t, "laptop", d["nome"])
	for _, k := range []string{"token_claro", "Client_Secret", "SENHA"} {
		require.Equal(t, "[omitido]", d[k], k)
	}
	require.Equal(t, map[string]any{"password": "[omitido]", "ok": float64(1)}, d["aninhado"])
	require.Equal(t, []any{map[string]any{"refresh_token": "[omitido]"}}, d["lista"])
}

// As chaves de credencial além de secret/token/senha/password também saem
// omitidas — inclusive as inofensivas que casam por substring (token_id), de
// propósito.
func TestListar_OmiteChavesDeCredencial(t *testing.T) {
	ctx, pool := mcptest.DB(t)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)
	ator := mcptest.Usuario(t, ctx, pool, "admin", true)
	omitidas := []string{"segredo_app", "chave_pix", "credencial", "Credentials", "api_key", "api-key", "apikey", "ApiKey",
		"Authorization", "bearer", "cookie", "set_cookie", "jwt", "private_key", "code_verifier", "session_id", "token_id"}
	meta := map[string]any{"nome": "laptop", "client_id": "abc", "cliente": "Hub"}
	for _, k := range omitidas {
		meta[k] = "VALOR-" + k
	}
	require.NoError(t, auditoria.Registrar(ctx, pool, ator, "mcp.x", "mcp_token", nil, meta))
	evs, _, err := auditoria.NovoRepo(pool).Listar(ctx, auditoria.Filtro{Limite: 10})
	require.NoError(t, err)
	d := evs[0].Detalhes
	for _, k := range omitidas {
		require.Equal(t, "[omitido]", d[k], k)
	}
	require.Equal(t, "laptop", d["nome"])
	require.Equal(t, "abc", d["client_id"], "client_id é identificador público do cliente OAuth")
	require.Equal(t, "Hub", d["cliente"])
}
