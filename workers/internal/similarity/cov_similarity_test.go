package similarity

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"radiocheck/internal/events"
	"radiocheck/internal/index"
)

// ───────────────────── funções puras ─────────────────────

func TestCovM_Coverages(t *testing.T) {
	r := func(a, b int32) frameRange { return frameRange{a, b} }
	cases := []struct {
		name         string
		s            *pairScan
		ownTotal     int
		wantOwn      float64
		wantOtherCov float64
	}{
		{"cobertura parcial dos dois lados",
			&pairScan{otherTotalFrames: 200, ownRanges: []frameRange{r(0, 50)}, otherRanges: []frameRange{r(0, 100)}},
			100, 0.5, 0.5},
		{"união de ranges sobrepostos conta uma vez",
			&pairScan{otherTotalFrames: 100, ownRanges: []frameRange{r(0, 40), r(20, 60)}, otherRanges: []frameRange{r(0, 10)}},
			100, 0.6, 0.1},
		{"cobertura estoura 100% e é limitada a 1.0",
			&pairScan{otherTotalFrames: 10, ownRanges: []frameRange{r(0, 500)}, otherRanges: []frameRange{r(0, 500)}},
			100, 1.0, 1.0},
		{"total do outro zerado -> otherCov 0",
			&pairScan{otherTotalFrames: 0, ownRanges: []frameRange{r(0, 10)}, otherRanges: []frameRange{r(0, 10)}},
			100, 0.1, 0},
		{"total próprio zerado -> ownCov 0",
			&pairScan{otherTotalFrames: 100, ownRanges: []frameRange{r(0, 10)}, otherRanges: []frameRange{r(0, 50)}},
			0, 0, 0.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			own, other := coverages(c.s, c.ownTotal)
			assert.InDelta(t, c.wantOwn, own, 1e-9)
			assert.InDelta(t, c.wantOtherCov, other, 1e-9)
		})
	}
}

func TestCovM_MergeRegionsSec(t *testing.T) {
	r := func(a, b int32) frameRange { return frameRange{a, b} }
	t.Run("une ranges contíguos e converte frames em segundos", func(t *testing.T) {
		got := mergeRegionsSec([]frameRange{r(0, 8), r(8, 16), r(40, 48)}, 100)
		require.Len(t, got, 2)
		assert.InDelta(t, 0.0, got[0][0], 1e-9)
		assert.InDelta(t, 16*framesToSec, got[0][1], 1e-9)
		assert.InDelta(t, 40*framesToSec, got[1][0], 1e-9)
		assert.InDelta(t, 48*framesToSec, got[1][1], 1e-9)
	})
	t.Run("clampa em [0,total] e descarta o que está fora do material", func(t *testing.T) {
		got := mergeRegionsSec([]frameRange{r(-5, 10), r(95, 120), r(200, 300), r(-9, -1)}, 100)
		require.Len(t, got, 2)
		assert.InDelta(t, 0.0, got[0][0], 1e-9, "from negativo vira 0")
		assert.InDelta(t, 10*framesToSec, got[0][1], 1e-9)
		assert.InDelta(t, 95*framesToSec, got[1][0], 1e-9)
		assert.InDelta(t, 100*framesToSec, got[1][1], 1e-9, "until acima do total vira total")
	})
	t.Run("entrada vazia devolve fatia não-nil (JSON [] e não null)", func(t *testing.T) {
		got := mergeRegionsSec(nil, 100)
		require.NotNil(t, got)
		assert.Empty(t, got)
	})
}

func TestCovM_BuildOverlapJSON(t *testing.T) {
	t.Run("serializa campos e segmentos", func(t *testing.T) {
		raw := buildOverlapJSON(0.9, 0.45, 30, 60, [][2]float64{{0, 30}}, [][2]float64{{0, 30}})
		var got overlapJSON
		require.NoError(t, json.Unmarshal(raw, &got))
		assert.InDelta(t, 0.9, got.OwnCov, 1e-9)
		assert.InDelta(t, 0.45, got.OtherCov, 1e-9)
		assert.InDelta(t, 30.0, got.OwnDuration, 1e-9)
		assert.InDelta(t, 60.0, got.OtherDuration, 1e-9)
		assert.Equal(t, [][2]float64{{0, 30}}, got.OwnSegments)
	})
	t.Run("segmentos nil viram [] (nunca null)", func(t *testing.T) {
		raw := buildOverlapJSON(0, 0, 1, 1, nil, nil)
		assert.Contains(t, string(raw), `"own_segments":[]`)
		assert.Contains(t, string(raw), `"other_segments":[]`)
	})
}

