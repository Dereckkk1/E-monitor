# Follow-ups da Fase 2 — dívida técnica registrada

Itens identificados durante a Fase 2 (semanas 7–18) que **não bloqueiam o critério de saída** mas **devem ser resolvidos antes da Fase 3** (semanas 19–30, escala para 30→200 emissoras). Cada item lista o que, por quê, onde no código, e dependências.

A lista nasceu dos code-reviews das Etapas 2A (fingerprint batch), 2B (ciclo de vida de campanha), 2C (webhooks), 2D (backup + tiering) e do security review de 2026-05-07.

---

## Resolvidos pós-Fase 2

- **Migration runner automático** (2026-05-07): service `migrate` (golang-migrate) no `infra/docker/docker-compose.yml` aplica `migrations/*.up.sql` antes do `api` subir. `api` depende de `migrate: condition: service_completed_successfully`. Bootstrap de DB existente via `scripts/bootstrap-migrations.sh` (one-shot, idempotente, popula `schema_migrations` com `version=14, dirty=false`). Documentação completa em [`docs/migrations.md`](migrations.md). Resolve o problema histórico de migrations novas precisarem de `psql` manual a cada deploy.

## Resolvidos no security-review (2026-05-07)

Cinco fixes aplicados em sequência sobre `master` após varredura de segurança. Cada commit é independente e pode ser revertido isoladamente.

| # | Commit  | Fix |
|---|---------|-----|
| 1 | `5b36b64` | `fix(webhook): bloqueia SSRF via custom DialContext + validação de URL no PATCH` — `safehttp.go` resolve host, rejeita private/loopback/link-local antes do dial; `CheckRedirect` re-valida; PATCH valida no momento da config. Cobre 169.254.169.254 (cloud metadata), 10/172.16/192.168/127, link-local, IPv6 ULA/LL/multicast. |
| 2 | `91391ff` | `fix(auth): fixar algoritmo JWT em HS256` — `ParseToken` agora exige `Method.Alg() == "HS256"` em vez de aceitar qualquer `*jwt.SigningMethodHMAC`. Defesa em profundidade contra alg confusion. |
| 3 | `f476d79` | `fix(api): exigir role admin para mutações em webhook config e campaign cancel` — PATCH `/clients/{id}/webhook`, POST `/clients/{id}/webhook-test`, POST `/campaigns/{id}/cancel`, PUT `/campaigns/{id}/start`, PUT `/campaigns/{id}/pause` agora rodam atrás de `auth.RequireRole("admin")`. Reads (`GET /webhook`, `GET /webhook-deliveries`) seguem operator+admin. |
| 4 | `47853fa` | `fix(webhook): adiciona X-Radiocheck-Timestamp e assina timestamp+body (replay protection)` — passa a assinar `<unix_seconds>.<raw_body>` (formato Stripe-style) e emite `X-Radiocheck-Timestamp`. Receivers devem checar freshness ±5min. `docs/webhooks.md` atualizado com Go/Node/curl. |
| 5 | `36132e6` | `fix(webhook): rejeitar http:// por default (exceção localhost em dev)` — validador refusa `http://` salvo se `RADIOCHECK_ENV=development` E host loopback. Cobre tanto config (PATCH) quanto delivery (mesmo client) — sem URL `http` salva, dispatcher nunca a vê. |

---

## Bloqueadores antes da Fase 3 (escala)

### F-01. Advisory lock no TieringJob para multi-réplica
**Por quê:** com 2+ instâncias do `api` rodando (HA típico em produção), ambas executam o tiering às 03:00. UPDATE em row já movida retorna `RowsAffected()==0`, segunda réplica trata como "detection row vanished mid-move" e infla `EvidenceTieringErrors`. Em pior caso pode causar copy duplicado pra cold storage.
**Onde:** `workers/internal/evidence/tiering.go` (`Run()`).
**Como:** envolver `Run()` em `pg_try_advisory_xact_lock(<key>)`. Lock é por transação Postgres — abrir tx no início, lock, executar, commit. Documentar em `docs/backup-and-retention.md`.
**Dependência:** nenhuma. Pode ser feito a qualquer momento.

