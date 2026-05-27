# Etapa "Conexão" no wizard de campanha — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Adicionar um Step 3 "Conexão" ao wizard de campanha onde o operador testa (ping/stream/worker) e troca a `stream_url` de cada emissora da campanha, com feedback visual por linha — tudo sob demanda e efêmero, sem subir worker permanente.

**Architecture:** Backend Go ganha um pacote `internal/probe` (funções puras testáveis + probes que spawnam ffmpeg/ffprobe sob um `Limiter` de concorrência), dois handlers novos em `StationsHandler` (`StationConnectionTest` e `UpdateStreamURL` dedicado e seguro), e wiring no router/main. Frontend ganha `ConnectionStep.jsx` + 2 hooks; o wizard é renumerado de 5 → 6 steps.

**Tech Stack:** Go 1.x (chi router, pgx, zap, stdlib `net`/`os/exec`), React 18 + @tanstack/react-query, ffmpeg/ffprobe (já no container).

**Spec:** [`docs/superpowers/specs/2026-05-27-campaign-connection-step-design.md`](../specs/2026-05-27-campaign-connection-step-design.md)

---

## File Structure

**Criar:**
- `workers/internal/probe/probe.go` — tipos de resultado, `Limiter`, funções puras (`hostPort`, `ingestOK`, `pickWorkerSource`).
- `workers/internal/probe/probe_test.go` — testes das funções puras + Limiter.
- `workers/internal/probe/ffmpeg.go` — `Ping`, `ProbeStream`, `ProbeIngest` (spawnam processo/rede).
- `frontend/src/pages/CampaignWizardSteps/ConnectionStep.jsx` — a etapa.

**Modificar:**
- `workers/internal/catalog/stations.go` — método `UpdateStreamURL`.
- `workers/internal/api/handlers/stations.go` — handlers `UpdateStreamURL` + `StationConnectionTest`, campo `Workers` no struct.
- `workers/internal/api/handlers/stations_test.go` — testes dos handlers novos.
- `workers/internal/api/router.go` — 2 rotas novas no subgrupo admin/operator.
- `workers/cmd/api/main.go` — injetar `Workers: sup` no `StationsHandler`.
- `frontend/src/api/hooks.js` — `useStationConnectionTest`, `useUpdateStationStreamURL`.
- `frontend/src/components/WizardStepper.jsx` — novo step "Conexão".
- `frontend/src/components/WizardLayout.jsx` — `STEP_META` 1..6, `isLast===6`, footer `/6`.
- `frontend/src/pages/CampaignWizardPage.jsx` — roteamento 6 steps + render do `ConnectionStep`.
- `docs/features/campaign-connection-step.md` (criar), `docs/features/campaign-wizard.md`, `docs/README.md`, `CLAUDE.md` (mapa).

---

## Task 1: Pacote `probe` — tipos, Limiter e funções puras

**Files:**
- Create: `workers/internal/probe/probe.go`
- Test: `workers/internal/probe/probe_test.go`

- [ ] **Step 1: Escrever os testes que falham**

```go
// workers/internal/probe/probe_test.go
package probe

import (
	"context"
	"testing"
	"time"
)

func TestHostPort(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		want    string
		wantErr bool
	}{
		{"http default port", "http://stream.example.com/live", "stream.example.com:80", false},
		{"https default port", "https://stream.example.com/live", "stream.example.com:443", false},
		{"explicit port", "http://1.2.3.4:8000/stream", "1.2.3.4:8000", false},
		{"https explicit port", "https://host:9443/x", "host:9443", false},
		{"empty", "", "", true},
		{"no scheme", "stream.example.com/live", "", true},
		{"bad scheme ftp", "ftp://host/file", "", true},
		{"bad scheme file", "file:///etc/passwd", "", true},
		{"no host", "http:///path", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := hostPort(c.url)
			if c.wantErr {
				if err == nil {
					t.Fatalf("hostPort(%q) = %q, want error", c.url, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("hostPort(%q) unexpected error: %v", c.url, err)
			}
			if got != c.want {
				t.Fatalf("hostPort(%q) = %q, want %q", c.url, got, c.want)
			}
		})
	}
}

func TestIngestOK(t *testing.T) {
	// 16kHz mono s16le = 32000 bytes/sec. Floor is 50% of expected.
	cases := []struct {
		name  string
		bytes int64
		dur   time.Duration
		want  bool
	}{
		{"full 5s", 160000, 5 * time.Second, true},
		{"exactly half 5s", 80000, 5 * time.Second, true},
		{"just under half", 79999, 5 * time.Second, false},
		{"zero bytes", 0, 5 * time.Second, false},
		{"zero dur guards", 100, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ingestOK(c.bytes, c.dur); got != c.want {
				t.Fatalf("ingestOK(%d, %v) = %v, want %v", c.bytes, c.dur, got, c.want)
			}
		})
	}
}

func TestPickWorkerSource(t *testing.T) {
	recent := time.Now().Add(-5 * time.Second)
	stale := time.Now().Add(-90 * time.Second)
	cases := []struct {
		name        string
		overrideURL string
		savedURL    string
		live        *LiveWorker
		wantSource  string
		wantUseLive bool
	}{
		{"no override, live recent", "", "http://a/s", &LiveWorker{Active: true, LastPCMAt: recent}, "live", true},
		{"no override, live stale", "", "http://a/s", &LiveWorker{Active: true, LastPCMAt: stale}, "ephemeral", false},
		{"no override, no worker", "", "http://a/s", nil, "ephemeral", false},
		{"override == saved, live recent", "http://a/s", "http://a/s", &LiveWorker{Active: true, LastPCMAt: recent}, "live", true},
		{"override != saved forces ephemeral", "http://b/s", "http://a/s", &LiveWorker{Active: true, LastPCMAt: recent}, "ephemeral", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src, useLive := PickWorkerSource(c.overrideURL, c.savedURL, c.live)
			if src != c.wantSource || useLive != c.wantUseLive {
				t.Fatalf("PickWorkerSource(%q,%q,%v) = (%q,%v), want (%q,%v)",
					c.overrideURL, c.savedURL, c.live, src, useLive, c.wantSource, c.wantUseLive)
			}
		})
	}
}

func TestLimiter_Capacity(t *testing.T) {
	l := NewLimiter(2)
	if !l.TryAcquire() {
		t.Fatal("1st TryAcquire should succeed")
	}
	if !l.TryAcquire() {
		t.Fatal("2nd TryAcquire should succeed")
	}
	if l.TryAcquire() {
		t.Fatal("3rd TryAcquire should fail at capacity 2")
	}
	l.Release()
	if !l.TryAcquire() {
		t.Fatal("TryAcquire after Release should succeed")
	}
}

func TestLimiter_AcquireRespectsContext(t *testing.T) {
	l := NewLimiter(1)
	if err := l.Acquire(context.Background()); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := l.Acquire(ctx); err == nil {
		t.Fatal("Acquire on full limiter should return ctx error")
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd workers && go test ./internal/probe/...`
Expected: FAIL — `undefined: hostPort`, `ingestOK`, `pickWorkerSource`, `NewLimiter`, `LiveWorker`.

- [ ] **Step 3: Implementar `probe.go`**

