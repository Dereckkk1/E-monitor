package recording

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Contrato com o handler: é o RecordingService de api/handlers/recordings.go.
var _ interface {
	Create(ctx context.Context, in CreateInput) (*Recording, error)
	List(ctx context.Context, limit, offset int) ([]Recording, int, error)
	Cancel(ctx context.Context, id uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
	Audio(ctx context.Context, id uuid.UUID) (io.ReadCloser, *Recording, int64, error)
} = (*Manager)(nil)

func holdCapture() *fakeMedia {
	return &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		<-ctx.Done()
		return nil
	}, duration: time.Minute}
}

// liveContext encerra os gravadores antes de o TempDir sumir: um gravador solto
// recriaria a pasta durante a limpeza.
func liveContext(t *testing.T, m *Manager) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		m.WaitRecorders(10 * time.Second)
	})
	return ctx
}

func liveCount(m *Manager) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.live)
}

// tickRepo injeta falhas e pontos de parada no que o tick chama.
type tickRepo struct {
	*fakeRepo
	missedErr, claimErr error
	listErr             map[string]error
	getErr              func() error
	afterGet            func()
	hangMissed          bool
	hangList            string
	hangEnded           time.Time
	claimDeadline       time.Time
	afterMissed         func()
	afterList           func(status string)
	claims, lists       atomic.Int32
	expiredCalls        atomic.Int32
}

// hang imita o banco pendurado: como o pgx, só solta quando o ctx acaba.
func hang(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (r *tickRepo) FailMissed(ctx context.Context, now time.Time, reason string) ([]Recording, error) {
	if r.hangMissed {
		err := hang(ctx)
		r.hangEnded = time.Now()
		return nil, err
	}
	rows, err := r.fakeRepo.FailMissed(ctx, now, reason)
	if r.afterMissed != nil {
		r.afterMissed()
	}
	if r.missedErr != nil {
		return rows, r.missedErr
	}
	return rows, err
}

func (r *tickRepo) ClaimDue(ctx context.Context, startBefore, now time.Time) (int64, error) {
	r.claims.Add(1)
	r.claimDeadline, _ = ctx.Deadline()
	if r.claimErr != nil {
		return 0, r.claimErr
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return r.fakeRepo.ClaimDue(ctx, startBefore, now)
}

func (r *tickRepo) ListByStatus(ctx context.Context, status string) ([]Recording, error) {
	r.lists.Add(1)
	if r.hangList == status {
		return nil, hang(ctx)
	}
	if err := r.listErr[status]; err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := r.fakeRepo.ListByStatus(ctx, status)
	if r.afterList != nil {
		r.afterList(status)
	}
	return rows, err
}

func (r *tickRepo) Get(ctx context.Context, id uuid.UUID) (*Recording, error) {
	if r.getErr != nil {
		if err := r.getErr(); err != nil {
			return nil, err
		}
	}
	rec, err := r.fakeRepo.Get(ctx, id)
	if r.afterGet != nil {
		r.afterGet()
	}
	return rec, err
}

func (r *tickRepo) Expired(ctx context.Context, now time.Time) ([]Recording, error) {
	r.expiredCalls.Add(1)
	return r.fakeRepo.Expired(ctx, now)
}

// stuckObjects imita um bucket lento: o Delete só volta quando o teste solta.
type stuckObjects struct {
	*fakeObjects
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newStuckObjects(t *testing.T) *stuckObjects {
	t.Helper()
	o := &stuckObjects{fakeObjects: newFakeObjects(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	t.Cleanup(o.unblock)
	return o
}

func (o *stuckObjects) unblock() { o.once.Do(func() { close(o.release) }) }

func (o *stuckObjects) Delete(ctx context.Context, key string) error {
	select {
	case o.entered <- struct{}{}:
	default:
	}
	select {
	case <-o.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return o.fakeObjects.Delete(ctx, key)
}

// runManager sobe o Run e, no fim do teste, confere que ele sai com o ctx.
func runManager(t *testing.T, m *Manager) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("Run não saiu depois de o ctx acabar")
		}
		m.WaitRecorders(10 * time.Second)
	})
}

func expiredRow() Recording {
	key := "recordings/x.m4a"
	past := time.Now().Add(-time.Hour)
	return Recording{ID: uuid.New(), Status: StatusConcluida, ObjectKey: &key, ExpiresAt: &past, Gaps: []Gap{}}
}

func TestTick_RespeitaOPreRoll(t *testing.T) {
	now := time.Now()
	future := Recording{ID: uuid.New(), StartsAt: now.Add(time.Minute), EndsAt: now.Add(time.Hour), Status: StatusProgramada, Gaps: []Gap{}}
	soon := Recording{ID: uuid.New(), StartsAt: now.Add(10 * time.Second), EndsAt: now.Add(time.Hour), Status: StatusProgramada, Gaps: []Gap{}}
	repo := newFakeRepo(future, soon)
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})
	ctx := liveContext(t, m)

	m.tick(ctx)

	require.Equal(t, StatusProgramada, repo.snapshot(future.ID).Status)
	require.Equal(t, StatusGravando, repo.snapshot(soon.ID).Status)
}

