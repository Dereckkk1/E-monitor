package evidence

import (
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/audit"
	"radiocheck/internal/catalog"
)

// covETwinWorld: dois materiais de MESMA duração (30s) que dividem os últimos 17s
// e só diferem nos primeiros 13s — gêmeos acústicos. A região discriminante de
// cada um é o início (frames [0,95)), onde o outro não casa.
type covETwinWorld struct {
	*covEWorld
	pcmX, pcmY []float32
	twins      *catalog.TwinDiscriminative
}

func covENewTwinWorld(t *testing.T, o covEWorldOpts) *covETwinWorld {
	t.Helper()
	shared := covESynth(403, 17)
	pcmX := covEConcat(covESynth(401, 13), shared)
	pcmY := covEConcat(covESynth(402, 13), shared)
	o.SelfPCM, o.SibPCM = pcmX, pcmY
	o.SelfDur, o.SibDur = 30, 30
	o.Twin = true
	w := covENewWorld(t, o)
	tw := &covETwinWorld{covEWorld: w, pcmX: pcmX, pcmY: pcmY, twins: catalog.NewTwinDiscriminative(w.pool)}
	return tw
}

// link grava as duas linhas (X→Y e Y→X) da região discriminante.
func (w *covETwinWorld) link(t *testing.T) {
	t.Helper()
	disc := []audit.FrameRange{{Lo: 0, Hi: 95}}
	require.NoError(t, w.twins.Upsert(w.ctx, w.self, w.sib, disc, 95))
	require.NoError(t, w.twins.Upsert(w.ctx, w.sib, w.self, disc, 95))
}

// ───────────────────── evaluateTwins / Preview ─────────────────────

func TestCovE_EvaluateTwins_VerdictsFollowTheDiscriminativeRegion(t *testing.T) {
	w := covENewTwinWorld(t, covEWorldOpts{})
	w.link(t)

	t.Run("clipe do gêmeo: reatribui pro gêmeo", func(t *testing.T) {
		action, winner, evals, ok := w.svc.evaluateTwins(w.ctx, w.self, w.pcmY)
		require.True(t, ok)
		assert.Equal(t, verdictReattribute, action)
		require.NotNil(t, winner)
		assert.Equal(t, w.sib, winner.twinID)
		assert.Equal(t, w.sibShrt, winner.twinShortID)
		require.Len(t, evals, 1)
		assert.GreaterOrEqual(t, winner.discCov, 0.8, "o clipe cobre a assinatura do gêmeo")
		assert.LessOrEqual(t, winner.covSelf, 0.2, "e quase nada da assinatura do atribuído")
		assert.GreaterOrEqual(t, winner.fullCov, 0.5)
	})

	t.Run("clipe do próprio material: mantém", func(t *testing.T) {
		action, winner, evals, ok := w.svc.evaluateTwins(w.ctx, w.self, w.pcmX)
		require.True(t, ok)
		assert.Equal(t, verdictKeep, action)
		assert.Nil(t, winner)
		require.Len(t, evals, 1)
		assert.GreaterOrEqual(t, evals[0].covSelf, 0.8)
		assert.LessOrEqual(t, evals[0].discCov, 0.2)
	})

	t.Run("clipe que não contém a assinatura de nenhum: ambíguo (nunca chuta)", func(t *testing.T) {
		shared := covESynth(403, 17) // só o trecho comum aos dois
		action, winner, _, ok := w.svc.evaluateTwins(w.ctx, w.self, shared)
		require.True(t, ok)
		assert.Equal(t, verdictAmbiguous, action)
		assert.Nil(t, winner)
	})
}

