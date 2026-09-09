package catalog

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// ⚠️ Os três primeiros PULAM sem TEST_DATABASE_URL. Um teste pulado sai com
// exit 0 e parece verde — conte os SKIP ao relatar o placar. O quarto roda
// sempre, de propósito: a guarda de string vazia é a que não pode depender de
// haver banco.
func hubClientsPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return ctx, pool
}

func TestLocalPorHubID_ResolveOTenant(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	hubID := "hub-" + uuid.NewString()
	var local uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name, hub_id) VALUES ($1, $2) RETURNING id`,
		"Cliente da fatia 2", hubID).Scan(&local))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, local) })

	got, err := NewHubClients(pool).LocalPorHubID(ctx, hubID)
	require.NoError(t, err)
	require.Equal(t, local, got)
}

func TestLocalPorHubID_ClienteNaoLigado(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	// Existe no E-monitor, mas NINGUÉM mapeou hub_id — é o cliente que o hub
	// ainda não conhece. Tem que ser indistinguível de "não existe".
	var local uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ($1) RETURNING id`, "Sem ponte").Scan(&local))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, local) })

	_, err := NewHubClients(pool).LocalPorHubID(ctx, local.String())
	require.ErrorIs(t, err, ErrHubClientNaoLigado)
}

func TestLocalPorHubID_HubIDInexistente(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	_, err := NewHubClients(pool).LocalPorHubID(ctx, "hub-"+uuid.NewString())
	require.ErrorIs(t, err, ErrHubClientNaoLigado)
}

func TestLocalPorHubID_VazioNaoConsultaOBanco(t *testing.T) {
	// Sem pool de propósito: string vazia tem que morrer ANTES da query. Um
	// `WHERE hub_id = ''` casaria com qualquer linha que tivesse hub_id vazio.
	_, err := NewHubClients(nil).LocalPorHubID(context.Background(), "")
	require.ErrorIs(t, err, ErrHubClientNaoLigado)
}
