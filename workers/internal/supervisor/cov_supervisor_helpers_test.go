package supervisor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	"go.uber.org/zap/zaptest/observer"

	"radiocheck/internal/catalog"
	"radiocheck/internal/db"
	"radiocheck/internal/evidence"
	"radiocheck/internal/index"
	"radiocheck/internal/ingestor"
)

// ── ffmpeg/ffprobe falsos ───────────────────────────────────────────────────
//
// Os workers que o supervisor sobe chamam ffprobe/ffmpeg. Os testes nunca
// executam os binários reais (rede + CPU): o PATH do teste aponta para um
// diretório SEM eles (covSNoTools → o exec falha na hora, igual em qualquer
// máquina) ou para cópias do próprio binário de teste que se comportam como
// ffprobe/ffmpeg determinísticos (covSUseFakeTools, só Linux: depende do
// pipe:3 / ExtraFiles).

const covSFakeActive = "COV_S_FAKE_ACTIVE"

var (
	covSToolsDir  string
	covSToolsOnce sync.Once
)

func TestMain(m *testing.M) {
	if os.Getenv(covSFakeActive) == "1" {
		switch strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe") {
		case "ffprobe":
			_, _ = io.WriteString(os.Stdout, os.Getenv("COV_S_FFPROBE_OUT"))
			os.Exit(0)
		case "ffmpeg":
			if n, err := strconv.Atoi(os.Getenv("COV_S_FFMPEG_PCM_BYTES")); err == nil && n > 0 {
				if pipe := os.NewFile(3, "pipe3"); pipe != nil {
					_, _ = pipe.Write(make([]byte, n))
					_ = pipe.Close()
				}
			}
			os.Exit(0)
		}
	}
	code := m.Run()
	if covSToolsDir != "" {
		_ = os.RemoveAll(covSToolsDir)
	}
	os.Exit(code)
}

func covSNoTools(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func covSUseFakeTools(t *testing.T) {
	t.Helper()
	covSToolsOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cov-s-tools-")
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
		covSToolsDir = dir
	})
	t.Setenv("PATH", covSToolsDir)
	t.Setenv(covSFakeActive, "1")
}

// ── fixture de integração ───────────────────────────────────────────────────

// covSFix monta um Supervisor de verdade (repos reais sobre o banco de teste,
// NATS falso em memória, evidence.Service real) e sabe semear/limpar dados.
type covSFix struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	srv      *covSNATS
	nc       *nats.Conn
	logs     *observer.ObservedLogs
	s        *Supervisor
	store    *index.Store
	clientID uuid.UUID

	mu       sync.Mutex
	stations []uuid.UUID
}

