# MCP do E-monitor — Fatia 3 (Escrita `livre` e `confirma`) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Dar ao MCP do E-monitor as 20 tools de escrita da spec §6 "Fatia 3" (2 `livre`, 18 `confirma`), o motor de níveis (token de confirmação HMAC de uso único, escalada reavaliada a cada chamada) e a auditoria das escritas com `meta.mcp = {token_id}`. Cada escrita chama a MESMA função que a rota HTTP chama e dispara os mesmos gatilhos (recategorização, reload de índice/workers, aviso ao hub). Escrita que escala para `critico` é **barrada com explicação e link da tela** até a Fatia 4 trazer `/pendentes`.

**Architecture:** Um pacote novo `workers/internal/acoes` concentra as escritas de negócio que hoje moram (parcialmente) dentro dos handlers: validação, chamada ao repositório e gatilhos. Os handlers passam a chamá-lo (refactor sem mudança de comportamento, travado por teste do handler escrito ANTES de mexer), e as tools do MCP também. As escritas que no handler já são uma chamada de repositório sem gatilho (reconhecer similaridade, roteiro, título, tipos de material, notificações, PMM, editar cliente) não ganham camada: a tool chama o mesmo método de repositório e a paridade é provada por teste de efeito. O motor mora em `workers/internal/mcp/escrita.go` (+ `confirmacao.go`), no padrão do hub (`backend/src/mcp/write/escrita.ts`), adaptado a Go: `Escrita[In]` é dado, `ferramentaEscrita` registra; `Escalar` consulta o banco e roda em TODA chamada.

**Tech Stack:** Go 1.26, go-sdk v1.8.0, pgx v5, chi v5, testify. Sem frontend nesta fatia.

**Spec:** [docs/superpowers/specs/2026-10-27-mcp-server-design.md](../specs/2026-10-27-mcp-server-design.md) §3 (regra de ouro), §4.4, §4.6 (auditoria), §5 (níveis), §6 "Fatia 3", §7.
**Contrato:** `hub/docs/architecture/mcp-ecossistema.md` (origin/main do hub) §3.4 (confirmação inválida não é erro), §3.5 (níveis). **Referência de código:** hub `backend/src/mcp/write/{escrita,confirmacao,niveis}.ts`.
**Plano anterior:** [2026-10-28-mcp-fatia-2-relatorios.md](2026-10-28-mcp-fatia-2-relatorios.md). **Handoff:** [HANDOFF-2026-10-28-mcp-fatia-3.md](../HANDOFF-2026-10-28-mcp-fatia-3.md).

---

## O que os handlers fazem hoje (levantamento de 2026-10-29)

Base de todas as decisões abaixo. Arquivos em `workers/internal/api/handlers/` salvo indicação.

| Rota | Handler | Gatilhos | Observações que mudam o desenho |
|---|---|---|---|
| `POST /campaigns` | `campaigns.go:127-153` | goroutine `avisarHubDaCampanha` (`campanha.upsert`) | Status nasce `programada` pelo DEFAULT do banco, mas o `LifecycleScheduler` (60 s, `supervisor/lifecycle_scheduler.go:193`) promove a `ativa` se `start_date ≤ hoje SP` → sobe workers. Datas em RFC3339; **sem** validação de datas. Barreira do código do hub `conferirCodigoOuRecusar` (`:188-256`) escreve direto no `ResponseWriter` |
| `PUT /campaigns/{id}` | `campaigns.go:370-482` | hub só se o código mudou | Substitui nome+datas; `hub_code` tri-estado. **Nenhuma** recategorização; o estado segue as datas pelo scheduler. Não transacional (nome/datas gravados antes de `AtualizarHubCode`) |
| `PUT /campaigns/{id}/stations` | `campaigns.go:645-696` | `Supervisor.UpdateStations` (`supervisor/station_changes.go:71`): para/sobe workers se `ativa` | Substituição total; lista omitida apaga todas. 204 sem corpo |
| `POST/PUT/DELETE /campaigns/{id}/materials…` | `campaign_materials.go:55-151` | `Supervisor.Reload(campanha)` (NATS `index.reload` + workers), só se `ativa` | **Sem recategorização**: atribuição é decidida na entrada da tocada (`evidence/attribution.go:29-70`) → passado intocado |
| `POST /materials/{id}/similarity/acknowledge` | `materials.go:368-379` | — | idempotente |
| `PATCH /materials/{id}/title|script` | `materials.go:288-350` | — | |
| `PATCH /materials/{id}/type` | `materials.go:248-278` | goroutine 30 s `RecategorizeForMaterial` (TODAS as campanhas do material) | 204 até para material inexistente |
| `POST/PUT /material-types` | `material_types.go:39-86` | — | nome duplicado = 500; Update sem checar nome vazio |
| `POST/PUT/DELETE …/distribution-rules` | `distribution_rules.go` | goroutine 30 s: `RecategorizeForRule` (período inteiro da campanha, tipo NOVO) / `RecategorizeForCampaign` no delete | PUT substitui tudo. **Update/Delete não conferem se a regra é da campanha da URL**; Update que troca o tipo não recategoriza o tipo antigo |
| `PUT/DELETE …/distribution-overrides/` | `distribution_overrides.go` | `RecategorizeForOverride` **síncrono** (célula-dia, advisory lock) | corpo JSON nos dois; `created_by` nunca preenchido |
| `POST /clients` | `clients.go:122-146` | goroutine `avisarHubDoCliente` (`cliente.upsert`) | CNPJ duplicado = 409 `cnpj_duplicado` |
| `PUT /clients/{id}` | `clients.go:211-254` | — (edição **não** avisa o hub) | substitui os 10 campos; `hub_id` não é editável por rota nenhuma |
| `PUT /clients/{id}/target-pmm` | `client_target_pmm.go:50-79` | — | lote parcial; `null` apaga; **retroativo** (sem versionamento, migration 0054) |
| `PUT /stations/{id}` | `stations.go:179-208` | geocoding síncrono; o reconciler reinicia o worker se `stream_url` mudar | substitui tudo, **exige `stream_url`**, `meta` nil apaga os metadados |
| `POST /admin/notifications/mark-read|mark-all-read` | `admin_notifications.go:71-123` | — | por usuário (`notification_reads`) |

Nenhum desses handlers grava `audit_log` nem lê o ator (exceto notificações). Erros são `http.Error` em texto puro, sem código: os códigos do MCP são definidos aqui (F3-9).

---

## Decisões deste plano (o dono aprova junto com o plano)

