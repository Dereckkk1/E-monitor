package campaignalerts

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/mailer"
)

// TestSendRealEmail manda os 3 emails de verdade via SMTP, sem tocar no banco.
// Valida a App Password, o remetente e o visual numa caixa real.
//
// Rodar (PowerShell):
//   $env:TEST_TO="voce@gmail.com"; $env:SMTP_USER="conta@dominio"; `
//   $env:SMTP_PASS="apppassword16"; $env:MAIL_FROM="E-monitor <conta@dominio>"; `
//   go test ./internal/campaignalerts -run SendRealEmail -v
//
// Sem TEST_TO o teste é pulado (não roda no CI).
func TestSendRealEmail(t *testing.T) {
	to := os.Getenv("TEST_TO")
	if to == "" {
		t.Skip("TEST_TO not set — pulando envio real")
	}
	port := 587
	if v := os.Getenv("SMTP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			port = p
		}
	}
	m := mailer.New(mailer.Config{
		Enabled: true,
		Host:    envOr("SMTP_HOST", "smtp.gmail.com"),
		Port:    port,
		User:    os.Getenv("SMTP_USER"),
		Pass:    os.Getenv("SMTP_PASS"),
		From:    os.Getenv("MAIL_FROM"),
	}, zap.NewNop())

	if _, ok := m.(interface {
		Send(context.Context, []string, string, string, string) error
	}); !ok {
		t.Fatal("mailer inválido")
	}

	base := envOr("NOTIFICATIONS_BASE_URL", "https://e-monitor.online")
	ps := func(s string) time.Time { tm, _ := time.Parse("2006-01-02", s); return tm }
	mk := func(name, client, start, end string, st int) CampaignAlert {
		return CampaignAlert{ID: uuid.New(), Name: name, ClientName: client,
			StartDate: ps(start), EndDate: ps(end), StationCount: st}
	}
	sample := []CampaignAlert{
		mk("Promo Inverno 2026", "Tintas Renner", "2026-06-11", "2026-06-30", 12),
		mk("Liquida Junina", "Supermercados Condor", "2026-06-11", "2026-07-05", 8),
		mk("Campanha Institucional Q3", "Unimed Curitiba", "2026-06-12", "2026-09-12", 24),
	}

	type job struct {
		name   string
		render func(string, []CampaignAlert, string) (EmailContent, error)
	}
	jobs := []job{
		{"starting_no_material", RenderStartingNoMaterial},
		{"starting", RenderStarting},
		{"ending", RenderEnding},
	}
	ctx := context.Background()
	for _, j := range jobs {
		c, err := j.render("Dereck", sample, base)
		if err != nil {
			t.Fatalf("render %s: %v", j.name, err)
		}
		if err := m.Send(ctx, []string{to}, "[TESTE] "+c.Subject, c.HTML, c.Text); err != nil {
			t.Fatalf("envio %s falhou: %v", j.name, err)
		}
		t.Logf("enviado %s → %s", j.name, to)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
