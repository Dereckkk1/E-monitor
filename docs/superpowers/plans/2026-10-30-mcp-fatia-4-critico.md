# MCP do E-monitor — Fatia 4 (Escrita `critico` executável + `/pendentes`) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fazer o nível `critico` executar de verdade: a tool grava um pedido em `mcp_acoes_pendentes` (24 h) e devolve o link `/pendentes?id=…`; o admin que pediu aprova (ou recusa) no portal, logado, e só então a escrita roda, com a mesma função que a tela usa. Junto: a tela `/pendentes`, o varredor, a correção do `EditarCampanha` não transacional, as tools críticas da spec §6 que sobreviveram ao levantamento (18 de escrita + `minhas_pendentes`) e a troca do ramo "barrado" da Fatia 3 pelo pedido pendente, o que torna executáveis também as escaladas das 20 tools existentes.

**Architecture:** O motor da Fatia 3 (`ferramentaEscrita`) troca o ramo crítico barrado por `criarPendente`. Todas as escritas passam a ser montadas por **uma lista só** (`escritasDe(cfg)`), que serve ao registro das tools e ao executor de pendentes: na aprovação o executor acha a escrita pelo nome, revalida os args gravados pelo **mesmo JSON schema** que o SDK usa, reroda o `Preview` (que relê o estado e recusa se o alvo sumiu) e chama o mesmo `Executar`. Os estados seguem o hub (`pendente → aprovada → executada`; recusa de negócio volta a `pendente` com o erro; exceção vira `falhou`, final), com transições por `UPDATE … WHERE estado = … AND tentativa = …`. A lógica de escrita nova nasce em `internal/acoes` (handler travado por teste antes de extrair), como no Lote B da Fatia 3.

**Tech Stack:** Go 1.26, go-sdk v1.8.0 (+ `github.com/google/jsonschema-go` passa de indireta a direta), pgx v5, chi v5, testify; React (JSX) + React Query + Playwright no frontend.

**Spec:** [docs/superpowers/specs/2026-10-27-mcp-server-design.md](../specs/2026-10-27-mcp-server-design.md) §4.6 (auditoria), §5.2 (mecânica do crítico), §6 "Fatia 4", §7.
**Contrato:** `hub/docs/architecture/mcp-ecossistema.md` (origin/main do hub) §3.5 e §6 ("só quem pediu aprova"). **Referência de código:** hub `backend/src/mcp/write/{pendentes,executarPendente,naAprovacao,registro}.ts`, `backend/src/controllers/mcpPendentesController.ts`, `frontend/src/pages/Pendentes.tsx`.
**Plano anterior:** [2026-10-29-mcp-fatia-3-escrita.md](2026-10-29-mcp-fatia-3-escrita.md). **Handoff:** [HANDOFF-2026-10-29-mcp-fatia-4.md](../HANDOFF-2026-10-29-mcp-fatia-4.md).

---

## O que os handlers fazem hoje (levantamento de 2026-10-30)

Base de todas as decisões abaixo. Arquivos em `workers/internal/api/handlers/` salvo indicação.

| Rota | Handler → função | Efeitos | Observações que mudam o desenho |
|---|---|---|---|
| `PUT /campaigns/{id}/start` | `campaigns.go:322` → `Supervisor.Start` | status `ativa` sem pré-condição, NATS `index.reload`, sobe workers | **Reativa `cancelada`/`concluida`** (e o `cancelled_at` fica). O `LifecycleScheduler` (60 s) rebaixa a `programada` se `start_date` é futura (incidente 2026-10-01) e a `concluida` se `end_date` passou. Campanha inexistente = 500 |
| `PUT /campaigns/{id}/pause` | `campaigns.go:425` | **410 sempre** (desde 2026-05-07: "use cancel") | `Supervisor.Pause` só é usado pelo Delete |
| `POST /campaigns/{id}/cancel` | `campaigns.go:388` → `Campaigns.CancelCampaign` + `StopWorkersForCampaign` | `status=cancelada`, `cancelled_at=now()`, para workers sem outra campanha ativa | Atômico no banco (1 CTE). Só de `programada`/`ativa`; senão 409 `{error, current_status}` |
| `DELETE /campaigns/{id}` | `campaigns.go:339` → `CountForeignProjections`, `Supervisor.Pause`, `Campaigns.Delete` (tx) | **apaga as próprias `detections`** + commercials + fingerprints; CASCADE em vínculos, regras, overrides, preços, `detection_campaigns`, `assertiveness_daily` | Não é bloqueado por veiculação. `manual_proof_batches` (NO ACTION) e `post_sale_report_campaigns` (RESTRICT) dão **500**. `Pause` roda ANTES da tx: falha no delete deixa `cancelada` sem `cancelled_at`. Id inexistente = 204 |
| `PUT /campaigns/{id}/fixed-cpm` | `campaigns.go:293` → `Campaigns.UpdateFixedCPM` | — | `{fixed_cpm: number\|null}`, ≥ 0, `NUMERIC(12,2)`. Sem checagem de estado |
| `PUT/DELETE /campaigns/{c}/pricing/{s}` | `pricing.go:67/115` → `Pricing.Upsert` (tx por emissora) / `Delete` | — | `{mode, consolidated_value, per_type[]}`; `ValidatePricingInput` → 422 texto. Não confere `target_stations`; FK → 500. O wizard salva **uma emissora por PUT, em sequência, sem tx**; nunca chama DELETE |
| `POST /detections/manual` | `detections.go:403` → `Detections.CreateManual` | `settleCellDay` síncrono na tx: **pode rebaixar outras tocadas do dia** | Exige vínculo material×emissora×campanha (422). Não confere estado/período (fora do período vira `out_date`). Sem idempotência. **Não tem campo de comprovante** |
| `POST /detections/manual/batch` | `detections_manual_batch.go:62` → `CreateManualBatch` | idem, em lote | O PDF de comprovante **só entra na criação** ("sem endpoint pra anexar depois") |
| `POST /detections/{id}/ignore` / `restore` | `detections.go:547/583` → `Detections.Ignore/Restore` (`mutateApprovedSet` + `resettleCells`) | recalcula a cota da(s) célula(s)-dia na mesma tx | Sem motivo, sem checagem de estado (ignorar 2× reescreve `ignored_by`; restaurar não-ignorada = 200 no-op). Restore não grava ator |
| `POST /post-sale/reports/{id}/publish` | `post_sale.go:414` → `postsale.Service.Publish` | S3, `payload_json`, destinatários, **email síncrono** | **Exige as capturas PNG que o navegador gera e sobe (`UploadAssets`), guardadas só na memória do processo** (`service.go:52`); sem elas, 500 (`TestPublish_AbortaSemAssets`) |
| `POST /post-sale/reports/{id}/resend` | `post_sale.go:429` → `Service.Resend` | email síncrono com o **mesmo** link | 404 se rascunho/revogado/de outro relatório. Não idempotente |
| `POST /post-sale/recipients/{rid}/revoke` | `post_sale.go:449` → `Repo.RevokeRecipient` | link morre para sempre | Não confere o relatório. Idempotente. Não há como emitir link novo (republicar = 409) |
| `POST /admin/users` | `users.go:231` (tudo inline) | bcrypt, trigger da carteira, `SetClients`, `welcome.Service.Issue` (email síncrono) | **A senha vem do admin, em claro, no corpo**; a resposta traz `welcome.link` (segredo: abre a senha). Mínimo 12 |
| `PATCH /admin/users/{id}` | `users.go:384` (inline) | trigger 0070 revoga tokens MCP ao rebaixar/desativar; corte do JWT em ≤ 60 s | **Sem guarda contra rebaixar/desativar a si mesmo nem o último admin** |
| `POST/…/clients/{id}/deactivate|activate` | `clients.go:157` → `Clients.SetActive` | só a coluna | Bloqueia **login novo**; sessões abertas (até 8 h), chaves de API, webhooks e workers seguem. Idempotente |
| `DELETE /clients/{id}` | `clients.go:121` → `Clients.Delete` | CASCADE: `api_keys`, `webhook_*`, `client_station_pmm`, `post_sale_reports` | 409 `client_has_dependents` com contagens se há campanha/material/usuário |
| `DELETE /materials/{id}` | `materials.go:362` → `Materials.Delete` | — | Vinculado a campanha = 23503 → **500**. **Não é bloqueado por veiculação: as tocadas ficam órfãs** (FK caiu na 0024). Inexistente = 204. Master no disco e hashes ficam |
| `PATCH /clients/{id}/webhook` | `webhooks.go:92` → `Clients.UpdateWebhookConfig` | — | Validação inline (URL, http só em dev, **SSRF**, segredo ≥ 16, eventos). **O segredo vem no corpo, digitado** |
| `POST /clients/{id}/webhook-test` | `webhooks.go:187` → `Outbox.Enqueue` | entrega assíncrona (worker, poll 5 s) | **202 `queued:true` mesmo quando o teste é descartado em silêncio** (sem URL, desativado, ou sem `webhook.test` nos eventos — o padrão é só `detection.confirmed`) |
| — | `acoes.EditarCampanha` (`acoes/campanhas.go:91`) | `UpdateBasic` e depois `AtualizarHubCode` | Dois `UPDATE` no pool, sem tx (pendência obrigatória do handoff). `hub_code` não tem índice único, FK nem CHECK (0069) |

---

## Decisões deste plano (o dono aprova junto com o plano)

