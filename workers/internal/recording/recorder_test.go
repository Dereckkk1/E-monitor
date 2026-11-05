package recording

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func recordingRow(dur time.Duration) Recording {
	now := time.Now()
	return Recording{ID: uuid.New(), StationName: "Radio X", CreatorEmail: "a@b.c",
		StartsAt: now, EndsAt: now.Add(dur), Status: StatusGravando, Gaps: []Gap{}}
}

// feed escreve bytes em ritmo constante, como um ffmpeg com stream saudável.
// Abre e fecha a cada escrita porque o NTFS só atualiza o tamanho na listagem da
// pasta quando o arquivo fecha; no Linux, o caso de produção, é imediato.
func feed(ctx context.Context, dir string) {
	p := filepath.Join(dir, "a1-20261105-200000"+pieceExt)
	t := time.NewTicker(5 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return
			}
			f.Write([]byte("x")) //nolint:errcheck
			f.Close()
		}
	}
}

// hungCapture simula o ffmpeg pendurado: não sai, não escreve, só obedece ao ctx.
func hungCapture(ctx context.Context, _, _ string) error {
	<-ctx.Done()
	return nil
}

// mailerFunc deixa o teste decidir quanto o envio do aviso demora.
type mailerFunc func() error

func (f mailerFunc) Send(context.Context, []string, string, string, string) error { return f() }

// slowDropRepo atrasa o MarkDropAlerted, como um banco lento.
type slowDropRepo struct {
	*fakeRepo
	block func()
}

func (r *slowDropRepo) MarkDropAlerted(ctx context.Context, id uuid.UUID) (bool, error) {
	r.block()
	return r.fakeRepo.MarkDropAlerted(ctx, id)
}

func TestRecord_NoFimDaJanelaVaiParaProcessando(t *testing.T) {
	r := recordingRow(150 * time.Millisecond)
	repo, mail := newFakeRepo(r), &fakeMailer{}
	media := &fakeMedia{capture: func(ctx context.Context, _, dir string) error {
		feed(ctx, dir)
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), mail)

	m.record(context.Background(), r)

	require.Equal(t, StatusProcessando, repo.snapshot(r.ID).Status)
	require.Empty(t, mail.all())
}

func TestRecord_ReconectaQuandoOFfmpegSai(t *testing.T) {
	r := recordingRow(120 * time.Millisecond)
	repo := newFakeRepo(r)
	var calls atomic.Int32
	media := &fakeMedia{capture: func(context.Context, string, string) error {
		calls.Add(1)
		return errors.New("caiu")
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})

	m.record(context.Background(), r)

	require.Greater(t, calls.Load(), int32(2))
}

// O limpador de 60min apaga pasta vazia, então cada tentativa recria a pasta.
func TestRecord_RecriaAPastaACadaTentativa(t *testing.T) {
	r := recordingRow(120 * time.Millisecond)
	repo := newFakeRepo(r)
	var missing atomic.Int32
	media := &fakeMedia{capture: func(_ context.Context, _, dir string) error {
		if _, err := os.Stat(dir); err != nil {
			missing.Add(1)
		}
		os.RemoveAll(dir) //nolint:errcheck
		return errors.New("caiu")
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})

	m.record(context.Background(), r)

	require.Zero(t, missing.Load())
}

func TestRecord_LeAURLACadaTentativa(t *testing.T) {
	r := recordingRow(120 * time.Millisecond)
	repo := newFakeRepo(r)
	var urls atomic.Value
	urls.Store("")
	media := &fakeMedia{capture: func(_ context.Context, url, _ string) error {
		urls.Store(url)
		repo.mu.Lock()
		repo.url = "http://nova/s"
		repo.mu.Unlock()
		return errors.New("caiu")
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})

	m.record(context.Background(), r)

	require.Equal(t, "http://nova/s", urls.Load())
}

// ffmpeg pendurado: não sai, não escreve. Só o watchdog destrava.
func TestRecord_WatchdogMataFfmpegPenduradoEAvisaUmaVez(t *testing.T) {
	r := recordingRow(300 * time.Millisecond)
	repo, mail := newFakeRepo(r), &fakeMailer{}
	var calls atomic.Int32
	media := &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		calls.Add(1)
		<-ctx.Done()
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), mail)

	m.record(context.Background(), r)

	require.GreaterOrEqual(t, calls.Load(), int32(2), "o watchdog precisa reiniciar a captura")
	require.Len(t, mail.all(), 1, "um aviso por gravação, mesmo com várias quedas")
	require.Contains(t, mail.all()[0].subject, "não conectou")
}

func TestRecord_QuedaDepoisDeTerAudioAvisaComoQueda(t *testing.T) {
	r := recordingRow(300 * time.Millisecond)
	repo, mail := newFakeRepo(r), &fakeMailer{}
	media := &fakeMedia{capture: func(ctx context.Context, _, dir string) error {
		p := filepath.Join(dir, "a1-20261105-200000"+pieceExt)
		os.WriteFile(p, []byte("audio"), 0o644) //nolint:errcheck
		<-ctx.Done()
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), mail)

	m.record(context.Background(), r)

	require.Len(t, mail.all(), 1)
	require.Contains(t, mail.all()[0].subject, "stream caiu")
}

// Desligamento da API: a linha fica em `gravando` para ser retomada no boot.
func TestRecord_ContextoCanceladoNaoFecha(t *testing.T) {
	r := recordingRow(time.Hour)
	repo := newFakeRepo(r)
	media := &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		<-ctx.Done()
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	m.record(ctx, r)

	require.Equal(t, StatusGravando, repo.snapshot(r.ID).Status)
}

