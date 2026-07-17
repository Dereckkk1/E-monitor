-- Paridade view × função daily_play_summary_for(). Rodar contra CLONE de
-- dados de prod (regra 4.8 do CLAUDE.md; procedimento: docs/operations/migrations.md).
-- Esperado: TODOS os counts abaixo = 0. Qualquer linha != 0 é divergência =
-- NÃO MERGEAR / NÃO DEPLOYAR.
--
-- Cobertura (Task 10 Step 3 do plano de performance):
--   (i)   p_campaigns NULL                         -> Janela 1
--   (ii)  p_campaigns com array                     -> Janela 2, 3, 5, 6, 7
--   (iii) bordas do GREATEST/LEAST (regra cruzando   -> Janela 3 (span completo),
--         a janela dos dois lados; regra parcial        Janela 4 (regra encerra
--         de entrada/saída; regra inteiramente fora)     antes da janela),
--                                                        Janela 5 (regra começa
--                                                        depois da janela)
--   (iv)  dias com override mas sem regra           -> Janela 6
--   (v)   dias com regra mas sem detecção            -> implícito em todas (linhas
--                                                        de deficit puro aparecem
--                                                        em qualquer janela com
--                                                        expected>0 e sem actual)
--   (vi)  campanha cancelada                         -> Janela 7
--
-- Janelas 3-7 são DINÂMICAS: escolhem candidatos reais no dataset (a maior
-- regra, uma regra que termina antes de hoje, um override sem regra
-- correspondente, uma campanha cancelada) em vez de UUIDs fixos — assim o
-- script funciona tanto contra um DB de teste semeado quanto contra um clone
-- de prod. Se um candidato não existir no dataset (ex.: nenhuma campanha
-- cancelada), a janela correspondente compara conjunto vazio contra conjunto
-- vazio (0=0) — o SELECT de diagnóstico antes de cada janela avisa quando
-- isso acontece, para não confundir "não teve o que testar" com "passou".

-- ============================================================
-- Janela 1: últimos 60 dias, todas as campanhas (exercita p_campaigns NULL).
-- ============================================================
WITH v AS (
    SELECT campaign_id, type_id, station_id, for_date,
           expected, in_slot, deficit, bonus, out_slot, out_date
    FROM daily_play_summary
    WHERE for_date BETWEEN CURRENT_DATE - 60 AND CURRENT_DATE
), f AS (
    SELECT * FROM daily_play_summary_for(CURRENT_DATE - 60, CURRENT_DATE, NULL)
)
SELECT 'janela1_view_minus_fn' AS lado, COUNT(*) FROM (SELECT * FROM v EXCEPT SELECT * FROM f) x
UNION ALL
SELECT 'janela1_fn_minus_view', COUNT(*) FROM (SELECT * FROM f EXCEPT SELECT * FROM v) y;

-- ============================================================
-- Janela 2: 1 ano inteiro + 90d futuro, top 10 campanhas recentes (exercita
-- p_campaigns com array e GREATEST/LEAST em escala).
-- ============================================================
WITH alvo AS (SELECT id FROM campaigns ORDER BY created_at DESC LIMIT 10),
v AS (
    SELECT campaign_id, type_id, station_id, for_date,
           expected, in_slot, deficit, bonus, out_slot, out_date
    FROM daily_play_summary
    WHERE for_date BETWEEN CURRENT_DATE - 365 AND CURRENT_DATE + 90
      AND campaign_id IN (SELECT id FROM alvo)
), f AS (
    SELECT * FROM daily_play_summary_for(CURRENT_DATE - 365, CURRENT_DATE + 90,
                                         (SELECT array_agg(id) FROM alvo))
)
SELECT 'janela2_view_minus_fn' AS lado, COUNT(*) FROM (SELECT * FROM v EXCEPT SELECT * FROM f) x
UNION ALL
SELECT 'janela2_fn_minus_view', COUNT(*) FROM (SELECT * FROM f EXCEPT SELECT * FROM v) y;

