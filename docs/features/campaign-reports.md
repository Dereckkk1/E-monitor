---
status: implementado
ultima-verificacao: 2026-08-17
codigo-relacionado:
  - workers/internal/reportcsv/reportcsv.go
  - workers/internal/reportcsv/format.go
  - workers/internal/reportcsv/footer.go
  - workers/internal/catalog/detections.go
  - workers/internal/catalog/campaigns.go
  - workers/internal/categorizer/categorizer.go
  - workers/internal/api/handlers/detections.go
  - workers/internal/api/handlers/reports.go
  - workers/internal/api/router.go
  - workers/cmd/api/main.go
  - frontend/src/api/hooks.js
  - frontend/src/components/CampaignReportsMenu.jsx
  - frontend/src/utils/pdfReport.js
  - frontend/src/utils/gridReport.js
  - frontend/src/pages/CampaignsPage.jsx
  - frontend/src/pages/DetectionsPage.jsx
  - frontend/src/components/AirtimeFiltersBar.jsx
---

> **Modo WYSIWYG em /detections:** desde 2026-07, `/detections` gera CSV/PDF
> localmente espelhando a grade filtrada (não o backend desta doc). Ver
> [detections-report-wysiwyg.md](detections-report-wysiwyg.md).

# Relatórios de Campanha (CSV consolidado, CSV detalhado, PDF)

## O que é

Componente unificado de exportação de relatórios de uma campanha, disponível
nas três telas de "veiculação":

- **/campaigns** — botão **Relatórios** em cada card de campanha (sem range
  de datas — usa a campanha inteira).
- **/detections** — botão **Relatórios** na toolbar secundária (filterStep 3).
  > **Atenção:** em `/detections` (para admin, com catálogo de emissoras
  > carregado) o menu opera em **modo WYSIWYG** — CSV Consolidado e PDF são
  > gerados **localmente** espelhando a grade filtrada (busca + programado +
  > por dia), via a prop `gridReport`. NÃO usa os endpoints backend descritos
  > abaixo nesse caso. Detalhes:
  > [detections-report-wysiwyg.md](detections-report-wysiwyg.md). O fluxo
  > backend desta doc continua valendo para `/campaigns`, `/reports/airtime` e
  > para o fallback de viewer em `/detections`.
- **/reports/airtime** — botão **Relatórios** substituiu o antigo
  "Exportar CSV" admin-only; agora viewer também consegue baixar
  consolidado/PDF dentro do escopo do próprio cliente.

O menu oferece três opções:

| Item | Forma | Granularidade | Acesso |
|------|-------|---------------|--------|
| CSV Consolidado | `text/csv; charset=utf-8` (BOM, separador `;`) | 1 linha por **material × emissora** com total + breakdown por status (Dentro da faixa/Fora da faixa/Fora da data/Bonificação) no período | viewer (próprio cliente) + operator + admin |
| CSV Detalhado | mesmo formato | 1 linha por **veiculação**, no **layout do relatório do fornecedor** + rodapé de totais (ver seção abaixo), coluna **Status** em PT-BR | **admin-only** (reusa `/detections/export`) |
| PDF | A4, gerado no browser via jsPDF | capa + KPIs + **legenda de cores** + tabela por material + tabela por emissora + tabela material × emissora — as três com **breakdown por status** (Dentro · Fora faixa · Fora data · Bônus, coloridos como o semáforo da grade) | viewer (próprio cliente) + operator + admin |

> O CSV detalhado continua admin-only por decisão histórica (o endpoint
> `/detections/export` já era restrito; mantemos pra evitar mudança de
> superfície de auditoria). Viewer ainda baixa consolidado e PDF —
> que contêm os mesmos números, só sem o timeline detalhado.

## Por que existe

Substituiu uma necessidade recorrente de exportar dados pra fechamento
comercial e prestação de contas pra clientes. O fornecedor anterior já
oferecia algo parecido; replicar isso é parte da entrega de paridade
(§17 do plano). A versão Radiocheck é mais simples (3 formatos, escopo
sempre por campanha) e leva a marca E-monitor no PDF.

## Coluna "Impactos": `PMM × (Dentro da faixa + Bonificação)`

Todo relatório desta feature — CSV Consolidado, PDF, e as versões WYSIWYG de
grade — usa a **base canônica de impactos** do produto:

```
Impactos           = PMM        × (in_slot + bonus)
Impactos no target = pmm_target × (in_slot + bonus)
```

