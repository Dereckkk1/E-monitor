# Incidente 2026-05-12 — Quase-perda do pgdata (e camadas de defesa que ficaram)

> **CORREÇÃO IMPORTANTE (2026-05-12 fim do dia):** O dado **nunca foi
> destruído**. Sobreviveu intacto em `/mnt/db/pgdata` no host o tempo
> todo, via bind mount declarado em `infra/docker/docker-compose.override.yml`
> (gitignored — só existe na VM). O `deploy.sh` em prod sempre usou esse
> override. Meus comandos manuais de diagnóstico (apenas
> `-f docker-compose.yml`) usaram o `pgdata` named volume do base yml —
> um volume DIFERENTE, vazio. Operamos por horas como se tivéssemos
> perdido tudo, mas a base estava lá. Quando o usuário rodou `deploy.sh`,
> compose voltou pro override → dado original ressurgiu. As 9 camadas
> de defesa adicionadas durante a "recovery" continuam todas valiosas
> porque agora o bind mount é **explícito e versionado** no base yml,
> backup funciona, snapshot GCP existe, alerta Prometheus dispara.
> Defesa em profundidade ficou redundante — e redundância é justamente
> o objetivo. Detalhes em "§Causa raiz #5 — Override file não
> documentado" abaixo.

## Resumo executivo

Durante um redeploy de rotina do service `api`, o comando
`docker compose up -d --force-recreate api` (sem `--no-deps`, sem
override file) levou compose a usar o `pgdata` named volume vazio do
base yml em vez do bind mount `/mnt/db/pgdata` que prod usa em
operação normal via override file. Sistema continuou rodando, mas
apontando para uma base "fantasma" — limpa, sem dados.

Backup automático **nunca rodou em produção** desde que o sistema foi
ao ar: o container `backup` tinha dois bugs de configuração que faziam
o script abortar silenciosamente em toda iteração. O R2 estava vazio.
Não havia base backup nem WAL archive utilizáveis. **Se** a perda
tivesse sido real (caso o override file também tivesse sido apagado),
não haveria recovery possível.

O que pareceu "recovery" foi na verdade `DROP SCHEMA public CASCADE`
+ `migrate` aplicando 15 migrations no named volume vazio. Operador
recadastrou parte do catálogo nesse estado. **Esse recadastro ficou
orfão** quando o usuário rodou `deploy.sh` algumas horas depois — o
override voltou a apontar pro bind mount original, dado pré-incidente
ressurgiu, e os cadastros da janela "fake recovery" ficaram presos
no named volume `docker_pgdata` (não acessível pelo sistema rodando).

Mitigação operacional: o fornecedor externo continua espelhando as
veiculações em paralelo (§17 do plano), preservando o registro
operacional para os clientes durante o período de coexistência.

## Linha do tempo (UTC)

