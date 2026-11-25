package similarity

// Infra de teste do pacote similarity (cobertura TCC). Tudo aqui é prefixado
// covM e só existe em _test.go:
//   - covMNATS: servidor NATS mínimo em TCP local (INFO/CONNECT/PING/SUB/PUB),
//     o bastante pro nats.go real assinar e publicar sem rede externa;
//   - covMInstallFakeFFmpeg: "ffmpeg" falso (o próprio binário de teste
//     re-executado, ver TestMain) que devolve PCM f32le de um arquivo, em vez de
//     rodar o ffmpeg de verdade;
//   - covMDB / covMSeed*: banco de teste + material/hashes sintéticos.

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/db"
)

// ───────────────────────── NATS falso ─────────────────────────

type covMNATS struct {
	ln   net.Listener
	mu   sync.Mutex
	subs map[string][]covMSub // subject -> assinantes
}

type covMSub struct {
	conn net.Conn
	sid  string
}

func covMNewNATS(t *testing.T) *covMNATS {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &covMNATS{ln: ln, subs: map[string][]covMSub{}}
	go s.accept()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *covMNATS) URL() string { return "nats://" + s.ln.Addr().String() }

func (s *covMNATS) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.serve(c)
	}
}

func (s *covMNATS) serve(c net.Conn) {
	defer c.Close()
	_, _ = fmt.Fprintf(c, "INFO {\"server_id\":\"covm\",\"version\":\"2.10.0\",\"proto\":1,\"headers\":true,\"max_payload\":1048576}\r\n")
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) == 0 {
			continue
		}
		switch strings.ToUpper(f[0]) {
		case "PING":
			s.write(c, "PONG\r\n")
		case "SUB":
			// SUB subject [queue] sid
			subj, sid := f[1], f[len(f)-1]
			s.mu.Lock()
			s.subs[subj] = append(s.subs[subj], covMSub{conn: c, sid: sid})
			s.mu.Unlock()
		case "UNSUB":
			sid := f[1]
			s.mu.Lock()
			for subj, l := range s.subs {
				var keep []covMSub
				for _, x := range l {
					if !(x.conn == c && x.sid == sid) {
						keep = append(keep, x)
					}
				}
				s.subs[subj] = keep
			}
			s.mu.Unlock()
		case "PUB":
			// PUB subject [reply] size
			n, _ := strconv.Atoi(f[len(f)-1])
			buf := make([]byte, n+2)
			if _, err := io.ReadFull(r, buf); err != nil {
				return
			}
			s.deliver(f[1], buf[:n], 0)
		case "HPUB":
			// HPUB subject [reply] hdrLen totalLen
			hl, _ := strconv.Atoi(f[len(f)-2])
			tl, _ := strconv.Atoi(f[len(f)-1])
			buf := make([]byte, tl+2)
			if _, err := io.ReadFull(r, buf); err != nil {
				return
			}
			s.deliver(f[1], buf[:tl], hl)
		}
	}
}

func (s *covMNATS) write(c net.Conn, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = c.Write([]byte(msg))
}

func (s *covMNATS) deliver(subject string, payload []byte, hdrLen int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sub := range s.subs[subject] {
		var out []byte
		if hdrLen > 0 {
			out = append(out, []byte(fmt.Sprintf("HMSG %s %s %d %d\r\n", subject, sub.sid, hdrLen, len(payload)))...)
		} else {
			out = append(out, []byte(fmt.Sprintf("MSG %s %s %d\r\n", subject, sub.sid, len(payload)))...)
		}
		out = append(out, payload...)
		out = append(out, '\r', '\n')
		_, _ = sub.conn.Write(out)
	}
}

// covMConnect abre uma conexão NATS real contra o servidor falso.
func covMConnect(t *testing.T, s *covMNATS) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(s.URL(), nats.NoReconnect(), nats.Timeout(5*time.Second))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	return nc
}

// ───────────────────────── ffmpeg falso ─────────────────────────

const (
	covMEnvFake    = "COV_M_FAKE_FFMPEG"
	covMEnvFail    = "COV_M_FAKE_FAIL"
	covMEnvPCMFile = "COV_M_FAKE_PCM_FILE"
)

var (
	covMBinOnce sync.Once
	covMBinDir  string
)