func TestTick_RetomaGravacaoDepoisDeReiniciar(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour), Status: StatusGravando, Gaps: []Gap{}}
	repo := newFakeRepo(r)
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

	select {
	case <-captured:
	case <-time.After(time.Second):
		t.Fatal("a captura não foi retomada")
	}
}

func TestTick_JanelaAcabouDuranteOReinicioFinaliza(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StartsAt: now.Add(-time.Hour), EndsAt: now.Add(-time.Minute), Status: StatusGravando, Gaps: []Gap{}}
	repo := newFakeRepo(r)
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})
	dir := m.dirFor(r.ID)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writePiece(t, dir, "1", r.StartsAt, r.EndsAt, validADTS(3))

	m.tick(context.Background())

	require.Eventually(t, func() bool {
		return repo.snapshot(r.ID).HasAudio
	}, time.Second, 5*time.Millisecond)
}

// Com gravador vivo quem fecha é ele: o tick não pode passar na frente e tirar a
// linha de `gravando` antes de o último pedaço fechar.
func TestTick_JanelaAcabouComGravadorVivoNaoMarca(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StartsAt: now.Add(-time.Hour), EndsAt: now.Add(-time.Second), Status: StatusGravando, Gaps: []Gap{}}
	repo := newFakeRepo(r)
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})
	m.live[r.ID] = &liveRecorder{}

	m.tick(context.Background())

	require.Equal(t, StatusGravando, repo.snapshot(r.ID).Status)
}

func TestTick_JanelaPerdidaFalhaEAvisa(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StationName: "R", CreatorEmail: "a@b.c", StartsAt: now.Add(-2 * time.Hour), EndsAt: now.Add(-time.Hour), Status: StatusProgramada, Gaps: []Gap{}}
	repo, mail := newFakeRepo(r), &fakeMailer{}
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), mail)

	m.tick(context.Background())

	require.Equal(t, StatusFalhou, repo.snapshot(r.ID).Status)
	require.Len(t, sentMails(m, mail), 1)
	require.Contains(t, sentMails(m, mail)[0].subject, "não gravou")
}

// O Store devolve as linhas que já viraram `falhou` junto com o erro: são as únicas
// que ainda podem ser avisadas, porque o próximo tick não as encontra mais.
func TestTick_FailMissedComErroAindaAvisaESegue(t *testing.T) {
	now := time.Now()
	missed := Recording{ID: uuid.New(), StationName: "R", CreatorEmail: "a@b.c", StartsAt: now.Add(-2 * time.Hour), EndsAt: now.Add(-time.Hour), Status: StatusProgramada, Gaps: []Gap{}}
	due := Recording{ID: uuid.New(), StartsAt: now, EndsAt: now.Add(time.Hour), Status: StatusProgramada, Gaps: []Gap{}}
	repo, mail := &tickRepo{fakeRepo: newFakeRepo(missed, due), missedErr: errors.New("banco fora")}, &fakeMailer{}
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), mail)
	ctx := liveContext(t, m)

	m.tick(ctx)

	require.Len(t, sentMails(m, mail), 1)
	require.Equal(t, StatusGravando, repo.snapshot(due.ID).Status)
}