| # | Decisão | Por quê |
|---|---|---|
| F3-1 | **`critico` antes da Fatia 4 = barrado, não executado.** Quando a escalada dá `critico`, a tool calcula o preview e devolve `{executou:false, nivel:"critico", motivo, preview, aviso, links:{tela}}` (sem `isError`), grava `mcp.escrita.requer_aprovacao` na auditoria e não escreve nada. `aviso`: "Nada aconteceu. Esta alteração atinge veiculação já ocorrida e precisa de aprovação humana; a aprovação pelo MCP ainda não existe — faça pela tela, logado". A Fatia 4 troca exatamente esse ramo por `criarPendente`. | A spec põe `critico` (e `mcp_acoes_pendentes`) na Fatia 4. Recusar com link é a única forma segura de entregar a Fatia 3 sem `/pendentes` e sem rebaixar o nível. |
| F3-2 | **`Escalar` consulta o banco e roda em TODA chamada** (preview e confirmação). Se entre o preview e a confirmação entrou uma tocada que faz a escrita escalar, a confirmação devolve o ramo `critico` (F3-1) e o token não é consumido. | No hub o `escalarPara` é puro sobre os args; aqui o critério da spec (D6, "atinge dia já veiculado") depende do estado. O token fixa os args, não o estado (contrato §3.5). |
| F3-3 | **"Atinge veiculação já ocorrida" = existe veiculação no MESMO recorte que o gatilho vai recategorizar/afetar.** Um helper só, `catalog.HaVeiculacao(ctx, EscopoVeiculacao)`, com o recorte de `recatScopeByCampaignSQL` (`detection_campaigns × detections × materials`, dia em America/Sao_Paulo) e o mesmo filtro que o recat aplica. Toda tocada está no passado, então "existe tocada no recorte" é o critério; para datas sem tocada ainda usa-se "dia < hoje SP" (`hojeSP(L.agora())`, nunca `time.Local`). | Uma regra só, exata: escala quando o gatilho mexe em número que o cliente já viu, e não escala por mexer num dia passado sem tocada nenhuma. |
| F3-4 | **Escalada por tool** (refina a tabela da spec §6 com o levantamento): **`criar_campanha`** → `critico` se `inicio ≤ hoje SP` (o scheduler a ativa em ≤ 60 s: sobe workers, o cliente passa a ver — é um `iniciar_campanha`). **`editar_campanha`** → `critico` se (a) muda o estado que as datas dão (`programada`/`ativa`/`concluida`, mesma regra do `PromoteScheduledLifecycle`), (b) muda o conjunto de dias < hoje do período, ou (c) muda o código do hub (é ponte). **`editar_emissoras_campanha`** → `critico` se remove emissora com veiculação da campanha (encolher `target_stations` reclassifica histórico — memória `milium-acoustic-twin-misattribution`). **`vincular_material`/`desvincular_material`/`editar_emissoras_material`** → **sem escalada** (diverge da spec: não há recat, o passado não muda; a Task 15 **prova** com teste que grade e insights do passado não mudam — se mudarem, a escalada volta). **`editar_material`** → `critico` só quando muda o **tipo** e o material tem veiculação (recat global). **Regras** → `critico` se a campanha tem veiculação do(s) tipo(s) que o recat varre (criar/editar: tipos novo **e** antigo; apagar: todos) **ou** se a vigência (antiga ou nova, recortada ao período da campanha) contém dia < hoje SP — mudar a meta N de um dia passado muda déficit e "esperado" que o cliente já viu, mesmo sem tocada (achado da revisão do Lote A, 2026-10-29; só torna a regra mais estrita). **Overrides** → `critico` se `for_date < hoje` **ou** a célula-dia tem veiculação. **`editar_pmm_target`** → `critico` se o cliente tem veiculação em alguma das emissoras do lote **ou** tem campanha (não cancelada) com alguma dessas emissoras e início < hoje SP (o PMM é retroativo e o total de target aparece mesmo sem tocada). **`criar_cliente`, `editar_cliente`, `editar_emissora`, tipos de material** → sem escalada. | Spec D6 com o comportamento real de cada gatilho. Consequência que o dono precisa saber: **editar regra/override de campanha em andamento quase sempre escala**, então na Fatia 3 isso continua sendo feito pela tela (F3-1). |
| F3-5 | **Uso único do token: consome ANTES de executar e libera na falha de negócio** (como o hub, `consumirConfirmacao`/`liberarConfirmacao`). `INSERT INTO mcp_confirmacoes_usadas(hash, expira_em) … ON CONFLICT DO NOTHING`; 1 linha = primeiro uso. Falha de negócio (`acoes.Recusa`) → `DELETE` (o token volta a valer); erro interno ou panic → **não** libera (algo pode ter sido gravado). Poda oportunista no consumo: `DELETE … WHERE expira_em < now() - interval '1 hour'` (LIMIT 500). **Diverge da spec §5.2**, que pedia o INSERT "na mesma transação da escrita": nenhuma das funções reaproveitadas aceita `pgx.Tx` (cada uma abre a sua ou usa o pool; o recat abre a própria), e passar uma transação por todas mudaria o comportamento das rotas HTTP. Atualizar §5.2 da spec. | Mesma garantia que o hub dá; risco residual = erro interno depois de escrever deixa o token gasto (a pessoa pede preview de novo). |
| F3-6 | **Migration `0071_mcp_confirmacoes`** (`mcp_confirmacoes_usadas(hash BYTEA PK, usado_em, expira_em)` + índice em `expira_em`). Só `CREATE TABLE`. O `master` já tem a `0070_users_username`: no merge, a `0070_mcp_fundacao` e esta sobem um número cada (já registrado no handoff). | Spec §7. |
| F3-7 | **Camada `internal/acoes`** com `acoes.Servico{Campanhas, MateriaisCampanha, Materiais, Regras, Ajustes, Clientes, Emissoras, Supervisor, Hub, Ligacoes, EmSegundoPlano}`. Funções extraídas (com os handlers passando a chamá-las): `CriarCampanha`, `EditarCampanha`, `TrocarEmissorasCampanha`, `VincularMaterial`/`DesvincularMaterial`/`TrocarEmissorasMaterial` (+ `Reload`), `TrocarTipoMaterial` (+ recat), `CriarRegra`/`EditarRegra`/`ApagarRegra` (+ recat), `DefinirAjuste`/`ApagarAjuste` (+ recat síncrono), `CriarCliente` (+ hub), `EditarCadastroEmissora` (nova, ver F3-10). A barreira do código do hub vira `acoes.ConferirCodigoHub(ctx, cliente, codigo) (canonico string, err error)` que devolve `*Recusa` (o handler traduz para o 422 de hoje). `recordRecatFailure` e `avisarHub*` saem de `handlers` para `acoes`. `EmSegundoPlano` (padrão `go f()`) é injetável para os testes esperarem o recat. Supervisor e hub entram por interface (`SupervisorDeCampanha{UpdateStations, Reload}`, `AvisoHub{Emitir, EmitirComResposta, Configured, ConferirCodigo}`) para os testes usarem falsos que registram as chamadas. | Regra de ouro (spec §3): a tool não reimplementa. Extrair primeiro, com teste de golden do handler antes de mexer. |
| F3-8 | **Dois defeitos do handler corrigidos na extração** (mudam a rota HTTP também, para melhor): (a) `EditarRegra`/`ApagarRegra` exigem que a regra pertença à campanha (senão `nao_encontrado`; hoje apaga a regra de outra campanha e recategoriza a errada); (b) `EditarRegra` que troca o tipo recategoriza o tipo **antigo** também (hoje só o `projrecon` cura, em até 48 h). Os outros defeitos levantados ficam como follow-up e o MCP só **não os herda** por validação de entrada própria (datas da campanha, vínculo de material de outro cliente, link inexistente dando 204). | (a) é perda de dado silenciosa; (b) deixa a cobrança errada por até 48 h. Pequenos e testáveis. **Se o dono preferir não mexer na rota, o MCP faz as duas checagens só do lado dele.** |
| F3-9 | **Códigos de recusa** (as rotas não têm código; a tool define): `entrada_invalida`, `nao_encontrado`, `cnpj_duplicado` (com `existente:{id,nome}` na mensagem), `codigo_hub` (as 4 mensagens do 422), `nome_duplicado` (tipo de material), `campanha_cancelada` (escrita em campanha cancelada é recusada pelo MCP — a tela não recusa, mas o doc `cancelled-campaign-handling.md` a tira das telas operacionais). Recusa de negócio sai como `isError` `"<mensagem> [<código>]"` (contrato §3.4) e libera o token. | Contrato §3.4. |
| F3-10 | **Edição é parcial no MCP, mesmo onde a rota é PUT total.** A tool lê o registro, aplica só os campos enviados e grava pelo mesmo caminho (spec §5.2 "o Executar relê e faz merge"). `editar_emissora` usa `acoes.EditarCadastroEmissora`, que grava por um método novo, `catalog.Stations.UpdateCadastro`, que só toca nome, frequência, cidade, UF, PMM e coordenada (`COALESCE`: campo não enviado fica) — o MCP nunca troca `stream_url` (spec §1 não-objetivos) nem apaga metadados. *(Revisto na execução do Lote B: o plano dizia reler e regravar pelo `Stations.Update`, mas ele reserializa o `metadata` pelo struct — chave desconhecida se perde — e reler-e-devolver desfaria um `stream_url` trocado entre as duas chamadas.)* Campos editáveis: nome, frequência, cidade, UF, PMM. `editar_cliente` não tem `hub_id` (nenhuma rota o grava; a ponte é Fatia 4/5). Listas de emissoras entram como `adicionar`/`remover` (nunca "a lista inteira": um modelo que esquece um id apagaria emissoras). | O modelo erra omitindo campo; PUT total transforma omissão em apagamento. |
| F3-11 | **Auditoria:** toda escrita executada grava `auditoria.Registrar(ator, "mcp.escrita.<tool>", recurso, id, {tool, nivel, args, mcp:{token_id}})`; escrita barrada grava `mcp.escrita.requer_aprovacao`. Feita pelo motor **depois** do sucesso, fora da transação da escrita (F3-5); falha da auditoria é logada e **não** derruba a resposta (a escrita já aconteceu; o modelo repetiria). `args` = argumentos canônicos sem `confirmacao`, com texto longo (roteiro) cortado em 500 runas. O `auditoria_mcp` omite a chave `token_id` (casa `token` no `chaveSecreta`): aceito e documentado. | Spec §4.6. A gravação "dentro da função compartilhada" da spec é para o `critico` executado fora da tool (Fatia 4). |
| F3-12 | **Resposta** — `livre` e `confirma` executado: `{executou:true, resultado}` (o registro depois da escrita, no mesmo formato da tool `obter_*` correspondente, e `ressalvas` quando houver recat assíncrono: "a recategorização roda em segundo plano e leva até 30 s"). Preview: `{resumo, mudancas:[{campo, de, para}], efeitos:[…], ressalvas?}`. Confirmação: `{executou:false, preview, confirmacao, expira_em}`. Token inválido/vencido/args mudados/usado: `{executou:false, erro:"… Peça o preview outra vez."}` (não é erro). Se o resultado não serializar depois de escrito: `{executou:true, resultado:null, aviso}` (nunca erro, senão o modelo repete). | Contrato §3.4/§3.5; hub `sucesso()`. |

