package sharing

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"radiocheck/internal/events"
)

const covHFramesPerSec = 16000.0 / 2048.0

// ───────────────────── funções puras ─────────────────────

func TestCovH_MergeRanges(t *testing.T) {
	cases := []struct {
		name string
		in   []frameRange
		want []frameRange
	}{
		{"vazio", nil, nil},
		{"um só", []frameRange{{3, 9}}, []frameRange{{3, 9}}},
		{"fora de ordem e sobrepostos fundem", []frameRange{{20, 30}, {0, 10}, {8, 22}}, []frameRange{{0, 30}}},
		{"contíguos fundem (until == from)", []frameRange{{0, 10}, {10, 20}}, []frameRange{{0, 20}}},
		{"disjuntos ficam separados e ordenados", []frameRange{{50, 60}, {0, 5}}, []frameRange{{0, 5}, {50, 60}}},
		{"contido dentro do anterior não estende", []frameRange{{0, 100}, {10, 20}}, []frameRange{{0, 100}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, mergeRanges(append([]frameRange(nil), c.in...)))
		})
	}
}

func TestCovH_FrameCoverage_Empty(t *testing.T) {
	assert.Equal(t, 0, frameCoverage(nil))
	assert.Equal(t, 15, frameCoverage([]frameRange{{0, 10}, {5, 15}}))
}

func TestCovH_MinScore_ReadsEnv(t *testing.T) {
	t.Setenv("SHARING_MIN_SCORE", "37")
	assert.Equal(t, 37, MinScore())
	t.Setenv("SHARING_MIN_SCORE", "abc")
	assert.Equal(t, DefaultMinScore, MinScore(), "valor inválido cai no default")
	t.Setenv("SHARING_MIN_SCORE", "")
	assert.Equal(t, DefaultMinScore, MinScore())
}

func TestCovH_MinShareableDurationFrames(t *testing.T) {
	// 10s * 16000 / 2048 = 78,125 -> piso 78
	assert.Equal(t, 78, MinShareableDurationFrames)
}

// ───────────────────── MarkSharedHashes (DB + ffmpeg falso) ─────────────────────