// Um aviso de queda em andamento quando a janela acaba não pode sobrar para depois
// de record voltar: ele correria junto com a finalização, e o criador receberia
// "stream caiu" depois do e-mail de fechamento.
func TestRecord_AvisoEmAndamentoTerminaAntesDeVoltar(t *testing.T) {
	r := recordingRow(150 * time.Millisecond)
	repo := newFakeRepo(r)
	sent := make(chan struct{})
	var statusAtSend atomic.Value
	m := newTestManager(t, repo, &fakeMedia{capture: hungCapture}, newFakeObjects(), &fakeMailer{})
	m.mail = mailerFunc(func() error {
		time.Sleep(time.Until(r.EndsAt) + 100*time.Millisecond)
		statusAtSend.Store(repo.snapshot(r.ID).Status)
		close(sent)
		return nil
	})

	m.record(context.Background(), r)

	select {
	case <-sent:
	default:
		t.Fatal("record voltou com o aviso em andamento")
	}
	require.Equal(t, StatusGravando, statusAtSend.Load(), "processando só depois do watchdog parar")
	require.Equal(t, StatusProcessando, repo.snapshot(r.ID).Status)
}

// A marcação demora além do fim da janela. Ela termina antes de record voltar, e o
// watchdog não tenta de novo com a janela encerrada.
func TestRecord_MarcacaoDeQuedaEmAndamentoTerminaAntesDeVoltar(t *testing.T) {
	r := recordingRow(150 * time.Millisecond)
	var started, finished atomic.Int32
	repo := &slowDropRepo{fakeRepo: newFakeRepo(r), block: func() {
		started.Add(1)
		time.Sleep(time.Until(r.EndsAt) + 100*time.Millisecond)
		finished.Add(1)
	}}
	mail := &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{capture: hungCapture}, newFakeObjects(), mail)

	m.record(context.Background(), r)

	require.Equal(t, int32(1), finished.Load(), "record voltou com o MarkDropAlerted em andamento")
	require.Equal(t, int32(1), started.Load(), "o watchdog agiu depois do fim da janela")
	require.Len(t, mail.all(), 1, "o aviso também sai antes de record voltar")
}

// SMTP lento não pode segurar a morte do ffmpeg travado: reiniciar vem antes de
// avisar. Se a ordem inverter, a segunda tentativa só sai depois do Send.
func TestRecord_AvisoLentoNaoAtrasaOReinicio(t *testing.T) {
	r := recordingRow(500 * time.Millisecond)
	repo := newFakeRepo(r)
	var calls atomic.Int32
	second := make(chan struct{})
	media := &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		if calls.Add(1) == 2 {
			close(second)
		}
		<-ctx.Done()
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})
	var restarted atomic.Bool
	m.mail = mailerFunc(func() error {
		select {
		case <-second:
			restarted.Store(true)
		case <-time.After(time.Until(r.EndsAt)):
		}
		return nil
	})

	m.record(context.Background(), r)

	require.True(t, restarted.Load(), "a captura tem que reiniciar com o aviso ainda saindo")
}

// No desligamento, o SIGTERM só adianta se o processo esperar o ffmpeg escrever o
// que tem em buffer.
func TestWaitRecorders_EsperaOGravadorTerminar(t *testing.T) {
	r := recordingRow(time.Hour)
	repo := newFakeRepo(r)
	started := make(chan struct{})
	var once sync.Once
	var flushed atomic.Bool
	media := &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		once.Do(func() { close(started) })
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		flushed.Store(true)
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.startRecorder(ctx, r)
	<-started
	cancel()

	begin := time.Now()
	m.WaitRecorders(30 * time.Second)

	require.True(t, flushed.Load(), "voltou antes de o gravador terminar")
	require.Less(t, time.Since(begin), 15*time.Second, "tem que voltar quando o gravador sai, não no timeout")
	require.Equal(t, StatusGravando, repo.snapshot(r.ID).Status)
}

func TestWaitRecorders_TimeoutComGravadorPreso(t *testing.T) {
	r := recordingRow(time.Hour)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	media := &fakeMedia{capture: func(context.Context, string, string) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}}
	m := newTestManager(t, newFakeRepo(r), media, newFakeObjects(), &fakeMailer{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.startRecorder(ctx, r)
	<-started
	cancel()

	begin := time.Now()
	m.WaitRecorders(50 * time.Millisecond)
	elapsed := time.Since(begin)

	require.GreaterOrEqual(t, elapsed, 50*time.Millisecond)
	require.Less(t, elapsed, 15*time.Second)
	m.mu.Lock()
	_, alive := m.live[r.ID]
	m.mu.Unlock()
	require.True(t, alive, "o gravador segue preso")

	close(release)
	m.WaitRecorders(30 * time.Second)
}

// O tick pode estar no meio do laço quando o desligamento começa. Um gravador que
// nasce depois da espera não seria esperado por ninguém.
func TestWaitRecorders_NaoNasceGravadorDepoisDeEsperar(t *testing.T) {
	r := recordingRow(time.Hour)
	var calls atomic.Int32
	media := &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		calls.Add(1)
		<-ctx.Done()
		return nil
	}}
	m := newTestManager(t, newFakeRepo(r), media, newFakeObjects(), &fakeMailer{})

	m.WaitRecorders(time.Second)
	m.startRecorder(context.Background(), r)

	m.mu.Lock()
	n := len(m.live)
	m.mu.Unlock()
	require.Zero(t, n)
	require.Zero(t, calls.Load())
}
