package evidence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/metrics"
	"radiocheck/internal/storage"
)

// covETierWorld: cliente/campanha/emissora + S3 falso pro job de tiering/prune.
type covETierWorld struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	s3      *covES3
	client  uuid.UUID
	camp    uuid.UUID
	station uuid.UUID
	mat     uuid.UUID
	now     time.Time
}

func covENewTierWorld(t *testing.T, tls bool) *covETierWorld {
	t.Helper()
	ctx, pool := covEDB(t)
	w := &covETierWorld{ctx: ctx, pool: pool, s3: covENewS3(t, tls), now: time.Date(2027, 10, 1, 12, 0, 0, 0, time.UTC)}
	w.client = covESeedClient(t, ctx, pool)
	w.camp = covESeedCampaign(t, ctx, pool, w.client)
	w.station = covESeedStation(t, ctx, pool)
	w.mat, _ = covESeedMaterial(t, ctx, pool, w.client, "Spot", 30, "", nil)
	return w
}

// ago devolve w.now menos d.
func (w *covETierWorld) ago(d time.Duration) time.Time { return w.now.Add(-d) }

const covEDay = 24 * time.Hour

// seed insere uma detecção com clipe: objeto no bucket do S3 falso + linha no banco.
// Cada detecção usa um minuto distinto (a tocada física é única por instante).
func (w *covETierWorld) seed(t *testing.T, bucket string, age time.Duration, size int64, o covEDetOpts) (uuid.UUID, string) {
	t.Helper()
	w.mat2(t)
	key := "evidences/" + uuid.NewString() + ".m4a"
	if o.Key == "" {
		o.Key = key
	}
	if o.Size == 0 {
		o.Size = size
	}
	body := make([]byte, size)
	w.s3.Put(bucket, o.Key, string(body))
	at := w.ago(age)
	ca := at
	o.CreatedAt = &ca
	id := covESeedDet(t, w.ctx, w.pool, w.camp, w.mat, w.station, at, o)
	return id, o.Key
}

func (w *covETierWorld) mat2(t *testing.T) {}

func (w *covETierWorld) job(t *testing.T, hot, cold, archive *storage.Client) *TieringJob {
	t.Helper()
	j := NewTieringJob(w.pool, hot, cold, archive, zap.NewNop())
	j.Now = func() time.Time { return w.now }
	j.Detections = catalog.NewDetections(w.pool)
	return j
}

func (w *covETierWorld) status(t *testing.T, id uuid.UUID) (string, *string, *int64) {
	t.Helper()
	d := covEReadDet(t, w.ctx, w.pool, id)
	return d.Status, d.Key, d.Size
}

// ───────────────────── pruneExpired: retenção local ─────────────────────

func TestCovE_PruneExpired_DisabledByConfig(t *testing.T) {
	w := covENewTierWorld(t, false)
	hot := w.s3.Client(t, "hot")
	id, key := w.seed(t, "hot", 90*covEDay, 100, covEDetOpts{})

	cases := []struct {
		name string
		mut  func(j *TieringJob)
	}{
		{"retenção zero", func(j *TieringJob) { j.RetentionMaxAge = 0 }},
		{"retenção negativa", func(j *TieringJob) { j.RetentionMaxAge = -time.Hour }},
		{"sem bucket hot", func(j *TieringJob) { j.RetentionMaxAge = 30 * covEDay; j.Hot = nil }},
		{"sem repo de detecções", func(j *TieringJob) { j.RetentionMaxAge = 30 * covEDay; j.Detections = nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := w.job(t, hot, nil, nil)
			c.mut(j)
			p, r, e := j.pruneExpired(w.ctx)
			assert.Zero(t, p)
			assert.Zero(t, r)
			assert.Zero(t, e)
		})
	}
	assert.True(t, w.s3.Has("hot", key), "nada foi apagado")
	st, _, _ := w.status(t, id)
	assert.Equal(t, "available", st)
}

