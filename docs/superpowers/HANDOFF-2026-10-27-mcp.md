# Handoff — MCP do E-monitor (fim da Fatia 0, início da Fatia 1)

Data: 2026-10-27. Abra uma sessão nova do Claude Code em
`C:\Users\marke\Desktop\Programas\E-Series\E-monitor` e cole o bloco abaixo como primeira mensagem.

**Pré-requisito:** Docker Desktop aberto (os testes de integração usam um Postgres em container).

---

## Prompt para colar na nova sessão

```
Você vai continuar o servidor MCP do E-monitor. A Fatia 0 (fundação) está implementada, revisada
e verificada na branch feat/mcp-server. Seu trabalho agora é a Fatia 1 (tools de leitura).

═══════════════════════════════════════════════════════════════
REGRAS DO DONO (não negociáveis)
═══════════════════════════════════════════════════════════════
- Trabalhe SÓ na branch feat/mcp-server (confira com `git branch --show-current`).
  A branch não tem upstream de propósito.
- NADA vai para o master, e nada é pushado, até o MCP estar 100% pronto. O dono decide quando.
  (Push no master publica o frontend no Cloudflare na hora; o backend só sobe com deploy.sh.)
- Leia o CLAUDE.md da raiz antes de tudo: regras 4.8, 5 (nunca `npm install` no frontend) e 6.x.
- O E-monitor NÃO tem acesso seu à VM nem ao banco de prod (CLAUDE.md §7).

═══════════════════════════════════════════════════════════════
LEIA NESTA ORDEM
═══════════════════════════════════════════════════════════════
1. docs/superpowers/HANDOFF-2026-10-27-mcp.md  (este arquivo — seções abaixo do prompt)
2. docs/superpowers/specs/2026-10-27-mcp-server-design.md  (spec: decisões D1–D10, §4 fundação,
   §5 escrita em 3 níveis, §6 catálogo de tools por fatia, §7 fatias)
3. docs/features/mcp.md e docs/operations/mcp.md  (o que está implementado HOJE)
4. Contrato comum dos MCPs da E-Series, no repo do hub, branch origin/main:
   `git -C ../hub show origin/main:docs/architecture/mcp-ecossistema.md`  (§3 e §6)
5. O código: workers/internal/mcp/ (servidor.go, toolkit.go, tools_sistema.go, instrucoes.go,
   autenticacao.go, modulo.go) — é o padrão que as tools novas seguem.

═══════════════════════════════════════════════════════════════
O QUE FAZER
═══════════════════════════════════════════════════════════════
1. Use a skill superpowers:writing-plans para escrever o plano da Fatia 1 em
   docs/superpowers/plans/AAAA-MM-DD-mcp-fatia-1-leitura.md, a partir da spec §6 "Fatia 1"
   e das CONVENÇÕES PARA TOOLS NOVAS deste handoff. Antes de escrever, leia de verdade cada
   handler/serviço que uma tool vai reaproveitar (a spec lista a rota de origem de cada tool).
2. Mostre o plano ao dono e espere o OK.
3. Execute com superpowers:subagent-driven-development (lotes coerentes; revisão de aderência
   + qualidade por lote; revisão final da branch). Leia docs/subagentic-use/Subagents-usage.md.
4. Ao fim: atualize docs/features/mcp.md (tabela de tools), a memória do projeto e um handoff
   novo para a Fatia 2.
```

---

## Estado em 2026-10-27

**Branch `feat/mcp-server`**: 35 commits sobre `origin/master` (`0468cb9`), árvore limpa, nada pushado.

Entregue e verificado (Fatia 0):

| Peça | Onde |
|---|---|
| Migration 0070 (tabelas `mcp_tokens`, `mcp_oauth_clients`, `mcp_oauth_codes` + trigger em `users` que revoga credenciais ao trocar senha/desativar/rebaixar/apagar) | `migrations/0070_mcp_fundacao.*.sql` |
| Store de credenciais (só hash `SHA-256(JWT_SECRET:token)`) | `workers/internal/mcp/tokens/` |
| Servidor MCP stateless (go-sdk v1.8.0), tool `quem_sou_eu`, fail-closed sem identidade, recusa lote JSON-RPC | `workers/internal/mcp/servidor.go` |
| Autenticação do `/mcp` (401 do contrato, 240/min por credencial, auditoria de token recusado) | `workers/internal/mcp/autenticacao.go` |
| OAuth 2.1 próprio (DCR só público e só para callbacks exatos, PKCE, consentimento no portal, refresh com rotação; cliente confidencial do Gemini só via CLI) | `workers/internal/mcp/oauth/` |
| API admin de tokens pessoais | `workers/internal/mcp/tokens_http.go` |
| Montagem na API, config, CORS, compose | `modulo.go`, `internal/api/router.go`, `cmd/api/main.go`, `internal/config/config.go`, `infra/docker/docker-compose.yml` |
| CLIs `revoga-mcp` e `registra-cliente-mcp` (ambos no Dockerfile) | `workers/cmd/` |
| Frontend: `/autorizar-ia` (consentimento) e "Integração com IA" em `/account` | `frontend/src/pages/AuthorizeAiPage.jsx`, `frontend/src/components/account/McpIntegrationSection.jsx`, `frontend/public/_headers` |
| Docs | `docs/features/mcp.md`, `docs/operations/mcp.md`, linha no `CLAUDE.md` |

