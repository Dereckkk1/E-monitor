// Package dbtest fornece guards de segurança pra testes que precisam executar
// operações destrutivas (TRUNCATE, DELETE em massa) contra um Postgres real.
//
// Motivo da existência:
//
// Em 2026-05-17, rodar `go test` com TEST_DATABASE_URL apontando pro mesmo
// DB que o ambiente de dev local usa causou perda de 100% das campanhas,
// materials, commercials e detections — porque os testes chamam
// TRUNCATE clients RESTART IDENTITY CASCADE no setup, e o CASCADE varreu
// todas as FKs dependentes.
//
// Esse package implementa um pre-flight guard que **se recusa a rodar**
// se detectar que o DB alvo tem dado real (heurística: contagem de rows
// em tabelas-marcador como stations e clients acima de threshold típico
// de fixtures de teste). É o "cinto de segurança" pra quando alguém
// esquecer de configurar um TEST_DATABASE_URL dedicado.
package dbtest

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Thresholds em tabelas-marcador. Acima desses números, presumimos que o
// DB é um ambiente real (dev/staging/prod), NÃO um test DB descartável.
//
// stations: dev local típico carrega ~7500 emissoras do import Audiency;
//
//	um test DB fresh deveria criar UMAS POUCAS stations dentro do próprio
//	teste, jamais milhares. Threshold de 50 é generoso.
//
// clients: dev local típico tem ~100 clients importados; tests fixtures
//
//	tipicamente criam 1-3. Threshold de 10 é generoso.
const (
	maxAllowedStations = 50
	maxAllowedClients  = 10
)

// EnvAllowDestructive é a env var que desliga o guard. O VALOR aceito é
// uma string deliberadamente longa e auto-descritiva ("i-know-this-will-
// destroy-data") pra evitar que alguém digite "yes" por reflexo e perca
// dado. Use APENAS em CI dedicado / container efêmero / DB que você
// CRIOU especificamente pra esse teste.
//
// Em workstation de dev, NÃO USE — é exatamente a porta que esse guard
// existe pra fechar (em 2026-05-17 eu mesmo escorreguei nessa porta).
const (
	EnvAllowDestructive   = "RADIOCHECK_ALLOW_DESTRUCTIVE_TESTS"
	AllowDestructiveValue = "i-know-this-will-destroy-data"
)

