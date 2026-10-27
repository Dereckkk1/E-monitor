package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/mcp/mcptest"
)

func TestValidar(t *testing.T) {
	require.Error(t, validar(opcoes{}), "sem modo")
	require.Error(t, validar(opcoes{listar: true, todos: true}), "dois modos")
	require.Error(t, validar(opcoes{listar: true, aplicar: true}), "--listar não aplica nada")
	require.NoError(t, validar(opcoes{listar: true}))
	require.NoError(t, validar(opcoes{email: "x@y", aplicar: true}))
	require.NoError(t, validar(opcoes{todos: true}))
}

type relatorio struct {
	Aplicado        bool     `json:"aplicado"`
	Pessoas         []string `json:"pessoas"`
	Credenciais     int      `json:"credenciais"`
	CodigosApagados int      `json:"codigos_apagados"`
}

func rodar(t *testing.T, pool *pgxpool.Pool, o opcoes) (relatorio, string) {
	t.Helper()
	var out, avisos bytes.Buffer
	require.NoError(t, executar(t.Context(), pool, o, &out, &avisos))
	var r relatorio
	require.NoError(t, json.Unmarshal(out.Bytes(), &r), out.String())
	return r, avisos.String()
}

// cenario: A tem 1 pessoal vivo, 1 access OAuth vivo, 1 vencido e 1 já
// revogado; B só tem um código pendente; C só tem credencial vencida.
func cenario(t *testing.T) (*pgxpool.Pool, uuid.UUID, uuid.UUID) {
	ctx, pool := mcptest.DB(t)
	_, err := pool.Exec(ctx, `DELETE FROM audit_log`)
	require.NoError(t, err)
	a := mcptest.Usuario(t, ctx, pool, "admin", true)
	b := mcptest.Usuario(t, ctx, pool, "admin", true)
	c := mcptest.Usuario(t, ctx, pool, "admin", true)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO mcp_oauth_clients (client_id, nome, redirect_uris) VALUES ('cli', 'Claude', ARRAY['https://claude.ai/api/mcp/auth_callback'])`, nil},
		{`INSERT INTO mcp_tokens (user_id, tipo, token_hash, nome, client_id, expires_at, revoked_at) VALUES
		    ($1, 'pessoal',      'h1', 'vivo',     NULL,  NOW() + INTERVAL '1 day', NULL),
		    ($1, 'oauth_access', 'h2', 'Claude',   'cli', NOW() + INTERVAL '1 hour', NULL),
		    ($1, 'pessoal',      'h3', 'vencido',  NULL,  NOW() - INTERVAL '1 day', NULL),
		    ($1, 'pessoal',      'h4', 'revogado', NULL,  NOW() + INTERVAL '1 day', NOW()),
		    ($2, 'pessoal',      'h5', 'vencido',  NULL,  NOW() - INTERVAL '1 day', NULL)`, []any{a, c}},
		{`INSERT INTO mcp_oauth_codes (code_hash, client_id, user_id, redirect_uri, expires_at)
		    VALUES ('c1', 'cli', $1, 'https://claude.ai/api/mcp/auth_callback', NOW() + INTERVAL '10 minutes')`, []any{b}},
	} {
		_, err = pool.Exec(ctx, q.sql, q.args...)
		require.NoError(t, err)
	}
	return pool, a, b
}

func emailDe(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) string {
	var e string
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT email FROM users WHERE id = $1`, id).Scan(&e))
	return e
}

