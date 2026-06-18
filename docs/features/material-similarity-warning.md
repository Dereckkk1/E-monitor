---
status: implementado
ultima-verificacao: 2026-06-18
codigo-relacionado:
  - workers/internal/similarity/similarity.go
  - workers/internal/similarity/segments.go
  - workers/internal/similarity/overlap.go
  - workers/internal/similarity/similarity_test.go
  - workers/internal/similarity/dense_audio_test.go
  - migrations/0025_material_similarity.up.sql
  - migrations/0040_similarity_segments.up.sql
  - frontend/src/components/SimilarityWarningModal.jsx
  - frontend/src/components/SimilarityTimeline.jsx
  - frontend/src/components/SimilarityHeadsUp.jsx
---

# Material Similarity Warning

## What it does

When an operator uploads a new material in the campaign wizard, the system
scans the new audio against the other materials of the **same client** (no
cross-client comparison) and persists the top match if the similarity score
is ≥50%. The frontend then shows an amber badge on the material card with a
modal that A/B's both audios and lets the operator decide:

- **Manter assim mesmo** → acknowledges the warning (badge disappears).
- **Remover material novo** → deletes the just-uploaded material.

## Scope and limitations

- Per-client only. Two clients with identical jingles will not cross-warn.
- Threshold is hard-coded at **50%** (`similarity.WarnThreshold` in
  `workers/internal/similarity/similarity.go`). Calibração: <5% é ruído,
  15-25% é sting compartilhado intencional (não bloqueia — vinheta reutilizada
  legitimamente), 50%+ é subset / near-duplicate (bloqueia, exige decisão
  manter-os-dois vs remover-o-novo). O comentário no código tem o racional
  completo.
- Operates on `fingerprint_status='ready'` materials only. Materials still in
  `pending`/`generating` are skipped on both sides (own + index).
- Top-1 match. If the new material is similar to N existing ones, only the
  highest-scoring is surfaced.
- No retroactive backfill. Materials uploaded before migration 0025 are
  marked `similarity_check_status='skipped'`.

## How the score is computed

For each candidate pair (own=new material, other=existing material), the
scanner slides a 4s window in 1s hops across the new material's PCM, runs
`MatchWindow` against an in-memory index built from the client's other ready
materials, and accumulates the matched frame ranges on both sides.

