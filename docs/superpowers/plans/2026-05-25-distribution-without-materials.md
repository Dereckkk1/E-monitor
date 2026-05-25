# Distribuição sem material — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Permitir que o operador planeje a distribuição de uma campanha (Step 4 do wizard) sem ter materiais linkados ainda, e quando o áudio chegar ele só sobe o arquivo — as regras já existentes começam a contar detections automaticamente.

**Architecture:** Mudança 100% frontend exceto por uma adição mínima ao endpoint de listing de campanhas (campo `material_count`). Aproveita que `distribution_rules.type_id` (migration 0019) já é independente de qualquer material. O Step 3 vira opcional, o Step 4 deriva linhas do grid de `materiais ∪ regras`, e rows "fantasma" (vindas só de regras) ganham visual sutilmente diferente.

**Tech Stack:** React 19 + Vite, React Query v5, Go (pgx) no backend. Sem testes automatizados de frontend nesse repositório (convenção do projeto — ver `docs/features/campaign-wizard.md`); verificação é smoke manual no dev server.

**Spec:** [`docs/superpowers/specs/2026-05-25-distribution-without-materials-design.md`](../specs/2026-05-25-distribution-without-materials-design.md)

---

## Pré-requisitos

Antes de começar, em um terminal **separado** rode o dev server e mantenha aberto durante todo o plano:

```bash
cd frontend && npm run dev
```

URL: `http://localhost:3000`. Login com seu usuário admin de dev. Mantenha aberto também `/campaigns/new` numa aba — você vai voltar pra ela várias vezes.

Se mexer no backend (Task 10), também precisa rodar:

```bash
docker compose -f infra/docker/docker-compose.yml up -d --force-recreate --no-deps api
```

(Sempre `--no-deps` em prod — ver `CLAUDE.md §4.1`. Em dev local pode ser sem.)

---

## Phase 1 — Desbloquear Step 3 (zero materiais OK)

### Task 1: Permitir avançar do Step 3 com 0 materiais, com label dinâmico no footer

**Files:**
- Modify: `frontend/src/pages/CampaignWizardPage.jsx:190` (remove `materialCount === 0` do bloqueio)
- Modify: `frontend/src/pages/CampaignWizardPage.jsx:220` (label dinâmico)

- [ ] **Step 1: Abrir o arquivo e localizar o bloco do Step 3**

Em `frontend/src/pages/CampaignWizardPage.jsx`, achar:

```jsx
  } else if (currentStep === 3) {
    stepContent = (
      <MaterialsStep ... />
    )
    // Migration 0019: distribution is by type, so every linked material MUST
    // have a type_id before advancing — otherwise no rule can cover it.
    const someWithoutType = campaignMaterials.some(cm => {
      const mat = materialsById[cm.material_id]
      return mat && !mat.type_id
    })
    const someWithoutStations = campaignMaterials.some(cm =>
      !cm.target_stations || cm.target_stations.length === 0)
    nextDisabled = materialCount === 0 || someWithoutType || someWithoutStations
```

- [ ] **Step 2: Trocar a condição de `nextDisabled`**

Substituir essa última linha por:

```jsx
    // Distribuição sem material é permitida: o operador pode planejar regras
    // por TIPO no Step 4 antes do áudio chegar (spec 2026-05-25). Só
    // bloqueamos quando há material linkado mas mal configurado.
    nextDisabled = someWithoutType || someWithoutStations
```

- [ ] **Step 3: Tornar o `nextLabel` dinâmico**

Achar a linha:

```jsx
  const nextLabel = currentStep === 5 ? 'Concluir campanha →' : 'Avançar →'
```

Trocar por:

```jsx
  const nextLabel =
    currentStep === 5
      ? 'Concluir campanha →'
      : (currentStep === 3 && materialCount === 0)
        ? 'Pular materiais →'
        : 'Avançar →'
```

- [ ] **Step 4: Verificação manual no navegador**

1. Recarregue `/campaigns/new` no browser
2. Preencha Step 1 (nome, cliente, datas) → "Avançar →"
3. Step 2 — selecione uma rádio → "Avançar →"
4. Step 3 deve carregar com `EmptyState` (nenhum material)
5. ✅ Footer mostra **"Pular materiais →"** em vez de "Avançar →"
6. ✅ Botão **não está disabled** — clique nele
7. ✅ Vai pro Step 4
8. Volte pro Step 3 clicando no nav lateral → ainda vazio → footer continua "Pular materiais →"
9. Agora se você vincular um material no Step 3, o footer vira "Avançar →" automaticamente

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/CampaignWizardPage.jsx
git commit -m "feat(wizard): permitir avançar do Step 3 sem materiais