| # | Decisão | Por quê |
|---|---|---|
| F4-1 | **Catálogo final: 18 escritas `critico` + 1 leitura (`minhas_pendentes`) = 83 tools.** Saem da tabela da spec: **`pausar_campanha`** (a rota é 410 desde 2026-05-07; "pausar" hoje é cancelar), **`iniciar_campanha`** (a rota reativa cancelada/concluída sem pré-condição, e o agendador rebaixa em ≤ 60 s uma campanha ativada antes do início — incidente 2026-10-01; o caminho honesto para "começar antes" é `editar_campanha` com início = hoje, que já escala a `critico` e agora executa; quem ativa é o agendador) e **`publicar_pos_venda`** (a publicação exige as capturas PNG que o navegador gera e que ficam só na memória do processo; não há renderizador no servidor — a tool `obter_pos_venda` já devolve o link da tela de publicação). Entra **`minhas_pendentes`** (o hub tem a mesma; sem ela o modelo não sabe se o pedido foi aprovado e tende a dizer que fez). **`testar_webhook` fica, `critico`**, mas recusa quando o teste seria descartado (ver F4-10). | Spec §10 deixou `testar_webhook` em aberto; as outras três o levantamento mostrou inviáveis ou enganosas. |
| F4-2 | **Tabela `mcp_acoes_pendentes` (migration `0072`; no merge vira `0073`)** e estados do hub: `pendente → aprovada → executada`; **recusa de negócio na execução volta a `pendente`** com `erro` e prazo `GREATEST(expira_em, agora + 1 h)` (a pessoa lê o porquê e recusa, ou corrige o mundo e aprova de novo); **erro interno/panic → `falhou`, final** (algo pode ter sido gravado: uma pessoa confere); `recusada` e `expirada` finais. `tentativa` conta as aprovações: só a tentativa vigente fecha o pedido; `marcarExecutada` aceita `aprovada` **ou** `falhou` da mesma tentativa (o varredor pode ter marcado `falhou` uma execução que terminou). Pendente é registro: sem poda. | Spec §5.2 dizia `executada \| falhou`; o hub refinou depois de uso real e é o mesmo contrato. Atualizar §5.2. |
| F4-3 | **Pedido igual aberto não duplica.** `args_hash = SHA-256(tool ‖ 0x00 ‖ args canônicos)`; índice único parcial `(user_id, args_hash) WHERE estado IN ('pendente','aprovada')`. Pedir de novo devolve o mesmo pedido com `ja_existia: true` (antes, expira o vencido daquele hash). | Modelo repete chamada; aprovar dois `criar_veiculacao_manual` iguais lançaria duas tocadas. |
| F4-4 | **Registro único das escritas.** Cada família passa a expor `escritas<Familia>(cfg) []escritaRegistravel`; `escritasDe(cfg)` concatena; `registrarEscritas(s, cfg, p)` registra; `indexarEscritas` monta o mapa nome → escrita e **falha se dois nomes colidem** (teste). O executor da aprovação: acha pelo nome (sumiu → `tool_desconhecida`), **revalida os args pelo mesmo JSON schema que o SDK infere** (`jsonschema.For[In]` + `Resolve` + `Validate`, espelhando o go-sdk v1.8.0) e decodifica com `DisallowUnknownFields` (falha → `args_invalidos`), **reroda o `Preview`** (recusa se o alvo sumiu ou o estado não permite mais) e chama o `Executar` sob `WithoutCancel` + 60 s. O `Escalar` **não** reroda (já é crítico). | Spec §5.2 ("toda critico tem executor por construção"); hub `registro.ts`. |
| F4-5 | **Ramo crítico = `criarPendente`.** Resposta (sem `isError`): `{executou:false, nivel:"critico", motivo, preview, pendente_id, expira_em, aprovar_em:"<portal>/pendentes?id=<uuid>", ja_existia, aviso, links:{tela}}`. Chaves em snake_case como o resto do E-monitor (`expira_em` já é assim desde a Fatia 3); as `Instrucoes` passam a dizer `aprovar_em`. `aviso` = "Nada aconteceu ainda. Entregue o link aprovar_em à pessoa: só ela, logada no portal, pode aprovar; o pedido vale 24 h." Escrita declarada `Critico` ganha o campo `Motivo string` (fixo, ex.: "muda a categoria das outras veiculações do mesmo dia"); escalada usa o motivo do `Escalar`. Token de confirmação que vier junto é ignorado (não consome). Auditoria: `mcp.pendente.criada` (substitui `mcp.escrita.requer_aprovacao`). Sem `Portal` configurado → `aprovar_em: null` + ressalva "abra /pendentes no portal". | Contrato §3.5; spec §5.2. |
| F4-6 | **Aprovação no portal.** Rotas sob o grupo admin já existente do MCP (`MontarAdmin`, JWT + `RequireRole("admin")`): `GET /v1/internal/mcp/pendentes[?id=]` → `{items (abertas: pendente+aprovada, 50 mais novas), destaque}`; `POST …/pendentes/{id}/aprovar`; `POST …/pendentes/{id}/recusar`. **Só quem pediu**: todo SQL filtra `user_id = claims.UserID`; "não existe", "não é seu" e id malformado respondem **igual** (404 `nao_encontrado`). Papel relido do banco (`Tokens.DonoAdminAtivo`, como `TokensHTTP.Criar`). A execução é síncrona no pedido HTTP; o `Principal` é o dono (que é quem aprova) com o `TokenID` gravado na pendente. Respostas: 200 `{pendente, resultado}`; 409 `{error:"pendente_<estado>", mensagem}`; 422 `{error:<codigo>, mensagem}` (recusa de negócio — a pendente voltou a `pendente`); 500 `{error:"interrompida", mensagem}` (virou `falhou`). A pendente devolvida à tela **não traz `args` nem `resultado`** (só preview, motivo, estados, datas, erro). | Contrato §6 ("só quem pediu aprova"); spec §5.2. |
| F4-7 | **Auditoria do crítico.** O executor grava, depois do sucesso e fora da transação da escrita (como o motor da Fatia 3), `mcp.escrita.<tool>` com `meta.mcp = {token_id, pendente_id}`; e os eventos `mcp.pendente.criada|aprovada|recusada|executada|falhou`. **Diverge da spec §4.6** ("dentro da função compartilhada"): o executor é ele próprio o ponto único por onde todo crítico passa, e pôr a auditoria dentro de cada função de `acoes` a gravaria também nas escritas da tela, que não são do MCP. `pendente_id` aparece no `auditoria_mcp` (não casa `chaveSecreta`); `token_id` segue oculto. | Atualizar §4.6. |
| F4-8 | **Varredor** (`mcp.Varredor`, goroutine iniciada em `main.go` com o `ctx` do processo, intervalo 1 min, `pg_try_advisory_lock` como o `assertiveness.Scheduler`): (1) `pendente` com `expira_em ≤ now()` → `expirada`; (2) `aprovada` com `aprovada_em < now() − 10 min` → `falhou` ("A execução foi interrompida; confira se a ação aconteceu") + auditoria de sistema `mcp.pendente.falhou`; (3) poda `mcp_confirmacoes_usadas` vencidas há > 1 h (até 5 000 por volta). A poda oportunista do `Confirmacoes.Consumir` fica (é barata e cobre o intervalo). | Spec §5.2; pendência F3-5. |
| F4-9 | **`EditarCampanha` atômico (pré-requisito).** `catalog.UpdateBasicInput` ganha `HubCode *string` (nil = não mexe); `UpdateBasic` grava nome, datas e, quando pedido, `hub_code`/`hub_notified_at=NULL` **num único `UPDATE`**. `acoes.EditarCampanha` deixa de chamar `AtualizarHubCode`. Teste: um trigger de teste que dá `RAISE` quando `NEW.hub_code = 'EH-FALHA1'` prova que nome e datas NÃO mudam quando a gravação do código falha. | Pendência obrigatória do handoff: o crítico executa sem token e a falha parcial enganaria quem aprovou. |
| F4-10 | **O MCP é mais estrito que a rota onde a rota destrói em silêncio** (recusas só do lado do MCP, no `Preview`, que reroda na aprovação): `apagar_campanha` só sem histórico (sem veiculação própria nem projeção, sem lote manual, sem pós-venda) — "cancele em vez de apagar" (`campanha_com_historico`); `apagar_material` só sem vínculo (`material_vinculado`) e sem veiculação (`material_com_historico`); `ignorar_veiculacao` de já ignorada (`ja_ignorada`) e `restaurar_veiculacao` de não ignorada (`nao_ignorada`); `cancelar_campanha` de terminal (`estado_terminal`); `ativar/desativar_cliente` que não muda nada (`sem_mudanca`); `testar_webhook` que seria descartado (`webhook_teste_descartado`, dizendo qual condição: sem URL, desativado, ou `webhook.test`/`*` fora dos eventos); `editar_precos_campanha` com emissora fora da campanha ou repetida (`entrada_invalida`); `revogar/reenviar_pos_venda` de destinatário de outro relatório (`nao_encontrado`). **Mudanças na rota HTTP** (vêm da extração, para melhor): material vinculado 500 → **409**; FK de preço 500 → 404/400 por `recusaDoBanco`. | Contrato §3.5: crítico é o que não tem volta. A tela continua podendo o que pode hoje. |
| F4-11 | **Segredo nunca passa pelo chat.** `criar_usuario` **não recebe senha**: o servidor gera uma aleatória (24 caracteres, `crypto/rand`) e a boas-vindas é **obrigatória** (é ela que entrega a senha à pessoa); sem `WELCOME_ENC_KEY` a tool recusa no preview (`boas_vindas_indisponivel`). O resultado nunca traz link nem senha (`obter_usuario`). `editar_webhook_cliente` **não tem campo de segredo** (só `url`, `ativo`, `eventos`; o segredo continua na tela) e recusa URL com credencial (`user:senha@`). Nenhum schema de escrita aceita `senha`/`segredo`/`token`/`password`/`secret` (inventário). | Contrato §3.3; spec §1 não-objetivos. Pendente guarda os args 24 h no banco. |
| F4-12 | **Guardas de si mesmo.** `editar_usuario` recusa mudar o próprio papel e `desativar_usuario` recusa a si mesmo (`proprio_usuario`): o trigger 0070 revogaria a credencial MCP de quem pediu no meio da aprovação. Ninguém desativa/rebaixa o **último admin ativo** (`ultimo_admin`). Só no MCP (a rota não tem essas guardas; follow-up). | — |
| F4-13 | **`criar_veiculacao_manual` sem comprovante.** O PDF só entra na criação pela tela (não há rota de anexar depois). A descrição manda o modelo **perguntar** se há comprovante; havendo, o caminho é a tela (`links.tela` = `/detections` filtrado). A censura de áudio pode ser anexada depois pela tela (`POST /detections/{id}/evidence` existe). Preview avisa possível duplicata (veiculação aprovada da mesma campanha × emissora × material a ±10 min), sem recusar. | Spec dizia "comprovante via deep-link"; o deep-link não existe. |
| F4-14 | **Extrações para `internal/acoes`** (handler travado por teste ANTES, depois extraído, depois a tool — padrão do Lote B da Fatia 3): `CancelarCampanha`, `ApagarCampanha`, `DefinirCPMFixo`, `DefinirPrecos` (lote numa transação só: `catalog.Pricing.UpsertLote`; a rota de uma emissora chama com 1), `CriarVeiculacaoManual`, `IgnorarVeiculacao`, `RestaurarVeiculacao`, `CriarUsuario`, `EditarUsuario`, `DefinirAtivoCliente`, `ApagarCliente`, `ApagarMaterial`, `EditarWebhook` (+ `ValidarWebhook`), `TestarWebhook`. Pós-venda não precisa: `postsale.Service.Resend` e `Repo.RevokeRecipient` já são as funções que a rota chama. `SupervisorDeCampanha` ganha `Pause` e `StopWorkersForCampaign`. **Códigos das `Recusa` de usuários** = os da rota (`email_taken`, `password_too_short`, …): a tool valida em português antes. | Regra de ouro (spec §3). |
| F4-15 | **Tela `/pendentes`** (JSX, admin, dentro do `AppShell`; **não** entra em `PUBLIC_ROUTES`): lista as abertas e, com `?id=`, destaca aquela em qualquer estado; cartão com resumo, motivo, mudanças (de → para), efeitos, ressalvas, prazo e os botões Aprovar/Recusar; estado `aprovada` = "executando"; `falhou` explica que pode ter acontecido; recusa de negócio mostra o `erro` e deixa aprovar de novo. Anti-frame: `_headers` (`/pendentes`, `/pendentes/*`) + a mesma guarda de moldura do `/autorizar-ia`. Link "Pedidos da IA" na seção "Integração com IA" do `/account`. Teste Playwright com API falsa. **Nada de `npm install`** (regra 5): só `npm ci` se precisar instalar. | Spec §5.2; contrato §3.5. |
| F4-16 | **Fora desta fatia:** iniciar/pausar/publicar (F4-1); reativar usuário; trocar senha; anexar comprovante depois; corrigir as rotas além do que a extração já melhora (follow-ups listados na Task 24). | — |

---

## Antes de começar — leia

- `CLAUDE.md` da raiz: regras 4.8 (migration passa pelo `shadow_migration_test`; aqui é só `CREATE TABLE`), 5 (**nunca** `npm install` em `frontend/`), 6.1 (cross-compile), 6.5 (boot), 6.7 (nenhum CLI novo nesta fatia).
- **Árvore compartilhada.** Só o worktree `C:\tmp\emonitor-mcp`, branch `feat/mcp-server` (`git branch --show-current`). Nunca `git checkout`/`switch`/`stash` na pasta principal. Nada de push.
- **Banco de teste:** container `rc-mcp-test-pg` (porta 55440, `fsync=off` — se recriar o container, reaplique: memória `mcp-emonitor-plan`). Template `radiocheck_mcp_test_d3` (até a 0071); a Task 1 cria o `d4`. Um banco por agente, criado do template. `export TEST_DATABASE_URL=…` em TODA chamada de Bash. `go test -p 1`. `./internal/mcp` leva 10–15 min: `-timeout 60m`, em segundo plano. `./internal/mcp/...` antes de `./internal/catalog/`, recriando o banco entre pacotes.
- **Padrões das Fatias 1–3** (leia o código antes de escrever): `ferramentaEscrita`, `Escrita[In]`, `previa`/`mudanca`/`feito`, `CampoConfirmacao`, `errEntrada`/`errNaoEncontrado`/`errRecusa`, `lerCampanhaParaEscrever`, `hojeSP`, `cfg.link`/`linkTela`, `instante`; `acoes.Recusa`, `ErrAlvoInexistente`, `recusaDoBanco`; nos testes `novaBancadaTool`, `.chamar`, `.preview`, `.confirmarEExecutar`, `.exigeAuditoria`, `chamarHandlerCorpo`, `mcptest.SupervisorFalso`/`HubFalso`/`Sincrono`, fixtures de `mcptest/catalogo.go`.
- **`ToolsEsperadas` em ORDEM ALFABÉTICA** (`TestToolsEsperadas_OrdemAlfabeticaE64`): toda task que cria tool acrescenta o nome na posição certa **e** em `escritasEsperadas` (`escrita_inventario_test.go`) no mesmo commit, e ajusta o número do teste de contagem (chega a 83 na Task 22).
- **Refactor de handler:** PRIMEIRO o teste do handler que trava o comportamento atual (status, corpo, linha no banco, chamadas aos falsos), verde no código velho; depois extrair; verde de novo. Commit separado da tool.
- **CRLF:** `gofmt -l` só nos arquivos tocados (confira com `git show HEAD:<arquivo> | gofmt -l` se listar arquivo formatado).
- **Revisores erram às vezes** (2 de ~40 achados refutados na Fatia 3): o implementador confirma no código antes de mudar.

