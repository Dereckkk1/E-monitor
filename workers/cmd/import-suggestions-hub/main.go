// import-suggestions-hub leva as sugestões do Postgres daqui para a fila do hub,
// uma vez (spec do hub 2026-10-26 §5). Lê suggestions + comentários + anexos
// (bytes do MinIO) e manda cada uma para POST /api/platform/sugestoes/importar.
//
// Idempotente: o hub reconhece a sugestão pelo id daqui e responde `ja-importada`.
// Para não subir bytes à toa numa segunda rodada, o CLI guarda um arquivo de
// estado local (id daqui → id do hub, fora do git) e PULA o que já foi. `--only`
// força uma sugestão mesmo marcada. Sem o arquivo (container recriado), a
// reimportação continua segura: o hub dá `ja-importada`, e os anexos subidos à
// toa somem em 24 h pelo `limpaUploadsOrfaos` de lá.
//
// Uma sugestão cujo corpo passe de 2 MB (só texto: os bytes vão pelo GCS) leva
// 413 `payload_too_large` do hub: entra como falha no relatório e o CLI segue.
//
// Roda DEPOIS de a tela já ler do hub (o deploy deste branch): antes disso, o
// que alguém criasse no meio ficaria órfão no Postgres.
//
// Usage (na VM, dentro do container da API, que já tem as envs):
//
//	docker compose exec api import-suggestions-hub --dry-run
//	docker compose exec api import-suggestions-hub
//	docker compose exec api import-suggestions-hub --only <uuid>
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

	"github.com/jackc/pgx/v5/pgxpool"

	"radiocheck/internal/catalog"
	"radiocheck/internal/config"
	"radiocheck/internal/hub"
	"radiocheck/internal/storage"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "só lista o que seria importado; não sobe nada")
	only := flag.String("only", "", "importa só esta sugestão (uuid), mesmo já marcada no estado")
	caminhoEstado := flag.String("estado", "/tmp/import-suggestions-hub.estado.json",
		"arquivo local id-daqui → id-do-hub; o que está nele é pulado")
	flag.Parse()

	est, err := carregaEstado(*caminhoEstado)
	if err != nil {
		log.Fatalf("estado: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	hc := hub.NewComTimeout(cfg.HubURL, cfg.HubPlatformKey, 60*time.Second)
	if !*dryRun && !hc.Configured() {
		log.Fatal("HUB_URL/HUB_PLATFORM_KEY não configurados")
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer pool.Close()
	store, err := storage.New(ctx, cfg.S3Endpoint, cfg.S3PublicEndpoint, cfg.S3Bucket,
		cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	lista, err := catalog.NewSuggestions(pool).ExportAll(ctx)
	if err != nil {
		log.Fatalf("leitura: %v", err)
	}

	var importadas, jaExistiam, seriam, puladas int
	var falhas []string
	for _, s := range lista {
		if *only != "" && s.ID.String() != *only {
			continue
		}
		if *only == "" && est.ja(s.ID.String()) {
			puladas++
			continue
		}
		if *dryRun {
			seriam++
			fmt.Printf("seria importada #%d %s (%d falas, %d anexos, status %s)\n",
				s.RefNum, s.ID, len(s.Comments), len(s.Attachments), s.Status)
			continue
		}
		r, err := importaUma(ctx, hc, store, s)
		if err != nil {
			// Inclui o 413 de corpo acima de 2 MB: registra e segue.
			falhas = append(falhas, fmt.Sprintf("#%d %s: %v", s.RefNum, s.ID, err))
			continue
		}
		if r.Acao == "importada" {
			importadas++
			fmt.Printf("importada #%d → #%d do hub\n", s.RefNum, r.Numero)
		} else {
			jaExistiam++
		}
		if err := est.marca(s.ID.String(), r.SugestaoID); err != nil {
			// A importação valeu; só a próxima rodada não vai saber pular esta.
			log.Printf("estado: não gravei %s: %v", s.ID, err)
		}
	}

	fmt.Printf("\nlidas: %d · importadas: %d · já existiam: %d · puladas (estado local): %d · falharam: %d",
		len(lista), importadas, jaExistiam, puladas, len(falhas))
	if *dryRun {
		fmt.Printf(" · seriam importadas: %d (dry-run)", seriam)
	}
	fmt.Println()
	for _, f := range falhas {
		fmt.Println("  FALHOU", f)
	}
	if len(falhas) > 0 {
		os.Exit(1)
	}
}
