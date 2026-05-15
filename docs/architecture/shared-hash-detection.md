# Shared-Hash Detection

## What it solves

Two commercials can share a snippet of audio — typically a sting, vinheta or
brand jingle reused at the end of a longer spot. When the longer commercial
plays on the air, the matching engine sees the shared snippet and may
accumulate enough time-coverage on the *other* commercial's fingerprint to
falsely confirm a detection of it.

Shared-hash detection eliminates this class of false positive without
weakening the matcher's robustness on degraded streams.

### Reference incident

In the staging catalog `AMBIENTAL 30` (a 30s spoken spot) and
`AMBIENTAL JINGLE` (a 30s jingle) share their last ~6.25s. With the previous
algorithm, every confirmed `AMBIENTAL 30` detection produced a paired false
`AMBIENTAL JINGLE` detection 6 seconds before it. Investigation in
`audio-refs/` confirmed the spectral overlap:

```
AMB30[22.00s..28.25s] ≡ JINGLE[23.50s..29.75s]
```

The integration test in [workers/internal/match/integration_audio_test.go](../workers/internal/match/integration_audio_test.go)
replays the same scenario end-to-end and asserts the false positive is gone.

## Algorithm

A fingerprint hash is *shared* when its `time_frame` falls inside a region
of the commercial that, when played through the matching engine, produces a
sustained match against another commercial's fingerprint.

Detection is by **matching-engine simulation, not exact hash-value
collision.** The fingerprint pipeline runs ffmpeg's `loudnorm` per file, so
the same audio in two different masters produces *different* hash values.
Marking by exact value misses ~98% of the genuinely shared content
(empirically: 39 of ~2300 hashes by value vs 2295 by simulation on the
AMB30/JINGLE pair).

For each new commercial Y the routine:

1. Loads every ready commercial's fingerprint into an in-memory index.
2. Decodes Y's master through the same `loudnorm + highpass + lowpass`
   pipeline used to build the catalog.
3. Slides a 4-second window with 1-second hop over the PCM. For every window
   where some other commercial X scores at or above
   [`sharing.MinScore`](../workers/internal/sharing/sharing.go) (currently
   `5`, aligned with the runtime matcher):
    - Y's `time_frame` range = the window's frame interval.
    - X's `time_frame` range is derived from the histogram delta:
      `entry.TimeFrame = live_frame - MatchResult.OffsetFrames`.
4. Merges overlapping/contiguous frame ranges per commercial and issues one
   `UPDATE fingerprint_hashes SET is_shared = true` per merged range.

At runtime the matcher continues to count *every* hit in the histogram peak
(so degraded streams keep matching robustly), but the state machine credits
only hits from non-shared hashes when deciding whether to confirm. A
commercial whose only matches come from shared content cannot accumulate
time-coverage and never advances out of `StateIdle`.

## When the flag gets set

- **Automatically**, every time a commercial is uploaded through the API.
  Flow:
  1. The API publishes `fingerprint.generate` (existing behaviour).
  2. The Python `fingerprint` service decodes the master, generates hashes,
     writes them to `fingerprint_hashes`, marks status='ready', and publishes
     both `index.reload` (so live matching sees the new commercial) and
     `fingerprint.shared-scan` (the new event for this feature).
  3. The Go `api` process subscribes to `fingerprint.shared-scan` via
     [`sharing.Subscriber`](../workers/internal/sharing/subscriber.go); it
     calls [`sharing.MarkSharedHashes`](../workers/internal/sharing/sharing.go)
     and, on success, republishes `index.reload` so the in-memory matching
     index picks up the freshly-set `is_shared` flags.
  4. Brief race window between steps 2 and 3 (~5–10 seconds) where the new
     commercial's hashes are visible without flags. Acceptable: the only
     impact is that a sister commercial uploaded simultaneously could
     produce one false-positive detection during that window.
- **One-shot backfill** for the existing catalog after migration 0015 lands:
  ```bash
  docker compose exec api backfill-shared-hashes --dsn "$DATABASE_URL"
  # If some masters are no longer on disk:
  docker compose exec api backfill-shared-hashes --dsn "$DATABASE_URL" --skip-missing
  ```
  Run this exactly once. It walks every `fingerprint_status='ready'`
  commercial in `created_at` order and applies the same algorithm new
  uploads get. The binary is shipped inside the `api` image (built by
  [workers.Dockerfile](../infra/docker/Dockerfiles/workers.Dockerfile)).
