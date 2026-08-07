// CheckingList.jsx — o checking emissora por emissora.
//
// Linhas separadas por RÉGUA, não cards: é o padrão da /boasvindas ("a régua
// deixa a tipografia carregar a hierarquia"). Com 20+ emissoras, uma grade de
// cards idênticos vira ruído; a régua mantém a leitura em coluna.
//
// "above" e "compensation" vêm classificadas do backend (postsale.Classify); o
// frontend não reclassifica — duas fontes de verdade divergiriam no primeiro
// ajuste de regra, e a divergência apareceria pro cliente.
import StationAvatar from '../StationAvatar'
import { useRevealOnce } from './motion'

// O selo da linha (bonificações / compensado). Fica ANTES da barra: lido da
// esquerda pra direita, o rótulo qualifica o que a barra mostra em seguida.
function rowPill(row) {
  if (row.kind === 'above' && row.bonus_count > 0) {
    return (
      <span className="ps-pill ps-pill--bonus">
        +{row.bonus_count} {row.bonus_count === 1 ? 'bonificação' : 'bonificações'}
      </span>
    )
  }
  if (row.kind === 'compensation' && row.compensated) {
    return <span className="ps-pill ps-pill--compensated">compensado</span>
  }
  return null
}

// `flagged` = alguma linha DESTA lista tem selo. Quando ninguém tem, a calha
// fixa some e as barras voltam a usar a largura toda — um recuo de 148px sem
// nada dentro pareceria bug.
function StationRow({ row, dial, flagged }) {
  const pct = row.delivery_pct

  return (
    <li className={`ps-station ps-station--${row.kind}`}>
      <StationAvatar station={{ name: row.name, logo_url: row.logo_url }} size={40} />

      <div className="ps-station-id">
        <span className="ps-station-name">{row.name}</span>
        <span className="ps-station-dial">{dial(row)}</span>
      </div>

      <div className="ps-station-meter">
        <div className="ps-station-gauge">
          {/* Calha de largura fixa mesmo vazia: sem ela as barras começariam em
              x diferente a cada linha, já que cada <li> é um grid próprio. */}
          {flagged && <span className="ps-station-flag">{rowPill(row)}</span>}
          {pct != null && (
            <div className="ps-station-bar">
              <div
                className="ps-station-bar-fill"
                style={{ width: `${Math.min(100, pct)}%` }}
                aria-hidden="true"
              />
            </div>
          )}
        </div>
        {row.note && <span className="ps-station-note">{row.note}</span>}
      </div>

      <div className="ps-station-tags">
        {pct != null && (
          <span className="ps-station-pct">
            {pct}<span className="ps-station-pct-unit">%</span>
          </span>
        )}
      </div>
    </li>
  )
}

export default function CheckingList({ text, rows = [], conformingCount = 0, dial }) {
  const ref = useRevealOnce({ delay: 120 })
  const above = rows.filter((r) => r.kind === 'above')
  const comp = rows.filter((r) => r.kind === 'compensation')
  const aboveFlagged = above.some((r) => rowPill(r) != null)
  const compFlagged = comp.some((r) => rowPill(r) != null)

  // Nada a mostrar e nada a contar: o bloco inteiro some em vez de virar um
  // "Checking" vazio.
  if (above.length === 0 && comp.length === 0 && conformingCount === 0 && !text) {
    return null
  }

  return (
    <section ref={ref} className="ps-checking">
      <h3 className="ps-checking-title">Checking</h3>
      {text && <p className="ps-checking-text">{text}</p>}

      {above.length > 0 && (
        <>
          <p className="ps-checking-sub">Acima do contratado</p>
          <ul className="ps-station-list">
            {above.map((r) => (
              <StationRow key={r.station_id} row={r} dial={dial} flagged={aboveFlagged} />
            ))}
          </ul>
        </>
      )}

      {comp.length > 0 && (
        <>
          <p className="ps-checking-sub ps-checking-sub--amber">Compensações</p>
          <ul className="ps-station-list">
            {comp.map((r) => (
              <StationRow key={r.station_id} row={r} dial={dial} flagged={compFlagged} />
            ))}
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
