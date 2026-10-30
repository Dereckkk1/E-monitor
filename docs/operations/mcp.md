---
status: implementado
ultima-verificacao: 2026-10-29
codigo-relacionado:
  - workers/internal/mcp/modulo.go
  - workers/internal/mcp/autenticacao.go
  - workers/internal/mcp/oauth/servidor.go
  - workers/cmd/revoga-mcp/main.go
  - workers/cmd/registra-cliente-mcp/main.go
  - infra/docker/docker-compose.yml
  - infra/docker/Dockerfiles/workers.Dockerfile
  - workers/internal/mcp/avaliacao/semear.go
  - workers/internal/mcp/avaliacao/cli/main.go
  - workers/internal/mcp/avaliacao/perguntas.xml
  - workers/internal/mcp/avaliacao_test.go
  - workers/internal/mcp/confirmacao.go
  - workers/internal/mcp/escrita.go
  - workers/internal/acoes/servico.go
  - migrations/0071_mcp_confirmacoes.up.sql
---

# MCP do E-monitor — operação

Endereço: `https://api.e-monitor.online/mcp`. Só **administrador ativo** conecta. Como o sistema
funciona (credenciais, OAuth, limites, auditoria): [features/mcp.md](../features/mcp.md).

## Variáveis (serviço `api` no `docker-compose.yml`)

| Variável | Padrão | Para quê |
|---|---|---|
| `JWT_SECRET` | (obrigatório, >= 32) | salga os hashes das credenciais; **girar invalida tudo** (ver Armadilhas) |
| `MCP_ORIGEM_PUBLICA` | `https://api.e-monitor.online` | issuer OAuth e `resource`; barra final é removida |
| `MCP_REDIRECT_URIS` | claude.ai e claude.com | callbacks https **exatos** aceitos no registro aberto (vírgula); loopback http vale sempre |
| `NOTIFICATIONS_BASE_URL` | `https://e-monitor.online` | origem do portal onde fica `/autorizar-ia` |

Mudou variável: `build` não basta, recrie a API (regras 4.1/4.2 do `CLAUDE.md`). Todo comando na VM
usa os dois arquivos de compose (regra 4.7); defina uma vez e use `$DC` nos exemplos deste doc:

```bash
DC="docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env"
$DC up -d --force-recreate --no-deps api
```

## Conectar

### claude.ai (web e celular)

Configurações -> Conectores -> Adicionar conector personalizado -> URL acima -> Conectar. O
navegador abre `e-monitor.online/autorizar-ia`; faça login se pedir e clique **Autorizar**.

### Claude Code

OAuth: `claude mcp add --transport http emonitor https://api.e-monitor.online/mcp` e siga o login.
Ou token pessoal (Minha conta -> Integração com IA -> Gerar token; aparece uma vez):

```bash
claude mcp add --transport http emonitor https://api.e-monitor.online/mcp \
  --header "Authorization: Bearer emonitor_mcp_..."
```

Teste: peça "chame a tool quem_sou_eu do emonitor".

### Gemini Enterprise

O Gemini não faz registro dinâmico e o registro aberto **recusa cliente confidencial**. Quem cadastra
é o admin, **na VM**, com o CLI `registra-cliente-mcp` (binário da imagem: depois de um deploy, só
vale após o recreate da `api`, regra 4.2). Ele lê o `JWT_SECRET` do ambiente do container (o mesmo da
API) e `--dsn` assume `DATABASE_URL` do container se omitido (os dois CLIs fazem isso: não passe `--dsn`).

```bash
$DC exec api registra-cliente-mcp \
  --nome "Gemini Enterprise" \
  --redirect https://vertexaisearch.cloud.google.com/oauth-redirect
```

`--redirect` pode repetir ou ir separada por vírgula. Saída: **uma linha JSON** com `client_id`
(`emc_...`), `client_secret` (`emonitor_mcs_...`), `nome`, `redirect_uris` e `aviso`. **O segredo só
aparece nessa execução** — guarde no cofre de senhas; perdeu, cadastre outro cliente. O cadastro grava
`mcp.oauth.cliente_confidencial_criado` no `audit_log`.

Códigos de saída: `0` ok; `2` uso errado (falta `--nome`/`--redirect`/DSN, `JWT_SECRET` curto ou
`redirect` inválido); `1` erro de banco.

