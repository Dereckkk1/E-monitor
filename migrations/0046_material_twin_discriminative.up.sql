-- 0046_material_twin_discriminative.up.sql
-- Regiões discriminantes por par de gêmeos acústicos (spec 2026-07-01).
-- disc_ranges = frame-ranges de material_id que NÃO se sobrepõem a twin_id.
-- disc_frames = total de frames discriminantes (denominador da cobertura).
-- disc_frames = 0 => par não separável pelo áudio (sempre ambíguo).
BEGIN;

CREATE TABLE material_twin_discriminative (
    material_id  UUID          NOT NULL REFERENCES materials(id) ON DELETE CASCADE,
    twin_id      UUID          NOT NULL REFERENCES materials(id) ON DELETE CASCADE,
    disc_ranges  int4range[]   NOT NULL,
    disc_frames  INT           NOT NULL,
    updated_at   TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (material_id, twin_id)
);
CREATE INDEX idx_twin_disc_material ON material_twin_discriminative(material_id);

COMMIT;
