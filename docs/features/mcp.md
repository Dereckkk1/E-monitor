---
status: parcialmente-implementado
ultima-verificacao: 2026-10-28
codigo-relacionado:
  - workers/internal/mcp/
  - workers/internal/mcp/oauth/
  - workers/internal/mcp/tokens/
  - workers/internal/mcp/auditoria/
  - workers/cmd/revoga-mcp/main.go
  - workers/cmd/registra-cliente-mcp/main.go
  - migrations/0070_mcp_fundacao.up.sql
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
| 3-5 | Escrita, crítico/pendentes, ecossistema | planejadas |

Tools hoje: as 44 da seção "Tools" (27 das Fatias 0 e 1 + 17 da Fatia 2). O `status` deste doc vira
`implementado` quando as fatias 3-5 fecharem.

## Regra de ouro: o MCP faz parte do pronto

- **Toda feature nova ganha tool** (ou decide por escrito que não ganha).
- **Tool nova entra em `ToolsEsperadas`** ([`servidor.go`](../../workers/internal/mcp/servidor.go))
  **no mesmo commit.** O teste de inventário trava o **conjunto** (clientes guardam o
  `tools/list` em cache, então nada entra ou sai por acidente). A lista está em ordem alfabética
  porque é a ordem em que o go-sdk devolve o `tools/list`; não é uma escolha nossa.
- **Tool nunca reimplementa regra de negócio**: chama a MESMA função que o handler HTTP chama
  (`catalog.ApprovedDetectionsFilter`, base financeira `in_slot + bonus`, `categorizer.Settle`,
  tratamento de `cancelada`). Tela e MCP não podem discordar de número.

## Tools (44)

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
| `mapa_cobertura_campanha` | emissoras com classe e raios Anatel, cidades (20 por emissora), municípios distintos | `LiveMap.Get` + `LiveMap.Coverage` | `/live-map?campaigns[&client_id]` (client_id só quando todas são de um cliente) |
| `falhas_emissoras` | emissoras com falha no dia (déficit TOTAL, sem o split) | `StationFailures.ListForDate` | `/admin/station-failures?view=by_station&date&min_down` |
| `falhas_campanhas` | modo dia (split "não tocou" × "fora do horário") ou histórico | `CampaignFailures.ListForDate` / `ListHistorical` | `…?view=by_campaign&date` / `…?view=by_campaign&sub=historical` |
| `obter_falhas_campanha` | o drill-in de UMA campanha (por emissora, com o split) | `CampaignFailures.Get` | `…?view=by_campaign&campaign=<id>` + `como_baixar` (PDF de cobrança) |
| `falhas_por_dia` | série diária + dias com falha, pico e média | `FailuresDaily.ListDaily` | `…?view=by_day&from&to&min_down` |
| `resumo_diario_falhas` | o digest de ontem (sem `seen`; nunca marca visto) | `CampaignFailures.ListForDate` | `…?view=by_campaign&date` |
| `listar_pos_vendas` | pós-vendas + contagens por estado | `postsale.Repo.List` | `/admin/pos-venda` (e `/admin/pos-venda/<id>` por item) |
| `obter_pos_venda` | números congelados, checking (20 por campanha), resumo de destinatários | `Repo.Get` + `Repo.Payload` | `/admin/pos-venda/<id>` |
| `destinatarios_pos_venda` | quem recebeu (sem token) ou quem receberia hoje | `Repo.Get`, `ActiveClientUsers`, `InternalRecipients` | — |
| `auditoria_mcp` | eventos gravados pelo MCP, por cursor (`page.total: null`) | `auditoria.Listar` (`metadata->>'origem' = 'mcp'`) | — |

**Tetos de página reais** (o `limit` do schema genérico diz "máximo 100", mas estas tools cortam antes; o teto
está na descrição de cada uma e em `page.limit`): `mapa_cobertura_campanha` 25, `relatorio_data_hora` 50,
`falhas_emissoras` 8, `falhas_campanhas` (modo dia) 8, `obter_falhas_campanha` 50, `obter_pos_venda` 5
campanhas por página, `destinatarios_pos_venda` 50, `auditoria_mcp` 20. Listas DENTRO de um item (quedas,
campanhas, emissoras, cidades, linhas de checking, dias de falha) saem cortadas em 20, com o `*_total` real e
uma ressalva. O alvo é cada resposta abaixo de 60 KB (o cliente MCP corta a saída em ~25k tokens), travado por
teste nas tools de lista larga.

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

## Como funciona

- `POST https://api.e-monitor.online/mcp` — Streamable HTTP **sem sessão**, resposta JSON
  (go-sdk v1.8.0). `GET`/`DELETE` -> 405 (`Allow: POST`, antes da autenticação: GET sem bearer dá
  405, não 401). Corpo até 1 MB. Protocolos aceitos pelo go-sdk v1.8.0: `2024-11-05`, `2025-03-26`, `2025-06-18`, `2025-11-25` e
  `2026-07-28` (esta última exige `_meta` no corpo e os cabeçalhos `Mcp-Method`/`Mcp-Name`; o CORS
  global já os permite). **Lotes JSON-RPC (corpo que começa com `[`) são recusados com 400**: o SDK
  os aceitaria no protocolo `2025-03-26` (o padrão quando o cliente não manda `Mcp-Protocol-Version`)
  e um pedido levaria centenas de `tools/call` contra a cota de 240/min, que é por pedido.
- Um `Server` do SDK é montado **por pedido**; o principal (dono da credencial) entra por closure
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
- **Tool nova exige atualizar `ToolsEsperadas`** (ver "Regra de ouro"); `TestToolsEsperadas_OrdemAlfabeticaE44`
  trava a ordem e o número (44): quem acrescentar tool atualiza o número junto.
- **Campo obrigatório ausente volta sem `[codigo]`.** A recusa vem do próprio SDK, na validação do schema
  (`validating "arguments": ... required: missing properties: [...]`), antes de a tool rodar. Decisão: fica
  assim — o `required` do schema já orienta o modelo, e reimplementar a validação só para pôr o código não
  compensa.
- **Fuso das falhas:** `station_failures` lê o dia em `time.Local` (UTC no container) e `failures_daily` em
  America/Sao_Paulo. As tools espelham a tela (o mesmo número que cada aba mostra); unificar é follow-up.
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

## Fora de escopo da Fatia 0

`mcp_confirmacoes_usadas` (Fatia 3) e `mcp_acoes_pendentes` + `/pendentes` (Fatia 4) ainda não
existem; os níveis de escrita (`livre`/`confirma`/`critico`) estão nas `Instrucoes` do servidor mas
sem nenhuma tool de escrita.
