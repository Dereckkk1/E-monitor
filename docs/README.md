# Documentação do Radiocheck

Índice geral. Todo doc tem header YAML no topo com `status`, `ultima-verificacao` e `codigo-relacionado`. Use isso para saber se um doc ainda reflete o código antes de confiar nele.

> **Auditoria mais recente:** [AUDIT-2026-05-15.md](AUDIT-2026-05-15.md) — classificação por status + divergências encontradas.

---

## Estrutura

```
docs/
  README.md                — este índice
  AUDIT-2026-05-15.md      — última auditoria (histórico, não substituir)
  architecture/            — como o sistema funciona (conceitual, estável)
  features/                — feature implementada (uma feature por arquivo)
  operations/              — operar o sistema em prod
  incidents/               — postmortems e snapshots ligados a eles
  roadmap/                 — follow-ups, planos de fase, evaluations
  archive/                 — histórico que sobrevive mas não é referência ativa
  runbooks/                — procedimentos de resposta a alerta
  superpowers/             — specs e plans (artefatos de planejamento — não editar)
```

## Convenções

- Toda doc nova **deve** ter header YAML com `status`, `ultima-verificacao` (formato AAAA-MM-DD) e `codigo-relacionado` (lista de paths).
- Status: `implementado` | `parcialmente-implementado` | `legado` | `planejado`.
- Nunca documente feature nova no `plano_implementacao.md` (esse é blueprint, não changelog).
- Após implementar feature, criar/atualizar doc em `features/` ou `architecture/`.
- Após incidente, criar `incidents/incident-AAAA-MM-DD-{slug}.md`.

---

## `architecture/` — como o sistema funciona

| Doc | Sobre |
|-----|-------|
| [system-architecture-overview.md](architecture/system-architecture-overview.md) | **Comece aqui** — fluxogramas Mermaid de toda a arquitetura (visão geral, pipeline de detecção, state machine, NATS, modelo de dados, infra, frontend) |
| [design.md](architecture/design.md) | Design system frontend (compartilhado Radiocheck + E-radios/Signalads) |
| [fingerprint-pipeline.md](architecture/fingerprint-pipeline.md) | Pipeline offline que gera fingerprints acústicos (STFT → peaks → hashes) |
| [shared-hash-detection.md](architecture/shared-hash-detection.md) | Algoritmo bidirecional de classificação subset/sting/skip de curtos |
| [campaign-lifecycle.md](architecture/campaign-lifecycle.md) | Estados programada/ativa/concluida/cancelada + scheduler 60s |
| [distribution-rules.md](architecture/distribution-rules.md) | Regras de distribuição + categorização in_slot/out_slot/out_date/orphan + view daily_play_summary |
| [version-disambiguation.md](architecture/version-disambiguation.md) | Dedup pós-confirmação entre cortes 30s/60s do mesmo cliente |
| [detection-count-consistency.md](architecture/detection-count-consistency.md) | Conjunto "aprovado" único (`catalog.ApprovedDetectionsFilter`) + matriz de toda query de contagem de veiculação + exceções deliberadas |
| [evidence-audit.md](architecture/evidence-audit.md) | Re-fingerprint do clipe salvo × master antes de confirmar veiculação (§9.9) |
| [evidence-segments.md](architecture/evidence-segments.md) | ffmpeg escreve segmentos ADTS-AAC 30s; evidence extrai por timestamp |
| [frontend-design-system.md](architecture/frontend-design-system.md) | Tokens CSS, componentes reutilizáveis (.btn, .field, RSelect) |

## `features/` — feature implementada

