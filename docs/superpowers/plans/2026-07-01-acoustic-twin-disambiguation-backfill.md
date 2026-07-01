# Desambiguação de Gêmeos Acústicos — Plano 3 (backfill)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Dois CLIs — (1) `backfill-twin-discriminative` popula `material_twin_discriminative` pros materiais que já existiam antes da feature; (2) `redisambiguate-twins` reprocessa detecções históricas de gêmeos re-rodando a decisão discriminante a partir do clipe de evidência salvo. O (1) é baixo risco (só escreve a tabela nova); o (2) é ALTO risco (muta atribuição real) → **dry-run por default, `--apply` obrigatório pra escrever, escopado**.

**Architecture:** Reusa o backend do Plano 1. O (1) chama `catalog.TwinDiscriminative.PopulateForMaterial` sobre materiais `ready` (idempotente via Upsert). O (2) constrói um `evidence.Service`, baixa o clipe de cada detecção do S3, decodifica pro mesmo PCM que o pass-path usa, e chama a MESMA `disambiguateTwin` (mesmos guards: co-fire, projeção sync, ambiguous⟺retracted, resolveAttribution ativa). Espelha `cmd/backfill-shared-hashes`.

**Tech Stack:** Go, pgxpool, `radiocheck/internal/{catalog,evidence,sharing,storage,audit}`, flag-based CLI.

**Spec:** [`docs/superpowers/specs/2026-07-01-acoustic-twin-disambiguation-design.md`](../specs/2026-07-01-acoustic-twin-disambiguation-design.md) · **Depende do Plano 1** (backend core, já mergeado na branch `feat/acoustic-twin-disambiguation`). Doc da feature: [`docs/architecture/twin-disambiguation.md`](../../architecture/twin-disambiguation.md).

---

## Pré-requisitos e travas de segurança (LER antes)

- **`redisambiguate-twins` muta atribuição histórica.** Isso é exatamente a classe de operação que causou incidentes (2026-06-25, 2026-06-30). Regras inegociáveis:
  - **Dry-run é o default.** Só escreve com `--apply`.
  - **Sempre escopado** — exige pelo menos um de `--campaign`, `--material`, `--station` ou `--since/--until`. Recusa rodar "no catálogo inteiro" sem `--all` explícito (e mesmo com `--all`, imprime aviso e conta antes).
  - **Reusa `disambiguateTwin`** (não reimplementa a decisão) → herda co-fire guard + `ReattributeDetection` (projeção sync) + `MarkAmbiguous` (retração) + `resolveAttribution` (só campanha viva). NUNCA um `UPDATE detections` cru.
  - **Preview por detecção** no dry-run: imprime `detection_id, station, attributed→winner, covSelf/covTwin, ação` sem escrever.
  - Rode primeiro **scoped na campanha Milium conhecida** e confira o preview antes de `--apply`.
- **`backfill-twin-discriminative` é seguro** (só INSERT/UPSERT na tabela nova, não toca detections). Idempotente.
- **Testar contra CÓPIA dos dados de prod** (§4.8 / regra 6.3), não DB local vazio — a decisão depende de gêmeos+clipes reais. O `backfill` é aditivo (passa no shadow test); o `redisambiguate` NÃO roda no shadow test (não é migration) — valide manualmente numa cópia.

---

## File Structure

| Arquivo | Responsabilidade | Ação |
|---|---|---|
| `workers/cmd/backfill-twin-discriminative/main.go` | CLI: popula a tabela pros materiais ready | Criar |
| `workers/internal/evidence/twin_disambig.go` | `RedisambiguateDetection` (wrapper público de `disambiguateTwin`) + `EligibleTwinDetections` query | Modificar |
| `workers/cmd/redisambiguate-twins/main.go` | CLI: reprocessa detecções históricas (dry-run/apply, escopado) | Criar |
| `docs/features/twin-backfill.md` | Doc operacional dos dois comandos | Criar |

**Ordem:** backfill (Task 1) → wrapper+query (Task 2) → redisambiguate CLI (Task 3) → docs (Task 4).

---

## Task 1: CLI `backfill-twin-discriminative`

**Files:**
- Create: `workers/cmd/backfill-twin-discriminative/main.go`

Espelha `cmd/backfill-shared-hashes/main.go`. Itera **materiais** `fingerprint_status='ready'` (gêmeos são materiais) e chama `PopulateForMaterial`. Só escreve `material_twin_discriminative`.

- [ ] **Step 1: Escreva o main.go**

