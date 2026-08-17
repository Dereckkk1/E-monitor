-- 0065: o resumo diário passa a falar o modelo de cota.
-- Spec: docs/superpowers/specs/2026-08-14-quota-aware-categorization-design.md (D3 + §2)
--
-- Duas mudanças, nos DOIS objetos (view e função — eles têm que continuar
-- semanticamente idênticos para a mesma janela; ver
-- scripts/sql/paridade-dps-function.sql):
--
--   1. deficit = GREATEST(0, expected - in_slot)   (antes: - in_slot - out_slot)
--      D3: out_slot NÃO abate o contrato. Tocada fora da faixa contratada não
--      fecha a obrigação — o déficit continua aberto. Antes ela abatia, e um dia
--      inteiro tocado fora do horário aparecia como "cumprido".
--
--   2. bonus = COUNT(*) FILTER (WHERE category = 'bonus')  (antes:
--      GREATEST(0, in_slot - expected) + orphan)
--      §2: com o fechamento por cota, in_slot <= expected POR CONSTRUÇÃO (o
--      categorizador só promove N tocadas a in_slot e manda o excedente pra
--      'bonus'), então o primeiro termo era sempre 0. O categorizador vira a
--      fonte única do bônus e a view só conta a categoria.
--
-- E na CTE `actual` das duas definições o contador de 'orphan' vira 'bonus' —
-- 0064 já renomeou o dado histórico. ESTA MIGRATION É O QUE FECHA A JANELA
-- ABERTA POR 0064: entre 0064 e 0065 o bônus lê 0 em todas as telas (o dado já
-- é 'bonus', a view ainda procurava 'orphan'). Por isso as duas têm que ir
-- juntas no mesmo `migrate up` — a branch não pode chegar em prod sem esta.
--
-- Contadores residuais 'orphan': o CHECK de 0063 ainda aceita 'orphan' de
-- propósito (binário antigo da API durante o deploy pode gravar), e essas
-- linhas NÃO são somadas aqui. Elas convergem sozinhas: o reconciler
-- `projrecon` re-assenta as últimas 48h a cada 15 min e o backfill global cobre
-- o histórico. É deliberado não somar os dois valores — somar esconderia um
-- produtor que continuasse gravando 'orphan' pra sempre.
--
-- O resto das duas definições é CÓPIA VERBATIM de 0041 (view) e 0052 (função):
-- exclusão de audit_rejected/retracted/ignored, date_trunc em America/Sao_Paulo,
-- bounds sargáveis pra poda de partição, contrato de NULL de p_from/p_to. Nada
-- disso muda aqui.
--
-- Segurança: só troca a definição de uma view e de uma função. Nenhum DML,
-- nenhum lock em detections/detection_campaigns. A lista de colunas de saída
-- (nome, tipo e ordem) é idêntica nos dois objetos, então CREATE OR REPLACE
-- passa — e nenhum outro objeto do banco depende da view (checado em
-- pg_depend/pg_rewrite: zero dependentes).

BEGIN;

