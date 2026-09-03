---
status: implementado
ultima-verificacao: 2026-09-03
codigo-relacionado:
  - workers/internal/catalog/insights.go
  - migrations/0065_quota_aware_summary.up.sql
  - workers/internal/catalog/insights_test.go
  - workers/internal/api/handlers/insights.go
  - workers/internal/api/handlers/insights_test.go
  - workers/internal/api/router.go
  - workers/cmd/api/main.go
  - frontend/src/pages/InsightsPage.jsx
  - frontend/src/pages/InsightsPage.css
  - frontend/src/components/insights/
  - frontend/src/utils/exportInsights.js
  - frontend/src/utils/insightsCards.js
  - frontend/src/api/hooks.js (useInsights)
  - frontend/src/components/Sidebar.jsx
  - frontend/src/App.jsx
---

# Dashboard de Veiculação (`/insights`)

Tela única que consolida KPIs e gráficos demográficos por cliente × campanha(s) × período × emissoras. Substitui o PDF manual que o time comercial montava ao fim de cada campanha. Exporta como PNG ou PDF.

Spec original: [docs/superpowers/specs/2026-05-26-insights-dashboard-design.md](../superpowers/specs/2026-05-26-insights-dashboard-design.md).
Plano de implementação: [docs/superpowers/plans/2026-05-26-insights-dashboard.md](../superpowers/plans/2026-05-26-insights-dashboard.md).

## Quem vê o quê

| Role | Cliente | Campanhas | Emissoras |
|---|---|---|---|
| **admin** | escolhe um na barra de filtros | as do cliente escolhido | as de interseção das campanhas escolhidas |
| **cliente** (viewer) | chip read-only com o próprio nome — sem dropdown | só as suas (forçado pelo JWT scope no backend) | só as das suas campanhas |

O backend resolve isso via `auth.ClientScopeFromContext(r.Context())`. Quando o scope é não-nil (role viewer), `client_id` da query é ignorado e a tentativa de pedir campanhas de outro cliente → 403 ("forbidden") com erro contendo "cross-client" no log.

## Endpoint

`GET /api/v1/internal/insights?client_id=<uuid>&campaigns=<csv>&from=<YYYY-MM-DD>&to=<YYYY-MM-DD>&stations=<csv>&materials=<csv>`

| Param | Obrigatório? | Default | Notas |
|---|---|---|---|
| `client_id` | sim p/ admin; ignorado p/ viewer | — | Viewer é forçado pelo JWT |
| `campaigns` | sim | — | CSV de uuids, 1 ≤ N ≤ 50 |
| `from` / `to` | não | mês corrente | YYYY-MM-DD |
| `stations` | não | todas | CSV de uuids; vazio = todas |
| `materials` | não | todos | CSV de uuids, max 200; vazio = todos. **Liga o rateio** — ver abaixo |

Resposta: ver `catalog.InsightsPayload` — KPIs, class_pyramid, age_ranges, veiculacoes_breakdown, buckets, `material_prorated`, `material_share`.

## Filtro por material — exato na entrega, RATEIO no dinheiro (2026-08-18)

O passo 5 da barra recorta por material (o spot específico). O recorte tem duas
naturezas diferentes, e a distinção é a coisa mais importante desta seção:

| O que | Com filtro de material | Por quê |
|---|---|---|
| Impactos, Impactos no target, Veiculações, breakdown (donut), gênero/classe/faixa etária | **Exato** | `detection_attributions.commercial_id` diz qual material tocou |
| `in_slot`/`out_slot`/`out_date`/extras do gráfico | **Exato** | idem — o `CASE` em `aggregateBuckets` troca a fonte pra tocada quando o filtro está ativo |
| Investido, Bonificação (R$), CPM, CPM no target, "Programado" e déficit do gráfico | **Rateio** | O contrato **não tem dimensão de material** |

**Por que o financeiro não pode ser recortado.** O pricing é por
(campanha × emissora × **tipo** × dia): dois materiais de 30s dividem a mesma
célula de cota e o mesmo `unit_value`. No modo `consolidated` é pior — o valor é
um número único por (campanha, emissora), sem quebra alguma. Não existe no dado
"quanto deste contrato é do material X". O mesmo vale pro `expected` de
`distribution_rules`, que é a meta do **tipo** no dia.

**O rateio.** `share = veiculações do material ÷ veiculações da seleção`, na base
canônica de impactos (`in_slot + bonus` — ver
[client-target-pmm.md](client-target-pmm.md)). `out_slot` e `out_date` ficam de
fora do numerador **e** do denominador: não valem nada comercialmente, então não
podem puxar valor pro material. O fator multiplica `investido.contratado`,
`investido.executado`, `bonificacao.valor` e o `programado` de cada bucket.

O rateio do CPM é **por campanha**, não pelo fator global (`shares.ByCampaign`):
`impactos` já vem recortado por campanha, então usar o fator global faria uma
campanha onde o material tocou pouco herdar valor de outra onde tocou muito.

