---
status: parcialmente-implementado
ultima-verificacao: 2026-09-01
codigo-relacionado:
  - workers/internal/api/handlers/hubsync.go
  - workers/internal/api/handlers/hubsync_upsert_test.go
  - workers/internal/auth/ativo.go
  - workers/internal/auth/middleware.go
  - workers/internal/hub/hub.go
  - workers/internal/api/router.go
---

# Sincronização vinda da Central de Clientes (`POST /v1/internal/hub/sync`)

> ## ✅ EM PRODUÇÃO desde 2026-09-01 — os quatro eventos implementados
>
> `user.deactivate` (fatia 1), `client.upsert` e `user.upsert` (fatia 2) e
> `user.password_changed` (fatia 3). Nenhum cai mais no ramo `default`.
>
> **Provado em produção neste dia, não só em teste:** desativar uma pessoa no
> `/admin/usuarios` do hub gravou `is_active = false` na tabela `users` daqui, e
> reativar trouxe de volta para `true`. Foi a primeira vez que os dois sistemas
> conversaram — até então todos os testes dos dois lados eram de um lado só, com
> o outro falseado.
>
> ⚠️ **A regra que o `default` carrega continua valendo para o PRÓXIMO evento.**
> Enquanto um evento conhecido responder ok sem agir, o hub marca como
> sincronizado algo que não foi. Aceitar em silêncio é a escolha certa para
> evento que **não se conhece**, e a errada para evento que se conhece e não se
> implementou. Ao acrescentar um evento novo ao §9.3 do RFC: ou se implementa o
> `case`, ou se aceita conscientemente que o hub vai mentir sobre ele.
>
> ⚠️ **`{"ok":true}` daqui NÃO significa que alguma linha mudou.** Os quatro
> handlers casam por `WHERE hub_id = $1`; sem linha casada eles respondem **200
> com `externalId` nulo**, de propósito — o provisionamento é JIT e a pessoa pode
> não ter clicado no card ainda. Quem lê só o status da fila no hub tem um falso
> positivo; o sinal certo é o `externalId`. Hoje há **3** usuários com `hub_id`
> aqui.

## O que resolve

A Central de Clientes (E-Hub) é dona da **identidade**: quem é a pessoa, e-mail,
nome, ativo/inativo, a que cliente pertence. O E-monitor continua dono da
**autorização** — `role` e escopo de cliente —, que o hub nunca toca (decisão D9
do RFC-001).

Até este endpoint existir, essa posse era só teoria: desativar alguém no hub
**não cortava o acesso aqui**. Era preciso desativar nos dois lugares, e nada
denunciava quem esquecesse do segundo.

## O sentido oposto do SSO

| | quem chama | quem responde | credencial |
|---|---|---|---|
| `POST /v1/internal/auth/sso` | E-monitor | hub | `HUB_PLATFORM_KEY` |
| `POST /v1/internal/hub/sync` | **hub** | **E-monitor** | `HUB_PLATFORM_KEY` |

**A mesma chave nas duas direções, e isso é decisão.** Uma chave separada
exigiria variável de ambiente nova — e variável nova neste repositório significa
lembrar do bloco `environment:` explícito do `docker-compose.yml`, porque o
`--env-file` do `deploy.sh` só interpola `${VAR}` dentro do compose e **não
injeta nada no processo**. Foi exatamente esse esquecimento que fez o SSO
responder **503** em produção com o `.env` da VM aparentemente certo, e a
correção virou o PR #8. Reusar `HUB_PLATFORM_KEY` remove a classe inteira do
problema: se o SSO funciona, o sync tem credencial.

A comparação usa `subtle.ConstantTimeCompare` (`hub.ChaveConfere`). Ela **é** a
autenticação — acontece antes de qualquer outra coisa —, e `==` em string vaza o
prefixo comum pelo tempo de resposta.

