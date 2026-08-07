-- ============================================================================
-- ASSERTIVIDADE DA PLATAFORMA — diagnóstico read-only
--
-- Mede: de todas as veiculações que existem no sistema, quantas o matcher
-- pegou sozinho vs quantas um operador teve que digitar depois (= miss).
--
--   assertividade = auto / (auto + manual_culpa_nossa)
--
-- Uma manual NÃO é culpa nossa quando:
--   (a) duplicata_de_auto  — já existe detecção automática do mesmo material na
--                            mesma emissora em ±dup_minutes. Não é miss nenhum:
--                            é redigitação. Aconteceu em escala no período de
--                            disco cheio (evidência ficava 'failed', o operador
--                            lia "sem áudio" e reinseria a tocada).
--   (b) sem_fingerprint    — o material só foi fingerprintado DEPOIS da tocada
--                            (não existia no índice na hora; impossível detectar)
--   (c) stream_down        — a tocada cai dentro de uma janela de down event
--                            da emissora (stream fora do ar)
--   (d) sem_monitoramento  — não há NENHUM sinal daquela emissora naquele dia
--                            (nem detecção automática, nem health event)
--
-- UNIDADE DE CONTAGEM: uma linha de `detection_campaigns` (a projeção canônica),
-- que é o que a grade, o /insights e os relatórios leem. Por isso os números
-- daqui batem com o que você vê nas telas e com um SELECT ad-hoc por campanha.
-- Com o fan-out de multi-atribuição LIGADO, uma tocada compartilhada conta em
-- cada campanha que a reivindica — numerador e denominador seguem a mesma
-- regra, então a razão continua honesta.
--
-- ATENÇÃO na leitura do bucket (c): ele é ambíguo por construção. "Emissora não
-- estava sendo monitorada" e "worker morreu e ninguém viu" produzem exatamente
-- o mesmo vazio no banco. O segundo caso É culpa nossa. Por isso ele sai numa
-- linha separada — olhe o tamanho dele antes de aceitar a assertividade ajustada.
--
-- LIMITE DA MÉTRICA: só enxerga o miss que alguém reportou. Se a emissora não
-- mandou comprovante e o operador não digitou, o miss fica invisível e o número
-- sai otimista. Isto é "assertividade contra o que foi reclamado", não recall
-- absoluto (pra recall real: docs/operations/vendor-reconciliation.md).
--
-- LIMITE DA JANELA: drop_old_health_partitions (migration 0053) apaga partições
-- de stream_health_events com mais de 6 meses. Fora disso não existe down event
-- pra perdoar ninguém e a assertividade sai artificialmente baixa. O bloco [A]
-- mostra a cobertura real — confira antes de esticar as datas.
--
-- Só cria TEMP TABLEs (some ao fechar a sessão). Nenhum INSERT/UPDATE/DELETE
-- em tabela real.
-- ============================================================================

\set ON_ERROR_STOP on

-- ▼▼▼ AJUSTE AQUI ▼▼▼ (datas locais, America/Sao_Paulo; to_date é inclusivo)
-- Janela = mês passado + o anterior (junho e julho/2026). Meses fechados: agosto
-- em curso ficaria de fora do denominador de propósito — manual de tocada
-- recente ainda não foi digitada, e isso inflaria a assertividade.
\set from_date '2026-06-01'
\set to_date   '2026-07-31'
-- folga em segundos aplicada nas duas pontas da janela de down (tolera o
-- intervalo entre o stream cair de fato e o worker registrar o evento)
\set grace_seconds 120
-- janela (minutos, pra cada lado) usada pra casar uma veiculação manual com uma
-- detecção automática do MESMO material na MESMA emissora. Casou = o operador
-- redigitou algo que já tínhamos pego; não é miss. Aumentar demais passa a
-- casar tocadas legítimas distintas do mesmo spot no mesmo intervalo.
\set dup_minutes 5
-- ▲▲▲ AJUSTE AQUI ▲▲▲

