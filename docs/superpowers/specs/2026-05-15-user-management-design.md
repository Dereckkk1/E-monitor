# Gerenciamento de usuários — design

**Data**: 2026-05-15
**Status**: aprovado, pronto pra implementação
**Skill que gerou**: superpowers:brainstorming

---

## 1. Contexto e motivação

Hoje o sistema tem autenticação por JWT com 3 roles (`admin`, `operator`, `viewer`) mas **nenhuma tela de gerenciamento de usuários**: o único caminho pra criar conta é o bootstrap por env vars (`workers/internal/auth/bootstrap.go`), que só cria 1 admin. Sem tela e sem CRUD, é impossível dar acesso a clientes finais (a empresa anunciante quer ver as próprias campanhas/veiculações).

Esta feature adiciona:

1. CRUD de usuários acessível apenas a admins (`/admin/users`).
2. Vínculo opcional `user → client` para usuários do tipo "Cliente".
3. Filtragem server-side de campanhas/veiculações/relatórios por `client_id` quando o requester é cliente.
4. Sidebar diferenciada e tela "Minha conta" pra todos os usuários.

## 2. Decisões tomadas no brainstorming

| # | Decisão | Justificativa |
|---|---------|---------------|
| 1 | Vínculo user↔client é **N:1** (um cliente pode ter vários usuários, cada usuário pertence a um cliente) | Caso de uso confirmado pelo PO; agência multi-marca é exceção que faz 5 logins. |
| 2 | Mantém roles `admin`/`operator`/`viewer` no banco; UI expõe **"Administrador"** (admin) e **"Cliente"** (viewer) | Zero migração de constraint; `admin` e `operator` já são tratados como sinônimos no middleware existente. |
| 3 | Senha inicial é **definida pelo admin** no momento do cadastro | Sem infra de SMTP no projeto; usuário troca depois em "Minha conta". |
| 4 | Soft delete (`deleted_at`) **+** flag `is_active` | "Excluir" é definitivo (não permite reativar); "Desativar" é reversível (cliente atrasou pagamento, funcionário em férias). |
| 5 | Campos novos no schema: `name`, `phone`, `last_login_at`, `client_id`, `is_active`, `deleted_at`, `updated_at` | Mínimo razoável pra UI ter conteúdo útil (nome no lugar do email cru, último login pra saber se cliente está usando). |
| 6 | Edição admin permite **tudo exceto email**; reset de senha sempre disponível | Email é chave de login; trocar email confunde audit/recuperação. Pra trocar email, criar usuário novo e desativar antigo. |
| 7 | Cliente é **read-only** em campanhas/veiculações/relatórios | "Acesso" no escopo do PO é apenas visualização; admin segura todas as escritas. |
| 8 | FK `users.client_id` usa **ON DELETE RESTRICT** | Excluir empresa cliente é raro e deve ser deliberado; cascade apagaria logins sem aviso. |
| Extra | Cliente pode editar **próprio nome/telefone** e trocar **própria senha** em `/account` | Senão admin saberia a senha do cliente pra sempre, o que é ruim. |

## 3. Schema

Migração nova: `migrations/00NN_user_management.up.sql` (numeração escolhida na implementação; ler `docs/operations/migrations.md` antes de criar).

```sql
ALTER TABLE users
  ADD COLUMN client_id      UUID REFERENCES clients(id) ON DELETE RESTRICT,
  ADD COLUMN name           TEXT NOT NULL DEFAULT '',
  ADD COLUMN phone          TEXT,
  ADD COLUMN is_active      BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN deleted_at     TIMESTAMPTZ,
  ADD COLUMN last_login_at  TIMESTAMPTZ,
  ADD COLUMN updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE users
  ADD CONSTRAINT users_client_role_consistency CHECK (
    (role = 'viewer'  AND client_id IS NOT NULL) OR
    (role IN ('admin','operator') AND client_id IS NULL)
  );

CREATE INDEX idx_users_client_id     ON users(client_id) WHERE client_id IS NOT NULL;
CREATE INDEX idx_users_active        ON users(is_active) WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS users_email_key;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_email_key;
CREATE UNIQUE INDEX idx_users_email_active
  ON users(LOWER(email)) WHERE deleted_at IS NULL;
```

