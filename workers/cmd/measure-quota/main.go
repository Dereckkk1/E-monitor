// measure-quota é um harness DESCARTÁVEL de medição: roda o código real de
// /insights (catalog.Insights.Compute), /campaigns
// (catalog.Campaigns.FinancialsByCampaign) e do pós-venda
// (postsale.Repo.StationRows + Classify) campanha a campanha contra um CLONE do
// dump de prod, e imprime CSV.
//
// Não entra no workers.Dockerfile de propósito: não é ferramenta de produção.
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/catalog"
	"radiocheck/internal/postsale"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string")
	out := flag.String("out", "", "arquivo CSV de saída")
	todayS := flag.String("today", "2026-07-01", "hoje (America/Sao_Paulo) para months_elapsed")
	limit := flag.Int("limit", 0, "medir só as N primeiras campanhas (0 = todas)")
	flag.Parse()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	today, err := time.Parse("2006-01-02", *todayS)
	if err != nil {
		log.Fatalf("today: %v", err)
	}

	ins := catalog.NewInsights(pool)
	camps := catalog.NewCampaigns(pool)
	ps := postsale.NewRepo(pool)

	type camp struct {
		id       uuid.UUID
		clientID uuid.UUID
		name     string
		start    time.Time
		end      time.Time
		status   string
	}
	rows, err := pool.Query(ctx, `
		SELECT c.id, c.client_id, c.name, c.start_date, c.end_date, c.status
		FROM campaigns c ORDER BY c.id`)
	if err != nil {
		log.Fatalf("campaigns: %v", err)
	}
	var cs []camp
	for rows.Next() {
		var c camp
		if err := rows.Scan(&c.id, &c.clientID, &c.name, &c.start, &c.end, &c.status); err != nil {
			log.Fatalf("scan: %v", err)
		}
		cs = append(cs, c)
	}
	rows.Close()

	if *limit > 0 && *limit < len(cs) {
		cs = cs[:*limit]
	}

	f := os.Stdout
	if *out != "" {
		f, err = os.Create(*out)
		if err != nil {
			log.Fatalf("create: %v", err)
		}
		defer f.Close()
	}
	w := csv.NewWriter(f)
	defer w.Flush()
	_ = w.Write([]string{
		"campaign_id", "client_id", "status", "start", "end",
		"pricing_mode", "consolidated_flag",
		"ins_executado", "ins_bonificacao", "ins_bonif_count", "ins_contratado", "ins_cpm",
		"ins_impactos", "ins_veic_total",
		"bd_in_slot", "bd_out_slot", "bd_out_date", "bd_bonus",
		"camp_invested", "camp_insertions", "camp_audience",
		"ps_rows", "ps_above", "ps_compensation", "ps_conforming",
		"ps_sum_deficit", "ps_sum_extras", "ps_sum_identified", "ps_sum_programmed",
	})

	ff := func(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }

	started := time.Now()
	for i, c := range cs {
		if i > 0 && i%25 == 0 {
			fmt.Fprintf(os.Stderr, "... %d/%d campanhas em %s\n", i, len(cs), time.Since(started).Round(time.Second))
		}
		// modo de pricing da campanha (per_insertion / consolidated / mixed / none)
		var nCons, nPer int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FILTER (WHERE mode='consolidated'),
			       COUNT(*) FILTER (WHERE mode='per_insertion')
			FROM campaign_station_pricing WHERE campaign_id=$1`, c.id).Scan(&nCons, &nPer); err != nil {
			log.Fatalf("pricing mode %s: %v", c.id, err)
		}
		mode := "none"
		switch {
		case nCons > 0 && nPer > 0:
			mode = "mixed"
		case nCons > 0:
			mode = "consolidated"
		case nPer > 0:
			mode = "per_insertion"
		}

		// /insights com a janela = campanha INTEIRA (é a condição do
		// TestInsights_FinancialBase_MatchesCampaigns).
		// StationIDs TEM que ser slice vazio, nunca nil: nil vira NULL no
		// driver e o predicado `$4::uuid[] = '{}'` das queries de insights
		// avalia NULL, filtrando TODAS as linhas e zerando os KPIs em
		// silêncio. O handler de produção faz a mesma normalização
		// (api/handlers/insights.go: `if stations == nil { stations = []uuid.UUID{} }`).
		p := catalog.InsightsParams{
			ClientID:    c.clientID,
			CampaignIDs: []uuid.UUID{c.id},
			From:        c.start,
			To:          c.end,
			Today:       today,
			StationIDs:  []uuid.UUID{},
		}
		pay, err := ins.Compute(ctx, p)
		if err != nil {
			log.Fatalf("insights %s: %v", c.id, err)
		}

		// /campaigns para a MESMA campanha.
		fin, err := camps.FinancialsByCampaign(ctx, nil, []uuid.UUID{c.id}, today)
		if err != nil {
			log.Fatalf("financials %s: %v", c.id, err)
		}
		var cf catalog.CampaignFinancials
		if len(fin) > 0 {
			cf = fin[0]
		}

		// pós-venda no período da campanha.
		srows, err := ps.StationRows(ctx, c.id, c.start, c.end)
		if err != nil {
			log.Fatalf("postsale %s: %v", c.id, err)
		}
		var above, comp, conf, sumDef, sumExtras, sumIdent, sumProg int
		for _, r := range srows {
			switch r.Kind {
			case postsale.KindAbove:
				above++
			case postsale.KindCompensation:
				comp++
			case postsale.KindConforming:
				conf++
			}
			sumDef += r.Deficit
			sumExtras += r.Extras
			sumIdent += r.Identified
			sumProg += r.Programmed
		}

		_ = w.Write([]string{
			c.id.String(), c.clientID.String(), c.status,
			c.start.Format("2006-01-02"), c.end.Format("2006-01-02"),
			mode, fmt.Sprint(pay.Consolidated),
			ff(pay.KPIs.Investido.Executado), ff(pay.KPIs.Bonificacao.Valor),
			strconv.FormatInt(pay.KPIs.Bonificacao.Count, 10),
			ff(pay.KPIs.Investido.Contratado), ff(pay.KPIs.CPM),
			strconv.FormatInt(pay.KPIs.Impactos, 10),
			strconv.FormatInt(pay.KPIs.VeiculacoesTotal, 10),
			strconv.FormatInt(pay.VeiculacoesBreakdown.InSlot, 10),
			strconv.FormatInt(pay.VeiculacoesBreakdown.OutSlot, 10),
			strconv.FormatInt(pay.VeiculacoesBreakdown.OutDate, 10),
			strconv.FormatInt(pay.VeiculacoesBreakdown.ExtrasOrphan, 10),
			ff(cf.TotalInvested), strconv.Itoa(cf.TotalInsertions), ff(cf.TotalAudience),
			strconv.Itoa(len(srows)), strconv.Itoa(above), strconv.Itoa(comp), strconv.Itoa(conf),
			strconv.Itoa(sumDef), strconv.Itoa(sumExtras), strconv.Itoa(sumIdent), strconv.Itoa(sumProg),
		})
	}
	w.Flush()
	fmt.Fprintf(os.Stderr, "OK: %d campanhas medidas\n", len(cs))
}
