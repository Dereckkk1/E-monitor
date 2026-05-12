package catalog

import (
	"testing"
)

func TestMaterials_CreateAndList(t *testing.T) {
	ctx, pool := newTestDB(t)

	// Setup: cria um cliente
	clientsRepo := NewClients(pool)
	cli, err := clientsRepo.Create(ctx, CreateClientInput{Name: "Test Co"})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM materials WHERE client_id = $1", cli.ID)
		pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", cli.ID)
	})

	matsRepo := NewMaterials(pool)

	created, err := matsRepo.Create(ctx, CreateMaterialInput{
		ClientID:          cli.ID,
		Title:             "Spot Test",
		DurationSeconds:   30.0,
		MasterStoragePath: "/tmp/test.mp3",
		MasterSHA256:      "deadbeef",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ClientID != cli.ID {
		t.Errorf("ClientID = %s, want %s", created.ClientID, cli.ID)
	}

	list, err := matsRepo.ListByClient(ctx, cli.ID, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}

	// Busca textual
	list2, _ := matsRepo.ListByClient(ctx, cli.ID, "spot")
	if len(list2) != 1 {
		t.Errorf("search 'spot' len = %d, want 1", len(list2))
	}
	list3, _ := matsRepo.ListByClient(ctx, cli.ID, "naoexiste")
	if len(list3) != 0 {
		t.Errorf("search 'naoexiste' len = %d, want 0", len(list3))
	}
}
