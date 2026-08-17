---
status: legado  # superseded 2026-08-17 pela categorizacao por cota
ultima-verificacao: 2026-08-17
codigo-relacionado:
  - frontend/src/pages/InsightsPage.jsx (a criar)
  - frontend/src/components/Sidebar.jsx
  - frontend/src/App.jsx
  - workers/internal/api/handlers/insights.go (a criar)
  - workers/internal/catalog/insights.go (a criar)
  - workers/internal/catalog/stations.go
  - workers/internal/api/handlers/campaigns.go
  - workers/internal/api/handlers/detections.go
  - frontend/src/utils/pdfReport.js
---

> ⚠️ **REGISTRO HISTÓRICO — não descreve o comportamento atual.**
> Este documento é um snapshot datado da sessão de design/implementação que o gerou.
> Em **2026-08-17** a categorização de veiculação foi substituída pelo
> [**fechamento por cota da célula-dia**](../../features/quota-aware-categorization.md):
> `orphan` foi renomeada pra `bonus`; `out_slot` deixou de faturar e de abater o déficit;
> `deficit = expected − in_slot`; `bonus = COUNT(category = 'bonus')` (acabou o termo
> sintético `GREATEST(0, in_slot − expected)`); e **`Impactos = pmm × (in_slot + bonus)`**
> em toda tela e exportável. As fórmulas de Investido (`in_slot + out_slot`), déficit e Impactos (`PMM × todas as detecções`) abaixo são as do modelo ANTIGO.
> **Não copie fórmula daqui pra código novo** — a autoridade é
> [`docs/features/quota-aware-categorization.md`](../../features/quota-aware-categorization.md).

# Dashboard de Veiculação (Insights)

> Tela única consolidando KPIs e gráficos demográficos por cliente × campanha(s) × período × emissoras. Substitui dashboards manuais em PDF que o time comercial monta hoje.

## 1. Contexto e Motivação

Hoje, ao final de uma campanha (ou em check-points), o time comercial monta manualmente um PDF para o cliente com impactos demográficos, CPM, bonificação e percentual de execução. O dado existe — está espalhado por `/campaigns` (financials, CPM), `/detections` (categorização) e `StationEditPage` (perfil demográfico) — mas não há uma visão consolidada.

Esta tela centraliza tudo num dashboard interativo que ambos lados podem consultar:
- **Admin:** seleciona cliente → campanhas → período → emissoras (opcional). Vê tudo de todos os clientes.
- **Cliente:** vê só as próprias campanhas, com filtragem de período e emissoras.

A possibilidade de exportar como imagem (PNG) ou PDF preserva o caso de uso comercial atual (anexar em e-mail/apresentação) sem o trabalho manual.

## 2. Escopo

### Inclui
- Rota `/insights` (admin + cliente)
- Entrada no sidebar sob o grupo "Veiculação" (label "Dashboard")
- Filtros: cliente, campanhas (multi), período (com default mês corrente + atalho "período completo"), emissoras (multi opcional)
- 5 cards de KPI: Impactos, CPM, Bonificação, Investido (com toggle Contratado/Executado), Gênero (M/F)
- 3 gráficos demográficos em linha: pirâmide de classe social, faixa etária, percentual de veiculações (pizza)
- 1 gráfico full-width: resumo diário/mensal (bucketização automática)
- Exportação para PNG (html2canvas) e PDF (jsPDF, reusando `pdfReport.js`)
- Empty states com "Tutorial Estilizado" (§4.7 do design.md)
- Backend: 1 endpoint `GET /api/v1/insights` que devolve payload já agregado

### Não inclui (fora de escopo nesta entrega)
- Comparação entre períodos (ex: "este mês vs. anterior")
- Métricas por share-of-voice / share-of-spend
- Drill-down para detecções individuais (já existe em `/detections`)
- Agendamento de envio recorrente do PDF por e-mail
- Cache de payload do dashboard (recalcula a cada request — otimização vem depois se virar gargalo)
- Cliente selecionar segundo cliente (admin only)

## 3. Decisões Chave (validadas com o usuário)

