-- Migration 0066 — classe Anatel da emissora + municípios no raio de cobertura.
--
-- Contexto: os Planos Básicos da Anatel (PBFM/PBOM) dizem a CLASSE de cada
-- canal outorgado. A classe determina o raio do contorno protegido de
-- 66 dBµV/m (Res. Anatel 546/2010), e com o raio + a coordenada da antena dá
-- pra listar quais municípios estão dentro do alcance do sinal.
--
-- Os planos não trazem o nome da emissora, então o cruzamento é por
-- dial + geografia e é INTRINSECAMENTE aproximado. Por isso guardamos a
-- procedência de cada match (anatel_match_tier + anatel_match_distance_km +
-- anatel_plan_city/state) junto do dado: sem isso não há como auditar depois
-- por que uma emissora ficou com a classe que ficou.
--
-- SAFETY: `stations` é lida pelo supervisor/reconciler em runtime, mas estas
-- são ADD COLUMN nullable (sem DEFAULT volátil) — rewrite-free no PG 11+, o
-- lock é AccessExclusive por instantes. Ainda assim o lock_timeout evita que
-- a DDL entre na fila FIFO e prenda leitura de `stations` atrás de si durante
-- o deploy, quando a API antiga segue servindo tráfego.
--
-- Doc: docs/features/anatel-station-class-coverage.md

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

ALTER TABLE stations
    -- Classe do plano (FM: E1..E3, A1..A4, B1, B2, C; AM: A, B, C) ou
    -- 'RADCOM' para rádio comunitária, que tem plano próprio e parâmetros
    -- fixados em lei (25 W / 30 m / raio de 1 km).
    ADD COLUMN IF NOT EXISTS anatel_class             TEXT,
    -- Raio do contorno protegido, em km, derivado da classe.
    -- NULL para AM: a norma de OM define o contorno protegido em intensidade
    -- de campo (mV/m da onda de superfície), NÃO em distância — o alcance real
    -- depende da frequência e da condutividade do solo de cada radial. NULL
    -- aqui significa "cobertura indeterminada", nunca "cobertura zero".
    ADD COLUMN IF NOT EXISTS anatel_coverage_km       NUMERIC(5,1),
    -- Raio de ALCANCE = contorno protegido + transbordo (×1,5). O contorno é
    -- onde o sinal é juridicamente protegido contra interferência; o alcance é
    -- até onde ele ainda é ouvido na prática. É este raio — não o contorno —
    -- que define station_coverage_cities. O fator vem do E-radios, que aplica
    -- o mesmo +50% sobre a mesma tabela da Res. 546/2010, para que os dois
    -- sistemas respondam a mesma coisa sobre a mesma emissora.
    ADD COLUMN IF NOT EXISTS anatel_reach_km          NUMERIC(6,1),
    ADD COLUMN IF NOT EXISTS anatel_erp_kw            NUMERIC(10,4),
    -- Coordenada da ANTENA segundo o plano. Distinta de latitude/longitude,
    -- que são o centroide do município do cadastro (migration 0002). É a
    -- antena que deve ancorar o cálculo de cobertura.
    ADD COLUMN IF NOT EXISTS anatel_latitude          NUMERIC(9,6),
    ADD COLUMN IF NOT EXISTS anatel_longitude         NUMERIC(9,6),
    -- Procedência do match:
    --   exact  — banda + UF + município + dial idênticos ao plano
    --   geo    — mesmo dial, antena dentro do raio da classe candidata
    --            (emissora licenciada em município vizinho ao do cadastro)
    --   radcom — comunitária, classificada por lei sem consultar plano
    ADD COLUMN IF NOT EXISTS anatel_match_tier        TEXT,
    -- Distância entre a coordenada do cadastro e a antena do plano.
    -- É o número que permite revisar um match suspeito sem refazer a conta.
    ADD COLUMN IF NOT EXISTS anatel_match_distance_km NUMERIC(6,1),
    -- Município de LICENÇA. Diverge de city/state exatamente nos matches
    -- tier=geo, e é essa divergência que explica o match a quem for auditar.
    ADD COLUMN IF NOT EXISTS anatel_plan_city         TEXT,
    ADD COLUMN IF NOT EXISTS anatel_plan_state        CHAR(2),
    ADD COLUMN IF NOT EXISTS anatel_matched_at        TIMESTAMPTZ;

ALTER TABLE stations
    DROP CONSTRAINT IF EXISTS stations_anatel_match_tier_check;
ALTER TABLE stations
    ADD CONSTRAINT stations_anatel_match_tier_check
    CHECK (anatel_match_tier IS NULL OR anatel_match_tier IN ('exact','geo','radcom'));

-- Municípios dentro do contorno protegido da emissora.
-- Derivada: reconstruível a qualquer momento por `backfill-anatel`. Persistida
-- porque o cálculo é um produto cartesiano emissora × 5.570 municípios, caro
-- demais pra refazer por request.
CREATE TABLE IF NOT EXISTS station_coverage_cities (
    -- CASCADE: a cobertura não tem vida própria fora da emissora.
    station_id  UUID        NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
    ibge_code   INTEGER     NOT NULL,
    city        TEXT        NOT NULL,
    state       CHAR(2)     NOT NULL,
    -- Distância da antena ao centroide do município. Comparada com
    -- stations.anatel_coverage_km separa o núcleo do transbordo:
    --   distance_km <= anatel_coverage_km  → dentro do contorno protegido
    --   distance_km >  anatel_coverage_km  → transbordo
    distance_km NUMERIC(6,1) NOT NULL,
    -- Município de licença/cadastro da emissora. Entra na cobertura SEMPRE,
    -- por premissa da outorga, mesmo quando o centroide cai fora do raio da
    -- classe — a antena costuma ficar num morro fora da cidade e o centroide
    -- não é a sede, então a geometria aproximada erra justamente aqui.
    -- Logo: `distance_km > anatel_coverage_km` é ESPERADO quando is_home.
    is_home     BOOLEAN     NOT NULL DEFAULT FALSE,
    -- Centroide do município, copiado do dataset IBGE embutido. Denormalizado
    -- de propósito: o /live-map precisa plotar a cidade no mapa, e sem isto
    -- toda leitura teria que reentrar no dataset em Go — a API deixaria de ser
    -- um SELECT e o dado ficaria inconsultável por SQL.
    latitude    NUMERIC(9,6) NOT NULL,
    longitude   NUMERIC(9,6) NOT NULL,
    PRIMARY KEY (station_id, ibge_code)
);

-- Busca reversa: "quais emissoras alcançam este município?" — é a direção que
-- a operação comercial usa (montar praça a partir da cidade do cliente).
CREATE INDEX IF NOT EXISTS idx_station_coverage_cities_ibge
    ON station_coverage_cities (ibge_code);

CREATE INDEX IF NOT EXISTS idx_station_coverage_cities_uf_city
    ON station_coverage_cities (state, city);

-- Emissoras sem classe: é a fila de trabalho manual do backfill.
CREATE INDEX IF NOT EXISTS idx_stations_anatel_unmatched
    ON stations (band, state) WHERE anatel_class IS NULL;

COMMIT;
