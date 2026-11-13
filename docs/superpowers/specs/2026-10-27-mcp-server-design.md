# MCP do E-monitor — desenho

- **Data:** 2026-10-27
- **Status:** aprovado em brainstorming (Dereck), aguardando plano de execução
- **Branch:** `feat/mcp-server`
- **Contrato que este desenho cumpre:** `hub/docs/architecture/mcp-ecossistema.md` (em `origin/main` do hub), §3 e §6
- **Referências de implementação:** Plura ADR-010/011/012 + `plura/docs/tech/mcp.md`; hub `backend/src/mcp/` + `docs/features/mcp.md` + `docs/operations/mcp.md`; mcp-builder (anthropics/skills, `reference/mcp_best_practices.md` e `reference/evaluation.md`)

## 1. Objetivo

Um servidor MCP que dá a um **administrador interno** acesso de leitura, escrita e relatório ao
E-monitor inteiro **do lado do negócio** (clientes, campanhas, materiais, emissoras, veiculações,
regras, pós-venda, usuários, falhas, assertividade), por linguagem natural, a partir de
**claude.ai (web/celular), Claude Code e Gemini Enterprise**.

O E-monitor é a terceira peça do ecossistema (Plura, hub, E-monitor). O hub é o canônico de
cadastro e números consolidados; o E-monitor é a evidência ao vivo (a tocada, o áudio, a grade,
a categorização). Nenhum MCP chama outro — o modelo passa de um para outro com os ids das pontes.

### Não objetivos

- Cliente externo (role cliente), `operator` e `viewer` **não** se conectam. Só `role=admin`.
- Operação técnica fica de fora: supervisor, workers, stream_url, teste de conexão, saúde de
  stream, calibração, threshold, tiering, `/admin/monitoring`, bloqueio de IP/usuário.
- Nada de SQL livre, deploy, migration ou CLI de backfill pelo MCP.
- Upload de binário (áudio de material, PDF de comprovante): o contrato proíbe base64/binário. O
  MCP devolve deep-link para a tela de upload.
- Criar chave de API (devolveria segredo), trocar/redefinir senha.
- Sugestões: a fila mora no hub desde 2026-10-26 (`docs/features/suggestions-board.md`); o MCP do
  hub já as cobre.
- **MCP Apps** (resposta renderizada como UI no chat): fora desta entrega. O desenho deixa a porta
  aberta porque toda tool devolve `structuredContent`.

## 2. Decisões tomadas

| # | Decisão | Por quê |
|---|---------|---------|
| D1 | MCP **dentro da API Go** (`POST /mcp`), tools chamando os mesmos serviços das telas | MCP e tela não podem discordar de número (cota, base financeira, impactos). Plura ADR-010 provou o desenho |
| D2 | SDK oficial `github.com/modelcontextprotocol/go-sdk`, Streamable HTTP **stateless**, JSON (sem SSE), `GET`/`DELETE` → 405 | Contrato §3.1. Se o spike (Fatia 0) mostrar que o SDK não suporta stateless/versão de spec dos clientes, cai para JSON-RPC à mão sobre `net/http` |
| D3 | **OAuth 2.1 desde a v1** + token pessoal | claude.ai e Gemini Enterprise só ligam por OAuth; Claude Code aceita os dois |
| D4 | Só `role=admin`, relido do banco a cada pedido | Contrato §3.1; papéis do E-monitor são próprios |
| D5 | Escrita em três níveis (`livre` / `confirma` / `critico`) com `/pendentes` no portal | Contrato §3.5; consistência entre os três MCPs |
| D6 | Nível **sobe com os argumentos**: escrita que atinge **dia já veiculado** vira `critico` | Quase toda escrita do E-monitor mexe em categoria → cobrança. A regra pura jogaria tudo em `critico`; a escalada mantém `confirma` para o futuro e põe humano no meio quando muda número que o cliente já viu/foi faturado |
| D7 | Relatórios: **dados estruturados + deep-link autenticado** para o portal (não URL assinada) | Contrato §3.6 proíbe URL assinada (credencial ao portador, fica gravada no histórico do chat). PDFs são gerados no navegador (`jsPDF`/`html2canvas`), então o MCP não os gera: devolve o link da tela já filtrada |
| D8 | Token de confirmação de **uso único** via `INSERT` com chave única no Postgres | Contrato §6 nota 1; replay duplicaria criação |
| D9 | Rate limit **em memória** (240/min por hash de token), reusando `auth.LoginRateLimiter` | Contrato §3.1. A API Go não tem cliente Redis (o `redis` do compose não é usado por ela) e roda num processo só; o limiter em memória já protege o `/auth/login` |
| D10 | `DisableLocalhostProtection: true` no handler do SDK | Em prod o `cloudflared` fala com a API por `http://localhost:8080`; a proteção anti-DNS-rebinding do SDK daria 403 a todo pedido com `Host: api.e-monitor.online`. Todo pedido ao `/mcp` exige bearer, então rebinding não ganha nada |