## Mapa de arquivos

| Arquivo | Responsabilidade |
|---|---|
| `migrations/0072_mcp_acoes_pendentes.{up,down}.sql` | (cria) tabela + índices |
| `workers/internal/mcp/pendentes.go` (+`_test`) | (cria) `Pendentes` (store): criar com dedup, aprovar, marcar executada/falha/interrompida, recusar, obter, listar, diagnóstico |
| `workers/internal/mcp/registro_escritas.go` (+`_test`) | (cria) `escritaRegistravel`, `escritasDe`, `registrarEscritas`, `indexarEscritas`, `decodificarArgs[In]` |
| `workers/internal/mcp/escrita.go` (+`escrita_test.go`) | (modifica) campo `Motivo`, ramo crítico → `criarPendente`, `respostaPendente`; some `avisoBarrado`/`respostaBarrada` |
| `workers/internal/mcp/aprovacao.go` (+`_test`) | (cria) `Aprovacoes.Aprovar/Recusar` (executor de pendente) |
| `workers/internal/mcp/pendentes_http.go` (+`_test`) | (cria) `PendentesHTTP` (`GET`, `aprovar`, `recusar`) |
| `workers/internal/mcp/varredor.go` (+`_test`) | (cria) `Varredor.Run(ctx)` |
| `workers/internal/mcp/{escritas,modulo,servidor}.go`, `workers/cmd/api/main.go` | (modifica) deps novas, rotas, varredor |
| `workers/internal/mcp/tools_escrita_*.go` (as 6 famílias da Fatia 3) | (modifica) `registrarEscritasX` → `escritasX(cfg) []escritaRegistravel`; descrições "nesse caso…" |
| `workers/internal/mcp/tools_pendentes.go` (+`_test`) | (cria) `minhas_pendentes` |
| `workers/internal/catalog/campaigns.go` (+`_test`) | (modifica) `UpdateBasic` com `HubCode` (F4-9) |
| `workers/internal/catalog/pricing.go` (+`_test`) | (modifica) `UpsertLote` (tx única) |
| `workers/internal/acoes/{campanhas,precos,veiculacoes,usuarios,clientes,materiais,webhooks}.go` (+`_test`) | (cria/modifica) extrações F4-14 |
| `workers/internal/api/handlers/{campaigns,pricing,detections,users,clients,materials,webhooks}.go` | (modifica) chamam `acoes` |
| `workers/internal/mcp/tools_escrita_{criticas_campanhas,veiculacoes,usuarios,clientes,webhooks,posvenda}.go` (+`_test`) | (cria) as 18 tools |
| `workers/internal/mcp/tools_veiculacoes.go`, `tools_posvenda.go` | (modifica) extrair `montarVeiculacao`; `id` no `destinatarioDTO` |
| `workers/internal/mcp/mcptest/escrita.go` | (modifica) falsos de boas-vindas/outbox se precisar |
| `workers/internal/mcp/instrucoes.go` | (modifica) parágrafo do crítico |
| `frontend/src/pages/PendentesPage.jsx` + `.css`, `frontend/src/api/hooks.js`, `frontend/src/App.jsx`, `frontend/src/components/account/McpIntegrationSection.jsx`, `frontend/public/_headers`, `frontend/e2e/mcp-pendentes.spec.js` | (cria/modifica) tela |
| `docs/features/mcp.md`, `docs/operations/mcp.md`, spec §4.6/§5.2/§6, `docs/README.md` | docs |
| `docs/superpowers/HANDOFF-AAAA-MM-DD-mcp-fatia-5.md` | handoff |

**Lotes:** A = Tasks 0–9 (migration, store, registro, motor, aprovação, HTTP, varredor, `EditarCampanha` atômico, `minhas_pendentes`); B = Tasks 10–14 (extrações para `acoes` + refactor dos handlers); C = Tasks 15–20 (as 18 tools); D = Tasks 21–25 (frontend, instruções/inventário/desempenho, docs, handoff/memória, verificação com aprovação ponta a ponta). Revisão de aderência + qualidade ao fim de cada lote; revisão final da branch no fim do D. Tasks que mexem em `servidor.go`/`ToolsEsperadas`/`registro_escritas.go` rodam em sequência.

---

### Task 0: Ambiente

- [ ] **Step 1:** `cd C:/tmp/emonitor-mcp && git branch --show-current && git status --short` → `feat/mcp-server`, limpo.
- [ ] **Step 2:** `docker ps --filter name=rc-mcp-test-pg --format '{{.Names}} {{.Status}}'` → `Up`. Se parado: `docker start rc-mcp-test-pg`. Conferir `docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -Atc "SHOW fsync"` = `off`.
- [ ] **Step 3:** Banco de trabalho do `d3` e linha de base (segundo plano):
```bash
docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -c "CREATE DATABASE radiocheck_mcp_test_f4 TEMPLATE radiocheck_mcp_test_d3;"
cd C:/tmp/emonitor-mcp/workers && export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_f4?sslmode=disable" && go build ./... && go test -p 1 -timeout 60m ./internal/mcp/... 2>&1 | tail -6
```
Expected: `ok` em todos. Isto também fecha a ressalva da Fatia 3 (a suíte inteira não foi rerodada depois do conserto do `TestTipoMaterial_ExecutarRepeteAChecagemDeNome`).

---

### Task 1: Migration `0072_mcp_acoes_pendentes` e template `d4`

**Files:** Create `migrations/0072_mcp_acoes_pendentes.up.sql`, `.down.sql`

- [ ] **Step 1: up**
```sql
-- Pedidos de escrita `critico` do MCP à espera de aprovação humana (spec §5.2,
-- plano Fatia 4 F4-2/F4-3). A tool não executa: grava os args canônicos e o
-- preview; o dono aprova em /pendentes, logado, e só aí o executor roda.
-- Estados: pendente → aprovada → executada; recusa de negócio volta a
-- pendente (com erro); exceção = falhou (final); recusada e expirada finais.
-- `tentativa` conta as aprovações: só a vigente fecha o pedido. Sem poda.
CREATE TABLE mcp_acoes_pendentes (
    id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_id     UUID        REFERENCES mcp_tokens(id) ON DELETE SET NULL,
    tool         TEXT        NOT NULL CHECK (char_length(tool) BETWEEN 1 AND 100),
    args         JSONB       NOT NULL,
    args_hash    BYTEA       NOT NULL CHECK (octet_length(args_hash) = 32),
    motivo       TEXT        NOT NULL DEFAULT '',
    preview      JSONB       NOT NULL,
    estado       TEXT        NOT NULL DEFAULT 'pendente'
                 CHECK (estado IN ('pendente','aprovada','executada','falhou','recusada','expirada')),
    tentativa    INT         NOT NULL DEFAULT 0,
    criada_em    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expira_em    TIMESTAMPTZ NOT NULL,
    aprovada_em  TIMESTAMPTZ,
    executada_em TIMESTAMPTZ,
    recusada_em  TIMESTAMPTZ,
    resultado    JSONB,
    erro         TEXT        CHECK (erro IS NULL OR char_length(erro) <= 2000)
);
CREATE INDEX mcp_acoes_pendentes_do_dono ON mcp_acoes_pendentes (user_id, criada_em DESC, id DESC);
CREATE INDEX mcp_acoes_pendentes_vencendo ON mcp_acoes_pendentes (expira_em) WHERE estado = 'pendente';
CREATE INDEX mcp_acoes_pendentes_presas ON mcp_acoes_pendentes (aprovada_em) WHERE estado = 'aprovada';
-- F4-3: o mesmo pedido aberto não duplica.
CREATE UNIQUE INDEX mcp_acoes_pendentes_aberta_unica ON mcp_acoes_pendentes (user_id, args_hash)
    WHERE estado IN ('pendente', 'aprovada');
```
(Confira na `0070` se a extensão `uuid-ossp` está disponível — `mcp_tokens` usa `uuid_generate_v4()`, então está.)
- [ ] **Step 2: down** — `DROP TABLE IF EXISTS mcp_acoes_pendentes;`
- [ ] **Step 3:** Criar `radiocheck_mcp_test_d4` do `d3` e migrar (receita de `docs/operations/mcp.md` "Testar as tools localmente"):
```bash
docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -c "CREATE DATABASE radiocheck_mcp_test_d4 TEMPLATE radiocheck_mcp_test_d3;"
cd C:/tmp/emonitor-mcp && MSYS_NO_PATHCONV=1 docker run --rm --network rc-mcp-test -v "$(pwd)/migrations:/migrations:ro" migrate/migrate:v4.17.1 -path=/migrations -database "postgres://radiocheck:radiocheck@rc-mcp-test-pg:5432/radiocheck_mcp_test_d4?sslmode=disable" up
docker exec rc-mcp-test-pg psql -U radiocheck -d radiocheck_mcp_test_d4 -Atc "SELECT version, dirty FROM schema_migrations"
```
Expected: `72|f`. Conferir `down 1` + `up` num banco descartável criado do `d4`.
- [ ] **Step 4:** `docs/operations/mcp.md` "Ambiente de teste": template = `d4`. Recriar `radiocheck_mcp_test_f4` do `d4`. Commit `feat(mcp): migration 0072 — pedidos pendentes do crítico`.

---

### Task 2: Store de pendentes

**Files:** Create `workers/internal/mcp/pendentes.go`, `pendentes_test.go`

- [ ] **Step 1: Testes que falham** (banco; `mcptest.DB` + `mcptest.Usuario` para o dono e para um segundo admin):
  1. `Criar` grava `pendente`, `tentativa=0`, `expira_em = agora+24h`, devolve `jaExistia=false`; criar de novo com o mesmo `(user, tool, args)` devolve o MESMO id e `jaExistia=true`; outro usuário com os mesmos args cria outro.
  2. Depois que a primeira vence (`expira_em` no passado), `Criar` igual expira a velha e cria uma nova.
  3. `Aprovar` pelo dono: `aprovada`, `tentativa=1`, `aprovada_em`; segunda `Aprovar` → `ErrEstadoPendente{Estado:"aprovada"}`; pelo outro admin → `ErrPendenteNaoAchada`; id inexistente → `ErrPendenteNaoAchada`.
  4. `Aprovar` de vencida → marca `expirada` e devolve `ErrEstadoPendente{Estado:"expirada"}`.
  5. `MarcarExecutada(id, 1, resultado)` fecha; com `tentativa` errada → `false`; aceita `falhou` da mesma tentativa.
  6. `MarcarFalhaDeNegocio(id, 1, "x")` volta a `pendente` com `erro`, `aprovada_em NULL`, `expira_em = max(antigo, agora+1h)`; aprovar de novo dá `tentativa=2`.
  7. `MarcarInterrompida` → `falhou`; não aprova mais (`ErrEstadoPendente{falhou}`).
  8. `Recusar` pelo dono → `recusada`; repetir → `ErrEstadoPendente{recusada}`; pelo outro → `ErrPendenteNaoAchada`.
  9. `Listar(dono, abertas)` traz só `pendente`/`aprovada` do dono, mais novas primeiro, desempate por id; `Obter` do outro → `ErrPendenteNaoAchada`.
  10. `erro` com 5 000 runas é cortado em 2 000 (CHECK do banco nunca dispara).
