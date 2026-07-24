# Padronização financeira /campaigns × /insights — base única `in_slot+bonus`

**Data:** 2026-07-24
**Autor:** Dereck + Claude
**Status:** aprovado (design) — pendente plano de implementação

## 1. Problema

Os números financeiros de uma mesma campanha divergem entre `/campaigns` (bloco
financeiro) e `/insights` (KPI cards): Impactos, Impactos no target, CPM no target
e Investido. A investigação (2026-07-24) achou **três eixos independentes** de
divergência, mais uma causa estrutural:

1. **Janela de tempo.** `/insights` abre no **mês corrente** por default
   (`handlers/insights.go`); `/campaigns` mostra a **campanha inteira** (sem
   filtro, `catalog/campaigns.go` `FinancialsByCampaign`).
2. **Base de contagem dos impactos.** `/insights` conta **todas as detecções
   aprovadas** (`in_slot + out_slot + out_date + orphan`, via `aggregateCore`
   contando `detection_attributions`); `/campaigns` conta só **`in_slot + bonus`**
   (via view `daily_play_summary`). `bonus = GREATEST(0, in_slot - expected) + orphan`
   (migration `0052`, linha 108) — depende do `expected` (plano de distribuição),
   que só existe na view.
3. **Fórmula do investido per_insertion.** `/insights` usa o "Modelo B"
   (`unit_value × (in_slot + out_slot)`); `/campaigns` usa `unit_value ×
   (in_slot + bonus)`. No consolidado as fórmulas já coincidem de propósito
   (`insights.go` `consolidatedSummary` ≡ `campaigns.go`), variando só a janela.

**Causa estrutural:** os dois caminhos de cálculo são **código separado** e
derivaram no tempo (o Modelo B foi aplicado só ao `/insights`). Enquanto forem
duas implementações, voltarão a divergir.

## 2. Decisão

Padronizar **as duas telas** na **base A** (o comportamento atual do `/campaigns`),
com janela e núcleo de cálculo unificados:

- **Base A dos impactos:** `impactos = Σ (in_slot + bonus) × pmm`;
  `impactos_target = Σ (in_slot + bonus) × pmm_target`.
- **Investido base A:** per_insertion `Σ_tipo unit_value × (in_slot + bonus)`;
  consolidado `consolidated_value × meses_no_período`.
- **Janela única:** as duas telas escopadas por `from/to`, default **mês corrente**.
  O `/campaigns` ganha um seletor de período.
- **Núcleo compartilhado:** um único helper calcula "quanto tocou / quanto custou";
  as duas telas o consomem — impossível divergir para o mesmo `(campanhas, from, to)`.
- **`/insights` migra a base inteira** (KPIs + veiculações + gráficos demográficos),
  não só os KPIs financeiros — pra o dashboard ficar internamente coerente.
- **Rótulo visual do período** nas duas telas.

### 2.1 Decisões pontuais fechadas no brainstorm

- **Alcance no `/insights`:** dashboard inteiro (gênero/classe/idade/share/resumo
  diário passam a usar `in_slot+bonus`).
- **Seletor de período no `/campaigns`:** sim, default mês atual, ajustável.
- **Contador "X de Y emissoras com target":** base **de quem tocou na janela**
  (period-dependent) nas duas telas.
- **"Bonificação" do `/insights`:** fica **como está** (métrica só do `/insights`,
  sem equivalente no `/campaigns`). Só investido/impactos/CPM migram pra base A.

### 2.2 Consequências aceitas

- **Material sem `type_id` sai do `/insights`.** A view `daily_play_summary` é
  chaveada por `type_id`; material sem tipo não entra. Hoje o `/insights` conta
  essas detecções (contagem crua). No `/campaigns` já é assim.
- **Números de cliente mudam retroativamente** (cobrança). Ver §6 (segurança).

## 3. Arquitetura da solução

### 3.1 Núcleo financeiro compartilhado (`catalog`)

Um helper único — a **única** fonte de "plays + dinheiro" — assinatura conceitual:

```
FinancialBase(ctx, campaignIDs, stationIDs, from, to, today)
  → []{ campaign_id, station_id, client_id, plays, invested }
```

- Lê `daily_play_summary_for(from, to, campaignIDs)` (migration `0052`), já escopada
  pela janela.
