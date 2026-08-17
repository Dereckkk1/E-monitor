---
status: implementado
ultima-verificacao: 2026-08-17
codigo-relacionado:
  - frontend/src/components/DayDetailModal.jsx
  - workers/internal/categorizer/categorizer.go
  - migrations/0029_daily_summary_exclude_audit_rejected.up.sql
  - migrations/0043_rule_material_scope.up.sql
  - migrations/0065_quota_aware_summary.up.sql
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

## Atribuição por faixa — apresentação, nunca veredito

A categoria gravada em `detections.category` (`in_slot`/`out_slot`/`out_date`/
`bonus`) é **a verdade** — o bloco não recategoriza nada e, desde 2026-08-17, não
tenta mais reproduzir a regra.

**A meta é da célula-dia inteira, não por faixa.** O backend fecha as N primeiras
tocadas dentro de **qualquer** janela válida, em ordem cronológica global
([quota-aware-categorization.md](quota-aware-categorization.md)). O cliente não tem
como reconstruir essa ordem — ele recebe `/detections` paginado —, então o
`buildDayPlan` faz só o que é honesto fazer: reparte, **entre as tocadas que o
backend já rotulou `in_slot`**, qual faixa credita cada uma, pra desenhar a barra
de progresso.

- cada `in_slot` é creditado a **uma** faixa (janela tolerante ±15 min =
  `SlotToleranceSeconds`; empate → janela mais curta, depois começo mais cedo);
- respeita o **carve-out**: material nomeado em regra específica é creditado só
  às faixas específicas dele; os demais, só às gerais;
- `in_slot` que não casa nenhuma janela **atual** (regra editada depois da
  categorização) vira a nota `+K tocou … fora das janelas atuais`.

A soma por faixa pode, em teoria, divergir do `in_slot` autoritativo (fontes
diferentes: aqui é a lista paginada, lá é a `daily_play_summary`). Quando divergir,
o header "esperado" e a tira de saldo usam **sempre** o autoritativo — de propósito,
não reconciliamos em silêncio.

Horário da tocada é calculado em `America/Sao_Paulo` (`Intl.DateTimeFormat`),
o mesmo fuso do categorizador e da view — independe do fuso do browser.

## Notas de rodapé do bloco

| Situação | O que o bloco diz |
|---|---|
| `out_slot > 0` | "N tocou fora da faixa {janela} ({origem}) — conta como fora do prazo **enquanto a meta não fecha**. Tolerância de 15 min já considerada." |
| Ajuste do dia com `plays_expected = 0` e bonificação | "N tocou, mas o ajuste do dia zerou a meta — toda tocada conta como **bonificação**, não como fora do prazo." (azul) |
| `in_slot` fora das janelas atuais | "+K tocou na faixa, mas fora das janelas atuais (regra editada depois)." |

A segunda linha era, até esta entrega, o texto errado que originou a investigação
(campanha 270, Band Vale FM 102.9): dizia que meta zerada fazia toda tocada contar
"como fora do prazo". Com N=0, `out_slot` é inalcançável — a tocada é sempre
bonificação.

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
   veiculação. Desde a migration 0065 essa fórmula é `deficit = max(0, expected −
   in_slot)` (sem abater `out_slot`) e `bonus` = contagem direta da categoria —
   `'orphan'` é aceito como sinônimo de `'bonus'` na leitura, pra bonificação nunca
   sumir por causa de um rótulo gravado pelo binário antigo.

## Override

Se `esperado ≠ Σ(alvos das faixas do dia)`, há provavelmente um
[override de célula](override-time-window.md) sobrepondo a regra: mostra a nota
honesta em vez de forçar um breakdown falso.

## Fora de escopo

- Não altera backend, schema, nem a categorização (só apresentação + derivação
  read-only no cliente).
- Não mexe na lista de detecções abaixo (segue agrupada pela categoria
  autoritativa — o grupo azul agora se chama "Bonificação (sem meta)").
- Não reconstrói a cota do dia no cliente: quem fecha a célula-dia é o backend.

## Links

- [quota-aware-categorization.md](quota-aware-categorization.md) — a regra que decide as categorias
- [override-time-window.md](override-time-window.md) — a faixa "ajuste do dia"
- [distribution-rules.md](../architecture/distribution-rules.md) — regras e gatilhos de recategorização