| Quando | Evento |
|--------|--------|
| 2026-05-12 11:21:10 | `docker compose -f docker-compose.yml up -d --force-recreate api` rodado sem `--no-deps` **e sem o override file**. `--force-recreate` propaga para dependências. Compose vê o postgres só com `pgdata` named volume (do base yml) → cria volume novo vazio. **O bind mount real em `/mnt/db/pgdata` continua intacto no host, só não está sendo montado.** |
| 2026-05-12 11:21:20 | Postgres inicializou na named volume vazia via `docker-entrypoint-initdb.d`, executando arquivos da pasta `migrations/` em ordem alfabética. Cada `.down.sql` falhou (tabelas ainda não existiam), cada `.up.sql` parcialmente bem-sucedido. Estado final: ~50 tabelas presentes mas `schema_migrations.dirty=true` na versão 1, e a tabela `users` (auth, migration 0007) ausente. |
| 2026-05-12 11:23:14 | Postgres detectou shutdown sujo, fez WAL recovery a partir do nada, ficou ready. |
| 2026-05-12 11:28 | Tentativa de continuar deploy. `api` em crash-loop com `ERROR: relation "users" does not exist`. |
| 2026-05-12 11:28 | Confirmado: 0 rows em `detections`, `commercials`, `stations`, `fingerprint_hashes` — **na named volume nova**. Nem o agente nem o operador olharam pro bind mount em `/mnt/db/pgdata` (que ninguém sabia que existia). |
| 2026-05-12 11:30 | Procura por backups: `docker_backup-data` vazio, `docker_pg_archive` vazio, R2 bucket `radiocheck-backups` vazio em todos os prefixes. |
| 2026-05-12 11:34 | Identificada causa do backup nunca rodar (ver §"Causa raiz #2" abaixo). |
| 2026-05-12 11:35 | "Recovery" (na realidade, ressuscitação no volume errado): `DROP SCHEMA public CASCADE` + `docker compose up migrate`. 15 migrations aplicadas em sequência. |
| 2026-05-12 11:37 | `api` voltou ao ar com schema completo na named volume vazia. Bootstrap admin recriado. |
| 2026-05-12 11:38-15:00 | Sequência de defesas adicionadas: backup container fixado (F-109), bind mount adicionado ao base yml (F-116), tzdata (F-114), RADIOCHECK_ENV parametrizado (F-115), node-exporter + alerta (F-110), snapshot GCP configurado (F-117), branches pushadas pro GitHub. Operador começou recadastro do catálogo no named volume vazio. |
| 2026-05-12 ~15:30 | Usuário rodou `./scripts/deploy.sh` (que inclui `-f docker-compose.override.yml`). Compose recreou containers usando o override → postgres voltou a apontar pro bind mount `/mnt/db/pgdata`. Dado original **ressurgiu**. Usuário relata "meus dados voltaram kkkkkk". |
| 2026-05-12 ~16:00 | Override file inspecionado, mistério resolvido. Postmortem corrigido. |

## Causa raiz #1 — `--force-recreate` propagou para dependências

`docker compose up -d --force-recreate api` recreates `api` **e suas
dependências declaradas em `depends_on`**, exceto quando `--no-deps` é
passado. No nosso caso isso incluiu `postgres`, `redis`, `minio`,
`nats`. O `--force-recreate` em `postgres` (que tem volume nomeado
`pgdata`) provavelmente acionou alguma corrida ou inconsistência que
fez o Docker considerar o volume divergente da config e recriar o
volume do zero.

**O comportamento exato não é totalmente reproduzível** (volumes
nomeados normalmente sobrevivem a recreates), mas o efeito foi
inequívoco: `CreatedAt` do volume `docker_pgdata` é `2026-05-12T11:21:10Z`
— exatamente o momento do comando.

A documentação do docker compose alerta: "Use `--no-deps` to recreate
a service without recreating its dependencies." Essa flag deveria ser
obrigatória em qualquer operação de recreate envolvendo serviços
stateful em produção.

## Causa raiz #2 — Backup automático nunca rodou

O service `backup` (Postgres physical backup via `pg_basebackup`,
upload para Cloudflare R2) estava deployed mas **silenciosamente
inoperante** desde o dia em que subiu (2026-05-08).

Dois bugs de configuração:

**Bug A — entrypoint com ordem invertida:**

```yaml
entrypoint: ["sh", "-c", "while true; do sleep 86400; sh /backup.sh; done"]
```

Faz `sleep 86400` (24h) **antes** do primeiro backup. Em ambientes
onde o container reinicia frequentemente (dev, deploys, reinício do
host), nenhum backup chega a ser executado.

**Bug B — env vars faltando:**

A definição do service só plumbava `DATABASE_URL` e `BACKUP_DIR`. O
script `backup.sh` exige `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD`,
`PGDATABASE` (para `pg_basebackup`) e `R2_ENDPOINT`, `R2_BUCKET`,
`R2_ACCESS_KEY`, `R2_SECRET_KEY` (para upload). O `set -euo pipefail`
+ `: "${VAR:?...}"` no início do script faria o script abortar
imediatamente — mas o `while true; do ...; done` engole o exit code
e silenciosamente continua a próxima iteração. Sem logs visíveis. Sem
métrica. Sem alerta.

