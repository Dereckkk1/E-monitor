# WorkerHighMemory

## Sintomas
Container `radiocheck-worker.*` (ou `api`) acima de 1.5 GiB de working set por mais de 30 minutos. Forte indicador de memory leak (R28 do plano), comum em ffmpeg de longa duração e em mudanças recentes no path de matching.

## Causas Comuns
1. Memory leak no ffmpeg embarcado (R28) — esperado, mitigado por restart preventivo (§8.7)
2. Restart preventivo desligado ou janela longa demais (`PREVENTIVE_RESTART_HOURS` alto)
3. Vazamento de buffers em código Go (referência mantida em map global, goroutine bloqueada)
4. Índice de fingerprints crescendo sem reload (acumulando hashes antigos)
5. Gravações de evidência segurando em RAM antes de flush para disco
6. Métrica `radiocheck_evidence_queue_size` alto → spool em memória cresce com o backlog

## Diagnóstico
```bash
# Memória atual de cada container worker
docker stats --no-stream --format "table {{.Name}}\t{{.MemUsage}}\t{{.MemPerc}}" | grep -E "worker|api"

# Histórico no Prometheus
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=container_memory_working_set_bytes{name=~"radiocheck-worker.*|api"}' | jq

# Heap profile (se pprof estiver habilitado)
curl -s http://localhost:8080/debug/pprof/heap?debug=1 | head -100

# Goroutines vazadas
curl -s http://localhost:8080/debug/pprof/goroutine?debug=1 | grep -c "^goroutine"

# Há quanto tempo o container está de pé?
docker inspect <container> --format '{{ .State.StartedAt }}'

# Quantos hashes em índice?
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=radiocheck_index_hashes_total' | jq
```

## Correção
- **Caso A — leak esperado de ffmpeg**: confirmar que `PREVENTIVE_RESTART_HOURS` está em <=24h. Se está, e ainda assim crescendo, baixar para 12h.
- **Caso B — restart preventivo desligado**: ativar via env e fazer rolling restart.
- **Caso C — leak de Go**: capturar `heap` e `goroutine` profiles, anexar ao incidente, fazer restart do container para mitigar imediatamente.
- **Caso D — índice inchado**: rodar `POST /v1/internal/index/reload` para forçar reload limpo.
- **Caso E — backlog de evidência**: ver `EvidenceQueueGrowing.md` — atacar a raiz; este alerta é só consequência.

## Escalação
- 2+ ocorrências por semana → escalar para `#dev-radiocheck` com profiles capturados; abre RC para investigação de leak.
- Se memória passar de 3 GiB → restart imediato, sem aguardar diagnóstico (risco de OOM-kill prejudicar outras emissoras).

## Prevenção
- Manter `PREVENTIVE_RESTART_HOURS=24` por padrão (§8.7).
- Rodar load test mensal de 72h para detectar leak antes que apareça em produção.
- Ativar pprof em endpoint interno para captura rápida quando alerta dispara.
