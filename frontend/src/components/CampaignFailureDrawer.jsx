import { useEffect, useMemo, useState } from 'react'
import { useCampaignFailureDetail } from '../api/hooks'
import { generateCampaignFailurePdf } from '../utils/pdfCampaignFailure'
import StationAvatar from './StationAvatar'
import './CampaignFailureCard.css'

const STATUS_LABEL = {
  programada: 'Programada',
  ativa:      'Ativa',
  concluida:  'Concluída',
}

function fmtDateBR(yyyymmdd) {
  if (!yyyymmdd) return '—'
  const [y, m, d] = yyyymmdd.split('-')
  return `${d}/${m}/${y}`
}
function fmtTimeNow() {
  const d = new Date()
  return `${String(d.getHours()).padStart(2,'0')}:${String(d.getMinutes()).padStart(2,'0')}`
}

// "24" if all days same month, "24/05" if multi-month.
function dayChipLabel(allDays, day) {
  const monthSet = new Set(allDays.map(d => d.slice(0, 7)))
  if (monthSet.size === 1) return day.slice(8, 10)
  return `${day.slice(8, 10)}/${day.slice(5, 7)}`
}

function DaysChips({ days, bonified }) {
  if (!days?.length) return null
  return (
    <div className="cfd-day-chips">
      {days.map(d => (
        <span
          key={d}
          className={`cfd-day-chip ${bonified ? 'cfd-day-chip--bonif' : ''}`}
          title={fmtDateBR(d)}
        >
          {dayChipLabel(days, d)}
        </span>
      ))}
    </div>
  )
}

// Stacked horizontal bar — 3 segments:
//  ok       (in_slot / programmed) — green
//  extras   (out_slot + out_date + bonus / programmed) — purple (compensating)
//  deficit  (deficit / programmed) — red
// Segments normalized so the row reads 100% even when extras overflow.
function FullCoverageBar({ programmed, identified, extras, deficit }) {
  const denom = Math.max(programmed, identified + Math.max(deficit, 0))
  if (denom <= 0) return null
  const okPct      = Math.max(0, Math.min(100, (identified / denom) * 100))
  const extrasPct  = Math.max(0, Math.min(100 - okPct, (extras / denom) * 100))
  const deficitPct = Math.max(0, Math.min(100 - okPct - extrasPct, (deficit / denom) * 100))
  const coveragePct = programmed > 0 ? Math.round((identified / programmed) * 100) : 0
  return (
    <div className="cfd-bar-wrap">
      <div className="cfd-bar" role="img" aria-label={`Cobertura ${coveragePct}%, extras ${extras}, déficit ${deficit}`}>
        {okPct > 0      && <div className="cfd-bar-seg-ok"      style={{ width: `${okPct}%` }} />}
        {extrasPct > 0  && <div className="cfd-bar-seg-extras"  style={{ width: `${extrasPct}%` }} />}
        {deficitPct > 0 && <div className="cfd-bar-seg-deficit" style={{ width: `${deficitPct}%` }} />}
      </div>
      <span className="cfd-bar-meta">
        <strong>{coveragePct}%</strong>
        {extras > 0 && <> · +{extras} extras</>}
      </span>
    </div>
  )
}

function StationRow({ entry }) {
  const { station: s, programmed, identified, deficit, extras, is_bonified, failure_days } = entry
  const pct = programmed > 0 ? Math.round((identified / programmed) * 100) : 0
  return (
    <>
      <tr>
        <td className="cfd-st-cell">
          <StationAvatar station={s} size={32} />
          <div className="cfd-st-id">
            <span className="cfd-st-name">{s.name}</span>
            <span className="cfd-st-city">{s.city || s.dial || '—'}</span>
          </div>
        </td>
        <td className="cfd-num">{programmed.toLocaleString('pt-BR')}</td>
        <td className="cfd-num">
          <span className="cfc-num-ok">{identified.toLocaleString('pt-BR')}</span>
          <span className="cfd-num-pct"> ({pct}%)</span>
        </td>
        <td>
          <DaysChips days={failure_days || []} bonified={is_bonified} />
          {is_bonified && (
            <div className="cfd-bonif-note">
              <svg width="11" height="11" viewBox="0 0 12 12" fill="none" aria-hidden="true">
                <path d="M3 6.5l2 2L9 4" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"/>
              </svg>
              Bonificada — extras: {extras}
            </div>
          )}
        </td>
      </tr>
      <tr className="cfd-bar-row">
        <td colSpan={4}>
          <FullCoverageBar programmed={programmed} identified={identified} extras={extras} deficit={deficit} />
        </td>
      </tr>
    </>
  )
}

