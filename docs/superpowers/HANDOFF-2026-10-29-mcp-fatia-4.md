# Handoff — MCP do E-monitor (fim da Fatia 3, início da Fatia 4)

Data: 2026-10-29 (código e docs); verificação final concluída em 2026-10-30. Abra uma sessão nova do Claude Code e cole o bloco abaixo como primeira mensagem.
**O trabalho acontece no worktree `C:\tmp\emonitor-mcp`** (branch `feat/mcp-server`), não na pasta
principal `C:\Users\marke\Desktop\Programas\E-Series\E-monitor`, que é compartilhada com outras sessões.

**Pré-requisito:** Docker Desktop aberto (os testes de integração usam um Postgres em container).

---

## Prompt para colar na nova sessão

```
Você vai continuar o servidor MCP do E-monitor. As Fatias 0 (fundação), 1 (leitura, 27 tools), 2
(relatórios, +17) e 3 (escrita `livre` e `confirma`, +20 = 64 tools) estão implementadas e revisadas na
branch feat/mcp-server. Seu trabalho agora é a Fatia 4: o nível `critico` EXECUTÁVEL (pedido pendente +
página /pendentes no portal) e as tools críticas da spec §6 "Fatia 4".

═══════════════════════════════════════════════════════════════
REGRAS DO DONO (não negociáveis)
═══════════════════════════════════════════════════════════════
- Trabalhe SÓ no worktree C:\tmp\emonitor-mcp, branch feat/mcp-server (confira com
  `git branch --show-current`). A pasta principal é compartilhada com outras sessões: nunca rode
  git checkout/switch/stash nela e não a toque. A branch não tem upstream de propósito.
- NADA vai para o master, e nada é pushado, até o MCP estar 100% pronto. O dono decide quando.
- Leia o CLAUDE.md da raiz antes de tudo: regras 4.8, 5 (nunca `npm install` no frontend; só
  `npm ci`: a Fatia 4 traz tela nova, /pendentes) e 6.x.
- Você NÃO tem acesso à VM nem ao banco de prod (CLAUDE.md §7).
- Mensagens de commit terminam com a linha Co-Authored-By exigida pela sessão.

═══════════════════════════════════════════════════════════════
LEIA NESTA ORDEM
═══════════════════════════════════════════════════════════════
1. docs/superpowers/HANDOFF-2026-10-29-mcp-fatia-4.md  (este arquivo — seções abaixo do prompt)
2. docs/superpowers/specs/2026-10-27-mcp-server-design.md  (spec: D5/D6, §4.6 auditoria, §5 níveis —
   em especial §5.2 "critico" e as notas datadas da Fatia 3 —, §6 "Fatia 4", §7 critério da fatia)
3. docs/superpowers/plans/2026-10-29-mcp-fatia-3-escrita.md  (decisões F3-1..F3-12 e o padrão de
   tasks/testes; as notas de execução datadas estão nas linhas F3-4 e F3-10)
4. docs/features/mcp.md (seção "Escrita (Fatia 3)" inteira) e docs/operations/mcp.md
5. Contrato comum dos MCPs da E-Series, no repo do hub, branch origin/main:
   `git -C ../hub show origin/main:docs/architecture/mcp-ecossistema.md`  (§3.5 níveis, §6 pendentes)
   e, como referência de código, o `/pendentes` do hub e da Plura
   (hub `backend/src/mcp/write/`; Plura ADR-011/012).
6. O código: workers/internal/mcp/escrita.go (o motor; o ramo "crítico: barrado" é onde você entra),
   escritas.go, confirmacao.go, tools_escrita_*.go, instrucoes.go; workers/internal/acoes/*;
   workers/internal/catalog/{ha_veiculacao.go,campaigns_ciclo.go}; workers/internal/mcp/mcptest/escrita.go.

═══════════════════════════════════════════════════════════════
O QUE FAZER
═══════════════════════════════════════════════════════════════
1. Use a skill superpowers:writing-plans para escrever o plano da Fatia 4 em
   docs/superpowers/plans/AAAA-MM-DD-mcp-fatia-4-critico.md: `mcp_acoes_pendentes` (24 h; estados
   pendente → aprovada → executada | falhou, recusada, expirada; transições por UPDATE … WHERE estado = …),
   `criarPendente` no lugar do ramo barrado, a tela /pendentes (aprovar/recusar; só quem pediu aprova;
   "não existe" e "não é seu" respondem igual), o varredor (expira pendentes, marca `falhou` o que ficou
   em `aprovada` > 10 min, e passa a podar `mcp_confirmacoes_usadas`), as 21 tools da spec §6 Fatia 4, e
   a correção do EditarCampanha não transacional (ver "Pendências" — é pré-requisito). Antes de
   escrever, leia de verdade cada handler que uma tool crítica vai reaproveitar.
2. Mostre o plano ao dono e espere o OK.
3. Execute com superpowers:subagent-driven-development (lotes coerentes; revisão de aderência +
   qualidade por lote; revisão final da branch). Leia docs/subagentic-use/Subagents-usage.md.
4. Ao fim: atualize docs/features/mcp.md, a memória do projeto e um handoff novo para a Fatia 5.
```

