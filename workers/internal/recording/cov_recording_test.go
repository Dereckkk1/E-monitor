package recording

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// covZListRepo devolve uma lista fixa em List, para provar que o Manager repassa
// pagina e total sem mexer (o fakeRepo padrao devolve vazio).
type covZListRepo struct {
	*fakeRepo
	items []Recording
	total int
	gotL  int
	gotO  int
}

func (r *covZListRepo) List(_ context.Context, limit, offset int) ([]Recording, int, error) {
	r.gotL, r.gotO = limit, offset
	return r.items, r.total, nil
}

func TestCovZ_Manager_ListAndGetDelegateToRepo(t *testing.T) {
	row := Recording{ID: uuid.New(), StationName: "Radio Teste FM", Status: StatusProgramada, Gaps: []Gap{}}
	repo := &covZListRepo{fakeRepo: newFakeRepo(row), items: []Recording{row}, total: 41}
	m := newTestManager(t, repo, &fakeMedia{}, newFakeObjects(), &fakeMailer{})

	items, total, err := m.List(context.Background(), 25, 50)
	require.NoError(t, err)
	assert.Equal(t, 41, total)
	require.Len(t, items, 1)
	assert.Equal(t, row.ID, items[0].ID)
	assert.Equal(t, 25, repo.gotL)
	assert.Equal(t, 50, repo.gotO)

	got, err := m.Get(context.Background(), row.ID)
	require.NoError(t, err)
	assert.Equal(t, "Radio Teste FM", got.StationName)

	_, err = m.Get(context.Background(), uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestCovZ_ValidationError(t *testing.T) {
	err := error(&ValidationError{Msg: "emissora não encontrada"})
	assert.Equal(t, "emissora não encontrada", err.Error())
}

// --- Store (Postgres real) --------------------------------------------------

func TestCovZ_Store_ListPaginatesNewestFirst(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	base := time.Now().Add(3650 * 24 * time.Hour).Truncate(time.Second) // bem a frente de qualquer outra linha da base

	var ids []uuid.UUID
	for i := 0; i < 3; i++ {
		from := base.Add(time.Duration(i) * 2 * time.Hour)
		id, err := s.Insert(ctx, st, "Radio Teste FM", nil, from, from.Add(time.Hour))
		require.NoError(t, err)
		ids = append(ids, id)
	}

	// Mais recentes primeiro: o ultimo inserido (comeca mais tarde) abre a lista.
	page1, total, err := s.List(ctx, 2, 0)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, 3)
	require.Len(t, page1, 2)
	assert.Equal(t, ids[2], page1[0].ID)
	assert.Equal(t, ids[1], page1[1].ID)

	page2, total2, err := s.List(ctx, 2, 2)
	require.NoError(t, err)
	assert.Equal(t, total, total2, "o total nao muda com a pagina")
	require.NotEmpty(t, page2)
	assert.Equal(t, ids[0], page2[0].ID)
	for _, r := range append(page1, page2...) {
		assert.NotNil(t, r.Gaps, "gaps nunca e nulo no JSON")
	}
}

func TestCovZ_Store_ListByStatusAndClaimDue(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)

	soon, err := s.Insert(ctx, st, "R", nil, now.Add(time.Minute), now.Add(time.Hour))
	require.NoError(t, err)
	later, err := s.Insert(ctx, st, "R", nil, now.Add(3*time.Hour), now.Add(4*time.Hour))
	require.NoError(t, err)

	contains := func(list []Recording, id uuid.UUID) bool {
		for _, r := range list {
			if r.ID == id {
				return true
			}
		}
		return false
	}

	prog, err := s.ListByStatus(ctx, StatusProgramada)
	require.NoError(t, err)
	assert.True(t, contains(prog, soon))
	assert.True(t, contains(prog, later))

	// Pre-roll: reivindica o que comeca ate startBefore e ainda nao terminou.
	n, err := s.ClaimDue(ctx, now.Add(2*time.Minute), now)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(1))

	rec, err := s.ListByStatus(ctx, StatusGravando)
	require.NoError(t, err)
	assert.True(t, contains(rec, soon))
	assert.False(t, contains(rec, later), "a de daqui a 3h segue programada")
	prog, err = s.ListByStatus(ctx, StatusProgramada)
	require.NoError(t, err)
	assert.False(t, contains(prog, soon))
	assert.True(t, contains(prog, later))

	// Janela que ja terminou nao e reivindicada (tratada por FailMissed).
	past, err := s.Insert(ctx, st, "R", nil, now.Add(-3*time.Hour), now.Add(-2*time.Hour))
	require.NoError(t, err)
	_, err = s.ClaimDue(ctx, now.Add(5*time.Minute), now)
	require.NoError(t, err)
	got, err := s.Get(ctx, past)
	require.NoError(t, err)
	assert.Equal(t, StatusProgramada, got.Status)

	none, err := s.ListByStatus(ctx, "status-inexistente")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestCovZ_Store_StreamURLIsReadFromTheStation(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://example.com/original")
	now := time.Now().Truncate(time.Second)
	id, err := s.Insert(ctx, st, "R", nil, now.Add(time.Hour), now.Add(2*time.Hour))
	require.NoError(t, err)

	got, err := s.StreamURL(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "http://example.com/original", got)

	// Corrigir a URL na emissora vale na proxima leitura (reconexao).
	_, err = pool.Exec(ctx, `UPDATE stations SET stream_url=$2 WHERE id=$1`, st, "http://example.com/corrigida")
	require.NoError(t, err)
	got, err = s.StreamURL(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "http://example.com/corrigida", got)

	_, err = s.StreamURL(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestCovZ_Store_CompleteRequiresProcessingAndClearsError(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)
	id, err := s.Insert(ctx, st, "R", nil, now.Add(time.Minute), now.Add(time.Hour))
	require.NoError(t, err)

	finish := CompleteInput{Status: StatusConcluida, FinishedAt: now}

	// Ainda `programada`: nao fecha.
	assert.ErrorIs(t, s.Complete(ctx, id, finish), ErrBusy)

	_, err = s.ClaimDue(ctx, now.Add(2*time.Minute), now)
	require.NoError(t, err)
	require.NoError(t, s.SetLastError(ctx, id, "queda de rede na captura"))
	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got.LastError)
	assert.Equal(t, "queda de rede na captura", *got.LastError)

	// MarkProcessing so vale para `gravando` (idempotente).
	require.NoError(t, s.MarkProcessing(ctx, id))
	require.NoError(t, s.MarkProcessing(ctx, id))

	key := "recordings/covz.m4a"
	size, cov := int64(2048), 3600
	exp := now.Add(Retention)
	require.NoError(t, s.Complete(ctx, id, CompleteInput{
		Status: StatusConcluida, ObjectKey: &key, SizeBytes: &size, CoveredSeconds: &cov,
		FinishedAt: now, ExpiresAt: &exp,
	}))
	got, err = s.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, StatusConcluida, got.Status)
	assert.Nil(t, got.LastError, "concluida limpa o motivo de uma tentativa anterior")
	assert.True(t, got.HasAudio)
	require.NotNil(t, got.SizeBytes)
	assert.Equal(t, int64(2048), *got.SizeBytes)
	assert.Empty(t, got.Gaps)
	require.NotNil(t, got.ExpiresAt)

	// Fechar de novo (outro caminho ja fechou) devolve ErrBusy.
	assert.ErrorIs(t, s.Complete(ctx, id, finish), ErrBusy)
}