## 3. Arquitetura

```
claude.ai / Claude Code / Gemini Enterprise
        │  Authorization: Bearer emonitor_mcp_… | access OAuth
        ▼
  API Go (chi) ──► /.well-known/oauth-protected-resource/mcp
              ──► /.well-known/oauth-authorization-server
              ──► /mcp/oauth/{register,authorize,token}   (CORS aberto, sem JWT)
              ──► /v1/internal/mcp/oauth/{pedido,grant}    (JWT admin do portal)
              ──► /v1/internal/mcp/tokens[, /pendentes]    (JWT admin)
              ──► POST /mcp  ─► mcpauth (token → user admin ativo) ─► ratelimit ─► McpServer novo por pedido
                                                                         │
                                                         tools ──► mesmos services/catalog das rotas HTTP
                                                                         │
                                                         escrita ──► motor de níveis ──► audit_log (origem mcp)

Frontend (CF Pages): /autorizar-ia (consentimento), /pendentes (aprovação), /account → "Integração com IA"
```

Pacote novo: `workers/internal/mcp/`

| Arquivo/subpacote | Responsabilidade |
|---|---|
| `server.go` | monta o `McpServer` por pedido, instruções, registro na ordem fixa |
| `toolkit.go` | envelope de lista, `ressalvas`, erro `"<msg> [<código>]"`, deep-link builder |
| `auth.go` | resolve bearer → token (pessoal ou OAuth) → usuário admin ativo; 401 com `WWW-Authenticate` |
| `auth.go` (rate limit) | 240/min por hash (em memória, `auth.LoginRateLimiter`); falhas de auth por IP (60/15min); DCR 20/h por IP |
| `tokens/` | emissão/hash/revogação de tokens pessoais e OAuth (tabela única) |
| `oauth/` | metadata, DCR (público + confidencial), authorize, grant, token, refresh com rotação |
| `write/` | `Escrita{Nome, Nivel, EscalarPara, Preview, Executar}`, motor de níveis, token de confirmação, pendentes, varredor |
| `tools/` | uma família por arquivo (`clientes.go`, `campanhas.go`, `materiais.go`, `emissoras.go`, `veiculacoes.go`, `relatorios.go`, `falhas.go`, `usuarios.go`, `posvenda.go`, …) |
| `inventory_test.go` | trava a lista exata de tools (`ToolsEsperadas`) e "toda escrita declara nível" |

### Regra de ouro das tools

Uma tool **nunca reimplementa regra de negócio**. Ela chama a mesma função que o handler HTTP
chama. Se o handler tem lógica que não está numa função reutilizável, a Fatia que precisar dela
extrai a função primeiro (refactor sem mudança de comportamento, coberto pelos testes do handler)
e só depois cria a tool. Em especial: `catalog.ApprovedDetectionsFilter`, a base financeira
compartilhada (`in_slot + bonus`), `categorizer.Settle`, o tratamento de `cancelada`.

## 4. Fundação (contrato §3)

### 4.1 Identidade e tokens

- Token pessoal `emonitor_mcp_` + 64 hex; guardado só como `SHA-256(JWT_SECRET:token)`; mostrado
  uma vez; 90 dias padrão, 365 máximo; nome livre; `last_used_at`.
- Tokens OAuth na **mesma tabela** (`tipo`: `pessoal` | `oauth_access` | `oauth_refresh`).
  Access 30 dias; refresh `emonitor_mcr_` 90 dias, rotação atômica: o refresh usado uma vez não
  serve de novo (`invalid_grant`), e a conexão continua com o par novo — como a Plura. A detecção
  de reuso do hub (reuso depois de 30 s derruba a conexão inteira, por suspeita de roubo) fica
  como follow-up; refresh nunca autentica `/mcp`.
- Toda chamada relê o usuário: `role = admin` **e** ativo. Senão 401.
- Revogação automática ao: trocar senha, reset de senha, desativar usuário, rebaixar de admin,
  apagar (soft delete). Implementada como **trigger em `users`** (migration 0070), não no repo:
  cobre todo caminho que mexe no usuário (tela, sync do hub, bloqueio do `/admin/monitoring`) e
  impede que reativar alguém ressuscite tokens antigos. Limitação aceita: uma renovação de token
  que esteja no meio da transação no mesmo milissegundo da troca de senha pode sobreviver a ela
  (corrida entre snapshots; fechar exigiria ordenar locks entre as duas transações).
