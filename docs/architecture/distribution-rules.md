---
status: implementado
ultima-verificacao: 2026-06-29
codigo-relacionado:
  - migrations/0017_distribution_plan.up.sql
  - migrations/0018_detections_categorization.up.sql
  - migrations/0019_rules_by_type.up.sql
  - migrations/0043_rule_material_scope.up.sql
  - workers/internal/api/handlers/distribution_rules.go
  - workers/internal/api/handlers/materials.go
  - workers/internal/catalog/distribution_rules.go
  - workers/internal/catalog/materials.go
  - workers/internal/categorizer/categorizer.go
---

# Distribution Rules — Semantica e Operacao

Documenta as regras de distribuicao (programado) e categorizacao de deteccoes introduzidas pelo Plano 1 — Foundations.

> Spec arquitetural: [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](../superpowers/specs/2026-05-11-campaign-wizard-design.md)

## O que e uma "regra de distribuicao"

Uma regra define quantas vezes um material deve tocar por dia, em quais emissoras, em qual faixa horaria e em qual periodo.

Tabela: `distribution_rules` (migration 0017).

Estrutura:
- `campaign_id` — sempre dentro de uma campanha
- `type_id` — qual tipo de material a regra cobre
- `material_ids[]` — vazio = todos os materiais do tipo (fungível); preenchido = regra "carve-out" só pra esses materiais específicos
- `station_ids[]` — quais emissoras (subconjunto das emissoras da campanha)
- `start_date`, `end_date` — periodo dentro da campanha
- `weekday_mask` — bitmask dos dias da semana (bit 0=Dom, 6=Sab). Ex: `62` = seg-sex (`0111110`)
- `time_start`, `time_end` — faixa horaria precisa (HH:MM, ex: `08:15`–`10:45`)
- `plays_per_day` — insercoes esperadas por dia (1-100)

Multiplas regras podem coexistir pra mesma combinacao (material, station) — ex: manha + tarde.

### Independência de material

Uma regra de distribuição é independente de qualquer material vinculado. O `type_id` referencia `material_types` (tabela global, não por campanha), e a view `daily_play_summary` calcula `expected` somando `plays_per_day` das regras sem precisar de material linkado.

