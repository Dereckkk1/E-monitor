package evidence

// Infra de teste do pacote evidence (cobertura TCC), tudo prefixado covE:
//   - covENATS: servidor NATS mínimo em TCP local (nats.go real assina/publica);
//   - covEInstallFakeFFmpeg: "ffmpeg" falso (o próprio binário de teste
//     re-executado, ver TestMain) — decodifica PCM de um arquivo e "grava" saídas
//     fixas; o ffmpeg real nunca roda;
//   - covES3: S3 falso (httptest, path-style) com injeção de falha;
//   - covESeed*: banco de teste + cliente/campanha/material/hashes sintéticos.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
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
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"radiocheck/internal/audit"
	"radiocheck/internal/catalog"
	"radiocheck/internal/db"
	"radiocheck/internal/segments"
	"radiocheck/internal/storage"
)

// ───────────────────────── NATS falso ─────────────────────────

type covENATS struct {
	ln   net.Listener
	mu   sync.Mutex
	subs map[string][]covESub
}

type covESub struct {
	conn net.Conn
	sid  string
}

func covENewNATS(t *testing.T) *covENATS {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &covENATS{ln: ln, subs: map[string][]covESub{}}
	go s.accept()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *covENATS) URL() string { return "nats://" + s.ln.Addr().String() }

func (s *covENATS) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.serve(c)
	}
}

func (s *covENATS) serve(c net.Conn) {
	defer c.Close()
	_, _ = fmt.Fprintf(c, "INFO {\"server_id\":\"cove\",\"version\":\"2.10.0\",\"proto\":1,\"headers\":true,\"max_payload\":1048576}\r\n")
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
			subj, sid := f[1], f[len(f)-1]
			s.mu.Lock()
			s.subs[subj] = append(s.subs[subj], covESub{conn: c, sid: sid})
			s.mu.Unlock()
		case "UNSUB":
			sid := f[1]
			s.mu.Lock()
			for subj, l := range s.subs {
				var keep []covESub
				for _, x := range l {
					if !(x.conn == c && x.sid == sid) {
						keep = append(keep, x)
					}
				}
				s.subs[subj] = keep
			}
			s.mu.Unlock()
		case "PUB":
			n, _ := strconv.Atoi(f[len(f)-1])
			buf := make([]byte, n+2)
			if _, err := io.ReadFull(r, buf); err != nil {
				return
			}
			s.deliver(f[1], buf[:n], 0)
		case "HPUB":
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

func (s *covENATS) write(c net.Conn, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = c.Write([]byte(msg))
}

func (s *covENATS) deliver(subject string, payload []byte, hdrLen int) {
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

func covEConnect(t *testing.T, s *covENATS) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(s.URL(), nats.NoReconnect(), nats.Timeout(5*time.Second))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	return nc
}

// ───────────────────────── ffmpeg falso ─────────────────────────

const (
	covEEnvFake    = "COV_E_FAKE_FFMPEG"
	covEEnvPCMFile = "COV_E_FAKE_PCM_FILE" // PCM f32le devolvido nas decodificações (saída "-" ou "pipe:1")
	covEEnvFailArg = "COV_E_FAKE_FAIL_ARG" // falha (exit 1) quando algum argumento é igual a este valor
	covEEnvEmpty   = "COV_E_FAKE_EMPTY_OUT" // "1": grava saída vazia nos arquivos de saída
)

const covEFakeFFmpegOutput = "fake-ffmpeg-output"

var (
	covEBinOnce sync.Once
	covEBinDir  string
)

// covERunFakeFFmpeg é o "ffmpeg": roda no processo filho (binário de teste
// re-executado com covEEnvFake=1).
func covERunFakeFFmpeg() {
	args := os.Args[1:]
	_, _ = io.Copy(io.Discard, os.Stdin)
	if bad := os.Getenv(covEEnvFailArg); bad != "" {
		for _, a := range args {
			if a == bad {
				fmt.Fprintln(os.Stderr, "fake ffmpeg: falha forçada em "+bad)
				os.Exit(1)
			}
		}
	}
	if len(args) == 0 {
		os.Exit(0)
	}
	last := args[len(args)-1]
	if last == "-" || last == "pipe:1" {
		if src := os.Getenv(covEEnvPCMFile); src != "" {
			if b, err := os.ReadFile(src); err == nil {
				_, _ = os.Stdout.Write(b)
			}
		}
		os.Exit(0)
	}
	out := []byte(covEFakeFFmpegOutput)
	if os.Getenv(covEEnvEmpty) == "1" {
		out = nil
	}
	_ = os.WriteFile(last, out, 0o644)
	os.Exit(0)
}

// covEInstallFakeFFmpeg põe o "ffmpeg" falso como ÚNICO executável do PATH.
func covEInstallFakeFFmpeg(t *testing.T) {
	t.Helper()
	covEBinOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		dir, err := os.MkdirTemp("", "cove-ffmpeg-")
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
		covEBinDir = dir
	})
	require.NotEmpty(t, covEBinDir, "falha ao instalar o ffmpeg falso")
	t.Setenv("PATH", covEBinDir)
	t.Setenv(covEEnvFake, "1")
}

