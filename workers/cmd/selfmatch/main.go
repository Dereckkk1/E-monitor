// selfmatch is a READ-ONLY diagnostic: it decodes a commercial/material's own
// master audio and runs it through the real Go matching engine against the
// stored fingerprint catalog, reporting the histogram score the entity gets
// against ITS OWN stored hashes per window.
//
// Purpose: distinguish "fingerprint is fine, just hasn't aired" from
// "fingerprint doesn't match live audio". If the Go query-hash generation
// (pkg/audio peaks.go) is in lockstep with the Python generator that produced
// the stored hashes, a master matched against its own stored fingerprint
// scores VERY high (tens to hundreds). If lockstep is broken (or the stored
// fingerprint is bad), the self-score collapses to the dense-catalog noise
// floor (~6-8) — the same number the live window scan shows.
//
// Touches no rows (SELECT + decode only). Safe to run in prod.
//
// Usage:
//
//	selfmatch --dsn "$DATABASE_URL" --short-id 102
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/fingerprint"
	"radiocheck/internal/index"
	"radiocheck/internal/match"
)

const (
	windowSeconds  = 4
	hopSeconds     = 1
	sampleRate     = fingerprint.SampleRate
	stftHopSamples = 2048
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string")
	shortID := flag.Int("short-id", 0, "short_id of the commercial/material to self-match")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("--dsn or DATABASE_URL is required")
	}
	if *shortID == 0 {
		log.Fatal("--short-id is required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// Resolve the target's master path. Try materials first, then commercials.
	var masterPath string
	var entityID uuid.UUID
	err = pool.QueryRow(ctx,
		`SELECT id, master_storage_path FROM materials WHERE short_id = $1`, int32(*shortID),
	).Scan(&entityID, &masterPath)
	if errors.Is(err, pgx.ErrNoRows) {
		err = pool.QueryRow(ctx,
			`SELECT id, master_storage_path FROM commercials WHERE short_id = $1`, int32(*shortID),
		).Scan(&entityID, &masterPath)
	}
	if err != nil {
		log.Fatalf("resolve short_id %d: %v", *shortID, err)
	}

	// Count the target's own stored hashes (the thing we're matching against).
	var ownHashes int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM fingerprint_hashes WHERE commercial_id = $1`, entityID,
	).Scan(&ownHashes); err != nil {
		log.Fatalf("count own hashes: %v", err)
	}

	// Load the full ready catalog index, mirroring the live matcher's index
	// exactly (UNION of commercials + net-new materials), so the noise floor we
	// print is the real one the worker sees.
	idx, totalHashes, err := loadCatalogIndex(ctx, pool)
	if err != nil {
		log.Fatalf("load catalog: %v", err)
	}
	store := index.New()
	store.Swap(idx)

	pcm, err := fingerprint.DecodePCM(ctx, masterPath, fingerprint.VariantClean)
	if err != nil {
		log.Fatalf("decode master %q: %v", masterPath, err)
	}

	windowSamples := sampleRate * windowSeconds
	hopSamples := sampleRate * hopSeconds
	if len(pcm) < windowSamples {
		log.Fatalf("master too short: %d samples < one %ds window", len(pcm), windowSeconds)
	}

	target := int32(*shortID)
	var nWindows int
	var maxSelf, maxOther int
	var maxOtherID int32
	selfScores := make([]int, 0, 512)
	for off := 0; off+windowSamples <= len(pcm); off += hopSamples {
		_, raw := match.MatchWindowDetailed(pcm[off:off+windowSamples], store, 1, 0.0)
		nWindows++
		self := raw[target]
		selfScores = append(selfScores, self)
		if self > maxSelf {
			maxSelf = self
		}
		for id, sc := range raw {
			if id != target && sc > maxOther {
				maxOther, maxOtherID = sc, id
			}
		}
	}

	sort.Sort(sort.Reverse(sort.IntSlice(selfScores)))
	p50, p90 := percentile(selfScores, 0.50), percentile(selfScores, 0.90)

	fmt.Printf("\n=== SELF-MATCH short_id=%d ===\n", *shortID)
	fmt.Printf("master            : %s\n", masterPath)
	fmt.Printf("own stored hashes : %d\n", ownHashes)
	fmt.Printf("catalog index hashes (all ready): %d\n", totalHashes)
	fmt.Printf("windows scanned   : %d (%ds win, %ds hop)\n", nWindows, windowSeconds, hopSeconds)
	fmt.Printf("SELF top-score    : %d\n", maxSelf)
	fmt.Printf("self p90 / p50    : %d / %d\n", p90, p50)
	fmt.Printf("best OTHER score  : %d (short_id=%d)  <- noise floor against this master\n", maxOther, maxOtherID)
	fmt.Println()
	switch {
	case maxSelf >= 40:
		fmt.Printf("VERDICT: fingerprint OK — lockstep holds. The master matches its own stored\n")
		fmt.Printf("         hashes strongly (%d). If it isn't detecting live, the cause is NOT the\n", maxSelf)
		fmt.Printf("         fingerprint (it just hasn't aired post-fix, or a live-path/station issue).\n")
	case maxSelf <= 12:
		fmt.Printf("VERDICT: fingerprint BROKEN — self-score (%d) is at the noise floor. The stored\n", maxSelf)
		fmt.Printf("         hashes do NOT match what peaks.go generates from this master (lockstep\n")
		fmt.Printf("         mismatch or bad master fingerprint). Re-fingerprint this entity.\n")
	default:
		fmt.Printf("VERDICT: INCONCLUSIVE (self top-score %d). Degraded but non-zero — investigate\n", maxSelf)
		fmt.Printf("         the master audio quality / variant generation for this entity.\n")
	}
}

func percentile(sortedDesc []int, p float64) int {
	if len(sortedDesc) == 0 {
		return 0
	}
	// sortedDesc is high→low; p90 = value below which 90% sit → index near the
	// low end. We want the p-th highest, so index = floor((1-p)*n).
	i := int(float64(len(sortedDesc)) * (1.0 - p))
	if i >= len(sortedDesc) {
		i = len(sortedDesc) - 1
	}
	return sortedDesc[i]
}

func loadCatalogIndex(ctx context.Context, pool *pgxpool.Pool) (index.Index, int, error) {
	rows, err := pool.Query(ctx, `
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id, c.short_id
		FROM fingerprint_hashes fh
		JOIN commercials c ON c.id = fh.commercial_id
		WHERE c.fingerprint_status = 'ready'
		UNION ALL
		SELECT fh.hash_value, fh.time_frame, fh.variant_id, fh.rate_id, m.short_id
		FROM fingerprint_hashes fh
		JOIN materials m ON m.id = fh.commercial_id
		WHERE m.fingerprint_status = 'ready'
		  AND m.id NOT IN (SELECT id FROM commercials)
	`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	idx := make(index.Index)
	var n int
	for rows.Next() {
		var hashValue uint32
		var timeFrame int32
		var variantID, rateID int16
		var shortID int32
		if err := rows.Scan(&hashValue, &timeFrame, &variantID, &rateID, &shortID); err != nil {
			return nil, 0, err
		}
		idx[hashValue] = append(idx[hashValue], index.Entry{
			CommercialShortID: shortID,
			VariantID:         uint8(variantID),
			RateID:            uint8(rateID),
			TimeFrame:         timeFrame,
		})
		n++
	}
	return idx, n, rows.Err()
}
