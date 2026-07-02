---
status: parcialmente-implementado
ultima-verificacao: 2026-07-01
codigo-relacionado:
  - migrations/0046_material_twin_discriminative.up.sql
  - migrations/0047_evidence_status_ambiguous.up.sql
  - workers/internal/audit/auditor.go
  - workers/internal/sharing/sharing.go
  - workers/internal/sharing/subscriber.go
  - workers/internal/similarity/similarity.go
  - workers/internal/catalog/twin_discriminative.go
  - workers/internal/catalog/detections.go
  - workers/internal/catalog/detection_filter.go
  - workers/internal/evidence/twin_disambig.go
  - workers/internal/evidence/service.go
  - workers/cmd/api/main.go
---

# Desambiguação de gêmeos acústicos pelo trecho discriminante

> **Estado:** Plano 1 (backend core) implementado e mergeável **com a flag `DISAMBIG_TWIN_DISCRIMINATIVE` DESLIGADA**. Fila de revisão (frontend, Plano 2) e backfill (Plano 3) **pendentes**. Ligar a flag em prod só após o checklist de pré-ativação (fim do doc).
>
> Spec de design: [docs/superpowers/specs/2026-07-01-acoustic-twin-disambiguation-design.md](../superpowers/specs/2026-07-01-acoustic-twin-disambiguation-design.md). Plano: [docs/superpowers/plans/2026-07-01-acoustic-twin-disambiguation-core.md](../superpowers/plans/2026-07-01-acoustic-twin-disambiguation-core.md).

## Problema

Gêmeos acústicos = dois materiais de **mesma duração** e áudio **quase idêntico** (mesmo `client_id`, `master_sha256` diferente, ~90%+ compartilhado), tipicamente co-programados (ex.: incidente Milium — "FESTIVAL DE INVERNO" 30s com cortes 9/15/48/149/150 quase iguais). Quando um toca, o matcher casa o master do gêmeo-base e credita o **material errado**.

Por que as defesas existentes NÃO resolvem:
- **Shared-hash** ([shared-hash-detection.md](shared-hash-detection.md)) deliberadamente **NÃO flaga** pares com ≥50% de overlap (`SubsetThreshold=0.5`) — deixa pra desambiguação por duração. Mas gêmeos de mesma duração não têm o que a duração desempate.
- **`reattributeByCoverage` (§18.2.2 v2)** desempata cortes de durações diferentes (15s vs 30s) pela cobertura-cheia do clipe. Entre gêmeos de mesma duração a cobertura-cheia **empata** (ambos ~idênticos) → não decide.
- Desempate por `short_id` é loteria (incidente 2026-06-25).

## Ideia central

O único trecho que separa dois gêmeos é onde o áudio **difere** (a "região discriminante"). Medir quanto o clipe de evidência cobre a **região discriminante de cada gêmeo** decide quem tocou: o clipe contém a assinatura única de no máximo um deles.

```
master A = [ compartilhado com B ........ | trecho único de A ]
master B = [ compartilhado com A ........ | trecho único de B ]
                                          ^ regiões discriminantes (disjuntas)
clipe tocado = B  →  cobre ~1.0 do trecho único de B, ~0 do de A  →  atribui a B
```

## Arquitetura (pós-audit, gancho no pass-path)

Roda **depois** do §9.9 audit passar e **depois** do `reattributeByCoverage`, só quando este NÃO agiu (gate de ordenação — não desfaz/duplica a decisão do v2). Não toca no matcher em tempo real.

### Offline (ingestão): popular as regiões discriminantes

`catalog.TwinDiscriminative.PopulateForMaterial(materialID)` roda via callback `afterScan` do `sharing.Subscriber` após cada shared-scan de **material** (dispara em re-fingerprint também → recompute bidirecional):