---

## Estado em 2026-10-30

**Branch `feat/mcp-server`**: **157 commits** sobre `origin/master` (`git rev-list --count origin/master..HEAD`
= 157 já com este handoff; 156 no commit anterior; `origin/master` em `61f9aaa`, sem `git fetch` desde 2026-10-29).
Nada pushado, mergeado nem deployado. A Fatia 3 vai de `7e3cd5c` (o plano) ao topo (`git log --oneline
7e3cd5c..HEAD` = 51 commits com este: migration, token, motor, `HaVeiculacao`, 4 extrações para `internal/acoes`,
20 tools em 7 tasks, correções de revisão, instruções/inventário, docs). Cada lote (A: motor; B: extrações; C: as 20
tools) passou por revisão de aderência e de qualidade, com re-revisão das correções.

**As 64 tools** = 44 de leitura (Fatias 0 a 2) + **20 de escrita** (2 `livre`, 18 `confirma`):
`marcar_notificacoes_lidas`, `reconhecer_similaridade_material`, `criar_tipo_material`, `editar_tipo_material`,
`editar_material`, `criar_cliente`, `editar_cliente`, `editar_pmm_target`, `editar_emissora`, `criar_campanha`,
`editar_campanha`, `editar_emissoras_campanha`, `vincular_material`, `desvincular_material`,
`editar_emissoras_material`, `criar_regra_distribuicao`, `editar_regra_distribuicao`,
`apagar_regra_distribuicao`, `definir_override`, `apagar_override`. Tabela com nível, escalada e função chamada de
cada uma: `docs/features/mcp.md` ("Fatia 3 — escrita (20)").

**O que a Fatia 3 entrega:** o motor de níveis (`Escrita[In]` + `ferramentaEscrita`); o token de confirmação
HMAC de uso único (`mcp_confirmacoes_usadas`, migration 0071); a escalada a `critico` reavaliada a cada chamada; o
`critico` BARRADO (resposta com motivo e link da tela, sem escrever); a auditoria `mcp.escrita.*` com
`meta.mcp = {token_id}`; a camada `internal/acoes`, que os handlers HTTP e as tools compartilham.

**Verificação final da Fatia 3** (HEAD `388f3e5`, código idêntico ao de `f14aa5a`; os dois últimos commits antes
deste handoff são só docs; bancos recriados do template `radiocheck_mcp_test_d3` entre os pacotes, `-p 1`):
- Cross-compile linux (`CGO_ENABLED=0 GOOS=linux go build ./...`) com e sem `-tags avaliacao`: ok. `go vet ./...`: ok.
  `go list -deps ./cmd/api | grep avaliacao`: vazio.
- `./internal/mcp/...`: `ok mcp 1037s`, `auditoria`, `oauth`, `tokens` ok (`avaliacao` e `mcptest` não têm teste).
  `./internal/acoes/...` ok (420s), `./internal/api/handlers/` ok (446s), `./internal/supervisor/` ok,
  `./internal/hubnotify/` ok.
- `./internal/catalog/`: **só as 4 falhas pré-existentes conhecidas** (`TestDetections_ListPaged_IgnoredExcluded`,
  `TestDetections_CreateManualBatch_WithProof`/`NoProof`, `TestMaterialTypes_List`) **mais** o flaky de horário
  `TestBuildDailySummary_WithDowntime` (CLAUDE.md §6.6: teste de unidade sem banco que usa `time.Now().Add(-13h)` e falha
  antes de ~13:00 UTC; rodou às 11:49 UTC; arquivo intocado nesta branch).
