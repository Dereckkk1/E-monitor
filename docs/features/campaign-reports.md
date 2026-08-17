---
status: implementado
ultima-verificacao: 2026-08-17
codigo-relacionado:
  - workers/internal/catalog/detections.go
  - workers/internal/reportcsv/reportcsv.go
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
| CSV Detalhado | mesmo formato | 1 linha por **veiculação**, coluna **Status** em PT-BR | **admin-only** (reusa `/detections/export`) |
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
`categoryLabelPT` dos handlers apenas delega. Se aparecer um valor de
categoria novo (improvável; a coluna é enum restrito pelo CHECK), o
fallback escreve o valor cru pra não silenciar.

> `orphan` é o nome antigo de `bonus` ([quota-aware-categorization.md](quota-aware-categorization.md)).
> Ele é mapeado explicitamente porque **este arquivo é o CSV que o cliente abre**:
> uma linha gravada pelo binário antigo na janela de deploy imprimiria a string
> crua "orphan" numa célula do relatório. O campo JSON `orphan_count` do agregado
> também manteve o nome por compatibilidade com o frontend — o conteúdo é a
> contagem de bonificação.

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
