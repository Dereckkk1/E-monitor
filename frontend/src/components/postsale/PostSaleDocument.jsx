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

/** Seta de link externo — o mesmo peso de traço dos ícones do documento. */
function ExternalIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor"
         strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M14 4h6v6" />
      <path d="M20 4 10.5 13.5" />
      <path d="M19 14.5V19a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h4.5" />
    </svg>
  )
}

export default function PostSaleDocument({ payload, interactive = true, onDownload, mapUrlFor }) {
  const greetRef = useRevealOnce()
  const attachRef = useRevealOnce()
  if (!payload) return null

  const {
    client,
    intro_message: intro,
    period_label: periodLabel,
    campaigns = [],
    attachments_url: attachmentsUrl,
    footer,
  } = payload

  // A faixa dos anexos continua a alternância navy/claro: as campanhas começam
  // em navy (i=0), então com nº PAR de campanhas a próxima faixa é navy de novo.
  // Sem campanha nenhuma, a saudação é clara e esta entra navy.
  const attachDark = campaigns.length % 2 === 0

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

      {/* Só existe quando o admin colou um link. O payload já chega filtrado
          para http(s) pelo safeExternalURL do backend. */}
      {attachmentsUrl && (
        <section className={`ps-band${attachDark ? ' is-dark' : ''}`}>
          <div ref={attachRef} className="ps-shell ps-attach">
            <h2 className="ps-band-name">Anexos</h2>
            <p className="ps-attach-text">
              Os arquivos deste fechamento estão numa pasta compartilhada.
            </p>
            <a
              className="ps-cta"
              href={attachmentsUrl}
              target="_blank"
              rel="noreferrer noopener"
            >
              Abrir anexos
              <ExternalIcon />
            </a>
          </div>
        </section>
      )}

      <PostSaleFooter footer={footer} />
    </div>
  )
}
