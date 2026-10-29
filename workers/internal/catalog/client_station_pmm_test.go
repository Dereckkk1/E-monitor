package catalog

import (
	"testing"

	"github.com/google/uuid"
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

// TestClientStationPMM_ForStations: o valor gravado por (cliente, emissora), só
// das emissoras pedidas — inclusive as que nenhuma campanha do cliente usa, que
// o ListForClient não enxerga. Ausência de linha = fora do mapa (não é zero).
func TestClientStationPMM_ForStations(t *testing.T) {
	ctx, pool := newTestDB(t)
	repo := NewClientStationPMM(pool)
	clientID := insSeedClient(t, ctx, pool, "Cliente ForStations")
	outroCli := insSeedClient(t, ctx, pool, "Outro ForStations")
	stA := insSeedStationNoProfile(t, ctx, pool, "FS A")
	stB := insSeedStationNoProfile(t, ctx, pool, "FS B")
	stC := insSeedStationNoProfile(t, ctx, pool, "FS C")
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM client_station_pmm WHERE client_id IN ($1, $2)", clientID, outroCli)
	})
	v3400, v0, v7 := 3400, 0, 7
	if _, _, err := repo.BulkUpsert(ctx, clientID, []TargetPMMEntry{{StationID: stA, PMMTarget: &v3400}, {StationID: stB, PMMTarget: &v0}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.BulkUpsert(ctx, outroCli, []TargetPMMEntry{{StationID: stC, PMMTarget: &v7}}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ForStations(ctx, clientID, []uuid.UUID{stA, stB, stC})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[stA] != 3400 {
		t.Errorf("got %v, quero A=3400 e B=0 (C é de outro cliente, fora)", got)
	}
	if v, ok := got[stB]; !ok || v != 0 {
		t.Errorf("zero é linha, não ausência: got[B]=%d ok=%v", v, ok)
	}
	if _, ok := got[stC]; ok {
		t.Error("a linha de outro cliente não vaza")
	}

	vazio, err := repo.ForStations(ctx, clientID, nil)
	if err != nil || len(vazio) != 0 {
		t.Errorf("sem emissoras: %v %v", vazio, err)
	}
}
