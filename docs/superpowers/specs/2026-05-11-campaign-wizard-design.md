# Wizard de Campanha — Design

**Data:** 2026-05-11
**Status:** Aprovado para implementação
**Plano de referência:** [plano_implementacao.md](../../../plano_implementacao.md) §18.2 (Fase 2 — Hardening)

---

## 1. Objetivo

Substituir o fluxo atual de cadastro de campanha (modal único com nome + cliente + datas + lista de emissoras + materiais soltos) por um **wizard de 4 etapas** que produz dados de **distribuição programada**. Esses dados alimentam a `/detections` permitindo classificar cada detecção em 6 categorias visuais (programado, tocou, faltou, bônus, fora da faixa, fora da data) — algo impossível hoje porque o sistema não conhece o "plano esperado".

Funcionalmente, isso transforma o Radiocheck de **"detector reativo"** em **"verificador de plano de mídia"**, alinhado ao objetivo de substituir o fornecedor externo (§1.1).

### Escopo desta entrega

1. Wizard de 4 etapas em rota dedicada (`/campaigns/new`, `/campaigns/:id/edit`)
2. Biblioteca de materiais por cliente (decuplada de campanha)
3. Modelo de regras de distribuição com override por dia
4. Categorização de detecções nos 6 estados acima
5. Refatoração da `/detections` para usar a mesma grade da etapa 4 do wizard

### Fora de escopo

- Mudanças no algoritmo de fingerprinting (§7-§10 do plano)
- Geração ou exibição de relatório PDF para cliente final
- Integração com sistema de tráfego ou DSP
- Suporte a campanhas multi-cliente
- Tipos de material que mudam regras de matching (tipos são puramente organizacionais)

---

## 2. Decisões de fluxo (brainstorm)

Decisões travadas durante o brainstorm em 2026-05-11:

| # | Pergunta | Decisão |
|---|----------|---------|
| Q1 | Escopo da biblioteca de materiais | **Por cliente** (escopo no `client_id`) |
| Q2 | Tipos de material são regra ou etiqueta | **Etiqueta** (organizacional); cadastrados em tela separada |
| Q3 | Modelo de distribuição | **Híbrido (D)**: regra base + override por dia. Faixa horária precisa (HH:MM, não daypart) |
| Q4 | Matemática de categorização | Caso 3 = A (verde caps + azul overflow); Caso 4 = B (amarelo abate vermelho). Vermelho é saldo devedor em tempo real |
| Q5 | Editabilidade pós-início | **Future-only**: passado é read-only |
| layout | Estrutura visual | Stepper horizontal + strip de resumo + main full-width + footer (sem sidebar persistente) |

---

## 3. Arquitetura geral

### 3.1 Container

- Rota dedicada `/campaigns/new` para criação; `/campaigns/:id/edit` para edição.
- O modal atual `NewCampaignModal` em [`CampaignsPage.jsx`](../../../frontend/src/pages/CampaignsPage.jsx) é removido. O botão "+ Nova campanha" passa a navegar pra rota.
- Lifecycle (§18.2.1) intacto: a campanha continua entrando em `programada → ativa → concluida` automaticamente; o wizard apenas configura o plano.

### 3.2 Layout do wizard

```
┌───────────────────────────────────────────────────┐
│  ① Dados  ✓  ② Emissoras  ✓  ③ Materiais  ④ Distrib │  ← Stepper horizontal
│  ───────────────────────●────────────              │  ← Progress bar contínua
├───────────────────────────────────────────────────┤
│  Verão 2026 · McDonald's · 01/06–30/06 · 12 emis... │  ← Strip de resumo
├───────────────────────────────────────────────────┤
│                                                   │
│         [Conteúdo da etapa atual]                 │
│         max-width: 640px (etapas 1,2,3)           │
│         full-width (etapa 4)                      │
│                                                   │
├───────────────────────────────────────────────────┤
│  ← Voltar                          Avançar →      │
└───────────────────────────────────────────────────┘
```

Comportamento:
- Stepper indica etapas concluídas (✓ verde), atual (rosa, número), futuras (cinza)
- Progress bar contínua na base do stepper preenche proporcional ao % completo
- Strip de resumo é compacta, 1 linha, atualizada conforme o usuário preenche
- Navegação backward: livre (clica no step anterior)
- Navegação forward: bloqueada até validação do step atual
- Footer fixo com Voltar (ghost) + Avançar (primary). No step 4, "Avançar" vira "Concluir campanha"
- Esc → confirma se quer descartar (custom modal); ou cancela direto se nada foi alterado

