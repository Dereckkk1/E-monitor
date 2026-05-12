# Plano 2 — Wizard Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Construir o wizard de criação de campanha em 4 etapas (rota `/campaigns/new` e `/campaigns/:id/edit`), incluindo o componente `<DistributionGrid mode="edit">` com side panel de regra e popover de override.

**Architecture:** React 19 + Vite + React Router 6 + TanStack React Query (sem state management adicional). Cada etapa é um componente focado; um `CampaignWizardPage` orquestra a navegação entre elas. A grade de distribuição é um componente compartilhado parametrizado por modo, com `mode="edit"` nesta entrega e `mode="view"` adicionado no Plano 3.

**Tech Stack:** React 19.2 · Vite 8 · React Router DOM 6.30 · TanStack React Query 5.100 · Axios 1.16 · React Select 5.10 · plain CSS (sem CSS Modules, sem Tailwind).

**Spec de referência:** [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](../specs/2026-05-11-campaign-wizard-design.md)
**Plano anterior:** [`2026-05-11-plano-1-foundations.md`](2026-05-11-plano-1-foundations.md) — backend completo, todos os endpoints já existem.

---

## Pré-requisitos

- [ ] Plano 1 (Foundations) deve estar mergeado em master OU sua branch deve estar baseada em `feat/campaign-wizard-foundations`. Confirme: `git log --oneline | grep "wire up new handlers"` deve retornar o commit `b3f9c78` (ou equivalente).
- [ ] API container deve estar rodando: `cd infra/docker && docker compose up -d api` e `curl http://localhost:8080/v1/internal/health` retornando 200.
- [ ] Frontend dev server testado: `cd frontend && npm install && npm run dev`. Abre em `http://localhost:5173`. Login funciona com credenciais bootstrap.

---

## Estrutura de arquivos

### Novos componentes (`frontend/src/components/`)

| Arquivo | Responsabilidade |
|---------|------------------|
| `BadgePill.jsx` | Pill compacto colorido (cinza/verde/vermelho/azul/amarelo/roxo/laranja) com número |
| `TypeIconPill.jsx` | Barra vertical colorida por tipo de material (para sub-linhas da grade) |
| `DayCell.jsx` | Célula da grade com até 4 badges + handler de clique |
| `MonthNavigator.jsx` | Botão ◀ + label "Junho 2026" + botão ▶ |
| `WizardStepper.jsx` | Stepper horizontal de 4 passos com progress bar |
| `WizardLayout.jsx` | Skeleton (stepper + strip + main + footer com Voltar/Avançar) |
| `CampaignSummaryStrip.jsx` | Strip compacta de 1 linha mostrando resumo da campanha |
| `DistributionGrid.jsx` | Grade emissora × material × dia. Parametrizada por `mode` (esta entrega: só `"edit"`) |
| `RuleSidePanel.jsx` | Side panel slide-from-right pra criar/editar regra |
| `OverridePopover.jsx` | Popover inline acima de célula pra override |

### Novas páginas (`frontend/src/pages/`)

| Arquivo | Responsabilidade |
|---------|------------------|
| `CampaignWizardPage.jsx` | Orquestrador do wizard (lê route param `id` opcional, decide criar vs editar, gerencia step state) |
| `CampaignWizardSteps/BasicDataStep.jsx` | Etapa 1: nome, cliente, datas |
| `CampaignWizardSteps/StationsStep.jsx` | Etapa 2: seleção de emissoras |
| `CampaignWizardSteps/MaterialsStep.jsx` | Etapa 3: biblioteca + upload + cards de vínculo |
| `CampaignWizardSteps/DistributionStep.jsx` | Etapa 4: usa DistributionGrid + Rule + Override |
| `MaterialTypesPage.jsx` | CRUD admin de tipos de material (lista + modal de create/edit) |

### Modificações

| Arquivo | Mudança |
|---------|---------|
| `frontend/src/api/hooks.js` | Adicionar ~10 hooks novos (material types, materials, campaign-materials, rules, overrides, daily-summary, get/update campaign) |
| `frontend/src/App.jsx` | Adicionar rotas `/campaigns/new`, `/campaigns/:id/edit`, `/material-types` |
| `frontend/src/pages/CampaignsPage.jsx` | Trocar botão "Nova campanha" pra `<Link to="/campaigns/new">`. Remover componente `NewCampaignModal` (inline no arquivo) e estado `showModal` |
| `frontend/src/index.css` | Adicionar tokens/classes de wizard (`.wizard-frame`, `.grid-cell`, etc) |

### Docs (criar)

| Arquivo | Conteúdo |
|---------|----------|
| `docs/campaign-wizard.md` | Guia operacional do wizard pro operador |

---

# Phase A — API hooks + routing

Estabelece o canal de comunicação com o backend (Plano 1) e prepara o roteamento. Sem UI nesta phase — só infra.

---

### Task 1: Adicionar hooks de API para as novas entidades

**Files:**
- Modify: `frontend/src/api/hooks.js`

- [ ] **Step 1: Ler o arquivo atual pra entender o padrão**

```bash
cat frontend/src/api/hooks.js | head -100
```

Confirme: existe `useStations(params)`, `useCampaigns()`, etc. Use o MESMO padrão (`useQuery` para GET, `useMutation` + `onSuccess` invalidating queries para mutations).

- [ ] **Step 2: Adicionar os novos hooks**

Append ao final de `frontend/src/api/hooks.js`:

```js
// ─── Material Types ─────────────────────────────────────────────────────────

export function useMaterialTypes() {
  return useQuery({
    queryKey: ['material-types'],
    queryFn: () => api.get('/material-types').then(r => r.data ?? []),
  })
}

export function useCreateMaterialType() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data) => api.post('/material-types', data).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['material-types'] }),
  })
}

export function useUpdateMaterialType() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }) => api.put(`/material-types/${id}`, body).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['material-types'] }),
  })
}

export function useDeleteMaterialType() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.delete(`/material-types/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['material-types'] }),
  })
}

// ─── Materials (per-client library) ────────────────────────────────────────

export function useMaterials(clientId, q = '') {
  return useQuery({
    queryKey: ['materials', clientId, q],
    queryFn: () => api.get(`/clients/${clientId}/materials`, { params: { q } }).then(r => r.data ?? []),
    enabled: !!clientId,
  })
}

export function useUploadMaterial() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (formData) => api.post('/materials', formData, {
      headers: { 'Content-Type': 'multipart/form-data' },
    }).then(r => r.data),
    onSuccess: (mat) => {
      qc.invalidateQueries({ queryKey: ['materials', mat.client_id] })
    },
  })
}

// useUpdateMaterialTypeId — changes a material's type_id (NOT the material_type entity).
// Distinct from useUpdateMaterialType above which edits a row in material_types.
export function useUpdateMaterialTypeId() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, type_id }) => api.patch(`/materials/${id}/type`, { type_id }).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['materials'] }),
  })
}

