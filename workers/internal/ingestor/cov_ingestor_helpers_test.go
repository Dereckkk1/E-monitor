package ingestor

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"radiocheck/internal/index"
	"radiocheck/pkg/audio"
	"radiocheck/pkg/ringbuffer"
)

// ── ffmpeg/ffprobe falsos ───────────────────────────────────────────────────
//
// Os testes NUNCA executam o ffmpeg/ffprobe de verdade (rede + CPU). Em vez
// disso, o próprio binário de teste é copiado como "ffprobe" e "ffmpeg" para um
// diretório temporário que vira o PATH do teste. Quando o binário é chamado
// com esse nome (e COV_I_FAKE_ACTIVE=1), o TestMain abaixo o transforma num
// stand-in determinístico: grava os argumentos recebidos, imprime o JSON do
// ffprobe ou escreve PCM no fd 3 (a "pipe:3" do ffmpeg). Assim o código de
// produção exercita o exec.Command real — montagem de argumentos, pipes,
// parse da saída — sem nenhum processo externo.

const covIFakeActive = "COV_I_FAKE_ACTIVE"

var (
	covIToolsDir  string
	covIToolsOnce sync.Once
)

func TestMain(m *testing.M) {
	if os.Getenv(covIFakeActive) == "1" {
		name := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
		switch name {
		case "ffprobe":
			covIFakeFFprobe()
			os.Exit(0)
		case "ffmpeg":
			covIFakeFFmpeg()
			os.Exit(0)
		}
	}
	code := m.Run()
	if covIToolsDir != "" {
		_ = os.RemoveAll(covIToolsDir)
	}
	os.Exit(code)
}

func covIRecordArgs(tool string) {
	dir := os.Getenv("COV_I_ARGS_DIR")
	if dir == "" {
		return
	}
	b, _ := json.Marshal(os.Args[1:])
	_ = os.WriteFile(filepath.Join(dir, tool+".args.json"), b, 0o644)
}

func covIFakeFFprobe() {
	covIRecordArgs("ffprobe")
	_, _ = io.WriteString(os.Stdout, os.Getenv("COV_I_FFPROBE_OUT"))
	if code, err := strconv.Atoi(os.Getenv("COV_I_FFPROBE_EXIT")); err == nil && code != 0 {
		os.Exit(code)
	}
}

func covIFakeFFmpeg() {
	covIRecordArgs("ffmpeg")
	if n, err := strconv.Atoi(os.Getenv("COV_I_FFMPEG_PCM_BYTES")); err == nil && n > 0 {
		if pipe := os.NewFile(3, "pipe3"); pipe != nil {
			_, _ = pipe.Write(make([]byte, n)) // PCM float32 "silêncio"
			_ = pipe.Close()
		}
	}
	if os.Getenv("COV_I_FFMPEG_HANG") == "1" {
		time.Sleep(10 * time.Minute) // até o Stop() matar o processo
	}
	if code, err := strconv.Atoi(os.Getenv("COV_I_FFMPEG_EXIT")); err == nil && code != 0 {
		os.Exit(code)
	}
}

// covIInstallTools copia o binário de teste como ffprobe/ffmpeg (uma vez por
// processo) e devolve o diretório.
func covIInstallTools(t *testing.T) string {
	t.Helper()
	covIToolsOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cov-i-tools-")
		require.NoError(t, err)
		exe, err := os.Executable()
		require.NoError(t, err)
		src, err := os.ReadFile(exe)
		require.NoError(t, err)
		ext := ""
		if os.PathSeparator == '\\' {
			ext = ".exe"
		}
		for _, name := range []string{"ffprobe", "ffmpeg"} {
			require.NoError(t, os.WriteFile(filepath.Join(dir, name+ext), src, 0o755))
		}
		covIToolsDir = dir
	})
	return covIToolsDir
}

// covIUseFakeTools põe os falsos no PATH (somente eles) e devolve o diretório
// onde cada um grava seus argumentos (<tool>.args.json).
func covIUseFakeTools(t *testing.T) (argsDir string) {
	t.Helper()
	dir := covIInstallTools(t)
	argsDir = t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv(covIFakeActive, "1")
	t.Setenv("COV_I_ARGS_DIR", argsDir)
	return argsDir
}

// covINoTools deixa o PATH sem ffmpeg/ffprobe: o exec falha na hora e de forma
// idêntica em qualquer máquina (CI com ou sem ffmpeg instalado).
func covINoTools(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func covIReadArgs(t *testing.T, argsDir, tool string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(argsDir, tool+".args.json"))
	require.NoError(t, err, "o %s falso deveria ter sido executado", tool)
	var args []string
	require.NoError(t, json.Unmarshal(b, &args))
	return args
}

// ── sinal sintético ─────────────────────────────────────────────────────────

// covISynthClip gera `seconds` de áudio 16 kHz "musical" e determinístico: a
// cada 125 ms troca o trio de tons, o que dá picos espectrais ricos e
// reproduzíveis (um seno puro gera poucos hashes distintos).
func covISynthClip(seed int64, seconds float64) []float32 {
	rng := rand.New(rand.NewSource(seed))
	n := int(seconds * 16000)
	out := make([]float32, n)
	const block = 2000
	for start := 0; start < n; start += block {
		var f [3]float64
		for i := range f {
			f[i] = 300 + rng.Float64()*3200
		}
		end := start + block
		if end > n {
			end = n
		}
		for i := start; i < end; i++ {
			tt := float64(i) / 16000
			v := math.Sin(2*math.Pi*f[0]*tt) + 0.8*math.Sin(2*math.Pi*f[1]*tt) + 0.6*math.Sin(2*math.Pi*f[2]*tt)
			out[i] = float32(v / 2.4)
		}
	}
	return out
}

// covINoise gera ruído branco de baixa amplitude (preenchimento do stream).
func covINoise(seed int64, samples int) []float32 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]float32, samples)
	for i := range out {
		out[i] = (rng.Float32()*2 - 1) * 0.02
	}
	return out
}

// covIStoreFromClip indexa o clipe (mesmo pré-processamento do matcher) sob o short_id.
func covIStoreFromClip(clip []float32, shortID int32) *index.Store {
	filtered := audio.ApplyHighPass(clip, 100.0, 16000)
	norm := audio.NormalizeRMS(filtered, -20.0)
	hashes := audio.GenerateHashes(audio.PickPeaks(audio.STFT(norm)))
	idx := make(index.Index)
	for _, h := range hashes {
		idx[h.Value] = append(idx[h.Value], index.Entry{CommercialShortID: shortID, TimeFrame: int32(h.TimeFrame)})
	}
	store := index.New()
	store.Swap(idx)
	return store
}

// covIPCMBytes serializa amostras como float32 little-endian (formato do pipe:3).
func covIPCMBytes(samples []float32) []byte {
	out := make([]byte, len(samples)*4)
	for i, s := range samples {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(s))
	}
	return out
}

// covIPadToChunks completa com zeros até múltiplo de 4096 amostras (o leitor
// descarta o último bloco parcial por causa do io.ReadFull).
func covIPadToChunks(samples []float32) []float32 {
	const chunk = 4096
	if rem := len(samples) % chunk; rem != 0 {
		samples = append(samples, make([]float32, chunk-rem)...)
	}
	return samples
}

// covINewRing devolve o anel de PCM com a mesma capacidade que Worker.Run usa (35s).
func covINewRing() *ringbuffer.PCMRing { return ringbuffer.NewPCMRing(16000 * 35) }
