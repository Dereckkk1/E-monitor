import ReactSelect from 'react-select'

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

export default function RSelect({ styleOverrides, ...props }) {
  return (
    <ReactSelect
      styles={buildStyles(styleOverrides)}
      noOptionsMessage={() => 'Nenhuma opção'}
      menuPortalTarget={typeof document !== 'undefined' ? document.body : null}
      menuPosition="fixed"
      {...props}
    />
  )
}