**Bug C (corolário) — falta o cliente AWS:**

A imagem `postgres:16-alpine` não traz `aws-cli`. O script tenta
`aws s3 cp` direto. Mesmo se as vars estivessem corretas, o comando
falharia com `aws: command not found`.

## Causa raiz #3 — Conflito entre `initdb.d` e migrate service

O `docker-compose.yml` monta `migrations/` em **dois lugares**:

```yaml
postgres:
  volumes:
    - ../../migrations:/docker-entrypoint-initdb.d:ro
migrate:
  volumes:
    - ../../migrations:/migrations:ro
```

Quando o pgdata é inicializado do zero, o postgres executa todos os
`.sql` files da pasta `initdb.d` em ordem alfabética — **incluindo os
`.down.sql`**. Como down vem antes de up alfabeticamente
(`0001_initial.down.sql` antes de `0001_initial.up.sql`), cada down
tenta dropar coisas que ainda não existem, gera erro, e o próximo up
parcialmente recupera. O resultado é um schema fragmentado com
`schema_migrations.dirty=true` na primeira versão.

O `migrate` service (golang-migrate) então detecta o estado dirty e
**se recusa a rodar**, deixando o sistema sem as migrations posteriores
aplicadas — especificamente `0007_fase2_auth.up.sql` que cria a tabela
`users` necessária para o boot do `api`.

Esse é um bug latente que esperava o próximo recovery: enquanto o
pgdata era preservado, `initdb.d` não rodava (só executa em DB
inicial). A perda do volume disparou.

## Causa raiz #4 — Sem monitoramento da existência de backups

Mesmo sem o bug A/B, não havia alerta para detectar que o último
backup tinha mais de 48h. O script tem suporte a `PROM_TEXTFILE_DIR`
para escrever métricas, mas a config não estava habilitada e não havia
alert rule no Prometheus correspondente.

## Causa raiz #5 — Override file de prod não documentado/versionado

`infra/docker/docker-compose.override.yml` existe na VM com bind mounts
para `/mnt/db/pgdata`, `/mnt/data/minio`, `/mnt/data/masters` e
`/mnt/data/audio-refs`. **O arquivo está gitignored** (via
`infra/docker/.env` + glob — nenhum *.override.yml no repositório) e
nunca foi mencionado em CLAUDE.md, deploy.md ou qualquer documentação
operacional.

O `deploy.sh` em prod sempre incluiu `-f docker-compose.override.yml`
no comando docker. Operações normais via deploy.sh funcionavam
corretamente. **Comandos manuais (`docker compose -f docker-compose.yml`
sem o override) usavam silenciosamente uma configuração diferente.**

O agente investigando o incidente (eu) não tinha como saber do
override sem perguntar explicitamente — e perguntei tarde demais. O
operador também não pensou em mencionar porque era infra "óbvia"
da rotina dele.

**Mitigação adotada:** o base yml agora declara bind mount via env
vars (`PGDATA_HOST_PATH` etc, F-116). Mesmo sem o override file, prod
fica corretamente bindado se o `.env` setar a var. O override file
continua existindo para compatibilidade com o deploy.sh atual, mas é
agora redundante — pode ser deletado em uma limpeza futura.

## O que foi *realmente* perdido vs. o que pareceu perdido

