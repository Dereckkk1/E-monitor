# DBLatencyHigh

> **Status:** alerta atualmente comentado em `infra/prometheus/alerts.yml`. Depende da exportação da métrica `radiocheck_db_query_duration_seconds_bucket` pelo serviço `api` (TODO em §15.1 do plano).

## Sintomas
Latência p95 de queries Postgres acima de 1s por mais de 10 minutos. Reflete em latência de API (endpoints de detecção, listagem, dashboards) e em atraso na escrita de eventos de detecção.

## Causas Comuns
1. Vacuum / autovacuum agressivo bloqueando tabela quente (`detections`, `commercial_hashes`)
2. Índice ausente após migration nova (verificar `EXPLAIN` da query lenta)
3. Plano de query degradado por estatísticas desatualizadas (`ANALYZE` necessário)
4. Lock de longa duração (transação esquecida, batch update sem `LIMIT`)
5. Picos de IOPS na infraestrutura (snapshot, backup concorrente)
6. Conexões saturadas no pool (`pgxpool` esgotado)

## Diagnóstico
```bash
# Queries em execução e tempo
docker compose exec postgres psql -U radiocheck -c \
  "SELECT pid, now()-query_start AS dur, state, wait_event_type, wait_event, substr(query,1,80) AS q
   FROM pg_stat_activity WHERE state <> 'idle' ORDER BY dur DESC LIMIT 20;"

# Tabelas mais quentes (sequencial scan vs index scan)
docker compose exec postgres psql -U radiocheck -c \
  "SELECT relname, seq_scan, idx_scan, n_live_tup, n_dead_tup
   FROM pg_stat_user_tables ORDER BY seq_scan DESC LIMIT 10;"

# Locks bloqueando
docker compose exec postgres psql -U radiocheck -c \
  "SELECT blocked.pid AS blocked_pid, blocking.pid AS blocking_pid,
          blocked.query AS blocked_query, blocking.query AS blocking_query
   FROM pg_stat_activity blocked
   JOIN pg_stat_activity blocking ON blocking.pid = ANY(pg_blocking_pids(blocked.pid));"

# Estado do pool no API process (logs)
docker compose logs api | grep -i "pool\|acquire" | tail -20
```

## Correção
- **Caso A — query sem índice**: rodar `EXPLAIN (ANALYZE, BUFFERS)` na query lenta, criar índice apropriado (em migration). Para `detections`, conferir se índices em `(station_id, observed_at)` e `(commercial_id, observed_at)` existem.
- **Caso B — estatísticas desatualizadas**: `VACUUM ANALYZE detections;` (ou tabela alvo).
- **Caso C — lock**: identificar `blocking_pid` e cancelar com `SELECT pg_cancel_backend(<pid>);`. Se for um `idle in transaction`, `pg_terminate_backend(<pid>)`.
- **Caso D — pool esgotado**: aumentar `max_conns` no `pgxpool` ou identificar leak (`Acquire` sem `Release`).
- **Caso E — IOPS**: mover backup para janela noturna; adiar tiering de evidências.

## Escalação
Se latência p95 não voltar abaixo de 500ms após 30min de mitigação, escalar para `#dev-radiocheck`. Considerar rollback do último deploy/migration.

## Prevenção
- Adicionar `EXPLAIN ANALYZE` em pre-commit para queries novas (CI mais leve).
- Configurar `log_min_duration_statement = 500ms` no Postgres para capturar lentidão.
- Revisar `pg_stat_statements` semanalmente para identificar queries no top de tempo total.
