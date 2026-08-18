# Radiocheck — Guia para Claude

Sistema de monitoramento de veiculação de comerciais em rádios AM/FM via streaming, substituindo fornecedor externo. Veja o plano completo em [plano_implementacao.md](plano_implementacao.md).

---

## IMPORTANTE
Quando for trabalhar com Sub-agentes, leia o doc em /docs/subagentic-use/`Subagents-usage.md` para extrair o melhor de cada sub-agente.

## Regras Críticas

### 1. O plano é lei — mas pode ser alterado com justificativa

O arquivo `plano_implementacao.md` é a fonte única de verdade arquitetural deste projeto. Ao desenvolver qualquer coisa:

- **Consulte o plano antes de começar.** Use o índice abaixo para ir direto à seção relevante sem ler o arquivo inteiro.
- **Não fuja do escopo silenciosamente.** Se perceber que a implementação vai em direção diferente do plano, pare e avise.
- **O plano é maleável**, mas só deve ser alterado quando uma solução melhor justificar a mudança. Antes de implementar diferente do que está escrito: defenda a tese, explique o porquê vale a pena mudar, aponte qual seção deve ser atualizada, e só então implemente.

### 2. Documentação de novas funcionalidades vai em `/docs`

**Nunca documente funcionalidades novas no `plano_implementacao.md`.** Esse arquivo é blueprint arquitetural, não documentação operacional. Toda funcionalidade implementada deve ter sua documentação oficial criada em `/docs`. Se a pasta não existir, crie-a.

### 3. Não invente escopo

Os **Não Objetivos** (seção 1.3, linha 32) definem explicitamente o que está fora do sistema. Não adicione reconhecimento de música, transcrição, integração com sistemas de tráfego ou captura over-the-air — mesmo que pareça útil. Qualquer expansão de escopo precisa de aprovação explícita do usuário.

### 4. Operações destrutivas em prod — leitura obrigatória antes de mexer

Esta seção existe por causa do **incidente 2026-05-12** ([postmortem](docs/incidents/incident-2026-05-12-pgdata-loss.md)) que destruiu 100% do `pgdata` de produção. Regras:

**4.1. `--force-recreate` sem `--no-deps` é proibido em produção.**

`docker compose up -d --force-recreate <service>` propaga o recreate para as dependências declaradas em `depends_on`. Para `api`, isso inclui `postgres`. Em determinadas condições o Docker considera o volume divergente e o recria do zero — apagando o dado.

```bash
# ❌ NUNCA em prod:
docker compose -f infra/docker/docker-compose.yml up -d --force-recreate api

# ✅ CORRETO:
docker compose -f infra/docker/docker-compose.yml up -d --force-recreate --no-deps api
```

A mesma regra vale para `down`, `restart`, `up`: se está mexendo em qualquer service stateful (postgres/minio/redis), passe `--no-deps` quando aplicável.

**4.2. `docker compose exec` NÃO usa imagem nova — recreate antes.**

`docker compose build api` produz imagem nova mas **não troca o binário no container que está rodando**. `docker compose exec api ...` continua executando o binário antigo. Ordem correta de deploy:

```bash
docker compose build api                                              # 1. build da imagem
docker compose up -d --force-recreate --no-deps api                   # 2. recreate (PEGA imagem nova)
docker compose exec api backfill-shared-hashes --dsn "$DATABASE_URL"  # 3. comandos one-shot
```

Inverter a ordem (2 e 3) faz o comando rodar com binário velho silenciosamente. Foi o que aconteceu com o F-108 v2 em prod no incidente 2026-05-12 — backfill rodou antigo.

**4.3. Antes de testar comando experimental em prod: teste local primeiro.**

Você tem dev local (Windows). Use. Não rode comando inédito direto na VM.

**4.4. Volumes de dado crítico usam bind mount no host, não volume nomeado.**

Em prod, o `.env` da VM deve ter `PGDATA_HOST_PATH`, `MINIODATA_HOST_PATH` e `MASTERSDATA_HOST_PATH` apontando para paths em `/srv/radiocheck/*`. Bind mounts sobrevivem a `docker compose down -v` e `--force-recreate`. Detalhes: [docs/operations/data-durability.md](docs/operations/data-durability.md).

**4.5. Backup precisa ser testado end-to-end, não só configurado.**

O backup do incidente 2026-05-12 estava "configurado" há 4 dias mas **nunca tinha rodado uma vez com sucesso** — falhas silenciosas no entrypoint engoliam o erro. Antes de confiar em qualquer backup, valide que o arquivo realmente aparece no destino:

```bash
# Após qualquer mudança no backup: rode manualmente e confirme upload
docker compose exec backup sh /backup.sh
docker run --rm -e AWS_ACCESS_KEY_ID="$R2_ACCESS_KEY" \
  -e AWS_SECRET_ACCESS_KEY="$R2_SECRET_KEY" \
  amazon/aws-cli --endpoint-url "$R2_ENDPOINT" \
  s3 ls "s3://$R2_BUCKET/postgres/daily/" | tail -3
```

**4.6. NUNCA monte `migrations/` em `docker-entrypoint-initdb.d` + use `migrate` service simultaneamente.**

O incidente 2026-05-12 expôs esse conflito latente: `initdb.d` executa `.sql` em ordem alfabética (incluindo `.down.sql` ANTES de `.up.sql`), deixando o schema em estado fragmentado que o golang-migrate marca como `dirty` e se recusa a continuar. Resultado: `users` table não criada → api crash loop. Recovery exige `DROP SCHEMA public CASCADE` + re-run do migrate. Limpeza definitiva em F-111 (pendente — remover o mount do `initdb.d`).

**4.7. Prod usa `docker-compose.override.yml` (gitignored) — sempre incluir em comandos manuais.**