| Categoria | Pareceu Perdido | Realidade |
|-----------|-----------------|-----------|
| `detections` (histórico de veiculações) | ✘ | ✓ **Preservado** em `/mnt/db/pgdata` |
| `fingerprint_hashes` | ✘ | ✓ Preservado |
| `commercials` (metadata) | ✘ | ✓ Preservado |
| `stations` (~33 emissoras) | ✘ | ✓ Preservado |
| `campaigns`, `clients` | ✘ | ✓ Preservado |
| `station_thresholds` (calibração) | ✘ | ✓ Preservado |
| `stream_health_events` | ✘ | ✓ Preservado |
| `webhook_deliveries` | ✘ | ✓ Preservado |
| Evidências em S3 (MinIO) | ✘ | ✓ Preservadas em `/mnt/data/minio` |
| Audio masters (mp3) | ✓ Preservado em volume nomeado | ✓ Também em `/mnt/data/masters` |
| **Recadastro feito durante a "fake recovery"** | n/a | **Orfão no named volume `docker_pgdata`** — não acessível pelo sistema rodando |
| Código | ✓ | ✓ |

A janela de "fake recovery" durou ~4h (11:35 → 15:30 UTC). Qualquer
cadastro feito pelo operador nesse período está no named volume vazio
que ninguém mais monta. Recuperação possível mas trabalhosa: criar
um postgres efêmero apontando pra `docker_pgdata`, exportar via
`pg_dump`, restore parcial no postgres real. Operador relatou que o
recadastro feito foi mínimo, então provavelmente não vale o esforço.

## Recovery aplicado

```bash
# 1. Limpa o estado fragmentado deixado pelo initdb.d
docker compose -f infra/docker/docker-compose.yml exec -T postgres \
  psql -U radiocheck -d radiocheck -c "
    DROP SCHEMA public CASCADE;
    CREATE SCHEMA public;
    GRANT ALL ON SCHEMA public TO radiocheck;
    GRANT ALL ON SCHEMA public TO public;
  "

# 2. Aplica as 15 migrations via golang-migrate
docker compose -f infra/docker/docker-compose.yml up migrate

# 3. Reinicia a api (bootstrap admin recriado automaticamente)
docker compose -f infra/docker/docker-compose.yml restart api
```

Resultado: 55 tabelas (incluindo as partitions), `users` existe,
bootstrap admin criado, api listening em :8080.

## Lições

0. **Configuração de prod precisa ser versionada (ou pelo menos
   documentada).** Arquivos críticos como override files do compose
   que estão presentes apenas na VM e ausentes do git são uma armadilha
   esperando vítimas. Quando algo "estranho" acontecer em prod, agentes
   e operadores precisam de informação suficiente pra reproduzir o
   estado mentalmente sem ter que adivinhar. Tornou-se a regra mais
   importante: **nenhum comportamento operacional de prod pode ser
   invisível pra leitor do repositório.**

1. **`docker compose up --force-recreate` sem `--no-deps` em produção
   é deploy destrutivo.** Stateful services (postgres, minio, redis)
   nunca devem ser recriados como "efeito colateral" de um deploy de
   service stateless. **E sempre incluir o override file ao operar em
   prod**, ou o compose pode silenciosamente usar uma config diferente
   da rodando — exatamente o que causou a confusão deste incidente.

2. **"Backup configurado" ≠ "backup funcionando".** Sem teste end-to-end
   (script roda → arquivo aparece no destino) e sem alerta de
   ausência de backup, qualquer falha silenciosa torna o sistema
   inteiro vulnerável a perda total. O incidente foi catastrófico
   apenas porque o backup nunca tinha rodado uma vez.

3. **`initdb.d` + migrate service no mesmo Postgres é footgun.** Os
   dois mecanismos têm contratos diferentes (initdb.d roda todos os
   .sql na ordem alfabética, sem state tracking; migrate aplica
   pendentes via `schema_migrations`) e juntos produzem estados que
   nenhum dos dois consegue racionalizar.

4. **`while true; do ... done` em containers de produção precisa de
   tratamento explícito de erro.** Engole exits, máscara silenciosa.
   Alternativas melhores: cron real, ou `set -e` no entrypoint para
   o container morrer e o restart-policy do compose lidar.

## Follow-ups

