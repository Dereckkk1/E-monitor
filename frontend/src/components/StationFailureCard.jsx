import StationAvatar from './StationAvatar'
import CampaignDeficitChip from './CampaignDeficitChip'
import './StationFailureCard.css'

function fmtDuration(sec) {
  if (!sec || sec < 60) return `${sec || 0}s`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}min`
  const h = Math.floor(min / 60)
  const remMin = min - h * 60
  return remMin > 0 ? `${h}h ${remMin}min` : `${h}h`
}

function badgeTone(sec) {
  if (sec >= 1800) return 'danger'
  if (sec >= 300) return 'warn'
  return 'neutral'
}

export default function StationFailureCard({ data }) {
  const { station, incidents, total_down_seconds, has_silent_gap, campaigns } = data
  const tone = badgeTone(total_down_seconds)

  return (
    <article className="station-failure-card">
      <header className="sfc-header">
        <div className="sfc-logo">
          <StationAvatar station={station} size={48} />
        </div>
        <div className="sfc-identity">
          <h3 className="sfc-name">{station.name}</h3>
          <p className="sfc-meta">{station.dial}{station.city ? ` · ${station.city}` : ''}</p>
        </div>
        <span className={`sfc-badge sfc-badge-${tone}`}>
          {fmtDuration(total_down_seconds)} fora
        </span>
      </header>

      <p className="sfc-summary-line">
        {incidents.length} {incidents.length === 1 ? 'incidente' : 'incidentes'}
        {' · '}{fmtDuration(total_down_seconds)} fora
        {has_silent_gap && ' · silent-gap detectado'}
      </p>

      {campaigns.length > 0 && (
        <div className="sfc-campaigns">
          <h4 className="sfc-campaigns-title">Campanhas afetadas</h4>
          <ul className="sfc-campaigns-list">
            {campaigns.map(c => (
              <li key={c.campaign_id}>
                <CampaignDeficitChip data={c} />
              </li>
            ))}
          </ul>
        </div>
      )}
      {campaigns.length === 0 && (
        <p className="sfc-no-campaigns">nenhuma campanha tinha slot nesse intervalo</p>
      )}
    </article>
  )
}
