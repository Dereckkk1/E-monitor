---
status: implementado
ultima-verificacao: 2026-08-17
codigo-relacionado:
  - migrations/0031_override_time_window.up.sql
  - migrations/0065_quota_aware_summary.up.sql
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

> **Atenção (2026-08-17).** A regra de categorização mudou: o veredito de uma
> tocada não sai mais de uma decisão isolada, e sim do **fechamento por cota da
> célula-dia** — ver [quota-aware-categorization.md](quota-aware-categorization.md),
> que é o documento canônico. Este doc continua correto sobre **como a faixa do
> override é editada, replicada e usada** (ela segue superseding as regras do dia).
> As seções §count=0 e §Categorizador foram reescritas; o que este doc dizia sobre
> elas antes descrevia o modelo antigo.

## O que faz

No step "Distribuição" do wizard de campanha (`/campaigns/:id/edit`),
o operador pode ajustar manualmente o número de veiculações esperadas
em qualquer célula `(emissora, tipo, dia)` clicando na célula ou usando
os botões `+`/`−`. A partir desta entrega, cada ajuste **também carrega
uma faixa horária**, gravada em `distribution_overrides.time_start` e
`time_end`.

## Por que existe

Sem a faixa horária no override, o categorizador classificava como
`orphan` (hoje `bonus`) toda detection numa célula que tinha override
mas nenhuma rule — não havia janela de tempo conhecida. E mesmo em células com
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

### count=0 — a célula zerada

Override `plays_expected=0` mantém faixa NOT NULL no banco (decisão de
schema, simplifica CHECK). O frontend desabilita os inputs de faixa quando
count=0 pra deixar claro que ela é inerte.

**O que acontece com as tocadas desse dia (desde 2026-08-17):** `plays_expected=0`
é simplesmente **meta do dia N = 0**. Pela regra de cota, `out_slot` só existe
enquanto `in_slot < N` — com N=0 isso é sempre falso, então **toda tocada do dia
vira `bonus`** (bonificação), independentemente da faixa. Não há mais ramo
especial pra `plays_expected = 0` em nenhum dos dois motores.

> **Era exatamente aqui que estava o bug** que motivou a mudança (campanha 270,
> Band Vale FM 102.9, 04/08/2026): o categorizador devolvia `out_slot` ignorando
> a faixa gravada, e a bonificação da emissora era lida como inadimplência. O
> `DayDetailModal` mostra hoje a nota azul *"o ajuste do dia zerou a meta — toda
> tocada conta como bonificação, não como fora do prazo"*.
>
> Ressalva que continua valendo: **`out_date` precede tudo** (D6). Material
> carve-out tocando fora do período das regras que o nomeiam continua `out_date`
> mesmo numa célula zerada — a meta 0 não mascara mais esse caminho.

### Replicação

Checkbox no popover: "Aplicar essa mesma faixa nas demais células deste
tipo neste mês". Quando marcado, ao aplicar, a faixa é replicada em
**todos os overrides existentes** do mesmo `type_id` no mês visível,
preservando o `plays_expected` de cada um. **Não cria override novo**
em células limpas.

### Categorizador

Quando há override numa célula+dia, o fechamento **ignora as rules daquela célula
naquele dia**: a faixa do override é a única considerada no teste "dentro da
faixa", e `plays_expected` é a meta N do dia. Mantém a tolerância de 15 min em
cada extremo (±900 s), idêntica nos dois motores.

O papel do override na regra de 5 passos
([quota-aware-categorization.md](quota-aware-categorization.md)):

| Passo | Com override na célula+dia |
|---|---|
| 0 — `out_date` | inalterado: precede tudo, **o override não mascara** (D6) |
| 1 — meta N do dia | `override.plays_expected` (as rules do dia não são somadas) |
| 2 — "dentro da faixa" | só a faixa do override, ±15 min |
| 3 — dentro da faixa | as N primeiras (cronológicas) → `in_slot`; o resto → `bonus` |
| 4 — fora da faixa | `out_slot` enquanto `in_slot < N`; depois → `bonus` |
| 5 — déficit | `max(0, N − in_slot)` — `out_slot` **não** abate |

Casos que costumam ser consultados:

| Caso | Resultado |
|------|-----------|
| `detected_at` fora de `[campaign.start, campaign.end]` | `out_date` |
| Override `count=0`, qualquer horário | **`bonus`** (era `out_slot` antes de 2026-08-17) |
| Override `count=N>0`, dentro da faixa, ainda há vaga | `in_slot` |
| Override `count=N>0`, dentro da faixa, meta já cheia | **`bonus`** |
| Override `count=N>0`, fora da faixa, meta ainda aberta | `out_slot` |
| Override `count=N>0`, fora da faixa, meta já fechada dentro da faixa | **`bonus`** |
| Sem override, nenhuma rule aplicável no dia (N=0) | **`bonus`** (era `orphan`, mesmo conceito, nome novo) |

## Fora de escopo

- Múltiplas faixas por célula (override com lista de slots) — escolha
  consciente, modelo "uma faixa por célula" (override REPLACE).
- Cota **por faixa**: a meta é do dia (D1 da spec 2026-08-14). A faixa do
  override é um teste booleano por tocada, não um balde com cota própria.
- Modo "pintar" / seleção múltipla de células.

> **Desatualizado desde 2026-07-03:** este doc dizia que mudar um override não
> recategorizava o passado. Não é mais verdade — `PUT`/`DELETE` de override
> disparam `RecategorizeForOverride`, que hoje **refecha a célula-dia inteira**
> (ver [distribution-rules.md](../architecture/distribution-rules.md)).

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