func TestCovE_PruneExpired_DryRunOnlyAccountsTheFullBacklog(t *testing.T) {
	w := covENewTierWorld(t, false)
	hot := w.s3.Client(t, "hot")
	old1, k1 := w.seed(t, "hot", 40*covEDay, 100, covEDetOpts{})
	old2, k2 := w.seed(t, "hot", 50*covEDay, 200, covEDetOpts{})
	old3, k3 := w.seed(t, "hot", 60*covEDay, 300, covEDetOpts{})
	w.seed(t, "hot", 5*covEDay, 999, covEDetOpts{}) // recente: fica

	j := w.job(t, hot, nil, nil)
	j.RetentionMaxAge = 30 * covEDay
	j.PruneDryRun = true
	j.BatchSize = 1 // dry-run ignora o tamanho do lote: é uma passada única sem LIMIT

	p, r, e := j.pruneExpired(w.ctx)

	assert.Equal(t, 3, p)
	assert.Equal(t, int64(600), r, "bytes que SERIAM recuperados")
	assert.Zero(t, e)
	for _, id := range []uuid.UUID{old1, old2, old3} {
		st, key, _ := w.status(t, id)
		assert.Equal(t, "available", st, "dry-run não muda linha")
		require.NotNil(t, key)
	}
	for _, k := range []string{k1, k2, k3} {
		assert.True(t, w.s3.Has("hot", k), "dry-run não apaga objeto")
	}
	assert.Empty(t, w.s3.deletes)
}

func TestCovE_PruneExpired_RealPruneDeletesObjectsAndExpiresRows(t *testing.T) {
	w := covENewTierWorld(t, false)
	hot := w.s3.Client(t, "hot")
	user := covESeedUser(t, w.ctx, w.pool)
	batch := covESeedProofBatch(t, w.ctx, w.pool, w.camp, w.station)

	old1, k1 := w.seed(t, "hot", 40*covEDay, 100, covEDetOpts{})
	old2, k2 := w.seed(t, "hot", 70*covEDay, 250, covEDetOpts{})
	recent, kRecent := w.seed(t, "hot", 10*covEDay, 400, covEDetOpts{})
	manual, kManual := w.seed(t, "hot", 90*covEDay, 500, covEDetOpts{Manual: true, ManualBy: &user})
	proof, kProof := w.seed(t, "hot", 91*covEDay, 600, covEDetOpts{ProofBatch: &batch})
	failed, _ := w.seed(t, "hot", 92*covEDay, 700, covEDetOpts{Status: "failed"})
	prunedTotal := covEMetric(t, metrics.EvidencePrunedTotal)
	prunedBytes := covEMetric(t, metrics.EvidencePrunedBytes)

	j := w.job(t, hot, nil, nil)
	j.RetentionMaxAge = 30 * covEDay
	j.BatchSize = 100

	p, r, e := j.pruneExpired(w.ctx)

	assert.Equal(t, 2, p)
	assert.Equal(t, int64(350), r)
	assert.Zero(t, e)
	for _, c := range []struct {
		id  uuid.UUID
		key string
	}{{old1, k1}, {old2, k2}} {
		st, key, size := w.status(t, c.id)
		assert.Equal(t, "expired", st)
		assert.True(t, key == nil || *key == "", "evidence_key limpa")
		require.NotNil(t, size)
		assert.Zero(t, *size)
		assert.False(t, w.s3.Has("hot", c.key), "objeto apagado do bucket")
	}
	// Isentos: recente, censura manual, comprovante em lote e status != available.
	for name, c := range map[string]struct {
		id  uuid.UUID
		key string
	}{"recente": {recent, kRecent}, "manual": {manual, kManual}, "comprovante": {proof, kProof}} {
		st, _, _ := w.status(t, c.id)
		assert.Equal(t, "available", st, "%s não expira", name)
		assert.True(t, w.s3.Has("hot", c.key), "%s mantém o objeto", name)
	}
	st, _, _ := w.status(t, failed)
	assert.Equal(t, "failed", st)
	assert.InDelta(t, prunedTotal+2, covEMetric(t, metrics.EvidencePrunedTotal), 0.001)
	assert.InDelta(t, prunedBytes+350, covEMetric(t, metrics.EvidencePrunedBytes), 0.001)

	// Idempotente: a segunda passada não acha nada.
	p, r, e = j.pruneExpired(w.ctx)
	assert.Zero(t, p+int(r)+e)
}

