# Documentação do Radiocheck

Índice geral. Todo doc tem header YAML no topo com `status`, `ultima-verificacao` e `codigo-relacionado`. Use isso para saber se um doc ainda reflete o código antes de confiar nele.

> **Auditoria de segurança (AppSec):** [AUDIT-2026-07-21.md](AUDIT-2026-07-21.md) — audit de segurança multi-agente (Anthropic-Cybersecurity-Skills): 2 críticos (BOLA na API externa `/v1/detections`, segredos em cleartext), 8 altos, 16 médios, 8 baixos. **Confidencial.**
> **Auditoria de detecção mais recente:** [AUDIT-2026-07-02.md](AUDIT-2026-07-02.md) — audit completo do sistema de detecção (notas 0–10 por área, 73 achados, plano P0/P1/P2). Anterior: [AUDIT-2026-05-15.md](AUDIT-2026-05-15.md).

---

## Estrutura

```
docs/
  README.md                — este índice
  AUDIT-2026-07-21.md      — auditoria de segurança AppSec (multi-agente, confidencial)
  AUDIT-2026-07-02.md      — auditoria do sistema de detecção (cabo a rabo)
  AUDIT-2026-05-15.md      — auditoria anterior (histórico, não substituir)
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
- **`superpowers/` e `archive/` são REGISTROS DATADOS, não referência de comportamento atual.** Um spec de maio descreve o que se decidiu em maio; se a regra mudou depois, o spec **continua dizendo a coisa antiga** — e está certo assim, é um registro. Nunca copie fórmula, contrato de API ou regra de negócio de lá pra código novo sem conferir contra o doc vivo em `features/` ou `architecture/`. Quando um desses artefatos passa a contradizer o sistema, ele ganha um bloco **"⚠️ REGISTRO HISTÓRICO"** no topo apontando pro doc que o substituiu — essa nota é a **única** edição permitida nesses arquivos.
- **Mudou o comportamento?** O doc vivo tem que dizer **o que era antes, o que é agora e desde quando** — não só o estado atual. Se um número que o cliente vê mudou, diga **em que direção** e por quê. Ver [features/quota-aware-categorization.md](features/quota-aware-categorization.md) como modelo do formato.
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
| [distribution-rules.md](architecture/distribution-rules.md) | Regras de distribuição + overrides + gatilhos de recategorização + view daily_play_summary (a **regra** de categorização está em features/quota-aware-categorization.md) |
| [version-disambiguation.md](architecture/version-disambiguation.md) | Dedup pós-confirmação entre cortes 30s/60s do mesmo cliente |
| [detection-count-consistency.md](architecture/detection-count-consistency.md) | Conjunto "aprovado" único (`catalog.ApprovedDetectionsFilter`) + matriz de toda query de contagem de veiculação + exceções deliberadas |
| [projection-category-invariant.md](architecture/projection-category-invariant.md) | Invariante `detection_campaigns.category` sempre igual ao veredito do categorizador: recat escopado por projeção + guarda da base + reconciler contínuo `projrecon` (caso motivador COPA 10/07) |
| [evidence-audit.md](architecture/evidence-audit.md) | Re-fingerprint do clipe salvo × master antes de confirmar veiculação (§9.9) |
| [evidence-segments.md](architecture/evidence-segments.md) | ffmpeg escreve segmentos ADTS-AAC 30s; evidence extrai por timestamp |
| [frontend-design-system.md](architecture/frontend-design-system.md) | Tokens CSS, componentes reutilizáveis (.btn, .field, RSelect), barra de filtros em passos (`.flow-filters`) e shell de estado vazio (`FlowEmptyState`) |

## `features/` — feature implementada

| Doc | Sobre |
|-----|-------|
| [campaign-wizard.md](features/campaign-wizard.md) | Wizard de 6 etapas para criar/editar campanha |
| [assertiveness-metric.md](features/assertiveness-metric.md) | Assertividade da plataforma (card da Visão Gerencial): quanto o matcher pegou sozinho vs o que foi digitado na mão. Janela = último mês **fechado** (o mês em curso mente pra cima); a métrica só vê o miss **reportado**, por isso o volume anda sempre junto do % |
| [cancelled-campaign-handling.md](features/cancelled-campaign-handling.md) | Campanha cancelada fora de seletores/telas ao vivo/KPIs/cobrança; histórico mantido+marcado, déficit congelado em `cancelled_at` (migration 0044) |
| [multi-attribution.md](features/multi-attribution.md) | F-119: mesma tocada conta p/ N campanhas (flag `MULTI_ATTRIBUTION`, tabela `detection_campaigns` + view `detection_attributions`) |
| [campaign-connection-step.md](features/campaign-connection-step.md) | Step 3 "Conexão" — testar (ping/stream/worker efêmeros) e trocar a stream_url por emissora |
| [detections-calendar.md](features/detections-calendar.md) | Grade station × dia da página /detections |
| [detections-view.md](features/detections-view.md) | Grade station × material × dia refatorada (Plano 3) |
| [detections-report-wysiwyg.md](features/detections-report-wysiwyg.md) | Relatório WYSIWYG de /detections (CSV/PDF espelham a grade filtrada — busca + programado + por dia; frontend-only) |
| [airtime-report.md](features/airtime-report.md) | `/reports/airtime` — lista cronológica de veiculações. Fluxo Cliente → Competência → **Campanhas (seleção múltipla)** → Período; cada linha diz de qual campanha veio; relatório continua sendo por campanha (seletor dentro do menu) |
| [quota-aware-categorization.md](features/quota-aware-categorization.md) | **Como uma veiculação vira in_slot/out_slot/out_date/bonus**: fechamento por cota da célula-dia (campanha × tipo × emissora × dia), `out_slot` não vale nada nem abate o déficit, `orphan`→`bonus`. Leia antes de mexer em categoria, déficit, bonificação ou base financeira |
| [detections-day-plan.md](features/detections-day-plan.md) | Bloco "Plano do dia" na DayDetailModal — faixas que valem no dia (janela · progresso · tocou/alvo), escopo por material, rodapé de faixas que não valem, saldo derivado |
| [manual-airings-bulk-and-proof.md](features/manual-airings-bulk-and-proof.md) | Veiculações manuais em lote + comprovante PDF (1 PDF→N) + censura tardia (subir áudio depois em /detections/:id) + rótulo /stations "Sem campanha ativa" |
| [materials-page.md](features/materials-page.md) | Tela `/materials` — materiais tocáveis por campanha + grade só-programado (Σ por emissora), admin + cliente |
| [user-management.md](features/user-management.md) | CRUD de usuários admin/cliente, /admin/users, /account, filtragem por client_id |
| [multi-client-user.md](features/multi-client-user.md) | Usuário de agência com vários clientes vinculados: carteira `user_clients`, escopo multi-cliente no JWT, seletor de cliente |
| [hub-sso.md](features/hub-sso.md) | Entrada pela Central de Clientes (E-Hub): `POST /v1/internal/auth/sso` + página `/sso`, código de uso único de 60s, JIT provisioning e `users.hub_id`/`clients.hub_id`. **Cliente sem `clients.hub_id` mapeado recusa com `client_not_provisioned` e não cria nada** — o preenchimento automático é da Fase 3 do RFC |
| [campaign-notification-emails.md](features/campaign-notification-emails.md) | 3 emails diários a admins: campanhas iniciando sem material / iniciando / terminando (janela <3d corridos ou ≤2d úteis, SMTP Workspace) |
| [post-sale.md](features/post-sale.md) | Pós-venda: admin monta o fechamento por cliente × campanhas × período, dispara email e o cliente abre em `/pos-venda/:token` — documento congelado com valor entregue, impactos, CPM, mapa, checking por emissora e zip dos relatórios |
| [client-deactivation.md](features/client-deactivation.md) | Desativar cliente (reversível) + delete bloqueado vira 409 com contagem de vínculos; login gating de cliente inativo |
| [webhooks.md](features/webhooks.md) | Entrega de eventos com HMAC-SHA256 + retry + outbox |
| [evidence-presigned-urls.md](features/evidence-presigned-urls.md) | URLs pré-assinadas de 5min pro frontend acessar evidências |
| [evidence-local-retention.md](features/evidence-local-retention.md) | Prune por idade do clipe de evidência no MinIO (§11.4 variante prod): apaga áudio >N dias (`EVIDENCE_RETENTION_DAYS`=30, dry-run pro 1º rollout) e marca a detecção `expired`; PDF de comprovante preservado (incidente 2026-07-02) |
| [broadcaster-search.md](features/broadcaster-search.md) | Busca multi-token AND/field-OR de emissoras |
| [anatel-station-class-coverage.md](features/anatel-station-class-coverage.md) | Classe Anatel da emissora (PBFM/PBOM) + municípios no raio, contorno protegido **+ transbordo (×1,5, mesmo fator do E-radios)**. AM fica sem raio de propósito (norma define em mV/m); comunitária é classificada por lei (1 km) |
| [client-contracted-stations.md](features/client-contracted-stations.md) | Recorte "emissoras que o cliente tem contratadas agora" em /stations (campanha ativa ou programada) + export CSV/PDF do conjunto |
| [material-library.md](features/material-library.md) | Catálogo de materiais por cliente (decuplado de campanha) |
| [material-fingerprint-pipeline.md](features/material-fingerprint-pipeline.md) | Pipeline polimórfico (material_id OU commercial_id) + hot-reload |
| [material-similarity-warning.md](features/material-similarity-warning.md) | Alerta de duplicata por similaridade ≥50% no upload |
| [material-upload-dedup-reuse.md](features/material-upload-dedup-reuse.md) | Reuso por `master_sha256` no upload: 201 = novo × 200 = dedup (não gera fingerprint nem similaridade), aviso na tela, re-upload não sobrescreve emissoras, limpeza do master órfão |
| [override-time-window.md](features/override-time-window.md) | Faixa horária por célula em distribution_overrides + popover com herança inteligente |
| [admin-system-overview.md](features/admin-system-overview.md) | Painel admin com health de toda a stack (infra + workers + streams + pipeline + atenção) |
| [admin-monitoring.md](features/admin-monitoring.md) | Painel `/admin/monitoring` — telemetria HTTP (rotas/p95/erros/slow), identidades (IP × usuário com risco), Web Vitals, bloqueio de IP/usuário |
| [admin-monitoring-user-journey.md](features/admin-monitoring-user-journey.md) | Aba `/admin/monitoring` → Jornada — fluxo de um usuário (telas/ações/horários por sessão), tradução rota→ação, sem backend novo |
| [admin-failures-daily.md](features/admin-failures-daily.md) | Aba "Por dia" de `/admin/station-failures` — série temporal de falhas por dia + padrão por terço do mês e por dia da semana; 3 métricas (emissoras/veiculações perdidas/tempo fora); clique na barra abre o dia em "Por emissora" |
| [login-page.md](features/login-page.md) | Tela `/login` com hero cinematográfico (globo + pulsos rosa) + form claro |
| [not-found-page.md](features/not-found-page.md) | Tela 404 fullscreen com cena Three.js (constellation map + torre wireframe + ondas de glitch) |
| [campaign-reports.md](features/campaign-reports.md) | Menu unificado de relatórios (CSV consolidado/detalhado + PDF com logo E-monitor) em /campaigns, /detections, /reports/airtime |
| [operations-page.md](features/operations-page.md) | Página `/operations` — supervisor ao vivo (bytes, reconnects, stall restarts, min_hashes) com wire contract de `GET /workers` |
| [connect-backoff-circuit-breaker.md](features/connect-backoff-circuit-breaker.md) | Backoff exponencial no respawn de stream que nunca conecta (IP bloqueado/URL morta) — para a sangria de connects que gerava ban de abuso (incidente jun/2026) |
| [station-audience-age-ranges.md](features/station-audience-age-ranges.md) | Faixa etária da emissora vira 3 percentuais (18-24/25-49/50+); texto antigo preservado em `ageRangeLegado` |
| [station-detail-modal.md](features/station-detail-modal.md) | Ficha read-only da emissora em `/stations` (clique na linha) — o caminho pelo qual o CLIENTE vê os dados de cada emissora; dados sensíveis e o botão Editar continuam admin-only |
| [geocoding-emissoras.md](features/geocoding-emissoras.md) | lat/long de emissoras por cidade+UF (dataset IBGE embutido + backfill); geocode no Create/Update |
| [material-specific-distribution-rules.md](features/material-specific-distribution-rules.md) | Escopagem de regras de distribuição a materiais específicos (carve-out via `material_ids[]`) — sobrescreve regras gerais do tipo |
| [client-target-pmm.md](features/client-target-pmm.md) | PMM no target por (cliente, emissora) (migration 0054) — impactos e CPM no target em /insights, /detections, /campaigns e relatórios; **base canônica única `pmm × (in_slot + bonus)` desde 2026-08-17**; ausência de linha ≠ `0`; cadastro em `/clients/:id/target-pmm` com colagem de planilha |
| [insights-dashboard.md](features/insights-dashboard.md) | `/insights` — KPIs + 4 gráficos + export PNG/PDF. **Os números caíram em 2026-08-17** (executado deixou de somar `out_slot`, fim do double-count do excedente, impactos passaram a `in_slot + bonus`); traz também as **divergências conhecidas e ACEITAS** entre `/insights` e `/campaigns` |
| [campaign-fixed-cpm.md](features/campaign-fixed-cpm.md) | CPM fixo opcional por campanha (Step 6 do wizard); o **CPM dinâmico soma investido + bonificado no numerador** — não "simplifique" (há guarda de teste) |
| [admin-station-failures.md](features/admin-station-failures.md) | `/admin/station-failures` — emissoras com falha no dia + campanhas com slots perdidos |
| [admin-campaign-failures.md](features/admin-campaign-failures.md) | Modo "Por campanha" + PDF de cobrança; desde 2026-08-17 o déficit vem partido em `deficit_absent` × `deficit_off_slot` (a emissora não pode ser acusada de ausência num dia em que veiculou fora do horário) |

## `operations/` — operar o sistema em prod

| Doc | Sobre |
|-----|-------|
| [deploy.md](operations/deploy.md) | Guia completo de deploy da VM GCP + Docker Compose + Cloudflare Tunnel |
| [capacity-and-unit-cost.md](operations/capacity-and-unit-cost.md) | Custo por emissora (médio × marginal × no teto), teto de capacidade da VM e como medir |
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
| [incident-2026-07-02-minio-storage-full.md](incidents/incident-2026-07-02-minio-storage-full.md) | 2026-07-02 | MinIO 100% cheio (`/mnt/data`) → PutObject 507 `XMinioStorageFull` → upload de evidência falha intermitente, UI culpa o "encoder". Causa: tiering hot=cold=archive no mesmo bucket nunca deleta → evidência acumula. 2 alertas mortos (métrica de upload nunca incrementada, DiskSpaceCritical só olhava `/`). Fix: retenção local (prune >30d, marca `expired`) + alertas + `expired` na UI (migration 0050) |
| [incident-2026-08-31-campaign-stations-wiped.md](incidents/incident-2026-08-31-campaign-stations-wiped.md) | 2026-08-31 | Campanha STIHL zerou `target_stations` pelo auto-save do Step 2 do wizard (disparava a partir de estado derivado, não de ação do usuário) — Step 4 anunciava "15 de 0 emissoras" porque `campaign_materials.target_stations` guardava a lista antiga. Dado restaurado a partir do link. Fix: gate de intenção (`dirty`) no save + chip honesto. Poda `link ⊆ campanha` no backend pendente |

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

> **Leia como registro datado, nunca como referência viva.** Um spec descreve o que era verdade **na data dele**. Doze artefatos daqui foram marcados com um bloco **"⚠️ REGISTRO HISTÓRICO"** em 2026-08-17 porque descreviam o modelo de categorização anterior (`orphan`, `deficit = expected − in_slot − out_slot`, `bonus = GREATEST(0, in_slot − expected) + orphan`, "três fórmulas de impactos") — todos apontam pra [features/quota-aware-categorization.md](features/quota-aware-categorization.md), e os 4 que têm header YAML ganharam `status: legado`. Acrescentar a nota de supersessão (e virar o `status` pra `legado`) é a **única** edição manual permitida nesses arquivos — o conteúdo em si nunca é reescrito.
