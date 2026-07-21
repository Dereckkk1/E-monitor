# PMM no target por cliente — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Permitir cadastrar, por cliente e por emissora, um "PMM no target" (audiência dentro do público-alvo do cliente) e exibir "Impactos no target" ao lado de "Impactos" em `/insights`, `/detections`, `/reports/airtime`, `/campaigns` e nos relatórios exportáveis de campanha.

**Architecture:** Tabela nova `client_station_pmm` com PK `(client_id, station_id)`. A resolução é sempre `client_station_pmm[campanha.client_id, station_id]` — toda tela de veiculação parte de uma campanha. Cada agregação existente ganha uma linha espelho (`SUM(... × pmm_target)`) ao lado da linha de `pmm` que já existe; nenhuma query existente muda de valor.

**Tech Stack:** Go 1.26 (pgx v5, chi v5), PostgreSQL, React 19 + react-query v5 (Vite), jsPDF.

**Spec:** [docs/superpowers/specs/2026-07-21-client-target-pmm-design.md](../specs/2026-07-21-client-target-pmm-design.md)

---

## Convenções deste plano

**Banco de teste (Go).** Os testes de integração precisam de um PostgreSQL. O PostgreSQL nativo do Windows ocupa a 5432 e rejeita a senha do Docker — use o descartável na 15432:

```bash
docker run -d --name rc-test-pg -p 15432:5432 \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=radiocheck \
  --network docker_default postgres:16-alpine
export TEST_DATABASE_URL="postgres://postgres:postgres@localhost:15432/radiocheck?sslmode=disable"
```

Falhas pré-existentes do harness de `internal/catalog` (material_ids NOT NULL, partição, FK user, isolamento de stations) **não** são regressões suas.

**Não rode `npm install` no frontend.** Regra 5 do `CLAUDE.md`: no Windows o npm poda as optional deps linux do lockfile e quebra o build do Cloudflare Pages. Nenhuma task deste plano adiciona dependência de frontend — os testes de JS usam o runner nativo do Node (`node --test`), que não precisa de instalação.

**Verificação de build.** O deploy cross-compila. Depois de qualquer mudança em Go:

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```

---

## Estrutura de arquivos

**Criar:**

| Arquivo | Responsabilidade |
|---|---|
| `migrations/0054_client_station_pmm.up.sql` / `.down.sql` | Tabela + índice + trigger |
| `workers/internal/catalog/client_station_pmm.go` | Repo: listar alvos do cliente, upsert em lote |
| `workers/internal/catalog/client_station_pmm_test.go` | Testes do repo |
| `workers/internal/api/handlers/client_target_pmm.go` | Handler HTTP (GET/PUT) |
| `frontend/src/utils/targetPmmPaste.js` | Parser puro da colagem de planilha |
| `frontend/src/utils/targetPmmPaste.test.mjs` | Teste do parser (`node --test`) |
| `frontend/src/pages/ClientTargetPmmPage.jsx` | Tela `/clients/:id/target-pmm` |
| `docs/features/client-target-pmm.md` | Documentação da feature |

**Modificar:** `workers/internal/catalog/{insights,campaigns,detections}.go`, `workers/internal/api/{router.go,handlers/{detections,reports}.go}`, `frontend/src/{App.jsx,api/hooks.js}`, `frontend/src/pages/{ClientsPage,CampaignsPage}.jsx`, `frontend/src/components/{DistributionGrid,AirtimeDetectionRow}.jsx`, `frontend/src/components/insights/KpiCards.jsx`, `frontend/src/utils/{pdfReport,gridReport}.js`, `docs/README.md`, `CLAUDE.md`.

---

## Task 1: Migration da tabela

**Files:**
- Create: `migrations/0054_client_station_pmm.up.sql`
- Create: `migrations/0054_client_station_pmm.down.sql`

- [ ] **Step 1: Escrever a migration up**

`migrations/0054_client_station_pmm.up.sql`:

```sql
-- 0054_client_station_pmm.up.sql
-- PMM no target por cliente: para cada par (cliente, emissora), a audiência
-- dentro do público-alvo daquele cliente naquela emissora.
--
-- Decisões de design:
--
-- 1. Granularidade é (cliente × emissora), não (campanha × emissora): o target
--    é uma propriedade do cliente e vale para todas as campanhas dele. A
--    resolução em toda query é client_station_pmm[campanha.client_id, station_id].
--
-- 2. AUSÊNCIA DE LINHA = "não cadastrado" → a emissora fica FORA do total de
--    impactos no target e fora do contador "X de Y emissoras com target".
--    O valor 0 é legítimo e DIFERENTE disso: significa target zero e CONTA
--    como cadastrada. Por isso não há DEFAULT nem NULL em pmm_target.
--
-- 3. Sem versionamento temporal — o valor corrente vale para todo o histórico,
--    igual ao stations.pmm de hoje. Corrigir um valor muda relatórios passados.

BEGIN;

CREATE TABLE IF NOT EXISTS client_station_pmm (
    client_id  UUID    NOT NULL REFERENCES clients(id)  ON DELETE CASCADE,
    station_id UUID    NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
    pmm_target INTEGER NOT NULL CHECK (pmm_target >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (client_id, station_id)
);

-- Lookup reverso (quais clientes têm target numa emissora) e suporte ao
-- LEFT JOIN por station_id nas agregações.
CREATE INDEX IF NOT EXISTS idx_client_station_pmm_station
    ON client_station_pmm(station_id);

-- touch_updated_at() já existe desde a 0022 (pricing).
DROP TRIGGER IF EXISTS trg_cspmm_touch ON client_station_pmm;
CREATE TRIGGER trg_cspmm_touch
    BEFORE UPDATE ON client_station_pmm
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

COMMIT;
```

- [ ] **Step 2: Escrever a migration down**

`migrations/0054_client_station_pmm.down.sql`:

```sql
BEGIN;
DROP TRIGGER IF EXISTS trg_cspmm_touch ON client_station_pmm;
DROP TABLE IF EXISTS client_station_pmm;
COMMIT;
```

- [ ] **Step 3: Aplicar num banco descartável e conferir**

```bash
docker run -d --name rc-mig-pg -p 15433:5432 -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=radiocheck postgres:16-alpine
docker run --rm -v "$PWD/migrations:/m" --network host migrate/migrate \
  -path=/m -database "postgres://postgres:postgres@localhost:15433/radiocheck?sslmode=disable" up
docker exec rc-mig-pg psql -U postgres -d radiocheck -c "\d client_station_pmm"
```

Esperado: a tabela aparece com as 5 colunas, PK `(client_id, station_id)`, o índice `idx_client_station_pmm_station` e o trigger `trg_cspmm_touch`.

- [ ] **Step 4: Conferir que o down reverte**

```bash
docker run --rm -v "$PWD/migrations:/m" --network host migrate/migrate \
  -path=/m -database "postgres://postgres:postgres@localhost:15433/radiocheck?sslmode=disable" down 1
docker exec rc-mig-pg psql -U postgres -d radiocheke -c "\d client_station_pmm"
```

Esperado: `Did not find any relation named "client_station_pmm"`. Depois rode `up` de novo e derrube o container: `docker rm -f rc-mig-pg`.

- [ ] **Step 5: Commit**

```bash
git add migrations/0054_client_station_pmm.up.sql migrations/0054_client_station_pmm.down.sql
git commit -m "feat(db): tabela client_station_pmm (PMM no target por cliente)"
```

---

## Task 2: Repositório Go

**Files:**
- Create: `workers/internal/catalog/client_station_pmm.go`
- Test: `workers/internal/catalog/client_station_pmm_test.go`

- [ ] **Step 1: Escrever o teste que falha**

`workers/internal/catalog/client_station_pmm_test.go`:

```go
package catalog

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestClientStationPMM_BulkUpsert cobre os três efeitos de uma única chamada:
// inserir uma linha nova, atualizar uma existente e apagar uma via nil.
func TestClientStationPMM_BulkUpsert(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewClientStationPMM(pool)

	clientID := insSeedClient(t, ctx, pool, "Cliente Target")
	stA := insSeedStationNoProfile(t, ctx, pool, "Alvo A")
	stB := insSeedStationNoProfile(t, ctx, pool, "Alvo B")
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM client_station_pmm WHERE client_id = $1", clientID)
	})

	v3400, v99 := 3400, 99
	// 1ª chamada: insere as duas.
	if _, _, err := repo.BulkUpsert(ctx, clientID, []TargetPMMEntry{
		{StationID: stA, PMMTarget: &v3400},
		{StationID: stB, PMMTarget: &v99},
	}); err != nil {
		t.Fatalf("bulk upsert insert: %v", err)
	}

	// 2ª chamada: atualiza A e apaga B.
	v5000 := 5000
	updated, deleted, err := repo.BulkUpsert(ctx, clientID, []TargetPMMEntry{
		{StationID: stA, PMMTarget: &v5000},
		{StationID: stB, PMMTarget: nil},
	})
	if err != nil {
		t.Fatalf("bulk upsert update: %v", err)
	}
	if updated != 1 || deleted != 1 {
		t.Errorf("updated=%d deleted=%d, want 1 e 1", updated, deleted)
	}

	// Asserção direta no estado da tabela: ListForClient depende de campanhas
	// com target_stations, que este teste de propósito não monta.
	var n, valA int
	if err := pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(max(pmm_target) FILTER (WHERE station_id = $2), -1)
		 FROM client_station_pmm WHERE client_id = $1`, clientID, stA,
	).Scan(&n, &valA); err != nil {
		t.Fatalf("assert state: %v", err)
	}
	if n != 1 {
		t.Fatalf("linhas restantes = %d, want 1 (B tinha que ter sido apagada)", n)
	}
	if valA != 5000 {
		t.Errorf("target de A = %d, want 5000", valA)
	}
}

// TestClientStationPMM_ZeroIsNotAbsent garante a distinção entre "target zero"
// (linha existe, conta no denominador) e "não cadastrado" (sem linha).
func TestClientStationPMM_ZeroIsNotAbsent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewClientStationPMM(pool)

	clientID := insSeedClient(t, ctx, pool, "Cliente Zero")
	st := insSeedStationNoProfile(t, ctx, pool, "Alvo Zero")
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM client_station_pmm WHERE client_id = $1", clientID)
	})

	zero := 0
	if _, _, err := repo.BulkUpsert(ctx, clientID, []TargetPMMEntry{
		{StationID: st, PMMTarget: &zero},
	}); err != nil {
		t.Fatalf("upsert zero: %v", err)
	}

	var got *int
	if err := pool.QueryRow(ctx,
		`SELECT pmm_target FROM client_station_pmm WHERE client_id = $1 AND station_id = $2`,
		clientID, st,
	).Scan(&got); err != nil {
		t.Fatalf("target 0 não gravou — zero tem que contar como cadastrado: %v", err)
	}
	if got == nil || *got != 0 {
		t.Errorf("target = %v, want 0", got)
	}
}
```

