package mcptest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/postsale"
)

// PMMTarget grava o PMM no target do cliente numa emissora.
func PMMTarget(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cliente, emissora uuid.UUID, pmm int) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO client_station_pmm (client_id, station_id, pmm_target) VALUES ($1, $2, $3)`,
		cliente, emissora, pmm)
	require.NoError(t, err)
}

// Queda grava um evento de stream fora do ar (o que as telas de falha cruzam).
// stream_health_events não tem FK para stations: o TRUNCATE do DBCatalogo não
// a limpa, mas as emissoras de cada teste são novas (uuid), então eventos de
// testes anteriores ficam órfãos e as telas (que cruzam com stations) os ignoram.
func Queda(t *testing.T, ctx context.Context, pool *pgxpool.Pool, emissora uuid.UUID, inicio time.Time, duracaoSeg int) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO stream_health_events (station_id, event_type, event_at, duration_seconds)
		VALUES ($1, 'down', $2, $3)`, emissora, inicio, duracaoSeg)
	require.NoError(t, err)
}

// CidadeAlcance é uma cidade no alcance de uma emissora (fixture Cobertura).
type CidadeAlcance struct {
	IBGE int
	Nome string
	Km   float64
}

// Cobertura dá à emissora coordenada, raio Anatel e cidades no alcance.
// contorno e alcance em km; a primeira cidade é a sede (is_home).
func Cobertura(t *testing.T, ctx context.Context, pool *pgxpool.Pool, emissora uuid.UUID, classe string,
	contorno, alcance float64, cidades ...CidadeAlcance) {
	t.Helper()
	_, err := pool.Exec(ctx, `UPDATE stations SET latitude = -23.5, longitude = -46.6, city = 'Sede', state = 'SP',
		anatel_class = $2, anatel_coverage_km = $3, anatel_reach_km = $4 WHERE id = $1`,
		emissora, classe, contorno, alcance)
	require.NoError(t, err)
	for i, c := range cidades {
		_, err = pool.Exec(ctx, `INSERT INTO station_coverage_cities
			(station_id, ibge_code, city, state, distance_km, is_home, latitude, longitude)
			VALUES ($1, $2, $3, 'SP', $4, $5, -23.5, -46.6)`, emissora, c.IBGE, c.Nome, c.Km, i == 0)
		require.NoError(t, err)
	}
}

// Assertividade grava o fato diário da assertividade (o card lê um SUM dele).
func Assertividade(t *testing.T, ctx context.Context, pool *pgxpool.Pool, dia time.Time, campanha, emissora, material uuid.UUID,
	auto, miss, semFingerprint int) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO assertiveness_daily
		(for_date, campaign_id, station_id, commercial_id, auto, miss, x_no_fingerprint)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, dia, campanha, emissora, material, auto, miss, semFingerprint)
	require.NoError(t, err)
}

// PosVenda cria um pós-venda com um bloco da campanha e os destinatários; enviado
// = true congela o payload (MarkSent) e marca a 1ª pessoa como "abriu".
func PosVenda(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cliente, campanha uuid.UUID, titulo, de, ate string,
	enviado bool, payload *postsale.Payload, pessoas ...postsale.RecipientInput) uuid.UUID {
	t.Helper()
	repo := postsale.NewRepo(pool)
	rep, err := repo.CreateDraft(ctx, postsale.CreateDraftInput{ClientID: cliente, Title: titulo})
	require.NoError(t, err)
	require.NoError(t, repo.ReplaceBlocks(ctx, rep.ID, []postsale.BlockRow{{CampaignID: campanha, From: Dia(de), To: Dia(ate)}}))
	for i := range pessoas {
		if pessoas[i].Token == "" {
			pessoas[i].Token = uuid.NewString() // o repo grava o token que recebe
		}
	}
	if len(pessoas) > 0 {
		dest, err := repo.CreateRecipients(ctx, rep.ID, pessoas)
		require.NoError(t, err)
		if enviado {
			_, err = pool.Exec(ctx, `UPDATE post_sale_report_recipients SET opened_at = now(), open_count = 2, email_status = 'sent' WHERE id = $1`, dest[0].ID)
			require.NoError(t, err)
		}
	}
	if enviado {
		b, err := json.Marshal(payload)
		require.NoError(t, err)
		require.NoError(t, repo.MarkSent(ctx, rep.ID, b))
	}
	return rep.ID
}