**A UI avisa.** `material_prorated: true` no payload renderiza a tarja
`.in-prorated-note` acima dos cards, com o `share` em %. Isso não é decoração:
esses números viram cobrança, e um valor rateado apresentado como valor contratual
é o tipo de coisa que vira discussão com cliente.

**Invariantes** (medidos contra a cópia de prod de 2026-08-17, cliente UNIUBE,
3 campanhas, ago/2026):

- Filtrar por **todos** os materiais que tocaram ⇒ resultado idêntico ao sem
  filtro, `share = 1.0000`. O filtro não move a base.
- **Σ dos materiais individuais = total sem filtro**, tanto em impactos
  (8.459.633) quanto em investido (R$ 76.697,66). O rateio particiona; não cria
  nem perde valor.

Vale a pena manter esses dois invariantes como teste ao mexer aqui.

## Fórmulas (autoridade é a spec; cópia rápida aqui)

| Métrica | Como é calculada |
|---|---|
| **Impactos** | `Σ_estação ((in_slot + bonus) × PMM)` — **base canônica de impactos do produto** ([client-target-pmm.md](client-target-pmm.md)); é o MESMO número do `/campaigns` (`total_audience`), travado por `TestInsights_FinancialBase_MatchesCampaigns`. `out_slot` e `out_date` **não entram** (não são impacto entregue). Estação sem PMM → não soma (mas conta em `stations_count`). **Mudou em 2026-08-17**: antes era `detections_count × PMM` (todas as categorias aprovadas) e vinha maior |
| **Impactos por gênero** | `Σ ((in_slot + bonus) × PMM × gender_pct / 100)` (percentuais em escala 0-100 no `stations.metadata.audience_profile`). Mesma base do KPI de impactos, de propósito: os rateios demográficos têm que somar de volta ao total exibido logo acima deles |
| **Veiculações total** | `COUNT(*)` das aprovadas — **as quatro** categorias. Este KPI é contagem, não impacto, e vem acompanhado do breakdown por categoria, então precisa fechar as quatro. Não confunda com a base de impactos |
| **CPM** | Padrão: `((investido_executado + bonificação) / impactos) × 1000` — **o numerador soma as duas parcelas** (ver §"O numerador do CPM inclui a bonificação"). Guard pra impactos=0 → CPM=0. Override por `campaigns.fixed_cpm` quando setado: média ponderada por impactos do `COALESCE(fixed_cpm, dynamic_cpm)` de cada campanha — ver [campaign-fixed-cpm.md](campaign-fixed-cpm.md). Herda o comportamento proporcional consolidado abaixo (em modo fornecedor as duas parcelas são as duas metades do mesmo total do `consolidatedSummary`, então a soma continua valendo — repartir a exibição em 2026-09-03 não moveu o CPM). É a MESMA expressão do `/campaigns` (`total_invested + total_bonus_value`) ÷ `total_audience`, travada por `TestInsights_FinancialBase_MatchesCampaigns` |
| **Bonificação** | Soma do valor das veiculações `bonus` da view `daily_play_summary` — desde a migration 0065 é a **contagem direta da categoria** `bonus` gravada pelo categorizador (excedente da cota do dia dentro da faixa + tocada sem meta). Valor é `unit_value × bonus_count` em modo per_insertion; em consolidated é `cv × bonus_na_janela / plano_da_campanha_INTEIRA` (mesma taxa estável por inserção do investido). Em per_insertion é o mesmo número que o `/campaigns` expõe em `total_bonus_value` (2026-08-17). **Desde 2026-09-03 o card NÃO some mais quando a seleção tem emissora consolidada**: exibe a parcela das por-inserção (a consolidada não tem preço por inserção com que precificar bônus) — ver §"Bonificação em seleção mista" |
| **Investido contratado** | `consolidated`: `cv × overlap_days/total_days`. `per_insertion`: `Σ_type (unit_value × expected_count)`. (Não é exibido em nenhum card hoje) |
| **Investido executado** | **Se QUALQUER emissora da seleção é `consolidated`** (regra do fornecedor): `Σ (consolidated_value × meses_decorridos + unit_value×(in_slot+bonus) das por-inserção)`. `consolidated_value` é MENSAL e **acumula por mês** (não varia com o filtro de período). **Desde 2026-09-03 o bônus das por-inserção NÃO fica mais embutido aqui**: o mesmo total é partido em `Investido = total − unit×bonus` e `Bonificação = unit×bonus`, o que faz esta célula bater com o `total_invested` do `/campaigns`. **100% `per_insertion`**: `Σ_type (unit_value × in_slot)` por veiculação, com Bonificação à parte. **Ver §"Consolidado: valor MENSAL que acumula por mês"**. ⚠️ Em campanha MISTA o `/campaigns` exibe um "Investimento" MENOR que este número pelo `unit_value × bonus` das emissoras por-inserção — lá o bônus vive num campo separado (`total_bonus_value`) e o modo fornecedor daqui o embute. **Só a exibição do dinheiro diverge; o CPM não** (o numerador soma as duas parcelas dos dois lados, e a soma é a mesma expressão) |
| **Buckets — programado** | `SUM(expected)` da view daily_play_summary |
| **Buckets — déficit** | `max(0, expected - in_slot)` |
| **Buckets — extras** | `count(detections WHERE category='bonus')` |

