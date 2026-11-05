package recording

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	writePiece(t, dir, "1", r.StartsAt, r.StartsAt.Add(10*time.Minute), validADTS(3))
}

// requireNoTempFiles: uma gravação de 6h com o bucket fora não pode manter a
// cópia inteira no disco enquanto espera a próxima tentativa.
func requireNoTempFiles(t *testing.T, m *Manager, r Recording) {
	t.Helper()
	for _, name := range []string{"joined.tmp", "out.m4a"} {
		_, err := os.Stat(filepath.Join(m.dirFor(r.ID), name))
		require.True(t, os.IsNotExist(err), name)
	}
}

// runFinalize faz o papel de um tick e espera a goroutine liberar o id.
func runFinalize(t *testing.T, ctx context.Context, m *Manager, r Recording) {
	t.Helper()
	m.startFinalize(ctx, r)
	require.Eventually(t, func() bool { return !m.isFinalizing(r.ID) }, 2*time.Second, time.Millisecond)
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
	require.Equal(t, validADTS(3), string(objs.puts[*got.ObjectKey]))
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
	writePiece(t, dir, "1", t0, t0.Add(5*time.Minute), validADTS(3))

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
	requireNoTempFiles(t, m, r)

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
	writePiece(t, dir, "1", t0, cancel, validADTS(3))

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

// A perda silenciosa: o frame cortado não chega ao remux, e o resto sobe.
func TestFinalize_ApagaFrameCortado(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	dir := m.dirFor(r.ID)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	cortado := validADTS(3)
	writePiece(t, dir, "1", t0, t0.Add(5*time.Minute), cortado[:len(cortado)-5])
	writePiece(t, dir, "2", t0.Add(5*time.Minute), t0.Add(10*time.Minute), validADTS(2))

	require.NoError(t, m.finalize(context.Background(), r))

	got := repo.snapshot(r.ID)
	require.Equal(t, StatusConcluida, got.Status)
	require.Equal(t, validADTS(4), string(objs.puts[*got.ObjectKey]))
}

// Bytes sem nenhum frame completo não são áudio: a janela inteira vira buraco.
func TestFinalize_SemFrameCompletoFalhaEAvisa(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	dir := m.dirFor(r.ID)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writePiece(t, dir, "1", t0, t0.Add(10*time.Minute), validADTS(1)[:4])

	require.NoError(t, m.finalize(context.Background(), r))

	got := repo.snapshot(r.ID)
	require.Equal(t, StatusFalhou, got.Status)
	require.Equal(t, []Gap{{From: r.StartsAt, To: r.EndsAt}}, got.Gaps)
	require.Nil(t, got.ObjectKey)
	require.Empty(t, objs.puts)
	require.Len(t, mail.all(), 1)
}

// Nenhuma falha entre o Concat e o upload pode custar os pedaços. O arquivo curto
// é o caso que o remux não acusa: ele sai com exit 0.
func TestFinalize_ConferenciaFalhaPreservaPedacos(t *testing.T) {
	cases := []struct {
		name  string
		media fakeMedia
		msg   string
	}{
		{"remux falha", fakeMedia{duration: 10 * time.Minute, remuxErr: errors.New("remux quebrou")}, "remux quebrou"},
		{"duration falha", fakeMedia{durationErr: errors.New("ffprobe quebrou")}, "ffprobe quebrou"},
		{"duration curta demais", fakeMedia{duration: time.Millisecond}, "1ms"},
		{"arquivo vazio", fakeMedia{duration: 10 * time.Minute, emptyOutput: true}, "0 bytes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
			r := processing(t0)
			repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
			m := newTestManager(t, repo, &c.media, objs, mail)
			seedPieces(t, m, r)

			err := m.finalize(context.Background(), r)

			require.ErrorContains(t, err, c.msg)
			require.Equal(t, StatusProcessando, repo.snapshot(r.ID).Status)
			require.Empty(t, objs.puts, "arquivo reprovado não sobe")
			pieces, err := ListPieces(m.dirFor(r.ID))
			require.NoError(t, err)
			require.Len(t, pieces, 1)
			requireNoTempFiles(t, m, r)
		})
	}
}

