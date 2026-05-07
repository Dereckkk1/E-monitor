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

## Reconfiguração Manual

Para forçar recalibração de uma emissora:
```sql
UPDATE station_thresholds
SET calibration_mode = true,
    calibration_started_at = NOW(),
    noise_samples = '{}'
WHERE station_id = '<uuid>';
```