func TestCovH_MarkSharedHashes_StingPairFlagsBothSidesNotWholeAudio(t *testing.T) {
	ctx, pool := covHDB(t)
	covHInstallFakeFFmpeg(t)

	x, a := covHStingPairAtStart()
	client := covHSeedClient(t, ctx, pool)
	xID, _ := covHSeedMaterial(t, ctx, pool, client, "mat-x", 30, "ready", "/tmp/x.pcm", x)
	aID, _ := covHSeedMaterial(t, ctx, pool, client, "mat-a", 30, "ready", "/tmp/a.pcm", a)
	aPath := covHWritePCM(t, "a.pcm", a)

	require.NoError(t, MarkSharedHashes(ctx, pool, aID, aPath))

	for name, id := range map[string]uuid.UUID{"A (escaneado)": aID, "X (catálogo)": xID} {
		total, shared := covHSharedCount(t, ctx, pool, id)
		assert.Greater(t, shared, 0, "%s deve ter hashes marcados como shared", name)
		assert.Less(t, shared, total/2, "%s: o sting é só ~1/7 do áudio, não pode ser marcado em massa", name)

		// O trecho compartilhado real (0s–4s => frames 0..31) tem que estar marcado.
		var head int
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM fingerprint_hashes
			WHERE commercial_id = $1 AND is_shared AND time_frame < 31`, id).Scan(&head))
		assert.Greater(t, head, 0, "%s: o trecho compartilhado (0–4s) precisa estar marcado", name)
	}

	// Idempotente: rodar de novo não muda as contagens.
	_, sharedBefore := covHSharedCount(t, ctx, pool, xID)
	require.NoError(t, MarkSharedHashes(ctx, pool, aID, aPath))
	_, sharedAfter := covHSharedCount(t, ctx, pool, xID)
	assert.Equal(t, sharedBefore, sharedAfter)
}

func TestCovH_MarkSharedHashes_SubsetIsNotFlagged(t *testing.T) {
	ctx, pool := covHDB(t)
	covHInstallFakeFFmpeg(t)

	full := covHNoise(73, 30)
	cut := full[:15*16000] // 15s ⊂ 30s: own coverage ~100% => subset
	client := covHSeedClient(t, ctx, pool)
	fullID, _ := covHSeedMaterial(t, ctx, pool, client, "mat-30s", 30, "ready", "/tmp/f.pcm", full)
	cutID, _ := covHSeedMaterial(t, ctx, pool, client, "mat-15s", 15, "ready", "/tmp/c.pcm", cut)

	require.NoError(t, MarkSharedHashes(ctx, pool, cutID, covHWritePCM(t, "cut.pcm", cut)))

	for _, id := range []uuid.UUID{fullID, cutID} {
		_, shared := covHSharedCount(t, ctx, pool, id)
		assert.Zero(t, shared, "subset/duplicata não é flagado (a defesa é a desambiguação por duração)")
	}
}

func TestCovH_MarkSharedHashes_UnrelatedAudioFlagsNothing(t *testing.T) {
	ctx, pool := covHDB(t)
	covHInstallFakeFFmpeg(t)

	a, b := covHNoise(74, 20), covHNoise(75, 20)
	client := covHSeedClient(t, ctx, pool)
	aID, _ := covHSeedMaterial(t, ctx, pool, client, "mat-a", 20, "ready", "/tmp/a.pcm", a)
	bID, _ := covHSeedMaterial(t, ctx, pool, client, "mat-b", 20, "ready", "/tmp/b.pcm", b)

	require.NoError(t, MarkSharedHashes(ctx, pool, aID, covHWritePCM(t, "a.pcm", a)))
	for _, id := range []uuid.UUID{aID, bID} {
		_, shared := covHSharedCount(t, ctx, pool, id)
		assert.Zero(t, shared)
	}
}

func TestCovH_MarkSharedHashes_LegacyCommercialInCatalog(t *testing.T) {
	ctx, pool := covHDB(t)
	covHInstallFakeFFmpeg(t)

	x, a := covHStingPairAtStart()
	client := covHSeedClient(t, ctx, pool)
	// X é um commercial legado (caminho 1 do catálogo); A é o commercial escaneado
	// (resolve o short_id pela tabela commercials).
	xID, _ := covHSeedCommercial(t, ctx, pool, client, "com-x", 30, "ready", "/tmp/x.pcm", x)
	aID, _ := covHSeedCommercial(t, ctx, pool, client, "com-a", 30, "ready", "/tmp/a.pcm", a)

	require.NoError(t, MarkSharedHashes(ctx, pool, aID, covHWritePCM(t, "a.pcm", a)))
	_, sharedX := covHSharedCount(t, ctx, pool, xID)
	_, sharedA := covHSharedCount(t, ctx, pool, aID)
	assert.Greater(t, sharedX, 0)
	assert.Greater(t, sharedA, 0)
}

func TestCovH_MarkSharedHashes_Errors(t *testing.T) {
	ctx, pool := covHDB(t)
	covHInstallFakeFFmpeg(t)

	t.Run("entidade inexistente", func(t *testing.T) {
		err := MarkSharedHashes(ctx, pool, uuid.New(), "/tmp/nada.pcm")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lookup short_id")
	})

	t.Run("catálogo sem nenhum hash é no-op sem erro", func(t *testing.T) {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM fingerprint_hashes`).Scan(&n))
		require.Zero(t, n, "pré-condição: banco de teste sem hashes residuais")
		client := covHSeedClient(t, ctx, pool)
		id, _ := covHSeedMaterial(t, ctx, pool, client, "primeiro", 30, "ready", "/tmp/p.pcm", nil)
		require.NoError(t, MarkSharedHashes(ctx, pool, id, "/tmp/p.pcm"))
	})

	t.Run("falha de decode do master", func(t *testing.T) {
		client := covHSeedClient(t, ctx, pool)
		pcm := covHNoise(76, 12)
		id, _ := covHSeedMaterial(t, ctx, pool, client, "m", 12, "ready", "/tmp/m.pcm", pcm)
		t.Setenv(covHEnvFail, "1")
		err := MarkSharedHashes(ctx, pool, id, "/tmp/m.pcm")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode master")
	})

	t.Run("variant_id fora de uint8 derruba o carregamento do catálogo", func(t *testing.T) {
		client := covHSeedClient(t, ctx, pool)
		id, _ := covHSeedMaterial(t, ctx, pool, client, "m", 12, "ready", "/tmp/m.pcm", nil)
		covHInsertHashes(t, ctx, pool, id, covHNoise(77, 5), 400)
		err := MarkSharedHashes(ctx, pool, id, "/tmp/m.pcm")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "load catalog")
	})
}

