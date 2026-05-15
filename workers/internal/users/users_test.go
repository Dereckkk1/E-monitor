package users_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/users"
)

func TestRepo_CreateAdmin_NoClientID(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)

	u, err := repo.Create(ctx, users.CreateInput{
		Email:        "admin@example.com",
		PasswordHash: "$2y$10$abc",
		Role:         "admin",
		Name:         "Admin Foo",
	})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, u.ID)
	require.Nil(t, u.ClientID)
	require.Equal(t, "admin@example.com", u.Email)
	require.Equal(t, "Admin Foo", u.Name)
}

func TestRepo_CreateViewer_RequiresClientID(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)

	c, err := clients.Create(ctx, catalog.CreateClientInput{Name: "Acme"})
	require.NoError(t, err)

	u, err := repo.Create(ctx, users.CreateInput{
		Email:        "user@acme.com",
		PasswordHash: "$2y$10$abc",
		Role:         "viewer",
		ClientID:     &c.ID,
		Name:         "Viewer Bar",
	})
	require.NoError(t, err)
	require.Equal(t, c.ID, *u.ClientID)
}

func TestRepo_CreateViewer_WithoutClientID_Errors(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)

	_, err := repo.Create(ctx, users.CreateInput{
		Email:        "broken@acme.com",
		PasswordHash: "$2y$10$abc",
		Role:         "viewer",
		Name:         "no client",
	})
	require.Error(t, err, "deve violar CHECK constraint")
}

func TestRepo_CreateAdmin_WithClientID_Errors(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)

	c, err := clients.Create(ctx, catalog.CreateClientInput{Name: "Acme"})
	require.NoError(t, err)

	_, err = repo.Create(ctx, users.CreateInput{
		Email:        "broken-admin@acme.com",
		PasswordHash: "$2y$10$abc",
		Role:         "admin",
		ClientID:     &c.ID,
		Name:         "no",
	})
	require.Error(t, err, "deve violar CHECK constraint")
}

func TestRepo_EmailReuse_AfterSoftDelete(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)

	u, err := repo.Create(ctx, users.CreateInput{
		Email: "reuse@example.com", PasswordHash: "$2y$10$x", Role: "admin", Name: "x",
	})
	require.NoError(t, err)

	_, err = repo.Create(ctx, users.CreateInput{
		Email: "reuse@example.com", PasswordHash: "$2y$10$x", Role: "admin", Name: "y",
	})
	require.Error(t, err)

	require.NoError(t, repo.SoftDelete(ctx, u.ID))

	_, err = repo.Create(ctx, users.CreateInput{
		Email: "reuse@example.com", PasswordHash: "$2y$10$x", Role: "admin", Name: "z",
	})
	require.NoError(t, err, "deve permitir reusar email após soft delete")
}

func TestRepo_GetByEmail_IgnoresDeleted(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)

	u, err := repo.Create(ctx, users.CreateInput{
		Email: "find@example.com", PasswordHash: "$2y$10$x", Role: "admin", Name: "x",
	})
	require.NoError(t, err)
	require.NoError(t, repo.SoftDelete(ctx, u.ID))

	_, err = repo.GetByEmail(ctx, "find@example.com")
	require.Error(t, err, "GetByEmail não deve retornar usuário deletado")
}

func TestRepo_TouchLastLogin(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)

	u, err := repo.Create(ctx, users.CreateInput{
		Email: "ll@example.com", PasswordHash: "$2y$10$x", Role: "admin", Name: "x",
	})
	require.NoError(t, err)
	require.Nil(t, u.LastLoginAt)

	require.NoError(t, repo.TouchLastLogin(ctx, u.ID))

	got, err := repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastLoginAt)
}

func TestRepo_List_FiltersByStatusAndRole(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)

	c, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Acme"})

	uA, _ := repo.Create(ctx, users.CreateInput{Email: "a@x.test", PasswordHash: "h", Role: "admin", Name: "A"})
	uV, _ := repo.Create(ctx, users.CreateInput{Email: "v@x.test", PasswordHash: "h", Role: "viewer", ClientID: &c.ID, Name: "V"})
	uD, _ := repo.Create(ctx, users.CreateInput{Email: "d@x.test", PasswordHash: "h", Role: "admin", Name: "D"})
	require.NoError(t, repo.SoftDelete(ctx, uD.ID))

	list, total, err := repo.List(ctx, users.ListInput{Status: "active", PageSize: 50})
	require.NoError(t, err)
	require.Equal(t, 2, total)
	ids := map[uuid.UUID]bool{}
	for _, u := range list {
		ids[u.ID] = true
	}
	require.True(t, ids[uA.ID])
	require.True(t, ids[uV.ID])
	require.False(t, ids[uD.ID])

	list, _, err = repo.List(ctx, users.ListInput{Role: "viewer", Status: "active", PageSize: 50})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, uV.ID, list[0].ID)
}

func TestRepo_Update_ClearClient(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)

	c, err := clients.Create(ctx, catalog.CreateClientInput{Name: "Acme"})
	require.NoError(t, err)

	u, err := repo.Create(ctx, users.CreateInput{
		Email: "promote@x.test", PasswordHash: "h", Role: "viewer",
		ClientID: &c.ID, Name: "P",
	})
	require.NoError(t, err)
	require.NotNil(t, u.ClientID)

	newRole := "admin"
	updated, err := repo.Update(ctx, u.ID, users.UpdateInput{
		Role:        &newRole,
		ClearClient: true,
	})
	require.NoError(t, err)
	require.Equal(t, "admin", updated.Role)
	require.Nil(t, updated.ClientID)
}

func TestRepo_SoftDelete_Idempotent(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)

	u, err := repo.Create(ctx, users.CreateInput{
		Email: "dd@x.test", PasswordHash: "h", Role: "admin", Name: "D",
	})
	require.NoError(t, err)

	require.NoError(t, repo.SoftDelete(ctx, u.ID))
	err = repo.SoftDelete(ctx, u.ID)
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestRepo_List_QSearchesNameAndEmail(t *testing.T) {
	ctx, pool := newTestDB(t)
	resetUsersAndClients(t, ctx, pool)
	repo := users.NewRepo(pool)

	_, err := repo.Create(ctx, users.CreateInput{
		Email: "alice@x.test", PasswordHash: "h", Role: "admin", Name: "Alice",
	})
	require.NoError(t, err)
	_, err = repo.Create(ctx, users.CreateInput{
		Email: "bob@y.test", PasswordHash: "h", Role: "admin", Name: "Robert",
	})
	require.NoError(t, err)

	// Match por nome
	list, total, err := repo.List(ctx, users.ListInput{Q: "alic", Status: "active"})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, "alice@x.test", list[0].Email)

	// Match por email
	list, total, err = repo.List(ctx, users.ListInput{Q: "bob@", Status: "active"})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, "Robert", list[0].Name)

	// Sem match
	_, total, err = repo.List(ctx, users.ListInput{Q: "xxx", Status: "active"})
	require.NoError(t, err)
	require.Equal(t, 0, total)
}
