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

// slowDropRepo segura o MarkDropAlerted, como um banco lento. Respeita ctx, como o pgx.
type slowDropRepo struct {
	*fakeRepo
	block func(ctx context.Context)
}

func (r *slowDropRepo) MarkDropAlerted(ctx context.Context, id uuid.UUID) (bool, error) {
	r.block(ctx)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return r.fakeRepo.MarkDropAlerted(ctx, id)
}

// deletedStationRepo é o Store depois de a emissora sumir: o JOIN não acha a linha.
type deletedStationRepo struct{ *fakeRepo }

func (r *deletedStationRepo) StreamURL(context.Context, uuid.UUID) (string, error) {
	return "", ErrNotFound
}

// recordWithin roda record com prazo: um SMTP mudo já o prendeu para sempre.
func recordWithin(t *testing.T, m *Manager, r Recording) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.record(context.Background(), r)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("record não voltou")
	}
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
	require.Empty(t, sentMails(m, mail))
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
	require.Len(t, sentMails(m, mail), 1, "um aviso por gravação, mesmo com várias quedas")
	require.Contains(t, sentMails(m, mail)[0].subject, "não conectou")
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

	require.Len(t, sentMails(m, mail), 1)
	require.Contains(t, sentMails(m, mail)[0].subject, "captura parou")
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

// O e-mail sai em paralelo: com SMTP mudo, record não pode ficar preso esperando,
// senão a linha nunca sai de `gravando` e a finalização nunca começa.
func TestRecord_SMTPMudoNaoPrendeAFinalizacao(t *testing.T) {
	r := recordingRow(200 * time.Millisecond)
	repo, muted := newFakeRepo(r), newMutedMailer(t)
	m := newTestManager(t, repo, &fakeMedia{capture: hungCapture}, newFakeObjects(), &fakeMailer{})
	m.mail = muted

	recordWithin(t, m, r)

	require.Equal(t, StatusProcessando, repo.snapshot(r.ID).Status)
	require.Eventually(t, func() bool { return muted.calls.Load() == 1 }, 2*time.Second, time.Millisecond,
		"o aviso tem que ter saído, senão o teste não provou nada")
}

func TestRecord_SMTPMudoNaoCegaOWatchdog(t *testing.T) {
	r := recordingRow(400 * time.Millisecond)
	var calls atomic.Int32
	media := &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		calls.Add(1)
		<-ctx.Done()
		return nil
	}}
	m := newTestManager(t, newFakeRepo(r), media, newFakeObjects(), &fakeMailer{})
	m.mail = newMutedMailer(t)

	recordWithin(t, m, r)

	require.Greater(t, calls.Load(), int32(2), "o watchdog segue derrubando ffmpeg travado")
}

// O banco segura o watchdog só até o fim da janela, e com a janela encerrada ele não
// tenta de novo.
func TestRecord_MarcacaoDeQuedaPresaAcabaComAJanela(t *testing.T) {
	r := recordingRow(150 * time.Millisecond)
	var started atomic.Int32
	repo := &slowDropRepo{fakeRepo: newFakeRepo(r), block: func(ctx context.Context) {
		started.Add(1)
		<-ctx.Done()
	}}
	mail := &fakeMailer{}
	m := newTestManager(t, repo, &fakeMedia{capture: hungCapture}, newFakeObjects(), mail)

	recordWithin(t, m, r)

	require.Equal(t, int32(1), started.Load(), "o watchdog agiu depois do fim da janela")
	require.Equal(t, StatusProcessando, repo.snapshot(r.ID).Status)
	require.Empty(t, sentMails(m, mail))
}

// Stream fora: cada ffmpeg desiste sozinho e nenhum chega à idade de morrer. O alerta
// sai mesmo assim, e o motivo real do ffmpeg não é trocado pelo do watchdog.
func TestRecord_StreamForaAvisaSemMatar(t *testing.T) {
	r := recordingRow(250 * time.Millisecond)
	repo, mail := newFakeRepo(r), &fakeMailer{}
	media := &fakeMedia{capture: func(context.Context, string, string) error {
		return errors.New("caiu")
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), mail)

	m.record(context.Background(), r)

	got := sentMails(m, mail)
	require.Len(t, got, 1)
	require.Contains(t, got[0].subject, "não conectou")
	require.Equal(t, "caiu", *repo.snapshot(r.ID).LastError)
}

