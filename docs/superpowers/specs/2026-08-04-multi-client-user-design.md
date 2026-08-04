# Usuário vinculado a múltiplos clientes (agências) — design

**Data:** 2026-08-04
**Status:** aprovado, não implementado

## 1. Problema

Hoje um usuário de role `viewer` (a "Cliente" da UI) aponta para **exatamente um**
cliente: `users.client_id` é uma coluna única com FK e uma CHECK da migration 0027
(`role='viewer'` ⇒ `client_id NOT NULL`). Todo o isolamento multi-tenant passa por
um único ponto — `auth.ClientScopeFromContext` (`workers/internal/auth/scope.go`)
— que devolve `*uuid.UUID` e vira `client_id = $N` no SQL dos repos.

Agências enviam vários clientes para a operação e precisam de **um usuário que
acesse a carteira inteira**. Hoje isso só é possível criando um login por cliente.

## 2. Decisões de produto (tomadas com o dono)

1. **Visão agregada por padrão, filtro opcional.** Com 2+ clientes vinculados, as
   telas mostram tudo junto; um seletor "Cliente" permite estreitar. Com 1 cliente
   o comportamento é idêntico ao de hoje, sem seletor nenhum.
2. **Sem entidade "Agência".** O vínculo é direto usuário → N clientes. Nenhum
   conceito novo de negócio, nenhuma marca/logo de agência.
3. **Filtro por tela, não global.** O seletor mora nas barras de filtro que já
   existem (as mesmas que o admin usa), não num seletor de workspace no cabeçalho.
4. **Relatórios continuam por cliente.** CSV consolidado/detalhado, PDF de campanha
   e export do /insights seguem sendo de um cliente (ou de uma campanha, que já
   pertence a um cliente). O gerador de relatório não muda.

## 3. Restrições do sistema que moldam a solução

- **Cliente é read-only.** O subgrupo A do router (`workers/internal/api/router.go`,
  a partir da linha 156) só expõe GET para `viewer`. Não existe o problema de
  "em qual dos clientes esse write cai".
- **`GET /clients` já é scope-aware** (`handlers/clients.go:32`): devolve ao viewer
  uma lista com o próprio cliente. Passando a devolver N, o frontend ganha a fonte
  de dados do seletor de graça.
- **As barras de filtro já têm seletor de Cliente**, gateado por `isAdmin`
  (`frontend/src/components/insights/FiltersBar.jsx:182`).
- **Frontend e backend sobem em momentos diferentes:** o Cloudflare Pages publica o
  frontend a cada push em master; o backend só com `scripts/deploy.sh` na VM. O
  design precisa funcionar nos dois sentidos durante a janela.
- **JWT tem validade de 8h e o middleware `RequireJWT` não toca no banco.** Manter
  isso é requisito de performance; logo o escopo viaja no token.

## 4. Abordagem escolhida

Tabela de vínculo `user_clients` como carteira, mantendo `users.client_id` como
**cliente principal**.

Alternativas descartadas:

- **Normalizar de vez (dropar `users.client_id`).** Fonte única de verdade, sem
  dual-write, mas obriga a mexer na CHECK 0027, no gating de login, em
  `CountDependents`, no filtro `?client_id=` do /admin/users e no claim do JWT tudo
  no mesmo deploy, sem janela de compatibilidade entre front e back. Fica como
  faxina posterior, se valer a pena.
- **Coluna array `users.client_ids uuid[]`.** Sem tabela nova, mas Postgres não faz
  FK de elemento de array: cliente deletado deixaria UUID órfão e o 409 de "cliente
  tem vínculos" pararia de proteger.

## 5. Modelo de dados

### 5.1 Migration `0062_user_clients`

```sql
CREATE TABLE user_clients (
    user_id    UUID NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    client_id  UUID NOT NULL REFERENCES clients(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, client_id)
);
CREATE INDEX idx_user_clients_client ON user_clients(client_id);

INSERT INTO user_clients (user_id, client_id)
SELECT id, client_id FROM users WHERE client_id IS NOT NULL
ON CONFLICT DO NOTHING;
```

- `ON DELETE RESTRICT` no cliente espelha a FK `users_client_id_fkey` de hoje — é o
  que sustenta o 409 "cliente tem vínculos".
- `ON DELETE CASCADE` no usuário porque o vínculo não tem vida própria. (Usuário é
  soft-deleted na prática; o CASCADE cobre o hard-delete de teste/limpeza.)
- O backfill é idempotente (`ON CONFLICT DO NOTHING`) e não pode colidir: a PK é
  `(user_id, client_id)` e cada usuário tem no máximo um `client_id` hoje.

### 5.2 Invariante `users.client_id ∈ user_clients`

