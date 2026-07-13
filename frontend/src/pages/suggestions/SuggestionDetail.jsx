import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import { useSuggestion, useUpdateSuggestion, useMarkSuggestionRead } from '../../api/hooks'
import { StatusPill, TypePill, ReqPriorityPill, DevPriorityPill, EffortPill } from './SuggestionPills'
import SuggestionThread from './SuggestionThread'
import AttachmentLightbox from './AttachmentLightbox'
import AttachmentImage from './AttachmentImage'
import { STATUS, STATUS_ORDER, DEV_PRIORITY, DEV_PRIORITY_ORDER, EFFORT, EFFORT_ORDER } from './constants'
import { timeAgo, fmtDateTime, authorLabel, initialOf } from './utils'

// Frase humana de um evento da timeline.
function eventPhrase(ev) {
  switch (ev.event_type) {
    case 'created':          return 'abriu a sugestão'
    case 'status_changed':   return <>moveu para <strong>{STATUS[ev.to_value]?.label || ev.to_value}</strong></>
    case 'priority_changed': return <>definiu prioridade <strong>{DEV_PRIORITY[ev.to_value]?.label || ev.to_value}</strong></>
    case 'feedback_given':   return 'deixou um feedback'
    case 'attachment_added': return 'anexou uma imagem'
    case 'reopened':         return 'reabriu'
    default:                 return ev.event_type
  }
}

