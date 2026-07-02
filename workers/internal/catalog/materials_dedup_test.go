package catalog

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// TestMaterials_GetByClientAndSHA: acha o material do cliente pelo master_sha256
// (o dedup que impede subir o mesmo áudio como 2º material — raiz do double-count
// UNIUBE 130/138). Escopado ao cliente: sha de outro cliente NÃO retorna.
func TestMaterials_GetByClientAndSHA(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewMaterials(pool)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "sha-dedup-cli"})
	require.NoError(t, err)
	other, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "sha-dedup-other"})
	require.NoError(t, err)
	sha := "dedup-sha-" + uuid.NewString()

	mat, err := repo.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "A", DurationSeconds: 30,
		MasterStoragePath: "/tmp/a", MasterSHA256: sha,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM materials WHERE id=$1", mat.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id IN ($1,$2)", cli.ID, other.ID)
	})

	// mesmo cliente + mesmo sha → acha
	got, err := repo.GetByClientAndSHA(ctx, cli.ID, sha)
	require.NoError(t, err)
	require.Equal(t, mat.ID, got.ID)

	// sha inexistente → ErrNoRows
	_, err = repo.GetByClientAndSHA(ctx, cli.ID, "no-such-sha")
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// mesmo sha, OUTRO cliente → ErrNoRows (escopado ao cliente)
	_, err = repo.GetByClientAndSHA(ctx, other.ID, sha)
	require.ErrorIs(t, err, pgx.ErrNoRows, "sha de outro cliente não deve retornar")
}