-- ============================================================
-- Janela 3: regra com o MAIOR span (start_date/end_date) do dataset, testada
-- com uma janela ESTRITAMENTE INTERIOR a ela (regra cruza os dois lados da
-- janela = GREATEST clipa o início E LEAST clipa o fim ao mesmo tempo).
-- ============================================================
WITH candidata AS (
    SELECT campaign_id,
           (start_date + ((end_date - start_date) / 4)) AS janela_from,
           (end_date   - ((end_date - start_date) / 4)) AS janela_to
    FROM distribution_rules
    WHERE end_date - start_date >= 8
    ORDER BY (end_date - start_date) DESC
    LIMIT 1
),
diag AS (
    SELECT COUNT(*) AS achou FROM candidata
),
v AS (
    SELECT dps.campaign_id, dps.type_id, dps.station_id, dps.for_date,
           dps.expected, dps.in_slot, dps.deficit, dps.bonus, dps.out_slot, dps.out_date
    FROM daily_play_summary dps, candidata c
    WHERE dps.for_date BETWEEN c.janela_from AND c.janela_to
      AND dps.campaign_id = c.campaign_id
),
f AS (
    SELECT fn.* FROM candidata c, LATERAL daily_play_summary_for(c.janela_from, c.janela_to, ARRAY[c.campaign_id]) fn
)
SELECT 'janela3_diagnostico_candidatos_achados' AS lado, achou AS COUNT FROM diag
UNION ALL
SELECT 'janela3_view_minus_fn', COUNT(*) FROM (SELECT * FROM v EXCEPT SELECT * FROM f) x
UNION ALL
SELECT 'janela3_fn_minus_view', COUNT(*) FROM (SELECT * FROM f EXCEPT SELECT * FROM v) y;

-- ============================================================
-- Janela 4: regra que TERMINA antes de hoje — testada com uma janela de 10
-- dias logo APÓS o fim da regra (regra inteiramente ANTES da janela =
-- GREATEST/LEAST deve produzir generate_series vazio, zero linhas de
-- 'expected' vindas dela).
-- ============================================================
WITH candidata AS (
    SELECT campaign_id, end_date,
           (end_date + 1) AS janela_from,
           (end_date + 10) AS janela_to
    FROM distribution_rules
    WHERE end_date < CURRENT_DATE + 300  -- evita pegar regra sem "depois" plausível
    ORDER BY end_date DESC
    LIMIT 1
),
diag AS (SELECT COUNT(*) AS achou FROM candidata),
v AS (
    SELECT dps.campaign_id, dps.type_id, dps.station_id, dps.for_date,
           dps.expected, dps.in_slot, dps.deficit, dps.bonus, dps.out_slot, dps.out_date
    FROM daily_play_summary dps, candidata c
    WHERE dps.for_date BETWEEN c.janela_from AND c.janela_to
      AND dps.campaign_id = c.campaign_id
),
f AS (
    SELECT fn.* FROM candidata c, LATERAL daily_play_summary_for(c.janela_from, c.janela_to, ARRAY[c.campaign_id]) fn
)
SELECT 'janela4_diagnostico_candidatos_achados' AS lado, achou AS COUNT FROM diag
UNION ALL
SELECT 'janela4_view_minus_fn', COUNT(*) FROM (SELECT * FROM v EXCEPT SELECT * FROM f) x
UNION ALL
SELECT 'janela4_fn_minus_view', COUNT(*) FROM (SELECT * FROM f EXCEPT SELECT * FROM v) y;

-- ============================================================
-- Janela 5: regra que COMEÇA no futuro — testada com uma janela de 10 dias
-- logo ANTES do início da regra (regra inteiramente DEPOIS da janela).
-- ============================================================
WITH candidata AS (
    SELECT campaign_id, start_date,
           (start_date - 10) AS janela_from,
           (start_date - 1)  AS janela_to
    FROM distribution_rules
    WHERE start_date > CURRENT_DATE - 300
    ORDER BY start_date ASC
    LIMIT 1
),
diag AS (SELECT COUNT(*) AS achou FROM candidata),
v AS (
    SELECT dps.campaign_id, dps.type_id, dps.station_id, dps.for_date,
           dps.expected, dps.in_slot, dps.deficit, dps.bonus, dps.out_slot, dps.out_date
    FROM daily_play_summary dps, candidata c
    WHERE dps.for_date BETWEEN c.janela_from AND c.janela_to
      AND dps.campaign_id = c.campaign_id
),
f AS (
    SELECT fn.* FROM candidata c, LATERAL daily_play_summary_for(c.janela_from, c.janela_to, ARRAY[c.campaign_id]) fn
)
SELECT 'janela5_diagnostico_candidatos_achados' AS lado, achou AS COUNT FROM diag
UNION ALL
SELECT 'janela5_view_minus_fn', COUNT(*) FROM (SELECT * FROM v EXCEPT SELECT * FROM f) x
UNION ALL
SELECT 'janela5_fn_minus_view', COUNT(*) FROM (SELECT * FROM f EXCEPT SELECT * FROM v) y;

