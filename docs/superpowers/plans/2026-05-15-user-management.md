# Gerenciamento de usuários — plano de implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Adicionar CRUD de usuários no painel admin (`/admin/users`), tela "Minha conta" pra todos, e filtragem server-side por `client_id` em campanhas/veiculações/relatórios quando o usuário é cliente.

**Architecture:** Estende a tabela `users` com `client_id`, `name`, `phone`, `is_active`, `deleted_at`, `last_login_at`, `updated_at`. Reusa o role `viewer` do banco como "Cliente" no vocabulário da UI (admin/operator → "Administrador"; viewer → "Cliente"). JWT ganha claim `client_id`; middleware extrai e injeta no context; handlers aplicam `WHERE client_id = $scope` quando o requester é viewer. Frontend ganha `<RequireRole>` guard, hooks React Query novos, página `/admin/users` com tabela + modais, página `/account` self-service.

**Tech Stack:** Go (chi router, pgx/v5, jwt/v5, bcrypt), PostgreSQL, React 19, React Router v6, React Query, axios, react-select.

**Spec:** [docs/superpowers/specs/2026-05-15-user-management-design.md](../specs/2026-05-15-user-management-design.md)

---

## File Structure

**Backend (Go):**
- Create: `migrations/0027_user_management.up.sql`, `migrations/0027_user_management.down.sql` — schema changes.
- Create: `workers/internal/auth/scope.go` — extrai `client_id` do JWT claims pra injetar em queries.
- Create: `workers/internal/users/users.go` — repo da tabela `users` (CRUD + queries especializadas).
- Create: `workers/internal/users/users_test.go` — testes do repo.
- Create: `workers/internal/api/handlers/users.go` — handler admin de CRUD `/v1/internal/admin/users/*`.
- Create: `workers/internal/api/handlers/users_test.go` — testes do handler.
- Create: `workers/internal/api/handlers/me.go` — handler self-service `/v1/internal/auth/me*`.
- Create: `workers/internal/api/handlers/me_test.go` — testes self-service.
- Modify: `workers/internal/auth/jwt.go` — adiciona claim `ClientID`, função `IssueTokenForUser(u)`.
- Modify: `workers/internal/auth/jwt_test.go` — testes do novo claim.
- Modify: `workers/internal/api/handlers/auth.go` — bloqueia login quando `is_active=false` ou `deleted_at` setado, atualiza `last_login_at`, inclui `client_id` na resposta.
- Modify: `workers/internal/api/handlers/auth_test.go` — testes pros novos comportamentos do login.
- Modify: `workers/internal/api/router.go` — adiciona grupos `/admin/users`, `/auth/me`, e expande role gates dos endpoints read pra incluir `viewer`.
- Modify: `workers/internal/auth/bootstrap.go` — usa `IssueTokenForUser` (na verdade só insere `name = ''` no INSERT).
- Modify: `workers/internal/api/handlers/detections.go`, `campaigns.go`, `materials.go` — aplica scope quando `ClientScopeFromContext != nil`.

**Frontend (React):**
- Create: `frontend/src/components/RequireRole.jsx` — guard de rota por role.
- Create: `frontend/src/pages/AdminUsersPage.jsx` — tela CRUD admin.
- Create: `frontend/src/pages/AdminUsersPage.css` — estilos da tela.
- Create: `frontend/src/components/UserFormModal.jsx` — modal de criar/editar.
- Create: `frontend/src/components/ResetPasswordModal.jsx` — modal de reset de senha.
- Create: `frontend/src/pages/AccountPage.jsx` — tela "Minha conta".
- Create: `frontend/src/utils/passwordGen.js` — gerador de senha forte.
- Modify: `frontend/src/contexts/AuthContext.jsx` — expõe `clientId`, ajusta `isAdmin/isClient` pra contemplar `operator` e `viewer`.
- Modify: `frontend/src/components/Sidebar.jsx` — link novo "Usuários" em admin; ClientNav ganha "Campanhas" + "Minha conta".
- Modify: `frontend/src/api/hooks.js` — hooks novos (`useUsersPaged`, `useUser`, `useCreateUser`, etc).
- Modify: `frontend/src/App.jsx` — rota `/admin/users`, `/account`, gating com `<RequireRole>`.
- Modify: `frontend/src/pages/CampaignsPage.jsx`, `frontend/src/pages/DetectionsPage.jsx` — esconde botões de escrita quando `!isAdmin`.

**Docs:**
- Create: `docs/features/user-management.md` — documentação operacional da feature.
- Modify: `docs/operations/auth-bootstrap.md` — atualiza §8 removendo TODOs cumpridos.
- Modify: `CLAUDE.md` — adiciona linha no mapa de consulta.
- Modify: `docs/README.md` — adiciona entrada nova.

---

## Task 1: Migração de schema

**Files:**
- Create: `migrations/0027_user_management.up.sql`
- Create: `migrations/0027_user_management.down.sql`

- [ ] **Step 1: Confirmar próximo número de migração**

Run: `ls migrations/*.up.sql | sort | tail -3`
Expected: última versão é `0026_material_script.up.sql`. Próxima é `0027`.

- [ ] **Step 2: Escrever a migração UP**

Cria `migrations/0027_user_management.up.sql`:

```sql
-- Adiciona campos de gerenciamento de usuários:
--   client_id     vínculo N:1 a clientes (NOT NULL pra viewer; NULL pra admin/operator)
--   name, phone   dados pessoais
--   is_active     desativar reversível (login bloqueado)
--   deleted_at    soft delete definitivo
--   last_login_at telemetria de uso (preenchido em login bem-sucedido)
--   updated_at    audit
ALTER TABLE users
  ADD COLUMN client_id     UUID REFERENCES clients(id) ON DELETE RESTRICT,
  ADD COLUMN name          TEXT NOT NULL DEFAULT '',
  ADD COLUMN phone         TEXT,
  ADD COLUMN is_active     BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN deleted_at    TIMESTAMPTZ,
  ADD COLUMN last_login_at TIMESTAMPTZ,
  ADD COLUMN updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- Cliente (viewer) DEVE ter client_id; admin/operator NÃO podem ter.
ALTER TABLE users
  ADD CONSTRAINT users_client_role_consistency CHECK (
    (role = 'viewer'  AND client_id IS NOT NULL) OR
    (role IN ('admin','operator') AND client_id IS NULL)
  );

CREATE INDEX idx_users_client_id ON users(client_id) WHERE client_id IS NOT NULL;
CREATE INDEX idx_users_active    ON users(is_active) WHERE deleted_at IS NULL;

-- UNIQUE parcial substitui o UNIQUE original em email.
-- Permite reusar email após exclusão (a linha antiga sai do índice porque
-- deleted_at deixou de ser NULL).
ALTER TABLE users DROP CONSTRAINT users_email_key;
CREATE UNIQUE INDEX idx_users_email_active
  ON users(LOWER(email)) WHERE deleted_at IS NULL;
```

- [ ] **Step 3: Escrever a migração DOWN**

Cria `migrations/0027_user_management.down.sql`:

```sql
-- Reverte indexes/constraints novos
DROP INDEX IF EXISTS idx_users_email_active;
DROP INDEX IF EXISTS idx_users_active;
DROP INDEX IF EXISTS idx_users_client_id;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_client_role_consistency;

-- Restaura UNIQUE original em email (sem case-fold; pode falhar se houver
-- emails duplicados case-insensitive — verificar antes de rodar down em prod)
ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);

-- Drop colunas
ALTER TABLE users
  DROP COLUMN IF EXISTS updated_at,
  DROP COLUMN IF EXISTS last_login_at,
  DROP COLUMN IF EXISTS deleted_at,
  DROP COLUMN IF EXISTS is_active,
  DROP COLUMN IF EXISTS phone,
  DROP COLUMN IF EXISTS name,
  DROP COLUMN IF EXISTS client_id;
```

- [ ] **Step 4: Subir o stack local e verificar a migração**

Run:
```bash
docker compose -f infra/docker/docker-compose.yml up -d --build migrate postgres
docker compose -f infra/docker/docker-compose.yml logs migrate | tail -10
```
Expected: log mostra `27/u user_management` sem erro; `schema_migrations` tem `version=27, dirty=false`.

- [ ] **Step 5: Verificar bootstrap admin continua válido**

Run:
```bash
docker compose -f infra/docker/docker-compose.yml exec postgres \
  psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
  -c "SELECT email, role, client_id IS NULL AS no_client FROM users;"
```
Expected: linha do admin com `role='admin'`, `no_client=t` (passa no CHECK).

- [ ] **Step 6: Commit**

```bash
git add migrations/0027_user_management.up.sql migrations/0027_user_management.down.sql
git commit -m "migration: estende users com client_id, soft delete e telemetria

Adiciona client_id (FK clients ON DELETE RESTRICT), name, phone,
is_active, deleted_at, last_login_at, updated_at. CHECK de
consistência: viewer exige client_id; admin/operator proíbem.
UNIQUE parcial em LOWER(email) WHERE deleted_at IS NULL permite
reusar email após exclusão.

Spec: docs/superpowers/specs/2026-05-15-user-management-design.md"
```

---

## Task 2: Repo backend `users` package

**Files:**
- Create: `workers/internal/users/users.go`
- Create: `workers/internal/users/users_test.go`

- [ ] **Step 1: Escrever os testes primeiro (TDD)**

Cria `workers/internal/users/users_test.go`:

```go
package users_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/catalog"
	"radiocheck/internal/users"
)

// testPool helper já existe no projeto (catalog/testhelpers_test.go).
// Aqui vamos usar a mesma estratégia: assumir DSN em TEST_DATABASE_URL.

func newTestRepo(t *testing.T) (*users.Repo, *catalog.Clients, func()) {
	t.Helper()
	pool, cleanup := catalog.NewTestPool(t)  // helper público do package catalog
	return users.NewRepo(pool), catalog.NewClients(pool), cleanup
}

func TestRepo_CreateAdmin_NoClientID(t *testing.T) {
	repo, _, cleanup := newTestRepo(t)
	defer cleanup()

	u, err := repo.Create(context.Background(), users.CreateInput{
		Email:        "admin@example.com",
		PasswordHash: "$2y$10$abc",  // bcrypt fake; repo só persiste
		Role:         "admin",
		Name:         "Admin Foo",
	})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, u.ID)
	require.Nil(t, u.ClientID)
}

func TestRepo_CreateViewer_RequiresClientID(t *testing.T) {
	repo, clients, cleanup := newTestRepo(t)
	defer cleanup()

	c, err := clients.Create(context.Background(), catalog.CreateClientInput{Name: "Acme"})
	require.NoError(t, err)

	u, err := repo.Create(context.Background(), users.CreateInput{
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
	repo, _, cleanup := newTestRepo(t)
	defer cleanup()

	_, err := repo.Create(context.Background(), users.CreateInput{
		Email:        "broken@acme.com",
		PasswordHash: "$2y$10$abc",
		Role:         "viewer",
		Name:         "no client",
	})
	require.Error(t, err, "deve violar CHECK constraint")
}

func TestRepo_CreateAdmin_WithClientID_Errors(t *testing.T) {
	repo, clients, cleanup := newTestRepo(t)
	defer cleanup()

	c, err := clients.Create(context.Background(), catalog.CreateClientInput{Name: "Acme"})
	require.NoError(t, err)

	_, err = repo.Create(context.Background(), users.CreateInput{
		Email:        "broken-admin@acme.com",
		PasswordHash: "$2y$10$abc",
		Role:         "admin",
		ClientID:     &c.ID,
		Name:         "no",
	})
	require.Error(t, err, "deve violar CHECK constraint")
}

func TestRepo_EmailReuse_AfterSoftDelete(t *testing.T) {
	repo, _, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()

	u, err := repo.Create(ctx, users.CreateInput{
		Email: "reuse@example.com", PasswordHash: "$2y$10$x", Role: "admin", Name: "x",
	})
	require.NoError(t, err)

	// Tentar criar outro com mesmo email → falha (UNIQUE parcial)
	_, err = repo.Create(ctx, users.CreateInput{
		Email: "reuse@example.com", PasswordHash: "$2y$10$x", Role: "admin", Name: "y",
	})
	require.Error(t, err)

	// Soft delete
	require.NoError(t, repo.SoftDelete(ctx, u.ID))

	// Agora consegue criar com o mesmo email
	_, err = repo.Create(ctx, users.CreateInput{
		Email: "reuse@example.com", PasswordHash: "$2y$10$x", Role: "admin", Name: "z",
	})
	require.NoError(t, err, "deve permitir reusar email após soft delete")
}

func TestRepo_GetByEmail_IgnoresDeleted(t *testing.T) {
	repo, _, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()

	u, err := repo.Create(ctx, users.CreateInput{
		Email: "find@example.com", PasswordHash: "$2y$10$x", Role: "admin", Name: "x",
	})
	require.NoError(t, err)
	require.NoError(t, repo.SoftDelete(ctx, u.ID))

	_, err = repo.GetByEmail(ctx, "find@example.com")
	require.Error(t, err, "GetByEmail não deve retornar usuário deletado")
}

func TestRepo_TouchLastLogin(t *testing.T) {
	repo, _, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()

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
	repo, clients, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()

	c, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Acme"})

	uA, _ := repo.Create(ctx, users.CreateInput{Email: "a@x", PasswordHash: "h", Role: "admin", Name: "A"})
	uV, _ := repo.Create(ctx, users.CreateInput{Email: "v@x", PasswordHash: "h", Role: "viewer", ClientID: &c.ID, Name: "V"})
	uD, _ := repo.Create(ctx, users.CreateInput{Email: "d@x", PasswordHash: "h", Role: "admin", Name: "D"})
	require.NoError(t, repo.SoftDelete(ctx, uD.ID))

	// status=active deve omitir uD
	list, total, err := repo.List(ctx, users.ListInput{Status: "active", PageSize: 50})
	require.NoError(t, err)
	require.Equal(t, 2, total)
	ids := map[uuid.UUID]bool{}
	for _, u := range list { ids[u.ID] = true }
	require.True(t, ids[uA.ID])
	require.True(t, ids[uV.ID])
	require.False(t, ids[uD.ID])

	// role=viewer
	list, _, err = repo.List(ctx, users.ListInput{Role: "viewer", Status: "active", PageSize: 50})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, uV.ID, list[0].ID)
}
```

(Ver `workers/internal/catalog/testhelpers_test.go` se `NewTestPool` precisa virar exported. Se for unexported, criar `workers/internal/catalog/testpool_export_test.go` ou exportá-lo via wrapper público.)

- [ ] **Step 2: Rodar os testes — devem falhar (package não existe)**

Run: `cd workers && go test ./internal/users/...`
Expected: build fail (`package radiocheck/internal/users does not exist`).

- [ ] **Step 3: Implementar o repo**

Cria `workers/internal/users/users.go`:

```go
// Package users implementa o repositório da tabela `users`.
//
// Nota de vocabulário: o role 'viewer' do banco corresponde ao "Cliente"
// na UI (vê apenas dados do próprio client_id). 'admin' e 'operator' são
// tratados como sinônimos no middleware de autorização e ambos viram
// "Administrador" na UI. Veja docs/features/user-management.md.
package users

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// User é a projeção completa de uma linha em users.
type User struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	PasswordHash string     `json:"-"`  // nunca serializa
	Role         string     `json:"role"`
	ClientID     *uuid.UUID `json:"client_id,omitempty"`
	Name         string     `json:"name"`
	Phone        *string    `json:"phone,omitempty"`
	IsActive     bool       `json:"is_active"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// Repo é o repositório de usuários.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo retorna um novo repo backed by pool.
func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

const userColumns = `id, email, password_hash, role, client_id, name, phone,
                     is_active, deleted_at, last_login_at, created_at, updated_at`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.ClientID,
		&u.Name, &u.Phone, &u.IsActive, &u.DeletedAt, &u.LastLoginAt,
		&u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateInput agrupa os campos pra inserir um usuário.
// PasswordHash já deve vir bcrypted; o repo não hashifica.
type CreateInput struct {
	Email        string
	PasswordHash string
	Role         string  // 'admin', 'operator' ou 'viewer'
	ClientID     *uuid.UUID
	Name         string
	Phone        *string
}

// Create insere um novo usuário. Retorna erro se a constraint de
// consistência role↔client_id falhar, ou se o email já existir entre
// não-deletados.
func (r *Repo) Create(ctx context.Context, in CreateInput) (*User, error) {
	row := r.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, client_id, name, phone)
		 VALUES (LOWER($1), $2, $3, $4, $5, $6)
		 RETURNING `+userColumns,
		in.Email, in.PasswordHash, in.Role, in.ClientID, in.Name, in.Phone,
	)
	return scanUser(row)
}

// Get busca por ID (inclui deletados). Retorna pgx.ErrNoRows se não achar.
func (r *Repo) Get(ctx context.Context, id uuid.UUID) (*User, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}

// GetByEmail busca por email entre não-deletados. Retorna pgx.ErrNoRows
// se não achar ou se o usuário foi soft-deletado.
func (r *Repo) GetByEmail(ctx context.Context, email string) (*User, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users
		 WHERE LOWER(email) = LOWER($1) AND deleted_at IS NULL`, email)
	return scanUser(row)
}

// UpdateInput permite mudar campos opcionais. Pointer == nil → não muda.
// Email é imutável (decisão da P6 do spec).
type UpdateInput struct {
	Name      *string
	Phone     *string
	Role      *string
	ClientID  *uuid.UUID  // nil só significa "não mudar" — se quiser zerar, precisa de outra API
	ClearClient bool       // true → set client_id = NULL (admin/operator)
	IsActive  *bool
}

// Update aplica as mudanças não-nulas.
func (r *Repo) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*User, error) {
	// Build SET clauses dinâmicos. Mantém ordem determinística.
	sets := []string{"updated_at = NOW()"}
	args := []any{}
	idx := 1
	push := func(clause string, val any) {
		sets = append(sets, clause+" = $"+itoa(idx))
		args = append(args, val)
		idx++
	}
	if in.Name != nil { push("name", *in.Name) }
	if in.Phone != nil { push("phone", *in.Phone) }
	if in.Role != nil { push("role", *in.Role) }
	if in.ClearClient {
		sets = append(sets, "client_id = NULL")
	} else if in.ClientID != nil {
		push("client_id", *in.ClientID)
	}
	if in.IsActive != nil { push("is_active", *in.IsActive) }

	args = append(args, id)
	q := `UPDATE users SET ` + strings.Join(sets, ", ") +
		` WHERE id = $` + itoa(idx) +
		` RETURNING ` + userColumns
	row := r.pool.QueryRow(ctx, q, args...)
	return scanUser(row)
}

// SetPassword troca o hash de senha (sem outras mudanças).
func (r *Repo) SetPassword(ctx context.Context, id uuid.UUID, newHash string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2`,
		newHash, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SoftDelete marca deleted_at = NOW() e força is_active = false.
func (r *Repo) SoftDelete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET deleted_at = NOW(), is_active = false, updated_at = NOW()
		 WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// TouchLastLogin atualiza last_login_at = NOW() (chamado em login OK).
func (r *Repo) TouchLastLogin(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET last_login_at = NOW() WHERE id = $1`, id)
	return err
}

// ListInput aceita filtros do endpoint GET /admin/users.
type ListInput struct {
	Role     string  // "" | "admin" | "viewer" | "operator"
	ClientID *uuid.UUID
	Status   string  // "" (= active) | "active" | "inactive" | "deleted" | "all"
	Q        string  // busca livre em name + email
	Page     int     // 1-based; 0 → 1
	PageSize int     // default 20, máximo 100
}

// List retorna a página filtrada e o total que casa o filtro (sem paginação).
func (r *Repo) List(ctx context.Context, in ListInput) ([]User, int, error) {
	if in.PageSize <= 0 { in.PageSize = 20 }
	if in.PageSize > 100 { in.PageSize = 100 }
	if in.Page <= 0 { in.Page = 1 }

	where := []string{"1=1"}
	args := []any{}
	idx := 1
	push := func(clause string, val any) {
		where = append(where, strings.ReplaceAll(clause, "?", "$"+itoa(idx)))
		args = append(args, val)
		idx++
	}

	switch in.Status {
	case "", "active":
		where = append(where, "deleted_at IS NULL", "is_active = TRUE")
	case "inactive":
		where = append(where, "deleted_at IS NULL", "is_active = FALSE")
	case "deleted":
		where = append(where, "deleted_at IS NOT NULL")
	case "all":
		// sem filtro de status
	default:
		return nil, 0, errors.New("invalid status filter")
	}
	if in.Role != "" {
		push("role = ?", in.Role)
	}
	if in.ClientID != nil {
		push("client_id = ?", *in.ClientID)
	}
	if in.Q != "" {
		// busca livre: name OR email contém Q (case-insensitive).
		clause := "(LOWER(name) LIKE ? OR LOWER(email) LIKE ?)"
		like := "%" + strings.ToLower(in.Q) + "%"
		where = append(where,
			strings.Replace(clause, "?", "$"+itoa(idx), 1))
		// rebuild com 2 placeholders
		where[len(where)-1] = "(LOWER(name) LIKE $" + itoa(idx) +
			" OR LOWER(email) LIKE $" + itoa(idx+1) + ")"
		args = append(args, like, like)
		idx += 2
	}

	whereSQL := strings.Join(where, " AND ")

	// total
	var total int
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// page
	args = append(args, in.PageSize, (in.Page-1)*in.PageSize)
	q := `SELECT ` + userColumns + ` FROM users WHERE ` + whereSQL +
		` ORDER BY created_at DESC LIMIT $` + itoa(idx) + ` OFFSET $` + itoa(idx+1)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *u)
	}
	return out, total, rows.Err()
}

// itoa simples sem alocar (substitui strconv.Itoa pra mostrar no plano).
func itoa(i int) string {
	// implementação real: import "strconv"; return strconv.Itoa(i)
	// — substituir aqui no commit final.
	return [...]string{"0","1","2","3","4","5","6","7","8","9","10","11","12","13","14","15","16","17","18","19","20"}[i]
}
```

> **Nota:** o `itoa` placeholder acima é só pro plano caber. No commit real, substitua por `strconv.Itoa(i)` (`import "strconv"`).

- [ ] **Step 4: Rodar os testes — devem passar**

Run: `cd workers && go test ./internal/users/... -count=1 -v`
Expected: todos PASS. Se `NewTestPool` não estiver exportado, criar `workers/internal/catalog/testpool.go` que exporta uma função pública só pra testes (`//go:build testpool` build tag) ou usar fixture local mínimo.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/users/
git commit -m "users: novo repo com CRUD, soft delete e telemetria de login

Implementa users.Repo com Create/Get/GetByEmail/Update/SetPassword/
SoftDelete/TouchLastLogin/List. CHECK constraint do banco garante
consistência role↔client_id; UNIQUE parcial em LOWER(email) WHERE
deleted_at IS NULL permite reusar email após exclusão."
```

---

## Task 3: JWT — adiciona claim client_id

**Files:**
- Modify: `workers/internal/auth/jwt.go`
- Modify: `workers/internal/auth/jwt_test.go`

- [ ] **Step 1: Estender o teste do JWT**

Adicionar em `workers/internal/auth/jwt_test.go` (ou criar se não existir conteúdo equivalente):

```go
func TestIssueAndParse_WithClientID(t *testing.T) {
	t.Setenv("JWT_SECRET", "this-is-a-32-char-secret-for-test!")
	uid := uuid.New()
	cid := uuid.New()
	tok, err := auth.IssueTokenForUser(&users.User{
		ID: uid, Role: "viewer", ClientID: &cid,
	})
	require.NoError(t, err)
	c, err := auth.ParseToken(tok)
	require.NoError(t, err)
	require.Equal(t, uid, c.UserID)
	require.Equal(t, "viewer", c.Role)
	require.NotNil(t, c.ClientID)
	require.Equal(t, cid, *c.ClientID)
}

