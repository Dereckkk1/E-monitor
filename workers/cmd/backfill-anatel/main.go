// backfill-anatel preenche a classe Anatel de cada emissora (stations.anatel_*)
// cruzando o cadastro com os Planos Básicos PBFM/PBOM embutidos no binário, e
// deriva daí os municípios dentro do contorno protegido
// (station_coverage_cities).
//
// O cruzamento é por dial + geografia — os planos não trazem o nome da
// emissora. Ver internal/anatel para os tiers (exact/geo/radcom) e
// docs/features/anatel-station-class-coverage.md para a leitura do resultado.
//
// Por padrão processa só emissoras SEM classe (anatel_class IS NULL).
// Com --force, reprocessa todas e LIMPA as colunas das que deixarem de casar,
// para que um plano atualizado não deixe classe velha para trás.
//
// Idempotente: rodar de novo sem --force só toca o que ainda está nulo.
// Não faz chamada de rede — planos e municípios são datasets embutidos.
//
// Usage:
//
//	backfill-anatel --dsn "$DATABASE_URL" --dry-run
//	backfill-anatel --dsn "$DATABASE_URL"
//	backfill-anatel --dsn "$DATABASE_URL" --force
//	backfill-anatel --dsn "$DATABASE_URL" --force --no-coverage
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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/anatel"
	"radiocheck/internal/geo"
)

type station struct {
	id       uuid.UUID
	name     string
	band     string
	city     string
	state    string
	freq     float64
	lat, lng float64
	hasCoord bool
}

