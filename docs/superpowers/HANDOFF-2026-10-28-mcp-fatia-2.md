# Handoff — MCP do E-monitor (fim da Fatia 1, início da Fatia 2)

Data: 2026-10-28. Abra uma sessão nova do Claude Code e cole o bloco abaixo como primeira mensagem.
**O trabalho acontece no worktree `C:\tmp\emonitor-mcp`** (branch `feat/mcp-server`), não na pasta
principal `C:\Users\marke\Desktop\Programas\E-Series\E-monitor`, que é compartilhada com outras sessões.

**Pré-requisito:** Docker Desktop aberto (os testes de integração usam um Postgres em container).

---

## Prompt para colar na nova sessão

```
Você vai continuar o servidor MCP do E-monitor. As Fatias 0 (fundação) e 1 (leitura, 27 tools) estão
implementadas e revisadas na branch feat/mcp-server. Seu trabalho agora é a Fatia 2 (relatórios).

═══════════════════════════════════════════════════════════════
REGRAS DO DONO (não negociáveis)
═══════════════════════════════════════════════════════════════
- Trabalhe SÓ no worktree C:\tmp\emonitor-mcp, branch feat/mcp-server (confira com
  `git branch --show-current`). A pasta principal é compartilhada com outras sessões e já trocou de
  branch sem aviso: nunca rode git checkout/switch/stash nela e não a toque. A branch não tem
  upstream de propósito.
- NADA vai para o master, e nada é pushado, até o MCP estar 100% pronto. O dono decide quando.
  (Push no master publica o frontend no Cloudflare na hora; o backend só sobe com deploy.sh.)
- Leia o CLAUDE.md da raiz antes de tudo: regras 4.8, 5 (nunca `npm install` no frontend) e 6.x.
- Você NÃO tem acesso à VM nem ao banco de prod (CLAUDE.md §7).
- Mensagens de commit terminam com a linha Co-Authored-By exigida pela sessão.

═══════════════════════════════════════════════════════════════
LEIA NESTA ORDEM
═══════════════════════════════════════════════════════════════
1. docs/superpowers/HANDOFF-2026-10-28-mcp-fatia-2.md  (este arquivo — seções abaixo do prompt)
2. docs/superpowers/specs/2026-10-27-mcp-server-design.md  (spec: decisões D1–D10, §3 regra de ouro,
   §4.4 resultado, §6 catálogo de tools por fatia, §7 fatias)
3. docs/superpowers/plans/2026-10-27-mcp-fatia-1-leitura.md  (o plano da Fatia 1: decisões F1-1..F1-8
   e o padrão de tasks/testes que a Fatia 2 repete)
4. docs/features/mcp.md e docs/operations/mcp.md  (o que está implementado HOJE)
5. Contrato comum dos MCPs da E-Series, no repo do hub, branch origin/main:
   `git -C ../hub show origin/main:docs/architecture/mcp-ecossistema.md`  (§3 e §6)
6. O código: workers/internal/mcp/ (toolkit.go, leitura.go, servidor.go, tools_*.go,
   ferramentas_test.go) — é o padrão que as tools novas seguem.

═══════════════════════════════════════════════════════════════
O QUE FAZER
═══════════════════════════════════════════════════════════════
1. Use a skill superpowers:writing-plans para escrever o plano da Fatia 2 em
   docs/superpowers/plans/AAAA-MM-DD-mcp-fatia-2-relatorios.md, a partir da spec §6 "Fatia 2":
   insights_campanha, visao_gerencial, relatorio_data_hora, resumo_relatorio_campanha,
   exportar_consolidado, exportar_veiculacoes, mapa_cobertura_campanha, as 5 de falhas, assertividade,
   as 3 de pós-venda e auditoria_mcp; mais as telas lendo o filtro da URL (deep-link) e as 10 perguntas
   de avaliação do mcp-builder (>= 9/10). Antes de escrever, leia de verdade cada handler que uma
   tool vai reaproveitar.
2. Mostre o plano ao dono e espere o OK.
3. Execute com superpowers:subagent-driven-development (lotes coerentes; revisão de aderência
   + qualidade por lote; revisão final da branch). Leia docs/subagentic-use/Subagents-usage.md.
4. Ao fim: atualize docs/features/mcp.md (tabela de tools), a memória do projeto e um handoff
   novo para a Fatia 3.
```

---

## Estado em 2026-10-28

**Branch `feat/mcp-server`**: 67 commits sobre `origin/master` (contados com
`git rev-list --count origin/master..HEAD` ao escrever este handoff, já incluindo o commit dele; o
hash do topo é o deste commit — `git log -1 --format=%h`; o anterior é `fefb9df`). Nada pushado,
nada mergeado, nada deployado.