- CLI `revoga-mcp` (`--listar`, `--email X [--aplicar]`, `--todos [--aplicar]`), dry-run sem
  `--aplicar` (o dry-run conta exatamente o que o `--aplicar` revoga; `--todos` também apaga os
  códigos OAuth pendentes), uma linha de auditoria por pessoa, com `actor_type 'cli'` e o alvo em
  `metadata`. CLI `registra-cliente-mcp` cadastra o cliente confidencial (§4.2). **Duas linhas no
  `workers.Dockerfile` para cada CLI** (regra 6.7).
- O token do MCP **só abre `/mcp`**. `auth.RequireJWT` continua recusando-o (prefixo diferente
  do JWT; teste garante).
- Tela "Integração com IA" em `/account`: criar (mostra uma vez), listar, revogar.

### 4.2 OAuth 2.1 (authorization server na própria API)

- Descoberta: RFC 9728 (`/.well-known/oauth-protected-resource/mcp`) e RFC 8414
  (`/.well-known/oauth-authorization-server`); issuer pelo host público (`MCP_ORIGEM_PUBLICA`),
  `iss` no redirect (RFC 9207).
- DCR aberto (RFC 7591) **só para cliente público**, 20/h por IP (`CF-Connecting-IP`, IPv6 por
  /64 — os cabeçalhos que o `middleware.RealIP` lê são forjáveis atrás do Cloudflare). No máximo
  500 clientes que nunca pegaram token: lotou, sai o pendente público mais antigo (recusar o novo
  deixaria um atacante bloquear toda conexão nova lotando o teto). `client_id` sozinho não dá
  acesso a nada.
- **Destinos permitidos (decisão do Dereck, 2026-10-27):** o registro aberto só aceita
  `redirect_uri` https **igual a um callback** de `MCP_REDIRECT_URIS` (padrão
  `https://claude.ai/api/mcp/auth_callback` e `https://claude.com/api/mcp/auth_callback`; sem porta,
  caminho ou query diferentes), mais loopback http em qualquer porta para o Claude Code. Fecha o
  phishing de consentimento: sem isso, qualquer um registra um "Claude" com destino próprio (ou
  homógrafo `сlaude.ai`) e um admin que clique em Autorizar entrega 30 dias de acesso total. Host
  não-ASCII é recusado, esquema e host são guardados em minúsculas e `client_name` perde
  caracteres de controle/formatação (U+202E). Cliente novo (outro assistente) = acrescentar o
  callback na variável, sem deploy de código.
- **Cliente público**: PKCE S256 obrigatório (challenge base64url de 43 chars).
- **Cliente confidencial (Gemini Enterprise)**: **nunca pelo registro aberto** — o admin cadastra
  na VM com o CLI `registra-cliente-mcp --nome … --redirect …` (sem lista de destinos: quem decide
  é o admin). `token_endpoint_auth_method` = `client_secret_post`
  ou `client_secret_basic`; secret `emonitor_mcs_…` devolvido uma vez, só hash guardado; pode omitir
  PKCE, mas PKCE parcial é recusado; secret exigido em **toda** ida ao `/token` (code e refresh),
  verificado **antes** de consumir o code. Redirect do Gemini:
  `https://vertexaisearch.cloud.google.com/oauth-redirect`.
- `redirect_uri` por igualdade exata, https (http só loopback), sem fragmento nem userinfo; em
  loopback a porta é ignorada no `/authorize` (RFC 8252 §7.3 — o Claude Code abre porta efêmera);
  erro só volta ao redirect depois de ele ser confiável e de o cliente já ter pegado um token
  (RFC 9700 §4.11.2 — senão é página 400). PKCE downgrade recusado: `code_verifier` sem
  `code_challenge` guardado dá `invalid_grant`.
- `/mcp/oauth/authorize` valida e faz 302 para o frontend `/autorizar-ia?pedido=…`. **COOP
  `unsafe-none` só nessa rota** (lição hub `b77b344e`: o COOP padrão separava o pop-up do Gemini
  do `window.opener`).
- `/autorizar-ia` (frontend, exige login, **não** entra em `PUBLIC_ROUTES`) mostra quem pede e
  envia a decisão para `POST /v1/internal/mcp/oauth/grant` com o JWT normal (o JWT do E-monitor vive no
  navegador, não em cookie — por isso não há CSRF a tratar e o consentimento não pode ser HTML
  servido pela API). Anti-frame (`X-Frame-Options: DENY` / `frame-ancestors 'none'`) em
  `/autorizar-ia` e `/pendentes`.
- Código de autorização: 10 min, uso único, morto se houve troca de senha depois da emissão.
- 401 de `/mcp`: `WWW-Authenticate: Bearer realm="emonitor-mcp", [error="invalid_token",]
  resource_metadata="…", scope="emonitor:all"` (sem `error=` quando não veio credencial).
