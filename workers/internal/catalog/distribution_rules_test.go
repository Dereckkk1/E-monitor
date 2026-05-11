package catalog

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDistributionRules_CRUD(t *testing.T) {
	ctx, pool := newTestDB(t)

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "Test"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	})
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "x",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
	})
	station := uuid.New()

	repo := NewDistributionRules(pool)

	rule, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID:  cmp.ID,
		MaterialID:  mat.ID,
		StationIDs:  []uuid.UUID{station},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62,
		TimeStart:   "08:15",
		TimeEnd:     "10:45",
		PlaysPerDay: 3,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rule.PlaysPerDay != 3 {
		t.Errorf("PlaysPerDay = %d, want 3", rule.PlaysPerDay)
	}

	list, _ := repo.ListByCampaign(ctx, cmp.ID)
	if len(list) != 1 {
		t.Errorf("list len = %d, want 1", len(list))
	}

	// Update
	if err := repo.Update(ctx, rule.ID, CreateDistributionRuleInput{
		CampaignID: cmp.ID, MaterialID: mat.ID,
		StationIDs:  []uuid.UUID{station},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "08:15", TimeEnd: "10:45",
		PlaysPerDay: 5,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	r, err := repo.Get(ctx, rule.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if r.PlaysPerDay != 5 {
		t.Errorf("after update: PlaysPerDay = %d, want 5", r.PlaysPerDay)
	}

	// Delete
	if err := repo.Delete(ctx, rule.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ = repo.ListByCampaign(ctx, cmp.ID)
	if len(list) != 0 {
		t.Errorf("after delete: len = %d, want 0", len(list))
	}
}

func TestDistributionRules_Constraints(t *testing.T) {
	ctx, pool := newTestDB(t)
	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Now(), EndDate: time.Now().AddDate(0, 1, 0),
	})
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "y",
	})
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", mat.ID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", cmp.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
	})

	repo := NewDistributionRules(pool)

	// end_date < start_date deve falhar (rule_dates_valid)
	_, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, MaterialID: mat.ID,
		StationIDs:  []uuid.UUID{uuid.New()},
		StartDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "08:00", TimeEnd: "10:00",
		PlaysPerDay: 3,
	})
	if err == nil {
		t.Error("expected error for end < start, got nil")
	}
}
