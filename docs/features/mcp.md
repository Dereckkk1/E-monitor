---
status: parcialmente-implementado
ultima-verificacao: 2026-11-03
codigo-relacionado:
  - workers/internal/mcp/
  - workers/internal/mcp/oauth/
  - workers/internal/mcp/tokens/
  - workers/internal/mcp/auditoria/
  - workers/cmd/revoga-mcp/main.go
  - workers/cmd/registra-cliente-mcp/main.go
  - migrations/0070_mcp_fundacao.up.sql
  - migrations/0071_mcp_confirmacoes.up.sql
  - frontend/src/pages/AuthorizeAiPage.jsx
  - frontend/src/components/account/McpIntegrationSection.jsx
  - frontend/public/_headers
  - workers/internal/mcp/leitura.go
  - workers/internal/mcp/tools_clientes.go
  - workers/internal/mcp/tools_campanhas.go
  - workers/internal/mcp/tools_grade.go
  - workers/internal/mcp/tools_materiais.go
  - workers/internal/mcp/tools_emissoras.go
  - workers/internal/mcp/tools_veiculacoes.go
  - workers/internal/mcp/tools_usuarios.go
  - workers/internal/mcp/tools_integracoes.go
  - workers/internal/catalog/mcp_leituras.go
  - workers/internal/catalog/clients_integracoes.go
  - workers/internal/mcp/toolkit_relatorios.go
  - workers/internal/mcp/tools_insights.go
  - workers/internal/mcp/tools_gerencial.go
  - workers/internal/mcp/tools_relatorios.go
  - workers/internal/mcp/tools_mapa.go
  - workers/internal/mcp/tools_falhas.go
  - workers/internal/mcp/tools_posvenda.go
  - workers/internal/mcp/tools_auditoria.go
  - workers/internal/mcp/avaliacao/
  - workers/internal/catalog/resumo_relatorio.go
  - workers/internal/catalog/preco_tabela.go
  - workers/internal/supervisor/contar_ativas.go
  - frontend/src/utils/filtrosDaUrl.js
  - frontend/src/api/hooks.js
  - workers/internal/mcp/escrita.go
  - workers/internal/mcp/escritas.go
  - workers/internal/mcp/confirmacao.go
  - workers/internal/mcp/instrucoes.go
  - workers/internal/mcp/tools_escrita_comum.go
  - workers/internal/mcp/tools_escrita_basicas.go
  - workers/internal/mcp/tools_escrita_tipos_material.go
  - workers/internal/mcp/tools_escrita_cadastros.go
  - workers/internal/mcp/tools_escrita_campanhas.go
  - workers/internal/mcp/tools_escrita_vinculos.go
  - workers/internal/mcp/tools_escrita_regras.go
  - workers/internal/mcp/tools_escrita_ajustes.go
  - workers/internal/mcp/mcptest/escrita.go
  - workers/internal/acoes/
  - workers/internal/api/handlers/acoes_http.go
  - workers/internal/catalog/ha_veiculacao.go
  - workers/internal/catalog/campaigns_ciclo.go
  - migrations/0072_mcp_acoes_pendentes.up.sql
  - workers/internal/mcp/pendentes.go
  - workers/internal/mcp/registro_escritas.go
  - workers/internal/mcp/aprovacao.go
  - workers/internal/mcp/pendentes_http.go
  - workers/internal/mcp/varredor.go
  - workers/internal/mcp/tools_pendentes.go
  - workers/internal/mcp/tools_escrita_criticas_campanhas.go
  - workers/internal/mcp/tools_escrita_criticas_veiculacoes.go
  - workers/internal/mcp/tools_escrita_criticas_usuarios.go
  - workers/internal/mcp/tools_escrita_criticas_clientes.go
  - workers/internal/mcp/tools_escrita_criticas_webhooks.go
  - workers/internal/mcp/tools_escrita_criticas_posvenda.go
  - frontend/src/pages/PendentesPage.jsx
  - frontend/src/pages/PendentesPage.css
  - frontend/src/utils/emoldurada.js
  - frontend/e2e/mcp-pendentes.spec.js
---

# MCP do E-monitor

Servidor MCP que dá a um **administrador** acesso ao E-monitor por linguagem natural, a partir do
claude.ai, do Claude Code e do Gemini Enterprise. Desenho completo e fatias:
[spec 2026-10-27](../superpowers/specs/2026-10-27-mcp-server-design.md). Segue o contrato comum
dos MCPs da E-Series (`hub/docs/architecture/mcp-ecossistema.md`). Como conectar, revogar e
diagnosticar: [operations/mcp.md](../operations/mcp.md).

## Estado

| Fatia | O quê | Estado |
|---|---|---|
| 0 | Porta: `/mcp`, OAuth 2.1, tokens, `quem_sou_eu` | implementada (branch `feat/mcp-server`; deploy e conexão dos 3 clientes em prod pendentes) |
| 1 | Leitura: 26 tools (clientes, campanhas, grade, plano do dia, materiais, emissoras, veiculações, usuários, integrações, notificações) | implementada (branch `feat/mcp-server`, não deployada) |
| 2 | Relatórios: 17 tools (insights, visão gerencial, assertividade, data e hora, resumo e exportações de campanha, mapa de cobertura, 5 de falhas, 3 de pós-venda, auditoria do MCP) + telas que abrem filtradas pela URL + avaliação 10/10 | implementada (branch `feat/mcp-server`, não deployada) |
| 3 | Escrita `livre` e `confirma`: 20 tools + motor de níveis (token HMAC de uso único, escalada a `critico` reavaliada a cada chamada) + camada `internal/acoes` compartilhada com as rotas HTTP | implementada (branch `feat/mcp-server`, não deployada; ver "Escrita (Fatia 3)") |
| 4 | Escrita `critico` executável: 18 tools que viram pedido pendente (`mcp_acoes_pendentes`), a tela `/pendentes` onde o admin que pediu aprova, o varredor e `minhas_pendentes`; torna executáveis também as escaladas das 20 tools da Fatia 3 | implementada (branch `feat/mcp-server`, não deployada; ver "Crítico e /pendentes (Fatia 4)") |
| 5 | Ecossistema: hub (linha "Pontes", `SEM_MIDIA_NO_HUB`, §1 do contrato), ADRs | planejada |

Tools hoje: as 83 da seção "Tools" (27 das Fatias 0 e 1 + 17 da Fatia 2 + 20 da Fatia 3 + 19 da Fatia 4: 18 escritas críticas e
`minhas_pendentes`). O `status` deste doc vira `implementado` quando a Fatia 5 fechar.

## Regra de ouro: o MCP faz parte do pronto

- **Toda feature nova ganha tool** (ou decide por escrito que não ganha).
- **Tool nova entra em `ToolsEsperadas`** ([`servidor.go`](../../workers/internal/mcp/servidor.go))
  **no mesmo commit.** O teste de inventário trava o **conjunto** (clientes guardam o
  `tools/list` em cache, então nada entra ou sai por acidente). A lista está em ordem alfabética
  porque é a ordem em que o go-sdk devolve o `tools/list`; não é uma escolha nossa.
- **Tool nunca reimplementa regra de negócio**: chama a MESMA função que o handler HTTP chama
  (`catalog.ApprovedDetectionsFilter`, base financeira `in_slot + bonus`, `categorizer.Settle`,
  tratamento de `cancelada`). Tela e MCP não podem discordar de número. Na escrita, essa função mora em
  `internal/acoes` (ver "Escrita (Fatia 3)"): handler e tool chamam a mesma ação, com os mesmos gatilhos.

## Tools (83)

### Fatias 0 e 1 (27)

Todas de leitura (`readOnlyHint`). Cada uma chama a MESMA função que a tela; a coluna "Fonte" diz qual.
Listas: `{items, page: {total, limit, offset, returned, has_more}}`, `limit` padrão 25 e teto 100,
`offset` até 100000 (acima disso `[entrada_invalida]`).

| Tool | Devolve | Fonte |
|---|---|---|
| `quem_sou_eu` | dono da credencial | — |
| `listar_clientes` | clientes com `hub_id` | `GET /clients` (paginado) -> `Clients.ListPaged` |
| `obter_cliente` | cadastro, vínculos, carteira | `Clients.Get`, `CountDependents`, `users.Repo.List{ClientID}` |
| `pmm_target_cliente` | PMM e PMM no target por emissora | `GET /clients/{id}/target-pmm` |
| `listar_campanhas` | campanhas (canceladas fora por padrão) | `GET /campaigns` -> `Campaigns.ListPaged` |
| `obter_campanha` | campanha por id ou código do hub | `Campaigns.Get` / `ListByHubCode` |
| `financeiro_campanhas` | investido, bonificação, impactos, CPM | `GET /campaigns/financials` + `CPMDaCampanha` |
| `listar_materiais_campanha` | materiais da campanha | `GET /campaigns/{id}/materials` |
| `listar_regras_distribuicao` | regras | `GET /campaigns/{id}/distribution-rules` |
| `listar_overrides` | ajustes do dia | `GET /campaigns/{id}/distribution-overrides` |
| `listar_precos_campanha` | preços por emissora | `GET /campaigns/{id}/pricing` |
| `grade_campanha` | grade emissora x tipo x dia + totais | `GET /campaigns/{id}/daily-summary` |
| `plano_do_dia` | saldo, meta, faixas e tocadas de UMA célula | grade + regras (`categorizer.RuleCoversDay`) + ajuste + `GET /detections` |
| `listar_materiais` | materiais de um cliente | `GET /clients/{id}/materials` |
| `obter_material` | material, similaridade, campanhas | `GET /materials/{id}` + `CampaignMaterials.ListByMaterial` |
| `listar_tipos_material` | tipos | `GET /material-types` |
| `buscar_emissoras` | emissoras (busca da tela) | `GET /stations` |
| `obter_emissora` | ficha, Anatel, cidades no alcance, campanhas | `GET /stations/{id}` + `Stations.Anatel`/`CoverageCities` |
| `listar_veiculacoes` | veiculações com total real | `GET /detections?page=` -> `Detections.ListPaged` |
| `obter_veiculacao` | veiculação, atribuições, aprovação | `GET /detections/{id}` + `Attributions` + `Approved` |
| `veiculacoes_por_material` | contagem por material | `GET /detections/aggregate-by-material` |
| `listar_usuarios` | usuários com estado do convite | `GET /admin/users` |
| `obter_usuario` | usuário | `GET /admin/users/{id}` |
| `obter_webhook_cliente` | configuração (sem segredo, URL só esquema+host) | `GET /clients/{id}/webhook` |
| `listar_entregas_webhook` | entregas | `GET /clients/{id}/webhook-deliveries` |
| `listar_chaves_api` | chaves (só metadados) | `GET /clients/{id}/api-keys` |
| `minhas_notificacoes` | sininho de quem está conectado (teto de 50 itens, dito na ressalva) | `GET /admin/notifications` |

### Fatia 2 — relatórios (17)

Mesma regra: cada tool chama a função que a tela chama, com teste de paridade contra o handler HTTP. Toda tool de
relatório devolve `links: {tela, como_baixar?}` (ver "Relatórios: deep-link e downloads").

| Tool | Devolve | Fonte | Link (`links.tela`) |
|---|---|---|---|
| `insights_campanha` | impactos (e no target), por categoria, investido/bonificação, CPM, demografia, série | `catalog.Insights.Compute` (`/insights`) | `/insights?client_id&campaigns&from&to[&stations][&materials]` |
| `visao_gerencial` | KPIs da operação (sem a lista de emissoras do mapa nem o feed); `emissoras_ao_vivo` null fora da API | `catalog.ManagementOverview.Get` + `supervisor.ContarAtivas` | `/management?[client_id][&campaigns][&status]&from&to` |
| `assertividade` | último mês fechado e o anterior, variação em p.p., manuais desculpadas | `assertiveness.Repo.Get` | `/management?[client_id][&campaigns]` |
| `relatorio_data_hora` | veiculações de UM cliente com PMM, PMM no target e valor de tabela | `Detections.ListPaged` + `Pricing.ListByCampaign` + `catalog.PrecoDeTabela` | `/reports/airtime?client_id&competence&campaigns&from&to[&q]` |
| `resumo_relatorio_campanha` | totais, por material (até 50), por emissora (paginada) | `Detections.AggregateByMaterial`/`AggregateByStation` + `catalog.TotaisDoResumo`/`ImpactosDaEmissora` | `/reports/airtime?…` + `como_baixar` |
| `exportar_consolidado` | nº de linhas do CSV Consolidado + onde baixar | `Detections.AggregateByMaterialStation` | `/reports/airtime?…` + `como_baixar` |
| `exportar_veiculacoes` | nº de linhas do CSV Detalhado + onde baixar | `Detections.ListPaged` (o total) | `/reports/airtime?…` + `como_baixar` |
| `mapa_cobertura_campanha` | emissoras com classe e raios Anatel, cidades (20 por emissora), municípios distintos | `LiveMap.Get` + `LiveMap.Coverage` + `LiveCoverageResult.Totals` | `/live-map?campaigns[&client_id]` (client_id só quando todas são de um cliente) |
| `falhas_emissoras` | emissoras com falha no dia (déficit TOTAL, sem o split) | `StationFailures.ListForDate` | `/admin/station-failures?view=by_station&date&min_down` |
| `falhas_campanhas` | modo dia (split "não tocou" × "fora do horário") ou histórico | `CampaignFailures.ListForDate` / `ListHistorical` | `…?view=by_campaign&date` / `…?view=by_campaign&sub=historical` |
| `obter_falhas_campanha` | o drill-in de UMA campanha (por emissora, com o split) | `CampaignFailures.Get` | `…?view=by_campaign&campaign=<id>` + `como_baixar` (PDF de cobrança) |
| `falhas_por_dia` | série diária + dias com falha, pico e média | `FailuresDaily.ListDaily` | `…?view=by_day&from&to&min_down` |
| `resumo_diario_falhas` | o digest de ontem em SP (sem `seen`; nunca marca visto; ver "Fuso das falhas") | `CampaignFailures.ListForDate` | `…?view=by_campaign&date` |
| `listar_pos_vendas` | pós-vendas + contagens por estado | `postsale.Repo.List` | `/admin/pos-venda` (e `/admin/pos-venda/<id>` por item) |
| `obter_pos_venda` | números congelados (com a regra de exibição do `/insights`: `bonificacao` null em bloco 100% consolidado, `cpm`/`cpm_target` null sem impactos), checking (20 por campanha), resumo de destinatários | `Repo.Get` + `Repo.Payload` | `/admin/pos-venda/<id>` |
| `destinatarios_pos_venda` | quem recebeu (com o `id` do destinatário, sem token) ou quem receberia hoje | `Repo.Get`, `ActiveClientUsers`, `InternalRecipients` | — |
| `auditoria_mcp` | eventos gravados pelo MCP, por cursor (`page.total: null`) | `auditoria.Listar` (`metadata->>'origem' = 'mcp'`) | — |