type result struct {
	st     station
	match  anatel.Match
	cities []anatel.CoveredCity
	ok     bool
}

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "postgres connection string")
	dryRun := flag.Bool("dry-run", false, "mostra o que mudaria sem escrever")
	force := flag.Bool("force", false, "reprocessa todas as emissoras, limpando as que não casarem")
	noCoverage := flag.Bool("no-coverage", false, "só grava a classe; não recalcula station_coverage_cities")
	verbose := flag.Bool("verbose", false, "loga uma linha por emissora")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("--dsn ou DATABASE_URL é obrigatório")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	idx, err := anatel.New()
	if err != nil {
		log.Fatalf("anatel: %v", err)
	}
	g, err := geo.New()
	if err != nil {
		log.Fatalf("geo: %v", err)
	}
	fmt.Printf("planos Anatel: %d canais; municípios IBGE: %d\n", idx.Len(), len(g.All()))

	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	where := "anatel_class IS NULL"
	if *force {
		where = "TRUE"
	}
	rows, err := pool.Query(ctx, `
		SELECT id, name, band, COALESCE(city,''), COALESCE(state,''),
		       COALESCE(frequency_mhz, 0), latitude, longitude
		FROM stations
		WHERE `+where+`
		ORDER BY short_id ASC`)
	if err != nil {
		log.Fatalf("listar emissoras: %v", err)
	}
	var stations []station
	for rows.Next() {
		var s station
		var lat, lng *float64
		if err := rows.Scan(&s.id, &s.name, &s.band, &s.city, &s.state, &s.freq, &lat, &lng); err != nil {
			log.Fatalf("scan: %v", err)
		}
		if lat != nil && lng != nil {
			s.lat, s.lng, s.hasCoord = *lat, *lng, true
		}
		stations = append(stations, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Fatalf("iterar: %v", err)
	}

	mode, scope := "WRITE", "sem-classe"
	if *dryRun {
		mode = "DRY-RUN"
	}
	if *force {
		scope = "force(todas)"
	}
	fmt.Printf("backfill-anatel [%s, %s]: %d emissoras\n\n", mode, scope, len(stations))

	// ---- fase 1: cruzamento (puro, em memória) ----
	results := make([]result, 0, len(stations))
	byTier := map[string]int{}
	var unmatched, ambiguous, noRadius, coverageRows, homeOutsideRadius, transbordoRows int

	for _, s := range stations {
		in := anatel.Station{
			Name: s.name, Band: s.band, City: s.city, State: s.state,
			FreqMHz: s.freq, Lat: s.lat, Lng: s.lng, HasCoord: s.hasCoord,
		}
		m, ok := idx.Match(in)
		r := result{st: s, match: m, ok: ok}
		if !ok {
			unmatched++
			results = append(results, r)
			if *verbose {
				fmt.Printf("  MISS  %-38.38s %s %.1f %s/%s\n", s.name, s.band, s.freq, s.city, s.state)
			}
			continue
		}
		byTier[m.Tier]++
		if m.Ambiguous {
			ambiguous++
		}
		// A cobertura é ancorada na ANTENA quando o plano a fornece; senão no
		// centroide do município (único ponto disponível para RadCom).
		lat, lng, hasPoint := m.Lat, m.Lng, m.HasCoord
		if !hasPoint {
			lat, lng, hasPoint = s.lat, s.lng, s.hasCoord
		}
		switch {
		case m.CoverageKm <= 0:
			noRadius++ // AM: contorno protegido em mV/m, não em km
		case !hasPoint:
			noRadius++ // sem coordenada não dá pra desenhar raio nenhum
		case !*noCoverage:
			// Raio de ALCANCE (contorno protegido + transbordo), não o
			// contorno puro: a cidade vizinha que ouve a emissora sem estar
			// na área protegida conta como praça atendida.
			r.cities = anatel.CoverageCities(g, lat, lng, m.ReachKm)
			// O município de licença (e o do cadastro, quando diferente) entra
			// sempre: é premissa da outorga, não inferência geométrica.
			r.cities = anatel.WithHomeCities(g, r.cities, lat, lng,
				anatel.CityRef{City: m.PlanCity, UF: m.PlanUF},
				anatel.CityRef{City: s.city, UF: s.state},
			)
			coverageRows += len(r.cities)
			for _, c := range r.cities {
				if c.IsHome && c.DistanceKm > m.ReachKm {
					homeOutsideRadius++
				}
				if c.DistanceKm > m.CoverageKm {
					transbordoRows++
				}
			}
		}
		if *verbose {
			fmt.Printf("  %-6s %-38.38s %s %.1f %s/%s → classe %-6s %4.1fkm  %d cidades\n",
				m.Tier, s.name, s.band, s.freq, s.city, s.state, m.Class, m.CoverageKm, len(r.cities))
		}
		results = append(results, r)
	}

	// ---- relatório ----
	fmt.Printf("cruzamento: %d casaram, %d sem match\n", len(results)-unmatched, unmatched)
	fmt.Printf("  por tier:")
	for _, t := range []string{anatel.TierExact, anatel.TierGeo, anatel.TierRadCom} {
		fmt.Printf("  %s=%d", t, byTier[t])
	}
	fmt.Printf("\n  ambíguos (desempate por Principal/ERP): %d\n", ambiguous)
	fmt.Printf("  sem raio de cobertura (AM ou sem coordenada): %d\n", noRadius)
	fmt.Printf("  linhas de cobertura a gravar: %d (%d no contorno, %d no transbordo)\n",
		coverageRows, coverageRows-transbordoRows, transbordoRows)
	fmt.Printf("  sedes incluídas apesar de o centroide cair fora do alcance: %d\n", homeOutsideRadius)
	printClassHistogram(results)

	if *dryRun {
		fmt.Printf("\nDRY-RUN: nada escrito.\n")
		return
	}

	// ---- fase 2: escrita ----
	start := time.Now()
	updated, cleared, err := writeMatches(ctx, pool, results, *force)
	if err != nil {
		log.Fatalf("gravar classes: %v", err)
	}
	fmt.Printf("\nclasses gravadas: %d (limpas: %d)\n", updated, cleared)

	if !*noCoverage {
		n, err := writeCoverage(ctx, pool, results)
		if err != nil {
			log.Fatalf("gravar cobertura: %v", err)
		}
		fmt.Printf("cobertura gravada: %d linhas\n", n)
	}
	fmt.Printf("concluído em %s\n", time.Since(start).Round(time.Millisecond))
}

