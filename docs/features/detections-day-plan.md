---
status: implementado
ultima-verificacao: 2026-07-01
codigo-relacionado:
  - frontend/src/components/DayDetailModal.jsx
  - workers/internal/categorizer/categorizer.go
  - migrations/0029_daily_summary_exclude_audit_rejected.up.sql
  - migrations/0043_rule_material_scope.up.sql
---

# Plano do dia — bloco de faixas na DayDetailModal

Substitui, no topo da `DayDetailModal` (aberta ao clicar numa célula da grade
`/detections`), a antiga **fileira achatada de chips de faixa** + o **box de
resumo** por um bloco único **"Plano do dia"**.

## Problema que resolve

Os chips antigos listavam **todas** as regras do par `(tipo, emissora)`,
independentemente de valerem naquela data. No exemplo real que motivou a
mudança (sábado 27/06), apareciam três chips — dois `09:00–23:59` quase
idênticos (4×/dia e 8×/dia) — sem dizer qual valia no dia, quanto cada um
esperava, nem quanto tocou em cada. A 3ª faixa (`8×`) era seg–sex e nem se
aplicava no sábado. Resultado: "não dá pra entender qual regra é pra quê".

## O que o bloco mostra

Por faixa que **vale naquela data** (uma linha compacta cada):

- **Janela** (`08:50–10:00`), **barra segmentada** de progresso (um bloco por
  tocada esperada; verde = tocou, cinza = falta) e **tocou/alvo** (`3/8`), com
  semáforo na contagem: verde cumpriu, âmbar parcial, **vermelho** não tocou
  nada (déficit).
- **Pílula de escopo** quando a faixa é escopada a material específico
  (carve-out `material_ids[]`, ver [material-specific-distribution-rules](material-specific-distribution-rules.md)):
  mostra o(s) nome(s) do(s) material(is) — é a info que importa numa faixa
  escopada, então o texto tem prioridade sobre a barra.
- **Header** reconcilia com o `esperado` autoritativo da célula.
- **Rodapé recolhido** `▾ N faixas não valem neste {dia}` lista as regras que
  existem mas não valem hoje, com o motivo (`só seg a sex`, `fora do período`,
  `começa DD/MM`). É o que desfaz a sensação de "faixas duplicadas".
- **Tira de saldo** com o não-zero (`tocou · faltou · fora da faixa · fora da
  data · bônus`).

## Atribuição por faixa (honesta)

A categoria gravada em `detections.category` (`in_slot`/`out_slot`/…) continua
**a verdade** — o bloco não recategoriza nada. A contagem "tocou dentro desta
faixa" é derivada no cliente espelhando o `categorizer.go`:

- cada `in_slot` é creditado a **uma** faixa (janela tolerante ±15 min =
  `SlotToleranceSeconds`; empate → janela mais curta, depois começo mais cedo),
  então a soma nunca estoura o total autoritativo;
- respeita o **carve-out**: material nomeado em regra específica é creditado só
  às faixas específicas dele; os demais, só às gerais;
- `in_slot` que não casa nenhuma janela **atual** (regra editada depois da
  categorização) vira a nota `+K tocou … fora das janelas atuais`.

Horário da tocada é calculado em `America/Sao_Paulo` (`Intl.DateTimeFormat`),
o mesmo fuso do categorizador e da view — independe do fuso do browser.

## Robustez (nunca some, nunca corta)

Dois bugs corrigidos na entrega, ambos de layout/dados:

1. **`flexShrink: 0` na `<section>` é obrigatório.** A `.modal-body` é um flex
   column com altura limitada (`overflowY: auto`). Num flex item com
   `overflow: hidden`, o `min-height: auto` colapsa pra `0` — sem o
   `flex-shrink: 0`, o flex **comprime e corta** o bloco quando a lista de
   detecções é longa (era o "some"/"cortando").
2. **Saldo derivado das detecções** quando a célula não trouxe `cellSummary`
   (ex.: regra removida/editada depois da tocada, deixando `in_slot` congelado
   sem linha na view). `deriveSummary` recalcula o saldo pela mesma fórmula da
   `daily_play_summary`, garantindo que o bloco apareça sempre que houver
   veiculação.

## Override

Se `esperado ≠ Σ(alvos das faixas do dia)`, há provavelmente um
[override de célula](override-time-window.md) sobrepondo a regra: mostra a nota
honesta em vez de forçar um breakdown falso.

## Fora de escopo

- Não altera backend, schema, nem a categorização (só apresentação + derivação
  read-only no cliente).
- Não mexe na lista de detecções abaixo (segue agrupada pela categoria
  autoritativa).
