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
| [design.md](architecture/design.md) | Design system frontend (compartilhado Radiocheck + E-radios/Signalads) |
| [fingerprint-pipeline.md](architecture/fingerprint-pipeline.md) | Pipeline offline que gera fingerprints acústicos (STFT → peaks → hashes) |
| [shared-hash-detection.md](architecture/shared-hash-detection.md) | Algoritmo bidirecional de classificação subset/sting/skip de curtos |
| [campaign-lifecycle.md](architecture/campaign-lifecycle.md) | Estados programada/ativa/concluida/cancelada + scheduler 60s |
| [distribution-rules.md](architecture/distribution-rules.md) | Regras de distribuição + categorização in_slot/out_slot/out_date/orphan + view daily_play_summary |
| [version-disambiguation.md](architecture/version-disambiguation.md) | Dedup pós-confirmação entre cortes 30s/60s do mesmo cliente |
| [evidence-audit.md](architecture/evidence-audit.md) | Re-fingerprint do clipe salvo × master antes de confirmar veiculação (§9.9) |
| [evidence-segments.md](architecture/evidence-segments.md) | ffmpeg escreve segmentos ADTS-AAC 30s; evidence extrai por timestamp |
| [frontend-design-system.md](architecture/frontend-design-system.md) | Tokens CSS, componentes reutilizáveis (.btn, .field, RSelect) |

## `features/` — feature implementada

| Doc | Sobre |
|-----|-------|
| [campaign-wizard.md](features/campaign-wizard.md) | Wizard de 4 etapas para criar/editar campanha |
| [detections-calendar.md](features/detections-calendar.md) | Grade station × dia da página /detections |
| [detections-view.md](features/detections-view.md) | Grade station × material × dia refatorada (Plano 3) |
| [webhooks.md](features/webhooks.md) | Entrega de eventos com HMAC-SHA256 + retry + outbox |
| [evidence-presigned-urls.md](features/evidence-presigned-urls.md) | URLs pré-assinadas de 5min pro frontend acessar evidências |
| [broadcaster-search.md](features/broadcaster-search.md) | Busca multi-token AND/field-OR de emissoras |
| [material-library.md](features/material-library.md) | Catálogo de materiais por cliente (decuplado de campanha) |
| [material-fingerprint-pipeline.md](features/material-fingerprint-pipeline.md) | Pipeline polimórfico (material_id OU commercial_id) + hot-reload |
| [material-similarity-warning.md](features/material-similarity-warning.md) | Alerta de duplicata por similaridade ≥50% no upload |

## `operations/` — operar o sistema em prod

| Doc | Sobre |
|-----|-------|
| [deploy.md](operations/deploy.md) | Guia completo de deploy da VM GCP + Docker Compose + Cloudflare Tunnel |
| [migrations.md](operations/migrations.md) | Runner automático golang-migrate + bootstrap idempotente |
| [auth-bootstrap.md](operations/auth-bootstrap.md) | Bootstrap admin via env vars + JWT HS256 + role gating |
| [tracing.md](operations/tracing.md) | OpenTelemetry OTLP gRPC → Jaeger |
| [dashboards.md](operations/dashboards.md) | 3 dashboards Grafana provisionados automaticamente |
| [calibration.md](operations/calibration.md) | Scheduler de calibração (initial 7d + recurring 24h) + advisory lock |
| [threshold-dynamic.md](operations/threshold-dynamic.md) | 3 caminhos que atualizam MatchThreshold atômico |
| [simulacao-radio.md](operations/simulacao-radio.md) | Simulador ffmpeg com 5 presets (fm-hifi → bad-stream) |
| [worker-commercial-reconciler.md](operations/worker-commercial-reconciler.md) | Reconciler 30s que detecta drift de stream_url e lista de comerciais + stall watchdog com grace de startup |
| [backup-and-retention.md](operations/backup-and-retention.md) | ⚠️ doc parcialmente desatualizado — descreve pg_basebackup, código usa pg_dump |
| [data-durability.md](operations/data-durability.md) | Modelo de ameaças + 5 camadas de defesa (bind mount, R2, snapshot GCP, alerta, drill) |

## `incidents/` — postmortems

| Doc | Quando | Sobre |
|-----|--------|-------|
| [incident-2026-05-09-jingle-falsepos.md](incidents/incident-2026-05-09-jingle-falsepos.md) | 2026-05-09 | Falso-positivo + saga F-108 v1→v2→v3 do algoritmo shared-hash |
| [incident-2026-05-12-pgdata-loss.md](incidents/incident-2026-05-12-pgdata-loss.md) | 2026-05-12 | Quase-perda do pgdata via `--force-recreate` + bind mount salvou o dado |
| [state-2026-05-12.md](incidents/state-2026-05-12.md) | 2026-05-12 | Snapshot handoff do final do dia (legado, contexto histórico) |
| [incident-2026-05-15-stream-url-snapshot.md](incidents/incident-2026-05-15-stream-url-snapshot.md) | 2026-05-15 | Workers presos em stream_url antiga — snapshot pattern + stall watchdog ignorava zumbi sem PCM |

## `roadmap/` — follow-ups e planos de fase

| Doc | Sobre |
|-----|-------|
| [follow-ups-fase2.md](roadmap/follow-ups-fase2.md) | Dívida técnica F-01 a F-121 (5 security fixes resolvidos, ~40 pendentes) |
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