-- Campanhas canceladas ficam INCLUÍDAS: a tocada aconteceu e o matcher tinha
-- obrigação de pegar — o status comercial não muda a qualidade do algoritmo.
-- Pra excluí-las, descomente as 2 linhas marcadas com "-- [cancel]" abaixo.

\timing off
\pset pager off

-- ── janela em timestamptz, ancorada no horário de Brasília ──────────────────
CREATE TEMP TABLE t_win AS
SELECT (:'from_date'::date + time '00:00') AT TIME ZONE 'America/Sao_Paulo' AS ts_from,
       (:'to_date'::date + 1 + time '00:00') AT TIME ZONE 'America/Sao_Paulo' AS ts_to;

-- ── janelas de stream fora do ar, com folga ─────────────────────────────────
CREATE TEMP TABLE t_down AS
SELECT h.station_id,
       tstzrange(
         h.event_at - (:grace_seconds * INTERVAL '1 second'),
         h.event_at + (COALESCE(h.duration_seconds, EXTRACT(EPOCH FROM (NOW() - h.event_at))::int)
                       * INTERVAL '1 second')
                    + (:grace_seconds * INTERVAL '1 second'),
         '[]'
       ) AS win
FROM stream_health_events h, t_win w
WHERE h.event_type = 'down'
  -- alarga a leitura pra pegar down que começou antes da janela e atravessou
  AND h.event_at >= w.ts_from - INTERVAL '2 days'
  AND h.event_at <  w.ts_to;
CREATE INDEX ON t_down (station_id);

