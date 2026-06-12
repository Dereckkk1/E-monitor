---
status: planejado
ultima-verificacao: 2026-06-08
codigo-relacionado:
  - workers/pkg/audio/peaks.go
  - fingerprint/fingerprint/generator.py
  - fingerprint/fingerprint/main.py
  - workers/internal/index/loader.go
---

# Migração: re-fingerprint para densidade (#2 — recall de áudio curto)

**O que mudou:** o raio temporal do max-filter no peak-picking encolheu de 8→3
frames (`peaks.go` neighborFrames; `generator.py` PEAK_NEIGHBORHOOD_T 17→7,
PEAK_NEIGHBORHOOD_F 17→13). Resultado: **~4× mais hashes por janela** → spots de
5-15s ganham margem de match pra sobreviver à degradação de broadcast.

**Por que é migração:** muda a **math do hash**. Query nova (densa) casa mal
com índice antigo (esparso). **Misturar = degradação severa, inversamente
proporcional à riqueza do material** — não é zero uniforme: os picos antigos
sobrevivem no conjunto denso, mas o fan-out re-pareia âncoras com vizinhos
mais próximos, então só uma minoria dos pares antigos continua sendo gerada.
Medido em produção (incidente
[2026-06-12](../incidents/incident-2026-06-12-detection-recall-gaps.md)):
materiais curtos/falados **zeram**; longos/ricos perdem 10-30% de detecções.
A forma parcial é mais traiçoeira que "0 match": detecções continuam pingando
e a migração incompleta passa despercebida. Logo o código novo PRECISA ir
junto com a base re-fingerprintada (cutover atômico) **e a completude precisa
ser verificada** (passo 3 abaixo).

> ⚠️ **Validado, mas com custo:** matcher ~2× CPU (4× hashes/janela). O box foi
> migrado para **c3-highcpu-8 (8 vCPU, 16 GB)** em 2026-06-08 — dobrar o CPU
> absorveu o 2× do matcher, então o uso fica confortável (~40%, era ~82% no
> c3-standard-4). O **audit §9.9 é a rede anti-FP** (já comprovado que rejeita
> match ruim), então a densidade extra é segura quanto a falso positivo. A RAM
> (16 GB) folga: o índice é ~40 MB mesmo com a densidade.

## Passo a passo

### 1. Deploy do código novo (Go workers + Python fingerprint)
```bash
cd ~/radiocheck
git pull                      # pega o commit do #2
./scripts/deploy.sh           # builda + sobe api/worker/fingerprint NOVOS
```

### 2. Re-fingerprint de TODA a base (imediatamente após o deploy)
Dispara `fingerprint.generate` pra cada material; o serviço Python re-fingerprinta
(5 variantes) e **substitui** os hashes (`DELETE`+`COPY`). O índice Go recarrega
via `index.reload`.

```bash
docker compose -f infra/docker/docker-compose.yml \
               -f infra/docker/docker-compose.override.yml \
               --env-file infra/docker/.env \
  exec -T fingerprint python -c '
import asyncio, os, json, asyncpg, nats
async def main():
    pool = await asyncpg.create_pool(os.environ["DATABASE_URL"])
    nc = await nats.connect(os.environ["NATS_URL"])
    rows = await pool.fetch("SELECT id FROM materials WHERE fingerprint_status = '"'"'ready'"'"'")
    for r in rows:
        await nc.publish("fingerprint.generate", json.dumps({"material_id": str(r["id"])}).encode())
    await nc.flush(); print(f"disparado re-fingerprint de {len(rows)} materiais")
    await nc.drain(); await pool.close()
asyncio.run(main())'
```

**Janela degradada:** cada material fica "fora" (query densa × índice esparso)
até a sua vez de re-processar. Acompanhe `docker compose logs -f fingerprint`
até ver `done` de todos. Em prod recorrente, preferir índice versionado (TODO)
ou janela de baixa audiência.

### 3. Verificar completude (OBRIGATÓRIO — não pular)

O passo 2 é fire-and-forget: mensagens NATS podem se perder e o serviço pode
falhar no meio da fila **sem nenhum erro visível** (foi exatamente o que
deixou 32 materiais cegos de 08 a 12/06). A migração só está concluída quando:

```bash
./scripts/check-fingerprint-freshness.sh <data-do-deploy>
# exit 0 + "Catálogo consistente" = concluída.
# exit 1 = re-rodar o passo 2 para os listados e verificar de novo.
```

O reconciler da fila (`workers/internal/fingerprintqueue/`) re-tenta presos
automaticamente, e o alerta `FingerprintStuck` pega o que o retry não resolve
— mas nenhum dos dois sabe da *data de corte*; só este check valida a
migração em si.

### 4. Recalibração dos thresholds (opcional)
A densidade muda a distribuição de ruído. O piso `min_hashes=5` + o audit já
seguram, mas pra recalibrar, re-armar a calibração das estações (o scheduler
faz isso a cada 7 dias; ou forçar via `RunOnceForStation`/endpoint admin).

## Verificação pós-migração
```sql
-- hashes por material devem ter ~4x crescido:
SELECT m.title, m.fingerprint_hash_count FROM materials m WHERE m.title ILIKE '%asaas%';
```
E na view `daily_play_summary`, o `faltou` dos tipos Spot 05"/15" deve cair.
Monitorar CPU: `docker stats`.

## Rollback
`write_hashes` substitui (DELETE+COPY), então rollback é **simétrico**:
1. Reverter o código (`git revert <commit>` ou checkout dos arquivos antigos de
   `peaks.go`/`generator.py`).
2. `./scripts/deploy.sh`.
3. Rodar o **mesmo script** do passo 2 → volta aos hashes esparsos.
Sem perda de dado (hashes são derivados dos masters). ~mesmo tempo.

## Histórico
- 2026-06-08: criado. Fix #2 validado em air-checks reais perdidos (Asaas SPOT
  15 em Ouro Verde/Antena 1: pico por janela 57→178, 50→185). Ver
  [docs/roadmap/short-audio-detection-plan.md](../roadmap/short-audio-detection-plan.md).