1. `similarity.FindSimilarMaterials(materialID, ≥0.50)` — todos os candidatos por cobertura (não só o top match).
2. Filtra `|dur − dur_twin| ≤ 1s` (só mesma duração).
3. Pra cada gêmeo, `sharing.ComputeTwinOverlap` mede os frame-ranges de overlap; o **complemento** (`complementRanges`) em `[0, totalFrames)` é a região discriminante.
4. Grava os **dois lados** (A vs B e B vs A — regiões diferentes) em `material_twin_discriminative` (`disc_ranges INT[]` achatado `[lo0,hi0,...]`, `disc_frames`). `disc_frames=0` ⇒ par não separável pelo áudio.

> **Injeção de dependência:** `sharing.Subscriber` recebe `afterScan func(ctx, materialID)` em vez de importar `catalog` (evita ciclo — `catalog` já importa `sharing`).

### Online (evidência): decidir

`(*Service).disambiguateTwin` (evidence):

1. `ListForMaterial(attributedID)` — gêmeos populados (já de mesma duração).
2. `AuditEvidence(self, pcm)` → `Result.CoveredFrames` (frames do master batidas pelo clipe, exposto em [auditor.go](../../workers/internal/audit/auditor.go)).
3. Pra cada gêmeo: `AuditEvidence(twin, pcm)`; `covSelf = CoverageOnFrames(selfCovered, discSelf)`; `covTwin = CoverageOnFrames(twinCovered, discTwin)`; `chooseTwinByDiscriminative(covSelf, covTwin, floor=0.15, margin=1.5)`.
4. `pickTwinAction` agrega: qualquer `reattribute` → o de maior `covTwin` vence; senão qualquer `ambiguous` → ambíguo; senão `keep`.
5. Aplica:
   - **reattribute** → `reattributeTwinWithCofireGuard` (ver Guards) → `ReattributeDetection` (sincroniza projeção in-tx) via `resolveAttribution` (só campanha `programada/ativa`).
   - **ambiguous** → `MarkAmbiguous` (retrata; vai pra revisão manual).
   - **keep** → nada.

`chooseTwinByDiscriminative`: maior cobertura discriminante `< floor` → ambíguo (assinatura não sobreviveu à degradação, ou `disc_frames=0`); vence por `≥ margin` → reatribui/mantém; quase-empate acima do piso → ambíguo (nunca chuta).

## Guards de regressão (auditoria contra 9 postmortems, 2026-07-01)

| Guard | Incidente | Onde |
|---|---|---|
| **Co-fire guard** antes de reatribuir (senão duplica tocada) | 2026-06-30 cofire | `reattributeTwinWithCofireGuard` reusa `FindSiblingDetectionInWindow` + `decideCofireAction` |
| Reatribuir só via `ReattributeDetection` (sync projeção) | 2026-06-30 fantasma | `disambiguateTwin` nunca faz `UPDATE detections` bare |
| `ambiguous ⟺ retracted` | 2026-05-17 + count-consistency | `MarkAmbiguous` seta status **E** `retracted_at`; views gateiam `retracted_at` ao vivo |
| `'ambiguous'` no CHECK preservando valores | 2026-05-17 | migration 0047 |
| Gate de ordenação (não desfaz v2) | §18.2.2 | `if s.disambigTwin && !reattributed` |
| Campanha cancelada não recebe tocada | cancelled-campaign | `resolveAttribution` filtra `('programada','ativa')` |
| Sem ciclo de import | — | callback `afterScan` (inversão de dependência) |

## Contagem: `ambiguous` sai do conjunto aprovado

`MarkAmbiguous` retrata a linha. Como `ApprovedDetectionsFilter` e as views (`daily_play_summary` 0029, projeção `detection_campaigns` 0041) filtram `d.retracted_at IS NULL` **ao vivo** (JOIN na row base), a detecção ambígua não conta em lugar nenhum — sem migration de view. O const também tem `<> 'ambiguous'` (defesa/documentação). Ver [detection-count-consistency.md](detection-count-consistency.md).

## Flags

