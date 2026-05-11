package catalog

import (
	"testing"

	"github.com/google/uuid"
)

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
	if mt.Color != "#ff00ff" {
		t.Errorf("Color = %q, want #ff00ff", mt.Color)
	}
	if mt.ID == (uuid.UUID{}) {
		t.Errorf("ID is zero")
	}
}