func TestCovZ_Store_CompleteParcialKeepsLastError(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)
	id, err := s.Insert(ctx, st, "R", nil, now.Add(time.Minute), now.Add(time.Hour))
	require.NoError(t, err)
	_, err = s.ClaimDue(ctx, now.Add(2*time.Minute), now)
	require.NoError(t, err)
	require.NoError(t, s.SetLastError(ctx, id, "buraco de 2 minutos"))
	require.NoError(t, s.MarkProcessing(ctx, id))

	gap := Gap{From: now.Add(10 * time.Minute), To: now.Add(12 * time.Minute)}
	require.NoError(t, s.Complete(ctx, id, CompleteInput{Status: StatusParcial, Gaps: []Gap{gap}, FinishedAt: now}))

	got, err := s.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, StatusParcial, got.Status)
	require.NotNil(t, got.LastError, "so `concluida` limpa o erro")
	assert.Equal(t, "buraco de 2 minutos", *got.LastError)
	require.Len(t, got.Gaps, 1)
	assert.True(t, gap.From.Equal(got.Gaps[0].From))
	assert.True(t, gap.To.Equal(got.Gaps[0].To))
	assert.False(t, got.HasAudio, "sem object_key nao ha audio")
}

func TestCovZ_Store_CancelTransitions(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)

	scheduled, err := s.Insert(ctx, st, "R", nil, now.Add(5*time.Hour), now.Add(6*time.Hour))
	require.NoError(t, err)
	live, err := s.Insert(ctx, st, "R", nil, now.Add(time.Minute), now.Add(time.Hour))
	require.NoError(t, err)
	_, err = s.ClaimDue(ctx, now.Add(2*time.Minute), now)
	require.NoError(t, err)

	// Cada cancelamento so vale no estado certo.
	assert.ErrorIs(t, s.CancelRecording(ctx, scheduled, now), ErrBusy, "programada nao e 'em andamento'")
	assert.ErrorIs(t, s.CancelScheduled(ctx, live, now), ErrBusy, "gravando nao e 'programada'")
	assert.ErrorIs(t, s.CancelScheduled(ctx, uuid.New(), now), ErrBusy)

	require.NoError(t, s.CancelScheduled(ctx, scheduled, now))
	got, err := s.Get(ctx, scheduled)
	require.NoError(t, err)
	assert.Equal(t, StatusCancelada, got.Status)
	require.NotNil(t, got.CanceledAt)
	require.NotNil(t, got.FinishedAt)
	assert.ErrorIs(t, s.CancelScheduled(ctx, scheduled, now), ErrBusy, "ja cancelada")

	// Em andamento: vai para `processando` (o audio ja gravado segue para a finalizacao).
	require.NoError(t, s.CancelRecording(ctx, live, now))
	got, err = s.Get(ctx, live)
	require.NoError(t, err)
	assert.Equal(t, StatusProcessando, got.Status)
	require.NotNil(t, got.CanceledAt)
	assert.Nil(t, got.FinishedAt, "ainda nao terminou: falta finalizar")
}

