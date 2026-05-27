# Geocoding de emissoras por cidade + UF — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preencher `latitude`/`longitude` de toda emissora a partir de `city`+`state` (UF), automaticamente no cadastro/edição e via comando de backfill para as existentes — usando um dataset IBGE embutido, sem API externa.

**Architecture:** Um pacote `geo` carrega, via `go:embed`, um CSV dos ~5.570 municípios brasileiros num índice em memória (chave `(nome_normalizado, UF)`) e expõe `Lookup(city, state) → (lat, lng, ok)`. O repositório `Stations` consome um `*geo.Geocoder` singleton (`geo.Default()`) e grava as coordenadas no `Create`/`Update`. Um comando `backfill-geocoding` reprocessa as emissoras já cadastradas. Sem migration (colunas já existem desde a 0002).

**Tech Stack:** Go 1.26, pgx v5, `encoding/csv`, `golang.org/x/text` (remoção de acento — já no módulo), `go:embed`. Dataset: `kelvins/municipios-brasileiros` (base IBGE).

**Spec:** [docs/superpowers/specs/2026-05-27-geocoding-emissoras-design.md](../specs/2026-05-27-geocoding-emissoras-design.md)

---

## Estrutura de arquivos

| Arquivo | Responsabilidade |
|---------|------------------|
| `workers/internal/geo/data/municipios.csv` (criar) | Dataset IBGE embutido (nome, lat, long, codigo_uf) |
| `workers/internal/geo/geo.go` (criar) | `Geocoder`, embed, `normalize`, `New`, `Lookup`, `Default` |
| `workers/internal/geo/geo_test.go` (criar) | Testes unitários (sem DB) |
| `workers/internal/catalog/stations.go` (modificar) | Struct `Stations` + `NewStations` + `Create` + `Update` |
| `workers/internal/catalog/stations_test.go` (modificar) | Testes de geocoding em Create/Update (DB-gated) |
| `workers/cmd/backfill-geocoding/main.go` (criar) | Comando one-shot de backfill |
| `infra/docker/Dockerfiles/workers.Dockerfile` (modificar) | Build + COPY do novo binário |
| `docs/features/geocoding-emissoras.md` (criar) | Documentação operacional da feature |
| `docs/README.md` + `CLAUDE.md` (modificar) | Índice / mapa de consulta |

**Nota de wiring:** `NewStations(pool *pgxpool.Pool)` **não muda de assinatura** — é chamada em ~8 lugares (produção + testes). Em vez de injetar o geocoder, o construtor pega o singleton `geo.Default()`. O dataset é estático e embutido, então um singleton carregado uma vez (`sync.Once`) é seguro e os testes existentes continuam compilando sem alteração.

---

### Task 1: Baixar e commitar o dataset IBGE

**Files:**
- Create: `workers/internal/geo/data/municipios.csv`

- [ ] **Step 1: Criar a pasta e baixar o CSV**

O dataset é o `csv/municipios.csv` do repositório público `kelvins/municipios-brasileiros` (colunas: `codigo_ibge,nome,latitude,longitude,capital,codigo_uf,siafi_id,ddd,fuso_horario`, base IBGE). Requer acesso à rede nesta etapa.

```powershell
New-Item -ItemType Directory -Force workers/internal/geo/data
curl.exe -L -o workers/internal/geo/data/municipios.csv https://raw.githubusercontent.com/kelvins/municipios-brasileiros/main/csv/municipios.csv
```

- [ ] **Step 2: Conferir o arquivo**

```powershell
(Get-Content workers/internal/geo/data/municipios.csv | Measure-Object -Line).Lines
Get-Content workers/internal/geo/data/municipios.csv -TotalCount 3
```

Esperado: ~5.571 linhas (5.570 municípios + cabeçalho). A 1ª linha deve conter o cabeçalho `codigo_ibge,nome,latitude,longitude,...`; as seguintes, cidades com acento corretas em UTF-8 (ex.: `...,São Paulo,-23.5475,-46.6361,...`).

> Se a URL acima estiver indisponível, qualquer CSV com colunas `nome`, `latitude`, `longitude` e `codigo_uf` (código IBGE numérico do estado) serve — o loader valida os cabeçalhos na Task 2.

- [ ] **Step 3: Commit**

```powershell
git add workers/internal/geo/data/municipios.csv
git commit -m "feat(geo): dataset IBGE de municípios (lat/long por cidade+UF)"
```

