package index

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"radiocheck/internal/db"
	"radiocheck/internal/events"
)

// covXSeedClient cria um cliente descartável.
func covXSeedClient(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('covx-client-'||gen_random_uuid()) RETURNING id`).Scan(&id))
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM clients WHERE id = $1`, id) })
	return id
}

// covXSeedMaterial cria um material 'ready' ligado (campaign_materials) à
// campanha e com nHashes hashes (hash_value = hashBase+i, time_frame = i).
func covXSeedMaterial(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	clientID, campaignID uuid.UUID, nHashes int, hashBase int64) (uuid.UUID, int32) {
	t.Helper()
	id := uuid.New()
	var shortID int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO materials (id, client_id, title, duration_seconds, master_storage_path,
		                       master_sha256, fingerprint_status)
		VALUES ($1, $2, 'covx-mat', 30, '/tmp/covx.mp3', $3, 'ready')
		RETURNING short_id`, id, clientID, "sha-covx-"+id.String()).Scan(&shortID))
	_, err := pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, '{}')`, campaignID, id)
	require.NoError(t, err)
	for i := 0; i < nHashes; i++ {
		_, err := pool.Exec(ctx, `
			INSERT INTO fingerprint_hashes (commercial_id, variant_id, rate_id, hash_value, time_frame, is_shared)
			VALUES ($1, 0, 0, $2, $3, false)`, id, hashBase+int64(i), i)
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		pool.Exec(bg, `DELETE FROM fingerprint_hashes WHERE commercial_id = $1`, id)
		pool.Exec(bg, `DELETE FROM campaign_materials WHERE material_id = $1`, id)
		pool.Exec(bg, `DELETE FROM materials WHERE id = $1`, id)
	})
	return id, shortID
}