- Avaliação da Fatia 2 ainda responde: `TestAvaliacao_Gabarito` passa com as 64 tools no servidor (as 10 respostas
  iguais ao gabarito). A releitura por subagente às cegas NÃO foi refeita (as escritas não mudam as 44 leituras).
- **Boot de verdade:** a API compilada subiu contra um banco do template + NATS em container (`/v1/internal/health` 200);
  com um token pessoal real, `POST /mcp tools/list` devolveu **64 nomes**, em ordem alfabética e iguais a `ToolsEsperadas`;
  `criar_tipo_material` pelo `/mcp` fez preview → confirmação (`executou: true`, linha `mcp.escrita.criar_tipo_material` no
  `audit_log` com `token_id`, 1 linha em `mcp_confirmacoes_usadas`); repetir o mesmo pedido foi recusado (`[nome_duplicado]`: o `Preview` roda antes
  da checagem do token, então a recusa de negócio vence). Banco, NATS e binário foram descartados.

**Desempenho:** `BenchmarkNovoServidor` ≈ **5,0 ms/op** com 64 tools (~3,2 MB, ~8,8 mil alocações; eram 2,75 ms com
44), acima do limite de 4 ms; por isso `NovoHandlerMCP` passou a montar o `Server` **uma vez por pedido** (o SDK o pede
duas) e `BenchmarkPedidoToolsList` (o pedido inteiro) caiu de ~12,5 ms para ~9,1 ms. Se a Fatia 4 somar mais ~21
tools, remeça os dois.

**Não verificado ainda (só depois do deploy):** conexão real pelos 3 clientes; paridade com dado real (roteiro
"Conferência com dado real" em `docs/operations/mcp.md`, que agora tem o passo 4 das escritas, numa campanha de
TESTE); os deep-links abertos num navegador logado.

## Ambiente de teste

- Container `rc-mcp-test-pg` (Postgres 16, porta **55440**, usuário/senha `radiocheck`, rede `rc-mcp-test`).
  **Template atual: `radiocheck_mcp_test_d3`** (migrations até a 0071, `mcp_confirmacoes_usadas` incluída; o `d2`
  parou na 0070). A migration da Fatia 4 (`mcp_acoes_pendentes`) gera o `d4`: receita em `docs/operations/mcp.md`
  ("Testar as tools localmente").
- **Um banco por agente/pacote**, criado do template:
  `docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -c "CREATE DATABASE radiocheck_mcp_test_<x> TEMPLATE radiocheck_mcp_test_d3;"`.
  **Nunca aponte `TEST_DATABASE_URL` para o template.**
- **`export TEST_DATABASE_URL=…` em TODA chamada de Bash** (sem ela os testes de banco dão SKIP e parecem verdes).
  Sempre `go test -p 1`. O pacote `./internal/mcp` leva **10 a 15 min** (~280 testes só na escrita e em `acoes`): `-timeout 60m`
  e rode em segundo plano. Rode `./internal/mcp/...` antes de `./internal/catalog/` e **recrie o banco do template
  entre os pacotes** (o catalog deixa clientes e o guard passa a recusar).
- O `radiocheck_test` "vazio ao lado" apodrece; falha de guard em `api/handlers`/`users` é ambiental (memória
  `test-db-native-pg-shadows-docker`).
- Template antigo nunca re-migrado quebra datas relativas (`no partition of relation`): `SELECT ensure_month_partitions(18);`
  nele ou recrie-o.
- Avaliação: banco `radiocheck_mcp_avaliacao`, `AVALIACAO_DATABASE_URL` (nunca `DATABASE_URL`), CLI
  `go run -tags avaliacao ./internal/mcp/avaliacao/cli semear|tools|chamar` (o `tools` lista as 64).