// covENoFFmpeg zera o PATH: qualquer exec de "ffmpeg" falha com "not found".
func covENoFFmpeg(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// covEPCMFile grava PCM float32 LE num arquivo e aponta o ffmpeg falso pra ele.
func covEServePCM(t *testing.T, pcm []float32) {
	t.Helper()
	buf := make([]byte, len(pcm)*4)
	for i, v := range pcm {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	p := filepath.Join(t.TempDir(), "clip.pcm")
	require.NoError(t, os.WriteFile(p, buf, 0o644))
	t.Setenv(covEEnvPCMFile, p)
}

// ───────────────────────── S3 falso ─────────────────────────

type covES3 struct {
	Srv *httptest.Server

	mu       sync.Mutex
	objs     map[string][]byte // "bucket/key" -> corpo
	types    map[string]string // "bucket/key" -> content-type
	hdrs     map[string]http.Header
	puts     int
	deletes  []string
	failPut  bool            // todo PUT responde 500
	failDel  map[string]bool // "bucket/key" -> DELETE responde 500
	failHead bool
}

func covENewS3(t *testing.T, tls bool) *covES3 {
	t.Helper()
	s := &covES3{objs: map[string][]byte{}, types: map[string]string{}, hdrs: map[string]http.Header{}, failDel: map[string]bool{}}
	if tls {
		s.Srv = httptest.NewTLSServer(s)
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Srv.Certificate().Raw})
		f := filepath.Join(t.TempDir(), "ca.pem")
		require.NoError(t, os.WriteFile(f, pemBytes, 0o644))
		pool := x509.NewCertPool()
		require.True(t, pool.AppendCertsFromPEM(pemBytes))
		t.Setenv("AWS_CA_BUNDLE", f)
	} else {
		s.Srv = httptest.NewServer(s)
	}
	t.Cleanup(s.Srv.Close)
	return s
}

func (s *covES3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/")
	switch r.Method {
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		if s.failPut {
			w.WriteHeader(http.StatusForbidden) // 4xx: o SDK não faz retry (evita segundos de backoff)
			return
		}
		s.puts++
		s.objs[key] = b
		s.types[key] = r.Header.Get("Content-Type")
		s.hdrs[key] = r.Header.Clone()
		w.Header().Set("ETag", `"fake"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet, http.MethodHead:
		if r.Method == http.MethodHead && s.failHead {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		b, ok := s.objs[key]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>x</Message></Error>`)
			return
		}
		w.Header().Set("Content-Type", s.types[key])
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(b)
		}
	case http.MethodDelete:
		if s.failDel[key] {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		s.deletes = append(s.deletes, key)
		delete(s.objs, key)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *covES3) Client(t *testing.T, bucket string) *storage.Client {
	t.Helper()
	c, err := storage.New(context.Background(), s.Srv.URL, "", bucket, "us-east-1", "k", "s")
	require.NoError(t, err)
	return c
}

func (s *covES3) Has(bucket, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objs[bucket+"/"+key]
	return ok
}

func (s *covES3) Body(bucket, key string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.objs[bucket+"/"+key]...)
}

func (s *covES3) ContentType(bucket, key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.types[bucket+"/"+key]
}

// PutHeader devolve um cabeçalho do último PUT do objeto (ex.: x-amz-storage-class).
func (s *covES3) PutHeader(bucket, key, name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hdrs[bucket+"/"+key].Get(name)
}