- [ ] **Step 2: Implemente**

```go
package mcp

// Store de pedidos `critico` (spec §5.2, plano Fatia 4 F4-2/F4-3). Transplante
// de hub backend/src/mcp/write/pendentes.ts para Postgres: toda transição é um
// UPDATE … WHERE estado = … (dois cliques, um vence) e todo SQL filtra pelo
// dono — "não existe" e "não é seu" são o mesmo erro.

const (
	ttlPendente       = 24 * time.Hour
	folgaAposFalha    = time.Hour
	tetoErroPendente  = 2000
	tetoListaPendente = 50
)

var ErrPendenteNaoAchada = errors.New("mcp: pedido não encontrado")

// ErrEstadoPendente: o pedido existe e é do dono, mas não está em `pendente`.
type ErrEstadoPendente struct{ Estado string }

func (e ErrEstadoPendente) Error() string { return "mcp: pedido em estado " + e.Estado }

type Pendente struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	TokenID     *uuid.UUID
	Tool        string
	Args        json.RawMessage
	Motivo      string
	Preview     previa
	Estado      string
	Tentativa   int
	CriadaEm    time.Time
	ExpiraEm    time.Time
	AprovadaEm  *time.Time
	ExecutadaEm *time.Time
	RecusadaEm  *time.Time
	Resultado   json.RawMessage
	Erro        *string
}

type NovaPendente struct {
	UserID  uuid.UUID
	TokenID uuid.UUID
	Tool    string
	Args    []byte // canônicos (argsCanonicos), sem "confirmacao"
	Motivo  string
	Preview previa
}

type Pendentes struct{ DB *pgxpool.Pool }

func hashDosArgs(tool string, args []byte) []byte {
	h := sha256.New()
	h.Write([]byte(tool))
	h.Write([]byte{0})
	h.Write(args)
	return h.Sum(nil)
}

const colunasPendente = `id, user_id, token_id, tool, args, motivo, preview, estado, tentativa,
	criada_em, expira_em, aprovada_em, executada_em, recusada_em, resultado, erro`

// Criar grava o pedido ou devolve o aberto igual (F4-3). Numa transação: expira
// o vencido daquele hash, tenta inserir, e no conflito lê o aberto.
func (s *Pendentes) Criar(ctx context.Context, n NovaPendente, agora time.Time) (Pendente, bool, error) {
	hash := hashDosArgs(n.Tool, n.Args)
	pv, err := json.Marshal(n.Preview)
	if err != nil {
		return Pendente{}, false, err
	}
	var tok *uuid.UUID
	if n.TokenID != uuid.Nil {
		tok = &n.TokenID
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Pendente{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE mcp_acoes_pendentes SET estado = 'expirada'
		WHERE user_id = $1 AND args_hash = $2 AND estado = 'pendente' AND expira_em <= $3`,
		n.UserID, hash, agora); err != nil {
		return Pendente{}, false, err
	}
	p, err := lerPendente(tx.QueryRow(ctx, `INSERT INTO mcp_acoes_pendentes
		(user_id, token_id, tool, args, args_hash, motivo, preview, expira_em, criada_em)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, args_hash) WHERE estado IN ('pendente', 'aprovada') DO NOTHING
		RETURNING `+colunasPendente,
		n.UserID, tok, n.Tool, n.Args, hash, n.Motivo, pv, agora.Add(ttlPendente), agora))
	jaExistia := false
	if errors.Is(err, pgx.ErrNoRows) {
		jaExistia = true
		p, err = lerPendente(tx.QueryRow(ctx, `SELECT `+colunasPendente+` FROM mcp_acoes_pendentes
			WHERE user_id = $1 AND args_hash = $2 AND estado IN ('pendente', 'aprovada')`, n.UserID, hash))
	}
	if err != nil {
		return Pendente{}, false, err
	}
	return p, jaExistia, tx.Commit(ctx)
}
```
Os demais métodos seguem o hub linha a linha (`pendentes.ts`), com estes SQL:
  - `Aprovar(ctx, id, dono, agora)`: `UPDATE … SET estado='aprovada', aprovada_em=$3, tentativa=tentativa+1 WHERE id=$1 AND user_id=$2 AND estado='pendente' AND expira_em > $3 RETURNING …`; sem linha → `diagnostico`.
  - `diagnostico(ctx, id, dono, agora)`: `SELECT estado, expira_em … WHERE id=$1 AND user_id=$2`; sem linha → `ErrPendenteNaoAchada`; `pendente` vencida → `UPDATE … SET estado='expirada' WHERE id=$1 AND estado='pendente' AND expira_em <= $2` e `ErrEstadoPendente{"expirada"}`; senão `ErrEstadoPendente{estado}`.
  - `MarcarExecutada(ctx, id, tentativa, resultado json.RawMessage, agora) (bool, error)`: `… SET estado='executada', executada_em=$4, resultado=$3, erro=NULL WHERE id=$1 AND tentativa=$2 AND estado IN ('aprovada','falhou')`.
  - `MarcarFalhaDeNegocio(ctx, id, tentativa, erro, agora)`: `… SET estado='pendente', erro=$3, aprovada_em=NULL, expira_em=GREATEST(expira_em, $4::timestamptz + interval '1 hour') WHERE id=$1 AND tentativa=$2 AND estado='aprovada'`.
  - `MarcarInterrompida(ctx, id, tentativa, erro)`: `… SET estado='falhou', erro=$3 WHERE id=$1 AND tentativa=$2 AND estado='aprovada'`.
  - `Recusar(ctx, id, dono, agora)`: `… SET estado='recusada', recusada_em=$3 WHERE id=$1 AND user_id=$2 AND estado='pendente' AND expira_em > $3 RETURNING …`; sem linha → `diagnostico`.
  - `Obter(ctx, id, dono)`, `Listar(ctx, dono, estados []string, limit, offset) ([]Pendente, int, error)` (`ORDER BY criada_em DESC, id DESC`; `estados` nil = todos; teto `tetoListaPendente`).
  - `cortarErro(s)` em `tetoErroPendente` runas antes de todo `UPDATE` que grava `erro`.
- [ ] **Step 3:** Verde. Commit `feat(mcp): store dos pedidos pendentes`.

---

### Task 3: Registro único das escritas e revalidação dos args

**Files:** Create `workers/internal/mcp/registro_escritas.go`, `registro_escritas_test.go`; Modify `servidor.go` e as 6 famílias `tools_escrita_{basicas,cadastros,campanhas,vinculos,regras,ajustes}.go`; `workers/go.mod` (jsonschema-go direta)

- [ ] **Step 1: Leia como o go-sdk v1.8.0 infere e valida o schema** (`$(go env GOMODCACHE)/github.com/modelcontextprotocol/go-sdk@v1.8.0/mcp/` — `AddTool`/`setSchema`/`toolForErr` ou equivalente): que `jsonschema.ForOptions` usa, se aplica defaults antes de validar, e como decodifica para `In`. `decodificarArgs` tem de fazer **o mesmo**; anote no comentário a função do SDK espelhada.
- [ ] **Step 2: Testes que falham**
  1. `indexarEscritas` sobre `escritasDe(cfgTeste)` devolve 20 nomes (crescerá) sem erro; uma lista com dois nomes iguais → erro que cita o nome.
  2. Os nomes de `escritasDe` == as chaves de `escritasEsperadas` (o inventário existente).
  3. `decodificarArgs[entradaCriarTipoMaterial]`: args válidos decodificam; campo desconhecido → erro; tipo errado (`nome: 1`) → erro; campo obrigatório ausente → erro; o mesmo payload que o SDK aceita pelo `/mcp` é aceito aqui (compare chamando a tool por `chamar` e o decodificador com o mesmo map).
  4. `novoServidor` continua registrando as mesmas 64 tools (o `TestToolsEsperadas_…E64` segue verde sem mudar).
- [ ] **Step 3: Implemente**

```go
package mcp

// Uma lista só de escritas (spec §5.2; hub registro.ts): a mesma serve ao
// registro das tools e ao executor das pendentes aprovadas — toda `critico`
// tem executor por construção.

type escritaRegistravel interface {
	nomeEscrita() string
	registrar(s *sdk.Server, cfg ConfigServidor, p *Principal)
	// executarAprovada roda a escrita fora do /mcp (aprovação em /pendentes):
	// revalida os args gravados, reroda o Preview e chama o Executar.
	executarAprovada(ctx context.Context, E *Escritas, p *Principal, args json.RawMessage) (feito, error)
}

func (d Escrita[In]) nomeEscrita() string { return d.Nome }

func (d Escrita[In]) registrar(s *sdk.Server, cfg ConfigServidor, p *Principal) {
	ferramentaEscrita(s, cfg, p, d)
}

// codigoArgsInvalidos: os args gravados não passam mais no schema (a tool mudou
// de forma entre o pedido e a aprovação).
const codigoArgsInvalidos = "args_invalidos"

func (d Escrita[In]) executarAprovada(ctx context.Context, E *Escritas, p *Principal, args json.RawMessage) (feito, error) {
	in, err := decodificarArgs[In](args)
	if err != nil {
		return feito{}, &acoes.Recusa{Codigo: codigoArgsInvalidos,
			Mensagem: "Os argumentos gravados não passam mais na validação desta ação: " + err.Error()}
	}
	if d.Preview != nil {
		if _, err := d.Preview(ctx, E, in); err != nil {
			return feito{}, err
		}
	}
	return executarSemPanic(ctx, d, E, p, in)
}

func escritasDe(cfg ConfigServidor) []escritaRegistravel {
	var todas []escritaRegistravel
	for _, familia := range [][]escritaRegistravel{
		escritasBasicas(cfg), escritasCadastros(cfg), escritasCampanhas(cfg),
		escritasVinculos(cfg), escritasRegras(cfg), escritasAjustes(cfg),
		// Fatia 4: acrescentar as famílias novas aqui (Tasks 15–20).
	} {
		todas = append(todas, familia...)
	}
	return todas
}

func registrarEscritas(s *sdk.Server, cfg ConfigServidor, p *Principal) {
	for _, e := range escritasDe(cfg) {
		e.registrar(s, cfg, p)
	}
}