- Boot local de verdade (foi feito na verificação): `go build -o <fora do repo>/api.exe ./cmd/api`; `nats:2.10-alpine`
  num `docker run -p 4223:4222`; um banco do template; env `DATABASE_URL`, `NATS_URL`, `S3_ENDPOINT` (qualquer URL: o
  cliente S3 é preguiçoso), `S3_BUCKET/ACCESS_KEY/SECRET_KEY`, `MASTERS_PATH`, `JWT_SECRET` (≥ 32 bytes),
  `RADIOCHECK_BOOTSTRAP_ADMIN_EMAIL/PASSWORD`, `API_PORT=18080`. `POST /v1/internal/auth/login` → JWT;
  `POST /v1/internal/mcp/tokens` `{nome, validade_dias}` → token pessoal; `POST /mcp` com `tools/list` e
  `tools/call` (cabeçalhos `Accept: application/json, text/event-stream` e `Mcp-Protocol-Version: 2025-06-18`).

## Convenções novas da Fatia 3 (somam às das Fatias 1 e 2)

- **O motor:** `Escrita[In]` é **dado** (`Nome`, `Descricao`, `Nivel`, `Escalar`, `Preview`, `Executar`, `Tela`,
  `Recurso`); `ferramentaEscrita(s, cfg, p, escritaX(cfg))` a registra, e `escritaX(cfg)` é uma função que **devolve**
  o valor (é assim que um executor de pendente poderá obtê-lo). O `In` de toda tool `confirma` ou escalável embute
  `CampoConfirmacao`. `Escalar` roda em TODA chamada e consulta o banco; recusa de negócio é `*acoes.Recusa`
  (libera o token), o resto é falha interna (não libera). O `Executar` roda sob `WithoutCancel` + 60 s.
- **`internal/acoes`:** `Servico` (repositórios + `SupervisorDeCampanha` + `AvisoHub` por interface +
  `EmSegundoPlano` injetável), `Recusa{Mensagem, Codigo, Dados, Causa}`, `ErrAlvoInexistente`, `recusaDoBanco` (FK/CHECK
  → recusa em português, sem nome de constraint), `RegistrarFalhaRecat`. Os handlers HTTP traduzem `Recusa` em
  `handlers/acoes_http.go`. **Regra:** lógica de escrita nova nasce em `acoes`; handler e tool chamam a mesma função.
- **`catalog.HaVeiculacao(ctx, EscopoVeiculacao)`** e `HaVeiculacaoNaEmissora`: "existe veiculação APROVADA no mesmo
  recorte que o recat varre" — a base da escalada. Travada contra o recat real por `TestHaVeiculacao_ParidadeComORecatReal`
  (se o recat mudar de recorte, este teste tem de falhar). **`catalog.EstadoPeloCiclo`** reproduz o
  `PromoteScheduledLifecycle` em Go, travada contra o SQL por `TestEstadoPeloCiclo_BateComOSQL`: mexeu num, mexa no outro.
- **Dia é sempre SP:** `hojeSP(E.agora())`, nunca `time.Local`. `E.Agora` é o relógio injetável; nos testes `agoraFixa(b)`.
- **Helpers das tools:** `tools_escrita_comum.go` (`errEntrada`, `errNaoEncontrado`, `errRecusa`, `uuidDaEntrada`,
  `aparar`, `limiteDeRunas`, `recorte`, `violacaoDeUnicidade`). Validar a entrada em português ANTES de chamar `acoes`
  (algumas mensagens movidas para `acoes` ainda são inglesas por paridade com a rota).
- **Fakes e bancada de teste:** `mcptest/escrita.go` (`SupervisorFalso` com `Start/Pause/StopWorkersForCampaign` além de
  `UpdateStations/Reload` — já serve a `iniciar_campanha`/`pausar_campanha` —, `HubFalso` com `Conferir`/`Eventos`/
  `EventosDoTipo`, `Sincrono` para o `EmSegundoPlano`); `escrita_ferramentas_test.go` (`novaBancadaTool`, `.chamar`,
  `.preview`, `.confirmarEExecutar`, `.barrado`, `.chamarErro`, `.exigeAuditoria`, `chamarHandlerCorpo` para a paridade
  tool × rota HTTP, `esquemaDa`/`propriedades`). **`confirmarEExecutar` mora no pacote `mcp`, não em `mcptest`** (o
  `mcptest` não pode importar quem o importa).
