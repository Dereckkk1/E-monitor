# Similarity Overlap Timeline — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** No upload de material, mostrar **% do material novo que é igual** a outro e uma **timeline** de onde-até-onde batem; ≥50% segue bloqueante, 25–50% vira heads-up não-bloqueante.

**Architecture:** O scan de similaridade (já roda 1× pós-fingerprint) passa a capturar, por janela casada com o top match, a tripla `(ownStart, ownEnd, offset)`; agrupa por offset e funde janelas contíguas em **segmentos conectados**, salvos num JSONB novo (`materials.similarity_segments`). O frontend lê isso no polling existente e renderiza a timeline no modal bloqueante (≥50%) e num heads-up novo (25–50%).

**Tech Stack:** Go (pgx, NATS), PostgreSQL (JSONB + golang-migrate), React (wizard MaterialsStep).

**Spec:** [docs/superpowers/specs/2026-06-18-similarity-overlap-timeline-design.md](../specs/2026-06-18-similarity-overlap-timeline-design.md)

**Branch:** `feat/similarity-overlap-timeline` (já criada, empilhada em `fix/similarity-dense-audio-coverage`).

---

## Convenções compartilhadas (referência para todas as tasks)

- **Frames → segundos:** `sec = frame * 2048.0 / 16000.0` (≈ 0,128 s/frame). `2048` = `stftHopSamples`, `16000` = `fingerprint.SampleRate`.
- **Tipos Go novos** (definidos na Task 2, usados nas Tasks 3–5):
  ```go
  // matchedWindow é uma janela que casou com o top match: posição no material
  // novo (own) + offset de alinhamento (frames).
  type matchedWindow struct{ ownStart, ownEnd, offset int32 }

  // Segment é um trecho igual conectado, em FRAMES.
  type Segment struct {
      OwnFrom, OwnTo     int32
      OtherFrom, OtherTo int32
  }
  ```
- **JSON persistido** em `materials.similarity_segments` (definido na Task 4):
  ```json
  { "own_cov": 0.33, "other_cov": 0.33,
    "own_duration": 30.0, "other_duration": 30.0,
    "segments": [ {"own":[0.0,5.0],"other":[0.0,5.0]} ] }
  ```
- **Thresholds:** `PersistThreshold = 0.25` (backend grava a partir disso), `WarnThreshold = 0.50` (frontend bloqueia a partir disso — já existe).

---

## Task 1: Migration `0040_similarity_segments`

**Files:**
- Create: `migrations/0040_similarity_segments.up.sql`
- Create: `migrations/0040_similarity_segments.down.sql`

- [ ] **Step 1: Escrever a migration up**

`migrations/0040_similarity_segments.up.sql`:
```sql
-- 0040_similarity_segments.up.sql
-- Persiste os trechos iguais (segmentos conectados) entre um material e seu
-- top match, pra desenhar a timeline de sobreposição no upload (wizard).
-- Ver docs/superpowers/specs/2026-06-18-similarity-overlap-timeline-design.md.

BEGIN;

ALTER TABLE materials ADD COLUMN similarity_segments JSONB;

COMMIT;
```

- [ ] **Step 2: Escrever a migration down**

`migrations/0040_similarity_segments.down.sql`:
```sql
-- 0040_similarity_segments.down.sql
BEGIN;

ALTER TABLE materials DROP COLUMN IF EXISTS similarity_segments;

COMMIT;
```

- [ ] **Step 3: Aplicar a migration no dev e verificar a coluna**

Aplique via o fluxo padrão do projeto (ver [docs/operations/migrations.md](../../operations/migrations.md)) e confirme:

Run:
```bash
docker exec docker-postgres-1 psql -U radiocheck -d radiocheck -c "\d materials" | grep similarity_segments
```
Expected: linha mostrando `similarity_segments | jsonb`.

- [ ] **Step 4: Commit**

```bash
git add migrations/0040_similarity_segments.up.sql migrations/0040_similarity_segments.down.sql
git commit -m "feat(migration): 0040 adiciona materials.similarity_segments (JSONB)"
```

---

## Task 2: Construção dos segmentos conectados (`buildSegments`)

**Files:**
- Create: `workers/internal/similarity/segments.go`
- Test: `workers/internal/similarity/segments_test.go`

- [ ] **Step 1: Escrever o teste que falha**

