---
status: implementado
ultima-verificacao: 2026-05-19
codigo-relacionado:
  - frontend/src/pages/OperationsPage.jsx
  - workers/internal/supervisor/supervisor.go
  - workers/internal/api/handlers/health.go
  - workers/internal/ingestor/worker.go
  - workers/internal/metrics/metrics.go
---

# Página /operations — Supervisor ao vivo

A tela `/operations` mostra o estado em tempo real de cada worker de stream:
identidade da emissora, último PCM recebido, status (running / stalled /
restarting / down), bytes consumidos, reconnects, stall restarts e o
threshold (`min_hashes`) corrente.

A página consome `GET /workers` (refresh 10s) e enriquece cada linha com
`GET /stream-health` (refresh 30s) para nome/cidade/banda e uptime.

## Wire contract — `GET /workers`

O handler [WorkerStatus](../../workers/internal/api/handlers/health.go) devolve:

```json
{
  "workers": [
    {
      "station_id": "uuid",
      "active": true,
      "last_pcm_at": "RFC3339",
      "stall_risk": false,
      "bytes_received": 12345678,
      "reconnects": 3,
      "stall_restarts": 0,
      "min_hashes": 7
    }
  ],
  "clap_verifier": true
}
```

O struct equivalente é
[`supervisor.WorkerStatus`](../../workers/internal/supervisor/supervisor.go).

### Semântica dos contadores

| Campo | Origem | Lifetime | Reset |
|---|---|---|---|
| `bytes_received` | `Worker.bytesReceived` (atomic.Uint64) | desde `Worker.Run` start | em recriação do worker (stall restart, troca de stream_url, troca de comerciais) |
| `reconnects` | `Worker.reconnects` (atomic.Uint32) | idem | idem |
| `stall_restarts` | `Supervisor.stallRestartCounts[stationID]` | desde o boot do supervisor | **não reseta** em recriação de worker — é cumulativo por emissora |
| `min_hashes` | `Worker.Threshold()` (atomic.Int32) | valor corrente do matcher | atualizado pelo refresher de threshold |

`bytes_received` e `reconnects` são "vida do worker corrente" deliberadamente:
quando o supervisor mata um worker stalled e cria outro, esses dois zeram —
o operador vê quanto a nova instância já recebeu/reconectou. Já
`stall_restarts` representa "quantas vezes esta emissora teve que ser
recuperada por stall watchdog" desde o boot do processo, então sobrevive a
recriação.

### Onde os incrementos acontecem

- **`bytes_received`** — [worker.go `runPCMReader`](../../workers/internal/ingestor/worker.go): cada `io.ReadFull` bem-sucedido. Também atualiza a métrica Prometheus `radiocheck_worker_bytes_received_total{station_id}`.
- **`reconnects`** — [worker.go `Run`](../../workers/internal/ingestor/worker.go): dois caminhos — `ffmpeg start failed` e `ffmpeg exited unexpectedly`. A primeira conexão não conta. Também alimenta `radiocheck_worker_reconnects_total{station_id}`.
- **`stall_restarts`** — [supervisor.go `runStallWatchdog`](../../workers/internal/supervisor/supervisor.go): quando o watchdog decide matar o worker por stall (60s sem PCM, cooldown de 2 min entre restarts). Também alimenta `radiocheck_worker_stall_restarts_total{station_id}`.
- **`min_hashes`** — Setado pelo [`runThresholdRefresher`](../../workers/internal/supervisor/supervisor.go) a cada `thresholdRefreshInterval` ou em refresh manual via admin. A métrica Prometheus correspondente é `radiocheck_station_threshold{station_id}`.

## Histórico

A página foi escrita no commit `8c87c43` esperando esses 4 campos, mas o
backend nunca entregou — `WorkerStatus` original tinha só `station_id`,
`active`, `last_pcm_at`, `stall_risk`. O resultado: tiles de "bytes" e
"min hashes" exibindo `—` e "reconnects" / "stall restarts" exibindo `0`
em **todas** as emissoras, em prod e dev, desde então. Os counters Prometheus
de bytes e reconnects também nunca foram instrumentados (o plano de fase 2
listava o item, mas ficou aberto). Resolvido em 2026-05-19.

## Como verificar

```bash
# 1. JSON cru — devem aparecer todos os campos preenchidos por worker ativo:
curl -s http://localhost:8080/workers | jq '.workers[0]'

# 2. Prometheus — os counters devem crescer em tempo real:
curl -s http://localhost:8080/metrics | grep -E \
  'radiocheck_worker_(bytes_received|reconnects|stall_restarts)_total'

# 3. UI — abrir /operations e confirmar que os 4 tiles e a tabela exibem números.
```