### 3.3 Componente compartilhado

`<DistributionGrid />` é o componente da grade emissora × material × dia. Reutilizado em 2 contextos:

| Contexto | Mode | Comportamento |
|----------|------|---------------|
| Etapa 4 do wizard | `edit` | Clique em célula → popover de override. Header tem "+ Regra". Só badges cinza/laranja |
| `/detections` | `view` | Clique em célula → modal de detalhes do dia (mantém `DayDetailModal` atual). Badges em todas as 6 cores |

Mesma estrutura HTML/CSS/colors. Diferença é só nos handlers e nos badges renderizados.

---

## 4. Detalhes por etapa

### 4.1 Etapa 1 — Dados básicos

Form simples, `max-width: 640px`, centrado.

Campos:
- **Nome** (text, required)
- **Cliente** (RSelect, required)
- **Início** (date, required)
- **Fim** (date, required, ≥ início)

Sem mudanças funcionais relativas ao modal atual. Apenas refinamento visual seguindo `pro-system-ui` (focus ring rosa, validação on-blur, error inline 12-13px cinza-500).

Validação para "Avançar": todos campos preenchidos e válidos.

### 4.2 Etapa 2 — Emissoras

Padrão **Entity-list** (PATTERNS.md §1.2).

```
┌─────────────────────────────────────────────────┐
│  🔍 Buscar emissora...   [Banda ▾] [Estado ▾]  │  ← Toolbar
│  Filtros ativos: AM × | SC ×                    │  ← Chips removíveis
├─────────────────────────────────────────────────┤
│  Selecionadas (5): [chips com X]                │  ← Stack de selecionadas
├─────────────────────────────────────────────────┤
│  ☑ Menina FM 97.5  · Blumenau/SC  ⚪ ativa      │
│  ☐ Atlântida 94.3  · POA/RS       ⚪ ativa      │
│  ☐ Itapema 93.7    · Floripa/SC   ⚪ ativa      │
│  ...                                            │
│  [▾ Carregar mais]                              │
└─────────────────────────────────────────────────┘
```

Componentes:
- **Search**: debounced 400ms, usa endpoint existente `useStations({ q })`
- **Filtros**: dropdown popovers com banda (AM/FM), estado (UF select), monitoring_status
- **Filtros aplicados**: chips removíveis abaixo da toolbar
- **Stack de selecionadas**: chips no topo com X pra remover individualmente. "Limpar todas" ao lado
- **Lista**: itens com checkbox + avatar + nome + dial + cidade + monitoring_status. Hover translateY(-1px)
- **Densidade toggle**: grid (cards 240px) ou list (rows). Padrão = list (mais denso)
- **Carregar mais**: paginação. Não infinite scroll (precisa controle deliberado em UI densa)
- **Bulk action**: "Adicionar todas com filtro atual" no menu de overflow do toolbar

Validação para "Avançar": ≥ 1 emissora selecionada.

API:
- GET `/v1/internal/stations` (existente) com `q`, `band`, `state`, `monitoring_status`, `limit`, `offset`
- PATCH `/v1/internal/campaigns/:id/stations` (existente) — salva ao avançar

### 4.3 Etapa 3 — Materiais

Padrão híbrido: biblioteca + uploader, com cards de campaign_materials abaixo.

```
┌─────────────────────────────────────────────────┐
│              + Adicionar material               │  ← CTA primary
├─────────────────────────────────────────────────┤
│  📁 Materiais nesta campanha (3)                │
│                                                 │
│  ┌─────────────────────────────────────────┐    │
│  │ 🎵 Spot 30s — Verão                      │    │
│  │   Spot ▾  · 30.0s · ✓ pronto             │    │
│  │   Emissoras: Menina, Atlântida, +1       │    │
│  │   [▶ Ouvir] [⬇ Baixar] [🗑]              │    │
│  └─────────────────────────────────────────┘    │
│  ... outros cards ...                           │
└─────────────────────────────────────────────────┘
```

**Botão "Adicionar material"** abre **side panel** (slide da direita, 480px) com 2 abas:

**Aba "Da biblioteca"**:
- Lista buscável dos `materials` onde `client_id = campanha.client_id`
- Mostra: título, tipo (pill colorido), duração, fingerprint status
- Multi-check pra adicionar à campanha
- Empty state se cliente não tem material ainda — ghost preview com CTA "Subir o primeiro"

**Aba "Subir novo"**:
- Drag-drop zone (semelhante ao atual `BulkUploadZone`, mas com upload sequencial + form claro)
- Pra cada arquivo: título, tipo (dropdown sourced de `material_types`), botão remover
- Após upload, arquivo vai pra biblioteca do cliente **e** vincula à campanha automaticamente

**Cards de campaign_materials**:
- Tipo: dropdown inline editável (muda `materials.type_id`, afeta todas as campanhas — confirma com modal se material está em outra campanha ativa)
- Duração e fingerprint_status: read-only
- Emissoras atribuídas: chips com popover similar ao `StationPickerDropdown` atual. Default ao adicionar = todas as emissoras da campanha. Pode reduzir.
- Ações: ▶ Ouvir, ⬇ Baixar, 🗑 (desvincula da campanha — não exclui material)

Empty state global da etapa 3 quando campanha não tem nenhum material: ghost preview com 2-3 cards fantasma + CTA centralizado "Adicione seu primeiro material".

Validação para "Avançar": ≥ 1 material vinculado E todo material tem ≥ 1 emissora atribuída.

API (novos endpoints):
- GET `/v1/internal/clients/:id/materials?q=` — biblioteca do cliente
- POST `/v1/internal/materials` — upload novo (multipart com title, type_id, audio)
- POST `/v1/internal/campaigns/:id/materials` — vincula material(is) existente(s)
- DELETE `/v1/internal/campaigns/:id/materials/:material_id` — desvincula
- PATCH `/v1/internal/campaign-materials/:campaign_id/:material_id/stations` — atualiza target_stations

### 4.4 Etapa 4 — Distribuição

A grade emissora-material × dia, com regras como side panel e override como popover.

#### Layout

```
┌──────────────────────────────────────────────────────────┐
│  Distribua os materiais        [⚙ Filtrar] [📅 Junho ▸] [+ Regra] │
├──────────────────────────────────────────────────────────┤
│  Filtros ativos: Spot ×  SC ×                            │
├──────────────────────────────────────────────────────────┤
│  Material / Regra │ Seg 01 │ Ter 02 │ ... │ Seg 30      │
├──────────────────────────────────────────────────────────┤
│  ▼ Menina FM 97.5 · Blumenau/SC      3 materiais · 7/dia  │
│    🔵 Spot 30s — Verão                                    │
│       3×/dia 08:15-10:45                                  │
│       +1 regra (manhã+tarde)        │ [3] │ [3] │ ... │   │
│    🟣 Testem. — Lanche Feliz                              │
│       2×/dia 17:00-18:30            │ [2] │ [2] │ ... │   │
├──────────────────────────────────────────────────────────┤
│  198 inserções programadas no mês · 3 regras · 1 override │
├──────────────────────────────────────────────────────────┤
│  ← Voltar                       Concluir campanha →      │
└──────────────────────────────────────────────────────────┘
```

#### Toolbar

- **Filtrar**: popover com filtros por tipo de material, emissora, estado
- **Navegação por mês**: ◂ Junho 2026 ▸. Para campanhas curtas (≤ 14 dias), exibe range completo. Para campanhas longas, navega mês a mês.
- **+ Regra**: abre side panel de regra (vazio)

#### Grade

Estrutura grid CSS:
```css
grid-template-columns: 220px 130px repeat(N, 64px);
```
Onde N = dias no mês visível.

**Linhas**:
- **Station header row** (full-width): avatar + nome + dial + cidade + stats no canto direito
- **Material sub-rows**: type-icon-pill (cor por tipo) + título do material + texto da regra (compacto)

Stations sem material vinculado **não aparecem** (linha vazia é poluição). Materiais sem regra ativa aparecem mas sem badges (vazio = "não definido").

**Coluna "Regra"** (130px):
- Texto compacto: `3×/dia 08:15–10:45` + dias da semana com glyph (`seg-sex`)
- Se há ≥ 2 regras na mesma (material, station): mostra primeira + chip `+1 regra` que abre tooltip listando todas
- Click no texto da regra → abre side panel pra editar essa regra específica

