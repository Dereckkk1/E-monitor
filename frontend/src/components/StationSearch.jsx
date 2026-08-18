import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { useStationSuggest } from '../api/hooks'
import { tokenize, highlightSegments, dialMatchesToken } from '../utils/search'
import StationAvatar from './StationAvatar'

// Combobox de busca de emissoras.
//
// Espelha o SearchSuggest do /marketplace do E-radios: grupos com cabeçalho,
// trecho casado em <mark>, navegação por seta, skeleton no loading. Não usa
// react-select de propósito — o RSelect serve pra escolher de uma lista
// fechada, e aqui a lista é heterogênea (emissora, cidade, UF) e o Enter sem
// seleção precisa valer como busca livre.
//
// A query é multi-token (`Jb 99.9 RJ`), então o highlight marca cada token
// separadamente e o dial acende inteiro quando um token numérico casa com ele —
// tentar substring contra "99,9" já formatado nunca bateria.

const MIN_CHARS = 2
const DEBOUNCE_MS = 250

function SearchIcon(props) {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.1" strokeLinecap="round" aria-hidden {...props}>
      <circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3" />
    </svg>
  )
}
function RadioIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M4.9 16.1a8 8 0 0 1 0-8.2M7.8 13.2a4 4 0 0 1 0-2.4M19.1 7.9a8 8 0 0 1 0 8.2M16.2 10.8a4 4 0 0 1 0 2.4" /><circle cx="12" cy="12" r="1.5" />
    </svg>
  )
}
function PinIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M20 10c0 6-8 12-8 12s-8-6-8-12a8 8 0 0 1 16 0Z" /><circle cx="12" cy="10" r="3" />
    </svg>
  )
}
function MapIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="m9 4-6 2v14l6-2 6 2 6-2V4l-6 2Z" /><path d="M9 4v14M15 6v14" />
    </svg>
  )
}
function XIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden>
      <path d="M18 6 6 18M6 6l12 12" />
    </svg>
  )
}

// Dial em português: FM com vírgula decimal, AM inteiro.
export function formatDial(freqMhz, band) {
  if (freqMhz == null) return band ?? ''
  const n = Number(freqMhz)
  const txt = Number.isInteger(n)
    ? String(n)
    : n.toLocaleString('pt-BR', { minimumFractionDigits: 1, maximumFractionDigits: 2 })
  return band ? `${txt} ${band}` : txt
}

function Highlighted({ text, tokens }) {
  return highlightSegments(text, tokens).map((seg, i) =>
    seg.hit
      ? <mark key={i} className="ssg-hit">{seg.text}</mark>
      : <span key={i}>{seg.text}</span>,
  )
}

const GROUP_LABEL = { stations: 'Emissoras', cities: 'Cidades', states: 'Estado' }
const GROUP_ICON = { stations: RadioIcon, cities: PinIcon, states: MapIcon }

// Rótulo curto da seleção aplicada — é o que fica no campo depois do clique.
export function selectionLabel(sel) {
  if (!sel) return ''
  if (sel.kind === 'station') return sel.station?.name ?? ''
  if (sel.kind === 'city') return sel.state ? `${sel.city}/${sel.state}` : sel.city
  if (sel.kind === 'state') return sel.state
  return sel.q ?? ''
}

