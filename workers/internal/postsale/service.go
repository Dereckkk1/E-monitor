package postsale

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/catalog"
	"radiocheck/internal/mailer"
)

// ObjectStore é o subconjunto do storage.Client que o pós-venda usa. Interface
// (e não o tipo concreto) pra que o teste consiga exercitar o publish inteiro
// sem subir MinIO.
type ObjectStore interface {
	Put(ctx context.Context, key string, body io.Reader, contentType string) error
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, time.Time, error)
}

// Service costura repo, insights, storage e mailer.
//
// Mailer PRÓPRIO (não o do scheduler de alertas): pós-venda é transacional —
// dispara na ação do admin e não pode depender de NOTIFICATIONS_ENABLED, que
// liga/desliga o job das 8h. Mesma decisão do welcome.
type Service struct {
	repo        *Repo
	insights    *catalog.Insights
	detections  *catalog.Detections
	storage     ObjectStore
	mail        mailer.Mailer
	mailEnabled bool
	baseURL     string
	footer      Footer
	log         *zap.Logger

	// pending guarda as capturas do browser entre o upload e o publish.
	// Em memória de propósito: são bytes efêmeros de um wizard aberto, e
	// persistir PNG intermediário no S3 deixaria lixo toda vez que o admin
	// desistir. O custo é que reiniciar a API no meio do wizard obriga a
	// refazer o passo 4 — aceitável, o publish inteiro leva segundos.
	mu      sync.Mutex
	pending map[pendingKey]uploadedAssets

	// Injetáveis pra teste determinístico.
	nowFn   func() time.Time
	todayFn func() time.Time
}

type pendingKey struct{ report, campaign uuid.UUID }

// uploadedAssets são os PNGs capturados no browser do admin.
type uploadedAssets struct {
	MapPNG      []byte
	InsightsPNG []byte
}

type Config struct {
	Repo        *Repo
	Insights    *catalog.Insights
	Detections  *catalog.Detections
	Storage     ObjectStore
	Mailer      mailer.Mailer
	MailEnabled bool
	BaseURL     string // base pública do frontend, sem barra final
	Footer      Footer
	Log         *zap.Logger
}

func New(cfg Config) *Service {
	log := cfg.Log
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{
		repo:        cfg.Repo,
		insights:    cfg.Insights,
		detections:  cfg.Detections,
		storage:     cfg.Storage,
		mail:        cfg.Mailer,
		mailEnabled: cfg.MailEnabled,
		baseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		footer:      cfg.Footer,
		log:         log,
		// Mapa inicializado no construtor: mapa nil dá panic em runtime na
		// primeira escrita (regra 6.5 do CLAUDE.md).
		pending: map[pendingKey]uploadedAssets{},
		nowFn:   time.Now,
		todayFn: todaySaoPaulo,
	}
}

func (s *Service) Repo() *Repo      { return s.repo }
func (s *Service) now() time.Time   { return s.nowFn() }
func (s *Service) today() time.Time { return s.todayFn() }

// Link monta a URL pública do pós-venda.
func (s *Service) Link(token string) string {
	return s.baseURL + "/pos-venda/" + url.PathEscape(token)
}

// SetAssets guarda a captura do browser até o publish montar o zip.
func (s *Service) SetAssets(_ context.Context, reportID, campaignID uuid.UUID, up uploadedAssets) error {
	if len(up.MapPNG) == 0 || len(up.InsightsPNG) == 0 {
		return fmt.Errorf("postsale: captura incompleta (mapa e indicadores são obrigatórios)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[pendingKey{reportID, campaignID}] = up
	return nil
}

func (s *Service) takePending(reportID, campaignID uuid.UUID) (uploadedAssets, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	up, ok := s.pending[pendingKey{reportID, campaignID}]
	return up, ok
}

func (s *Service) clearPending(reportID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.pending {
		if k.report == reportID {
			delete(s.pending, k)
		}
	}
}

// Resolve valida o token, registra a abertura e devolve o JSON congelado cru.
func (s *Service) Resolve(ctx context.Context, token string) ([]byte, error) {
	res, err := s.repo.ResolveToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if err := s.repo.TouchOpen(ctx, res.RecipientID); err != nil {
		// Telemetria não é o produto: falha aqui não pode negar a página.
		s.log.Warn("postsale: abertura não registrada", zap.Error(err))
	}
	return res.Payload, nil
}

// BundleURL revalida o token ANTES de presignar. A chave S3 nunca sai daqui —
// o payload público não a carrega.
func (s *Service) BundleURL(ctx context.Context, token string, campaignID uuid.UUID, ttl time.Duration) (string, error) {
	res, err := s.repo.ResolveToken(ctx, token)
	if err != nil {
		return "", err
	}
	key, err := s.repo.BundleKey(ctx, res.ReportID, campaignID)
	if err != nil {
		return "", err
	}
	u, _, err := s.storage.PresignGet(ctx, key, ttl)
	if err != nil {
		return "", fmt.Errorf("postsale: presign bundle: %w", err)
	}
	return u, nil
}

// NewToken gera o token opaco da URL: 32 bytes de aleatoriedade cripto-segura
// em base64url sem padding (43 chars, seguro em path de URL).
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("postsale: token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// todaySaoPaulo devolve "hoje" no fuso do Brasil, date-only — é o que o cálculo
// consolidado do Insights espera em InsightsParams.Today.
func todaySaoPaulo() time.Time {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		loc = time.FixedZone("BRT", -3*3600)
	}
	n := time.Now().In(loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// firstName pega o primeiro nome pra saudação. "Ana Maria Souza" → "Ana".
func firstName(full string) string {
	f := strings.Fields(strings.TrimSpace(full))
	if len(f) == 0 {
		return ""
	}
	return f[0]
}