func (s *covES3) Put(bucket, key, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objs[bucket+"/"+key] = []byte(body)
	s.types[bucket+"/"+key] = "video/mp4"
}

func (s *covES3) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objs)
}

func (s *covES3) Puts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.puts
}

func (s *covES3) FailPuts(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failPut = v
}

func (s *covES3) FailDelete(bucket, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failDel[bucket+"/"+key] = true
}

func (s *covES3) FailHeads(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failHead = v
}

// ───────────────────────── áudio sintético ─────────────────────────

var (
	covESynthMu    sync.Mutex
	covESynthCache = map[string][]float32{}
)

// covESynth devolve áudio sintético determinístico (cacheado): a cada ~83ms três
// senoides de frequência aleatória. Rende ~200 hashes/s — esparso o bastante pra
// que áudios de seeds diferentes NÃO casem (cobertura ~0.01), ao contrário de
// ruído branco denso, cujas colisões aleatórias chegam a ~0.17 de cobertura e
// passariam no auditor (limiar 0.15). Mesma seed = mesmo áudio.
func covESynth(seed int64, seconds int) []float32 {
	key := fmt.Sprintf("%d/%d", seed, seconds)
	covESynthMu.Lock()
	defer covESynthMu.Unlock()
	if v, ok := covESynthCache[key]; ok {
		return v
	}
	const sr = 16000
	r := rand.New(rand.NewSource(seed))
	n := seconds * sr
	out := make([]float32, n)
	seg := sr / 12
	for off := 0; off < n; off += seg {
		const k = 3
		var freqs, amps [k]float64
		for i := 0; i < k; i++ {
			freqs[i] = 300 + r.Float64()*3500
			amps[i] = 0.3 + 0.7*r.Float64()
		}
		for i := off; i < off+seg && i < n; i++ {
			var v float64
			for j := 0; j < k; j++ {
				v += amps[j] * math.Sin(2*math.Pi*freqs[j]*float64(i)/sr)
			}
			out[i] = float32(v / k)
		}
	}
	covESynthCache[key] = out
	return out
}

func covEConcat(parts ...[]float32) []float32 {
	var out []float32
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// ───────────────────────── banco de teste ─────────────────────────

func covEDB(t *testing.T) (context.Context, *pgxpool.Pool) {
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

// covEClosedPool devolve um pool já fechado (toda query falha).
func covEClosedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	_, p := covEDB(t)
	p.Close()
	return p
}

func covESeedClient(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ($1) RETURNING id`, "Cliente Teste "+uuid.NewString()[:8]).Scan(&id))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id) })
	return id
}

func covESeedStation(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO stations (name, band, stream_url) VALUES ($1, 'FM', 'http://stream.example.com/a')
		RETURNING id`, "Rádio Teste FM "+uuid.NewString()[:6]).Scan(&id))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, id) })
	return id
}

// covESeedCampaign cria uma campanha ativa que cobre qualquer data de teste.
func covESeedCampaign(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clientID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status)
		VALUES ($1, 'Campanha Teste', '2020-01-01', '2099-12-31', 'ativa') RETURNING id`,
		clientID).Scan(&id))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM detection_campaigns WHERE campaign_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM detections WHERE campaign_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM campaign_materials WHERE campaign_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1`, id)
	})
	return id
}

