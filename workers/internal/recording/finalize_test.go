package recording

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func processing(t0 time.Time) Recording {
	return Recording{ID: uuid.New(), StationName: "Radio X", CreatorEmail: "a@b.c",
		StartsAt: t0, EndsAt: t0.Add(10 * time.Minute), Status: StatusProcessando, Gaps: []Gap{}}
}

func seedPieces(t *testing.T, m *Manager, r Recording) {
	t.Helper()
	dir := m.dirFor(r.ID)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writePiece(t, dir, "1", r.StartsAt, r.StartsAt.Add(10*time.Minute), "audio")
}

func TestFinalize_Concluida(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	seedPieces(t, m, r)

	require.NoError(t, m.finalize(context.Background(), r))

	got := repo.snapshot(r.ID)
	require.Equal(t, StatusConcluida, got.Status)
	require.Equal(t, "audio", string(objs.puts[*got.ObjectKey]))
	require.Equal(t, 600, *got.CoveredSeconds)
	require.WithinDuration(t, time.Now().Add(Retention), *got.ExpiresAt, time.Minute)
	require.Empty(t, mail.all(), "sucesso não manda e-mail")
	_, err := os.Stat(m.dirFor(r.ID))
	require.True(t, os.IsNotExist(err), "pedaços apagados depois do upload")
}

func TestFinalize_ParcialAvisa(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{duration: 5 * time.Minute}, objs, mail)
	dir := m.dirFor(r.ID)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writePiece(t, dir, "1", t0, t0.Add(5*time.Minute), "audio")

	require.NoError(t, m.finalize(context.Background(), r))

	got := repo.snapshot(r.ID)
	require.Equal(t, StatusParcial, got.Status)
	require.Len(t, got.Gaps, 1)
	require.Len(t, mail.all(), 1)
	require.Contains(t, mail.all()[0].subject, "parcial")
}

func TestFinalize_SemAudioFalhaEAvisa(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{}, objs, mail)

	require.NoError(t, m.finalize(context.Background(), r))

	got := repo.snapshot(r.ID)
	require.Equal(t, StatusFalhou, got.Status)
	require.Nil(t, got.ObjectKey)
	require.Nil(t, got.ExpiresAt)
	require.Empty(t, objs.puts)
	require.Len(t, mail.all(), 1)
}

// A garantia central: falha no upload não pode perder áudio já capturado.
func TestFinalize_FalhaNoUploadPreservaPedacos(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	objs.putErr = errors.New("bucket fora")
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	seedPieces(t, m, r)

	require.Error(t, m.finalize(context.Background(), r))

	require.Equal(t, StatusProcessando, repo.snapshot(r.ID).Status)
	pieces, err := ListPieces(m.dirFor(r.ID))
	require.NoError(t, err)
	require.Len(t, pieces, 1)

	objs.putErr = nil
	require.NoError(t, m.finalize(context.Background(), r), "a retentativa conclui")
	require.Equal(t, StatusConcluida, repo.snapshot(r.ID).Status)
}

func TestFinalize_CanceladaGuardaAudioSemEmail(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	cancel := t0.Add(5 * time.Minute)
	r.CanceledAt = &cancel
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{duration: 5 * time.Minute}, objs, mail)
	dir := m.dirFor(r.ID)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writePiece(t, dir, "1", t0, cancel, "audio")

	require.NoError(t, m.finalize(context.Background(), r))

	got := repo.snapshot(r.ID)
	require.Equal(t, StatusCancelada, got.Status)
	require.NotNil(t, got.ObjectKey)
	require.Empty(t, got.Gaps, "o que veio depois do cancelamento não é buraco")
	require.Empty(t, mail.all())
}

func TestObjectKey(t *testing.T) {
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	r := Recording{ID: id, StartsAt: time.Date(2026, 11, 5, 23, 0, 0, 0, time.UTC)}
	require.Equal(t, "recordings/2026/11/"+id.String()+".m4a", objectKey(r))
}

// Falha ao registrar o status também não pode perder áudio: os pedaços só somem
// depois de o Complete confirmar.
func TestFinalize_FalhaNoCompletePreservaPedacos(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	repo.completeErr = errors.New("banco fora")
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	seedPieces(t, m, r)

	require.ErrorIs(t, m.finalize(context.Background(), r), repo.completeErr)

	require.Equal(t, StatusProcessando, repo.snapshot(r.ID).Status)
	pieces, err := ListPieces(m.dirFor(r.ID))
	require.NoError(t, err)
	require.Len(t, pieces, 1)

	repo.completeErr = nil
	require.NoError(t, m.finalize(context.Background(), r), "a retentativa conclui")
	require.Equal(t, StatusConcluida, repo.snapshot(r.ID).Status)
}

