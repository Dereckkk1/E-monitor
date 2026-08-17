---
status: implementado
ultima-verificacao: 2026-08-17
codigo-relacionado:
  - workers/internal/categorizer/categorizer.go
  - workers/internal/categorizer/settle_test.go
  - workers/internal/catalog/detections.go
  - workers/internal/catalog/distribution_rules.go
  - workers/internal/catalog/projection_reconcile.go
  - workers/internal/catalog/detection_filter.go
  - workers/internal/catalog/insights.go
  - workers/internal/catalog/campaign_failures.go
  - workers/internal/catalog/daily_summary.go
  - workers/internal/catalog/settle_parity_test.go
  - workers/internal/catalog/detections_settle_test.go
  - workers/internal/catalog/detections_resettle_test.go
  - workers/internal/catalog/daily_summary_test.go
  - workers/internal/catalog/campaign_failures_test.go
  - workers/internal/reportcsv/reportcsv.go
  - workers/cmd/backfill-recategorize/main.go
  - migrations/0063_category_bonus_constraint.up.sql
  - migrations/0064_category_bonus_rename.up.sql
  - migrations/0065_quota_aware_summary.up.sql
  - frontend/src/components/DayDetailModal.jsx
  - frontend/src/components/DeficitSplit.jsx
---

# Categorização por cota — fechamento da célula-dia

Documento canônico de como uma veiculação vira `in_slot` / `out_slot` / `out_date` /
`bonus`, e do que cada categoria vale comercialmente. **Toda tela, relatório e
cobrança que fala em "tocou dentro da faixa", "bonificação" ou "déficit" deriva
desta regra.**

> Spec (autoridade, decisões D1–D9): [`docs/superpowers/specs/2026-08-14-quota-aware-categorization-design.md`](../superpowers/specs/2026-08-14-quota-aware-categorization-design.md)
> · Plano de execução: [`docs/superpowers/plans/2026-08-14-quota-aware-categorization.md`](../superpowers/plans/2026-08-14-quota-aware-categorization.md)

## O que faz

A categorização deixou de ser **por tocada e sem estado** e passou a ser um
**fechamento por célula-dia**: o sistema olha o conjunto inteiro de veiculações de
uma célula naquele dia, confronta com a meta do dia e decide as categorias de
todas de uma vez.

**Célula-dia** = `(campanha, tipo de material, emissora, dia calendário local em
São Paulo)`. É a mesma coordenada da grade de `/detections` e da view
`daily_play_summary`.

## Por que existe

Antes, o bônus era calculado em **dois lugares que não conversavam**: o
categorizador (que emitia `orphan` quando não havia regra aplicável) e a view
`daily_play_summary` (que sintetizava `bonus = GREATEST(0, in_slot − expected) + orphan`).
Daí saíram três problemas concretos:

| Sintoma | Causa |
|---|---|
| Bonificação lida como "fora do prazo" (campanha 270, Band Vale FM 102.9 — o caso que abriu a investigação) | Override com `plays_expected = 0` devolvia `out_slot` ignorando a faixa gravada |
| A mesma tocada valia coisas diferentes em cada tela | `out_slot` faturava como entrega no `/insights` e valia zero no `/campaigns` |
| Excedente contado duas vezes no dinheiro | `in_slot` sem teto **e** re-somado no termo `GREATEST(0, in_slot − expected)` do bônus |

Havia ainda uma **terceira implementação** da regra no frontend (`buildDayPlan`
do `DayDetailModal`), que atribuía tocadas a faixas sem teto de cota e divergia
das outras duas.

## A regra, em 5 passos

```
0. out_date — tocada fora do período da campanha, ou (material carve-out) fora do
              período das regras que o nomeiam. Precede tudo e NÃO consome cota.

1. N = meta do DIA da célula
       override.plays_expected            se existir override para (campanha, tipo, emissora, dia)
       senão Σ plays_per_day das regras    que valem naquele dia (data + dia-da-semana)

2. "dentro da faixa" = a tocada cai em ALGUMA faixa válida naquele dia, com
   ±15 min de tolerância (categorizer.SlotToleranceSeconds / 900s no SQL).
   NÃO há cota por faixa — a meta é do dia (D1).

3. Tocadas DENTRO da faixa, em ordem cronológica (detected_at, id):
       as N primeiras → in_slot
       as demais      → bonus

4. Tocadas FORA da faixa:
       se in_slot < N → out_slot   (a meta ainda não fechou dentro da faixa)
       senão          → bonus

5. deficit = max(0, N − in_slot)   ← out_slot NÃO abate o contrato (D3)
```

