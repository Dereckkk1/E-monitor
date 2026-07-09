import { useState, useEffect } from 'react'
import { createPortal } from 'react-dom'
import RSelect from '../../components/RSelect'
import { useCreateSuggestion, useUploadSuggestionAttachment } from '../../api/hooks'
import ClipboardPasteZone from './ClipboardPasteZone'
import { TYPE, TYPE_ORDER, REQ_PRIORITY, REQ_PRIORITY_ORDER, TARGET_SCREENS } from './constants'

// Modal de criação. Serve ao autor (nova sugestão) e ao dev (nova demanda).
// Fluxo de submit: cria a sugestão → sobe os anexos referenciando o id novo →
// fecha e devolve a sugestão criada pro chamador (que abre o detalhe).
export default function SuggestionCreateModal({ onClose, onCreated, devMode }) {
  const [title, setTitle] = useState('')
  const [type, setType] = useState('melhoria')
  const [targetScreen, setTargetScreen] = useState(null)
  const [description, setDescription] = useState('')
  const [priority, setPriority] = useState('media')
  const [files, setFiles] = useState([])
  const [error, setError] = useState('')

  const createM = useCreateSuggestion()
  const uploadM = useUploadSuggestionAttachment()
  const busy = createM.isPending || uploadM.isPending

  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape' && !busy) onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [busy, onClose])

  async function handleSubmit(e) {
    e.preventDefault()
    if (busy) return
    setError('')
    if (!title.trim() || !description.trim()) {
      setError('Título e descrição são obrigatórios.')
      return
    }
    try {
      const created = await createM.mutateAsync({
        title: title.trim(),
        type,
        target_screen: targetScreen || null,
        description: description.trim(),
        requester_priority: priority,
      })
      for (const f of files) {
        await uploadM.mutateAsync({ id: created.id, file: f })
      }
      onCreated?.(created)
      onClose()
    } catch (err) {
      setError(err?.response?.data?.error || 'Não consegui salvar. Tenta de novo.')
    }
  }

  const screenOptions = TARGET_SCREENS.map((s) => ({ value: s, label: s }))

  return createPortal(
    <div className="confirm-backdrop sug-modal-backdrop" onClick={busy ? undefined : onClose}
         role="dialog" aria-modal="true" aria-labelledby="sug-create-title">
      <div className="sug-modal" onClick={(e) => e.stopPropagation()}>
        <header className="sug-modal-header">
          <div className="sug-modal-eyebrow">{devMode ? 'Nova demanda' : 'Nova sugestão'}</div>
          <h2 id="sug-create-title" className="sug-modal-title">
            {devMode ? 'O que precisa entrar na fila?' : 'Conta o que você precisa'}
          </h2>
          <p className="sug-modal-sub">
            Quanto mais detalhe (tela, print, o que esperava), mais rápido vira realidade.
          </p>
          <button className="sug-modal-x" onClick={onClose} disabled={busy} aria-label="Fechar">×</button>
        </header>

        <form className="sug-modal-form" onSubmit={handleSubmit}>
          {/* Tipo — chips */}
          <div className="field">
            <label>Tipo</label>
            <div className="sug-chips">
              {TYPE_ORDER.map((t) => (
                <button key={t} type="button"
                        className={`sug-chip sug-chip--type${type === t ? ' is-active' : ''} sug-type--${t}`}
                        onClick={() => setType(t)} disabled={busy}>
                  <span aria-hidden="true">{TYPE[t].icon}</span> {TYPE[t].label}
                </button>
              ))}
            </div>
          </div>

          <div className="field">
            <label htmlFor="sug-title">Título *</label>
            <input id="sug-title" className="input" value={title} maxLength={140}
                   onChange={(e) => setTitle(e.target.value)} disabled={busy}
                   placeholder="Resumo em uma linha" autoFocus />
          </div>

          <div className="sug-modal-grid">
            <div className="field">
              <label htmlFor="sug-screen">Tela / área</label>
              <RSelect inputId="sug-screen"
                       value={screenOptions.find((o) => o.value === targetScreen) ?? null}
                       options={screenOptions} onChange={(o) => setTargetScreen(o?.value ?? null)}
                       placeholder="Onde isso acontece?" isClearable isDisabled={busy} />
            </div>
            <div className="field">
              <label>Prioridade sugerida</label>
              <div className="sug-segmented">
                {REQ_PRIORITY_ORDER.map((p) => (
                  <button key={p} type="button"
                          className={`sug-seg sug-reqprio--${p}${priority === p ? ' is-active' : ''}`}
                          onClick={() => setPriority(p)} disabled={busy}>
                    {REQ_PRIORITY[p].label}
                  </button>
                ))}
              </div>
            </div>
          </div>

          <div className="field">
            <label htmlFor="sug-desc">Descrição *</label>
            <textarea id="sug-desc" className="input sug-textarea" value={description} rows={5}
                      onChange={(e) => setDescription(e.target.value)} disabled={busy}
                      placeholder={type === 'bug'
                        ? 'O que acontece? O que você esperava? Como reproduzir?'
                        : 'Descreva o que quer e por quê. Pode caprichar no detalhe.'} />
          </div>

          <div className="field">
            <label>Anexos</label>
            <ClipboardPasteZone files={files} onChange={setFiles} max={6} />
          </div>

          {error && <div className="sug-modal-error" role="alert">{error}</div>}

          <div className="sug-modal-actions">
            <button type="button" className="btn btn-secondary" onClick={onClose} disabled={busy}>Cancelar</button>
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {busy ? 'Enviando…' : devMode ? 'Criar demanda' : 'Enviar sugestão'}
            </button>
          </div>
        </form>
      </div>
    </div>,
    document.body,
  )
}