// covXSeedCommercial cria um commercial legado 'ready' (sem linha em materials)
// dentro da campanha, com nHashes hashes.
func covXSeedCommercial(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	campaignID uuid.UUID, nHashes int, hashBase int64) (uuid.UUID, int32) {
	t.Helper()
	id := uuid.New()
	var shortID int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO commercials (id, campaign_id, title, duration_seconds, master_storage_path,
		                         master_sha256, fingerprint_status)
		VALUES ($1, $2, 'covx-com', 30, '/tmp/covx.mp3', $3, 'ready')
		RETURNING short_id`, id, campaignID, "sha-covx-"+id.String()).Scan(&shortID))
	for i := 0; i < nHashes; i++ {
		_, err := pool.Exec(ctx, `
			INSERT INTO fingerprint_hashes (commercial_id, variant_id, rate_id, hash_value, time_frame, is_shared)
			VALUES ($1, 0, 0, $2, $3, false)`, id, hashBase+int64(i), i)
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		pool.Exec(bg, `DELETE FROM fingerprint_hashes WHERE commercial_id = $1`, id)
		pool.Exec(bg, `DELETE FROM commercials WHERE id = $1`, id)
	})
	return id, shortID
}

// covXCount conta as entradas do índice que pertencem ao short_id.
func covXCount(idx Index, shortID int32) int {
	n := 0
	for _, entries := range idx {
		for _, e := range entries {
			if e.CommercialShortID == shortID {
				n++
			}
		}
	}
	return n
}

// covXSubscribeFixture sobe NATS falso + loader com logger observado.
type covXSubscribeFixture struct {
	ctx    context.Context
	pool   *pgxpool.Pool
	store  *Store
	nc     *nats.Conn
	logs   *observer.ObservedLogs
	loader *Loader
}

func covXNewSubscribeFixture(t *testing.T) *covXSubscribeFixture {
	t.Helper()
	ctx, pool := newTestDB(t)
	srv := covXStartNATS(t)
	nc := covXConnect(t, srv)
	log, logs := covXObservedLogger()
	store := New()
	loader := NewLoader(store, pool, nc, log)
	sub, err := loader.Subscribe(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	// Garante que o SUB já foi processado pelo servidor antes do 1º publish.
	require.NoError(t, nc.Flush())
	return &covXSubscribeFixture{ctx: ctx, pool: pool, store: store, nc: nc, logs: logs, loader: loader}
}

func (f *covXSubscribeFixture) publish(t *testing.T, payload any) {
	t.Helper()
	var data []byte
	switch p := payload.(type) {
	case []byte:
		data = p
	default:
		var err error
		data, err = json.Marshal(p)
		require.NoError(t, err)
	}
	require.NoError(t, f.nc.Publish(events.SubjectIndexReload, data))
	require.NoError(t, f.nc.Flush())
}

// Reload de material: o handler troca TODAS as entradas daquele short_id pelas
// atuais do banco (variant/rate/is_shared mapeados), preserva as de outros
// materiais e nunca altera o mapa que já estava publicado (snapshot imutável).
func TestCovX_Subscribe_MaterialReload_ReplacesEntriesAndKeepsOthers(t *testing.T) {
	f := covXNewSubscribeFixture(t)
	clientID := covXSeedClient(t, f.ctx, f.pool)
	camp := seedCampaign(t, f.ctx, f.pool, clientID, "ativa", "2026-06-01", "2026-07-31")
	matID, shortID := covXSeedMaterial(t, f.ctx, f.pool, clientID, camp, 3, 910_000)

	// Uma linha com variante/rate/shared diferentes, pra provar o mapeamento.
	_, err := f.pool.Exec(f.ctx, `
		UPDATE fingerprint_hashes SET variant_id = 2, rate_id = 1, is_shared = true
		WHERE commercial_id = $1 AND hash_value = 910001`, matID)
	require.NoError(t, err)

	const otherShort = int32(987_654)
	stale := Index{
		7:      {{CommercialShortID: shortID, TimeFrame: 99}},                                 // some do índice
		910001: {{CommercialShortID: otherShort, TimeFrame: 5}},                               // fica
		55:     {{CommercialShortID: otherShort, TimeFrame: 1}, {CommercialShortID: shortID}}, // só a do material sai
	}
	f.store.Swap(stale)
	before := f.store.Load()

	f.publish(t, map[string]string{"material_id": matID.String()})
	covXWaitLog(t, f.logs, "index reloaded for entity", 1)

	after := f.store.Load()
	require.NotContains(t, after, uint32(7), "chave que só tinha o short_id do material deve sumir")
	require.Equal(t, []Entry{{CommercialShortID: otherShort, TimeFrame: 1}}, after[55],
		"entradas de outro material na mesma chave são preservadas")
	require.Equal(t, 3, covXCount(after, shortID), "exatamente os 3 hashes atuais do material")
	require.Equal(t, []Entry{{CommercialShortID: shortID, TimeFrame: 0}}, after[910000])
	require.Equal(t, []Entry{{CommercialShortID: otherShort, TimeFrame: 5},
		{CommercialShortID: shortID, VariantID: 2, RateID: 1, TimeFrame: 1, IsShared: true}}, after[910001],
		"a chave compartilhada guarda a entrada antiga + a nova, com variant/rate/is_shared mapeados")
	require.Equal(t, []Entry{{CommercialShortID: shortID, TimeFrame: 2}}, after[910002])

	// O mapa publicado antes do reload não pode ter sido mutado.
	require.Contains(t, before, uint32(7))
	require.Len(t, before[55], 2, "snapshot anterior continua com as 2 entradas originais em 55")
	require.Len(t, before[910001], 1)
}

// Reload via commercial_id (legado): usa a query de commercials e o mesmo merge.
func TestCovX_Subscribe_CommercialReload_LoadsLegacyEntity(t *testing.T) {
	f := covXNewSubscribeFixture(t)
	clientID := covXSeedClient(t, f.ctx, f.pool)
	camp := seedCampaign(t, f.ctx, f.pool, clientID, "programada", "2026-09-01", "2026-09-30")
	comID, shortID := covXSeedCommercial(t, f.ctx, f.pool, camp, 4, 920_000)

	f.publish(t, map[string]string{"commercial_id": comID.String()})
	covXWaitLog(t, f.logs, "index reloaded for entity", 1)

	idx := f.store.Load()
	require.Equal(t, 4, covXCount(idx, shortID))
	require.Equal(t, []Entry{{CommercialShortID: shortID, TimeFrame: 3}}, idx[920003])
	entry := f.logs.FilterMessage("index reloaded for entity").All()[0]
	require.Equal(t, "commercial", entry.ContextMap()["kind"])
	require.EqualValues(t, 4, entry.ContextMap()["hashes"])
}

// commercial_id tem precedência quando o payload traz os dois (comportamento
// herdado do daemon Python, documentado em reloadPayload).
func TestCovX_Subscribe_CommercialIDWinsOverMaterialID(t *testing.T) {
	f := covXNewSubscribeFixture(t)
	clientID := covXSeedClient(t, f.ctx, f.pool)
	camp := seedCampaign(t, f.ctx, f.pool, clientID, "ativa", "2026-06-01", "2026-07-31")
	comID, comShort := covXSeedCommercial(t, f.ctx, f.pool, camp, 2, 930_000)
	matID, matShort := covXSeedMaterial(t, f.ctx, f.pool, clientID, camp, 2, 931_000)

	f.publish(t, map[string]string{"commercial_id": comID.String(), "material_id": matID.String()})
	covXWaitLog(t, f.logs, "index reloaded for entity", 1)

	idx := f.store.Load()
	require.Equal(t, 2, covXCount(idx, comShort))
	require.Equal(t, 0, covXCount(idx, matShort), "o material do payload não deve ser carregado")
}

// Entidade fora do conjunto elegível (campanha concluída / inexistente): o
// índice não muda e o motivo aparece no log.
func TestCovX_Subscribe_IneligibleOrUnknownEntity_LeavesIndexUntouched(t *testing.T) {
	f := covXNewSubscribeFixture(t)
	clientID := covXSeedClient(t, f.ctx, f.pool)
	done := seedCampaign(t, f.ctx, f.pool, clientID, "concluida", "2026-01-01", "2026-01-31")
	matID, _ := covXSeedMaterial(t, f.ctx, f.pool, clientID, done, 2, 940_000)
	comID, _ := covXSeedCommercial(t, f.ctx, f.pool, done, 2, 941_000)

	sentinel := Index{1: {{CommercialShortID: 1}}}
	f.store.Swap(sentinel)

	f.publish(t, map[string]string{"material_id": matID.String()})
	f.publish(t, map[string]string{"commercial_id": comID.String()})
	f.publish(t, map[string]string{"material_id": uuid.NewString()})
	covXWaitLog(t, f.logs, "index.reload: entity not eligible or not found", 3)

	require.Equal(t, sentinel, f.store.Load(), "nenhuma das 3 entidades pode alterar o índice")
	kinds := map[string]int{}
	for _, e := range f.logs.FilterMessage("index.reload: entity not eligible or not found").All() {
		kinds[e.ContextMap()["kind"].(string)]++
	}
	require.Equal(t, map[string]int{"material": 2, "commercial": 1}, kinds)
}

// Payloads inválidos são ignorados com warn — nunca derrubam o handler.
func TestCovX_Subscribe_InvalidPayloads_AreWarnedAndIgnored(t *testing.T) {
	f := covXNewSubscribeFixture(t)
	sentinel := Index{3: {{CommercialShortID: 3}}}
	f.store.Swap(sentinel)

	f.publish(t, []byte("isto não é json"))
	f.publish(t, []byte(`{}`))
	f.publish(t, []byte(`{"material_id":"","commercial_id":""}`))
	covXWaitLog(t, f.logs, "index.reload: invalid JSON payload", 1)
	covXWaitLog(t, f.logs, "index.reload: payload missing both commercial_id and material_id", 2)

	require.Equal(t, sentinel, f.store.Load())
}

// variant_id/rate_id fora de uint8 abortam o reload (erro) sem trocar o índice.
func TestCovX_Subscribe_OutOfRangeVariant_AbortsWithoutSwap(t *testing.T) {
	f := covXNewSubscribeFixture(t)
	clientID := covXSeedClient(t, f.ctx, f.pool)
	camp := seedCampaign(t, f.ctx, f.pool, clientID, "ativa", "2026-06-01", "2026-07-31")
	matID, _ := covXSeedMaterial(t, f.ctx, f.pool, clientID, camp, 2, 950_000)
	_, err := f.pool.Exec(f.ctx, `UPDATE fingerprint_hashes SET variant_id = 300 WHERE commercial_id = $1 AND hash_value = 950000`, matID)
	require.NoError(t, err)

	sentinel := Index{9: {{CommercialShortID: 9}}}
	f.store.Swap(sentinel)

	f.publish(t, map[string]string{"material_id": matID.String()})
	covXWaitLog(t, f.logs, "index.reload: variant_id or rate_id out of uint8 range", 1)

	require.Equal(t, sentinel, f.store.Load(), "índice não pode ser trocado quando um hash é inválido")
	require.Zero(t, f.logs.FilterMessage("index reloaded for entity").Len())
}

// Entidade elegível mas sem hashes: o reload remove o que havia (merge vazio).
func TestCovX_Subscribe_EligibleWithoutHashes_RemovesStaleEntries(t *testing.T) {
	f := covXNewSubscribeFixture(t)
	clientID := covXSeedClient(t, f.ctx, f.pool)
	camp := seedCampaign(t, f.ctx, f.pool, clientID, "ativa", "2026-06-01", "2026-07-31")
	matID, shortID := covXSeedMaterial(t, f.ctx, f.pool, clientID, camp, 0, 960_000)
	f.store.Swap(Index{
		11: {{CommercialShortID: shortID, TimeFrame: 1}},
		12: {{CommercialShortID: 4242, TimeFrame: 2}},
	})

	f.publish(t, map[string]string{"material_id": matID.String()})
	covXWaitLog(t, f.logs, "index reloaded for entity", 1)

	require.Equal(t, Index{12: {{CommercialShortID: 4242, TimeFrame: 2}}}, f.store.Load())
}

// Subscribe numa conexão já fechada devolve erro embrulhado (sem sub).
func TestCovX_Subscribe_ClosedConnection_ReturnsError(t *testing.T) {
	srv := covXStartNATS(t)
	nc := covXConnect(t, srv)
	nc.Close()
	l := NewLoader(New(), nil, nc, zap.NewNop())

	sub, err := l.Subscribe(context.Background())

	require.Error(t, err)
	require.Nil(t, sub)
	require.Contains(t, err.Error(), "index loader: nats subscribe")
}

// LoadAll respeita o filtro de status: programada/ativa entram (material e
// commercial legado), concluida/cancelada não; um commercial com linha em
// materials é carregado uma vez só (pelo material).
func TestCovX_LoadAll_EligibilityByCampaignStatus(t *testing.T) {
	ctx, pool := newTestDB(t)
	clientID := covXSeedClient(t, ctx, pool)
	ativa := seedCampaign(t, ctx, pool, clientID, "ativa", "2026-06-01", "2026-07-31")
	prog := seedCampaign(t, ctx, pool, clientID, "programada", "2026-09-01", "2026-09-30")
	canc := seedCampaign(t, ctx, pool, clientID, "cancelada", "2026-06-01", "2026-07-31")
	conc := seedCampaign(t, ctx, pool, clientID, "concluida", "2026-01-01", "2026-01-31")

	_, matAtiva := covXSeedMaterial(t, ctx, pool, clientID, ativa, 2, 970_000)
	_, matProg := covXSeedMaterial(t, ctx, pool, clientID, prog, 2, 971_000)
	_, matCanc := covXSeedMaterial(t, ctx, pool, clientID, canc, 2, 972_000)
	_, comProg := covXSeedCommercial(t, ctx, pool, prog, 3, 973_000)
	_, comConc := covXSeedCommercial(t, ctx, pool, conc, 3, 974_000)

	store := New()
	require.NoError(t, NewLoader(store, pool, nil, zap.NewNop()).LoadAll(ctx))
	idx := store.Load()

	require.Equal(t, 2, covXCount(idx, matAtiva))
	require.Equal(t, 2, covXCount(idx, matProg), "programada também indexa (evita corrida com a ativação)")
	require.Equal(t, 0, covXCount(idx, matCanc), "cancelada fica fora")
	require.Equal(t, 3, covXCount(idx, comProg), "commercial legado de campanha programada entra pelo caminho B")
	require.Equal(t, 0, covXCount(idx, comConc), "commercial legado de campanha concluída fica fora")
}

// LoadAll recusa variant/rate fora de uint8 e não troca o índice em uso.
func TestCovX_LoadAll_OutOfRangeRate_ReturnsErrorKeepsIndex(t *testing.T) {
	ctx, pool := newTestDB(t)
	clientID := covXSeedClient(t, ctx, pool)
	camp := seedCampaign(t, ctx, pool, clientID, "ativa", "2026-06-01", "2026-07-31")
	matID, _ := covXSeedMaterial(t, ctx, pool, clientID, camp, 1, 980_000)
	_, err := pool.Exec(ctx, `UPDATE fingerprint_hashes SET rate_id = 256 WHERE commercial_id = $1`, matID)
	require.NoError(t, err)

	store := New()
	sentinel := Index{5: {{CommercialShortID: 5}}}
	store.Swap(sentinel)

	err = NewLoader(store, pool, nil, zap.NewNop()).LoadAll(ctx)

	require.Error(t, err)
	require.Contains(t, err.Error(), "out of uint8 range")
	require.Equal(t, sentinel, store.Load())
}

// Falha de query (pool fechado) vira erro embrulhado e o índice em uso fica.
func TestCovX_LoadAll_QueryError_KeepsIndex(t *testing.T) {
	url := covXTestURL(t)
	pool, err := db.New(context.Background(), url, zap.NewNop())
	require.NoError(t, err)
	pool.Close()

	store := New()
	sentinel := Index{6: {{CommercialShortID: 6}}}
	store.Swap(sentinel)

	err = NewLoader(store, pool, nil, zap.NewNop()).LoadAll(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "index loader: query fingerprint_hashes")
	require.Equal(t, sentinel, store.Load())
}

// RunReconcileLoop reconstrói o índice sozinho a cada tick (cura reload
// perdido) e para quando o contexto é cancelado.
func TestCovX_RunReconcileLoop_RebuildsIndexAndStopsOnCancel(t *testing.T) {
	ctx, pool := newTestDB(t)
	clientID := covXSeedClient(t, ctx, pool)
	camp := seedCampaign(t, ctx, pool, clientID, "ativa", "2026-06-01", "2026-07-31")
	_, shortID := covXSeedMaterial(t, ctx, pool, clientID, camp, 3, 990_000)

	store := New()
	loader := NewLoader(store, pool, nil, zap.NewNop())
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		loader.RunReconcileLoop(loopCtx, 20*time.Millisecond)
	}()

	require.Eventually(t, func() bool { return covXCount(store.Load(), shortID) == 3 },
		10*time.Second, 10*time.Millisecond, "o tick deveria carregar os 3 hashes do material")

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunReconcileLoop não parou após cancelar o contexto")
	}
}

// Se o LoadAll de um tick falha, o loop só loga e mantém o último índice bom.
func TestCovX_RunReconcileLoop_LoadFailure_LogsAndKeepsLastGoodIndex(t *testing.T) {
	url := covXTestURL(t)
	pool, err := db.New(context.Background(), url, zap.NewNop())
	require.NoError(t, err)
	pool.Close() // todo LoadAll vai falhar

	log, logs := covXObservedLogger()
	store := New()
	good := Index{8: {{CommercialShortID: 8, TimeFrame: 3}}}
	store.Swap(good)
	loader := NewLoader(store, pool, nil, log)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		loader.RunReconcileLoop(ctx, 10*time.Millisecond)
	}()
	covXWaitLog(t, logs, "index reconcile: LoadAll failed", 2)
	cancel()
	<-done

	require.Equal(t, good, store.Load())
}