-- daily_play_summary: cópia de 0041_detection_campaigns.up.sql:78-141 com as
-- duas trocas acima. CREATE OR REPLACE (não DROP+CREATE): a lista de colunas
-- não muda, e assim nenhum grant/dependente é perdido.
CREATE OR REPLACE VIEW daily_play_summary AS
WITH expected AS (
    SELECT
        r.campaign_id,
        r.type_id,
        s.station_id,
        d.for_date::date AS for_date,
        SUM(r.plays_per_day)::int AS rule_expected
    FROM distribution_rules r
    CROSS JOIN LATERAL unnest(r.station_ids) AS s(station_id)
    CROSS JOIN LATERAL generate_series(r.start_date, r.end_date, INTERVAL '1 day') AS d(for_date)
    WHERE (1 << EXTRACT(DOW FROM d.for_date)::INT) & r.weekday_mask != 0
    GROUP BY r.campaign_id, r.type_id, s.station_id, d.for_date
),
expected_with_override AS (
    SELECT
        COALESCE(o.campaign_id, e.campaign_id) AS campaign_id,
        COALESCE(o.type_id,     e.type_id)     AS type_id,
        COALESCE(o.station_id,  e.station_id)  AS station_id,
        COALESCE(o.for_date,    e.for_date)    AS for_date,
        COALESCE(o.plays_expected, e.rule_expected)::int AS expected
    FROM expected e
    FULL OUTER JOIN distribution_overrides o
        ON e.campaign_id = o.campaign_id
       AND e.type_id     = o.type_id
       AND e.station_id  = o.station_id
       AND e.for_date    = o.for_date
),
actual AS (
    SELECT
        dc.campaign_id,
        m.type_id,
        d.station_id,
        date_trunc('day', d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS for_date,
        COUNT(*) FILTER (WHERE dc.category = 'in_slot')::int  AS in_slot,
        COUNT(*) FILTER (WHERE dc.category = 'out_slot')::int AS out_slot,
        COUNT(*) FILTER (WHERE dc.category = 'out_date')::int AS out_date,
        COUNT(*) FILTER (WHERE dc.category = 'bonus')::int    AS bonus
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    JOIN materials   m ON m.id = dc.commercial_id
    WHERE d.retracted_at IS NULL
      AND d.ignored_at IS NULL
      AND d.evidence_status <> 'audit_rejected'
      AND m.type_id IS NOT NULL
    GROUP BY dc.campaign_id, m.type_id, d.station_id, for_date
)
SELECT
    COALESCE(e.campaign_id, a.campaign_id) AS campaign_id,
    COALESCE(e.type_id,     a.type_id)     AS type_id,
    COALESCE(e.station_id,  a.station_id)  AS station_id,
    COALESCE(e.for_date,    a.for_date)    AS for_date,
    COALESCE(e.expected, 0)::int                                                    AS expected,
    COALESCE(a.in_slot,  0)::int                                                    AS in_slot,
    GREATEST(0, COALESCE(e.expected,0) - COALESCE(a.in_slot,0))::int                AS deficit,
    COALESCE(a.bonus, 0)::int                                                       AS bonus,
    COALESCE(a.out_slot, 0)::int                                                    AS out_slot,
    COALESCE(a.out_date, 0)::int                                                    AS out_date
FROM expected_with_override e
FULL OUTER JOIN actual a
    ON e.campaign_id = a.campaign_id
   AND e.type_id     = a.type_id
   AND e.station_id  = a.station_id
   AND e.for_date    = a.for_date;

-- daily_play_summary_for: cópia de 0052_daily_play_summary_fn.up.sql com as
-- mesmas duas trocas. O cabeçalho de 0052 (por que não há pushdown na view,
-- equivalência validada por scripts/sql/paridade-dps-function.sql, contrato de
-- NULL de p_from/p_to e por que NÃO usar STRICT) continua valendo integralmente
-- e está reproduzido nos comentários abaixo.
--
-- Contrato de NULL: p_from/p_to são OBRIGATÓRIOS (NOT NULL) — GREATEST/LEAST
-- ignoram NULL silenciosamente (a CTE expected continuaria produzindo linhas
-- reais), mas `p_from::timestamp AT TIME ZONE ...` vira NULL e zera a CTE
-- actual, o que produziria "100% de déficit" plausível e ERRADO em vez de um
-- erro visível. Por isso a CTE expected também é blindada com
-- `p_from IS NOT NULL AND p_to IS NOT NULL`: com qualquer um dos dois NULL a
-- função devolve VAZIO (obviamente quebrado), nunca um número fabricado.
-- Não usar STRICT/RETURNS NULL ON NULL INPUT: mataria o `p_campaigns IS NULL`
-- legítimo (= "todas as campanhas"). p_campaigns = '{}' (array vazio, não
-- NULL) é "zero campanhas" via semântica de `= ANY('{}')` — não é bug, mas
-- callers devem passar NULL, nunca array vazio, para "todas".
--
-- A lista de colunas do RETURNS TABLE é INTOCADA (nome, tipo e ordem): os
-- callers fazem Scan posicional.
CREATE OR REPLACE FUNCTION daily_play_summary_for(p_from date, p_to date, p_campaigns uuid[] DEFAULT NULL)
RETURNS TABLE (
    campaign_id uuid, type_id uuid, station_id uuid, for_date date,
    expected int, in_slot int, deficit int, bonus int, out_slot int, out_date int)
LANGUAGE sql STABLE AS $$
WITH expected AS (
    SELECT
        r.campaign_id,
        r.type_id,
        s.station_id,
        d.for_date::date AS for_date,
        SUM(r.plays_per_day)::int AS rule_expected
    FROM distribution_rules r
    CROSS JOIN LATERAL unnest(r.station_ids) AS s(station_id)
    CROSS JOIN LATERAL generate_series(
        GREATEST(r.start_date, p_from),
        LEAST(r.end_date, p_to),
        INTERVAL '1 day') AS d(for_date)
    WHERE p_from IS NOT NULL AND p_to IS NOT NULL
      AND (p_campaigns IS NULL OR r.campaign_id = ANY(p_campaigns))
      AND (1 << EXTRACT(DOW FROM d.for_date)::INT) & r.weekday_mask != 0
    GROUP BY r.campaign_id, r.type_id, s.station_id, d.for_date
),
expected_with_override AS (
    SELECT
        COALESCE(o.campaign_id, e.campaign_id) AS campaign_id,
        COALESCE(o.type_id,     e.type_id)     AS type_id,
        COALESCE(o.station_id,  e.station_id)  AS station_id,
        COALESCE(o.for_date,    e.for_date)    AS for_date,
        COALESCE(o.plays_expected, e.rule_expected)::int AS expected
    FROM expected e
    FULL OUTER JOIN (
        SELECT * FROM distribution_overrides ov
        WHERE ov.for_date BETWEEN p_from AND p_to
          AND (p_campaigns IS NULL OR ov.campaign_id = ANY(p_campaigns))
    ) o
        ON e.campaign_id = o.campaign_id
       AND e.type_id     = o.type_id
       AND e.station_id  = o.station_id
       AND e.for_date    = o.for_date
),
actual AS (
    SELECT
        dc.campaign_id,
        m.type_id,
        d.station_id,
        date_trunc('day', d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS for_date,
        COUNT(*) FILTER (WHERE dc.category = 'in_slot')::int  AS in_slot,
        COUNT(*) FILTER (WHERE dc.category = 'out_slot')::int AS out_slot,
        COUNT(*) FILTER (WHERE dc.category = 'out_date')::int AS out_date,
        COUNT(*) FILTER (WHERE dc.category = 'bonus')::int    AS bonus
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    JOIN materials   m ON m.id = dc.commercial_id
    WHERE (p_campaigns IS NULL OR dc.campaign_id = ANY(p_campaigns))
      AND dc.detected_at >= (p_from::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND dc.detected_at <  ((p_to + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND d.detected_at  >= (p_from::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND d.detected_at  <  ((p_to + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND d.retracted_at IS NULL
      AND d.ignored_at IS NULL
      AND d.evidence_status <> 'audit_rejected'
      AND m.type_id IS NOT NULL
    GROUP BY dc.campaign_id, m.type_id, d.station_id,
             date_trunc('day', d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
)
SELECT
    COALESCE(e.campaign_id, a.campaign_id) AS campaign_id,
    COALESCE(e.type_id,     a.type_id)     AS type_id,
    COALESCE(e.station_id,  a.station_id)  AS station_id,
    COALESCE(e.for_date,    a.for_date)    AS for_date,
    COALESCE(e.expected, 0)::int AS expected,
    COALESCE(a.in_slot,  0)::int AS in_slot,
    GREATEST(0, COALESCE(e.expected,0) - COALESCE(a.in_slot,0))::int AS deficit,
    COALESCE(a.bonus, 0)::int AS bonus,
    COALESCE(a.out_slot, 0)::int AS out_slot,
    COALESCE(a.out_date, 0)::int AS out_date
FROM expected_with_override e
FULL OUTER JOIN actual a
    ON e.campaign_id = a.campaign_id
   AND e.type_id     = a.type_id
   AND e.station_id  = a.station_id
   AND e.for_date    = a.for_date
$$;

COMMIT;
