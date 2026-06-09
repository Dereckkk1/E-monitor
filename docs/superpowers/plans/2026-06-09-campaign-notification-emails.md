# Emails diários de alerta de campanha — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Disparar 3 emails diários (campanhas iniciando sem material / iniciando / terminando) para usuários admin+operator, em dia útil, dentro da janela `<3 dias corridos OU ≤2 dias úteis`.

**Architecture:** Um job em goroutine (ticker 5 min) que, em dia útil a partir das 08:00 BRT e uma vez por dia por tipo (dedup via `notification_log` + advisory lock), seleciona campanhas, renderiza HTML/texto e envia via SMTP (Google Workspace). Abordagem A do spec (envio direto + retry curto, sem outbox). Pacotes novos: `calendar` (dias úteis, puro), `mailer` (SMTP reusável), `campaignalerts` (queries + render + scheduler/service).

**Tech Stack:** Go 1.26 (módulo `radiocheck`), pgx/v5, zap, prometheus, `net/smtp`, `html/template`/`text/template`, `//go:embed`. Frontend: nenhum (HTML do email é server-side). Skill `/impeccable` para o craft visual dos templates.

**Spec:** [docs/superpowers/specs/2026-06-09-campaign-notification-emails-design.md](../specs/2026-06-09-campaign-notification-emails-design.md)

---

## File Structure

| Arquivo | Responsabilidade |
|---|---|
| `workers/internal/calendar/businessdays.go` | Utilitário puro de dias úteis / janela de alerta |
| `workers/internal/calendar/businessdays_test.go` | Testes da janela (cenários do usuário) |
| `migrations/0036_notification_log.up.sql` / `.down.sql` | Tabela de dedup diário |
| `workers/internal/config/config.go` (modificar) | Env vars SMTP + notificações |
| `workers/internal/mailer/mailer.go` | Interface `Mailer` + impl SMTP + noop |
| `workers/internal/mailer/mailer_test.go` | Teste do noop + montagem MIME |
| `workers/internal/metrics/metrics.go` (modificar) | Counters de notificações |
| `workers/internal/campaignalerts/queries.go` | Seleção das campanhas (3 tipos) |
| `workers/internal/campaignalerts/queries_test.go` | Integração das 3 seleções |
| `workers/internal/campaignalerts/logstore.go` | `AlreadySent` / `Record` no notification_log |
| `workers/internal/campaignalerts/recipients.go` (em `users`) | método `ActiveInternal` |
| `workers/internal/campaignalerts/render.go` | Render dos 3 emails (HTML+texto) |
| `workers/internal/campaignalerts/render_test.go` | Golden test do render |
| `workers/internal/campaignalerts/templates/*.html` / `*.txt` | Templates (visual via /impeccable) |
| `workers/internal/campaignalerts/service.go` | Orquestra build+envio por tipo |
| `workers/internal/campaignalerts/scheduler.go` | Ticker + janela diária + dedup + advisory lock |
| `workers/internal/campaignalerts/scheduler_test.go` | Teste de dedup/skip fim de semana |
| `workers/cmd/api/main.go` (modificar) | Wiring do job |
| `docs/features/campaign-notification-emails.md` | Doc operacional |

---

## Task 1: Pacote `calendar` — dias úteis e janela de alerta

**Files:**
- Create: `workers/internal/calendar/businessdays.go`
- Test: `workers/internal/calendar/businessdays_test.go`

- [ ] **Step 1: Write the failing test**

Cobre exatamente os cenários do usuário + bordas de ponte de fim de semana.

```go
package calendar

import (
	"testing"
	"time"
)

// d builds a midnight America/Sao_Paulo date.
func d(t *testing.T, s string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatalf("load loc: %v", err)
	}
	tm, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm
}

func TestInAlertWindow(t *testing.T) {
	// 2026-06-08 is a Monday. Calendar anchor:
	// Mon 06-08, Tue 06-09, Wed 06-10, Thu 06-11, Fri 06-12, Sat 06-13, Sun 06-14, Mon 06-15.
	cases := []struct {
		name         string
		today, target string
		want          bool
	}{
		{"cenario1 sexta->segunda", "2026-06-12", "2026-06-15", true},   // cal 3 (não<3), úteis 1 (≤2)
		{"cenario1 sabado nao envia (irrelevante: job nem roda) mas window sab->seg", "2026-06-13", "2026-06-15", true}, // cal 2 <3 — janela ok; o skip é no scheduler, não aqui
		{"cenario2 quinta->sabado", "2026-06-11", "2026-06-13", true},   // cal 2 <3
		{"cenario3 segunda->terca", "2026-06-08", "2026-06-09", true},   // cal 1 <3
		{"ponte sexta->terca (2 uteis)", "2026-06-12", "2026-06-16", true}, // cal 4, úteis 2 (≤2)
		{"sexta->quarta (3 uteis) fora", "2026-06-12", "2026-06-17", false}, // cal 5, úteis 3 (>2)
		{"alvo hoje", "2026-06-09", "2026-06-09", true},                 // cal 0 <3
		{"alvo passado fora", "2026-06-10", "2026-06-09", false},        // target<today
		{"longe fora", "2026-06-08", "2026-06-30", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := InAlertWindow(d(t, c.today), d(t, c.target))
			if got != c.want {
				t.Errorf("InAlertWindow(%s,%s) = %v, want %v", c.today, c.target, got, c.want)
			}
		})
	}
}

func TestIsBusinessDay(t *testing.T) {
	if IsBusinessDay(d(t, "2026-06-13")) { // sábado
		t.Error("sábado deveria ser não-útil")
	}
	if IsBusinessDay(d(t, "2026-06-14")) { // domingo
		t.Error("domingo deveria ser não-útil")
	}
	if !IsBusinessDay(d(t, "2026-06-12")) { // sexta
		t.Error("sexta deveria ser útil")
	}
}

func TestBusinessDaysUntil(t *testing.T) {
	if got := BusinessDaysUntil(d(t, "2026-06-12"), d(t, "2026-06-15")); got != 1 { // sex->seg
		t.Errorf("sex->seg = %d, want 1", got)
	}
	if got := BusinessDaysUntil(d(t, "2026-06-12"), d(t, "2026-06-16")); got != 2 { // sex->ter
		t.Errorf("sex->ter = %d, want 2", got)
	}
	if got := BusinessDaysUntil(d(t, "2026-06-10"), d(t, "2026-06-09")); got != 0 { // alvo passado
		t.Errorf("passado = %d, want 0", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd workers && go test ./internal/calendar/...`
Expected: FAIL — `undefined: InAlertWindow` (etc.)

- [ ] **Step 3: Write minimal implementation**

```go
// Package calendar implementa cálculo de dias úteis (seg–sex, SEM feriados) e
// a janela de alerta de campanha compartilhada pelos 3 disparos de email.
// Toda comparação é por data (meia-noite), em America/Sao_Paulo.
package calendar

import "time"

// BR é o fuso de referência do negócio. Datas vindas do banco (DATE) são
// comparadas neste fuso.
var BR = mustBR()

func mustBR() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		// America/Sao_Paulo está na tzdata padrão; se faltar, UTC é um fallback
		// seguro (o pior caso desloca a fronteira de dia em horas, não quebra).
		return time.UTC
	}
	return loc
}

// DateOnly normaliza t para meia-noite no fuso BR, descartando hora/min/seg.
func DateOnly(t time.Time) time.Time {
	t = t.In(BR)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, BR)
}

// IsBusinessDay retorna true para segunda a sexta.
func IsBusinessDay(t time.Time) bool {
	wd := t.In(BR).Weekday()
	return wd != time.Saturday && wd != time.Sunday
}

// CalendarDaysUntil retorna (target - today) em dias de calendário.
func CalendarDaysUntil(today, target time.Time) int {
	a := DateOnly(today)
	b := DateOnly(target)
	return int(b.Sub(a).Hours() / 24)
}

// BusinessDaysUntil conta dias úteis no intervalo semiaberto (today, target]:
// conta target se for útil, não conta today. Retorna 0 quando target <= today.
func BusinessDaysUntil(today, target time.Time) int {
	a := DateOnly(today)
	b := DateOnly(target)
	if !b.After(a) {
		return 0
	}
	count := 0
	for cur := a.AddDate(0, 0, 1); !cur.After(b); cur = cur.AddDate(0, 0, 1) {
		if IsBusinessDay(cur) {
			count++
		}
	}
	return count
}

// InAlertWindow é o predicado compartilhado: dispara alerta quando faltam menos
// de 3 dias corridos OU até 2 dias úteis para a data-alvo. Datas no passado
// (target < today) nunca entram.
func InAlertWindow(today, target time.Time) bool {
	if CalendarDaysUntil(today, target) < 0 {
		return false
	}
	return CalendarDaysUntil(today, target) < 3 || BusinessDaysUntil(today, target) <= 2
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd workers && go test ./internal/calendar/...`
Expected: PASS (ok radiocheck/internal/calendar)