func TestIssueAndParse_AdminHasNilClientID(t *testing.T) {
	t.Setenv("JWT_SECRET", "this-is-a-32-char-secret-for-test!")
	tok, err := auth.IssueTokenForUser(&users.User{
		ID: uuid.New(), Role: "admin",
	})
	require.NoError(t, err)
	c, err := auth.ParseToken(tok)
	require.NoError(t, err)
	require.Nil(t, c.ClientID)
}
```

- [ ] **Step 2: Rodar — deve falhar**

Run: `cd workers && go test ./internal/auth/... -run JWT -v`
Expected: build fail (`IssueTokenForUser` não existe; `Claims.ClientID` não existe).

- [ ] **Step 3: Atualizar `workers/internal/auth/jwt.go`**

```go
package auth

import (
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"radiocheck/internal/users"
)

type Claims struct {
	UserID   uuid.UUID  `json:"user_id"`
	Role     string     `json:"role"`
	ClientID *uuid.UUID `json:"client_id,omitempty"`
	jwt.RegisteredClaims
}

func getSecret() ([]byte, error) {
	s := os.Getenv("JWT_SECRET")
	if len(s) < 32 {
		return nil, errors.New("JWT_SECRET must be at least 32 bytes")
	}
	return []byte(s), nil
}

// IssueTokenForUser gera o JWT a partir de uma struct users.User. Sempre
// inclui o claim client_id quando o user é viewer; nil caso contrário.
func IssueTokenForUser(u *users.User) (string, error) {
	secret, err := getSecret()
	if err != nil {
		return "", err
	}
	claims := Claims{
		UserID:   u.ID,
		Role:     u.Role,
		ClientID: u.ClientID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(secret)
}

// IssueToken é mantido pra compatibilidade com bootstrap.go e testes legados.
// Não inclui client_id — admin/operator nunca tem.
//
// Deprecated: use IssueTokenForUser.
func IssueToken(userID uuid.UUID, role string) (string, error) {
	return IssueTokenForUser(&users.User{ID: userID, Role: role})
}

func ParseToken(tokenStr string) (*Claims, error) {
	secret, err := getSecret()
	if err != nil {
		return nil, err
	}
	tok, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok || t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected signing method")
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
```

- [ ] **Step 4: Rodar testes — devem passar**

Run: `cd workers && go test ./internal/auth/... -count=1 -v`
Expected: PASS, incluindo testes legados (compatibilidade preservada).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/auth/jwt.go workers/internal/auth/jwt_test.go
git commit -m "auth: JWT carrega client_id pra usuários cliente

Adiciona claim opcional client_id ao Claims. Nova função
IssueTokenForUser(*users.User) substitui IssueToken (mantida
deprecated pra compat). ParseToken já popula o novo campo
automaticamente porque é decode JSON."
```

---

## Task 4: Middleware de scope (`auth/scope.go`)

**Files:**
- Create: `workers/internal/auth/scope.go`
- Create: `workers/internal/auth/scope_test.go`

- [ ] **Step 1: Escrever o teste**

`workers/internal/auth/scope_test.go`:

```go
package auth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
)

func TestClientScopeFromContext_Viewer(t *testing.T) {
	cid := uuid.New()
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{
		Role: "viewer", ClientID: &cid,
	})
	got := auth.ClientScopeFromContext(ctx)
	require.NotNil(t, got)
	require.Equal(t, cid, *got)
}

func TestClientScopeFromContext_Admin_ReturnsNil(t *testing.T) {
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{Role: "admin"})
	require.Nil(t, auth.ClientScopeFromContext(ctx))
}

func TestClientScopeFromContext_NoClaims(t *testing.T) {
	require.Nil(t, auth.ClientScopeFromContext(context.Background()))
}
```

- [ ] **Step 2: Rodar — deve falhar**

Run: `cd workers && go test ./internal/auth/... -run Scope -v`
Expected: build fail (`ClientScopeFromContext`, `ContextWithClaims` não existem).

- [ ] **Step 3: Implementar `scope.go`**

`workers/internal/auth/scope.go`:

```go
package auth

import (
	"context"

	"github.com/google/uuid"
)

// ContextWithClaims é exposto pra testes (e qualquer caller que queira
// montar um context com claims pré-definidas, ex: testes integrados de
// handlers). Em produção, o RequireJWT middleware é quem popula isso.
func ContextWithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

// ClientScopeFromContext retorna o client_id do JWT se o requester for
// viewer (cliente), ou nil se for admin/operator/anônimo. Use em handlers
// que precisam filtrar por cliente quando o requester é cliente.
//
// Convenção: nil = "sem scope" = pode ver tudo.
func ClientScopeFromContext(ctx context.Context) *uuid.UUID {
	c, ok := ClaimsFromContext(ctx)
	if !ok {
		return nil
	}
	if c.Role != "viewer" {
		return nil
	}
	return c.ClientID
}
```

- [ ] **Step 4: Rodar — devem passar**

Run: `cd workers && go test ./internal/auth/... -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/auth/scope.go workers/internal/auth/scope_test.go
git commit -m "auth: middleware de scope por client_id

ClientScopeFromContext extrai client_id do JWT pra usuários viewer
e retorna nil pra admin/operator. Handlers downstream usam pra
adicionar WHERE client_id = \$scope quando há scope ativo."
```

---

## Task 5: Atualizar login handler

**Files:**
- Modify: `workers/internal/api/handlers/auth.go`
- Modify: `workers/internal/api/handlers/auth_test.go`

- [ ] **Step 1: Estender testes**

Adicionar em `auth_test.go` casos:
- Login OK pra viewer retorna `client_id` no body e atualiza `last_login_at`.
- Login com `is_active=false` retorna 403 `account_disabled`.
- Login com `deleted_at NOT NULL` retorna 401 `invalid credentials` (mantém comportamento conservador — não diferencia "deletado" de "não existe" pra evitar oracle).

```go
func TestLogin_DisabledAccount(t *testing.T) {
	pool, cleanup := newTestPool(t)
	defer cleanup()
	repo := users.NewRepo(pool)
	ctx := context.Background()

	hash, _ := bcrypt.GenerateFromPassword([]byte("super-secret-pw-12345"), 10)
	u, _ := repo.Create(ctx, users.CreateInput{
		Email: "off@example.com", PasswordHash: string(hash),
		Role: "admin", Name: "Off",
	})
	disabled := false
	_, _ = repo.Update(ctx, u.ID, users.UpdateInput{IsActive: &disabled})

	t.Setenv("JWT_SECRET", "this-is-a-32-char-secret-for-test!")
	h := handlers.NewAuthHandler(pool, repo)
	body := strings.NewReader(`{"email":"off@example.com","password":"super-secret-pw-12345"}`)
	req := httptest.NewRequest("POST", "/login", body)
	w := httptest.NewRecorder()
	h.Login(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "account_disabled")
}

func TestLogin_TouchesLastLoginAndReturnsClientID(t *testing.T) {
	// ... cria cliente, viewer com client_id, login OK, valida resposta
	// inclui client_id e que last_login_at é populado.
}
```

- [ ] **Step 2: Rodar — devem falhar**

Run: `cd workers && go test ./internal/api/handlers/... -run Login -v`
Expected: fails (`NewAuthHandler` ainda só recebe `*pgxpool.Pool`; sem checagem de is_active).

- [ ] **Step 3: Atualizar `auth.go`**

```go
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/auth"
	"radiocheck/internal/users"
)

type AuthHandler struct {
	db    *pgxpool.Pool
	users *users.Repo
}

func NewAuthHandler(db *pgxpool.Pool, repo *users.Repo) *AuthHandler {
	return &AuthHandler{db: db, users: repo}
}

type loginUserPayload struct {
	ID       any    `json:"id"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	ClientID *any   `json:"client_id,omitempty"`
	Name     string `json:"name"`
}

type loginResponse struct {
	Token     string           `json:"token"`
	ExpiresAt time.Time        `json:"expires_at"`
	User      loginUserPayload `json:"user"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	u, err := h.users.GetByEmail(r.Context(), body.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(body.Password)) != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if !u.IsActive {
		// Conta desativada (soft) — distinguimos de credencial inválida porque
		// o usuário precisa saber que existe mas está bloqueada (procurar
		// admin). Deletadas caem em pgx.ErrNoRows acima (GetByEmail filtra).
		http.Error(w, "account_disabled", http.StatusForbidden)
		return
	}

	tok, err := auth.IssueTokenForUser(u)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	claims, err := auth.ParseToken(tok)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	expiresAt := time.Now().Add(8 * time.Hour)
	if claims.RegisteredClaims.ExpiresAt != nil {
		expiresAt = claims.RegisteredClaims.ExpiresAt.Time
	}

	// Não bloqueia o login se touch falhar — telemetria não é critical path.
	_ = h.users.TouchLastLogin(r.Context(), u.ID)

	resp := loginResponse{Token: tok, ExpiresAt: expiresAt}
	resp.User.ID = u.ID
	resp.User.Email = u.Email
	resp.User.Role = u.Role
	resp.User.Name = u.Name
	if u.ClientID != nil {
		v := any(*u.ClientID)
		resp.User.ClientID = &v
	}

	writeJSON(w, http.StatusOK, resp)
}
```

- [ ] **Step 4: Atualizar callsite em `cmd/api/main.go` (ou onde NewAuthHandler é chamado)**

Run: `cd workers && grep -rn "NewAuthHandler" cmd/`
Expected: 1 ou 2 callsites. Adicionar `users.NewRepo(pool)` como segundo arg em todos.

- [ ] **Step 5: Rodar — devem passar**

Run: `cd workers && go test ./internal/api/handlers/... -run Login -v -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/api/handlers/auth.go workers/internal/api/handlers/auth_test.go workers/cmd/api/
git commit -m "auth: login carrega client_id, bloqueia desativados, atualiza last_login_at

Login handler agora usa users.Repo. Bloqueia conta com is_active=false
(403 account_disabled). Conta deletada cai em invalid credentials.
Resposta inclui client_id e name no objeto user. last_login_at é
atualizado em background — falha de telemetria não bloqueia login."
```

---

## Task 6: Handler self-service `/auth/me*`

**Files:**
- Create: `workers/internal/api/handlers/me.go`
- Create: `workers/internal/api/handlers/me_test.go`

- [ ] **Step 1: Escrever testes**

`me_test.go`:

```go
func TestMe_Get(t *testing.T) {
	// cria user admin, monta context com claims, chama GET /me, valida JSON
}

func TestMe_Patch_NameAndPhone(t *testing.T) {
	// PATCH com {name, phone} aplica; PATCH com {role} é ignorado silenciosamente.
}

func TestMe_ChangePassword_RequiresCurrent(t *testing.T) {
	// POST /me/password sem current_password → 400
	// com current_password errado → 401
	// com correto + nova senha < 12 chars → 400
	// happy path → 204
}
```

- [ ] **Step 2: Rodar — fails**

Run: `cd workers && go test ./internal/api/handlers/... -run Me -v`
Expected: build fail.

- [ ] **Step 3: Implementar `me.go`**

```go
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/auth"
	"radiocheck/internal/users"
)

type MeHandler struct {
	users *users.Repo
}

func NewMeHandler(repo *users.Repo) *MeHandler { return &MeHandler{users: repo} }

func (h *MeHandler) Get(w http.ResponseWriter, r *http.Request) {
	c, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	u, err := h.users.Get(r.Context(), c.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not_found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type mePatchPayload struct {
	Name  *string `json:"name,omitempty"`
	Phone *string `json:"phone,omitempty"`
}

func (h *MeHandler) Patch(w http.ResponseWriter, r *http.Request) {
	c, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var p mePatchPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	u, err := h.users.Update(r.Context(), c.UserID, users.UpdateInput{
		Name: p.Name, Phone: p.Phone,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type changePwdPayload struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

const minPasswordLen = 12

func (h *MeHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	c, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var p changePwdPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if p.CurrentPassword == "" || p.NewPassword == "" {
		http.Error(w, "missing_fields", http.StatusBadRequest)
		return
	}
	if len(p.NewPassword) < minPasswordLen {
		http.Error(w, "password_too_short", http.StatusBadRequest)
		return
	}
	u, err := h.users.Get(r.Context(), c.UserID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(p.CurrentPassword)) != nil {
		http.Error(w, "invalid_current_password", http.StatusUnauthorized)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(p.NewPassword), 10)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.users.SetPassword(r.Context(), c.UserID, string(hash)); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Rodar — devem passar**

Run: `cd workers && go test ./internal/api/handlers/... -run Me -v -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/me.go workers/internal/api/handlers/me_test.go
git commit -m "auth: handler self-service /auth/me

GET /me retorna o próprio usuário; PATCH /me edita name/phone (role
e client_id são imutáveis pelo próprio usuário); POST /me/password
exige current_password + new_password >= 12 chars."
```

---

## Task 7: Handler admin de CRUD `/admin/users/*`

**Files:**
- Create: `workers/internal/api/handlers/users.go`
- Create: `workers/internal/api/handlers/users_test.go`

- [ ] **Step 1: Escrever testes pros 6 endpoints**

```go
// TestUsers_List_FiltersAndPaginate
// TestUsers_Get_NotFound
// TestUsers_Create_Admin_NoClientID_OK
// TestUsers_Create_Client_RequiresClientID
// TestUsers_Create_Client_RoleConvertsToViewer
// TestUsers_Create_DuplicateEmail_409
// TestUsers_Create_PasswordTooShort_400
// TestUsers_Patch_RejectsEmail
// TestUsers_Patch_CannotReactivateDeleted
// TestUsers_ResetPassword_OK
// TestUsers_Delete_BlocksSelf
// TestUsers_Delete_OK_AndIdempotent
```

(implementação completa segue o padrão de `material_types_test.go` — usar `httptest`, `chi.NewRouter`, `auth.ContextWithClaims` pra simular admin caller.)

- [ ] **Step 2: Rodar — fails**

Run: `cd workers && go test ./internal/api/handlers/... -run Users -v`
Expected: build fail.

- [ ] **Step 3: Implementar `users.go`**

```go
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"radiocheck/internal/auth"
	"radiocheck/internal/users"
)

type UsersHandler struct {
	repo *users.Repo
}

func NewUsersHandler(repo *users.Repo) *UsersHandler { return &UsersHandler{repo: repo} }

// roleAlias converte o vocabulário externo ("client" / "admin") pro role do
// banco ("viewer" / "admin"). 'operator' não é aceito via API por ora.
func roleAlias(in string) (string, bool) {
	switch in {
	case "admin":
		return "admin", true
	case "client":
		return "viewer", true
	}
	return "", false
}

func (h *UsersHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := users.ListInput{
		Q:        q.Get("q"),
		Status:   q.Get("status"),
		Role:     "", // mapeia abaixo
		Page:     atoiOr(q.Get("page"), 1),
		PageSize: atoiOr(q.Get("page_size"), 20),
	}
	if rawRole := q.Get("role"); rawRole != "" {
		if dbRole, ok := roleAlias(rawRole); ok {
			in.Role = dbRole
		} else if rawRole == "operator" {
			in.Role = "operator"
		} else {
			http.Error(w, "invalid_role_filter", http.StatusBadRequest)
			return
		}
	}
	if rawCID := q.Get("client_id"); rawCID != "" {
		cid, err := uuid.Parse(rawCID)
		if err != nil {
			http.Error(w, "invalid_client_id", http.StatusBadRequest)
			return
		}
		in.ClientID = &cid
	}
	list, total, err := h.repo.List(r.Context(), in)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	totalPages := (total + in.PageSize - 1) / in.PageSize
	writeJSON(w, http.StatusOK, map[string]any{
		"data":        list,
		"total":       total,
		"total_pages": totalPages,
		"page":        in.Page,
		"page_size":   in.PageSize,
	})
}

func (h *UsersHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid_id", http.StatusBadRequest)
		return
	}
	u, err := h.repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not_found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type createUserPayload struct {
	Email    string     `json:"email"`
	Password string     `json:"password"`
	Name     string     `json:"name"`
	Phone    *string    `json:"phone,omitempty"`
	Role     string     `json:"role"`       // "admin" | "client"
	ClientID *uuid.UUID `json:"client_id,omitempty"`
}

func (h *UsersHandler) Create(w http.ResponseWriter, r *http.Request) {
	var p createUserPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if p.Email == "" || p.Password == "" || p.Name == "" {
		http.Error(w, "missing_fields", http.StatusBadRequest)
		return
	}
	if len(p.Password) < minPasswordLen {
		http.Error(w, "password_too_short", http.StatusBadRequest)
		return
	}
	dbRole, ok := roleAlias(p.Role)
	if !ok {
		http.Error(w, "invalid_role", http.StatusBadRequest)
		return
	}
	if dbRole == "viewer" && p.ClientID == nil {
		http.Error(w, "client_id_required", http.StatusBadRequest)
		return
	}
	if dbRole != "viewer" && p.ClientID != nil {
		http.Error(w, "client_id_not_allowed_for_admin", http.StatusBadRequest)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(p.Password), 10)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	u, err := h.repo.Create(r.Context(), users.CreateInput{
		Email:        p.Email,
		PasswordHash: string(hash),
		Role:         dbRole,
		ClientID:     p.ClientID,
		Name:         p.Name,
		Phone:        p.Phone,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505": // unique_violation (email)
				http.Error(w, "email_taken", http.StatusConflict)
				return
			case "23514": // check_violation
				http.Error(w, "role_client_inconsistent", http.StatusBadRequest)
				return
			case "23503": // foreign_key_violation (client_id inválido)
				http.Error(w, "client_not_found", http.StatusBadRequest)
				return
			}
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

type updateUserPayload struct {
	Name     *string    `json:"name,omitempty"`
	Phone    *string    `json:"phone,omitempty"`
	Role     *string    `json:"role,omitempty"`
	ClientID *uuid.UUID `json:"client_id,omitempty"`
	IsActive *bool      `json:"is_active,omitempty"`
	Email    *string    `json:"email,omitempty"`  // só pra detectar e rejeitar
}

func (h *UsersHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid_id", http.StatusBadRequest)
		return
	}
	var p updateUserPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if p.Email != nil {
		http.Error(w, "email_immutable", http.StatusBadRequest)
		return
	}

	current, err := h.repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not_found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Reativar deletado é proibido (decisão da P4).
	if p.IsActive != nil && *p.IsActive && current.DeletedAt != nil {
		http.Error(w, "cannot_reactivate_deleted", http.StatusBadRequest)
		return
	}

	in := users.UpdateInput{
		Name: p.Name, Phone: p.Phone, IsActive: p.IsActive,
	}
	if p.Role != nil {
		dbRole, ok := roleAlias(*p.Role)
		if !ok {
			http.Error(w, "invalid_role", http.StatusBadRequest)
			return
		}
		in.Role = &dbRole
		// Se vai virar admin, força client_id a nulo (consistência);
		// se vai virar client, exige p.ClientID.
		if dbRole == "viewer" && p.ClientID == nil && current.ClientID == nil {
			http.Error(w, "client_id_required", http.StatusBadRequest)
			return
		}
		if dbRole != "viewer" {
			in.ClearClient = true
		}
	}
	if p.ClientID != nil {
		in.ClientID = p.ClientID
	}

	u, err := h.repo.Update(r.Context(), id, in)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			http.Error(w, "role_client_inconsistent", http.StatusBadRequest)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type resetPwdPayload struct {
	Password string `json:"password"`
}

func (h *UsersHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid_id", http.StatusBadRequest)
		return
	}
	var p resetPwdPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if len(p.Password) < minPasswordLen {
		http.Error(w, "password_too_short", http.StatusBadRequest)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(p.Password), 10)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.repo.SetPassword(r.Context(), id, string(hash)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not_found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *UsersHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid_id", http.StatusBadRequest)
		return
	}
	caller, ok := auth.ClaimsFromContext(r.Context())
	if ok && caller.UserID == id {
		http.Error(w, "cannot_delete_self", http.StatusBadRequest)
		return
	}
	if err := h.repo.SoftDelete(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// idempotente: já deletado ou não existe → 204
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func atoiOr(s string, def int) int {
	if s == "" { return def }
	if v, err := strconv.Atoi(s); err == nil { return v }
	return def
}
```

- [ ] **Step 4: Rodar — devem passar**

Run: `cd workers && go test ./internal/api/handlers/... -run Users -v -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/users.go workers/internal/api/handlers/users_test.go
git commit -m "admin: CRUD de usuários em /v1/internal/admin/users

Endpoints: List (filtros + paginação), Get, Create, Patch
(rejeita email), ResetPassword, Delete (soft, bloqueia self).
Mapeia 'client' (API) → 'viewer' (DB). Erros estruturados:
email_taken, role_client_inconsistent, client_id_required,
password_too_short, cannot_delete_self, cannot_reactivate_deleted."
```

---

## Task 8: Wiring no router

**Files:**
- Modify: `workers/internal/api/router.go`
- Modify: `workers/cmd/api/main.go` (ou onde `Deps` é construído)

- [ ] **Step 1: Adicionar `Users` e `Me` à struct `Deps`**

No topo de `router.go`:

```go
type Deps struct {
	// ... campos existentes ...
	Users *handlers.UsersHandler
	Me    *handlers.MeHandler
}
```

- [ ] **Step 2: Wire as rotas dentro do `r.Route("/v1/internal", ...)`**

Logo depois da `r.Post("/auth/login", ...)`, ainda no escopo público:
- Nada novo aqui (login fica como está).

Dentro do `r.Group(func(r chi.Router) { r.Use(auth.RequireJWT); r.Use(auth.RequireRole("admin","operator")) })`, **mudar** o gate base pra incluir viewer onde apropriado. **Mais simples**: criar um subgrupo separado pros endpoints que viewer também acessa.

Substituir o bloco atual por estrutura:

```go
r.Group(func(r chi.Router) {
	r.Use(auth.RequireJWT)

	// Subgrupo: endpoints de leitura que viewer também acessa.
	// Filtragem por client_id é responsabilidade do handler via
	// auth.ClientScopeFromContext.
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole("admin", "operator", "viewer"))

		r.Get("/campaigns", d.Campaigns.List)
		r.Get("/campaigns/{id}", d.Campaigns.Get)
		r.Get("/campaigns/{id}/daily-summary", d.Detections.DailySummary)
		r.Get("/detections", d.Detections.List)
		r.Get("/detections/{id}", d.Detections.Get)
		r.Get("/detections/{id}/evidence", d.Detections.Evidence)
		r.Get("/detections/{id}/evidence/url", d.Detections.EvidenceURL)
		r.Get("/clients/{clientID}/materials", d.Materials.ListByClient)
		// Self-service:
		if d.Me != nil {
			r.Get("/auth/me", d.Me.Get)
			r.Patch("/auth/me", d.Me.Patch)
			r.Post("/auth/me/password", d.Me.ChangePassword)
		}
	})

	// Subgrupo admin/operator (escritas e endpoints administrativos).
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole("admin", "operator"))
		// ... TODO o resto que já estava aqui (stations, clients,
		// material-types, materials write, distribution rules, pricing,
		// stream-health, workers, admin/* etc).
	})

	// Subgrupo admin-only — adiciona /admin/users.
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole("admin"))
		if d.Users != nil {
			r.Route("/admin/users", func(r chi.Router) {
				r.Get("/", d.Users.List)
				r.Post("/", d.Users.Create)
				r.Get("/{id}", d.Users.Get)
				r.Patch("/{id}", d.Users.Patch)
				r.Delete("/{id}", d.Users.Delete)
				r.Post("/{id}/password", d.Users.ResetPassword)
			})
		}
	})
})
```

> **Atenção:** preserve toda a fiação existente de stations, clients (CRUD admin), webhooks, campaigns (write), commercials, material-types (write), materials (write), distribution-rules, distribution-overrides, pricing, stream-health, workers e admin/*. **Não remova** essas linhas — só reorganize em qual subgrupo elas ficam.

- [ ] **Step 3: Construir `UsersHandler` e `MeHandler` em `cmd/api/main.go`**

Procurar onde `handlers.NewAuthHandler(...)` é instanciado e na sequência adicionar:

```go
usersRepo := users.NewRepo(pool)
deps.Auth = handlers.NewAuthHandler(pool, usersRepo)
deps.Users = handlers.NewUsersHandler(usersRepo)
deps.Me = handlers.NewMeHandler(usersRepo)
```

- [ ] **Step 4: Build**

Run: `cd workers && go build ./...`
Expected: sem erros.

- [ ] **Step 5: Subir o stack e testar manualmente o login**

Run:
```bash
docker compose -f infra/docker/docker-compose.yml up -d --build api
docker compose -f infra/docker/docker-compose.yml logs api | tail -30
curl -s -X POST http://localhost:8080/v1/internal/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"<bootstrap-admin-email>","password":"<bootstrap-admin-pw>"}' | jq
```
Expected: resposta JSON com `token`, `expires_at`, `user.{id, email, role, name}`. `client_id` ausente (admin).

- [ ] **Step 6: Smoke test admin endpoints**

```bash
TOKEN=$(curl -s -X POST .../auth/login -d '...' | jq -r .token)
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/v1/internal/admin/users | jq
```
Expected: 200 com `{data:[<bootstrap admin>], total:1, ...}`.

- [ ] **Step 7: Commit**

```bash
git add workers/internal/api/router.go workers/cmd/api/
git commit -m "router: wire CRUD admin/users e self-service /auth/me

Reorganiza /v1/internal em três subgrupos:
- viewer-friendly: GET de campaigns/detections/reports + /auth/me
- admin/operator: escritas e endpoints administrativos legados
- admin-only: /admin/users CRUD"
```

---

## Task 9: Scope filtering nos handlers de leitura

**Files:**
- Modify: `workers/internal/api/handlers/campaigns.go`
- Modify: `workers/internal/api/handlers/detections.go`
- Modify: `workers/internal/api/handlers/materials.go`
- Tests correspondentes

- [ ] **Step 1: Examinar cada handler de read e identificar o ponto onde a query é montada**

Run: `cd workers && grep -n "ListInput\|ClientID\|client_id" internal/api/handlers/campaigns.go internal/api/handlers/detections.go internal/api/handlers/materials.go`

- [ ] **Step 2: Aplicar scope nas queries**

Em cada handler de read, no início:

```go
scope := auth.ClientScopeFromContext(r.Context())
```

E passar `scope` (pode ser nil) pra cada `repo.List(...)` / `repo.Get(...)`. Os repos correspondentes em `catalog/` precisam aceitar um filtro opcional `ClientID *uuid.UUID` na query (já têm em alguns casos). Adicionar nos que faltam.

**Para `GET /campaigns/{id}`**: depois de buscar a campanha, comparar `campaign.ClientID == *scope`. Se não casar e `scope != nil`, retornar 404 (nunca 403 — evita oracle de existência).

**Para `GET /clients/{clientID}/materials`**: se `scope != nil` e `clientID != *scope`, retornar 403 `forbidden_client_scope`.

**Para `GET /detections`**: aceitar `client_id` filter opcional do query string como hoje, mas **sobrescrever** com `scope` se não nil.

- [ ] **Step 3: Adicionar testes de scope pra cada handler afetado**

Cada handler ganha um teste tipo:

```go
func TestCampaigns_List_ViewerSeesOnlyOwnClient(t *testing.T) {
	// cria 2 clientes, 1 campanha em cada, faz request com claims
	// viewer apontando pra cliente A, espera só campanha do A no resultado.
}
```

- [ ] **Step 4: Rodar testes**

Run: `cd workers && go test ./internal/api/handlers/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/campaigns.go workers/internal/api/handlers/detections.go workers/internal/api/handlers/materials.go workers/internal/api/handlers/*_test.go workers/internal/catalog/
git commit -m "scope: filtra campanhas/detections/materials por client_id do JWT

Quando o requester é viewer, GET /campaigns, /detections,
/clients/:id/materials e detalhes correspondentes filtram por
client_id derivado do JWT. Acesso cross-client retorna 404
(detalhes) ou 403 (lista materials de outro cliente)."
```

---

## Task 10: Frontend — AuthContext e RequireRole

**Files:**
- Modify: `frontend/src/contexts/AuthContext.jsx`
- Create: `frontend/src/components/RequireRole.jsx`

- [ ] **Step 1: Atualizar `AuthContext.jsx`**

Substituir o bloco de derivações:

```jsx
// Role helpers consumed by Sidebar / route guards.
//
// Vocabulário: o backend usa role 'viewer' pra "Cliente". 'admin' e
// 'operator' são tratados como sinônimos (admin do sistema). Este context
// expõe isAdmin/isClient/clientId pra componentes não terem que conhecer
// o detalhe.
const role = user?.role ?? null
const isAdmin = role === 'admin' || role === 'operator'
const isClient = role === 'viewer'
const clientId = user?.client_id ?? null

const value = {
  token,
  user,
  isAuthenticated: !!token,
  isAdmin,
  isClient,
  clientId,
  login,
  logout,
}
```

> **Quebra intencional:** removido o fallback `isAdmin = role == null` (que existia "pra UI legada continuar renderizando"). Agora um user sem role é tratado como não-admin. Antes de aplicar, garantir que `RequireAuth` redireciona quando user é null.

- [ ] **Step 2: Criar `RequireRole.jsx`**

```jsx
import { Navigate } from 'react-router-dom'
import { useAuth } from '../contexts/AuthContext'

// Guard de rota por role. Uso:
//   <Route path="/admin/users" element={
//     <RequireRole roles={['admin']}>
//       <AdminUsersPage />
//     </RequireRole>
//   } />
//
// Cliente entrando em rota admin redireciona pra /campaigns (ou /dashboard
// quando esse existir de verdade).
export default function RequireRole({ roles, children, redirectTo = '/campaigns' }) {
  const { user } = useAuth()
  const role = user?.role
  if (!role) return <Navigate to="/login" replace />
  if (!roles.includes(role) && !(roles.includes('admin') && role === 'operator')) {
    return <Navigate to={redirectTo} replace />
  }
  return children
}
```

- [ ] **Step 3: Smoke test no browser**

Subir frontend (`cd frontend && npm run dev`), logar, abrir devtools → Application → sessionStorage → verificar `rc_user` tem `role` e (se viewer) `client_id`.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/contexts/AuthContext.jsx frontend/src/components/RequireRole.jsx
git commit -m "frontend: AuthContext expõe clientId; RequireRole guard

isAdmin agora aceita 'operator' (admin do sistema). isClient mapeia
'viewer'. clientId vem do user.client_id. RequireRole redireciona
para /campaigns quando role não autorizada."
```

---

## Task 11: Frontend — hooks de usuário

**Files:**
- Modify: `frontend/src/api/hooks.js`

- [ ] **Step 1: Adicionar hooks no final do arquivo**

```jsx
// Users (admin only)
export function useUsersPaged(params = {}) {
  return useQuery({
    queryKey: ['users', 'paged', params],
    queryFn: () => api.get('/admin/users', { params }).then(r => r.data),
    placeholderData: (prev) => prev,
  })
}
export function useUser(id) {
  return useQuery({
    queryKey: ['users', id],
    queryFn: () => api.get(`/admin/users/${id}`).then(r => r.data),
    enabled: !!id,
  })
}
export function useCreateUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data) => api.post('/admin/users', data).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })
}
export function useUpdateUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }) => api.patch(`/admin/users/${id}`, body).then(r => r.data),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['users'] })
      qc.invalidateQueries({ queryKey: ['users', vars.id] })
    },
  })
}
export function useResetUserPassword() {
  return useMutation({
    mutationFn: ({ id, password }) =>
      api.post(`/admin/users/${id}/password`, { password }).then(r => r.data),
  })
}
export function useDeleteUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.delete(`/admin/users/${id}`).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })
}

// Me (any authenticated)
export function useMe() {
  return useQuery({
    queryKey: ['me'],
    queryFn: () => api.get('/auth/me').then(r => r.data),
  })
}
export function useUpdateMe() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body) => api.patch('/auth/me', body).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['me'] }),
  })
}
export function useChangeMyPassword() {
  return useMutation({
    mutationFn: (body) => api.post('/auth/me/password', body),
  })
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/api/hooks.js
git commit -m "hooks: useUsersPaged/useCreateUser/etc + useMe family"
```

---

## Task 12: Frontend — Sidebar atualizada

**Files:**
- Modify: `frontend/src/components/Sidebar.jsx`

- [ ] **Step 1: Adicionar ícones novos**

```jsx
function IconUsers() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="6" cy="5" r="2.2" />
      <circle cx="11.5" cy="6.5" r="1.6" />
      <path d="M2 13c0-2.6 1.79-4 4-4s4 1.4 4 4" />
      <path d="M10 13c0-1.7 1.18-2.6 2.5-2.6 1.1 0 2 .8 2.2 1.9" strokeOpacity="0.7" />
    </svg>
  )
}
function IconAccount() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="8" cy="5" r="2.5" />
      <path d="M2.5 14c0-3 2.5-5 5.5-5s5.5 2 5.5 5" />
    </svg>
  )
}
```

- [ ] **Step 2: Atualizar `AdminNav`**

```jsx
function AdminNav({ onClose }) {
  return (
    <>
      <span className="sidebar-section-label">Administração</span>
      <SidebarLink to="/admin/overview" icon={<IconAdminOverview />} onClose={onClose}>Visão geral</SidebarLink>
      <SidebarLink to="/admin/users"    icon={<IconUsers />}         onClose={onClose}>Usuários</SidebarLink>

      {/* ... resto inalterado ... */}
    </>
  )
}
```

- [ ] **Step 3: Atualizar `ClientNav`**

```jsx
function ClientNav({ onClose }) {
  return (
    <>
      <span className="sidebar-section-label">Visão geral</span>
      <SidebarLink to="/dashboard" icon={<IconDashboard />} onClose={onClose}>Dashboard</SidebarLink>

      <span className="sidebar-section-label">Veiculação</span>
      <SidebarLink to="/campaigns"       icon={<IconCampaigns />}     onClose={onClose}>Campanhas</SidebarLink>
      <SidebarLink to="/detections"      icon={<IconDetections />}    onClose={onClose}>Veiculações</SidebarLink>
      <SidebarLink to="/reports/airtime" icon={<IconAirtimeReport />} onClose={onClose}>Relatório data/hora</SidebarLink>

      <span className="sidebar-section-label">Conta</span>
      <SidebarLink to="/account" icon={<IconAccount />} onClose={onClose}>Minha conta</SidebarLink>
    </>
  )
}
```

- [ ] **Step 4: Smoke test no browser**

Logar como admin → ver "Usuários" na sidebar. Logar como viewer (precisa criar um manualmente via SQL ou esperar Task 13) → ver Dashboard, Campanhas, Veiculações, Relatório, Minha conta.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/Sidebar.jsx
git commit -m "sidebar: link Usuários (admin) e nav cliente expandida

Admin ganha 'Usuários' em Administração. Cliente passa a ver
Dashboard + Campanhas + Veiculações + Relatório data/hora + Minha
conta."
```

