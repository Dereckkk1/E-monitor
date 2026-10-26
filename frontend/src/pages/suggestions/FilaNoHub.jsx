import { HUB_PORTAL_URL } from './constants'

// O que a pessoa gestora vê aqui desde que a fila mora no hub. A Central de
// Comando (CentralDeComando.jsx) continua no código; só deixou de ser a tela.
export default function FilaNoHub() {
  return (
    <div className="sug-page sug-page--author">
      <section className="sug-hubnotice" role="status">
        <h1 className="sug-h1">A fila agora vive no hub</h1>
        <p className="sug-sub">
          As sugestões do E-monitor, da Plura e do hub caem numa fila só, na Central de Clientes. A triagem é feita lá.
        </p>
        <a className="btn btn-primary" href={`${HUB_PORTAL_URL}/admin/sugestoes`} target="_blank" rel="noopener noreferrer">
          Abrir a fila no hub
        </a>
      </section>
    </div>
  )
}
