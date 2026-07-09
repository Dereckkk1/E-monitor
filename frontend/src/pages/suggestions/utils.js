// Helpers de formatação locais ao módulo de Sugestões.

export function timeAgo(iso) {
  if (!iso) return ''
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return ''
  const s = Math.floor((Date.now() - then) / 1000)
  if (s < 45) return 'agora'
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} min atrás`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} h atrás`
  const d = Math.floor(h / 24)
  if (d < 30) return `${d} d atrás`
  const mo = Math.floor(d / 30)
  if (mo < 12) return `${mo} m atrás`
  return `${Math.floor(mo / 12)} a atrás`
}

export function fmtDateTime(iso) {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString('pt-BR', {
    day: '2-digit', month: '2-digit', year: 'numeric',
    hour: '2-digit', minute: '2-digit',
  })
}

// Inicial pra avatar textual.
export function initialOf(nameOrEmail) {
  const s = (nameOrEmail || '?').trim()
  return (s[0] || '?').toUpperCase()
}

// Nome amigável do autor a partir dos campos que o backend devolve.
export function authorLabel(s) {
  return s?.created_by_name || s?.created_by_email || 'Autor'
}
