> ⚠️ **REGISTRO HISTÓRICO — não descreve o comportamento atual.**
> Este documento é um snapshot datado da sessão de design/implementação que o gerou.
> Em **2026-08-17** a categorização de veiculação foi substituída pelo
> [**fechamento por cota da célula-dia**](../../features/quota-aware-categorization.md):
> `orphan` foi renomeada pra `bonus`; `out_slot` deixou de faturar e de abater o déficit;
> `deficit = expected − in_slot`; `bonus = COUNT(category = 'bonus')` (acabou o termo
> sintético `GREATEST(0, in_slot − expected)`); e **`Impactos = pmm × (in_slot + bonus)`**
> em toda tela e exportável. O numerador "entregue" descrito aqui era `in_slot + out_slot`; hoje é só `in_slot`, e o excedente já nasce `bonus` no categorizador.
> **Não copie fórmula daqui pra código novo** — a autoridade é
> [`docs/features/quota-aware-categorization.md`](../../features/quota-aware-categorization.md).

# Spec — Investido consolidado proporcional ao período (`/insights`, Modelo B)

**Data:** 2026-07-08
**Autor:** Claude + Dereck
**Status:** aprovado, implementado
**Supersede:** [2026-07-08-insights-consolidated-fill-cap-design.md](2026-07-08-insights-consolidated-fill-cap-design.md) (a abordagem clamp-em-hoje foi substituída — ver §5)

## 1. Problema

Após o primeiro fix (clamp em "hoje" + cap no contrato), o **Investido** de campanha consolidada ainda não escalava com o período: como o executado era `contrato × min(1, entregue_na_janela ÷ planejado_na_janela)`, assim que a janela tinha entrega ≥ plano (comum, com over-delivery), a razão batia 1,0 e o Investido mostrava o **contrato inteiro** já num recorte curto.

**Evidência (campanha `5252c79b-...`, contrato R$ 91.704, 19/06–18/07, ativa):** selecionar só junho já mostrava ~R$ 91.527 (≈ contrato cheio). Ampliar o período não somava — ficava travado no teto. O usuário quer "de X a Y = tanto", proporcional ao período.

## 2. Decisão (Modelo B — por entrega)

O Investido consolidado passa a ser **proporcional à entrega dentro do período, sobre o plano da campanha inteira**:

```
executado = cv × LEAST(1, entregue_na_janela ÷ plano_da_campanha_INTEIRA)
bonus     = cv ×        (excedente_na_janela ÷ plano_da_campanha_INTEIRA)
```

- **Numerador** = entregue (`in_slot + out_slot`) / excedente (`bonus`) **dentro da janela** `[from, to]`.
- **Denominador** = `SUM(expected)` sobre a campanha **inteira** `[start_date, end_date]` (fixo, independe da janela). É a taxa estável por inserção: `cv ÷ plano_total`.

Escolha entre B (por entrega) e C (por tempo, `cv × dias/dias_totais`): usuário escolheu **B** — "Investido" é valor entregue; sub-entrega reduz, over-delivery vira bônus.

## 3. Propriedades

- **Proporcional ao período:** junho = fração do contrato; período todo (entregue) → contrato.
- **Monotônico:** alargar a janela para dias ainda-não-veiculados adiciona 0 (não deflaciona nem infla) — **dispensa o clamp de "hoje"** do fix anterior.
- **Cap em 100% + bônus:** over-delivery capa o Investido no contrato; excedente só na Bonificação, à mesma taxa por inserção (fim do double-count).
- **Déficit reduz:** entrega < plano → `< contrato` (correto).
- **Campanha ativa:** "campanha inteira" hoje mostra o entregue até agora, completando conforme veicula.

## 4. Implementação

`workers/internal/catalog/insights.go`:
- `aggregateInvestment`: CTE `cs_window` (numerador — entregue/bônus na janela `[from,to]`) + CTE `cs_plan` (denominador — `expected` da campanha inteira `[start,end]`). Executado consolidado = `cv × LEAST(1, executed / plan_expected)`; bônus = `cv × bonus / plan_expected`.
- `computeCPM` (slow-path): subqueries `w` (entregue na janela) + `pl` (plano cheio), mesmo `LEAST(1, w.executed / pl.plan_expected)`.
- `per_insertion` inalterado (aditivo).
- **Removidos:** `InsightsParams.Today`, `investmentUpperBound`, `todaySaoPaulo`/`spLocation` no handler — o clamp de "hoje" ficou redundante sob o Modelo B.

Testes (`insights_test.go`, todos com Postgres real): proporcional (meia janela = metade), monotônico (dias não-veiculados não deflacionam), cap+bônus, déficit reduz, per_insertion aditivo, CPM fast+slow com sub-janela discriminante.

## 5. Por que substituiu o fix anterior

O fix de manhã (clamp em `hoje` + cap) resolvia a **queda ao alargar a janela** (dias futuros no denominador), mas mantinha o denominador = plano da **janela**, então cada janela totalmente entregue mostrava o contrato cheio (junho = 91k). O Modelo B usa o plano da **campanha inteira** como denominador → resolve os dois problemas de uma vez (proporcionalidade + não-deflação) e torna o clamp de "hoje" desnecessário.

## 6. Deploy

Só SQL de leitura (agregação). Sem migration, sem schema, sem risco de dado. Fluxo normal (`go build ./...` cross-compile linux, `go test`).
