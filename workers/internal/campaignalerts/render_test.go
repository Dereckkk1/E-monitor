package campaignalerts

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func sampleAlerts() []CampaignAlert {
	return []CampaignAlert{
		{ID: uuid.New(), Name: "Promo Inverno", ClientName: "Tintas Renner",
			StartDate:    time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
			EndDate:      time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
			StationCount: 12, MaterialCount: 0},
	}
}

func TestRender_StartingNoMaterial(t *testing.T) {
	c, err := RenderStartingNoMaterial("Dereck", sampleAlerts(), "https://e-monitor.online")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if c.Subject == "" {
		t.Error("subject vazio")
	}
	for _, want := range []string{"Promo Inverno", "Tintas Renner", "15/06/2026", "Dereck",
		"https://e-monitor.online/campaigns"} {
		if !strings.Contains(c.HTML, want) {
			t.Errorf("HTML não contém %q", want)
		}
		if !strings.Contains(c.Text, want) && want != "https://e-monitor.online/campaigns" {
			t.Errorf("Text não contém %q", want)
		}
	}
	if !strings.Contains(c.HTML, "E-monitor%20logo.png") {
		t.Error("HTML não referencia o logo")
	}
}

func TestRender_EmptyIsHandledByCaller(t *testing.T) {
	// Render nunca recebe lista vazia (o service pula). Mas se receber, não
	// deve panicar.
	if _, err := RenderStarting("X", nil, "https://e-monitor.online"); err != nil {
		t.Fatalf("render vazio: %v", err)
	}
}
