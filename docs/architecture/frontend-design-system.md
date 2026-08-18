---
status: implementado
ultima-verificacao: 2026-08-18
codigo-relacionado:
  - frontend/src/index.css
  - frontend/src/components/RSelect.jsx
  - frontend/src/components/FlowEmptyState.jsx
  - frontend/src/components/FlowStepper.jsx
  - frontend/src/App.css
---

# Frontend — Design System de Controles de Formulário

Documenta os padrões de botões, inputs e selects do Radiocheck. Qualquer novo elemento de formulário deve seguir essas convenções.

---

## Tokens relevantes (index.css :root)

| Token | Valor | Uso |
|---|---|---|
| `--c-action` | `#E81E75` | Rosa Digital — cor primária de ação |
| `--c-action-hover` | `#c4175f` | Hover do botão primário |
| `--c-action-light` | `#fce7f3` | Fundo de opção hover no RSelect |
| `--c-border` | `#e2e8f0` | Borda padrão de inputs e botões |
| `--c-surface` | `#ffffff` | Fundo de inputs e botões secundários |
| `--c-surface-2` | `#f1f5f9` | Hover de botão secundário / disabled |
| `--c-text` | `#06055B` | Texto principal |
| `--c-text-2` | `#4b5563` | Labels e texto de suporte |
| `--c-text-3` | `#9ca3af` | Placeholder e texto muted |
| `--c-label` | `#6b7280` | Label de campo — o único cinza de rótulo que passa no WCAG AA (4,65:1 sobre `--c-bg`; `--c-text-3` dá 2,5:1) |
| `--radius-md` | `8px` | Border-radius padrão de inputs e botões |
| `--font-body` | `Fira Sans Condensed` | Fonte de inputs, labels e botões |

---

## Botões

### Classes disponíveis

Todo botão precisa da classe base `btn` mais uma variante:

```jsx
<button className="btn btn-primary">Salvar</button>
<button className="btn btn-secondary">Cancelar</button>
<button className="btn btn-primary btn-sm">Ação pequena</button>
```

| Classe | Aparência | Hover |
|---|---|---|
| `btn-primary` | Rosa `#E81E75`, texto branco | `translateY(-1px)` + sombra rosa difusa |
| `btn-secondary` | Fundo branco, borda `--c-border` | Cinza suave + `translateY(-1px)` |
| `btn-success` | Verde, texto branco | `brightness(1.08)` |
| `btn-danger` | Vermelho, texto branco | `brightness(1.08)` |
| `btn-muted` | Cinza claro, texto `--c-text-2` | Cinza mais escuro |
| `btn-sm` | Modificador de tamanho (padding menor) | — |

**Regra**: nunca use `btn-primary` ou `btn-secondary` sem o `btn` base — os estilos de padding, border-radius e font-family estão em `.btn`.

### Botão ícone

Para botões de ação com apenas ícone SVG (ex: editar, deletar):

```jsx
<button className="btn-icon" title="Editar">
  <EditIcon />
</button>
```

30×30px, border-radius 8px, borda `--c-border`, hover cinza suave.

---

## Inputs e Textareas

Sempre use a classe `.input`:

```jsx
<input className="input" type="text" placeholder="..." />
<input className="input" type="number" min="0" />
<input className="input" type="date" />
<textarea className="input" rows={4} />
```

**Focus**: borda muda para `--c-action` + aura `box-shadow: 0 0 0 3px rgba(232,30,117,0.1)`.

Nunca use `outline` nativo — já está removido via `appearance: none` + focus customizado.

---

## Select — RSelect

**Todo select do projeto usa o componente `RSelect`** em `src/components/RSelect.jsx`. Nunca use `<select>` nativo.

```jsx
import RSelect from '../components/RSelect'

// Select simples (sem search)
<RSelect
  options={[{ value: 'FM', label: 'FM' }, { value: 'AM', label: 'AM' }]}
  value={options.find(o => o.value === form.band) ?? null}
  onChange={opt => setField('band', opt?.value ?? '')}
  isSearchable={false}
/>

// Select com search (lista longa)
<RSelect
  options={clients.map(c => ({ value: c.id, label: c.name }))}
  value={selectedOption}
  onChange={opt => setField('client_id', opt?.value ?? '')}
  placeholder="Selecione um cliente..."
  isClearable
/>
```

