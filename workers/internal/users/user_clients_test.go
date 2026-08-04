package users_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/users"
)

func TestSetClients_ReplacesWalletAndKeepsPrimary(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)

	var cliA, cliB, cliC uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('A') RETURNING id`).Scan(&cliA))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('B') RETURNING id`).Scan(&cliB))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('C') RETURNING id`).Scan(&cliC))

	u, err := repo.Create(ctx, users.CreateInput{
		Email: "agencia@example.com", PasswordHash: "x", Role: "viewer",
		ClientID: &cliA, Name: "Agência",
	})
	require.NoError(t, err)

	// O trigger da 0062 já deve ter criado o vínculo do principal.
	require.Equal(t, []uuid.UUID{cliA}, u.ClientIDs)

	// Amplia a carteira: principal (cliA) continua no conjunto, logo continua
	// sendo o principal.
	require.NoError(t, repo.SetClients(ctx, u.ID, []uuid.UUID{cliA, cliB}))
	got, err := repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{cliA, cliB}, got.ClientIDs)
	require.Equal(t, cliA, *got.ClientID)

	// Troca a carteira inteira: o principal antigo saiu, então o primeiro da
	// nova lista assume.
	require.NoError(t, repo.SetClients(ctx, u.ID, []uuid.UUID{cliC}))
	got, err = repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{cliC}, got.ClientIDs)
	require.Equal(t, cliC, *got.ClientID)
}

func TestSetClients_EmptyListRejected(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)

	var cli uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('A') RETURNING id`).Scan(&cli))
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "vazio@example.com", PasswordHash: "x", Role: "viewer",
		ClientID: &cli, Name: "Vazio",
	})
	require.NoError(t, err)

	require.Error(t, repo.SetClients(ctx, u.ID, nil))
}

func TestListFilterByClient_MatchesSecondaryLink(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)

	var cliA, cliB uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('A') RETURNING id`).Scan(&cliA))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ('B') RETURNING id`).Scan(&cliB))

	u, err := repo.Create(ctx, users.CreateInput{
		Email: "sec@example.com", PasswordHash: "x", Role: "viewer",
		ClientID: &cliA, Name: "Secundário",
	})
	require.NoError(t, err)
	require.NoError(t, repo.SetClients(ctx, u.ID, []uuid.UUID{cliA, cliB}))

	// Filtrar por cliB tem que achar o usuário, mesmo cliB sendo secundário.
	list, total, err := repo.List(ctx, users.ListInput{ClientID: &cliB})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, list, 1)
	require.Equal(t, u.ID, list[0].ID)
}
