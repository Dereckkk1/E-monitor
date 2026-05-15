---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - workers/internal/api/handlers/system_health.go
  - workers/internal/api/router.go
  - workers/cmd/api/main.go
  - frontend/src/pages/AdminOverviewPage.jsx
  - frontend/src/pages/AdminOverviewPage.css
  - frontend/src/components/Sidebar.jsx
  - frontend/src/App.jsx
---

# Admin → Visão geral (`/admin/overview`)

Painel único de health do sistema inteiro: infra, observabilidade, workers, streams, pipeline de detecção e lista priorizada de "atenção agora". Foi desenhado pra ser a primeira parada do operador quando algo parece estranho.

## Por que existe

Antes desta tela, o operador precisava abrir três páginas e o Grafana pra entender o estado do sistema:

- `/operations` mostrava workers em memória
- `/monitoring` mostrava streams
- Grafana / Prometheus mostravam infra e métricas
- Postmortems mostravam o que pode quebrar — mas só em retrospectiva

Cada um exigia interpretação cruzada. `/admin/overview` consolida tudo em uma única visão com rollup de severidade e linkagem direta pra ferramenta de correção de cada problema.

## Quem pode ver

Admin only. A rota `/admin/overview` no React não tem gate explícito além do `RequireAuth`, mas o endpoint `GET /v1/internal/admin/system-health` é gated por `auth.RequireRole("admin")` em [workers/internal/api/router.go](../../workers/internal/api/router.go#L251). Operadores que tentarem acessar verão a tela carregar com erro 403 da API.

A sidebar esconde a categoria "Administração" inteira pra quem não é admin (ver `AdminNav` vs `ClientNav` em [frontend/src/components/Sidebar.jsx](../../frontend/src/components/Sidebar.jsx)).

## O que monitora

### Infraestrutura (5 serviços)

Direct-ping de cada dependência. Status: `ok` | `down` | `disabled` (env var não setada).

| Serviço | Como é checado | Origem |
|---------|----------------|--------|
| **PostgreSQL** | `pool.Ping(ctx)` | `DATABASE_URL` (sempre obrigatório) |
| **NATS** | `nc.IsConnected()` | `NATS_URL` (sempre obrigatório) |
| **Redis** | TCP dial em host:port | `REDIS_URL` (opcional) |
| **MinIO / S3** | TCP dial em host:port | `S3_ENDPOINT` (sempre obrigatório) |
| **CLAP verifier** | HTTP GET `/health` | `CLAP_VERIFIER_URL` (opcional) |

Por que TCP dial em vez de HTTP probe para MinIO/Redis: a stack suporta múltiplos backends S3-compatíveis (MinIO local, R2 em prod) e múltiplas configs Redis (com/sem auth, com/sem TLS). TCP reach é o sinal universal e cobre o caso de "daemon está vivo e a porta está aberta".

### Observabilidade (3 serviços, opcionais)

Não escalam severidade se estiverem fora — perder observabilidade é cego, não morto. Endpoints opcionais via env:

| Serviço | Endpoint probado | Env var |
|---------|------------------|---------|
| **Prometheus** | `GET /-/ready` | `PROMETHEUS_URL` |
| **Grafana** | `GET /api/health` | `GRAFANA_URL` |
| **Jaeger** | `GET /` | `JAEGER_URL` |

Sem env var setada, aparecem como "Não usa" (pill cinza) — não dispara alerta.

### Workers (KPI consolidado)

Cruza `stations.monitoring_status='active'` com `supervisor.WorkerStatuses()`:

- **Em execução** — worker registrado E produzindo PCM nos últimos 30s
- **Travados** — worker registrado mas sem PCM há mais de 30s OU nunca produziu PCM desde o boot
- **Ausentes** — station ativa no banco mas sem worker no supervisor (drift do reconciler — origem do incidente 2026-05-15)

`expected_active` = total de stations com `monitoring_status='active'`, é o denominador.

### Streams (KPI consolidado)

Consulta `stream_health_events` da última 6h:

- **Ao ar agora** — stations ativas sem evento `down` nos últimos 15min
- **Fora agora** — stations ativas com evento `down` mais recente que 15min
- **Quedas 24h** — total de eventos `down` nas últimas 24h

A janela de 15min existe porque `stream_health_events` é append-only sem evento `up` correspondente — o watchdog escreve novos eventos enquanto a queda persiste, então um evento "envelhecido" sinaliza recuperação implícita.

### Pipeline de detecção

- **Detecções 1h** — `COUNT(*) FROM detections WHERE detected_at > NOW() - INTERVAL '1 hour'`
- **Última detecção** — `MAX(detected_at)` (humanizado em "há Xmin/h/d")
- **Webhooks pendentes** — `webhook_deliveries.status IN ('pending','retry')`
- **Webhooks falhos 24h** — `status IN ('failed','dead')` criados nas últimas 24h

## Lista "Atenção agora"

Quando algo está quebrado, vira uma linha clicável no fim da página. Cada item carrega:

- **kind** — `worker_offline` | `stream_outage` | `infra_down` | `data_pipeline`
- **severity** — `warning` | `critical` (define cor da borda esquerda + ícone)
- **reason** — enum machine-readable: `not_registered`, `stalled`, `stream_down`
- **title** — nome da emissora afetada
- **detail** — frase humanizada explicando o motivo classificado
- **action_url + action_label** — onde clicar pra resolver (ex.: `/operations`)

### Regras de classificação

Cada station ativa cai em **um** bucket (mais específico ganha):

1. **`not_registered`** (critical) — station ativa, mas supervisor não conhece. Drift do reconciler. Ação: `/operations`.
2. **`stream_down`** (warning) — evento `down` recente em `stream_health_events`. Causa raiz é externa (rádio fora). Ação: `/monitoring`.
3. **`stalled`** (warning) — worker registrado sem PCM > 30s OU `last_pcm_at` zero (URL morta desde o boot). Ação: `/operations`.

Stations rodando saudáveis não geram item de atenção.

### Rollup de severidade

O banner no topo agrega tudo em uma label:

- **`critical`** — qualquer infra core (postgres, nats, minio) fora **OU** workers ausentes
- **`degraded`** — workers travados, streams fora agora, ou Redis/CLAP fora
- **`healthy`** — tudo ok

Observabilidade fora **não** escala severidade.

## Refresh e cache

- **Polling** — `useQuery` do React Query, `refetchInterval: 10_000` (10s)
- **Stale time** — 5s (não dispara refetch antes disso mesmo em refocus)
- **Botão manual** — ícone de refresh no canto superior direito força `refetch()` imediato
- **Indicador visual** — botão gira durante request em flight; "Atualizado há Xs" sempre visível

## Custo do endpoint

`GET /admin/system-health` faz:

1. 5 probes de infra **em paralelo** (goroutines) — cap individual 1.5s
2. 3 probes de observabilidade **em paralelo** — cap individual 1.5s
3. 3 queries SQL pequenas (active stations, stream events 6h, incidents 24h)
4. 3 queries SQL pra data pipeline (max(detected_at), detections 1h, webhook counts)

Total: ~2s no pior caso (todos os probes timeout). Tipicamente < 200ms quando tudo está saudável.

A função `summarizeRuntime()` é a única SQL-heavy — usa 3 queries com cap de 1 round-trip cada porque consulta tabelas indexadas (`stations(monitoring_status)`, `stream_health_events(event_at)`).

## Extensões futuras

Coisas que ficaram fora desta v1 mas cabem no mesmo endpoint:

- **Disk space** — host metrics via node-exporter; requereria proxy ou cache do Prometheus
- **Backup recente** — `radiocheck_postgres_backup_last_success_timestamp` está só no Prometheus hoje; ideal seria persistir em uma tabela `ops_backups` pra não criar dependência circular com Prometheus
- **Worker reasons mais granulares** — hoje classificamos `stalled` genericamente; com uma tabela `worker_errors` (last_error, last_http_status) daria pra dizer "HTTP 503 há 4min" vs "DNS NXDOMAIN" sem ler logs
- **Histórico** — sparkline de health agregado das últimas 24h; precisa de uma tabela `system_health_snapshots` escrita periodicamente

Nenhuma é obrigatória — todas escalonam a partir do mesmo endpoint quando o operador sentir falta.

## Como debugar quando o painel mostra algo errado

| O que está aparecendo | Onde olhar |
|------------------------|------------|
| Service "down" mas você acha que está ok | Ver `detail` no card; ver logs do container (`docker compose logs <svc>`) |
| Worker "ausente" | `/operations` mostra a lista do supervisor; reconciler logs em `radiocheck-api` (procurar `supervisor.reconcile`) |
| Worker "travado" sem stream caído | Bug do watchdog ou ffmpeg zombie — incidentes [2026-05-08](../incidents/) e [2026-05-15](../incidents/incident-2026-05-15-stream-url-snapshot.md) |
| Streams fora mas worker rodando | Stream realmente caiu na origem; ver runbook [StreamDownProlongado](../runbooks/) |
| Pipeline 0 detecções/h em horário ativo | Index não recarregou OU todos os workers travados; ver `radiocheck_index_reload_failed_total` no Prometheus |

## Não cobre

Por design, `/admin/overview` **não** mostra:

- Validação de input do usuário (campos vazios, datas inválidas) — isso é responsabilidade das telas de cadastro
- Erros HTTP 4xx individuais — esses vão pro Logger e ao próprio caller
- Métricas de negócio (faturamento, CPM, GRP) — `/dashboard` cuida disso
- Performance de queries específicas — isso é Jaeger + Grafana
