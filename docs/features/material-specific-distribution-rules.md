---
status: implementado
ultima-verificacao: 2026-06-29
codigo-relacionado:
  - migrations/0043_rule_material_scope.up.sql
  - workers/internal/categorizer/categorizer.go
  - workers/internal/catalog/distribution_rules.go
  - workers/internal/catalog/detections.go
  - workers/internal/api/handlers/distribution_rules.go
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
| Fora do período/dia programado dele (dentro da campanha) | `out_date` 🔴 |

Material sem regra específica usa as regras gerais, inalterado (`orphan` para
tocadas sem regra). Override por tipo continua vencendo a célula no dia
([override-time-window](override-time-window.md)).

## Exibição

Nada muda no grid/resumo (emissora × tipo × dia). Só `detections.category` fica
mais fina — desvios do material específico aparecem roxos/amarelos na própria
célula do tipo.

## Onde fica

Step 5 do wizard ([campaign-wizard](campaign-wizard.md)): no painel "Nova regra",
com 1 tipo selecionado aparece o seletor opcional "Materiais específicos".
