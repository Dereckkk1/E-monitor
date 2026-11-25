package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/events"
	"radiocheck/internal/metrics"
)

const covEBucket = "evidence"

// covEPipe é um mini-mundo pra exercitar handle()/processEvidence de ponta a
// ponta: cliente, campanha ativa, emissora, material com master no banco, S3
// falso e um Service real.
type covEPipe struct {
	ctx        context.Context
	pool       *pgxpool.Pool
	s3         *covES3
	svc        *Service
	client     uuid.UUID
	campaign   uuid.UUID
	station    uuid.UUID
	material   uuid.UUID
	short      int32
	pcm        []float32 // áudio do master do material (30s)
	detectedAt time.Time
}

type covEPipeOpts struct {
	Auditor    bool
	ByCoverage bool
	Twin       bool
	Multi      bool
	NoMaster   bool // material sem hashes no banco (auditor falha ao carregar o master)
}

func covENewPipe(t *testing.T, o covEPipeOpts) *covEPipe {
	t.Helper()
	ctx, pool := covEDB(t)
	covEInstallFakeFFmpeg(t)
	s3 := covENewS3(t, false)

	p := &covEPipe{ctx: ctx, pool: pool, s3: s3, pcm: covESynth(201, 30)}
	p.client = covESeedClient(t, ctx, pool)
	p.campaign = covESeedCampaign(t, ctx, pool, p.client)
	p.station = covESeedStation(t, ctx, pool)
	master := p.pcm
	if o.NoMaster {
		master = nil
	}
	p.material, p.short = covESeedMaterial(t, ctx, pool, p.client, "Spot Teste 30s", 30, "", master)
	covELink(t, ctx, pool, p.campaign, p.material, p.station)

	p.svc = covENewService(pool, covESvcOpts{
		Store:      s3.Client(t, covEBucket),
		Auditor:    o.Auditor,
		ByCoverage: o.ByCoverage,
		Twin:       o.Twin,
		Multi:      o.Multi,
	})
	p.detectedAt = time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	return p
}

func (p *covEPipe) windowStart() time.Time { return p.detectedAt.Add(-20 * time.Second) }
func (p *covEPipe) windowEnd() time.Time   { return p.detectedAt.Add(10 * time.Second) }

// registerSegments registra o diretório de segmentos da emissora no Service.
func (p *covEPipe) registerSegments(t *testing.T) {
	t.Helper()
	p.svc.Register(p.station, covESegDir(t, p.windowStart()))
}

func (p *covEPipe) eventJSON(t *testing.T, short int32) []byte {
	t.Helper()
	b, err := json.Marshal(detectionEvent{
		StationID:           p.station.String(),
		CommercialShortID:   short,
		DetectedAt:          p.detectedAt.Format(time.RFC3339),
		OffsetFrames:        4,
		Confidence:          0.91,
		EvidenceWindowStart: p.windowStart().Format(time.RFC3339),
		EvidenceWindowEnd:   p.windowEnd().Format(time.RFC3339),
		HashCount:           120,
		TemporalCoverage:    0.91,
		MatchStartOffsetMs:  0,
		MatchEndOffsetMs:    30000,
	})
	require.NoError(t, err)
	return b
}

func (p *covEPipe) fire(t *testing.T) {
	t.Helper()
	p.svc.handle(&nats.Msg{Subject: events.SubjectDetectionConfirmed, Data: p.eventJSON(t, p.short)})
}