| # | Decisão | Implicação |
|---|---|---|
| 1 | **Impactos = PMM × todas as detecções** (in_slot + out_slot + out_date + orphan) | Maximiza a métrica de alcance; inclui execuções fora do horário programado e cortesias |
| 2 | **Bonificação = soma do valor das veiculações extras/orphan** | `count(detections.category = 'orphan') × valor_unitario_da_inserção_na_campanha`. Reflete mídia ganha em dinheiro |
| 3 | **Investido tem dois modos no card** (toggle) | Contratado = valor do contrato; Executado = valor executado de fato. Toggle inline no card 4 |
| 4 | **Stack de gráficos: Recharts** | Lib nova no projeto; +~90KB gz. Justificativa: React-first declarativo, integrável com tokens do design system, será reusada |
| 5 | **Filtro de período "inclusivo"** | Se filtro Fev–Dez e 3 campanhas têm períodos diferentes mas todas com overlap não-vazio, todas entram (cada uma contribui só com seu overlap) |
| 6 | **Bucketização do gráfico 4 automática** | ≤ 31 dias filtrados → barras diárias; > 31 dias → mensais |
| 7 | **Layout em 3 linhas** | Row 1: 5 cards · Row 2: 3 gráficos · Row 3: gráfico daily/monthly full width |

## 4. Arquitetura

### 4.1 Frontend

```
frontend/src/pages/InsightsPage.jsx          (página)
frontend/src/pages/InsightsPage.module.css   (estilos)
frontend/src/components/insights/
  ├── FiltersBar.jsx        (cliente · campanhas · período · emissoras · export)
  ├── KpiCards.jsx          (5 cards)
  ├── InvestmentToggleCard.jsx  (card 4 com toggle)
  ├── ClassPyramidChart.jsx (gráfico 1)
  ├── AgeRangeChart.jsx     (gráfico 2)
  ├── BroadcastShareChart.jsx (gráfico 3 — pizza %)
  ├── DailySummaryChart.jsx (gráfico 4 — full width)
  ├── CustomTooltip.jsx     (tooltip glassmorphism reusável)
  └── EmptyTutorial.jsx     (shadow UI quando filtros incompletos)
frontend/src/hooks/useInsights.js  (React Query wrapper)
frontend/src/utils/exportInsights.js  (PNG via html2canvas + PDF via jsPDF)
```

- **Data fetching:** React Query 5 (`useInsights({ clientId, campaignIds, from, to, stationIds })`)
- **Estado dos filtros:** URL search params (campanhas em querystring → permite compartilhar link/voltar do browser preserva estado)
- **Charts:** Recharts. Cores derivadas das tokens CSS via `getComputedStyle` ou hard-coded constants compatíveis com `--color-tertiary-500`, `--color-success-500`, etc.

### 4.2 Backend

```
workers/internal/api/handlers/insights.go       (handler HTTP)
workers/internal/catalog/insights.go            (repo SQL)
workers/internal/catalog/insights_test.go       (testes com seed)
```

**Endpoint:** `GET /api/v1/insights`

**Query params:**
- `client_id` (uuid, obrigatório — admin pode passar qualquer; cliente é forçado pelo JWT)
- `campaigns` (csv de uuids, obrigatório, mín 1)
- `from`, `to` (ISO date, opcionais — default = primeiro/último dia do mês corrente)
- `stations` (csv de uuids, opcional — vazio = todas as estações das campanhas)

**Role-gating:** segue o pattern de `auth.ClientScopeFromContext()` (anti-oracle): se `scope != nil`, ignora `client_id` da query e força `scope.ClientID`. Verifica que toda campanha em `campaigns` pertence ao cliente — qualquer campanha de outro cliente → 403.

**Response shape:**
```json
{
  "period": { "from": "2026-02-01", "to": "2026-12-31", "granularity": "month" },
  "campaigns": [
    { "id": "uuid", "name": "...", "start_date": "...", "end_date": "..." }
  ],
  "kpis": {
    "impactos": 12500000,
    "veiculacoes_total": 9500,
    "stations_count": 47,
    "cpm": 14.23,
    "bonificacao": { "valor": 4500.0, "count": 87 },
    "investido": { "contratado": 178000.0, "executado": 165000.0 },
    "gender": { "m": 7500000, "f": 5000000 }
  },
  "class_pyramid": { "ab": 4200000, "c": 6300000, "de": 2000000 },
  "age_ranges": { "18_24": 2100000, "25_49": 7300000, "50_plus": 3100000 },
  "veiculacoes_breakdown": {
    "in_slot": 8500,
    "out_slot": 600,
    "out_date": 120,
    "extras_orphan": 280
  },
  "buckets": [
    { "bucket": "2026-02", "programado": 320, "in_slot": 280, "out_slot": 12,
      "out_date": 4, "deficit": 28, "extras": 9 }
  ]
}
```