Fatia 0 (fundação) e Fatia 1 (leitura) implementadas e revisadas por lote (aderência ao plano +
qualidade). A verificação final da branch (Task 18: vet, cross-compile, todos os pacotes tocados,
revisor da branch inteira) é feita pelo controlador logo depois deste commit.

O que a minha parte (Lote D) verificou: `go vet ./internal/mcp/...`, `CGO_ENABLED=0 GOOS=linux go build ./...`
e `go test -p 1 ./internal/mcp/...` (resultado no relatório do lote); `TestInstrucoes_*` e
`TestInventario_*` verdes.

**As 27 tools** (todas de leitura, ordem alfabética = a do `tools/list` do SDK):
`buscar_emissoras`, `financeiro_campanhas`, `grade_campanha`, `listar_campanhas`, `listar_chaves_api`,
`listar_clientes`, `listar_entregas_webhook`, `listar_materiais`, `listar_materiais_campanha`,
`listar_overrides`, `listar_precos_campanha`, `listar_regras_distribuicao`, `listar_tipos_material`,
`listar_usuarios`, `listar_veiculacoes`, `minhas_notificacoes`, `obter_campanha`, `obter_cliente`,
`obter_emissora`, `obter_material`, `obter_usuario`, `obter_veiculacao`, `obter_webhook_cliente`,
`plano_do_dia`, `pmm_target_cliente`, `quem_sou_eu`, `veiculacoes_por_material`.
Tabela com a fonte de cada uma em `docs/features/mcp.md` ("Tools (27)").

**Desempenho:** `BenchmarkNovoServidor` = **1.844.667 ns/op** (1,84 ms; 1,24 MB e 3.058 alocações por
montagem), abaixo do limite de 2 ms do plano. O SDK monta o `Server` duas vezes por pedido (~3,7 ms).
Não otimizado. Cada tool nova encarece isso: remeça ao passar de ~45 tools; se pesar, memoize o
`*sdk.Server` por pedido.

**Não verificado ainda (só depois do deploy):** conexão real pelo claude.ai, Claude Code, Gemini
Enterprise e MCP Inspector; paridade dos números com dado real de prod (a Fatia 1 prova paridade em
fixtures, não no restore de prod — roteiro em `docs/operations/mcp.md`); cabeçalho anti-frame do
Cloudflare Pages em `/autorizar-ia`.

**Decisões da Fatia 1 que valem para a 2:** F1-1 (`plano_do_dia` devolve os ingredientes
autoritativos, sem "progresso por faixa"), F1-2 (resposta em português por whitelist de campos),
F1-3 (paginação no MCP, repositórios intactos), F1-4 (leituras novas no `catalog`, sem regra), F1-5
(`CPMDaCampanha` no `catalog`), F1-6 (canceladas), F1-7 (paridade contra fixtures), F1-8 (códigos de
erro). Texto completo no plano.

## Ambiente de teste

- Container `rc-mcp-test-pg` (Postgres 16, porta **55440**, usuário/senha `radiocheck`, rede
  Docker `rc-mcp-test`). Banco-template migrado até a 0070: `radiocheck_mcp_test_d2`.
- **Um banco por agente**, criado do template. Dois agentes no mesmo banco dão deadlock (os pacotes
  fazem `TRUNCATE ... CASCADE`):
  `docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -c "CREATE DATABASE radiocheck_mcp_test_<x> TEMPLATE radiocheck_mcp_test_d2;"`
- **`export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_<x>?sslmode=disable"`
  em TODA chamada de Bash** (o shell não persiste entre chamadas). Sem a variável, os testes de banco
  PULAM e a saída parece verde.
- Sempre `go test -p 1`. O pacote `./internal/mcp` inteiro leva ~2 min: use timeout longo
  (até 600000 ms no Bash).
- **Ordem:** rode `./internal/mcp/...` ANTES de `./internal/catalog/`. A suíte do catalog deixa ~15
  clientes no banco e o `dbtest` guard (limite de 10 clientes / 50 emissoras) passa a derrubar testes
  do MCP; é ambiental, não regressão (refaça o banco do template).