func TestTick_ErroAoIniciarNaoParaAsOutrasEtapas(t *testing.T) {
	now := time.Now()
	recording := Recording{ID: uuid.New(), StartsAt: now, EndsAt: now.Add(time.Hour), Status: StatusGravando, Gaps: []Gap{}}
	processing := Recording{ID: uuid.New(), StartsAt: now.Add(-time.Hour), EndsAt: now, Status: StatusProcessando, Gaps: []Gap{}}
	repo := &tickRepo{fakeRepo: newFakeRepo(recording, processing), claimErr: errors.New("banco fora")}
	captured := make(chan struct{}, 1)
	media := &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		captured <- struct{}{}
		<-ctx.Done()
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})
	ctx := liveContext(t, m)

	m.tick(ctx)

	select {
	case <-captured:
	case <-time.After(time.Second):
		t.Fatal("a retomada não rodou")
	}
	require.Eventually(t, func() bool {
		return repo.snapshot(processing.ID).Status == StatusFalhou
	}, time.Second, 5*time.Millisecond, "a finalização não rodou")
}

func TestTick_ErroAoListarGravandoAindaFinaliza(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StartsAt: now.Add(-time.Hour), EndsAt: now, Status: StatusProcessando, Gaps: []Gap{}}
	repo := &tickRepo{fakeRepo: newFakeRepo(r), listErr: map[string]error{StatusGravando: errors.New("banco fora")}}
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})

	m.tick(context.Background())

	require.Eventually(t, func() bool {
		return repo.snapshot(r.ID).Status == StatusFalhou
	}, time.Second, 5*time.Millisecond)
}

func TestTick_ContextoCanceladoNaoFazNada(t *testing.T) {
	repo := &tickRepo{fakeRepo: newFakeRepo()}
	missedCalled := false
	repo.afterMissed = func() { missedCalled = true }
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	m.tick(ctx)

	require.False(t, missedCalled)
	require.Zero(t, repo.claims.Load())
	require.Zero(t, repo.lists.Load())
}

// O desligamento pode chegar com o tick no meio: as etapas seguintes não começam.
func TestTick_ContextoCanceladoNoMeioParaAsProximasEtapas(t *testing.T) {
	repo := &tickRepo{fakeRepo: newFakeRepo()}
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo.afterMissed = cancel

	m.tick(ctx)

	require.Zero(t, repo.claims.Load())
	require.Zero(t, repo.lists.Load())
}

// O admin cancela depois de o tick listar `gravando`: o stopRecorder volta sem nada
// para parar, e o tick segue com a linha velha na mão. Religar aqui deixaria a linha
// em `processando` com gravador vivo, capturando depois do cancelamento e segurando
// a finalização até ends_at.
func TestTick_CancelamentoDepoisDeListarNaoReligaACaptura(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StartsAt: now, EndsAt: now.Add(time.Hour), Status: StatusGravando, Gaps: []Gap{}}
	repo := &tickRepo{fakeRepo: newFakeRepo(r)}
	var captures atomic.Int32
	media := &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		captures.Add(1)
		<-ctx.Done()
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})
	ctx := liveContext(t, m)
	var once sync.Once
	repo.afterList = func(status string) {
		if status == StatusGravando {
			once.Do(func() { require.NoError(t, m.Cancel(context.Background(), r.ID)) })
		}
	}

	m.tick(ctx)

	require.Eventually(t, func() bool { return liveCount(m) == 0 }, time.Second, 5*time.Millisecond,
		"gravador vivo depois do cancelamento")
	require.Zero(t, captures.Load())
	require.Eventually(t, func() bool {
		m.tick(ctx)
		return repo.snapshot(r.ID).Status == StatusCancelada
	}, time.Second, 5*time.Millisecond, "a finalização tem que fechar a linha sem esperar ends_at")
}

