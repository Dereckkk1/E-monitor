---
status: implementado
ultima-verificacao: 2026-08-04
codigo-relacionado:
  - migrations/0062_user_clients.up.sql
  - workers/internal/auth/scope.go
  - workers/internal/auth/jwt.go
  - workers/internal/users/users.go
  - workers/internal/api/handlers/auth.go
  - workers/internal/api/handlers/users.go
  - workers/internal/api/handlers/insights.go
  - workers/internal/catalog/clients.go
  - frontend/src/contexts/AuthContext.jsx
  - frontend/src/components/insights/FiltersBar.jsx
  - frontend/src/pages/LiveMapPage.jsx
  - frontend/src/components/UserFormModal.jsx
---

# Usuário vinculado a múltiplos clientes (agências)

Uma agência manda vários clientes para a operação e precisa de **um login só**
que enxergue todos eles. Antes desta feature, `users.client_id` era uma coluna
única: cada login via exatamente um cliente, e atender uma agência exigia criar
uma conta por cliente.

Agora um usuário tem uma **carteira** de clientes. Quem tem um cliente só
continua funcionando exatamente como antes — nenhuma tela muda, nenhum seletor
novo aparece.

## Modelo

```
users                      user_clients                 clients
  id                         user_id  ──────────────►     id
  client_id ────────────►    client_id ─────────────►     name
  (o "principal")            PRIMARY KEY (user_id, client_id)
```

`users.client_id` **continua existindo** e passa a significar **cliente
principal**. Isso mantém intacta a CHECK `users_client_role_consistency` da
migration 0027 (`role='viewer'` ⇒ `client_id NOT NULL`) e todo o código que já
lia a coluna. O principal aparece no cabeçalho da página de boas-vindas e como
rótulo curto; não é um privilégio, só um desempate.

A tabela `user_clients` (migration 0062) é a carteira. `ON DELETE RESTRICT` no
cliente é o que sustenta o 409 `client_has_dependents` ao tentar deletar um
cliente que ainda tem login vinculado — inclusive como **secundário**.

### A invariante e o trigger

`users.client_id` está sempre dentro de `user_clients`. Garantido pelo trigger
`trg_sync_user_primary_client`, que insere a linha correspondente a cada
`INSERT` ou `UPDATE OF client_id` em `users`.

**O trigger só ADICIONA.** Remover vínculo é sempre explícito. Isso é
deliberado: qualquer caminho legado que escreva `users.client_id` (fluxo de
boas-vindas, testes, reparo manual em prod) fica consistente sozinho em vez de
quebrar. Mas tem uma consequência que já causou bug — ver "Poda" abaixo.

### Quem escreve a carteira

| Operação | Efeito na carteira |
|---|---|
| `users.Repo.SetClients(userID, ids)` | redefine a carteira inteira, numa transação. Lista vazia é rejeitada |
| `users.Repo.Update` com `ClientID` | carteira vira **exatamente aquele cliente** (poda o resto) |
| `users.Repo.Update` com `ClearClient` | esvazia a carteira (o usuário virou admin) |

**Poda — leia antes de mexer.** Como o trigger só adiciona, reatribuir um viewer
do cliente A para o B mandando só `client_id` deixaria A na carteira. E a
carteira **é o escopo de leitura**: o usuário removido de A continuaria vendo os
dados de A. Vazamento de acesso, não sujeira cosmética. Por isso o `Update` poda
dentro da própria transação sempre que toca `client_id`.

Semântica resultante: **`client_id` sozinho significa "este usuário tem
exatamente este cliente"**; carteira com N clientes se edita por `SetClients`.

Corolário para quem for mexer no handler: **não misture as duas coisas na mesma
requisição**. Setar `in.ClientID` e depois chamar `SetClients` faz o `Update`
truncar a carteira antes de o `SetClients` reconstruí-la — e se o segundo passo
falhar, a carteira fica destruída. O handler só seta `in.ClientID` a partir de
`client_ids` na promoção admin→cliente, quando o usuário ainda não tem principal
e o CHECK da 0027 exige gravar role e client_id juntos.

## Escopo de leitura

Todo o isolamento multi-tenant passa por dois helpers em
[workers/internal/auth/scope.go](../../workers/internal/auth/scope.go):

| Helper | Uso | Contrato |
|---|---|---|
| `ClientScopesFromContext(ctx) []uuid.UUID` | filtro de lista que desce pro repo | `nil` = sem escopo (admin/operator vê tudo) |
| `ScopeAllows(ctx, clientID) bool` | check pontual 404 anti-oracle | `true` sem escopo; pertinência caso contrário |

Regras que não podem regredir:

- **Um viewer nunca sai sem escopo.** Token mal-formado devolve
  `[]uuid.UUID{uuid.Nil}` — sentinela que não casa com nenhum cliente real, então
  toda query escopada dá vazio em vez de dar tudo.
- **`ScopeAllows` nega `uuid.Nil` sempre**, inclusive para admin. Nenhum cliente
  real tem id zero (`clients.id` é `uuid_generate_v4()`), então o zero só chega
  ali por bug ou sondagem — e ele casaria com a sentinela acima.
