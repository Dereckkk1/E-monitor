---
status: implementado
ultima-verificacao: 2026-05-19
codigo-relacionado:
  - workers/internal/reqmetrics/writer.go
  - workers/internal/reqmetrics/middleware.go
  - workers/internal/reqmetrics/blocked.go
  - workers/internal/api/handlers/admin_monitoring.go
  - workers/internal/api/handlers/admin_monitoring_runtime.go
  - workers/internal/api/router.go
  - workers/cmd/api/main.go
  - migrations/0030_admin_monitoring.up.sql
  - migrations/0030_admin_monitoring.down.sql
  - frontend/src/pages/AdminMonitoringPage.jsx
  - frontend/src/pages/AdminMonitoringPage.css
  - frontend/src/utils/webVitals.js
  - frontend/src/main.jsx
  - frontend/src/components/Sidebar.jsx
  - frontend/src/App.jsx
---

# Painel /admin/monitoring

Painel admin de telemetria HTTP — espelho funcional do `/admin/monitoring` do
E-radios/Signalads, adaptado para a stack Go + React do Radiocheck. Mostra
performance por rota, identidades acessando (IPs × usuários) com risco
calculado, Web Vitals, top erros e requests lentos, com capacidade de bloquear
IPs e contas suspeitas.

**Quem usa**: somente role `admin`. Rota gateada via `RequireRole("admin")`
no router Go e `<RequireRole roles={['admin']}>` no frontend.

**Coexiste com**:
- `/monitoring` (≠ este) — "Saúde do Stream", uptime de emissoras.
- `/admin/overview` (≠ este) — pulso de infraestrutura (postgres/NATS/MinIO).

Cada um cobre uma camada diferente do sistema. Este aqui é sobre **tráfego
HTTP**: quem acessou, o que pediu, em quanto tempo respondemos, quantos
quebraram.

---

## Arquitetura

```
HTTP request
   │
   ▼
chi router
   │ middleware.RequestID
   │ middleware.RealIP
   │ middleware.Logger
   │ middleware.Recoverer
   │ middleware.Timeout(60s)
   │ corsMiddleware
   │ otelRoutePatternMiddleware
   │ reqmetrics.BlockMiddleware  ← rejeita IP banido (exceto /admin/*)
   │ reqmetrics.Middleware       ← captura sample, submete ao writer
   ▼
handler
   │
   ▼
response
   │
   ▼ (após handler, no defer do middleware)
reqmetrics.Writer.Submit(sample)
   │
   ▼ (canal buffered, 4096 slots)
goroutine flush
   │
   ▼ (batch ≤200 ou flush a cada 2s)
pgx.CopyFrom → system_metrics
```

O **caminho hot** (request HTTP) só faz uma escrita não-bloqueante no canal.
Se o canal estiver cheio, o sample é descartado e o counter `dropped` é
incrementado. Uma vez por minuto, se `dropped > 0`, o writer loga um WARN.
Pico de tráfego nunca pressiona a latência da request.

### Tabelas

| Tabela          | Função                                              | Retenção |
|-----------------|-----------------------------------------------------|----------|
| `system_metrics`| 1 linha por request HTTP (≠ /metrics, /health)      | 30 dias  |
| `blocked_ips`   | IPs banidos manualmente pelo painel                 | indefinido |
| `web_vitals`    | LCP/INP/CLS/FCP/TTFB postados pelo frontend         | 30 dias  |

Prune roda a cada 6h pela mesma goroutine do writer. DELETE com `LIMIT 50000`
em loop para evitar lock longo.

---

## Endpoints

Todos sob `/v1/internal/admin/monitoring/...` exigindo `Authorization: Bearer
<jwt-admin>`.

### Reads agregados

| Endpoint              | Resposta                                                   |
|-----------------------|------------------------------------------------------------|
| `GET /overview`       | total/errors/slow/avg + uptime/heap/RSS                   |
| `GET /routes`         | por rota: p50/p95/p99/avg/health                          |
| `GET /errors`         | top 50 (rota, status, count, lastOccurrence)              |
| `GET /slow`           | top 100 requests > 2s                                     |
| `GET /timeline`       | bucket por hora (1h/24h) ou dia (7d/30d)                  |
| `GET /vitals`         | LCP/INP/CLS/FCP/TTFB agrupado por (name, page)            |
| `GET /top-actors`     | top 300 (IP × user_id) com risco automático               |
| `GET /actor-detail`   | requests + timeline + top-routes de um ator               |
| `GET /blocked-ips`    | lista de IPs banidos                                       |

Todos aceitam `?range=1h|24h|7d|30d&hideLocalhost=true|false`. Default = 24h,
hideLocalhost=true.

### Mutations

| Endpoint                          | Efeito                                       |
|-----------------------------------|----------------------------------------------|
| `POST /block-ip`                  | Insere em `blocked_ips`, adiciona ao set RAM |
| `DELETE /block-ip/{ip}`           | Remove da tabela e do set RAM                |
| `POST /block-user/{userId}`       | `users.is_active = false` (ver caveat abaixo)|

### Endpoint público (autenticado)

| Endpoint                | Quem usa                                         |
|-------------------------|--------------------------------------------------|
| `POST /v1/internal/web-vitals` | qualquer usuário autenticado (frontend) |

Body esperado:
```json
{"name": "LCP", "value": 1240, "rating": "good", "page": "/campaigns"}
```

---

## Algoritmo de risco

Espelho do scoring do E-radios (`getRiskLevel` em `monitoringController.ts`):

