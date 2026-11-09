package users_test

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/users"
)

func TestNormalizeUsername(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"joao.silva", "joao.silva", false},
		{"  Joao.Silva  ", "joao.silva", false},
		{"ana_b-2", "ana_b-2", false},
		{"", "", false},
		{"   ", "", false},
		{"ab", "", true},                                 // curto demais
		{"a234567890123456789012345678901234", "", true}, // 34 > 32
		{"joao@x.com", "", true},                         // @ é o que separa do e-mail
		{"joão", "", true},                               // sem acento
		{"joao silva", "", true},                         // sem espaço
	}
	for _, c := range cases {
		got, err := users.NormalizeUsername(c.in)
		if c.wantErr {
			require.ErrorIs(t, err, users.ErrInvalidUsername, c.in)
			continue
		}
		require.NoError(t, err, c.in)
		require.Equal(t, c.want, got, c.in)
	}
}

func TestRepo_Username_CreateLookupUpdateClear(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)

	un := "joao.silva"
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "joao@example.com", Username: &un, PasswordHash: "$2y$10$abc",
		Role: "admin", Name: "João",
	})
	require.NoError(t, err)
	require.NotNil(t, u.Username)
	require.Equal(t, "joao.silva", *u.Username)

	// Lookup normaliza como o cadastro: espaço e maiúscula não importam.
	got, err := repo.GetByUsername(ctx, "  JOAO.Silva ")
	require.NoError(t, err)
	require.Equal(t, u.ID, got.ID)

	// Troca.
	novo := "jsilva"
	u, err = repo.Update(ctx, u.ID, users.UpdateInput{Username: &novo})
	require.NoError(t, err)
	require.Equal(t, "jsilva", *u.Username)
	_, err = repo.GetByUsername(ctx, "joao.silva")
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// "" remove.
	vazio := ""
	u, err = repo.Update(ctx, u.ID, users.UpdateInput{Username: &vazio})
	require.NoError(t, err)
	require.Nil(t, u.Username)
	_, err = repo.GetByUsername(ctx, "jsilva")
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestRepo_Username_UniqueAmongActive_ReusableAfterDelete(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)

	un := "maria"
	first, err := repo.Create(ctx, users.CreateInput{
		Email: "maria1@example.com", Username: &un, PasswordHash: "x",
		Role: "admin", Name: "Maria 1",
	})
	require.NoError(t, err)

	_, err = repo.Create(ctx, users.CreateInput{
		Email: "maria2@example.com", Username: &un, PasswordHash: "x",
		Role: "admin", Name: "Maria 2",
	})
	require.Error(t, err, "username repetido entre contas ativas tem que colidir")

	// Conta excluída libera o username (mesma regra do e-mail).
	require.NoError(t, repo.SoftDelete(ctx, first.ID))
	_, err = repo.Create(ctx, users.CreateInput{
		Email: "maria2@example.com", Username: &un, PasswordHash: "x",
		Role: "admin", Name: "Maria 2",
	})
	require.NoError(t, err)

	// E a excluída não é achada pelo login.
	got, err := repo.GetByUsername(ctx, "maria")
	require.NoError(t, err)
	require.NotEqual(t, first.ID, got.ID)
}

func TestRepo_Username_CheckRejectsUnnormalized(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)

	// O CHECK da 0070 é a última barreira: nada fora de NormalizeUsername entra,
	// nem por um caminho que esqueça de normalizar.
	bad := "Com@Arroba"
	_, err := repo.Create(ctx, users.CreateInput{
		Email: "x@example.com", Username: &bad, PasswordHash: "x",
		Role: "admin", Name: "X",
	})
	require.Error(t, err)
}

func TestRepo_List_SearchesUsername(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)

	un := "operador.norte"
	_, err := repo.Create(ctx, users.CreateInput{
		Email: "fulano@example.com", Username: &un, PasswordHash: "x",
		Role: "admin", Name: "Fulano",
	})
	require.NoError(t, err)

	list, total, err := repo.List(ctx, users.ListInput{Q: "Norte"})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, list, 1)
}
