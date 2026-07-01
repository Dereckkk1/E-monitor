// redisambiguate-twins reprocessa detecções históricas de gêmeos acústicos: pra
// cada tocada aprovada atribuída a um material que tem gêmeo (tabela
// material_twin_discriminative), baixa o clipe de evidência do S3, re-audita
// contra o próprio material e cada gêmeo, e re-decide pela cobertura da região
// discriminante — a MESMA lógica do pass-path vivo (mesmos guards: co-fire,
// projeção sync, ambiguous⟺retracted).
//
// SEGURANÇA (muta atribuição histórica real):
//   - dry-run é o DEFAULT. Só escreve com --apply.
//   - exige escopo (--campaign/--material/--station/--since/--until), a menos que
//     --all (e --all sem escopo exige --yes).
//   - reusa o backend do Plano 1 — NUNCA um UPDATE cru.
//
// Rode primeiro dry-run escopado na campanha suspeita, confira o preview linha a
// linha, e SÓ ENTÃO --apply.
//
//	redisambiguate-twins --campaign <uuid> --limit 50            # dry-run
//	redisambiguate-twins --campaign <uuid> --apply               # aplica no escopo
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"radiocheck/internal/audit"
	"radiocheck/internal/catalog"
	"radiocheck/internal/config"
	"radiocheck/internal/evidence"
	"radiocheck/internal/fingerprint"
	"radiocheck/internal/storage"
)

func main() {
	campaign := flag.String("campaign", "", "escopo: campaign_id")
	material := flag.String("material", "", "escopo: material/commercial_id")
	station := flag.String("station", "", "escopo: station_id")
	since := flag.String("since", "", "escopo: detected_at >= (RFC3339 ou YYYY-MM-DD)")
	until := flag.String("until", "", "escopo: detected_at <= (RFC3339 ou YYYY-MM-DD)")
	limit := flag.Int("limit", 200, "máximo de detecções (0 = sem limite)")
	apply := flag.Bool("apply", false, "APLICA as mudanças (default: dry-run, só mostra)")
	all := flag.Bool("all", false, "permite rodar SEM escopo (perigoso)")
	yes := flag.Bool("yes", false, "confirma --all sem escopo")
	flag.Parse()

	scoped := false
	parseUUID := func(s, name string) *uuid.UUID {
		if s == "" {
			return nil
		}
		id, err := uuid.Parse(s)
		if err != nil {
			log.Fatalf("--%s inválido: %v", name, err)
		}
		scoped = true
		return &id
	}
	parseTime := func(s, name string) *time.Time {
		if s == "" {
			return nil
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t, err = time.Parse("2006-01-02", s)
		}
		if err != nil {
			log.Fatalf("--%s inválido (use RFC3339 ou YYYY-MM-DD): %v", name, err)
		}
		scoped = true
		return &t
	}

	f := catalog.EligibleTwinFilter{
		CampaignID: parseUUID(*campaign, "campaign"),
		MaterialID: parseUUID(*material, "material"),
		StationID:  parseUUID(*station, "station"),
		Since:      parseTime(*since, "since"),
		Until:      parseTime(*until, "until"),
		Limit:      *limit,
	}
	if !scoped && !*all {
		log.Fatal("recusando rodar sem escopo — passe --campaign/--material/--station/--since/--until, ou --all (perigoso)")
	}
	if *all && !scoped && !*yes {
		log.Fatal("--all sem escopo processa TODAS as detecções elegíveis — confirme com --yes")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	logger, _ := zap.NewProduction()
	defer logger.Sync() //nolint:errcheck

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	s3, err := storage.New(ctx, cfg.S3Endpoint, cfg.S3PublicEndpoint, cfg.S3Bucket,
		cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		log.Fatalf("s3: %v", err)
	}

	auditor := audit.NewAuditor(pool, logger, 0, 0)
	detections := catalog.NewDetections(pool)
	detCampaigns := catalog.NewDetectionCampaigns(pool)
	twinRepo := catalog.NewTwinDiscriminative(pool)
	// nc=nil: o CLI não publica eventos; disambiguateTwin/co-fire não usam s.nc.
	// flags (disambigByCoverage/disambigTwin/multiAttribution) irrelevantes aqui —
	// chamamos RedisambiguateDetection/PreviewTwinDecision direto.
	svc := evidence.NewService(pool, s3, nil, detections, detCampaigns, auditor, false, false, false, logger)

	dets, err := twinRepo.ListEligibleDetections(ctx, f)
	if err != nil {
		log.Fatalf("list eligible: %v", err)
	}

	mode := "DRY-RUN"
	if *apply {
		mode = "APPLY"
	}
	fmt.Printf("redisambiguate-twins [%s]: %d detecções elegíveis\n\n", mode, len(dets))

	counts := map[string]int{}
	for i, d := range dets {
		if ctx.Err() != nil {
			log.Fatalf("interrompido: %v", ctx.Err())
		}
		pcm, err := downloadAndDecode(ctx, s3, d.EvidenceKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] SKIP %s: decode falhou: %v\n", i+1, len(dets), d.ID, err)
			counts["skip_decode"]++
			continue
		}
		if *apply {
			act := svc.RedisambiguateDetection(ctx, d.ID, d.DetectedAt, d.StationID, d.CommercialID, pcm)
			counts[actionKey(act)]++
			fmt.Printf("[%d/%d] %-22s det=%s station=%s\n", i+1, len(dets), act, d.ID, d.StationID)
		} else {
			p := svc.PreviewTwinDecision(ctx, d.CommercialID, pcm)
			counts[p.Action]++
			line := fmt.Sprintf("[%d/%d] %-11s det=%s", i+1, len(dets), p.Action, d.ID)
			if p.Action == "reattribute" {
				line += fmt.Sprintf(" -> sid=%d (cov=%.2f)", p.WinnerShortID, p.WinnerCov)
			}
			fmt.Println(line)
			if p.Detail != "" {
				fmt.Printf("            %s\n", p.Detail)
			}
		}
	}

	fmt.Printf("\ndone [%s]. ", mode)
	for k, v := range counts {
		fmt.Printf("%s=%d ", k, v)
	}
	fmt.Println()
	if !*apply {
		fmt.Println("(dry-run — nada foi escrito. Rode com --apply pra aplicar.)")
	}
}

// actionKey normaliza a ação (que pode ser "reattribute->sid=N") num bucket.
func actionKey(a string) string {
	if len(a) >= 11 && a[:11] == "reattribute" {
		return "reattribute"
	}
	return a
}

// downloadAndDecode baixa o clipe de evidência (m4a) do S3 pra um temp file e
// decodifica pro PCM pelo mesmo pipeline do fingerprint.
func downloadAndDecode(ctx context.Context, s3 *storage.Client, key string) ([]float32, error) {
	rc, _, _, err := s3.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	defer rc.Close()

	tmp, err := os.CreateTemp("", "redisambig-*.m4a")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, rc); err != nil {
		tmp.Close()
		return nil, err
	}
	tmp.Close()

	return fingerprint.DecodePCM(ctx, tmp.Name(), fingerprint.VariantClean)
}