`workers/internal/similarity/segments_test.go`:
```go
package similarity

import "testing"

// Janelas contíguas no mesmo offset fundem num único segmento; o lado "other"
// é deslocado pelo offset.
func TestBuildSegments_MergesContiguousSameOffset(t *testing.T) {
	ws := []matchedWindow{
		{ownStart: 0, ownEnd: 31, offset: 0},
		{ownStart: 8, ownEnd: 39, offset: 0},
		{ownStart: 16, ownEnd: 47, offset: 0},
	}
	segs := buildSegments(ws, 4)
	if len(segs) != 1 {
		t.Fatalf("esperava 1 segmento, veio %d: %+v", len(segs), segs)
	}
	if segs[0].OwnFrom != 0 || segs[0].OwnTo != 47 {
		t.Fatalf("own range errado: %+v", segs[0])
	}
	if segs[0].OtherFrom != 0 || segs[0].OtherTo != 47 {
		t.Fatalf("other range (offset 0) errado: %+v", segs[0])
	}
}

// Offsets diferentes (alinhamentos distintos) viram segmentos separados; o
// lado other = own - offset.
func TestBuildSegments_SeparatesByOffset(t *testing.T) {
	ws := []matchedWindow{
		{ownStart: 0, ownEnd: 31, offset: 0},
		{ownStart: 200, ownEnd: 231, offset: 100},
	}
	segs := buildSegments(ws, 4)
	if len(segs) != 2 {
		t.Fatalf("esperava 2 segmentos, veio %d", len(segs))
	}
	// ordenado por OwnFrom: [0]=offset0, [1]=offset100
	if segs[1].OtherFrom != 100 || segs[1].OtherTo != 131 {
		t.Fatalf("other do 2o segmento (own-offset) errado: %+v", segs[1])
	}
}

// Segmentos curtos (< minFrames) são descartados como ruído.
func TestBuildSegments_DropsShort(t *testing.T) {
	ws := []matchedWindow{{ownStart: 0, ownEnd: 3, offset: 0}}
	segs := buildSegments(ws, 4)
	if len(segs) != 0 {
		t.Fatalf("esperava 0 segmentos (curto), veio %d", len(segs))
	}
}

// other negativo é clampado em 0; se o segmento inteiro é negativo, descarta.
func TestBuildSegments_ClampsOtherToZero(t *testing.T) {
	ws := []matchedWindow{{ownStart: 0, ownEnd: 31, offset: 10}}
	segs := buildSegments(ws, 4)
	if len(segs) != 1 || segs[0].OtherFrom != 0 {
		t.Fatalf("esperava OtherFrom clampado em 0: %+v", segs)
	}
}
```

- [ ] **Step 2: Rodar o teste pra confirmar que falha**

Run: `cd workers && go test ./internal/similarity/ -run TestBuildSegments -v`
Expected: FAIL com `undefined: buildSegments` (e `matchedWindow`/`Segment`).

- [ ] **Step 3: Implementar `buildSegments` + os tipos**

`workers/internal/similarity/segments.go`:
```go
package similarity

import "sort"

// matchedWindow é uma janela que casou com o top match: posição no material
// novo (own) + offset de alinhamento, em frames.
type matchedWindow struct{ ownStart, ownEnd, offset int32 }

// Segment é um trecho igual conectado entre o material novo (own) e o
// existente (other), em FRAMES. half-open [from, to).
type Segment struct {
	OwnFrom, OwnTo     int32
	OtherFrom, OtherTo int32
}

// buildSegments agrupa as janelas casadas por offset (cada offset é um
// alinhamento próprio entre os dois materiais), funde janelas contíguas/
// sobrepostas no eixo own (via mergeRanges), e converte cada faixa fundida num
// Segment conectado (other = own - offset). Descarta segmentos com menos de
// minFrames de duração e ordena por OwnFrom.
func buildSegments(windows []matchedWindow, minFrames int32) []Segment {
	byOffset := make(map[int32][]frameRange)
	for _, w := range windows {
		byOffset[w.offset] = append(byOffset[w.offset], frameRange{w.ownStart, w.ownEnd})
	}
	var segs []Segment
	for off, rs := range byOffset {
		for _, r := range mergeRanges(rs) {
			if r.until-r.from < minFrames {
				continue
			}
			otherFrom := r.from - off
			otherTo := r.until - off
			if otherTo <= 0 {
				continue
			}
			if otherFrom < 0 {
				otherFrom = 0
			}
			segs = append(segs, Segment{r.from, r.until, otherFrom, otherTo})
		}
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].OwnFrom < segs[j].OwnFrom })
	return segs
}
```

- [ ] **Step 4: Rodar o teste pra confirmar que passa**

