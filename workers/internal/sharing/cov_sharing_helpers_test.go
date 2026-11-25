package sharing

// Infra de teste do pacote sharing (cobertura TCC). Tudo aqui é prefixado
// covH e só existe em _test.go:
//   - covHNATS: servidor NATS mínimo em TCP local (INFO/CONNECT/PING/SUB/PUB),
//     o bastante pro nats.go real assinar e publicar sem rede externa;
//   - covHInstallFakeFFmpeg: "ffmpeg" falso (o próprio binário de teste
//     re-executado, ver TestMain) que devolve PCM f32le de um arquivo, em vez de
//     rodar o ffmpeg de verdade;
//   - covHDB / covHSeed*: banco de teste + material/hashes sintéticos.

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
	"radiocheck/internal/index"
)

// ───────────────────────── NATS falso ─────────────────────────

type covHNATS struct {
	ln   net.Listener
	mu   sync.Mutex
	subs map[string][]covHSub // subject -> assinantes
}

type covHSub struct {
	conn net.Conn
	sid  string
}

func covHNewNATS(t *testing.T) *covHNATS {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &covHNATS{ln: ln, subs: map[string][]covHSub{}}
	go s.accept()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *covHNATS) URL() string { return "nats://" + s.ln.Addr().String() }

func (s *covHNATS) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.serve(c)
	}
}

func (s *covHNATS) serve(c net.Conn) {
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
			s.subs[subj] = append(s.subs[subj], covHSub{conn: c, sid: sid})
			s.mu.Unlock()
		case "UNSUB":
			sid := f[1]
			s.mu.Lock()
			for subj, l := range s.subs {
				var keep []covHSub
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

func (s *covHNATS) write(c net.Conn, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = c.Write([]byte(msg))
}

func (s *covHNATS) deliver(subject string, payload []byte, hdrLen int) {
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

// covHConnect abre uma conexão NATS real contra o servidor falso.
func covHConnect(t *testing.T, s *covHNATS) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(s.URL(), nats.NoReconnect(), nats.Timeout(5*time.Second))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	return nc
}

// ───────────────────────── ffmpeg falso ─────────────────────────

const (
	covHEnvFake    = "COV_H_FAKE_FFMPEG"
	covHEnvFail    = "COV_H_FAKE_FAIL"
	covHEnvPCMFile = "COV_H_FAKE_PCM_FILE"
)

var (
	covHBinOnce sync.Once
	covHBinDir  string
)

// covHRunFakeFFmpeg é o "ffmpeg": roda no processo filho (binário de teste
// re-executado com covHEnvFake=1). Nunca decodifica áudio de verdade.
func covHRunFakeFFmpeg() {
	args := os.Args[1:]
	_, _ = io.Copy(io.Discard, os.Stdin)
	if os.Getenv(covHEnvFail) == "1" {
		fmt.Fprintln(os.Stderr, "fake ffmpeg: falha forçada")
		os.Exit(1)
	}
	if len(args) == 0 {
		os.Exit(0)
	}
	last := args[len(args)-1]
	if last == "-" || last == "pipe:1" {
		src := os.Getenv(covHEnvPCMFile)
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

// covHInstallFakeFFmpeg põe um "ffmpeg" falso como ÚNICO executável no PATH
// deste teste (nunca roda o ffmpeg real).
func covHInstallFakeFFmpeg(t *testing.T) {
	t.Helper()
	covHBinOnce.Do(func() {
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
		covHBinDir = dir
	})
	require.NotEmpty(t, covHBinDir, "falha ao instalar o ffmpeg falso")
	t.Setenv("PATH", covHBinDir)
	t.Setenv(covHEnvFake, "1")
}

// covHNoFFmpeg zera o PATH: qualquer exec de "ffmpeg" falha com "not found".
func covHNoFFmpeg(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// covHWritePCM grava PCM float32 little-endian num arquivo (o "master" de mentira).
func covHWritePCM(t *testing.T, name string, pcm []float32) string {
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

func covHDB(t *testing.T) (context.Context, *pgxpool.Pool) {
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

func covHSeedClient(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
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

// covHSeedMaterial cria um material do cliente. hashes (pode ser nil) vão pra
// fingerprint_hashes (variant 0, rate 0).
func covHSeedMaterial(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clientID uuid.UUID,
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
		covHInsertHashes(t, ctx, pool, id, hashesPCM, 0)
	}
	return id, short
}

func covHInsertHashes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, pcm []float32, variant int16) {
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

// covHNoise cacheia ruído denso determinístico por (seed, segundos).
var (
	covHNoiseMu    sync.Mutex
	covHNoiseCache = map[string][]float32{}
)

func covHNoise(seed int64, seconds int) []float32 {
	key := fmt.Sprintf("%d/%d", seed, seconds)
	covHNoiseMu.Lock()
	defer covHNoiseMu.Unlock()
	if v, ok := covHNoiseCache[key]; ok {
		return v
	}
	v := makeDenseNoise(seed, seconds)
	covHNoiseCache[key] = v
	return v
}

// covHEventually espera cond virar verdadeira com margem larga (sem sleeps apertados).
func covHEventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	require.Eventually(t, cond, 20*time.Second, 25*time.Millisecond, msg)
}

// covHSeedCommercial cria campanha + commercial legado do cliente, com hashes
// do PCM informado (variant 0). Devolve (commercial_id, short_id).
func covHSeedCommercial(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clientID uuid.UUID,
	title string, durationSec float64, status, masterPath string, hashesPCM []float32) (uuid.UUID, int32) {
	t.Helper()
	var campID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'Campanha Teste', '2026-06-01', '2026-07-31', 'ativa') RETURNING id`,
		clientID).Scan(&campID))
	var id uuid.UUID
	var short int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO commercials (campaign_id, title, duration_seconds, master_storage_path,
		                         master_sha256, fingerprint_status)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, short_id`,
		campID, title, durationSec, masterPath, "covh-"+uuid.NewString(), status).Scan(&id, &short))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM fingerprint_hashes WHERE commercial_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM commercials WHERE id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, campID)
	})
	if hashesPCM != nil {
		covHInsertHashes(t, ctx, pool, id, hashesPCM, 0)
	}
	return id, short
}