A VM tem `infra/docker/docker-compose.override.yml` com bind mounts para `/mnt/db/pgdata`, `/mnt/data/minio`, `/mnt/data/masters` e `/mnt/data/audio-refs`. O `scripts/deploy.sh` em prod sempre inclui esse arquivo. **Comandos manuais sem o override usam configuração diferente da rodando — silenciosamente.** Isso causou ~4h de caos no incidente 2026-05-12: rodamos diagnóstico apontado pro `pgdata` named volume vazio enquanto o dado real estava intacto em `/mnt/db/pgdata`. Detalhes em [incident-2026-05-12-pgdata-loss.md §Causa raiz #5](docs/incidents/incident-2026-05-12-pgdata-loss.md).

```bash
# ❌ CONFIGURAÇÃO DIFERENTE da prod (silenciosamente):
docker compose -f infra/docker/docker-compose.yml ps

# ✅ Espelha o que o deploy.sh usa:
docker compose -f infra/docker/docker-compose.yml \
               -f infra/docker/docker-compose.override.yml \
               --env-file infra/docker/.env ps

# ✅✅ Atalho — só roda o deploy.sh, que já faz isso:
./scripts/deploy.sh
```

Após F-116 (base yml suporta `PGDATA_HOST_PATH` via env vars), o override é redundante e será removido em limpeza futura. Por enquanto continua existindo na VM por compatibilidade.

**4.8. Migration que depende de dados: testar contra uma CÓPIA dos dados de prod, nunca só no DB local.**

Incidente 2026-06-17: a migration `0039_backfill_legacy_commercials` (um `INSERT ... SELECT` de `commercials` pra `materials`) passou no teste local porque o DB de dev tinha **0 commercials** — o INSERT era no-op. Em prod, com dados reais, colidiu em `materials.short_id UNIQUE` (sequences separadas pré-0024 geram short_ids sobrepostos), **falhou e deixou o schema dirty**, travando o deploy (`migrate exit 1` em 0.7s, que o golang-migrate emite ao recusar rodar em DB dirty). Recovery exigiu `UPDATE schema_migrations SET version=38, dirty=false` + redeploy com a migration corrigida.

Lição: **DB local vazio/sparso dá falso verde** pra qualquer migration cuja falha dependa de volume/colisão/constraint sobre dados existentes (backfills, `ADD CONSTRAINT`, `CREATE UNIQUE INDEX`, dedup).

Proteções (já implementadas):
- **`scripts/deploy.sh` roda um "teste de sombra"** (`shadow_migration_test`) ANTES do `up -d`: sobe um postgres descartável, restaura o dump de prod nele, roda as migrations pendentes ali, e **aborta o deploy se falhar** — prod nunca fica dirty. Bypass de emergência: `SKIP_MIGRATION_SHADOW=yes` (não use sem motivo forte).
- **Pra testar uma migration local com confiança**, clone os dados de prod num postgres descartável e rode `migrate up` lá. Procedimento canônico: [docs/operations/migrations.md §Testar migration contra dados de prod](docs/operations/migrations.md). NUNCA confie só no `go test`/local vazio pra migrations de dado.

### 5. `npm install` no Windows quebra o build do Cloudflare Pages — leitura obrigatória antes de mexer em deps do frontend

Esta seção existe porque o erro **se repete**: rodar `npm install`/`npm i <pkg>` em `frontend/` no Windows **poda do `package-lock.json` as dependências opcionais específicas de Linux** (ex.: `@emnapi/*`, bindings nativos que o `vite`/`rolldown` usa no build). O Cloudflare Pages roda **`npm ci` em Linux**, que exige o lockfile completo e falha com:

```
npm error code EUSAGE
npm error `npm ci` can only install packages when your package.json and
npm error package-lock.json ... are in sync.
npm error Missing: @emnapi/core@x.y.z from lock file
```

**5.1. Sintoma.** Deploy do CF Pages morre no passo `npm clean-install` logo após instalar o Node. Quase sempre depois de um commit que tocou `frontend/package.json` + `package-lock.json`.

**5.2. Causa raiz.** `npm` no Windows reescreve o lockfile sem os pacotes `os: ["linux"]` opcionais. Lockfile v3 *deveria* preservá-los cross-platform, mas na prática o install no Windows os remove. Os bindings nativos do toolchain de build (rolldown/vite 8) são linux-only em prod.

**5.3. Como adicionar/atualizar uma dep do frontend sem quebrar o CF Pages.** NÃO commite um lockfile podado. Reconstrua a partir do lockfile completo (master) injetando só a dep nova, e valide:

```bash
# 1. parte do lockfile completo do master e injeta SÓ a dep nova (node script)
node -e "
const cp=require('child_process'), fs=require('fs');
const head=JSON.parse(cp.execSync('git show HEAD:frontend/package-lock.json',{maxBuffer:1e8}));
const base=JSON.parse(cp.execSync('git show master:frontend/package-lock.json',{maxBuffer:1e8}));
const k='node_modules/<PKG>';
base.packages[k]=head.packages[k];
base.packages[''].dependencies['<PKG>']=head.packages[''].dependencies['<PKG>'];
const o={};Object.keys(base.packages).sort().forEach(x=>o[x]=base.packages[x]);base.packages=o;
fs.writeFileSync('frontend/package-lock.json',JSON.stringify(base,null,2)+'\n');
"

# 2. valida a sincronia (é o check exato que o CF roda). exit 0 = ok.
cd frontend && npm ci --dry-run

# 3. confirma que as opcionais linux continuam no lockfile
grep -c emnapi frontend/package-lock.json   # deve bater com o do master, não cair
```

**5.4. Verificação rápida antes de qualquer push que toque o lockfile.** `git show master:frontend/package-lock.json | grep -c emnapi` vs `grep -c emnapi frontend/package-lock.json` — se o número **caiu**, o lockfile foi podado: NÃO pushe, refaça pelo passo 5.3. Detalhe do incidente: ver o commit que restaurou `@emnapi` (`fix(frontend): restaura optional deps @emnapi no lockfile`).

**5.5. Alternativa definitiva (quando der pra testar).** Rodar o `npm install` num ambiente Linux (WSL/container) gera o lockfile completo de uma vez. Enquanto não houver esse fluxo, use 5.3.

### 6. Antes de qualquer commit+push que vai pra prod via `scripts/deploy.sh` — prove que o deploy não quebra

O `deploy.sh` roda em prod e tem várias etapas que podem **abortar o deploy** (ou pior, subir binário velho/quebrado). **Nunca pushe pra `master` confiando só no `go build` nativo.** Reproduza localmente o que o deploy realmente faz e confirme cada item abaixo que sua mudança toca:

**6.1. Build da imagem = cross-compile, não o build nativo.** O [`workers.Dockerfile`](infra/docker/Dockerfiles/workers.Dockerfile) compila com `CGO_ENABLED=0 GOOS=linux go build` (e **só** `go build` — não roda `go test`/`go vet`). Reproduza idêntico antes de pushar:

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...   # tem que passar pra TODOS os cmd/*
```

Build nativo Windows passar **não** garante que o cross-compile linux passa. Se Docker local estiver de pé, o gold standard é `docker build -f infra/docker/Dockerfiles/workers.Dockerfile -t rc-verify .` na raiz.

**6.2. `go.mod`/`go.sum` e versão de Go.** Se mudou deps, confirme `go.mod`/`go.sum` consistentes (o Dockerfile faz `go mod download`). Se bumpou a diretiva `go` no `go.mod`, ela tem que ser **≤** a major-minor da imagem (`golang:1.26-alpine`) — senão o build quebra com "requires go >= X".

**6.3. Migrations.** Se tocou `migrations/`, o `shadow_migration_test` do deploy aplica as pendentes sobre uma **cópia dos dados de prod** e **aborta** se falhar (regra 4.8). Teste contra dados de prod antes — não confie em DB local vazio.

**6.4. Frontend.** Se tocou `frontend/package*.json`, vale a regra 5 inteira (lockfile podado quebra o CF Pages).

**6.5. Boot da API.** O deploy faz health check em `/v1/internal/health` (60s) e aborta se não responder. Garanta que a API sobe: nomes de métrica Prometheus únicos no `MustRegister` (colisão = panic no `init`), maps inicializados no construtor (`supervisor.New` etc — nil map = panic em runtime), sem panic no boot.

**6.6. Testes flaky ≠ regressão.** Rode `go test ./...` mesmo o deploy não rodando testes — mas saiba distinguir falha sua de flaky pré-existente. Conhecidas: `internal/catalog TestBuildDailySummary_WithDowntime` falha antes de ~13:00 UTC (usa `time.Now().Add(-13h)` que atravessa a meia-noite). Confirme que a falha está num pacote que você **não tocou** antes de descartá-la.

**6.7. CLI novo em `cmd/*` NÃO entra na imagem sozinho — adicione ao `workers.Dockerfile`.** O [`workers.Dockerfile`](infra/docker/Dockerfiles/workers.Dockerfile) **NÃO faz `go build ./...`** — ele builda e copia **cada binário explicitamente** (uma linha `RUN ... go build -o /out/<nome> ./cmd/<nome>` + uma `COPY --from=builder /out/<nome> /usr/local/bin/<nome>`). Se você criar um `cmd/<novo>/` e esquecer as duas linhas, o `go build ./...` local passa, o deploy sobe **sem o binário**, e na VM `docker compose exec api <novo>` falha com "not found" — silenciosamente, só na hora de rodar. **Toda vez que criar um CLI novo, adicione as duas linhas ao Dockerfile no mesmo commit.** (Os CLIs rodam em prod via `docker compose exec api <nome> --dsn "$DATABASE_URL"`, regra 4.2.)

---

### 7. Você (Claude) NÃO tem acesso direto à VM de produção nem ao banco de prod

Não existe `gcloud` no PATH desta máquina dev (Windows), e o SSH pra VM falha (host key rotacionada / acesso restrito por IP). **Não tente `gcloud`/`ssh` pra rodar query em prod** — não funciona e só gasta tempo (lição 2026-06-30).

Como investigar dado de prod, então:

- **Leia as queries do próprio sistema** no código Go pra entender atribuição/categorização — é ali que mora o SQL canônico: `workers/internal/evidence/{attribution,disambig_coverage,service}.go` (atribuição + reatribuição §18.2.2), `workers/internal/catalog/{detections,daily_summary,distribution_rules,detection_campaigns}.go` (projeção canônica + categorizador + grade).
- **Monte o SQL de diagnóstico read-only (SELECT)** e **entregue pro Dereck rodar** na VM; ele cola o resultado de volta. Escreva o SQL completo e auto-contido (o Dereck roda às cegas) — de preferência via `docker compose ... exec -T postgres psql -At -c "..."`. Qualquer reparo de dado (`UPDATE`) você **escreve com preview+ROLLBACK→COMMIT**, mas QUEM executa em prod é o Dereck.
- O método canônico de cruzamento com o fornecedor está em [docs/operations/vendor-reconciliation.md](docs/operations/vendor-reconciliation.md) (as 5 bandejas de discrepância). Padrões de má-atribuição já mapeados: memórias `duplicate-material-cross-campaign-misattribution` e `detection-campaigns-projection-sync-reattribution`.
- Snapshots/backups pontuais às vezes existem em `c:\tmp` (ex.: `backup-prod-*.sql`, `prod-snapshot.json`), mas costumam estar **desatualizados** — confira a data antes de confiar; não valem pra dado recente.

---

## Índice do Plano (`plano_implementacao.md`)

Use estes links para ir direto à seção relevante em vez de ler o arquivo inteiro.

| Seção | Linha | Conteúdo |
|-------|-------|----------|
| [§1 Visão Geral](plano_implementacao.md#L11) | 11 | Contexto, objetivos, não-objetivos, métricas de sucesso |
| [§1.1 Contexto de Negócio](plano_implementacao.md#L13) | 13 | Por que o projeto existe, o que substitui |
| [§1.2 Objetivos Funcionais](plano_implementacao.md#L19) | 19 | Lista dos 8 requisitos funcionais do sistema |
| [§1.3 Não Objetivos](plano_implementacao.md#L32) | 32 | **Leia antes de adicionar qualquer coisa nova** |
| [§1.4 Métricas de Sucesso](plano_implementacao.md#L41) | 41 | KPIs que definem quando o projeto é bem-sucedido |
| [§2 Glossário](plano_implementacao.md#L53) | 53 | Fingerprint, constellation map, peak pair, LUFS, SBR etc. |
| [§3 Riscos Técnicos](plano_implementacao.md#L81) | 81 | Catálogo de R1–R37: todos os riscos mapeados |
| [§3.1 Riscos de Áudio Broadcast](plano_implementacao.md#L85) | 85 | R1–R10: compressão, clipping, codecs, AM |
| [§3.2 Riscos de Transporte](plano_implementacao.md#L107) | 107 | R11–R17: quedas, dropouts, mudança de URL |
| [§3.3 Riscos de Detecção](plano_implementacao.md#L123) | 123 | R18–R25: falsos positivos/negativos |
| [§3.4 Riscos Operacionais](plano_implementacao.md#L141) | 141 | R26–R37: memory leak, cooldown, storage |
| [§4 Mitigações](plano_implementacao.md#L169) | 169 | Como cada risco é endereçado |
| [§5 Arquitetura de Alto Nível](plano_implementacao.md#L229) | 229 | Componentes, fluxo de dados, deployment |
| [§5.1 Visão de Componentes](plano_implementacao.md#L231) | 231 | Diagrama textual dos componentes |
| [§5.2 Fluxo de Dados](plano_implementacao.md#L249) | 249 | Como os dados fluem entre componentes |
| [§5.3 Comunicação entre Componentes](plano_implementacao.md#L310) | 310 | Protocolos internos |
| [§5.4 Modelo de Deployment](plano_implementacao.md#L322) | 322 | Topologia de servidores |
| [§6 Stack Tecnológico](plano_implementacao.md#L420) | 420 | Linguagens, frameworks, bibliotecas — com justificativas |
| [§6.1 Stack Definitiva](plano_implementacao.md#L334) | 334 | Decisões fechadas de tecnologia |
| [§6.2 Justificativas](plano_implementacao.md#L356) | 356 | Por que cada escolha foi feita |
| [§6.3 Bibliotecas Externas](plano_implementacao.md#L386) | 386 | Libs específicas usadas |
| [§7 Pipeline de Fingerprint](plano_implementacao.md#L420) | 420 | Geração de fingerprint acústico do master |
| [§7.1 Fluxo Geral](plano_implementacao.md#L424) | 424 | Passos do pipeline |
| [§7.2 Comandos Concretos](plano_implementacao.md#L461) | 461 | Comandos ffmpeg exatos |
| [§7.3 Algoritmo de Fingerprint](plano_implementacao.md#L510) | 510 | Constellation map, peak pairs, hashing |
| [§7.4 Persistência](plano_implementacao.md#L601) | 601 | Como fingerprints são armazenados |
| [§7.5 Validação Pós-Geração](plano_implementacao.md#L609) | 609 | Como validar que o fingerprint gerado é bom |
| [§8 Pipeline de Ingestão](plano_implementacao.md#L621) | 621 | Stream worker: captura e análise de stream |
| [§8.1 Estrutura do Worker](plano_implementacao.md#L623) | 623 | Arquitetura interna do worker |
| [§8.2 Comando ffmpeg](plano_implementacao.md#L653) | 653 | Comando exato de ingestão |
| [§8.3 Ring Buffers](plano_implementacao.md#L682) | 682 | Buffers para evidência e análise |
| [§8.4 Pré-processamento](plano_implementacao.md#L690) | 690 | Processamento da janela antes do matching |
| [§8.5 Health Monitoring](plano_implementacao.md#L713) | 713 | Como detectar worker travado |
| [§8.6 Reconexão](plano_implementacao.md#L723) | 723 | Backoff exponencial e jitter |
| [§8.7 Restart Preventivo](plano_implementacao.md#L744) | 744 | Reinício periódico para mitigar memory leaks |
| [§9 Algoritmo de Matching](plano_implementacao.md#L751) | 751 | Matching acústico: o coração do sistema |
| [§9.1 Visão Geral](plano_implementacao.md#L755) | 755 | Overview do algoritmo |
| [§9.2 Índice em Memória](plano_implementacao.md#L768) | 768 | Estrutura de dados do índice de hashes |
| [§9.3 Match por Janela](plano_implementacao.md#L795) | 795 | Histograma de delta, algoritmo completo |
| [§9.4 Threshold Adaptativo](plano_implementacao.md#L871) | 871 | Calibração por emissora |
| [§9.5 State Machine](plano_implementacao.md#L891) | 891 | Máquina de estados de detecção |
| [§9.6 Cobertura Temporal](plano_implementacao.md#L928) | 928 | Cálculo de cobertura para confirmação |
| [§9.7 Matching Multi-Rate](plano_implementacao.md#L950) | 950 | Tolerância a time stretching (R8) |
| [§9.8 Desambiguação de Versões](plano_implementacao.md#L962) | 962 | Escolha entre cortes do mesmo comercial (R20) |
| [§9.9 Audit de Evidência Pré-Persist](plano_implementacao.md#L979) | 979 | Re-fingerprint do clipe salvo × master antes de marcar veiculação como confirmada |
| [§10 Verificação Neural](plano_implementacao.md#L1037) | 1037 | Camada de verificação por embeddings neurais |
| [§10.1 Quando Ativar](plano_implementacao.md#L985) | 985 | Condições para uso da camada neural |
| [§10.2 Modelo](plano_implementacao.md#L995) | 995 | Modelo de embedding usado |
| [§10.3 Pipeline](plano_implementacao.md#L1001) | 1001 | Pipeline de verificação neural |
| [§11 Geração de Evidência](plano_implementacao.md#L1033) | 1033 | Gravação e armazenamento de clips de evidência |
| [§11.1 Fluxo](plano_implementacao.md#L1035) | 1035 | Como a evidência é gerada |
| [§11.2 Encoding Final](plano_implementacao.md#L1048) | 1048 | Comando de encoding do clip |
| [§11.3 Layout no Storage](plano_implementacao.md#L1062) | 1062 | Estrutura de pastas/buckets |
| [§11.4 Retenção e Tiering](plano_implementacao.md#L1079) | 1079 | Política de 30 dias hot / 12 meses cold |
| [§11.5 Tratamento de Falhas](plano_implementacao.md#L1087) | 1087 | O que fazer quando o clip não salva |
| [§12 Esquema de Dados](plano_implementacao.md#L1097) | 1097 | DDL PostgreSQL completo |
| [§12.1 DDL Completo](plano_implementacao.md#L1099) | 1099 | Tabelas, índices, constraints |
| [§12.2 Estimativas de Volume](plano_implementacao.md#L1358) | 1358 | Projeção de crescimento dos dados |
| [§13 Contratos de API](plano_implementacao.md#L1371) | 1371 | API REST e eventos NATS |
| [§13.1 API Externa (Clientes)](plano_implementacao.md#L1373) | 1373 | Endpoints para integração de clientes |
| [§13.2 API Interna (Operadores)](plano_implementacao.md#L1493) | 1493 | Endpoints de operação |
| [§13.3 Eventos NATS](plano_implementacao.md#L1504) | 1504 | Mensageria interna |
| [§14 Infraestrutura](plano_implementacao.md#L1536) | 1536 | Topologia de servidores, networking, backup |
| [§14.1 Topologia](plano_implementacao.md#L1538) | 1538 | Layout de servidores recomendado |
| [§14.2 Distribuição de Workers](plano_implementacao.md#L1569) | 1569 | Como distribuir workers entre servidores |
| [§14.3 Networking](plano_implementacao.md#L1573) | 1573 | Rede, IPs, pool para anti-bloqueio |
| [§14.4 Backup e DR](plano_implementacao.md#L1581) | 1581 | Estratégia de backup e disaster recovery |
| [§14.5 Provisionamento](plano_implementacao.md#L1600) | 1600 | Configuração de servidores |
| [§15 Observabilidade](plano_implementacao.md#L1622) | 1622 | Métricas, logs, tracing, dashboards, alertas |
| [§15.1 Métricas Prometheus](plano_implementacao.md#L1624) | 1624 | Lista de métricas expostas |
| [§15.2 Logging](plano_implementacao.md#L1659) | 1659 | Estratégia de logging estruturado |
| [§15.3 Tracing](plano_implementacao.md#L1666) | 1666 | Distributed tracing |
| [§15.4 Dashboards Grafana](plano_implementacao.md#L1677) | 1677 | Painéis de monitoramento |
| [§15.5 Alertas](plano_implementacao.md#L1697) | 1697 | Regras de alerta crítico |
| [§15.6 Runbooks](plano_implementacao.md#L1714) | 1714 | Procedimentos de resposta a incidentes |
| [§16 Segurança](plano_implementacao.md#L1736) | 1736 | Auth, roles, proteção de dados, audit log |
| [§17 Migração e Coexistência](plano_implementacao.md#L1797) | 1797 | Convivência com o fornecedor atual |
| [§18 Plano de Execução por Fases](plano_implementacao.md#L1845) | 1845 | Cronograma: 4 fases ao longo de 36 semanas |
| [§18.1 Fase 1 — Prova Técnica](plano_implementacao.md#L1847) | 1847 | Semanas 1–6: PoC funcional |
| [§18.2 Fase 2 — Hardening](plano_implementacao.md#L1873) | 1873 | Semanas 7–18: estabilização e coexistência |
| [§18.2.1 Ciclo de vida de campanha](plano_implementacao.md#L1893) | 1893 | Estados programada/ativa/concluida/cancelada com transição automática por data |
| [§18.2.2 Desambiguação de versões](plano_implementacao.md#L1970) | 1970 | Dedup pós-confirmação entre cortes 30s/60s do mesmo cliente |
| [§18.3 Fase 3 — Escala](plano_implementacao.md#L1892) | 1892 | Semanas 19–30: migração comercial |
| [§18.4 Fase 4 — Corte Total](plano_implementacao.md#L1908) | 1908 | Semanas 31–36: desligamento do fornecedor |
| [§18.5 Cronograma Sumário](plano_implementacao.md#L1922) | 1922 | Tabela resumida |
| [§18.6 Go/No-Go](plano_implementacao.md#L1934) | 1934 | Marcos de decisão entre fases |
| [§19 Validação](plano_implementacao.md#L1945) | 1945 | Testes unitários, integração, carga, chaos |
| [§20 Estimativa de Custos](plano_implementacao.md#L2009) | 2009 | Infraestrutura, equipe, comparativo com fornecedor |
| [§21 Riscos do Projeto](plano_implementacao.md#L2065) | 2065 | Riscos de prazo, técnicos, regulatórios |
| [§22 Decisões Pendentes](plano_implementacao.md#L2120) | 2120 | Decisões tomadas, premissas, itens aguardando input |
| [§23 Referências](plano_implementacao.md#L2158) | 2158 | Material de estudo e papers |
| [§24 Apêndice A — ffmpeg](plano_implementacao.md#L2174) | 2174 | Comandos ffmpeg de referência |
| [§25 Apêndice B — Constantes](plano_implementacao.md#L2234) | 2234 | Tabela de constantes do sistema |
| [§26 Apêndice C — Estrutura de Repositório](plano_implementacao.md#L2276) | 2276 | Layout de pastas esperado |
| [§27 Conclusão e Próximos Passos](plano_implementacao.md#L2401) | 2401 | Próximas ações e critério de sucesso final |

---

## Contexto Rápido

- **Problema:** substituir fornecedor de monitoramento de comerciais em rádio que custa caro e tem atendimento ruim.
- **Meta principal:** monitorar 200+ emissoras simultaneamente, detectar comerciais em <10s, com 98%+ de concordância com o fornecedor atual.
- **Núcleo técnico:** fingerprinting acústico estilo Shazam (constellation map + histogram de delta), com broadcast simulation na referência para robustez a degradação de stream.
- **Fase atual:** ver §18 para o cronograma. Fase 1 é PoC técnico (semanas 1–6).
- **Stack:** ver §6. Decisões fechadas; não sugira trocar sem justificativa forte.

---

## Documentação em `/docs` — formato obrigatório

A documentação operacional do sistema fica em `/docs`. Estrutura canônica:

```
docs/
  README.md                 — índice (mantenha atualizado ao adicionar/mover docs)
  AUDIT-AAAA-MM-DD.md       — relatórios de auditoria (preservar histórico, não sobrescrever)
  architecture/             — como o sistema funciona (conceitual, estável)
  features/                 — feature implementada (uma feature por arquivo)
  operations/               — operar o sistema em prod
  incidents/                — postmortems (incident-AAAA-MM-DD-{slug}.md)
  roadmap/                  — follow-ups, evaluations, planos de fase
  archive/                  — histórico inativo (não mover de volta sem motivo forte)
  runbooks/                 — resposta a alertas Prometheus
  superpowers/              — specs/plans (gerados por skill — não editar à mão)
```

### Regras

1. **Header YAML obrigatório no topo de todo doc** (exceto README/AUDIT/superpowers):

   ```yaml
   ---
   status: implementado | parcialmente-implementado | legado | planejado
   ultima-verificacao: AAAA-MM-DD
   codigo-relacionado:
     - workers/internal/foo/bar.go
     - migrations/00NN_xxx.up.sql
   ---
   ```

2. **Ao implementar feature nova:** criar `docs/features/{nome}.md` com o header. Se a feature for arquitetural (algoritmo, fluxo de dados), usar `docs/architecture/`. Se for operacional (deploy, observabilidade), usar `docs/operations/`.

3. **Ao resolver incidente:** criar `docs/incidents/incident-AAAA-MM-DD-{slug}.md` com postmortem, ações pós-incidente e referências cruzadas.

4. **NUNCA documente feature nova no `plano_implementacao.md`.** Esse arquivo é blueprint arquitetural, não changelog operacional.

5. **Atualizar `ultima-verificacao`** quando revalidar o doc contra o código.

6. **Status flutua:** `implementado` → `legado` quando o código diverge significativamente; sinalize com comentário inline e crie follow-up para reescrever.

### Mapa de consulta — quando trabalhar em X, ver Y

| Quando você for mexer em… | Comece por |
|---------------------------|------------|
| Schema / migrations | [docs/operations/migrations.md](docs/operations/migrations.md) — leitura obrigatória antes de mexer em schema |
| Auth, JWT, bootstrap admin, role gating | [docs/operations/auth-bootstrap.md](docs/operations/auth-bootstrap.md) |
| Gerenciamento de usuários (admin/cliente, /admin/users, /account) | [docs/features/user-management.md](docs/features/user-management.md) |
| **Usuário de agência com vários clientes vinculados** (carteira `user_clients`, escopo em lista no JWT, `/insights` exige escolher 1 cliente) | [docs/features/multi-client-user.md](docs/features/multi-client-user.md) — a poda de carteira no `Update` é o que impede vazamento: `client_id` sozinho significa "tem EXATAMENTE este cliente" |
| **Boas-vindas ao novo usuário** (checkbox em /admin/users → email + página pública `/boasvindas/:token` com credenciais, vídeo e tutorial; senha cifrada AES-GCM, link sem expiração, revogação) | [docs/features/welcome-onboarding.md](docs/features/welcome-onboarding.md) — **ao criar qualquer página pública nova, acrescente a rota em `PUBLIC_ROUTES` no `frontend/src/api/client.js`**: sem isso, qualquer chamada paralela que tome 401 (telemetria, por exemplo) expulsa o visitante pro /login |
| **Sugestões** / central de demandas do dev (`/admin/suggestions`, 2 personas: autor cria/acompanha, dev `SUGGESTIONS_DEV_EMAIL`=tatico3@hubradios.com gerencia; thread, imagens S3, board/lista) | [docs/features/suggestions-board.md](docs/features/suggestions-board.md) |
| **Pós-venda** (admin monta o fechamento em `/admin/pos-venda`, dispara email e o cliente abre em `/pos-venda/:token`; valor entregue, impactos, CPM, mapa e checking por emissora + zip dos relatórios; admin com `receive_post_sale_emails` recebe cópia de TODOS) | [docs/features/post-sale.md](docs/features/post-sale.md) — **documento CONGELADO** (`payload_json`): recategorização/reatribuição depois do envio não mudam o que o cliente leu. Números vêm da base do `/insights` (única período-aware, e é a foto que vai no zip), NÃO da do `/campaigns`. Publish sobe artefatos ANTES de qualquer email — S3 fora do ar aborta em `draft`. Rota pública em `PUBLIC_ROUTES` e 404 (nunca 401) |
| Desativar cliente (reversível) + delete→409 com vínculos + login gating de cliente inativo | [docs/features/client-deactivation.md](docs/features/client-deactivation.md) |
| Deploy, docker-compose, override file, Cloudflare Tunnel | [docs/operations/deploy.md](docs/operations/deploy.md) |
| Custo por emissora, teto de capacidade da VM, dimensionar RAM/CPU pra N emissoras | [docs/operations/capacity-and-unit-cost.md](docs/operations/capacity-and-unit-cost.md) — medições de prod 2026-07-21; **não dimensione por `load average`** (a carga é em rajada, lê 6.9/8 com CPU em 53%) |
| Disco cheio na VM / segmentos de evidência (`segmentsdata`) ocupando o disco de OS | [docs/operations/segments-disk-migration.md](docs/operations/segments-disk-migration.md) |
| Backup, restore, retenção de evidência | [docs/operations/data-durability.md](docs/operations/data-durability.md) (canônico) + [docs/operations/backup-and-retention.md](docs/operations/backup-and-retention.md) (parcialmente desatualizado) |
| OpenTelemetry, Jaeger, spans, sampling | [docs/operations/tracing.md](docs/operations/tracing.md) |
| Profiling com pprof (CPU, heap, goroutines, mutex) | [docs/operations/profiling.md](docs/operations/profiling.md) |
| Dashboards Grafana, métricas | [docs/operations/dashboards.md](docs/operations/dashboards.md) |
| Calibração de threshold | [docs/operations/calibration.md](docs/operations/calibration.md) + [docs/operations/threshold-dynamic.md](docs/operations/threshold-dynamic.md) |
| Worker reconciler (stream_url + comerciais) + stall watchdog | [docs/operations/worker-commercial-reconciler.md](docs/operations/worker-commercial-reconciler.md) (incidentes 2026-05-08 e 2026-05-15) |
| Algoritmo de fingerprint / pipeline offline | [docs/architecture/fingerprint-pipeline.md](docs/architecture/fingerprint-pipeline.md) |
| Algoritmo `shared-hash` (subset vs sting vs skip <10s) | [docs/architecture/shared-hash-detection.md](docs/architecture/shared-hash-detection.md) (incidentes 2026-05-09/12) |
| Ciclo de vida de campanha (programada/ativa/concluida/cancelada) | [docs/architecture/campaign-lifecycle.md](docs/architecture/campaign-lifecycle.md) |
| **Nova listagem/seletor/dropdown/contagem/KPI de campanhas (ou de dados derivados: detecções, materiais, emissoras-alvo)** — SEMPRE decidir o tratamento de `cancelada` | [docs/features/cancelled-campaign-handling.md](docs/features/cancelled-campaign-handling.md) — regra: cancelada **fora** de seletores/telas ao vivo/KPIs operacionais/cobrança; **mantida e marcada** (selo + déficit congelado em `cancelled_at`) só no histórico/relatórios. Não nasça uma listagem sem aplicar isso. |
| Dedup pós-confirmação entre cortes 30s/60s | [docs/architecture/version-disambiguation.md](docs/architecture/version-disambiguation.md) |
| Multi-atribuição (mesma tocada conta p/ N campanhas; flag `MULTI_ATTRIBUTION`; tabela `detection_campaigns` + view `detection_attributions`) | [docs/features/multi-attribution.md](docs/features/multi-attribution.md) |
| **Qualquer coisa que decida a categoria de uma veiculação** (`in_slot`/`out_slot`/`out_date`/`bonus`), o **déficit**, a **bonificação** ou a **base financeira** — inclusive "por que essa tocada não contou?", "por que a bonificação virou fora-do-prazo?", "por que o número do cliente mudou?" | [docs/features/quota-aware-categorization.md](docs/features/quota-aware-categorization.md) — fechamento por **cota da célula-dia** (campanha × tipo × emissora × dia SP): as N primeiras dentro de faixa são `in_slot`, o resto `bonus`; `out_slot` só enquanto `in_slot < N` e **não vale nada** (não fatura, não abate déficit); `orphan` foi renomeada pra `bonus`. **Dois motores** (`categorizer.Settle` × `recatClassifiedCTE`) que só não divergem por causa do `settle_parity_test.go` — mexeu num, mexa no outro. Retratar/ignorar/reatribuir uma tocada **muda a categoria das outras do mesmo dia** |
| Regras de distribuição, overrides e gatilhos de recategorização | [docs/architecture/distribution-rules.md](docs/architecture/distribution-rules.md) — a **regra** de categorização mora no doc de cota acima |
| Regra de distribuição escopada a materiais específicos (carve-out, `material_ids[]`) | [docs/features/material-specific-distribution-rules.md](docs/features/material-specific-distribution-rules.md) |
| Contagem de veiculações divergindo entre telas (modal × grid × /insights × /management × /live-map) — filtro canônico "aprovado" (`catalog.ApprovedDetectionsFilter`) | [docs/architecture/detection-count-consistency.md](docs/architecture/detection-count-consistency.md) — desde 2026-08-17 o recat/reconciler **também** aplicam o filtro (a cota exige), e `/campaigns` × `/insights` valorizam o mesmo conjunto (`in_slot + bonus`) |
| Categoria de projeção divergente da grade (bônus fantasma), reconciler de projeções, drift | [docs/architecture/projection-category-invariant.md](docs/architecture/projection-category-invariant.md) |
| Audit de evidência pré-persist (§9.9) | [docs/architecture/evidence-audit.md](docs/architecture/evidence-audit.md) |
| Segmentos ADTS-AAC e extração de evidência | [docs/architecture/evidence-segments.md](docs/architecture/evidence-segments.md) |
| Design tokens, CSS, .btn, RSelect, .field | [docs/architecture/frontend-design-system.md](docs/architecture/frontend-design-system.md) |
| **Barra de filtros de qualquer tela nova** (`.flow-filters` — label + badge numerada + controle de 38px, SEM card) e **estado vazio** dela (`FlowEmptyState` — silhueta ao fundo + cartão + stepper) | [docs/architecture/frontend-design-system.md §Barra de filtros em passos](docs/architecture/frontend-design-system.md) — passo **encadeado** (`--locked/--active/--done`) só quando um filtro realmente destrava o próximo; senão é `--optional` (badge vazada + tag "opcional"), que nunca bloqueia. Não crie card de filtros nem empty-state próprio |
| Wizard de campanha (6 etapas) | [docs/features/campaign-wizard.md](docs/features/campaign-wizard.md) |
| CPM fixo opcional por campanha (Step 6 do wizard, override em /campaigns + /insights + dashboard) | [docs/features/campaign-fixed-cpm.md](docs/features/campaign-fixed-cpm.md) — o **CPM dinâmico é `(investido + bonificado) ÷ impactos × 1000`**: a bonificação está no denominador, então tem que estar no numerador a preço de tabela. Já foi "simplificada" por acidente uma vez (−6,5%); há **guarda de teste** que falha se voltar a ser só o pago. `fixed_cpm` **não** é obrigatório em consolidado (18 de 25 campanhas não têm) |
| Etapa Conexão do wizard (Step 3 — testar/trocar stream_url por emissora; ping/stream/worker efêmeros) | [docs/features/campaign-connection-step.md](docs/features/campaign-connection-step.md) |
| Página /detections (grade station × material × dia) | [docs/features/detections-view.md](docs/features/detections-view.md) |
| Página /reports/airtime (Relatório Data e Hora — lista cronológica de veiculações; **seleção múltipla de campanhas** de UM cliente, campanha na linha, relatório por campanha) | [docs/features/airtime-report.md](docs/features/airtime-report.md) — `nil` ≠ slice vazio nos filtros `CampaignIDs`; pricing indexado por `(campanha, emissora)`; escopo do viewer no agregado virou filtro SQL (campanha alheia não soma, não dá 404) |
| Modal de detalhe do dia (`DayDetailModal`) — bloco "Plano do dia" (faixas que valem no dia, progresso por faixa, escopo por material, saldo derivado) | [docs/features/detections-day-plan.md](docs/features/detections-day-plan.md) — o bloco é **só apresentação**: o veredito vem do backend (`det.category`), o cliente não recalcula categoria |
| Veiculações manuais em lote + comprovante PDF (1 PDF→N, materiais mistos, tabela `manual_proof_batches` + `detections.proof_batch_id`) + censura tardia (`POST /detections/:id/evidence`) + rótulo /stations "Sem campanha ativa" | [docs/features/manual-airings-bulk-and-proof.md](docs/features/manual-airings-bulk-and-proof.md) |
| Página /materials (materiais tocáveis por campanha + Σ programado por emissora, admin + cliente) | [docs/features/materials-page.md](docs/features/materials-page.md) |
| Webhooks (HMAC, retry, outbox) | [docs/features/webhooks.md](docs/features/webhooks.md) |
| Presigned URLs (frontend acessa evidência direto no S3) | [docs/features/evidence-presigned-urls.md](docs/features/evidence-presigned-urls.md) |
| Busca de emissoras (tokens AND, field OR) | [docs/features/broadcaster-search.md](docs/features/broadcaster-search.md) |
| Listagem `/stations` e ficha read-only da emissora (clique na linha; é como o CLIENTE vê os dados — a tela de editar é admin-only) | [docs/features/station-detail-modal.md](docs/features/station-detail-modal.md) |
| Geocoding de emissoras (lat/long por cidade+UF, dataset IBGE, backfill) | [docs/features/geocoding-emissoras.md](docs/features/geocoding-emissoras.md) |
| Biblioteca de materiais por cliente | [docs/features/material-library.md](docs/features/material-library.md) |
| Pipeline polimórfico (material_id OR commercial_id) | [docs/features/material-fingerprint-pipeline.md](docs/features/material-fingerprint-pipeline.md) |
| Alerta de duplicata por similaridade ≥50% | [docs/features/material-similarity-warning.md](docs/features/material-similarity-warning.md) |
| Faixa horária em overrides (popover do grid, categorizador) | [docs/features/override-time-window.md](docs/features/override-time-window.md) — override supersede as regras do dia: `plays_expected` vira a meta N e a faixa dele é a única válida. **Célula zerada (`plays_expected = 0`) = meta 0 → toda tocada é `bonus`**, nunca `out_slot` |
| Simulador de stream pra teste local | [docs/operations/simulacao-radio.md](docs/operations/simulacao-radio.md) |
| Página `/operations` (supervisor ao vivo — bytes, reconnects, stall restarts, min_hashes por worker) | [docs/features/operations-page.md](docs/features/operations-page.md) |
| Painel admin `/admin/overview` (health de toda a stack: infra + workers + streams + pipeline) | [docs/features/admin-system-overview.md](docs/features/admin-system-overview.md) |
| Visão Gerencial `/management` (painel admin da operação inteira — KPIs cross-campanha + mapa + feed global ao vivo, filtros opcionais) | [docs/features/management-overview.md](docs/features/management-overview.md) |
| Painel admin `/admin/monitoring` (telemetria HTTP por rota, IP × usuário com risco, bloqueio de IP/usuário, Web Vitals) | [docs/features/admin-monitoring.md](docs/features/admin-monitoring.md) |
| Painel admin `/admin/station-failures` (emissoras com falha no dia + campanhas com slots perdidos, deep-link pra /campaigns) | [docs/features/admin-station-failures.md](docs/features/admin-station-failures.md) |
| Modo "Por campanha" de `/admin/station-failures` (cards por campanha + drill-in drawer + PDF de cobrança) | [docs/features/admin-campaign-failures.md](docs/features/admin-campaign-failures.md) — desde 2026-08-17 o déficit vem partido em `deficit_absent` ("não tocou") × `deficit_off_slot` ("fora do horário"), porque `out_slot` deixou de fechar a obrigação e a emissora não pode ser cobrada por ausência num dia em que veiculou |
| Aba "Por dia" de `/admin/station-failures` (série temporal de falhas por dia + padrão por terço do mês e dia da semana; 3 métricas; clique na barra abre o dia) | [docs/features/admin-failures-daily.md](docs/features/admin-failures-daily.md) — mesma definição de falha das outras 2 abas (é o que faz o nº bater ao clicar); "tempo fora do ar" plotado em MINUTOS (em segundos o eixo repetia "2h"); painéis comparam MÉDIA por dia, nunca soma (blocos têm nº de dias diferente) |
| Sininho de notificações (`/dashboard` admin, last-7d com read-state persistido) | [docs/features/admin-notifications.md](docs/features/admin-notifications.md) |
| Modal de resumo diário de falhas (admin, 1x/dia/usuário no primeiro load, leva pra /admin/station-failures) | [docs/features/daily-failures-digest-modal.md](docs/features/daily-failures-digest-modal.md) |
| Emails diários de alerta (campanhas iniciando sem material / iniciando / terminando + emissoras >2h fora — janela dias úteis, SMTP Workspace, dedup por dia, opt-out por usuário) | [docs/features/campaign-notification-emails.md](docs/features/campaign-notification-emails.md) |
| Tela de login (`/login`) — hero cinematográfico, layout split, fluxo de auth | [docs/features/login-page.md](docs/features/login-page.md) |
| Relatórios de campanha (CSV consolidado/detalhado, PDF com logo E-monitor) | [docs/features/campaign-reports.md](docs/features/campaign-reports.md) |
| Dashboard de veiculação (`/insights`, admin + cliente, KPIs + 4 charts + export PNG/PDF) | [docs/features/insights-dashboard.md](docs/features/insights-dashboard.md) — **os números caíram em 2026-08-17** (executado deixou de somar `out_slot` + fim do double-count do excedente + impactos passaram a `in_slot + bonus`); comparar com relatório antigo não bate, e é o antigo que estava errado. O doc traz também as **divergências CONHECIDAS E ACEITAS** entre `/insights` e `/campaigns` (modo fornecedor por seleção = R$ 271.179 no investimento de campanhas mistas; consolidado sem valor de bônus; `consolidated_value` mensal lido de 3 jeitos; `total_bonus_value` não renderizado) — **não "conserte" nenhuma delas sem falar com o dono** |
| **Impactos / PMM em qualquer tela ou relatório** (`/insights`, `/detections`, `/campaigns`, `/dashboard`, `/reports/airtime`, CSV/PDF, pós-venda) — PMM no target por (cliente, emissora) | [docs/features/client-target-pmm.md](docs/features/client-target-pmm.md) — desde 2026-08-17 existe **UMA base canônica: `Impactos = PMM × (in_slot + bonus)`**, igual em toda tela e todo exportável (se duas divergirem, é bug). `out_slot` não vale nada (D3) e `out_date` está fora do período — nenhum dos dois é impacto entregue; `in_slot` sozinho esconde a bonificação. Resolução canônica do target: `client_station_pmm[campanha.client_id, station_id]`; **ausência de linha ≠ `pmm_target = 0`** (sem linha fica fora do total e do contador; zero conta e soma nada); CPM no target é SEMPRE dinâmico, mesmo com `fixed_cpm`. **Ao criar qualquer número de impacto novo, use `in_slot + bonus`** — nunca `COUNT(*)` nem só `in_slot` |
| Mapa ao Vivo (`/live-map`, admin + cliente — emissoras monitoradas pulsando no mapa do Brasil + feed de veiculações em tempo real) | [docs/features/live-map.md](docs/features/live-map.md) |
| Dívida técnica Fase 2 (F-01..F-132) | [docs/roadmap/follow-ups-fase2.md](docs/roadmap/follow-ups-fase2.md) — **F-127/F-128 são PRÉ-DEPLOY** da categorização por cota (alcance do backfill retroativo indeciso; deploy tudo-ou-nada 0064+0065); F-129..F-132 são bugs latentes medidos e não corrigidos (zip do pós-venda em UTC, material sem `type_id`, CPM dependente da seleção, veiculação sem linha de pricing) |
| Avaliação E2E do matcher (recomendações 4.x) | [docs/roadmap/detection-evaluation-report.md](docs/roadmap/detection-evaluation-report.md) |
| Comparar detecções com o fornecedor externo / investigar "miss" | [docs/operations/vendor-reconciliation.md](docs/operations/vendor-reconciliation.md) |
| Responder a alerta Prometheus disparado | [docs/runbooks/README.md](docs/runbooks/README.md) (índice por alerta) |
| Postmortem de incidente passado | [docs/incidents/](docs/incidents/) (incident-AAAA-MM-DD-*.md) |

Antes de afirmar "vou consultar X", verifique no header YAML do doc se `status` é `implementado`. Se for `legado` ou `parcialmente-implementado`, o doc é ponto de partida, mas confirme no código antes de agir.

### Estado das branches stacked

Em **2026-05-12 fim do dia**, todas as 4 branches que estavam stacked durante o incidente foram **mergeadas em `master`**: `fix/backup-actually-runs`, `fix/postgres-bind-mount`, `fix/sharing-bidirectional-subset`, `feat/campaign-wizard-foundations`. `origin/master` HEAD em `5937cc0` (ou além — verificar com `git log -1`).

Se você é um agente futuro entrando no projeto, rode `git log --all --not master` para listar o que está fora do master atual. Snapshots históricos: [docs/incidents/state-2026-05-12.md](docs/incidents/state-2026-05-12.md).
