---
status: implementado
ultima-verificacao: 2026-05-17
codigo-relacionado:
  - migrations/0027_user_management.up.sql
  - workers/internal/users/users.go
  - workers/internal/auth/scope.go
  - workers/internal/auth/jwt.go
  - workers/internal/api/handlers/users.go
  - workers/internal/api/handlers/me.go
  - workers/internal/api/handlers/auth.go
  - frontend/src/pages/AdminUsersPage.jsx
  - frontend/src/pages/AccountPage.jsx
  - frontend/src/components/RequireRole.jsx
  - frontend/src/components/UserFormModal.jsx
  - frontend/src/components/ResetPasswordModal.jsx
---

# Gerenciamento de usuários

CRUD de usuários no painel admin (`/admin/users`) e tela "Minha conta"
(`/account`) para self-service. Suporta dois tipos de conta:

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
`WHERE cmp.client_id = $scope` quando o requester é viewer.

Acesso cross-client a `/campaigns/:id` ou `/detections/:id` retorna **404**
(evita oracle de existência). Acesso a `/clients/{outro_id}/materials`
retorna **403** `forbidden_client_scope`.

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
   nome, email, telefone, senha (ou "Gerar" → 16 chars cripto-seguros).
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
- Email é imutável após criação (PATCH com `email` → 400 `email_immutable`).

## Fora de escopo (ver follow-ups)

- Refresh token / httpOnly cookie.
- 2FA / MFA.
- Audit log de ações.
- Email de convite (sem SMTP no projeto — admin define senha e passa por fora).
- N:M user↔clientes (agência multi-marca → 1 login por marca).


---

## Design & origem

Specs e planos que originaram esta doc (histórico de desenvolvimento):

- **Spec:** [Gerenciamento de usuários — design](../superpowers/specs/2026-05-15-user-management-design.md)
- **Plano:** [Gerenciamento de usuários — plano](../superpowers/plans/2026-05-15-user-management.md)