`in_slot` **nunca passa de N por construção** — é essa garantia que permitiu tirar
o termo sintético de bônus da view.

A soma `Σ plays_per_day` do passo 1 **não filtra por material**: N é propriedade da
célula-dia, não do material. A faixa (passo 2) é que respeita carve-out — material
nomeado em regra específica é julgado só pelas regras que o nomeiam; material comum,
só pelas gerais. Havendo override, **a faixa do override é a única considerada**.

### Tabela-verdade

Faixa da regra `10:00–12:00`, célula dentro do período da campanha:

| N | Tocadas | in_slot | out_slot | bonus | deficit |
|---|---|---|---|---|---|
| 2 | 1 dentro, 1 fora | 1 | 1 | 0 | **1** |
| 2 | 2 dentro, 1 fora | 2 | 0 | **1** | 0 |
| 2 | 0 dentro, 3 fora | 0 | **3** | 0 | 2 |
| 2 | 4 dentro | 2 | 0 | 2 | 0 |
| 3 (2 faixas somando 3) | 3 dentro da 1ª faixa | 3 | 0 | 0 | 0 |
| **0** (célula zerada por override) | 3 quaisquer | 0 | 0 | **3** | 0 |

Leituras que valem memorizar:

- **Linha 3 (D2).** Emissora que não acertou horário nenhum não gera bonificação:
  o gatilho do bônus é a meta ter fechado **dentro da faixa**, não o total de
  tocadas.
- **Linha 5 (D1).** Três tocadas na primeira faixa fecham a meta do dia inteiro —
  não existe "vaga sobrando na faixa da tarde" cobrando déficit enquanto a manhã
  já bonifica.
- **Linha 6.** É o caso da campanha 270. Cai fora sozinho, sem ramo especial pra
  `plays_expected = 0`: com N=0, `in_slot < N` é sempre falso, então `out_slot`
  é inalcançável e toda tocada vira `bonus`.

### Vocabulário: `orphan` virou `bonus`

A categoria `orphan` foi aposentada como veredito (D4). O nome sempre significou
"bonificação" (a view somava `orphan` em `bonus` desde a 0018) — agora é explícito
no banco.

- Nenhum produtor grava mais `'orphan'`. `categorizer.Categorize` (a função antiga,
  por tocada) é o único código que ainda devolve o valor e **não tem caller de
  produção** — sobrevive só nos próprios testes, documentando o modelo velho.
- O CHECK de 0063 **continua aceitando** `'orphan'` de propósito (janela de deploy /
  banco sem backfill). Quem soma bonificação lendo `detections` direto deve usar
  `categorizer.BonusCategoriesSQL` (`('bonus','orphan')`) em vez de repetir o
  literal — é o que `AggregateByMaterialStation` e `reportcsv.CategoryLabelPT`
  fazem. A view/função (0065) contam **só `'bonus'`**, deliberadamente: somar os
  dois esconderia um produtor que voltasse a gravar o valor velho.

## Dois motores que TÊM que concordar

A mesma regra existe duas vezes, e não dá pra ter só uma:

| Motor | Onde | Quem usa |
|---|---|---|
| **Go** — `categorizer.Settle` | [`workers/internal/categorizer/categorizer.go`](../../workers/internal/categorizer/categorizer.go) | insert-path (tocada nova, manual, reatribuição, refechamento) |
| **SQL** — CTE `classified` (`recatClassifiedCTE`) | [`workers/internal/catalog/distribution_rules.go`](../../workers/internal/catalog/distribution_rules.go) | recategorização em massa (rule/override/tipo do material), reconciler `projrecon`, backfill |

