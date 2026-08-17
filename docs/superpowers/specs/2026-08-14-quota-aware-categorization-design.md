# Categorização por cota (quota-aware) — design

**Data:** 2026-08-14
**Origem:** investigação da campanha 270 (Band Vale FM 102.9, 04/08/2026) — bonificação
da emissora sendo contada como "fora da faixa".
**Decisões tomadas por:** Dereck (dono do produto), nesta sessão.

---

## 1. Problema

A categorização de uma veiculação hoje é **por detecção e sem estado**: cada tocada
é classificada isoladamente por data + dia-da-semana + faixa horária
([categorizer.go](../../../workers/internal/categorizer/categorizer.go)). A noção de
"quantas já tocaram hoje" não existe no categorizador — ela aparece só depois, na view
`daily_play_summary`, através de `bonus = GREATEST(0, in_slot − expected) + orphan`.

Consequência: **o bônus é calculado em dois lugares que não conversam**, e o sistema
acumulou regras divergentes pra mesma pergunta ("tocou num dia/horário sem meta"):

| Caminho | Categoria | Vira bônus? |
|---|---|---|
| Sem regra aplicável no dia | `orphan` | sim |
| Carve-out, dia extra dentro da vigência (decisão 2026-07-13) | `orphan` | sim |
| **Override com `plays_expected = 0`** | **`out_slot`** | **não** |

