package evidence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/audit"
	"radiocheck/internal/catalog"
	"radiocheck/internal/metrics"
)

// covEWorld monta o cenário da desambiguação de versões (§18.2.2): dois cortes do
// mesmo cliente, um atribuído ("self") e um irmão ("sib"), ambos com master no
// banco, e uma detecção atribuída ao self.
type covEWorld struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	client   uuid.UUID
	camp     uuid.UUID
	station  uuid.UUID
	self     uuid.UUID
	sib      uuid.UUID
	selfShrt int32
	sibShrt  int32
	at       time.Time
	svc      *Service
	aud      *audit.Auditor
}

type covEWorldOpts struct {
	SelfPCM, SibPCM []float32
	SelfDur, SibDur float64
	NoLinkSib       bool // irmão sem vínculo com campanha viva (resolveAttribution falha)
	NoSibHashes     bool // irmão sem master no banco (auditoria dele falha)
	NoSelfHashes    bool
	ByCoverage      bool
	Twin            bool
	TwinRetract     bool
}

func covENewWorld(t *testing.T, o covEWorldOpts) *covEWorld {
	t.Helper()
	ctx, pool := covEDB(t)
	w := &covEWorld{ctx: ctx, pool: pool, at: time.Now().UTC().Add(-time.Hour).Truncate(time.Second)}
	w.client = covESeedClient(t, ctx, pool)
	w.camp = covESeedCampaign(t, ctx, pool, w.client)
	w.station = covESeedStation(t, ctx, pool)
	selfPCM, sibPCM := o.SelfPCM, o.SibPCM
	if o.NoSelfHashes {
		selfPCM = nil
	}
	if o.NoSibHashes {
		sibPCM = nil
	}
	w.self, w.selfShrt = covESeedMaterial(t, ctx, pool, w.client, "Corte A", o.SelfDur, "", selfPCM)
	w.sib, w.sibShrt = covESeedMaterial(t, ctx, pool, w.client, "Corte B", o.SibDur, "", sibPCM)
	covELink(t, ctx, pool, w.camp, w.self, w.station)
	if !o.NoLinkSib {
		covELink(t, ctx, pool, w.camp, w.sib, w.station)
	}
	w.svc = covENewService(pool, covESvcOpts{
		Auditor: true, ByCoverage: o.ByCoverage, Twin: o.Twin, TwinRetract: o.TwinRetract,
	})
	w.aud = audit.NewAuditor(pool, zap.NewNop(), 0, 0)
	return w
}

// seedSelfDet semeia a detecção atribuída ao corte "self".
func (w *covEWorld) seedSelfDet(t *testing.T, o covEDetOpts) uuid.UUID {
	t.Helper()
	return covESeedDet(t, w.ctx, w.pool, w.camp, w.self, w.station, w.at, o)
}

// seedSibDet semeia uma detecção do irmão, offset depois da tocada.
func (w *covEWorld) seedSibDet(t *testing.T, offset time.Duration, o covEDetOpts) uuid.UUID {
	t.Helper()
	return covESeedDet(t, w.ctx, w.pool, w.camp, w.sib, w.station, w.at.Add(offset), o)
}

func (w *covEWorld) coverage(t *testing.T, id uuid.UUID, pcm []float32) float64 {
	t.Helper()
	r, err := w.aud.AuditEvidence(w.ctx, id, pcm)
	require.NoError(t, err)
	return r.Coverage
}

func (w *covEWorld) det(t *testing.T, id uuid.UUID) covEDet {
	t.Helper()
	return covEReadDet(t, w.ctx, w.pool, id)
}

func covEMetricLabel(t *testing.T, label string) float64 {
	t.Helper()
	return covEMetric(t, metrics.MatchDisambiguation.WithLabelValues(label))
}

// ───────────────────── reattributeByCoverage (pass-path) ─────────────────────

