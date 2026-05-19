import { Link } from 'react-router-dom'
import './CampaignDeficitChip.css'

export default function CampaignDeficitChip({ data }) {
  const { campaign_id, campaign_name, client_name, expected, delivered, deficit, affected_by } = data
  const isSilent = affected_by?.includes('silent-gap')

  return (
    <Link to={`/campaigns?campaign=${campaign_id}`} className="campaign-deficit-chip">
      <div className="cdc-name-row">
        <strong className="cdc-name">{campaign_name}</strong>
        <span className="cdc-client">{client_name}</span>
      </div>
      <div className="cdc-numbers-row">
        <span className="cdc-num">esperado <b>{expected}</b></span>
        <span className="cdc-dot">·</span>
        <span className="cdc-num">entregue <b>{delivered}</b></span>
        <span className="cdc-dot">·</span>
        <span className="cdc-num cdc-deficit">faltam <b>{deficit}</b></span>
        {isSilent && <span className="cdc-pill cdc-pill-warn">silent-gap</span>}
      </div>
      <svg className="cdc-arrow" width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
        <path d="M5 3l4 4-4 4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
      </svg>
    </Link>
  )
}