Este teste usa três helpers. `insSeedStationNoProfile` já existe em [`insights_test.go:99`](../../../workers/internal/catalog/insights_test.go#L99). `testPool` já existe no pacote. `insSeedClient` pode não existir — confira com `grep -rn "func insSeedClient" workers/internal/catalog/`. Se não existir, adicione-o em `client_station_pmm_test.go`:

```go
// insSeedClient cria um cliente descartável e o remove no fim do teste.
func insSeedClient(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO clients (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("seed client: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", id) })
	return id
}
```

(Se adicionar, inclua `"github.com/jackc/pgx/v5/pgxpool"` nos imports do arquivo de teste.)

- [ ] **Step 2: Rodar o teste e ver falhar**

```bash
cd workers && go test ./internal/catalog/ -run TestClientStationPMM -v
```

Esperado: FAIL — `undefined: NewClientStationPMM`.

- [ ] **Step 3: Implementar o repositório**

`workers/internal/catalog/client_station_pmm.go`:

```go
package catalog

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// REGRA DE RESOLUÇÃO DO PMM NO TARGET — canônica, vale no sistema inteiro:
//
//	o target de uma linha é client_station_pmm[campanha.client_id, station_id]
//
// O target é uma propriedade do CLIENTE e vale para todas as campanhas dele. Em
// multi-atribuição cada atribuição resolve pelo cliente DELA, que é o
// comportamento correto.
//
// Não há um fragmento SQL compartilhado (ao contrário do ApprovedDetectionsFilter):
// cada consumidor chega na campanha por um alias diferente (`cmp` em detections,
// `cc` em campaigns, a CTE `per_station` em insights), então um literal comum não
// encaixaria em nenhum deles sem renomear queries estáveis. O join é escrito à mão
// em cada lugar, sempre nesta forma:
//
//	LEFT JOIN client_station_pmm cst
//	       ON cst.client_id = <alias da campanha>.client_id
//	      AND cst.station_id = <alias da emissora>.id
//
// Consumidores: insights.go (aggregateCore), campaigns.go (FinancialsByCampaign),
// detections.go (ListPaged, IterateForExport, AggregateByMaterialStation,
// AggregateByStation).

// TargetPMMRow é uma emissora-alvo do cliente com os dois PMMs lado a lado:
// o global da emissora e o do target do cliente (nil quando não cadastrado).
type TargetPMMRow struct {
	StationID    uuid.UUID `json:"station_id"`
	StationShort int32     `json:"short_id"`
	Name         string    `json:"name"`
	Band         *string   `json:"band,omitempty"`
	FrequencyMHz *float64  `json:"frequency_mhz,omitempty"`
	City         *string   `json:"city,omitempty"`
	State        *string   `json:"state,omitempty"`
	PMM          *float64  `json:"pmm,omitempty"`
	PMMTarget    *int      `json:"pmm_target"`
}

// TargetPMMEntry é um item do payload de escrita. PMMTarget nil APAGA a linha
// (volta ao estado "não cadastrado"); 0 grava zero, que é diferente de ausente.
type TargetPMMEntry struct {
	StationID uuid.UUID `json:"station_id"`
	PMMTarget *int      `json:"pmm_target"`
}

type ClientStationPMM struct {
	pool *pgxpool.Pool
}

func NewClientStationPMM(pool *pgxpool.Pool) *ClientStationPMM {
	return &ClientStationPMM{pool: pool}
}

// ListForClient devolve as emissoras-alvo das campanhas do cliente (união dos
// campaigns.target_stations), cada uma com o PMM global e o target já resolvido.
// Emissoras sem cadastro vêm com PMMTarget nil — a tela precisa delas para
// oferecer o input vazio.
//
// Campanhas canceladas entram normalmente: a lista é de cadastro, não uma tela
// operacional (docs/features/cancelled-campaign-handling.md).
func (r *ClientStationPMM) ListForClient(ctx context.Context, clientID uuid.UUID) ([]TargetPMMRow, error) {
	rows, err := r.pool.Query(ctx, `
		WITH alvo AS (
		    SELECT DISTINCT unnest(c.target_stations) AS station_id
		    FROM campaigns c
		    WHERE c.client_id = $1
		)
		SELECT s.id, s.short_id, s.name, s.band, s.frequency_mhz, s.city, s.state,
		       s.pmm, cst.pmm_target
		FROM alvo a
		JOIN stations s ON s.id = a.station_id
		LEFT JOIN client_station_pmm cst
		       ON cst.client_id = $1 AND cst.station_id = s.id
		ORDER BY s.name`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]TargetPMMRow, 0)
	for rows.Next() {
		var row TargetPMMRow
		if err := rows.Scan(&row.StationID, &row.StationShort, &row.Name, &row.Band,
			&row.FrequencyMHz, &row.City, &row.State, &row.PMM, &row.PMMTarget); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// BulkUpsert aplica o lote inteiro numa transação: entradas com valor viram
// INSERT ... ON CONFLICT DO UPDATE, entradas com nil viram DELETE. Devolve
// (gravadas, apagadas). Idempotente — reenviar o mesmo lote não muda nada.
func (r *ClientStationPMM) BulkUpsert(ctx context.Context, clientID uuid.UUID, entries []TargetPMMEntry) (int, int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)

	var updated, deleted int
	for _, e := range entries {
		if e.PMMTarget == nil {
			tag, err := tx.Exec(ctx,
				`DELETE FROM client_station_pmm WHERE client_id = $1 AND station_id = $2`,
				clientID, e.StationID)
			if err != nil {
				return 0, 0, err
			}
			deleted += int(tag.RowsAffected())
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO client_station_pmm (client_id, station_id, pmm_target)
			VALUES ($1, $2, $3)
			ON CONFLICT (client_id, station_id)
			DO UPDATE SET pmm_target = EXCLUDED.pmm_target`,
			clientID, e.StationID, *e.PMMTarget); err != nil {
			return 0, 0, err
		}
		updated++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return updated, deleted, nil
}

var _ = pgx.ErrNoRows // mantém o import de pgx caso handlers reusem
```

Se o `var _ = pgx.ErrNoRows` disparar lint de import não usado no seu ambiente, remova a linha e o import de `pgx`.

- [ ] **Step 4: Rodar os testes e ver passar**

```bash
cd workers && go test ./internal/catalog/ -run TestClientStationPMM -v
```

Esperado: PASS nos dois testes.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/client_station_pmm.go workers/internal/catalog/client_station_pmm_test.go
git commit -m "feat(catalog): repo de PMM no target por cliente"
```

---

## Task 3: Handler HTTP + rotas

**Files:**
- Create: `workers/internal/api/handlers/client_target_pmm.go`
- Modify: `workers/internal/api/router.go:172` (GET, subgrupo A) e `:258` (PUT, subgrupo B)

- [ ] **Step 1: Escrever o handler**

`workers/internal/api/handlers/client_target_pmm.go`:

```go
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"radiocheck/internal/auth"
	"radiocheck/internal/catalog"
)

// ClientTargetPmmHandler serve o cadastro de PMM no target por cliente.
//
// GET é viewer-friendly com scope-check (o cliente logado só enxerga o próprio
// cadastro) porque a grid de /detections e o dashboard precisam do mapa para
// renderizar os impactos no target. PUT é admin-only — quem cadastra é o
// operador interno.
type ClientTargetPmmHandler struct {
	Repo *catalog.ClientStationPMM
}

// List devolve as emissoras-alvo do cliente com PMM global e target.
// GET /clients/{clientID}/target-pmm
func (h *ClientTargetPmmHandler) List(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "clientID"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	// Anti-oracle: viewer pedindo outro cliente recebe 404, não 403 — não
	// confirma a existência do id. Mesmo padrão de /campaigns/{id}.
	if scope := auth.ClientScopeFromContext(r.Context()); scope != nil && *scope != id {
		http.Error(w, "not found", 404)
		return
	}
	rows, err := h.Repo.ListForClient(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}

type bulkTargetPmmRequest struct {
	Entries []catalog.TargetPMMEntry `json:"entries"`
}

// Bulk aplica o lote de cadastro. PUT /clients/{clientID}/target-pmm
func (h *ClientTargetPmmHandler) Bulk(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "clientID"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var in bulkTargetPmmRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	// Guarda de sanidade: a UI manda só as linhas alteradas, então um lote
	// gigante indica bug de cliente, não uso legítimo.
	if len(in.Entries) > 5000 {
		http.Error(w, "too many entries", 400)
		return
	}
	for _, e := range in.Entries {
		if e.PMMTarget != nil && *e.PMMTarget < 0 {
			http.Error(w, "pmm_target must be >= 0", 400)
			return
		}
	}
	updated, deleted, err := h.Repo.BulkUpsert(r.Context(), id, in.Entries)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"updated": updated, "deleted": deleted})
}
```

- [ ] **Step 2: Registrar o handler nas dependências**

Encontre a struct de dependências do router (o tipo do parâmetro `d` usado em `router.go`, ex.: `d.Clients`, `d.Webhooks`):

```bash
grep -rn "Clients \+\*handlers.ClientsHandler\|Clients .*ClientsHandler" workers/internal/
```

Adicione o campo na struct, seguindo o padrão dos vizinhos:

```go
ClientTargetPmm *handlers.ClientTargetPmmHandler
```

E, onde essa struct é montada (mesmo lugar onde `Clients: &handlers.ClientsHandler{Repo: ...}` é construído), adicione:

```go
ClientTargetPmm: &handlers.ClientTargetPmmHandler{Repo: catalog.NewClientStationPMM(pool)},
```

- [ ] **Step 3: Registrar as rotas**

Em `workers/internal/api/router.go`, no **subgrupo A** (viewer-friendly), logo abaixo da linha `r.Get("/clients", d.Clients.List)` (linha 172):

```go
				// PMM no target por cliente — leitura viewer-friendly (scope-check
				// no handler): a grid de /detections e o /insights do cliente
				// precisam do mapa para exibir "Impactos no target". A escrita
				// fica admin-only no subgrupo B.
				if d.ClientTargetPmm != nil {
					r.Get("/clients/{clientID}/target-pmm", d.ClientTargetPmm.List)
				}
```

No **subgrupo B**, logo abaixo de `r.Post("/clients/{id}/activate", d.Clients.Activate)` (linha 258):

```go
				// Cadastro do PMM no target (bulk upsert). Admin/operator-only —
				// NÃO usar r.Route() aqui pelo mesmo motivo dos writes de /clients:
				// mascararia o GET registrado no subgrupo A.
				if d.ClientTargetPmm != nil {
					r.Put("/clients/{clientID}/target-pmm", d.ClientTargetPmm.Bulk)
				}
```

- [ ] **Step 4: Compilar (cross-compile, como o deploy faz)**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```

Esperado: sem saída (sucesso).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/client_target_pmm.go workers/internal/api/router.go
git commit -m "feat(api): rotas de cadastro de PMM no target por cliente"
```

---

## Task 4: `/insights` — impactos e CPM no target

**Files:**
- Modify: `workers/internal/catalog/insights.go:76-85` (struct), `:149-209` (Compute), `:303-397` (aggregateCore)
- Test: `workers/internal/catalog/insights_test.go`

**Atenção — armadilha de contagem.** Hoje `per_station` agrupa só por `station_id`, e `stations_count`/`stations_with_pmm` são `COUNT(*)` sobre esse conjunto. Ao adicionar `client_id` no GROUP BY (necessário para resolver o target), uma emissora que aparece em campanhas de dois clientes vira **duas linhas** e esses `COUNT(*)` passariam a contar em dobro. Por isso os contadores viram `COUNT(DISTINCT station_id)`. As somas (`impactos`, demografia) não são afetadas: `Σ det_count` por emissora é o mesmo particionado ou não.

- [ ] **Step 1: Escrever o teste que falha**

Adicione em `workers/internal/catalog/insights_test.go`:

```go
// TestInsights_AggregateCore_TargetPMM cobre as três combinações possíveis:
// emissora só com PMM global, só com target, e com os dois. Também trava a
// regressão de contagem: stations_count não pode contar em dobro.
func TestInsights_AggregateCore_TargetPMM(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	clientID := insSeedClient(t, ctx, pool, "Cliente Target Insights")
	camp := insSeedCampaignForClient(t, ctx, pool, clientID, "Campanha Target")
	mat := insSeedMaterial(t, ctx, pool, camp, "Spot Target")

	// stAmbos: PMM 1000 e target 400. stSoPMM: PMM 1000, sem target.
	// stSoTarget: sem PMM, target 700.
	stAmbos := insSeedStation(t, ctx, pool, "Ambos", 1000, 60, 40, 20, 50, 30, 30, 50, 20)
	stSoPMM := insSeedStation(t, ctx, pool, "SoPMM", 1000, 60, 40, 20, 50, 30, 30, 50, 20)
	stSoTarget := insSeedStationNoProfile(t, ctx, pool, "SoTarget")

	repo := NewClientStationPMM(pool)
	v400, v700 := 400, 700
	if _, _, err := repo.BulkUpsert(ctx, clientID, []TargetPMMEntry{
		{StationID: stAmbos, PMMTarget: &v400},
		{StationID: stSoTarget, PMMTarget: &v700},
	}); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM client_station_pmm WHERE client_id = $1", clientID)
	})

	// 2 detecções em stAmbos, 1 em stSoPMM, 1 em stSoTarget.
	insSeedDetection(t, ctx, pool, camp, mat, stAmbos, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, stAmbos, "in_slot", "2026-06-11")
	insSeedDetection(t, ctx, pool, camp, mat, stSoPMM, "in_slot", "2026-06-10")
	insSeedDetection(t, ctx, pool, camp, mat, stSoTarget, "in_slot", "2026-06-10")

	core, err := NewInsights(pool).aggregateCore(ctx, InsightsParams{
		CampaignIDs: []uuid.UUID{camp},
		From:        mustDate(t, "2026-06-01"),
		To:          mustDate(t, "2026-06-30"),
		StationIDs:  []uuid.UUID{},
	})
	if err != nil {
		t.Fatalf("aggregateCore: %v", err)
	}

	// impactos = 2×1000 (ambos) + 1×1000 (soPMM) + 0 (soTarget, sem pmm) = 3000
	if core.Impactos != 3000 {
		t.Errorf("impactos = %d, want 3000", core.Impactos)
	}
	// impactos_target = 2×400 (ambos) + 0 (soPMM, sem target) + 1×700 = 1500
	if core.ImpactosTarget != 1500 {
		t.Errorf("impactos_target = %d, want 1500", core.ImpactosTarget)
	}
	if core.StationsCount != 3 {
		t.Errorf("stations_count = %d, want 3", core.StationsCount)
	}
	if core.StationsWithPMM != 2 {
		t.Errorf("stations_with_pmm = %d, want 2", core.StationsWithPMM)
	}
	if core.StationsWithTarget != 2 {
		t.Errorf("stations_with_target = %d, want 2", core.StationsWithTarget)
	}
}
```

Os helpers `insSeedCampaignForClient`, `insSeedMaterial`, `insSeedDetection` e `mustDate` — confira os nomes reais no arquivo antes de rodar:

```bash
grep -n "^func ins\|^func mustDate" workers/internal/catalog/insights_test.go
```

Use os que existirem. Se o helper de campanha existente não aceitar `client_id`, crie o cliente e faça `UPDATE campaigns SET client_id = $1 WHERE id = $2` logo depois do seed, em vez de escrever um helper novo.

- [ ] **Step 2: Rodar e ver falhar**

```bash
cd workers && go test ./internal/catalog/ -run TestInsights_AggregateCore_TargetPMM -v
```

Esperado: FAIL — `core.ImpactosTarget undefined`.

- [ ] **Step 3: Adicionar os campos ao `coreAggregates`**

Em `insights.go:303-313`, substitua a struct por:

```go
// coreAggregates é o resultado interno usado pelo Compute().
type coreAggregates struct {
	Impactos           int64
	ImpactosTarget     int64
	VeiculacoesTotal   int64
	StationsCount      int
	StationsWithPMM    int
	StationsWithTarget int
	Gender             GenderK
	Class              ClassPyramidData
	Ages               AgeRangesData
	Breakdown          VeiculacoesBreakdownData
}
```

- [ ] **Step 4: Alterar a query do `aggregateCore`**

Em `insights.go:325-383`, aplique três mudanças na query.

**(a)** Na CTE `filt`, trazer o cliente da campanha — troque o `SELECT`/`FROM` por:

```sql
		WITH filt AS (
		    SELECT d.id, d.station_id, d.category, c.client_id
		    FROM detection_attributions d
		    JOIN campaigns c ON c.id = d.campaign_id
		    WHERE d.campaign_id = ANY($1::uuid[])
```

(o resto do `WHERE` fica igual).

**(b)** Em `per_station`, agrupar também por cliente:

```sql
		per_station AS (
		    SELECT f.station_id, f.client_id,
		           COUNT(*)::bigint AS det_count,
		           COUNT(*) FILTER (WHERE f.category='in_slot')::bigint  AS in_slot_n,
		           COUNT(*) FILTER (WHERE f.category='out_slot')::bigint AS out_slot_n,
		           COUNT(*) FILTER (WHERE f.category='out_date')::bigint AS out_date_n,
		           COUNT(*) FILTER (WHERE f.category='orphan')::bigint   AS orphan_n
		    FROM filt f
		    GROUP BY f.station_id, f.client_id
		),
```

**(c)** Em `joined`, resolver o target; e no SELECT final, trocar os `COUNT(*)` por `COUNT(DISTINCT station_id)` e somar os impactos no target:

```sql
		joined AS (
		    SELECT ps.*,
		           s.pmm,
		           cst.pmm_target,
		           (s.metadata->'audience_profile'->'gender'      ->>'male')::float    AS male_p,
```

(as outras linhas de percentual ficam iguais), e o `FROM` da CTE:

```sql
		    FROM per_station ps
		    JOIN stations s ON s.id = ps.station_id
		    LEFT JOIN client_station_pmm cst
		           ON cst.client_id = ps.client_id AND cst.station_id = ps.station_id
		)
```

No SELECT final, substitua as três primeiras linhas de agregação por:

```sql
		SELECT
		    COALESCE(SUM(det_count), 0)::bigint                                  AS veic_total,
		    -- DISTINCT obrigatório: per_station agora particiona por cliente, então
		    -- uma emissora usada por 2 clientes vira 2 linhas. Sem DISTINCT os
		    -- contadores dobrariam (as SOMAS não são afetadas).
		    COUNT(DISTINCT station_id)::int                                      AS stations_count,
		    COUNT(DISTINCT station_id) FILTER (WHERE pmm IS NOT NULL)::int       AS stations_with_pmm,
		    COUNT(DISTINCT station_id) FILTER (WHERE pmm_target IS NOT NULL)::int AS stations_with_target,
		    COALESCE(SUM(det_count * pmm) FILTER (WHERE pmm IS NOT NULL), 0)::bigint AS impactos,
		    COALESCE(SUM(det_count * pmm_target) FILTER (WHERE pmm_target IS NOT NULL), 0)::bigint AS impactos_target,
```

(o resto do SELECT — gender, classes, faixas, breakdown — fica idêntico).

E o `Scan` em `insights.go:386-392` vira:

```go
	out := &coreAggregates{}
	if err := row.Scan(
		&out.VeiculacoesTotal, &out.StationsCount, &out.StationsWithPMM, &out.StationsWithTarget,
		&out.Impactos, &out.ImpactosTarget,
		&out.Gender.M, &out.Gender.F,
		&out.Class.AB, &out.Class.C, &out.Class.DE,
		&out.Ages.R18_24, &out.Ages.R25_49, &out.Ages.R50Plus,
		&out.Breakdown.InSlot, &out.Breakdown.OutSlot, &out.Breakdown.OutDate, &out.Breakdown.ExtrasOrphan,
	); err != nil {
		return nil, err
	}
```

- [ ] **Step 5: Rodar e ver passar**

```bash
cd workers && go test ./internal/catalog/ -run "TestInsights_AggregateCore" -v
```

Esperado: PASS — inclusive o `TestInsights_AggregateCore_StationWithoutPMM` que já existia (garante que a mudança de contagem não regrediu).

- [ ] **Step 6: Expor no payload da API**

Em `insights.go:76-85`, a struct `InsightsKPIs` vira:

```go
type InsightsKPIs struct {
	Impactos         int64        `json:"impactos"`
	VeiculacoesTotal int64        `json:"veiculacoes_total"`
	StationsCount    int          `json:"stations_count"`
	StationsWithPMM  int          `json:"stations_with_pmm"`
	CPM              float64      `json:"cpm"`
	Bonificacao      BonificacaoK `json:"bonificacao"`
	Investido        InvestidoK   `json:"investido"`
	Gender           GenderK      `json:"gender"`
	// Bloco "no target": só faz sentido quando o cliente tem cadastro de
	// PMM no target. StationsWithTarget == 0 → o frontend esconde os cards.
	ImpactosTarget     int64   `json:"impactos_target"`
	StationsWithTarget int     `json:"stations_with_target"`
	CPMTarget          float64 `json:"cpm_target"`
}
```

Em `Compute` (`insights.go:187-209`), antes do `return`, calcule o CPM no target:

```go
	// CPM no target é SEMPRE dinâmico (executado ÷ impactos_target × 1000),
	// mesmo em campanha com fixed_cpm: o CPM fixo é contratado sobre a base
	// total de audiência, não sobre o recorte de público-alvo.
	var cpmTarget float64
	if core.ImpactosTarget > 0 {
		cpmTarget = (inv.Executado / float64(core.ImpactosTarget)) * 1000.0
	}
```

E no literal `KPIs: InsightsKPIs{...}`, adicione as três linhas:

```go
			ImpactosTarget:     core.ImpactosTarget,
			StationsWithTarget: core.StationsWithTarget,
			CPMTarget:          cpmTarget,
```

- [ ] **Step 7: Compilar e rodar o pacote inteiro**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./... && go test ./internal/catalog/ -run TestInsights -v
```

Esperado: build limpo, testes de Insights passando.

- [ ] **Step 8: Commit**

```bash
git add workers/internal/catalog/insights.go workers/internal/catalog/insights_test.go
git commit -m "feat(insights): impactos e CPM no target por cliente"
```

---

## Task 5: `/campaigns` — audiência no target nos financials

**Files:**
- Modify: `workers/internal/catalog/campaigns.go:456-562`

- [ ] **Step 1: Estender a struct**

Em `campaigns.go:470-478`, adicione dois campos e atualize o comentário de fórmula logo acima (`:463` e `:466`) para mencionar a linha espelho:

```go
type CampaignFinancials struct {
	CampaignID      uuid.UUID `json:"campaign_id"`
	TotalInvested   float64   `json:"total_invested"`
	TotalInsertions int       `json:"total_insertions"`
	TotalAudience   float64   `json:"total_audience"`
	// TotalAudienceTarget espelha TotalAudience trocando stations.pmm pelo
	// client_station_pmm.pmm_target do cliente DONO da campanha.
	// StationsWithTarget > 0 é o gate de exibição no frontend.
	TotalAudienceTarget float64 `json:"total_audience_target"`
	StationsWithTarget  int     `json:"stations_with_target"`
	// FixedCPM, quando setado, sobrescreve o CPM derivado (invested/audience).
	// O frontend usa esse valor diretamente em vez de calcular.
	FixedCPM *float64 `json:"fixed_cpm"`
}
```

- [ ] **Step 2: Alterar a query**

Em `FinancialsByCampaign` (`campaigns.go:485-547`):

Na CTE `per_ins`, adicione a coluna de audiência no target e o join. O bloco vira:

```sql
		WITH per_ins AS (
			-- Investimento, inserções e audiência no modo per_insertion:
			-- audience = (in_slot + bonus) × stations.pmm somado por campanha.
			-- audience_target = mesma soma trocando pmm por pmm_target.
			SELECT
				p.campaign_id,
				COALESCE(SUM(tp.unit_value * (s.in_slot + s.bonus)), 0)::float8 AS invested,
				COALESCE(SUM(s.in_slot + s.bonus), 0)::int                    AS insertions,
				COALESCE(SUM((s.in_slot + s.bonus) * COALESCE(st.pmm, 0)), 0)::float8 AS audience,
				COALESCE(SUM((s.in_slot + s.bonus) * COALESCE(cst.pmm_target, 0)), 0)::float8 AS audience_target
			FROM campaign_station_pricing p
			JOIN campaigns cc ON cc.id = p.campaign_id
			JOIN campaign_station_type_pricing tp
				ON tp.campaign_id = p.campaign_id
			   AND tp.station_id  = p.station_id
			LEFT JOIN daily_play_summary s
				ON s.campaign_id = p.campaign_id
			   AND s.station_id  = p.station_id
			   AND s.type_id     = tp.type_id
			LEFT JOIN stations st
				ON st.id = p.station_id
			LEFT JOIN client_station_pmm cst
				ON cst.client_id = cc.client_id AND cst.station_id = p.station_id
			WHERE p.mode = 'per_insertion'
			GROUP BY p.campaign_id
		),
