-- diagnose-base-a-financials.sql
-- ============================================================================
-- Diagnóstico READ-ONLY da base financeira unificada (base A = in_slot+bonus)
-- da branch feat/unify-campaigns-insights-financials. Reproduz, em SQL puro, o
-- que `financialBaseCTE` calcula, para conferir os números ANTES do deploy
-- (regra 4.8 do CLAUDE.md — a mudança altera cobrança).
--
-- COMO USAR (Dereck, contra uma CÓPIA de prod num Postgres descartável):
--   1. Restaure o dump de prod num PG descartável.
--   2. Edite os 3 :params abaixo (janela) e o filtro de campanha no final.
--   3. Rode e compare:
--      - Janela ACUMULADO (from bem antigo, to=hoje): os números de impactos/
--        investido/CPM devem BATER com o que o /campaigns mostra HOJE em prod
--        (a base do /campaigns já era in_slot+bonus; só a janela é nova). Se
--        bater, a migração preservou o /campaigns.
--      - Janela MÊS CORRENTE: devem BATER com o /insights do mesmo mês (é o
--        objetivo da unificação). O /insights vai MUDAR vs. hoje (saiu de
--        "todas aprovadas" para in_slot+bonus) — confira que os novos números
--        fazem sentido (≤ os antigos, pois exclui out_slot/out_date).
--   4. CPM no target = investido ÷ impactos_target × 1000 (calcule à mão).
--
-- NÃO faz nenhum UPDATE/INSERT. Seguro rodar. Ainda assim, QUEM roda em prod
-- é o Dereck (regra 7) — o Claude não tem acesso.
-- ============================================================================

-- ACUMULADO: p_from = data bem antiga. Para "mês corrente", use o 1o dia do mês em p_from
-- e o último dia do mês em p_to. p_today = "hoje" (acúmulo mensal do consolidado).
-- ATENÇÃO: NÃO ponha comentário na mesma linha do \set — o psql inclui o texto no valor.
\set p_from '2000-01-01'
\set p_to '2026-07-31'
\set p_today '2026-07-24'

WITH
fb_dps AS (
    SELECT campaign_id, station_id, type_id, in_slot, bonus
    FROM daily_play_summary_for(DATE :'p_from', DATE :'p_to', NULL)
),
fb_plays AS (
    SELECT campaign_id, station_id, SUM(in_slot + bonus)::bigint AS plays
    FROM fb_dps GROUP BY campaign_id, station_id
),
fb_perins AS (
    SELECT d.campaign_id, d.station_id,
           COALESCE(SUM(tp.unit_value * (d.in_slot + d.bonus)), 0)::numeric AS invested
    FROM fb_dps d
    JOIN campaign_station_type_pricing tp
      ON tp.campaign_id = d.campaign_id
     AND tp.station_id  = d.station_id
     AND tp.type_id     = d.type_id
    GROUP BY d.campaign_id, d.station_id
),
fin_base AS (
    SELECT p.campaign_id, p.station_id, c.client_id,
           COALESCE(fp.plays, 0)::bigint AS plays,
           (CASE
               WHEN p.mode = 'consolidated'
                   THEN COALESCE(p.consolidated_value, 0)::numeric
                        * (SELECT count(*)::int
                             FROM generate_series(date_trunc('month', c.start_date),
                                                  date_trunc('month', c.end_date),
                                                  interval '1 month') gm(ms)
                            WHERE GREATEST(gm.ms::date, c.start_date) <= DATE :'p_today'
                              AND GREATEST(gm.ms::date, c.start_date) <= DATE :'p_to'
                              AND LEAST((gm.ms + interval '1 month' - interval '1 day')::date, c.end_date) >= DATE :'p_from')
               WHEN p.mode = 'per_insertion' THEN COALESCE(fpi.invested, 0)
               ELSE 0
           END)::float8 AS invested
    FROM campaign_station_pricing p
    JOIN campaigns c ON c.id = p.campaign_id
    LEFT JOIN fb_plays  fp  ON fp.campaign_id  = p.campaign_id AND fp.station_id  = p.station_id
    LEFT JOIN fb_perins fpi ON fpi.campaign_id = p.campaign_id AND fpi.station_id = p.station_id
)
SELECT
    c.name                                                                        AS campanha,
    SUM(fb.plays)                                                                  AS insercoes,
    SUM(fb.plays * COALESCE(st.pmm, 0))::bigint                                    AS impactos,
    SUM(fb.plays * COALESCE(cst.pmm_target, 0))::bigint                            AS impactos_target,
    ROUND(SUM(fb.invested)::numeric, 2)                                           AS investido,
    COUNT(DISTINCT fb.station_id) FILTER (WHERE cst.pmm_target IS NOT NULL AND fb.plays > 0) AS emissoras_com_target,
    CASE WHEN SUM(fb.plays * COALESCE(cst.pmm_target, 0)) > 0
         THEN ROUND((SUM(fb.invested) / SUM(fb.plays * COALESCE(cst.pmm_target, 0)) * 1000)::numeric, 2)
    END                                                                           AS cpm_no_target
FROM fin_base fb
JOIN campaigns c ON c.id = fb.campaign_id
LEFT JOIN stations st ON st.id = fb.station_id
LEFT JOIN client_station_pmm cst ON cst.client_id = fb.client_id AND cst.station_id = fb.station_id
-- >>> EDITE o filtro: nome da campanha que você quer conferir (ou remova o WHERE p/ todas) <<<
WHERE c.name ILIKE '%EDITE_AQUI%'
GROUP BY c.name
ORDER BY c.name;
