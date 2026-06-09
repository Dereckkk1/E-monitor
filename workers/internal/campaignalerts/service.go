package campaignalerts

import (
	"context"
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/mailer"
	"radiocheck/internal/metrics"
	"radiocheck/internal/users"
)

// recipientLister é satisfeito por *users.Repo.
type recipientLister interface {
	ActiveInternal(ctx context.Context) ([]users.User, error)
}

// Service orquestra a montagem e o envio dos 3 disparos.
type Service struct {
	repo    *Repo
	logs    *LogStore
	users   recipientLister
	mail    mailer.Mailer
	baseURL string
	log     *zap.Logger

	// sendRetries é o nº de tentativas por destinatário.
	sendRetries int
}

// NewService constrói o serviço de alertas.
func NewService(repo *Repo, logs *LogStore, usersRepo recipientLister, mail mailer.Mailer, baseURL string, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{repo: repo, logs: logs, users: usersRepo, mail: mail, baseURL: baseURL, log: log, sendRetries: 3}
}

// alertType encapsula a query e o render de um disparo.
type alertType struct {
	name   string // valor do CHECK em notification_log
	fetch  func(ctx context.Context, today time.Time) ([]CampaignAlert, error)
	render func(recipient string, campaigns []CampaignAlert, baseURL string) (EmailContent, error)
}

func (s *Service) types() []alertType {
	return []alertType{
		{"starting_no_material", s.repo.StartingNoMaterial, RenderStartingNoMaterial},
		{"starting", s.repo.Starting, RenderStarting},
		{"ending", s.repo.Ending, RenderEnding},
	}
}

// ProcessType executa um disparo para o dia `today`. Assume que o caller já
// verificou dedup/advisory lock. Grava o notification_log ao final.
func (s *Service) ProcessType(ctx context.Context, today time.Time, at alertType) {
	campaigns, err := at.fetch(ctx, today)
	if err != nil {
		s.log.Error("campaignalerts: fetch falhou", zap.String("type", at.name), zap.Error(err))
		_ = s.logs.Record(ctx, LogEntry{Date: today, Type: at.name, Status: "failed", Error: err.Error()})
		metrics.NotificationsFailedTotal.WithLabelValues(at.name).Inc()
		return
	}
	if len(campaigns) == 0 {
		s.log.Info("campaignalerts: nada a enviar", zap.String("type", at.name))
		_ = s.logs.Record(ctx, LogEntry{Date: today, Type: at.name, Status: "skipped_empty"})
		return
	}

	recipients, err := s.users.ActiveInternal(ctx)
	if err != nil {
		s.log.Error("campaignalerts: lista de destinatários falhou", zap.String("type", at.name), zap.Error(err))
		_ = s.logs.Record(ctx, LogEntry{Date: today, Type: at.name, CampaignCount: len(campaigns), Status: "failed", Error: err.Error()})
		metrics.NotificationsFailedTotal.WithLabelValues(at.name).Inc()
		return
	}

	sent, failed := 0, 0
	for _, r := range recipients {
		if r.Email == "" {
			continue
		}
		content, rerr := at.render(displayName(r), campaigns, s.baseURL)
		if rerr != nil {
			s.log.Error("campaignalerts: render falhou", zap.String("type", at.name), zap.Error(rerr))
			failed++
			continue
		}
		if s.sendWithRetry(ctx, r.Email, content) {
			sent++
		} else {
			failed++
		}
	}

	status := "sent"
	if failed > 0 && sent > 0 {
		status = "partial"
	} else if failed > 0 {
		status = "failed"
	}
	metrics.NotificationsSentTotal.WithLabelValues(at.name).Add(float64(sent))
	if failed > 0 {
		metrics.NotificationsFailedTotal.WithLabelValues(at.name).Add(float64(failed))
	}
	metrics.NotificationsRecipients.WithLabelValues(at.name).Set(float64(len(recipients)))
	s.log.Info("campaignalerts: disparo concluído",
		zap.String("type", at.name), zap.Int("campaigns", len(campaigns)),
		zap.Int("sent", sent), zap.Int("failed", failed), zap.String("status", status))
	_ = s.logs.Record(ctx, LogEntry{Date: today, Type: at.name, RecipientCount: sent, CampaignCount: len(campaigns), Status: status})
}

func (s *Service) sendWithRetry(ctx context.Context, email string, c EmailContent) bool {
	var last error
	for attempt := 1; attempt <= s.sendRetries; attempt++ {
		if err := s.mail.Send(ctx, []string{email}, c.Subject, c.HTML, c.Text); err == nil {
			return true
		} else {
			last = err
			select {
			case <-ctx.Done():
				return false
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
	}
	s.log.Warn("campaignalerts: envio falhou após retries", zap.String("to", email), zap.Error(last))
	return false
}

func displayName(u users.User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}
