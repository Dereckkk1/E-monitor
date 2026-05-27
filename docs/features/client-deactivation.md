---
status: implementado
ultima-verificacao: 2026-05-27
codigo-relacionado:
  - migrations/0034_clients_is_active.up.sql
  - migrations/0034_clients_is_active.down.sql
  - workers/internal/catalog/clients.go
  - workers/internal/api/handlers/clients.go
  - workers/internal/api/handlers/auth.go
  - workers/internal/api/router.go
  - frontend/src/api/hooks.js
  - frontend/src/pages/ClientsPage.jsx
  - frontend/src/pages/LoginPage.jsx
---

# Desativação de cliente + bloqueio de hard-delete

## Problema que originou

`DELETE /v1/internal/clients/{id}` retornava **500** para qualquer cliente que
tivesse campanhas, materiais ou usuários vinculados. Causa: o repo fazia
`DELETE FROM clients` direto e o handler mapeava só `pgx.ErrNoRows` → 404;
**qualquer outro erro caía no 500**, incluindo a `foreign_key_violation`
(SQLSTATE 23503) levantada pelas FKs que protegem o cliente:

| Tabela | FK | `ON DELETE` |
|--------|-----|-------------|
| `campaigns` | `client_id → clients(id)` | (default) `NO ACTION` → bloqueia |
| `materials` | `client_id → clients(id)` | `RESTRICT` → bloqueia |
| `users` | `client_id → clients(id)` | `RESTRICT` → bloqueia |
| `api_keys`, `webhook_*` | `client_id → clients(id)` | `CASCADE` → somem junto |

A spec de user-management (decisão #8) escolheu `RESTRICT` **de propósito**:
excluir uma empresa cliente é raro e deve ser deliberado; cascade apagaria
logins sem aviso. O bug não era o bloqueio — era bloquear com um 500 mudo.

## Comportamento atual

### Hard-delete bloqueado → 409 (não mais 500)

`Clients.Delete` detecta o erro 23503 e devolve a sentinela
`catalog.ErrClientHasDependents`. O handler responde **409 Conflict** com a
contagem do que está vinculado, pra UI explicar e oferecer a alternativa:

```json
{ "error": "client_has_dependents", "campaigns": 2, "materials": 3, "users": 1 }
```

Contagens via `CountDependents` contam **referências FK cruas** (inclusive
usuários soft-deletados, que ainda seguram a FK e ainda disparam o RESTRICT),
então o número explica honestamente por que o delete foi recusado.

### Desativar (alternativa reversível ao delete)

Coluna `clients.is_active BOOLEAN NOT NULL DEFAULT TRUE` (migration 0034).
`NULL`/ausente/`true` = ativo; `false` = desativado. Endpoints:

```
POST /v1/internal/clients/{id}/deactivate   → 200 {client}  | 404
POST /v1/internal/clients/{id}/activate     → 200 {client}  | 404
```

Ambos atrás de `RequireRole("admin","operator")` (mesmo grupo do delete).

### Login gating

`auth.Login` bloqueia o login de **todos** os usuários de um cliente
desativado (a empresa foi suspensa, não cada conta). Fail-closed: se a linha do
cliente sumir (não deveria — FK é RESTRICT), também bloqueia. Resposta:
**403 `client_disabled`** (distinto de `account_disabled`, que é a conta
individual). O `LoginPage` mostra "Acesso desativado. Procure o administrador."
para ambos os 403.

### Listagem: dois modos, dois comportamentos

- **`GET /clients` sem paginação** (lookup map — resolve nome/logo por id em
  CampaignsPage, DetectionsPage, dropdowns) → retorna **todos** (ativos +
  inativos). Inativos precisam continuar resolvíveis pra não virar "cliente
  desconhecido" em campanhas existentes.
- **`GET /clients?page=&page_size=` (ListPaged — tela de gestão)** → esconde
  inativos por padrão. `?include_inactive=1` revela (toggle "Mostrar inativos").

### Frontend (`/clients`)

- Botão **"Mostrar inativos"** alterna `include_inactive`.
- Cliente inativo: linha esmaecida + badge **"Inativo"**; ação de lixeira vira
  **"Reativar"**.
- Clicar excluir → no 409, um `confirm` explica os vínculos e oferece
  **desativar**; se aceitar, chama `/deactivate`.
- **Robustez de transição:** o frontend só trata como inativo quando
  `is_active === false` *explícito*. Campo ausente (API ainda não migrada/
  rebuildada) = ativo — bate com o `DEFAULT TRUE` e evita marcar todos como
  inativos contra um backend antigo.

## Deploy (ordem importa)

A coluna e os endpoints só funcionam após **migrar + rebuildar a API**. Frontend
novo contra backend antigo: o delete continua 500 e `is_active` vem ausente
(tratado como ativo pela robustez acima, então a lista fica correta, mas
desativar/reativar ainda não existe até o backend subir). Ver
[operations/migrations.md](../operations/migrations.md) e
[operations/deploy.md](../operations/deploy.md).

## Limitações conhecidas

- O dropdown de seleção de cliente (wizard de campanha) usa o `GET /clients` sem
  paginação, que inclui inativos — então ainda é possível criar campanha pra um
  cliente desativado. Não gateado neste passo; gate futuro se virar problema.
- Relacionado a **F-85** ([roadmap/follow-ups-fase2.md](../roadmap/follow-ups-fase2.md)):
  `Delete` agora é "loud" (erro tipado), mas a padronização ampla de
  Delete/Update entre os repos do catalog segue pendente.