func TestCovZ_Store_DeleteRules(t *testing.T) {
	ctx, s, pool := newTestStore(t)
	st := seedStation(t, ctx, pool, "http://x/s")
	now := time.Now().Truncate(time.Second)

	assert.ErrorIs(t, s.Delete(ctx, uuid.New()), ErrNotFound)

	scheduled, err := s.Insert(ctx, st, "R", nil, now.Add(5*time.Hour), now.Add(6*time.Hour))
	require.NoError(t, err)
	require.NoError(t, s.Delete(ctx, scheduled))
	_, err = s.Get(ctx, scheduled)
	assert.ErrorIs(t, err, ErrNotFound)

	live, err := s.Insert(ctx, st, "R", nil, now.Add(time.Minute), now.Add(time.Hour))
	require.NoError(t, err)
	_, err = s.ClaimDue(ctx, now.Add(2*time.Minute), now)
	require.NoError(t, err)
	assert.ErrorIs(t, s.Delete(ctx, live), ErrBusy, "gravando nao se apaga")
	got, err := s.Get(ctx, live)
	require.NoError(t, err)
	assert.Equal(t, StatusGravando, got.Status, "a linha fica intacta")

	require.NoError(t, s.MarkProcessing(ctx, live))
	assert.ErrorIs(t, s.Delete(ctx, live), ErrBusy, "processando tambem nao")
}

func TestCovZ_Store_DatabaseErrorsAreWrapped(t *testing.T) {
	_, s, _ := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id := uuid.New()
	now := time.Now()

	_, _, err := s.List(ctx, 10, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contar")

	_, err = s.ClaimDue(ctx, now, now)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "claim")

	_, err = s.StreamURL(ctx, id)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound, "falha de banco nao vira 'nao encontrada'")

	require.Error(t, s.Complete(ctx, id, CompleteInput{Status: StatusConcluida, FinishedAt: now}))
	assert.Error(t, s.CancelScheduled(ctx, id, now))
	assert.Error(t, s.CancelRecording(ctx, id, now))
	assert.Error(t, s.Delete(ctx, id))
	assert.Error(t, s.MarkProcessing(ctx, id))
	assert.Error(t, s.SetLastError(ctx, id, "x"))
	_, err = s.MarkDropAlerted(ctx, id)
	assert.Error(t, err)
	_, err = s.ListByStatus(ctx, StatusGravando)
	assert.Error(t, err)
}
