---
status: implementado
ultima-verificacao: 2026-10-27
codigo-relacionado:
  - workers/internal/mcp/modulo.go
  - workers/internal/mcp/autenticacao.go
  - workers/internal/mcp/oauth/servidor.go
  - workers/cmd/revoga-mcp/main.go
  - workers/cmd/registra-cliente-mcp/main.go
  - infra/docker/docker-compose.yml
  - infra/docker/Dockerfiles/workers.Dockerfile
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

Mudou variável: `build` não basta, recrie a API com `up -d --force-recreate --no-deps api`
(regras 4.1/4.2 do `CLAUDE.md`).

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
API) e `--dsn` assume `DATABASE_URL` do container se omitido.

```bash
docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml \
  --env-file infra/docker/.env exec api registra-cliente-mcp --dsn "$DATABASE_URL" \
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

## Revogar

- **Uma conexão:** Minha conta -> Integração com IA -> Desconectar (numa conexão OAuth derruba
  todos os tokens daquele assistente para a pessoa; o assistente perde o acesso na hora).
- **Automático:** trocar/redefinir senha, desativar, tirar de admin ou apagar o usuário revoga tudo
  (trigger em `users`). Reativar **não** ressuscita.
- **Em massa**, na VM (regra 4.2: recreate antes, senão roda o binário velho):

```bash
DC="docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env"
$DC exec api revoga-mcp --dsn "$DATABASE_URL" --listar
$DC exec api revoga-mcp --dsn "$DATABASE_URL" --email pessoa@emidiastec.com.br            # dry-run
$DC exec api revoga-mcp --dsn "$DATABASE_URL" --email pessoa@emidiastec.com.br --aplicar
$DC exec api revoga-mcp --dsn "$DATABASE_URL" --todos                                     # dry-run
$DC exec api revoga-mcp --dsn "$DATABASE_URL" --todos --aplicar
```

| Flag | Efeito |
|---|---|
| `--listar` | lista credenciais vivas (tokens pessoais e uma por conexão OAuth); só lê, não combina com `--aplicar` |
| `--email X` | todas as credenciais dessa pessoa |
| `--todos` | de todo mundo; **também apaga todos os códigos OAuth pendentes** |
| `--aplicar` | sem ele é dry-run |

Use exatamente um de `--listar`/`--email`/`--todos`. A saída é **uma linha JSON**
(`aplicado`, `pessoas`, `credenciais`, `codigos_apagados`) e nunca contém token nem hash. O dry-run
conta **exatamente o que o `--aplicar` revoga**, incluindo os access tokens OAuth; por isso
`credenciais` pode ser **maior** que as linhas do `--listar`. Uma linha de auditoria por pessoa
(`mcp.token.revogado`, `actor_type 'cli'`, `metadata.pelo = "revoga-mcp"`). Saída: `0` ok (também
`--email` sem ninguém, com aviso no stderr); `2` uso errado; `1` erro de banco.

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
| Tudo parou de autenticar depois de girar o `JWT_SECRET` | esperado: os hashes mudam; todos reconectam e o Gemini precisa de cliente novo |
| Em dev, o consentimento abre o frontend de prod | falta `NOTIFICATIONS_BASE_URL=http://localhost:5174` no `.env` |

Dev local: no `.env`, `MCP_ORIGEM_PUBLICA=http://localhost:8080` **e**
`NOTIFICATIONS_BASE_URL=http://localhost:5174`. A API local leva minutos para escutar (carrega o
índice antes); health 000 logo após o recreate não é crash.

## Deploy

Dois caminhos independentes: o frontend (CF Pages) sobe sozinho a cada push em `master`; o backend só
com `./scripts/deploy.sh` na VM. **Backend primeiro**, depois o frontend — senão `/account` chama
`/v1/internal/mcp/tokens` e leva 404. Smoke de prod:

```bash
curl -si -X POST https://api.e-monitor.online/mcp | grep -i -E "^HTTP|www-authenticate"
curl -s https://api.e-monitor.online/.well-known/oauth-authorization-server
```

Esperado: `401` com `WWW-Authenticate: Bearer realm="emonitor-mcp", resource_metadata=..., scope="emonitor:all"`
(não 404, não 403). O Cloudflare Tunnel precisa expor `/mcp`, `/mcp/oauth/*` e `/.well-known/*` no host
público da API, e o WAF não pode bloquear o egress do claude.ai/Google. Novo CLI em `cmd/*` exige as
duas linhas no `workers.Dockerfile` (regra 6.7) — `revoga-mcp` e `registra-cliente-mcp` já estão.

## Armadilhas

- **Girar o `JWT_SECRET` invalida todas as credenciais, `client_secret` e códigos do MCP.**
- Access tokens antigos **não** são revogados na rotação do refresh; `mcp_tokens` nunca é podada
  (follow-ups). Reuso de refresh só recebe `invalid_grant` (a detecção estilo hub é follow-up).
- Limitação aceita: uma renovação de token em voo no mesmo milissegundo da troca de senha pode
  sobreviver a ela.
- **Recomendações ao dono (não feitas):** (1) publicar a porta da API só em `127.0.0.1` no compose de
  prod — hoje um acesso direto à 8080 forja o `CF-Connecting-IP` e foge dos limites por IP;
  (2) o limiter de `/auth/login` é contornável atrás do Cloudflare porque `middleware.RealIP` confia
  em cabeçalhos forjáveis (problema anterior ao MCP).