- **Teste por tool (padrão):** preview não escreve; confirmar executa; **paridade de efeito** (mesmo estado semeado em duas
  fixtures, uma pela rota e uma pela tool, comparando linhas e chamadas aos falsos); cada caso de escalada e o vizinho que
  NÃO escala; cada recusa; uma linha de auditoria com o recurso certo. Mais: `escrita_inventario_test.go` (as 20 escritas:
  nível, sufixo `Nível:`, anotações, `confirmacao` no schema, campos proibidos) — **toda escrita nova entra em
  `escritasEsperadas` e em `ToolsEsperadas` no mesmo commit.**
- **Provas de "o passado não muda":** `TestEditarEmissorasCampanha_ProvaDaRemocao`, `TestVinculos_ProvaF34_PassadoNaoMuda`.
  A decisão de NÃO escalar essas tools depende delas; se uma regra de negócio mudar, elas devem falhar.

## O ponto de troca da Fatia 4 (exatamente onde você entra)

`workers/internal/mcp/escrita.go`, em `ferramentaEscrita`, o passo **"4. crítico: barrado até a Fatia 4 (F3-1)"**: hoje
ele audita `mcp.escrita.requer_aprovacao`, devolve `respostaBarrada{Executou:false, Nivel:"critico", Motivo, Preview, Aviso:
avisoBarrado, Links:{Tela}}` e NÃO escreve nem consome token. A Fatia 4 troca esse bloco por `criarPendente(...)` (grava
`mcp_acoes_pendentes` e devolve `{executou:false, preview, pendenteId, expiraEm, aprovarEm:"<portal>/pendentes?id=…",
aviso}`); o resto do motor não muda. Junto: a constante `avisoBarrado`, o parágrafo "Hoje o crítico ainda não gera pedido
de aprovação…" de `Instrucoes` (volta a valer o texto do hub, "entregue o link e diga que nada aconteceu ainda") e
`TestInstrucoes_EscritaCriticoBarrado`; o `Tela` de cada tool deixa de ser o destino principal (vira link de apoio).

O que já está pronto para você:
- **Todo `Executar` roda sem a tool, e o dos ramos barrados já é testado direto.** Os testes de paridade de efeito
  de regra e de override semeiam tocadas (estado em que a tool escalaria a `critico` e seria barrada) e por isso chamam o
  `Executar` diretamente, ao lado da rota HTTP (`criar/editar/apagar_regra_distribuicao`, `definir_override`,
  `apagar_override`); o `editar_campanha` com remoção do código do hub idem. A Fatia 4 roda esses mesmos `Executar` DEPOIS
  da aprovação, num pedido do portal. Nenhum `Executar` depende do `ctx` do MCP além do `Principal` (e do relógio `E.agora()`).
- `Escritas`, `Escalada.Motivo` e `previa` já são os dados que o pendente guarda (preview e motivo).
- **Falta o registro:** hoje cada `registrarEscritas*` constrói a `Escrita[In]` por pedido, dentro de closures genéricas.
  O executor de pendente precisa de um mapa `nome → func(ctx, E, p, argsJSON) (feito, error)` que desserialize para o `In`
  da tool e **revalide** (o SDK valida o schema ANTES da tool rodar; aqui você mesmo desserializa e valida) — e o `Escalar`
  NÃO precisa rodar de novo, mas o `Preview`/`planejar*` de cada tool relê o estado e recusa se o alvo sumiu em 24 h.
- `Principal` hoje só vem do bearer do `/mcp`. Na aprovação o ator é o admin logado no portal (JWT) e o `token_id` é o do
  token que PEDIU (guardado na linha pendente); a auditoria da Fatia 3 já aceita `meta.mcp = {token_id}` — acrescente
  `pendente_id`.

## Fatia 4: as tools (spec §6) e a mecânica (spec §5.2)

| Tool | Por que é crítica |
|---|---|
| `iniciar_campanha`, `pausar_campanha` | sobe/derruba workers; cliente passa a ver (o `SupervisorFalso` já tem `Start`/`Pause`) |
| `cancelar_campanha` | congela déficit; sai das telas operacionais |
| `editar_precos_campanha`, `editar_cpm_fixo` | dinheiro |
| `criar_veiculacao_manual` (sem comprovante; comprovante via deep-link) | entra na cobrança |
| `ignorar_veiculacao`, `restaurar_veiculacao` | muda categoria das outras do mesmo dia |
| `publicar_pos_venda`, `reenviar_pos_venda`, `revogar_destinatario_pos_venda` | email para fora; documento congelado |
| `criar_usuario` (com boas-vindas), `editar_usuario` (papel/carteira), `desativar_usuario` | email para fora; acesso de cliente |
| `desativar_cliente`, `ativar_cliente` | login do cliente |
| `apagar_campanha`, `apagar_material`, `apagar_cliente` | irreversível |
| `editar_webhook_cliente`, `testar_webhook` | dado sai para sistema externo (confirmar se `testar_webhook` fica crítico ou sai do catálogo) |

