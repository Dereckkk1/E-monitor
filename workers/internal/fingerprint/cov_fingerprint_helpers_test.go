package fingerprint

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/db"
)

// ── ffmpeg falso ─────────────────────────────────────────────────────────────
//
// Os testes NUNCA rodam o ffmpeg de verdade. O proprio binario de teste e
// copiado como "ffmpeg" para um diretorio temporario que entra na frente do
// PATH; quando chamado com esse nome (e COV_Z_FAKE_ACTIVE=1) o TestMain o
// transforma num stand-in deterministico que registra os argumentos e devolve
// PCM f32le de um arquivo. O codigo de producao exercita o exec.Command real
// (montagem de argumentos, pipes, parse da saida) sem processo externo.

const covZFakeActive = "COV_Z_FAKE_ACTIVE"

var (
	covZToolsDir  string
	covZToolsOnce sync.Once
)

func TestMain(m *testing.M) {
	if os.Getenv(covZFakeActive) == "1" {
		name := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
		if name == "ffmpeg" {
			covZFakeFFmpeg()
			os.Exit(0)
		}
	}
	code := m.Run()
	if covZToolsDir != "" {
		_ = os.RemoveAll(covZToolsDir)
	}
	os.Exit(code)
}

func covZFakeFFmpeg() {
	if f := os.Getenv("COV_Z_ARGS_FILE"); f != "" {
		b, _ := json.Marshal(os.Args[1:])
		_ = os.WriteFile(f, b, 0o644)
	}
	switch os.Getenv("COV_Z_MODE") {
	case "fail":
		_, _ = os.Stderr.WriteString("boom: invalid data found when processing input")
		os.Exit(1)
	case "empty":
		return
	case "unaligned":
		_, _ = os.Stdout.Write([]byte{1, 2, 3, 4, 5})
		return
	}
	if f := os.Getenv("COV_Z_PCM_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			_, _ = os.Stderr.WriteString("fake ffmpeg: " + err.Error())
			os.Exit(2)
		}
		_, _ = os.Stdout.Write(b)
	}
}

func covZInstallFFmpeg(t *testing.T) string {
	t.Helper()
	covZToolsOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cov-z-tools-")
		require.NoError(t, err)
		exe, err := os.Executable()
		require.NoError(t, err)
		src, err := os.ReadFile(exe)
		require.NoError(t, err)
		ext := ""
		if os.PathSeparator == '\\' {
			ext = ".exe"
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, "ffmpeg"+ext), src, 0o755))
		covZToolsDir = dir
	})
	return covZToolsDir
}

// covZFakeFFmpegEnv instala o ffmpeg falso na frente do PATH e configura o modo.
// pcmFile vazio nao escreve PCM. Devolve o caminho do arquivo onde os argumentos
// recebidos sao gravados (JSON).
func covZFakeFFmpegEnv(t *testing.T, mode, pcmFile string) (argsFile string) {
	t.Helper()
	dir := covZInstallFFmpeg(t)
	argsFile = filepath.Join(t.TempDir(), "args.json")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(covZFakeActive, "1")
	t.Setenv("COV_Z_MODE", mode)
	t.Setenv("COV_Z_PCM_FILE", pcmFile)
	t.Setenv("COV_Z_ARGS_FILE", argsFile)
	return argsFile
}

func covZReadArgs(t *testing.T, argsFile string) []string {
	t.Helper()
	b, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	var args []string
	require.NoError(t, json.Unmarshal(b, &args))
	return args
}

// ── sinal sintetico ──────────────────────────────────────────────────────────

// covZSignal gera um sinal "musical" deterministico: a cada 100 ms, tres tons
// em frequencias sorteadas (seed fixa) mais um pouco de ruido. Tem picos
// espectrais suficientes para o pipeline gerar centenas de hashes.
func covZSignal(seed int64, seconds float64) []float32 {
	rng := rand.New(rand.NewSource(seed))
	n := int(seconds * SampleRate)
	out := make([]float32, n)
	block := SampleRate / 10
	for start := 0; start < n; start += block {
		freqs := [3]float64{
			300 + rng.Float64()*3700,
			300 + rng.Float64()*3700,
			300 + rng.Float64()*3700,
		}
		for i := start; i < start+block && i < n; i++ {
			var v float64
			for _, f := range freqs {
				v += 0.3 * math.Sin(2*math.Pi*f*float64(i)/SampleRate)
			}
			v += 0.02 * (rng.Float64()*2 - 1)
			out[i] = float32(v)
		}
	}
	return out
}

// covZWritePCM grava amostras como f32le em um arquivo temporario.
func covZWritePCM(t *testing.T, pcm []float32) string {
	t.Helper()
	buf := make([]byte, len(pcm)*4)
	for i, s := range pcm {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(s))
	}
	p := filepath.Join(t.TempDir(), "pcm.f32le")
	require.NoError(t, os.WriteFile(p, buf, 0o644))
	return p
}

// covZPool abre um pool contra TEST_DATABASE_URL (skip se ausente).
func covZPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return ctx, pool
}
