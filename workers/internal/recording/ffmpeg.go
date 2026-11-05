package recording

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"radiocheck/internal/ingestor"
)

const stderrTail = 2048

// tailWriter guarda só o fim do stderr: uma gravação de 6h com stream instável
// escreveria sem limite num bytes.Buffer.
type tailWriter struct{ buf []byte }

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > stderrTail {
		t.buf = t.buf[len(t.buf)-stderrTail:]
	}
	return len(p), nil
}

// FFmpeg é a implementação real de Media.
type FFmpeg struct{}

func piecePattern(dir string, now time.Time) string {
	return filepath.Join(dir, fmt.Sprintf("a%d-%%Y%%m%%d-%%H%%M%%S%s", now.Unix(), pieceExt))
}

func captureArgs(streamURL, codec, pattern string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	args = append(args, ingestor.ReconnectArgs(streamURL)...)
	args = append(args,
		"-timeout", "10000000",
		"-user_agent", "VLC/3.0.20 LibVLC/3.0.20",
		"-i", streamURL,
		"-map", "0:a:0",
	)
	args = append(args, ingestor.SegmentAudioArgs(codec)...)
	return append(args,
		"-f", "segment",
		"-segment_time", "60",
		"-segment_format", "adts",
		"-segment_atclocktime", "1",
		"-reset_timestamps", "1",
		"-strftime", "1",
		pattern,
	)
}

// Capture roda até ctx acabar (fim da janela) ou o ffmpeg sair. ctx cancelado
// não é erro: é o fim normal.
func (FFmpeg) Capture(ctx context.Context, streamURL, dir string) error {
	// Falha no probe vira codec "" e cai no reencode, que aceita qualquer entrada.
	codec, _ := ingestor.ProbeAudioCodec(ctx, streamURL)

	cmd := exec.CommandContext(ctx, "ffmpeg", captureArgs(streamURL, codec, piecePattern(dir, time.Now()))...)
	// SIGTERM deixa o ffmpeg fechar o pedaço; WaitDelay é o Kill de garantia.
	// No Windows o Signal falha e o Kill resolve: lá só roda em desenvolvimento.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	stderr := &tailWriter{}
	cmd.Stderr = stderr

	err := cmd.Run()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(string(stderr.buf)))
	}
	return errors.New("ffmpeg terminou antes do fim da janela")
}

// Remux troca o contêiner de ADTS para M4A sem reencodar. M4A abre em qualquer
// player e tem índice; ADTS cru não tem duração nem busca confiáveis.
func (FFmpeg) Remux(ctx context.Context, in, out string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "aac", "-i", in, "-c", "copy", "-movflags", "+faststart", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("remux: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

// Duration devolve a duração real do arquivo.
func (FFmpeg) Duration(ctx context.Context, path string) (time.Duration, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path)
	b, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w", err)
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return 0, fmt.Errorf("ffprobe: duração %q: %w", b, err)
	}
	return time.Duration(secs * float64(time.Second)), nil
}