// 30s (self) atribuído, mas o clipe é o 15s (primeira metade do 30s): o corte
// irmão cobre ~1.0 do próprio master contra ~0.5 do 30s, e vence por margem.
func covESubsetWorld(t *testing.T, o covEWorldOpts) (*covEWorld, []float32) {
	t.Helper()
	pcm30 := covESynth(301, 30)
	clip := pcm30[:15*16000]
	o.SelfPCM, o.SibPCM = pcm30, clip
	o.SelfDur, o.SibDur = 30, 15
	return covENewWorld(t, o), clip
}

func TestCovE_ReattributeByCoverage_RepointsToTheCutTheClipReallyCovers(t *testing.T) {
	w, clip := covESubsetWorld(t, covEWorldOpts{})
	det := w.seedSelfDet(t, covEDetOpts{})
	selfCov := w.coverage(t, w.self, clip)
	require.InDelta(t, 0.5, selfCov, 0.1, "pré-condição: o 30s só é coberto pela metade")
	before := covEMetricLabel(t, "reattributed_by_coverage")

	got := w.svc.reattributeByCoverage(w.ctx, det, w.at, w.station, w.self, selfCov, clip)

	require.True(t, got, "o corte irmão cobre >1.5× mais: reatribui")
	d := w.det(t, det)
	assert.Equal(t, w.sib, d.CommercialID, "linha aponta pro corte de 15s")
	assert.Equal(t, w.camp, d.CampaignID)
	require.NotNil(t, d.Cov)
	assert.GreaterOrEqual(t, *d.Cov, 0.9, "audit_coverage reflete a cobertura do corte vencedor")
	assert.False(t, d.Retracted)
	assert.Equal(t, map[uuid.UUID]uuid.UUID{w.camp: w.sib}, covEProjections(t, w.ctx, w.pool, det),
		"a projeção canônica acompanha a reatribuição")
	assert.InDelta(t, before+1, covEMetricLabel(t, "reattributed_by_coverage"), 0.001)
}

func TestCovE_ReattributeByCoverage_KeepsAttributionWhenSelfWinsOrNoSiblings(t *testing.T) {
	t.Run("clipe do 30s inteiro: empate de cobertura cai pra duração e mantém o 30s", func(t *testing.T) {
		w, _ := covESubsetWorld(t, covEWorldOpts{})
		full := covESynth(301, 30)
		det := w.seedSelfDet(t, covEDetOpts{})
		got := w.svc.reattributeByCoverage(w.ctx, det, w.at, w.station, w.self, w.coverage(t, w.self, full), full)
		assert.False(t, got)
		assert.Equal(t, w.self, w.det(t, det).CommercialID)
	})

	t.Run("sem cortes irmãos não há o que desambiguar", func(t *testing.T) {
		ctx, pool := covEDB(t)
		client := covESeedClient(t, ctx, pool)
		camp := covESeedCampaign(t, ctx, pool, client)
		station := covESeedStation(t, ctx, pool)
		pcm := covESynth(305, 20)
		mat, _ := covESeedMaterial(t, ctx, pool, client, "Único", 20, "", pcm)
		covELink(t, ctx, pool, camp, mat, station)
		at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
		det := covESeedDet(t, ctx, pool, camp, mat, station, at, covEDetOpts{})
		svc := covENewService(pool, covESvcOpts{Auditor: true, ByCoverage: true})
		assert.False(t, svc.reattributeByCoverage(ctx, det, at, station, mat, 0.9, pcm))
		assert.Equal(t, mat, covEReadDet(t, ctx, pool, det).CommercialID)
	})

	t.Run("material legado (fora de materials) não tem irmãos", func(t *testing.T) {
		w, clip := covESubsetWorld(t, covEWorldOpts{})
		det := w.seedSelfDet(t, covEDetOpts{})
		assert.False(t, w.svc.reattributeByCoverage(w.ctx, det, w.at, w.station, uuid.New(), 0.5, clip))
	})

	t.Run("falha ao buscar irmãos deixa tudo como está", func(t *testing.T) {
		w, clip := covESubsetWorld(t, covEWorldOpts{})
		det := w.seedSelfDet(t, covEDetOpts{})
		broken := NewService(w.pool, nil, nil, catalog.NewDetections(covEClosedPool(t)), nil,
			w.aud, true, false, false, false, zap.NewNop())
		assert.False(t, broken.reattributeByCoverage(w.ctx, det, w.at, w.station, w.self, 0.5, clip))
		assert.Equal(t, w.self, w.det(t, det).CommercialID)
	})
}

