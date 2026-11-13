package recording

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func pcm(samples ...int16) io.Reader {
	var b bytes.Buffer
	for _, s := range samples {
		binary.Write(&b, binary.LittleEndian, s) //nolint:errcheck
	}
	return &b
}

func TestPeaksFromPCM_PicoAbsolutoPorJanela(t *testing.T) {
	// Janela de 2 amostras: [100,-32768] [5,0] [-16384] (incompleta também entra).
	got, err := peaksFromPCM(pcm(100, -32768, 5, 0, -16384), 2)
	require.NoError(t, err)
	require.Equal(t, []byte{255, 0, 128}, got)
}

func TestPeaksKey(t *testing.T) {
	require.Equal(t, "recordings/2026/11/abc.peaks", PeaksKey("recordings/2026/11/abc.m4a"))
}

func TestValidateClips(t *testing.T) {
	dur := 60 * time.Second
	var ve *ValidationError
	cases := map[string][]ClipSpec{
		"vazio":         nil,
		"negativo":      {{From: -1, To: 5}},
		"depois do fim": {{From: 10, To: 62}},
		"curto":         {{From: 10, To: 10.2}},
		"invertido":     {{From: 20, To: 10}},
		"muitos":        make([]ClipSpec, MaxClips+1),
	}
	for name, clips := range cases {
		t.Run(name, func(t *testing.T) {
			require.ErrorAs(t, validateClips(clips, dur), &ve)
		})
	}
	// Até 1s além do fim passa: a duração do ffprobe arredonda.
	require.NoError(t, validateClips([]ClipSpec{{From: 0, To: 60.8}, {From: 30, To: 31}}, dur))
}

func TestClipArgs_FadeNuncaPassaDeUmQuartoDoTrecho(t *testing.T) {
	args := strings.Join(clipArgs("in.m4a", ClipSpec{From: 1, To: 1.8}, true), " ")
	require.Contains(t, args, "-ss 1.000 -t 0.800")
	require.Contains(t, args, "afade=t=in:st=0:d=0.200,afade=t=out:st=0.600:d=0.200")
	require.NotContains(t, strings.Join(clipArgs("in.m4a", ClipSpec{From: 1, To: 9}, false), " "), "afade")
}

func TestClipFileNames_UnicosESeguros(t *testing.T) {
	got := clipFileNames([]ClipSpec{{Name: "Entrevista: bloco 1"}, {Name: "entrevista: bloco 1"}, {Name: "  "}, {Name: "a/b"}})
	require.Equal(t, []string{"Entrevista- bloco 1.mp3", "entrevista- bloco 1 (2).mp3", "trecho-03.mp3", "a-b.mp3"}, got)
}

// Com ffmpeg de verdade: forma de onda, corte em MP3 e zip, a partir de um .m4a
// de 4s guardado no bucket falso.
func TestEditor_ComFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg não instalado")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.m4a")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4", "-c:a", "aac", "-movflags", "+faststart", src)
	require.NoError(t, gen.Run())
	audio, err := os.ReadFile(src)
	require.NoError(t, err)

	id := uuid.New()
	key := "recordings/2026/11/" + id.String() + ".m4a"
	repo := newFakeRepo(Recording{ID: id, Status: StatusConcluida, ObjectKey: &key, HasAudio: true})
	objects := newFakeObjects()
	require.NoError(t, objects.Put(context.Background(), key, bytes.NewReader(audio), "audio/mp4"))
	m := newTestManager(t, repo, FFmpeg{}, objects, &fakeMailer{})
	ctx := context.Background()

	peaks, err := m.Peaks(ctx, id)
	require.NoError(t, err)
	require.InDelta(t, 4*PeaksPerSecond, len(peaks), 2)
	// O seno do lavfi tem amplitude 1/8: pico ~32 de 255.
	require.InDelta(t, 32, int(peaks[len(peaks)/2]), 4)
	_, stored := objects.puts[PeaksKey(key)]
	require.True(t, stored, "peaks guardado no bucket")

	// Segunda chamada vem do bucket: sem cópia local, não haveria como gerar.
	require.NoError(t, os.Remove(m.editorCachePath(id)))
	again, err := m.Peaks(ctx, id)
	require.NoError(t, err)
	require.Equal(t, peaks, again)

	var mp3 bytes.Buffer
	require.NoError(t, m.Clip(ctx, id, ClipSpec{From: 1, To: 2.5}, true, func(*Recording) io.Writer { return &mp3 }))
	clip := filepath.Join(dir, "clip.mp3")
	require.NoError(t, os.WriteFile(clip, mp3.Bytes(), 0o644))
	d, err := FFmpeg{}.Duration(ctx, clip)
	require.NoError(t, err)
	require.InDelta(t, 1.5, d.Seconds(), 0.1)

	// Erro de validação acontece antes de qualquer byte: o handler ainda devolve 400.
	called := false
	var ve *ValidationError
	err = m.Clip(ctx, id, ClipSpec{From: 3, To: 9}, false, func(*Recording) io.Writer { called = true; return io.Discard })
	require.ErrorAs(t, err, &ve)
	require.False(t, called)

	var zbuf bytes.Buffer
	require.NoError(t, m.Clips(ctx, id, []ClipSpec{{Name: "a", From: 0, To: 1}, {Name: "a", From: 2, To: 3}}, false,
		func(*Recording) io.Writer { return &zbuf }))
	zr, err := zip.NewReader(bytes.NewReader(zbuf.Bytes()), int64(zbuf.Len()))
	require.NoError(t, err)
	require.Len(t, zr.File, 2)
	require.Equal(t, "a.mp3", zr.File[0].Name)
	require.Equal(t, "a (2).mp3", zr.File[1].Name)
	require.Positive(t, zr.File[1].UncompressedSize64)

	// Excluir leva a cópia local e a forma de onda junto.
	require.NoError(t, m.Delete(ctx, id))
	_, err = os.Stat(m.editorCachePath(id))
	require.True(t, errors.Is(err, os.ErrNotExist))
	require.Contains(t, objects.deleted, PeaksKey(key))
}

func TestEditor_SemAudio(t *testing.T) {
	id := uuid.New()
	m := newTestManager(t, newFakeRepo(Recording{ID: id, Status: StatusFalhou}), &fakeMedia{}, newFakeObjects(), &fakeMailer{})
	_, err := m.Peaks(context.Background(), id)
	require.ErrorIs(t, err, ErrNoAudio)
}

func TestPruneEditorCache_ApagaSoOQueEstaSemUso(t *testing.T) {
	m := newTestManager(t, newFakeRepo(), &fakeMedia{}, newFakeObjects(), &fakeMailer{})
	require.NoError(t, os.MkdirAll(m.editorDir(), 0o755))
	old := filepath.Join(m.editorDir(), "velho.m4a")
	fresh := filepath.Join(m.editorDir(), "novo.m4a")
	require.NoError(t, os.WriteFile(old, []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(fresh, []byte("x"), 0o644))
	past := time.Now().Add(-2 * EditorCacheTTL)
	require.NoError(t, os.Chtimes(old, past, past))

	m.pruneEditorCache()
	_, err := os.Stat(old)
	require.True(t, errors.Is(err, os.ErrNotExist))
	_, err = os.Stat(fresh)
	require.NoError(t, err)
}