A window only counts as a hit when its histogram peak clears BOTH
`MinScore` (absolute, =5) AND `MinScoreCoverage` (the peak must be ≥2% of the
window's total hashes). The coverage floor mirrors the runtime ingestor
(`MinScoreCoverage: 0.02`) and is what stops spectrally dense audio from
false-matching — see the dense-audio bug below. Then:

```
ownCov   = framesUnionOwn   / totalFramesOwn
otherCov = framesUnionOther / totalFramesOther
score    = max(ownCov, otherCov)     # clamped to [0, 1]
```

Calibration (empirical):

| Pair type | Score |
|-----------|-------|
| Unrelated audios | < 5% |
| Shared sting (~6s vinheta in 30s jingle) | 15-25% |
| Subset (30s cut of a 60s master) | ≥ 50% |
| Near-duplicate | ≥ 95% |

The cov values are clamped at 1.0 because window-based hits can extend past
the nominal frame total when the last window straddles the end of the
material (e.g. ownCov computed as 240/234 = 1.026 → clamped to 1.0).

## Event flow

```
POST /materials  →  api creates row  →  publishes fingerprint.generate
                                          ↓
Python daemon decodes + writes hashes + marks fingerprint_status='ready'
                                          ↓
Python publishes:                         index.reload
                                          fingerprint.shared-scan
                                          material.similarity-check  ★
                                          ↓
similarity.Subscriber (api process) consumes material.similarity-check
                                          ↓
similarity.CheckMaterialSimilarity runs per-client scan
                                          ↓
UPDATE materials SET similarity_* = ...
                                          ↓
Frontend polling picks up the change (3s refetch interval while pending)
```

The Python publish for `material.similarity-check` is gated on
`entity_kind == "material"` — commercial uploads do not trigger it (they
don't have a `materials` row to compare against).

## Re-triggering a scan manually

```bash
nats pub material.similarity-check '{"material_id":"<uuid>"}'
```

The Go subscriber in the `api` process picks it up, re-runs the scan, and
overwrites the row's `similarity_*` columns. Useful when a scan failed and
left the row in `similarity_check_status='failed'`, or after editing
audio-refs locally.

## Inspecting

```sql
SELECT m.title,
       m.similarity_check_status,
       ROUND((m.similarity_score * 100)::numeric, 1) AS pct,
       sim.title AS similar_to,
       m.similarity_acknowledged_at
FROM materials m
LEFT JOIN materials sim ON sim.id = m.most_similar_material_id
WHERE m.similarity_score >= 0.50
ORDER BY m.created_at DESC;
```

To find materials stuck in `pending` for too long (probable cause:
api/subscriber wasn't running when the event was published):

```sql
SELECT id, title, created_at
FROM materials
WHERE similarity_check_status = 'pending'
  AND created_at < NOW() - INTERVAL '10 minutes';
```

Re-trigger each via `nats pub`.

## Performance

Per upload: ~50ms index load + ~200ms PCM decode + ~30ms scan against ~20
materials = sub-second. Scales linearly with the client's material count.
A 200-material client would hit ~2-3 seconds per upload — still acceptable.

The scan runs **after** fingerprint generation completes, so the upload
acknowledgement to the operator (HTTP 201) is not blocked by similarity work.

## Dense-audio false positive (fixed 2026-06-18)

**Symptom:** two completely different full-length songs uploaded for the same
client scored ~100% similar and tripped the blocking modal.

**Root cause:** `runScan` called `MatchWindow(window, store, MinScore, 0.0)` —
the `0.0` disabled the per-window coverage floor. Two compounding factors:

1. **Dense audio + small effective hash space.** A 3-minute song produces ~350
   hashes/s; in a 4s window that's ~1400 hashes. The hash masks frequency to
   9 bits (`f & 0x1FF`), so the effective space is small and **~8% of hash
   VALUES coincide between two unrelated tracks by pure chance**. Those random
   collisions pile 5+ into the same delta bin, clearing `MinScore=5`, so a
   chunk of windows false-hit. The coverage formula then paints 4s of frames
   per hit and takes `max(ownCov, otherCov)`. With a single variant this
   already reaches ~56% on two real songs.

2. **5 broadcast-sim variants per material in the index.** The python daemon
   (`broadcast_sim.py`) stores variants 0–4 per material; the matcher takes the
   `max` peak across variants per window, i.e. **5 independent chances** to
   spuriously clear `MinScore`. Variants 3 & 4 inject white noise (−32/−35 dB)
   and variant 4 lowpasses to 3500 Hz — both *increase* hash density (variant 4
   alone ≈ 105k hashes), so the spurious-hit rate climbs further. With the real
   5-variant index, two completely different songs reach **exactly 100%** (this
   is what was observed in production; reproduced faithfully with the prod
   ffmpeg chains).

The runtime matcher never had this problem because it always applied the 2%
coverage floor plus its downstream guards (unique-hash score, temporal
coverage, cooldown, evidence audit); the similarity scan reused `MinScore` but
dropped the coverage guard.

**Fix:** pass `MinScoreCoverage = 0.02` instead of `0.0`. This drops the two
test songs from 55% → 0% while a real 30s subset stays at 100%. Regression
guard: `dense_audio_test.go` (synthetic broadband noise reproduces the
false positive at 100% with the guard off, 0% with it on; a real subset stays
≥90%).

## Timeline de sobreposição (2026-06-18)

Além do score, o scan agora persiste **onde** os dois materiais batem, pra
desenhar uma timeline no upload. Spec:
[docs/superpowers/specs/2026-06-18-similarity-overlap-timeline-design.md](../superpowers/specs/2026-06-18-similarity-overlap-timeline-design.md).

- **Segmentos conectados:** `runScan` registra, por janela casada com o top
  match, a tripla `(ownStart, ownEnd, offset)`. `buildSegments`
  (`segments.go`) agrupa por offset (cada offset = um alinhamento) e funde
  janelas contíguas → uma lista de trechos `own[de,até] ↔ other[de,até]`.
- **Persistência:** coluna `materials.similarity_segments` (JSONB, migration
  0040) guarda `{ own_cov, other_cov, own_duration, other_duration, segments }`
  em segundos. O **piso de persistência caiu de 0.50 → 0.25**
  (`PersistThreshold`): abaixo disso a linha fica limpa (`NULL`).
- **Headline:** o número mostrado é `own_cov` (% do material novo que é igual),
  não o `max`. O `similarity_score` persistido continua sendo o `max` e é ele
  que decide o bloqueio.
- **Dois estados no upload (wizard):**
  - `score ≥ 0.50` → modal **bloqueante** (`SimilarityWarningModal`) com a
    timeline embutida (manter/remover).
  - `0.25 ≤ score < 0.50` → **heads-up não-bloqueante** (`SimilarityHeadsUp`):
    mesma timeline, uma ação "Entendi, seguir" que sempre prossegue.
  - `< 0.25` → nada.
- **Componente:** `SimilarityTimeline.jsx` (duas faixas, trechos iguais em
  verde) é compartilhado pelos dois estados.

**Limitações conhecidas (verificadas no e2e 2026-06-18):**
- O lado `other` da timeline pode mostrar **menos trechos** que o `own`: as
  variantes de broadcast-sim com ruído às vezes fazem o matcher escolher um
  alinhamento fantasma (fora dos limites do material), que é **descartado**
  (`buildSegments` clampa em `otherTotalFrames`). O eixo `own` é confiável.
- O `similarity_score` (e portanto a faixa bloqueante/heads-up) ainda pode ser
  **inflado** por casamentos fantasma além da duração — `coverages()` usa os
  ranges crus, sem clamp. Pré-existente (não introduzido por esta feature);
  follow-up: clampar `otherRanges` em `coverages()` também.

## Known limitations / follow-ups

- **F-120 (new):** Threshold is not tunable per client. Some clients may want
  a tighter threshold (e.g. 8% for radio production agencies with many shared
  stings). Currently a code change.
- **F-121 (new):** Acknowledgment is per-material. If the operator
  acknowledges material B (similar to A) and later uploads C that's similar
  to A, C gets its own warning. There's no transitive ack of "the A family
  is fine, move on".
- **Audio playback auth (pre-existing):** the modal's `<audio>` tags hit
  `/v1/internal/materials/{id}/audio` directly (not via axios), and that
  route is behind the `auth.RequireRole("admin","operator")` middleware.
  In dev (Vite proxy on localhost:3000 → localhost:8080) cookies persist
  across origins and this works. In production with split origins, the
  `<audio>` request won't carry the JWT. Same issue exists on
  `/v1/internal/commercials/{id}/audio`; fix is shared.