São 21 tools (total 85). Mecânica (spec §5.2): `critico` grava `mcp_acoes_pendentes` (24 h); estados `pendente → aprovada →
executada | falhou`, `recusada`, `expirada`; transições por `UPDATE … WHERE estado = …` (atômico); **só quem pediu aprova**
e "não existe" e "não é seu" respondem igual; na aprovação os argumentos são revalidados pelo mesmo schema; varredor
periódico na API expira pendentes e marca `falhou` o que ficou em `aprovada` > 10 min. Eventos de auditoria:
`mcp.pendente.criada|aprovada|recusada|executada|falhou`. Frontend: `/pendentes` (exige login; **não** entra em
`PUBLIC_ROUTES`), com anti-frame (`X-Frame-Options: DENY` + `frame-ancestors 'none'`) em `frontend/public/_headers`, como
`/autorizar-ia`. Tela nova = regra 5 do `CLAUDE.md` (`npm ci`, lockfile completo) e o deploy em dois caminhos (backend
primeiro; memória `deploy-split-frontend-backend-ordering`). Verifique, para cada tool, se a rota HTTP já tem uma função
reutilizável: se não tiver, extraia para `internal/acoes` ANTES (handler travado por teste, depois a extração, depois a
tool), como no Lote B da Fatia 3. `criar_usuario`, `publicar_pos_venda` e os webhooks mexem em segredo e token: o
resultado nunca leva senha, token, link de boas-vindas nem segredo (contrato §3.3).

## Armadilhas novas (valem para a Fatia 4)

- **Prioridade da recusa:** o `Preview` roda ANTES do ramo crítico; uma recusa de negócio nele (`entrada_invalida`,
  `nao_encontrado`) vale antes de qualquer pendente ou token. Mantenha assim.
- **`mcp.escrita.falha` e token gasto:** erro interno depois de consumir o token não o devolve. No `critico` o análogo é a
  pendente que fica em `aprovada` > 10 min (o varredor marca `falhou`).
- **Auditoria fora da transação:** o motor audita depois do sucesso; falha de auditoria é logada e não derruba a resposta.
  Para o `critico` executado fora da tool a spec (§4.6) pede a gravação dentro da função compartilhada — decida isso no plano.
- **`token_id` fica oculto no `auditoria_mcp`** (o leitor omite toda chave que case `token`); `pendente_id` não casa, mas
  decida se quer expô-lo.
- **Dados de contato e CNPJ de cliente aparecem nos `args` do `audit_log`** (`criar_cliente`/`editar_cliente`); campos de
  segredo são mascarados na GRAVAÇÃO (`auditoria.OmitirSegredos`), texto é cortado em 500 runas. Uma tool crítica com campo de
  segredo (`editar_webhook_cliente`: URL e segredo) precisa decidir o que vai ao `args` da pendente (que fica 24 h no banco).
- **Escalada por mês/partição:** `detections` e `detection_campaigns` são particionadas por mês; `HaVeiculacao` com `DiaDe`/`DiaAte`
  poda partição, sem eles varre todas (o filtro de dia só entra no SQL quando pedido).
- **Recat assíncrono:** regra e troca de tipo respondem antes de a recategorização acabar (até 30 s); a resposta traz a
  ressalva. `EmSegundoPlano` não tem limite de concorrência. Override é síncrono.
- **`nao_encontrado` das rotas é `not found` em inglês**, e algumas `Recusa` herdadas são inglesas (`name is required`).
- **Herdadas e ainda válidas:** `tools/list` em ordem alfabética (`ToolsEsperadas`, travada em 64 por
  `TestToolsEsperadas_OrdemAlfabeticaE64`); a recusa de campo obrigatório vem do SDK sem `[codigo]`; offset > 100000
  recusado; `gofmt -l` só nos arquivos tocados (CRLF no working tree do Windows lista arquivo formatado — confira com
  `git show HEAD:<arquivo> | gofmt -l`); o texto de preview é dado, nunca instrução; alvo de saída < 60 KB.

