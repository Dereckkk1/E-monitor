package catalog

import (
	"testing"
	"time"
)

func TestDistributionOverrides_Upsert(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Now(), EndDate: time.Now().AddDate(0, 1, 0),
	})
	typeID := seedType(t, ctx, pool, "Spot")
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "z",
	})
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Test FM", Band: "FM", StreamURL: "http://example.com",
	})
	if err != nil {
		t.Fatalf("create station: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM distribution_overrides WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", stat.ID)
	})

	repo := NewDistributionOverrides(pool)
	date := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	// Insert
	if err := repo.Upsert(ctx, UpsertOverrideInput{
		CampaignID: cmp.ID, TypeID: typeID, StationID: stat.ID,
		ForDate: date, PlaysExpected: 2,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Update via upsert
	if err := repo.Upsert(ctx, UpsertOverrideInput{
		CampaignID: cmp.ID, TypeID: typeID, StationID: stat.ID,
		ForDate: date, PlaysExpected: 5,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	list, err := repo.ListByCampaignAndDateRange(ctx, cmp.ID,
		date.AddDate(0, 0, -1), date.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
	if list[0].PlaysExpected != 5 {
		t.Errorf("PlaysExpected = %d, want 5", list[0].PlaysExpected)
	}
	if list[0].TypeID != typeID {
		t.Errorf("TypeID = %v, want %v", list[0].TypeID, typeID)
	}

	// Delete
	if err := repo.Delete(ctx, cmp.ID, typeID, stat.ID, date); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ = repo.ListByCampaignAndDateRange(ctx, cmp.ID,
		date.AddDate(0, 0, -1), date.AddDate(0, 0, 1))
	if len(list) != 0 {
		t.Errorf("after delete: %d, want 0", len(list))
	}
}
