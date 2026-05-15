# Evidence Audit (§9.9 — Audit de Evidência Pré-Persist)

Camada de verificação determinística que roda sobre o **clipe de evidência salvo** antes de marcar a veiculação como confirmada. Implementa a invariante:

> Se o sistema marca uma veiculação como confirmada, o áudio salvo como evidência **tem que conter** o comercial reivindicado.

Caso a invariante seja violada, a detecção é registrada com `evidence_status = 'audit_rejected'` e é automaticamente ocultada dos endpoints client-facing — vira dado forense para operadores investigarem o motivo.

Plano arquitetural: [`plano_implementacao.md` §9.9](../plano_implementacao.md#L979).

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
| `DefaultMinCoverage` | 0.4 | §9.3 `MIN_COVERAGE` |
| `DeltaBinSize` | 2 | §9.3 (compartilhado com `internal/match`) |

Hoje os thresholds são globais. Calibração por emissora (análogo ao §9.4 do matching live) fica fora de escopo — o audit é um *guardrail* contra discrepância grosseira, não substituto da calibração fina. Construtor expõe override no `NewAuditor(db, log, minScore, minCoverage)` para testes futuros.

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

Aplicado em `ListPaged`, `List` legacy, `Export`, `DailySummary`. Não aplicado em `GetByID` (acesso direto preserva visibilidade pra operadores).

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

## Limitações conhecidas

- **Audit não cobre 100% do problema do incidente original.** A janela `00:00.000 → 00:00.000` com `duração -2.0s` da tela de UNIFIQUE indica bug na *gravação* da janela — separado do match. Audit captura o sintoma (clipe não bate com master) mas o bug raiz da janela precisa ser investigado em PR separado.
- **Sem suporte a multi-attribution.** Hoje o audit testa contra UM commercial_id (o atribuído pelo evidence service). Se a evidência poderia legitimamente bater com OUTRO master da mesma campanha (R20 / §9.8), o audit rejeita — desejável por enquanto, mas pode virar falso-negativo após F-119 (multi-attribution).
- **Fail-open em erros de infra.** DB down ou ffmpeg quebrado → audit pula, upload prossegue. Filosofia: nunca perder veiculação real por bug do audit. Trade-off explícito; monitorar via `result=error`.