### 4.3 Modelo de dados — leitura

Tabelas usadas (todas já existem):
- `campaigns`: `id, client_id, start_date, end_date, name`
- `campaigns_pricing`: `campaign_id, mode (consolidated | per_insertion), consolidated_value, price_per_insertion`
- `detections`: `id, campaign_id, station_id, material_id, detected_at, category` (in_slot | out_slot | out_date | orphan)
- `distribution_rules`: programado por dia/janela por campaign × station × material (para cálculo de "programado" e "déficit" no gráfico 4)
- `stations.meta` (JSONB): `pmm`, `audience.gender.{male_pct, female_pct}`, `audience.age_ranges.{18_24_pct, 25_49_pct, 50_plus_pct}`, `audience.social_class.{ab_pct, c_pct, de_pct}`

### 4.4 Cálculos detalhados

Sejam:
- `D(s)` = detecções da estação `s` no escopo (todas as categorias por padrão; filtrável)
- `pmm(s)` = PMM da estação
- `g_m(s), g_f(s)` = % gênero M/F na estação
- `ab(s), c(s), de(s)` = % classe social
- `age18(s), age25(s), age50(s)` = % faixa etária

**Impactos totais:**
```
impactos = Σ_s |D(s)| × pmm(s)
```

**Impactos por dimensão demográfica:**
```
impactos_M    = Σ_s |D(s)| × pmm(s) × g_m(s)
impactos_AB   = Σ_s |D(s)| × pmm(s) × ab(s)
impactos_25_49 = Σ_s |D(s)| × pmm(s) × age25(s)
```
(análogo para outras dimensões — sempre é o `impactos × percentual_da_estação`, somado em todas as estações)

**Tratamento de NULL:** se a estação não tem `pmm` ou perfil demográfico configurado, ela é **excluída do cálculo de impactos demográficos** (não soma e o `stations_count` mostra "X de Y com perfil completo" em tooltip). Para CPM (denominador), também excluída. Para gráfico 4 e breakdown de veiculações, é incluída normalmente — não depende de demografia.

**CPM:**
```
CPM = (investido_executado / impactos_total) × 1000
```
Usa o investido **executado** (não o contratado) — é a métrica de eficiência real. Mostrado como `R$ X,XX`.

**Bonificação:**
```
bonificacao_valor = count(detections WHERE category='orphan' AND scope) × valor_unitario
```
- Modo `per_insertion`: `valor_unitario = price_per_insertion` da campanha
- Modo `consolidated`: `valor_unitario = consolidated_value / programmed_count_total_da_campanha`

Quando múltiplas campanhas selecionadas com regras de preço diferentes, soma por campanha individualmente.

**Investido — Contratado:**
- Modo `consolidated`: `consolidated_value` da campanha, **prorrateado pela fração do período filtrado que cai dentro da janela da campanha** (`days_overlap / days_total_campaign`)
- Modo `per_insertion`: `price × programmed_count_in_overlap` (programmed_count filtrado pelo overlap)
- Soma de todas as campanhas selecionadas (modos podem ser heterogêneos — soma direta)

**Investido — Executado:**
- Modo `consolidated`: `consolidated_value × (veiculadas_no_overlap / programadas_no_overlap)`
- Modo `per_insertion`: `price × veiculated_count_in_overlap`

> Notas:
> - "veiculadas" para fins de Investido inclui só `in_slot + out_slot` (executou na data certa, mesmo fora do horário). Não conta `out_date` nem `orphan` — esses já estão na **Bonificação**.
> - Quando o usuário seleciona N campanhas com modos heterogêneos (algumas `consolidated`, outras `per_insertion`), o card de Investido soma direto. A sublinha mostra "N campanhas · X em consolidado · Y por inserção" para dar contexto.

**Buckets do gráfico 4:**

