# Design — Carve-out: tocada em dia extra dentro do período conta como bônus

**Data:** 2026-07-13
**Autor:** Dereck + Claude
**Status:** aprovado (aguardando revisão do spec)

## Problema

Material com **carve-out** (regra de distribuição escopada a material específico,
`distribution_rules.material_ids`) que toca **dentro do período de vigência das
suas regras**, mas num **dia-da-semana sem meta**, é hoje categorizado como
`out_date` — que **não conta como bônus** e aparece como "fora de data" 🔴.

Caso concreto (o que motivou): campanha `5e39554f-739e-4418-a96e-bf5ff41e402c`
(371 MRA VERISURE), material **147 VERISURECARVÃO**. As regras do CARVÃO em julho
são majoritariamente **Seg–Sex** (`weekday_mask = 62`, `cobre_sabado = false`),
vigência **29/06–26/07**. Uma tocada no **sábado 04/07** (dentro da vigência, mas
dia sem regra) cai em `out_date`. A intenção do negócio: como está **dentro do
período contratado**, a tocada é uma **entrega extra = bônus**.

Diagnóstico completo confirmou que **não** é projeção-fantasma (base e projeção
`detection_campaigns` sincronizadas, sem divergência) nem override — é a semântica
do carve-out. Ver [material-specific-distribution-rules.md](../../features/material-specific-distribution-rules.md).

## Objetivo

No ramo carve-out do categorizador, distinguir dois casos que hoje colapsam em
`out_date`:

| Situação | Hoje | Depois |
|---|---|---|
| Tocada **fora** do range de datas de todas as regras do material (naquela emissora) | `out_date` 🔴 | `out_date` 🔴 (inalterado — fora do período de verdade) |
| Tocada **dentro** do range de alguma regra do material (naquela emissora), mas dia-da-semana/faixa não casa | `out_date` 🔴 | **`orphan`** 🔵 → entra no **bônus** |

`out_date` fica reservado a tocadas realmente fora do período de vigência do
material. As demais categorias (`in_slot`, `out_slot`) são **inalteradas**.

## Semântica precisa da fronteira