func TestCovE_ReattributeByCoverage_SkipsSiblingWhoseAuditFails(t *testing.T) {
	// 3 cortes: o irmão "Corte B" tem master; um terceiro sem hashes quebra a auditoria
	// dele mas não impede a decisão com o resto.
	w, clip := covESubsetWorld(t, covEWorldOpts{})
	ghost, _ := covESeedMaterial(t, w.ctx, w.pool, w.client, "Corte C (sem master)", 20, "", nil)
	covELink(t, w.ctx, w.pool, w.camp, ghost, w.station)
	det := w.seedSelfDet(t, covEDetOpts{})

	got := w.svc.reattributeByCoverage(w.ctx, det, w.at, w.station, w.self, w.coverage(t, w.self, clip), clip)
	assert.True(t, got)
	assert.Equal(t, w.sib, w.det(t, det).CommercialID)
}

func TestCovE_ReattributeByCoverage_CofireGuard(t *testing.T) {
	t.Run("irmão vencedor já tem a tocada contada: retrata a duplicata em vez de reatribuir", func(t *testing.T) {
		w, clip := covESubsetWorld(t, covEWorldOpts{})
		det := w.seedSelfDet(t, covEDetOpts{})
		winner := w.seedSibDet(t, 4*time.Second, covEDetOpts{Status: "available"})
		before := covEMetricLabel(t, "duplicate_cofire_retracted")

		got := w.svc.reattributeByCoverage(w.ctx, det, w.at, w.station, w.self, w.coverage(t, w.self, clip), clip)

		require.True(t, got)
		assert.True(t, w.det(t, det).Retracted, "a row do self (duplicata) é retratada")
		assert.Equal(t, w.self, w.det(t, det).CommercialID, "e NÃO é reatribuída (senão contaria 2×)")
		assert.False(t, w.det(t, winner).Retracted, "o vencedor segue contando")
		assert.InDelta(t, before+1, covEMetricLabel(t, "duplicate_cofire_retracted"), 0.001)
	})

	t.Run("vencedor retraído porém com evidência: restaura o vencedor e retrata o self", func(t *testing.T) {
		w, clip := covESubsetWorld(t, covEWorldOpts{})
		det := w.seedSelfDet(t, covEDetOpts{})
		winner := w.seedSibDet(t, -3*time.Second, covEDetOpts{Status: "available", Retracted: true})

		got := w.svc.reattributeByCoverage(w.ctx, det, w.at, w.station, w.self, w.coverage(t, w.self, clip), clip)

		require.True(t, got)
		assert.False(t, w.det(t, winner).Retracted, "vencedor des-retratado")
		assert.True(t, w.det(t, det).Retracted, "self retratado")
	})

	t.Run("vencedor presente mas rejeitado (não conta): reatribui o self pra não zerar a tocada", func(t *testing.T) {
		w, clip := covESubsetWorld(t, covEWorldOpts{})
		det := w.seedSelfDet(t, covEDetOpts{})
		w.seedSibDet(t, 2*time.Second, covEDetOpts{Status: "audit_rejected"})

		got := w.svc.reattributeByCoverage(w.ctx, det, w.at, w.station, w.self, w.coverage(t, w.self, clip), clip)

		require.True(t, got)
		d := w.det(t, det)
		assert.False(t, d.Retracted)
		assert.Equal(t, w.sib, d.CommercialID, "a veiculação passa a contar uma vez, no corte certo")
	})
}

func TestCovE_ReattributeByCoverage_WinnerWithoutLiveCampaignLeavesAsIs(t *testing.T) {
	w, clip := covESubsetWorld(t, covEWorldOpts{NoLinkSib: true})
	det := w.seedSelfDet(t, covEDetOpts{})
	got := w.svc.reattributeByCoverage(w.ctx, det, w.at, w.station, w.self, w.coverage(t, w.self, clip), clip)
	assert.False(t, got, "sem campanha viva pro vencedor não se inventa detecção")
	assert.Equal(t, w.self, w.det(t, det).CommercialID)
}