export default function CampaignFailureDrawer({ campaignId, onClose }) {
  const { data, isLoading, error } = useCampaignFailureDetail(campaignId)
  const [pdfBusy, setPdfBusy] = useState(false)
  const [pdfStamp, setPdfStamp] = useState(null)

  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const campaign = data?.campaign
  const summary = data?.summary
  const stations = data?.stations ?? []

  const totalExtras = useMemo(
    () => stations.reduce((acc, s) => acc + (s.extras || 0), 0),
    [stations]
  )

  async function handleDownloadPdf() {
    if (!data) return
    setPdfBusy(true)
    try {
      await generateCampaignFailurePdf(data)
      setPdfStamp(fmtTimeNow())
    } catch (e) {
      console.error('PDF gen failed', e)
      alert('Não foi possível gerar o PDF. Tente novamente.')
    } finally {
      setPdfBusy(false)
    }
  }

  return (
    <div className="cfd-backdrop" onClick={onClose}>
      <aside className="cfd-drawer" onClick={e => e.stopPropagation()}>
        <header className="cfd-head">
          <div className="cfd-head-id">
            <span className="cfd-eyebrow">Falhas da campanha</span>
            {campaign ? (
              <>
                <h2 className="cfd-title">{campaign.client_name}</h2>
                <p className="cfd-subtitle">{campaign.name}</p>
                <p className="cfd-meta">
                  <span>{fmtDateBR(campaign.start_date)} – {fmtDateBR(campaign.end_date)}</span>
                  <span className={`cfd-meta-status cfd-meta-status--${campaign.status}`}>
                    {STATUS_LABEL[campaign.status] || campaign.status}
                  </span>
                </p>
              </>
            ) : (
              <h2 className="cfd-title">Carregando…</h2>
            )}
          </div>
          <div className="cfd-head-actions">
            <button
              className="cfd-btn cfd-btn-primary"
              onClick={handleDownloadPdf}
              disabled={!data || pdfBusy}
            >
              {pdfBusy ? (
                <>
                  <span className="cfc-pdf-spin" aria-hidden="true" />
                  Gerando…
                </>
              ) : (
                <>
                  <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
                    <path d="M7 1.5v8.5M7 10l-3-3m3 3l3-3M2.5 12.5h9"
                          stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"/>
                  </svg>
                  Baixar PDF de cobrança
                </>
              )}
            </button>
            <button className="cfd-btn cfd-btn-close" onClick={onClose} aria-label="Fechar">×</button>
          </div>
        </header>

        {campaign?.status === 'ativa' && (
          <div className="cfd-warn" role="note">
            <svg className="cfd-warn-icon" width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
              <path d="M7 4v3.5M7 9.5v.01M7 1.5L12.5 11.5h-11L7 1.5z"
                    stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"/>
            </svg>
            <span>
              Campanha ativa, bonificação calculada até <strong>{pdfStamp || fmtTimeNow()}</strong>.
              Pode mudar até o fim da campanha.
            </span>
          </div>
        )}

        {summary && (
          <div className="cfd-kpis">
            <div className="cfd-kpi">
              <span className="cfd-kpi-num">{summary.stations_with_failure}</span>
              <span className="cfd-kpi-label">Emissoras com falha</span>
            </div>
            <div className="cfd-kpi">
              <span className="cfd-kpi-num">{summary.total_failure_days}</span>
              <span className="cfd-kpi-label">Dias com falha</span>
            </div>
            <div className="cfd-kpi">
              <span className="cfd-kpi-num cfd-kpi-num-deficit">{summary.total_deficit}</span>
              <span className="cfd-kpi-label">Veiculações faltam</span>
            </div>
          </div>
        )}

        <div className="cfd-body">
          {isLoading ? (
            <p className="cfd-state">Carregando…</p>
          ) : error ? (
            <p className="cfd-state cfd-state-err">
              {error?.response?.status === 404
                ? 'Campanha não encontrada (pode ter sido cancelada).'
                : 'Erro ao carregar campanha.'}
            </p>
          ) : stations.length === 0 ? (
            <p className="cfd-state">Sem falhas registradas no momento.</p>
          ) : (
            <table className="cfd-table">
              <thead>
                <tr>
                  <th>Emissora</th>
                  <th className="cfd-num">Programado</th>
                  <th className="cfd-num">Veiculou</th>
                  <th>Dias com falha</th>
                </tr>
              </thead>
              <tbody>
                {stations.map(entry => (
                  <StationRow key={String(entry.station.id)} entry={entry} />
                ))}
              </tbody>
            </table>
          )}
          {totalExtras > 0 && (
            <p className="cfd-foot-note">
              Total de execuções extras no período: <strong>{totalExtras.toLocaleString('pt-BR')}</strong>.
            </p>
          )}
        </div>
      </aside>
    </div>
  )
}