### `out_slot` saiu da base (2026-08-17)

Até esta entrega o executado somava `in_slot + out_slot` — ou seja, **faturava do
cliente uma veiculação que foi ao ar fora do horário comprado**, enquanto o
`/campaigns` a tratava como valendo zero. Pela decisão D3 do
[fechamento por cota](quota-aware-categorization.md), `out_slot` não vale nada:
não fatura, não bonifica e não abate o déficit. As duas telas passaram a
compartilhar a base `in_slot + bonus`.

**Os números caem — de propósito.** Duas quedas somadas: (1) o `out_slot` que saiu
do executado/CPM; (2) o excedente que era contado **duas vezes** (era `in_slot` e
reaparecia no termo `GREATEST(0, in_slot − expected)` do bônus da view). Comparar
com um relatório anterior a 2026-08-17 vai mostrar diferença — o número velho é que
estava errado. Pós-venda já enviado não muda (`payload_json` congelado).

### O `/campaigns` também parou de faturar o bônus (2026-08-17)

Complemento da entrega acima, do outro lado: o `/campaigns` somava
`unit_value × (in_slot + bonus)` num `total_invested` único. Bonificação é
veiculação **gratuita**, então isso inflava o "Investimento" do cliente. Agora
`total_invested = unit_value × in_slot` e a parcela que saiu vive num campo
novo, `total_bonus_value = unit_value × bonus` (só `per_insertion`; no
consolidado o pacote não varia com a entrega).

Isso fecha a paridade parcela a parcela entre as duas telas, travada por
`TestInsights_FinancialBase_MatchesCampaigns`:

```
insights.Investido.Executado == campaigns.total_invested      (unit × in_slot)
insights.Bonificacao.Valor   == campaigns.total_bonus_value   (unit × bonus)
insights.Impactos            == campaigns.total_audience      (in_slot + bonus)
insights.KPIs.CPM            == (total_invested + total_bonus_value)
                                ÷ total_audience × 1000
```

Medição no clone de prod em 2026-08-17 (1.076 campanhas): investido agregado
7.433.634 → 6.949.640 (−6,5%). `insertions`, `audience` e `audience_target`
**não mudaram** — só o dinheiro exibido. **O CPM também não mudou**: ver a seção
abaixo.

### O numerador do CPM inclui a bonificação (2026-08-17)

> **Definição do dono, confirmada:** *"valor investido: só o que o cliente pagou
> — impactos: o que o cliente pagou + o que veio de bônus — CPM: valor investido
> + valor bonificado dividido pelos impactos"*.

```
Investimento = unit_value × in_slot                    ← só o que o cliente pagou
Impactos     = pmm × (in_slot + bonus)                 ← pago + bônus
CPM          = (investido + bonificado) ÷ impactos × 1000
             = unit_value × (in_slot + bonus) ÷ impactos × 1000
```

**A RAZÃO — leia antes de "simplificar" isso de volta:** o CPM mede a
**eficiência da mídia entregue a preço de tabela**, não a eficiência da
negociação. A veiculação de bônus é mídia real que foi ao ar, e ela já está no
denominador (impactos conta `in_slot + bonus`), então tem que estar no numerador
ao preço de tabela dela. Se o numerador fosse só o valor pago, uma campanha com
muita bonificação exibiria um CPM artificialmente baixo, **incomparável com o de
qualquer outra campanha** — e o CPM existe justamente pra comparar campanhas.

Consequência prática: tirar o bônus do "Investimento" (mudança acima) **não
mexeu no CPM**. Entre a mudança do investido e esta correção o CPM agregado do
clone caiu de R$ 6,72 pra R$ 6,28 (−6,5%); com o numerador correto ele volta a
**R$ 6,7162**, o valor de antes. As campanhas que mais tinham se distorcido são
as de bônus alto sobre investimento baixo: `207 ENGIE SÃO SALVADOR` (R$ 15,65 →
R$ 252,97), `254 ENGIE AFOGAMENTO` (17,53 → 60,92), `207 ENGIE PONTE DE PEDRA`
(41,53 → 126,26), `211 CORTEVA` (16,93 → 39,96), `223 TIROL` (4,57 → 6,80).

Onde isso vive (mexeu num, mexa nos outros):