func TestTodos_DryRunContaOQueOAplicarRevoga(t *testing.T) {
	pool, a, b := cenario(t)
	ctx := t.Context()

	seco, _ := rodar(t, pool, opcoes{todos: true})
	require.False(t, seco.Aplicado)
	require.ElementsMatch(t, []string{emailDe(t, pool, a), emailDe(t, pool, b)}, seco.Pessoas,
		"só quem tem credencial viva ou código pendente")
	require.Equal(t, 2, seco.Credenciais, "vivas, de todos os tipos; vencida e revogada não contam")
	require.Equal(t, 1, seco.CodigosApagados)

	feito, _ := rodar(t, pool, opcoes{todos: true, aplicar: true})
	require.True(t, feito.Aplicado)
	require.Equal(t, seco.Pessoas, feito.Pessoas)
	require.Equal(t, seco.Credenciais, feito.Credenciais, "o dry-run conta exatamente o que o RevogarTodos devolve")
	require.Equal(t, seco.CodigosApagados, feito.CodigosApagados)

	var vivas, codigos int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT (SELECT COUNT(*) FROM mcp_tokens WHERE revoked_at IS NULL AND expires_at > NOW()),
		        (SELECT COUNT(*) FROM mcp_oauth_codes)`).Scan(&vivas, &codigos))
	require.Zero(t, vivas)
	require.Zero(t, codigos)

	var linhas int
	var ator *uuid.UUID
	var tipo, pelo, alvo, quantidade string
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_log WHERE action = 'mcp.token.revogado'`).Scan(&linhas))
	require.Equal(t, 1, linhas, "uma linha por pessoa com credencial revogada; B só tinha código")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT actor_id, actor_type, metadata->>'pelo', metadata->>'alvo_user_id', metadata->>'quantidade'
		   FROM audit_log WHERE action = 'mcp.token.revogado'`).Scan(&ator, &tipo, &pelo, &alvo, &quantidade))
	require.Nil(t, ator)
	require.Equal(t, "cli", tipo)
	require.Equal(t, "revoga-mcp", pelo)
	require.Equal(t, a.String(), alvo)
	require.Equal(t, "2", quantidade)

	denovo, _ := rodar(t, pool, opcoes{todos: true, aplicar: true})
	require.Empty(t, denovo.Pessoas)
	require.Zero(t, denovo.Credenciais)
}

func TestEmail_SoAPessoaECaseInsensitive(t *testing.T) {
	pool, a, b := cenario(t)
	r, avisos := rodar(t, pool, opcoes{email: "  " + bytesUpper(emailDe(t, pool, b)), aplicar: true})
	require.Empty(t, avisos)
	require.Equal(t, []string{emailDe(t, pool, b)}, r.Pessoas, "quem só tem código pendente também entra")
	require.Zero(t, r.Credenciais)
	require.Equal(t, 1, r.CodigosApagados)

	var vivasDeA int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM mcp_tokens WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()`, a).Scan(&vivasDeA))
	require.Equal(t, 2, vivasDeA, "A não foi tocado")
}

func TestEmail_InexistenteAvisaSemFalhar(t *testing.T) {
	pool, _, _ := cenario(t)
	r, avisos := rodar(t, pool, opcoes{email: "nao@existe"})
	require.Empty(t, r.Pessoas)
	require.Contains(t, avisos, "nao@existe")
}

func bytesUpper(s string) string { return string(bytes.ToUpper([]byte(s))) }

// A revogação já commitou quando a auditoria é escrita: falhar nela não pode
// abortar o lote (as próximas pessoas ficariam com credencial viva), só virar
// aviso + exit 1 no fim, com o resumo ainda impresso.
func TestAplicar_FalhaDeAuditoriaNaoAbortaOLote(t *testing.T) {
	pool, a, _ := cenario(t)
	ctx := t.Context()
	// Segunda pessoa com credencial viva, para provar que o lote continua.
	d := mcptest.Usuario(t, ctx, pool, "admin", true)
	_, err := pool.Exec(ctx, `INSERT INTO mcp_tokens (user_id, tipo, token_hash, nome, expires_at)
		VALUES ($1, 'pessoal', 'h9', 'outro', NOW() + INTERVAL '1 day')`, d)
	require.NoError(t, err)

	var out, avisos bytes.Buffer
	o := opcoes{todos: true, aplicar: true, auditar: func(context.Context, uuid.UUID, string, int64) error {
		return errors.New("audit_log fora do ar")
	}}
	err = executar(ctx, pool, o, &out, &avisos)
	require.Error(t, err, "exit 1 no fim")

	var r struct {
		Aplicado        bool     `json:"aplicado"`
		Credenciais     int      `json:"credenciais"`
		AuditoriaFalhou []string `json:"auditoria_falhou"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &r), "o resumo sai mesmo assim: %s", out.String())
	require.True(t, r.Aplicado)
	require.Equal(t, 3, r.Credenciais, "as duas de A e a de D foram revogadas")
	require.ElementsMatch(t, []string{emailDe(t, pool, a), emailDe(t, pool, d)}, r.AuditoriaFalhou)
	require.Contains(t, avisos.String(), "revoga-mcp: aviso: auditoria falhou para "+emailDe(t, pool, a))

	var vivas int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM mcp_tokens WHERE revoked_at IS NULL AND expires_at > NOW()`).Scan(&vivas))
	require.Zero(t, vivas)
}