```

Na CTE `consolidated_ins`, o mesmo tratamento:

```sql
		consolidated_ins AS (
			-- Inserções e audiência de emissoras em modo consolidado entram no
			-- denominador do CPM (mesma definição pra ambos os modos).
			SELECT
				p.campaign_id,
				COALESCE(SUM(s.in_slot + s.bonus), 0)::int AS insertions,
				COALESCE(SUM((s.in_slot + s.bonus) * COALESCE(st.pmm, 0)), 0)::float8 AS audience,
				COALESCE(SUM((s.in_slot + s.bonus) * COALESCE(cst.pmm_target, 0)), 0)::float8 AS audience_target
			FROM campaign_station_pricing p
			JOIN campaigns cc ON cc.id = p.campaign_id
			LEFT JOIN daily_play_summary s
				ON s.campaign_id = p.campaign_id
			   AND s.station_id  = p.station_id
			LEFT JOIN stations st
				ON st.id = p.station_id
			LEFT JOIN client_station_pmm cst
				ON cst.client_id = cc.client_id AND cst.station_id = p.station_id
			WHERE p.mode = 'consolidated'
			GROUP BY p.campaign_id
		),
		target_cov AS (
			-- Cobertura do cadastro: quantas emissoras COM PRICING da campanha
			-- têm target. CTE separada porque somar as contagens das duas CTEs
			-- acima contaria em dobro emissoras presentes nos dois modos.
			SELECT p.campaign_id,
			       COUNT(DISTINCT p.station_id)::int AS stations_with_target
			FROM campaign_station_pricing p
			JOIN campaigns cc ON cc.id = p.campaign_id
			JOIN client_station_pmm cst
			  ON cst.client_id = cc.client_id AND cst.station_id = p.station_id
			GROUP BY p.campaign_id
		)
```

No SELECT final, depois da linha `... AS total_audience,` (`:540`), adicione:

```sql
			COALESCE(per_ins.audience_target, 0) + COALESCE(consolidated_ins.audience_target, 0) AS total_audience_target,
			COALESCE(target_cov.stations_with_target, 0) AS stations_with_target,
```

e mais um LEFT JOIN junto aos outros três (`:543-545`):

```sql
		LEFT JOIN target_cov        ON target_cov.campaign_id        = c.id