```go
// backfill-twin-discriminative popula material_twin_discriminative pros materiais
// que já eram 'ready' antes da feature de desambiguação de gêmeos (spec
// 2026-07-01). Idempotente (Upsert dos dois lados por par). Seguro: só escreve a
// tabela nova, não toca detections. Rode uma vez após 0046/0047 landing em prod.
//
//	backfill-twin-discriminative --dsn "$DATABASE_URL"
//	backfill-twin-discriminative --dsn "$DATABASE_URL" --client <uuid>
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
		start := time.Now()
		if err := repo.PopulateForMaterial(ctx, j.id); err != nil {
			// best-effort: PopulateForMaterial já é tolerante a falha parcial por gêmeo.
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
```

- [ ] **Step 2: Cross-compile linux** (regra 6.1):
```bash
cd workers && WPATH=$(pwd -W) && MSYS_NO_PATHCONV=1 docker run --rm -v "${WPATH}:/app" -w /app -v rc_gomod:/go/pkg/mod -v rc_gocache:/root/.cache/go-build -e CGO_ENABLED=0 -e GOOS=linux -e GOFLAGS=-mod=mod golang:1.26-alpine sh -c 'go build ./... && echo OK'
```
Expected `OK`.

- [ ] **Step 3: Commit**
```bash
git add workers/cmd/backfill-twin-discriminative/main.go
git commit -m "feat(cmd): backfill-twin-discriminative popula regiões discriminantes de gêmeos existentes"
```

> Sem teste unitário dedicado (é shell CLI que orquestra `PopulateForMaterial`, já coberto pelos testes do repo + a filosofia de shells do repo). Valide manualmente contra uma cópia de prod: rode com `--client <milium>` e confira `SELECT count(*), min(disc_frames), max(disc_frames) FROM material_twin_discriminative`.

---

## Task 2: `RedisambiguateDetection` (wrapper) + query de elegíveis

**Files:**
- Modify: `workers/internal/evidence/twin_disambig.go`

O CLI (Task 3) está noutro pacote e `disambiguateTwin` é privado. Exponha um wrapper fino + uma query que lista as detecções candidatas (atribuídas a um material que TEM gêmeo populado, no escopo).

- [ ] **Step 1: Wrapper público.** Em `twin_disambig.go`:
```go
// RedisambiguateDetection re-roda a decisão de desambiguação de gêmeos sobre UMA
// detecção histórica, a partir do PCM do clipe de evidência já baixado. Usado
// pelo CLI redisambiguate-twins (backfill). Mesma lógica/guards do pass-path
// (co-fire, projeção sync, ambiguous⟺retracted). Best-effort.
func (s *Service) RedisambiguateDetection(ctx context.Context, detectionID uuid.UUID, detectedAt time.Time, stationID, attributedID uuid.UUID, pcm []float32) {
	s.disambiguateTwin(ctx, detectionID, detectedAt, stationID, attributedID, pcm)
}
```

- [ ] **Step 2: Query de elegíveis.** Método no `catalog.TwinDiscriminative` (ou `Detections`) que lista detecções APROVADAS (usar `ApprovedDetectionsFilter`) cujo `commercial_id` aparece como `material_id` em `material_twin_discriminative`, com `evidence_key` não-vazio (precisa do clipe), no escopo (campaign/material/station/janela). Retorna `{id, detectedAt, stationID, commercialID, evidenceKey}`. Escreva a query espelhando os filtros do `ListPaged` + join na tabela de gêmeos. Sem teste dedicado (query de leitura; valide no dry-run).

- [ ] **Step 3: Cross-compile + commit.**
```bash
git add workers/internal/evidence/twin_disambig.go workers/internal/catalog/*.go
git commit -m "feat(evidence): RedisambiguateDetection + query de detecções elegíveis (backfill de gêmeos)"
```

---

## Task 3: CLI `redisambiguate-twins` (dry-run default, escopado, --apply)

**Files:**
- Create: `workers/cmd/redisambiguate-twins/main.go`

- [ ] **Step 1: Estrutura + travas.** Flags: `--dsn`, escopo (`--campaign`, `--material`, `--station`, `--since`, `--until`, `--all`), `--apply` (default false = dry-run), `--limit`. Recusa rodar sem escopo a menos que `--all` (e com `--all` imprime aviso + count e pede confirmação via `--yes`). Constrói:
  - `pool`, `storage.Client` (S3), `audit.NewAuditor(pool, log, 0, 0)`, `catalog.NewDetections(pool)`, `catalog.NewDetectionCampaigns(pool)`, `evidence.NewService(pool, s3, nil /*nc*/, detections, detCampaigns, auditor, false, false, false, log)` (nc nil: o CLI não publica eventos; confirme que Service não deref nc fora de Subscribe — se deref, passe um nc real ou um stub).