Garantida por trigger `AFTER INSERT OR UPDATE OF client_id ON users FOR EACH ROW
WHEN (NEW.client_id IS NOT NULL)`, que insere a linha correspondente com
`ON CONFLICT DO NOTHING`.

O trigger **só adiciona, nunca remove**. Remover vínculo é sempre explícito, via
repo. A escolha de um trigger auto-corretivo (em vez de uma constraint que rejeita)
é deliberada: qualquer caminho legado que escreva `users.client_id` — fluxo de
boas-vindas, testes, reparo manual em prod — fica automaticamente consistente em
vez de quebrar.

### 5.3 Semântica de "cliente principal"

`users.client_id` continua `NOT NULL` para viewer (CHECK 0027 intacta) e passa a
significar **cliente principal**: usado no cabeçalho da página de boas-vindas
(`workers/internal/welcome/repo.go:167`) e como rótulo curto.

Ao salvar a carteira, o principal é **mantido se continuar na lista**; caso
contrário vira o primeiro selecionado. Determinístico e estável.

## 6. Backend

### 6.1 Claims e helpers de escopo

`auth.Claims` ganha `ClientIDs []uuid.UUID \`json:"client_ids,omitempty"\`` e
**mantém** `ClientID` (o principal).

Dois helpers substituem `ClientScopeFromContext`, porque os call sites atuais fazem
duas coisas diferentes:

| Helper | Uso | Contrato |
|---|---|---|
| `ClientScopesFromContext(ctx) []uuid.UUID` | filtro de lista que desce pro repo | `nil` = sem escopo (admin/operator vê tudo) |
| `ScopeAllows(ctx, clientID) bool` | check pontual 404 anti-oracle | `true` quando não há escopo ou o cliente está na carteira |

Regras preservadas do código atual:

- **Fail-closed:** viewer sem nenhum vínculo devolve `[]uuid.UUID{uuid.Nil}` (não
  `nil`), forçando resultado vazio em vez de acesso total.
- **Compatibilidade de token:** se `client_ids` vier ausente mas `client_id`
  presente (JWT emitido antes do deploy, válido por até 8h), o helper devolve
  `[]uuid.UUID{*client_id}`. Sem isso, todo cliente logado seria deslogado no
  deploy.

`ClientScopeFromContext` é **removida** — deixá-la viva criaria um caminho que só
enxerga um cliente e vaza inconsistência silenciosa.

### 6.2 Call sites a migrar

**Filtro de lista → `ClientScopesFromContext`:**

- `handlers/campaigns.go:47` (List) e `:162` (Financials)
- `handlers/detections.go:41` (List) e `:98` (AggregateByMaterial)
- `handlers/insights.go:44`
- `handlers/live_map.go:47`
- `handlers/clients.go:32` (List — devolve os N clientes da carteira)

**Check pontual → `ScopeAllows`:**

- `handlers/campaigns.go:150` (Get)
- `handlers/campaign_materials.go:82`
- `handlers/distribution_rules.go:98`
- `handlers/pricing.go:35`
- `handlers/reports.go:80`
- `handlers/materials.go:46`
- `handlers/client_target_pmm.go:33`
- `handlers/detections.go:163`, `:204`, `:251`, `:661`, `:711`

### 6.3 Repos

Nos filtros alimentados por escopo de viewer, `ClientID *uuid.UUID` vira
`ClientIDs []uuid.UUID` (`catalog/detections.go:628,646,960`,
`catalog/insights.go`, `catalog/campaigns.go`, live-map). O SQL troca:

```sql
AND ($7::uuid   IS NULL OR cmp.client_id = $7)        -- hoje
AND ($7::uuid[] IS NULL OR cmp.client_id = ANY($7))   -- depois
```

Array vazio **não** é NULL: `= ANY('{}')` é falso, ou seja, falha fechada — que é o
comportamento desejado. Admin continua passando `nil` e não filtra nada.

`catalog/management_overview.go` fica **fora**: o `/management-overview` é
admin-only (router linha 281, subgrupo B) e seu `client_id` é o filtro que o
próprio admin escolhe, não escopo de viewer.

Queries com `client_id = $1` posicional e sem o guard de NULL
(`catalog/campaigns.go:206,210`) recebem o mesmo tratamento de array.

### 6.4 Repo de usuários

- `users.User` ganha `ClientIDs []uuid.UUID \`json:"client_ids"\``, populado por
  `array_agg` num LEFT JOIN LATERAL — sem N+1 em `Get`, `GetByEmail` e `List`.
- Novo `users.Repo.SetClients(ctx, userID, []uuid.UUID) error`: numa transação,
  insere os que faltam, remove os que saíram e ajusta `users.client_id` conforme
  a regra do principal (§5.3).
- `users.ListInput.ClientID` passa a filtrar por vínculo
  (`EXISTS (SELECT 1 FROM user_clients …)`), não só pelo principal.