Run: `cd workers && go test ./internal/similarity/ -run TestBuildSegments -v`
Expected: PASS (4 testes).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/similarity/segments.go workers/internal/similarity/segments_test.go
git commit -m "feat(similarity): buildSegments agrupa janelas casadas em segmentos conectados"
```

---

## Task 3: `runScan` captura as janelas casadas do top match

**Files:**
- Modify: `workers/internal/similarity/similarity.go` (struct `pairScan`, func `runScan`)
- Test: `workers/internal/similarity/segments_test.go` (adiciona caso)

- [ ] **Step 1: Escrever o teste que falha (runScan popula `windows`)**

Adicione em `workers/internal/similarity/segments_test.go`:
```go
import "github.com/google/uuid" // (adicione ao import block existente do arquivo)

// runScan deve registrar, por par, as janelas casadas (own + offset) pra
// alimentar buildSegments. Aqui usamos um índice vazio só pra garantir que o
// campo existe e começa vazio — a cobertura real do casamento já é exercida
// pelos testes de áudio denso.
func TestPairScan_HasWindowsField(t *testing.T) {
	s := &pairScan{}
	s.windows = append(s.windows, matchedWindow{0, 31, 0})
	if len(s.windows) != 1 {
		t.Fatalf("campo windows não acumulou")
	}
}
```

- [ ] **Step 2: Rodar pra confirmar que falha**

Run: `cd workers && go test ./internal/similarity/ -run TestPairScan_HasWindowsField -v`
Expected: FAIL com `s.windows undefined (type *pairScan has no field windows)`.

- [ ] **Step 3: Adicionar o campo `windows` e popular em `runScan`**

Em `workers/internal/similarity/similarity.go`, no struct `pairScan` (perto da linha 44), adicione o campo:
```go
type pairScan struct {
	otherTotalFrames int
	ownRanges        []frameRange
	otherRanges      []frameRange
	windows          []matchedWindow // janelas casadas (own + offset) p/ segmentos
}
```

Em `runScan` (perto da linha 317, dentro do `for _, r := range results`), logo após o `s.ownRanges = append(...)`, registre a janela:
```go
			s.ownRanges = append(s.ownRanges, frameRange{ownStart, ownEnd})
			s.windows = append(s.windows, matchedWindow{ownStart, ownEnd, int32(r.OffsetFrames)})
```
(Mantenha o resto do bloco — o cálculo de `xStart/xEnd` e `s.otherRanges` — inalterado.)

- [ ] **Step 4: Rodar pra confirmar que passa (e nada quebrou)**

Run: `cd workers && go test ./internal/similarity/ -count=1`
Expected: `ok radiocheck/internal/similarity`.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/similarity/similarity.go workers/internal/similarity/segments_test.go
git commit -m "feat(similarity): runScan registra janelas casadas por par"
```

---

## Task 4: Persistir overlap (cov + durações + segmentos) e baixar o piso pra 0.25

**Files:**
- Modify: `workers/internal/similarity/similarity.go` (const, `loadClientIndex`, `CheckMaterialSimilarity`)
- Create: `workers/internal/similarity/overlap.go`
- Test: `workers/internal/similarity/overlap_test.go`

- [ ] **Step 1: Escrever o teste que falha (montagem do JSON)**

`workers/internal/similarity/overlap_test.go`:
```go
package similarity

import (
	"encoding/json"
	"testing"
)

func TestBuildOverlapJSON_ShapeAndSeconds(t *testing.T) {
	segs := []Segment{{OwnFrom: 0, OwnTo: 39, OtherFrom: 0, OtherTo: 39}}
	raw := buildOverlapJSON(0.33, 0.5, 30.0, 60.0, segs)

	var got overlapJSON
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json inválido: %v", err)
	}
	if got.OwnCov != 0.33 || got.OtherCov != 0.5 {
		t.Fatalf("cov errado: %+v", got)
	}
	if got.OwnDuration != 30.0 || got.OtherDuration != 60.0 {
		t.Fatalf("duração errada: %+v", got)
	}
	if len(got.Segments) != 1 {
		t.Fatalf("esperava 1 segmento, veio %d", len(got.Segments))
	}
	// 39 frames * 2048 / 16000 = 4.992s
	if s := got.Segments[0].Own[1]; s < 4.9 || s > 5.1 {
		t.Fatalf("own[1] em segundos errado: %v", s)
	}
}
```

- [ ] **Step 2: Rodar pra confirmar que falha**

Run: `cd workers && go test ./internal/similarity/ -run TestBuildOverlapJSON -v`
Expected: FAIL com `undefined: buildOverlapJSON` / `overlapJSON`.

- [ ] **Step 3: Implementar `overlap.go`**