- O template `radiocheck_mcp_test_d2` agora tem as partições de `detections` e `detection_campaigns`
  de 2026_01 a 2026_09 (acrescentadas na Fatia 1; antes, testes antigos do catalog falhavam com "no
  partition for relation").
- Falhas **pré-existentes** do `internal/catalog` (também no commit anterior à Fatia 1, não suas):
  `TestDetections_ListPaged_IgnoredExcluded` (FK `ignored_by`), `TestBuildDailySummary_WithDowntime`
  (hora do dia), `TestDetections_CreateManualBatch_WithProof`/`NoProof`, `TestMaterialTypes_List`.
- Migration nova no banco de teste:
  `MSYS_NO_PATHCONV=1 docker run --rm --network rc-mcp-test -v "$(pwd)/migrations:/migrations:ro" migrate/migrate:v4.17.1 -path=/migrations -database "postgres://radiocheck:radiocheck@rc-mcp-test-pg:5432/radiocheck_mcp_test_<x>?sslmode=disable" up`
- Se o container sumir, recrie (`docker network create rc-mcp-test; docker run -d --name rc-mcp-test-pg
  --network rc-mcp-test -p 55440:5432 -e POSTGRES_USER=radiocheck -e POSTGRES_PASSWORD=radiocheck -e
  POSTGRES_DB=radiocheck_mcp_test postgres:16-alpine`) e rode as migrations.
- O restore de prod local (`docker-postgres-1:15433`) não existe mais; a paridade é em fixtures.

## Convenções (o que a Fatia 1 criou e a Fatia 2 reusa)

- **Registro:** `ferramentaLeitura(s, cfg, nome, descricao, handler)` ([toolkit.go](../../workers/internal/mcp/toolkit.go))
  — anotações de leitura, recuperação de `panic` (vira `[interno]`, não derruba a API), recusa
  `offset > 100000` (`[entrada_invalida]`), resultado nulo vira erro interno. Cada família em
  `tools_<familia>.go`, registrada em `novoServidor` e acrescentada a `ToolsEsperadas` **em ordem
  alfabética** (é a ordem do `tools/list` do SDK; o teste trava o conjunto) **no mesmo commit**.
- **Dependências:** struct `Leitura` ([leitura.go](../../workers/internal/mcp/leitura.go), montada em
  `NovoModulo`) passada por `ConfigServidor{Versao, Portal, Leitura, Log}`; helpers `nomesClientes`,
  `nomesEmissoras`, `nomesTipos`, `nomesMateriais`, `todasCampanhas`.
- **Listas:** `Paginacao` (embutida na entrada), `lista`, `paginar` (em memória), `buscarJanela`
  (repositório já paginado; trata `COUNT(*) OVER ()` = 0 além do fim), `mapear`, `listaInteira`.
- **Respostas/erros:** `ok`, `recusa`, `recusaEntrada`, `naoEncontrado` ("<x>: não existe no E-monitor
  [nao_encontrado]"), `falhaInterna` (loga, devolve `[interno]`; nunca `err.Error()`). `ressalvas []string`
  quando houver ressalva de negócio.
- **Leitura de entidade:** `lerCliente`, `lerCampanha`; `categoriaDe`; `limitesDoDiaSP`, `lerDia`, `dia`,
  `instante` (datas de calendário vs instantes UTC).
- **Testes:** `mcptest.DBCatalogo` + fixtures `mcptest.*` (`mcptest/catalogo.go`: cliente, emissora, tipo,
  material, campanha, regra, ajuste, preço, veiculação); `chamarTool`, `chamarToolErro`, `chamarHandler`,
  `itens`, `pag`, `ids`, `cfgTeste` (`ferramentas_test.go`). **Teste de paridade obrigatório**: tool x
  handler HTTP com claims de admin devolvem o mesmo número. `TestInventario_IdObrigatorioMandaNaoInventar`
  exige "nunca invente" na descrição da tool ou do parâmetro de todo `*_id`/`*_ids` obrigatório.
- **Leitura nova no `catalog`, sem regra:** quando só o handler tinha o SQL, extraia para o `catalog` e
  faça o handler chamar (foi assim em webhook-deliveries e api-keys).
- **Nada de regra de negócio no MCP:** `catalog.ApprovedDetectionsFilter`, base `in_slot + bonus`,
  `categorizer.Settle`/`RuleCoversDay`, tratamento de `cancelada` (docs/features/cancelled-campaign-handling.md).

## Armadilhas novas

- **O "progresso por faixa" do `DayDetailModal` é JavaScript** (`buildDayPlan`); não porte para Go (seria
  um terceiro motor de categoria). `plano_do_dia` devolve os ingredientes do servidor.
- **`ListPaged` de veiculações não usa o `ApprovedDetectionsFilter` literal** (equivalente na prática);
  o `Detections.Approved` usa o literal.
- **`COUNT(*) OVER ()` dá total 0 além do fim da lista**: o `buscarJanela` já trata.
- **Paginação instável sem desempate:** `clients`, `campaigns`, `stations` e `detections` ganharam
  `id` como último critério do `ORDER BY`; faça o mesmo em qualquer lista nova.
- **URL de webhook pode carregar segredo no caminho ou na query** (Slack/Discord/Zapier): só sai
  esquema+host (`https://host/...`), também em `ultimo_erro`. Vale para qualquer campo de URL do cliente.
- **CPM:** `catalog.CPMDaCampanha` espelha `/campaigns`: nenhum CPM (nem o fixo) quando investido +
  bonificação = 0; a bonificação fica no numerador.
- **`listar_veiculacoes` sem campanhas** exige período de até 93 dias; `de > ate` é recusado.
- **`minhas_notificacoes`** tem teto de 50 itens (o sininho); a ressalva avisa.
- **`gofmt -l` acusa quase tudo** por causa de CRLF (autocrlf); rode só nos arquivos que você tocou.
- **Pasta principal compartilhada:** use o worktree. A `feat/username-login` (outra sessão) tem
  `migrations/0070_users_username.*`, que **colide em número** com a nossa `0070_mcp_fundacao`; quem
  mergear por último renumera — o dono decide.
- Herdadas da Fatia 0 e ainda válidas: `window.confirm` do frontend devolve Promise (`useConfirm`);
  `go mod tidy` remove dependência ainda não importada; `middleware.RealIP` é forjável (no MCP use
  `oauth.IPDe`); o SDK aceita lote JSON-RPC (recusado no `SoPost`, não remova); a API local leva minutos
  para escutar.

## Pendências registradas

Follow-ups técnicos do MCP (abertos desde a Fatia 0):
- Detecção de reuso de refresh (reuso após 30 s derruba a conexão, como o hub).
- Revogar o access antigo na rotação; podar `mcp_tokens` (cresce para sempre).
- Corrida de milissegundos entre renovar token e trocar senha (aceita na spec §4.1).
- Variável própria para a origem do portal (hoje reaproveita `NOTIFICATIONS_BASE_URL`).
- Anti-frame também em `/pendentes` quando a Fatia 4 criar a tela.
- `actor_type` da auditoria para escritas do MCP: spec §4.6 pede `meta.mcp = {token_id, pendente_id?}`
  — implementar na Fatia 3 (`Principal.TokenID` existe e não é lido).
- MCP Apps (resposta renderizada no chat) — fora desta entrega, porta aberta pelo `structuredContent`.
- Desempenho: memoizar o `*sdk.Server` por pedido se o benchmark passar de ~2 ms/op ao crescer o catálogo.

Para o dono (infra, fora do MCP — **não faça sem pedir**):
- Limitador do `/auth/login` em prod é burlável (mesmo problema do `RealIP`).
- Publicar a porta da API só em `127.0.0.1` no compose de prod.
- Imagens `minio/minio` e `minio/mc` não baixam mais do Docker Hub.
- Compose base ainda monta `migrations/` em `docker-entrypoint-initdb.d` (regra 4.6 / F-111).
- A pasta `C:\tmp\emonitor-base-check` (cópia solta, não registrada como worktree) pode ser apagada.

## Quando o MCP estiver 100% (fim de todas as fatias) — deploy

Roteiro completo em `docs/operations/mcp.md`. Resumo:
1. Merge da branch no master **com o OK do dono** (renumerar a migration 0070 se a `feat/username-login`
   entrar antes); push do backend; o dono roda `./scripts/deploy.sh` na VM (o `shadow_migration_test`
   aplica a 0070 numa cópia de prod).
2. Smoke: `curl -si -X POST https://api.e-monitor.online/mcp` -> 401 com
   `WWW-Authenticate: Bearer realm="emonitor-mcp", ...` (404 = módulo não montou; 403 = alguém
   removeu `DisableLocalhostProtection`).
3. Só então o frontend vai ao ar (sobe junto com o push do master — combine a ordem com o dono).
4. Gemini: o dono roda `registra-cliente-mcp` na VM e configura o console do Gemini.
5. Conectar claude.ai, Claude Code e Gemini; chamar `quem_sou_eu` em cada.
6. Paridade com dado real: `financeiro_campanhas` e `grade_campanha` de 2 campanhas ativas contra
   `/campaigns` e `/detections` no portal. Divergência = bug.
7. Hub (Fatia 5): atualizar a linha "Pontes" das instruções do hub (hoje diz que o E-monitor não tem MCP),
   o `SEM_MIDIA_NO_HUB` das tools de checking e o §1 do contrato do ecossistema.
