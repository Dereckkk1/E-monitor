---
status: implementado
ultima-verificacao: 2026-08-26
codigo-relacionado:
  - migrations/0068_hub_sso.up.sql
  - migrations/0068_hub_sso.down.sql
  - workers/internal/hub/hub.go
  - workers/internal/api/handlers/hubsso.go
  - workers/internal/api/handlers/hubsso_test.go
  - workers/internal/users/users.go
  - workers/internal/config/config.go
  - workers/internal/api/router.go
  - workers/cmd/api/main.go
  - frontend/src/pages/HubSsoPage.jsx
  - frontend/e2e/hub-sso.spec.js
  - frontend/playwright.config.js
  - frontend/src/pages/HubSsoPage.css
  - frontend/src/App.jsx
  - frontend/src/api/client.js
---

# Entrada pela Central de Clientes (E-Hub) — `POST /v1/internal/auth/sso`

Segunda porta de entrada no E-monitor, **pública**. Quem tem conta na Central de
Clientes (`clientes.emidiastec.com.br`) clica no card do E-monitor e chega aqui já
logado, sem digitar senha de novo.

**Não substitui o login local.** As duas portas coexistem para sempre — é a
decisão D5 do RFC-001, e existe para que o E-monitor nunca fique dependente de
outro sistema para alguém conseguir entrar.

> Esta feature nasce fora do `plano_implementacao.md`: ela vem do **RFC-001 do
> E-Hub**, em `E-Series/hub/docs/RFC-001-hub-emidiastec.md` (§7 o fluxo, §8.1 o
> que muda aqui). O plano do E-monitor continua sendo a fonte de verdade
> arquitetural *deste* sistema; o que o RFC define é o contrato entre os dois.

## O fluxo, em uma passada

```
Navegador (logado no hub)        E-Hub API                E-monitor
     │ clique no card               │                          │
     │ POST /api/sso/start ────────>│  gera código one-time     │
     │ <──────────── redirectUrl ───│  (60s, Redis)             │
     │ window.location = .../sso?code=hs_…                      │
     │────────────────────────────────────────────────────────> │
     │                              │   POST /v1/internal/auth/sso
     │                              │<───── troca o código ─────│
     │                              │  (X-Hub-Platform-Key)     │
     │                              │────── dados do usuário ──>│
     │ <──── token de 8h + user, no MESMO envelope do /auth/login│
```

Depois do último passo **o hub sai do caminho**: a sessão é o JWT de 8h de
sempre, e nenhuma tela do E-monitor passa a depender do hub em runtime.

## Por que o código na URL não é vazamento

Ele é opaco — 32 bytes aleatórios, sem nenhum dado do usuário dentro —, vive
**60 segundos**, e só quem tem a chave desta plataforma consegue trocá-lo. O
primeiro uso o consome no hub (`GETDEL`), então nem duas abas competindo trocam
duas vezes.

## O que muda no banco

Migration `0068`: `users.hub_id` e `clients.hub_id`, ambas `TEXT` nullable, com
índice **único parcial** (`WHERE hub_id IS NOT NULL`).

`hub_id` presente = registro **vinculado**: a identidade (nome, e-mail,
ativo/inativo) é do hub. `role` e escopo de cliente continuam sendo daqui — o
hub nunca os toca (decisão D9).

> **Por que a 0068 não cai na regra 4.8 do CLAUDE.md** (migration validada só em
> DB local dá falso verde): as colunas são criadas na própria migration, e os
> índices são parciais. Toda linha existente nasce com `hub_id` NULL e fica
> **fora** do índice — ele é literalmente vazio no instante em que é criado, com
> 0 ou 10 milhões de linhas na tabela. Não existe dado de produção capaz de
> fazer isto colidir. Verificado num Postgres real com as 66 migrations
> aplicadas: dois clientes sem `hub_id` convivem, e `hub_id` repetido é barrado.

## Provisionamento no primeiro acesso (JIT)

| Payload do hub | Vira aqui |
|---|---|
| `level: "internal"` | `role = 'admin'`, sem `client_id` |
| `level: "client"` | `role = 'viewer'` + `client_id` resolvido por `clients.hub_id` |

Ordem de resolução do usuário: **1)** por `hub_id`; **2)** por e-mail, e aí
**vincula** (não cria uma segunda conta para a mesma pessoa); **3)** cria.

**`provisionProfile` é ignorado de propósito.** No E-rádios ele importa porque lá
um cliente pode ser `advertiser` ou `agency`. Aqui todo usuário de cliente é
`viewer` — não há segunda opção, então ler o campo só criaria uma porta para a
configuração de um cliente no hub virar `role` aqui dentro, que é o oposto da
D9. Há teste travando isso.

### `client_not_provisioned` — o erro que o time vai ver

Se o cliente existe no hub mas **ninguém preencheu `clients.hub_id`** aqui, o SSO
recusa com `403 client_not_provisioned` e **não cria nada**.

Isso é deliberado. O `clients` do E-monitor carrega cadastro de verdade —
contrato, PMM alvo, regras de distribuição —, e adivinhar isso a partir de um
nome geraria um cliente fantasma que alguém teria que limpar depois. Um usuário
`viewer` sem cliente seria pior ainda: logaria sem escopo nenhum.

Quem preenche `clients.hub_id` automaticamente é o evento `client.upsert` do sync
(**Fase 3** do RFC, ainda não implementada). Até lá o vínculo é manual:

```sql
UPDATE clients SET hub_id = '<ObjectId do cliente no hub>' WHERE id = '<uuid>';
```

