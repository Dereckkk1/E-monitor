import { useState } from 'react'
import api from '../api/client'
import StationAvatar from './StationAvatar'
import DeficitSplit from './DeficitSplit'
import { generateCampaignFailurePdf } from '../utils/pdfCampaignFailure'
import './CampaignFailureCard.css'

const MAX_STATIONS_PREVIEW = 6
const STRIP_SLOTS = 10

function pct(identified, programmed) {
  if (!programmed) return 0
  return Math.round((identified / programmed) * 100)
}

// 10-segment inline coverage bar — green segments = coverage %, red rest.
// Tagged 'bonif' when station is bonificada (purple wash).
function CoverageBar({ identified, programmed, isBonified }) {
  const ratio = programmed > 0 ? Math.min(1, identified / programmed) : 0
  const filled = Math.round(ratio * 10)
  return (
    <div
      className={`cfc-cov ${isBonified ? 'cfc-cov--bonif' : ''}`}
      role="img"
      aria-label={`Cobertura ${Math.round(ratio * 100)}%`}
    >
      {Array.from({ length: 10 }).map((_, i) => (
        <span
          key={i}
          className={`cfc-cov-seg ${i < filled ? 'cfc-cov-seg--on' : 'cfc-cov-seg--off'}`}
        />
      ))}
    </div>
  )
}

// 10-slot strip on the card header — dots colored by station state.
// Stations beyond slot 10 collapse into the last slot as a stacked indicator.
function SeverityStrip({ stations }) {
  const slots = Array.from({ length: STRIP_SLOTS }).map((_, i) => {
    const s = stations[i]
    if (!s) return 'empty'
    return s.is_bonified ? 'bonif' : 'deficit'
  })
  const overflow = Math.max(0, stations.length - STRIP_SLOTS)
  return (
    <div className="cfc-strip" role="img" aria-label={`${stations.length} emissoras envolvidas`}>
      {slots.map((kind, i) => (
        <span key={i} className={`cfc-strip-dot cfc-strip-dot--${kind}`} />
      ))}
      {overflow > 0 && <span className="cfc-strip-overflow">+{overflow}</span>}
    </div>
  )
}

// Inline PDF icon button. Generates PDF from drill-in fetch on click.
function PdfButton({ campaignId }) {
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

function StationRow({ station }) {
  const {
    station: s, programmed, identified, deficit,
    deficit_absent, deficit_off_slot, is_bonified,
  } = station
  return (
    <li className="cfc-station-row">
      <StationAvatar station={s} size={28} />
      <div className="cfc-station-id">
        <span className="cfc-station-name" title={s.name}>{s.name}</span>
        <span className="cfc-station-city">{s.city || s.dial || '—'}</span>
      </div>
      <div className="cfc-station-num">
        <div className="cfc-station-num-row">
          <span className="cfc-num-ok">{identified.toLocaleString('pt-BR')}</span>
          <span className="cfc-num-sep">/</span>
          <span className="cfc-num-total">{programmed.toLocaleString('pt-BR')}</span>
          <span className="cfc-num-pct"> · {pct(identified, programmed)}%</span>
        </div>
        <CoverageBar identified={identified} programmed={programmed} isBonified={is_bonified} />
        {is_bonified ? (
          <span className="cfc-tag cfc-tag-bonif">falhou, bonificada</span>
        ) : (
          <DeficitSplit
            total={deficit}
            absent={deficit_absent}
            offSlot={deficit_off_slot}
            align="end"
          />
        )}
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
          <SeverityStrip stations={stations} />
        </div>
        <div className="cfc-head-side">
          <PdfButton campaignId={campaign.id} campaignName={campaign.name} />
          <div className="cfc-head-count">
            <span className="cfc-count-num">{stations.length}</span>
            <span className="cfc-count-label">
              {stations.length === 1 ? 'emissora' : 'emissoras'}
            </span>
          </div>
        </div>
      </header>

      <ul className="cfc-station-list">
        {preview.map((st) => (
          <StationRow key={String(st.station.id)} station={st} />
        ))}
      </ul>

      <footer className="cfc-foot">
        {remaining > 0 ? (
          <span className="cfc-more">+{remaining} {remaining === 1 ? 'emissora' : 'emissoras'} no detalhe</span>
        ) : (
          <span className="cfc-more">{stations.length === 1 ? 'detalhe da emissora' : 'detalhe das emissoras'}</span>
        )}
        <span className="cfc-cta">Ver detalhes →</span>
      </footer>
    </article>
  )
}