func TestCovE_PruneExpired_DrainsInBatches(t *testing.T) {
	w := covENewTierWorld(t, false)
	hot := w.s3.Client(t, "hot")
	var ids []uuid.UUID
	for i := 0; i < 5; i++ {
		id, _ := w.seed(t, "hot", time.Duration(40+i)*covEDay, 10, covEDetOpts{})
		ids = append(ids, id)
	}
	j := w.job(t, hot, nil, nil)
	j.RetentionMaxAge = 30 * covEDay
	j.BatchSize = 2 // 2 + 2 + 1

	p, r, e := j.pruneExpired(w.ctx)

	assert.Equal(t, 5, p, "o laço repete até a fila esvaziar")
	assert.Equal(t, int64(50), r)
	assert.Zero(t, e)
	for _, id := range ids {
		st, _, _ := w.status(t, id)
		assert.Equal(t, "expired", st)
	}
}

func TestCovE_PruneExpired_DeleteFailuresAreCountedAndRetried(t *testing.T) {
	t.Run("uma falha entre sucessos", func(t *testing.T) {
		w := covENewTierWorld(t, false)
		hot := w.s3.Client(t, "hot")
		ok1, _ := w.seed(t, "hot", 40*covEDay, 100, covEDetOpts{})
		bad, badKey := w.seed(t, "hot", 41*covEDay, 100, covEDetOpts{})
		ok2, _ := w.seed(t, "hot", 42*covEDay, 100, covEDetOpts{})
		w.s3.FailDelete("hot", badKey)
		j := w.job(t, hot, nil, nil)
		j.RetentionMaxAge = 30 * covEDay
		j.BatchSize = 100

		p, _, e := j.pruneExpired(w.ctx)

		assert.Equal(t, 2, p)
		assert.Equal(t, 1, e)
		st, key, _ := w.status(t, bad)
		assert.Equal(t, "available", st, "falha no delete deixa a linha intacta pro próximo ciclo")
		require.NotNil(t, key)
		assert.True(t, w.s3.Has("hot", badKey))
		for _, id := range []uuid.UUID{ok1, ok2} {
			st, _, _ := w.status(t, id)
			assert.Equal(t, "expired", st)
		}
	})

	t.Run("lote inteiro falhando não vira laço infinito", func(t *testing.T) {
		w := covENewTierWorld(t, false)
		hot := w.s3.Client(t, "hot")
		for i := 0; i < 3; i++ {
			_, k := w.seed(t, "hot", time.Duration(40+i)*covEDay, 10, covEDetOpts{})
			w.s3.FailDelete("hot", k)
		}
		j := w.job(t, hot, nil, nil)
		j.RetentionMaxAge = 30 * covEDay
		j.BatchSize = 2

		done := make(chan struct{})
		var p, e int
		go func() {
			p, _, e = j.pruneExpired(w.ctx)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("pruneExpired ficou preso reprocessando o mesmo lote")
		}
		assert.Zero(t, p)
		assert.Equal(t, 2, e, "um lote de 2 falhas e para")
	})
}

func TestCovE_PruneExpired_DBFailuresAndCancellation(t *testing.T) {
	w := covENewTierWorld(t, false)
	hot := w.s3.Client(t, "hot")
	closed := covEClosedPool(t)

	t.Run("dry-run: consulta de candidatos falha", func(t *testing.T) {
		j := NewTieringJob(closed, hot, nil, nil, zap.NewNop())
		j.Detections = catalog.NewDetections(closed)
		j.RetentionMaxAge = 30 * covEDay
		j.PruneDryRun = true
		p, r, e := j.pruneExpired(w.ctx)
		assert.Zero(t, p+int(r))
		assert.Equal(t, 1, e)
	})

	t.Run("real: consulta de candidatos falha", func(t *testing.T) {
		j := NewTieringJob(closed, hot, nil, nil, zap.NewNop())
		j.Detections = catalog.NewDetections(closed)
		j.RetentionMaxAge = 30 * covEDay
		p, _, e := j.pruneExpired(w.ctx)
		assert.Zero(t, p)
		assert.Equal(t, 1, e)
	})

	t.Run("contexto cancelado interrompe sem apagar nada", func(t *testing.T) {
		id, key := w.seed(t, "hot", 40*covEDay, 100, covEDetOpts{})
		j := w.job(t, hot, nil, nil)
		j.RetentionMaxAge = 30 * covEDay
		ctx, cancel := context.WithCancel(w.ctx)
		cancel()
		p, r, e := j.pruneExpired(ctx)
		assert.Zero(t, p+int(r)+e)
		st, _, _ := w.status(t, id)
		assert.Equal(t, "available", st)
		assert.True(t, w.s3.Has("hot", key))
	})
}