No backend isso é `MaterialStationRow.ImpactCount` / `StationAggregateRow.ImpactCount`
(computados no SQL de `AggregateByMaterialStation`/`AggregateByStation`); no
frontend é `impactBase()` em [`pdfReport.js`](../../frontend/src/utils/pdfReport.js)
e [`gridReport.js`](../../frontend/src/utils/gridReport.js). É o mesmo número que
`/insights`, `/campaigns` e o pós-venda mostram. Ver
[client-target-pmm.md](client-target-pmm.md).

> ⚠️ **A coluna "Total" NÃO é o multiplicador.** `Total` soma as quatro categorias;
> `Impactos ÷ PMM` = `Dentro da faixa + Bonificação`. Fora-da-faixa não vale nada
> comercialmente (decisão D3 do [fechamento por cota](quota-aware-categorization.md))
> e fora-da-data está fora do período contratado — nenhum dos dois é impacto
> entregue ao cliente.
>
> **Mudou em 2026-08-17.** Antes: o CSV Consolidado e o PDF de campanha
> multiplicavam por `Total` (todas as categorias, inflado), enquanto o CSV/PDF de
> grade multiplicava só por `Dentro da faixa` (deflacionado — escondia a
> bonificação). Um relatório novo não bate com um antigo da mesma campanha: o de
> campanha cai um pouco, o de grade sobe. É o antigo que estava errado.
>
> `impactBase()` no `pdfReport.js` tem fallback pro breakdown da própria linha
> (`Dentro + Bônus`) quando `impact_count` não vem no JSON. Isso é de propósito: o
> frontend sobe no Cloudflare Pages **antes** do backend ir pra VM, e sem o
> fallback a coluna Impactos zeraria no PDF do cliente durante a janela entre os
> dois deploys.

> A coluna **Impactos** é do CSV Consolidado e do PDF. O **CSV Detalhado** é uma
> linha por veiculação e não tem coluna de impacto — o equivalente dele é a
> coluna `PMM`, que o leitor multiplica pelas linhas que quiser.

## CSV Detalhado — layout do fornecedor

Desde **2026-08-14** o CSV Detalhado sai no formato do relatório do fornecedor
externo que o E-monitor substitui (arquivo de referência:
`183.1-Rogga-_-Midia-Geral-01-05-2026-31-05-2026.xlsx`). O objetivo é que o
cliente abra o nosso relatório e reconheça o formato, sem reaprender a ler o
arquivo.

Continua sendo **CSV** (`;` + BOM UTF-8) — só o conjunto e a ordem das colunas
mudaram. A formatação inteira vive em
[`reportcsv.WriteDetailed`](../../workers/internal/reportcsv/reportcsv.go).

### Colunas

| # | Coluna | Origem |
|---|--------|--------|
| 1 | `Identificador` | `stations.short_id` |
| 2 | `Data` | `detected_at` → `DD/MM/AAAA`, America/Sao_Paulo |
| 3 | `Hora` | `detected_at` → `HH:MM:SS` |
| 4 | `Rádio` | nome + banda + frequência → `Massa - FM (106.9)` |
| 5 | `Cidade / UF` | `Joinville / SC` |
| 6 | `Peça` | `material_types.name` (`Spot 30s`, `Jingle`, `Testemunhal`) |
| 7 | `Comercial` | título do material |
| 8 | `Status` | `category` em PT-BR (tabela abaixo) |
| 9 | `PMM` | `stations.pmm` |
| 10 | `Preço` | `campaign_station_type_pricing.unit_value` |
| 11 | `Cliente` | `clients.name` |
| 12 | `PMM no target` | `client_station_pmm.pmm_target` |
| 13 | `Duração (s)` | `materials.duration_seconds` |

**As colunas 1–10 são o layout do fornecedor, nesta ordem exata.** As 11–13 são
nossas e vêm depois, pra não perder informação que o layout dele não cobre.
`Cliente` é a que mais importa: `/detections/export` aceita `campaign_id`
opcional, e sem ela um export cross-campanha viraria uma lista indistinguível.

Ordenação: `detected_at DESC` (mais recente primeiro), igual ao fornecedor.

**Diferenças deliberadas do arquivo original:**
- **Sem a linha em branco** entre o cabeçalho e a primeira veiculação — ela
  quebra importadores (Power Query, scripts) e não agrega nada visualmente.