-- ============================================================
-- Janela 6: override SEM regra correspondente (carve-out) — dia com override
-- mas sem regra que cubra campanha+tipo+estação+data. Testa que o FULL OUTER
-- JOIN da CTE expected_with_override introduz a linha corretamente e que o
-- filtro `ov.for_date BETWEEN p_from AND p_to` da função não a perde.
-- ============================================================
WITH candidata AS (
    SELECT ov.campaign_id, ov.for_date
    FROM distribution_overrides ov
    WHERE NOT EXISTS (
        SELECT 1 FROM distribution_rules r
        WHERE r.campaign_id = ov.campaign_id
          AND r.type_id = ov.type_id
          AND ov.station_id = ANY(r.station_ids)
          AND ov.for_date BETWEEN r.start_date AND r.end_date
    )
    LIMIT 1
),
diag AS (SELECT COUNT(*) AS achou FROM candidata),
v AS (
    SELECT dps.campaign_id, dps.type_id, dps.station_id, dps.for_date,
           dps.expected, dps.in_slot, dps.deficit, dps.bonus, dps.out_slot, dps.out_date
    FROM daily_play_summary dps, candidata c
    WHERE dps.for_date = c.for_date
      AND dps.campaign_id = c.campaign_id
),
f AS (
    SELECT fn.* FROM candidata c, LATERAL daily_play_summary_for(c.for_date, c.for_date, ARRAY[c.campaign_id]) fn
)
SELECT 'janela6_diagnostico_candidatos_achados' AS lado, achou AS COUNT FROM diag
UNION ALL
SELECT 'janela6_view_minus_fn', COUNT(*) FROM (SELECT * FROM v EXCEPT SELECT * FROM f) x
UNION ALL
SELECT 'janela6_fn_minus_view', COUNT(*) FROM (SELECT * FROM f EXCEPT SELECT * FROM v) y;

-- ============================================================
-- Janela 7: campanha cancelada — a função NÃO trata status de campanha
-- (igual à view); confirma que uma campanha 'cancelada' produz exatamente as
-- mesmas linhas nos dois lados, cobrindo toda a vida útil da campanha.
-- ============================================================
WITH candidata AS (
    SELECT id AS campaign_id, start_date, end_date
    FROM campaigns
    WHERE status = 'cancelada'
    ORDER BY created_at DESC
    LIMIT 1
),
diag AS (SELECT COUNT(*) AS achou FROM candidata),
v AS (
    SELECT dps.campaign_id, dps.type_id, dps.station_id, dps.for_date,
           dps.expected, dps.in_slot, dps.deficit, dps.bonus, dps.out_slot, dps.out_date
    FROM daily_play_summary dps, candidata c
    WHERE dps.for_date BETWEEN c.start_date - 5 AND c.end_date + 5
      AND dps.campaign_id = c.campaign_id
),
f AS (
    SELECT fn.* FROM candidata c,
        LATERAL daily_play_summary_for(c.start_date - 5, c.end_date + 5, ARRAY[c.campaign_id]) fn
)
SELECT 'janela7_diagnostico_candidatos_achados' AS lado, achou AS COUNT FROM diag
UNION ALL
SELECT 'janela7_view_minus_fn', COUNT(*) FROM (SELECT * FROM v EXCEPT SELECT * FROM f) x
UNION ALL
SELECT 'janela7_fn_minus_view', COUNT(*) FROM (SELECT * FROM f EXCEPT SELECT * FROM v) y;
