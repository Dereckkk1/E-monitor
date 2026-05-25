# Distribuição sem material — Spec

**Status:** Aprovado (2026-05-25)
**Autor:** Dereck + Claude (brainstorm)
**Escopo:** Frontend only. Zero migrations.

## 1. Problema

Campanhas costumam ser planejadas semanas antes da veiculação, mas o áudio do
comercial chega em cima da hora. Hoje o wizard de campanha (`/campaigns/new`,
`/campaigns/:id/edit`) força o operador a subir/vincular pelo menos um material
no **Step 3** antes de conseguir avançar para o **Step 4** (Distribuição). Isso
gera trabalho corrido: o operador volta na pressa pra fazer toda a distribuição
quando finalmente recebe os áudios.

**O que queremos:** deixar o operador planejar distribuição (regras de
"3×/dia, seg-sex, 08–10h, nas rádios X/Y/Z, de DD/MM até DD/MM") **sem
nenhum material linkado**, e quando o áudio chegar ele só sobe o arquivo — as
regras já estão lá e começam a contar detections automaticamente.

## 2. Por que dá pra fazer sem schema

A **migration 0019** (2026-05-12) mudou a granularidade de `distribution_rules`
de `material_id` pra `type_id` (FK pra `material_types`). Uma regra hoje diz:

> "Spot 30s = 5×/dia nas rádios A/B/C, seg-sex, 08:00–10:00, de 01/06 a 30/06"

Qualquer material do tipo "Spot 30s" linkado à campanha cumpre essa regra.
A regra **não precisa de material existente** — só do tipo. A `daily_play_summary`
view já agrega `expected` por `(campaign, type, station, date)` sem depender
de material linkado. O lado `actual` (detections) fica zerado até o áudio chegar
e veicular.

**Conclusão:** o modelo de dados já suporta. Os bloqueios são todos no frontend
(`nextDisabled` no Step 3, derivações de `ruleEditorTypes`/`ruleEditorStations`
a partir de `campaignMaterials`).

## 3. Não-objetivos

- **Sem migration.** Tabelas e view existentes ficam intocadas.
- **Sem "modo simplificado" do wizard.** Não vamos esconder Steps. A ordem
  continua 1→2→3→4→5; o que muda é que Step 3 deixa de ser obrigatório.
- **Sem auto-criação de regras** a partir de templates. Operador continua
  criando regra a regra via `RuleSidePanel`.
- **Sem alterar o comportamento de upload de material posterior.** Ao subir
  um material novo na campanha, ele continua sendo linkado em **todas as
  emissoras da campanha** (decisão confirmada pelo Dereck em 2026-05-25 —
  manter UX atual; ajustar emissoras depois clicando no chip do card é OK).

## 4. UX changes

### 4.1. Step 3 — botão "Pular materiais por enquanto"

Aparece em dois lugares:

1. **Dentro do `EmptyState`** (componente em `MaterialsStep.jsx`, função
   `EmptyState`) — abaixo do CTA atual "Adicionar primeiro material",
   adiciona link/ghost-button secundário:

   > "Pular por enquanto — planejar distribuição sem áudio"

   Estilo: text-link, fontSize 12, color `var(--c-text-2)`, sem border,
   margin-top 12. Click chama a mesma `handleNext()` do footer (que invoca
   `markStepCompleteAndAdvance`) — marca Step 3 como concluído e avança
   pro Step 4. Não é equivalente a clicar no nav lateral (que NÃO marcaria
   como completo).

2. **No footer do wizard** (`WizardLayout.jsx`) — quando `materialCount === 0`,
   o botão principal "Avançar →" troca o label pra **"Pular materiais →"**
   (visual idêntico, copy diferente). Continua sendo um botão primário —
   não é descartado, é só renomeado, porque o operador entrou em "Pular"
   conscientemente via Step 3 inteiro vazio. Quando `materialCount > 0`,
   volta a ser "Avançar →" normal.

