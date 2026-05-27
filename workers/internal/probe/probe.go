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
	Detail      string `json:"detail,omitempty"`        // preenchido em fail
	LatencyMs   int64  `json:"latency_ms,omitempty"`    // ping
	Codec       string `json:"codec,omitempty"`         // stream
	Source      string `json:"source,omitempty"`        // worker: "live"|"ephemeral"
	LastPCMAt   string `json:"last_pcm_at,omitempty"`   // worker (live), RFC3339
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
