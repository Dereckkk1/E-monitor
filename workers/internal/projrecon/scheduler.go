// Package projrecon fecha a camada contínua do invariante de categoria por
// projeção (spec 2026-07-14): toda projeção em detection_campaigns deve ter
// category igual ao veredito do categorizador contra as regras/overrides
// VIVOS da campanha da projeção. Os produtores calculam certo na escrita e o
// recat cobre edições de regra — este reconciler cura (e DENUNCIA via métrica)
// qualquer caminho futuro que fure o invariante. Cura sem alerta esconderia o
// bug upstream: drift sustentado > 0 entre ciclos = investigar o produtor
// (runbook ProjectionDriftPersistent).
//
// Sem advisory lock (cf. calibration.Scheduler): HealProjectionDrift é
// idempotente — duas réplicas curando a mesma janela fazem o mesmo UPDATE; o
// único efeito colateral é dupla contagem aproximada nas métricas, aceitável.
package projrecon

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/metrics"
)

const (
	DefaultInterval = 15 * time.Minute
	DefaultLookback = 48 * time.Hour
)

// Reconciler é a fatia de catalog.DistributionRules que o scheduler usa.
type Reconciler interface {
	CountProjectionDrift(ctx context.Context, since time.Time) ([]catalog.ProjectionDrift, error)
	HealProjectionDrift(ctx context.Context, since time.Time) (int64, error)
}

type Scheduler struct {
	rec Reconciler
	log *zap.Logger

	Interval time.Duration
	Lookback time.Duration
	NowFn    func() time.Time
}

func New(rec Reconciler, log *zap.Logger) *Scheduler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Scheduler{
		rec:      rec,
		log:      log,
		Interval: DefaultInterval,
		Lookback: DefaultLookback,
		NowFn:    time.Now,
	}
}

// Run bloqueia até ctx cancelar. Tick imediato no boot (padrão
// calibration.Scheduler) — um deploy não espera 15 min pra primeira cura.
func (s *Scheduler) Run(ctx context.Context) error {
	if s.rec == nil {
		return errors.New("projrecon: reconciler is nil")
	}
	s.log.Info("projection reconciler started",
		zap.Duration("interval", s.Interval), zap.Duration("lookback", s.Lookback))

	s.tick(ctx)
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("projection reconciler stopped")
			return nil
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	if _, _, err := s.RunOnce(ctx); err != nil {
		s.log.Warn("projrecon: tick falhou", zap.Error(err))
	}
}

// RunOnce executa um ciclo: conta divergências na janela, cura se houver, e
// reporta métrica + log por transição. Devolve (encontradas, curadas).
func (s *Scheduler) RunOnce(ctx context.Context) (found, healed int64, err error) {
	since := s.NowFn().Add(-s.Lookback)
	drifts, err := s.rec.CountProjectionDrift(ctx, since)
	if err != nil {
		return 0, 0, err
	}
	for _, d := range drifts {
		found += d.N
	}
	metrics.ProjectionDriftLastRun.Set(float64(found))
	if found == 0 {
		return 0, 0, nil
	}

	healed, err = s.rec.HealProjectionDrift(ctx, since)
	if err != nil {
		return found, 0, err
	}
	for _, d := range drifts {
		metrics.ProjectionDriftHealed.WithLabelValues(d.From, d.To).Add(float64(d.N))
		s.log.Info("projrecon: divergência de categoria curada",
			zap.String("campaign_id", d.CampaignID.String()),
			zap.String("from", d.From), zap.String("to", d.To),
			zap.Int64("n", d.N))
	}
	return found, healed, nil
}
