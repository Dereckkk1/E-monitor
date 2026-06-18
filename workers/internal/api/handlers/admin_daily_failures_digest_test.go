package handlers

import (
	"testing"

	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

func TestDigestFromDaily(t *testing.T) {
	idA, idB := uuid.New(), uuid.New()
	res := &catalog.DailyResult{
		Mode: "by_date",
		Date: "2026-06-17",
		Summary: catalog.CampaignDailySummary{
			Campaigns: 2, Stations: 3, TotalDeficit: 9,
		},
		Campaigns: []catalog.CampaignDailyFailure{
			{
				Campaign: catalog.CampaignInfo{
					ID: idA, Name: "Verão 2026",
					ClientName: "Cliente A", ClientLogoURL: "logoA",
				},
				Stations: []catalog.CampaignFailureStation{{}, {}}, // 2 emissoras
			},
			{
				Campaign: catalog.CampaignInfo{
					ID: idB, Name: "Liquida Inverno",
					ClientName: "Cliente B", ClientLogoURL: "",
				},
				Stations: []catalog.CampaignFailureStation{{}}, // 1 emissora
			},
		},
	}

	out := digestFromDaily(res, true)

	if out.Date != "2026-06-17" {
		t.Errorf("Date = %q, want 2026-06-17", out.Date)
	}
	if !out.Seen {
		t.Error("Seen = false, want true")
	}
	if out.Summary.Campaigns != 2 || out.Summary.Stations != 3 {
		t.Errorf("Summary = %+v, want {Campaigns:2 Stations:3}", out.Summary)
	}
	if len(out.Campaigns) != 2 {
		t.Fatalf("len(Campaigns) = %d, want 2", len(out.Campaigns))
	}
	if out.Campaigns[0].ID != idA || out.Campaigns[0].Name != "Verão 2026" {
		t.Errorf("Campaigns[0] id/name wrong: %+v", out.Campaigns[0])
	}
	if out.Campaigns[0].ClientName != "Cliente A" || out.Campaigns[0].ClientLogoURL != "logoA" {
		t.Errorf("Campaigns[0] client wrong: %+v", out.Campaigns[0])
	}
	if out.Campaigns[0].StationsFailed != 2 {
		t.Errorf("Campaigns[0].StationsFailed = %d, want 2", out.Campaigns[0].StationsFailed)
	}
	if out.Campaigns[1].StationsFailed != 1 {
		t.Errorf("Campaigns[1].StationsFailed = %d, want 1", out.Campaigns[1].StationsFailed)
	}
}

func TestDigestFromDaily_Empty(t *testing.T) {
	res := &catalog.DailyResult{
		Mode: "by_date", Date: "2026-06-17",
		Summary:   catalog.CampaignDailySummary{},
		Campaigns: []catalog.CampaignDailyFailure{},
	}
	out := digestFromDaily(res, false)
	if out.Campaigns == nil {
		t.Error("Campaigns is nil, want non-nil empty slice (JSON [])")
	}
	if len(out.Campaigns) != 0 {
		t.Errorf("len(Campaigns) = %d, want 0", len(out.Campaigns))
	}
}