| Sinal                          | Peso                                |
|--------------------------------|-------------------------------------|
| % de 404s                      | 0 → 40 (drive principal)            |
| Diversidade de rotas únicas    | 0 → 35 (mínimo 5 requests)          |
| Não autenticado                | +15                                 |
| Volume total                   | +5 a +10 (≥200, ≥500 reqs)          |

Threshold: low < 15 ≤ medium < 35 ≤ high < 60 ≤ critical.

**Por que volume é o último sinal**: usuários legítimos fazem muitos
requests. Bots se revelam pela diversidade de rotas inexistentes (probing),
não pela quantidade.

---

## Bloqueio de IP

`reqmetrics.BlockList` mantém o conjunto de IPs banidos em RAM e re-lê o DB
a cada 60s. Inserção via `BlockIP` handler chama `list.Add()` para propagar
imediatamente (não espera o tick).

O middleware `BlockMiddleware` checa o IP em todo request **exceto**:
- `/metrics` (Prometheus interno)
- `/v1/internal/health` (probes)
- `/v1/internal/auth/login` (admin precisa entrar mesmo se compartilha NAT)
- `/v1/internal/admin/*` (admin precisa poder desbloquear)

Rejeição: HTTP 403 `forbidden: ip blocked`.

---

## Bloqueio de usuário — caveat importante

`POST /block-user/{userId}` faz `UPDATE users SET is_active=false`. Isso
**barra novos logins** mas **não revoga JWTs já emitidos** — o token é HS256
sem revocation list, válido por 8h. O middleware `RequireJWT` só valida
assinatura, não consulta o DB.

**Janela máxima de exposição**: 8 horas (validade do token). Para corte
imediato, há duas opções (não implementadas hoje, ambas em follow-up):

1. Checar `is_active` em cada chamada de RequireJWT (overhead = 1 query/req).
2. Token blacklist com TTL no Redis (lookup ~1ms via redis-client).

Operacionalmente, se for um caso crítico (conta comprometida ativa), também
bloqueie o IP da pessoa para cortar a sessão na hora.

---

## Web Vitals

Coleta instrumentada em [`frontend/src/utils/webVitals.js`](../../frontend/src/utils/webVitals.js), inicializada em `main.jsx`. Sem dependência externa — usa `PerformanceObserver` nativo para LCP/FCP/CLS/INP e Navigation Timing API para TTFB. Cada métrica reporta no máximo uma vez por carregamento (LCP/CLS/INP emitem ao trocar de aba via `visibilitychange`).

Body do POST:
```json
{"name": "LCP", "value": 1240, "rating": "good", "page": "/campaigns"}
```

Rating é calculado no client conforme as faixas oficiais do Google (LCP bom <2.5s, ruim >4s, etc — ver `rate()` em `webVitals.js`).

**Limitação**: a métrica `INP` é uma aproximação via `PerformanceObserver('event')` com `durationThreshold: 16`. Para INP fiel ao spec (que considera processing time + presentation delay), trocar para o pacote `web-vitals` oficial. Considerar quando INP começar a guiar decisões operacionais.

---

## Operação

### Aplicar a migration

```bash
# Local (Windows dev)
migrate -path migrations -database "$DATABASE_URL" up

# Prod (na VM)
./scripts/deploy.sh   # já roda migrate via init container
```

### Comprovar que está coletando

Após subir, fazer uma chamada qualquer autenticada e:

```sql
SELECT COUNT(*), MAX(ts) FROM system_metrics;
```

Deve aparecer dado em menos de 2 segundos (FlushEvery default). Se ficar 0
após 30s de tráfego, ver o log da API por "reqmetrics: dropped samples" ou
"reqmetrics: flush failed".

### Tunables

Em `workers/cmd/api/main.go`, `metricsCfg`:

| Campo        | Default     | Quando mexer                                 |
|--------------|-------------|----------------------------------------------|
| BufferSize   | 4096        | Aumentar se "dropped samples" aparecer em log|
| BatchSize    | 200         | Subir pra 1000 se o DB aguentar              |
| FlushEvery   | 2s          | Reduzir se quiser dados quase em tempo real  |
| Retention    | 30 dias     | Subir requer mais disco; descer prune próxima janela |
| PruneEvery   | 6h          | Diário é OK pra retenção de 30d              |
| SlowMs       | 2000        | É o threshold de is_slow                     |

---

## Limitações conhecidas

1. **JWT sem revocation**: ver "Bloqueio de usuário — caveat".
2. **INP é aproximação**: ver seção Web Vitals.
3. **Sem distinção entre 5xx do app e timeout do middleware**: ambos
   contam como `is_error=true`. Para forensics fino, olhar a coluna
   `status_code` direto na tabela.
4. **`hideLocalhost=true` é heurístico**: filtra apenas
   `127.0.0.1`/`::1`/`localhost`/string vazia. IPs internos da rede docker
   (172.x.x.x) aparecem. Se incomodar, ajustar a lista em
   `hideLocalhostClause()` no handler.
5. **percentile_disc é exato mas escala O(n log n)** por grupo. Em rotas
   muito quentes (>50k reqs/24h), considerar amostragem ou trocar por
   `percentile_cont` aproximado via `tdigest` (extension, requer install).

---

## Referências

- E-radios original: `signalads-backend/src/controllers/monitoringController.ts` +
  `signalads-frontend/src/pages/MonitoringDashboard/index.js`.
- Design tokens: [docs/architecture/design.md](../architecture/design.md) §1.
- Pattern de coletor async batched: o webhook outbox usa estratégia similar
  (`workers/internal/webhook/`), mas com persistência síncrona porque a
  garantia de entrega é exigida — aqui priorizamos throughput.
