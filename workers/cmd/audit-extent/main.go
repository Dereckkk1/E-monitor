// audit-extent is a READ-ONLY diagnostic: decode an arbitrary audio clip (e.g. a
// vendor censura) and run the REAL §9.9 audit match against a master, reporting
// score / coverage / EXTENT.
//
// Extent = how deep into the master the clip matched. A 15s airing matches only
// the first half of a 30s master (extent ~0.5) while a full 30s reaches ~1.0.
// With ground-truth-labelled censuras this proves whether the extent signal can
// disambiguate the 15s/30s version confusion (§18.2.2).
//
// Two modes:
//
//	# DB mode (on the VM — loads master hashes from the catalog by short_id):
//	audit-extent --audio /tmp/c.mp3 --short-id 78
//
//	# offline mode (local — fingerprints a master FILE, no DB):
//	audit-extent --audio censura.mp3 --master-file "ASAAS ... (1).mp3"
//
// Touches no rows (SELECT + decode only). Safe to run in prod.
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
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string (DB mode)")
	audioPath := flag.String("audio", "", "path to the audio clip (censura) to audit")
	shortID := flag.Int("short-id", 0, "short_id of the master to audit against (DB mode)")
	masterFile := flag.String("master-file", "", "local master audio file (offline mode, no DB)")
	flag.Parse()
	if *audioPath == "" {
		log.Fatal("--audio is required")
	}

	ctx := context.Background()

	censuraPCM, err := fingerprint.DecodePCM(ctx, *audioPath, fingerprint.VariantClean)
	if err != nil {
		log.Fatalf("decode censura %q: %v", *audioPath, err)
	}
	queryHashes := audit.PCMToHashes(censuraPCM)

	var res *audit.Result
	var label string

	if *masterFile != "" {
		// Offline mode: fingerprint the master file locally, no DB.
		masterPCM, err := fingerprint.DecodePCM(ctx, *masterFile, fingerprint.VariantClean)
		if err != nil {
			log.Fatalf("decode master %q: %v", *masterFile, err)
		}
		res = audit.MatchHashes(queryHashes, audit.PCMToHashes(masterPCM))
		label = *masterFile
	} else {
		// DB mode: load the master hashes for short_id from the catalog.
		if *dsn == "" || *shortID == 0 {
			log.Fatal("DB mode needs --dsn (or DATABASE_URL) and --short-id; or use --master-file for offline")
		}
		pool, err := pgxpool.New(ctx, *dsn)
		if err != nil {
			log.Fatalf("pool: %v", err)
		}
		defer pool.Close()
		var entityID uuid.UUID
		err = pool.QueryRow(ctx, `SELECT id FROM materials WHERE short_id = $1`, int32(*shortID)).Scan(&entityID)
		if errors.Is(err, pgx.ErrNoRows) {
			err = pool.QueryRow(ctx, `SELECT id FROM commercials WHERE short_id = $1`, int32(*shortID)).Scan(&entityID)
		}
		if err != nil {
			log.Fatalf("resolve short_id %d: %v", *shortID, err)
		}
		res, err = audit.NewAuditor(pool, zap.NewNop(), 0, 0).AuditEvidence(ctx, entityID, censuraPCM)
		if err != nil {
			log.Fatalf("audit: %v", err)
		}
		label = fmt.Sprintf("short_id=%d", *shortID)
	}

	fmt.Printf("\n=== AUDIT-EXTENT  master=%s  audio=%s ===\n", label, *audioPath)
	fmt.Printf("score        : %d\n", res.Score)
	fmt.Printf("coverage     : %.3f\n", res.Coverage)
	fmt.Printf("MATCH EXTENT : %.3f\n", res.MatchExtent)
	fmt.Printf("master hashes: %d   query hashes: %d\n", res.MasterHashes, res.QueryHashes)
}
