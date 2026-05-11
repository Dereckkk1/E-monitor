# Wizard de Campanha — Guia Operacional

Documenta o fluxo de 4 etapas pra criar/editar campanha. Implementado pelo Plano 2.

> Spec: [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](superpowers/specs/2026-05-11-campaign-wizard-design.md)
> Plano: [`docs/superpowers/plans/2026-05-11-plano-2-wizard-frontend.md`](superpowers/plans/2026-05-11-plano-2-wizard-frontend.md)

## Quando usar

- **Criar** uma campanha nova: `/campaigns/new`
- **Editar** uma campanha existente: `/campaigns/:id/edit` (botão "Editar" em cada linha de `/campaigns`)

## As 4 etapas

### 1. Dados básicos
- Nome (livre), cliente (dropdown), data início, data fim
- Cliente não pode ser alterado depois de criar a campanha (a biblioteca de materiais é por cliente)
- Salva a campanha imediatamente ao clicar "Avançar"; redireciona pra `/campaigns/:id/edit`

### 2. Emissoras
- Busca textual + filtro por banda (AM/FM)
- Toggle checkbox por linha
- Lista de selecionadas em chips no topo (com X pra remover)
- Salva automaticamente após 600ms de inatividade

### 3. Materiais
- Cards de materiais já vinculados ao topo
- Botão "+ Adicionar material" abre side panel com 2 abas:
  - **Da biblioteca**: lista materiais já existentes do cliente; multi-check pra vincular
  - **Subir novo**: file picker; preencher título e tipo
- Vinculação default = todas as emissoras da campanha
- Inline: dropdown de tipo pra reclassificar; botão de excluir (desvincula, não deleta da biblioteca)

### 4. Distribuição
- Grade emissora × material × dia, com toolbar mês-navegador
- Botão "+ Regra" abre side panel pra criar uma `distribution_rule`
- Click em célula abre popover de override (laranja)
- Botão "Concluir campanha" finaliza e volta pra `/campaigns`

## Edição de campanha existente

A rota `/campaigns/:id/edit` carrega a campanha pelo ID e abre o wizard com todos os steps marcados como completos. Permite mexer em qualquer etapa, sem auto-avançar.

**Limitações** (validação atualmente só no frontend — backend não bloqueia):
- Cliente não pode mudar
- Regras com `start_date < hoje` não devem ser editadas (ver F-91)

## Tipos de material

Gerenciado em `/material-types`. Globais (não por cliente). Cores hex usadas pra colorir a barra vertical das sub-linhas da grade.

## Limitações conhecidas

- Sem teste automatizado (depende de smoke manual)
- `probeDuration()` no backend retorna 30s stub — duração de áudio fica incorreta até F-88 ser resolvido
- Validação "future-only edit" só no frontend (F-91)
- `daily_play_summary` view não é materializada — performance pode degradar com volume (F-84)
