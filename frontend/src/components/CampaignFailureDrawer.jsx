import { useEffect, useMemo, useState } from 'react'
import { useCampaignFailureDetail } from '../api/hooks'
import { generateCampaignFailurePdf } from '../utils/pdfCampaignFailure'
import StationAvatar from './StationAvatar'
import './CampaignFailureCard.css'

function fmtDateBR(yyyymmdd) {
  if (!yyyymmdd) return '—'
  const [y, m, d] = yyyymmdd.split('-')
  return `${d}/${m}/${y}`
}

// Compact day chip — "24" if same month, "24/05" if crossing months.
function dayChipLabel(allDays, day) {
  const monthSet = new Set(allDays.map(d => d.slice(0, 7)))
  if (monthSet.size === 1) return day.slice(8, 10)
  return `${day.slice(8, 10)}/${day.slice(5, 7)}`
}

function DaysChips({ days }) {
  if (!days?.length) return null
  return (
    <div className="cfd-day-chips">
      {days.map(d => (
        <span key={d} className="cfd-day-chip" title={fmtDateBR(d)}>
          {dayChipLabel(days, d)}
        </span>
      ))}
    </div>
  )
}

function StationRow({ entry }) {
  const { station: s, programmed, identified, deficit, extras, is_bonified, failure_days } = entry
  const pct = programmed > 0 ? Math.round((identified / programmed) * 100) : 0
  return (
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
        <DaysChips days={failure_days || []} />
        {is_bonified && (
          <div className="cfd-bonif-note">Bonificada — extras: {extras}</div>
        )}
      </td>
    </tr>
  )
}

export default function CampaignFailureDrawer({ campaignId, onClose }) {
  const { data, isLoading, error } = useCampaignFailureDetail(campaignId)
  const [pdfBusy, setPdfBusy] = useState(false)

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
                  {fmtDateBR(campaign.start_date)} – {fmtDateBR(campaign.end_date)}
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
              {pdfBusy ? 'Gerando…' : 'Baixar PDF de cobrança'}
            </button>
            <button className="cfd-btn cfd-btn-close" onClick={onClose} aria-label="Fechar">×</button>
          </div>
        </header>

        {campaign?.status === 'ativa' && (
          <div className="cfd-warn">
            Campanha ainda ativa — bonificação considera execuções extras
            até agora. Pode mudar até o fim da campanha.
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
              <span className="cfd-kpi-label">Déficit total</span>
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
              Total de execuções extras no período: <strong>{totalExtras}</strong>.
            </p>
          )}
        </div>
      </aside>
    </div>
  )
}