func covSNewFix(t *testing.T) *covSFix {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	covSNoTools(t)
	ctx := context.Background()
	pool, err := db.New(ctx, url, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	srv := covSStartNATS(t)
	nc := covSConnect(t, srv)
	log, logs := covSObservedLogger()
	store := index.New()
	ev := evidence.NewService(pool, nil, nc, nil, nil, nil, false, false, false, false, log)
	s := New(pool, store, nc, ev,
		catalog.NewCampaigns(pool), catalog.NewStations(pool), catalog.NewCommercials(pool),
		catalog.NewMaterials(pool), catalog.NewHealthEvents(pool),
		false, t.TempDir(), log)

	f := &covSFix{t: t, ctx: ctx, pool: pool, srv: srv, nc: nc, logs: logs, s: s, store: store}
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('covs-client-'||gen_random_uuid()) RETURNING id`).Scan(&f.clientID))
	t.Cleanup(f.cleanup)
	return f
}

// stopAll cancela todos os workers do supervisor (e esvazia o mapa).
func (f *covSFix) stopAll() {
	f.s.mu.Lock()
	for id, e := range f.s.workers {
		if e != nil && e.cancel != nil {
			e.cancel()
		}
		delete(f.s.workers, id)
	}
	f.s.mu.Unlock()
}

func (f *covSFix) cleanup() {
	f.stopAll()
	time.Sleep(50 * time.Millisecond) // deixa goroutines de escrita em voo terminarem
	bg := context.Background()
	f.mu.Lock()
	ids := append([]uuid.UUID(nil), f.stations...)
	f.mu.Unlock()
	f.pool.Exec(bg, `DELETE FROM dedup_suppressions WHERE station_id = ANY($1)`, ids)
	f.pool.Exec(bg, `DELETE FROM detections WHERE station_id = ANY($1)`, ids)
	f.pool.Exec(bg, `DELETE FROM detections WHERE campaign_id IN (SELECT id FROM campaigns WHERE client_id = $1)`, f.clientID)
	f.pool.Exec(bg, `DELETE FROM stream_health_events WHERE station_id = ANY($1)`, ids)
	f.pool.Exec(bg, `DELETE FROM commercials WHERE campaign_id IN (SELECT id FROM campaigns WHERE client_id = $1)`, f.clientID)
	f.pool.Exec(bg, `DELETE FROM campaigns WHERE client_id = $1`, f.clientID)
	f.pool.Exec(bg, `DELETE FROM materials WHERE client_id = $1`, f.clientID)
	f.pool.Exec(bg, `DELETE FROM clients WHERE id = $1`, f.clientID)
	f.pool.Exec(bg, `DELETE FROM stations WHERE id = ANY($1)`, ids)
}

// station cria uma emissora genérica (já com a linha de station_thresholds).
func (f *covSFix) station() uuid.UUID {
	f.t.Helper()
	tag := uuid.NewString()
	st, err := catalog.NewStations(f.pool).Create(f.ctx, catalog.CreateStationInput{
		Name:      "Rádio Teste FM " + tag,
		Band:      "FM",
		StreamURL: "http://stream.example.com/" + tag,
	})
	require.NoError(f.t, err)
	f.mu.Lock()
	f.stations = append(f.stations, st.ID)
	f.mu.Unlock()
	return st.ID
}

// campaign cria uma campanha do cliente da fixture. startOff/endOff são dias
// relativos a CURRENT_DATE.
func (f *covSFix) campaign(status string, startOff, endOff int, stations ...uuid.UUID) uuid.UUID {
	f.t.Helper()
	if stations == nil {
		stations = []uuid.UUID{}
	}
	var id uuid.UUID
	require.NoError(f.t, f.pool.QueryRow(f.ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date, status, target_stations)
		VALUES ($1, 'covs-camp-'||gen_random_uuid(), CURRENT_DATE + $2::int, CURRENT_DATE + $3::int, $4, $5)
		RETURNING id`, f.clientID, startOff, endOff, status, stations).Scan(&id))
	return id
}

