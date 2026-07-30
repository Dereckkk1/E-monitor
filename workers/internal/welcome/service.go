package welcome

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/mailer"
)

// ErrDisabled é devolvido quando alguém pede envio de boas-vindas mas a
// feature não está configurada (sem WELCOME_ENC_KEY). Vira 503 na API, não
// 500: é estado de configuração conhecido, não bug.
var ErrDisabled = errors.New("welcome: feature desabilitada (sem WELCOME_ENC_KEY)")

// Service costura cifra, repositório e mailer.
//
// O mailer aqui é PRÓPRIO, e não o do job de alertas diários: boas-vindas é
// email transacional (dispara na ação do admin) e não pode depender de
// NOTIFICATIONS_ENABLED, que liga/desliga o scheduler das 8h. Sem credenciais
// SMTP o mailer.New devolve noop e o convite fica com email_status='disabled'
// — o link continua válido e o admin manda por fora.
type Service struct {
	repo        *Repo
	cipher      *Cipher
	mail        mailer.Mailer
	mailEnabled bool
	baseURL     string
	log         *zap.Logger
}

// Config pro New.
type Config struct {
	Repo   *Repo
	Cipher *Cipher // nil = feature desabilitada
	Mailer mailer.Mailer
	// MailEnabled distingue "mailer real" de "noop de dev". O mailer.Mailer é
	// uma interface e o noop implementa Send sem erro, então sem esta flag o
	// serviço marcaria 'sent' num email que nunca saiu — exatamente o tipo de
	// falha silenciosa que a regra 4.5 do CLAUDE.md manda evitar.
	MailEnabled bool
	BaseURL     string // base pública do frontend, sem barra final
	Log         *zap.Logger
}

func New(cfg Config) *Service {
	log := cfg.Log
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{
		repo:        cfg.Repo,
		cipher:      cfg.Cipher,
		mail:        cfg.Mailer,
		mailEnabled: cfg.MailEnabled,
		baseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		log:         log,
	}
}

// Enabled diz se dá pra emitir convite. Falso quando falta a chave de cifra.
func (s *Service) Enabled() bool { return s != nil && s.cipher != nil }

// SendResult é o que o handler de criação devolve pro frontend, pra ele poder
// mostrar o link (e o admin copiar) mesmo quando o SMTP não está configurado.
type SendResult struct {
	InviteID    uuid.UUID `json:"invite_id"`
	Link        string    `json:"link"`
	EmailStatus string    `json:"email_status"` // sent | failed | disabled
	EmailError  string    `json:"email_error,omitempty"`
}

// SendInput agrupa o que o serviço precisa pra emitir e enviar o convite.
type SendInput struct {
	UserID     uuid.UUID
	Name       string
	Email      string
	Password   string // texto claro, só em memória — cifrado antes de persistir
	Role       string // vocabulário do banco
	ClientName string
	CreatedBy  *uuid.UUID
}

// Issue cria o convite e tenta enviar o email.
//
// Falha de SMTP NÃO derruba a operação: o convite já está gravado e o link é
// válido: devolvemos email_status='failed' e o admin manda o link por fora.
// Perder o usuário recém-criado porque o Gmail recusou a conexão seria pior.
func (s *Service) Issue(ctx context.Context, in SendInput) (*SendResult, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	enc, err := s.cipher.Encrypt(in.Password)
	if err != nil {
		return nil, err
	}
	token, err := NewToken()
	if err != nil {
		return nil, err
	}
	inv, err := s.repo.Create(ctx, CreateInput{
		UserID:      in.UserID,
		Token:       token,
		PasswordEnc: enc,
		CreatedBy:   in.CreatedBy,
	})
	if err != nil {
		return nil, fmt.Errorf("welcome: criar convite: %w", err)
	}

	link := s.Link(token)
	res := &SendResult{InviteID: inv.ID, Link: link}

	if !s.mailEnabled || s.mail == nil {
		// Dev local ou VM sem SMTP_USER/SMTP_PASS. O convite vale: a UI mostra
		// o link pro admin copiar e mandar por WhatsApp, que é o fluxo atual.
		res.EmailStatus = "disabled"
		_ = s.repo.MarkEmail(ctx, inv.ID, "disabled", "")
		return res, nil
	}

	subject, html, text := renderWelcome(welcomeData{
		Name:       firstName(in.Name),
		FullName:   in.Name,
		Email:      in.Email,
		ClientName: in.ClientName,
		Link:       link,
		BaseURL:    s.baseURL,
		IsClient:   in.Role == "viewer",
	})
	if err := s.mail.Send(ctx, []string{in.Email}, subject, html, text); err != nil {
		s.log.Error("welcome: falha ao enviar email de boas-vindas",
			zap.String("to", in.Email), zap.Error(err))
		res.EmailStatus = "failed"
		res.EmailError = err.Error()
		_ = s.repo.MarkEmail(ctx, inv.ID, "failed", err.Error())
		return res, nil
	}
	res.EmailStatus = "sent"
	if err := s.repo.MarkEmail(ctx, inv.ID, "sent", ""); err != nil {
		s.log.Warn("welcome: email enviado mas status não gravado", zap.Error(err))
	}
	s.log.Info("welcome: convite enviado",
		zap.String("to", in.Email), zap.String("invite_id", inv.ID.String()))
	return res, nil
}

// Link monta a URL pública do convite.
func (s *Service) Link(token string) string {
	return s.baseURL + "/boasvindas/" + url.PathEscape(token)
}

// Resolve é o que o endpoint público chama: carrega, decifra e registra a
// abertura. Token inválido/revogado/de usuário excluído vira ErrNotFound.
func (s *Service) Resolve(ctx context.Context, token string) (*Resolved, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	row, err := s.repo.Resolve(ctx, token)
	if err != nil {
		return nil, err
	}
	password, err := s.cipher.Decrypt(row.PasswordEnc)
	if err != nil {
		// Chave trocada ou blob corrompido. Não é 404 semanticamente, mas para
		// o visitante é indistinguível — e vazar "existe mas não abre" não
		// ajuda ninguém. Loga alto porque é sintoma de rotação de chave.
		s.log.Error("welcome: convite não decifra (chave trocada?)",
			zap.String("invite_id", row.InviteID.String()), zap.Error(err))
		return nil, ErrNotFound
	}
	if err := s.repo.TouchOpen(ctx, row.InviteID); err != nil {
		s.log.Warn("welcome: falha ao registrar abertura", zap.Error(err))
	}
	return &Resolved{
		Name:          row.Name,
		Email:         row.Email,
		Password:      password,
		Role:          apiRole(row.Role),
		ClientName:    row.ClientName,
		ClientLogoURL: row.ClientLogoURL,
	}, nil
}

// Repo expõe o repositório pros handlers que precisam listar/revogar.
func (s *Service) Repo() *Repo { return s.repo }

// firstName pega o primeiro nome pra saudação. "Ana Maria Souza" → "Ana".
// Nome vazio devolve string vazia; o template trata.
func firstName(full string) string {
	f := strings.Fields(strings.TrimSpace(full))
	if len(f) == 0 {
		return ""
	}
	return f[0]
}
