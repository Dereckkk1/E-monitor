# Tela "Materiais" (`/materials`) — Design

> Status: spec aprovado (brainstorming) · Data: 2026-05-27
> Autor: Dereck + Claude
> Telas-irmãs: [`detections-view`](../../features/detections-view.md), [`campaign-wizard`](../../features/campaign-wizard.md), [`material-library`](../../features/material-library.md)

## 1. Objetivo

Tela nova na seção **Veiculação**, acessível a **admin e cliente** (cliente só com as próprias campanhas/materiais). Permite, por campanha:

1. **Ouvir** cada material que a campanha tem — **todos os vinculados, programados ou não** (play/download), pra saber o que está no ar.
2. Ver **quanto está programado pra rodar em cada emissora** — a mesma grade visual da `/detections`, mas **só o programado** (células cinza), **sem** os veiculados, **sem** R$ e **sem** impactos; com o **Σ programado por emissora** ao lado.

Intenção da tela (palavras do dono): *ver quanto está programado pra rodar em cada emissora, e quantos materiais a campanha tem, sejam eles programados ou não.* Nada de cobrança, cobertura ou detecção real aqui — isso vive na `/detections` e `/insights`.

### 1.1 Não-objetivos
- Não mostra detecções/veiculados (verde/vermelho/bônus/etc).
- Não mostra R$, impactos ou cobertura.
- Não edita material, tipo, emissoras, regras ou script (isso é só no wizard de campanha).
- Não cria escopo novo de backend — reusa os endpoints e hooks existentes.

## 2. Decisões fechadas (brainstorming)

| # | Decisão | Valor |
|---|---------|-------|
| D1 | Estrutura da página | Foco em **1 campanha** (igual `/detections`): filtros no topo → painel de materiais → grade de plano |
| D2 | Filtros | **Igual `/detections`**: Competência → Campanha → Período (início→fim) |
| D3 | Materiais | Painel **separado acima** da grade (a grade é por *tipo*, materiais são *áudios* distintos) |
| D4 | Janela da grade | `capAtToday={false}` — mostra o período **inteiro**, inclusive dias futuros (é um plano) |
| D5 | R$/impactos | **Removidos** da grade nesta tela |
| D6 | Reuso de código | **Híbrido**: extrair só os helpers de data puros pra `utils/dates.js`; cada tela mantém seu JSX de barra |

## 3. Arquitetura

### 3.1 Arquivos

**Novos**
- `frontend/src/pages/MaterialsPage.jsx` — página principal.
- `frontend/src/components/MaterialPlaybackList.jsx` — painel de materiais tocáveis (read-only).
- `docs/features/materials-page.md` — doc operacional (header YAML).

**Editados**
- `frontend/src/App.jsx` — registra rota `/materials` (sem `RequireRole`).
- `frontend/src/components/Sidebar.jsx` — link "Materiais" na seção Veiculação de `AdminNav` **e** `ClientNav` + ícone novo.
- `frontend/src/utils/dates.js` — recebe os helpers de data puros (extração D6).
- `frontend/src/pages/DetectionsPage.jsx` — passa a **importar** os helpers de `utils/dates.js` (mudança mecânica; comportamento idêntico).
- `frontend/src/components/DistributionGrid.jsx` — novo prop opt-in `summary` (default mantém comportamento atual).
- `CLAUDE.md` + `docs/README.md` — linha de índice apontando pro doc novo.

### 3.2 Extração de helpers (D6)

Movidos de `DetectionsPage.jsx` para `utils/dates.js` (funções puras, baixo risco):
`pad2`, `monthFromDate`, `isoFromDate`, `monthToRange`, `monthLabel`, `rangeLabel`, `defaultRangeForCampaign`, `formatCampaignPeriod`.

`utils/dates.js` já exporta `parseLocalDate`. A `/detections` troca as definições locais por imports. **Nenhuma mudança de comportamento** — só relocação. Validação: a `/detections` continua funcionando idêntica após a troca.

> A barra de filtros (JSX + state machine `filterStep`) **não** é extraída nesta passada — fica duplicada de propósito (decisão D6) pra não tocar a renderização da `/detections`. Promover pra um `<CampaignFlowFilters>` compartilhado é follow-up opcional, num passo testado.

## 4. MaterialsPage — comportamento

### 4.1 Estado e dados
Espelha a `/detections`:
- `selectedMonth` (competência), `selectedCampaignId`, `userRange {start,end}`, `search`, `pageSize`, `page`.
- Suporta deep-link `?campaign_id=` (mesma lógica de `monthFromDeepLink`).
- Hooks: `useCampaigns`, `useClients`, `useStations({limit:2000, enabled:isAdmin})`, `useCampaignMaterials`, `useMaterials(client_id)`, `useMaterialTypes`, `useDistributionRules`, `useDailySummary(campaignId, mesFrom, mesTo)`.
- **Sem** `useCampaignPricing` (não há R$/impactos).

### 4.2 Filtros (topo)
Reusa classes CSS `flow-filters` + `FlowStepper` + `RSelect`:
1. **Competência** (`<input type="month">`)
2. **Campanha** (`RSelect` com `formatCampaignOption` mostrando cliente inline — copiado da /detections)
3. **Período** (dois `<input type="date">` início→fim, clampados ao intervalo mês ∩ campanha)

`filterStep = !selectedMonth ? 1 : !selectedCampaignId ? 2 : 3`.