`workers/internal/similarity/overlap.go`:
```go
package similarity

import "encoding/json"

// framesToSec converte frames do matcher (stftHop=2048 @ 16kHz) em segundos.
const framesToSec = 2048.0 / 16000.0

type segmentJSON struct {
	Own   [2]float64 `json:"own"`
	Other [2]float64 `json:"other"`
}

// overlapJSON é o conteúdo de materials.similarity_segments. Tudo em segundos.
type overlapJSON struct {
	OwnCov        float64       `json:"own_cov"`
	OtherCov      float64       `json:"other_cov"`
	OwnDuration   float64       `json:"own_duration"`
	OtherDuration float64       `json:"other_duration"`
	Segments      []segmentJSON `json:"segments"`
}

// buildOverlapJSON monta o JSON persistido, convertendo segmentos de frames
// para segundos.
func buildOverlapJSON(ownCov, otherCov, ownDur, otherDur float64, segs []Segment) json.RawMessage {
	out := overlapJSON{
		OwnCov:        ownCov,
		OtherCov:      otherCov,
		OwnDuration:   ownDur,
		OtherDuration: otherDur,
		Segments:      make([]segmentJSON, 0, len(segs)),
	}
	for _, s := range segs {
		out.Segments = append(out.Segments, segmentJSON{
			Own:   [2]float64{float64(s.OwnFrom) * framesToSec, float64(s.OwnTo) * framesToSec},
			Other: [2]float64{float64(s.OtherFrom) * framesToSec, float64(s.OtherTo) * framesToSec},
		})
	}
	b, _ := json.Marshal(out) // overlapJSON é sempre serializável
	return b
}

// coverages recalcula ownCov/otherCov do top match (mesma fórmula do
// pickTopMatch), clampados em [0,1].
func coverages(s *pairScan, ownTotalFrames int) (ownCov, otherCov float64) {
	if ownTotalFrames > 0 {
		ownCov = float64(frameCoverage(s.ownRanges)) / float64(ownTotalFrames)
	}
	if s.otherTotalFrames > 0 {
		otherCov = float64(frameCoverage(s.otherRanges)) / float64(s.otherTotalFrames)
	}
	if ownCov > 1.0 {
		ownCov = 1.0
	}
	if otherCov > 1.0 {
		otherCov = 1.0
	}
	return ownCov, otherCov
}
```

- [ ] **Step 4: Rodar pra confirmar que passa**

Run: `cd workers && go test ./internal/similarity/ -run TestBuildOverlapJSON -v`
Expected: PASS.

- [ ] **Step 5: `loadClientIndex` passa a devolver a duração por material**

Em `workers/internal/similarity/similarity.go`, mude a assinatura e o corpo de `loadClientIndex` pra também retornar `durationByID map[uuid.UUID]float64`:

Assinatura (linha ~228):
```go
func loadClientIndex(
	ctx context.Context,
	pool *pgxpool.Pool,
	clientID uuid.UUID,
	selfID uuid.UUID,
) (index.Index, map[int32]uuid.UUID, map[uuid.UUID]int, map[uuid.UUID]float64, error) {
```
No corpo: crie `durationByID := make(map[uuid.UUID]float64)`, preencha `durationByID[matID] = durationSec` dentro do loop (junto de `totalFramesByID[matID] = ...`), e retorne os 5 valores (inclua `durationByID` antes do `rows.Err()`). Atualize os 4 `return nil, nil, nil, nil, err` de erro pra ter 5 nils + err.

No chamador `CheckMaterialSimilarity` (linha ~164), capture o novo retorno:
```go
	idx, shortToID, totalFramesByID, durationByID, err := loadClientIndex(ctx, pool, clientID, materialID)
```

- [ ] **Step 6: `CheckMaterialSimilarity` busca a duração própria, baixa o piso pra 0.25 e grava segmentos**

(a) Adicione a constante perto de `WarnThreshold` (linha ~35):
```go
	// PersistThreshold é o piso pra GRAVAR um match + segmentos. Abaixo disso
	// é ruído e a linha fica limpa. O bloqueio (modal) continua no
	// WarnThreshold (0.50) — decidido no frontend; entre 0.25 e 0.50 o
	// frontend mostra um heads-up não-bloqueante.
	PersistThreshold = 0.25
```

(b) Na query inicial (linha ~146), inclua `duration_seconds`:
```go
	var clientID uuid.UUID
	var masterPath string
	var fpStatus string
	var ownDuration float64
	if err := pool.QueryRow(ctx, `
		SELECT client_id, master_storage_path, fingerprint_status, duration_seconds
		FROM materials WHERE id = $1
	`, materialID).Scan(&clientID, &masterPath, &fpStatus, &ownDuration); err != nil {
		return fmt.Errorf("similarity: lookup material: %w", err)
	}
```

