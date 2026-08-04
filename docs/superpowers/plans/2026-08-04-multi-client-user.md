# Usuário vinculado a múltiplos clientes (agências) — plano de implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** permitir que um usuário de role `viewer` (a "Cliente" da UI) enxergue as
campanhas e veiculações de vários clientes, para atender agências que enviam mais
de um cliente para a operação.

**Architecture:** nova tabela `user_clients` guarda a carteira; `users.client_id`
permanece como "cliente principal" (CHECK da migration 0027 intacta), sincronizado
por trigger. O escopo de leitura deixa de ser `*uuid.UUID` e vira `[]uuid.UUID`,
viajando no JWT como `client_ids` e virando `= ANY($N::uuid[])` no SQL. No
frontend, o seletor de Cliente que já existe para o admin passa a aparecer quando
o usuário tem mais de um cliente vinculado.

**Tech Stack:** Go 1.26 (chi, pgx/v5, golang-jwt/v5, testify), PostgreSQL 16,
golang-migrate, React 18 + TanStack Query + Vite.

**Spec:** [docs/superpowers/specs/2026-08-04-multi-client-user-design.md](../specs/2026-08-04-multi-client-user-design.md)

---

## Preparação do ambiente de testes (faça uma vez, antes da Task 1)

Os testes de integração Go pulam sem `TEST_DATABASE_URL`. No host Windows há um
PostgreSQL nativo ocupando a porta 5432 que **sombreia** o Postgres do Docker, e o
`rc-prodcopy` (5544) é cópia de produção — nenhum dos dois serve. Use um Postgres
descartável em 15432:

```bash
docker run -d --name rc-test-pg --network docker_default -p 15432:5432 \
  -e POSTGRES_USER=radiocheck -e POSTGRES_PASSWORD=radiocheck \
  -e POSTGRES_DB=radiocheck_test postgres:16-alpine

cd c:/Users/marke/Desktop/Programas/E-Series/E-monitor
MSYS_NO_PATHCONV=1 docker run --rm --network docker_default \
  -v "$(pwd)/migrations:/migrations:ro" migrate/migrate:v4.17.1 \
  -path=/migrations \
  -database "postgres://radiocheck:radiocheck@rc-test-pg:5432/radiocheck_test?sslmode=disable" up
```

Se o container `rc-test-pg` já existir de uma sessão anterior, cheque se o banco
`radiocheck_test` existe (`docker exec rc-test-pg psql -U radiocheck -lqt`) e crie-o
se faltar — o `dbtest/guard.go` recusa rodar os pacotes destrutivos
(`internal/users`, `internal/api/handlers`) contra um banco com dado real.

Em todo comando `go test` deste plano, exporte:

```bash
export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:15432/radiocheck_test?sslmode=disable"
```

**Rode um pacote por vez.** Vários pacotes contra o mesmo banco produzem deadlock e
contagens erradas que parecem regressão.

---

## Estrutura de arquivos

**Criados:**

| Arquivo | Responsabilidade |
|---|---|
| `migrations/0062_user_clients.up.sql` | tabela `user_clients`, backfill, trigger da invariante |
| `migrations/0062_user_clients.down.sql` | reversão |
| `workers/internal/users/user_clients_test.go` | testes de `SetClients` e da leitura da carteira |
| `docs/features/multi-client-user.md` | doc operacional da feature |

**Modificados (núcleo):**

| Arquivo | Mudança |
|---|---|
| `workers/internal/auth/scope.go` | `ClientScopeFromContext` → `ClientScopesFromContext` + `ScopeAllows` |
| `workers/internal/auth/jwt.go` | claim `client_ids` |
| `workers/internal/users/users.go` | `User.ClientIDs`, `SetClients`, filtro por vínculo |
| `workers/internal/catalog/{campaigns,detections,live_map}.go` | filtro `[]uuid.UUID` + `= ANY` |
| `workers/internal/catalog/clients.go` | `CountDependents` conta por vínculo |
| `workers/internal/api/handlers/*.go` | 19 call sites do escopo |
| `frontend/src/contexts/AuthContext.jsx` | expõe `clientIds` |
| `frontend/src/components/insights/FiltersBar.jsx`, `frontend/src/pages/LiveMapPage.jsx` | seletor de cliente |
| `frontend/src/components/UserFormModal.jsx`, `frontend/src/pages/AdminUsersPage.jsx` | multi-select |

---

## Task 1: Migration `0062_user_clients`

**Files:**
- Create: `migrations/0062_user_clients.up.sql`
- Create: `migrations/0062_user_clients.down.sql`

- [ ] **Step 1: Escrever a migration up**

`migrations/0062_user_clients.up.sql`:

```sql
-- Migration 0062 — carteira de clientes por usuário (agências).
--
-- Contexto: users.client_id é 1:1 e agências precisam de um login que enxergue
-- vários clientes. user_clients passa a ser a carteira; users.client_id vira o
-- "cliente principal" e continua NOT NULL pra viewer (CHECK users_client_role_
-- consistency da 0027 permanece intacta).
--
-- Doc: docs/features/multi-client-user.md

CREATE TABLE IF NOT EXISTS user_clients (
    -- CASCADE: o vínculo não tem vida própria fora do usuário.
    user_id    UUID NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    -- RESTRICT espelha users_client_id_fkey: é o que sustenta o 409
    -- "client_has_dependents" ao tentar deletar cliente com usuário vinculado.
    client_id  UUID NOT NULL REFERENCES clients(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, client_id)
);

CREATE INDEX IF NOT EXISTS idx_user_clients_client ON user_clients(client_id);

-- Backfill: cada usuário com client_id vira uma linha. Idempotente e sem risco
-- de colisão — a PK é (user_id, client_id) e cada usuário tem no máximo um
-- client_id hoje.
INSERT INTO user_clients (user_id, client_id)
SELECT id, client_id FROM users WHERE client_id IS NOT NULL
ON CONFLICT DO NOTHING;

-- Invariante users.client_id ∈ user_clients.
--
-- O trigger só ADICIONA; remover vínculo é sempre explícito (users.Repo.
-- SetClients). Auto-corretivo de propósito: qualquer caminho que escreva
-- users.client_id (fluxo de boas-vindas, testes, reparo manual em prod) fica
-- consistente em vez de quebrar.
CREATE OR REPLACE FUNCTION sync_user_primary_client() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.client_id IS NOT NULL THEN
        INSERT INTO user_clients (user_id, client_id)
        VALUES (NEW.id, NEW.client_id)
        ON CONFLICT DO NOTHING;
    END IF;
    RETURN NULL; -- AFTER trigger: valor de retorno é ignorado
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_sync_user_primary_client ON users;
CREATE TRIGGER trg_sync_user_primary_client
    AFTER INSERT OR UPDATE OF client_id ON users
    FOR EACH ROW EXECUTE FUNCTION sync_user_primary_client();
```

- [ ] **Step 2: Escrever a migration down**

`migrations/0062_user_clients.down.sql`:

```sql
-- Reversão da 0062. users.client_id (o principal) sobrevive intacto, então
-- nenhum vínculo primário é perdido — só os secundários, que são exatamente
-- o que a feature adicionou.
DROP TRIGGER IF EXISTS trg_sync_user_primary_client ON users;
DROP FUNCTION IF EXISTS sync_user_primary_client();
DROP INDEX IF EXISTS idx_user_clients_client;
DROP TABLE IF EXISTS user_clients;
```

- [ ] **Step 3: Aplicar no banco de teste e verificar**

```bash
cd c:/Users/marke/Desktop/Programas/E-Series/E-monitor
MSYS_NO_PATHCONV=1 docker run --rm --network docker_default \
  -v "$(pwd)/migrations:/migrations:ro" migrate/migrate:v4.17.1 \
  -path=/migrations \
  -database "postgres://radiocheck:radiocheck@rc-test-pg:5432/radiocheck_test?sslmode=disable" up
```

Esperado: `62/u user_clients (…ms)` sem erro.

- [ ] **Step 4: Verificar tabela, trigger e idempotência do backfill**

```bash
docker exec rc-test-pg psql -U radiocheck -d radiocheck_test -At -c "
  SELECT to_regclass('user_clients');
  SELECT tgname FROM pg_trigger WHERE tgname = 'trg_sync_user_primary_client';
  INSERT INTO user_clients (user_id, client_id)
  SELECT id, client_id FROM users WHERE client_id IS NOT NULL
  ON CONFLICT DO NOTHING;
  SELECT 'ok';"
```

Esperado: `user_clients`, `trg_sync_user_primary_client`, `ok` — o segundo backfill
não pode levantar erro de chave duplicada.

- [ ] **Step 5: Verificar o down e reaplicar**