Notas:

- `idx_users_email_active` é UNIQUE parcial: permite reusar email após exclusão (linha antiga sai do índice).
- O bootstrap admin existente continua válido (role='admin', client_id=NULL — passa no CHECK).
- `.down.sql` reverte: drop constraint, drop indexes novos, drop colunas, recria `users_email_key` original.

## 4. Backend (Go)

### 4.1 Endpoints novos — admin only

| Método | Path | Descrição |
|--------|------|-----------|
| `GET`  | `/v1/internal/admin/users` | Lista paginada. Query: `?role=admin\|client`, `?client_id=`, `?status=active\|inactive\|deleted\|all`, `?q=`, `?page=`, `?page_size=` |
| `GET`  | `/v1/internal/admin/users/:id` | Detalhe |
| `POST` | `/v1/internal/admin/users` | Cria. Body: `{email, password, name, phone?, role:'admin'\|'client', client_id?}`. Backend converte `'client' → 'viewer'`. |
| `PATCH`| `/v1/internal/admin/users/:id` | Edita parcial: `{name?, phone?, role?, client_id?, is_active?}`. **Não aceita `email`**. |
| `POST` | `/v1/internal/admin/users/:id/password` | Reset. Body: `{password}`. |
| `DELETE`| `/v1/internal/admin/users/:id` | Soft delete. Bloqueia self-delete. |

### 4.2 Endpoints novos — self-service

| Método | Path | Descrição |
|--------|------|-----------|
| `GET`  | `/v1/internal/auth/me` | Próprio user (id, email, name, phone, role, client_id) |
| `PATCH`| `/v1/internal/auth/me` | Edita próprio `name` e `phone` |
| `POST` | `/v1/internal/auth/me/password` | Body: `{current_password, new_password}`. Exige senha atual. |

### 4.3 Validações

- Email: regex básica + lowercase + não-vazio. Conflito → `409 email_taken`.
- Senha: mínimo 12 chars (mesma regra do bootstrap).
- Cliente sem `client_id` → `400 client_id_required`.
- Admin com `client_id` → `400 client_id_not_allowed_for_admin`.
- `client_id` apontando pra cliente inexistente → `400 client_not_found`.
- Self-delete → `400 cannot_delete_self`.
- Self-deactivate → `400 cannot_deactivate_self`.
- Reativar (`is_active=true`) usuário com `deleted_at NOT NULL` → `400 cannot_reactivate_deleted` (excluir é definitivo).
- Tentar editar (`PATCH`) com campo `email` no body → `400 email_immutable`.
- Conta `is_active=false` ou `deleted_at NOT NULL` no login → `403 account_disabled`.

### 4.4 JWT

`workers/internal/auth/jwt.go`:

- Adiciona claim `client_id *uuid.UUID` no `Claims`.
- Renomeia `IssueToken(userID, role)` → `IssueTokenForUser(u *User)` pra evitar bug de esquecer o claim. Mantém wrapper antigo deprecado por compat até remover usos.

### 4.5 Middleware de scope

Arquivo novo `workers/internal/auth/scope.go`:

```go
// ClientScopeFromContext retorna o client_id do JWT se o usuário for viewer (cliente),
// ou nil se for admin/operator (acesso total).
func ClientScopeFromContext(ctx context.Context) *uuid.UUID
```

### 4.6 Handlers existentes que ganham scope

Quando `ClientScopeFromContext(ctx) != nil`, adiciona `WHERE client_id = $scope` (ou JOIN equivalente):