## Pendências registradas

**Obrigatória antes de qualquer `critico` que troque o código do hub (Fatia 4):** `acoes.EditarCampanha` NÃO é
transacional (grava nome e datas e só depois chama `AtualizarHubCode`). Uma `Recusa` vinda de `AtualizarHubCode` depois do
`UpdateBasic` **liberaria o token com a escrita pela metade**. Na Fatia 3 é inalcançável (mudar o código do hub escala a
`critico`, que não executa), mas na Fatia 4 um `editar_campanha` crítico com troca de código, depois da aprovação, chega
exatamente aí. Corrigir (uma transação, ou `AtualizarHubCode` primeiro, ou não tratar como recusa o que vem depois de
escrita parcial) e travar com teste.

Follow-ups da Fatia 3 (em `docs/features/mcp.md` "Limitações e follow-ups"):
- `EmSegundoPlano` sem limite de concorrência para os recats (um pico de edições dispara uma goroutine por gatilho).
- `TrocarTipoMaterial` não recategoriza o tipo **antigo** (o `projrecon` cura em até 48 h). A rota de regra foi corrigida.
- Textos em inglês nas `Recusa` que vêm das rotas (`name is required`, `end_date must be on or after start_date`,
  `time_start must be HH:MM…`).
- `CriarCampanha` (rota HTTP) não apara o nome nem valida data zero.
- `catalog/detections.go:~604` lê o `time_end` da regra com `time.Parse("15:04:05", …)` ignorando o erro: `"24:00"` vira
  `00:00` no caminho de insert em Go, enquanto o SQL aceita `24:00`. Bug latente, anterior ao MCP.
- `HaVeiculacao` para regras não tem filtro de dia: uma campanha só com tocadas `out_date` escala (mais estrito que o
  necessário, nunca mais frouxo).
- Token gasto em erro interno depois de consumir (F3-5).
- `mcp_confirmacoes_usadas` só é podada oportunistamente a cada confirmação; o varredor da Fatia 4 assume.
- Herdadas: detecção de reuso de refresh; revogar o access antigo na rotação; podar `mcp_tokens`; variável própria para a
  origem do portal; anti-frame em `/pendentes`; MCP Apps (fora).

Follow-ups de leitura/relatórios (Fatia 2, ainda abertos): fuso do `station_failures` e do modal do digest em UTC;
`visao_gerencial` paga `queryStations`/`queryRecentDetections` e descarta; `insights_campanha` sem limite de período;
a tela limpa a URL antes de um 401; o seletor do `/insights` junta campanhas fora da página 1 com 1 request por id.

Para o dono (fora do MCP — **não faça sem pedir**): limitador do `/auth/login` burlável; porta da API só em `127.0.0.1`;
imagens `minio/minio`/`minio/mc`; mount de `migrations/` em `initdb.d` (F-111); a pasta `C:\tmp\emonitor-base-check` pode
ser apagada. **Renumerar as migrations no merge:** o `master` tem `0070_users_username`; `0070_mcp_fundacao` vira `0071`,
`0071_mcp_confirmacoes` vira `0072` e a da Fatia 4 (`mcp_acoes_pendentes`, hoje seria `0072`) vira `0073`. Os trailers
dos commits da branch misturam "Claude Opus 5.5" e "Claude Sonnet 5.5" (dois modelos trabalharam nela); não há o que
corrigir.

## Quando o MCP estiver 100% (fim de todas as fatias) — deploy

Roteiro completo em `docs/operations/mcp.md` (inclui "Conferência com dado real" das tools de relatório e, desde a Fatia 3,
das escritas numa campanha de TESTE, e a renumeração das migrations). Resumo: merge com OK do dono (renumerar as migrations)
→ push do backend → `./scripts/deploy.sh` na VM (o `shadow_migration_test` aplica as pendentes sobre uma cópia dos dados de
prod) → smoke `POST /mcp` = 401 com `WWW-Authenticate` → frontend → conectar os 3 clientes e chamar `quem_sou_eu` →
paridade com dado real (leitura, relatórios e escritas) → hub (Fatia 5).
