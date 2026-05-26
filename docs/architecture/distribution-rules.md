---
status: implementado
ultima-verificacao: 2026-05-26
codigo-relacionado:
  - migrations/0017_distribution_plan.up.sql
  - migrations/0018_detections_categorization.up.sql
  - migrations/0019_rules_by_type.up.sql
  - workers/internal/api/handlers/distribution_rules.go
  - workers/internal/catalog/distribution_rules.go
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
- `material_id` — qual material a regra cobre
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

## Editabilidade

Secao 8 da spec: edicao livre so do **futuro**.

- Regras com `start_date >= hoje`: criar/editar/excluir livremente
- Regras com `start_date < hoje`: somente encurtar `end_date` pra `hoje`
- Overrides em datas passadas: read-only

Validacao atualmente NAO esta implementada no backend — fica a criterio do frontend. Ver follow-up F-91.

## Re-categorizacao

Quando uma regra e criada/editada/excluida via API, o handler `DistributionRulesHandler` dispara `RecategorizeForRule` (ou `RecategorizeForCampaign` no caso de delete) em goroutine com timeout de 30s. Detections existentes no escopo da regra sao atualizadas conforme a nova logica.

A operacao roda inteira em SQL via `recategorizeScope` em `catalog/distribution_rules.go` — sem N+1 queries. Para uma campanha inteira leva milissegundos mesmo com centenas de milhares de detections.

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