func TestCovE_RunPrune_StopsWhenContextIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	del := func(context.Context, string) error { called = true; return nil }
	exp := func(context.Context, uuid.UUID, time.Time) error { called = true; return nil }
	p, r, e := runPrune(ctx, []pruneCandidate{cand("a", 1), cand("b", 2)}, false, del, exp, zap.NewNop())
	assert.Zero(t, p+int(r)+e)
	assert.False(t, called, "contexto cancelado: nenhum candidato é processado")
}

// ───────────────────── Run: passada completa ─────────────────────

func TestCovE_TieringRun_PrunesAndUpdatesMetrics(t *testing.T) {
	w := covENewTierWorld(t, false)
	hot := w.s3.Client(t, "hot")
	old, oldKey := w.seed(t, "hot", 40*covEDay, 111, covEDetOpts{})
	_, keepKey := w.seed(t, "hot", 2*covEDay, 222, covEDetOpts{})

	j := w.job(t, hot, hot, hot) // mesmo bucket: o "move" é pulado, só a retenção age
	j.RetentionMaxAge = 30 * covEDay

	require.NoError(t, j.Run(w.ctx))

	st, _, _ := w.status(t, old)
	assert.Equal(t, "expired", st)
	assert.False(t, w.s3.Has("hot", oldKey))
	assert.True(t, w.s3.Has("hot", keepKey))
	assert.InDelta(t, float64(w.now.Unix()), covEMetric(t, metrics.EvidenceTieringLastRun), 0.5)

	// Gauge de armazenamento por tier = soma do que sobrou com chave.
	var hotBytes int64
	require.NoError(t, w.pool.QueryRow(w.ctx,
		`SELECT COALESCE(SUM(evidence_size_bytes),0) FROM detections WHERE evidence_key IS NOT NULL AND evidence_key <> '' AND tier='hot'`).Scan(&hotBytes))
	assert.GreaterOrEqual(t, hotBytes, int64(222))
}

func TestCovE_TieringRun_ReportsErrorsFromThePass(t *testing.T) {
	w := covENewTierWorld(t, false)
	hot := w.s3.Client(t, "hot")
	_, key := w.seed(t, "hot", 40*covEDay, 100, covEDetOpts{})
	w.s3.FailDelete("hot", key)
	j := w.job(t, hot, nil, nil)
	j.RetentionMaxAge = 30 * covEDay

	err := j.Run(w.ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tiering: 1 errors in pass")
}

func TestCovE_TieringRun_DryRunNeverFailsAndNeverDeletes(t *testing.T) {
	w := covENewTierWorld(t, false)
	hot := w.s3.Client(t, "hot")
	id, key := w.seed(t, "hot", 40*covEDay, 100, covEDetOpts{})
	j := w.job(t, hot, hot, hot)
	j.RetentionMaxAge = 30 * covEDay
	j.PruneDryRun = true
	require.NoError(t, j.Run(w.ctx))
	st, _, _ := w.status(t, id)
	assert.Equal(t, "available", st)
	assert.True(t, w.s3.Has("hot", key))
}

func TestCovE_RefreshStorageGauge(t *testing.T) {
	w := covENewTierWorld(t, false)
	w.seed(t, "hot", 1*covEDay, 1000, covEDetOpts{Tier: "hot"})
	w.seed(t, "hot", 1*covEDay, 4000, covEDetOpts{Tier: "cold"})
	j := w.job(t, nil, nil, nil)

	require.NoError(t, j.refreshStorageGauge(w.ctx))

	var hotSum, coldSum, archSum int64
	require.NoError(t, w.pool.QueryRow(w.ctx, `
		SELECT COALESCE(SUM(evidence_size_bytes) FILTER (WHERE tier='hot'),0),
		       COALESCE(SUM(evidence_size_bytes) FILTER (WHERE tier='cold'),0),
		       COALESCE(SUM(evidence_size_bytes) FILTER (WHERE tier='archive'),0)
		FROM detections WHERE evidence_key IS NOT NULL`).Scan(&hotSum, &coldSum, &archSum))
	assert.InDelta(t, float64(hotSum), covEMetric(t, metrics.EvidenceStorageBytes.WithLabelValues("hot")), 0.001)
	assert.InDelta(t, float64(coldSum), covEMetric(t, metrics.EvidenceStorageBytes.WithLabelValues("cold")), 0.001)
	assert.InDelta(t, float64(archSum), covEMetric(t, metrics.EvidenceStorageBytes.WithLabelValues("archive")), 0.001,
		"tier sem linhas é zerado (não fica com valor velho)")
	assert.GreaterOrEqual(t, coldSum, int64(4000))

	broken := NewTieringJob(covEClosedPool(t), nil, nil, nil, zap.NewNop())
	assert.Error(t, broken.refreshStorageGauge(w.ctx))
}

// ───────────────────── Schedule ─────────────────────

func TestCovE_Schedule_RunsStartupPassThenStopsOnCancel(t *testing.T) {
	w := covENewTierWorld(t, false)
	j := w.job(t, nil, nil, nil)
	stamp := time.Date(2031, 3, 4, 5, 6, 7, 0, time.UTC) // valor único: nenhum outro teste usa
	j.Now = func() time.Time { return stamp }

	ctx, cancel := context.WithCancel(w.ctx)
	done := make(chan struct{})
	go func() { j.Schedule(ctx); close(done) }()

	// A passada de partida roda em background e carimba o gauge (Now é fixo).
	covEEventually(t, func() bool {
		return covEMetric(t, metrics.EvidenceTieringLastRun) == float64(stamp.Unix())
	}, "passada de partida deve atualizar EvidenceTieringLastRun")

	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Schedule não parou após cancelar o contexto")
	}
}

