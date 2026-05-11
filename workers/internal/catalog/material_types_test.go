package catalog

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"radiocheck/internal/db"
)

// newTestDB returns a raw pool connected to TEST_DATABASE_URL.
// It skips the test if the env var is not set and closes the pool on cleanup.
func newTestDB(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	return ctx, pool
}

func TestMaterialTypes_List(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewMaterialTypes(pool)

	types, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Migration 0016 semeia 6 tipos
	if len(types) < 6 {
		t.Fatalf("expected at least 6 types, got %d", len(types))
	}
	// Spot 30s deve estar presente
	found := false
	for _, mt := range types {
		if mt.Name == "Spot 30s" {
			found = true
			if mt.Color != "#3b82f6" {
				t.Errorf("Spot 30s color = %q, want #3b82f6", mt.Color)
			}
		}
	}
	if !found {
		t.Errorf("Spot 30s not found in seed data")
	}
}

func TestMaterialTypes_Create(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewMaterialTypes(pool)

	// Clean up any leftover row from a previous run
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM material_types WHERE name = 'Promo Especial'`) //nolint:errcheck
	})

	mt, err := repo.Create(ctx, CreateMaterialTypeInput{
		Name:  "Promo Especial",
		Color: "#ff00ff",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if mt.Name != "Promo Especial" {
		t.Errorf("Name = %q, want Promo Especial", mt.Name)
	}
}
