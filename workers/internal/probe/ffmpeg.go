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
	pingTimeout = 2 * time.Second
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
