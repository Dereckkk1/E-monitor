---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - infra/prometheus/alerts.yml
  - workers/internal/calibration/scheduler.go
  - workers/internal/metrics/metrics.go
---

# CalibrationStale

## Sintomas
A métrica `radiocheck_calibration_last_success_timestamp` para uma ou mais
emissoras está há mais de 14 dias sem ser atualizada (alerta dispara após 1h
nesse estado). O esperado pelo §9.4 é uma recalibração a cada 7 dias; 14d é
um sinal claro de que o scheduler parou.

Sintomas correlatos:
- `radiocheck_calibration_runs_total{result="error"}` subindo sem
  contrapartida em `result="success"`.
- Logs do API com `calibration scheduler: tick failed` ou
  `calibration scheduler: station failed`.
- Detecções da emissora com taxa de falsos positivos/negativos diferente do
  histórico (threshold antigo virou inadequado para o ruído atual).

## Causas Comuns
1. Advisory lock preso por uma sessão Postgres morta — `pg_try_advisory_lock`
   continua retornando `false` indefinidamente.
2. Scheduler caiu silenciosamente (ex: panic em outra goroutine derrubou o
   processo, restart loop em sequência).
3. Pool de conexões saturado — `pool.Acquire(ctx)` excede o timeout.
4. Migração pendente — a tabela `station_thresholds` mudou de schema e o
   UPDATE falha por coluna desconhecida.
5. `CALIBRATION_INTERVAL` foi setado para um valor absurdo (ex: `8760h`)
   por engano.

## Diagnóstico

```bash
# 1. Confirmar a quais emissoras o alerta se refere
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=time() - radiocheck_calibration_last_success_timestamp > 14*24*3600' \
  | jq

# 2. Ver logs do scheduler nos últimos 30 min
docker compose logs api --since 30m | grep -i "calibration scheduler"

# 3. Conferir a idade real dos thresholds no banco
psql "$DATABASE_URL" -c "
  SELECT station_id, calibration_mode, updated_at, NOW() - updated_at AS age
  FROM station_thresholds
  ORDER BY updated_at ASC
  LIMIT 20;"

# 4. Ver se alguma sessão está segurando o advisory lock
psql "$DATABASE_URL" -c "
  SELECT pid, mode, granted, locktype, classid, objid
  FROM pg_locks
  WHERE locktype = 'advisory';"

# 5. Conferir env vars
docker compose exec api env | grep CALIBRATION
```

## Correção

### 1. Forçar uma rodada manual
O endpoint admin dispara um pass imediato sem esperar o tick de 24h:

```bash
# Full pass (todas as emissoras elegíveis)
curl -X POST http://api/v1/internal/admin/calibration/run \
  -H "Authorization: Bearer $ADMIN_JWT"

# Apenas uma emissora (ignora a janela de 7d)
curl -X POST http://api/v1/internal/admin/calibration/run \
  -H "Authorization: Bearer $ADMIN_JWT" \
  -H "Content-Type: application/json" \
  -d '{"station_id":"<uuid>"}'
```

**Idempotência:** acionar várias vezes a mesma emissora é seguro — o
UPDATE em `station_thresholds` é idempotente (sempre seta
`calibration_mode=true` e zera `noise_samples`), e
`calibration_started_at` é simplesmente sobrescrito. Em concorrência
real (dois admins clicando), a perda fracional de janela é aceitável.
A rota admin **emite as mesmas métricas que o tick natural**, então
não há "buraco" em `radiocheck_calibration_last_success_timestamp`
após um run manual bem-sucedido.

### 2. Soltar advisory lock travado
Se o diagnóstico (4) mostrou um pid antigo segurando o lock advisory:

```sql
-- Confirmar a chave
SELECT classid, objid FROM pg_locks WHERE locktype='advisory';
-- Matar a sessão dona
SELECT pg_terminate_backend(<pid>);
```

A chave é determinística (FNV-1a de `radiocheck:calibration-scheduler`) e
está exposta via `calibration.AdvisoryLockKey()` para inspeção.

### 3. Restartar o API
Se o scheduler caiu silenciosamente, um restart do container reinicializa o
loop. Como o scheduler executa um pass inicial imediato, não há atraso de
24h após o restart.

```bash
docker compose restart api
```

### 4. Verificar conexão DB
Se `pool.Acquire` está timing out:

```bash
psql "$DATABASE_URL" -c "SELECT count(*) FROM pg_stat_activity;"
# Se > max_connections-5, há leak em outro componente; investigar antes de
# subir o limite.
```

### 5. Reverter `CALIBRATION_INTERVAL` / `CALIBRATION_MIN_AGE`
Defaults sãos: `24h` e `168h` (7d). Em produção raramente devem ser
alterados — só em ambientes de teste.

## Escalação
- Se nenhuma das correções acima resolver e o alerta persistir após 4h,
  escalar para o time de plataforma via `#ops-radiocheck` com:
  - Saída de `pg_locks`.
  - Últimos 100 logs do scheduler.
  - Resultado do `psql` mostrando `updated_at` por emissora.

## Ver também
- [operations/calibration.md](../operations/calibration.md) — como a calibração por emissora funciona (§9.4) e é operada.
- [operations/threshold-dynamic.md](../operations/threshold-dynamic.md) — threshold adaptativo derivado da calibração.
- [runbooks/README.md](README.md) — índice dos runbooks.