(c) Substitua o bloco final (linha ~191, do `topID, score := pickTopMatch(report)` até o último `return err`) por:
```go
	topID, score := pickTopMatch(report)
	if score < PersistThreshold {
		_, err := pool.Exec(ctx, `
			UPDATE materials
			SET similarity_check_status = 'ready',
			    most_similar_material_id = NULL,
			    similarity_score = NULL,
			    similarity_segments = NULL
			WHERE id = $1
		`, materialID)
		return err
	}

	top := report.perOther[topID]
	ownCov, otherCov := coverages(top, report.ownTotalFrames)
	segs := buildSegments(top.windows, 4) // ~0,5s mínimo
	overlap := buildOverlapJSON(ownCov, otherCov, ownDuration, durationByID[topID], segs)

	_, err = pool.Exec(ctx, `
		UPDATE materials
		SET similarity_check_status = 'ready',
		    most_similar_material_id = $2,
		    similarity_score = $3,
		    similarity_segments = $4
		WHERE id = $1
	`, materialID, topID, score, overlap)
	return err
```

- [ ] **Step 7: Rodar a suíte inteira do pacote**

Run: `cd workers && go build ./... && go test ./internal/similarity/ -count=1`
Expected: `ok radiocheck/internal/similarity` (incl. dense_audio_test continua passando — ruído denso fica < 0.25 → segmentos não são gravados).

- [ ] **Step 8: Commit**

```bash
git add workers/internal/similarity/similarity.go workers/internal/similarity/overlap.go workers/internal/similarity/overlap_test.go
git commit -m "feat(similarity): persiste segmentos + cov + durações; piso de persistência 0.25"
```

---

## Task 5: Expor `similarity_segments` na API de materiais

**Files:**
- Modify: `workers/internal/catalog/materials.go` (struct `Material`, `materialColumns`, `scanMaterial`)

- [ ] **Step 1: Adicionar o campo ao struct**

Em `workers/internal/catalog/materials.go`, no struct `Material` (após `SimilarityAckdAt`, linha ~30):
```go
	SimilarityAckdAt       *time.Time      `json:"similarity_acknowledged_at,omitempty"`
	SimilaritySegments     json.RawMessage `json:"similarity_segments,omitempty"`
```
Adicione `"encoding/json"` ao import block.

- [ ] **Step 2: Incluir a coluna no SELECT**

Em `materialColumns` (linha ~62), adicione `similarity_segments` após `similarity_acknowledged_at`:
```go
const materialColumns = `id, short_id, client_id, title, type_id, duration_seconds,
       master_storage_path, master_sha256, fingerprint_status,
       fingerprint_generated_at, fingerprint_hash_count,
       similarity_check_status, most_similar_material_id, similarity_score,
       similarity_acknowledged_at, similarity_segments, script, created_at, updated_at`
```

- [ ] **Step 3: Escanear a coluna**

Em `scanMaterial` (linha ~71), adicione `&m.SimilaritySegments` na ordem certa (após `&m.SimilarityAckdAt`):
```go
	return row.Scan(&m.ID, &m.ShortID, &m.ClientID, &m.Title, &m.TypeID,
		&m.DurationSeconds, &m.MasterStoragePath, &m.MasterSHA256,
		&m.FingerprintStatus, &m.FingerprintGeneratedAt, &m.FingerprintHashCount,
		&m.SimilarityCheckStatus, &m.MostSimilarMaterialID, &m.SimilarityScore,
		&m.SimilarityAckdAt, &m.SimilaritySegments, &m.Script, &m.CreatedAt, &m.UpdatedAt)
```

- [ ] **Step 4: Compilar + rodar testes do catalog**

Run: `cd workers && go build ./... && go test ./internal/catalog/ -count=1`
Expected: build OK; testes do catalog passam (ou "no test files" — aceitável).

- [ ] **Step 5: Verificação manual end-to-end (dev)**

Rebuild + recreate a api (pega o binário novo) e re-dispare um scan, então confira o JSON:
```bash
docker compose -f infra/docker/docker-compose.yml build api
docker compose -f infra/docker/docker-compose.yml up -d --force-recreate --no-deps api
# (após subir 2 materiais com overlap e o scan rodar)
docker exec docker-postgres-1 psql -U radiocheck -d radiocheck -c \
  "SELECT title, ROUND((similarity_score*100)::numeric,1) pct, similarity_segments->'segments' FROM materials WHERE similarity_segments IS NOT NULL;"
```
Expected: linhas com `pct` e um array de segmentos `[{"own":[...],"other":[...]}]`.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/catalog/materials.go
git commit -m "feat(materials): API devolve similarity_segments"
```

---

## Task 6: Componente `SimilarityTimeline.jsx`

**Files:**
- Create: `frontend/src/components/SimilarityTimeline.jsx`

- [ ] **Step 1: Criar o componente**

`frontend/src/components/SimilarityTimeline.jsx`:
```jsx
/**
 * Timeline de sobreposição entre dois materiais. Desenha duas barras (novo em
 * cima, existente embaixo) na escala da duração de cada um, com os trechos
 * iguais em verde. Lê `data` = materials.similarity_segments:
 *   { own_cov, other_cov, own_duration, other_duration, segments:[{own:[a,b],other:[c,d]}] }
 */