### F-02. Lockfile no `backup.sh` (já em parte resolvido)
**Por quê:** dois cron jobs simultâneos (ex: retry manual durante run em andamento) podem rodar `pg_basebackup` em paralelo, corrompendo dados ou inflando carga.
**Onde:** `infra/scripts/backup.sh`.
**Status:** parcialmente resolvido em fix pré-merge (Etapa 2D) — verificar se `flock` está realmente plumbado e funciona em runtime de cron.

### F-03. `CREATE INDEX CONCURRENTLY` em `detections_tier_created_at_idx`
**Por quê:** migration 0013 cria índice sem `CONCURRENTLY`, o que bloqueia escritas durante o build em tabela grande. Em `detections` com 100M+ linhas (esperado em Fase 3), causaria downtime de minutos a horas.
**Onde:** `migrations/0013_evidence_tier.up.sql`.
**Como:** dividir em duas migrations: 0013 (ALTER TABLE), 0014 (CREATE INDEX CONCURRENTLY com header `-- migrate:no-transaction`). Verificar suporte do migration tool.
**Dependência:** decisão sobre quando re-rodar (idealmente antes de tabela crescer).

### F-04. Worker de webhook: fan-out paralelo
**Por quê:** atualmente o batch de 10 deliveries é processado **serialmente** dentro de um tick de 5s. Com timeout de 10s por entrega, pior caso é 100s por tick — inaceitável quando taxa de detecção sobe.
**Onde:** `workers/internal/webhook/worker.go`.
**Como:** `errgroup` + semáforo (`golang.org/x/sync/semaphore`) com concorrência configurável (default 10). Cada delivery roda em goroutine, com timeout próprio.
**Dependência:** não. Mudança contida no pacote `webhook`.

### F-05. Outbox transacional para webhooks
**Por quê:** hoje a detecção é `INSERT detections` + `nc.Publish` em pontos distintos no `ingestor.Worker`. O `webhook.Deliverer` insere em `webhook_deliveries` ao receber NATS. Se o processo crasha entre `INSERT detections` e `nc.Publish`, o webhook nunca é enfileirado. NATS core não persiste, então mensagens em voo durante restart também são perdidas.
**Onde:** `workers/internal/ingestor/worker.go` (publish point) + `workers/internal/webhook/`.
**Como:** insert no outbox **na mesma transação** que persiste a detecção. NATS vira só "wakeup notification" para o worker pescar mais cedo. Worker continua pescando do DB também (independente de NATS).
**Dependência:** entender o fluxo de persistência atual de detecção. Risco médio.

### F-06. `Idempotency-Key` em webhooks entregues
**Por quê:** retries podem entregar a mesma detecção 2x se o cliente respondeu 200 mas o `markDelivered` falhou no DB. Cliente precisa estar idempotente sozinho.
**Onde:** `workers/internal/webhook/worker.go` (header montagem) + payload.
**Como:** adicionar header `Idempotency-Key: <delivery_id>` (mesmo valor de `X-Radiocheck-Delivery-Id`). Documentar em `docs/webhooks.md` que clientes devem usar pra dedup.
**Dependência:** pequena.

### F-07. Encryption do `webhook_secret` em DB
**Por quê:** secrets em plaintext violam best practices e potencialmente compliance. DB dump = vazamento. **Urgência elevada após security review (2026-05-07):** com tenancy real (F-50) operadores terão acesso amplo ao dump por design — secrets têm que estar cifrados antes disso. Hoje secrets viajam em texto claro também no backup nightly (ver F-51).
**Onde:** `migrations/` + `workers/internal/catalog/clients.go`.
**Como:** AES-GCM com KEK em variável de ambiente, ou dependência externa (Vault, KMS). Coluna nova `webhook_secret_encrypted` + migration de dados.
**Dependência:** definição de gestão de chaves.

### F-50. Tenancy real (operator ↔ client) substituindo "admin gate"
**Por quê:** os fixes F-3 do security review tornam mutações de webhook/cancel admin-only — solução PoC. Em produção, operadores precisam mutar configs **dos clientes que servem**, sem virar admin global. Sem isso, qualquer ajuste em config força promoção a admin (overprivileging).
**Onde:** novo modelo `operator_clients` (m2m) + middleware `auth.RequireClientAccess(clientIDFromPath)` em `workers/internal/auth/`. Endpoints já gateados a admin podem voltar a operator+admin com filtro de tenancy.
**Como:** (a) tabela `operator_clients(operator_id uuid, client_id uuid)`; (b) UI de admin para gerenciar mapeamento; (c) middleware extrai `client_id` da URL e checa `claims.UserID ∈ operator_clients(client_id)` OU `claims.Role=="admin"`.
**Dependência:** decisão de UX sobre como ofertar a tela de mapeamento.