// detectionID devolve a detecção criada pra emissora (0 ou 1 linhas).
func (p *covEPipe) detectionID(t *testing.T) (uuid.UUID, bool) {
	t.Helper()
	var id uuid.UUID
	err := p.pool.QueryRow(p.ctx, `SELECT id FROM detections WHERE station_id = $1 ORDER BY created_at LIMIT 1`, p.station).Scan(&id)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// waitSettled espera a goroutine de evidência tirar a detecção de "pending".
func (p *covEPipe) waitSettled(t *testing.T) (uuid.UUID, covEDet) {
	t.Helper()
	var id uuid.UUID
	covEEventually(t, func() bool {
		var ok bool
		id, ok = p.detectionID(t)
		if !ok {
			return false
		}
		return covEReadDet(t, p.ctx, p.pool, id).Status != "pending"
	}, "a evidência deveria sair de 'pending'")
	return id, covEReadDet(t, p.ctx, p.pool, id)
}

func (p *covEPipe) expectedKey(detID uuid.UUID) string {
	d := p.detectedAt.UTC()
	return fmt.Sprintf("evidences/%s/%s/%s/%s/%s.m4a", d.Format("2006"), d.Format("01"), d.Format("02"), p.station, detID)
}

// ───────────────────── handle: validação do evento ─────────────────────

func TestCovE_Handle_UnknownShortIDDropsTheDetection(t *testing.T) {
	p := covENewPipe(t, covEPipeOpts{})
	p.svc.handle(&nats.Msg{Subject: events.SubjectDetectionConfirmed, Data: p.eventJSON(t, 2_000_000_007)})
	_, found := p.detectionID(t)
	assert.False(t, found, "short_id sem material/commercial não gera detecção (nem FK quebrada)")
}

// ───────────────────── pipeline feliz ─────────────────────

func TestCovE_Handle_HappyPath_PersistsDetectionProjectionAndUploadsEvidence(t *testing.T) {
	p := covENewPipe(t, covEPipeOpts{})
	p.registerSegments(t)
	p.fire(t)

	id, det := p.waitSettled(t)
	assert.Equal(t, "available", det.Status)
	assert.Equal(t, p.material, det.CommercialID)
	assert.Equal(t, p.campaign, det.CampaignID)

	key := p.expectedKey(id)
	require.NotNil(t, det.Key)
	assert.Equal(t, key, *det.Key)
	require.NotNil(t, det.Size)
	assert.Equal(t, int64(len(covEFakeFFmpegOutput)), *det.Size)
	assert.True(t, p.s3.Has(covEBucket, key), "clipe enviado ao S3 na chave esperada")
	assert.Equal(t, covEFakeFFmpegOutput, string(p.s3.Body(covEBucket, key)))
	assert.Equal(t, "video/mp4", p.s3.ContentType(covEBucket, key))

	// Projeção canônica (F-119) gravada junto da detecção.
	proj := covEProjections(t, p.ctx, p.pool, id)
	assert.Equal(t, map[uuid.UUID]uuid.UUID{p.campaign: p.material}, proj)
}

// ───────────────────── falhas da goroutine de evidência ─────────────────────

func TestCovE_ProcessEvidence_FailureModes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, p *covEPipe)
	}{
		{"sem diretório de segmentos registrado", func(t *testing.T, p *covEPipe) {}},
		{"diretório sem nenhum segmento no intervalo", func(t *testing.T, p *covEPipe) {
			p.svc.Register(p.station, t.TempDir())
		}},
		{"ffmpeg falha ao aparar o trecho", func(t *testing.T, p *covEPipe) {
			p.registerSegments(t)
			t.Setenv(covEEnvFailArg, "-ss")
		}},
		{"trecho extraído vazio", func(t *testing.T, p *covEPipe) {
			p.registerSegments(t)
			t.Setenv(covEEnvEmpty, "1")
		}},
		{"ffmpeg falha ao encapsular em m4a", func(t *testing.T, p *covEPipe) {
			p.registerSegments(t)
			t.Setenv(covEEnvFailArg, "-movflags")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := covENewPipe(t, covEPipeOpts{})
			c.setup(t, p)
			p.fire(t)
			_, det := p.waitSettled(t)
			assert.Equal(t, "failed", det.Status)
			assert.Empty(t, covEKey(det), "falha não deixa evidence_key")
			assert.Zero(t, p.s3.Count(), "nada pode ir pro S3")
		})
	}
}

func TestCovE_ProcessEvidence_UploadFailureCountsMetricAndMarksFailed(t *testing.T) {
	p := covENewPipe(t, covEPipeOpts{})
	p.registerSegments(t)
	p.s3.FailPuts(true)
	before := covEMetric(t, metrics.EvidenceUploadFailures)

	p.fire(t)
	_, det := p.waitSettled(t)

	assert.Equal(t, "failed", det.Status)
	assert.Empty(t, covEKey(det))
	assert.InDelta(t, before+1, covEMetric(t, metrics.EvidenceUploadFailures), 0.001,
		"falha de upload alimenta o alerta EvidenceUploadFailures")
}