No "Custom MCP Server" do Gemini:

| Campo | Valor |
|---|---|
| URL do servidor MCP | `https://api.e-monitor.online/mcp` |
| Authorization URL | `https://api.e-monitor.online/mcp/oauth/authorize` |
| Token URL | `https://api.e-monitor.online/mcp/oauth/token` |
| Client ID / Client secret | os da saída do CLI |
| Escopo | `emonitor:all` |

Cliente confidencial que **não chegar ao `/token` em 7 dias é apagado** (a faxina roda a cada novo
registro público); refaça o cadastro e a configuração no console. Depois do primeiro token ele fica.

## Login na aba de consentimento

A aba que o claude.ai, o Claude Code ou o Gemini abre para o consentimento **sempre começa deslogada**:
o JWT do portal fica em `sessionStorage`, que é por aba. Então toda conexão pede o login do E-monitor
em `/login`, que volta para `/autorizar-ia?pedido=...` depois de entrar. Quem costuma entrar pelo SSO
do hub (`/sso`) perde o `?pedido=` (a `HubSsoPage` sempre vai para `/`): nessa aba, entre com
**e-mail e senha**, ou faça login e **reinicie a conexão** no assistente. O pedido vale 10 min.

## Revogar

- **Uma conexão:** Minha conta -> Integração com IA -> Desconectar (numa conexão OAuth derruba
  todos os tokens daquele assistente para a pessoa; o assistente perde o acesso na hora).
- **Automático:** trocar/redefinir senha, desativar, tirar de admin ou apagar o usuário revoga tudo
  (trigger em `users`). Reativar **não** ressuscita.
- **Em massa**, na VM (regra 4.2: recreate antes, senão roda o binário velho):

```bash
DC="docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env"
$DC exec api revoga-mcp --listar
$DC exec api revoga-mcp --email pessoa@emidiastec.com.br            # dry-run
$DC exec api revoga-mcp --email pessoa@emidiastec.com.br --aplicar
$DC exec api revoga-mcp --todos                                     # dry-run
$DC exec api revoga-mcp --todos --aplicar
```

| Flag | Efeito |
|---|---|
| `--listar` | lista as credenciais vivas: cada token pessoal e **cada refresh token vivo** do OAuth (uma linha por refresh); `/account`, em contraste, mostra **uma linha por cliente OAuth**. Só lê, não combina com `--aplicar` |
| `--email X` | todas as credenciais dessa pessoa |
| `--todos` | de todo mundo; **também apaga todos os códigos OAuth pendentes** |
| `--aplicar` | sem ele é dry-run |

Use exatamente um de `--listar`/`--email`/`--todos`. A saída é **uma linha JSON**
(`aplicado`, `pessoas`, `credenciais`, `codigos_apagados`) e nunca contém token nem hash. O dry-run
conta **exatamente o que o `--aplicar` revoga**, incluindo os access tokens OAuth; por isso
`credenciais` pode ser **maior** que as linhas do `--listar`. Uma linha de auditoria por pessoa
(`mcp.token.revogado`, `actor_type 'cli'`, `metadata.pelo = "revoga-mcp"`). Se a auditoria de
uma pessoa falhar depois da revogação, o lote continua: aviso no stderr, o email entra em
`auditoria_falhou` no resumo e o exit é `1` no fim. Saída: `0` ok (também
`--email` sem ninguém, com aviso no stderr); `2` uso errado; `1` erro de banco ou auditoria que falhou.

## Auditoria (consulta)

```sql
SELECT created_at, action, actor_type, metadata
  FROM audit_log
 WHERE metadata->>'origem' = 'mcp'
 ORDER BY created_at DESC LIMIT 50;
```

Eventos: `mcp.token.criado`, `mcp.token.revogado`, `mcp.token.recusado` (token existente cujo dono
já não é admin ativo; <= 1 por token a cada 10 min), `mcp.oauth.autorizado`,
`mcp.oauth.cliente_confidencial_criado`. Ações dos CLIs têm `actor_id` nulo e `actor_type 'cli'`.

