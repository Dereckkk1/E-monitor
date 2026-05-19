import { useMemo } from 'react'
import StationAvatar from './StationAvatar'
import CampaignDeficitChip from './CampaignDeficitChip'
import './StationFailureCard.css'

function fmtDuration(sec) {
  if (!sec || sec < 60) return `${sec || 0}s`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}min`
  const h = Math.floor(min / 60)
  const rem = min - h * 60
  return rem > 0 ? `${h}h ${rem}min` : `${h}h`
}

// 5-block severity bar. Thresholds calibrated against typical outage sizes:
//   <1min  → 1 block
//   1-5    → 2
//   5-30   → 3
//   30-120 → 4
//   120+   → 5
function severityLevel(sec) {
  if (sec >= 7200) return 5
  if (sec >= 1800) return 4
  if (sec >= 300)  return 3
  if (sec >= 60)   return 2
  return 1
}
function severityTone(level) {
  if (level >= 4) return 'crit'
  if (level >= 3) return 'warn'
  return 'mild'
}

function SeverityBar({ sec }) {
  const level = severityLevel(sec)
  const tone  = severityTone(level)
  return (
    <span className={`sfc-sev sfc-sev-${tone}`} aria-label={`Severidade ${level} de 5`}>
      <span className="sfc-sev-blocks" aria-hidden="true">
        {[1, 2, 3, 4, 5].map(i => (
          <span key={i} className={'sfc-sev-block' + (i <= level ? ' on' : '')} />
        ))}
      </span>
      <span className="sfc-sev-value">{fmtDuration(sec)} fora</span>
    </span>
  )
}

// Parse YYYY-MM-DD as local-date — same helper as the page.
function parseLocalDate(iso) {
  if (!iso) return null
  const [y, m, d] = iso.split('-').map(Number)
  return new Date(y, m - 1, d)
}

function IncidentRibbon({ incidents, dateIso }) {
  const ranges = useMemo(() => {
    const day = parseLocalDate(dateIso)
    if (!day) return []
    const dayStart = day.getTime()
    const dayEnd   = dayStart + 86_400_000
    const out = []
    for (const inc of incidents || []) {
      const t0 = new Date(inc.event_at).getTime()
      const t1 = t0 + (inc.duration_seconds || 0) * 1000
      const a = Math.max(t0, dayStart)
      const b = Math.min(t1, dayEnd)
      if (b <= a) continue
      out.push({
        from: (a - dayStart) / 60_000,
        to:   (b - dayStart) / 60_000,
      })
    }
    return out
  }, [incidents, dateIso])

  if (ranges.length === 0) return null

  return (
    <div className="sfc-ribbon" aria-hidden="true">
      <svg className="sfc-ribbon-svg" viewBox="0 0 1440 8" preserveAspectRatio="none">
        <line x1="0" y1="4" x2="1440" y2="4" className="sfc-ribbon-axis" />
        {[6, 12, 18].map(h => (
          <line key={h} x1={h * 60} y1="1" x2={h * 60} y2="7" className="sfc-ribbon-grid" />
        ))}
        {ranges.map((r, i) => (
          <rect
            key={i}
            x={r.from}
            y="1"
            width={Math.max(r.to - r.from, 2)}
            height="6"
            rx="1"
            className="sfc-ribbon-mark"
          />
        ))}
      </svg>
    </div>
  )
}

export default function StationFailureCard({ data, rank, dateIso }) {
  const { station, incidents, total_down_seconds, has_silent_gap, campaigns } = data

  return (
    <article className="sfc">
      <div className="sfc-head">
        <span className="sfc-rank" aria-hidden="true">
          {String(rank ?? 1).padStart(2, '0')}
        </span>

        <div className="sfc-logo">
          <StationAvatar station={station} size={44} />
        </div>

        <div className="sfc-identity">
          <h3 className="sfc-name" title={station.name}>{station.name}</h3>
          <p className="sfc-meta">
            {station.dial || '—'}{station.city ? ` · ${station.city}` : ''}
          </p>
        </div>

        <SeverityBar sec={total_down_seconds} />
      </div>

      <IncidentRibbon incidents={incidents} dateIso={dateIso} />

      <p className="sfc-line">
        <span>
          {incidents.length} {incidents.length === 1 ? 'incidente' : 'incidentes'}
        </span>
        {has_silent_gap && (
          <>
            <span className="sfc-line-sep">·</span>
            <span className="sfc-silent">silent-gap detectado</span>
          </>
        )}
        {campaigns.length > 0 && (
          <>
            <span className="sfc-line-sep">·</span>
            <span>
              {campaigns.length} {campaigns.length === 1 ? 'campanha' : 'campanhas'} afetada{campaigns.length === 1 ? '' : 's'}
            </span>
          </>
        )}
      </p>

      {campaigns.length > 0 && (
        <ul className="sfc-camps">
          {campaigns.map(c => (
            <li key={c.campaign_id}>
              <CampaignDeficitChip data={c} />
            </li>
          ))}
        </ul>
      )}

      {campaigns.length === 0 && (
        <p className="sfc-empty-camps">
          <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
            <path d="M3 6.5l2 2 4-5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"/>
          </svg>
          Nenhuma campanha tinha slot nessa janela
        </p>
      )}
    </article>
  )
}
