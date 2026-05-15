---
status: planejado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - workers/internal/supervisor/lifecycle_scheduler.go
  - infra/prometheus/alerts.yml
  # metrica radiocheck_lifecycle_scheduler_last_run_timestamp nao exposta ainda
---

# LifecycleSchedulerStuck

> **Status:** alerta cadastrado, mas a métrica `radiocheck_lifecycle_scheduler_last_run_timestamp` ainda **não é exposta** pelo serviço. Este runbook se aplica assim que o gauge for adicionado ao scheduler de campanhas (§18.2.1). Item de débito técnico — não fechado na implementação 2B.

## Sintomas
Scheduler de transições de campanha (`programada → ativa → concluida`, §18.2.1) não roda há mais de 5 minutos. Campanhas podem ficar "presas" em estado incorreto: anúncios não detectam (campanha ainda `programada`) ou continuam contando após a data fim (campanha não vai a `concluida`).

## Causas Comuns
1. Goroutine do scheduler bloqueada por query Postgres lenta
2. Panic não recuperado no scheduler derrubou o ticker (sem restart automático)
3. Lock de transação concorrente bloqueando a tabela `campaigns`
4. Container `api` reiniciou e o scheduler não inicializou (regressão em código de bootstrap)
5. Métrica `radiocheck_lifecycle_scheduler_last_run_timestamp` está sendo atualizada apenas em transições reais e fica congelada quando não há nada a transitar (BUG — gauge precisa ser tocada todo tick, mesmo sem transição)

## Diagnóstico
```bash
# Última execução conforme métrica (em segundos atrás)
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=time() - radiocheck_lifecycle_scheduler_last_run_timestamp' | jq

# Logs do scheduler
docker compose logs api 2>&1 | grep -i "lifecycle\|scheduler" | tail -50

# Campanhas em estados que deveriam ter transitado já
docker compose exec postgres psql -U radiocheck -c \
  "SELECT id, name, status, start_at, end_at FROM campaigns
   WHERE (status='programada' AND start_at < now())
      OR (status='ativa' AND end_at < now())
   ORDER BY start_at LIMIT 20;"

# Está vivo o ticker? (pprof)
curl -s http://localhost:8080/debug/pprof/goroutine?debug=2 | grep -A 5 -i "lifecycle"
```

## Correção
- **Caso A — query lenta**: ver `DBLatencyHigh.md`. Resolver gargalo no Postgres restaura o scheduler automaticamente.
- **Caso B — panic**: reiniciar `docker compose restart api` para reerguer scheduler. Capturar logs anteriores antes do restart.
- **Caso C — lock**: cancelar transação bloqueando `campaigns` (ver `DBLatencyHigh.md` Caso C).
- **Caso D — não inicializou**: confirmar feature flag/env (`LIFECYCLE_SCHEDULER_ENABLED=true`); se acidental, restaurar.
- **Caso E — métrica congelada por bug**: corrigir gauge para ser atualizada a cada tick (não só em transição). Workaround: olhar logs em vez do alerta.
- **Mitigação manual**: rodar `POST /v1/internal/campaigns/lifecycle/run` (se endpoint existir) para forçar uma execução; ou executar a SQL de transição diretamente como fallback.

## Escalação
- Após 15min sem retomar, escalar para `#dev-radiocheck` — campanhas paradas geram impacto comercial direto (faturamento incorreto).
- Comunicar time comercial assim que confirmado para evitar reclamações sem contexto.

## Prevenção
- Adicionar testes de integração que verifiquem o tick do scheduler (subir serviço, esperar 2 ticks, validar que a métrica avançou).
- Atualizar `radiocheck_lifecycle_scheduler_last_run_timestamp` a cada execução do tick, mesmo sem transições (TODO atual).
- Adicionar endpoint interno `POST /v1/internal/campaigns/lifecycle/run` para fallback manual, idempotente.
- Instrumentar panics com `recover()` + alerta separado, em vez de derrubar a goroutine silenciosamente.
