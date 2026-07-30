// PostSalePage.jsx — /pos-venda/:token.
//
// Fora do AppShell: sem sidebar, sem menu, sem sessão. O token da URL é a
// credencial — o cliente chega aqui pelo link do email.
//
// O backend responde 404 (nunca 401) para token inválido, revogado ou de
// rascunho, então o interceptor de 401 do axios não é acionado por esta
// chamada. Ainda assim, /pos-venda está em PUBLIC_ROUTES no client.js: sem
// isso, qualquer 401 de chamada paralela (telemetria) sequestraria o visitante
// pro /login — foi o que aconteceu com /boasvindas.
import { useParams } from 'react-router-dom'

import { usePublicPostSale } from '../api/hooks'
import PostSaleDocument from '../components/postsale/PostSaleDocument'
import './PostSalePage.css'

const API_BASE = `${import.meta.env.VITE_API_URL ?? ''}/v1/internal`

export default function PostSalePage() {
  const { token } = useParams()
  const { data, isLoading, isError } = usePublicPostSale(token)

  // Skeleton no mesmo desenho do hero (faixa escura + shimmer), pra que a
  // página não pisque de branco pra navy quando o dado chega.
  if (isLoading) {
    return (
      <div className="ps-loading" role="status" aria-label="Carregando seu pós-venda">
        <div className="ps-shell">
          <span className="ps-sk ps-sk-logo" />
          <span className="ps-sk ps-sk-title" />
          <span className="ps-sk ps-sk-lead" />
          <span className="ps-sk ps-sk-lead ps-sk-short" />
        </div>
      </div>
    )
  }

  // Token inválido, revogado e rascunho respondem igual, de propósito: a
  // mensagem é a mesma nos três casos pra não virar oráculo.
  if (isError || !data) {
    return (
      <div className="ps-gone">
        <div className="ps-gone-card">
          <img src="/E-monitor%20logo.png" alt="E-monitor" className="ps-gone-logo" />
          <h1 className="ps-gone-title">Este link não está mais disponível</h1>
          <p className="ps-gone-text">
            Fale com quem enviou o pós-venda para receber um novo acesso.
          </p>
        </div>
      </div>
    )
  }

  const base = `${API_BASE}/public/post-sale/${encodeURIComponent(token)}`

  function handleDownload(block) {
    // O endpoint redireciona (302) pra uma URL presignada de 15 minutos.
    // window.location em vez de fetch: download atravessa o redirect sem CORS.
    window.location.href = `${base}/campaigns/${block.campaign_id}/bundle.zip`
  }

  // O mapa é servido pela mesma rota pública, que revalida o token antes de
  // presignar. A URL é montada aqui (e não gravada no payload) porque cada
  // destinatário tem seu próprio token e a assinatura do S3 expira.
  function mapUrlFor(block) {
    if (!block.has_bundle) return null
    return `${base}/campaigns/${block.campaign_id}/image/map.png`
  }

  return (
    <PostSaleDocument
      payload={data}
      onDownload={handleDownload}
      mapUrlFor={mapUrlFor}
    />
  )
}
