---
status: implementado
ultima-verificacao: 2026-06-03
codigo-relacionado:
  - infra/docker/docker-compose.yml
  - infra/docker/.env.example
  - workers/internal/segments/segments.go
---

# Mover os segmentos de evidência para `/mnt/data` (tirar do disco de OS)

## Contexto

O ffmpeg grava segmentos ADTS-AAC rotativos por emissora em `/data/segments/<station>/`
(buffer de evidência). O sidecar `segments-cleanup` apaga `*.aac` com mais de 60min
a cada 5min, então o volume é **bounded** — mas o tamanho em regime é
`#emissoras × ~60min de buffer`. Com **118 emissoras** isso dá **~5GB**, e rumo às
200 vai a **~8.5GB**.

O problema não é o tamanho (é dado vivo legítimo, o cleanup funciona) — é o **disco**.
O volume `segmentsdata` era um named volume em `/var/lib/docker`, no **disco de OS de
24GB**, que também carrega as imagens Docker (~8.8GB). Isso levou o root a 98% e
**derrubou um deploy** (`no space left on device` no `go build`). Enquanto isso,
`/mnt/data` (49GB) fica quase vazio.

## Solução

O `docker-compose.yml` agora monta os segmentos via
`${SEGMENTSDATA_HOST_PATH:-segmentsdata}` (mesmo padrão de `PGDATA_HOST_PATH` /
`MASTERSDATA_HOST_PATH`). Em **dev** (env não setada) cai no named volume de sempre;
em **prod** aponta para um bind mount em `/mnt/data/segments`.

> ⚠️ Segmentos são **efêmeros**: a evidência confirmada já está no MinIO/S3. Perder o
> buffer atual não perde nada que importa — o novo bind começa limpo e se repovoa em
> minutos. (Detecções dos últimos minutos cuja evidência ainda não foi extraída podem
> ficar `evidence_status='missing'` — janela mínima. Se quiser zerar esse risco, faça
> o `cp -a` opcional do passo 3.)

## Migração na VM (uma vez)

```bash
cd ~/radiocheck

# 1. destino no disco grande
sudo mkdir -p /mnt/data/segments

# 2. aponta a env var (no .env que o deploy.sh usa)
grep -q '^SEGMENTSDATA_HOST_PATH=' infra/docker/.env \
  && sed -i 's#^SEGMENTSDATA_HOST_PATH=.*#SEGMENTSDATA_HOST_PATH=/mnt/data/segments#' infra/docker/.env \
  || echo 'SEGMENTSDATA_HOST_PATH=/mnt/data/segments' >> infra/docker/.env

# 3. (OPCIONAL) preserva a janela atual de segmentos
sudo cp -a /var/lib/docker/volumes/docker_segmentsdata/_data/. /mnt/data/segments/ 2>/dev/null || true

# 4. deploy normal — recria SÓ api + segments-cleanup com o bind novo.
#    (up -d sem --force-recreate; postgres/minio/etc. não são tocados.)
#    ⚠️ pausa a captura das emissoras pelos segundos do recreate — faça off-peak.
./scripts/deploy.sh
```

## Verificação pós-migração

```bash
COMPOSE="docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env"

# o mount do api deve ser bind para /mnt/data/segments (não mais o named volume)
docker inspect "$($COMPOSE ps -q api)" \
  --format '{{range .Mounts}}{{.Type}} {{.Source}} -> {{.Destination}}{{"\n"}}{{end}}' | grep segments
# esperado: bind /mnt/data/segments -> /data/segments

# o cleanup está escrevendo no path novo?
docker run --rm -v /mnt/data/segments:/seg alpine:3.19 sh -c 'ls /seg | wc -l; du -sh /seg'
```

## Reclamar o disco (depois de confirmar o bind)

O named volume antigo (`docker_segmentsdata`, ~5GB) fica **órfão** — nada mais o usa.
Só então remova:

```bash
docker volume rm docker_segmentsdata   # falha de propósito se ainda houver consumidor
df -h /                                 # deve liberar ~5GB no root
```

> 🚫 **NUNCA** `docker volume prune` / `--volumes` — apaga volume de dado ativo
> (postgres/minio). Remova **só** o `docker_segmentsdata` nominalmente, e **só** depois
> de o `docker inspect` confirmar que o api migrou pro bind.

## Alternativa / complemento: reduzir a retenção

Independente do disco, dá pra cortar o footprint reduzindo a janela do
`segments-cleanup` (hoje `-mmin +60`). A maior janela de evidência extraída é ~3min,
então 60min é bem folgado; `-mmin +30` corta ~2x. É no `command` do service
`segments-cleanup` em `docker-compose.yml`. Menos impactante que mover de disco —
use como complemento, não substituto.