func indexarEscritas(lista []escritaRegistravel) (map[string]escritaRegistravel, error) {
	m := make(map[string]escritaRegistravel, len(lista))
	for _, e := range lista {
		if _, dup := m[e.nomeEscrita()]; dup {
			return nil, fmt.Errorf("mcp: escrita com nome repetido: %s", e.nomeEscrita())
		}
		m[e.nomeEscrita()] = e
	}
	return m, nil
}
```
`decodificarArgs[In any](args json.RawMessage) (In, error)`: `jsonschema.For[In](<as mesmas opções do SDK>)` → `Resolve(nil)` → `Validate(<map decodificado com UseNumber, se o SDK fizer assim>)` → `json.Decoder` com `DisallowUnknownFields` para `In`. Cache do schema resolvido por tipo (`sync.Map` com chave `reflect.Type`): o executor roda raramente, mas o inventário chama para todas.
- [ ] **Step 4:** Cada família: `func registrarEscritasX(s, cfg, p)` vira `func escritasX(cfg ConfigServidor) []escritaRegistravel { return []escritaRegistravel{escritaA(cfg), …} }` (mesma ordem); `novoServidor` chama `registrarEscritas(s, cfg, p)` no lugar das 6 chamadas. Sem mudança de comportamento: suíte `./internal/mcp` inteira verde (segundo plano).
- [ ] **Step 5:** `go mod tidy` só se `jsonschema-go` precisar virar direta (sem mudar versão; regra 6.2). Commit `refactor(mcp): registro único das escritas`.

---

### Task 4: Motor — o ramo crítico vira pedido pendente

**Files:** Modify `escrita.go`, `escritas.go`, `escrita_test.go`, `escrita_ferramentas_test.go` (helper `.barrado`), testes das tools da Fatia 3 que chamam `.barrado`

- [ ] **Step 1: Testes que falham** (`escrita_test.go`, substituindo os três testes do barrado e o de :508):
  1. `TestEscrita_CriticoDeclaradoGravaPendente`: escrita `Critico` com `Motivo` fixo → `{executou:false, nivel:"critico", motivo, preview, pendente_id, expira_em, aprovar_em:"https://portal.teste/pendentes?id=<id>", ja_existia:false, aviso}`, sem `isError`; 1 linha em `mcp_acoes_pendentes` com `args` canônicos (sem `confirmacao`), `token_id = p.TokenID`, `preview` igual ao devolvido; `Executar` não chamado; 1 auditoria `mcp.pendente.criada` com `meta.pendente_id`.
  2. `TestEscrita_EscaladaGravaPendenteENaoConsomeToken` (adapta o de :275): preview `confirma`; o falso passa a escalar; a confirmação com token devolve o pedido pendente; `mcp_confirmacoes_usadas` = 0.
  3. `TestEscrita_PendenteIgualNaoDuplica`: duas chamadas iguais → mesmo `pendente_id`, a segunda com `ja_existia:true`; 1 linha.
  4. `TestEscrita_RecusaDoPreviewVenceOPendente`: `Preview` devolve `Recusa` → `isError [codigo]`, nenhuma linha.
  5. `TestEscrita_SemPortalAprovarEmNulo`: `cfg.Portal=""` → `aprovar_em:null` + ressalva "abra /pendentes no portal do E-monitor".
  6. `TestEscrita_PendenteAuditaORecursoDaTool` (adapta o de :568).
- [ ] **Step 2: Implemente.** `Escrita[In]` ganha `Motivo string` ("por que é crítica", usado quando o nível declarado é `Critico`). `Escritas` ganha `Pendentes *Pendentes` (montado em `NovasEscritas` quando `pool != nil`). Somem `avisoBarrado`, `respostaBarrada`, `linksBarrado`. O passo 4 de `ferramentaEscrita`:

```go
		// 4. crítico: grava o pedido; quem executa é a aprovação em /pendentes (F4-5).
		if nivel == Critico {
			if motivo == "" {
				motivo = d.Motivo
			}
			return criarPendente(ctx, cfg, E, p, d, in, args, motivo, pv)
		}
```
```go
const avisoPendente = "Nada aconteceu ainda. Entregue o link aprovar_em à pessoa: só ela, logada no portal, pode aprovar; o pedido vale 24 h."

type respostaPendente struct {
	Executou  bool         `json:"executou"`
	Nivel     string       `json:"nivel"`
	Motivo    string       `json:"motivo"`
	Preview   previa       `json:"preview"`
	PendenteID string      `json:"pendente_id"`
	ExpiraEm  string       `json:"expira_em"`
	AprovarEm *string      `json:"aprovar_em"`
	JaExistia bool         `json:"ja_existia"`
	Aviso     string       `json:"aviso"`
	Ressalvas []string     `json:"ressalvas,omitempty"`
	Links     linksPendente `json:"links"`
}

type linksPendente struct {
	Tela *string `json:"tela"`
}

func criarPendente[In any](ctx context.Context, cfg ConfigServidor, E *Escritas, p *Principal, d Escrita[In],
	in In, args []byte, motivo string, pv previa) (*sdk.CallToolResult, any, error) {
	if E.Pendentes == nil {
		return falhaInterna(cfg.log(), d.Nome, errors.New("mcp: Pendentes não configuradas"))
	}
	pend, jaExistia, err := E.Pendentes.Criar(context.WithoutCancel(ctx), NovaPendente{
		UserID: p.UserID, TokenID: p.TokenID, Tool: d.Nome, Args: args, Motivo: motivo, Preview: pv,
	}, E.agora())
	if err != nil {
		return falhaInterna(cfg.log(), d.Nome, err)
	}
	recurso, recursoID := d.alvo(in)
	if !jaExistia {
		auditar(ctx, cfg, E, p, "mcp.pendente.criada", recurso, recursoID, d.Nome, Critico, args,
			map[string]any{"motivo": motivo, "pendente_id": pend.ID.String()})
	}
	q := url.Values{"id": {pend.ID.String()}}
	aprovar := cfg.linkTela("/pendentes", q)
	var ressalvas []string
	if aprovar == nil {
		ressalvas = append(ressalvas, "O endereço do portal não está configurado: abra /pendentes no portal do E-monitor para aprovar.")
	}
	var tela *string
	if d.Tela != nil {
		tela = d.Tela(E, in)
	}
	return resultado(respostaPendente{
		Executou: false, Nivel: Critico.String(), Motivo: motivo, Preview: pv,
		PendenteID: pend.ID.String(), ExpiraEm: instante(pend.ExpiraEm), AprovarEm: aprovar,
		JaExistia: jaExistia, Aviso: avisoPendente, Ressalvas: ressalvas, Links: linksPendente{Tela: tela},
	})
}
```
(Confirme que `linkTela` devolve nil sem `Portal`; se não, use `cfg.link("/pendentes?id=" + id)`.)
- [ ] **Step 3:** Helper `.barrado` vira `.pedePendente(t, nome, args) map[string]any` (asserta `executou:false`, `nivel:"critico"`, `pendente_id` parseável, sem `confirmacao`, 1 linha aberta com aquele id e `tool = nome`). Troque todos os usos (`grep -rn "\.barrado(" workers/internal/mcp`). Os testes que asseguravam "nada foi escrito" continuam valendo (pedido pendente não escreve).
- [ ] **Step 4:** Descrições das tools escaláveis da Fatia 3: troque "nesse caso nada é feito e a resposta traz o link da tela" (e variantes — `grep -rn "link da tela\|faça pela tela" workers/internal/mcp/tools_escrita_*.go`) por "nesse caso vira um pedido que uma pessoa aprova em /pendentes". Acrescente no inventário: nenhuma descrição de escrita contém "faça pela tela" nem "barrad".
- [ ] **Step 5:** `mcptest.DB`: confira que o `TRUNCATE users … CASCADE` limpa `mcp_acoes_pendentes` (FK) — senão acrescente-a às bancadas, como `mcp_confirmacoes_usadas`. Suíte `./internal/mcp` verde (segundo plano). Commit `feat(mcp): crítico grava pedido pendente`.

---

### Task 5: Executor da aprovação

**Files:** Create `workers/internal/mcp/aprovacao.go`, `aprovacao_test.go`; Modify `escritas.go` (`Escritas` guarda o índice), `modulo.go`

- [ ] **Step 1: Testes que falham** (escrita de teste registrada num índice de teste; `Executar` conta chamadas):
  1. Aprovar pendente válida: `Executar` chamado 1 vez com o `In` decodificado dos args gravados e `Principal{UserID: dono, TokenID: token_id da pendente}`; pendente `executada` com `resultado`; auditorias `mcp.pendente.aprovada`, `mcp.escrita.<tool>` (`meta.mcp.token_id` e `meta.mcp.pendente_id`) e `mcp.pendente.executada`.
  2. `Preview` recusa na aprovação (alvo sumiu): `Executar` NÃO chamado; pendente volta a `pendente` com o `erro` = mensagem da recusa; devolve `*acoes.Recusa`; auditoria `mcp.pendente.falhou` com `codigo`.
  3. `Executar` devolve `Recusa` → igual ao 2.
  4. `Executar` devolve erro interno / panic → `falhou` com erro genérico ("A execução caiu no meio…; confira se a ação aconteceu antes de pedir de novo."), nunca o texto do erro Go; devolve `ErrExecucaoInterrompida`; auditoria `mcp.pendente.falhou` `{codigo:"interno"}`.
  5. Tool que não existe mais → `Recusa{tool_desconhecida}`, pendente volta a `pendente` com erro.
  6. Args que não passam no schema → `Recusa{args_invalidos}`.
  7. Outro admin aprova → `ErrPendenteNaoAchada`, nada executa.
  8. Dois `Aprovar` concorrentes (goroutines) → `Executar` 1 vez.
  9. `Recusar` → `recusada` + auditoria `mcp.pendente.recusada`.
  10. Resultado que não serializa → `executada` com `resultado` null (nunca falha depois de escrito).
- [ ] **Step 2: Implemente**

```go
package mcp

// Aprovar → executar → fechar (spec §5.2; hub executarPendente.ts), com a
// sessão de quem aprova — que é o dono: o store filtra por user_id.

var ErrExecucaoInterrompida = errors.New("mcp: execução interrompida")

const msgInterrompida = "A execução caiu no meio por um erro inesperado; confira se a ação aconteceu antes de pedir de novo."

type Aprovacoes struct {
	cfg    ConfigServidor
	indice map[string]escritaRegistravel
}

func NovasAprovacoes(cfg ConfigServidor) (*Aprovacoes, error) {
	idx, err := indexarEscritas(escritasDe(cfg))
	if err != nil {
		return nil, err
	}
	return &Aprovacoes{cfg: cfg, indice: idx}, nil
}

type Aprovado struct {
	Pendente  Pendente
	Resultado any
}

