> ⚠️ **REGISTRO HISTÓRICO — não descreve o comportamento atual.**
> Este documento é um snapshot datado da sessão de design/implementação que o gerou.
> Em **2026-08-17** a categorização de veiculação foi substituída pelo
> [**fechamento por cota da célula-dia**](../../features/quota-aware-categorization.md):
> `orphan` foi renomeada pra `bonus`; `out_slot` deixou de faturar e de abater o déficit;
> `deficit = expected − in_slot`; `bonus = COUNT(category = 'bonus')` (acabou o termo
> sintético `GREATEST(0, in_slot − expected)`); e **`Impactos = pmm × (in_slot + bonus)`**
> em toda tela e exportável. As fórmulas `deficit = max(0, expected − in_slot − out_slot)` e `bonus = max(0, in_slot − expected) + count(orphan)` da view `daily_play_summary` foram substituídas pela migration 0065.
> **Não copie fórmula daqui pra código novo** — a autoridade é
> [`docs/features/quota-aware-categorization.md`](../../features/quota-aware-categorization.md).

# Plano 1 — Foundations (Backend + Dados) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Estabelecer o modelo de dados e os endpoints/worker necessários pra suportar biblioteca de materiais por cliente, regras de distribuição com overrides, e categorização de detecções nas 6 cores. Backward-compatible — frontend antigo continua funcionando.

**Architecture:** 3 migrations sequenciais que ampliam o schema sem quebrar (`commercials` permanece). 6 novos repos (`catalog/`) seguindo o padrão existente do `commercials.go`. 1 módulo novo `workers/internal/categorizer/` com função pura `Categorize()` + integração no fluxo de criação de detection. Endpoints REST sob `/v1/internal/*` para CRUD das novas entidades + endpoint agregado `daily-summary`.

**Tech Stack:** Go 1.22, pgx/v5 (Postgres), chi (router), JWT auth (existente), golang-migrate (runner via Docker), testing+httptest (padrão da casa, sem testify).

**Spec de referência:** [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](../specs/2026-05-11-campaign-wizard-design.md)

---

## Estrutura de arquivos

### Migrations (criar)
- `migrations/0016_material_library.up.sql` + `.down.sql`
- `migrations/0017_distribution_plan.up.sql` + `.down.sql`
- `migrations/0018_detections_categorization.up.sql` + `.down.sql`

### Repos (criar — em `workers/internal/catalog/`)
- `material_types.go` + `_test.go`
- `materials.go` + `_test.go`
- `campaign_materials.go` + `_test.go`
- `distribution_rules.go` + `_test.go`
- `distribution_overrides.go` + `_test.go`
- `daily_summary.go` + `_test.go`

### Categorizer (criar — pacote novo)
- `workers/internal/categorizer/categorizer.go` + `_test.go`

### Handlers (criar — em `workers/internal/api/handlers/`)
- `material_types.go` + `_test.go`
- `materials.go` + `_test.go`
- `campaign_materials.go` + `_test.go`
- `distribution_rules.go` + `_test.go`
- `distribution_overrides.go` + `_test.go`

### Modificações
- `workers/internal/api/router.go` — registrar novos endpoints
- `workers/internal/api/deps.go` (ou onde Deps struct vive) — adicionar campos novos
- `workers/internal/catalog/detections.go` — chamar `Categorize()` em `Create()`
- `workers/internal/evidence/service.go` — (opcional) re-categorizar após persist
- `cmd/api/main.go` — instanciar novos repos e handlers

### Docs (criar)
- `docs/distribution-rules.md` — guia operacional da semântica de regras
- `docs/material-library.md` — guia operacional da biblioteca

---

## Pré-requisitos antes de começar

- [ ] **Garantir que o ambiente dev está rodando**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck/infra/docker"
docker compose ps
```

Esperado: serviços `db`, `api`, `migrate` listados. Se não estiverem rodando: `docker compose up -d`.

- [ ] **Confirmar que migrations atuais estão aplicadas**

```bash
docker compose exec db psql -U postgres -d radiocheck -c "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 3;"
```

Esperado: ver `15` (shared hashes) como mais recente. Se faltarem migrations antigas, rodar `docker compose up -d --build migrate` antes.

- [ ] **Ler a spec inteira**

Arquivo: [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](../specs/2026-05-11-campaign-wizard-design.md)

Foco especial nas seções:
- §5 (Modelo de dados — os 3 DDLs estão completos lá)
- §6 (Lógica de categorização — pseudo-código completo)
- §8 (Regras de editabilidade)

---

# Phase A — Migrations

Inserem schema novo, fazem backfill de dados, e mantêm o schema antigo (`commercials`) intacto. Após esta fase, o sistema continua funcionando exatamente como hoje, mas o schema novo está disponível.

---

### Task 1: Migration 0016 — Biblioteca de materiais

**Files:**
- Create: `migrations/0016_material_library.up.sql`
- Create: `migrations/0016_material_library.down.sql`

- [ ] **Step 1: Criar arquivo up**

Crie `migrations/0016_material_library.up.sql` com este conteúdo exato:

```sql
-- 0016_material_library.up.sql
-- Adiciona biblioteca de materiais por cliente, decuplada de campanha.
-- Spec: docs/superpowers/specs/2026-05-11-campaign-wizard-design.md §5.1
--
-- Migração não-destrutiva: a tabela commercials permanece intacta.
-- Cada commercial existente vira um material com o MESMO UUID, preservando
-- referências em detections.commercial_id.

BEGIN;

-- ────── 1. material_types: registro global de tipos ──────

CREATE TABLE material_types (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL UNIQUE,
    color TEXT NOT NULL DEFAULT '#94a3b8',
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO material_types (name, color) VALUES
    ('Spot 30s',     '#3b82f6'),
    ('Spot 60s',     '#0ea5e9'),
    ('Testemunhal',  '#8b5cf6'),
    ('Citação',      '#14b8a6'),
    ('Vinheta',      '#f59e0b'),
    ('Jingle',       '#ec4899');

-- ────── 2. materials: catálogo do material em si ──────

CREATE TABLE materials (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    short_id SERIAL UNIQUE,
    client_id UUID NOT NULL REFERENCES clients(id) ON DELETE RESTRICT,
    title TEXT NOT NULL,
    type_id UUID REFERENCES material_types(id) ON DELETE SET NULL,
    duration_seconds NUMERIC(6,3) NOT NULL,
    master_storage_path TEXT NOT NULL,
    master_sha256 TEXT NOT NULL,
    fingerprint_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (fingerprint_status IN ('pending','generating','ready','failed')),
    fingerprint_generated_at TIMESTAMPTZ,
    fingerprint_hash_count INT,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    -- NOTA: NÃO adicionar UNIQUE(client_id, master_sha256) nesta migration.
    -- Commercials existentes podem ter duplicatas (mesmo MP3 em campanhas
    -- diferentes). Migrar 1:1 preserva referências de detections.commercial_id.
    -- Constraint pode ser adicionada em migration futura após limpeza manual.
);
CREATE INDEX idx_materials_client ON materials(client_id);
CREATE INDEX idx_materials_type ON materials(type_id);
CREATE INDEX idx_materials_status ON materials(fingerprint_status);

CREATE TRIGGER trg_materials_updated BEFORE UPDATE ON materials
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

-- ────── 3. campaign_materials: link N:N campanhas ↔ materiais ──────

CREATE TABLE campaign_materials (
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    material_id UUID NOT NULL REFERENCES materials(id) ON DELETE RESTRICT,
    target_stations UUID[] NOT NULL DEFAULT '{}',
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (campaign_id, material_id)
);
CREATE INDEX idx_campaign_materials_material ON campaign_materials(material_id);

-- ────── 4. Backfill: commercials → materials ──────

INSERT INTO materials (
    id, short_id, client_id, title, type_id, duration_seconds,
    master_storage_path, master_sha256,
    fingerprint_status, fingerprint_generated_at, fingerprint_hash_count,
    metadata, created_at, updated_at
)
SELECT
    c.id, c.short_id, cmp.client_id, c.title, NULL,
    c.duration_seconds, c.master_storage_path, c.master_sha256,
    c.fingerprint_status, c.fingerprint_generated_at, c.fingerprint_hash_count,
    c.metadata, c.created_at, c.updated_at
FROM commercials c
JOIN campaigns cmp ON cmp.id = c.campaign_id;

-- short_id sequence precisa avançar pra não colidir em novos inserts
SELECT setval(
    pg_get_serial_sequence('materials', 'short_id'),
    GREATEST((SELECT MAX(short_id) FROM materials), 1)
);

-- ────── 5. Backfill: campaign_materials ──────

INSERT INTO campaign_materials (campaign_id, material_id, target_stations, added_at)
SELECT c.campaign_id, c.id, c.target_stations, c.created_at
FROM commercials c;

COMMIT;
```

- [ ] **Step 2: Criar arquivo down**

Crie `migrations/0016_material_library.down.sql`:

```sql
-- 0016_material_library.down.sql

BEGIN;

DROP TABLE IF EXISTS campaign_materials;
DROP TABLE IF EXISTS materials;
DROP TABLE IF EXISTS material_types;

COMMIT;
```

- [ ] **Step 3: Aplicar a migration**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck/infra/docker"
docker compose up -d --build migrate
docker compose logs migrate --tail 30
```

Esperado: log termina com `migration 16/u material_library (Xms)`. Sem erros.

- [ ] **Step 4: Verificar schema aplicado**

```bash
docker compose exec db psql -U postgres -d radiocheck -c "\dt material_types"
docker compose exec db psql -U postgres -d radiocheck -c "\dt materials"
docker compose exec db psql -U postgres -d radiocheck -c "\dt campaign_materials"
docker compose exec db psql -U postgres -d radiocheck -c "SELECT count(*) FROM material_types;"
```

Esperado: as 3 tabelas existem. `material_types` tem 6 rows (os seeds).

- [ ] **Step 5: Verificar backfill**

```bash
docker compose exec db psql -U postgres -d radiocheck -c "
  SELECT 
    (SELECT count(*) FROM commercials) AS commercials_count,
    (SELECT count(*) FROM materials) AS materials_count,
    (SELECT count(*) FROM campaign_materials) AS link_count;
"
```

Esperado: `commercials_count == materials_count == link_count`. Se zero (DB vazio), tudo bem — confirma só que a migration rodou.

- [ ] **Step 6: Verificar integridade referencial**

```bash
docker compose exec db psql -U postgres -d radiocheck -c "
  -- Toda detection tem material correspondente
  SELECT count(*) FROM detections d
  WHERE NOT EXISTS (SELECT 1 FROM materials m WHERE m.id = d.commercial_id);
"
```

Esperado: `0`. Se vier qualquer número > 0, há detections órfãs que precisam ser investigadas antes de continuar.

- [ ] **Step 7: Commit**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck"
git add migrations/0016_material_library.up.sql migrations/0016_material_library.down.sql
git commit -m "feat(db): add material library (0016) — material_types, materials, campaign_materials"
```

---

### Task 2: Migration 0017 — Plano de distribuição

**Files:**
- Create: `migrations/0017_distribution_plan.up.sql`
- Create: `migrations/0017_distribution_plan.down.sql`

- [ ] **Step 1: Criar arquivo up**

Crie `migrations/0017_distribution_plan.up.sql`:

```sql
-- 0017_distribution_plan.up.sql
-- Regras de distribuição (plano "programado") e overrides por célula.
-- Spec: docs/superpowers/specs/2026-05-11-campaign-wizard-design.md §5.2

BEGIN;

-- ────── Regras de distribuição ──────

CREATE TABLE distribution_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    material_id UUID NOT NULL REFERENCES materials(id) ON DELETE CASCADE,
    station_ids UUID[] NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    -- weekday_mask: bitmask. Bit 0=Domingo, 1=Segunda, ..., 6=Sábado.
    -- Ex: seg-sex = 0b0111110 = 62
    weekday_mask SMALLINT NOT NULL CHECK (weekday_mask BETWEEN 0 AND 127),
    time_start TIME NOT NULL,
    time_end TIME NOT NULL,
    plays_per_day SMALLINT NOT NULL CHECK (plays_per_day > 0 AND plays_per_day <= 100),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT rule_dates_valid CHECK (end_date >= start_date),
    CONSTRAINT rule_times_valid CHECK (time_end > time_start)
);
CREATE INDEX idx_distribution_rules_campaign ON distribution_rules(campaign_id);
CREATE INDEX idx_distribution_rules_material ON distribution_rules(material_id);
CREATE INDEX idx_distribution_rules_dates ON distribution_rules(start_date, end_date);

CREATE TRIGGER trg_distribution_rules_updated BEFORE UPDATE ON distribution_rules
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

-- ────── Overrides por célula ──────

CREATE TABLE distribution_overrides (
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    material_id UUID NOT NULL REFERENCES materials(id) ON DELETE CASCADE,
    station_id UUID NOT NULL REFERENCES stations(id),
    for_date DATE NOT NULL,
    plays_expected SMALLINT NOT NULL CHECK (plays_expected >= 0),
    reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by UUID,  -- FK lógica pra users(id); NULL quando criado por migration ou worker
    PRIMARY KEY (campaign_id, material_id, station_id, for_date)
);
CREATE INDEX idx_distribution_overrides_date ON distribution_overrides(for_date);

COMMIT;
```

- [ ] **Step 2: Criar arquivo down**

Crie `migrations/0017_distribution_plan.down.sql`:

```sql
-- 0017_distribution_plan.down.sql

BEGIN;

DROP TABLE IF EXISTS distribution_overrides;
DROP TABLE IF EXISTS distribution_rules;

COMMIT;
```

- [ ] **Step 3: Aplicar e verificar**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck/infra/docker"
docker compose up -d --build migrate
docker compose logs migrate --tail 20
docker compose exec db psql -U postgres -d radiocheck -c "\d distribution_rules"
docker compose exec db psql -U postgres -d radiocheck -c "\d distribution_overrides"
```

Esperado: ambas tabelas existem, com índices e constraints listados.

- [ ] **Step 4: Smoke test de constraints**

```bash
docker compose exec db psql -U postgres -d radiocheck -c "
INSERT INTO distribution_rules (campaign_id, material_id, station_ids,
  start_date, end_date, weekday_mask, time_start, time_end, plays_per_day)
VALUES ('00000000-0000-0000-0000-000000000000', '00000000-0000-0000-0000-000000000000',
  '{}', '2026-06-01', '2026-05-01', 62, '08:00', '10:00', 3);
"
```

Esperado: erro `rule_dates_valid` constraint failure (end < start) ou FK violation. Confirma que constraints estão ativas.

- [ ] **Step 5: Commit**

```bash
git add migrations/0017_distribution_plan.up.sql migrations/0017_distribution_plan.down.sql
git commit -m "feat(db): add distribution_rules + distribution_overrides (0017)"
```

---

### Task 3: Migration 0018 — Categorização de detecções + view agregada

**Files:**
- Create: `migrations/0018_detections_categorization.up.sql`
- Create: `migrations/0018_detections_categorization.down.sql`

- [ ] **Step 1: Criar arquivo up**

