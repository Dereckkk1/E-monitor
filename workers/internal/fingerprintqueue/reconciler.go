// Package fingerprintqueue fecha o buraco que deixou materiais invisíveis por
// um mês (incidente 2026-06-12): a geração de fingerprint é disparada por uma
// mensagem NATS fire-and-forget (`fingerprint.generate`); se a mensagem se
// perde ou o serviço falha, o material fica preso em pending/generating/failed
// para sempre, sem alerta — e nunca entra no índice de matching.
//
// O Reconciler é a rede de segurança, no mesmo espírito do reconciler de
// workers do supervisor: a cada tick, procura materiais presos e re-publica o
// generate. Idempotente — o serviço Python regenera por DELETE+COPY, então um
// re-disparo a mais nunca corrompe, só custa CPU.
//
// Política de retry:
//   - pending/generating há mais de StuckAfter → re-publica sempre (caso
//     canônico: mensagem perdida; o retry resolve na primeira tentativa).
//   - failed → re-publica até MaxFailedRetries por processo (cap em memória;
//     reinício do processo zera — aceitável, o alerta FingerprintStuck cobre
//     o caso de falha permanente).
//
// Multi-réplica: advisory lock por tick, padrão da casa (calibration,
// campaignalerts).
package fingerprintqueue

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"radiocheck/internal/events"
	"radiocheck/internal/metrics"
)

var advisoryLockKey = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("radiocheck:fingerprint-queue-reconciler"))
	return int64(h.Sum64())
}()

// StuckMaterial é um material elegível a re-disparo.
type StuckMaterial struct {
	ID     uuid.UUID
	Status string
}

// Reconciler re-publica fingerprint.generate para materiais presos.
type Reconciler struct {
	pool *pgxpool.Pool
	nc   *nats.Conn
	log  *zap.Logger

	Interval         time.Duration // cadência do tick
	StuckAfter       time.Duration // pending/generating mais velhos que isso são presos
	MaxFailedRetries int           // cap de re-tentativas para status failed (por processo)

	mu          sync.Mutex
	failedTries map[uuid.UUID]int
}

// New constrói o reconciler com os defaults do plano (tick 5min, preso após
// 15min, 5 tentativas para failed).
func New(pool *pgxpool.Pool, nc *nats.Conn, log *zap.Logger) *Reconciler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Reconciler{
		pool:             pool,
		nc:               nc,
		log:              log,
		Interval:         5 * time.Minute,
		StuckAfter:       15 * time.Minute,
		MaxFailedRetries: 5,
		failedTries:      make(map[uuid.UUID]int),
	}
}

