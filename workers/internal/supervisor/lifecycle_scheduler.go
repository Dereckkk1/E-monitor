package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/metrics"
	"radiocheck/internal/observability"
)

// NATS subjects emitted when the LifecycleScheduler promotes campaigns
// between states. Subscribers (the supervisor itself) react to these
// events to start/stop workers.
//
// Defined here (not in package events) because they are scheduler-internal
// and the events package is already a hard dependency from many places —
// keeping this self-contained simplifies the rollout. Once the rest of the
// system listens to them, they can be promoted to package events.
const (
	SubjectCampaignActivated = "campaign.activated"
	SubjectCampaignEnded     = "campaign.ended"
)

// DefaultSchedulerInterval is the period between lifecycle scans.
//
// 60s matches §18.2.1 (R-A acknowledged: up to ~60s of "ar perdido" in the
// boundary minute is acceptable for PoC; production may shift to a finer
// resolution or a "T-5min" pre-arm heuristic).
const DefaultSchedulerInterval = 60 * time.Second

// LifecycleEventBus publishes campaign lifecycle events.
//
// The production implementation wraps a *nats.Conn (see NATSEventBus).
// In tests / when NATS is not available, a stub may be used.
type LifecycleEventBus interface {
	PublishActivated(campaignID uuid.UUID) error
	PublishEnded(campaignID uuid.UUID) error
}

// NATSEventBus is the production LifecycleEventBus backed by NATS.
type NATSEventBus struct {
	nc  *nats.Conn
	log *zap.Logger
}

// NewNATSEventBus returns a NATSEventBus. If nc is nil, a no-op (logging only)
// implementation is used.
func NewNATSEventBus(nc *nats.Conn, log *zap.Logger) LifecycleEventBus {
	if nc == nil {
		return &noopBus{log: log}
	}
	return &NATSEventBus{nc: nc, log: log}
}

type lifecyclePayload struct {
	CampaignID string    `json:"campaign_id"`
	At         time.Time `json:"at"`
}