**Fora desta fatia:** `critico` executável, `mcp_acoes_pendentes`, `/pendentes`, varredor (Fatia 4); ligar `hub_id` de cliente; upload de áudio/PDF; qualquer escrita da tabela da Fatia 4; frontend.

---

## Antes de começar — leia

- `CLAUDE.md` da raiz: regras 4.8 (migration contra dado de prod — aqui é só `CREATE TABLE`, mas passa pelo `shadow_migration_test`), 6.1 (cross-compile), 6.5 (boot), 6.7 (nenhum CLI novo nesta fatia).
- **Árvore compartilhada.** Só o worktree `C:\tmp\emonitor-mcp`, branch `feat/mcp-server`. Nunca `git checkout`/`switch` na pasta principal. Nunca `git stash` sem tag. Nada de push.
- **Banco de teste:** container `rc-mcp-test-pg` (porta 55440). O template `radiocheck_mcp_test_d2` está migrado até a 0070; a Task 1 cria o `radiocheck_mcp_test_d3` (até a 0071). Um banco por agente, criado do template. `export TEST_DATABASE_URL=…` em TODA chamada. `go test -p 1`. `./internal/mcp/...` antes de `./internal/catalog/` (recriar o banco entre os dois). `./internal/mcp` leva ~9 min (timeout 600000 ms).
- **Padrões das Fatias 1–2** (leia o código antes de escrever): `ferramentaLeitura`, `ok`/`recusa`/`recusaEntrada`/`naoEncontrado`, `falhaInterna`, `ref`/`refDe`, `lerCampanha`, `hojeSP`, `linkTela`, `ressalvaCancelada`; nos testes `mcptest.DBCatalogo` + fixtures, `chamarTool`, `chamarToolErro`, `chamarHandler`, `cfgTeste`.
- **`ToolsEsperadas` em ORDEM ALFABÉTICA**, travada por `TestToolsEsperadas_OrdemAlfabeticaE44` → passa a 64 na Task 18. Toda task que cria tool acrescenta o nome na posição certa no mesmo commit (o teste de contagem é atualizado só na Task 18; até lá ajuste o número a cada task).
- **Refactor de handler:** escreva PRIMEIRO o teste do handler que trava o comportamento atual (status, corpo, linha no banco, chamadas ao falso de supervisor/hub), veja passar no código velho, extraia, veja passar de novo. Commit separado da tool.
- **CRLF:** `gofmt -l` só nos arquivos tocados.

## Mapa de arquivos

| Arquivo | Responsabilidade |
|---|---|
| `migrations/0071_mcp_confirmacoes.{up,down}.sql` | (cria) tabela de uso único |
| `workers/internal/mcp/confirmacao.go` (+`_test`) | (cria) `argsCanonicos`, `assinarConfirmacao`, `verificarConfirmacao`, `Confirmacoes.Consumir/Liberar` |
| `workers/internal/mcp/escrita.go` (+`_test`) | (cria) `Nivel`, `Escalada`, `Escrita[In]`, `ferramentaEscrita`, `previa`, ramo `critico` barrado, auditoria |
| `workers/internal/mcp/escritas.go` | (cria) `Escritas` (deps das tools de escrita: `Acoes`, `Confirmacoes`, `Segredo`, `Pool`) |
| `workers/internal/mcp/servidor.go`, `modulo.go`, `workers/cmd/api/main.go` | (modifica) `ConfigServidor.Escritas`, `DepsModulo.{Supervisor,Hub,Ligacoes}`, registro das famílias |
| `workers/internal/catalog/ha_veiculacao.go` (+`_test`) | (cria) `EscopoVeiculacao`, `HaVeiculacao` |
| `workers/internal/acoes/*.go` (+`_test`) | (cria) `Servico`, `Recusa`, interfaces, `campanhas.go`, `materiais.go`, `regras.go`, `clientes.go`, `emissoras.go`, `recat.go`, `hub.go` |
| `workers/internal/api/handlers/{campaigns,campaign_materials,materials,distribution_rules,distribution_overrides,clients,recat_failures}.go` | (modifica) chamam `acoes` |
| `workers/internal/mcp/tools_escrita_{basicas,cadastros,campanhas,materiais,regras}.go` (+`_test`) | (cria) as 20 tools |
| `workers/internal/mcp/mcptest/escrita.go` | (cria) `SupervisorFalso`, `HubFalso`, `Principal`, `ConfirmarEExecutar` |
| `workers/internal/mcp/instrucoes.go` | (modifica) parágrafo do `critico` barrado |
| `docs/features/mcp.md`, `docs/operations/mcp.md`, `docs/README.md`, spec §5.2 | docs |
| `docs/superpowers/HANDOFF-AAAA-MM-DD-mcp-fatia-4.md` | handoff |