func (a *Aprovacoes) Aprovar(ctx context.Context, id uuid.UUID, ator *Principal) (Aprovado, error) {
	E := a.cfg.Escritas
	pend, err := E.Pendentes.Aprovar(ctx, id, ator.UserID, E.agora())
	if err != nil {
		return Aprovado{}, err
	}
	p := &Principal{UserID: ator.UserID, Email: ator.Email, Nome: ator.Nome}
	if pend.TokenID != nil {
		p.TokenID = *pend.TokenID
	}
	extra := map[string]any{"pendente_id": pend.ID.String()}
	a.auditarPendente(ctx, p, "mcp.pendente.aprovada", pend, extra)

	ectx, cancela := context.WithTimeout(context.WithoutCancel(ctx), prazoExecucao)
	defer cancela()
	var f feito
	if e, ok := a.indice[pend.Tool]; !ok {
		err = &acoes.Recusa{Codigo: "tool_desconhecida", Mensagem: "A ação " + pend.Tool + " não existe mais neste servidor."}
	} else {
		f, err = e.executarAprovada(ectx, E, p, pend.Args)
	}
	if err != nil {
		var r *acoes.Recusa
		if errors.As(err, &r) {
			_, _ = E.Pendentes.MarcarFalhaDeNegocio(context.WithoutCancel(ctx), pend.ID, pend.Tentativa, r.Mensagem, E.agora())
			a.auditarPendente(ctx, p, "mcp.pendente.falhou", pend, map[string]any{"pendente_id": pend.ID.String(), "codigo": r.Codigo})
			return Aprovado{}, r
		}
		a.cfg.log().Error("mcp: pendente interrompida", zap.String("tool", pend.Tool), zap.Error(err))
		_, _ = E.Pendentes.MarcarInterrompida(context.WithoutCancel(ctx), pend.ID, pend.Tentativa, msgInterrompida)
		a.auditarPendente(ctx, p, "mcp.pendente.falhou", pend, map[string]any{"pendente_id": pend.ID.String(), "codigo": "interno"})
		return Aprovado{}, ErrExecucaoInterrompida
	}
	// Escrita feita: daqui para baixo nada vira erro.
	auditar(ctx, a.cfg, E, p, "mcp.escrita."+pend.Tool, f.Recurso, f.RecursoID, pend.Tool, Critico, pend.Args, nil) // ver Step 3
	res, merr := json.Marshal(f.Resultado)
	if merr != nil {
		res = nil
	}
	if ok, err := E.Pendentes.MarcarExecutada(context.WithoutCancel(ctx), pend.ID, pend.Tentativa, res, E.agora()); err != nil || !ok {
		a.cfg.log().Error("mcp: pendente executada sem marcar", zap.String("pendente_id", pend.ID.String()), zap.Error(err))
	}
	a.auditarPendente(ctx, p, "mcp.pendente.executada", pend, extra)
	pend.Estado = "executada"
	return Aprovado{Pendente: pend, Resultado: f.Resultado}, nil
}
```
- [ ] **Step 3:** `auditar` passa a aceitar `pendente_id` em `meta.mcp`: assinatura nova `auditar(ctx, cfg, E, p, acao, recurso, recursoID, tool, nivel, args, extra, pendenteID *uuid.UUID)`, ou `meta.mcp` lido de `extra["mcp"]` — escolha a que mexe menos nos chamadores e mantenha `token_id` sempre presente. `auditarPendente` grava com recurso `"mcp_pendente"` + `pend.ID` e `{tool, motivo?, codigo?}`. `Recusar(ctx, id, ator)` = store + auditoria.
- [ ] **Step 4:** `Modulo` monta `Aprovacoes` em `NovoModulo` (erro de índice = log + módulo sem aprovação: nunca panic, regra 6.5; o teste de registro único já pega a colisão). Verde. Commit `feat(mcp): executor dos pedidos aprovados`.

---

### Task 6: Rotas HTTP de `/pendentes`

**Files:** Create `workers/internal/mcp/pendentes_http.go`, `pendentes_http_test.go`; Modify `modulo.go` (`MontarAdmin`)

- [ ] **Step 1: Testes que falham** (`chamarHandlerCorpo` com claims de admin; um segundo admin como "outro"):
  1. `GET /mcp/pendentes` → 200 `{items:[…abertas do dono…], destaque:null}`; item = `{id, tool, motivo, preview, estado, criada_em, expira_em, aprovada_em, executada_em, recusada_em, erro}` — **sem `args`, sem `resultado`, sem `token_id`**; `Cache-Control: no-store`.
  2. `GET …?id=<executada do dono>` → `destaque` preenchido; `?id=<do outro>` e `?id=lixo` → `destaque:null` (sem erro: a lista não vira oráculo).
  3. `POST …/{id}/aprovar` → 200 `{pendente:{estado:"executada",…}, resultado}`; repetir → 409 `{error:"pendente_executada", mensagem}`; do outro / inexistente / malformado → 404 `{error:"nao_encontrado", mensagem:"Pedido não encontrado."}` — **corpos idênticos** (compare os bytes).
  4. Recusa de negócio → 422 `{error:<codigo>, mensagem}` e a pendente segue aprovável; interrompida → 500 `{error:"interrompida", mensagem: msgInterrompida}`.
  5. `POST …/{id}/recusar` → 200; vencida → 409 `pendente_expirada`.
  6. Admin rebaixado no banco depois de emitir o JWT → 403 (relê `DonoAdminAtivo`).
  7. Mensagens de 409 em português (tabela do hub: "Este pedido já foi executado.", "…já foi recusado.", "…já está em execução.", "…foi interrompido; confira se a ação aconteceu.", "Este pedido expirou (24 h). Peça de novo à IA.").
- [ ] **Step 2: Implemente** `PendentesHTTP{Aprovacoes *Aprovacoes; Pendentes *Pendentes; Tokens *tokens.Store; Log}` com `Listar`, `Aprovar`, `Recusar`, reaproveitando `escreverJSON`/`erroJSON` de `tokens_http.go` (estenda `erroJSON` com `mensagem` se preciso, sem quebrar os testes dos tokens). Corpo do POST ignorado (`MaxBytesReader` 1 KB). `MontarAdmin` acrescenta `GET /mcp/pendentes`, `POST /mcp/pendentes/{id}/aprovar`, `POST /mcp/pendentes/{id}/recusar`.
- [ ] **Step 3:** Teste de autorização no padrão de `router_authz_test.go`: cliente (role viewer) → 403; sem JWT → 401. Verde. Commit `feat(mcp): rotas de aprovação dos pedidos`.

---

### Task 7: Varredor

**Files:** Create `workers/internal/mcp/varredor.go`, `varredor_test.go`; Modify `modulo.go` (`IniciarVarredor(ctx)`), `workers/cmd/api/main.go`

- [ ] **Step 1: Testes que falham** (`Agora` injetado; chama `tick` direto):
  1. `pendente` vencida → `expirada`; `pendente` no prazo intacta.
  2. `aprovada` com `aprovada_em` há 11 min → `falhou` com o texto de interrompida + 1 auditoria de sistema `mcp.pendente.falhou` (`auditoria.RegistrarSistema`); há 9 min intacta.
  3. `mcp_confirmacoes_usadas` com `expira_em` há 2 h some; há 30 min fica.
  4. Segundo varredor concorrente com o lock tomado não faz nada (advisory lock).
  5. `Run(ctx)` faz uma volta imediata e para quando o `ctx` é cancelado (teste com `context.WithCancel` e intervalo curto).
- [ ] **Step 2: Implemente** no padrão de `assertiveness.Scheduler.Run` (`internal/assertiveness/scheduler.go:65`): volta imediata, `time.Ticker` de 1 min, cada volta com `context.WithTimeout(ctx, 30*time.Second)` e `pg_try_advisory_xact_lock(<constante própria, documentada>)`. As três etapas são `UPDATE`/`DELETE` em lote (`LIMIT` via subselect `FOR UPDATE SKIP LOCKED`, como a poda do `Consumir`). Erro de uma etapa é logado e não impede as outras.
- [ ] **Step 3:** `main.go`: depois de montar o módulo MCP (`main.go:~693`), `if mcpModulo != nil { go mcpModulo.IniciarVarredor(ctx) }` com o `ctx` do processo (`main.go:65`). `Versao` do módulo passa de `"fatia-0"` para `"fatia-4"`. Cross-compile linux. Commit `feat(mcp): varredor dos pedidos e das confirmações`.

---

### Task 8: `EditarCampanha` atômico (F4-9)

**Files:** Modify `workers/internal/catalog/campaigns.go`, `campaigns_hubcode_test.go` (ou novo `campaigns_update_test.go`), `workers/internal/acoes/campanhas.go`, `campanhas_test.go`

- [ ] **Step 1: Teste que falha** (catalog): cria campanha com `hub_code = 'EH-ANTIGO'`; instala no banco de teste
```sql
CREATE OR REPLACE FUNCTION teste_falha_hub_code() RETURNS trigger AS $$
BEGIN IF NEW.hub_code = 'EH-FALHA1' THEN RAISE EXCEPTION 'falha injetada'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER teste_falha_hub_code BEFORE UPDATE ON campaigns FOR EACH ROW EXECUTE FUNCTION teste_falha_hub_code();
```
(com `t.Cleanup` que derruba trigger e função); `UpdateBasic(…, Name:"novo", HubCode: ptr("EH-FALHA1"))` → erro, e o `Get` mostra **nome, datas e código antigos**. Sem `HubCode` → só nome/datas mudam, `hub_notified_at` intacto. Com `HubCode: ptr("")` → `hub_code NULL`, `hub_notified_at NULL`. No `acoes`: `TestEditarCampanha_FalhaNoCodigoNaoGravaNome` com o mesmo trigger pela função `acoes.EditarCampanha`.
- [ ] **Step 2: Implemente**
```go
type UpdateBasicInput struct {
	Name      string
	StartDate time.Time
	EndDate   time.Time
	// HubCode nil = não mexe; não-nil grava (""/espaços = NULL) e zera
	// hub_notified_at, no MESMO UPDATE do nome e das datas (plano Fatia 4 F4-9).
	HubCode *string
}
```
```sql
UPDATE campaigns
SET name = $2, start_date = $3, end_date = $4, updated_at = now(),
    hub_code        = CASE WHEN $5 THEN NULLIF(btrim($6), '') ELSE hub_code END,
    hub_notified_at = CASE WHEN $5 THEN NULL ELSE hub_notified_at END