- `plays = Σ (in_slot + bonus)` por (campanha, emissora) somando sobre tipos/dias.
- `invested` por (campanha, emissora):
  - **per_insertion:** `Σ_tipo unit_value × (in_slot + bonus)`
    (join `campaign_station_type_pricing` por `type_id`).
  - **consolidado:** `consolidated_value × monthsElapsedSQL(start, end, today, from, to)`
    (independe de plays; vem de `campaign_station_pricing`, igual à CTE
    `consolidated_inv` de hoje, mas **escopada à janela**).

O helper **não** faz o join de `pmm`/`pmm_target`/demografia — esses ficam nos
consumidores (join estável, já hand-written idêntico em todo lugar). O que ele
centraliza é exatamente o que estava divergindo (`plays` e `invested`).

### 3.2 Consumidor `/campaigns` (`FinancialsByCampaign`)

Reescrito pra chamar o helper e agregar **por campanha**:

- `total_audience = Σ plays × pmm`
- `total_audience_target = Σ plays × pmm_target`
- `total_invested = Σ invested`
- `total_insertions = Σ plays`
- `stations_with_target = COUNT(DISTINCT station_id)` onde `pmm_target` presente
  **e** `plays > 0` na janela (period-dependent, §2.1).

Assinatura ganha `from, to`. Join `stations` (pmm) + `client_station_pmm`
(pmm_target por `client_id` da campanha).

### 3.3 Consumidor `/insights` (`aggregateCore`)

Reescrito pra usar a **mesma base** do helper (`plays` por emissora) em vez de
contar `detection_attributions`:

- KPIs: `impactos = Σ plays × pmm`; `impactos_target = Σ plays × pmm_target`;
  `veiculações = Σ plays`.
- Demografia: `Σ plays × pmm × pct` (gênero/classe/idade) — o `pct` vem de
  `stations.metadata`, join por emissora, igual a hoje; só troca `det_count` por
  `plays`.
- `stations_with_target`/`stations_count`: base de quem tocou na janela
  (`plays > 0`), mesma régua do `/campaigns`.
- **Investido (executado):** passa a vir do campo `invested` do helper (base A) —
  numerador do CPM. Isso substitui, para o número de investido, o Modelo B do
  `aggregateInvestment` per_insertion (`in_slot + out_slot` → `in_slot + bonus`);
  o `consolidatedSummary` já era base A.
- **CPM no target:** segue `investido ÷ impactos_target × 1000` (backend), agora
  com numerador e denominador na base A.
- **Bonificação:** inalterada (continua no Modelo B). Ver a ressalva em §8 — ela
  passa a usar um modelo diferente do investido base-A e não reconcilia com ele.
- **Breakdown de categorias** (`in_slot/out_slot/out_date/orphan`) continua
  disponível da view (tem as colunas) pra qualquer UI informativa, mas **não** entra
  em impactos/demografia.

### 3.4 Janela de período unificada

- **`/insights`:** já tem seletor (default mês). Mantém.
- **`/campaigns`:** novo seletor de período. Como a tela é uma **lista** de várias
  campanhas (cada uma com seu start/end), o seletor aplica um `from/to` **global**
  a todas as linhas. Presets:
  - **"Mês atual"** (default) — 1º ao último dia do mês corrente (America/Sao_Paulo).
  - **"Acumulado"** — `from` bem antigo (ex.: `0001-01-01`/época), `to = hoje` →
    reproduz o comportamento antigo (total da campanha até hoje) para cada linha,
    já que as plays só existem dentro das datas de cada campanha.
  - **"Personalizado"** — range livre.
- Endpoint `GET /campaigns/financials` passa a aceitar `from`/`to` (query params);
  handler parseia com default mês corrente; `FinancialsByCampaign` ganha os dois args.
- O `monthsElapsedSQL` do consolidado passa a ser escopado à janela nas **duas**
  telas (hoje o `/campaigns` passa `start/end` → sem escopo).

### 3.5 Rótulo visual do período

Nas duas telas, um rótulo curto do período ativo junto ao bloco financeiro:

- Mês de calendário cheio → `"jul/2026"`.
- Range parcial/custom → `"01–24 jul 2026"`.

Deixa explícito que o número é daquela janela — mata a confusão "por que os totais
não batem". Componente simples, pode ser compartilhado.

## 4. Fora de escopo (YAGNI / follow-up)

