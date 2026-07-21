package catalog

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestClients_Delete_BlockedByDependents verifies that deleting a client that
// still owns rows in a blocking table (campaigns has a NO ACTION FK) returns
// ErrClientHasDependents — not a raw pg error — so the handler can map it to a
// 409 instead of a silent 500. Regression test for the prod bug where DELETE
// /v1/internal/clients/{id} returned 500 for any client with campaigns.
func TestClients_Delete_BlockedByDependents(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewClients(pool)

	cli, err := repo.Create(ctx, CreateClientInput{Name: "Dependents Test Co"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id = $1`, cli.ID) //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID)          //nolint:errcheck
	})

	// A campaign references the client (FK NO ACTION → blocks the delete).
	if _, err := pool.Exec(ctx, `
		INSERT INTO campaigns (client_id, name, start_date, end_date)
		VALUES ($1, 'Blocking Campaign', '2026-01-01', '2026-01-31')`, cli.ID); err != nil {
		t.Fatalf("insert campaign fixture: %v", err)
	}

	err = repo.Delete(ctx, cli.ID)
	if !errors.Is(err, ErrClientHasDependents) {
		t.Fatalf("Delete err = %v, want ErrClientHasDependents", err)
	}

	counts, err := repo.CountDependents(ctx, cli.ID)
	if err != nil {
		t.Fatalf("CountDependents: %v", err)
	}
	if counts.Campaigns != 1 {
		t.Errorf("counts.Campaigns = %d, want 1", counts.Campaigns)
	}
	if counts.Total() != 1 {
		t.Errorf("counts.Total() = %d, want 1", counts.Total())
	}
}

// TestClients_Delete_NoRows asserts an unknown id surfaces pgx.ErrNoRows (→ 404),
// distinct from the dependents path.
func TestClients_Delete_NoRows(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewClients(pool)

	if err := repo.Delete(ctx, uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("Delete(unknown) err = %v, want pgx.ErrNoRows", err)
	}
}

// TestClients_Delete_Success removes a client with no dependents (the happy
// path → 204).
func TestClients_Delete_Success(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewClients(pool)

	cli, err := repo.Create(ctx, CreateClientInput{Name: "Deletable Co"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID) //nolint:errcheck
	})

	if err := repo.Delete(ctx, cli.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, cli.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("Get after delete err = %v, want pgx.ErrNoRows", err)
	}
}

// TestClients_SetActive round-trips the deactivate/reactivate flow and confirms
// new clients start active.
func TestClients_SetActive(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewClients(pool)

	cli, err := repo.Create(ctx, CreateClientInput{Name: "Toggle Co"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID) //nolint:errcheck
	})
	if !cli.IsActive {
		t.Fatalf("new client IsActive = false, want true (DEFAULT TRUE)")
	}

	off, err := repo.SetActive(ctx, cli.ID, false)
	if err != nil {
		t.Fatalf("SetActive(false): %v", err)
	}
	if off.IsActive {
		t.Errorf("after deactivate IsActive = true, want false")
	}
	if got, _ := repo.Get(ctx, cli.ID); got != nil && got.IsActive {
		t.Errorf("Get after deactivate IsActive = true, want false")
	}

	on, err := repo.SetActive(ctx, cli.ID, true)
	if err != nil {
		t.Fatalf("SetActive(true): %v", err)
	}
	if !on.IsActive {
		t.Errorf("after reactivate IsActive = false, want true")
	}
}

// TestClients_SetActive_NoRows asserts an unknown id surfaces pgx.ErrNoRows.
func TestClients_SetActive_NoRows(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewClients(pool)

	if _, err := repo.SetActive(ctx, uuid.New(), false); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("SetActive(unknown) err = %v, want pgx.ErrNoRows", err)
	}
}

// TestClients_ListPaged_HidesInactive confirms the management list hides
// deactivated clients by default and reveals them with includeInactive=true.
// Names share a unique token so the search filter isolates this test's rows
// from whatever else lives in the DB.
func TestClients_ListPaged_HidesInactive(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewClients(pool)

	const token = "ZZListPagedInactiveTok"
	act, err := repo.Create(ctx, CreateClientInput{Name: token + " Active"})
	if err != nil {
		t.Fatalf("Create active: %v", err)
	}
	ina, err := repo.Create(ctx, CreateClientInput{Name: token + " Inactive"})
	if err != nil {
		t.Fatalf("Create inactive: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM clients WHERE id = ANY($1)`, []uuid.UUID{act.ID, ina.ID}) //nolint:errcheck
	})
	if _, err := repo.SetActive(ctx, ina.ID, false); err != nil {
		t.Fatalf("SetActive: %v", err)
	}

	// Default (includeInactive=false): only the active one.
	rows, total, err := repo.ListPaged(ctx, token, 1, 50, false)
	if err != nil {
		t.Fatalf("ListPaged(default): %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("default total=%d len=%d, want 1/1", total, len(rows))
	}
	if rows[0].ID != act.ID {
		t.Errorf("default returned %v, want active %v", rows[0].ID, act.ID)
	}

	// includeInactive=true: both show up.
	rows, total, err = repo.ListPaged(ctx, token, 1, 50, true)
	if err != nil {
		t.Fatalf("ListPaged(include): %v", err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("include total=%d len=%d, want 2/2", total, len(rows))
	}
}

// TestClients_TargetLabel_RoundTrip guards the clientColumns × scanClient
// alignment for the target_label column (migration 0055): a misordered Scan
// wouldn't break the build, it would silently put the CNPJ in Name and the
// label somewhere else. Round-trips Create → Get → Update → SetActive and
// asserts every neighbouring field keeps its own value.
func TestClients_TargetLabel_RoundTrip(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewClients(pool)

	label := "Homens 25-49, classe AB"
	cnpj := "12345678000199"
	state := "MG"
	cli, err := repo.Create(ctx, CreateClientInput{
		Name: "Target Label Co", CNPJ: &cnpj, State: &state, TargetLabel: &label,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, cli.ID) //nolint:errcheck
	})

	assertFields := func(where string, c *Client) {
		t.Helper()
		if c.Name != "Target Label Co" {
			t.Errorf("%s: Name = %q, want %q (scan order drift?)", where, c.Name, "Target Label Co")
		}
		if c.CNPJ == nil || *c.CNPJ != cnpj {
			t.Errorf("%s: CNPJ = %v, want %q", where, c.CNPJ, cnpj)
		}
		if c.State == nil || *c.State != state {
			t.Errorf("%s: State = %v, want %q", where, c.State, state)
		}
		if !c.IsActive {
			t.Errorf("%s: IsActive = false, want true", where)
		}
		if c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
			t.Errorf("%s: timestamps zero (created=%v updated=%v)", where, c.CreatedAt, c.UpdatedAt)
		}
	}

	assertFields("Create", cli)
	if cli.TargetLabel == nil || *cli.TargetLabel != label {
		t.Fatalf("Create: TargetLabel = %v, want %q", cli.TargetLabel, label)
	}

	got, err := repo.Get(ctx, cli.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	assertFields("Get", got)
	if got.TargetLabel == nil || *got.TargetLabel != label {
		t.Errorf("Get: TargetLabel = %v, want %q", got.TargetLabel, label)
	}

	// Update writes a new label…
	newLabel := "Mulheres 18-34"
	upd, err := repo.Update(ctx, cli.ID, UpdateClientInput{
		Name: "Target Label Co", CNPJ: &cnpj, State: &state, TargetLabel: &newLabel,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	assertFields("Update", upd)
	if upd.TargetLabel == nil || *upd.TargetLabel != newLabel {
		t.Errorf("Update: TargetLabel = %v, want %q", upd.TargetLabel, newLabel)
	}

	// …and nil clears it (cliente sem rótulo → telas dizem só "no target").
	cleared, err := repo.Update(ctx, cli.ID, UpdateClientInput{
		Name: "Target Label Co", CNPJ: &cnpj, State: &state, TargetLabel: nil,
	})
	if err != nil {
		t.Fatalf("Update(clear): %v", err)
	}
	if cleared.TargetLabel != nil {
		t.Errorf("Update(nil): TargetLabel = %v, want nil", *cleared.TargetLabel)
	}
}