// covESeedMaterial cria um material pronto do cliente e (se pcm != nil) grava os
// hashes do áudio como master dele. Devolve (id, short_id).
func covESeedMaterial(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clientID uuid.UUID,
	title string, durationSec float64, masterSHA string, pcm []float32) (uuid.UUID, int32) {
	t.Helper()
	if masterSHA == "" {
		masterSHA = "cove-" + uuid.NewString()
	}
	var id uuid.UUID
	var short int32
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO materials (client_id, title, duration_seconds, master_storage_path,
		                       master_sha256, fingerprint_status)
		VALUES ($1, $2, $3, '/tmp/m.mp3', $4, 'ready') RETURNING id, short_id`,
		clientID, title, durationSec, masterSHA).Scan(&id, &short))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM material_twin_discriminative WHERE material_id = $1 OR twin_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM fingerprint_hashes WHERE commercial_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, id)
	})
	if pcm != nil {
		covEInsertHashes(t, ctx, pool, id, pcm)
	}
	return id, short
}

type covEHashSet struct {
	vals   []int64
	frames []int32
	src    []float32 // mantém o PCM vivo: o endereço usado na chave do cache nunca é reciclado
}

var (
	covEHashMu    sync.Mutex
	covEHashCache = map[string]covEHashSet{}
)

// covEHashesOf calcula (e cacheia por ponteiro+tamanho) os hashes de um PCM.
func covEHashesOf(pcm []float32) covEHashSet {
	key := ""
	if len(pcm) > 0 {
		key = fmt.Sprintf("%p/%d", &pcm[0], len(pcm))
	}
	covEHashMu.Lock()
	defer covEHashMu.Unlock()
	if v, ok := covEHashCache[key]; ok && key != "" {
		return v
	}
	hs := audit.PCMToHashes(pcm)
	set := covEHashSet{vals: make([]int64, 0, len(hs)), frames: make([]int32, 0, len(hs)), src: pcm}
	for _, h := range hs {
		set.vals = append(set.vals, int64(h.Value))
		set.frames = append(set.frames, int32(h.TimeFrame))
	}
	if key != "" {
		covEHashCache[key] = set
	}
	return set
}

func covEInsertHashes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, pcm []float32) {
	t.Helper()
	set := covEHashesOf(pcm)
	vals, frames := set.vals, set.frames
	_, err := pool.Exec(ctx, `
		INSERT INTO fingerprint_hashes (commercial_id, variant_id, rate_id, hash_value, time_frame)
		SELECT $1, 0, 0, u.h, u.t FROM unnest($2::bigint[], $3::int[]) AS u(h, t)
		ON CONFLICT DO NOTHING`, id, vals, frames)
	require.NoError(t, err)
}

// covESeedUser cria um usuário (necessário pra manual_by).
func covESeedUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role, name)
		VALUES ($1, 'x', 'admin', 'Admin Teste') RETURNING id`,
		"admin-"+uuid.NewString()[:8]+"@example.com").Scan(&id))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM detections WHERE manual_by = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

// covESeedProofBatch cria um lote de comprovante manual (proof_batch_id).
func covESeedProofBatch(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campaignID, stationID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO manual_proof_batches (campaign_id, station_id, proof_pdf_key, proof_pdf_size)
		VALUES ($1, $2, 'proofs/x.pdf', 10) RETURNING id`, campaignID, stationID).Scan(&id))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM detections WHERE proof_batch_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM manual_proof_batches WHERE id = $1`, id)
	})
	return id
}

// covELink vincula o material à campanha para a emissora (target_stations).
func covELink(t *testing.T, ctx context.Context, pool *pgxpool.Pool, campaignID, materialID, stationID uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, ARRAY[$3]::uuid[])`, campaignID, materialID, stationID)
	require.NoError(t, err)
}

// covEDetOpts descreve o estado inicial de uma detecção semeada direto no banco.
type covEDetOpts struct {
	Status      string // evidence_status (default "available")
	Retracted   bool
	Cov         *float64
	Key         string // evidence_key ("" = NULL)
	Size        int64
	Tier        string // default "hot"
	Manual      bool   // manual_at/manual_by (exige user; ver covESeedUser)
	ManualBy    *uuid.UUID
	ProofBatch  *uuid.UUID
	CreatedAt   *time.Time
	Category    string // default "in_slot"
}

func covESeedDet(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	campaignID, materialID, stationID uuid.UUID, at time.Time, o covEDetOpts) uuid.UUID {
	t.Helper()
	if o.Status == "" {
		o.Status = "available"
	}
	if o.Tier == "" {
		o.Tier = "hot"
	}
	if o.Category == "" {
		o.Category = "in_slot"
	}
	var retractedAt *time.Time
	if o.Retracted {
		retractedAt = &at
	}
	var key *string
	if o.Key != "" {
		key = &o.Key
	}
	var manualAt *time.Time
	if o.Manual {
		manualAt = &at
	}
	created := time.Now()
	if o.CreatedAt != nil {
		created = *o.CreatedAt
	}
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		    match_start_offset_ms, match_end_offset_ms, confidence, hash_count,
		    temporal_coverage, variant_used, rate_used, category,
		    retracted_at, evidence_status, audit_coverage, evidence_key, evidence_size_bytes,
		    tier, manual_at, manual_by, proof_batch_id, created_at)
		VALUES ($1,$2,$3,$4, 0,30000, 0.9,100, 0.85,0,0, $5, $6,$7,$8,$9,$10, $11,$12,$13,$14,$15)
		RETURNING id`,
		stationID, materialID, campaignID, at, o.Category, retractedAt, o.Status, o.Cov, key, o.Size,
		o.Tier, manualAt, o.ManualBy, o.ProofBatch, created).Scan(&id))
	_, err := pool.Exec(ctx, `
		INSERT INTO detection_campaigns (detection_id, detected_at, campaign_id, commercial_id, category)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`, id, at, campaignID, materialID, o.Category)
	require.NoError(t, err)
	return id
}

