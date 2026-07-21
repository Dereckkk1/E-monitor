package catalog

import (
	"testing"
)

// TestClientStationPMM_BulkUpsert cobre os três efeitos de uma única chamada:
// inserir uma linha nova, atualizar uma existente e apagar uma via nil.
func TestClientStationPMM_BulkUpsert(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewClientStationPMM(pool)

	clientID := insSeedClient(t, ctx, pool, "Cliente Target")
	stA := insSeedStationNoProfile(t, ctx, pool, "Alvo A")
	stB := insSeedStationNoProfile(t, ctx, pool, "Alvo B")
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM client_station_pmm WHERE client_id = $1", clientID)
	})

	v3400, v99 := 3400, 99
	// 1ª chamada: insere as duas.
	if _, _, err := repo.BulkUpsert(ctx, clientID, []TargetPMMEntry{
		{StationID: stA, PMMTarget: &v3400},
		{StationID: stB, PMMTarget: &v99},
	}); err != nil {
		t.Fatalf("bulk upsert insert: %v", err)
	}

	// 2ª chamada: atualiza A e apaga B.
	v5000 := 5000
	updated, deleted, err := repo.BulkUpsert(ctx, clientID, []TargetPMMEntry{
		{StationID: stA, PMMTarget: &v5000},
		{StationID: stB, PMMTarget: nil},
	})
	if err != nil {
		t.Fatalf("bulk upsert update: %v", err)
	}
	if updated != 1 || deleted != 1 {
		t.Errorf("updated=%d deleted=%d, want 1 e 1", updated, deleted)
	}

	// Asserção direta no estado da tabela: ListForClient depende de campanhas
	// com target_stations, que este teste de propósito não monta.
	var n, valA int
	if err := pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(max(pmm_target) FILTER (WHERE station_id = $2), -1)
		 FROM client_station_pmm WHERE client_id = $1`, clientID, stA,
	).Scan(&n, &valA); err != nil {
		t.Fatalf("assert state: %v", err)
	}
	if n != 1 {
		t.Fatalf("linhas restantes = %d, want 1 (B tinha que ter sido apagada)", n)
	}
	if valA != 5000 {
		t.Errorf("target de A = %d, want 5000", valA)
	}
}

// TestClientStationPMM_ZeroIsNotAbsent garante a distinção entre "target zero"
// (linha existe, conta no denominador) e "não cadastrado" (sem linha).
func TestClientStationPMM_ZeroIsNotAbsent(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewClientStationPMM(pool)

	clientID := insSeedClient(t, ctx, pool, "Cliente Zero")
	st := insSeedStationNoProfile(t, ctx, pool, "Alvo Zero")
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM client_station_pmm WHERE client_id = $1", clientID)
	})

	zero := 0
	if _, _, err := repo.BulkUpsert(ctx, clientID, []TargetPMMEntry{
		{StationID: st, PMMTarget: &zero},
	}); err != nil {
		t.Fatalf("upsert zero: %v", err)
	}

	var got *int
	if err := pool.QueryRow(ctx,
		`SELECT pmm_target FROM client_station_pmm WHERE client_id = $1 AND station_id = $2`,
		clientID, st,
	).Scan(&got); err != nil {
		t.Fatalf("target 0 não gravou — zero tem que contar como cadastrado: %v", err)
	}
	if got == nil || *got != 0 {
		t.Errorf("target = %v, want 0", got)
	}
}
