// Package mcptest prepara o banco para os testes de integração do MCP.
// Só é importado por arquivos _test.go.
//
// Todo pacote que usa DB() faz TRUNCATE nas mesmas tabelas do mesmo
// TEST_DATABASE_URL, então os testes têm de rodar com `go test -p 1`.
// O audit_log NÃO é truncado (não tem FK para users): testes que o leem
// limpam a tabela por conta própria.
package mcptest

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/db"
	"radiocheck/internal/dbtest"
)

// SegredoTeste faz as vezes do JWT_SECRET nos testes (≥ 32 bytes, como exige auth).
const SegredoTeste = "segredo-de-teste-do-mcp-com-mais-de-32-bytes"

// DB conecta no TEST_DATABASE_URL, passa pelo guard e zera as tabelas que o
// MCP toca. mcp_tokens e mcp_oauth_codes caem junto pelo CASCADE de users.
func DB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	dbtest.GuardOrSkip(t, ctx, pool)
	_, err = pool.Exec(ctx, `TRUNCATE users, clients, mcp_oauth_clients RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
	return ctx, pool
}

// Usuario cria um usuário interno com o papel e o estado pedidos.
// Use "admin" ou "operator" (viewer exige cliente vinculado).
func Usuario(t *testing.T, ctx context.Context, pool *pgxpool.Pool, papel string, ativo bool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	email := fmt.Sprintf("%s-%s@mcp.teste", papel, uuid.NewString()[:8])
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, name, is_active)
		 VALUES ($1, 'x', $2, 'Pessoa de Teste', $3) RETURNING id`,
		email, papel, ativo).Scan(&id))
	return id
}
