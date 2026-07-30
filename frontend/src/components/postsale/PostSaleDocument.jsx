// PostSaleDocument.jsx — o documento de pós-venda.
//
// UM componente para DOIS consumidores: o preview do passo 4 do wizard e a
// página pública /pos-venda/:token. Os dois recebem o MESMO payload (o preview
// vem de GET /preview, o público de GET /public/post-sale/{token}), então o que
// o admin aprova é literalmente o que o cliente abre.
//
// `interactive={false}` no preview desliga os downloads: o .zip só existe
// depois do publish.
import PostSaleHero from './PostSaleHero'
import CampaignBlock from './CampaignBlock'
import PostSaleFooter from './PostSaleFooter'
import { useReveal } from './motion'

export default function PostSaleDocument({ payload, interactive = true, onDownload, mapUrlFor }) {
  const [greetRef, greetShown] = useReveal()
  if (!payload) return null

  const {
    client,
    intro_message: intro,
    period_label: periodLabel,
    campaigns = [],
    footer,
  } = payload

  return (
    <div className="ps-doc">
      <PostSaleHero periodLabel={periodLabel} />

      <div className="ps-container">
        <section ref={greetRef} className={`ps-greeting${greetShown ? ' is-shown' : ''}`}>
          {client?.logo_url && (
            <img className="ps-greeting-logo" src={client.logo_url} alt="" aria-hidden="true" />
          )}
          <div className="ps-greeting-body">
            <h2 className="ps-greeting-title">Olá, equipe {client?.name}!</h2>
            <p className="ps-greeting-sub">Vamos conferir os resultados?</p>
            {intro && <p className="ps-greeting-text">{intro}</p>}
          </div>
        </section>

        <div className="ps-blocks">
          {campaigns.map((c) => (
            <CampaignBlock
              key={c.campaign_id}
              block={c}
              interactive={interactive}
              onDownload={onDownload}
              mapUrlFor={mapUrlFor}
            />
          ))}
        </div>
      </div>

      <PostSaleFooter footer={footer} />
    </div>
  )
}