**Tetos de página reais** (o `limit` do schema genérico diz "máximo 100", mas estas tools cortam antes; o teto
está na descrição de cada uma e em `page.limit`): `mapa_cobertura_campanha` 25, `relatorio_data_hora` 50,
`falhas_emissoras` 7, `falhas_campanhas` (modo dia) 8, `obter_falhas_campanha` 50, `obter_pos_venda` 5
campanhas por página, `listar_pos_vendas` 50, `destinatarios_pos_venda` 50, `auditoria_mcp` 20. Listas DENTRO de um item (quedas,
campanhas, emissoras, cidades, linhas de checking, dias de falha) saem cortadas em 20, com o `*_total` real e
uma ressalva. Nas tools de falha o `cliente` sai como `ref {id, nome}`, como no resto do MCP (o
`StationFailures` passou a trazer `client_id` só para isso). O alvo é cada resposta abaixo de 60 KB (o cliente MCP corta a saída em ~25k tokens), travado por
teste nas tools de lista larga.

### Fatia 3 — escrita (20)

Cada tool chama a MESMA ação da rota HTTP (`internal/acoes`, ou o mesmo método de repositório quando a rota já era uma
chamada só) e dispara os mesmos gatilhos. Nível, fluxo do token e respostas: [Escrita (Fatia 3)](#escrita-fatia-3). A
descrição de toda tool termina em `Nível: livre|confirma|critico.`. **Sobe para `critico`** significa: a tool grava um pedido
pendente e devolve `aprovar_em` (`/pendentes?id=…`); nada acontece até o admin que pediu aprovar na tela (ver "Crítico e
/pendentes (Fatia 4)"). Na Fatia 3 o crítico era só barrado.

| Tool | Faz | Nível | Sobe para `critico` quando | Chama |
|---|---|---|---|---|
| `marcar_notificacoes_lidas` | marca lidas as notificações do próprio admin (`notificacoes[]` ≤ 200 **ou** `todas: true`) | livre | nunca | `Notifications.MarkRead` / `MarkAllReadInWindow` (por usuário) |
| `reconhecer_similaridade_material` | dispensa o aviso de similaridade ≥ 50% de um material | livre | nunca | `Materials.Acknowledge` (material inexistente = `nao_encontrado`; a rota dá 204) |
| `criar_tipo_material`, `editar_tipo_material` | cria/edita tipo (nome único, cor `#rrggbb`, descrição; edição parcial) | confirma | nunca | `MaterialTypes.Create` / `Update` (nome repetido = `nome_duplicado`, a rota dá 500) |
| `editar_material` | título, roteiro (`""` limpa) e/ou tipo | confirma | muda o **tipo** e o material tem veiculação aprovada (a troca recategoriza as veiculações dele em TODAS as campanhas) | `Materials.UpdateTitle` / `UpdateScript`, `acoes.TrocarTipoMaterial` |
| `criar_cliente` | cria cliente (CNPJ opcional e único) | confirma | nunca | `acoes.CriarCliente` (+ evento `cliente.upsert` ao hub) |
| `editar_cliente` | edição parcial do cadastro; **sem** `hub_id` (nenhuma rota o grava) | confirma | nunca | `acoes.ChecarCNPJ` (só quando os 14 dígitos mudam) + `acoes.EditarCliente` (não avisa o hub, como a rota) |
| `editar_pmm_target` | PMM no target por (cliente, emissora): `entradas[{emissora_id, pmm_target: int \| null}]` ≤ 500; `null` **remove** a linha (≠ 0) | confirma | o cliente tem veiculação aprovada em emissora do lote **ou** tem campanha não cancelada com alguma delas já iniciada (início < hoje SP): o valor é retroativo, sem versionamento | `ClientStationPMM.BulkUpsert` |
| `editar_emissora` | nome, frequência, cidade, UF, PMM (nunca `stream_url`, banda, logo nem metadados) | confirma | o `pmm` muda **e** a emissora tem veiculação aprovada (`stations.pmm` entra nos impactos de todas as campanhas que a usam, inclusive dias passados) | `acoes.EditarCadastroEmissora` → `Stations.UpdateCadastro` (`COALESCE`) + geocodificação |
| `criar_campanha` | cria campanha (nasce `programada`; emissoras e código do hub opcionais) | confirma | `inicio` ≤ hoje em SP: o agendador a ativaria em ≤ 60 s (sobe workers, o cliente passa a ver) — é um "iniciar campanha" | `acoes.CriarCampanha` (barreira do código do hub, `campanha.upsert`) |
| `editar_campanha` | nome, `inicio`, `fim`, `codigo_hub` (`""` apaga) | confirma | (a) as novas datas mudam o estado que o agendador dá (`catalog.EstadoPeloCiclo`, a regra do `PromoteScheduledLifecycle`); (b) muda o conjunto de dias já passados do período; (c) muda o código do hub (é a ponte). Renomear e esticar o fim de campanha em andamento não escalam | `acoes.EditarCampanha` |
| `editar_emissoras_campanha` | `adicionar[]` / `remover[]` emissoras-alvo (nunca "a lista inteira") | confirma | **nunca** — decisão do dono (Dereck, 2026-10-29), depois de `TestEditarEmissorasCampanha_ProvaDaRemocao` provar que os números passados ficam idênticos; muda só as listas, o mapa ao vivo, os alertas, o PMM no target e os workers | `acoes.TrocarEmissorasCampanha` |
| `vincular_material`, `desvincular_material`, `editar_emissoras_material` | vínculo material↔campanha e as emissoras dele (`adicionar[]`/`remover[]`) | confirma | **nunca** (`TestVinculos_ProvaF34_PassadoNaoMuda`: atribuição é decidida na entrada da tocada). As ressalvas do preview: outra campanha com o mesmo áudio nas mesmas emissoras **disputa a atribuição** (o preview diz quem leva) e webhook de detecção ainda não entregue pode ser descartado | `acoes.VincularMaterial` / `DesvincularMaterial` / `TrocarEmissorasMaterial` (+ `Reload` se a campanha está ativa) |
| `criar_regra_distribuicao`, `editar_regra_distribuicao` | regra por TIPO (`dias[]` em vez de bitmask, faixa HH:MM, `insercoes_por_dia` 1–100); edição parcial (emissoras e materiais por `adicionar_*`/`remover_*`) | confirma | a campanha tem veiculação aprovada do(s) tipo(s) que o recat varre (criar: o novo; editar: o novo **e** o antigo) **ou** um dia passado da vigência (∩ período da campanha) está coberto pela máscara de dias da semana da versão antiga ou da nova. **Editar só o nome escala igual**: o recat roda de novo | `acoes.CriarRegra` / `EditarRegra` (recat do tipo em segundo plano, ≤ 30 s) |
| `apagar_regra_distribuicao` | apaga UMA regra da campanha | confirma | a campanha tem **qualquer** veiculação aprovada (apagar varre todos os tipos) **ou** um dia passado da vigência está coberto pela máscara | `acoes.ApagarRegra` (recat da campanha inteira em segundo plano) |
| `definir_override`, `apagar_override` | ajuste do dia de UMA célula (campanha × tipo × emissora × dia): meta `insercoes` 0–100 e faixa | confirma | `data` < hoje em SP **ou** a célula-dia já tem veiculação aprovada | `acoes.DefinirAjuste` / `ApagarAjuste` (recat **síncrono** da célula-dia; o resultado traz o plano do dia recalculado) |

Notas: toda tool que mexe em campanha recusa a **cancelada** com `[campanha_cancelada]` (a tela não recusa, mas o
[tratamento de cancelada](cancelled-campaign-handling.md) a tira das telas operacionais); `insercoes: 0` num override é
**meta zero**, não "nada esperado": toda tocada da célula-dia vira `bonus`. A resposta de cada tool traz o registro depois
da escrita no formato da tool `obter_*`/`listar_*` correspondente.

### Fatia 4 — escrita crítica (18) e `minhas_pendentes`

Todas as 18 são `critico` por declaração (o `Nivel` da tool): **nunca executam na chamada**. A tool roda o `Preview`, grava o
pedido e devolve `aprovar_em`; a escrita só roda quando o admin aprova em `/pendentes` e chama a MESMA função que a tela chama
(`internal/acoes`, ou o método do repositório quando a rota já era uma chamada só). Nenhuma tem campo `confirmacao`. O
resultado de cada uma é o registro como ficou, no formato da tool `obter_*` correspondente. Fluxo, estados e garantias:
[Crítico e /pendentes (Fatia 4)](#crítico-e-pendentes-fatia-4).

| Tool | Entrada | Por que é crítica (`motivo`) | Recusas só do MCP (ou que a tool nomeia) | Chama |
|---|---|---|---|---|
| `cancelar_campanha` | `campanha_id` | congela o déficit e tira a campanha das telas operacionais e da cobrança | `estado_terminal` (só programada/ativa se cancela). Ressalva diz o que fez com os workers | `acoes.CancelarCampanha` (+ `StopWorkersForCampaign`) |
| `apagar_campanha` | `campanha_id` | irreversível: apaga a campanha e tudo que é dela | `campanha_com_historico` (qualquer veiculação própria ou projeção, comprovante manual ou pós-venda: "cancele em vez de apagar"). A rota apaga as próprias veiculações; o MCP não | `acoes.ApagarCampanhaCom{ExigirSemHistorico}` |
| `editar_cpm_fixo` | `campanha_id`, `cpm_fixo` (obrigatório, aceita `null` = volta ao dinâmico) | muda o CPM que o cliente vê | `campanha_cancelada`, `sem_mudanca`, `entrada_invalida` (negativo, > 2 casas, ≥ 1e10) | `acoes.DefinirCPMFixo` |
| `editar_precos_campanha` | `campanha_id`, `precos[]` (1–300 emissoras; cada item substitui o preço da emissora) | dinheiro: muda investido, bonificação e CPM, inclusive dos dias já veiculados | `entrada_invalida` (emissora fora da campanha ou repetida, modo/valores), `nao_encontrado` (tipo de material), `campanha_cancelada`, `sem_mudanca`. Tudo ou nada numa transação | `acoes.DefinirPrecos` → `Pricing.UpsertLote` |
| `criar_veiculacao_manual` | `campanha_id`, `emissora_id`, `material_id`, `quando` (RFC3339 COM fuso), `nota?` | entra na cobrança e pode mudar a categoria das outras veiculações do mesmo dia | `material_nao_vinculado`, instante no futuro, `campanha_cancelada`. **Sem comprovante** (o PDF só entra na criação pela tela; a descrição manda perguntar). Preview avisa possível duplicata (mesmo material × emissora × campanha a ±10 min), sem recusar | `acoes.CriarVeiculacaoManual` |
| `ignorar_veiculacao` | `veiculacao_id` | a vaga liberada pode promover outra veiculação do mesmo dia | `ja_ignorada` | `acoes.IgnorarVeiculacao` |
| `restaurar_veiculacao` | `veiculacao_id` | a veiculação volta a disputar a cota do dia e pode rebaixar outra | `nao_ignorada`. Não desfaz retratação nem rejeição do audit | `acoes.RestaurarVeiculacao` |
| `criar_usuario` | `nome`, `email`, `papel`, `cliente_ids?`, `telefone?`, `recebe_alertas?`, `recebe_pos_venda?` (**sem senha**) | manda email para fora e dá acesso | `boas_vindas_indisponivel` (sem `WELCOME_ENC_KEY`), `email_desligado` (sem SMTP), `email_taken`, `client_id_required`, `client_id_not_allowed_for_admin`, `client_not_found`, e-mail inválido | `acoes.CriarUsuario` (+ `acoes.SenhaAleatoria`) |
| `editar_usuario` | `usuario_id`, `nome?`, `telefone?`, `papel?`, `adicionar_clientes?`, `remover_clientes?`, `recebe_alertas?`, `recebe_pos_venda?` | muda acesso a dados de cliente | `proprio_usuario` (mudar o próprio papel), `ultimo_admin`, `usuario_apagado`, `sem_mudanca`, `client_id_required`, `client_id_not_allowed_for_admin`, `client_not_found`. Carteira calculada na hora da aprovação | `acoes.EditarUsuario` |
| `desativar_usuario` | `usuario_id` | corta o acesso | `proprio_usuario`, `ultimo_admin`, `usuario_apagado`, `sem_mudanca` (já inativo) | `acoes.EditarUsuario{Ativo:false}` |
| `desativar_cliente` | `cliente_id` | bloqueia o login dos usuários do cliente | `sem_mudanca`. Ressalvas: sessão aberta dura até 8 h; chaves de API, webhooks e monitoramento seguem | `acoes.DefinirAtivoCliente` |
| `ativar_cliente` | `cliente_id` | o cliente volta a entrar | `sem_mudanca` | `acoes.DefinirAtivoCliente` |
| `apagar_cliente` | `cliente_id` | irreversível: apaga o cliente e o que depende só dele (chaves de API, webhooks, PMM no target e pós-vendas) | `cliente_com_vinculos` (com as contagens: campanhas, materiais, usuários inclusive inativos e apagados) | `acoes.ApagarCliente` |
| `apagar_material` | `material_id` | irreversível: apaga o material da biblioteca (master e hashes ficam no servidor) | `material_vinculado` (lista as campanhas), `material_com_historico` (qualquer veiculação, também retratada ou ignorada, **e** `detection_campaigns`) | `acoes.ApagarMaterial` |
| `editar_webhook_cliente` | `cliente_id`, `url?`, `ativo?`, `eventos?[]` (ao menos um; **sem segredo**) | dado do cliente passa a sair para outro sistema | `webhook_nao_configurado` (sem URL gravada, só `url` cria a configuração), `sem_mudanca`, `entrada_invalida` (URL inválida, http fora de dev, rede interna/SSRF, URL com `usuario:senha@`, evento desconhecido). Preview mostra a URL nova inteira | `acoes.EditarWebhook` (+ `ValidarWebhook`) |
| `testar_webhook` | `cliente_id` | manda um evento de teste para o sistema do cliente | `webhook_teste_descartado` (diz a condição: sem URL, desligado ou sem `webhook.test`/`*` nos eventos), `webhook_indisponivel` (sem a fila de entregas). Resultado `{enfileirado:true}`; entrega assíncrona | `acoes.TestarWebhook` |
| `reenviar_pos_venda` | `pos_venda_id`, `destinatario_id` | manda email para fora | `nao_encontrado` (rascunho, destinatário de outro relatório ou revogado), `email_desligado`, `pos_venda_indisponivel`, `pos_venda_sem_documento` (enviado antes do congelamento, sem payload) | `postsale.Service.Resend` |
| `revogar_destinatario_pos_venda` | `pos_venda_id`, `destinatario_id` | irreversível: o link morre e não há como emitir outro (o documento não se republica) | `nao_encontrado` (rascunho ou destinatário de outro relatório), `sem_mudanca` (já revogado). Ressalva especial quando era o último link ativo | `postsale.Repo.RevokeRecipient` |
| `minhas_pendentes` (leitura) | `estado?`, paginação | — | `entrada_invalida` (estado desconhecido) | `Pendentes.Listar` (sempre do dono do token) |

Notas: `destinatarios_pos_venda` e `obter_pos_venda` passaram a trazer o `id` do destinatário (não é segredo), que as duas tools de
pós-venda pedem. Toda mensagem de erro de email passa por `semSegredoNoTexto`. `minhas_pendentes` devolve por padrão só os
abertos (`pendente` e `aprovada`); `estado=todos` traz o histórico; o `erro` sai cortado em 250 caracteres e as `ressalvas`
da execução em 250 cada / 300 somadas (a lista de 50 pedidos tem de caber em ~60 KB). Nunca devolve `args`, `resultado` nem `token_id`.

## Relatórios: deep-link e downloads

- **Nenhum link direto de arquivo** (decisão F2-1). As rotas de CSV/PDF do portal só aceitam
  `Authorization: Bearer` (o frontend baixa por `fetch` + blob): um link direto daria 401 no navegador, e um
  link com credencial seria URL assinada, que o contrato proíbe. As tools devolvem `links.tela` (a tela já
  filtrada) e, quando há arquivo, `links.como_baixar` com o botão a apertar. `exportar_consolidado` e
  `exportar_veiculacoes` dizem quantas linhas o arquivo vai ter (provado por teste contra o CSV real).
- **As telas leem os filtros da URL** (F2-2), com os nomes da API, só no primeiro render; depois limpam a URL
  e seguem com o próprio estado. A validação mora em `frontend/src/utils/filtrosDaUrl.js`: valor vazio ou
  malformado vira "sem filtro" (nunca um pedido que a API recusa) e os ids saem em minúsculas.

| Tela | Parâmetros lidos | Doc |
|---|---|---|
| `/insights` | `client_id`, `campaigns`, `from`, `to`, `stations`, `materials` | [insights-dashboard](insights-dashboard.md#abrir-já-filtrado-pela-url) |
| `/management` | `client_id`, `campaigns`, `status`, `from`, `to` | [management-overview](management-overview.md#abrir-já-filtrado-pela-url) |
| `/live-map` | `client_id`, `campaigns` | [live-map](live-map.md#abrir-já-filtrado-pela-url) |
| `/admin/station-failures` | `view`, `date`, `campaign` (já lia) + `sub=historical`, `min_down` e `from`/`to` (aba "Por dia") | [admin-station-failures](admin-station-failures.md#abrir-já-filtrado-pela-url) |
| `/reports/airtime` | `client_id`, `competence`, `campaigns`, `from`, `to`, `q` (já lia tudo; não mudou) | [airtime-report](airtime-report.md) |

  Todo parâmetro que uma tool põe em `links.tela` é lido pela tela (conferido em 2026-10-28).
- **Campanha fora da página do seletor.** Os seletores de campanha carregam a página 1 de `/campaigns` (200,
  ativas primeiro). Um link para uma campanha concluída antiga traz um id que não está nela: as telas buscam
  essas campanhas à parte (`useCampaignsWithSelected`, via `/campaigns?id=`, que respeita o escopo de quem está
  logado) e as juntam às opções. Sem isso a tela filtrava por elas com o seletor vazio.

## O que nunca sai (Fatia 2)

Além da lista da Fatia 1 (abaixo):

- **Pós-venda:** o `token` de cada destinatário (é a credencial do link público `/pos-venda/:token`, sem
  expiração) e o link público em si — nem como campo, nem dentro de texto livre (título, mensagem, nota do
  checking, erro de email). Toda URL do texto livre, com ou sem esquema (`drive.google.com/…`, `www.x.com/…`,
  `//x.com/…`), vira só o domínio, e um `pos-venda/<token>` solto é apagado. Nenhuma chave S3. O
  `attachments_url` sai só como domínio. Rascunho não tem números (`numeros: null`).
- **URL de anexo e de webhook:** só o domínio (o caminho e a query podem carregar segredo).
- **Auditoria:** o valor de toda chave de `detalhes` que pareça segredo (`secret`, `token`, `senha`, `chave`,
  `api_key`, `authorization`, `cookie`, `jwt`…) sai como `"[omitido]"`, inclusive em objetos aninhados;
  detalhes acima de 2 KB saem resumidos, com ressalva.

## Avaliação (mcp-builder)

Rodada de 2026-10-28: **10/10** (as 10 respostas iguais ao gabarito, sem erro de tool e sem id inventado), por
um avaliador às cegas que só usou o CLI. Cenário, perguntas, gabarito e como rodar:
[operations/mcp.md](../operations/mcp.md#avaliação-mcp-builder).

**O que nunca sai (Fatia 1)** (whitelist nos DTOs): `stream_url` e saúde do stream, `master_storage_path`/`master_sha256`,
`evidence_key`, segredo do webhook (nem o prefixo), `key_hash` (só o prefixo de 8 hex que a tela já mostra, como `identificador`), token e link de boas-vindas (o link mostra a
senha inicial), e o caminho e a query da URL de webhook (podem carregar segredo, como em Slack/Discord/Zapier:
só saem esquema e host, `https://host/...`; vale também para `ultimo_erro` das entregas). Áudio: só o link
`/detections/:id` do portal.

**Comportamento comum**

- Toda tool de leitura roda em `ferramentaLeitura`: um `panic` vira `[interno]` (não derruba a API), `offset`
  acima de 100000 vira `[entrada_invalida]` e resultado nulo vira erro interno.
- Erros: `[entrada_invalida]`, `[nao_encontrado]` ("<x>: não existe no E-monitor"), `[ambiguo]`, `[interno]` e
  `[sem_identidade]` (deve ser inalcançável: a autenticação sempre põe o principal no contexto)
  (o erro Go real só vai para o log, nunca ao modelo).
- Paginação estável: as listas de clientes, campanhas, emissoras, veiculações, usuários e entregas de webhook
  desempatam o `ORDER BY` por `id`.
- Teto de emissoras por item: `listar_materiais_campanha` e `listar_regras_distribuicao` devolvem no máximo 20
  refs em `emissoras` por item, com `emissoras_total` (número real) e uma ressalva quando algo foi cortado. A
  paginação limita itens, não as emissoras dentro deles (campanha de 239 emissoras estourava o limite de saída).
  `obter_campanha` lista todas as emissoras-alvo.
- `listar_veiculacoes` sem campanhas exige período de no máximo 93 dias; `de > ate` é recusado.
- `financeiro_campanhas`: o CPM vem de `catalog.CPMDaCampanha`, que espelha `/campaigns`: sem CPM nenhum (nem o
  fixo) quando investido + bonificação = 0.
- `plano_do_dia` em campanha cancelada acrescenta ressalvas (cancelada; dia posterior ao cancelamento não tem
  obrigação); a ressalva "meta 0 -> bonus" lembra a exceção `out_date`.
- `obter_veiculacao`: `manual.por` é uma referência com o nome do usuário; avisa quando a campanha canônica não
  tem projeção; `aprovada` vem de `Detections.Approved` (usa `catalog.ApprovedDetectionsFilter`).
- `obter_emissora`: `perfil.perfil_audiencia` é um DTO em português; `perfil.site` cai no site do Audiency.

**Decisões da Fatia 1** (plano `docs/superpowers/plans/2026-10-27-mcp-fatia-1-leitura.md`): `plano_do_dia`
devolve os ingredientes autoritativos e **não** o "progresso por faixa" do `DayDetailModal` (heurística só de
tela, em JS); o CPM de `/campaigns` virou `catalog.CPMDaCampanha` (antes só existia no navegador).

## Escrita (Fatia 3)

20 tools (2 `livre`, 18 `confirma`) que fazem o que o portal faz, pela mesma função e com os mesmos gatilhos. Desenho e
decisões F3-1..F3-12: [plano](../superpowers/plans/2026-10-29-mcp-fatia-3-escrita.md) (com as notas de execução datadas) e
[spec §5 e §6](../superpowers/specs/2026-10-27-mcp-server-design.md). A tabela de tools, com nível e escalada de cada uma, está em
"Fatia 3 — escrita (20)".

### O motor de níveis

[`escrita.go`](../../workers/internal/mcp/escrita.go): `Escrita[In]` é **dado** (`Nome`, `Descricao`, `Nivel`, `Escalar`,
`Preview`, `Executar`, `Tela`, `Motivo`, `Recurso`, `ArgsParaAuditoria`) e `ferramentaEscrita` o registra; a Fatia 4 reaproveita o
mesmo valor para executar a pendente aprovada. Fluxo de toda chamada, na ordem:

1. **Nível efetivo.** `Escalar` consulta o banco e roda em **toda** chamada, preview e confirmação (F3-2): o token fixa os
   argumentos, não o estado do mundo. Se entre o preview e a confirmação entrou uma tocada que faz a escrita escalar, a
   confirmação devolve o ramo `critico` e o token **não** é consumido. O nível nunca desce do declarado.
2. **`livre`:** executa na hora (`executou: true`).
3. **Preview** (`confirma` e `critico`): relê o estado, valida e monta `{resumo, mudancas[{campo, de, para}], efeitos[],
   ressalvas[]}` com nomes (não só ids). Roda **antes do ramo crítico e antes do token**: uma recusa de negócio
   (`entrada_invalida`, `nao_encontrado`…) vale antes de qualquer pedido pendente ou consumo de token. O `Escalar`
   (passo 1, que também relê o estado) pode recusar pelo mesmo motivo, então a recusa pode vir de qualquer um dos dois.
4. **`critico`:** grava um pedido pendente e devolve o link `aprovar_em` (ver "Crítico e /pendentes (Fatia 4)"); nunca executa.
5. **`confirma` sem token:** `{executou: false, preview, confirmacao, expira_em}`. **Com token:** verifica, consome, executa.

Respostas (F3-12): executada `{executou: true, resultado, ressalvas?, aviso?}`; preview acima; token inválido, vencido, de
outro usuário, de outra tool, com argumentos mudados **ou já usado** → `{executou: false, erro: "… Peça o preview outra
vez."}` (**não é `isError`**, contrato §3.4); recusa de negócio → `isError` `"<mensagem> [<código>]"`. Códigos: `entrada_invalida`,
`nao_encontrado`, `cnpj_duplicado` (com o cadastro existente na mensagem), `codigo_hub`, `nome_duplicado`,
`campanha_cancelada`, `interno`, `sem_identidade`. Se o resultado não serializar depois de escrito, a resposta é
`{executou: true, resultado: null, aviso}` — nunca erro, senão o modelo repetiria uma escrita já feita. Um `panic` é
recuperado (`[interno]`), sem derrubar a API.

### O token de confirmação

[`confirmacao.go`](../../workers/internal/mcp/confirmacao.go). Formato `"<exp_ms>.<mac43>"`: `exp_ms` em milissegundos, `mac43`
o HMAC-SHA256 em base64 URL sem padding, calculado com o `JWT_SECRET` sobre `[tool, args canônicos, user_id, exp_ms]`, com
**separação de domínio** (o prefixo `mcp-confirmacao.` entra antes do payload: o mesmo segredo assina outras coisas, como o
pedido do OAuth, e um MAC de um domínio nunca vale no outro). Comparação em tempo constante. A forma é canônica de
propósito (regex `^[1-9][0-9]{0,15}\.[A-Za-z0-9_-]{43}$`; teto de 200 caracteres no campo): sem isso `01…` e `+1…` dariam hashes
de uso único diferentes para a mesma confirmação.

- **Validade:** 10 min (`ttlConfirmacao`) + 1 min de folga (`folgaConfirmacao`): um `exp` além de agora + 11 min é recusado.
- **Argumentos canônicos:** JSON com chaves ordenadas em todo nível, números preservados (`json.Number`), **sem** a chave
  `confirmacao`. Ordem de chave e tipo de número não invalidam o token; qualquer argumento diferente, sim.
- O campo `confirmacao` está no schema de toda tool `confirma` ou escalável (`CampoConfirmacao`) e **só** nelas; nunca é
  obrigatório (o preview não tem token). Travado por `TestInventarioEscrita_ConfirmacaoNoSchemaSoOndePodePedir`.

### Uso único: consumido ANTES de executar (F3-5) — diverge da spec §5.2

`mcp_confirmacoes_usadas` (migration 0071; no merge vira 0072, ver "Limitações e follow-ups"): `hash BYTEA PK` (SHA-256 do
token), `usado_em`, `expira_em`. **Consumir** é `INSERT … ON CONFLICT DO NOTHING`: 1 linha afetada = primeiro uso.

- Consome **antes** de executar. **Só a recusa de negócio libera** (`DELETE` da linha: o token volta a valer); erro interno
  ou `panic` **não** libera, porque algo pode ter sido gravado (a pessoa pede o preview de novo).
- **Divergência:** a spec §5.2 pedia o `INSERT` "na mesma transação da escrita" (falha de negócio → rollback → token volta).
  Nenhuma das funções reaproveitadas aceita `pgx.Tx` (cada uma abre a sua ou usa o pool, e o recat abre a própria), e passar
  uma transação por todas mudaria o comportamento das rotas HTTP. O custo é o risco residual descrito acima (token gasto
  depois de erro interno). A spec foi atualizada.
- Poda oportunista a cada `Consumir`: apaga até 500 linhas vencidas há mais de 1 h (`FOR UPDATE SKIP LOCKED`; erro da poda é
  logado e ignorado, nunca bloqueia a confirmação). O varredor da Fatia 4 assume. Operação: [operations/mcp.md](../operations/mcp.md).

### Crítico na Fatia 3 (F3-1) — superado pela Fatia 4

Na Fatia 3 o ramo `critico` só devolvia `{executou: false, nivel: "critico", motivo, preview, aviso, links: {tela}}`, sem
escrever, sem consumir token e gravando `mcp.escrita.requer_aprovacao` ("faça pela tela"). A Fatia 4 trocou exatamente esse
ramo por `criarPendente`: o evento `mcp.escrita.requer_aprovacao` deixou de existir (agora é `mcp.pendente.criada`), a resposta
passou a levar `pendente_id` e `aprovar_em`, e as `Instrucoes` mandam entregar o link. O `motivo` continua dizendo qual condição
escalou (pode haver mais de uma). Resto do motor (nível efetivo, `Preview` antes do ramo crítico, recusas) não mudou.

### Escalada: o critério (F3-3, F3-4)

"Atinge veiculação já ocorrida" = existe veiculação **aprovada** no mesmo recorte que o gatilho vai recategorizar.
`catalog.HaVeiculacao` ([`ha_veiculacao.go`](../../workers/internal/catalog/ha_veiculacao.go)) usa o recorte do recat
(`detection_campaigns × detections × materials`, dia em America/Sao_Paulo) e o mesmo `ApprovedDetectionsFilter`;
`TestHaVeiculacao_ParidadeComORecatReal` prova que "false" significa que o recat desse recorte não tem tocada aprovada para
reclassificar. `HaVeiculacaoNaEmissora` é a pergunta por emissora, sem âncora de campanha (`editar_emissora`). "Dia passado" é
sempre **hoje em America/Sao_Paulo** (`hojeSP`), nunca `time.Local`.

Regras finais, como implementadas (a tabela de tools traz a coluna "Sobe para `critico` quando"; aqui os detalhes que ela não cabe):

- **`criar_campanha`:** `inicio ≤ hoje SP`.
- **`editar_campanha`:** o estado que as datas dão muda (`programada`/`ativa`/`concluida`, via `catalog.EstadoPeloCiclo`,
  travada contra o SQL do `PromoteScheduledLifecycle` por `TestEstadoPeloCiclo_BateComOSQL`), **ou** o conjunto de dias < hoje
  do período muda, **ou** o código do hub muda.
- **`editar_emissoras_campanha`: SEM escalada.** O plano (F3-4) previa `critico` ao remover emissora com veiculação. O dono
  (Dereck) decidiu em 2026-10-29, depois de `TestEditarEmissorasCampanha_ProvaDaRemocao` provar que grade, insights, financeiro,
  veiculações, planos do dia, falhas e `detection_campaigns` ficam **idênticos** (também depois do recat) ao tirar a emissora,
  que a tool segue `confirma`. O que muda: `target_stations` (lista de `obter_campanha`), a lista de PMM no target, o mapa ao
  vivo, os alertas de queda e, se a campanha está ativa, os workers. **Numa campanha ativa o efeito nos workers vai além da
  diferença:** `Supervisor.UpdateStations` sobe os workers das emissoras acrescentadas, para os das removidas (as que outra
  campanha ativa ainda usa continuam) e **reinicia também o worker de TODAS as emissoras que ficam** (`startStationWorker`
  sobre `kept + added`, para recarregar os materiais): cada uma reconecta por um instante, com uma pequena lacuna de
  captura, inclusive as compartilhadas com outras campanhas ativas. A rota `PUT /campaigns/{id}/stations` faz o mesmo; o
  preview e a ressalva da tool dizem isso.
- **Vínculos (`vincular`/`desvincular`/`editar_emissoras_material`): SEM escalada** (`TestVinculos_ProvaF34_PassadoNaoMuda`:
  não há recat, a atribuição é decidida na entrada da tocada). Duas ressalvas no preview: a **competição de atribuição**
  (outra campanha com o mesmo áudio nas mesmas emissoras disputa as tocadas futuras: sem `MULTI_ATTRIBUTION`, a tocada vai para
  UMA campanha só — a do vínculo mais recente quando é o mesmo material, ou o que o detector reconhece, e em empate o de menor
  `short_id`, quando são materiais diferentes com o mesmo áudio; com a flag ligada, conta nas duas) e o **webhook de detecção
  ainda não entregue** que a retirada pode descartar.
- **`editar_material`:** só quando o **tipo** muda e o material tem veiculação aprovada (`HaVeiculacao{MaterialID}`); título e
  roteiro nunca escalam.
- **Regras (criar/editar/apagar):** (a) a campanha tem veiculação aprovada do(s) tipo(s) que o recat varre — criar: o novo;
  editar: o novo **e** o antigo; apagar: **todos** (a regra apagada não diz mais o escopo, o recat varre a campanha inteira);
  **ou** (b) a vigência, antiga e/ou nova, recortada ao período da campanha, contém dia passado que a **máscara de dias da
  semana da própria versão** cobre (olha no máximo 7 dias: a máscara se repete por semana). A regra de datas **conhece a
  máscara**: num dia que nem a antiga nem a nova cobrem não há meta, e o déficit não pode mudar. Mudar a meta N de um dia
  passado muda déficit e "esperado" que o cliente já viu, mesmo sem tocada. **Editar só o nome também escala**, porque o recat
  roda de novo. Consequência prática: editar regra de campanha em andamento quase sempre escala, então isso continua sendo
  feito pela tela.
- **Overrides:** `data < hoje SP` **ou** a célula-dia (campanha × tipo × emissora × dia) tem veiculação aprovada.
- **`editar_pmm_target`:** o cliente tem veiculação aprovada nas emissoras do lote **ou** campanha não cancelada dele com
  alguma delas e `início < hoje SP` (o total de impactos no target aparece mesmo antes da primeira tocada; o PMM é retroativo,
  migration 0054).
- **`editar_emissora`: escala quando o `pmm` muda e a emissora tem veiculação aprovada** (o plano previa "sem escalada"; o
  levantamento mostrou que `stations.pmm` alimenta audiência e impactos de todas as campanhas da emissora, inclusive dias
  passados, sem versionamento). Nome, dial, cidade e UF não escalam.
- **Sem escalada:** `criar_cliente`, `editar_cliente`, tipos de material, `marcar_notificacoes_lidas`,
  `reconhecer_similaridade_material`.

### Execução, auditoria e resposta (F3-11)

- **`Executar`** roda sob `context.WithoutCancel` com prazo de **60 s** (o mesmo valor do `middleware.Timeout` do router):
  depois de consumir o token, o desfecho não depende de o cliente continuar conectado. Os 60 s do `Executar` são **só dele**,
  contados depois do `Escalar` e do `Preview` (ver "Limitações e follow-ups" sobre a escrita lenta).
- **Auditoria** feita pelo motor **depois** do sucesso e fora da transação da escrita: `audit_log` com
  `actor_id` = o admin, `actor_type 'user'`, `action = "mcp.escrita.<tool>"`, `resource`/`resource_id` do alvo (nos ajustes do
  dia, `campaign` + o id da campanha: tipo, emissora e dia vão nos `args`) e
  `metadata = {origem: "mcp", tool, nivel, args, mcp: {token_id}}`. Falha da auditoria é logada e **não** derruba a resposta
  (a escrita já aconteceu). Eventos próprios: `mcp.pendente.criada` (ramo crítico, com o `motivo`; ver a Fatia 4) e
  `mcp.escrita.falha` (erro interno ou `panic` no `Executar` de uma tool `livre` ou `confirma`; só `"erro": "interno"`, nunca o texto
  do erro Go). Recusa de negócio não grava nada (nada foi escrito).
- **Os `args` são mascarados na gravação** (`auditoria.OmitirSegredos`: chaves que parecem segredo viram `"[omitido]"`) e todo
  texto é cortado em 500 runas (`…[cortado]`; um roteiro de 5 mil caracteres não vai para o log). A chave `mcp` (com o
  `token_id`) não é mascarada. **Dados de contato do cliente (e-mail, telefone, CNPJ) aparecem nos `args` de
  `criar_cliente`/`editar_cliente`** — o `audit_log` é só para admin.
- **A tool `auditoria_mcp` não mostra o `token_id`:** o leitor omite toda chave que case `token`. Para ligar uma escrita ao
  token que a fez, consulte o `audit_log` direto (SQL em [operations/mcp.md](../operations/mcp.md)).

### Edição parcial (F3-10)

Mesmo onde a rota é `PUT` total, a tool lê o registro, aplica **só os campos enviados** e grava pelo mesmo caminho: o modelo
erra omitindo campo, e `PUT` total transformaria omissão em apagamento. Listas de emissoras/materiais entram como
`adicionar`/`remover` (nunca "a lista inteira": um modelo que esquece um id apagaria emissoras). Texto vazio apaga onde faz
sentido (`codigo_hub: ""`, `roteiro: ""`). `editar_emissora` grava por `Stations.UpdateCadastro`, que só toca nome, frequência,
cidade, UF, PMM e coordenada (`COALESCE`: campo não enviado fica): o MCP **nunca** troca `stream_url`, banda, logo nem
metadados (spec §1, não-objetivo). `editar_cliente` não tem `hub_id`. Pedido que não muda nada é recusado
(`entrada_invalida`: "nada a alterar").

### A camada `internal/acoes`

[`workers/internal/acoes/`](../../workers/internal/acoes/) concentra as escritas de negócio que antes moravam (parcialmente) nos
handlers: `CriarCampanha`/`EditarCampanha`/`TrocarEmissorasCampanha`, `VincularMaterial`/`DesvincularMaterial`/
`TrocarEmissorasMaterial`/`TrocarTipoMaterial`, `CriarRegra`/`EditarRegra`/`ApagarRegra`, `DefinirAjuste`/`ApagarAjuste`,
`CriarCliente`/`EditarCliente`/`ChecarCNPJ`, `EditarCadastroEmissora` e `ConferirCodigoHub`. `acoes.Servico` reúne os
repositórios e os colaboradores por interface (`SupervisorDeCampanha{UpdateStations, Reload}`, `AvisoHub`), o que deixa os testes
usarem falsos que registram as chamadas (`mcptest.SupervisorFalso`, `HubFalso`). `EmSegundoPlano` (nil = goroutine **com
recover**) é injetável para o teste esperar a recategorização. `acoes.Recusa{Mensagem, Codigo, Dados, Causa}` é a falha de
**negócio**: o handler a traduz para o status de sempre (`handlers/acoes_http.go`) e o motor do MCP a devolve como
`"<mensagem> [<código>]"` e libera o token. `Recusa.Causa = ErrAlvoInexistente` marca "o alvo já não existe": a rota HTTP
responde 204 (idempotência de sempre) e o MCP mostra `nao_encontrado`. Erro do Postgres de entrada (FK, CHECK) passa por
`recusaDoBanco`, que casa a constraint pelo nome exato e nunca vaza o nome nem o SQLSTATE na mensagem; deadlock,
serialização, cancelamento e o que não é erro de entrada seguem erros internos.

A barreira do código do hub (`conferirCodigoOuRecusar`) virou `acoes.ConferirCodigoHub`, que devolve `*Recusa{codigo_hub}` com as
mesmas 4 mensagens do 422. Gatilhos que continuam assíncronos: a recategorização de regra e de troca de tipo
(`context.Background` + 30 s, `acoes.RegistrarFalhaRecat`) e os avisos ao hub. O recat de override é **síncrono**.

### Mudanças nas rotas HTTP

Extrair as ações para `internal/acoes` foi refactor sem mudança de comportamento (cada handler foi travado por teste ANTES de
mexer), **exceto** os pontos abaixo, que mudaram a rota para melhor (conferidos contra `git log` e testes de handler):

- **Regra de outra campanha:** `PUT`/`DELETE /campaigns/{id}/distribution-rules/{ruleID}` com a regra de **outra** campanha agora
  dão **404** e não mudam nada (antes editavam/apagavam a regra alheia com 204 e recategorizavam a campanha errada).
- **`PUT` de regra inexistente: 404** (antes o `UPDATE` de zero linhas dava 204). **`DELETE` de regra inexistente segue 204** e
  **deixou de recategorizar** a campanha à toa.
- **`PUT` de regra que troca o tipo** agora recategoriza também o tipo **antigo** (antes só o novo; o `projrecon` curava em até 48 h).
- **Falha de constraint que era 500 virou 400/404 com o motivo em português:** regras `POST`/`PUT` (máscara fora de 0–127,
  inserções fora de 1–100, data final antes da inicial, hora final não maior que a inicial, tipo/campanha/emissora inexistente);
  `PUT` de override (meta negativa, tipo/emissora/campanha inexistente); `PATCH /materials/{id}/type` com tipo inexistente (400);
  `POST /campaigns` com cliente inexistente (404) ou `end_date` < `start_date` (400); `POST /campaigns/{id}/materials` com
  material ou campanha inexistente (404). O 404 do handler continua o texto `not found`; o 400 leva o motivo.
- **Desvincular / trocar emissoras de um vínculo que não existe** seguem **204**, mas **não chamam mais `Reload`** da campanha (antes
  um reload à toa reiniciava os workers de campanha ativa). **`PATCH /materials/{id}/type` de material inexistente** segue 204
  sem recategorizar. **`DELETE …/distribution-overrides` de um ajuste que não existe** (0 linhas apagadas) também segue **204**,
  mas **não recategoriza mais** a célula-dia à toa; a ação o devolve como `nao_encontrado` (`ErrAlvoInexistente`) e a tool
  `apagar_override` mostra `[nao_encontrado]` em vez de `apagado: true` se o ajuste sumir entre o plano e o `DELETE`.

### Desempenho

Medido em 2026-11-03 (Ryzen 7 7435HS), com as **83 tools** da Fatia 4: `BenchmarkNovoServidor` (montar o `Server` do SDK)
**~5,8 ms/op** (~4,1 MB, ~11 mil alocações) e `BenchmarkPedidoToolsList` (um `tools/list` pelo handler real, sem rede)
**~11,9 ms/op** (~6,3 MB, ~19,6 mil alocações) — logo abaixo do teto de ~12 ms que o plano fixou. A progressão: 44 tools = 2,75 ms
(`NovoServidor`); 64 tools = ~5,0 ms (~3,2 MB, ~8,8 mil alocações) e ~9,1 ms o pedido. O go-sdk pede o `Server` **duas** vezes por pedido
stateless (uma só para ler as versões de protocolo), então `NovoHandlerMCP` monta-o **uma vez por pedido** (um guardador no
contexto do pedido; nada é compartilhado entre pedidos nem entre principais): sem isso o `tools/list` de 64 tools custava ~12,5 ms.
Travado por `TestServidor_MontaOServerUmaVezPorPedido` (falha sem a memoização) e `TestServidor_MemoizacaoNaoMisturaPrincipais`.
**Próximo passo antes de acrescentar tools:** memoizar por processo os schemas de entrada que o SDK infere a cada
`AddTool` (o custo cresce linear com o número de tools e a parte estática é a mesma em todo pedido); hoje só `decodificarArgs`
guarda o schema resolvido por tipo (`schemasDeEntrada`), para a aprovação. Remeça os dois benchmarks e registre o antes/depois.

### Limitações e follow-ups (Fatia 3)

- **`EmSegundoPlano` sem limite de concorrência:** um pico de edições de regra ou de tipo dispara uma goroutine de recat por
  gatilho (com recover, mas sem teto). Follow-up: semáforo.
- **`TrocarTipoMaterial` não recategoriza o tipo ANTIGO** (as células do tipo antigo perdem um membro e ficam como estavam; o
  `projrecon` cura em até 48 h). A rota de regra foi corrigida nisso (F3-8b); a de material não.
- **Textos em inglês nas `Recusa` que vêm das rotas:** as validações que saíram do handler guardam a mensagem antiga
  (`name is required`, `end_date must be on or after start_date`, `time_start must be HH:MM…`), e o MCP as repete ao modelo.
- **`CriarCampanha` (rota) não apara o nome nem valida data zero** (a rota sempre aceitou como veio; no MCP quem valida é a tool).
- ~~**`acoes.EditarCampanha` NÃO é transacional**~~ — **corrigido na Fatia 4 (F4-9).** `catalog.UpdateBasicInput` ganhou
  `HubCode *string` e `UpdateBasic` grava nome, datas e (quando pedido) `hub_code`/`hub_notified_at = NULL` num único `UPDATE`;
  `acoes.EditarCampanha` não chama mais `AtualizarHubCode`. Um trigger de teste que dá `RAISE` quando `NEW.hub_code = 'EH-FALHA1'`
  prova que nome e datas não mudam quando o código falha. Era a pendência obrigatória antes de o crítico poder trocar o código do
  hub sem token de confirmação.
- **Bug latente em `catalog/detections.go:~604`:** o `time_end` da regra é lido com `time.Parse("15:04:05", …)` ignorando o erro
  (`"24:00"` vira `00:00` no caminho de insert em Go, enquanto o SQL aceita `24:00`). Não é do MCP; anotado.
- **`HaVeiculacao` para regras não filtra por dia:** a escalada da regra olha a campanha inteira, então uma campanha só com
  tocadas `out_date` (fora do período) também escala. Mais estrita do que o necessário, nunca mais frouxa.
- **Token gasto em erro interno depois de consumir** (F3-5): a pessoa pede o preview de novo.
- **Escrita lenta pode terminar depois de a resposta HTTP já ter falhado.** O `Executar` tem os PRÓPRIOS 60 s, contados
  depois do `Escalar` e do `Preview`, enquanto o `middleware.Timeout` de 60 s do router corre desde o início do pedido. Uma
  escrita lenta (ex.: o recat síncrono de um override) pode concluir DEPOIS de o cliente/proxy já ter desistido (o `middleware.Timeout`
  do chi só cancela o contexto e escreve 504 se o handler voltar sem ter escrito; quem corta antes é o Cloudflare, com 524). É seguro:
  o token já foi consumido (repetir devolve "já foi usada") e o `Executar` não é cancelado. Mas o modelo só vê um erro de
  transporte e não sabe se a escrita aconteceu: deve conferir com a tool de leitura correspondente (`listar_overrides`,
  `obter_campanha`…) antes de pedir outro preview.
- **`token_id` não aparece na tool `auditoria_mcp`** e dados de contato/CNPJ aparecem nos `args` do `audit_log` (ver acima).
- **Recat assíncrono:** a regra e a troca de tipo respondem antes de a recategorização acabar (até 30 s); a resposta traz a
  ressalva. O override é síncrono.
- **Renumerar as migrations no merge:** o `master` já tem `0070_users_username`, então `0070_mcp_fundacao` vira `0071`,
  `0071_mcp_confirmacoes` vira `0072` e `0072_mcp_acoes_pendentes` (Fatia 4) vira `0073` (e as referências a "migration 0070/0071/0072"
  neste doc). Ver também a nota sobre a 0072 editada no lugar, em [operations/mcp.md](../operations/mcp.md).
- **Trailers de commit da branch** misturam "Claude Opus 5.5" e "Claude Sonnet 5.5" (dois modelos trabalharam nela).

## Crítico e /pendentes (Fatia 4)

18 tools `critico` + `minhas_pendentes` (83 no total). Desenho e decisões F4-1..F4-16:
[plano](../superpowers/plans/2026-10-30-mcp-fatia-4-critico.md) e [spec §4.6, §5.2 e §6](../superpowers/specs/2026-10-27-mcp-server-design.md).
A tabela de tools está em "Fatia 4 — escrita crítica (18)". O contrato é o do hub (`mcp-ecossistema.md` §3.5 e §6): o modelo pede, **uma
pessoa autenticada executa**. É a tela `/pendentes` que dá a garantia que o protocolo MCP não dá.

### O pedido (F4-5)

O ramo `critico` do motor roda o `Preview` (uma recusa de negócio vale antes de qualquer pedido), grava uma linha em
`mcp_acoes_pendentes` e responde, **sem `isError`**:

```json
{ "executou": false, "nivel": "critico", "motivo": "...", "preview": {"resumo": "...", "mudancas": [], "efeitos": [], "ressalvas": []},
  "pendente_id": "<uuid>", "expira_em": "...", "aprovar_em": "<portal>/pendentes?id=<uuid>", "ja_existia": false,
  "aviso": "Nada aconteceu ainda. Entregue o link aprovar_em à pessoa: só ela, logada no portal, pode aprovar; o pedido vale 24 h.",
  "links": { "tela": "..." } }
```

Chaves em `snake_case` (a spec dizia `pendenteId`/`aprovarEm`; as `Instrucoes` dizem `aprovar_em`). O `motivo` é o da escalada
(tools da Fatia 3 que subiram a `critico`) ou o `Motivo` fixo da própria tool. Sem `Portal` configurado, `aprovar_em` vem `null` com
uma ressalva mandando abrir `/pendentes` no portal. Um token de confirmação que viesse junto é ignorado (não é consumido). A
linha guarda os **args canônicos** (sem `confirmacao`), o `motivo` e o `preview`; o `token_id` de quem pediu fica nela (FK
`ON DELETE SET NULL`). **Todo crítico tem executor por construção** (uma lista só, ver abaixo): isso torna executáveis também as
escaladas das 20 tools da Fatia 3.

### Não duplica (F4-3)

`args_hash = SHA-256(tool ‖ 0x00 ‖ args canônicos)` e um índice único parcial `(user_id, args_hash) WHERE estado IN ('pendente',
'aprovada')`. Pedir de novo o mesmo devolve **o mesmo pedido** com `ja_existia: true` e o preview/motivo **como estão gravados**
(o que a pessoa vai ver em `/pendentes`, não o recalculado agora); antes, expira o vencido daquele hash. Se o pedido igual já foi
aprovado e está rodando, a resposta não traz `aprovar_em` e diz que ele está em execução. Sem isso um modelo que repete a chamada
faria a pessoa aprovar duas vezes o mesmo `criar_veiculacao_manual` (duas tocadas lançadas).

### Estados (F4-2)

`pendente → aprovada → executada`, com `recusada` e `expirada` finais. Transições por `UPDATE … WHERE estado = … AND tentativa = …`
(dois cliques, um vence); `tentativa` conta as aprovações e só a vigente fecha o pedido; `MarcarExecutada` aceita `aprovada` **ou**
`falhou` da mesma tentativa (o varredor pode ter dado como interrompida uma execução que terminou).

- **Recusa de negócio na execução** (o estado mudou: campanha cancelada nesse meio-tempo, material desvinculado…) e **conferência que
  cai por erro inesperado** voltam o pedido a `pendente`, com o texto em `erro` e prazo `GREATEST(expira_em, agora + 1 h)`: a
  pessoa lê o porquê e recusa, ou corrige o mundo e aprova de novo. `args_invalidos` e `tool_desconhecida` (a tool mudou ou sumiu
  entre o pedido e a aprovação) **não** ganham a folga.
- **Exceção ou `panic` no `Executar`** → `falhou`, **final**: algo pode ter sido gravado, uma pessoa confere. Se a ação soube dizer o
  que ficou feito (`*acoes.ErroAposEscrita`), o `erro` leva o texto dela no lugar do genérico (ver abaixo).
- Prazo de 24 h. `pendente` vencido vira `expirada` pelo varredor, ou na hora em que alguém tenta aprovar ou recusar.
- **Revogar a conexão de IA expira os pedidos dela.** O pedido guarda o `token_id` da chamada que o criou; o que precisa estar vivo
  é a **conexão**: um token pessoal vale por si, e numa conexão OAuth o token da chamada é o access token (dura 1 h, o pedido 24 h),
  então vale enquanto o usuário tiver algum token vivo do mesmo `client_id`. Dois caminhos (os dois escrevem em `erro` o texto
  `msgCredencialRevogada` — «A conexão de IA que fez este pedido foi revogada; peça de novo por uma conexão válida.» — e levam o
  pedido a `expirada`):
  - *Proativo*: `Store.Revogar` (o «Desconectar» de Minha conta, token pessoal ou conexão OAuth inteira) expira os `pendente` de **qualquer
    token da conexão**, mesmo vencido ou trocado no refresh; `Store.RevogarTodos` (e o CLI `revoga-mcp`) e a função do trigger
    `users_revoga_mcp` (senha trocada/redefinida, desativação, rebaixamento, apagar — a função é recriada no fim da `0072`) expiram
    **todos** os `pendente` do usuário. A `aprovada` não é tocada: uma pessoa já clicou e a execução está rodando.
  - *Na aprovação* (rede de segurança: revogação que não passou pelo caminho acima, `token_id` virado `NULL`, token vencido):
    `Pendentes.Aprovar` só pega a linha se a conexão está viva; senão fecha o pedido como `expirada`, responde 409 `pendente_expirada`
    com o motivo no `mensagem`, audita `mcp.pendente.falhou` com `codigo: credencial_revogada` e **nada executa**. A tentativa seguinte
    cai no 409 normal de estado, com o mesmo motivo.

  `minhas_pendentes` mostra o motivo no `erro` do pedido `expirada`, então o modelo sabe que precisa pedir de novo por uma conexão válida.
- O pedido não é podado: a linha (e os `args`, inclusive a URL inteira de um webhook) fica gravada.

### Aprovação (F4-6)

Rotas no grupo admin do MCP (`/v1/internal`, JWT + `RequireRole("admin")`):

| Rota | Faz |
|---|---|
| `GET /mcp/pendentes[?id=<uuid>]` | `{items, destaque}`: as abertas (`pendente` + `aprovada`, 50 mais novas) e, com `?id=`, o pedido do link em qualquer estado (`null` se for de outra pessoa ou malformado: mesmo 200, a lista não serve de oráculo) |
| `POST /mcp/pendentes/{id}/aprovar` | executa. **Corpo obrigatório `{"tentativa": N}`**: a versão do pedido que a tela mostrou (ver abaixo); sem ela, 400 `entrada_invalida` |
| `POST /mcp/pendentes/{id}/recusar` | recusa |

**Só quem pediu aprova.** Todo SQL filtra `user_id` = o JWT; "não existe", "não é seu" e id malformado respondem igual (404
`nao_encontrado`). O papel é relido do banco (`DonoAdminAtivo`): rebaixado desde o login → 403 `proibido`. A execução é
síncrona no pedido HTTP; o `Principal` é o dono, com o `token_id` gravado no pedido. O que `Aprovacoes.Aprovar` faz, na ordem:

1. `UPDATE pendente → aprovada` (`tentativa + 1`), com 10 s próprios, sem o cancelamento do pedido HTTP; audita `mcp.pendente.aprovada`.
   O `UPDATE` só pega a linha **se** `tentativa` = a do corpo **e** a conexão que fez o pedido está viva (ver «Estados»).
2. Acha a escrita pelo nome (sumiu → `tool_desconhecida`) e **revalida os args gravados pelo mesmo JSON schema que o SDK infere**
   (`jsonschema.ForType` + `Resolve`, `ApplyDefaults`, `Validate`, decodificando com `DisallowUnknownFields`; falha → `args_invalidos`).
3. **Reroda o `Preview`** (com o dono no contexto, `ComPrincipal`, para as guardas "nunca a si mesmo" valerem também aqui), que relê o
   estado e recusa se o alvo sumiu. Depois **compara o resumo novo (`resumo`, `mudancas`, `efeitos`, `ressalvas`) com o que a pessoa viu**:
   se mudou, recusa com `preview_desatualizado`, **grava o resumo novo no lugar do antigo** na mesma atualização que devolve o pedido
   a `pendente`, e a pessoa lê o que vale agora e aprova de novo — só se aprova o que se viu. Por isso o `Preview` precisa ser
   **determinístico** (nada de horário de relógio, ordem de mapa ou contagem de fila no texto).
4. Chama o `Executar` sob `WithoutCancel` + 60 s. O `Escalar` **não** reroda (já é crítico).
5. Sucesso: audita `mcp.escrita.<tool>`, serializa o resultado, fecha com `MarcarExecutada` (gravando as **ressalvas** da execução) e
   audita `.executada`. Daqui para baixo nada vira erro: se só o fechamento falha, a resposta ainda é 200. Um erro do banco ao fechar
   ganha **uma** segunda tentativa (contexto novo, 250 ms depois) antes de desistir; sem sucesso a linha fica `aprovada` até o
   varredor, e a pessoa ainda lê o desfecho verdadeiro (a ação rodou).

**A `tentativa` é a versão do que a pessoa leu.** Todo resumo gravado novo (`preview_desatualizado`, passo 3) vem depois de uma
aprovação que incrementou a `tentativa`; então a aprovação com `tentativa` velha é uma aprovação de um resumo que a pessoa nunca viu
(a aba B ainda mostrando o resumo velho depois de a aba A receber o `preview_desatualizado`) e é recusada: 409 `pendente_mudou`
(«O pedido mudou desde que você abriu; releia antes de aprovar.»), nada executa, a `tentativa` não é gasta. Toda recusa de negócio
incrementa a versão, então uma aba desatualizada por uma recusa que **não** mudou o resumo (`erro` novo) também toma o 409 — custa um
recarregamento. O DTO do pedido (lista, `destaque`, 200 e 422) expõe `tentativa`; o 422 traz o pedido relido, já com a versão nova.

Respostas da aprovação:

| Status | Corpo | Quando |
|---|---|---|
| 200 | `{pendente, resultado, ressalvas}` | executou. `ressalvas` são os avisos da execução (sempre array, `[]` sem avisos; também ficam na linha do pedido): "o email não saiu, a pessoa não tem a senha", "gravei mas não consegui reler…" |
| 400 | `{error: "entrada_invalida", mensagem}` | o corpo não traz `tentativa` (inteiro ≥ 0) |
| 409 | `{error: "pendente_<estado>", mensagem}` | o pedido já não está `pendente` (outra aba, prazo). `pendente_expirada` também quando a **conexão de IA foi revogada** (o `mensagem` diz isso e o pedido acabou de ser fechado como `expirada`) |
| 409 | `{error: "pendente_mudou", mensagem}` | `tentativa` do corpo ≠ a vigente: o pedido (resumo) mudou desde que a tela o abriu. Nada executou |
| 422 | `{error: <código>, mensagem, pendente}` | recusa de negócio: nada foi feito e o pedido voltou a `pendente`. `pendente` é o mesmo DTO do `GET`, relido depois da recusa (um `preview_desatualizado` já traz o `preview` novo, com a `tentativa` nova); aditivo e ausente se a releitura falhar. A releitura usa `WithoutCancel` + 5 s: o 422 pode sair depois de o pedido HTTP ter sido cancelado |
| 503 | `{error: "conferencia_falhou"}` | a conferência caiu por erro inesperado; nada foi feito e o pedido voltou a `pendente`: aprove de novo em instantes |
| 500 | `{error: "interrompida", mensagem}` | virou `falhou`. A `mensagem` é a da ação quando ela sabe dizer o que ficou feito (`*acoes.ErroAposEscrita`: "a campanha foi cancelada, mas NÃO foi apagada: apareceu veiculação…"; "o usuário foi criado, mas a carteira não foi gravada por inteiro e o email NÃO saiu"), senão o genérico "confira se a ação aconteceu antes de pedir de novo" |
| 503 | `{error: "indisponivel"}` | o índice das escritas falhou no boot (o módulo sobe sem aprovações; regra 6.5) |

O pedido que a tela recebe **não traz `args` nem `resultado`** (só ferramenta, motivo, preview, estados, datas, `erro` e `ressalvas`).
`minhas_pendentes` devolve o mesmo, cortado para a lista caber.

### Auditoria (F4-7)

Quem audita é o **executor da aprovação**, depois do sucesso e fora da transação da escrita (como o motor da Fatia 3). **Diverge
da spec §4.6** ("dentro da função compartilhada"): o executor é o ponto único por onde todo crítico passa, e gravar dentro de cada
função de `acoes` a gravaria também nas escritas da tela. Linhas: `mcp.pendente.criada` (recurso = o alvo da tool, via `Recurso`),
`.aprovada`, `.recusada`, `.executada`, `.falhou` (recurso `mcp_pendente`, id = o do pedido) e `mcp.escrita.<tool>` com
`nivel = critico`; todas levam `meta.mcp = {token_id, pendente_id}`. O ator é o admin dono (`actor_type 'user'`). `.falhou` leva `codigo`:
o da recusa (`preview_desatualizado`, `args_invalidos`, `campanha_cancelada`…: o pedido voltou a `pendente`), `conferencia_falhou`,
`interno`, `falha_apos_escrita` (a ação explicou o meio-estado), `credencial_revogada` (a aprovação foi recusada porque a conexão de IA do
pedido foi revogada; o pedido virou `expirada`) ou `presa` (varredor). `pendente_id` aparece em `auditoria_mcp` (a
chave não casa `chaveSecreta`); `token_id` segue oculto.

**`Escrita.ArgsParaAuditoria`** é o gancho para a tool cujo argumento carrega um segredo que o log não guarda por inteiro. Hoje só
`editar_webhook_cliente`: no `audit_log` a `url` vira **esquema + host** e entra `url_sha256` (SHA-256 do texto gravado, que deixa
conferir de qual URL se tratava sem guardá-la). Vale em toda linha que leva os args, no pedido e na aprovação
(`argsGravadosDaAuditoria` decodifica os args do pedido). Gancho que falha ou dá `panic` grava `{}`, **nunca** os args sem redação. Só
o `audit_log` muda: o pedido gravado mantém a URL inteira, que é o que o `Executar` precisa e só o dono lê (a mesma URL já mora em
`clients.webhook_url`).

### Varredor (F4-8)

`mcp.Varredor`, goroutine iniciada em `main.go` com o contexto do processo: uma passada na subida e outra **a cada 1 minuto**, com
`pg_try_advisory_xact_lock` (hash FNV-64a de `radiocheck:mcp-varredor`; uma réplica varre por vez), 30 s por passada e cada passo
num savepoint (o erro de um é logado e desfeito sem derrubar os outros):

1. `pendente` com `expira_em ≤ agora` → `expirada` (até 500 por volta);
2. `aprovada` com `aprovada_em` há mais de **10 min** → `falhou` ("A execução caiu no meio por um erro inesperado; confira se a ação
   aconteceu…") + auditoria de sistema `mcp.pendente.falhou` com `codigo: presa` (`actor_type 'cli'`, `metadata.pelo = "varredor-mcp"`);
3. poda `mcp_confirmacoes_usadas` vencidas há mais de 1 h (até 5 000 por volta). A poda oportunista de `Confirmacoes.Consumir` continua.

### Registro único das escritas (F4-4)

Cada família expõe `escritas<Familia>(cfg) []escritaRegistravel`; `escritasDe(cfg)` concatena as 12 (6 da Fatia 3, 6 críticas),
`registrarEscritas` registra as tools e `indexarEscritas` monta o mapa nome → escrita (falha se dois nomes colidem). O **mesmo
valor `Escrita[In]`** registra a tool e executa a pendente aprovada, sem segunda cópia da lógica. A interface `escritaRegistravel`
esconde o tipo genérico (`registrar`, `executarAprovada`, `validarArgs`, `argsGravadosDaAuditoria`).

### Segredos (F4-11)

- **`criar_usuario` não recebe senha.** O servidor gera uma de 24 caracteres (`crypto/rand`, alfabeto sem `0/O/1/l/I`, sempre com maiúscula,
  minúscula e dígito) **dentro do `Executar`**; ela vive só na memória até o `CriarUsuario` cifrá-la no convite (AES-GCM, ver
  [welcome-onboarding](welcome-onboarding.md)) e **só o email de boas-vindas a entrega**. A boas-vindas é obrigatória: sem `WELCOME_ENC_KEY` a
  tool recusa no preview (`boas_vindas_indisponivel`) e sem SMTP também (`email_desligado`: sem email a pessoa criada não teria a
  senha). O resultado nunca traz link nem senha. Email que falha **depois** de criar não é recusa: vira ressalva na aprovação ("a
  pessoa não tem a senha — redefina pela tela").
- **`editar_webhook_cliente` não tem campo de segredo** (só `url`, `ativo`, `eventos`; o segredo continua na tela) e recusa URL com
  credencial (`usuario:senha@`). O preview mostra a URL **antiga** só com esquema+host e a **nova** inteira (é o destino que a pessoa
  aprova).
- **O token do destinatário de pós-venda e o link público nunca saem**: as duas tools de pós-venda pedem o `id` do destinatário
  (não é segredo).
- Nenhum schema de escrita aceita `senha`, `segredo`, `token`, `password`, `secret` (inventário). O pedido guarda os args por tempo
  indeterminado no banco; por isso nenhum deles é segredo.

### Guardas de si mesmo (F4-12)

`editar_usuario` recusa mudar o **próprio** papel e `desativar_usuario` recusa a si mesmo (`proprio_usuario`): o trigger da 0070
revogaria a credencial MCP de quem pediu no meio da aprovação. Ninguém desativa ou rebaixa o **último admin ativo** (`ultimo_admin`;
conta `role = 'admin' AND is_active AND deleted_at IS NULL`). **Só no MCP**: a rota `PATCH /admin/users` não tem essas guardas (follow-up). O
`Preview` não recebe o `Principal` como argumento: o motor o põe no contexto (`ComPrincipal`/`PrincipalDe`) tanto na chamada quanto
na aprovação.

### O que saiu do catálogo e por quê (F4-1)

A spec §6 listava 21 tools; ficaram 18 críticas + `minhas_pendentes`. Saíram:

- **`pausar_campanha`**: a rota `PUT /campaigns/{id}/pause` responde 410 sempre desde 2026-05-07 ("use cancel"); "pausar" hoje é cancelar.
- **`iniciar_campanha`**: `PUT /campaigns/{id}/start` marca `ativa` **sem pré-condição** (reativa `cancelada`/`concluida`, e o `cancelled_at`
  fica), e o `LifecycleScheduler` rebaixa em ≤ 60 s uma campanha ativada antes do início (incidente 2026-10-01). O caminho honesto para
  "começar antes" é `editar_campanha` com início = hoje, que já escala a `critico` e agora executa; quem ativa é o agendador.
- **`publicar_pos_venda`**: a publicação exige as capturas PNG que o **navegador** gera e sobe (`UploadAssets`), guardadas só na memória do
  processo; não há renderizador no servidor. `obter_pos_venda` já devolve o link da tela de publicação.

Entrou **`minhas_pendentes`** (o hub tem a mesma): sem ela o modelo não sabe se o pedido foi aprovado e tende a dizer que fez.
`testar_webhook` ficou (a spec §10 deixara em aberto), crítica, mas recusa quando o teste seria descartado.

### Recusas que só o MCP faz (F4-10)

O MCP é mais estrito que a rota onde a rota destrói ou responde sucesso em silêncio. Todas moram no `Preview` (que reroda na
aprovação): `campanha_com_historico`, `material_com_historico`, `ja_ignorada`, `nao_ignorada`, `estado_terminal`, `sem_mudanca`,
`webhook_teste_descartado`, `webhook_nao_configurado`, `webhook_indisponivel`, `email_desligado`, `pos_venda_indisponivel`,
`pos_venda_sem_documento`, `boas_vindas_indisponivel`, `proprio_usuario`, `ultimo_admin`, `usuario_apagado` e `entrada_invalida`
(preço de emissora fora da campanha ou repetida). Detalhes na tabela de tools. `material_com_historico` conta também
`detection_campaigns` (a tocada base pode ser de outro material), não só `detections`.

**`apagar_campanha` reconfere o histórico dentro da transação do delete** (`ExigirSemHistorico` → `DeleteSemHistorico`): entre a
conferência do preview e o `DELETE` uma veiculação, um comprovante manual ou um pós-venda pode ter aparecido; se aparecer, o delete
aborta com `*ErroAposEscrita` (a campanha já foi cancelada, então não é recusa) e o pedido fica `falhou` com um texto que diz o que
aconteceu com a campanha (conforme o estado de antes: "já estava cancelada", "foi cancelada (os workers pararam)", "estava concluída e
continua concluída…") e que nada foi destruído.

**Mudanças nas rotas HTTP** (vieram das extrações para `acoes`, para melhor; cada handler foi travado por teste antes):
`DELETE /materials/{id}` com material vinculado **500 → 409** `material_vinculado`; `PUT /campaigns/{c}/pricing/{s}` com referência
inexistente (FK) **500 → 404/400**; `DELETE /campaigns/{id}` agora **cancela de verdade** (grava `cancelled_at`; antes o `Pause` deixava
`cancelada` sem a data) e responde **409** `campanha_com_lote_manual`/`campanha_em_pos_venda` (antes 500, pelas FKs). Pelo menos
essas três mudaram o comportamento da rota; as demais extrações são refactor sem mudança.

### A tela `/pendentes` (F4-15)

[`PendentesPage.jsx`](../../frontend/src/pages/PendentesPage.jsx), dentro do `AppShell`, `RequireRole admin`, **fora de `PUBLIC_ROUTES`** (sem
sessão, o `RequireAuth` manda ao `/login` guardando o destino em `state.from` — caminho **e** query, então o `?id=` volta — e o `LoginPage` devolve para cá depois de entrar: é o caminho do link que a IA entrega). Lista as abertas e, com `?id=`, destaca o
pedido em qualquer estado. Cada cartão mostra a ferramenta, o resumo, o motivo ("Por que precisa da sua aprovação"), as mudanças
**de → para**, os efeitos ("O que acontece"), as ressalvas do preview ("Atenção"), o prazo em horário de São Paulo e os botões
**Recusar** / **Aprovar e executar** (desabilitados enquanto age). **Aprovar manda a `tentativa` do cartão** que está na tela. Estados: `pendente` = "Aguardando você" (um `pendente` vencido pelo
relógio do navegador já aparece como `expirada`, sem botões); `aprovada` = "Executando…" (passando de 10 min o pedido é marcado
interrompido); `executada` ("Já foi executada."); `falhou` = "A execução foi interrompida", com o texto do servidor ou "confira se a ação
aconteceu e, se não, peça de novo à IA"; `recusada`; `expirada` («Expirou sem aprovação.», ou o motivo — a conexão de IA foi revogada — quando o servidor o gravou). Depois de decidir, o cartão fica na tela com o desfecho e as
**ressalvas da execução**. Um 422 mostra a mensagem do servidor no cartão e o pedido segue com os botões (`preview_desatualizado` mostra
"O pedido mudou: releia antes de aprovar"); o `pendente` que o 422 traz **entra no cache na hora** (`setQueriesData`), então o cartão
já mostra o resumo e a `tentativa` novos mesmo que a releitura da fila falhe em seguida. 409 puxa o estado real pelo `?id=`
(`pendente_mudou` tem o mesmo rótulo do `preview_desatualizado`); 500 `interrompida` vira cartão `falhou`.

**Quando o resultado é incerto.** Sem resposta (a rede caiu) ou 5xx sem corpo nosso (o Cloudflare responde **524** em HTML se o
Executar demora), a aprovação pode ter chegado e executado: a tela diz «Não sabemos se foi feito — recarregue a página para ver o estado»
e relê a fila; «Não foi feito» fica para o que o servidor garante que não aconteceu (422, 503 `conferencia_falhou`/`indisponivel`).

Outros cuidados da tela: **só esta fila relê ao voltar para a aba** (`refetchOnWindowFocus: true` no `usePendentesMcp`; o resto do app
segue sem), porque quem volta a uma aba aberta há tempo pode ver um resumo que mudou; `placeholderData: keepPreviousData` (o 409 troca
`?id=` sem piscar o esqueleto); a região viva do aviso (`aria-live`) fica sempre montada; e os textos que podem vir do modelo
(resumo, campo, de/para, efeitos, ressalvas) levam `dir="auto"` + `unicode-bidi: isolate`, além de o servidor tirar os controles de
direção do resumo (`limparPrevia`: U+202A–202E, U+2066–2069, LRM/RLM/ALM) antes de gravá-lo e de compará-lo na aprovação.

O texto do preview vem do servidor e é só **texto** (React escapa; nada de `dangerouslySetInnerHTML`): o que estiver escrito ali
descreve a ação, não manda em nada. **Anti-clickjacking** (um clique aqui grava dinheiro): `frontend/public/_headers`
(`X-Frame-Options: DENY` + `frame-ancestors 'none'` em `/pendentes` e `/pendentes/*`) **mais** uma guarda de moldura em JS
(`utils/emoldurada.js`: dentro de um iframe a página pede "Abra numa aba própria", não mostra botões nem chama a API). A rota
`/autorizar-ia` tem as duas camadas também (`_headers` + a guarda de moldura em JS; emoldurada, nem o pedido de autorização é lido). Em Minha conta → "Integração com IA" há o link **Pedidos da IA** com a contagem dos que
esperam. **Não há item na barra lateral**: a página é alcançada pelo link da IA ou por Minha conta. Teste Playwright com API falsa em
`frontend/e2e/mcp-pendentes.spec.js`. Tela nova = regra 5 do `CLAUDE.md` (o lockfile não pode ser podado) e deploy em dois
caminhos (backend primeiro: a tela chama rotas novas).

### Limitações e follow-ups (Fatia 4)

- **Corrida do último admin.** `ultimo_admin` conta os admins ativos no `Preview`/`planejar*`, **fora** da transação da escrita
  (`acoes.EditarUsuario`). Dois pedidos aprovados ao mesmo tempo (ou em abas diferentes) podem rebaixar ou desativar os dois últimos
  admins. Fix: advisory lock em `acoes.EditarUsuario` e recontar dentro dele.
- **Hub.** Ativar, desativar, editar e apagar cliente não avisam o hub (só `acoes.CriarCliente` manda `cliente.upsert`), e o `client.upsert` do hub (`hubsync.go`) regrava `is_active` e **recria** cliente
  vinculado que foi apagado aqui. `desativar_cliente`/`apagar_cliente` de um cliente com ponte são desfeitos na próxima edição dele no
  hub (o preview avisa).
- **Webhook.** O worker lê a URL na hora do envio e ignora `webhook_enabled`: desligar não cancela as entregas já na fila e trocar a
  URL **redireciona** as pendentes (o preview diz isso, num texto fixo para a fila não gerar `preview_desatualizado` à toa).
- **`editar_campanha` crítica entre a meia-noite de São Paulo.** O preview cita os dias que já passaram; pedido antes de 00:00 e
  aprovado depois recebe **um** `preview_desatualizado` (aprovar de novo funciona).
- **`minhas_pendentes` corta o `erro` em 250 caracteres** (e as ressalvas em 250 cada / 300 somadas); o texto inteiro está em
  `/pendentes?id=`.
- **Sem item na barra lateral** para `/pendentes`.
- **`tools/list` ~11,9 ms** com 83 tools: memoizar os schemas inferidos por processo antes de acrescentar tools (ver "Desempenho").
- **A aprovação é síncrona e uma execução lenta chega tarde à tela.** O `middleware.Timeout(60s)` do chi **não interrompe** o
  handler: ele só cancela o `r.Context()` aos 60 s e, quando o handler **volta**, tenta `WriteHeader(504)`, que não vale se o handler já
  escreveu. A aprovação roda sob `WithoutCancel`, então segue até o fim e escreve a resposta **verdadeira** (200/422/500) — tarde. Quem
  corta antes é o proxy: o Cloudflare devolve **524** em HTML por volta dos 100 s, e a tela, que não sabe se executou, diz «Não sabemos se
  foi feito» e relê a fila (ver «A tela `/pendentes`»). O pedido fecha como `executada` de qualquer forma; recarregar `/pendentes?id=`
  mostra o desfecho.
- **O pedido não é podado**: os `args` ficam no banco para sempre (inclusive a URL inteira do webhook).

## Como funciona

- `POST https://api.e-monitor.online/mcp` — Streamable HTTP **sem sessão**, resposta JSON
  (go-sdk v1.8.0). `GET`/`DELETE` -> 405 (`Allow: POST`, antes da autenticação: GET sem bearer dá
  405, não 401). Corpo até 1 MB. Protocolos aceitos pelo go-sdk v1.8.0: `2024-11-05`, `2025-03-26`, `2025-06-18`, `2025-11-25` e
  `2026-07-28` (esta última exige `_meta` no corpo e os cabeçalhos `Mcp-Method`/`Mcp-Name`; o CORS
  global já os permite). **Lotes JSON-RPC (corpo que começa com `[`) são recusados com 400**: o SDK
  os aceitaria no protocolo `2025-03-26` (o padrão quando o cliente não manda `Mcp-Protocol-Version`)
  e um pedido levaria centenas de `tools/call` contra a cota de 240/min, que é por pedido.
- Um `Server` do SDK é montado **por pedido** (uma vez só: o SDK o pede duas vezes e o handler guarda o
  primeiro no contexto do pedido; ver "Desempenho"); o principal (dono da credencial) entra por closure
  em cada tool.
- **Falha fechada**: se o middleware de autenticação não estiver montado, o handler responde 500
  `MCP sem autenticação montada` em vez de servir. Nenhuma tool precisa checar `nil`.
- Cada pedido traz `Authorization: Bearer emonitor_mcp_...` (token pessoal ou access OAuth). A
  credencial é relida a cada pedido e o dono tem de ser **admin ativo e não apagado**
  (`role = 'admin' AND is_active AND deleted_at IS NULL`). `operator`, `viewer` e cliente não
  conectam.
- O tráfego do MCP é atribuído ao admin dono em `/admin/monitoring` (`reqmetrics.SetUserID`).
  Sem isso o egress do claude.ai apareceria como IP anônimo, e bloquear esse IP derrubaria o MCP de
  todo mundo.
- O token do MCP **só abre `/mcp`**; `auth.RequireJWT` o recusa (prefixo diferente do JWT).

### Credenciais

Só o hash `SHA-256(JWT_SECRET:token)` chega ao banco (`mcp_tokens`, `mcp_oauth_clients`,
`mcp_oauth_codes`). O texto claro existe uma única vez, na resposta que o emite.

| Prefixo | O quê | Validade | Abre o `/mcp`? |
|---|---|---|---|
| `emonitor_mcp_` | token pessoal (`tipo pessoal`) e access OAuth (`oauth_access`) | pessoal: 90 dias padrão, 1-365; access: 30 dias | sim |
| `emonitor_mcr_` | refresh OAuth (`oauth_refresh`) | 90 dias, rotação a cada uso | **nunca** |
| `emonitor_mcs_` | `client_secret` de cliente confidencial | até ser apagado | n/a (só no `/token`) |
| `emonitor_mcc_` | código de autorização OAuth | 10 min, uso único | n/a |

`expires_at` é `NOT NULL` de propósito (o hub chegou a aceitar documento sem validade).
`last_used_at` é gravado no máximo uma vez por minuto por token.

### Revogação automática

É um **trigger em `users`** (migration 0070, `users_revoga_mcp`), não código do repo: cobre toda
via que mexe no usuário (tela, sync do hub, bloqueio do `/admin/monitoring`). Revoga todo
`mcp_tokens` vivo e apaga os `mcp_oauth_codes` pendentes quando:

- a **senha** muda (`password_hash`) — troca ou reset;
- o usuário é **desativado** (`is_active` true -> false);
- sai de **admin** (`role` deixa de ser `admin`);
- é **apagado** (soft delete: `deleted_at` preenchido).

**Reativar não ressuscita os tokens antigos** (já têm `revoked_at`); a pessoa conecta de novo.

Limitação conhecida e aceita: uma renovação de token que esteja no meio da transação no mesmo
milissegundo da troca de senha pode sobreviver a ela (corrida entre snapshots; fechar exigiria
ordenar locks entre as duas transações).

### Rate limit (em memória, um processo só)

| Limite | Chave | Resposta |
|---|---|---|
| 240 chamadas/min | hash da credencial, antes de qualquer leitura de banco | 429, `Retry-After: 60` |
| 60 credenciais **recusadas** em 15 min | IP | 429, `Retry-After: 900` |
| 20 registros DCR por hora | IP | 429, `Retry-After: 3600` |
| <= 500 clientes públicos pendentes | global | lotou: o pendente público mais antigo é despejado (até 10 por registro) |

- **Token válido nunca é barrado por IP** (egress do claude.ai e NAT do escritório são
  compartilhados): o limite por IP conta só falhas.
- A chave de IP é o `CF-Connecting-IP` (o Cloudflare sempre o sobrescreve); IPv6 vira prefixo
  `/64`. Sem o header (dev, teste, chamada direta), cai no `RemoteAddr`.

### Auditoria

Toda ação sobre credenciais grava em `audit_log` com `metadata.origem = "mcp"`:

| Evento | Quando |
|---|---|
| `mcp.token.criado` | admin gera token pessoal em `/account` |
| `mcp.token.revogado` | admin desconecta em `/account`; `revoga-mcp --aplicar` (uma linha por pessoa, `actor_type 'cli'`, `metadata.pelo = "revoga-mcp"`) |
| `mcp.token.recusado` | token **existente e vivo** cujo dono já não é admin ativo; no máximo 1 por token a cada 10 min (cliente insistente não inunda o log) |
| `mcp.oauth.autorizado` | admin clica **Autorizar** em `/autorizar-ia` |
| `mcp.oauth.cliente_confidencial_criado` | `registra-cliente-mcp` (`actor_type 'cli'`) |
| `mcp.escrita.<tool>` | uma tool de escrita **executou** (`metadata`: `tool`, `nivel`, `args` mascarados, `mcp.token_id`; num crítico aprovado, também `mcp.pendente_id`) |
| `mcp.escrita.falha` | erro interno ou `panic` no `Executar` de uma tool `livre` ou `confirma` (`metadata.erro = "interno"`) |
| `mcp.pendente.criada` · `.aprovada` · `.recusada` · `.executada` · `.falhou` | ciclo de um pedido `critico` (ver "Crítico e /pendentes (Fatia 4)"; `meta.mcp.pendente_id` aponta o pedido) |

Falha ao gravar a auditoria é logada (`Warn`) e **não** derruba o 401/200 da operação principal.

## OAuth 2.1

O authorization server mora na própria API.

| Endpoint | Para quê |
|---|---|
| `GET /.well-known/oauth-protected-resource[/mcp]` | RFC 9728 |
| `GET /.well-known/oauth-authorization-server` | RFC 8414 (`iss` no redirect, RFC 9207) |
| `POST /mcp/oauth/register` | DCR (RFC 7591) — **só cliente público** |
| `GET /mcp/oauth/authorize` | valida e redireciona para `/autorizar-ia` no portal |
| `GET /v1/internal/mcp/oauth/pedido?p=...` | dados do pedido para a tela (JWT admin) |
| `POST /v1/internal/mcp/oauth/grant` | decisão (autorizar/recusar), JWT admin |
| `POST /mcp/oauth/token` | `authorization_code` e `refresh_token` (rotação) |
| `GET/POST/DELETE /v1/internal/mcp/tokens` | tela "Integração com IA" (JWT admin) |

Escopo único: `emonitor:all`. 401 do `/mcp`:
`WWW-Authenticate: Bearer realm="emonitor-mcp"[, error="invalid_token"], resource_metadata="...", scope="emonitor:all"`
— sem `error=` quando nem veio credencial (é o primeiro passo da descoberta, não uma falha).

### Cliente público (claude.ai, Claude Code) — registro aberto, com lista de destinos

- PKCE **S256 obrigatório**; PKCE pela metade ou `plain` é recusado; `code_verifier` sem
  `code_challenge` guardado é tentativa de downgrade (`invalid_grant`).
- O DCR é aberto, então o `redirect_uri` só é aceito se for **igual** a um callback de
  `MCP_REDIRECT_URIS` ou **loopback http** (`localhost`, `127.0.0.1`, `::1`, qualquer porta).
  Padrão da variável:
  `https://claude.ai/api/mcp/auth_callback,https://claude.com/api/mcp/auth_callback`.
  - **Por quê (decisão do Dereck, 2026-10-27):** sem a lista, qualquer um registraria um
    "Claude" com destino próprio (ou homógrafo `сlaude.ai`) e mandaria o link do `/authorize` a um
    admin; quem clicasse em Autorizar entregaria 30 dias de acesso total (phishing de
    consentimento). A comparação é exata (path e query também), então não basta ter o mesmo host.
  - Assistente novo = acrescentar o callback exato à variável, **sem deploy de código**.
  - Entrada que não é URL https válida é ignorada com aviso no log; se todas caírem, a lista fica
    vazia (só loopback) — **não** volta ao padrão (falha fechada).
- Também recusados: host não-ASCII, userinfo (`https://evil@claude.ai`), fragmento, `client_name`
  com caracteres de controle/formatação (U+202E é removido), mais de 5 `redirect_uris`.
- No `/authorize`, em loopback a porta é ignorada (RFC 8252 §7.3: o Claude Code abre porta
  efêmera); fora dele a comparação com o registrado é exata.
- Erro do `/authorize` só volta ao `redirect_uri` para cliente que **já obteve token**; para
  cliente pendente é página 400 (RFC 9700 §4.11.2, senão o endpoint vira open redirect).
- Cliente que nunca chegou ao `/token` é apagado em 1 dia (a faxina roda a cada registro).

### Cliente confidencial (Gemini Enterprise) — NUNCA pelo registro aberto

`POST /mcp/oauth/register` com `client_secret_post`/`client_secret_basic` responde
`invalid_client_metadata` ("registra-cliente-mcp na VM"). Aberto, o confidencial ficaria 7 dias
pendente, encheria o teto de 500 e travaria toda conexão nova. O admin cadastra na VM com
`registra-cliente-mcp` (procedimento em [operations/mcp.md](../operations/mcp.md)):

- sem lista de destinos (quem decide é o admin), mas cada URI ainda passa na validação
  (https ou loopback, sem fragmento/userinfo/host não-ASCII);
- PKCE opcional (mas parcial é recusado); `client_secret` exigido em **toda** ida ao `/token`
  (code e refresh), conferido **antes** de consumir o código, por `Basic` **ou** corpo — nunca os
  dois;
- nunca é despejado do teto; sem primeiro token em 7 dias é apagado.

### Consentimento: `/autorizar-ia`

- O consentimento é uma **página do frontend**, não HTML da API: o JWT do E-monitor vive no
  navegador (não em cookie), então a API não consegue saber quem é o admin num GET direto. Pelo
  mesmo motivo não há CSRF a tratar no `grant` (a API do E-monitor autentica só por header e o CORS
  é aberto).
- `/mcp/oauth/authorize` valida o pedido e responde 302 para `<portal>/autorizar-ia?pedido=<assinado>`.
  O pedido é assinado com o `JWT_SECRET` e vale 10 min. `<portal>` = `NOTIFICATIONS_BASE_URL`.
- A rota exige login e **não** entra em `PUBLIC_ROUTES`. A tela mostra quem pede e o destino, e traz
  o aviso anti-phishing: "Só autorize se foi você que acabou de clicar em 'Conectar' no assistente.
  Se alguém te mandou este link, recuse."
- `frontend/public/_headers` põe `X-Frame-Options: DENY` e `frame-ancestors 'none'` em
  `/autorizar-ia` (clickjacking do botão Autorizar).
- No `grant` o dono é relido (`DonoAdminAtivo`): o JWT do portal vale 8h e a pessoa pode ter sido
  rebaixada desde o login.
- `/mcp/oauth/authorize` responde com `Cross-Origin-Opener-Policy: unsafe-none` — sem isso o
  pop-up do Gemini perde o `window.opener` e o código nunca chega (lição do hub, commit
  `b77b344e`).

### Rotação de refresh

`ConsumirRefresh` revoga o refresh e o devolve atomicamente: o segundo uso do mesmo refresh dá
`invalid_grant`, e a conexão segue com o par novo. Revogar uma conexão OAuth em `/account` derruba
todos os tokens daquele cliente para o usuário.

## Armadilhas e limitações conhecidas

- **`DisableLocalhostProtection: true` é proposital.** O `cloudflared` fala com a API por
  `localhost:8080` com `Host: api.e-monitor.online`; a proteção anti-DNS-rebinding do SDK daria 403
  `invalid Host header` a todo pedido. Todo pedido exige bearer, então rebinding não ganha nada.
  Teste: `TestServidor_HostPublicoPorLoopbackNaoLeva403`. Não "conserte".
- **Girar o `JWT_SECRET` invalida TODA credencial do MCP**, todo `client_secret` e todo código:
  ele salga os hashes. Depois de girar, cada pessoa reconecta e o Gemini precisa de cliente novo
  (`registra-cliente-mcp`). O CLI exige o mesmo `JWT_SECRET` da API, senão o `/token` não reconhece
  o segredo.
- **`JWT_SECRET` com menos de 32 bytes**: `NovoModulo` devolve `nil` e loga
  `mcp: JWT_SECRET ausente ou curto`; o `/mcp` e o OAuth **não montam** (404), sem derrubar o boot.
- **Detecção de reuso de refresh** (reuso depois de 30 s derruba a conexão inteira, como o hub) é
  follow-up; hoje o reuso só recebe `invalid_grant`.
- **Access tokens antigos não são revogados na rotação** (valem até os 30 dias deles ou até a
  conexão ser revogada) e **`mcp_tokens` nunca é podada** (follow-up).
- **O limite de falhas por IP troca 401 por 429, mas não poupa consultas ao banco**: de propósito,
  uma credencial válida nunca é barrada por IP, então cada pedido ainda é conferido no banco.
- **`revoga-mcp --listar` e `/account` contam diferente**: o CLI imprime uma linha por token
  pessoal e **por refresh token vivo** do OAuth; `/account` mostra **uma linha por cliente OAuth**
  (conexão), agrupando os tokens dele.
- **Tool nova exige atualizar `ToolsEsperadas`** (ver "Regra de ouro"); `TestToolsEsperadas_OrdemAlfabeticaE83`
  trava a ordem e o número (83): quem acrescentar tool atualiza o número junto. **Tool de escrita nova também entra
  em `escritasEsperadas`** ([`escrita_inventario_test.go`](../../workers/internal/mcp/escrita_inventario_test.go)), que
  trava o conjunto das 38 (20 da Fatia 3 + 18 críticas), o nível declarado, o sufixo `Nível: …` da descrição, as anotações, o
  campo `confirmacao` (só onde pode pedir confirmação: nunca numa crítica declarada) e a lista de campos proibidos (`stream_url`, `hub_id`,
  `senha`, `token`, `password`, `segredo`, `secret`) em qualquer profundidade do schema. `registro_escritas_test.go` trava que a lista única
  (`escritasDe`) tem os mesmos 38 nomes do inventário e que `indexarEscritas` falha com nome repetido: toda escrita tem executor.
- **Campo obrigatório ausente volta sem `[codigo]`.** A recusa vem do próprio SDK, na validação do schema
  (`validating "arguments": ... required: missing properties: [...]`), antes de a tool rodar. Decisão: fica
  assim — o `required` do schema já orienta o modelo, e reimplementar a validação só para pôr o código não
  compensa.
- **Fuso das falhas:** `station_failures` lê o dia em `time.Local` (UTC no container) e `failures_daily` em
  America/Sao_Paulo. As tools espelham a tela (o mesmo número que cada aba mostra); unificar é follow-up.
- **O aviso diário do portal e `resumo_diario_falhas` podem ler dias diferentes.** O modal do digest
  (`GET /admin/daily-failures-digest`) usa `yesterdayLocal()` = `time.Now()` − 1 dia no fuso do processo (UTC no
  container): entre 21h e 24h de SP ele mostra como "ontem" o dia CORRENTE de SP, ainda incompleto. A tool usa o
  ontem de America/Sao_Paulo (F2-5) e diz isso na descrição; o cálculo é o mesmo (`CampaignFailures.ListForDate`),
  só o dia muda. **Follow-up do handler:** calcular o ontem em America/Sao_Paulo (a chave de "visto" do modal muda
  junto).
- **`visao_gerencial` paga consultas que descarta.** `ManagementOverview.Get` sempre roda `queryStations` (a lista
  do mapa) e `queryRecentDetections` (o feed), e a tool não devolve nenhuma das duas (F2-3). **Follow-up de custo:**
  uma opção no `Get` para pular as duas quando quem chama só quer os KPIs.
- **`insights_campanha` não tem limite de período** (o handler `/insights` também não): um período de anos é aceito
  e custa o que custa na tela. E o `links.tela` leva as emissoras e os materiais filtrados na query: com dezenas de
  ids (36 caracteres cada), a URL pode passar de ~16 KB, acima do que alguns proxies e navegadores aceitam; nesse
  caso abra a tela e refaça o filtro à mão.

### Divergências do plano e da spec (registradas)

O plano (`docs/superpowers/plans/2026-10-28-mcp-fatia-2-relatorios.md`) e a spec não são editados à mão; o que
mudou na implementação, e por quê:

- **Avaliação:** a semente é código Go (`workers/internal/mcp/avaliacao/semear.go`, numa transação, só em banco
  vazio), não o `semente.sql` do plano: as datas relativas e os ids cruzados ficam testáveis e o gabarito acha os
  ids pelos nomes.
- **Deep-link:** a spec §4.5 fala em `links: {tela, baixar?}` com rotas como `/detections`; o código usa
  `{tela, como_baixar}` (o texto do botão a apertar, nunca uma URL de arquivo — F2-1) e os downloads apontam para
  `/reports/airtime`, de onde saem o CSV detalhado e o consolidado (F2-2).
- **Tetos de página menores que o plano** (ver "Tetos de página reais"): cada um medido por um teste de pior caso
  com o alvo de 60 KB.
- **`Leitura.Pool` virou `Leitura.Auditoria`** (um `auditoria.Repo`): nenhuma tool lê o pool cru.
- **Formatos:** `investido` é número (o card "Investido" da tela); `bonificacao` pode ser `null` (a tela esconde o
  card em seleção 100% consolidada, onde o zero é ausência de preço) — no `insights_campanha` e no
  `obter_pos_venda`.
- **`Paginacao.limit` do schema diz "máximo 100"**, mas várias tools de relatório têm teto menor (ver "Tetos de
  página reais"); o teto real está na descrição de cada tool e em `page.limit`.
- **A tela limpa a URL antes de saber se a sessão vale.** Numa aba com sessão vencida, o 401 leva ao login e o
  filtro do link se perde: abra o link de novo depois de entrar.
- **Período fora da janela na aba "Por dia".** `/admin/station-failures?view=by_day&from&to` só aceita de
  hoje − 90 dias até hoje (o limite do seletor); fora disso a aba volta para "Este mês" sem avisar.
- **O consentimento sempre pede login.** A aba aberta pelo assistente começa deslogada (JWT em
  `sessionStorage`, por aba): `/login` volta para `/autorizar-ia?pedido=...`. O login pelo SSO do hub
  (`/sso`) perde o `?pedido=` (`HubSsoPage` sempre vai para `/`); o admin entra com e-mail e senha
  ou reinicia a conexão.
- **Limitação conhecida:** o anti-frame de `/autorizar-ia` (`_headers`) depende de o Cloudflare Pages
  aplicá-lo à resposta de fallback da SPA. Verifique após o deploy:
  `curl -sI "https://e-monitor.online/autorizar-ia?pedido=x" | grep -i frame`.
- **Dev local:** defina `MCP_ORIGEM_PUBLICA=http://localhost:8080` **e**
  `NOTIFICATIONS_BASE_URL=http://localhost:3000` (origem do portal usada no redirect do
  consentimento). Sem a segunda, o `/authorize` redireciona para o frontend de prod.
- **Fatia 3 (plano `2026-10-29-mcp-fatia-3-escrita.md`; as notas datadas estão no próprio plano e na spec §5.2/§6):**
  - **Uso único fora da transação da escrita** (F3-5): a spec §5.2 pedia o `INSERT` na mesma transação; as funções
    reaproveitadas não aceitam `pgx.Tx`. Consome antes, libera só na recusa de negócio.
  - **`editar_emissoras_campanha` não escala** (decisão do dono, 2026-10-29) e **`editar_emissora` escala quando o `pmm` muda
    com veiculação aprovada** (a spec §6 dizia "—"). A regra de data das regras conhece a máscara de dias da semana.
  - **`editar_emissora` grava por `Stations.UpdateCadastro`**, não pelo `Stations.Update` do plano (F3-10): o `Update`
    reserializa o `metadata` e perde chave desconhecida.
  - **Auditoria feita pelo motor, depois do sucesso**, não "dentro da função compartilhada" (spec §4.6): falha de auditoria
    não pode derrubar uma escrita já feita. Na Fatia 4 o mesmo vale para o `critico`: quem audita é o executor da aprovação
    (`Aprovacoes.Aprovar`), o ponto único por onde todo crítico passa (ver F4-7).
  - **`confirmarEExecutar` mora no pacote `mcp`** (`escrita_ferramentas_test.go`), não em `mcptest` como o plano previa: ele
    recebe um `ConfigServidor`, e o `mcptest` não pode importar o pacote que o importa.
  - **`marcar_notificacoes_lidas` recebe `notificacoes[]`** (as chaves de `minhas_notificacoes`), não `chaves[]`.
- **Fatia 4 (plano `2026-10-30-mcp-fatia-4-critico.md`; spec §4.6, §5.2 e §6 já trazem as notas datadas):**
  - **Estados do hub, não os da spec:** `pendente → aprovada → executada`; recusa de negócio volta a `pendente`; só exceção vira `falhou`
    (a spec dizia `executada | falhou`). `tentativa` conta as aprovações.
  - **Auditoria no executor da aprovação**, não "dentro da função compartilhada" (spec §4.6), e a URL do webhook resumida a
    esquema+host+SHA-256 (`ArgsParaAuditoria`).
  - **Aprovação compara o resumo:** `preview_desatualizado` (não está na spec) e as `ressalvas` da execução gravadas no
    pedido e devolvidas pela aprovação, por `/pendentes` e por `minhas_pendentes`.
  - **Catálogo:** `iniciar_campanha`, `pausar_campanha` e `publicar_pos_venda` saíram; entrou `minhas_pendentes` (F4-1).
  - **Chaves `snake_case`** (`pendente_id`, `aprovar_em`, `expira_em`), não as `camelCase` da spec.
  - **`criar_usuario` sem senha** e com boas-vindas obrigatória (e `email_desligado`); `editar_webhook_cliente` sem segredo.

### Recomendações ao dono (NÃO feitas)

1. **Publicar a porta da API só em `127.0.0.1` no compose de prod.** Hoje, um acesso direto à 8080
   permite forjar `CF-Connecting-IP` e fugir dos limites por IP (DCR e falhas de credencial).
2. **O limiter de `/auth/login`, que já existia, é contornável atrás do Cloudflare**:
   `middleware.RealIP` confia em `True-Client-IP`/`X-Real-IP`/`X-Forwarded-For`, que o cliente
   escreve à vontade. O MCP não depende disso (usa `CF-Connecting-IP`), mas o login sim.
3. **As imagens `minio/minio` e `minio/mc` não podem mais ser baixadas do Docker Hub** ("pull access
   denied"): uma máquina nova, ou uma VM que perca o cache de imagens, falha no `docker compose up`.
   Infra, fora do escopo do MCP.
4. **O compose base ainda monta `migrations/` em `docker-entrypoint-initdb.d`** (regra 4.6 do
   `CLAUDE.md` / F-111), o que quebra um banco local novo (os `.down.sql` rodam antes dos `.up.sql`).

## Fora de escopo (até a Fatia 4)

Ficam para a Fatia 5: o lado do hub (linha "Pontes", `SEM_MIDIA_NO_HUB`, §1 do contrato) e os ADRs. Fora do catálogo de propósito (F4-1): iniciar,
pausar e publicar pós-venda (ver "O que saiu do catálogo e por quê"), reativar usuário, trocar senha e anexar comprovante depois.
