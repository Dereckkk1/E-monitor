package audit

import (
	"context"
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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/db"
	"radiocheck/pkg/audio"
)

const covZSampleRate = 16000

// ── ffmpeg falso ─────────────────────────────────────────────────────────────
//
// O ffmpeg de verdade nunca roda nos testes. O binario de teste e copiado como
// "ffmpeg" para um diretorio temporario na frente do PATH; chamado com esse nome
// (e COV_Z_FAKE_ACTIVE=1) o TestMain o transforma num stand-in: consome o stdin
// (registrando o tamanho), grava os argumentos e devolve PCM f32le de um arquivo.

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
	in, _ := io.ReadAll(os.Stdin)
	if f := os.Getenv("COV_Z_STDIN_LEN_FILE"); f != "" {
		_ = os.WriteFile(f, []byte(strconv.Itoa(len(in))), 0o644)
	}
	switch os.Getenv("COV_Z_MODE") {
	case "fail":
		_, _ = os.Stderr.WriteString("boom: invalid data found when processing input")
		os.Exit(1)
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
		dir, err := os.MkdirTemp("", "cov-z-audit-tools-")
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

type covZFFmpeg struct {
	argsFile     string
	stdinLenFile string
}

func covZFakeFFmpegEnv(t *testing.T, mode, pcmFile string) covZFFmpeg {
	t.Helper()
	dir := covZInstallFFmpeg(t)
	tmp := t.TempDir()
	f := covZFFmpeg{argsFile: filepath.Join(tmp, "args.json"), stdinLenFile: filepath.Join(tmp, "stdin.len")}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(covZFakeActive, "1")
	t.Setenv("COV_Z_MODE", mode)
	t.Setenv("COV_Z_PCM_FILE", pcmFile)
	t.Setenv("COV_Z_ARGS_FILE", f.argsFile)
	t.Setenv("COV_Z_STDIN_LEN_FILE", f.stdinLenFile)
	return f
}

func (f covZFFmpeg) args(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(f.argsFile)
	require.NoError(t, err)
	var a []string
	require.NoError(t, json.Unmarshal(b, &a))
	return a
}

func (f covZFFmpeg) stdinLen(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(f.stdinLenFile)
	require.NoError(t, err)
	n, err := strconv.Atoi(string(b))
	require.NoError(t, err)
	return n
}

// ── sinal sintetico ──────────────────────────────────────────────────────────

// covZSignal gera um sinal deterministico: a cada 100 ms, tres tons sorteados
// (seed fixa) mais ruido leve. Rico o bastante em picos para gerar muitos hashes.
func covZSignal(seed int64, seconds float64) []float32 {
	rng := rand.New(rand.NewSource(seed))
	n := int(seconds * covZSampleRate)
	out := make([]float32, n)
	block := covZSampleRate / 10
	for start := 0; start < n; start += block {
		freqs := [3]float64{
			300 + rng.Float64()*3700,
			300 + rng.Float64()*3700,
			300 + rng.Float64()*3700,
		}
		for i := start; i < start+block && i < n; i++ {
			var v float64
			for _, f := range freqs {
				v += 0.3 * math.Sin(2*math.Pi*f*float64(i)/covZSampleRate)
			}
			v += 0.02 * (rng.Float64()*2 - 1)
			out[i] = float32(v)
		}
	}
	return out
}

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

// ── banco ────────────────────────────────────────────────────────────────────

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

// covZLoadMaster grava os hashes do master em fingerprint_hashes para um id novo
// (sem FK) e devolve o id e a quantidade de linhas distintas gravadas.
func covZLoadMaster(t *testing.T, ctx context.Context, pool *pgxpool.Pool, hashes []audio.Hash, variant, rate int16, shared bool) (uuid.UUID, int) {
	t.Helper()
	id := uuid.New()
	type key struct {
		v  uint32
		tf int
	}
	seen := map[key]bool{}
	var rows [][]any
	for _, h := range hashes {
		k := key{h.Value, h.TimeFrame}
		if seen[k] {
			continue // PK (id, variant, rate, hash, frame) nao admite repeticao
		}
		seen[k] = true
		rows = append(rows, []any{id, variant, rate, int64(h.Value), int32(h.TimeFrame), shared})
	}
	_, err := pool.CopyFrom(ctx, pgx.Identifier{"fingerprint_hashes"},
		[]string{"commercial_id", "variant_id", "rate_id", "hash_value", "time_frame", "is_shared"},
		pgx.CopyFromRows(rows))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM fingerprint_hashes WHERE commercial_id=$1`, id)
	})
	return id, len(rows)
}