**Lotes:** A = Tasks 0–6 (ambiente, migration, token, consumo, motor, `HaVeiculacao`, deps); B = Tasks 7–10 (extrações para `acoes` + refactor dos handlers); C = Tasks 11–17 (as 20 tools); D = Tasks 18–21 (instruções/inventário/desempenho, docs, handoff/memória, verificação). Revisão de aderência + qualidade ao fim de cada lote; revisão final da branch no fim do D. Tasks que mexem em `servidor.go`/`ToolsEsperadas` rodam em sequência.

---

### Task 0: Ambiente

- [ ] **Step 1:** `cd C:/tmp/emonitor-mcp && git branch --show-current && git status --short` → `feat/mcp-server`, limpo.
- [ ] **Step 2:** `docker ps --filter name=rc-mcp-test-pg --format '{{.Names}} {{.Status}}'` → `Up`. Se parado: `docker start rc-mcp-test-pg`.
- [ ] **Step 3:** Banco de trabalho do template atual e linha de base:
```bash
docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -c "CREATE DATABASE radiocheck_mcp_test_f3 TEMPLATE radiocheck_mcp_test_d2;"
cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f3?sslmode=disable" && go build ./... && go test -p 1 ./internal/mcp/... 2>&1 | tail -6
```
Expected: `ok` em todos (rodar em segundo plano se passar de 10 min).

---

### Task 1: Migration `0071_mcp_confirmacoes` e template `d3`

**Files:** Create `migrations/0071_mcp_confirmacoes.up.sql`, `.down.sql`

- [ ] **Step 1: up**
```sql
-- Uso único do token de confirmação do MCP (spec §5.2, plano Fatia 3 F3-5).
-- O token é HMAC sem estado; esta tabela guarda o SHA-256 dos já usados até
-- a expiração. Consumir = INSERT ON CONFLICT DO NOTHING; falha de negócio
-- apaga a linha (o token volta a valer).
CREATE TABLE mcp_confirmacoes_usadas (
    hash      BYTEA       PRIMARY KEY CHECK (octet_length(hash) = 32),
    usado_em  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expira_em TIMESTAMPTZ NOT NULL
);
CREATE INDEX mcp_confirmacoes_usadas_expira ON mcp_confirmacoes_usadas (expira_em);
```
- [ ] **Step 2: down** — `DROP TABLE IF EXISTS mcp_confirmacoes_usadas;`
- [ ] **Step 3:** Aplicar no template novo: criar `radiocheck_mcp_test_d3` a partir do `d2` e rodar `migrate up` nele (mesmo comando de `docs/operations/mcp.md` "Ambiente de teste"); conferir `SELECT version, dirty FROM schema_migrations` = `71|f`; conferir `down` + `up` num banco descartável.
- [ ] **Step 4:** Atualizar a seção "Ambiente de teste" de `docs/operations/mcp.md` (template = `d3`). Commit `feat(mcp): migration 0071 — uso único da confirmação`.

---

### Task 2: Token de confirmação e argumentos canônicos

**Files:** Create `workers/internal/mcp/confirmacao.go`, `confirmacao_test.go`

- [ ] **Step 1: Testes que falham**

```go
package mcp

func TestArgsCanonicos_OrdemDeChaveNaoImportaEConfirmacaoSai(t *testing.T) {
	type in struct {
		B           int    `json:"b"`
		A           string `json:"a"`
		Confirmacao string `json:"confirmacao,omitempty"`
	}
	x, err := argsCanonicos(in{B: 1, A: "z", Confirmacao: "tok"})
	require.NoError(t, err)
	require.Equal(t, `{"a":"z","b":1}`, string(x))
	y, _ := argsCanonicos(map[string]any{"b": 1, "a": "z"})
	require.Equal(t, string(x), string(y))
}

func TestConfirmacao_VerificaSoComMesmosArgsUsuarioEPrazo(t *testing.T) {
	seg := bytes.Repeat([]byte("s"), 32)
	u := uuid.New()
	agora := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	tok := assinarConfirmacao(seg, "editar_material", []byte(`{"a":1}`), u, agora.Add(ttlConfirmacao))
	require.Regexp(t, `^[1-9][0-9]{0,15}\.[A-Za-z0-9_-]{43}$`, tok)
	require.True(t, verificarConfirmacao(seg, tok, "editar_material", []byte(`{"a":1}`), u, agora))
	require.False(t, verificarConfirmacao(seg, tok, "editar_material", []byte(`{"a":2}`), u, agora), "args mudados")
	require.False(t, verificarConfirmacao(seg, tok, "editar_cliente", []byte(`{"a":1}`), u, agora), "outra tool")
	require.False(t, verificarConfirmacao(seg, tok, "editar_material", []byte(`{"a":1}`), uuid.New(), agora), "outro usuário")
	require.False(t, verificarConfirmacao(seg, tok, "editar_material", []byte(`{"a":1}`), u, agora.Add(11*time.Minute)), "vencido")
	require.False(t, verificarConfirmacao(bytes.Repeat([]byte("x"), 32), tok, "editar_material", []byte(`{"a":1}`), u, agora), "outro segredo")
	for _, ruim := range []string{"", "x", "0" + tok, "+" + tok, " " + tok, tok + "=", strings.Replace(tok, ".", "", 1)} {
		require.False(t, verificarConfirmacao(seg, ruim, "editar_material", []byte(`{"a":1}`), u, agora), ruim)
	}
	longe := assinarConfirmacao(seg, "editar_material", []byte(`{"a":1}`), u, agora.Add(time.Hour))
	require.False(t, verificarConfirmacao(seg, longe, "editar_material", []byte(`{"a":1}`), u, agora), "exp além do TTL+folga")
}
```

- [ ] **Step 2:** Rode e veja falhar (compilação).
- [ ] **Step 3: Implemente**

```go
package mcp

// Token do nível confirma (spec §5.2; transplante de hub
// backend/src/mcp/write/confirmacao.ts): HMAC-SHA256 com o JWT_SECRET, com
// separação de domínio, sobre [tool, args canônicos, usuário, exp]. Os ARGS
// entram na assinatura: o que se executa é o que o preview mostrou. Forma
// canônica "<exp_ms>.<mac43>" — sem ela, "01…"/"+1…" dariam hashes de uso
// único diferentes para a mesma confirmação.

const (
	ttlConfirmacao   = 10 * time.Minute
	folgaConfirmacao = time.Minute
)

var formaDoToken = regexp.MustCompile(`^([1-9][0-9]{0,15})\.([A-Za-z0-9_-]{43})$`)

// argsCanonicos: JSON com chaves ordenadas em todo nível (map do Go serializa
// ordenado), sem a chave "confirmacao". Números passam como json.Number para
// não perder precisão nem mudar de forma entre as duas chamadas.
func argsCanonicos(in any) ([]byte, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("mcp: args precisam ser objeto JSON: %w", err)
	}
	delete(m, "confirmacao")
	return json.Marshal(m)
}

func macConfirmacao(segredo []byte, tool string, args []byte, user uuid.UUID, expMs int64) string {
	payload, _ := json.Marshal([]any{tool, json.RawMessage(args), user.String(), expMs})
	h := hmac.New(sha256.New, segredo)
	h.Write([]byte("mcp-confirmacao."))
	h.Write(payload)
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func assinarConfirmacao(segredo []byte, tool string, args []byte, user uuid.UUID, exp time.Time) string {
	expMs := exp.UnixMilli()
	return strconv.FormatInt(expMs, 10) + "." + macConfirmacao(segredo, tool, args, user, expMs)
}

// verificarConfirmacao nunca entra em pânico: qualquer coisa fora do esperado é false.
func verificarConfirmacao(segredo []byte, token, tool string, args []byte, user uuid.UUID, agora time.Time) bool {
	m := formaDoToken.FindStringSubmatch(token)
	if m == nil {
		return false
	}
	expMs, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return false
	}
	exp := time.UnixMilli(expMs)
	if exp.Before(agora) || exp.After(agora.Add(ttlConfirmacao+folgaConfirmacao)) {
		return false
	}
	return hmac.Equal([]byte(m[2]), []byte(macConfirmacao(segredo, tool, args, user, expMs)))
}

func hashDoToken(token string) []byte { s := sha256.Sum256([]byte(token)); return s[:] }
```

