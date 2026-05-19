---
status: planejado
ultima-verificacao: 2026-05-19
codigo-relacionado:
  - migrations/0017_distribution_plan.up.sql
  - migrations/0019_rules_by_type.up.sql
  - workers/internal/catalog/distribution_overrides.go
  - workers/internal/api/handlers/distribution_overrides.go
  - workers/internal/categorizer/categorizer.go
  - workers/internal/catalog/detections.go
  - frontend/src/components/OverridePopover.jsx
  - frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx
  - frontend/src/components/DistributionGrid.jsx
  - frontend/src/api/hooks.js
---

# Faixa horária em overrides de distribuição

## Contexto

A tela de edição de campanha (`/campaigns/:id/edit`, step 4 "Distribuição")
permite ao operador ajustar a distribuição esperada de veiculações por célula
`(emissora, tipo, dia)`. Hoje o ajuste manual via grid grava um `override` na
tabela `distribution_overrides` com APENAS `plays_expected` — sem faixa
horária.

Consequências do gap atual:

1. **Detection cai como `orphan`** quando uma célula tem override mas nenhuma
   rule — o categorizador (`workers/internal/categorizer/categorizer.go`) só
   olha rules, não overrides. Não há janela de tempo conhecida → não há como
   classificar como `in_slot`.
2. **Override "++ uma veiculação" usa a faixa da rule** silenciosamente,
   inclusive quando há múltiplas rules com faixas diferentes na mesma
   célula+dia. O operador não tem como saber qual faixa "encarnou" na
   inserção extra.
3. **Não há flexibilidade**: o operador não consegue dizer "neste dia
   específico, a inserção extra é às 19h" — só "neste dia espero 3 plays".

Este design preenche o gap adicionando `time_start`/`time_end` ao override,
estende o categorizador pra consultar overrides e atualiza a UI do
`OverridePopover` pra suportar entrada da faixa com herança inteligente.

## Decisões de design

| # | Decisão | Justificativa |
|---|---------|---------------|
| D1 | **Uma faixa por célula** (override REPLACE total das rules) | Mais simples que múltiplas faixas; cobre o caso prático sem inflar schema. |
| D2 | Replicação via **popover individual + atalho "aplicar essa faixa nas demais células deste tipo no mês"** | Sem novos modos de edição (modo pintar) ou seleção múltipla — encaixa no fluxo mental atual. |
| D3 | **Inline +/- só funciona se a célula tem rule OU override**; caso contrário abre popover | Mantém velocidade no caso comum, força clareza no caso ambíguo. |
| D4 | Em célula com **múltiplas rules**: popover não pré-preenche faixa, mostra alerta + chips clicáveis com cada range existente | Evita pegadinha (escolher "primeira" rule é arbitrário; composição é ampla demais). |
| D5 | **`count=0` mantém faixa NOT NULL** mas inerte; frontend desabilita o campo visualmente | Schema simples (sem CHECK condicional); semântica "expected=0 → toda detection é out_slot" independe da faixa. |
| D6 | Migração faz **backfill inteligente**: faixa da rule mais antiga da célula+dia, fallback `06:00–22:00` quando não houver rule | Preserva intenção sem ser destrutivo. |
| D7 | Categorizador passa a consultar overrides; quando override existe, **rules são ignoradas** pra essa célula+dia (segue D1) | Mantém o modelo coerente: override é "fonte da verdade" da célula. |

## Esquema de dados

### Migration `0030_override_time_window`

```sql
BEGIN;

-- 1. Adiciona colunas nullable pra permitir backfill em duas fases.
ALTER TABLE distribution_overrides
    ADD COLUMN time_start TIME,
    ADD COLUMN time_end   TIME;

-- 2. Backfill inteligente: faixa da rule aplicável mais antiga; fallback
--    06:00-22:00 quando não houver rule na célula+dia.
UPDATE distribution_overrides o
SET time_start = COALESCE(r.time_start, TIME '06:00'),
    time_end   = COALESCE(r.time_end,   TIME '22:00')
FROM (
    SELECT DISTINCT ON (o.campaign_id, o.type_id, o.station_id, o.for_date)
        o.campaign_id, o.type_id, o.station_id, o.for_date,
        r.time_start, r.time_end
    FROM distribution_overrides o
    LEFT JOIN distribution_rules r
        ON r.campaign_id = o.campaign_id
       AND r.type_id     = o.type_id
       AND o.station_id  = ANY(r.station_ids)
       AND o.for_date BETWEEN r.start_date AND r.end_date
       AND (1 << EXTRACT(DOW FROM o.for_date)::INT) & r.weekday_mask != 0
    ORDER BY o.campaign_id, o.type_id, o.station_id, o.for_date,
             r.created_at, r.id
) r
WHERE o.campaign_id = r.campaign_id
  AND o.type_id     = r.type_id
  AND o.station_id  = r.station_id
  AND o.for_date    = r.for_date;

-- 3. Salvaguarda: se algum override ainda estiver com faixa NULL, a próxima
--    instrução falha e o BEGIN/COMMIT rola back. Garante atomicidade da
--    migration (ou aplica tudo, ou nada).
ALTER TABLE distribution_overrides
    ALTER COLUMN time_start SET NOT NULL,
    ALTER COLUMN time_end   SET NOT NULL,
    ADD CONSTRAINT override_times_valid CHECK (time_end > time_start);

COMMIT;
```