func TestFinalize_DuracaoCurtaExplicaOsDoisValores(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Millisecond}, objs, mail)
	seedPieces(t, m, r)

	err := m.finalize(context.Background(), r)

	require.ErrorContains(t, err, "10ms")
	require.ErrorContains(t, err, framesDuration(3).Round(time.Millisecond).String())
}

func TestFinalize_DuracaoNoLimiteDe98PorCentoPassa(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	limite := (framesDuration(3)*98 + 99) / 100
	m := newTestManager(t, repo, &fakeMedia{duration: limite}, objs, mail)
	seedPieces(t, m, r)

	require.NoError(t, m.finalize(context.Background(), r))
	require.Equal(t, StatusConcluida, repo.snapshot(r.ID).Status)
}

func TestStartFinalize_TemPrazo(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	media := &fakeMedia{duration: 10 * time.Minute}
	m := newTestManager(t, repo, media, objs, mail)
	seedPieces(t, m, r)

	runFinalize(t, context.Background(), m, r)

	require.WithinDuration(t, time.Now().Add(30*time.Minute), media.deadline, time.Minute)
}

// Reinício da API não é falha: a finalização retoma sozinha no boot.
func TestStartFinalize_ReinicioNaoGravaMotivoNemContaFalha(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	objs.setPutErr(context.Canceled)
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	m.cfg.FinalizeRetry = time.Hour
	seedPieces(t, m, r)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runFinalize(t, ctx, m, r)

	require.Nil(t, repo.snapshot(r.ID).LastError)
	require.Zero(t, m.finalizeFailureCount(r.ID))
	require.Equal(t, StatusProcessando, repo.snapshot(r.ID).Status)
}

func TestStartFinalize_TerceiraFalhaSeguidaAvisaUmaVez(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	objs.setPutErr(errors.New("bucket fora"))
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	seedPieces(t, m, r)

	runFinalize(t, context.Background(), m, r)
	runFinalize(t, context.Background(), m, r)
	require.Empty(t, mail.all(), "falha transitória não gera alarme")

	runFinalize(t, context.Background(), m, r)
	require.Len(t, mail.all(), 1)
	got := mail.all()[0]
	require.Contains(t, got.subject, "não fechou")
	require.Contains(t, got.text, "preservado")
	require.Contains(t, got.text, "segue tentando")
	require.Contains(t, got.text, "bucket fora")

	runFinalize(t, context.Background(), m, r)
	runFinalize(t, context.Background(), m, r)
	require.Len(t, mail.all(), 1, "uma vez só")
	require.Equal(t, 5, objs.calls())
}

func TestStartFinalize_SucessoZeraAContagem(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	objs.setPutErr(errors.New("bucket fora"))
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	seedPieces(t, m, r)

	runFinalize(t, context.Background(), m, r)
	runFinalize(t, context.Background(), m, r)
	require.Equal(t, 2, m.finalizeFailureCount(r.ID))

	objs.setPutErr(nil)
	runFinalize(t, context.Background(), m, r)

	require.Equal(t, StatusConcluida, repo.snapshot(r.ID).Status)
	require.Zero(t, m.finalizeFailureCount(r.ID))
	require.Empty(t, mail.all())
}

func TestStartFinalize_ErrBusyZeraAContagem(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).Truncate(time.Second)
	r := processing(t0)
	repo, objs, mail := newFakeRepo(r), newFakeObjects(), &fakeMailer{}
	objs.setPutErr(errors.New("bucket fora"))
	m := newTestManager(t, repo, &fakeMedia{duration: 10 * time.Minute}, objs, mail)
	seedPieces(t, m, r)
	runFinalize(t, context.Background(), m, r)
	require.Equal(t, 1, m.finalizeFailureCount(r.ID))

	objs.setPutErr(nil)
	repo.completeErr = ErrBusy
	runFinalize(t, context.Background(), m, r)

	require.Zero(t, m.finalizeFailureCount(r.ID))
}

func (m *Manager) finalizeFailureCount(id uuid.UUID) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.finalizeFailures[id]
}