| Site | Numerador |
|---|---|
| `catalog.Insights.Compute` (`/insights` CPM e CPM no target) | `inv.Executado + bon.Valor`, calculado **depois** do override consolidado |
| `catalog.Insights.computeCPM` — fast path | o mesmo valor recebido por parâmetro |
| `catalog.Insights.computeCPM` — slow path (`fixed_cpm`) | por campanha: `pi_executado + pi_bonus` (per_insertion) / `cv × (LEAST(1, entregue÷plano) + bônus÷plano)` (consolidated) |
| `CampaignsPage.jsx` + `DashboardPage.jsx` | `total_invested + total_bonus_value` |
| `postsale.applyOverrides` + `PostSaleSteps/ContentStep.jsx` | `valor_entregue + bonificação` (os dois já com override do admin) |

Campanha com `fixed_cpm` continua exibindo o valor fixo; só a dica de "CPM
dinâmico seria X" usa a fórmula corrigida.

> **Há uma guarda de teste contra "simplificar" isso.** `TestInsights_FinancialBase_MatchesCampaigns`
> ([`insights_test.go:1359-1370`](../../workers/internal/catalog/insights_test.go)) monta
> o fixture de modo que o numerador só-do-pago e o numerador correto dão valores
> **diferentes**, e **falha explicitamente** se o CPM calculado for igual ao numerador
> só-do-pago. Ela existe porque essa exata regressão já aconteceu uma vez (commit
> `b4d8d45` tirou o bônus do investido e levou o CPM junto sem querer, −6,5% agregado,
> consertado em `c749c47`). Se você "limpar" o numerador, este teste fica vermelho —
> **conserte o código, não o teste.**

**Isso também fecha a divergência de pricing MISTO** entre `/campaigns` e
`/insights`. Em modo fornecedor o `/insights` zera a Bonificação e embute o bônus
no Investido, enquanto o `/campaigns` mostra as duas parcelas separadas — mas a
SOMA é a mesma expressão dos dois lados
(`pacote × meses + unit × (in_slot + bonus)`). Verificado no clone: as 25
campanhas com emissora consolidada têm delta **0,00** entre os dois numeradores.
Travado por `TestInsights_Compute_Mixed_MatchesCampaignsFormula`.

Divergência conhecida que **permanece** (pré-existente, ortogonal): dentro do
próprio `/insights`, quando a seleção tem emissora consolidada, o fast path usa
o total do `consolidatedSummary` (`cv × meses_decorridos`) e o slow path (o que
roda quando alguma campanha tem `fixed_cpm`) usa o Modelo B
(`cv × entregue ÷ plano_cheio`). Ligar um `fixed_cpm` numa seleção consolidada
pode, por isso, mover o CPM das campanhas vizinhas.

### Divergências CONHECIDAS E ACEITAS (decididas em 2026-08-17)

As quatro abaixo foram levantadas na auditoria da entrega de cota, **medidas** e
**mantidas por decisão do dono** em 2026-08-17. Estão aqui pra ninguém "consertar"
nenhuma delas achando que é bug novo. Se for mexer, é mudança de produto — leve
pro dono antes.

**Uma delas já não vale:** a #1 (Bonificação zerada em seleção com consolidada)
foi revertida a pedido do dono em 2026-09-03 e está reescrita abaixo. As outras
três seguem de pé.

#### 1. ~~O modo fornecedor zera a Bonificação~~ — RESOLVIDO em 2026-09-03

> Esta era uma das quatro divergências aceitas em 2026-08-17. O dono pediu a
> mudança em 2026-09-03: *"tirando esse valor de todas, a gente perde um insight
> valioso"*. O que segue descreve o estado NOVO; o antigo fica registrado
> abaixo porque relatório tirado antes dessa data mostra outro Investido.

`hasConsolidated` continua sendo `true` quando **qualquer** emissora da seleção
tem pricing `consolidated`
([`insights.go`](../../workers/internal/catalog/insights.go)) — o gatilho é por
seleção, e isso não mudou. O que mudou é o que ele faz com o dinheiro.

**Antes:** `inv.Executado = total; bon = BonificacaoK{}` — o bônus das
por-inserção ficava embutido no Investido e o card de Bonificação sumia da tela.
Como **14 dos 28 clientes** têm ao menos uma emissora consolidada na seleção
típica, metade da base nunca via a bonificação precificada.

**Agora:** o mesmo total é **partido em duas parcelas exibidas**:

```
Investido   = total − unit×bonus   (pacote × meses + o que o cliente pagou)
Bonificação = unit×bonus           (o que veio de graça, a preço de tabela)
```

A soma é idêntica à de antes, então **o numerador do CPM não se moveu** e a
paridade segue travada por `TestInsights_Compute_Mixed_MatchesCampaignsFormula`.
É repartição de exibição, não número novo.

**Efeito colateral bom:** essa é exatamente a partição que o `/campaigns` já
usava (`total_invested` separado de `total_bonus_value`), então a divergência de
**R$ 271.179** no "Investimento" de campanhas mistas — medida no clone de prod em
2026-08-17 e aceita na época — **fecha**. O teste passou a exigir isso parcela a
parcela (`Investido == TotalInvested`, `Bonificacao.Valor == TotalBonusValue`).

