// Ícones inline do módulo (SVG, stroke currentColor) — sem dependência nova
// (instalar lib de ícone poda o lockfile no Windows, CLAUDE.md §5). Seguem a
// mesma gramática dos ícones da sidebar: viewBox 16, stroke 1.5, round caps.

const base = {
  viewBox: '0 0 16 16', fill: 'none', stroke: 'currentColor',
  strokeWidth: 1.5, strokeLinecap: 'round', strokeLinejoin: 'round',
}

export function TypeIcon({ type, size = 14 }) {
  const p = { ...base, width: size, height: size, 'aria-hidden': true }
  switch (type) {
    case 'bug':
      return (<svg {...p}><path d="M8 5.5v6" strokeOpacity="0" /><rect x="5" y="5" width="6" height="7" rx="3" /><path d="M8 3.5a2 2 0 0 1 2 2M6 5.5a2 2 0 0 1 2-2" /><path d="M5 7.5H2.5M11 7.5h2.5M5 10H2.8M11 10h2.2M5.4 5.8 3.6 4M10.6 5.8 12.4 4" /></svg>)
    case 'melhoria':
      return (<svg {...p}><path d="M8 2.2l1.3 2.9L12.2 6l-2.9 1.3L8 10.2 6.7 7.3 3.8 6l2.9-.9L8 2.2z" /><path d="M12.5 10.5l.5 1.1 1.1.5-1.1.5-.5 1.1-.5-1.1-1.1-.5 1.1-.5.5-1.1z" strokeOpacity="0.7" /></svg>)
    case 'feature':
      return (<svg {...p}><path d="M8 1.8c2.2 1 3.4 3.1 3.4 5.6 0 1.4-.5 2.7-1.3 3.7H5.9C5.1 10.1 4.6 8.8 4.6 7.4 4.6 4.9 5.8 2.8 8 1.8z" /><circle cx="8" cy="6.4" r="1.2" /><path d="M6 13.4c-.7.6-1 1.4-1 1.4M10 13.4c.7.6 1 1.4 1 1.4" strokeOpacity="0.7" /></svg>)
    case 'duvida':
      return (<svg {...p}><circle cx="8" cy="8" r="6" /><path d="M6.3 6.2c0-1 .8-1.7 1.7-1.7s1.7.6 1.7 1.6c0 1.3-1.6 1.4-1.7 2.6" /><path d="M8 11.2h.01" /></svg>)
    default:
      return null
  }
}

export function IconPlus({ size = 15 }) {
  return (<svg {...base} width={size} height={size} aria-hidden="true"><path d="M8 3.5v9M3.5 8h9" /></svg>)
}
export function IconList({ size = 15 }) {
  return (<svg {...base} width={size} height={size} aria-hidden="true"><path d="M5.5 4h8M5.5 8h8M5.5 12h8M2.5 4h.01M2.5 8h.01M2.5 12h.01" /></svg>)
}
export function IconBoard({ size = 15 }) {
  return (<svg {...base} width={size} height={size} aria-hidden="true"><rect x="2" y="2.5" width="3.5" height="11" rx="1" /><rect x="6.5" y="2.5" width="3.5" height="7.5" rx="1" /><rect x="11" y="2.5" width="3" height="9.5" rx="1" /></svg>)
}
export function IconSearch({ size = 15 }) {
  return (<svg {...base} width={size} height={size} aria-hidden="true"><circle cx="7" cy="7" r="4.2" /><path d="M10.2 10.2 14 14" /></svg>)
}
export function IconInbox({ size = 18 }) {
  return (<svg {...base} width={size} height={size} aria-hidden="true"><path d="M2 9.5 3.6 3.4A1 1 0 0 1 4.6 2.6h6.8a1 1 0 0 1 1 .8L14 9.5V13a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V9.5z" /><path d="M2 9.5h3.2a1 1 0 0 1 1 .8 1.8 1.8 0 0 0 3.6 0 1 1 0 0 1 1-.8H14" /></svg>)
}
