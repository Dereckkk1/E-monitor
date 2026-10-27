package users_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/users"
)

// A revogação do MCP mora num trigger de users (migration 0070): trocar a senha,
// desativar, rebaixar de admin ou apagar o usuário corta toda credencial do MCP
// e todo código OAuth ainda não trocado (spec 2026-10-27 §4.1).

// adminComMCP cria um admin com 1 token pessoal vivo e 1 código OAuth pendente.
func adminComMCP(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repo *users.Repo, email string) uuid.UUID {
	t.Helper()
	u, err := repo.Create(ctx, users.CreateInput{Email: email, PasswordHash: "x", Role: "admin", Name: "MCP"})
	require.NoError(t, err)
	cid := "emc_" + uuid.NewString()[:8]
	_, err = pool.Exec(ctx, `INSERT INTO mcp_oauth_clients (client_id, nome, redirect_uris) VALUES ($1, 'Claude', ARRAY['https://x.test/cb'])`, cid)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO mcp_tokens (user_id, tipo, token_hash, nome, expires_at)
		VALUES ($1, 'pessoal', $2, 'n', NOW() + INTERVAL '1 day')`, u.ID, "h-"+uuid.NewString())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO mcp_oauth_codes (code_hash, client_id, user_id, redirect_uri, expires_at)
		VALUES ($1, $2, $3, 'https://x.test/cb', NOW() + INTERVAL '10 minutes')`, "c-"+uuid.NewString(), cid, u.ID)
	require.NoError(t, err)
	return u.ID
}

func contaMCP(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (vivos, codigos int) {
	t.Helper()
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_tokens WHERE user_id = $1 AND revoked_at IS NULL`, id).Scan(&vivos))
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_oauth_codes WHERE user_id = $1`, id).Scan(&codigos))
	return vivos, codigos
}

func novoMCPDB(t *testing.T) (context.Context, *pgxpool.Pool, *users.Repo) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	// resetUsersAndClients não alcança mcp_oauth_clients (sem FK de users/clients para ela).
	_, err := pool.Exec(ctx, `TRUNCATE mcp_oauth_clients CASCADE`)
	require.NoError(t, err)
	return ctx, pool, users.NewRepo(pool)
}

func TestUsuarioMudando_RevogaCredenciaisDoMCP(t *testing.T) {
	casos := []struct {
		nome string
		age  func(ctx context.Context, pool *pgxpool.Pool, repo *users.Repo, id uuid.UUID) error
	}{
		{"SetPassword", func(ctx context.Context, _ *pgxpool.Pool, repo *users.Repo, id uuid.UUID) error {
			return repo.SetPassword(ctx, id, "novo-hash")
		}},
		{"desativar", func(ctx context.Context, pool *pgxpool.Pool, _ *users.Repo, id uuid.UUID) error {
			_, err := pool.Exec(ctx, `UPDATE users SET is_active = false WHERE id = $1`, id)
			return err
		}},
		{"rebaixar de admin", func(ctx context.Context, pool *pgxpool.Pool, _ *users.Repo, id uuid.UUID) error {
			_, err := pool.Exec(ctx, `UPDATE users SET role = 'operator' WHERE id = $1`, id)
			return err
		}},
		{"SoftDelete", func(ctx context.Context, _ *pgxpool.Pool, repo *users.Repo, id uuid.UUID) error {
			return repo.SoftDelete(ctx, id)
		}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			ctx, pool, repo := novoMCPDB(t)
			alvo := adminComMCP(t, ctx, pool, repo, "alvo@teste.local")
			outro := adminComMCP(t, ctx, pool, repo, "outro@teste.local")

			require.NoError(t, c.age(ctx, pool, repo, alvo))

			vivos, codigos := contaMCP(t, ctx, pool, alvo)
			require.Zero(t, vivos, "tokens do alvo")
			require.Zero(t, codigos, "códigos do alvo")
			vivos, codigos = contaMCP(t, ctx, pool, outro)
			require.Equal(t, 1, vivos, "token do outro admin sobrevive")
			require.Equal(t, 1, codigos, "código do outro admin sobrevive")
		})
	}
}

func TestReativarNaoRessuscitaTokens(t *testing.T) {
	ctx, pool, repo := novoMCPDB(t)
	id := adminComMCP(t, ctx, pool, repo, "volta@teste.local")

	_, err := pool.Exec(ctx, `UPDATE users SET is_active = false WHERE id = $1`, id)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE users SET is_active = true WHERE id = $1`, id)
	require.NoError(t, err)

	vivos, _ := contaMCP(t, ctx, pool, id)
	require.Zero(t, vivos, "reativar a pessoa não traz o token antigo de volta")
}

func TestMudarSoONome_NaoRevogaNada(t *testing.T) {
	ctx, pool, repo := novoMCPDB(t)
	id := adminComMCP(t, ctx, pool, repo, "nome@teste.local")

	_, err := pool.Exec(ctx, `UPDATE users SET name = 'Outro Nome' WHERE id = $1`, id)
	require.NoError(t, err)

	vivos, codigos := contaMCP(t, ctx, pool, id)
	require.Equal(t, 1, vivos)
	require.Equal(t, 1, codigos)
}

func TestSetPassword_UsuarioInexistenteDevolveErrNoRows(t *testing.T) {
	ctx, _, repo := novoMCPDB(t)
	require.ErrorIs(t, repo.SetPassword(ctx, uuid.New(), "x"), pgx.ErrNoRows)
}