// Run bloqueia até ctx cancelar. Tick imediato na subida (um material preso
// não espera 5 min depois de um deploy).
func (r *Reconciler) Run(ctx context.Context) {
	r.log.Info("fingerprint-queue reconciler iniciado",
		zap.Duration("interval", r.Interval),
		zap.Duration("stuck_after", r.StuckAfter))
	r.tick(ctx)
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			r.log.Info("fingerprint-queue reconciler parado")
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

// shouldRetryFailed aplica o cap em memória de re-tentativas para failed.
func (r *Reconciler) shouldRetryFailed(id uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failedTries[id] >= r.MaxFailedRetries {
		return false
	}
	r.failedTries[id]++
	return true
}

// listStuck retorna materiais presos: pending/generating mais velhos que
// StuckAfter, e todos os failed (o cap de retry é aplicado pelo caller).
func (r *Reconciler) listStuck(ctx context.Context) ([]StuckMaterial, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, fingerprint_status
		FROM materials
		WHERE (fingerprint_status IN ('pending','generating') AND updated_at < now() - $1::interval)
		   OR fingerprint_status = 'failed'
		ORDER BY updated_at ASC`,
		r.StuckAfter.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StuckMaterial
	for rows.Next() {
		var s StuckMaterial
		if err := rows.Scan(&s.ID, &s.Status); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// mirrorReadyLegacyCommercials espelha commercials 'ready' que ainda não têm
// linha em materials → cria o material (mesmo UUID/short_id, type_id NULL) e o
// link campaign_materials da campanha original (preservando target_stations).
// É o "going forward" do backfill da migration 0039: uploads pela tela antiga
// /campaigns continuam criando só commercial e sendo detectados via path
// commercials; quando o fingerprint fica 'ready', este passo materializa o
// mirror pra ele aparecer na biblioteca do wizard (Bug 1) e ser reaproveitável.
//
// SÓ roda sobre 'ready' — espelhar um pending criaria um material que some dos
// dois paths do índice (ver migration 0039). Detection-neutral: o material
// nasce 'ready', e as hashes (chaveadas pelo UUID) já estão no índice via path
// commercials; após o mirror o MESMO UUID passa a carregar via
// materials/campaign_materials — mesmas hashes, sem janela de invisibilidade.
// Idempotente (ON CONFLICT DO NOTHING) → seguro entre réplicas sem lock.
func (r *Reconciler) mirrorReadyLegacyCommercials(ctx context.Context) {
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO materials (
		    id, short_id, client_id, title, type_id, duration_seconds,
		    master_storage_path, master_sha256,
		    fingerprint_status, fingerprint_generated_at, fingerprint_hash_count,
		    created_at, updated_at
		)
		SELECT c.id, c.short_id, cmp.client_id, c.title, NULL,
		       c.duration_seconds, c.master_storage_path, c.master_sha256,
		       c.fingerprint_status, c.fingerprint_generated_at, c.fingerprint_hash_count,
		       c.created_at, c.updated_at
		FROM commercials c
		JOIN campaigns cmp ON cmp.id = c.campaign_id
		WHERE c.fingerprint_status = 'ready'
		  AND NOT EXISTS (SELECT 1 FROM materials m WHERE m.id = c.id)
		ON CONFLICT (id) DO NOTHING`); err != nil {
		r.log.Warn("fingerprint-queue: mirror legacy commercials (materials) falhou", zap.Error(err))
		return
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations, added_at)
		SELECT c.campaign_id, c.id, c.target_stations, c.created_at
		FROM commercials c
		WHERE c.fingerprint_status = 'ready'
		  AND EXISTS (SELECT 1 FROM materials m WHERE m.id = c.id)
		ON CONFLICT (campaign_id, material_id) DO NOTHING`); err != nil {
		r.log.Warn("fingerprint-queue: mirror legacy commercials (links) falhou", zap.Error(err))
	}
}

func (r *Reconciler) tick(ctx context.Context) {
	// Espelha commercials legados 'ready' → materials (Bug 1, going forward).
	// Antes da lógica de stuck e sem gate: roda todo tick (após o primeiro, o
	// NOT EXISTS faz o INSERT achar 0 linhas — custo desprezível).
	r.mirrorReadyLegacyCommercials(ctx)

	stuck, err := r.listStuck(ctx)
	if err != nil {
		r.log.Warn("fingerprint-queue: listStuck falhou", zap.Error(err))
		return
	}

	// Gauge por status reflete o estado ANTES do retry — é o que o alerta
	// FingerprintStuck observa. Zera os três labels para não deixar valor
	// fantasma quando uma categoria esvazia.
	counts := map[string]int{"pending": 0, "generating": 0, "failed": 0}
	for _, s := range stuck {
		counts[s.Status]++
	}
	for status, n := range counts {
		metrics.FingerprintStuck.WithLabelValues(status).Set(float64(n))
	}
	if len(stuck) == 0 {
		return
	}

	// Serializa entre réplicas: só uma re-publica por tick.
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		r.log.Warn("fingerprint-queue: acquire conn falhou", zap.Error(err))
		return
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&locked); err != nil {
		r.log.Warn("fingerprint-queue: advisory lock falhou", zap.Error(err))
		return
	}
	if !locked {
		return
	}
	defer func() { _, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey) }()

	republished := 0
	for _, s := range stuck {
		if s.Status == "failed" && !r.shouldRetryFailed(s.ID) {
			continue
		}
		payload, _ := json.Marshal(map[string]string{"material_id": s.ID.String()})
		if err := r.nc.Publish(events.SubjectFingerprintGenerate, payload); err != nil {
			r.log.Warn("fingerprint-queue: publish falhou",
				zap.String("material_id", s.ID.String()), zap.Error(err))
			continue
		}
		metrics.FingerprintRetriesTotal.WithLabelValues(s.Status).Inc()
		republished++
		r.log.Info("fingerprint-queue: re-disparado",
			zap.String("material_id", s.ID.String()),
			zap.String("status", s.Status))
	}
	if err := r.nc.Flush(); err != nil {
		r.log.Warn("fingerprint-queue: flush falhou", zap.Error(err))
	}
	r.log.Info("fingerprint-queue: tick concluído",
		zap.Int("stuck", len(stuck)), zap.Int("republished", republished))
}
