# Discriminação de containment para material curto — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Registrar a relação "material curto X está contido no spot Y" e usá-la na janela de análise para distinguir "o curto tocou sozinho" de "o curto é a cauda do spot" — substituindo a heurística de altura de score que hoje decide isso às cegas.

**Architecture:** O `sharing.go` já **calcula** a sobreposição entre pares e joga fora quando um lado tem <10s. Passamos a persistir essa relação numa tabela nova (`material_containments`), carregá-la no worker, e consultá-la na state machine: se o container está casando na MESMA janela, na posição onde o curto mora dentro dele, o match do curto é cauda e não confirma sozinho. Tudo atrás de flag, default OFF.

**Tech Stack:** Go (workers), PostgreSQL (migration), Prometheus (métrica).

**Contexto obrigatório:** [spec de design](../specs/2026-08-07-containment-aware-short-material-design.md) — em especial §2.4 (as 6 mortes medidas) e §4 (componentes). Feature relacionada que este plano estende: [short-material-single-window.md](../../features/short-material-single-window.md).

**Regras do projeto que este plano OBEDECE (CLAUDE.md):** §6.1 cross-compile antes de push; §6.7 CLI novo exige as 2 linhas no `workers.Dockerfile`; §4.8 migration testada contra cópia de prod (aqui é `CREATE TABLE` puro, sem dependência de dado); §7 prod é executado pelo Dereck.

---

### Task 1: Diagnóstico da falha do dedup (pré-requisito, sem código)

A spec §3.2 rejeita a alternativa "publicar-provisório" porque o dedup foi flagrado falhando. Antes de construir em cima dele, entender aquela falha. **Esta task não escreve código** — produz SQL que o Dereck roda (CLAUDE.md §7).

**Files:**
- Create: `scripts/sql/diagnose-dedup-miss-unifique.sql`

- [ ] **Step 1: Criar o arquivo com este conteúdo**

```sql
-- diagnose-dedup-miss-unifique.sql — READ-ONLY.
-- Caso: 07/08 16:28 Jovem Pan, citação 180 (6,4s, audit_coverage 0,1667 =
-- false-confirm) contou JUNTO com o spot 169 (30,8s, cov 0,5316), 22s de
-- diferença, mesmo cliente — e o dedup §18.2.2 não retratou nenhuma.
-- Ver docs/superpowers/specs/2026-08-07-containment-aware-short-material-design.md §2.5

\echo '=== 1. as duas rows, com tudo que o dedup usa pra decidir ==='
SELECT m.short_id, m.duration_seconds AS dur,
       to_char(d.detected_at AT TIME ZONE 'America/Sao_Paulo','DD/MM HH24:MI:SS') AS detected_at,
       d.hash_count, d.confidence, d.audit_coverage AS cov,
       d.evidence_status, (d.retracted_at IS NOT NULL) AS retratada,
       c.id AS client_id, c.name AS cliente
FROM detections d
JOIN materials m ON m.id = d.commercial_id
JOIN clients c   ON c.id = m.client_id
WHERE m.short_id IN (169,180)
  AND d.detected_at BETWEEN '2026-08-07 19:27:00+00' AND '2026-08-07 19:30:00+00'
ORDER BY d.detected_at;

\echo ''
\echo '=== 2. o dedup registrou alguma supressao nesse instante? ==='
SELECT to_char(ds.detected_at AT TIME ZONE 'America/Sao_Paulo','DD/MM HH24:MI:SS') AS quando,
       ds.suppressed_short_id, ds.kept_short_id, ds.reason,
       ds.suppressed_duration, ds.kept_duration,
       ds.broadcast_start
FROM dedup_suppressions ds
WHERE ds.detected_at BETWEEN '2026-08-07 19:25:00+00' AND '2026-08-07 19:32:00+00';

\echo ''
\echo '=== 3. os dois materiais sao mesmo do mesmo client_id? ==='
SELECT m.short_id, m.client_id, c.name, m.duration_seconds
FROM materials m JOIN clients c ON c.id=m.client_id
WHERE m.short_id IN (169,180);

\echo ''
\echo '=== 4. recorrencia: o par 180x169 ja contou junto outras vezes? ==='
SELECT date(a.detected_at AT TIME ZONE 'America/Sao_Paulo') AS dia, count(*) AS ocorrencias
FROM detections a
JOIN materials ma ON ma.id=a.commercial_id AND ma.short_id=180
JOIN detections b ON b.station_id=a.station_id
     AND b.detected_at BETWEEN a.detected_at - interval '90 seconds'
                           AND a.detected_at + interval '90 seconds'
JOIN materials mb ON mb.id=b.commercial_id AND mb.short_id=169
WHERE a.detected_at > now() - interval '30 days'
  AND a.retracted_at IS NULL AND b.retracted_at IS NULL
GROUP BY 1 ORDER BY 1;
```

- [ ] **Step 2: Commit**

```bash
git add scripts/sql/diagnose-dedup-miss-unifique.sql
git commit -m "chore(sql): diagnóstico read-only da falha do dedup no par UNIFIQUE 180x169"
```

- [ ] **Step 3: Entregar ao Dereck e PARAR até a resposta**

O resultado decide se o dedup tem bug de janela de broadcast (item 2 vazio = o conflito nem foi detectado) ou de race (item 2 com linha = detectou e a retração se perdeu). **Não bloqueia as Tasks 2-9** — elas não dependem do dedup —, mas bloqueia qualquer decisão de baixar o `SHORT_SINGLE_WINDOW_FACTOR`.