// covHSharedCount devolve (total, compartilhados) de fingerprint_hashes do id.
func covHSharedCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (total, shared int) {
	t.Helper()
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE is_shared) FROM fingerprint_hashes WHERE commercial_id = $1`,
		id).Scan(&total, &shared))
	return total, shared
}

// covHStingPair monta dois PCMs de 30s que compartilham só um trecho curto
// (sting): A recebe, entre 8s e 12s, o áudio de X entre 8s e 12s. Cobertura
// ~1/3 de cada lado — abaixo do SubsetThreshold (0.5).
func covHStingPair() (x, a []float32) {
	const sr = 16000
	x = append([]float32(nil), covHNoise(71, 30)...)
	a = append([]float32(nil), covHNoise(72, 30)...)
	copy(a[8*sr:12*sr], x[8*sr:12*sr])
	return x, a
}

// covHStingPairAtStart é como covHStingPair mas o trecho compartilhado fica nos
// primeiros 4s dos dois áudios. Nessa posição a posição do trecho em X não
// depende do deslocamento de janela (ver BUG em sharing.go:296), então o teste
// de flagging fica estável.
func covHStingPairAtStart() (x, a []float32) {
	const sr = 16000
	x = append([]float32(nil), covHNoise(71, 30)...)
	a = append([]float32(nil), covHNoise(72, 30)...)
	copy(a[0:4*sr], x[0:4*sr])
	return x, a
}

// covHStoreFor monta um index.Store com os hashes de pcm sob o short_id dado.
func covHStoreFor(pcm []float32, short int32) *index.Store {
	st := index.New()
	st.Swap(indexFromHashes(denseHashes(pcm), short))
	return st
}