Step 3 deixa de bloquear quando materialCount === 0; o footer mostra
'Pular materiais →' em vez de 'Avançar →' nesse caso, sinalizando a ação
deliberada. Materiais mal configurados (sem tipo ou estação) continuam
bloqueando."
```

---

### Task 2: Link "Pular por enquanto" dentro do EmptyState do Step 3

**Files:**
- Modify: `frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx` (componente `EmptyState`, função no fim do arquivo)
- Modify: `frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx` (passar nova prop `onSkip` pro EmptyState)
- Modify: `frontend/src/pages/CampaignWizardPage.jsx` (passar `handleNext` como `onSkip` pro MaterialsStep)

- [ ] **Step 1: Adicionar prop `onSkip` ao componente `MaterialsStep`**

Em `MaterialsStep.jsx`, achar a assinatura do componente principal (linha 21):

```jsx
export default function MaterialsStep({ campaignId, clientId, materialsById = {}, campaignStations }) {
```

Trocar por:

```jsx
export default function MaterialsStep({ campaignId, clientId, materialsById = {}, campaignStations, onSkip }) {
```

- [ ] **Step 2: Encaminhar `onSkip` ao `EmptyState`**

No JSX do componente, achar onde `<EmptyState>` é renderizado (linha ~153):

```jsx
      {cmpMats.length === 0 ? (
        <EmptyState onAdd={() => setShowAdd(true)} />
      ) : (
```

Trocar por:

```jsx
      {cmpMats.length === 0 ? (
        <EmptyState onAdd={() => setShowAdd(true)} onSkip={onSkip} />
      ) : (
```

- [ ] **Step 3: Adicionar o link "Pular por enquanto" no `EmptyState`**

Achar a definição de `EmptyState` (linha ~1003):

```jsx
function EmptyState({ onAdd }) {
```

Trocar por:

```jsx
function EmptyState({ onAdd, onSkip }) {
```

Depois, achar o final do componente — o botão "Adicionar primeiro material" termina assim:

```jsx
          Adicionar primeiro material
        </button>
      </div>
    </div>
  )
}
```

Logo ANTES do `</button>` de fechamento, NÃO — adicione DEPOIS do `</button>`, ANTES do primeiro `</div>` que fecha o overlay com `position: absolute`. O resultado final do bloco do overlay deve ficar:

```jsx
        <button
          onClick={onAdd}
          style={{
            marginTop: 8, padding: '10px 18px', borderRadius: 'var(--radius-md)',
            background: 'var(--c-action)', color: '#fff', border: 0,
            cursor: 'pointer', fontSize: 13, fontWeight: 700,
            fontFamily: 'var(--font-heading)',
            display: 'flex', alignItems: 'center', gap: 6,
            boxShadow: 'var(--shadow-md)',
            transition: 'all 150ms cubic-bezier(0.16,1,0.3,1)',
          }}
          onMouseEnter={e => {
            e.currentTarget.style.transform = 'translateY(-1px)'
            e.currentTarget.style.boxShadow = 'var(--shadow-lg)'
          }}
          onMouseLeave={e => {
            e.currentTarget.style.transform = 'translateY(0)'
            e.currentTarget.style.boxShadow = 'var(--shadow-md)'
          }}
        >
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <path d="M8 3v10M3 8h10" />
          </svg>
          Adicionar primeiro material
        </button>
        {onSkip && (
          <button
            type="button"
            onClick={onSkip}
            style={{
              marginTop: 12,
              padding: '6px 12px',
              background: 'transparent', border: 0,
              color: 'var(--c-text-2)',
              fontSize: 12, fontWeight: 500,
              fontFamily: 'var(--font-body)',
              cursor: 'pointer',
              textDecoration: 'underline',
              textUnderlineOffset: 3,
              textDecorationColor: 'var(--c-text-3)',
            }}
            onMouseEnter={e => { e.currentTarget.style.color = 'var(--c-text)' }}
            onMouseLeave={e => { e.currentTarget.style.color = 'var(--c-text-2)' }}
          >
            Pular por enquanto — planejar distribuição sem áudio
          </button>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Conectar `handleNext` ao prop `onSkip` em `CampaignWizardPage`**

Em `CampaignWizardPage.jsx`, achar o bloco do Step 3 (linha ~173):

```jsx
  } else if (currentStep === 3) {
    stepContent = (
      <MaterialsStep
        campaignId={campaignId}
        clientId={draftCampaign.client_id}
        materialsById={materialsById}
        campaignStations={targetStationIds.map(id => allStations.find(s => s.id === id)).filter(Boolean)}
      />
    )
```

Adicionar a prop `onSkip`:

```jsx
  } else if (currentStep === 3) {
    stepContent = (
      <MaterialsStep
        campaignId={campaignId}
        clientId={draftCampaign.client_id}
        materialsById={materialsById}
        campaignStations={targetStationIds.map(id => allStations.find(s => s.id === id)).filter(Boolean)}
        onSkip={handleNext}
      />
    )
```

- [ ] **Step 5: Verificação manual**

1. `/campaigns/new` → Step 1 → Step 2 → Step 3 vazio
2. ✅ Empty state mostra CTA "Adicionar primeiro material" + link sublinhado embaixo "Pular por enquanto — planejar distribuição sem áudio"
3. Clica no link → avança pro Step 4 e marca Step 3 como completo (verifique no nav lateral — Step 3 deve estar com check)
4. Volta pro Step 3 pelo nav → link "Pular" ainda aparece (porque ainda vazio)
5. Adiciona um material via "Adicionar material" → empty state some → link "Pular" some também

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx frontend/src/pages/CampaignWizardPage.jsx
git commit -m "feat(wizard): link 'Pular por enquanto' no empty state do Step 3

Quando não há nenhum material vinculado, o empty state mostra um link
secundário sublinhado pra avançar sem materiais — atalho complementar ao
'Pular materiais →' do footer."
```

---

## Phase 2 — Step 4 RuleSidePanel: tipos e estações independentes de material

### Task 3: `ruleEditorTypes` vem de todos os tipos globais

**Files:**
- Modify: `frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx:285-297` (a `useMemo` `ruleEditorTypes`)

- [ ] **Step 1: Localizar o bloco**

Em `DistributionStep.jsx`, achar (linha ~285):

```jsx
  // Pre-compute lists for the RuleSidePanel — types present in the campaign
  // (a type is "present" when at least one material of that type is linked).
  const ruleEditorTypes = useMemo(() => {
    const seen = new Map()
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      const t = mat?.type_id ? typeById[mat.type_id] : null
      if (!t) continue
      if (!seen.has(t.id)) {
        seen.set(t.id, { id: t.id, name: t.name, color: t.color, materialCount: 0 })
      }
      seen.get(t.id).materialCount += 1
    }
    return [...seen.values()]
  }, [campaignMaterials, materialsById, typeById])
```

- [ ] **Step 2: Trocar pela versão que usa todos os tipos globais**

```jsx
  // Pre-compute lists for the RuleSidePanel — TODOS os tipos globais (não
  // só os presentes na campanha), pra permitir criar regra pra um tipo que
  // ainda não tem material vinculado (spec 2026-05-25). materialCount segue
  // sendo "quantos materiais desse tipo estão linkados na campanha", podendo
  // ser 0.
  const ruleEditorTypes = useMemo(() => {
    const counts = new Map()
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      const tid = mat?.type_id
      if (!tid) continue
      counts.set(tid, (counts.get(tid) ?? 0) + 1)
    }
    return materialTypes.map(t => ({
      id: t.id, name: t.name, color: t.color,
      materialCount: counts.get(t.id) ?? 0,
    }))
  }, [materialTypes, campaignMaterials, materialsById])
```

- [ ] **Step 3: Verificação manual**

1. Numa campanha que tem 0 materiais (criada em Task 1/2): clica em "+ Nova regra"
2. ✅ O panel abre listando **todos os tipos globais** cadastrados em `/material-types` (não só os linkados na campanha)
3. ✅ Cada tipo mostra `materialCount` (0 para tipos sem material vinculado)
4. Numa campanha que tem materiais já: lista mostra todos os tipos, mas os tipos com material aparecem com contagem > 0

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx
git commit -m "feat(wizard): RuleSidePanel lista todos os tipos globais

Permite criar regra por tipo mesmo sem material desse tipo linkado na
campanha — viabiliza planejar distribuição antes de receber os áudios."
```

---

### Task 4: `ruleEditorStations` vem das emissoras da campanha (não de materiais)

**Files:**
- Modify: `frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx:299-305` (a `useMemo` `ruleEditorStations`)
- Modify: `frontend/src/pages/CampaignWizardPage.jsx:191-201` (passar lista de estações da campanha pro `DistributionStep`)

- [ ] **Step 1: Verificar como o `DistributionStep` recebe a info de estações**

Hoje o `DistributionStep` recebe `allStations` (todas as estações do sistema). Pra saber "quais são as da campanha" ele só sabia indiretamente, via materiais (`campaignMaterials[].target_stations`). Vamos passar explicitamente a lista de IDs da campanha.

- [ ] **Step 2: Adicionar prop `campaignStationIds` em `CampaignWizardPage`**

Em `CampaignWizardPage.jsx`, achar o bloco do Step 4 (linha ~191):

```jsx
  } else if (currentStep === 4) {
    stepContent = (
      <DistributionStep
        campaignId={campaignId}
        campaignStart={existingCampaign?.start_date ?? draftCampaign.start_date}
        campaignEnd={existingCampaign?.end_date ?? draftCampaign.end_date}
        campaignMaterials={campaignMaterials}
        materialsById={materialsById}
        allStations={allStations}
      />
    )
```

Trocar por:

```jsx
  } else if (currentStep === 4) {
    stepContent = (
      <DistributionStep
        campaignId={campaignId}
        campaignStart={existingCampaign?.start_date ?? draftCampaign.start_date}
        campaignEnd={existingCampaign?.end_date ?? draftCampaign.end_date}
        campaignMaterials={campaignMaterials}
        campaignStationIds={targetStationIds}
        materialsById={materialsById}
        allStations={allStations}
      />
    )
```

- [ ] **Step 3: Receber a prop em `DistributionStep` e usar no `ruleEditorStations`**

Em `DistributionStep.jsx`, achar a assinatura (linha ~25):

```jsx
export default function DistributionStep({
  campaignId, campaignStart, campaignEnd,
  campaignMaterials, materialsById = {}, allStations,
}) {
```

Trocar por:

```jsx
export default function DistributionStep({
  campaignId, campaignStart, campaignEnd,
  campaignMaterials, campaignStationIds = [], materialsById = {}, allStations,
}) {
```

Depois, achar o `ruleEditorStations` (linha ~299):

```jsx
  const ruleEditorStations = useMemo(() => {
    const stationSet = new Set()
    for (const cm of campaignMaterials) {
      for (const sid of cm.target_stations) stationSet.add(sid)
    }
    return [...stationSet].map(id => allStations.find(s => s.id === id)).filter(Boolean)
  }, [campaignMaterials, allStations])
```

Trocar por:

```jsx
  // Todas as emissoras da campanha (target_stations), independente de
  // material vinculado. Permite criar regra antes de qualquer áudio existir
  // (spec 2026-05-25).
  const ruleEditorStations = useMemo(() => {
    return campaignStationIds
      .map(id => allStations.find(s => s.id === id))
      .filter(Boolean)
  }, [campaignStationIds, allStations])
```

- [ ] **Step 4: Verificação manual**

1. Numa campanha 0-material: clica "+ Nova regra"
2. Escolhe um tipo
3. ✅ Na seção de seleção de emissoras, **todas as emissoras da campanha** aparecem disponíveis (não vazio como antes)
4. Marca algumas, preenche os outros campos, salva
5. ✅ Regra é criada com sucesso (vai aparecer no `RuleChipList`, mas grid ainda vazio — isso é a Task 6)

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx frontend/src/pages/CampaignWizardPage.jsx
git commit -m "feat(wizard): RuleSidePanel lista todas as emissoras da campanha

Antes derivava de campaignMaterials[].target_stations (vazio quando sem
material). Agora usa o target_stations da campanha direto, viabilizando
criar regra antes do upload do material."
```

---

### Task 5: Copy do RuleSidePanel quando `materialCount === 0`

**Files:**
- Modify: `frontend/src/components/RuleSidePanel.jsx` (linha ~211, o trecho "(X material(s) desse tipo na campanha)")

- [ ] **Step 1: Localizar o trecho**

Em `RuleSidePanel.jsx`, achar (próximo da linha 211, dentro do bloco que descreve o que a regra faz quando um tipo foi selecionado):

```jsx
                material do tipo {selectedType.name}</strong> tocar nas emissoras
                selecionadas{selectedType.materialCount != null && ` (${selectedType.materialCount} material${selectedType.materialCount !== 1 ? 'is' : ''} desse tipo na campanha)`}.
```

- [ ] **Step 2: Substituir pelo copy condicional**

```jsx
                material do tipo {selectedType.name}</strong> tocar nas emissoras
                selecionadas{selectedType.materialCount != null && (
                  selectedType.materialCount === 0
                    ? ' (nenhum material desse tipo na campanha ainda — será contado quando subir)'
                    : ` (${selectedType.materialCount} material${selectedType.materialCount !== 1 ? 'is' : ''} desse tipo na campanha)`
                )}.
```

- [ ] **Step 3: Localizar a badge de `materialCount` no card do tipo**

Ainda em `RuleSidePanel.jsx`, achar (próximo da linha 188):

```jsx
                      {t.materialCount != null && (
```

Veja o JSX em volta (linhas 186-198 aproximadamente). Espera-se algo como:

```jsx
                    >
                      {t.name}
                      {t.materialCount != null && (
                        <span style={{
                          ...
                        }}>
                          {t.materialCount}
                        </span>
                      )}
                    </button>
```

- [ ] **Step 4: Garantir que badge `0` não vira label assustadora**

A badge com número 0 funciona — mostra "0" colado no nome do tipo. Visualmente é OK, mas pra deixar mais claro que ainda não é problema, mude o estilo da badge quando o valor é 0 pra ficar mais sutil. Achar o `<span>` da badge e adicionar lógica de cor:

Substituir o bloco do `<span>`:

```jsx
                      {t.materialCount != null && (
                        <span style={{
                          marginLeft: 6,
                          padding: '1px 6px',
                          borderRadius: 'var(--radius-full)',
                          background: t.materialCount === 0
                            ? 'var(--c-surface-2)'
                            : on ? 'rgba(255,255,255,0.25)' : 'var(--c-bg)',
                          color: t.materialCount === 0
                            ? 'var(--c-text-3)'
                            : on ? '#fff' : 'var(--c-text-2)',
                          fontSize: 10, fontWeight: 700,
                          fontFamily: 'var(--font-heading)',
                        }}>
                          {t.materialCount}
                        </span>
                      )}
```

> **Nota:** se o estilo atual da badge não bate exatamente com o snippet acima, **não force** — só ajuste o cálculo de `background` e `color` para incluir o branch de `materialCount === 0`. Mantenha o resto do styling como está no código.

- [ ] **Step 5: Verificação manual**

1. Campanha 0-material → "+ Nova regra"
2. ✅ Tipos no painel mostram badge "0" em cor sutil (cinza claro)
3. Seleciona um tipo
4. ✅ Bloco descritivo mostra: **"nenhum material desse tipo na campanha ainda — será contado quando subir"**
5. Em uma campanha com materiais, badge de tipo com material aparece colorida e o texto descritivo mantém o formato com contagem

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/RuleSidePanel.jsx
git commit -m "feat(rules): copy clara no RuleSidePanel quando tipo sem material

Quando o tipo selecionado tem 0 materiais na campanha, troca o trecho
'(0 materials...)' por 'será contado quando subir' e deixa a badge
visualmente mais sutil pra não parecer um erro."
```

---

## Phase 3 — Grid mostra linhas fantasma vindas de regras + banner

### Task 6: `typesInScopeByStation` inclui tipos vindos de regras + flag `ghost` por row

**Files:**
- Modify: `frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx:72-83` (a `useMemo` `typesInScopeByStation`)
- Modify: `frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx:85-109` (a `useMemo` `rows`)

- [ ] **Step 1: Localizar o bloco `typesInScopeByStation`**

Em `DistributionStep.jsx`, linha ~72:

```jsx
  // Build "rows" — one per (station, TYPE) combination. A row only exists when
  // at least one material of that type is linked to the station; otherwise the
  // type is "unreachable" on that station and showing an empty row would be
  // misleading.
  //
  // typesInScopeByStation: stationId → Set<typeId>
  const typesInScopeByStation = useMemo(() => {
    const m = new Map()
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (!mat?.type_id) continue
      for (const sid of cm.target_stations) {
        if (!m.has(sid)) m.set(sid, new Set())
        m.get(sid).add(mat.type_id)
      }
    }
    return m
  }, [campaignMaterials, materialsById])
```

- [ ] **Step 2: Refatorar pra também receber tipos vindos de regras + marcar quais são fantasma**

Substituir por:

```jsx
  // typesInScopeByStation: stationId → Map<typeId, { hasMaterial: boolean }>
  // União de:
  //   1. (station, type) onde há ao menos um material desse tipo linkado à
  //      estação → hasMaterial = true
  //   2. (station, type) onde há ao menos uma regra cobrindo essa estação +
  //      tipo, mesmo sem material → hasMaterial = false (linha "fantasma")
  // Spec: 2026-05-25-distribution-without-materials-design §4.4
  const typesInScopeByStation = useMemo(() => {
    const m = new Map()
    const add = (sid, tid, hasMaterial) => {
      if (!m.has(sid)) m.set(sid, new Map())
      const station = m.get(sid)
      const existing = station.get(tid)
      // Material sobrescreve "fantasma" — se tem material, deixa de ser ghost.
      if (existing) {
        if (hasMaterial) existing.hasMaterial = true
      } else {
        station.set(tid, { hasMaterial })
      }
    }
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (!mat?.type_id) continue
      for (const sid of cm.target_stations) add(sid, mat.type_id, true)
    }
    for (const r of rules) {
      for (const sid of r.station_ids) add(sid, r.type_id, false)
    }
    return m
  }, [campaignMaterials, materialsById, rules])
```

- [ ] **Step 3: Atualizar a `useMemo` `rows` pra usar a nova estrutura e propagar `ghost`**

Achar o bloco da linha ~85:

```jsx
  const rows = useMemo(() => {
    const r = []
    for (const [sid, typeSet] of typesInScopeByStation.entries()) {
      for (const tid of typeSet) {
        const type = typeById[tid]
        if (!type) continue
        const matching = rules.filter(rule =>
          rule.type_id === tid && rule.station_ids.includes(sid))
        const first = matching[0]
        r.push({
          stationId: sid,
          // Grid was built around `materialId`; we keep the prop name and feed
          // the type's UUID so the grid's cell key stays a string-keyed UUID.
          materialId: tid,
          materialTitle: type.name,
          typeColor: type.color ?? '#94a3b8',
          ruleSummary: first
            ? `${first.plays_per_day}×/dia ${first.time_start}–${first.time_end}`
            : null,
          extraRules: Math.max(0, matching.length - 1),
        })
      }
    }
    return r
  }, [typesInScopeByStation, typeById, rules])
```

Substituir por:

```jsx
  const rows = useMemo(() => {
    const r = []
    for (const [sid, typeMap] of typesInScopeByStation.entries()) {
      for (const [tid, { hasMaterial }] of typeMap.entries()) {
        const type = typeById[tid]
        if (!type) continue
        const matching = rules.filter(rule =>
          rule.type_id === tid && rule.station_ids.includes(sid))
        const first = matching[0]
        r.push({
          stationId: sid,
          // Grid was built around `materialId`; we keep the prop name and feed
          // the type's UUID so the grid's cell key stays a string-keyed UUID.
          materialId: tid,
          materialTitle: type.name,
          typeColor: type.color ?? '#94a3b8',
          ruleSummary: first
            ? `${first.plays_per_day}×/dia ${first.time_start}–${first.time_end}`
            : null,
          extraRules: Math.max(0, matching.length - 1),
          // Linha "fantasma": existe só porque uma regra cobre esse tipo nessa
          // estação, mas ainda não há material desse tipo linkado. Visual
          // diferente no DistributionGrid (Task 7).
          ghost: !hasMaterial,
        })
      }
    }
    return r
  }, [typesInScopeByStation, typeById, rules])
```

- [ ] **Step 4: Verificação manual**

1. Numa campanha 0-material com 1 regra "Spot 30s em rádios A/B/C": volte pro Step 4
2. ✅ Grid mostra 3 linhas (uma por estação), cada uma com sub-linha "Spot 30s"
3. ✅ Visual ainda igual ao normal — só vai diferenciar após Task 7
4. Faça `console.log(rows)` no DevTools para confirmar que tem `ghost: true` em cada row

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx
git commit -m "feat(wizard): grid mostra linhas vindas de regras sem material

typesInScopeByStation passa a unir tipos de materiais linkados + tipos
cobertos por regras. Rows ganham flag 'ghost' quando vêm só de regra,
preparando o estilo diferenciado da Task 7."
```

---

### Task 7: Estilo "fantasma" pras rows ghost no `DistributionGrid`

**Files:**
- Modify: `frontend/src/components/DistributionGrid.jsx` (componente que renderiza cada sub-linha — buscar pelo trecho que usa `row.typeColor` e `row.materialTitle`)

- [ ] **Step 1: Inspecionar o `DistributionGrid` pra identificar onde a row é renderizada**

```bash
grep -n "row.typeColor\|row.materialTitle\|materialTitle" frontend/src/components/DistributionGrid.jsx | head -20
```

Espera achar uma seção (em torno de uma sub-linha do grid) que usa `row.typeColor` (barrinha vertical à esquerda) e `row.materialTitle` (label do tipo). Localizar visualmente esse JSX block.

- [ ] **Step 2: Aplicar estilo condicional baseado em `row.ghost`**

Onde o `row.typeColor` é usado como background (geralmente uma `<div>` que faz a barra vertical da sub-linha), trocar:

```jsx
  background: row.typeColor,
```

por:

```jsx
  background: row.ghost
    ? `color-mix(in srgb, ${row.typeColor} 45%, var(--c-bg))`
    : row.typeColor,
```

Onde `row.materialTitle` é renderizado dentro de um `<span>` ou `<div>` (cor do texto), trocar:

```jsx
  color: 'var(--c-text)',
```

por:

```jsx
  color: row.ghost ? 'var(--c-text-2)' : 'var(--c-text)',
```

E logo depois do texto do nome do tipo, adicionar:

```jsx
{row.ghost && (
  <span style={{
    marginLeft: 6,
    fontSize: 10,
    color: 'var(--c-text-3)',
    fontWeight: 500,
    fontFamily: 'var(--font-body)',
  }}>
    · aguardando áudio
  </span>
)}
```

> **Nota:** O arquivo `DistributionGrid.jsx` é grande. Se encontrar mais de um lugar onde `row.typeColor` é usado (ex: barra vertical, hover indicator), aplique a lógica de fade só na barra vertical principal (geralmente a esquerda mais grossa). Em caso de dúvida, prefira aplicar **só** ao trecho onde a cor é a "tag" do tipo, não ao fundo do hover.

- [ ] **Step 3: Verificação manual**

1. Campanha 0-material com 1 regra → Step 4
2. ✅ Linhas do grid têm barra esquerda em tom mais claro (45% do `typeColor` misturado com background)
3. ✅ Label "Spot 30s" aparece em cor um tom mais sutil (`var(--c-text-2)`)
4. ✅ Sufixo "· aguardando áudio" aparece à direita do nome do tipo, em cor `var(--c-text-3)`
5. Adicione um material desse tipo na Step 3 → volte Step 4 → linhas ficam normais (sem fade, sem sufixo)
6. **Regressão:** uma campanha COM material normal: barra esquerda continua na cor cheia, sem sufixo, sem fade

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/DistributionGrid.jsx
git commit -m "feat(grid): estilo fantasma para linhas sem material

Quando row.ghost === true (vinda só de regra sem material), barra vertical
do tipo fica em 45% de opacidade, label em cor secundária e sufixo
'· aguardando áudio' à direita. Visual sinaliza que a célula ainda não
está sendo monitorada."
```

---

### Task 8: Banner "regras sem áudio" no topo do Step 4

**Files:**
- Modify: `frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx` (adicionar banner no return, abaixo do título + antes do `RuleChipList`)

- [ ] **Step 1: Calcular tipos órfãos (regras sem material correspondente)**

Em `DistributionStep.jsx`, dentro do componente — após o `useMemo` de `rows` e antes do `return` — adicionar:

```jsx
  // Tipos que aparecem em alguma regra mas NÃO têm nenhum material linkado
  // na campanha. Alimenta o banner âmbar do topo (spec §4.3).
  const orphanRuleTypes = useMemo(() => {
    const linkedTypeIds = new Set()
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (mat?.type_id) linkedTypeIds.add(mat.type_id)
    }
    const orphanIds = new Set()
    for (const r of rules) {
      if (!linkedTypeIds.has(r.type_id)) orphanIds.add(r.type_id)
    }
    return [...orphanIds]
      .map(id => typeById[id])
      .filter(Boolean)
  }, [rules, campaignMaterials, materialsById, typeById])
```

- [ ] **Step 2: Renderizar o banner condicionalmente**

No JSX do `DistributionStep`, achar (linha ~374):

```jsx
      {rules.length > 0 && (
        <RuleChipList
          rules={rules}
          typeById={typeById}
          onEdit={openRuleEditor}
        />
      )}
```

Inserir ANTES desse bloco:

```jsx
      {orphanRuleTypes.length > 0 && (
        <div style={{
          padding: '12px 16px',
          background: '#fef9c3',
          border: '1px solid #fde047',
          borderRadius: 'var(--radius-md)',
          display: 'flex',
          alignItems: 'flex-start',
          gap: 12,
        }}>
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none"
               stroke="#a16207" strokeWidth="1.75" strokeLinecap="round"
               strokeLinejoin="round" style={{ flexShrink: 0, marginTop: 1 }}>
            <path d="M8 1.5L1.5 13.5h13L8 1.5z" />
            <path d="M8 6v3.5M8 11.5v.5" />
          </svg>
          <div style={{ fontSize: 13, color: '#854d0e', lineHeight: 1.5 }}>
            Você planejou{' '}
            <strong>
              {rules.filter(r =>
                orphanRuleTypes.some(t => t.id === r.type_id)
              ).length} regra{rules.filter(r =>
                orphanRuleTypes.some(t => t.id === r.type_id)
              ).length !== 1 ? 's' : ''}
            </strong>{' '}
            sem áudio vinculado ainda. Quando subir um material do{' '}
            {orphanRuleTypes.length === 1 ? 'tipo' : 'tipos'}{' '}
            <strong>
              {orphanRuleTypes.map(t => t.name).join(', ')}
            </strong>{' '}
            na campanha, ele começa a ser contado automaticamente nas
            emissoras planejadas.
          </div>
        </div>
      )}
```

- [ ] **Step 3: Verificação manual**

1. Campanha 0-material → Step 4 → cria 1 regra de "Spot 30s" + 1 regra de "Testemunhal"
2. ✅ Banner âmbar aparece logo abaixo do título, listando **"2 regras sem áudio vinculado ainda. Quando subir um material dos tipos Spot 30s, Testemunhal..."**
3. Volta Step 3 → linka um material "Spot 30s" → volta Step 4
4. ✅ Banner agora mostra apenas **"1 regra sem áudio vinculado ainda. Quando subir um material do tipo Testemunhal..."**
5. Linka material "Testemunhal" → volta Step 4
6. ✅ Banner some completamente

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx
git commit -m "feat(wizard): banner âmbar lista regras sem material no Step 4

Mostra ao operador quais tipos ainda estão sem áudio vinculado e tranquiliza
que o monitoramento começa automaticamente quando o material for subido."
```

---

## Phase 4 — Pricing aceita tipos vindos de regras

### Task 9: `typesInScope` do PricingStep inclui tipos cobertos por regras

**Files:**
- Modify: `frontend/src/pages/CampaignWizardPage.jsx:203-216` (passar `distributionRules` como prop pro `PricingStep`)
- Modify: `frontend/src/pages/CampaignWizardSteps/PricingStep.jsx:79-106` (receber prop + expandir `typesInScope`)

- [ ] **Step 1: Passar `distributionRules` pro `PricingStep` em `CampaignWizardPage`**

Em `CampaignWizardPage.jsx`, achar (linha ~203):

```jsx
  } else if (currentStep === 5) {
    const campaignStations = targetStationIds
      .map(id => allStations.find(s => s.id === id))
      .filter(Boolean)
    stepContent = (
      <PricingStep
        ref={pricingRef}
        campaignId={campaignId}
        campaignStations={campaignStations}
        campaignMaterials={campaignMaterials}
        materialsById={materialsById}
      />
    )
```

Trocar por (note: `distributionRules` já é carregado na page no scope existente, linha 48):

```jsx
  } else if (currentStep === 5) {
    const campaignStations = targetStationIds
      .map(id => allStations.find(s => s.id === id))
      .filter(Boolean)
    stepContent = (
      <PricingStep
        ref={pricingRef}
        campaignId={campaignId}
        campaignStations={campaignStations}
        campaignMaterials={campaignMaterials}
        materialsById={materialsById}
        distributionRules={distributionRules}
      />
    )
```

- [ ] **Step 2: Receber `distributionRules` no `PricingStep` e expandir `typesInScope`**

Em `PricingStep.jsx`, achar a assinatura (linha ~79):

```jsx
const PricingStep = forwardRef(function PricingStep({
  campaignId, campaignStations, campaignMaterials, materialsById = {},
}, ref) {
```

Trocar por:

```jsx
const PricingStep = forwardRef(function PricingStep({
  campaignId, campaignStations, campaignMaterials, materialsById = {},
  distributionRules = [],
}, ref) {
```

Achar o `typesInScope` (linha ~89):

```jsx
  // typesInScope[stationId] = Array<MaterialType> presentes na estação dentro
  // da campanha (precisam ter unit_value cadastrado se mode=per_insertion).
  const typesInScope = useMemo(() => {
    const m = new Map()
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (!mat?.type_id) continue
      const type = materialTypes.find(t => t.id === mat.type_id)
      if (!type) continue
      for (const sid of cm.target_stations ?? []) {
        if (!m.has(sid)) m.set(sid, new Map())
        m.get(sid).set(type.id, type)
      }
    }
    const out = {}
    for (const [sid, types] of m.entries()) {
      out[sid] = [...types.values()].sort((a, b) => a.name.localeCompare(b.name))
    }
    return out
  }, [campaignMaterials, materialsById, materialTypes])
```

Trocar por:

```jsx
  // typesInScope[stationId] = Array<MaterialType> presentes na estação dentro
  // da campanha. União de tipos vindos de materiais linkados E tipos cobertos
  // por regras de distribuição (spec 2026-05-25 §4.6). Permite cadastrar
  // unit_value pra um tipo antes mesmo do áudio chegar.
  const typesInScope = useMemo(() => {
    const m = new Map()
    const addType = (sid, type) => {
      if (!m.has(sid)) m.set(sid, new Map())
      m.get(sid).set(type.id, type)
    }
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (!mat?.type_id) continue
      const type = materialTypes.find(t => t.id === mat.type_id)
      if (!type) continue
      for (const sid of cm.target_stations ?? []) addType(sid, type)
    }
    for (const r of distributionRules) {
      const type = materialTypes.find(t => t.id === r.type_id)
      if (!type) continue
      for (const sid of r.station_ids) addType(sid, type)
    }
    const out = {}
    for (const [sid, types] of m.entries()) {
      out[sid] = [...types.values()].sort((a, b) => a.name.localeCompare(b.name))
    }
    return out
  }, [campaignMaterials, materialsById, materialTypes, distributionRules])
```

- [ ] **Step 3: Verificação manual**

1. Campanha 0-material com 1 regra "Spot 30s nas rádios A/B/C" → avança pro Step 5 (Pricing)
2. ✅ Para cada rádio A/B/C, o pricing default é `per_insertion` (porque há tipo no scope agora)
3. ✅ Aparece input pra `unit_value` do tipo "Spot 30s" em cada uma das 3 rádios
4. Preencha valores e clique "Concluir campanha"
5. ✅ Salva sem erro
6. **Regressão:** campanha com material normal continua oferecendo `per_insertion` com o(s) tipo(s) certo(s)

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/CampaignWizardPage.jsx frontend/src/pages/CampaignWizardSteps/PricingStep.jsx
git commit -m "feat(pricing): typesInScope inclui tipos vindos de regras

Operador consegue cadastrar unit_value por tipo antes do áudio chegar,
desde que haja regra cobrindo aquele tipo + estação. Sem isso, a estação
caía em 'consolidado' por falta de tipo no escopo e perdia granularidade
quando o material finalmente chegasse."
```

---

## Phase 5 — Listing /campaigns: chip "sem material"

### Task 10: Backend — adicionar `material_count` no listing de campanhas

**Files:**
- Modify: `workers/internal/catalog/campaigns.go:13-23` (struct `Campaign` — novo campo `MaterialCount`)
- Modify: `workers/internal/catalog/campaigns.go:111-143` (query do `ListPaged` — subquery de count + scan)

- [ ] **Step 1: Adicionar campo `MaterialCount` na struct `Campaign`**

Em `workers/internal/catalog/campaigns.go`, achar (linha 13):

```go
type Campaign struct {
    ID             uuid.UUID   `json:"id"`
    ClientID       uuid.UUID   `json:"client_id"`
    Name           string      `json:"name"`
    StartDate      time.Time   `json:"start_date"`
    EndDate        time.Time   `json:"end_date"`
    Status         string      `json:"status"`
    TargetStations []uuid.UUID `json:"target_stations"`
    CreatedAt      time.Time   `json:"created_at"`
    UpdatedAt      time.Time   `json:"updated_at"`
}
```

Adicionar `MaterialCount` ao final (mantém ordem dos campos antigos pra não quebrar outras queries):

```go
type Campaign struct {
    ID             uuid.UUID   `json:"id"`
    ClientID       uuid.UUID   `json:"client_id"`
    Name           string      `json:"name"`
    StartDate      time.Time   `json:"start_date"`
    EndDate        time.Time   `json:"end_date"`
    Status         string      `json:"status"`
    TargetStations []uuid.UUID `json:"target_stations"`
    CreatedAt      time.Time   `json:"created_at"`
    UpdatedAt      time.Time   `json:"updated_at"`
    // MaterialCount só é populado pelo ListPaged (não pelas outras queries —
    // ficam em zero/nulo). Usado pela UI pra mostrar chip "sem material".
    MaterialCount int `json:"material_count"`
}
```

- [ ] **Step 2: Atualizar a query SELECT do `ListPaged`**

Achar (linha ~112):

```go
    rows, err := c.pool.Query(ctx, `
        SELECT c.id, c.client_id, c.name, c.start_date, c.end_date, c.status, c.target_stations,
               c.created_at, c.updated_at
        FROM campaigns c
        LEFT JOIN clients cl ON cl.id = c.client_id`+where+`
        ORDER BY CASE c.status
```

Trocar o SELECT por:

```go
    rows, err := c.pool.Query(ctx, `
        SELECT c.id, c.client_id, c.name, c.start_date, c.end_date, c.status, c.target_stations,
               c.created_at, c.updated_at,
               COALESCE((
                 SELECT COUNT(*)::int
                 FROM campaign_materials cm
                 WHERE cm.campaign_id = c.id
               ), 0) AS material_count
        FROM campaigns c
        LEFT JOIN clients cl ON cl.id = c.client_id`+where+`
        ORDER BY CASE c.status
```

- [ ] **Step 3: Atualizar o `Scan` do loop de rows**

Achar (linha ~134):

```go
    var out []Campaign
    for rows.Next() {
        var camp Campaign
        if err := rows.Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate,
            &camp.EndDate, &camp.Status, &camp.TargetStations,
            &camp.CreatedAt, &camp.UpdatedAt); err != nil {
            return nil, 0, err
        }
        out = append(out, camp)
    }
```

Trocar por:

```go
    var out []Campaign
    for rows.Next() {
        var camp Campaign
        if err := rows.Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate,
            &camp.EndDate, &camp.Status, &camp.TargetStations,
            &camp.CreatedAt, &camp.UpdatedAt, &camp.MaterialCount); err != nil {
            return nil, 0, err
        }
        out = append(out, camp)
    }
```

- [ ] **Step 4: Verificar se há outros `Scan` que precisariam acompanhar**

```bash
grep -n "rows.Scan\|QueryRow.*Scan" workers/internal/catalog/campaigns.go | head -10
```

> Confirme que **apenas** o scan dentro do `ListPaged` foi alterado. Outros métodos (`Get`, `ListFiltered`, etc.) continuam scaneando os 9 campos antigos — eles deixam `MaterialCount` como zero, o que é correto e seguro.

- [ ] **Step 5: Rebuild + restart da API (dev local)**

```bash
docker compose -f infra/docker/docker-compose.yml build api
docker compose -f infra/docker/docker-compose.yml up -d --force-recreate --no-deps api
docker compose -f infra/docker/docker-compose.yml logs --tail=30 api
```

Esperado: api sobe sem erro, sem ERROR no log.

- [ ] **Step 6: Verificação via curl**

```bash
# pega um JWT (substitua com o seu token de dev)
TOKEN="<seu token JWT>"
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/v1/internal/campaigns?page=1&size=5" | jq '.items[] | {name, material_count}'
```

Esperado: cada item tem o campo `material_count` com um inteiro (`0` pra campanhas sem material, número > 0 pras outras).

- [ ] **Step 7: Commit**

```bash
git add workers/internal/catalog/campaigns.go
git commit -m "feat(campaigns): expor material_count no ListPaged

Subquery COUNT em campaign_materials por campanha. Habilita o chip
'sem material' no listing /campaigns (próxima task)."
```

---

### Task 11: Frontend — chip "sem material" no card de campanha em /campaigns

**Files:**
- Modify: `frontend/src/pages/CampaignsPage.jsx` (componente que renderiza cada linha/card de campanha)

- [ ] **Step 1: Localizar onde cada linha de campanha é renderizada**

```bash
grep -n "campaign\.name\|campaign\.status\|RowSummary\|CampaignRow\|MotionCardRow" frontend/src/pages/CampaignsPage.jsx | head -20
```

Vai listar os pontos onde o JSX renderiza cada campanha. Identifique onde estão os badges/chips de status (geralmente perto do nome da campanha) — o chip "sem material" deve aparecer no mesmo cluster visual.

- [ ] **Step 2: Adicionar chip âmbar próximo ao status**

No componente que renderiza cada linha de campanha, junto aos outros badges (status, "X emissoras", "Y materiais"), adicionar logo depois do status:

```jsx
{campaign.material_count === 0 && (
  <span
    title="Esta campanha não tem nenhum material vinculado ainda — abra o wizard pra subir o áudio quando estiver pronto."
    style={{
      display: 'inline-flex',
      alignItems: 'center',
      gap: 5,
      padding: '2px 8px',
      borderRadius: 'var(--radius-full)',
      background: '#fef9c3',
      color: '#a16207',
      fontSize: 10,
      fontWeight: 700,
      letterSpacing: '0.03em',
      fontFamily: 'var(--font-heading)',
    }}
  >
    <svg width="9" height="9" viewBox="0 0 16 16" fill="none"
         stroke="currentColor" strokeWidth="2" strokeLinecap="round"
         strokeLinejoin="round">
      <path d="M8 1.5L1.5 13.5h13L8 1.5z" />
      <path d="M8 6v3.5M8 11.5v.5" />
    </svg>
    sem material
  </span>
)}
```

> **Nota:** O arquivo `CampaignsPage.jsx` tem ~1300 linhas. Existem possivelmente DUAS visões (lista compacta e cards). Se houver duas, adicione o chip nas duas — mas se uma delas for mobile-only / menos prioritária, pode focar primeiro na visão padrão. Ajuste o estilo (tamanho/spacing) ao contexto local para ficar coerente com os outros chips ali.

- [ ] **Step 3: Verificação manual**

1. Volte pra `/campaigns` no browser → recarregue
2. ✅ Campanha que tem 0 material aparece com chip âmbar "sem material" (com ícone de triângulo de aviso)
3. ✅ Campanha que tem materiais não mostra esse chip
4. Hover no chip mostra o tooltip explicativo
5. Crie campanha nova sem material via wizard → ela aparece na lista com o chip
6. Vai no wizard, sobe um material → volta /campaigns → chip some

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/CampaignsPage.jsx
git commit -m "feat(campaigns): chip 'sem material' no listing

Sinal visual discreto pra operador identificar campanhas que ainda
aguardam upload de áudio. Não bloqueia nenhuma ação — só recall."
```

---

## Phase 6 — Wrap-up: smoke E2E + docs

### Task 12: Smoke test ponta-a-ponta

Sem testes automatizados nesse repositório para o wizard (convenção do projeto). Use este checklist manual para validar a feature inteira antes de mergear.

- [ ] **Step 1: Cenário canônico — campanha planejada antes do áudio**

1. `/campaigns/new` → preencha Step 1 (nome="Teste Sem Material", cliente=qualquer, datas=mês corrente)
2. Step 2 → selecione 3 emissoras
3. Step 3 → ✅ empty state mostra "Pular por enquanto"; footer mostra "Pular materiais →"
4. Clique no link "Pular por enquanto"
5. ✅ Avança pro Step 4, Step 3 fica marcado como completo no nav lateral
6. Step 4 → clique "+ Nova regra"
7. ✅ Painel lista todos os tipos globais (inclusive sem material na campanha)
8. ✅ Painel lista todas as 3 emissoras da campanha
9. Selecione tipo "Spot 30s" (ou qualquer outro), marque as 3 emissoras, plays=3, weekday=todos, faixa 06:00-22:00, dates=período da campanha. Salve.
10. ✅ Aparece RuleChipList com 1 regra
11. ✅ Aparece banner âmbar "Você planejou 1 regra sem áudio vinculado..."
12. ✅ Grid mostra 3 linhas fantasma (uma por emissora), barra esquerda em tom claro + sufixo "· aguardando áudio"
13. Step 5 (Pricing) → ✅ cada emissora oferece per_insertion com o tipo escolhido
14. Preencha valor unitário em cada uma. Salve.
15. Volte pra `/campaigns`
16. ✅ "Teste Sem Material" aparece na lista com chip âmbar "sem material"

- [ ] **Step 2: Cenário "áudio chega depois"**

1. Em `/campaigns`, abra a campanha "Teste Sem Material" no wizard (Editar)
2. Step 3 → suba um material com tipo "Spot 30s" (o mesmo da regra)
3. Material é linkado automaticamente em todas as 3 emissoras (comportamento atual, ver §3 da spec)
4. Avance pra Step 4
5. ✅ Banner âmbar **some**
6. ✅ Linhas do grid não estão mais fantasmas — barra cheia, sem sufixo "aguardando áudio"
7. Volte pra `/campaigns`
8. ✅ Chip âmbar "sem material" some da linha

- [ ] **Step 3: Regressão — campanha "normal" continua igual**

1. Crie campanha "Teste Normal" preenchendo Step 3 com 1 material desde o início
2. ✅ Footer no Step 3 mostra "Avançar →" (não "Pular")
3. ✅ Empty state NÃO aparece (porque já tem material)
4. Continue até finalizar — fluxo do wizard idêntico à versão pré-feature
5. ✅ Em `/campaigns` essa campanha NÃO mostra chip "sem material"

- [ ] **Step 4: Edge case — material desvinculado depois**

1. Em "Teste Normal", entre no wizard novamente
2. Step 3 → desvincule o único material
3. Step 4 → ✅ banner âmbar aparece, linhas viram fantasma
4. `/campaigns` → ✅ chip "sem material" aparece

- [ ] **Step 5: Commit somente se algo precisar de ajuste**

Se algum passo do smoke falhar e exigir correção, fix inline e commit com mensagem descritiva. Caso passe tudo, não precisa commit — siga pra Task 13.

---

### Task 13: Atualizar documentação

**Files:**
- Modify: `docs/features/campaign-wizard.md` (adicionar seção sobre fluxo sem material)
- Modify: `docs/architecture/distribution-rules.md` (nota curta confirmando que rules independem de material)

- [ ] **Step 1: Adicionar seção em `docs/features/campaign-wizard.md`**

Logo após a seção "Edição de campanha existente", adicionar:

```markdown
## Planejamento sem material (distribuição "fantasma")

Campanhas costumam ser planejadas antes dos áudios chegarem. O wizard
suporta esse fluxo:

1. **Step 3 (Materiais) é opcional.** Quando não há material linkado, o
   empty state mostra um link "Pular por enquanto" e o footer renomeia
   "Avançar →" para "Pular materiais →". Materiais mal configurados (sem
   tipo ou sem estação) continuam bloqueando.

2. **Step 4 (Distribuição) aceita regras sem material.** O `RuleSidePanel`
   lista todos os tipos globais e todas as emissoras da campanha. A regra
   é por `type_id` (migration 0019), não exige material.

3. **Linhas "fantasma" no grid.** `(station, type)` que existe só por
   causa de uma regra (sem material desse tipo linkado à estação) aparece
   com barra esquerda em tom claro + sufixo "· aguardando áudio". Quando
   o material chega, a linha vira normal.

4. **Banner âmbar.** O Step 4 mostra um banner listando os tipos sem
   áudio: "Você planejou X regras sem áudio vinculado. Quando subir um
   material do tipo Y, ele começa a ser contado automaticamente."

5. **Pricing aceita tipos vindos de regras.** No Step 5, `typesInScope` é
   união de tipos de materiais E tipos cobertos por regras — operador
   consegue cadastrar `unit_value` por tipo antes do áudio chegar.

6. **Chip no listing.** `/campaigns` mostra chip âmbar "sem material" pra
   campanhas com `material_count === 0`, como recall visual.

Quando o material é subido depois (no Step 3 da edição), ele é vinculado
automaticamente a todas as emissoras da campanha — comportamento atual,
inalterado. As regras já existentes do mesmo tipo começam a contar
detections imediatamente, sem ação extra.
```

- [ ] **Step 2: Atualizar `ultima-verificacao` no header de `campaign-wizard.md`**

Trocar a linha:

```yaml
ultima-verificacao: 2026-05-15
```

por:

```yaml
ultima-verificacao: 2026-05-25
```

- [ ] **Step 3: Adicionar nota em `docs/architecture/distribution-rules.md`**

Logo após a seção "## O que é uma 'regra de distribuição'", adicionar:

```markdown
### Independência de material

Uma regra de distribuição é independente de qualquer material vinculado.
O `type_id` referencia `material_types` (tabela global, não por campanha),
e a view `daily_play_summary` calcula `expected` somando `plays_per_day`
das regras sem precisar de material linkado.

Isso habilita o fluxo "planejar antes do áudio chegar" no wizard — ver
[campaign-wizard.md#planejamento-sem-material-distribuição-fantasma](../features/campaign-wizard.md#planejamento-sem-material-distribuição-fantasma).

Quando o material finalmente é subido na campanha (Step 3 do wizard) com
o `type_id` planejado, as detections daquele material são automaticamente
categorizadas pelas regras existentes — zero ação manual.
```

- [ ] **Step 4: Atualizar `ultima-verificacao` em `distribution-rules.md`**

Trocar:

```yaml
ultima-verificacao: 2026-05-15
```

por:

```yaml
ultima-verificacao: 2026-05-25
```

- [ ] **Step 5: Commit final**

```bash
git add docs/features/campaign-wizard.md docs/architecture/distribution-rules.md
git commit -m "docs: planejamento de campanha sem material

Adiciona seção em campaign-wizard.md documentando o fluxo 'pular Step 3 →
planejar Step 4 → upload depois', e nota em distribution-rules.md
explicitando a independência de regras vs material."
```

---

## Self-Review (post-write)

**Spec coverage:**
- §4.1 Pular button (footer + empty state) → Tasks 1 + 2 ✓
- §4.2 nextDisabled unlock → Task 1 ✓
- §4.3 Banner âmbar → Task 8 ✓
- §4.4 Grid ghost rows → Tasks 6 + 7 ✓
- §4.5 ruleEditorTypes + ruleEditorStations + copy → Tasks 3 + 4 + 5 ✓
- §4.6 Pricing typesInScope ∪ rules → Task 9 ✓
- §4.7 Chip "sem material" → Tasks 10 + 11 ✓
- §5 Cenário canônico → Task 12 (smoke) ✓
- §6 Edge cases — cobertos pelo smoke (Task 12) ✓
- §7 Verificações pré-implementação — endereçadas nas tasks específicas ✓
- §8 Aceitação — coberta pelo smoke (Task 12) ✓
- §9 Arquivos tocados — alinhados com as tasks ✓
- §10 Docs — Task 13 ✓

**Placeholder scan:** Nenhum "TBD" / "TODO" / "implement later". Onde a estrutura exata do código requer inspeção viva (Task 7 e Task 11 dependem de inspecionar `DistributionGrid.jsx` e `CampaignsPage.jsx` em runtime), incluí instruções de localização (`grep`) + notas com fallback claro.

**Type consistency:**
- `ruleEditorTypes` shape: `{ id, name, color, materialCount }` — mesmo nas Tasks 3, 5 (badge), 8 (banner). ✓
- `typesInScopeByStation` mudou de `Map<sid, Set<tid>>` para `Map<sid, Map<tid, {hasMaterial}>>` na Task 6, e a Task seguinte usa o novo formato. ✓
- `row.ghost` adicionado na Task 6, lido na Task 7. ✓
- `MaterialCount` (Go) e `material_count` (JSON) — Task 10 backend, Task 11 frontend. ✓