- **Manual re-trigger** for a single commercial (e.g. after edits or
  catalog repair):
  ```bash
  nats pub fingerprint.shared-scan '{"commercial_id":"<uuid>"}'
  ```

## Inspecting

Total flagged hashes:
```sql
SELECT count(*) FROM fingerprint_hashes WHERE is_shared = true;
```

Per-commercial breakdown:
```sql
SELECT c.title,
       c.short_id,
       count(*) FILTER (WHERE fh.is_shared) AS shared,
       count(*) AS total,
       round(100.0 * count(*) FILTER (WHERE fh.is_shared) / count(*), 1) AS shared_pct
FROM fingerprint_hashes fh
JOIN commercials c ON c.id = fh.commercial_id
GROUP BY c.id, c.title, c.short_id
ORDER BY shared_pct DESC NULLS LAST
LIMIT 20;
```

A commercial whose `shared_pct` is close to 100 has very little uniquely
identifying audio. Typically: a corrupt master, an accidental re-upload, or a
30s cut taken verbatim from a 60s master that's also in the catalog.

## Re-running after edits

`MarkSharedHashes` is idempotent and additive. Re-running for a commercial
already flagged produces redundant `UPDATE`s but no false unflagging. To
reset the flag globally and start fresh:

```sql
UPDATE fingerprint_hashes SET is_shared = false WHERE is_shared = true;
```

Then run the backfill again.

## Cost

- **Per-upload** (new commercial): `O(catalog_size)` — decode the new master
  + run `MatchWindow` per analysis window against every other commercial's
  fingerprint. For a 30s master against a 200-commercial catalog: ~3 seconds
  of decode + ~5 seconds of matching + a handful of `UPDATE`s. Runs after
  `Persist` so it does not block the upload acknowledgement to the operator.
- **Backfill**: `O(catalog_size²)` total work, but each commercial is
  processed independently and serially. ~10 minutes for 200 commercials on
  the staging hardware.

## Subset / version-cut relationships

A **subset relationship** appears when one commercial is a literal cut of
another (e.g. a 30s edit extracted from a 60s master). Without special
handling the algorithm above would flag *every* hash of the shorter cut as
shared (because every window of its audio also appears in the longer
master), causing the matcher to never confirm it — a false negative.

The algorithm classifies each pair (A, X) at the end of A's scan:

```
fraction = windows_with_hits_on_X / total_windows_in_scan

fraction ≥ SubsetThreshold (0.5) → subset/duplicate — DO NOT flag the pair
fraction <  SubsetThreshold      → sting overlap  — flag both sides
```

For a clean 30s extract of a 60s master, the 30s scan hits the 60s in 100%
of its windows. Reciprocally, the 60s scan hits the 30s in ~50% of its
windows (the matching half). Both fall at or above the threshold → neither
side is flagged. The runtime then relies on the
[version-disambiguation](version-disambiguation.md) layer: both versions
confirm legitimately, and the supervisor picks the longer cut.

For the AMB30/JINGLE sting (~6,25s overlap in 30s commercials), the scan
hits the other in ~11-15% of windows — well below the threshold — so flags
are applied normally and the false-positive defense works as designed.

The threshold lives in `sharing.SubsetThreshold` and is unit-tested in
[`sharing_test.go`](../workers/internal/sharing/sharing_test.go).

## Known limits

- `loudnorm` divergence: the same audio in two masters may produce hashes
  with different values. Exact-value collision misses ~98% of the genuinely
  shared content. Matching-engine simulation captures it correctly, at the
  cost of a longer per-upload step.
- **Duplicates** (two commercials with essentially identical audio): both
  scans see the other at ratio ≥ SubsetThreshold, so both are classified as
  subset and neither is flagged. The disambiguation-by-duration layer
  cannot break the tie (same duration) — both detections get published
  legitimately. Detect operationally via the `shared_pct` audit query
  before the fix landed; after the algorithm change duplicates surface as
  both rows with `shared = 0` and overlapping detection windows. Resolve
  by deleting the redundant master from the catalog.
- The flag is stored on `fingerprint_hashes` rather than on a separate
  ranges table. The denormalization keeps the runtime index loader on a
  single SELECT (no JOIN in the hot path); the trade-off is that resetting
  the flag for one commercial requires touching every row of that
  commercial.
