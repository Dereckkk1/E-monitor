---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - workers/pkg/audio/stft.go
  - workers/pkg/audio/peaks.go
  - workers/pkg/audio/hashes.go
  - workers/internal/fingerprint/pipeline.go
  - workers/cmd/fingerprint/main.go
  - migrations/0001_initial.up.sql
---

# Fingerprint Pipeline (offline / batch)

Documentação operacional do pipeline que gera fingerprints acústicos a partir
do master de um comercial e os persiste em `fingerprint_hashes`. Spec
arquitetural completa: §7 do `plano_implementacao.md`.

## Visão geral

```
master.{wav,mp3,m4a,ogg}
    │
    ▼
ffmpeg (decode + filtros, mono 16 kHz f32le)
    │
    ▼
STFT (4096 / 2048 hop, Hann)            ── pkg/audio/stft.go
    │
    ▼
Peak picking (vizinhança 17×17, p80)    ── pkg/audio/peaks.go
    │
    ▼
Peak pairing → hash uint32 (f1,f2,dt)   ── pkg/audio/hashes.go
    │
    ▼
COPY → fingerprint_hashes               ── internal/fingerprint/persist.go
       (transação que também atualiza
        commercials.fingerprint_status)
```

A camada `internal/fingerprint` é apenas orquestração — todos os blocos de DSP
moram em `pkg/audio` e são compartilhados com o `cmd/diag` e o stream worker.

## CLI

Binário: `workers/cmd/fingerprint`.

```bash
# Compilar
cd workers && go build ./cmd/fingerprint

# Modo dry-run: roda o pipeline e imprime estatísticas, sem tocar no banco
./fingerprint --input master.mp3 --dry-run

# Modo gravação: persiste em fingerprint_hashes e marca o comercial como ready
DATABASE_URL=postgres://radiocheck@localhost/radiocheck \
  ./fingerprint --commercial-short-id 123 --input master.mp3

# Re-rodar para o mesmo comercial (apaga rows anteriores antes de inserir)
./fingerprint --commercial-short-id 123 --input master.mp3 --replace
```

Flags relevantes:

| Flag | Default | Descrição |
|------|---------|-----------|
| `--commercial-short-id` | — | `commercials.short_id` do alvo. Obrigatório fora de `--dry-run`. |
| `--input` | — | Caminho do master (qualquer formato suportado pelo ffmpeg). |
| `--variant` | `clean` | `clean\|light\|medium\|heavy`. Apenas `clean` está implementado hoje. |
| `--rate-id` | `0` | Identificador multi-rate (§9.7). Hoje usamos só 0 (1.0×). |
| `--db-url` | `$DATABASE_URL` | Connection string do Postgres. |
| `--replace` | `false` | Apaga rows existentes para `(commercial, variant, rate)` antes de inserir. |
| `--dry-run` | `false` | Não conecta no banco; apenas imprime estatísticas. |

### Exemplo de saída

```
[1/4] decoding master   : master.mp3 (variant=clean)
[2/4] decoded duration  : 30.05s
[3/4] STFT frames       : 470
       constellation peaks : 1240
       hashes generated   : 5800 (193.0 hash/s, 4520 unique, entropy ≈ 11.92 bits)
       pipeline elapsed   : 410ms
       validation         : OK
[4/4] writing hashes    : commercial=… (short_id=123, title="Promo Verão 30s")
       inserted rows      : 5800 (variant=0, rate_id=0, replace=false)
done.
```

## Comandos ffmpeg

A variante `clean` usa o pré-processamento de análise descrito em §7.2:

```
ffmpeg -hide_banner -loglevel error \
  -i <input> \
  -ac 1 -ar 16000 \
  -af "loudnorm=I=-16:LRA=11:TP=-1.5,highpass=f=80,lowpass=f=7500" \
  -f f32le -c:a pcm_f32le -
```

A normalização LUFS é importante para que os limiares de pico sejam estáveis
entre masters de níveis diferentes; o highpass remove rumble e o lowpass corta
ruído acima da banda útil em 7.5 kHz (consistente com `FreqMaxBin` na STFT).

## Constantes

Definidas em `pkg/audio` (espelhando §25 do plano):

