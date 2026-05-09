-- §18.2.2 follow-up — Shared-hash detection.
--
-- A fingerprint hash is "shared" when its time_frame falls inside a region of
-- the commercial that overlaps audio with another commercial. During matching
-- we count all hits for the histogram peak (preserving robustness against
-- degraded streams), but only hits coming from non-shared hashes advance the
-- state machine toward confirmation. This eliminates false positives where a
-- short shared sting (e.g. a 6s element reused at the end of a 30s spoken
-- spot) is enough to accumulate ≥15% time-coverage on the wrong commercial.
--
-- Detection is by matching-engine simulation, not exact hash-value collision:
-- the loudnorm filter in the fingerprint pipeline is per-file, so the same
-- audio in two different files produces *different* hash values. The
-- production helper (workers/internal/fingerprint/sharing.go) decodes the
-- master, runs MatchWindow against the existing index, and flags hashes
-- whose time_frame falls inside any window where another commercial scored
-- above the runtime minScore.
--
-- The flag is denormalized onto fingerprint_hashes so the matching index can
-- be loaded with a single SELECT — adding a JOIN in the hot path was
-- considered and rejected (the index is rebuilt on every catalog change).

ALTER TABLE fingerprint_hashes
    ADD COLUMN IF NOT EXISTS is_shared BOOLEAN NOT NULL DEFAULT false;
