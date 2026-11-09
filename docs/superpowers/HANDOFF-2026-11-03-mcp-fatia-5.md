# Handoff — MCP do E-monitor (fim da Fatia 4, início da Fatia 5)

Data: 2026-11-03. Abra uma sessão nova do Claude Code e cole o bloco abaixo como primeira mensagem.
**O trabalho acontece no worktree `C:\tmp\emonitor-mcp`** (branch `feat/mcp-server`), não na pasta
principal `C:\Users\marke\Desktop\Programas\E-Series\E-monitor`, que é compartilhada com outras sessões.

**Pré-requisito:** Docker Desktop aberto (os testes de integração usam um Postgres em container).

---

## Prompt para colar na nova sessão

```
Você vai fechar o servidor MCP do E-monitor. As Fatias 0 (fundação), 1 (leitura, 27 tools), 2 (relatórios, +17),
3 (escrita `livre` e `confirma`, +20) e 4 (escrita `critico` executável + a tela /pendentes, +18 críticas e
minhas_pendentes = 83 tools) estão implementadas e revisadas na branch feat/mcp-server. Seu trabalho agora é a
Fatia 5 (ecossistema e docs, spec §7) e, com o OK do dono, o merge e o deploy.

═══════════════════════════════════════════════════════════════
REGRAS DO DONO (não negociáveis)
═══════════════════════════════════════════════════════════════
- Trabalhe SÓ no worktree C:\tmp\emonitor-mcp, branch feat/mcp-server (confira com
  `git branch --show-current`). A pasta principal é compartilhada com outras sessões: nunca rode
  git checkout/switch/stash nela e não a toque. Nunca `git stash` puro. A branch não tem upstream de propósito.
- NADA vai para o master, e nada é pushado, até o MCP estar 100% pronto. O dono (Dereck) decide quando mergear.
- Leia o CLAUDE.md da raiz antes de tudo: regras 4.8 (migration passa pelo shadow test), 5 (nunca `npm install` no
  frontend; só `npm ci`), 6.x (provar que o deploy não quebra) e 7 (você NÃO tem acesso à VM nem ao banco de prod).
- Mensagens de commit terminam com a linha Co-Authored-By exigida pela sessão.

═══════════════════════════════════════════════════════════════
LEIA NESTA ORDEM
═══════════════════════════════════════════════════════════════
1. docs/superpowers/HANDOFF-2026-11-03-mcp-fatia-5.md  (este arquivo — seções abaixo do prompt)
2. docs/superpowers/specs/2026-10-27-mcp-server-design.md  (§7 "5 — Ecossistema e docs"; §5.2 e §6 já trazem as notas
   da Fatia 4)
3. docs/features/mcp.md (seção "Crítico e /pendentes (Fatia 4)") e docs/operations/mcp.md
4. O contrato comum dos MCPs da E-Series, no repo do hub, branch origin/main:
   `git -C ../hub show origin/main:docs/architecture/mcp-ecossistema.md`  (confira o caminho do repo do hub; §1 lista os MCPs)
5. docs/superpowers/plans/2026-10-30-mcp-fatia-4-critico.md  (decisões F4-1..F4-16)

═══════════════════════════════════════════════════════════════
O QUE FAZER
═══════════════════════════════════════════════════════════════
Ver "Fatia 5: o que falta" e "Checklist de merge e deploy" neste handoff. Mostre ao dono o que vai mexer no hub
ANTES de mexer (é outro repositório) e espere o OK. Não corrija follow-ups de rota sem pedir (lista abaixo).
```

---

## Estado em 2026-11-03

**Branch `feat/mcp-server`**: `git rev-list --count origin/master..HEAD` = **233** (já com este handoff; era 230 antes dos
commits de docs da Fatia 4; `origin/master` sem `git fetch` desde 2026-10-29, então reconfira). **Nada pushado, mergeado nem
deployado.** O dono só mergeia com o MCP 100% pronto.