// O stream volta quando o relógio de bytes já está vencido. O ffmpeg novo está
// saudável, mas leva ~16s até a primeira escrita, e não pode morrer antes disso.
func TestRecord_TentativaNovaSobreviveComBytesAtrasados(t *testing.T) {
	r := recordingRow(700 * time.Millisecond)
	repo, mail := newFakeRepo(r), &fakeMailer{}
	var calls atomic.Int32
	var killed atomic.Bool
	media := &fakeMedia{capture: func(ctx context.Context, _, dir string) error {
		if calls.Add(1) == 1 {
			time.Sleep(250 * time.Millisecond)
			return errors.New("caiu")
		}
		select {
		case <-time.After(150 * time.Millisecond):
		case <-ctx.Done():
			killed.Store(true)
			return nil
		}
		feed(ctx, dir)
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), mail)
	m.cfg.StallAfter = 300 * time.Millisecond

	m.record(context.Background(), r)

	require.False(t, killed.Load(), "morreu antes de completar StallAfter de idade")
	require.Equal(t, int32(2), calls.Load())
	got := sentMails(m, mail)
	require.Len(t, got, 1, "o alerta segue o relógio de bytes, não a idade")
	require.Contains(t, got[0].subject, "não conectou")
}

// A idade só adia a morte: ffmpeg que fica pendurado continua morrendo.
func TestRecord_TentativaPenduradaMorreAoCompletarStallAfter(t *testing.T) {
	r := recordingRow(800 * time.Millisecond)
	repo := newFakeRepo(r)
	var calls atomic.Int32
	var secondAge atomic.Int64
	media := &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		n := calls.Add(1)
		if n == 1 {
			time.Sleep(250 * time.Millisecond)
			return errors.New("caiu")
		}
		begin := time.Now()
		<-ctx.Done()
		if n == 2 {
			secondAge.Store(int64(time.Since(begin)))
		}
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})
	m.cfg.StallAfter = 300 * time.Millisecond

	m.record(context.Background(), r)

	require.GreaterOrEqual(t, calls.Load(), int32(3), "a tentativa pendurada tem que morrer")
	require.GreaterOrEqual(t, time.Duration(secondAge.Load()), 290*time.Millisecond)
	require.Contains(t, *repo.snapshot(r.ID).LastError, "stream sem dados há", "sem isto, `falhou` fica sem motivo")
}

// A tentativa acabou sozinha e o watchdog passa na espera até a próxima: não há o que
// matar, e o erro real do ffmpeg não pode ser trocado por "sem dados".
func TestRecord_EntreTentativasNaoTrocaOMotivo(t *testing.T) {
	r := recordingRow(300 * time.Millisecond)
	repo := newFakeRepo(r)
	media := &fakeMedia{capture: func(context.Context, string, string) error {
		time.Sleep(150 * time.Millisecond)
		return errors.New("caiu")
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})
	m.cfg.PollEvery = 100 * time.Millisecond
	m.cfg.StallAfter = 120 * time.Millisecond
	m.cfg.RetryDelay = time.Hour

	m.record(context.Background(), r)

	require.Equal(t, "caiu", *repo.snapshot(r.ID).LastError)
}

func TestRecord_MotivoDoWatchdogUsaStallAfter(t *testing.T) {
	require.Equal(t, "stream sem dados há 60s", stalledMessage(60*time.Second))
}