### 4.2. Step 3 — desbloqueio do `nextDisabled`

`CampaignWizardPage.jsx` linha 190, mudar:

```js
// Antes
nextDisabled = materialCount === 0 || someWithoutType || someWithoutStations
// Depois
nextDisabled = someWithoutType || someWithoutStations
```

Significado: zero materiais é OK; materiais mal configurados (sem tipo ou
sem estação) continuam bloqueando.

### 4.3. Step 4 — banner de "regras sem áudio"

No topo de `DistributionStep`, ABAIXO do título e ACIMA do `RuleChipList`
(quando aplicável), aparece banner âmbar discreto se há ≥1 regra cujo `type_id`
não tem material linkado na campanha:

> ⚠ Você planejou **N regra(s)** sem áudio vinculado ainda. Quando subir um
> material dos tipos **Spot 30s, Testemunhal** na campanha, ele começa a ser
> contado automaticamente.

Tipos listados são os distintos `rule.type_id` que NÃO aparecem em
`campaignMaterials`. Banner some quando todos os tipos cobertos por regras
têm material correspondente.

Estilo: igual ao counter bar do Step 3 quando há materiais sem tipo
(background `#fef9c3`, border `#fde047`, padding `12px 16px`, fontSize 13).

### 4.4. Step 4 — grid mostra "linhas fantasma"

`DistributionGrid` recebe linhas geradas pela união:

```
typesInScopeByStation = {}
for cm in campaignMaterials:
    if cm.mat.type_id: add (sid, type_id) for sid in cm.target_stations
for rule in rules:
    add (sid, rule.type_id) for sid in rule.station_ids
```

Cada `(stationId, typeId)` que veio APENAS via regra (sem material linkado
desse tipo na estação) é marcado como `ghost: true` no objeto de row.

`DistributionGrid` renderiza essas rows com:
- Barra colorida esquerda em opacidade reduzida:
  `color-mix(in srgb, ${typeColor} 45%, var(--c-bg))` (vs `${typeColor}` cheio)
- Texto da sub-linha (nome do tipo) em `color: var(--c-text-2)` em vez de
  `var(--c-text)`
- Sufixo discreto após o nome do tipo: `· aguardando áudio` em
  `fontSize: 10, color: var(--c-text-3)`

O resto da linha (células × dia, +/-, click) funciona idêntico — é só o
visual que sinaliza que ainda não tem material.

### 4.5. Step 4 — RuleSidePanel: types e stations vêm de fonte nova

`DistributionStep.jsx`:

- **`ruleEditorTypes`** (linha 285) passa a derivar de `useMaterialTypes()`
  (todos os tipos globais). Cada entrada continua tendo `{ id, name, color,
  materialCount }` onde `materialCount` é "quantos materiais desse tipo já
  estão linkados na campanha" (pode ser 0).

- **`ruleEditorStations`** (linha 299) passa a derivar de
  `allStations.filter(s ∈ campaign.target_stations)` — todas as emissoras
  da campanha, independente de material.

`RuleSidePanel` em si quase não muda. Único ajuste de copy: o trecho
"(${selectedType.materialCount} material${...} desse tipo na campanha)"
na linha ~211 — quando `materialCount === 0`, mostrar
**"(nenhum material desse tipo ainda — será contado quando subir)"**
em vez de "(0 materials desse tipo)".

### 4.6. Step 5 — Pricing: tipos vêm de materiais ∪ regras

`PricingStep.jsx` linha 89, `typesInScope` é uma `Map<stationId, Map<typeId,
type>>`. Adicionar pass extra após o loop de `campaignMaterials`:

```js
for (const r of distributionRules) {
  const type = materialTypes.find(t => t.id === r.type_id)
  if (!type) continue
  for (const sid of r.station_ids) {
    if (!m.has(sid)) m.set(sid, new Map())
    m.get(sid).set(type.id, type)
  }
}
```

