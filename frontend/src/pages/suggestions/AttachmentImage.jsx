import { useEffect, useState } from 'react'
import api from '../../api/client'

// Exibe um anexo de sugestão buscando os BYTES pela API (com JWT) e tocando
// um blob: object URL — NÃO usa a URL presigned direto no <img src>.
//
// Por quê: em produção o host do MinIO assado na URL presigned é
// `localhost:9000`, que o navegador do usuário (em https://e-monitor.online)
// não consegue alcançar → ERR_CONNECTION_REFUSED / bloqueio de loopback. É o
// mesmo motivo pelo qual o áudio de evidência migrou pro proxy em 2026-07-03.
// Ver docs/features/evidence-presigned-urls.md e suggestions-board.md.
export default function AttachmentImage({ att, className, alt }) {
  const aid = att?.id
  // O estado carrega o aid a que pertence; enquanto state.aid !== aid, ainda
  // não temos o blob deste anexo → mostramos o placeholder. Assim evitamos
  // resetar o estado sincronamente dentro do effect (react-hooks/set-state).
  const [state, setState] = useState({ aid: null, url: null, err: false })

  useEffect(() => {
    if (!aid) return
    let objectUrl = null
    let cancelled = false
    api
      .get(`/suggestions/attachments/${aid}`, { responseType: 'blob' })
      .then((resp) => {
        if (cancelled) return
        objectUrl = URL.createObjectURL(resp.data)
        setState({ aid, url: objectUrl, err: false })
      })
      .catch(() => { if (!cancelled) setState({ aid, url: null, err: true }) })
    return () => {
      cancelled = true
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [aid])

  const ready = state.aid === aid
  if (ready && state.err) {
    return (
      <span className="sug-att-broken" role="img" aria-label="imagem indisponível" title="Não consegui carregar a imagem">
        imagem indisponível
      </span>
    )
  }
  if (!ready || !state.url) {
    return <span className="sug-att-loading sug-sk" aria-hidden="true" />
  }
  return <img src={state.url} alt={alt || att?.filename || 'anexo'} className={className} loading="lazy" />
}
