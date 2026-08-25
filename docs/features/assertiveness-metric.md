---
status: implementado
ultima-verificacao: 2026-08-07
codigo-relacionado:
  - migrations/0067_assertiveness_daily.up.sql
  - workers/internal/assertiveness/compute.go
  - workers/internal/assertiveness/read.go
  - workers/internal/assertiveness/scheduler.go
  - workers/internal/api/handlers/assertiveness.go
  - frontend/src/pages/ManagementPage.jsx
  - scripts/sql/assertividade.sql
---

# Assertividade da plataforma

De tudo que veiculou, **quanto o matcher pegou sozinho** — o resto alguém teve
que digitar na mão depois.

```
assertividade = auto / (auto + miss)
```

Aparece como card na [Visão Gerencial](management-overview.md) (`/management`),
no lugar do antigo "Materiais monitorados" (que virou parte do sub do card de
veiculações, junto com a contagem de campanhas).

---

## As duas armadilhas de leitura

**1. A métrica só enxerga o miss que alguém REPORTOU.** Se a emissora não mandou
comprovante e ninguém digitou, o miss fica invisível. Consequência direta: um
time que parou de digitar manual é indistinguível de um matcher perfeito — o
número *sobe* quando o processo piora.

Por isso o card **nunca** mostra o percentual sozinho. O sub carrega sempre o
volume (`julho · 199 de 26.582 não detectadas`). Se as manuais despencarem, o
denominador denuncia. **Não remova o volume do card.**

Isto é "assertividade contra o que foi reclamado" — que é o que o cliente sente
—, não recall absoluto. Pra recall real, só cruzando com o fornecedor:
[vendor-reconciliation.md](../operations/vendor-reconciliation.md).

**2. O mês em curso mente pra cima.** No dia 3 do mês quase nenhuma manual
daquele mês foi digitada — a emissora manda o comprovante depois. O percentual
nasceria perto de 100% e desabaria no fim do mês.

Por isso o card usa **sempre o último mês fechado** e **ignora o filtro de
período da tela** (respeita os de cliente/campanha). O endpoint nem aceita
`from`/`to`. O nome do mês fica visível no sub justamente pra deixar isso óbvio.

---

## Os baldes

Nem toda veiculação digitada na mão é erro nosso. A classificação roda nesta
precedência (a ordem importa):

| Balde | Regra | Conta como erro? |
|---|---|---|
| `duplicate` | já existe detecção **automática** do mesmo material, na mesma emissora, em ±5 min | Não — é redigitação |
| `no_fingerprint` | o material só foi fingerprintado **depois** da tocada | Não — não existia no índice |
| `stream_down` | a tocada cai dentro de uma janela de `down` da emissora | Não |
| `unmonitored` | nenhum sinal da emissora naquele dia (nem detecção, nem health event) | **Ambíguo** |
| `miss` | o resto: stream no ar, material no índice, worker vivo | **Sim** |

`duplicate` é avaliado primeiro porque não é miss de espécie nenhuma. Caso real
que motivou o balde: no período de disco cheio a detecção acontecia mas a
evidência ficava `failed`; o operador lia "sem áudio", pedia o comprovante e
redigitava a tocada.

`unmonitored` é ambíguo **por construção**: "emissora fora do ar" e "worker
morreu calado" produzem o mesmo vazio no banco, e o segundo é culpa nossa. Ele é
contado separado por isso. Na medição de jul/2026 veio **zero**, o que é o que
permite confiar no número sem ressalva.

---

## Como o dado é produzido

Tabela `assertiveness_daily`, granularidade **(dia, campanha, emissora,
material)**. O material está na chave de propósito: foi esse eixo que revelou, na
primeira medição, que 26% de todo o miss eram materiais "PULSO SONORO" — um bug
conhecido com fix mergeado atrás de flag. Agregado só por emissora, o padrão
fica invisível.

O `assertiveness.Scheduler` recomputa **do 1º dia de dois meses atrás até hoje**,
na subida da API e a cada 6h, com advisory lock entre réplicas.

Duas decisões que parecem exagero e não são:

- **Recomputa janela larga, não só ontem.** Manual de uma tocada do dia 3 costuma
  ser digitada dia 20. Gravando só o dia anterior, o número congelaria errado.
  `DELETE` + `INSERT` por dia, idempotente.
- **Dois meses, não "últimos N dias".** O card lê o mês fechado *e* o anterior (a
  tendência), e ambos precisam estar completos o mês inteiro. Uma janela de 45
  dias quebraria na virada: em 31/08, 45 dias atrás é 17/07 e julho ficaria pela
  metade. Coberto por `TestWindowCobreOsDoisMesesQueOCardLe`.

**Unidade de contagem: a linha de `detection_campaigns`** (projeção canônica), não
`detections.campaign_id`. É o que a grade, o `/insights` e os relatórios leem —
então o número bate com as telas e com um `SELECT` ad-hoc por campanha. Com
`detections.campaign_id` divergia sob reatribuição/multi-atribuição.

### Janela de `down` fechada pelo próximo `up`

O fim da janela sai, nesta ordem: `duration_seconds` quando existe → próximo
evento `up` da emissora → agora. E nunca depois de agora.

Fechar direto em `NOW()` é armadilha: um `down` que ficou **aberto** (sem
duration e sem `up` subsequente) teria janela crescendo pra sempre e passaria a
perdoar todos os meses seguintes. O erro cai no lado perigoso — **infla** a
assertividade.

---

## Verificação

[`scripts/sql/assertividade.sql`](../../scripts/sql/assertividade.sql) roda a
mesma conta de forma ad-hoc, com blocos de amostra pra auditar cada balde
(inclusive pares manual × automática, pra calibrar a janela de 5 min).

**A fonte é o Go; o script é a ferramenta de verificação.** Quando alguém
desconfiar do card, roda o script e compara. Se divergirem, um dos dois está
errado — **mexeu num, mexa no outro** (o projeto já se queimou com dois motores
de categorização divergindo).

Armadilha já paga no script: `t_auto` é agregado (`COUNT(*) AS n`), então quem
consome tem que fazer `SUM(n)`. Um `COUNT(*)` ali conta **grupos** — campanha
com 1 emissora reportava "auto = 1" tendo 114 detecções, e todos os percentuais
saíam catastroficamente subestimados.

## Baseline

Julho/2026, primeira medição: **99,35%** em prod (26.394 auto, 172 miss). Os
misses são concentrados, não espalhados: 26% em materiais "PULSO SONORO" (bug
conhecido, fix atrás de flag) e 33 num único material numa única emissora
(Stereo Vale/SJC). `no_fingerprint` (182) foi **maior que o miss inteiro** — a
maior causa de trabalho manual não é o matcher errar, é material cadastrado
depois da veiculação.

## Fora de escopo (por ora)

- **Drill-in** por emissora/material ao clicar no card — é onde mora a ação; a
  tabela já tem a granularidade pronta pra isso.
- **Série histórica** além dos dois meses recomputados (a tabela guarda o que já
  foi computado; janelas anteriores exigem backfill).
- **Exposição pro cliente** — `/management` é admin-only, e a métrica amadurece
  internamente antes de virar argumento comercial.