// ───────────────────── ComputeTwinOverlap / resolveEntity / loadHashesForID ─────────────────────

func TestCovH_ResolveEntity(t *testing.T) {
	ctx, pool := covHDB(t)
	client := covHSeedClient(t, ctx, pool)
	matID, matShort := covHSeedMaterial(t, ctx, pool, client, "mat", 12.5, "ready", "/tmp/mat.pcm", nil)
	comID, comShort := covHSeedCommercial(t, ctx, pool, client, "com", 7, "ready", "/tmp/com.pcm", nil)

	short, path, dur, err := resolveEntity(ctx, pool, matID)
	require.NoError(t, err)
	assert.Equal(t, matShort, short)
	assert.Equal(t, "/tmp/mat.pcm", path)
	assert.InDelta(t, 12.5, dur, 1e-9)

	short, path, dur, err = resolveEntity(ctx, pool, comID)
	require.NoError(t, err)
	assert.Equal(t, comShort, short)
	assert.Equal(t, "/tmp/com.pcm", path)
	assert.InDelta(t, 7.0, dur, 1e-9)

	_, _, _, err = resolveEntity(ctx, pool, uuid.New())
	require.Error(t, err)
}

func TestCovH_LoadHashesForID(t *testing.T) {
	ctx, pool := covHDB(t)
	client := covHSeedClient(t, ctx, pool)
	pcm := covHNoise(78, 6)
	id, _ := covHSeedMaterial(t, ctx, pool, client, "mat", 6, "ready", "/tmp/m.pcm", pcm)

	idx, err := loadHashesForID(ctx, pool, id, 4242)
	require.NoError(t, err)
	require.NotEmpty(t, idx)
	for _, entries := range idx {
		for _, e := range entries {
			assert.Equal(t, int32(4242), e.CommercialShortID, "toda entrada leva o short_id informado")
			assert.False(t, e.IsShared, "IsShared fica falso: o scan re-deriva o compartilhamento")
		}
	}

	empty, err := loadHashesForID(ctx, pool, uuid.New(), 1)
	require.NoError(t, err)
	assert.Empty(t, empty)

	covHInsertHashes(t, ctx, pool, id, covHNoise(79, 4), 500)
	_, err = loadHashesForID(ctx, pool, id, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of uint8 range")
}

func TestCovH_LoadCatalogIndex_MapsBothTables(t *testing.T) {
	ctx, pool := covHDB(t)
	client := covHSeedClient(t, ctx, pool)
	matID, matShort := covHSeedMaterial(t, ctx, pool, client, "mat", 20, "ready", "/tmp/m.pcm", covHNoise(80, 6))
	comID, comShort := covHSeedCommercial(t, ctx, pool, client, "com", 10, "ready", "/tmp/c.pcm", covHNoise(81, 6))
	// 'pending' não entra no catálogo
	pendID, pendShort := covHSeedMaterial(t, ctx, pool, client, "pend", 20, "pending", "/tmp/p.pcm", covHNoise(82, 6))

	idx, shortToID, frames, err := loadCatalogIndex(ctx, pool)
	require.NoError(t, err)
	assert.NotEmpty(t, idx)
	assert.Equal(t, matID, shortToID[matShort])
	assert.Equal(t, comID, shortToID[comShort])
	assert.NotContains(t, shortToID, pendShort)
	assert.NotContains(t, frames, pendID)
	assert.Equal(t, 156, frames[matID], "frames = int(20s × 16000/2048)")
	assert.Equal(t, 78, frames[comID])
}

func TestCovH_ComputeTwinOverlap(t *testing.T) {
	ctx, pool := covHDB(t)
	covHInstallFakeFFmpeg(t)

	x, a := covHStingPair() // a (gêmeo "X" do ponto de vista da função) compartilha 8s–12s com x
	client := covHSeedClient(t, ctx, pool)
	yID, _ := covHSeedMaterial(t, ctx, pool, client, "y", 30, "ready", "/tmp/y.pcm", x)
	xID, _ := covHSeedMaterial(t, ctx, pool, client, "x", 30, "ready", covHWritePCM(t, "x.pcm", a), nil)

	t.Run("devolve as faixas de X que sobrepõem Y e o total de frames de X", func(t *testing.T) {
		ranges, total, err := ComputeTwinOverlap(ctx, pool, xID, yID)
		require.NoError(t, err)
		assert.Equal(t, len(a)/2048, total)
		require.NotEmpty(t, ranges)
		for _, r := range ranges {
			assert.Less(t, r.Lo, r.Hi)
			assert.GreaterOrEqual(t, float64(r.Lo), 4.0*covHFramesPerSec, "sobreposição começa perto de 8s")
			assert.LessOrEqual(t, float64(r.Hi), 16.0*covHFramesPerSec, "e termina perto de 12s")
		}
	})

	t.Run("X inexistente", func(t *testing.T) {
		_, _, err := ComputeTwinOverlap(ctx, pool, uuid.New(), yID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "resolve X")
	})

	t.Run("Y inexistente", func(t *testing.T) {
		_, _, err := ComputeTwinOverlap(ctx, pool, xID, uuid.New())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "resolve Y")
	})

	t.Run("decode do master de X falha", func(t *testing.T) {
		t.Setenv(covHEnvFail, "1")
		_, _, err := ComputeTwinOverlap(ctx, pool, xID, yID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode X master")
	})

	t.Run("hashes de Y inválidos", func(t *testing.T) {
		badY, _ := covHSeedMaterial(t, ctx, pool, client, "y-ruim", 30, "ready", "/tmp/yb.pcm", nil)
		covHInsertHashes(t, ctx, pool, badY, covHNoise(83, 4), 777)
		_, _, err := ComputeTwinOverlap(ctx, pool, xID, badY)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "load Y hashes")
	})
}

