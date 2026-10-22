package catalog

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// Usa `hubClientsPool` de hubclients_test.go: PULA sem TEST_DATABASE_URL.

func novoCliente(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nome string, hubID *string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name, hub_id) VALUES ($1, $2) RETURNING id`, nome, hubID).Scan(&id))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM campaigns WHERE client_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id)
	})
	return id
}

func comCampanha(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cliente uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO campaigns (client_id, name, start_date, end_date, target_stations)
		 VALUES ($1, 'pendurada', '2026-01-01', '2026-01-31', '{}')`, cliente)
	require.NoError(t, err)
}

func ptr(s string) *string { return &s }

func TestClienteVazio_SemNadaPendurado(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	id := novoCliente(t, ctx, pool, "Vazio "+uuid.NewString()[:6], nil)
	vazio, err := NewLigacoes(pool).ClienteVazio(ctx, id)
	require.NoError(t, err)
	require.True(t, vazio)
}

func TestClienteVazio_ComCampanha(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	id := novoCliente(t, ctx, pool, "Com campanha "+uuid.NewString()[:6], nil)
	comCampanha(t, ctx, pool, id)
	vazio, err := NewLigacoes(pool).ClienteVazio(ctx, id)
	require.NoError(t, err)
	require.False(t, vazio)
}

// Prova que a descoberta das FKs é dinâmica: uma tabela que nenhuma lista
// escrita à mão conheceria também conta.
func TestClienteVazio_TabelaNovaComFK(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	// Tudo dentro de uma transação revertida: o DDL não aparece no banco compartilhado.
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var id uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ($1) RETURNING id`, "FK nova "+uuid.NewString()[:6]).Scan(&id))
	_, err = tx.Exec(ctx, `CREATE TABLE teste_fk_ligacao (id serial PRIMARY KEY, cliente uuid REFERENCES clients(id))`)
	require.NoError(t, err)

	vazio, err := clienteVazio(ctx, tx, id)
	require.NoError(t, err)
	require.True(t, vazio)

	_, err = tx.Exec(ctx, `INSERT INTO teste_fk_ligacao (cliente) VALUES ($1)`, id)
	require.NoError(t, err)
	vazio, err = clienteVazio(ctx, tx, id)
	require.NoError(t, err)
	require.False(t, vazio)
}

func TestFatos_DetentorDuplicadoVazio(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	hubID := "hub-" + uuid.NewString()
	escolhido := novoCliente(t, ctx, pool, "GIGA MAIS FIBRA", nil)
	novoCliente(t, ctx, pool, "Giga + Internet", ptr(hubID))

	f, err := NewLigacoes(pool).Fatos(ctx, escolhido, hubID, escolhido.String())
	require.NoError(t, err)
	require.Equal(t, "GIGA MAIS FIBRA", f.Escolhido.Nome)
	require.NotNil(t, f.Detentor)
	require.Equal(t, "Giga + Internet", f.Detentor.Nome)
	require.True(t, f.DetentorVazio)
	require.True(t, f.HubApontaEscolhido)
}

func TestFatos_HubApontaParaOutroEmUso(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	escolhido := novoCliente(t, ctx, pool, "Rôgga", nil)
	outro := novoCliente(t, ctx, pool, "Pilecco", nil)
	comCampanha(t, ctx, pool, outro)

	f, err := NewLigacoes(pool).Fatos(ctx, escolhido, "hub-"+uuid.NewString(), outro.String())
	require.NoError(t, err)
	require.NotNil(t, f.ApontadoPeloHub)
	require.Equal(t, outro, f.ApontadoPeloHub.ID)
	require.False(t, f.ApontadoVazio)
}

func TestFatos_HubSemPonteOuComLixoOuInexistente(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	escolhido := novoCliente(t, ctx, pool, "Rôgga", nil)
	for _, id := range []string{"", "lixo", uuid.NewString()} {
		f, err := NewLigacoes(pool).Fatos(ctx, escolhido, "hub-x", id)
		require.NoError(t, err)
		require.True(t, f.HubSemPonte, "id %q", id)
		require.Nil(t, f.ApontadoPeloHub)
	}
}

func hubIDDe(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var h *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT hub_id FROM clients WHERE id = $1`, id).Scan(&h))
	if h == nil {
		return ""
	}
	return *h
}

func TestLigarLocal_ClienteSemLigacao(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	hubID := "hub-" + uuid.NewString()
	c := novoCliente(t, ctx, pool, "CCGL", nil)
	ator := uuid.New()

	mudou, err := NewLigacoes(pool).LigarLocal(ctx, c, hubID, &ator)
	require.NoError(t, err)
	require.True(t, mudou)
	require.Equal(t, hubID, hubIDDe(t, ctx, pool, c))

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'client.hub_link' AND resource_id = $1 AND actor_id = $2`,
		c, ator).Scan(&n))
	require.Equal(t, 1, n)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE resource_id = $1`, c) })
}