func (b *NATSEventBus) publish(subject string, campaignID uuid.UUID) error {
	data, err := json.Marshal(lifecyclePayload{
		CampaignID: campaignID.String(),
		At:         time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	return b.nc.Publish(subject, data)
}

func (b *NATSEventBus) PublishActivated(id uuid.UUID) error {
	return b.publish(SubjectCampaignActivated, id)
}

func (b *NATSEventBus) PublishEnded(id uuid.UUID) error {
	return b.publish(SubjectCampaignEnded, id)
}

type noopBus struct {
	log *zap.Logger
}

func (b *noopBus) PublishActivated(id uuid.UUID) error {
	if b.log != nil {
		b.log.Info("lifecycle bus (noop): campaign.activated",
			zap.String("campaign_id", id.String()))
	}
	return nil
}

func (b *noopBus) PublishEnded(id uuid.UUID) error {
	if b.log != nil {
		b.log.Info("lifecycle bus (noop): campaign.ended",
			zap.String("campaign_id", id.String()))
	}
	return nil
}

// LifecycleAction describes what the supervisor must do after a transition.
// Hooked via OnActivated / OnEnded so the scheduler stays decoupled from the
// supervisor's worker map.
type LifecycleAction func(ctx context.Context, campaignID uuid.UUID)

// LifecycleScheduler periodically reconciles campaigns.status with the wall
// clock (§18.2.1):
//
//	programada → ativa     when start_date <= today (America/Sao_Paulo)
//	ativa     → concluida  when end_date < today
//
// Each transition emits a NATS event AND invokes the in-process callback so
// the supervisor can react synchronously without a NATS round-trip.
type LifecycleScheduler struct {
	db        *pgxpool.Pool
	campaigns *catalog.Campaigns
	bus       LifecycleEventBus
	log       *zap.Logger
	interval  time.Duration

	OnActivated LifecycleAction
	OnEnded     LifecycleAction
}

// NewLifecycleScheduler builds a scheduler with the default 60s interval.
func NewLifecycleScheduler(
	db *pgxpool.Pool,
	campaigns *catalog.Campaigns,
	bus LifecycleEventBus,
	log *zap.Logger,
) *LifecycleScheduler {
	return &LifecycleScheduler{
		db:        db,
		campaigns: campaigns,
		bus:       bus,
		log:       log,
		interval:  DefaultSchedulerInterval,
	}
}

// SetInterval overrides the scan interval (used by tests).
func (s *LifecycleScheduler) SetInterval(d time.Duration) {
	if d > 0 {
		s.interval = d
	}
}

// Run blocks until ctx is canceled, scanning for transitions every interval.
//
// The first scan runs immediately (no initial sleep) so a freshly started
// API process picks up overnight transitions without waiting.
func (s *LifecycleScheduler) Run(ctx context.Context) error {
	if s.campaigns == nil {
		return errors.New("lifecycle scheduler: campaigns repo is nil")
	}
	s.log.Info("lifecycle scheduler started",
		zap.Duration("interval", s.interval))

	// Initial pass.
	s.tick(ctx)
	// Refresh metrics gauge once at startup even if no transitions happened.
	s.refreshGauges(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("lifecycle scheduler stopped")
			return nil
		case <-ticker.C:
			s.tick(ctx)
			s.refreshGauges(ctx)
		}
	}
}

// tick runs both transitions in a single TX (see Campaigns.PromoteScheduledLifecycle)
// and dispatches the resulting events.
func (s *LifecycleScheduler) tick(ctx context.Context) {
	ctx, span := observability.Tracer().Start(ctx, "lifecycle.tick")
	defer span.End()

	scanCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	activated, ended, err := s.campaigns.PromoteScheduledLifecycle(scanCtx)
	if err != nil {
		s.log.Error("lifecycle scheduler: promote failed", zap.Error(err))
		return
	}

	span.SetAttributes(
		attribute.Int("activated_count", len(activated)),
		attribute.Int("ended_count", len(ended)),
	)

	for _, id := range activated {
		metrics.CampaignTransitions.WithLabelValues("programada", "ativa").Inc()
		s.log.Info("lifecycle: campaign activated",
			zap.String("campaign_id", id.String()))
		if err := s.bus.PublishActivated(id); err != nil {
			s.log.Warn("lifecycle: publish activated failed",
				zap.String("campaign_id", id.String()),
				zap.Error(err))
		}
		if s.OnActivated != nil {
			cbCtx, cbCancel := context.WithTimeout(context.Background(), 60*time.Second)
			s.OnActivated(cbCtx, id)
			cbCancel()
		}
	}

	for _, id := range ended {
		metrics.CampaignTransitions.WithLabelValues("ativa", "concluida").Inc()
		s.log.Info("lifecycle: campaign ended",
			zap.String("campaign_id", id.String()))
		if err := s.bus.PublishEnded(id); err != nil {
			s.log.Warn("lifecycle: publish ended failed",
				zap.String("campaign_id", id.String()),
				zap.Error(err))
		}
		if s.OnEnded != nil {
			cbCtx, cbCancel := context.WithTimeout(context.Background(), 60*time.Second)
			s.OnEnded(cbCtx, id)
			cbCancel()
		}
	}
}

// refreshGauges keeps campaigns_by_status up to date even when no transitions
// happen (gauge must reflect the current snapshot, not just deltas).
func (s *LifecycleScheduler) refreshGauges(ctx context.Context) {
	scanCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	counts, err := s.campaigns.CountByStatus(scanCtx)
	if err != nil {
		s.log.Warn("lifecycle: count by status failed", zap.Error(err))
		return
	}
	for _, st := range []string{"programada", "ativa", "concluida", "cancelada"} {
		metrics.CampaignsByStatus.WithLabelValues(st).Set(float64(counts[st]))
	}
}