export default function SimilarityTimeline({ newTitle, otherTitle, data }) {
  if (!data || !Array.isArray(data.segments)) return null
  const ownDur = data.own_duration || 1
  const otherDur = data.other_duration || 1

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14, margin: '4px 0 2px' }}>
      <Track
        label={`🆕 ${newTitle ?? 'Material novo'}`}
        duration={ownDur}
        segments={data.segments.map(s => s.own)}
        accent="var(--c-action)"
      />
      <Track
        label={`📁 ${otherTitle ?? 'Já existente'}`}
        duration={otherDur}
        segments={data.segments.map(s => s.other)}
        accent="var(--c-text-2)"
      />
      <div style={{ display: 'flex', gap: 18, fontSize: 11, color: 'var(--c-text-3)' }}>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <i style={{ width: 12, height: 12, borderRadius: 3, background: '#10b981' }} /> Trecho igual
        </span>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <i style={{ width: 12, height: 12, borderRadius: 3, background: 'repeating-linear-gradient(45deg,#f4f4fa,#f4f4fa 3px,#ececf4 3px,#ececf4 6px)', border: '1px solid #e2e2ec' }} /> Diferente
        </span>
      </div>
    </div>
  )
}

function fmt(secs) {
  const m = Math.floor(secs / 60)
  const s = Math.round(secs % 60)
  return `${m}:${String(s).padStart(2, '0')}`
}

function Track({ label, duration, segments, accent }) {
  return (
    <div>
      <div style={{
        display: 'flex', justifyContent: 'space-between',
        fontSize: 11.5, fontWeight: 700, color: 'var(--c-text)', marginBottom: 6,
      }}>
        <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 280 }}>{label}</span>
        <span style={{ color: 'var(--c-text-3)', fontWeight: 600 }}>{fmt(duration)}</span>
      </div>
      <div style={{
        position: 'relative', height: 30, borderRadius: 7, overflow: 'hidden',
        background: 'repeating-linear-gradient(45deg,#f4f4fa,#f4f4fa 5px,#ececf4 5px,#ececf4 10px)',
      }}>
        {segments.map(([from, to], i) => {
          const left = Math.max(0, Math.min(100, (from / duration) * 100))
          const width = Math.max(0.5, Math.min(100 - left, ((to - from) / duration) * 100))
          return (
            <div key={i} title={`${fmt(from)}–${fmt(to)}`} style={{
              position: 'absolute', top: 0, bottom: 0,
              left: `${left}%`, width: `${width}%`,
              background: 'linear-gradient(180deg,#34d399,#10b981)',
              boxShadow: 'inset 0 0 0 1px rgba(255,255,255,.35)',
            }} />
          )
        })}
      </div>
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 9.5, color: 'var(--c-text-3)', marginTop: 4 }}>
        <span>0:00</span><span>{fmt(duration)}</span>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Verificar que compila (typecheck/build)**

Run: `cd frontend && npm run build`
Expected: build conclui sem erro de sintaxe/import. (Não rode `npm install` — ver CLAUDE.md §5.)

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/SimilarityTimeline.jsx
git commit -m "feat(frontend): componente SimilarityTimeline (duas barras + trechos iguais)"
```

---

## Task 7: Embutir a timeline no modal bloqueante (≥50%)

**Files:**
- Modify: `frontend/src/components/SimilarityWarningModal.jsx`

- [ ] **Step 1: Importar a timeline e ajustar o headline pra `own_cov`**

No topo de `frontend/src/components/SimilarityWarningModal.jsx`, após os imports existentes:
```jsx
import SimilarityTimeline from './SimilarityTimeline'
```
Troque o cálculo do `pct` (linha ~33) pra preferir `own_cov` quando houver segmentos:
```jsx
  const seg = newMaterial.similarity_segments
  const pct = Math.round(((seg?.own_cov ?? newMaterial.similarity_score) ?? 0) * 100)
```

- [ ] **Step 2: Renderizar a timeline acima dos cards de comparação**

Logo antes do bloco `{/* ── Body: side-by-side comparison cards ── */}` (linha ~155), insira:
```jsx
        {seg && (
          <div style={{ padding: '0 32px 8px' }}>
            <SimilarityTimeline
              newTitle={newMaterial.title}
              otherTitle={similarMaterial.title}
              data={seg}
            />
          </div>
        )}