- [ ] **Step 4:** Rode e veja passar. Commit `feat(mcp): token de confirmação HMAC com args canônicos`.

---

### Task 3: Consumo de uso único

**Files:** Modify `confirmacao.go`, `confirmacao_test.go`

- [ ] **Step 1: Testes que falham** (banco): `Consumir` devolve `true` na 1ª vez e `false` na 2ª; `Liberar` faz a 3ª voltar a `true`; linhas com `expira_em` de 2 h atrás somem no próximo `Consumir`; token fora da forma → `false` sem tocar o banco.
- [ ] **Step 2: Implemente**

```go
// Confirmacoes marca o uso único (F3-5): consome ANTES de executar; só a falha
// de negócio libera. Erro interno não libera — a escrita pode ter gravado algo.
type Confirmacoes struct{ DB *pgxpool.Pool }

func (c *Confirmacoes) Consumir(ctx context.Context, token string, exp time.Time) (bool, error) {
	if !formaDoToken.MatchString(token) {
		return false, nil
	}
	// Poda oportunista: sem varredor nesta fatia (o da Fatia 4 assume).
	if _, err := c.DB.Exec(ctx, `DELETE FROM mcp_confirmacoes_usadas WHERE hash IN (
		SELECT hash FROM mcp_confirmacoes_usadas WHERE expira_em < now() - interval '1 hour' LIMIT 500)`); err != nil {
		return false, err
	}
	tag, err := c.DB.Exec(ctx, `INSERT INTO mcp_confirmacoes_usadas (hash, expira_em) VALUES ($1, $2)
		ON CONFLICT (hash) DO NOTHING`, hashDoToken(token), exp)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (c *Confirmacoes) Liberar(ctx context.Context, token string) error {
	_, err := c.DB.Exec(ctx, `DELETE FROM mcp_confirmacoes_usadas WHERE hash = $1`, hashDoToken(token))
	return err
}
```
(`exp` vem do próprio token já verificado; `expiraDoToken(token)` lê o prefixo.)
- [ ] **Step 3:** Passa. Commit `feat(mcp): uso único da confirmação`.

---

### Task 4: Motor de níveis

**Files:** Create `escrita.go`, `escritas.go`, `escrita_test.go`; Modify `servidor.go` (`ConfigServidor.Escritas *Escritas`)

- [ ] **Step 1: Tipos**

```go
type Nivel int

const (
	Livre Nivel = iota
	Confirma
	Critico
)

func (n Nivel) String() string { return [...]string{"livre", "confirma", "critico"}[n] }

// Escalada: o nível que os ARGUMENTOS e o ESTADO pedem, e por quê (o motivo
// vai para a resposta barrada e para a auditoria). Nunca desce do declarado.
type Escalada struct {
	Nivel  Nivel
	Motivo string
}

// previa é o que a pessoa lê antes de aprovar.
type previa struct {
	Resumo    string    `json:"resumo"`
	Mudancas  []mudanca `json:"mudancas,omitempty"`
	Efeitos   []string  `json:"efeitos,omitempty"`
	Ressalvas []string  `json:"ressalvas,omitempty"`
}

type mudanca struct {
	Campo string `json:"campo"`
	De    any    `json:"de"`
	Para  any    `json:"para"`
}

// feito: o que o Executar devolve — o resultado e o alvo da auditoria.
type feito struct {
	Resultado any
	Recurso   string     // "campaign", "material", …
	RecursoID *uuid.UUID
}

// Escrita é DADO (spec §5.2): o mesmo valor registra a tool e, na Fatia 4,
// executará a pendente aprovada. Recusa de negócio = *acoes.Recusa (libera o
// token); qualquer outro erro = falha interna (não libera).
type Escrita[In any] struct {
	Nome, Descricao string
	Nivel           Nivel
	Escalar         func(ctx context.Context, E *Escritas, in In) (Escalada, error) // nil = nunca escala
	Preview         func(ctx context.Context, E *Escritas, in In) (previa, error)
	Executar        func(ctx context.Context, E *Escritas, p *Principal, in In) (feito, error)
	Tela            func(E *Escritas, in In) *string // link da tela para o ramo crítico barrado
}
```

`CampoConfirmacao` (embutido no `In` de toda tool `confirma` ou escalável):
```go
type CampoConfirmacao struct {
	Confirmacao string `json:"confirmacao,omitempty" jsonschema:"O token devolvido pelo preview. Só vale com os MESMOS argumentos, por 10 min. Nunca o reenvie sem a pessoa aprovar o preview."`
}
func (c CampoConfirmacao) confirmacao() string { return c.Confirmacao }
```

- [ ] **Step 2: Testes do motor** (com uma `Escrita` de teste registrada num servidor de teste; `Executar` conta chamadas):
  1. `livre` executa na 1ª chamada: `{executou:true, resultado}` e 1 linha `mcp.escrita.<tool>` no `audit_log` com `metadata.mcp.token_id = principal.TokenID` e `origem=mcp`.
  2. `confirma` sem token: `executou:false`, `preview`, `confirmacao`, `expira_em`; `Executar` NÃO chamado; nenhuma linha de auditoria.
  3. com o token e os mesmos args: executa uma vez; auditoria gravada.
  4. **reuso** do mesmo token: `{executou:false, erro:"…já foi usada…Peça o preview outra vez."}`, sem `isError`; `Executar` chamado 1 vez no total.
  5. **args mudados**, **token de outro usuário**, **vencido** (`Agora` injetado +11 min), **lixo**: `{executou:false, erro:"…inválida ou expirada…"}`.
  6. **falha de negócio** (`Executar` devolve `&acoes.Recusa{…}`): `isError` com `[codigo]`; o MESMO token funciona na chamada seguinte.
  7. **erro interno** e **panic** no `Executar`: `isError [interno]`; o token NÃO volta a valer; o processo não cai.
  8. **escalada na 2ª chamada**: preview com `Escalar` = confirma; antes da confirmação o falso passa a devolver `Critico` → resposta do ramo barrado, `Executar` não chamado, token não consumido (`SELECT count(*) FROM mcp_confirmacoes_usadas` = 0).
  9. **ramo crítico**: `{executou:false, nivel:"critico", motivo, preview, aviso, links:{tela}}`, sem `isError`; 1 linha `mcp.escrita.requer_aprovacao`.
  10. **auditoria fora do ar** (tabela renomeada na transação de teste ou `Execer` falso): escrita executada devolve `executou:true` mesmo assim.
  11. `Escritas == nil` → `isError [interno]` antes de qualquer coisa.
  12. Descrição registrada termina com `Nível: confirma.`; anotações: `livre` sem `Escalar` → `DestructiveHint=false`, demais `true`; `ReadOnlyHint=false`, `OpenWorldHint=false`.

- [ ] **Step 3: Implemente `ferramentaEscrita`** — fluxo (mesma ordem do hub `aplicarNivel`):