- [ ] **Step 5: Commit**

```bash
git add workers/internal/calendar/
git commit -m "feat(calendar): util de dias úteis e janela de alerta de campanha"
```

---

## Task 2: Migration `notification_log`

**Files:**
- Create: `migrations/0036_notification_log.up.sql`
- Create: `migrations/0036_notification_log.down.sql`

> Antes: ler [docs/operations/migrations.md](../../operations/migrations.md). Não montar `migrations/` em `docker-entrypoint-initdb.d` (regra 4.6 do CLAUDE.md).

- [ ] **Step 1: Write the up migration**

```sql
-- notification_log: garante UM envio por tipo de alerta por dia (dedup) e
-- registra o resultado de cada execução do job de emails de campanha.
-- A PK (notification_date, type) é a barreira de idempotência: o scheduler só
-- envia se não existir linha para (hoje, tipo). Reenvio diário acontece
-- naturalmente porque notification_date muda a cada dia.
CREATE TABLE IF NOT EXISTS notification_log (
    notification_date DATE        NOT NULL,
    type              TEXT        NOT NULL
        CHECK (type IN ('starting_no_material', 'starting', 'ending')),
    sent_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    recipient_count   INT         NOT NULL DEFAULT 0,
    campaign_count    INT         NOT NULL DEFAULT 0,
    status            TEXT        NOT NULL DEFAULT 'sent'
        CHECK (status IN ('sent', 'partial', 'failed', 'skipped_empty')),
    error             TEXT,
    PRIMARY KEY (notification_date, type)
);
```

- [ ] **Step 2: Write the down migration**

```sql
DROP TABLE IF EXISTS notification_log;
```

- [ ] **Step 3: Apply locally and verify**

Run (dev local):
```bash
cd workers && go run ./cmd/migrate up   # ou o runner usual do projeto (ver migrations.md)
psql "$TEST_DATABASE_URL" -c "\d notification_log"
```
Expected: tabela existe, PK `(notification_date, type)`.

> Se o runner de migration diferir, seguir o documentado em `docs/operations/migrations.md`. NÃO usar o serviço `migrate` + initdb.d simultaneamente (regra 4.6).

- [ ] **Step 4: Commit**

```bash
git add migrations/0036_notification_log.up.sql migrations/0036_notification_log.down.sql
git commit -m "feat(db): tabela notification_log para dedup diário de emails de campanha"
```

---

## Task 3: Config — env vars SMTP e notificações

**Files:**
- Modify: `workers/internal/config/config.go`

- [ ] **Step 1: Add fields to Config struct**

Adicionar ao `type Config struct` (depois de `APIPort string`):

```go
	APIPort      string

	// ── Notificações por email (emails diários de alerta de campanha) ──
	NotificationsEnabled bool   // NOTIFICATIONS_ENABLED ("true" liga o job)
	NotificationsBaseURL string // NOTIFICATIONS_BASE_URL (base dos links/CTA)
	NotificationsSendHour int    // NOTIFICATIONS_SEND_HOUR (hora local BRT de corte)
	SMTPHost string // SMTP_HOST
	SMTPPort int    // SMTP_PORT
	SMTPUser string // SMTP_USER
	SMTPPass string // SMTP_PASS
	MailFrom string // MAIL_FROM (default = SMTPUser)
```

- [ ] **Step 2: Load + defaults**

Adicionar `import "strconv"` ao bloco de imports. No corpo de `Load()`, antes de `return cfg, nil`, inserir:

```go
	cfg.NotificationsEnabled = os.Getenv("NOTIFICATIONS_ENABLED") == "true"
	cfg.NotificationsBaseURL = os.Getenv("NOTIFICATIONS_BASE_URL")
	if cfg.NotificationsBaseURL == "" {
		cfg.NotificationsBaseURL = "https://e-monitor.online"
	}
	cfg.NotificationsSendHour = 8
	if v := os.Getenv("NOTIFICATIONS_SEND_HOUR"); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h >= 0 && h <= 23 {
			cfg.NotificationsSendHour = h
		}
	}
	cfg.SMTPHost = os.Getenv("SMTP_HOST")
	if cfg.SMTPHost == "" {
		cfg.SMTPHost = "smtp.gmail.com"
	}
	cfg.SMTPPort = 587
	if v := os.Getenv("SMTP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			cfg.SMTPPort = p
		}
	}
	cfg.SMTPUser = os.Getenv("SMTP_USER")
	cfg.SMTPPass = os.Getenv("SMTP_PASS")
	cfg.MailFrom = os.Getenv("MAIL_FROM")
	if cfg.MailFrom == "" {
		cfg.MailFrom = cfg.SMTPUser
	}
```

Não adicionar nada ao mapa `required`: o job é opt-in via `NOTIFICATIONS_ENABLED` e degrada para noop sem credenciais.

- [ ] **Step 3: Verify build**

Run: `cd workers && go build ./internal/config/...`
Expected: sem erros.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/config/config.go
git commit -m "feat(config): env vars de SMTP e notificações de campanha"
```

---

## Task 4: Pacote `mailer` — SMTP + noop

**Files:**
- Create: `workers/internal/mailer/mailer.go`
- Test: `workers/internal/mailer/mailer_test.go`

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd workers && go test ./internal/mailer/...`
Expected: FAIL — `undefined: New`, `undefined: buildMessage`.

- [ ] **Step 3: Write minimal implementation**

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd workers && go test ./internal/mailer/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/mailer/
git commit -m "feat(mailer): cliente SMTP reusável com fallback noop"
```

---

## Task 5: Métricas de notificação

**Files:**
- Modify: `workers/internal/metrics/metrics.go`

- [ ] **Step 1: Add metric vars**

Antes do fechamento `)` do bloco `var (` (depois de `AuditAttempts, ...` declarations), adicionar:

```go
	// ── Notificações por email (emails diários de alerta de campanha) ──
	NotificationsSentTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_notifications_sent_total",
		Help: "Total de emails de alerta de campanha enviados, por tipo.",
	}, []string{"type"})

	NotificationsFailedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_notifications_failed_total",
		Help: "Total de falhas de envio de email de alerta, por tipo.",
	}, []string{"type"})

	NotificationsRecipients = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "radiocheck_notifications_recipients",
		Help: "Número de destinatários do último disparo, por tipo.",
	}, []string{"type"})
```

- [ ] **Step 2: Register in init()**

No `prometheus.MustRegister(` adicionar uma linha:

```go
		AuditAttempts, AuditScore, AuditCoverage, AuditDuration,
		NotificationsSentTotal, NotificationsFailedTotal, NotificationsRecipients,
	)
```

- [ ] **Step 3: Verify build**

Run: `cd workers && go build ./internal/metrics/...`
Expected: sem erros.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/metrics/metrics.go
git commit -m "feat(metrics): counters de notificações de campanha"
```

---

## Task 6: `users.ActiveInternal` — destinatários admin+operator

**Files:**
- Modify: `workers/internal/users/users.go`
- Test: `workers/internal/users/users_recipients_test.go`

- [ ] **Step 1: Write the failing test**