func TestCovE_EvaluateTwins_SkipsWhenThereIsNothingToDecide(t *testing.T) {
	t.Run("material sem gêmeos populados", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{}) // sem link()
		_, _, evals, ok := w.svc.evaluateTwins(w.ctx, w.self, w.pcmX)
		assert.False(t, ok)
		assert.Nil(t, evals)
	})

	t.Run("auditoria do próprio material falha (sem master no banco)", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{NoSelfHashes: true})
		w.link(t)
		_, _, _, ok := w.svc.evaluateTwins(w.ctx, w.self, w.pcmX)
		assert.False(t, ok)
	})

	t.Run("auditoria do gêmeo falha: sem avaliações, sem decisão", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{NoSibHashes: true})
		w.link(t)
		_, _, evals, ok := w.svc.evaluateTwins(w.ctx, w.self, w.pcmX)
		assert.False(t, ok)
		assert.Nil(t, evals)
	})

	t.Run("falha ao listar gêmeos (banco indisponível)", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{})
		w.link(t)
		broken := NewService(covEClosedPool(t), nil, nil, nil, nil, w.aud, true, true, false, false, zap.NewNop())
		_, _, _, ok := broken.evaluateTwins(w.ctx, w.self, w.pcmX)
		assert.False(t, ok)
	})

	t.Run("região reversa ausente conta como 0: ambíguo", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{})
		require.NoError(t, w.twins.Upsert(w.ctx, w.self, w.sib, []audit.FrameRange{{Lo: 0, Hi: 95}}, 95)) // só X→Y
		action, winner, evals, ok := w.svc.evaluateTwins(w.ctx, w.self, w.pcmY)
		require.True(t, ok)
		assert.Equal(t, verdictAmbiguous, action, "sem a região do gêmeo não há sinal pra reatribuir")
		assert.Nil(t, winner)
		require.Len(t, evals, 1)
		assert.Zero(t, evals[0].discCov)
	})
}

func TestCovE_PreviewTwinDecision(t *testing.T) {
	w := covENewTwinWorld(t, covEWorldOpts{})
	w.link(t)
	det := w.seedSelfDet(t, covEDetOpts{})

	t.Run("reatribuir: devolve vencedor e detalhe, sem escrever nada", func(t *testing.T) {
		p := w.svc.PreviewTwinDecision(w.ctx, w.self, w.pcmY)
		assert.Equal(t, "reattribute", p.Action)
		assert.Equal(t, w.sibShrt, p.WinnerShortID)
		assert.GreaterOrEqual(t, p.WinnerCov, 0.8)
		assert.Equal(t, 1, p.Twins)
		assert.Contains(t, p.Detail, "sid="+strconv.Itoa(int(w.sibShrt)))
		assert.Contains(t, p.Detail, "->reattribute")
		assert.Equal(t, w.self, w.det(t, det).CommercialID, "preview é read-only")
	})

	t.Run("manter", func(t *testing.T) {
		p := w.svc.PreviewTwinDecision(w.ctx, w.self, w.pcmX)
		assert.Equal(t, "keep", p.Action)
		assert.Zero(t, p.WinnerShortID)
		assert.Contains(t, p.Detail, "->keep")
	})

	t.Run("ambíguo", func(t *testing.T) {
		p := w.svc.PreviewTwinDecision(w.ctx, w.self, covESynth(403, 17))
		assert.Equal(t, "ambiguous", p.Action)
		assert.Contains(t, p.Detail, "->ambiguous")
	})

	t.Run("sem gêmeos: skip", func(t *testing.T) {
		p := w.svc.PreviewTwinDecision(w.ctx, uuid.New(), w.pcmX)
		assert.Equal(t, TwinPreview{Action: "skip"}, p)
	})
}

// ───────────────────── disambiguateTwin (aplica) ─────────────────────