### F-51. GPG-encrypt do tarball de backup antes de upload R2
**Por quê:** o backup nightly (`infra/scripts/backup.sh`) serializa o DB inteiro — incluindo `clients.webhook_secret`, `auth_users.password_hash`, dados sensíveis de detecção — e faz upload pra R2 sem encryption-at-rest controlada por nós. Comprometimento da chave R2 = leak total.
**Onde:** `infra/scripts/backup.sh` + provisão de keypair GPG.
**Como:** `gpg --encrypt --recipient backup-key < dump.sql.gz > dump.sql.gz.gpg` antes do `rclone copy`. Chave privada armazenada offline (cofre); chave pública no servidor de backup. Documentar runbook de restore em `docs/backup-and-retention.md`.
**Dependência:** F-07 (encryption de secrets em DB) atenua mas não substitui — outros campos ainda saem em claro.

### F-70. Leader election / queue group para supervisor permitir multi-réplica
**Por quê:** o subscriber `SubscribePendingDetections` no `supervisor` (§18.2.2 — desambiguação de versões) é uma subscription core NATS **sem queue group**. O dedup buffer (`workers/internal/supervisor/dedup_buffer.go`) é in-memory, por processo. Se 2+ instâncias do binário `cmd/api` rodam em paralelo, cada réplica recebe cópia de `detections.pending` e decide por si só → duplicação de `detections.confirmed`/`detections.retracted`. Hoje a Fase 2 assume **single-instance** (documentado em `docs/version-disambiguation.md`); HA é por failover, não por load balancer com réplicas ativas. Para Fase 3 (30→200 emissoras com HA real), isso vira bloqueador.
**Onde:** `workers/cmd/api/main.go` (chamada de `SubscribePendingDetections`) + `workers/internal/supervisor/disambiguation.go` (subscriber) + `workers/internal/supervisor/dedup_buffer.go` (buffer in-memory).
**Como:** duas opções: (a) queue group NATS (`SubscribeQueue("detections.pending", "supervisor")`) + buffer compartilhado (Redis ou tabela Postgres com TTL); (b) leader election via `pg_try_advisory_lock(<key>)` — só o líder consome `detections.pending`. Opção (b) é mais simples se já temos Postgres e nenhum motivo pra Redis.
**Dependência:** decisão sobre stack de coordenação (Redis vs Postgres advisory lock). Mesma decisão de F-60/F-61.

### F-08. Cobertura de testes ≥70% nas camadas críticas
**Por quê:** atualmente abaixo de 50% em `supervisor`, `ingestor`, `evidence`, `index`, `webhook`, `lifecycle_scheduler`. PoC tolera; produção não.
**Onde:** todo o `workers/internal/`.
**Status:** Etapa 2F do plano de execução. Já está na fila.

### F-80. Cobertura de testes ≥70% nas camadas críticas (parcial em Fase 2)

**Por quê:** §19.1 do plano exige ≥70% nas camadas críticas (matching, fingerprint, state machine, parsers); ≥85% no Match Engine. Estado atual após Item G (entrega parcial por rate limit do agent):
- match: 62.1% — perto da meta
- handlers: 35.0%
- webhook: 25.6%
- supervisor: 18.7%
- evidence: 17.1%
- ingestor: 17.0%
- index: 10.3%
- catalog: 8.2%

A baixa cobertura é **estrutural**: pacotes com dependências concretas (pgxpool, nats.Conn, ffmpeg, S3Client) não foram extraídos como interfaces, então o caminho I/O-bound só é testável com mocks viáveis após refactor de seams.

**Onde:** todos os pacotes acima.

**Como:**
1. Extrair interfaces mínimas (apenas o subset usado): ex `type StationsRepo interface { GetThreshold(ctx, id) (int32, error); ... }` em vez de depender de `*catalog.Stations` concreto.
2. Mocks com `gomock` ou hand-written via interface (consistente com `mockBus` em `lifecycle_scheduler_test.go`).
3. Para handlers: usar `httptest.NewRecorder` + `chi.NewRouter` populando `URLParam`.
4. Para evidence: criar `AudioEncoder` interface (envelopa ffmpeg shell exec).
5. Aproveitar testes já escritos pelo agent G (worktree merged como entrega parcial) e expandir.