**As 83 tools** = 44 de leitura (Fatias 0 a 2) + 20 de escrita `livre`/`confirma` (Fatia 3) + **18 de escrita `critico`** +
`minhas_pendentes` (Fatia 4). As 18 críticas, que nunca executam na chamada e viram um pedido aprovado em `/pendentes`:
`cancelar_campanha`, `apagar_campanha`, `editar_cpm_fixo`, `editar_precos_campanha`, `criar_veiculacao_manual`,
`ignorar_veiculacao`, `restaurar_veiculacao`, `criar_usuario`, `editar_usuario`, `desativar_usuario`, `desativar_cliente`,
`ativar_cliente`, `apagar_cliente`, `apagar_material`, `editar_webhook_cliente`, `testar_webhook`, `reenviar_pos_venda`,
`revogar_destinatario_pos_venda`. Tabela com motivo, recusas e função chamada: `docs/features/mcp.md` ("Fatia 4 — escrita crítica
(18)"). Saíram do catálogo da spec: `iniciar_campanha`, `pausar_campanha`, `publicar_pos_venda` (F4-1, com o porquê).

**O que a Fatia 4 entrega:** `mcp_acoes_pendentes` (migration `0072`) e o store `Pendentes`; o ramo crítico do motor trocado por
`criarPendente` (dedup por `args_hash`); a aprovação `Aprovacoes.Aprovar` (revalida args pelo schema, reroda o `Preview`,
**recusa com `preview_desatualizado` se o resumo mudou**, executa, grava as `ressalvas`); as rotas `GET/POST /v1/internal/mcp/pendentes…`
(só quem pediu); o varredor; a tela `/pendentes` (JSX, anti-frame em `_headers` + `emoldurada.js`); `EditarCampanha` atômico
(F4-9); as extrações para `internal/acoes` com handler travado por teste antes; `ErroAposEscrita` e `feitoRelido`; os
benchmarks e a documentação. Doc canônico: `docs/features/mcp.md` ("Crítico e /pendentes (Fatia 4)") e `docs/operations/mcp.md`.

**Desempenho (2026-11-03, Ryzen 7 7435HS):** `BenchmarkNovoServidor` ~5,8 ms/op (~4,1 MB, ~11 mil alocações);
`BenchmarkPedidoToolsList` ~11,9 ms/op (~6,3 MB, ~19,6 mil alocações) — logo abaixo do teto de ~12 ms. **Memoizar por processo os
schemas que o SDK infere a cada `AddTool` antes de acrescentar qualquer tool** e remedir.

**Verificação final (Task 25 do plano):** este handoff foi escrito pelo agente de documentação, que **não** rodou a verificação
final (cross-compile, suítes por pacote, Playwright, boot de verdade com aprovação ponta a ponta). Registre aqui o resultado dela
quando existir, ou refaça-a antes do merge (receita no plano, Task 25, e em "Checklist de merge e deploy").

**Não verificado ainda (só depois do deploy):** conexão real pelos 3 clientes; paridade com dado real (roteiro "Conferência com
dado real" em `docs/operations/mcp.md`, agora com o passo 5 do crítico, numa campanha de TESTE); `/pendentes` aberto logado num
navegador real contra a API de prod; o anti-frame de `/pendentes` e `/autorizar-ia` aplicado pelo Cloudflare Pages à resposta de
fallback da SPA (`curl -sI … | grep -i frame`).

## Ambiente de teste

- Container `rc-mcp-test-pg` (Postgres 16, porta **55440**, usuário/senha `radiocheck`, rede `rc-mcp-test`, `fsync=off`).
  **Template atual: `radiocheck_mcp_test_d4`** (migrations até a 0072, `mcp_acoes_pendentes` incluída). **O `d4` foi refeito com a
  coluna `ressalvas TEXT[]`**: a 0072 foi editada no lugar durante o desenvolvimento (commit `d2b3fb4`); qualquer banco criado do `d4`
  antigo, ou migrado com a 0072 antiga, dá `column "ressalvas" does not exist` — recrie-o. O `d3` parou na 0071. Receita para gerar o
  template seguinte (`dN`) em `docs/operations/mcp.md` ("Testar as tools localmente"); o plano da Fatia 4 (Tasks 0 e 1) tem os comandos
  do `d3` → `d4`.
- **Um banco por agente/pacote**, criado do template:
  `docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -c "CREATE DATABASE radiocheck_mcp_test_<x> TEMPLATE radiocheck_mcp_test_d4;"`.
  **Nunca aponte `TEST_DATABASE_URL` para o template** e **nunca duas suítes no mesmo banco** (concorrência no mesmo DB gera deadlock
  e contagem errada que parece regressão).
- **`export TEST_DATABASE_URL=…` em TODA chamada de Bash** (sem ela os testes de banco dão SKIP e parecem verdes).
  Sempre `go test -p 1`. O pacote `./internal/mcp` leva **10 a 15 min**: `-timeout 60m` e rode em segundo plano. Rode
  `./internal/mcp/...` antes de `./internal/catalog/` e **recrie o banco do template entre os pacotes**.
- O `radiocheck_test` "vazio ao lado" apodrece; falha de guard em `api/handlers`/`users` é ambiental (memória
  `test-db-native-pg-shadows-docker`).
- Avaliação: banco `radiocheck_mcp_avaliacao`, `AVALIACAO_DATABASE_URL` (nunca `DATABASE_URL`), CLI
  `go run -tags avaliacao ./internal/mcp/avaliacao/cli semear|tools|chamar` (o `tools` lista as 83).
- Boot local de verdade: receita do handoff da Fatia 4 (`HANDOFF-2026-10-29-mcp-fatia-4.md`, "Ambiente de teste"). Para o crítico,
  com o JWT do login: `GET /v1/internal/mcp/pendentes`, `POST …/pendentes/{id}/aprovar`.

## Convenções novas da Fatia 4 (somam às das Fatias 1 a 3)

- **Registro único das escritas.** Cada família expõe `escritas<Familia>(cfg) []escritaRegistravel`; `escritasDe(cfg)`
  (`registro_escritas.go`) concatena as 12; `registrarEscritas` registra as tools; `indexarEscritas` monta o mapa nome → escrita e
  **falha com nome repetido**. O **mesmo `Escrita[In]`** registra a tool e executa a pendente aprovada: nada de segunda cópia da
  lógica. Escrita nova = função `escritaX(cfg)` + entrada na lista da família + `ToolsEsperadas` (ordem alfabética) + `escritasEsperadas`
  (`escrita_inventario_test.go`) + o número em `TestToolsEsperadas_OrdemAlfabeticaE83` e em `TestIndexarEscritas_TrintaEOito`, **no mesmo
  commit**.
- **O executor é `Aprovacoes.Aprovar`** (`aprovacao.go`). Ordem: `UPDATE → aprovada` (10 s, `WithoutCancel`) → acha a escrita → `decodificarArgs`
  (mesmo JSON schema do SDK, `DisallowUnknownFields`) → `Preview` (com `ComPrincipal`) → compara o resumo (`mesmaPrevia`) →
  `Executar` (60 s, `WithoutCancel`) → audita `mcp.escrita.<tool>` → `MarcarExecutada` com as ressalvas. Os caminhos de falha são
  distintos de propósito: `*acoes.Recusa` → volta a `pendente` (422); `*erroConferencia` (a conferência caiu) → volta a `pendente` (503);
  qualquer outro erro do `Executar` → `falhou` (500 `interrompida`). Tool que chama `Aprovar` à mão nos testes usa `b.aprovar` /
  `b.aprovarPelaRota` (`escrita_ferramentas_test.go`).
- **O store `Pendentes`** (`pendentes.go`): toda transição é `UPDATE … WHERE estado = … AND tentativa = …`; todo SQL filtra
  `user_id` (o "não existe" e o "não é seu" são o mesmo `ErrPendenteNaoAchada`). `Criar` é idempotente por `(user_id, args_hash)`
  entre os abertos. `MarcarFalhaDeNegocio` aceita um resumo novo (`novaPrevia`) para gravar na mesma atualização.
- **O varredor** (`varredor.go`): 1 min, `pg_try_advisory_xact_lock`, 3 passos em savepoints (expirar, fechar presas > 10 min, podar
  confirmações). Iniciado em `cmd/api/main.go` via `IniciarVarredor(ctx)`.
- **`ComPrincipal` / `PrincipalDe`** (`principal.go`): o `Preview` não recebe o `Principal`; o motor (na chamada) e a aprovação (na
  conferência) o põem no contexto. É o que faz "nunca a si mesmo" valer nos dois momentos; falha fechada se faltar (`donoDoPedido`).
- **`feitoRelido[T]`** (`escrita.go`): o `feito` de uma escrita JÁ gravada. Se só a releitura do registro falha, a escrita **não** vira
  erro (o modelo repetiria; o pedido aprovado ficaria `falhou` com a escrita feita): o resultado cai para o formato mínimo (`saidaSoID`…)
  e uma ressalva manda conferir. Use-o em toda escrita nova; **nada depois da gravação pode devolver erro** sem ser um `*acoes.ErroAposEscrita`.
- **`acoes.ErroAposEscrita{Mensagem, Causa}`** (`acoes/recusa.go`): a ação já gravou algo e não terminou. É o oposto de `Recusa`
  ("nada foi gravado"). A `Mensagem` (em português, sem SQLSTATE, nome de tabela nem segredo) vira o `erro` do pedido `falhou` e o 500
  `interrompida`; a `Causa` fica só no log. Hoje: `ApagarCampanha` (histórico apareceu depois do cancelamento) e `CriarUsuario`
  (carteira não gravada por inteiro).
- **`Escrita.ArgsParaAuditoria`**: gancho opcional que troca os args no `audit_log` (a URL do webhook vira esquema+host+SHA-256). Vale no
  pedido e na aprovação; gancho que falha grava `{}`. Os `args` do **pedido** ficam inteiros (o `Executar` os usa).
- **O `Preview` precisa ser determinístico.** A aprovação compara o resumo recalculado com o gravado: tudo que o `Preview` escreve é
  comparado. Texto com horário de agora, ordem de mapa, contagem de fila ou qualquer coisa que varie sem o mundo ter mudado gera
  `preview_desatualizado` à toa (foi o caso do texto da fila de webhook, hoje fixo). O `Executar` re-planeja (`planejar*`) e **não**
  confia no retrato do `Preview`.
- **Ressalvas.** `feito.Ressalvas` = os avisos da execução (aprovação 200, linha do pedido, `/pendentes`, `minhas_pendentes`). O aviso
  que a pessoa PRECISA ler ("a pessoa não tem a senha") vai nas ressalvas, nunca só num campo do resultado (a releitura pode falhar).
- **Segredos.** Nenhum schema de escrita aceita `senha`/`segredo`/`token`/`password`/`secret`; teste por tool prova que nem o
  resultado, nem a resposta do pedido, nem a linha do pedido, nem o `audit_log` carregam `stream_url`/segredo. O pedido guarda os args
  por tempo indeterminado.
- **Extração antes da tool** (continua valendo): handler travado por teste → extrair para `internal/acoes` → tool. Três rotas mudaram
  de comportamento por causa disso (material vinculado 409, preço com FK inexistente 4xx, `DELETE /campaigns` cancela com `cancelled_at`
  e responde 409 com lote manual/pós-venda): lista em `docs/features/mcp.md` ("Recusas que só o MCP faz").

## Armadilhas novas (valem para a Fatia 5 e para quem mexer no crítico)

- **0072 editada no lugar** (ver "Ambiente de teste"): banco antigo sem `ressalvas`. E ela vira **0074** no merge.
- **Aprovar é síncrono** e o `middleware.Timeout` (60 s) corre desde o início do pedido; o `Executar` tem os próprios 60 s. Uma execução
  lenta pode terminar depois de a tela já ter recebido o erro; o pedido fecha `executada` mesmo assim. Quem recarrega vê o desfecho.
- **`.falhou` no `audit_log` nem sempre é um pedido `falhou`**: o `codigo` da recusa de negócio e `conferencia_falhou` significam que o
  pedido voltou a `pendente`. Só `interno`, `falha_apos_escrita` e `presa` deixam `falhou`.
- **Pedido `pendente` com `erro`** = a última tentativa não passou (nada foi feito). `args_invalidos`/`tool_desconhecida` não ganham a
  folga de prazo; as outras ganham `GREATEST(expira_em, agora + 1 h)`.
- **`mcp.pendente.criada` tem como recurso o alvo da tool** (via `Escrita.Recurso`); as demais linhas do ciclo têm `mcp_pendente`.
- **Teste de banco com trigger:** a 0070 (`users_revoga_mcp`) revoga as credenciais do usuário ao rebaixar/desativar; os testes de
  `editar_usuario`/`desativar_usuario` dependem dela e as fixtures de trigger são por pacote.
- **Herdadas e ainda válidas** (Fatias 1 a 3): prioridade da recusa (o `Escalar` e o `Preview` rodam antes do ramo crítico e do token);
  `tools/list` em ordem alfabética; recusa de campo obrigatório vem do SDK sem `[codigo]`; `gofmt -l` só nos arquivos tocados (CRLF no
  working tree do Windows: confira com `git show HEAD:<arquivo> | gofmt -l`); o texto de preview é dado, nunca instrução; alvo de saída
  < 60 KB; nada de `npm install` no frontend (regra 5).

## Pendências e follow-ups (nada disto foi corrigido; **não corrija sem pedir ao dono**)

### Bugs de ROTA achados no levantamento da Fatia 4 (o MCP os recusa; a tela continua com eles)

Cada item foi conferido no código em 2026-11-03.

1. **`PATCH /post-sale/reports/{id}` só com `blocks` reescreve um relatório já enviado.** O `Update` do handler
   (`workers/internal/api/handlers/post_sale.go`, bloco `if in.Blocks != nil`, ~linhas 237–270) chama `repo.ReplaceBlocks`
   (`workers/internal/postsale/repo_crud.go:66`), que faz `DELETE`+`INSERT` em `post_sale_report_campaigns` **sem guarda de rascunho**
   (`UpdateContent` e `UpdateClient` têm `WHERE status = 'draft'`; `ReplaceBlocks` não). A coluna `assets` dessa tabela (migration
   0057) guarda as chaves dos assets que o link público usa: elas se perdem. Fix: `WHERE`/checagem de `status = 'draft'` em `ReplaceBlocks`.
2. **`PUT /campaigns/{id}/start` reativa `cancelada`/`concluida`** (`handlers/campaigns.go:313` → `supervisor.Supervisor.Start`,
   `supervisor/supervisor.go:228`: `UpdateStatus(…, "ativa")` sem pré-condição, o `cancelled_at` fica; campanha inexistente = 500). O
   `LifecycleScheduler` rebaixa em ≤ 60 s uma campanha ativada antes do início (incidente 2026-10-01). É o motivo de não existir
   `iniciar_campanha`.
3. **`POST /clients/{id}/webhook-test` responde `202 {"queued": true}` mesmo quando descarta** (`handlers/webhooks.go:144`,
   `acoes.TestarWebhook` em `acoes/webhooks.go:131` devolve `Enfileirado:false`; a rota ignora). Descarta com cliente sem URL, webhook
   desligado ou sem `webhook.test`/`*` nos eventos (`acoes.MotivoDoTesteDescartado`). A tool recusa; a UI nunca distinguiu.
4. **Desativar cliente não corta sessão, chave de API nem webhook** (`catalog.Clients.SetActive` só grava a coluna; só o login novo
   é barrado; JWT dura até 8 h). Documentado em `client-deactivation.md`; o preview da tool avisa.
5. **`PATCH /admin/users/{id}` não tem guarda de si mesmo nem de último admin** (`handlers/users.go`, `acoes.EditarUsuario`
   `acoes/usuarios.go:284`). Só o MCP as tem (`proprio_usuario`, `ultimo_admin`).
6. **`POST /admin/users` recebe a senha em claro no corpo** (`handlers/users.go:162`, `Password string`) e devolve o `welcome.link` (que
   abre a senha). O MCP gera a senha no servidor.
7. **`DELETE /materials/{id}` deixa veiculações órfãs** (`acoes.ApagarMaterial`, `acoes/materiais.go:119`, só bate na FK de
   `campaign_materials`; a FK de `detections.commercial_id` caiu na migration 0024). O master no disco e os hashes também ficam. O MCP
   recusa (`material_com_historico`); a rota apaga.
8. **`RevokeRecipient` não confere o relatório da URL** (`handlers/post_sale.go:449` → `postsale/repo_crud.go:430`, `UPDATE … WHERE id =
   recipient_id`). O MCP confere (`nao_encontrado` para destinatário de outro relatório).

*Itens do levantamento que a extração JÁ corrigiu (não são mais follow-ups):* `DELETE /campaigns/{id}` pausava antes da transação
(deixava `cancelada` sem `cancelled_at`) e dava 500 com lote manual/pós-venda — agora `acoes.ApagarCampanha` recusa antes de gravar,
cancela de verdade e a rota responde 409 (commit `6ec55fa`). Resta: o cancelamento acontece **antes** do delete, então um delete
que falha depois deixa a campanha `cancelada` (agora com `cancelled_at`).

### Achados DURANTE a Fatia 4 (não corrigidos)

9. **Corrida do último admin.** `recusarUltimoAdmin`/`contarAdminsAtivos` (`mcp/tools_escrita_criticas_usuarios.go:224–260`) contam fora
   da transação da escrita (`acoes.EditarUsuario`). Duas aprovações simultâneas podem rebaixar/desativar os dois últimos admins.
   Fix: advisory lock em `acoes.EditarUsuario` e recontar dentro dele.
10. **O hub desfaz ações do E-monitor.** `client.upsert` (`handlers/hubsync.go`, `upsertCliente`; `is_active=$9` na linha ~221 e o `INSERT` do passo 3 na ~258) regrava
    `is_active=$9` (padrão `true` se o hub não manda `active`) e, no passo 3, **recria** o cliente vinculado que foi apagado aqui
    (`INSERT INTO clients … hub_id`). Ativar/desativar/editar/apagar cliente no E-monitor **não avisam o hub** (só `acoes.CriarCliente`
    manda `cliente.upsert`, `acoes/clientes.go:98`).
11. **O worker de webhook lê a URL na hora do envio e ignora `webhook_enabled`** (`webhook/worker.go:167`, `COALESCE(c.webhook_url,'')`
    no `claimBatch`). Desligar o webhook não cancela as entregas na fila e trocar a URL redireciona as pendentes.
12. **`Resend` com SMTP desligado sobrescreve um `sent` real com `disabled` e responde 204** (`postsale/publish.go:170` →
    `sendOne` devolve `"disabled"` → `MarkEmail`; handler `post_sale.go:429`). A tool recusa (`email_desligado`); a rota não.
13. **`/autorizar-ia` só tem a camada `_headers` contra moldura**, sem guarda em JS (`AuthorizeAiPage.jsx` não usa
    `frontend/src/utils/emoldurada.js`; `PendentesPage.jsx:244` usa). Fix: o mesmo `emoldurada()` lá.
14. **`/pendentes` não tem item na barra lateral** (só o link da IA e Minha conta → "Integração com IA" → "Pedidos da IA").
15. **`tools/list` ~11,9 ms** com 83 tools (teto do plano ~12 ms): memoizar os schemas inferidos por processo antes de acrescentar tools.
16. **`editar_campanha` escalada a crítico atravessando a meia-noite de SP** recebe um `preview_desatualizado` (o preview cita os dias que
    já passaram). Aprovar de novo funciona; poderia ser evitado tirando a data corrente do texto.
17. **`minhas_pendentes` corta o `erro` em 250 caracteres** (`tools_pendentes.go:19`) e as ressalvas em 250 cada / 300 somadas; o texto
    inteiro está em `/pendentes?id=`.
18. **O pedido não é podado**: `mcp_acoes_pendentes` e os `args` (inclusive a URL inteira do webhook) ficam para sempre; só
    `mcp_confirmacoes_usadas` é podada. Decidir uma retenção (ex.: 90 dias para finais) quando a tabela crescer.

### Herdadas de fatias anteriores (ainda abertas)

`docs/features/mcp.md` "Limitações e follow-ups (Fatia 3)" e "Armadilhas e limitações conhecidas": `EmSegundoPlano` sem limite de
concorrência; `TrocarTipoMaterial` não recategoriza o tipo antigo; textos em inglês nas `Recusa` herdadas das rotas; `CriarCampanha`
(rota) não apara o nome; `catalog/detections.go:~604` e `time.Parse("24:00")`; `HaVeiculacao` para regras sem filtro de dia; detecção
de reuso de refresh; revogar o access antigo na rotação; podar `mcp_tokens`; variável própria para a origem do portal; fuso do
`station_failures` e do modal do digest em UTC; `visao_gerencial` paga consultas que descarta; `insights_campanha` sem limite de
período. Para o dono (fora do MCP, não faça sem pedir): limitador do `/auth/login` burlável; porta da API só em `127.0.0.1`; imagens
`minio/minio`/`minio/mc`; mount de `migrations/` em `initdb.d` (F-111). Os trailers de commit da branch misturam "Claude Opus 5.5" e
"Claude Sonnet 5.5"; não há o que corrigir.

## Fatia 5: o que falta (spec §7, "5 — Ecossistema e docs")

Pronto quando: "doc com header YAML, índice `docs/README.md` atualizado". O que a spec lista e o estado de cada item:

- **`docs/features/mcp.md` e `docs/operations/mcp.md`**: feitos (e atualizados para a Fatia 4 em 2026-11-03). `status` do `mcp.md` está
  `parcialmente-implementado`: vira `implementado` quando a Fatia 5 fechar. Reconfira `ultima-verificacao` e `codigo-relacionado`.
- **`docs/README.md`**: as duas linhas do MCP já falam das Fatias 0 a 4; atualize quando a 5 fechar.
- **Regra "o MCP faz parte do pronto" no `CLAUDE.md`**: já existe uma linha no mapa de consulta (`| **MCP do E-monitor** …`). Falta
  acrescentar o crítico: "escrita nova passa pela lista única `escritasDe`, `Preview` determinístico (a aprovação compara o resumo),
  `/pendentes`, a 0072/0074" e a armadilha do `npm ci`. Edite o `CLAUDE.md` só com o OK do dono (é arquivo de instruções do projeto).
- **Hub (outro repositório; peça OK antes de mexer):** linha **"Pontes"** (o hub já devolve `pontes.emonitor` em `obter_cliente`; as instruções
  do MCP do hub ainda dizem que "o E-monitor ainda não tem MCP"), **`SEM_MIDIA_NO_HUB`** e **§1 do contrato** `mcp-ecossistema.md`
  (a lista dos MCPs da E-Series passa a incluir o do E-monitor). Não verificado por este agente: confira no `origin/main` do hub o texto
  exato de cada um.
- **ADRs** (padrão Plura ADR-010/011/012): decidir onde moram no E-monitor (não existe `docs/adr/`; hoje as decisões vivem em
  `docs/superpowers/plans/` e `docs/architecture/`). Candidatas: MCP dentro da API Go (D1), crítico = pedido aprovado por humano em
  `/pendentes` (F4), auditoria no executor.
- **Os 3 clientes conectando em prod** (claude.ai, Claude Code, Gemini Enterprise) chamando `quem_sou_eu`, e a avaliação mcp-builder
  refeita às cegas se o catálogo de leitura mudar (as 44 leituras não mudaram na Fatia 4; a releitura por avaliador às cegas não foi refeita, e o `TestAvaliacao_Gabarito` não foi reexecutado por este agente).

## Checklist de merge e deploy (com OK do dono; nunca antes do MCP 100%)

1. **`git fetch origin`** e reveja `git log --oneline HEAD..origin/master`; rebase/merge e resolva.
2. **Renumere as migrations**, nesta ordem, no master que tiver `0070_users_username` e `0071_recordings`: `0072_mcp_acoes_pendentes` → **0074**,
   `0071_mcp_confirmacoes` → **0073**, `0070_mcp_fundacao` → **0072** (seis arquivos `.up/.down.sql`; atualize as referências nos docs
   e nos comentários de código). **Confira o maior número de migration no master na hora do merge** (em 2026-11-09 era
   `0071_recordings`); se o master ganhou migrations novas, desloque tudo mais. **Preserve a ordem relativa:** a `CREATE OR REPLACE
   FUNCTION mcp_revogar_credenciais_do_usuario` no fim da `0072_mcp_acoes_pendentes` (vira 0074) precisa rodar DEPOIS da que cria a função
   na `0070_mcp_fundacao` (vira 0072); a renumeração em bloco mantém isso, mas não renumere uma sem a outra.
3. **Bancos que aplicaram a 0072 antiga** (dev, testes, o template `d4` de outras máquinas): recrie. Em prod não há o que fazer (nunca
   rodou).
4. **Shadow migration test** (regra 4.8): o `deploy.sh` aplica as pendentes sobre uma cópia dos dados de prod e aborta se falhar. As três
   são só `CREATE TABLE`/índices/trigger; teste ainda assim num banco com dados (o clone de prod do dev, `local-db-has-prod-restore`).
5. **Provar o deploy sem quebrar** (CLAUDE.md regra 6): `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` (com e sem `-tags
   avaliacao`), `go vet ./...`, `go list -deps ./cmd/api | grep avaliacao` vazio; sem CLI novo nesta fatia (regra 6.7 não se aplica;
   `revoga-mcp` e `registra-cliente-mcp` já estão no `workers.Dockerfile`). Boot: nomes de métrica únicos, maps inicializados, o
   módulo sobe sem panic (o `NovoModulo` não derruba o boot se o índice das escritas falhar).
6. **Frontend:** `frontend/package.json` e `package-lock.json` **intocados** (`git diff origin/master -- frontend/package-lock.json` vazio;
   regra 5); `npx playwright test e2e/mcp-*.spec.js` e `npm run build` verdes.
7. **Testes** (bancos recriados do `d4` entre pacotes, `-p 1`): `./internal/mcp/...`, `./internal/acoes/...`, `./internal/api/handlers/`,
   `./internal/api/`, `./internal/supervisor/`, `./internal/postsale/`, `./internal/welcome/`, `./internal/users/`, `./internal/catalog/`
   (só as 4 falhas pré-existentes conhecidas + o flaky de horário `TestBuildDailySummary_WithDowntime` antes das 13:00 UTC).
8. **Ordem do deploy: BACKEND primeiro, depois o frontend** (memória `deploy-split-frontend-backend-ordering`): o CF Pages publica a
   cada push em `master`, e a `/pendentes` e a Minha conta chamam rotas novas (`/v1/internal/mcp/pendentes`, `/mcp/tokens`). Se o
   front subir antes, dá 404. Receita: push do backend → `./scripts/deploy.sh` na VM (com o override, regra 4.7) → smoke
   `POST /mcp` = 401 com `WWW-Authenticate` → smoke 401-vs-404 em `GET /v1/internal/mcp/pendentes` → só então o frontend. Confira o
   anti-frame (`curl -sI "https://e-monitor.online/pendentes" | grep -i frame`).
9. **Conferência com dado real** (`docs/operations/mcp.md`, passos 1 a 5), numa campanha de TESTE: o passo 5 prova o crítico ponta a
   ponta (pedido → aprovar em `/pendentes` → `audit_log` com `pendente_id`).
10. Variáveis: nada novo além das já documentadas (`MCP_ORIGEM_PUBLICA`, `MCP_REDIRECT_URIS`, `NOTIFICATIONS_BASE_URL`). O `criar_usuario` do
    MCP precisa de `WELCOME_ENC_KEY` e SMTP no servidor (senão recusa). Recrie a API com `--force-recreate --no-deps` (regras 4.1/4.2).