// Um tick que leu a linha antes de ela fechar dispara um finalize atrasado. Sem
// pedaços, ele não pode rebaixar uma gravação concluída para falhou nem avisar.
func TestFinalize_AtrasadoNaoSobrescreveNemAvisa(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	seedPieces(t, m, r)
	require.NoError(t, m.finalize(context.Background(), r))

	require.ErrorIs(t, m.finalize(context.Background(), r), ErrBusy)

	require.Equal(t, StatusConcluida, repo.snapshot(r.ID).Status)
	require.Empty(t, mail.all())
}

func (m *Manager) isFinalizing(id uuid.UUID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.finalizing[id]
	return ok
}

// A linha fica em processando com o motivo visível, sem perder áudio.
func TestStartFinalize_FalhaGravaMotivoEPreservaPedacos(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	objs.setPutErr(errors.New("bucket fora"))
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	seedPieces(t, m, r)

	m.startFinalize(context.Background(), r)

	require.Eventually(t, func() bool { return repo.snapshot(r.ID).LastError != nil },
		2*time.Second, time.Millisecond)
	got := repo.snapshot(r.ID)
	require.Contains(t, *got.LastError, "bucket fora")
	require.Equal(t, StatusProcessando, got.Status)
	pieces, err := ListPieces(m.dirFor(r.ID))
	require.NoError(t, err)
	require.Len(t, pieces, 1)
}

// Sem a espera, gravação de 6h reescreveria 330 MB em disco a cada tick.
func TestStartFinalize_NaoRepeteDuranteFinalizeRetry(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	objs.setPutErr(errors.New("bucket fora"))
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	m.cfg.FinalizeRetry = time.Hour
	seedPieces(t, m, r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.startFinalize(ctx, r)
	require.Eventually(t, func() bool { return repo.snapshot(r.ID).LastError != nil },
		2*time.Second, time.Millisecond)

	for i := 0; i < 5; i++ {
		m.startFinalize(ctx, r)
	}
	require.Never(t, func() bool { return objs.calls() > 1 }, 100*time.Millisecond, 5*time.Millisecond)
	require.Equal(t, 1, objs.calls())
}

func TestStartFinalize_CtxEncerradoLiberaAEspera(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	objs.setPutErr(errors.New("bucket fora"))
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	m.cfg.FinalizeRetry = time.Hour
	seedPieces(t, m, r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.startFinalize(ctx, r)
	require.Eventually(t, func() bool { return repo.snapshot(r.ID).LastError != nil },
		2*time.Second, time.Millisecond)
	require.True(t, m.isFinalizing(r.ID), "falhou: segura o id")

	cancel()

	require.Eventually(t, func() bool { return !m.isFinalizing(r.ID) }, 2*time.Second, time.Millisecond)
}

func TestStartFinalize_DepoisDoRetryConclui(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	objs.setPutErr(errors.New("bucket fora"))
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	seedPieces(t, m, r)

	m.startFinalize(context.Background(), r)
	require.Eventually(t, func() bool { return repo.snapshot(r.ID).LastError != nil },
		2*time.Second, time.Millisecond)

	objs.setPutErr(nil)
	require.Eventually(t, func() bool {
		m.startFinalize(context.Background(), r) // faz o papel do tick
		return repo.snapshot(r.ID).Status == StatusConcluida
	}, 2*time.Second, 5*time.Millisecond)

	// O status fecha antes de a pasta ser apagada: espera a goroutine terminar.
	require.Eventually(t, func() bool { return !m.isFinalizing(r.ID) }, 2*time.Second, time.Millisecond)
	_, err := os.Stat(m.dirFor(r.ID))
	require.True(t, os.IsNotExist(err), "pedaços apagados depois do upload")
}

func TestStartFinalize_SucessoLiberaNaHora(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	m.cfg.FinalizeRetry = time.Hour
	seedPieces(t, m, r)

	m.startFinalize(context.Background(), r)

	require.Eventually(t, func() bool { return !m.isFinalizing(r.ID) }, 2*time.Second, time.Millisecond)
	require.Equal(t, StatusConcluida, repo.snapshot(r.ID).Status)
	require.Nil(t, repo.snapshot(r.ID).LastError)
}

// ErrBusy diz que a linha já fechou: um tick atrasado não pode escrever um motivo
// falso numa gravação encerrada, nem segurar o id.
func TestStartFinalize_LinhaJaFechadaNaoGravaMotivo(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	m.cfg.FinalizeRetry = time.Hour
	seedPieces(t, m, r)
	require.NoError(t, m.finalize(context.Background(), r))

	m.startFinalize(context.Background(), r)

	require.Eventually(t, func() bool { return !m.isFinalizing(r.ID) }, 2*time.Second, time.Millisecond)
	require.Nil(t, repo.snapshot(r.ID).LastError)
}
