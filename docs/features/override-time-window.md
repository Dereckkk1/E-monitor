---
status: implementado
ultima-verificacao: 2026-05-19
codigo-relacionado:
  - migrations/0031_override_time_window.up.sql
  - workers/internal/catalog/distribution_overrides.go
  - workers/internal/api/handlers/distribution_overrides.go
  - workers/internal/categorizer/categorizer.go
  - workers/internal/catalog/detections.go
  - frontend/src/components/OverridePopover.jsx
  - frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx
  - frontend/src/components/DayCell.jsx
  - frontend/src/components/DistributionGrid.jsx
---

# Faixa horária em overrides de distribuição

## O que faz

No step "Distribuição" do wizard de campanha (`/campaigns/:id/edit`),
o operador pode ajustar manualmente o número de veiculações esperadas
em qualquer célula `(emissora, tipo, dia)` clicando na célula ou usando
os botões `+`/`−`. A partir desta entrega, cada ajuste **também carrega
uma faixa horária**, gravada em `distribution_overrides.time_start` e
`time_end`.

## Por que existe

Sem a faixa horária no override, o categorizador classificava como
`orphan` toda detection numa célula que tinha override mas nenhuma
rule — não havia janela de tempo conhecida. E mesmo em células com
rule + override, o "+1 manual" usava a faixa da rule por arrasto, sem
flexibilidade pro operador dizer "a inserção extra vai às 19h".

## Comportamento

### Default da faixa no popover

Ao abrir o `OverridePopover` numa célula, o campo de faixa vem
pré-preenchido (primeira condição que casar):

1. Override existente → faixa do override.
2. Apenas 1 rule aplicável → faixa dessa rule.
3. Mais de 1 rule aplicável → **sem pré-preenchimento**, alerta amarelo
   com chips clicáveis pra cada faixa existente.
4. Nenhuma rule, mas há `lastUsedWindow` (última faixa usada na sessão)
   → faixa anterior.
5. Fallback: `06:00–22:00`.

### Inline +/-

Os botões `+`/`−` na célula só fazem staging direto quando a célula
tem ou (a) uma única rule aplicável, ou (b) um override existente. Em
células ambíguas (multi-rule sem override) ou "limpas" (sem rule e sem
override), o clique abre o popover pra forçar escolha explícita de
faixa.

### count=0

Override `plays_expected=0` mantém faixa NOT NULL no banco (decisão de
schema, simplifica CHECK), mas o categorizador trata o caso como
"qualquer detection naquele dia → `out_slot`", ignorando a faixa. O
frontend desabilita os inputs de faixa quando count=0 pra deixar claro
que ela é inerte.

### Replicação

Checkbox no popover: "Aplicar essa mesma faixa nas demais células deste
tipo neste mês". Quando marcado, ao aplicar, a faixa é replicada em
**todos os overrides existentes** do mesmo `type_id` no mês visível,
preservando o `plays_expected` de cada um. **Não cria override novo**
em células limpas.

### Categorizador

Quando há override numa célula+dia, o categorizador **ignora as rules
daquela célula naquele dia** — a faixa do override é a única
considerada pra decidir `in_slot`/`out_slot`. Mantém a tolerância
existente de 15 min em cada extremo.

Tabela completa de decisão (cobre todos os caminhos do
[categorizer.go](../../workers/internal/categorizer/categorizer.go)):

| Caso | Resultado |
|------|-----------|
| `detected_at` fora de `[campaign.start, campaign.end]` | `out_date` |
| Override existe, `count=0` | `out_slot` |
| Override existe, `count>0`, detection ∈ `[ts-15min, te+15min]` | `in_slot` |
| Override existe, `count>0`, detection fora da faixa | `out_slot` |
| Sem override, nenhuma rule aplicável | `orphan` |
| Sem override, rule aplicável, detection ∈ alguma faixa tolerada | `in_slot` |
| Sem override, rule aplicável, detection fora de toda faixa | `out_slot` |

## Fora de escopo

- Múltiplas faixas por célula (override com lista de slots) — escolha
  consciente, modelo "uma faixa por célula" (override REPLACE).
- Recategorização retroativa de detections já gravadas — mudança de
  override afeta apenas detections **futuras**. Mesmo princípio das rules.
- Modo "pintar" / seleção múltipla de células.

## Como testar manualmente

1. Abre uma campanha existente em `/campaigns/<id>/edit`, vai pro
   step "Distribuição".
2. Cria uma rule "Spot 30s, 08–10h, 2×/dia, seg-sex, todas as estações".
3. Clica numa célula da grid pra abrir o popover. A faixa vem
   pré-preenchida 08:00–10:00.
4. Muda pra 14:00–16:00, mantém count=3, aplica.
5. Cria outra rule "Spot 30s, 12–14h, 1×/dia" na mesma estação.
6. Clica numa célula ainda sem override — popover mostra alerta com 2
   chips (08–10h e 12–14h).
7. Marca o checkbox de replicação, aplica → todos os overrides
   existentes do tipo no mês mudam pra mesma faixa.

## Migration

[`migrations/0031_override_time_window`](../../migrations/0031_override_time_window.up.sql)
— adiciona colunas em duas fases (nullable → backfill → NOT NULL).
Backfill usa a faixa da rule mais antiga aplicável à célula+dia;
fallback `06:00–22:00` quando não há rule. Tudo em transaction — risco
de perda de dado: zero.

## Spec original

[docs/superpowers/specs/2026-05-19-override-time-window-design.md](../superpowers/specs/2026-05-19-override-time-window-design.md)