```go
// workers/internal/probe/probe.go
//
// Package probe roda diagnósticos sob demanda de uma stream_url de emissora —
// alcançabilidade de rede (Ping), validade do stream (ProbeStream via ffprobe)
// e ingest efêmero (ProbeIngest via ffmpeg). Tudo é efêmero por design: NUNCA
// sobe worker permanente. Ver docs/features/campaign-connection-step.md.
package probe

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// sampleRate / bytesPerSample espelham o pipe PCM de análise que o worker real
// consome (16kHz mono s16le) — ver ingestor.StartFFmpeg ("-ar 16000 -ac 1").
const (
	ingestSampleRate   = 16000
	ingestBytesPerSamp = 2 // s16le
	ingestFloorFrac    = 0.5
)

// Status é o veredito de um teste individual.
type Status string

const (
	StatusOK      Status = "ok"
	StatusFail    Status = "fail"
	StatusSkipped Status = "skipped"
)

// TestResult é o resultado de UM teste (ping, stream ou worker). Campos opcionais
// usam omitempty pra manter o JSON enxuto por tipo de teste.
type TestResult struct {
	Status      Status `json:"status"`
	Detail      string `json:"detail,omitempty"`       // preenchido em fail
	LatencyMs   int64  `json:"latency_ms,omitempty"`   // ping
	Codec       string `json:"codec,omitempty"`        // stream
	Source      string `json:"source,omitempty"`       // worker: "live"|"ephemeral"
	LastPCMAt   string `json:"last_pcm_at,omitempty"`  // worker (live), RFC3339
	BytesPerSec int64  `json:"bytes_per_sec,omitempty"` // worker (ephemeral)
}

// LiveWorker é o subset do supervisor.WorkerStatus que o caminho híbrido precisa.
// O handler converte WorkerStatus → LiveWorker pra manter o pacote probe sem
// dependência do supervisor.
type LiveWorker struct {
	Active    bool
	LastPCMAt time.Time
}

// liveFreshness é a idade máxima do último PCM pra considerar um worker vivo
// "provando" que o stream funciona, sem rodar probe efêmero. Espelha o
// StallRisk do supervisor (30s).
const liveFreshness = 30 * time.Second

// hostPort extrai "host:porta" de uma stream URL, aplicando a porta default do
// scheme. Rejeita schemes != http/https (mitiga SSRF: impede file://, gopher://
// etc. de fazerem o servidor abrir conexões arbitrárias).
func hostPort(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("url vazia")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("url inválida: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("scheme não suportado: %q (use http/https)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("host ausente na url")
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return host + ":" + port, nil
}

// ingestOK decide se a quantidade de PCM recebida num probe efêmero é suficiente
// pra considerar o stream "tocável pelo worker". Piso = 50% do esperado pra
// tolerar o ramp-up do ffmpeg/reconnect inicial dentro da janela.
func ingestOK(bytes int64, dur time.Duration) bool {
	if dur <= 0 {
		return false
	}
	expected := float64(ingestSampleRate*ingestBytesPerSamp) * dur.Seconds()
	return float64(bytes) >= expected*ingestFloorFrac
}

// PickWorkerSource implementa o caminho híbrido (spec §6):
//   - override de URL diferente da salva → sempre efêmero (o worker vivo, se
//     existe, está na URL antiga — irrelevante pra essa URL).
//   - senão, se há worker Active com PCM recente → usa o status ao vivo.
//   - caso contrário → efêmero.
//
// Retorna (source, useLive). useLive=true significa "não spawne nada, devolva o
// status do worker vivo". Exportada pra o handler usar como fonte única da regra.
func PickWorkerSource(overrideURL, savedURL string, live *LiveWorker) (string, bool) {
	if overrideURL != "" && overrideURL != savedURL {
		return "ephemeral", false
	}
	if live != nil && live.Active && !live.LastPCMAt.IsZero() &&
		time.Since(live.LastPCMAt) < liveFreshness {
		return "live", true
	}
	return "ephemeral", false
}

// Limiter é um semáforo de contagem que limita quantos probes spawnam
// ffmpeg/ffprobe ao mesmo tempo — a barreira autoritativa anti-sobrecarga
// (spec §7). Ping não passa por aqui (é barato).
type Limiter struct {
	sem chan struct{}
}

func NewLimiter(n int) *Limiter {
	if n < 1 {
		n = 1
	}
	return &Limiter{sem: make(chan struct{}, n)}
}

// TryAcquire pega um slot sem bloquear; retorna false se cheio.
func (l *Limiter) TryAcquire() bool {
	select {
	case l.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// Acquire bloqueia até ter slot ou o ctx expirar.
func (l *Limiter) Acquire(ctx context.Context) error {
	select {
	case l.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release devolve um slot.
func (l *Limiter) Release() { <-l.sem }
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd workers && go test ./internal/probe/...`
Expected: PASS (todos os testes do Step 1).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/probe/probe.go workers/internal/probe/probe_test.go
git commit -m "feat(probe): pacote probe — tipos, Limiter e funções puras"
```

---

## Task 2: Probes de rede/processo (`Ping`, `ProbeStream`, `ProbeIngest`)

**Files:**
- Create: `workers/internal/probe/ffmpeg.go`
- Test: `workers/internal/probe/ffmpeg_test.go`

`ProbeStream`/`ProbeIngest` spawnam ffprobe/ffmpeg — não dá pra unit-testar puro. Testamos só o `Ping` (com um listener TCP local) e a validação de scheme dos três. As funções de processo são exercitadas no smoke manual (Task 9) com o simulador de stream.

- [ ] **Step 1: Escrever o teste que falha**

```go
// workers/internal/probe/ffmpeg_test.go
package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPing_Reachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	res := Ping(context.Background(), srv.URL)
	if res.Status != StatusOK {
		t.Fatalf("Ping(%s) = %s (%s), want ok", srv.URL, res.Status, res.Detail)
	}
}

func TestPing_Unreachable(t *testing.T) {
	// Porta fechada: abrimos e fechamos um listener pra pegar uma porta que
	// (quase com certeza) ninguém está escutando.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()

	res := Ping(context.Background(), "http://"+addr+"/stream")
	if res.Status != StatusFail {
		t.Fatalf("Ping(closed) = %s, want fail", res.Status)
	}
}

func TestPing_BadScheme(t *testing.T) {
	res := Ping(context.Background(), "ftp://host/x")
	if res.Status != StatusFail {
		t.Fatalf("Ping(ftp) = %s, want fail", res.Status)
	}
}

func TestProbeStream_BadScheme(t *testing.T) {
	res := ProbeStream(context.Background(), NewLimiter(1), "file:///etc/passwd")
	if res.Status != StatusFail {
		t.Fatalf("ProbeStream(file) = %s, want fail", res.Status)
	}
}