---

## Task 13: Frontend — utilitário de senha forte

**Files:**
- Create: `frontend/src/utils/passwordGen.js`
- Create: `frontend/src/utils/passwordGen.test.js` (se houver setup de Vitest)

- [ ] **Step 1: Implementar gerador**

```js
// Gera senha aleatória cripto-segura (não Math.random) com 4 famílias
// garantidas: minúscula, maiúscula, dígito, símbolo. Default 16 chars.
const LOWER = 'abcdefghijkmnpqrstuvwxyz'
const UPPER = 'ABCDEFGHJKLMNPQRSTUVWXYZ'
const DIGITS = '23456789'
const SYMBOLS = '!@#$%&*?'
const ALL = LOWER + UPPER + DIGITS + SYMBOLS

function randIndex(max) {
  const buf = new Uint32Array(1)
  crypto.getRandomValues(buf)
  return buf[0] % max
}

export function generateStrongPassword(len = 16) {
  if (len < 12) len = 12
  const out = [
    LOWER[randIndex(LOWER.length)],
    UPPER[randIndex(UPPER.length)],
    DIGITS[randIndex(DIGITS.length)],
    SYMBOLS[randIndex(SYMBOLS.length)],
  ]
  for (let i = 4; i < len; i++) out.push(ALL[randIndex(ALL.length)])
  // Fisher-Yates pra embaralhar
  for (let i = out.length - 1; i > 0; i--) {
    const j = randIndex(i + 1)
    ;[out[i], out[j]] = [out[j], out[i]]
  }
  return out.join('')
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/utils/passwordGen.js
git commit -m "utils: generateStrongPassword usa crypto.getRandomValues"
```