O que impede a divergência silenciosa é
[`workers/internal/catalog/settle_parity_test.go`](../../workers/internal/catalog/settle_parity_test.go):
ele roda o lado SQL como **SELECT puro** e o lado Go pelos **loaders de produção**
(`loadRulesForCell`, `loadOverrideForCell`, `loadCellDayPlays`) e exige veredito
idêntico. Cobre a tabela-verdade, os limites exatos de tolerância (900 e 901 s nos
dois extremos, na faixa da regra e na do override), empate no mesmo segundo, bordas
do período da campanha, linhas fora do conjunto aprovado, carve-out, override zerado,
independência entre tipos e entre emissoras, `meta.N` × `expected` da view, e um
sweep aleatório parametrizável (`SETTLE_PARITY_SCENARIOS`, `SETTLE_PARITY_SEED`;
150 cenários por padrão, 25 em `-short`).

> **Se este teste ficar vermelho, conserte o motor — nunca o teste.** A tolerância
> de 15 min já divergiu em silêncio uma vez entre os dois caminhos (2026-05-26) e o
> sintoma foi contagem sumindo quando o operador editava a regra.

Há um **terceiro lugar** que fala de faixas, e ele não é motor: o `buildDayPlan` do
`DayDetailModal`. Desde esta entrega ele é **apresentação pura** — reparte entre as
faixas do dia as tocadas que o backend **já** rotulou `in_slot`, só pra desenhar a
barra de progresso. O veredito vem sempre de `det.category` / `cellSummary`.

## Onde o fechamento acontece

| Gatilho | O que fecha | Código |
|---|---|---|
| Veiculação nova (engine) ou manual | a célula-dia **inteira** da tocada, na mesma transação do insert | `Detections.Create` / `CreateManual` → `settleCellDay` |
| Fan-out multi-atribuição (F-119) | uma célula-dia **por campanha projetada** | `CategorizeFor`; falha do `InsertProjections` dispara `ResettleCellDay` pra convergir na hora |
| Retratar / ignorar / marcar ambígua / des-retratar / restaurar | as células-dia da tocada, **relendo** o conjunto aprovado | `mutateApprovedSet` |
| Reatribuição (§18.2.2, gêmeos, reject-path) | célula-dia de **origem e destino** | `ReattributeDetection` / `ReattributeRejectedDetection` |
| Criar/editar/apagar regra ou override, trocar `type_id` do material | escopo expandido pra célula-dia completa | `RecategorizeFor{Rule,Campaign,Material,Override}` |
| Reconciler `projrecon` | reassenta as **últimas 48h a cada 15 min** | [`workers/internal/projrecon/scheduler.go`](../../workers/internal/projrecon/scheduler.go) |
| Backfill retroativo | histórico inteiro, sob demanda | `workers/cmd/backfill-recategorize` (dry-run por padrão) |

Três propriedades que caem daí:

**Expansão de escopo é obrigatória.** Qualquer recategorização — mesmo a de uma
única célula por override — **expande o escopo recebido pras células-dia completas
e recarrega todas as tocadas aprovadas delas**. Classificar um escopo parcial linha
a linha produziria cota errada (uma tocada de fora do escopo que já ocupa vaga
ficaria invisível). Consequência deliberada: `classified` devolve **mais** linhas do
que o `scope` recebeu, e o apply reescreve todas.

**A cota conta só o conjunto aprovado.** Os dois motores filtram por
[`ApprovedDetectionsFilter`](../architecture/detection-count-consistency.md) (retratada / ignorada /
`audit_rejected` / `ambiguous` fora). A linha não-aprovada **não** tem a categoria
reescrita pelo recat — ninguém a lê, e ela é regravada quando volta ao conjunto.

**Fechamento é por projeção.** O conjunto é escopado por `dc.campaign_id`, a
reescrita toca a projeção **desta** campanha, e a tocada-base (`detections.category`)
só é espelhada quando a projeção é a canônica. Sem essa guarda, o fechamento de uma
campanha secundária do fan-out sobrescreveria a base com o veredito de outra
campanha.

**Serialização.** Cada fechamento toma um `pg_advisory_xact_lock` de
`(campanha, emissora, dia)` como **primeiro statement** da transação — sem ele duas
transações leem o mesmo conjunto (READ COMMITTED), ambas veem vaga e ambas gravam
`in_slot`, fechando a célula com `in_slot > N`. Quem trava mais de uma célula pede
as chaves em ordem lexicográfica, e **todos os advisory locks são tomados antes de
qualquer escrita de linha** — é o que impede o ciclo advisory↔linha.