| Doc | Sobre |
|-----|-------|
| [campaign-wizard.md](features/campaign-wizard.md) | Wizard de 6 etapas para criar/editar campanha |
| [multi-attribution.md](features/multi-attribution.md) | F-119: mesma tocada conta p/ N campanhas (flag `MULTI_ATTRIBUTION`, tabela `detection_campaigns` + view `detection_attributions`) |
| [campaign-connection-step.md](features/campaign-connection-step.md) | Step 3 "Conexão" — testar (ping/stream/worker efêmeros) e trocar a stream_url por emissora |
| [detections-calendar.md](features/detections-calendar.md) | Grade station × dia da página /detections |
| [detections-view.md](features/detections-view.md) | Grade station × material × dia refatorada (Plano 3) |
| [materials-page.md](features/materials-page.md) | Tela `/materials` — materiais tocáveis por campanha + grade só-programado (Σ por emissora), admin + cliente |
| [user-management.md](features/user-management.md) | CRUD de usuários admin/cliente, /admin/users, /account, filtragem por client_id |
| [campaign-notification-emails.md](features/campaign-notification-emails.md) | 3 emails diários a admins: campanhas iniciando sem material / iniciando / terminando (janela <3d corridos ou ≤2d úteis, SMTP Workspace) |
| [client-deactivation.md](features/client-deactivation.md) | Desativar cliente (reversível) + delete bloqueado vira 409 com contagem de vínculos; login gating de cliente inativo |
| [webhooks.md](features/webhooks.md) | Entrega de eventos com HMAC-SHA256 + retry + outbox |
| [evidence-presigned-urls.md](features/evidence-presigned-urls.md) | URLs pré-assinadas de 5min pro frontend acessar evidências |
| [broadcaster-search.md](features/broadcaster-search.md) | Busca multi-token AND/field-OR de emissoras |
| [material-library.md](features/material-library.md) | Catálogo de materiais por cliente (decuplado de campanha) |
| [material-fingerprint-pipeline.md](features/material-fingerprint-pipeline.md) | Pipeline polimórfico (material_id OU commercial_id) + hot-reload |
| [material-similarity-warning.md](features/material-similarity-warning.md) | Alerta de duplicata por similaridade ≥50% no upload |
| [override-time-window.md](features/override-time-window.md) | Faixa horária por célula em distribution_overrides + popover com herança inteligente |
| [admin-system-overview.md](features/admin-system-overview.md) | Painel admin com health de toda a stack (infra + workers + streams + pipeline + atenção) |
| [admin-monitoring.md](features/admin-monitoring.md) | Painel `/admin/monitoring` — telemetria HTTP (rotas/p95/erros/slow), identidades (IP × usuário com risco), Web Vitals, bloqueio de IP/usuário |
| [login-page.md](features/login-page.md) | Tela `/login` com hero cinematográfico (globo + pulsos rosa) + form claro |
| [not-found-page.md](features/not-found-page.md) | Tela 404 fullscreen com cena Three.js (constellation map + torre wireframe + ondas de glitch) |
| [campaign-reports.md](features/campaign-reports.md) | Menu unificado de relatórios (CSV consolidado/detalhado + PDF com logo E-monitor) em /campaigns, /detections, /reports/airtime |
| [operations-page.md](features/operations-page.md) | Página `/operations` — supervisor ao vivo (bytes, reconnects, stall restarts, min_hashes) com wire contract de `GET /workers` |
| [connect-backoff-circuit-breaker.md](features/connect-backoff-circuit-breaker.md) | Backoff exponencial no respawn de stream que nunca conecta (IP bloqueado/URL morta) — para a sangria de connects que gerava ban de abuso (incidente jun/2026) |
| [station-audience-age-ranges.md](features/station-audience-age-ranges.md) | Faixa etária da emissora vira 3 percentuais (18-24/25-49/50+); texto antigo preservado em `ageRangeLegado` |
| [geocoding-emissoras.md](features/geocoding-emissoras.md) | lat/long de emissoras por cidade+UF (dataset IBGE embutido + backfill); geocode no Create/Update |

## `operations/` — operar o sistema em prod

| Doc | Sobre |
|-----|-------|
| [deploy.md](operations/deploy.md) | Guia completo de deploy da VM GCP + Docker Compose + Cloudflare Tunnel |
| [segments-disk-migration.md](operations/segments-disk-migration.md) | Mover segmentos de evidência (`segmentsdata`) do disco de OS pro `/mnt/data` via `SEGMENTSDATA_HOST_PATH` |
| [migrations.md](operations/migrations.md) | Runner automático golang-migrate + bootstrap idempotente |
| [auth-bootstrap.md](operations/auth-bootstrap.md) | Bootstrap admin via env vars + JWT HS256 + role gating |
| [tracing.md](operations/tracing.md) | OpenTelemetry OTLP gRPC → Jaeger (sampling 5% por padrão em prod) |
| [profiling.md](operations/profiling.md) | pprof endpoint em loopback do container (CPU/heap/goroutines/mutex) |
| [dashboards.md](operations/dashboards.md) | 3 dashboards Grafana provisionados automaticamente |
| [calibration.md](operations/calibration.md) | Scheduler de calibração (initial 7d + recurring 24h) + advisory lock |
| [threshold-dynamic.md](operations/threshold-dynamic.md) | 3 caminhos que atualizam MatchThreshold atômico |
| [simulacao-radio.md](operations/simulacao-radio.md) | Simulador ffmpeg com 5 presets (fm-hifi → bad-stream) |
| [worker-commercial-reconciler.md](operations/worker-commercial-reconciler.md) | Reconciler 30s que detecta drift de stream_url e lista de comerciais + stall watchdog com grace de startup |
| [backup-and-retention.md](operations/backup-and-retention.md) | ⚠️ doc parcialmente desatualizado — descreve pg_basebackup, código usa pg_dump |
| [data-durability.md](operations/data-durability.md) | Modelo de ameaças + 5 camadas de defesa (bind mount, R2, snapshot GCP, alerta, drill) |
| [vendor-reconciliation.md](operations/vendor-reconciliation.md) | Método de comparação com o fornecedor externo: regimes do algoritmo, dedup 30/60s, audit_rejected, workflow por discrepância |

## `incidents/` — postmortems

