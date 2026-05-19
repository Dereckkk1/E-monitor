---
status: implementado
ultima-verificacao: 2026-05-19
codigo-relacionado:
  - workers/internal/observability/tracing.go
  - workers/internal/observability/nats.go
  - workers/cmd/api/main.go
  - infra/docker/docker-compose.yml
---

# Tracing distribuído (§15.3)

Este documento descreve a instrumentação de tracing OpenTelemetry implantada
nos workers Go do Radiocheck. Cobre o setup local, como ler um trace
end-to-end de uma detecção, a lista de spans gerados, e os trade-offs de
sampling que produção precisa fechar.

## Objetivos

- **Debug de outliers.** Um histograma diz que 1% das janelas de match
  passou de 50ms; o trace daquela janela específica diz por quê (lookup no
  índice lento, fila de NATS travada, GC pause).
- **Latência por hop.** Cada hop crítico (worker.window → publish_pending →
  supervisor.handle_pending → submit_detection → publishConfirmed → evidence.
  process_detection → s3_upload → webhook.deliver) vira um span filho da raiz,
  então a UI mostra exatamente onde a detecção gastou tempo.
- **Multi-réplica join.** Quando o ingestor está num pod, supervisor noutro
  e webhook noutro, traceparent na header NATS amarra tudo num único trace.
- **Linkar Prometheus → trace.** Histogramas críticos observam exemplares
  com o `trace_id` corrente. No Grafana basta clicar no ponto outlier e a
  data source Tempo/Jaeger abre o trace.

## Setup local

```bash
docker compose up jaeger api
```

A `infra/docker/docker-compose.yml` traz Jaeger all-in-one (collector + UI)
exposto em:

- `localhost:4317` — OTLP gRPC ingest (usado pelos workers).
- `localhost:4318` — OTLP HTTP ingest.
- `localhost:16686` — UI (`http://localhost:16686`).

A API exporta para `jaeger:4317` por padrão (variável `OTEL_EXPORTER_OTLP_
ENDPOINT` no compose). Para desligar tracing temporariamente basta apagar a
variável; o pacote `internal/observability` instala um `TracerProvider`
no-op nesse caso e o startup continua normal.

## Variáveis de ambiente

| Variável | Default | Significado |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | (vazio) | Endpoint OTLP gRPC. Vazio = no-op tracer. |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | (vazio) | Override só para traces. |
| `OTEL_SERVICE_NAME` | `radiocheck-api` | Resource attribute `service.name`. |
| `OTEL_SERVICE_VERSION` | `dev` | Resource attribute `service.version`. |
| `OTEL_TRACES_SAMPLER` | `parentbased_always_on` | Algoritmo. Aceita `always_on`, `always_off`, `traceidratio`, `parentbased_*`. |
| `OTEL_TRACES_SAMPLER_ARG` | `0.05` | Argumento (ratio) do sampler. Default reduzido em 2026-05-19 para mitigar I/O do Jaeger sob carga de 52+ workers. |
| `OTEL_RESOURCE_ATTRIBUTES` | (vazio) | Resource bag adicional, formato `k1=v1,k2=v2`. |
| `RADIOCHECK_ENV` | `development` | Mapeado para `deployment.environment`. |

## Sampling em produção

**Default a partir de 2026-05-19: `parentbased_traceidratio` com ratio `0.05`**
(5% dos traces capturados). Em volumes de ~52 workers cada um abrindo um
span `worker.window` a 0.5 Hz, sampling 1.0 produz ~26 spans/segundo de raiz
+ filhos, o que pressiona o coletor Jaeger e o disco (cada span ~1-5 KB
serializado). 5% mantém amostra estatística suficiente pra investigar
tendências sem saturar.

**Garantias do sampling 5%:**

- Detecções confirmadas (linha em `detections`) seguem 100% — não passam
  por sampler.
- Métricas Prometheus (`detections_confirmed_total`, `MatchWindowDuration`
  etc.) seguem 100% — não passam por sampler.
- Logs estruturados (zap) seguem 100% — não passam por sampler.

