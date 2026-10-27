package users_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/users"
)

// Trocar ou redefinir a senha corta toda credencial do MCP e todo código
// OAuth ainda não trocado (spec 2026-10-27 §4.1).
func TestSetPassword_RevogaCredenciaisDoMCP(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	// resetUsersAndClients não alcança mcp_oauth_clients (sem FK de users/clients para ela).
	_, err := pool.Exec(ctx, `TRUNCATE mcp_oauth_clients CASCADE`)
	require.NoError(t, err)
	repo := users.NewRepo(pool)
	u, err := repo.Create(ctx, users.CreateInput{Email: "mcp@teste.local", PasswordHash: "x", Role: "admin", Name: "MCP"})
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO mcp_oauth_clients (client_id, nome, redirect_uris) VALUES ('emc_s', 'Claude', ARRAY['https://x.test/cb'])`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO mcp_tokens (user_id, tipo, token_hash, nome, expires_at)
		VALUES ($1, 'pessoal', 'h1', 'n', NOW() + INTERVAL '1 day')`, u.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO mcp_oauth_codes (code_hash, client_id, user_id, redirect_uri, expires_at)
		VALUES ('c1', 'emc_s', $1, 'https://x.test/cb', NOW() + INTERVAL '10 minutes')`, u.ID)
	require.NoError(t, err)

	require.NoError(t, repo.SetPassword(ctx, u.ID, "novo-hash"))

	var vivos, codigos int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_tokens WHERE user_id = $1 AND revoked_at IS NULL`, u.ID).Scan(&vivos))
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_oauth_codes WHERE user_id = $1`, u.ID).Scan(&codigos))
	require.Zero(t, vivos)
	require.Zero(t, codigos)
}