- **Frequência com ponto decimal** (`106.9`), ao contrário do resto do CSV que
  usa vírgula: aqui é rótulo de dial, não número que o Excel vá somar.
- **`R$ 6,00` com espaço comum**, não o NBSP do original — visualmente idêntico
  e sem o risco de um byte invisível confundir quem processa o arquivo.
- **Os 4 rótulos de status**, não só "Dentro da Faixa" (o fornecedor não tem o
  conceito de fora-da-faixa/bonificação — no arquivo de referência as 1.671
  linhas são *todas* "Dentro da Faixa"). Mantemos a nossa grafia
  ("Dentro da faixa", f minúsculo) porque `CategoryLabelPT` é compartilhada com
  o CSV Consolidado e o `DayDetailModal`.
- **Ordem determinística no resumo por comercial**: em empate de total,
  desempatamos por título asc. O arquivo do fornecedor não desempata (três
  comerciais com 36 saem fora de ordem alfabética), o que tornaria o golden
  test flaky.

### Regra da coluna `Preço`

Preenchida **somente** quando a linha é `in_slot` **e** existe `unit_value`
cadastrado pra (campanha, emissora, tipo). Qualquer outro caso sai `R$ 0,00`.

A restrição a `in_slot` segue a regra de cobrança da
[`0022_pricing.up.sql`](../../migrations/0022_pricing.up.sql) — "valor total =
`unit_value × in_slot`". Com ela, **a soma da coluna bate com o que é
faturado**; preencher fora-da-faixa/fora-da-data/bonificação inflaria o número.

> ⚠️ **Interação com o fechamento por cota (2026-08-17).** `in_slot` passou a ser
> **limitado pela meta N da célula-dia**: as N primeiras tocadas dentro da faixa
> são `in_slot`, o excedente vira `bonus`. A coluna `Preço` acompanha isso
> automaticamente — o excedente sai `R$ 0,00`, que é o comportamento correto
> (bonificação não fatura). No mesmo passo `out_slot` deixou de fechar a
> obrigação, e continua `R$ 0,00` como sempre foi. Ver
> [quota-aware-categorization.md](quota-aware-categorization.md).

Campanha com pricing em modo `consolidated` não tem valor por inserção **por
definição** → todas as linhas saem `R$ 0,00`, que é exatamente o que o arquivo
do fornecedor mostra na maioria das emissoras.

### Rodapé de totais

Depois de **duas** linhas em branco, três blocos:

```
TOTAL DE RADIOS MONITORADAS;16
TOTAL DE RÁDIOS POR ESTADO COM VEICULAÇÕES;16
TOTAL DE VEICULAÇÕES;1671

RESUMO DE RÁDIOS POR ESTADO COM VEICULAÇÕES
UF;TOTAL
SC;16

RESUMO DE VEICULAÇÕES POR COMERCIAL
Comercial;Total     ← desc; empate desempata por título asc
```

**"Rádios monitoradas" = emissoras com pelo menos uma veiculação no período**,
não emissoras no `target_stations` da campanha. Emissora que ficou fora do ar o
mês inteiro não aparece aqui — pra isso existe `/admin/station-failures`.

O rodapé é acumulado **durante** o stream, em
[`footer.go`](../../workers/internal/reportcsv/footer.go): guarda chaves
distintas (emissoras, UFs, títulos), não linhas. Memória O(emissoras +
materiais) — dezenas de entradas — e não O(veiculações), então o export
continua streamando arquivo de qualquer tamanho.

Emissora sem `state` cadastrado entra num grupo de chave vazia, por último no
resumo por estado. Não é caso esperado, mas omiti-la faria o total geral
divergir da soma do bloco por UF.

### Nome do arquivo

`{Cliente}-Veiculacoes-{DD-MM-AAAA}-{DD-MM-AAAA}.csv` — ex.
`Rogga-Veiculacoes-01-05-2026-31-05-2026.csv`. Mesmo espírito do fornecedor,
sem os códigos internos dele (`183.1`, `Midia Geral`).

Fallbacks:
- sem `campaign_id`, ou falha ao resolver o cliente → `veiculacoes_{timestamp}.csv`
- sem `from`/`to` → `{Cliente}-Veiculacoes-{timestamp}.csv`

O nome do cliente passa por `reportcsv.SanitizeFilename`: acentos removidos,
caractere fora de `[A-Za-z0-9._-]` vira `-`, hifens repetidos colapsam,
truncado em 60. `Content-Disposition` com byte não-ASCII quebra em parte dos
navegadores, e sanitizar é mais simples que `filename*=UTF-8''`.

