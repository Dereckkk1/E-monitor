package catalog

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestClients_ListWebhookDeliveries(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Webhook "+uuid.NewString()[:6])
	for i, st := range []string{"delivered", "failed", "delivered"} {
		_, err := pool.Exec(ctx, `
			INSERT INTO webhook_deliveries (client_id, event_type, payload, status, attempt_count, next_attempt_at, created_at)
			VALUES ($1, 'detection.confirmed', '{}'::jsonb, $2, 1, now(), now() - make_interval(mins => $3))`,
			cli, st, i)
		require.NoError(t, err)
	}
	repo := NewClients(pool)

	todas, total, err := repo.ListWebhookDeliveries(ctx, cli, "", 2, 0)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, todas, 2)
	require.True(t, todas[0].CreatedAt.After(todas[1].CreatedAt), "mais nova primeiro")

	resto, _, err := repo.ListWebhookDeliveries(ctx, cli, "", 2, 2)
	require.NoError(t, err)
	require.Len(t, resto, 1)

	falhas, total, err := repo.ListWebhookDeliveries(ctx, cli, "failed", 50, 0)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, "failed", falhas[0].Status)
}

func TestClients_ListAPIKeys(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli := insSeedClient(t, ctx, pool, "Chaves "+uuid.NewString()[:6])
	// key_hash é CHAR(64) UNIQUE (migration 0007): hashes de 64 hex distintos.
	ativa := "abcdef01" + strings.Repeat("a", 56)
	revogada := "ffffffff" + strings.Repeat("b", 56)
	_, err := pool.Exec(ctx, `INSERT INTO api_keys (client_id, key_hash) VALUES ($1, $2), ($1, $3)`, cli, ativa, revogada)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE api_keys SET revoked_at = now() WHERE key_hash LIKE 'ffff%'`)
	require.NoError(t, err)

	got, err := NewClients(pool).ListAPIKeys(ctx, cli)
	require.NoError(t, err)
	require.Len(t, got, 1, "revogada fica de fora")
	require.Equal(t, "abcdef01...", got[0].KeyMasked, "só os 8 primeiros hex do HASH")
	require.Nil(t, got[0].LastUsedAt)
}
