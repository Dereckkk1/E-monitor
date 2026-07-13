import { useState } from 'react'
import { useAddSuggestionComment, useUploadSuggestionAttachment } from '../../api/hooks'
import AttachmentImage from './AttachmentImage'
import ClipboardPasteZone from './ClipboardPasteZone'
import { timeAgo, initialOf } from './utils'

// Thread de conversa da sugestão. Cada comentário sabe se veio do autor ou do
// dev (compara author_id com o created_by da sugestão) pra alinhar/estilizar.
export default function SuggestionThread({ suggestionId, requesterId, comments = [], attachmentsByComment = {}, onOpenImage }) {
  const [body, setBody] = useState('')
  const [files, setFiles] = useState([])
  const [error, setError] = useState('')
  const addM = useAddSuggestionComment()
  const uploadM = useUploadSuggestionAttachment()
  const busy = addM.isPending || uploadM.isPending

  async function submit(e) {
    e.preventDefault()
    if (busy) return
    if (!body.trim() && !files.length) return
    setError('')
    try {
      const c = await addM.mutateAsync({ id: suggestionId, body: body.trim() })
      for (const f of files) {
        await uploadM.mutateAsync({ id: suggestionId, file: f, commentId: c.id })
      }
      setBody(''); setFiles([])
    } catch {
      setError('Não consegui enviar. Tenta de novo.')
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
                  {!fromRequester && <span className="sug-msg-tag">dev</span>}
                  <span className="sug-msg-time">{timeAgo(c.created_at)}</span>
                </div>
                {c.body && <div className="sug-msg-text">{c.body}</div>}
                {!!atts.length && (
                  <div className="sug-msg-atts">
                    {atts.map((a, i) => (
                      <button key={a.id} type="button" className="sug-att-thumb"
                              onClick={() => onOpenImage?.(a)}>
                        <AttachmentImage att={a} />
                      </button>
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
