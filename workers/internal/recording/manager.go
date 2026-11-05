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
	Root       string // <SEGMENTS_PATH>/_recordings
	BaseURL    string // link do e-mail
	TickEvery  time.Duration
	PollEvery  time.Duration // medição de bytes do watchdog
	StallAfter time.Duration // sem bytes novos por este tempo = caiu
	RetryDelay time.Duration
	MinGap     time.Duration
	PreRoll    time.Duration
	PruneEvery time.Duration
	// FinalizeRetry espaça as tentativas de finalização: cada uma reescreve o
	// arquivo inteiro em disco, até 330 MB, e com o bucket fora o tick de 15s
	// faria isso sem parar.
	FinalizeRetry time.Duration
}

func (c Config) withDefaults() Config {
	def := func(v *time.Duration, d time.Duration) {
		if *v == 0 {
			*v = d
		}
	}
	def(&c.TickEvery, 15*time.Second)
	def(&c.PollEvery, 10*time.Second)
	def(&c.StallAfter, 60*time.Second)
	def(&c.RetryDelay, 5*time.Second)
	def(&c.MinGap, 30*time.Second)
	// PreRoll cobre o atraso do tick somado à conexão do ffmpeg. Sem ele, uma
	// gravação marcada para 20:00 só teria áudio a partir de ~20:00:20.
	def(&c.PreRoll, 20*time.Second)
	def(&c.PruneEvery, time.Hour)
	def(&c.FinalizeRetry, 2*time.Minute)
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
	// finalizeFailures conta as falhas seguidas de cada gravação. Fica em memória:
	// reiniciar a API zera a contagem, e o pior caso é um aviso atrasado.
	finalizeFailures map[uuid.UUID]int
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
