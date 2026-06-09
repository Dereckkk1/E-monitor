// Package mailer é um cliente SMTP minimalista e reusável. Envia emails
// multipart/alternative (texto + HTML) via STARTTLS (Google Workspace na porta
// 587). Quando desabilitado ou sem credenciais, vira um noop que só loga —
// usado em dev local e quando NOTIFICATIONS_ENABLED=false.
package mailer

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"net/smtp"
	"strconv"

	"go.uber.org/zap"
)

// Mailer envia um email para um ou mais destinatários.
type Mailer interface {
	Send(ctx context.Context, to []string, subject, htmlBody, textBody string) error
}

// Config para construir o Mailer.
type Config struct {
	Enabled bool
	Host    string
	Port    int
	User    string
	Pass    string
	From    string // header From completo, ex.: "E-monitor <no-reply@e-monitor.online>"
}

// New retorna um Mailer SMTP quando habilitado e com credenciais; caso
// contrário um noop.
func New(cfg Config, log *zap.Logger) Mailer {
	if log == nil {
		log = zap.NewNop()
	}
	if !cfg.Enabled || cfg.User == "" || cfg.Pass == "" {
		log.Info("mailer: rodando em modo noop (desabilitado ou sem credenciais)",
			zap.Bool("enabled", cfg.Enabled), zap.Bool("has_user", cfg.User != ""))
		return &noopMailer{log: log}
	}
	from := cfg.From
	if from == "" {
		from = cfg.User
	}
	return &smtpMailer{
		addr: cfg.Host + ":" + strconv.Itoa(cfg.Port),
		auth: smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host),
		from: from,
		log:  log,
	}
}

type smtpMailer struct {
	addr string
	auth smtp.Auth
	from string
	log  *zap.Logger
}

func (m *smtpMailer) Send(ctx context.Context, to []string, subject, htmlBody, textBody string) error {
	if len(to) == 0 {
		return nil
	}
	msg := buildMessage(m.from, to[0], subject, htmlBody, textBody)
	// smtp.SendMail negocia STARTTLS automaticamente quando o servidor anuncia
	// suporte (Gmail/Workspace na 587). O envelope-from é derivado do header.
	if err := smtp.SendMail(m.addr, m.auth, fromAddress(m.from), to, msg); err != nil {
		return fmt.Errorf("mailer: SendMail: %w", err)
	}
	return nil
}

// fromAddress extrai só o endereço de um header "Nome <addr>".
func fromAddress(header string) string {
	if i := bytes.IndexByte([]byte(header), '<'); i >= 0 {
		if j := bytes.IndexByte([]byte(header), '>'); j > i {
			return header[i+1 : j]
		}
	}
	return header
}

// buildMessage monta a mensagem RFC 5322 multipart/alternative.
func buildMessage(from, to, subject, htmlBody, textBody string) []byte {
	var b bytes.Buffer
	boundary := "rc-boundary-9f2a"
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundary)

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(textBody)
	b.WriteString("\r\n\r\n")

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(htmlBody)
	b.WriteString("\r\n\r\n")

	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

type noopMailer struct{ log *zap.Logger }

func (m *noopMailer) Send(ctx context.Context, to []string, subject, htmlBody, textBody string) error {
	m.log.Info("mailer noop: email não enviado (modo dev)",
		zap.Strings("to", to), zap.String("subject", subject))
	return nil
}
