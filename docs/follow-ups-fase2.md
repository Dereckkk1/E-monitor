# Follow-ups da Fase 2 — dívida técnica registrada

Itens identificados durante a Fase 2 (semanas 7–18) que **não bloqueiam o critério de saída** mas **devem ser resolvidos antes da Fase 3** (semanas 19–30, escala para 30→200 emissoras). Cada item lista o que, por quê, onde no código, e dependências.

A lista nasceu dos code-reviews das Etapas 2A (fingerprint batch), 2B (ciclo de vida de campanha), 2C (webhooks) e 2D (backup + tiering). Confira PRs e branches `worktree-agent-*` para o histórico de decisão.

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
**Por quê:** secrets em plaintext violam best practices e potencialmente compliance. DB dump = vazamento.
**Onde:** `migrations/` + `workers/internal/catalog/clients.go`.
**Como:** AES-GCM com KEK em variável de ambiente, ou dependência externa (Vault, KMS). Coluna nova `webhook_secret_encrypted` + migration de dados.
**Dependência:** definição de gestão de chaves.

### F-08. Cobertura de testes ≥70% nas camadas críticas
**Por quê:** atualmente abaixo de 50% em `supervisor`, `ingestor`, `evidence`, `index`, `webhook`, `lifecycle_scheduler`. PoC tolera; produção não.
**Onde:** todo o `workers/internal/`.
**Status:** Etapa 2F do plano de execução. Já está na fila.

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