-- ── sinal de "essa emissora estava viva neste dia" ──────────────────────────
CREATE TEMP TABLE t_sig AS
SELECT station_id, day FROM (
  SELECT d.station_id, (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS day
  FROM detections d, t_win w
  WHERE d.detected_at >= w.ts_from AND d.detected_at < w.ts_to
    AND d.manual_at IS NULL
  UNION
  SELECT h.station_id, (h.event_at AT TIME ZONE 'America/Sao_Paulo')::date
  FROM stream_health_events h, t_win w
  WHERE h.event_at >= w.ts_from AND h.event_at < w.ts_to
) x GROUP BY 1, 2;
CREATE INDEX ON t_sig (station_id, day);

-- ── veiculações manuais classificadas ───────────────────────────────────────
CREATE TEMP TABLE t_manual AS
SELECT d.id,
       d.station_id,
       dc.campaign_id,
       dc.commercial_id,
       d.detected_at,
       (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS day,
       to_char(d.detected_at AT TIME ZONE 'America/Sao_Paulo', 'YYYY-MM')  AS mes,
       COALESCE(m.title, cm.title, '(material removido)')                  AS material,
       CASE
         -- Primeiro de todos: a tocada JÁ tinha sido detectada automaticamente.
         -- Caso real (disco cheio, jul/26): a detecção acontecia mas a evidência
         -- ficava 'failed' por falta de espaço; o operador via "sem áudio",
         -- pedia o comprovante e redigitava. Não é miss — é dupla contagem, e
         -- infla o denominador exatamente nas campanhas mais monitoradas.
         WHEN EXISTS (
                SELECT 1 FROM detections a
                 WHERE a.station_id    = d.station_id
                   AND a.commercial_id = dc.commercial_id
                   AND a.manual_at IS NULL
                   AND a.retracted_at IS NULL AND a.ignored_at IS NULL
                   AND a.evidence_status <> 'audit_rejected'
                   AND a.evidence_status <> 'ambiguous'
                   AND a.detected_at BETWEEN d.detected_at - (:dup_minutes * INTERVAL '1 minute')
                                         AND d.detected_at + (:dup_minutes * INTERVAL '1 minute')
              )
           THEN 'duplicata_de_auto'
         WHEN COALESCE(m.fingerprint_generated_at, cm.fingerprint_generated_at) IS NULL
           OR COALESCE(m.fingerprint_generated_at, cm.fingerprint_generated_at) > d.detected_at
           THEN 'sem_fingerprint'
         WHEN EXISTS (SELECT 1 FROM t_down w
                       WHERE w.station_id = d.station_id AND w.win @> d.detected_at)
           THEN 'stream_down'
         WHEN NOT EXISTS (SELECT 1 FROM t_sig s
                           WHERE s.station_id = d.station_id
                             AND s.day = (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date)
           THEN 'sem_monitoramento'
         ELSE 'miss_nosso'
       END AS bucket
FROM detection_campaigns dc
JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
CROSS JOIN t_win w
LEFT JOIN materials   m  ON m.id  = dc.commercial_id
LEFT JOIN commercials cm ON cm.id = dc.commercial_id
JOIN campaigns c ON c.id = dc.campaign_id
WHERE dc.detected_at >= w.ts_from AND dc.detected_at < w.ts_to
  AND d.detected_at  >= w.ts_from AND d.detected_at  < w.ts_to
  AND d.manual_at IS NOT NULL
  -- filtro canônico de "veiculação aprovada" (catalog.ApprovedDetectionsFilter)
  AND d.retracted_at IS NULL AND d.ignored_at IS NULL
  AND d.evidence_status <> 'audit_rejected' AND d.evidence_status <> 'ambiguous'
  -- AND c.status <> 'cancelada'                                        -- [cancel]
;

-- ── veiculações automáticas (o acerto), já agregadas ────────────────────────
-- Agregado e não linha-a-linha de propósito: 6 meses de detecção automática são
-- ~1-2M linhas e nenhuma consulta abaixo precisa da linha, só da contagem.
--
-- ATENÇÃO ao consumir: a coluna de contagem é `n`. Quem quiser o total tem que
-- fazer SUM(n) — COUNT(*) aqui conta GRUPOS (emissora × campanha × mês), não
-- tocadas. Foi exatamente esse o bug da 1ª versão: campanha com 1 emissora
-- reportava "auto = 1" tendo 114 detecções.
CREATE TEMP TABLE t_auto AS
SELECT d.station_id,
       dc.campaign_id,
       to_char(d.detected_at AT TIME ZONE 'America/Sao_Paulo', 'YYYY-MM') AS mes,
       COUNT(*) AS n
FROM detection_campaigns dc
JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
CROSS JOIN t_win w
JOIN campaigns c ON c.id = dc.campaign_id
WHERE dc.detected_at >= w.ts_from AND dc.detected_at < w.ts_to
  AND d.detected_at  >= w.ts_from AND d.detected_at  < w.ts_to
  AND d.manual_at IS NULL
  AND d.retracted_at IS NULL AND d.ignored_at IS NULL
  AND d.evidence_status <> 'audit_rejected' AND d.evidence_status <> 'ambiguous'
  -- AND c.status <> 'cancelada'                                        -- [cancel]
GROUP BY 1, 2, 3;

ANALYZE t_down; ANALYZE t_sig; ANALYZE t_manual; ANALYZE t_auto;

\echo ''
\echo '===== [A] JANELA E COBERTURA DOS DADOS ====================================='
SELECT :'from_date' AS janela_de,
       :'to_date'   AS janela_ate,
       (SELECT MIN(event_at)::date FROM stream_health_events) AS health_mais_antigo,
       (SELECT MAX(event_at)::date FROM stream_health_events) AS health_mais_recente,
       (SELECT COUNT(*) FROM t_down)                  AS janelas_down_na_faixa,
       (SELECT COUNT(*) FROM t_manual)                AS manuais_na_faixa,
       (SELECT COALESCE(SUM(n),0) FROM t_auto)        AS automaticas_na_faixa;
\echo '(se health_mais_antigo > janela_de, a parte anterior da janela NAO tem como'
\echo ' perdoar stream caido — a assertividade daquele trecho sai subestimada)'

\echo ''
\echo '===== [B] NUMERO GLOBAL ===================================================='
WITH b AS (
  SELECT (SELECT COALESCE(SUM(n),0) FROM t_auto)                                AS auto,
         COUNT(*) FILTER (WHERE bucket = 'miss_nosso')                          AS miss_nosso,
         COUNT(*) FILTER (WHERE bucket = 'duplicata_de_auto')                   AS x_dup,
         COUNT(*) FILTER (WHERE bucket = 'stream_down')                         AS x_stream_down,
         COUNT(*) FILTER (WHERE bucket = 'sem_fingerprint')                     AS x_sem_fp,
         COUNT(*) FILTER (WHERE bucket = 'sem_monitoramento')                   AS x_sem_monit,
         COUNT(*)                                                               AS manual_total
  FROM t_manual
)
SELECT auto                                                                     AS "auto (acertos)",
       manual_total                                                             AS "manual total",
       x_dup                                                                    AS "  - ja detectada (redigitada)",
       x_stream_down                                                            AS "  - stream caido",
       x_sem_fp                                                                 AS "  - sem fingerprint",
       x_sem_monit                                                              AS "  - sem monitoramento (AMBIGUO)",
       miss_nosso                                                               AS "  = MISS NOSSO",
       ROUND(100.0 * auto / NULLIF(auto + miss_nosso, 0), 2)                    AS "assertividade AJUSTADA %",
       ROUND(100.0 * auto / NULLIF(auto + miss_nosso + x_sem_monit, 0), 2)      AS "  se ambiguo contar contra %",
       ROUND(100.0 * auto / NULLIF(auto + manual_total, 0), 2)                  AS "assertividade BRUTA %"
FROM b;

\echo ''
\echo '===== [C] POR MES =========================================================='
SELECT COALESCE(a.mes, m.mes)                                          AS mes,
       COALESCE(a.auto, 0)                                             AS auto,
       COALESCE(m.miss_nosso, 0)                                       AS miss_nosso,
       COALESCE(m.x_dup, 0)                                            AS x_redigitada,
       COALESCE(m.x_down, 0)                                           AS x_down,
       COALESCE(m.x_fp, 0)                                             AS x_sem_fp,
       COALESCE(m.x_mon, 0)                                            AS x_sem_monit,
       ROUND(100.0 * COALESCE(a.auto, 0)
             / NULLIF(COALESCE(a.auto, 0) + COALESCE(m.miss_nosso, 0), 0), 2)   AS "assert %"
FROM (SELECT mes, SUM(n) AS auto FROM t_auto GROUP BY 1) a
FULL JOIN (
  SELECT mes,
         COUNT(*) FILTER (WHERE bucket = 'miss_nosso')        AS miss_nosso,
         COUNT(*) FILTER (WHERE bucket = 'duplicata_de_auto') AS x_dup,
         COUNT(*) FILTER (WHERE bucket = 'stream_down')       AS x_down,
         COUNT(*) FILTER (WHERE bucket = 'sem_fingerprint')   AS x_fp,
         COUNT(*) FILTER (WHERE bucket = 'sem_monitoramento') AS x_mon
  FROM t_manual GROUP BY 1
) m ON m.mes = a.mes
ORDER BY 1;

\echo ''
\echo '===== [D] TOP 20 EMISSORAS POR MISS NOSSO =================================='
SELECT s.name                                                          AS emissora,
       COALESCE(s.city, '') || '/' || COALESCE(s.state, '')            AS praca,
       COALESCE(a.auto, 0)                                             AS auto,
       COALESCE(m.miss_nosso, 0)                                       AS miss_nosso,
       COALESCE(m.x_down, 0)                                           AS x_down,
       COALESCE(m.x_mon, 0)                                            AS x_sem_monit,
       ROUND(100.0 * COALESCE(a.auto, 0)
             / NULLIF(COALESCE(a.auto, 0) + COALESCE(m.miss_nosso, 0), 0), 2)   AS "assert %"
FROM (
  SELECT station_id,
         COUNT(*) FILTER (WHERE bucket = 'miss_nosso')        AS miss_nosso,
         COUNT(*) FILTER (WHERE bucket = 'stream_down')       AS x_down,
         COUNT(*) FILTER (WHERE bucket = 'sem_monitoramento') AS x_mon
  FROM t_manual GROUP BY 1
) m
LEFT JOIN (SELECT station_id, SUM(n) AS auto FROM t_auto GROUP BY 1) a
       ON a.station_id = m.station_id
JOIN stations s ON s.id = m.station_id
WHERE m.miss_nosso > 0
ORDER BY m.miss_nosso DESC, s.name
LIMIT 20;

\echo ''
\echo '===== [E] TOP 20 CAMPANHAS POR MISS NOSSO =================================='
SELECT c.name                                                          AS campanha,
       cl.name                                                         AS cliente,
       c.status,
       COALESCE(a.auto, 0)                                             AS auto,
       m.miss_nosso,
       ROUND(100.0 * COALESCE(a.auto, 0)
             / NULLIF(COALESCE(a.auto, 0) + m.miss_nosso, 0), 2)       AS "assert %"
FROM (
  SELECT campaign_id, COUNT(*) FILTER (WHERE bucket = 'miss_nosso') AS miss_nosso
  FROM t_manual GROUP BY 1
) m
LEFT JOIN (SELECT campaign_id, SUM(n) AS auto FROM t_auto GROUP BY 1) a
       ON a.campaign_id = m.campaign_id
JOIN campaigns c ON c.id = m.campaign_id
LEFT JOIN clients cl ON cl.id = c.client_id
WHERE m.miss_nosso > 0
ORDER BY m.miss_nosso DESC, c.name
LIMIT 20;

\echo ''
\echo '===== [F] AMOSTRA DE 25 MISSES (pra auditar se sao mesmo miss) ============='
SELECT to_char(t.detected_at AT TIME ZONE 'America/Sao_Paulo', 'YYYY-MM-DD HH24:MI') AS quando,
       s.name        AS emissora,
       t.material,
       c.name        AS campanha
FROM t_manual t
JOIN stations s  ON s.id = t.station_id
JOIN campaigns c ON c.id = t.campaign_id
WHERE t.bucket = 'miss_nosso'
ORDER BY t.detected_at DESC
LIMIT 25;

\echo ''
\echo '===== [H] AMOSTRA DE 15 REDIGITADAS (manual x auto no mesmo minuto) ========'
\echo '(confira: o horario da auto tem que estar colado no da manual. Se estiverem'
\echo ' longe, dup_minutes esta largo demais e engolindo tocada legitima.)'
SELECT to_char(t.detected_at AT TIME ZONE 'America/Sao_Paulo', 'YYYY-MM-DD HH24:MI') AS manual_em,
       (SELECT to_char(MIN(a.detected_at) AT TIME ZONE 'America/Sao_Paulo', 'HH24:MI:SS')
          FROM detections a
         WHERE a.station_id = t.station_id AND a.commercial_id = t.commercial_id
           AND a.manual_at IS NULL AND a.retracted_at IS NULL AND a.ignored_at IS NULL
           AND a.detected_at BETWEEN t.detected_at - (:dup_minutes * INTERVAL '1 minute')
                                 AND t.detected_at + (:dup_minutes * INTERVAL '1 minute')) AS auto_em,
       s.name AS emissora, t.material
FROM t_manual t
JOIN stations s ON s.id = t.station_id
WHERE t.bucket = 'duplicata_de_auto'
ORDER BY t.detected_at DESC
LIMIT 15;

\echo ''
\echo '===== [G] AMOSTRA DE 15 DO BUCKET AMBIGUO (sem_monitoramento) =============='
\echo '(confira 2 ou 3: a emissora estava mesmo fora do ar/sem campanha nesse dia,'
\echo ' ou o worker morreu calado? Se for worker morto, isso e miss nosso.)'
SELECT to_char(t.detected_at AT TIME ZONE 'America/Sao_Paulo', 'YYYY-MM-DD HH24:MI') AS quando,
       s.name        AS emissora,
       t.material,
       c.name        AS campanha
FROM t_manual t
JOIN stations s  ON s.id = t.station_id
JOIN campaigns c ON c.id = t.campaign_id
WHERE t.bucket = 'sem_monitoramento'
ORDER BY t.detected_at DESC
LIMIT 15;