- **`DISAMBIG_TWIN_DISCRIMINATIVE`** (default OFF) — liga a desambiguação no pass-path.
- **`DISAMBIG_TWIN_RETRACT_AMBIGUOUS`** (default OFF) — o que fazer no sinal fraco/confuso (`verdictAmbiguous`): OFF = **preserva a atribuição** (`ambiguous_kept`), não retrai; ON = retrai (`MarkAmbiguous` → revisão). **Default OFF de propósito:** sem a fila de revisão (Plano 2), retrair sub-contaria a tocada. Ligar só depois do Plano 2.

Métrica `radiocheck_match_disambiguation_total`: labels `reattributed_by_discriminative` / `ambiguous_by_discriminative` / `ambiguous_kept` / `kept_by_discriminative` / `duplicate_cofire_retracted`.

### Validação em prod (dry-run 2026-07-02, campanha Milium ativa)

`redisambiguate-twins --dry-run` sobre 15 detecções: **6 keep, 8 ambiguous, 1 reattribute, 0 erradas.** Padrão:
- **Par 9↔15** (diferem ~2.7s): separa limpo — `keep` com `covSelf 0.50–0.68` quando certo, `reattribute→sid=9 (covTwin 0.50)` quando errado. A feature funciona.
- **Trio 45/46/48** (diferem numa tag de marca ~2s): `covSelf`/`covTwin` todos 0.00–0.14 (ruído) → `ambiguous`. É o **window-straddle** do `ComputeTwinOverlap` (janela de 4s atravessa a diferença de ~2s) — a região discriminante sai mal-medida.

Conclusão: calibração (`floor=0.15`/`margin=1.5`) **está boa** — não baixar (a 0.10-0.14 é ruído; reatribuir ali seria chute). Com `RETRACT_AMBIGUOUS=OFF` o trio fica como está (preserva contagem) e só o par limpo é corrigido. Recuperar o trio = melhorar o `ComputeTwinOverlap` (janela menor pro scan discriminante) + re-backfill.

## Filosofia de teste

Cores puros testados (`chooseTwinByDiscriminative`, `pickTwinAction`, `complementRanges`, `sumFrames`, `filterTwinsByDuration`, `CoverageOnFrames`, repo `Upsert/Get`, `MarkAmbiguous`). Shells de decode+áudio+DB (`ComputeTwinOverlap`, `FindSimilarMaterials`, `PopulateForMaterial`, orquestração `disambiguateTwin`) sem teste dedicado — espelha o repo (`MarkSharedHashes`/`CheckMaterialSimilarity` também são shells sem teste).

## Checklist de pré-ativação (antes de `DISAMBIG_TWIN_DISCRIMINATIVE=true` em prod)

- [ ] **Teste do `reattributeTwinWithCofireGuard`.** É cópia inline manual do caminho de maior risco (co-fire dedup, incidente 2026-06-30). Hoje só a decisão (`decideCofireAction`) é testada, não a fiação dos 3 ramos. Abstrair `s.detections` numa interface e table-test os 3 `cofireAction`. (Review final I/gap, 2026-07-01.)
- [ ] **I1 — defesa da view.** Decidir entre (a) `AND d.evidence_status <> 'ambiguous'` no CTE `actual` da `daily_play_summary`/0041, ou (b) CHECK `evidence_status <> 'ambiguous' OR retracted_at IS NOT NULL` em `detections`. Nota: (b) obriga o Plano 2 (fila de revisão) a tirar o status `ambiguous` no MESMO passo que limpa a retração ao resolver.
- [ ] **Calibrar `twinDiscFloor` (0.15) / `twinDiscMargin` (1.5) / `durTolerance` (1s)** contra clipes reais de gêmeos degradados, em sombra (flag ON num worker isolado ou métricas dry-run) — a feature reatribui/retrata tocadas reais.
- [ ] **Plano 3 — backfill** de `material_twin_discriminative` pra materiais existentes, via shadow-migration test contra dados de prod (§4.8).
- [ ] **Plano 2 — fila de revisão** pras detecções `ambiguous` (senão viram supressão silenciosa como `audit_rejected` foi no incidente 2026-06-12).
