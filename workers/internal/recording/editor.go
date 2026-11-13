package recording

// Editor de trechos (/admin/gravacoes/:id/editar). Uma gravação chega a 6h e
// ~340 MB: decodificar isso no navegador custaria gigabytes de memória. Então o
// pesado fica aqui: a forma de onda reduzida (peaks) e o corte em MP3. O
// navegador só desenha, toca e manda os tempos.
//
// O arquivo é baixado do bucket uma vez para um cache local (<Root>/_editor) e
// reaproveitado pelos cortes seguintes: quem edita costuma exportar vários
// trechos da mesma gravação em sequência. O prune de hora em hora apaga cópias
// sem uso há mais de EditorCacheTTL.
//
// Ver docs/features/recording-clip-editor.md.

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"radiocheck/internal/storage"
)

const (
	// PeaksPerSecond é a resolução da forma de onda: um valor a cada 50 ms. 6h
	// dão 432 KB, e 50 ms basta para posicionar um corte de ouvido.
	PeaksPerSecond = 20
	peaksSampleHz  = 8000
	// MaxClips limita o .zip de uma vez.
	MaxClips = 50
	// MinClip evita trecho que nem dá para ouvir.
	MinClip = 500 * time.Millisecond
	// FadeLen é o fade nas pontas, para o corte não estalar.
	FadeLen = 300 * time.Millisecond
	// EditorCacheTTL é quanto a cópia local fica sem uso antes de o prune apagar.
	EditorCacheTTL = time.Hour
	// editorSlots é quantos ffmpeg do editor rodam ao mesmo tempo. A API também
	// grava (um ffmpeg reencodando por gravação), então o editor não pode tomar a CPU.
	editorSlots = 2
	// editorWait é quanto um pedido espera vaga antes de desistir com ErrEditorBusy.
	editorWait = 2 * time.Minute
)

// ErrEditorBusy: todas as vagas do editor ocupadas por mais de editorWait.
var ErrEditorBusy = errors.New("o editor está ocupado com outros cortes, tente de novo em instantes")

// ClipSpec é um trecho pedido pelo editor, em segundos desde o início do arquivo.
type ClipSpec struct {
	Name string  `json:"name"`
	From float64 `json:"from"`
	To   float64 `json:"to"`
}

type editorState struct {
	slots chan struct{}
	mu    sync.Mutex
	locks map[uuid.UUID]*sync.Mutex
}

func newEditorState() editorState {
	return editorState{slots: make(chan struct{}, editorSlots), locks: map[uuid.UUID]*sync.Mutex{}}
}

func (m *Manager) editorDir() string { return filepath.Join(m.cfg.Root, "_editor") }

func (m *Manager) editorCachePath(id uuid.UUID) string {
	return filepath.Join(m.editorDir(), id.String()+".m4a")
}

// PeaksKey é onde a forma de onda fica no bucket, ao lado do áudio.
func PeaksKey(objectKey string) string {
	return strings.TrimSuffix(objectKey, filepath.Ext(objectKey)) + ".peaks"
}

// idLock serializa download e geração de peaks por gravação: dois cliques no
// mesmo segundo não baixam 340 MB duas vezes.
func (m *Manager) idLock(id uuid.UUID) *sync.Mutex {
	m.editor.mu.Lock()
	defer m.editor.mu.Unlock()
	l, ok := m.editor.locks[id]
	if !ok {
		l = &sync.Mutex{}
		m.editor.locks[id] = l
	}
	return l
}