// ───────────────────── recoverRejectedByCoverage (reject-path v2c) ─────────────────────

// Cortes sem relação de áudio: o clipe é o do 15s; o 30s (atribuído) o rejeita.
func covERejectWorld(t *testing.T, o covEWorldOpts) (*covEWorld, []float32) {
	t.Helper()
	clip := covESynth(302, 15)
	o.SelfPCM, o.SibPCM = covESynth(301, 30), clip
	o.SelfDur, o.SibDur = 30, 15
	return covENewWorld(t, o), clip
}

func TestCovE_RecoverRejectedByCoverage(t *testing.T) {
	t.Run("irmão suprimido (sem row): reatribui a row rejeitada pro corte que o clipe cobre", func(t *testing.T) {
		w, clip := covERejectWorld(t, covEWorldOpts{ByCoverage: true})
		det := w.seedSelfDet(t, covEDetOpts{Status: "audit_rejected"})
		before := covEMetricLabel(t, "reattributed_on_reject")

		w.svc.recoverRejectedByCoverage(w.ctx, det, w.at, w.station, w.self, w.coverage(t, w.self, clip), clip)

		d := w.det(t, det)
		assert.Equal(t, w.sib, d.CommercialID)
		assert.Equal(t, "missing", d.Status, "veiculação válida sem áudio (vira 'missing', conta nos agregados)")
		require.NotNil(t, d.Cov)
		assert.GreaterOrEqual(t, *d.Cov, 0.9)
		assert.InDelta(t, before+1, covEMetricLabel(t, "reattributed_on_reject"), 0.001)
	})

	t.Run("irmão retraído com evidência: só des-retrata", func(t *testing.T) {
		w, clip := covERejectWorld(t, covEWorldOpts{ByCoverage: true})
		det := w.seedSelfDet(t, covEDetOpts{Status: "audit_rejected"})
		winner := w.seedSibDet(t, 3*time.Second, covEDetOpts{Status: "available", Retracted: true})
		before := covEMetricLabel(t, "restored_on_reject")

		w.svc.recoverRejectedByCoverage(w.ctx, det, w.at, w.station, w.self, w.coverage(t, w.self, clip), clip)

		assert.False(t, w.det(t, winner).Retracted)
		assert.Equal(t, "audit_rejected", w.det(t, det).Status, "a row rejeitada fica como está")
		assert.Equal(t, w.self, w.det(t, det).CommercialID)
		assert.InDelta(t, before+1, covEMetricLabel(t, "restored_on_reject"), 0.001)
	})

	t.Run("irmão já contado: não mexe em nada", func(t *testing.T) {
		w, clip := covERejectWorld(t, covEWorldOpts{ByCoverage: true})
		det := w.seedSelfDet(t, covEDetOpts{Status: "audit_rejected"})
		winner := w.seedSibDet(t, -2*time.Second, covEDetOpts{Status: "available"})

		w.svc.recoverRejectedByCoverage(w.ctx, det, w.at, w.station, w.self, w.coverage(t, w.self, clip), clip)

		assert.Equal(t, "audit_rejected", w.det(t, det).Status)
		assert.Equal(t, w.self, w.det(t, det).CommercialID)
		assert.False(t, w.det(t, winner).Retracted)
	})

	t.Run("clipe sem relação com nenhum corte: a rejeição procede", func(t *testing.T) {
		w, _ := covERejectWorld(t, covEWorldOpts{ByCoverage: true})
		other := covESynth(399, 15)
		det := w.seedSelfDet(t, covEDetOpts{Status: "audit_rejected"})
		w.svc.recoverRejectedByCoverage(w.ctx, det, w.at, w.station, w.self, w.coverage(t, w.self, other), other)
		assert.Equal(t, "audit_rejected", w.det(t, det).Status)
		assert.Equal(t, w.self, w.det(t, det).CommercialID)
	})

	t.Run("cobertura do vencedor abaixo do piso do audit não reatribui (sem chutar)", func(t *testing.T) {
		w, _ := covERejectWorld(t, covEWorldOpts{ByCoverage: true})
		short := covESynth(302, 15)[:int(1.5*16000)] // ~5% do corte de 15s
		det := w.seedSelfDet(t, covEDetOpts{Status: "audit_rejected"})
		sibCov := w.coverage(t, w.sib, short)
		require.Greater(t, sibCov, 0.0)
		require.Less(t, sibCov, audit.DefaultMinCoverage, "pré-condição: o clipe curto cobre pouco")

		w.svc.recoverRejectedByCoverage(w.ctx, det, w.at, w.station, w.self, 0.0, short)

		assert.Equal(t, "audit_rejected", w.det(t, det).Status)
		assert.Equal(t, w.self, w.det(t, det).CommercialID)
	})

	t.Run("vencedor sem campanha viva: deixa rejeitada", func(t *testing.T) {
		w, clip := covERejectWorld(t, covEWorldOpts{ByCoverage: true, NoLinkSib: true})
		det := w.seedSelfDet(t, covEDetOpts{Status: "audit_rejected"})
		w.svc.recoverRejectedByCoverage(w.ctx, det, w.at, w.station, w.self, w.coverage(t, w.self, clip), clip)
		assert.Equal(t, "audit_rejected", w.det(t, det).Status)
		assert.Equal(t, w.self, w.det(t, det).CommercialID)
	})

	t.Run("row que já não está rejeitada: ErrReattributeNoRow, nada muda", func(t *testing.T) {
		w, clip := covERejectWorld(t, covEWorldOpts{ByCoverage: true})
		det := w.seedSelfDet(t, covEDetOpts{Status: "available"}) // outro caminho já a aprovou
		w.svc.recoverRejectedByCoverage(w.ctx, det, w.at, w.station, w.self, w.coverage(t, w.self, clip), clip)
		assert.Equal(t, "available", w.det(t, det).Status)
		assert.Equal(t, w.self, w.det(t, det).CommercialID)
	})

	t.Run("sem irmãos: no-op", func(t *testing.T) {
		w, clip := covERejectWorld(t, covEWorldOpts{ByCoverage: true})
		det := w.seedSelfDet(t, covEDetOpts{Status: "audit_rejected"})
		w.svc.recoverRejectedByCoverage(w.ctx, det, w.at, w.station, uuid.New(), 0.0, clip)
		assert.Equal(t, "audit_rejected", w.det(t, det).Status)
	})

	t.Run("falha na busca de irmãos: no-op", func(t *testing.T) {
		w, clip := covERejectWorld(t, covEWorldOpts{ByCoverage: true})
		det := w.seedSelfDet(t, covEDetOpts{Status: "audit_rejected"})
		broken := NewService(w.pool, nil, nil, catalog.NewDetections(covEClosedPool(t)), nil,
			w.aud, true, false, false, false, zap.NewNop())
		broken.recoverRejectedByCoverage(w.ctx, det, w.at, w.station, w.self, 0.0, clip)
		assert.Equal(t, "audit_rejected", w.det(t, det).Status)
	})
}