- CORS: a API do E-monitor já é `Access-Control-Allow-Origin: *` globalmente e autentica só por
  header (JWT/bearer, nunca cookie), então não há CSRF a proteger e o `grant` pode ficar sob o
  CORS global sem o defeito nº 5 da Plura (que era cookie + CORS aberto). O middleware global
  ganha `Mcp-Protocol-Version`, `Mcp-Session-Id` e `Last-Event-ID` em `Allow-Headers` e expõe
  `WWW-Authenticate`, `Mcp-Session-Id`, `Mcp-Protocol-Version` e `Retry-After`.

### 4.3 Rate limit

- 240 chamadas/min por hash do token, antes de qualquer leitura de banco; 429 com `Retry-After`.
- Falhas de autenticação: 60 por IP em 15 min. **Token válido nunca é barrado por IP** (egress do
  claude.ai e NAT do escritório são compartilhados).
- Body de `/mcp` até 1 MB.

### 4.4 Resultado e erros (contrato §3.3/§3.4)

- Mesmo JSON em `content[0].text` e `structuredContent`. Sem markdown, sem `outputSchema`.
- Listas: `{items, page:{total, limit, offset, returned, has_more}}`, `limit` padrão 25, teto 100,
  `offset` com teto (lição hub: `offsetSchema` sem limite).