// Banco pendurado: a etapa acaba no prazo dela, e a seguinte ainda tem prazo inteiro.
func TestTick_EtapaPresaNaoImpedeASeguinte(t *testing.T) {
	now := time.Now()
	due := Recording{ID: uuid.New(), StartsAt: now, EndsAt: now.Add(time.Hour), Status: StatusProgramada, Gaps: []Gap{}}
	repo := &tickRepo{fakeRepo: newFakeRepo(due), hangMissed: true}
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})
	m.cfg.TickTimeout = 20 * time.Millisecond
	ctx := liveContext(t, m)

	tickWithin(t, m, ctx)

	require.Equal(t, StatusGravando, repo.snapshot(due.ID).Status)
	require.True(t, repo.claimDeadline.After(repo.hangEnded), "a etapa seguinte herdou o prazo estourado")
}

func TestTick_ListagemPresaAindaFinaliza(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StartsAt: now.Add(-time.Hour), EndsAt: now, Status: StatusProcessando, Gaps: []Gap{}}
	repo := &tickRepo{fakeRepo: newFakeRepo(r), hangList: StatusGravando}
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})
	m.cfg.TickTimeout = 20 * time.Millisecond

	tickWithin(t, m, context.Background())

	require.Eventually(t, func() bool {
		return repo.snapshot(r.ID).Status == StatusFalhou
	}, time.Second, 5*time.Millisecond)
}

// O prazo é da consulta ao banco. Se o gravador herdasse esse ctx, morreria assim que
// a etapa acabasse.
func TestTick_GravadorSobreviveAoPrazoDaEtapa(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StartsAt: now, EndsAt: now.Add(time.Hour), Status: StatusGravando, Gaps: []Gap{}}
	m := newTestManager(t, newFakeRepo(r), holdCapture(), newFakeObjects(), &fakeMailer{})
	m.cfg.TickTimeout = 20 * time.Millisecond
	ctx := liveContext(t, m)

	m.tick(ctx)

	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 1, liveCount(m), "o gravador morreu com o prazo da etapa")
}

func TestTick_FinalizacaoSobreviveAoPrazoDaEtapa(t *testing.T) {
	r := processing(time.Now().Add(-time.Hour).Truncate(time.Second))
	repo := newFakeRepo(r)
	m := newTestManager(t, repo, &fakeMedia{blockRemux: true}, newFakeObjects(), &fakeMailer{})
	m.cfg.TickTimeout = 20 * time.Millisecond
	seedPieces(t, m, r)
	running := func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		_, ok := m.finalizing[r.ID]
		return ok
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		require.Eventually(t, func() bool { return !running() }, 5*time.Second, 5*time.Millisecond)
	})

	m.tick(ctx)

	time.Sleep(100 * time.Millisecond)
	require.True(t, running(), "a finalização morreu com o prazo da etapa")
	require.Nil(t, repo.snapshot(r.ID).LastError)
}

// tickWithin falha em vez de pendurar a suíte: tick preso é justamente o defeito.
func tickWithin(t *testing.T, m *Manager, ctx context.Context) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.tick(ctx)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("o tick ficou preso")
	}
}

func TestPrune_ApagaObjetoAntesDeMarcar(t *testing.T) {
	r := expiredRow()
	repo, objs := newFakeRepo(r), newFakeObjects()
	m := newTestManager(t, repo, holdCapture(), objs, &fakeMailer{})

	objs.deleteErr = errors.New("bucket fora")
	m.prune(context.Background())
	require.Equal(t, StatusConcluida, repo.snapshot(r.ID).Status, "falhou o delete: a linha continua e repete")

	objs.deleteErr = nil
	m.prune(context.Background())
	require.Equal(t, StatusExpirada, repo.snapshot(r.ID).Status)
	require.Equal(t, []string{*r.ObjectKey}, objs.deleted)
}

