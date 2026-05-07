# Backup, restore-test e tiering de evidências

Este documento operacional cobre os jobs introduzidos para implementar §14.4
(backup do Postgres) e §11.4 (retenção e tiering das evidências) do
`plano_implementacao.md`.

## 1. Backup do Postgres

### 1.1 O que roda

| Job | Trigger | O que faz |
|-----|---------|-----------|
| `infra/scripts/backup.sh` | cron diário, 03:00 BR | `pg_basebackup -F tar -z -X stream` + bundle único + upload pra R2 + rotação local 7 dias |
| `infra/scripts/restore-test.sh` | cron mensal, 04:00 BR (dia 1) | Baixa o último daily, sobe Postgres efêmero, roda smoke queries |
| WAL archiving | contínuo (pelo Postgres) | `archive_command` joga cada WAL pra `s3://$R2_BUCKET/wal/%f` |

Cron file: `infra/cron/postgres-backup.cron`.
Postgres config snippet: `infra/postgres/postgresql.conf.snippet`.

### 1.2 Variáveis de ambiente

```
PGUSER, PGPASSWORD, PGHOST, PGPORT, PGDATABASE
R2_ENDPOINT, R2_BUCKET, R2_ACCESS_KEY, R2_SECRET_KEY
BACKUP_DIR              (default /var/lib/radiocheck/backup)
LOCAL_RETENTION_DAYS    (default 7)
LOCK_FILE               (default /var/lock/radiocheck-pg-backup.lock)
PROM_TEXTFILE_DIR       (opcional — para node_exporter scrape)
```

Todas as variáveis `PG*` são **fail-fast** em ambos `backup.sh` e
`restore-test.sh` — se alguma estiver vazia, o script aborta antes de
qualquer operação. Isso é deliberado: rodar `restore-test.sh` contra o
banco errado (porque algum default mascarou a config faltando) seria
silencioso e perigoso. Defina `PGDATABASE=radiocheck` (ou o nome do banco
em uso) explicitamente no `.env` da máquina de backup.

Em produção mantenha o `.env` da máquina de backup separado do `.env` da
aplicação. R2 usa pares de credenciais distintos por bucket — o de backup
deve ter permissão `Object Read & Write` apenas no `radiocheck-backups`.

#### 1.2.1 Mutex via flock

`backup.sh` adquire um `flock(1)` exclusivo sobre `$LOCK_FILE` na entrada do
script. Se outra instância já estiver rodando (cron sobreposto, retry manual
durante uma execução em andamento), a segunda invocação sai com status `0`
e mensagem `another backup is already running` — sem disparar
`pg_basebackup` em paralelo. Configure o lockfile via `LOCK_FILE` se o
default `/var/lock/radiocheck-pg-backup.lock` não couber no host (ex.: WSL
sem `/var/lock` montado em tmpfs).

### 1.3 Política de retenção

Conforme §14.4 (linha 1581-1599):

| Tier | Janela | Onde fica | Quem faz a retenção |
|------|--------|-----------|---------------------|
| Diário | últimos 7 | local SSD + R2 | `backup.sh` apaga local >7d |
| Diário | últimos 30 | R2 | lifecycle rule no bucket R2 |
| Semanal | últimos 4 | R2 | lifecycle rule no bucket R2 |
| Mensal | últimos 12 | R2 | lifecycle rule no bucket R2 |

> A rotação semanal/mensal é responsabilidade da **lifecycle policy do bucket R2**,
> não do shell script. Configure no painel da Cloudflare:
> - prefixo `postgres/daily/` → retenção 30 dias
> - prefixo `postgres/weekly/` → retenção 8 semanas (cópia carimbada por outra rule)
> - prefixo `postgres/monthly/` → retenção 13 meses
>
> Promoção daily→weekly→monthly: rodar `aws s3 cp` cruzando prefixos no domingo
> (semanal) e dia 1 (mensal). Esses dois copy jobs ainda **não** estão
> automatizados — pendência pra próxima sprint, anotada em `plano_implementacao.md`
> §22.

### 1.4 Rodar um backup manual

```bash
# Como root no host de backup, com /etc/radiocheck/backup.env carregado:
sudo -u radiocheck \
    bash -c 'set -a; . /etc/radiocheck/backup.env; set +a; \
             /opt/radiocheck/infra/scripts/backup.sh'
```

A última linha do stdout é JSON com o resultado:

```json
{"timestamp":"2026-05-07T06:00:43Z","size_bytes":472183091,"duration_seconds":97,"status":"ok","tarball":"/var/lib/radiocheck/backup/radiocheck-20260507-030043.tar.gz"}
```

### 1.5 Restaurar de um backup