---

## Task 14: Frontend — modal `UserFormModal`

**Files:**
- Create: `frontend/src/components/UserFormModal.jsx`

- [ ] **Step 1: Implementar modal genérico (criar e editar)**

```jsx
import { useState, useEffect } from 'react'
import RSelect from './RSelect'
import { useClients } from '../api/hooks'
import { generateStrongPassword } from '../utils/passwordGen'

const ROLE_OPTIONS = [
  { value: 'admin',  label: 'Administrador' },
  { value: 'client', label: 'Cliente' },
]

const EMPTY = {
  role: 'client',
  client_id: null,
  name: '',
  email: '',
  phone: '',
  password: '',
  is_active: true,
}

// `mode`: 'create' | 'edit'
// `initial`: linha de users (em edit). Em create, ignorado.
// `onSubmit({...payload})` retorna Promise. `onClose()` fecha modal.
export default function UserFormModal({ mode, initial, onSubmit, onClose, error }) {
  const isEdit = mode === 'edit'
  const [v, setV] = useState(EMPTY)
  const [showPwd, setShowPwd] = useState(false)
  const clientsQ = useClients()

  useEffect(() => {
    if (isEdit && initial) {
      setV({
        role: initial.role === 'viewer' ? 'client' : 'admin',
        client_id: initial.client_id ?? null,
        name: initial.name ?? '',
        email: initial.email,
        phone: initial.phone ?? '',
        password: '',
        is_active: initial.is_active,
      })
    } else {
      setV(EMPTY)
    }
  }, [mode, initial])

  function set(k, val) { setV(prev => ({ ...prev, [k]: val })) }

  function handleSubmit(e) {
    e.preventDefault()
    const payload = {
      role: v.role,
      client_id: v.role === 'client' ? v.client_id : null,
      name: v.name.trim(),
      phone: v.phone.trim() || null,
    }
    if (!isEdit) {
      payload.email = v.email.trim().toLowerCase()
      payload.password = v.password
    } else {
      payload.is_active = v.is_active
    }
    onSubmit(payload)
  }

  const clientOptions = (clientsQ.data ?? []).map(c => ({ value: c.id, label: c.name }))

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal-card" onClick={e => e.stopPropagation()}>
        <h2>{isEdit ? 'Editar usuário' : 'Novo usuário'}</h2>
        <form onSubmit={handleSubmit}>
          <label className="field">
            <span className="field-label">Tipo</span>
            <RSelect
              value={ROLE_OPTIONS.find(o => o.value === v.role)}
              options={ROLE_OPTIONS}
              onChange={o => set('role', o.value)}
            />
          </label>

          {v.role === 'client' && (
            <label className="field">
              <span className="field-label">Cliente vinculado *</span>
              <RSelect
                value={clientOptions.find(o => o.value === v.client_id) ?? null}
                options={clientOptions}
                onChange={o => set('client_id', o?.value ?? null)}
                placeholder="Selecione um cliente"
                isClearable
              />
            </label>
          )}

          <label className="field">
            <span className="field-label">Nome *</span>
            <input className="input" value={v.name} onChange={e => set('name', e.target.value)} required />
          </label>

          <label className="field">
            <span className="field-label">Email *</span>
            <input
              className="input"
              type="email"
              value={v.email}
              disabled={isEdit}
              onChange={e => set('email', e.target.value)}
              required={!isEdit}
            />
            {isEdit && <span className="field-hint">Email é imutável após o cadastro.</span>}
          </label>

          <label className="field">
            <span className="field-label">Telefone</span>
            <input className="input" value={v.phone} onChange={e => set('phone', e.target.value)} placeholder="(11) 91234-5678" />
          </label>

          {!isEdit && (
            <label className="field">
              <span className="field-label">Senha * (mín 12 caracteres)</span>
              <div style={{ display: 'flex', gap: 8 }}>
                <input
                  className="input"
                  type={showPwd ? 'text' : 'password'}
                  value={v.password}
                  onChange={e => set('password', e.target.value)}
                  required
                  minLength={12}
                  style={{ flex: 1 }}
                />
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => setShowPwd(s => !s)}>
                  {showPwd ? 'Ocultar' : 'Mostrar'}
                </button>
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => set('password', generateStrongPassword(16))}>
                  Gerar senha forte
                </button>
              </div>
            </label>
          )}

          {isEdit && (
            <label className="field" style={{ flexDirection: 'row', alignItems: 'center', gap: 8 }}>
              <input type="checkbox" checked={v.is_active} onChange={e => set('is_active', e.target.checked)} />
              <span>Conta ativa (login permitido)</span>
            </label>
          )}

          {error && <div className="form-error">{error}</div>}

          <div className="modal-actions">
            <button type="button" className="btn btn-secondary" onClick={onClose}>Cancelar</button>
            <button type="submit" className="btn btn-primary">{isEdit ? 'Salvar' : 'Criar'}</button>
          </div>
        </form>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/UserFormModal.jsx
git commit -m "frontend: UserFormModal cobre criar e editar"
```