### Props principais

| Prop | Tipo | Descrição |
|---|---|---|
| `options` | `{ value, label }[]` | Lista de opções |
| `value` | `{ value, label } \| null` | Opção selecionada (objeto, não string) |
| `onChange` | `(opt) => void` | Recebe o objeto opção (ou `null` se `isClearable`) |
| `isSearchable` | `boolean` | `false` para selects curtos (ex: FM/AM). Default `true` |
| `isClearable` | `boolean` | Mostra botão de limpar seleção |
| `isDisabled` | `boolean` | Desabilita o controle |
| `isLoading` | `boolean` | Mostra indicador de loading |
| `placeholder` | `string` | Texto quando nenhuma opção está selecionada |

### Armadilha comum: `value` deve ser objeto, não string

```jsx
// ERRADO — passa string
<RSelect value={form.band} />

// CERTO — passa o objeto encontrado na lista
<RSelect value={options.find(o => o.value === form.band) ?? null} />
```

### Tema aplicado automaticamente

O `RSelect` aplica o tema do design system internamente (não precisa de CSS externo):
- Borda e focus ring rosa `#E81E75`
- Opção selecionada: fundo rosa sólido, texto branco
- Opção hover: fundo `#fce7f3` (rosa translúcido)
- Seta animada ao abrir
- Sem separador nativo entre valor e seta
- Mensagem de vazio: "Nenhuma opção"

---

## Field — agrupamento de label + controle

Use `.field` para empacotar label e qualquer controle:

```jsx
<div className="field">
  <label>Nome da emissora *</label>
  <input className="input" value={...} onChange={...} required />
</div>

<div className="field">
  <label>Cliente *</label>
  <RSelect options={...} value={...} onChange={...} />
</div>
```

Label estilizado automaticamente: Fira Sans Condensed, 11px, uppercase, semi-bold, `--c-text-2`.

Para hint/instrução abaixo do controle:

```jsx
<div className="field">
  <label>URL do logotipo</label>
  <input className="input" ... />
  <span className="field-hint">Caminho no bucket GCS</span>
</div>
```

---

## Filtros pill (band tabs / period pills)

Para grupos de filtro estilo tab/pill:

```jsx
{['', 'FM', 'AM'].map(b => (
  <button
    key={b || 'all'}
    className={`band-tab${band === b ? ' active' : ''}`}
    onClick={() => setBand(b)}
  >
    {b || 'Todas'}
  </button>
))}
```

Ativo: fundo `--c-action` (rosa), texto branco. Inativo: transparente, hover cinza.


---

## Barra de filtros em passos (`.flow-filters`)

**É a barra de recorte padrão de toda tela que filtra dados.** Nasceu em
`/detections` e hoje vale para `/detections`, `/materials`, `/campaigns`,
`/reports/airtime`, `/insights`, `/management`, `/live-map` e
`/admin/pos-venda`. Tela nova com filtro usa esta barra — não invente um card
de filtros.

### Anatomia

- **Sem card.** A barra vive direto sobre o fundo da página (`--c-bg`). Nada de
  superfície branca com borda e sombra em volta: o card rouba o alinhamento do
  primeiro campo com o título da página e engorda a tela sem informar nada.
- **Label** `.flow-filter-label`: 10,5px, 700, caixa alta, `--c-label`.
- **Badge numerada** `.flow-filter-label-step`: 16px, redonda, dentro do label.
- **Controle** de **38px** de altura: `.flow-input` (month/date/search),
  `.flow-range` (par de datas com seta) ou `RSelect`.

```jsx
<div className="flow-filters flow-filters--auto minha-flow">
  <div className={`flow-filter ${valor ? 'flow-filter--done' : 'flow-filter--active'}`}>
    <label className="flow-filter-label" htmlFor="x">
      <span className="flow-filter-label-step">1</span>
      Cliente
    </label>
    <RSelect inputId="x" … />
  </div>
</div>
```