// ───────────────────── pipeline completo: runAuditOrReject × disambiguação ─────────────────────

func TestCovE_Pipeline_PassPathReattributesByCoverageAndStillUploads(t *testing.T) {
	// Master atribuído = [áudio A 15s][áudio B 15s]; o clipe é só o A (15s).
	a, b := covESynth(311, 15), covESynth(312, 15)
	p := covENewPipe(t, covEPipeOpts{Auditor: true, ByCoverage: true})
	// substitui o master atribuído por [A][B] e cria o irmão de 15s (= A) ligado à campanha
	_, err := p.pool.Exec(p.ctx, `DELETE FROM fingerprint_hashes WHERE commercial_id = $1`, p.material)
	require.NoError(t, err)
	covEInsertHashes(t, p.ctx, p.pool, p.material, covEConcat(a, b))
	sib, sibShort := covESeedMaterial(t, p.ctx, p.pool, p.client, "Corte 15s", 15, "", a)
	covELink(t, p.ctx, p.pool, p.campaign, sib, p.station)
	p.registerSegments(t)
	covEServePCM(t, a)
	before := covEMetricLabel(t, "reattributed_by_coverage")

	p.fire(t)
	id, det := p.waitSettled(t)

	assert.Equal(t, "available", det.Status, "reatribuir não bloqueia o upload")
	assert.Equal(t, sib, det.CommercialID, "linha corrigida pro corte de 15s (short %d)", sibShort)
	assert.True(t, p.s3.Has(covEBucket, p.expectedKey(id)))
	assert.InDelta(t, before+1, covEMetricLabel(t, "reattributed_by_coverage"), 0.001)
}

