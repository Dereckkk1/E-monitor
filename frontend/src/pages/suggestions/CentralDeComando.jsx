import { useMemo, useState } from 'react'
import { useSuggestions, useSuggestionsSummary, useUpdateSuggestion } from '../../api/hooks'
import { StatusPill, TypePill, DevPriorityPill } from './SuggestionPills'
import SuggestionCreateModal from './SuggestionCreateModal'
import SuggestionDetail from './SuggestionDetail'
import EmptyState from './EmptyState'
import { TableSkeleton } from './Skeletons'
import { IconPlus, IconList, IconBoard, IconSearch, IconInbox } from './icons'
import {
  STATUS, STATUS_ORDER, BOARD_COLUMNS, TYPE, TYPE_ORDER,
  DEV_PRIORITY, DEV_PRIORITY_ORDER, devPriorityRank,
} from './constants'
import { timeAgo, fmtDateTime, authorLabel, initialOf } from './utils'

// Leitura secundária de status (label:valor), sem virar card.
function Read({ label, value, tone }) {
  return (
    <div className={`sug-read${tone ? ' sug-read--' + tone : ''}`}>
      <span className="sug-read-value">{value}</span>
      <span className="sug-read-label">{label}</span>
    </div>
  )
}

export default function CentralDeComando() {
  const [view, setView] = useState('list') // 'list' | 'board'
  const [query, setQuery] = useState('')
  const [fStatus, setFStatus] = useState('')
  const [fType, setFType] = useState('')
  const [fPriority, setFPriority] = useState('')
  const [fAuthor, setFAuthor] = useState('')
  const [sort, setSort] = useState('priority')
  const [creating, setCreating] = useState(false)
  const [openId, setOpenId] = useState(null)

  const summaryQ = useSuggestionsSummary()
  const params = {}
  if (fStatus) params.status = fStatus
  if (fType) params.type = fType
  if (fPriority) params.priority = fPriority
  if (fAuthor) params.author_id = fAuthor
  if (query.trim()) params.q = query.trim()
  if (sort) params.sort = sort
  const q = useSuggestions(params)
  const updateM = useUpdateSuggestion()

  const items = q.data?.items || []
  const sm = summaryQ.data || {}
  const counts = sm.by_status || {}

  // Opções de autor derivadas dos resultados.
  const authorOptions = useMemo(() => {
    const seen = new Map()
    for (const s of items) {
      if (s.created_by && !seen.has(s.created_by)) seen.set(s.created_by, authorLabel(s))
    }
    return Array.from(seen, ([value, label]) => ({ value, label }))
  }, [items])

  // Ordenação client-side de reforço (o servidor já ordena; isto garante board).
  const sorted = useMemo(() => {
    const arr = items.slice()
    if (sort === 'priority') {
      arr.sort((a, b) => devPriorityRank(a.dev_priority) - devPriorityRank(b.dev_priority)
        || new Date(b.updated_at) - new Date(a.updated_at))
    } else if (sort === 'oldest') {
      arr.sort((a, b) => new Date(a.created_at) - new Date(b.created_at))
    } else {
      arr.sort((a, b) => new Date(b.updated_at) - new Date(a.updated_at))
    }
    return arr
  }, [items, sort])

  const hasFilters = fStatus || fType || fPriority || fAuthor || query.trim()

  return (
    <div className="sug-page sug-page--dev">
      <header className="sug-header">
        <div className="sug-header-text">
          <h1 className="sug-h1">Central de Sugestões</h1>
          <p className="sug-sub">Toda demanda da plataforma num lugar só. Triage, prioriza, responde — sem sair daqui.</p>
        </div>
        <button className="btn btn-primary sug-newbtn" onClick={() => setCreating(true)}><IconPlus /> Nova demanda</button>
      </header>

      {/* Command strip — primário (inbox) + leituras secundárias */}
      <section className="sug-cmdstrip">
        <button className={`sug-inbox${counts.nova ? ' is-hot' : ''}`}
                onClick={() => setFStatus(fStatus === 'nova' ? '' : 'nova')}
                title="Filtrar novas">
          <span className="sug-inbox-icon"><IconInbox /></span>
          <span className="sug-inbox-num">{counts.nova ?? 0}</span>
          <span className="sug-inbox-label">na caixa<br />p/ triar</span>
        </button>
        <div className="sug-reads">
          <Read label="em progresso" value={counts.em_progresso ?? 0} tone="progress" />
          <Read label="aceitas / backlog" value={counts.aceita ?? 0} />
          <Read label="concluídas no mês" value={sm.resolved_this_month ?? 0} tone="done" />
          <Read label={sm.oldest_open ? 'mais antiga aberta' : 'tudo fresco'}
                value={sm.oldest_open ? timeAgo(sm.oldest_open) : '—'}
                tone={sm.oldest_open ? 'aging' : null} />
        </div>
      </section>

      {/* Toolbar */}
      <div className="sug-toolbar">
        <div className="sug-search">
          <IconSearch />
          <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Buscar título, descrição…" />
        </div>
        <select className="input sug-select" value={fStatus} onChange={(e) => setFStatus(e.target.value)}>
          <option value="">Status: todos</option>
          {STATUS_ORDER.map((st) => <option key={st} value={st}>{STATUS[st].label}</option>)}
        </select>
        <select className="input sug-select" value={fType} onChange={(e) => setFType(e.target.value)}>
          <option value="">Tipo: todos</option>
          {TYPE_ORDER.map((t) => <option key={t} value={t}>{TYPE[t].label}</option>)}
        </select>
        <select className="input sug-select" value={fPriority} onChange={(e) => setFPriority(e.target.value)}>
          <option value="">Prioridade: todas</option>
          {DEV_PRIORITY_ORDER.map((p) => <option key={p} value={p}>{DEV_PRIORITY[p].label}</option>)}
        </select>
        <select className="input sug-select" value={fAuthor} onChange={(e) => setFAuthor(e.target.value)}>
          <option value="">Autor: todos</option>
          {authorOptions.map((a) => <option key={a.value} value={a.value}>{a.label}</option>)}
        </select>
        <select className="input sug-select" value={sort} onChange={(e) => setSort(e.target.value)}>
          <option value="priority">Ordenar: prioridade</option>
          <option value="recent">Ordenar: recentes</option>
          <option value="oldest">Ordenar: mais antigas</option>
        </select>
        <div className="sug-viewtoggle">
          <button className={view === 'list' ? 'is-active' : ''} onClick={() => setView('list')} title="Lista" aria-label="Ver como lista"><IconList /></button>
          <button className={view === 'board' ? 'is-active' : ''} onClick={() => setView('board')} title="Board" aria-label="Ver como board"><IconBoard /></button>
        </div>
      </div>

      {q.isLoading && <TableSkeleton rows={7} />}

      {!q.isLoading && sorted.length === 0 && (
        <EmptyState
          variant={view === 'board' ? 'board' : 'cards'}
          title={hasFilters ? 'Nada bate com esses filtros' : 'Caixa limpa — nada apodrecendo'}
          text={hasFilters
            ? 'Afrouxa um filtro (ou limpa a busca) pra ver mais demandas.'
            : 'Nenhuma demanda em aberto. Quando um admin mandar algo, cai aqui na hora. Você também pode lançar as suas.'}
          ctaLabel={hasFilters ? null : 'Nova demanda'}
          onCta={() => setCreating(true)}
        />
      )}

      {/* LISTA */}
      {view === 'list' && sorted.length > 0 && (
        <div className="sug-table">
          <div className="sug-tr sug-tr--head">
            <span className="sug-th sug-th--flag" />
            <span className="sug-th sug-th--ref">#</span>
            <span className="sug-th sug-th--title">Sugestão</span>
            <span className="sug-th sug-th--author">Autor</span>
            <span className="sug-th sug-th--prio">Prioridade</span>
            <span className="sug-th sug-th--status">Status</span>
            <span className="sug-th sug-th--time">Atualizada</span>
          </div>
          {sorted.map((s) => (
            <div key={s.id} className={`sug-tr${s.unread ? ' is-unread' : ''}`} onClick={() => setOpenId(s.id)}>
              <span className="sug-td sug-td--flag">{s.unread && <span className="sug-card-unread" />}</span>
              <span className="sug-td sug-td--ref">{s.ref_num}</span>
              <span className="sug-td sug-td--title">
                <span className="sug-td-title-main">
                  <TypePill value={s.type} size="sm" />
                  <span className="sug-td-titletext">{s.title}</span>
                </span>
                {s.target_screen && <span className="sug-td-screen">{s.target_screen}</span>}
              </span>
              <span className="sug-td sug-td--author" title={authorLabel(s)}>
                <span className="sug-mini-avatar" aria-hidden="true">{initialOf(authorLabel(s))}</span>
              </span>
              <span className="sug-td sug-td--prio" onClick={(e) => e.stopPropagation()}>
                <select className="sug-inline-select" value={s.dev_priority || ''}
                        onChange={(e) => updateM.mutate({ id: s.id, dev_priority: e.target.value || null })}>
                  <option value="">— definir</option>
                  {DEV_PRIORITY_ORDER.map((p) => <option key={p} value={p}>{DEV_PRIORITY[p].label}</option>)}
                </select>
              </span>
              <span className="sug-td sug-td--status" onClick={(e) => e.stopPropagation()}>
                <select className={`sug-inline-select sug-inline-status sug-status--${s.status}`} value={s.status}
                        onChange={(e) => updateM.mutate({ id: s.id, status: e.target.value })}>
                  {STATUS_ORDER.map((st) => <option key={st} value={st}>{STATUS[st].label}</option>)}
                </select>
              </span>
              <span className="sug-td sug-td--time">{timeAgo(s.updated_at)}</span>
            </div>
          ))}
        </div>
      )}

      {/* BOARD */}
      {view === 'board' && sorted.length > 0 && (
        <div className="sug-board">
          {BOARD_COLUMNS.map((col) => {
            const colItems = sorted.filter((s) => s.status === col)
            return (
              <div key={col} className={`sug-col sug-col--${col}`}>
                <div className="sug-col-head">
                  <StatusPill value={col} size="sm" />
                  <span className="sug-col-count">{colItems.length}</span>
                </div>
                <div className="sug-col-body">
                  {colItems.map((s) => (
                    <button key={s.id} className={`sug-bcard${s.unread ? ' is-unread' : ''}`} onClick={() => setOpenId(s.id)}>
                      {s.unread && <span className="sug-card-unread" />}
                      <div className="sug-bcard-top">
                        <span className="sug-ref">#{s.ref_num}</span>
                        <TypePill value={s.type} size="sm" />
                      </div>
                      <div className="sug-bcard-title">{s.title}</div>
                      <div className="sug-bcard-foot">
                        {s.dev_priority && <DevPriorityPill value={s.dev_priority} size="sm" />}
                        <span className="sug-mini-avatar" aria-hidden="true" title={authorLabel(s)}>{initialOf(authorLabel(s))}</span>
                        <span className="sug-bcard-time">{timeAgo(s.updated_at)}</span>
                      </div>
                    </button>
                  ))}
                  {colItems.length === 0 && <div className="sug-col-empty">vazio</div>}
                </div>
              </div>
            )
          })}
        </div>
      )}

      {creating && <SuggestionCreateModal devMode onClose={() => setCreating(false)} onCreated={(s) => setOpenId(s.id)} />}
      {openId && <SuggestionDetail id={openId} isDev onClose={() => setOpenId(null)} />}
    </div>
  )
}