**Células de dia** (64px):
- Fundo branco normal
- Fundo `#f8fafc` se sábado/domingo
- Fundo listrado se dia fora do range da campanha
- Fundo `#fef9c3` se há override
- Badge cinza com valor `E` (esperado computado)
- Hover: borda interna cinza, cursor pointer
- Click: abre popover de override

#### Side panel "+ Regra"

460px, slide da direita, backdrop dim mas grade fica visível.

Campos:
- **Material** (chips multi-select, opções = materiais da campanha)
- **Emissoras** (chips multi-select, opções = emissoras da campanha; só aparecem as que têm o material atribuído)
- **Inserções por dia** (number input, 1-50)
- **Dias da semana** (toggle chips: S T Q Q S S D)
- **Faixa horária — início** (time input HH:MM)
- **Faixa horária — fim** (time input HH:MM)
- **Início do período** (date input, ≥ campanha.start_date)
- **Fim do período** (date input, ≤ campanha.end_date)

**Preview ao vivo** no rodapé do panel: "Vai gerar **N inserções** em **M dias úteis** × **K emissoras**". Atualiza conforme campos mudam.

Footer do panel:
- Modo create: Cancelar + Adicionar regra
- Modo edit: Excluir regra (vermelho ghost, esquerda) + Cancelar + Salvar

Side panel sai com animação `translateX 320ms cubic-bezier(0.16,1,0.3,1)`. Backdrop fade-in 200ms.

#### Popover de override

Aparece acima da célula clicada (240px). Não usa backdrop — outside-click fecha.

Conteúdo:
- Header: `Menina FM · Spot 30s · Qua 10/06`
- Linha "Regra atual: 5×/dia" (cinza, não editável)
- Input numérico com stepper +/− pra valor de override (laranja)
- Botões: "Voltar à regra" (remove override) + "Aplicar"

#### Footer da grade

Stats:
- Total de inserções programadas no período visível
- Regras ativas
- Overrides ativos
- Se há regra mas alguma combinação material-station sem cobertura: alerta amarelo

#### Validação para "Concluir"

- **Permite** concluir com zero regras (caso operador queira monitorar só bônus puro). Mostra confirmação: "Nenhuma regra de distribuição definida. Detecções serão classificadas como bônus. Continuar?"
- Conclui campanha → POST consolida tudo, navega pra `/campaigns`, abre highlight da campanha criada

API:
- POST/PATCH/DELETE `/v1/internal/campaigns/:id/distribution-rules` — CRUD de regras
- POST/PATCH/DELETE `/v1/internal/campaigns/:id/distribution-overrides` — CRUD de overrides
- GET `/v1/internal/campaigns/:id/distribution-summary?month=YYYY-MM` — agregado por (material, station, date) pra alimentar a grade

---

## 5. Modelo de dados

### 5.1 Migration 0016 — Biblioteca de materiais

