package recording

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCaptureArgs(t *testing.T) {
	aac := strings.Join(captureArgs("http://r/stream", "aac", "/d/a1-%Y.adts"), " ")
	require.Contains(t, aac, "-c:a copy")
	require.Contains(t, aac, "-reconnect_at_eof 1")
	require.Contains(t, aac, "-segment_time 60")
	require.Contains(t, aac, "-segment_format adts")
	require.Contains(t, aac, "-user_agent VLC/3.0.20 LibVLC/3.0.20")
	require.True(t, strings.HasSuffix(aac, "/d/a1-%Y.adts"))

	mp3 := strings.Join(captureArgs("http://r/stream", "mp3", "/d/x"), " ")
	require.Contains(t, mp3, "-c:a aac -b:a 128k")

	hls := strings.Join(captureArgs("http://r/playlist.m3u8", "aac", "/d/x"), " ")
	require.NotContains(t, hls, "-reconnect_at_eof", "HLS com esse flag nunca decodifica (incidente 2026-05-15)")
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