---

### Task 2: Pacote `geo` — normalize, Lookup, New, Default (TDD)

**Files:**
- Create: `workers/internal/geo/geo.go`
- Test: `workers/internal/geo/geo_test.go`

- [ ] **Step 1: Escrever os testes (vão falhar)**

Crie `workers/internal/geo/geo_test.go`:

```go
package geo

import (
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"São Paulo":   "sao paulo",
		"SÃO PAULO":   "sao paulo",
		"  São  Paulo ": "sao paulo",
		"Mogi-Mirim":  "mogi-mirim",
		"Brasília":    "brasilia",
	}
	for in, want := range cases {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLookup_KnownCity(t *testing.T) {
	g, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lat, lng, ok := g.Lookup("São Paulo", "SP")
	if !ok {
		t.Fatal("São Paulo/SP not found")
	}
	if lat < -24 || lat > -23 || lng < -47 || lng > -46 {
		t.Errorf("São Paulo coords out of expected box: lat=%v lng=%v", lat, lng)
	}
}

func TestLookup_AccentAndCaseInsensitive(t *testing.T) {
	g, _ := New()
	want1, want2, _ := g.Lookup("São Paulo", "SP")
	for _, variant := range []string{"sao paulo", "SÃO PAULO", " São  Paulo "} {
		lat, lng, ok := g.Lookup(variant, "sp")
		if !ok || lat != want1 || lng != want2 {
			t.Errorf("Lookup(%q,\"sp\") = (%v,%v,%v), want (%v,%v,true)", variant, lat, lng, ok, want1, want2)
		}
	}
}

func TestLookup_HomonymResolvedByUF(t *testing.T) {
	g, _ := New()
	latMS, lngMS, okMS := g.Lookup("Bonito", "MS")
	latPE, lngPE, okPE := g.Lookup("Bonito", "PE")
	if !okMS || !okPE {
		t.Fatalf("Bonito missing: MS=%v PE=%v", okMS, okPE)
	}
	if latMS == latPE && lngMS == lngPE {
		t.Error("Bonito/MS and Bonito/PE resolved to the same coords")
	}
}

func TestLookup_NotFound(t *testing.T) {
	g, _ := New()
	if _, _, ok := g.Lookup("Cidade Que Nao Existe XYZ", "SP"); ok {
		t.Error("unexpected hit for nonexistent city")
	}
	if _, _, ok := g.Lookup("", "SP"); ok {
		t.Error("empty city should not match")
	}
	if _, _, ok := g.Lookup("São Paulo", ""); ok {
		t.Error("empty state should not match")
	}
}

func TestDataset_Sanity(t *testing.T) {
	g, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(g.byKey) < 5000 || len(g.byKey) > 6000 {
		t.Errorf("unexpected dataset size: %d", len(g.byKey))
	}
	ufs := map[string]bool{}
	for k, c := range g.byKey {
		// k = "<nome>|<UF>"
		uf := k[len(k)-2:]
		ufs[uf] = true
		if c.Lat < -34 || c.Lat > 6 || c.Lng < -74 || c.Lng > -34 {
			t.Errorf("coord out of Brazil bbox for key %q: %+v", k, c)
		}
	}
	if len(ufs) != 27 {
		t.Errorf("expected 27 UFs, got %d", len(ufs))
	}
}
```

- [ ] **Step 2: Rodar os testes — devem falhar**

Run: `cd workers && go test ./internal/geo/...`
Expected: FAIL — `geo.go` não existe (`undefined: normalize`, `undefined: New`).

- [ ] **Step 3: Implementar `geo.go`**

Crie `workers/internal/geo/geo.go`:

```go
// Package geo resolves a Brazilian city + UF (state abbreviation) to the
// latitude/longitude of the municipality's centroid, using an embedded IBGE
// dataset. No network calls: the lookup is an in-memory map built once from a
// CSV compiled into the binary. Because we only ever have city+UF (never a
// street address), municipality-centroid precision is the best achievable.
package geo

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

//go:embed data/municipios.csv
var municipiosCSV []byte

type coord struct{ Lat, Lng float64 }

// Geocoder holds an in-memory index keyed by "<normalized name>|<UF>".
type Geocoder struct {
	byKey map[string]coord
}

// ufByCode maps the numeric IBGE state code to the 2-letter UF abbreviation.
// Fixed table — the 27 federative units don't change.
var ufByCode = map[string]string{
	"11": "RO", "12": "AC", "13": "AM", "14": "RR", "15": "PA", "16": "AP", "17": "TO",
	"21": "MA", "22": "PI", "23": "CE", "24": "RN", "25": "PB", "26": "PE", "27": "AL", "28": "SE", "29": "BA",
	"31": "MG", "32": "ES", "33": "RJ", "35": "SP",
	"41": "PR", "42": "SC", "43": "RS",
	"50": "MS", "51": "MT", "52": "GO", "53": "DF",
}

func key(normName, uf string) string { return normName + "|" + uf }

// normalize lowercases, strips diacritics, trims, and collapses internal
// whitespace so "São  Paulo", "sao paulo" and "SÃO PAULO" map to one key.
func normalize(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	out = strings.ToLower(strings.TrimSpace(out))
	out = strings.Join(strings.Fields(out), " ")
	return out
}

// New builds a Geocoder from the embedded dataset. Returns an error only if the
// embedded CSV is malformed — a build-time error, not a runtime one.
func New() (*Geocoder, error) {
	r := csv.NewReader(bytes.NewReader(municipiosCSV))
	r.FieldsPerRecord = -1 // tolerate trailing columns we don't read
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("geo: read header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimSpace(strings.ToLower(h))] = i
	}
	iName, okN := col["nome"]
	iLat, okLa := col["latitude"]
	iLng, okLo := col["longitude"]
	iUF, okU := col["codigo_uf"]
	if !okN || !okLa || !okLo || !okU {
		return nil, fmt.Errorf("geo: dataset missing required columns (have %v)", header)
	}
	g := &Geocoder{byKey: make(map[string]coord, 6000)}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("geo: read row: %w", err)
		}
		uf := ufByCode[strings.TrimSpace(rec[iUF])]
		if uf == "" {
			continue // unknown/blank state code → skip defensively
		}
		lat, err1 := strconv.ParseFloat(strings.TrimSpace(rec[iLat]), 64)
		lng, err2 := strconv.ParseFloat(strings.TrimSpace(rec[iLng]), 64)
		if err1 != nil || err2 != nil {
			continue
		}
		g.byKey[key(normalize(rec[iName]), uf)] = coord{Lat: lat, Lng: lng}
	}
	if len(g.byKey) == 0 {
		return nil, fmt.Errorf("geo: empty dataset")
	}
	return g, nil
}

// Lookup returns the municipality centroid for city+state, or ok=false if the
// pair isn't in the dataset. Applies the same normalization used at index time.
func (g *Geocoder) Lookup(city, state string) (lat, lng float64, ok bool) {
	city = strings.TrimSpace(city)
	state = strings.ToUpper(strings.TrimSpace(state))
	if city == "" || state == "" {
		return 0, 0, false
	}
	c, ok := g.byKey[key(normalize(city), state)]
	if !ok {
		return 0, 0, false
	}
	return c.Lat, c.Lng, true
}

var (
	defaultOnce sync.Once
	defaultGeo  *Geocoder
)

// Default returns a process-wide Geocoder, loaded once. It panics if the
// embedded dataset fails to load: that can only happen with a corrupt build,
// which the package tests catch before merge — so failing loudly at startup is
// preferable to silently disabling geocoding in production.
func Default() *Geocoder {
	defaultOnce.Do(func() {
		g, err := New()
		if err != nil {
			panic("geo: load embedded dataset: " + err.Error())
		}
		defaultGeo = g
	})
	return defaultGeo
}
```

- [ ] **Step 4: Resolver dependências (promove `x/text` para direta)**

Run: `cd workers && go mod tidy`
Expected: `golang.org/x/text` deixa de ser `// indirect` no `go.mod`; `go.sum` atualizado.

- [ ] **Step 5: Rodar os testes — devem passar**

Run: `cd workers && go test ./internal/geo/...`
Expected: PASS (todos os testes da Task 2).

- [ ] **Step 6: Commit**

```powershell
git add workers/internal/geo/geo.go workers/internal/geo/geo_test.go workers/go.mod workers/go.sum
git commit -m "feat(geo): geocoder cidade+UF via dataset IBGE embutido"
```

---

### Task 3: Geocodar no `Stations.Create` (TDD)

**Files:**
- Modify: `workers/internal/catalog/stations.go` (struct `Stations` ~97-103, `Create` ~265-300)
- Test: `workers/internal/catalog/stations_test.go`

