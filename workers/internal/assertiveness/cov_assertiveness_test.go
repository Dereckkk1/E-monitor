package assertiveness

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"radiocheck/internal/calendar"
)

// covZJulyScenario monta um julho/2026 com um exemplo de cada balde:
//
//	auto=2  (A, C)          dup=1 (B)      miss=1 (D)
//	no_fingerprint=2 (E,F)  stream_down=3 (G,H,I)   unmonitored=1 (J)
//
// mais tres deteccoes que NAO podem contar (retratada, ignorada, audit_rejected)
// e uma fora da janela de julho.
func covZJulyScenario(t *testing.T, w *covZWorld) {
	t.Helper()
	early := brt(2026, time.June, 1, 0, 0)
	late := brt(2026, time.July, 20, 0, 0)
	mReady := w.material(t, "Material Pronto", &early)
	mLate := w.material(t, "Material Tardio", &late)
	mNone := w.material(t, "Material Sem Fingerprint", nil)

	s1 := w.station(t, "Radio Teste Um FM")
	s2 := w.station(t, "Radio Teste Dois FM")
	s3 := w.station(t, "Radio Teste Tres FM")

	// S1: acerto + redigitacao no mesmo minuto (duplicate).
	w.detection(t, s1, mReady, brt(2026, time.July, 5, 10, 0), false, "")
	w.detection(t, s1, mReady, brt(2026, time.July, 5, 10, 3), true, "")
	// S1: acerto a tarde + manual de manha, longe da janela de 5min => miss.
	w.detection(t, s1, mReady, brt(2026, time.July, 6, 15, 0), false, "")
	w.detection(t, s1, mReady, brt(2026, time.July, 6, 11, 0), true, "")
	// S1: material fingerprintado DEPOIS da tocada / nunca fingerprintado.
	w.detection(t, s1, mLate, brt(2026, time.July, 7, 9, 0), true, "")
	w.detection(t, s1, mNone, brt(2026, time.July, 7, 10, 0), true, "")

	// S2: down com duracao explicita (11h-13h), manual as 12h.
	w.health(t, s2, "down", brt(2026, time.July, 8, 11, 0), 7200)
	w.detection(t, s2, mReady, brt(2026, time.July, 8, 12, 0), true, "")
	// S2: down sem duracao, fechado pelo proximo 'up' (13h).
	w.health(t, s2, "down", brt(2026, time.July, 10, 11, 30), -1)
	w.health(t, s2, "up", brt(2026, time.July, 10, 13, 0), -1)
	w.detection(t, s2, mReady, brt(2026, time.July, 10, 12, 0), true, "")
	// S2: manual 1 minuto ANTES do down comecar: so cai na janela por causa da folga de 120s.
	w.health(t, s2, "down", brt(2026, time.July, 11, 12, 5), 600)
	w.detection(t, s2, mReady, brt(2026, time.July, 11, 12, 4), true, "")

	// S3: nenhum sinal de vida no dia => unmonitored.
	w.detection(t, s3, mReady, brt(2026, time.July, 9, 12, 0), true, "")

	// Nao contam: filtro canonico de veiculacao aprovada.
	w.detection(t, s1, mReady, brt(2026, time.July, 12, 10, 0), true, "retracted")
	w.detection(t, s1, mReady, brt(2026, time.July, 12, 10, 10), false, "ignored")
	w.detection(t, s1, mReady, brt(2026, time.July, 12, 11, 0), true, "audit_rejected")

	// Fora da janela de julho.
	w.detection(t, s1, mReady, brt(2026, time.August, 2, 10, 0), false, "")
}

func TestCovZ_Recompute_ClassifiesEveryBucket(t *testing.T) {
	w := covZSeed(t)
	covZJulyScenario(t, w)
	repo := New(w.pool)

	from, to := ClosedMonth(brt(2026, time.August, 7, 10, 0))
	n, err := repo.Recompute(w.ctx, from, to)
	require.NoError(t, err)

	got := w.totals(t, "2026-07-01", "2026-07-31")
	assert.Equal(t, covZTotals{Auto: 2, Miss: 1, Dup: 1, NoFP: 2, Down: 3, Unmon: 1, Rows: 8}, got)
	// >= porque o recompute cobre TODAS as campanhas da base; as 8 linhas deste cenario estao entre elas.
	assert.GreaterOrEqual(t, n, int64(8), "linhas inseridas = grupos (dia, campanha, emissora, material)")

	// Idempotente: recomputar a mesma janela nao duplica nada.
	_, err = repo.Recompute(w.ctx, from, to)
	require.NoError(t, err)
	assert.Equal(t, got, w.totals(t, "2026-07-01", "2026-07-31"))

	// A tocada de agosto nao entrou na janela de julho.
	assert.Equal(t, covZTotals{}, w.totals(t, "2026-08-01", "2026-08-31"))
}

