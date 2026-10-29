---
status: parcialmente-implementado
ultima-verificacao: 2026-10-29
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
| 3 | Escrita `livre` e `confirma`: 20 tools + motor de níveis (token HMAC de uso único, escalada a `critico` reavaliada a cada chamada, `critico` barrado até a Fatia 4) + camada `internal/acoes` compartilhada com as rotas HTTP | implementada (branch `feat/mcp-server`, não deployada; ver "Escrita (Fatia 3)") |
| 4-5 | `critico` executável (`/pendentes`), ecossistema | planejadas |

Tools hoje: as 64 da seção "Tools" (27 das Fatias 0 e 1 + 17 da Fatia 2 + 20 da Fatia 3). O `status` deste doc vira
`implementado` quando as fatias 4-5 fecharem.

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

## Tools (64)

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
| `destinatarios_pos_venda` | quem recebeu (sem token) ou quem receberia hoje | `Repo.Get`, `ActiveClientUsers`, `InternalRecipients` | — |
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
descrição de toda tool termina em `Nível: livre|confirma|critico.`. **Sobe para `critico`** significa: a resposta vem barrada
(`executou: false`, `nivel: critico`, motivo, `links.tela`) e nada é gravado; hoje o crítico só se faz pela tela.

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
`Preview`, `Executar`, `Tela`, `Recurso`) e `ferramentaEscrita` o registra; a Fatia 4 reaproveita o mesmo valor para
executar a pendente aprovada. Fluxo de toda chamada, na ordem:

1. **Nível efetivo.** `Escalar` consulta o banco e roda em **toda** chamada, preview e confirmação (F3-2): o token fixa os
   argumentos, não o estado do mundo. Se entre o preview e a confirmação entrou uma tocada que faz a escrita escalar, a
   confirmação devolve o ramo `critico` e o token **não** é consumido. O nível nunca desce do declarado.
2. **`livre`:** executa na hora (`executou: true`).
3. **Preview** (`confirma` e `critico`): relê o estado, valida e monta `{resumo, mudancas[{campo, de, para}], efeitos[],
   ressalvas[]}` com nomes (não só ids). Recusa de negócio aqui (`entrada_invalida`, `nao_encontrado`…) vale antes de qualquer
   escalada ou token.
4. **`critico`:** barrado (ver abaixo).
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

### Crítico barrado até a Fatia 4 (F3-1)

A spec põe o `critico` executável (e `mcp_acoes_pendentes`, `/pendentes`) na Fatia 4. Até lá, quando a escalada dá `critico` a
tool calcula o preview e devolve, **sem `isError`**: `{executou: false, nivel: "critico", motivo, preview, aviso, links: {tela}}`,
não escreve nada, não consome token e grava `mcp.escrita.requer_aprovacao` no `audit_log`. O `aviso` diz "Nada aconteceu. …
faça pela tela, logado", e as `Instrucoes` mandam o modelo repetir isso (`TestInstrucoes_EscritaCriticoBarrado`). **É exatamente
este ramo que a Fatia 4 troca por `criarPendente`** — o resto do motor não muda. O `motivo` diz qual condição escalou (pode
haver mais de uma).

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
  vivo, os alertas de queda e, se a campanha está ativa, os workers.
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

- **`Executar`** roda sob `context.WithoutCancel` com prazo de **60 s** (o `middleware.Timeout` do router): depois de consumir o
  token, o desfecho não depende de o cliente continuar conectado.
- **Auditoria** feita pelo motor **depois** do sucesso e fora da transação da escrita: `audit_log` com
  `actor_id` = o admin, `actor_type 'user'`, `action = "mcp.escrita.<tool>"`, `resource`/`resource_id` do alvo e
  `metadata = {origem: "mcp", tool, nivel, args, mcp: {token_id}}`. Falha da auditoria é logada e **não** derruba a resposta
  (a escrita já aconteceu). Eventos próprios: `mcp.escrita.requer_aprovacao` (ramo barrado, com o `motivo`) e
  `mcp.escrita.falha` (erro interno ou `panic` no `Executar`; só `"erro": "interno"`, nunca o texto do erro Go). Recusa de negócio
  não grava nada (nada foi escrito).
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
  sem recategorizar.

### Desempenho

Com as 64 tools, `BenchmarkNovoServidor` (montar o `Server` do SDK) ficou em **~5,0 ms/op** (~3,2 MB, ~8,8 mil alocações), acima
dos 4 ms do limite do plano (eram 2,75 ms com 44 tools). O go-sdk pede o `Server` **duas** vezes por pedido stateless (uma só
para ler as versões de protocolo), então `NovoHandlerMCP` passou a montá-lo **uma vez por pedido** (um guardador no
contexto do pedido; nada é compartilhado entre pedidos nem entre principais). `BenchmarkPedidoToolsList` (um `tools/list` pelo
handler real, sem rede): **~12,5 ms → ~9,1 ms** e 24,6 mil → 15,8 mil alocações. Travado por
`TestServidor_MontaOServerUmaVezPorPedido` (falha sem a memoização) e `TestServidor_MemoizacaoNaoMisturaPrincipais`.