---

### Task 2: Migration da tabela `material_containments`

**Files:**
- Create: `migrations/0063_material_containments.up.sql`
- Create: `migrations/0063_material_containments.down.sql`

- [ ] **Step 1: Criar o `.up.sql`**

```sql
-- 0063_material_containments.up.sql
-- Registra "o material curto X está contido no material longo Y", com a
-- posição de X dentro de Y.
--
-- Por que existe: o shared-hash scan (workers/internal/sharing/sharing.go)
-- JÁ calcula essa sobreposição, mas descarta o resultado quando qualquer lado
-- tem menos de MinShareableDurationSeconds (10s) — o flagging de is_shared
-- realmente não se aplica a material curto. Só que sem registrar a relação,
-- nada no pipeline sabe que o pulso de 5,7s mora no final do spot de 30s, e a
-- state machine não consegue distinguir "o pulso tocou" de "é a cauda do spot".
--
-- Medição que motivou (2026-08-06/07): de 6 mortes do pulso MILIUM, 5 eram
-- cauda de spot (o spot casou na mesma emissora 24-26s antes, com
-- audit_coverage 0,76-0,82) e 1 era tocada real perdida. Nenhum limiar de
-- score separa os dois casos; a relação de containment separa.
-- Ver docs/superpowers/specs/2026-08-07-containment-aware-short-material-design.md
--
-- Migration puramente estrutural (CREATE TABLE de tabela nova): não depende de
-- dados existentes, então não cai no risco da regra 4.8 do CLAUDE.md.
BEGIN;

CREATE TABLE material_containments (
    -- o curto (< 10s). FK lógica: materials.id OU commercials.id — o pipeline
    -- é polimórfico aqui, igual a detections.commercial_id. Sem FK por isso.
    contained_id    UUID NOT NULL,
    -- o longo que o contém (>= 10s), sempre do MESMO cliente.
    container_id    UUID NOT NULL,
    -- onde dentro do container o curto começa. É o discriminador: o worker
    -- compara com o OffsetFrames do match do container na janela corrente.
    offset_seconds  REAL NOT NULL,
    -- frames do curto cobertos pela relação — serve de triagem/confiança.
    match_frames    INT  NOT NULL,
    measured_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (contained_id, container_id)
);

-- O worker carrega por container para montar o mapa da emissora.
CREATE INDEX material_containments_container_idx
    ON material_containments (container_id);

COMMIT;
```

- [ ] **Step 2: Criar o `.down.sql`**

```sql
-- 0063_material_containments.down.sql
BEGIN;
DROP TABLE IF EXISTS material_containments;
COMMIT;
```

- [ ] **Step 3: Aplicar no DB local e conferir**

```bash
cd infra/docker && docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < ../../migrations/0063_material_containments.up.sql
```
Esperado: `BEGIN / CREATE TABLE / CREATE INDEX / COMMIT`, sem erro.

- [ ] **Step 4: Commit**

```bash
git add migrations/0063_material_containments.up.sql migrations/0063_material_containments.down.sql
git commit -m "feat(db): tabela material_containments (curto contido em spot, com offset)"
```

---

### Task 3: Detectar containment no scan (TDD, sem DB)

`classifyAndFilter` hoje descarta o par quando um lado é curto. Passa a devolver também as relações detectadas. O comportamento de flagging **não muda**.

**Files:**
- Modify: `workers/internal/sharing/sharing.go:376-411` (`classifyAndFilter`)
- Test: `workers/internal/sharing/containment_test.go`

- [ ] **Step 1: Escrever o teste que falha**

Criar `workers/internal/sharing/containment_test.go`:

