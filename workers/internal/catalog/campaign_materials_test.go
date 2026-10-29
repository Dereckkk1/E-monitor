package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCampaignMaterials_LinkAndList(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "Test"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "Test Camp", ClientID: cli.ID,
		StartDate: time.Now(), EndDate: time.Now().AddDate(0, 1, 0),
	})
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "Spot", DurationSeconds: 30,
		MasterStoragePath: "/tmp/x.mp3", MasterSHA256: "abc",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM campaign_materials WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
	})

	repo := NewCampaignMaterials(pool)

	station := uuid.New()
	if err := repo.Link(ctx, cmp.ID, mat.ID, []uuid.UUID{station}); err != nil {
		t.Fatalf("link: %v", err)
	}

	links, err := repo.ListByCampaign(ctx, cmp.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(links) != 1 || links[0].MaterialID != mat.ID {
		t.Fatalf("links = %v, want one with material %s", links, mat.ID)
	}
	if len(links[0].TargetStations) != 1 || links[0].TargetStations[0] != station {
		t.Errorf("TargetStations = %v, want [%s]", links[0].TargetStations, station)
	}

	// Update stations
	newStation := uuid.New()
	if _, err := repo.UpdateStations(ctx, cmp.ID, mat.ID, []uuid.UUID{newStation}); err != nil {
		t.Fatalf("update stations: %v", err)
	}
	links, _ = repo.ListByCampaign(ctx, cmp.ID)
	if links[0].TargetStations[0] != newStation {
		t.Errorf("after update: stations = %v, want [%s]", links[0].TargetStations, newStation)
	}

	// Unlink
	if _, err := repo.Unlink(ctx, cmp.ID, mat.ID); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	links, _ = repo.ListByCampaign(ctx, cmp.ID)
	if len(links) != 0 {
		t.Errorf("after unlink: %d links, want 0", len(links))
	}
}

// UpdateStations e Unlink dizem quantas linhas tocaram: 0 = o vínculo não existe
// (a ação de escrita usa isso para não dizer "feito" quando nada foi feito).
func TestCampaignMaterials_UpdateStationsEUnlinkContamLinhas(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "Test rows"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "Rows Camp", ClientID: cli.ID, StartDate: time.Now(), EndDate: time.Now().AddDate(0, 1, 0),
	})
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "Spot rows", DurationSeconds: 30,
		MasterStoragePath: "/tmp/rows.mp3", MasterSHA256: "rows-" + uuid.NewString(),
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM campaign_materials WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
	})
	repo := NewCampaignMaterials(pool)

	n, err := repo.UpdateStations(ctx, cmp.ID, mat.ID, nil)
	if err != nil || n != 0 {
		t.Fatalf("sem vínculo: UpdateStations = (%d, %v), want (0, nil)", n, err)
	}
	n, err = repo.Unlink(ctx, cmp.ID, mat.ID)
	if err != nil || n != 0 {
		t.Fatalf("sem vínculo: Unlink = (%d, %v), want (0, nil)", n, err)
	}

	if err := repo.Link(ctx, cmp.ID, mat.ID, nil); err != nil {
		t.Fatal(err)
	}
	n, err = repo.UpdateStations(ctx, cmp.ID, mat.ID, []uuid.UUID{uuid.New()})
	if err != nil || n != 1 {
		t.Fatalf("com vínculo: UpdateStations = (%d, %v), want (1, nil)", n, err)
	}
	n, err = repo.Unlink(ctx, cmp.ID, mat.ID)
	if err != nil || n != 1 {
		t.Fatalf("com vínculo: Unlink = (%d, %v), want (1, nil)", n, err)
	}
}
