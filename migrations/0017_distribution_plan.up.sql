-- 0017_distribution_plan.up.sql
-- Regras de distribuição (plano "programado") e overrides por célula.
-- Spec: docs/superpowers/specs/2026-05-11-campaign-wizard-design.md §5.2

BEGIN;

-- ────── Regras de distribuição ──────

CREATE TABLE distribution_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    material_id UUID NOT NULL REFERENCES materials(id) ON DELETE CASCADE,
    station_ids UUID[] NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    -- weekday_mask: bitmask. Bit 0=Domingo, 1=Segunda, ..., 6=Sábado.
    -- Ex: seg-sex = 0b0111110 = 62
    weekday_mask SMALLINT NOT NULL CHECK (weekday_mask BETWEEN 0 AND 127),
    time_start TIME NOT NULL,
    time_end TIME NOT NULL,
    plays_per_day SMALLINT NOT NULL CHECK (plays_per_day > 0 AND plays_per_day <= 100),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT rule_dates_valid CHECK (end_date >= start_date),
    CONSTRAINT rule_times_valid CHECK (time_end > time_start)
);
CREATE INDEX idx_distribution_rules_campaign ON distribution_rules(campaign_id);
CREATE INDEX idx_distribution_rules_material ON distribution_rules(material_id);
CREATE INDEX idx_distribution_rules_dates ON distribution_rules(start_date, end_date);

CREATE TRIGGER trg_distribution_rules_updated BEFORE UPDATE ON distribution_rules
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

-- ────── Overrides por célula ──────

CREATE TABLE distribution_overrides (
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    material_id UUID NOT NULL REFERENCES materials(id) ON DELETE CASCADE,
    station_id UUID NOT NULL REFERENCES stations(id),
    for_date DATE NOT NULL,
    plays_expected SMALLINT NOT NULL CHECK (plays_expected >= 0),
    reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by UUID,  -- FK lógica pra users(id); NULL quando criado por migration ou worker
    PRIMARY KEY (campaign_id, material_id, station_id, for_date)
);
CREATE INDEX idx_distribution_overrides_date ON distribution_overrides(for_date);

COMMIT;