### Efeito no zip do pós-venda

O `relatorio-detalhado.csv` dentro do bundle do pós-venda usa **o mesmo**
`WriteDetailed`, então também mudou de formato — o cliente recebe o mesmo
layout pelos dois caminhos. O nome da entrada dentro do zip continua
`relatorio-detalhado.csv` (é caminho fixo do bundle, não download avulso).

**Pós-vendas já publicados não são regerados**: quem baixar um zip antigo pega
o formato antigo. É o comportamento correto — o documento do pós-venda é
congelado por design (ver [post-sale.md](post-sale.md)).

## Rótulos de status nos CSVs

A coluna **Status** (CSV Detalhado) e as colunas de breakdown (CSV
Consolidado) usam o mesmo vocabulário PT-BR do
[`DayDetailModal.jsx`](../../frontend/src/components/DayDetailModal.jsx),
não o enum técnico do banco. Mapeamento:

| `category` (banco) | Status (CSV) |
|--------------------|--------------|
| `in_slot`          | Dentro da faixa |
| `out_slot`         | Fora da faixa |
| `out_date`         | Fora da data |
| `bonus`            | Bonificação |
| `orphan` (legado)  | Bonificação — mesmo rótulo, **não** cai no fallback |

A conversão canônica vive em `reportcsv.CategoryLabelPT`
([reportcsv.go](../../workers/internal/reportcsv/reportcsv.go)); o
`categoryLabelPT` de [detections.go](../../workers/internal/api/handlers/detections.go)
é só um delegate pros outros handlers do arquivo. Se aparecer um valor de
categoria novo (improvável; a coluna é enum restrito pelo CHECK e por
`categorizer.go`), o fallback escreve o valor cru pra não silenciar.

> `orphan` é o nome antigo de `bonus` ([quota-aware-categorization.md](quota-aware-categorization.md)).
> Ele é mapeado explicitamente — via `categorizer.CatBonus, categorizer.CatOrphan`
> no mesmo `case` — porque **este arquivo é o CSV que o cliente abre**: uma linha
> gravada pelo binário antigo na janela de deploy imprimiria a string crua
> "orphan" numa célula do relatório. Isso vale para os dois CSVs: a coluna
> `Status` do detalhado e o cabeçalho `Bonificação` do consolidado. O campo JSON
> `orphan_count` do agregado também manteve o nome por compatibilidade com o
> frontend — o conteúdo é a contagem de bonificação.

## Filtro de período (dentro do menu)

O dropdown abre com um seletor de período opcional no topo:

- **Default** quando não há range vindo da página (cenário `/campaigns`):
  **mês corrente inteiro** (1º ao último dia do mês atual).
- **Default** em `/detections` e `/reports/airtime`: herda o `from`/`to`
  que o usuário já escolheu nos filtros da página.
- Dois chips rápidos: **"Mês atual"** restaura o default e **"Toda"**
  zera o filtro (backend passa a usar a campanha inteira).
- Validação: `from > to` desabilita as 3 ações e exibe inline a mensagem
  "Intervalo inválido".

O range editado dentro do menu **não** propaga de volta pros filtros da
página — é só o recorte do relatório.

## Como funciona

### Backend (Go)

- **Catálogo** ([detections.go](../../workers/internal/catalog/detections.go)):
  duas novas agregações sem paginação, usando o mesmo `WHERE` da
  `AggregateByMaterial` (filtra `ignored_at`, `retracted_at`,
  `evidence_status != 'audit_rejected'`), pra que o relatório bata
  com a UI:
  - `AggregateByMaterialStation(ctx, filter) → []MaterialStationRow`
  - `AggregateByStation(ctx, filter) → []StationAggregateRow`

- **Handler** ([reports.go](../../workers/internal/api/handlers/reports.go)):
  - `GET /v1/internal/reports/campaigns/{id}/consolidated.csv` — stream
    CSV com BOM UTF-8 e separador `;` (igual ao detalhado, abre limpo
    no Excel pt-BR). Filename `relatorio-consolidado-{slug}-{stamp}.csv`.
  - `GET /v1/internal/reports/campaigns/{id}/summary` — JSON com
    `campaign`, `client` (nome + CNPJ), `period`, `totals` (3 KPIs),
    `by_material`, `by_station`, `by_material_station` e `generated_at`.