```go
package users

import "testing"

// Compila-guard: garante a assinatura. O teste de dados real depende de
// TEST_DATABASE_URL e roda no pacote catalog-style; aqui só fixamos a API.
func TestActiveInternal_Signature(t *testing.T) {
	var _ func() = func() {
		var r *Repo
		_, _ = r.ActiveInternal(nil) //nolint:errcheck,staticcheck
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd workers && go test ./internal/users/...`
Expected: FAIL — `r.ActiveInternal undefined`.

- [ ] **Step 3: Add the method**

Adicionar ao fim de `workers/internal/users/users.go`:

```go
// ActiveInternal retorna todos os usuários internos ativos (role 'admin' ou
// 'operator', não deletados, is_active=true). É o público-alvo dos emails de
// alerta de campanha — o conjunto que a UI chama de "Administrador". Sem
// paginação: o volume de internos é pequeno.
func (r *Repo) ActiveInternal(ctx context.Context) ([]User, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+userColumns+` FROM users
		 WHERE deleted_at IS NULL AND is_active = TRUE
		   AND role IN ('admin','operator')
		 ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd workers && go test ./internal/users/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/users/
git commit -m "feat(users): ActiveInternal lista admins+operators ativos"
```

---

## Task 7: `campaignalerts` — seleção de campanhas (queries)

**Files:**
- Create: `workers/internal/campaignalerts/queries.go`
- Test: `workers/internal/campaignalerts/queries_test.go`

- [ ] **Step 1: Write the failing test**

Usa `TEST_DATABASE_URL` (skip se ausente), padrão de `catalog`.

```go
package campaignalerts

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"radiocheck/internal/db"
)

func newTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url)
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return ctx, pool
}

func TestStartingNoMaterial(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := New(pool)

	cli := mustClient(t, ctx, pool, "Alerts Co")
	// Anchor: hoje = uma segunda fixa. Campanha programada que começa em 1 dia,
	// sem material → deve aparecer.
	today := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC) // Monday
	soon := mustCampaign(t, ctx, pool, cli, "programada", "2026-06-09", "2026-06-30")
	// Campanha programada com material → NÃO aparece no disparo 1.
	withMat := mustCampaign(t, ctx, pool, cli, "programada", "2026-06-09", "2026-06-30")
	mustMaterialOn(t, ctx, pool, cli, withMat)
	// Campanha longe → não aparece.
	mustCampaign(t, ctx, pool, cli, "programada", "2026-06-30", "2026-07-30")

	got, err := repo.StartingNoMaterial(ctx, today)
	if err != nil {
		t.Fatalf("StartingNoMaterial: %v", err)
	}
	if !containsID(got, soon) {
		t.Errorf("esperava a campanha sem material na janela")
	}
	if containsID(got, withMat) {
		t.Errorf("campanha COM material não deveria aparecer no disparo 1")
	}
}

func TestStarting_IncludesWithMaterial(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := New(pool)
	cli := mustClient(t, ctx, pool, "Alerts Co 2")
	today := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	withMat := mustCampaign(t, ctx, pool, cli, "programada", "2026-06-09", "2026-06-30")
	mustMaterialOn(t, ctx, pool, cli, withMat)

	got, err := repo.Starting(ctx, today)
	if err != nil {
		t.Fatalf("Starting: %v", err)
	}
	if !containsID(got, withMat) {
		t.Errorf("disparo 2 deve incluir campanha com material")
	}
}

func TestEnding_OnlyActive(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := New(pool)
	cli := mustClient(t, ctx, pool, "Alerts Co 3")
	today := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	ending := mustCampaign(t, ctx, pool, cli, "ativa", "2026-05-01", "2026-06-09")
	// programada terminando na janela NÃO entra (disparo 3 = só ativa).
	prog := mustCampaign(t, ctx, pool, cli, "programada", "2026-06-08", "2026-06-09")

	got, err := repo.Ending(ctx, today)
	if err != nil {
		t.Fatalf("Ending: %v", err)
	}
	if !containsID(got, ending) {
		t.Errorf("disparo 3 deve incluir campanha ativa terminando")
	}
	if containsID(got, prog) {
		t.Errorf("disparo 3 não deve incluir programada")
	}
}

// ── helpers de fixture ───────────────────────────────────────────────
func containsID(rows []CampaignAlert, id uuid.UUID) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

func mustClient(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert client: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaign_materials WHERE campaign_id IN (SELECT id FROM campaigns WHERE client_id=$1)`, id) //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM materials WHERE client_id=$1`, id)  //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id=$1`, id)  //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM clients WHERE id=$1`, id)           //nolint:errcheck
	})
	return id
}

func mustCampaign(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cli uuid.UUID, status, start, end string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		 VALUES ($1, 'C', $2, $3, $4) RETURNING id`, cli, start, end, status).Scan(&id); err != nil {
		t.Fatalf("insert campaign: %v", err)
	}
	return id
}

func mustMaterialOn(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cli, camp uuid.UUID) {
	t.Helper()
	var matID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO materials (client_id, title) VALUES ($1, 'M') RETURNING id`, cli).Scan(&matID); err != nil {
		t.Fatalf("insert material: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO campaign_materials (campaign_id, material_id) VALUES ($1, $2)`, camp, matID); err != nil {
		t.Fatalf("insert campaign_material: %v", err)
	}
}
```

> Se as colunas mínimas de `materials` exigirem mais que `(client_id, title)` (ex.: NOT NULL sem default), ajustar o `mustMaterialOn` conforme `migrations/0016_material_library.up.sql`. Verificar antes de rodar.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd workers && go test ./internal/campaignalerts/...`
Expected: FAIL — `undefined: New`, `undefined: CampaignAlert`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package campaignalerts seleciona campanhas elegíveis para os 3 emails diários
// de alerta, renderiza o conteúdo e dispara o envio. Ver
// docs/superpowers/specs/2026-06-09-campaign-notification-emails-design.md.
package campaignalerts

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/calendar"
)

// CampaignAlert é a projeção de uma campanha usada nos emails.
type CampaignAlert struct {
	ID            uuid.UUID
	Name          string
	ClientName    string
	StartDate     time.Time
	EndDate       time.Time
	StationCount  int
	MaterialCount int
}

// Repo executa as seleções de campanha.
type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// lookaheadDays é a faixa larga buscada no SQL; o refinamento por dias úteis é
// feito em Go via calendar.InAlertWindow. 4 cobre o pior caso (sexta→terça /
// quinta→segunda = 2 dias úteis = 4 dias corridos).
const lookaheadDays = 4