- [ ] **Step 1: Escrever o teste (vai falhar)**

Adicione ao final de `workers/internal/catalog/stations_test.go`:

```go
func TestStations_Create_Geocodes(t *testing.T) {
	ctx, repo := newTestPool(t)

	st, err := repo.Create(ctx, CreateStationInput{
		Name: "Geo FM", Band: "FM",
		City: strPtr("São Paulo"), State: strPtr("SP"),
		StreamURL: "http://example.com/geo",
	})
	require.NoError(t, err)
	require.NotNil(t, st.Latitude)
	require.NotNil(t, st.Longitude)
	require.InDelta(t, -23.55, *st.Latitude, 0.6)
	require.InDelta(t, -46.63, *st.Longitude, 0.6)
}

func TestStations_Create_UnknownCity_NoCoords(t *testing.T) {
	ctx, repo := newTestPool(t)

	st, err := repo.Create(ctx, CreateStationInput{
		Name: "Nowhere FM", Band: "FM",
		City: strPtr("Cidade Inexistente XYZ"), State: strPtr("SP"),
		StreamURL: "http://example.com/nowhere",
	})
	require.NoError(t, err) // cadastro NÃO falha por geocode
	require.Nil(t, st.Latitude)
	require.Nil(t, st.Longitude)
}
```

- [ ] **Step 2: Rodar — deve falhar**

Run: `cd workers && go test ./internal/catalog/ -run TestStations_Create_Geocodes -v`
Expected: FAIL — `st.Latitude` é nil (Create ainda não geocoda). *(Se `TEST_DATABASE_URL` não estiver setado, o teste dá SKIP — configure o DB de teste do repo antes; ver `docs/operations/migrations.md`.)*

- [ ] **Step 3: Adicionar o geocoder ao repositório**

Em `workers/internal/catalog/stations.go`, adicione o import (no bloco de imports existente):

```go
	"radiocheck/internal/geo"
```

Troque a struct e o construtor (linhas ~97-103):

```go
type Stations struct {
	pool *pgxpool.Pool
	geo  *geo.Geocoder
}

func NewStations(pool *pgxpool.Pool) *Stations {
	return &Stations{pool: pool, geo: geo.Default()}
}
```

- [ ] **Step 4: Geocodar no `Create`**

Em `Stations.Create`, logo após `defer tx.Rollback(ctx)` e antes do `INSERT`, derive as coordenadas; depois inclua-as no INSERT. Substitua o bloco do INSERT (linhas ~272-277) por:

```go
	var lat, lng *float64
	if in.City != nil && in.State != nil {
		if glat, glng, ok := s.geo.Lookup(*in.City, *in.State); ok {
			lat, lng = &glat, &glng
		}
	}

	st, err := scanStationRow(tx.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO stations (name, band, frequency_mhz, city, state, stream_url, latitude, longitude)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING %s`, stationSelectCols),
		in.Name, in.Band, in.FrequencyMHz, in.City, in.State, in.StreamURL, lat, lng,
	).Scan)