- **Acesso**: ambos os endpoints vivem em **Subgrupo A** do router
  (viewer-friendly). O handler verifica `ClientScopeFromContext` e
  responde 404 (não 403) quando o viewer tenta acessar campanha de
  outro cliente — não vaza existência.

### Frontend (React + jsPDF)

- **`CampaignReportsMenu`**
  ([component](../../frontend/src/components/CampaignReportsMenu.jsx)):
  botão "Relatórios" com dropdown renderizado por portal (escapa
  overflow de cards/listas). Estados de loading independentes por
  ação. Suporta `variant` (`button | icon | compact`) e `placement`
  (`bottom-end | bottom-start`).

- **`pdfReport.js`**
  ([builder](../../frontend/src/utils/pdfReport.js)): monta o PDF no
  cliente:
  1. **Header**: logo E-monitor (do `/public/E-monitor logo.png`,
     pré-carregado no mount do menu).
  2. **Hero**: barra rosa-action (#E81E75), nome da campanha em
     Helvetica bold 20pt navy (#06055B), meta `cliente · período`,
     badge de status à direita.
  3. **KPIs**: 3 cards (Veiculações, Materiais, Emissoras) — número
     grande rosa, label cinza. Abaixo, uma **legenda de cores**
     (Tocou · Fora da faixa · Fora da data · Déficit · Bônus) espelhando
     o semáforo de [`DayDetailModal.jsx`](../../frontend/src/components/DayDetailModal.jsx).
  4. **Tabelas** via `jspdf-autotable` (linha zebra clara,
     cabeçalho cinza, colunas de total em bold rosa). As três tabelas
     (por material, por emissora, material × emissora) trazem colunas
     **Dentro · Fora faixa · Fora data · Bônus** com números coloridos.
     O breakdown vem do `by_material_station` (que já carrega
     `in_slot_count`/`out_slot_count`/`out_date_count`/`orphan_count`),
     somado no cliente por material e por emissora — **sem mudança de
     backend**. A tabela de detalhe trocou `Primeira/Última` por esse
     status (as datas seguem no CSV consolidado).
  5. **Footer**: `Gerado por E-monitor · DD/MM/YYYY` à esquerda,
     paginação à direita.

  Decisão: gerar PDF no browser, não no backend. Razão:
  - O styling segue o design system que já vive no front (cores,
    tokens). Replicar no Go exigiria duplicação ou serviço de
    rendering (Chromium, wkhtmltopdf) — overhead alto.
  - Bundle adicionou ~280kb gzip (jsPDF + autotable), aceitável.

- **Helpers** ([hooks.js](../../frontend/src/api/hooks.js)):
  - `exportConsolidatedCsv({ campaignId, from, to })` — imperativo,
    dispara download via blob + `<a download>`. Reusa filename do
    `Content-Disposition`.
  - `fetchCampaignReportSummary({ campaignId, from, to })` — devolve a
    payload crua pro builder do PDF.
  - `exportDetectionsCsv` (já existia) — usado pelo "CSV Detalhado".

## Como tirar do ar / debug

- Comportamento esperado: o botão fica desabilitado em `/reports/airtime`
  enquanto não tem campanha selecionada (mostra "Selecione uma campanha"
  no tooltip).
- Se a logo não carregar (404, CORS), o PDF degrada pra texto "E-monitor"
  rosa no topo. Não é erro fatal — só uma marca menos pomposa.
- Erros de backend (404, 500) caem em `window.alert` com mensagem PT-BR.

## O que NÃO faz

- Não permite agrupamentos customizados (por dia da semana, faixa de
  horário etc.). Os 3 formatos são fixos.
- Não envia o relatório por email — só download local. Webhook continua
  sendo a forma de entregar veiculações em tempo real.
- Não há cache de PDFs no servidor. Cada clique no PDF re-busca o
  summary e re-renderiza. O JSON é leve (<200 KB no pior caso).

## Próximos passos possíveis (não compromissados)

- Embed da Fira Sans Condensed no PDF pra alinhar 100% com o design
  system (atualmente helvetica built-in).
- Code-splitting de `jspdf` (dynamic import) — reduzir o bundle
  inicial. Hoje o PDF builder é importado eagerly via
  `CampaignReportsMenu`.
- Quebra paginada da tabela "Material × Emissora" com cabeçalho
  repetido em cada página (autotable já faz isso por padrão; só
  conferir visual em campanhas com 500+ linhas).
