---
status: implementado
ultima-verificacao: 2026-07-13
codigo-relacionado:
  - migrations/0043_rule_material_scope.up.sql
  - workers/internal/categorizer/categorizer.go
  - workers/internal/catalog/distribution_rules.go
  - workers/internal/catalog/detections.go
  - workers/internal/api/handlers/distribution_rules.go
  - workers/cmd/backfill-recategorize/main.go
  - frontend/src/components/RuleSidePanel.jsx
  - frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx
---

# Regras de distribuição por material específico

Uma `distribution_rule` pode ser escopada a materiais específicos dentro de um
tipo via `material_ids UUID[]` (vazio = todos do tipo, como na migration 0019).

## Carve-out (precedência)

Material nomeado em ≥1 regra específica é julgado **só** por essas regras
(as gerais do tipo deixam de valer pra ele), por emissora:

| Tocada | Categoria |
|---|---|
| No período + dia + faixa da regra dele | `in_slot` 🟢 |
| No período + dia, fora da faixa | `out_slot` 🟡 |
| No período (range de datas) dele, mas em dia-da-semana sem regra (ex.: sábado, regra seg-sex) | `orphan` 🔵 → conta como **bônus** |
| Fora do período das regras dele (antes/depois da vigência, dentro da campanha) | `out_date` 🔴 |

Material sem regra específica usa as regras gerais, inalterado (`orphan` para
tocadas sem regra). Override por tipo continua vencendo a célula no dia
([override-time-window](override-time-window.md)).

> **Dia extra dentro do período = bônus (spec 2026-07-13).** `out_date` fica
> reservado a tocadas fora do range de datas das regras do material. Uma tocada
> dentro desse range mas num dia-da-semana sem regra é uma entrega extra dentro
> do período contratado → `orphan`, que a view `daily_play_summary` soma no bônus
> (`bonus = GREATEST(0, in_slot − expected) + orphan`). A regra vale nos dois
> caminhos que categorizam — insert `categorizer.Categorize` e recat
> `recatClassifyTailSQL` (sincronizados; teste de paridade Go×SQL em
> `distribution_rules_carveout_test.go`). Backfill histórico:
> `cmd/backfill-recategorize` — o dry-run (default) reporta os **totais atuais**
> de `out_date`/`orphan` das campanhas com carve-out (não um delta previsto); o
> delta real só aparece rodando `--apply` contra um **clone** do dump de prod
> (§4.8), que é o passo obrigatório antes de aplicar em prod.

## Exibição

Nada muda no grid/resumo (emissora × tipo × dia). Só `detections.category` fica
mais fina — desvios do material específico aparecem roxos/amarelos/azuis na
própria célula do tipo (`out_date` roxo, `out_slot` amarelo, o dia-extra-bônus
azul de `orphan`).

## Onde fica

Step 5 do wizard ([campaign-wizard](campaign-wizard.md)): no painel "Nova regra",
com 1 tipo selecionado aparece o seletor opcional "Materiais específicos".