```go
func ferramentaEscrita[In any](s *sdk.Server, cfg ConfigServidor, p *Principal, d Escrita[In]) {
	destrutiva := d.Nivel != Livre || d.Escalar != nil
	sdk.AddTool(s, &sdk.Tool{Name: d.Nome, Description: d.Descricao + " Nível: " + d.Nivel.String() + ".",
		Annotations: anotacoesEscrita(destrutiva)},
		func(ctx context.Context, _ *sdk.CallToolRequest, in In) (res *sdk.CallToolResult, out any, err error) {
			defer func() { if r := recover(); r != nil { res, out, err = falhaInterna(…panic…) } }()
			E := cfg.Escritas
			if E == nil { return falhaInterna(cfg.log(), d.Nome, errors.New("mcp: Escritas não configuradas")) }
			args, err := argsCanonicos(in)
			// 1. nível efetivo (roda SEMPRE — F3-2)
			nivel, motivo := d.Nivel, ""
			if d.Escalar != nil {
				esc, err := d.Escalar(ctx, E, in)
				if err != nil { return comoFalha(cfg, d.Nome, err) }
				if esc.Nivel > nivel { nivel, motivo = esc.Nivel, esc.Motivo }
			}
			// 2. livre: executa
			if nivel == Livre { return executarEAuditar(ctx, cfg, E, p, d, in, args, nivel, "") }
			// 3. preview (falha de negócio para tudo antes do token)
			pv, err := d.Preview(ctx, E, in)
			if err != nil { return comoFalha(cfg, d.Nome, err) }
			// 4. crítico: barrado até a Fatia 4 (F3-1)
			if nivel == Critico { auditar(…"mcp.escrita.requer_aprovacao"…); return resultado(barrado{…}) }
			// 5. confirma
			tok := confirmacaoDe(in) // "" se o In não embute CampoConfirmacao
			agora := E.agora()
			if tok == "" { return resultado(pedido{Executou:false, Preview:pv, Confirmacao: assinar(…agora+TTL…), ExpiraEm: …}) }
			if !verificarConfirmacao(E.Segredo, tok, d.Nome, args, p.UserID, agora) { return resultado(semEfeito{erro: msgInvalida}) }
			okUso, err := E.Confirmacoes.Consumir(ctx, tok, expiraDoToken(tok))
			if err != nil { return falhaInterna(…) }
			if !okUso { return resultado(semEfeito{erro: msgUsada}) }
			return executarEAuditar(ctx, cfg, E, p, d, in, args, nivel, tok) // Recusa → Liberar(tok)
		})
}
```
`comoFalha`: `errors.As(err, &*acoes.Recusa)` → `falha(r.Mensagem, r.Codigo)`; senão `falhaInterna`. `executarEAuditar` aplica F3-11/F3-12 (auditoria com `context.WithoutCancel`, resultado não serializável → `executou:true, resultado:null, aviso`). Mensagens fixas: `msgInvalida = "Confirmação inválida ou expirada, ou os argumentos mudaram. Peça o preview outra vez."`, `msgUsada = "Esta confirmação já foi usada. Peça o preview outra vez se quiser repetir."`.

- [ ] **Step 4:** `Escritas` (`escritas.go`): `{Acoes *acoes.Servico; Confirmacoes *Confirmacoes; Segredo []byte; Leitura *Leitura; Agora func() time.Time}`; `NovasEscritas(pool, segredo, servico, leitura)`. Escritas usam a `Leitura` para montar previews e resultados (mesmo formato das tools `obter_*`).
- [ ] **Step 5:** Testes verdes. Commit `feat(mcp): motor de níveis da escrita (livre/confirma, crítico barrado)`.

---

### Task 5: `catalog.HaVeiculacao`

**Files:** Create `workers/internal/catalog/ha_veiculacao.go`, `_test.go`

- [ ] **Step 1:** Leia `recatScopeByCampaignSQL` (`distribution_rules.go:630`) e o filtro que `recatClassifiedCTE` (`:292`) aplica às linhas do escopo; o EXISTS usa **o mesmo** recorte e filtro (se o recat considera só aprovadas, aqui também).
- [ ] **Step 2: Testes** (uma fixture por campo): campanha sem tocada → false; com tocada → true; filtro por tipo (tocada de outro tipo → false), por emissoras, por material (sem campanha: todas as campanhas do material), por cliente (via `campaigns.client_id`), por dia SP (tocada 23:30 SP do dia D conta em D, não em D+1 — fixture com `mcptest.Quando`); tocada ignorada/retratada segue a regra do recat.
- [ ] **Step 3: Implemente**

```go
// EscopoVeiculacao: o recorte de uma escrita do MCP para a escalada a crítico
// (plano Fatia 3, F3-3). Campos nil/vazios = sem filtro. Pelo menos um de
// CampanhaIDs, MaterialID ou ClienteID é obrigatório.
type EscopoVeiculacao struct {
	CampanhaIDs []uuid.UUID
	ClienteID   *uuid.UUID
	MaterialID  *uuid.UUID
	TipoIDs     []uuid.UUID
	EmissoraIDs []uuid.UUID
	DiaDe       *time.Time // data de calendário (meia-noite UTC), em SP
	DiaAte      *time.Time
}

// HaVeiculacao diz se existe veiculação no recorte — o MESMO de
// recatScopeByCampaignSQL: se esta função diz false, o recat não toca em
// nenhuma tocada.
func (d *Detections) HaVeiculacao(ctx context.Context, e EscopoVeiculacao) (bool, error)
```
- [ ] **Step 4:** Commit `feat(catalog): HaVeiculacao — recorte do recat para a escalada do MCP`.

---

### Task 6: Dependências da escrita

**Files:** Create `workers/internal/acoes/servico.go`, `recusa.go`; Modify `mcp/modulo.go`, `cmd/api/main.go`

- [ ] **Step 1:** `acoes.Recusa{Mensagem, Codigo string}` (implementa `error`); `acoes.Servico` com os repositórios de F3-7 e as interfaces:

```go
type SupervisorDeCampanha interface {
	UpdateStations(campaignID uuid.UUID, stations []uuid.UUID) error
	Reload(campaignID uuid.UUID) error
}
type AvisoHub interface { /* o subconjunto de *hub.Client que campanhas/clientes usam hoje — leia os call sites */ }
```
`EmSegundoPlano func(func())` (nil = `go f()`).
- [ ] **Step 2:** `DepsModulo` ganha `Supervisor acoes.SupervisorDeCampanha` (pode ser nil: mesma regra do handler — sem supervisor, só banco), `Hub *hub.Client`, `Ligacoes *catalog.Ligacoes`. `NovoModulo` monta `acoes.Servico` e `Escritas`. `main.go` passa `sup`, o `hub.Client` e `Ligacoes` que já existem (`main.go:555-561, 595, 693-701`).
- [ ] **Step 3:** Teste de ligação `DepsModulo → Escritas` no padrão do teste de `Leitura` já existente. `CGO_ENABLED=0 GOOS=linux go build ./...`. Commit `feat(mcp): dependências da escrita (acoes.Servico)`.

---

### Task 7: Extração — regras e overrides

**Files:** Create `acoes/regras.go`, `acoes/recat.go` (+`_test`); Modify `handlers/distribution_rules.go`, `distribution_overrides.go`, `recat_failures.go`