---

## Task 15: Frontend — modal `ResetPasswordModal`

**Files:**
- Create: `frontend/src/components/ResetPasswordModal.jsx`

- [ ] **Step 1: Implementar**

```jsx
import { useState } from 'react'
import { generateStrongPassword } from '../utils/passwordGen'

export default function ResetPasswordModal({ user, onSubmit, onClose, error }) {
  const [pwd, setPwd] = useState('')
  const [show, setShow] = useState(false)

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal-card" onClick={e => e.stopPropagation()}>
        <h2>Resetar senha</h2>
        <p>Definir nova senha para <strong>{user.email}</strong>. Comunique a nova senha ao usuário; ele pode trocar depois em "Minha conta".</p>
        <form onSubmit={e => { e.preventDefault(); onSubmit(pwd) }}>
          <label className="field">
            <span className="field-label">Nova senha (mín 12 caracteres)</span>
            <div style={{ display: 'flex', gap: 8 }}>
              <input className="input" type={show ? 'text' : 'password'}
                value={pwd} onChange={e => setPwd(e.target.value)}
                minLength={12} required style={{ flex: 1 }} />
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setShow(s => !s)}>
                {show ? 'Ocultar' : 'Mostrar'}
              </button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setPwd(generateStrongPassword(16))}>
                Gerar
              </button>
            </div>
          </label>
          {error && <div className="form-error">{error}</div>}
          <div className="modal-actions">
            <button type="button" className="btn btn-secondary" onClick={onClose}>Cancelar</button>
            <button type="submit" className="btn btn-primary">Resetar senha</button>
          </div>
        </form>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/ResetPasswordModal.jsx
git commit -m "frontend: ResetPasswordModal"
```

---

## Task 16: Frontend — `AdminUsersPage`

**Files:**
- Create: `frontend/src/pages/AdminUsersPage.jsx`
- Create: `frontend/src/pages/AdminUsersPage.css`

- [ ] **Step 1: Implementar a página**