```bash
# 1. Listar backups disponíveis no R2:
aws s3 ls s3://$R2_BUCKET/postgres/daily/ \
    --endpoint-url $R2_ENDPOINT

# 2. Baixar o desejado:
aws s3 cp s3://$R2_BUCKET/postgres/daily/radiocheck-20260507-030043.tar.gz . \
    --endpoint-url $R2_ENDPOINT

# 3. Extrair:
mkdir -p restore && tar -xzf radiocheck-20260507-030043.tar.gz -C restore
mkdir -p restore/pgdata && tar -xzf restore/base.tar.gz -C restore/pgdata
mkdir -p restore/pgdata/pg_wal && tar -xzf restore/pg_wal.tar.gz -C restore/pgdata/pg_wal

# 4. Apontar Postgres pra esse data dir, ou subir um container:
docker run -d --name pg-restore \
    -v "$(pwd)/restore/pgdata:/var/lib/postgresql/data" \
    -e POSTGRES_PASSWORD=temp \
    -p 55432:5432 \
    postgres:16-alpine

# 5. Validar:
psql -h localhost -p 55432 -U postgres -d radiocheck -c "SELECT count(*) FROM detections;"
```

Para PITR (point-in-time recovery), copie também os WAL files de
`s3://$R2_BUCKET/wal/` para `restore/pgdata/pg_wal/` e crie um
`recovery.signal` + `restore_command = 'aws s3 cp s3://$R2_BUCKET/wal/%f %p ...'`
no `postgresql.auto.conf`.

### 1.6 Disaster recovery — passos completos

1. Provisionar novo host Postgres (Terraform ou playbook em §14.5).
2. Aplicar `infra/postgres/postgresql.conf.snippet` no novo host.
3. Restaurar o backup mais recente (passos 1-4 acima).
4. Replay dos WAL files até o ponto desejado (PITR).
5. Repointar a `DATABASE_URL` da aplicação no docker-compose / config manager.
6. Reiniciar o serviço `api` — supervisor faz `RestoreActive` automaticamente.
7. Forçar um `restore-test.sh` manual pra validar antes de declarar OK.

### 1.7 RPO / RTO esperados

| Métrica | Alvo | Como atingimos |
|---------|------|----------------|
| RPO (Recovery Point Objective) | ≤ 60 segundos | `archive_timeout=60s` força WAL flush por minuto |
| RTO (Recovery Time Objective) | ≤ 30 minutos | restore-test mensal cronometrado tipicamente em <10min para o DB atual; +20min de buffer pra repointar config |
| Frequência de backup full | 1x/dia | cron 03:00 BR |
| Frequência de restore-test | 1x/mês | cron dia 1, 04:00 BR |

## 2. Tiering de evidências

### 2.1 O que roda

`workers/internal/evidence/tiering.go` é um job em Go embutido no processo
`api`. Ele:

1. Roda na inicialização do `api` (smoke pass).
2. Daí em diante, dispara uma vez por dia entre 03:00 e 03:59 BR (timezone
   configurada explicitamente, não confia em relógio do host).
3. Pode ser disparado manualmente: `POST /v1/internal/admin/evidence/tiering/run`
   (requer JWT com `role=admin`).

### 2.2 Tiers e idades

| Tier | Idade da detecção | Bucket | Storage class |
|------|-------------------|--------|---------------|
| `hot` | 0–30 dias | local / SSD bucket | STANDARD |
| `cold` | 30–365 dias | R2 standard | STANDARD |
| `archive` | >365 dias | R2 IA | STANDARD_IA |

A coluna `detections.tier` (migration 0013) carrega o estado canônico.

### 2.3 Idempotência

A ordem é importante: primeiro PUT no destino, depois HEAD pra confirmar,
depois UPDATE da row, **só depois** DELETE da origem. Se falhar entre o
PUT e o UPDATE, o próximo pass tenta de novo (PUT é overwrite). Se falhar
entre o UPDATE e o DELETE, o objeto fica órfão na origem e o próximo pass
não vai tentar mover (porque o tier já mudou) — o cleanup é feito pelo
`Delete` ser idempotente quando rodado manualmente, ou por uma rotina
futura de "find orphans".

Quando os 3 buckets apontam pro mesmo backend (dev local), o DELETE da
origem é pulado pra não destruir o objeto recém-promovido. O storage class
ainda muda, então a economia em produção (com buckets distintos) continua
real.

### 2.4 Configuração para mover objetos fisicamente

O job de tiering só **move bytes** quando `Hot.Bucket()` ≠ `Cold.Bucket()`
(ou `Cold.Bucket()` ≠ `Archive.Bucket()`). Quando os buckets coincidem — o
caso default em dev/staging com um único MinIO — o tiering vira só uma
flag de coluna no banco (`detections.tier`): o objeto fica no mesmo lugar
e a economia de storage não acontece. Isso é proposital (evita destruir o
objeto recém-promovido), mas exige configuração explícita em produção.

Para que produção realmente economize, defina buckets distintos via env:

```
EVIDENCE_HOT_BUCKET=radiocheck-evidence-hot         # SSD local ou R2 STANDARD
EVIDENCE_COLD_BUCKET=radiocheck-evidence-cold       # R2 STANDARD outra região
EVIDENCE_ARCHIVE_BUCKET=radiocheck-evidence-archive # R2 STANDARD_IA
```

