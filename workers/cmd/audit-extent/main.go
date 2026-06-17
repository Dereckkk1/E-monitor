// audit-extent is a READ-ONLY diagnostic: decode an arbitrary audio clip (e.g. a
// vendor censura) and run the REAL §9.9 audit against a given short_id's stored
// master hashes, reporting score / coverage / EXTENT.
//
// Extent = how deep into the master the clip matched. A 15s airing matches only
// the first half of a 30s master (extent ~0.5) while a full 30s reaches ~1.0.
// With ground-truth-labelled censuras (you KNOW which cut aired) this proves
// whether the extent signal can disambiguate the 15s/30s version confusion
// (§18.2.2) — before we build the fix on it.
//
// Touches no rows (SELECT + decode only). Safe to run in prod.
//
// Usage:
//
//	audit-extent --dsn "$DATABASE_URL" --audio /path/censura.mp3 --short-id 78
//	# against both cuts at once:
//	for s in 77 78; do audit-extent --audio /path/censura.mp3 --short-id $s; done
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/audit"
	"radiocheck/internal/fingerprint"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string")
	audioPath := flag.String("audio", "", "path to the audio clip (censura) to audit")
	shortID := flag.Int("short-id", 0, "short_id of the master to audit against")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("--dsn or DATABASE_URL is required")
	}
	if *audioPath == "" || *shortID == 0 {
		log.Fatal("--audio and --short-id are required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// Resolve short_id → master UUID (materials first, then legacy commercials).
	var entityID uuid.UUID
	err = pool.QueryRow(ctx, `SELECT id FROM materials WHERE short_id = $1`, int32(*shortID)).Scan(&entityID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = pool.QueryRow(ctx, `SELECT id FROM commercials WHERE short_id = $1`, int32(*shortID)).Scan(&entityID)
	}
	if err != nil {
		log.Fatalf("resolve short_id %d: %v", *shortID, err)
	}

	pcm, err := fingerprint.DecodePCM(ctx, *audioPath, fingerprint.VariantClean)
	if err != nil {
		log.Fatalf("decode %q: %v", *audioPath, err)
	}

	auditor := audit.NewAuditor(pool, zap.NewNop(), 0, 0)
	res, err := auditor.AuditEvidence(ctx, entityID, pcm)
	if err != nil {
		log.Fatalf("audit: %v", err)
	}

	fmt.Printf("\n=== AUDIT-EXTENT  short_id=%d  audio=%s ===\n", *shortID, *audioPath)
	fmt.Printf("score         : %d   (min %d)\n", res.Score, audit.DefaultMinScore)
	fmt.Printf("coverage      : %.3f (min %.2f)\n", res.Coverage, audit.DefaultMinCoverage)
	fmt.Printf("MATCH EXTENT  : %.3f   <- how deep into the master the clip reached\n", res.MatchExtent)
	fmt.Printf("master hashes : %d    query hashes: %d\n", res.MasterHashes, res.QueryHashes)
	fmt.Printf("audit passed  : %v\n\n", res.Passed)
	fmt.Printf("READ: vs the 30s master (78), extent ~0.5 => this clip is a 15s airing;\n")
	fmt.Printf("      extent ~1.0 => the full 30s aired.\n\n")
}