func TestRecord_SlotSoDerrubaTentativaVivaEVelha(t *testing.T) {
	s := &attemptSlot{}
	require.False(t, s.restartIfOlder(0), "sem tentativa")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.set(cancel)
	require.False(t, s.restartIfOlder(time.Hour), "nova demais")
	require.NoError(t, ctx.Err())
	require.True(t, s.restartIfOlder(0))
	require.Error(t, ctx.Err())
	require.False(t, s.restartIfOlder(0), "já derrubada: não conta de novo enquanto o ffmpeg fecha")

	_, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	s.set(cancel2)
	s.clear()
	require.False(t, s.restartIfOlder(0), "entre tentativas não há o que derrubar")
}

func TestRecord_EmissoraExcluidaDizOMotivo(t *testing.T) {
	r := recordingRow(60 * time.Millisecond)
	repo := &deletedStationRepo{newFakeRepo(r)}
	var calls atomic.Int32
	media := &fakeMedia{capture: func(context.Context, string, string) error {
		calls.Add(1)
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})

	m.record(context.Background(), r)

	require.Zero(t, calls.Load())
	require.Equal(t, "a emissora foi excluída", *repo.snapshot(r.ID).LastError)
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

// O aviso que já saiu de notify ainda precisa de tempo para chegar ao SMTP.
func TestWaitRecorders_EsperaOEmailEmAndamento(t *testing.T) {
	r := recordingRow(time.Hour)
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), &fakeMailer{})
	var sent atomic.Bool
	m.mail = mailerFunc(func() error {
		time.Sleep(50 * time.Millisecond)
		sent.Store(true)
		return nil
	})

	m.notifyFailed(r)
	m.WaitRecorders(30 * time.Second)

	require.True(t, sent.Load(), "voltou antes de o e-mail sair")
}

// O mailer ignora contexto, então só o timeout tira o desligamento de um SMTP mudo.
func TestWaitRecorders_EmailPresoNaoPassaDoTimeout(t *testing.T) {
	r := recordingRow(time.Hour)
	muted := newMutedMailer(t)
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), &fakeMailer{})
	m.mail = muted

	m.notifyFailed(r)
	begin := time.Now()
	m.WaitRecorders(50 * time.Millisecond)
	elapsed := time.Since(begin)

	require.GreaterOrEqual(t, elapsed, 50*time.Millisecond)
	require.Less(t, elapsed, 15*time.Second)
}

// Add depois de Wait é uso inválido de WaitGroup, então o aviso é descartado.
func TestWaitRecorders_DescartaEmailDepoisDeEsperar(t *testing.T) {
	r := recordingRow(time.Hour)
	mail := &fakeMailer{}
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), mail)

	m.WaitRecorders(time.Second)
	m.notifyFailed(r)

	require.Empty(t, sentMails(m, mail))
}

// A goroutine pode nascer com a linha já cancelada: a que o tick tem na mão é velha.
func TestRecord_LinhaQueSaiuDeGravandoNaoCaptura(t *testing.T) {
	r := recordingRow(time.Hour)
	stored := r
	stored.Status = StatusProcessando
	var calls atomic.Int32
	media := &fakeMedia{capture: func(context.Context, string, string) error {
		calls.Add(1)
		return nil
	}}
	m := newTestManager(t, newFakeRepo(stored), media, newFakeObjects(), &fakeMailer{})

	recordWithin(t, m, r)

	require.Zero(t, calls.Load())
}

// Linha excluída entre o tick e a goroutine: nada a gravar, e nada a marcar.
func TestRecord_LinhaExcluidaNaoCaptura(t *testing.T) {
	r := recordingRow(time.Hour)
	var calls atomic.Int32
	media := &fakeMedia{capture: func(context.Context, string, string) error {
		calls.Add(1)
		return nil
	}}
	m := newTestManager(t, newFakeRepo(), media, newFakeObjects(), &fakeMailer{})

	recordWithin(t, m, r)

	require.Zero(t, calls.Load())
}

