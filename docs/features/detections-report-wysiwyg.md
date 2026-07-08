---
status: implementado
ultima-verificacao: 2026-07-08
codigo-relacionado:
  - frontend/src/utils/gridReport.js
  - frontend/src/utils/pdfReport.js
  - frontend/src/utils/dates.js
  - frontend/src/components/CampaignReportsMenu.jsx
  - frontend/src/components/DistributionGrid.jsx
  - frontend/src/pages/DetectionsPage.jsx
---

# Relatório WYSIWYG de /detections (espelha a grade)

Em `/detections`, o botão **Relatórios** (na toolbar secundária) passou a gerar
CSV e PDF **espelhando exatamente o que está na tela** — as mesmas emissoras/
materiais filtrados pela busca, o mesmo período, os mesmos números da grade,
com o **programado** (esperado) e um detalhamento **dia a dia** no PDF.

> É um **modo específico da `/detections`**. Em `/campaigns` e `/reports/airtime`
> o [`CampaignReportsMenu`](../../frontend/src/components/CampaignReportsMenu.jsx)
> continua idêntico (relatório backend genérico + editor de período próprio).
> Ver [campaign-reports.md](campaign-reports.md) para o modo genérico.

## Problema que resolve

Antes, o "Relatórios" da `/detections` chamava o backend genérico
(`/reports/campaigns/{id}/...`), que:

1. **ignorava o filtro de busca** — pesquisar "nativa" filtrava a grade, mas o
   relatório vinha com **todas** as emissoras da campanha;
2. **não trazia o programado** — só contava veiculações reais, nunca o plano;
3. **não tinha quebra por dia** — tudo era total do período.

Agora o relatório "conversa com o filtro": pesquisou → exporta só aquilo.

## Princípio: uma fonte, zero divergência

O relatório é derivado do **mesmo dado que a grade renderiza** — não há segunda
query nem segunda regra de categorização:

- **Emissoras/materiais** = `filteredRows` (todas as linhas que batem com a
  busca, de **todas as páginas** da grade paginada, não só a visível).
- **Dias** = `enumerateVisibleDays(...)` de [`dates.js`](../../frontend/src/utils/dates.js),
  o **mesmo helper** que o `DistributionGrid` usa (extraído dele nesta entrega).
  Isso inclui o **cap-at-today**: dias futuros não entram (senão virariam
  déficit fantasma), igual à tela.
- **Números por célula** = o `cellData` (`daily_play_summary`): `expected`,
  `in_slot`, `deficit`, `bonus`, `out_slot`, `out_date`. As mesmas 6 métricas
  das pílulas do `RowSummaryCell`.

Como grade e relatório enumeram dias pelo mesmo helper e somam o mesmo
`cellData`, os totais batem por construção (per-dia = total por material =
total por emissora = KPIs).

## Os três formatos

| Item | Conteúdo |
|------|----------|
| **CSV Consolidado** | 1 linha por **emissora × material**, colunas: Emissora, Dial, Cidade, UF, Material, **Programado**, Tocou (faixa), Déficit, Bônus, Fora da faixa, Fora da data. BOM UTF-8 + separador `;` (Excel pt-BR). |
| **PDF** | Capa (campanha + cliente + período + **nota do recorte**) → KPIs filtrados (Cobertura %, Esperado, Tocou, Déficit, Bônus) → **uma seção por emissora** com tabela **dia a dia** (`Data · Material · Prog · Tocou · Déf · Bônus`) e total por material. Fora-faixa/fora-data aparecem como nota por emissora. |
| **CSV Detalhado** (admin) | **Inalterado** — segue o backend `/detections/export` (1 linha por veiculação, escopo campanha + data). É o dump cru do timeline e **não aplica o filtro de busca** (o hint no menu avisa). |

"Tocou" = `in_slot` (dentro da faixa, verde) — o mesmo vocabulário das cores da
grade. Déficit em vermelho, Bônus em azul, espelhando o semáforo da tela.

## Arquitetura (frontend-only, sem backend)

Tudo roda no cliente a partir do dado já buscado pela página — **nenhuma
mudança de backend / schema / migration**.

- **[`utils/gridReport.js`](../../frontend/src/utils/gridReport.js)** —
  `buildGridReportModel(...)` (função pura → modelo estruturado),
  `buildGridReportCSV(model)` (modelo → string CSV) e `exportGridReportCsv(model)`
  (download via Blob).
- **[`utils/pdfReport.js`](../../frontend/src/utils/pdfReport.js)** —
  `buildGridReportPDF(model)` (jsPDF), reusando hero/tokens/footer do builder
  genérico existente.
- **[`utils/dates.js`](../../frontend/src/utils/dates.js)** —
  `enumerateVisibleDays(...)` e `startOfLocalToday()`, compartilhados com o
  `DistributionGrid`.
- **[`CampaignReportsMenu`](../../frontend/src/components/CampaignReportsMenu.jsx)** —
  prop opcional `gridReport={{ model, filterNote }}`. Presente → esconde o
  editor de período, mostra a **nota do recorte**, e liga Consolidado/PDF nos
  geradores locais. Ausente → comportamento backend atual (outras telas).
- **[`DetectionsPage`](../../frontend/src/pages/DetectionsPage.jsx)** — memoiza
  `reportDays` + `reportModel` e passa `gridReport` ao menu.

## Fallback do viewer (sem regressão)

O modelo precisa do catálogo de emissoras (`useStations`, hoje admin-only) pra
resolver nome/dial. A `DetectionsPage` só passa `gridReport` quando
`stationCatalog.length > 0`. Sem catálogo (ex.: viewer), o menu **cai no
relatório backend atual** — o viewer continua baixando consolidado/PDF como
antes, sem quebra. O gate é robusto independentemente de como (ou se) o viewer
recebe o catálogo.

## Regras de exibição respeitadas

- **Cap-at-today**: o relatório corta em hoje, igual à grade. Filtrar um mês
  passado inteiro não corta nada (todos os dias ≤ hoje).
- **Dias vazios somem**: no PDF, dia sem plano e sem tocada não vira linha (é
  ruído; não afeta totais). Dia com `expected>0` e 0 tocadas **aparece**
  (déficit importa).
- **Material com tudo-zero é omitido** no PDF (grade mostraria a linha vazia; o
  relatório prioriza legibilidade). No CSV, toda linha filtrada entra.
- **Campanha cancelada**: `daily_play_summary` já congela em `cancelled_at`
  (política "manter e marcar"); e o seletor da página nem lista cancelada.
  Nada de novo a tratar aqui — ver
  [cancelled-campaign-handling.md](cancelled-campaign-handling.md).

## Fora de escopo

- Não mexe em backend, schema nem na view `daily_play_summary`.
- Não filtra o **CSV Detalhado** por busca (é backend; ficaria como follow-up
  com um param `station_id` no `/detections/export`).
- Não altera `/campaigns` nem `/reports/airtime`.

## Como validar

- Lógica pura (`buildGridReportModel`/`buildGridReportCSV`/`enumerateVisibleDays`)
  tem cobertura por teste Node standalone (o frontend não tem test runner — ver
  regra 5 do CLAUDE.md, não dá pra `npm install` vitest no Windows).
- Build (`npm run build`, mesma toolchain do CF Pages) e render do PDF
  conferidos na entrega (2026-07-08).