- **F-109 — *RESOLVIDO* — Backup container reescrito.** Branch
  [`fix/backup-actually-runs`](../infra/docker/docker-compose.yml).
  Inverter ordem entrypoint, plumbar todas as env vars (PG* + R2_*),
  instalar `aws-cli` via `apk add` no entrypoint, `depends_on`
  exigindo `service_healthy` em vez de `service_started`.
  Adicionalmente: trocado `pg_basebackup` (que exige replication
  config no postgres) por `pg_dump -Fc` (apenas SELECT, mais
  portável). Verificação em prod 2026-05-12 12:44 UTC:
  `s3://radiocheck-backups/postgres/daily/radiocheck-20260512-084414.dump`
  (177KB, primeiro backup com sucesso da história do sistema).
- **F-110 — *RESOLVIDO* — Alerta Prometheus de ausência de backup.**
  Branch [`fix/postgres-bind-mount`](../infra/prometheus/alerts.yml)
  (commit `80c1e0f`). Novo service `node-exporter` no compose com
  `--collector.textfile.directory=/textfile`, volume compartilhado
  `prometheus-textfile` entre `backup` (rw) e `node-exporter` (ro).
  `backup.sh` escreve métricas via `PROM_TEXTFILE_DIR=/textfile`.
  Prometheus scrapeia `node-exporter:9100`. Alerta
  `BackupNotRunning` (existente, fica passivo sem dado) +
  `BackupMetricMissing` (novo, dispara via `absent()` se a métrica
  nunca apareceu — protege contra o cenário exato de 2026-05-12,
  onde o pipeline estava quebrado em silêncio).
- **F-111 — Separar `migrations/` em `initdb.d` vs `migrate`.**
  Pendente. O ideal é remover totalmente o mount em
  `postgres.volumes` e deixar só o `migrate` service. Postgres sobe
  vazio sempre, migrate é source of truth. Alternativa: mover
  migrations para `migrations/up/` e mount só essa subpasta (sem os
  `.down.sql`) em initdb.d. Mitigado por enquanto pela regra §4.6 do
  CLAUDE.md ("se DB vier suja, dropa schema antes de rodar migrate").
- **F-112 — Política `--no-deps` em todos os deploys**. Pendente como
  script (`scripts/deploy-service.sh`), mas já codificado como regra
  §4.1 do CLAUDE.md. Todo agente futuro lê isso antes de mexer.
- **F-113 — Backup do MinIO/S3** (evidências). Pendente. Hoje o
  `backup.sh` cobre apenas o Postgres. Em prod futura (object storage
  cloud), o provider já replica; pra MinIO local valeria um
  `mc mirror` para R2 periódico. Mitigado por F-116 (bind mount em
  `miniodata` impede compose de apagar).
- **F-114 — *RESOLVIDO* — Imagem do api inclui `tzdata`.** Branch
  [`fix/postgres-bind-mount`](../infra/docker/Dockerfiles/workers.Dockerfile)
  (commit `9fddea3`). `apk add tzdata` no stage final. Próximo build
  da imagem do api elimina o warning de timezone no startup.
- **F-115 — *RESOLVIDO* — `RADIOCHECK_ENV` parametrizado.** Branch
  [`fix/postgres-bind-mount`](../infra/docker/docker-compose.yml)
  (commit `9fddea3`). Compose passou de hardcoded `development` para
  `${RADIOCHECK_ENV:-development}`. Prod seta `RADIOCHECK_ENV=production`
  no `.env`. Vira o `deployment.environment` attribute de OTel e
  base para guards futuros de "este é prod, não roda comando
  destrutivo".
- **F-116 — *RESOLVIDO (código) / Pendente (deploy)* — Bind mount em
  pgdata, miniodata e mastersdata.** Branch
  [`fix/postgres-bind-mount`](../infra/docker/docker-compose.yml)
  (commit `b553f56`). Compose aceita 3 env vars opcionais:
  `PGDATA_HOST_PATH`, `MINIODATA_HOST_PATH`, `MASTERSDATA_HOST_PATH`.
  Quando setadas, usa bind mount no host (dado sobrevive a
  `docker compose down -v` e `--force-recreate`). Default = volume
  nomeado (dev local). Script de migração
  [`infra/scripts/migrate-volumes-to-bind.sh`](../infra/scripts/migrate-volumes-to-bind.sh)
  automatiza a transição. Documentação completa em
  [data-durability.md](data-durability.md).