func (m *Manager) acquireSlot(ctx context.Context) (func(), error) {
	t := time.NewTimer(editorWait)
	defer t.Stop()
	select {
	case m.editor.slots <- struct{}{}:
		return func() { <-m.editor.slots }, nil
	case <-t.C:
		return nil, ErrEditorBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// editable devolve a gravação se ela tem áudio para editar.
func (m *Manager) editable(ctx context.Context, id uuid.UUID) (*Recording, error) {
	rec, err := m.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec.ObjectKey == nil {
		return nil, ErrNoAudio
	}
	return rec, nil
}

// ensureCached garante a cópia local do áudio e devolve o caminho. Renova o mtime,
// que é o relógio do prune.
func (m *Manager) ensureCached(ctx context.Context, rec *Recording) (string, error) {
	l := m.idLock(rec.ID)
	l.Lock()
	defer l.Unlock()

	path := m.editorCachePath(rec.ID)
	now := m.now()
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		_ = os.Chtimes(path, now, now)
		return path, nil
	}
	if err := os.MkdirAll(m.editorDir(), 0o755); err != nil {
		return "", fmt.Errorf("editor: criar cache: %w", err)
	}
	body, _, _, err := m.objects.Get(ctx, *rec.ObjectKey)
	if storage.IsNotFound(err) {
		return "", ErrNoAudio
	}
	if err != nil {
		return "", fmt.Errorf("editor: baixar áudio: %w", err)
	}
	defer body.Close()
	tmp, err := os.CreateTemp(m.editorDir(), rec.ID.String()+"-*.part")
	if err != nil {
		return "", fmt.Errorf("editor: criar cache: %w", err)
	}
	_, copyErr := io.Copy(tmp, body)
	closeErr := tmp.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("editor: baixar áudio: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("editor: cache: %w", err)
	}
	return path, nil
}

// Peaks devolve a forma de onda: um byte (0–255, pico absoluto) a cada
// 1/PeaksPerSecond segundo. Gera na primeira vez e guarda no bucket.
func (m *Manager) Peaks(ctx context.Context, id uuid.UUID) ([]byte, error) {
	rec, err := m.editable(ctx, id)
	if err != nil {
		return nil, err
	}
	key := PeaksKey(*rec.ObjectKey)
	if b, err := m.readObject(ctx, key); err == nil {
		return b, nil
	} else if !storage.IsNotFound(err) {
		m.log.Warn("recording: ler peaks", zap.Stringer("id", id), zap.Error(err))
	}

	// Gera sob o lock da gravação e confere de novo: outro pedido pode ter gerado
	// enquanto este esperava.
	path, err := m.ensureCached(ctx, rec)
	if err != nil {
		return nil, err
	}
	l := m.idLock(id)
	l.Lock()
	defer l.Unlock()
	if b, err := m.readObject(ctx, key); err == nil {
		return b, nil
	}
	release, err := m.acquireSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	peaks, err := computePeaks(ctx, path)
	if err != nil {
		return nil, err
	}
	// Falha ao guardar não impede de entregar: só gera de novo na próxima vez.
	if err := m.objects.Put(ctx, key, bytes.NewReader(peaks), "application/octet-stream"); err != nil {
		m.log.Warn("recording: guardar peaks", zap.Stringer("id", id), zap.Error(err))
	}
	return peaks, nil
}

func (m *Manager) readObject(ctx context.Context, key string) ([]byte, error) {
	body, _, _, err := m.objects.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(body)
}

// computePeaks decodifica para mono 8 kHz e tira o pico absoluto de cada janela.
// Lê o PCM em stream: 6h a 8 kHz seriam 345 MB se fossem para a memória.
func computePeaks(ctx context.Context, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", path, "-vn", "-ac", "1", "-ar", strconv.Itoa(peaksSampleHz), "-f", "s16le", "-")
	stderr := &tailWriter{}
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("peaks: %w", err)
	}
	peaks, readErr := peaksFromPCM(bufio.NewReaderSize(out, 64<<10), peaksSampleHz/PeaksPerSecond)
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("peaks: ffmpeg: %w: %s", err, cleanStderr(stderr.buf))
	}
	if readErr != nil {
		return nil, fmt.Errorf("peaks: %w", readErr)
	}
	return peaks, nil
}

// peaksFromPCM reduz PCM s16le mono a um byte por janela de `window` amostras.
// A última janela, incompleta, também entra.
func peaksFromPCM(r io.Reader, window int) ([]byte, error) {
	var out []byte
	var buf [2]byte
	peak, n := 0, 0
	for {
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, err
		}
		v := int(int16(binary.LittleEndian.Uint16(buf[:])))
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
		n++
		if n == window {
			out = append(out, scalePeak(peak))
			peak, n = 0, 0
		}
	}
	if n > 0 {
		out = append(out, scalePeak(peak))
	}
	return out, nil
}

func scalePeak(v int) byte {
	return byte(min(255, int(math.Round(float64(v)*255/32768))))
}

// validateClips confere os tempos contra a duração do arquivo. A duração vem do
// arquivo, não da janela: é o eixo do player que o editor usa.
func validateClips(clips []ClipSpec, dur time.Duration) error {
	if len(clips) == 0 {
		return &ValidationError{Msg: "nenhum trecho para exportar"}
	}
	if len(clips) > MaxClips {
		return &ValidationError{Msg: fmt.Sprintf("no máximo %d trechos de uma vez", MaxClips)}
	}
	total := dur.Seconds()
	for i, c := range clips {
		label := fmt.Sprintf("trecho %d", i+1)
		if strings.TrimSpace(c.Name) != "" {
			label = fmt.Sprintf("trecho %q", c.Name)
		}
		if math.IsNaN(c.From) || math.IsNaN(c.To) || math.IsInf(c.From, 0) || math.IsInf(c.To, 0) {
			return &ValidationError{Msg: label + ": tempo inválido"}
		}
		if c.From < 0 || c.To > total+1 {
			return &ValidationError{Msg: label + ": fora da gravação"}
		}
		if time.Duration((c.To-c.From)*float64(time.Second)) < MinClip {
			return &ValidationError{Msg: label + ": curto demais (mínimo meio segundo)"}
		}
	}
	return nil
}

