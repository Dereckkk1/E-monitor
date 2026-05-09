// backfill-shared-hashes runs the shared-hash detection over every existing
// ready commercial in the catalog, flagging fingerprint_hashes.is_shared on
// hashes whose audio overlaps with another commercial. Run this once after
// migration 0015_shared_hashes lands in production so historical commercials
// get the same treatment new uploads get automatically through the
// fingerprint CLI.
//
// Idempotent: re-running issues redundant UPDATEs but does not flip flags
// back to false; safe to run multiple times.
//
// Usage:
//
//	backfill-shared-hashes --dsn "$DATABASE_URL"
//	backfill-shared-hashes --dsn "$DATABASE_URL" --skip-missing
//
// The --skip-missing flag lets the binary continue when a commercial's
// master_storage_path no longer exists on disk (e.g. after a partial
// migration). Without it, missing files cause a hard error.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/sharing"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string")
	skipMissing := flag.Bool("skip-missing", false, "log and continue when a master file is missing on disk")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("--dsn or DATABASE_URL is required")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT id, master_storage_path, title
		FROM commercials
		WHERE fingerprint_status = 'ready'
		ORDER BY created_at ASC
	`)
	if err != nil {
		log.Fatalf("list commercials: %v", err)
	}
	type job struct {
		id    uuid.UUID
		path  string
		title string
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.path, &j.title); err != nil {
			log.Fatalf("scan: %v", err)
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Fatalf("iterate: %v", err)
	}
	fmt.Printf("backfill: %d commercials to scan\n", len(jobs))

	var ok, skipped, failed int
	for i, j := range jobs {
		if _, err := os.Stat(j.path); err != nil {
			if *skipMissing {
				fmt.Printf("[%d/%d] SKIP %s (%s) — master missing: %v\n", i+1, len(jobs), j.id, j.title, err)
				skipped++
				continue
			}
			log.Fatalf("master missing for %s (%s): %v — re-run with --skip-missing to continue", j.id, j.title, err)
		}
		start := time.Now()
		if err := sharing.MarkSharedHashes(ctx, pool, j.id, j.path); err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] FAIL %s (%s): %v\n", i+1, len(jobs), j.id, j.title, err)
			failed++
			continue
		}
		fmt.Printf("[%d/%d] OK   %s (%s) in %s\n", i+1, len(jobs), j.id, j.title, time.Since(start).Round(time.Millisecond))
		ok++
	}

	// Final tally + global count of flagged rows for an at-a-glance check.
	var flagged int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM fingerprint_hashes WHERE is_shared = true`,
	).Scan(&flagged); err != nil {
		log.Printf("count flagged rows: %v", err)
	}
	fmt.Printf("\ndone. ok=%d skipped=%d failed=%d  total flagged hashes in catalog: %d\n",
		ok, skipped, failed, flagged)
	if failed > 0 {
		os.Exit(1)
	}
}