### Limitações e follow-ups (Fatia 3)

- **`EmSegundoPlano` sem limite de concorrência:** um pico de edições de regra ou de tipo dispara uma goroutine de recat por
  gatilho (com recover, mas sem teto). Follow-up: semáforo.
- **`TrocarTipoMaterial` não recategoriza o tipo ANTIGO** (as células do tipo antigo perdem um membro e ficam como estavam; o
  `projrecon` cura em até 48 h). A rota de regra foi corrigida nisso (F3-8b); a de material não.
- **Textos em inglês nas `Recusa` que vêm das rotas:** as validações que saíram do handler guardam a mensagem antiga
  (`name is required`, `end_date must be on or after start_date`, `time_start must be HH:MM…`), e o MCP as repete ao modelo.
- **`CriarCampanha` (rota) não apara o nome nem valida data zero** (a rota sempre aceitou como veio; no MCP quem valida é a tool).
- **`acoes.EditarCampanha` NÃO é transacional** (nome e datas são gravados antes de `AtualizarHubCode`). Uma `Recusa` vinda de
  `AtualizarHubCode` depois do `UpdateBasic` **liberaria o token** com a escrita pela metade. **Inalcançável na Fatia 3**
  (mudar o código do hub escala a `critico`, então nunca chega ao `Executar`), mas **ALCANÇÁVEL na Fatia 4**: um `editar_campanha`
  `critico` com troca de código, executado após a aprovação. **Tem de ser corrigido na Fatia 4** (uma transação, ou a
  `AtualizarHubCode` primeiro, ou não liberar o token quando houver escrita parcial).
- **Bug latente em `catalog/detections.go:~604`:** o `time_end` da regra é lido com `time.Parse("15:04:05", …)` ignorando o erro
  (`"24:00"` vira `00:00` no caminho de insert em Go, enquanto o SQL aceita `24:00`). Não é do MCP; anotado.
- **`HaVeiculacao` para regras não filtra por dia:** a escalada da regra olha a campanha inteira, então uma campanha só com
  tocadas `out_date` (fora do período) também escala. Mais estrita do que o necessário, nunca mais frouxa.
- **Token gasto em erro interno depois de consumir** (F3-5): a pessoa pede o preview de novo.
- **`token_id` não aparece na tool `auditoria_mcp`** e dados de contato/CNPJ aparecem nos `args` do `audit_log` (ver acima).
- **Recat assíncrono:** a regra e a troca de tipo respondem antes de a recategorização acabar (até 30 s); a resposta traz a
  ressalva. O override é síncrono.
- **Renumerar as migrations no merge:** o `master` já tem `0070_users_username`, então `0070_mcp_fundacao` vira `0071` e
  `0071_mcp_confirmacoes` vira `0072` (e as referências a "migration 0070" neste doc).
- **Trailers de commit da branch** misturam "Claude Opus 5.5" e "Claude Sonnet 5.5" (dois modelos trabalharam nela).

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
| `mcp.escrita.<tool>` | uma tool de escrita **executou** (`metadata`: `tool`, `nivel`, `args` mascarados, `mcp.token_id`) |
| `mcp.escrita.requer_aprovacao` | a escalada deu `critico` e a tool foi barrada (nada foi escrito; `metadata.motivo`) |
| `mcp.escrita.falha` | erro interno ou `panic` no `Executar` (`metadata.erro = "interno"`) |

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
- **Tool nova exige atualizar `ToolsEsperadas`** (ver "Regra de ouro"); `TestToolsEsperadas_OrdemAlfabeticaE64`
  trava a ordem e o número (64): quem acrescentar tool atualiza o número junto. **Tool de escrita nova também entra
  em `escritasEsperadas`** ([`escrita_inventario_test.go`](../../workers/internal/mcp/escrita_inventario_test.go)), que
  trava o conjunto das 20, o nível declarado, o sufixo `Nível: …` da descrição, as anotações, o campo `confirmacao` e a
  lista de campos proibidos (`stream_url`, `hub_id`, `senha`, `token`, `password`) em qualquer profundidade do schema.
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
    não pode derrubar uma escrita já feita. A gravação dentro da função compartilhada vale só para o `critico` executado fora
    da tool (Fatia 4).
  - **`confirmarEExecutar` mora no pacote `mcp`** (`escrita_ferramentas_test.go`), não em `mcptest` como o plano previa: ele
    recebe um `ConfigServidor`, e o `mcptest` não pode importar o pacote que o importa.
  - **`marcar_notificacoes_lidas` recebe `notificacoes[]`** (as chaves de `minhas_notificacoes`), não `chaves[]`.

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

## Fora de escopo (até a Fatia 3)

`mcp_acoes_pendentes`, `/pendentes` e o varredor são da Fatia 4 e **ainda não existem**: `critico` não executa pela tool
(ver "Crítico barrado até a Fatia 4"). `mcp_confirmacoes_usadas` existe desde a Fatia 3 (migration 0071).
