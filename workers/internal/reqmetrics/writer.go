// Package reqmetrics captura uma linha por request HTTP que atravessa a API e
// grava em system_metrics de forma assíncrona e batched. Alimenta o painel
// /admin/monitoring (handlers.AdminMonitoringHandler).
//
// DESIGN: o caminho quente (handler HTTP → middleware) escreve em um canal
// bufferizado e retorna imediatamente. Uma goroutine consumidora flush-a em
// batches de até 200 rows ou a cada 2s — o que vier primeiro. Pico de tráfego
// que enche o canal cai para drop silencioso, evitando back-pressure no
// handler: prefiro perder telemetria a derrubar throughput.
//
// Retenção: prune diário corta tudo > 30d. Roda na mesma goroutine para
// simplificar shutdown.
package reqmetrics

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Sample é uma única observação capturada pelo middleware. Campos curtos para
// caber no canal sem alocar — IP e route como strings já interned pelo runtime
// (chi gera o pattern uma vez por handler).
type Sample struct {
	TS         time.Time
	Route      string
	Method     string
	StatusCode int
	DurationMs int
	IP         string
	UserID     *uuid.UUID
	UserEmail  string
	IsError    bool
	IsSlow     bool
}

// Writer é o coletor de samples. Não exporta o canal — chamadores usam Submit.
type Writer struct {
	pool *pgxpool.Pool
	log  *zap.Logger
	ch   chan Sample

	// dropped conta samples descartados quando o canal está cheio. Surfa via
	// log a cada minuto se > 0, para o operador saber que está perdendo dado
	// e considerar aumentar o buffer (ou diagnosticar lock no DB).
	dropped atomic.Uint64
}

// Config controla os tunables do writer. Zero values usam defaults sãos.
type Config struct {
	BufferSize int           // tamanho do canal; default 4096
	BatchSize  int           // rows por COPY; default 200
	FlushEvery time.Duration // flush forçado mesmo com batch < BatchSize; default 2s
	Retention  time.Duration // prune > Retention; default 30 dias
	PruneEvery time.Duration // intervalo do prune; default 6h
	SlowMs     int           // request > SlowMs marcado is_slow; default 2000
}

// NewWriter constrói o writer mas NÃO inicia a goroutine — chame Run.
func NewWriter(pool *pgxpool.Pool, log *zap.Logger, cfg Config) *Writer {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 4096
	}
	return &Writer{
		pool: pool,
		log:  log,
		ch:   make(chan Sample, cfg.BufferSize),
	}
}

// Submit enfileira um sample. Não bloqueia: drop silencioso se o canal estiver
// cheio. Retorna `false` quando dropou, para o caller decidir se quer logar.
func (w *Writer) Submit(s Sample) bool {
	if w == nil {
		return false
	}
	select {
	case w.ch <- s:
		return true
	default:
		w.dropped.Add(1)
		return false
	}
}

// Run consome o canal, faz batched inserts e prune. Sai quando ctx cancela.
// Deve ser chamado em uma goroutine pelo main.
func (w *Writer) Run(ctx context.Context, cfg Config) {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 200
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = 2 * time.Second
	}
	if cfg.Retention <= 0 {
		cfg.Retention = 30 * 24 * time.Hour
	}
	if cfg.PruneEvery <= 0 {
		cfg.PruneEvery = 6 * time.Hour
	}

	batch := make([]Sample, 0, cfg.BatchSize)
	flushTicker := time.NewTicker(cfg.FlushEvery)
	pruneTicker := time.NewTicker(cfg.PruneEvery)
	statTicker := time.NewTicker(time.Minute)
	defer flushTicker.Stop()
	defer pruneTicker.Stop()
	defer statTicker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := w.flushBatch(ctx, batch); err != nil {
			if w.log != nil {
				w.log.Warn("reqmetrics: flush failed",
					zap.Int("samples", len(batch)),
					zap.Error(err))
			}
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return

		case s := <-w.ch:
			batch = append(batch, s)
			if len(batch) >= cfg.BatchSize {
				flush()
			}

		case <-flushTicker.C:
			flush()

		case <-pruneTicker.C:
			w.prune(ctx, cfg.Retention)

		case <-statTicker.C:
			if n := w.dropped.Swap(0); n > 0 && w.log != nil {
				w.log.Warn("reqmetrics: dropped samples (buffer full)",
					zap.Uint64("dropped", n))
			}
		}
	}
}

// flushBatch insere as samples acumuladas via pgx.CopyFrom — mais eficiente
// que INSERT múltiplo quando o batch passa de ~50 rows.
func (w *Writer) flushBatch(ctx context.Context, batch []Sample) error {
	// Mesmo que o caller cancele, queremos uma janela para o último drain
	// antes do shutdown. Se ctx já está expirado, cai pro background com cap.
	parent := ctx
	if ctx.Err() != nil {
		parent = context.Background()
	}
	flushCtx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	rows := make([][]any, len(batch))
	for i, s := range batch {
		var uid any
		if s.UserID != nil {
			uid = *s.UserID
		}
		var email any
		if s.UserEmail != "" {
			email = s.UserEmail
		}
		rows[i] = []any{
			s.TS, s.Route, s.Method, s.StatusCode, s.DurationMs,
			s.IP, uid, email, s.IsError, s.IsSlow,
		}
	}

	_, err := w.pool.CopyFrom(flushCtx,
		pgx.Identifier{"system_metrics"},
		[]string{"ts", "route", "method", "status_code", "duration_ms",
			"ip", "user_id", "user_email", "is_error", "is_slow"},
		pgx.CopyFromRows(rows),
	)
	return err
}

// prune deleta linhas mais antigas que retention. Usa LIMIT subselect para
// evitar um único DELETE gigante segurar o lock por muito tempo: cada
// iteração remove até 50k linhas e o loop pára quando vazio. Em 30d a 2M
// rows/mês (≈70k/dia), o overflow real é raro — mas o cap defende contra
// rebote de retenção (operador muda 30→7 dias com mês acumulado).
func (w *Writer) prune(ctx context.Context, retention time.Duration) {
	pruneCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cutoff := time.Now().Add(-retention)
	for {
		ct, err := w.pool.Exec(pruneCtx, `
			DELETE FROM system_metrics
			WHERE id IN (
				SELECT id FROM system_metrics WHERE ts < $1 LIMIT 50000
			)
		`, cutoff)
		if err != nil {
			if w.log != nil {
				w.log.Warn("reqmetrics: prune failed", zap.Error(err))
			}
			return
		}
		if ct.RowsAffected() == 0 {
			break
		}
	}

	// Mesma estratégia para web_vitals — retenção compartilhada por
	// simplicidade. CLS scores são pequenos, mas LCP/INP histogram acumula.
	_, _ = w.pool.Exec(pruneCtx, `
		DELETE FROM web_vitals WHERE ts < $1
	`, cutoff)
}