- **Exportáveis (CSV/PDF de campanha, grade).** Têm bases próprias e documentadas
  (client-target-pmm.md, armadilhas #4/#5). Não migram nesta mudança. Se a paridade
  tela↔export virar demanda, é follow-up separado.
- **Versionamento histórico de `pmm`/`pmm_target`.** Segue sem `valid_from/valid_to`.
- **Reconciliar a "Bonificação"** entre telas — não há equivalente no `/campaigns`.

## 5. Testes

- **Teste de paridade (o guard-chave):** table test em Go que, para um dataset
  semeado, roda a agregação do `/campaigns` e a dos KPIs do `/insights` para o mesmo
  `(campaigns, from, to)` e **falha se divergirem**. Impede a causa-raiz (dois
  caminhos que derivam) de voltar.
- **`insights_test.go`:** atualizar `TargetPMM`, `TargetPMM_ZeroIsNotAbsent`,
  `TargetPMM_MultiClientNoDoubleCount` pra base nova.
- **`campaigns` financials:** teste do seletor de janela (mês atual × acumulado ×
  custom) e do consolidado escopado.
- **Frontend:** o seletor de período e o rótulo (teste do formatador de período —
  puro, sem dependência de frontend, `node --test`, igual a `targetPmmPaste`).

## 6. Segurança e rollout (regra 4.8 do CLAUDE.md)

- **Sem migration de schema** (a função `daily_play_summary_for` já existe) → o
  `shadow_migration_test` do `deploy.sh` não é acionado. **Mas os números mudam**
  (cobrança), então:
- **Validação contra cópia de prod antes de subir:** restaurar dump de prod num
  postgres descartável, rodar o binário novo e comparar **antes×depois** em algumas
  campanhas reais (per_insertion, consolidada, mista). Dereck revisa o diff de
  números. Nada vai pra `master` sem esse OK.
- Eu escrevo tudo; **quem roda em prod é o Dereck** (regra 7).

## 7. Docs a atualizar

- **`docs/features/client-target-pmm.md`** — a seção "Base de contagem: cada tela
  espelha a própria base" (linha ~179) hoje diz que `/campaigns` e `/insights`
  divergem de propósito e "não tente reconciliar". Depois desta mudança elas
  **convergem** para o mesmo período/filtro. Reescrever essa seção.
- **`docs/architecture/detection-count-consistency.md`** — idem: registrar que as
  duas telas passam a usar a base única `in_slot+bonus` via helper compartilhado.
- **`docs/architecture/` (novo ou anexo):** documentar o núcleo financeiro
  compartilhado (`FinancialBase`) como fonte única de plays+investido das telas.
- **`docs/features/insights-dashboard.md`** — nota da nova base + seletor.

## 8. Riscos e pontos de atenção

- **`daily_play_summary_for` é plan-driven.** Confirmar no plano se emissora com
  plano e **zero** plays na janela aparece (in_slot=0, bonus=0) e se emissora com
  plays **sem** plano (orphan puro) aparece — afeta `stations_count`/impactos. Alinhar
  com o que o `/campaigns` já faz hoje pela view.
- **Consolidado com zero plays na janela** ainda deve somar `invested`
  (`value × meses`) — garantir que o helper busca consolidadas de
  `campaign_station_pricing`, não só das linhas da view.
- **Timezone.** Toda janela em `America/Sao_Paulo` (o `/insights` já converte
  `detected_at AT TIME ZONE`); a view usa `for_date`. Confirmar coerência de fuso na
  borda do mês.
- **Performance.** `/campaigns/financials` sem filtro varria a campanha inteira;
  com janela menor tende a ficar mais leve. `daily_play_summary_for` já é o pushdown
  usado em prod.
- **Bonificação (Modelo B) × investido (base A) — modelos diferentes.** Decisão do
  brainstorm foi manter a Bonificação como está. Mas o investido base-A per_insertion
  (`unit × (in_slot + bonus)`) **já embute** as tocadas de bônus, enquanto a
  Bonificação do Modelo B calcula o valor do excedente separadamente. Ou seja, depois
  da mudança os dois cards do `/insights` deixam de ser complementares/somáveis — a
  Bonificação vira uma métrica **informativa isolada**, não uma parcela do investido.
  O `/campaigns` (referência) não tem esse card. **Se isso incomodar**, as opções são:
  (a) remover/zerar a Bonificação no `/insights` (fiel ao `/campaigns`), ou
  (b) redefinir Bonificação = `Σ (bonus) × unit_value` (o valor do bônus dentro da
  base A). Fica registrado como decisão a confirmar; default atual = manter como está.