// clipArgs monta o corte. -ss antes do -i busca rápido no .m4a (que tem índice
// por causa do +faststart) e, como há reencode, o corte é preciso na amostra.
func clipArgs(in string, c ClipSpec, fade bool) []string {
	dur := c.To - c.From
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin",
		"-ss", ffSeconds(c.From), "-t", ffSeconds(dur), "-i", in, "-vn"}
	if fade {
		f := math.Min(FadeLen.Seconds(), dur/4)
		args = append(args, "-af", fmt.Sprintf("afade=t=in:st=0:d=%s,afade=t=out:st=%s:d=%s",
			ffSeconds(f), ffSeconds(dur-f), ffSeconds(f)))
	}
	return append(args, "-c:a", "libmp3lame", "-b:a", "128k", "-ar", "44100", "-ac", "2", "-f", "mp3", "-")
}

func ffSeconds(s float64) string { return strconv.FormatFloat(s, 'f', 3, 64) }

func encodeClip(ctx context.Context, in string, c ClipSpec, fade bool, w io.Writer) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", clipArgs(in, c, fade)...)
	stderr := &tailWriter{}
	cmd.Stderr = stderr
	cmd.Stdout = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("corte: ffmpeg: %w: %s", err, cleanStderr(stderr.buf))
	}
	return nil
}

// prepareClips valida antes de escrever qualquer byte: depois do primeiro byte
// não dá mais para devolver 400.
func (m *Manager) prepareClips(ctx context.Context, id uuid.UUID, clips []ClipSpec) (string, *Recording, error) {
	rec, err := m.editable(ctx, id)
	if err != nil {
		return "", nil, err
	}
	path, err := m.ensureCached(ctx, rec)
	if err != nil {
		return "", nil, err
	}
	dur, err := m.media.Duration(ctx, path)
	if err != nil {
		return "", nil, err
	}
	if err := validateClips(clips, dur); err != nil {
		return "", nil, err
	}
	return path, rec, nil
}

// ClipWriter devolve onde escrever o resultado. Só é chamado depois da validação,
// para o handler escrever os headers de sucesso apenas quando não vai mais haver 400.
type ClipWriter func(rec *Recording) io.Writer

// Clip corta um trecho em MP3 e escreve em out(rec).
func (m *Manager) Clip(ctx context.Context, id uuid.UUID, c ClipSpec, fade bool, out ClipWriter) error {
	path, rec, err := m.prepareClips(ctx, id, []ClipSpec{c})
	if err != nil {
		return err
	}
	release, err := m.acquireSlot(ctx)
	if err != nil {
		return err
	}
	defer release()
	return encodeClip(ctx, path, c, fade, out(rec))
}

// Clips corta vários trechos e escreve um .zip em out(rec), um MP3 por trecho.
// MP3 já é comprimido: as entradas vão sem compressão (Store).
func (m *Manager) Clips(ctx context.Context, id uuid.UUID, clips []ClipSpec, fade bool, out ClipWriter) error {
	path, rec, err := m.prepareClips(ctx, id, clips)
	if err != nil {
		return err
	}
	release, err := m.acquireSlot(ctx)
	if err != nil {
		return err
	}
	defer release()
	zw := zip.NewWriter(out(rec))
	names := clipFileNames(clips)
	for i, c := range clips {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: names[i], Method: zip.Store, Modified: m.now()})
		if err != nil {
			return err
		}
		if err := encodeClip(ctx, path, c, fade, f); err != nil {
			return err
		}
	}
	return zw.Close()
}

var unsafeName = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]+`)

// SafeFileName tira do nome o que quebra arquivo no Windows ou no zip.
func SafeFileName(name string) string {
	s := strings.TrimSpace(unsafeName.ReplaceAllString(name, "-"))
	s = strings.Trim(s, ". ")
	if r := []rune(s); len(r) > 100 {
		s = string(r[:100])
	}
	return s
}

// clipFileNames dá um nome único por entrada do zip: dois trechos com o mesmo
// nome não podem se sobrescrever ao extrair.
func clipFileNames(clips []ClipSpec) []string {
	seen := map[string]int{}
	out := make([]string, len(clips))
	for i, c := range clips {
		base := SafeFileName(c.Name)
		if base == "" {
			base = fmt.Sprintf("trecho-%02d", i+1)
		}
		key := strings.ToLower(base)
		seen[key]++
		if n := seen[key]; n > 1 {
			base = fmt.Sprintf("%s (%d)", base, n)
		}
		out[i] = base + ".mp3"
	}
	return out
}

// dropEditorArtifacts apaga a cópia local e a forma de onda de uma gravação.
func (m *Manager) dropEditorArtifacts(ctx context.Context, rec *Recording) {
	os.Remove(m.editorCachePath(rec.ID))
	if rec.ObjectKey != nil {
		if err := m.objects.Delete(ctx, PeaksKey(*rec.ObjectKey)); err != nil && !storage.IsNotFound(err) {
			m.log.Warn("recording: apagar peaks", zap.Stringer("id", rec.ID), zap.Error(err))
		}
	}
}

// pruneEditorCache apaga cópias locais sem uso há mais de EditorCacheTTL e
// sobras de download interrompido.
func (m *Manager) pruneEditorCache() {
	entries, err := os.ReadDir(m.editorDir())
	if err != nil {
		return
	}
	cutoff := m.now().Add(-EditorCacheTTL)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(m.editorDir(), e.Name()))
		}
	}
}