## O que isso vale em dinheiro

### `out_slot` não vale nada (D3)

Veicular fora da faixa contratada **não fecha a obrigação e não fatura**:

| Consumidor | Antes | Agora |
|---|---|---|
| `/insights` — executado / investido | `in_slot + out_slot` | `in_slot` |
| `/campaigns` — investimento (`total_invested`) | `in_slot + bonus` | `in_slot` |
| `/campaigns` — bonificação (`total_bonus_value`) | não existia (ia dentro do investido) | `bonus` |
| `/campaigns` — inserções e impactos | `in_slot + bonus` | `in_slot + bonus` (inalterado) |
| **CPM (as duas telas)** | `in_slot + out_slot` (insights) / `in_slot + bonus` (campaigns) | `in_slot + bonus` **nas duas** — o numerador soma investido + bonificado |
| `daily_play_summary.deficit` | `expected − in_slot − out_slot` | `expected − in_slot` |
| `daily_play_summary.bonus` | `GREATEST(0, in_slot − expected) + orphan` | `COUNT(category = 'bonus')` |

As duas telas passaram a valorizar **o mesmo conjunto de categorias**
(`in_slot + bonus`) e a **parti-lo do mesmo jeito**: o dinheiro pago (`in_slot`)
separado da entrega gratuita (`bonus`). O `/insights` mostra isso em dois cards
(Investido executado / Bonificação); o `/campaigns` em dois campos
(`total_invested` / `total_bonus_value`). Se não fecha o contrato, também não
fatura — e o déficit continua aberto pra emissora repor.

> **`bonus` não é investimento.** Até 2026-08-17 o `/campaigns` somava
> `unit_value × bonus` dentro de "Investimento". Bonificação é, por definição,
> veiculação que o cliente **não pagou** (excedente da cota ou tocada sem meta),
> então o número superestimava o gasto. Agora o valor mora em
> `total_bonus_value` — a identidade `insights.Investido.Executado ==
> campaigns.TotalInvested` e `insights.Bonificacao.Valor ==
> campaigns.TotalBonusValue` está travada por `TestInsights_FinancialBase_MatchesCampaigns`.
> Medido no clone de prod (2026-08-17, 1.076 campanhas): investido agregado
> 7.433.634 → 6.949.640 (−483.994, −6,5%). Inserções, impactos e impactos no
> target **não mudaram**.