- Ids vêm com nome (`cliente: {id, nome}`); `null` = não se aplica, nunca zero.
- `ressalvas[]` para o que o modelo **tem** de repetir (ex.: "assertividade só enxerga miss
  reportado", "campanha cancelada: déficit congelado em cancelled_at", "números caíram em
  2026-08-17 por mudança de regra").
- Instantes em ISO 8601 UTC; períodos `YYYY-MM-DD`; dinheiro em reais.
- Falha de negócio: `isError: true`, `"<mensagem> [<código>]"` com o mesmo código da API HTTP.
- Nunca sai de uma tool: segredo, hash, URL assinada, base64, binário, link de boas-vindas.

### 4.5 Deep-links (D7)

Base = origem do portal (`e-monitor.online`, configurável). Toda tool de relatório devolve
`links: {tela, baixar?}` apontando para rotas que já existem: `/insights`, `/detections`,
`/detections/:id`, `/reports/airtime`, `/admin/station-failures`, `/campaigns/:id/edit`,
`/admin/pos-venda/:id`, `/materials`, `/live-map`. Onde a tela ainda não lê filtro da URL, a
Fatia 2 acrescenta a leitura do query string (só frontend, sem efeito colateral).

### 4.6 Auditoria

- Ressuscita `audit_log` (migration 0009, hoje sem escritor): toda escrita pelo MCP grava
  `actor_id`, ação, alvo, argumentos canônicos e `meta.mcp = {token_id, pendente_id?}`.
- O registro é feito **dentro da função compartilhada** chamada pelo `Executar` (o `critico`
  executa fora da tool, na aprovação).
  > **Na Fatia 3 (2026-10-29)** quem grava é o motor da tool, depois do sucesso do `Executar` e fora da transação da escrita
  > (falha de auditoria é logada e não derruba uma escrita já feita): `mcp.escrita.<tool>`, `mcp.escrita.requer_aprovacao` e
  > `mcp.escrita.falha`. A gravação dentro da função compartilhada fica para o `critico` executado na aprovação (Fatia 4).
  > **Na Fatia 4 (2026-11-03)** quem grava o `critico` é o executor da aprovação (`Aprovacoes.Aprovar`), depois do sucesso e fora da
  > transação da escrita, não "dentro da função compartilhada": o executor é o ponto único por onde todo crítico passa, e gravar dentro
  > de cada função de `acoes` a gravaria também nas escritas da tela, que não são do MCP. `mcp.escrita.requer_aprovacao` deixou de
  > existir (agora é `mcp.pendente.criada`); `meta.mcp = {token_id, pendente_id}` vale em toda linha do ciclo do pedido. Um gancho
  > por tool (`ArgsParaAuditoria`) pode resumir um argumento sensível no `audit_log` (a URL do webhook vira esquema+host+SHA-256).
- Eventos próprios: `mcp.token.criado|revogado|recusado` (só para token que existe),
  `mcp.oauth.*`, `mcp.pendente.criada|aprovada|recusada|executada|falhou`.

### 4.7 Instruções do servidor

Texto fixo (gerado de constantes onde houver enum) com: o que é o E-monitor e o papel dele no
ecossistema; glossário (veiculação, material, célula-dia, cota, `in_slot`/`bonus`/`out_slot`/
`out_date`, déficit "não tocou" × "fora do horário", impactos = PMM × (`in_slot` + `bonus`), CPM
dinâmico, consolidado × por inserção, cancelada); datas; "o servidor soma, nunca recalcule";
pontes (`obter_cliente.hub_id`, `obter_campanha` aceita `codigo` do hub); e o parágrafo dos três
níveis copiado do hub, incluindo **"NUNCA reenvie o token de confirmação sozinho, nem na mesma
resposta"** e **"entregue o link e diga que nada aconteceu ainda"**.

## 5. Escrita em três níveis

### 5.1 Classificação

`critico` se **qualquer** "sim": irreversível na prática? o cliente externo passa a ver algo
**novo** (email, ativação, publicação)? mexe em dinheiro **já realizado** (preço, CPM fixo,
categoria de tocada já veiculada)? dispara email para fora? Nenhum "sim" e é interno/desfazível:
`livre`. O resto: `confirma`.

`EscalarPara(args)` sobe o nível (nunca desce). Regra E-monitor: escrita que recategoriza **dia
anterior a hoje (SP)** vira `critico`.

### 5.2 Mecânica

- `Escrita{Nome, Titulo, Descricao, Nivel, EscalarPara, Schema, Preview(ctx,args), Executar(ctx,args)}`
  registrada numa lista de dados (`Escritas`) — a mesma lista serve ao servidor e à aprovação de
  pendentes (toda `critico` tem executor por construção).
- Anotação `destructiveHint = nivel != livre || podeEscalar`.
- `confirma`: 1ª chamada devolve `{executou:false, preview, confirmacao}`; token HMAC-SHA256
  (`JWT_SECRET`, com separação de domínio) sobre `(tool, args canônicos, user_id, exp)`, 10 min
  (+ 1 min de folga), comparação em tempo constante; uso único via `INSERT INTO
  mcp_confirmacoes_usadas(hash) ON CONFLICT DO NOTHING`. Token inválido/vencido/usado:
  `{executou:false, erro:"… Peça o preview outra vez."}` (não é erro).
  > **Atualizado na execução da Fatia 3 (2026-10-29, plano F3-5).** O `INSERT` NÃO está na mesma
  > transação da escrita: nenhuma das funções reaproveitadas das rotas aceita `pgx.Tx` (cada uma
  > abre a sua ou usa o pool, e o recat abre a própria), e passar uma transação por todas mudaria o
  > comportamento das rotas HTTP. O token é **consumido ANTES de executar** e **liberado
  > (`DELETE`) só na falha de negócio** (`acoes.Recusa`); erro interno ou `panic` não libera, porque
  > algo pode ter sido gravado (a pessoa pede o preview de novo). Poda oportunista a cada consumo
  > (linhas vencidas há > 1 h) até o varredor da Fatia 4. A auditoria é gravada pelo motor depois do
  > sucesso, também fora da transação da escrita.
- `critico`: grava `mcp_acoes_pendentes` (24 h) e devolve
  `{executou:false, preview, pendenteId, expiraEm, aprovarEm:"<portal>/pendentes?id=…", aviso}`.
  Estados: `pendente → aprovada → executada | falhou`, `recusada`, `expirada`; transições por
  `UPDATE … WHERE estado = …` (atômico). **Só quem pediu aprova**; "não existe" e "não é seu"
  respondem igual. Na aprovação, os argumentos são re-validados pelo mesmo schema. Varredor
  (goroutine periódica na API) expira pendentes e marca `falhou` o que ficou em `aprovada` > 10 min.
  > **Atualizado na execução da Fatia 4 (2026-11-03, plano F4-2..F4-8).** Estados do hub: `pendente → aprovada → executada`;
  > **recusa de negócio na execução volta a `pendente`** com o `erro` e prazo `GREATEST(expira_em, agora + 1 h)`; **só exceção/`panic`
  > vira `falhou`** (final: algo pode ter sido gravado); `recusada` e `expirada` finais. `tentativa` conta as aprovações e só a vigente
  > fecha o pedido. **O mesmo pedido aberto não duplica** (`args_hash` + índice único parcial; pedir de novo devolve o mesmo com
  > `ja_existia: true`). Chaves em `snake_case`: `{executou:false, nivel:"critico", motivo, preview, pendente_id, expira_em,
  > aprovar_em, ja_existia, aviso, links}`. Na aprovação o executor revalida os args pelo mesmo schema, **reroda o `Preview` e compara
  > o resumo com o que a pessoa viu** (`preview_desatualizado` recusa e troca o resumo gravado pelo novo), executa e devolve/grava as
  > `ressalvas` da execução; falha DEPOIS de gravar que a pessoa precisa entender vira `falhou` com o texto da ação. O varredor (1 min,
  > advisory lock) também poda `mcp_confirmacoes_usadas`.
- Edição que substitui o registro inteiro: o `Executar` relê e faz merge.
- Invalida os mesmos caches e dispara os mesmos gatilhos (recategorização, reload de índice,
  reconciler) que a rota HTTP.

## 6. Catálogo de tools

Nomes `snake_case` em português (contrato §3.2). Descrição: o que devolve, quando usar, a
armadilha, "nunca invente ids, pergunte". Escritas terminam com `Nível: <nível>.`

### Fatia 1 — Leitura (todas `readOnlyHint: true`)

| Família | Tool | Fonte (rota/serviço atual) |
|---|---|---|
| Clientes | `listar_clientes`, `obter_cliente` (+`hub_id`, ativo/inativo, carteira de usuários) | `/clients` |
| | `pmm_target_cliente` | `/clients/{id}/target-pmm` |
| Campanhas | `listar_campanhas` (filtros estado/cliente/período/busca; **cancelada fora por padrão**, `incluir_canceladas`) | `/campaigns` |
| | `obter_campanha` (id **ou** `codigo` do hub) | `/campaigns/{id}`, `/hub-codes/{code}` |
| | `financeiro_campanhas` | `/campaigns/financials` |
| | `grade_campanha` (emissora × material × dia, categorias) | `/campaigns/{id}/daily-summary` |
| | `plano_do_dia` (faixas, progresso, saldo de uma célula) | serviço do `DayDetailModal` |
| | `listar_materiais_campanha`, `listar_regras_distribuicao`, `listar_overrides`, `listar_precos_campanha` | rotas `/campaigns/{id}/…` |
| Materiais | `listar_materiais` (por cliente), `obter_material` (duração, tipo, roteiro, status fingerprint, similaridade, campanhas), `listar_tipos_material` | `/clients/{id}/materials`, `/materials/{id}`, `/material-types` |
| Emissoras | `buscar_emissoras` (tokens AND, campo OR), `obter_emissora` (classe Anatel, contorno, transbordo, cidades no alcance, campanhas ativas) | `/stations`, `/stations/{id}`, coverage |
| Veiculações | `listar_veiculacoes` (filtros campanha/emissora/material/categoria/período; filtro canônico aprovado) | `/detections` |
| | `obter_veiculacao` (categoria, campanhas atribuídas, cobertura, link para ouvir) | `/detections/{id}` |
| | `veiculacoes_por_material` | `/detections/aggregate-by-material` |
| Usuários | `listar_usuarios`, `obter_usuario` (papel, clientes vinculados, ativo, boas-vindas pendente) | `/admin/users` |
| Integrações | `obter_webhook_cliente`, `listar_entregas_webhook`, `listar_chaves_api` (só metadados) | `/clients/{id}/webhook*`, api-keys |
| Notificações | `minhas_notificacoes` | `/admin/notifications` |

### Fatia 2 — Relatórios (leitura + deep-link)

| Tool | Fonte | Link |
|---|---|---|
| `insights_campanha` (KPIs, impactos, CPM, investido/bonificação, séries) | `/insights` | `/insights?…` |
| `visao_gerencial` (KPIs cross-campanha) | `/management-overview` | `/management` |
| `relatorio_data_hora` (lista cronológica, várias campanhas de 1 cliente) | serviço do airtime | `/reports/airtime?…` |
| `resumo_relatorio_campanha` + `exportar_consolidado` (link do CSV) | `/reports/campaigns/{id}/summary`, `consolidated.csv` | tela de campanha |
| `exportar_veiculacoes` (link do CSV) | `/detections/export` | `/detections?…` |
| `mapa_cobertura_campanha` (emissoras, raios, cidades) | `/live-map/coverage` | `/live-map?…` |
| `falhas_emissoras`, `falhas_campanhas`, `obter_falhas_campanha` (déficit "não tocou" × "fora do horário"), `falhas_por_dia`, `resumo_diario_falhas` | `/admin/station-failures`, `/admin/campaign-failures*`, `/admin/failures-daily`, digest | `/admin/station-failures?…` |
| `assertividade` (sempre com volume; janela = último mês fechado) | `/assertiveness` | `/management` |
| `listar_pos_vendas`, `obter_pos_venda` (payload congelado), `destinatarios_pos_venda` | `/post-sale/*` | `/admin/pos-venda/:id` |
| `auditoria_mcp` (cursor, `total: null`) | `audit_log` | — |

### Fatia 3 — Escrita `livre` e `confirma`

> **Escaladas finais (2026-10-29, execução da Fatia 3; plano F3-4 com as notas datadas).** A coluna da direita é o que ficou
> implementado; "hoje" é sempre hoje em America/Sao_Paulo. "Veiculação" é a aprovada (`ApprovedDetectionsFilter`) no mesmo
> recorte que o gatilho recategoriza (`catalog.HaVeiculacao`). Até a Fatia 4, `critico` é **barrado** (resposta com motivo e
> link da tela, sem escrever); ver `docs/features/mcp.md` "Escrita (Fatia 3)".

| Nível | Tool | Escala para `critico` quando |
|---|---|---|
| `livre` | `marcar_notificacoes_lidas`, `reconhecer_similaridade_material` | — |
| `confirma` | `criar_campanha` (nasce `programada`) | `inicio` ≤ hoje (o agendador a ativa em ≤ 60 s: é um "iniciar campanha") |
| `confirma` | `editar_campanha` | as datas mudam o estado que o agendador dá (`programada`/`ativa`/`concluida`), **ou** muda o conjunto de dias passados do período, **ou** muda o código do hub (é a ponte) |
| `confirma` | `editar_emissoras_campanha` | **nunca** — decisão do dono (2026-10-29): `TestEditarEmissorasCampanha_ProvaDaRemocao` provou que os números passados não mudam (muda só `target_stations`, PMM no target, mapa ao vivo, alertas e workers). Era: "remove emissora com tocada no período" |
| `confirma` | `vincular_material`, `desvincular_material`, `editar_emissoras_material` | **nunca** — não há recat e a atribuição é decidida na entrada da tocada (`TestVinculos_ProvaF34_PassadoNaoMuda`). Ressalvas no preview: competição de atribuição e webhook pendente. Era: "atinge tocada já veiculada" |
| `confirma` | `editar_material` (título, tipo, roteiro) | o tipo muda e o material tem veiculação aprovada (a troca recategoriza todas as campanhas dele) |
| `confirma` | `criar_regra_distribuicao`, `editar_regra_distribuicao`, `apagar_regra_distribuicao` | a campanha tem veiculação aprovada do(s) tipo(s) que o recat varre (criar/editar: o novo e o antigo; apagar: todos) **ou** a vigência (antiga/nova ∩ campanha) contém dia passado coberto pela máscara de dias da semana da versão. Editar só o nome escala igual (o recat reroda) |
| `confirma` | `definir_override`, `apagar_override` | `for_date` < hoje **ou** a célula-dia tem veiculação aprovada |
| `confirma` | `criar_cliente`, `editar_cliente` | — (o MCP não grava `hub_id`; a ponte é Fatia 4/5) |
| `confirma` | `editar_pmm_target` | o cliente tem veiculação aprovada nas emissoras do lote **ou** campanha não cancelada com alguma delas e início < hoje (o PMM é retroativo, sem versionamento) |
| `confirma` | `editar_emissora` (cadastro: nome, dial, cidade, UF, PMM — nunca stream_url) | o `pmm` muda e a emissora tem veiculação aprovada (`stations.pmm` entra nos impactos de todas as campanhas que a usam). Era: "—" |
| `confirma` | `criar_tipo_material`, `editar_tipo_material` | — |

### Fatia 4 — Escrita `critico` (+ `/pendentes`)

> **Catálogo final (2026-11-03, F4-1): 18 escritas `critico` + `minhas_pendentes` = 83 tools.** Saíram da tabela abaixo
> `iniciar_campanha` (a rota reativa cancelada/concluída sem pré-condição e o agendador a rebaixa em ≤ 60 s: o caminho para começar
> antes é `editar_campanha`), `pausar_campanha` (a rota é 410 desde 2026-05-07; "pausar" é cancelar) e `publicar_pos_venda` (exige as
> capturas PNG que só o navegador gera). Entrou `minhas_pendentes`. `testar_webhook` ficou, crítica, e recusa quando o teste seria
> descartado. `criar_usuario` não recebe senha (o servidor gera e a boas-vindas a entrega), `editar_webhook_cliente` não tem
> campo de segredo, `criar_veiculacao_manual` não recebe comprovante (o PDF só entra pela tela). Detalhe: `docs/features/mcp.md`.

| Tool | Por que é crítica |
|---|---|
| `iniciar_campanha`, `pausar_campanha` | sobe/derruba workers; cliente passa a ver |
| `cancelar_campanha` | congela déficit; sai das telas operacionais |
| `editar_precos_campanha`, `editar_cpm_fixo` | dinheiro |
| `criar_veiculacao_manual` (sem comprovante; comprovante via deep-link) | entra na cobrança |
| `ignorar_veiculacao`, `restaurar_veiculacao` | muda categoria das outras do mesmo dia |
| `publicar_pos_venda`, `reenviar_pos_venda`, `revogar_destinatario_pos_venda` | email para fora; documento congelado |
| `criar_usuario` (com boas-vindas), `editar_usuario` (papel/carteira), `desativar_usuario` | email para fora; acesso de cliente |
| `desativar_cliente`, `ativar_cliente` | login do cliente |
| `apagar_campanha`, `apagar_material`, `apagar_cliente` | irreversível |
| `editar_webhook_cliente`, `testar_webhook` | dado sai para sistema externo |

## 7. Fatias de entrega

| Fatia | Entrega | Pronto quando |
|---|---|---|
| **0 — Fundação** | go-sdk v1.8.0 (já suporta a spec 2026-07-28, `Stateless`, `JSONResponse`; pede go 1.25); migrations (tokens, oauth_clients, oauth_codes — `mcp_confirmacoes_usadas` nasce na Fatia 3 e `mcp_acoes_pendentes` na 4); `/mcp` com auth + rate limit + 1 tool `ping`; OAuth completo (público + confidencial); `/autorizar-ia`; tela de tokens; `revoga-mcp`; auditoria; instruções | os 3 clientes (claude.ai, Claude Code, Gemini Enterprise) conectam e chamam `ping` em **dev e prod**; suíte OAuth (vetor PKCE RFC 7636, open redirect, reuso de code, refresh como access, rotação, confidencial com secret errado) verde |
| **1 — Leitura** | ~30 tools de §6 Fatia 1 + teste de inventário | cada tool tem teste de paridade "tool × handler HTTP devolvem o mesmo número" contra o banco de dev (restore de prod) |
| **2 — Relatórios** | tools de §6 Fatia 2; telas lendo filtro da URL; avaliação | 10 perguntas de avaliação (padrão mcp-builder: só leitura, várias chamadas, resposta única verificável, estável) com ≥ 9/10 |
| **3 — Escrita livre/confirma** | motor de níveis, token de confirmação, auditoria, tools de §6 Fatia 3 | teste do motor (token errado, args mudados, reuso, vencido) + paridade de efeito tool × rota HTTP |
| **4 — Escrita crítica** | `mcp_acoes_pendentes`, `/pendentes`, varredor, tools de §6 Fatia 4 | aprovação ponta-a-ponta em dev; "só quem pediu aprova"; pendente presa vira `falhou` |
| **5 — Ecossistema e docs** | hub: linha "Pontes", `SEM_MIDIA_NO_HUB`, §1 do contrato; `docs/features/mcp.md`, `docs/operations/mcp.md`, ADRs; regra "o MCP faz parte do pronto" no `CLAUDE.md` | doc com header YAML, índice `docs/README.md` atualizado |

Cada fatia vira um plano próprio (`docs/superpowers/plans/`). Ordem fixa: 0 → 1 → 2 → 3 → 4 → 5
(a 5 pode andar junto com a 2).

## 8. Deploy e riscos de produção

- **Dois caminhos** (memória `deploy-split-frontend-backend-ordering`): backend via `deploy.sh`,
  frontend sobe sozinho no CF Pages a cada push em `master`. Cada fatia com tela nova: push do
  backend → `deploy.sh` → smoke test (`/mcp` sem bearer devolve 401 com `WWW-Authenticate`, não
  404) → push do frontend.
- Migrations são só `CREATE TABLE` + índices; ainda assim passam pelo `shadow_migration_test`.
- Antes de cada push: `CGO_ENABLED=0 GOOS=linux go build ./...` (regra 6.1); o go-sdk muda
  `go.mod` (regra 6.2: diretiva `go` ≤ 1.26).
- Boot da API (regra 6.5): métricas Prometheus novas com nome único; nada de panic no registro de
  tools (o teste de inventário roda o registro).
- O Cloudflare Tunnel precisa expor `/mcp`, `/mcp/oauth/*` e `/.well-known/*` no host público da
  API; o WAF não pode bloquear o egress do claude.ai/Google.
- Variáveis novas: `MCP_ORIGEM_PUBLICA`, `NOTIFICATIONS_BASE_URL` (origem do portal, para deep-links e `aprovarEm`; é compartilhada com os links dos emails, e uma variável dedicada é um possível follow-up).

## 9. Testes

- Unidade: hash/prefixo de token, HMAC de confirmação, canonicalização de args, `EscalarPara` por
  tool, PKCE (vetor do RFC 7636), validação de `redirect_uri`.
- Integração (DB de teste `rc-t-pg`, um pacote por vez `-p 1`): fluxo OAuth inteiro; `/mcp` com
  token pessoal, OAuth, rebaixado, desativado, revogado, vencido; rate limit; motor de níveis;
  pendentes; auditoria gravada.
- Paridade: para toda tool que espelha uma tela, mesmo número que o handler HTTP no mesmo filtro.
- Inventário: lista exata de tools; toda escrita tem nível; descrição de escrita termina em `Nível:`.
- Avaliação mcp-builder (Fatia 2) rodada contra o banco de dev.
- Manual: MCP Inspector + os 3 clientes reais.

## 10. Em aberto (decidir no plano da fatia correspondente, não bloqueia a Fatia 0)

- Se o go-sdk não fizer stateless com a versão de spec dos clientes, o fallback JSON-RPC à mão
  exige testes de negociação de versão próprios (decisão no spike).
- `plano_do_dia` depende de extrair a lógica que hoje mora no handler do modal (verificar na Fatia 1).
- Webhook: `testar_webhook` dispara POST real para o cliente — confirmar na Fatia 4 se fica
  `critico` ou sai do catálogo. **Resolvido (Fatia 4):** fica, `critico`, e recusa o teste que a rota descartaria em silêncio.
