# Spec — Correção do Investido/Bonificação/CPM em campanha consolidada (`/insights`)

**Data:** 2026-07-08
**Autor:** Claude + Dereck
**Status:** SUPERSEDED por [2026-07-08-insights-consolidated-period-proportional-design.md](2026-07-08-insights-consolidated-period-proportional-design.md)

> ⚠️ **Superseded no mesmo dia.** A abordagem deste spec (clamp do denominador em "hoje" + cap) resolvia a queda ao alargar a janela, mas mantinha o denominador = plano da *janela*, então junho totalmente entregue mostrava o contrato cheio. O Modelo B (denominador = plano da *campanha inteira*) substituiu isso — torna o Investido proporcional ao período e dispensa o clamp. Mantido aqui como registro histórico do raciocínio.

## 1. Problema

No dashboard `/insights`, os KPIs **Investido**, **Bonificação** e **CPM** de campanhas com pricing `consolidated` têm dois defeitos que compartilham a mesma raiz — a fórmula do executado é uma *taxa de cumprimento* multiplicada pelo valor total do contrato, e o denominador (`esperado`) vem da view `daily_play_summary` sem tratamento de tempo nem de excedente.

### Defeito 1 — dias futuros derrubam o número

A view gera `expected` via `generate_series(start_date, end_date)` para **todos** os dias do plano, inclusive dias que ainda não aconteceram. O executado consolidado é:

```
executado = consolidated_value × (executed_na_janela / esperado_na_janela)
```

(`workers/internal/catalog/insights.go:451-457`; bonificação idêntica em `:458-464`, com o mesmo denominador). Ao esticar o período para frente, entram dias com `esperado > 0` e `executado = 0` (impossível ter tocado — são futuro), o denominador incha, a razão cai e **Investido/Bonificação/CPM diminuem quando a janela aumenta**.

**Evidência (prod, campanha `5252c79b-b8ab-43b1-b63e-69add4402c66`, 5 emissoras `consolidated`, Σ `consolidated_value` = R$ 39.037,52; hoje = 08/07/2026):**

| Janela | Investido | Razão executed/expected |
|---|---|---|
| 19–30/06 | ~R$ 60k | ~1,27 |
| 19/06–18/07 | ~R$ 39k | ~1,0 (dias 09–17/07 entram com esperado=32/executado=0) |

### Defeito 2 — over-delivery contado duas vezes

`executed` no numerador = `SUM(in_slot + out_slot)`, incluindo as tocadas **acima** do planejado. A view também define `bonus = GREATEST(0, in_slot − expected) + orphan`. Então uma tocada acima do plano entra **tanto** no Investido (fazendo a razão passar de 1,0 → executado > contrato) **quanto** na Bonificação. O excedente aparece em dois KPIs ao mesmo tempo.

Decisão de negócio (Dereck): **tocada acima do planejado é bonificação, não investimento.** O executado deve capar no valor do contrato; o excedente fica só na Bonificação.

## 2. Escopo

Afeta **somente** o modo `consolidated`. O modo `per_insertion` é aditivo (`Σ unit_value × tocadas`), dias futuros contribuem 0, e não sofre nenhum dos dois defeitos — permanece intacto.

**Fora de escopo:** `contratado` (calculado mas não exibido em nenhum card); `impactos`/veiculações; a régua `ApprovedDetectionsFilter`; qualquer mudança no frontend além de, opcionalmente, tooltip de rótulo (ver §6).

## 3. Correção

Duas mudanças, aplicadas em **dois lugares** que replicam a mesma fórmula: `aggregateInvestment` e `computeCPM` (o CTE `per_campaign_exec` → subquery `t`).

### A. Cortar a janela em "hoje"

O intervalo de `for_date` usado para somar `expected`/`executed`/`bonus` no modo consolidado passa a ter teto em hoje:

```
s.for_date BETWEEN GREATEST(cm.start_date, $from)
              AND LEAST(cm.end_date, $to, $today)
```

- `$today` é **injetado**, não `now()` na SQL — para ser TZ-correto (America/Sao_Paulo, alinhado ao resto da atribuição) **e determinístico nos testes**.
- Injeção: novo campo `Today time.Time` (date-only) em `catalog.InsightsParams`. O handler (`handlers/insights.go`) calcula `time.Now()` em `America/Sao_Paulo` truncado para data. Testes passam valor fixo.
- Aplica-se ao CTE `cs_totals` (consolidado) em `aggregateInvestment` e ao subquery `t` de `per_campaign_exec` em `computeCPM`. **Não** se aplica a `cs_per_ins` (per_insertion) — lá dias futuros já contribuem 0 e o `contratado` per_insertion deve manter o plano cheio.

