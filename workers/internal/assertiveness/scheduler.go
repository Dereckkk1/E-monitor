package assertiveness

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/calendar"
)

// advisoryLockKey serializa o recompute entre réplicas da API (mesmo padrão do
// campaignalerts e do calibration scheduler). Duas réplicas recomputando a
// mesma janela ao mesmo tempo não corrompem nada — o DELETE+INSERT é
// transacional — mas é trabalho jogado fora.
var advisoryLockKey = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("radiocheck:assertiveness-scheduler"))
	return int64(h.Sum64())
}()

// Scheduler recomputa a tabela assertiveness_daily periodicamente.
//
// Não há gating por hora nem por dia útil (ao contrário do campaignalerts):
// o recompute é idempotente e não manda email pra ninguém, então rodar mais
// vezes só deixa o número mais fresco.
type Scheduler struct {
	repo *Repo
	pool *pgxpool.Pool
	log  *zap.Logger

	Interval time.Duration
	NowFn    func() time.Time
}

func NewScheduler(pool *pgxpool.Pool, repo *Repo, log *zap.Logger) *Scheduler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Scheduler{
		repo: repo, pool: pool, log: log,
		Interval: 6 * time.Hour,
		NowFn:    time.Now,
	}
}

// Window devolve a janela recomputada a cada execução: do 1º dia de dois meses
// atrás até hoje.
//
// Por que tão larga: o card mostra o mês fechado anterior e compara com o
// anterior a ele, então AMBOS precisam estar completos e atualizados o mês
// inteiro. Uma janela de "últimos N dias" quebra na virada — no dia 31 de
// agosto, 45 dias atrás é 17 de julho, e julho ficaria pela metade.
func Window(now time.Time) (from, to time.Time) {
	local := now.In(calendar.BR)
	firstOfThis := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, calendar.BR)
	from = firstOfThis.AddDate(0, -2, 0)
	to = calendar.Today(now)
	return from, to
}

// Run bloqueia até ctx cancelar. Recomputa na subida e a cada Interval.
func (s *Scheduler) Run(ctx context.Context) {
	s.log.Info("assertiveness scheduler iniciado", zap.Duration("interval", s.Interval))
	s.tick(ctx)
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("assertiveness scheduler parado")
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		s.log.Warn("assertiveness: acquire falhou", zap.Error(err))
		return
	}
	defer conn.Release()

	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, advisoryLockKey).Scan(&got); err != nil {
		s.log.Warn("assertiveness: advisory lock falhou", zap.Error(err))
		return
	}
	if !got {
		return // outra réplica está recomputando
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, advisoryLockKey)
	}()

	from, to := Window(s.NowFn())
	start := time.Now()
	n, err := s.repo.Recompute(ctx, from, to)
	if err != nil {
		s.log.Error("assertiveness: recompute falhou",
			zap.Time("from", from), zap.Time("to", to), zap.Error(err))
		return
	}
	s.log.Info("assertiveness recomputada",
		zap.String("from", from.Format("2006-01-02")),
		zap.String("to", to.Format("2006-01-02")),
		zap.Int64("rows", n),
		zap.Duration("took", time.Since(start)))
}