**Dependência:** sem; mas afeta outros follow-ups que precisam de seams (F-XX abaixo).

**Estimativa:** 8-16h dedicadas, dependendo da profundidade dos refactors.

### F-81. Extrair interfaces mínimas para testabilidade

**Por quê:** vide F-80. Hoje o supervisor depende de `*catalog.Stations` concreto, evidence service depende de `*storage.S3Client` + ffmpeg shell exec, etc. Sem seams, mocks são impossíveis sem testcontainer.

**Onde:**
- `workers/internal/supervisor/supervisor.go` — recebe interfaces em vez de structs concretas.
- `workers/internal/evidence/service.go` — `AudioEncoder` (ffmpeg) + `BlobStore` (S3) interfaces.
- `workers/internal/ingestor/worker.go` — `StreamReader` interface (envelopa http get + ffmpeg input).
- `workers/internal/api/handlers/*.go` — handlers já recebem dependências; só falta documentar contratos mínimos.

**Como:** extrair interfaces no pacote consumidor (não no pacote de implementação). Ex: `supervisor` define `StationsRepo`, `Calibration` define `CatalogRepo`. Implementação concreta (`*catalog.Stations`) implementa tacitamente.

**Dependência:** decisão de estilo (interfaces locais vs centralizadas em `internal/contracts/`).

### F-82. Limpeza pós-merge dos testes do Item G

Sugestões do code-review do Item G (entrega parcial mergeada como `worktree-agent-a05865f29ac7968f8`):

1. **Confirmar guarda `<-ctx.Done()` no topo de `ingestor.Worker.Run`** para sustentar `TestRun_RespectsCancelledContext` sem precisar de timeout de 2s. Se não existir, adicionar (1 linha).
2. **Asserts positivos em `evidence/service_test.go::TestHandle_Invalid*`** depois que `Service` ganhar contador observable de "rejected" (vai sair junto com extração de interface, F-81).
3. **Podar `TestDedupBufferRetention_Constant` e `TestDefaultThresholdConstant`** — pinning de constantes não-públicas é tautologia. Manter só pinning de wire-format (subjects NATS, headers HTTP, event types).
4. **Unificar `RetractedEvent` struct** entre `supervisor/`, `webhook/`, `ingestor/` em uma lib comum (`internal/events/types.go`?), eliminando 3 testes JSON-shape duplicados.

### F-83. Limitações conhecidas do tracing (Item F)

1. **Match engine sem instrumentação interna.** `worker.window` no ingestor cobre a duração total da janela; lookup no índice e geração de candidate ficam dentro desse span sem subdivisão. Não é gap operacional sério — duração total + atributos `score`/`coverage` em logs já permitem investigar outliers.
2. **`evidence.process_async` e `webhook.deliver` em traces separados.** Async/decoupled por design (callback NATS retorna antes do upload S3 terminar; webhook outbox pode entregar minutos depois). `PropagateTraceContext` mantém o `trace_id` em comum, mas não há `parent_span_id` ligando os dois — aparecem como traces irmãos com mesmo trace_id no Jaeger. Documentado em `docs/tracing.md`.
3. **CLAP verifier sidecar (Python) não instrumentado.** Fora do escopo do Item F (Go-only). Se for instrumentar depois, usar `opentelemetry-instrumentation-fastapi` ou similar e injetar trace context no header HTTP da chamada do `neural/client.go`.

### F-84. `daily_play_summary` view performance com predicate pushdown

**Por quê:** a view criada na migration 0018 não suporta predicate pushdown de `campaign_id`. Em volume de produção (centenas de campanhas, milhões de detections/mês), queries filtradas por campanha vão materializar a view inteira antes de aplicar o filtro. Consultas de clientes no endpoint `GET /v1/internal/campaigns/:id/daily-summary` sofrem latência inaceitável quando há muitos dados históricos.

**Onde:** `migrations/0018_detections_categorization.up.sql` (criação da view) + endpoint em `workers/internal/api/handlers/campaigns.go`.

