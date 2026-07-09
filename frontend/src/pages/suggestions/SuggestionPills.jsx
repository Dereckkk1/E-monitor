// Pills de status / tipo / prioridade / esforço. Semânticos e reutilizados
// por lista, board, cards e detalhe. As cores moram no CSS (classes
// sug-pill--<value>) pra manter o JSX limpo e o tema consistente.
import { STATUS, TYPE, REQ_PRIORITY, DEV_PRIORITY, EFFORT } from './constants'

export function StatusPill({ value, size }) {
  const meta = STATUS[value]
  if (!meta) return null
  return (
    <span className={`sug-pill sug-pill--status sug-status--${value}${size === 'sm' ? ' sug-pill--sm' : ''}`}
          title={meta.hint}>
      <span className="sug-pill-dot" aria-hidden="true" />
      {meta.label}
    </span>
  )
}

export function TypePill({ value, size }) {
  const meta = TYPE[value]
  if (!meta) return null
  return (
    <span className={`sug-pill sug-pill--type sug-type--${value}${size === 'sm' ? ' sug-pill--sm' : ''}`}>
      <span className="sug-pill-emoji" aria-hidden="true">{meta.icon}</span>
      {meta.label}
    </span>
  )
}

// Prioridade pedida pelo autor.
export function ReqPriorityPill({ value, size }) {
  const meta = REQ_PRIORITY[value]
  if (!meta) return null
  return (
    <span className={`sug-pill sug-pill--reqprio sug-reqprio--${value}${size === 'sm' ? ' sug-pill--sm' : ''}`}>
      {meta.label}
    </span>
  )
}

// Prioridade real definida pelo dev (só aparece quando setada).
export function DevPriorityPill({ value, size }) {
  const meta = DEV_PRIORITY[value]
  if (!meta) return null
  return (
    <span className={`sug-pill sug-pill--devprio sug-devprio--${value}${size === 'sm' ? ' sug-pill--sm' : ''}`}>
      {meta.label}
    </span>
  )
}

export function EffortPill({ value }) {
  const meta = EFFORT[value]
  if (!meta) return null
  return <span className={`sug-pill sug-pill--effort sug-effort--${value}`}>{meta.label}</span>
}
