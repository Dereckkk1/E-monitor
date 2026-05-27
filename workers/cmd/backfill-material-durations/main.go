// backfill-material-durations re-probes the real audio duration of every
// material's stored master file and updates materials.duration_seconds when it
// differs from what's recorded. This repairs rows written while the upload
// handler stubbed duration at 30.0 (bug fixed in materials.go — see the
// "fix(materials): duração real via ffprobe no upload" commit). Materials
// migrated from commercials already carry correct durations; re-probing them
// is a no-op since the value matches and no UPDATE is issued.
//
// Idempotent: only issues an UPDATE when the probed value differs from the
// stored one by more than --tolerance seconds. Safe to run multiple times.
//
// Must run where ffprobe AND the master files are available — i.e. inside the
// api container, which mounts MASTERSDATA. Mirrors backfill-shared-hashes.
//
// Usage:
//
//	backfill-material-durations --dsn "$DATABASE_URL"
//	backfill-material-durations --dsn "$DATABASE_URL" --dry-run
//	backfill-material-durations --dsn "$DATABASE_URL" --skip-missing
//
// --dry-run     logs what WOULD change without writing.
// --skip-missing logs and continues when a master file is gone from disk
//                (without it, a missing file is a hard error).
// --tolerance   seconds of allowed drift before an UPDATE is issued (default 0.05).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string")
	skipMissing := flag.Bool("skip-missing", false, "log and continue when a master file is missing on disk")
	dryRun := flag.Bool("dry-run", false, "log what would change without writing")
	tolerance := flag.Float64("tolerance", 0.05, "seconds of drift before an UPDATE is issued")
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
		SELECT id, master_storage_path, title, duration_seconds
		FROM materials
		ORDER BY created_at ASC
	`)
	if err != nil {
		log.Fatalf("list materials: %v", err)
	}
	type job struct {
		id       uuid.UUID
		path     string
		title    string
		duration float64
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.path, &j.title, &j.duration); err != nil {
			log.Fatalf("scan: %v", err)
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Fatalf("iterate: %v", err)
	}
	mode := "WRITE"
	if *dryRun {
		mode = "DRY-RUN"
	}
	fmt.Printf("backfill-material-durations [%s]: %d materials to scan (tolerance %.3fs)\n", mode, len(jobs), *tolerance)

	var updated, unchanged, skipped, failed int
	for i, j := range jobs {
		if _, err := os.Stat(j.path); err != nil {
			if *skipMissing {
				fmt.Printf("[%d/%d] SKIP %s (%s) — master missing: %v\n", i+1, len(jobs), j.id, j.title, err)
				skipped++
				continue
			}
			log.Fatalf("master missing for %s (%s): %v — re-run with --skip-missing to continue", j.id, j.title, err)
		}
		real, err := probeDuration(j.path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] FAIL %s (%s): probe: %v\n", i+1, len(jobs), j.id, j.title, err)
			failed++
			continue
		}
		if math.Abs(real-j.duration) <= *tolerance {
			unchanged++
			continue
		}
		if *dryRun {
			fmt.Printf("[%d/%d] WOULD UPDATE %s (%s): %.3fs → %.3fs\n", i+1, len(jobs), j.id, j.title, j.duration, real)
			updated++
			continue
		}
		if _, err := pool.Exec(ctx,
			`UPDATE materials SET duration_seconds = $1 WHERE id = $2`, real, j.id,
		); err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] FAIL %s (%s): update: %v\n", i+1, len(jobs), j.id, j.title, err)
			failed++
			continue
		}
		fmt.Printf("[%d/%d] UPDATE %s (%s): %.3fs → %.3fs\n", i+1, len(jobs), j.id, j.title, j.duration, real)
		updated++
	}

	fmt.Printf("\ndone [%s]. updated=%d unchanged=%d skipped=%d failed=%d\n",
		mode, updated, unchanged, skipped, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

// probeDuration returns the container-format duration in seconds via ffprobe.
// Same invocation as handlers.probeDuration; inlined here to avoid importing
// the HTTP handlers package into a one-shot CLI.
func probeDuration(path string) (float64, error) {
	cmd := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(out))
	d, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("non-positive duration %.3f", d)
	}
	return d, nil
}
