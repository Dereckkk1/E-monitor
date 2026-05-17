---
status: resolvido
ultima-verificacao: 2026-05-17
codigo-relacionado:
  - workers/internal/audit/auditor.go
  - workers/internal/evidence/service.go
  - migrations/0028_audit_rejected_status.up.sql
---

# Incidente 2026-05-17 — audit de §9.9 deixa detections órfãs em `pending`, áudio devolve 404

## TL;DR

A PR do audit pré-persist (§9.9, commit `8f8f3ee` em 2026-05-15) shipou usando o valor `'audit_rejected'` em `evidence_status` mas **não criou a migration** que adicionasse esse valor ao `CHECK` constraint da tabela `detections`. Toda detecção que o audit decidia rejeitar (coverage abaixo do threshold) batia em `SQLSTATE 23514` no UPDATE — a função logava o erro, retornava early, e a row ficava órfã em `'pending'` com `evidence_key=NULL` pra sempre.

Como `'pending'` não é filtrado das queries `List`/`ListPaged`, as 104 órfãs apareciam normalmente no relatório data/hora e nas modais do calendário, mas qualquer tentativa de tocar o áudio devolvia 404 porque o handler de `/evidence` exige `evidence_status='available'`.

Agravante secundário: `DefaultMinCoverage` foi inicialmente 0.4 — **2.6× mais rigoroso** que o threshold do matching live (0.15). Audit rejeitando detecções que o produtor (live) já tinha aceitado. Em 48h, ~49% das detecções viraram órfãs.

## Linha do tempo

- **2026-05-15 08:33 -03** — Commit `8f8f3ee` (`feat(audit): pre-persist evidence audit catches false positives (§9.9)`) é mergeado em master. 12 arquivos mexidos, **zero migrations**.
- **2026-05-15 deploy em prod** — Audit começa a rodar. Primeira `pending` órfã às 11:42 UTC.
- **2026-05-15 → 2026-05-17** — 104 detections órfãs acumuladas, distribuídas ao longo das 48h. `available` continua coexistindo com ~50% de share, sugerindo que coverage de 0.40 era arbitrariamente atingido por algumas detecções mas a maioria ficava em 0.15–0.40.
- **2026-05-17 ~17h BRT** — Usuário reporta no Slack: "quando eu tento ouvir os audios nas modais, eles dao como 404, no relatorio data e hora eu nao consigo tocar o play, mas as detecções estão ali".
- **2026-05-17 ~19h BRT** — Diagnóstico: SQL revela 104 `pending` vs 110 `available`. Logs do API filtrados pelo `detection_id 8fc09ab5` mostram `audit REJECTED` → `failed to mark audit_rejected (check constraint violation)`. Root cause confirmado.
- **2026-05-17 noite** — Migration 0028 + threshold ajustado para 0.15. PR mergeado.

## Causa raiz

### #1 — Feature shippou sem migration de schema