// Gravação marcada para agora não pode esperar o bucket: a linha chega depois do tick
// de subida, com o prune preso no Delete.
func TestPrune_BucketLentoNaoAtrasaOTick(t *testing.T) {
	repo, objs := newFakeRepo(expiredRow()), newStuckObjects(t)
	m := newTestManager(t, repo, holdCapture(), objs, &fakeMailer{})
	m.cfg.TickEvery = 5 * time.Millisecond
	runManager(t, m)

	select {
	case <-objs.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("o prune não rodou na subida")
	}
	now := time.Now()
	due := Recording{ID: uuid.New(), StartsAt: now, EndsAt: now.Add(time.Hour), Status: StatusProgramada, Gaps: []Gap{}}
	repo.mu.Lock()
	repo.rows[due.ID] = &due
	repo.mu.Unlock()

	require.Eventually(t, func() bool {
		return repo.snapshot(due.ID).Status == StatusGravando
	}, 2*time.Second, 5*time.Millisecond)
}

func TestPrune_PassadaLentaNaoSobrepoe(t *testing.T) {
	r := expiredRow()
	repo, objs := &tickRepo{fakeRepo: newFakeRepo(r)}, newStuckObjects(t)
	m := newTestManager(t, repo, holdCapture(), objs, &fakeMailer{})
	m.cfg.PruneEvery = 2 * time.Millisecond
	runManager(t, m)

	select {
	case <-objs.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("o prune não rodou na subida")
	}
	time.Sleep(50 * time.Millisecond)
	require.EqualValues(t, 1, repo.expiredCalls.Load(), "passada nova com a anterior em andamento")

	objs.unblock()
	require.Eventually(t, func() bool {
		return repo.snapshot(r.ID).Status == StatusExpirada && repo.expiredCalls.Load() > 1
	}, 2*time.Second, 5*time.Millisecond, "a passada seguinte tem que rodar")
}

// cancelingObjects encerra o ctx no meio da passada, como um desligamento.
type cancelingObjects struct {
	*fakeObjects
	cancel context.CancelFunc
}

func (o *cancelingObjects) Delete(ctx context.Context, key string) error {
	o.cancel()
	return o.fakeObjects.Delete(ctx, key)
}

func TestPrune_ContextoCanceladoParaAPassada(t *testing.T) {
	repo := newFakeRepo(expiredRow(), expiredRow(), expiredRow())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	objs := &cancelingObjects{fakeObjects: newFakeObjects(), cancel: cancel}
	m := newTestManager(t, repo, holdCapture(), objs, &fakeMailer{})

	m.prune(ctx)

	require.Len(t, objs.deleted, 1)
}

func TestCreate(t *testing.T) {
	repo := newFakeRepo()
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})
	m.now = func() time.Time { return windowNow }

	got, err := m.Create(context.Background(), CreateInput{StationID: uuid.New(), Day: "2026-11-05", Start: "14:00", End: "15:00"})
	require.NoError(t, err)
	require.Equal(t, StatusProgramada, got.Status)

	repo.url = ""
	_, err = m.Create(context.Background(), CreateInput{StationID: uuid.New(), Day: "2026-11-05", Start: "16:00", End: "17:00"})
	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	require.Equal(t, "esta emissora não tem URL de stream cadastrada", ve.Msg)
}

func TestCreate_StreamSoComEspacos(t *testing.T) {
	repo := newFakeRepo()
	repo.url = "  \t "
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})
	m.now = func() time.Time { return windowNow }

	_, err := m.Create(context.Background(), CreateInput{StationID: uuid.New(), Day: "2026-11-05", Start: "14:00", End: "15:00"})

	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	require.Equal(t, "esta emissora não tem URL de stream cadastrada", ve.Msg)
}

func TestCreate_Sobreposicao(t *testing.T) {
	repo := newFakeRepo()
	repo.insertErr = ErrOverlap
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})
	m.now = func() time.Time { return windowNow }

	_, err := m.Create(context.Background(), CreateInput{StationID: uuid.New(), Day: "2026-11-05", Start: "14:00", End: "15:00"})

	require.ErrorIs(t, err, ErrOverlap)
}

