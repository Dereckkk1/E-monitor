// PostSaleDocument.jsx — o documento de pós-venda.
//
// UM componente para DOIS consumidores: o preview do passo 4 do wizard e a
// página pública /pos-venda/:token. Os dois recebem o MESMO payload, então o que
// o admin aprova é literalmente o que o cliente abre.
//
// RITMO DA PÁGINA (igual à /boasvindas): navy → claro → navy → claro → navy. As
// faixas são full-bleed e um container interno (.ps-shell) limita a medida do
// texto. Sem a alternância, o miolo vira uma laje única de quase-branco entre o
// hero e o rodapé.
//
// `interactive={false}` no preview desliga os downloads: o .zip só existe depois
// do publish.
import PostSaleHero from './PostSaleHero'
import CampaignBlock from './CampaignBlock'
import PostSaleFooter from './PostSaleFooter'
import { useRevealOnce } from './motion'

export default function PostSaleDocument({ payload, interactive = true, onDownload, mapUrlFor }) {
  const greetRef = useRevealOnce()
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
      <PostSaleHero
        clientName={client?.name}
        clientLogo={client?.logo_url}
        periodLabel={periodLabel}
      />

      <section className="ps-band ps-band--greet">
        <div ref={greetRef} className="ps-shell">
          <h2 className="ps-greet-title">Olá, equipe {client?.name}!</h2>
          {intro && <p className="ps-greet-text">{intro}</p>}
        </div>
      </section>

      {campaigns.map((c, i) => (
        <CampaignBlock
          key={c.campaign_id}
          block={c}
          index={i}
          // Alterna a partir da faixa clara da saudação: campanha 1 em navy.
          dark={i % 2 === 0}
          interactive={interactive}
          onDownload={onDownload}
          mapUrlFor={mapUrlFor}
        />
      ))}

      <PostSaleFooter footer={footer} />
    </div>
  )
}