Requer passar `distributionRules` como prop pro `PricingStep`
(`CampaignWizardPage.jsx` linha 207 — já tem `distributionRules` carregado
no scope da page).

Default mode na hidratação (linha 129) continua igual: `per_insertion` se
há tipos (agora incluindo tipos de regras), `consolidated` se não.

### 4.7. /campaigns — chip "sem material"

`CampaignsPage.jsx` — no card/linha de cada campanha, quando
`campaign.material_count === 0` (campo já existe na response do listing —
**verificar antes de implementar**, ver §7.1), adiciona chip âmbar discreto:

> ⚠ sem material

Estilo: padding `2px 8px`, border-radius `var(--radius-full)`,
background `#fef9c3`, color `#a16207`, fontSize 10, fontWeight 700.

Não bloqueia nada. Não filtra. É só recall visual.

## 5. Comportamento "feliz" end-to-end (cenário canônico)

1. Operador entra em `/campaigns/new`.
2. **Step 1:** preenche nome, cliente, datas. Avança.
3. **Step 2:** seleciona emissoras. Avança.
4. **Step 3:** não tem áudio ainda. Clica em **"Pular por enquanto"**
   (ou no footer "Pular materiais →"). Vai pro Step 4.
5. **Step 4:** clica "+ Nova regra". `RuleSidePanel` abre com todos os
   tipos globais e todas as emissoras da campanha. Cria regra
   "Spot 30s = 3×/dia, seg-sex, 06–10h, nas rádios A/B/C, 01/06 a 30/06".
   Banner âmbar aparece: "1 regra sem áudio vinculado". Grid mostra 3 linhas
   fantasma (uma por rádio) com a sub-linha "Spot 30s · aguardando áudio"
   em cor sutil. Cria mais regras como quiser.
6. **Step 5:** pricing. Para A/B/C aparece o tipo "Spot 30s" no per_insertion.
   Cadastra valor unitário ou usa consolidado. Avança.
7. Clica "Concluir campanha". Volta pra `/campaigns`. Card da campanha
   exibe chip "sem material".
8. **Dias depois**, áudio chega. Operador entra em `/campaigns/:id/edit`,
   vai pro Step 3, sobe o arquivo via upload (ou linka da biblioteca),
   marca tipo "Spot 30s". Material é linkado em todas as emissoras da
   campanha (comportamento atual).
9. Grid no Step 4: linhas que eram fantasma viram normais (opacidade cheia,
   sem "aguardando áudio"). Banner âmbar some. Chip "sem material" no
   `/campaigns` some. Detections novas começam a ser categorizadas
   normalmente pela view `daily_play_summary`.

## 6. Edge cases

### 6.1. Regra sobra após material ser desvinculado

Operador linka material "Spot 30s", cria regra "Spot 30s = 5×/dia",
depois desvincula o material. Regra permanece. Sistema entra em estado
"regra sem áudio" automaticamente — banner reaparece, linha vira fantasma.
Sem ação especial requerida.

### 6.2. Material existe mas não tem tipo

Material linkado sem `type_id` definido continua bloqueando o `nextDisabled`
do Step 3 (`someWithoutType`). Nada muda nesse front — é regra existente
e correta.

### 6.3. Override em célula fantasma

Operador cria override (ex: "12 plays no dia 15/06") numa célula cujo
tipo ainda não tem material. Permitido — `distribution_overrides.type_id`
é igualmente independente de material. Quando o áudio chegar, o
`expected` daquela célula = valor do override (já é assim hoje).

### 6.4. Pricing per_insertion sem material e sem regra

Estação sem material linkado E sem regra de nenhum tipo cobrindo ela.
Fica forçada em `consolidated` (já é o comportamento atual via fallback
linha 51 do `PricingStep`). Não muda nada.

### 6.5. Campanha em modo edit, material já existia