| Constante | Valor | Onde |
|-----------|-------|------|
| `SampleRate` | 16000 Hz | `internal/fingerprint/audio.go` |
| `WindowSize` | 4096 | `pkg/audio/stft.go` |
| `HopSize` | 2048 | `pkg/audio/stft.go` |
| `FreqMinBin` / `FreqMaxBin` | 25 / 1024 | `pkg/audio/stft.go` |
| `PeakAmplitudePercentile` | 80 | `pkg/audio/peaks.go` |
| `FanOut` | 8 | `pkg/audio/hashes.go` |
| `TargetZoneTMin` / `TargetZoneTMax` | 1 / 24 | `pkg/audio/hashes.go` |
| `TargetZoneF` | 50 | `pkg/audio/hashes.go` |

Encoding do hash (32 bits): `(f1 & 0x1FF) << 23 | (f2 & 0x1FF) << 14 | (dt & 0x3FFF)`.
Persistido como `BIGINT` em `fingerprint_hashes.hash_value` para evitar
ambiguidade de sinal — o índice em memória reconverte para `uint32`.

## Validação pós-geração

`fingerprint.Validate(Result)` aplica os checks rápidos do §7.5:

- **Densidade mínima**: `hashes/s ≥ 30`. Se cair, é sinal de áudio com pouca
  energia espectral (silêncio, voz fina sem música) ou STFT/threshold mal
  calibrado.
- **Entropia**: estimativa Shannon sobre a distribuição empírica de
  `Hash.Value` ≥ 8 bits. Distribuições degeneradas (poucos hashes dominando)
  tendem a produzir falsos positivos.

Hoje a CLI apenas imprime warnings; ela ainda não bloqueia o `INSERT` quando
a validação falha. Ver TODOs abaixo.

## TODOs para produção

- **Broadcast simulation (§7.2 etapa 2)** — implementar variantes `light`,
  `medium` e `heavy`. A assinatura do código já existe
  (`fingerprint.GenerateForVariant(ctx, path, variant)`); resta:
  1. Adicionar à `filterChain()` os pipelines com `acompressor` /
     `alimiter` / reencode para AAC nos bitrates do plano (96k / 64k / 48k).
  2. Possivelmente fazer dois passos no ffmpeg (encode AAC + decode de
     volta para PCM) para preservar artefatos do codec.
  3. Persistir cada variante com um `variant_id` distinto (hoje `clean=0`).
- **Multi-rate matching (§9.7)** — gerar fingerprints adicionais com
  resampling ±2% (rate_ids 1 e 2). O CLI já aceita `--rate-id`.
- **Validação completa (§7.5)** — hoje fazemos apenas density+entropy. Falta:
  - Re-match contra o próprio índice (esperado confidence > 0.95).
  - Match contra corpus de ruído (esperado: nenhum match).
  - Perturbações controladas (white noise -30 dB, time-stretch ±2%, reencode
    extra) com confidence > 0.6.
- **Watcher**: hoje o pipeline roda só via CLI manual. Para Fase 2 precisamos
  de um worker que escute eventos NATS (`commercial.created`,
  `commercial.master_updated`) e dispare `fingerprint.GenerateForVariant`
  automaticamente.
- **Hot reload**: depois do `Persist` bem-sucedido, publicar
  `events.SubjectIndexReload` com o `commercial_id` para o índice em memória
  recarregar sem restart (já implementado em `index.Loader.Subscribe`).
- **Métricas Prometheus**: tempo de geração, hashes/segundo, taxa de validação
  falhada (§15.1).

## Schema relacionado

`fingerprint_hashes` (definida em `migrations/0001_initial.up.sql`):

```sql
CREATE TABLE fingerprint_hashes (
    commercial_id UUID NOT NULL,
    variant_id SMALLINT NOT NULL,
    rate_id SMALLINT NOT NULL DEFAULT 0,
    hash_value BIGINT NOT NULL,
    time_frame INT NOT NULL,
    PRIMARY KEY (commercial_id, variant_id, rate_id, hash_value, time_frame)
) PARTITION BY HASH (commercial_id);
```

Não foi necessária migration nova — a coluna `variant_id` (e `rate_id`) já
existia no schema inicial.

A transação de persistência também atualiza:

```sql
UPDATE commercials
SET fingerprint_status = 'ready',
    fingerprint_generated_at = NOW(),
    fingerprint_hash_count = $N
WHERE id = $1;
```

Isso é o gatilho que `index/loader.go` usa para incluir o comercial no índice
em memória do stream worker.
