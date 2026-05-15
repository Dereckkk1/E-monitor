# Material Similarity Warning

## What it does

When an operator uploads a new material in the campaign wizard, the system
scans the new audio against the other materials of the **same client** (no
cross-client comparison) and persists the top match if the similarity score
is ≥15%. The frontend then shows an amber badge on the material card with a
modal that A/B's both audios and lets the operator decide:

- **Manter assim mesmo** → acknowledges the warning (badge disappears).
- **Remover material novo** → deletes the just-uploaded material.

## Scope and limitations

- Per-client only. Two clients with identical jingles will not cross-warn.
- Threshold is hard-coded at 15% (`similarity.WarnThreshold` in
  `workers/internal/similarity/similarity.go`).
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
materials, and accumulates the matched frame ranges on both sides. Then:

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
WHERE m.similarity_score >= 0.15
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