WHERE id = $1
RETURNING …
```
(`$5 = in.HubCode != nil`, `$6 = deref(in.HubCode)`.) `acoes.EditarCampanha`: passa `HubCode: &canonico` só quando `codigoMudou`; remove a chamada a `AtualizarHubCode` e o remendo de `out.HubCode`/`HubNotifiedAt` (o `RETURNING` já traz). Atualize o comentário de "não transacional" (`acoes/campanhas.go:89-90`).
- [ ] **Step 3:** `./internal/acoes/...`, `./internal/catalog/ -run 'Campaign|HubCode'`, `./internal/api/handlers/ -run Campanha` e `./internal/mcp/ -run EditarCampanha` verdes. Commit `fix(campanhas): nome, datas e código do hub num UPDATE só`.

---

### Task 9: `minhas_pendentes`

**Files:** Create `workers/internal/mcp/tools_pendentes.go`, `tools_pendentes_test.go`; Modify `servidor.go` (`ToolsEsperadas` +1, registro), `servidor_test.go` (contagem 65)

- [ ] **Step 1: Testes que falham:** sem filtro → as abertas (`pendente`/`aprovada`) do principal; `estado: "executada"|…|"todos"`; paginação `{items, page}` (`limit` ≤ 50, teto de `offset`); item = `{id, tool, estado, motivo, resumo (preview.resumo), criada_em, expira_em, aprovada_em, executada_em, erro, aprovar_em}` — `aprovar_em` só quando `pendente`; nunca `args`/`resultado`/`token_id`; pendente de outro usuário nunca aparece; `readOnlyHint: true`; pior caso (50 itens com resumo longo) < 60 KB (`*_PiorCasoCabe`).
- [ ] **Step 2:** `ferramentaLeitura` com `Pendentes.Listar(p.UserID, …)`. Descrição: "Lista os pedidos de escrita crítica que você fez e o estado de cada um (pendente, aprovada, executada, falhou, recusada, expirada). Use para saber se uma pessoa já aprovou antes de dizer que algo foi feito: só `executada` significa feito." Commit `feat(mcp): minhas_pendentes`.

---

### Tasks 10–14: Extrações para `internal/acoes` (Lote B)

**Padrão de toda extração** (o mesmo do Lote B da Fatia 3): (1) teste do handler que trava o comportamento atual — status, corpo, linha no banco, chamadas aos falsos — verde **no código velho**; (2) função em `acoes` com a validação e os efeitos da rota; recusas como `*Recusa` (códigos da rota onde a rota tem código; senão os de F3-9/F4-10); (3) handler passa a chamá-la e traduz a `Recusa` no status de hoje (`handlers/acoes_http.go`); (4) verde de novo; commit `refactor(acoes): <família> sai do handler`. Mudanças de comportamento da rota **só** as listadas em F4-10 (cada uma com teste próprio e commit `fix(...)` separado).

| Task | Funções novas em `acoes` | Handler | Trava (testes do handler antes) | Observações |
|---|---|---|---|---|
| **10** | `CancelarCampanha(ctx, id) (*catalog.Campaign, error)` (`CancelCampaign` + `StopWorkersForCampaign`; 404; terminal → `Recusa{estado_terminal, Dados:{estado_atual}}` que a rota traduz no 409 de hoje); `ApagarCampanha(ctx, id) error` (`CountForeignProjections` → `Recusa{campanha_com_projecoes}` 409; `Pause`; `Delete`); `DefinirCPMFixo(ctx, id, *float64)`; `DefinirPrecos(ctx, campanha, []EntradaPreco) ([]catalog.StationPricing, error)` sobre `catalog.Pricing.UpsertLote` (tx única, mesmo SQL do `Upsert`) | `campaigns.go` (Cancel, Delete, UpdateFixedCPM), `pricing.go` (Upsert → `DefinirPrecos` com 1 entrada) | Cancel: 204, `cancelled_at`, `SupervisorFalso` "StopWorkers"; 409 com `current_status`; 404. Delete: 204, linhas somem, 409 de projeção. CPM: 200, 400 negativo, 404. Pricing: 200 consolidado/por inserção, 422 de cada `ErrPricing*` | `SupervisorDeCampanha` ganha `Pause(id) error` e `StopWorkersForCampaign(id)`; atualize `supervisorFalso` de `modulo_escritas_test.go`. `UpsertLote`: teste de atomicidade (2ª entrada com `type_id` inexistente → nenhuma emissora gravada). FK de preço → `recusaDoBanco` (404 `nao_encontrado`) |
| **11** | `CriarVeiculacaoManual(ctx, EntradaManual{Campanha, Emissora, Material, Quando, Nota, Ator}) (*catalog.Detection, error)` (validações do handler: obrigatórios, `Quando` ≤ agora+5 min, `ErrMaterialNotLinkedToStation` → `Recusa{material_nao_vinculado}` 422); `IgnorarVeiculacao(ctx, id, ator)`; `RestaurarVeiculacao(ctx, id)` (ambas: `Get` → 404 → repo → `Get`) | `detections.go` (CreateManual — o núcleo depois do parse JSON/multipart; o áudio continua no handler —, Ignore, Restore) | CreateManual JSON: 201, linha com `manual_by`, categoria; 400/422 de cada validação. Ignore/Restore: 200, `ignored_at`/`ignored_by`, **outra tocada do mesmo dia promovida a `in_slot`** (fixture: cota 1, duas tocadas; ignora a 1ª) | O handler HTTP hoje não tem nenhum teste dessas rotas: os de trava são novos |
| **12** | `CriarUsuario(ctx, EntradaUsuario{…, Senha, EnviarBoasVindas}) (*users.User, *BoasVindas, error)`; `EditarUsuario(ctx, id, EdicaoUsuario{ponteiros; ClienteIDs *[]uuid.UUID; Ativo *bool})` | `users.go` (Create, Patch) | os 27 testes de `handlers/users_test.go` + `welcome_test.go` já travam: rode-os antes e depois | Toda a regra de papel/carteira (`client_id` × `client_ids`, `ClearClient`, `cannot_reactivate_deleted`) muda de lugar sem mudar de efeito. `acoes.Servico` ganha `Usuarios *users.Repo` e `BoasVindas *welcome.Service` (interface se o teste precisar falsear o email) |
| **13** | `DefinirAtivoCliente(ctx, id, ativo bool) (*catalog.Client, error)`; `ApagarCliente(ctx, id) error` (`ErrClientHasDependents` → `Recusa{cliente_com_vinculos, Dados:{campanhas, materiais, usuarios}}` que a rota traduz no 409 de hoje); `ApagarMaterial(ctx, id) error` (23503 de `campaign_materials` → `Recusa{material_vinculado}` → **409**; inexistente → `ErrAlvoInexistente`, rota segue 204) | `clients.go` (Deactivate, Activate, Delete), `materials.go` (Delete) | 200/404 do ativo; 204/409 com corpo do delete; material: 204, 204 inexistente | `fix(materiais): apagar material vinculado responde 409` em commit próprio |
| **14** | `ValidarWebhook(ctx, catalog.UpdateWebhookInput) error` (URL, http só em dev, SSRF, segredo ≥ 16, eventos — as mensagens de hoje); `EditarWebhook(ctx, cliente, in) (*catalog.WebhookConfig, error)`; `TestarWebhook(ctx, cliente) error` (`Outbox.Enqueue`) | `webhooks.go` (PatchConfig, SendTest) | os `TestWebhooks_PatchConfig_*` existentes + um de sucesso (200, segredo mascarado) | `acoes.Servico` ganha `Webhooks` (interface `Enqueue`) — nil = a rota segue 503 |

---

### Tasks 15–20: as 18 tools críticas (Lote C)

**Padrão de toda tool** (cada task cria `tools_escrita_<familia>.go` + `_test.go`, uma função `escritas<Familia>(cfg) []escritaRegistravel` acrescentada a `escritasDe`, e os nomes em `ToolsEsperadas` + `escritasEsperadas`):

- `Nivel: Critico`, `Motivo` fixo (coluna "Motivo" abaixo), `Escalar: nil`, **`In` sem `CampoConfirmacao`** (crítico nunca recebe token). Ids como string UUID; datas `YYYY-MM-DD`; instantes RFC3339 com fuso.
- Descrição: o que faz, quando usar, a armadilha, "vira um pedido que a pessoa aprova em /pendentes: entregue o link aprovar_em e diga que nada aconteceu ainda", "nunca invente ids, pergunte".
- `Preview` relê o estado, aplica as recusas (as do MCP de F4-10 inclusive) e lista `mudancas`/`efeitos`/`ressalvas` com nomes, não só ids. **Roda de novo na aprovação** (Task 5): toda recusa que depende do estado mora no `Preview`.
- `Executar` chama a função de `acoes`/do serviço e devolve o registro no formato da tool `obter_*` correspondente.
- `Tela` = a tela onde a pessoa faria o mesmo (`links.tela`).
- **Testes obrigatórios por tool:** (1) a chamada grava pendente e não escreve (estado igual); (2) **aprovar executa** (via `Aprovacoes.Aprovar` do dono — helper novo `.aprovar(t, pendenteID) Aprovado` na bancada) e o resultado tem o formato da `obter_*`; (3) **paridade de efeito** com a rota HTTP (duas fixtures, uma pela rota, uma pela aprovação; compara linhas e chamadas aos falsos); (4) cada recusa da tool, **no pedido e na aprovação** (o estado muda entre os dois: ex. alvo apagado); (5) auditoria `mcp.escrita.<tool>` com `meta.mcp.pendente_id`; (6) nenhum segredo no resultado nem nos `args` gravados.

| Task | Tool | Entrada | Motivo / recusas / efeitos | Chama |
|---|---|---|---|---|
| **15** | `cancelar_campanha` | `campanha_id` | "congela o déficit e tira a campanha das telas operacionais e da cobrança". Recusa `estado_terminal` (cancelada/concluída). Efeitos: emissoras cujo worker para (as sem outra campanha ativa, por nome); "a campanha sai de seletores, telas ao vivo e KPIs; fica marcada no histórico e nos relatórios" (`docs/features/cancelled-campaign-handling.md`) | `acoes.CancelarCampanha` |
| 15 | `apagar_campanha` | `campanha_id` | "irreversível". Recusas: `campanha_com_historico` (qualquer `detections.campaign_id` ou `detection_campaigns` da campanha, lote manual, pós-venda — "cancele em vez de apagar"). Efeitos: o que vai junto (vínculos N, regras N, ajustes N, preços N) | `acoes.ApagarCampanha` |
| 15 | `editar_cpm_fixo` | `campanha_id`, `cpm_fixo` (número ≥ 0, 2 casas, ou `null` = volta ao dinâmico) | "muda o CPM que o cliente vê". Recusa campanha cancelada (`lerCampanhaParaEscrever`), `sem_mudanca`. Efeito: "/campaigns, /insights, dashboard; pós-venda já enviado não muda (congelado)"; ressalva "o CPM no target é sempre dinâmico" (`client-target-pmm.md`) | `acoes.DefinirCPMFixo` |
| 15 | `editar_precos_campanha` | `campanha_id`, `precos[{emissora_id, modo:"consolidado"\|"por_insercao", valor_consolidado?, por_tipo?[{tipo_id, valor_unitario}]}]` (1–300) | "dinheiro". Recusas: emissora fora de `target_stations` ou repetida, tipo inexistente, campos misturados (`ValidatePricingInput` em português), cancelada. Mudanças por emissora (de → para, com nome). Ressalva: "pós-venda enviado não muda" | `acoes.DefinirPrecos` (1 transação) |
| **16** | `criar_veiculacao_manual` | `campanha_id`, `emissora_id`, `material_id`, `quando` (RFC3339 com fuso), `nota?` (≤ 500) | "entra na cobrança e pode mudar a categoria das outras veiculações do mesmo dia". Recusas: `material_nao_vinculado`, futuro, cancelada. Ressalvas: F4-13 (comprovante só pela tela na criação; censura de áudio depois pela tela); possível duplicata (±10 min) | `acoes.CriarVeiculacaoManual` (ator = dono) |
| 16 | `ignorar_veiculacao` | `veiculacao_id` | "a vaga liberada pode promover outra veiculação do mesmo dia". Recusa `ja_ignorada`. Mostra categoria atual e a célula-dia (campanha, emissora, tipo, dia SP) | `acoes.IgnorarVeiculacao` |
| 16 | `restaurar_veiculacao` | `veiculacao_id` | "a veiculação volta a disputar a cota do dia e pode rebaixar outra". Recusa `nao_ignorada` | `acoes.RestaurarVeiculacao` |
| **17** | `criar_usuario` | `nome`, `email`, `papel:"admin"\|"cliente"`, `cliente_ids[]?`, `telefone?`, `recebe_alertas?`, `recebe_pos_venda?` | "manda email para fora e dá acesso". Senha gerada no servidor + boas-vindas obrigatória (F4-11). Recusas: `email_em_uso`, `boas_vindas_indisponivel`, cliente sem carteira, admin com carteira, cliente inexistente. Efeito: "envia email de boas-vindas para <email> com o link e a senha inicial". Ressalva no resultado se o email falhou: "o email não saiu; a pessoa não tem a senha — redefina pela tela" | `acoes.CriarUsuario` |
| 17 | `editar_usuario` | `usuario_id`, `nome?`, `telefone?`, `papel?`, `adicionar_clientes[]?`, `remover_clientes[]?`, `recebe_alertas?`, `recebe_pos_venda?` (ao menos um) | "muda acesso a dados de cliente". Recusas: `proprio_usuario` (papel), `ultimo_admin`, carteira vazia para cliente, excluído/inexistente. Efeito ao rebaixar: "revoga as conexões de IA dessa pessoa" | `acoes.EditarUsuario` (merge da carteira no `Preview`/`Executar`) |
| 17 | `desativar_usuario` | `usuario_id` | "corta o acesso". Recusas: `proprio_usuario`, `ultimo_admin`, `sem_mudanca` (já inativo). Efeitos: "login cortado em até 60 s; conexões de IA dela revogadas" | `acoes.EditarUsuario{Ativo:false}` |
| **18** | `desativar_cliente` | `cliente_id` | "bloqueia o login dos usuários do cliente". Recusa `sem_mudanca`. Ressalvas: sessões abertas duram até 8 h; chaves de API, webhooks e monitoramento das campanhas continuam | `acoes.DefinirAtivoCliente` |
| 18 | `ativar_cliente` | `cliente_id` | "o cliente volta a entrar". Recusa `sem_mudanca` | idem |
| 18 | `apagar_cliente` | `cliente_id` | "irreversível". Recusa `cliente_com_vinculos` (com as contagens). Efeitos: o que vai junto (chaves de API, entregas de webhook, PMM no target, pós-vendas) | `acoes.ApagarCliente` |
| 18 | `apagar_material` | `material_id` | "irreversível". Recusas: `material_vinculado` (lista campanhas), `material_com_historico` (MCP). Ressalva: o arquivo master e os hashes ficam no servidor | `acoes.ApagarMaterial` |
| **19** | `editar_webhook_cliente` | `cliente_id`, `url?`, `ativo?`, `eventos?[]` (ao menos um; sem segredo) | "dado do cliente passa a sair para outro sistema". Recusas: URL inválida/http/SSRF (mensagens de `ValidarWebhook` em português), URL com credencial, `eventos` vazio, evento desconhecido, `sem_mudanca`. Preview mostra a URL inteira (a pessoa aprova o destino); ressalva "sem segredo configurado, as entregas não são assinadas — configure pela tela" quando `has_secret=false` | `acoes.EditarWebhook` |
| 19 | `testar_webhook` | `cliente_id` | "manda um evento de teste para o sistema do cliente". Recusa `webhook_teste_descartado` (qual condição). Resultado `{enfileirado:true}` + ressalva "a entrega é assíncrona; confira em instantes com listar_entregas_webhook" | `acoes.TestarWebhook` |
| **20** | `reenviar_pos_venda` | `pos_venda_id`, `destinatario_id` | "manda email para fora". Recusas: rascunho, destinatário de outro relatório ou revogado (`nao_encontrado`). Efeito: "reenvia o MESMO link para <email>" | `postsale.Service.Resend` |
| 20 | `revogar_destinatario_pos_venda` | `pos_venda_id`, `destinatario_id` | "irreversível: o link morre e não há como emitir outro para esta pessoa (o documento não se republica)". Recusas: de outro relatório, `sem_mudanca` (já revogado) | `postsale.Repo.RevokeRecipient(rid, dono)` |

Notas por task:
- **15:** `apagar_campanha` resultado `{apagada:{id, nome}}` (não há `obter_campanha` depois). Teste de paridade do `cancelar_campanha` compara também as chamadas `StopWorkers` do `SupervisorFalso`.
- **16:** extraia de `tools_veiculacoes.go` (closure de `obter_veiculacao`, `:260-331`) a função `montarVeiculacao(ctx, L, id) (saidaObterVeiculacao, error)` num commit `refactor(mcp): …` antes da tool; as três tools devolvem esse formato. Teste de prova: ignorar a tocada `in_slot` de uma célula de cota 1 com duas tocadas → a outra vira `in_slot` (mesmo cenário do `TestResettle_ReenteringPlayRetakesTheQuota`).
- **17:** `DepsModulo`/`Escritas` ganham o `*welcome.Service` (o mesmo de `main.go`); gere a senha com `crypto/rand` em `acoes` (função `senhaAleatoria()`, testada: comprimento, alfabeto, sem repetir em 1 000 chamadas). Teste: a senha gerada nunca aparece no resultado, nos `args` da pendente nem no `audit_log`. "Último admin" conta `role='admin' AND is_active AND deleted_at IS NULL`.
- **18:** "histórico do material" = `EXISTS detections WHERE commercial_id = $1` (sem filtro de aprovação: retratada também é histórico).
- **19:** `DepsModulo` ganha o `*webhook.Outbox`. Rode os testes com `RADIOCHECK_ENV` vazio (produção) e um caso com `development` + loopback.
- **20:** `destinatarioDTO` ganha `id` (não é segredo) em `destinatarios_pos_venda`/`obter_pos_venda` — commit `feat(mcp): id do destinatário na leitura do pós-venda` antes das tools; atualize os testes de forma dessas leituras. `DepsModulo` ganha o `*postsale.Service`. Todo texto de erro de email passa por `semSegredoNoTexto`. Mailer do teste: o falso já usado em `postsale/publish_test.go`.

---

### Task 21: Tela `/pendentes`

**Files:** Create `frontend/src/pages/PendentesPage.jsx`, `PendentesPage.css`, `frontend/e2e/mcp-pendentes.spec.js`; Modify `frontend/src/api/hooks.js`, `frontend/src/App.jsx`, `frontend/src/components/account/McpIntegrationSection.jsx`, `frontend/public/_headers`

- [ ] **Step 1: Leia** `frontend/src/pages/AuthorizeAiPage.jsx` (guarda de moldura, padrão de erro `err?.response?.data?.error`), `McpIntegrationSection.jsx`, os hooks MCP de `hooks.js:1750-1790`, `docs/architecture/frontend-design-system.md` (tokens, `.btn`, estado vazio) e, como referência de comportamento, `hub/frontend/src/pages/Pendentes.tsx` (origin/main). **Não rode `npm install`** (regra 5); se `node_modules` faltar, `npm ci`.
- [ ] **Step 2: Teste Playwright que falha** (`e2e/mcp-pendentes.spec.js`, no padrão de `e2e/mcp-autorizar.spec.js`: `sessaoAdmin(page)` + `page.route` falso):
  1. Lista com dois pedidos → dois cartões com resumo, motivo, mudanças "de → para", efeitos e prazo em horário de SP.
  2. `?id=<executada>` → o cartão destacado aparece mesmo fora das abertas, sem botões, com "Já foi executada."
  3. Aprovar → `POST …/aprovar`; toast "Ação executada"; a lista recarrega.
  4. 422 → a mensagem do servidor aparece e o pedido segue com os botões.
  5. Recusar → `POST …/recusar`, toast "Pedido recusado".
  6. Lista vazia → estado vazio "Nenhum pedido esperando você".
  7. Dentro de iframe → "Abra numa aba própria", sem botões.
  8. Usuário cliente → não acessa (redireciona como as outras rotas admin).
- [ ] **Step 3: Implemente.** Hooks `usePendentesMcp(id)` (`['mcp','pendentes', id]`), `useAprovarPendenteMcp`, `useRecusarPendenteMcp` (invalidam `['mcp','pendentes']`). Rota `/pendentes` em `App.jsx` dentro do `AppShell` com `<RequireRole roles={['admin']}>`. Estados visuais: `pendente` (vencida pelo relógio do navegador = "expirada", sem botões), `aprovada` ("Executando…"), `executada`, `falhou` ("A execução foi interrompida: confira se a ação aconteceu e, se não, peça de novo à IA."), `recusada`, `expirada`. Texto do preview renderizado como texto (React escapa; nada de `dangerouslySetInnerHTML`). Botões desabilitados enquanto age. `_headers`:
```
/pendentes
  X-Frame-Options: DENY
  Content-Security-Policy: frame-ancestors 'none'