Como `executed`/`bonus` de dias futuros são 0, o corte remove apenas denominador morto — a razão sobe de volta ao valor real de "até hoje".

### B. Capar o executado no valor do contrato

O CASE consolidado do executado passa a limitar a razão a 1,0:

```
WHEN mode='consolidated' AND expected > 0
    THEN consolidated_value × LEAST(1, executed::numeric / expected)
```

Aplica-se ao executado em `aggregateInvestment` **e** em `computeCPM`. A **bonificação não muda** — continua `consolidated_value × bonus / expected` (o excedente já vive ali; agora é o único lugar onde aparece). O `contratado` não muda.

## 4. Efeito nos números (campanha 5252)

| KPI | Antes (jun) | Antes (cheia) | Depois (jun) | Depois (cheia) |
|---|---|---|---|---|
| Investido | ~60k | ~39k | **~39k** (cap no contrato) | **~39k** (estável) |
| Bonificação | ~37k | ~26k (encolheu) | ~37k (inalterado) | **recupera — ≥ jun, não cai** |
| CPM | inflado | despenca | corrigido | estável |

Notas:
- **Investido junho** cai ~60k → ~39k por causa da mudança **B** (cap): remove o excedente que estava sendo double-counted. Σ dos contratos das 5 emissoras = R$ 39.037,52. Janela cheia também ~39k (mudança A ignora dias futuros) — não cai mais ao alargar.
- **Bonificação junho** não muda (sem dias futuros na janela; o cap B não toca a bonificação). Na **janela cheia**, a mudança **A** tira os dias futuros do denominador, então ela *sobe* de volta (para de encolher). O cap B não afeta bonificação — o excedente já vivia nela.
- **CPM** usa o executado corrigido (clamp A + cap B) nos dois caminhos (dinâmico e por-campanha).

## 5. Comportamento residual (intencional)

Se uma emissora **entregou menos que o planejado em dias que já passaram** (déficit real — ex.: fora do ar), o executado reflete isso (< contrato), e uma janela que inclua esse período pode mostrar Investido menor que uma janela só de dias 100% entregues. **Isso é correto**: entregou menos → executou menos. O corte em "hoje" (A) remove apenas dias que ainda **não puderam** tocar, não déficit legítimo.

## 6. Frontend (opcional, baixo custo)

Adicionar tooltip no card **Investido** deixando o rótulo honesto: "Valor do contrato efetivamente cumprido (limitado a 100%); excedente aparece em Bonificação. Dias futuros não contam." Sem mudança de layout. Decisão de incluir ou não fica no plano.

## 7. Testes (`insights_test.go`)

Fixtures já existem (`insSeedStation` etc.). Casos novos:

1. **Futuro não derruba:** campanha consolidada com dias planejados após `Today`; executado com janela até `end_date` == executado com janela até `Today`. Sem o fix, o primeiro é menor.
2. **Cap em 100%:** emissora com `in_slot > expected`; executado == `consolidated_value` (não mais), e o excedente aparece em `bonificacao.valor`.
3. **Déficit passado reduz:** emissora com `executed < expected` em dias passados; executado == `consolidated_value × executed/expected` (< contrato). Garante que o cap não vira piso.
4. **`per_insertion` intacto:** regressão — modo per_insertion não muda com o fix.
5. **`computeCPM` coerente:** CPM dinâmico usa o executado corrigido (mesma clamp+cap); um caso com dias futuros não infla nem despenca o CPM.

## 8. Arquivos tocados

- `workers/internal/catalog/insights.go` — `aggregateInvestment`, `computeCPM`, struct `InsightsParams` (+`Today`), `Compute` (repassa `Today`).
- `workers/internal/api/handlers/insights.go` — calcula `Today` em America/Sao_Paulo e popula `InsightsParams`.
- `workers/internal/catalog/insights_test.go` — casos 1–5.
- `workers/internal/api/handlers/insights_test.go` — garantir que `Today` é populado (mock/param).
- `docs/features/insights-dashboard.md` — atualizar as fórmulas (tabela §Fórmulas) e `ultima-verificacao`.

## 9. Deploy

Mudança é só de leitura (SQL de agregação) — sem migration, sem alteração de schema. Segue o fluxo normal (`go build ./...` cross-compile linux OK, `go test ./...`). Sem risco de dado.
