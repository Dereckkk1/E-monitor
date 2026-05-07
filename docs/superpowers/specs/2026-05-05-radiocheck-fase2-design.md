# Radiocheck Fase 2 — Design Spec

**Data:** 2026-05-05  
**Fase:** 2 — Hardening e Coexistência (Semanas 7–18)  
**Referência:** `plano_implementacao.md` §18.2, §8.5–8.7, §9.4, §9.7, §9.8, §10, §11, §13, §15, §16, §17  
**Branch:** `feature/fase2-hardening`  
**Critério de saída:** ≥95% concordância com fornecedor por 3 semanas consecutivas. Uptime ≥99% mensal. Latência p95 de confirmação <10s.

---

## Escopo

30 emissoras, 50 comerciais. Operação paralela em shadow mode com o fornecedor atual (§17.1 Fase A). A comparação de concordância é **manual e visual** — operador acessa plataforma do fornecedor e Radiocheck lado a lado.

---

## Grupo A — Scale Foundation (Supervisor + Confiabilidade)

### A1 — Restart Preventivo Escalonado (§8.7)

Ao iniciar cada worker via `Supervisor.Start()`, sorteamos um offset aleatório dentro da janela 3h–5h (7200–10800s desde meia-noite). Uma goroutine interna calcula `time.Until(nextRestart)` e dorme até lá. No momento do restart, chama `cancel()` do worker e relança. Ciclo: 24h.

Implementação: método `schedulePreventiveRestart(ctx, stationID)` interno ao Supervisor, lançado em goroutine no momento do `Start()`.

### A2 — Health Monitoring (§8.5)

O Supervisor mantém ticker de 30s que itera por todos os workers ativos e verifica se `worker.LastPCMAt()` foi atualizado nos últimos 60s. Se não: cancela o worker, registra evento `worker.stall` no log estruturado, relança. Métrica: `worker_stall_restarts_total` (counter por station_id).

O worker expõe `LastPCMAt() time.Time` via método público, atualizado a cada chunk PCM recebido do pipe ffmpeg.

### A3 — Matching Multi-Rate (§9.7)

O pipeline Python de fingerprint já gera 3 variantes por broadcast sim (compressão leve/média/pesada). A multi-rate adiciona **variantes de velocidade**: referência ressampleada a 0.97x e 1.03x via `sox tempo`. Cada variante de velocidade recebe `rate_id` distinto (0=nominal, 1=0.97x, 2=1.03x). Total de combinações: 3 variantes broadcast × 3 rates = 9 conjuntos de fingerprints por comercial.

No Match Engine, o histograma já agrupa por `(commercialID, variantID, rateID, deltaBin)` — nenhuma mudança de estrutura necessária. O índice simplesmente terá mais entradas.

### A4 — Desambiguação de Versões (§9.8)

Quando dois comerciais distintos (cortes 15s e 30s do mesmo conceito) confirmam no mesmo intervalo e emissora, o Match Engine escolhe o de maior duração que atingiu threshold de cobertura. Implementado como desambiguação pós-confirmação antes de emitir o evento NATS `detection.confirmed`.

---

## Grupo B — Calibração Adaptativa de Threshold (§9.4)

### B1 — Migration: tabela `station_thresholds`

