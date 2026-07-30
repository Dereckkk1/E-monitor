# Design — Fix do par curto⊂longo (caso PULSO SONORO MILIUM)

**Data:** 2026-07-30
**Incidente de origem:** [incident-2026-07-24-pulso-milium-nao-detectado.md](../../incidents/incident-2026-07-24-pulso-milium-nao-detectado.md)
**Plano de execução:** [2026-07-30-pulso-subset-dedup-fix.md](../plans/2026-07-30-pulso-subset-dedup-fix.md)

## 1. Problema (1 parágrafo)

Material curto (pulso 5.7s) cujo áudio está contido no final de um spot ≥10s do MESMO cliente,
ambos vinculados às mesmas emissoras. Quando o pulso toca sozinho, o spot **false-confirma**
de carona (score 43-127, conf ~0.2 — borderline, varia por emissora) e o dedup §18.2.2 v1
decide **por duração**: o pulso perde SEMPRE — suprimido sem row, retratado, ou (race) fica
row dupla. Resultado em prod: pulso zerado em 2 de 8 emissoras; nas outras 6 o spot não cruza
o gate e o pulso detecta. Demonstrado ponta-a-ponta no E2E (incident report §4/§4c/§4d).

## 2. Decisão de design

**O veículo do fix é a máquina §18.2.2-v2 JÁ MERGEADA, gated por `DISAMBIG_BY_COVERAGE`** —
não construir arbitragem nova. Racional: a arbitragem correta precisa da EVIDÊNCIA (no
supervisor os dois cenários são indistinguíveis — conf da state machine é estruturalmente
enviesada: curto ~1.0 pelo piso de 32 frames, longo ~0.2 por confirmar na 2ª janela; provado
no E2E §4d, que REPROVOU `DISAMBIG_CONFIDENCE_AWARE`). A v2 arbitra pós-audit por cobertura
do clipe contra cada master — o discriminador certo (medido: spot cobre 0.183 do próprio
master quando o pulso toca sozinho vs 0.90+ quando o spot toca).

### 2.1 Inventário do que JÁ existe (verificado no código, 30/07)

| peça | onde | o que faz no nosso caso |
|---|---|---|
| `reattributeByCoverage` | [evidence/service.go:600-720](../../../workers/internal/evidence/service.go) | pass-path: row do spot passa no audit (cov 0.183) → re-audita o clipe contra os irmãos → pulso cobre 0.90 → `chooseByCoverage` elege o pulso |
| `chooseByCoverage` (margem 1.5) | [evidence/disambig_coverage.go:31-50](../../../workers/internal/evidence/disambig_coverage.go) | 0.90 ≥ 0.183×1.5 → pulso vence; caso spot-tocando: 1.0 < 0.90×1.5 → quase-empate → duração → spot vence (fail-safe correto) |
| co-fire guard | [evidence/cofire_guard.go](../../../workers/internal/evidence/cofire_guard.go) + service.go:643-689 | vencedor JÁ tem row que conta → retrata a duplicata (`cofireRetractSelf`); vencedor retraído c/ evidência → restaura + retrata self; vencedor SEM row (pulso suprimido pela v1!) → **reatribui a row do spot pro pulso** |
| reject-path v2b/v2c | service.go:557-583 + evidence/reject_recovery.go | se a row falsa do spot REPROVAR no audit (cov <0.15 em algumas tocadas — 0.183 é borderline): restaura o curto deslocado, senão reatribui a row rejeitada por cobertura |
| `ReattributeDetection` | [catalog/detections.go:487-522](../../../workers/internal/catalog/detections.go) | recategoriza + UPDATE + **`syncCanonicalProjection` na MESMA tx** (invariante detection_campaigns preservado; testes `TestDetections_ReattributeDetection*`) |
| passthrough compose | infra/docker/docker-compose.yml:219 | `DISAMBIG_BY_COVERAGE` **tem** passthrough (diferente do CONFIDENCE_AWARE, que não tinha — corrigido no working tree) |

### 2.2 Como os cenários fecham com `DISAMBIG_BY_COVERAGE=true` (análise + E2E fase D)

