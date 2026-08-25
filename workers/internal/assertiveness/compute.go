// Package assertiveness mede a assertividade da plataforma: de tudo que
// veiculou, quanto o matcher pegou sozinho vs quanto um operador teve que
// digitar na mão depois.
//
// LIMITE DA MÉTRICA, e é importante: ela só enxerga o miss que alguém
// REPORTOU. Se a emissora não mandou comprovante e ninguém digitou, o miss
// fica invisível e o número sai otimista. Isto é "assertividade contra o que
// foi reclamado" — que é o que o cliente sente —, não recall absoluto. Por
// isso a UI nunca deve mostrar o percentual sozinho: sem o volume de manuais
// ao lado, um time que parou de digitar é indistinguível de um matcher
// perfeito.
//
// A definição canônica dos baldes vive em scripts/sql/assertividade.sql, que é
// a ferramenta de verificação ad-hoc. Este arquivo é a fonte que alimenta a
// tabela; quando alguém desconfiar do card, roda o script e compara.
package assertiveness

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// Folga aplicada nas duas pontas da janela de down: tolera o intervalo
	// entre o stream cair de fato e o worker registrar o evento.
	graceSeconds = 120
	// Janela (minutos, para cada lado) que casa uma manual com uma detecção
	// automática do MESMO material na MESMA emissora. Casou = redigitação.
	// Aumentar demais passa a casar tocadas legítimas distintas do mesmo spot.
	dupMinutes = 5
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// Recompute apaga e regrava as linhas de [from, to] (inclusive, dias locais
// America/Sao_Paulo). É idempotente de propósito: manual de uma tocada do dia 3
// costuma ser digitada dia 20, então o job recomputa uma janela larga a cada
// execução em vez de gravar só o dia anterior — senão o número congela errado.
func (r *Repo) Recompute(ctx context.Context, from, to time.Time) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx,
		`DELETE FROM assertiveness_daily WHERE for_date >= $1::date AND for_date <= $2::date`,
		from, to); err != nil {
		return 0, fmt.Errorf("delete window: %w", err)
	}

	tag, err := tx.Exec(ctx, insertSQL, from, to, graceSeconds, dupMinutes)
	if err != nil {
		return 0, fmt.Errorf("insert window: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// insertSQL espelha scripts/sql/assertividade.sql. A precedência dos baldes
// importa: `duplicate` é avaliado ANTES de tudo (não é miss de espécie
// nenhuma), e `unmonitored` por último porque é o único ambíguo — "emissora
// fora do ar" e "worker morto calado" produzem o mesmo vazio no banco.
//
// A unidade é a linha de detection_campaigns (projeção canônica), não
// detections.campaign_id: é o que a grade, o /insights e os relatórios leem,
// então os números batem com as telas.
const insertSQL = `
WITH bounds AS (
    SELECT ($1::date + time '00:00')       AT TIME ZONE 'America/Sao_Paulo' AS ts_from,
           (($2::date + 1) + time '00:00') AT TIME ZONE 'America/Sao_Paulo' AS ts_to
),
down AS (
    -- Fim da janela, nesta ordem: duration_seconds quando existe; senão o
    -- próximo evento 'up' da emissora; senão agora. E nunca depois de agora.
    --
    -- O fallback em NOW() sozinho é armadilha: um down que ficou ABERTO (sem
    -- duration e sem 'up' subsequente) teria janela crescendo pra sempre e
    -- passaria a "perdoar" todos os meses seguintes. O erro cai justamente no
    -- lado perigoso — infla a assertividade. Medido: num restore de prod isso
    -- dobrava o balde stream_down de julho (36 -> 77).
    SELECT h.station_id,
           tstzrange(
             h.event_at - ($3 * INTERVAL '1 second'),
             LEAST(
               COALESCE(
                 h.event_at + (h.duration_seconds * INTERVAL '1 second'),
                 (SELECT MIN(u.event_at) FROM stream_health_events u
                   WHERE u.station_id = h.station_id
                     AND u.event_type = 'up'
                     AND u.event_at > h.event_at),
                 NOW()),
               NOW()
             ) + ($3 * INTERVAL '1 second'),
             '[]') AS win
    FROM stream_health_events h, bounds b
    WHERE h.event_type = 'down'
      -- alarga a leitura pra pegar down que começou antes da janela e atravessou
      AND h.event_at >= b.ts_from - INTERVAL '2 days'
      AND h.event_at <  b.ts_to
),
sig AS (
    -- "essa emissora deu algum sinal de vida neste dia?"
    SELECT station_id, day FROM (
        SELECT d.station_id, (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS day
        FROM detections d, bounds b
        WHERE d.detected_at >= b.ts_from AND d.detected_at < b.ts_to
          AND d.manual_at IS NULL
        UNION
        SELECT h.station_id, (h.event_at AT TIME ZONE 'America/Sao_Paulo')::date
        FROM stream_health_events h, bounds b
        WHERE h.event_at >= b.ts_from AND h.event_at < b.ts_to
    ) x GROUP BY 1, 2
),
classified AS (
    SELECT (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS for_date,
           dc.campaign_id,
           d.station_id,
           dc.commercial_id,
           (d.manual_at IS NULL) AS is_auto,
           CASE
             WHEN d.manual_at IS NULL THEN NULL
             WHEN EXISTS (
                    SELECT 1 FROM detections a
                     WHERE a.station_id    = d.station_id
                       AND a.commercial_id = dc.commercial_id
                       AND a.manual_at IS NULL
                       AND a.retracted_at IS NULL AND a.ignored_at IS NULL
                       AND a.evidence_status <> 'audit_rejected'
                       AND a.evidence_status <> 'ambiguous'
                       AND a.detected_at BETWEEN d.detected_at - ($4 * INTERVAL '1 minute')
                                             AND d.detected_at + ($4 * INTERVAL '1 minute')
                  ) THEN 'duplicate'
             WHEN COALESCE(m.fingerprint_generated_at, cm.fingerprint_generated_at) IS NULL
               OR COALESCE(m.fingerprint_generated_at, cm.fingerprint_generated_at) > d.detected_at
                  THEN 'no_fingerprint'
             WHEN EXISTS (SELECT 1 FROM down w
                           WHERE w.station_id = d.station_id AND w.win @> d.detected_at)
                  THEN 'stream_down'
             WHEN NOT EXISTS (SELECT 1 FROM sig s
                               WHERE s.station_id = d.station_id
                                 AND s.day = (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date)
                  THEN 'unmonitored'
             ELSE 'miss'
           END AS bucket
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    CROSS JOIN bounds b
    LEFT JOIN materials   m  ON m.id  = dc.commercial_id
    LEFT JOIN commercials cm ON cm.id = dc.commercial_id
    WHERE dc.detected_at >= b.ts_from AND dc.detected_at < b.ts_to
      AND d.detected_at  >= b.ts_from AND d.detected_at  < b.ts_to
      -- filtro canônico de veiculação aprovada (catalog.ApprovedDetectionsFilter)
      AND d.retracted_at IS NULL AND d.ignored_at IS NULL
      AND d.evidence_status <> 'audit_rejected' AND d.evidence_status <> 'ambiguous'
)
INSERT INTO assertiveness_daily (
    for_date, campaign_id, station_id, commercial_id,
    auto, miss, x_duplicate, x_no_fingerprint, x_stream_down, x_unmonitored)
SELECT for_date, campaign_id, station_id, commercial_id,
       COUNT(*) FILTER (WHERE is_auto)::int,
       COUNT(*) FILTER (WHERE bucket = 'miss')::int,
       COUNT(*) FILTER (WHERE bucket = 'duplicate')::int,
       COUNT(*) FILTER (WHERE bucket = 'no_fingerprint')::int,
       COUNT(*) FILTER (WHERE bucket = 'stream_down')::int,
       COUNT(*) FILTER (WHERE bucket = 'unmonitored')::int
FROM classified
GROUP BY 1, 2, 3, 4`