// writeMatches grava as colunas anatel_* das emissoras que casaram e, em modo
// --force, limpa as que deixaram de casar (senão sobraria classe obsoleta de
// uma execução anterior sobre um plano velho).
func writeMatches(ctx context.Context, pool *pgxpool.Pool, results []result, force bool) (updated, cleared int, err error) {
	batch := &pgx.Batch{}
	// isClear[i] diz se o i-ésimo item do batch é uma limpeza ou uma escrita,
	// para atribuir o RowsAffected de cada um ao contador certo. Os resultados
	// voltam na ordem em que foram enfileirados.
	var isClear []bool
	for _, r := range results {
		if !r.ok {
			if force {
				batch.Queue(`
					UPDATE stations SET
					  anatel_class=NULL, anatel_coverage_km=NULL, anatel_reach_km=NULL,
					  anatel_erp_kw=NULL,
					  anatel_latitude=NULL, anatel_longitude=NULL, anatel_match_tier=NULL,
					  anatel_match_distance_km=NULL, anatel_plan_city=NULL,
					  anatel_plan_state=NULL, anatel_matched_at=NULL, updated_at=NOW()
					WHERE id=$1 AND anatel_class IS NOT NULL`, r.st.id)
				isClear = append(isClear, true)
			}
			continue
		}
		m := r.match
		batch.Queue(`
			UPDATE stations SET
			  anatel_class=$2,
			  anatel_coverage_km=$3,
			  anatel_reach_km=$4,
			  anatel_erp_kw=$5,
			  anatel_latitude=$6,
			  anatel_longitude=$7,
			  anatel_match_tier=$8,
			  anatel_match_distance_km=$9,
			  anatel_plan_city=$10,
			  anatel_plan_state=$11,
			  anatel_matched_at=NOW(),
			  updated_at=NOW()
			WHERE id=$1`,
			r.st.id,
			m.Class,
			nullFloat(m.CoverageKm), // NULL = cobertura indeterminada (AM)
			nullFloat(m.ReachKm),    // contorno + transbordo
			nullFloat(m.ERPkW),
			nullCoord(m.Lat, m.HasCoord),
			nullCoord(m.Lng, m.HasCoord),
			m.Tier,
			nullDistance(m.DistanceKm, m.HasDistance),
			nullString(m.PlanCity),
			nullString(m.PlanUF),
		)
		isClear = append(isClear, false)
	}
	if batch.Len() == 0 {
		return 0, 0, nil
	}
	// Ambos os contadores vêm de RowsAffected, não do que foi enfileirado: a
	// maioria das emissoras sem match JÁ estava com anatel_class NULL, e a
	// limpeza delas não muda linha nenhuma. Contar o enfileirado reportaria
	// "limpas: 1488" quando na prática nada foi limpo.
	br := pool.SendBatch(ctx, batch)
	for i, clear := range isClear {
		tag, e := br.Exec()
		if e != nil {
			br.Close()
			return 0, 0, fmt.Errorf("batch item %d: %w", i, e)
		}
		if clear {
			cleared += int(tag.RowsAffected())
		} else {
			updated += int(tag.RowsAffected())
		}
	}
	return updated, cleared, br.Close()
}

// writeCoverage reescreve station_coverage_cities das emissoras processadas.
// Delete-then-copy numa transação só: se falhar no meio, nenhuma emissora fica
// com cobertura parcial (que seria pior que cobertura nenhuma — um município
// faltando é invisível, enquanto zero municípios é obviamente "não calculado").
func writeCoverage(ctx context.Context, pool *pgxpool.Pool, results []result) (int, error) {
	ids := make([]uuid.UUID, 0, len(results))
	for _, r := range results {
		if r.ok {
			ids = append(ids, r.st.id)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op após Commit

	if _, err := tx.Exec(ctx,
		`DELETE FROM station_coverage_cities WHERE station_id = ANY($1)`, ids); err != nil {
		return 0, fmt.Errorf("limpar cobertura anterior: %w", err)
	}

	var rows [][]any
	for _, r := range results {
		for _, c := range r.cities {
			rows = append(rows, []any{r.st.id, c.IBGECode, c.Name, c.UF, round1(c.DistanceKm), c.IsHome, c.Lat, c.Lng})
		}
	}
	if len(rows) > 0 {
		n, err := tx.CopyFrom(ctx,
			pgx.Identifier{"station_coverage_cities"},
			[]string{"station_id", "ibge_code", "city", "state", "distance_km", "is_home", "latitude", "longitude"},
			pgx.CopyFromRows(rows))
		if err != nil {
			return 0, fmt.Errorf("copy cobertura: %w", err)
		}
		if int(n) != len(rows) {
			return 0, fmt.Errorf("copy gravou %d de %d linhas", n, len(rows))
		}
	}
	return len(rows), tx.Commit(ctx)
}

func printClassHistogram(results []result) {
	// Chaveia por banda: a classe "C" do FM (7,5 km) e a classe "C" do AM são
	// coisas distintas, e somá-las num bucket só engana quem lê o relatório.
	hist := map[string]int{}
	for _, r := range results {
		if !r.ok {
			continue
		}
		if r.match.Class == anatel.ClassRadCom {
			hist[r.match.Class]++
			continue
		}
		hist[r.st.band+" "+r.match.Class]++
	}
	keys := make([]string, 0, len(hist))
	for k := range hist {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool { return hist[keys[a]] > hist[keys[b]] })
	fmt.Printf("  classes:")
	for _, k := range keys {
		fmt.Printf("  %s=%d", k, hist[k])
	}
	fmt.Println()
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }

func nullFloat(f float64) any {
	if f <= 0 {
		return nil
	}
	return f
}

func nullDistance(f float64, has bool) any {
	if !has {
		return nil
	}
	return round1(f)
}

func nullCoord(f float64, has bool) any {
	if !has {
		return nil
	}
	return f
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
