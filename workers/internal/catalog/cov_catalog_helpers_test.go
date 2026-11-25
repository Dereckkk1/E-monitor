package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// covCWorld agrupa o mínimo de linhas que quase todo teste de repositório
// precisa: cliente, campanha, tipo, material e emissora. Tudo genérico
// ("Cliente Teste", "Rádio Teste FM") — o repo é espelhado publicamente.
type covCWorld struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	client  uuid.UUID
	camp    uuid.UUID
	typeID  uuid.UUID
	mat     uuid.UUID
	station uuid.UUID
}

// covCSeedWorld cria o mundo mínimo (campanha 2026-06-01..2026-06-30, material
// de 30s do tipo semeado, emissora FM) e registra a limpeza em ordem de FK.
func covCSeedWorld(t *testing.T, label string) *covCWorld {
	t.Helper()
	ctx, pool := newTestDB(t)

	cli, err := NewClients(pool).Create(ctx, CreateClientInput{Name: "Cliente Teste " + label})
	require.NoError(t, err)
	cmp, err := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "Campanha " + label, ClientID: cli.ID,
		StartDate:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:        time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err)
	typeID := seedType(t, ctx, pool, "covc-"+label)
	mat, err := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "Material " + label, TypeID: &typeID, DurationSeconds: 30,
		MasterStoragePath: "/tmp/covc-" + label, MasterSHA256: "covc-" + uuid.NewString(),
	})
	require.NoError(t, err)
	stat, err := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Rádio Teste FM " + label, Band: "FM",
		StreamURL: "http://example.com/covc/" + label + "/" + uuid.NewString(),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detection_campaigns WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM detections WHERE station_id = $1`, stat.ID)
		pool.Exec(ctx, `DELETE FROM distribution_rules WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM campaign_station_type_pricing WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM campaign_station_pricing WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM campaign_materials WHERE campaign_id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, cmp.ID)
		pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, mat.ID)
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, stat.ID)
	})
	return &covCWorld{ctx: ctx, pool: pool, client: cli.ID, camp: cmp.ID,
		typeID: typeID, mat: mat.ID, station: stat.ID}
}

// covCSeedStation cria uma emissora extra (com limpeza) e devolve o id.
func covCSeedStation(t *testing.T, w *covCWorld, name string) uuid.UUID {
	t.Helper()
	st, err := NewStations(w.pool).Create(w.ctx, CreateStationInput{
		Name: name, Band: "FM",
		StreamURL: "http://example.com/covc/extra/" + uuid.NewString(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		w.pool.Exec(w.ctx, `DELETE FROM detection_campaigns dc USING detections d
		                     WHERE dc.detection_id = d.id AND dc.detected_at = d.detected_at
		                       AND d.station_id = $1`, st.ID)
		w.pool.Exec(w.ctx, `DELETE FROM detections WHERE station_id = $1`, st.ID)
		w.pool.Exec(w.ctx, `DELETE FROM stations WHERE id = $1`, st.ID)
	})
	return st.ID
}

// covCSeedType cria um tipo de material extra (nome único) e devolve o id.
func covCSeedType(t *testing.T, w *covCWorld, name string) uuid.UUID {
	t.Helper()
	return seedType(t, w.ctx, w.pool, name)
}

// covCSeedMaterial cria um material do cliente do mundo (SHA único).
func covCSeedMaterial(t *testing.T, w *covCWorld, title string, dur float64) uuid.UUID {
	t.Helper()
	mat, err := NewMaterials(w.pool).Create(w.ctx, CreateMaterialInput{
		ClientID: w.client, Title: title, TypeID: &w.typeID, DurationSeconds: dur,
		MasterStoragePath: "/tmp/covc-" + title, MasterSHA256: "covc-" + uuid.NewString(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		w.pool.Exec(w.ctx, `DELETE FROM campaign_materials WHERE material_id = $1`, mat.ID)
		w.pool.Exec(w.ctx, `DELETE FROM materials WHERE id = $1`, mat.ID)
	})
	return mat.ID
}

// covCInsertDet insere uma detecção crua (sem passar pelo categorizador) com a
// categoria dada, mais a projeção canônica. Devolve o id da detecção.
func covCInsertDet(t *testing.T, w *covCWorld, material, station uuid.UUID, at time.Time, category string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, w.pool.QueryRow(w.ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used, category)
		VALUES ($1, $2, $3, $4, 0, 30000, 0.95, 100, 0.85, 0, 0, $5)
		RETURNING id`, station, material, w.camp, at, category).Scan(&id))
	_, err := w.pool.Exec(w.ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, $5)`, id, at, w.camp, material, category)
	require.NoError(t, err)
	return id
}

// covCCancelled devolve um contexto já cancelado: toda query que o recebe falha
// na hora, o que exercita o ramo "erro do banco propaga" sem depender de
// derrubar o Postgres.
func covCCancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// covCUser cria um usuário admin com e-mail @example.com (FKs de manual_by,
// ignored_by, created_by).
func covCUser(t *testing.T, w *covCWorld) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, w.pool.QueryRow(w.ctx, `
		INSERT INTO users (email, password_hash, role)
		VALUES ($1, 'x', 'admin') RETURNING id`,
		"covc-"+uuid.NewString()+"@example.com").Scan(&id))
	t.Cleanup(func() {
		w.pool.Exec(w.ctx, `DELETE FROM user_clients WHERE user_id = $1`, id)
		w.pool.Exec(w.ctx, `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

func covCF(f float64) *float64 { return &f }