O terceiro caso é o bug relatado: [categorizer.go:93](../../../workers/internal/categorizer/categorizer.go#L93)
(`if override.PlaysExpected == 0 { return CatOutSlot }`) e seu espelho SQL em
[distribution_rules.go:263](../../../workers/internal/catalog/distribution_rules.go#L263)
devolvem `out_slot` **ignorando a faixa horária gravada**, que a decisão D5 do spec de
2026-05-19 declarou inerte. Uma bonificação que tocou dentro da janela vira "fora da faixa".

E há ainda uma **terceira implementação** da mesma regra no frontend
([DayDetailModal.jsx:100-154](../../../frontend/src/components/DayDetailModal.jsx#L100-L154)),
que atribui cada tocada à faixa mais estreita que casa, **sem teto de cota** — diverge
das outras duas.

### Divergência financeira medida

`out_slot` significa coisas diferentes em cada tela:

| Consumidor | Tratamento de `out_slot` |
|---|---|
| `FinancialsByCampaign` (`/campaigns`) | fora da base (`in_slot + bonus`) — vale zero |
| `insights.go` (`/insights`) | **entra em `executado`** (`in_slot + out_slot`) — é faturado como entrega contratada |
| `campaign_failures.go` (`/admin/station-failures`) | entra em `extras` |
| `daily_play_summary.deficit` | **abate o déficit** (`expected − in_slot − out_slot`) |

Ou seja: hoje a bonificação da emissora é faturada como veiculação contratada no
`/insights` e simultaneamente invisível no `/campaigns`.

---

## 2. Modelo novo

Unidade de decisão: a **célula-dia** = (campanha, tipo de material, emissora, dia local SP).

```
0. out_date  — tocada fora do período da campanha, ou (material carve-out) fora do
               período das regras que o nomeiam. Sai da conta e não vira bônus.

1. N = meta do dia da célula
       override.plays_expected  se existir override pra (campanha, tipo, emissora, dia)
       senão Σ plays_per_day das regras que valem naquele dia

2. "dentro da faixa" = a tocada cai em ALGUMA faixa que vale hoje, com ±15 min de
   tolerância (SlotToleranceSeconds). NÃO há cota por faixa — a meta é do dia.

3. Tocadas DENTRO da faixa, em ordem cronológica:
       as N primeiras  → in_slot
       as demais       → bonus

4. Tocadas FORA da faixa:
       se in_slot < N  → out_slot     (a meta não foi cumprida dentro da faixa)
       senão           → bonus

5. deficit = max(0, N − in_slot)      ← out_slot NÃO abate o contrato
```

`in_slot` nunca passa de `N` por construção.

### Tabela-verdade (casos validados com o dono)

| N | Tocadas | in_slot | out_slot | bonus | deficit |
|---|---|---|---|---|---|
| 2 | 1 dentro, 1 fora | 1 | 1 | 0 | **1** |
| 2 | 2 dentro, 1 fora | 2 | 0 | **1** | 0 |
| 2 | 0 dentro, 3 fora | 0 | **3** | 0 | 2 |
| 2 | 4 dentro | 2 | 0 | 2 | 0 |
| 3 (2 faixas) | 3 dentro da 1ª faixa | 3 | 0 | 0 | 0 |
| **0** (célula zerada) | 3 quaisquer | 0 | 0 | **3** | 0 |

A última linha é o caso da campanha 270: cai fora sozinha, sem regra especial pra
`plays_expected = 0`.

### O que o modelo elimina

- `bonus = GREATEST(0, in_slot − expected) + orphan` perde o primeiro termo (sempre 0):
  **o categorizador vira a fonte única do bônus**.
- A categoria `orphan` deixa de existir como veredito — vira `bonus`, explícito.
- O ramo especial de `plays_expected = 0` desaparece dos dois motores.

---

## 3. Decisões

| # | Decisão | Justificativa |
|---|---|---|
| D1 | Meta é **do dia**, não por faixa. A faixa é só um teste booleano por tocada. | Palavras do dono: "a meta é do dia, mas as veiculações precisam ser dentro da faixa da regra". Evita bônus+déficit simultâneos por rateio de cota entre faixas. |
| D2 | Gatilho do bônus é **`in_slot` ter fechado N**, não o total de tocadas. | Decisão explícita no caso "N=2, 0 dentro, 3 fora" → 3 `out_slot`, bônus 0. Emissora que não cumpriu horário nenhum não gera bonificação. |
| D3 | `out_slot` **não abate o déficit** e **não vale nada** comercialmente (nem impacto, nem investido, nem bônus). | Se não fecha o contrato, também não fatura. Padroniza tudo na base do `/campaigns` (`in_slot + bonus`); `/insights` passa a bater com ela. |
| D4 | Categoria `bonus` de verdade no banco (migration), não `orphan` reaproveitado. | Auto-explicativo no SQL; elimina o duplo sentido de "órfã". |
| D5 | Fechamento da célula-dia **no insert**, na mesma transação. | Tela correta em tempo real. Efeito colateral aceito: uma tocada gravada `out_slot` às 03:00 vira `bonus` às 10:00 quando a meta fecha — o dia se assenta. |
| D6 | `out_date` continua precedendo tudo e fora da conta de bônus. | Ressalva explícita do dono: "cuida com os materiais fora da data". |
| D7 | `/admin/station-failures` segue o déficit novo, **separando "não tocou" de "tocou fora do horário"**. | Uma só definição de déficit; mas a emissora não pode receber cobrança de ausência num dia em que veiculou. |
| D8 | Tolerância de ±15 min mantida no teste de faixa. | Sem mudança de comportamento conhecido; jitter de stream continua real. |
| D9 | Alcance retroativo decidido **depois** de medir o delta contra um clone do dump de prod. | §4.8 do CLAUDE.md. As últimas 48h se curam sozinhas via `projrecon` no deploy. |

### Premissas adotadas (não questionadas, registradas)

- Veiculação manual entra na fila igual à automática.
- Multi-atribuição (F-119): cada projeção conta na meta da campanha **dela**.
- Ordem de consumo dentro de cada passo: cronológica (`detected_at`, desempate por `id`).
- Material sem `type_id` continua fora das regras → `bonus`.
- Emissora fora de qualquer regra → N=0 → `bonus`.

---

## 4. Impacto por consumidor

| Arquivo | Mudança |
|---|---|
| `migrations/0063_*` | `bonus` no CHECK de `detections.category` e `detection_campaigns.category`; `UPDATE orphan → bonus`. `orphan` continua aceito (janela de deploy). |
| `workers/internal/categorizer/categorizer.go` | `Categorize` (por tocada) → `Settle` (por célula-dia). |
| `workers/internal/catalog/detections.go` | insert-path chama `Settle` e regrava a célula-dia inteira. |
| `workers/internal/catalog/distribution_rules.go` | `recatClassifiedCTE` reescrita com window functions; **escopo expandido pra célula-dia inteira**. |
| `migrations/0064_*` | `daily_play_summary` + `daily_play_summary_for`: `deficit = expected − in_slot`, `bonus = COUNT(category='bonus')`. |
| `workers/internal/catalog/insights.go` | `executado = in_slot` (remove `out_slot`); breakdown lê `bonus`. |
| `workers/internal/catalog/campaign_failures.go` | déficit novo + split `absent` / `off_slot`. |
| `frontend/src/components/DayDetailModal.jsx` | `buildDayPlan` espelha o `Settle`. |
| `frontend/src/pages/StationFailures*` | colunas separadas de falha. |
| `workers/internal/reportcsv`, `postsale/repo.go` | rótulos de categoria. |

### Ponto de atenção crítico (escopo do recat)

O motor de recategorização hoje classifica **linha a linha** dentro de um escopo
arbitrário (uma regra, uma campanha, um material). No modelo novo a decisão depende de
**todas** as tocadas da célula-dia. Todo escopo precisa ser **expandido pra célula-dia
completa** antes de classificar, senão uma recategorização parcial produz cotas erradas.
Vale pra `RecategorizeForRule`, `ForCampaign`, `ForMaterial`, `ForOverride`,
`HealProjectionDrift` e `HealProjectionDriftForCampaign`.

---

## 5. Fora de escopo

- Cota por faixa horária (rejeitada em D1).
- Mudar a tolerância de 15 min.
- Mudar o que já foi congelado em pós-venda (`payload_json`) — documentos enviados não mudam.
- Reprocessar detecção/áudio: nada aqui toca matching, fingerprint ou evidência.