Verificação final feita: `go vet ./...`, `CGO_ENABLED=0 GOOS=linux go build ./...`, `go test -p 1`
em `internal/mcp/...`, `internal/users`, `internal/api`, `cmd/revoga-mcp` (verdes); 10 testes
Playwright verdes; lockfile do frontend idêntico ao master (`grep -c emnapi` = 11 nos dois); E2E
local com a API real (smoke, token pessoal, fluxo OAuth completo com PKCE e refresh, CLIs, auditoria).

**Não verificado ainda (só depois do deploy):** conexão real pelo claude.ai, Claude Code, Gemini
Enterprise e MCP Inspector; cabeçalho anti-frame do Cloudflare Pages em `/autorizar-ia`.

## Ambiente de teste

- Container `rc-mcp-test-pg` (Postgres 16, porta **55440**, usuário/senha `radiocheck`, rede
  Docker `rc-mcp-test`). Bancos: `radiocheck_mcp_test`, `radiocheck_mcp_test_t14`,
  `radiocheck_mcp_test_d2` — todos com migrations até a 0070.
- **Um banco por agente.** Dois agentes rodando testes no mesmo banco dão deadlock (os pacotes
  fazem `TRUNCATE users ... CASCADE`). Para um agente novo:
  `docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -c "CREATE DATABASE radiocheck_mcp_test_<x> TEMPLATE radiocheck_mcp_test_d2;"`
- Sempre `go test -p 1` com
  `TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_<x>?sslmode=disable"`.
- Migration nova no banco de teste:
  `MSYS_NO_PATHCONV=1 docker run --rm --network rc-mcp-test -v "$(pwd)/migrations:/migrations:ro" migrate/migrate:v4.17.1 -path=/migrations -database "postgres://radiocheck:radiocheck@rc-mcp-test-pg:5432/radiocheck_mcp_test_<x>?sslmode=disable" up`
- Se o container sumir (o Docker já foi resetado uma vez nesta sessão), recrie:
  `docker network create rc-mcp-test; docker run -d --name rc-mcp-test-pg --network rc-mcp-test -p 55440:5432 -e POSTGRES_USER=radiocheck -e POSTGRES_PASSWORD=radiocheck -e POSTGRES_DB=radiocheck_mcp_test postgres:16-alpine` e rode as migrations.
- Os testes com dados de volume real (paridade de números) pedem uma cópia de prod — ver
  `docs/operations/migrations.md` §Testar migration contra dados de prod e a memória
  `local-db-has-prod-restore` (o restore local que existia antes do reset do Docker pode não
  existir mais: confira e, se precisar, peça um dump ao dono).

## Convenções para tools novas (a Fatia 1 tem de seguir)

- **Registro:** cada família num arquivo `workers/internal/mcp/tools_<familia>.go`, registrada em
  `novoServidor` (servidor.go) e acrescentada a `ToolsEsperadas` **na mesma ordem e no mesmo
  commit** — o `TestInventario` trava o conjunto e exige anotações e descrição.
- **Identidade:** o handler recebe o `*Principal` por closure (`registrarX(s, p, deps)`); o servidor
  é reconstruído a cada pedido. As dependências (repos/serviços) entram por `ConfigServidor` ou
  struct de deps montada em `NovoModulo` — hoje `ConfigServidor` só tem `Versao`: amplie-o.
- **Resultado:** `resultado(v)` (mesmo JSON no texto e no `structuredContent`, sempre objeto, sem
  `outputSchema` — use `Out = any` no `sdk.AddTool`); falha de negócio com `falha(msg, código)`.
- **Falta criar (deixado para a Fatia 1):**
  - envelope de lista do contrato §3.3: `{items, page:{total, limit, offset, returned, has_more}}`,
    `limit` padrão 25, teto 100, `offset` com teto, `total` real;
  - `ressalvas []string` no resultado quando houver ressalva de negócio;
  - helper `falhaInterna(log, err)` → loga e devolve `falha("erro interno", "interno")`. **Nunca
    devolva `error` Go do handler**: o SDK repassa `err.Error()` cru ao modelo (ex.: SQLSTATE).
- **Anotações:** `anotacoesLeitura()` em toda tool de leitura (as quatro explícitas).
- **Nada de regra de negócio no MCP:** a tool chama a mesma função que o handler HTTP. Se a lógica
  está dentro do handler, extraia primeiro (refactor sem mudar comportamento, coberto pelos testes
  do handler). Atenção especial: `catalog.ApprovedDetectionsFilter`, base financeira
  `in_slot + bonus`, tratamento de `cancelada` (docs/features/cancelled-campaign-handling.md),
  `plano_do_dia` (lógica do `DayDetailModal`, provavelmente no handler).
