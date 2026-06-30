---
status: implementado
ultima-verificacao: 2026-06-30
codigo-relacionado:
  - migrations/0045_distribution_rule_name.up.sql
  - workers/internal/catalog/distribution_rules.go
  - workers/internal/api/handlers/distribution_rules.go
  - frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx
  - frontend/src/components/RuleSidePanel.jsx
---

# Lista escaneável de regras + nome opcional do conjunto

Melhoria da Step 5 (Distribuição) do wizard de campanha. Quando uma campanha tem
muitas regras, a lista antiga (pílulas idênticas mostrando só tipo · horário ·
plays/dia · escopo) ficava impossível de varrer — as dimensões que **distinguem**
as regras (emissoras, período, dias da semana) estavam escondidas.

## O que mudou

**1. Nome opcional do conjunto (`distribution_rules.name`, migration 0045).**
Coluna `TEXT NOT NULL DEFAULT ''` (vazio = sem nome; sem NULL no Go). O editor de
regra ([RuleSidePanel](../../frontend/src/components/RuleSidePanel.jsx)) ganhou o
campo "Nome do conjunto (opcional)". Quando preenchido (ex.: "Rede Nova Brasil"),
vira o **título** da regra na lista — útil quando a regra cobre muitas emissoras e
listar nomes seria ruído. Passa pela API como `name` (passthrough no
`rulePayload`/`toInput`, trim no backend).

**2. Lista escaneável + busca** (`RuleList` em
[DistributionStep.jsx](../../frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx)).
Substitui o `RuleChipList`. Cada regra é uma linha (divisórias finas, sem cards
aninhados) com a **assinatura** completa:

- **Título**: nome do conjunto se houver, senão o nome do tipo (com selo do tipo).
- **Subtítulo**: `faixa horária · dias da semana · período` (período "Todo o
  período" quando cobre a campanha inteira; dias com atalhos "Todos os dias",
  "Seg a Sex", "Fim de semana").
- **Rodapé**: `N emissoras: nome1, nome2 +K · escopo` (lista completa no `title`;
  escopo = "todos do tipo" / "N materiais").
- **Badge** de `N×/dia` à direita, tingido pela cor do tipo.

Um **campo de busca** no topo filtra por nome, tipo, emissora, período, faixa e
dias (tokens AND, via `tokenize`/`matchesAllTokens` — mesmo util de /detections e
/materials). O contador vira "X de N" ao filtrar. Lista rola quando passa de ~7
regras. Clicar numa linha abre o editor pré-preenchido.

## Decisão de design

Surfacing das dimensões distintivas (emissoras/período/dias) + busca resolve o
"achar entre muitas" na raiz; o nome é a camada de identidade pra quem tem
**muitas** emissoras por regra. Reusa os tokens `--c-*` e os padrões inline do
componente; segue o design system E-radios (Clean Light, Rosa Digital no foco da
busca, sem cards aninhados).

## Relacionado

- [distribution-rules.md](../architecture/distribution-rules.md) — semântica das regras
- [material-specific-distribution-rules.md](material-specific-distribution-rules.md) — carve-out por material
- [campaign-wizard.md](campaign-wizard.md) — wizard (Step 5)
- [broadcaster-search.md](broadcaster-search.md) — o util de busca por tokens reusado aqui