// noStationRepo é o Store quando a emissora não existe.
type noStationRepo struct{ *fakeRepo }

func (r *noStationRepo) StationInfo(context.Context, uuid.UUID) (string, string, error) {
	return "", "", ErrNotFound
}

func TestCreate_EmissoraInexistente(t *testing.T) {
	m := newTestManager(t, &noStationRepo{newFakeRepo()}, holdCapture(), newFakeObjects(), &fakeMailer{})
	m.now = func() time.Time { return windowNow }

	_, err := m.Create(context.Background(), CreateInput{StationID: uuid.New(), Day: "2026-11-05", Start: "14:00", End: "15:00"})

	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	require.Equal(t, "emissora não encontrada", ve.Msg)
}

// O status muda ANTES de parar o gravador: na ordem inversa, um tick no meio
// veria `gravando` sem gravador vivo e religaria a captura.
func TestCancel_Gravando(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StartsAt: now, EndsAt: now.Add(time.Hour), Status: StatusGravando, Gaps: []Gap{}}
	repo := newFakeRepo(r)
	started := make(chan struct{})
	var once sync.Once
	var statusWhenStopped atomic.Value
	media := &fakeMedia{capture: func(ctx context.Context, _, _ string) error {
		once.Do(func() { close(started) })
		<-ctx.Done()
		statusWhenStopped.Store(repo.snapshot(r.ID).Status)
		return nil
	}}
	m := newTestManager(t, repo, media, newFakeObjects(), &fakeMailer{})
	ctx := liveContext(t, m)
	m.startRecorder(ctx, r)
	<-started

	require.NoError(t, m.Cancel(context.Background(), r.ID))

	got := repo.snapshot(r.ID)
	require.Equal(t, StatusProcessando, got.Status)
	require.NotNil(t, got.CanceledAt)
	require.Equal(t, StatusProcessando, statusWhenStopped.Load(), "parou o gravador antes de mudar o status")
	require.Zero(t, liveCount(m))
}

func TestCancel_Programada(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour), Status: StatusProgramada, Gaps: []Gap{}}
	repo := newFakeRepo(r)
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})

	require.NoError(t, m.Cancel(context.Background(), r.ID))
	require.Equal(t, StatusCancelada, repo.snapshot(r.ID).Status)
	require.ErrorIs(t, m.Cancel(context.Background(), r.ID), ErrBusy)
}

