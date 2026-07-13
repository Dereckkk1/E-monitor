import { useEffect, useCallback } from 'react'
import { createPortal } from 'react-dom'
import AttachmentImage from './AttachmentImage'

// Lightbox de imagem: recebe a lista de anexos e o índice aberto. Cada imagem é
// buscada pela API (blob autenticado) via AttachmentImage — não usa presigned
// direto. Setas navegam, Esc/clique-no-fundo fecha. Portaliza pro body.
export default function AttachmentLightbox({ attachments = [], index, onClose, onIndex }) {
  const open = index != null && index >= 0 && index < attachments.length

  const go = useCallback((delta) => {
    if (!open) return
    const n = (index + delta + attachments.length) % attachments.length
    onIndex(n)
  }, [open, index, attachments.length, onIndex])

  useEffect(() => {
    if (!open) return
    const onKey = (e) => {
      if (e.key === 'Escape') onClose()
      else if (e.key === 'ArrowRight') go(1)
      else if (e.key === 'ArrowLeft') go(-1)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, go, onClose])

  if (!open) return null
  const att = attachments[index]

  return createPortal(
    <div className="sug-lightbox" onClick={onClose}>
      <button className="sug-lightbox-close" onClick={onClose} aria-label="Fechar">×</button>
      {attachments.length > 1 && (
        <button className="sug-lightbox-nav sug-lightbox-prev"
                onClick={(e) => { e.stopPropagation(); go(-1) }} aria-label="Anterior">‹</button>
      )}
      <figure className="sug-lightbox-figure" onClick={(e) => e.stopPropagation()}>
        <AttachmentImage att={att} />
        <figcaption>{index + 1} / {attachments.length}</figcaption>
      </figure>
      {attachments.length > 1 && (
        <button className="sug-lightbox-nav sug-lightbox-next"
                onClick={(e) => { e.stopPropagation(); go(1) }} aria-label="Próxima">›</button>
      )}
    </div>,
    document.body,
  )
}