O sampling só decide se o **span OTel** vai pro Jaeger. Em incident response
ou debug específico, suba temporariamente:

```bash
OTEL_TRACES_SAMPLER_ARG=1.0 docker compose -f infra/docker/docker-compose.yml \
  -f infra/docker/docker-compose.override.yml \
  --env-file infra/docker/.env up -d --force-recreate --no-deps api
```

Lembre de baixar de volta quando terminar.

O `parentbased` garante que se a raiz foi sampleada, todos os filhos também
são — então quando um trace é capturado ele chega completo (worker → publish
→ supervisor → evidence → webhook).

## Lendo um trace de detecção end-to-end

Trace típico de um anúncio detectado em um stream de rádio:

```
worker.window  station_id=...           [240ms]
├─ worker.publish_pending                 [3ms]   commercial_short_id=42
└─ nats.publish detections.pending        [2ms]
   └─ supervisor.handle_pending           [22ms]  (consumer span)
      └─ supervisor.submit_detection      [20ms]  station_id=..., dedup.action=0
         ├─ dedup.lookup                  [4ms]
         ├─ dedup.evaluate                [<1ms]
         └─ nats.publish detections.confirmed
            ├─ evidence.process_detection [62s]   (espera janela + 2s)
            │  └─ evidence.process_async  [62s]
            │     ├─ evidence.extract_buffer    [3ms]
            │     ├─ evidence.ffmpeg_encode     [180ms]
            │     ├─ evidence.s3_upload         [420ms]   s3.key=evidences/...
            │     └─ evidence.persist_path      [12ms]
            └─ webhook.outbox_enqueue           [18ms]  client_id=...
               (mais tarde)
               └─ webhook.batch_tick            [—]
                  └─ webhook.deliver            [240ms]  attempt=1
                     └─ HTTP POST                [220ms] http.response.status_code=200
```

Notas:

- O span `worker.window` é a raiz do trace. Cada janela de matching abre
  uma raiz nova; janelas sem hit têm o span muito curto e sem filhos
  além de eventuais queries Postgres.
- `evidence.process_detection` aparece com duração ~62s porque a
  goroutine async deliberadamente espera a janela de evidência (60s pós-
  detecção) ser preenchida no ring buffer. Isso é esperado.
- `webhook.deliver` aparece num trace **separado** quando o tick do
  worker pega a row da outbox, mas o `parent_span_id` é o
  `webhook.outbox_enqueue` correspondente (link direto na UI).

## Lista de spans

| Span | Camada | Atributos principais |
|---|---|---|
| `radiocheck-api` | HTTP server (otelhttp) | `http.method`, `http.route`, `http.status_code` |
| `pgx.query.<sql>` | Postgres (otelpgx) | `db.system`, `db.statement` |
| `worker.window` | Ingestor | `station_id`, `results_count`, `duration_seconds` |
| `worker.publish_pending` | Ingestor | `station_id`, `commercial_short_id`, `confidence` |
| `nats.publish <subject>` | Producer | `messaging.system=nats`, `messaging.destination` |
| `supervisor.handle_pending` | Supervisor (consumer) | `messaging.system=nats` |
| `supervisor.submit_detection` | Supervisor | `station_id`, `commercial_short_id`, `detected_at`, `confidence`, `dedup.action` |
| `dedup.lookup` | Supervisor | — |
| `dedup.evaluate` | Supervisor | `dedup.action` |
| `detection.retract` | Supervisor | `station_id`, `commercial_short_id`, `reason`, `replacement_short_id` |
| `lifecycle.tick` | Supervisor | `activated_count`, `ended_count` |
| `supervisor.start_worker` | Supervisor | `station_id` |
| `threshold.refresh` | Supervisor | `station_id` |
| `evidence.process_detection` | Evidence (consumer) | `station_id`, `commercial_short_id` |
| `evidence.process_async` | Evidence | `detection_id`, `station_id` |
| `evidence.extract_buffer` | Evidence | `bytes` |
| `evidence.ffmpeg_encode` | Evidence | `bytes_in`, `bytes_out` |
| `evidence.s3_upload` | Evidence | `s3.key`, `bytes` |
| `evidence.persist_path` | Evidence | — |
| `webhook.outbox_enqueue` | Webhook (consumer) | `client_id`, `station_id`, `commercial_short_id` |
| `webhook.batch_tick` | Webhook | `batch_size` |
| `webhook.deliver` | Webhook | `delivery_id`, `client_id`, `attempt`, `event_type`, `http.response.status_code` |
| `calibration.scheduler_tick` | Calibration | `eligible_count`, `advisory_lock_acquired` |
| `calibration.recalibrate` | Calibration | `station_id` |