func TestDelete(t *testing.T) {
	key := "recordings/x.m4a"
	done := Recording{ID: uuid.New(), Status: StatusConcluida, ObjectKey: &key, Gaps: []Gap{}}
	busy := Recording{ID: uuid.New(), Status: StatusGravando, Gaps: []Gap{}}
	repo, objs := newFakeRepo(done, busy), newFakeObjects()
	m := newTestManager(t, repo, holdCapture(), objs, &fakeMailer{})

	require.ErrorIs(t, m.Delete(context.Background(), busy.ID), ErrBusy)
	require.NoError(t, m.Delete(context.Background(), done.ID))
	require.Equal(t, []string{key}, objs.deleted)
	_, err := m.repo.Get(context.Background(), done.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestDelete_ProcessandoDevolveBusy(t *testing.T) {
	key := "recordings/x.m4a"
	r := Recording{ID: uuid.New(), Status: StatusProcessando, ObjectKey: &key, Gaps: []Gap{}}
	repo, objs := newFakeRepo(r), newFakeObjects()
	m := newTestManager(t, repo, holdCapture(), objs, &fakeMailer{})

	require.ErrorIs(t, m.Delete(context.Background(), r.ID), ErrBusy)

	require.Empty(t, objs.deleted)
	require.Equal(t, StatusProcessando, repo.snapshot(r.ID).Status)
}

// Get vê `programada`, o tick reivindica no meio: apagar agora tiraria a gravação da
// lista e da guarda de deploy com o gravador vivo, deixando a pasta órfã.
func TestDelete_ReivindicadaNoMeioDevolveBusy(t *testing.T) {
	now := time.Now()
	r := Recording{ID: uuid.New(), StartsAt: now, EndsAt: now.Add(time.Hour), Status: StatusProgramada, Gaps: []Gap{}}
	repo := &tickRepo{fakeRepo: newFakeRepo(r)}
	var once sync.Once
	repo.afterGet = func() {
		once.Do(func() {
			_, err := repo.fakeRepo.ClaimDue(context.Background(), now, now)
			require.NoError(t, err)
		})
	}
	m := newTestManager(t, repo, holdCapture(), newFakeObjects(), &fakeMailer{})

	require.ErrorIs(t, m.Delete(context.Background(), r.ID), ErrBusy)

	require.Equal(t, StatusGravando, repo.snapshot(r.ID).Status)
}

// Mesma ordem do prune: sem a linha, o objeto nunca mais seria encontrado.
func TestDelete_FalhaNoBucketMantemALinha(t *testing.T) {
	key := "recordings/x.m4a"
	r := Recording{ID: uuid.New(), Status: StatusConcluida, ObjectKey: &key, Gaps: []Gap{}}
	repo, objs := newFakeRepo(r), newFakeObjects()
	objs.deleteErr = errors.New("bucket fora")
	m := newTestManager(t, repo, holdCapture(), objs, &fakeMailer{})

	require.ErrorIs(t, m.Delete(context.Background(), r.ID), objs.deleteErr)
	_, err := m.repo.Get(context.Background(), r.ID)
	require.NoError(t, err)
}

func TestAudio(t *testing.T) {
	key := "recordings/x.m4a"
	with := Recording{ID: uuid.New(), Status: StatusConcluida, ObjectKey: &key, Gaps: []Gap{}}
	without := Recording{ID: uuid.New(), Status: StatusExpirada, Gaps: []Gap{}}
	repo, objs := newFakeRepo(with, without), newFakeObjects()
	objs.puts[key] = []byte("audio")
	m := newTestManager(t, repo, holdCapture(), objs, &fakeMailer{})

	body, _, size, err := m.Audio(context.Background(), with.ID)
	require.NoError(t, err)
	defer body.Close()
	b, _ := io.ReadAll(body)
	require.Equal(t, "audio", string(b))
	require.EqualValues(t, 5, size)

	_, _, _, err = m.Audio(context.Background(), without.ID)
	require.ErrorIs(t, err, ErrNoAudio)
}

// A linha diz que há áudio e o bucket não tem: é 404 para quem pediu, não erro
// interno. Mesmo erro embrulhado que storage.Client.Get devolve.
func TestAudio_ObjetoAusenteNoBucket(t *testing.T) {
	key := "recordings/x.m4a"
	r := Recording{ID: uuid.New(), Status: StatusConcluida, ObjectKey: &key, Gaps: []Gap{}}
	objs := newFakeObjects()
	objs.getErr = fmt.Errorf("storage: get %s: %w", key, &s3types.NoSuchKey{})
	m := newTestManager(t, newFakeRepo(r), holdCapture(), objs, &fakeMailer{})

	_, _, _, err := m.Audio(context.Background(), r.ID)

	require.ErrorIs(t, err, ErrNoAudio)
}

// Bucket fora do ar não é áudio ausente: dizer 404 esconderia a falha.
func TestAudio_OutroErroDoBucketNaoViraNoAudio(t *testing.T) {
	key := "recordings/x.m4a"
	r := Recording{ID: uuid.New(), Status: StatusConcluida, ObjectKey: &key, Gaps: []Gap{}}
	objs := newFakeObjects()
	objs.getErr = errors.New("bucket fora")
	m := newTestManager(t, newFakeRepo(r), holdCapture(), objs, &fakeMailer{})

	_, _, _, err := m.Audio(context.Background(), r.ID)

	require.ErrorIs(t, err, objs.getErr)
	require.NotErrorIs(t, err, ErrNoAudio)
}