export function useDeleteMaterial() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.delete(`/materials/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['materials'] }),
  })
}

// ─── Campaign Materials (N:N link) ─────────────────────────────────────────

export function useCampaignMaterials(campaignId) {
  return useQuery({
    queryKey: ['campaign-materials', campaignId],
    queryFn: () => api.get(`/campaigns/${campaignId}/materials`).then(r => r.data ?? []),
    enabled: !!campaignId,
  })
}

export function useLinkCampaignMaterial() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, material_id, target_stations }) =>
      api.post(`/campaigns/${campaignId}/materials`, { material_id, target_stations }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['campaign-materials', vars.campaignId] })
    },
  })
}

export function useUnlinkCampaignMaterial() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, materialId }) =>
      api.delete(`/campaigns/${campaignId}/materials/${materialId}`),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['campaign-materials', vars.campaignId] })
    },
  })
}

export function useUpdateCampaignMaterialStations() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, materialId, target_stations }) =>
      api.put(`/campaigns/${campaignId}/materials/${materialId}/stations`, { target_stations }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['campaign-materials', vars.campaignId] })
    },
  })
}

// ─── Distribution Rules ────────────────────────────────────────────────────

export function useDistributionRules(campaignId) {
  return useQuery({
    queryKey: ['distribution-rules', campaignId],
    queryFn: () => api.get(`/campaigns/${campaignId}/distribution-rules`).then(r => r.data ?? []),
    enabled: !!campaignId,
  })
}

export function useCreateDistributionRule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, ...body }) =>
      api.post(`/campaigns/${campaignId}/distribution-rules`, body).then(r => r.data),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['distribution-rules', vars.campaignId] })
      qc.invalidateQueries({ queryKey: ['daily-summary', vars.campaignId] })
    },
  })
}

export function useUpdateDistributionRule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, ruleId, ...body }) =>
      api.put(`/campaigns/${campaignId}/distribution-rules/${ruleId}`, body),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['distribution-rules', vars.campaignId] })
      qc.invalidateQueries({ queryKey: ['daily-summary', vars.campaignId] })
    },
  })
}

export function useDeleteDistributionRule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, ruleId }) =>
      api.delete(`/campaigns/${campaignId}/distribution-rules/${ruleId}`),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['distribution-rules', vars.campaignId] })
      qc.invalidateQueries({ queryKey: ['daily-summary', vars.campaignId] })
    },
  })
}

// ─── Distribution Overrides ────────────────────────────────────────────────

export function useDistributionOverrides(campaignId, from, to) {
  return useQuery({
    queryKey: ['distribution-overrides', campaignId, from, to],
    queryFn: () => api.get(`/campaigns/${campaignId}/distribution-overrides`,
      { params: { from, to } }).then(r => r.data ?? []),
    enabled: !!campaignId && !!from && !!to,
  })
}

export function useUpsertOverride() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, ...body }) =>
      api.put(`/campaigns/${campaignId}/distribution-overrides`, body),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['distribution-overrides', vars.campaignId] })
      qc.invalidateQueries({ queryKey: ['daily-summary', vars.campaignId] })
    },
  })
}

export function useDeleteOverride() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, ...body }) =>
      api.delete(`/campaigns/${campaignId}/distribution-overrides`, { data: body }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['distribution-overrides', vars.campaignId] })
      qc.invalidateQueries({ queryKey: ['daily-summary', vars.campaignId] })
    },
  })
}

// ─── Daily Summary ─────────────────────────────────────────────────────────

export function useDailySummary(campaignId, from, to) {
  return useQuery({
    queryKey: ['daily-summary', campaignId, from, to],
    queryFn: () => api.get(`/campaigns/${campaignId}/daily-summary`,
      { params: { from, to } }).then(r => r.data ?? []),
    enabled: !!campaignId && !!from && !!to,
  })
}

// ─── Campaign single fetch + update (não existe ainda) ─────────────────────

export function useCampaign(id) {
  return useQuery({
    queryKey: ['campaigns', id],
    queryFn: () => api.get(`/campaigns/${id}`).then(r => r.data),
    enabled: !!id,
  })
}
```

Remova o `useUpdateMaterialType_` placeholder se não houver conflito — só estava ali pra evitar warning de duplicate export se você renomeou alguma coisa. Provavelmente pode deletar.

- [ ] **Step 3: Verificar que compila**

```bash
cd frontend
npm run lint
```

Esperado: sem novos erros. Se aparecer "Unused: useUpdateMaterialType_" delete a placeholder.

- [ ] **Step 4: Commit**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck"
git add frontend/src/api/hooks.js
git commit -m "feat(api-hooks): add hooks for material library + distribution + daily-summary"
```

---

### Task 2: Adicionar rotas no App.jsx

**Files:**
- Modify: `frontend/src/App.jsx`

- [ ] **Step 1: Ler o App.jsx atual**

```bash
cat frontend/src/App.jsx
```

Você vai ver `<Routes>` com várias `<Route>` dentro. Adicione novas rotas seguindo o mesmo padrão (com `RequireAuth` se as outras rotas privadas usarem).

- [ ] **Step 2: Adicionar imports e rotas**

No topo do arquivo, adicione os imports (junto com os outros imports de páginas):

```js
import CampaignWizardPage from './pages/CampaignWizardPage'
import MaterialTypesPage from './pages/MaterialTypesPage'
```

Dentro do `<Routes>`, ANTES da rota catch-all (se houver — provavelmente fim do bloco), adicione:

```jsx
<Route path="/campaigns/new" element={<RequireAuth><CampaignWizardPage /></RequireAuth>} />
<Route path="/campaigns/:id/edit" element={<RequireAuth><CampaignWizardPage /></RequireAuth>} />
<Route path="/material-types" element={<RequireAuth><MaterialTypesPage /></RequireAuth>} />
```

Confira o nome exato do componente HOC de auth — pode ser `RequireAuth` ou outro. Use o mesmo que `<Route path="/campaigns">` usa.

- [ ] **Step 3: Build vai falhar (componentes ainda não existem) — criar stubs**

Crie stubs vazios pra build passar enquanto os componentes reais são desenvolvidos nas próximas tasks.

Crie `frontend/src/pages/CampaignWizardPage.jsx`:

```jsx
export default function CampaignWizardPage() {
  return <div style={{ padding: 24 }}>CampaignWizardPage placeholder (Task 12)</div>
}
```

Crie `frontend/src/pages/MaterialTypesPage.jsx`:

```jsx
export default function MaterialTypesPage() {
  return <div style={{ padding: 24 }}>MaterialTypesPage placeholder (Task 18)</div>
}
```

- [ ] **Step 4: Verificar que a build passa**

```bash
cd frontend
npm run build
```

Esperado: build limpa. As novas rotas existem mas mostram placeholder.

- [ ] **Step 5: Commit**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck"
git add frontend/src/App.jsx frontend/src/pages/CampaignWizardPage.jsx frontend/src/pages/MaterialTypesPage.jsx
git commit -m "feat(routing): add wizard and material-types routes with placeholders"
```

---

# Phase B — Shared primitives

Componentes pequenos e focados que outras tasks vão compor. Sem dependência de API.

---

### Task 3: BadgePill component

**Files:**
- Create: `frontend/src/components/BadgePill.jsx`

Pill compacto com número, colorido por variant. Usado em DayCell e em qualquer lugar que mostre contadores categorizados.

- [ ] **Step 1: Implementar**

Crie `frontend/src/components/BadgePill.jsx`:

```jsx
const VARIANT_STYLES = {
  gray:   { background: '#e2e8f0', color: '#475569' },
  green:  { background: '#dcfce7', color: '#15803d' },
  red:    { background: '#fee2e2', color: '#b91c1c' },
  blue:   { background: '#dbeafe', color: '#1d4ed8' },
  yellow: { background: '#fef3c7', color: '#b45309' },
  purple: { background: '#ede9fe', color: '#6d28d9' },
  orange: { background: '#fde68a', color: '#b45309' },
}

/**
 * Compact colored pill with a number. Used in DayCell and grid headers.
 *
 * Props:
 *  - variant: 'gray' | 'green' | 'red' | 'blue' | 'yellow' | 'purple' | 'orange'
 *  - value: number to display (prefix shown for +/- variants)
 *  - prefix: optional string prepended to value (e.g. '+', '-')
 */
export default function BadgePill({ variant = 'gray', value, prefix = '', title }) {
  const style = VARIANT_STYLES[variant] ?? VARIANT_STYLES.gray
  return (
    <span
      title={title}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        minWidth: 18,
        height: 18,
        padding: '0 5px',
        borderRadius: 999,
        fontSize: 10,
        fontWeight: 700,
        lineHeight: 1,
        ...style,
      }}
    >
      {prefix}{value}
    </span>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/BadgePill.jsx
git commit -m "feat(ui): add BadgePill primitive (7 variants)"
```

---

### Task 4: TypeIconPill component

**Files:**
- Create: `frontend/src/components/TypeIconPill.jsx`

Barra vertical fina colorida por tipo de material (Spot/Testemunhal/etc). Aparece à esquerda do nome do material nas sub-linhas da grade.

- [ ] **Step 1: Implementar**

Crie `frontend/src/components/TypeIconPill.jsx`:

```jsx
/**
 * Vertical color bar representing a material type.
 * Width fixed at 5px, height inherits from container.
 *
 * Props:
 *  - color: hex string from material_types.color (e.g. '#3b82f6'). Defaults to gray.
 *  - height: pixel height (default 14)
 */
export default function TypeIconPill({ color = '#94a3b8', height = 14 }) {
  return (
    <span
      style={{
        display: 'inline-block',
        width: 5,
        height,
        borderRadius: 2,
        background: color,
        flexShrink: 0,
      }}
    />
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/TypeIconPill.jsx
git commit -m "feat(ui): add TypeIconPill primitive"
```

---

### Task 5: DayCell component

**Files:**
- Create: `frontend/src/components/DayCell.jsx`

Célula da grade emissora × material × dia. Renderiza até 4 badges com base em props categorizados. Suporta states: empty, weekend, outside-range, has-override, has-detection. Clicável.

- [ ] **Step 1: Implementar**

Crie `frontend/src/components/DayCell.jsx`:

```jsx
import BadgePill from './BadgePill'

const WEEKEND_BG = '#f8fafc'
const OUTSIDE_BG = 'repeating-linear-gradient(45deg, #fafbfc 0 5px, #f1f5f9 5px 10px)'
const OVERRIDE_BG = '#fef9c3'
const HOVER_BG = '#f8fafc'

/**
 * Cell of the distribution grid.
 *
 * Props:
 *  - expected: number | null         — gray badge (plan)
 *  - inSlot:   number | null         — green badge (played in slot)
 *  - deficit:  number | null         — red badge (still owed)
 *  - bonus:    number | null         — blue badge "+N"
 *  - outSlot:  number | null         — yellow badge "+N"
 *  - outDate:  number | null         — purple badge "+N"
 *  - isWeekend: bool                 — gray background, not clickable
 *  - isOutsideRange: bool            — striped pattern, not clickable
 *  - hasOverride: bool               — orange tint + dot indicator
 *  - onClick: () => void             — clicked (for editing override)
 *  - hint: string                    — tooltip text
 */
export default function DayCell({
  expected = null, inSlot = null, deficit = null,
  bonus = null, outSlot = null, outDate = null,
  isWeekend = false, isOutsideRange = false,
  hasOverride = false, onClick, hint,
}) {
  const disabled = isWeekend || isOutsideRange
  const bg = hasOverride ? OVERRIDE_BG
    : isOutsideRange ? OUTSIDE_BG
    : isWeekend ? WEEKEND_BG
    : '#fff'

  const showExpected = expected != null && expected > 0
  const showInSlot   = inSlot != null && inSlot > 0
  const showDeficit  = deficit != null && deficit > 0
  const showBonus    = bonus != null && bonus > 0
  const showOutSlot  = outSlot != null && outSlot > 0
  const showOutDate  = outDate != null && outDate > 0

  return (
    <div
      onClick={disabled ? undefined : onClick}
      title={hint}
      style={{
        position: 'relative',
        padding: '8px 4px',
        borderBottom: '1px solid #f1f5f9',
        borderRight: '1px solid #f1f5f9',
        background: bg,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 2,
        minHeight: 38,
        cursor: disabled ? 'default' : 'pointer',
      }}
      onMouseEnter={(e) => { if (!disabled) e.currentTarget.style.boxShadow = `inset 0 0 0 1px #cbd5e1` }}
      onMouseLeave={(e) => { e.currentTarget.style.boxShadow = 'none' }}
    >
      {hasOverride && (
        <span style={{
          position: 'absolute', top: 3, right: 3,
          width: 4, height: 4, borderRadius: '50%', background: '#b45309',
        }} />
      )}
      {showExpected && <BadgePill variant={hasOverride ? 'orange' : 'gray'} value={expected} />}
      {showInSlot   && <BadgePill variant="green"  value={inSlot} />}
      {showDeficit  && <BadgePill variant="red"    value={-deficit} />}
      {showBonus    && <BadgePill variant="blue"   value={bonus} prefix="+" />}
      {showOutSlot  && <BadgePill variant="yellow" value={outSlot} prefix="+" />}
      {showOutDate  && <BadgePill variant="purple" value={outDate} prefix="+" />}
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/DayCell.jsx
git commit -m "feat(ui): add DayCell component with multi-badge support"
```

---

### Task 6: MonthNavigator component

**Files:**
- Create: `frontend/src/components/MonthNavigator.jsx`

Botões prev/next + label do mês corrente.

- [ ] **Step 1: Implementar**

Crie `frontend/src/components/MonthNavigator.jsx`:

```jsx
const MONTH_NAMES = ['Janeiro', 'Fevereiro', 'Março', 'Abril', 'Maio', 'Junho',
  'Julho', 'Agosto', 'Setembro', 'Outubro', 'Novembro', 'Dezembro']

/**
 * Month navigator: < [Junho 2026] >
 *
 * Props:
 *  - value: Date (any day of the month being displayed)
 *  - onChange: (Date) => void — fires with first-of-month for new month
 *  - minDate?: Date — disables prev button if it would go before this
 *  - maxDate?: Date — disables next button if it would go after this
 */
export default function MonthNavigator({ value, onChange, minDate, maxDate }) {
  const y = value.getFullYear()
  const m = value.getMonth()
  const label = `${MONTH_NAMES[m]} ${y}`

  const prev = new Date(y, m - 1, 1)
  const next = new Date(y, m + 1, 1)

  const prevDisabled = minDate && prev < new Date(minDate.getFullYear(), minDate.getMonth(), 1)
  const nextDisabled = maxDate && next > new Date(maxDate.getFullYear(), maxDate.getMonth(), 1)

  return (
    <div style={{
      display: 'inline-flex', alignItems: 'center',
      border: '1px solid #e2e8f0', borderRadius: 8, overflow: 'hidden', background: '#fff',
    }}>
      <button
        onClick={() => onChange(prev)}
        disabled={prevDisabled}
        style={{
          padding: '7px 9px', border: 0, background: 'transparent',
          color: prevDisabled ? '#cbd5e1' : '#64748b',
          cursor: prevDisabled ? 'not-allowed' : 'pointer',
        }}
        aria-label="Mês anterior"
      >‹</button>
      <span style={{
        padding: '7px 12px', fontWeight: 600, color: '#0f172a', fontSize: 12,
        borderLeft: '1px solid #f1f5f9', borderRight: '1px solid #f1f5f9',
      }}>{label}</span>
      <button
        onClick={() => onChange(next)}
        disabled={nextDisabled}
        style={{
          padding: '7px 9px', border: 0, background: 'transparent',
          color: nextDisabled ? '#cbd5e1' : '#64748b',
          cursor: nextDisabled ? 'not-allowed' : 'pointer',
        }}
        aria-label="Próximo mês"
      >›</button>
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/MonthNavigator.jsx
git commit -m "feat(ui): add MonthNavigator component"
```

---

### Task 7: WizardStepper component

**Files:**
- Create: `frontend/src/components/WizardStepper.jsx`

Stepper horizontal de 4 passos com progress bar contínua na base.

- [ ] **Step 1: Implementar**

Crie `frontend/src/components/WizardStepper.jsx`:

```jsx
/**
 * Horizontal stepper with 4 fixed steps and a continuous progress bar.
 *
 * Props:
 *  - currentStep: 1 | 2 | 3 | 4
 *  - completedSteps: Array<number> — steps already done (for the ✓ icon)
 *  - onStepClick: (step) => void — fires when user clicks a step (controller decides if allowed)
 */
const STEPS = [
  { id: 1, label: 'Dados básicos' },
  { id: 2, label: 'Emissoras' },
  { id: 3, label: 'Materiais' },
  { id: 4, label: 'Distribuição' },
]

export default function WizardStepper({ currentStep, completedSteps = [], onStepClick }) {
  const progress = ((currentStep - 1) / (STEPS.length - 1)) * 100

  return (
    <div style={{
      display: 'flex', padding: '14px 24px', background: '#fff',
      borderBottom: '1px solid #f1f5f9', gap: 32, alignItems: 'center', position: 'relative',
    }}>
      {STEPS.map(step => {
        const done = completedSteps.includes(step.id)
        const active = step.id === currentStep
        const clickable = done || step.id < currentStep

        return (
          <button
            key={step.id}
            onClick={() => clickable && onStepClick?.(step.id)}
            disabled={!clickable}
            style={{
              display: 'flex', alignItems: 'center', gap: 10, fontSize: 12,
              color: active ? '#0f172a' : done ? '#15803d' : '#94a3b8',
              fontWeight: active ? 600 : 400,
              background: 'transparent', border: 0,
              cursor: clickable ? 'pointer' : 'default',
              padding: 0,
            }}
          >
            <span style={{
              width: 22, height: 22, borderRadius: '50%',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              fontWeight: 700, fontSize: 11,
              background: active ? '#E81E75' : done ? '#15803d' : '#f1f5f9',
              color: (active || done) ? '#fff' : '#94a3b8',
            }}>{done ? '✓' : step.id}</span>
            {step.label}
          </button>
        )
      })}
      <div style={{
        position: 'absolute', left: 24, right: 24, bottom: -1, height: 2,
        background: '#f1f5f9',
      }}>
        <div style={{
          height: '100%', width: `${progress}%`,
          background: '#E81E75',
          transition: 'width 250ms cubic-bezier(0.16,1,0.3,1)',
        }} />
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/WizardStepper.jsx
git commit -m "feat(ui): add WizardStepper with progress bar"
```

---

### Task 8: CampaignSummaryStrip component

**Files:**
- Create: `frontend/src/components/CampaignSummaryStrip.jsx`

Strip compacta de 1 linha mostrando o resumo da campanha (nome, cliente, datas, contadores).

- [ ] **Step 1: Implementar**

Crie `frontend/src/components/CampaignSummaryStrip.jsx`:

```jsx
function fmtDate(iso) {
  if (!iso) return '—'
  const [y, m, d] = iso.slice(0, 10).split('-')
  return `${d}/${m}/${y}`
}

/**
 * Compact 1-line summary strip used at the top of the wizard.
 *
 * Props:
 *  - name: string
 *  - clientName: string
 *  - startDate: ISO string
 *  - endDate: ISO string
 *  - stationCount: number
 *  - materialCount: number
 *  - distributedCount: number — how many (material × station) combinations have rules
 *  - distributedTotal: number — total combinations needed
 */
export default function CampaignSummaryStrip({
  name, clientName, startDate, endDate,
  stationCount = 0, materialCount = 0,
  distributedCount = 0, distributedTotal = 0,
}) {
  const dot = (
    <span style={{ width: 3, height: 3, borderRadius: '50%', background: '#cbd5e1', display: 'inline-block' }} />
  )
  const coverage = distributedTotal > 0
    ? distributedCount === distributedTotal ? 'good'
    : distributedCount === 0 ? 'bad'
    : 'warn'
    : null

  const coverageStyle = coverage === 'good' ? { background: '#dcfce7', color: '#15803d' }
    : coverage === 'warn' ? { background: '#fef9c3', color: '#a16207' }
    : coverage === 'bad'  ? { background: '#fee2e2', color: '#b91c1c' }
    : null

  return (
    <div style={{
      padding: '11px 24px', background: '#fafbfc',
      borderBottom: '1px solid #f1f5f9',
      fontSize: 11, color: '#475569',
      display: 'flex', gap: 18, alignItems: 'center', flexWrap: 'wrap',
    }}>
      <span><strong style={{ color: '#0f172a', fontWeight: 600 }}>{name || 'Nova campanha'}</strong></span>
      {clientName && <>{dot}<span>{clientName}</span></>}
      {startDate && endDate && <>{dot}<span>{fmtDate(startDate)} — {fmtDate(endDate)}</span></>}
      {dot}<span><strong style={{ color: '#0f172a', fontWeight: 600 }}>{stationCount}</strong> emissora{stationCount !== 1 && 's'}</span>
      {dot}<span><strong style={{ color: '#0f172a', fontWeight: 600 }}>{materialCount}</strong> material{materialCount !== 1 && 'is'}</span>
      {coverage && (
        <span style={{
          marginLeft: 'auto', padding: '3px 9px', borderRadius: 999,
          fontWeight: 600, fontSize: 11,
          ...coverageStyle,
        }}>{distributedCount} / {distributedTotal} distribuídos</span>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/CampaignSummaryStrip.jsx
git commit -m "feat(ui): add CampaignSummaryStrip component"
```

---

### Task 9: WizardLayout component

**Files:**
- Create: `frontend/src/components/WizardLayout.jsx`

Skeleton do wizard: header com nome da campanha + stepper + strip + main + footer.

- [ ] **Step 1: Implementar**

Crie `frontend/src/components/WizardLayout.jsx`:

```jsx
import { useNavigate } from 'react-router-dom'
import WizardStepper from './WizardStepper'
import CampaignSummaryStrip from './CampaignSummaryStrip'

/**
 * Skeleton for the 4-step campaign wizard.
 *
 * Props:
 *  - currentStep: 1..4
 *  - completedSteps: Array<number>
 *  - onStepClick: (step) => void
 *  - onPrev: () => void
 *  - onNext: () => void
 *  - nextDisabled: bool
 *  - nextLabel: string (default "Avançar →"; step 4 uses "Concluir campanha →")
 *  - prevLabel: string (default "← Voltar")
 *  - summary: props passed to CampaignSummaryStrip
 *  - children: ReactNode (step content)
 *  - title: string (header text, default "Nova campanha")
 */
export default function WizardLayout({
  currentStep, completedSteps, onStepClick,
  onPrev, onNext,
  nextDisabled = false,
  nextLabel = 'Avançar →',
  prevLabel = '← Voltar',
  summary,
  title = 'Nova campanha',
  children,
}) {
  const navigate = useNavigate()

  return (
    <div style={{
      background: '#fff',
      border: '1px solid #e2e8f0',
      borderRadius: 16,
      overflow: 'hidden',
      boxShadow: '0 1px 2px rgba(15,23,42,0.04)',
      margin: 16,
      display: 'flex',
      flexDirection: 'column',
      minHeight: 'calc(100vh - 32px)',
    }}>
      <div style={{
        padding: '18px 24px', borderBottom: '1px solid #e2e8f0',
        display: 'flex', justifyContent: 'space-between', alignItems: 'center',
      }}>
        <h2 style={{ margin: 0, fontSize: 16, fontFamily: 'inherit', fontWeight: 700 }}>{title}</h2>
        <button
          onClick={() => navigate('/campaigns')}
          aria-label="Fechar"
          style={{
            width: 28, height: 28, borderRadius: 8,
            background: '#f1f5f9', color: '#64748b',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            cursor: 'pointer', border: 0, fontSize: 14,
          }}
        >×</button>
      </div>

      <WizardStepper
        currentStep={currentStep}
        completedSteps={completedSteps}
        onStepClick={onStepClick}
      />

      {summary && <CampaignSummaryStrip {...summary} />}

      <div style={{ flex: 1, padding: '24px' }}>
        {children}
      </div>

      <div style={{
        padding: '14px 24px', borderTop: '1px solid #e2e8f0',
        display: 'flex', justifyContent: 'space-between', background: '#fff',
      }}>
        <button
          className="btn btn-secondary btn-sm"
          onClick={onPrev}
          disabled={currentStep === 1}
        >{prevLabel}</button>
        <button
          className="btn btn-primary btn-sm"
          onClick={onNext}
          disabled={nextDisabled}
        >{nextLabel}</button>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/WizardLayout.jsx
git commit -m "feat(ui): add WizardLayout skeleton (stepper + strip + footer)"
```

---

# Phase C — DistributionGrid + side panel + popover

A grade visual + componentes auxiliares. Esta phase é o coração visual da etapa 4.

---

### Task 10: DistributionGrid component

**Files:**
- Create: `frontend/src/components/DistributionGrid.jsx`

Grade emissora × material × dia. Recebe dados pré-computados via props (a página orquestradora junta `daily-summary` + `campaign-materials` + `stations`).

- [ ] **Step 1: Implementar**

Crie `frontend/src/components/DistributionGrid.jsx`:

```jsx
import DayCell from './DayCell'
import TypeIconPill from './TypeIconPill'
import StationAvatar from './StationAvatar'

/**
 * Grid of station × material × day with distribution badges.
 *
 * Modes:
 *  - "edit"  → cells clickable, opens override popover (Task 11/16)
 *  - "view"  → cells clickable, opens day detail modal (Plan 3)
 *
 * Data props:
 *  - month: Date (first of month being displayed)
 *  - campaignStart: ISO string
 *  - campaignEnd: ISO string
 *  - stations: Array<{id, name, frequency_mhz, city, ...}>
 *  - rows: Array<{
 *      stationId: uuid,
 *      materialId: uuid,
 *      materialTitle: string,
 *      typeColor: string,
 *      ruleSummary: string,  // e.g. "3×/dia 08:15-10:45"
 *      extraRules: number    // count of additional rules
 *    }>
 *  - cellData: Map<key, {expected, in_slot, deficit, bonus, out_slot, out_date, hasOverride}>
 *               where key = `${stationId}|${materialId}|${dateISO}`
 *  - onCellClick?: (stationId, materialId, date) => void
 *  - mode: "edit" | "view"
 */
export default function DistributionGrid({
  month, campaignStart, campaignEnd, stations, rows, cellData,
  onCellClick, mode = 'edit',
}) {
  const year = month.getFullYear()
  const monthIdx = month.getMonth()
  const daysInMonth = new Date(year, monthIdx + 1, 0).getDate()
  const days = Array.from({ length: daysInMonth }, (_, i) => new Date(year, monthIdx, i + 1))
  const dayNames = ['DOM','SEG','TER','QUA','QUI','SEX','SÁB']

  const cStart = new Date(campaignStart)
  const cEnd = new Date(campaignEnd)
  const today = new Date()
  today.setHours(0,0,0,0)

  // Group rows by station for rendering
  const byStation = new Map()
  for (const r of rows) {
    if (!byStation.has(r.stationId)) byStation.set(r.stationId, [])
    byStation.get(r.stationId).push(r)
  }

  // Compute grid-template-columns: 220px (header) + 130px (rule) + 64px per day
  const gridTemplate = `220px 130px repeat(${daysInMonth}, 64px)`

  return (
    <div style={{ overflowX: 'auto', background: '#fff', borderTop: '1px solid #f1f5f9' }}>
      <div style={{ display: 'grid', gridTemplateColumns: gridTemplate, fontSize: 12, minWidth: 'fit-content' }}>

        {/* Header row */}
        <div style={headStation}>Emissora / Material</div>
        <div style={head}>Regra</div>
        {days.map((d, i) => {
          const wkd = d.getDay() === 0 || d.getDay() === 6
          const isToday = d.toDateString() === today.toDateString()
          return (
            <div key={i} style={{
              ...head,
              color: wkd ? '#cbd5e1' : isToday ? '#E81E75' : '#64748b',
              background: isToday ? '#fdf2f8' : wkd ? '#f8fafc' : '#fafbfc',
            }}>
              <span style={{ display: 'block', fontSize: 9 }}>{dayNames[d.getDay()]}</span>
              <span style={{ display: 'block', fontSize: 13, color: '#0f172a', fontWeight: 700, marginTop: 2 }}>
                {String(d.getDate()).padStart(2, '0')}
              </span>
            </div>
          )
        })}

        {/* Station blocks + material rows */}
        {[...byStation.entries()].map(([stationId, stationRows]) => {
          const station = stations.find(s => s.id === stationId)
          if (!station) return null
          return (
            <RenderStationBlock
              key={stationId}
              station={station}
              stationRows={stationRows}
              days={days}
              cStart={cStart}
              cEnd={cEnd}
              cellData={cellData}
              onCellClick={onCellClick}
            />
          )
        })}

      </div>
    </div>
  )
}

const head = {
  background: '#fafbfc',
  borderBottom: '2px solid #e2e8f0',
  borderRight: '1px solid #f1f5f9',
  padding: '9px 6px',
  fontSize: 10,
  fontWeight: 600,
  textAlign: 'center',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  position: 'sticky',
  top: 0,
}

const headStation = { ...head, textAlign: 'left', paddingLeft: 14, textTransform: 'none', letterSpacing: 0, fontSize: 11 }

function RenderStationBlock({ station, stationRows, days, cStart, cEnd, cellData, onCellClick }) {
  return (
    <>
      <div style={{
        gridColumn: '1 / -1', background: '#fff', borderBottom: '1px solid #e2e8f0',
        padding: '11px 14px', display: 'flex', alignItems: 'center', gap: 10,
      }}>
        <StationAvatar station={station} size={30} />
        <div>
          <span style={{ fontWeight: 600, color: '#0f172a' }}>{station.name}</span>
          <span style={{ color: '#64748b', fontSize: 11, marginLeft: 6 }}>
            {station.band} {station.frequency_mhz ?? ''} · {station.city ?? ''}
          </span>
        </div>
      </div>

      {stationRows.map(row => (
        <>
          <div key={`label-${row.materialId}`} style={{
            padding: '11px 14px 11px 24px', background: '#fafbfc', color: '#334155',
            borderBottom: '1px solid #f1f5f9', borderRight: '1px solid #f1f5f9',
            display: 'flex', alignItems: 'center', gap: 8, fontSize: 12, fontWeight: 500,
          }}>
            <TypeIconPill color={row.typeColor} />
            {row.materialTitle}
          </div>
          <div key={`rule-${row.materialId}`} style={{
            padding: '8px 8px', background: '#fafbfc', color: '#be185d',
            borderBottom: '1px solid #f1f5f9', borderRight: '1px solid #f1f5f9',
            fontSize: 10, fontWeight: 600, lineHeight: 1.3,
            display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 2,
          }}>
            {row.ruleSummary || <span style={{ color: '#94a3b8' }}>—</span>}
            {row.extraRules > 0 && (
              <span style={{ fontSize: 9, color: '#64748b', fontWeight: 700 }}>+{row.extraRules} regra</span>
            )}
          </div>
          {days.map((d, i) => {
            const dateISO = d.toISOString().slice(0, 10)
            const key = `${row.stationId}|${row.materialId}|${dateISO}`
            const cell = cellData.get(key) ?? {}
            const isWeekend = d.getDay() === 0 || d.getDay() === 6
            const isOutsideRange = d < cStart || d > cEnd
            return (
              <DayCell
                key={`cell-${row.materialId}-${i}`}
                expected={cell.expected}
                inSlot={cell.in_slot}
                deficit={cell.deficit}
                bonus={cell.bonus}
                outSlot={cell.out_slot}
                outDate={cell.out_date}
                isWeekend={isWeekend}
                isOutsideRange={isOutsideRange}
                hasOverride={!!cell.hasOverride}
                onClick={() => onCellClick?.(row.stationId, row.materialId, dateISO)}
                hint={`${row.materialTitle} · ${dateISO}`}
              />
            )
          })}
        </>
      ))}
    </>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/DistributionGrid.jsx
git commit -m "feat(ui): add DistributionGrid component (station × material × day)"
```

---

### Task 11: RuleSidePanel component

**Files:**
- Create: `frontend/src/components/RuleSidePanel.jsx`

Side panel slide-from-right pra criar ou editar uma `distribution_rule`.

- [ ] **Step 1: Implementar**

Crie `frontend/src/components/RuleSidePanel.jsx`:

```jsx
import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import TypeIconPill from './TypeIconPill'

const WEEKDAY_NAMES = ['D','S','T','Q','Q','S','S']

/**
 * Slide-from-right panel for creating or editing a distribution rule.
 *
 * Props:
 *  - open: bool
 *  - onClose: () => void
 *  - onSubmit: (payload) => void   // payload matches POST/PUT distribution-rules
 *  - onDelete?: () => void         // only shown in edit mode
 *  - mode: "create" | "edit"
 *  - initial: { material_id, station_ids, start_date, end_date,
 *               weekday_mask, time_start, time_end, plays_per_day } | null
 *  - materials: Array<{id, title, type_color}>    // from campaign_materials
 *  - stations:  Array<{id, name}>                 // from campaign.target_stations
 *  - campaignStart: ISO date
 *  - campaignEnd:   ISO date
 *  - submitting: bool
 */
export default function RuleSidePanel({
  open, onClose, onSubmit, onDelete,
  mode = 'create', initial = null,
  materials = [], stations = [],
  campaignStart, campaignEnd,
  submitting = false,
}) {
  const [materialId, setMaterialId] = useState(initial?.material_id ?? '')
  const [stationIds, setStationIds] = useState(initial?.station_ids ?? [])
  const [startDate, setStartDate] = useState(initial?.start_date ?? campaignStart?.slice(0, 10) ?? '')
  const [endDate, setEndDate] = useState(initial?.end_date ?? campaignEnd?.slice(0, 10) ?? '')
  const [weekdayMask, setWeekdayMask] = useState(initial?.weekday_mask ?? 62) // Mon-Fri default
  const [timeStart, setTimeStart] = useState(initial?.time_start ?? '08:00')
  const [timeEnd, setTimeEnd] = useState(initial?.time_end ?? '10:00')
  const [playsPerDay, setPlaysPerDay] = useState(initial?.plays_per_day ?? 3)

  // Reset when opening with a new initial
  useEffect(() => {
    if (open) {
      setMaterialId(initial?.material_id ?? '')
      setStationIds(initial?.station_ids ?? [])
      setStartDate(initial?.start_date ?? campaignStart?.slice(0, 10) ?? '')
      setEndDate(initial?.end_date ?? campaignEnd?.slice(0, 10) ?? '')
      setWeekdayMask(initial?.weekday_mask ?? 62)
      setTimeStart(initial?.time_start ?? '08:00')
      setTimeEnd(initial?.time_end ?? '10:00')
      setPlaysPerDay(initial?.plays_per_day ?? 3)
    }
  }, [open, initial, campaignStart, campaignEnd])

  // Esc to close
  useEffect(() => {
    if (!open) return
    function onKey(e) { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  function toggleWeekday(idx) {
    setWeekdayMask(m => m ^ (1 << idx))
  }

  function toggleStation(id) {
    setStationIds(prev =>
      prev.includes(id) ? prev.filter(s => s !== id) : [...prev, id]
    )
  }

  function isValid() {
    return materialId &&
      stationIds.length > 0 &&
      startDate && endDate &&
      timeStart && timeEnd &&
      playsPerDay >= 1 && playsPerDay <= 100
  }

  function submit() {
    onSubmit({
      material_id: materialId,
      station_ids: stationIds,
      start_date: startDate,
      end_date: endDate,
      weekday_mask: weekdayMask,
      time_start: timeStart,
      time_end: timeEnd,
      plays_per_day: Number(playsPerDay),
    })
  }

  return createPortal(
    <div style={{
      position: 'fixed', inset: 0, background: 'rgba(15,23,42,0.4)',
      backdropFilter: 'blur(4px)', zIndex: 50,
    }} onClick={onClose}>
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          position: 'fixed', top: 0, right: 0, bottom: 0, width: 460,
          background: '#fff', boxShadow: '-24px 0 48px -12px rgba(15,23,42,0.25)',
          display: 'flex', flexDirection: 'column',
        }}
      >
        <div style={{ padding: '18px 22px', borderBottom: '1px solid #e2e8f0', display: 'flex', justifyContent: 'space-between' }}>
          <h3 style={{ margin: 0, fontSize: 15 }}>{mode === 'edit' ? 'Editar regra' : 'Adicionar regra'}</h3>
          <button onClick={onClose} style={{ width: 28, height: 28, border: 0, background: '#f1f5f9', borderRadius: 8, cursor: 'pointer' }}>×</button>
        </div>

        <div style={{ padding: '18px 22px', overflowY: 'auto', flex: 1 }}>

          {/* Material */}
          <div style={{ marginBottom: 16 }}>
            <Label>Material *</Label>
            <div style={chipRow}>
              {materials.map(m => (
                <button key={m.id}
                  onClick={() => setMaterialId(m.id)}
                  style={{ ...chip, ...(materialId === m.id ? chipOn : {}) }}
                >
                  <TypeIconPill color={m.type_color ?? '#94a3b8'} height={10} />
                  <span style={{ marginLeft: 5 }}>{m.title}</span>
                </button>
              ))}
            </div>
          </div>

          {/* Emissoras */}
          <div style={{ marginBottom: 16 }}>
            <Label>Emissoras *</Label>
            <div style={chipRow}>
              {stations.map(s => (
                <button key={s.id}
                  onClick={() => toggleStation(s.id)}
                  style={{ ...chip, ...(stationIds.includes(s.id) ? chipOn : {}) }}
                >{s.name}</button>
              ))}
            </div>
          </div>

          {/* Plays + Weekdays */}
          <div style={twoCol}>
            <div>
              <Label>Inserções por dia *</Label>
              <input type="number" min="1" max="100" value={playsPerDay}
                onChange={e => setPlaysPerDay(e.target.value)}
                style={input} />
            </div>
            <div>
              <Label>Dias da semana</Label>
              <div style={chipRow}>
                {WEEKDAY_NAMES.map((n, i) => {
                  const on = (weekdayMask & (1 << i)) !== 0
                  return (
                    <button key={i} onClick={() => toggleWeekday(i)}
                      style={{ ...chip, padding: '3px 7px', fontSize: 10, ...(on ? chipOn : { opacity: 0.45 }) }}
                    >{n}</button>
                  )
                })}
              </div>
            </div>
          </div>

          {/* Time window */}
          <div style={twoCol}>
            <div>
              <Label>Início da faixa</Label>
              <input type="time" value={timeStart} onChange={e => setTimeStart(e.target.value)} style={input} />
            </div>
            <div>
              <Label>Fim da faixa</Label>
              <input type="time" value={timeEnd} onChange={e => setTimeEnd(e.target.value)} style={input} />
            </div>
          </div>

          {/* Date range */}
          <div style={twoCol}>
            <div>
              <Label>Início do período</Label>
              <input type="date" value={startDate} min={campaignStart?.slice(0,10)} max={campaignEnd?.slice(0,10)}
                onChange={e => setStartDate(e.target.value)} style={input} />
            </div>
            <div>
              <Label>Fim do período</Label>
              <input type="date" value={endDate} min={startDate} max={campaignEnd?.slice(0,10)}
                onChange={e => setEndDate(e.target.value)} style={input} />
            </div>
          </div>
        </div>

        <div style={{ padding: '14px 22px', borderTop: '1px solid #e2e8f0', display: 'flex', justifyContent: 'space-between', gap: 8, background: '#fafbfc' }}>
          {mode === 'edit' && onDelete ? (
            <button onClick={onDelete} className="btn btn-danger btn-sm">🗑 Excluir regra</button>
          ) : <div />}
          <div style={{ display: 'flex', gap: 6 }}>
            <button onClick={onClose} className="btn btn-secondary btn-sm">Cancelar</button>
            <button onClick={submit} disabled={!isValid() || submitting} className="btn btn-primary btn-sm">
              {submitting ? 'Salvando…' : (mode === 'edit' ? 'Salvar' : 'Adicionar regra')}
            </button>
          </div>
        </div>
      </div>
    </div>,
    document.body
  )
}

const Label = ({ children }) => (
  <label style={{ display: 'block', fontSize: 11, color: '#64748b', fontWeight: 600,
    marginBottom: 6, letterSpacing: '0.04em', textTransform: 'uppercase' }}>
    {children}
  </label>
)

const input = {
  width: '100%', border: '1px solid #e2e8f0', borderRadius: 8,
  padding: '9px 12px', fontSize: 13, color: '#0f172a', boxSizing: 'border-box',
  fontFamily: 'inherit', background: '#fff',
}

const twoCol = { display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 16 }
const chipRow = { display: 'flex', flexWrap: 'wrap', gap: 5 }
const chip = {
  padding: '5px 10px', borderRadius: 999, background: '#f1f5f9', color: '#475569',
  fontSize: 11, fontWeight: 600, cursor: 'pointer', border: '1px solid transparent',
  display: 'inline-flex', alignItems: 'center',
}
const chipOn = { background: '#fdf2f8', color: '#be185d', borderColor: '#f9a8d4' }
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/RuleSidePanel.jsx
git commit -m "feat(ui): add RuleSidePanel (create/edit distribution rule)"
```

---

### Task 12: OverridePopover component

**Files:**
- Create: `frontend/src/components/OverridePopover.jsx`

Popover inline acima de uma célula clicada. Permite setar plays_expected ou "voltar à regra" (remover override).

- [ ] **Step 1: Implementar**

Crie `frontend/src/components/OverridePopover.jsx`:

```jsx
import { useState, useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'

/**
 * Inline popover for setting per-cell override.
 *
 * Props:
 *  - open: bool
 *  - anchorRect: DOMRect (from the clicked cell)
 *  - onClose: () => void
 *  - onApply: (newValue) => void
 *  - onRevert: () => void   // remove the override
 *  - currentRuleValue: number  // what the rule would produce (gray badge)
 *  - currentOverrideValue?: number | null  // existing override if any
 *  - materialTitle: string
 *  - stationName: string
 *  - date: ISO string
 */
export default function OverridePopover({
  open, anchorRect, onClose, onApply, onRevert,
  currentRuleValue, currentOverrideValue = null,
  materialTitle, stationName, date,
}) {
  const [value, setValue] = useState(currentOverrideValue ?? currentRuleValue ?? 0)
  const ref = useRef(null)

  useEffect(() => {
    setValue(currentOverrideValue ?? currentRuleValue ?? 0)
  }, [currentOverrideValue, currentRuleValue, open])

  // Close on outside click or Escape
  useEffect(() => {
    if (!open) return
    function onMouseDown(e) {
      if (ref.current && !ref.current.contains(e.target)) onClose()
    }
    function onKey(e) { if (e.key === 'Escape') onClose() }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open, onClose])

  if (!open || !anchorRect) return null

  // Position above the cell
  const top = anchorRect.top + window.scrollY - 200
  const left = Math.max(8, anchorRect.left + window.scrollX + anchorRect.width / 2 - 120)
  const dateStr = date ? date.slice(0, 10).split('-').reverse().join('/') : '—'

  return createPortal(
    <div ref={ref} style={{
      position: 'absolute', top, left, width: 240, zIndex: 60,
      background: '#fff', border: '1px solid #e2e8f0', borderRadius: 12,
      padding: '14px 16px',
      boxShadow: '0 10px 25px -5px rgba(15,23,42,0.18), 0 4px 10px -4px rgba(15,23,42,0.08)',
      fontSize: 12,
    }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8, color: '#64748b' }}>
        <span>{stationName} · {materialTitle}</span>
        <strong style={{ color: '#0f172a' }}>{dateStr}</strong>
      </div>
      <div style={{
        display: 'flex', justifyContent: 'space-between', alignItems: 'center',
        padding: 8, background: '#fafbfc', borderRadius: 6, marginBottom: 10,
      }}>
        <span style={{ color: '#64748b' }}>Regra: {currentRuleValue}×/dia</span>
      </div>
      <div style={{ display: 'flex', gap: 4, alignItems: 'center', marginBottom: 10 }}>
        <span style={{ fontSize: 11, color: '#475569' }}>Override:</span>
        <button onClick={() => setValue(v => Math.max(0, v - 1))}
          style={stepperBtn}>−</button>
        <input type="number" min="0" max="100" value={value}
          onChange={e => setValue(Number(e.target.value))}
          style={{
            width: 56, padding: '5px 8px', border: '1px solid #e2e8f0',
            borderRadius: 6, textAlign: 'center', fontWeight: 700, color: '#b45309',
            background: '#fef9c3', fontFamily: 'inherit',
          }} />
        <button onClick={() => setValue(v => Math.min(100, v + 1))}
          style={stepperBtn}>+</button>
      </div>
      <div style={{ display: 'flex', gap: 6 }}>
        {currentOverrideValue != null && (
          <button onClick={onRevert} style={{
            flex: 1, padding: 6, borderRadius: 6, fontSize: 11, fontWeight: 600,
            background: '#fafbfc', color: '#475569', border: '1px solid #e2e8f0', cursor: 'pointer',
          }}>↺ Voltar à regra</button>
        )}
        <button onClick={() => onApply(value)} className="btn btn-primary btn-sm" style={{ flex: 1 }}>
          Aplicar
        </button>
      </div>
    </div>,
    document.body
  )
}

const stepperBtn = {
  width: 24, height: 24, borderRadius: 6, border: '1px solid #e2e8f0',
  background: '#fff', fontWeight: 700, cursor: 'pointer', color: '#475569',
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/OverridePopover.jsx
git commit -m "feat(ui): add OverridePopover for per-cell rule override"
```

---

# Phase D — Wizard pages

A página orquestradora + as 4 etapas. Cada step é um componente focado que recebe state via props do wizard pai.

---

### Task 13: CampaignWizardPage (orchestrator)

**Files:**
- Modify: `frontend/src/pages/CampaignWizardPage.jsx` (substitui o stub)

Página principal que detecta create vs edit via route param, gerencia step state, e renderiza o step ativo.

- [ ] **Step 1: Substituir o stub**

Substitua todo o conteúdo de `frontend/src/pages/CampaignWizardPage.jsx`:

```jsx
import { useState, useEffect } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import {
  useCampaign, useCreateCampaign, useUpdateCampaignStations,
  useCampaignMaterials, useClients, useStations,
  useDistributionRules,
} from '../api/hooks'
import WizardLayout from '../components/WizardLayout'
import BasicDataStep from './CampaignWizardSteps/BasicDataStep'
import StationsStep from './CampaignWizardSteps/StationsStep'
import MaterialsStep from './CampaignWizardSteps/MaterialsStep'
import DistributionStep from './CampaignWizardSteps/DistributionStep'

export default function CampaignWizardPage() {
  const { id: routeId } = useParams()
  const navigate = useNavigate()
  const isEdit = !!routeId

  // Local state for the campaign being built (used in create mode before campaign exists)
  const [draftCampaign, setDraftCampaign] = useState({
    name: '', client_id: '', start_date: '', end_date: '',
  })
  const [campaignId, setCampaignId] = useState(routeId ?? null)
  const [currentStep, setCurrentStep] = useState(1)
  const [completedSteps, setCompletedSteps] = useState([])

  // Mode: edit → fetch existing
  const { data: existingCampaign } = useCampaign(routeId)
  useEffect(() => {
    if (existingCampaign) {
      setDraftCampaign({
        name: existingCampaign.name,
        client_id: existingCampaign.client_id,
        start_date: existingCampaign.start_date?.slice(0, 10) ?? '',
        end_date: existingCampaign.end_date?.slice(0, 10) ?? '',
      })
      setCampaignId(existingCampaign.id)
      setCompletedSteps([1, 2, 3]) // assume edit starts past step 1
    }
  }, [existingCampaign])

  const { data: clients = [] } = useClients()
  const { data: stationsData } = useStations({ limit: 2000 })
  const allStations = stationsData?.data ?? stationsData ?? []
  const { data: campaignMaterials = [] } = useCampaignMaterials(campaignId)
  const { data: distributionRules = [] } = useDistributionRules(campaignId)

  const clientName = clients.find(c => c.id === draftCampaign.client_id)?.name ?? ''
  const targetStationIds = existingCampaign?.target_stations ?? []
  const stationCount = targetStationIds.length
  const materialCount = campaignMaterials.length
  const distributedTotal = stationCount * materialCount
  const distributedCount = countCoveredCombinations(distributionRules, campaignMaterials, targetStationIds)

  const createCampaign = useCreateCampaign()

  function handleStepClick(step) {
    if (step <= currentStep || completedSteps.includes(step - 1)) {
      setCurrentStep(step)
    }
  }

  function markStepCompleteAndAdvance() {
    if (!completedSteps.includes(currentStep)) {
      setCompletedSteps([...completedSteps, currentStep])
    }
    setCurrentStep(s => Math.min(4, s + 1))
  }

  function handlePrev() {
    setCurrentStep(s => Math.max(1, s - 1))
  }

  async function handleNext() {
    // Step 1 in create mode: actually create the campaign
    if (currentStep === 1 && !campaignId) {
      try {
        const created = await createCampaign.mutateAsync({
          ...draftCampaign,
          start_date: draftCampaign.start_date + 'T00:00:00Z',
          end_date:   draftCampaign.end_date   + 'T00:00:00Z',
          target_stations: [],
        })
        setCampaignId(created.id)
        // Update route to /campaigns/:id/edit so refresh works
        navigate(`/campaigns/${created.id}/edit`, { replace: true })
      } catch (e) {
        alert('Erro ao criar campanha. Tente novamente.')
        return
      }
    }
    markStepCompleteAndAdvance()
  }

  function handleFinish() {
    navigate('/campaigns', { replace: true })
  }

  const summary = {
    name: draftCampaign.name,
    clientName,
    startDate: draftCampaign.start_date,
    endDate: draftCampaign.end_date,
    stationCount, materialCount,
    distributedCount, distributedTotal,
  }

  const title = isEdit ? `Editar: ${draftCampaign.name}` : 'Nova campanha'

  let stepContent = null
  let nextDisabled = false

  if (currentStep === 1) {
    stepContent = (
      <BasicDataStep
        value={draftCampaign}
        onChange={setDraftCampaign}
        clients={clients}
        isEditMode={isEdit}
      />
    )
    nextDisabled = !draftCampaign.name || !draftCampaign.client_id ||
                   !draftCampaign.start_date || !draftCampaign.end_date
  } else if (currentStep === 2) {
    stepContent = (
      <StationsStep
        campaignId={campaignId}
        allStations={allStations}
        currentSelection={targetStationIds}
      />
    )
    nextDisabled = stationCount === 0
  } else if (currentStep === 3) {
    stepContent = (
      <MaterialsStep
        campaignId={campaignId}
        clientId={draftCampaign.client_id}
        campaignStations={targetStationIds.map(id => allStations.find(s => s.id === id)).filter(Boolean)}
      />
    )
    nextDisabled = materialCount === 0
  } else if (currentStep === 4) {
    stepContent = (
      <DistributionStep
        campaignId={campaignId}
        campaignStart={existingCampaign?.start_date ?? draftCampaign.start_date}
        campaignEnd={existingCampaign?.end_date ?? draftCampaign.end_date}
        campaignMaterials={campaignMaterials}
        allStations={allStations}
      />
    )
    nextDisabled = false  // Concluir is always enabled on step 4
  }

  const nextLabel = currentStep === 4 ? 'Concluir campanha →' : 'Avançar →'
  const onNext = currentStep === 4 ? handleFinish : handleNext

  return (
    <WizardLayout
      title={title}
      currentStep={currentStep}
      completedSteps={completedSteps}
      onStepClick={handleStepClick}
      onPrev={handlePrev}
      onNext={onNext}
      nextDisabled={nextDisabled}
      nextLabel={nextLabel}
      summary={summary}
    >
      {stepContent}
    </WizardLayout>
  )
}

// Returns count of (station, material) pairs that have at least one distribution rule.
function countCoveredCombinations(rules, campaignMaterials, allTargetStations) {
  const covered = new Set()
  for (const r of rules) {
    for (const sid of r.station_ids) {
      covered.add(`${sid}|${r.material_id}`)
    }
  }
  return covered.size
}
```

- [ ] **Step 2: Build vai falhar (steps ainda não existem) — criar stubs**

Crie a pasta + stubs:

```bash
mkdir -p frontend/src/pages/CampaignWizardSteps
```

`frontend/src/pages/CampaignWizardSteps/BasicDataStep.jsx`:
```jsx
export default function BasicDataStep() { return <div>BasicDataStep (Task 14)</div> }
```

`frontend/src/pages/CampaignWizardSteps/StationsStep.jsx`:
```jsx
export default function StationsStep() { return <div>StationsStep (Task 15)</div> }
```

`frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx`:
```jsx
export default function MaterialsStep() { return <div>MaterialsStep (Task 16)</div> }
```

`frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx`:
```jsx
export default function DistributionStep() { return <div>DistributionStep (Task 17)</div> }
```

- [ ] **Step 3: Remova o placeholder + build limpa**

Confirme que o arquivo `frontend/src/api/hooks.js` NÃO contém nenhuma função `useUpdateMaterialType_` ou similar — só os hooks reais. Se removeu o placeholder do Step 2 corretamente, está OK.

```bash
cd frontend
npm run build
```

Esperado: build passa. Você pode navegar pra `/campaigns/new` e ver o stepper + step stubs.

- [ ] **Step 4: Commit**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck"
git add frontend/src/pages/CampaignWizardPage.jsx frontend/src/pages/CampaignWizardSteps/
git commit -m "feat(wizard): orchestrator page with step stubs"
```

---

### Task 14: BasicDataStep (Etapa 1 — nome/cliente/datas)

**Files:**
- Modify: `frontend/src/pages/CampaignWizardSteps/BasicDataStep.jsx`

- [ ] **Step 1: Substituir o stub**

```jsx
import RSelect from '../../components/RSelect'

/**
 * Step 1 of the wizard: campaign basic data.
 *
 * Props:
 *  - value: { name, client_id, start_date, end_date }
 *  - onChange: (newValue) => void
 *  - clients: Array<{id, name}>
 *  - isEditMode: bool — disables certain fields in edit mode (e.g. client_id)
 */
export default function BasicDataStep({ value, onChange, clients, isEditMode }) {
  function setField(k, v) {
    onChange({ ...value, [k]: v })
  }

  return (
    <div style={{ maxWidth: 640, margin: '0 auto' }}>
      <h3 style={{ marginTop: 0, fontSize: 16 }}>Dados básicos</h3>

      <div className="field">
        <label>Nome da campanha *</label>
        <input
          className="input"
          placeholder="ex: Verão 2026"
          value={value.name}
          onChange={e => setField('name', e.target.value)}
          autoFocus
        />
      </div>

      <div className="field">
        <label>Cliente *</label>
        <RSelect
          options={clients.map(c => ({ value: c.id, label: c.name }))}
          value={
            clients.find(c => c.id === value.client_id)
              ? { value: value.client_id, label: clients.find(c => c.id === value.client_id).name }
              : null
          }
          onChange={opt => setField('client_id', opt?.value ?? '')}
          placeholder="Selecione o cliente…"
          isClearable
          isDisabled={isEditMode}
        />
        {isEditMode && (
          <p style={{ fontSize: 12, color: '#64748b', marginTop: 4 }}>
            Cliente não pode ser alterado após criar a campanha.
          </p>
        )}
      </div>

      <div className="form-row">
        <div className="field">
          <label>Início *</label>
          <input
            className="input"
            type="date"
            value={value.start_date}
            onChange={e => setField('start_date', e.target.value)}
          />
        </div>
        <div className="field">
          <label>Fim *</label>
          <input
            className="input"
            type="date"
            value={value.end_date}
            min={value.start_date}
            onChange={e => setField('end_date', e.target.value)}
          />
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Build + commit**

```bash
cd frontend && npm run build && cd ..
git add frontend/src/pages/CampaignWizardSteps/BasicDataStep.jsx
git commit -m "feat(wizard): implement step 1 (basic data form)"
```

---

### Task 15: StationsStep (Etapa 2 — emissoras)

**Files:**
- Modify: `frontend/src/pages/CampaignWizardSteps/StationsStep.jsx`

Search-driven station picker. Usa `useStations({ q })` pra buscar; salva via `useUpdateCampaignStations`.

- [ ] **Step 1: Substituir o stub**

```jsx
import { useState, useEffect, useMemo } from 'react'
import { useStations, useUpdateCampaignStations, useCampaign } from '../../api/hooks'
import StationAvatar from '../../components/StationAvatar'

/**
 * Step 2 of the wizard: pick stations for the campaign.
 *
 * Props:
 *  - campaignId: uuid (set after step 1 completes)
 *  - allStations: Array<station> (pre-fetched in parent)
 *  - currentSelection: Array<uuid> (campaign.target_stations)
 */
export default function StationsStep({ campaignId, allStations, currentSelection }) {
  const [searchQ, setSearchQ] = useState('')
  const [debouncedQ, setDebouncedQ] = useState('')
  const [bandFilter, setBandFilter] = useState('')
  const [selectedIds, setSelectedIds] = useState(new Set(currentSelection))

  useEffect(() => { setSelectedIds(new Set(currentSelection)) }, [currentSelection])
  useEffect(() => {
    const t = setTimeout(() => setDebouncedQ(searchQ), 300)
    return () => clearTimeout(t)
  }, [searchQ])

  const { data: searchData } = useStations({ q: debouncedQ, band: bandFilter || undefined, limit: 100 })
  const searchResults = searchData?.data ?? searchData ?? []

  const updateCampaign = useUpdateCampaignStations()

  // Save on every change with debounce
  useEffect(() => {
    if (!campaignId) return
    const arr = [...selectedIds]
    const sameAsCurrent = arr.length === currentSelection.length &&
      arr.every(id => currentSelection.includes(id))
    if (sameAsCurrent) return
    const t = setTimeout(() => {
      updateCampaign.mutate({ id: campaignId, targetStations: arr })
    }, 600)
    return () => clearTimeout(t)
  }, [selectedIds, campaignId])

  function toggle(id) {
    setSelectedIds(prev => {
      const next = new Set(prev)
      next.has(id) ? next.delete(id) : next.add(id)
      return next
    })
  }

  const selectedStations = useMemo(
    () => [...selectedIds].map(id => allStations.find(s => s.id === id)).filter(Boolean),
    [selectedIds, allStations]
  )

  return (
    <div style={{ maxWidth: 960, margin: '0 auto' }}>
      <h3 style={{ marginTop: 0, fontSize: 16 }}>Selecione as emissoras</h3>

      {/* Toolbar */}
      <div style={{ display: 'flex', gap: 8, marginBottom: 16, flexWrap: 'wrap' }}>
        <input
          className="input"
          placeholder="Buscar por nome, cidade, dial…"
          value={searchQ}
          onChange={e => setSearchQ(e.target.value)}
          style={{ flex: 1, minWidth: 220 }}
        />
        <select className="input" value={bandFilter} onChange={e => setBandFilter(e.target.value)} style={{ width: 120 }}>
          <option value="">Todas bandas</option>
          <option value="AM">AM</option>
          <option value="FM">FM</option>
        </select>
      </div>

      {/* Selected chips */}
      {selectedStations.length > 0 && (
        <div style={{
          marginBottom: 16, padding: 12, background: '#fdf2f8',
          border: '1px solid #f9a8d4', borderRadius: 8,
        }}>
          <div style={{ fontSize: 11, color: '#be185d', fontWeight: 600, marginBottom: 6, textTransform: 'uppercase' }}>
            {selectedStations.length} emissora{selectedStations.length !== 1 ? 's' : ''} selecionada{selectedStations.length !== 1 ? 's' : ''}
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
            {selectedStations.map(s => (
              <span key={s.id} style={{
                padding: '4px 10px', background: '#fff', border: '1px solid #f9a8d4',
                borderRadius: 999, fontSize: 11, fontWeight: 600, color: '#be185d',
                display: 'inline-flex', alignItems: 'center', gap: 6,
              }}>
                {s.name}
                <button onClick={() => toggle(s.id)} style={{
                  border: 0, background: 'transparent', color: '#be185d', cursor: 'pointer', padding: 0,
                }}>×</button>
              </span>
            ))}
          </div>
        </div>
      )}

      {/* List */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        {searchResults.map(s => {
          const checked = selectedIds.has(s.id)
          return (
            <label key={s.id} style={{
              display: 'flex', alignItems: 'center', gap: 12, padding: 10,
              border: '1px solid #e2e8f0', borderRadius: 8, cursor: 'pointer',
              background: checked ? '#fdf2f8' : '#fff',
            }}>
              <input type="checkbox" checked={checked} onChange={() => toggle(s.id)} />
              <StationAvatar station={s} size={28} />
              <div style={{ flex: 1 }}>
                <div style={{ fontWeight: 600 }}>{s.name}</div>
                <div style={{ fontSize: 11, color: '#64748b' }}>
                  {s.band} {s.frequency_mhz ?? ''} · {s.city ?? ''} · {s.monitoring_status ?? ''}
                </div>
              </div>
            </label>
          )
        })}
        {searchResults.length === 0 && (
          <div style={{ padding: 24, textAlign: 'center', color: '#64748b' }}>
            Nenhuma emissora encontrada.
          </div>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Build + commit**

```bash
cd frontend && npm run build && cd ..
git add frontend/src/pages/CampaignWizardSteps/StationsStep.jsx
git commit -m "feat(wizard): implement step 2 (stations picker)"
```

---

### Task 16: MaterialsStep (Etapa 3 — materiais)

**Files:**
- Modify: `frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx`

Lista materiais vinculados + botão "Adicionar material" que abre side panel com 2 abas (biblioteca / upload).

- [ ] **Step 1: Substituir o stub**

```jsx
import { useState } from 'react'
import {
  useCampaignMaterials, useMaterials, useMaterialTypes,
  useLinkCampaignMaterial, useUnlinkCampaignMaterial, useUploadMaterial,
  useUpdateCampaignMaterialStations, useUpdateMaterialTypeId,
} from '../../api/hooks'
import TypeIconPill from '../../components/TypeIconPill'
import { useConfirm } from '../../components/ConfirmModal'

/**
 * Step 3 of the wizard: link/upload materials to the campaign.
 *
 * Props:
 *  - campaignId: uuid
 *  - clientId: uuid (campaign.client_id)
 *  - campaignStations: Array<station> (used as default for new links)
 */
export default function MaterialsStep({ campaignId, clientId, campaignStations }) {
  const { data: cmpMats = [] } = useCampaignMaterials(campaignId)
  const { data: materialTypes = [] } = useMaterialTypes()
  const { data: libMats = [] } = useMaterials(clientId)
  const link = useLinkCampaignMaterial()
  const unlink = useUnlinkCampaignMaterial()
  const updateType = useUpdateMaterialTypeId()
  const confirm = useConfirm()
  const [showAdd, setShowAdd] = useState(false)

  const typeById = Object.fromEntries(materialTypes.map(t => [t.id, t]))
  const libById  = Object.fromEntries(libMats.map(m => [m.id, m]))

  async function handleUnlink(materialId, title) {
    const ok = await confirm(`Desvincular "${title}" desta campanha?`)
    if (!ok) return
    unlink.mutate({ campaignId, materialId })
  }

  return (
    <div style={{ maxWidth: 960, margin: '0 auto' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <h3 style={{ margin: 0, fontSize: 16 }}>Materiais ({cmpMats.length})</h3>
        <button onClick={() => setShowAdd(true)} className="btn btn-primary btn-sm">
          + Adicionar material
        </button>
      </div>

      {cmpMats.length === 0 ? (
        <EmptyState onAdd={() => setShowAdd(true)} />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {cmpMats.map(link => {
            const mat = libById[link.material_id]
            if (!mat) return null
            const type = mat.type_id ? typeById[mat.type_id] : null
            return (
              <MaterialCard
                key={link.material_id}
                material={mat}
                link={link}
                type={type}
                allTypes={materialTypes}
                campaignStations={campaignStations}
                onTypeChange={(typeId) => updateType.mutate({ id: mat.id, type_id: typeId })}
                onUnlink={() => handleUnlink(mat.id, mat.title)}
              />
            )
          })}
        </div>
      )}

      {showAdd && (
        <AddMaterialPanel
          onClose={() => setShowAdd(false)}
          campaignId={campaignId}
          clientId={clientId}
          libraryMaterials={libMats}
          alreadyLinkedIds={new Set(cmpMats.map(l => l.material_id))}
          campaignStations={campaignStations}
          materialTypes={materialTypes}
        />
      )}
    </div>
  )
}

function EmptyState({ onAdd }) {
  return (
    <div style={{
      padding: 48, textAlign: 'center', background: '#fafbfc',
      border: '1px dashed #e2e8f0', borderRadius: 12,
    }}>
      <div style={{ fontSize: 40, color: '#cbd5e1', marginBottom: 8 }}>🎵</div>
      <h4 style={{ margin: '0 0 6px', color: '#0f172a' }}>Nenhum material ainda</h4>
      <p style={{ margin: '0 0 16px', color: '#64748b', fontSize: 13 }}>
        Vincule materiais existentes da biblioteca do cliente, ou suba arquivos novos.
      </p>
      <button onClick={onAdd} className="btn btn-primary btn-sm">+ Adicionar primeiro material</button>
    </div>
  )
}

function MaterialCard({ material, link, type, allTypes, campaignStations, onTypeChange, onUnlink }) {
  const stationNames = link.target_stations
    .map(id => campaignStations.find(s => s.id === id)?.name)
    .filter(Boolean)

  return (
    <div style={{
      padding: 12, background: '#fff', border: '1px solid #e2e8f0', borderRadius: 8,
      display: 'flex', alignItems: 'center', gap: 12,
    }}>
      <TypeIconPill color={type?.color ?? '#94a3b8'} height={32} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontWeight: 600, color: '#0f172a' }}>{material.title}</div>
        <div style={{ fontSize: 11, color: '#64748b', marginTop: 2 }}>
          {material.duration_seconds ? `${material.duration_seconds.toFixed(1)}s` : '—'}
          {' · '}
          {material.fingerprint_status === 'ready' ? '✓ pronto' :
           material.fingerprint_status === 'generating' ? '⏳ gerando' :
           material.fingerprint_status === 'failed' ? '✗ falhou' :
           'aguardando'}
          {stationNames.length > 0 ? ` · ${stationNames.length} emissora${stationNames.length !== 1 ? 's' : ''}` : ' · sem emissora'}
        </div>
      </div>
      <select
        value={material.type_id ?? ''}
        onChange={e => onTypeChange(e.target.value || null)}
        className="input"
        style={{ width: 160 }}
      >
        <option value="">Sem tipo</option>
        {allTypes.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
      </select>
      <button onClick={onUnlink} className="btn btn-icon btn-danger-ghost btn-sm" title="Desvincular">🗑</button>
    </div>
  )
}

function AddMaterialPanel({
  onClose, campaignId, clientId, libraryMaterials, alreadyLinkedIds,
  campaignStations, materialTypes,
}) {
  const [tab, setTab] = useState('library')
  const [selectedLibIds, setSelectedLibIds] = useState(new Set())
  const [uploadQueue, setUploadQueue] = useState([])
  const link = useLinkCampaignMaterial()
  const upload = useUploadMaterial()

  const available = libraryMaterials.filter(m => !alreadyLinkedIds.has(m.id))
  const defaultStationIds = campaignStations.map(s => s.id)

  async function linkSelected() {
    for (const id of selectedLibIds) {
      await link.mutateAsync({ campaignId, material_id: id, target_stations: defaultStationIds })
    }
    onClose()
  }

  function addFiles(files) {
    const entries = Array.from(files)
      .filter(f => /\.(wav|mp3|m4a|aac|mpeg)$/i.test(f.name))
      .map(file => ({
        key: `${file.name}-${Date.now()}-${Math.random()}`,
        file, title: file.name.replace(/\.[^.]+$/, ''), typeId: '', status: 'pending',
      }))
    setUploadQueue(q => [...q, ...entries])
  }

  async function submitUploads() {
    for (const entry of uploadQueue) {
      if (entry.status !== 'pending') continue
      const fd = new FormData()
      fd.append('client_id', clientId)
      fd.append('title', entry.title)
      if (entry.typeId) fd.append('type_id', entry.typeId)
      fd.append('audio', entry.file)
      try {
        const mat = await upload.mutateAsync(fd)
        await link.mutateAsync({ campaignId, material_id: mat.id, target_stations: defaultStationIds })
        setUploadQueue(q => q.map(e => e.key === entry.key ? { ...e, status: 'done' } : e))
      } catch {
        setUploadQueue(q => q.map(e => e.key === entry.key ? { ...e, status: 'error' } : e))
      }
    }
    setTimeout(onClose, 500)
  }

  return (
    <div style={{
      position: 'fixed', inset: 0, background: 'rgba(15,23,42,0.4)', backdropFilter: 'blur(4px)', zIndex: 50,
    }} onClick={onClose}>
      <div onClick={e => e.stopPropagation()} style={{
        position: 'fixed', top: 0, right: 0, bottom: 0, width: 480, background: '#fff',
        boxShadow: '-24px 0 48px -12px rgba(15,23,42,0.25)',
        display: 'flex', flexDirection: 'column',
      }}>
        <div style={{ padding: '18px 22px', borderBottom: '1px solid #e2e8f0', display: 'flex', justifyContent: 'space-between' }}>
          <h3 style={{ margin: 0 }}>Adicionar material</h3>
          <button onClick={onClose} style={{ width: 28, height: 28, border: 0, background: '#f1f5f9', borderRadius: 8, cursor: 'pointer' }}>×</button>
        </div>
        <div style={{ display: 'flex', borderBottom: '1px solid #e2e8f0' }}>
          <button onClick={() => setTab('library')} style={tabStyle(tab === 'library')}>Da biblioteca ({available.length})</button>
          <button onClick={() => setTab('upload')} style={tabStyle(tab === 'upload')}>Subir novo</button>
        </div>
        <div style={{ flex: 1, overflowY: 'auto', padding: 22 }}>
          {tab === 'library' ? (
            available.length === 0 ? (
              <p style={{ color: '#64748b' }}>Nenhum material disponível na biblioteca deste cliente. Use a aba "Subir novo" pra adicionar.</p>
            ) : (
              available.map(m => (
                <label key={m.id} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: 8, borderBottom: '1px solid #f1f5f9' }}>
                  <input type="checkbox" checked={selectedLibIds.has(m.id)}
                    onChange={() => {
                      const next = new Set(selectedLibIds)
                      next.has(m.id) ? next.delete(m.id) : next.add(m.id)
                      setSelectedLibIds(next)
                    }} />
                  <span style={{ flex: 1 }}>{m.title}</span>
                  <span style={{ fontSize: 11, color: '#64748b' }}>{m.duration_seconds?.toFixed(1)}s</span>
                </label>
              ))
            )
          ) : (
            <div>
              <input
                type="file"
                multiple
                accept=".wav,.mp3,.m4a,.aac,.mpeg"
                onChange={e => { addFiles(e.target.files); e.target.value = '' }}
              />
              <div style={{ marginTop: 12 }}>
                {uploadQueue.map(entry => (
                  <div key={entry.key} style={{
                    padding: 8, background: '#fafbfc', border: '1px solid #e2e8f0', borderRadius: 6, marginBottom: 6,
                  }}>
                    <div style={{ fontSize: 12, marginBottom: 4 }}>📎 {entry.file.name}</div>
                    <input
                      className="input"
                      placeholder="Título"
                      value={entry.title}
                      onChange={e => setUploadQueue(q => q.map(x => x.key === entry.key ? { ...x, title: e.target.value } : x))}
                      disabled={entry.status !== 'pending'}
                    />
                    <select
                      className="input"
                      value={entry.typeId}
                      onChange={e => setUploadQueue(q => q.map(x => x.key === entry.key ? { ...x, typeId: e.target.value } : x))}
                      style={{ marginTop: 4 }}
                      disabled={entry.status !== 'pending'}
                    >
                      <option value="">Selecione o tipo…</option>
                      {materialTypes.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
                    </select>
                    <div style={{ fontSize: 11, marginTop: 4, color: entry.status === 'done' ? '#15803d' : entry.status === 'error' ? '#b91c1c' : '#64748b' }}>
                      {entry.status === 'done' ? '✓ enviado' : entry.status === 'error' ? '✗ erro' : 'pendente'}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
        <div style={{ padding: '14px 22px', borderTop: '1px solid #e2e8f0', display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <button onClick={onClose} className="btn btn-secondary btn-sm">Cancelar</button>
          {tab === 'library' ? (
            <button onClick={linkSelected} disabled={selectedLibIds.size === 0} className="btn btn-primary btn-sm">
              Vincular {selectedLibIds.size > 0 ? `(${selectedLibIds.size})` : ''}
            </button>
          ) : (
            <button onClick={submitUploads} disabled={uploadQueue.length === 0 || upload.isPending} className="btn btn-primary btn-sm">
              Subir e vincular
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

const tabStyle = (active) => ({
  flex: 1, padding: '10px 14px', border: 0,
  borderBottom: active ? '2px solid #E81E75' : '2px solid transparent',
  background: '#fff', cursor: 'pointer',
  color: active ? '#E81E75' : '#64748b',
  fontWeight: active ? 600 : 400,
})
```

- [ ] **Step 2: Build + commit**

```bash
cd frontend && npm run build && cd ..
git add frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx
git commit -m "feat(wizard): implement step 3 (materials library + upload)"
```

---

### Task 17: DistributionStep (Etapa 4 — distribuição)

**Files:**
- Modify: `frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx`

Renderiza DistributionGrid + RuleSidePanel + OverridePopover. Orquestra o estado das regras e overrides.

- [ ] **Step 1: Substituir o stub**

```jsx
import { useState, useMemo, useRef } from 'react'
import {
  useDistributionRules, useDistributionOverrides, useDailySummary,
  useCreateDistributionRule, useUpdateDistributionRule, useDeleteDistributionRule,
  useUpsertOverride, useDeleteOverride,
  useMaterialTypes,
} from '../../api/hooks'
import DistributionGrid from '../../components/DistributionGrid'
import MonthNavigator from '../../components/MonthNavigator'
import RuleSidePanel from '../../components/RuleSidePanel'
import OverridePopover from '../../components/OverridePopover'
import { useConfirm } from '../../components/ConfirmModal'

const WEEKDAY_FULL = ['dom','seg','ter','qua','qui','sex','sáb']

/**
 * Step 4: distribution rules + grid + override.
 *
 * Props:
 *  - campaignId: uuid
 *  - campaignStart: ISO
 *  - campaignEnd: ISO
 *  - campaignMaterials: Array<{material_id, target_stations[], ...}>
 *  - allStations: Array<station>
 */
export default function DistributionStep({
  campaignId, campaignStart, campaignEnd,
  campaignMaterials, allStations,
}) {
  const cStart = new Date(campaignStart)
  const cEnd = new Date(campaignEnd)
  const [month, setMonth] = useState(new Date(cStart.getFullYear(), cStart.getMonth(), 1))

  const fromISO = new Date(month.getFullYear(), month.getMonth(), 1).toISOString().slice(0, 10)
  const toISO   = new Date(month.getFullYear(), month.getMonth() + 1, 0).toISOString().slice(0, 10)

  const { data: rules = [] } = useDistributionRules(campaignId)
  const { data: overrides = [] } = useDistributionOverrides(campaignId, fromISO, toISO)
  const { data: summary = [] } = useDailySummary(campaignId, fromISO, toISO)
  const { data: materialTypes = [] } = useMaterialTypes()

  const createRule = useCreateDistributionRule()
  const updateRule = useUpdateDistributionRule()
  const deleteRule = useDeleteDistributionRule()
  const upsertOverride = useUpsertOverride()
  const deleteOverride = useDeleteOverride()
  const confirm = useConfirm()

  // Modal state
  const [ruleEditOpen, setRuleEditOpen] = useState(false)
  const [editingRule, setEditingRule] = useState(null)
  const [popoverAnchor, setPopoverAnchor] = useState(null)
  const [popoverContext, setPopoverContext] = useState(null) // { stationId, materialId, date }

  const typeColorById = Object.fromEntries(materialTypes.map(t => [t.id, t.color]))

  // Build "rows" for the grid: one row per (station, material) combination with at least one rule.
  // For materials without rules, still show a row (gray) so operator knows it exists.
  const rows = useMemo(() => {
    const r = []
    for (const cm of campaignMaterials) {
      const mat = cm.material  // wait — campaign_materials response doesn't include material body
      // We need to find the material in some list. For now, just use the id and link.
      for (const sid of cm.target_stations) {
        // Find rules for this station+material
        const matching = rules.filter(rule =>
          rule.material_id === cm.material_id && rule.station_ids.includes(sid))
        const first = matching[0]
        const ruleSummary = first
          ? `${first.plays_per_day}×/dia ${first.time_start}–${first.time_end}`
          : null
        r.push({
          stationId: sid,
          materialId: cm.material_id,
          materialTitle: cm.material_title ?? '(material)',  // see note below
          typeColor: typeColorById[cm.material_type_id] ?? '#94a3b8',
          ruleSummary,
          extraRules: Math.max(0, matching.length - 1),
        })
      }
    }
    return r
  }, [campaignMaterials, rules, typeColorById])

  // Build cellData map from summary rows.
  const cellData = useMemo(() => {
    const m = new Map()
    for (const s of summary) {
      const key = `${s.station_id}|${s.material_id}|${s.for_date.slice(0, 10)}`
      const hasOverride = overrides.some(o =>
        o.station_id === s.station_id && o.material_id === s.material_id &&
        o.for_date.slice(0, 10) === s.for_date.slice(0, 10))
      m.set(key, { ...s, hasOverride })
    }
    return m
  }, [summary, overrides])

  function openRuleEditor(existing = null) {
    setEditingRule(existing)
    setRuleEditOpen(true)
  }

  async function submitRule(payload) {
    if (editingRule) {
      await updateRule.mutateAsync({ campaignId, ruleId: editingRule.id, ...payload })
    } else {
      await createRule.mutateAsync({ campaignId, ...payload })
    }
    setRuleEditOpen(false)
  }

  async function handleDeleteRule() {
    if (!editingRule) return
    const ok = await confirm('Excluir esta regra?')
    if (!ok) return
    await deleteRule.mutateAsync({ campaignId, ruleId: editingRule.id })
    setRuleEditOpen(false)
  }

  function handleCellClick(stationId, materialId, dateISO, target) {
    const rect = target?.getBoundingClientRect()
    setPopoverAnchor(rect)
    setPopoverContext({ stationId, materialId, date: dateISO })
  }

  // We need a way to capture the clicking element. DistributionGrid currently doesn't pass it.
  // Workaround: use a ref to capture last clicked DOM target via a click listener delegation here.
  const gridContainerRef = useRef(null)
  function handleClickCapture(e) {
    // No-op — the grid will call handleCellClick directly. Removed delegation for clarity.
  }

  const ctx = popoverContext
  const cellKey = ctx ? `${ctx.stationId}|${ctx.materialId}|${ctx.date}` : null
  const cellInfo = cellKey ? cellData.get(cellKey) : null
  const matchingOverride = ctx ? overrides.find(o =>
    o.station_id === ctx.stationId && o.material_id === ctx.materialId &&
    o.for_date.slice(0, 10) === ctx.date) : null

  return (
    <div ref={gridContainerRef} onClick={handleClickCapture}>
      {/* Toolbar */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '0 0 12px' }}>
        <h3 style={{ margin: 0, fontSize: 16 }}>Distribua os materiais</h3>
        <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
          <span style={{ fontSize: 11, color: '#64748b' }}>
            {rules.length} regra{rules.length !== 1 ? 's' : ''}, {overrides.length} override{overrides.length !== 1 ? 's' : ''}
          </span>
          <MonthNavigator
            value={month}
            onChange={setMonth}
            minDate={cStart}
            maxDate={cEnd}
          />
          <button onClick={() => openRuleEditor(null)} className="btn btn-primary btn-sm">
            + Regra
          </button>
        </div>
      </div>

      {rows.length === 0 ? (
        <EmptyDistributionState onAddRule={() => openRuleEditor(null)} />
      ) : (
        <DistributionGrid
          mode="edit"
          month={month}
          campaignStart={campaignStart}
          campaignEnd={campaignEnd}
          stations={allStations}
          rows={rows}
          cellData={cellData}
          onCellClick={(stationId, materialId, date) => {
            // The grid doesn't expose the target element directly. As a workaround,
            // use document.elementFromPoint of mouse, or restructure DistributionGrid
            // to forward the click event. For now, position popover at fixed location.
            const rect = { top: window.innerHeight / 2, left: window.innerWidth / 2 - 120, width: 0, height: 0 }
            setPopoverAnchor(rect)
            setPopoverContext({ stationId, materialId, date })
          }}
        />
      )}

      {/* Rule editor */}
      <RuleSidePanel
        open={ruleEditOpen}
        onClose={() => setRuleEditOpen(false)}
        onSubmit={submitRule}
        onDelete={editingRule ? handleDeleteRule : undefined}
        mode={editingRule ? 'edit' : 'create'}
        initial={editingRule}
        materials={campaignMaterials.map(cm => ({
          id: cm.material_id,
          title: cm.material_title ?? '(material)',
          type_color: typeColorById[cm.material_type_id],
        }))}
        stations={allStations.filter(s =>
          campaignMaterials.some(cm => cm.target_stations.includes(s.id))
        )}
        campaignStart={campaignStart}
        campaignEnd={campaignEnd}
        submitting={createRule.isPending || updateRule.isPending}
      />

      {/* Override popover */}
      <OverridePopover
        open={!!popoverAnchor && !!ctx}
        anchorRect={popoverAnchor}
        onClose={() => { setPopoverAnchor(null); setPopoverContext(null) }}
        onApply={async (newValue) => {
          await upsertOverride.mutateAsync({
            campaignId,
            material_id: ctx.materialId,
            station_id: ctx.stationId,
            for_date: ctx.date,
            plays_expected: newValue,
          })
          setPopoverAnchor(null); setPopoverContext(null)
        }}
        onRevert={async () => {
          await deleteOverride.mutateAsync({
            campaignId,
            material_id: ctx.materialId,
            station_id: ctx.stationId,
            for_date: ctx.date,
          })
          setPopoverAnchor(null); setPopoverContext(null)
        }}
        currentRuleValue={cellInfo?.expected ?? 0}
        currentOverrideValue={matchingOverride?.plays_expected ?? null}
        materialTitle={rows.find(r => r.materialId === ctx?.materialId)?.materialTitle ?? '—'}
        stationName={allStations.find(s => s.id === ctx?.stationId)?.name ?? '—'}
        date={ctx?.date}
      />
    </div>
  )
}

function EmptyDistributionState({ onAddRule }) {
  return (
    <div style={{
      padding: 48, textAlign: 'center', background: '#fafbfc',
      border: '1px dashed #e2e8f0', borderRadius: 12,
    }}>
      <div style={{ fontSize: 40, color: '#cbd5e1', marginBottom: 8 }}>▦</div>
      <h4 style={{ margin: '0 0 6px', color: '#0f172a' }}>Comece criando uma regra</h4>
      <p style={{ margin: '0 0 16px', color: '#64748b', fontSize: 13 }}>
        Uma regra define quantas vezes um material toca por dia, em quais emissoras, faixa horária e período.
      </p>
      <button onClick={onAddRule} className="btn btn-primary btn-sm">+ Adicionar primeira regra</button>
    </div>
  )
}
```

**KNOWN GAP:** este componente assume que `campaignMaterials` traz `material_title` e `material_type_id` no payload. O endpoint atual do backend retorna só `(campaign_id, material_id, target_stations, added_at)` sem hidratar. **A engenharia tem 2 opções:**

(A) Modificar o backend pra incluir `material_title` e `type_id` no JOIN — pequena mudança no `ListByCampaign` do `CampaignMaterials` repo.

(B) Hidratar no frontend: usar `useMaterials(clientId)` no `CampaignWizardPage` e passar o map material_id → material body via prop.

Recomendação: **(B)** pra não precisar modificar backend já mergeado. Atualize `CampaignWizardPage` pra passar uma prop `materialsById` na `MaterialsStep` e `DistributionStep`.

Pra simplicidade: marque o "TODO" e siga; o engenheiro de execução decide. O smoke test (Task 19) vai expor isso.

**KNOWN GAP 2:** o `OverridePopover` precisa do `anchorRect` da célula clicada. O `DistributionGrid` atualmente não captura/forward o evento DOM. Solução simples: aceitar `event.currentTarget.getBoundingClientRect()` em `onCellClick`. Modifique a assinatura pra passar `(stationId, materialId, date, event)` — atualize `DistributionGrid` e este componente.

- [ ] **Step 2: Build + commit (mesmo com gaps acima — vamos resolver no smoke test)**

```bash
cd frontend && npm run build && cd ..
git add frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx
git commit -m "feat(wizard): implement step 4 (distribution grid + rule panel + override popover)"
```

---

# Phase E — Integration + polish

---

### Task 18: Update CampaignsPage + remove modal

**Files:**
- Modify: `frontend/src/pages/CampaignsPage.jsx`

- [ ] **Step 1: Trocar o botão "Nova campanha" pra navegar pra `/campaigns/new`**

No `CampaignsPage.jsx`:

1. Localize o botão `<button className="btn btn-primary btn-sm" onClick={() => setShowModal(true)}>+ Nova campanha</button>`. Substitua por:

```jsx
import { Link } from 'react-router-dom'
// no import block at top — add Link if not already there

<Link to="/campaigns/new" className="btn btn-primary btn-sm">+ Nova campanha</Link>
```

2. Localize a referência ao `NewCampaignModal` (`{showModal && <NewCampaignModal ... />}`). Delete essa linha.

3. Localize o componente `function NewCampaignModal({...}) {...}` (linhas ~1011–1132). Delete o componente inteiro.

4. Localize o estado `const [showModal, setShowModal] = useState(false)`. Delete essa linha.

5. Adicione um botão "Editar" em cada `CampaignRow` que navega pra `/campaigns/:id/edit`. Dentro do bloco `<div className="campaign-row-actions" onClick={e => e.stopPropagation()}>` adicione:

```jsx
<Link to={`/campaigns/${campaign.id}/edit`} className="btn btn-secondary btn-sm">
  Editar
</Link>
```

- [ ] **Step 2: Verificar e commit**

```bash
cd frontend && npm run build && cd ..
git add frontend/src/pages/CampaignsPage.jsx
git commit -m "refactor(campaigns): use route-based wizard, remove inline modal"
```

---

### Task 19: MaterialTypesPage (admin CRUD)

**Files:**
- Modify: `frontend/src/pages/MaterialTypesPage.jsx`

Página simples lista + modal de create/edit.

- [ ] **Step 1: Substituir o stub**

```jsx
import { useState } from 'react'
import {
  useMaterialTypes, useCreateMaterialType,
  useUpdateMaterialType, useDeleteMaterialType,
} from '../api/hooks'
import { useConfirm } from '../components/ConfirmModal'

export default function MaterialTypesPage() {
  const { data: types = [], isLoading } = useMaterialTypes()
  const create = useCreateMaterialType()
  const update = useUpdateMaterialType()
  const del = useDeleteMaterialType()
  const confirm = useConfirm()
  const [editing, setEditing] = useState(null) // null | 'new' | type object
  const [form, setForm] = useState({ name: '', color: '#94a3b8', description: '' })

  function openNew() {
    setForm({ name: '', color: '#94a3b8', description: '' })
    setEditing('new')
  }
  function openEdit(t) {
    setForm({ name: t.name, color: t.color, description: t.description ?? '' })
    setEditing(t)
  }
  async function save() {
    const payload = { name: form.name, color: form.color, description: form.description || null }
    if (editing === 'new') {
      await create.mutateAsync(payload)
    } else {
      await update.mutateAsync({ id: editing.id, ...payload })
    }
    setEditing(null)
  }
  async function handleDelete(t) {
    const ok = await confirm(`Excluir o tipo "${t.name}"?`)
    if (!ok) return
    del.mutate(t.id)
  }

  return (
    <div>
      <div className="page-header">
        <h2>Tipos de material</h2>
        <button onClick={openNew} className="btn btn-primary btn-sm">+ Novo tipo</button>
      </div>

      {isLoading ? <p>Carregando…</p> : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {types.map(t => (
            <div key={t.id} style={{
              padding: 12, background: '#fff', border: '1px solid #e2e8f0',
              borderRadius: 8, display: 'flex', alignItems: 'center', gap: 12,
            }}>
              <span style={{ width: 6, height: 24, borderRadius: 2, background: t.color }} />
              <div style={{ flex: 1 }}>
                <div style={{ fontWeight: 600 }}>{t.name}</div>
                {t.description && <div style={{ fontSize: 11, color: '#64748b' }}>{t.description}</div>}
              </div>
              <button onClick={() => openEdit(t)} className="btn btn-secondary btn-sm">Editar</button>
              <button onClick={() => handleDelete(t)} className="btn btn-danger-ghost btn-sm">🗑</button>
            </div>
          ))}
        </div>
      )}

      {editing && (
        <div style={{
          position: 'fixed', inset: 0, background: 'rgba(15,23,42,0.4)',
          display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 50,
        }} onClick={() => setEditing(null)}>
          <div onClick={e => e.stopPropagation()} style={{
            background: '#fff', padding: 24, borderRadius: 12, width: 400,
          }}>
            <h3 style={{ marginTop: 0 }}>{editing === 'new' ? 'Novo tipo' : 'Editar tipo'}</h3>
            <div className="field">
              <label>Nome *</label>
              <input className="input" value={form.name}
                onChange={e => setForm({ ...form, name: e.target.value })} autoFocus />
            </div>
            <div className="field">
              <label>Cor</label>
              <input type="color" value={form.color}
                onChange={e => setForm({ ...form, color: e.target.value })} />
            </div>
            <div className="field">
              <label>Descrição (opcional)</label>
              <input className="input" value={form.description}
                onChange={e => setForm({ ...form, description: e.target.value })} />
            </div>
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 16 }}>
              <button onClick={() => setEditing(null)} className="btn btn-secondary btn-sm">Cancelar</button>
              <button onClick={save} disabled={!form.name} className="btn btn-primary btn-sm">Salvar</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Adicionar link no Sidebar**

Localize `frontend/src/components/Sidebar.jsx` e adicione um item de menu pro `/material-types` perto de "Campaigns" ou em "Settings". Padrão de outras entradas vale.

- [ ] **Step 3: Build + commit**

```bash
cd frontend && npm run build && cd ..
git add frontend/src/pages/MaterialTypesPage.jsx frontend/src/components/Sidebar.jsx
git commit -m "feat(material-types): admin CRUD page + sidebar link"
```

---

### Task 20: End-to-end smoke test + docs

**Files:**
- Create: `docs/campaign-wizard.md`

- [ ] **Step 1: Iniciar dev server e API**

```bash
cd "c:/Users/marke/Desktop/Programas/Radiocheck"
docker compose -f infra/docker/docker-compose.yml up -d
cd frontend && npm run dev
```

Abre em `http://localhost:5173`.

- [ ] **Step 2: Smoke test manual — fluxo completo**

Faça login. Navegue por `/campaigns` → clique "+ Nova campanha". Verifique:

1. **Etapa 1**: Preencha nome ("Smoke Test"), selecione cliente, datas de hoje até +30 dias. Clique "Avançar". Deve criar a campanha (URL muda pra `/campaigns/:id/edit`).
2. **Etapa 2**: Selecione 2-3 emissoras. Strip mostra "X emissoras". Clique "Avançar".
3. **Etapa 3**: Clique "+ Adicionar material" → aba "Subir novo" → escolha um MP3 pequeno → preencha título e tipo → "Subir e vincular". O material aparece como card. Clique "Avançar".
4. **Etapa 4**: Tela vazia mostra ghost de "Comece criando uma regra". Clique "+ Regra". Side panel abre. Selecione material, emissoras (chips), valida dias da semana, faixa horária 08:00–10:00, plays/day=3, datas. "Adicionar regra". Side panel fecha, grade preenche com badges cinza. Clique numa célula — override popover aparece. Mude pra 5, "Aplicar". Célula vira laranja.
5. Clique "Concluir campanha". Redireciona pra `/campaigns`, a campanha está na lista.

Anote qualquer bug ou comportamento estranho encontrado.

- [ ] **Step 3: Resolver os "KNOWN GAPS" da Task 17 que apareceram no smoke**

Dois gaps conhecidos do plano:

**Gap A — material_title e material_type_id não vêm em `campaign_materials`**: Hidrate no frontend.

Em `CampaignWizardPage.jsx`, ANTES do `return`, adicione:

```js
const { data: clientLibrary = [] } = useMaterials(draftCampaign.client_id ?? null)
const materialsById = useMemo(
  () => Object.fromEntries(clientLibrary.map(m => [m.id, m])),
  [clientLibrary]
)
```

Passe `materialsById` como prop pra `MaterialsStep` e `DistributionStep`. Atualize esses componentes pra resolver `material_title` e `material_type_id` via `materialsById[link.material_id]`.

Especificamente em `DistributionStep.jsx`, mude a construção de `rows`:

```js
for (const cm of campaignMaterials) {
  const mat = materialsById[cm.material_id]
  if (!mat) continue
  for (const sid of cm.target_stations) {
    const matching = rules.filter(r =>
      r.material_id === cm.material_id && r.station_ids.includes(sid))
    const first = matching[0]
    r.push({
      stationId: sid,
      materialId: cm.material_id,
      materialTitle: mat.title,
      typeColor: typeColorById[mat.type_id] ?? '#94a3b8',
      ruleSummary: first ? `${first.plays_per_day}×/dia ${first.time_start}–${first.time_end}` : null,
      extraRules: Math.max(0, matching.length - 1),
    })
  }
}
```

Mesmo padrão em `MaterialsStep` e `RuleSidePanel`'s materials prop.

**Gap B — OverridePopover precisa do `anchorRect`**: Modifique `DistributionGrid` pra passar o evento.

Em `DistributionGrid.jsx`, mude:
```jsx
onClick={() => onCellClick?.(row.stationId, row.materialId, dateISO)}
```
para
```jsx
onClick={(e) => onCellClick?.(row.stationId, row.materialId, dateISO, e.currentTarget.getBoundingClientRect())}
```

E em `DistributionStep.jsx`, atualize o handler:
```jsx
onCellClick={(stationId, materialId, date, rect) => {
  setPopoverAnchor(rect)
  setPopoverContext({ stationId, materialId, date })
}}
```

(Os parâmetros precisam fluir pelo `DayCell` também — passe `onClick={(e) => onClick?.(e)}` no DayCell pra que o parent receba o event.)

Após esses ajustes, build + retest manual:

```bash
cd frontend && npm run build && cd ..
git add -A frontend/
git commit -m "fix(wizard): hydrate material details in frontend; forward anchor rect to popover"
```

- [ ] **Step 4: Criar a doc operacional**

Crie `docs/campaign-wizard.md`:

```markdown
# Wizard de Campanha — Guia Operacional

Documenta o fluxo de 4 etapas pra criar/editar campanha. Implementado pelo Plano 2.

> Spec: [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](superpowers/specs/2026-05-11-campaign-wizard-design.md)
> Plano: [`docs/superpowers/plans/2026-05-11-plano-2-wizard-frontend.md`](superpowers/plans/2026-05-11-plano-2-wizard-frontend.md)

## Quando usar

- **Criar** uma campanha nova: `/campaigns/new`
- **Editar** uma campanha existente: `/campaigns/:id/edit` (acessível pelo botão "Editar" em cada linha de `/campaigns`)

## As 4 etapas

### 1. Dados básicos
- Nome (livre), cliente (dropdown), data início, data fim
- Cliente não pode ser alterado depois de criar a campanha (a biblioteca de materiais é por cliente)
- Salva a campanha imediatamente ao clicar "Avançar"

### 2. Emissoras
- Busca textual + filtro por banda (AM/FM)
- Toggle checkbox por linha
- Lista de selecionadas em chips no topo (com X pra remover)
- Salva automaticamente após 600ms de inatividade

### 3. Materiais
- Cards de materiais já vinculados
- Botão "+ Adicionar material" abre side panel com 2 abas:
  - **Da biblioteca**: lista materiais já existentes do cliente; multi-check pra vincular
  - **Subir novo**: drag-drop ou file picker; preencher título e tipo
- Vinculação sempre default = todas as emissoras da campanha
- Inline: dropdown de tipo pra reclassificar; botão de excluir (desvincula, não deleta da biblioteca)

### 4. Distribuição
- Grade emissora × material × dia, com toolbar mês-navegador
- Botão "+ Regra" abre side panel pra criar uma `distribution_rule`
- Click em célula abre popover de override (laranja)
- Botão "Concluir campanha" finaliza e volta pra `/campaigns`

## Edição de campanha existente

A rota `/campaigns/:id/edit` carrega a campanha pelo ID e abre o wizard com todos os steps marcados como completos. Permite mexer em qualquer etapa, sem auto-avançar.

**Limitações** (validação atualmente só no frontend — backend não bloqueia):
- Cliente não pode mudar
- Regras com `start_date < hoje` não devem ser editadas (ver F-91)

## Tipos de material

Gerenciado em `/material-types`. Globais (não por cliente). Cores hex usadas pra colorir a barra vertical das sub-linhas da grade.

## Limitações conhecidas

- Sem teste automatizado (depende de smoke manual)
- `probeDuration()` no backend retorna 30s stub — duração de áudio fica incorreta até F-88 ser resolvido
- Validação "future-only edit" só no frontend (F-91)
```

- [ ] **Step 5: Commit final do plano**

```bash
git add docs/campaign-wizard.md
git commit -m "docs: add campaign wizard operational guide"
```

---

## Critério de conclusão do Plano 2

- [ ] `npm run build` passa sem erros
- [ ] `npm run lint` sem novos warnings críticos
- [ ] Smoke test manual passa end-to-end (criar campanha completa nas 4 etapas)
- [ ] Edit mode funcional (clicar "Editar" numa campanha existente abre o wizard com dados carregados)
- [ ] `/material-types` lista os 6 seeds e permite criar/editar/excluir
- [ ] Grade da etapa 4 mostra badges cinza após criar regra
- [ ] Override popover funciona (aplica + "voltar à regra")
- [ ] Doc `docs/campaign-wizard.md` criada
- [ ] Plano 3 fica como próximo passo: refatorar `/detections` pra usar `<DistributionGrid mode="view">` com badges nas 6 cores

## Follow-ups pra registrar em `docs/follow-ups-fase2.md`

- **F-94** — Modificar `CampaignMaterials.ListByCampaign` no backend pra hidratar `material_title` e `type_id` no JOIN, evitando o `useMaterials(clientId)` extra no frontend
- **F-95** — Adicionar testes Vitest pra componentes críticos (DistributionGrid, RuleSidePanel, OverridePopover)
- **F-96** — Skeleton loaders nas etapas durante fetch inicial (atualmente mostra "Carregando…" placeholder)
- **F-97** — Validação "future-only edit" no frontend (pré-bloqueio + tooltip explicando)
- **F-98** — Drag-and-drop pra reordenar materiais dentro de uma campanha (UX nice-to-have)
- **F-99** — Atalho "Aplicar regra a todas as emissoras com este material" (bulk action no rule editor)