```sql
CREATE TABLE station_thresholds (
    station_id          UUID PRIMARY KEY REFERENCES stations(id),
    calibration_mode    BOOLEAN NOT NULL DEFAULT true,
    calibration_started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    noise_samples       JSONB NOT NULL DEFAULT '[]',
    noise_p99           FLOAT,
    min_hashes          INT NOT NULL DEFAULT 5,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### B2 — Coleta de amostras durante calibração

Durante `calibration_mode = true`, o Match Engine registra o maior `HashCount` observado em cada janela (mesmo que não haja candidato) numa fila em memória. A cada 1000 amostras, o worker faz upsert em `noise_samples` via NATS→API.

### B3 — Cálculo do threshold

Após 7 dias (302400 janelas a 2s/janela), um job agendado calcula:
- `noise_p99` = percentil 99 do array de amostras em `noise_samples`
- `min_hashes = max(noise_p99 * 1.5, 5)`
- Atualiza `calibration_mode = false`

Endpoint `POST /internal/stations/{id}/calibrate` força reset para `calibration_mode = true` e reinicia contagem (§13.2).

### B4 — Uso do threshold no Match Engine

O worker carrega `min_hashes` da tabela ao iniciar. O Match Engine usa esse valor como piso para `HashCount` ao emitir candidato. Padrão=5 durante calibração.

---

## Grupo C — Verificação Neural CLAP (§10)

### C1 — Sidecar `clap-verifier` (Python + FastAPI)

Novo container `clap-verifier` no `docker-compose.yml`:
- Na inicialização: baixa `630k-audioset-best.pt` da LAION se não existir em volume, exporta para ONNX (`clap_model.onnx`), cacheia em volume montado
- Endpoint: `POST /embed` — recebe bytes PCM 16kHz mono (raw), retorna `{"embedding": [float, ...512]}`
- Latência alvo: <100ms por inferência (§10.5)
- Fallback gracioso: se modelo não carregou, retorna HTTP 503

### C2 — Migration: tabela `commercial_embeddings`

```sql
CREATE TABLE commercial_embeddings (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    commercial_id   UUID NOT NULL REFERENCES commercials(id) ON DELETE CASCADE,
    variant_id      SMALLINT NOT NULL,
    rate_id         SMALLINT NOT NULL DEFAULT 0,
    window_offset_ms INT NOT NULL,
    embedding       FLOAT[] NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX ON commercial_embeddings (commercial_id, variant_id, rate_id, window_offset_ms);
```

### C3 — Geração de embeddings no pipeline Python

O `fingerprint/fingerprint/generator.py` ganha função `generate_embeddings(audio_pcm, commercial_id, variant_id)`:
- Janelas deslizantes de 4s, hop 2s
- Para cada janela: POST para `clap-verifier:8080/embed`
- Persiste em `commercial_embeddings` via `persistence.py`

### C4 — Estado `StateUncertain` no Match Engine

A state machine ganha estado `StateUncertain`:
- Transição `StateDetecting → StateUncertain` quando cobertura está entre 0.4–0.6 após 3+ janelas
- Em `StateUncertain`: worker envia janela PCM atual via HTTP `POST clap-verifier:8080/embed`, recupera embedding de `commercial_embeddings` alinhado pelo delta, calcula cosine similarity
- `similarity > 0.85` → `StateDetected`
- `similarity < 0.70` → `StateIdle`
- Caso contrário: permanece `StateUncertain` por até 3 janelas adicionais, depois `StateIdle`
- Se sidecar retornar 503: o estado `StateUncertain` expira normalmente (§10.6)

---

## Grupo D — Evidence Service + R2 (§11)

### D1 — Troca MinIO → Cloudflare R2

O `storage/s3.go` já usa interface S3-compatible. A troca é apenas via variáveis de ambiente:
```
S3_ENDPOINT=https://<account-id>.r2.cloudflarestorage.com
S3_BUCKET=evidences
S3_ACCESS_KEY_ID=<r2-access-key>
S3_SECRET_ACCESS_KEY=<r2-secret>
S3_REGION=auto
```

### D2 — Fila local persistente com retry (§11.5)

Falha de upload: clip AAC fica em `/var/spool/radiocheck/evidence-queue/{detection_id}.aac`. Goroutine de retry tenta a cada 5min com backoff exponencial até 24h. Após 24h: alerta crítico, clip permanece no spool para intervenção manual. Métrica: `evidence_upload_failures_total`.

### D3 — Tiering após 30 dias (§11.4)

Job diário (ticker em goroutine no Evidence Service): lista detecções com `detected_at < NOW() - 30 days` e `evidence_status = 'available'`. Para cada uma: copia objeto R2 para bucket `evidences-cold` (mesma path), deleta do bucket quente, atualiza `evidence_status = 'archived'` no Postgres.

---

## Grupo E — API v1 Completa + Auth (§13, §16)

### E1 — API Keys para clientes (§16.1)

Migration:
```sql
CREATE TABLE api_keys (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    client_id     UUID NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    key_hash      CHAR(64) NOT NULL UNIQUE,  -- SHA-256 hex
    scopes        TEXT[] NOT NULL DEFAULT '{}',
    rate_limit    INT NOT NULL DEFAULT 60,   -- req/min
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at  TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ
);
```

Middleware Go: extrai `X-Api-Key` ou `Authorization: Bearer`, SHA-256 hasha, busca `key_hash` no DB. Rate limiting com sliding window em memória (`sync.Map` com contadores por minuto).

Endpoints:
- `POST /v1/clients/me/api-keys` — gera key (retorna raw uma vez)
- `DELETE /v1/clients/me/api-keys/{id}` — revoga

### E2 — JWT para usuários internos (§16.1)

Migration:
```sql
CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,  -- bcrypt cost 12
    role          TEXT NOT NULL CHECK (role IN ('admin','operator','viewer')),
    totp_secret   TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Endpoints:
- `POST /internal/auth/login` — valida email/senha bcrypt, retorna JWT (8h) + refresh token (7d)
- `POST /internal/auth/refresh` — troca refresh por novo JWT
- Middleware `RequireRole(roles...)` protege todas as rotas `/internal/`

### E3 — Webhooks (§13.1.4)

Migration:
```sql
ALTER TABLE clients ADD COLUMN webhook_url TEXT;
ALTER TABLE clients ADD COLUMN webhook_secret TEXT;  -- gerado pelo sistema, exibido uma vez

CREATE TABLE webhook_failures (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    client_id     UUID NOT NULL REFERENCES clients(id),
    detection_id  UUID NOT NULL,
    payload       JSONB NOT NULL,
    attempt       INT NOT NULL DEFAULT 1,
    next_retry_at TIMESTAMPTZ NOT NULL,
    last_error    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Goroutine `WebhookDeliverer` subscreve NATS `detection.confirmed`:
- Para cada cliente com `webhook_url` configurado para a campanha: POST com HMAC-SHA256
- Retry schedule: 1m, 5m, 15m, 1h, 4h — após 5 tentativas, grava em `webhook_failures` e para
- Worker de retry: ticker 1h, processa `webhook_failures` com `next_retry_at < NOW()`

### E4 — Paginação e rate limiting

Todos os endpoints de listagem: garantir que response inclui `pagination.total` (COUNT(*) na query). Índices adicionados onde faltarem. Rate limiting de API keys já cobre clientes externos; para rotas internas, sem rate limit adicional.

### E5 — Audit Log (§16.4)

Migration:
```sql
CREATE TABLE audit_log (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    actor_id    UUID,
    actor_type  TEXT NOT NULL,  -- 'user' | 'api_key'
    action      TEXT NOT NULL,
    resource    TEXT NOT NULL,
    resource_id UUID,
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- sem UPDATE/DELETE para o usuário da app
```

Middleware de audit log registra: criação/edição/exclusão de campanha, comercial, emissora, usuário. Geração/revogação de API key. Acesso a evidência.

---

## Grupo F — Observabilidade (§15)

### F1 — Métricas Prometheus

Endpoint `GET /metrics` no servidor Go (biblioteca `prometheus/client_golang`). Métricas completas conforme §15.1:

Workers (label `station_id`): `worker_bytes_received_total`, `worker_reconnects_total`, `worker_dead_air_events_total`, `worker_uptime_seconds`, `worker_buffer_evidence_seconds`, `worker_audio_rms_dbfs`, `worker_stall_restarts_total`.

Match Engine (label `station_id`): `match_windows_processed_total`, `match_window_duration_seconds` (histogram), `match_candidates_emitted_total` (labels: `status=confirmed|rejected|uncertain`), `match_neural_verifications_total`.

Evidence: `evidence_clips_generated_total`, `evidence_upload_duration_seconds` (histogram), `evidence_upload_failures_total`, `evidence_queue_size` (gauge).

Sistema: `system_active_workers` (gauge), `system_active_commercials` (gauge), `system_index_size_hashes` (gauge).

### F2 — Infraestrutura de observabilidade no Docker Compose

Novos containers em `infra/docker/docker-compose.yml`:
- `prometheus`: scrape do endpoint `/metrics` da API a cada 15s
- `grafana`: 3 dashboards provisionados como JSON em `infra/grafana/dashboards/`
  - **Operações:** mapa de saúde de emissoras, detecções/hora, latência p95
  - **Detecções:** taxa por campanha, confidence distribution, neural verifications
  - **Infra:** CPU/mem/disco, latência Postgres, throughput NATS
- `alertmanager`: config de roteamento (Slack por padrão; PagerDuty para críticos)

### F3 — Alertas (§15.5)

Regras Prometheus em `infra/prometheus/alerts.yml`:
- `StreamDownProlongado`: `worker_bytes_received_total` sem incremento por 10min → critical
- `MultipleStreamsDown`: >5% das emissoras ativas down → critical
- `EvidenceUploadFailures`: >10 falhas/hora → critical
- `NeuralVerifierDown`: clap-verifier não responde por 5min → warning
- `IndexReloadFailed`: falha de hot reload → critical
- `DiskSpaceCritical`: disco <5% livre → critical

### F4 — Runbooks (§15.6)

Arquivos em `docs/runbooks/`:
- `StreamDownProlongado.md`
- `EvidenceUploadFailures.md`
- `IndexReloadFailed.md`
- `NeuralVerifierDown.md`
- `DiskSpaceCritical.md`

Cada runbook: sintomas, causas comuns, comandos de diagnóstico, procedimento de correção, como escalar.

---

## Grupo G — Reliability + Backup (§14.4)

### G1 — Backup automático Postgres

Script `infra/scripts/backup.sh`:
```bash
pg_dump $DATABASE_URL | gzip > /backup/radiocheck-$(date +%Y%m%d).sql.gz
find /backup -name "*.sql.gz" -mtime +30 -delete
```

Execução: cron diário via container `backup` no docker-compose ou cron do host. Saída em volume `/backup` montado.

### G2 — Teste de restore

Script `infra/scripts/restore-test.sh`:
- Sobe container Postgres temporário
- Restaura último backup
- Verifica contagem mínima de tabelas e registros
- Log de resultado com timestamp

Execução mensal manual. Documentado em `docs/runbooks/backup-restore-test.md`.

### G3 — Testes de integração end-to-end

Testes em `workers/internal/integration/` (tag `//go:build integration`):
- Áudio de teste mockado (sine wave com fingerprint conhecido) → pipeline completo → detecção confirmada
- Usa MinIO local e NATS local (já no docker-compose de dev)
- Executado com `go test -tags=integration ./internal/integration/`

---

## Grupo H — Frontend Extensions

Extensão das páginas React existentes (sem novas páginas — só adições inline).

### H1 — Stations page

- Badge de status de calibração por emissora (`calibration_mode = true` → "Em calibração" amarelo, com dias restantes)
- Exibe `min_hashes` atual após calibração completa

### H2 — Clients page

- Seção "API Keys": lista keys existentes (hash mascarado), botão "Gerar nova key" (exibe raw uma vez em modal), botão "Revogar"
- Seção "Webhook": campo webhook_url + botão salvar, exibe secret gerado (mascarado, com opção "revelar")

### H3 — Monitoring page

- Status de saúde por worker: verde (PCM recebido <60s), vermelho (stall detectado), cinza (parado)
- Uptime e bytes recebidos por worker
- Status do `clap-verifier` (verde/vermelho baseado em health check)

---

## Banco de Dados — Resumo de Migrations

| Migration | Arquivo | Conteúdo |
|-----------|---------|----------|
| 0002 | `0002_fase2_thresholds.up.sql` | `station_thresholds` |
| 0003 | `0003_fase2_embeddings.up.sql` | `commercial_embeddings` |
| 0004 | `0004_fase2_auth.up.sql` | `users`, `api_keys` |
| 0005 | `0005_fase2_webhooks.up.sql` | `webhook_failures`, ALTER clients |
| 0006 | `0006_fase2_audit.up.sql` | `audit_log` |

---

## Estrutura de Arquivos Novos

```
workers/
  internal/
    auth/
      jwt.go               # geração e validação JWT
      middleware.go        # RequireJWT, RequireRole, RequireAPIKey
      apikeys.go           # geração, hashing, rate limiting
    webhook/
      deliverer.go         # goroutine de entrega + retry
    calibration/
      job.go               # cálculo de threshold após 7 dias
    neural/
      client.go            # HTTP client para clap-verifier
  match/
    statemachine.go        # + StateUncertain
    engine.go              # + desambiguação de versões, multi-rate
  supervisor/
    supervisor.go          # + schedulePreventiveRestart, health check ticker

clap-verifier/
  main.py                  # FastAPI app
  model.py                 # download, export ONNX, inferência
  Dockerfile

infra/
  docker/
    docker-compose.yml     # + prometheus, grafana, alertmanager, clap-verifier, backup
  prometheus/
    prometheus.yml
    alerts.yml
  grafana/
    dashboards/
      operacoes.json
      deteccoes.json
      infra.json
    datasources/
      prometheus.yaml
  scripts/
    backup.sh
    restore-test.sh

migrations/
  0002_fase2_thresholds.up.sql
  0002_fase2_thresholds.down.sql
  0003_fase2_embeddings.up.sql
  0003_fase2_embeddings.down.sql
  0004_fase2_auth.up.sql
  0004_fase2_auth.down.sql
  0005_fase2_webhooks.up.sql
  0005_fase2_webhooks.down.sql
  0006_fase2_audit.up.sql
  0006_fase2_audit.down.sql

docs/
  runbooks/
    StreamDownProlongado.md
    EvidenceUploadFailures.md
    IndexReloadFailed.md
    NeuralVerifierDown.md
    DiskSpaceCritical.md
  superpowers/
    specs/2026-05-05-radiocheck-fase2-design.md  (este arquivo)
```

---

## Critério de Conclusão da Fase 2

- [ ] `go build ./...` e `go test ./...` passam sem falhas
- [ ] `go test -tags=integration ./internal/integration/` passa
- [ ] Sistema sobe com `docker compose up` sem crash em 60s (incluindo clap-verifier)
- [ ] 30 workers ativos simultaneamente sem degradação de CPU/memória
- [ ] Detecção de comercial conhecido em stream real → evidência salva no R2
- [ ] Dashboard Grafana exibindo métricas em tempo real
- [ ] Webhook disparado e recebido após detecção confirmada
- [ ] Backup diário executando e restore-test passando
- [ ] ≥95% concordância com fornecedor por 3 semanas consecutivas