**Escritas (Fatias 3 e 4):** `mcp.escrita.<tool>` (executou; para um pedido crítico aprovado, `nivel` é `critico` e
`meta.mcp.pendente_id` aponta o pedido), `mcp.escrita.falha` (erro interno ou `panic` no `Executar` de uma tool `livre` ou
`confirma`: **a escrita pode ter sido feita pela metade e o token fica gasto** — investigue) e, para o nível `critico`, as
linhas `mcp.pendente.criada`, `.aprovada`, `.recusada`, `.executada` e `.falhou` (recurso `mcp_pendente`; o id do pedido em
`meta.mcp.pendente_id`). `.falhou` traz `codigo`: `conferencia_falhou` (a conferência antes de executar caiu; nada foi feito e o
pedido voltou a `pendente`), `interno` (a execução caiu no meio: o pedido fica `falhou`, **confira se a ação aconteceu**) ou
`presa` (feito pelo varredor). O ator é o admin dono do token (`actor_id`, `actor_type 'user'`). Quem escreveu e
com qual credencial:

```sql
SELECT created_at, action, actor_id, resource, resource_id,
       metadata->>'tool' AS tool, metadata->>'nivel' AS nivel,
       metadata->'mcp'->>'token_id' AS token_id, metadata->'args' AS args, metadata->>'motivo' AS motivo
  FROM audit_log
 WHERE action LIKE 'mcp.escrita.%'
 ORDER BY created_at DESC LIMIT 50;
```

A tool `auditoria_mcp` **omite** o `token_id` (o leitor oculta toda chave que case `token`): para ligar uma escrita ao token,
use este SQL. Os `args` saem mascarados (chave que parece segredo vira `"[omitido]"`) e com texto cortado em 500 caracteres, mas
**dados de contato e CNPJ de cliente aparecem** nos `args` de `criar_cliente`/`editar_cliente`.

### Pedidos críticos e varredor (`mcp_acoes_pendentes`)

Escrita `critico` nunca executa pela tool: grava um pedido (24 h) e devolve `aprovar_em` (`/pendentes?id=…`). Só um admin
ativo, dono do pedido, aprova ou recusa no portal; a aprovação revalida os argumentos gravados, refaz o preview e executa.
Estados: `pendente`, `aprovada` (executando), `executada`, `recusada`, `expirada`, `falhou`. Recusa de negócio ou conferência
que cai por erro inesperado devolvem o pedido a `pendente` (nada foi feito); queda no meio da execução o deixa `falhou`.
O varredor (a cada minuto, uma réplica por vez via advisory lock) expira o `pendente` vencido, fecha como `falhou` a `aprovada`
presa há mais de 10 min e poda `mcp_confirmacoes_usadas`.

### Uso único das confirmações (`mcp_confirmacoes_usadas`)

Guarda o SHA-256 dos tokens `confirma` já usados até a expiração (10 min). Cada confirmação nova poda, de forma oportunista,
até 500 linhas vencidas há mais de 1 h (`FOR UPDATE SKIP LOCKED`; erro da poda é só logado). O varredor
dos pedidos críticos (abaixo) também a poda a cada minuto; ela é minúscula (uma linha de 32 bytes por escrita). Para conferir ou
limpar à mão (seguro: linha vencida há mais de 1 h nunca volta a valer, o token já expirou):

```sql
SELECT count(*), min(expira_em), max(expira_em) FROM mcp_confirmacoes_usadas;
DELETE FROM mcp_confirmacoes_usadas WHERE expira_em < now() - interval '1 hour';
```

Nunca apague linha **não vencida**: reabriria o uso do token.

## Diagnóstico