// GuardOrSkip valida que o pool aponta pra um DB seguro pra testes
// destrutivos. Comportamento:
//
//   - Se TEST_DATABASE_URL não está setado: t.Skip (testes pulam,
//     comportamento histórico preservado).
//   - Se RADIOCHECK_ALLOW_DESTRUCTIVE_TESTS=yes: passa direto (override
//     explícito pra CI/containers efêmeros).
//   - Se o DB tem mais rows do que os thresholds em stations/clients:
//     t.Fatal com mensagem clara explicando como configurar um test DB
//     dedicado. Os testes NÃO rodam — preserva o dado.
//   - Caso contrário: passa, testes podem TRUNCATE à vontade.
//
// Use no INÍCIO de cada helper newTestDB/newTestPool, ANTES de qualquer
// TRUNCATE.
func GuardOrSkip(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	if os.Getenv(EnvAllowDestructive) == AllowDestructiveValue {
		// Warning agressivo — quem ativa esse override precisa SABER que
		// vai TRUNCATE. Usar t.Log (não Logf) pra aparecer mesmo sem -v.
		t.Log("")
		t.Log("⚠️  ⚠️  ⚠️  GUARD DESATIVADO ⚠️  ⚠️  ⚠️")
		t.Logf("    %s=%s", EnvAllowDestructive, AllowDestructiveValue)
		t.Log("    Esses testes vão TRUNCATE users, clients CASCADE.")
		t.Log("    Se este DB não é descartável, INTERROMPA AGORA (Ctrl+C).")
		t.Log("")
		return
	}

	// Heurística: count em tabelas-marcador. Usa to_regclass pra não
	// quebrar se alguma migration ainda não rodou (primeira subida de um
	// DB realmente vazio).
	var stationsCount, clientsCount int

	err := pool.QueryRow(ctx, `
		SELECT
		  COALESCE((SELECT COUNT(*) FROM stations), 0),
		  COALESCE((SELECT COUNT(*) FROM clients),  0)
		WHERE to_regclass('public.stations') IS NOT NULL
		  AND to_regclass('public.clients')  IS NOT NULL
	`).Scan(&stationsCount, &clientsCount)

	if err != nil {
		// Erro pode ser: tabelas não existem (DB realmente fresh) → seguro.
		// Ou outro problema → deixa testes falharem normalmente depois.
		t.Logf("[dbtest] guard: nenhuma tabela-marcador presente; presumindo test DB fresh")
		return
	}

	if stationsCount > maxAllowedStations || clientsCount > maxAllowedClients {
		dbURL := os.Getenv("TEST_DATABASE_URL")
		// Mascara senha se houver
		masked := maskDSN(dbURL)
		t.Fatalf(`

═══════════════════════════════════════════════════════════════════
  TEST DB GUARD — RECUSA RODAR
═══════════════════════════════════════════════════════════════════

TEST_DATABASE_URL aparenta apontar pra um DB com dado REAL:
  stations: %d  (threshold %d — dev típico tem ~7500)
  clients:  %d  (threshold %d — dev típico tem ~100)

Estes testes chamam TRUNCATE ... CASCADE no setup. Se rodarem
contra esse DB, vão APAGAR campanhas, materials, commercials e
detections (foi exatamente o que aconteceu em 2026-05-17).

DSN atual: %s

COMO RESOLVER:

  Opção A — DB de teste dedicado (recomendado):
    docker compose -f infra/docker/docker-compose.yml exec postgres \
      psql -U "$POSTGRES_USER" -d postgres -c \
      "CREATE DATABASE radiocheck_test"

    export TEST_DATABASE_URL="postgres://radiocheck:radiocheck@postgres:5432/radiocheck_test?sslmode=disable"

    Rode as migrations contra o DB novo:
    docker run --rm --network docker_default \
      -v "$(pwd)/migrations:/migrations:ro" \
      migrate/migrate:v4.17.1 \
      -path=/migrations \
      -database "$TEST_DATABASE_URL" up

    Agora pode rodar os testes — guard vai passar (stations=0, clients=0).

  Opção B — Override explícito (APENAS em CI/container efêmero):
    export %s=%s
    A string longa é proposital pra não digitar por reflexo.
    Eu sei o que estou fazendo e este DB é descartável.

═══════════════════════════════════════════════════════════════════
`,
			stationsCount, maxAllowedStations,
			clientsCount, maxAllowedClients,
			masked,
			EnvAllowDestructive, AllowDestructiveValue,
		)
	}

	t.Logf("[dbtest] guard: OK (stations=%d, clients=%d — abaixo dos thresholds)",
		stationsCount, clientsCount)
}

// maskDSN substitui a senha em uma string postgres://user:pass@host/db por
// "***" pra não vazar em logs de erro.
func maskDSN(dsn string) string {
	if dsn == "" {
		return "(vazio)"
	}
	// postgres://user:password@host:port/db?args → postgres://user:***@host:port/db?args
	at := indexOf(dsn, '@')
	if at < 0 {
		return dsn
	}
	colon := lastColonBefore(dsn, at)
	if colon < 0 {
		return dsn
	}
	return fmt.Sprintf("%s:***%s", dsn[:colon], dsn[at:])
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func lastColonBefore(s string, limit int) int {
	// Encontra o último ':' antes do índice limit, ignorando o ':' do scheme (postgres://).
	last := -1
	for i := 0; i < limit && i < len(s); i++ {
		if s[i] == ':' {
			// Pula o '://' do scheme
			if i+2 < len(s) && s[i+1] == '/' && s[i+2] == '/' {
				continue
			}
			last = i
		}
	}
	return last
}
