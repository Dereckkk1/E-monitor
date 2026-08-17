---
status: implementado
ultima-verificacao: 2026-08-17
codigo-relacionado:
  - migrations/0043_rule_material_scope.up.sql
  - migrations/0065_quota_aware_summary.up.sql
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
| No período + dia + faixa da regra dele, **com vaga na meta do dia** | `in_slot` 🟢 |
| No período + dia + faixa, mas a meta do dia já fechou | `bonus` 🔵 |
| No período + dia, fora da faixa, **enquanto a meta não fechou dentro dela** | `out_slot` 🟡 |
| No período + dia, fora da faixa, depois da meta fechada | `bonus` 🔵 |
| No período (range de datas) dele, mas em dia-da-semana sem regra (ex.: sábado, regra seg-sex) | `bonus` 🔵 |
| Fora do período das regras dele (antes/depois da vigência, dentro da campanha) | `out_date` 🔴 |

Material sem regra específica usa as regras gerais, inalterado (`bonus` para
tocadas sem meta no dia). Override por tipo continua vencendo a célula no dia
([override-time-window](override-time-window.md)) — e a meta do dia passa a ser a
do override.

> **O que o carve-out contribui pra regra de cota (2026-08-17):** a lista de
> materiais afeta **só o teste "dentro da faixa"** — material nomeado é julgado
> pelas faixas das regras que o nomeiam, material comum pelas gerais. A **meta N
> do dia NÃO é filtrada por material**: é a soma de `plays_per_day` de todas as
> regras da célula que valem naquele dia. E `out_date` de carve-out **precede o
> override** (D6): material fora do período das regras dele continua `out_date`
> mesmo numa célula com meta zerada — antes o override mascarava esse caminho.
> Regra completa: [quota-aware-categorization.md](quota-aware-categorization.md).

> **Dia extra dentro do período = bônus (spec 2026-07-13).** `out_date` fica
> reservado a tocadas fora do range de datas das regras do material. Uma tocada
> dentro desse range mas num dia-da-semana sem regra é uma entrega extra dentro
> do período contratado → hoje `bonus` explícito (era `orphan`, renomeado pela
> migration 0064; a view conta a categoria direto desde a 0065). A regra vale nos
> dois motores — `categorizer.Settle` (insert) e `recatClassifiedCTE` (recat) —,
> com paridade travada por `settle_parity_test.go` e
> `distribution_rules_carveout_test.go`. Backfill histórico:
> `cmd/backfill-recategorize` — o dry-run (default) reporta os totais atuais de
> **todas** as categorias (in_slot/out_slot/bonus/orphan/out_date), não só as duas
> antigas; o delta real só aparece rodando `--apply` contra um **clone** do dump
> de prod (§4.8), que é o passo obrigatório antes de aplicar em prod.

## Exibição

Nada muda no grid/resumo (emissora × tipo × dia). Só `detections.category` fica
mais fina — desvios do material específico aparecem roxos/amarelos/azuis na
própria célula do tipo (`out_date` roxo, `out_slot` amarelo, o dia-extra-bônus
azul de `bonus`).

## Onde fica

Step 5 do wizard ([campaign-wizard](campaign-wizard.md)): no painel "Nova regra",
com 1 tipo selecionado aparece o seletor opcional "Materiais específicos".