func TestCovZ_Recompute_ReplacesWindowAndKeepsOutside(t *testing.T) {
	w := covZSeed(t)
	covZJulyScenario(t, w)
	repo := New(w.pool)
	// Pega qualquer emissora/material do cenario para forjar linhas de fato.
	var station, material uuid.UUID
	require.NoError(t, w.pool.QueryRow(w.ctx,
		`SELECT station_id, commercial_id FROM detections WHERE campaign_id=$1 LIMIT 1`, w.camp).Scan(&station, &material))

	insert := func(day string, auto int) {
		_, err := w.pool.Exec(w.ctx,
			`INSERT INTO assertiveness_daily (for_date, campaign_id, station_id, commercial_id, auto) VALUES ($1::date,$2,$3,$4,$5)`,
			day, w.camp, station, material, auto)
		require.NoError(t, err)
	}
	insert("2026-07-15", 77) // lixo dentro da janela: tem que sumir
	insert("2026-06-30", 9)  // fora da janela: tem que sobreviver

	from, to := ClosedMonth(brt(2026, time.August, 7, 10, 0))
	_, err := repo.Recompute(w.ctx, from, to)
	require.NoError(t, err)

	assert.Equal(t, 2, w.totals(t, "2026-07-01", "2026-07-31").Auto, "linha forjada de 07-15 foi substituida")
	assert.Equal(t, 9, w.totals(t, "2026-06-30", "2026-06-30").Auto, "fora da janela e preservado")
}

func TestCovZ_Recompute_DatabaseErrors(t *testing.T) {
	w := covZSeed(t)
	repo := New(w.pool)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := repo.Recompute(cancelled, brt(2026, time.July, 1, 0, 0), brt(2026, time.July, 31, 0, 0))
	require.Error(t, err)

	_, err = repo.Get(cancelled, brt(2026, time.August, 7, 10, 0), Filter{})
	require.Error(t, err)
}

func TestCovZ_Get_SummaryAndTrend(t *testing.T) {
	w := covZSeed(t)
	covZJulyScenario(t, w)
	repo := New(w.pool)
	now := brt(2026, time.August, 7, 10, 0)

	from, to := ClosedMonth(now)
	_, err := repo.Recompute(w.ctx, from, to)
	require.NoError(t, err)

	// Junho: 9 auto e 1 miss => 90%.
	var station, material uuid.UUID
	require.NoError(t, w.pool.QueryRow(w.ctx,
		`SELECT station_id, commercial_id FROM detections WHERE campaign_id=$1 LIMIT 1`, w.camp).Scan(&station, &material))
	_, err = w.pool.Exec(w.ctx,
		`INSERT INTO assertiveness_daily (for_date, campaign_id, station_id, commercial_id, auto, miss) VALUES ('2026-06-15',$1,$2,$3,9,1)`,
		w.camp, station, material)
	require.NoError(t, err)

	// Escopo na campanha do cenario: os totais exatos nao dependem do que outros testes deixam na base.
	res, err := repo.Get(w.ctx, now, Filter{CampaignIDs: []uuid.UUID{w.camp}})
	require.NoError(t, err)

	assert.Equal(t, "2026-07", res.Current.Month)
	assert.Equal(t, int64(2), res.Current.Auto)
	assert.Equal(t, int64(1), res.Current.Miss)
	assert.Equal(t, int64(1), res.Current.Duplicate)
	assert.Equal(t, int64(2), res.Current.NoFingerprint)
	assert.Equal(t, int64(3), res.Current.StreamDown)
	assert.Equal(t, int64(1), res.Current.Unmonitored)
	assert.Equal(t, int64(8), res.Current.ManualTotal, "miss + todas as desculpadas")
	require.NotNil(t, res.Current.Pct)
	assert.InDelta(t, 100.0*2/3, *res.Current.Pct, 0.001, "pct = auto / (auto + miss); desculpadas ficam fora da conta")

	require.NotNil(t, res.Previous)
	assert.Equal(t, "2026-06", res.Previous.Month)
	require.NotNil(t, res.Previous.Pct)
	assert.InDelta(t, 90.0, *res.Previous.Pct, 0.001)
	require.NotNil(t, res.DeltaPP)
	assert.InDelta(t, 100.0*2/3-90.0, *res.DeltaPP, 0.001)
}