| Sintoma | Causa provável |
|---|---|
| 401 com `error="invalid_token"` | token revogado/vencido, ou o dono deixou de ser admin ativo (procure `mcp.token.recusado`) |
| 401 sem `error=` | cliente não mandou bearer — normal no primeiro passo do OAuth |
| 405 em `GET /mcp` | esperado: o MCP é sem sessão, só POST |
| 403 `invalid Host header` | alguém removeu `DisableLocalhostProtection` |
| 404 em `/mcp` | `JWT_SECRET` < 32 bytes: o módulo não montou (procure `mcp: JWT_SECRET` no log da API) |
| 500 `MCP sem autenticação montada` | rota do `/mcp` montada sem o middleware de autenticação (regressão no router) |
| 429 | 240/min por credencial, 60 falhas/15 min no IP, ou 20 registros/h no IP |
| `invalid_redirect_uri` no registro | o assistente usa um callback fora de `MCP_REDIRECT_URIS`: inclua o callback **exato** e recrie a API |
| `invalid_client_metadata` ("registra-cliente-mcp na VM") | tentaram registrar cliente confidencial pelo registro aberto: use o CLI |
| Gemini emite código mas nunca chama `/token` | COOP no `/authorize` (deve ser `unsafe-none`); ou `client_secret` errado (`invalid_client`) |
| `/autorizar-ia` diz "pedido vencido" | passaram mais de 10 min entre conectar e autorizar |
| Depois do login SSO do hub a aba cai em `/` e não pede autorização | o SSO descarta o `?pedido=`: entre com e-mail e senha na aba de consentimento, ou reinicie a conexão (ver "Login na aba de consentimento") |
| Tudo parou de autenticar depois de girar o `JWT_SECRET` | esperado: os hashes mudam; todos reconectam e o Gemini precisa de cliente novo |
| Em dev, o consentimento abre o frontend de prod | falta `NOTIFICATIONS_BASE_URL=http://localhost:3000` no `.env` |
| Tool de escrita devolve `executou: false` e `nivel: critico` | a escalada deu crítico (o `motivo` diz qual condição) e a tool gravou um pedido: nada foi feito até uma pessoa aprovar em `/pendentes` (link `aprovar_em`). Procure `mcp.pendente.criada` no `audit_log` e use `minhas_pendentes` para o estado |
| Tool de escrita devolve `executou: false` e "Confirmação inválida ou expirada" | token vencido (10 min), de outro usuário/tool, ou **os argumentos mudaram** entre o preview e a confirmação: peça o preview de novo. "Esta confirmação já foi usada" = reuso do mesmo token |
| Tool de escrita devolve `[interno]` | erro interno ou `panic` (o texto Go só vai para o log da API; procure `mcp: ` e o nome da tool). O token **não** volta a valer; confira `mcp.escrita.falha` no `audit_log` |
| `[campanha_cancelada]` | a tool recusa escrita em campanha cancelada (a tela não recusa) |

Dev local: no `.env`, `MCP_ORIGEM_PUBLICA=http://localhost:8080` **e**
`NOTIFICATIONS_BASE_URL=http://localhost:3000`. A API local leva minutos para escutar (carrega o
índice antes); health 000 logo após o recreate não é crash.

## Testar as tools localmente

- Um banco por agente no container `rc-mcp-test-pg` (porta 55440). O template atual é o
  `radiocheck_mcp_test_d4` (migrations até a **0072**, `mcp_acoes_pendentes` incluída; o `d3` parou na 0071):
  `docker exec rc-mcp-test-pg psql -U radiocheck -d postgres -c "CREATE DATABASE radiocheck_mcp_test_<x> TEMPLATE radiocheck_mcp_test_d4;"`.
  Para gerar o template seguinte (`dN` = cópia do anterior + a migration nova; rede Docker `rc-mcp-test`;
  `MSYS_NO_PATHCONV=1` no Git Bash do Windows), crie `dN` com o `CREATE DATABASE` acima e rode
  `MSYS_NO_PATHCONV=1 docker run --rm --network rc-mcp-test -v "$(pwd)/migrations:/migrations:ro" migrate/migrate:v4.17.1 -path=/migrations -database "postgres://radiocheck:radiocheck@rc-mcp-test-pg:5432/radiocheck_mcp_test_dN?sslmode=disable" up`.
- **Nunca aponte `TEST_DATABASE_URL` para o template** (`d3`): os testes inserem usuários e linhas nele. Crie um banco
  por pacote a partir do template e descarte-o no fim (`DROP DATABASE`).
- `export TEST_DATABASE_URL=postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_test_<x>?sslmode=disable`
  e `go test -p 1 -timeout 60m ./internal/mcp/...`. O pacote `./internal/mcp` leva **10–15 min** (rode em segundo plano).
  Rode `internal/mcp` ANTES de `internal/catalog` e **recrie o banco do template entre os pacotes**: a suíte do catalog
  deixa clientes no banco e o guard do `dbtest` passa a derrubar testes do MCP. Sem `TEST_DATABASE_URL` exportada os
  testes de banco PULAM e parecem verdes.
- Os testes de tool usam `mcptest.DBCatalogo` (trunca usuários, clientes e emissoras) e as fixtures de
  `internal/mcp/mcptest/catalogo.go`. Cada tool que espelha tela tem um teste `..._ParidadeComATela` que chama
  o handler HTTP com claims de admin e compara os números.