```sql
-- 0016_material_library.up.sql

BEGIN;

-- Tipos de material — registro global gerenciado em /material-types
CREATE TABLE material_types (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL UNIQUE,
    color TEXT NOT NULL DEFAULT '#94a3b8',  -- hex pro type-icon-pill
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seeds básicos
INSERT INTO material_types (name, color) VALUES
    ('Spot 30s', '#3b82f6'),
    ('Spot 60s', '#0ea5e9'),
    ('Testemunhal', '#8b5cf6'),
    ('Citação', '#14b8a6'),
    ('Vinheta', '#f59e0b'),
    ('Jingle', '#ec4899');

-- Materiais (antiga commercials, decuplada de campanha)
CREATE TABLE materials (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    short_id SERIAL UNIQUE,
    client_id UUID NOT NULL REFERENCES clients(id) ON DELETE RESTRICT,
    title TEXT NOT NULL,
    type_id UUID REFERENCES material_types(id),
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
    -- Commercials existentes podem ter duplicatas (mesmo MP3 subido em campanhas
    -- diferentes). Migrar 1:1 preserva referências de detections.commercial_id.
    -- Operador pode mesclar duplicatas via tela de gestão da biblioteca;
    -- constraint pode ser adicionada em migration futura após limpeza.
);
CREATE INDEX idx_materials_client ON materials(client_id);
CREATE INDEX idx_materials_type ON materials(type_id);
CREATE INDEX idx_materials_status ON materials(fingerprint_status);

-- Link campaigns ↔ materials (N:N)
CREATE TABLE campaign_materials (
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    material_id UUID NOT NULL REFERENCES materials(id) ON DELETE RESTRICT,
    target_stations UUID[] NOT NULL DEFAULT '{}',  -- subconjunto de campaigns.target_stations
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (campaign_id, material_id)
);
CREATE INDEX idx_campaign_materials_material ON campaign_materials(material_id);

-- Trigger updated_at em materials
CREATE TRIGGER trg_materials_updated BEFORE UPDATE ON materials
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

-- ────── Migração de dados ──────
-- 1. Cada commercial vira material, com client_id derivado da campanha
INSERT INTO materials (
    id, short_id, client_id, title, type_id, duration_seconds,
    master_storage_path, master_sha256,
    fingerprint_status, fingerprint_generated_at, fingerprint_hash_count,
    metadata, created_at, updated_at
)
SELECT
    c.id, c.short_id,
    cmp.client_id,
    c.title,
    NULL,  -- type_id é null nos materiais migrados; operador classifica depois
    c.duration_seconds,
    c.master_storage_path, c.master_sha256,
    c.fingerprint_status, c.fingerprint_generated_at, c.fingerprint_hash_count,
    c.metadata, c.created_at, c.updated_at
FROM commercials c
JOIN campaigns cmp ON cmp.id = c.campaign_id;
-- Migração 1:1: cada commercial vira um material com mesmo UUID. Sem dedup
-- automático — duplicatas (mesmo sha256 em campanhas diferentes do mesmo
-- cliente) ficam como materiais separados. Operador pode mesclar manualmente
-- via UI da biblioteca. Preserva integridade referencial de detections.

-- 2. campaign_materials reconstrói os links
INSERT INTO campaign_materials (campaign_id, material_id, target_stations, added_at)
SELECT
    c.campaign_id,
    c.id,  -- material.id = commercial.id após migração
    c.target_stations,
    c.created_at
FROM commercials c
WHERE EXISTS (SELECT 1 FROM materials m WHERE m.id = c.id);

-- 3. detections continuam apontando pra commercials.id, que agora também
-- existe em materials.id. A coluna detections.commercial_id é mantida pra
-- backward-compat; podemos adicionar uma coluna material_id idêntica via
-- migration 0018 se quisermos.

-- 4. Drop futuro de commercials.target_stations e commercials.campaign_id em
-- migration separada após código novo estar em produção (estratégia
-- backward-compat).

COMMIT;
```

**Decisão sobre `commercials`**: a tabela permanece existindo provisoriamente como **view ou shadow** apontando pra `materials` + `campaign_materials`. Workers e detections continuam funcionando sem mudança de código imediata. Em migration futura (0019+), removemos a tabela `commercials` após código estar 100% migrado.

### 5.2 Migration 0017 — Plano de distribuição

```sql
-- 0017_distribution_plan.up.sql

BEGIN;

-- Regras de distribuição
CREATE TABLE distribution_rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    material_id UUID NOT NULL REFERENCES materials(id) ON DELETE CASCADE,
    station_ids UUID[] NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    -- weekday_mask: bit 0 = Domingo, 1 = Segunda, ..., 6 = Sábado.
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

-- Overrides por célula (material × station × data)
CREATE TABLE distribution_overrides (
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    material_id UUID NOT NULL REFERENCES materials(id) ON DELETE CASCADE,
    station_id UUID NOT NULL REFERENCES stations(id),
    for_date DATE NOT NULL,
    plays_expected SMALLINT NOT NULL CHECK (plays_expected >= 0),
    reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by UUID,  -- FK para users; null pra migrations
    PRIMARY KEY (campaign_id, material_id, station_id, for_date)
);
CREATE INDEX idx_distribution_overrides_date ON distribution_overrides(for_date);

COMMIT;
```

### 5.3 Migration 0018 — Categorização de detecções

