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
