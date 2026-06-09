package mailer

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestNew_NoopWhenDisabled(t *testing.T) {
	m := New(Config{Enabled: false}, zap.NewNop())
	if _, ok := m.(*noopMailer); !ok {
		t.Fatalf("disabled config should yield noopMailer, got %T", m)
	}
	if err := m.Send(context.Background(), []string{"a@b.com"}, "s", "<p>h</p>", "h"); err != nil {
		t.Errorf("noop Send should never error: %v", err)
	}
}

func TestNew_NoopWhenNoCreds(t *testing.T) {
	m := New(Config{Enabled: true, Host: "smtp.gmail.com", Port: 587, User: "", Pass: ""}, zap.NewNop())
	if _, ok := m.(*noopMailer); !ok {
		t.Fatalf("missing creds should yield noopMailer, got %T", m)
	}
}

func TestBuildMessage_MultipartAlternative(t *testing.T) {
	msg := buildMessage("E-monitor <no-reply@e-monitor.online>", "dest@x.com", "Assunto Olá", "<h1>Oi</h1>", "Oi")
	s := string(msg)
	for _, want := range []string{
		"From: E-monitor <no-reply@e-monitor.online>",
		"To: dest@x.com",
		"MIME-Version: 1.0",
		"multipart/alternative",
		"text/plain; charset=\"utf-8\"",
		"text/html; charset=\"utf-8\"",
		"<h1>Oi</h1>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("message missing %q\n---\n%s", want, s)
		}
	}
	// Assunto deve ser MIME-encoded (acento) — não aparecer cru.
	if strings.Contains(s, "Subject: Assunto Olá") {
		t.Error("subject com acento deveria estar MIME-encoded (=?utf-8?...)")
	}
}
