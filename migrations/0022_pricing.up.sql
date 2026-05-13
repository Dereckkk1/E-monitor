-- 0022_pricing.up.sql
-- Modelo de preços por campanha + emissora.
--
-- Decisões de design:
--
-- 1. PMM (Penetração Média por Minuto / audiência) já existe em stations.pmm
--    desde a migration 0002 (NUMERIC(10,2)). Toda campanha herda. Se houver
--    demanda futura de override por contrato, adicionamos uma coluna nullable
--    em campaign_station_pricing.
--
-- 2. Pricing é por (campaign, station) — cada emissora dentro de uma campanha
--    escolhe seu modo independentemente. Dois modos:
--      • consolidated  → um valor fechado pra emissora inteira na campanha,
--                        irrelevante quantos materiais ou inserções rodaram.
--      • per_insertion → valor unitário por TIPO de material (Spot,
--                        Testemunhal, etc.). Valor total = unit_value × in_slot
--                        (categorizadas dentro da faixa).
--
-- 3. CHECK constraint garante que `consolidated_value` só faz sentido no modo
--    consolidated. O detalhe por tipo (tabela campaign_station_type_pricing)
--    só é populado quando o modo é per_insertion — não há check forte cruzando
--    tabelas, fica a cargo da app validar.

BEGIN;

-- ────── 1. Pricing por (campanha × emissora) ──────

CREATE TABLE IF NOT EXISTS campaign_station_pricing (
    campaign_id        UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    station_id         UUID NOT NULL REFERENCES stations(id)  ON DELETE CASCADE,
    mode               TEXT NOT NULL
        CHECK (mode IN ('consolidated', 'per_insertion')),
    consolidated_value NUMERIC(12, 2)
        CHECK (consolidated_value IS NULL OR consolidated_value >= 0),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (campaign_id, station_id),
    -- consolidated_value SÓ pode ser NOT NULL quando mode=consolidated
    CONSTRAINT consolidated_value_matches_mode CHECK (
        (mode = 'consolidated' AND consolidated_value IS NOT NULL)
        OR
        (mode = 'per_insertion' AND consolidated_value IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_campaign_station_pricing_station
    ON campaign_station_pricing(station_id);

-- ────── 2. Pricing por tipo de material (quando mode=per_insertion) ──────

CREATE TABLE IF NOT EXISTS campaign_station_type_pricing (
    campaign_id UUID    NOT NULL REFERENCES campaigns(id)       ON DELETE CASCADE,
    station_id  UUID    NOT NULL REFERENCES stations(id)        ON DELETE CASCADE,
    type_id     UUID    NOT NULL REFERENCES material_types(id)  ON DELETE RESTRICT,
    unit_value  NUMERIC(12, 2) NOT NULL CHECK (unit_value >= 0),
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ    NOT NULL DEFAULT now(),
    PRIMARY KEY (campaign_id, station_id, type_id),
    -- Garante que a linha "pai" em campaign_station_pricing existe e está
    -- em modo per_insertion (a aplicação verifica antes de inserir; o FK só
    -- garante existência básica).
    FOREIGN KEY (campaign_id, station_id)
        REFERENCES campaign_station_pricing (campaign_id, station_id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_campaign_station_type_pricing_lookup
    ON campaign_station_type_pricing(campaign_id, station_id);

-- ────── 3. Trigger pra manter updated_at em sync ──────

CREATE OR REPLACE FUNCTION touch_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_csp_touch ON campaign_station_pricing;
CREATE TRIGGER trg_csp_touch
    BEFORE UPDATE ON campaign_station_pricing
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

DROP TRIGGER IF EXISTS trg_cstp_touch ON campaign_station_type_pricing;
CREATE TRIGGER trg_cstp_touch
    BEFORE UPDATE ON campaign_station_type_pricing
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

COMMIT;
