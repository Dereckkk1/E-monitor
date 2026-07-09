import { useCallback, useEffect, useRef, useState } from 'react'

// Zona de anexos que aceita 3 caminhos: colar print do clipboard (Ctrl+V),
// arrastar-e-soltar, e clicar pra escolher. É o toque "não-é-outro-software":
// tirou print → cola → já anexou. Trabalha com File objects locais (preview via
// object URL); o upload real acontece no submit do form/comentário.
//
// Props:
//   files: File[]                 — lista controlada
//   onChange: (File[]) => void
//   max: número máximo (default 6)
//   pasteTarget: 'window' | 'self' — onde escuta o paste (default 'self')
const IMAGE_MIME = ['image/png', 'image/jpeg', 'image/webp', 'image/gif']
const MAX_BYTES = 10 * 1024 * 1024

export default function ClipboardPasteZone({ files = [], onChange, max = 6, pasteTarget = 'self', hint }) {
  const inputRef = useRef(null)
  const rootRef = useRef(null)
  const [dragging, setDragging] = useState(false)
  const [error, setError] = useState('')
  const [previews, setPreviews] = useState([])

  // Gera/limpa object URLs pros previews conforme a lista muda.
  useEffect(() => {
    const urls = files.map((f) => URL.createObjectURL(f))
    setPreviews(urls)
    return () => urls.forEach((u) => URL.revokeObjectURL(u))
  }, [files])

  const addFiles = useCallback((incoming) => {
    setError('')
    const valid = []
    for (const f of incoming) {
      if (!IMAGE_MIME.includes(f.type)) { setError('Só imagens (PNG, JPG, WEBP, GIF).'); continue }
      if (f.size > MAX_BYTES) { setError('Máx. 10MB por imagem.'); continue }
      valid.push(f)
    }
    if (!valid.length) return
    const next = [...files, ...valid].slice(0, max)
    if (files.length + valid.length > max) setError(`Máximo de ${max} imagens.`)
    onChange(next)
  }, [files, max, onChange])

  // Paste handler — escuta no elemento (ou na window) e captura imagens.
  useEffect(() => {
    const handler = (e) => {
      const items = e.clipboardData?.items
      if (!items) return
      const imgs = []
      for (const it of items) {
        if (it.kind === 'file' && it.type.startsWith('image/')) {
          const f = it.getAsFile()
          if (f) imgs.push(f)
        }
      }
      if (imgs.length) { e.preventDefault(); addFiles(imgs) }
    }
    const el = pasteTarget === 'window' ? window : rootRef.current
    if (!el) return
    el.addEventListener('paste', handler)
    return () => el.removeEventListener('paste', handler)
  }, [addFiles, pasteTarget])

  function onDrop(e) {
    e.preventDefault(); setDragging(false)
    if (e.dataTransfer?.files?.length) addFiles(Array.from(e.dataTransfer.files))
  }
  function removeAt(i) {
    const next = files.slice(); next.splice(i, 1); onChange(next)
  }

  return (
    <div className="sug-paste" ref={rootRef} tabIndex={-1}>
      <div
        className={`sug-paste-drop${dragging ? ' is-dragging' : ''}`}
        onDragOver={(e) => { e.preventDefault(); setDragging(true) }}
        onDragLeave={() => setDragging(false)}
        onDrop={onDrop}
        onClick={() => inputRef.current?.click()}
        role="button"
        tabIndex={0}
        onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') inputRef.current?.click() }}
      >
        <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <rect x="3" y="3" width="18" height="18" rx="2" />
          <circle cx="8.5" cy="8.5" r="1.5" />
          <path d="M21 15l-5-5L5 21" />
        </svg>
        <span className="sug-paste-cta">
          <strong>Cole um print</strong> (Ctrl+V), arraste ou clique
        </span>
        <span className="sug-paste-hint">{hint || 'PNG · JPG · WEBP · GIF · até 10MB'}</span>
      </div>
      <input
        ref={inputRef}
        type="file"
        accept={IMAGE_MIME.join(',')}
        multiple
        hidden
        onChange={(e) => { addFiles(Array.from(e.target.files || [])); e.target.value = '' }}
      />
      {error && <div className="sug-paste-error">{error}</div>}
      {!!files.length && (
        <ul className="sug-paste-thumbs">
          {files.map((f, i) => (
            <li key={i} className="sug-paste-thumb">
              <img src={previews[i]} alt={f.name} />
              <button type="button" className="sug-paste-thumb-x" onClick={() => removeAt(i)} aria-label="Remover">×</button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
