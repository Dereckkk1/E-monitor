---
status: implementado
ultima-verificacao: 2026-07-14
codigo-relacionado:
  - workers/internal/catalog/distribution_rules.go
  - workers/internal/catalog/projection_reconcile.go
  - workers/internal/projrecon/scheduler.go
  - workers/cmd/backfill-recategorize/main.go
---

# Invariante de categoria por projeção

Garante que `detection_campaigns.category` — a categoria por-campanha que a view
`daily_play_summary`, a grade `/detections`, `/insights` e os relatórios leem —
nunca fique dessincronizada das regras/overrides vivas daquela campanha.

> Spec: [docs/superpowers/specs/2026-07-14-projection-category-invariant-design.md](../superpowers/specs/2026-07-14-projection-category-invariant-design.md).
> Ver também [multi-attribution.md](../features/multi-attribution.md) (o que é uma
> "projeção") e [detection-count-consistency.md](detection-count-consistency.md)
> (o conjunto "aprovado" que qualquer contagem deve respeitar).

## O invariante

> Para toda projeção em `detection_campaigns`, `category` é igual ao veredito do
> categorizador para `(dc.campaign_id, dc.commercial_id, d.station_id, d.detected_at)`
> contra as `distribution_rules` + `distribution_overrides` **vivas** daquela campanha.

Nota de escopo: o invariante **não** decide *qual* material/campanha uma tocada
pertence (isso é atribuição/dedup — [version-disambiguation.md](version-disambiguation.md),
fora de escopo aqui). Ele garante que, **dada** a projeção, a categoria gravada
está sempre certa.

## O caso motivador — COPA 10/07

Campanha "189 COPA", emissora ND FM, 10/07/2026:

1. O material 43 (campanha "183.1 Mídia Geral") e o material 143 (campanha
   "189 COPA") são o **mesmo áudio** — mesmo `master_sha256`, subido 2×.
2. O spot RÔGGA toca 11× na ND FM naquele dia. O dedup/co-fire mantém viva a
   detecção do material **43** (base = 183.1 Mídia Geral). O fan-out F-119
   (ver [multi-attribution.md](../features/multi-attribution.md)) cria, para
   cada tocada, a projeção `(campanha=COPA, commercial_id=143)`.
3. A regra carve-out da COPA (material 143, 06:00–23:59, 7/dia) só foi criada
   **no meio do dia 10/07**. As tocadas anteriores nasceram `orphan` — correto
   no instante (não havia regra ainda).
4. A criação da regra disparou `RecategorizeForRule` — mas o escopo antigo
   filtrava por `d.campaign_id = COPA` (a campanha-base da detecção), e a base
   dessas tocadas era 183.1, não COPA. As 10 projeções fan-out ficaram
   `orphan` **para sempre**: nenhum recat subsequente as alcançava.
5. Resultado na grade: `expected 7 / in_slot 1 / deficit 6 / bonus 10` — a
   campanha parecia furada mesmo com a regra certa. Reparo manual em prod
   (2026-07-14) via `UPDATE` dirigido — terceira ocorrência dessa classe de bug
   (ver memórias `duplicate-material-cross-campaign-misattribution` e
   `detection-campaigns-projection-sync-reattribution`).

Causa raiz estrutural: **todo** caminho de recategorização escopava pela
campanha-base da tocada (`detections.campaign_id`), não pela campanha da
projeção (`detection_campaigns.campaign_id`). Uma projeção fan-out cuja
campanha difere da base nunca era alcançada por nenhum recat.

## As 3 camadas de enforcement

| Camada | Mecanismo | Cobre |
|---|---|---|
| Na escrita | Produtores calculam a categoria via categorizador no insert (já correto antes desta mudança) | Estado inicial correto |
| Na edição | Motor de recat **escopado por projeção** | Regra/override criada ou editada depois do insert |
| Contínua | Reconciler periódico (`projrecon`) com métrica + alerta | Qualquer produtor futuro/bug novo que fure o invariante |

A camada contínua é o que transforma "corrigimos os bugs conhecidos" em "nunca
mais": um caminho novo que fure o invariante é curado em ≤1 intervalo do
reconciler e denunciado por métrica/alerta — não fica anos em silêncio como o
caso COPA.

### Camada 2 — motor de recat escopado por projeção

Em `workers/internal/catalog/distribution_rules.go`, o SQL de classificação
(`recatClassifyTailSQL`) é dividido em duas consts:

- **`recatClassifiedCTE`** — a CTE `classified`: replica `categorizer.Categorize`
  em SQL para cada linha da CTE `scope`. Fonte única Go×SQL (paridade preservada
  — os testes de paridade carve-out/override existentes continuam válidos).