Calculado em duas passadas separadas e depois unidas por bucket:

1. **Esperado (programado):** das `distribution_rules`, expansão diária dentro do overlap do filtro com a janela da campanha. Soma por bucket.
2. **Executado:** das `detections`, agrupando por bucket × category.

Definição precisa por bucket:
- `programado` = nº de slots esperados pela `distribution_rules` no bucket (independe de execução)
- `in_slot` = count(detections WHERE category='in_slot' AND bucket)
- `out_slot` = count(detections WHERE category='out_slot' AND bucket)
- `out_date` = count(detections WHERE category='out_date' AND bucket)
- `deficit` = max(0, programado − in_slot − out_slot) — só conta como déficit o que faltou na data certa (out_date não compensa)
- `extras` = count(detections WHERE category='orphan' AND bucket)

> **Decisão de simplicidade:** "extras" é exclusivamente `orphan`. Não tentamos derivar "in_slot acima do programado por material" porque exigiria join material-a-material com `distribution_rules` por slot — complexidade desproporcional ao valor. Se um material foi tocado mais vezes que o programado dentro da janela, ainda conta como `in_slot` (não vai para extras). Essa limitação é documentada no tooltip do gráfico 4.

## 5. UI / Visual

### 5.1 Layout
```
┌────────────────────────────────────────────────────────────────────┐
│ Header: "Dashboard de Veiculação"                                  │
├────────────────────────────────────────────────────────────────────┤
│ FiltersBar (sticky): [Cliente] [Campanhas] [Período] [Emissoras]   │
│                                              [↓ Imagem] [↓ PDF]    │
├────────────────────────────────────────────────────────────────────┤
│ Row 1 (5 cards):                                                   │
│ [Impactos] [CPM] [Bonificação] [Investido ⇄] [Gênero M/F]          │
├────────────────────────────────────────────────────────────────────┤
│ Row 2 (3 gráficos lado-a-lado, grid 1fr 1fr 1fr):                  │
│ [Pirâmide Classe] [Faixa Etária] [% Veiculações]                   │
├────────────────────────────────────────────────────────────────────┤
│ Row 3 (1 gráfico full-width):                                      │
│ [Resumo diário/mensal — barras agrupadas]                          │
└────────────────────────────────────────────────────────────────────┘
                            <Footer />
```

### 5.2 Cards de KPI

Cada card:
- Branco, `--radius-xl`, `--shadow-sm`, hover translateY(-2px) + `--shadow-md` + borda `--color-tertiary-300`
- Padding `var(--spacing-lg)`
- Ícone 24px com fundo `--color-tertiary-100` em pílula
- Label `font-secondary` `weight-600` `font-size-xs` `--color-gray-500`
- Valor `font-primary` `weight-700` `font-size-3xl` `--color-gray-900`
- Sublinha de contexto `font-secondary` `font-size-xs` `--color-gray-500`

**Card 5 (Gênero):** Em vez de número grande, mostra mini barra horizontal stacked (~32px altura) com M em `--color-tertiary-500` e F em `--color-secondary-400`, com labels percentuais e absolutos abaixo.

**Card 4 (Investido):** Toggle chip no topo direito do card alternando "Contratado / Executado". Estado armazenado em URL param ou local state (não precisa persistir). Valor central reflete o modo selecionado.

### 5.3 Gráficos (Recharts)

**Cores semânticas globais (do design.md):**
- `in_slot` / sucesso → `--color-success-500` (verde)
- `out_slot` → `--color-warning-500` (amarelo/laranja)
- `out_date` → `--color-secondary-500` (roxo)
- `orphan/extras` → `--color-primary-500` (azul) ou `--color-info-500`
- `deficit` → `--color-error-500` (vermelho)
- `programado` → `--color-gray-400` tracejado

**Gráfico 1 — Pirâmide de Classe:** `BarChart layout="vertical"` com 3 barras horizontais (AB, C, DE). Gradiente único em tons de `--color-tertiary` (500 / 400 / 300). Tooltip glassmorphism mostra valor absoluto + %. Eixo X formatado em milhões/milhares pt-BR (`Intl.NumberFormat('pt-BR', { notation: 'compact' })`).

**Gráfico 2 — Faixa Etária:** `BarChart` vertical 3 barras (18-24, 25-49, 50+). Mesma paleta da pirâmide. Largura de barra confortável (~60px).