// ───────────────────── Subscriber (NATS falso) ─────────────────────

type covHReloadSink struct {
	mu   sync.Mutex
	msgs [][]byte
}

func (s *covHReloadSink) add(m *nats.Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, append([]byte(nil), m.Data...))
}

func (s *covHReloadSink) snapshot() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.msgs...)
}

func TestCovH_Subscriber_Subscribe(t *testing.T) {
	ctx, pool := covHDB(t)
	covHInstallFakeFFmpeg(t)
	srv := covHNewNATS(t)
	nc := covHConnect(t, srv)
	spy := covHConnect(t, srv)

	var reloads covHReloadSink
	_, err := spy.Subscribe(events.SubjectIndexReload, reloads.add)
	require.NoError(t, err)
	require.NoError(t, spy.Flush())

	var afterMu sync.Mutex
	var afterIDs []uuid.UUID
	core, logs := observer.New(zap.DebugLevel)
	sub := NewSubscriber(pool, nc, zap.New(core), func(_ context.Context, id uuid.UUID) {
		afterMu.Lock()
		defer afterMu.Unlock()
		afterIDs = append(afterIDs, id)
	})

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s, err := sub.Subscribe(subCtx)
	require.NoError(t, err)
	require.NoError(t, nc.Flush())

	publish := func(data string) {
		require.NoError(t, spy.Publish(events.SubjectFingerprintSharedScan, []byte(data)))
		require.NoError(t, spy.Flush())
	}
	hasLog := func(msg string) func() bool {
		return func() bool { return logs.FilterMessage(msg).Len() > 0 }
	}

	publish("lixo-nao-json")
	covHEventually(t, hasLog("shared-scan: invalid payload"), "JSON inválido")

	publish(`{}`)
	covHEventually(t, hasLog("shared-scan: payload missing both commercial_id and material_id"), "payload vazio")

	publish(`{"commercial_id":"nao-e-uuid"}`)
	covHEventually(t, hasLog("shared-scan: lookup failed"), "uuid inválido de commercial")

	publish(`{"material_id":"` + uuid.NewString() + `"}`)
	covHEventually(t, func() bool { return logs.FilterMessage("shared-scan: lookup failed").Len() >= 2 }, "material inexistente")

	// fingerprint não pronto -> skip, sem scan nem reload.
	client := covHSeedClient(t, ctx, pool)
	pendID, _ := covHSeedMaterial(t, ctx, pool, client, "pend", 30, "pending", "/tmp/p.pcm", nil)
	publish(`{"material_id":"` + pendID.String() + `"}`)
	covHEventually(t, hasLog("shared-scan: skipping — fingerprint_status not ready"), "status não-ready é pulado")

	// Caminho feliz com material: flaga o sting, republica index.reload e chama afterScan.
	x, a := covHStingPairAtStart()
	xID, _ := covHSeedMaterial(t, ctx, pool, client, "mat-x", 30, "ready", "/tmp/x.pcm", x)
	aPath := covHWritePCM(t, "a.pcm", a)
	aID, _ := covHSeedMaterial(t, ctx, pool, client, "mat-a", 30, "ready", aPath, a)
	publish(`{"material_id":"` + aID.String() + `"}`)

	covHEventually(t, func() bool { return len(reloads.snapshot()) == 1 }, "index.reload republicado")
	var reloadPayload payload
	require.NoError(t, json.Unmarshal(reloads.snapshot()[0], &reloadPayload))
	assert.Equal(t, aID.String(), reloadPayload.MaterialID)
	assert.Empty(t, reloadPayload.CommercialID)
	covHEventually(t, func() bool {
		afterMu.Lock()
		defer afterMu.Unlock()
		return len(afterIDs) == 1
	}, "afterScan chamado para material")
	afterMu.Lock()
	assert.Equal(t, aID, afterIDs[0])
	afterMu.Unlock()
	_, sharedX := covHSharedCount(t, ctx, pool, xID)
	assert.Greater(t, sharedX, 0, "o scan disparado pelo evento flagou o sting no catálogo")
	assert.Equal(t, 1, logs.FilterMessage("shared-scan: ok").Len())

	// Falha do scan (decode) -> loga erro, não republica e não chama afterScan.
	t.Setenv(covHEnvFail, "1")
	publish(`{"material_id":"` + aID.String() + `"}`)
	covHEventually(t, hasLog("shared-scan: MarkSharedHashes failed"), "falha do scan é logada")
	assert.Len(t, reloads.snapshot(), 1, "falha não republica index.reload")
	afterMu.Lock()
	assert.Len(t, afterIDs, 1, "falha não chama afterScan")
	afterMu.Unlock()

	cancel()
	covHEventually(t, func() bool { return !s.IsValid() }, "ctx cancelado drena a assinatura")
}

