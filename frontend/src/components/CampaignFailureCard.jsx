import StationAvatar from './StationAvatar'
import './CampaignFailureCard.css'

const MAX_STATIONS_PREVIEW = 6

function pct(identified, programmed) {
  if (!programmed) return 0
  return Math.round((identified / programmed) * 100)
}

function StationRow({ station }) {
  const { station: s, programmed, identified, deficit, is_bonified } = station
  return (
    <li className="cfc-station-row">
      <StationAvatar station={s} size={28} />
      <div className="cfc-station-id">
        <span className="cfc-station-name" title={s.name}>{s.name}</span>
        <span className="cfc-station-city">{s.city || s.dial || '—'}</span>
      </div>
      <div className="cfc-station-num">
        <span className="cfc-station-num-line">
          <span className="cfc-num-ok">{identified.toLocaleString('pt-BR')}</span>
          <span className="cfc-num-sep">/</span>
          <span className="cfc-num-total">{programmed.toLocaleString('pt-BR')}</span>
          <span className="cfc-num-pct"> · {pct(identified, programmed)}%</span>
        </span>
        {is_bonified ? (
          <span className="cfc-tag cfc-tag-bonif">falhou, bonificada</span>
        ) : deficit > 0 ? (
          <span className="cfc-tag cfc-tag-deficit">faltam {deficit.toLocaleString('pt-BR')}</span>
        ) : null}
      </div>
    </li>
  )
}

export default function CampaignFailureCard({ entry, onOpen }) {
  const { campaign, stations } = entry
  const preview = stations.slice(0, MAX_STATIONS_PREVIEW)
  const remaining = stations.length - preview.length

  return (
    <article
      className="cfc-card"
      onClick={onOpen}
      role="button"
      tabIndex={0}
      onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onOpen() } }}
    >
      <header className="cfc-head">
        {campaign.client_logo_url ? (
          <img
            src={campaign.client_logo_url}
            alt={campaign.client_name}
            className="cfc-client-logo"
            onError={(e) => { e.target.style.display = 'none' }}
          />
        ) : (
          <div className="cfc-client-logo cfc-client-logo--fallback">
            {(campaign.client_name || '?').charAt(0).toUpperCase()}
          </div>
        )}
        <div className="cfc-head-id">
          <span className="cfc-client" title={campaign.client_name}>{campaign.client_name}</span>
          <span className="cfc-campaign" title={campaign.name}>{campaign.name}</span>
        </div>
        <div className="cfc-head-num">
          <span className="cfc-num-stations">{stations.length}</span>
          <span className="cfc-num-stations-label">
            {stations.length === 1 ? 'emissora' : 'emissoras'}
          </span>
        </div>
      </header>

      <ul className="cfc-station-list">
        {preview.map((st) => (
          <StationRow key={String(st.station.id)} station={st} />
        ))}
      </ul>

      <footer className="cfc-foot">
        {remaining > 0 && (
          <span className="cfc-more">+{remaining} {remaining === 1 ? 'emissora' : 'emissoras'} no detalhe</span>
        )}
        <span className="cfc-cta">Ver detalhes →</span>
      </footer>
    </article>
  )
}
