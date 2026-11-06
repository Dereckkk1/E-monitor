package recording

import (
	"context"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/mailer"
)

// O Store e o FFmpeg reais têm que satisfazer as interfaces usadas nos testes.
var (
	_ Repo  = (*Store)(nil)
	_ Media = FFmpeg{}
)

// Repo é o que o Manager usa do Store.
type Repo interface {
	StationInfo(ctx context.Context, id uuid.UUID) (string, string, error)
	Insert(ctx context.Context, stationID uuid.UUID, stationName string, createdBy *uuid.UUID, from, to time.Time) (uuid.UUID, error)
	Get(ctx context.Context, id uuid.UUID) (*Recording, error)
	List(ctx context.Context, limit, offset int) ([]Recording, int, error)
	ListByStatus(ctx context.Context, status string) ([]Recording, error)
	ClaimDue(ctx context.Context, startBefore, now time.Time) (int64, error)
	FailMissed(ctx context.Context, now time.Time, reason string) ([]Recording, error)
	StreamURL(ctx context.Context, id uuid.UUID) (string, error)
	MarkProcessing(ctx context.Context, id uuid.UUID) error
	MarkDropAlerted(ctx context.Context, id uuid.UUID) (bool, error)
	SetLastError(ctx context.Context, id uuid.UUID, msg string) error
	Complete(ctx context.Context, id uuid.UUID, in CompleteInput) error
	CancelScheduled(ctx context.Context, id uuid.UUID, now time.Time) error
	CancelRecording(ctx context.Context, id uuid.UUID, now time.Time) error
	Delete(ctx context.Context, id uuid.UUID) error
	Expired(ctx context.Context, now time.Time) ([]Recording, error)
	MarkExpired(ctx context.Context, id uuid.UUID) error
}

// Media isola o ffmpeg, para os testes não dependerem dele.
type Media interface {
	Capture(ctx context.Context, streamURL, dir string) error
	Remux(ctx context.Context, in, out string) error
	Duration(ctx context.Context, path string) (time.Duration, error)
}

// ObjectStore é o subconjunto de storage.Client usado aqui.
type ObjectStore interface {
	Put(ctx context.Context, key string, body io.Reader, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, string, int64, error)
	Delete(ctx context.Context, key string) error
}

// Config ajusta o Manager. Zero assume o padrão de produção.
type Config struct {
	Root      string // <SEGMENTS_PATH>/_recordings
	BaseURL   string // link do e-mail
	TickEvery time.Duration
	// TickTimeout é o prazo de cada etapa do tick: banco pendurado não pode parar o
	// agendador para sempre.
	TickTimeout time.Duration
	PollEvery   time.Duration // medição de bytes do watchdog
	StallAfter  time.Duration // sem bytes novos por este tempo = caiu
	RetryDelay  time.Duration
	MinGap      time.Duration
	PreRoll     time.Duration
	PruneEvery  time.Duration
	// FinalizeRetry espaça as tentativas de finalização: cada uma reescreve o
	// arquivo inteiro em disco, até 330 MB, e com o bucket fora o tick de 15s
	// faria isso sem parar.
	FinalizeRetry time.Duration
	// FinalizeTimeout é folga para juntar e subir o maior arquivo. Sem prazo, um
	// upload pendurado prenderia o id em finalizing para sempre.
	FinalizeTimeout time.Duration
}

func (c Config) withDefaults() Config {
	def := func(v *time.Duration, d time.Duration) {
		if *v == 0 {
			*v = d
		}
	}
	def(&c.TickEvery, 15*time.Second)
	def(&c.TickTimeout, 10*time.Second)
	def(&c.PollEvery, 10*time.Second)
	def(&c.StallAfter, 60*time.Second)
	def(&c.RetryDelay, 5*time.Second)
	def(&c.MinGap, 30*time.Second)
	// PreRoll cobre o atraso do tick somado à conexão do ffmpeg. Sem ele, uma
	// gravação marcada para 20:00 só teria áudio a partir de ~20:00:20.
	def(&c.PreRoll, 20*time.Second)
	def(&c.PruneEvery, time.Hour)
	def(&c.FinalizeRetry, 2*time.Minute)
	def(&c.FinalizeTimeout, 30*time.Minute)
	return c
}

type liveRecorder struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Manager agenda, grava, finaliza e expira.
type Manager struct {
	cfg     Config
	repo    Repo
	media   Media
	objects ObjectStore
	mail    mailer.Mailer
	log     *zap.Logger
	now     func() time.Time

	mu         sync.Mutex
	live       map[uuid.UUID]*liveRecorder
	finalizing map[uuid.UUID]struct{}
	// finalizeFailures conta as falhas seguidas de cada gravação. Fica em memória,
	// então o aviso sai uma vez por processo: reinício zera a contagem e pode repetir
	// o e-mail.
	finalizeFailures map[uuid.UUID]int
	// draining barra gravador e e-mail novos depois que WaitRecorders começou.
	draining bool

	// recorders e notifications contam gravadores vivos e e-mails em andamento,
	// para o desligamento esperar por eles.
	recorders     sync.WaitGroup
	notifications sync.WaitGroup
}