> **O CPM NÃO acompanha essa queda.** Tirar o bônus do investido é sobre o
> *dinheiro exibido*; o CPM tem numerador próprio, que soma as duas parcelas:
>
> ```
> Investimento = unit_value × in_slot                 ← só o que o cliente pagou
> Impactos     = pmm × (in_slot + bonus)              ← pago + bônus
> CPM          = (investido + bonificado) ÷ impactos × 1000
> ```
>
> **A razão:** o CPM mede a **eficiência da mídia entregue a preço de tabela**,
> não a eficiência da negociação. A tocada de bônus foi ao ar e já está no
> denominador, então tem que estar no numerador ao preço de tabela dela. Com o
> numerador só do pago, campanha com muito bônus exibiria um CPM artificialmente
> baixo, incomparável com o de qualquer outra — e o CPM existe pra comparar.
> No clone, o numerador só-do-pago tinha derrubado o CPM agregado de R$ 6,72 pra
> R$ 6,28; com a fórmula correta ele volta a **R$ 6,7162**. Detalhes e maiores
> movimentos em [insights-dashboard.md §"O numerador do CPM inclui a
> bonificação"](insights-dashboard.md).

### Os números do cliente CAEM — e é correção, não regressão

Quem comparar antes/depois precisa saber de duas quedas somadas:

1. **`/insights` parava de faturar `out_slot`** como entrega contratada.
2. **O excedente dentro da faixa era contado duas vezes.** No modelo antigo uma
   tocada excedente era `in_slot` **e** reaparecia no termo
   `GREATEST(0, in_slot − expected)` do bônus; como a base financeira é
   `in_slot + bonus`, ela entrava duas vezes. Exemplo: `expected = 2`, 4 tocadas
   dentro da faixa → antes `in_slot = 4`, `bonus = 2`, base **6** pra 4 veiculações;
   agora `in_slot = 2`, `bonus = 2`, base **4**.

Impacto e investido **diminuem** em campanhas com excedente ou com tocadas
fora do horário. Não tente reconciliar com um relatório antigo: o número velho
estava errado. O CPM sobe onde os impactos caem (numerador e denominador não
encolhem juntos) — o que ele NÃO faz é cair junto com o investido, porque a
bonificação continua no numerador dele.

Documento de pós-venda já enviado **não muda** — o `payload_json` é congelado no
envio (ver [post-sale.md](post-sale.md)).

### Déficit em dois tipos (D7)

Como `out_slot` deixou de abater o déficit, um dia inteiro veiculado no horário
errado — que antes lia "cumprido" — agora tem `deficit > 0`. E o painel
`/admin/station-failures` alimenta o **PDF de cobrança que vai pra emissora**. Pra
nunca acusar de ausência quem veiculou, o déficit é quebrado **por célula-dia** e só
depois somado:

```
deficit_off_slot = LEAST(deficit, out_slot)          -- tocou, no horário errado
deficit_absent   = GREATEST(0, deficit - out_slot)   -- não foi ao ar
deficit_off_slot + deficit_absent == deficit         -- invariante travada em teste
```

Aplicar `LEAST`/`GREATEST` sobre os totais já somados seria errado: `out_slot`
sobrando num dia passaria a desculpar o silêncio de outro. Detalhes de UI e PDF em
[admin-campaign-failures.md](admin-campaign-failures.md).

## Efeitos colaterais que mordem

- **O dia se assenta ao longo do dia.** Uma tocada gravada `out_slot` às 03:00 vira
  `bonus` às 10:00, quando a meta fecha dentro da faixa. É esperado (D5) — a tela
  está certa nos dois momentos, o conjunto é que mudou.
- **Retratar/ignorar/reatribuir uma tocada muda a categoria das OUTRAS da mesma
  célula-dia.** Sair do conjunto aprovado **libera uma vaga**, e a `out_slot` retida
  é promovida a `in_slot`. Esse acoplamento não existia no modelo antigo, onde cada
  tocada era classificada isoladamente. Qualquer caminho novo que tire ou devolva
  uma tocada ao conjunto aprovado **tem** que refechar a célula (`mutateApprovedSet`),
  senão as categorias ficam erradas **pra sempre** naquela célula caso nenhuma
  tocada nova caia nela — `in_slot` subnotificado e déficit inflado.
- **Nunca filtre "a linha que está saindo" na mão.** O `ApprovedDetectionsFilter` é a
  única definição do conjunto; o fechamento relê o conjunto depois de aplicar a
  mudança de estado.
- **Drift do reconciler ficou 100% real.** `CountProjectionDrift` não conta mais
  projeção de tocada não-aprovada (ela não tem veredito definido sob cota, logo
  produziria drift permanente). Todo valor no gauge hoje é drift visível ao usuário
  — ver [ProjectionDriftPersistent.md](../runbooks/ProjectionDriftPersistent.md).
- **Uma célula-dia cortada ao meio pela janela do reconciler é reavaliada inteira**,
  inclusive as tocadas anteriores ao `since`. É o comportamento correto: a meta é do
  dia, não da janela.
- **Material sem `type_id`** não pertence a célula nenhuma → N = 0 → `bonus` (ou
  `out_date` fora do período da campanha).

## ⛔ Deploy é tudo-ou-nada

Esta mudança **não tem deploy parcial seguro**:

- **0063** alarga o CHECK pra aceitar `'bonus'` (só `ALTER`, lock de milissegundos).
- **0064** renomeia o dado `orphan → bonus` (só `UPDATE`, row locks).
- **0065** faz a `daily_play_summary` / `daily_play_summary_for` **contarem** `'bonus'`
  e mudarem a fórmula do déficit.

Subir **0064 sem 0065** deixa a view procurando `'orphan'`, que passou a ser sempre
0: **bonificação lê zero em todas as telas e relatórios**. Do mesmo modo, o binário
tem que subir junto — `insights.go`, `daily_summary.go`, `detections.go` e o
`DayDetailModal` do modelo antigo leem `'orphan'`.

Ordem operacional:

1. **Backend primeiro** (`scripts/deploy.sh`; o frontend sobe sozinho no push —
   ver a memória `deploy-split-frontend-backend-ordering`). As migrations passam
   pelo `shadow_migration_test` (regra 4.8 do CLAUDE.md).
2. Confirmar o reconciler curando: `docker compose logs api | grep projrecon`. As
   últimas 48h convergem sozinhas em até 15 min.
3. **Só então** o frontend, senão a UI nova lê campos que a API ainda não devolve
   (o `DeficitSplit` tem fallback pro rótulo antigo justamente pra essa janela).
4. **Backfill retroativo** (`cmd/backfill-recategorize`) só depois de medir o delta
   contra um **clone** do dump de prod e o dono decidir o alcance (D9). O dry-run
   imprime a distribuição por categoria antes/depois; `orphan` residual deve ir a 0
   após o `--apply` — se não for, sobrou linha fora do escopo.

## Como testar manualmente

Célula com meta 2 na faixa `10:00–12:00`, uma emissora, um material:

1. Crie a regra "Spot 30s, 10:00–12:00, 2×/dia, todos os dias" na campanha.
2. Lance uma veiculação manual às **03:00** (`/detections` → dia → "Adicionar
   veiculação manualmente"). Ela nasce **`out_slot`** — a meta está aberta.
3. Lance **10:30** e **11:00**. As duas viram `in_slot` e a das 03:00 **vira
   bonificação** sozinha: a meta fechou dentro da faixa.
4. Lance **11:30**: `bonus` (excedente da cota do dia).
5. Abra o modal do dia: o saldo mostra `2 tocou · 0 faltou · 0 fora da faixa ·
   2 bônus`, e a célula da grade sai do vermelho.
6. **Célula zerada:** ajuste o dia pra `0` no popover de override e recarregue.
   Todas as tocadas viram **bonificação**, com a nota azul "o ajuste do dia zerou a
   meta — toda tocada conta como bonificação, não como fora do prazo". Nenhuma vira
   `out_slot`.
7. **Vaga liberada:** volte a meta pra 2, marque a tocada das 10:30 como
   "Desconsiderar" e recarregue: a das 03:00 (ou a de 11:30) assume a vaga —
   `in_slot` continua 2.

## Testes automatizados

| Arquivo | O que trava |
|---|---|
| `workers/internal/categorizer/settle_test.go` | tabela-verdade da spec, sem DB (incl. duas faixas somando N, empates, override zerado, carve-out `out_date`) |
| `workers/internal/catalog/settle_parity_test.go` | **paridade Go × SQL** (11 testes + sweep aleatório) |
| `workers/internal/catalog/detections_settle_test.go` | insert-path fecha a célula-dia inteira; advisory lock antes da leitura; campanha secundária não toca a base |
| `workers/internal/catalog/detections_resettle_test.go` | retratar libera vaga; tocada que volta retoma a cota; reatribuição refecha a célula de origem |
| `workers/internal/catalog/daily_summary_test.go` | `TestDailySummary_QuotaModel` — aritmética da view 0065 |
| `workers/internal/catalog/campaign_failures_test.go` | split `off_slot`/`absent` e a invariante da soma |

## Links

- [distribution-rules.md](../architecture/distribution-rules.md) — regras, overrides, gatilhos de recategorização
- [override-time-window.md](override-time-window.md) — faixa horária no override e a célula zerada
- [detection-count-consistency.md](../architecture/detection-count-consistency.md) — qual linha conta (conjunto aprovado)
- [projection-category-invariant.md](../architecture/projection-category-invariant.md) — invariante da projeção e reconciler
- [admin-campaign-failures.md](admin-campaign-failures.md) — os dois tipos de déficit e o PDF de cobrança
- [insights-dashboard.md](insights-dashboard.md) — base financeira do `/insights`
- [detections-day-plan.md](detections-day-plan.md) — bloco "Plano do dia" da `DayDetailModal`
- [material-specific-distribution-rules.md](material-specific-distribution-rules.md) — carve-out por material
- [multi-attribution.md](multi-attribution.md) — fan-out F-119
