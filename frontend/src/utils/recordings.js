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

/**
 * null = ainda não medida. Não é a mesma coisa que 0%.
 * Gravação parada no meio mede contra o que era possível gravar: a janela
 * acaba no cancelamento (o backend faz o mesmo). Parcial para em 99: a cobertura
 * inclui o pré-roll e o burst do servidor, e passaria de 100% mesmo com buraco.
 */
export function coveragePercent(r) {
  if (r.covered_seconds == null) return null
  const start = new Date(r.starts_at)
  let end = new Date(r.ends_at)
  if (r.canceled_at) {
    const canceled = new Date(r.canceled_at)
    if (canceled < end) end = canceled
  }
  const window = (end - start) / 1000
  if (window <= 0) return null
  return Math.min(r.status === 'parcial' ? 99 : 100, Math.floor((r.covered_seconds / window) * 100))
}

/** Stream oscilando por 6h pode dar centenas de trechos. O e-mail usa o mesmo limite. */
const MAX_GAP_LINES = 20

export function gapLabels(r) {
  const gaps = r.gaps ?? []
  const labels = gaps.slice(0, MAX_GAP_LINES).map(g =>
    `sem áudio das ${gapTimeFmt.format(new Date(g.from))} às ${gapTimeFmt.format(new Date(g.to))}`)
  const rest = gaps.length - labels.length
  return rest > 0 ? [...labels, `e mais ${rest} trechos`] : labels
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

const GENERIC_ERROR = 'Não foi possível concluir. Tente de novo.'
const TECHNICAL_ERRORS = new Set(['forbidden', 'invalid json', 'invalid id', 'internal error'])

/**
 * Texto de erro para o admin. A API escreve mensagens em português só nos 4xx
 * de validação; o resto (HTML de 502/504 do proxy, texto técnico em inglês,
 * erro de rede) vira a mensagem genérica.
 */
export function errorMessage(err) {
  const status = err?.response?.status
  const data = err?.response?.data
  if (typeof status !== 'number' || status < 400 || status >= 500) return GENERIC_ERROR
  if (typeof data !== 'string') return GENERIC_ERROR
  const text = data.trim()
  if (!text || text.length > 200 || text.includes('<') || TECHNICAL_ERRORS.has(text.toLowerCase())) {
    return GENERIC_ERROR
  }
  return text
}
