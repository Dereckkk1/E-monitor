package assertiveness

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/calendar"
	"radiocheck/internal/db"
)

func covZPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return ctx, pool
}

// covZWorld e o mundo minimo de um cenario de assertividade.
type covZWorld struct {
	ctx    context.Context
	pool   *pgxpool.Pool
	client uuid.UUID
	camp   uuid.UUID
	user   uuid.UUID
}

func covZSeed(t *testing.T) *covZWorld {
	t.Helper()
	ctx, pool := covZPool(t)
	w := &covZWorld{ctx: ctx, pool: pool}

	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO clients (name) VALUES ('Cliente Teste Assert') RETURNING id`).Scan(&w.client))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		 VALUES ($1, 'Campanha Teste Assert', '2026-06-01', '2026-08-30', 'ativa') RETURNING id`, w.client).Scan(&w.camp))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role) VALUES ($1, 'x', 'admin') RETURNING id`,
		"assert-"+uuid.NewString()+"@example.com").Scan(&w.user))

	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM assertiveness_daily WHERE campaign_id=$1`, w.camp)
		_, _ = pool.Exec(bg, `DELETE FROM detection_campaigns WHERE campaign_id=$1`, w.camp)
		_, _ = pool.Exec(bg, `DELETE FROM detections WHERE campaign_id=$1`, w.camp)
		_, _ = pool.Exec(bg, `DELETE FROM campaign_materials WHERE campaign_id=$1`, w.camp)
		_, _ = pool.Exec(bg, `DELETE FROM campaigns WHERE id=$1`, w.camp)
		_, _ = pool.Exec(bg, `DELETE FROM materials WHERE client_id=$1`, w.client)
		_, _ = pool.Exec(bg, `DELETE FROM clients WHERE id=$1`, w.client)
		_, _ = pool.Exec(bg, `DELETE FROM users WHERE id=$1`, w.user)
	})
	return w
}

func (w *covZWorld) station(t *testing.T, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, w.pool.QueryRow(w.ctx,
		`INSERT INTO stations (name, band, stream_url) VALUES ($1,'FM','http://127.0.0.1:1/teste') RETURNING id`, name).Scan(&id))
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = w.pool.Exec(bg, `DELETE FROM assertiveness_daily WHERE station_id=$1`, id)
		_, _ = w.pool.Exec(bg, `DELETE FROM stream_health_events WHERE station_id=$1`, id)
		_, _ = w.pool.Exec(bg, `DELETE FROM stations WHERE id=$1`, id)
	})
	return id
}

// material cria um material do cliente; fp nil = sem fingerprint gerado.
func (w *covZWorld) material(t *testing.T, title string, fp *time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, w.pool.QueryRow(w.ctx,
		`INSERT INTO materials (client_id, title, duration_seconds, master_storage_path, master_sha256, fingerprint_generated_at)
		 VALUES ($1,$2,30,'covz/'||$2,'covz-'||$3::text,$4) RETURNING id`,
		w.client, title, uuid.NewString(), fp).Scan(&id))
	return id
}

// detection insere uma deteccao (auto ou manual) e a projecao canonica.
// state: "" (aprovada), "retracted", "ignored", "audit_rejected".
func (w *covZWorld) detection(t *testing.T, station, material uuid.UUID, at time.Time, manual bool, state string) {
	t.Helper()
	var id uuid.UUID
	var manualAt, manualBy any
	status := "available"
	if manual {
		manualAt, manualBy = at.Add(time.Hour), w.user
		status = "missing"
	}
	if state == "audit_rejected" {
		status = "audit_rejected"
	}
	var retracted, ignored any
	switch state {
	case "retracted":
		retracted = at.Add(time.Minute)
	case "ignored":
		ignored = at.Add(time.Minute)
	}
	require.NoError(t, w.pool.QueryRow(w.ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used, category,
		                        evidence_status, manual_at, manual_by, retracted_at, ignored_at)
		VALUES ($1,$2,$3,$4,0,30000,0.95,100,0.85,0,0,'in_slot',$5,$6,$7,$8,$9)
		RETURNING id`,
		station, material, w.camp, at, status, manualAt, manualBy, retracted, ignored).Scan(&id))
	_, err := w.pool.Exec(w.ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1,$2,$3,$4,'in_slot')`, id, at, w.camp, material)
	require.NoError(t, err)
}

// health insere um stream_health_event (durSecs<0 => duration_seconds NULL).
func (w *covZWorld) health(t *testing.T, station uuid.UUID, typ string, at time.Time, durSecs int) {
	t.Helper()
	var dur any
	if durSecs >= 0 {
		dur = durSecs
	}
	_, err := w.pool.Exec(w.ctx,
		`INSERT INTO stream_health_events (station_id, event_type, event_at, duration_seconds) VALUES ($1,$2,$3,$4)`,
		station, typ, at, dur)
	require.NoError(t, err)
}

// totals soma as colunas da tabela para a campanha do mundo no intervalo.
type covZTotals struct {
	Auto, Miss, Dup, NoFP, Down, Unmon int
	Rows                               int
}

func (w *covZWorld) totals(t *testing.T, from, to string) covZTotals {
	t.Helper()
	var x covZTotals
	require.NoError(t, w.pool.QueryRow(w.ctx, `
		SELECT COALESCE(SUM(auto),0), COALESCE(SUM(miss),0), COALESCE(SUM(x_duplicate),0),
		       COALESCE(SUM(x_no_fingerprint),0), COALESCE(SUM(x_stream_down),0),
		       COALESCE(SUM(x_unmonitored),0), COUNT(*)
		FROM assertiveness_daily
		WHERE campaign_id=$1 AND for_date BETWEEN $2::date AND $3::date`, w.camp, from, to).
		Scan(&x.Auto, &x.Miss, &x.Dup, &x.NoFP, &x.Down, &x.Unmon, &x.Rows))
	return x
}

func brt(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, calendar.BR)
}