func TestProbeIngest_BadScheme(t *testing.T) {
	res := ProbeIngest(context.Background(), NewLimiter(1), "ftp://host/x", time.Second)
	if res.Status != StatusFail {
		t.Fatalf("ProbeIngest(ftp) = %s, want fail", res.Status)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd workers && go test ./internal/probe/... -run 'Ping|Probe'`
Expected: FAIL — `undefined: Ping`, `ProbeStream`, `ProbeIngest`.

- [ ] **Step 3: Implementar `ffmpeg.go`**

```go
// workers/internal/probe/ffmpeg.go
package probe

import (
	"context"
	"io"
	"net"
	"os/exec"
	"time"

	"radiocheck/internal/ingestor"
)

const (
	pingTimeout   = 2 * time.Second
	ingestProbeMs = 5 // segundos lidos pelo ProbeIngest
)

// Ping faz um TCP dial no host:porta da URL. É o teste mais barato e distingue
// "host inalcançável" de "host vivo, stream podre". Não passa pelo Limiter.
func Ping(ctx context.Context, rawURL string) TestResult {
	hp, err := hostPort(rawURL)
	if err != nil {
		return TestResult{Status: StatusFail, Detail: err.Error()}
	}
	d := net.Dialer{Timeout: pingTimeout}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", hp)
	if err != nil {
		return TestResult{Status: StatusFail, Detail: "host inalcançável"}
	}
	_ = conn.Close()
	return TestResult{Status: StatusOK, LatencyMs: time.Since(start).Milliseconds()}
}

// ProbeStream usa ffprobe (via ingestor.ProbeAudioCodec) pra confirmar que há
// um stream de áudio decodável na URL. Passa pelo Limiter (spawna ffprobe).
func ProbeStream(ctx context.Context, lim *Limiter, rawURL string) TestResult {
	if _, err := hostPort(rawURL); err != nil {
		return TestResult{Status: StatusFail, Detail: err.Error()}
	}
	if err := lim.Acquire(ctx); err != nil {
		return TestResult{Status: StatusFail, Detail: "fila de testes ocupada, tente de novo"}
	}
	defer lim.Release()

	codec, err := ingestor.ProbeAudioCodec(ctx, rawURL)
	if err != nil {
		return TestResult{Status: StatusFail, Detail: "ffprobe falhou ou timeout"}
	}
	if codec == "" {
		return TestResult{Status: StatusFail, Detail: "nenhum stream de áudio"}
	}
	return TestResult{Status: StatusOK, Codec: codec}
}

// ProbeIngest mimica o worker: ffmpeg puxa o stream pra PCM s16le 16kHz mono por
// `dur` e conta bytes. ok se recebeu fluxo acima do piso (ingestOK). Mata o
// ffmpeg ao fim via context. Passa pelo Limiter.
func ProbeIngest(ctx context.Context, lim *Limiter, rawURL string, dur time.Duration) TestResult {
	if _, err := hostPort(rawURL); err != nil {
		return TestResult{Status: StatusFail, Detail: err.Error()}
	}
	if err := lim.Acquire(ctx); err != nil {
		return TestResult{Status: StatusFail, Detail: "fila de testes ocupada, tente de novo"}
	}
	defer lim.Release()

	// Janela = dur + folga pra conexão/handshake antes de o ffmpeg ser morto.
	runCtx, cancel := context.WithTimeout(ctx, dur+pingTimeout+2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "ffmpeg",
		"-nostdin",
		"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "2",
		"-timeout", "5000000",
		"-user_agent", "VLC/3.0.20 LibVLC/3.0.20",
		"-i", rawURL,
		"-t", itoa(int(dur.Seconds())),
		"-vn",
		"-ar", "16000", "-ac", "1", "-f", "s16le",
		"pipe:1",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return TestResult{Status: StatusFail, Detail: "erro ao preparar ffmpeg"}
	}
	if err := cmd.Start(); err != nil {
		return TestResult{Status: StatusFail, Detail: "ffmpeg não iniciou"}
	}
	n, _ := io.Copy(io.Discard, stdout) // conta bytes de PCM
	_ = cmd.Wait()                      // reap; processo já morreu por -t ou ctx

	if !ingestOK(n, dur) {
		return TestResult{Status: StatusFail, Detail: "sem PCM suficiente"}
	}
	bps := int64(0)
	if dur.Seconds() > 0 {
		bps = int64(float64(n) / dur.Seconds())
	}
	return TestResult{Status: StatusOK, Source: "ephemeral", BytesPerSec: bps}
}

// itoa evita importar strconv só pra um int pequeno e não-negativo.
func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `cd workers && go test ./internal/probe/...`
Expected: PASS. (Os testes de bad-scheme passam sem spawnar processo; `TestPing_*` usam listener local.)

- [ ] **Step 5: Commit**

```bash
git add workers/internal/probe/ffmpeg.go workers/internal/probe/ffmpeg_test.go
git commit -m "feat(probe): Ping, ProbeStream e ProbeIngest efêmeros"
```

---

## Task 3: `catalog.Stations.UpdateStreamURL`

**Files:**
- Modify: `workers/internal/catalog/stations.go` (após o método `Update`, ~linha 350)

Método dedicado e seguro que atualiza **só** `stream_url` — sem o footgun do `Update` (que reescreve todas as colunas).

- [ ] **Step 1: Implementar o método**

Adicionar logo após o método `Update` (depois da linha 350) em `stations.go`:

```go
// UpdateStreamURL atualiza SOMENTE a stream_url da emissora (e updated_at).
// Existe separado do Update porque o Update reescreve todas as colunas — usar
// ele pra trocar só a URL zeraria city/state/frequency_mhz/logo_url/pmm/metadata
// quando o caller não reenvia tudo. A etapa Conexão do wizard troca a URL de
// forma cirúrgica, então usa este caminho. Ver
// docs/features/campaign-connection-step.md.
func (s *Stations) UpdateStreamURL(ctx context.Context, id uuid.UUID, streamURL string) (*Station, error) {
	st, err := scanStationRow(s.pool.QueryRow(ctx, fmt.Sprintf(`
		UPDATE stations SET
		  stream_url = $2,
		  updated_at = NOW()
		WHERE id = $1
		RETURNING %s`, stationSelectCols),
		id, streamURL,
	).Scan)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}
	return &st, nil
}
```

- [ ] **Step 2: Compilar**

Run: `cd workers && go build ./internal/catalog/...`
Expected: sem erros (usa `scanStationRow`, `stationSelectCols`, `pgx`, `fmt`, `uuid` já importados no arquivo).

- [ ] **Step 3: Commit**

```bash
git add workers/internal/catalog/stations.go
git commit -m "feat(catalog): UpdateStreamURL — troca cirúrgica da stream_url"
```

---

## Task 4: Handlers `UpdateStreamURL` e `StationConnectionTest`

**Files:**
- Modify: `workers/internal/api/handlers/stations.go`
- Test: `workers/internal/api/handlers/stations_test.go`

- [ ] **Step 1: Escrever os testes que falham**

Adicionar ao final de `stations_test.go` (antes ou depois dos testes existentes; o pacote já importa `chi`, `httptest`, `http`, `strings`, `json`):

```go
// TestStations_UpdateStreamURL_BadJSON rejeita body malformado.
func TestStations_UpdateStreamURL_BadJSON(t *testing.T) {
	h := &StationsHandler{}
	r := chi.NewRouter()
	r.Patch("/stations/{id}/stream-url", h.UpdateStreamURL)
	req := httptest.NewRequest(http.MethodPatch,
		"/stations/3b1f0c2e-0000-0000-0000-000000000000/stream-url",
		strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestStations_UpdateStreamURL_MissingURL rejeita url vazia.
func TestStations_UpdateStreamURL_MissingURL(t *testing.T) {
	h := &StationsHandler{}
	r := chi.NewRouter()
	r.Patch("/stations/{id}/stream-url", h.UpdateStreamURL)
	req := httptest.NewRequest(http.MethodPatch,
		"/stations/3b1f0c2e-0000-0000-0000-000000000000/stream-url",
		strings.NewReader(`{"url":""}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestStations_UpdateStreamURL_BadScheme rejeita scheme != http/https.
func TestStations_UpdateStreamURL_BadScheme(t *testing.T) {
	h := &StationsHandler{}
	r := chi.NewRouter()
	r.Patch("/stations/{id}/stream-url", h.UpdateStreamURL)
	req := httptest.NewRequest(http.MethodPatch,
		"/stations/3b1f0c2e-0000-0000-0000-000000000000/stream-url",
		strings.NewReader(`{"url":"ftp://host/x"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestStations_ConnectionTest_BadJSON rejeita body malformado.
func TestStations_ConnectionTest_BadJSON(t *testing.T) {
	h := &StationsHandler{}
	r := chi.NewRouter()
	r.Post("/stations/{id}/connection-test", h.ConnectionTest)
	req := httptest.NewRequest(http.MethodPost,
		"/stations/3b1f0c2e-0000-0000-0000-000000000000/connection-test",
		strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestStations_ConnectionTest_InvalidID rejeita uuid inválido.
func TestStations_ConnectionTest_InvalidID(t *testing.T) {
	h := &StationsHandler{}
	r := chi.NewRouter()
	r.Post("/stations/{id}/connection-test", h.ConnectionTest)
	req := httptest.NewRequest(http.MethodPost, "/stations/not-a-uuid/connection-test",
		strings.NewReader(`{"tests":["ping"]}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `cd workers && go test ./internal/api/handlers/ -run 'UpdateStreamURL|ConnectionTest'`
Expected: FAIL — `h.UpdateStreamURL undefined`, `h.ConnectionTest undefined`.

- [ ] **Step 3: Implementar os handlers**

No topo de `stations.go`, ampliar imports e o struct. Trocar o bloco de imports e o struct atuais:

```go
import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"radiocheck/internal/catalog"
	"radiocheck/internal/probe"
	"radiocheck/internal/supervisor"
)

type StationsHandler struct {
	Repo *catalog.Stations
	// Workers expõe o snapshot de workers vivos pro caminho híbrido do teste
	// "worker". Satisfeito por *supervisor.Supervisor. nil em testes de
	// validação (o caminho live é pulado → cai em efêmero).
	Workers workerLister
	// Limiter limita probes concorrentes que spawnam ffmpeg/ffprobe. nil → o
	// handler cria um default lazy (capacidade probeConcurrency).
	Limiter *probe.Limiter
}

const probeConcurrency = 8
```

> `workerLister` já existe em `health.go` (mesmo pacote `handlers`): `interface { WorkerStatuses() []supervisor.WorkerStatus }`.

Adicionar os dois handlers (após `GetThreshold`, antes de `writeJSON`):

```go
// UpdateStreamURL troca SOMENTE a stream_url da emissora. Caminho dedicado e
// seguro usado pela etapa Conexão do wizard — ver catalog.UpdateStreamURL.
func (h *StationsHandler) UpdateStreamURL(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var in struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if in.URL == "" {
		http.Error(w, "url is required", 400)
		return
	}
	if !validStreamScheme(in.URL) {
		http.Error(w, "url must be http or https", 400)
		return
	}
	st, err := h.Repo.UpdateStreamURL(r.Context(), id, in.URL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}
	writeJSON(w, 200, st)
}

// connectionTestRequest é o body de POST /stations/{id}/connection-test.
type connectionTestRequest struct {
	Tests []string `json:"tests"` // subset de ping|stream|worker; vazio = todos
	URL   string   `json:"url"`   // override opcional
}

// ConnectionTest roda os diagnósticos pedidos (ping/stream/worker) na URL salva
// ou no override. Efêmero: nunca sobe worker permanente. Ver
// docs/features/campaign-connection-step.md.
func (h *StationsHandler) ConnectionTest(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var req connectionTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	st, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, "internal error", 500)
		}
		return
	}

	savedURL := st.StreamURL
	targetURL := savedURL
	if req.URL != "" {
		targetURL = req.URL
	}

	if h.Limiter == nil {
		h.Limiter = probe.NewLimiter(probeConcurrency)
	}

	want := func(name string) bool {
		if len(req.Tests) == 0 {
			return true
		}
		for _, t := range req.Tests {
			if t == name {
				return true
			}
		}
		return false
	}

	results := map[string]probe.TestResult{
		"ping":   {Status: probe.StatusSkipped},
		"stream": {Status: probe.StatusSkipped},
		"worker": {Status: probe.StatusSkipped},
	}

	if want("ping") {
		results["ping"] = probe.Ping(r.Context(), targetURL)
	}
	if want("stream") {
		results["stream"] = probe.ProbeStream(r.Context(), h.Limiter, targetURL)
	}
	if want("worker") {
		results["worker"] = h.workerTest(r, id, targetURL, savedURL)
	}

	writeJSON(w, 200, map[string]any{
		"station_id": id.String(),
		"tested_url": targetURL,
		"results":    results,
	})
}

// workerTest implementa o caminho híbrido: status ao vivo se há worker fresco na
// URL salva; senão probe efêmero.
func (h *StationsHandler) workerTest(r *http.Request, id uuid.UUID, targetURL, savedURL string) probe.TestResult {
	var live *probe.LiveWorker
	if h.Workers != nil {
		for _, ws := range h.Workers.WorkerStatuses() {
			if ws.StationID == id.String() {
				live = &probe.LiveWorker{Active: ws.Active, LastPCMAt: ws.LastPCMAt}
				break
			}
		}
	}
	source, useLive := probe.PickWorkerSource(targetURL, savedURL, live)
	if useLive {
		return probe.TestResult{
			Status:    probe.StatusOK,
			Source:    source,
			LastPCMAt: live.LastPCMAt.Format(time.RFC3339),
		}
	}
	return probe.ProbeIngest(r.Context(), h.Limiter, targetURL, 5*time.Second)
}

// validStreamScheme aceita só http/https (mitiga SSRF no override/salvar).
func validStreamScheme(raw string) bool {
	return len(raw) >= 7 && (raw[:7] == "http://" || (len(raw) >= 8 && raw[:8] == "https://"))
}
```

> **Nota de consistência:** o `30*time.Second` em `pickLiveOrEphemeral` deve casar com `probe.liveFreshness`. Como `pickWorkerSource`/`liveFreshness` são unexported no pacote probe, replicamos a constante aqui; a regra pura continua coberta por `probe.TestPickWorkerSource`. Se for incômodo, no futuro exporte `probe.LiveFreshness` e a função — não é necessário pro v1.

- [ ] **Step 4: Rodar e ver passar**

Run: `cd workers && go test ./internal/api/handlers/ -run 'UpdateStreamURL|ConnectionTest'`
Expected: PASS. (Os testes não setam `Repo`/`Workers`; o `ConnectionTest_InvalidID` e os `BadJSON` falham antes de tocar o Repo. `ConnectionTest_BadJSON` decodifica body inválido → 400 antes do `Repo.Get`.)

> Atenção: `TestStations_ConnectionTest_BadJSON` precisa do decode falhar antes do `Repo.Get`. O handler decodifica o body logo após parsear o id, então com id válido e body inválido retorna 400 sem tocar o Repo. ✅

- [ ] **Step 5: Compilar o pacote inteiro**

Run: `cd workers && go build ./...`
Expected: erro só em `cmd/api/main.go` se o wiring ainda não foi feito? Não — o campo `Workers` é opcional, então `&handlers.StationsHandler{Repo: stations}` continua compilando. Build deve passar.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/api/handlers/stations.go workers/internal/api/handlers/stations_test.go
git commit -m "feat(api): handlers UpdateStreamURL e ConnectionTest"
```

---

## Task 5: Rotas + wiring do Supervisor

**Files:**
- Modify: `workers/internal/api/router.go` (subgrupo admin/operator, após linha 219)
- Modify: `workers/cmd/api/main.go` (linha 288)

- [ ] **Step 1: Adicionar as rotas**

Em `router.go`, no subgrupo B (admin/operator, onde já estão `Post("/stations", ...)` e `Put("/stations/{id}", ...)` nas linhas 217-219), adicionar logo após a linha 219 (`r.Get("/stations/{id}/threshold", ...)`):

```go
			r.Patch("/stations/{id}/stream-url", d.Stations.UpdateStreamURL)
			r.Post("/stations/{id}/connection-test", d.Stations.ConnectionTest)
```

- [ ] **Step 2: Injetar o Supervisor no handler**

Em `workers/cmd/api/main.go` linha 288, trocar:

```go
		Stations:     &handlers.StationsHandler{Repo: stations},
```

por:

```go
		Stations:     &handlers.StationsHandler{Repo: stations, Workers: sup},
```

> `sup` é o `*supervisor.Supervisor` já em escopo (usado em `Health`, `StreamHealth`, `Commercials`, `Admin`, `SystemHealth` nas linhas vizinhas). Ele satisfaz `workerLister` via `WorkerStatuses()`.

- [ ] **Step 3: Compilar tudo**

Run: `cd workers && go build ./...`
Expected: sem erros.

- [ ] **Step 4: Rodar a suíte do backend**

Run: `cd workers && go test ./...`
Expected: PASS (incluindo probe + handlers novos).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/router.go workers/cmd/api/main.go
git commit -m "feat(api): rotas stream-url + connection-test e wiring do supervisor"
```

---

## Task 6: Hooks no frontend

**Files:**
- Modify: `frontend/src/api/hooks.js` (após `useUpdateStation`, ~linha 35)

- [ ] **Step 1: Adicionar os hooks**

Inserir após o `useUpdateStation` (linha 35), antes do comentário `// Clients`:

```js
// Conexão (etapa do wizard) — testa ping/stream/worker de uma emissora.
// Não invalida cache: resultado é efêmero (vive no estado da ConnectionStep).
export function useStationConnectionTest() {
  return useMutation({
    mutationFn: ({ id, tests, url }) =>
      api.post(`/stations/${id}/connection-test`, {
        tests: tests ?? undefined,
        url: url || undefined,
      }).then(r => r.data),
  })
}
// PATCH cirúrgico da stream_url (não reescreve as outras colunas, ao contrário
// do PUT /stations/{id}). Usado pela ConnectionStep ao salvar uma URL nova.
export function useUpdateStationStreamURL() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, url }) =>
      api.patch(`/stations/${id}/stream-url`, { url }).then(r => r.data),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['stations'] })
      qc.invalidateQueries({ queryKey: ['stations', vars.id] })
    },
  })
}
```

- [ ] **Step 2: Verificar build do frontend**

Run: `cd frontend && npm run build`
Expected: build sem erros de sintaxe/import. (`api` já é importado no topo; `useMutation`/`useQueryClient` também.)

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/hooks.js
git commit -m "feat(frontend): hooks useStationConnectionTest e useUpdateStationStreamURL"
```

---

## Task 7: Componente `ConnectionStep.jsx`

**Files:**
- Create: `frontend/src/pages/CampaignWizardSteps/ConnectionStep.jsx`

- [ ] **Step 1: Criar o componente**

```jsx
// frontend/src/pages/CampaignWizardSteps/ConnectionStep.jsx
import { useState, useEffect, useRef, useMemo, useCallback } from 'react'
import { useStationConnectionTest, useUpdateStationStreamURL } from '../../api/hooks'
import { useDialog } from '../../contexts/DialogContext'
import StationAvatar from '../../components/StationAvatar'

/**
 * Step 3 do wizard: Conexão. Lista as emissoras da campanha e deixa o operador
 * testar (ping/stream/worker) e trocar a stream_url de cada uma. Tudo efêmero:
 * o resultado vive só neste componente; nada de worker permanente. Salvar uma
 * URL nova chama PATCH /stations/{id}/stream-url (cirúrgico).
 *
 * Props:
 *  - campaignStations: Array<station> (as emissoras da campanha, objetos completos)
 */
const TEST_CONCURRENCY = 6

export default function ConnectionStep({ campaignStations = [] }) {
  const { showConfirm } = useDialog()
  const connTest = useStationConnectionTest()
  const saveURL = useUpdateStationStreamURL()

  // Estado por emissora: { urlDraft, dirty, results, running }
  const [rows, setRows] = useState({})
  const didAutoPing = useRef(false)

  // Hidrata o estado quando as emissoras chegam/mudam, preservando drafts já digitados.
  useEffect(() => {
    setRows(prev => {
      const next = { ...prev }
      for (const s of campaignStations) {
        if (!next[s.id]) {
          next[s.id] = {
            urlDraft: s.stream_url || '',
            dirty: false,
            results: { ping: null, stream: null, worker: null },
            running: false,
          }
        }
      }
      return next
    })
  }, [campaignStations])

  const patchRow = useCallback((id, partial) => {
    setRows(prev => ({ ...prev, [id]: { ...prev[id], ...partial } }))
  }, [])

  // Roda um conjunto de testes numa emissora. `tests` = ['ping'] | ['ping','stream','worker'].
  const runTests = useCallback(async (station, tests) => {
    const id = station.id
    const url = (rowsRef.current[id]?.urlDraft || '').trim()
    if (!url) {
      patchRow(id, { results: { ping: fail('sem URL'), stream: null, worker: null } })
      return
    }
    patchRow(id, { running: true })
    try {
      const data = await connTest.mutateAsync({ id, tests, url })
      patchRow(id, {
        running: false,
        results: {
          ...rowsRef.current[id].results,
          ...mapResults(data.results, tests),
        },
      })
    } catch {
      patchRow(id, {
        running: false,
        results: {
          ...rowsRef.current[id].results,
          ...Object.fromEntries(tests.map(t => [t, fail('erro de rede')])),
        },
      })
    }
  }, [connTest, patchRow])

  // Espelho de `rows` num ref pra runTests ler o valor corrente sem virar dep.
  const rowsRef = useRef(rows)
  useEffect(() => { rowsRef.current = rows }, [rows])

  // Auto-ping ao montar (uma vez), com limite de concorrência.
  useEffect(() => {
    if (didAutoPing.current) return
    if (campaignStations.length === 0) return
    didAutoPing.current = true
    runPool(campaignStations, TEST_CONCURRENCY, s => runTests(s, ['ping']))
  }, [campaignStations, runTests])

  const testAll = useCallback(() => {
    runPool(campaignStations, TEST_CONCURRENCY, s => runTests(s, ['ping', 'stream', 'worker']))
  }, [campaignStations, runTests])

  const onSave = useCallback(async (station) => {
    const id = station.id
    const url = (rowsRef.current[id]?.urlDraft || '').trim()
    if (!url) return
    const ok = await showConfirm(
      `Isso vai trocar a URL de streaming da emissora "${station.name}" para todas as campanhas que a usam, não só esta. Confirmar?`,
      'Salvar nova URL de streaming',
    )
    if (!ok) return
    try {
      await saveURL.mutateAsync({ id, url })
      patchRow(id, { dirty: false })
      // re-testa na URL agora salva
      runTests(station, ['ping', 'stream', 'worker'])
    } catch {
      alert('Erro ao salvar a URL. Tente novamente.')
    }
  }, [saveURL, showConfirm, patchRow, runTests])

  const problemCount = useMemo(() => {
    let n = 0
    for (const s of campaignStations) {
      const st = rowState(rows[s.id])
      if (st === 'fail') n++
    }
    return n
  }, [rows, campaignStations])

  if (campaignStations.length === 0) {
    return <EmptyState />
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
      <Header
        total={campaignStations.length}
        problemCount={problemCount}
        onTestAll={testAll}
      />
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        {campaignStations.map(s => (
          <StationRow
            key={s.id}
            station={s}
            row={rows[s.id]}
            onUrlChange={v => patchRow(s.id, { urlDraft: v, dirty: v.trim() !== (s.stream_url || '').trim() })}
            onTest={() => runTests(s, ['ping', 'stream', 'worker'])}
            onSave={() => onSave(s)}
          />
        ))}
      </div>
    </div>
  )
}

// ─── helpers ──────────────────────────────────────────────────────────────

function fail(detail) { return { status: 'fail', detail } }

function mapResults(apiResults, tests) {
  const out = {}
  for (const t of tests) {
    const r = apiResults?.[t]
    out[t] = r && r.status !== 'skipped' ? r : null
  }
  return out
}

// rowState deriva o estado visual da linha a partir dos resultados.
//   'testing' se algum teste rodando; 'fail' se algum fail; 'ok' se todos os
//   testados deram ok e ao menos um foi testado; 'idle' caso contrário.
function rowState(row) {
  if (!row) return 'idle'
  if (row.running) return 'testing'
  const vals = ['ping', 'stream', 'worker'].map(k => row.results?.[k]).filter(Boolean)
  if (vals.length === 0) return 'idle'
  if (vals.some(v => v.status === 'fail')) return 'fail'
  if (vals.every(v => v.status === 'ok')) return 'ok'
  return 'idle'
}

// runPool roda `fn` sobre `items` com no máximo `limit` em paralelo.
async function runPool(items, limit, fn) {
  const queue = [...items]
  const workers = Array.from({ length: Math.min(limit, queue.length) }, async () => {
    while (queue.length) {
      const item = queue.shift()
      await fn(item)
    }
  })
  await Promise.all(workers)
}

const STATE_COLOR = {
  ok:      { bg: 'rgba(34,197,94,0.10)',  border: 'var(--c-success, #22c55e)', dot: 'var(--c-success, #22c55e)' },
  fail:    { bg: 'rgba(239,68,68,0.08)',  border: 'var(--c-danger, #ef4444)',  dot: 'var(--c-danger, #ef4444)' },
  testing: { bg: 'rgba(59,130,246,0.08)', border: 'var(--c-info, #3b82f6)',    dot: 'var(--c-info, #3b82f6)' },
  idle:    { bg: 'var(--c-surface)',      border: 'var(--c-border)',           dot: 'var(--c-text-3)' },
}

function Header({ total, problemCount, onTestAll }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16, maxWidth: 760 }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <h2 style={{
          margin: 0, fontFamily: 'var(--font-heading)', fontWeight: 700,
          fontSize: 24, color: 'var(--c-text)', letterSpacing: '-0.01em',
        }}>
          A conexão de cada emissora está de pé?
        </h2>
        <p style={{ margin: 0, color: 'var(--c-text-2)', fontSize: 13, lineHeight: 1.55 }}>
          Testamos o <strong style={{ color: 'var(--c-text)' }}>ping</strong> de
          todas ao abrir. Rode os testes completos (stream + worker) quando quiser.
          Se uma URL estiver morta, cole outra no campo e teste — só salva quando
          você confirmar.
        </p>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <button
          onClick={onTestAll}
          style={{
            padding: '10px 18px', borderRadius: 'var(--radius-md)',
            background: 'var(--c-action)', color: '#fff', border: 0,
            cursor: 'pointer', fontSize: 13, fontWeight: 700,
            fontFamily: 'var(--font-heading)', boxShadow: 'var(--shadow-sm)',
          }}
        >
          Testar todas
        </button>
        <span style={{ fontSize: 12, color: 'var(--c-text-3)' }}>
          {total} emissora{total !== 1 ? 's' : ''}
          {problemCount > 0 && (
            <strong style={{ color: 'var(--c-danger, #ef4444)', marginLeft: 6 }}>
              · {problemCount} com problema
            </strong>
          )}
        </span>
      </div>
    </div>
  )
}

function StationRow({ station, row, onUrlChange, onTest, onSave }) {
  const state = rowState(row)
  const c = STATE_COLOR[state]
  const place = [station.city, station.state].filter(Boolean).join(' / ')
  return (
    <div style={{
      display: 'grid',
      gridTemplateColumns: 'minmax(180px, 1fr) minmax(220px, 1.4fr) auto auto',
      gap: 14, alignItems: 'center',
      background: c.bg,
      border: `1px solid ${c.border}`,
      borderRadius: 'var(--radius-lg, 12px)',
      padding: '12px 16px',
      transition: 'all 200ms cubic-bezier(0.16,1,0.3,1)',
    }}>
      {/* identidade */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, minWidth: 0 }}>
        <span style={{ width: 8, height: 8, borderRadius: '50%', background: c.dot, flexShrink: 0 }} />
        <StationAvatar station={station} size={30} />
        <div style={{ minWidth: 0 }}>
          <div style={{
            fontSize: 13, fontWeight: 600, color: 'var(--c-text)',
            fontFamily: 'var(--font-heading)',
            whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>
            {station.name}
          </div>
          <div style={{ fontSize: 11, color: 'var(--c-text-3)' }}>
            {station.band}{place ? ` · ${place}` : ''}
          </div>
        </div>
      </div>

      {/* url editável */}
      <input
        value={row?.urlDraft ?? ''}
        onChange={e => onUrlChange(e.target.value)}
        placeholder="http://stream…"
        spellCheck={false}
        style={{
          width: '100%', padding: '8px 10px',
          borderRadius: 'var(--radius-md)', border: '1px solid var(--c-border)',
          fontSize: 12, color: 'var(--c-text)', background: 'var(--c-surface)',
          fontFamily: 'var(--font-mono, monospace)',
        }}
      />

      {/* pílulas de status */}
      <div style={{ display: 'flex', gap: 6 }}>
        <Pill label="Ping" r={row?.results?.ping} running={row?.running} />
        <Pill label="Stream" r={row?.results?.stream} running={row?.running} />
        <Pill label="Worker" r={row?.results?.worker} running={row?.running} />
      </div>

      {/* ações */}
      <div style={{ display: 'flex', gap: 8 }}>
        <button
          onClick={onTest}
          disabled={row?.running}
          style={{
            padding: '8px 14px', borderRadius: 'var(--radius-md)',
            background: 'transparent', color: 'var(--c-text)',
            border: '1px solid var(--c-border)',
            cursor: row?.running ? 'wait' : 'pointer',
            fontSize: 12, fontWeight: 600, fontFamily: 'var(--font-heading)',
          }}
        >
          {row?.running ? 'Testando…' : 'Testar'}
        </button>
        <button
          onClick={onSave}
          disabled={!row?.dirty}
          title={row?.dirty ? 'Salvar nova URL pra esta emissora' : 'Edite a URL pra habilitar'}
          style={{
            padding: '8px 14px', borderRadius: 'var(--radius-md)',
            background: row?.dirty ? 'var(--c-action)' : 'var(--c-surface-2)',
            color: row?.dirty ? '#fff' : 'var(--c-text-3)',
            border: 0, cursor: row?.dirty ? 'pointer' : 'not-allowed',
            fontSize: 12, fontWeight: 700, fontFamily: 'var(--font-heading)',
          }}
        >
          Salvar
        </button>
      </div>
    </div>
  )
}

function Pill({ label, r, running }) {
  let bg = 'var(--c-surface-2)', color = 'var(--c-text-3)', title = 'não testado'
  if (running && !r) { bg = 'rgba(59,130,246,0.12)'; color = 'var(--c-info, #3b82f6)'; title = 'testando' }
  else if (r?.status === 'ok') { bg = 'rgba(34,197,94,0.14)'; color = 'var(--c-success, #16a34a)'; title = r.codec || r.source || 'ok' }
  else if (r?.status === 'fail') { bg = 'rgba(239,68,68,0.12)'; color = 'var(--c-danger, #dc2626)'; title = r.detail || 'falhou' }
  return (
    <span title={title} style={{
      display: 'inline-flex', alignItems: 'center', gap: 4,
      padding: '4px 10px', borderRadius: 'var(--radius-full, 999px)',
      background: bg, color, fontSize: 11, fontWeight: 700,
      fontFamily: 'var(--font-heading)', whiteSpace: 'nowrap',
    }}>
      {label}
    </span>
  )
}

function EmptyState() {
  return (
    <div style={{
      padding: '48px 24px', textAlign: 'center',
      background: 'var(--c-bg)', border: '1px dashed var(--c-border)',
      borderRadius: 'var(--radius-xl)',
    }}>
      <div style={{
        width: 48, height: 48, margin: '0 auto 12px', borderRadius: '50%',
        background: 'var(--c-surface)', boxShadow: 'var(--shadow-sm)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        color: 'var(--c-action)',
      }}>
        <svg width="22" height="22" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
          <path d="M2 8a6 6 0 0 1 12 0" /><path d="M4.5 8a3.5 3.5 0 0 1 7 0" /><circle cx="8" cy="8" r="1" />
        </svg>
      </div>
      <div style={{ fontFamily: 'var(--font-heading)', fontWeight: 700, fontSize: 15, color: 'var(--c-text)' }}>
        Nenhuma emissora na campanha ainda
      </div>
      <div style={{ fontSize: 12, color: 'var(--c-text-2)', marginTop: 4 }}>
        Volte ao passo "Emissoras" e selecione ao menos uma para testar a conexão.
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Build do frontend**

Run: `cd frontend && npm run build`
Expected: build sem erros. (Confirma que `useDialog`, `StationAvatar` e os hooks resolvem.)

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/CampaignWizardSteps/ConnectionStep.jsx
git commit -m "feat(frontend): ConnectionStep — testa e troca stream_url por emissora"
```

---

## Task 8: Renumeração do wizard (5 → 6 steps) + render do ConnectionStep

**Files:**
- Modify: `frontend/src/components/WizardStepper.jsx`
- Modify: `frontend/src/components/WizardLayout.jsx`
- Modify: `frontend/src/pages/CampaignWizardPage.jsx`

- [ ] **Step 1: WizardStepper — inserir o step "Conexão" como id 3**

Trocar a const `STEPS` (linhas 62-68) por:

```jsx
const STEPS = [
  { id: 1, label: 'Dados básicos', hint: 'Nome, cliente, período',     Icon: IconInfo },
  { id: 2, label: 'Emissoras',     hint: 'Quem vai monitorar',         Icon: IconRadio },
  { id: 3, label: 'Conexão',       hint: 'Testar e ajustar streams',   Icon: IconSignal },
  { id: 4, label: 'Materiais',     hint: 'Áudios da campanha',         Icon: IconStack },
  { id: 5, label: 'Distribuição',  hint: 'Regras de veiculação',       Icon: IconCalendar },
  { id: 6, label: 'Valores',       hint: 'Investimento por emissora',  Icon: IconMoney },
]
```

Adicionar o ícone `IconSignal` junto dos outros ícones (após `IconRadio`, ~linha 27):

```jsx
function IconSignal() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <path d="M2 9a6 6 0 0 1 12 0" />
      <path d="M4.5 9a3.5 3.5 0 0 1 7 0" />
      <circle cx="8" cy="9" r="1" />
    </svg>
  )
}
```

> O `progress` e o `.map` já são genéricos sobre `STEPS.length`, então passam de 5 → 6 sem outra mudança.

- [ ] **Step 2: WizardLayout — STEP_META 1..6, isLast, footer**

Trocar `STEP_META` (linhas 6-12) por:

```jsx
const STEP_META = {
  1: { eyebrow: 'Passo 1 de 6', kicker: 'Identificação' },
  2: { eyebrow: 'Passo 2 de 6', kicker: 'Onde vai tocar' },
  3: { eyebrow: 'Passo 3 de 6', kicker: 'Conexão das emissoras' },
  4: { eyebrow: 'Passo 4 de 6', kicker: 'O que vai tocar' },
  5: { eyebrow: 'Passo 5 de 6', kicker: 'Quando e quanto' },
  6: { eyebrow: 'Passo 6 de 6', kicker: 'Investimento por emissora' },
}
```

Trocar `const isLast = currentStep === 5` (linha 46) por:

```jsx
  const isLast = currentStep === 6
```

Corrigir o footer (linha 182): trocar `{currentStep} / 4` por:

```jsx
          {currentStep} / 6
```

- [ ] **Step 3: CampaignWizardPage — roteamento 6 steps + render do ConnectionStep**

Adicionar o import (após a linha 11, junto dos outros steps):

```jsx
import ConnectionStep from './CampaignWizardSteps/ConnectionStep'
```

Trocar `setCompletedSteps([1, 2, 3, 4])` (linha 40) por:

```jsx
      setCompletedSteps([1, 2, 3, 4, 5])
```

Trocar `markStepCompleteAndAdvance` (linhas 79-84) — o `Math.min(5, ...)` vira `6`:

```jsx
  function markStepCompleteAndAdvance() {
    if (!completedSteps.includes(currentStep)) {
      setCompletedSteps([...completedSteps, currentStep])
    }
    setCurrentStep(s => Math.min(6, s + 1))
  }
```

Reescrever o bloco de roteamento dos steps (linhas 150-232). Substituir do `let stepContent = null` até o final do bloco `else if (currentStep === 5)` por:

```jsx
  let stepContent = null
  let nextDisabled = false

  if (currentStep === 1) {
    stepContent = (
      <BasicDataStep
        value={draftCampaign}
        onChange={setDraftCampaign}
        clients={clients}
        isEditMode={isEdit}
      />
    )
    nextDisabled = !draftCampaign.name || !draftCampaign.client_id ||
                   !draftCampaign.start_date || !draftCampaign.end_date
  } else if (currentStep === 2) {
    stepContent = (
      <StationsStep
        campaignId={campaignId}
        allStations={allStations}
        currentSelection={targetStationIds}
      />
    )
    nextDisabled = stationCount === 0
  } else if (currentStep === 3) {
    stepContent = (
      <ConnectionStep
        campaignStations={targetStationIds.map(id => allStations.find(s => s.id === id)).filter(Boolean)}
      />
    )
    // Etapa diagnóstica: nunca bloqueia avançar (spec §3).
    nextDisabled = false
  } else if (currentStep === 4) {
    stepContent = (
      <MaterialsStep
        campaignId={campaignId}
        clientId={draftCampaign.client_id}
        materialsById={materialsById}
        campaignStations={targetStationIds.map(id => allStations.find(s => s.id === id)).filter(Boolean)}
        onSkip={handleNext}
      />
    )
    const someWithoutType = campaignMaterials.some(cm => {
      const mat = materialsById[cm.material_id]
      return mat && !mat.type_id
    })
    const someWithoutStations = campaignMaterials.some(cm =>
      !cm.target_stations || cm.target_stations.length === 0)
    nextDisabled = someWithoutType || someWithoutStations
  } else if (currentStep === 5) {
    stepContent = (
      <DistributionStep
        campaignId={campaignId}
        campaignStart={existingCampaign?.start_date ?? draftCampaign.start_date}
        campaignEnd={existingCampaign?.end_date ?? draftCampaign.end_date}
        campaignMaterials={campaignMaterials}
        campaignStationIds={targetStationIds}
        materialsById={materialsById}
        allStations={allStations}
      />
    )
    nextDisabled = false
  } else if (currentStep === 6) {
    const campaignStations = targetStationIds
      .map(id => allStations.find(s => s.id === id))
      .filter(Boolean)
    stepContent = (
      <PricingStep
        ref={pricingRef}
        campaignId={campaignId}
        campaignStations={campaignStations}
        campaignMaterials={campaignMaterials}
        materialsById={materialsById}
        distributionRules={distributionRules}
      />
    )
    nextDisabled = false
  }
```

Atualizar o `nextLabel`/`onNext` (linhas 226-232): o passo final agora é o 6, e a checagem de "pular materiais" passou pro step 4. Trocar por:

```jsx
  const nextLabel =
    currentStep === 6
      ? 'Concluir campanha →'
      : (currentStep === 4 && materialCount === 0)
        ? 'Pular materiais →'
        : 'Avançar →'
  const onNext = currentStep === 6 ? handleFinish : handleNext
```

> **Verificar:** o `handleNext` tem lógica especial só pro `currentStep === 1` (criar/editar campanha). Os demais steps caem direto em `markStepCompleteAndAdvance()`. A inserção do step 3 não quebra isso — Materiais (agora 4) e adiante seguem o mesmo caminho genérico. ✅

- [ ] **Step 4: Build do frontend**

Run: `cd frontend && npm run build`
Expected: build sem erros.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/WizardStepper.jsx frontend/src/components/WizardLayout.jsx frontend/src/pages/CampaignWizardPage.jsx
git commit -m "feat(frontend): renumera wizard pra 6 steps com Conexão em 3"
```

---

## Task 9: Smoke manual end-to-end + docs

**Files:**
- Create: `docs/features/campaign-connection-step.md`
- Modify: `docs/features/campaign-wizard.md`, `docs/README.md`, `CLAUDE.md`

- [ ] **Step 1: Smoke do backend com o simulador de stream**

Seguir [`docs/operations/simulacao-radio.md`](../../operations/simulacao-radio.md) pra subir um stream local. Depois (ajuste host/porta conforme o simulador):

```bash
# Pegue um station_id real:
curl -s http://localhost:8080/v1/internal/stations?limit=1 | jq '.data[0].id'

# Teste os três contra a URL salva (precisa de um JWT admin/operator — use o do dev):
curl -s -X POST http://localhost:8080/v1/internal/stations/<ID>/connection-test \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"tests":["ping","stream","worker"]}' | jq

# Teste um override (URL ainda não salva):
curl -s -X POST http://localhost:8080/v1/internal/stations/<ID>/connection-test \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"tests":["ping","stream"],"url":"http://localhost:8000/stream"}' | jq

# Salve uma URL nova (cirúrgico) e confirme que city/state NÃO sumiram:
curl -s -X PATCH http://localhost:8080/v1/internal/stations/<ID>/stream-url \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"url":"http://localhost:8000/stream"}' | jq '{stream_url, city, state, frequency_mhz}'
```

Expected: ping ok pro host vivo; stream `ok` com `codec`; worker `ok` com `source` `live` ou `ephemeral`; URL morta → `fail` com `detail`. O PATCH retorna a station com `city`/`state`/`frequency_mhz` intactos.

- [ ] **Step 2: Smoke do frontend**

Subir o frontend (`cd frontend && npm run dev`), abrir `/campaigns/new`, criar campanha, selecionar emissoras, avançar pro Step 3 "Conexão". Verificar:
- Auto-ping pinta as pílulas de Ping ao abrir.
- "Testar todas" roda os três e colore as linhas (verde/vermelho).
- Editar uma URL habilita "Salvar"; "Testar" usa a URL digitada sem salvar.
- "Salvar" mostra o confirm; ao confirmar, persiste e re-testa.
- "Avançar" não é bloqueado mesmo com emissora vermelha.
- Stepper mostra 6 passos; footer mostra "3 / 6".

- [ ] **Step 3: Criar `docs/features/campaign-connection-step.md`**

```markdown
---
status: implementado
ultima-verificacao: 2026-05-27
codigo-relacionado:
  - frontend/src/pages/CampaignWizardSteps/ConnectionStep.jsx
  - frontend/src/pages/CampaignWizardPage.jsx
  - workers/internal/probe/probe.go
  - workers/internal/probe/ffmpeg.go
  - workers/internal/api/handlers/stations.go
  - workers/internal/catalog/stations.go
---

# Etapa "Conexão" do wizard de campanha

Step 3 do wizard (Dados → Emissoras → **Conexão** → Materiais → Distribuição →
Preços). Lista as emissoras da campanha e deixa o operador diagnosticar e trocar
a `stream_url` de cada uma.

## Escada de 3 testes (todos efêmeros, sob demanda)

| Teste | Endpoint interno | O que faz |
|-------|------------------|-----------|
| Ping | `probe.Ping` | TCP dial no host:porta da URL (~2s). Host alcançável? |
| Stream | `probe.ProbeStream` → `ingestor.ProbeAudioCodec` | ffprobe acha áudio decodável? Retorna codec. |
| Worker | híbrido (ver abaixo) | O ingest puxaria PCM? |

**Híbrido do worker:** se a campanha que usa a emissora já está ativa (há worker
vivo com PCM recente) e o teste é da URL salva → devolve o status ao vivo, sem
spawnar nada. Senão → `probe.ProbeIngest` (ffmpeg puxa ~5s de PCM e mede). Nunca
sobe worker permanente — campanhas `programada` não pesam o servidor.

## Trocar a URL

Editar a URL na linha e clicar "Testar" roda os probes na URL digitada (override)
**sem gravar**. "Salvar" chama `PATCH /v1/internal/stations/{id}/stream-url`, que
atualiza **só** a `stream_url` (não usa o `PUT` cru, que reescreveria todas as
colunas). A troca vale pra **todas** as campanhas da emissora; o reconciler
recria o worker em ≤30s se a campanha estiver ativa
([worker-commercial-reconciler](../operations/worker-commercial-reconciler.md)).

## Contrato

`POST /v1/internal/stations/{id}/connection-test` (admin/operator)
- body: `{ "tests": ["ping","stream","worker"], "url": "<override opcional>" }`
- resposta: `{ station_id, tested_url, results: { ping, stream, worker } }`, cada
  result com `status` (`ok|fail|skipped`), `detail` (em fail) e campos extras
  (`latency_ms`, `codec`, `source`, `last_pcm_at`, `bytes_per_sec`).

## Concorrência e segurança

- `probe.Limiter` (capacidade 8) limita probes que spawnam ffmpeg/ffprobe — o
  front também limita a 6 em paralelo no "Testar todas". Ping não conta.
- Override de URL é restrito a `http`/`https` (mitiga SSRF). Endpoint é
  admin/operator-only. Sem allowlist de host no v1.

## Não-objetivos

- Não persiste resultado (efêmero, vive na sessão). Histórico de saúde de
  emissoras ativas continua em `/stream-health`.
- Não sobe worker permanente pra campanha `programada`.
```

- [ ] **Step 4: Atualizar `campaign-wizard.md`, `README.md` e `CLAUDE.md`**

Em `docs/features/campaign-wizard.md`: trocar "As 4 etapas" / menções a 5 steps pelo fluxo de 6 etapas, inserindo "3. Conexão" e renumerando Materiais (4), Distribuição (5), Preços (6). Atualizar `ultima-verificacao: 2026-05-27`.

Em `docs/README.md`: adicionar a linha do novo doc na seção de features.

Em `CLAUDE.md`, na tabela "Mapa de consulta", adicionar a linha:

```markdown
| Etapa Conexão do wizard (testar/trocar stream_url por emissora) | [docs/features/campaign-connection-step.md](docs/features/campaign-connection-step.md) |
```

- [ ] **Step 5: Commit**

```bash
git add docs/features/campaign-connection-step.md docs/features/campaign-wizard.md docs/README.md CLAUDE.md
git commit -m "docs: etapa Conexão do wizard + atualiza mapa e wizard de 6 steps"
```

---

## Self-Review (preenchido pelo autor do plano)

**1. Spec coverage:**
- Posição Step 3 → Task 8. ✅
- 3 testes (ping/stream/worker) → Tasks 1-2 + handler Task 4. ✅
- Híbrido worker → `pickWorkerSource` (Task 1) + `workerTest` (Task 4) + §6. ✅
- Troca de URL inline + save cirúrgico → Task 3 (catalog) + Task 4 (handler) + Task 7 (UI). ✅
- Override sem gravar → `url` no body do connection-test (Task 4) + `runTests` usa `urlDraft` (Task 7). ✅
- Auto-ping ao abrir → Task 7 `useEffect` + `runPool`. ✅
- Não bloqueia avançar → Task 8 `nextDisabled=false` no step 3. ✅
- Persistência efêmera → estado local, hooks não persistem (Tasks 6-7). ✅
- Semáforo/concorrência → `Limiter` (Task 1), uso no handler (Task 4), `runPool` (Task 7). ✅
- SSRF (http/https only) → `hostPort` (Task 1), `validStreamScheme` (Task 4). ✅
- Renumeração 5→6 + fix "/4" → Task 8. ✅
- Docs → Task 9. ✅

**2. Placeholder scan:** sem TBD/TODO; todo step de código tem o código. ✅

**3. Type consistency:** `TestResult`, `Status`, `LiveWorker`, `Limiter`, `PickWorkerSource` (probe) usados de forma consistente entre Tasks 1, 2 e 4 — a regra híbrida tem fonte única (`probe.PickWorkerSource`, exportada e testada; o handler chama direto, sem duplicar). `useStationConnectionTest({id,tests,url})` e `useUpdateStationStreamURL({id,url})` batem entre Task 6 e Task 7. `ConnectionStep` recebe `campaignStations` (Task 7) e é renderizado com essa prop (Task 8). ✅
