// PostSaleFooter.jsx — rodapé institucional, adaptado do Footer do
// signalads-frontend (E-rádios).
//
// Sem a barra de disclaimers de marketplace do original: ela fala de valores
// estimativos e de emissoras sem vínculo comercial, o que não tem relação com
// um pós-venda de campanha já entregue — e num documento de fechamento soaria
// como ressalva sobre os próprios números.
export default function PostSaleFooter({ footer }) {
  const year = new Date().getFullYear()

  return (
    <footer className="ps-footer">
      <div className="ps-footer-main">
        <div className="ps-footer-brand">
          <img src="/E-monitor%20logo.png" alt="E-monitor" className="ps-footer-logo" />
          <p className="ps-footer-desc">
            Monitoramento de veiculação de comerciais em rádio, com evidência de
            áudio de cada inserção.
          </p>
        </div>

        <div className="ps-footer-contact">
          <span className="ps-footer-label">Contato</span>
          {footer?.email && (
            <a className="ps-footer-link" href={`mailto:${footer.email}`}>{footer.email}</a>
          )}
          {footer?.city && <span className="ps-footer-city">{footer.city}</span>}

          <div className="ps-footer-social">
            {footer?.instagram && (
              <a className="ps-footer-link" href={footer.instagram}
                 target="_blank" rel="noreferrer noopener">Instagram</a>
            )}
            {footer?.linkedin && (
              <a className="ps-footer-link" href={footer.linkedin}
                 target="_blank" rel="noreferrer noopener">LinkedIn</a>
            )}
          </div>
        </div>
      </div>

      <div className="ps-footer-bottom">
        © {year} E-monitor. Todos os direitos reservados.
      </div>
    </footer>
  )
}