func TestLigarLocal_MoveDoDuplicadoVazio(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	hubID := "hub-" + uuid.NewString()
	c := novoCliente(t, ctx, pool, "GIGA MAIS FIBRA", nil)
	d := novoCliente(t, ctx, pool, "Giga + Internet", ptr(hubID))

	mudou, err := NewLigacoes(pool).LigarLocal(ctx, c, hubID, nil)
	require.NoError(t, err)
	require.True(t, mudou)
	require.Equal(t, hubID, hubIDDe(t, ctx, pool, c))
	require.Equal(t, "", hubIDDe(t, ctx, pool, d))
	var tipo string
	var meta map[string]any
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT actor_type, metadata FROM audit_log WHERE action = 'client.hub_link' AND resource_id = $1`, c).Scan(&tipo, &meta))
	require.Equal(t, "system", tipo)
	require.Equal(t, d.String(), meta["de"])
	require.Equal(t, hubID, meta["hub_id"])
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE resource_id = $1`, c) })
}

func TestLigarLocal_RecusaDuplicadoEmUso_SemMexerEmNada(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	hubID := "hub-" + uuid.NewString()
	c := novoCliente(t, ctx, pool, "GIGA MAIS FIBRA", nil)
	d := novoCliente(t, ctx, pool, "Giga + Internet", ptr(hubID))
	comCampanha(t, ctx, pool, d)

	_, err := NewLigacoes(pool).LigarLocal(ctx, c, hubID, nil)
	require.ErrorIs(t, err, ErrDetentorEmUso)
	require.Equal(t, "", hubIDDe(t, ctx, pool, c))
	require.Equal(t, hubID, hubIDDe(t, ctx, pool, d))
}

func TestLigarLocal_RecusaEscolhidoDeOutroClienteDoHub(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	c := novoCliente(t, ctx, pool, "Renner", ptr("hub-"+uuid.NewString()))

	antes := hubIDDe(t, ctx, pool, c)
	_, err := NewLigacoes(pool).LigarLocal(ctx, c, "hub-"+uuid.NewString(), nil)
	require.ErrorIs(t, err, ErrEscolhidoDeOutro)
	require.Equal(t, antes, hubIDDe(t, ctx, pool, c))
}

func TestLigarLocal_JaLigadoNaoMudaNem_Audita(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	hubID := "hub-" + uuid.NewString()
	c := novoCliente(t, ctx, pool, "Renner", ptr(hubID))

	mudou, err := NewLigacoes(pool).LigarLocal(ctx, c, hubID, nil)
	require.NoError(t, err)
	require.False(t, mudou)
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE resource_id = $1`, c).Scan(&n))
	require.Equal(t, 0, n)
}

func TestLigarLocal_Corrida_NuncaVazaErroCruDoPg(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	hubID := "hub-" + uuid.NewString()
	c1 := novoCliente(t, ctx, pool, "Corrida A", nil)
	c2 := novoCliente(t, ctx, pool, "Corrida B", nil)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE resource_id = ANY($1)`, []uuid.UUID{c1, c2})
	})

	l := NewLigacoes(pool)
	largada := make(chan struct{})
	var wg sync.WaitGroup
	erros := make([]error, 2)
	for i, c := range []uuid.UUID{c1, c2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-largada
			_, erros[i] = l.LigarLocal(ctx, c, hubID, nil)
		}()
	}
	close(largada)
	wg.Wait()

	ok := 0
	for _, err := range erros {
		if err == nil {
			ok++
			continue
		}
		require.True(t, errors.Is(err, ErrDetentorEmUso) || errors.Is(err, ErrLigacaoConcorrente), "erro cru: %v", err)
	}
	require.GreaterOrEqual(t, ok, 1)
	// O índice único garante: no máximo um dos dois segura o código.
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM clients WHERE hub_id = $1`, hubID).Scan(&n))
	require.Equal(t, 1, n)
}

func TestFatos_EscolhidoJaSegura_SemDetentor_Ligado(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	hubID := "hub-" + uuid.NewString()
	c := novoCliente(t, ctx, pool, "Renner", ptr(hubID))

	f, err := NewLigacoes(pool).Fatos(ctx, c, hubID, c.String())
	require.NoError(t, err)
	require.Nil(t, f.Detentor)
	require.Equal(t, EstadoLigado, EstadoDaLigacao(*f, hubID, "Renner").Estado)
}

func TestFatos_EscolhidoInexistente(t *testing.T) {
	ctx, pool := hubClientsPool(t)
	_, err := NewLigacoes(pool).Fatos(ctx, uuid.New(), "hub-x", "")
	require.True(t, errors.Is(err, pgx.ErrNoRows))
}