> **Cuidado (nc nil):** `disambiguateTwin`/`reattributeTwinWithCofireGuard` não usam `s.nc`. Confirme por leitura que nenhum caminho chamado pelo CLI toca `s.nc`. Se tocar, o CLI precisa de uma conexão NATS real.

- [ ] **Step 2: Loop.** Pra cada detecção elegível (Task 2 query):
  - Baixa `evidence_key` do S3 e decodifica pro PCM — **reusar o MESMO caminho de decode do pass-path** (procure em `service.go` como `runAuditOrReject` obtém o `pcm` do clipe; extraia/reuse esse helper). 
  - **Dry-run:** roda uma versão de PREVIEW que computa o veredito (audit self + twins + `chooseTwinByDiscriminative` + `pickTwinAction`) e IMPRIME `detection_id station attributed→winner covSelf/covTwin AÇÃO` sem escrever. (Extraia a parte de decisão de `disambiguateTwin` numa função que devolve `(action, winner, evals)` sem aplicar — reusável pelo preview E pelo apply.)
  - **`--apply`:** chama `s.RedisambiguateDetection(...)` (aplica com todos os guards).
  - Conta por ação (`reattribute`/`ambiguous`/`keep`/`cofire_retract`).

- [ ] **Step 3: Refactor pra preview-sem-aplicar.** Em `twin_disambig.go`, quebre `disambiguateTwin` em: `evaluateTwins(ctx, attributedID, pcm) (action twinVerdict, winner *twinEval, evals []twinEval)` (PURO da parte de decisão, dado os audits — testável) + a aplicação. O preview do CLI usa `evaluateTwins`; o apply usa `disambiguateTwin` inteiro. Adicione teste de `evaluateTwins`? A parte de decisão já é `pickTwinAction` (testado); `evaluateTwins` é orquestração de audit (shell). Mantenha `pickTwinAction` como o núcleo testado.

- [ ] **Step 4: Cross-compile + commit.**
```bash
git add workers/cmd/redisambiguate-twins/main.go workers/internal/evidence/twin_disambig.go
git commit -m "feat(cmd): redisambiguate-twins (dry-run default, escopado) reprocessa atribuição de gêmeos histórica"
```

- [ ] **Step 5: Validação manual (NÃO pule).** Contra uma cópia de prod: `redisambiguate-twins --dsn <copia> --campaign <milium>` (dry-run) → confira o preview linha a linha contra o que o fornecedor/verdade diz. Só então `--apply` num escopo pequeno. Documente os números no PR.

---

## Task 4: Doc operacional

**Files:**
- Create: `docs/features/twin-backfill.md` (header YAML `status: implementado`, `codigo-relacionado` os dois cmd + twin_disambig.go)

- [ ] **Step 1** Documente: quando rodar cada comando, a ordem (backfill popula ANTES do redisambiguate ter o que usar), as travas de segurança do redisambiguate (dry-run/escopo/apply), e o procedimento de validação contra cópia de prod. Cruze com [twin-disambiguation.md](../../architecture/twin-disambiguation.md) e marque o item do checklist de pré-ativação como coberto. Commit.

---

## Self-Review (feito)

- **Cobertura da spec:** §7 (backfill) → Tasks 1-3. `backfill-twin-discriminative` (popular) + `redisambiguate-twins` (reprocessar) = os dois comandos que a decomposição previa.
- **Placeholders:** os "reuse o helper de decode do pass-path" (Task 3 Step 2) e "quebre disambiguateTwin em evaluateTwins" (Step 3) são passos de descoberta/refactor explícitos com alvo nomeado, não buracos. O CLI de baixo risco (Task 1) tem código completo.
- **Consistência de tipos:** `RedisambiguateDetection` espelha a assinatura de `disambiguateTwin`; `evaluateTwins` devolve os mesmos `twinVerdict`/`*twinEval` já definidos no Plano 1.
- **Segurança:** o comando mutante é dry-run-default + escopado + reusa os guards do Plano 1 (nada de UPDATE cru). O comando aditivo é idempotente.

## Riscos de execução

- **`nc` nil no Service do CLI** (Task 3 Step 1): confirme por leitura que o caminho do CLI não deref `s.nc`. Se deref, o CLI precisa de NATS real — reavalie.
- **Decode de evidência histórica**: alguns `evidence_key` antigos podem estar em cold storage / ausentes. Trate com skip+log (como o `--skip-missing` do backfill-shared-hashes).
- **Custo do redisambiguate**: baixar+decodificar S3 por detecção é caro. Escope sempre; `--limit` pra amostrar antes.
- **`evaluateTwins` refactor** (Task 3 Step 3) mexe no `disambiguateTwin` do Plano 1 — rode a suíte `evidence` inteira depois e confirme que o pass-path continua idêntico.