```

- [ ] **Step 3: Build**

Run: `cd frontend && npm run build`
Expected: build OK.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/SimilarityWarningModal.jsx
git commit -m "feat(frontend): timeline embutida no modal de duplicata"
```

---

## Task 8: Componente `SimilarityHeadsUp.jsx` (não-bloqueante, 25–50%)

**Files:**
- Create: `frontend/src/components/SimilarityHeadsUp.jsx`

- [ ] **Step 1: Criar o componente**

`frontend/src/components/SimilarityHeadsUp.jsx`:
```jsx
import SimilarityTimeline from './SimilarityTimeline'

/**
 * Aviso NÃO-bloqueante de sobreposição parcial (25–50%) mostrado no upload.
 * Diferente do SimilarityWarningModal: uma única ação ("Entendi, seguir") que
 * sempre prossegue (nunca remove). Backdrop e ESC fecham normalmente.
 */
export default function SimilarityHeadsUp({ newMaterial, similarMaterial, onContinue }) {
  const seg = newMaterial.similarity_segments
  const pct = Math.round(((seg?.own_cov ?? newMaterial.similarity_score) ?? 0) * 100)

  return (
    <div
      role="dialog" aria-modal="true"
      onClick={onContinue}
      style={{
        position: 'fixed', inset: 0, background: 'rgba(6,5,91,0.45)',
        backdropFilter: 'blur(6px)', zIndex: 80,
        display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 24,
      }}
    >
      <div
        onClick={e => e.stopPropagation()}
        style={{
          background: 'var(--c-surface)', borderRadius: 'var(--radius-lg)',
          width: 'min(680px, 100%)', boxShadow: '0 30px 60px -15px rgba(6,5,91,0.4)',
          overflow: 'hidden',
        }}
      >
        <header style={{ padding: '24px 28px 16px' }}>
          <div style={{
            display: 'inline-flex', alignItems: 'center', gap: 7, padding: '4px 11px',
            background: '#e7f0ff', color: '#1d5fd0', borderRadius: 'var(--radius-full)',
            fontSize: 10.5, fontWeight: 800, letterSpacing: '0.12em', textTransform: 'uppercase',
            fontFamily: 'var(--font-heading)',
          }}>ℹ Só pra te avisar</div>
          <h2 style={{
            margin: '12px 0 0', fontFamily: 'var(--font-heading)', fontWeight: 700,
            fontSize: 20, color: 'var(--c-text)',
          }}>
            <span style={{ color: '#1d5fd0' }}>{pct}%</span> do material novo é igual a um já existente
          </h2>
          <p style={{ margin: '8px 0 0', color: 'var(--c-text-2)', fontSize: 13, lineHeight: 1.5 }}>
            Pode ser proposital (vinheta/abertura compartilhada). Não trava nada — é só pra você saber.
          </p>
        </header>
        <div style={{ padding: '4px 28px 20px' }}>
          <SimilarityTimeline newTitle={newMaterial.title} otherTitle={similarMaterial.title} data={seg} />
        </div>
        <footer style={{
          padding: '16px 28px', background: 'var(--c-bg)', borderTop: '1px solid var(--c-border)',
          display: 'flex', justifyContent: 'flex-end',
        }}>
          <button
            type="button" onClick={onContinue}
            style={{
              padding: '10px 18px', borderRadius: 'var(--radius-md)',
              background: '#1d5fd0', color: '#fff', border: 0, cursor: 'pointer',
              fontSize: 13, fontWeight: 700, fontFamily: 'var(--font-heading)',
            }}
          >
            Entendi, seguir
          </button>
        </footer>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Build**

Run: `cd frontend && npm run build`
Expected: build OK.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/SimilarityHeadsUp.jsx
git commit -m "feat(frontend): SimilarityHeadsUp (aviso não-bloqueante 25-50%)"
```

---

## Task 9: Disparar o heads-up no fluxo de upload (`MaterialsStep`)

**Files:**
- Modify: `frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx`

- [ ] **Step 1: Importar o heads-up**

Após a linha 10 (`import SimilarityWarningModal ...`):
```jsx
import SimilarityHeadsUp from '../../components/SimilarityHeadsUp'
```

- [ ] **Step 2: Generalizar `pendingDecision` com `kind` e tratar o heads-up no `submitUploads`**

