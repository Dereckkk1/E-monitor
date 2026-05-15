---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - infra/docker/docker-compose.yml
  - infra/scripts/backup.sh
  - infra/scripts/migrate-volumes-to-bind.sh
  - infra/prometheus/alerts.yml
  - workers/internal/supervisor/supervisor.go
---

# Durabilidade de dados

Como Radiocheck protege os dados de cada classe de falha — qual camada
cobre o quê, qual ainda é gap, e procedimentos de recuperação.

> Motivação: incidente 2026-05-12 destruiu 100% do `pgdata`. Postmortem
> em [`incident-2026-05-12-pgdata-loss.md`](../incidents/incident-2026-05-12-pgdata-loss.md).
> Este doc é o produto da análise, mapeando defesa em profundidade contra
> reincidência.

## Modelo de ameaças

| Falha | Probabilidade | Severidade |
|-------|---------------|-----------|
| `docker compose down -v` / `--force-recreate` | Alta (operador humano) | Catastrófica |
| Bug do postgres (corrupção) | Baixa | Catastrófica |
| Disco da VM falha | Média (cloud) | Catastrófica |
| VM deletada por engano (console GCP) | Baixa | Catastrófica |
| `DROP DATABASE` / `TRUNCATE` errado | Média | Catastrófica |
| Ransomware na VM | Baixa | Catastrófica |
| Postgres OOM no meio de um write | Baixa | Recuperável |

## Camadas de defesa

| Camada | Protege contra | Status | Onde |
|--------|---------------|--------|------|
| Bind mount no host (data fora de volume nomeado) | `down -v`, `--force-recreate` | Implementado (este branch) | `infra/docker/docker-compose.yml` + `.env` |
| Backup pg_dump diário no R2 | Disco VM, corrupção, DROP errado | Funcionando desde 2026-05-12 | `infra/scripts/backup.sh` |
| Snapshot diário do disco GCP | Tudo acima + VM deletada + ransomware | **Pendente — configurar no console GCP** | n/a (provider feature) |
| Alerta Prometheus de backup ausente | Backup quebrar silenciosamente | **F-110, pendente** | `infra/prometheus/alerts.yml` |
| Restore drill mensal | Backup existir mas não funcionar | **Pendente — operacional** | Procedimento §"Restore drill" abaixo |

A combinação **bind mount + R2 + snapshot GCP** = três cópias independentes
em três tecnologias diferentes (filesystem do host, object storage da CF,
disco da GCP). Perda de dado requer falha simultânea em três planos
separados — improvável.

## Camada 1 — Bind mount no host

### Como funciona

O `docker-compose.yml` aceita 3 env vars opcionais:

```bash
PGDATA_HOST_PATH=/srv/radiocheck/postgres-data
MINIODATA_HOST_PATH=/srv/radiocheck/minio-data
MASTERSDATA_HOST_PATH=/srv/radiocheck/masters-data
```

Quando setadas, o postgres/minio/api passam a usar bind mount em vez de
volume nomeado. Bind mounts são paths normais do host — `docker compose
down -v` não toca, `--force-recreate` não toca, `docker volume rm` não
alcança.

Quando NÃO setadas, cai no volume nomeado (`pgdata`, `miniodata`,
`mastersdata`). Esse é o default para dev local (mais simples no
Windows/macOS sem `/srv`).

### Migração (dev → prod com bind)

Script automatizado em `infra/scripts/migrate-volumes-to-bind.sh`. Roda
em modo dry-run primeiro pra inspecionar:

```bash
sudo TARGET_BASE=/srv/radiocheck bash infra/scripts/migrate-volumes-to-bind.sh --dry-run
```

Confirma o destino, então:

```bash
sudo TARGET_BASE=/srv/radiocheck bash infra/scripts/migrate-volumes-to-bind.sh
```

O script:
1. Para api/postgres/minio/fingerprint/backup/radio-sim
2. Cria `/srv/radiocheck/{postgres-data,minio-data,masters-data}` com
   ownership correto (UID 70 pro postgres, 1000 pro minio, 0 pro api)
3. Copia conteúdo dos volumes nomeados via container alpine temporário
   (`cp -a` preserva permissões)
4. Imprime as linhas para adicionar ao `.env`
5. Pede confirmação manual antes de remover os volumes antigos

### Verificação pós-migração

```bash
docker compose -f infra/docker/docker-compose.yml up -d
docker compose -f infra/docker/docker-compose.yml ps

# Conta linhas críticas pra confirmar que os dados vieram
docker compose -f infra/docker/docker-compose.yml exec -T postgres \
  psql -U radiocheck -d radiocheck -c "
    SELECT 'stations' tbl, COUNT(*) FROM stations
    UNION ALL SELECT 'commercials', COUNT(*) FROM commercials
    UNION ALL SELECT 'detections', COUNT(*) FROM detections;
  "

# Inspeciona o conteúdo direto no host
sudo du -sh /srv/radiocheck/*
sudo ls /srv/radiocheck/postgres-data/ | head -5  # deve mostrar base/, global/, pg_wal/, ...
```