func TestCovH_Subscriber_CommercialPath_NoAfterScan(t *testing.T) {
	ctx, pool := covHDB(t)
	srv := covHNewNATS(t)
	nc := covHConnect(t, srv)
	spy := covHConnect(t, srv)

	var reloads covHReloadSink
	_, err := spy.Subscribe(events.SubjectIndexReload, reloads.add)
	require.NoError(t, err)
	require.NoError(t, spy.Flush())

	called := false
	var mu sync.Mutex
	sub := NewSubscriber(pool, nc, zap.NewNop(), func(context.Context, uuid.UUID) {
		mu.Lock()
		called = true
		mu.Unlock()
	})
	_, err = sub.Subscribe(context.Background())
	require.NoError(t, err)
	require.NoError(t, nc.Flush())

	// Catálogo vazio de hashes: MarkSharedHashes devolve nil sem decode.
	client := covHSeedClient(t, ctx, pool)
	comID, _ := covHSeedCommercial(t, ctx, pool, client, "com", 30, "ready", "/tmp/c.pcm", nil)
	require.NoError(t, spy.Publish(events.SubjectFingerprintSharedScan, []byte(`{"commercial_id":"`+comID.String()+`"}`)))
	require.NoError(t, spy.Flush())

	covHEventually(t, func() bool { return len(reloads.snapshot()) == 1 }, "reload republicado para commercial")
	var p payload
	require.NoError(t, json.Unmarshal(reloads.snapshot()[0], &p))
	assert.Equal(t, comID.String(), p.CommercialID)
	assert.Empty(t, p.MaterialID)
	mu.Lock()
	assert.False(t, called, "afterScan só vale para materiais (gêmeos são da biblioteca de materiais)")
	mu.Unlock()
}