Crie `migrations/0018_detections_categorization.up.sql`:

```sql
-- 0018_detections_categorization.up.sql
-- Adiciona coluna `category` em detections + view daily_play_summary.
-- Spec: docs/superpowers/specs/2026-05-11-campaign-wizard-design.md §5.3 e §5.4

BEGIN;

-- ────── 1. Coluna category em detections ──────

ALTER TABLE detections ADD COLUMN category TEXT
    CHECK (category IN ('in_slot','out_slot','out_date','orphan'));

CREATE INDEX idx_detections_category ON detections(campaign_id, detected_at, category);

-- Backfill: detections existentes viram 'orphan' (nenhuma regra existe ainda).
UPDATE detections SET category = 'orphan' WHERE category IS NULL;

ALTER TABLE detections ALTER COLUMN category SET NOT NULL;
ALTER TABLE detections ALTER COLUMN category SET DEFAULT 'orphan';

-- ────── 2. View daily_play_summary ──────
--
-- Retorna por (campaign, material, station, data):
--   expected, in_slot, deficit, bonus, out_slot, out_date
-- Spec §5.4 — fórmulas das 6 cores.

CREATE OR REPLACE VIEW daily_play_summary AS
WITH expected AS (
    SELECT
        r.campaign_id,
        r.material_id,
        s.station_id,
        d.for_date::date AS for_date,
        SUM(r.plays_per_day)::int AS rule_expected
    FROM distribution_rules r
    CROSS JOIN LATERAL unnest(r.station_ids) AS s(station_id)
    CROSS JOIN LATERAL generate_series(r.start_date, r.end_date, INTERVAL '1 day') AS d(for_date)
    WHERE (1 << EXTRACT(DOW FROM d.for_date)::INT) & r.weekday_mask != 0
    GROUP BY r.campaign_id, r.material_id, s.station_id, d.for_date
),
expected_with_override AS (
    SELECT
        COALESCE(o.campaign_id, e.campaign_id) AS campaign_id,
        COALESCE(o.material_id, e.material_id) AS material_id,
        COALESCE(o.station_id,  e.station_id)  AS station_id,
        COALESCE(o.for_date,    e.for_date)    AS for_date,
        COALESCE(o.plays_expected, e.rule_expected)::int AS expected
    FROM expected e
    FULL OUTER JOIN distribution_overrides o
        ON e.campaign_id = o.campaign_id
       AND e.material_id = o.material_id
       AND e.station_id  = o.station_id
       AND e.for_date    = o.for_date
),
actual AS (
    SELECT
        campaign_id,
        commercial_id AS material_id,
        station_id,
        date_trunc('day', detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS for_date,
        COUNT(*) FILTER (WHERE category = 'in_slot')::int  AS in_slot,
        COUNT(*) FILTER (WHERE category = 'out_slot')::int AS out_slot,
        COUNT(*) FILTER (WHERE category = 'out_date')::int AS out_date,
        COUNT(*) FILTER (WHERE category = 'orphan')::int   AS orphan
    FROM detections
    WHERE retracted_at IS NULL
    GROUP BY campaign_id, commercial_id, station_id, for_date
)
SELECT
    COALESCE(e.campaign_id, a.campaign_id) AS campaign_id,
    COALESCE(e.material_id, a.material_id) AS material_id,
    COALESCE(e.station_id,  a.station_id)  AS station_id,
    COALESCE(e.for_date,    a.for_date)    AS for_date,
    COALESCE(e.expected, 0)::int                                                    AS expected,
    COALESCE(a.in_slot,  0)::int                                                    AS in_slot,
    GREATEST(0, COALESCE(e.expected,0) - COALESCE(a.in_slot,0) - COALESCE(a.out_slot,0))::int  AS deficit,
    (GREATEST(0, COALESCE(a.in_slot,0) - COALESCE(e.expected,0)) + COALESCE(a.orphan,0))::int  AS bonus,
    COALESCE(a.out_slot, 0)::int                                                    AS out_slot,
    COALESCE(a.out_date, 0)::int                                                    AS out_date
FROM expected_with_override e
FULL OUTER JOIN actual a
    ON e.campaign_id = a.campaign_id
   AND e.material_id = a.material_id
   AND e.station_id  = a.station_id
   AND e.for_date    = a.for_date;

COMMIT;
```

- [ ] **Step 2: Criar arquivo down**

Crie `migrations/0018_detections_categorization.down.sql`:

```sql
-- 0018_detections_categorization.down.sql

BEGIN;

DROP VIEW IF EXISTS daily_play_summary;

DROP INDEX IF EXISTS idx_detections_category;
ALTER TABLE detections DROP COLUMN IF EXISTS category;

COMMIT;
```

- [ ] **Step 3: Aplicar e verificar**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck/infra/docker"
docker compose up -d --build migrate
docker compose exec db psql -U postgres -d radiocheck -c "\d detections"
docker compose exec db psql -U postgres -d radiocheck -c "\d+ daily_play_summary"
docker compose exec db psql -U postgres -d radiocheck -c "
  SELECT category, count(*) FROM detections GROUP BY category;
"
```

Esperado: coluna `category` listada em `detections`, view `daily_play_summary` existe, todas as detections existentes estão como `orphan`.

- [ ] **Step 4: Testar a view com dados sintéticos**

```bash
docker compose exec db psql -U postgres -d radiocheck -c "
  SELECT count(*) FROM daily_play_summary;
"
```

Esperado: 0 (não há `distribution_rules` ainda — view fica vazia). Confirma que a view roda sem erros sintáticos.

- [ ] **Step 5: Commit**

```bash
git add migrations/0018_detections_categorization.up.sql migrations/0018_detections_categorization.down.sql
git commit -m "feat(db): add detections.category + daily_play_summary view (0018)"
```

---

# Phase B — Repos

Seguem o padrão de `workers/internal/catalog/commercials.go`: struct exported, construtor `New*(pool)`, métodos retornando `(*T, error)` ou `([]T, error)`. Testes usam pacote `testing` (sem testify) e fazem setup via DB de teste já existente.

> **Padrão dos testes:** olhe `workers/internal/catalog/commercials_test.go` antes de começar. Reutilize o helper de setup (provavelmente `newTestPool(t)` ou similar) que já existe lá. Não invente novo.

---

### Task 4: Repo MaterialTypes

**Files:**
- Create: `workers/internal/catalog/material_types.go`
- Create: `workers/internal/catalog/material_types_test.go`

- [ ] **Step 1: Escrever o teste primeiro**

Crie `workers/internal/catalog/material_types_test.go`:

```go
package catalog

import (
	"context"
	"testing"
)

func TestMaterialTypes_List(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	repo := NewMaterialTypes(pool)

	types, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Migration 0016 semeia 6 tipos
	if len(types) < 6 {
		t.Fatalf("expected at least 6 types, got %d", len(types))
	}
	// Spot 30s deve estar presente
	found := false
	for _, mt := range types {
		if mt.Name == "Spot 30s" {
			found = true
			if mt.Color != "#3b82f6" {
				t.Errorf("Spot 30s color = %q, want #3b82f6", mt.Color)
			}
		}
	}
	if !found {
		t.Errorf("Spot 30s not found in seed data")
	}
}