## Filtros úteis no Jaeger

- **Por estação:** Tags = `station_id="..."`
- **Por cliente:** Tags = `client_id="..."`
- **Erros:** marcadores `error=true` automáticos (qualquer span com
  `codes.Error`)
- **Detecções de uma campanha:** filtrar por
  `commercial_short_id="..."` na operação `supervisor.submit_detection`.

## Linkando Prometheus exemplar → trace

Histogramas que observam com exemplar:

- `radiocheck_match_window_duration_seconds` — uma janela lenta linka para
  o `worker.window` correspondente.
- `radiocheck_webhook_delivery_duration_seconds` — uma entrega lenta linka
  para o `webhook.deliver` correspondente.
- `radiocheck_calibration_duration_seconds` — uma recalibração lenta linka
  para o `calibration.recalibrate` correspondente.

No Grafana, configure a Prometheus data source com "Exemplars enabled" e
adicione um link interno para a Tempo/Jaeger data source mapeando
`{trace_id}` → URL do Jaeger:
`http://jaeger:16686/trace/${__value.raw}`.

## Limitações conhecidas

- **Match engine (`workers/internal/match/`) não está instrumentado
  internamente.** Razão: as assinaturas exportadas (`Update`,
  `MatchWindow`, etc.) não foram alteradas para preservar contrato com
  testes existentes e workers paralelos que importam o pacote. A duração
  total da janela é capturada por `worker.window` no ingestor (raiz por
  janela de match) — esse span engloba a chamada completa do engine e dá
  visibilidade suficiente em escala (atributos `results_count`,
  `duration_seconds`, mais `score`/`coverage` nos logs estruturados
  ligados pelo mesmo `trace_id`). Refatoração futura pode adicionar spans
  internos (`match.lookup`, `match.candidate`, `match.confirm`) quando o
  engine for tocado por outra razão; até lá não há ganho operacional que
  justifique o churn.

## Limites conhecidos

- Jaeger all-in-one mantém apenas 24h de traces em memória por padrão.
  Para retenção maior, trocar por Jaeger + Cassandra/Elasticsearch ou
  Tempo + S3.
- O ingestor `worker.Run` não abre um span raiz porque seria long-lived
  (horas/dias). Isso é deliberado — janelas individuais são a unidade
  útil. Se precisar correlacionar reconnects, use os logs de
  `worker.window` filtrados por station_id.
- O match engine (`match.MatchWindow`) não está instrumentado com spans
  internos para preservar a assinatura pública. A duração total já é
  capturada em `worker.window.duration_seconds`.
- Postgres queries iniciadas dentro de spans NATS-consumer aparecem
  como filhas; queries de jobs sem trace de origem (initial calibration
  job, restoration de campanhas) aparecem como raízes próprias.

## Trade-offs

- **OTLP gRPC vs HTTP:** escolhemos gRPC por ser mais eficiente e ser o
  default do Jaeger all-in-one. Se o operador precisar atravessar um
  proxy HTTP-only, basta trocar o exporter manualmente em
  `internal/observability/tracing.go`.
- **Sem otelnats:** o módulo oficial foi descontinuado durante a
  migração v0→v1 do contrib. Implementamos helper interno em
  `internal/observability/nats.go` (~80 linhas) que injeta/extrai
  trace-context via `nats.Header`. Quando uma versão estável do otelnats
  voltar, faz sentido migrar.
- **Sampling default 1.0:** ótimo em dev, perigoso em prod. Ajustar
  `OTEL_TRACES_SAMPLER_ARG=0.1` no rollout — ver seção de sampling
  acima.