### 4.3 Painel de materiais (`MaterialPlaybackList`)
- Renderiza quando `filterStep === 3` e a campanha tem materiais vinculados.
- Lista `campaignMaterials` → hidrata via `materialsById` (de `useMaterials(client_id)`), **ordenada/agrupada por tipo**.
- Cada item: `TypeIconPill` (cor do tipo) + título + duração (`fmtDuration`) + badge de fingerprint (`ready`/`generating`/`pending`/`failed`) + **selo de programação** + botão **play/pause** + botão **download**.
- **Selo de programação** (atende à intenção "programados ou não"): material é "programado" se seu `type_id` aparece em alguma `distributionRule` da campanha. Selo verde "programado" vs cinza "sem programação". Materiais sem tipo entram como "sem programação".
- **Áudio** (padrão existente do `MaterialsStep`): `api.get('/materials/{id}/audio', {responseType:'blob'})` → `URL.createObjectURL` → `<audio>`; blob cacheado por item, revogado no unmount. Só **um** material toca por vez (estado de `playingId` centralizado no painel).
- Read-only: sem editar tipo/emissoras/script.
- Lista **todos** os materiais vinculados (programados ou não). Contador no topo: "N materiais · K programados".

### 4.4 Grade de plano
- `DistributionGrid` com `mode="view"`, `inlineStationInfo`, `capAtToday={false}`, `summary="plan"`.
- `rows`: station × tipo, construídas como na `/detections` (a partir de `campaignMaterials` + `distributionRules` + `materialTypes`). Reusa a lógica de `typesInScopeByStation` → `rows`.
- `cellData`: de `useDailySummary`, **mapeado mantendo apenas `expected`** (in_slot/deficit/bonus/out_slot/out_date descartados). Resultado: célula cinza pura (confirmado em `DayCell` — cada badge só aparece se `> 0`).
- **Σ programado por emissora**: a coluna de total por emissora (`StationTotalCell`) **não é removida** — é repurposada pelo `summary="plan"` pra mostrar só "N programados" (soma de `expected` da emissora no período), sem R$ e sem impactos. Atende a "quanto está programado pra rodar em cada emissora".
- `gridStart`/`gridEnd` = período (início/fim) escolhido, com fallback pro intervalo da campanha (igual /detections).
- Paginação por emissora via `AirtimePaginator` (5/10/15, default 5).
- Busca (`search`) filtra emissora + material (mesmos campos da /detections).

### 4.5 Resumo do plano (no lugar do CoverageSummary)
Faixa enxuta acima da grade (componente leve inline, **não** o `CoverageSummary`): **N materiais (K programados) · M emissoras com programação · Σ inserções programadas no período** (soma de `expected` do `rangedSummary`). Sem cobertura, sem R$.

### 4.6 Empty states
Mesma linguagem visual da `/detections` (ghost backdrop + card central, reusando o padrão `DetectionsEmpty`/`FlowStepper`):
- `no-month` → escolher competência.
- `no-campaign` → escolher campanha (com contagem do mês).
- `no-rules` → campanha sem materiais **e** sem regras (CTA "Editar campanha" só se `isAdmin`).
- `no-plan` → período válido mas sem `expected` no recorte.

## 5. DistributionGrid — novo prop `summary`

Prop opt-in, **default preserva o comportamento atual** (telas existentes não mudam). **O layout de colunas não muda** entre os modos — ambas as colunas de resumo continuam montadas; só o *conteúdo* delas difere:

| Valor | RowSummaryCell (200px) | StationTotalCell (180px) | Usado por |
|-------|------------------------|--------------------------|-----------|
| `'full'` (default) | 6 pílulas (programada/veiculada/bônus/déficit/fora-faixa/fora-data) | impactos + R$ (+ bônus em R$) | `/detections`, wizard |
| `'plan'` | só a pílula cinza "programado" (Σ expected da linha) | só "N programados" da emissora (Σ expected), **sem R$/impactos** | `/materials` |

Implementação quando `summary="plan"`:
- `gridTemplate`, header "Resumo" e posições sticky-right **permanecem idênticos** (zero mudança de layout — menor risco de regressão).
- `RowSummaryCell` recebe `summary` e renderiza apenas `SumPill variant="dark"` (Σ expected da linha) quando `'plan'`.
- `StationTotalCell` recebe `summary` e, quando `'plan'`, calcula `Σ expected` de todas as linhas×dias da emissora e renderiza uma única `ValuePill` "N programados" (tone neutro), ignorando `pricing`/`pmm`.

`mode`, `capAtToday`, `inlineStationInfo` permanecem como estão. `pricingByStation`/`pmm` são ignorados quando `summary="plan"`.

## 6. Role gating
- Rota sem `RequireRole` (igual `/detections`). Backend escopa: cliente recebe só as próprias campanhas (`useCampaigns`) e materiais (`useMaterials`).
- CTA "Editar campanha" (empty `no-rules`) condicionado a `isAdmin`.
- Sidebar: link presente em `AdminNav` e `ClientNav`.

## 7. Documentação
- `docs/features/materials-page.md` com header YAML (`status: implementado`, `ultima-verificacao: 2026-05-27`, `codigo-relacionado` listando os arquivos novos/editados).
- Linha no mapa de consulta do `CLAUDE.md` e no índice `docs/README.md`.

## 8. Plano de validação
1. `/detections` continua idêntica após extração dos helpers (smoke manual: filtros, grade, paginação, empty states).
2. `/materials` como admin: competência→campanha→período; materiais tocam/baixam; grade mostra só cinza; período inteiro (futuro incluso); sem R$/impactos.
3. `/materials` como cliente: vê só as próprias campanhas; sem CTA de edição.
4. `DistributionGrid summary="full"` (default) inalterado em /detections e wizard.
5. Lint do frontend limpo.

## 9. Riscos
- **Extração dos helpers** pode introduzir typo/import quebrado na /detections → mitigado por smoke test e por ser relocação 1:1.
- **Novo prop no DistributionGrid** pode afetar telas existentes → mitigado por default `'full'` e revisão do layout sticky-right.
