-- 0054_client_station_pmm.up.sql
-- PMM no target por cliente: para cada par (cliente, emissora), a audiência
-- dentro do público-alvo daquele cliente naquela emissora.
--
-- Decisões de design:
--
-- 1. Granularidade é (cliente × emissora), não (campanha × emissora): o target
--    é uma propriedade do cliente e vale para todas as campanhas dele. A
--    resolução em toda query é client_station_pmm[campanha.client_id, station_id].
--
-- 2. AUSÊNCIA DE LINHA = "não cadastrado" → a emissora fica FORA do total de
--    impactos no target e fora do contador "X de Y emissoras com target".
--    O valor 0 é legítimo e DIFERENTE disso: significa target zero e CONTA
--    como cadastrada. Por isso não há DEFAULT nem NULL em pmm_target.
--
-- 3. Sem versionamento temporal — o valor corrente vale para todo o histórico,
--    igual ao stations.pmm de hoje. Corrigir um valor muda relatórios passados.

BEGIN;

CREATE TABLE IF NOT EXISTS client_station_pmm (
    client_id  UUID    NOT NULL REFERENCES clients(id)  ON DELETE CASCADE,
    station_id UUID    NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
    pmm_target INTEGER NOT NULL CHECK (pmm_target >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (client_id, station_id)
);

-- Lookup reverso (quais clientes têm target numa emissora) e suporte ao
-- LEFT JOIN por station_id nas agregações.
CREATE INDEX IF NOT EXISTS idx_client_station_pmm_station
    ON client_station_pmm(station_id);

-- touch_updated_at() já existe desde a 0022 (pricing).
DROP TRIGGER IF EXISTS trg_cspmm_touch ON client_station_pmm;
CREATE TRIGGER trg_cspmm_touch
    BEFORE UPDATE ON client_station_pmm
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

COMMIT;