**Como:** promover pra MATERIALIZED VIEW com refresh incremental disparado pelo worker após INSERT/UPDATE em detections. Spec §5.4 já antecipa essa arquitetura — implementação deve seguir modelo de invalidação por campaign. Adicionar trigger ou update schedule no supervisor para disparar `REFRESH MATERIALIZED VIEW daily_play_summary WHERE campaign_id = $1` após confirmar detecção.

**Dependência:** decisão sobre trigger vs. refresh manual. Risco médio se view tornar grande.

**Validação:** rodar `EXPLAIN ANALYZE` com volume representativo (1M+ detections, 100+ campanhas) antes de mergear o Plano 3 (Detections refactor).

---

## Itens médios — fazer antes do crescimento real

### F-10. Race entre startup pass e scheduled pass do TieringJob (parcial)
**Status:** resolvido em fix pré-merge (`lastRun` setado após startup pass).
**Acompanhamento:** confirmar com observação de produção que não há mais double-runs.

### F-11. Refresh de gauges com `COUNT(*)` por status a cada tick
**Por quê:** `webhook_queue_size`, `webhook_dlq_size`, e `EvidenceStorageBytes` são atualizados via `SUM/COUNT` em tabela inteira. Com índice parcial é barato hoje, mas escala mal com 100k+ deliveries históricas e 100M+ detections.
**Onde:** `workers/internal/webhook/worker.go` (gauges) e `workers/internal/evidence/tiering.go` (`refreshStorageGauge`).
**Como:** view materializada refrescada N vezes/h, ou atualização incremental nos pontos de mudança (enqueue/markDelivered/markFailed).

### F-12. Pre-arming "T-5min" do scheduler de campanhas (R-A do plano)
**Por quê:** scheduler roda a cada 60s; campanha que ativa às 23:59:59 pode perder até ~1 min de "ar" inicial.
**Onde:** `workers/internal/supervisor/lifecycle_scheduler.go`.
**Como:** adiantar query de promoção para `start_date 00:00 - 5min` AT TIME ZONE 'America/Sao_Paulo'. Worker aceita pre-arming sem efeitos colaterais (ainda só monitora dentro da janela).

### F-13. Throttle quando muitas campanhas mudam de estado simultaneamente (R-B)
**Por quê:** se 50 campanhas terminam à meia-noite, scheduler dispara dezenas de `stopWorkersForCampaign` em paralelo. Pode causar reload de muitos workers concorrentes.
**Onde:** `workers/internal/supervisor/`.
**Como:** semáforo de concorrência no supervisor (max 5 reload paralelos), fila com priorização.

### F-14. Compose `backup` service — substituir por cron real
**Por quê:** entrypoint `while true; do sleep 86400; sh /backup.sh; done` é frágil (primeiro run só dia +1, sem retry).
**Onde:** `infra/docker/docker-compose.yml`.
**Como:** rodar `cron`/`crond` no container, ou (melhor) tirar o serviço backup do compose e rodar via cron do host conforme `infra/cron/postgres-backup.cron`.

### F-15. Tests do `LifecycleScheduler` e `CancelCampaign`
**Por quê:** componentes que rodam em background com SQL crítico não têm cobertura unitária.
**Onde:** `workers/internal/supervisor/lifecycle_scheduler_test.go` (criar), `workers/internal/catalog/campaigns_test.go` (estender).

### F-16. Audit log do cancelamento de campanha
**Por quê:** quem cancelou? Quando? Não há registro hoje além do log estruturado do scheduler.
**Onde:** `workers/internal/api/handlers/campaigns.go` (`Cancel`).
**Como:** insert em `audit_log` (tabela criada na migration 0009 da Fase 2).

### F-60. Rate-limit distribuído para API keys (substituir in-memory)
**Por quê:** `workers/internal/auth/apikey.go` aplica rate-limit em mapa em memória — funciona com 1 réplica, fura com 2+ atrás de LB (cada uma com sua própria contagem). Atacante com keys válidas pode multiplicar QPS proporcionalmente ao número de réplicas.
**Onde:** `workers/internal/auth/apikey.go`.
**Como:** `pg_advisory_xact_lock` por hash de key + contador em tabela `apikey_rate_limit`, OU Redis com `INCR` + TTL. Escolher Redis se já formos rodar Redis pra outra coisa; senão Postgres advisory lock é mais simples.