**Risco de perda de dado: zero.** As colunas são novas, nenhuma coluna
existente é alterada. Tudo está numa transaction — falha em qualquer passo
faz rollback total.

**Distorção possível (não é perda):** overrides em células com >1 rule
herdam a faixa da rule mais antiga. Editável pela UI em 2 cliques.

### Down

```sql
ALTER TABLE distribution_overrides
    DROP CONSTRAINT override_times_valid,
    DROP COLUMN time_end,
    DROP COLUMN time_start;
```

### View `daily_play_summary`

**Sem mudança.** A view continua usando `plays_expected` do override (que
preserva o valor existente). A lógica de janela de tempo está no
categorizador, não na view.

## API

### Go types

`workers/internal/catalog/distribution_overrides.go`:

```go
type DistributionOverride struct {
    CampaignID    uuid.UUID  `json:"campaign_id"`
    TypeID        uuid.UUID  `json:"type_id"`
    StationID     uuid.UUID  `json:"station_id"`
    ForDate       time.Time  `json:"for_date"`
    PlaysExpected int16      `json:"plays_expected"`
    TimeStart     string     `json:"time_start"`   // "HH:MM"
    TimeEnd       string     `json:"time_end"`     // "HH:MM"
    Reason        *string    `json:"reason,omitempty"`
    CreatedAt     time.Time  `json:"created_at"`
    CreatedBy     *uuid.UUID `json:"created_by,omitempty"`
}

type UpsertOverrideInput struct {
    CampaignID    uuid.UUID
    TypeID        uuid.UUID
    StationID     uuid.UUID
    ForDate       time.Time
    PlaysExpected int16
    TimeStart     string  // "HH:MM" obrigatório
    TimeEnd       string  // "HH:MM" obrigatório
    Reason        *string
    CreatedBy     *uuid.UUID
}
```

`Upsert` SQL inclui as 2 colunas no INSERT e no `ON CONFLICT DO UPDATE`.
`ListByCampaignAndDateRange` retorna `time_start::text` e `time_end::text`.

### Endpoint REST

`PUT /v1/internal/campaigns/{campaignID}/distribution-overrides`

Payload novo:

```json
{
  "type_id":         "uuid",
  "station_id":      "uuid",
  "for_date":        "YYYY-MM-DD",
  "plays_expected":  3,
  "time_start":      "08:00",
  "time_end":        "10:00",
  "reason":          null
}
```

Validações adicionais no handler:

- `time_start` e `time_end` devem casar `^([01]\d|2[0-3]):[0-5]\d$` (regex HH:MM)
- `time_end > time_start` (string compare lexicográfica funciona porque
  o formato é HH:MM com zero-padding)
- Campos obrigatórios mesmo quando `plays_expected == 0` (faixa inerte
  mas armazenada — decisão D5)

`DELETE` não muda.

`GET /v1/internal/campaigns/{campaignID}/distribution-overrides?from&to`
inclui `time_start`/`time_end` no JSON de cada item.

## Categorizador

### Assinatura nova

`workers/internal/categorizer/categorizer.go`:

```go
type Override struct {
    PlaysExpected int16
    TimeStart     time.Time
    TimeEnd       time.Time
}

func Categorize(
    detectedAt time.Time,
    cmp Campaign,
    rules []Rule,
    override *Override,    // nil quando não há override pra (campaign, type, station, date)
) string
```

### Tabela de decisão

