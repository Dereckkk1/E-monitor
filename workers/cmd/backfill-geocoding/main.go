// backfill-geocoding preenche stations.latitude/longitude a partir de
// city+state (UF), usando o dataset IBGE embutido (pacote internal/geo).
//
// Por padrão processa só emissoras SEM coordenada (latitude IS NULL) que tenham
// city e state. Com --force, reprocessa TODAS as que tenham city+state,
// sobrescrevendo coordenadas existentes.
//
// Idempotente: rodar de novo sem --force só toca o que ainda está nulo.
// Não faz chamada de rede — o geocode é uma busca em memória.
//
// Usage:
//
//	backfill-geocoding --dsn "$DATABASE_URL"
//	backfill-geocoding --dsn "$DATABASE_URL" --dry-run
//	backfill-geocoding --dsn "$DATABASE_URL" --force
//
// --dry-run  loga o que MUDARIA sem escrever.
// --force    reprocessa todas as emissoras com city+state (sobrescreve).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/geo"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string")
	dryRun := flag.Bool("dry-run", false, "log what would change without writing")
	force := flag.Bool("force", false, "re-geocode all stations with city+state, overwriting existing coords")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("--dsn or DATABASE_URL is required")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	g, err := geo.New()
	if err != nil {
		log.Fatalf("geo: %v", err)
	}

	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	where := "latitude IS NULL AND city IS NOT NULL AND state IS NOT NULL"
	if *force {
		where = "city IS NOT NULL AND state IS NOT NULL"
	}
	rows, err := pool.Query(ctx, `
		SELECT id, name, city, state
		FROM stations
		WHERE `+where+`
		ORDER BY created_at ASC`)
	if err != nil {
		log.Fatalf("list stations: %v", err)
	}
	type job struct {
		id          uuid.UUID
		name        string
		city, state string
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.name, &j.city, &j.state); err != nil {
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
	scope := "null-only"
	if *force {
		scope = "force(all)"
	}
	fmt.Printf("backfill-geocoding [%s, %s]: %d stations to process\n", mode, scope, len(jobs))

	var ok, skipped, failed int
	missing := map[string]bool{}
	for i, j := range jobs {
		lat, lng, found := g.Lookup(j.city, j.state)
		if !found {
			fmt.Printf("[%d/%d] SKIP %s (%s) — cidade não encontrada: %q/%s\n", i+1, len(jobs), j.id, j.name, j.city, j.state)
			missing[j.city+"/"+j.state] = true
			skipped++
			continue
		}
		if *dryRun {
			fmt.Printf("[%d/%d] WOULD SET %s (%s): %q/%s → (%.6f, %.6f)\n", i+1, len(jobs), j.id, j.name, j.city, j.state, lat, lng)
			ok++
			continue
		}
		if _, err := pool.Exec(ctx,
			`UPDATE stations SET latitude=$1, longitude=$2, updated_at=NOW() WHERE id=$3`,
			lat, lng, j.id,
		); err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] FAIL %s (%s): update: %v\n", i+1, len(jobs), j.id, j.name, err)
			failed++
			continue
		}
		fmt.Printf("[%d/%d] SET %s (%s): %q/%s → (%.6f, %.6f)\n", i+1, len(jobs), j.id, j.name, j.city, j.state, lat, lng)
		ok++
	}

	fmt.Printf("\ndone [%s]. ok=%d pulados=%d falhas=%d\n", mode, ok, skipped, failed)
	if len(missing) > 0 {
		keys := make([]string, 0, len(missing))
		for k := range missing {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Printf("cidades não encontradas (corrigir no cadastro):\n")
		for _, k := range keys {
			fmt.Printf("  - %s\n", k)
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}