```bash
MSYS_NO_PATHCONV=1 docker run --rm --network docker_default \
  -v "$(pwd)/migrations:/migrations:ro" migrate/migrate:v4.17.1 \
  -path=/migrations \
  -database "postgres://radiocheck:radiocheck@rc-test-pg:5432/radiocheck_test?sslmode=disable" down 1
MSYS_NO_PATHCONV=1 docker run --rm --network docker_default \
  -v "$(pwd)/migrations:/migrations:ro" migrate/migrate:v4.17.1 \
  -path=/migrations \
  -database "postgres://radiocheck:radiocheck@rc-test-pg:5432/radiocheck_test?sslmode=disable" up
```

Esperado: ambos sem erro, e nenhum `dirty`.

- [ ] **Step 6: Commit**

```bash
git add migrations/0062_user_clients.up.sql migrations/0062_user_clients.down.sql
git commit -m "feat(multi-cliente): migration 0062 cria user_clients + trigger do principal"
```

---

## Task 2: `users.Repo` — ler a carteira e gravá-la

**Files:**
- Modify: `workers/internal/users/users.go`
- Test: `workers/internal/users/user_clients_test.go` (criar)

- [ ] **Step 1: Escrever os testes falhando**

Criar `workers/internal/users/user_clients_test.go`:

```go
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
```

`newTestDB` e `resetUsersAndClients` já existem em
`workers/internal/users/testhelpers_test.go`. O `TRUNCATE users, clients …
CASCADE` de lá também limpa `user_clients`, então nenhum ajuste é necessário.

- [ ] **Step 2: Rodar e ver falhar**

```bash
cd workers && go test ./internal/users/... -run 'TestSetClients|TestListFilterByClient' -v
```

Esperado: FAIL na compilação — `u.ClientIDs undefined` e `repo.SetClients undefined`.

- [ ] **Step 3: Adicionar `ClientIDs` ao model e à leitura**

Em `workers/internal/users/users.go`, no struct `User` (depois de `ClientID`):

```go
	// ClientIDs é a carteira completa (tabela user_clients). Contém sempre o
	// ClientID acima, que é o "principal". Vazio para admin/operator.
	ClientIDs []uuid.UUID `json:"client_ids"`
```

Trocar a constante `userColumns` (linha 54) por:

```go
// userColumns é a projeção de leitura. O array_agg correlacionado traz a
// carteira sem N+1. Exige que a tabela apareça como `users` (sem alias) na
// query — é o caso de todos os SELECTs deste arquivo.
const userColumns = `id, email, password_hash, role, client_id, name, phone,
                     is_active, receive_alert_emails, receive_post_sale_emails,
                     deleted_at, last_login_at, created_at, updated_at,
                     COALESCE((SELECT array_agg(uc.client_id ORDER BY uc.created_at, uc.client_id)
                               FROM user_clients uc WHERE uc.user_id = users.id), '{}')`
```

Acrescentar `&u.ClientIDs` ao final do `scanUser` (antes do `if err != nil`):

```go
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.ClientID,
		&u.Name, &u.Phone, &u.IsActive, &u.ReceiveAlertEmails, &u.ReceivePostSaleEmails,
		&u.DeletedAt, &u.LastLoginAt,
		&u.CreatedAt, &u.UpdatedAt, &u.ClientIDs)
```

- [ ] **Step 4: Fazer `Create` e `Update` relerem a linha**

O `array_agg` dentro de um `RETURNING` é avaliado **antes** do trigger `AFTER`
disparar, então a carteira voltaria vazia. Create e Update passam a reler.

Em `Create`, substituir o corpo por:

```go
func (r *Repo) Create(ctx context.Context, in CreateInput) (*User, error) {
	var id uuid.UUID
	if err := r.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role, client_id, name, phone,
		                    receive_alert_emails, receive_post_sale_emails)
		 VALUES (LOWER($1), $2, $3, $4, $5, $6,
		         COALESCE($7, TRUE), COALESCE($8, FALSE))
		 RETURNING id`,
		in.Email, in.PasswordHash, in.Role, in.ClientID, in.Name, in.Phone,
		in.ReceiveAlertEmails, in.ReceivePostSaleEmails,
	).Scan(&id); err != nil {
		return nil, err
	}
	// Relê pra trazer a carteira já materializada pelo trigger da 0062 — o
	// array_agg num RETURNING roda antes do AFTER trigger e viria vazio.
	return r.Get(ctx, id)
}
```

Em `Update`, trocar o `RETURNING ` + userColumns por `RETURNING id`, escanear o id
e devolver `r.Get(ctx, id)` pelo mesmo motivo.

- [ ] **Step 5: Implementar `SetClients`**

Acrescentar em `workers/internal/users/users.go`:

```go
// SetClients redefine a carteira de clientes do usuário numa transação:
// insere os que faltam, remove os que saíram e reposiciona o cliente principal
// (users.client_id).
//
// O principal é MANTIDO se continuar na lista; senão vira o primeiro id
// recebido — regra determinística pra não embaralhar o cabeçalho da página de
// boas-vindas a cada edição.
//
// Lista vazia é rejeitada: quem não tem cliente é admin/operator, e esse
// caminho é o ClearClient do UpdateInput.
func (r *Repo) SetClients(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return errors.New("client list must not be empty")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var current *uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT client_id FROM users WHERE id = $1 FOR UPDATE`, userID,
	).Scan(&current); err != nil {
		return err
	}

	primary := ids[0]
	if current != nil {
		for _, id := range ids {
			if id == *current {
				primary = *current
				break
			}
		}
	}

	// Ordem: UPDATE primeiro (o trigger da 0062 insere o principal em
	// user_clients), depois INSERT do resto, e o DELETE por último — assim o
	// DELETE nunca apaga uma linha recém-criada. primary ∈ ids, então ele
	// sobrevive ao <> ALL.
	if _, err := tx.Exec(ctx,
		`UPDATE users SET client_id = $2, updated_at = NOW() WHERE id = $1`,
		userID, primary); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_clients (user_id, client_id)
		 SELECT $1, unnest($2::uuid[])
		 ON CONFLICT DO NOTHING`, userID, ids); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM user_clients
		 WHERE user_id = $1 AND client_id <> ALL($2::uuid[])`,
		userID, ids); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

- [ ] **Step 6: Filtrar `List` por vínculo, não pelo principal**

Em `List` (linha ~261), trocar o bloco do filtro de cliente por:

```go
	if in.ClientID != nil {
		// Casa por VÍNCULO (carteira), não só pelo principal: um usuário de
		// agência filtrado por um cliente secundário precisa aparecer.
		where = append(where,
			"EXISTS (SELECT 1 FROM user_clients uc WHERE uc.user_id = users.id"+
				" AND uc.client_id = $"+strconv.Itoa(idx)+")")
		args = append(args, *in.ClientID)
		idx++
	}
```

O `COUNT(*)` e o `SELECT` de `List` usam `FROM users` sem alias, então
`users.id` resolve nos dois.

- [ ] **Step 7: Rodar os testes**

```bash
cd workers && go test ./internal/users/... -run 'TestSetClients|TestListFilterByClient' -v
```

Esperado: PASS nos três testes.

- [ ] **Step 8: Rodar o pacote inteiro (regressão)**

```bash
cd workers && go test ./internal/users/... -p 1
```

Esperado: `ok radiocheck/internal/users`.

- [ ] **Step 9: Commit**

```bash
git add workers/internal/users/users.go workers/internal/users/user_clients_test.go
git commit -m "feat(multi-cliente): users.Repo le e grava a carteira de clientes"
```

---

## Task 3: `auth` — escopo em lista

**Files:**
- Modify: `workers/internal/auth/scope.go`
- Modify: `workers/internal/auth/jwt.go`
- Test: `workers/internal/auth/scope_test.go` (reescrever)

- [ ] **Step 1: Reescrever o teste de escopo**

Substituir o conteúdo de `workers/internal/auth/scope_test.go` por:

```go
package auth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
)

func TestClientScopes_Viewer_MultipleClients(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{
		Role: "viewer", ClientID: &a, ClientIDs: []uuid.UUID{a, b},
	})
	require.Equal(t, []uuid.UUID{a, b}, auth.ClientScopesFromContext(ctx))
	require.True(t, auth.ScopeAllows(ctx, a))
	require.True(t, auth.ScopeAllows(ctx, b))
	require.False(t, auth.ScopeAllows(ctx, uuid.New()))
}

func TestClientScopes_LegacyToken_OnlyClientID(t *testing.T) {
	// JWT emitido antes da migração multi-cliente (validade de 8h): traz só
	// client_id. Sem essa tradução, todo cliente logado seria deslogado no
	// deploy do backend.
	cid := uuid.New()
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{
		Role: "viewer", ClientID: &cid,
	})
	require.Equal(t, []uuid.UUID{cid}, auth.ClientScopesFromContext(ctx))
	require.True(t, auth.ScopeAllows(ctx, cid))
	require.False(t, auth.ScopeAllows(ctx, uuid.New()))
}