// candidates busca campanhas de um status cuja coluna de data (dateCol) cai na
// faixa [hoje, hoje+lookahead]. dateCol é injetado a partir de constantes
// internas — nunca de input externo.
func (r *Repo) candidates(ctx context.Context, status, dateCol string, today time.Time) ([]CampaignAlert, error) {
	t0 := calendar.DateOnly(today)
	t1 := t0.AddDate(0, 0, lookaheadDays)
	q := fmt.Sprintf(`
		SELECT c.id, c.name, COALESCE(cl.name, ''), c.start_date, c.end_date,
		       COALESCE(array_length(c.target_stations, 1), 0) AS station_count,
		       COALESCE((
		         SELECT COUNT(*)::int FROM campaign_materials cm WHERE cm.campaign_id = c.id
		       ), 0) AS material_count
		FROM campaigns c
		LEFT JOIN clients cl ON cl.id = c.client_id
		WHERE c.status = $1
		  AND c.%s BETWEEN $2 AND $3
		ORDER BY c.%s ASC, c.name ASC`, dateCol, dateCol)
	rows, err := r.pool.Query(ctx, q, status, t0, t1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CampaignAlert
	for rows.Next() {
		var a CampaignAlert
		if err := rows.Scan(&a.ID, &a.Name, &a.ClientName, &a.StartDate, &a.EndDate,
			&a.StationCount, &a.MaterialCount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// StartingNoMaterial: disparo 1. Campanhas programadas, na janela pelo
// start_date, com zero materiais.
func (r *Repo) StartingNoMaterial(ctx context.Context, today time.Time) ([]CampaignAlert, error) {
	cands, err := r.candidates(ctx, "programada", "start_date", today)
	if err != nil {
		return nil, err
	}
	out := make([]CampaignAlert, 0, len(cands))
	for _, c := range cands {
		if c.MaterialCount == 0 && calendar.InAlertWindow(today, c.StartDate) {
			out = append(out, c)
		}
	}
	return out, nil
}

// Starting: disparo 2. Todas as campanhas programadas na janela pelo start_date
// (com ou sem material).
func (r *Repo) Starting(ctx context.Context, today time.Time) ([]CampaignAlert, error) {
	cands, err := r.candidates(ctx, "programada", "start_date", today)
	if err != nil {
		return nil, err
	}
	out := make([]CampaignAlert, 0, len(cands))
	for _, c := range cands {
		if calendar.InAlertWindow(today, c.StartDate) {
			out = append(out, c)
		}
	}
	return out, nil
}

// Ending: disparo 3. Campanhas ativas na janela pelo end_date.
func (r *Repo) Ending(ctx context.Context, today time.Time) ([]CampaignAlert, error) {
	cands, err := r.candidates(ctx, "ativa", "end_date", today)
	if err != nil {
		return nil, err
	}
	out := make([]CampaignAlert, 0, len(cands))
	for _, c := range cands {
		if calendar.InAlertWindow(today, c.EndDate) {
			out = append(out, c)
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd workers && TEST_DATABASE_URL=$TEST_DATABASE_URL go test ./internal/campaignalerts/...`
Expected: PASS (ou SKIP se `TEST_DATABASE_URL` ausente — nesse caso rode com o banco de teste local).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/campaignalerts/queries.go workers/internal/campaignalerts/queries_test.go
git commit -m "feat(campaignalerts): seleção das campanhas dos 3 disparos"
```

---

## Task 8: `campaignalerts` — logstore (dedup)

**Files:**
- Create: `workers/internal/campaignalerts/logstore.go`
- Test: `workers/internal/campaignalerts/logstore_test.go`

- [ ] **Step 1: Write the failing test**

```go
package campaignalerts

import (
	"testing"
	"time"
)

func TestLogStore_DedupRoundTrip(t *testing.T) {
	ctx, pool := newTestDB(t)
	store := NewLogStore(pool)
	day := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	typ := "starting"
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM notification_log WHERE notification_date=$1 AND type=$2`, day, typ) //nolint:errcheck
	})

	sent, err := store.AlreadySent(ctx, day, typ)
	if err != nil {
		t.Fatalf("AlreadySent: %v", err)
	}
	if sent {
		t.Fatal("não deveria estar enviado ainda")
	}
	if err := store.Record(ctx, LogEntry{Date: day, Type: typ, RecipientCount: 3, CampaignCount: 2, Status: "sent"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	sent, err = store.AlreadySent(ctx, day, typ)
	if err != nil {
		t.Fatalf("AlreadySent#2: %v", err)
	}
	if !sent {
		t.Fatal("deveria estar marcado como enviado")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd workers && go test ./internal/campaignalerts/... -run LogStore`
Expected: FAIL — `undefined: NewLogStore`.

- [ ] **Step 3: Write minimal implementation**

```go
package campaignalerts

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LogEntry é uma linha do notification_log.
type LogEntry struct {
	Date           time.Time
	Type           string
	RecipientCount int
	CampaignCount  int
	Status         string // sent | partial | failed | skipped_empty
	Error          string
}

// LogStore lê/grava o registro de dedup diário.
type LogStore struct {
	pool *pgxpool.Pool
}

func NewLogStore(pool *pgxpool.Pool) *LogStore { return &LogStore{pool: pool} }

// AlreadySent indica se já existe registro para (date, type) — a barreira de
// dedup diário.
func (s *LogStore) AlreadySent(ctx context.Context, date time.Time, typ string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM notification_log WHERE notification_date = $1::date AND type = $2)`,
		date, typ).Scan(&exists)
	return exists, err
}

// Record grava o resultado do disparo. ON CONFLICT garante idempotência se dois
// caminhos correrem (o advisory lock já serializa, isto é cinto e suspensório).
func (s *LogStore) Record(ctx context.Context, e LogEntry) error {
	var errPtr *string
	if e.Error != "" {
		errPtr = &e.Error
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO notification_log
		    (notification_date, type, recipient_count, campaign_count, status, error)
		VALUES ($1::date, $2, $3, $4, $5, $6)
		ON CONFLICT (notification_date, type) DO NOTHING`,
		e.Date, e.Type, e.RecipientCount, e.CampaignCount, e.Status, errPtr)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd workers && go test ./internal/campaignalerts/... -run LogStore`
Expected: PASS (ou SKIP sem banco).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/campaignalerts/logstore.go workers/internal/campaignalerts/logstore_test.go
git commit -m "feat(campaignalerts): logstore de dedup diário"
```

---

## Task 9: `campaignalerts` — render dos emails (baseline funcional)

**Files:**
- Create: `workers/internal/campaignalerts/render.go`
- Create: `workers/internal/campaignalerts/templates/starting_no_material.html`
- Create: `workers/internal/campaignalerts/templates/starting.html`
- Create: `workers/internal/campaignalerts/templates/ending.html`
- Create: `workers/internal/campaignalerts/templates/_email.txt` (texto compartilhado)
- Test: `workers/internal/campaignalerts/render_test.go`

> Este é o baseline **funcional**. O polimento visual (layout inspirado nos refs do reallygoodemails) é feito na Task 13 via `/impeccable`, preservando o contrato de variáveis abaixo.

- [ ] **Step 1: Write the failing test**

```go
package campaignalerts

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func sampleAlerts() []CampaignAlert {
	return []CampaignAlert{
		{ID: uuid.New(), Name: "Promo Inverno", ClientName: "Tintas Renner",
			StartDate: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
			StationCount: 12, MaterialCount: 0},
	}
}

func TestRender_StartingNoMaterial(t *testing.T) {
	c, err := RenderStartingNoMaterial("Dereck", sampleAlerts(), "https://e-monitor.online")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if c.Subject == "" {
		t.Error("subject vazio")
	}
	for _, want := range []string{"Promo Inverno", "Tintas Renner", "15/06/2026", "Dereck",
		"https://e-monitor.online/campaigns"} {
		if !strings.Contains(c.HTML, want) {
			t.Errorf("HTML não contém %q", want)
		}
		if !strings.Contains(c.Text, want) && want != "https://e-monitor.online/campaigns" {
			t.Errorf("Text não contém %q", want)
		}
	}
	if !strings.Contains(c.HTML, "E-monitor%20logo.png") {
		t.Error("HTML não referencia o logo")
	}
}

func TestRender_EmptyIsHandledByCaller(t *testing.T) {
	// Render nunca recebe lista vazia (o service pula). Mas se receber, não
	// deve panicar.
	if _, err := RenderStarting("X", nil, "https://e-monitor.online"); err != nil {
		t.Fatalf("render vazio: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd workers && go test ./internal/campaignalerts/... -run Render`
Expected: FAIL — `undefined: RenderStartingNoMaterial`.

- [ ] **Step 3: Write the templates (baseline)**

`workers/internal/campaignalerts/templates/starting_no_material.html`:
```html
<!doctype html>
<html lang="pt-BR"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"></head>
<body style="margin:0;background:#f3f4f6;font-family:Arial,Helvetica,sans-serif;color:#111827;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f3f4f6;">
<tr><td align="center" style="padding:24px;">
  <table role="presentation" width="600" cellpadding="0" cellspacing="0" style="max-width:600px;background:#ffffff;border-radius:12px;overflow:hidden;">
    <tr><td style="padding:28px 32px;border-bottom:1px solid #e5e7eb;">
      <img src="{{.BaseURL}}/E-monitor%20logo.png" alt="E-monitor" height="28" style="display:block;">
    </td></tr>
    <tr><td style="padding:32px;">
      <h1 style="margin:0 0 8px;font-size:24px;line-height:1.2;">{{len .Campaigns}} campanha{{if ne (len .Campaigns) 1}}s{{end}} sem material</h1>
      <p style="margin:0 0 24px;font-size:15px;color:#374151;">Olá {{.Recipient}}, {{if eq (len .Campaigns) 1}}esta campanha começa em breve e ainda não tem material cadastrado{{else}}estas campanhas começam em breve e ainda não têm material cadastrado{{end}}. Cadastre os materiais antes do início.</p>
      {{range .Campaigns}}
      <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border:1px solid #e5e7eb;border-radius:10px;margin-bottom:12px;">
        <tr><td style="padding:16px 18px;">
          <div style="font-size:16px;font-weight:bold;">{{.Name}}</div>
          <div style="font-size:13px;color:#6b7280;margin-top:2px;">{{.ClientName}}</div>
          <div style="font-size:13px;color:#374151;margin-top:8px;">Início: <b>{{fmtDate .StartDate}}</b> · {{.StationCount}} emissora{{if ne .StationCount 1}}s{{end}}</div>
          <div style="display:inline-block;margin-top:8px;padding:3px 10px;border-radius:999px;background:#fef2f2;color:#b91c1c;font-size:12px;font-weight:bold;">0 materiais</div>
        </td></tr>
      </table>
      {{end}}
      <a href="{{.BaseURL}}/campaigns" style="display:inline-block;margin-top:12px;padding:12px 22px;background:#2563eb;color:#ffffff;text-decoration:none;border-radius:8px;font-weight:bold;font-size:15px;">Cadastrar materiais</a>
    </td></tr>
    <tr><td style="padding:20px 32px;border-top:1px solid #e5e7eb;font-size:12px;color:#9ca3af;">Alerta automático do E-monitor. Você recebe porque é administrador da plataforma.</td></tr>
  </table>
</td></tr></table>
</body></html>
```

`workers/internal/campaignalerts/templates/starting.html` (mesmo shell; headline e CTA diferentes, sem o badge "0 materiais"):
```html
<!doctype html>
<html lang="pt-BR"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"></head>
<body style="margin:0;background:#f3f4f6;font-family:Arial,Helvetica,sans-serif;color:#111827;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f3f4f6;">
<tr><td align="center" style="padding:24px;">
  <table role="presentation" width="600" cellpadding="0" cellspacing="0" style="max-width:600px;background:#ffffff;border-radius:12px;overflow:hidden;">
    <tr><td style="padding:28px 32px;border-bottom:1px solid #e5e7eb;">
      <img src="{{.BaseURL}}/E-monitor%20logo.png" alt="E-monitor" height="28" style="display:block;">
    </td></tr>
    <tr><td style="padding:32px;">
      <h1 style="margin:0 0 8px;font-size:24px;line-height:1.2;">{{len .Campaigns}} campanha{{if ne (len .Campaigns) 1}}s{{end}} iniciando</h1>
      <p style="margin:0 0 24px;font-size:15px;color:#374151;">Olá {{.Recipient}}, segue a lista de campanhas que vão iniciar nos próximos dias.</p>
      {{range .Campaigns}}
      <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border:1px solid #e5e7eb;border-radius:10px;margin-bottom:12px;">
        <tr><td style="padding:16px 18px;">
          <div style="font-size:16px;font-weight:bold;">{{.Name}}</div>
          <div style="font-size:13px;color:#6b7280;margin-top:2px;">{{.ClientName}}</div>
          <div style="font-size:13px;color:#374151;margin-top:8px;">Início: <b>{{fmtDate .StartDate}}</b> · Fim: {{fmtDate .EndDate}} · {{.StationCount}} emissora{{if ne .StationCount 1}}s{{end}}</div>
        </td></tr>
      </table>
      {{end}}
      <a href="{{.BaseURL}}/campaigns" style="display:inline-block;margin-top:12px;padding:12px 22px;background:#2563eb;color:#ffffff;text-decoration:none;border-radius:8px;font-weight:bold;font-size:15px;">Ver campanhas</a>
    </td></tr>
    <tr><td style="padding:20px 32px;border-top:1px solid #e5e7eb;font-size:12px;color:#9ca3af;">Alerta automático do E-monitor. Você recebe porque é administrador da plataforma.</td></tr>
  </table>
</td></tr></table>
</body></html>
```

`workers/internal/campaignalerts/templates/ending.html` (foco no fim):
```html
<!doctype html>
<html lang="pt-BR"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"></head>
<body style="margin:0;background:#f3f4f6;font-family:Arial,Helvetica,sans-serif;color:#111827;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f3f4f6;">
<tr><td align="center" style="padding:24px;">
  <table role="presentation" width="600" cellpadding="0" cellspacing="0" style="max-width:600px;background:#ffffff;border-radius:12px;overflow:hidden;">
    <tr><td style="padding:28px 32px;border-bottom:1px solid #e5e7eb;">
      <img src="{{.BaseURL}}/E-monitor%20logo.png" alt="E-monitor" height="28" style="display:block;">
    </td></tr>
    <tr><td style="padding:32px;">
      <h1 style="margin:0 0 8px;font-size:24px;line-height:1.2;">{{len .Campaigns}} campanha{{if ne (len .Campaigns) 1}}s{{end}} terminando</h1>
      <p style="margin:0 0 24px;font-size:15px;color:#374151;">Olá {{.Recipient}}, estas campanhas estão chegando ao fim nos próximos dias.</p>
      {{range .Campaigns}}
      <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border:1px solid #e5e7eb;border-radius:10px;margin-bottom:12px;">
        <tr><td style="padding:16px 18px;">
          <div style="font-size:16px;font-weight:bold;">{{.Name}}</div>
          <div style="font-size:13px;color:#6b7280;margin-top:2px;">{{.ClientName}}</div>
          <div style="font-size:13px;color:#374151;margin-top:8px;">Fim: <b>{{fmtDate .EndDate}}</b> · {{.StationCount}} emissora{{if ne .StationCount 1}}s{{end}}</div>
        </td></tr>
      </table>
      {{end}}
      <a href="{{.BaseURL}}/campaigns" style="display:inline-block;margin-top:12px;padding:12px 22px;background:#2563eb;color:#ffffff;text-decoration:none;border-radius:8px;font-weight:bold;font-size:15px;">Ver campanhas</a>
    </td></tr>
    <tr><td style="padding:20px 32px;border-top:1px solid #e5e7eb;font-size:12px;color:#9ca3af;">Alerta automático do E-monitor. Você recebe porque é administrador da plataforma.</td></tr>
  </table>
</td></tr></table>
</body></html>
```

`workers/internal/campaignalerts/templates/_email.txt`:
```
{{.Heading}}

Olá {{.Recipient}},
{{range .Campaigns}}
- {{.Name}} ({{.ClientName}}) — início {{fmtDate .StartDate}}, fim {{fmtDate .EndDate}}, {{.StationCount}} emissora(s){{end}}

Acesse: {{.BaseURL}}/campaigns
—
Alerta automático do E-monitor.
```

- [ ] **Step 4: Write render.go**

```go
package campaignalerts

import (
	"bytes"
	"embed"
	"fmt"
	htmltpl "html/template"
	"time"

	"radiocheck/internal/calendar"
	texttpl "text/template"
)

//go:embed templates/*.html templates/*.txt
var templatesFS embed.FS

// EmailContent é o resultado renderizado de um disparo.
type EmailContent struct {
	Subject string
	HTML    string
	Text    string
}

// viewData é o contrato passado aos templates. NÃO renomear campos sem atualizar
// os templates (Task 13 deve preservar este contrato).
type viewData struct {
	Recipient string
	Heading   string
	BaseURL   string
	Campaigns []CampaignAlert
}

func fmtDate(t time.Time) string {
	return t.In(calendar.BR).Format("02/01/2006")
}

var (
	htmlTemplates = htmltpl.Must(htmltpl.New("").Funcs(htmltpl.FuncMap{
		"fmtDate": fmtDate,
	}).ParseFS(templatesFS, "templates/*.html"))
	textTemplate = texttpl.Must(texttpl.New("_email.txt").Funcs(texttpl.FuncMap{
		"fmtDate": fmtDate,
	}).ParseFS(templatesFS, "templates/_email.txt"))
)

func render(htmlFile, subject, heading, recipient, baseURL string, campaigns []CampaignAlert) (EmailContent, error) {
	data := viewData{Recipient: recipient, Heading: heading, BaseURL: baseURL, Campaigns: campaigns}
	var html bytes.Buffer
	if err := htmlTemplates.ExecuteTemplate(&html, htmlFile, data); err != nil {
		return EmailContent{}, fmt.Errorf("render html %s: %w", htmlFile, err)
	}
	var text bytes.Buffer
	if err := textTemplate.Execute(&text, data); err != nil {
		return EmailContent{}, fmt.Errorf("render text: %w", err)
	}
	return EmailContent{Subject: subject, HTML: html.String(), Text: text.String()}, nil
}

func subjectCount(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// RenderStartingNoMaterial — disparo 1.
func RenderStartingNoMaterial(recipient string, campaigns []CampaignAlert, baseURL string) (EmailContent, error) {
	n := len(campaigns)
	subj := "⚠️ " + subjectCount(n, "campanha sem material", "campanhas sem material")
	return render("starting_no_material.html", subj, subjectCount(n, "campanha sem material", "campanhas sem material"), recipient, baseURL, campaigns)
}

// RenderStarting — disparo 2.
func RenderStarting(recipient string, campaigns []CampaignAlert, baseURL string) (EmailContent, error) {
	n := len(campaigns)
	subj := subjectCount(n, "campanha iniciando", "campanhas iniciando")
	return render("starting.html", subj, subj, recipient, baseURL, campaigns)
}

// RenderEnding — disparo 3.
func RenderEnding(recipient string, campaigns []CampaignAlert, baseURL string) (EmailContent, error) {
	n := len(campaigns)
	subj := subjectCount(n, "campanha terminando", "campanhas terminando")
	return render("ending.html", subj, subj, recipient, baseURL, campaigns)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd workers && go test ./internal/campaignalerts/... -run Render`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/campaignalerts/render.go workers/internal/campaignalerts/render_test.go workers/internal/campaignalerts/templates/
git commit -m "feat(campaignalerts): render baseline dos 3 emails (HTML+texto)"
```

---

## Task 10: `campaignalerts` — service (build + envio por tipo)

**Files:**
- Create: `workers/internal/campaignalerts/service.go`

- [ ] **Step 1: Write the service**

```go
package campaignalerts

import (
	"context"
	"time"

	"go.uber.org/zap"

	"radiocheck/internal/mailer"
	"radiocheck/internal/metrics"
	"radiocheck/internal/users"
)

// recipientLister é satisfeito por *users.Repo.
type recipientLister interface {
	ActiveInternal(ctx context.Context) ([]users.User, error)
}

// Service orquestra a montagem e o envio dos 3 disparos.
type Service struct {
	repo    *Repo
	logs    *LogStore
	users   recipientLister
	mail    mailer.Mailer
	baseURL string
	log     *zap.Logger

	// sendRetries é o nº de tentativas por destinatário.
	sendRetries int
}

// NewService constrói o serviço de alertas.
func NewService(repo *Repo, logs *LogStore, usersRepo recipientLister, mail mailer.Mailer, baseURL string, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{repo: repo, logs: logs, users: usersRepo, mail: mail, baseURL: baseURL, log: log, sendRetries: 3}
}

// alertType encapsula a query e o render de um disparo.
type alertType struct {
	name   string // valor do CHECK em notification_log
	fetch  func(ctx context.Context, today time.Time) ([]CampaignAlert, error)
	render func(recipient string, campaigns []CampaignAlert, baseURL string) (EmailContent, error)
}

func (s *Service) types() []alertType {
	return []alertType{
		{"starting_no_material", s.repo.StartingNoMaterial, RenderStartingNoMaterial},
		{"starting", s.repo.Starting, RenderStarting},
		{"ending", s.repo.Ending, RenderEnding},
	}
}

// ProcessType executa um disparo para o dia `today`. Assume que o caller já
// verificou dedup/advisory lock. Grava o notification_log ao final.
func (s *Service) ProcessType(ctx context.Context, today time.Time, at alertType) {
	campaigns, err := at.fetch(ctx, today)
	if err != nil {
		s.log.Error("campaignalerts: fetch falhou", zap.String("type", at.name), zap.Error(err))
		_ = s.logs.Record(ctx, LogEntry{Date: today, Type: at.name, Status: "failed", Error: err.Error()})
		metrics.NotificationsFailedTotal.WithLabelValues(at.name).Inc()
		return
	}
	if len(campaigns) == 0 {
		s.log.Info("campaignalerts: nada a enviar", zap.String("type", at.name))
		_ = s.logs.Record(ctx, LogEntry{Date: today, Type: at.name, Status: "skipped_empty"})
		return
	}

	recipients, err := s.users.ActiveInternal(ctx)
	if err != nil {
		s.log.Error("campaignalerts: lista de destinatários falhou", zap.String("type", at.name), zap.Error(err))
		_ = s.logs.Record(ctx, LogEntry{Date: today, Type: at.name, CampaignCount: len(campaigns), Status: "failed", Error: err.Error()})
		metrics.NotificationsFailedTotal.WithLabelValues(at.name).Inc()
		return
	}

	sent, failed := 0, 0
	for _, r := range recipients {
		if r.Email == "" {
			continue
		}
		content, rerr := at.render(displayName(r), campaigns, s.baseURL)
		if rerr != nil {
			s.log.Error("campaignalerts: render falhou", zap.String("type", at.name), zap.Error(rerr))
			failed++
			continue
		}
		if s.sendWithRetry(ctx, r.Email, content) {
			sent++
		} else {
			failed++
		}
	}

	status := "sent"
	if failed > 0 && sent > 0 {
		status = "partial"
	} else if failed > 0 {
		status = "failed"
	}
	metrics.NotificationsSentTotal.WithLabelValues(at.name).Add(float64(sent))
	if failed > 0 {
		metrics.NotificationsFailedTotal.WithLabelValues(at.name).Add(float64(failed))
	}
	metrics.NotificationsRecipients.WithLabelValues(at.name).Set(float64(len(recipients)))
	s.log.Info("campaignalerts: disparo concluído",
		zap.String("type", at.name), zap.Int("campaigns", len(campaigns)),
		zap.Int("sent", sent), zap.Int("failed", failed), zap.String("status", status))
	_ = s.logs.Record(ctx, LogEntry{Date: today, Type: at.name, RecipientCount: sent, CampaignCount: len(campaigns), Status: status})
}

func (s *Service) sendWithRetry(ctx context.Context, email string, c EmailContent) bool {
	var last error
	for attempt := 1; attempt <= s.sendRetries; attempt++ {
		if err := s.mail.Send(ctx, []string{email}, c.Subject, c.HTML, c.Text); err == nil {
			return true
		} else {
			last = err
			select {
			case <-ctx.Done():
				return false
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
	}
	s.log.Warn("campaignalerts: envio falhou após retries", zap.String("to", email), zap.Error(last))
	return false
}

func displayName(u users.User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}
```

- [ ] **Step 2: Verify build**

Run: `cd workers && go build ./internal/campaignalerts/...`
Expected: sem erros.

- [ ] **Step 3: Commit**

```bash
git add workers/internal/campaignalerts/service.go
git commit -m "feat(campaignalerts): service de build e envio por disparo"
```

---

## Task 11: `campaignalerts` — scheduler (ticker, janela, dedup, advisory lock)

**Files:**
- Create: `workers/internal/campaignalerts/scheduler.go`
- Test: `workers/internal/campaignalerts/scheduler_test.go`

- [ ] **Step 1: Write the failing test**

Testa a decisão de "deve rodar agora?" sem tocar banco/SMTP (relógio injetado).

```go
package campaignalerts

import (
	"testing"
	"time"

	"radiocheck/internal/calendar"
)

func at(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.ParseInLocation("2006-01-02 15:04", s, calendar.BR)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm
}

func TestShouldRun(t *testing.T) {
	sendHour := 8
	cases := []struct {
		name string
		now  string
		want bool
	}{
		{"sexta 08:30 roda", "2026-06-12 08:30", true},
		{"sexta 07:59 antes da hora", "2026-06-12 07:59", false},
		{"sabado nunca", "2026-06-13 10:00", false},
		{"domingo nunca", "2026-06-14 10:00", false},
		{"segunda 09:00 roda", "2026-06-08 09:00", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldRun(at(t, c.now), sendHour); got != c.want {
				t.Errorf("shouldRun(%s) = %v, want %v", c.now, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd workers && go test ./internal/campaignalerts/... -run ShouldRun`
Expected: FAIL — `undefined: shouldRun`.

- [ ] **Step 3: Write the scheduler**

```go
package campaignalerts

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/calendar"
)

// advisoryLockKey serializa o disparo entre réplicas da API (mesmo padrão do
// calibration scheduler).
var advisoryLockKey = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("radiocheck:campaign-alerts-scheduler"))
	return int64(h.Sum64())
}()

// Scheduler dispara os emails diários. Roda só em dia útil, a partir de
// sendHour (BRT), uma vez por dia por tipo (dedup via notification_log).
type Scheduler struct {
	pool     *pgxpool.Pool
	svc      *Service
	sendHour int
	log      *zap.Logger

	Interval time.Duration  // intervalo do ticker
	NowFn    func() time.Time
}

// NewScheduler constrói o scheduler. sendHour é a hora local de corte (ex.: 8).
func NewScheduler(pool *pgxpool.Pool, svc *Service, sendHour int, log *zap.Logger) *Scheduler {
	if log == nil {
		log = zap.NewNop()
	}
	return &Scheduler{
		pool: pool, svc: svc, sendHour: sendHour, log: log,
		Interval: 5 * time.Minute,
		NowFn:    time.Now,
	}
}

// shouldRun decide se a janela de envio do dia está aberta: dia útil e hora
// local >= sendHour. O dedup diário é responsabilidade do notification_log.
func shouldRun(now time.Time, sendHour int) bool {
	local := now.In(calendar.BR)
	if !calendar.IsBusinessDay(local) {
		return false
	}
	return local.Hour() >= sendHour
}

// Run bloqueia até ctx cancelar.
func (s *Scheduler) Run(ctx context.Context) {
	s.log.Info("campaign-alerts scheduler iniciado",
		zap.Duration("interval", s.Interval), zap.Int("send_hour", s.sendHour))
	s.tick(ctx)
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("campaign-alerts scheduler parado")
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	now := s.NowFn()
	if !shouldRun(now, s.sendHour) {
		return
	}
	today := calendar.DateOnly(now)

	// Antes de pegar o lock, pula tipos já enviados hoje (barato e evita
	// segurar conexão à toa).
	pending := make([]alertType, 0, 3)
	for _, at := range s.svc.types() {
		sent, err := s.svc.logs.AlreadySent(ctx, today, at.name)
		if err != nil {
			s.log.Warn("campaign-alerts: AlreadySent falhou", zap.String("type", at.name), zap.Error(err))
			continue
		}
		if !sent {
			pending = append(pending, at)
		}
	}
	if len(pending) == 0 {
		return
	}

	// Advisory lock de sessão: segura a conexão durante todo o processamento.
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		s.log.Warn("campaign-alerts: acquire conn falhou", zap.Error(err))
		return
	}
	defer conn.Release()

	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&locked); err != nil {
		s.log.Warn("campaign-alerts: advisory lock falhou", zap.Error(err))
		return
	}
	if !locked {
		s.log.Info("campaign-alerts: outra instância segura o lock; pulando tick")
		return
	}
	defer func() { _, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey) }()

	for _, at := range pending {
		// Re-checa dedup sob lock (outra instância pode ter enviado entre o
		// pré-filtro e a aquisição do lock).
		sent, err := s.svc.logs.AlreadySent(ctx, today, at.name)
		if err != nil {
			s.log.Warn("campaign-alerts: AlreadySent (sob lock) falhou", zap.String("type", at.name), zap.Error(err))
			continue
		}
		if sent {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					s.log.Error("campaign-alerts: panic no disparo",
						zap.String("type", at.name), zap.Any("recover", r))
				}
			}()
			s.svc.ProcessType(ctx, today, at)
		}()
	}
	_ = fmt.Sprint // keep fmt import if unused after edits
}
```

> Se o `fmt` ficar sem uso, remover o import e a linha `_ = fmt.Sprint`. O `go build` aponta.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd workers && go test ./internal/campaignalerts/... -run ShouldRun`
Expected: PASS.

- [ ] **Step 5: Build the whole package**

Run: `cd workers && go build ./internal/campaignalerts/...`
Expected: sem erros (remover `fmt` se acusar unused).

- [ ] **Step 6: Commit**

```bash
git add workers/internal/campaignalerts/scheduler.go workers/internal/campaignalerts/scheduler_test.go
git commit -m "feat(campaignalerts): scheduler com janela diária, dedup e advisory lock"
```

---

## Task 12: Wiring em `main.go`

**Files:**
- Modify: `workers/cmd/api/main.go`

- [ ] **Step 1: Add imports**

No bloco de imports, adicionar (caminhos `radiocheck/internal/...`):

```go
	"radiocheck/internal/campaignalerts"
	"radiocheck/internal/mailer"
```

- [ ] **Step 2: Wire the job after usersRepo is built**

Logo após a linha `usersRepo := users.NewRepo(pool)` (linha ~261), inserir:

```go
	// Emails diários de alerta de campanha (§ docs/features/campaign-notification-emails.md).
	// Opt-in via NOTIFICATIONS_ENABLED; sem credenciais SMTP, mailer.New retorna noop.
	if cfg.NotificationsEnabled {
		mail := mailer.New(mailer.Config{
			Enabled: cfg.NotificationsEnabled,
			Host:    cfg.SMTPHost,
			Port:    cfg.SMTPPort,
			User:    cfg.SMTPUser,
			Pass:    cfg.SMTPPass,
			From:    cfg.MailFrom,
		}, logger)
		alertRepo := campaignalerts.New(pool)
		alertLogs := campaignalerts.NewLogStore(pool)
		alertSvc := campaignalerts.NewService(alertRepo, alertLogs, usersRepo, mail, cfg.NotificationsBaseURL, logger)
		alertSched := campaignalerts.NewScheduler(pool, alertSvc, cfg.NotificationsSendHour, logger)
		if v := os.Getenv("NOTIFICATIONS_INTERVAL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				alertSched.Interval = d
			}
		}
		go alertSched.Run(ctx)
		logger.Info("campaign notification emails enabled",
			zap.Int("send_hour", cfg.NotificationsSendHour),
			zap.String("base_url", cfg.NotificationsBaseURL))
	}
```

> `os`, `time`, `zap` já são importados no `main.go` (usados pelos outros jobs). Confirmar; se algum faltar, adicionar.

- [ ] **Step 3: Build the binary**

Run: `cd workers && go build ./cmd/api/...`
Expected: sem erros.

- [ ] **Step 4: Run the full test suite for touched packages**

Run: `cd workers && go test ./internal/calendar/... ./internal/mailer/... ./internal/users/... ./internal/campaignalerts/... ./internal/config/...`
Expected: PASS (campaignalerts DB-tests podem SKIP sem `TEST_DATABASE_URL`).

- [ ] **Step 5: Commit**

```bash
git add workers/cmd/api/main.go
git commit -m "feat(api): wiring do job de emails de alerta de campanha"
```

---

## Task 13: Polimento visual dos emails via `/impeccable`

**Files:**
- Modify: `workers/internal/campaignalerts/templates/starting_no_material.html`
- Modify: `workers/internal/campaignalerts/templates/starting.html`
- Modify: `workers/internal/campaignalerts/templates/ending.html`

> **INTERATIVO — rodar na sessão principal, NÃO em subagent.** Esta task usa a skill `/impeccable` em 3 passos, conforme pedido pelo usuário.

**Contrato a preservar** (não renomear; o render.go depende disto):
- Variáveis: `{{.Recipient}}`, `{{.BaseURL}}`, `{{.Campaigns}}` (range), e por campanha `{{.Name}}`, `{{.ClientName}}`, `{{.StartDate}}`, `{{.EndDate}}`, `{{.StationCount}}`, `{{.MaterialCount}}`.
- Função `{{fmtDate .StartDate}}` para datas.
- Logo: `{{.BaseURL}}/E-monitor%20logo.png`.
- CTA aponta para `{{.BaseURL}}/campaigns`.

- [ ] **Step 1: `/impeccable` shape** — definir a estrutura/wireframe dos 3 emails inspirado nos refs do reallygoodemails (Webflow Conf, Mobbin, Semrush): header com logo, headline bold, intro, cards de campanha com hierarquia clara, um CTA primário, footer. Email-client-safe: tabelas aninhadas, CSS inline, largura ≤600px, sem `<style>` dependente, imagens com `alt`.

- [ ] **Step 2: `/impeccable critique`** — auditar o shape contra boas práticas de email (compatibilidade Gmail/Outlook, contraste/acessibilidade, dark mode, fallback de imagem, tamanho).

- [ ] **Step 3: `/impeccable craft`** — produzir o HTML final inline dos 3 templates, mantendo o contrato de variáveis acima.

- [ ] **Step 4: Re-render golden test passa**

Run: `cd workers && go test ./internal/campaignalerts/... -run Render`
Expected: PASS (os asserts de conteúdo — nome, cliente, data, logo, CTA — continuam válidos).

- [ ] **Step 5: Preview manual do HTML**

Renderizar um exemplo e abrir no navegador para conferência visual (script ad-hoc `go run` que chama `RenderStarting` e escreve um `.html` em `c:\tmp`), e idealmente um envio de teste real (ver Task 14).

- [ ] **Step 6: Commit**

```bash
git add workers/internal/campaignalerts/templates/
git commit -m "feat(campaignalerts): craft visual dos emails via /impeccable"
```

---

## Task 14: Validação end-to-end + documentação

**Files:**
- Create: `docs/features/campaign-notification-emails.md`
- Modify: `docs/README.md`
- Modify: `CLAUDE.md` (tabela de consulta)

- [ ] **Step 1: Teste de envio real (regra cultural — não confiar em config não testada)**

Localmente, com uma App Password do Workspace, setar env e forçar um intervalo curto + dado de fixture, confirmar que o email chega de verdade:

```bash
cd workers
NOTIFICATIONS_ENABLED=true \
SMTP_USER="ops@e-monitor.online" SMTP_PASS="<app-password>" \
MAIL_FROM="E-monitor <ops@e-monitor.online>" \
NOTIFICATIONS_BASE_URL="https://e-monitor.online" \
NOTIFICATIONS_INTERVAL=30s NOTIFICATIONS_SEND_HOUR=0 \
DATABASE_URL=$TEST_DATABASE_URL ... go run ./cmd/api
```
Com uma campanha programada sem material dentro da janela, confirmar a chegada do email na caixa de um admin de teste. (Ajustar a forma de subir o `api` ao runner local usual; o ponto é validar o envio real, não a config no papel.)

> NUNCA testar config inédita direto na VM de prod (regra 4.3). Validar local primeiro.

- [ ] **Step 2: Escrever a doc da feature**

`docs/features/campaign-notification-emails.md`:
```markdown
---
status: implementado
ultima-verificacao: 2026-06-09
codigo-relacionado:
  - workers/internal/campaignalerts/scheduler.go
  - workers/internal/campaignalerts/service.go
  - workers/internal/campaignalerts/queries.go
  - workers/internal/campaignalerts/render.go
  - workers/internal/calendar/businessdays.go
  - workers/internal/mailer/mailer.go
  - migrations/0036_notification_log.up.sql
---

# Emails diários de alerta de campanha

Três emails diários para usuários internos (admin/operator) sobre o ciclo de
campanha. Disparo só em dia útil, a partir das 08:00 BRT, uma vez por dia por
tipo (dedup via `notification_log`).

## Os 3 disparos
- **starting_no_material:** campanhas `programada` na janela e sem material.
- **starting:** todas as campanhas `programada` na janela.
- **ending:** campanhas `ativa` terminando na janela.

## Janela
`<3 dias corridos OU ≤2 dias úteis` (seg–sex, sem feriados) para start/end.
Implementação: `internal/calendar`.

## Configuração (env)
| Var | Default | |
|---|---|---|
| NOTIFICATIONS_ENABLED | false | liga o job |
| SMTP_HOST | smtp.gmail.com | |
| SMTP_PORT | 587 | |
| SMTP_USER / SMTP_PASS | — | App Password do Workspace |
| MAIL_FROM | =SMTP_USER | |
| NOTIFICATIONS_BASE_URL | https://e-monitor.online | links/CTA |
| NOTIFICATIONS_SEND_HOUR | 8 | hora BRT de corte |
| NOTIFICATIONS_INTERVAL | 5m | (opcional) intervalo do ticker |

## Observabilidade
`radiocheck_notifications_sent_total{type}`,
`radiocheck_notifications_failed_total{type}`,
`radiocheck_notifications_recipients{type}`.

## Operação do SMTP
Usa App Password do Google Workspace (Conta Google → Segurança → Senhas de app).
Multi-réplica: advisory lock serializa o disparo. Reenvio diário é automático.
```

- [ ] **Step 3: Indexar a doc**

Adicionar linha em `docs/README.md` e na tabela "Mapa de consulta" do `CLAUDE.md`:
```
| Emails diários de alerta de campanha (iniciando sem material / iniciando / terminando) | [docs/features/campaign-notification-emails.md](docs/features/campaign-notification-emails.md) |
```

- [ ] **Step 4: Commit**

```bash
git add docs/features/campaign-notification-emails.md docs/README.md CLAUDE.md
git commit -m "docs(features): emails diários de alerta de campanha"
```

---

## Self-Review (preenchido)

**1. Spec coverage:**
- 3 disparos → Tasks 7 (queries) + 9/13 (render) + 10 (service). ✅
- Janela `<3 corridos OU ≤2 úteis`, sem feriados, nunca fim de semana → Task 1 (`InAlertWindow`) + Task 11 (`shouldRun`). ✅
- SMTP Workspace, Abordagem A → Task 4 + Task 10 (retry) + Task 11 (advisory lock/dedup). ✅
- "Sem material" = MaterialCount==0 → Task 7 `StartingNoMaterial`. ✅
- Destinatários admin+operator ativos → Task 6 `ActiveInternal`. ✅
- 08:00 BRT, reenvio diário → Task 11 + `notification_log` (PK por data). ✅
- 3 emails separados; disparo 2 lista todas → Task 7 `Starting`. ✅
- Base URL + logo da sidebar → Task 3 (config) + Task 9 (template). ✅
- Disparo 1 só `programada`, disparo 3 só `ativa` → Task 7 (status filters). ✅
- Observabilidade/sem falha silenciosa → Task 5 + logs no Task 10. ✅
- Teste E2E real do envio → Task 14 Step 1. ✅

**2. Placeholder scan:** sem TBD/TODO; todo passo de código tem código real. ✅

**3. Type consistency:** `CampaignAlert`, `EmailContent`, `viewData`, `LogEntry`, `alertType`, `Service`, `Scheduler`, `Repo`, `LogStore` usados de forma consistente entre Tasks 7–13. `ActiveInternal` (Task 6) casa com `recipientLister` (Task 10). Render funcs `RenderStartingNoMaterial/Starting/Ending` casam entre Task 9 e Task 10. ✅

**4. Ambiguity:** janela definida como intervalo semiaberto `(today, target]` explicitamente; `lookaheadDays=4` justificado. ✅