"Dentro do período" é avaliado **por emissora** (a granularidade que o categorizador
já usa — [detections.go:186-193](../../../workers/internal/catalog/detections.go#L186)
filtra as regras por `station_id = ANY(r.station_ids)`).

Regra formal, dentro do ramo `carved` (material nomeado em ≥1 regra específica
daquela emissora):

1. Existe regra específica do material, naquela emissora, casando **data + dia + faixa** (±15min)? → `in_slot`
2. Senão, existe regra específica do material casando **data + dia** (não a faixa)? → `out_slot`
3. **[NOVO]** Senão, existe regra **específica do material** (`material_ids` contém o material — nunca as regras gerais do tipo) naquela emissora cujo **range `[start_date, end_date]` cobre a data** (ignorando dia-da-semana/faixa)? → **`orphan`**
4. Senão → `out_date`

O passo 3 é a única inserção. Os passos 1, 2 e 4 são os atuais. As regras gerais
do tipo (`material_ids` vazio) continuam **sem valer** para material carved, em
todos os passos — inclusive o novo.

## Por que reusar `orphan` (e não criar categoria nova)

Decisão do dono (2026-07-13): reaproveitar `orphan`, que a view
[`daily_play_summary`](../../../migrations/0041_detection_campaigns.up.sql#L133)
**já soma no bônus** (`bonus = GREATEST(0, in_slot − expected) + orphan`). Assim:

- **Sem migration** (o `CHECK (category IN (...))` já permite `orphan`).
- **Sem mudança na view** (orphan já credita bônus).
- **Sem mudança no frontend** (orphan já tem cor/label — azul `#2563EB`).

Trade-off aceito: a tocada aparece como "órfã" (azul), não com rótulo "Bônus"
próprio, e não é distinguível de "material tocado sem plano" no relatório.
Categoria dedicada `bonus` foi considerada e **descartada por YAGNI** (custo de
migration + view + ~5 componentes de frontend + docs).

## Onde muda — 2 lugares, obrigatoriamente sincronizados

A lógica de carve-out é duplicada e **precisa divergir zero** (o próprio código
avisa: divergência = bug silencioso):

1. **Go (insert ao vivo):** [categorizer.go::Categorize](../../../workers/internal/categorizer/categorizer.go#L128),
   ramo `if carved`. Após o loop que decide `in_slot`/`out_slot`, antes de
   `return CatOutDate`, inserir a checagem do passo 3: varrer as regras
   específicas do material e retornar `CatOrphan` se alguma tem
   `date ∈ [StartDate, EndDate]`.
2. **SQL (recategorização/backfill):** [recatClassifyTailSQL](../../../workers/internal/catalog/distribution_rules.go#L281),
   ramo carve-out. O `ELSE 'out_date'` interno vira um `CASE` que checa
   `EXISTS (… range cobre a data …)` → `'orphan'`, senão `'out_date'`.

## Alcance e impacto

**Global.** A regra vale para **toda** campanha com material carve-out, não só a
VERISURE. Confirmado pelo dono (2026-07-13). Toda tocada de carve-out que caía em
`out_date` **dentro do período** passa a creditar bônus — muda números de
bônus/cobrança **retroativamente** em outras campanhas.

## Backfill retroativo

Novo `cmd/backfill-recategorize` (padrão dos `cmd/backfill-*`):

- Roda `RecategorizeForCampaign` em todas as campanhas (idempotente; atualiza
  `detections.category` **e** `detection_campaigns.category` — o tail SQL já faz as
  duas).
- **Regra 4.8 (cautela de dado que afeta cobrança):** rodar **primeiro contra um
  clone** do dump de prod, medir o delta (`out_date → orphan` por campanha) e o
  Dereck revisa antes de aplicar em prod.
- **Preview read-only:** o mesmo SQL do tail com `COUNT(*) FILTER` em vez de
  `UPDATE` dá a contagem de tocadas que mudariam de categoria, por campanha — pode
  rodar direto em prod (SELECT) para dimensionar sem aplicar.
- **Regra 6.7:** o CLI novo precisa das 2 linhas no
  [`workers.Dockerfile`](../../../infra/docker/Dockerfiles/workers.Dockerfile) no
  mesmo commit.

## Testes (TDD)

`workers/internal/categorizer/categorizer_test.go` + teste de recat SQL
(DB-gated). Casos:

1. **[o bug]** Material carved, regra Seg–Sex, tocada no sábado dentro do range → `orphan` (era `out_date`).
2. Material carved, tocada **fora** do range de todas as regras do material → `out_date` (inalterado).
3. Material carved, dia programado, dentro da faixa → `in_slot` (inalterado).
4. Material carved, dia programado, fora da faixa → `out_slot` (inalterado).
5. **Paridade Go × SQL:** um dataset roda pelo `Categorize` (Go) e pelo
   `recategorizeScope` (SQL) e produz o **mesmo** veredito por tocada.

## Não-objetivos (YAGNI)

- **Não** cria categoria `bonus` (reusa `orphan`).
- **Não** mexe em `out_slot` (tocada em dia programado fora da faixa continua `out_slot`).
- **Não** mexe no ramo de material comum (sem carve-out) — que já retorna `orphan`
  nesse caso, portanto já credita bônus; a mudança apenas **alinha o carve-out** ao
  comportamento que o material comum já tem.
- **Não** cria etiqueta/cor/coluna nova no frontend.

## Riscos

- **Divergência Go × SQL** se um dos dois lugares não for atualizado. Mitigado pelo
  teste de paridade (caso 5).
- **Impacto de cobrança retroativa** subestimado. Mitigado pelo preview read-only +
  clone antes de aplicar.
- **Tocada exatamente no limite do range** (start_date/end_date): usar a mesma
  normalização de data SP (`dateOnlySP`) já usada no categorizador, sem tolerância
  de dia (a tolerância de ±15min é só de faixa horária).

## Referências

- [material-specific-distribution-rules.md](../../features/material-specific-distribution-rules.md) — carve-out
- [detection-count-consistency.md](../../architecture/detection-count-consistency.md) — filtro aprovado
- [incident-2026-06-30-phantom-projection-reattribution.md](../../incidents/incident-2026-06-30-phantom-projection-reattribution.md) — descartado no diagnóstico
- categorizer: `workers/internal/categorizer/categorizer.go`
- recat SQL: `workers/internal/catalog/distribution_rules.go`
- view: `migrations/0041_detection_campaigns.up.sql`