**Gráfico 3 — % Veiculações (donut):** `PieChart` com `innerRadius` (donut). 4 slices: in_slot, out_slot, out_date, extras/orphan. Centro do donut mostra o total de veiculações. Legenda à direita com cor + label + valor absoluto + %.

**Gráfico 4 — Resumo diário/mensal:** `BarChart` agrupado (não stacked — facilita comparação). Eixo X: bucket label (`DD/MM` para diário, `MMM/YY` para mensal — pt-BR). 6 séries lado a lado por bucket. Tooltip rico custom mostrando todos os 6 valores em ordem com cores. Altura ~360px.

### 5.4 FiltersBar

Sticky no topo abaixo do header da página. Fundo branco com `box-shadow: var(--shadow-sm)` no scroll, `padding var(--spacing-md) var(--spacing-lg)`, gap `var(--spacing-md)`.

Componentes:
- `RSelect` cliente (admin: select normal. Cliente: substituído por um chip read-only com o nome da própria conta, sem dropdown — visualmente comunica "você só vê seus dados")
- `RSelect` campanhas multi (opção `Período completo: DD/MM/AAAA – DD/MM/AAAA` aparece como sub-label)
- DateRangePicker (`react-day-picker` ou input nativo `type=date` × 2 — decidir na implementação) + chip `Período completo` que seta o range
- `RSelect` emissoras multi (carrega só após campanhas selecionadas)
- Botões `↓ Imagem` `↓ PDF` em outline com ícone

### 5.5 Empty States

**Admin sem cliente selecionado:** Tutorial Estilizado §4.7
- Lado A: ícone grande `MdInsights` (64px) em rosa, título "Selecione um cliente", texto "Escolha um cliente e até 10 campanhas para visualizar os impactos demográficos consolidados." CTA: foca o select de cliente
- Lado B: shadow UI dos cards + gráficos com opacidade reduzida (0.35) e cores pasteurizadas

**Cliente sem campanhas:** Tutorial Estilizado, lado A com texto "Você ainda não tem campanhas. Fale com seu gerente comercial.", sem CTA acionável

**Filtro retorna vazio:** Card único centralizado "Sem veiculações no período selecionado" com botão "Voltar ao mês corrente"

### 5.6 Export

**PNG:**
```javascript
import html2canvas from 'html2canvas'
const canvas = await html2canvas(dashboardRef.current, {
  backgroundColor: '#f8fafc',  // --color-gray-50
  scale: 2,                    // retina
  useCORS: true,
})
canvas.toBlob(blob => saveAs(blob, `dashboard-${clientName}-${period}.png`))
```
Captura tudo abaixo da `FiltersBar` (cards + gráficos).

**PDF:**
- A4 paisagem
- Página 1: capa (logo E-monitor, nome cliente, período, lista de campanhas selecionadas, data de geração)
- Página 2: PNG do dashboard centralizado, escalado para caber
- Reusa estrutura de `frontend/src/utils/pdfReport.js`. Cria função nova `exportInsightsPdf(payload, dashboardImage)`

## 6. Sidebar e Rota

**Admin** ([Sidebar.jsx:184](frontend/src/components/Sidebar.jsx)):
```jsx
<span className="sidebar-section-label">Veiculação</span>
<SidebarLink to="/campaigns">Campanhas</SidebarLink>
<SidebarLink to="/insights">Dashboard</SidebarLink>      {/* NOVO */}
<SidebarLink to="/detections">Veiculações</SidebarLink>
<SidebarLink to="/reports/airtime">Relatório data/hora</SidebarLink>
```

**Cliente** (ClientNav, mesmo arquivo): adicionar `<SidebarLink to="/insights">Dashboard</SidebarLink>` no grupo equivalente. Confirmar se já existe grupo "Veiculação" no ClientNav; se não, criar.

**App.jsx:** adicionar rota
```jsx
<Route path="/insights" element={<InsightsPage />} />
```
Não precisa `RequireRole` pois a página é acessível a admin E cliente. Anti-oracle é feito no backend.

## 7. Testes