/pendentes/*
  X-Frame-Options: DENY
  Content-Security-Policy: frame-ancestors 'none'
```
Link "Pedidos da IA" (com contagem das abertas, se o hook for barato) em `McpIntegrationSection.jsx`.
- [ ] **Step 4:** `cd frontend && npx playwright test e2e/mcp-pendentes.spec.js` verde; `npm run build` verde; `git diff --stat frontend/package*.json` **vazio**. Commit `feat(frontend): /pendentes — aprovação dos pedidos da IA`.

---

### Task 22: Instruções, inventário e desempenho

- [ ] **Step 1:** `Instrucoes`: no parágrafo dos três níveis, `aprovarEm` → `aprovar_em`; a linha 14 ("Hoje o crítico ainda não gera pedido…") vira: "Escrita que mexe em veiculação já ocorrida (cobrança, números que o cliente viu) sobe para crítico; o motivo diz qual condição escalou. Um pedido crítico só acontece quando a pessoa aprova em /pendentes: use minhas_pendentes para saber o estado e só diga que foi feito quando ele estiver executada." Atualize o comentário do topo de `instrucoes.go`. `TestInstrucoes_EscritaCriticoBarrado` → `TestInstrucoes_EscritaCritico` (asserta `aprovar_em`, "entregue o link e diga que nada aconteceu ainda", "minhas_pendentes", "executada", "NUNCA reenvie o token de confirmação sozinho"; e que "Hoje o crítico ainda não" sumiu).
- [ ] **Step 2:** `TestToolsEsperadas_OrdemAlfabeticaE64` → `…E83`. Inventário: as 18 novas com `Critico`; crítica declarada **sem** `confirmacao` no schema; toda escrita tem executor (`indexarEscritas(escritasDe(cfg))` cobre `escritasEsperadas` inteiro); lista de campos proibidos ganha `segredo`, `secret`, `webhook_secret`; `minhas_pendentes` com `ReadOnlyHint`.
- [ ] **Step 3:** `BenchmarkNovoServidor` e `BenchmarkPedidoToolsList` com 83 tools; registrar em `docs/features/mcp.md`. Se o pedido inteiro passar de ~12 ms, memoizar a parte estática do registro (schemas inferidos) por processo e remedir — não mude o desenho sem registrar o número antes/depois.
- [ ] **Step 4:** Commit `feat(mcp): instruções e inventário do crítico`.

### Task 23: Documentação

- [ ] `docs/features/mcp.md`: "Tools (83)" com a tabela da Fatia 4 (motivo, recusas, função chamada); seção "Crítico e /pendentes" (pedido, dedup F4-3, estados F4-2, aprovação F4-6, auditoria F4-7, varredor F4-8, segredos F4-11, guardas F4-12); o que saiu do catálogo e por quê (F4-1); as recusas que só o MCP faz (F4-10); limitações; `ultima-verificacao`.
- [ ] `docs/operations/mcp.md`: a tela `/pendentes` e o diagnóstico (pedido preso em `aprovada` → o varredor marca `falhou` em 10 min; SQL read-only para ver pedidos de um usuário); o varredor (intervalo, lock, o que poda); "Conferência com dado real" ganha o passo do crítico numa campanha de TESTE (pedir → aprovar → conferir `audit_log` com `pendente_id`); renumeração no merge (`0070→0071`, `0071→0072`, `0072→0073`).
- [ ] Spec: §4.6 (auditoria no executor, F4-7), §5.2 (estados do hub, dedup, chaves snake_case), §6 Fatia 4 (catálogo final F4-1). `docs/README.md` se algo mudou de lugar. `docs/features/post-sale.md`, `welcome-onboarding.md`, `client-deactivation.md`, `webhooks.md`: uma linha "o MCP faz isto pela tool X (crítica)" onde couber.
- [ ] Commit `docs(mcp): Fatia 4 — crítico e /pendentes`.

### Task 24: Handoff da Fatia 5 e memória

- [ ] `docs/superpowers/HANDOFF-AAAA-MM-DD-mcp-fatia-5.md` no formato dos anteriores: estado, ambiente (`d4`), convenções novas (registro único, executor, store, varredor), armadilhas, pendências e follow-ups **das rotas** que o levantamento achou e esta fatia não corrigiu: `PATCH /post-sale/reports/{id}` com só `blocks` reescreve relatório enviado (`ReplaceBlocks` sem guarda de rascunho — apaga as chaves dos assets dos links públicos); `DELETE /campaigns/{id}` pausa antes da tx (falha deixa `cancelada` sem `cancelled_at`) e dá 500 com lote manual/pós-venda; `PUT /campaigns/{id}/start` reativa cancelada/concluída; `webhook-test` responde 202 quando descarta; desativar cliente não corta sessão, chave de API nem webhook; `PATCH /admin/users` sem guarda de si mesmo/último admin; senha da criação de usuário em claro no corpo; `DELETE /materials` deixa tocadas órfãs; `RevokeRecipient` não confere o relatório.
- [ ] Memória `mcp-emonitor-plan.md` atualizada (e o índice `MEMORY.md`). Commit `docs(mcp): handoff da Fatia 5`.

### Task 25: Verificação final (com aprovação ponta a ponta)

- [ ] `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` (com e sem `-tags avaliacao`), `go vet ./...`, `go list -deps ./cmd/api | grep avaliacao` vazio.
- [ ] Testes (bancos recriados do `d4` entre pacotes, `-p 1`): `./internal/mcp/...`, `./internal/acoes/...`, `./internal/api/handlers/`, `./internal/api/` (authz), `./internal/supervisor/`, `./internal/postsale/`, `./internal/welcome/`, `./internal/users/`, `./internal/catalog/` (só as 4 falhas pré-existentes conhecidas + o flaky de horário `TestBuildDailySummary_WithDowntime` antes das 13:00 UTC).
- [ ] Avaliação da Fatia 2 ainda 10/10 (`TestAvaliacao_Gabarito`).
- [ ] Frontend: `npx playwright test e2e/mcp-*.spec.js` e `npm run build` verdes; `frontend/package-lock.json` intocado (`git diff origin/master -- frontend/package-lock.json` vazio).
- [ ] **Boot de verdade** (receita do handoff da Fatia 4, "Ambiente de teste"): API + NATS em container + banco do `d4`; token pessoal; `tools/list` = 83 nomes iguais a `ToolsEsperadas`; `cancelar_campanha` de uma campanha semeada → `pendente_id` + `aprovar_em`; `GET /v1/internal/mcp/pendentes` com o JWT do dono lista o pedido; `POST …/aprovar` → 200 e a campanha está `cancelada` com `cancelled_at`; `audit_log` tem `mcp.pendente.criada`, `.aprovada`, `mcp.escrita.cancelar_campanha` (com `pendente_id`) e `.executada`; aprovar com o JWT de outro admin → 404; repetir → 409. Banco, NATS e binário descartados.
- [ ] `git rev-list --count origin/master..HEAD` registrado no handoff. Nada de push.