- `catalog.Clients.CountDependents` conta usuários via `user_clients` — senão o 409
  de delete de cliente deixaria de proteger vínculos secundários.

### 6.5 Login

`handlers/auth.go:93` hoje bloqueia o login quando o cliente do usuário está
desativado. Nova regra:

- Busca os clientes vinculados **ativos**.
- Nenhum ativo → `client_disabled` (com 1 cliente, comportamento idêntico ao atual).
- ≥1 ativo → entra, e **o JWT carrega apenas os ativos**.

Desativar um cliente da agência o remove da visão no próximo login. O token vivo
continua valendo até expirar — mesma semântica de hoje.

`loginUser` no response ganha `client_ids` e mantém `client_id`.

### 6.6 API de usuários (`/admin/users`)

`createUserPayload` e `updateUserPayload` ganham `client_ids []uuid.UUID`,
mantendo `client_id` aceito como equivalente a uma lista de um elemento (o
frontend antigo continua funcionando durante a janela de deploy). Quando os dois
vierem, `client_ids` vence.

Validação: role "client" exige `client_ids` não-vazio; role "admin" exige vazio.

## 7. Frontend

Regra única, aplicada onde hoje existe `isAdmin ? <select Cliente> : <chip travado>`:

```js
const canPickClient = isAdmin || clientOpts.length > 1
```

Como `GET /clients` é scope-aware, o select do admin e o do usuário-agência são o
mesmo componente com a mesma fonte de dados.

**Telas tocadas:**

- `components/insights/FiltersBar.jsx:182` (/insights) — e o `useEffect:171` que
  trava `clientId` no próprio cliente passa a travar só quando há 1 cliente.
- `pages/LiveMapPage.jsx:124` — o `clientId` do viewer deixa de vir de
  `user.client_id` e passa a ser a seleção do filtro (`null` = todos), que também
  alimenta o chip de nome/logo do cabeçalho.

`/management` **não** entra: é admin-only (`App.jsx:147`, `RequireRole ['admin']`).

**Telas que não precisam de select novo:** `/campaigns`, `/detections`,
`/materials` e `/reports/airtime` já renderizam o nome do cliente por campanha e
recebem do backend o conjunto certo. `AirtimeReportPage.jsx:147` já trata
explicitamente o caso de múltiplos clientes.

**Sessão:** `AuthContext` expõe `clientIds` ao lado de `clientId` (principal).

**/admin/users:** `UserFormModal.jsx:244` troca o `RSelect` por multi-select; a
lista mostra `Cliente A +2`.

## 8. Casos de borda

| Situação | Decisão |
|---|---|
| 1 de 2 clientes desativado | Login entra; JWT só com os ativos (§6.5) |
| Deletar cliente com usuário vinculado | 409 via `CountDependents` contando `user_clients` |
| Página de boas-vindas | Usa o cliente principal — sem mudança |
| Pós-venda | Continua por cliente, link público por token; a agência recebe um email por cliente |
| API keys externas `/v1/*` | Escopadas por cliente, não por usuário — inalterado |
| Admin/operator | `client_ids` sempre vazio; CHECK 0027 continua barrando |
| KPIs somados no /insights | Impactos e investido somam; CPM vira média ponderada (correta). PMM segue resolvido por `(campanha.client_id, station)` |

## 9. Testes

- **Escopo:** `nil` (admin), vazio (fail-closed), múltiplo, e **token legado só com
  `client_id`**.
- **Paridade:** com 1 cliente vinculado, cada repo devolve exatamente o que devolve
  hoje. É o teste que protege a regressão silenciosa.
- **Authz de rota:** usuário com A+B lê campanha de A e de B; 404 em C.
- **Login:** 1 de 2 clientes desativado entra com escopo reduzido; 2 de 2
  desativados leva `client_disabled`.
- **Migration:** backfill idempotente, trigger da invariante, RESTRICT no delete de
  cliente.

## 10. Rollout

A migration é `INSERT … SELECT` sobre dado real: vale a regra 4.8 do CLAUDE.md
(testar contra cópia de prod). O `shadow_migration_test` do `deploy.sh` roda de
qualquer forma e aborta o deploy se falhar.

Ordem, pela regra do split de deploy: **backend primeiro** (`deploy.sh` na VM),
smoke test, **frontend depois**. O claim de compatibilidade (§6.1) e o
`client_id` aceito na API de usuários (§6.6) garantem que o frontend antigo
continua funcionando contra o backend novo durante a janela.

Documentação a atualizar quando implementado: `docs/features/user-management.md`.

## 11. Fora de escopo

Entidade "Agência", relatório agregando múltiplos clientes, seletor global
persistente de cliente, e cliente com permissão de escrita.
