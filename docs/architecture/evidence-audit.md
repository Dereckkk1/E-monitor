---
status: implementado
ultima-verificacao: 2026-06-16
codigo-relacionado:
  - workers/internal/audit/auditor.go
  - workers/internal/evidence/service.go
  - workers/internal/metrics/metrics.go
  - migrations/0028_audit_rejected_status.up.sql
  - migrations/0029_daily_summary_exclude_audit_rejected.up.sql
---

# Evidence Audit (§9.9 — Audit de Evidência Pré-Persist)

Camada de verificação determinística que roda sobre o **clipe de evidência salvo** antes de marcar a veiculação como confirmada. Implementa a invariante:

> Se o sistema marca uma veiculação como confirmada, o áudio salvo como evidência **tem que conter** o comercial reivindicado.

Caso a invariante seja violada, a detecção é registrada com `evidence_status = 'audit_rejected'` e é automaticamente ocultada dos endpoints client-facing — vira dado forense para operadores investigarem o motivo.

Plano arquitetural: [`plano_implementacao.md` §9.9](../../plano_implementacao.md#L979).

---

## Quando o audit roda

Dentro de `internal/evidence/service.go::processEvidence`, após o ffmpeg ter finalizado o segmento de áudio no disco e os bytes ADTS-AAC terem sido extraídos. Antes do encode m4a e do upload pro MinIO.

```
state machine confirma
  → NATS detections.pending
  → supervisor faz §9.8 (desambiguação)
  → NATS detections.confirmed
  → evidence service:
       INSERT detection (evidence_status='pending')
       async: aguarda janela completar
       extrai segmentos AAC do disco
       decodifica para PCM 16k mono float32
       ┌──────────────────────────────────────────┐
       │ §9.9 AUDIT                               │
       │   - carrega hashes do master do DB       │
       │   - gera hashes da evidência (mesmo      │
       │     pipeline: HPF → RMS → STFT → peaks   │
       │     → constellation hashes)              │
       │   - histograma de delta por (var, rate)  │
       │   - cobertura = distinct_master_frames   │
       │                 / total_master_frames    │
       │   - passa se score >= 5 AND cov >= 0.4   │
       └──────────────────────────────────────────┘
       passa  → encode m4a, upload S3, evidence_status='available'
       falha  → evidence_status='audit_rejected', sem upload
       erro   → log + métrica result=error, segue upload (fail-open)
```

## Kill switch

Em emergência (ex.: audit começa a rejeitar tudo por bug), desligue via env var no `.env` da VM:

```
AUDIT_ENABLED=false
```

Default é `true`. Quando desligado, o evidence service pula o passo inteiro — comportamento idêntico ao pré-§9.9. O log de boot anuncia o estado:

```
INFO  audit enabled (§9.9 pre-persist evidence audit)
# ou
WARN  audit DISABLED via AUDIT_ENABLED=false — all detections will be persisted regardless of evidence quality
```

## Thresholds

| Constante | Valor | Origem |
|---|---|---|
| `DefaultMinScore` | 5 | §9.3 `MATCH_THRESHOLD` |
| `DefaultMinCoverage` | 0.15 | §9.3 `MIN_COVERAGE` (idêntico ao live `state machine minTemporalCoverage`) |
| `DeltaBinSize` | 2 | §9.3 (compartilhado com `internal/match`) |
| `coverageBinRadius` | 2 | une os frames distintos do master no bin de pico ±N ao medir cobertura — tolera drift de playout em spots longos (fix 2026-06-16) |
| `coverageBypassScore` | 30 | score que **dispensa** o gate de cobertura em master **sem** hash compartilhado — degradação de broadcast num 30s deixa score altíssimo mas cobertura baixa (fix 2026-06-16) |

**Importante:** o threshold do audit DEVE espelhar o threshold do matching live. Se o audit for mais rigoroso que o produtor (live), ele vai rejeitar detecções que o live já tinha aceitado — situação que destruiu 104 detections em prod entre 15/05 e 17/05 quando o default foi acidentalmente shippado em 0.4 (incidente 2026-05-17). Audit é *guardrail contra mismatch grosseiro* entre clipe salvo e master atribuído, não filtro independente.

Calibração por emissora (análogo ao §9.4 do matching live) fica fora de escopo. Construtor expõe override no `NewAuditor(db, log, minScore, minCoverage)` para testes futuros.

## Métricas

Expostas no `/metrics` do API (Prometheus). Todas com prefixo `radiocheck_audit_`.

| Métrica | Tipo | Labels | Significado |
|---|---|---|---|
| `radiocheck_audit_attempts_total` | counter | `result` (`passed` \| `rejected` \| `error`) | Quantos audits rodaram e como terminaram |
| `radiocheck_audit_score` | histogram | — | Score (peak count do histograma) por audit |
| `radiocheck_audit_coverage` | histogram | — | Cobertura temporal por audit |
| `radiocheck_audit_duration_seconds` | histogram | — | Latência do audit (decode + match) |

**Alertas sugeridos** (adicionar a `infra/prometheus/alerts.yml` em PR futuro):

- Taxa de `result=rejected` > 5% em janela de 1h → bug provável ou regressão de calibração de threshold de alguma emissora; investigar imediatamente.
- Taxa de `result=error` > 1% em 1h → infra do audit degradada (DB ou ffmpeg); audit virou fail-open, perdemos a invariante.

## API: o que cliente vê

Detecções com `evidence_status = 'audit_rejected'` são **excluídas** das queries `LIST` da `internal/catalog/detections.go`. Cliente não vê. Operador que abrir o `GET /detections/{id}` diretamente VÊ a detecção rejeitada (preserva forense).

Filtros adicionados (todos no SQL):

```sql
AND d.evidence_status <> 'audit_rejected'
```

Aplicado em `ListPaged`, `List` legacy, `Export` (handlers Go) e no CTE `actual` da view `daily_play_summary` (migration 0029). Não aplicado em `GetByID` (acesso direto preserva visibilidade pra operadores).

> Entre 17/05 e 18/05 o filtro na view ficou faltando — handlers Go já excluíam audit_rejected mas o agregado da grid `/detections` (e a `CoverageSummary` no topo) continuavam contando. Sintoma visível: célula mostrava "2 veiculações" e o modal listava 1. Corrigido pela migration 0029.

## Estrutura do package `internal/audit`

```
internal/audit/
├── auditor.go        # Auditor, AuditEvidence, runMatch (pure func, testável)
├── auditor_test.go   # unit tests (sem DB, sem ffmpeg)
└── pcm.go            # DecodeADTSToPCM / DecodeM4AToPCM via ffmpeg
```

API pública:

```go
auditor := audit.NewAuditor(db, log, 0, 0) // 0,0 = defaults §9.3
result, err := auditor.AuditEvidence(ctx, commercialID, pcm)
// result.Passed, result.Score, result.Coverage, result.Duration, ...
```

Quem instancia: só `cmd/api/main.go`. O Auditor é injetado no `evidence.NewService` como `*audit.Auditor`. `nil` desabilita o passo (usado em testes).

## Forensia: como auditar um caso manualmente

Quando aparecer uma detecção rejeitada e o operador quiser entender o porquê, dois caminhos:

1. **Tooling Python offline** (`fingerprint/scripts/audit_detection.py`):
   - Baixar a censura cadastrada e a evidência salva da plataforma
   - Rodar `python audit_detection.py --master <master.mp3> --evidence <clip.m4a>`
   - Saída mostra score+coverage por variante + histograma de delta — diagnóstico completo

2. **Logs estruturados:** procurar por `evidence: audit REJECTED` no log do API com o `detection_id` em mãos. Inclui `score`, `coverage`, `master_hashes`, `query_hashes`.

## Histórico

- **2026-05-14** Incidente UNIFIQUE Solaris FM: detecção confirmada com clipe salvo que não continha o comercial (score 2 / cov 1% em audit offline contra todos os 6 variantes do master). Foi o que motivou §9.9.
- **2026-05-15** Implementação inicial. Comentário do incidente original: ["e se a gente auditar a censura do comercial que deu como veiculado com o áudio q ele disse q veiculou?"](#).
- **2026-05-17** Postmortem [incident-2026-05-17-audit-status-constraint.md](../incidents/incident-2026-05-17-audit-status-constraint.md). Duas falhas correlacionadas: (1) feature shippou sem migração que adicionasse `'audit_rejected'` ao CHECK de `evidence_status`, então toda rejeição quebrava no UPDATE e deixava a row órfã em `'pending'`; (2) `DefaultMinCoverage` foi inicialmente 0.4 — 2.6× mais rigoroso que o threshold do matching live (0.15) — rejeitando detecções legítimas. Migration 0028 corrige o CHECK e backfilla as 104 órfãs pra `'missing'`. Threshold corrigido pra 0.15.
- **2026-06-16** O audit rejeitava **~43%** das veiculações reais do corte de 30s (ASAAS PLATAFORMA, `short_id 78`): score 50-176 mas cobertura *single-bin* ~0.05-0.13 (< 0.15). Causa dupla: (a) drift de playout espalhava os frames casados entre bins de delta vizinhos; (b) degradação de broadcast destruía o fingerprint das partes quietas/faladas, sobrando só um trecho robusto (score alto, cobertura estruturalmente baixa). Fix (commits `179e95c` + `862bda3`): a cobertura passa a unir os frames distintos do master no pico **±2 bins** (`coverageBinRadius`), e um score **≥30** (`coverageBypassScore`) **dispensa** o gate de cobertura em master **sem** hash compartilhado — espelha o `UniqueScore` do matcher live; master com sting compartilhado mantém o gate (clipe sting-only pontua alto só nos frames compartilhados → cobertura baixa → rejeitado). **Validado em prod: `audit_rejected` do 78 caiu de ~43% → 0%.** Origem: [incidente de recall 2026-06-12](../incidents/incident-2026-06-12-detection-recall-gaps.md).

## Limitações conhecidas

- **Audit não cobre 100% do problema do incidente original.** A janela `00:00.000 → 00:00.000` com `duração -2.0s` da tela de UNIFIQUE indica bug na *gravação* da janela — separado do match. Audit captura o sintoma (clipe não bate com master) mas o bug raiz da janela precisa ser investigado em PR separado.
- **Sem suporte a multi-attribution.** Hoje o audit testa contra UM commercial_id (o atribuído pelo evidence service). Se a evidência poderia legitimamente bater com OUTRO master da mesma campanha (R20 / §9.8), o audit rejeita — desejável por enquanto, mas pode virar falso-negativo após F-119 (multi-attribution).
- **Fail-open em erros de infra.** DB down ou ffmpeg quebrado → audit pula, upload prossegue. Filosofia: nunca perder veiculação real por bug do audit. Trade-off explícito; monitorar via `result=error`.