// NewManager monta o Manager.
func NewManager(cfg Config, repo Repo, media Media, objects ObjectStore, mail mailer.Mailer, log *zap.Logger) *Manager {
	return &Manager{
		cfg:              cfg.withDefaults(),
		repo:             repo,
		media:            media,
		objects:          objects,
		mail:             mail,
		log:              log,
		now:              time.Now,
		live:             map[uuid.UUID]*liveRecorder{},
		finalizing:       map[uuid.UUID]struct{}{},
		finalizeFailures: map[uuid.UUID]int{},
	}
}

func (m *Manager) dirFor(id uuid.UUID) string {
	return filepath.Join(m.cfg.Root, id.String())
}

// Run bloqueia até ctx acabar.
//
// A retenção tem goroutine própria: apagar objetos num bucket lento não pode atrasar
// o início de uma gravação. Por ser um laço só, ela nunca se sobrepõe a si mesma.
func (m *Manager) Run(ctx context.Context) {
	pruneDone := make(chan struct{})
	go func() {
		defer close(pruneDone)
		m.pruneLoop(ctx)
	}()
	defer func() { <-pruneDone }()

	t := time.NewTicker(m.cfg.TickEvery)
	defer t.Stop()
	m.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.tick(ctx)
		}
	}
}

func (m *Manager) pruneLoop(ctx context.Context) {
	t := time.NewTicker(m.cfg.PruneEvery)
	defer t.Stop()
	m.prune(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.prune(ctx)
		}
	}
}

// tick não guarda estado próprio: tudo vem do banco, e é isso que faz o reinício
// da API se resolver sozinho, sem caminho especial de recuperação.
//
// Cada etapa registra o próprio erro e a seguinte roda mesmo assim: banco instável
// não pode impedir a finalização de quem já gravou.
func (m *Manager) tick(ctx context.Context) {
	now := m.now()
	m.tickStep(ctx, func(stepCtx context.Context) { m.failMissed(stepCtx, now) })
	m.tickStep(ctx, func(stepCtx context.Context) { m.claimDue(stepCtx, now) })
	m.tickStep(ctx, func(stepCtx context.Context) { m.resumeRecording(ctx, stepCtx, now) })
	m.tickStep(ctx, func(stepCtx context.Context) { m.finalizeProcessing(ctx, stepCtx) })
}

// tickStep dá prazo próprio a uma etapa, para o estouro de uma não consumir o da
// seguinte. O prazo é só das consultas: gravador e finalização nascem do ctx do Run,
// senão morreriam assim que a etapa acabasse.
func (m *Manager) tickStep(ctx context.Context, step func(stepCtx context.Context)) {
	if ctx.Err() != nil {
		return
	}
	stepCtx, cancel := context.WithTimeout(ctx, m.cfg.TickTimeout)
	defer cancel()
	step(stepCtx)
}

// failMissed avisa também quando há erro: as linhas devolvidas junto com ele já
// viraram `falhou`, e o próximo tick não as encontra mais.
func (m *Manager) failMissed(ctx context.Context, now time.Time) {
	missed, err := m.repo.FailMissed(ctx, now, "a plataforma ficou fora do ar durante toda a janela")
	if err != nil {
		m.log.Warn("recording: janelas perdidas", zap.Error(err))
	}
	for _, rec := range missed {
		m.notifyMissed(rec)
	}
}

func (m *Manager) claimDue(ctx context.Context, now time.Time) {
	if _, err := m.repo.ClaimDue(ctx, now.Add(m.cfg.PreRoll), now); err != nil {
		m.log.Warn("recording: iniciar gravações", zap.Error(err))
	}
}

func (m *Manager) resumeRecording(runCtx, stepCtx context.Context, now time.Time) {
	recording, err := m.repo.ListByStatus(stepCtx, StatusGravando)
	if err != nil {
		m.log.Warn("recording: listar gravando", zap.Error(err))
	}
	for _, rec := range recording {
		if rec.EndsAt.After(now) {
			m.startRecorder(runCtx, rec)
			continue
		}
		// Janela acabou com a API fora. Se há gravador vivo, ele mesmo fecha.
		m.mu.Lock()
		_, alive := m.live[rec.ID]
		m.mu.Unlock()
		if !alive {
			if err := m.repo.MarkProcessing(stepCtx, rec.ID); err != nil {
				m.log.Warn("recording: marcar processando", zap.Stringer("id", rec.ID), zap.Error(err))
			}
		}
	}
}

func (m *Manager) finalizeProcessing(runCtx, stepCtx context.Context) {
	processing, err := m.repo.ListByStatus(stepCtx, StatusProcessando)
	if err != nil {
		m.log.Warn("recording: listar processando", zap.Error(err))
	}
	for _, rec := range processing {
		m.startFinalize(runCtx, rec)
	}
}

// prune apaga o objeto primeiro e só então marca a linha, como em
// evidence/prune.go: na ordem inversa, uma falha deixaria objeto que nunca mais
// seria encontrado.
func (m *Manager) prune(ctx context.Context) {
	expired, err := m.repo.Expired(ctx, m.now())
	if err != nil {
		m.log.Warn("recording: listar expiradas", zap.Error(err))
		return
	}
	for _, rec := range expired {
		if ctx.Err() != nil {
			return
		}
		if err := m.objects.Delete(ctx, *rec.ObjectKey); err != nil {
			m.log.Warn("recording: apagar áudio", zap.Stringer("id", rec.ID), zap.Error(err))
			continue
		}
		if err := m.repo.MarkExpired(ctx, rec.ID); err != nil {
			m.log.Warn("recording: marcar expirada", zap.Stringer("id", rec.ID), zap.Error(err))
		}
	}
}