```

- [ ] **Step 3: Atualizar o Scan**

Em `campaigns.go:556`:

```go
		if err := rows.Scan(&f.CampaignID, &f.TotalInvested, &f.TotalInsertions, &f.TotalAudience,
			&f.TotalAudienceTarget, &f.StationsWithTarget, &f.FixedCPM); err != nil {
```

- [ ] **Step 4: Compilar e rodar os testes do pacote**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./... && go test ./internal/catalog/ -run TestCampaign -v
```

Esperado: build limpo; testes de campanha passando (a ordem do Scan bate com a do SELECT).

- [ ] **Step 5: Verificar a query contra o banco**

O spec previa um teste Go para os financials com target. **Não escreva um:** a query depende da view `daily_play_summary`, que é derivada de `detections` + `distribution_rules` + pricing — montar o fixture custaria mais que o valor da asserção, e o teste ficaria acoplado à mecânica da view. A verificação é direta no banco.

Num banco com dados (cópia de prod restaurada num descartável, ou o de dev com pricing cadastrado), rode:

```sql
-- Escolha uma campanha cujo cliente tenha cadastro de target:
SELECT c.id, c.name, count(cst.*) AS emissoras_com_target
FROM campaigns c
JOIN client_station_pmm cst ON cst.client_id = c.client_id
GROUP BY c.id, c.name
ORDER BY 3 DESC LIMIT 5;
```

Depois chame `GET /v1/internal/campaigns/financials` e confirme, para essa campanha, que `total_audience_target > 0`, que `stations_with_target` bate com a contagem acima restrita às emissoras com pricing, e que `total_audience` continua **exatamente** igual ao valor de antes da mudança (rode a mesma chamada na `master` para comparar). Este último ponto é o critério que importa: a coluna nova não pode ter alterado a antiga.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/catalog/campaigns.go
git commit -m "feat(campaigns): audiencia no target nos financials"
```

---

## Task 6: `/detections` e `/reports/airtime` — PMM no target por linha

**Files:**
- Modify: `workers/internal/catalog/detections.go:670` (struct), `:704-746` (ListPaged), `:859-896` (IterateForExport)

- [ ] **Step 1: Adicionar o campo à struct**

Em `detections.go:670`, logo abaixo de `StationPMM`:

```go
	StationPMM          *float64   `json:"station_pmm,omitempty"`
	// StationPMMTarget é o PMM no target do CLIENTE DONO da campanha desta
	// atribuição, resolvido por (cmp.client_id × station_id). nil = sem cadastro.
	StationPMMTarget    *int       `json:"station_pmm_target,omitempty"`
```

- [ ] **Step 2: Alterar o `ListPaged`**

Em `detections.go:711`, na lista de colunas, troque:

```sql
		       s.frequency_mhz, s.band, s.city, s.state, s.logo_url, s.pmm,
```

por:

```sql
		       s.frequency_mhz, s.band, s.city, s.state, s.logo_url, s.pmm, cst.pmm_target,
```

E logo depois de `LEFT JOIN clients cli ON cli.id = cmp.client_id` (`:721`), adicione:

```sql
			LEFT JOIN client_station_pmm cst
			       ON cst.client_id = cmp.client_id AND cst.station_id = d.station_id
```

No `Scan` (`:766`), troque `&det.StationLogoURL, &det.StationPMM,` por:

```go
			&det.StationLogoURL, &det.StationPMM, &det.StationPMMTarget,
```

- [ ] **Step 3: Aplicar as MESMAS três mudanças no `IterateForExport`**

Linhas correspondentes: coluna em `:867`, join depois de `:876`, scan em `:912`. O texto a inserir é idêntico ao do passo anterior.

- [ ] **Step 4: Compilar**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```

Esperado: sem saída.

- [ ] **Step 5: Adicionar a coluna ao CSV detalhado**

Em `workers/internal/api/handlers/detections.go:630-633`, o header vira:

```go
	_ = cw.Write([]string{
		"Data", "Hora", "Emissora", "Frequência", "Banda", "Cidade", "UF",
		"Material", "Duração (s)", "Tipo", "Cliente", "PMM", "PMM no target", "Status",
	})
```

Em `:643-646`, logo depois do bloco que formata `pmm`, adicione:

```go
		pmmTarget := ""
		if d.StationPMMTarget != nil {
			pmmTarget = fmt.Sprintf("%d", *d.StationPMMTarget)
		}
```

E na linha de escrita (`:663`), insira `pmmTarget` imediatamente depois de `pmm`.

- [ ] **Step 6: Compilar e conferir a contagem de colunas**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./... && \
  grep -c '"' workers/internal/api/handlers/detections.go > /dev/null && echo ok
```

Confira **manualmente** que a linha de `cw.Write` do corpo tem exatamente 14 elementos, batendo com os 14 do header. Um desalinhamento aqui não quebra o build — só produz um CSV torto.

- [ ] **Step 7: Commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/api/handlers/detections.go
git commit -m "feat(detections): PMM no target por linha e no CSV detalhado"
```

---

## Task 7: Relatórios de campanha — backend

**Files:**
- Modify: `workers/internal/catalog/detections.go:1098-1175` (MaterialStationRow + query), `:1176-1210` (StationAggregateRow + query)
- Modify: `workers/internal/api/handlers/reports.go:132-197` (CSV), `:199-282` (Summary)

- [ ] **Step 1: Estender `MaterialStationRow`**

Em `detections.go:1098-1118`, adicione dois campos depois de `StationState`:

```go
	StationState        *string   `json:"station_state,omitempty"`
	StationPMM          *float64  `json:"station_pmm,omitempty"`
	StationPMMTarget    *int      `json:"station_pmm_target,omitempty"`
```

- [ ] **Step 2: Alterar a query do `AggregateByMaterialStation`**

Abra `detections.go:1124-1175`. Na lista de colunas do SELECT, depois de `s.city, s.state` (ou equivalente — confira o texto exato), acrescente `, s.pmm, cst.pmm_target`. Adicione o join do cliente e do target logo após o `LEFT JOIN stations s`:

```sql
		LEFT JOIN campaigns cmp ON cmp.id = d.campaign_id
		LEFT JOIN client_station_pmm cst
		       ON cst.client_id = cmp.client_id AND cst.station_id = d.station_id
```

Acrescente `s.pmm, cst.pmm_target` ao `GROUP BY` (a query agrega por material × emissora; ambas são funcionalmente dependentes de `d.station_id`, mas o Postgres exige que estejam no GROUP BY porque a PK de `stations` não está lá). Acrescente os dois ponteiros ao `Scan`, na mesma posição relativa da lista de colunas.

- [ ] **Step 3: Fazer o mesmo em `AggregateByStation`**

Em `detections.go:1176-1184`, a struct ganha:

```go
	StationPMM       *float64 `json:"station_pmm,omitempty"`
	StationPMMTarget *int     `json:"station_pmm_target,omitempty"`
```

Na query (`:1189-1202`): colunas `s.pmm, cst.pmm_target` no SELECT, os dois joins acima, `s.pmm, cst.pmm_target` no GROUP BY, e os dois ponteiros no Scan.

- [ ] **Step 4: Compilar**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```

Esperado: sem saída. Se der erro de GROUP BY em runtime (não aparece no build), o teste do passo 7 pega.

- [ ] **Step 5: Adicionar as colunas ao CSV consolidado**

Em `handlers/reports.go:153-161`, o header vira:

```go
	_ = cw.Write([]string{
		"ID Material", "Material", "Tipo", "Duração (s)",
		"Emissora", "Frequência", "Banda", "Cidade", "UF",
		"Total Veiculações",
		// Breakdown por status — útil pra fechamento (saber quanto foi
		// bônus, quanto foi fora-faixa, etc. dentro de cada combinação).
		"Dentro da faixa", "Fora da faixa", "Fora da data", "Bônus",
		// Impactos = Total Veiculações × PMM da emissora. A coluna "no target"
		// usa o PMM no target do cliente dono da campanha; vazia quando não há
		// cadastro (não confundir com zero).
		"PMM", "Impactos", "PMM no target", "Impactos no target",
		"Primeira", "Última",
	})
```

No loop (`:164-195`), antes do `cw.Write`, adicione:

```go
		pmmStr, impactosStr := "", ""
		if row.StationPMM != nil {
			pmmStr = strings.ReplaceAll(fmt.Sprintf("%.0f", *row.StationPMM), ".", ",")
			impactosStr = fmt.Sprintf("%.0f", *row.StationPMM*float64(row.Count))
		}
		pmmTargetStr, impactosTargetStr := "", ""
		if row.StationPMMTarget != nil {
			pmmTargetStr = fmt.Sprintf("%d", *row.StationPMMTarget)
			impactosTargetStr = fmt.Sprintf("%d", *row.StationPMMTarget*row.Count)
		}
```

E insira os quatro valores na linha escrita, entre `fmt.Sprintf("%d", row.OrphanCount),` e `row.FirstDetectedAt...`:

```go
			pmmStr,
			impactosStr,
			pmmTargetStr,
			impactosTargetStr,
```

- [ ] **Step 6: Adicionar os totais ao JSON do PDF**

Em `handlers/reports.go:217-221`, o bloco `Totals` vira:

```go
	Totals struct {
		Detections        int   `json:"detections"`
		DistinctMaterials int   `json:"distinct_materials"`
		DistinctStations  int   `json:"distinct_stations"`
		// Impactos = Σ (veiculações da emissora × PMM). ImpactosTarget usa o
		// PMM no target; StationsWithTarget > 0 é o gate de exibição no PDF.
		Impactos           int64 `json:"impactos"`
		ImpactosTarget     int64 `json:"impactos_target"`
		StationsWithTarget int   `json:"stations_with_target"`
	} `json:"totals"`
```

Em `Summary` (`:272-274`), depois de `resp.Totals.DistinctStations = len(byStation)`, adicione:

```go
	// Impactos derivados de byStation (uma linha por emissora), não de
	// byMaterialStation — senão a mesma emissora entraria uma vez por material.
	for _, s := range byStation {
		if s.StationPMM != nil {
			resp.Totals.Impactos += int64(*s.StationPMM * float64(s.Count))
		}
		if s.StationPMMTarget != nil {
			resp.Totals.ImpactosTarget += int64(*s.StationPMMTarget * s.Count)
			resp.Totals.StationsWithTarget++
		}
	}
```

- [ ] **Step 7: Compilar e exercitar as queries**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./... && go test ./internal/catalog/ -run "TestDetections|TestAggregate" -v
```

Esperado: build limpo. Se não houver teste cobrindo esses agregados, valide manualmente contra o banco de teste:

```bash
docker exec rc-test-pg psql -U postgres -d radiocheck -c \
  "SELECT count(*) FROM client_station_pmm"
```

e chame `GET /reports/campaigns/{id}/consolidated.csv` no ambiente local conferindo que o CSV abre com as 4 colunas novas preenchidas.

- [ ] **Step 8: Commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/api/handlers/reports.go
git commit -m "feat(reports): impactos e impactos no target no CSV consolidado e no PDF"
```

---

## Task 8: Parser da colagem de planilha (frontend, puro)

**Files:**
- Create: `frontend/src/utils/targetPmmPaste.js`
- Test: `frontend/src/utils/targetPmmPaste.test.mjs`

O parser é puro (sem React, sem rede) para poder ser testado com o runner nativo do Node — o frontend **não tem** test runner instalado e adicionar um exigiria `npm install`, que poda o lockfile no Windows (regra 5 do `CLAUDE.md`).

- [ ] **Step 1: Escrever o teste que falha**

`frontend/src/utils/targetPmmPaste.test.mjs`:

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import { parseNumberBR, parsePastedTargets } from './targetPmmPaste.js'

const stations = [
  { station_id: 'a', short_id: 12, name: 'Rádio Alfa', frequency_mhz: 99.5 },
  { station_id: 'b', short_id: 34, name: 'Radio Beta', frequency_mhz: 101.1 },
  { station_id: 'c', short_id: 56, name: 'Rádio Beta', frequency_mhz: 88.3 },
]

test('numero BR: ponto e milhar, virgula e decimal', () => {
  assert.equal(parseNumberBR('12.345'), 12345)
  assert.equal(parseNumberBR('12345'), 12345)
  assert.equal(parseNumberBR('3.400,6'), 3401)
  assert.equal(parseNumberBR(' 1 200 '), 1200)
  assert.equal(parseNumberBR('abc'), null)
  assert.equal(parseNumberBR(''), null)
  assert.equal(parseNumberBR('-5'), null)
})

test('casa por short_id exato', () => {
  const r = parsePastedTargets('12\t3400', stations)
  assert.equal(r.matched.length, 1)
  assert.equal(r.matched[0].station_id, 'a')
  assert.equal(r.matched[0].pmm_target, 3400)
})

test('casa por nome normalizado (sem acento, sem caixa)', () => {
  const r = parsePastedTargets('radio alfa;1200', stations)
  assert.equal(r.matched.length, 1)
  assert.equal(r.matched[0].station_id, 'a')
})

test('nome duplicado sem dial vira ambiguo', () => {
  const r = parsePastedTargets('radio beta\t900', stations)
  assert.equal(r.matched.length, 0)
  assert.equal(r.ambiguous.length, 1)
  assert.equal(r.ambiguous[0].line, 1)
})

test('nome duplicado COM dial desempata', () => {
  const r = parsePastedTargets('radio beta 88,3\t900', stations)
  assert.equal(r.matched.length, 1)
  assert.equal(r.matched[0].station_id, 'c')
})

test('emissora inexistente vira notFound', () => {
  const r = parsePastedTargets('Radio Fantasma\t900', stations)
  assert.equal(r.notFound.length, 1)
  assert.equal(r.notFound[0].raw, 'Radio Fantasma')
})

test('linhas vazias e cabecalho sao ignorados', () => {
  const r = parsePastedTargets('Emissora\tPMM Target\n\n12\t3400\n', stations)
  assert.equal(r.matched.length, 1)
  assert.equal(r.notFound.length, 0)
})

test('valor invalido vira notFound com motivo', () => {
  const r = parsePastedTargets('12\tabc', stations)
  assert.equal(r.matched.length, 0)
  assert.equal(r.invalid.length, 1)
})
```

- [ ] **Step 2: Rodar e ver falhar**

```bash
node --test frontend/src/utils/targetPmmPaste.test.mjs
```

Esperado: FAIL — `Cannot find module ... targetPmmPaste.js`.

- [ ] **Step 3: Implementar o parser**

`frontend/src/utils/targetPmmPaste.js`:

```js
// Parser da colagem de planilha da tela /clients/:id/target-pmm.
//
// Puro de propósito (sem React, sem rede): roda no browser e no runner nativo
// do Node (`node --test`), o que permite testar sem instalar dependência de
// frontend — `npm install` no Windows poda as optional deps linux do lockfile
// e quebra o build do Cloudflare Pages (regra 5 do CLAUDE.md).

// normalize tira acento, colapsa espaço e baixa a caixa — a mesma régua que o
// backend usa com unaccent(lower(...)) nas buscas de emissora.
export function normalize(s) {
  return String(s ?? '')
    .normalize('NFD').replace(/\p{Diacritic}/gu, '')
    .toLowerCase().replace(/\s+/g, ' ').trim()
}

// parseNumberBR lê número em formato brasileiro: '.' é separador de MILHAR e é
// removido; ',' é decimal e o valor é arredondado. Devolve null quando não é um
// inteiro >= 0 reconhecível. '12.345' → 12345 (doze mil), nunca 12,345.
export function parseNumberBR(raw) {
  const s = String(raw ?? '').replace(/\s/g, '')
  if (!s) return null
  if (!/^\d{1,3}(\.\d{3})*(,\d+)?$|^\d+(,\d+)?$/.test(s)) return null
  const n = Number(s.replace(/\./g, '').replace(',', '.'))
  if (!Number.isFinite(n) || n < 0) return null
  return Math.round(n)
}

// splitLine aceita TAB (colagem do Excel), ';' e ',' seguido de espaço.
// Vírgula sozinha NÃO separa: ela é decimal e aparece dentro do dial ('88,3').
function splitLine(line) {
  if (line.includes('\t')) return line.split('\t')
  if (line.includes(';')) return line.split(';')
  const m = line.match(/^(.*?)[,\s]+(\d[\d.,]*)$/)
  return m ? [m[1], m[2]] : [line]
}

// extractDial acha um dial (99.5 / 99,5) no texto e devolve [semDial, dial].
function extractDial(text) {
  const m = text.match(/(\d{2,3}[.,]\d)/)
  if (!m) return [text, null]
  return [text.replace(m[1], '').trim(), Number(m[1].replace(',', '.'))]
}

/**
 * parsePastedTargets casa cada linha colada com uma emissora da lista.
 *
 * Ordem de casamento: short_id exato → nome normalizado único → nome + dial.
 * Nome que bate em mais de uma emissora e não tem dial vira `ambiguous`.
 *
 * @param {string} text     conteúdo colado (TSV/CSV)
 * @param {Array}  stations linhas de GET /clients/:id/target-pmm
 * @returns {{matched: Array, ambiguous: Array, notFound: Array, invalid: Array}}
 */
export function parsePastedTargets(text, stations) {
  const byShortID = new Map()
  const byName = new Map()
  for (const st of stations) {
    byShortID.set(String(st.short_id), st)
    const key = normalize(st.name)
    if (!byName.has(key)) byName.set(key, [])
    byName.get(key).push(st)
  }

  const matched = [], ambiguous = [], notFound = [], invalid = []
  const lines = String(text ?? '').split(/\r?\n/)

  lines.forEach((line, i) => {
    const lineNo = i + 1
    if (!line.trim()) return

    const parts = splitLine(line)
    if (parts.length < 2) { notFound.push({ line: lineNo, raw: line.trim() }); return }

    const rawKey = parts[0].trim()
    const value = parseNumberBR(parts[parts.length - 1])

    // Cabeçalho de planilha: primeira linha cujo valor não é número.
    if (value === null) {
      if (lineNo === 1) return
      invalid.push({ line: lineNo, raw: rawKey, value: parts[parts.length - 1].trim() })
      return
    }

    if (byShortID.has(rawKey)) {
      matched.push({ station_id: byShortID.get(rawKey).station_id, name: byShortID.get(rawKey).name, pmm_target: value, line: lineNo })
      return
    }

    const [nameOnly, dial] = extractDial(rawKey)
    const candidates = byName.get(normalize(nameOnly)) ?? byName.get(normalize(rawKey)) ?? []

    if (candidates.length === 0) { notFound.push({ line: lineNo, raw: rawKey }); return }
    if (candidates.length === 1) {
      matched.push({ station_id: candidates[0].station_id, name: candidates[0].name, pmm_target: value, line: lineNo })
      return
    }
    const byDial = dial != null
      ? candidates.filter(st => Math.abs(Number(st.frequency_mhz) - dial) < 0.05)
      : []
    if (byDial.length === 1) {
      matched.push({ station_id: byDial[0].station_id, name: byDial[0].name, pmm_target: value, line: lineNo })
      return
    }
    ambiguous.push({ line: lineNo, raw: rawKey, candidates: candidates.map(st => st.name) })
  })

  return { matched, ambiguous, notFound, invalid }
}
```

- [ ] **Step 4: Rodar e ver passar**

```bash
node --test frontend/src/utils/targetPmmPaste.test.mjs
```

Esperado: `# pass 8`, `# fail 0`.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/utils/targetPmmPaste.js frontend/src/utils/targetPmmPaste.test.mjs
git commit -m "feat(frontend): parser de colagem de PMM no target"
```

---

## Task 9: Hooks de API do frontend

**Files:**
- Modify: `frontend/src/api/hooks.js:125` (depois de `useActivateClient`)

- [ ] **Step 1: Adicionar os hooks**

Logo abaixo de `useActivateClient` (`hooks.js:119-125`), antes do comentário `// Campaigns`:

```js
// PMM no target por cliente — cadastro em /clients/:id/target-pmm e leitura
// pelas telas de veiculação (grid de /detections). A lista traz as emissoras-
// alvo das campanhas do cliente com pmm (global) e pmm_target (null = não
// cadastrado, que é DIFERENTE de zero).
export function useClientTargetPmm(clientId, { enabled = true } = {}) {
  return useQuery({
    queryKey: ['client-target-pmm', clientId],
    queryFn: () => api.get(`/clients/${clientId}/target-pmm`).then(r => r.data.data ?? []),
    enabled: enabled && !!clientId,
  })
}

// Bulk upsert: entries com pmm_target null APAGAM a linha (voltam pra "não
// cadastrado"). Manda só as linhas alteradas.
export function useSaveClientTargetPmm() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ clientId, entries }) =>
      api.put(`/clients/${clientId}/target-pmm`, { entries }).then(r => r.data),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: ['client-target-pmm', vars.clientId] })
      // Impactos no target mudaram → as telas que os exibem precisam refazer.
      qc.invalidateQueries({ queryKey: ['insights'] })
      qc.invalidateQueries({ queryKey: ['campaign-financials'] })
    },
  })
}
```

Confira as queryKeys reais de insights e financials antes de commitar:

```bash
grep -n "queryKey: \['insights'\|queryKey: \['campaign" frontend/src/api/hooks.js
```

Ajuste as duas linhas de `invalidateQueries` para as chaves que existirem.

- [ ] **Step 2: Verificar o lint**

```bash
cd frontend && npm run lint
```

Esperado: sem erro novo (o projeto pode ter warnings pré-existentes).

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/hooks.js
git commit -m "feat(frontend): hooks de PMM no target"
```

---

## Task 10: Tela de cadastro `/clients/:id/target-pmm`

**Files:**
- Create: `frontend/src/pages/ClientTargetPmmPage.jsx`
- Modify: `frontend/src/App.jsx:112` (rota)
- Modify: `frontend/src/pages/ClientsPage.jsx` (link na linha do cliente)

- [ ] **Step 1: Criar a página**

`frontend/src/pages/ClientTargetPmmPage.jsx`:

```jsx
import { useMemo, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useClients, useClientTargetPmm, useSaveClientTargetPmm } from '../api/hooks'
import { parsePastedTargets } from '../utils/targetPmmPaste'

const fmtInt = new Intl.NumberFormat('pt-BR')

// Tela de cadastro do PMM no target por cliente. Lista as emissoras-alvo das
// campanhas do cliente (não o catálogo inteiro) e permite editar linha a linha
// ou colar duas colunas de planilha.
//
// Campo vazio = APAGA o cadastro (volta pra "não cadastrado"). Zero é um valor
// legítimo e diferente disso: conta como cadastrado, somando zero impactos.
export default function ClientTargetPmmPage() {
  const { id } = useParams()
  const { data: clients } = useClients()
  const { data: rows = [], isLoading } = useClientTargetPmm(id)
  const save = useSaveClientTargetPmm()

  // draft: station_id → string do input. Só as chaves tocadas entram aqui.
  const [draft, setDraft] = useState({})
  const [pasteOpen, setPasteOpen] = useState(false)
  const [pasteText, setPasteText] = useState('')
  const [preview, setPreview] = useState(null)

  const client = (clients ?? []).find(c => c.id === id)

  const withTarget = useMemo(
    () => rows.filter(r => (draft[r.station_id] ?? (r.pmm_target != null ? String(r.pmm_target) : '')) !== '').length,
    [rows, draft],
  )

  function valueOf(row) {
    return draft[row.station_id] ?? (row.pmm_target != null ? String(row.pmm_target) : '')
  }

  function setValue(stationId, v) {
    setDraft(d => ({ ...d, [stationId]: v }))
  }

  // Só manda o que mudou de fato — evita reescrever 200 linhas por causa de uma.
  function dirtyEntries() {
    const out = []
    for (const row of rows) {
      if (!(row.station_id in draft)) continue
      const raw = draft[row.station_id].trim()
      const next = raw === '' ? null : Number(raw)
      const prev = row.pmm_target ?? null
      if (next === prev) continue
      out.push({ station_id: row.station_id, pmm_target: next })
    }
    return out
  }

  async function handleSave() {
    const entries = dirtyEntries()
    if (entries.length === 0) return
    await save.mutateAsync({ clientId: id, entries })
    setDraft({})
  }

  function handlePreview() {
    setPreview(parsePastedTargets(pasteText, rows))
  }

  function applyPreview() {
    if (!preview) return
    setDraft(d => {
      const next = { ...d }
      for (const m of preview.matched) next[m.station_id] = String(m.pmm_target)
      return next
    })
    setPasteOpen(false)
    setPasteText('')
    setPreview(null)
  }

  const pendingCount = dirtyEntries().length

  return (
    <div className="page">
      <header className="page-head">
        <div>
          <h1>PMM no target</h1>
          <p className="page-sub">
            {client?.name ?? 'Cliente'} · {fmtInt.format(withTarget)} de {fmtInt.format(rows.length)} emissoras com PMM no target
          </p>
        </div>
        <div className="page-actions">
          <Link to="/clients" className="btn btn-ghost">Voltar</Link>
          <button type="button" className="btn btn-secondary" onClick={() => setPasteOpen(true)}>
            Colar planilha
          </button>
          <button type="button" className="btn btn-primary" onClick={handleSave}
                  disabled={pendingCount === 0 || save.isPending}>
            {save.isPending ? 'Salvando…' : `Salvar${pendingCount ? ` (${pendingCount})` : ''}`}
          </button>
        </div>
      </header>

      {isLoading ? (
        <p className="muted">Carregando emissoras…</p>
      ) : rows.length === 0 ? (
        <p className="muted">
          Este cliente não tem emissoras-alvo. Inclua emissoras em alguma campanha dele para cadastrar o PMM no target.
        </p>
      ) : (
        <table className="table">
          <thead>
            <tr>
              <th>Emissora</th>
              <th className="num">PMM da emissora</th>
              <th className="num">PMM no target</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {rows.map(row => (
              <tr key={row.station_id}>
                <td>
                  <strong>{row.name}</strong>
                  <span className="muted">
                    {row.band}{row.frequency_mhz != null ? ` ${String(row.frequency_mhz).replace('.', ',')}` : ''}
                    {row.city ? ` · ${row.city}` : ''}{row.state ? `/${row.state}` : ''}
                  </span>
                </td>
                <td className="num">{row.pmm != null ? fmtInt.format(Math.round(row.pmm)) : '—'}</td>
                <td className="num">
                  <input
                    type="number" min="0" step="1"
                    className="input input-sm"
                    value={valueOf(row)}
                    placeholder="não cadastrado"
                    onChange={e => setValue(row.station_id, e.target.value)}
                  />
                </td>
                <td>
                  <button type="button" className="btn btn-ghost btn-sm"
                          onClick={() => setValue(row.station_id, '')}
                          disabled={valueOf(row) === ''}>
                    Limpar
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {pasteOpen && (
        <div className="modal-backdrop" role="dialog" aria-label="Colar planilha">
          <div className="modal">
            <h2>Colar planilha</h2>
            <p className="muted">
              Duas colunas: emissora (ID ou nome) e o PMM no target. Cole direto do Excel.
              Números em formato brasileiro — <code>12.345</code> vale doze mil trezentos e quarenta e cinco.
            </p>
            <textarea rows={10} className="input" value={pasteText}
                      onChange={e => { setPasteText(e.target.value); setPreview(null) }} />

            {preview && (
              <div className="paste-preview">
                <p><strong>{preview.matched.length}</strong> emissoras casadas.</p>
                {preview.ambiguous.length > 0 && (
                  <details>
                    <summary>{preview.ambiguous.length} ambíguas (não serão aplicadas)</summary>
                    <ul>{preview.ambiguous.map(a => (
                      <li key={a.line}>Linha {a.line}: “{a.raw}” casa com {a.candidates.join(', ')}</li>
                    ))}</ul>
                  </details>
                )}
                {preview.notFound.length > 0 && (
                  <details>
                    <summary>{preview.notFound.length} não encontradas</summary>
                    <ul>{preview.notFound.map(n => <li key={n.line}>Linha {n.line}: “{n.raw}”</li>)}</ul>
                  </details>
                )}
                {preview.invalid.length > 0 && (
                  <details>
                    <summary>{preview.invalid.length} com valor inválido</summary>
                    <ul>{preview.invalid.map(v => (
                      <li key={v.line}>Linha {v.line}: “{v.raw}” → “{v.value}”</li>
                    ))}</ul>
                  </details>
                )}
              </div>
            )}

            <div className="modal-actions">
              <button type="button" className="btn btn-ghost"
                      onClick={() => { setPasteOpen(false); setPreview(null); setPasteText('') }}>
                Cancelar
              </button>
              {preview ? (
                <button type="button" className="btn btn-primary" onClick={applyPreview}
                        disabled={preview.matched.length === 0}>
                  Aplicar {preview.matched.length} linhas
                </button>
              ) : (
                <button type="button" className="btn btn-primary" onClick={handlePreview}
                        disabled={!pasteText.trim()}>
                  Conferir
                </button>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
```

As classes CSS usadas (`page`, `page-head`, `btn`, `input`, `table`, `modal`) são as do design system — confira os nomes reais em [docs/architecture/frontend-design-system.md](../../architecture/frontend-design-system.md) e ajuste se divergirem. Não crie tokens novos.

- [ ] **Step 2: Registrar a rota**

Em `frontend/src/App.jsx`, adicione o import junto aos outros de página e a rota logo depois da de api-keys (linha 110-112):

```jsx
            <Route path="/clients/:id/target-pmm" element={
              <RequireRole roles={['admin']}><ClientTargetPmmPage /></RequireRole>
            } />
```

- [ ] **Step 3: Adicionar o link na listagem de clientes**

Em `frontend/src/pages/ClientsPage.jsx`, ache onde os links de "Webhooks" e "API keys" da linha do cliente são renderizados:

```bash
grep -n "api-keys\|webhooks" frontend/src/pages/ClientsPage.jsx
```

Adicione, no mesmo bloco e seguindo o mesmo padrão de markup do vizinho:

```jsx
<Link to={`/clients/${c.id}/target-pmm`} className="btn btn-ghost btn-sm">PMM no target</Link>
```

- [ ] **Step 4: Verificar build e lint**

```bash
cd frontend && npm run lint && npm run build
```

Esperado: build conclui. **Não** rode `npm install`.

- [ ] **Step 5: Conferir que o lockfile não foi podado**

```bash
git show master:frontend/package-lock.json | grep -c emnapi
grep -c emnapi frontend/package-lock.json
```

Esperado: os dois números iguais. Se o segundo caiu, algo rodou `npm install` — restaure o lockfile com `git checkout master -- frontend/package-lock.json` antes de commitar.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/ClientTargetPmmPage.jsx frontend/src/App.jsx frontend/src/pages/ClientsPage.jsx
git commit -m "feat(frontend): tela de cadastro de PMM no target por cliente"
```

---

## Task 11: `/insights` — cards no frontend

**Files:**
- Modify: `frontend/src/components/insights/KpiCards.jsx`

- [ ] **Step 1: Adicionar os dois cards condicionais**

Substitua o conteúdo de `KpiCards.jsx` por:

```jsx
import { IconChartBars, IconMoney, IconGift } from './icons'

const fmtBR = new Intl.NumberFormat('pt-BR')
const fmtCurrency = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })

export default function KpiCards({ data }) {
  const k = data?.kpis
  if (!k) return null
  // Bloco "no target" só aparece quando o cliente tem cadastro. Sem cadastro,
  // a tela fica idêntica à de antes da feature.
  const hasTarget = (k.stations_with_target ?? 0) > 0
  return (
    <>
      <div className="in-card" title="Impactos = soma de (detecções × PMM) por estação.">
        <div className="in-card-head">
          <span className="in-card-icon"><IconChartBars /></span>
          <span className="in-card-label">Impactos</span>
        </div>
        <div className="in-card-value in-card-value--num">{fmtBR.format(k.impactos)}</div>
      </div>

      {hasTarget && (
        <div className="in-card" title={`Impactos no target = soma de (detecções × PMM no target do cliente) por estação. ${k.stations_with_target} de ${k.stations_count} emissoras com target cadastrado.`}>
          <div className="in-card-head">
            <span className="in-card-icon"><IconChartBars /></span>
            <span className="in-card-label">Impactos no target</span>
          </div>
          <div className="in-card-value in-card-value--num">{fmtBR.format(k.impactos_target)}</div>
          <div className="in-card-sub">{k.stations_with_target} de {k.stations_count} emissoras</div>
        </div>
      )}

      <div className="in-card">
        <div className="in-card-head">
          <span className="in-card-icon"><IconMoney /></span>
          <span className="in-card-label">CPM</span>
        </div>
        <div className="in-card-value">{fmtCurrency.format(k.cpm)}</div>
      </div>

      {hasTarget && (
        <div className="in-card" title="CPM no target = investido executado ÷ impactos no target × 1000. Sempre dinâmico, mesmo em campanha com CPM fixo — o CPM fixo é contratado sobre a audiência total, não sobre o recorte de público-alvo.">
          <div className="in-card-head">
            <span className="in-card-icon"><IconMoney /></span>
            <span className="in-card-label">CPM no target</span>
          </div>
          <div className="in-card-value">{fmtCurrency.format(k.cpm_target ?? 0)}</div>
        </div>
      )}

      {/* Consolidado (estilo fornecedor): a Bonificação some — o card não é
          renderizado, e o grid de cards vira 4 colunas (ver InsightsPage). */}
      {!data?.consolidated && (
        <div className="in-card">
          <div className="in-card-head">
            <span className="in-card-icon"><IconGift /></span>
            <span className="in-card-label">Bonificação</span>
          </div>
          <div className="in-card-value">{fmtCurrency.format(k.bonificacao.valor)}</div>
        </div>
      )}
    </>
  )
}
```

- [ ] **Step 2: Ajustar a contagem de colunas do grid**

`InsightsPage` decide o número de colunas do grid de cards em função de `data.consolidated`. Ache a lógica:

```bash
grep -n "consolidated" frontend/src/pages/InsightsPage.jsx frontend/src/pages/InsightsPage.css
```

Com o target ligado entram até 2 cards a mais. Se o grid tiver contagem fixa de colunas, troque para um layout que quebra sozinho (`grid-template-columns: repeat(auto-fit, minmax(180px, 1fr))`) em `InsightsPage.css`, em vez de multiplicar os casos condicionais.

- [ ] **Step 3: Adicionar o estilo do sub-rótulo, se não existir**

```bash
grep -n "in-card-sub" frontend/src/pages/InsightsPage.css
```

Se não existir, adicione junto às outras regras `.in-card-*`:

```css
.in-card-sub {
  margin-top: 4px;
  font-size: 11px;
  color: var(--text-muted);
}
```

- [ ] **Step 4: Build**

```bash
cd frontend && npm run lint && npm run build
```

Esperado: build conclui.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/insights/KpiCards.jsx frontend/src/pages/InsightsPage.css frontend/src/pages/InsightsPage.jsx
git commit -m "feat(insights): cards de impactos e CPM no target"
```

---

## Task 12: `/detections` — pill na grid

**Files:**
- Modify: `frontend/src/components/DistributionGrid.jsx:296-304` e `:405-524`
- Modify: `frontend/src/pages/DetectionsPage.jsx` (passar o mapa de targets)

- [ ] **Step 1: Passar o target para a célula**

Em `DistributionGrid.jsx:296-304`, adicione a prop:

```jsx
                  {ri === 0 && (
                    <StationTotalCell
                      rows={stationRows}
                      days={days}
                      cellData={cellData}
                      pricing={pricingByStation[row.stationId] ?? null}
                      pmm={Number(station.pmm) || 0}
                      pmmTarget={pmmTargetByStation[row.stationId] ?? null}
                      summary={summary}
                    />
                  )}
```

Declare a prop na assinatura do componente, junto de `pricingByStation` (`:45`):

```jsx
  // pmmTargetByStation: station_id → PMM no target do cliente dono da campanha.
  // Ausente/vazio = feature não cadastrada; a pill de target não é renderizada
  // e a grid fica idêntica à de antes.
  pmmTargetByStation = {},
```

- [ ] **Step 2: Renderizar a segunda pill**

Em `StationTotalCell`, atualize a assinatura (`:416`) e o cálculo (`:479`):

```jsx
function StationTotalCell({ rows, days, cellData, pricing, pmm, pmmTarget = null, summary = 'full' }) {
```

Depois da linha `const impactos = pmm > 0 ? pmm * inSlotStation : null` (`:479`), adicione:

```jsx
  // Espelha a base da própria tela (Σ in_slot), trocando pmm por pmm_target.
  // null = sem cadastro pra essa emissora → pill não renderizada.
  const impactosTarget = pmmTarget != null ? pmmTarget * inSlotStation : null
```

E logo depois da `<ValuePill tone="pink" ...>` de impactos (`:498-505`), insira:

```jsx
      {impactosTarget != null && (
        <ValuePill
          tone="pink"
          icon={<IconHeadset />}
          label={`${fmtImpactos(impactosTarget)} target`}
          hint={`${fmtInt(impactosTarget)} impactos no target = PMM no target ${fmtInt(pmmTarget)} × ${inSlotStation} veiculações na estação`}
        />
      )}
```

Atualize também o comentário de fórmula em `:410-415`, acrescentando a linha:

```
//   • Impactos no target = pmm_target × Σ in_slot (só quando cadastrado)
```

- [ ] **Step 3: Alimentar o mapa em `/detections`**

Em `frontend/src/pages/DetectionsPage.jsx`, ache o `<DistributionGrid` (perto da linha 1109) e o objeto da campanha selecionada:

```bash
grep -n "DistributionGrid\|client_id" frontend/src/pages/DetectionsPage.jsx | head -20
```

Importe o hook e construa o mapa:

```jsx
import { useClientTargetPmm } from '../api/hooks'
```

```jsx
  // PMM no target do cliente dono da campanha selecionada. Sem campanha (ou
  // sem cadastro) o mapa fica vazio e a grid não muda em nada.
  const { data: targetRows = [] } = useClientTargetPmm(campaign?.client_id, {
    enabled: !!campaign?.client_id,
  })
  const pmmTargetByStation = useMemo(() => {
    const m = {}
    for (const r of targetRows) if (r.pmm_target != null) m[r.station_id] = r.pmm_target
    return m
  }, [targetRows])
```

Use o nome real da variável da campanha selecionada no arquivo (pode ser `campaign`, `selectedCampaign` etc.) e passe a prop na grid:

```jsx
                pmmTargetByStation={pmmTargetByStation}
```

- [ ] **Step 4: Build**

```bash
cd frontend && npm run lint && npm run build
```

Esperado: build conclui.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/DistributionGrid.jsx frontend/src/pages/DetectionsPage.jsx
git commit -m "feat(detections): pill de impactos no target na grid"
```

---

## Task 13: `/reports/airtime` — pill por linha

**Files:**
- Modify: `frontend/src/components/AirtimeDetectionRow.jsx:123` e `:190-193`
- Modify: `frontend/src/pages/AirtimeReportPage.css`

- [ ] **Step 1: Renderizar a pill**

Em `AirtimeDetectionRow.jsx:123`, depois de `const pmm = fmtPMM(detection.station_pmm)`:

```jsx
  const pmmTarget = detection.station_pmm_target != null ? fmtPMM(detection.station_pmm_target) : null
```

E logo depois do bloco da pill de PMM (`:190-193`):

```jsx
      {pmmTarget != null && (
        <div className="airtime-row-pill airtime-row-pill-pmm airtime-row-pill-target"
             title={`PMM no target: ${detection.station_pmm_target}`}>
          <IconHeadset />
          <span>{pmmTarget}</span>
        </div>
      )}
```

- [ ] **Step 2: Ajustar o grid da linha**

O layout da linha é um grid com colunas fixas — a pill nova precisa de espaço. Ache a regra:

```bash
grep -n "airtime-row\b" frontend/src/pages/AirtimeReportPage.css | head
```

Na regra que define `grid-template-columns` de `.airtime-row`, acrescente uma coluna do mesmo tamanho da coluna de PMM, e adicione a variante visual:

```css
/* PMM no target: mesma pill do PMM, tom mais fraco pra ler como derivado. */
.airtime-row-pill-target {
  opacity: 0.75;
}
```

Se a coluna extra desalinhar as linhas sem target, torne a pill sempre renderizada com `—` quando ausente, em vez de condicional — a consistência de colunas vale mais que esconder a célula. Nesse caso troque o `{pmmTarget != null && (...)}` por render incondicional com `{pmmTarget ?? '—'}`.

- [ ] **Step 3: Build e conferência visual**

```bash
cd frontend && npm run lint && npm run build
```

Abra `/reports/airtime` no dev server (`npm run dev`) e confirme que as linhas continuam alinhadas com e sem target.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/AirtimeDetectionRow.jsx frontend/src/pages/AirtimeReportPage.css
git commit -m "feat(airtime): pill de PMM no target por linha"
```

---

## Task 14: `/campaigns` — impactos explícitos

**Files:**
- Modify: `frontend/src/pages/CampaignsPage.jsx:993-1029`

- [ ] **Step 1: Exibir impactos e impactos no target**

Substitua o corpo de `CampaignFinancials` (`:993-1029`) por:

```jsx
function CampaignFinancials({ financials, loading }) {
  // Enquanto a query de financials não resolveu, reserva o espaço com o
  // esqueleto de mesma forma — evita layout shift quando o valor entra.
  if (loading) return <CampaignFinancialsSkeleton />
  // Sem pricing cadastrado: nada a mostrar (o slot colapsa após o load).
  if (!financials || !(financials.total_invested > 0)) return null

  const {
    total_invested: inv, total_insertions: ins, total_audience: aud,
    total_audience_target: audTarget, stations_with_target: withTarget,
    fixed_cpm: fixed,
  } = financials
  // CPM fixo (quando setado na campanha) sobrescreve o cálculo dinâmico, pra
  // refletir o número comercial pré-acordado em vez do derivado de pricing.
  const dynamicCPM = aud > 0 ? (inv / aud) * 1000 : null
  const cpm = fixed != null ? fixed : dynamicCPM
  const isFixed = fixed != null
  // CPM no target é SEMPRE dinâmico — o CPM fixo é contratado sobre a audiência
  // total, não sobre o recorte de público-alvo.
  const hasTarget = (withTarget ?? 0) > 0 && audTarget > 0
  const cpmTarget = hasTarget ? (inv / audTarget) * 1000 : null
  const cpmTip = isFixed
    ? `CPM fixo da campanha: ${_BRL_CAMPAIGN_LIST.format(fixed)}. Investimento ${_BRL_CAMPAIGN_LIST.format(inv)} sobre ${ins} inserções (CPM dinâmico seria ${dynamicCPM != null ? _BRL_CAMPAIGN_LIST.format(dynamicCPM) : '—'}).`
    : cpm != null
      ? `${_BRL_CAMPAIGN_LIST.format(inv)} ÷ (${ins} inserções × PMM = ${Math.round(aud).toLocaleString('pt-BR')} impressões) × 1000`
      : ins > 0
        ? 'Emissoras sem PMM cadastrado — CPM indeterminado.'
        : 'Nenhuma inserção realizada ainda — CPM indeterminado.'

  return (
    <div className="campaign-fin">
      <span className="campaign-fin-label">CPM</span>
      <span
        className={`campaign-fin-value${cpm == null ? ' campaign-fin-value--empty' : ''}`}
        title={cpmTip}
      >
        {cpm != null ? _BRL_CAMPAIGN_LIST.format(cpm) : '—'}
        {isFixed && <span className="campaign-fin-tag">fixo</span>}
      </span>
      {hasTarget && (
        <span className="campaign-fin-sub"
              title={`CPM no target: ${_BRL_CAMPAIGN_LIST.format(cpmTarget)} — sempre dinâmico (investimento ÷ impactos no target × 1000).`}>
          <span className="campaign-fin-sub-label">CPM no target</span> {_BRL_CAMPAIGN_LIST.format(cpmTarget)}
        </span>
      )}
      <span className="campaign-fin-sub" title={`${Math.round(aud).toLocaleString('pt-BR')} impressões`}>
        <span className="campaign-fin-sub-label">Impactos</span> {Math.round(aud).toLocaleString('pt-BR')}
      </span>
      {hasTarget && (
        <span className="campaign-fin-sub"
              title={`${Math.round(audTarget).toLocaleString('pt-BR')} impactos no target · ${withTarget} emissoras com target cadastrado`}>
          <span className="campaign-fin-sub-label">Impactos no target</span> {Math.round(audTarget).toLocaleString('pt-BR')}
        </span>
      )}
      <span className="campaign-fin-sub" title={`Investimento total: ${_BRL_CAMPAIGN_LIST.format(inv)}`}>
        <span className="campaign-fin-sub-label">Investimento</span> {_BRL_CAMPAIGN_LIST.format(inv)}
      </span>
    </div>
  )
}
```

- [ ] **Step 2: Conferir o esqueleto de loading**

`CampaignFinancialsSkeleton` (logo abaixo, `:1031+`) reserva o espaço de três barras. Com até quatro linhas a mais, o layout shift volta. Ajuste o esqueleto para o número máximo de linhas, ou torne a caixa de altura fixa no CSS (`.campaign-fin { min-height: … }`).

- [ ] **Step 3: Build**

```bash
cd frontend && npm run lint && npm run build
```

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/CampaignsPage.jsx frontend/src/index.css
git commit -m "feat(campaigns): impactos e CPM no target explicitos na listagem"
```

---

## Task 15: PDF e CSV de grade

**Files:**
- Modify: `frontend/src/utils/pdfReport.js:289-300` (KPIs) e a tabela por emissora
- Modify: `frontend/src/utils/gridReport.js`

- [ ] **Step 1: Adicionar os KPIs ao PDF de campanha**

Em `pdfReport.js:289-300`, o bloco de 3 KPIs vira condicional a 3 ou 5. Substitua por:

```js
  // 3) KPIs. Sem cadastro de PMM no target: 3 boxes, idêntico ao anterior.
  //    Com cadastro: 5 boxes (Impactos e Impactos no target entram).
  const kpiY = 76
  const kpiH = 22
  const kpiGap = 4
  const pageW = doc.internal.pageSize.getWidth()
  const hasTarget = (summary.totals?.stations_with_target ?? 0) > 0
  const kpis = [
    ['Veiculações', fmtNumber(summary.totals?.detections)],
    ['Materiais', fmtNumber(summary.totals?.distinct_materials)],
    ['Emissoras', fmtNumber(summary.totals?.distinct_stations)],
    ['Impactos', fmtNumber(summary.totals?.impactos)],
  ]
  if (hasTarget) kpis.push(['Impactos no target', fmtNumber(summary.totals?.impactos_target)])
  const kpiW = (pageW - marginX * 2 - kpiGap * (kpis.length - 1)) / kpis.length
  kpis.forEach(([label, value], i) => {
    drawKPI(doc, marginX + (kpiW + kpiGap) * i, kpiY, kpiW, kpiH, label, value)
  })
```

- [ ] **Step 2: Adicionar as colunas na tabela por emissora**

Ache a tabela de emissoras do PDF:

```bash
grep -n "by_station\|byStation" frontend/src/utils/pdfReport.js
```

Na definição de cabeçalhos e de linhas dessa tabela, acrescente `Impactos` (`row.station_pmm != null ? Math.round(row.station_pmm * row.count) : '—'`) e, **somente quando `hasTarget`**, `Impactos no target` (`row.station_pmm_target != null ? row.station_pmm_target * row.count : '—'`). Mantenha o mesmo helper de formatação numérica que as outras colunas usam.

- [ ] **Step 3: Adicionar a coluna no CSV/PDF de grade**

Em `frontend/src/utils/gridReport.js`, ache a montagem do cabeçalho e das linhas por emissora:

```bash
grep -n "header\|push(\[" frontend/src/utils/gridReport.js | head -20
```

A grade usa a base `in_slot`, espelhando a tela. Acrescente `Impactos` = `pmm × Σ in_slot da emissora` e, quando houver cadastro, `Impactos no target` = `pmm_target × Σ in_slot`. O `pmm` já vem no objeto de emissora usado pela grade; o `pmm_target` precisa ser passado pelo chamador — reuse o `pmmTargetByStation` construído na Task 12 e passe-o como argumento da função de export.

- [ ] **Step 4: Build e teste manual**

```bash
cd frontend && npm run lint && npm run build && npm run dev
```

Gere os três relatórios pelo `CampaignReportsMenu` numa campanha com target cadastrado e numa sem. Confirme: com target, colunas presentes e preenchidas; sem target, saída idêntica à de antes da feature (a não ser pela coluna Impactos, que agora existe nos dois casos).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/utils/pdfReport.js frontend/src/utils/gridReport.js frontend/src/components/CampaignReportsMenu.jsx
git commit -m "feat(reports): impactos e impactos no target no PDF e no CSV de grade"
```

---

## Task 16: Documentação

**Files:**
- Create: `docs/features/client-target-pmm.md`
- Modify: `docs/README.md`, `CLAUDE.md`

- [ ] **Step 1: Escrever o doc da feature**

`docs/features/client-target-pmm.md`:

```markdown
---
status: implementado
ultima-verificacao: 2026-07-21
codigo-relacionado:
  - migrations/0054_client_station_pmm.up.sql
  - workers/internal/catalog/client_station_pmm.go
  - workers/internal/api/handlers/client_target_pmm.go
  - workers/internal/catalog/insights.go
  - workers/internal/catalog/campaigns.go
  - workers/internal/catalog/detections.go
  - frontend/src/pages/ClientTargetPmmPage.jsx
  - frontend/src/utils/targetPmmPaste.js
---

# PMM no target por cliente

`stations.pmm` é a audiência TOTAL da emissora e vale para todo mundo. O "PMM no
target" é a audiência dentro do público-alvo de um cliente específico naquela
emissora — um número diferente (em geral menor) por par (cliente, emissora).

## Regra de resolução

> O PMM no target de uma linha é `client_station_pmm[campanha.client_id, station_id]`.

Vale em toda superfície porque toda tela de veiculação parte de uma campanha. Em
multi-atribuição, cada atribuição resolve pelo cliente dela.

## Cadastrado / não cadastrado / zero

- **Sem linha na tabela** = não cadastrado. A emissora fica FORA do total de
  impactos no target e fora do contador "X de Y emissoras com target".
- **`pmm_target = 0`** = cadastrado com valor zero. CONTA no contador e soma
  zero impactos. Na UI, apagar o campo remove a linha; digitar `0` grava zero.

## Onde aparece

Todas as superfícies escondem o bloco quando não há nenhum target cadastrado no
escopo — cliente sem cadastro vê exatamente a tela anterior à feature.

| Superfície | O que mostra | Base de contagem |
|---|---|---|
| `/insights` | cards "Impactos no target" e "CPM no target" | detecções aprovadas (mesma do card "Impactos") |
| `/detections` | pill na coluna-total por emissora | Σ `in_slot` |
| `/reports/airtime` | pill por linha | — (é o PMM no target cru) |
| `/campaigns` | Impactos, Impactos no target, CPM no target | `in_slot + bonus` |
| CSV detalhado | coluna `PMM no target` | — |
| CSV consolidado | `PMM`, `Impactos`, `PMM no target`, `Impactos no target` | detecções aprovadas |
| PDF de campanha | KPIs + colunas por emissora | detecções aprovadas |
| CSV/PDF de grade | colunas de impactos | Σ `in_slot` |

**Cada tela usa a própria base de contagem.** Essa divergência entre `/insights`
(todas as aprovadas) e `/campaigns` (`in_slot + bonus`) é ANTERIOR a esta feature
— ver [detection-count-consistency.md](../architecture/detection-count-consistency.md).
A regra adotada foi espelhar a base de cada tela, para que os dois números da
MESMA tela sempre sejam comparáveis entre si.

## CPM no target

`investido_executado ÷ impactos_no_target × 1000`, **sempre dinâmico** — inclusive
em campanha com `fixed_cpm`. O CPM fixo é contratado sobre a audiência total, não
sobre o recorte de público-alvo.

## Cadastro

Tela `/clients/:id/target-pmm` (admin). Lista as emissoras-alvo das campanhas do
cliente (união dos `campaigns.target_stations`), não o catálogo inteiro.

Botão "Colar planilha" aceita duas colunas do Excel (TSV) ou CSV. Casamento da
emissora: `short_id` exato → nome normalizado único → nome + dial. Números em
formato BR (`12.345` = doze mil). O preview mostra casadas / ambíguas / não
encontradas / valor inválido, e só aplica as casadas.

Emissora que sai do `target_stations` some da tela mas a linha NÃO é apagada — se
voltar para uma campanha, o valor está lá.

## Sem versionamento histórico

Corrigir um `pmm_target` muda relatórios de meses anteriores, igual ao
`stations.pmm` de hoje. Foi decisão explícita no design.

## API

- `GET /v1/internal/clients/{id}/target-pmm` — viewer-friendly com scope-check
  (o cliente logado só enxerga o próprio cadastro).
- `PUT /v1/internal/clients/{id}/target-pmm` — admin/operator. Bulk upsert
  transacional; `pmm_target: null` apaga a linha.
```

- [ ] **Step 2: Indexar o doc**

Em `docs/README.md`, adicione a linha na seção de features, seguindo o formato das vizinhas:

```markdown
- [client-target-pmm.md](features/client-target-pmm.md) — PMM no target por cliente e "Impactos no target" nas telas de veiculação e relatórios
```

Em `CLAUDE.md`, no mapa "Quando você for mexer em X, ver Y", adicione:

```markdown
| **Impactos / PMM** em qualquer tela ou relatório (o PMM no target é por cliente, não global) | [docs/features/client-target-pmm.md](docs/features/client-target-pmm.md) — regra: resolução por `(campanha.client_id × station_id)`; ausência de linha ≠ zero; cada tela espelha a própria base de contagem |
```

- [ ] **Step 3: Commit**

```bash
git add docs/features/client-target-pmm.md docs/README.md CLAUDE.md
git commit -m "docs: PMM no target por cliente"
```

---

## Task 17: Verificação final antes do push

**Files:** nenhum (só verificação)

- [ ] **Step 1: Cross-compile linux (o que o deploy faz)**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```

Esperado: sem saída. Build nativo Windows passar não garante isto (regra 6.1 do `CLAUDE.md`).

- [ ] **Step 2: Suíte Go completa**

```bash
cd workers && go test ./... 2>&1 | tail -40
```

Esperado: os pacotes que você tocou passam. Falhas conhecidas que NÃO são suas: `internal/catalog TestBuildDailySummary_WithDowntime` falha antes de ~13:00 UTC (regra 6.6), e as falhas pré-existentes do harness de catalog (material_ids NOT NULL, partição, FK user, isolamento de stations). Qualquer falha em pacote que você tocou é sua.

- [ ] **Step 3: Build do frontend e sanidade do lockfile**

```bash
cd frontend && npm run build
git diff --stat master -- frontend/package-lock.json
```

Esperado: build conclui e o lockfile **não** aparece no diff. Se aparecer, restaure: `git checkout master -- frontend/package-lock.json` (regra 5 do `CLAUDE.md`).

- [ ] **Step 4: Teste do parser**

```bash
node --test frontend/src/utils/targetPmmPaste.test.mjs
```

Esperado: `# fail 0`.

- [ ] **Step 5: Ensaio da migration contra dados reais**

A 0054 é DDL puro (CREATE TABLE), então não há risco de colisão de dados — mas o `shadow_migration_test` do `deploy.sh` vai rodá-la sobre uma cópia de prod de qualquer jeito. Se houver um dump de prod disponível, ensaie localmente:

```bash
docker run -d --name rc-shadow-pg -p 15434:5432 -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=radiocheck postgres:16-alpine
# restaure o dump mais recente disponível, então:
docker run --rm -v "$PWD/migrations:/m" --network host migrate/migrate \
  -path=/m -database "postgres://postgres:postgres@localhost:15434/radiocheck?sslmode=disable" up
docker rm -f rc-shadow-pg
```

Esperado: `up` conclui sem deixar o schema dirty.

- [ ] **Step 6: Regressão visual — cliente SEM cadastro**

Suba o frontend (`npm run dev`) e abra `/insights`, `/detections`, `/reports/airtime` e `/campaigns` com uma campanha de cliente **sem** nenhum `client_station_pmm`. Confirme que as quatro telas estão idênticas à `master` — nenhum card, pill ou coluna nova aparece. Este é o critério de aceite mais importante da feature: nenhum cliente existente pode ver mudança.

- [ ] **Step 7: Aceite — cliente COM cadastro**

Cadastre 2 emissoras pela tela `/clients/:id/target-pmm` (uma pela colagem, uma pelo input), e confirme nas quatro telas que "Impactos no target" aparece e que o contador "X de Y emissoras" bate com o que foi cadastrado.

---

## Notas de risco

**Não há CLI novo** neste plano, então a regra 6.7 do `CLAUDE.md` (adicionar o binário ao `workers.Dockerfile`) não se aplica.

**A divergência de bases entre `/insights` e `/campaigns` fica mais visível.** Antes, `/campaigns` só mostrava CPM e a audiência ficava escondida no tooltip; depois desta feature as duas telas exibem "Impactos" explicitamente, com valores diferentes entre si (bases diferentes, decisão registrada no spec). Se isso virar reclamação de usuário, o trabalho de unificação é próprio e tem impacto em números que o cliente já vê — não o faça dentro deste plano.