**O que NÃO mudou:** a emissora consolidada continua sem valor de bônus (§2
abaixo), o gatilho continua por seleção, e o pedaço consolidado do Investido
continua contando meses por argumentos diferentes nas duas telas (§3).

**Quem lê relatório antigo precisa saber:** numa seleção mista, o "Investido"
exibido **caiu** exatamente o valor que agora aparece em "Bonificação". Nada foi
perdido e o CPM é o mesmo — mas os dois cards, lado a lado, somam o que antes
aparecia num só.

#### 2. Consolidado não tem valor de bonificação — de propósito

Em `consolidated` **não existe `unit_value`**: o preço é um pacote pela emissora, não
por inserção. Logo não há taxa com que precificar a tocada de bônus, e
`total_bonus_value` do `/campaigns` é **0** nesse modo (`FinancialsByCampaign`). Não é
omissão — é ausência de dado. Inventar uma taxa (ex.: `cv ÷ plano`) seria criar um
preço que ninguém contratou.

Isso **continua valendo depois da mudança de 2026-09-03** e é o que define o
recorte do card: numa seleção mista, a Bonificação exibida cobre só as emissoras
por-inserção, e o rótulo diz isso ("Bonificação (por inserção)"). Numa seleção
**100% consolidada** o card continua sumindo — ali o zero significaria "não há
preço", e exibi-lo afirmaria "não houve bônus", que é outra coisa. As tocadas de
bônus da consolidada continuam contadas em impactos e no breakdown de
veiculações; o que não existe é o valor delas.

#### 3. `consolidated_value` é MENSAL, e o mesmo campo lê três números diferentes

O campo cadastrado é o valor **por mês** (regra de 2026-07-08). O dono decidiu, em
2026-08-17, **não renomear o rótulo na UI**. Consequência que um leitor precisa saber
antes de comparar telas:

| Onde | O que exibe a partir de `consolidated_value` |
|---|---|
| `/detections` (pill "Valor" da emissora, `DistributionGrid.jsx:492-495`) | **o valor cru** — o mensal, sem multiplicar por mês nenhum e sem olhar o período visível |
| `/insights` (Investido executado) e `/campaigns` | `cv × meses_decorridos` (`monthsElapsedSQL`, virada de mês, limitado ao filtro de período) |
| `/insights` slow path do CPM (quando há `fixed_cpm` na seleção) | Modelo B: `cv × entregue ÷ plano_da_campanha_inteira` |

Ou seja: numa campanha de 3 meses, o mesmo cadastro de R$ 1.000 lê **1.000** no
`/detections`, **3.000** no `/insights` no 3º mês, e um terceiro valor no CPM se
houver `fixed_cpm` na seleção. **Conhecido e aceito.**

#### 4. `total_bonus_value` existe na API e não é renderizado

`GET /campaigns/financials` devolve `total_bonus_value` desde 2026-08-17, e **nenhuma
tela o desenha** como card próprio — ele só entra no numerador do CPM
(`CampaignsPage.jsx`, `DashboardPage.jsx`). É deliberado: o campo nasceu pra tirar o
bônus do "Investimento" sem inventar UI nova na mesma entrega. Renderizá-lo é decisão
de produto pendente, não bug.

### "Impactos" também mudou de base (2026-08-17)

O KPI **Impactos** (e o "no target", e os rateios de gênero/classe/idade) usava
`COUNT(*) × PMM` sobre **todas** as categorias aprovadas. Passou a usar
`(in_slot + bonus) × PMM` — a base canônica descrita em
[client-target-pmm.md](client-target-pmm.md). O `/detections` (grid + CSV/PDF da
grade), que contava só `in_slot`, e o PDF/CSV de campanha, que contava tudo,
convergiram para a mesma base na mesma entrega.

Efeito medido no clone de prod (junho/2026 em diante, 56.219 veiculações
aprovadas): impactos do `/insights` **caem 0,874%** no agregado (os 201 `out_slot`
+ 554 `out_date` que saíram) e os do `/detections` **sobem 13,0%** no agregado (a
bonificação que entrou). Por campanha, o `/insights` mexe pouco e só onde há
`out_slot`/`out_date` — 189 RÔGGA COPA 0,0%, 244 UNIUBE −0,12%, 227 PARAFLU
−3,76% — mas o `/detections` muda MUITO onde o bônus é concentrado: 189 RÔGGA vai
de 2.620 K pra 13.969 K (+433%, porque 1.014 das 1.307 tocadas são `bonus`), 244
UNIUBE de 14.454 K pra 27.020 K (+87%), 227 PARAFLU de 1.710 K pra 4.549 K (+166%).

**O CPM sobe na mesma proporção em que os impactos caem** (o numerador,
`investido_executado`, não mudou): 227 PARAFLU +3,91%, 244 UNIUBE +0,12%, 189
RÔGGA 0% — e 189 tem `fixed_cpm`, então o card nem é dinâmico. Não é o CPM
"quebrando": é o denominador deixando de contar audiência que não foi entregue.

