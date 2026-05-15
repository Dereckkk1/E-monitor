---
status: parcialmente-implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - infra/grafana/dashboards/
  - infra/docker/docker-compose.yml
  - workers/internal/metrics/metrics.go
  # nota: 4 paineis sao placeholders por metricas ainda nao implementadas (documentado)
---

# Dashboards Grafana — Radiocheck

Documentação operacional dos painéis Grafana provisionados em
`infra/grafana/dashboards/`. Referência arquitetural: §15.4 do
[plano_implementacao.md](../../plano_implementacao.md).

## Provisioning

Os dashboards são carregados automaticamente pelo provider definido em
`infra/grafana/dashboards/dashboard-provider.yaml`. O provider lê todos
os arquivos `*.json` do diretório `/etc/grafana/provisioning/dashboards`
(montado a partir de `infra/grafana/dashboards/` no container Grafana).

Para adicionar um dashboard novo:

1. Crie/edite o JSON em `infra/grafana/dashboards/`.
2. Garanta que o JSON é válido (`ConvertFrom-Json` no PowerShell ou `jq .` em ambientes Unix).
3. Faça reload do Grafana — opções:
   - `docker compose restart grafana`
   - ou aguardar o intervalo de polling (default 10s) — o provider detecta
     mudanças automaticamente sem restart.

Datasource esperado: `Prometheus` (UID `prometheus`). Cada painel referencia
o datasource via variável de template `$datasource` para permitir override
em ambientes onde o UID não bate.

## Lista de dashboards

| UID | Título | Arquivo |
|-----|--------|---------|
| `radiocheck-ops` | Radiocheck — Operações | `operacoes.json` |
| `radiocheck-deteccoes` | Radiocheck — Detecções | `deteccoes.json` |
| `radiocheck-infra` | Radiocheck — Infra | `infra.json` |

---

## Operações (`operacoes.json`)

Dashboard básico com saúde geral do sistema. Painéis:

- **Workers Ativos** — `radiocheck_worker_active_total`.
- **Detecções por hora** — `increase(radiocheck_detections_total{status="confirmed"}[1h])` por estação.
- **Falhas de upload de evidência** — `radiocheck_evidence_upload_failures_total`.

Dependências: API Radiocheck expondo `/metrics` (porta 8080 default).

---

## Detecções (`deteccoes.json`)

Visão analítica do pipeline de detecção. Filtros: `station_id` (multi-select).

| Painel | Tipo | Query principal | Status |
|--------|------|-----------------|--------|
| Detecções por hora — total | timeseries | `sum(rate(radiocheck_detections_total{status="confirmed", station_id=~"$station_id"}[5m])) * 3600` | real |
| Detecções por estação (top 10) | bargauge | `topk(10, sum by (station_id) (rate(radiocheck_detections_total{status="confirmed"}[1h]) * 3600))` | real (TODO: por campanha exige label `campaign_id`) |
| Distribuição de duração de janela | heatmap | `sum by (le) (rate(radiocheck_match_window_duration_seconds_bucket[5m]))` | real (proxy para confidence) |
| Versões — supressões/retrações | stat | `sum by (action) (rate(radiocheck_match_disambiguation_total[1h]) * 3600)` | placeholder (Item A — §18.2.2) |
| Verificação neural — taxa | gauge | `sum(rate(radiocheck_match_neural_verifications_total[5m])) / clamp_min(sum(rate(radiocheck_detections_total[5m])), 1e-9)` | placeholder (§10) |
| Detecções por status | piechart | `sum by (status) (increase(radiocheck_detections_total[24h]))` | real |
| Latência de confirmação (p50/p95/p99) | timeseries | `histogram_quantile(0.95, sum by (le) (rate(radiocheck_match_window_duration_seconds_bucket[5m])))` | real (proxy — TODO `radiocheck_detection_confirmation_seconds`) |
| Versões — supressões 24h | stat | `sum(increase(radiocheck_match_disambiguation_total{action="suppressed"}[24h]))` | placeholder (Item A) |

### Métricas pendentes referenciadas