| Caso | Resultado |
|------|-----------|
| `detectedAt` fora de `[cmp.StartDate, cmp.EndDate]` | `out_date` |
| Override existe, `count == 0` | `out_slot` |
| Override existe, `count > 0`, detection ∈ `[ts-15min, te+15min]` | `in_slot` |
| Override existe, `count > 0`, detection fora da faixa tolerada | `out_slot` |
| Sem override, nenhuma rule aplicável (date+weekday) | `orphan` |
| Sem override, rule aplicável, detection ∈ alguma faixa tolerada | `in_slot` |
| Sem override, rule aplicável, detection fora de toda faixa | `out_slot` |

Tolerância `SlotToleranceSeconds = 15min` permanece.

### Call site

`workers/internal/catalog/detections.go` — função `categorize`:

```go
// Após carregar campaign e rules, busca override:
var ov *categorizer.Override
var pe int16
var tsStr, teStr string
err := d.pool.QueryRow(ctx, `
    SELECT plays_expected, time_start::text, time_end::text
    FROM distribution_overrides
    WHERE campaign_id = $1
      AND type_id = (SELECT type_id FROM materials WHERE id = $2)
      AND station_id = $3
      AND for_date = ($4::timestamptz AT TIME ZONE 'America/Sao_Paulo')::date
`, in.CampaignID, in.CommercialID, in.StationID, in.DetectedAt).Scan(&pe, &tsStr, &teStr)
switch {
case err == nil:
    ts, _ := time.Parse("15:04:05", tsStr)
    te, _ := time.Parse("15:04:05", teStr)
    ov = &categorizer.Override{PlaysExpected: pe, TimeStart: ts, TimeEnd: te}
case errors.Is(err, pgx.ErrNoRows):
    // ov fica nil — comportamento anterior
default:
    return categorizer.CatOrphan, err
}

return categorizer.Categorize(in.DetectedAt, cmp, rules, ov), nil
```

Mesmo path serve `CreateDetection` (engine) e `CreateManual` (operador) —
ambos passam pelo `categorize()` interno.

### Recategorização retroativa

**Fora de escopo desta entrega.** Detections já gravadas têm `category`
congelada no INSERT. Mudar override depois NÃO recategoriza detections
anteriores. Mesma limitação atual de mudança de rule. Se necessário no
futuro, criar job `recategorize-detections --campaign=...` separado.

## Frontend

### `OverridePopover`

Props novas:

- `currentRuleWindows: Array<{time_start, time_end}>` — todas as faixas das
  rules aplicáveis à célula+dia.
- `lastUsedWindow: {time_start, time_end} | null` — última faixa aplicada
  pelo usuário na sessão (estado em `DistributionStep`).
- `currentOverrideWindow: {time_start, time_end} | null` — faixa do override
  existente, se houver.

Layout:

```
┌────────────────────────────────────────────┐
│ Rádio Globo AM · Spot 30s    19/05/2026   │
├────────────────────────────────────────────┤
│ Regra: 2×/dia · 08:00–10:00                │
│                                             │
│ Veiculações: [ − ] [ 3 ] [ + ]              │
│                                             │
│ Faixa horária:                              │
│ [ 08:00 ] – [ 10:00 ]                       │
│                                             │
│ ☐ Aplicar essa mesma faixa nas demais       │
│   células deste tipo neste mês             │
│                                             │
│ [↺ Voltar à regra]  [ Aplicar ]            │
└────────────────────────────────────────────┘
```

Inputs nativos `<input type="time">`. Validação inline:

- `time_end > time_start` — mensagem inline + Aplicar desabilitado.
- Inputs desabilitados (opacity 0.5, label "inativa quando 0 veiculações")
  quando `value === 0`. Valor armazenado é o que estava herdado/digitado.

### Default de faixa (prioridade — primeira condição que casa, para)

1. `currentOverrideWindow` existe → usa a faixa do override.
2. `currentRuleWindows.length === 1` → usa a faixa única dessa rule.
3. `currentRuleWindows.length > 1` → **sem default**, mostra alerta + chips (descrito abaixo).
4. `currentRuleWindows.length === 0` e `lastUsedWindow` existe → usa última faixa aplicada na sessão.
5. `currentRuleWindows.length === 0` e nada mais → fallback `06:00–22:00`.

### Alerta multi-rule

Quando `currentRuleWindows.length > 1` e a célula ainda não tem override:

```
┌────────────────────────────────────────────┐
│ ⚠ Esta célula tem 2 regras:                │
│   [ 08:00–10:00 ]  [ 14:00–16:00 ]         │
│ Definir override substituirá ambas neste   │
│ dia. Escolha uma faixa ou edite os campos. │
└────────────────────────────────────────────┘
```

Chips são clicáveis: clicar preenche os dois inputs com aquele range. O
usuário ainda pode editar manualmente.