func TestCovZ_Get_Filters(t *testing.T) {
	w := covZSeed(t)
	covZJulyScenario(t, w)
	repo := New(w.pool)
	now := brt(2026, time.August, 7, 10, 0)
	from, to := ClosedMonth(now)
	_, err := repo.Recompute(w.ctx, from, to)
	require.NoError(t, err)

	// Sem filtro soma a base inteira: no minimo o cenario deste teste.
	global, err := repo.Get(w.ctx, now, Filter{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, global.Current.Auto, int64(2))
	require.NotNil(t, global.Current.Pct)

	other := uuid.New()
	cases := []struct {
		name     string
		filter   Filter
		wantAuto int64
		wantPct  bool
	}{
		{"cliente da campanha", Filter{ClientID: &w.client}, 2, true},
		{"cliente sem dados", Filter{ClientID: &other}, 0, false},
		{"campanha especifica", Filter{CampaignIDs: []uuid.UUID{w.camp}}, 2, true},
		{"campanha inexistente", Filter{CampaignIDs: []uuid.UUID{other}}, 0, false},
		{"cliente certo e campanha errada", Filter{ClientID: &w.client, CampaignIDs: []uuid.UUID{other}}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := repo.Get(w.ctx, now, c.filter)
			require.NoError(t, err)
			assert.Equal(t, c.wantAuto, res.Current.Auto)
			if c.wantPct {
				assert.NotNil(t, res.Current.Pct)
			} else {
				assert.Nil(t, res.Current.Pct, "sem veiculacao o percentual nao existe (nem 0% nem 100%)")
				assert.Nil(t, res.DeltaPP)
			}
			assert.NotNil(t, res.Previous)
		})
	}
}

func TestCovZ_Get_EmptyMonthHasNoPctAndNoDelta(t *testing.T) {
	_, pool := covZPool(t)
	// Janeiro/2020 nao tem nada: percentual e tendencia ausentes, volumes zerados.
	res, err := New(pool).Get(context.Background(), brt(2020, time.February, 10, 10, 0), Filter{})
	require.NoError(t, err)
	assert.Equal(t, "2020-01", res.Current.Month)
	assert.Equal(t, int64(0), res.Current.Auto+res.Current.Miss+res.Current.ManualTotal)
	assert.Nil(t, res.Current.Pct)
	require.NotNil(t, res.Previous)
	assert.Equal(t, "2019-12", res.Previous.Month)
	assert.Nil(t, res.DeltaPP)
}

func TestCovZ_Window(t *testing.T) {
	from, to := Window(brt(2026, time.August, 7, 10, 0))
	assert.Equal(t, "2026-06-01", from.In(calendar.BR).Format("2006-01-02"), "1o dia de dois meses atras")
	assert.Equal(t, time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC), to, "ate hoje (dia civil, meia-noite UTC)")

	// Dia 1o as 00h BRT ainda e "hoje" no Brasil (mesmo sendo 03h UTC).
	from, to = Window(time.Date(2026, 3, 1, 3, 0, 0, 0, time.UTC))
	assert.Equal(t, "2026-01-01", from.In(calendar.BR).Format("2006-01-02"))
	assert.Equal(t, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), to)
}

func TestCovZ_NewScheduler_Defaults(t *testing.T) {
	s := NewScheduler(nil, nil, nil)
	require.NotNil(t, s.log)
	assert.Equal(t, 6*time.Hour, s.Interval)
	require.NotNil(t, s.NowFn)
	assert.WithinDuration(t, time.Now(), s.NowFn(), time.Minute)
}