// ───────────────────── §9.9 audit pré-persist ─────────────────────

func TestCovE_ProcessEvidence_Audit(t *testing.T) {
	t.Run("clipe que contém o master passa, grava a cobertura e sobe", func(t *testing.T) {
		p := covENewPipe(t, covEPipeOpts{Auditor: true})
		p.registerSegments(t)
		covEServePCM(t, p.pcm)
		passed := covEMetric(t, metrics.AuditAttempts.WithLabelValues("passed"))

		p.fire(t)
		id, det := p.waitSettled(t)

		assert.Equal(t, "available", det.Status)
		require.NotNil(t, det.Cov, "audit_coverage persistido")
		assert.GreaterOrEqual(t, *det.Cov, 0.9)
		assert.True(t, p.s3.Has(covEBucket, p.expectedKey(id)))
		assert.InDelta(t, passed+1, covEMetric(t, metrics.AuditAttempts.WithLabelValues("passed")), 0.001)
	})

	t.Run("clipe que NÃO contém o master é audit_rejected e não sobe", func(t *testing.T) {
		p := covENewPipe(t, covEPipeOpts{Auditor: true})
		p.registerSegments(t)
		covEServePCM(t, covESynth(999, 30))
		rejected := covEMetric(t, metrics.AuditAttempts.WithLabelValues("rejected"))

		p.fire(t)
		_, det := p.waitSettled(t)

		assert.Equal(t, "audit_rejected", det.Status)
		assert.Empty(t, covEKey(det))
		assert.Zero(t, p.s3.Count(), "clipe rejeitado não vai pro S3")
		assert.InDelta(t, rejected+1, covEMetric(t, metrics.AuditAttempts.WithLabelValues("rejected")), 0.001)
	})

	t.Run("falha ao decodificar o clipe não bloqueia o upload", func(t *testing.T) {
		p := covENewPipe(t, covEPipeOpts{Auditor: true})
		p.registerSegments(t)
		t.Setenv(covEEnvFailArg, "pipe:1") // só a decodificação do auditor usa pipe:1
		errs := covEMetric(t, metrics.AuditAttempts.WithLabelValues("error"))

		p.fire(t)
		id, det := p.waitSettled(t)

		assert.Equal(t, "available", det.Status, "audit é rede de segurança, não dependência dura")
		assert.True(t, p.s3.Has(covEBucket, p.expectedKey(id)))
		assert.InDelta(t, errs+1, covEMetric(t, metrics.AuditAttempts.WithLabelValues("error")), 0.001)
	})

	t.Run("master sem hashes no banco: erro de infra, upload segue", func(t *testing.T) {
		p := covENewPipe(t, covEPipeOpts{Auditor: true, NoMaster: true})
		p.registerSegments(t)
		covEServePCM(t, p.pcm)
		errs := covEMetric(t, metrics.AuditAttempts.WithLabelValues("error"))

		p.fire(t)
		_, det := p.waitSettled(t)

		assert.Equal(t, "available", det.Status)
		assert.InDelta(t, errs+1, covEMetric(t, metrics.AuditAttempts.WithLabelValues("error")), 0.001)
	})
}

// ───────────────────── multi-atribuição (F-119) ─────────────────────

func TestCovE_Handle_MultiAttributionFansOutOneProjectionPerCampaign(t *testing.T) {
	p := covENewPipe(t, covEPipeOpts{Multi: true})
	// Segunda campanha roda o MESMO áudio (mesmo master_sha256) na mesma emissora.
	var sha string
	require.NoError(t, p.pool.QueryRow(p.ctx, `SELECT master_sha256 FROM materials WHERE id = $1`, p.material).Scan(&sha))
	campB := covESeedCampaign(t, p.ctx, p.pool, p.client)
	matB, _ := covESeedMaterial(t, p.ctx, p.pool, p.client, "Spot Teste 30s (B)", 30, sha, nil)
	covELink(t, p.ctx, p.pool, campB, matB, p.station)

	p.registerSegments(t)
	p.fire(t)
	id, det := p.waitSettled(t)

	assert.Equal(t, "available", det.Status, "a tocada física segue o fluxo normal de evidência")
	proj := covEProjections(t, p.ctx, p.pool, id)
	assert.Len(t, proj, 2, "uma projeção por campanha que roda o áudio")
	assert.Equal(t, matB, proj[campB], "cada campanha com o SEU material")
	assert.Equal(t, p.material, proj[p.campaign])

	var n int
	require.NoError(t, p.pool.QueryRow(p.ctx, `SELECT COUNT(*) FROM detections WHERE station_id = $1`, p.station).Scan(&n))
	assert.Equal(t, 1, n, "continua sendo UMA tocada física")
}