Ou, mantendo um único bucket, troque a **storage class** entre tiers — o
job já chama `PutWithStorageClass(..., "STANDARD_IA")` na promoção
cold→archive (ver `ArchiveStorageClass` em `NewTieringJob`). Nesse modelo,
o backend de storage faz a economia via lifecycle/class rebate, e o
DELETE da origem fica desligado mesmo. Use uma das duas estratégias:
buckets distintos OU classes distintas; misturar as duas é redundante.

Sintoma de configuração errada: gauge
`radiocheck_evidence_storage_bytes{tier="cold"}` cresce, mas o uso real do
bucket hot não diminui. Cheque `kubectl exec api -- env | grep EVIDENCE_`
e compare com o bucket reportado por `radiocheck_storage_*` (se houver) ou
diretamente pelo painel R2.

### 2.5 Reverter tier (cold → hot)

Não é automatizado. Procedimento manual:

```sql
-- 1. Marca a detecção pra ser tratada como hot novamente.
UPDATE detections SET tier = 'hot' WHERE id = '<uuid>';
```

```bash
# 2. Copiar o objeto de volta pro bucket hot.
aws s3 cp s3://radiocheck-cold/<key> s3://radiocheck-hot/<key> \
    --endpoint-url $R2_ENDPOINT
aws s3 rm s3://radiocheck-cold/<key> --endpoint-url $R2_ENDPOINT
```

Use só pra incidentes (cliente reclamou, auditoria solicitou). Não é fluxo
normal.

### 2.6 Forçar tiering manual

```bash
# pega um JWT admin
TOKEN=$(curl -s -X POST -H 'content-type: application/json' \
    -d '{"email":"admin@radiocheck","password":"..."}' \
    http://api.radiocheck/v1/internal/auth/login | jq -r .token)

curl -X POST -H "Authorization: Bearer $TOKEN" \
    http://api.radiocheck/v1/internal/admin/evidence/tiering/run
```

Resposta esperada:

```json
{"status":"ok","duration_ms":12453}
```

### 2.7 Métricas

Expostas pelo `api` em `/metrics`:

- `radiocheck_evidence_tier_movements_total{from,to}` — counter de movimentações.
- `radiocheck_evidence_storage_bytes{tier}` — gauge agregado de bytes por tier.
- `radiocheck_evidence_tiering_last_run_timestamp` — gauge unix epoch.
- `radiocheck_evidence_tiering_errors_total` — counter de erros não-fatais.

Alerta `EvidenceTieringStalled` em `infra/prometheus/alerts.yml` dispara se o
job não rodar nas últimas 48h.

### 2.8 Follow-ups

- **Migration 0013 — `CREATE INDEX CONCURRENTLY`.** A migration cria
  `detections_tier_created_at_idx` em transação (default da ferramenta de
  migração). Em PoC e Fase 2 isso é aceitável porque a tabela ainda é
  pequena. Em produção, com a tabela passando de ~10M linhas, considere
  rodar manualmente um `CREATE INDEX CONCURRENTLY` antes do deploy da
  migration e ajustar a tool para aceitar `--no-transaction` na 0013
  específica. Comentário com TODO está deixado no próprio arquivo SQL.
- **Promoção daily→weekly→monthly.** Ainda manual via `aws s3 cp`
  cruzando prefixos; ver §1.3.
- **Limpeza de objetos órfãos no bucket hot** quando o move falha entre
  UPDATE e DELETE — ver §2.3.

## 3. Onde os scripts rodam

| Script | Host | Trigger |
|--------|------|---------|
| `backup.sh` | host de backup dedicado (`backup-01` em §14.1) | cron `radiocheck-postgres` |
| `restore-test.sh` | mesmo host | cron mensal |
| Tiering job (Go) | dentro do container `api` | scheduler interno + endpoint admin |

Nenhum dos dois lê dados sensíveis: o `backup.sh` se conecta no Postgres como
um usuário com role `pg_read_all_data` + `REPLICATION` (basta pra
`pg_basebackup`); o `restore-test.sh` opera num container Postgres efêmero
isolado por rede.

## 4. Como as métricas chegam no Prometheus

- **Tiering** (Go): exposto direto em `/metrics` no serviço `api`, scrape padrão.
- **Backup / restore-test** (shell): escrito via textfile-collector. Veja
  `infra/prometheus/textfile-collector/README.md`. O `node_exporter` precisa
  rodar com `--collector.textfile.directory=/var/lib/node_exporter/textfile`,
  e os scripts precisam ter `PROM_TEXTFILE_DIR` apontando pro mesmo diretório.

Decisão arquitetural: **textfile** em vez de **pushgateway** porque
(1) o `node_exporter` já roda no host de backup como parte do baseline,
(2) restore-test é mensal — pushgateway daria a falsa sensação de "última
métrica boa" entre execuções, mascarando falhas, e
(3) zero novos componentes na infra.