func TestCovH_Subscriber_NilAfterScanIsSafe(t *testing.T) {
	ctx, pool := covHDB(t)
	srv := covHNewNATS(t)
	nc := covHConnect(t, srv)
	spy := covHConnect(t, srv)
	var reloads covHReloadSink
	_, err := spy.Subscribe(events.SubjectIndexReload, reloads.add)
	require.NoError(t, err)
	require.NoError(t, spy.Flush())

	_, err = NewSubscriber(pool, nc, zap.NewNop(), nil).Subscribe(ctx)
	require.NoError(t, err)
	require.NoError(t, nc.Flush())

	client := covHSeedClient(t, ctx, pool)
	id, _ := covHSeedMaterial(t, ctx, pool, client, "mat", 30, "ready", "/tmp/m.pcm", nil)
	require.NoError(t, spy.Publish(events.SubjectFingerprintSharedScan, []byte(`{"material_id":"`+id.String()+`"}`)))
	require.NoError(t, spy.Flush())
	covHEventually(t, func() bool { return len(reloads.snapshot()) == 1 }, "reload republicado sem afterScan")
}

func TestCovH_Subscriber_SubscribeOnClosedConn(t *testing.T) {
	srv := covHNewNATS(t)
	nc := covHConnect(t, srv)
	nc.Close()
	s, err := NewSubscriber(nil, nc, zap.NewNop(), nil).Subscribe(context.Background())
	require.Error(t, err)
	assert.Nil(t, s)
	assert.Contains(t, err.Error(), "sharing: subscribe")
}

// ───────────────────── BUG documentado ─────────────────────

// TestCovH_ScanForSharedRegions_OtherRangesTrackTruePosition: varrendo um áudio
// contra um índice que contém ELE MESMO (mesma posição nos dois lados), a faixa
// do "outro" (otherRanges) de cada janela deveria coincidir com a faixa própria
// (ownRanges) — o mesmo trecho de áudio nas mesmas posições.
//
// BUG (sharing.go:296-297, mesma fórmula em similarity.go:436-437): o código faz
// xStart = ownStartFrame - r.OffsetFrames. Mas MatchWindow recebe a janela como
// PCM isolado, então os time_frames do hash são RELATIVOS à janela e
// OffsetFrames = w - entry ≈ -(posição da janela no master). Resultado: o
// ownStartFrame é somado duas vezes e otherRanges anda 2 frames por frame de
// janela (medido: janela em 5s -> OffsetFrames=-38, x=[77,108] em vez de
// [39,70]). A correção é xStart = -r.OffsetFrames.
// Efeito: o lado "outro" do shared-hash é marcado em frames errados (deslocados
// pra frente) e a cobertura do outro lado (otherCov) é superestimada, podendo
// classificar um sting como subset (e não flagar nada).
func TestCovH_ScanForSharedRegions_OtherRangesTrackTruePosition(t *testing.T) {

	pcm := covHNoise(90, 20)
	store := covHStoreFor(pcm, 5)
	xid, aid := uuid.New(), uuid.New()
	rep := scanForSharedRegions(pcm, store, 6, map[int32]uuid.UUID{5: xid}, aid,
		map[uuid.UUID]int{xid: len(pcm) / 2048}, MinScore())

	scan := rep.perOther[xid]
	require.NotNil(t, scan)
	require.Equal(t, len(scan.ownRanges), len(scan.otherRanges))
	for i := range scan.ownRanges {
		assert.InDelta(t, float64(scan.ownRanges[i].from), float64(scan.otherRanges[i].from), 2,
			"janela %d: mesmo áudio na mesma posição => mesma faixa nos dois lados", i)
	}
}