- `radiocheck_match_disambiguation_total{action}` — Item A (§18.2.2).
- `radiocheck_match_neural_verifications_total` — camada neural (§10).
- `radiocheck_detection_confirmation_seconds_bucket` — latência end-to-end.
- `radiocheck_match_confidence_score` — score por match (substituirá o heatmap de duração).

Painéis com placeholders mostram "Sem dados" até as métricas serem
instrumentadas — não são erros de configuração.

### Dependências

- API Radiocheck expondo `radiocheck_detections_total`, `radiocheck_match_window_duration_seconds`.
- Prometheus com retenção mínima de 24h para o painel "Detecções por status" (intervalo `[24h]`).

---

## Infra (`infra.json`)

Visão de saúde da infraestrutura. Filtro: `instance` (host).

| Painel | Tipo | Query principal | Dependência |
|--------|------|-----------------|-------------|
| CPU por container | timeseries | `sum by (name) (rate(container_cpu_usage_seconds_total{name=~"radiocheck.*"}[5m])) * 100` | cAdvisor |
| Memória por container | timeseries | `container_memory_working_set_bytes{name=~"radiocheck.*"} / 1024 / 1024` | cAdvisor |
| Uso de disco por mountpoint | gauge | `(1 - node_filesystem_avail_bytes / node_filesystem_size_bytes) * 100` | node_exporter |
| Throughput de rede | timeseries | `rate(node_network_receive_bytes_total[5m])` / `rate(node_network_transmit_bytes_total[5m])` | node_exporter |
| Postgres — commits/rollbacks | timeseries | `rate(pg_stat_database_xact_commit{datname="radiocheck"}[5m])` | postgres_exporter (TODO: substituir por histogram de query duration) |
| NATS — throughput | timeseries | `rate(gnatsd_varz_in_msgs[5m])` / `rate(gnatsd_varz_out_msgs[5m])` | nats_exporter (placeholder) |
| R2 — storage por tier | gauge | `radiocheck_evidence_storage_bytes` | API Radiocheck (real, §11.4) |
| Backup — horas desde último sucesso | stat | `(time() - radiocheck_postgres_backup_last_success_timestamp) / 3600` | API Radiocheck (real, §14.4) |
| Detection ingestion lag | timeseries | `time() - radiocheck_worker_last_pcm_timestamp` | placeholder |

### Exporters necessários no stack

Para que todos os painéis preencham, o `docker-compose` precisa incluir:

- `cadvisor` — CPU/memória por container.
- `node_exporter` — disco, rede, host metrics.
- `postgres_exporter` — `pg_stat_database`, latência de queries.
- `nats_exporter` (ou `prometheus_nats_exporter`) — apontando para o
  endpoint `/varz` do servidor NATS.

Se algum exporter não estiver presente, o painel correspondente exibe
"Sem dados (<exporter> requerido)" — esse é o comportamento esperado.

### Métricas pendentes referenciadas

- `radiocheck_db_query_duration_seconds_bucket` — substituirá o painel
  proxy de Postgres por uma medida real de p95 de queries.
- `radiocheck_worker_last_pcm_timestamp{station_id}` — necessário para o
  painel de ingestion lag detectar workers travados.

---

## Como recarregar Grafana após alterar JSONs

```bash
# Opção 1 — restart do container
docker compose -f infra/docker/docker-compose.yml restart grafana

# Opção 2 — aguardar polling do provider (default 10s)
# Nenhuma ação manual: o provider detecta mtime mudada e recarrega.

# Opção 3 — sinal SIGHUP via HTTP (em dev)
curl -X POST -H "Content-Type: application/json" \
  http://admin:admin@localhost:3000/api/admin/provisioning/dashboards/reload
```

## Convenções para novos painéis

- Use sempre `${datasource}` em `datasource.uid` — nunca hardcode UID literal.
- Adicione `description` em todo painel: explique a query e marque
  explicitamente quando a métrica ainda não existe (`TODO: ...`).
- `noValue` deve ter mensagem clara para placeholders ("Sem dados (X requerido)").
- Mantenha o painel-grade alinhado em múltiplos de 12 (largura) para
  layouts de duas colunas.
- `schemaVersion` mínimo: 38 (Grafana 10+).
