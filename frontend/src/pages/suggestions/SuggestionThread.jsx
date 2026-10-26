import { useRef, useState } from 'react'
import { useAddSuggestionComment, useStageSuggestionAttachment } from '../../api/hooks'
import AttachmentItem from './AttachmentItem'
import ClipboardPasteZone from './ClipboardPasteZone'
import { timeAgo, initialOf } from './utils'

// Thread de conversa da sugestão. Cada comentário sabe se veio do autor ou do
// dev (compara author_id com o created_by da sugestão) pra alinhar/estilizar.
export default function SuggestionThread({ suggestionId, requesterId, comments = [], attachmentsByComment = {}, onOpenImage }) {
  const [body, setBody] = useState('')
  const [files, setFiles] = useState([])
  const [error, setError] = useState('')
  const addM = useAddSuggestionComment()
  const stageM = useStageSuggestionAttachment()
  const busy = addM.isPending || stageM.isPending
  const [clientKey, setClientKey] = useState(() => crypto.randomUUID())
  const staged = useRef(new Map())

  async function submit(e) {
    e.preventDefault()
    if (busy) return
    if (!body.trim() && !files.length) return
    setError('')
    try {
      const ids = []
      for (const f of files) {
        if (!staged.current.has(f)) staged.current.set(f, (await stageM.mutateAsync(f)).id)
        ids.push(staged.current.get(f))
      }
      await addM.mutateAsync({ id: suggestionId, body: body.trim(), attachmentIds: ids, clientKey })
      setBody(''); setFiles([])
      staged.current = new Map()
      setClientKey(crypto.randomUUID()) // a próxima fala é outro envio
    } catch (err) {
      setError(err?.response?.data?.error || 'Não consegui enviar. Tenta de novo.')
    }
  }

  return (
    <div className="sug-thread">
      <h4 className="sug-section-title">Conversa</h4>

      {comments.length === 0 && (
        <p className="sug-thread-empty">Sem mensagens ainda. Puxe o assunto aqui.</p>
      )}

      <ul className="sug-thread-list">
        {comments.map((c) => {
          const fromRequester = c.author_id && requesterId && c.author_id === requesterId
          const name = c.author_name || c.author_email || (fromRequester ? 'Autor' : 'Dev')
          const atts = attachmentsByComment[c.id] || []
          return (
            <li key={c.id} className={`sug-msg${fromRequester ? ' sug-msg--author' : ' sug-msg--dev'}`}>
              <div className="sug-msg-avatar" aria-hidden="true">{initialOf(name)}</div>
              <div className="sug-msg-body">
                <div className="sug-msg-meta">
                  <span className="sug-msg-name">{name}</span>
                  {(c.from_dev ?? !fromRequester) && <span className="sug-msg-tag">dev</span>}
                  <span className="sug-msg-time">{timeAgo(c.created_at)}</span>
                </div>
                {c.body && <div className="sug-msg-text">{c.body}</div>}
                {!!atts.length && (
                  <div className="sug-msg-atts">
                    {atts.map((a) => (
                      <AttachmentItem key={a.id} att={a} onOpenImage={onOpenImage} />
                    ))}
                  </div>
                )}
              </div>
            </li>
          )
        })}
      </ul>

      <form className="sug-reply" onSubmit={submit}>
        <textarea className="input sug-reply-input" rows={2} value={body}
                  onChange={(e) => setBody(e.target.value)} disabled={busy}
                  placeholder="Escreva uma resposta…" />
        <ClipboardPasteZone files={files} onChange={setFiles} max={4} hint="Cole/arraste um print" />
        {error && <div className="sug-modal-error" role="alert">{error}</div>}
        <div className="sug-reply-actions">
          <button type="submit" className="btn btn-primary btn-sm" disabled={busy || (!body.trim() && !files.length)}>
            {busy ? 'Enviando…' : 'Responder'}
          </button>
        </div>
      </form>
    </div>
  )
}