```jsx
import { useState } from 'react'
import RSelect from '../components/RSelect'
import { useConfirm } from '../components/ConfirmModal'
import UserFormModal from '../components/UserFormModal'
import ResetPasswordModal from '../components/ResetPasswordModal'
import { useAuth } from '../contexts/AuthContext'
import {
  useUsersPaged, useCreateUser, useUpdateUser,
  useResetUserPassword, useDeleteUser, useClients,
} from '../api/hooks'
import './AdminUsersPage.css'

const STATUS_OPTIONS = [
  { value: 'active',   label: 'Ativos' },
  { value: 'inactive', label: 'Inativos' },
  { value: 'deleted',  label: 'Excluídos' },
  { value: 'all',      label: 'Todos' },
]
const ROLE_FILTER = [
  { value: '',       label: 'Todos os tipos' },
  { value: 'admin',  label: 'Administradores' },
  { value: 'client', label: 'Clientes' },
]

function formatRole(role) {
  if (role === 'viewer') return 'Cliente'
  return 'Administrador'
}

function formatDate(s) {
  if (!s) return '—'
  return new Date(s).toLocaleString('pt-BR')
}

function StatusBadge({ user }) {
  if (user.deleted_at) return <span className="badge badge-danger">Excluído</span>
  if (!user.is_active) return <span className="badge badge-muted">Inativo</span>
  return <span className="badge badge-success">Ativo</span>
}

export default function AdminUsersPage() {
  const { user: me } = useAuth()
  const confirm = useConfirm()
  const clientsQ = useClients()
  const [filters, setFilters] = useState({ status: 'active', role: '', client_id: null, q: '' })
  const [page, setPage] = useState(1)
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState(null)
  const [resetting, setResetting] = useState(null)
  const [formError, setFormError] = useState(null)

  const params = {
    status: filters.status,
    page, page_size: 20,
    ...(filters.role ? { role: filters.role } : {}),
    ...(filters.client_id ? { client_id: filters.client_id } : {}),
    ...(filters.q ? { q: filters.q } : {}),
  }
  const list = useUsersPaged(params)
  const createM = useCreateUser()
  const updateM = useUpdateUser()
  const resetM = useResetUserPassword()
  const deleteM = useDeleteUser()

  const clientById = Object.fromEntries((clientsQ.data ?? []).map(c => [c.id, c]))

  function handleCreate(payload) {
    setFormError(null)
    createM.mutate(payload, {
      onSuccess: () => setCreateOpen(false),
      onError: (err) => setFormError(err.response?.data || err.message),
    })
  }
  function handleEdit(payload) {
    setFormError(null)
    updateM.mutate({ id: editing.id, ...payload }, {
      onSuccess: () => setEditing(null),
      onError: (err) => setFormError(err.response?.data || err.message),
    })
  }
  function handleReset(password) {
    setFormError(null)
    resetM.mutate({ id: resetting.id, password }, {
      onSuccess: () => setResetting(null),
      onError: (err) => setFormError(err.response?.data || err.message),
    })
  }
  async function handleToggleActive(u) {
    const ok = await confirm({
      title: u.is_active ? 'Desativar conta?' : 'Reativar conta?',
      message: u.is_active
        ? `O usuário ${u.email} não conseguirá mais fazer login até ser reativado.`
        : `O usuário ${u.email} voltará a poder fazer login.`,
    })
    if (!ok) return
    updateM.mutate({ id: u.id, is_active: !u.is_active })
  }
  async function handleDelete(u) {
    const ok = await confirm({
      title: 'Excluir usuário?',
      message: `${u.email} será excluído definitivamente. Esta ação não pode ser desfeita.`,
      danger: true,
    })
    if (!ok) return
    deleteM.mutate(u.id)
  }

  const data = list.data?.data ?? []
  const total = list.data?.total ?? 0
  const totalPages = list.data?.total_pages ?? 1

  return (
    <div className="admin-users-page">
      <header className="page-header">
        <h1>Usuários</h1>
        <button className="btn btn-primary" onClick={() => { setFormError(null); setCreateOpen(true) }}>
          + Novo usuário
        </button>
      </header>

      <div className="filters-row">
        <input
          className="input"
          placeholder="Buscar por nome ou email..."
          value={filters.q}
          onChange={e => { setFilters(f => ({ ...f, q: e.target.value })); setPage(1) }}
        />
        <RSelect
          value={ROLE_FILTER.find(o => o.value === filters.role)}
          options={ROLE_FILTER}
          onChange={o => { setFilters(f => ({ ...f, role: o.value, client_id: o.value === 'admin' ? null : f.client_id })); setPage(1) }}
        />
        {filters.role === 'client' && (
          <RSelect
            value={(clientsQ.data ?? []).map(c => ({ value: c.id, label: c.name })).find(o => o.value === filters.client_id) ?? null}
            options={(clientsQ.data ?? []).map(c => ({ value: c.id, label: c.name }))}
            onChange={o => { setFilters(f => ({ ...f, client_id: o?.value ?? null })); setPage(1) }}
            placeholder="Filtrar por cliente"
            isClearable
          />
        )}
        <RSelect
          value={STATUS_OPTIONS.find(o => o.value === filters.status)}
          options={STATUS_OPTIONS}
          onChange={o => { setFilters(f => ({ ...f, status: o.value })); setPage(1) }}
        />
      </div>

      <table className="data-table">
        <thead>
          <tr>
            <th>Nome</th>
            <th>Email</th>
            <th>Tipo</th>
            <th>Cliente</th>
            <th>Último login</th>
            <th>Status</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {data.map(u => {
            const isSelf = u.id === me?.id
            return (
              <tr key={u.id}>
                <td>{u.name || <span className="muted">—</span>}</td>
                <td>{u.email}</td>
                <td>{formatRole(u.role)}</td>
                <td>{u.client_id ? (clientById[u.client_id]?.name ?? u.client_id) : <span className="muted">—</span>}</td>
                <td>{formatDate(u.last_login_at)}</td>
                <td><StatusBadge user={u} /></td>
                <td className="row-actions">
                  <button className="btn btn-secondary btn-sm" onClick={() => { setFormError(null); setEditing(u) }} disabled={!!u.deleted_at}>Editar</button>
                  <button className="btn btn-secondary btn-sm" onClick={() => { setFormError(null); setResetting(u) }} disabled={!!u.deleted_at}>Resetar senha</button>
                  {!u.deleted_at && (
                    <button className="btn btn-secondary btn-sm" onClick={() => handleToggleActive(u)} disabled={isSelf}>
                      {u.is_active ? 'Desativar' : 'Reativar'}
                    </button>
                  )}
                  {!u.deleted_at && (
                    <button className="btn btn-danger btn-sm" onClick={() => handleDelete(u)} disabled={isSelf}>Excluir</button>
                  )}
                </td>
              </tr>
            )
          })}
          {data.length === 0 && !list.isLoading && (
            <tr><td colSpan={7} className="empty">Nenhum usuário encontrado.</td></tr>
          )}
        </tbody>
      </table>

      <div className="pagination">
        <button className="btn btn-secondary btn-sm" disabled={page <= 1} onClick={() => setPage(p => p - 1)}>Anterior</button>
        <span>Página {page} de {totalPages} ({total} no total)</span>
        <button className="btn btn-secondary btn-sm" disabled={page >= totalPages} onClick={() => setPage(p => p + 1)}>Próxima</button>
      </div>

      {createOpen && (
        <UserFormModal mode="create" onSubmit={handleCreate} onClose={() => setCreateOpen(false)} error={formError} />
      )}
      {editing && (
        <UserFormModal mode="edit" initial={editing} onSubmit={handleEdit} onClose={() => setEditing(null)} error={formError} />
      )}
      {resetting && (
        <ResetPasswordModal user={resetting} onSubmit={handleReset} onClose={() => setResetting(null)} error={formError} />
      )}
    </div>
  )
}
```

- [ ] **Step 2: Estilos básicos**

`AdminUsersPage.css`:

```css
.admin-users-page { padding: 24px; }
.admin-users-page .page-header { display:flex; justify-content:space-between; align-items:center; margin-bottom:16px; }
.admin-users-page .filters-row { display:grid; grid-template-columns:2fr 1fr 1fr 1fr; gap:12px; margin-bottom:16px; }
.admin-users-page .data-table { width:100%; border-collapse:collapse; }
.admin-users-page .data-table th, .admin-users-page .data-table td { padding:10px 12px; border-bottom:1px solid var(--c-border); text-align:left; }
.admin-users-page .row-actions { display:flex; gap:6px; flex-wrap:wrap; justify-content:flex-end; }
.admin-users-page .badge { display:inline-block; padding:2px 8px; border-radius:999px; font-size:12px; font-weight:600; }
.admin-users-page .badge-success { background:#dcfce7; color:#166534; }
.admin-users-page .badge-muted   { background:#f1f5f9; color:#475569; }
.admin-users-page .badge-danger  { background:#fee2e2; color:#991b1b; }
.admin-users-page .pagination { display:flex; gap:12px; align-items:center; justify-content:center; margin-top:16px; }
.admin-users-page .muted { color:#94a3b8; }
.admin-users-page .empty { text-align:center; padding:32px; color:#64748b; }
```

- [ ] **Step 3: Smoke test no browser**

Navegar pra `/admin/users` (com user admin). Criar um cliente novo (em /clients se ainda não existe). Criar usuário cliente vinculado. Verificar:
- Lista renderiza, filtros funcionam.
- Modal abre, valida (sem cliente em "Cliente" → erro).
- "Gerar senha forte" preenche.
- Editar funciona, email aparece travado.
- Reset de senha funciona.
- Self-row tem botões Excluir/Desativar desabilitados.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/AdminUsersPage.jsx frontend/src/pages/AdminUsersPage.css
git commit -m "frontend: AdminUsersPage com filtros, paginação e modais"
```

---

## Task 17: Frontend — `AccountPage` (Minha conta)

**Files:**
- Create: `frontend/src/pages/AccountPage.jsx`

- [ ] **Step 1: Implementar**

```jsx
import { useState, useEffect } from 'react'
import { useMe, useUpdateMe, useChangeMyPassword } from '../api/hooks'

