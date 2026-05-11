-- 0016_material_library.up.sql
-- Adiciona biblioteca de materiais por cliente, decuplada de campanha.
-- Spec: docs/superpowers/specs/2026-05-11-campaign-wizard-design.md §5.1
--
-- Migração não-destrutiva: a tabela commercials permanece intacta.
-- Cada commercial existente vira um material com o MESMO UUID, preservando
-- referências em detections.commercial_id.

BEGIN;

-- ────── 1. material_types: registro global de tipos ──────

CREATE TABLE material_types (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL UNIQUE,
    color TEXT NOT NULL DEFAULT '#94a3b8',
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO material_types (name, color) VALUES
    ('Spot 30s',     '#3b82f6'),
    ('Spot 60s',     '#0ea5e9'),
    ('Testemunhal',  '#8b5cf6'),
    ('Citação',      '#14b8a6'),
    ('Vinheta',      '#f59e0b'),
    ('Jingle',       '#ec4899');

-- ────── 2. materials: catálogo do material em si ──────

CREATE TABLE materials (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    short_id SERIAL UNIQUE,
    client_id UUID NOT NULL REFERENCES clients(id) ON DELETE RESTRICT,
    title TEXT NOT NULL,
    type_id UUID REFERENCES material_types(id) ON DELETE SET NULL,
    duration_seconds NUMERIC(6,3) NOT NULL,
    master_storage_path TEXT NOT NULL,
    master_sha256 TEXT NOT NULL,
    fingerprint_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (fingerprint_status IN ('pending','generating','ready','failed')),
    fingerprint_generated_at TIMESTAMPTZ,
    fingerprint_hash_count INT,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    -- NOTA: NÃO adicionar UNIQUE(client_id, master_sha256) nesta migration.
    -- Commercials existentes podem ter duplicatas (mesmo MP3 em campanhas
    -- diferentes). Migrar 1:1 preserva referências de detections.commercial_id.
    -- Constraint pode ser adicionada em migration futura após limpeza manual.
);
CREATE INDEX idx_materials_client ON materials(client_id);
CREATE INDEX idx_materials_type ON materials(type_id);
CREATE INDEX idx_materials_status ON materials(fingerprint_status);

CREATE TRIGGER trg_materials_updated BEFORE UPDATE ON materials
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

-- ────── 3. campaign_materials: link N:N campanhas ↔ materiais ──────

CREATE TABLE campaign_materials (
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    material_id UUID NOT NULL REFERENCES materials(id) ON DELETE RESTRICT,
    target_stations UUID[] NOT NULL DEFAULT '{}',
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (campaign_id, material_id)
);
CREATE INDEX idx_campaign_materials_material ON campaign_materials(material_id);

-- ────── 4. Backfill: commercials → materials ──────

INSERT INTO materials (
    id, short_id, client_id, title, type_id, duration_seconds,
    master_storage_path, master_sha256,
    fingerprint_status, fingerprint_generated_at, fingerprint_hash_count,
    metadata, created_at, updated_at
)
SELECT
    c.id, c.short_id, cmp.client_id, c.title, NULL,
    c.duration_seconds, c.master_storage_path, c.master_sha256,
    c.fingerprint_status, c.fingerprint_generated_at, c.fingerprint_hash_count,
    c.metadata, c.created_at, c.updated_at
FROM commercials c
JOIN campaigns cmp ON cmp.id = c.campaign_id;

-- short_id sequence precisa avançar pra não colidir em novos inserts
SELECT setval(
    pg_get_serial_sequence('materials', 'short_id'),
    GREATEST((SELECT MAX(short_id) FROM materials), 1)
);

-- ────── 5. Backfill: campaign_materials ──────

INSERT INTO campaign_materials (campaign_id, material_id, target_stations, added_at)
SELECT c.campaign_id, c.id, c.target_stations, c.created_at
FROM commercials c;

COMMIT;
