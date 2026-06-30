# Design — Guard anti-duplicata no §18.2.2-v2 pass-path (co-fire sting/spot)

- **Data:** 2026-06-30
- **Status:** aprovado (aguardando review do spec)
- **Autor:** Claude + Dereck
- **Código relacionado:**
  - `workers/internal/evidence/service.go` (`reattributeByCoverage`)
  - `workers/internal/evidence/reject_recovery.go` (`recoverRejectedByCoverage`, `decideRejectRecovery`)
  - `workers/internal/catalog/detections.go` (`FindSiblingDetectionInWindow`, `ClearRetraction`, `ReattributeDetection`)
  - Memória: `disambig-v2-reattribution-duplicates-cofiring-sting`

## 1. Problema (confirmado em prod)

Quando um **sting curto** que é subset de um **spot longo** toca sozinho (ex.: camp 673c30b7 PILECCO — mat 34 "PULSO SONORO" 5.4s ⊂ mat 33 "Spot 30" 30s):

1. O sting confirma forte (`temporal_coverage` 1.0) → 1 row genuína de mat 34.
2. O spot **falso-confirma** fraco (~0.2) na região de áudio compartilhada → 1 row de mat 33. (A defesa shared-hash **não** impede: stings <10s são deliberadamente excluídos — `MinShareableDurationSeconds=10`, por causa da regressão RÔGGA. Decisão de design existente, **não vamos mexer**.)
3. O **pass-path** `reattributeByCoverage` (§18.2.2-v2, gated `DISAMBIG_BY_COVERAGE=true` em prod) audita a row do spot, vê que o clipe cobre o master do sting mais (`chooseByCoverage`) e **re-aponta a row do spot pra mat 34 in-place**.

Resultado: **2 rows mat-34 no mesmo segundo** (cov 0.2 + 1.0), nenhuma retratada → contam as duas = **duplicidade**. Provado por log (`detection reattributed by coverage` `from_short_id:33 to_short_id:34`) e SQL (2 rows `created_at` 7ms apart). **Sistêmico** — o mesmo log mostra `104→147`, `78→77`, `48→47`, `48→9`.

**Confirmado pelo operador (2026-06-30, escuta dos clipes `db3703dd`/`a60f8ef3`):** tocou **uma única vez**, o material que sobrevive (mat 34) está **correto** — o bug é **contagem dobrada pura**, NÃO má-atribuição. Isso valida a direção do fix (manter o vencedor que já tem a tocada, retratar a row irmã redundante). `coverage` é normalizado pela duração do MASTER (`auditor.go:63`), então a row de 0.2 é o irmão mais longo (mat 33) casando só na região compartilhada — não é "match fraco/errado", é a mesma veiculação vista por um segundo fingerprint do catálogo.

**Fora de escopo (anotado, não bloqueia):** `chooseByCoverage` compara coverage entre masters de durações muito diferentes (viés estrutural ao master curto). Aqui não causou má-atribuição (operador confirmou), mas o storm `104→147` merece auditoria à parte — possível usar `MatchExtent`/duração além de coverage crua. Não é este fix.

### Causa exata

O `reattributeByCoverage` (pass-path) **reatribui incondicionalmente** quando um irmão vence por cobertura. Ele assume *"exactly one row exists and it is corrected in place"* (comentário em service.go:560) — premissa **violada** quando o irmão vencedor já tem a tocada genuína no mesmo segundo. O **reject-path** (`recoverRejectedByCoverage`) **já trata isso**: consulta `FindSiblingDetectionInWindow` e, se o vencedor já tem row presente, faz `RecoverySkip`. O pass-path é o único sem o guard.

## 2. Objetivo / Não-objetivos

**Objetivo:** o pass-path parar de criar duplicata quando o cut vencedor já tem detecção no station+janela — preservando 100% a recuperação legítima "15s contado como 30s" (1 row só → reatribui como hoje).

**Não-objetivos:**
- Mexer no shared-hash / `MinShareableDurationSeconds` (decisão de design existente; reabrir = regressão RÔGGA).
- Desligar `DISAMBIG_BY_COVERAGE` (mata as recuperações 15s→30s legítimas — 104/110).
- Mexer no algoritmo de matching, cooldown, ou v1 do supervisor.

## 3. Design

### 3.1 Guard no pass-path (`reattributeByCoverage`)

Depois de decidir que `best.ShortID != self.ShortID` e resolver `newCommercialID/newCampaignID` (winner), **antes** do `ReattributeDetection`, consultar a row existente do vencedor:

```go
existing, err := s.detections.FindSiblingDetectionInWindow(ctx, best.ShortID, stationID, detectedAt, coverageRecoveryWindowSeconds)
```

e ramificar pela **mesma árvore de decisão** do reject-path, com ações ajustadas ao pass-path (onde `self` está **aprovada** e precisa ser retratada, não só pulada):

| Estado da row do vencedor | Ação no pass-path |
|---|---|
| **não existe** (`nil`) | **Reatribui** `self` pro vencedor (comportamento atual — caso legítimo 15s-as-30s). |
| **presente, não-retraída** | **Retrai `self`** (duplicata co-fire). NÃO reatribui. |
| **retraída + `available`** | **ClearRetraction(vencedor)** + **retrai `self`** (v1 tinha retraído o vencedor real; restaura e remove o falso-positivo). |

A árvore (`nil→reattribute`, `retracted+available→restore`, `else→skip`) é **idêntica** à `decideRejectRecovery`. Vamos **renomear** `decideRejectRecovery → decideCoverageRecovery` (função pura, path-neutral) e reusá-la nos dois caminhos. As **ações** continuam diferentes por path (reject-path: skip não mexe em self porque self já é `audit_rejected`; pass-path: skip/restore **retratam** self).