Isso habilita o fluxo "planejar antes do áudio chegar" no wizard — ver [campaign-wizard.md#planejamento-sem-material-distribuição-fantasma](../features/campaign-wizard.md#planejamento-sem-material-distribuição-fantasma).

Quando o material finalmente é subido na campanha (Step 3 do wizard) com o `type_id` planejado, as detections daquele material são automaticamente categorizadas pelas regras existentes — zero ação manual.

## Overrides

Tabela: `distribution_overrides` (migration 0017).

Permite sobrescrever o `plays_expected` calculado por regras pra uma celula especifica (material, station, data). Util quando a emissora avisa que vai trocar a grade num dia, ou quando o cliente pede ajuste pontual.

Override tem precedencia sobre regras na view `daily_play_summary` — se ha override pra (material, station, data), o `expected` exibido = `plays_expected` do override (ignorando totalmente o calculo de regras).

## Categorias de detection

Cada detection e classificada em uma das 4 categorias (`detections.category` — migration 0018):

| Categoria | Significado | Cor na UI |
|-----------|-------------|-----------|
| `in_slot`  | Tocou dentro da faixa horaria de uma regra aplicavel | Verde |
| `out_slot` | Tocou na data, mas fora da faixa horaria | Amarelo |
| `out_date` | Tocou fora da data da campanha | Roxo |
| `orphan`   | Tocou sem regra aplicavel (bonus puro) | Azul |

A view `daily_play_summary` agrega por (campaign, material, station, data) e calcula os 6 numeros exibidos:
- `expected` (cinza) = soma de `plays_per_day` das regras aplicaveis, OU override
- `in_slot`  (verde) = count(in_slot)
- `deficit` (vermelho) = max(0, expected - in_slot - out_slot)
- `bonus`   (azul) = max(0, in_slot - expected) + count(orphan)
- `out_slot` (amarelo) = count(out_slot)
- `out_date` (roxo) = count(out_date)

## Carve-out por material

Regras podem ser escopadas a materiais específicos dentro de um tipo via `material_ids[]`:

- **Vazio** (`material_ids = []` — a coluna é `NOT NULL DEFAULT '{}'`, nunca NULL): regra se aplica a **todos** os materiais do tipo (comportamento clássico pré-migration 0043)
- **Preenchido** (`material_ids = [uuid1, uuid2, ...]`): regra só se aplica a esses materiais específicos (carve-out)

Quando um material tem ≥1 regra específica, ele é julgado **só** por essas regras, não pelas regras gerais do tipo. Material sem regra específica continua usando as regras gerais, inalterado.

Detalhes: [material-specific-distribution-rules.md](../features/material-specific-distribution-rules.md).

## Editabilidade

Secao 8 da spec: edicao livre so do **futuro**.

- Regras com `start_date >= hoje`: criar/editar/excluir livremente
- Regras com `start_date < hoje`: somente encurtar `end_date` pra `hoje`
- Overrides em datas passadas: read-only

Validacao atualmente NAO esta implementada no backend — fica a criterio do frontend. Ver follow-up F-91.

## Re-categorizacao

Quando uma regra e criada/editada/excluida via API, o handler `DistributionRulesHandler` dispara `RecategorizeForRule` (ou `RecategorizeForCampaign` no caso de delete) em goroutine com timeout de 30s. Detections existentes no escopo da regra sao atualizadas conforme a nova logica.

A operacao roda inteira em SQL via `recategorizeScope` em `catalog/distribution_rules.go` — sem N+1 queries. Para uma campanha inteira leva milissegundos mesmo com centenas de milhares de detections.

### Gatilho por troca de tipo do material (não só por rule)

A categoria é casada por **tipo** (`r.type_id = material.type_id`), então mudar o `type_id` de um material também invalida a categoria gravada das detections dele — calculada no insert com o tipo antigo. Sem recategorizar, uma detection que passa a casar uma regra do tipo novo continua `orphan` e some pra "bônus (sem regra)" no resumo diário.

Por isso `PATCH /materials/{id}/type` (`MaterialsHandler.UpdateType`) dispara `RecategorizeForMaterial(materialID)` em goroutine best-effort após o `UPDATE` do `type_id`. Esse método recategoriza **todas as detections do material, em todas as campanhas** onde ele aparece (scope `d.commercial_id = $materialID`, sem filtro de campanha). Como o scope resolve `m.type_id` ao vivo (JOIN materials), rodar após o update reclassifica contra as regras do tipo atual.

`RecategorizeForMaterial` e `recategorizeScope` compartilham o mesmo trecho de classificação SQL (`recatClassifyTailSQL`) — fonte única pra regra de tolerância de 15 min, evitando divergência entre os dois caminhos. O frontend (`useUpdateMaterialTypeId`) invalida `daily-summary` + `detections` no sucesso pra UI refletir a nova categoria sem reload.

> Bug corrigido em 2026-06-17: material trocava de tipo e as veiculações viravam todas "bônus" mesmo com regra existindo pro tipo novo — porque o `UpdateType` só trocava a coluna, sem recategorizar.

### Tolerância de 15 min

A faixa horária das rules é comparada com folga de **±900 segundos (15 min)** em cada extremo, igual ao `categorizer.SlotToleranceSeconds` aplicado no insert. Uma rule `09:30–10:00` cobre detections entre `09:15` e `10:15` como `in_slot`.

A tolerância existe pra absorver jitter de stream (buffer + atraso de programação ao vivo) — o operador entende "tocou às 6h" mesmo quando o trecho real veiculou às 05:45.

Antes deste fix (2026-05-26), o SQL usava `BETWEEN r.time_start AND r.time_end` direto: detections que o Go categorizer tinha marcado `in_slot` viravam `out_slot` na primeira recategorize disparada por criação/edição de rule. Sintoma reportado pelo operador: "a tolerância não está funcionando, contagem some quando edito a regra".

## Endpoints relevantes

| Metodo | Rota | Descricao |
|--------|------|-----------|
| GET    | `/v1/internal/campaigns/{id}/distribution-rules` | Lista regras |
| POST   | `/v1/internal/campaigns/{id}/distribution-rules` | Cria regra |
| PUT    | `/v1/internal/campaigns/{id}/distribution-rules/{rid}` | Atualiza regra |
| DELETE | `/v1/internal/campaigns/{id}/distribution-rules/{rid}` | Remove regra |
| GET    | `/v1/internal/campaigns/{id}/distribution-overrides?from=&to=` | Lista overrides |
| PUT    | `/v1/internal/campaigns/{id}/distribution-overrides` | Cria/atualiza override |
| DELETE | `/v1/internal/campaigns/{id}/distribution-overrides` | Remove override |
| GET    | `/v1/internal/campaigns/{id}/daily-summary?from=&to=` | Agregado pra UI |

## Tabelas relacionadas

- `distribution_rules` — regras
- `distribution_overrides` — overrides por celula
- `detections.category` — coluna preenchida pelo categorizer no insert + recategorizacao
- `daily_play_summary` — VIEW agregada (nao tabela)