- **`recatApplySQL`** — aplica o veredito: `UPDATE detection_campaigns` (sempre)
  e `UPDATE detections` (só quando a projeção é a canônica — ver guarda abaixo).
  Separada da CTE de classificação para que o reconciler (camada 3) possa
  **contar** divergências (`SELECT`, via `recatClassifiedCTE` sozinha) sem
  aplicá-las.

```go
const recatClassifyTailSQL = recatClassifiedCTE + recatApplySQL
```

`recategorizeScope` (usado por `RecategorizeForRule`/`ForCampaign`/`ForOverride`)
monta a CTE `scope` a partir de **`detection_campaigns`** (a projeção), não de
`detections` (a base):

```sql
WITH scope AS (
    SELECT dc.detection_id AS id, dc.detected_at, dc.campaign_id,
           dc.commercial_id AS material_id, m.type_id, d.station_id
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    JOIN materials m ON m.id = dc.commercial_id
    WHERE dc.campaign_id = $1
      AND ($2::uuid IS NULL OR m.type_id = $2)
      AND ($3::uuid[] IS NULL OR d.station_id = ANY($3))
      AND (date_trunc('day', dc.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
           BETWEEN $4::date AND $5::date)
)
```

Uma projeção fan-out cuja campanha é `$1` agora entra no escopo mesmo quando a
base da detecção (`d.campaign_id`) é outra campanha — é exatamente o ponto cego
do caso COPA.

`RecategorizeForMaterial` (disparado por `PATCH /materials/:id` ao trocar o
`type_id`) segue o mesmo padrão, escopando por `dc.commercial_id = $1`: alcança
todas as projeções que carregam o material, em **qualquer** campanha, não só
onde ele é a atribuição-base.

### A guarda da base

Sem cuidado, o recat de uma projeção **secundária** (fan-out) sobrescreveria a
categoria da tocada-base (`detections.category`) com o veredito de uma campanha
que não é a dona da base — um bug novo, inverso ao caso COPA. `recatApplySQL`
evita isso com uma guarda no `UPDATE detections`:

```sql
, upd_det AS (
    UPDATE detections d
    SET category = cl.new_category
    FROM classified cl
    WHERE d.id = cl.id AND d.detected_at = cl.detected_at
      AND d.campaign_id = cl.campaign_id   -- guarda: só a projeção CANÔNICA propaga p/ a base
      AND d.category IS DISTINCT FROM cl.new_category
    RETURNING 1
)
UPDATE detection_campaigns dc
SET category = cl.new_category
FROM classified cl
WHERE dc.detection_id = cl.id AND dc.detected_at = cl.detected_at
  AND dc.campaign_id = cl.campaign_id
  AND dc.category IS DISTINCT FROM cl.new_category
```

`d.campaign_id = cl.campaign_id` só é verdadeiro quando a projeção classificada
(`cl`) é a projeção canônica daquela tocada (a campanha da projeção é a mesma
campanha-base da detecção). O `UPDATE detection_campaigns` não tem essa guarda —
ele atualiza a projeção, seja ela canônica ou fan-out, sempre.

### Camada 3 — reconciler contínuo (`projrecon`)

`workers/internal/catalog/projection_reconcile.go` expõe dois métodos, ambos
reutilizando `recatClassifiedCTE` como fonte única:

- **`CountProjectionDrift(ctx, since)`** — `SELECT`-only. Recomputa a categoria
  de toda projeção com `detected_at >= since` e devolve as divergências
  agrupadas por `(campaign_id, from, to, n)`. Não muta nada.
- **`HealProjectionDrift(ctx, since)`** — aplica `recatClassifyTailSQL` na
  mesma janela (todas as campanhas de uma vez) e devolve o nº de projeções
  corrigidas. Idempotente — uma segunda chamada é no-op.

`workers/internal/projrecon/scheduler.go` (`projrecon.Scheduler`, no padrão de
`calibration.Scheduler` — ver [operations/calibration.md](../operations/calibration.md))
roda um tick imediato no boot e depois a cada `Interval`:

1. `CountProjectionDrift(since)` — se `found == 0`, não faz nada além de
   reportar o gauge.
2. Se `found > 0`, chama `HealProjectionDrift(since)` e loga cada transição
   curada (`campaign_id`, `from`, `to`, `n`) com `zap.Info`.

Sem advisory lock (diferente do `calibration.Scheduler`): `HealProjectionDrift`
é idempotente, então duas réplicas curando a mesma janela ao mesmo tempo fazem
o mesmo `UPDATE` — o único efeito colateral é dupla contagem aproximada nas
métricas, aceitável.