func TestCovE_Handle_WithoutMultiAttributionOnlyCanonicalProjection(t *testing.T) {
	p := covENewPipe(t, covEPipeOpts{Multi: false})
	var sha string
	require.NoError(t, p.pool.QueryRow(p.ctx, `SELECT master_sha256 FROM materials WHERE id = $1`, p.material).Scan(&sha))
	campB := covESeedCampaign(t, p.ctx, p.pool, p.client)
	matB, _ := covESeedMaterial(t, p.ctx, p.pool, p.client, "Spot (B)", 30, sha, nil)
	covELink(t, p.ctx, p.pool, campB, matB, p.station)

	p.registerSegments(t)
	p.fire(t)
	id, _ := p.waitSettled(t)
	assert.Len(t, covEProjections(t, p.ctx, p.pool, id), 1, "com a flag OFF só a projeção canônica")
}

// ───────────────────── NATS: Subscribe ─────────────────────

func TestCovE_Subscribe_ConsumesConfirmedEvents(t *testing.T) {
	p := covENewPipe(t, covEPipeOpts{})
	srv := covENewNATS(t)
	nc := covEConnect(t, srv)
	pub := covEConnect(t, srv)
	p.svc.nc = nc
	p.registerSegments(t)

	sub, err := p.svc.Subscribe(p.ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	require.NoError(t, nc.Flush())

	require.NoError(t, pub.Publish(events.SubjectDetectionConfirmed, []byte("isto-nao-e-json")))
	require.NoError(t, pub.Publish(events.SubjectDetectionConfirmed, p.eventJSON(t, p.short)))
	require.NoError(t, pub.Flush())

	id, det := p.waitSettled(t)
	assert.Equal(t, "available", det.Status)
	assert.True(t, p.s3.Has(covEBucket, p.expectedKey(id)))
}

func TestCovE_Subscribe_FailsOnClosedConn(t *testing.T) {
	srv := covENewNATS(t)
	nc := covEConnect(t, srv)
	nc.Close()
	s := NewService(nil, nil, nc, nil, nil, nil, false, false, false, false, zap.NewNop())
	sub, err := s.Subscribe(context.Background())
	require.Error(t, err)
	assert.Nil(t, sub)
	assert.Contains(t, err.Error(), "evidence: subscribe")
}

// ───────────────────── pedaços isolados ─────────────────────

func TestCovE_EncodeToM4A(t *testing.T) {
	id := uuid.New()
	at := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	t.Run("devolve o que o ffmpeg gravou e passa os metadados", func(t *testing.T) {
		covEInstallFakeFFmpeg(t)
		out, err := encodeToM4A([]byte("aac-bytes"), id, at)
		require.NoError(t, err)
		assert.Equal(t, covEFakeFFmpegOutput, string(out))
	})

	t.Run("erro do ffmpeg vira erro com a saída anexada", func(t *testing.T) {
		covEInstallFakeFFmpeg(t)
		t.Setenv(covEEnvFailArg, "-movflags")
		out, err := encodeToM4A([]byte("aac-bytes"), id, at)
		require.Error(t, err)
		assert.Nil(t, out)
		assert.Contains(t, err.Error(), "ffmpeg encode")
		assert.Contains(t, err.Error(), "falha forçada", "stderr do ffmpeg vai na mensagem")
	})

	t.Run("ffmpeg ausente do PATH", func(t *testing.T) {
		covENoFFmpeg(t)
		_, err := encodeToM4A([]byte("x"), id, at)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ffmpeg encode")
	})
}

func TestCovE_TraceContextFromParent_KeepsChildWhenParentHasNoSpan(t *testing.T) {
	type key struct{}
	child := context.WithValue(context.Background(), key{}, "filho")
	got := traceContextFromParent(context.Background(), child)
	assert.Equal(t, "filho", got.Value(key{}), "sem span no pai devolve o contexto filho intacto")
}

// ───────────────────── degradações de banco no handle ─────────────────────

func TestCovE_Handle_InsertFailureDropsTheEvent(t *testing.T) {
	p := covENewPipe(t, covEPipeOpts{})
	// O banco de resolução da atribuição funciona; o INSERT da detecção não.
	p.svc = NewService(p.pool, p.s3.Client(t, covEBucket), nil, catalog.NewDetections(covEClosedPool(t)),
		nil, nil, false, false, false, false, zap.NewNop())
	p.fire(t)
	_, found := p.detectionID(t)
	assert.False(t, found, "sem conseguir gravar, nada fica e nada pode panicar")
}

func TestCovE_Handle_ProjectionInsertFailureResettlesFanOutCells(t *testing.T) {
	p := covENewPipe(t, covEPipeOpts{})
	var sha string
	require.NoError(t, p.pool.QueryRow(p.ctx, `SELECT master_sha256 FROM materials WHERE id = $1`, p.material).Scan(&sha))
	campB := covESeedCampaign(t, p.ctx, p.pool, p.client)
	matB, _ := covESeedMaterial(t, p.ctx, p.pool, p.client, "Spot (B)", 30, sha, nil)
	covELink(t, p.ctx, p.pool, campB, matB, p.station)
	// Fan-out ligado, mas InsertProjections vai falhar (pool fechado): a canônica já
	// entrou na transação do Create; as extras são refechadas sem a tocada nova.
	p.svc = NewService(p.pool, p.s3.Client(t, covEBucket), nil, catalog.NewDetections(p.pool),
		catalog.NewDetectionCampaigns(covEClosedPool(t)), nil, false, false, false, true, zap.NewNop())
	p.registerSegments(t)
	p.fire(t)

	id, det := p.waitSettled(t)
	assert.Equal(t, "available", det.Status, "a falha das projeções extras não derruba a evidência")
	assert.Equal(t, map[uuid.UUID]uuid.UUID{p.campaign: p.material}, covEProjections(t, p.ctx, p.pool, id),
		"só a projeção canônica (gravada no Create) existe")
}

func TestCovE_Handle_WithoutProjectionRepoStillPersistsCanonicalProjection(t *testing.T) {
	p := covENewPipe(t, covEPipeOpts{})
	p.svc = NewService(p.pool, p.s3.Client(t, covEBucket), nil, catalog.NewDetections(p.pool),
		nil, nil, false, false, false, false, zap.NewNop())
	p.registerSegments(t)
	p.fire(t)
	id, det := p.waitSettled(t)
	assert.Equal(t, "available", det.Status)
	assert.Equal(t, map[uuid.UUID]uuid.UUID{p.campaign: p.material}, covEProjections(t, p.ctx, p.pool, id))
}

func TestCovE_MarkFailed(t *testing.T) {
	ctx, pool := covEDB(t)
	client := covESeedClient(t, ctx, pool)
	camp := covESeedCampaign(t, ctx, pool, client)
	station := covESeedStation(t, ctx, pool)
	mat, _ := covESeedMaterial(t, ctx, pool, client, "Spot", 30, "", nil)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	t.Run("marca a linha como failed", func(t *testing.T) {
		id := covESeedDet(t, ctx, pool, camp, mat, station, at, covEDetOpts{Status: "pending"})
		covENewService(pool, covESvcOpts{}).markFailed(ctx, id, at, "motivo qualquer")
		assert.Equal(t, "failed", covEReadDet(t, ctx, pool, id).Status)
	})

	t.Run("falha ao gravar só loga", func(t *testing.T) {
		svc := NewService(pool, nil, nil, catalog.NewDetections(covEClosedPool(t)), nil, nil, false, false, false, false, zap.NewNop())
		assert.NotPanics(t, func() { svc.markFailed(ctx, uuid.New(), at, "x") })
	})
}
