import { useState } from 'react'
import { useSuggestions } from '../../api/hooks'
import { StatusPill, TypePill, ReqPriorityPill } from './SuggestionPills'
import SuggestionCreateModal from './SuggestionCreateModal'
import SuggestionDetail from './SuggestionDetail'
import { STATUS, STATUS_ORDER } from './constants'
import { timeAgo } from './utils'

// Visão do admin comum: só as próprias sugestões. Cria, acompanha, conversa.
export default function MinhasSugestoes() {
  const [statusFilter, setStatusFilter] = useState('')
  const [creating, setCreating] = useState(false)
  const [openId, setOpenId] = useState(null)

  const q = useSuggestions(statusFilter ? { status: statusFilter } : {})
  const items = q.data?.items || []

  return (
    <div className="sug-page sug-page--author">
      <header className="sug-header">
        <div className="sug-header-text">
          <h1 className="sug-h1">Sugestões</h1>
          <p className="sug-sub">Mande o que você quer ver na plataforma. Acompanhe cada uma até virar realidade.</p>
        </div>
        <button className="btn btn-primary" onClick={() => setCreating(true)}>+ Nova sugestão</button>
      </header>

      <div className="sug-filterbar">
        <button className={`sug-filter-chip${!statusFilter ? ' is-active' : ''}`} onClick={() => setStatusFilter('')}>Todas</button>
        {STATUS_ORDER.map((st) => (
          <button key={st} className={`sug-filter-chip${statusFilter === st ? ' is-active' : ''}`}
                  onClick={() => setStatusFilter(st)}>{STATUS[st].label}</button>
        ))}
      </div>

      {q.isLoading && <div className="sug-loading">Carregando…</div>}

      {!q.isLoading && items.length === 0 && (
        <div className="sug-empty">
          <div className="sug-empty-emoji" aria-hidden="true">💡</div>
          <h3>{statusFilter ? 'Nada por aqui neste filtro' : 'Sua primeira ideia começa aqui'}</h3>
          <p>Achou algo que dava pra melhorar? Faltou uma tela? Conta pra gente — com print e tudo.</p>
          {!statusFilter && <button className="btn btn-primary" onClick={() => setCreating(true)}>+ Nova sugestão</button>}
        </div>
      )}

      <ul className="sug-cardlist">
        {items.map((s) => (
          <li key={s.id}>
            <button className={`sug-card${s.unread ? ' is-unread' : ''}`} onClick={() => setOpenId(s.id)}>
              {s.unread && <span className="sug-card-unread" aria-label="Novidade" />}
              <div className="sug-card-top">
                <span className="sug-ref">#{s.ref_num}</span>
                <TypePill value={s.type} size="sm" />
                <StatusPill value={s.status} size="sm" />
                {s.awaiting_author && <span className="sug-pill sug-pill--awaiting sug-pill--sm">Sua vez</span>}
              </div>
              <div className="sug-card-title">{s.title}</div>
              {s.description && <div className="sug-card-snippet">{s.description}</div>}
              <div className="sug-card-foot">
                <ReqPriorityPill value={s.requester_priority} size="sm" />
                {s.comment_count > 0 && <span className="sug-card-comments">💬 {s.comment_count}</span>}
                <span className="sug-card-time">{timeAgo(s.updated_at)}</span>
              </div>
            </button>
          </li>
        ))}
      </ul>

      {creating && (
        <SuggestionCreateModal onClose={() => setCreating(false)} onCreated={(s) => setOpenId(s.id)} />
      )}
      {openId && <SuggestionDetail id={openId} isDev={false} onClose={() => setOpenId(null)} />}
    </div>
  )
}
