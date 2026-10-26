import api from '../../api/client'
import AttachmentImage from './AttachmentImage'
import { isImage } from './utils'

// Um anexo da sugestão, como o hub o guarda: imagem (miniatura + lightbox),
// outro arquivo (baixa pelo proxy) ou link. Os de antes não traziam `kind`:
// valem como arquivo.
// ⚠️ A "miniatura" é a imagem INTEIRA pelo proxy de bytes, como sempre foi. Não
// peça `?thumb=1`: o que sobe por aqui não tem WebP (o hub degrada para
// `temThumb: false`) e o pedido de miniatura dá 404.

function hostOf(u) {
  try { return new URL(u).host } catch { return u }
}

// Baixa pelos BYTES da API (com JWT), nunca por URL de bucket — o mesmo motivo
// do AttachmentImage.
async function baixar(att) {
  const r = await api.get(`/suggestions/attachments/${att.id}`, { responseType: 'blob' })
  const url = URL.createObjectURL(r.data)
  const a = document.createElement('a')
  a.href = url
  a.download = att.filename || 'anexo'
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export default function AttachmentItem({ att, onOpenImage, large }) {
  if (att.kind === 'link') {
    return (
      <a className="sug-att-link" href={att.link_url} target="_blank" rel="noopener noreferrer">
        {att.label || hostOf(att.link_url)}
      </a>
    )
  }
  if (isImage(att)) {
    return (
      <button type="button" className={`sug-att-thumb${large ? ' sug-att-thumb--lg' : ''}`} onClick={() => onOpenImage?.(att)}>
        <AttachmentImage att={att} />
      </button>
    )
  }
  return (
    <button type="button" className="sug-att-file" onClick={() => { baixar(att).catch(() => {}) }}>
      {att.filename || 'arquivo'}
    </button>
  )
}