Wiring em `cmd/api/main.go`, ao lado do `calibrationScheduler`.

#### Variáveis de ambiente

| Var | Default | Uso |
|-----|---------|-----|
| `PROJECTION_RECONCILE` | ligado | `=off` desliga o scheduler inteiro |
| `PROJECTION_RECONCILE_INTERVAL` | `15m` | Período entre ciclos (`time.ParseDuration`) |
| `PROJECTION_RECONCILE_LOOKBACK` | `48h` | Janela de `detected_at` varrida em cada ciclo |

A janela de 48h limita o custo do scan (as partições de
`detection_campaigns`/`detections` por `detected_at` podam antes de escanear);
o histórico completo converge de uma vez pelo backfill (`--all`, abaixo), não
pelo reconciler.

#### Métricas

| Métrica | Tipo | Labels | Significado |
|---------|------|--------|-------------|
| `radiocheck_projection_drift_last_run` | gauge | — | Nº de divergências encontradas no último ciclo (`CountProjectionDrift`) |
| `radiocheck_projection_drift_healed_total` | counter | `from`, `to` | Projeções cuja categoria o reconciler corrigiu, por transição |
| `radiocheck_recategorize_failures_total` | counter | `origin` | Falhas dos disparos best-effort de recategorização (ver camada 2/handlers) |

`origin` de `radiocheck_recategorize_failures_total`: `rule_create`,
`rule_update`, `rule_delete`, `override_upsert`, `override_delete`,
`material_type_change`. Antes dessas falhas eram engolidas (`_ = h.Repo.Recategorize...`)
e a categoria ficava velha em silêncio; agora incrementam o counter e logam via
`zap.L().Error` (o reconciler continua sendo a rede de segurança — o recat
segue best-effort/assíncrono). O fallback do fan-out em `evidence/service.go`
(`CategorizeFor` falha → projeção nasce `"orphan"`) agora também loga
`zap.Warn` em vez de falhar silenciosamente.

Alerta associado: [docs/runbooks/ProjectionDriftPersistent.md](../runbooks/ProjectionDriftPersistent.md).

### Backfill do histórico (`--all`)

O reconciler cura só a janela móvel (`PROJECTION_RECONCILE_LOOKBACK`, default
48h) — o histórico completo de projeções fan-out que nasceram divergentes antes
do deploy desta mudança não é alcançado por ele. `workers/cmd/backfill-recategorize`
ganhou a flag `--all`:

```bash
# Default (comportamento anterior): só campanhas com regra carve-out
backfill-recategorize --dsn "$DATABASE_URL"

# --all: toda campanha com pelo menos uma projeção em detection_campaigns —
# necessário 1x pra convergir o histórico de fan-out (F-119) que os recats
# antigos (escopados pela base) nunca alcançavam
backfill-recategorize --dsn "$DATABASE_URL" --all
```

Dry-run é o default em ambos os modos (só imprime `ANTES`/`DEPOIS`/`DELTA` de
`out_date`/`orphan`); `--apply` muta. Procedimento canônico para rodar em prod
(regra 4.8 do `CLAUDE.md` — migração/backfill que depende de volume de dados
não pode confiar em DB local vazio):

1. Clonar o dump de prod para um Postgres descartável.
2. Rodar `backfill-recategorize --all --apply` **contra o clone** e conferir o
   delta reportado (`out_date`/`orphan` antes×depois) — espera-se um delta
   grande na primeira execução, incluindo a classe COPA em outras campanhas.
3. Só então rodar `--all --apply` em prod, com o delta já conferido.

Sem mudança no `workers.Dockerfile` (o binário já está listado — regra 6.7).

## Testes

- `workers/internal/catalog/distribution_rules_projection_test.go` — regressão
  COPA (`TestRecategorizeForRule_ReachesFanoutProjections`: recat da regra na
  campanha secundária alcança a projeção fan-out e não toca a base nem a
  projeção canônica da campanha original) e
  `TestRecategorizeForMaterial_ReachesFanoutProjections` (troca de tipo do
  material reclassifica projeções em campanhas ≠ base).
- `workers/internal/catalog/projection_reconcile_test.go` —
  `TestProjectionDrift_CountAndHeal`: semeia drift real (projeção fan-out
  `orphan` + regra criada via repo, sem disparar recat), confirma que `Count`
  acha a divergência e `Heal` a corrige (e que `Count` volta a não reportá-la).
- `workers/internal/projrecon/scheduler_test.go` — `RunOnce` só chama `Heal`
  quando há drift; erro de `Count` propaga sem chamar `Heal`.
- `workers/internal/api/handlers/recat_failures_test.go` — helper
  `recordRecatFailure` incrementa o counter só quando `err != nil`.