| cenário | fluxo | desfecho |
|---|---|---|
| pulso standalone, v1 SUPRIMIU o pulso (sem row) | row falsa do spot → audit passa (0.183) → v2: pulso cobre 0.90 → vence → co-fire: pulso sem row → `cofireReattribute` → **row do spot re-apontada pro pulso** | tocada conta pro pulso ✅ |
| pulso standalone, race deixou row dupla | audit da row do spot → v2 → co-fire: pulso TEM row que conta → `cofireRetractSelf` → row do spot retratada | 1 row, do pulso ✅ |
| pulso standalone, row do spot REPROVA no audit | reject-path: `RestoreDisplacedShorterCut` (se pulso foi retratado) senão `recoverRejectedByCoverage` | tocada recuperada ✅ |
| spot tocando (cauda co-fire do pulso) | audit da row do spot (0.90) → v2: pulso 1.0 vs 0.90×1.5 → quase-empate → duração → spot fica; row-race do pulso → v2 do pulso: spot vence o quase-empate → co-fire → duplicata do pulso retratada | 1 row, do spot ✅ |

**Validação E2E (fase D, par real, flag ON):** ver §5 do plano — resultados registrados no
incident report §4e. Gate de aceite: pulso standalone → N tocadas = N rows contando pro pulso,
0 rows vivas do spot; spot tocando → rows do spot intactas, 0 roubo.

## 3. O que o plano implementa além de ligar a flag

1. **Rollout validado da flag em prod** (ela nunca rodou em prod com o co-fire guard; em
   30/06 rodou SEM o guard e causou duplicatas — o guard de 02/07 corrige exatamente aquilo).
   Sombra de 48h com queries de aceitação.
2. **Alerta de supressão suspeita** (`dedup_suppressions` com `suppressed_confidence >
   kept_confidence`) — supressão de tocada real não pode ser silenciosa nunca mais.
3. **Aviso de UI pra material <10s** no upload (transparência da limitação; não bloqueia).
4. **Backfill do histórico** do caso Milium: rows do spot com `audit_coverage < 0.3` nos
   períodos/emissoras afetados re-arbitradas (dry-run → apply), + censuras 24/07 e 29/07
   viram veiculação manual se não recuperáveis.
5. **Higiene**: teste de regressão E2E-in-Go do par curto⊂longo; doc de feature.

## 4. Decisões tomadas (defaults na ausência do dono — revisáveis)

| decisão | default escolhido | alternativa |
|---|---|---|
| Mexer no supervisor v1 (suppress sem row)? | **NÃO nesta fase** — a v2 recupera via a row do spot; mexer no v1 é cirurgia em caminho quente com risco de duplicata. Fica follow-up F-122 (registrar containment no shared-scan e publicar-provisório) para o caso raro "spot false-confirma mas a row dele nem nasce" (não observado no E2E) | publicar-provisório já agora |
| `DISAMBIG_CONFIDENCE_AWARE` | **permanece OFF** (reprovada §4d); passthrough fica no compose documentado | remover passthrough |
| Threshold do reject-path | manter os existentes (floor 0.15, margem 1.5) — validados no E2E | recalibrar |
| Histórico | backfill só Milium/período do incidente (escopo fechado, auditável) | backfill global |

## 5. Riscos e mitigação

- **v2 re-audita irmãos por row aprovada** (custo CPU): catálogo Milium tem N irmãos; cada
  audit ~0.7-1.2s (medido). Mitigado: só roda em row com irmão do mesmo cliente; monitorar
  `radiocheck_audit_duration_seconds` no rollout.
- **Duplicata transitória** no `cofireRestoreThenRetractSelf` (writes não-transacionais,
  comentado no código): varrida pelo reparo keep-max-coverage (spec 2026-06-30 §6); aceito.
- **Flag ON reabre o caso 2026-06-30?** Não — aquele incidente foi v2 SEM co-fire guard; o
  guard existe desde 02/07 e o E2E fase D exercita exatamente o cenário que duplicava.