// Neste ponto o watch ainda não subiu e o tick não religa quem está em live: sem
// prazo, um banco pendurado seguraria a gravação para sempre.
func TestRecord_ConferenciaDeStatusPresaAcabaNoPrazo(t *testing.T) {
	r := recordingRow(time.Hour)
	repo := &tickRepo{fakeRepo: newFakeRepo(r)}
	repo.hangGet.Store(true)
	var calls atomic.Int32
	media := &fakeMedia{capture: func(context.Context, string, string) error {
		calls.Add(1)
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})
	m.cfg.TickTimeout = 20 * time.Millisecond
	ctx := liveContext(t, m)

	m.startRecorder(ctx, r)

	require.Eventually(t, func() bool { return liveCount(m) == 0 }, 2*time.Second, time.Millisecond,
		"o gravador ficou preso em live")
	require.Zero(t, calls.Load())
	require.Equal(t, StatusGravando, repo.snapshot(r.ID).Status)
}

// Sem saber o status, capturar arriscaria gravar depois de um cancelamento. O tick
// tenta de novo, então a falha custa no máximo um intervalo.
func TestRecord_FalhaAoConferirStatusTentaNoProximoTick(t *testing.T) {
	r := recordingRow(time.Hour)
	var broken atomic.Bool
	broken.Store(true)
	repo := &tickRepo{fakeRepo: newFakeRepo(r), getErr: func() error {
		if broken.Load() {
			return errors.New("banco fora")
		}
		return nil
	}}
	captured := make(chan struct{}, 1)
	media := &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		select {
		case captured <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})
	ctx := liveContext(t, m)

	m.tick(ctx)
	require.Eventually(t, func() bool { return liveCount(m) == 0 }, time.Second, time.Millisecond)
	require.Empty(t, captured)
	require.Equal(t, StatusGravando, repo.snapshot(r.ID).Status)

	broken.Store(false)
	m.tick(ctx)
	select {
	case <-captured:
	case <-time.After(time.Second):
		t.Fatal("o tick seguinte não retomou")
	}
}

// Com o stream recusando na hora, um retry fixo de 5s dava ~720 conexões por hora do
// mesmo IP da detecção, e é esse padrão que já levou painel a banir a VM. A espera
// dobra a cada tentativa sem áudio, para em RetryMax e volta ao início quando a
// tentativa gravou.
func TestRecord_EsperaEntreTentativasCresceTemTetoEZeraAoGravar(t *testing.T) {
	r := recordingRow(3 * time.Second)
	repo := newFakeRepo(r)
	var mu sync.Mutex
	var starts []time.Time
	media := &fakeMedia{capture: func(ctx context.Context, _, dir string) error {
		mu.Lock()
		starts = append(starts, time.Now())
		n := len(starts)
		mu.Unlock()
		switch {
		case n == 6:
			// Esta tentativa gravou: a espera seguinte volta a RetryDelay.
			writePiece(t, dir, "6", time.Now(), time.Now(), validADTS(3))
			return errors.New("caiu depois de gravar")
		case n <= 7:
			return errors.New("recusou")
		}
		<-ctx.Done()
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})
	m.cfg.RetryDelay, m.cfg.RetryMax = 50*time.Millisecond, 200*time.Millisecond
	m.cfg.StallAfter = time.Hour // o watchdog não entra nesta conta

	m.record(context.Background(), r)

	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(starts), 8)
	wait := func(i int) time.Duration { return starts[i+1].Sub(starts[i]) }
	// Mínimos: time.After nunca dispara antes, então não oscilam.
	require.GreaterOrEqual(t, wait(0), 50*time.Millisecond)
	require.GreaterOrEqual(t, wait(1), 100*time.Millisecond, "dobra")
	require.GreaterOrEqual(t, wait(2), 200*time.Millisecond, "dobra de novo")
	require.GreaterOrEqual(t, wait(3), 200*time.Millisecond)
	// Teto: sem ele seria 400ms e 800ms.
	require.Less(t, wait(3), 350*time.Millisecond, "para em RetryMax")
	require.Less(t, wait(4), 350*time.Millisecond, "para em RetryMax")
	// Depois da tentativa que gravou: sem zerar seria 200ms.
	require.Less(t, wait(5), 150*time.Millisecond, "volta a RetryDelay depois de gravar")
	require.GreaterOrEqual(t, wait(6), 100*time.Millisecond, "e volta a dobrar")
}
