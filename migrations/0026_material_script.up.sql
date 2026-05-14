-- 0026_material_script.up.sql
-- Adds an optional free-text script field to materials. Operators fill it in
-- at upload time (or later, via the wizard's material card) so detections of
-- the material can surface the spoken copy on /detections/:id — useful for
-- clients comparing what aired vs. what was approved.
--
-- Nullable on purpose: not every material has a script (jingles, sound logos
-- etc. are pure music). No length cap — kept as TEXT because broadcast copy
-- can range from 5-second taglines to 60-second narratives.

BEGIN;

ALTER TABLE materials
    ADD COLUMN script TEXT;

COMMIT;
