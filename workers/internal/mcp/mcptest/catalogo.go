package mcptest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
)

// DBCatalogo é o DB() que também zera as emissoras: os testes de tool semeiam
// catálogo e o guard do dbtest recusa banco com mais de 50 emissoras.
// TRUNCATE de clients (no DB) já leva campanhas, materiais e veiculações.
func DBCatalogo(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx, pool := DB(t)
	_, err := pool.Exec(ctx, `TRUNCATE stations RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
	return ctx, pool
}

// Dia lê AAAA-MM-DD como data de calendário (meia-noite UTC, como o DATE do pgx).
func Dia(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

// Quando monta um instante em America/Sao_Paulo (dia AAAA-MM-DD, hora HH:MM).
func Quando(diaISO, hora string) time.Time {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		loc = time.FixedZone("BRT", -3*3600)
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", diaISO+" "+hora, loc)
	if err != nil {
		panic(err)
	}
	return t
}

func Cliente(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nome string) uuid.UUID {
	t.Helper()
	c, err := catalog.NewClients(pool).Create(ctx, catalog.CreateClientInput{Name: nome})
	require.NoError(t, err)
	return c.ID
}

// Emissora cria uma emissora FM com PMM (pmm 0 = sem PMM).
func Emissora(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nome string, pmm float64) uuid.UUID {
	t.Helper()
	s, err := catalog.NewStations(pool).Create(ctx, catalog.CreateStationInput{
		Name: nome, Band: "FM", StreamURL: "http://teste.mcp/" + uuid.NewString(),
	})
	require.NoError(t, err)
	if pmm > 0 {
		_, err = pool.Exec(ctx, `UPDATE stations SET pmm = $1 WHERE id = $2`, pmm, s.ID)
		require.NoError(t, err)
	}
	return s.ID
}

// TipoMaterial cria um tipo com nome único (a tabela não é truncada).
func TipoMaterial(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nome string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO material_types (id, name, color) VALUES ($1, $2, '#3b82f6')`,
		id, nome+" "+id.String()[:8])
	require.NoError(t, err)
	return id
}

func Material(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cliente, tipo uuid.UUID, titulo string) uuid.UUID {
	t.Helper()
	m, err := catalog.NewMaterials(pool).Create(ctx, catalog.CreateMaterialInput{
		ClientID: cliente, Title: titulo, TypeID: &tipo, DurationSeconds: 30,
		MasterStoragePath: "/tmp/mcp-" + titulo, MasterSHA256: "mcp-" + uuid.NewString(),
	})
	require.NoError(t, err)
	return m.ID
}

// Campanha cria a campanha (estado = default da coluna) com as emissoras-alvo.
func Campanha(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cliente uuid.UUID, nome, inicio, fim string, emissoras ...uuid.UUID) uuid.UUID {
	t.Helper()
	if emissoras == nil {
		emissoras = []uuid.UUID{} // target_stations é NOT NULL
	}
	c, err := catalog.NewCampaigns(pool).Create(ctx, catalog.CreateCampaignInput{
		ClientID: cliente, Name: nome, StartDate: Dia(inicio), EndDate: Dia(fim), TargetStations: emissoras,
	})
	require.NoError(t, err)
	return c.ID
}

// EstadoCampanha força o estado; "cancelada" grava cancelled_at = now().
func EstadoCampanha(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campanha uuid.UUID, estado string) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		UPDATE campaigns SET status = $2,
		       cancelled_at = CASE WHEN $2 = 'cancelada' THEN now() ELSE cancelled_at END
		WHERE id = $1`, campanha, estado)
	require.NoError(t, err)
}

func Vincular(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campanha, material uuid.UUID, emissoras ...uuid.UUID) {
	t.Helper()
	if emissoras == nil {
		emissoras = []uuid.UUID{}
	}
	require.NoError(t, catalog.NewCampaignMaterials(pool).Link(ctx, campanha, material, emissoras))
}

// Regra cria uma regra geral (todos os materiais do tipo) numa emissora.
// mascara: bit 0 = domingo … bit 6 = sábado (127 = todos os dias, 62 = seg a sex).
func Regra(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campanha, tipo, emissora uuid.UUID,
	inicio, fim string, mascara int16, faixaIni, faixaFim string, porDia int16) uuid.UUID {
	t.Helper()
	r, err := catalog.NewDistributionRules(pool).Create(ctx, catalog.CreateDistributionRuleInput{
		CampaignID: campanha, TypeID: tipo, StationIDs: []uuid.UUID{emissora}, MaterialIDs: []uuid.UUID{},
		Name: "Regra " + faixaIni, StartDate: Dia(inicio), EndDate: Dia(fim), WeekdayMask: mascara,
		TimeStart: faixaIni, TimeEnd: faixaFim, PlaysPerDay: porDia,
	})
	require.NoError(t, err)
	return r.ID
}

// Ajuste cria o ajuste do dia (distribution_override) de uma célula.
func Ajuste(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campanha, tipo, emissora uuid.UUID,
	data string, esperadas int16, faixaIni, faixaFim string) {
	t.Helper()
	require.NoError(t, catalog.NewDistributionOverrides(pool).Upsert(ctx, catalog.UpsertOverrideInput{
		CampaignID: campanha, TypeID: tipo, StationID: emissora, ForDate: Dia(data),
		PlaysExpected: esperadas, TimeStart: faixaIni, TimeEnd: faixaFim,
	}))
}

// Preco cria preço por inserção (unitário por tipo) numa emissora.
func Preco(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campanha, emissora, tipo uuid.UUID, unitario float64) {
	t.Helper()
	_, err := catalog.NewPricing(pool).Upsert(ctx, catalog.UpsertInput{
		CampaignID: campanha, StationID: emissora, Mode: catalog.PricingModePerInsertion,
		PerType: []catalog.TypePricing{{TypeID: tipo, UnitValue: unitario}},
	})
	require.NoError(t, err)
}

// Veiculacao insere uma tocada com categoria explícita + a projeção canônica
// (detection_campaigns), sem passar pelo categorizador — o mesmo atalho do
// insSeedDetection do catalog.
func Veiculacao(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campanha, material, emissora uuid.UUID,
	categoria string, quando time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used, category)
		VALUES ($1, $2, $3, $4, 0, 30000, 0.95, 100, 0.85, 0, 0, $5)
		RETURNING id`, emissora, material, campanha, quando, categoria).Scan(&id))
	_, err := pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, $5)`, id, quando, campanha, material, categoria)
	require.NoError(t, err)
	return id
}

// Projecao acrescenta uma atribuição extra (multi-atribuição) a uma tocada.
func Projecao(t *testing.T, ctx context.Context, pool *pgxpool.Pool, veiculacao uuid.UUID, quando time.Time,
	campanha, material uuid.UUID, categoria string) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, $5)`, veiculacao, quando, campanha, material, categoria)
	require.NoError(t, err)
}