```

(O `RETURNING %s` com `stationSelectCols` já devolve `latitude, longitude`, e `scanStationRow` já as lê — nada mais a mudar no scan.)

- [ ] **Step 5: Rodar — deve passar**

Run: `cd workers && go test ./internal/catalog/ -run TestStations_Create -v`
Expected: PASS (`TestStations_Create_Geocodes`, `TestStations_Create_UnknownCity_NoCoords` e os Create já existentes).

- [ ] **Step 6: Commit**

```powershell
git add workers/internal/catalog/stations.go workers/internal/catalog/stations_test.go
git commit -m "feat(stations): geocoda cidade+UF no Create"
```

---

### Task 4: Re-geocodar no `Stations.Update` (TDD)

**Files:**
- Modify: `workers/internal/catalog/stations.go` (`Update` ~316-350)
- Test: `workers/internal/catalog/stations_test.go`

A regra: re-deriva a coordenada da cidade+UF recebida; se achar, sobrescreve; se não achar, **mantém** a coordenada atual. Implementação com `COALESCE($n, coluna)` — passando `nil` quando não há match, o `COALESCE` preserva o valor existente.

- [ ] **Step 1: Escrever o teste (vai falhar)**

Adicione ao final de `workers/internal/catalog/stations_test.go`:

```go
func TestStations_Update_Geocoding(t *testing.T) {
	ctx, repo := newTestPool(t)

	// Cria sem coordenada (cidade desconhecida).
	st, err := repo.Create(ctx, CreateStationInput{
		Name: "Upd FM", Band: "FM",
		City: strPtr("Cidade Inexistente XYZ"), State: strPtr("SP"),
		StreamURL: "http://example.com/upd",
	})
	require.NoError(t, err)
	require.Nil(t, st.Latitude)

	// Editar para cidade conhecida → coordenada preenchida.
	st, err = repo.Update(ctx, st.ID, UpdateStationInput{
		Name: "Upd FM", Band: "FM",
		City: strPtr("São Paulo"), State: strPtr("SP"),
		StreamURL: "http://example.com/upd",
	})
	require.NoError(t, err)
	require.NotNil(t, st.Latitude)
	require.InDelta(t, -23.55, *st.Latitude, 0.6)
	savedLat, savedLng := *st.Latitude, *st.Longitude

	// Editar só o nome (mantendo a cidade) → coordenada preservada.
	st, err = repo.Update(ctx, st.ID, UpdateStationInput{
		Name: "Upd FM 2", Band: "FM",
		City: strPtr("São Paulo"), State: strPtr("SP"),
		StreamURL: "http://example.com/upd",
	})
	require.NoError(t, err)
	require.NotNil(t, st.Latitude)
	require.Equal(t, savedLat, *st.Latitude)
	require.Equal(t, savedLng, *st.Longitude)

	// Editar para cidade desconhecida → coordenada anterior preservada (não destrói dado).
	st, err = repo.Update(ctx, st.ID, UpdateStationInput{
		Name: "Upd FM 2", Band: "FM",
		City: strPtr("Outra Cidade Inexistente"), State: strPtr("SP"),
		StreamURL: "http://example.com/upd",
	})
	require.NoError(t, err)
	require.NotNil(t, st.Latitude)
	require.Equal(t, savedLat, *st.Latitude)
}
```

- [ ] **Step 2: Rodar — deve falhar**

Run: `cd workers && go test ./internal/catalog/ -run TestStations_Update_Geocoding -v`
Expected: FAIL — após editar para "São Paulo", `st.Latitude` continua nil (Update não geocoda).

- [ ] **Step 3: Geocodar no `Update`**

Em `Stations.Update`, adicione a derivação das coordenadas logo após o bloco que monta `metaJSON` (antes do `UPDATE`):

```go
	var lat, lng *float64
	if in.City != nil && in.State != nil {
		if glat, glng, ok := s.geo.Lookup(*in.City, *in.State); ok {
			lat, lng = &glat, &glng
		}
	}
```

Troque a query do `UPDATE` (linhas ~326-342) para incluir `latitude`/`longitude` via `COALESCE` (preserva o valor atual quando `lat`/`lng` são nil):

```go
	st, err := scanStationRow(s.pool.QueryRow(ctx, fmt.Sprintf(`
		UPDATE stations SET
		  name          = $2,
		  band          = $3,
		  frequency_mhz = $4,
		  city          = $5,
		  state         = $6,
		  stream_url    = $7,
		  logo_url      = $8,
		  pmm           = $9,
		  metadata      = $10::jsonb,
		  latitude      = COALESCE($11, latitude),
		  longitude     = COALESCE($12, longitude),
		  updated_at    = NOW()
		WHERE id = $1
		RETURNING %s`, stationSelectCols),
		id, in.Name, in.Band, in.FrequencyMHz, in.City, in.State,
		in.StreamURL, in.LogoURL, in.PMM, string(metaJSON), lat, lng,
	).Scan)