type covEDet struct {
	CommercialID uuid.UUID
	CampaignID   uuid.UUID
	Status       string
	Key          *string
	Size         *int64
	Retracted    bool
	Cov          *float64
	Tier         string
}

func covEReadDet(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) covEDet {
	t.Helper()
	var d covEDet
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT commercial_id, campaign_id, evidence_status, evidence_key, evidence_size_bytes,
		       retracted_at IS NOT NULL, audit_coverage::float8, tier
		FROM detections WHERE id = $1`, id).
		Scan(&d.CommercialID, &d.CampaignID, &d.Status, &d.Key, &d.Size, &d.Retracted, &d.Cov, &d.Tier))
	return d
}

// covEKey devolve o evidence_key ("" quando NULL ou vazio).
func covEKey(d covEDet) string {
	if d.Key == nil {
		return ""
	}
	return *d.Key
}

// covEProjections devolve (campaign_id -> commercial_id) das projeções da detecção.
func covEProjections(t *testing.T, ctx context.Context, pool *pgxpool.Pool, detID uuid.UUID) map[uuid.UUID]uuid.UUID {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT campaign_id, commercial_id FROM detection_campaigns WHERE detection_id = $1`, detID)
	require.NoError(t, err)
	defer rows.Close()
	out := map[uuid.UUID]uuid.UUID{}
	for rows.Next() {
		var c, m uuid.UUID
		require.NoError(t, rows.Scan(&c, &m))
		out[c] = m
	}
	require.NoError(t, rows.Err())
	return out
}

// ───────────────────────── Service ─────────────────────────

type covESvcOpts struct {
	Store       *storage.Client
	NC          *nats.Conn
	Auditor     bool
	ByCoverage  bool
	Twin        bool
	TwinRetract bool
	Multi       bool
}

// covENewService monta um Service real sobre o pool de teste.
func covENewService(pool *pgxpool.Pool, o covESvcOpts) *Service {
	var aud *audit.Auditor
	if o.Auditor {
		aud = audit.NewAuditor(pool, zap.NewNop(), 0, 0)
	}
	return NewService(pool, o.Store, o.NC, catalog.NewDetections(pool), catalog.NewDetectionCampaigns(pool),
		aud, o.ByCoverage, o.Twin, o.TwinRetract, o.Multi, zap.NewNop())
}

// covESegDir cria um diretório de segmentos com um arquivo ADTS (falso) que cobre
// o início da janela, no layout de nome que o segments.Extract espera.
func covESegDir(t *testing.T, windowStart time.Time) string {
	t.Helper()
	dir := t.TempDir()
	name := windowStart.In(time.Local).Format("20060102-150405") + ".aac"
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("fake-adts-bytes"), 0o644))
	return dir
}

var _ = segments.SegmentDuration

// covEMetric lê o valor atual de um Counter/Gauge/Histogram-count do Prometheus.
func covEMetric(t *testing.T, m interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	var d dto.Metric
	require.NoError(t, m.Write(&d))
	switch {
	case d.Counter != nil:
		return d.Counter.GetValue()
	case d.Gauge != nil:
		return d.Gauge.GetValue()
	case d.Histogram != nil:
		return float64(d.Histogram.GetSampleCount())
	}
	t.Fatalf("métrica sem counter/gauge/histogram")
	return 0
}

// covEEventually espera cond virar verdadeira com margem larga.
func covEEventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	require.Eventually(t, cond, 30*time.Second, 25*time.Millisecond, msg)
}

var _ = bytes.NewReader