```sql
-- 0018_detections_categorization.up.sql

BEGIN;

-- Adiciona categoria pré-computada em cada detection
ALTER TABLE detections ADD COLUMN category TEXT
    CHECK (category IN ('in_slot','out_slot','out_date','orphan'));

CREATE INDEX idx_detections_category ON detections(campaign_id, detected_at, category);

-- Backfill: detections existentes são classificadas como 'orphan' provisoriamente
-- (até regras serem definidas). Worker recalcula incremental ao criar/editar regra.
UPDATE detections SET category = 'orphan' WHERE category IS NULL;

ALTER TABLE detections ALTER COLUMN category SET NOT NULL;
ALTER TABLE detections ALTER COLUMN category SET DEFAULT 'orphan';

COMMIT;
```

### 5.4 View agregada (criada em 0018 também)

```sql
-- View que alimenta a /detections — agrega por (station, material, date)
-- com fórmulas das 6 categorias.
CREATE OR REPLACE VIEW daily_play_summary AS
WITH expected AS (
    -- Soma de plays_per_day das regras aplicáveis pra cada (campaign, material, station, date)
    SELECT
        r.campaign_id,
        r.material_id,
        s.station_id,
        d.for_date,
        SUM(r.plays_per_day) AS rule_expected
    FROM distribution_rules r
    CROSS JOIN LATERAL unnest(r.station_ids) s(station_id)
    CROSS JOIN LATERAL generate_series(r.start_date, r.end_date, INTERVAL '1 day') AS d(for_date)
    WHERE (1 << EXTRACT(DOW FROM d.for_date)::INT) & r.weekday_mask != 0
    GROUP BY r.campaign_id, r.material_id, s.station_id, d.for_date
),
expected_with_override AS (
    -- Override tem precedência sobre rule_expected
    SELECT
        COALESCE(o.campaign_id, e.campaign_id) AS campaign_id,
        COALESCE(o.material_id, e.material_id) AS material_id,
        COALESCE(o.station_id, e.station_id) AS station_id,
        COALESCE(o.for_date, e.for_date) AS for_date,
        COALESCE(o.plays_expected, e.rule_expected) AS expected
    FROM expected e
    FULL OUTER JOIN distribution_overrides o
        ON e.campaign_id = o.campaign_id
       AND e.material_id = o.material_id
       AND e.station_id = o.station_id
       AND e.for_date = o.for_date
),
actual AS (
    SELECT
        campaign_id,
        commercial_id AS material_id,
        station_id,
        date_trunc('day', detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS for_date,
        COUNT(*) FILTER (WHERE category = 'in_slot') AS in_slot,
        COUNT(*) FILTER (WHERE category = 'out_slot') AS out_slot,
        COUNT(*) FILTER (WHERE category = 'out_date') AS out_date,
        COUNT(*) FILTER (WHERE category = 'orphan') AS orphan
    FROM detections
    GROUP BY campaign_id, commercial_id, station_id, for_date
)
SELECT
    COALESCE(e.campaign_id, a.campaign_id) AS campaign_id,
    COALESCE(e.material_id, a.material_id) AS material_id,
    COALESCE(e.station_id, a.station_id) AS station_id,
    COALESCE(e.for_date, a.for_date) AS for_date,
    COALESCE(e.expected, 0) AS expected,            -- cinza
    COALESCE(a.in_slot, 0) AS in_slot,              -- verde
    GREATEST(0, COALESCE(e.expected, 0) - COALESCE(a.in_slot, 0) - COALESCE(a.out_slot, 0)) AS deficit,  -- vermelho
    GREATEST(0, COALESCE(a.in_slot, 0) - COALESCE(e.expected, 0)) + COALESCE(a.orphan, 0) AS bonus,     -- azul
    COALESCE(a.out_slot, 0) AS out_slot,            -- amarelo
    COALESCE(a.out_date, 0) AS out_date             -- roxo
FROM expected_with_override e
FULL OUTER JOIN actual a
    ON e.campaign_id = a.campaign_id
   AND e.material_id = a.material_id
   AND e.station_id = a.station_id
   AND e.for_date = a.for_date;
```

Se a view ficar lenta em produção (esperado com volume de detecções), promover pra **MATERIALIZED VIEW** com refresh incremental disparado pelo worker.

---

## 6. Lógica de categorização (worker)

### 6.1 Quando uma detection é confirmada

Worker já produz a detection com `campaign_id` resolvido pelo matching engine (NOT NULL no schema atual — uma detection sempre nasce vinculada a uma campanha porque o material que foi detectado está numa campanha). A categoria é computada e gravada em `detections.category`:

```pseudo
def categorize(detection):
    campaign = load_campaign(detection.campaign_id)  # sempre existe

    local_date = detection.detected_at.at_timezone('America/Sao_Paulo').date()
    local_time = detection.detected_at.at_timezone('America/Sao_Paulo').time()

    if local_date < campaign.start_date or local_date > campaign.end_date:
        return 'out_date'  # detection chegou fora do range (lag entre worker e fim)

    rules = find_applicable_rules(
        campaign_id=campaign.id,
        material_id=detection.commercial_id,  # = material_id no novo modelo
        station_id=detection.station_id,
        date=local_date,
    )
    if not rules:
        return 'orphan'  # campanha existe mas não há regra cobrindo esse (material, station, dia)

    for rule in rules:
        if rule.time_start <= local_time <= rule.time_end:
            return 'in_slot'
    return 'out_slot'
```

**Caso material em múltiplas campanhas simultâneas** (mesmo cliente, mesma janela): a engine de matching escolhe UMA campanha por detection (estratégia existente; ver R20 do plano). Não é responsabilidade desta spec resolver ambiguidade — assume-se que o `campaign_id` que chega pra categorize() já é o "correto" segundo a heurística do matcher.

### 6.2 Quando uma regra é criada/editada/excluída

Reclassificar detections já existentes que possam ser afetadas. Estratégia:

1. Coletar `(material_id, station_ids, date_range)` antes e depois da mudança
2. Re-rodar `categorize()` em batch nos detections afetados
3. Operação assíncrona (job na fila); UI mostra "Recalculando..." se necessário

Restrição §18.2.1: regras com `start_date < hoje` não podem ser editadas — somente encurtar `end_date` pra `hoje` (efetivo "desligar"). Backend valida.

### 6.3 Quando uma campanha muda de status

- `programada → ativa`: nada (regras já estavam definidas)
- `ativa → concluida`: nada (detections que chegarem após `end_date` viram `out_date`)
- `* → cancelada`: stop monitoring; detections existentes mantém categoria

---

## 7. Refatoração da `/detections`

A `/detections` atual usa um componente `DetectionsCalendar` que renderiza um calendário mensal com hits por dia. Vai ser substituído pelo `<DistributionGrid mode="view">`.

Mudanças:
- Mesmo seletor de campanha no topo
- Mesmos filtros de mês
- Em vez do calendário atual, renderiza a grade emissora-material × dia
- Click em célula abre `DayDetailModal` atual (preservado)
- Cada célula mostra até 5 badges: cinza, verde, vermelho, azul, amarelo (roxo só aparece em dias fora do range, com fundo distinto)
- Buscador continua filtrando emissoras
- Header tem nova métrica: "Cobertura do plano" = `verde / esperado` no período

API: novo endpoint `GET /v1/internal/campaigns/:id/daily-summary?from=YYYY-MM-DD&to=YYYY-MM-DD` retornando a query da `daily_play_summary` filtrada.

---

## 8. Editabilidade pós-lançamento (Q5 = B)

### 8.1 O que pode mudar depois que a campanha está ativa

- **Adicionar/remover emissora** da campanha → ajusta `campaigns.target_stations`. Workers reconfigurados via reconciler ([worker-commercial-reconciler.md](../../worker-commercial-reconciler.md)).
- **Vincular novo material** → cria `campaign_materials` row. Worker carrega o material no índice.
- **Desvincular material** → remove `campaign_materials` row. Materiais permanecem na biblioteca.
- **Adicionar regra de distribuição** → apenas se `start_date >= hoje`. Sem efeito no passado.
- **Editar regra existente** → apenas se `start_date >= hoje`. Se a regra começou no passado, só permite encurtar `end_date` pra `hoje`.
- **Excluir regra** → apenas se `start_date >= hoje`. Senão, equivale a encurtar `end_date` pra ontem.
- **Override em data futura** → criar/editar/excluir livre.
- **Override em data passada** → read-only.

### 8.2 Cancelar campanha

Continua via `POST /v1/internal/campaigns/:id/cancel`. Mantém regras e overrides existentes (não apaga histórico). Workers param imediatamente.

---

## 9. Concorrência e consistência

### 9.1 Edição simultânea

Sem locks pessimistas. Estratégia de last-write-wins com `updated_at`. Para detection, o backend re-classifica em background; UI cliente faz polling/refetch.

