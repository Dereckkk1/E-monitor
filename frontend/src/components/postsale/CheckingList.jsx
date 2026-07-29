// CheckingList.jsx — as duas listas de emissora do Checking e a frase de fecho.
//
// "above" e "compensation" vêm classificadas do backend (postsale.Classify); o
// frontend não reclassifica. Duas fontes de verdade divergiriam no primeiro
// ajuste de regra, e a divergência apareceria pro cliente.
import StationAvatar from '../StationAvatar'
import { stationDial, useReveal } from './motion'

function StationCard({ row }) {
  const pct = row.delivery_pct

  return (
    <li className={`ps-station ps-station--${row.kind}`}>
      <StationAvatar station={{ name: row.name, logo_url: row.logo_url }} size={44} />

      <div className="ps-station-info">
        <span className="ps-station-name">{row.name}</span>
        <span className="ps-station-dial">{stationDial(row)}</span>

        {pct != null && (
          <div className="ps-station-bar">
            {/* A barra é decorativa: a leitura acessível é a % em texto ao
                lado, então aqui é aria-hidden em vez de role="img". */}
            <div
              className="ps-station-bar-fill"
              style={{ width: `${Math.min(100, pct)}%` }}
              aria-hidden="true"
            />
          </div>
        )}
      </div>

      <div className="ps-station-tags">
        {pct != null && (
          <span className="ps-station-pct">
            {pct}<span className="ps-station-pct-unit">%</span>
          </span>
        )}
        {row.kind === 'above' && row.bonus_count > 0 && (
          <span className="ps-pill ps-pill--bonus">
            +{row.bonus_count} {row.bonus_count === 1 ? 'bonificação' : 'bonificações'}
          </span>
        )}
        {row.kind === 'compensation' && row.compensated && (
          <span className="ps-pill ps-pill--compensated">compensado</span>
        )}
        {row.note && <span className="ps-station-note">{row.note}</span>}
      </div>
    </li>
  )
}

export default function CheckingList({ text, rows = [], conformingCount = 0 }) {
  const above = rows.filter((r) => r.kind === 'above')
  const comp = rows.filter((r) => r.kind === 'compensation')
  const [ref, shown] = useReveal()

  // Nada a mostrar e nada a contar: o bloco inteiro some em vez de virar um
  // "Checking" vazio.
  if (above.length === 0 && comp.length === 0 && conformingCount === 0 && !text) {
    return null
  }

  return (
    <section ref={ref} className={`ps-checking${shown ? ' is-shown' : ''}`}>
      <h4 className="ps-checking-title">Checking</h4>
      {text && <p className="ps-checking-text">{text}</p>}

      {above.length > 0 && (
        <>
          <h5 className="ps-checking-sub">Acima do contratado</h5>
          <ul className="ps-station-list">
            {above.map((r) => <StationCard key={r.station_id} row={r} />)}
          </ul>
        </>
      )}

      {comp.length > 0 && (
        <>
          <h5 className="ps-checking-sub ps-checking-sub--amber">Compensações</h5>
          <ul className="ps-station-list">
            {comp.map((r) => <StationCard key={r.station_id} row={r} />)}
          </ul>
        </>
      )}

      {conformingCount > 0 && (
        <p className="ps-checking-rest">
          {conformingCount === 1
            ? 'A outra emissora entregou conforme o planejado.'
            : `As outras ${conformingCount} emissoras entregaram conforme o planejado.`}
        </p>
      )}
    </section>
  )
}