// material cria um material 'ready' ligado à campanha (campaign_materials) e
// direcionado às emissoras dadas. Devolve o id e o short_id.
func (f *covSFix) material(campaignID uuid.UUID, durationSeconds float64, stations ...uuid.UUID) (uuid.UUID, int32) {
	f.t.Helper()
	if stations == nil {
		stations = []uuid.UUID{}
	}
	id := uuid.New()
	var shortID int32
	require.NoError(f.t, f.pool.QueryRow(f.ctx, `
		INSERT INTO materials (id, client_id, title, duration_seconds, master_storage_path,
		                       master_sha256, fingerprint_status)
		VALUES ($1, $2, 'covs-mat', $3, '/tmp/covs.mp3', $4, 'ready')
		RETURNING short_id`, id, f.clientID, durationSeconds, "sha-covs-"+id.String()).Scan(&shortID))
	_, err := f.pool.Exec(f.ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, $3)`, campaignID, id, stations)
	require.NoError(f.t, err)
	return id, shortID
}

// commercial cria um commercial legado 'ready' (sem linha em materials).
func (f *covSFix) commercial(campaignID uuid.UUID, durationSeconds float64, stations ...uuid.UUID) (uuid.UUID, int32) {
	f.t.Helper()
	if stations == nil {
		stations = []uuid.UUID{}
	}
	id := uuid.New()
	var shortID int32
	require.NoError(f.t, f.pool.QueryRow(f.ctx, `
		INSERT INTO commercials (id, campaign_id, title, duration_seconds, master_storage_path,
		                         master_sha256, fingerprint_status, target_stations)
		VALUES ($1, $2, 'covs-com', $3, '/tmp/covs.mp3', $4, 'ready', $5)
		RETURNING short_id`, id, campaignID, durationSeconds, "sha-covs-"+id.String(), stations).Scan(&shortID))
	return id, shortID
}

// worker devolve o worker em execução da emissora (nil se não houver).
func (f *covSFix) worker(station uuid.UUID) *ingestor.Worker {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if e, ok := f.s.workers[station]; ok && e != nil {
		return e.worker
	}
	return nil
}

// hasEntry diz se há entrada (worker ou placeholder) para a emissora.
func (f *covSFix) hasEntry(station uuid.UUID) bool {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	_, ok := f.s.workers[station]
	return ok
}

func (f *covSFix) campaignStatus(id uuid.UUID) string {
	f.t.Helper()
	var st string
	require.NoError(f.t, f.pool.QueryRow(f.ctx, `SELECT status FROM campaigns WHERE id=$1`, id).Scan(&st))
	return st
}

func (f *covSFix) monitoring(station uuid.UUID) string {
	f.t.Helper()
	var st string
	require.NoError(f.t, f.pool.QueryRow(f.ctx, `SELECT monitoring_status FROM stations WHERE id=$1`, station).Scan(&st))
	return st
}

func (f *covSFix) waitWorker(station uuid.UUID) *ingestor.Worker {
	f.t.Helper()
	require.Eventually(f.t, func() bool { return f.worker(station) != nil },
		15*time.Second, 10*time.Millisecond, "worker da emissora %s não subiu", station)
	return f.worker(station)
}

func (f *covSFix) waitNoEntry(station uuid.UUID) {
	f.t.Helper()
	require.Eventually(f.t, func() bool { return !f.hasEntry(station) },
		15*time.Second, 10*time.Millisecond, "entrada da emissora %s não saiu do mapa", station)
}

// wait espera a condição (até 15s) — usar no lugar de sleeps fixos.
func (f *covSFix) wait(cond func() bool, msg string) {
	f.t.Helper()
	require.Eventually(f.t, cond, 15*time.Second, 10*time.Millisecond, msg)
}

// blockUpdate faz QUALQUER UPDATE da coluna `col` da linha `id` em `table`
// falhar (trigger BEFORE UPDATE OF col ... RAISE EXCEPTION) até o fim do teste.
// Serve pra exercitar os ramos de "a escrita no banco falhou" sem mockar o
// repositório: o código de produção roda inteiro contra um Postgres de verdade
// que rejeita aquela escrita específica.
func (f *covSFix) blockUpdate(table, col string, id uuid.UUID) {
	f.t.Helper()
	name := "covs_block_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := f.pool.Exec(f.ctx, fmt.Sprintf(
		`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'covs: update bloqueado'; END $$`, name))
	require.NoError(f.t, err)
	_, err = f.pool.Exec(f.ctx, fmt.Sprintf(
		`CREATE TRIGGER %[1]s BEFORE UPDATE OF %[3]s ON %[2]s FOR EACH ROW WHEN (OLD.id = '%[4]s') EXECUTE FUNCTION %[1]s()`,
		name, table, col, id))
	require.NoError(f.t, err)
	f.t.Cleanup(func() {
		bg := context.Background()
		f.pool.Exec(bg, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, name, table))
		f.pool.Exec(bg, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, name))
	})
}