Em `submitUploads` (dentro de `AddMaterialPanel`), substitua o bloco do passo 4 (linhas ~1238–1273, do comentário `// 4. If similarity ≥ threshold...` até o fim do `if (needsDecision) {...}`) por:
```jsx
        // 4. Decisão por faixa de similaridade:
        //    ≥0.50 → modal bloqueante (manter/remover)
        //    0.25–0.50 → heads-up não-bloqueante (segue sempre)
        const score = verified.similarity_check_status === 'ready' ? (verified.similarity_score ?? 0) : 0
        const hasMatch = !verified.similarity_acknowledged_at && verified.most_similar_material_id
        const kind = hasMatch && score >= 0.50 ? 'blocking'
          : hasMatch && score >= 0.25 ? 'headsup'
          : null

        if (kind) {
          let similar = null
          try {
            const r = await api.get(`/materials/${verified.most_similar_material_id}`)
            similar = r.data
          } catch {
            similar = null
          }
          if (similar) {
            setEntryStage(entry.key, 'deciding')
            const decision = await new Promise((resolve) => {
              setPendingDecision({ kind, newMaterial: verified, similarMaterial: similar, resolve })
            })
            setPendingDecision(null)
            if (decision === 'removed') {
              setEntryStage(entry.key, 'removed')
              continue
            }
            // 'kept' (modal) ou 'continue' (heads-up) → segue pro link
          }
        }
```

- [ ] **Step 3: Renderizar o componente certo conforme `kind`**

Substitua o bloco do modal bloqueante (linhas ~1543–1551) por:
```jsx
      {/* ── Similaridade: bloqueante (≥50%) ou heads-up (25–50%) ── */}
      {pendingDecision && pendingDecision.kind === 'blocking' && (
        <SimilarityWarningModal
          newMaterial={pendingDecision.newMaterial}
          similarMaterial={pendingDecision.similarMaterial}
          onKept={() => pendingDecision.resolve('kept')}
          onRemoved={() => pendingDecision.resolve('removed')}
        />
      )}
      {pendingDecision && pendingDecision.kind === 'headsup' && (
        <SimilarityHeadsUp
          newMaterial={pendingDecision.newMaterial}
          similarMaterial={pendingDecision.similarMaterial}
          onContinue={() => pendingDecision.resolve('continue')}
        />
      )}
```

- [ ] **Step 4: Build**

Run: `cd frontend && npm run build`
Expected: build OK.

- [ ] **Step 5: Verificação manual (dev)**

Com a api e o frontend rodando: suba dois materiais com ~30% de overlap (ex.: dois cortes do mesmo master com miolo trocado) → deve aparecer o **heads-up azul** com a timeline e um único botão "Entendi, seguir", e o material é vinculado ao seguir. Suba um corte ≥50% → **modal bloqueante** com a timeline.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx
git commit -m "feat(wizard): heads-up de sobreposição parcial (25-50%) no upload"
```

---

## Task 10: Documentação

**Files:**
- Modify: `docs/features/material-similarity-warning.md`

- [ ] **Step 1: Atualizar a doc da feature**

Em `docs/features/material-similarity-warning.md`:
- No header YAML: bump `ultima-verificacao: 2026-06-18` e adicione aos `codigo-relacionado`:
  `workers/internal/similarity/segments.go`, `workers/internal/similarity/overlap.go`,
  `migrations/0040_similarity_segments.up.sql`,
  `frontend/src/components/SimilarityTimeline.jsx`, `frontend/src/components/SimilarityHeadsUp.jsx`.
- Adicione uma seção "## Timeline de sobreposição (2026-06-18)" descrevendo: segmentos conectados persistidos em `similarity_segments`, headline = `own_cov`, piso de persistência 0.25, e os dois estados no upload (≥50% bloqueante, 25–50% heads-up). Referencie o spec.

- [ ] **Step 2: Commit**

```bash
git add docs/features/material-similarity-warning.md
git commit -m "docs(similarity): documenta timeline de sobreposição + thresholds"
```

---

## Ordem de execução / dependências

1 → 2 → 3 → 4 → 5 (backend, em ordem; 5 depende de 4). 6 → 7, 8 → 9 (frontend; 7/8/9 dependem de 6; 9 depende de 8). 10 por último. Backend (1–5) e frontend (6–9) podem ir em paralelo **depois** que a forma do JSON estiver fixada (Task 4).

## Notas para o executor

- **Não rodar `npm install`** em `frontend/` no Windows (CLAUDE.md §5 — poda o lockfile e quebra o CF Pages). `npm run build` é seguro.
- **Deploy de api exige recreate** antes de `exec` (CLAUDE.md §4.2): `build` → `up -d --force-recreate --no-deps api`.
- O `dense_audio_test.go` (do bugfix) é a guarda anti-ruído: continua garantindo que áudio não-relacionado fica < 0.25 e portanto **sem** `similarity_segments`.
- `similarity_score` persistido continua sendo `max(ownCov,otherCov)`; o headline visual usa `own_cov` do JSON.