- `GET /v1/internal/campaigns` (lista)
- `GET /v1/internal/campaigns/:id` — retorna **404** se não for do cliente (não 403 — evita oracle)
- `GET /v1/internal/detections`
- `GET /v1/internal/reports/airtime`
- `GET /v1/internal/clients/:id/materials` — bloqueia se `:id` ≠ scope.client_id

Endpoints admin (stations, material-types, /admin/*) **não** ganham scope — viewer não pode acessar (`RequireRole("admin","operator")`).

### 4.7 Roteamento

```go
admin := r.Group("/v1/internal/admin")
admin.Use(auth.RequireJWT, auth.RequireRole("admin"))
admin.Group("/users").Routes(...)

me := r.Group("/v1/internal/auth/me")
me.Use(auth.RequireJWT)  // qualquer role autenticada
me.Routes(...)

// Endpoints existentes que ganham scope viewer:
authedRead := r.Group("/v1/internal")
authedRead.Use(auth.RequireJWT, auth.RequireRole("admin","operator","viewer"))
// ... GET /campaigns, /detections, /reports/airtime
```

### 4.8 Login handler

`POST /v1/internal/auth/login` ganha:

1. Bloqueia login se `is_active=false` OR `deleted_at NOT NULL` → `403 account_disabled`.
2. `UPDATE users SET last_login_at = NOW() WHERE id = $1` em sucesso.
3. Resposta inclui `client_id` no objeto `user`.

## 5. Frontend (React)

### 5.1 Sidebar admin (`frontend/src/components/Sidebar.jsx`)

Adicionar item em "Administração":

```
📊 Administração
  ├─ Visão geral → /admin/overview
  └─ Usuários   → /admin/users
```

### 5.2 Sidebar cliente (substitui o `ClientNav` atual)

```
🏠 Início
  └─ Dashboard          → /dashboard      (placeholder "em breve")

📡 Veiculação
  ├─ Campanhas          → /campaigns      (read-only)
  ├─ Veiculações        → /detections     (read-only)
  └─ Relatório data/hora → /reports/airtime (read-only)

👤 Conta
  └─ Minha conta        → /account
```

### 5.3 AuthContext

Em `frontend/src/contexts/AuthContext.jsx`:

```jsx
const isAdmin   = user?.role === 'admin' || user?.role === 'operator'
const isClient  = user?.role === 'viewer'
const clientId  = user?.client_id ?? null
```

### 5.4 Guard de rotas

Componente `<RequireRole roles={['admin']}>` em `frontend/src/components/RequireRole.jsx`. Cliente entrando em rota admin redireciona pra `/campaigns`.

Páginas de campanhas/detections/reports — esconder botões "Nova", "Editar", "Cancelar" quando `!isAdmin`.

### 5.5 Página `/admin/users`

`frontend/src/pages/AdminUsersPage.jsx`:

- Header: título + botão `[+ Novo usuário]`.
- Filtros: busca livre, tipo (Todos/Admin/Cliente), cliente vinculado (RSelect, aparece se tipo=Cliente), status (Ativos/Inativos/Excluídos/Todos).
- Tabela: Nome / Email / Tipo / Cliente / Último login / Status / Ações.
- Ações por linha (menu kebab): Editar / Resetar senha / Desativar (ou Reativar) / Excluir.
- Paginação `AirtimePaginator` existente.
- Self-actions desabilitadas (admin não pode deletar/desativar a si mesmo).

### 5.6 Modais

- **Novo usuário**: tipo (RSelect), cliente (RSelect com busca, condicional), nome, email, telefone (máscara BR), senha (com "Gerar senha forte" — 16 chars cripto-aleatórios). Validação client-side básica + erros de servidor.
- **Editar usuário**: idêntico, sem senha, com email travado, com toggle `is_active`.
- **Resetar senha**: nova senha + "Gerar senha forte" + Confirmar.
- **ConfirmModal** existente reusado pra desativar/excluir.

### 5.7 Página `/account` (Minha conta)

`frontend/src/pages/AccountPage.jsx`. Single-column:

- **Dados pessoais**: form com nome, telefone editáveis; email como label travada.
- **Trocar senha**: senha atual + nova + confirmação. Mín 12 chars.
- Botão "Sair" (já existe na sidebar, mas vale ter aqui também).

### 5.8 Hooks (`frontend/src/api/hooks.js`)

`useUsersPaged`, `useUser`, `useCreateUser`, `useUpdateUser`, `useResetUserPassword`, `useDeleteUser`, `useToggleUserActive`, `useMe`, `useUpdateMe`, `useChangeMyPassword`. Padrão React Query.

## 6. Testes

### 6.1 Backend

- Constraint do banco: tentar inserir admin com `client_id` ou viewer sem → erro.
- Email único parcial: deletar usuário X com email A, criar novo com email A → ok.
- CRUD: admin cria/edita/deleta; non-admin recebe 403.
- Scope: viewer só vê campanhas/detections do próprio cliente; tentativa de acessar `/v1/internal/clients/{outro_id}/materials` → 403.
- Login: `is_active=false` retorna 403; sucesso atualiza `last_login_at`.
- Self-delete bloqueado: admin tentando `DELETE /admin/users/{próprio_id}` → 400 `cannot_delete_self`.
- Reset de senha: admin pode em qualquer usuário; non-admin só na própria via `/me/password` (com senha atual).

### 6.2 Frontend

- Cliente entrando em `/admin/users` → redireciona pra `/campaigns`.
- Cliente em `/campaigns` não vê botão "Nova campanha".
- Form de criar usuário: tipo=Cliente exibe dropdown de cliente; tipo=Admin esconde.
- "Gerar senha forte" preenche campo com 16 chars.
- Self-row desabilita ações de excluir/desativar.

## 7. Documentação

- Criar `docs/features/user-management.md` (header YAML `status: implementado`, lista de código relacionado).
- Atualizar `docs/operations/auth-bootstrap.md` removendo da §8 (Próximos passos) os itens implementados.
- Atualizar `CLAUDE.md` mapa de consulta com linha "Gerenciamento de usuários (admin/cliente, /admin/users)".
- Adicionar entrada em `docs/README.md`.

## 8. Fora de escopo (explícito)

- Refresh tokens / httpOnly cookies (continua F-XX no roadmap; JWT 8h sem refresh por enquanto).
- Blacklist de JWT (logout não invalida token, só limpa storage do cliente).
- Email de convite / SMTP (admin define senha e passa por fora — decisão da P3).
- 2FA / MFA.
- Auditoria de ações ("quem criou esta campanha"). Pode entrar depois usando `last_login_at` e logs estruturados.
- Multi-tenant N:M (um usuário em vários clientes). Hoje N:1; pra mudar no futuro, basta tabela `user_clients` e remover FK direta.
- Avatar / upload de foto de perfil.
- Permissões granulares (RBAC com permissions). Continuamos com role flat.

## 9. Critério de aceitação

1. Admin consegue criar, editar, resetar senha, desativar, reativar e excluir usuários pelo `/admin/users`.
2. Admin não consegue excluir/desativar a si mesmo.
3. Usuário cliente entra no sistema, vê apenas Dashboard (placeholder), Campanhas, Veiculações, Relatório data/hora — todos filtrados pelo seu `client_id` e read-only.
4. Cliente em `/account` consegue editar próprio nome/telefone e trocar a própria senha (exigindo senha atual).
5. Login bloqueia conta `is_active=false` ou `deleted_at NOT NULL` com erro claro.
6. Tentativa de acessar campanha/material de outro cliente retorna 404 (campanha) ou 403 (materiais), nunca expõe dado.
7. Email único entre não-deletados (pode reusar email de usuário excluído).