func TestClientScopes_Admin_ReturnsNil(t *testing.T) {
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{Role: "admin"})
	require.Nil(t, auth.ClientScopesFromContext(ctx))
	require.True(t, auth.ScopeAllows(ctx, uuid.New()))
}

func TestClientScopes_Operator_ReturnsNil(t *testing.T) {
	cid := uuid.New()
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{
		Role: "operator", ClientID: &cid, ClientIDs: []uuid.UUID{cid},
	})
	require.Nil(t, auth.ClientScopesFromContext(ctx))
}

func TestClientScopes_NoClaims(t *testing.T) {
	require.Nil(t, auth.ClientScopesFromContext(context.Background()))
	require.True(t, auth.ScopeAllows(context.Background(), uuid.New()))
}

func TestClientScopes_ViewerNoClient_FailsClosed(t *testing.T) {
	// Viewer sem nenhum cliente é token mal-formado. Devolve [uuid.Nil] pra
	// toda query escopada dar vazio, em vez de virar unscoped (acesso total).
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{Role: "viewer"})
	require.Equal(t, []uuid.UUID{uuid.Nil}, auth.ClientScopesFromContext(ctx))
	require.False(t, auth.ScopeAllows(ctx, uuid.New()))
}
```

- [ ] **Step 2: Rodar e ver falhar**

```bash
cd workers && go test ./internal/auth/... -run TestClientScopes -v
```

Esperado: FAIL na compilação — `ClientIDs`, `ClientScopesFromContext` e
`ScopeAllows` não existem.

- [ ] **Step 3: Adicionar o claim**

Em `workers/internal/auth/jwt.go`, no struct `Claims`:

```go
type Claims struct {
	UserID   uuid.UUID  `json:"user_id"`
	Role     string     `json:"role"`
	ClientID *uuid.UUID `json:"client_id,omitempty"`
	// ClientIDs é a carteira completa do usuário (multi-cliente / agências).
	// ClientID acima continua sendo o principal, mantido por compatibilidade
	// com tokens e frontends anteriores à feature.
	ClientIDs []uuid.UUID `json:"client_ids,omitempty"`
	jwt.RegisteredClaims
}
```

E em `IssueTokenForUser`, dentro do literal `Claims`:

```go
		ClientID:  u.ClientID,
		ClientIDs: u.ClientIDs,
```

- [ ] **Step 4: Reescrever `scope.go`**

Substituir `ClientScopeFromContext` (o arquivo inteiro abaixo de
`ContextWithClaims`) por:

```go
// ClientScopesFromContext devolve a carteira de clientes do requester quando ele
// é viewer (cliente), ou nil quando é admin/operator/anônimo. Use em handlers que
// filtram listas por cliente.
//
// Convenção: nil = "sem escopo" = enxerga tudo (só admin/operator).
//
// Defense-in-depth: viewer sem nenhum cliente é token mal-formado (anterior à
// migration 0027 ou bug futuro). Devolve []uuid.UUID{uuid.Nil} pra toda query
// escopada dar resultado vazio, em vez de tratar como unscoped (acesso total).
//
// Compatibilidade: token emitido antes da migration 0062 traz apenas client_id
// e vale por até 8h. Traduzimos pra lista de um elemento — sem isso, todo
// cliente logado seria deslogado no deploy do backend.
func ClientScopesFromContext(ctx context.Context) []uuid.UUID {
	c, ok := ClaimsFromContext(ctx)
	if !ok {
		return nil
	}
	if c.Role != "viewer" {
		return nil
	}
	if len(c.ClientIDs) > 0 {
		return c.ClientIDs
	}
	if c.ClientID != nil {
		return []uuid.UUID{*c.ClientID}
	}
	return []uuid.UUID{uuid.Nil}
}

// ScopeAllows diz se o requester pode enxergar dados do cliente informado.
// Devolve true quando não há escopo (admin/operator). É o helper dos checks
// pontuais que respondem 404 anti-oracle.
func ScopeAllows(ctx context.Context, clientID uuid.UUID) bool {
	scopes := ClientScopesFromContext(ctx)
	if scopes == nil {
		return true
	}
	return slices.Contains(scopes, clientID)
}
```

Acrescentar `"slices"` ao bloco de imports.

- [ ] **Step 5: Rodar os testes**

```bash
cd workers && go test ./internal/auth/... -v
```

Esperado: PASS. Se algum teste antigo referenciar `ClientScopeFromContext`,
atualize-o — a função deixou de existir de propósito (um caminho que só enxerga
um cliente vazaria inconsistência silenciosa).

- [ ] **Step 6: Commit**

```bash
git add workers/internal/auth/
git commit -m "feat(multi-cliente): escopo de leitura vira lista de clientes no JWT"
```

---

## Task 4: Handlers — checks pontuais (404/403 anti-oracle)

O build está quebrado desde a Task 3 (`ClientScopeFromContext` sumiu). Esta task e
a Task 5 juntas o restauram.

**Files:**
- Modify: `workers/internal/api/handlers/campaigns.go:150`
- Modify: `workers/internal/api/handlers/campaign_materials.go:82`
- Modify: `workers/internal/api/handlers/distribution_rules.go:98`
- Modify: `workers/internal/api/handlers/pricing.go:35`
- Modify: `workers/internal/api/handlers/reports.go:80`
- Modify: `workers/internal/api/handlers/materials.go:46`
- Modify: `workers/internal/api/handlers/client_target_pmm.go:33`
- Modify: `workers/internal/api/handlers/detections.go:163,204,251,661,711`
- Test: `workers/internal/api/handlers/client_target_pmm_scope_test.go` (criar)

- [ ] **Step 1: Escrever o teste de autorização falhando**

Um teste cobre a regra dos nove sites: a carteira autoriza qualquer cliente dela e
recusa quem está fora. `/clients/{clientID}/target-pmm` é o mais barato de montar
(não precisa de campanha semeada). Criar
`workers/internal/api/handlers/client_target_pmm_scope_test.go`:

```go
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"radiocheck/internal/auth"
)