### Backend
- `insights_test.go`: cenários
  - Cliente com 1 campanha, 1 estação, X detecções por categoria → confere impactos, CPM, breakdown
  - Cliente com 3 campanhas sobrepostas em períodos diferentes, filtro maior que todos → confere agregação
  - Cliente com estação sem PMM configurado → não soma impactos mas conta veiculações
  - Cliente cliente_A pedindo campanha do cliente_B → 403
  - Modo per_insertion vs consolidated → confere investido contratado/executado
  - Granularidade diária (filtro ≤ 31 dias) vs mensal (> 31 dias) — buckets corretos
  - Filtro com 0 emissoras retornadas → payload com zeros mas estrutura completa

### Frontend
- `InsightsPage.test.jsx`:
  - Renderiza sem cliente selecionado → empty tutorial
  - Renderiza com dados mockados → cards e gráficos
  - Toggle Contratado/Executado muda o valor exibido
  - Filtro de período "Período completo" seta o range correto
  - Export PNG chama html2canvas (mock)

### Visual / Manual
- Verificar em browser que hover dos cards aplica a animação rosa
- Conferir responsividade: row de 3 gráficos quebra em coluna única abaixo de 1024px; row de 5 cards quebra em 2+2+1 ou 3+2 abaixo de 1280px
- Conferir tooltips Recharts em todos os gráficos
- Validar PNG export em alta resolução (scale=2)

## 8. Riscos e Mitigações

| Risco | Mitigação |
|---|---|
| SQL pesado: 1 endpoint agregando 4–6 dimensões pode ficar lento com muitas detecções | CTE comum filtrada uma vez; índices em `detections(campaign_id, detected_at)` já existem. Monitorar via tracing OpenTelemetry. Se passar de 2s P95, adicionar materialized view diária. |
| Estações sem perfil demográfico configurado quebram cálculos | Excluídas do numerador/denominador de impactos demográficos. UI exibe contagem "X de Y estações com perfil completo" em tooltip do card de impactos. |
| Recharts não respeita tokens CSS dinamicamente | Constants hardcoded sincronizadas com `--color-tertiary-500` etc. Comentário no arquivo lembrando que se trocar tokens precisa atualizar constants. |
| html2canvas falha em CSS modernos (backdrop-filter, gradients) | Testar antes do merge. Se quebrar, fallback: usar `dom-to-image-more` ou rasterizar via canvas manualmente. |
| Cliente com muitas campanhas (>20) trava o RSelect | Virtualização do RSelect (`react-select` já suporta). Lazy load se passar de 50. |
| Múltiplas campanhas com regras de preço heterogêneas confundem usuário no card Investido | Tooltip do card explica "soma de N campanhas em modos mistos". Sub-linha mostra "X campanhas, Y em consolidado, Z por inserção". |

## 9. Métricas de Sucesso

- Time comercial para de montar dashboard manual em PDF — todas exportações vêm da tela
- Adoção: ao menos 50% dos clientes ativos abrem a tela em 30 dias após release
- P95 do endpoint `/api/v1/insights` < 1.5s para campanha típica (5 estações × 30 dias)
- Zero incidentes de cliente vendo dado de outro cliente (validar com teste de role-gating no CI)

## 10. Ordem de Implementação

1. **Backend**: schema das queries, repo `insights.go`, handler, testes com seed. Smoke test via curl
2. **Frontend skeleton**: rota, sidebar entry, `InsightsPage` com filtros e payload mockado
3. **Cards de KPI** + skeleton de loading
4. **3 gráficos demográficos** (Row 2)
5. **Gráfico daily/monthly** (Row 3) com bucketização
6. **Toggle Contratado/Executado** no card 4
7. **Empty states** (admin sem cliente, cliente sem campanhas, sem dados)
8. **Export PNG e PDF**
9. **Responsividade** e revisão visual
10. **Doc operacional** em `docs/features/insights-dashboard.md`

## 11. Documentação Pós-Implementação

Criar `docs/features/insights-dashboard.md` com:
- Header YAML (`status: implementado`)
- Fórmulas de cálculo (copiar §4.4)
- Mapa de roles e o que cada um vê
- Como adicionar nova métrica ao endpoint (extensibilidade)
- Troubleshooting (estação sem PMM, performance, export quebrado)

Atualizar `docs/README.md` com o link na seção de "Quando você for mexer em..."
