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

func TestRender_StationsOffline(t *testing.T) {
	outages := []StationOutage{
		{StationName: "Jovem Pan Curitiba", Day: time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC), Down: 9*time.Hour + 32*time.Minute},
		{StationName: "Favorita FM", Day: time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC), Down: 2*time.Hour + 5*time.Minute},
	}
	c, err := RenderStationsOffline("Dereck", outages, "de 11/06", "https://e-monitor.online")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"Jovem Pan Curitiba", "Favorita FM", "9h32", "2h05", "11/06/2026",
		"https://e-monitor.online/operations", "de 11/06"} {
		if !strings.Contains(c.HTML, want) {
			t.Errorf("HTML não contém %q", want)
		}
	}
	for _, want := range []string{"Jovem Pan Curitiba", "9h32"} {
		if !strings.Contains(c.Text, want) {
			t.Errorf("Text não contém %q", want)
		}
	}
	if !strings.Contains(c.Subject, "2 emissoras") {
		t.Errorf("subject deveria contar 2 emissoras: %q", c.Subject)
	}
}

func TestRender_EmptyIsHandledByCaller(t *testing.T) {
	// Render nunca recebe lista vazia (o service pula). Mas se receber, não
	// deve panicar.
	if _, err := RenderStarting("X", nil, "https://e-monitor.online"); err != nil {
		t.Fatalf("render vazio: %v", err)
	}
}
