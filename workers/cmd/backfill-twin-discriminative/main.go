// backfill-twin-discriminative popula material_twin_discriminative pros materiais
// que já eram 'ready' antes da feature de desambiguação de gêmeos acústicos (spec
// 2026-07-01). Idempotente (Upsert dos dois lados por par de gêmeos). Seguro: só
// escreve a tabela nova, NÃO toca em detections. Rode uma vez após as migrations
// 0046/0047 landarem em prod, e antes de ligar DISAMBIG_TWIN_DISCRIMINATIVE.
//
// Novos uploads populam sozinhos via o callback afterScan do sharing.Subscriber;
// este comando é só pra o histórico.
//
// Usage:
//
//	backfill-twin-discriminative --dsn "$DATABASE_URL"
//	backfill-twin-discriminative --dsn "$DATABASE_URL" --client <uuid>
//
// PopulateForMaterial é best-effort: uma falha parcial (um gêmeo cujo master
// sumiu, por ex.) é logada e o material conta como "parcial", sem abortar o resto.
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

	"radiocheck/internal/catalog"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string")
	clientFilter := flag.String("client", "", "opcional: só materiais deste client_id")
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

	q := `SELECT id, title FROM materials WHERE fingerprint_status = 'ready'`
	args := []any{}
	if *clientFilter != "" {
		cid, perr := uuid.Parse(*clientFilter)
		if perr != nil {
			log.Fatalf("--client inválido: %v", perr)
		}
		q += ` AND client_id = $1`
		args = append(args, cid)
	}
	q += ` ORDER BY created_at ASC`

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		log.Fatalf("list materials: %v", err)
	}
	type job struct {
		id    uuid.UUID
		title string
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.title); err != nil {
			log.Fatalf("scan: %v", err)
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Fatalf("iterate: %v", err)
	}
	fmt.Printf("backfill-twin-disc: %d materiais a processar\n", len(jobs))

	repo := catalog.NewTwinDiscriminative(pool)
	var ok, failed int
	for i, j := range jobs {
		if ctx.Err() != nil {
			log.Fatalf("interrompido: %v", ctx.Err())
		}
		start := time.Now()
		if err := repo.PopulateForMaterial(ctx, j.id); err != nil {
			// best-effort: PopulateForMaterial já tolera falha parcial por gêmeo.
			fmt.Fprintf(os.Stderr, "[%d/%d] PARCIAL %s (%s): %v\n", i+1, len(jobs), j.id, j.title, err)
			failed++
			continue
		}
		fmt.Printf("[%d/%d] OK   %s (%s) in %s\n", i+1, len(jobs), j.id, j.title, time.Since(start).Round(time.Millisecond))
		ok++
	}

	var pairs int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM material_twin_discriminative`).Scan(&pairs); err != nil {
		log.Printf("count pairs: %v", err)
	}
	fmt.Printf("\ndone. ok=%d parcial=%d  total de pares na tabela: %d\n", ok, failed, pairs)
}