```

- [ ] **Step 4: Rodar — deve passar**

Run: `cd workers && go test ./internal/catalog/ -run TestStations_Update -v`
Expected: PASS.

- [ ] **Step 5: Garantir que o pacote inteiro segue verde**

Run: `cd workers && go build ./... && go test ./internal/catalog/...`
Expected: build OK; testes PASS (ou SKIP se sem `TEST_DATABASE_URL`).

- [ ] **Step 6: Commit**

```powershell
git add workers/internal/catalog/stations.go workers/internal/catalog/stations_test.go
git commit -m "feat(stations): re-geocoda cidade+UF no Update (preserva no miss)"
```

---

### Task 5: Comando `backfill-geocoding`

**Files:**
- Create: `workers/cmd/backfill-geocoding/main.go`

- [ ] **Step 1: Criar o comando**

Crie `workers/cmd/backfill-geocoding/main.go` (segue o padrão de `backfill-material-durations`):

```go
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
```

- [ ] **Step 2: Compilar**

Run: `cd workers && go build ./cmd/backfill-geocoding`
Expected: compila sem erros (gera o binário; pode apagá-lo depois).

- [ ] **Step 3: Smoke test local (opcional, se houver DB de dev)**

Run: `cd workers && go run ./cmd/backfill-geocoding --dsn "$env:DATABASE_URL" --dry-run`
Expected: imprime `backfill-geocoding [DRY-RUN, null-only]: N stations...` e, no fim, `done [DRY-RUN]. ok=.. pulados=.. falhas=0` mais a lista de cidades não encontradas (se houver).

- [ ] **Step 4: Commit**

```powershell
git add workers/cmd/backfill-geocoding/main.go
git commit -m "feat(backfill): comando backfill-geocoding (lat/long por cidade+UF)"
```

---

### Task 6: Incluir o binário na imagem Docker

**Files:**
- Modify: `infra/docker/Dockerfiles/workers.Dockerfile`

- [ ] **Step 1: Adicionar build do binário**

Em `infra/docker/Dockerfiles/workers.Dockerfile`, após a linha 10 (`...backfill-material-durations ./cmd/backfill-material-durations`), adicione:

```dockerfile
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/backfill-geocoding ./cmd/backfill-geocoding
```

- [ ] **Step 2: Adicionar o COPY para a imagem final**

Após a linha 22 (`COPY --from=builder /out/backfill-material-durations ...`), adicione:

```dockerfile
COPY --from=builder /out/backfill-geocoding  /usr/local/bin/backfill-geocoding
```

- [ ] **Step 3: Build da imagem (validação local)**

Run: `docker compose -f infra/docker/docker-compose.yml build api`
Expected: build conclui; o stage builder compila `backfill-geocoding` sem erro. *(O `COPY workers/ ./` já leva o `data/municipios.csv` para dentro do build, então o `go:embed` resolve.)*

- [ ] **Step 4: Commit**

```powershell
git add infra/docker/Dockerfiles/workers.Dockerfile
git commit -m "fix(docker): inclui backfill-geocoding na imagem workers/api"
```

---

### Task 7: Documentação

**Files:**
- Create: `docs/features/geocoding-emissoras.md`
- Modify: `docs/README.md` (índice)
- Modify: `CLAUDE.md` (mapa de consulta "quando trabalhar em X")

- [ ] **Step 1: Criar o doc da feature**

Crie `docs/features/geocoding-emissoras.md`:

```markdown
---
status: implementado
ultima-verificacao: 2026-05-27
codigo-relacionado:
  - workers/internal/geo/geo.go
  - workers/internal/geo/data/municipios.csv
  - workers/internal/catalog/stations.go
  - workers/cmd/backfill-geocoding/main.go
---

# Geocoding de emissoras (cidade + UF → lat/long)

Preenche `stations.latitude`/`longitude` a partir de `city` + `state` (UF),
usando o **centroide do município** (única resolução possível com cidade+UF).

## Fonte de dados

Dataset IBGE (`kelvins/municipios-brasileiros`, base IBGE) com os ~5.570
municípios, embutido no binário via `go:embed`
(`workers/internal/geo/data/municipios.csv`). **Sem chamada de rede em runtime** —
o lookup é uma busca num mapa em memória, carregado uma vez (`geo.Default()`).

## Regra de match

Chave `(nome_normalizado, UF)`. Normalização: minúsculas + remove acentos +
trim + colapsa espaços. Match **exato** após normalizar (sem fuzzy). Nome de
município é único dentro de um estado, então a chave é determinística.
Cidade que não bate (typo, distrito, nome não-oficial) → não geocoda; é pulada.

## Quando dispara

- **Create:** ao cadastrar emissora com city+UF, grava lat/long no INSERT.
- **Update:** re-deriva da city+UF recebida; se achar, sobrescreve; se não achar,
  **mantém** a coordenada atual (`COALESCE($n, coluna)` — não destrói dado).
- **Backfill:** `backfill-geocoding` para as emissoras já cadastradas.

Não há caminho na API para setar coordenada à mão; por isso re-derivar no Update
é idempotente (editar só o nome reenvia a mesma cidade → mesma coordenada).

