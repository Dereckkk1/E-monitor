// Formatação da tela /admin/gravacoes. Tudo em horário de São Paulo: é o que o
// admin digitou e o que a rádio usa, independente do fuso do navegador.
const TZ = 'America/Sao_Paulo'

const dayFmt = new Intl.DateTimeFormat('pt-BR', { timeZone: TZ, day: '2-digit', month: '2-digit', year: 'numeric' })
const timeFmt = new Intl.DateTimeFormat('pt-BR', { timeZone: TZ, hour: '2-digit', minute: '2-digit' })
// Só para buracos: um de 40s pode caber dentro do mesmo minuto.
const gapTimeFmt = new Intl.DateTimeFormat('pt-BR', { timeZone: TZ, hour: '2-digit', minute: '2-digit', second: '2-digit' })

const STATUS = {
  programada: { label: 'Programada', badge: 'badge-planned' },
  gravando: { label: 'Gravando', badge: 'badge-active' },
  processando: { label: 'Processando', badge: 'badge-planned' },
  concluida: { label: 'Concluída', badge: 'badge-active' },
  parcial: { label: 'Parcial', badge: 'badge-paused' },
  falhou: { label: 'Falhou', badge: 'badge-error' },
  cancelada: { label: 'Cancelada', badge: 'badge-ended' },
  expirada: { label: 'Expirada', badge: 'badge-ended' },
}

const LIVE = new Set(['programada', 'gravando', 'processando'])

export function statusInfo(status) {
  return STATUS[status] ?? { label: status, badge: 'badge-ended' }
}

export function isLive(items) {
  return (items ?? []).some(r => LIVE.has(r.status))
}

export function windowLabel(r) {
  const from = new Date(r.starts_at)
  return `${dayFmt.format(from)}, ${timeFmt.format(from)} às ${timeFmt.format(new Date(r.ends_at))}`
}

/** null = ainda não medida. Não é a mesma coisa que 0%. */
export function coveragePercent(r) {
  if (r.covered_seconds == null) return null
  const window = (new Date(r.ends_at) - new Date(r.starts_at)) / 1000
  if (window <= 0) return null
  return Math.min(100, Math.round((r.covered_seconds / window) * 100))
}

export function gapLabels(r) {
  return (r.gaps ?? []).map(g =>
    `sem áudio das ${gapTimeFmt.format(new Date(g.from))} às ${gapTimeFmt.format(new Date(g.to))}`)
}

export function expiresLabel(r) {
  if (!r.has_audio || !r.expires_at) return '—'
  return dayFmt.format(new Date(r.expires_at))
}

/** AAAA-MM-DD, o formato do input type=date. */
export function todayInSaoPaulo(now = new Date()) {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: TZ, year: 'numeric', month: '2-digit', day: '2-digit' })
  return parts.format(now)
}

/** O nome é montado aqui porque o arquivo chega como blob, sem o header da API. */
export function downloadName(r) {
  const slug = r.station_name
    .normalize('NFD').replace(/\p{Diacritic}/gu, '')
    .toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
  const from = new Date(r.starts_at)
  const [hh, mm] = timeFmt.format(from).split(':')
  return `gravacao-${slug}-${todayInSaoPaulo(from)}-${hh}h${mm}.m4a`
}