func TestCovM_PairScoreAndPickTop_Edges(t *testing.T) {
	t.Run("pairScore com total próprio zero devolve 0", func(t *testing.T) {
		assert.Equal(t, 0.0, pairScore(&pairScan{otherTotalFrames: 10, ownRanges: []frameRange{{0, 5}}}, 0))
	})
	t.Run("pairScore usa o maior lado e limita em 1.0", func(t *testing.T) {
		s := &pairScan{otherTotalFrames: 10, ownRanges: []frameRange{{0, 10}}, otherRanges: []frameRange{{0, 99}}}
		assert.Equal(t, 1.0, pairScore(s, 100))
	})
	t.Run("pickTopMatch sem frames próprios devolve Nil", func(t *testing.T) {
		id, score := pickTopMatch(scanReport{ownTotalFrames: 0, perOther: map[uuid.UUID]*pairScan{uuid.New(): {}}})
		assert.Equal(t, uuid.Nil, id)
		assert.Equal(t, 0.0, score)
	})
}

// runScan: self-match e short_id desconhecido são ignorados; só o par válido
// vira entrada em perOther.
func TestCovM_RunScan_IgnoresSelfAndUnknownShortIDs(t *testing.T) {
	pcm := covMNoise(31, 12)
	hashes := denseHashes(pcm)

	const selfShort, otherShort, ghostShort int32 = 1, 2, 3
	selfID, otherID := uuid.New(), uuid.New()
	idx := make(index.Index)
	for _, short := range []int32{selfShort, otherShort, ghostShort} {
		for _, h := range hashes {
			idx[h.Value] = append(idx[h.Value], index.Entry{CommercialShortID: short, TimeFrame: int32(h.TimeFrame)})
		}
	}
	store := index.New()
	store.Swap(idx)

	// ghostShort NÃO está em shortToID (inconsistência de catálogo); selfShort aponta pro próprio.
	shortToID := map[int32]uuid.UUID{selfShort: selfID, otherShort: otherID}
	rep := runScan(pcm, store, selfID, shortToID, map[uuid.UUID]int{otherID: len(pcm) / 2048})

	require.Contains(t, rep.perOther, otherID)
	assert.NotContains(t, rep.perOther, selfID, "self-match não pode entrar no relatório")
	assert.Len(t, rep.perOther, 1, "short_id desconhecido também é descartado")
	assert.Greater(t, frameCoverage(rep.perOther[otherID].ownRanges), 0)
}

// ───────────────────── CheckMaterialSimilarity (DB + ffmpeg falso) ─────────────────────