// pmmScopeRequest monta um GET /clients/{clientID}/target-pmm com o param de
// rota e as claims já no contexto.
func pmmScopeRequest(id uuid.UUID, claims *auth.Claims) *http.Request {
	req := httptest.NewRequest("GET", "/clients/"+id.String()+"/target-pmm", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientID", id.String())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	return req.WithContext(auth.ContextWithClaims(ctx, claims))
}

// Cliente fora da carteira leva 404 anti-oracle. O Repo nil prova que o guard
// barra ANTES de qualquer query — se ele deixasse passar, o teste entraria no
// repo e explodiria com nil pointer em vez de responder 404.
func TestClientTargetPmm_ForeignClientIs404(t *testing.T) {
	a, b, foreign := uuid.New(), uuid.New(), uuid.New()
	claims := &auth.Claims{Role: "viewer", ClientID: &a, ClientIDs: []uuid.UUID{a, b}}

	h := &ClientTargetPmmHandler{}
	rec := httptest.NewRecorder()
	h.List(rec, pmmScopeRequest(foreign, claims))

	require.Equal(t, http.StatusNotFound, rec.Code)
}

// Token legado (só client_id) segue autorizando o próprio cliente.
func TestClientTargetPmm_LegacyTokenForeignClientIs404(t *testing.T) {
	own, foreign := uuid.New(), uuid.New()
	claims := &auth.Claims{Role: "viewer", ClientID: &own}

	h := &ClientTargetPmmHandler{}
	rec := httptest.NewRecorder()
	h.List(rec, pmmScopeRequest(foreign, claims))

	require.Equal(t, http.StatusNotFound, rec.Code)
}
```

O caso positivo (cliente **dentro** da carteira passa do guard) fica coberto pelos
testes de rota que já existem em `workers/internal/api/router_authz_test.go`, que
sobem o router com repos reais.

- [ ] **Step 2: Rodar e ver falhar**

```bash
cd workers && go test ./internal/api/handlers/ -run TestClientTargetPmm_ -v -p 1
```

Esperado: FAIL na compilação — o pacote não builda enquanto os call sites usarem
`ClientScopeFromContext`, que deixou de existir na Task 3.

- [ ] **Step 3: Sites de comparação direta**

Quatro sites comparam um `clientID` já em mãos. Trocar cada condição por
`!auth.ScopeAllows(...)`, mantendo status e mensagem atuais:

`campaigns.go:150`:

```go
	// Viewer scope: hide cross-client campaigns as 404 (anti-oracle).
	if !auth.ScopeAllows(r.Context(), out.ClientID) {
		http.Error(w, "not found", 404)
		return
	}
```

`reports.go:80`:

```go
	// Viewer scope: 404 (não 403) pra não vazar existência.
	if !auth.ScopeAllows(r.Context(), camp.ClientID) {
		http.Error(w, "not found", http.StatusNotFound)
		return catalog.AggregateFilter{}, nil, false
	}
```

`materials.go:46` (mantém 403, que é o contrato atual desta rota):

```go
	if !auth.ScopeAllows(r.Context(), clientID) {
		http.Error(w, "forbidden_client_scope", http.StatusForbidden)
		return
	}
```

`client_target_pmm.go:33`:

```go
	// Anti-oracle: viewer pedindo cliente fora da carteira recebe 404, não
	// 403 — não confirma a existência do id. Mesmo padrão de /campaigns/{id}.
	if !auth.ScopeAllows(r.Context(), id) {
		http.Error(w, "not found", 404)
		return
	}
```

- [ ] **Step 4: Sites que buscam a campanha antes de comparar**

Quatro sites (`campaign_materials.go:82`, `distribution_rules.go:98`,
`pricing.go:35`, `detections.go:661` e `detections.go:711`) têm a forma:

```go
	if scope := auth.ClientScopeFromContext(r.Context()); scope != nil && h.CampaignRepo != nil {
		camp, err := h.CampaignRepo.Get(r.Context(), campaignID)
		...
		if camp.ClientID != *scope {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
	}
```

Em cada um, trocar **a linha do guard** e **a linha da comparação**, preservando o
resto do bloco (o `Get` só roda quando há escopo, o que evita uma query extra pro
admin):

```go
	if auth.ClientScopesFromContext(r.Context()) != nil && h.CampaignRepo != nil {
		camp, err := h.CampaignRepo.Get(r.Context(), campaignID)
		...
		if !auth.ScopeAllows(r.Context(), camp.ClientID) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
	}
```

`detections.go:661` usa a variável `cid` em vez de `campaignID` e **não** testa
`h.CampaignRepo != nil` — mantenha exatamente como está, trocando só as duas
linhas:

```go
	if auth.ClientScopesFromContext(r.Context()) != nil {
		camp, err := h.CampaignRepo.Get(r.Context(), cid)
		...
		if !auth.ScopeAllows(r.Context(), camp.ClientID) {
```

- [ ] **Step 5: Sites que resolvem o cliente pela detecção**

`detections.go:163` (Get), `:204` (EvidenceURL) e `:251` (Evidence) têm a forma:

```go
	if scope := auth.ClientScopeFromContext(r.Context()); scope != nil {
		clientID, err := h.Repo.GetClientID(r.Context(), id)
		...
		if *clientID != *scope {
			http.Error(w, "not found", 404)
			return
		}
	}
```

Trocar as duas linhas nos três:

```go
	if auth.ClientScopesFromContext(r.Context()) != nil {
		clientID, err := h.Repo.GetClientID(r.Context(), id)
		...
		if !auth.ScopeAllows(r.Context(), *clientID) {
			http.Error(w, "not found", 404)
			return
		}
	}
```

- [ ] **Step 6: Verificar que não sobrou nenhum call site pontual**

```bash
cd workers && grep -rn "ClientScopeFromContext" --include="*.go" .
```

Esperado neste ponto: **apenas** os sites de lista (`campaigns.go:47,162`,
`detections.go:41,98`, `insights.go:44`, `live_map.go:47`, `clients.go:32`), que
são a próxima task. Nenhum outro.

- [ ] **Step 7: Commit**

```bash
git add workers/internal/api/handlers/
git commit -m "feat(multi-cliente): checks pontuais de escopo usam ScopeAllows"
```

---

## Task 5: Repos e handlers de lista — filtro por array

**Files:**
- Modify: `workers/internal/catalog/campaigns.go` (ListPaged, ListFiltered, FinancialsByCampaign)
- Modify: `workers/internal/catalog/detections.go` (ListFilter, ListPagedFilter, AggregateFilter)
- Modify: `workers/internal/catalog/live_map.go` (Get)
- Modify: `workers/internal/api/handlers/campaigns.go:47,162`
- Modify: `workers/internal/api/handlers/detections.go:41,98`
- Modify: `workers/internal/api/handlers/live_map.go:47`
- Modify: `workers/internal/api/handlers/clients.go:32`

**Contrato dos filtros de array** (vale para todos os pontos abaixo):

- `nil` → pgx envia NULL → `$N::uuid[] IS NULL` verdadeiro → **sem filtro** (admin).
- slice não-vazio → filtra por `= ANY($N)`.
- slice vazio (`[]uuid.UUID{}`) → `'{}'` não é NULL → `= ANY('{}')` é falso →
  **nenhuma linha**. Falha fechada, que é o comportamento desejado.

- [ ] **Step 1: `catalog/campaigns.go` — ListPaged**

Trocar a assinatura `clientID *uuid.UUID` por `clientIDs []uuid.UUID` e, no bloco
`const where` (linha ~118):

```sql
		    AND ($5::uuid[] IS NULL OR c.client_id = ANY($5))
```

O argumento posicional `clientID` passa a ser `clientIDs` nas duas chamadas
(`QueryRow` do COUNT e `Query` da página).

- [ ] **Step 2: `catalog/campaigns.go` — ListFiltered**

Substituir o `switch` de quatro braços (linhas ~202-211) por uma query única —
o guard de array cobre os dois filtros:

```go
// ListFiltered returns campaigns filtered by status and/or client. If statuses
// is nil/empty, all lifecycle states are returned. clientIDs, when non-nil,
// restringe aos clientes da carteira do viewer (nil = admin, sem filtro; slice
// vazio = nenhuma linha, falha fechada). Ordering follows the lifecycle UX rule:
// ativas → programadas (próximas a entrar) → concluidas/canceladas.
func (c *Campaigns) ListFiltered(ctx context.Context, statuses []string, clientIDs []uuid.UUID) ([]Campaign, error) {
	const baseQuery = `
		SELECT id, client_id, name, start_date, end_date, status, target_stations,
		       fixed_cpm, created_at, updated_at
		FROM campaigns
		WHERE ($1::text[] IS NULL OR status = ANY($1))
		  AND ($2::uuid[] IS NULL OR client_id = ANY($2))
	`
	// ... orderClause inalterado ...
	if len(statuses) == 0 {
		statuses = nil // nil vira NULL no pgx = "sem filtro de status"
	}
	rows, err := c.pool.Query(ctx, baseQuery+orderClause, statuses, clientIDs)
	if err != nil {
		return nil, err
	}
	// ... scan inalterado ...
}
```

Atenção: teste `clientIDs == nil` e **não** `len(clientIDs) == 0` em qualquer
decisão Go que você acrescente — os dois casos têm significados opostos.

- [ ] **Step 3: `catalog/campaigns.go` — FinancialsByCampaign**

Assinatura `clientID *uuid.UUID` → `clientIDs []uuid.UUID`; no SQL (linha ~579):

```sql
		WHERE ($1::uuid[] IS NULL OR c.client_id = ANY($1))
```

- [ ] **Step 4: `catalog/detections.go` — os três filtros**

Nos structs `ListFilter` (linha 619), `ListPagedFilter` (linha 636) e
`AggregateFilter` (linha 952), trocar:

```go
	// ClientIDs, quando não-nil, restringe às detecções cujas campanhas
	// pertencem a esses clientes (carteira do viewer no JWT). nil = admin.
	ClientIDs []uuid.UUID
```

No SQL de `ListPaged` (linha 730) e de `List` (linha 820):

```sql
			  AND ($7::uuid[] IS NULL OR cmp.client_id = ANY($7))
```

e nas chamadas `d.pool.Query(...)` trocar `f.ClientID` por `f.ClientIDs` (é o
sétimo argumento nas duas). `AggregateFilter.ClientIDs` não vira filtro SQL — o
handler usa para o guard de posse, como já fazia.

- [ ] **Step 5: `catalog/live_map.go` — Get**

Assinatura `scope *uuid.UUID` → `scopes []uuid.UUID`. Onde hoje compara o
`clientID` resolvido da campanha com `*scope`, trocar por:

```go
	// scopes == nil = admin/operator. Fora da carteira → 404 anti-oracle.
	if scopes != nil && !slices.Contains(scopes, clientID) {
		return res, ErrCampaignNotFound
	}
```

Acrescentar `"slices"` aos imports do arquivo.

- [ ] **Step 6: Handlers de lista**

`handlers/campaigns.go:47` e `:162`:

```go
	scope := auth.ClientScopesFromContext(r.Context())
```

(o nome da variável não muda, então as chamadas a `h.Repo.ListPaged`,
`h.Repo.ListFiltered` e `h.Repo.FinancialsByCampaign` seguem iguais).

`handlers/detections.go:41` e `:98`:

```go
	f := catalog.ListFilter{
		ClientIDs: auth.ClientScopesFromContext(r.Context()),
	}
```

```go
	f := catalog.ListPagedFilter{
		ClientIDs: auth.ClientScopesFromContext(r.Context()),
	}
```

`handlers/live_map.go:47`:

```go
	scope := auth.ClientScopesFromContext(r.Context())
```

`handlers/clients.go:32` — o viewer passa a receber a carteira inteira:

```go
	// Viewer scope: cliente enxerga a própria carteira (1 item no caso comum,
	// N no caso de agência). Mesmo envelope {data: [...]} da lista global — a
	// UI trata igual, sem gates de role, e usa o tamanho da lista pra decidir
	// se mostra o seletor de cliente. Paginação não se aplica.
	if scopes := auth.ClientScopesFromContext(r.Context()); scopes != nil {
		items, err := h.Repo.ListByIDs(r.Context(), scopes)
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"data": items})
		return
	}
```

- [ ] **Step 7: Implementar `catalog.Clients.ListByIDs`**

Em `workers/internal/catalog/clients.go`, ao lado de `Get`:

```go
// ListByIDs devolve os clientes pedidos, ordenados por nome. Ids inexistentes
// são simplesmente omitidos (sem erro) — é o que a lista scope-aware precisa:
// um vínculo órfão não pode derrubar a tela inteira.
func (c *Clients) ListByIDs(ctx context.Context, ids []uuid.UUID) ([]Client, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT `+clientColumns+` FROM clients WHERE id = ANY($1) ORDER BY name`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Client{}
	for rows.Next() {
		cli, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *cli)
	}
	return out, rows.Err()
}
```

Use os mesmos nomes de constante/scanner que `Get` e `List` já usam nesse arquivo
(se a projeção estiver inline em vez de numa constante, replique a lista de
colunas de `List`).

- [ ] **Step 8: Compilar**

```bash
cd workers && go build ./...
```

Esperado: sem erros. Corrija os call sites de teste que ainda passem
`*uuid.UUID` para os filtros.

- [ ] **Step 9: Rodar os pacotes tocados**

```bash
cd workers && go test ./internal/catalog/... -p 1
cd workers && go test ./internal/api/... -p 1
```

Esperado: PASS. **Estes testes são a prova de paridade**: todos os fixtures
existentes usam um cliente só, então vê-los verdes significa que a carteira de
tamanho 1 devolve exatamente o que devolvia antes.

Falhas conhecidas **não** relacionadas: `internal/catalog
TestBuildDailySummary_WithDowntime` falha antes de ~13:00 UTC (usa
`time.Now().Add(-13h)`, que atravessa a meia-noite), e o harness de catalog tem
falhas pré-existentes de seed (`distribution_rules` sem `material_ids`, partição
de `detections`). Confirme que a falha existe também em `master` antes de
tratá-la como sua.

- [ ] **Step 10: Commit**

```bash
git add workers/internal/catalog/ workers/internal/api/handlers/
git commit -m "feat(multi-cliente): filtros de lista aceitam carteira de clientes"
```

---

## Task 6: `/insights` — cliente único validado contra a carteira

`GET /insights` exige `client_id` **mesmo do admin** e devolve 403 quando uma
campanha pedida não pertence a ele; `catalog.InsightsParams.ClientID` é
`uuid.UUID`, não ponteiro. A tela é por cliente por construção. O usuário de
agência escolhe um cliente, como o admin já faz.

**Files:**
- Modify: `workers/internal/api/handlers/insights.go:42-62`
- Test: `workers/internal/api/handlers/insights_test.go`

- [ ] **Step 1: Escrever os testes falhando**

Acrescentar em `workers/internal/api/handlers/insights_test.go`. O arquivo já tem
o `fakeInsightsRepo`, que registra os `InsightsParams` recebidos em `got` — é ele
que prova qual cliente o handler resolveu:

```go
// insightsClaims monta claims de viewer com a carteira informada.
func insightsClaims(wallet ...uuid.UUID) *auth.Claims {
	return &auth.Claims{Role: "viewer", ClientID: &wallet[0], ClientIDs: wallet}
}

// Agência com 2 clientes: sem client_id na query o handler não tem como
// escolher — 400, igual ao admin.
func TestInsights_MultiClientScope_RequiresClientID(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	h := &InsightsHandler{Repo: &fakeInsightsRepo{}}
	req := httptest.NewRequest("GET", "/insights?campaigns="+uuid.NewString(), nil)
	req = req.WithContext(auth.ContextWithClaims(req.Context(), insightsClaims(a, b)))
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

// client_id fora da carteira é 403 — não vaza dado de outro cliente, e o repo
// não chega a ser chamado.
func TestInsights_MultiClientScope_RejectsForeignClient(t *testing.T) {
	a, b, foreign := uuid.New(), uuid.New(), uuid.New()
	fake := &fakeInsightsRepo{}
	h := &InsightsHandler{Repo: fake}
	req := httptest.NewRequest("GET",
		"/insights?client_id="+foreign.String()+"&campaigns="+uuid.NewString(), nil)
	req = req.WithContext(auth.ContextWithClaims(req.Context(), insightsClaims(a, b)))
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
	if fake.hits != 0 {
		t.Fatalf("repo chamado %d vezes, want 0", fake.hits)
	}
}

// client_id dentro da carteira é aceito e é o que desce pro repo.
func TestInsights_MultiClientScope_AcceptsWalletClient(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	fake := &fakeInsightsRepo{out: &catalog.InsightsPayload{}}
	h := &InsightsHandler{Repo: fake}
	req := httptest.NewRequest("GET",
		"/insights?client_id="+b.String()+"&campaigns="+uuid.NewString(), nil)
	req = req.WithContext(auth.ContextWithClaims(req.Context(), insightsClaims(a, b)))
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if fake.got.ClientID != b {
		t.Fatalf("ClientID = %v, want %v", fake.got.ClientID, b)
	}
}

// Um cliente só: o client_id continua sendo FORÇADO pelo JWT, ignorando a
// query. É o comportamento anterior à feature e não pode regredir.
func TestInsights_SingleClientScope_IgnoresQueryClientID(t *testing.T) {
	own, foreign := uuid.New(), uuid.New()
	fake := &fakeInsightsRepo{out: &catalog.InsightsPayload{}}
	h := &InsightsHandler{Repo: fake}
	req := httptest.NewRequest("GET",
		"/insights?client_id="+foreign.String()+"&campaigns="+uuid.NewString(), nil)
	req = req.WithContext(auth.ContextWithClaims(req.Context(), insightsClaims(own)))
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if fake.got.ClientID != own {
		t.Fatalf("ClientID = %v, want %v (o da query tem que ser ignorado)",
			fake.got.ClientID, own)
	}
}
```

Acrescentar `"radiocheck/internal/auth"` aos imports do arquivo.

- [ ] **Step 2: Rodar e ver falhar**

```bash
cd workers && go test ./internal/api/handlers/ -run TestInsights_ -v -p 1
```

Esperado: FAIL — hoje o handler usa o primeiro (e único) cliente do escopo.

- [ ] **Step 3: Implementar a resolução do cliente**

Substituir o bloco de resolução de `client_id` (`handlers/insights.go:44-62`) por:

```go
	scopes := auth.ClientScopesFromContext(r.Context())

	// client_id:
	//   - admin (scopes == nil): obrigatório na query, como sempre foi.
	//   - carteira de 1 cliente: FORÇADO pelo JWT, query ignorada (anti-oracle,
	//     comportamento anterior à feature multi-cliente).
	//   - carteira de N clientes: obrigatório na query e precisa pertencer à
	//     carteira. A tela é por cliente por construção — somar valor
	//     consolidado de clientes diferentes não teria significado comercial.
	var clientID uuid.UUID
	switch {
	case len(scopes) == 1:
		clientID = scopes[0]
	default:
		cid := q.Get("client_id")
		if cid == "" {
			http.Error(w, "client_id required", http.StatusBadRequest)
			return
		}
		parsed, err := uuid.Parse(cid)
		if err != nil {
			http.Error(w, "invalid client_id", http.StatusBadRequest)
			return
		}
		if !auth.ScopeAllows(r.Context(), parsed) {
			http.Error(w, "forbidden_client_scope", http.StatusForbidden)
			return
		}
		clientID = parsed
	}
```

Atualizar também o comentário de doc do handler (linhas 30-32) para descrever as
três situações.

- [ ] **Step 4: Rodar os testes**

```bash
cd workers && go test ./internal/api/handlers/ -run TestInsights_ -v -p 1
```

Esperado: PASS nos três.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/insights.go workers/internal/api/handlers/insights_test.go
git commit -m "feat(multi-cliente): /insights valida client_id contra a carteira"
```

---

## Task 7: Login — clientes ativos definem o escopo

**Files:**
- Modify: `workers/internal/api/handlers/auth.go:88-131`
- Test: `workers/internal/api/handlers/auth_test.go`

- [ ] **Step 1: Escrever os testes falhando**

Acrescentar em `workers/internal/api/handlers/auth_test.go`, usando
`newAuthTestPool` e o padrão de seed de `TestAuth_Login_Viewer_TouchesLastLogin…`:

```go
// seedAgencyUser cria dois clientes (o segundo com o is_active informado) e um
// usuário Cliente vinculado aos dois. Devolve os ids na ordem A, B.
func seedAgencyUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	email string, aActive, bActive bool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	repo := users.NewRepo(pool)
	clients := catalog.NewClients(pool)

	a, err := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente A"})
	require.NoError(t, err)
	b, err := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente B"})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE clients SET is_active = $2 WHERE id = $1`, a.ID, aActive)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE clients SET is_active = $2 WHERE id = $1`, b.ID, bActive)
	require.NoError(t, err)

	hash, err := bcrypt.GenerateFromPassword([]byte("super-secret-pw-12345"), 10)
	require.NoError(t, err)
	u, err := repo.Create(ctx, users.CreateInput{
		Email: email, PasswordHash: string(hash),
		Role: "viewer", ClientID: &a.ID, Name: "Agência",
	})
	require.NoError(t, err)
	require.NoError(t, repo.SetClients(ctx, u.ID, []uuid.UUID{a.ID, b.ID}))
	return a.ID, b.ID
}

// Agência com 2 clientes, um deles desativado: entra, e o token carrega só o
// cliente ativo.
func TestAuth_Login_MultiClient_SkipsInactiveClient(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	cliA, _ := seedAgencyUser(t, ctx, pool, "ag@acme.com", true, false)

	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := NewAuthHandler(pool, users.NewRepo(pool))
	body := strings.NewReader(`{"email":"ag@acme.com","password":"super-secret-pw-12345"}`)
	req := httptest.NewRequest("POST", "/login", body)
	w := httptest.NewRecorder()
	h.Login(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp struct {
		Token string `json:"token"`
		User  struct {
			ClientIDs []uuid.UUID `json:"client_ids"`
		} `json:"user"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, []uuid.UUID{cliA}, resp.User.ClientIDs)

	claims, err := auth.ParseToken(resp.Token)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{cliA}, claims.ClientIDs)
}

// Todos os clientes desativados: bloqueia, como já bloqueava com 1 cliente.
func TestAuth_Login_AllClientsInactive_Blocked(t *testing.T) {
	ctx, pool := newAuthTestPool(t)
	seedAgencyUser(t, ctx, pool, "ag2@acme.com", false, false)

	t.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	h := NewAuthHandler(pool, users.NewRepo(pool))
	body := strings.NewReader(`{"email":"ag2@acme.com","password":"super-secret-pw-12345"}`)
	req := httptest.NewRequest("POST", "/login", body)
	w := httptest.NewRecorder()
	h.Login(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "client_disabled")
}
```

Acrescentar `"github.com/google/uuid"` e `"radiocheck/internal/auth"` aos imports
do arquivo.

- [ ] **Step 2: Rodar e ver falhar**

```bash
cd workers && go test ./internal/api/handlers/ -run TestLogin_ -v -p 1
```

Esperado: FAIL — hoje o gating olha só `u.ClientID`.

- [ ] **Step 3: Implementar o gating por carteira ativa**

Substituir o bloco de gating (`handlers/auth.go:88-107`) por:

```go
	// Cliente desativado bloqueia o login de TODOS os seus usuários (a empresa
	// foi suspensa, não cada conta individualmente). Com carteira multi-cliente
	// (agências), a regra é "pelo menos um cliente ativo": desativar um cliente
	// da carteira não pode derrubar a conta inteira, só some com aquele cliente
	// da visão. Com 1 cliente o comportamento é idêntico ao anterior.
	//
	// Fail-closed: a lista ativa é a que vai no token, então um vínculo órfão
	// (cliente deletado — não deveria, a FK é RESTRICT) simplesmente não entra.
	if len(u.ClientIDs) > 0 {
		rows, err := h.db.Query(r.Context(),
			`SELECT id FROM clients WHERE id = ANY($1) AND is_active = TRUE`,
			u.ClientIDs)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		active := []uuid.UUID{}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			active = append(active, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if len(active) == 0 {
			http.Error(w, "client_disabled", http.StatusForbidden)
			return
		}
		// O token carrega só os ativos. O principal também é reposicionado
		// quando ele mesmo foi desativado, pra não rotular a sessão com um
		// cliente que o usuário não enxerga mais.
		u.ClientIDs = active
		if u.ClientID == nil || !slices.Contains(active, *u.ClientID) {
			u.ClientID = &active[0]
		}
	}
```

Acrescentar `"slices"` aos imports.

- [ ] **Step 4: Devolver a carteira no payload de login**

Em `loginUser` (linha 39) acrescentar o campo e preenchê-lo na resposta:

```go
type loginUser struct {
	ID        uuid.UUID   `json:"id"`
	Email     string      `json:"email"`
	Role      string      `json:"role"`
	Name      string      `json:"name"`
	ClientID  *uuid.UUID  `json:"client_id,omitempty"`
	ClientIDs []uuid.UUID `json:"client_ids,omitempty"`
}
```

```go
	resp := loginResponse{Token: tok, ExpiresAt: expiresAt, User: loginUser{
		ID: u.ID, Email: u.Email, Role: u.Role, Name: u.Name,
		ClientID: u.ClientID, ClientIDs: u.ClientIDs,
	}}
```

- [ ] **Step 5: Rodar os testes**

```bash
cd workers && go test ./internal/api/handlers/ -run TestLogin_ -v -p 1
```

Esperado: PASS.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/api/handlers/auth.go workers/internal/api/handlers/auth_test.go
git commit -m "feat(multi-cliente): login escopa o token nos clientes ativos da carteira"
```

---

## Task 8: `/admin/users` — API da carteira

**Files:**
- Modify: `workers/internal/api/handlers/users.go` (payloads de Create e Patch)
- Modify: `workers/internal/catalog/clients.go` (`CountDependents`)
- Test: `workers/internal/api/handlers/users_test.go`

- [ ] **Step 1: Escrever os testes falhando**

Acrescentar em `workers/internal/api/handlers/users_test.go`, seguindo o padrão de
`TestUsers_Create_Client_RoleConvertsToViewer` (`newUsersTestPool`,
`NewUsersHandler(users.NewRepo(pool), nil)`, `reqWithIDParam` para o PATCH):

```go
// Criar usuário de agência com 2 clientes: o primeiro da lista vira o principal.
func TestUsers_Create_Client_WithClientIDs(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	clients := catalog.NewClients(pool)
	a, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente A"})
	b, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente B"})
	h := NewUsersHandler(users.NewRepo(pool), nil)

	body := `{"email":"ag@acme.com","password":"super-secret-pw-12345","name":"Agência",
	          "role":"client","client_ids":["` + a.ID.String() + `","` + b.ID.String() + `"]}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var u users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &u))
	require.ElementsMatch(t, []uuid.UUID{a.ID, b.ID}, u.ClientIDs)
	require.Equal(t, a.ID, *u.ClientID)
}

// client_id sozinho continua aceito: é o que o frontend antigo manda na janela
// entre o deploy do backend e o do frontend.
func TestUsers_Create_Client_LegacyClientIDStillWorks(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	clients := catalog.NewClients(pool)
	c, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Legado"})
	h := NewUsersHandler(users.NewRepo(pool), nil)

	body := `{"email":"legado@acme.com","password":"super-secret-pw-12345","name":"L",
	          "role":"client","client_id":"` + c.ID.String() + `"}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var u users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &u))
	require.Equal(t, []uuid.UUID{c.ID}, u.ClientIDs)
}

// Role client com carteira vazia é 400.
func TestUsers_Create_Client_EmptyWalletRejected(t *testing.T) {
	_, pool := newUsersTestPool(t)
	h := NewUsersHandler(users.NewRepo(pool), nil)

	body := `{"email":"sem@acme.com","password":"super-secret-pw-12345","name":"S",
	          "role":"client","client_ids":[]}`
	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Create(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "client_id_required")
}

// PATCH substitui a carteira inteira.
func TestUsers_Patch_ReplacesWallet(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	clients := catalog.NewClients(pool)
	a, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente A"})
	b, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente B"})
	repo := users.NewRepo(pool)
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "edit@acme.com", PasswordHash: "h", Role: "viewer",
		ClientID: &a.ID, Name: "Edit",
	})
	require.NoError(t, err)
	h := NewUsersHandler(repo, nil)

	body := `{"client_ids":["` + a.ID.String() + `","` + b.ID.String() + `"]}`
	req := reqWithIDParam("PATCH", "/admin/users/"+u.ID.String(), body, u.ID.String())
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got users.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.ElementsMatch(t, []uuid.UUID{a.ID, b.ID}, got.ClientIDs)
}

// Virar Administrador limpa a carteira — senão o filtro por vínculo continuaria
// achando o usuário pelo cliente antigo.
func TestUsers_Patch_ToAdmin_ClearsWallet(t *testing.T) {
	ctx, pool := newUsersTestPool(t)
	clients := catalog.NewClients(pool)
	a, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente A"})
	b, _ := clients.Create(ctx, catalog.CreateClientInput{Name: "Cliente B"})
	repo := users.NewRepo(pool)
	u, err := repo.Create(ctx, users.CreateInput{
		Email: "promo@acme.com", PasswordHash: "h", Role: "viewer",
		ClientID: &a.ID, Name: "Promo",
	})
	require.NoError(t, err)
	require.NoError(t, repo.SetClients(ctx, u.ID, []uuid.UUID{a.ID, b.ID}))
	h := NewUsersHandler(repo, nil)

	req := reqWithIDParam("PATCH", "/admin/users/"+u.ID.String(),
		`{"role":"admin"}`, u.ID.String())
	w := httptest.NewRecorder()
	h.Patch(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	got, err := repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.Nil(t, got.ClientID)
	require.Empty(t, got.ClientIDs)
}
```

- [ ] **Step 2: Rodar e ver falhar**

```bash
cd workers && go test ./internal/api/handlers/ -run 'TestUsers_Create_Client_|TestUsers_Patch_' -v -p 1
```

Esperado: FAIL — `client_ids` é ignorado no decode.

- [ ] **Step 3: Aceitar `client_ids` no Create**

Em `createUserPayload` (linha 171) acrescentar:

```go
	// ClientIDs é a carteira do usuário Cliente (agências). ClientID continua
	// aceito e equivale a uma lista de um elemento — é o que o frontend antigo
	// manda durante a janela entre o deploy do backend e o do frontend.
	// Quando os dois vêm, ClientIDs vence.
	ClientIDs []uuid.UUID `json:"client_ids,omitempty"`
```

No handler `Create`, logo após validar o role, normalizar a carteira:

```go
	// Normaliza carteira: client_ids vence, client_id é o fallback legado.
	wallet := p.ClientIDs
	if len(wallet) == 0 && p.ClientID != nil {
		wallet = []uuid.UUID{*p.ClientID}
	}
	if dbRole == "viewer" && len(wallet) == 0 {
		http.Error(w, "client_id_required", http.StatusBadRequest)
		return
	}
	if dbRole != "viewer" && len(wallet) > 0 {
		http.Error(w, "role_client_inconsistent", http.StatusBadRequest)
		return
	}
```

No `users.CreateInput`, o principal passa a sair da carteira normalizada — nunca
indexe `wallet[0]` sem o guard, porque para admin a lista é vazia:

```go
	var primary *uuid.UUID
	if len(wallet) > 0 {
		primary = &wallet[0]
	}
	// ... in := users.CreateInput{..., ClientID: primary, ...}
```

E, quando `len(wallet) > 1`, chamar `h.repo.SetClients` logo após o Create,
relendo o usuário para a resposta:

```go
	if len(wallet) > 1 {
		if err := h.repo.SetClients(r.Context(), u.ID, wallet); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if u, err = h.repo.Get(r.Context(), u.ID); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
```

- [ ] **Step 4: Aceitar `client_ids` no Patch**

Em `updateUserPayload` (linha 308) acrescentar o mesmo campo. No handler `Patch`,
depois do `h.repo.Update` bem-sucedido:

```go
	// Carteira: client_ids vence; client_id sozinho vira lista de um (já
	// aplicado pelo Update acima via in.ClientID, então só entra aqui quando a
	// lista veio explícita).
	if len(p.ClientIDs) > 0 && !in.ClearClient {
		if err := h.repo.SetClients(r.Context(), id, p.ClientIDs); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if u, err = h.repo.Get(r.Context(), id); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
```

E, quando o usuário vira admin (`in.ClearClient == true`), limpar a carteira
inteira — senão sobram vínculos órfãos que o filtro por vínculo ainda enxergaria:

```go
	if in.ClearClient {
		if _, err := h.repo.ClearClients(r.Context(), id); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
```

Implementar em `workers/internal/users/users.go`:

```go
// ClearClients remove todos os vínculos do usuário. Usado quando um Cliente
// vira Administrador — users.client_id já foi anulado pelo Update, e a carteira
// precisa acompanhar, senão o filtro por vínculo continuaria encontrando o
// usuário pelo cliente antigo.
func (r *Repo) ClearClients(ctx context.Context, userID uuid.UUID) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM user_clients WHERE user_id = $1`, userID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
```

- [ ] **Step 5: `CountDependents` conta por vínculo**

Em `workers/internal/catalog/clients.go:175`, trocar a terceira subquery:

```sql
			(SELECT COUNT(*) FROM user_clients WHERE client_id = $1)
```

Assim o 409 de delete de cliente continua protegendo vínculos secundários, que é
exatamente o que o `ON DELETE RESTRICT` da 0062 vai barrar no banco.

- [ ] **Step 6: Rodar os testes**

```bash
cd workers && go test ./internal/api/handlers/ -run 'TestUsers_Create_Client_|TestUsers_Patch_' -v -p 1
cd workers && go test ./internal/catalog/ -run TestClient -v -p 1
```

Esperado: PASS.

- [ ] **Step 7: Commit**

```bash
git add workers/internal/api/handlers/users.go workers/internal/api/handlers/users_test.go \
        workers/internal/users/users.go workers/internal/catalog/clients.go
git commit -m "feat(multi-cliente): /admin/users cria e edita carteira de clientes"
```

---

## Task 9: Verificação do backend antes do deploy

**Files:** nenhum — é uma porta de qualidade.

- [ ] **Step 1: Cross-compile Linux (é o que o Dockerfile faz)**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```

Esperado: sem saída. O build nativo do Windows passar **não** garante isto.

- [ ] **Step 2: Suite completa, um pacote por vez**

```bash
cd workers && go test ./... -p 1
```

Esperado: PASS, exceto as falhas pré-existentes conhecidas (§Task 5 Step 9).
Antes de acusar regressão, confirme que a falha aparece também em `master`.

- [ ] **Step 3: Nenhum resquício do escopo antigo**

```bash
cd workers && grep -rn "ClientScopeFromContext" --include="*.go" .
```

Esperado: nenhuma saída.

- [ ] **Step 4: Testar a migration contra dados de produção**

Regra 4.8 do CLAUDE.md: migration com `INSERT … SELECT` sobre dado real não pode
ser validada só em banco local. Restaure o dump mais recente de prod num Postgres
descartável e rode as migrations pendentes ali, conforme
[docs/operations/migrations.md](../../operations/migrations.md). O
`shadow_migration_test` do `deploy.sh` faz isso automaticamente e aborta o deploy
se falhar — este passo é para descobrir o problema antes.

- [ ] **Step 5: Commit (se algum ajuste saiu daqui)**

```bash
git add -A workers/
git commit -m "fix(multi-cliente): ajustes do cross-compile e da suite"
```

---

## Task 10: Frontend — sessão expõe a carteira

**Files:**
- Modify: `frontend/src/contexts/AuthContext.jsx:49-63`

- [ ] **Step 1: Expor `clientIds`**

Em `AuthContext.jsx`, junto de `clientId`:

```js
  const clientId = user?.client_id ?? null
  // Carteira de clientes (agências). Cai pro principal quando o backend é
  // anterior à feature multi-cliente — a lista nunca fica vazia pra um Cliente.
  const clientIds = user?.client_ids?.length
    ? user.client_ids
    : (clientId ? [clientId] : [])
```

E no objeto `value`, acrescentar `clientIds` depois de `clientId`.

- [ ] **Step 2: Verificar que a sessão persiste o campo**

O login grava o objeto `user` inteiro em `sessionStorage`, então `client_ids`
acompanha sem mudança adicional. Confirme lendo `login` no mesmo arquivo — se ele
selecionar campos manualmente, acrescente `client_ids` à seleção.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/contexts/AuthContext.jsx
git commit -m "feat(multi-cliente): AuthContext expoe a carteira de clientes"
```

---

## Task 11: Frontend — seletor de cliente em /insights e /live-map

**Files:**
- Modify: `frontend/src/components/insights/FiltersBar.jsx:100-103,169-200`
- Modify: `frontend/src/pages/LiveMapPage.jsx:122-153`

- [ ] **Step 1: `FiltersBar` — liberar o select para carteira com N clientes**

Substituir o `useEffect` que trava o cliente (linhas 169-177) por:

```js
  // Trava o clientId quando há exatamente um cliente na carteira (o caso
  // comum). Com dois ou mais (agência), quem escolhe é o usuário — o /insights
  // é por cliente por construção e o backend rejeita client_id fora da
  // carteira com 403.
  const canPickClient = isAdmin || clientOpts.length > 1

  useEffect(() => {
    if (canPickClient) return
    const only = clientOpts[0]?.value ?? null
    if (only && value.clientId !== only) {
      onChange({ ...value, clientId: only })
    }
    // Não dependemos de `value` inteiro pra evitar loop: só recalcula quando a
    // sessão troca ou a carteira muda.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canPickClient, clientOpts])
```

Trocar o gate de render (linha 182) de `{isAdmin ? (` para `{canPickClient ? (`.

Ajustar `ownClient` (linhas 100-103) para resolver pelo cliente **selecionado**,
não pelo `user.client_id`:

```js
  const ownClient = useMemo(() => {
    if (canPickClient) return null
    return clientOpts[0]?.raw ?? null
  }, [clientOpts, canPickClient])
```

E o `campOpts` (linha 112) passa a filtrar por cliente sempre que houver seleção,
não só para admin:

```js
    const scoped = value.clientId
      ? allCampaigns.filter(c => c.client_id === value.clientId)
      : allCampaigns
```

- [ ] **Step 2: `LiveMapPage` — mesma regra**

Substituir as linhas 122-141:

```js
  const { isAdmin } = useAuth()
  const [pickedClientId, setPickedClientId] = useState(null)

  const clientsQ = useClients()
  const clientOpts = useMemo(
    () => (clientsQ.data || []).map(c => ({ value: c.id, label: c.name, raw: c })),
    [clientsQ.data],
  )
  // Um cliente na carteira → fica travado nele (sem seletor), como antes.
  // Dois ou mais (agência) → o usuário escolhe; null = todos.
  const canPickClient = isAdmin || clientOpts.length > 1
  const clientId = canPickClient ? pickedClientId : (clientOpts[0]?.value ?? null)
  const ownClient = useMemo(
    () => (canPickClient ? null : clientOpts[0]?.raw ?? null),
    [clientOpts, canPickClient],
  )
```

Trocar todo uso de `setAdminClientId` por `setPickedClientId`, o gate de render do
select de cliente de `isAdmin` para `canPickClient`, e o filtro de campanhas
(linhas 148-151):

```js
    const rows = (clientId
      ? allCampaigns.filter(c => c.client_id === clientId)
      : allCampaigns
    ).filter(c => c.status !== 'cancelada')
```

- [ ] **Step 3: Build do frontend**

```bash
cd frontend && npm run build
```

Esperado: build sem erro. **Não rode `npm install`** — no Windows ele poda as
dependências opcionais de Linux do lockfile e quebra o `npm ci` do Cloudflare
Pages (regra 5 do CLAUDE.md).

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/insights/FiltersBar.jsx frontend/src/pages/LiveMapPage.jsx
git commit -m "feat(multi-cliente): seletor de cliente aparece para carteira com 2+"
```

---

## Task 12: Frontend — multi-select no /admin/users

**Files:**
- Modify: `frontend/src/components/UserFormModal.jsx:10,105,137,244-246`
- Modify: `frontend/src/pages/AdminUsersPage.jsx:444-445`

- [ ] **Step 1: Estado do formulário vira lista**

Em `UserFormModal.jsx`, no estado inicial (linha 10), trocar
`client_id: null` por `client_ids: []`, e na hidratação (linha 105):

```js
        client_ids: initial.client_ids?.length
          ? initial.client_ids
          : (initial.client_id ? [initial.client_id] : []),
```

- [ ] **Step 2: Enviar `client_ids`**

Na montagem do payload (linha 137):

```js
    // Agências: um usuário Cliente pode ter mais de um cliente vinculado. O
    // primeiro da lista vira o "principal" no backend.
    if (v.role === 'client') payload.client_ids = v.client_ids
```

- [ ] **Step 3: Trocar o select por multi-select**

No `RSelect` do cliente (linhas 244-246):

```jsx
              <RSelect
                isMulti
                options={clientOptions}
                value={clientOptions.filter(o => v.client_ids.includes(o.value))}
                onChange={opts => set('client_ids', (opts ?? []).map(o => o.value))}
```

Ajustar a validação de submit do modal para exigir `client_ids.length > 0` quando
o role for `client` (hoje ela testa `client_id`).

- [ ] **Step 4: Mostrar a carteira na lista**

Em `AdminUsersPage.jsx` (linhas 444-445), substituir a célula do cliente:

```jsx
                      {u.client_ids?.length
                        ? (
                          <span className="au-client-name">
                            {clientById[u.client_ids[0]]?.name ?? `#${u.client_ids[0].slice(0, 6)}`}
                            {u.client_ids.length > 1 && ` +${u.client_ids.length - 1}`}
                          </span>
                        )
                        : u.client_id
                          ? <span className="au-client-name">{clientById[u.client_id]?.name ?? `#${u.client_id.slice(0, 6)}`}</span>
```

mantendo o restante do ternário existente (o caso "sem cliente").

- [ ] **Step 5: Build**

```bash
cd frontend && npm run build
```

Esperado: build sem erro.

- [ ] **Step 6: Teste manual**

Com o backend rodando local, criar um usuário Cliente com dois clientes, logar com
ele e conferir: `/campaigns` lista campanhas dos dois; `/insights` exige escolher
um cliente e recusa (403) um `client_id` de fora; `/detections` mostra as duas
carteiras; `/admin/users` mostra `Cliente A +1`.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/UserFormModal.jsx frontend/src/pages/AdminUsersPage.jsx
git commit -m "feat(multi-cliente): /admin/users edita carteira com multi-select"
```

---

## Task 13: Documentação

**Files:**
- Create: `docs/features/multi-client-user.md`
- Modify: `docs/features/user-management.md`
- Modify: `docs/README.md`
- Modify: `CLAUDE.md` (linha do mapa de consulta)

- [ ] **Step 1: Escrever o doc da feature**

`docs/features/multi-client-user.md`, com o header YAML obrigatório:

```markdown
---
status: implementado
ultima-verificacao: 2026-08-04
codigo-relacionado:
  - migrations/0062_user_clients.up.sql
  - workers/internal/auth/scope.go
  - workers/internal/users/users.go
  - workers/internal/api/handlers/insights.go
  - frontend/src/components/insights/FiltersBar.jsx
---

# Usuário vinculado a múltiplos clientes (agências)
```

O corpo deve cobrir, sem repetir o plano: para que serve (agências), o modelo
(`user_clients` + cliente principal + trigger), como o escopo viaja no JWT
(`client_ids`, validade de 8h, tradução do token legado), a regra do `/insights`
(cliente único validado contra a carteira), a regra de login com cliente
desativado, e como o admin gerencia a carteira no `/admin/users`.

- [ ] **Step 2: Atualizar os índices**

Em `docs/features/user-management.md`, acrescentar um parágrafo apontando para o
doc novo e atualizar `ultima-verificacao`. Em `docs/README.md` e na tabela "Mapa de
consulta" do `CLAUDE.md`, acrescentar a linha:

```markdown
| Usuário de agência com vários clientes vinculados (carteira, escopo multi-cliente) | [docs/features/multi-client-user.md](docs/features/multi-client-user.md) |
```

- [ ] **Step 3: Commit**

```bash
git add docs/ CLAUDE.md
git commit -m "docs(multi-cliente): documenta a carteira de clientes por usuario"
```

---

## Ordem de deploy

1. **Backend primeiro:** `./scripts/deploy.sh` na VM. O `shadow_migration_test`
   aplica a 0062 sobre uma cópia dos dados de prod e aborta se falhar.
2. **Smoke test** com um usuário Cliente existente: login continua funcionando e
   `/campaigns` devolve as mesmas campanhas de antes (carteira de 1).
3. **Frontend depois:** push para `master` publica no Cloudflare Pages.

O claim de compatibilidade (Task 3) e o `client_id` ainda aceito na API de
usuários (Task 8) garantem que o frontend antigo funciona contra o backend novo
durante a janela entre os dois.

## Limpeza do ambiente de teste

```bash
docker rm -f rc-test-pg
```