```go
package sharing

import (
	"testing"

	"github.com/google/uuid"
)

// framesPerSecond do pipeline: 16000/2048 = 7.8125.
// Um spot de 30,7s ≈ 240 frames; um pulso de 5,7s ≈ 44 frames.
// MinShareableDurationFrames é 78 (10s), então 44 é "curto" e 240 é "longo".

func TestClassifyAndFilter_RegistraContainmentDoCurtoNoLongo(t *testing.T) {
	curto := uuid.New()
	longo := uuid.New()

	// O curto (44 frames) casa INTEIRO, e no longo esse trecho fica em
	// [196, 240] — ou seja, no final, que é onde o pulso mora no spot.
	report := scanReport{
		ownCommercialID: curto,
		ownTotalFrames:  44,
		perOther: map[uuid.UUID]*perOtherScan{
			longo: {
				otherTotalFrames: 240,
				ownRanges:        []frameRange{{0, 44}},
				otherRanges:      []frameRange{{196, 240}},
			},
		},
	}

	flags, containments := classifyAndFilter(report, 0.5)

	if len(flags) != 0 {
		t.Fatalf("flagging não pode mudar para par com lado curto, got %v", flags)
	}
	if len(containments) != 1 {
		t.Fatalf("esperava 1 containment, got %d", len(containments))
	}
	c := containments[0]
	if c.ContainedID != curto || c.ContainerID != longo {
		t.Errorf("direção errada: contained=%v container=%v", c.ContainedID, c.ContainerID)
	}
	// 196 frames / 7.8125 = 25.088s — bate com os 24-26s medidos em prod.
	if c.OffsetSeconds < 24.5 || c.OffsetSeconds > 25.5 {
		t.Errorf("offset = %.2fs, esperava ~25.1s", c.OffsetSeconds)
	}
	if c.MatchFrames != 44 {
		t.Errorf("match_frames = %d, esperava 44", c.MatchFrames)
	}
}

func TestClassifyAndFilter_RegistraContainmentQuandoOwnEhOLongo(t *testing.T) {
	longo := uuid.New()
	curto := uuid.New()

	// Mesma relação, scan rodando a partir do LONGO.
	report := scanReport{
		ownCommercialID: longo,
		ownTotalFrames:  240,
		perOther: map[uuid.UUID]*perOtherScan{
			curto: {
				otherTotalFrames: 44,
				ownRanges:        []frameRange{{196, 240}},
				otherRanges:      []frameRange{{0, 44}},
			},
		},
	}

	_, containments := classifyAndFilter(report, 0.5)

	if len(containments) != 1 {
		t.Fatalf("esperava 1 containment, got %d", len(containments))
	}
	c := containments[0]
	if c.ContainedID != curto || c.ContainerID != longo {
		t.Errorf("direção errada: contained=%v container=%v", c.ContainedID, c.ContainerID)
	}
	if c.OffsetSeconds < 24.5 || c.OffsetSeconds > 25.5 {
		t.Errorf("offset = %.2fs, esperava ~25.1s", c.OffsetSeconds)
	}
}

func TestClassifyAndFilter_NaoRegistraQuandoOsDoisSaoLongos(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	report := scanReport{
		ownCommercialID: a,
		ownTotalFrames:  240,
		perOther: map[uuid.UUID]*perOtherScan{
			b: {otherTotalFrames: 200, ownRanges: []frameRange{{0, 30}}, otherRanges: []frameRange{{0, 30}}},
		},
	}
	_, containments := classifyAndFilter(report, 0.5)
	if len(containments) != 0 {
		t.Fatalf("par longo×longo não é containment, got %d", len(containments))
	}
}

func TestClassifyAndFilter_NaoRegistraCoberturaParcial(t *testing.T) {
	curto := uuid.New()
	longo := uuid.New()
	// Só 10 dos 44 frames do curto casam (0,23) — abaixo do subsetThreshold
	// 0,5. Não é containment, é coincidência acústica.
	report := scanReport{
		ownCommercialID: curto,
		ownTotalFrames:  44,
		perOther: map[uuid.UUID]*perOtherScan{
			longo: {otherTotalFrames: 240, ownRanges: []frameRange{{0, 10}}, otherRanges: []frameRange{{100, 110}}},
		},
	}
	_, containments := classifyAndFilter(report, 0.5)
	if len(containments) != 0 {
		t.Fatalf("cobertura parcial não é containment, got %d", len(containments))
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

```bash
cd workers && go test ./internal/sharing -run TestClassifyAndFilter_Registra -v
```
Esperado: FAIL de compilação — `classifyAndFilter` devolve 1 valor, não 2; `Containment` não existe.

- [ ] **Step 3: Implementar**

Em `workers/internal/sharing/sharing.go`, adicionar o tipo logo após `type frameRange struct{ from, until int32 }` (linha 224):

```go
// Containment descreve "o material curto ContainedID está contido no material
// longo ContainerID, começando em OffsetSeconds dentro dele".
//
// O shared-hash flagging não se aplica a material <10s (ver
// MinShareableDurationSeconds), mas a RELAÇÃO importa: sem ela a state machine
// não distingue "o pulso tocou sozinho" de "o pulso é a cauda do spot" — os
// dois produzem o mesmo score. Medição em prod (2026-08-06/07): 5 de 6 mortes
// do pulso eram cauda, com o spot casando 24-26s antes na mesma emissora.
type Containment struct {
	ContainedID   uuid.UUID
	ContainerID   uuid.UUID
	OffsetSeconds float64
	MatchFrames   int
}

// framesToSeconds converte frames de análise em segundos usando a mesma
// fórmula do resto do pipeline (16000 / 2048 = 7,8125 frames/s).
func framesToSeconds(f int32) float64 {
	return float64(f) * 2048.0 / float64(fingerprint.SampleRate)
}

