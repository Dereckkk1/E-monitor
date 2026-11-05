package recording

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestCaptureArgs(t *testing.T) {
	const fixo = "-c:a aac -b:a 128k -ar 44100 -ac 2"
	for _, url := range []string{"http://r/stream", "http://r/playlist.m3u8"} {
		args := strings.Join(captureArgs(url, "/d/a1-%Y.adts"), " ")
		require.Contains(t, args, fixo, url)
		require.NotContains(t, args, "-c:a copy", url)
		require.Contains(t, args, "-segment_time 60")
		require.Contains(t, args, "-segment_format adts")
		require.Contains(t, args, "-user_agent VLC/3.0.20 LibVLC/3.0.20")
		require.True(t, strings.HasSuffix(args, "/d/a1-%Y.adts"))
	}

	require.Contains(t, strings.Join(captureArgs("http://r/stream", "/d/x"), " "), "-reconnect_at_eof 1")

	hls := strings.Join(captureArgs("http://r/playlist.m3u8", "/d/x"), " ")
	require.NotContains(t, hls, "-reconnect_at_eof", "HLS com esse flag nunca decodifica (incidente 2026-05-15)")
}

func TestCleanStderr_ProduzTextoValidoParaOPostgres(t *testing.T) {
	// rune UTF-8 partido no início (tailWriter corta por bytes), Latin-1 de metadado
	// ICY e NUL: nenhum desses pode chegar na coluna TEXT.
	in := []byte("\x80\xa9erro ao abrir: caf\xe9\x00 fim\n")
	got := cleanStderr(in)
	require.True(t, utf8.ValidString(got), "%q", got)
	require.NotContains(t, got, "\x00")
	require.Equal(t, "erro ao abrir: caf fim", got)
}

func TestTailWriter_GuardaSoOFim(t *testing.T) {
	w := &tailWriter{}
	for i := 0; i < 10; i++ {
		n, err := w.Write([]byte(strings.Repeat("a", 1000)))
		require.NoError(t, err)
		require.Equal(t, 1000, n)
	}
	_, _ = w.Write([]byte("FINAL"))
	require.Len(t, w.buf, stderrTail)
	require.True(t, strings.HasSuffix(string(w.buf), "FINAL"))
}

func TestPiecePattern_MantemOPercentDoStrftime(t *testing.T) {
	got := piecePattern("/d", time.Unix(1762380000, 0))
	require.Equal(t, filepath.Join("/d", "a1762380000-%Y%m%d-%H%M%S.adts"), got)
	require.True(t, pieceName.MatchString("a1762380000-20261105-200000.adts"))
}

// Prova com ffmpeg de verdade que ADTS concatenado vira M4A com duração.
func TestRemuxEDuration(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg não instalado")
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "joined.tmp")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-c:a", "aac", "-f", "adts", in)
	require.NoError(t, gen.Run())

	out := filepath.Join(dir, "out.m4a")
	f := FFmpeg{}
	require.NoError(t, f.Remux(context.Background(), in, out))

	dur, err := f.Duration(context.Background(), out)
	require.NoError(t, err)
	require.InDelta(t, 3.0, dur.Seconds(), 0.2)

	info, err := os.Stat(out)
	require.NoError(t, err)
	require.Positive(t, info.Size())
}

// Pedaços de fontes diferentes (22 kHz mono, 48 kHz estéreo) concatenados sem
// formato fixo geravam um .m4a de duração errada sem nenhum erro. Com o formato
// fixo de audioArgs, a duração final é a soma.
func TestRemux_PedacosComMesmoFormatoFixo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg não instalado")
	}
	dir := t.TempDir()
	gen := func(name, src, layout string, secs int) Piece {
		p := filepath.Join(dir, name)
		args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
			"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:%s:duration=%d", src, secs),
			"-af", "aformat=channel_layouts=" + layout}
		args = append(args, audioArgs...)
		args = append(args, "-f", "adts", p)
		out, err := exec.Command("ffmpeg", args...).CombinedOutput()
		require.NoError(t, err, string(out))
		return Piece{Path: p}
	}
	pieces := []Piece{
		gen("a1-20261105-200000.adts", "sample_rate=22050", "mono", 3),
		gen("a2-20261105-200100.adts", "sample_rate=48000", "stereo", 4),
	}

	joined := filepath.Join(dir, "joined.tmp")
	require.NoError(t, Concat(pieces, joined))
	out := filepath.Join(dir, "out.m4a")
	f := FFmpeg{}
	require.NoError(t, f.Remux(context.Background(), joined, out))

	dur, err := f.Duration(context.Background(), out)
	require.NoError(t, err)
	require.InDelta(t, 7.0, dur.Seconds(), 0.3)
}