| Doc | Quando | Sobre |
|-----|--------|-------|
| [incident-2026-05-09-jingle-falsepos.md](incidents/incident-2026-05-09-jingle-falsepos.md) | 2026-05-09 | Falso-positivo + saga F-108 v1→v2→v3 do algoritmo shared-hash |
| [incident-2026-05-12-pgdata-loss.md](incidents/incident-2026-05-12-pgdata-loss.md) | 2026-05-12 | Quase-perda do pgdata via `--force-recreate` + bind mount salvou o dado |
| [state-2026-05-12.md](incidents/state-2026-05-12.md) | 2026-05-12 | Snapshot handoff do final do dia (legado, contexto histórico) |
| [incident-2026-05-15-stream-url-snapshot.md](incidents/incident-2026-05-15-stream-url-snapshot.md) | 2026-05-15 | Workers presos em stream_url antiga — snapshot pattern + stall watchdog ignorava zumbi sem PCM |
| [incident-2026-05-17-audit-status-constraint.md](incidents/incident-2026-05-17-audit-status-constraint.md) | 2026-05-17 | Audit §9.9 deixava detections órfãs em `pending` (CHECK constraint sem `audit_rejected`) — áudio 404 |
| [incident-2026-05-22-stale-master-catalog.md](incidents/incident-2026-05-22-stale-master-catalog.md) | 2026-05-22 | Material trocado no ar sem atualizar o catálogo — master desatualizado |
| [incident-2026-06-12-detection-recall-gaps.md](incidents/incident-2026-06-12-detection-recall-gaps.md) | 2026-06-12 | Gap de detecções vs fornecedor: re-fingerprint incompleto (32 materiais degradados 08-12/06), fila de fingerprint sem retry (8 nunca monitorados), audit_rejected invisível, Jovem Pan geo-block + Favorita 404 |
| [incident-2026-06-17-migration-0039-dirty.md](incidents/incident-2026-06-17-migration-0039-dirty.md) | 2026-06-17 | Backfill 0039 colidiu em `materials.short_id UNIQUE` em prod (DB local vazio deu falso verde) → schema dirty → deploy travado. Defesa: teste de migrations em sombra no deploy.sh + 0024 endurecida contra DB vazio |
| [incident-2026-06-22-s3-checksum-upload-failure.md](incidents/incident-2026-06-22-s3-checksum-upload-failure.md) | 2026-06-22 | AWS SDK v2 (checksum CRC default) quebra todo PutObject contra MinIO sem TLS → upload/tiering de evidência falham → `failed` na modal + audit pulado faz "15 contar como 30". Bug latente no go.mod, ativado pelo 1º rebuild. Fix: `RequestChecksumCalculation=when_required` |
| [incident-2026-06-25-uniube-cross-campaign-misattribution.md](incidents/incident-2026-06-25-uniube-cross-campaign-misattribution.md) | 2026-06-25 | UNIUBE: mesmo áudio (master_sha256 igual) em 2 campanhas sobrepostas compartilhando emissora → desambiguação §18.2.2 elege o menor short_id, a outra campanha zera. Fix manual (forward+backfill+recat, 2 emissoras/75 veic.) + fix definitivo F-119 multi-atribuição (tabela `detection_campaigns`, flag `MULTI_ATTRIBUTION`) |

## `roadmap/` — follow-ups e planos de fase

| Doc | Sobre |
|-----|-------|
| [follow-ups-fase2.md](roadmap/follow-ups-fase2.md) | Dívida técnica F-01 a F-121 (5 security fixes resolvidos, ~40 pendentes) |
| [2026-06-12-plano-remediacao-recall.md](roadmap/2026-06-12-plano-remediacao-recall.md) | Plano-mestre pós-incidente: 3 ondas (falhas silenciosas → recall do algoritmo → infra/processo), 13 tasks ordenadas |
| [detection-evaluation-report.md](roadmap/detection-evaluation-report.md) | Avaliação E2E do matcher; recomendações 4.1/4.4/4.5/4.7 aplicadas |

## `archive/` — histórico inativo

| Doc | Sobre |
|-----|-------|
| [status-e-roadmap.md](archive/status-e-roadmap.md) | Snapshot Fase 1 PoC já concluída (canônico está em plano_implementacao.md) |
| [prompt-fix-poc-fase1.md](archive/prompt-fix-poc-fase1.md) | Prompt de sessão concluída (10 fix-groups Fase 1) |

## `runbooks/` — resposta a alertas

Ver [runbooks/README.md](runbooks/README.md) para o índice. Cada alerta em `infra/prometheus/alerts.yml` tem (ou deveria ter) um runbook correspondente. Estado atual:

- **9 implementados:** CalibrationStale, DetectionRateAnomaly, DiskSpaceCritical, EvidenceQueueGrowing, EvidenceUploadFailures, MultipleStreamsDown, StreamDownProlongado, WorkerHighMemory, README
- **2 parcialmente:** ClockDrift (depende node_exporter externo), IndexReloadFailed (alerta não existe)
- **2 planejados:** DBLatencyHigh, LifecycleSchedulerStuck (alertas comentados TODO)
- **1 sem alerta:** NeuralVerifierDown (verificação manual via /health)

## `superpowers/` — artefatos de planejamento

Pasta gerenciada pelo workflow `superpowers:*`. Não editar manualmente — specs e plans são snapshots imutáveis de sessões de design/implementação.