### 3.2 Métodos reutilizados (já existem)
- `FindSiblingDetectionInWindow(shortID, stationID, detectedAt, windowSeconds) (*SiblingDetectionRow, error)` — catalog/detections.go:1224.
- `ClearRetraction(id, detectedAt)` — catalog/detections.go:1256.
- `FindCutWithSiblings` — já usado pelo `reattributeByCoverage`.

### 3.3 Método novo
- `catalog.Detections.RetractByID(ctx, id, detectedAt, at) error` — `UPDATE detections SET retracted_at = $3 WHERE id=$1 AND detected_at=$2 AND retracted_at IS NULL`. Simétrico ao `ClearRetraction`. **Não** mexe em `detection_campaigns`: a projeção é gateada pelo `retracted_at` da row base (view `daily_play_summary` CTE `actual` filtra `d.retracted_at IS NULL`), então retração in-place basta (diferente da reatribuição, que muda commercial_id/campaign_id e por isso precisa do `syncCanonicalProjection`).

### 3.4 Janela e métrica
- `coverageRecoveryWindowSeconds = 60` — alinhado ao `recoverRejWindowSeconds` do reject-path (proven em prod). Os pares reais batem em gap 0s; 60s cobre jitter e fica abaixo do intervalo entre breaks. (Pré-condição que torna 60s seguro: o pass-path só reatribui rows de **baixa** cobertura — uma tocada real do spot vence como ele mesmo e nunca entra aqui; logo `self` retratada é sempre um falso-positivo de áudio compartilhado.)
- Métrica: `MatchDisambiguation.WithLabelValues("duplicate_cofire_retracted").Inc()` no ramo de retração de self.

### 3.5 Reject-path
Já coberto pelo `RecoverySkip`. Mudança: só o rename `decideRejectRecovery→decideCoverageRecovery` + atualizar o caller e o teste. Comportamento **inalterado**. Adicionar 1 teste que documenta o skip (regressão).

## 4. Limitação conhecida
`self` (a row do spot) pode já ter disparado webhook/evento como detecção confirmada do spot antes da retração (o audit roda async, ~min depois). A retração é in-place (`retracted_at`), sem evento NATS de retração — consistente com a filosofia in-place do v2 (o `reattributeByCoverage` atual também não emite evento). UI/contagens refletem via `retracted_at`; webhooks de um falso-positivo já enviado é problema pré-existente e fora de escopo.

## 5. Testing (TDD — escrever testes primeiro)

`workers/internal/evidence/` (unit, com auditor/detections fakes ou pool de teste):
1. **Co-fire (bug):** vencedor já tem row aprovada na janela → `self` retratada, **não** reatribuída, sem duplicata; métrica `duplicate_cofire_retracted`.
2. **Legítimo 15s-as-30s:** vencedor **sem** row → `self` reatribuída (comportamento atual preservado).
3. **Restore:** vencedor com row retraída+available → ClearRetraction no vencedor + `self` retratada.
4. **Sem irmão vencedor:** `best==self` → no-op (já coberto; manter verde).
5. **Reject-path skip:** regressão — vencedor presente → reject-path continua `RecoverySkip` (self segue `audit_rejected`, sem mudança).
6. **`decideCoverageRecovery` puro:** tabela nil/retracted+available/present → reattribute/restore/skip.

## 6. Reparo de dado (sistema todo — Dereck executa)

Independente do fix. Read-only blast-radius primeiro, depois retração com preview→COMMIT.

```sql
-- blast radius (todas as campanhas): grupos (estação,material,segundo) com >1 aprovada
WITH grp AS (
  SELECT d.station_id, d.commercial_id, date_trunc('second', d.detected_at) AS sec, count(*) AS n
  FROM detections d
  WHERE d.retracted_at IS NULL AND d.ignored_at IS NULL AND d.evidence_status <> 'audit_rejected'
  GROUP BY 1,2,3 HAVING count(*) > 1
)
SELECT count(*) AS grupos_duplicados, COALESCE(sum(n-1),0) AS excedente_total FROM grp;
```

Retração (mantém a de MAIOR coverage por (estação,material,segundo), retrai o resto):

```sql
BEGIN;
WITH ranked AS (
  SELECT d.id, d.detected_at,
         row_number() OVER (PARTITION BY d.station_id, d.commercial_id, date_trunc('second', d.detected_at)
                            ORDER BY d.temporal_coverage DESC, d.hash_count DESC, d.id) AS rn
  FROM detections d
  WHERE d.retracted_at IS NULL AND d.ignored_at IS NULL AND d.evidence_status <> 'audit_rejected'
)
UPDATE detections d SET retracted_at = now()
FROM ranked r WHERE d.id=r.id AND d.detected_at=r.detected_at AND r.rn>1;
ROLLBACK; -- trocar por COMMIT após revisar contagem
```

NÃO restaurar pro spot — as rows 0.2 são falso-positivo de shared-hash, não houve spot real.

## 7. Rollout / régua regra 6

- Fix atrás de `DISAMBIG_BY_COVERAGE` (kill-switch instantâneo: `=false` + recreate `--no-deps api`).
- Cross-compile linux antes de push: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...`.
- `go test ./internal/evidence/... ./internal/catalog/...`.
- Sem migration nova → sem shadow test. Sem mudança de frontend.
- Boot da API inalterado (sem métrica nova no MustRegister além de um label novo no `MatchDisambiguation` existente — label não colide).