A tela `/sso` traduz esse erro em português e diz que o passo depende do time,
não de quem clicou.

## Gates locais continuam mandando

O hub é dono da identidade, **não** da permissão de entrar aqui:

- usuário com `is_active = false` → `403 account_disabled`;
- cliente com `is_active = false` → `403 client_disabled`;
- vínculo órfão (cliente sumido) → recusa, **fail-closed**.

Conta criada agora nasce ativa, então esses gates só alcançam quem já existia.

## Tradução dos erros do hub

| Hub responde | E-monitor responde | Por quê |
|---|---|---|
| `410` código inválido/expirado/usado | `410` | Resolve clicando de novo na Central |
| `401` chave da plataforma recusada | **`503`** | É problema **nosso**, não de quem clicou. E um `401` numa rota de login faria o interceptor do frontend tentar renovar uma sessão que não existe |
| `403` sem entitlement | `403` | O produto não está no contrato do cliente |
| rede caiu / timeout (10s) | `502` | Distinguir de erro interno nosso |
| `HUB_URL`/`HUB_PLATFORM_KEY` ausentes | `503` | Integração desligada nesta instalação |

## Configuração

```
HUB_URL=https://api-clientes.emidiastec.com.br
HUB_PLATFORM_KEY=pk_…    # gerada no admin do hub, exibida UMA vez
```

**Opcionais**, e fora do `required` do `config.Load()` de propósito: ausentes, só
o endpoint de SSO responde 503 e o resto do sistema não muda. Tornar
obrigatórias faria a API inteira deixar de subir por causa de uma integração
opcional.

Rotacionar a chave no hub mantém a anterior válida por **24h** — a janela para
publicar a nova aqui sem derrubar o SSO.

## Testes

`workers/internal/api/handlers/hubsso_test.go` — **15 testes**, rodados contra um
Postgres real com as 66 migrations aplicadas (não contra DB vazio).

O hub é simulado por um `httptest.Server`, o que faz o `internal/hub` inteiro
rodar de verdade (headers, tradução de status, parsing). **O que isso não prova**
é que o hub real responde neste formato — para isso existe o roteiro de ponta a
ponta com os dois sistemas no ar, exigido pela §15 do RFC.

### A página, em navegador — 2026-08-26

`frontend/e2e/hub-sso.spec.js` — **4 testes**, Playwright, `npm run test:e2e`.

Existem porque a tela foi entregue com os 15 testes acima verdes e **não
funcionava em navegador nenhum**. Ninguém a tinha aberto. O defeito vivia
exatamente onde aqueles testes não chegam — no browser, **depois** da resposta
do backend:

> A 1ª passada do efeito marcava `jaTrocou` e disparava a troca; o React
> desmontava (StrictMode) e a limpeza fazia `cancelado = true`; a 2ª passada
> saltava a troca pela guarda `jaTrocou`, **antes** de criar um `cancelado`
> novo. Quando a troca resolvia, `if (cancelado) return` matava a única passada
> que fez trabalho: nem `login`, nem `navigate`. O backend respondia **200**, a
> sessão de 8h era emitida, e a pessoa ficava olhando o **spinner para sempre**.

A guarda `jaTrocou` e o `cancelado` se cancelavam. Quem garante troca única é o
`jaTrocou` — e só ele precisa garantir. **Não reintroduzir um `cancelado` de
limpeza nesta página**; há comentário no código dizendo o porquê, e o teste
falha se voltar. A mesma linha quebrava a `/sso` da Plura do mesmo jeito.

Os 4 testes falseiam a API com `page.route` de propósito — o que está sendo
provado é a **tela**, e amarrar isso a Postgres + NATS + MinIO + Go faria um
teste que ninguém roda. Além do caminho feliz, eles travam os três recados que
alguém vai ver de verdade: sem código, 410, e `client_not_provisioned` — este
último porque não é erro de borda, é o caminho normal até a Fase 3 existir.
**Verificado reintroduzindo o defeito**: o caminho feliz trava e o teste falha.

> **Playwright entrou aqui, e só ele.** O `frontend/` não tinha framework de
> teste nenhum, e a tela que precisava de prova era justamente uma que depende
> de rede e de navegação. `@playwright/test` é **uma** devDependency; o lockfile
> foi conferido pela regra 5.4 do `CLAUDE.md` (`emnapi` em 11 antes e depois,
> bindings linux em 21 antes e depois) e validado com um `npm ci` de verdade em
> diretório limpo. O `npm install` **podou** `@emnapi/core`, `@emnapi/runtime` e
> `yaml`, exatamente como a regra 5.2 avisa — os três foram devolvidos, e o
> diff do lockfile saiu **só com inserções**.

**Roteiro de clique completo, com TUDO no ar: 13 de 13** — hub em `:3010`,
frontend em `:5174`, API Go nativa em `:8080` contra Postgres, NATS e MinIO em
container com as 68 migrations aplicadas. Código real emitido pelo hub, trocado
pelo Go de verdade, sessão de admin de verdade: sai da `/sso`, cai em
`/stations` (o mesmo `HomeRedirect` do login local) e as chamadas
`/v1/internal/*` seguintes respondem 200.

> **Observação, não defeito da `/sso`:** a `/stations` acumula ~17
> `ERR_BLOCKED_BY_ORB` de logos de emissora hospedados no Google. Medido abrindo
> `/stations` com a sessão pronta, **sem passar pela `/sso`**: os mesmos 17.
> É da página de destino, e aconteceria igual entrando pelo login local.