### "Extras" no chart × "Bonificação" no KPI

Hoje **os dois leem a mesma coisa**: a categoria `bonus`. As categorias são
disjuntas (cada tocada tem exatamente uma), então plotar `bonus` ao lado de
`in_slot` no mesmo gráfico não duplica nada.

> Antes da 0065 o `bonus` da view era sintetizado (`max(0, in_slot − expected) + orphan`)
> e **sobrepunha** o `in_slot`; por isso o gráfico lia `orphan` puro e o KPI lia a
> definição ampla. Essa distinção deixou de existir.

Documentado nos comentários do `aggregateBuckets` em [workers/internal/catalog/insights.go](../../workers/internal/catalog/insights.go).

### Consolidado: valor MENSAL que acumula por mês (estilo fornecedor)

**Regra vigente (2026-07-08, fim da tarde):** o `consolidated_value` cadastrado é o valor **POR MÊS**, não o total. A equipe cadastra o mensal e o sistema acumula conforme os meses passam (ex.: campanha de 3 meses × R$1000 → total 3000, mostrando 1000 no mês 1, 2000 no mês 2, 3000 no mês 3).

Se **QUALQUER emissora da seleção** tem pricing `consolidated`, o `/insights` entra em **modo fornecedor**:

- **Investido** = `Σ_estação (consolidated_value × meses_decorridos das consolidadas + unit_value × (in_slot + bonus) das por-inserção)`
  (`Insights.consolidatedSummary`). Era byte-a-byte a fórmula do `/campaigns` até
  2026-08-17, quando o `/campaigns` tirou o bônus do investido
  (`Campaigns.FinancialsByCampaign`). **Numa campanha 100% consolidada continuam
  iguais** (o bônus não tem preço por inserção); numa **mista** o `/campaigns`
  fica menor pelo `unit_value × bonus` das emissoras por-inserção. Aqui o card de
  Bonificação some, então a parcela não tem onde aparecer separada.
  - **`meses_decorridos`** = nº de **meses de calendário** da campanha que **(a)** já começaram até **hoje** e **(b)** estão dentro da **janela de período selecionada** `[from, to]`. Incremento na **virada do mês** (todo dia 1º), não no aniversário de 30 dias: o 1º mês conta a partir da data de início (0 antes dela); ao entrar num novo mês soma +1; limitado ao mês de fim. Ex.: campanha **09/06–08/07** conta **1** em junho e **2 a partir de 01/07**. Função `monthsElapsedSQL` (via `generate_series`).
  - **Respeita o filtro de período:** filtrar só junho de uma campanha de 3 meses → 1 mês (não a campanha toda). O per-inserção também é escopado a `[from, to]`. Bate com o `/campaigns` (que não tem filtro) quando o filtro cobre a campanha inteira até hoje.
  - **`hoje`** vem do handler (America/Sao_Paulo); testes injetam via `InsightsParams.Today`; zero → sem cap de hoje (só o filtro escopa).
- **Bonificação**: **some** — o backend zera e o frontend **não renderiza o card** (grid de cards vira 4 colunas). No fornecedor fica zerado.
- **CPM**: usa o `fixed_cpm` da campanha **quando existe**; sem ele, cai no dinâmico `(total + bonificação) ÷ impactos × 1000`. ⚠️ **Não presuma que consolidado tem `fixed_cpm`** — este doc afirmava "consolidado sempre tem cadastrado" e isso é **falso**: no clone de prod de 2026-08-17, **18 das 25** campanhas com emissora consolidada estavam **sem** `fixed_cpm`. Nada obriga o cadastro (a coluna é `NULL`-able, `campaigns.fixed_cpm NUMERIC(12,2) NULL`, e o Step 6 do wizard não exige o campo em modo consolidado), então o caminho dinâmico é o **comum**, não a exceção.
- **Flag `consolidated: true`** no payload dispara o comportamento no frontend.
- **Campanha 100% `per_insertion`**: nada muda — segue por veiculação, com Bonificação.

Implementação: `catalog.Insights.consolidatedSummary(…, today)` calcula o total + a flag; `Compute` sobrescreve `inv.Executado` e zera `bon` quando `hasConsolidated`. O `/campaigns` (`FinancialsByCampaign(…, today)`) usa a MESMA `monthsElapsedSQL`. Frontend: `KpiCards` esconde a Bonificação e `InsightsPage` aplica `in-row--cards--4` quando `data.consolidated`. **Compatível com campanhas de 1 mês** (meses_decorridos = 1 → inalterado).

> **Nota:** o cálculo **Modelo B (proporcional)** abaixo continua existindo no `aggregateInvestment` (e nos testes diretos), mas é **sobrescrito** pelo total fixo para consolidado no `Compute` — preservado caso a regra mude de novo. Vale hoje só como o número por-veiculação de campanhas `per_insertion`.

<details><summary>Modelo B — proporcional ao período (camada de baixo, sobrescrita em consolidado)</summary>

