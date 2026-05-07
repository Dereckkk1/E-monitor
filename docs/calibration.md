# Calibração Adaptativa de Threshold

O sistema ajusta automaticamente o `min_hashes` (mínimo de hashes para confirmar uma detecção) por emissora.

## Fluxo

1. Cada nova emissora começa em `calibration_mode = true` (threshold padrão: 5 hashes).
2. A cada janela de análise (2s), o worker registra o `maxScore` observado em `noise_samples`.
3. Após 7 dias, o job diário calcula `noise_p99` e define `min_hashes = max(p99 × 1.5, 5)`.
4. A emissora sai do modo de calibração (`calibration_mode = false`).

## Limites

- `noise_samples` é limitado a 5000 entradas (~2.8h de dados). Suficiente para p99 estatisticamente válido.
- O job roda uma vez por dia às 24h desde o início do processo (sem horário fixo).

## Campos em `station_thresholds`

| Campo | Descrição |
|-------|-----------|
| `calibration_mode` | `true` enquanto coletando amostras |
| `calibration_started_at` | Início da calibração |
| `noise_samples` | Amostras de hashCount (máximo por janela) |
| `noise_p99` | Percentil 99 das amostras (calculado ao final) |
| `min_hashes` | Threshold atual (padrão 5, recalculado após calibração) |

## Recalibração Periódica (Scheduler)

A condição acústica de uma emissora muda ao longo do tempo (novos jingles,
mudança de equipamentos, atualização de cadeia de áudio). O `min_hashes`
calculado uma única vez na entrada do sistema não é suficiente: o §9.4 do
plano exige recálculo a cada 7 dias.

O `calibration.Scheduler` (em `workers/internal/calibration/scheduler.go`)
fecha esse loop:

1. A cada **24h** (configurável via `CALIBRATION_INTERVAL`), o scheduler
   procura emissoras com `calibration_mode = false` cujo `updated_at` em
   `station_thresholds` é mais antigo que **7 dias** (configurável via
   `CALIBRATION_MIN_AGE`).
2. Para cada emissora elegível, o scheduler reseta a linha de threshold:
   `calibration_mode = true`, `calibration_started_at = NOW()`,
   `noise_samples = '{}'`.
3. O worker volta a coletar amostras durante a janela de 7 dias.
4. O job diário existente (`RunCalibrationJob`) promove a emissora de
   volta para fora de `calibration_mode` com o `noise_p99` recém
   calculado.

Esse arranjo mantém a lógica de cálculo do threshold em um único lugar
(`RunCalibrationJob`) e usa o scheduler apenas para *armar* uma nova
janela quando a anterior envelhece.

### Variáveis de ambiente

| Var | Default | Uso |
|-----|---------|-----|
| `CALIBRATION_INTERVAL` | `24h` | Período entre passes do scheduler. Aceita qualquer `time.ParseDuration`. |
| `CALIBRATION_MIN_AGE` | `168h` (7d) | Idade mínima do `station_thresholds.updated_at` para uma emissora ser elegível. |

### Throttle e timeout

- Máximo **5 emissoras em paralelo** por pass (semáforo `golang.org/x/sync`).
- **5 minutos** de timeout por emissora (`Scheduler.StationTimeout`).
- Falha em uma emissora **não interrompe** as demais.

### Multi-réplica (advisory lock)

Em deploys com 2+ réplicas do API, ambas tentariam recalibrar as mesmas
emissoras a cada tick. Para evitar duplicação, o scheduler envolve cada
tick em um `pg_try_advisory_lock` cuja chave é
`FNV-1a("radiocheck:calibration-scheduler")` (exposta via
`calibration.AdvisoryLockKey()`). Quem perder a corrida loga
`another instance holds the advisory lock; skipping tick` e sai. O lock
é liberado quando a sessão volta para o pool ou via
`pg_advisory_unlock` no defer do tick.

### Métricas

| Métrica | Tipo | Labels |
|---------|------|--------|
| `radiocheck_calibration_runs_total` | counter | `result` (success/error) |
| `radiocheck_calibration_last_success_timestamp` | gauge | `station_id` |
| `radiocheck_calibration_duration_seconds` | histogram | `station_id` |

Alerta associado: **CalibrationStale** (em `infra/prometheus/alerts.yml`)
dispara quando `time() - radiocheck_calibration_last_success_timestamp >
14*24*3600` por 1h. Runbook: `docs/runbooks/CalibrationStale.md`.

### Endpoint admin

Para disparar uma rodada manual sem esperar o tick (útil em ops e testes):

```http
POST /v1/internal/admin/calibration/run
Authorization: Bearer <admin-jwt>
Content-Type: application/json

{"station_id": "<uuid-opcional>"}
```

- Sem corpo (ou sem `station_id`): scan completo das emissoras elegíveis.
- Com `station_id`: força recalibração de uma emissora **ignorando** o
  `MinAge` (útil quando o operador acaba de trocar a stream URL ou a
  cadeia de áudio).

Resposta:
```json
{"status":"ok","stations":7,"duration_ms":42}
```

## Reconfiguração Manual (banco)

Quando o endpoint admin não estiver disponível, é possível disparar a
recalibração editando direto a tabela:

```sql
UPDATE station_thresholds
SET calibration_mode = true,
    calibration_started_at = NOW(),
    noise_samples = '{}'
WHERE station_id = '<uuid>';
```