func TestCovE_Pipeline_RejectPathRecovers(t *testing.T) {
	clip := covESynth(322, 15)

	t.Run("v2c: sem row irmã, a rejeitada é reatribuída ao corte que o clipe cobre", func(t *testing.T) {
		p := covENewPipe(t, covEPipeOpts{Auditor: true, ByCoverage: true})
		sib, _ := covESeedMaterial(t, p.ctx, p.pool, p.client, "Corte 15s", 15, "", clip)
		covELink(t, p.ctx, p.pool, p.campaign, sib, p.station)
		p.registerSegments(t)
		covEServePCM(t, clip)

		p.fire(t)
		_, det := p.waitSettled(t)
		// a goroutine marca audit_rejected e DEPOIS recupera: espera o desfecho final
		covEEventually(t, func() bool {
			id, _ := p.detectionID(t)
			return covEReadDet(t, p.ctx, p.pool, id).Status == "missing"
		}, "v2c reatribui a row rejeitada")
		_ = det

		id, _ := p.detectionID(t)
		d := covEReadDet(t, p.ctx, p.pool, id)
		assert.Equal(t, sib, d.CommercialID)
		assert.Zero(t, p.s3.Count(), "o clipe rejeitado nunca sobe")
	})

	t.Run("v2b: irmão curto retratado pela v1 é restaurado", func(t *testing.T) {
		p := covENewPipe(t, covEPipeOpts{Auditor: true, ByCoverage: true})
		sib, _ := covESeedMaterial(t, p.ctx, p.pool, p.client, "Corte 15s", 15, "", clip)
		covELink(t, p.ctx, p.pool, p.campaign, sib, p.station)
		displaced := covESeedDet(t, p.ctx, p.pool, p.campaign, sib, p.station, p.detectedAt.Add(3*time.Second),
			covEDetOpts{Status: "available", Retracted: true})
		p.registerSegments(t)
		covEServePCM(t, clip)
		before := covEMetricLabel(t, "restored_on_reject")

		p.fire(t)
		covEEventually(t, func() bool {
			return !covEReadDet(t, p.ctx, p.pool, displaced).Retracted
		}, "o corte curto deslocado deve ser des-retratado")

		id, _ := p.detectionID(t)
		_ = id
		var rejected uuid.UUID
		require.NoError(t, p.pool.QueryRow(p.ctx,
			`SELECT id FROM detections WHERE station_id = $1 AND id <> $2`, p.station, displaced).Scan(&rejected))
		assert.Equal(t, "audit_rejected", covEReadDet(t, p.ctx, p.pool, rejected).Status,
			"o corte longo segue rejeitado; a veiculação é contada via corte curto restaurado")
		assert.InDelta(t, before+1, covEMetricLabel(t, "restored_on_reject"), 0.001)
	})

	t.Run("sem ByCoverage a rejeição é definitiva (comportamento pré-v2)", func(t *testing.T) {
		p := covENewPipe(t, covEPipeOpts{Auditor: true, ByCoverage: false})
		sib, _ := covESeedMaterial(t, p.ctx, p.pool, p.client, "Corte 15s", 15, "", clip)
		covELink(t, p.ctx, p.pool, p.campaign, sib, p.station)
		p.registerSegments(t)
		covEServePCM(t, clip)
		p.fire(t)
		_, det := p.waitSettled(t)
		assert.Equal(t, "audit_rejected", det.Status)
		assert.Equal(t, p.material, det.CommercialID, "sem a flag não há reatribuição")
	})
}