`evidence_status` é um `CHECK` constraint declarado em [`migrations/0001_initial.up.sql:112`](../../migrations/0001_initial.up.sql#L112):

```sql
CHECK (evidence_status IN ('pending','generating','available','missing','failed'))
```

O commit `8f8f3ee` introduziu uso do valor `'audit_rejected'` em [`evidence/service.go:465`](../../workers/internal/evidence/service.go#L465) sem incluir uma migration que estendesse o CHECK. A migration `0009_fase2_audit.up.sql` que a doc original do §9.9 citava em `codigo-relacionado` **não tem nada a ver com o audit de evidência** — ela cria a tabela `audit_log` (trail de ações de usuário). Provavelmente o nome confundiu o autor da doc.

Consequência: o UPDATE para `'audit_rejected'` sempre falhava com `SQLSTATE 23514`. O código loga o erro mas mesmo assim retorna `true` da função `runAuditOrReject` (indicando "rejeitei, não suba"), o que faz `processEvidence` pular o upload. A row fica em `'pending'` permanentemente, sem `evidence_key`.

### #2 — `DefaultMinCoverage` divergente do produtor

Audit foi shippado com `DefaultMinCoverage = 0.4`. Matching live usa `minTemporalCoverage = 0.15` ([`integration_audio_test.go:239`](../../workers/internal/match/integration_audio_test.go#L239), comentário em [`disambiguation_test.go:155`](../../workers/internal/supervisor/disambiguation_test.go#L155): "60s commercial confirms at T+9s (0.15 × 60s coverage)").

O audit roda **o mesmo algoritmo de match** que o live, sobre o **mesmo clipe que o live aprovou**. Se o threshold do audit é mais alto que o do live, o audit vai rejeitar coisas que o live já passou — por definição. Audit deveria ser igual ou ligeiramente *mais brando* (clipe salvo pode ter degradação leve de codec adicional), nunca mais rigoroso.

Causa provável do número 0.4: alguém escreveu por analogia ao número em [`statemachine.go:179`](../../workers/internal/match/statemachine.go#L179) (`covPath := cov >= 0.4 && cov < sm.minTemporalCoverage`), que é parte da lógica de transição pra `StateUncertain` no produtor — não é o threshold de confirmação.

### #3 — Bug silencioso, sem alarme

`evidence: failed to mark audit_rejected` saiu como `level=error` mas (a) sem alerta Prometheus configurado, (b) misturado no ruído de `window scan` (~30 linhas/segundo), (c) métrica `radiocheck_audit_attempts_total{result=rejected}` incrementava normal — do ponto de vista do operador, o audit "funcionava". O detection ficar órfão era invisível até alguém tentar tocar o áudio.

## Sintoma

- Modal `DayDetailModal` no calendário e botão de play no relatório data/hora devolvendo `404 evidence not available`
- 104 detections em `evidence_status='pending'` com `evidence_key=NULL`, idade até 48h
- Zero detections em `evidence_status='audit_rejected'` (porque o UPDATE sempre falhava)
- Logs do API: `error: ERROR: new row for relation "detections_2026_05" violates check constraint "detections_evidence_status_check" (SQLSTATE 23514)`

## Resolução

[migration 0028](../../migrations/0028_audit_rejected_status.up.sql):
1. Recria o `CHECK` constraint da parent `detections` incluindo `'audit_rejected'`. Cascateia pras partitions em PG11+.
2. Backfill atômico: 104 `pending` órfãs (criadas há mais de 5min, sem `evidence_key`) viram `'missing'`. Estado correto: veiculação detectada, clipe indisponível — mesma semântica de detection manual sem áudio (`CreateManual` sem upload). Conta em agregados, áudio não toca (esperado).

[auditor.go](../../workers/internal/audit/auditor.go):
- `DefaultMinCoverage` 0.4 → 0.15. Alinhado com matching live.

[evidence-audit.md](../architecture/evidence-audit.md):
- Corrigido `codigo-relacionado` (migration 0009 estava errada).
- Adicionado aviso explícito: threshold do audit não pode ser maior que threshold do live.

## Ações pós-incidente

### Imediatas (esta PR)

- [x] Migration 0028 (CHECK + backfill)
- [x] `DefaultMinCoverage = 0.15` em prod
- [x] Doc evidence-audit.md corrigido
- [x] Este postmortem

### Pendentes (criar follow-ups na fase 2)

- [ ] **Alerta Prometheus**: `radiocheck_audit_attempts_total{result="error"}` > 0 em 5min → page. O fail-open atual mascarava o constraint violation. Hoje, qualquer falha no UPDATE volta como `result=error` na métrica (a contagem é incrementada em [`service.go:427`](../../workers/internal/evidence/service.go#L427) — mas só pelo path de `audit.AuditEvidence` retornar erro, não pelo path do `UpdateEvidence`. Esse último merece sua própria métrica.)
- [ ] **Métrica de orphan pending**: gauge `radiocheck_detections_pending_age_seconds` que mede idade da `pending` mais antiga com `evidence_key=NULL`. Alerta se > 10min — invariante saudável é todo `pending` viver no máximo 2min.
- [ ] **CI guard**: lint que detecta uso de `evidence_status = '<literal>'` em código Go e checa se o literal existe em migration. Evita repetir o bug.
- [ ] **Calibração revisada**: thresholds `DefaultMinScore=5` e `DefaultMinCoverage=0.15` foram herdados do live por analogia, não calibrados pra audit especificamente. Considerar uma análise dirigida por dados: rodar audit offline em 30 dias de detections já confirmadas e ver a distribuição de score/coverage. O threshold do audit pode ficar mais brando ainda (já que o input é o clipe que o live já aprovou — espera-se 100% de pass exceto pra falsos positivos do live).
- [ ] **Decisão**: as 104 backfilladas pra `'missing'` somem do agregado de evidência mas continuam contando como veiculação. Verificar com o operador se isso é o comportamento desejado de longo prazo ou se algumas merecem investigação manual (download offline forense via §9.9 tooling Python — embora o clipe AAC já tenha sido limpo do disco, então o ground-truth está perdido para essas 104).

## Lições

1. **Code + schema é uma transação**. Qualquer PR que introduz literal de string num `CHECK`/`ENUM`/`FK` precisa ou (a) vir acoplada à migration que estende o domínio, ou (b) ser bloqueada no review até a migration ser anexada. Hoje não existe checklist nem lint que enforce isso — vale criar.

2. **Threshold espelhado, não estimado**. Quando um componente roda o mesmo algoritmo que outro (audit roda o mesmo match do live), os constantes devem ser **importadas**, não duplicadas. Constantes duplicadas drifam — esse é o terceiro caso de drift em 6 meses (vide [incident-2026-04-21-segment-size-drift.md](.) e [incident-2026-03-09-pmm-default-mismatch.md](.)).

3. **Fail-open mascara constraint violations**. A filosofia "audit nunca derruba veiculação real" é correta, mas o fail-open atual engole `UPDATE` errors como se fossem fail-open de infra. Eles são bugs distintos: `audit.AuditEvidence` retornar erro = infra problem (fail-open OK). `UpdateEvidence(... 'audit_rejected' ...)` retornar erro = código + schema inconsistentes (deveria pagear urgente, não silenciar). Discriminação fica como follow-up.

4. **Métricas saudáveis ≠ pipeline saudável**. O dashboard de `radiocheck_audit_attempts_total` mostrava número crescente de `rejected` — coerente com a invariante do audit funcionando. Mas a métrica derivada que importava — "quantas dessas rejected viraram efetivamente `audit_rejected` no DB" — não existia. Métrica nova proposta acima.