export default function StationSearch({
  value,
  onChange,
  band = '',
  placeholder = 'Buscar por nome, dial, cidade ou UF…',
}) {
  const uid = useId()
  const [text, setText] = useState(() => selectionLabel(value))
  const [debounced, setDebounced] = useState('')
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(-1)
  const rootRef = useRef(null)

  // Sincroniza com a limpeza vinda de fora (um "limpar filtros" da página).
  // Ajuste durante o render, não em efeito: é o padrão da própria doc do React
  // pra derivar estado de uma prop que mudou, e evita o render extra.
  const [prevValue, setPrevValue] = useState(value)
  if (value !== prevValue) {
    setPrevValue(value)
    if (!value) setText('')
  }

  useEffect(() => {
    const t = setTimeout(() => setDebounced(text), DEBOUNCE_MS)
    return () => clearTimeout(t)
  }, [text])

  const q = debounced.trim()
  // Sem tratamento de erro de propósito: se o endpoint falhar (404 do backend
  // anterior ao deploy, por exemplo), `data` fica indefinido e a lista cai na
  // linha de busca livre — que usa o /stations, esse sim sempre presente. O
  // `retry: false` do hook impede insistir num endpoint que não existe.
  const { data, isFetching } = useStationSuggest({ q, band, enabled: open })

  useEffect(() => {
    const onDoc = e => { if (!rootRef.current?.contains(e.target)) setOpen(false) }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [])

  const tokens = useMemo(() => tokenize(q), [q])

  // Lista plana na ordem de renderização — é o índice dela que as setas
  // percorrem.
  const items = useMemo(() => {
    const list = []
    for (const st of data?.stations ?? []) {
      list.push({ kind: 'station', group: 'stations', key: `s-${st.id}`, id: st.id, station: st })
    }
    for (const c of data?.cities ?? []) {
      list.push({ kind: 'city', group: 'cities', key: `c-${c.city}-${c.state ?? ''}`, city: c.city, state: c.state ?? null, count: c.count })
    }
    for (const s of data?.states ?? []) {
      list.push({ kind: 'state', group: 'states', key: `u-${s.state}`, state: s.state, count: s.count })
    }
    // Sempre por último e sempre presente: é a saída quando nada casa e o
    // caminho único quando o suggest está fora.
    if (q.length >= MIN_CHARS) list.push({ kind: 'text', group: 'free', key: 'free', q })
    return list
  }, [data, q])

  const showDropdown = open && q.length >= MIN_CHARS
  // Clampa em vez de zerar num efeito: a lista encolhe quando o resultado novo
  // chega, e um índice velho apontaria pra fora dela.
  const activeItem = active >= 0 && active < items.length ? items[active] : null
  const optId = item => `${uid}-opt-${item.key}`

  function pick(item) {
    if (!item) return
    setText(selectionLabel(item))
    setOpen(false)
    setActive(-1)
    onChange?.(item)
  }

  function clear() {
    setText('')
    setDebounced('')
    setOpen(false)
    setActive(-1)
    onChange?.(null)
  }

  function onKeyDown(e) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setOpen(true)
      setActive(a => Math.min(a + 1, items.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActive(a => Math.max(a - 1, 0))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      // Sem item ativo, Enter vale como busca livre pelo texto cru — antes
      // disso, digitar e dar Enter não filtrava absolutamente nada.
      pick(activeItem ?? (q.length >= MIN_CHARS ? { kind: 'text', q } : null))
    } else if (e.key === 'Escape') {
      setOpen(false)
      setActive(-1)
    }
  }

  function renderOption(item, i) {
    const isActive = i === active
    const common = {
      key: item.key,
      id: optId(item),
      type: 'button',
      role: 'option',
      'aria-selected': isActive,
      className: `ssg-option${isActive ? ' ssg-option-active' : ''}`,
      onMouseEnter: () => setActive(i),
      onClick: () => pick(item),
    }

    if (item.kind === 'station') {
      const st = item.station
      const dialHit = tokens.some(t => dialMatchesToken(st.frequency_mhz, t))
      const loc = [st.city, st.state].filter(Boolean).join('/')
      return (
        <button {...common}>
          <StationAvatar station={st} size={28} />
          <span className="ssg-option-body">
            <span className="ssg-option-title"><Highlighted text={st.name} tokens={tokens} /></span>
            <span className="ssg-option-sub">
              <span className={dialHit ? 'ssg-hit' : undefined}>{formatDial(st.frequency_mhz, st.band)}</span>
              {loc ? <> · <Highlighted text={loc} tokens={tokens} /></> : null}
            </span>
          </span>
        </button>
      )
    }

    if (item.kind === 'city') {
      return (
        <button {...common}>
          <span className="ssg-option-icon"><PinIcon /></span>
          <span className="ssg-option-body">
            <span className="ssg-option-title">
              <Highlighted text={item.city} tokens={tokens} />
              {item.state ? <span className="ssg-option-dim">/{item.state}</span> : null}
            </span>
          </span>
          <small className="ssg-option-meta">{item.count} emissora{item.count === 1 ? '' : 's'}</small>
        </button>
      )
    }

    if (item.kind === 'state') {
      return (
        <button {...common}>
          <span className="ssg-option-icon"><MapIcon /></span>
          <span className="ssg-option-body">
            <span className="ssg-option-title">Todas de <Highlighted text={item.state} tokens={tokens} /></span>
          </span>
          <small className="ssg-option-meta">{item.count} emissora{item.count === 1 ? '' : 's'}</small>
        </button>
      )
    }

    return (
      <button {...common}>
        <span className="ssg-option-icon"><SearchIcon width="13" height="13" /></span>
        <span className="ssg-option-body">
          <span className="ssg-option-title">Buscar <strong>“{item.q}”</strong> em tudo</span>
        </span>
      </button>
    )
  }

  const groupsInOrder = ['stations', 'cities', 'states']
  // `!data` e não `items.length === 0`: a linha de busca livre está sempre
  // presente, então contar itens deixaria o skeleton inalcançável.
  const loading = isFetching && !data

  return (
    <div className={`ssg${showDropdown ? ' ssg-open' : ''}`} ref={rootRef}>
      <span className="ssg-lead"><SearchIcon /></span>
      <input
        className="input ssg-input"
        type="text"
        role="combobox"
        aria-expanded={showDropdown}
        aria-controls={`${uid}-listbox`}
        aria-activedescendant={activeItem ? optId(activeItem) : undefined}
        aria-autocomplete="list"
        aria-label="Buscar emissora"
        placeholder={placeholder}
        value={text}
        onChange={e => { setText(e.target.value); setOpen(true); setActive(-1) }}
        onFocus={() => setOpen(true)}
        onKeyDown={onKeyDown}
      />
      {(text.length > 0 || value) && (
        <button type="button" className="ssg-clear" aria-label="Limpar busca" onClick={clear}>
          <XIcon />
        </button>
      )}

      {showDropdown && (
        <div className="ssg-menu" id={`${uid}-listbox`} role="listbox">
          {loading && (
            <div className="ssg-skeleton" aria-hidden>
              {[0, 1, 2].map(i => (
                <div className="ssg-skel-row" key={i}>
                  <span className="skeleton ssg-skel-avatar" />
                  <span className="ssg-skel-lines">
                    <span className="skeleton ssg-skel-line" />
                    <span className="skeleton ssg-skel-line ssg-skel-line-short" />
                  </span>
                </div>
              ))}
            </div>
          )}

          {groupsInOrder.map(group => {
            const inGroup = items.filter(it => it.group === group)
            if (inGroup.length === 0) return null
            const Icon = GROUP_ICON[group]
            return (
              <div className="ssg-group" role="group" aria-label={GROUP_LABEL[group]} key={group}>
                <div className="ssg-group-head"><Icon />{GROUP_LABEL[group]}</div>
                {inGroup.map(item => renderOption(item, items.indexOf(item)))}
              </div>
            )
          })}

          {items.some(it => it.group === 'free') && (
            <div className="ssg-free">
              {renderOption(items[items.length - 1], items.length - 1)}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
