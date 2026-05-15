---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - infra/prometheus/alerts.yml
  - workers/internal/metrics/metrics.go
---

# DetectionRateAnomaly

## Sintomas
Taxa de detecções confirmadas (últimos 10min) caiu para **menos de 30%** da média móvel de 7 dias, sustentada por 30 minutos. Sintoma agregado — pode mascarar várias causas distintas.

## Causas Comuns
1. Índice de fingerprints não recarregou após troca de comerciais (`POST /v1/internal/index/reload` falhou silenciosamente)
2. Threshold de matching deslocado por deploy recente (mudança em `MATCH_THRESHOLD` ou regenerated constants)
3. Quedas em massa de stream (verificar primeiro se `MultipleStreamsDown` também disparou)
4. Comerciais ativos zerados (todas as campanhas chegaram a `concluida` e novas não foram cadastradas)
5. Mudança no formato/codec de stream do upstream que invalida o pré-processamento
6. Bug regressivo no algoritmo de matching introduzido em deploy

## Diagnóstico
```bash
# Taxa atual vs média
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=sum(rate(radiocheck_detections_total{status="confirmed"}[10m]))' | jq
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=sum(avg_over_time(rate(radiocheck_detections_total{status="confirmed"}[10m])[7d:10m]))' | jq

# Quantos comerciais ativos no índice?
docker compose exec postgres psql -U radiocheck -c \
  "SELECT COUNT(DISTINCT commercial_id) FROM commercial_hashes;"

# Quantas campanhas ativas no momento?
docker compose exec postgres psql -U radiocheck -c \
  "SELECT status, COUNT(*) FROM campaigns GROUP BY status;"

# Último deploy
git log --oneline -5

# Conferir se MultipleStreamsDown está ativo no Alertmanager
curl -s http://localhost:9093/api/v2/alerts | jq '.[] | select(.labels.alertname=="MultipleStreamsDown")'
```

## Correção
- **Caso A — índice desatualizado**: forçar reload via `POST /v1/internal/index/reload`. Confirmar logs do `api` mostrando "index loaded N hashes".
- **Caso B — threshold quebrado por deploy**: revisar diff recente em `workers/internal/matching/`. Considerar rollback se confirmado.
- **Caso C — streams down**: tratar `MultipleStreamsDown` primeiro; este alerta deve auto-resolver depois.
- **Caso D — sem campanhas ativas**: confirmar com time comercial. Pode ser cenário esperado (fim de semana, fim de mês).
- **Caso E — codec mudou**: comparar amostra recente (`docker compose exec api ffprobe <stream_url>`) com baseline. Atualizar pipeline se formato mudou.

## Escalação
- Após 1h sem identificar causa, escalar para `#dev-radiocheck` com snapshot do dashboard de detecções e diff do último deploy.
- Se detecções zerarem completamente (`rate == 0`), tratar como incidente crítico — clientes verão dados em branco.

## Prevenção
- Adicionar smoke test pós-deploy: gerar 1 detecção sintética e verificar pipeline end-to-end antes de marcar deploy ok.
- Alarme secundário em `radiocheck_detections_total{status="confirmed"} == 0 for 1h` durante horário comercial.
- Manter histórico de threshold por release no changelog para correlacionar regressões.