### Cleanup dos volumes antigos (depois de verificar)

```bash
docker volume rm docker_pgdata docker_miniodata docker_mastersdata
```

**Só depois** de confirmar via query que o sistema enxerga os dados
migrados.

## Camada 2 — Backup pg_dump diário no R2

Coberto em detalhe em [`backup-and-retention.md`](backup-and-retention.md).
Resumo:

- Container `backup` roda o script `infra/scripts/backup.sh` imediatamente
  após startup, depois a cada 24h
- `pg_dump -Fc --no-owner --no-acl` produz um `.dump` em `/backup/`
- Upload pra `s3://radiocheck-backups/postgres/daily/`
- Retenção local: 7 dias
- Retenção remota: sem limite hoje (configurar lifecycle rule no R2 para
  expirar após 90d como follow-up)

### Restore a partir de backup

```bash
# 1. Baixa o dump mais recente
source infra/docker/.env
mkdir -p /tmp/restore
docker run --rm \
  -e AWS_ACCESS_KEY_ID="$R2_ACCESS_KEY" \
  -e AWS_SECRET_ACCESS_KEY="$R2_SECRET_KEY" \
  -v /tmp/restore:/restore \
  amazon/aws-cli \
  --endpoint-url "$R2_ENDPOINT" \
  s3 cp "s3://$R2_BUCKET/postgres/daily/radiocheck-YYYYMMDD-HHMMSS.dump" /restore/

# 2. Restora no postgres rodando
docker cp /tmp/restore/radiocheck-*.dump docker-postgres-1:/tmp/restore.dump
docker compose -f infra/docker/docker-compose.yml exec -T postgres \
  pg_restore -U radiocheck -d radiocheck --clean --if-exists /tmp/restore.dump

# 3. Restart api pra recarregar índices em memória
docker compose -f infra/docker/docker-compose.yml restart api
```

## Camada 3 — Snapshot diário do disco GCP

**Pendente — configurar no console GCP.** Passo-a-passo:

1. Console GCP → Compute Engine → Disks
2. Localize o disco da VM `vm-e-monitor` (provavelmente
   `vm-e-monitor` ou similar)
3. Edit → Snapshot schedule → Create new schedule
4. Settings:
   - **Name:** `radiocheck-daily`
   - **Region:** mesma da VM (southamerica-east1, etc.)
   - **Frequency:** Daily, 04:00 BRT (07:00 UTC) — fora do pico de
     veiculação (05:00–23:00) e antes do backup pg_dump (que roda quando
     o container backup tiver 24h de uptime)
   - **Retention:** 14 days (ou 30 — custa centavos)
   - **Source disk type:** Standard Persistent Disk (PD-SSD se houver
     tier diferente)
5. Apply

Custa ~$0,03 por GB-mês. Para um disco de 50GB, ~$1,50/mês para 14 snapshots.

### Restore a partir de snapshot

Console GCP → Snapshots → seleciona → Create Disk from snapshot →
anexa o novo disco à VM (ou cria nova VM). Tempo típico de restore:
minutos.

## Camada 4 — Alerta Prometheus de backup ausente

**F-110, pendente.** Plano:

1. Habilitar `PROM_TEXTFILE_DIR` no service `backup` (env var aponta para
   um volume compartilhado com o `node_exporter`)
2. Adicionar regra em `infra/prometheus/alerts.yml`:

   ```yaml
   - alert: PostgresBackupStale
     expr: time() - radiocheck_postgres_backup_last_success_timestamp > 48 * 3600
     for: 30m
     labels:
       severity: critical
     annotations:
       summary: "Postgres backup mais recente tem >48h"
       description: "Último backup com sucesso foi {{ $value | humanizeDuration }} atrás. Recovery possível mas reduzida — investigar e re-rodar manualmente."
   ```

3. Adicionar rota no Alertmanager para receber e mandar pro canal de
   incidente.

## Restore drill

Procedimento mensal para garantir que os backups são REALMENTE
restoráveis (não basta o arquivo existir):

1. Baixa o backup mais recente do R2
2. Sobe um postgres efêmero em container isolado
3. Roda `pg_restore`
4. Roda smoke-test (`SELECT COUNT(*)` em 3 tabelas críticas, conferir
   que os números fazem sentido)
5. Para o container, descarta

Script automatizável em `infra/scripts/restore-drill.sh` (follow-up).

## Resumo executivo

> **Camadas implementadas:** bind mount (este branch) + backup R2 (desde
> 2026-05-12 12:44 UTC). Combinadas, cobrem 80% dos cenários de perda.
>
> **Pendente para 100%:** snapshot GCP (10 min de setup no console),
> alerta Prometheus (F-110), restore drill (operacional).
>
> **Risco residual aceitável:** bug do postgres que corrompe o file
> sem disparar alerta — backup ainda existe e funciona, mas o último
> bom pode estar com 24h de defasagem. Mitigado pelo snapshot do disco
> GCP (que captura o estado pré-corrupção).