### Replicação ("aplicar nas demais")

Checkbox no popover. Quando marcado e `Aplicar` clicado:

```js
onApply(value, timeStart, timeEnd, applyToOthers: true)
```

`DistributionStep` faz:

```js
const others = overrides.filter(o =>
  o.type_id === ctx.typeId &&
  !(o.station_id === ctx.stationId && o.for_date.slice(0,10) === ctx.date)
)
await Promise.all(others.map(o => upsertOverride.mutateAsync({
  campaignId,
  type_id: o.type_id,
  station_id: o.station_id,
  for_date:  o.for_date.slice(0,10),
  plays_expected: o.plays_expected,    // preservado
  time_start: timeStart,                // sobrescrito
  time_end:   timeEnd,                  // sobrescrito
})))
```

**Importante:** afeta APENAS células que já têm override. Não cria override
novo em célula limpa.

### `DistributionStep`

Mudanças:

- Estado novo: `lastUsedWindow`, atualizado após cada `onApply` bem-sucedido.
- `handleCellClick` computa `currentRuleWindows` filtrando `rules` pela
  célula clicada (mesmo type, station ∈ station_ids, data dentro do range,
  weekday match).
- `onCellIncrement`/`onCellDecrement` ganham branch novo: se a célula não
  tem rule E não tem override, abre o popover (em vez de stagear draft no
  PendingDraftsBar). O valor inicial do popover já vem pré-incrementado.
- Drafts pendentes do PendingDraftsBar agora levam consigo `time_start`/
  `time_end` derivados (override existente → rule única → bloqueado em
  multi-rule sem override; o caso multi-rule sem override força popover).
- `commitDrafts` envia faixa junto com `plays_expected`.

### `DistributionGrid`

Mudança mínima. Pode (opcional) adicionar ícone de relógio pequeno no badge
de override pra sinalizar "faixa customizada" — não bloqueia a entrega.

### Hook `useUpsertOverride`

`frontend/src/api/hooks.js` — propaga `time_start`/`time_end` no body
PUT. Sem mudança na assinatura externa (`mutateAsync({...})` aceita campos
novos).

## Testes

### Backend

`workers/internal/categorizer/categorizer_test.go`:

- Override `count=2`, faixa 14:00–16:00, detection 14:30 → `in_slot`
- Override `count=2`, faixa 14:00–16:00, detection 09:00 com rule 08:00–10:00 existente → `out_slot` (rule ignorada)
- Override `count=0` + qualquer detection → `out_slot`
- Override + tolerância 15min nos extremos (boundary-inclusive)
- Sem override: regressão completa do comportamento atual

`workers/internal/catalog/distribution_overrides_test.go`:

- Upsert com faixa persiste corretamente
- Upsert duplicado atualiza faixa via `ON CONFLICT`
- CHECK constraint rejeita `time_end <= time_start`
- `ListByCampaignAndDateRange` retorna faixa nas linhas

`workers/internal/catalog/detections_test.go`:

- Criar detection numa célula com override usa a faixa do override

**Migration smoke:** test em Go que (1) insere overrides sem faixa antes da
migration, (2) roda a migration, (3) verifica que cada override recebeu
faixa correta (rule mais antiga ou 06:00–22:00).

### Frontend

Cobertura manual no plan:

- Popover com 0 rule → default 06:00–22:00 (ou last-used)
- Popover com 1 rule → default = faixa da rule
- Popover com 2+ rules → mostra alerta + chips, sem default
- Checkbox "aplicar nas demais" → dispara upsert nas outras células com override do tipo no mês
- count=0 → faixa desabilitada visualmente
- Inline +/- em célula sem rule + sem override → abre popover
- Inline +/- em célula com rule → bumpa count usando faixa herdada

## Migração e rollout

1. Aplicar migration 0030 (backfill automático).
2. Deploy do backend (catalog + handler + categorizador atualizados).
3. Deploy do frontend (popover + step).

Sem flag de feature — a mudança é compatível com overrides existentes via
backfill. O endpoint REST quebra compatibilidade pra clientes externos que
não passem `time_start`/`time_end` — hoje só o frontend interno consome.

## Fora de escopo

- Múltiplas faixas por célula (override com lista de slots)
- Modo "pintar" ou seleção múltipla de células
- Recategorização retroativa de detections já gravadas
- Mudanças no schema/comportamento de `distribution_rules`
- Indicador visual obrigatório de "faixa customizada" no badge da grid
  (opcional, não bloqueia)