export default function SuggestionDetail({ id, isDev, onClose }) {
  const q = useSuggestion(id)
  const updateM = useUpdateSuggestion()
  const markRead = useMarkSuggestionRead()
  const [lightbox, setLightbox] = useState(null) // {list, index}

  // Marca como lida ao abrir (zera a bolinha).
  useEffect(() => { if (id) markRead.mutate(id) }, [id]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape' && lightbox == null) onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose, lightbox])

  const s = q.data
  const attachments = s?.attachments || []
  const rootAtts = useMemo(() => attachments.filter((a) => !a.comment_id), [attachments])
  const attsByComment = useMemo(() => {
    const m = {}
    for (const a of attachments) if (a.comment_id) (m[a.comment_id] ||= []).push(a)
    return m
  }, [attachments])

  function openLightbox(att, list) {
    const arr = list || rootAtts
    const idx = arr.findIndex((x) => x.id === att.id)
    setLightbox({ list: arr, index: idx < 0 ? 0 : idx })
  }

  return createPortal(
    <div className="sug-drawer-backdrop" onClick={onClose}>
      <aside className="sug-drawer" onClick={(e) => e.stopPropagation()} role="dialog" aria-modal="true">
        <button className="sug-drawer-close" onClick={onClose} aria-label="Fechar">×</button>

        {q.isLoading && <div className="sug-drawer-loading">Carregando…</div>}
        {q.isError && <div className="sug-drawer-loading">Não consegui carregar essa sugestão.</div>}

        {s && (
          <div className="sug-drawer-scroll">
            <header className="sug-drawer-header">
              <div className="sug-drawer-badges">
                <span className="sug-ref">#{s.ref_num}</span>
                <TypePill value={s.type} size="sm" />
                <StatusPill value={s.status} size="sm" />
                {s.awaiting_author && <span className="sug-pill sug-pill--awaiting">Aguardando autor</span>}
              </div>
              <h2 className="sug-drawer-title">{s.title}</h2>
              <div className="sug-drawer-byline">
                <span className="sug-mini-avatar" aria-hidden="true">{initialOf(authorLabel(s))}</span>
                <span>{authorLabel(s)}</span>
                <span className="sug-dot-sep">·</span>
                <span title={fmtDateTime(s.created_at)}>{timeAgo(s.created_at)}</span>
              </div>
            </header>

            {/* Painel de gestão do dev */}
            {isDev && (
              <section className="sug-manage">
                <div className="sug-manage-row">
                  <label className="sug-manage-field">
                    <span>Status</span>
                    <select className="input sug-select" value={s.status}
                            onChange={(e) => updateM.mutate({ id: s.id, status: e.target.value })}>
                      {STATUS_ORDER.map((st) => <option key={st} value={st}>{STATUS[st].label}</option>)}
                    </select>
                  </label>
                  <label className="sug-manage-field">
                    <span>Prioridade</span>
                    <select className="input sug-select" value={s.dev_priority || ''}
                            onChange={(e) => updateM.mutate({ id: s.id, dev_priority: e.target.value || null })}>
                      <option value="">—</option>
                      {DEV_PRIORITY_ORDER.map((p) => <option key={p} value={p}>{DEV_PRIORITY[p].label}</option>)}
                    </select>
                  </label>
                  <label className="sug-manage-field">
                    <span>Esforço</span>
                    <select className="input sug-select" value={s.effort || ''}
                            onChange={(e) => updateM.mutate({ id: s.id, effort: e.target.value || null })}>
                      <option value="">—</option>
                      {EFFORT_ORDER.map((ef) => <option key={ef} value={ef}>{EFFORT[ef].label}</option>)}
                    </select>
                  </label>
                  <label className="sug-manage-toggle">
                    <input type="checkbox" checked={!!s.awaiting_author}
                           onChange={(e) => updateM.mutate({ id: s.id, awaiting_author: e.target.checked })} />
                    <span>Aguardando autor</span>
                  </label>
                </div>
                <DevTextField label="Feedback (o autor lê)" placeholder="O que responder pro autor…"
                              initial={s.dev_feedback} onSave={(v) => updateM.mutate({ id: s.id, dev_feedback: v })} />
                <DevTextField label="Notas privadas (só você)" privateNote placeholder="Rascunho técnico, links, TODO…"
                              initial={s.dev_notes} onSave={(v) => updateM.mutate({ id: s.id, dev_notes: v })} />
              </section>
            )}

            {/* Feedback do dev em destaque (pro autor) */}
            {!isDev && s.dev_feedback && (
              <section className="sug-feedback-highlight">
                <div className="sug-feedback-label">Resposta do dev</div>
                <div className="sug-feedback-text">{s.dev_feedback}</div>
              </section>
            )}

            {/* Meta */}
            <section className="sug-meta-grid">
              {s.target_screen && <div className="sug-meta"><span>Tela</span><strong>{s.target_screen}</strong></div>}
              <div className="sug-meta"><span>Prioridade pedida</span><ReqPriorityPill value={s.requester_priority} size="sm" /></div>
              {s.dev_priority && <div className="sug-meta"><span>Prioridade real</span><DevPriorityPill value={s.dev_priority} size="sm" /></div>}
              {s.effort && <div className="sug-meta"><span>Esforço</span><EffortPill value={s.effort} /></div>}
            </section>

            {/* Descrição */}
            <section className="sug-desc-block">
              <h4 className="sug-section-title">Descrição</h4>
              <div className="sug-desc-text">{s.description}</div>
              {!!rootAtts.length && (
                <div className="sug-att-grid">
                  {rootAtts.map((a) => (
                    <button key={a.id} type="button" className="sug-att-thumb sug-att-thumb--lg"
                            onClick={() => openLightbox(a, rootAtts)}>
                      <AttachmentImage att={a} />
                    </button>
                  ))}
                </div>
              )}
            </section>

            {/* Timeline */}
            {!!(s.events && s.events.length) && (
              <section className="sug-timeline">
                <h4 className="sug-section-title">Histórico</h4>
                <ul className="sug-timeline-list">
                  {s.events.map((ev) => (
                    <li key={ev.id} className={`sug-tl-item sug-tl--${ev.event_type}`}>
                      <span className="sug-tl-dot" aria-hidden="true" />
                      <span className="sug-tl-text">
                        <strong>{ev.actor_name || 'Alguém'}</strong> {eventPhrase(ev)}
                      </span>
                      <span className="sug-tl-time" title={fmtDateTime(ev.created_at)}>{timeAgo(ev.created_at)}</span>
                    </li>
                  ))}
                </ul>
              </section>
            )}

            {/* Thread */}
            <SuggestionThread suggestionId={s.id} requesterId={s.created_by}
                              comments={s.comments || []} attachmentsByComment={attsByComment}
                              onOpenImage={(a) => openLightbox(a, attsByComment[a.comment_id] || [])} />
          </div>
        )}
      </aside>

      {lightbox && (
        <AttachmentLightbox attachments={lightbox.list} index={lightbox.index}
                            onClose={() => setLightbox(null)}
                            onIndex={(i) => setLightbox((lb) => ({ ...lb, index: i }))} />
      )}
    </div>,
    document.body,
  )
}

// Campo de texto com salvar-explícito (evita salvar a cada tecla).
function DevTextField({ label, initial, onSave, placeholder, privateNote }) {
  const [v, setV] = useState(initial || '')
  const [saved, setSaved] = useState(false)
  useEffect(() => { setV(initial || '') }, [initial])
  const dirty = v !== (initial || '')
  return (
    <div className={`sug-devtext${privateNote ? ' sug-devtext--private' : ''}`}>
      <div className="sug-devtext-head">
        <span>{label}</span>
        {saved && !dirty && <span className="sug-devtext-saved">salvo ✓</span>}
      </div>
      <textarea className="input sug-textarea" rows={2} value={v} placeholder={placeholder}
                onChange={(e) => { setV(e.target.value); setSaved(false) }} />
      {dirty && (
        <div className="sug-devtext-actions">
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => setV(initial || '')}>Descartar</button>
          <button type="button" className="btn btn-primary btn-sm"
                  onClick={() => { onSave(v.trim()); setSaved(true) }}>Salvar</button>
        </div>
      )}
    </div>
  )
}