### F-61. Login rate-limit por IP/email
**Por quê:** `POST /v1/internal/auth/login` não tem proteção contra brute-force. Com a base de usuários da Fase 2 (operadores + admin) virando alvo, basta um script para tentar credenciais sequencialmente.
**Onde:** `workers/internal/api/handlers/auth.go`.
**Como:** janela móvel de 60s, max 10 tentativas por IP+email. Mesma stack escolhida em F-60 (Redis ou advisory lock).

### F-62. CORS whitelist em vez de wildcard
**Por quê:** `corsMiddleware` em `workers/internal/api/router.go` envia `Access-Control-Allow-Origin: *`. Combinado com `Authorization: Bearer ...` em browser-based callers, é permissivo demais — qualquer site pode disparar requests cross-origin que carregam credentials de outro site se o usuário estiver autenticado.
**Onde:** `workers/internal/api/router.go::corsMiddleware`.
**Como:** ler whitelist de `RADIOCHECK_CORS_ORIGINS` (CSV); se origem não bate, não emitir o header. Documentar no runbook como adicionar novos domínios.

### F-63. File size limit no fingerprint CLI antes do ffmpeg decode
**Por quê:** `workers/cmd/fingerprint/main.go` aceita arquivo de qualquer tamanho e passa pra ffmpeg. Upload malicioso multi-GB consegue: (a) encher disco do worker, (b) congelar pipeline (ffmpeg single-threaded por arquivo), (c) custar muita CPU.
**Onde:** `workers/cmd/fingerprint/main.go`.
**Como:** `os.Stat` antes de decode; rejeitar arquivos >100MB (configurável via env). Mensagem de erro clara orientando que o esperado é WAV de master, não master + bônus.

### F-64. UI: render de `webhook_deliveries.response_body` deve ser text-only
**Por quê:** `response_body` é capped em 4KB e persistido como string. Se o receiver retorna HTML/JS, a UI hoje pode renderizá-lo — risco de XSS armazenado quando operador abre tela de deliveries.
**Onde:** componente que mostra deliveries (provavelmente `frontend/src/pages/ClientsPage.jsx` ou modal de webhook).
**Como:** usar `<pre>` com texto plano + escape (`React` já escapa por default, confirmar que não há `dangerouslySetInnerHTML` no caminho).

---

## Itens menores — pode esperar Fase 3

### F-20. Filter chain do fingerprint vs. pré-processamento do stream (§8.4)
**Por quê:** o pipeline de fingerprint usa `loudnorm=I=-16, highpass=80, lowpass=7500`; o plano §7.2 etapa 3 prescreve `loudnorm=I=-23, highpass=100`. Hoje o stream worker não pré-processa, então não há regressão. Quando §8.4 (pré-processamento de stream) for implementado, **ambos os lados precisam usar exatamente a mesma chain**, senão matching degrada.
**Decisão:** documentar o que o stream vai usar **antes** de regenerar fingerprints; reprocessar masters depois.

### F-21. Variantes de broadcast simulation (`light`/`medium`/`heavy`)
**Por quê:** §7.2 do plano lista 3 variantes além da clean. Hoje só `clean` está implementada (outras retornam `ErrVariantNotImplemented`).
**Onde:** `workers/internal/fingerprint/audio.go::filterChain`.
**Como:** ffmpeg em 2 passos por variante (encode AAC → decode PCM) preservando artefatos do codec. Calibrar parâmetros com áudio real de emissoras.

### F-22. Multi-rate (§9.7)
**Por quê:** tolerância a time-stretching ±2% requer fingerprints adicionais com resampling.
**Onde:** `workers/cmd/fingerprint/main.go` (já aceita `--rate-id`) + `workers/internal/fingerprint/`.
**Como:** rerodar pipeline com `atempo=0.98` e `atempo=1.02`, gravar com `rate_id=1` e `rate_id=2`.

### F-23. Validação completa pós-geração §7.5
**Por quê:** atualmente cobre só densidade ≥30 hash/s e entropia ≥8 bits. Faltam re-match self, corpus de ruído (zero match), perturbações controladas.
**Onde:** `workers/internal/fingerprint/pipeline.go::Validate`.

### F-24. Watcher NATS para geração automática de fingerprint
**Por quê:** hoje fingerprint é invocado manualmente via CLI. Plano sugere daemon que escuta `commercial.created` / `commercial.master_updated` e dispara automaticamente.
**Onde:** novo `workers/cmd/fingerprint-watcher/`.