Não regredimos: campanha que já tinha material continua exibindo Step 3
normal sem o botão "Pular" (porque o `EmptyState` não renderiza com
`cmpMats.length > 0`). Footer mostra "Avançar →" porque
`materialCount > 0`. Banner do Step 4 some.

### 6.6. Material chega com tipo diferente do planejado

Operador planejou regras de "Spot 30s" mas sobe um material de "Testemunhal".
O Spot 30s continua sem áudio (banner permanece). Testemunhal não tem regra
(grid linha normal, mas sem regra cobrindo → tudo orphan). Cabe ao operador
ajustar — não é uma falha do sistema. **Não vamos** alertar sobre tipos
incompatíveis automaticamente.

## 7. Detalhes técnicos / verificações antes de implementar

### 7.1. Verificar se `campaign.material_count` existe no listing

Para o chip "sem material" em `/campaigns`. Se não existir, alternativas
em ordem de preferência:
1. Adicionar ao SELECT do handler de listing (mais limpo).
2. Computar client-side a partir de `useCampaignMaterials` por linha
   (mais N requests).

Verificar `workers/internal/api/handlers/campaigns.go` ou similar.

### 7.2. `WizardLayout` precisa aceitar `nextLabel` dinâmico

Já aceita (`CampaignWizardPage.jsx` linha 220 passa `nextLabel`). Só
precisamos garantir que a página computa o label correto:

```js
const nextLabel = currentStep === 5
  ? 'Concluir campanha →'
  : (currentStep === 3 && materialCount === 0)
    ? 'Pular materiais →'
    : 'Avançar →'
```

### 7.3. `distributionRules` precisa fluir pro `PricingStep`

`CampaignWizardPage.jsx` já tem `distributionRules` no scope (linha 48).
Adicionar à prop list do `PricingStep` (atualmente recebe `campaignId`,
`campaignStations`, `campaignMaterials`, `materialsById`).

### 7.4. `DistributionGrid` precisa suportar prop `ghost` por row

Verificar se `rows` aceita campo extra livre. Se sim, basta adicionar
`ghost: true` em `DistributionStep.jsx` na construção de rows e ler no
`DistributionGrid` pra ajustar estilo.

## 8. Aceitação

Feature é considerada pronta quando:

1. Campanha nova em `/campaigns/new` permite avançar do Step 3 com 0
   materiais via botão "Pular".
2. Step 4 permite criar regra escolhendo qualquer `material_type` global e
   qualquer subset das emissoras da campanha, mesmo sem material linkado.
3. Grid renderiza linha fantasma com visual sutilmente diferente para
   `(station, type)` que vem só de regra.
4. Banner âmbar no Step 4 lista tipos sem material.
5. Pricing Step 5 oferece per_insertion para tipos cobertos por regras.
6. Chip "sem material" aparece na lista `/campaigns` quando aplicável.
7. Upload posterior de material com tipo já planejado faz as linhas
   saírem do estado fantasma e o banner desaparecer.
8. Edit de campanha existente com materiais continua igual — sem
   regressão visual no fluxo normal.

## 9. Arquivos tocados

- `frontend/src/pages/CampaignWizardPage.jsx`
- `frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx`
- `frontend/src/pages/CampaignWizardSteps/DistributionStep.jsx`
- `frontend/src/pages/CampaignWizardSteps/PricingStep.jsx`
- `frontend/src/pages/CampaignsPage.jsx`
- `frontend/src/components/DistributionGrid.jsx`
- (Possivelmente) `workers/internal/api/handlers/campaigns.go` — se o listing
  não retorna `material_count`.

## 10. Documentação a atualizar

Após implementar:

- `docs/features/campaign-wizard.md` — adicionar seção "Distribuição sem
  material" descrevendo o fluxo "pular Step 3 → planejar → upload depois".
- `docs/architecture/distribution-rules.md` — nota curta confirmando que
  rules independem de material vinculado (já é semanticamente verdade,
  mas vale explicitar).
