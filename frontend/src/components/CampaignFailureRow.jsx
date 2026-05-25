import './CampaignFailureCard.css'

const STATUS_LABEL = {
  programada: 'Programada',
  ativa:      'Ativa',
  concluida:  'Concluída',
  cancelada:  'Cancelada',
}

function fmtDateBR(yyyymmdd) {
  if (!yyyymmdd) return '—'
  const [y, m, d] = yyyymmdd.split('-')
  return `${d}/${m}/${y}`
}

export default function CampaignFailureRow({ entry, onOpen }) {
  const { campaign, stations_with_failure, total_failure_days, total_deficit, is_fully_bonified } = entry
  return (
    <tr
      className="cfr-row"
      onClick={onOpen}
      role="button"
      tabIndex={0}
      onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onOpen() } }}
    >
      <td className="cfr-cli">
        <div className="cfr-cli-stack">
          <span className="cfr-cli-name" title={campaign.client_name}>{campaign.client_name}</span>
          <span className="cfr-camp-name" title={campaign.name}>{campaign.name}</span>
          <span className="cfr-period">
            {fmtDateBR(campaign.start_date)} – {fmtDateBR(campaign.end_date)}
          </span>
        </div>
      </td>
      <td className="cfr-status">
        <span className={`cfr-status-chip cfr-status-${campaign.status}`}>
          {STATUS_LABEL[campaign.status] || campaign.status}
        </span>
      </td>
      <td className="cfr-num">{stations_with_failure}</td>
      <td className="cfr-num">{total_failure_days}</td>
      <td className="cfr-num cfr-num-deficit">{total_deficit}</td>
      <td className="cfr-bonif">
        {is_fully_bonified ? (
          <span className="cfc-tag cfc-tag-bonif">100% bonificada</span>
        ) : null}
      </td>
    </tr>
  )
}