- As tools de escrita têm, além disso: teste de **paridade de efeito** tool × rota HTTP (`chamarHandlerCorpo`: o mesmo
  estado semeado em duas fixtures, uma pela rota e outra pela tool, comparando as linhas gravadas e as chamadas aos falsos),
  o protocolo preview → confirmar (`confirmarEExecutar`, na bancada `escrita_ferramentas_test.go`) e os falsos
  `mcptest.SupervisorFalso` / `HubFalso` (registram `UpdateStations`, `Reload` e os eventos ao hub). O inventário das 20
  escritas (`escrita_inventario_test.go`) roda sem banco.
- Paridade contra dado real: no deploy (abaixo), depois do `deploy.sh`, siga o roteiro "Conferência com dado
  real". Divergência = bug: não siga para a fatia seguinte.
- **O template envelhece.** Os testes e a semente da avaliação usam datas relativas a hoje, e `detections`,
  `detection_campaigns` e `stream_health_events` são particionadas por mês: a migration 0048 cria as partições
  de 18 meses à frente **na hora do `migrate`** (`ensure_month_partitions(18)`), e no banco de teste ninguém roda a
  manutenção. Um template nunca re-migrado começa a falhar quando o mês corrente passa do horizonte (insert sem
  partição). Sintoma: `no partition of relation "detections" found for row`. Conserto: rode
  `SELECT ensure_month_partitions(18);` no template (ou recrie-o com `migrate up`) e crie os bancos de novo.

## Avaliação (mcp-builder)

Prova de que um modelo responde perguntas reais só com as tools (spec §7: ≥ 9/10). Sem chave da API da
Anthropic nesta máquina, a rodada é: uma semente determinística, um teste que prova o gabarito chamando as tools
e um avaliador às cegas que só usa um CLI de desenvolvimento.

- **Arquivos:** `workers/internal/mcp/avaliacao/semear.go` (o cenário: clientes Aurora Alimentos e Brisa
  Seguros, 3 emissoras, as campanhas "Aurora Setembro", "Aurora Agora" e "Brisa Outubro", queda, assertividade,
  pós-venda enviado, dois eventos de auditoria), `perguntas.xml` (as 10 perguntas, com as contas do gabarito num
  comentário — **não entregue o arquivo ao avaliador**), `cli/main.go` (o CLI) e
  `workers/internal/mcp/avaliacao_test.go` (`TestAvaliacao_Gabarito`, que acha os ids pelos nomes e prova cada
  resposta; roda no `go test ./internal/mcp/...` normal, sem tag).
- **As datas da campanha ativa são relativas a hoje** (America/Sao_Paulo): as tools de falha e a assertividade
  têm janela móvel. Semente de outro dia ainda costuma bater; de outro mês, a assertividade muda. O CLI avisa no
  stderr (`AVISO: a semente é de …`). Semeie no dia da rodada.
- **A semente só roda em banco vazio** (sem cliente, emissora, campanha nem usuário; `ErrBancoNaoVazio`) e numa
  transação só. O CLI lê a variável própria `AVALIACAO_DATABASE_URL` — **nunca** `DATABASE_URL`, que é o nome da
  de prod — e só compila com a tag `avaliacao` (o binário de prod não inclui nem o CLI nem `NovoServidorLocal`;
  o CLI também não está no `workers.Dockerfile`, de propósito).

Preparar (na raiz `workers/`):

```bash
docker exec rc-mcp-test-pg psql -U radiocheck -d postgres \
  -c "DROP DATABASE IF EXISTS radiocheck_mcp_avaliacao;" \
  -c "CREATE DATABASE radiocheck_mcp_avaliacao TEMPLATE radiocheck_mcp_test_d4;"
# o semear recusa banco com usuário/cliente/emissora/campanha; se algum teste já rodou contra o
# template, ele traz usuários de sobra — limpar NESSE banco é inofensivo quando ele já vem vazio
docker exec rc-mcp-test-pg psql -U radiocheck -d radiocheck_mcp_avaliacao -c "TRUNCATE users CASCADE;"
export AVALIACAO_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:55440/radiocheck_mcp_avaliacao?sslmode=disable"
go run -tags avaliacao ./internal/mcp/avaliacao/cli semear
go run -tags avaliacao ./internal/mcp/avaliacao/cli tools | head -5     # começa por "## assertividade"
go run -tags avaliacao ./internal/mcp/avaliacao/cli chamar listar_clientes '{"busca":"aurora"}'
```