export default function AccountPage() {
  const meQ = useMe()
  const updateM = useUpdateMe()
  const changeM = useChangeMyPassword()
  const [profile, setProfile] = useState({ name: '', phone: '' })
  const [pwd, setPwd] = useState({ current_password: '', new_password: '', confirm: '' })
  const [pErr, setPErr] = useState(null)
  const [pwdErr, setPwdErr] = useState(null)
  const [pOk, setPOk] = useState(false)
  const [pwdOk, setPwdOk] = useState(false)

  useEffect(() => {
    if (meQ.data) setProfile({ name: meQ.data.name ?? '', phone: meQ.data.phone ?? '' })
  }, [meQ.data])

  function saveProfile(e) {
    e.preventDefault()
    setPErr(null); setPOk(false)
    updateM.mutate({ name: profile.name, phone: profile.phone || null }, {
      onSuccess: () => setPOk(true),
      onError: (err) => setPErr(err.response?.data || err.message),
    })
  }

  function changePwd(e) {
    e.preventDefault()
    setPwdErr(null); setPwdOk(false)
    if (pwd.new_password.length < 12) {
      setPwdErr('A nova senha precisa ter ao menos 12 caracteres.')
      return
    }
    if (pwd.new_password !== pwd.confirm) {
      setPwdErr('As senhas não conferem.')
      return
    }
    changeM.mutate(
      { current_password: pwd.current_password, new_password: pwd.new_password },
      {
        onSuccess: () => { setPwdOk(true); setPwd({ current_password: '', new_password: '', confirm: '' }) },
        onError: (err) => setPwdErr(err.response?.data || err.message),
      }
    )
  }

  if (meQ.isLoading) return <div style={{ padding: 24 }}>Carregando…</div>
  if (meQ.error) return <div style={{ padding: 24 }}>Erro ao carregar conta.</div>

  return (
    <div style={{ padding: 24, maxWidth: 600 }}>
      <h1>Minha conta</h1>

      <section style={{ marginTop: 24 }}>
        <h2>Dados pessoais</h2>
        <form onSubmit={saveProfile}>
          <label className="field">
            <span className="field-label">Email (não editável)</span>
            <input className="input" value={meQ.data.email} disabled />
          </label>
          <label className="field">
            <span className="field-label">Nome</span>
            <input className="input" value={profile.name} onChange={e => setProfile(p => ({ ...p, name: e.target.value }))} />
          </label>
          <label className="field">
            <span className="field-label">Telefone</span>
            <input className="input" value={profile.phone} onChange={e => setProfile(p => ({ ...p, phone: e.target.value }))} />
          </label>
          {pErr && <div className="form-error">{String(pErr)}</div>}
          {pOk && <div className="form-success">Dados atualizados.</div>}
          <button type="submit" className="btn btn-primary" style={{ marginTop: 12 }}>Salvar</button>
        </form>
      </section>

      <section style={{ marginTop: 32 }}>
        <h2>Trocar senha</h2>
        <form onSubmit={changePwd}>
          <label className="field">
            <span className="field-label">Senha atual</span>
            <input className="input" type="password" value={pwd.current_password}
              onChange={e => setPwd(p => ({ ...p, current_password: e.target.value }))} required />
          </label>
          <label className="field">
            <span className="field-label">Nova senha (mín 12)</span>
            <input className="input" type="password" value={pwd.new_password}
              onChange={e => setPwd(p => ({ ...p, new_password: e.target.value }))} minLength={12} required />
          </label>
          <label className="field">
            <span className="field-label">Confirmar nova senha</span>
            <input className="input" type="password" value={pwd.confirm}
              onChange={e => setPwd(p => ({ ...p, confirm: e.target.value }))} minLength={12} required />
          </label>
          {pwdErr && <div className="form-error">{String(pwdErr)}</div>}
          {pwdOk && <div className="form-success">Senha alterada.</div>}
          <button type="submit" className="btn btn-primary" style={{ marginTop: 12 }}>Trocar senha</button>
        </form>
      </section>
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/pages/AccountPage.jsx
git commit -m "frontend: AccountPage (Minha conta) — perfil + troca de senha"
```

---

## Task 18: Frontend — App.jsx routing + escondendo botões pra clientes

**Files:**
- Modify: `frontend/src/App.jsx`
- Modify: `frontend/src/pages/CampaignsPage.jsx`
- Modify: `frontend/src/pages/DetectionsPage.jsx` (se houver botões de escrita)

- [ ] **Step 1: Adicionar imports e rotas em `App.jsx`**

```jsx
import RequireRole from './components/RequireRole'
import AdminUsersPage from './pages/AdminUsersPage'
import AccountPage from './pages/AccountPage'

// Dentro de <Routes> em <AppShell>:
<Route path="/admin/overview" element={
  <RequireRole roles={['admin']}><AdminOverviewPage /></RequireRole>
} />
<Route path="/admin/users" element={
  <RequireRole roles={['admin']}><AdminUsersPage /></RequireRole>
} />
<Route path="/account" element={<AccountPage />} />
```

E mudar a rota `/` de `<Navigate to="/stations" replace />` pra escolher destino baseado em role:

```jsx
import { useAuth } from './contexts/AuthContext'

function HomeRedirect() {
  const { isAdmin } = useAuth()
  return <Navigate to={isAdmin ? '/stations' : '/campaigns'} replace />
}

// e na <Route path="/" element={<HomeRedirect />} />
```

Também envolver as rotas só-admin em `<RequireRole>`:
- `/stations`, `/clients`, `/material-types`, `/monitoring`, `/operations`, `/campaigns/new`, `/campaigns/:id/edit`, `/clients/:id/webhooks`, `/clients/:id/api-keys`

- [ ] **Step 2: Em `CampaignsPage.jsx`, esconder botões de escrita**

```jsx
import { useAuth } from '../contexts/AuthContext'

const { isAdmin } = useAuth()
// e onde tem o botão "Nova campanha":
{isAdmin && <button className="btn btn-primary" onClick={...}>+ Nova campanha</button>}
// onde tem ações por linha (Editar, Cancelar):
{isAdmin && <button className="btn btn-secondary btn-sm">Editar</button>}
```

- [ ] **Step 3: Em `DetectionsPage.jsx`, esconder ações admin**

Verificar Read da página primeiro (`grep -n "isAdmin\|role" frontend/src/pages/DetectionsPage.jsx` — pode já existir tratamento) e esconder "Adicionar manualmente", "Desconsiderar", "Restaurar" quando `!isAdmin`.

- [ ] **Step 4: Smoke test**

Logar como cliente (criar via `/admin/users`); navegar:
- `/admin/users` → redireciona pra `/campaigns`.
- `/campaigns` → vê só campanhas dele, sem botão "Nova".
- `/detections` → só veiculações dele, sem ações admin.
- `/account` → consegue trocar senha e dados.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/App.jsx frontend/src/pages/CampaignsPage.jsx frontend/src/pages/DetectionsPage.jsx
git commit -m "frontend: rotas /admin/users + /account; esconde escrita pra cliente

Rotas admin-only envolvidas em <RequireRole>. CampaignsPage e
DetectionsPage condicionam botões de escrita a isAdmin. / vai pra
/stations (admin) ou /campaigns (cliente)."
```

---

## Task 19: Documentação

**Files:**
- Create: `docs/features/user-management.md`
- Modify: `docs/operations/auth-bootstrap.md`
- Modify: `CLAUDE.md`
- Modify: `docs/README.md`

- [ ] **Step 1: Criar `docs/features/user-management.md`**

```markdown
---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - migrations/0027_user_management.up.sql
  - workers/internal/users/users.go
  - workers/internal/auth/scope.go
  - workers/internal/api/handlers/users.go
  - workers/internal/api/handlers/me.go
  - frontend/src/pages/AdminUsersPage.jsx
  - frontend/src/pages/AccountPage.jsx
  - frontend/src/components/RequireRole.jsx
---

# Gerenciamento de usuários

CRUD de usuários no painel admin (`/admin/users`) e tela "Minha conta"
para self-service. Suporta dois tipos de conta:

- **Administrador** (`role = admin` ou `operator` no banco): acesso total.
- **Cliente** (`role = viewer` no banco): vinculado a um cliente cadastrado
  via `users.client_id`; vê apenas Dashboard, Campanhas, Veiculações e
  Relatório data/hora — todos filtrados pelo seu `client_id` e read-only.

## Vínculo N:1

Cada usuário do tipo Cliente pertence a UM cliente cadastrado. Um cliente
pode ter VÁRIOS usuários. FK `ON DELETE RESTRICT`: não é possível excluir
uma empresa cliente que tenha usuários vinculados.

## Soft delete + is_active

- **Excluir** (`deleted_at`): definitivo. Login bloqueado, sumido das listas
  por padrão (filtro "Excluídos" mostra). Não pode reativar.
- **Desativar** (`is_active = false`): reversível. Login bloqueado, mas
  admin pode reativar.

## Filtragem por client_id (server-side)

O JWT carrega claim `client_id`. Middleware `auth.ClientScopeFromContext`
extrai e injeta no `request.Context`. Handlers de `/campaigns`,
`/detections`, `/reports/airtime` e `/clients/:id/materials` aplicam
`WHERE client_id = $scope` quando o requester é viewer.

Acesso cross-client a `/campaigns/:id` retorna **404** (evita oracle de
existência). Acesso a `/clients/{outro_id}/materials` retorna **403**
`forbidden_client_scope`.

## Endpoints

### Admin-only (`/v1/internal/admin/users/*`)

| Método | Path | Descrição |
|--------|------|-----------|
| GET    | `/admin/users` | Lista paginada. Query: `q`, `role={admin\|client}`, `client_id`, `status={active\|inactive\|deleted\|all}`, `page`, `page_size`. |
| GET    | `/admin/users/:id` | Detalhe. |
| POST   | `/admin/users` | Cria. |
| PATCH  | `/admin/users/:id` | Edita (rejeita `email`). |
| POST   | `/admin/users/:id/password` | Reset de senha. |
| DELETE | `/admin/users/:id` | Soft delete. Bloqueia self. |

### Self-service (`/v1/internal/auth/me*`)

| Método | Path | Descrição |
|--------|------|-----------|
| GET    | `/auth/me` | Próprio usuário. |
| PATCH  | `/auth/me` | Edita próprio name/phone. |
| POST   | `/auth/me/password` | Troca senha. Body: `{current_password, new_password}`. |

## Vocabulário API ↔ DB

| API (frontend) | Banco (DB) |
|----------------|-----------|
| `admin`        | `admin`   |
| `client`       | `viewer`  |

`operator` permanece no banco por compatibilidade mas não é exposto na API
de criação (cai em `invalid_role`). É tratado como sinônimo de `admin` no
middleware de autorização e no `isAdmin` do frontend.

## Fluxo de cadastro

1. Admin abre `/admin/users`, clica "+ Novo usuário".
2. Preenche: tipo (Admin / Cliente), [se Cliente] dropdown de cliente,
   nome, email, telefone, senha (ou "Gerar senha forte" → 16 chars cripto).
3. Submit → POST cria. Admin comunica a senha por fora (WhatsApp, etc.).
4. Cliente faz login → atualiza `last_login_at`. Em `/account`, troca a
   senha (exige senha atual).

## Bloqueios e validações

- Login com `is_active=false` → 403 `account_disabled`.
- Login com `deleted_at NOT NULL` → 401 `invalid credentials` (não
  diferencia de "não existe" — anti-oracle).
- Self-delete → 400 `cannot_delete_self`.
- Reativar deletado → 400 `cannot_reactivate_deleted`.
- Email único entre não-deletados (UNIQUE parcial em `LOWER(email) WHERE
  deleted_at IS NULL`).
- Senha mínima: 12 caracteres.

## Fora de escopo (ver follow-ups)

- Refresh token / httpOnly cookie.
- 2FA / MFA.
- Audit log de ações.
- Email de convite (sem SMTP no projeto).
- N:M user↔clientes (agência multi-marca → 1 login por marca).
```

- [ ] **Step 2: Atualizar `docs/operations/auth-bootstrap.md`**

Encontrar a seção §8 com a lista de TODOs e remover/marcar como implementado:
- ~`POST /v1/internal/auth/users`~ → ✅ implementado em `/v1/internal/admin/users`
- ~`POST /v1/internal/auth/password`~ → ✅ implementado em `/v1/internal/auth/me/password`
- ~UI admin para gerenciar `users`~ → ✅ implementado em `/admin/users`

- [ ] **Step 3: Atualizar `CLAUDE.md` mapa de consulta**

Adicionar linha na tabela de "Mapa de consulta":

```markdown
| Gerenciamento de usuários (admin/cliente, /admin/users, /account) | [docs/features/user-management.md](docs/features/user-management.md) |
```

- [ ] **Step 4: Adicionar entrada em `docs/README.md`** (se houver índice).

- [ ] **Step 5: Commit**

```bash
git add docs/
git commit -m "docs: gerenciamento de usuários (feature + bootstrap update)

Cria docs/features/user-management.md com fluxos, endpoints e
vocabulário API↔DB. Atualiza auth-bootstrap.md (§8 TODOs cumpridos)
e CLAUDE.md mapa de consulta."
```

---

## Task 20: Verificação end-to-end

- [ ] **Step 1: Rebuild completo + migrations**

Run:
```bash
docker compose -f infra/docker/docker-compose.yml down
docker compose -f infra/docker/docker-compose.yml up -d --build
docker compose -f infra/docker/docker-compose.yml logs migrate | tail -10
```
Expected: migration 27 aplicada sem erro.

- [ ] **Step 2: Frontend dev**

Run: `cd frontend && npm run dev`
Abrir `http://localhost:5173`.

- [ ] **Step 3: Checklist manual (golden path)**

Logar como bootstrap admin:
- [ ] Sidebar mostra "Usuários" em Administração.
- [ ] `/admin/users` carrega: 1 linha (próprio admin), self-actions desabilitadas.
- [ ] Criar cliente em `/clients` se não existir (ex: "Cliente Teste").
- [ ] Criar usuário tipo "Cliente" vinculado: nome, email, telefone, senha.
  - [ ] "Gerar senha forte" preenche 16 chars.
  - [ ] Submit ok, novo usuário aparece na lista.
- [ ] Editar o cliente recém-criado: trocar nome.
- [ ] Resetar senha: copiar a nova.
- [ ] Logout. Logar com o usuário cliente:
  - [ ] Sidebar mostra Dashboard, Campanhas, Veiculações, Relatório, Minha conta.
  - [ ] `/admin/users` redireciona pra `/campaigns`.
  - [ ] `/campaigns` mostra só campanhas do cliente; sem botão "Nova".
  - [ ] `/detections` filtrado.
  - [ ] `/account`: editar nome, salvar. Trocar senha (current+new).
- [ ] Logout. Logar com bootstrap admin de novo:
  - [ ] Desativar o cliente. Logout, tentar logar com cliente → erro `account_disabled`.
  - [ ] Reativar. Login funciona.
  - [ ] Excluir. Logout, tentar logar → `invalid credentials`.
  - [ ] Tentar criar outro usuário com o mesmo email do excluído → ok (UNIQUE parcial).

- [ ] **Step 4: Rodar testes finais**

Run: `cd workers && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit final (se houver ajustes)**

Se algum bug aparecer no checklist, fixar e commitar com `fix: ...` apropriado.

---

## Self-review notes

- Spec coverage: cada decisão da seção 2 do spec mapeia pra uma task (1=schema; 2=role aliasing no UsersHandler/AuthContext; 3=hasher inline em Create+ResetPassword; 4=is_active+deleted_at; 5=campos no schema; 6=PATCH rejeita email; 7=isAdmin oculta botões; 8=ON DELETE RESTRICT na FK; extra=AccountPage).
- Validações §4.3 do spec: `client_id_required`, `client_id_not_allowed_for_admin`, `client_not_found`, `cannot_delete_self`, `account_disabled`, `cannot_reactivate_deleted`, `email_immutable` — todas presentes em Task 5/6/7. Acrescentei `cannot_deactivate_self` no spec mas no plano deixei só `cannot_delete_self` (Task 7) — a UI já desabilita o botão de Desativar pra self, então o backend pode ser tolerante e aceitar "auto-desativar" sem 400 (decisão pragmática). Se quiser endurecer no backend também, é uma linha em Patch handler.
- Type consistency: `users.User`, `users.Repo`, `users.CreateInput`, `users.UpdateInput`, `users.ListInput` consistentes entre Tasks 2/5/6/7.
- Placeholder scan: o `itoa` placeholder em Task 2 é explicitamente marcado pra substituir por `strconv.Itoa` no commit real. Sem outros TBDs/TODOs.