- **A carteira sai copiada.** O slice desce como argumento de query pro pgx em 19
  call sites; um caller que ordenasse in-place corromperia as claims do request.

No SQL, o filtro é sempre:

```sql
AND ($N::uuid[] IS NULL OR cmp.client_id = ANY($N))
```

`nil` → NULL → sem filtro (admin). Slice vazio → `'{}'` → `= ANY('{}')` é falso →
**nenhuma linha**. Falha fechada. Em Go, teste `slice == nil`, **nunca**
`len(slice) == 0` — os dois casos significam o oposto um do outro.

### Compatibilidade de token

O JWT ganhou o claim `client_ids` e **manteve** `client_id`. Um token emitido
antes do deploy traz só `client_id` e vale por até 8h; `ClientScopesFromContext`
o traduz numa carteira de um elemento. Sem isso, o deploy do backend deslogaria
todo cliente com sessão viva. A remoção dessa tradução está rastreada em
**F-126** ([follow-ups-fase2.md](../roadmap/follow-ups-fase2.md)).

## Login e cliente desativado

Desativar um cliente bloqueia o login de todos os seus usuários (a empresa foi
suspensa, não cada conta). Com carteira, a regra é **"pelo menos um cliente
ativo"**:

- nenhum ativo → 403 `client_disabled` (com 1 cliente, idêntico ao anterior);
- ≥1 ativo → entra, e **o token carrega só os ativos**;
- se o próprio principal foi desativado, ele é reposicionado para o vínculo mais
  antigo ainda ativo — a sessão não pode ficar rotulada com um cliente que o
  usuário não enxerga mais.

O reposicionamento é **só da sessão**: não gravamos `users.client_id`. Reativar o
cliente original o devolve como principal no próximo login, e o banco continua
sendo a fonte de verdade do operador.

**A desativação vale a partir do próximo login.** Um token emitido antes segue
válido até expirar (8h) — mesmo comportamento de antes da feature.

## `/insights` é por cliente

`GET /insights` exige `client_id` **mesmo do admin**, e
`catalog.InsightsParams.ClientID` é um `uuid.UUID`, não lista. A tela inteira
(PMM no target, CPM, investido, valor consolidado) só tem significado comercial
dentro de um cliente — somar valor consolidado de clientes diferentes não
significa nada.

Então o usuário-agência **escolhe um cliente**, exatamente como o admin faz:

| Quem | Regra |
|---|---|
| admin | `client_id` obrigatório na query (inalterado) |
| carteira de 1 | `client_id` **forçado** pelo JWT, query ignorada (inalterado) |
| carteira de N | `client_id` obrigatório (400 se ausente) e tem que estar na carteira (403 se não) |

As demais telas (`/campaigns`, `/detections`, `/materials`, `/live-map`) agregam
a carteira inteira por padrão.

## Frontend

Uma regra só, onde antes havia `isAdmin ? <select Cliente> : <chip travado>`:

```js
const canPickClient = isAdmin || clientOpts.length > 1
```

`GET /clients` é scope-aware — devolve ao cliente a própria carteira —, então o
select do admin e o do usuário-agência são o mesmo componente com a mesma fonte
de dados. Telas com seletor: `/insights` e `/live-map`. `/management` é
admin-only e ficou de fora.

`AuthContext` expõe `clientIds` ao lado de `clientId`. No `/admin/users`, o
campo de cliente virou multi-select; a lista mostra `Cliente A +2`.

## O que NÃO existe

- **Entidade "Agência".** O vínculo é direto usuário → N clientes. Se a agência
  tem 3 pessoas, cada uma recebe a própria carteira.
- **Relatório agregando clientes.** CSV, PDF e o export do `/insights` continuam
  por cliente (ou por campanha, que já pertence a um cliente).
- **Seletor global persistente.** O filtro é por tela, nas barras que já existem.
- **Cliente com permissão de escrita.** Continua read-only.
- **Multi-cliente na API externa.** A API key é escopada a um cliente; o
  `APIKeyViewerScope` monta uma carteira de exatamente um.

## Operação

Vincular clientes a um usuário: `/admin/users` → editar → campo "Clientes
vinculados". O primeiro da lista vira o principal; se o principal atual continuar
na lista, ele é mantido.

Diagnóstico de carteira em prod (read-only):

```sql
SELECT u.email, c.name, (c.id = u.client_id) AS principal, c.is_active
FROM user_clients uc
JOIN users u   ON u.id = uc.user_id
JOIN clients c ON c.id = uc.client_id
WHERE u.email = 'agencia@exemplo.com'
ORDER BY uc.created_at, uc.client_id;
```

Divergência entre `users.client_id` e `user_clients` (não deveria acontecer — o
trigger garante a invariante):

```sql
SELECT u.id, u.email, u.client_id
FROM users u
WHERE u.client_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM user_clients uc
    WHERE uc.user_id = u.id AND uc.client_id = u.client_id
  );
```
