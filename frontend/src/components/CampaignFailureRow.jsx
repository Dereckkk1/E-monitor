import { useState } from 'react'
import api from '../api/client'
import { generateCampaignFailurePdf } from '../utils/pdfCampaignFailure'
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

function InlinePdfButton({ campaignId }) {
  const [busy, setBusy] = useState(false)
  async function handleClick(e) {
    e.stopPropagation()
    if (busy) return
    setBusy(true)
    try {
      const { data } = await api.get(`/admin/campaign-failures/${campaignId}`)
      await generateCampaignFailurePdf(data)
    } catch (err) {
      console.error('PDF gen failed', err)
      alert('Não foi possível gerar o PDF. Tente novamente.')
    } finally {
      setBusy(false)
    }
  }
  return (
    <button
      className="cfc-pdf-btn"
      onClick={handleClick}
      disabled={busy}
      aria-label="Baixar PDF de cobrança"
      title="Baixar PDF de cobrança"
    >
      {busy ? (
        <span className="cfc-pdf-spin" aria-hidden="true" />
      ) : (
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
          <path d="M7 1.5v8.5M7 10l-3-3m3 3l3-3M2.5 12.5h9"
                stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
        </svg>
      )}
      <span className="cfc-pdf-btn-label">PDF</span>
    </button>
  )
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
      <td className="cfr-action">
        <InlinePdfButton campaignId={campaign.id} />
      </td>
    </tr>
  )
}
