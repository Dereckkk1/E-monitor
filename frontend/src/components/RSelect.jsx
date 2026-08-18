import ReactSelect, { components as RS } from 'react-select'

const C = {
  action:       '#E81E75',
  actionHover:  '#c4175f',
  actionLight:  '#fce7f3',
  actionShadow: 'rgba(232,30,117,0.12)',
  border:       '#e2e8f0',
  borderHover:  '#cbd5e1',
  surface:      '#ffffff',
  surface2:     '#f1f5f9',
  text:         '#06055B',
  text2:        '#4b5563',
  text3:        '#9ca3af',
  fontBody:     "'Fira Sans Condensed', -apple-system, sans-serif",
}

const buildStyles = (overrides = {}) => ({
  control: (base, state) => ({
    ...base,
    minHeight: 38,
    height: overrides.height ?? 'auto',
    border: `1px solid ${state.isFocused ? C.action : C.border}`,
    borderRadius: 8,
    boxShadow: state.isFocused ? `0 0 0 3px ${C.actionShadow}` : 'none',
    background: C.surface,
    fontFamily: C.fontBody,
    fontSize: 13,
    cursor: 'pointer',
    transition: 'border-color 0.15s, box-shadow 0.15s',
    '&:hover': { borderColor: state.isFocused ? C.action : C.borderHover },
  }),
  valueContainer: (base) => ({
    ...base,
    padding: '2px 10px',
    // No modo compacto o container NÃO quebra linha: é justamente o que evita
    // o controle crescer em altura e empurrar a página. O primeiro chip
    // encolhe com reticências e o "+N" fica sempre visível ao lado dele.
    ...(overrides.compact ? { flexWrap: 'nowrap', overflow: 'hidden' } : null),
  }),
  menu: (base) => ({
    ...base,
    borderRadius: 8,
    border: `1px solid ${C.border}`,
    boxShadow: '0 4px 16px rgba(6,5,91,0.10), 0 1px 4px rgba(0,0,0,0.06)',
    overflow: 'hidden',
    marginTop: 4,
  }),
  menuPortal: (base) => ({
    ...base,
    zIndex: 9999,
  }),
  menuList: (base) => ({
    ...base,
    padding: 4,
  }),
  option: (base, state) => ({
    ...base,
    borderRadius: 6,
    background: state.isSelected
      ? C.action
      : state.isFocused
        ? C.actionLight
        : 'transparent',
    color: state.isSelected ? '#fff' : C.text,
    fontFamily: C.fontBody,
    fontSize: 13,
    fontWeight: state.isSelected ? 600 : 400,
    cursor: 'pointer',
    padding: '7px 10px',
    '&:active': { background: state.isSelected ? C.actionHover : C.actionLight },
  }),
  singleValue: (base) => ({
    ...base,
    color: C.text,
    fontFamily: C.fontBody,
    fontSize: 13,
  }),
  multiValue: (base) => ({
    ...base,
    background: C.actionLight,
    borderRadius: 9999,
    padding: '0 2px',
    ...(overrides.compact ? { minWidth: 0, maxWidth: '100%' } : null),
  }),
  multiValueLabel: (base) => ({
    ...base,
    color: C.action,
    fontFamily: C.fontBody,
    fontSize: 11,
    fontWeight: 600,
    paddingLeft: 8,
  }),
  multiValueRemove: (base) => ({
    ...base,
    color: C.action,
    borderRadius: 9999,
    '&:hover': { background: C.action, color: '#fff' },
  }),
  placeholder: (base) => ({
    ...base,
    color: C.text3,
    fontFamily: C.fontBody,
    fontSize: 13,
  }),
  input: (base) => ({
    ...base,
    color: C.text,
    fontFamily: C.fontBody,
    fontSize: 13,
    margin: 0,
    padding: 0,
  }),
  indicatorSeparator: () => ({ display: 'none' }),
  dropdownIndicator: (base, state) => ({
    ...base,
    color: state.isFocused ? C.action : C.text3,
    padding: '0 10px 0 4px',
    transition: 'color 0.15s, transform 0.2s',
    transform: state.selectProps.menuIsOpen ? 'rotate(180deg)' : 'none',
    '&:hover': { color: C.action },
  }),
  clearIndicator: (base) => ({
    ...base,
    color: C.text3,
    padding: '0 4px',
    '&:hover': { color: C.action },
  }),
  noOptionsMessage: (base) => ({
    ...base,
    color: C.text3,
    fontFamily: C.fontBody,
    fontSize: 13,
  }),
})

// Separador do tooltip do contador "+N".
const NEWLINE = '\n'

// Resumo da seleção múltipla: mantém os primeiros N chips e colapsa o resto
// num contador "+N".
//
// Por que existe: o controle cresce em altura a cada item escolhido, e como ele
// vive dentro da barra de filtros, a barra inteira empurra o conteúdo da página
// pra baixo. Com 10+ materiais selecionados a tela vira uma lista de chips com
// um dashboard escondido embaixo.
//
// O contador leva os nomes restantes no `title` — a informação não some, muda
// de lugar. Quem precisa remover um item específico abre o menu e desmarca
// (por isso `hideSelectedOptions` vai a false junto: sem os chips visíveis, o
// menu passa a ser o único caminho de desmarcar).
function makeCompactMultiValue(visible) {
  return function CompactMultiValue(props) {
    const { index, getValue } = props
    const all = getValue() || []
    if (index < visible) return <RS.MultiValue {...props} />
    if (index > visible) return null
    const rest = all.slice(visible)
    return (
      <div
        title={rest.map(o => o.label).join(NEWLINE)}
        style={{
          display: 'inline-flex',
          alignItems: 'center',
          margin: 2,
          padding: '2px 9px',
          borderRadius: 9999,
          background: C.surface2,
          color: C.text2,
          fontFamily: C.fontBody,
          fontSize: 11,
          fontWeight: 700,
          whiteSpace: 'nowrap',
          cursor: 'default',
        }}
      >
        +{rest.length}
      </div>
    )
  }
}

// compactValues: `true` mantém 1 chip visível, número N mantém N. Opt-in — sem
// a prop o RSelect se comporta exatamente como antes em toda tela que já o usa.
export default function RSelect({ styleOverrides, compactValues, components, ...props }) {
  const visible = compactValues === true ? 1 : (Number(compactValues) || 0)
  const compact = visible > 0 && props.isMulti
  return (
    <ReactSelect
      styles={buildStyles({ ...styleOverrides, compact })}
      noOptionsMessage={() => 'Nenhuma opção'}
      menuPortalTarget={typeof document !== 'undefined' ? document.body : null}
      menuPosition="fixed"
      {...(compact ? { hideSelectedOptions: false } : null)}
      {...props}
      components={compact
        ? { MultiValue: makeCompactMultiValue(visible), ...components }
        : components}
    />
  )
}
