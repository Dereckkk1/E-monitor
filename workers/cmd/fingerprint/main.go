// fingerprint is a one-shot CLI that turns a master audio file into the rows
// stored in fingerprint_hashes for a given commercial.
//
// Usage:
//
//	fingerprint --commercial-short-id 123 --input master.mp3
//	fingerprint --commercial-short-id 123 --input master.mp3 --variant clean --replace
//	fingerprint --input master.mp3 --dry-run     # no DB, just print stats
//
// Required env (unless --dry-run is set):
//
//	DATABASE_URL — postgres connection string (overridden by --db-url)
//
// The CLI runs decode (ffmpeg) → STFT → peak detection → hash generation
// (§7 of plano_implementacao.md) and then either prints stats (--dry-run) or
// COPYs the rows into fingerprint_hashes inside a single transaction that
// also flips the commercial's fingerprint_status to 'ready'.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/fingerprint"
)

func main() {
	var (
		shortID    int
		input      string
		variantArg string
		rateID     int
		dbURL      string
		replace    bool
		dryRun     bool
	)

	flag.IntVar(&shortID, "commercial-short-id", 0, "commercials.short_id of the target commercial (required unless --dry-run)")
	flag.StringVar(&input, "input", "", "path to the master audio file (required)")
	flag.StringVar(&variantArg, "variant", "clean", "broadcast variant: clean|light|medium|heavy (only 'clean' is implemented)")
	flag.IntVar(&rateID, "rate-id", 0, "multi-rate id (§9.7); leave 0 for the canonical 1.0× rate")
	flag.StringVar(&dbURL, "db-url", os.Getenv("DATABASE_URL"), "postgres connection string (defaults to $DATABASE_URL)")
	flag.BoolVar(&replace, "replace", false, "delete existing hashes for (commercial,variant,rate) before inserting")
	flag.BoolVar(&dryRun, "dry-run", false, "run the pipeline but do not touch the database")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "fingerprint — generate fingerprint hashes for a commercial master\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n  %s --commercial-short-id <id> --input <file> [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if input == "" {
		fmt.Fprintln(os.Stderr, "error: --input is required")
		flag.Usage()
		os.Exit(2)
	}
	if !dryRun && shortID <= 0 {
		fmt.Fprintln(os.Stderr, "error: --commercial-short-id is required (or use --dry-run)")
		flag.Usage()
		os.Exit(2)
	}
	if rateID < 0 || rateID > 255 {
		fmt.Fprintf(os.Stderr, "error: --rate-id must be in [0,255], got %d\n", rateID)
		os.Exit(2)
	}
	if !dryRun && dbURL == "" {
		fmt.Fprintln(os.Stderr, "error: --db-url not set and DATABASE_URL is empty (or use --dry-run)")
		flag.Usage()
		os.Exit(2)
	}

	variant, err := fingerprint.ParseVariant(variantArg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	fmt.Printf("[1/4] decoding master   : %s (variant=%s)\n", input, variant)
	result, err := fingerprint.GenerateForVariant(ctx, input, variant)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fingerprint failed:", err)
		os.Exit(1)
	}

	fmt.Printf("[2/4] decoded duration  : %.2fs\n", result.DurationSec)
	fmt.Printf("[3/4] STFT frames       : %d\n", result.Frames)
	fmt.Printf("       constellation peaks : %d\n", result.Peaks)
	fmt.Printf("       hashes generated   : %d (%.1f hash/s, %d unique, entropy ≈ %.2f bits)\n",
		len(result.Hashes), result.HashesPerSec, result.UniqueHashes, result.HashEntropyEst)
	fmt.Printf("       pipeline elapsed   : %s\n", result.Elapsed.Round(time.Millisecond))

	v := fingerprint.Validate(result)
	if len(v.Warnings) == 0 {
		fmt.Println("       validation         : OK")
	} else {
		fmt.Println("       validation warnings:")
		for _, w := range v.Warnings {
			fmt.Println("         -", w)
		}
	}

	if dryRun {
		fmt.Println("[4/4] dry-run            : skipping DB write")
		return
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db connect:", err)
		os.Exit(1)
	}
	defer pool.Close()

	commercial, err := fingerprint.LookupCommercialByShortID(ctx, pool, int32(shortID))
	if err != nil {
		fmt.Fprintln(os.Stderr, "lookup:", err)
		os.Exit(1)
	}
	fmt.Printf("[4/4] writing hashes    : commercial=%s (short_id=%d, title=%q)\n",
		commercial.ID, commercial.ShortID, commercial.Title)

	inserted, err := fingerprint.Persist(ctx, pool, result.Hashes, fingerprint.PersistOptions{
		CommercialID:    commercial.ID,
		Variant:         variant,
		RateID:          uint8(rateID),
		ReplaceExisting: replace,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "persist:", err)
		os.Exit(1)
	}
	fmt.Printf("       inserted rows      : %d (variant=%d, rate_id=%d, replace=%v)\n",
		inserted, variant, rateID, replace)
	fmt.Println("done.")
}
