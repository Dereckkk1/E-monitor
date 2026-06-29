---
status: parcialmente-implementado
ultima-verificacao: 2026-06-18
codigo-relacionado:
  - frontend/src/pages/CampaignWizardPage.jsx
  - frontend/src/pages/CampaignWizardSteps/
  - frontend/src/pages/CampaignWizardSteps/ConnectionStep.jsx
  - frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx
  - frontend/src/utils/search.js
  - workers/internal/api/handlers/materials.go
  - migrations/0019_rules_by_type.up.sql
  - migrations/0022_pricing.up.sql
  - migrations/0026_material_script.up.sql
  # limitacoes: probeDuration stub 30s (F-88); validacao future-only edit so frontend (F-91)
---

# Wizard de Campanha — Guia Operacional

Documenta o fluxo de 6 etapas pra criar/editar campanha. Base implementada pelo
Plano 2 (4 etapas); Preços e Conexão entraram depois — Conexão é o Step 3
([campaign-connection-step](campaign-connection-step.md)).

> Spec: [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](../superpowers/specs/2026-05-11-campaign-wizard-design.md)
> Plano: [`docs/superpowers/plans/2026-05-11-plano-2-wizard-frontend.md`](../superpowers/plans/2026-05-11-plano-2-wizard-frontend.md)

## Quando usar

- **Criar** uma campanha nova: `/campaigns/new`
- **Editar** uma campanha existente: `/campaigns/:id/edit` (botão "Editar" em cada linha de `/campaigns`)

## As 6 etapas

### 1. Dados básicos
- Nome (livre), cliente (dropdown), data início, data fim
- Cliente não pode ser alterado depois de criar a campanha (a biblioteca de materiais é por cliente)
- Salva a campanha imediatamente ao clicar "Avançar"; redireciona pra `/campaigns/:id/edit`

### 2. Emissoras
- Busca textual + filtro por banda (AM/FM)
- Toggle checkbox por linha
- Lista de selecionadas em chips no topo (com X pra remover)
- Salva automaticamente após 600ms de inatividade

### 3. Conexão
- Lista as emissoras da campanha com testes de conexão por linha (ping / stream
  / worker) e troca inline da `stream_url`. Tudo efêmero (não sobe worker
  permanente). Detalhes: [campaign-connection-step](campaign-connection-step.md).
- Não bloqueia avançar (etapa diagnóstica).

### 4. Materiais
- Cards de materiais já vinculados ao topo
- Botão "+ Adicionar material" abre side panel com 2 abas:
  - **Da biblioteca**: lista materiais já existentes do cliente; multi-check pra vincular
  - **Subir novo**: file picker; preencher título e tipo
- Vinculação default = todas as emissoras da campanha
- Inline: dropdown de tipo pra reclassificar; botão de excluir (desvincula, não deleta da biblioteca)

### 5. Distribuição
- Grade emissora × material × dia, com toolbar mês-navegador
- Busca de emissora acima do grid (nome, dial/frequência, cidade, UF ou band, em qualquer ordem). Filtra **só no front** — mesmo helper `tokenize`/`matchesAllTokens` de `/detections` e `/materials` (token-AND, campo-OR). Sem busca, o grid mostra tudo; sem resultado, exibe estado vazio com "Limpar busca".
- Botão "+ Regra" abre side panel pra criar uma `distribution_rule`
  - Painel "Nova regra" com 1 tipo selecionado mostra o seletor opcional **Materiais específicos** — escopa a regra a materiais individuais (carve-out). Ver [material-specific-distribution-rules](material-specific-distribution-rules.md).
- Click em célula abre popover de override (laranja)

### 6. Preços
- Valor por emissora/tipo (investimento da campanha)
- Botão "Concluir campanha" finaliza e volta pra `/campaigns`

## Edição de campanha existente

A rota `/campaigns/:id/edit` carrega a campanha pelo ID e abre o wizard com todos os steps marcados como completos. Permite mexer em qualquer etapa, sem auto-avançar.

**Limitações** (validação atualmente só no frontend — backend não bloqueia):
- Cliente não pode mudar
- Regras com `start_date < hoje` não devem ser editadas (ver F-91)

## Planejamento sem material (distribuição "fantasma")

Campanhas costumam ser planejadas antes dos áudios chegarem. O wizard suporta esse fluxo (spec [2026-05-25](../superpowers/specs/2026-05-25-distribution-without-materials-design.md)):

1. **Step 4 (Materiais) é opcional.** Quando não há material linkado, o empty state mostra um link "Pular por enquanto" e o footer renomeia "Avançar →" para "Pular materiais →". Materiais mal configurados (sem tipo ou sem estação) continuam bloqueando.

2. **Step 5 (Distribuição) aceita regras sem material.** O `RuleSidePanel` lista todos os tipos globais e todas as emissoras da campanha. A regra é por `type_id` (migration 0019), não exige material.

3. **Linhas "fantasma" no grid.** `(station, type)` que existe só por causa de uma regra (sem material desse tipo linkado à estação) aparece com `TypeIconPill` em opacidade reduzida, label em cor secundária e sufixo "· aguardando áudio". Quando o material chega, a linha vira normal.

4. **Banner âmbar.** O Step 5 mostra um banner no topo listando os tipos sem áudio: "Você planejou X regras sem áudio vinculado. Quando subir um material do tipo Y, ele começa a ser contado automaticamente."

5. **Pricing aceita tipos vindos de regras.** No Step 6, `typesInScope` é união de tipos de materiais E tipos cobertos por regras — operador consegue cadastrar `unit_value` por tipo antes do áudio chegar.

6. **Chip no listing.** `/campaigns` mostra chip âmbar "sem material" pra campanhas com `material_count === 0` (campo novo no `ListPaged`), como recall visual.

Quando o material é subido depois (no Step 4 da edição), ele é vinculado automaticamente a todas as emissoras da campanha — comportamento atual, inalterado. As regras já existentes do mesmo tipo começam a contar detections imediatamente, sem ação extra.

## Tipos de material

Gerenciado em `/material-types`. Globais (não por cliente). Cores hex usadas pra colorir a barra vertical das sub-linhas da grade.

## Limitações conhecidas

- Sem teste automatizado (depende de smoke manual)
- `probeDuration()` no backend retorna 30s stub — duração de áudio fica incorreta até F-88 ser resolvido
- Validação "future-only edit" só no frontend (F-91)
- `daily_play_summary` view não é materializada — performance pode degradar com volume (F-84)