- **F-117 — *RESOLVIDO (operacional)* — Snapshot diário do disco da
  VM no GCP.** Schedule `default-schedule-1` criado no GCP Console,
  retenção 14 dias, executa 04:00 UTC. Primeira execução: 2026-05-13.
  Independente das outras camadas de defesa — protege contra
  ransomware na VM, erro humano `rm -rf` no host, e VM deletada.
- **F-118 — Restore drill mensal.** Pendente. Script
  `infra/scripts/restore-drill.sh` que: (1) baixa o backup mais
  recente do R2, (2) sobe postgres efêmero, (3) restora, (4) roda
  smoke-test (`SELECT COUNT(*)` em 3 tabelas), (5) destrói. Sem
  isso, sabemos que backups existem mas não que são restoráveis. Ver
  [data-durability.md §Restore drill](data-durability.md).

## Recadastro do catálogo

Os 18 masters mp3 estão preservados em `docker_mastersdata` com nomes
UUID. Para evitar re-upload + esperar fingerprint gerar:

```bash
# Lista os UUIDs existentes
docker run --rm -v docker_mastersdata:/data alpine ls /data
```

Duas opções:

1. **Re-upload pela UI** (simples, gera novos UUIDs, fingerprint roda
   automático via NATS).
2. **INSERT manual** preservando os UUIDs existentes (evita
   re-upload), depois publicar `fingerprint.generate` no NATS para
   cada commercial. Requer correlacionar UUID com título manualmente
   (escutar cada mp3, ou cruzar com lista do fornecedor antigo).

Recomendação: opção 1, mais simples e idempotente. Os masters antigos
em `docker_mastersdata` ficam órfãos e podem ser limpos depois
(`docker volume rm` ou deixar para o próximo `down -v`).

## Estado de recuperação (atualizado 2026-05-12 fim do dia)

### Camadas de defesa ativas em prod

| Camada | Protege contra | Status |
|--------|---------------|--------|
| Bind mount `/srv/radiocheck/*` | `docker compose --force-recreate`, `down -v` | ✅ Deployed |
| `pg_dump` diário no Cloudflare R2 | Disco da VM falhar, DROP errado, corrupção | ✅ Rodando (1ª execução 12:44 UTC, 177KB) |
| Snapshot diário disco GCP | VM deletada, ransomware, `rm -rf` no host | ✅ Schedule criado |
| Alerta Prometheus `BackupNotRunning` (>26h) | Backup quebrar silenciosamente | ✅ Pipeline ativo |
| Alerta `BackupMetricMissing` (`absent()`) | Pipeline de métricas quebrar | ✅ Pipeline ativo |

A combinação **bind mount + R2 + snapshot GCP** = três cópias
independentes em três tecnologias diferentes. Perda de dado agora
exige falha simultânea em três planos separados.

### Doc relacionado

- [data-durability.md](data-durability.md) — modelo completo de
  ameaças, runbooks de recovery, procedimento de migração
  named-volume → bind-mount, restore drill (pendente).
- [incident-2026-05-09-jingle-falsepos.md](incident-2026-05-09-jingle-falsepos.md)
  — incidente da véspera (falso-positivo de AMBIENTAL JINGLE) cuja
  saga de fixes (F-108 v1→v2→v3) acabou expondo o problema do
  backup e levou a este incidente.
- [shared-hash-detection.md](shared-hash-detection.md) — algoritmo de
  shared-hash em sua forma final pós-2026-05-12.
- `CLAUDE.md` §4 — regras críticas operacionais para qualquer
  agente futuro, derivadas deste incidente.