func TestCovZ_Scheduler_Tick(t *testing.T) {
	w := covZSeed(t)
	covZJulyScenario(t, w)
	repo := New(w.pool)

	core, logs := observer.New(zap.InfoLevel)
	s := NewScheduler(w.pool, repo, zap.New(core))
	s.NowFn = func() time.Time { return brt(2026, time.August, 7, 10, 0) }

	t.Run("outra replica com o lock: nada e recomputado", func(t *testing.T) {
		conn, err := w.pool.Acquire(w.ctx)
		require.NoError(t, err)
		defer conn.Release()
		var got bool
		require.NoError(t, conn.QueryRow(w.ctx, `SELECT pg_try_advisory_lock($1)`, advisoryLockKey).Scan(&got))
		require.True(t, got)
		defer func() { _, _ = conn.Exec(w.ctx, `SELECT pg_advisory_unlock($1)`, advisoryLockKey) }()

		s.tick(w.ctx)
		assert.Equal(t, covZTotals{}, w.totals(t, "2026-06-01", "2026-08-31"))
		assert.Zero(t, logs.FilterMessage("assertiveness recomputada").Len())
	})

	t.Run("com o lock livre recomputa a janela larga", func(t *testing.T) {
		s.tick(w.ctx)
		assert.Equal(t, covZTotals{Auto: 2, Miss: 1, Dup: 1, NoFP: 2, Down: 3, Unmon: 1, Rows: 8}, w.totals(t, "2026-07-01", "2026-07-31"))
		assert.Equal(t, 1, logs.FilterMessage("assertiveness recomputada").Len())
	})

	t.Run("lock e liberado ao final do tick", func(t *testing.T) {
		conn, err := w.pool.Acquire(w.ctx)
		require.NoError(t, err)
		defer conn.Release()
		var got bool
		require.NoError(t, conn.QueryRow(w.ctx, `SELECT pg_try_advisory_lock($1)`, advisoryLockKey).Scan(&got))
		assert.True(t, got, "o tick nao pode deixar o advisory lock preso")
		_, _ = conn.Exec(w.ctx, `SELECT pg_advisory_unlock($1)`, advisoryLockKey)
	})
}

func TestCovZ_Recompute_InsertFailureRollsBackTheDelete(t *testing.T) {
	w := covZSeed(t)
	covZJulyScenario(t, w)
	var station, material uuid.UUID
	require.NoError(t, w.pool.QueryRow(w.ctx,
		`SELECT station_id, commercial_id FROM detections WHERE campaign_id=$1 LIMIT 1`, w.camp).Scan(&station, &material))
	_, err := w.pool.Exec(w.ctx,
		`INSERT INTO assertiveness_daily (for_date, campaign_id, station_id, commercial_id, auto) VALUES ('2026-07-15',$1,$2,$3,77)`,
		w.camp, station, material)
	require.NoError(t, err)

	// Trigger que derruba todo INSERT novo: simula falha no meio da transacao.
	_, err = w.pool.Exec(w.ctx, `CREATE OR REPLACE FUNCTION covz_fail_insert() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'falha de teste'; END $$ LANGUAGE plpgsql`)
	require.NoError(t, err)
	_, err = w.pool.Exec(w.ctx, `CREATE TRIGGER covz_fail BEFORE INSERT ON assertiveness_daily FOR EACH ROW EXECUTE FUNCTION covz_fail_insert()`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS covz_fail ON assertiveness_daily`)
		_, _ = w.pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS covz_fail_insert()`)
	})

	from, to := ClosedMonth(brt(2026, time.August, 7, 10, 0))
	_, err = New(w.pool).Recompute(w.ctx, from, to)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert window")
	assert.Equal(t, 77, w.totals(t, "2026-07-01", "2026-07-31").Auto,
		"o DELETE foi desfeito junto: a janela nao fica vazia por uma falha no INSERT")

	// O scheduler registra a falha em vez de derrubar o processo.
	core, logs := observer.New(zap.ErrorLevel)
	s := NewScheduler(w.pool, New(w.pool), zap.New(core))
	s.NowFn = func() time.Time { return brt(2026, time.August, 7, 10, 0) }
	require.NotPanics(t, func() { s.tick(w.ctx) })
	assert.Equal(t, 1, logs.FilterMessage("assertiveness: recompute falhou").Len())
}

func TestCovZ_Scheduler_TickAcquireFailure(t *testing.T) {
	_, pool := covZPool(t)
	core, logs := observer.New(zap.WarnLevel)
	s := NewScheduler(pool, New(pool), zap.New(core))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	s.tick(cancelled)
	assert.Equal(t, 1, logs.FilterMessage("assertiveness: acquire falhou").Len())
}

func TestCovZ_Scheduler_RunTicksAndStops(t *testing.T) {
	_, pool := covZPool(t)
	s := NewScheduler(pool, New(pool), nil)
	s.Interval = 20 * time.Millisecond
	var calls atomic.Int32
	s.NowFn = func() time.Time {
		calls.Add(1)
		return brt(2020, time.February, 10, 10, 0) // janela sem dados: recompute barato
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	require.Eventually(t, func() bool { return calls.Load() >= 3 }, 20*time.Second, 20*time.Millisecond,
		"tick inicial + ticks do ticker")
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Scheduler.Run nao terminou apos cancelar")
	}
}
