-- Enables unaccent() for case/accent-insensitive search used by the airtime
-- report endpoints (paginated /detections + aggregate-by-material + export).
-- Same semantics the frontend `tokenize()` helper applies client-side, now
-- pushed down to SQL so search results stay consistent across pages.
CREATE EXTENSION IF NOT EXISTS unaccent;
