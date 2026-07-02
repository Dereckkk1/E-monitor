package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestMaterials_ListReadyIDsByCampaign: retorna só os materiais 'ready' linkados
// à campanha (não os pending nem os de outra campanha). É a lista que o
// Supervisor.Reload usa pra publicar index.reload (audit E3).
func TestMaterials_ListReadyIDsByCampaign(t *testing.T) {
	ctx, pool := newTestDB(t)
	mats := NewMaterials(pool)
	links := NewCampaignMaterials(pool)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "mat-ready-cli"})
	require.NoError(t, err)
	cmp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "mat-ready", ClientID: cli.ID,
		StartDate: time.Now().AddDate(0, 0, -1), EndDate: time.Now().AddDate(0, 0, 1),
	})
	require.NoError(t, err)
	station := uuid.New()

	readyMat, err := mats.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "ready-one", DurationSeconds: 30,
		MasterStoragePath: "/tmp/ry", MasterSHA256: "mat-ready-" + uuid.NewString(),
	})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE materials SET fingerprint_status='ready' WHERE id=$1`, readyMat.ID)
	require.NoError(t, err)

	pendingMat, err := mats.Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "pending-one", DurationSeconds: 30,
		MasterStoragePath: "/tmp/pd", MasterSHA256: "mat-pending-" + uuid.NewString(),
	})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE materials SET fingerprint_status='pending' WHERE id=$1`, pendingMat.ID)
	require.NoError(t, err)

	require.NoError(t, links.Link(ctx, cmp.ID, readyMat.ID, []uuid.UUID{station}))
	require.NoError(t, links.Link(ctx, cmp.ID, pendingMat.ID, []uuid.UUID{station}))

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaign_materials WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id IN ($1,$2)`, readyMat.ID, pendingMat.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
	})

	ids, err := mats.ListReadyIDsByCampaign(ctx, cmp.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{readyMat.ID}, ids,
		"só o material 'ready' linkado deve retornar (pending fica de fora)")
}