Em campanha com pricing `consolidated`, o **Investido executado** (do `aggregateInvestment`, hoje sobrescrito) é **proporcional ao período selecionado**:

```
executado = cv × LEAST(1, entregue_na_janela ÷ plano_da_campanha_INTEIRA)
bonus     = cv ×        (excedente_na_janela ÷ plano_da_campanha_INTEIRA)
```

A chave é o **denominador = plano da campanha inteira** (fixo, `SUM(expected)` em `[start_date, end_date]`), **não** o plano da janela. Isso dá:

- **Proporcional:** "de 19/06 a 30/06" mostra a fração do contrato entregue nesse recorte; ampliar o período **soma**. `cv ÷ plano_total` é a taxa estável por inserção.
- **Monotônico / sem deflação por dia futuro:** dia ainda-não-veiculado entrega 0 no numerador, então alargar a janela pra frente nunca faz o número cair (nem precisa de clamp de "hoje").
- **Cap em 100% + bônus:** over-delivery (entregue > plano) capa o Investido no contrato; o excedente aparece **só** na Bonificação, valorizado à mesma taxa por inserção. Fim do double-count.
- **Déficit reduz (correto):** emissora que entregou menos que o plano mostra `< contrato` — reflete a não-entrega.

**Campanha ativa:** enquanto a campanha não termina, "campanha inteira" mostra o **entregue até agora** (não o contrato cheio), completando conforme veicula. É o comportamento por-entrega (não por-tempo) — decisão de negócio registrada na spec.

`per_insertion` é aditivo e não muda (`Σ unit_value × tocadas`). Os dois pontos que replicam a fórmula (`aggregateInvestment` via CTEs `cs_window`/`cs_plan`, e o slow-path de `computeCPM`) usam o mesmo denominador de plano cheio. Spec: [docs/superpowers/specs/2026-07-08-insights-consolidated-period-proportional-design.md](../superpowers/specs/2026-07-08-insights-consolidated-period-proportional-design.md).

</details>

## Granularidade automática do gráfico 4

- ≤ 31 dias filtrados → buckets diários (`YYYY-MM-DD`)
- > 31 dias → buckets mensais (`YYYY-MM`)

Decidido no backend (`aggregateBuckets`). Frontend formata o label localmente.

## Exportação PNG / PDF

- **PNG:** `html2canvas` captura `<div ref={dashboardRef} className="in-body">`. Scale 2 (retina). Nome do arquivo: `dashboard-<slug-cliente>-<from>-<to>.png`.
- **PDF:** A4 paisagem, 2 páginas. Capa com logo, nome do cliente, período, contagem de campanhas. Página 2 = PNG do dashboard centralizado.

Reutilizar o padrão de [pdfReport.js](../../frontend/src/utils/pdfReport.js) — mesmo `jsPDF`.

## Como adicionar uma nova métrica

1. Adicionar campo no struct `InsightsKPIs` (ou similar) em [workers/internal/catalog/insights.go](../../workers/internal/catalog/insights.go).
2. Calcular na SQL apropriada (`aggregateCore`, `aggregateInvestment` ou `aggregateBuckets`).
3. Adicionar teste em `insights_test.go` (tem fixtures `insSeedStation`/etc).
4. Atualizar o struct TypeScript implícito no frontend e renderizar (card ou chart).

## Troubleshooting

- **Impactos vêm baixos:** verifique se as estações têm PMM e perfil cadastrado. `psql -c "SELECT name, pmm, metadata->'audience_profile' FROM stations WHERE pmm IS NULL LIMIT 10"`. Edite em `/stations/:id/edit`.
- **Export PNG vazio:** confira se `dashboardRef.current` está mountado (a captura roda em `handleExport*`). Se o usuário clica antes do payload carregar, o ref é válido mas o conteúdo é o empty state — comportamento esperado.
- **PDF cortado:** ajustar `margin` em [exportInsights.js:73](../../frontend/src/utils/exportInsights.js) ou diminuir `scale` do html2canvas (atualmente 2).
- **Performance ruim (>2s):** rodar com tracing habilitado (Jaeger) e identificar o CTE lento. Candidatos: `aggregateCore` se há muitas estações, `aggregateInvestment` se há muitas campanhas. Se passar de 2s P95 em prod, considerar materialized view por mês.
- **A tela levava ~12s e agora leva <1s (2026-08-18).** `aggregateInvestment` e `computeCPM` liam `daily_play_summary` — **view sem pushdown de predicado**: cada leitura materializava o resumo do banco INTEIRO (todas as campanhas, todo o histórico) pra depois descartar 99%. Eram 3 leituras num statement, e mais 3 no slow path do `fixed_cpm`. Medido na cópia de prod de 2026-08-17 (ENGIE, 9 campanhas, ago/2026):

  | Etapa | Antes | Depois |
  |---|---:|---:|
  | fetchCampaigns | 249 ms | 183 ms |
  | aggregateCore | 552 ms | 423 ms |
  | **aggregateInvestment** | **10.793 ms** | **161 ms** |
  | aggregateBuckets | 258 ms | 40 ms |
  | consolidatedSummary | 82 ms | 26 ms |
  | computeCPM | 5 ms | 2 ms |
  | targetLabel | 15 ms | 5 ms |
  | **total** | **11.954 ms** | **840 ms** |

  A correção foi trocar a view pela função `daily_play_summary_for(from, to, campanhas)` (migration 0052), materializada **uma vez** por statement numa CTE `dps` e reusada pelas três CTEs — o intervalo pedido é a união das três janelas (`MIN(start_date)`..`MAX(end_date)`), e cada CTE mantém o próprio `BETWEEN`. Equivalência provada no banco: `daily_play_summary WHERE for_date BETWEEN lo AND hi` × `daily_play_summary_for(lo, hi, NULL)` devolvem **23.727 linhas cada, 0 divergências** (`EXCEPT ALL` nos dois sentidos), e os 4 números do `aggregateInvestment` batem em **24/24 casos** (8 clientes × 3 janelas, incluindo janela que ultrapassa o fim das campanhas).

  **Cuidado ao repetir isso em outro consumidor:** a função corta linhas fora de `[p_from, p_to]`. Quem agrega `out_date` **sem lower bound** não pode migrar — ver a armadilha em [daily-play-summary-for](../operations/migrations.md) e os 4 consumidores que continuam na view em `campaign_failures.go` (Q3/Get/ListHistorical), deliberadamente.