- [ ] **Step 1: Trava do handler (antes de mexer):** testes de `DistributionRulesHandler` Create/Update/Delete e `DistributionOverridesHandler` Upsert/Delete: status, linha no banco, e categoria recalculada (com `EmSegundoPlano` síncrono via campo novo no handler, ou esperando com `require.Eventually` no código velho). Veja passar.
- [ ] **Step 2:** `acoes.CriarRegra/EditarRegra/ApagarRegra(ctx, campanha, …)` e `DefinirAjuste/ApagarAjuste`: validação que hoje está no handler (datas, `HH:MM`, ordem das horas) + **validações novas como `Recusa{entrada_invalida}`** (mask 1–127, `plays` 1–100 na regra e ≥ 0 no override, `fim ≥ inicio`, hora final > inicial, `type_id` existente) — o handler continua devolvendo 400/500 como hoje para entradas que já passavam (mapeie `Recusa` para 400 só onde hoje é 400; onde hoje é 500 por CHECK do banco, 400 é melhora aceita). F3-8 (a) e (b). Recat: `recatRegra` em `EmSegundoPlano` com `context.WithTimeout(context.Background(), 30*time.Second)` e `RegistrarFalhaRecat(origem, err)` (movido de `handlers`); override síncrono.
- [ ] **Step 3:** Handlers passam a chamar `acoes`. Testes da Step 1 verdes + testes novos de F3-8. Commit `refactor(acoes): regras e overrides saem do handler` e `fix(regras): editar/apagar exigem a regra da campanha; troca de tipo recategoriza o antigo`.

---

### Task 8: Extração — campanhas

**Files:** Create `acoes/campanhas.go`, `acoes/hub.go` (+`_test`); Modify `handlers/campaigns.go`

- [ ] **Step 1: Trava do handler:** Create (201, linha, `HubFalso` recebeu `campanha.upsert`), as 4 recusas 422 do código do hub (com `HubFalso` programado), Update (200, campos, hub só quando o código mudou, 404), UpdateStations (204; `SupervisorFalso.UpdateStations` chamado com a lista; fallback sem supervisor).
- [ ] **Step 2:** `acoes.ConferirCodigoHub(ctx, cliente, codigo) (string, error)` — mesma lógica de `conferirCodigoOuRecusar`, devolvendo `*Recusa{codigo_hub}` com as mesmas mensagens; erros de hub "seguem" como hoje (log). `CriarCampanha(ctx, CriarCampanhaEntrada)`, `EditarCampanha(ctx, id, EditarCampanhaEntrada)` (mesma ordem: Get → barreira → UpdateBasic → AtualizarHubCode → aviso), `TrocarEmissorasCampanha(ctx, id, lista)` (supervisor ou fallback). Handler traduz `Recusa` para o status de hoje.
- [ ] **Step 3:** Verde. Commit `refactor(acoes): escrita de campanha sai do handler`.

---

### Task 9: Extração — materiais

**Files:** Create `acoes/materiais.go` (+`_test`); Modify `handlers/campaign_materials.go`, `materials.go`

- [ ] **Step 1: Trava:** Link (201, upsert de estações), UpdateStations, Unlink — `SupervisorFalso.Reload(campanha)` chamado uma vez; UpdateType dispara `RecategorizeForMaterial` (categoria muda numa fixture com regra por tipo).
- [ ] **Step 2:** `VincularMaterial`, `TrocarEmissorasMaterial`, `DesvincularMaterial` (+ `reload` com log como `reloadOrLog`), `TrocarTipoMaterial` (+ recat em `EmSegundoPlano`). Commit `refactor(acoes): vínculo e tipo de material saem do handler`.

---

### Task 10: Extração — cliente e emissora

**Files:** Create `acoes/clientes.go`, `acoes/emissoras.go` (+`_test`); Modify `handlers/clients.go`

- [ ] **Step 1: Trava:** Create (201, `cliente.upsert` no `HubFalso`, 409 `cnpj_duplicado` com `existente`).
- [ ] **Step 2:** `acoes.CriarCliente` (validação de nome, `target_label` ≤ 200 runas, CNPJ, `Create`, aviso ao hub) e `acoes.ChecarCNPJ` (usada também pelo `editar_cliente` do MCP). `acoes.EditarCadastroEmissora(ctx, id, CadastroEmissora{Nome, FrequenciaMHz, Cidade, UF, PMM *…})`: `Stations.Get` → merge dos campos enviados → `Stations.Update` com `StreamURL` e `Meta` **lidos do banco** (F3-10). Teste: `stream_url` e um `meta` com chave desconhecida sobrevivem; geocoding roda quando a cidade muda. **Não** mexer no handler `Stations.Update` (a tela continua PUT total).
- [ ] **Step 3:** Commit `refactor(acoes): criar cliente e cadastro de emissora`.

---

### Tasks 11–17: as 20 tools

**Padrão de toda tool de escrita** (cada task cria `tools_escrita_<familia>.go` + `_test.go` e acrescenta os nomes em `ToolsEsperadas`):

- `In` com `jsonschema` em português; ids como string UUID; datas `YYYY-MM-DD`; `CampoConfirmacao` embutido se `confirma` ou escalável.
- Descrição: o que faz, quando usar, a armadilha, quando escala ("Sobe para crítico quando …; nesse caso nada é feito e a resposta traz o link da tela"), "nunca invente ids, pergunte". O motor acrescenta `Nível: …`.
- `Preview` relê o estado, valida (recusas de F3-9) e lista `mudancas` (de → para, com nomes, não só ids) e `efeitos` (ex.: "recategoriza as veiculações do tipo Spot 30s da campanha inteira", "a campanha passa a ser monitorada nas emissoras X e Y agora").
- `Executar` chama `acoes`/o repositório e devolve o registro no formato da tool `obter_*`/`listar_*` correspondente.
- **Testes obrigatórios por tool:** (1) preview não escreve (estado igual antes/depois); (2) confirmar executa; (3) **paridade de efeito**: mesmo estado semeado em duas fixtures, uma pela rota HTTP (`chamarHandler` com método/corpo — generalize o helper para `chamarHandlerCorpo(t, h, metodo, alvo, corpo, rota, admin)` aceitando 200/201/204) e outra pela tool; compara as linhas gravadas e as chamadas dos falsos de supervisor/hub; para recat, compara `detection_campaigns.category`; (4) cada caso de escalada (e o caso vizinho que NÃO escala); (5) cada recusa de F3-9 que a tool tem; (6) uma linha de auditoria com o recurso certo.
- Helper de teste `mcptest.ConfirmarEExecutar(t, cfg, nome, args)`: chama sem token, lê `confirmacao`, chama de novo com ele.