func TestCovE_DisambiguateTwin_Applies(t *testing.T) {
	t.Run("clipe do gêmeo reatribui a detecção e acerta a projeção", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{})
		w.link(t)
		det := w.seedSelfDet(t, covEDetOpts{})
		before := covEMetricLabel(t, "reattributed_by_discriminative")

		got := w.svc.disambiguateTwin(w.ctx, det, w.at, w.station, w.self, w.pcmY)

		assert.Equal(t, "reattribute->sid="+strconv.Itoa(int(w.sibShrt)), got)
		d := w.det(t, det)
		assert.Equal(t, w.sib, d.CommercialID)
		require.NotNil(t, d.Cov)
		assert.GreaterOrEqual(t, *d.Cov, 0.5, "audit_coverage = cobertura cheia do gêmeo vencedor")
		assert.Equal(t, map[uuid.UUID]uuid.UUID{w.camp: w.sib}, covEProjections(t, w.ctx, w.pool, det))
		assert.InDelta(t, before+1, covEMetricLabel(t, "reattributed_by_discriminative"), 0.001)
	})

	t.Run("clipe do próprio material mantém", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{})
		w.link(t)
		det := w.seedSelfDet(t, covEDetOpts{})
		before := covEMetricLabel(t, "kept_by_discriminative")
		got := w.svc.disambiguateTwin(w.ctx, det, w.at, w.station, w.self, w.pcmX)
		assert.Equal(t, "keep", got)
		assert.Equal(t, w.self, w.det(t, det).CommercialID)
		assert.InDelta(t, before+1, covEMetricLabel(t, "kept_by_discriminative"), 0.001)
	})

	t.Run("sem gêmeos: skip", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{})
		det := w.seedSelfDet(t, covEDetOpts{})
		assert.Equal(t, "skip", w.svc.disambiguateTwin(w.ctx, det, w.at, w.station, w.self, w.pcmX))
	})

	t.Run("ambíguo com retração DESLIGADA preserva a atribuição (não sub-conta)", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{TwinRetract: false})
		w.link(t)
		det := w.seedSelfDet(t, covEDetOpts{})
		before := covEMetricLabel(t, "ambiguous_kept")
		got := w.svc.disambiguateTwin(w.ctx, det, w.at, w.station, w.self, covESynth(403, 17))
		assert.Equal(t, "ambiguous(kept)", got)
		d := w.det(t, det)
		assert.False(t, d.Retracted)
		assert.Equal(t, "available", d.Status)
		assert.InDelta(t, before+1, covEMetricLabel(t, "ambiguous_kept"), 0.001)
	})

	t.Run("ambíguo com retração LIGADA retrata e marca ambiguous", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{TwinRetract: true})
		w.link(t)
		det := w.seedSelfDet(t, covEDetOpts{})
		before := covEMetricLabel(t, "ambiguous_by_discriminative")
		got := w.svc.disambiguateTwin(w.ctx, det, w.at, w.station, w.self, covESynth(403, 17))
		assert.Equal(t, "ambiguous", got)
		d := w.det(t, det)
		assert.True(t, d.Retracted, "ambiguous ⟺ retracted")
		assert.Equal(t, "ambiguous", d.Status)
		assert.InDelta(t, before+1, covEMetricLabel(t, "ambiguous_by_discriminative"), 0.001)
	})

	t.Run("falha ao marcar ambíguo devolve error sem alterar a linha", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{TwinRetract: true})
		w.link(t)
		det := w.seedSelfDet(t, covEDetOpts{})
		broken := NewService(w.pool, nil, nil, catalog.NewDetections(covEClosedPool(t)), nil, w.aud, true, true, true, false, zap.NewNop())
		got := broken.disambiguateTwin(w.ctx, det, w.at, w.station, w.self, covESynth(403, 17))
		assert.Equal(t, "error", got)
		assert.False(t, w.det(t, det).Retracted)
	})

	t.Run("RedisambiguateDetection aplica o mesmo fluxo", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{})
		w.link(t)
		det := w.seedSelfDet(t, covEDetOpts{})
		got := w.svc.RedisambiguateDetection(w.ctx, det, w.at, w.station, w.self, w.pcmY)
		assert.Equal(t, "reattribute->sid="+strconv.Itoa(int(w.sibShrt)), got)
		assert.Equal(t, w.sib, w.det(t, det).CommercialID)
	})
}

// ───────────────────── reattributeTwinWithCofireGuard ─────────────────────