`tools` lista as 64 tools (nome, descrição, schema de entrada) e termina com as instruções do servidor;
`chamar <tool> '<json>'` chama a tool no servidor MCP real, em memória, com o admin da semente como principal
(erro sai como `ERRO: …`). Rodar `semear` duas vezes recusa (banco não vazio).

**Despachar o avaliador:** um subagente novo, sem o gabarito e sem acesso declarado ao código, recebe os dois
comandos (`tools` e `chamar`, com `AVALIACAO_DATABASE_URL` e `-tags avaliacao`), a ordem de não rodar `semear`
nem ler arquivos do repositório, e as 10 `<question>` do XML numeradas; devolve `N. <resposta>` por linha e um
parágrafo por pergunta com as tools que chamou e os tropeços. Nota = respostas iguais ao `<answer>` (após trim;
`95` = `95.0`). Abaixo de 9/10, o defeito costuma ser descrição de tool ou nome de campo: conserte a tool (com
teste), rode o gabarito de novo e repita com um avaliador NOVO.

**Última rodada — 2026-10-28: 10/10.** As 10 respostas iguais ao gabarito, sem erro de tool e sem id inventado.
Tropeços relatados pelo avaliador (nenhum custou ponto):

- `assertividade` devolve o percentual como número (`95`), e a pergunta pedia uma casa decimal: o avaliador
  formatou `95.0` por conta própria;
- em `auditoria_mcp`, o nome do token só está dentro de `detalhes.nome` (não há campo de topo);
- as perguntas de assertividade e de auditoria se resolvem com 1–2 chamadas (as outras exigem achar ids pelos
  nomes e cruzar duas ou três tools).

## Deploy

Dois caminhos independentes: o frontend (CF Pages) sobe sozinho a cada push em `master`; o backend só
com `./scripts/deploy.sh` na VM. **Backend primeiro**, depois o frontend — senão `/account` chama
`/v1/internal/mcp/tokens` e leva 404. Smoke de prod:

```bash
curl -si -X POST https://api.e-monitor.online/mcp | grep -i -E "^HTTP|www-authenticate"
curl -s https://api.e-monitor.online/.well-known/oauth-authorization-server
```

Esperado: `401` com `WWW-Authenticate: Bearer realm="emonitor-mcp", resource_metadata=..., scope="emonitor:all"`
(não 404, não 403). Confira também o anti-frame do consentimento: `curl -sI "https://e-monitor.online/autorizar-ia?pedido=x" | grep -i frame` deve mostrar `X-Frame-Options: DENY` e `frame-ancestors 'none'`. A regra de `frontend/public/_headers` depende de o Cloudflare Pages aplicá-la à resposta de fallback da SPA; se não aparecer, o clickjacking do botão Autorizar está aberto. O Cloudflare Tunnel precisa expor `/mcp`, `/mcp/oauth/*` e `/.well-known/*` no host
público da API, e o WAF não pode bloquear o egress do claude.ai/Google. Novo CLI em `cmd/*` exige as
duas linhas no `workers.Dockerfile` (regra 6.7) — `revoga-mcp` e `registra-cliente-mcp` já estão. O CLI da
avaliação (`internal/mcp/avaliacao/cli`) **não** entra na imagem, de propósito.

**Migrations antes do merge (Fatia 3):** o `master` já tem `0070_users_username`. Ao mergear o `feat/mcp-server`,
`0070_mcp_fundacao` vira **0071** e `0071_mcp_confirmacoes` vira **0072** (renomeie os quatro arquivos `.up/.down.sql` e as
referências nos docs). Ambas são só `CREATE TABLE` + índices (+ o trigger em `users` da primeira), mas passam pelo
`shadow_migration_test` do `deploy.sh` como qualquer outra (regra 4.8 do `CLAUDE.md`).

### Conferência com dado real (depois do `deploy.sh`)

Pelo Claude Code conectado ao MCP de prod, em **2 campanhas ativas** (de preferência uma por inserção e uma
consolidada):