### 9.2 Worker reload

Após mutação em `campaign_materials`, `distribution_rules` ou `distribution_overrides`, o reconciler ([docs/worker-commercial-reconciler.md](../../worker-commercial-reconciler.md)) detecta divergência no próximo ciclo (30s) e recarrega o worker. Para mudanças críticas (adicionar/remover material), API publica evento NATS pra disparo imediato.

---

## 10. Validação e testes

### 10.1 Unitários (backend)
- Função `categorize()` cobrindo todos os 4 casos com edge cases (TZ, weekday boundary, time exactly at boundary)
- Fórmula da view `daily_play_summary` com fixtures sintéticos cobrindo casos 1-6 da Q4
- Conversor `weekday_mask` ↔ array de dias da semana
- Validação de regras (data fim ≥ início, hora fim > início, plays > 0)

### 10.2 Integração
- Wizard end-to-end: cria campanha 4 passos, valida em DB todas as 3 migrations
- Edição: muda regra ativa (deve permitir só encurtar end_date), tenta editar override passado (deve falhar)
- Recategorização: cria detection ➜ adiciona regra que cobriria a data ➜ verifica que category mudou
- View `daily_play_summary` retorna valores corretos pra cenário de teste com regras + overrides + detections de várias categorias

### 10.3 Manual (UI)
- Wizard completo até concluir
- Empty state das etapas 2, 3, 4 (todas com ghost preview)
- Filtros + busca na etapa 2
- Side panel da etapa 3 (biblioteca + upload)
- Side panel da etapa 4 (criar/editar/excluir regra)
- Popover de override (aplicar + voltar à regra)
- Multi-regras na mesma (material, station)
- `/detections` carregando grade com badges em todas as 6 cores

---

## 11. Estimativa de impacto

| Área | Linhas afetadas / criadas |
|------|---------------------------|
| Migrations | 3 arquivos (~300 linhas) |
| Backend (Go) | ~1200 linhas (handlers, repos, worker categorizer, reconciler hooks) |
| Frontend (React) | ~3500 linhas (4 etapas + DistributionGrid + side panels + popover + nova /detections) |
| Testes | ~800 linhas (unit + integration) |
| Docs operacionais em `/docs` | 3 arquivos novos |

Duração estimada: **3-4 sprints de 1 semana** (1 desenvolvedor fullstack), sem contar revisão e ajustes de UX em campo.

---

## 12. Não-objetivos explícitos

Mesmo dentro de escopo desta entrega, **não** vamos implementar:

1. Edição em massa de células da grade (drag-select pra preencher rapidamente) — pode entrar em fase 2.5 se demandado
2. Export do plano de distribuição como PDF/Excel
3. Templates de campanha ("copia da última campanha similar")
4. Aprovação de cliente por link público
5. Comparação visual planejado-vs-realizado em formato de gráfico (continua sendo o calendário)
6. Notificação automática quando vermelho passa de threshold (alerta de campanha em risco)
7. Mudança no algoritmo de fingerprint ou no schema de `fingerprint_hashes`

Esses itens ficam para release futuro, opcionalmente como follow-ups F-XX no [follow-ups-fase2.md](../../follow-ups-fase2.md).

---

## 13. Documentação operacional a criar (em `/docs`, fora desta spec)

- `docs/campaign-wizard.md` — guia operacional do wizard
- `docs/material-library.md` — gestão da biblioteca de materiais e tipos
- `docs/distribution-rules.md` — semântica de regras, overrides, categorização e regras de editabilidade
- Atualizar `docs/follow-ups-fase2.md` se houver follow-ups específicos identificados durante implementação

---

## 14. Referências

- [plano_implementacao.md §18.2](../../../plano_implementacao.md#L1873) — Fase 2 Hardening
- [plano_implementacao.md §18.2.1](../../../plano_implementacao.md#L1893) — Lifecycle de campanha
- [design.md](../../../design.md) — Design system Signalads (cores, tipografia, componentes)
- `.claude/skills/pro-system-ui/references/PATTERNS.md` — Padrões de layout aplicados
- [docs/worker-commercial-reconciler.md](../../worker-commercial-reconciler.md) — Reconciler usado para sincronizar regras no worker
- [docs/migrations.md](../../migrations.md) — Runner automático de migrations
