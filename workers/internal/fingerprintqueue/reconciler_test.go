package fingerprintqueue

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/db"
)

func newTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url)
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return ctx, pool
}

// TestFailedRetryCap garante que um material 'failed' só é re-tentado
// MaxFailedRetries vezes por processo — sem isso, um arquivo permanentemente
// corrompido seria reprocessado a cada tick para sempre.
func TestFailedRetryCap(t *testing.T) {
	r := New(nil, nil, zap.NewNop())
	id := uuid.New()

	for i := 0; i < r.MaxFailedRetries; i++ {
		if !r.shouldRetryFailed(id) {
			t.Fatalf("tentativa %d deveria ser permitida (cap=%d)", i+1, r.MaxFailedRetries)
		}
	}
	if r.shouldRetryFailed(id) {
		t.Fatalf("tentativa %d deveria ser bloqueada pelo cap", r.MaxFailedRetries+1)
	}
	// Outro material não é afetado pelo cap do primeiro.
	if !r.shouldRetryFailed(uuid.New()) {
		t.Fatal("cap de um material não pode vazar para outro")
	}
}

// TestListStuck cobre a seleção: pending/generating velhos entram, recentes
// não; failed entra; ready nunca.
func TestListStuck(t *testing.T) {
	ctx, pool := newTestDB(t)
	r := New(pool, nil, zap.NewNop())

	cli := mustClient(t, ctx, pool)
	oldPending := mustMaterial(t, ctx, pool, cli, "pending", "-30 minutes")
	freshPending := mustMaterial(t, ctx, pool, cli, "pending", "-1 minutes")
	oldGenerating := mustMaterial(t, ctx, pool, cli, "generating", "-30 minutes")
	failed := mustMaterial(t, ctx, pool, cli, "failed", "-30 minutes")
	ready := mustMaterial(t, ctx, pool, cli, "ready", "-30 minutes")

	stuck, err := r.listStuck(ctx)
	if err != nil {
		t.Fatalf("listStuck: %v", err)
	}
	got := map[uuid.UUID]string{}
	for _, s := range stuck {
		got[s.ID] = s.Status
	}
	if _, ok := got[oldPending]; !ok {
		t.Error("pending velho deveria entrar")
	}
	if _, ok := got[freshPending]; ok {
		t.Error("pending recente NÃO deveria entrar (ainda dentro do prazo normal)")
	}
	if _, ok := got[oldGenerating]; !ok {
		t.Error("generating velho deveria entrar")
	}
	if _, ok := got[failed]; !ok {
		t.Error("failed deveria entrar")
	}
	if _, ok := got[ready]; ok {
		t.Error("ready NUNCA deveria entrar")
	}
}

// ── fixtures ─────────────────────────────────────────────────────────

func mustClient(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('FPQueue Test Co') RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("insert client: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM materials WHERE client_id=$1`, id) //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM clients WHERE id=$1`, id)          //nolint:errcheck
	})
	return id
}

// mustMaterial insere material com fingerprint_status e updated_at já
// deslocado no INSERT (ageInterval negativo = no passado). O trigger
// trg_materials_updated só dispara em UPDATE, então o valor explícito no
// INSERT é preservado.
func mustMaterial(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cli uuid.UUID, status, ageInterval string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO materials (client_id, title, duration_seconds, master_storage_path, master_sha256,
		                        fingerprint_status, updated_at)
		 VALUES ($1, 'M '||$2||' '||$3, 30, 'test/fpq.mp3', 'deadbeef', $2, now() + $3::interval)
		 RETURNING id`,
		cli, status, ageInterval).Scan(&id); err != nil {
		t.Fatalf("insert material: %v", err)
	}
	return id
}