### Os dois tipos de passo

| Tipo | Quando | Classes | Badge |
|---|---|---|---|
| **Encadeado** | O filtro destrava o próximo (o backend não tem o que devolver sem ele) | `--locked` → `--active` → `--done` | vazia → rosa cheia → rosa clara |
| **Opcional** | Nunca bloqueia; só estreita o recorte | `--optional`, ganha `--done` quando preenchido | anel vazado → rosa clara |

Passo opcional leva `<span className="flow-filter-tag">opcional</span>` colado
ao rótulo. O anel vazado é o sinal silencioso; a palavra é a confirmação.

`--optional` e `--locked` **podem** conviver: é o caso de um refino cujas
opções derivam de um passo anterior (Emissoras no `/insights` só existe depois
das campanhas). O que `--optional` garante não é que o campo esteja sempre
disponível — é que ele **nunca impede o usuário de chegar ao resultado**.

### Meta e atalhos

`.flow-filter-hint` (alinhado à direita do label) carrega contagem
(`4 disponíveis`, `1 de 2`) ou um atalho — `Resetar`, `Limpar`,
`Período completo` — combinado com `.flow-range-reset`.

### Colunas

Barras de 3 passos usam a grade default. Qualquer outro número usa
`.flow-filters--auto` e declara as colunas em `--flow-cols`, dosando pelo que
cada campo carrega (nome de campanha é longo, status é curto):

```css
.minha-flow { --flow-cols: minmax(170px, 210px) minmax(240px, 1fr) 250px minmax(150px, 190px); }
```

O colapso responsivo é do sistema: 2 colunas ≤1180px, 1 coluna ≤720px.

**Grid não quebra sozinho — ele estoura.** A soma dos mínimos das colunas
declaradas tem que caber na área de conteúdo; se não couber, a barra transborda
a largura da página em vez de passar pra linha de baixo. Com 5+ passos isso
acontece antes do breakpoint de 1180px, então declare um degrau intermediário
com menos colunas (o `/insights` cai pra 3 colunas ≤1500px, deixando os dois
passos opcionais na segunda linha — ver `.in-flow` em `InsightsPage.css`).

---

## Estado vazio das telas com filtro (`FlowEmptyState`)

Toda tela com `.flow-filters` usa o mesmo shell de vazio: **silhueta desbotada
do resultado ao fundo + cartão centrado**. A silhueta é o que separa "ensinar a
tela" de "nada aqui" — o usuário reconhece o formato do que vai receber antes
de escolher qualquer filtro.

```jsx
<FlowEmptyState
  className="minha-empty detection-empty--veil"
  step={2} steps={['Cliente', 'Campanha']}
  icon={<IconeQualquer />}
  tone="action"            // 'action' | 'warn' | 'mute'
  title={<>Escolha uma <strong>campanha</strong></>}
  description="…"
  actions={<button className="detection-empty-cta">…</button>}
  ghost={<SilhuetaDaTela />}
/>
```

- **`step`/`steps`** desenham o `FlowStepper` (pílulas numeradas). Só liste os
  passos **encadeados** — passo opcional não é etapa de um caminho.
  Sem cadeia (ex.: `/management`), não passe `step`.
- **`ghost`** é a silhueta. Reaproveite os componentes reais com dados vazios
  (mapa, canvas) ou blocos de skeleton na proporção do layout.
- **`detection-empty--veil`** adiciona um véu radial. Use quando a silhueta tem
  massa no miolo (mapa, blocos grandes) e o cartão perde leitura; silhueta de
  linhas finas dispensa.
- Silhueta feita só de blocos claros some no `opacity: .22` do shell — suba
  para `.6` na classe da página.

As classes CSS mantêm o prefixo `detection-*` por serem as originais de
`/detections`; o bloco no `index.css` está marcado como compartilhado.
`/detections` e `/materials` montam esse DOM à mão (equivalente), o resto usa o
componente.