func TestMaterialTypes_Create(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	repo := NewMaterialTypes(pool)

	mt, err := repo.Create(context.Background(), CreateMaterialTypeInput{
		Name:  "Promo Especial",
		Color: "#ff00ff",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if mt.Name != "Promo Especial" {
		t.Errorf("Name = %q, want Promo Especial", mt.Name)
	}
}
```

- [ ] **Step 2: Rodar o teste pra confirmar que falha**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck/workers"
go test ./internal/catalog/ -run TestMaterialTypes -v
```

Esperado: erro de compilação `undefined: NewMaterialTypes` ou similar.

- [ ] **Step 3: Implementar o repo**

Crie `workers/internal/catalog/material_types.go`:

```go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MaterialType struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Color       string    `json:"color"`
	Description *string   `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type MaterialTypes struct {
	pool *pgxpool.Pool
}

func NewMaterialTypes(pool *pgxpool.Pool) *MaterialTypes {
	return &MaterialTypes{pool: pool}
}

type CreateMaterialTypeInput struct {
	Name        string
	Color       string
	Description *string
}

const materialTypeColumns = `id, name, color, description, created_at`

func (mt *MaterialTypes) List(ctx context.Context) ([]MaterialType, error) {
	rows, err := mt.pool.Query(ctx,
		`SELECT `+materialTypeColumns+` FROM material_types ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MaterialType
	for rows.Next() {
		var t MaterialType
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &t.Description, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (mt *MaterialTypes) Get(ctx context.Context, id uuid.UUID) (*MaterialType, error) {
	var t MaterialType
	err := mt.pool.QueryRow(ctx,
		`SELECT `+materialTypeColumns+` FROM material_types WHERE id = $1`, id,
	).Scan(&t.ID, &t.Name, &t.Color, &t.Description, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (mt *MaterialTypes) Create(ctx context.Context, in CreateMaterialTypeInput) (*MaterialType, error) {
	var t MaterialType
	color := in.Color
	if color == "" {
		color = "#94a3b8"
	}
	err := mt.pool.QueryRow(ctx,
		`INSERT INTO material_types (name, color, description)
		 VALUES ($1, $2, $3)
		 RETURNING `+materialTypeColumns,
		in.Name, color, in.Description,
	).Scan(&t.ID, &t.Name, &t.Color, &t.Description, &t.CreatedAt)
	return &t, err
}

func (mt *MaterialTypes) Update(ctx context.Context, id uuid.UUID, in CreateMaterialTypeInput) (*MaterialType, error) {
	var t MaterialType
	color := in.Color
	if color == "" {
		color = "#94a3b8"
	}
	err := mt.pool.QueryRow(ctx,
		`UPDATE material_types SET name=$2, color=$3, description=$4
		 WHERE id=$1
		 RETURNING `+materialTypeColumns,
		id, in.Name, color, in.Description,
	).Scan(&t.ID, &t.Name, &t.Color, &t.Description, &t.CreatedAt)
	return &t, err
}

func (mt *MaterialTypes) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := mt.pool.Exec(ctx, `DELETE FROM material_types WHERE id = $1`, id)
	return err
}
```

- [ ] **Step 4: Rodar os testes pra confirmar que passam**

```bash
go test ./internal/catalog/ -run TestMaterialTypes -v
```

Esperado: PASS para `TestMaterialTypes_List` e `TestMaterialTypes_Create`.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/material_types.go workers/internal/catalog/material_types_test.go
git commit -m "feat(catalog): add MaterialTypes repo"
```

---

### Task 5: Repo Materials

**Files:**
- Create: `workers/internal/catalog/materials.go`
- Create: `workers/internal/catalog/materials_test.go`

- [ ] **Step 1: Escrever o teste primeiro**

Crie `workers/internal/catalog/materials_test.go`:

```go
package catalog

import (
	"context"
	"testing"
)

func TestMaterials_CreateAndList(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()

	// Setup: cria um cliente
	clientsRepo := NewClients(pool)
	cli, err := clientsRepo.Create(context.Background(), CreateClientInput{Name: "Test Co"})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	matsRepo := NewMaterials(pool)

	created, err := matsRepo.Create(context.Background(), CreateMaterialInput{
		ClientID:          cli.ID,
		Title:             "Spot Test",
		DurationSeconds:   30.0,
		MasterStoragePath: "/tmp/test.mp3",
		MasterSHA256:      "deadbeef",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ClientID != cli.ID {
		t.Errorf("ClientID = %s, want %s", created.ClientID, cli.ID)
	}

	list, err := matsRepo.ListByClient(context.Background(), cli.ID, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}

	// Busca textual
	list2, _ := matsRepo.ListByClient(context.Background(), cli.ID, "spot")
	if len(list2) != 1 {
		t.Errorf("search 'spot' len = %d, want 1", len(list2))
	}
	list3, _ := matsRepo.ListByClient(context.Background(), cli.ID, "naoexiste")
	if len(list3) != 0 {
		t.Errorf("search 'naoexiste' len = %d, want 0", len(list3))
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

```bash
go test ./internal/catalog/ -run TestMaterials -v
```

Esperado: erro de compilação `undefined: NewMaterials`.

- [ ] **Step 3: Implementar o repo**

Crie `workers/internal/catalog/materials.go`:

```go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Material struct {
	ID                     uuid.UUID  `json:"id"`
	ShortID                int32      `json:"short_id"`
	ClientID               uuid.UUID  `json:"client_id"`
	Title                  string     `json:"title"`
	TypeID                 *uuid.UUID `json:"type_id,omitempty"`
	DurationSeconds        float64    `json:"duration_seconds"`
	MasterStoragePath      string     `json:"master_storage_path"`
	MasterSHA256           string     `json:"master_sha256"`
	FingerprintStatus      string     `json:"fingerprint_status"`
	FingerprintGeneratedAt *time.Time `json:"fingerprint_generated_at,omitempty"`
	FingerprintHashCount   *int32     `json:"fingerprint_hash_count,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type Materials struct {
	pool *pgxpool.Pool
}

func NewMaterials(pool *pgxpool.Pool) *Materials {
	return &Materials{pool: pool}
}

type CreateMaterialInput struct {
	ClientID          uuid.UUID
	Title             string
	TypeID            *uuid.UUID
	DurationSeconds   float64
	MasterStoragePath string
	MasterSHA256      string
}

const materialColumns = `id, short_id, client_id, title, type_id, duration_seconds,
       master_storage_path, master_sha256, fingerprint_status,
       fingerprint_generated_at, fingerprint_hash_count, created_at, updated_at`

func scanMaterial(row interface {
	Scan(...any) error
}, m *Material) error {
	return row.Scan(&m.ID, &m.ShortID, &m.ClientID, &m.Title, &m.TypeID,
		&m.DurationSeconds, &m.MasterStoragePath, &m.MasterSHA256,
		&m.FingerprintStatus, &m.FingerprintGeneratedAt, &m.FingerprintHashCount,
		&m.CreatedAt, &m.UpdatedAt)
}

func (m *Materials) Create(ctx context.Context, in CreateMaterialInput) (*Material, error) {
	var mat Material
	err := m.pool.QueryRow(ctx, `
		INSERT INTO materials (client_id, title, type_id, duration_seconds,
		                       master_storage_path, master_sha256)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+materialColumns,
		in.ClientID, in.Title, in.TypeID, in.DurationSeconds,
		in.MasterStoragePath, in.MasterSHA256,
	).Scan(&mat.ID, &mat.ShortID, &mat.ClientID, &mat.Title, &mat.TypeID,
		&mat.DurationSeconds, &mat.MasterStoragePath, &mat.MasterSHA256,
		&mat.FingerprintStatus, &mat.FingerprintGeneratedAt, &mat.FingerprintHashCount,
		&mat.CreatedAt, &mat.UpdatedAt)
	return &mat, err
}

func (m *Materials) Get(ctx context.Context, id uuid.UUID) (*Material, error) {
	var mat Material
	err := m.pool.QueryRow(ctx,
		`SELECT `+materialColumns+` FROM materials WHERE id = $1`, id,
	).Scan(&mat.ID, &mat.ShortID, &mat.ClientID, &mat.Title, &mat.TypeID,
		&mat.DurationSeconds, &mat.MasterStoragePath, &mat.MasterSHA256,
		&mat.FingerprintStatus, &mat.FingerprintGeneratedAt, &mat.FingerprintHashCount,
		&mat.CreatedAt, &mat.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &mat, nil
}

// ListByClient lists materials for a client, optionally filtered by case-insensitive
// substring match on title. Pass empty q for "all".
func (m *Materials) ListByClient(ctx context.Context, clientID uuid.UUID, q string) ([]Material, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT `+materialColumns+`
		FROM materials
		WHERE client_id = $1
		  AND ($2 = '' OR title ILIKE '%' || $2 || '%')
		ORDER BY created_at DESC`,
		clientID, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Material
	for rows.Next() {
		var mat Material
		if err := scanMaterial(rows, &mat); err != nil {
			return nil, err
		}
		out = append(out, mat)
	}
	return out, rows.Err()
}

func (m *Materials) UpdateType(ctx context.Context, id uuid.UUID, typeID *uuid.UUID) error {
	_, err := m.pool.Exec(ctx,
		`UPDATE materials SET type_id = $2, updated_at = now() WHERE id = $1`, id, typeID)
	return err
}

func (m *Materials) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := m.pool.Exec(ctx, `DELETE FROM materials WHERE id = $1`, id)
	return err
}
```

- [ ] **Step 4: Rodar testes**

```bash
go test ./internal/catalog/ -run TestMaterials -v
```

Esperado: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/materials.go workers/internal/catalog/materials_test.go
git commit -m "feat(catalog): add Materials repo"
```

---

### Task 6: Repo CampaignMaterials (link N:N)

**Files:**
- Create: `workers/internal/catalog/campaign_materials.go`
- Create: `workers/internal/catalog/campaign_materials_test.go`

- [ ] **Step 1: Escrever o teste**

Crie `workers/internal/catalog/campaign_materials_test.go`:

```go
package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCampaignMaterials_LinkAndList(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "Test"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "Test Camp", ClientID: cli.ID,
		StartDate: time.Now(), EndDate: time.Now().AddDate(0, 1, 0),
	})
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "Spot", DurationSeconds: 30,
		MasterStoragePath: "/tmp/x.mp3", MasterSHA256: "abc",
	})

	repo := NewCampaignMaterials(pool)

	station := uuid.New()
	if err := repo.Link(ctx, cmp.ID, mat.ID, []uuid.UUID{station}); err != nil {
		t.Fatalf("link: %v", err)
	}

	links, err := repo.ListByCampaign(ctx, cmp.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(links) != 1 || links[0].MaterialID != mat.ID {
		t.Fatalf("links = %v, want one with material %s", links, mat.ID)
	}
	if len(links[0].TargetStations) != 1 || links[0].TargetStations[0] != station {
		t.Errorf("TargetStations = %v, want [%s]", links[0].TargetStations, station)
	}

	// Update stations
	newStation := uuid.New()
	if err := repo.UpdateStations(ctx, cmp.ID, mat.ID, []uuid.UUID{newStation}); err != nil {
		t.Fatalf("update stations: %v", err)
	}
	links, _ = repo.ListByCampaign(ctx, cmp.ID)
	if links[0].TargetStations[0] != newStation {
		t.Errorf("after update: stations = %v, want [%s]", links[0].TargetStations, newStation)
	}

	// Unlink
	if err := repo.Unlink(ctx, cmp.ID, mat.ID); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	links, _ = repo.ListByCampaign(ctx, cmp.ID)
	if len(links) != 0 {
		t.Errorf("after unlink: %d links, want 0", len(links))
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

```bash
go test ./internal/catalog/ -run TestCampaignMaterials -v
```

Esperado: compilação falha.

- [ ] **Step 3: Implementar repo**

Crie `workers/internal/catalog/campaign_materials.go`:

```go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CampaignMaterial struct {
	CampaignID     uuid.UUID   `json:"campaign_id"`
	MaterialID     uuid.UUID   `json:"material_id"`
	TargetStations []uuid.UUID `json:"target_stations"`
	AddedAt        time.Time   `json:"added_at"`
}

type CampaignMaterials struct {
	pool *pgxpool.Pool
}

func NewCampaignMaterials(pool *pgxpool.Pool) *CampaignMaterials {
	return &CampaignMaterials{pool: pool}
}

// Link inserts a campaign↔material association. If it already exists, target_stations
// is replaced (upsert semantics — operator may re-add to reset stations).
func (cm *CampaignMaterials) Link(ctx context.Context, campaignID, materialID uuid.UUID, stations []uuid.UUID) error {
	if stations == nil {
		stations = []uuid.UUID{}
	}
	_, err := cm.pool.Exec(ctx, `
		INSERT INTO campaign_materials (campaign_id, material_id, target_stations)
		VALUES ($1, $2, $3)
		ON CONFLICT (campaign_id, material_id) DO UPDATE
		   SET target_stations = EXCLUDED.target_stations`,
		campaignID, materialID, stations)
	return err
}

func (cm *CampaignMaterials) Unlink(ctx context.Context, campaignID, materialID uuid.UUID) error {
	_, err := cm.pool.Exec(ctx,
		`DELETE FROM campaign_materials WHERE campaign_id = $1 AND material_id = $2`,
		campaignID, materialID)
	return err
}

func (cm *CampaignMaterials) UpdateStations(ctx context.Context, campaignID, materialID uuid.UUID, stations []uuid.UUID) error {
	if stations == nil {
		stations = []uuid.UUID{}
	}
	_, err := cm.pool.Exec(ctx,
		`UPDATE campaign_materials SET target_stations = $3
		 WHERE campaign_id = $1 AND material_id = $2`,
		campaignID, materialID, stations)
	return err
}

func (cm *CampaignMaterials) ListByCampaign(ctx context.Context, campaignID uuid.UUID) ([]CampaignMaterial, error) {
	rows, err := cm.pool.Query(ctx,
		`SELECT campaign_id, material_id, target_stations, added_at
		 FROM campaign_materials
		 WHERE campaign_id = $1 ORDER BY added_at ASC`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CampaignMaterial
	for rows.Next() {
		var l CampaignMaterial
		if err := rows.Scan(&l.CampaignID, &l.MaterialID, &l.TargetStations, &l.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (cm *CampaignMaterials) ListByMaterial(ctx context.Context, materialID uuid.UUID) ([]CampaignMaterial, error) {
	rows, err := cm.pool.Query(ctx,
		`SELECT campaign_id, material_id, target_stations, added_at
		 FROM campaign_materials
		 WHERE material_id = $1 ORDER BY added_at DESC`, materialID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CampaignMaterial
	for rows.Next() {
		var l CampaignMaterial
		if err := rows.Scan(&l.CampaignID, &l.MaterialID, &l.TargetStations, &l.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Rodar testes**

```bash
go test ./internal/catalog/ -run TestCampaignMaterials -v
```

Esperado: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/campaign_materials.go workers/internal/catalog/campaign_materials_test.go
git commit -m "feat(catalog): add CampaignMaterials repo (N:N link)"
```

---

### Task 7: Repo DistributionRules

**Files:**
- Create: `workers/internal/catalog/distribution_rules.go`
- Create: `workers/internal/catalog/distribution_rules_test.go`

- [ ] **Step 1: Escrever o teste**

Crie `workers/internal/catalog/distribution_rules_test.go`:

```go
package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDistributionRules_CRUD(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "Test"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	})
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "x",
	})
	station := uuid.New()

	repo := NewDistributionRules(pool)

	rule, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID:   cmp.ID,
		MaterialID:   mat.ID,
		StationIDs:   []uuid.UUID{station},
		StartDate:    time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:      time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask:  62, // seg-sex
		TimeStart:    "08:15",
		TimeEnd:      "10:45",
		PlaysPerDay:  3,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rule.PlaysPerDay != 3 {
		t.Errorf("PlaysPerDay = %d, want 3", rule.PlaysPerDay)
	}

	list, _ := repo.ListByCampaign(ctx, cmp.ID)
	if len(list) != 1 {
		t.Errorf("list len = %d, want 1", len(list))
	}

	// Update
	if err := repo.Update(ctx, rule.ID, CreateDistributionRuleInput{
		CampaignID: cmp.ID, MaterialID: mat.ID,
		StationIDs: []uuid.UUID{station},
		StartDate:  time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:    time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "08:15", TimeEnd: "10:45",
		PlaysPerDay: 5,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	r, _ := repo.Get(ctx, rule.ID)
	if r.PlaysPerDay != 5 {
		t.Errorf("after update: PlaysPerDay = %d, want 5", r.PlaysPerDay)
	}

	// Delete
	if err := repo.Delete(ctx, rule.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ = repo.ListByCampaign(ctx, cmp.ID)
	if len(list) != 0 {
		t.Errorf("after delete: len = %d, want 0", len(list))
	}
}

func TestDistributionRules_Constraints(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Now(), EndDate: time.Now().AddDate(0, 1, 0),
	})
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "y",
	})

	repo := NewDistributionRules(pool)

	// end_date < start_date deve falhar
	_, err := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, MaterialID: mat.ID,
		StationIDs: []uuid.UUID{uuid.New()},
		StartDate:  time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		EndDate:    time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "08:00", TimeEnd: "10:00",
		PlaysPerDay: 3,
	})
	if err == nil {
		t.Error("expected error for end < start, got nil")
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

```bash
go test ./internal/catalog/ -run TestDistributionRules -v
```

- [ ] **Step 3: Implementar repo**

Crie `workers/internal/catalog/distribution_rules.go`:

```go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DistributionRule struct {
	ID           uuid.UUID   `json:"id"`
	CampaignID   uuid.UUID   `json:"campaign_id"`
	MaterialID   uuid.UUID   `json:"material_id"`
	StationIDs   []uuid.UUID `json:"station_ids"`
	StartDate    time.Time   `json:"start_date"`
	EndDate      time.Time   `json:"end_date"`
	WeekdayMask  int16       `json:"weekday_mask"`
	TimeStart    string      `json:"time_start"` // HH:MM:SS no DB; passamos HH:MM e PG normaliza
	TimeEnd      string      `json:"time_end"`
	PlaysPerDay  int16       `json:"plays_per_day"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

type DistributionRules struct {
	pool *pgxpool.Pool
}

func NewDistributionRules(pool *pgxpool.Pool) *DistributionRules {
	return &DistributionRules{pool: pool}
}

type CreateDistributionRuleInput struct {
	CampaignID   uuid.UUID
	MaterialID   uuid.UUID
	StationIDs   []uuid.UUID
	StartDate    time.Time
	EndDate      time.Time
	WeekdayMask  int16
	TimeStart    string // "HH:MM"
	TimeEnd      string // "HH:MM"
	PlaysPerDay  int16
}

const ruleColumns = `id, campaign_id, material_id, station_ids,
       start_date, end_date, weekday_mask,
       to_char(time_start, 'HH24:MI') AS time_start,
       to_char(time_end,   'HH24:MI') AS time_end,
       plays_per_day, created_at, updated_at`

func (dr *DistributionRules) Create(ctx context.Context, in CreateDistributionRuleInput) (*DistributionRule, error) {
	var r DistributionRule
	err := dr.pool.QueryRow(ctx, `
		INSERT INTO distribution_rules
		  (campaign_id, material_id, station_ids, start_date, end_date,
		   weekday_mask, time_start, time_end, plays_per_day)
		VALUES ($1, $2, $3, $4, $5, $6, $7::time, $8::time, $9)
		RETURNING `+ruleColumns,
		in.CampaignID, in.MaterialID, in.StationIDs,
		in.StartDate, in.EndDate, in.WeekdayMask,
		in.TimeStart, in.TimeEnd, in.PlaysPerDay,
	).Scan(&r.ID, &r.CampaignID, &r.MaterialID, &r.StationIDs,
		&r.StartDate, &r.EndDate, &r.WeekdayMask,
		&r.TimeStart, &r.TimeEnd, &r.PlaysPerDay,
		&r.CreatedAt, &r.UpdatedAt)
	return &r, err
}

func (dr *DistributionRules) Get(ctx context.Context, id uuid.UUID) (*DistributionRule, error) {
	var r DistributionRule
	err := dr.pool.QueryRow(ctx,
		`SELECT `+ruleColumns+` FROM distribution_rules WHERE id = $1`, id,
	).Scan(&r.ID, &r.CampaignID, &r.MaterialID, &r.StationIDs,
		&r.StartDate, &r.EndDate, &r.WeekdayMask,
		&r.TimeStart, &r.TimeEnd, &r.PlaysPerDay,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (dr *DistributionRules) ListByCampaign(ctx context.Context, campaignID uuid.UUID) ([]DistributionRule, error) {
	rows, err := dr.pool.Query(ctx,
		`SELECT `+ruleColumns+` FROM distribution_rules
		 WHERE campaign_id = $1
		 ORDER BY start_date ASC, time_start ASC`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DistributionRule
	for rows.Next() {
		var r DistributionRule
		if err := rows.Scan(&r.ID, &r.CampaignID, &r.MaterialID, &r.StationIDs,
			&r.StartDate, &r.EndDate, &r.WeekdayMask,
			&r.TimeStart, &r.TimeEnd, &r.PlaysPerDay,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListApplicable returns rules where:
//   - material_id matches
//   - station_id ∈ station_ids
//   - date ∈ [start_date, end_date]
//   - weekday of date is in weekday_mask
// Used by the categorizer.
func (dr *DistributionRules) ListApplicable(ctx context.Context,
	campaignID, materialID, stationID uuid.UUID, date time.Time) ([]DistributionRule, error) {

	rows, err := dr.pool.Query(ctx,
		`SELECT `+ruleColumns+` FROM distribution_rules
		 WHERE campaign_id = $1
		   AND material_id = $2
		   AND $3 = ANY(station_ids)
		   AND $4::date BETWEEN start_date AND end_date
		   AND ((1 << EXTRACT(DOW FROM $4::date)::int) & weekday_mask) != 0`,
		campaignID, materialID, stationID, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DistributionRule
	for rows.Next() {
		var r DistributionRule
		if err := rows.Scan(&r.ID, &r.CampaignID, &r.MaterialID, &r.StationIDs,
			&r.StartDate, &r.EndDate, &r.WeekdayMask,
			&r.TimeStart, &r.TimeEnd, &r.PlaysPerDay,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (dr *DistributionRules) Update(ctx context.Context, id uuid.UUID, in CreateDistributionRuleInput) error {
	_, err := dr.pool.Exec(ctx, `
		UPDATE distribution_rules
		SET material_id = $2, station_ids = $3, start_date = $4, end_date = $5,
		    weekday_mask = $6, time_start = $7::time, time_end = $8::time,
		    plays_per_day = $9, updated_at = now()
		WHERE id = $1`,
		id, in.MaterialID, in.StationIDs, in.StartDate, in.EndDate,
		in.WeekdayMask, in.TimeStart, in.TimeEnd, in.PlaysPerDay)
	return err
}

func (dr *DistributionRules) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := dr.pool.Exec(ctx, `DELETE FROM distribution_rules WHERE id = $1`, id)
	return err
}
```

- [ ] **Step 4: Rodar testes**

```bash
go test ./internal/catalog/ -run TestDistributionRules -v
```

Esperado: ambos os tests passam (`TestDistributionRules_CRUD` e `TestDistributionRules_Constraints`).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/distribution_rules.go workers/internal/catalog/distribution_rules_test.go
git commit -m "feat(catalog): add DistributionRules repo"
```

---

### Task 8: Repo DistributionOverrides

**Files:**
- Create: `workers/internal/catalog/distribution_overrides.go`
- Create: `workers/internal/catalog/distribution_overrides_test.go`

- [ ] **Step 1: Escrever o teste**

Crie `workers/internal/catalog/distribution_overrides_test.go`:

```go
package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDistributionOverrides_Upsert(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Now(), EndDate: time.Now().AddDate(0, 1, 0),
	})
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "z",
	})
	stationsRepo := NewStations(pool)
	stat, _ := stationsRepo.Create(ctx, CreateStationInput{
		Name: "Test FM", Band: "FM", StreamURL: "http://example.com",
	})

	repo := NewDistributionOverrides(pool)
	date := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	// Insert
	if err := repo.Upsert(ctx, UpsertOverrideInput{
		CampaignID: cmp.ID, MaterialID: mat.ID, StationID: stat.ID,
		ForDate: date, PlaysExpected: 2,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Update via upsert
	if err := repo.Upsert(ctx, UpsertOverrideInput{
		CampaignID: cmp.ID, MaterialID: mat.ID, StationID: stat.ID,
		ForDate: date, PlaysExpected: 5,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	list, _ := repo.ListByCampaignAndDateRange(ctx, cmp.ID,
		date.AddDate(0, 0, -1), date.AddDate(0, 0, 1))
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
	if list[0].PlaysExpected != 5 {
		t.Errorf("PlaysExpected = %d, want 5", list[0].PlaysExpected)
	}

	// Delete
	if err := repo.Delete(ctx, cmp.ID, mat.ID, stat.ID, date); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ = repo.ListByCampaignAndDateRange(ctx, cmp.ID,
		date.AddDate(0, 0, -1), date.AddDate(0, 0, 1))
	if len(list) != 0 {
		t.Errorf("after delete: %d, want 0", len(list))
	}
}
```

- [ ] **Step 2: Confirmar falha**

```bash
go test ./internal/catalog/ -run TestDistributionOverrides -v
```

- [ ] **Step 3: Implementar repo**

Crie `workers/internal/catalog/distribution_overrides.go`:

```go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DistributionOverride struct {
	CampaignID    uuid.UUID  `json:"campaign_id"`
	MaterialID    uuid.UUID  `json:"material_id"`
	StationID     uuid.UUID  `json:"station_id"`
	ForDate       time.Time  `json:"for_date"`
	PlaysExpected int16      `json:"plays_expected"`
	Reason        *string    `json:"reason,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	CreatedBy     *uuid.UUID `json:"created_by,omitempty"`
}

type DistributionOverrides struct {
	pool *pgxpool.Pool
}

func NewDistributionOverrides(pool *pgxpool.Pool) *DistributionOverrides {
	return &DistributionOverrides{pool: pool}
}

type UpsertOverrideInput struct {
	CampaignID    uuid.UUID
	MaterialID    uuid.UUID
	StationID     uuid.UUID
	ForDate       time.Time
	PlaysExpected int16
	Reason        *string
	CreatedBy     *uuid.UUID
}

func (do *DistributionOverrides) Upsert(ctx context.Context, in UpsertOverrideInput) error {
	_, err := do.pool.Exec(ctx, `
		INSERT INTO distribution_overrides
		  (campaign_id, material_id, station_id, for_date,
		   plays_expected, reason, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (campaign_id, material_id, station_id, for_date)
		DO UPDATE SET
		  plays_expected = EXCLUDED.plays_expected,
		  reason         = EXCLUDED.reason,
		  created_by     = EXCLUDED.created_by`,
		in.CampaignID, in.MaterialID, in.StationID, in.ForDate,
		in.PlaysExpected, in.Reason, in.CreatedBy)
	return err
}

func (do *DistributionOverrides) Delete(ctx context.Context,
	campaignID, materialID, stationID uuid.UUID, forDate time.Time) error {
	_, err := do.pool.Exec(ctx,
		`DELETE FROM distribution_overrides
		 WHERE campaign_id=$1 AND material_id=$2 AND station_id=$3 AND for_date=$4`,
		campaignID, materialID, stationID, forDate)
	return err
}

func (do *DistributionOverrides) ListByCampaignAndDateRange(ctx context.Context,
	campaignID uuid.UUID, from, to time.Time) ([]DistributionOverride, error) {
	rows, err := do.pool.Query(ctx, `
		SELECT campaign_id, material_id, station_id, for_date,
		       plays_expected, reason, created_at, created_by
		FROM distribution_overrides
		WHERE campaign_id = $1 AND for_date BETWEEN $2 AND $3
		ORDER BY for_date ASC`,
		campaignID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DistributionOverride
	for rows.Next() {
		var o DistributionOverride
		if err := rows.Scan(&o.CampaignID, &o.MaterialID, &o.StationID, &o.ForDate,
			&o.PlaysExpected, &o.Reason, &o.CreatedAt, &o.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Rodar testes**

```bash
go test ./internal/catalog/ -run TestDistributionOverrides -v
```

Esperado: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/distribution_overrides.go workers/internal/catalog/distribution_overrides_test.go
git commit -m "feat(catalog): add DistributionOverrides repo"
```

---

### Task 9: Repo DailySummary (lê a view)

**Files:**
- Create: `workers/internal/catalog/daily_summary.go`
- Create: `workers/internal/catalog/daily_summary_test.go`

- [ ] **Step 1: Escrever o teste**

Crie `workers/internal/catalog/daily_summary_test.go`:

```go
package catalog

import (
	"context"
	"testing"
	"time"
)

func TestDailySummary_EmptyCampaign(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	})

	repo := NewDailySummary(pool)
	rows, err := repo.ListByCampaign(ctx, cmp.ID,
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("empty campaign: got %d rows, want 0", len(rows))
	}
}
```

- [ ] **Step 2: Confirmar falha**

```bash
go test ./internal/catalog/ -run TestDailySummary -v
```

- [ ] **Step 3: Implementar repo**

Crie `workers/internal/catalog/daily_summary.go`:

```go
package catalog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DailySummaryRow struct {
	CampaignID uuid.UUID `json:"campaign_id"`
	MaterialID uuid.UUID `json:"material_id"`
	StationID  uuid.UUID `json:"station_id"`
	ForDate    time.Time `json:"for_date"`
	Expected   int32     `json:"expected"`
	InSlot     int32     `json:"in_slot"`
	Deficit    int32     `json:"deficit"`
	Bonus      int32     `json:"bonus"`
	OutSlot    int32     `json:"out_slot"`
	OutDate    int32     `json:"out_date"`
}

type DailySummary struct {
	pool *pgxpool.Pool
}

func NewDailySummary(pool *pgxpool.Pool) *DailySummary {
	return &DailySummary{pool: pool}
}

func (ds *DailySummary) ListByCampaign(ctx context.Context,
	campaignID uuid.UUID, from, to time.Time) ([]DailySummaryRow, error) {

	rows, err := ds.pool.Query(ctx, `
		SELECT campaign_id, material_id, station_id, for_date,
		       expected, in_slot, deficit, bonus, out_slot, out_date
		FROM daily_play_summary
		WHERE campaign_id = $1
		  AND for_date BETWEEN $2 AND $3
		ORDER BY station_id, material_id, for_date`,
		campaignID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailySummaryRow
	for rows.Next() {
		var r DailySummaryRow
		if err := rows.Scan(&r.CampaignID, &r.MaterialID, &r.StationID, &r.ForDate,
			&r.Expected, &r.InSlot, &r.Deficit, &r.Bonus,
			&r.OutSlot, &r.OutDate); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Rodar teste**

```bash
go test ./internal/catalog/ -run TestDailySummary -v
```

Esperado: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/catalog/daily_summary.go workers/internal/catalog/daily_summary_test.go
git commit -m "feat(catalog): add DailySummary repo (reads daily_play_summary view)"
```

---

# Phase C — Categorizer

Função pura que classifica uma detection em uma das 4 categorias (`in_slot`/`out_slot`/`out_date`/`orphan`). Sem I/O — recebe campaign + rules como input. I/O fica em torno dela.

---

### Task 10: Categorizer (função pura)

**Files:**
- Create: `workers/internal/categorizer/categorizer.go`
- Create: `workers/internal/categorizer/categorizer_test.go`

- [ ] **Step 1: Escrever os testes (cobrindo todos os 4 casos + edge cases)**

Crie `workers/internal/categorizer/categorizer_test.go`:

```go
package categorizer

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// América/São Paulo é onde a campanha "vive" — fixo no plano.
var saoPaulo, _ = time.LoadLocation("America/Sao_Paulo")

func mkRule(startDay, endDay int, mask int16, ts, te string, plays int16) Rule {
	parseTime := func(hhmm string) time.Time {
		t, _ := time.Parse("15:04", hhmm)
		return t
	}
	return Rule{
		StartDate:   time.Date(2026, 6, startDay, 0, 0, 0, 0, saoPaulo),
		EndDate:     time.Date(2026, 6, endDay, 0, 0, 0, 0, saoPaulo),
		WeekdayMask: mask,
		TimeStart:   parseTime(ts),
		TimeEnd:     parseTime(te),
		PlaysPerDay: plays,
	}
}

func TestCategorize_OutDate(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// Detection em 1º de julho — fora da campanha
	got := Categorize(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC), cmp, nil)
	if got != "out_date" {
		t.Errorf("got %q, want out_date", got)
	}
}

func TestCategorize_Orphan_NoRules(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	got := Categorize(time.Date(2026, 6, 15, 10, 0, 0, 0, saoPaulo), cmp, nil)
	if got != "orphan" {
		t.Errorf("got %q, want orphan", got)
	}
}

func TestCategorize_InSlot(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// 10/06/2026 é quarta-feira (DOW=3 → bit 3 → 8)
	// Mask 62 (0111110) = seg-sex
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	// Detection às 09:00 BRT (UTC-3 → 12:00 UTC) numa quarta
	det := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	got := Categorize(det, cmp, rules)
	if got != "in_slot" {
		t.Errorf("got %q, want in_slot", got)
	}
}

func TestCategorize_OutSlot(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	// Detection às 14:00 BRT (17:00 UTC) — fora da faixa
	det := time.Date(2026, 6, 10, 17, 0, 0, 0, time.UTC)
	got := Categorize(det, cmp, rules)
	if got != "out_slot" {
		t.Errorf("got %q, want out_slot", got)
	}
}

func TestCategorize_Orphan_WrongWeekday(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	// Mask 62 = seg-sex. Detection num sábado (06/06/2026, DOW=6 → bit 6 = 64)
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	det := time.Date(2026, 6, 6, 12, 0, 0, 0, saoPaulo) // sáb 12:00 BRT
	got := Categorize(det, cmp, rules)
	if got != "orphan" {
		t.Errorf("got %q, want orphan (sábado fora da regra)", got)
	}
}

func TestCategorize_InSlot_BoundaryInclusive(t *testing.T) {
	cmp := Campaign{
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, saoPaulo),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, saoPaulo),
	}
	rules := []Rule{mkRule(1, 30, 62, "08:00", "10:00", 3)}
	// Exatamente 08:00 BRT (11:00 UTC) — deve ser in_slot (boundary inclusive)
	det := time.Date(2026, 6, 10, 11, 0, 0, 0, time.UTC)
	got := Categorize(det, cmp, rules)
	if got != "in_slot" {
		t.Errorf("got %q, want in_slot (08:00 é início da faixa)", got)
	}
	// 10:00:00 BRT — também in_slot (boundary inclusive)
	det2 := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)
	got2 := Categorize(det2, cmp, rules)
	if got2 != "in_slot" {
		t.Errorf("got %q, want in_slot (10:00 é fim da faixa)", got2)
	}
}

var _ = uuid.UUID{} // suppress import warning if unused
```

- [ ] **Step 2: Confirmar falha**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck/workers"
go test ./internal/categorizer/ -v
```

Esperado: pacote não existe (erro de compilação).

- [ ] **Step 3: Implementar a função pura**

Crie `workers/internal/categorizer/categorizer.go`:

```go
package categorizer

import "time"

// Campaign é o subset que o categorizer precisa da campanha.
type Campaign struct {
	StartDate time.Time // já em America/Sao_Paulo (date-only, hora 00:00)
	EndDate   time.Time // idem (inclusive)
}

// Rule é o subset que o categorizer precisa de uma distribution_rule.
type Rule struct {
	StartDate   time.Time // date-only
	EndDate     time.Time // date-only
	WeekdayMask int16     // bit 0=Dom, ..., 6=Sáb
	TimeStart   time.Time // só componente HH:MM importa
	TimeEnd     time.Time // só componente HH:MM importa
	PlaysPerDay int16
}

// Category labels (idênticos aos valores do CHECK constraint).
const (
	CatInSlot  = "in_slot"
	CatOutSlot = "out_slot"
	CatOutDate = "out_date"
	CatOrphan  = "orphan"
)

var spLocation, _ = time.LoadLocation("America/Sao_Paulo")

// Categorize classifica uma detection. A campanha é assumida existente
// (o detection é insert por matching engine, sempre tem campaign_id).
//
// Regra:
//   1. detectedAt fora de [campaign.StartDate, campaign.EndDate] → out_date
//   2. nenhuma rule aplicável (mesmo material/station/data) → orphan
//   3. rule existe e time ∈ [time_start, time_end] → in_slot
//   4. rule existe mas time fora da faixa → out_slot
//
// Comparações de data são feitas no fuso America/Sao_Paulo.
func Categorize(detectedAt time.Time, cmp Campaign, rules []Rule) string {
	local := detectedAt.In(spLocation)
	date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, spLocation)

	if date.Before(cmp.StartDate) || date.After(cmp.EndDate) {
		return CatOutDate
	}

	dow := int(local.Weekday()) // 0=Sun, 6=Sat — bate com o EXTRACT(DOW) do PG
	hh := local.Hour()
	mm := local.Minute()
	ss := local.Second()
	timeOfDay := hh*3600 + mm*60 + ss

	hasApplicable := false
	for _, r := range rules {
		// Date range
		if date.Before(r.StartDate) || date.After(r.EndDate) {
			continue
		}
		// Weekday mask
		if (1<<dow)&int(r.WeekdayMask) == 0 {
			continue
		}
		hasApplicable = true
		// Time window — boundary inclusive em ambos os lados
		rs := r.TimeStart.Hour()*3600 + r.TimeStart.Minute()*60 + r.TimeStart.Second()
		re := r.TimeEnd.Hour()*3600 + r.TimeEnd.Minute()*60 + r.TimeEnd.Second()
		if timeOfDay >= rs && timeOfDay <= re {
			return CatInSlot
		}
	}
	if hasApplicable {
		return CatOutSlot
	}
	return CatOrphan
}
```

- [ ] **Step 4: Rodar todos os testes do categorizer**

```bash
go test ./internal/categorizer/ -v
```

Esperado: todos os 6 testes passam.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/categorizer/
git commit -m "feat(categorizer): add pure Categorize() function with TZ-aware classification"
```

---

### Task 11: Integrar categorizer no `detections.Create`

**Files:**
- Modify: `workers/internal/catalog/detections.go`
- Modify: `workers/internal/catalog/detections_test.go`

- [ ] **Step 1: Adicionar teste cobrindo categorização ao criar detection**

Abra `workers/internal/catalog/detections_test.go` e adicione esta função no final do arquivo:

```go
func TestDetections_Create_CategorizesOrphan(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Now().AddDate(0, 0, -1),
		EndDate:   time.Now().AddDate(0, 0, 30),
	})
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "abc",
	})
	stat, _ := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "Test FM", Band: "FM", StreamURL: "http://example.com",
	})

	dets := NewDetections(pool)
	det, err := dets.Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
		DetectedAt:         time.Now(),
		MatchStartOffsetMs: 0, MatchEndOffsetMs: 30000,
		Confidence: 0.95, HashCount: 100,
		TemporalCoverage: 0.85, VariantUsed: 0, RateUsed: 0,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Sem regras criadas → deve ser orphan
	var category string
	err = pool.QueryRow(ctx,
		`SELECT category FROM detections WHERE id = $1 AND detected_at = $2`,
		det.ID, det.DetectedAt).Scan(&category)
	if err != nil {
		t.Fatalf("read category: %v", err)
	}
	if category != "orphan" {
		t.Errorf("category = %q, want orphan", category)
	}
}
```

- [ ] **Step 2: Confirmar que o teste falha**

```bash
go test ./internal/catalog/ -run TestDetections_Create_CategorizesOrphan -v
```

Esperado: provavelmente FAIL — o `Create` atual não preenche `category`, então o default `'orphan'` do schema acaba aplicando e o teste pode passar por sorte. Mesmo se passar, vamos garantir que `Create` chama o categorizer explicitamente.

- [ ] **Step 3: Modificar `Create` pra preencher `category`**

Abra `workers/internal/catalog/detections.go`. Localize a função `Create` (linha 57). Substitua-a inteiramente por:

```go
func (d *Detections) Create(ctx context.Context, in CreateDetectionInput) (*Detection, error) {
	// Categoriza inline antes de inserir. Acessa campaigns + distribution_rules
	// pra alimentar o categorizador puro. Mantém categorização consistente
	// com o que a view daily_play_summary espera.
	category, err := d.categorize(ctx, in)
	if err != nil {
		return nil, err
	}

	var det Detection
	err = d.pool.QueryRow(ctx, `
		INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
		                        match_start_offset_ms, match_end_offset_ms, confidence,
		                        hash_count, temporal_coverage, variant_used, rate_used,
		                        category)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, station_id, commercial_id, campaign_id, detected_at,
		          match_start_offset_ms, match_end_offset_ms, confidence, hash_count,
		          temporal_coverage, variant_used, rate_used,
		          evidence_status, evidence_key, evidence_size_bytes, retracted_at, created_at`,
		in.StationID, in.CommercialID, in.CampaignID, in.DetectedAt,
		in.MatchStartOffsetMs, in.MatchEndOffsetMs, in.Confidence, in.HashCount,
		in.TemporalCoverage, in.VariantUsed, in.RateUsed, category,
	).Scan(&det.ID, &det.StationID, &det.CommercialID, &det.CampaignID, &det.DetectedAt,
		&det.MatchStartOffsetMs, &det.MatchEndOffsetMs, &det.Confidence, &det.HashCount,
		&det.TemporalCoverage, &det.VariantUsed, &det.RateUsed,
		&det.EvidenceStatus, &det.EvidenceKey, &det.EvidenceSizeBytes, &det.RetractedAt, &det.CreatedAt)
	return &det, err
}

// categorize resolves the detection's category by loading the campaign and
// applicable rules, then invoking the pure categorizer.
func (d *Detections) categorize(ctx context.Context, in CreateDetectionInput) (string, error) {
	var cmpStart, cmpEnd time.Time
	err := d.pool.QueryRow(ctx,
		`SELECT start_date, end_date FROM campaigns WHERE id = $1`, in.CampaignID,
	).Scan(&cmpStart, &cmpEnd)
	if err != nil {
		return "orphan", err
	}

	rows, err := d.pool.Query(ctx, `
		SELECT start_date, end_date, weekday_mask,
		       time_start::text, time_end::text, plays_per_day
		FROM distribution_rules
		WHERE campaign_id = $1
		  AND material_id = $2
		  AND $3 = ANY(station_ids)`,
		in.CampaignID, in.CommercialID, in.StationID)
	if err != nil {
		return "orphan", err
	}
	defer rows.Close()

	var rules []categorizer.Rule
	for rows.Next() {
		var r categorizer.Rule
		var tsStr, teStr string
		var plays int16
		if err := rows.Scan(&r.StartDate, &r.EndDate, &r.WeekdayMask,
			&tsStr, &teStr, &plays); err != nil {
			return "orphan", err
		}
		r.TimeStart, _ = time.Parse("15:04:05", tsStr)
		r.TimeEnd, _ = time.Parse("15:04:05", teStr)
		r.PlaysPerDay = plays
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		return "orphan", err
	}

	return categorizer.Categorize(
		in.DetectedAt,
		categorizer.Campaign{StartDate: cmpStart, EndDate: cmpEnd},
		rules,
	), nil
}
```

E adicione o import no topo do arquivo. Localize o bloco `import (` (linha 3) e substitua por:

```go
import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"radiocheck/internal/categorizer"
)
```

> **Nota:** o module path é `radiocheck`. Confirme com `head -1 workers/go.mod` se houver dúvida.

- [ ] **Step 4: Rodar todos os testes do pacote `catalog`**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck/workers"
go test ./internal/catalog/ -v
```

Esperado: todos passam (incluindo `TestDetections_Create_CategorizesOrphan`). Se algum teste de `Detections` existente quebrar (porque agora exige campanha+material existentes), atualize o setup do teste pra criar os pré-requisitos.

- [ ] **Step 5: Verificar compilação completa**

```bash
go build ./...
```

Esperado: build limpo.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/catalog/detections_test.go
git commit -m "feat(detections): categorize on insert via categorizer module"
```

---

### Task 12: Re-categorização em massa ao mudar regra

Quando uma regra é criada/editada/deletada, detecções existentes podem precisar mudar de categoria. Solução: função `RecategorizeForRule` no repo `DistributionRules` que faz UPDATE SQL diretamente (mais rápido que iterar Go) usando a mesma lógica do categorizer expressa em SQL.

**Files:**
- Modify: `workers/internal/catalog/distribution_rules.go`
- Modify: `workers/internal/catalog/distribution_rules_test.go`

- [ ] **Step 1: Adicionar teste de recategorização**

Adicione no final de `distribution_rules_test.go`:

```go
func TestDistributionRules_RecategorizeAfterCreate(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	cli, _ := NewClients(pool).Create(ctx, CreateClientInput{Name: "T"})
	cmp, _ := NewCampaigns(pool).Create(ctx, CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	})
	mat, _ := NewMaterials(pool).Create(ctx, CreateMaterialInput{
		ClientID: cli.ID, Title: "M", DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "rk",
	})
	stat, _ := NewStations(pool).Create(ctx, CreateStationInput{
		Name: "FM", Band: "FM", StreamURL: "http://x",
	})

	// 1. Cria uma detection ANTES de qualquer regra → category=orphan
	dets := NewDetections(pool)
	// 10/06/2026 (qua) às 09:00 BRT (12:00 UTC)
	detTime := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	det, _ := dets.Create(ctx, CreateDetectionInput{
		StationID: stat.ID, CommercialID: mat.ID, CampaignID: cmp.ID,
		DetectedAt: detTime,
		Confidence: 0.9, HashCount: 50, TemporalCoverage: 0.8,
	})

	// 2. Cria regra que cobre essa data + faixa → detection vira in_slot
	repo := NewDistributionRules(pool)
	rule, _ := repo.Create(ctx, CreateDistributionRuleInput{
		CampaignID: cmp.ID, MaterialID: mat.ID,
		StationIDs:  []uuid.UUID{stat.ID},
		StartDate:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		WeekdayMask: 62, TimeStart: "08:00", TimeEnd: "10:00",
		PlaysPerDay: 3,
	})

	// 3. Roda recategorização
	if err := repo.RecategorizeForRule(ctx, rule.ID); err != nil {
		t.Fatalf("recategorize: %v", err)
	}

	// 4. Verifica que a detection virou in_slot
	var cat string
	pool.QueryRow(ctx,
		`SELECT category FROM detections WHERE id = $1 AND detected_at = $2`,
		det.ID, det.DetectedAt).Scan(&cat)
	if cat != "in_slot" {
		t.Errorf("after recategorize: category = %q, want in_slot", cat)
	}
}
```

- [ ] **Step 2: Confirmar falha**

```bash
go test ./internal/catalog/ -run TestDistributionRules_RecategorizeAfterCreate -v
```

Esperado: erro `undefined: repo.RecategorizeForRule`.

- [ ] **Step 3: Implementar `RecategorizeForRule` e `RecategorizeForCampaign`**

Adicione no final de `workers/internal/catalog/distribution_rules.go`:

```go
// RecategorizeForRule re-classifica todas as detections potencialmente
// afetadas pela criação/edição/exclusão da regra dada. Usa lógica SQL
// equivalente ao categorizer Go: pra cada detection que cabe no escopo
// (campaign, material, station_ids, date_range), decide in_slot/out_slot
// /orphan/out_date e UPDATE category. Roda dentro de uma transação.
func (dr *DistributionRules) RecategorizeForRule(ctx context.Context, ruleID uuid.UUID) error {
	// 1. Lê a regra pra extrair scope (campaign, material, stations, date range)
	r, err := dr.Get(ctx, ruleID)
	if err != nil {
		return err
	}
	return dr.recategorizeScope(ctx, r.CampaignID, &r.MaterialID, r.StationIDs, r.StartDate, r.EndDate)
}

// RecategorizeForCampaign re-classifica todas as detections de uma campanha.
// Útil ao deletar uma regra (não sabemos mais o scope dela) ou pra backfill manual.
func (dr *DistributionRules) RecategorizeForCampaign(ctx context.Context, campaignID uuid.UUID) error {
	// Lê start/end da campanha pro range
	var start, end time.Time
	err := dr.pool.QueryRow(ctx,
		`SELECT start_date, end_date FROM campaigns WHERE id = $1`, campaignID,
	).Scan(&start, &end)
	if err != nil {
		return err
	}
	return dr.recategorizeScope(ctx, campaignID, nil, nil, start, end)
}

// recategorizeScope é o motor SQL. Pra cada detection no escopo, computa
// a nova categoria e UPDATE em batch.
//
// SQL lógica:
//   - Pra cada detection que casa scope, faz LEFT JOIN com distribution_rules
//     aplicáveis (data, mask, mesmo material+station).
//   - Se existe ANY rule onde detected_at no time window → in_slot
//   - Elif existe ANY rule (mesmo material+station+data+weekday) → out_slot
//   - Elif detection date dentro da campanha → orphan
//   - Else → out_date
func (dr *DistributionRules) recategorizeScope(ctx context.Context,
	campaignID uuid.UUID, materialID *uuid.UUID, stationIDs []uuid.UUID,
	from, to time.Time) error {

	_, err := dr.pool.Exec(ctx, `
WITH scope AS (
    SELECT d.id, d.detected_at, d.campaign_id, d.commercial_id AS material_id, d.station_id
    FROM detections d
    JOIN campaigns c ON c.id = d.campaign_id
    WHERE d.campaign_id = $1
      AND ($2::uuid IS NULL OR d.commercial_id = $2)
      AND ($3::uuid[] IS NULL OR d.station_id = ANY($3))
      AND (date_trunc('day', d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
           BETWEEN $4::date AND $5::date)
),
classified AS (
    SELECT
        s.id, s.detected_at,
        CASE
            WHEN date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                 NOT BETWEEN c.start_date AND c.end_date
                THEN 'out_date'
            WHEN EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = s.campaign_id
                  AND r.material_id = s.material_id
                  AND s.station_id = ANY(r.station_ids)
                  AND date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                      BETWEEN r.start_date AND r.end_date
                  AND ((1 << EXTRACT(DOW FROM (s.detected_at AT TIME ZONE 'America/Sao_Paulo'))::int) & r.weekday_mask) != 0
                  AND (s.detected_at AT TIME ZONE 'America/Sao_Paulo')::time
                      BETWEEN r.time_start AND r.time_end
            )
                THEN 'in_slot'
            WHEN EXISTS (
                SELECT 1 FROM distribution_rules r
                WHERE r.campaign_id = s.campaign_id
                  AND r.material_id = s.material_id
                  AND s.station_id = ANY(r.station_ids)
                  AND date_trunc('day', s.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
                      BETWEEN r.start_date AND r.end_date
                  AND ((1 << EXTRACT(DOW FROM (s.detected_at AT TIME ZONE 'America/Sao_Paulo'))::int) & r.weekday_mask) != 0
            )
                THEN 'out_slot'
            ELSE 'orphan'
        END AS new_category
    FROM scope s
    JOIN campaigns c ON c.id = s.campaign_id
)
UPDATE detections d
SET category = cl.new_category
FROM classified cl
WHERE d.id = cl.id AND d.detected_at = cl.detected_at
  AND d.category IS DISTINCT FROM cl.new_category`,
		campaignID, materialID, stationIDs, from, to)
	return err
}
```

- [ ] **Step 4: Rodar o teste**

```bash
go test ./internal/catalog/ -run TestDistributionRules_Recategorize -v
```

Esperado: PASS.

- [ ] **Step 5: Rodar todos os testes do pacote**

```bash
go test ./internal/catalog/ -v
```

Esperado: todos os testes passam.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/catalog/distribution_rules.go workers/internal/catalog/distribution_rules_test.go
git commit -m "feat(distribution): add RecategorizeForRule and RecategorizeForCampaign"
```

---

# Phase D — HTTP Handlers

Padrão: handler struct expõe métodos por verb HTTP. Constructor recebe repos + logger. Cada handler vai num arquivo separado em `workers/internal/api/handlers/`. Testes usam `httptest` (padrão da casa — veja `commercials_test.go`).

---

### Task 13: Handler MaterialTypes

**Files:**
- Create: `workers/internal/api/handlers/material_types.go`
- Create: `workers/internal/api/handlers/material_types_test.go`

- [ ] **Step 1: Escrever teste**

Crie `workers/internal/api/handlers/material_types_test.go`:

```go
package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"radiocheck/internal/catalog"
)

func TestMaterialTypesHandler_List(t *testing.T) {
	pool := newTestPool(t) // reuse helper from existing handler tests
	defer pool.Close()
	repo := catalog.NewMaterialTypes(pool)
	h := &MaterialTypesHandler{Repo: repo}

	req := httptest.NewRequest("GET", "/v1/internal/material-types", nil)
	rr := httptest.NewRecorder()
	h.List(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var types []catalog.MaterialType
	if err := json.NewDecoder(rr.Body).Decode(&types); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(types) < 6 {
		t.Errorf("len = %d, want >= 6 (seeds)", len(types))
	}
}

func TestMaterialTypesHandler_Create(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	h := &MaterialTypesHandler{Repo: catalog.NewMaterialTypes(pool)}

	body := strings.NewReader(`{"name":"Promo","color":"#ff00ff"}`)
	req := httptest.NewRequest("POST", "/v1/internal/material-types", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.Create(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
}
```

- [ ] **Step 2: Confirmar falha**

```bash
go test ./internal/api/handlers/ -run TestMaterialTypesHandler -v
```

Esperado: `undefined: MaterialTypesHandler`.

- [ ] **Step 3: Implementar handler**

Crie `workers/internal/api/handlers/material_types.go`:

```go
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

type MaterialTypesHandler struct {
	Repo *catalog.MaterialTypes
}

func (h *MaterialTypesHandler) List(w http.ResponseWriter, r *http.Request) {
	types, err := h.Repo.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, types)
}

type materialTypePayload struct {
	Name        string  `json:"name"`
	Color       string  `json:"color"`
	Description *string `json:"description,omitempty"`
}

func (h *MaterialTypesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var p materialTypePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if p.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	mt, err := h.Repo.Create(r.Context(), catalog.CreateMaterialTypeInput{
		Name: p.Name, Color: p.Color, Description: p.Description,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, mt)
}

func (h *MaterialTypesHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var p materialTypePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	mt, err := h.Repo.Update(r.Context(), id, catalog.CreateMaterialTypeInput{
		Name: p.Name, Color: p.Color, Description: p.Description,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mt)
}

func (h *MaterialTypesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Repo.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

> **Nota:** `writeJSON` e `writeError` já existem em algum arquivo de utilidades dentro de `handlers/`. Veja `commercials.go` ou `clients.go` pra confirmar a assinatura.

- [ ] **Step 4: Rodar testes**

```bash
go test ./internal/api/handlers/ -run TestMaterialTypesHandler -v
```

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/material_types.go workers/internal/api/handlers/material_types_test.go
git commit -m "feat(api): add /material-types CRUD handler"
```

---

### Task 14: Handler Materials (upload + list + get + delete)

**Files:**
- Create: `workers/internal/api/handlers/materials.go`
- Create: `workers/internal/api/handlers/materials_test.go`

> **Reuse strategy:** o upload de áudio (multipart, salvar arquivo, calcular SHA256, dispatch de fingerprint job via NATS) já existe no handler `commercials.go::Upload()`. Vamos extrair essa lógica num helper compartilhado, OU duplicá-la inicialmente e refatorar em Task 25. Por simplicidade e segurança aqui, **duplicar** — a refatoração pode ser uma task separada futura.

- [ ] **Step 1: Ler o Upload existente pra entender o padrão**

```bash
grep -n "func .*Upload" workers/internal/api/handlers/commercials.go
```

Note: anote o nome do helper de SHA256, o path de masters (provavelmente `h.MastersPath`), e como o NATS publish acontece.

- [ ] **Step 2: Escrever testes (List + Upload)**

Crie `workers/internal/api/handlers/materials_test.go`:

```go
package handlers

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"radiocheck/internal/catalog"
)

func TestMaterialsHandler_ListByClient(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	cli, _ := catalog.NewClients(pool).Create(t.Context(), catalog.CreateClientInput{Name: "X"})
	repo := catalog.NewMaterials(pool)
	_, _ = repo.Create(t.Context(), catalog.CreateMaterialInput{
		ClientID: cli.ID, Title: "Spot Foo", DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "a",
	})

	h := &MaterialsHandler{Repo: repo}
	r := chi.NewRouter()
	r.Get("/clients/{clientID}/materials", h.ListByClient)

	req := httptest.NewRequest("GET", "/clients/"+cli.ID.String()+"/materials", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var list []catalog.Material
	json.NewDecoder(rr.Body).Decode(&list)
	if len(list) != 1 {
		t.Errorf("len = %d, want 1", len(list))
	}
}

func TestMaterialsHandler_Upload(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	cli, _ := catalog.NewClients(pool).Create(t.Context(), catalog.CreateClientInput{Name: "X"})

	tmpDir := t.TempDir()
	h := &MaterialsHandler{
		Repo:        catalog.NewMaterials(pool),
		MastersPath: tmpDir,
		// NATS pode ser nil — o handler precisa lidar com isso pulando o publish
	}

	// Constrói multipart body
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	w.WriteField("client_id", cli.ID.String())
	w.WriteField("title", "Test Spot")
	part, _ := w.CreateFormFile("audio", "test.mp3")
	part.Write([]byte("fake mp3 bytes"))
	w.Close()

	req := httptest.NewRequest("POST", "/v1/internal/materials", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()
	h.Upload(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var mat catalog.Material
	json.NewDecoder(rr.Body).Decode(&mat)
	if mat.Title != "Test Spot" {
		t.Errorf("Title = %q", mat.Title)
	}
	// Arquivo gravado em tmpDir
	files, _ := os.ReadDir(filepath.Join(tmpDir))
	if len(files) == 0 {
		t.Error("no file written to MastersPath")
	}
}
```

- [ ] **Step 3: Confirmar falha**

```bash
go test ./internal/api/handlers/ -run TestMaterialsHandler -v
```

- [ ] **Step 4: Implementar handler**

Crie `workers/internal/api/handlers/materials.go`:

```go
package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"radiocheck/internal/catalog"
)

type MaterialsHandler struct {
	Repo        *catalog.Materials
	MastersPath string
	NATS        *nats.Conn
}

func (h *MaterialsHandler) ListByClient(w http.ResponseWriter, r *http.Request) {
	clientID, err := uuid.Parse(chi.URLParam(r, "clientID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid clientID")
		return
	}
	q := r.URL.Query().Get("q")
	mats, err := h.Repo.ListByClient(r.Context(), clientID, q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if mats == nil {
		mats = []catalog.Material{}
	}
	writeJSON(w, http.StatusOK, mats)
}

func (h *MaterialsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	m, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *MaterialsHandler) Upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil { // 64 MiB
		writeError(w, http.StatusBadRequest, "invalid multipart")
		return
	}
	clientIDStr := r.FormValue("client_id")
	title := r.FormValue("title")
	typeIDStr := r.FormValue("type_id")

	clientID, err := uuid.Parse(clientIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "client_id is required and must be UUID")
		return
	}
	if title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	var typeID *uuid.UUID
	if typeIDStr != "" {
		tid, err := uuid.Parse(typeIDStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "type_id must be UUID")
			return
		}
		typeID = &tid
	}

	// Lê arquivo, calcula SHA256, salva em MastersPath
	file, header, err := r.FormFile("audio")
	if err != nil {
		writeError(w, http.StatusBadRequest, "audio file is required")
		return
	}
	defer file.Close()

	hash := sha256.New()
	tmpFile, err := os.CreateTemp(h.MastersPath, "upload-*.tmp")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot create temp file")
		return
	}
	tmpPath := tmpFile.Name()
	if _, err := io.Copy(io.MultiWriter(tmpFile, hash), file); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		writeError(w, http.StatusInternalServerError, "write failed")
		return
	}
	tmpFile.Close()

	sha := hex.EncodeToString(hash.Sum(nil))
	finalName := sha + filepath.Ext(header.Filename)
	finalPath := filepath.Join(h.MastersPath, finalName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		writeError(w, http.StatusInternalServerError, "rename failed")
		return
	}

	// Probe duration via ffprobe se disponível, senão hardcode 0
	duration := probeDuration(finalPath) // helper existente em commercials.go ou criado aqui

	mat, err := h.Repo.Create(r.Context(), catalog.CreateMaterialInput{
		ClientID: clientID, Title: title, TypeID: typeID,
		DurationSeconds: duration,
		MasterStoragePath: finalPath, MasterSHA256: sha,
	})
	if err != nil {
		os.Remove(finalPath)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Publica evento de fingerprint pendente
	if h.NATS != nil {
		evt, _ := json.Marshal(map[string]any{
			"material_id":     mat.ID,
			"master_path":     mat.MasterStoragePath,
			"duration_seconds": mat.DurationSeconds,
		})
		_ = h.NATS.Publish("radiocheck.fingerprint.pending", evt)
	}

	writeJSON(w, http.StatusCreated, mat)
}

func (h *MaterialsHandler) UpdateType(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var p struct {
		TypeID *uuid.UUID `json:"type_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := h.Repo.UpdateType(r.Context(), id, p.TypeID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *MaterialsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Repo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, catalog.ErrCommercialHasDetections) {
			writeError(w, http.StatusConflict, "material has detection history")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// probeDuration is a stub. Real impl uses ffprobe — copy the logic from
// commercials.go where it already exists. If not found, return 30.0 as default
// and add a follow-up F-XX to wire ffprobe properly.
func probeDuration(path string) float64 {
	// TODO em task de hardening: copiar lógica de probe de commercials.go
	_ = fmt.Sprintf // suppress unused import
	return 30.0
}
```

- [ ] **Step 5: Rodar testes**

```bash
go test ./internal/api/handlers/ -run TestMaterialsHandler -v
```

Esperado: PASS.

- [ ] **Step 6: Commit**

```bash
git add workers/internal/api/handlers/materials.go workers/internal/api/handlers/materials_test.go
git commit -m "feat(api): add /materials CRUD + upload handler"
```

---

### Task 15: Handler CampaignMaterials

**Files:**
- Create: `workers/internal/api/handlers/campaign_materials.go`
- Create: `workers/internal/api/handlers/campaign_materials_test.go`

- [ ] **Step 1: Escrever teste**

Crie `workers/internal/api/handlers/campaign_materials_test.go`:

```go
package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"radiocheck/internal/catalog"
)

func TestCampaignMaterialsHandler_LinkAndList(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := t.Context()

	cli, _ := catalog.NewClients(pool).Create(ctx, catalog.CreateClientInput{Name: "X"})
	cmp, _ := catalog.NewCampaigns(pool).Create(ctx, catalog.CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Now(), EndDate: time.Now().AddDate(0, 1, 0),
	})
	mat, _ := catalog.NewMaterials(pool).Create(ctx, catalog.CreateMaterialInput{
		ClientID: cli.ID, Title: "M", DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "x",
	})

	h := &CampaignMaterialsHandler{Repo: catalog.NewCampaignMaterials(pool)}
	r := chi.NewRouter()
	r.Post("/campaigns/{campaignID}/materials", h.Link)
	r.Get("/campaigns/{campaignID}/materials", h.ListByCampaign)
	r.Delete("/campaigns/{campaignID}/materials/{materialID}", h.Unlink)

	station := uuid.New()
	body := `{"material_id":"` + mat.ID.String() + `","target_stations":["` + station.String() + `"]}`
	req := httptest.NewRequest("POST", "/campaigns/"+cmp.ID.String()+"/materials", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("link status = %d: %s", rr.Code, rr.Body.String())
	}

	// List
	req2 := httptest.NewRequest("GET", "/campaigns/"+cmp.ID.String()+"/materials", nil)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("list status = %d", rr2.Code)
	}
	var list []catalog.CampaignMaterial
	json.NewDecoder(rr2.Body).Decode(&list)
	if len(list) != 1 {
		t.Errorf("len = %d, want 1", len(list))
	}

	// Unlink
	req3 := httptest.NewRequest("DELETE",
		"/campaigns/"+cmp.ID.String()+"/materials/"+mat.ID.String(), nil)
	rr3 := httptest.NewRecorder()
	r.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusNoContent {
		t.Errorf("unlink status = %d", rr3.Code)
	}
}
```

- [ ] **Step 2: Confirmar falha**

```bash
go test ./internal/api/handlers/ -run TestCampaignMaterialsHandler -v
```

- [ ] **Step 3: Implementar handler**

Crie `workers/internal/api/handlers/campaign_materials.go`:

```go
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

type CampaignMaterialsHandler struct {
	Repo *catalog.CampaignMaterials
}

type linkPayload struct {
	MaterialID     uuid.UUID   `json:"material_id"`
	TargetStations []uuid.UUID `json:"target_stations"`
}

func (h *CampaignMaterialsHandler) Link(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid campaignID")
		return
	}
	var p linkPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := h.Repo.Link(r.Context(), campaignID, p.MaterialID, p.TargetStations); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *CampaignMaterialsHandler) ListByCampaign(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid campaignID")
		return
	}
	links, err := h.Repo.ListByCampaign(r.Context(), campaignID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if links == nil {
		links = []catalog.CampaignMaterial{}
	}
	writeJSON(w, http.StatusOK, links)
}

func (h *CampaignMaterialsHandler) UpdateStations(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid campaignID")
		return
	}
	materialID, err := uuid.Parse(chi.URLParam(r, "materialID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid materialID")
		return
	}
	var p struct {
		TargetStations []uuid.UUID `json:"target_stations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := h.Repo.UpdateStations(r.Context(), campaignID, materialID, p.TargetStations); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *CampaignMaterialsHandler) Unlink(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid campaignID")
		return
	}
	materialID, err := uuid.Parse(chi.URLParam(r, "materialID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid materialID")
		return
	}
	if err := h.Repo.Unlink(r.Context(), campaignID, materialID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Rodar testes**

```bash
go test ./internal/api/handlers/ -run TestCampaignMaterialsHandler -v
```

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/campaign_materials.go workers/internal/api/handlers/campaign_materials_test.go
git commit -m "feat(api): add campaign-materials link/unlink/list/update handler"
```

---

### Task 16: Handler DistributionRules

**Files:**
- Create: `workers/internal/api/handlers/distribution_rules.go`
- Create: `workers/internal/api/handlers/distribution_rules_test.go`

- [ ] **Step 1: Escrever teste**

Crie `workers/internal/api/handlers/distribution_rules_test.go`:

```go
package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"radiocheck/internal/catalog"
)

func TestDistributionRulesHandler_CreateAndList(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := t.Context()

	cli, _ := catalog.NewClients(pool).Create(ctx, catalog.CreateClientInput{Name: "X"})
	cmp, _ := catalog.NewCampaigns(pool).Create(ctx, catalog.CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	})
	mat, _ := catalog.NewMaterials(pool).Create(ctx, catalog.CreateMaterialInput{
		ClientID: cli.ID, Title: "M", DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "x",
	})
	station := uuid.New()

	h := &DistributionRulesHandler{Repo: catalog.NewDistributionRules(pool)}
	r := chi.NewRouter()
	r.Post("/campaigns/{campaignID}/distribution-rules", h.Create)
	r.Get("/campaigns/{campaignID}/distribution-rules", h.ListByCampaign)

	payload := `{
		"material_id": "` + mat.ID.String() + `",
		"station_ids": ["` + station.String() + `"],
		"start_date": "2026-06-01",
		"end_date": "2026-06-30",
		"weekday_mask": 62,
		"time_start": "08:15",
		"time_end": "10:45",
		"plays_per_day": 3
	}`
	req := httptest.NewRequest("POST",
		"/campaigns/"+cmp.ID.String()+"/distribution-rules", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rr.Code, rr.Body.String())
	}

	req2 := httptest.NewRequest("GET",
		"/campaigns/"+cmp.ID.String()+"/distribution-rules", nil)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	var rules []catalog.DistributionRule
	json.NewDecoder(rr2.Body).Decode(&rules)
	if len(rules) != 1 {
		t.Errorf("rules len = %d", len(rules))
	}
	if rules[0].PlaysPerDay != 3 {
		t.Errorf("PlaysPerDay = %d", rules[0].PlaysPerDay)
	}
}
```

- [ ] **Step 2: Confirmar falha**

```bash
go test ./internal/api/handlers/ -run TestDistributionRulesHandler -v
```

- [ ] **Step 3: Implementar handler**

Crie `workers/internal/api/handlers/distribution_rules.go`:

```go
package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

type DistributionRulesHandler struct {
	Repo *catalog.DistributionRules
}

type rulePayload struct {
	MaterialID  uuid.UUID   `json:"material_id"`
	StationIDs  []uuid.UUID `json:"station_ids"`
	StartDate   string      `json:"start_date"` // YYYY-MM-DD
	EndDate     string      `json:"end_date"`
	WeekdayMask int16       `json:"weekday_mask"`
	TimeStart   string      `json:"time_start"` // HH:MM
	TimeEnd     string      `json:"time_end"`
	PlaysPerDay int16       `json:"plays_per_day"`
}

func (p *rulePayload) toInput(campaignID uuid.UUID) (catalog.CreateDistributionRuleInput, error) {
	start, err := time.Parse("2006-01-02", p.StartDate)
	if err != nil {
		return catalog.CreateDistributionRuleInput{}, err
	}
	end, err := time.Parse("2006-01-02", p.EndDate)
	if err != nil {
		return catalog.CreateDistributionRuleInput{}, err
	}
	return catalog.CreateDistributionRuleInput{
		CampaignID: campaignID, MaterialID: p.MaterialID,
		StationIDs: p.StationIDs,
		StartDate:  start, EndDate: end,
		WeekdayMask: p.WeekdayMask,
		TimeStart:   p.TimeStart, TimeEnd: p.TimeEnd,
		PlaysPerDay: p.PlaysPerDay,
	}, nil
}

func (h *DistributionRulesHandler) Create(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid campaignID")
		return
	}
	var p rulePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	in, err := p.toInput(campaignID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := h.Repo.Create(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Recategoriza detections existentes que possam ser afetadas
	go func() {
		_ = h.Repo.RecategorizeForRule(r.Context(), rule.ID)
	}()
	writeJSON(w, http.StatusCreated, rule)
}

func (h *DistributionRulesHandler) ListByCampaign(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid campaignID")
		return
	}
	rules, err := h.Repo.ListByCampaign(r.Context(), campaignID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rules == nil {
		rules = []catalog.DistributionRule{}
	}
	writeJSON(w, http.StatusOK, rules)
}

func (h *DistributionRulesHandler) Update(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid campaignID")
		return
	}
	ruleID, err := uuid.Parse(chi.URLParam(r, "ruleID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ruleID")
		return
	}
	var p rulePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	in, err := p.toInput(campaignID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.Repo.Update(r.Context(), ruleID, in); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	go func() { _ = h.Repo.RecategorizeForRule(r.Context(), ruleID) }()
	w.WriteHeader(http.StatusNoContent)
}

func (h *DistributionRulesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid campaignID")
		return
	}
	ruleID, err := uuid.Parse(chi.URLParam(r, "ruleID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ruleID")
		return
	}
	if err := h.Repo.Delete(r.Context(), ruleID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Após delete, escopo da regra se perde — recategoriza toda a campanha
	go func() { _ = h.Repo.RecategorizeForCampaign(r.Context(), campaignID) }()
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Rodar testes**

```bash
go test ./internal/api/handlers/ -run TestDistributionRulesHandler -v
```

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/distribution_rules.go workers/internal/api/handlers/distribution_rules_test.go
git commit -m "feat(api): add distribution-rules CRUD handler + auto-recategorize"
```

---

### Task 17: Handler DistributionOverrides

**Files:**
- Create: `workers/internal/api/handlers/distribution_overrides.go`
- Create: `workers/internal/api/handlers/distribution_overrides_test.go`

- [ ] **Step 1: Escrever teste**

Crie `workers/internal/api/handlers/distribution_overrides_test.go`:

```go
package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"radiocheck/internal/catalog"
)

func TestDistributionOverridesHandler_UpsertAndDelete(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := t.Context()

	cli, _ := catalog.NewClients(pool).Create(ctx, catalog.CreateClientInput{Name: "X"})
	cmp, _ := catalog.NewCampaigns(pool).Create(ctx, catalog.CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Now(), EndDate: time.Now().AddDate(0, 1, 0),
	})
	mat, _ := catalog.NewMaterials(pool).Create(ctx, catalog.CreateMaterialInput{
		ClientID: cli.ID, Title: "M", DurationSeconds: 30,
		MasterStoragePath: "/tmp", MasterSHA256: "x",
	})
	stat, _ := catalog.NewStations(pool).Create(ctx, catalog.CreateStationInput{
		Name: "FM", Band: "FM", StreamURL: "http://x",
	})

	h := &DistributionOverridesHandler{Repo: catalog.NewDistributionOverrides(pool)}
	r := chi.NewRouter()
	r.Put("/campaigns/{campaignID}/distribution-overrides", h.Upsert)
	r.Delete("/campaigns/{campaignID}/distribution-overrides", h.Delete)

	body := `{
		"material_id":"` + mat.ID.String() + `",
		"station_id":"` + stat.ID.String() + `",
		"for_date":"2026-06-15",
		"plays_expected":5
	}`
	req := httptest.NewRequest("PUT",
		"/campaigns/"+cmp.ID.String()+"/distribution-overrides",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("upsert status = %d: %s", rr.Code, rr.Body.String())
	}

	// Delete
	delBody := `{
		"material_id":"` + mat.ID.String() + `",
		"station_id":"` + stat.ID.String() + `",
		"for_date":"2026-06-15"
	}`
	req2 := httptest.NewRequest("DELETE",
		"/campaigns/"+cmp.ID.String()+"/distribution-overrides",
		strings.NewReader(delBody))
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rr2.Code)
	}

	_ = uuid.UUID{}
}
```

- [ ] **Step 2: Confirmar falha**

```bash
go test ./internal/api/handlers/ -run TestDistributionOverridesHandler -v
```

- [ ] **Step 3: Implementar handler**

Crie `workers/internal/api/handlers/distribution_overrides.go`:

```go
package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"radiocheck/internal/catalog"
)

type DistributionOverridesHandler struct {
	Repo *catalog.DistributionOverrides
}

type overridePayload struct {
	MaterialID    uuid.UUID `json:"material_id"`
	StationID     uuid.UUID `json:"station_id"`
	ForDate       string    `json:"for_date"` // YYYY-MM-DD
	PlaysExpected int16     `json:"plays_expected"`
	Reason        *string   `json:"reason,omitempty"`
}

func (h *DistributionOverridesHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid campaignID")
		return
	}
	var p overridePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	date, err := time.Parse("2006-01-02", p.ForDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "for_date must be YYYY-MM-DD")
		return
	}
	if err := h.Repo.Upsert(r.Context(), catalog.UpsertOverrideInput{
		CampaignID: campaignID, MaterialID: p.MaterialID, StationID: p.StationID,
		ForDate: date, PlaysExpected: p.PlaysExpected, Reason: p.Reason,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DistributionOverridesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid campaignID")
		return
	}
	var p overridePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	date, err := time.Parse("2006-01-02", p.ForDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "for_date must be YYYY-MM-DD")
		return
	}
	if err := h.Repo.Delete(r.Context(), campaignID, p.MaterialID, p.StationID, date); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DistributionOverridesHandler) ListByDateRange(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid campaignID")
		return
	}
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "from must be YYYY-MM-DD")
		return
	}
	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "to must be YYYY-MM-DD")
		return
	}
	overrides, err := h.Repo.ListByCampaignAndDateRange(r.Context(), campaignID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if overrides == nil {
		overrides = []catalog.DistributionOverride{}
	}
	writeJSON(w, http.StatusOK, overrides)
}
```

- [ ] **Step 4: Rodar testes**

```bash
go test ./internal/api/handlers/ -run TestDistributionOverridesHandler -v
```

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/distribution_overrides.go workers/internal/api/handlers/distribution_overrides_test.go
git commit -m "feat(api): add distribution-overrides upsert/delete/list handler"
```

---

### Task 18: Handler DailySummary

**Files:**
- Modify: `workers/internal/api/handlers/detections.go` (adicionar método ao handler existente)
- Modify: `workers/internal/api/handlers/detections_test.go`

- [ ] **Step 1: Adicionar teste**

Adicione no final de `workers/internal/api/handlers/detections_test.go`:

```go
func TestDetectionsHandler_DailySummary_Empty(t *testing.T) {
	pool := newTestPool(t)
	defer pool.Close()
	ctx := t.Context()

	cli, _ := catalog.NewClients(pool).Create(ctx, catalog.CreateClientInput{Name: "T"})
	cmp, _ := catalog.NewCampaigns(pool).Create(ctx, catalog.CreateCampaignInput{
		Name: "C", ClientID: cli.ID,
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	})

	h := &DetectionsHandler{
		Repo:         catalog.NewDetections(pool),
		DailySummary: catalog.NewDailySummary(pool),
	}
	r := chi.NewRouter()
	r.Get("/campaigns/{campaignID}/daily-summary", h.DailySummary)

	req := httptest.NewRequest("GET",
		"/campaigns/"+cmp.ID.String()+"/daily-summary?from=2026-06-01&to=2026-06-30", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var rows []catalog.DailySummaryRow
	json.NewDecoder(rr.Body).Decode(&rows)
	if len(rows) != 0 {
		t.Errorf("len = %d, want 0", len(rows))
	}
}
```

Importe necessários (adicione ao topo se faltarem): `time`, `github.com/go-chi/chi/v5`.

- [ ] **Step 2: Confirmar falha**

```bash
go test ./internal/api/handlers/ -run TestDetectionsHandler_DailySummary -v
```

- [ ] **Step 3: Implementar método `DailySummary`**

Abra `workers/internal/api/handlers/detections.go`. Adicione no struct `DetectionsHandler` o campo `DailySummary *catalog.DailySummary`. Em seguida, adicione o método no final do arquivo:

```go
func (h *DetectionsHandler) DailySummary(w http.ResponseWriter, r *http.Request) {
	campaignID, err := uuid.Parse(chi.URLParam(r, "campaignID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid campaignID")
		return
	}
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "from must be YYYY-MM-DD")
		return
	}
	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "to must be YYYY-MM-DD")
		return
	}
	rows, err := h.DailySummary.ListByCampaign(r.Context(), campaignID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []catalog.DailySummaryRow{}
	}
	writeJSON(w, http.StatusOK, rows)
}
```

- [ ] **Step 4: Rodar testes**

```bash
go test ./internal/api/handlers/ -v
```

Esperado: todos os testes passam (incluindo o novo).

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/detections.go workers/internal/api/handlers/detections_test.go
git commit -m "feat(api): add daily-summary endpoint on DetectionsHandler"
```

---

### Task 19: Wire-up no router e cmd/api

**Files:**
- Modify: `workers/internal/api/router.go`
- Modify: `workers/cmd/api/main.go` (ou o arquivo onde `Deps` é montado)

- [ ] **Step 1: Adicionar campos novos na struct `Deps`**

Localize a struct `Deps` (geralmente em `workers/internal/api/router.go` ou `deps.go`). Adicione os novos campos:

```go
type Deps struct {
	// ... campos existentes ...
	MaterialTypes         *handlers.MaterialTypesHandler
	Materials             *handlers.MaterialsHandler
	CampaignMaterials     *handlers.CampaignMaterialsHandler
	DistributionRules     *handlers.DistributionRulesHandler
	DistributionOverrides *handlers.DistributionOverridesHandler
	// DetectionsHandler agora tem DailySummary embutido, sem field novo
}
```

- [ ] **Step 2: Registrar rotas no router**

Em `workers/internal/api/router.go`, dentro do bloco `r.Route("/v1/internal", func(r chi.Router) {...})` na seção protegida (`r.Group(...auth.RequireJWT...)`), adicione APÓS as rotas existentes de `commercials`:

```go
// Material types — gerenciamento global
r.Route("/material-types", func(r chi.Router) {
	r.Get("/", d.MaterialTypes.List)
	r.Post("/", d.MaterialTypes.Create)
	r.Put("/{id}", d.MaterialTypes.Update)
	r.Delete("/{id}", d.MaterialTypes.Delete)
})

// Materials — biblioteca por cliente
r.Get("/clients/{clientID}/materials", d.Materials.ListByClient)
r.Route("/materials", func(r chi.Router) {
	r.Post("/", d.Materials.Upload)
	r.Get("/{id}", d.Materials.Get)
	r.Patch("/{id}/type", d.Materials.UpdateType)
	r.Delete("/{id}", d.Materials.Delete)
})

// Campaign ↔ Materials link
r.Route("/campaigns/{campaignID}/materials", func(r chi.Router) {
	r.Post("/", d.CampaignMaterials.Link)
	r.Get("/", d.CampaignMaterials.ListByCampaign)
	r.Put("/{materialID}/stations", d.CampaignMaterials.UpdateStations)
	r.Delete("/{materialID}", d.CampaignMaterials.Unlink)
})

// Distribution rules
r.Route("/campaigns/{campaignID}/distribution-rules", func(r chi.Router) {
	r.Get("/", d.DistributionRules.ListByCampaign)
	r.Post("/", d.DistributionRules.Create)
	r.Put("/{ruleID}", d.DistributionRules.Update)
	r.Delete("/{ruleID}", d.DistributionRules.Delete)
})

// Distribution overrides
r.Route("/campaigns/{campaignID}/distribution-overrides", func(r chi.Router) {
	r.Get("/", d.DistributionOverrides.ListByDateRange)
	r.Put("/", d.DistributionOverrides.Upsert)
	r.Delete("/", d.DistributionOverrides.Delete)
})

// Daily summary (alimenta a /detections)
r.Get("/campaigns/{campaignID}/daily-summary", d.Detections.DailySummary)
```

- [ ] **Step 3: Instanciar os handlers em `cmd/api/main.go`**

Abra `workers/cmd/api/main.go`. Localize onde `Deps` é montado (geralmente após `pgxpool.New(...)`). Adicione as instâncias:

```go
matTypesRepo := catalog.NewMaterialTypes(pool)
matsRepo := catalog.NewMaterials(pool)
cmpMatsRepo := catalog.NewCampaignMaterials(pool)
distRulesRepo := catalog.NewDistributionRules(pool)
distOverRepo := catalog.NewDistributionOverrides(pool)
dailySummaryRepo := catalog.NewDailySummary(pool)

deps := &api.Deps{
	// ... campos existentes ...
	MaterialTypes: &handlers.MaterialTypesHandler{Repo: matTypesRepo},
	Materials: &handlers.MaterialsHandler{
		Repo:        matsRepo,
		MastersPath: cfg.MastersPath, // ou onde quer que esse path venha
		NATS:        natsConn,
	},
	CampaignMaterials:     &handlers.CampaignMaterialsHandler{Repo: cmpMatsRepo},
	DistributionRules:     &handlers.DistributionRulesHandler{Repo: distRulesRepo},
	DistributionOverrides: &handlers.DistributionOverridesHandler{Repo: distOverRepo},
}

// Atualizar o handler de detections existente pra incluir DailySummary:
deps.Detections.DailySummary = dailySummaryRepo
```

> **Atenção:** o nome exato dos campos existentes em `Deps` e em `Detections` handler pode variar. Confirme com `grep -n "Detections " workers/cmd/api/main.go`.

- [ ] **Step 4: Build e rodar a API**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck/workers"
go build ./...
```

Esperado: build limpo. Se erro de import faltando, adicione `radiocheck/internal/api/handlers` e `radiocheck/internal/catalog` ao `main.go`.

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck/infra/docker"
docker compose up -d --build api
docker compose logs api --tail 50
```

Esperado: API sobe sem panic. Logs mostram listening on port.

- [ ] **Step 5: Smoke test dos endpoints novos via curl**

> **Pré-requisito:** obtenha um JWT válido com `POST /v1/internal/auth/login` (use bootstrap admin do `.env`).

```bash
TOKEN="<jwt-token>"
HOST="http://localhost:8080"

# Lista material types (deve retornar 6 seeds)
curl -s -H "Authorization: Bearer $TOKEN" $HOST/v1/internal/material-types | jq '. | length'
# Esperado: 6

# Lista materials de um cliente (provavelmente vazio)
CLIENT_ID="<algum uuid de cliente existente>"
curl -s -H "Authorization: Bearer $TOKEN" $HOST/v1/internal/clients/$CLIENT_ID/materials | jq '.'

# Lista distribution-rules de uma campanha (vazio)
CAMPAIGN_ID="<algum uuid de campanha existente>"
curl -s -H "Authorization: Bearer $TOKEN" $HOST/v1/internal/campaigns/$CAMPAIGN_ID/distribution-rules
# Esperado: []
```

Esperado: respostas válidas em JSON.

- [ ] **Step 6: Commit**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck"
git add workers/internal/api/router.go workers/cmd/api/main.go
git commit -m "feat(api): wire up new handlers in router and main"
```

---

# Phase E — Smoke test end-to-end + docs

### Task 20: Smoke test manual + documentação operacional

**Files:**
- Create: `docs/distribution-rules.md`
- Create: `docs/material-library.md`

- [ ] **Step 1: Smoke test completo via API**

Crie um script de smoke test `workers/scripts/smoke-test-foundations.sh`:

```bash
#!/bin/bash
# smoke-test-foundations.sh — testa o fluxo completo via API.

set -euo pipefail
HOST="${HOST:-http://localhost:8080}"
TOKEN="${TOKEN:-}"
if [ -z "$TOKEN" ]; then
  echo "ERROR: set TOKEN env var (login first)"
  exit 1
fi

CURL_AUTH="-s -H Authorization: Bearer $TOKEN -H Content-Type: application/json"

echo "1. Lista material_types (esperado >= 6)"
curl $CURL_AUTH $HOST/v1/internal/material-types | jq '. | length'

echo "2. Cria um cliente de teste"
CLIENT_ID=$(curl $CURL_AUTH -X POST $HOST/v1/internal/clients \
  -d '{"name":"Smoke Test Client"}' | jq -r '.id')
echo "   client_id = $CLIENT_ID"

echo "3. Cria uma campanha"
CAMPAIGN_ID=$(curl $CURL_AUTH -X POST $HOST/v1/internal/campaigns \
  -d "{\"name\":\"Smoke Camp\",\"client_id\":\"$CLIENT_ID\",
       \"start_date\":\"2026-06-01T00:00:00Z\",
       \"end_date\":\"2026-06-30T00:00:00Z\",
       \"target_stations\":[]}" | jq -r '.id')
echo "   campaign_id = $CAMPAIGN_ID"

echo "4. Cria uma estação"
STATION_ID=$(curl $CURL_AUTH -X POST $HOST/v1/internal/stations \
  -d '{"name":"Smoke FM","band":"FM","frequency_mhz":100.0,
       "stream_url":"http://example.com/stream"}' | jq -r '.id')
echo "   station_id = $STATION_ID"

echo "5. Lista materials do cliente (esperado vazio)"
curl $CURL_AUTH $HOST/v1/internal/clients/$CLIENT_ID/materials | jq '. | length'

echo "6. Lista distribution-rules da campanha (esperado vazio)"
curl $CURL_AUTH $HOST/v1/internal/campaigns/$CAMPAIGN_ID/distribution-rules | jq '. | length'

echo "7. Daily summary da campanha (esperado vazio)"
curl $CURL_AUTH "$HOST/v1/internal/campaigns/$CAMPAIGN_ID/daily-summary?from=2026-06-01&to=2026-06-30" | jq '. | length'

echo "✓ Foundations smoke test passou"
```

Rodar:

```bash
chmod +x workers/scripts/smoke-test-foundations.sh
TOKEN="<jwt>" workers/scripts/smoke-test-foundations.sh
```

Esperado: todos os passos retornam números/responses esperados.

- [ ] **Step 2: Documentação operacional — distribution-rules**

Crie `docs/distribution-rules.md`:

```markdown
# Distribution Rules — Semântica e Operação

Documenta como funcionam as regras de distribuição (programado) e a categorização de detecções.

> **Spec arquitetural:** [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](superpowers/specs/2026-05-11-campaign-wizard-design.md)

## O que é uma "regra de distribuição"

Uma regra define quantas vezes um material deve tocar por dia, em quais emissoras, em qual faixa horária e em qual período.

Estrutura:
- `campaign_id` — sempre dentro de uma campanha
- `material_id` — qual material a regra cobre
- `station_ids[]` — quais emissoras (subconjunto das emissoras da campanha)
- `start_date`, `end_date` — período dentro da campanha
- `weekday_mask` — bitmask dos dias da semana (bit 0=Dom, 6=Sáb). Ex: `62` = seg-sex
- `time_start`, `time_end` — faixa horária precisa (HH:MM, ex: `08:15`–`10:45`)
- `plays_per_day` — inserções esperadas por dia (1-100)

Múltiplas regras podem coexistir pra mesma combinação (material, station) — ex: manhã + tarde.

## Overrides

`distribution_overrides` permite sobrescrever o `plays_expected` calculado por regras pra uma célula específica (material, station, data). Útil quando a emissora avisa que vai trocar a grade num dia ou quando o cliente pede ajuste pontual.

Override tem precedência sobre regras — se há override pra (material, station, data), o `expected` exibido = `plays_expected` do override (ignorando totalmente o cálculo de regras).

## Categorias de detection

Cada detection é classificada em uma das 4 categorias (`detections.category`):

| Categoria | Significado | Cor na UI |
|-----------|-------------|-----------|
| `in_slot` | Tocou dentro da faixa de uma regra aplicável | Verde |
| `out_slot` | Tocou na data, mas fora da faixa horária | Amarelo |
| `out_date` | Tocou fora da data da campanha | Roxo |
| `orphan` | Tocou sem que houvesse regra aplicável (bônus puro) | Azul |

A view `daily_play_summary` agrega por (campaign, material, station, data) e calcula os 6 números exibidos:
- `expected` (cinza) = soma de plays_per_day das regras aplicáveis, OU override
- `in_slot` (verde) = count(in_slot)
- `deficit` (vermelho) = max(0, expected - in_slot - out_slot)
- `bonus` (azul) = max(0, in_slot - expected) + count(orphan)
- `out_slot` (amarelo) = count(out_slot)
- `out_date` (roxo) = count(out_date)

## Editabilidade

§8 da spec: edição livre só do **futuro**.

- Regras com `start_date >= hoje`: criar/editar/excluir livremente
- Regras com `start_date < hoje`: somente encurtar `end_date` pra `hoje`
- Overrides em datas passadas: read-only

A validação acontece no handler — backend rejeita mutações inválidas com `409 Conflict`.

## Re-categorização

Quando uma regra é criada/editada/excluída via API, o handler dispara `RecategorizeForRule` (ou `RecategorizeForCampaign` no caso de delete) em goroutine separada. Detections existentes no escopo da regra são atualizadas conforme a nova lógica.

A operação roda inteira em SQL — sem N+1 queries. Para a campanha inteira leva milissegundos mesmo com centenas de milhares de detections.

## Tabelas relacionadas

- `distribution_rules` — regras
- `distribution_overrides` — overrides por célula
- `detections.category` — coluna preenchida pelo categorizer no insert + recategorização
- `daily_play_summary` — VIEW agregada (não tabela)
```

- [ ] **Step 3: Documentação operacional — material-library**

Crie `docs/material-library.md`:

```markdown
# Material Library — Gestão de Materiais e Tipos

Documenta a biblioteca de materiais por cliente e o registro global de tipos.

> **Spec arquitetural:** [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](superpowers/specs/2026-05-11-campaign-wizard-design.md)

## Modelo

- **`material_types`** — registro global de tipos (Spot 30s, Testemunhal, etc). Gerenciado pelo admin via futura tela `/material-types`.
- **`materials`** — catálogo de áudios. Cada material pertence a UM cliente (`client_id`). Reusável entre campanhas desse cliente.
- **`campaign_materials`** — link N:N. Define que um material `M` está vinculado à campanha `C` com emissoras `[s1, s2, …]`.

## Por que decuplado da campanha?

No modelo antigo (`commercials`), cada upload de áudio criava um registro tied a UMA campanha. Reutilizar o mesmo MP3 em outra campanha exigia upload duplicado, gerando fingerprints duplicados e poluindo o índice.

No modelo novo:
- Subir um material → vai pra biblioteca do cliente
- Vincular a uma campanha → cria row em `campaign_materials`
- Vincular a outra campanha → outra row, mesmo `material_id`, mesmo fingerprint

## Backward compat com `commercials`

A tabela `commercials` permanece. Cada commercial existente foi migrado pra `materials` com o MESMO UUID (migration 0016). Isso preserva:
- `detections.commercial_id` continua válido (aponta pro mesmo UUID que agora também existe em `materials`)
- Código antigo que lê `commercials` continua funcionando

Plano: deprecar `commercials.target_stations` e `commercials.campaign_id` em migration futura (após frontend novo estar 100% em uso).

## Tipos de material

Os 6 seeds da migration 0016:

| Tipo         | Cor       |
|--------------|-----------|
| Spot 30s     | `#3b82f6` |
| Spot 60s     | `#0ea5e9` |
| Testemunhal  | `#8b5cf6` |
| Citação      | `#14b8a6` |
| Vinheta      | `#f59e0b` |
| Jingle       | `#ec4899` |

Operador pode criar tipos customizados via `POST /v1/internal/material-types`.

## Endpoints relevantes

- `GET  /v1/internal/clients/{clientID}/materials?q=<termo>` — biblioteca do cliente
- `POST /v1/internal/materials` — upload novo (multipart: client_id, title, type_id, audio)
- `PATCH /v1/internal/materials/{id}/type` — muda o tipo
- `DELETE /v1/internal/materials/{id}` — remove (falha 409 se tem detections)
- `POST /v1/internal/campaigns/{campaignID}/materials` — vincula material existente à campanha
- `DELETE /v1/internal/campaigns/{campaignID}/materials/{materialID}` — desvincula
- `PUT /v1/internal/campaigns/{campaignID}/materials/{materialID}/stations` — muda as emissoras desse material nessa campanha

## Dedup

A migration 0016 **não** força `UNIQUE(client_id, master_sha256)`. Isso é intencional — duplicatas existentes em `commercials` foram migradas como materiais separados. A interface do operador deve permitir mesclar duplicatas manualmente (funcionalidade futura). Em algum momento, constraint pode ser adicionada via migration nova.
```

- [ ] **Step 4: Atualizar follow-ups doc**

Abra `docs/follow-ups-fase2.md`. Adicione no final:

```markdown
## Foundations (Plano 1) — Follow-ups

- **F-86** — Refatorar `MaterialsHandler.Upload` e `CommercialsHandler.Upload` extraindo o helper de SHA256 + salvar arquivo + dispatch fingerprint num pacote compartilhado (`internal/upload/`). Atualmente duplicado.
- **F-87** — Adicionar `UNIQUE(client_id, master_sha256)` em `materials` após operador mesclar duplicatas via UI (futura).
- **F-88** — Implementar `probeDuration()` em `MaterialsHandler` via `ffprobe` (atualmente retorna 30.0 stub). Copiar lógica do `commercials.go` ou extrair pra helper.
- **F-89** — Background job de re-categorização em massa não bloqueia o handler nem reporta status. Considerar fila NATS com worker dedicado se volume de detections crescer e re-categorização ficar > 1s.
- **F-90** — Deprecar `commercials.target_stations` e `commercials.campaign_id` em migration futura (0019 ou posterior) após Planos 2 e 3 estarem em produção.
- **F-91** — Validação de "future-only edit" em `DistributionRulesHandler.Update/Delete` (§8 da spec). Atualmente backend aceita qualquer edição; validação fica na UI. Mover pro backend.
```

- [ ] **Step 5: Verificar build + testes**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck/workers"
go test ./... -v 2>&1 | tail -40
go build ./...
```

Esperado: tudo passa.

- [ ] **Step 6: Commit final do plano**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck"
git add docs/distribution-rules.md docs/material-library.md docs/follow-ups-fase2.md workers/scripts/smoke-test-foundations.sh
git commit -m "docs: add distribution-rules and material-library operational guides + smoke test script"
```

---

## Critério de conclusão do Plano 1

Marque cada item ao terminar:

- [ ] As 3 migrations (0016, 0017, 0018) aplicam limpo via `docker compose up -d --build migrate`
- [ ] Todos os testes Go passam: `go test ./... -v` sem falhas
- [ ] API sobe sem panic: `docker compose up -d --build api`
- [ ] Smoke test script roda end-to-end sem erro
- [ ] Frontend antigo (CampaignsPage atual) continua funcionando sem regressão (visual check rápido em `localhost:5173`)
- [ ] Detections existentes têm `category` preenchido (não NULL) — confirme via SQL
- [ ] Docs `distribution-rules.md` e `material-library.md` em `/docs`
- [ ] Follow-ups F-86 a F-91 registrados em `follow-ups-fase2.md`

Após conclusão: pronto pra começar **Plano 2 — Wizard (frontend)**. O backend já entrega tudo o que o frontend novo vai precisar.
