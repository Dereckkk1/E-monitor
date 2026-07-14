// backfill-recategorize re-classifica detections/detection_campaigns de campanhas
// com carve-out (distribution_rules.material_ids não-vazio) usando a lógica atual
// do categorizador — necessário após a mudança do spec 2026-07-13 (dia extra
// dentro do período do material vira orphan/bônus em vez de out_date). Idempotente:
// RecategorizeForCampaign só altera linhas cuja categoria muda.
//
//	# DEFAULT DRY-RUN (só reporta a distribuição atual, não altera nada):
//	backfill-recategorize --dsn "$DATABASE_URL"
//
//	# Aplicar (muta linhas) — SÓ após rodar --apply contra um CLONE do dump de
//	# prod e conferir o delta (§4.8):
//	backfill-recategorize --dsn "$DATABASE_URL" --apply
//
// Escopa a uma campanha com --campaign <uuid>.
//
// --all amplia o alvo para TODA campanha com ao menos uma projeção em
// detection_campaigns (não só as com carve-out) — necessário uma vez, após o
// motor de recat passar a escopar por projeção em vez da tocada-base (spec
// 2026-07-14), para convergir o histórico de projeções fan-out (F-119) que os
// recats antigos nunca alcançavam.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/catalog"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string")
	campaign := flag.String("campaign", "", "recategorizar só esta campanha (uuid); vazio = todas com carve-out")
	apply := flag.Bool("apply", false, "aplicar a recategorização (default: dry-run, só reporta)")
	all := flag.Bool("all", false, "todas as campanhas com projeções (default: só campanhas com regra carve-out)")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("--dsn (ou DATABASE_URL) é obrigatório")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// Campanhas-alvo: as que têm ao menos uma regra carve-out (material_ids não-vazio).
	// São as únicas cuja categoria pode mudar com o spec 2026-07-13.
	//
	// --all: toda campanha com ao menos uma projeção — necessário 1× após o motor
	// de recat passar a escopar por projeção (spec 2026-07-14), pra convergir o
	// histórico de projeções fan-out que os recats antigos nunca alcançaram.
	targetQuery := `
		SELECT DISTINCT c.id, c.name
		FROM campaigns c
		JOIN distribution_rules r ON r.campaign_id = c.id
		WHERE cardinality(r.material_ids) > 0
		  AND ($1::uuid IS NULL OR c.id = $1)
		ORDER BY c.name`
	if *all {
		targetQuery = `
		SELECT c.id, c.name
		FROM campaigns c
		WHERE EXISTS (SELECT 1 FROM detection_campaigns dc WHERE dc.campaign_id = c.id)
		  AND ($1::uuid IS NULL OR c.id = $1)
		ORDER BY c.name`
	}
	var campaignFilter *uuid.UUID
	if *campaign != "" {
		id, err := uuid.Parse(*campaign)
		if err != nil {
			log.Fatalf("--campaign inválido: %v", err)
		}
		campaignFilter = &id
	}

	rows, err := pool.Query(ctx, targetQuery, campaignFilter)
	if err != nil {
		log.Fatalf("target query: %v", err)
	}
	type camp struct {
		id   uuid.UUID
		name string
	}
	var camps []camp
	var campIDs []uuid.UUID
	for rows.Next() {
		var c camp
		if err := rows.Scan(&c.id, &c.name); err != nil {
			log.Fatalf("scan: %v", err)
		}
		camps = append(camps, c)
		campIDs = append(campIDs, c.id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Fatalf("rows: %v", err)
	}

	// Distribuição de out_date/orphan das campanhas-alvo, medida na projeção
	// canônica (detection_campaigns.category) que a view daily_play_summary lê,
	// com o mesmo gate "aprovado" (retracted/ignored/audit_rejected fora). É o
	// número que o operador confere antes do --apply (§4.8).
	countCats := func() (outDate, orphan int64, err error) {
		err = pool.QueryRow(ctx, `
			SELECT
			  COUNT(*) FILTER (WHERE dc.category = 'out_date'),
			  COUNT(*) FILTER (WHERE dc.category = 'orphan')
			FROM detection_campaigns dc
			JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
			WHERE dc.campaign_id = ANY($1::uuid[])
			  AND d.retracted_at IS NULL
			  AND d.ignored_at IS NULL
			  AND d.evidence_status <> 'audit_rejected'`, campIDs).Scan(&outDate, &orphan)
		return
	}

	beforeOut, beforeOrphan, err := countCats()
	if err != nil {
		log.Fatalf("count (antes): %v", err)
	}
	fmt.Printf("\n=== backfill-recategorize (%d campanhas com carve-out) ===\n", len(camps))
	fmt.Printf("ANTES:  out_date=%d  orphan=%d\n", beforeOut, beforeOrphan)

	if !*apply {
		fmt.Printf("\nDRY-RUN: nada foi alterado. Rode com --apply (após --apply num CLONE, §4.8) para recategorizar.\n")
		return
	}

	dr := catalog.NewDistributionRules(pool)
	var ok, failed int
	for _, c := range camps {
		if err := dr.RecategorizeForCampaign(ctx, c.id); err != nil {
			log.Printf("FALHOU %s (%s): %v", c.name, c.id, err)
			failed++
			continue
		}
		ok++
	}

	afterOut, afterOrphan, err := countCats()
	if err != nil {
		log.Fatalf("count (depois): %v", err)
	}
	fmt.Printf("\nDEPOIS: out_date=%d  orphan=%d\n", afterOut, afterOrphan)
	fmt.Printf("DELTA:  out_date %+d  orphan %+d\n", afterOut-beforeOut, afterOrphan-beforeOrphan)
	fmt.Printf("APLICADO: %d campanhas ok, %d falharam.\n", ok, failed)
}