// firstFrame devolve o menor `from` de um conjunto de ranges (0 se vazio).
func firstFrame(rs []frameRange) int32 {
	if len(rs) == 0 {
		return 0
	}
	min := rs[0].from
	for _, r := range rs[1:] {
		if r.from < min {
			min = r.from
		}
	}
	return min
}
```

Substituir a função `classifyAndFilter` inteira (linhas 376-411) por:

```go
func classifyAndFilter(report scanReport, subsetThreshold float64) (map[uuid.UUID][]frameRange, []Containment) {
	out := make(map[uuid.UUID][]frameRange)
	var containments []Containment
	if report.ownTotalFrames == 0 {
		return out, nil
	}
	ownIsShort := report.ownTotalFrames < MinShareableDurationFrames

	for otherID, scan := range report.perOther {
		otherIsShort := scan.otherTotalFrames < MinShareableDurationFrames

		ownCov := float64(frameCoverage(scan.ownRanges)) / float64(report.ownTotalFrames)
		var otherCov float64
		if scan.otherTotalFrames > 0 {
			otherCov = float64(frameCoverage(scan.otherRanges)) / float64(scan.otherTotalFrames)
		}

		// Containment: exatamente um lado é curto e o curto está
		// essencialmente inteiro dentro do longo. O offset vem das ranges
		// medidas NO LONGO — é onde o curto mora dentro dele.
		if ownIsShort != otherIsShort {
			if ownIsShort && ownCov >= subsetThreshold {
				containments = append(containments, Containment{
					ContainedID:   report.ownCommercialID,
					ContainerID:   otherID,
					OffsetSeconds: framesToSeconds(firstFrame(scan.otherRanges)),
					MatchFrames:   frameCoverage(scan.ownRanges),
				})
			}
			if otherIsShort && otherCov >= subsetThreshold {
				containments = append(containments, Containment{
					ContainedID:   otherID,
					ContainerID:   report.ownCommercialID,
					OffsetSeconds: framesToSeconds(firstFrame(scan.ownRanges)),
					MatchFrames:   frameCoverage(scan.otherRanges),
				})
			}
		}

		// Comerciais curtos não participam do shared-hash flagging (ver doc da
		// constante MinShareableDurationSeconds) — skip simétrico dos 2 lados.
		if ownIsShort || otherIsShort {
			continue
		}
		score := ownCov
		if otherCov > score {
			score = otherCov
		}
		if score >= subsetThreshold {
			// Subset/duplicate — não flag.
			continue
		}
		out[report.ownCommercialID] = append(out[report.ownCommercialID], scan.ownRanges...)
		out[otherID] = append(out[otherID], scan.otherRanges...)
	}
	return out, containments
}
```

Ajustar o **único** chamador em `MarkSharedHashes` (procurar `classifyAndFilter(` no arquivo) para receber os dois valores. Nesta task, descartar o segundo com `_` — a Task 4 o persiste:

```go
	flagged, _ := classifyAndFilter(report, SubsetThreshold)
```

- [ ] **Step 4: Rodar e ver passar**

```bash
cd workers && go test ./internal/sharing -v 2>&1 | tail -20
CGO_ENABLED=0 GOOS=linux go build ./...
```
Esperado: todos os testes do pacote passam (inclusive os antigos — o flagging não mudou) e build limpo.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/sharing/sharing.go workers/internal/sharing/containment_test.go
git commit -m "feat(sharing): detecta e devolve relação de containment para pares <10s"
```

---

### Task 4: Persistir os containments

> **Atenção — ciclo de import (verificado em 2026-08-07):** `catalog` **já importa** `sharing`
> (`workers/internal/catalog/twin_discriminative.go:16`). Portanto `sharing` **não pode** importar
> `catalog`. A escrita fica no próprio pacote `sharing` (query própria) e só a leitura mora em
> `catalog`, que é quem o `main.go` consome.

**Files:**
- Create: `workers/internal/sharing/containment_store.go` (escrita)
- Create: `workers/internal/catalog/material_containments.go` (leitura)
- Modify: `workers/internal/sharing/sharing.go` (`MarkSharedHashes`, o `_` da Task 3)
- Test: `workers/internal/catalog/material_containments_test.go`

- [ ] **Step 1a: Criar a escrita, dentro do pacote `sharing`**

`workers/internal/sharing/containment_store.go`:

```go
package sharing

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// upsertContainments grava as relações medidas pelo scan (migration 0063).
//
// Mora neste pacote, e não em `catalog`, porque `catalog` já importa `sharing`
// (twin_discriminative.go) — o caminho inverso fecharia um ciclo de import. A
// LEITURA fica em catalog.MaterialContainments, que é quem o boot consome.
func upsertContainments(ctx context.Context, pool *pgxpool.Pool, cs []Containment) error {
	for _, c := range cs {
		if _, err := pool.Exec(ctx, `
			INSERT INTO material_containments
			    (contained_id, container_id, offset_seconds, match_frames, measured_at)
			VALUES ($1, $2, $3, $4, NOW())
			ON CONFLICT (contained_id, container_id) DO UPDATE
			SET offset_seconds = EXCLUDED.offset_seconds,
			    match_frames   = EXCLUDED.match_frames,
			    measured_at    = NOW()`,
			c.ContainedID, c.ContainerID, c.OffsetSeconds, c.MatchFrames); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 1b: Criar a leitura, no pacote `catalog`**

`workers/internal/catalog/material_containments.go`:

```go
package catalog

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaterialContainments lê a relação "curto contido em longo" (migration 0063).
// A ESCRITA mora em internal/sharing (upsertContainments) para não fechar
// ciclo de import — catalog já importa sharing. Ver
// docs/superpowers/specs/2026-08-07-containment-aware-short-material-design.md
type MaterialContainments struct{ pool *pgxpool.Pool }

func NewMaterialContainments(pool *pgxpool.Pool) *MaterialContainments {
	return &MaterialContainments{pool: pool}
}

// ContainmentRow é a relação já resolvida em short_ids — que é o identificador
// com que o worker trabalha em runtime.
type ContainmentRow struct {
	ContainedShortID int32
	ContainerShortID int32
	OffsetSeconds    float64
}

// ListAll devolve todas as relações resolvidas em short_id. O pipeline é
// polimórfico (materials OU commercials), então resolve nos dois — igual ao
// loader do índice.
func (r *MaterialContainments) ListAll(ctx context.Context) ([]ContainmentRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT ci.short_id AS contained_short_id,
		       co.short_id AS container_short_id,
		       mc.offset_seconds
		FROM material_containments mc
		JOIN LATERAL (
		    SELECT short_id FROM materials   WHERE id = mc.contained_id
		    UNION ALL
		    SELECT short_id FROM commercials WHERE id = mc.contained_id
		    LIMIT 1
		) ci ON true
		JOIN LATERAL (
		    SELECT short_id FROM materials   WHERE id = mc.container_id
		    UNION ALL
		    SELECT short_id FROM commercials WHERE id = mc.container_id
		    LIMIT 1
		) co ON true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ContainmentRow
	for rows.Next() {
		var c ContainmentRow
		if err := rows.Scan(&c.ContainedShortID, &c.ContainerShortID, &c.OffsetSeconds); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
```

- [ ] **Step 2: Persistir no `MarkSharedHashes`**

Em `workers/internal/sharing/sharing.go`, trocar a linha da Task 3 (`flagged, _ := ...`) por:

```go
	flagged, containments := classifyAndFilter(report, SubsetThreshold)

	// Persistir a relação de containment medida no scan.
	if len(containments) > 0 {
		if err := upsertContainments(ctx, pool, containments); err != nil {
			return fmt.Errorf("sharing: upsert containments: %w", err)
		}
	}
```

Nenhum import novo é necessário: `upsertContainments` mora no mesmo pacote.

- [ ] **Step 3: Teste do repositório (DB-gated, padrão do repo)**

`workers/internal/catalog/material_containments_test.go`:

```go
package catalog

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// newTestPool é o helper de DB já usado pelos outros testes deste pacote
// (verificado em 2026-08-07). Sem DB de teste configurado, ele pula.
func TestMaterialContainments_ListAllResolveShortIDs(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Duas linhas cruas; o ListAll tem que resolver os UUIDs em short_id
	// pelos dois caminhos (materials e commercials).
	contained, container := uuid.New(), uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO material_containments (contained_id, container_id, offset_seconds, match_frames)
		VALUES ($1, $2, 25.09, 44)
		ON CONFLICT (contained_id, container_id) DO UPDATE SET offset_seconds = EXCLUDED.offset_seconds`,
		contained, container)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	rows, err := NewMaterialContainments(pool).ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	// Os UUIDs acima não existem em materials/commercials, então os JOINs
	// LATERAL não produzem linha — ListAll não pode explodir nem inventar.
	for _, r := range rows {
		if r.ContainedShortID == 0 || r.ContainerShortID == 0 {
			t.Errorf("short_id zerado numa linha: %+v", r)
		}
	}
}
```

> A escrita (`upsertContainments`) é exercitada pelos testes do pacote `sharing` e pelo backfill;
> aqui o que importa é a resolução de short_id, que é o que o boot consome.

- [ ] **Step 4: Rodar e conferir**

```bash
cd workers && go build ./... && go test ./internal/catalog -run TestMaterialContainments -v
CGO_ENABLED=0 GOOS=linux go build ./...
```
Esperado: build limpo (se acusar ciclo de import, algo do Step 1a foi para o pacote errado).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/sharing/containment_store.go workers/internal/catalog/material_containments.go workers/internal/catalog/material_containments_test.go workers/internal/sharing/sharing.go
git commit -m "feat(sharing): persiste material_containments no scan"
```

---

### Task 5: State machine consulta o co-fire do container (TDD)

**Files:**
- Modify: `workers/internal/match/statemachine.go`
- Test: `workers/internal/match/statemachine_shortwindow_test.go` (adicionar ao final)

- [ ] **Step 1: Escrever o teste que falha**

Acrescentar ao final de `workers/internal/match/statemachine_shortwindow_test.go`:

```go
// Discriminação de containment (spec 2026-08-07): quando o spot que contém
// este material está casando NESTA janela, na posição onde ele mora dentro do
// spot, o match é cauda — não pode confirmar em janela única.
//
// Medição em prod (2026-08-06/07): das 6 mortes do pulso MILIUM, 5 eram cauda
// (spot com audit_coverage 0,76-0,82 na mesma emissora, 24-26s antes) e 1 era
// tocada real. Um limiar de score não separa os dois; esta flag separa.
func TestShortSingleWindow_NaoConfirmaQuandoContainerCoFira(t *testing.T) {
	sm := newTestStateMachine(shortFrames, 19, 0.15)
	sm.EnableShortSingleWindow(2.5)
	sm.SetContainerCoFiring(true)

	// Score 76 — o mesmo que confirmaria sozinho sem o co-fire.
	det := sm.Update(MatchResult{UniqueScore: 76, Score: 76, OffsetFrames: 4}, time.Now())

	assert.Nil(t, det, "cauda do container não pode confirmar em janela única")
	assert.Equal(t, StateDetecting, sm.State(), "deve seguir esperando a 2ª janela")
}

func TestShortSingleWindow_ConfirmaQuandoContainerNaoCoFira(t *testing.T) {
	sm := newTestStateMachine(shortFrames, 19, 0.15)
	sm.EnableShortSingleWindow(2.5)
	sm.SetContainerCoFiring(false)

	det := sm.Update(MatchResult{UniqueScore: 76, Score: 76, OffsetFrames: 4}, time.Now())

	require.NotNil(t, det, "sem co-fire do container, é tocada própria — confirma")
	assert.Equal(t, 76, det.HashCount)
}

// O flag é por janela: precisa ser reavaliado a cada Update, não ficar grudado.
func TestShortSingleWindow_CoFiringEhPorJanela(t *testing.T) {
	sm := newTestStateMachine(shortFrames, 19, 0.15)
	sm.EnableShortSingleWindow(2.5)

	sm.SetContainerCoFiring(true)
	if det := sm.Update(MatchResult{UniqueScore: 76, Score: 76, OffsetFrames: 4}, time.Now()); det != nil {
		t.Fatal("com co-fire não deveria confirmar")
	}
	// Reset pro estado inicial e nova janela, agora sem co-fire.
	sm2 := newTestStateMachine(shortFrames, 19, 0.15)
	sm2.EnableShortSingleWindow(2.5)
	sm2.SetContainerCoFiring(false)
	if det := sm2.Update(MatchResult{UniqueScore: 76, Score: 76, OffsetFrames: 4}, time.Now()); det == nil {
		t.Fatal("sem co-fire deveria confirmar")
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

```bash
cd workers && go test ./internal/match -run TestShortSingleWindow_ -v 2>&1 | tail -10
```
Esperado: FAIL — `sm.SetContainerCoFiring undefined`.

- [ ] **Step 3: Implementar**

Em `workers/internal/match/statemachine.go`, adicionar o campo junto de `shortSingleWindowFactor`:

```go
	// containerCoFiring: nesta janela, o material que CONTÉM este está casando
	// na posição onde este mora dentro dele. Setado pelo worker antes de cada
	// Update (é estado de janela, não de configuração).
	containerCoFiring bool
```

Adicionar o setter logo após `EnableShortSingleWindow`:

```go
// SetContainerCoFiring informa, para a janela corrente, se o material que
// contém este está casando na posição de containment. Quando true, o match
// atual é provavelmente a cauda do container e não uma veiculação própria —
// a confirmação em janela única fica desabilitada e vale a regra de 2 janelas.
//
// O worker recalcula isso a cada janela; a state machine não guarda entre
// janelas por design.
func (sm *StateMachine) SetContainerCoFiring(v bool) {
	sm.containerCoFiring = v
}
```

Em `shortSingleWindowConfirms`, adicionar a guarda como primeira condição:

```go
func (sm *StateMachine) shortSingleWindowConfirms(uniqueScore int) bool {
	if sm.shortSingleWindowFactor <= 0 {
		return false
	}
	// Cauda do container: o áudio está aí, mas quem tocou foi o spot.
	if sm.containerCoFiring {
		return false
	}
	if float64(sm.totalFrames)/framesPerSecond >= shortMaterialMaxSeconds {
		return false
	}
	return float64(uniqueScore) >= sm.shortSingleWindowFactor*float64(sm.minScore)
}
```

- [ ] **Step 4: Rodar e ver passar**

```bash
cd workers && go test ./internal/match -run TestShortSingleWindow -v 2>&1 | grep -E "^(--- |ok|FAIL)"
```
Esperado: todos PASS (os 5 antigos + os 3 novos).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/match/statemachine.go workers/internal/match/statemachine_shortwindow_test.go
git commit -m "feat(match): janela única desabilitada quando o container co-dispara"
```

---

### Task 6: Worker calcula o co-fire por janela

**Files:**
- Modify: `workers/internal/ingestor/worker.go` (struct `Config` e o loop de janela, ~linha 450-500)

- [ ] **Step 1: Adicionar a config**

No struct `Config` de `worker.go`, logo após `ShortSingleWindowFactor`:

```go
	// Containments mapeia short_id do material CURTO → container e posição.
	// Vazio = discriminação desligada (comportamento da fase anterior).
	// Preenchido pelo supervisor a partir de material_containments (0063).
	Containments map[int32]ContainmentInfo
```

E o tipo, junto dos outros tipos do pacote:

```go
// ContainmentInfo diz em qual material longo este curto está contido e em que
// posição dele. Ver docs/superpowers/specs/2026-08-07-containment-aware-short-material-design.md
type ContainmentInfo struct {
	ContainerShortID int32
	OffsetFrames     int
}

// containmentOffsetToleranceFrames — folga na comparação de offset. O match do
// container não aponta exatamente o mesmo frame a cada janela (bin de delta,
// jitter de alinhamento). 16 frames ≈ 2s, ordem de grandeza do hop de análise.
const containmentOffsetToleranceFrames = 16
```

- [ ] **Step 2: Calcular e propagar no loop de janela**

Em `worker.go`, no bloco que já monta `resultByID` (procurar `resultByID := make(map[int32]match.MatchResult`), **depois** de o mapa estar preenchido e **antes** do laço que chama `sm.Update(...)`, inserir:

```go
		// Discriminação de containment: para cada material curto com container
		// registrado, verificar se o container está casando NESTA janela na
		// posição onde o curto mora dentro dele. Se sim, o match do curto é a
		// cauda do container — não é veiculação própria.
		for id, sm := range machines {
			info, ok := w.cfg.Containments[id]
			if !ok {
				sm.SetContainerCoFiring(false)
				continue
			}
			cr, containerMatched := resultByID[info.ContainerShortID]
			if !containerMatched {
				sm.SetContainerCoFiring(false)
				continue
			}
			delta := cr.OffsetFrames - info.OffsetFrames
			if delta < 0 {
				delta = -delta
			}
			sm.SetContainerCoFiring(delta <= containmentOffsetToleranceFrames)
		}
```

> **Por que o offset importa:** sem ele, um curto que toque logo depois do spot no mesmo intervalo seria classificado como cauda (o container também casou na janela) e perderia a confirmação. Com ele, só conta como cauda quando o container está casando exatamente no trecho onde o curto vive.

- [ ] **Step 3: Compilar**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./... && echo "BUILD OK"
```

- [ ] **Step 4: Commit**

```bash
git add workers/internal/ingestor/worker.go
git commit -m "feat(worker): calcula co-fire do container por janela e propaga pra state machine"
```

---

### Task 7: Supervisor carrega os containments e passa ao worker

**Files:**
- Modify: `workers/internal/supervisor/supervisor.go`

- [ ] **Step 1: Campo + setter no Supervisor**

Junto de `shortSingleWindowFactor` no struct:

```go
	// containments: short_id do curto → container + offset, carregado de
	// material_containments (0063). nil = discriminação desligada.
	containments map[int32]ingestor.ContainmentInfo
```

E o setter, ao lado de `SetShortSingleWindowFactor`:

```go
// SetContainments injeta o mapa de containment usado pelos workers criados
// daqui pra frente. Chamar no boot. Como a relação é propriedade permanente do
// par de masters, não há hot-reload: material novo entra no próximo restart,
// junto com o reload de índice.
func (s *Supervisor) SetContainments(m map[int32]ingestor.ContainmentInfo) {
	s.containments = m
}
```

- [ ] **Step 2: Passar na construção do worker**

No `ingestor.Config{...}` (onde já está `ShortSingleWindowFactor: s.shortSingleWindowFactor`), acrescentar:

```go
		Containments:            s.containments,
```

- [ ] **Step 3: Compilar**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./... && echo "BUILD OK"
```

- [ ] **Step 4: Commit**

```bash
git add workers/internal/supervisor/supervisor.go
git commit -m "feat(supervisor): propaga mapa de containment pros workers"
```

---

### Task 8: Wiring da flag no boot + passthrough no compose

**Files:**
- Modify: `workers/cmd/api/main.go`
- Modify: `infra/docker/docker-compose.yml`

- [ ] **Step 1: Carregar e logar no boot**

Em `workers/cmd/api/main.go`, logo **após** o bloco que faz `sup.SetShortSingleWindowFactor(...)`:

```go
	// Discriminação de containment (spec 2026-08-07): com CONTAINMENT_DISCRIMINATION=true
	// o worker deixa de confirmar material curto em janela única quando o spot
	// que o contém está casando na mesma janela, na posição de containment.
	// Sem a flag, o comportamento é o da fase anterior (só o limiar de score).
	if os.Getenv("CONTAINMENT_DISCRIMINATION") == "true" {
		rows, err := catalog.NewMaterialContainments(pool).ListAll(ctx)
		if err != nil {
			logger.Warn("containment: carga falhou, discriminação desligada", zap.Error(err))
		} else {
			m := make(map[int32]ingestor.ContainmentInfo, len(rows))
			for _, r := range rows {
				m[r.ContainedShortID] = ingestor.ContainmentInfo{
					ContainerShortID: r.ContainerShortID,
					// 7,8125 frames/s — mesma fórmula do resto do pipeline.
					OffsetFrames: int(r.OffsetSeconds * 16000.0 / 2048.0),
				}
			}
			sup.SetContainments(m)
			logger.Info("discriminação de containment ENABLED",
				zap.Int("pares", len(m)))
		}
	}
```

`main.go` **não** importa `ingestor` hoje (verificado em 2026-08-07). Adicionar na lista de
imports, em ordem alfabética entre `"radiocheck/internal/index"` e `"radiocheck/internal/match"`:

```go
	"radiocheck/internal/ingestor"
```

- [ ] **Step 2: Passthrough no compose**

Em `infra/docker/docker-compose.yml`, logo após a linha `SHORT_SINGLE_WINDOW_FACTOR:`:

```yaml
      # Discriminação de containment: material curto não confirma em janela
      # única quando o spot que o contém está casando na mesma janela, na
      # posição de containment. Exige material_containments populada (0063 +
      # backfill-shared-hashes). Vazio = desligado.
      # Medição que motivou: 5 de 6 mortes do pulso eram cauda de spot.
      # docs/superpowers/specs/2026-08-07-containment-aware-short-material-design.md
      CONTAINMENT_DISCRIMINATION: ${CONTAINMENT_DISCRIMINATION:-false}
```

- [ ] **Step 3: Validar compose e compilar**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./... && echo "BUILD OK"
cd ../infra/docker && docker compose --env-file .env config -q && echo "COMPOSE OK"
```

- [ ] **Step 4: Commit**

```bash
git add workers/cmd/api/main.go infra/docker/docker-compose.yml
git commit -m "feat(api): flag CONTAINMENT_DISCRIMINATION + passthrough no compose"
```

---

### Task 9: Documentação da feature + gate final

**Files:**
- Create: `docs/features/containment-discrimination.md`
- Modify: `CLAUDE.md` (linha no mapa de consulta)

- [ ] **Step 1: Criar o doc**

```markdown
---
status: implementado
ultima-verificacao: 2026-08-07
codigo-relacionado:
  - workers/internal/sharing/sharing.go
  - workers/internal/catalog/material_containments.go
  - workers/internal/match/statemachine.go
  - workers/internal/ingestor/worker.go
  - migrations/0063_material_containments.up.sql
---

# Discriminação de containment (curto ⊂ spot)

**Flags:** `CONTAINMENT_DISCRIMINATION` (bool, default **false**) — depende de
`SHORT_SINGLE_WINDOW_FACTOR` estar ligada para ter efeito.
**Spec:** [2026-08-07-containment-aware-short-material-design.md](../superpowers/specs/2026-08-07-containment-aware-short-material-design.md)

## O problema

Material <10s contido num spot do mesmo cliente produz o **mesmo score** em dois cenários
opostos: o curto tocou sozinho, ou o spot tocou e o curto casou na cauda dele. A regra de janela
única ([short-material-single-window.md](short-material-single-window.md)) decide por altura de
score, e por isso erra um dos dois lados.

Medição em produção (2026-08-06/07), 6 mortes do pulso MILIUM: **5 eram cauda** (o spot casou na
mesma emissora 24-26s antes, com `audit_coverage` 0,76-0,82) e **1 era tocada real perdida**.
Baixar o limiar recuperaria a real e criaria 3 falsos positivos.

## Como funciona

1. O shared-hash scan já mede a sobreposição entre pares. Quando um lado tem <10s ele pula o
   *flagging* (correto — a defesa não se aplica), mas agora **registra a relação** em
   `material_containments`: quem contém quem, e em que segundo do container o curto começa.
2. O worker carrega o mapa no boot e, a cada janela, verifica se o container está casando com
   `OffsetFrames` dentro de ±16 frames (~2s) da posição de containment.
3. Se estiver, a state machine não confirma o curto em janela única — o match é cauda. Se não
   estiver, o curto tocou sozinho e confirma.

A checagem de **offset** é o que separa "é a cauda do spot" de "tocou logo depois do spot no
mesmo intervalo": nos dois o container casa na janela, mas em posições diferentes do master.

## Operação

```bash
# 1. popular a tabela (o scan já roda no upload; para o catálogo existente:)
docker compose exec api backfill-shared-hashes --dsn "$DATABASE_URL"

# 2. conferir
psql -c "SELECT count(*) FROM material_containments"

# 3. ligar
#    .env: CONTAINMENT_DISCRIMINATION=true
$DC build api && $DC up -d --force-recreate --no-deps api
docker logs docker-api-1 2>&1 | grep "discriminação de containment ENABLED"
```

Se a tabela estiver vazia, a flag não faz nada (não quebra — cai no comportamento anterior).

## O que olhar

- Mortes classificadas como cauda devem continuar morrendo; as "tocada real perdida" devem ir a
  zero. Método em [spec §8](../superpowers/specs/2026-08-07-containment-aware-short-material-design.md).
- Critério de parada: a query de dupla contagem (curto + longo do mesmo cliente contando em <90s).
  Baseline: zero.

## Limites

- Depende de a relação ter sido medida. Par novo só entra depois do scan.
- Não ajuda material ≥10s, por construção.
- Se o container não passar nos gates da janela, o co-fire não é detectado e o comportamento cai
  no anterior — conservador por design.
```

- [ ] **Step 2: Linha no mapa de consulta do `CLAUDE.md`**

Acrescentar na tabela "Mapa de consulta", logo após a linha de `shared-hash-detection.md`:

```markdown
| Material curto <10s contido em spot (pulso/vinheta): confirmação em janela única e discriminação de cauda | [docs/features/short-material-single-window.md](docs/features/short-material-single-window.md) + [docs/features/containment-discrimination.md](docs/features/containment-discrimination.md) |
```

- [ ] **Step 3: Gate final (CLAUDE.md §6)**

```bash
cd workers
CGO_ENABLED=0 GOOS=linux go build ./...            # tem que passar
go test ./... 2>&1 | grep -vE "^ok|no test files"  # só flaky conhecida (§6.6)
cd ..
git show master:frontend/package-lock.json | grep -c emnapi
grep -c emnapi frontend/package-lock.json          # os dois números têm que bater
git diff --name-only master..HEAD | grep "^migrations/"  # 0063 — shadow test do deploy cobre
```

- [ ] **Step 4: Commit + push**

```bash
git add docs/features/containment-discrimination.md CLAUDE.md
git commit -m "docs: feature de discriminação de containment + mapa de consulta"
git push -u origin <branch>
```

---

## Gate final antes do merge (CLAUDE.md §6)

- [ ] `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` — limpo
- [ ] `cd workers && go test ./...` — falhas só nas flaky conhecidas (§6.6) em pacotes não tocados
- [ ] Lockfile do frontend intocado (este plano não mexe em `frontend/`)
- [ ] Migration `0063` é `CREATE TABLE` puro — o `shadow_migration_test` do deploy cobre (§4.8)
- [ ] Nenhum CLI novo em `cmd/*` (§6.7 não se aplica)
- [ ] As duas flags nascem **OFF**: master fica idêntico em comportamento até alguém ligar

## Self-review (feito na escrita, 2026-08-07)

- **Cobertura da spec:** §4.1 (persistir containment) → Tasks 2-4; §4.2 (consultar na janela) →
  Tasks 5-7; §4.3 (limiar deixa de ser árbitro) → consequência, medida na sombra, sem código;
  §3.2 (dedup como pré-requisito) → Task 1; §7 itens 2-5 permanecem fora de escopo, por decisão.
- **Sem placeholders:** todo passo de código traz o código; todo comando traz o esperado.
- **Consistência de tipos:** `Containment` (pacote `sharing`, com `MatchFrames`) é o tipo do scan;
  `ContainmentRow` (pacote `catalog`, em short_ids) é o de leitura; `ContainmentInfo` (pacote
  `ingestor`, com `OffsetFrames`) é o de runtime. Os três aparecem definidos antes de serem usados.
- **Riscos resolvidos antes de fechar o plano (verificados no código em 2026-08-07):** o ciclo de
  import é **real** — `catalog/twin_discriminative.go:16` importa `sharing` —, então a escrita foi
  posta em `sharing/containment_store.go` e só a leitura ficou em `catalog`; e o helper de pool
  dos testes de `catalog` chama-se `newTestPool`.
- **Dependências entre tasks:** 4 depende de 3 (assinatura de `classifyAndFilter`); 6 depende de 5
  (setter) e 4 (tipo); 7 depende de 6; 8 depende de 4 e 7. Task 1 é independente.