| Task | Tools | Nível / escala (F3-4) | Chama |
|---|---|---|---|
| **11** | `marcar_notificacoes_lidas` (`chaves[]` ≤ 200 **ou** `todas:true`, exclusivos; devolve `marcadas`) · `reconhecer_similaridade_material` (`material_id`) | `livre`, sem escalada | `Notifications.MarkRead`/`MarkAllReadInWindow` com `p.UserID`; `Materials.Acknowledge` (material inexistente → `nao_encontrado`, diferente da rota) |
| **12** | `criar_tipo_material` (`nome`, `cor?` `#rrggbb`, `descricao?`) · `editar_tipo_material` (parcial) · `editar_material` (`titulo?`, `tipo_id?`, `roteiro?` — `""` limpa o roteiro; ao menos um) | `confirma`; `editar_material` escala só quando `tipo_id` muda e `HaVeiculacao{MaterialID}` | `MaterialTypes.Create/Update` (unique → `nome_duplicado`); `Materials.UpdateTitle`/`UpdateScript`, `acoes.TrocarTipoMaterial` |
| **13** | `criar_cliente` · `editar_cliente` (parcial; sem `hub_id`) · `editar_pmm_target` (`cliente_id`, `entradas[{emissora_id, pmm_target: int\|null}]` ≤ 500; null remove) · `editar_emissora` (`nome?`, `frequencia_mhz?`, `cidade?`, `uf?`, `pmm?`) | `confirma`; `editar_pmm_target` escala com `HaVeiculacao{ClienteID, EmissoraIDs}` ou campanha iniciada (F3-4). **Antes de fechar `editar_emissora` sem escalada, verifique se `stations.pmm` entra em algum número de relatório** (grep nos SQL de impactos); se entrar, escala com `HaVeiculacao{EmissoraIDs:[id]}` e registre | `acoes.CriarCliente`; `acoes.ChecarCNPJ` + `Clients.Update` com merge; `ClientStationPMM.BulkUpsert` (cliente/emissora inexistente → `nao_encontrado` antes do banco); `acoes.EditarCadastroEmissora` + ressalva "classe/raio Anatel não são recalculados" quando dial/cidade mudam |
| **14** | `criar_campanha` (`cliente_id`, `nome`, `inicio`, `fim`, `emissora_ids[]?`, `codigo_hub?`) · `editar_campanha` (`campanha_id`, `nome?`, `inicio?`, `fim?`, `codigo_hub?` — `""` desliga) · `editar_emissoras_campanha` (`campanha_id`, `adicionar[]`, `remover[]`) | `confirma`; escalas de F3-4 (ciclo de vida por data reproduzindo a regra do `PromoteScheduledLifecycle` numa função pura testada contra o SQL dele; dias passados; código do hub; emissora removida com `HaVeiculacao{CampanhaIDs, EmissoraIDs: removidas}`). Cancelada → `campanha_cancelada` | `acoes.CriarCampanha` (resultado traz `estado: "programada"` e ressalva "é ativada automaticamente na data de início"), `EditarCampanha`, `TrocarEmissorasCampanha` |
| **15** | `vincular_material` (`campanha_id`, `material_id`, `emissora_ids[]?` — vazio = todas da campanha) · `desvincular_material` · `editar_emissoras_material` (`adicionar[]`/`remover[]`) | `confirma`, **sem escalada**. **Teste de prova F3-4:** semeia tocadas passadas, desvincula, e compara `grade_campanha` e `insights_campanha` antes/depois — se mudar, pare e volte ao dono | `acoes.VincularMaterial` etc. Validação própria: material de outro cliente → `entrada_invalida`; emissora fora da campanha → `entrada_invalida`; vínculo inexistente → `nao_encontrado` |
| **16** | `criar_regra_distribuicao` · `editar_regra_distribuicao` (parcial) · `apagar_regra_distribuicao` | `confirma`; escala com `HaVeiculacao{CampanhaIDs, TipoIDs: {novo, antigo}}` / apagar: `{CampanhaIDs}`, **ou** vigência antiga/nova (∩ campanha) com dia < hoje SP | `acoes.CriarRegra/EditarRegra/ApagarRegra`; `mask` recebido como lista de dias (`["seg","ter",…]`) e convertido — o modelo erra bitmask; ressalva do recat assíncrono |
| **17** | `definir_override` (`campanha_id`, `tipo_id`, `emissora_id`, `data`, `inserções`, `inicio?`, `fim?` HH:MM, `motivo?`) · `apagar_override` | `confirma`; escala se `data < hoje SP` ou `HaVeiculacao{CampanhaIDs, TipoIDs, EmissoraIDs, DiaDe=DiaAte=data}` | `acoes.DefinirAjuste/ApagarAjuste` (recat síncrono: o resultado já traz a célula recalculada via `plano_do_dia`) |

Cada linha da tabela = uma task com Steps: testes que falham → implementação → verde → `gofmt -l` nos arquivos tocados → commit `feat(mcp): <tools>`.

---

### Task 18: Instruções, inventário e desempenho

- [ ] **Step 1:** `Instrucoes`: acrescentar, depois do parágrafo dos três níveis: "Hoje o crítico ainda não gera pedido de aprovação: a resposta traz nivel: critico, o motivo e links.tela — diga que nada foi feito e que a alteração tem de ser feita na tela, logado." e "Escrita que mexe em veiculação já ocorrida (cobrança, números que o cliente viu) sobe para crítico." Atualizar o comentário do topo de `instrucoes.go`.
- [ ] **Step 2:** `TestToolsEsperadas_OrdemAlfabeticaE44` → `…E64`. Testes de inventário novos: toda escrita tem descrição terminada em `Nível: livre|confirma|critico.`; toda `confirma`/escalável tem `confirmacao` no schema e nenhuma `livre` sem escalada tem; nenhuma tool de escrita tem `ReadOnlyHint`; nenhum schema de escrita tem campo `stream_url`/`hub_id`/`senha`/`token` (lista de proibidos).
- [ ] **Step 3:** `BenchmarkNovoServidor` com 64 tools. Acima de 4 ms/op: memoizar o `*sdk.Server` por (principal, cfg) dentro do pedido (o SDK monta duas vezes) e remedir. Registrar o número em `docs/features/mcp.md`.
- [ ] **Step 4:** Commit `feat(mcp): instruções e inventário da escrita`.

### Task 19: Documentação

- [ ] `docs/features/mcp.md`: "Tools (64)" com a tabela da Fatia 3 (nível, escala, função chamada), seção "Escrita" (motor, token, uso único e a divergência F3-5, crítico barrado F3-1, auditoria F3-11, edição parcial F3-10), limitações (token gasto em erro interno; `token_id` omitido no `auditoria_mcp`; recat assíncrono), `ultima-verificacao`.
- [ ] `docs/operations/mcp.md`: roteiro "Conferência com dado real" ganha as escritas (preview → confirmar numa campanha de teste em prod; checar `audit_log`); poda de `mcp_confirmacoes_usadas`.
- [ ] Spec §5.2 (uso único fora da transação, com o porquê) e §6 Fatia 3 (escaladas finais de F3-4). `docs/architecture/distribution-rules.md:144` (override é síncrono com advisory lock — o doc diz goroutine). `docs/README.md` se algo mudou de lugar.
- [ ] Commit `docs(mcp): Fatia 3 — escrita`.

### Task 20: Handoff da Fatia 4 e memória

- [ ] `docs/superpowers/HANDOFF-AAAA-MM-DD-mcp-fatia-4.md` no formato dos anteriores: estado, ambiente (`d3`), convenções novas (motor, `acoes`, `HaVeiculacao`, falsos), armadilhas, pendências (inclui: o ramo barrado de F3-1 é o ponto de troca; `mcp_confirmacoes_usadas` passa a ser podada pelo varredor; follow-ups dos handlers não corrigidos).
- [ ] Memória `mcp-emonitor-plan.md` atualizada. Commit `docs(mcp): handoff da Fatia 4`.

### Task 21: Verificação final

- [ ] `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` (com e sem `-tags avaliacao`), `go vet ./...`, `go list -deps ./cmd/api | grep avaliacao` vazio.
- [ ] Testes (bancos recriados do `d3` entre pacotes): `./internal/mcp/...`, `./internal/acoes/...`, `./internal/api/handlers/`, `./internal/supervisor/`, `./internal/catalog/` (só as 4 falhas pré-existentes conhecidas: `TestDetections_ListPaged_IgnoredExcluded`, `TestDetections_CreateManualBatch_WithProof`/`NoProof`, `TestMaterialTypes_List`).
- [ ] Avaliação da Fatia 2 ainda 10/10 pelo CLI (as escritas não podem ter quebrado leitura).
- [ ] Boot: a API sobe localmente (`/v1/internal/health` 200) e `tools/list` pelo `/mcp` com token pessoal devolve 64 nomes.
- [ ] `git rev-list --count origin/master..HEAD` registrado no handoff. Nada de push.