// covMRunFakeFFmpeg é o "ffmpeg": roda no processo filho (binário de teste
// re-executado com covMEnvFake=1). Nunca decodifica áudio de verdade.
func covMRunFakeFFmpeg() {
	args := os.Args[1:]
	_, _ = io.Copy(io.Discard, os.Stdin)
	if os.Getenv(covMEnvFail) == "1" {
		fmt.Fprintln(os.Stderr, "fake ffmpeg: falha forçada")
		os.Exit(1)
	}
	if len(args) == 0 {
		os.Exit(0)
	}
	last := args[len(args)-1]
	if last == "-" || last == "pipe:1" {
		src := os.Getenv(covMEnvPCMFile)
		for i, a := range args {
			if a == "-i" && i+1 < len(args) && args[i+1] != "pipe:0" {
				if _, err := os.Stat(args[i+1]); err == nil {
					src = args[i+1]
				}
			}
		}
		if src != "" {
			if b, err := os.ReadFile(src); err == nil {
				_, _ = os.Stdout.Write(b)
			}
		}
		os.Exit(0)
	}
	_ = os.WriteFile(last, []byte("fake-ffmpeg-output"), 0o644)
	os.Exit(0)
}

// covMInstallFakeFFmpeg põe um "ffmpeg" falso como ÚNICO executável no PATH
// deste teste (nunca roda o ffmpeg real).
func covMInstallFakeFFmpeg(t *testing.T) {
	t.Helper()
	covMBinOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		dir, err := os.MkdirTemp("", "covm-ffmpeg-")
		if err != nil {
			return
		}
		name := "ffmpeg"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		src, err := os.Open(exe)
		if err != nil {
			return
		}
		defer src.Close()
		dst, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY, 0o755)
		if err != nil {
			return
		}
		defer dst.Close()
		if _, err := io.Copy(dst, src); err != nil {
			return
		}
		covMBinDir = dir
	})
	require.NotEmpty(t, covMBinDir, "falha ao instalar o ffmpeg falso")
	t.Setenv("PATH", covMBinDir)
	t.Setenv(covMEnvFake, "1")
}

// covMNoFFmpeg zera o PATH: qualquer exec de "ffmpeg" falha com "not found".
func covMNoFFmpeg(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// covMWritePCM grava PCM float32 little-endian num arquivo (o "master" de mentira).
func covMWritePCM(t *testing.T, name string, pcm []float32) string {
	t.Helper()
	buf := make([]byte, len(pcm)*4)
	for i, v := range pcm {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, buf, 0o644))
	return p
}

// ───────────────────────── banco de teste ─────────────────────────

func covMDB(t *testing.T) (context.Context, *pgxpool.Pool) {
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

func covMSeedClient(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ($1) RETURNING id`, "Cliente Teste "+uuid.NewString()[:8]).Scan(&id))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM materials WHERE client_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id)
	})
	return id
}

// covMSeedMaterial cria um material do cliente. hashes (pode ser nil) vão pra
// fingerprint_hashes (variant 0, rate 0).
func covMSeedMaterial(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clientID uuid.UUID,
	title string, durationSec float64, status, masterPath string, hashesPCM []float32) (uuid.UUID, int32) {
	t.Helper()
	var id uuid.UUID
	var short int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO materials (client_id, title, duration_seconds, master_storage_path,
		                       master_sha256, fingerprint_status)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, short_id`,
		clientID, title, durationSec, masterPath, "covm-"+uuid.NewString(), status).Scan(&id, &short))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM fingerprint_hashes WHERE commercial_id = $1`, id)
	})
	if hashesPCM != nil {
		covMInsertHashes(t, ctx, pool, id, hashesPCM, 0)
	}
	return id, short
}

func covMInsertHashes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, pcm []float32, variant int16) {
	t.Helper()
	hs := denseHashes(pcm)
	vals := make([]int64, 0, len(hs))
	frames := make([]int32, 0, len(hs))
	for _, h := range hs {
		vals = append(vals, int64(h.Value))
		frames = append(frames, int32(h.TimeFrame))
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO fingerprint_hashes (commercial_id, variant_id, rate_id, hash_value, time_frame)
		SELECT $1, $2, 0, u.h, u.t FROM unnest($3::bigint[], $4::int[]) AS u(h, t)
		ON CONFLICT DO NOTHING`, id, variant, vals, frames)
	require.NoError(t, err)
}

// covMNoise cacheia ruído denso determinístico por (seed, segundos).
var (
	covMNoiseMu    sync.Mutex
	covMNoiseCache = map[string][]float32{}
)

func covMNoise(seed int64, seconds int) []float32 {
	key := fmt.Sprintf("%d/%d", seed, seconds)
	covMNoiseMu.Lock()
	defer covMNoiseMu.Unlock()
	if v, ok := covMNoiseCache[key]; ok {
		return v
	}
	v := makeDenseNoise(seed, seconds)
	covMNoiseCache[key] = v
	return v
}

// covMEventually espera cond virar verdadeira com margem larga (sem sleeps apertados).
func covMEventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	require.Eventually(t, cond, 20*time.Second, 25*time.Millisecond, msg)
}