- **Cliente com 50+ campanhas:** o select faz busca local; se ficar lento, virtualizar o `RSelect` ou adicionar busca server-side.

## Limitações conhecidas

- "Extras" no gráfico 4 e a Bonificação do KPI leem a mesma categoria `bonus`; o gráfico filtra pelo literal `'bonus'` (não pelo sinônimo legado `'orphan'`), então uma linha ainda não convertida pelo backfill/reconciler não aparece ali. Convergem em até ~15 min pelo `projrecon`.
- Investido em modo `consolidated` prorrateia linearmente por dias (`overlap_days/total_days`), sem considerar distribuição irregular de slots dentro da campanha.
- 🔴 **Materiais sem `type_id` ficam ausentes da view `daily_play_summary`** — afeta os buckets do gráfico 4 (não aparecem ali), MAS continuam contando em `aggregateCore` (impactos + breakdown), que lê detections direto. A mesma view alimenta a **grade do `/detections`**, então a veiculação **existe aqui e não existe lá**: medido em **28% das veiculações de uma campanha-mês** (2026-08-17). **F-130** em [follow-ups-fase2.md](../roadmap/follow-ups-fase2.md).
- 🔴 **O CPM de uma campanha muda conforme quais OUTRAS campanhas estão na seleção.** Basta uma campanha da seleção ter `fixed_cpm` (`EXISTS` sobre o array inteiro, `insights.go:777-789`) pra todas caírem no slow path, em que cada uma usa o numerador **dela** em vez da fatia do agregado. Medido: **7 campanhas** com variação de até **~3,7×** no mesmo período. **F-131**.
- 🔴 **Emissora com veiculação e SEM linha de pricing conta aqui e não conta no `/campaigns`.** O `aggregateCore` não toca pricing (`insights.go:431-441`); o `/campaigns` tem `campaign_station_pricing` como FROM (`campaigns.go:583-591`), então a linha nem existe lá. Medido: **11 veiculações** em prod. Assimetria interna: o *dinheiro* do `/insights` concorda com o `/campaigns` (o `aggregateInvestment` também parte de pricing), mas `impactos`/`veiculacoes_total`/`stations_count` não — a emissora sem preço **infla o denominador do CPM**. **F-132**.
- Estação sem `audience_profile.gender` (ou `socialClass`, `ageRanges`) → não soma na dimensão correspondente. O card de gênero pode subestimar quando muitas estações estão sem perfil.

## Decisões de modelagem que diferiram do plano original

Durante a implementação foram identificados desvios da spec/plano:

1. **Coluna era `metadata`, não `meta`.** O plano supunha `s.meta`, mas o DB tem `s.metadata` (Go aliasa para `Meta` no struct).
2. **Percentuais 0-100, não 0-1.** Os percentuais em `audience_profile` (gender, social_class, age_ranges) estão em escala 0-100. SQL multiplica por `/100.0` ao aplicar.
3. **Pricing é por (campaign, station), não por campanha.** A tabela existente é `campaign_station_pricing` (modo + consolidated_value) + `campaign_station_type_pricing` (unit_value por type). O plano supunha `campaigns_pricing` (que não existe).
4. **`daily_play_summary` view foi reaproveitada.** O plano expandia `generate_series` + `weekday_mask` manualmente; a view já faz isso e ainda aplica overrides — usamos ela em `aggregateInvestment` e `aggregateBuckets`.

Essas decisões estão refletidas nos commit messages individuais (`git log workers/internal/catalog/insights.go`).