func TestCovE_ReattributeTwinWithCofireGuard(t *testing.T) {
	t.Run("gêmeo vencedor já tem a tocada contada: retrata a duplicata", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{})
		det := w.seedSelfDet(t, covEDetOpts{})
		winner := w.seedSibDet(t, 5*time.Second, covEDetOpts{Status: "available"})
		before := covEMetricLabel(t, "duplicate_cofire_retracted")

		w.svc.reattributeTwinWithCofireGuard(w.ctx, det, w.at, w.station, w.sibShrt, 0.9)

		assert.True(t, w.det(t, det).Retracted)
		assert.Equal(t, w.self, w.det(t, det).CommercialID, "não reatribui (contaria 2×)")
		assert.False(t, w.det(t, winner).Retracted)
		assert.InDelta(t, before+1, covEMetricLabel(t, "duplicate_cofire_retracted"), 0.001)
	})

	t.Run("gêmeo vencedor retraído com evidência: restaura e retrata o self", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{})
		det := w.seedSelfDet(t, covEDetOpts{})
		winner := w.seedSibDet(t, -5*time.Second, covEDetOpts{Status: "available", Retracted: true})
		w.svc.reattributeTwinWithCofireGuard(w.ctx, det, w.at, w.station, w.sibShrt, 0.9)
		assert.False(t, w.det(t, winner).Retracted)
		assert.True(t, w.det(t, det).Retracted)
	})

	t.Run("vencedor sem campanha viva: deixa como está", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{NoLinkSib: true})
		det := w.seedSelfDet(t, covEDetOpts{})
		w.svc.reattributeTwinWithCofireGuard(w.ctx, det, w.at, w.station, w.sibShrt, 0.9)
		assert.Equal(t, w.self, w.det(t, det).CommercialID)
		assert.False(t, w.det(t, det).Retracted)
	})

	t.Run("detecção inexistente: a reatribuição falha e nada é gravado", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{})
		assert.NotPanics(t, func() {
			w.svc.reattributeTwinWithCofireGuard(w.ctx, uuid.New(), w.at, w.station, w.sibShrt, 0.9)
		})
	})

	t.Run("falha na busca da row do vencedor: no-op", func(t *testing.T) {
		w := covENewTwinWorld(t, covEWorldOpts{})
		det := w.seedSelfDet(t, covEDetOpts{})
		broken := NewService(w.pool, nil, nil, catalog.NewDetections(covEClosedPool(t)), nil, w.aud, true, true, false, false, zap.NewNop())
		broken.reattributeTwinWithCofireGuard(w.ctx, det, w.at, w.station, w.sibShrt, 0.9)
		assert.Equal(t, w.self, w.det(t, det).CommercialID)
	})
}

// ───────────────────── pipeline: gêmeos no pass-path do audit ─────────────────────

func TestCovE_Pipeline_PassPathTwinDisambiguationReattributes(t *testing.T) {
	pcmX := covEConcat(covESynth(401, 13), covESynth(403, 17))
	pcmY := covEConcat(covESynth(402, 13), covESynth(403, 17))

	p := covENewPipe(t, covEPipeOpts{Auditor: true, Twin: true})
	_, err := p.pool.Exec(p.ctx, `DELETE FROM fingerprint_hashes WHERE commercial_id = $1`, p.material)
	require.NoError(t, err)
	covEInsertHashes(t, p.ctx, p.pool, p.material, pcmX) // material atribuído = X
	twinY, twinShort := covESeedMaterial(t, p.ctx, p.pool, p.client, "Gêmeo Y", 30, "", pcmY)
	covELink(t, p.ctx, p.pool, p.campaign, twinY, p.station)
	repo := catalog.NewTwinDiscriminative(p.pool)
	disc := []audit.FrameRange{{Lo: 0, Hi: 95}}
	require.NoError(t, repo.Upsert(p.ctx, p.material, twinY, disc, 95))
	require.NoError(t, repo.Upsert(p.ctx, twinY, p.material, disc, 95))
	p.registerSegments(t)
	covEServePCM(t, pcmY) // quem tocou foi o gêmeo Y, mas o matcher ao vivo creditou X

	p.fire(t)
	id, det := p.waitSettled(t)
	require.Equal(t, "available", det.Status)
	covEEventually(t, func() bool {
		return covEReadDet(t, p.ctx, p.pool, id).CommercialID == twinY
	}, "a desambiguação de gêmeos deve re-apontar a linha pro gêmeo Y (short "+strconv.Itoa(int(twinShort))+")")
}