### F-25. Hot reload do índice após `Persist` do fingerprint
**Por quê:** após gerar fingerprint, índice em memória precisa recarregar — hoje exige restart.
**Onde:** publicar em `events.SubjectIndexReload` após `persist.go::Commit`.
**Estado:** já existe a infra; só plumbar.

### F-26. Métricas Prometheus do fingerprint pipeline
**Por quê:** §15.1 do plano lista métricas que ainda não são expostas (tempo de geração, hashes/s, taxa de validação falhada).
**Onde:** `workers/internal/fingerprint/` + `workers/internal/metrics/metrics.go`.

### F-27. Casos edge de teste no fingerprint
**Por quê:** silêncio puro, áudio < window size, saturação/clipping não estão cobertos.
**Onde:** `workers/internal/fingerprint/*_test.go`.

### F-28. `apikeys.SetWebhook` removido — confirmar
**Status:** resolvido em fix pré-merge. Apenas registrar como done.

### F-29. Stub `webhook_publisher.go` removido — confirmar
**Status:** resolvido em fix pré-merge. Apenas registrar como done.

### F-30. HTTPS exception para localhost dev em webhooks
**Por quê:** validador rejeita URLs não-HTTPS — desenvolvedor não consegue testar contra `http://localhost:3000`.
**Onde:** `workers/internal/api/handlers/webhooks.go` (validação).
**Como:** allowlist `localhost`, `127.0.0.1` quando `RADIOCHECK_ENV=development`.

---

## Como tratar este documento

- **Cada item aceita PR isolado.** Não esperar release coordenado.
- **Antes de iniciar Fase 3**, F-01 a F-08 devem estar resolvidos ou explicitamente aceitos como dívida.
- **Atualizar este arquivo** ao resolver: marcar item como `**Status:** resolvido em commit <sha>` e remover quando antigo.
- **Não documentar aqui o que já está em `plano_implementacao.md`.** Esse arquivo é apenas para itens identificados durante implementação que merecem rastreamento operacional.

---

## Foundations (Plano 1) — Follow-ups

- **F-85** — Padronizar comportamento de `Delete` / `Update` em repos do catalog. Hoje `material_types.Delete`, `materials.Delete`, `materials.UpdateType`, `campaign_materials.Unlink`, `campaign_materials.UpdateStations` retornam nil silenciosamente quando a linha não existe. `clients.Delete` retorna `pgx.ErrNoRows`. Decidir um padrão único (provavelmente loud — verificar `RowsAffected`) e aplicar consistentemente.
- **F-86** — Refatorar `MaterialsHandler.Upload` e `CommercialsHandler.Upload` extraindo helper compartilhado de SHA256 + storage + dispatch fingerprint num pacote `internal/upload/`. Atualmente duplicado.
- **F-87** — Adicionar `UNIQUE(client_id, master_sha256)` em `materials` após operador mesclar duplicatas via UI (futura).
- **F-88** — Implementar `probeDuration()` em `MaterialsHandler` via `ffprobe` (atualmente retorna 30.0 stub). Copiar lógica de `commercials.go` ou extrair pra helper compartilhado.
- **F-89** — Background job de re-categorização em `DistributionRulesHandler` não bloqueia o handler nem reporta status. Considerar fila NATS com worker dedicado se volume de detections crescer e re-categorização ficar > 1s.
- **F-90** — Deprecar `commercials.target_stations` e `commercials.campaign_id` em migration futura (0019+) após Planos 2 e 3 estarem em produção.
- **F-91** — Validação "future-only edit" (§8 da spec) em `DistributionRulesHandler.Update/Delete`. Atualmente backend aceita qualquer edição; validação fica na UI. Mover pro backend antes do Plano 2.
- **F-92** — Escape de metacaracteres LIKE (`%`, `_`, `\`) no parâmetro `q` de `Materials.ListByClient`. Atualmente vulnerável a injeção semântica (não SQL injection, mas comportamento inesperado). Sanitizar no handler layer.
- **F-93** — `ListApplicable` em `distribution_rules.go` documentou contrato de TZ (caller deve passar SP-local-midnight). Considerar mudar assinatura pra aceitar `string` "YYYY-MM-DD" pra remover ambiguidade no runtime.
