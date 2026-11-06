package recording

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/db"
)

func newTestStore(t *testing.T) (context.Context, *Store, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return ctx, NewStore(pool), pool
}

func seedStation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, streamURL string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO stations (name, band, frequency_mhz, city, state, stream_url)
		VALUES ($1, 'FM', 99.9, 'Teste', 'SC', $2) RETURNING id`,
		"Gravacao Teste "+uuid.NewString()[:8], streamURL).Scan(&id))
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM recordings WHERE station_id = $1`, id) //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, id)           //nolint:errcheck
	})
	return id
}

func TestStore_InsertBarraSobreposicao(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	t0 := time.Now().Add(time.Hour).Truncate(time.Second)

	id, err := s.Insert(ctx, st, "R", nil, t0, t0.Add(time.Hour))
	require.NoError(t, err)

	_, err = s.Insert(ctx, st, "R", nil, t0.Add(30*time.Minute), t0.Add(90*time.Minute))
	require.ErrorIs(t, err, ErrOverlap)

	// Encostada não é sobreposição.
	_, err = s.Insert(ctx, st, "R", nil, t0.Add(time.Hour), t0.Add(2*time.Hour))
	require.NoError(t, err)

	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusProgramada, got.Status)
	require.False(t, got.HasAudio)
	require.Empty(t, got.Gaps)
	require.NotNil(t, got.Gaps)
}

func TestStore_Get_NaoEncontrada(t *testing.T) {
	ctx, s, _ := newTestStore(t)
	_, err := s.Get(ctx, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_CicloCompleto(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)
	id, err := s.Insert(ctx, st, "R", nil, now.Add(time.Minute), now.Add(time.Hour))
	require.NoError(t, err)

	// Ainda não chegou a hora.
	_, err = s.ClaimDue(ctx, now, now)
	require.NoError(t, err)
	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusProgramada, got.Status)

	_, err = s.ClaimDue(ctx, now.Add(2*time.Minute), now)
	require.NoError(t, err)
	got, err = s.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusGravando, got.Status)

	first, err := s.MarkDropAlerted(ctx, id)
	require.NoError(t, err)
	require.True(t, first)
	second, err := s.MarkDropAlerted(ctx, id)
	require.NoError(t, err)
	require.False(t, second, "o e-mail de queda sai uma vez só")

	require.NoError(t, s.MarkProcessing(ctx, id))

	key := "recordings/2026/11/x.m4a"
	size := int64(10)
	cov := 3540
	exp := now.Add(-time.Hour) // já vencida, para o Expired achar
	require.NoError(t, s.Complete(ctx, id, CompleteInput{
		Status: StatusParcial, ObjectKey: &key, SizeBytes: &size, CoveredSeconds: &cov,
		Gaps:       []Gap{{From: now, To: now.Add(time.Minute)}},
		FinishedAt: now, ExpiresAt: &exp,
	}))

	got, err = s.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusParcial, got.Status)
	require.True(t, got.HasAudio)
	require.Len(t, got.Gaps, 1)

	expired, err := s.Expired(ctx, now)
	require.NoError(t, err)
	var achou bool
	for _, r := range expired {
		achou = achou || r.ID == id
	}
	require.True(t, achou)

	require.NoError(t, s.MarkExpired(ctx, id))
	got, err = s.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusExpirada, got.Status)
	require.False(t, got.HasAudio)
}

func seedUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, string) {
	t.Helper()
	email := "gravacao-" + uuid.NewString()[:8] + "@teste.local"
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role, name) VALUES ($1, 'x', 'admin', $1) RETURNING id`,
		email).Scan(&id))
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id) //nolint:errcheck
	})
	return id, email
}

func TestStore_FailMissed(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	user, email := seedUser(t, ctx, pool)
	now := time.Now().Truncate(time.Second)
	id, err := s.Insert(ctx, st, "R", &user, now.Add(-2*time.Hour), now.Add(-time.Hour))
	require.NoError(t, err)
	orphan, err := s.Insert(ctx, st, "R", nil, now.Add(-4*time.Hour), now.Add(-3*time.Hour))
	require.NoError(t, err)

	missed, err := s.FailMissed(ctx, now, "motivo")
	require.NoError(t, err)
	byID := map[uuid.UUID]Recording{}
	for _, r := range missed {
		byID[r.ID] = r
	}

	// O e-mail sai destas linhas, então elas já vêm com o estado novo e com o criador.
	r, ok := byID[id]
	require.True(t, ok, "a linha do teste deve estar entre as fechadas")
	require.Equal(t, StatusFalhou, r.Status)
	require.Equal(t, email, r.CreatorEmail)
	require.Equal(t, "R", r.StationName)
	require.NotNil(t, r.LastError)
	require.Equal(t, "motivo", *r.LastError)
	require.NotNil(t, r.FinishedAt)
	require.NotNil(t, r.Gaps)

	// Sem criador a linha continua vindo: não pode ser um JOIN que a descarte.
	sem, ok := byID[orphan]
	require.True(t, ok, "a linha sem criador deve estar entre as fechadas")
	require.Empty(t, sem.CreatorEmail)

	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusFalhou, got.Status)
	require.Equal(t, "motivo", *got.LastError)

	again, err := s.FailMissed(ctx, now, "motivo")
	require.NoError(t, err)
	for _, r := range again {
		require.NotEqual(t, id, r.ID, "linha já fechada não volta: o e-mail sairia de novo")
	}
}

// queryCounter conta os comandos que chegam ao banco.
type queryCounter struct{ n atomic.Int32 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}
func (c *queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// Fechar a linha e ler os dados do e-mail têm que ser o mesmo comando. Em dois, um
// prazo que estoura entre eles deixa `falhou` sem aviso para sempre, porque o tick
// seguinte não encontra mais a linha.
func TestStore_FailMissed_UmComandoSo(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)
	for i := 0; i < 2; i++ {
		from := now.Add(time.Duration(-2*i-2) * time.Hour)
		_, err := s.Insert(ctx, st, "R", nil, from, from.Add(time.Hour))
		require.NoError(t, err)
	}

	cfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	require.NoError(t, err)
	counter := &queryCounter{}
	cfg.ConnConfig.Tracer = counter
	traced, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(traced.Close)

	missed, err := NewStore(traced).FailMissed(ctx, now, "motivo")

	require.NoError(t, err)
	require.GreaterOrEqual(t, len(missed), 2)
	require.EqualValues(t, 1, counter.n.Load())
}

func TestStore_Cancelar(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)

	id, err := s.Insert(ctx, st, "R", nil, now.Add(time.Hour), now.Add(2*time.Hour))
	require.NoError(t, err)
	require.NoError(t, s.CancelScheduled(ctx, id, now))
	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusCancelada, got.Status)
	require.ErrorIs(t, s.CancelScheduled(ctx, id, now), ErrBusy)

	id2, err := s.Insert(ctx, st, "R", nil, now.Add(-time.Minute), now.Add(time.Hour))
	require.NoError(t, err)
	_, err = s.ClaimDue(ctx, now, now)
	require.NoError(t, err)
	require.NoError(t, s.CancelRecording(ctx, id2, now))
	got, err = s.Get(ctx, id2)
	require.NoError(t, err)
	require.Equal(t, StatusProcessando, got.Status)
	require.NotNil(t, got.CanceledAt)
}

func TestStore_StationInfoEStreamURL(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")

	_, url, err := s.StationInfo(ctx, st)
	require.NoError(t, err)
	require.Equal(t, "http://x/s", url)

	_, _, err = s.StationInfo(ctx, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)

	id, err := s.Insert(ctx, st, "R", nil, time.Now().Add(time.Hour), time.Now().Add(2*time.Hour))
	require.NoError(t, err)
	got, err := s.StreamURL(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "http://x/s", got)
}

func TestStore_ListPagina(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	t0 := time.Now().Add(10 * 365 * 24 * time.Hour).Truncate(time.Second)
	_, err := s.Insert(ctx, st, "A", nil, t0, t0.Add(time.Hour))
	require.NoError(t, err)
	idB, err := s.Insert(ctx, st, "B", nil, t0.Add(2*time.Hour), t0.Add(3*time.Hour))
	require.NoError(t, err)

	items, total, err := s.List(ctx, 1, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.GreaterOrEqual(t, total, 2)
	require.Equal(t, idB, items[0].ID, "mais recente primeiro")
}

func TestStore_Complete_ForaDeProcessandoDevolveBusy(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)
	id, err := s.Insert(ctx, st, "R", nil, now.Add(time.Hour), now.Add(2*time.Hour))
	require.NoError(t, err)

	key := "recordings/2026/11/y.m4a"
	err = s.Complete(ctx, id, CompleteInput{Status: StatusConcluida, ObjectKey: &key, FinishedAt: now})
	require.ErrorIs(t, err, ErrBusy)

	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusProgramada, got.Status)
	require.False(t, got.HasAudio)
	require.Nil(t, got.FinishedAt)
}

// Motivo de uma tentativa que falhou não pode sobrar numa gravação sem problema,
// mas continua valendo como explicação de parcial, falhou e cancelada.
func TestStore_Complete_LimpaLastErrorSoEmConcluida(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)

	for i, c := range []struct {
		status string
		keep   bool
	}{{StatusConcluida, false}, {StatusParcial, true}, {StatusFalhou, true}, {StatusCancelada, true}} {
		from := now.Add(time.Duration(i+1) * 2 * time.Hour)
		id, err := s.Insert(ctx, st, "R", nil, from, from.Add(time.Hour))
		require.NoError(t, err)
		_, err = s.ClaimDue(ctx, from, from)
		require.NoError(t, err)
		require.NoError(t, s.SetLastError(ctx, id, "bucket fora"))
		require.NoError(t, s.MarkProcessing(ctx, id))

		require.NoError(t, s.Complete(ctx, id, CompleteInput{Status: c.status, Gaps: []Gap{}, FinishedAt: now}))

		got, err := s.Get(ctx, id)
		require.NoError(t, err)
		require.Equal(t, c.status, got.Status)
		if c.keep {
			require.NotNil(t, got.LastError, c.status)
			require.Equal(t, "bucket fora", *got.LastError)
		} else {
			require.Nil(t, got.LastError, c.status)
		}
	}
}

func TestStore_ClaimDue_IgnoraJanelaVencida(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)
	id, err := s.Insert(ctx, st, "R", nil, now.Add(-2*time.Hour), now.Add(-time.Hour))
	require.NoError(t, err)

	_, err = s.ClaimDue(ctx, now, now)
	require.NoError(t, err)
	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusProgramada, got.Status, "janela já vencida não vira gravando")
}

func TestStore_Delete(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)
	id, err := s.Insert(ctx, st, "R", nil, now.Add(time.Hour), now.Add(2*time.Hour))
	require.NoError(t, err)

	require.NoError(t, s.Delete(ctx, id))
	_, err = s.Get(ctx, id)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_Delete_NaoApagaGravacaoViva(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)

	for i, status := range []string{StatusGravando, StatusProcessando} {
		from := now.Add(time.Duration(i+1) * time.Hour)
		id, err := s.Insert(ctx, st, "R", nil, from, from.Add(time.Minute))
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `UPDATE recordings SET status = $2 WHERE id = $1`, id, status)
		require.NoError(t, err)

		require.ErrorIs(t, s.Delete(ctx, id), ErrBusy, status)

		got, err := s.Get(ctx, id)
		require.NoError(t, err, status)
		require.Equal(t, status, got.Status)
	}
}

func TestStore_Delete_Inexistente(t *testing.T) {
	ctx, s, _ := newTestStore(t)
	require.ErrorIs(t, s.Delete(ctx, uuid.New()), ErrNotFound)
}

// requireMissingRowContract é o que o Store faz com id que não existe. Roda também
// contra o fakeRepo: um fake que diverge esconde a corrida que deveria reproduzir.
func requireMissingRowContract(t *testing.T, r Repo) {
	t.Helper()
	ctx, id, now := context.Background(), uuid.New(), time.Now()
	require.NoError(t, r.MarkProcessing(ctx, id))
	require.NoError(t, r.MarkExpired(ctx, id))
	require.ErrorIs(t, r.CancelScheduled(ctx, id, now), ErrBusy)
	require.ErrorIs(t, r.CancelRecording(ctx, id, now), ErrBusy)
	require.ErrorIs(t, r.Complete(ctx, id, CompleteInput{Status: StatusConcluida, Gaps: []Gap{}, FinishedAt: now}), ErrBusy)
	require.ErrorIs(t, r.Delete(ctx, id), ErrNotFound)
}

func TestStore_LinhaAusente(t *testing.T) {
	_, s, _ := newTestStore(t)
	requireMissingRowContract(t, s)
}

func TestFakeRepo_LinhaAusenteImitaOStore(t *testing.T) {
	requireMissingRowContract(t, newFakeRepo())
}
