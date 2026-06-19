// backfill-unretract-displaced recovers airings lost to the §18.2.2 v1
// misattribution: a real SHORTER cut (e.g. the 15s) was retracted in favour of a
// LONGER cut (the 30s) by duration, and the longer cut then FAILED the §9.9 audit
// (audit_rejected) — so the genuine airing is counted nowhere. The shorter cut
// already passed its own audit (evidence_status='available'); this clears its
// retracted_at so it counts again. Same predicate as
// catalog.Detections.RestoreDisplacedShorterCut, applied in bulk over a window.
//
// Measured 2026-06-19: 104/110 such losses (14d) are recoverable this way. The
// other ~6 have the shorter cut also audit-rejected (degraded clip) and are left
// alone.
//
//	# DEFAULT IS DRY-RUN (reports only, touches nothing):
//	backfill-unretract-displaced --dsn "$DATABASE_URL" --since-days 45
//
//	# Apply (mutates rows) — ONLY after a dry-run on a CLONE of prod data (§4.8):
//	backfill-unretract-displaced --dsn "$DATABASE_URL" --since-days 45 --apply
//
// Per CLAUDE.md §4.8, never run --apply against prod without first dry-running
// the SAME command against a throwaway restore of the prod dump.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string")
	sinceDays := flag.Int("since-days", 30, "look back this many days for audit_rejected rows")
	windowSeconds := flag.Int("window-seconds", 60, "broadcast-window radius to match the displaced shorter cut")
	apply := flag.Bool("apply", false, "actually clear retracted_at (default: dry-run, reports only)")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("--dsn (or DATABASE_URL) is required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	since := time.Now().AddDate(0, 0, -*sinceDays)

	// candidateCTE resolves, for each audit_rejected longer cut, the single
	// closest shorter sibling cut of the same client/station that v1 retracted
	// within the window and that passed its own audit. Shared by the count, the
	// sample, and the UPDATE so they can never diverge.
	const candidateCTE = `
		WITH rej AS (
		    SELECT d.id AS rej_id, d.station_id, d.detected_at,
		           m.client_id, m.duration_seconds AS rej_dur
		    FROM detections d
		    JOIN materials m ON m.id = d.commercial_id
		    WHERE d.evidence_status = 'audit_rejected'
		      AND d.detected_at >= $1
		),
		restore AS (
		    SELECT DISTINCT ON (r.rej_id) t.id AS restore_id, tm.client_id
		    FROM rej r
		    JOIN detections t
		      ON t.station_id = r.station_id
		     AND t.detected_at BETWEEN r.detected_at - ($2 * interval '1 second')
		                           AND r.detected_at + ($2 * interval '1 second')
		     AND t.retracted_at IS NOT NULL
		     AND t.evidence_status = 'available'
		    JOIN materials tm
		      ON tm.id = t.commercial_id
		     AND tm.client_id = r.client_id
		     AND tm.duration_seconds < r.rej_dur
		    ORDER BY r.rej_id, abs(extract(epoch FROM t.detected_at - r.detected_at)),
		             t.audit_coverage DESC NULLS LAST
		)`

	// Per-client breakdown of distinct rows we would restore.
	rows, err := pool.Query(ctx, candidateCTE+`
		SELECT COALESCE(cl.name, '—') AS client, COUNT(DISTINCT s.restore_id) AS qtd
		FROM restore s
		LEFT JOIN clients cl ON cl.id = s.client_id
		GROUP BY cl.name
		ORDER BY qtd DESC`, since, *windowSeconds)
	if err != nil {
		log.Fatalf("count query: %v", err)
	}
	var total int
	fmt.Printf("\n=== backfill-unretract-displaced (since %s, window ±%ds) ===\n",
		since.Format("2006-01-02"), *windowSeconds)
	fmt.Printf("%-32s %s\n", "cliente", "veiculações recuperáveis")
	for rows.Next() {
		var client string
		var qtd int
		if err := rows.Scan(&client, &qtd); err != nil {
			log.Fatalf("scan: %v", err)
		}
		fmt.Printf("%-32s %d\n", client, qtd)
		total += qtd
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Fatalf("rows: %v", err)
	}
	fmt.Printf("%-32s %d\n", "TOTAL", total)

	if !*apply {
		fmt.Printf("\nDRY-RUN: nada foi alterado. Rode com --apply (após dry-run num CLONE, §4.8) para des-retratar.\n")
		return
	}

	// Apply: clear retracted_at on the distinct restore set. detected_at >= since
	// keeps the UPDATE pruned to recent partitions.
	tag, err := pool.Exec(ctx, candidateCTE+`
		UPDATE detections u
		SET retracted_at = NULL
		WHERE u.id IN (SELECT DISTINCT restore_id FROM restore)
		  AND u.detected_at >= $1`, since, *windowSeconds)
	if err != nil {
		log.Fatalf("apply update: %v", err)
	}
	fmt.Printf("\nAPLICADO: %d linhas des-retratadas.\n", tag.RowsAffected())
}
