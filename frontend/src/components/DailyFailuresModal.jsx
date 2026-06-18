import { useEffect } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../contexts/AuthContext'
import { useDailyFailuresDigest, useAckDailyFailuresDigest } from '../api/hooks'
import ClientAvatar from './ClientAvatar'
import './DailyFailuresModal.css'

// 'YYYY-MM-DD' → 'DD/MM'. Sem usar Date pra evitar drift de fuso.
function formatDayBR(iso) {
  const parts = (iso || '').split('-')
  if (parts.length !== 3) return ''
  return `${parts[2]}/${parts[1]}`
}

function plural(n, singular, pluralForm) {
  return n === 1 ? singular : pluralForm
}

// Modal admin-only de resumo das falhas de ontem. Aparece 1x/dia/usuário
// no primeiro load (gatilho global). Marca "visto" só ao interagir
// (fechar / ESC / backdrop / clicar em "Ver falhas").
export default function DailyFailuresModal() {
  const { isAdmin } = useAuth()
  const { data } = useDailyFailuresDigest({ enabled: isAdmin })
  const ack = useAckDailyFailuresDigest()
  const navigate = useNavigate()

  const open = !!(isAdmin && data && !data.seen && data.campaigns?.length > 0)

  useEffect(() => {
    if (!open) return undefined
    function onKey(e) {
      if (e.key === 'Escape') ack.mutate()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // ack é estável (mutation do React Query); incluir `open` basta.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  if (!open) return null

  function dismiss() {
    ack.mutate()
  }

  function goToFailures() {
    ack.mutate()
    navigate(`/admin/station-failures?view=by_campaign&date=${data.date}`)
  }

  const { summary, campaigns } = data

  return createPortal(
    <div className="confirm-backdrop" onClick={dismiss} role="dialog" aria-modal="true">
      <div className="dfm-card" onClick={e => e.stopPropagation()}>
        <div className="dfm-header">
          <p className="confirm-title">Falhas de ontem ({formatDayBR(data.date)})</p>
          <p className="confirm-message">
            {summary.campaigns} {plural(summary.campaigns, 'campanha', 'campanhas')}
            {' · '}
            {summary.stations} {plural(summary.stations, 'emissora', 'emissoras')} com falha
          </p>
        </div>

        <ul className="dfm-list">
          {campaigns.map(c => (
            <li key={c.id} className="dfm-item">
              <ClientAvatar
                client={{ client_name: c.client_name, client_logo_url: c.client_logo_url }}
                size={32}
              />
              <div className="dfm-item-text">
                <span className="dfm-item-name">{c.name}</span>
                <span className="dfm-item-client">{c.client_name}</span>
              </div>
              <span className="dfm-badge">
                {c.stations_failed} {plural(c.stations_failed, 'emissora', 'emissoras')}
              </span>
            </li>
          ))}
        </ul>

        <div className="confirm-actions">
          <button className="btn btn-secondary btn-sm" onClick={dismiss}>
            Fechar
          </button>
          <button className="btn btn-primary btn-sm" onClick={goToFailures}>
            Ver falhas
          </button>
        </div>
      </div>
    </div>,
    document.body
  )
}