## Backfill

```bash
# 1. build + recreate (pega imagem nova ANTES do exec — CLAUDE.md §4.2)
docker compose build api
docker compose up -d --force-recreate --no-deps api

# 2. dry-run: confere quantos ok/pulados e quais cidades não bateram
docker compose exec api backfill-geocoding --dsn "$DATABASE_URL" --dry-run

# 3. pra valer (só preenche as nulas)
docker compose exec api backfill-geocoding --dsn "$DATABASE_URL"

# opcional: reprocessa TODAS, sobrescrevendo
docker compose exec api backfill-geocoding --dsn "$DATABASE_URL" --force
```

O relatório final lista as cidades não encontradas, para corrigir o cadastro.

## Limitações

- Distritos/bairros e nomes não-oficiais não casam (ficam sem coordenada).
- Cidade editada de válida→inválida mantém a coordenada antiga (decisão
  conservadora: não apagar dado num lookup que falhou).
- Atualizar o dataset IBGE (fusão/criação de municípios) exige re-baixar o CSV
  e rodar `backfill-geocoding --force`.
```

- [ ] **Step 2: Adicionar ao índice `docs/README.md`**

Abra `docs/README.md`, localize a seção que lista os `features/` e adicione (em ordem coerente com as demais entradas):

```markdown
- [features/geocoding-emissoras.md](features/geocoding-emissoras.md) — lat/long de emissoras por cidade+UF (dataset IBGE embutido + backfill)
```

- [ ] **Step 3: Adicionar ao mapa de consulta do `CLAUDE.md`**

Abra `CLAUDE.md`, na tabela "Mapa de consulta — quando trabalhar em X, ver Y", adicione a linha:

```markdown
| Geocoding de emissoras (lat/long por cidade+UF, dataset IBGE, backfill) | [docs/features/geocoding-emissoras.md](docs/features/geocoding-emissoras.md) |
```

- [ ] **Step 4: Commit**

```powershell
git add docs/features/geocoding-emissoras.md docs/README.md CLAUDE.md
git commit -m "docs(geo): documenta geocoding de emissoras e backfill"
```

---

## Verificação final (antes de abrir PR / deploy)

- [ ] `cd workers && go build ./...` — compila tudo.
- [ ] `cd workers && go vet ./internal/geo/... ./internal/catalog/... ./cmd/backfill-geocoding/...`
- [ ] `cd workers && go test ./internal/geo/...` — PASS (sem DB).
- [ ] `cd workers && go test ./internal/catalog/...` — PASS (ou SKIP sem `TEST_DATABASE_URL`).
- [ ] `docker compose -f infra/docker/docker-compose.yml build api` — imagem builda com o novo binário e o CSV embutido.

## Rollout em produção (CLAUDE.md §4 — disparado pelo usuário na VM)

1. Testar tudo no dev local primeiro (§4.3).
2. `docker compose build api`
3. `docker compose up -d --force-recreate --no-deps api` (§4.1/§4.2 — recreate pega imagem nova antes do exec; `--no-deps` protege o postgres).
4. `docker compose exec api backfill-geocoding --dsn "$DATABASE_URL" --dry-run` → conferir relatório.
5. `docker compose exec api backfill-geocoding --dsn "$DATABASE_URL"` → pra valer.

Em prod, preferir `./scripts/deploy.sh` (já inclui o override file, §4.7) ou incluir `-f infra/docker/docker-compose.override.yml --env-file infra/docker/.env` manualmente nos comandos `docker compose`.

---

## Self-review (preenchido pelo autor do plano)

- **Cobertura do spec:** fonte IBGE embutida ✔ (Task 1-2); normalização + match exato ✔ (Task 2); Create ✔ (Task 3); Update preservando no miss ✔ (Task 4); backfill null-only/`--force`/`--dry-run` + relatório de não-encontradas ✔ (Task 5); sem migration ✔ (nenhuma task de DDL); Docker ✔ (Task 6); docs ✔ (Task 7); rollout §4 ✔ (seção final).
- **Placeholders:** nenhum — todo passo traz código/comando concreto.
- **Consistência de tipos:** `Lookup(city, state string) (lat, lng float64, ok bool)` usado igual em Create, Update e backfill; `geo.New`/`geo.Default` coerentes; `NewStations(pool)` mantém assinatura (sem quebrar call-sites).
```