⚠️ **A chave é conferida ANTES de ler o corpo.** Responder `400` a quem não se
autenticou contaria a quem sonda que o endpoint existe e o que ele espera.

## `user.deactivate`

```jsonc
{
  "eventId": "uuid",
  "event": "user.deactivate",
  "occurredAt": "2026-09-01T12:00:00Z",
  "data": { "hubUserId": "...", "email": "..." }
}
```

→ `UPDATE users SET is_active = FALSE WHERE hub_id = $1 AND deleted_at IS NULL`

Resposta: `{"ok":true,"externalId":"<uuid local>"}`. O `externalId` é o que o hub
grava em `userPlatformIdentities` (§5.6) — é assim que ele descobre o id desta
pessoa aqui dentro, coisa que o SSO deliberadamente não faz.

**O casamento é por `hub_id` e SÓ por `hub_id`.** Cair para o e-mail seria
perigoso de um jeito que não aparece em teste: dois sistemas com o mesmo endereço
em pessoas diferentes existem, e desativar a errada por heurística é pior que não
desativar ninguém. O e-mail vem no payload para diagnóstico, não para busca.

`hub_id` que não casa com ninguém devolve **200 com `externalId` nulo**, não erro:
o provisionamento é JIT, então a pessoa pode simplesmente ainda não ter clicado
no card do hub. Não há o que desativar, e insistir não faria aparecer — 500 aqui
mandaria o hub para oito tentativas e a DLQ por um evento sem defeito.

**500 é reservado a falha de banco**, que é o único caso que melhora sozinho e
portanto o único que merece o retry do hub.

### Idempotência — vale para os três eventos