1. Fatia 1: `financeiro_campanhas` × `/campaigns` e `grade_campanha` × `/detections`.
2. Fatia 2 — dois relatórios:
   - `insights_campanha` (cliente, campanha, mês corrente) × `/insights` com o mesmo filtro: impactos,
     impactos no target, veiculações por categoria, investido, bonificação e CPM têm de bater;
   - `obter_falhas_campanha` × o drawer da campanha em `/admin/station-failures` (aba Por campanha):
     déficit, "não tocou", "fora do horário" e os dias de falha por emissora.
3. Abra o `links.tela` de cada um: a tela tem de abrir já filtrada no mesmo recorte (é o frontend da
   Fatia 2; ele sobe sozinho no push para `master`, então confira depois que o CF Pages publicar).
4. **Fatia 3 — escritas, numa campanha de TESTE** (de um cliente de teste, `programada`, com início no futuro; **nunca**
   numa campanha de cliente real, e nunca com o índice de uma campanha ativa em jogo). Pelo Claude Code conectado:
   1. `criar_campanha` (início futuro, uma emissora) **sem token**: a resposta é o preview (`executou: false`, `confirmacao`).
      Confira em `/campaigns` que a campanha **não** existe. Repita com o token e os **mesmos** argumentos: `executou: true`;
      a campanha aparece em `/campaigns` como `programada`.
   2. **Reuso:** chame de novo com o mesmo token: `executou: false` ("já foi usada"), e nada duplica. **Argumentos mudados:** troque
      o nome e reenvie o token: "inválida ou expirada".
   3. `editar_campanha` (só o nome) → preview → confirmar; confira o nome na tela. Depois peça `inicio` = hoje: tem de voltar
      `nivel: critico`, com `links.tela`, e **nada** muda na campanha.
   4. `vincular_material` / `criar_regra_distribuicao` na campanha de teste, e confira na tela da campanha (etapas Materiais e
      Distribuição) que o resultado é o mesmo de fazer pela tela; a recategorização da regra leva até 30 s.
   5. **Paridade:** faça a mesma edição pela tela numa segunda campanha de teste idêntica e compare as linhas
      (`campaigns`, `distribution_rules`, `campaign_materials`).
   6. **Auditoria:** o SQL de "Auditoria (consulta)" tem de mostrar, para cada passo acima, uma linha `mcp.escrita.<tool>` com o
      `token_id` do seu token; para o passo do crítico, uma `mcp.pendente.criada` e, depois de você aprovar em `/pendentes`, as
   linhas `mcp.pendente.aprovada`, `mcp.escrita.<tool>` e `mcp.pendente.executada`. Nenhuma `mcp.escrita.falha`.
   7. No fim, apague a campanha de teste pela tela e confira `mcp_confirmacoes_usadas` (ver "Uso único das confirmações").

Divergência de número ou de efeito = bug: não siga adiante.

## Armadilhas

- **Girar o `JWT_SECRET` invalida todas as credenciais, `client_secret` e códigos do MCP.**
- Access tokens antigos **não** são revogados na rotação do refresh; `mcp_tokens` nunca é podada
  (follow-ups). Reuso de refresh só recebe `invalid_grant` (a detecção estilo hub é follow-up).
- O limite de falhas por IP troca 401 por 429, mas não reduz consultas ao banco (de propósito: uma
  credencial válida nunca é barrada por IP).
- Protocolos aceitos (go-sdk v1.8.0): `2024-11-05`, `2025-03-26`, `2025-06-18`, `2025-11-25`,
  `2026-07-28`. Lotes JSON-RPC são recusados com 400.
- Limitação aceita: uma renovação de token em voo no mesmo milissegundo da troca de senha pode
  sobreviver a ela.
- **Recomendações ao dono (não feitas):** (1) publicar a porta da API só em `127.0.0.1` no compose de
  prod — hoje um acesso direto à 8080 forja o `CF-Connecting-IP` e foge dos limites por IP;
  (2) o limiter de `/auth/login` é contornável atrás do Cloudflare porque `middleware.RealIP` confia
  em cabeçalhos forjáveis (problema anterior ao MCP); (3) as imagens `minio/minio` e `minio/mc` não
  podem mais ser baixadas do Docker Hub ("pull access denied"): máquina nova ou VM que perca o cache
  de imagens falha no `docker compose up`; (4) o compose base ainda monta `migrations/` em
  `docker-entrypoint-initdb.d` (regra 4.6 / F-111), o que quebra um banco local novo.
