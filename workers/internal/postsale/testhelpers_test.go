package postsale

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/db"
)

// newTestDB abre o pool do banco descartável. Sem TEST_DATABASE_URL o teste
// pula — mesma convenção do resto do repo.
//
// Não usamos TRUNCATE aqui: cada seed limpa o que criou via t.Cleanup, então
// os testes convivem com o que já existe no banco de teste em vez de zerá-lo.
func newTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	return ctx, pool
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// scenario são os ids do cenário semeado.
type scenario struct {
	ClientID   uuid.UUID
	CampaignID uuid.UUID
	MaterialID uuid.UUID
	TypeID     uuid.UUID
	AdminID    uuid.UUID
	Acima      uuid.UUID
	Devendo    uuid.UUID
	Exata      uuid.UUID
}

// seedScenario monta o cenário canônico dos testes: 1 cliente, 1 campanha de
// junho/2026, 3 emissoras e uma regra de 2 tocadas/dia entre 01 e 05/06
// (expected = 10 por emissora).
//
//	Radio Acima   → 10 in_slot + 3 orphan  → deficit 0, extras 3  → above, 100%
//	Radio Devendo →  7 in_slot             → deficit 3, extras 0  → compensation, 70%
//	Radio Exata   → 10 in_slot             → deficit 0, extras 0  → conforming
//
// As colunas da view daily_play_summary que importam aqui (migration 0041):
//
//	deficit = max(0, expected - in_slot - out_slot)
//	bonus   = max(0, in_slot - expected) + orphan
func seedScenario(t *testing.T, ctx context.Context, pool *pgxpool.Pool) scenario {
	t.Helper()
	var s scenario

	cli, err := catalog.NewClients(pool).Create(ctx, catalog.CreateClientInput{
		Name: "PosVenda " + uuid.NewString()[:8],
	})
	require.NoError(t, err, "seed client")
	s.ClientID = cli.ID
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", s.ClientID) })

	camp, err := catalog.NewCampaigns(pool).Create(ctx, catalog.CreateCampaignInput{
		Name:           "PV-" + uuid.NewString()[:8],
		ClientID:       s.ClientID,
		StartDate:      date(2026, 6, 1),
		EndDate:        date(2026, 6, 30),
		TargetStations: []uuid.UUID{},
	})
	require.NoError(t, err, "seed campaign")
	s.CampaignID = camp.ID
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM detection_campaigns WHERE campaign_id = $1", s.CampaignID)
		pool.Exec(ctx, "DELETE FROM detections WHERE campaign_id = $1", s.CampaignID)
		pool.Exec(ctx, "DELETE FROM distribution_rules WHERE campaign_id = $1", s.CampaignID)
		pool.Exec(ctx, "DELETE FROM campaigns WHERE id = $1", s.CampaignID)
	})

	// material_type + material: FK obrigatória das detections e das regras.
	s.TypeID = uuid.New()
	_, err = pool.Exec(ctx,
		`INSERT INTO material_types (id, name, color) VALUES ($1, $2, '#3b82f6')`,
		s.TypeID, "PV-"+s.TypeID.String()[:8])
	require.NoError(t, err, "seed material_type")

	mat, err := catalog.NewMaterials(pool).Create(ctx, catalog.CreateMaterialInput{
		ClientID:          s.ClientID,
		Title:             "Spot 30s",
		TypeID:            &s.TypeID,
		DurationSeconds:   30,
		MasterStoragePath: "/tmp/pv",
		MasterSHA256:      "pv-" + uuid.NewString(),
	})
	require.NoError(t, err, "seed material")
	s.MaterialID = mat.ID
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM materials WHERE id = $1", s.MaterialID)
		pool.Exec(ctx, "DELETE FROM material_types WHERE id = $1", s.TypeID)
	})

	s.Acima = seedStation(t, ctx, pool, "Radio Acima")
	s.Devendo = seedStation(t, ctx, pool, "Radio Devendo")
	s.Exata = seedStation(t, ctx, pool, "Radio Exata")

	// Regra: 01–05/06, todos os dias da semana, 2 tocadas/dia → expected 10.
	_, err = catalog.NewDistributionRules(pool).Create(ctx, catalog.CreateDistributionRuleInput{
		CampaignID:  s.CampaignID,
		TypeID:      s.TypeID,
		StationIDs:  []uuid.UUID{s.Acima, s.Devendo, s.Exata},
		MaterialIDs: []uuid.UUID{},
		StartDate:   date(2026, 6, 1),
		EndDate:     date(2026, 6, 5),
		WeekdayMask: 0b1111111,
		TimeStart:   "08:00",
		TimeEnd:     "20:00",
		PlaysPerDay: 2,
	})
	require.NoError(t, err, "seed distribution_rule")

	// Acima e Exata: 2 in_slot por dia nos 5 dias = 10.
	for day := 1; day <= 5; day++ {
		for i := 0; i < 2; i++ {
			seedDetection(t, ctx, pool, s, s.Acima, "in_slot", day, 10+i)
			seedDetection(t, ctx, pool, s, s.Exata, "in_slot", day, 10+i)
		}
	}
	// Acima ganha 3 órfãs (mídia extra fora de plano) → bonus 3.
	for i := 0; i < 3; i++ {
		seedDetection(t, ctx, pool, s, s.Acima, "orphan", 3, 15+i)
	}
	// Devendo: 2+2+2+1 = 7 in_slot em 4 dias; o 5º dia fica sem nada.
	for day, n := range map[int]int{1: 2, 2: 2, 3: 2, 4: 1} {
		for i := 0; i < n; i++ {
			seedDetection(t, ctx, pool, s, s.Devendo, "in_slot", day, 10+i)
		}
	}

	// Admin real: revoked_by/created_by têm FK pra users.
	err = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, name)
		 VALUES ($1, 'h', 'admin', 'Admin PV') RETURNING id`,
		"pv-admin-"+uuid.NewString()[:8]+"@test.local").Scan(&s.AdminID)
	require.NoError(t, err, "seed admin")
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id = $1", s.AdminID) })

	return s
}

func seedStation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	st, err := catalog.NewStations(pool).Create(ctx, catalog.CreateStationInput{
		Name:      name,
		Band:      "FM",
		StreamURL: "http://test/pv/" + uuid.NewString(),
	})
	require.NoError(t, err, "seed station")
	_, err = pool.Exec(ctx,
		`UPDATE stations SET city = 'Passo Fundo', state = 'RS',
		        frequency_mhz = 98.5, pmm = 1000, logo_url = 'https://x/logo.png'
		  WHERE id = $1`, st.ID)
	require.NoError(t, err, "seed station metadata")
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM station_thresholds WHERE station_id = $1", st.ID)
		pool.Exec(ctx, "DELETE FROM stations WHERE id = $1", st.ID)
	})
	return st.ID
}

// seedDetection insere a veiculação e a projeção canônica (detection_campaigns),
// que é de onde a view daily_play_summary lê a categoria desde a F-119.
func seedDetection(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	s scenario, stationID uuid.UUID, category string, day, hour int) {
	t.Helper()
	// 12h–20h UTC cai dentro do dia civil brasileiro (UTC-3) sem atravessar a
	// meia-noite, que é o que o for_date da view usa.
	ts := time.Date(2026, 6, day, hour, 0, 0, 0, time.UTC)
	var detID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used,
		                        category)
		VALUES ($1, $2, $3, $4, 0, 30000, 0.95, 100, 0.85, 0, 0, $5)
		RETURNING id`,
		stationID, s.MaterialID, s.CampaignID, ts, category).Scan(&detID)
	require.NoError(t, err, fmt.Sprintf("seed detection %s dia %d", category, day))

	_, err = pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, $5)`,
		detID, ts, s.CampaignID, s.MaterialID, category)
	require.NoError(t, err, "seed projeção canônica")
}
