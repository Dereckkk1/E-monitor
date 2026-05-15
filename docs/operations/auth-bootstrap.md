---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - workers/internal/auth/bootstrap.go
  - workers/internal/auth/jwt.go
  - workers/internal/api/handlers/auth.go
  - frontend/src/contexts/AuthContext.jsx
  - migrations/0007_fase2_auth.up.sql
---

# Bootstrap de admin & login

Documenta como o primeiro usuário admin é criado, como trocar a senha e o
fluxo completo de login (backend `cmd/api` + frontend SPA).

Referências cruzadas:
- §16 do `plano_implementacao.md` — autenticação e RBAC.
- `workers/internal/auth/bootstrap.go` — implementação.
- `migrations/0007_fase2_auth.up.sql` — schema da tabela `users`.

---

## 1. Como o bootstrap funciona

No startup, `cmd/api` chama `auth.EnsureAdmin(ctx, pool, cfg, logger)`
logo após abrir o pool de DB.

A função:

1. Lê as variáveis de ambiente:
   - `RADIOCHECK_BOOTSTRAP_ADMIN_EMAIL`
   - `RADIOCHECK_BOOTSTRAP_ADMIN_PASSWORD`
2. Se qualquer uma estiver vazia → no-op silencioso (a API continua
   subindo normalmente, sem criar nada).
3. Valida o email (precisa conter `@`) e o tamanho da senha (mínimo 12
   caracteres).
4. `SELECT EXISTS (SELECT 1 FROM users WHERE email = $1)`.
   - Existe: log `bootstrap admin already exists` (com email mascarado) e
     retorna sem alterar nada. **A senha não é atualizada.**
   - Não existe: gera bcrypt cost 10, `INSERT INTO users (email, password_hash, role)`
     e log `bootstrap admin created`.

A operação é **idempotente**: rodar a API várias vezes com as mesmas env
vars não cria duplicatas e não sobrescreve a senha de um usuário já
existente.

### Mascaramento de email no log

Para evitar vazar o email completo nos logs, ele é mascarado:
`alice@example.com` → `a***@example.com`.

---

## 2. Configuração

`.env` (copie de `.env.example` e preencha):

```env
RADIOCHECK_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
RADIOCHECK_BOOTSTRAP_ADMIN_PASSWORD=changeme-12chars-min
JWT_SECRET=replace-with-32-byte-random-secret-please
```

Gere um `JWT_SECRET` decente:

```bash
openssl rand -hex 32
```

> **Segurança:** após o primeiro startup com sucesso, **remova
> `RADIOCHECK_BOOTSTRAP_ADMIN_PASSWORD` do ambiente**. A senha já está
> no banco em bcrypt, não há motivo para mantê-la em texto plano em
> arquivos `.env` que vão para servidores e backups.

---

## 3. Como testar localmente

Pré-requisitos: docker, go ≥1.22, Node ≥20.

```bash
# 1. .env preenchido (ver seção 2).
cd /caminho/Radiocheck
cp .env.example .env
# edite .env

# 2. Suba postgres + nats (resto opcional).
docker-compose -f infra/docker/docker-compose.yml up -d postgres nats

# 3. Rode as migrations (depende do seu fluxo; goose, dbmate, etc.).
#    Verifique que a 0007_fase2_auth foi aplicada.

# 4. Suba a API.
cd workers
go run ./cmd/api
# Espera-se um log: "bootstrap admin created" (primeira vez) ou
# "bootstrap admin already exists" nas execuções seguintes.

# 5. Em outro terminal, suba o frontend.
cd frontend
npm install
npm run dev

# 6. Abra http://localhost:5173 → você é redirecionado para /login.
#    Use as credenciais do .env. Após sucesso, navega para /stations.
```

Sanity-check direto via curl (sem o frontend):

```bash
curl -s -X POST http://localhost:8080/v1/internal/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"admin@example.com","password":"changeme-12chars-min"}'
# resposta esperada:
# {"token":"eyJ...","expires_at":"...","user":{"id":"...","email":"...","role":"admin"}}
```

---

## 4. Trocar a senha do admin

Não há endpoint nativo de troca de senha ainda (TODO §16). Por enquanto
faça via SQL, gerando o hash bcrypt fora do banco para evitar dependência
da extensão `pgcrypto` (a migration atual não a habilita):

```bash
# Gere o hash (Python; cost 10 = default do auth.EnsureAdmin):
python -c "import bcrypt; print(bcrypt.hashpw(b'nova-senha-12+', bcrypt.gensalt(10)).decode())"
```

```sql
UPDATE users
SET password_hash = '<hash-gerado-acima>'
WHERE email = 'admin@example.com';
```

Após isso, o JWT já emitido continua válido até expirar (8h). Para
revogar imediatamente, restart da API + login novo (não temos blacklist
de tokens — TODO).

---

## 5. Criar mais usuários

Igual a trocar senha: via SQL, até termos o endpoint admin de
gerenciamento de usuários (TODO §16).

```sql
INSERT INTO users (email, password_hash, role)
VALUES ('operator@example.com', '<bcrypt>', 'operator');
```

Roles válidas (CHECK constraint em `0007_fase2_auth.up.sql`):
`admin`, `operator`, `viewer`.

---

## 6. Frontend — como o token é usado

- Login: `POST /v1/internal/auth/login` retorna `{token, expires_at, user}`.
- O `AuthContext` (`frontend/src/contexts/AuthContext.jsx`) persiste
  `token` e `user` em `sessionStorage` (`rc_token`, `rc_user`). Isso
  significa que **fechar a aba** desconecta o usuário; um simples
  refresh mantém a sessão.
- O interceptor de request em `frontend/src/api/client.js` injeta
  `Authorization: Bearer <token>` em **toda** chamada para
  `/v1/internal/*`.
- O interceptor de response detecta `401`, limpa o storage e redireciona
  para `/login?next=<rota-atual>` para que o usuário volte ao lugar de
  onde veio depois de re-autenticar.
- `RequireAuth` (`frontend/src/components/RequireAuth.jsx`) protege
  todas as rotas exceto `/login`.

---

## 7. Troubleshooting

| Sintoma | Causa provável | Como verificar |
|--------|----------------|----------------|
| API não sobe, "bootstrap admin: password too short" | Env var com menos de 12 chars | `echo $RADIOCHECK_BOOTSTRAP_ADMIN_PASSWORD` |
| API não sobe, "JWT_SECRET must be at least 32 bytes" | `JWT_SECRET` curto/ausente | `openssl rand -hex 32 > /tmp/jwt && export JWT_SECRET=$(cat /tmp/jwt)` |
| `/auth/login` retorna 401 mesmo com credenciais corretas | Hash bcrypt corrompido na tabela / email diferente do esperado | `SELECT email, length(password_hash) FROM users;` (deve ser 60 chars) |
| Frontend dá 401 em qualquer rota mesmo logado | Token expirou (>8h) ou `JWT_SECRET` mudou | F12 → Network → `Authorization` no header; relogar |
| Loop infinito de redirect para /login | Bug; `clearStoredAuth` está sendo chamado durante login | Ver console: a interceptor pula 401 quando URL termina com `/auth/login` |

---

## 8. TODO

- [ ] Endpoint admin: `POST /v1/internal/auth/users` para CRUD de usuários.
- [ ] Endpoint de troca de senha: `POST /v1/internal/auth/password`.
- [ ] Refresh token em httpOnly cookie + rotação.
- [ ] Blacklist de JWT (revogação imediata).
- [ ] UI admin para gerenciar `users` e `api_keys`.