// ───────────────────── moveTier: hot → cold → archive (buckets distintos) ─────────────────────

func TestCovE_MoveTier_MovesObjectsBetweenBuckets(t *testing.T) {
	w := covENewTierWorld(t, true) // TLS: o SDK só reenvia body não-seekable sem TLS+checksum
	hot, cold, archive := w.s3.Client(t, "hot"), w.s3.Client(t, "cold"), w.s3.Client(t, "archive")

	t.Run("hot -> cold copia, atualiza tier/tamanho e apaga a origem", func(t *testing.T) {
		id, key := w.seed(t, "hot", 40*covEDay, 64, covEDetOpts{Size: 1})
		j := w.job(t, hot, cold, archive)
		moved, errs := j.moveTier(w.ctx, "hot", "cold", hot, cold, j.HotMaxAge, "")
		assert.Equal(t, 1, moved)
		assert.Zero(t, errs)
		assert.True(t, w.s3.Has("cold", key), "objeto no destino")
		assert.False(t, w.s3.Has("hot", key), "origem apagada")
		d := covEReadDet(t, w.ctx, w.pool, id)
		assert.Equal(t, "cold", d.Tier)
		require.NotNil(t, d.Size)
		assert.Equal(t, int64(64), *d.Size, "tamanho passa a ser o do objeto (Content-Length)")
		assert.Equal(t, "available", d.Status)
	})

	t.Run("cold -> archive usa a storage class pedida", func(t *testing.T) {
		id, key := w.seed(t, "cold", 400*covEDay, 32, covEDetOpts{Tier: "cold"})
		j := w.job(t, hot, cold, archive)
		moved, errs := j.moveTier(w.ctx, "cold", "archive", cold, archive, j.ColdMaxAge, "STANDARD_IA")
		assert.Equal(t, 1, moved)
		assert.Zero(t, errs)
		assert.Equal(t, "STANDARD_IA", w.s3.PutHeader("archive", key, "X-Amz-Storage-Class"))
		assert.Equal(t, "archive", covEReadDet(t, w.ctx, w.pool, id).Tier)
	})

	t.Run("só move o que passou da idade, está no tier de origem e tem clipe", func(t *testing.T) {
		young, youngKey := w.seed(t, "hot", 3*covEDay, 10, covEDetOpts{})
		otherTier, otherKey := w.seed(t, "hot", 50*covEDay, 10, covEDetOpts{Tier: "cold"})
		failed, _ := w.seed(t, "hot", 50*covEDay, 10, covEDetOpts{Status: "failed"})
		j := w.job(t, hot, cold, archive)
		moved, errs := j.moveTier(w.ctx, "hot", "cold", hot, cold, j.HotMaxAge, "")
		assert.Zero(t, moved)
		assert.Zero(t, errs)
		assert.Equal(t, "hot", covEReadDet(t, w.ctx, w.pool, young).Tier)
		assert.True(t, w.s3.Has("hot", youngKey))
		assert.Equal(t, "cold", covEReadDet(t, w.ctx, w.pool, otherTier).Tier)
		assert.True(t, w.s3.Has("hot", otherKey))
		assert.Equal(t, "hot", covEReadDet(t, w.ctx, w.pool, failed).Tier)
	})

	t.Run("respeita o tamanho do lote", func(t *testing.T) {
		w2 := covENewTierWorld(t, true)
		h, c := w2.s3.Client(t, "hot"), w2.s3.Client(t, "cold")
		w2.seed(t, "hot", 40*covEDay, 8, covEDetOpts{})
		w2.seed(t, "hot", 41*covEDay, 8, covEDetOpts{})
		j := w2.job(t, h, c, nil)
		j.BatchSize = 1
		moved, errs := j.moveTier(w2.ctx, "hot", "cold", h, c, j.HotMaxAge, "")
		assert.Equal(t, 1, moved)
		assert.Zero(t, errs)
	})
}

