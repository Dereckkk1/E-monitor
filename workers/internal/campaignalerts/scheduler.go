package campaignalerts

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/calendar"
)

// advisoryLockKey serializa o disparo entre réplicas da API (mesmo padrão do
// calibration scheduler).
var advisoryLockKey = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("radiocheck:campaign-alerts-scheduler"))
	return int64(h.Sum64())
}()

// Scheduler dispara os emails diários. Roda só em dia útil, a partir de
// sendHour (BRT), uma vez por dia por tipo (dedup via notification_log).
type Scheduler struct {
	pool     *pgxpool.Pool
	svc      *Service
	sendHour int
	log      *zap.Logger

	Interval time.Duration // intervalo do ticker
	NowFn    func() time.Time
}

// NewScheduler constrói o scheduler. sendHour é a hora local de corte (ex.: 8).
func NewScheduler(pool *pgxpool.Pool, svc *Service, sendHour int, log *zap.Logger) *Scheduler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Scheduler{
		pool: pool, svc: svc, sendHour: sendHour, log: log,
		Interval: 5 * time.Minute,
		NowFn:    time.Now,
	}
}

// shouldRun decide se a janela de envio do dia está aberta: dia útil e hora
// local >= sendHour. O dedup diário é responsabilidade do notification_log.
func shouldRun(now time.Time, sendHour int) bool {
	local := now.In(calendar.BR)
	if !calendar.IsBusinessDay(local) {
		return false
	}
	return local.Hour() >= sendHour
}

// Run bloqueia até ctx cancelar.
func (s *Scheduler) Run(ctx context.Context) {
	s.log.Info("campaign-alerts scheduler iniciado",
		zap.Duration("interval", s.Interval), zap.Int("send_hour", s.sendHour))
	s.tick(ctx)
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("campaign-alerts scheduler parado")
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	now := s.NowFn()
	if !shouldRun(now, s.sendHour) {
		return
	}
	today := calendar.Today(now)

	// Antes de pegar o lock, pula tipos já enviados hoje (barato e evita
	// segurar conexão à toa).
	pending := make([]alertType, 0, 3)
	for _, a := range s.svc.types() {
		sent, err := s.svc.logs.AlreadySent(ctx, today, a.name)
		if err != nil {
			s.log.Warn("campaign-alerts: AlreadySent falhou", zap.String("type", a.name), zap.Error(err))
			continue
		}
		if !sent {
			pending = append(pending, a)
		}
	}
	if len(pending) == 0 {
		return
	}

	// Advisory lock de sessão: segura a conexão durante todo o processamento.
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		s.log.Warn("campaign-alerts: acquire conn falhou", zap.Error(err))
		return
	}
	defer conn.Release()

	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&locked); err != nil {
		s.log.Warn("campaign-alerts: advisory lock falhou", zap.Error(err))
		return
	}
	if !locked {
		s.log.Info("campaign-alerts: outra instância segura o lock; pulando tick")
		return
	}
	defer func() { _, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey) }()

	for _, a := range pending {
		// Re-checa dedup sob lock (outra instância pode ter enviado entre o
		// pré-filtro e a aquisição do lock).
		sent, err := s.svc.logs.AlreadySent(ctx, today, a.name)
		if err != nil {
			s.log.Warn("campaign-alerts: AlreadySent (sob lock) falhou", zap.String("type", a.name), zap.Error(err))
			continue
		}
		if sent {
			continue
		}
		at := a
		func() {
			defer func() {
				if r := recover(); r != nil {
					s.log.Error("campaign-alerts: panic no disparo",
						zap.String("type", at.name), zap.Any("recover", r))
				}
			}()
			s.svc.ProcessType(ctx, today, at)
		}()
	}
}
