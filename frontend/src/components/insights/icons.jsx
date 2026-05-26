// Ícones inline pro dashboard /insights. Seguimos o padrão do resto do
// projeto (sem react-icons) — cada ícone é um <svg> 20px stroke-based,
// usando currentColor pra herdar a cor do container.

const sz = (s = 20) => ({ width: s, height: s, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: 1.7, strokeLinecap: 'round', strokeLinejoin: 'round' })

export function IconChartBars() {
  return (
    <svg {...sz()}>
      <path d="M4 19V11" />
      <path d="M10 19V5" />
      <path d="M16 19V13" />
      <path d="M3 21h18" />
    </svg>
  )
}

export function IconMoney() {
  return (
    <svg {...sz()}>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 6v12" />
      <path d="M15 9.5c-.5-1-1.7-1.5-3-1.5s-3 .8-3 2c0 2.5 6 1.5 6 4 0 1.2-1.5 2-3 2s-2.5-.5-3-1.5" />
    </svg>
  )
}

export function IconGift() {
  return (
    <svg {...sz()}>
      <path d="M3 8h18v4H3z" />
      <path d="M5 12v9h14v-9" />
      <path d="M12 8v13" />
      <path d="M12 8c-2-2-5-2-5 0 0 1 2 1 5 0Z" />
      <path d="M12 8c2-2 5-2 5 0 0 1-2 1-5 0Z" />
    </svg>
  )
}

export function IconWallet() {
  return (
    <svg {...sz()}>
      <path d="M3 7h15a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z" />
      <path d="M3 7V5a2 2 0 0 1 2-2h11" />
      <circle cx="16" cy="13" r="1.2" fill="currentColor" stroke="none" />
    </svg>
  )
}

export function IconPeople() {
  return (
    <svg {...sz()}>
      <circle cx="9" cy="8" r="3.5" />
      <path d="M2 20c0-3.5 3-5.5 7-5.5s7 2 7 5.5" />
      <circle cx="17" cy="9" r="2.5" />
      <path d="M17 14c2.5 0 5 1.5 5 4" />
    </svg>
  )
}

export function IconLock() {
  return (
    <svg {...sz()}>
      <rect x="4" y="11" width="16" height="10" rx="2" />
      <path d="M8 11V8a4 4 0 1 1 8 0v3" />
      <circle cx="12" cy="16" r="1" fill="currentColor" stroke="none" />
    </svg>
  )
}