- **Teste de paridade:** toda tool que espelha uma tela tem teste "tool × handler HTTP devolvem o
  mesmo número" no mesmo filtro.
- **Pontes:** `obter_campanha` aceita id **ou** `codigo` do hub; `obter_cliente` devolve `hub_id`.
  Quando essas tools existirem, acrescente às `Instrucoes` (instrucoes.go) a linha de pontes.
- **Descrição de tool:** o que devolve, quando usar, a armadilha, "nunca invente ids, pergunte".
  Português, `snake_case`, prefixos do contrato (`listar_`, `obter_`, `buscar_`…).
- **Desempenho:** o SDK chama o `getServer` duas vezes por pedido; o `SchemaCache` já evita
  refazer a reflexão. Com ~40 tools, meça; se pesar, memoize o servidor por pedido.
- **Datas:** instantes em ISO 8601 UTC; períodos `YYYY-MM-DD`; dinheiro em reais. `null` = não se
  aplica, nunca zero.

## Armadilhas que já custaram caro nesta sessão

- **`window.confirm` no frontend devolve Promise** (o `ConfirmProvider` substitui o nativo): sempre
  `await`, senão "Cancelar" executa a ação. Use `useConfirm()` de `components/ConfirmModal`.
- **Plano com código pronto também erra:** as revisões pegaram bugs no próprio código do plano
  (confirm, teste não-idempotente, teste que passava pelo motivo errado). Não pule revisão.
- **`go mod tidy`** remove dependência que ainda não é importada; só rode quando o código importar.
- **`gofmt -l` acusa quase tudo** por causa de CRLF na árvore (autocrlf). Não reformate arquivos
  que você não tocou; `git ls-files --eol` mostra que o índice está em LF.
- **`middleware.RealIP` confia em cabeçalho forjável atrás do Cloudflare.** No MCP use
  `oauth.IPDe(r)` (lê `CF-Connecting-IP`).
- **SDK aceita lote JSON-RPC** na versão 2025-03-26 — já recusado no `SoPost`; não remova.
- **A API local leva tempo para subir** (carrega o índice); espere o `/v1/internal/health`.

## Pendências registradas (não bloqueiam a Fatia 1)

Follow-ups técnicos do MCP:
- Detecção de reuso de refresh (estilo hub: reuso após 30 s derruba a conexão).
- Revogar o access antigo na rotação; podar `mcp_tokens` (cresce para sempre).
- Corrida de milissegundos entre renovar token e trocar senha (aceita na spec §4.1).
- Variável própria para a origem do portal (hoje reaproveita `NOTIFICATIONS_BASE_URL`).
- Anti-frame também em `/pendentes` quando a Fatia 4 criar a tela.
- `actor_type` da auditoria para escritas do MCP: a spec §4.6 pede `meta.mcp = {token_id,
  pendente_id?}` — implementar na Fatia 3 (hoje `Principal.TokenID` existe e não é lido).
- MCP Apps (resposta renderizada no chat) — fora desta entrega, porta aberta pelo
  `structuredContent`.

Para o dono (infra, fora do MCP — **não faça sem pedir**):
- Limitador do `/auth/login` em prod é burlável (mesmo problema do `RealIP`).
- Publicar a porta da API só em `127.0.0.1` no compose de prod (senão o `CF-Connecting-IP` pode
  ser forjado batendo direto na 8080).
- Imagens `minio/minio` e `minio/mc` não baixam mais do Docker Hub.
- Compose base ainda monta `migrations/` no `docker-entrypoint-initdb.d` (regra 4.6 / F-111).

## Quando o MCP estiver 100% (fim de todas as fatias) — deploy

Roteiro completo em `docs/operations/mcp.md`. Resumo:
1. Merge da branch no master **com o OK do dono**; push do backend; o dono roda `./scripts/deploy.sh`
   na VM (o `shadow_migration_test` aplica a 0070 numa cópia de prod).
2. Smoke: `curl -si -X POST https://api.e-monitor.online/mcp` → 401 com
   `WWW-Authenticate: Bearer realm="emonitor-mcp", ...` (404 = módulo não montou; 403 = alguém
   removeu `DisableLocalhostProtection`).
3. Só então o frontend vai ao ar (já sobe junto com o push do master — combine a ordem com o dono).
4. Gemini: o dono roda `registra-cliente-mcp` na VM e configura o console do Gemini.
5. Conectar claude.ai, Claude Code e Gemini; chamar `quem_sou_eu` em cada.
6. Hub (Fatia 5): atualizar a linha "Pontes" das instruções do hub (hoje diz que o E-monitor não
   tem MCP), o `SEM_MIDIA_NO_HUB` das tools de checking e o §1 do contrato do ecossistema.