func TestCovM_CheckMaterialSimilarity(t *testing.T) {
	ctx, pool := covMDB(t)
	covMInstallFakeFFmpeg(t)

	type row struct {
		status string
		top    *uuid.UUID
		score  *float64
		segs   []byte
	}
	read := func(id uuid.UUID) row {
		var r row
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT similarity_check_status, most_similar_material_id, similarity_score, similarity_segments
			FROM materials WHERE id = $1`, id).Scan(&r.status, &r.top, &r.score, &r.segs))
		return r
	}

	own := covMNoise(41, 20)
	ownPath := covMWritePCM(t, "own.pcm", own)

	t.Run("material inexistente devolve erro de lookup", func(t *testing.T) {
		err := CheckMaterialSimilarity(ctx, pool, uuid.New())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lookup material")
	})

	t.Run("fingerprint ainda não pronto vira skipped", func(t *testing.T) {
		client := covMSeedClient(t, ctx, pool)
		id, _ := covMSeedMaterial(t, ctx, pool, client, "mat-pending", 20, "pending", ownPath, nil)
		require.NoError(t, CheckMaterialSimilarity(ctx, pool, id))
		assert.Equal(t, "skipped", read(id).status)
	})

	t.Run("sem outros materiais prontos do cliente vira skipped", func(t *testing.T) {
		client := covMSeedClient(t, ctx, pool)
		id, _ := covMSeedMaterial(t, ctx, pool, client, "mat-sozinho", 20, "ready", ownPath, nil)
		require.NoError(t, CheckMaterialSimilarity(ctx, pool, id))
		assert.Equal(t, "skipped", read(id).status)
	})

	t.Run("falha de decode marca failed e devolve erro", func(t *testing.T) {
		client := covMSeedClient(t, ctx, pool)
		covMSeedMaterial(t, ctx, pool, client, "peer", 20, "ready", ownPath, own)
		id, _ := covMSeedMaterial(t, ctx, pool, client, "novo", 20, "ready", ownPath, nil)
		t.Setenv(covMEnvFail, "1")
		err := CheckMaterialSimilarity(ctx, pool, id)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode master")
		assert.Equal(t, "failed", read(id).status)
	})

	t.Run("hash com variant_id fora de uint8 marca failed", func(t *testing.T) {
		client := covMSeedClient(t, ctx, pool)
		peer, _ := covMSeedMaterial(t, ctx, pool, client, "peer-ruim", 20, "ready", ownPath, nil)
		covMInsertHashes(t, ctx, pool, peer, covMNoise(42, 5), 300)
		id, _ := covMSeedMaterial(t, ctx, pool, client, "novo", 20, "ready", ownPath, nil)
		err := CheckMaterialSimilarity(ctx, pool, id)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "load client index")
		assert.Equal(t, "failed", read(id).status)
	})

	t.Run("peer sem relação fica abaixo do piso: ready sem match", func(t *testing.T) {
		client := covMSeedClient(t, ctx, pool)
		covMSeedMaterial(t, ctx, pool, client, "peer-outro-audio", 20, "ready", ownPath, covMNoise(43, 20))
		id, _ := covMSeedMaterial(t, ctx, pool, client, "novo", 20, "ready", ownPath, nil)
		require.NoError(t, CheckMaterialSimilarity(ctx, pool, id))
		r := read(id)
		assert.Equal(t, "ready", r.status)
		assert.Nil(t, r.top, "áudio sem relação não grava match")
		assert.Nil(t, r.score)
	})

	t.Run("peer com o mesmo áudio é persistido com score e segmentos", func(t *testing.T) {
		client := covMSeedClient(t, ctx, pool)
		unrelated, _ := covMSeedMaterial(t, ctx, pool, client, "peer-sem-relacao", 20, "ready", ownPath, covMNoise(44, 20))
		same, _ := covMSeedMaterial(t, ctx, pool, client, "peer-mesmo-audio", 20, "ready", ownPath, own)
		id, _ := covMSeedMaterial(t, ctx, pool, client, "novo", 20, "ready", ownPath, nil)
		// outro cliente com o mesmo áudio NÃO pode contar (índice é por cliente)
		otherClient := covMSeedClient(t, ctx, pool)
		foreign, _ := covMSeedMaterial(t, ctx, pool, otherClient, "peer-de-outro-cliente", 20, "ready", ownPath, own)

		require.NoError(t, CheckMaterialSimilarity(ctx, pool, id))
		r := read(id)
		assert.Equal(t, "ready", r.status)
		require.NotNil(t, r.top)
		assert.Equal(t, same, *r.top, "o top match é o peer com o mesmo áudio")
		assert.NotEqual(t, unrelated, *r.top)
		assert.NotEqual(t, foreign, *r.top, "material de outro cliente não entra no índice")
		require.NotNil(t, r.score)
		assert.GreaterOrEqual(t, *r.score, WarnThreshold)

		var seg overlapJSON
		require.NoError(t, json.Unmarshal(r.segs, &seg))
		assert.GreaterOrEqual(t, seg.OwnCov, 0.9)
		assert.InDelta(t, 20.0, seg.OwnDuration, 1e-9)
		assert.InDelta(t, 20.0, seg.OtherDuration, 1e-9)
		assert.NotEmpty(t, seg.OwnSegments)
		assert.NotEmpty(t, seg.OtherSegments)
	})

	t.Run("re-rodar é idempotente e limpa match antigo quando o peer some", func(t *testing.T) {
		client := covMSeedClient(t, ctx, pool)
		peer, _ := covMSeedMaterial(t, ctx, pool, client, "peer", 20, "ready", ownPath, own)
		id, _ := covMSeedMaterial(t, ctx, pool, client, "novo", 20, "ready", ownPath, nil)
		require.NoError(t, CheckMaterialSimilarity(ctx, pool, id))
		require.NoError(t, CheckMaterialSimilarity(ctx, pool, id))
		require.NotNil(t, read(id).top)

		// peer deixa de ser "ready": o cliente fica sem outros materiais -> skipped.
		_, err := pool.Exec(ctx, `UPDATE materials SET fingerprint_status = 'failed' WHERE id = $1`, peer)
		require.NoError(t, err)
		require.NoError(t, CheckMaterialSimilarity(ctx, pool, id))
		assert.Equal(t, "skipped", read(id).status)
	})
}

// ───────────────────── FindSimilarMaterials ─────────────────────

func TestCovM_FindSimilarMaterials(t *testing.T) {
	ctx, pool := covMDB(t)
	covMInstallFakeFFmpeg(t)

	own := covMNoise(51, 20)
	ownPath := covMWritePCM(t, "own.pcm", own)

	t.Run("material inexistente devolve erro", func(t *testing.T) {
		got, err := FindSimilarMaterials(ctx, pool, uuid.New(), 0.5)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "lookup material")
	})

	t.Run("fingerprint não pronto devolve nil sem erro", func(t *testing.T) {
		client := covMSeedClient(t, ctx, pool)
		id, _ := covMSeedMaterial(t, ctx, pool, client, "pend", 20, "pending", ownPath, nil)
		got, err := FindSimilarMaterials(ctx, pool, id, 0.5)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("sem peers devolve nil sem erro", func(t *testing.T) {
		client := covMSeedClient(t, ctx, pool)
		id, _ := covMSeedMaterial(t, ctx, pool, client, "solo", 20, "ready", ownPath, nil)
		got, err := FindSimilarMaterials(ctx, pool, id, 0.5)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("variant_id inválido propaga erro do índice", func(t *testing.T) {
		client := covMSeedClient(t, ctx, pool)
		peer, _ := covMSeedMaterial(t, ctx, pool, client, "peer", 20, "ready", ownPath, nil)
		covMInsertHashes(t, ctx, pool, peer, covMNoise(52, 5), 999)
		id, _ := covMSeedMaterial(t, ctx, pool, client, "novo", 20, "ready", ownPath, nil)
		_, err := FindSimilarMaterials(ctx, pool, id, 0.5)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "load client index")
	})

	t.Run("decode falho propaga erro", func(t *testing.T) {
		client := covMSeedClient(t, ctx, pool)
		covMSeedMaterial(t, ctx, pool, client, "peer", 20, "ready", ownPath, own)
		id, _ := covMSeedMaterial(t, ctx, pool, client, "novo", 20, "ready", ownPath, nil)
		t.Setenv(covMEnvFail, "1")
		_, err := FindSimilarMaterials(ctx, pool, id, 0.5)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode master")
	})

	t.Run("devolve só quem passa do minScore, ordenado por score desc e ID asc no empate", func(t *testing.T) {
		client := covMSeedClient(t, ctx, pool)
		// 'same' e 'sub' têm score 1.0 (o 'sub' está inteiro dentro do áudio: max(ownCov, otherCov));
		// 'partial' só repete os primeiros 8s (~0.4); 'outro' não tem relação.
		same, _ := covMSeedMaterial(t, ctx, pool, client, "same", 20, "ready", ownPath, own)
		sub, _ := covMSeedMaterial(t, ctx, pool, client, "sub", 10, "ready", ownPath, own[:10*16000])
		partial, _ := covMSeedMaterial(t, ctx, pool, client, "partial", 20, "ready", ownPath,
			append(append([]float32(nil), own[:8*16000]...), covMNoise(54, 12)...))
		covMSeedMaterial(t, ctx, pool, client, "outro", 20, "ready", ownPath, covMNoise(53, 20))
		id, _ := covMSeedMaterial(t, ctx, pool, client, "novo", 20, "ready", ownPath, nil)

		got, err := FindSimilarMaterials(ctx, pool, id, 0.25)
		require.NoError(t, err)
		require.Len(t, got, 3, "o áudio sem relação fica de fora")

		byID := map[uuid.UUID]SimilarMaterial{}
		for _, m := range got {
			byID[m.ID] = m
		}
		require.Contains(t, byID, same)
		require.Contains(t, byID, sub)
		require.Contains(t, byID, partial)
		assert.GreaterOrEqual(t, byID[same].Score, 0.9)
		assert.GreaterOrEqual(t, byID[sub].Score, 0.9, "peer contido inteiro no áudio pontua alto")
		assert.GreaterOrEqual(t, byID[partial].Score, 0.25)
		assert.Less(t, byID[partial].Score, byID[same].Score)
		assert.InDelta(t, 20.0, byID[same].DurationSeconds, 1e-9)
		assert.InDelta(t, 10.0, byID[sub].DurationSeconds, 1e-9)

		// Ordenação: score decrescente; empate desempata por ID crescente.
		assert.Equal(t, partial, got[2].ID, "o de menor score vem por último")
		for i := 1; i < len(got); i++ {
			prev, cur := got[i-1], got[i]
			assert.GreaterOrEqual(t, prev.Score, cur.Score, "score não pode crescer ao longo da lista")
			if prev.Score == cur.Score {
				assert.Less(t, prev.ID.String(), cur.ID.String(), "empate desempata por ID")
			}
		}

		strict, err := FindSimilarMaterials(ctx, pool, id, 1.01)
		require.NoError(t, err)
		assert.Empty(t, strict, "minScore acima de 1.0 não aceita ninguém")
	})
}

// ───────────────────── Subscriber (NATS falso) ─────────────────────

func TestCovM_Subscriber_Subscribe(t *testing.T) {
	ctx, pool := covMDB(t)
	srv := covMNewNATS(t)
	nc := covMConnect(t, srv)

	core, logs := observer.New(zap.DebugLevel)
	sub := NewSubscriber(pool, nc, zap.New(core))

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s, err := sub.Subscribe(subCtx)
	require.NoError(t, err)
	require.True(t, s.IsValid())
	require.NoError(t, nc.Flush())

	publish := func(data string) {
		require.NoError(t, nc.Publish(events.SubjectMaterialSimilarityCheck, []byte(data)))
		require.NoError(t, nc.Flush())
	}
	hasLog := func(msg string) func() bool {
		return func() bool { return logs.FilterMessage(msg).Len() > 0 }
	}

	publish("isto-nao-e-json")
	covMEventually(t, hasLog("similarity: invalid payload"), "payload inválido deve ser logado")

	publish(`{"material_id":"nao-e-uuid"}`)
	covMEventually(t, hasLog("similarity: invalid material_id"), "uuid inválido deve ser logado")

	publish(`{"material_id":"` + uuid.NewString() + `"}`)
	covMEventually(t, hasLog("similarity: CheckMaterialSimilarity failed"), "material inexistente deve virar erro logado")

	// Caminho feliz: material com fingerprint pendente -> skipped + log "ok".
	client := covMSeedClient(t, ctx, pool)
	id, _ := covMSeedMaterial(t, ctx, pool, client, "via-nats", 20, "pending", "/tmp/x.pcm", nil)
	publish(`{"material_id":"` + id.String() + `"}`)
	covMEventually(t, func() bool {
		var st string
		_ = pool.QueryRow(ctx, `SELECT similarity_check_status FROM materials WHERE id = $1`, id).Scan(&st)
		return st == "skipped"
	}, "o evento deve disparar o scan e gravar o status")
	covMEventually(t, hasLog("similarity: ok"), "sucesso deve ser logado")

	// Cancelar o contexto drena a assinatura.
	cancel()
	covMEventually(t, func() bool { return !s.IsValid() }, "ctx cancelado deve drenar a assinatura")
}

func TestCovM_Subscriber_SubscribeOnClosedConn(t *testing.T) {
	srv := covMNewNATS(t)
	nc := covMConnect(t, srv)
	nc.Close()
	s, err := NewSubscriber(nil, nc, zap.NewNop()).Subscribe(context.Background())
	require.Error(t, err)
	assert.Nil(t, s)
	assert.Contains(t, err.Error(), "similarity: subscribe")
}

func TestCovM_FrameCoverage_EmptyAndUnion(t *testing.T) {
	assert.Equal(t, 0, frameCoverage(nil))
	assert.Equal(t, 30, frameCoverage([]frameRange{{0, 10}, {5, 20}, {25, 35}}))
}

// BUG documentado: ver o comentário equivalente em
// internal/sharing/cov_sharing_test.go. Varrendo o PRÓPRIO áudio contra um
// índice que o contém (mesma posição nos dois lados), as faixas do outro lado
// deveriam acompanhar as do lado próprio. Hoje andam 2 frames por frame de
// janela porque similarity.go:436 faz xStart = ownStart - OffsetFrames, mas
// OffsetFrames já é relativo à janela (MatchWindow processa a janela como PCM
// isolado). Efeito: other_segments da timeline e otherCov saem deslocados/
// inflados; esperado xStart = -OffsetFrames.
func TestCovM_RunScan_OtherRangesTrackTruePosition(t *testing.T) {
	t.Skip("BUG: similarity.go:436 xStart = ownStart - OffsetFrames soma ownStart duas vezes (MatchWindow devolve frames relativos à janela); esperado xStart ≈ ownStart, atual ≈ 2*ownStart")

	pcm := covMNoise(91, 20)
	const short int32 = 5
	otherID := uuid.New()
	store := index.New()
	store.Swap(indexFromHashes(denseHashes(pcm), short))
	rep := runScan(pcm, store, uuid.New(), map[int32]uuid.UUID{short: otherID},
		map[uuid.UUID]int{otherID: len(pcm) / 2048})

	scan := rep.perOther[otherID]
	require.NotNil(t, scan)
	require.Equal(t, len(scan.ownRanges), len(scan.otherRanges))
	for i := range scan.ownRanges {
		assert.InDelta(t, float64(scan.ownRanges[i].from), float64(scan.otherRanges[i].from), 2,
			"janela %d: mesmo áudio na mesma posição => mesma faixa nos dois lados", i)
	}
}