func TestCovE_MoveTier_FailuresLeaveTheRowInTheOriginTier(t *testing.T) {
	t.Run("objeto de origem inexistente", func(t *testing.T) {
		w := covENewTierWorld(t, true)
		hot, cold := w.s3.Client(t, "hot"), w.s3.Client(t, "cold")
		id, key := w.seed(t, "hot", 40*covEDay, 8, covEDetOpts{})
		w.s3.mu.Lock()
		delete(w.s3.objs, "hot/"+key)
		w.s3.mu.Unlock()
		j := w.job(t, hot, cold, nil)
		moved, errs := j.moveTier(w.ctx, "hot", "cold", hot, cold, j.HotMaxAge, "")
		assert.Zero(t, moved)
		assert.Equal(t, 1, errs)
		assert.Equal(t, "hot", covEReadDet(t, w.ctx, w.pool, id).Tier)
	})

	t.Run("PUT no destino falha", func(t *testing.T) {
		w := covENewTierWorld(t, true)
		hot, cold := w.s3.Client(t, "hot"), w.s3.Client(t, "cold")
		id, key := w.seed(t, "hot", 40*covEDay, 8, covEDetOpts{})
		w.s3.FailPuts(true)
		j := w.job(t, hot, cold, nil)
		err := j.moveOne(w.ctx, candidate{ID: id, DetectedAt: w.ago(40 * covEDay), EvidenceKey: key, SizeBytes: 8}, "hot", "cold", hot, cold, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "put to cold")
		assert.Equal(t, "hot", covEReadDet(t, w.ctx, w.pool, id).Tier)
		assert.True(t, w.s3.Has("hot", key), "origem intacta")
	})

	t.Run("sem TLS o SDK recusa reenviar body não-seekable: erro, origem intacta", func(t *testing.T) {
		w := covENewTierWorld(t, false)
		hot, cold := w.s3.Client(t, "hot"), w.s3.Client(t, "cold")
		id, key := w.seed(t, "hot", 40*covEDay, 8, covEDetOpts{})
		j := w.job(t, hot, cold, nil)
		moved, errs := j.moveTier(w.ctx, "hot", "cold", hot, cold, j.HotMaxAge, "STANDARD_IA")
		assert.Zero(t, moved)
		assert.Equal(t, 1, errs)
		assert.Equal(t, "hot", covEReadDet(t, w.ctx, w.pool, id).Tier)
		assert.True(t, w.s3.Has("hot", key))
	})

	t.Run("verificação HEAD no destino falha antes de tocar na linha", func(t *testing.T) {
		w := covENewTierWorld(t, true)
		hot, cold := w.s3.Client(t, "hot"), w.s3.Client(t, "cold")
		id, key := w.seed(t, "hot", 40*covEDay, 8, covEDetOpts{})
		w.s3.FailHeads(true)
		j := w.job(t, hot, cold, nil)
		err := j.moveOne(w.ctx, candidate{ID: id, DetectedAt: w.ago(40 * covEDay), EvidenceKey: key, SizeBytes: 8}, "hot", "cold", hot, cold, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "verify dst")
		assert.Equal(t, "hot", covEReadDet(t, w.ctx, w.pool, id).Tier)
		assert.True(t, w.s3.Has("hot", key))
	})

	t.Run("linha some no meio da movimentação", func(t *testing.T) {
		w := covENewTierWorld(t, true)
		hot, cold := w.s3.Client(t, "hot"), w.s3.Client(t, "cold")
		_, key := w.seed(t, "hot", 40*covEDay, 8, covEDetOpts{})
		j := w.job(t, hot, cold, nil)
		err := j.moveOne(w.ctx, candidate{ID: uuid.New(), DetectedAt: w.ago(40 * covEDay), EvidenceKey: key, SizeBytes: 8}, "hot", "cold", hot, cold, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "vanished")
	})

	t.Run("falha ao apagar a origem não desfaz o move (próxima passada refaz o delete)", func(t *testing.T) {
		w := covENewTierWorld(t, true)
		hot, cold := w.s3.Client(t, "hot"), w.s3.Client(t, "cold")
		id, key := w.seed(t, "hot", 40*covEDay, 8, covEDetOpts{})
		w.s3.FailDelete("hot", key)
		before := covEMetric(t, metrics.EvidenceTieringErrors)
		j := w.job(t, hot, cold, nil)
		moved, errs := j.moveTier(w.ctx, "hot", "cold", hot, cold, j.HotMaxAge, "")
		assert.Equal(t, 1, moved)
		assert.Zero(t, errs)
		assert.Equal(t, "cold", covEReadDet(t, w.ctx, w.pool, id).Tier)
		assert.True(t, w.s3.Has("cold", key))
		assert.InDelta(t, before+1, covEMetric(t, metrics.EvidenceTieringErrors), 0.001)
	})

	t.Run("consulta de candidatos falha", func(t *testing.T) {
		w := covENewTierWorld(t, true)
		hot, cold := w.s3.Client(t, "hot"), w.s3.Client(t, "cold")
		j := NewTieringJob(covEClosedPool(t), hot, cold, nil, zap.NewNop())
		moved, errs := j.moveTier(w.ctx, "hot", "cold", hot, cold, time.Hour, "")
		assert.Zero(t, moved)
		assert.Equal(t, 1, errs)
	})

	t.Run("contexto cancelado entre candidatos para o lote", func(t *testing.T) {
		w := covENewTierWorld(t, true)
		hot, cold := w.s3.Client(t, "hot"), w.s3.Client(t, "cold")
		id1, _ := w.seed(t, "hot", 40*covEDay, 8, covEDetOpts{})
		j := w.job(t, hot, cold, nil)
		ctx, cancel := context.WithCancel(w.ctx)
		cancel()
		moved, errs := j.moveTier(ctx, "hot", "cold", hot, cold, j.HotMaxAge, "")
		assert.Zero(t, moved)
		assert.LessOrEqual(t, errs, 1)
		assert.Equal(t, "hot", covEReadDet(t, w.ctx, w.pool, id1).Tier)
	})
}

func TestCovE_TieringRun_MovesAcrossBucketsEndToEnd(t *testing.T) {
	w := covENewTierWorld(t, true)
	hot, cold, archive := w.s3.Client(t, "hot"), w.s3.Client(t, "cold"), w.s3.Client(t, "archive")
	toCold, k1 := w.seed(t, "hot", 40*covEDay, 16, covEDetOpts{})
	toArchive, k2 := w.seed(t, "cold", 400*covEDay, 16, covEDetOpts{Tier: "cold"})
	j := w.job(t, hot, cold, archive)

	require.NoError(t, j.Run(w.ctx))

	assert.Equal(t, "cold", covEReadDet(t, w.ctx, w.pool, toCold).Tier)
	assert.True(t, w.s3.Has("cold", k1))
	assert.Equal(t, "archive", covEReadDet(t, w.ctx, w.pool, toArchive).Tier)
	assert.True(t, w.s3.Has("archive", k2))
	assert.Equal(t, "STANDARD_IA", w.s3.PutHeader("archive", k2, "X-Amz-Storage-Class"))
	_ = fmt.Sprint
}
