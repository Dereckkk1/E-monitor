---
status: parcialmente-implementado
ultima-verificacao: 2026-09-01
codigo-relacionado:
  - workers/internal/api/handlers/hubsync.go
  - workers/internal/auth/ativo.go
  - workers/internal/auth/middleware.go
  - workers/internal/hub/hub.go
  - workers/internal/api/router.go
---

# Sincronização vinda da Central de Clientes (`POST /v1/internal/hub/sync`)

> **`parcialmente-implementado` porque só um evento existe.** O receptor aceita
> os quatro tipos do RFC-001 §9.3, mas apenas `user.deactivate` faz alguma coisa.
> Os outros respondem `{"ok":true}` e são ignorados de propósito — ver
> [Eventos desconhecidos](#eventos-desconhecidos-sao-aceitos).

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

### Idempotência

A operação é idempotente por natureza: repetir o `UPDATE` não muda nada. É o que
o §9.2 aceita ("ignorar repetido é aceitável"). **Não há tabela de dedup por
`eventId`** — ela entra quando chegar o primeiro evento que não se basta sozinho
(o `user.upsert`); hoje seria uma migration sem uso.

### Eventos desconhecidos são aceitos

`user.upsert`, `client.upsert` e `user.password_changed` respondem `{"ok":true}` e
não fazem nada. Responder erro faria o hub tentar oito vezes e enterrar na DLQ um
evento que não tem defeito nenhum — só chegou antes do código que o entende.
Aceitar é o que permite hub e plataforma subirem em ordens diferentes, que é a
única forma realista de evoluir quatro repositórios.

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

## Testes

`workers/internal/api/handlers/hubsync_test.go` (porta e comportamento) e
`workers/internal/auth/ativo_test.go` (a mecânica do cache, incluindo a expiração,
escrita direto na estrutura para não depender de um minuto de relógio).

⚠️ Os testes que dependem de banco **pulam** sem `TEST_DATABASE_URL`. Pela regra
4.8, rodá-los contra Postgres real com as migrations aplicadas é obrigatório antes
de qualquer deploy — banco vazio dá falso verde.