Os três são idempotentes **por natureza**: repetir qualquer um deles é repetir um
`UPDATE` que já não muda nada. É o que o §9.2 aceita ("ignorar repetido é
aceitável").

**Não há tabela de dedup por `eventId`**, e ela só passa a fazer falta com um
evento que não se baste sozinho. O candidato é o `user.password_changed` da fatia
3: reprocessar uma troca de senha antiga sobrescreveria uma mais nova. Hoje a
tabela seria uma migration sem uso.

### Eventos desconhecidos são aceitos

Só `user.password_changed` cai neste ramo hoje. Ele responde `{"ok":true}` e não
faz nada.

Responder erro faria o hub tentar oito vezes e enterrar na DLQ um evento que não
tem defeito nenhum — só chegou antes do código que o entende. Aceitar é o que
permite hub e plataforma subirem em ordens diferentes, que é a única forma
realista de evoluir quatro repositórios.

⚠️ Repetindo o aviso do topo porque é o ponto em que isso vira defeito: **ao
implementar a fatia 3, tire o `user.password_changed` daqui**. Um evento
conhecido, não implementado e respondendo ok faz o hub marcar a senha como
sincronizada sem que tenha sido.

## `client.upsert`

```jsonc
{ "hubClientId": "...", "name": "...", "cnpj": "...", "logoUrl": "...",
  "contactName": "...", "phone": "...", "city": "...", "state": "...", "active": true }
```

A escada, nesta ordem: **`hub_id`** (chave forte) → **CNPJ igual e sem vínculo**
(carimba o `hub_id` no registro que já existe) → **cria** com o mínimo.

**Aqui o E-monitor CRIA cliente, e no SSO ele não cria.** A diferença é
deliberada. O `criarPorJit` recusa inventar um tenant porque lá a informação
chega no meio do login de alguém, como efeito colateral de um clique — adivinhar
ali geraria cliente fantasma que ninguém pediu. Este evento é o oposto: alguém
habilitou o produto para aquele cliente no admin do hub, de propósito. O §9.3
manda "criar com defaults mínimos se não existir", e é barato — `clients` só
exige `name`. Contrato, PMM alvo e regras de distribuição seguem vazios e seguem
sendo preenchidos por aqui, como sempre foram.

**É isto que destrava o `client_not_provisioned`** do §8.1 — o erro que barra
todo usuário de cliente cuja empresa não tem `hub_id` carimbado.

⚠️ **Casar por NOME ficou de fora, e é regra, não esquecimento.** A §9.5 do RFC
registra que o importador casava por `cnpj || nome`, e um cliente renomeado no
E-monitor virava um cliente **novo** no hub, em silêncio. Nome é rótulo; CNPJ é
identidade. Prefere-se um registro a mais, visível, a um vínculo errado — e há
teste (`TestHubSync_ClientUpsert_NaoCasaPorNome`) que falha se alguém "melhorar"
isso.

## `user.upsert`

```jsonc
{ "hubUserId": "...", "email": "...", "name": "...", "phone": "...",
  "level": "client", "active": true, "client": {...} | null, "provisionProfile": {} }
```

Atualiza `name`, `phone` e `is_active`. A escada: **`hub_id`** → **e-mail igual
com `hub_id IS NULL`** (vincula em vez de duplicar) → **nada**.

⚠️ **Não cria conta**, e essa é a única divergência consciente da leitura literal
do §9.3 ("criar/atualizar"):

- A conta na plataforma nasce no **primeiro clique** (JIT). Não é detalhe de
  implementação — é o modelo mental do §8.1 e do handoff, e o `criarPorJit` já
  cria com este mesmo payload no momento em que a pessoa aparece.
- Criar aqui povoaria o `users` com dezenas de contas de gente que talvez nunca
  clique, e as telas de operação listam usuários.
- Acrescentar a criação depois é uma linha. Apagar contas criadas por engano em
  produção não é.

O degrau do e-mail exige **`hub_id IS NULL`**. Sem essa guarda, um evento
reapontaria para outra pessoa a identidade de uma conta já vinculada — há teste
(`TestHubSync_UserUpsert_NaoRoubaVinculoDeOutroHubId`).

`provisionProfile` chega no payload e é **ignorado** aqui, pela mesma razão que o
`hubsso.go` já documenta: no E-monitor todo usuário de cliente é `viewer`, não há
escolha a fazer. Ele existe no contrato porque o E-rádios precisa dele.

## A janela de 8 horas, e por que ela precisou ser fechada junto

`RequireJWT` valida **assinatura e expiração**, e mais nada — não consulta o
banco. Com o token valendo 8h (`internal/auth/jwt.go`), marcar `is_active = false`
barrava o **próximo login** e não tocava em quem já estava dentro.

Isso deixava **dois** controles sem efeito prático:

1. O `user.deactivate` acima.
2. O botão "bloquear usuário" do `/admin/monitoring` — cujo próprio comentário
   registrava a lacuna e a chamava de follow-up *"JWT revogação imediata"*.

Um controle de segurança com desvio silencioso é pior que a ausência dele: quem
clica acredita que cortou.

`auth.VerificadorAtivo` confere `is_active AND deleted_at IS NULL` com **cache em
processo por usuário, TTL de 60s**, instalado por `RequireJWTAtivo`. A janela cai
de 8h para 1 minuto ao custo de ~1 consulta por usuário por minuto. A diferença
entre 0s e 60s não muda nenhuma decisão de operação; entre 60s e 8h muda todas.

**Falha de banco LIBERA, de propósito**, e o resultado da falha não vai para o
cache. Postgres fora do ar já é uma indisponibilidade; bloquear aqui a
transformaria em total, deslogando todo mundo no exato momento em que ninguém
consegue investigar. Quando o banco volta, a checagem volta na primeira
requisição, não daqui a um minuto.

> **Descartada:** deny-list em Redis, que espelharia o `iatfloor` do hub. O Go
> deste repositório **não usa Redis** — não está no `go.mod`, aparece só como
> sonda de health em `system_health.go`. Seria dependência nova no caminho mais
> quente do sistema.

`RequireJWTAtivo(nil)` devolve o `RequireJWT` puro. É o mesmo "nil desliga a
peça" que o router já usa para `HubSSO`, `Metrics` e `BlockList`, e é o que
permite os testes de rota montarem o router sem banco.

## Configuração

Nenhuma variável nova. `HUB_URL` e `HUB_PLATFORM_KEY` já existem para o SSO, já
estão no bloco `environment:` do compose desde o PR #8, e são opcionais: ausentes,
a rota não é registrada e a API sobe igual.

⚠️ **Do lado do hub há um pré-requisito operacional.** As entregas precisam
apresentar a chave, e o hub guardava apenas o hash dela. A partir da Fase 3 ele
guarda também uma cópia cifrada (`platformKeySealed`) — mas só a partir da
**próxima rotação**. Para o E-monitor, cuja chave foi gerada antes disso, é
preciso **rotacionar a chave no `/admin/catalogo` do hub** antes que qualquer
evento seja entregue. A rotação é segura: o §7.3 mantém a chave anterior válida
por 24h, tempo de atualizar o `.env` daqui.

## Como verificar em produção

```bash
# 401 = a porta existe e recusa quem não tem a chave
curl -s -o /dev/null -w "%{http_code}\n" -X POST \
  https://api.e-monitor.online/v1/internal/hub/sync \
  -H "Content-Type: application/json" -d '{"eventId":"x","event":"user.upsert","data":{}}'
```

| resposta | significa |
|---|---|
| **404** | o código não subiu |
| **401** | ✅ subiu — sem a chave, ninguém entra |
| **200** | ⚠️ passou sem chave: investigar `hub.Configured()` |

A prova que vale é a de ponta: desativar alguém no `/admin/usuarios` do hub e ver
`is_active` virar `false` aqui, e a sessão dela cair em ≤60s. Suíte verde não
prova isto — o §8 do postmortem do go-live é sobre exatamente essa diferença.

### Foi feita em 2026-09-01, e o resultado está aqui

`401` nas duas rotas (`/hub/sync` e `/hub/users`), e a prova de ponta nos dois
sentidos com `marketing@hubradios.com`:

```
antes    marketing@hubradios.com | t   ← estado inicial
desativa no /admin/usuarios do hub
depois   marketing@hubradios.com | f   ← gravado por user.deactivate
reativa no /admin/usuarios do hub
depois   marketing@hubradios.com | t   ← gravado por user.upsert
```

Fila do hub em `delivered`, `dead: 0`, `identidadesEmErro: 0`. A consulta:

```bash
COMPOSE="docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env"
$COMPOSE exec -T postgres psql -U radiocheck -d radiocheck -c \
  "SELECT email, is_active, hub_id FROM users WHERE hub_id IS NOT NULL AND deleted_at IS NULL ORDER BY email;"
```

O `-f` do override não é opcional (regra 4.7 do `CLAUDE.md`): sem ele o comando
roda contra uma configuração que não é a real.

> ⚠️ **Um furo que só apareceu depois, e não está fechado.** Trocar a senha no
> hub **não derruba a sessão viva** daqui. A conferência que a fatia 1
> acrescentou ao `RequireJWT` olha `is_active`, não troca de senha — e o token
> vale 8h. Desativar corta em ≤60s; trocar a senha corta só o próximo login.
> **Para cortar o acesso de alguém comprometido: desative, não troque a senha.**

## Testes

`workers/internal/api/handlers/hubsync_test.go` (porta e comportamento) e
`workers/internal/auth/ativo_test.go` (a mecânica do cache, incluindo a expiração,
escrita direto na estrutura para não depender de um minuto de relógio).

⚠️ Os testes que dependem de banco **pulam** sem `TEST_DATABASE_URL`. Pela regra
4.8, rodá-los contra Postgres real com as migrations aplicadas é obrigatório antes
de qualquer deploy — banco vazio dá falso verde.
