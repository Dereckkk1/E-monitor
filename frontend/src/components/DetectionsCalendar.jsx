import StationAvatar from './StationAvatar'
import {
  bucketDetections,
  daysBetween,
  countSplit,
  formatShortDay,
  weekdayOf,
  stationLabel,
} from '../pages/detections/utils'

export default function DetectionsCalendar({ stations, detections, period, onCellClick }) {
  const days = daysBetween(period.start, period.end)
  const buckets = bucketDetections(detections)

  return (
    <div className="calendar-card">
      <div className="calendar-grid">
        {/* Header row */}
        <div className="calendar-header-row">
          <div className="calendar-corner" />
          <div className="calendar-days-header">
            {days.map(dk => (
              <div key={dk} className="calendar-day-header">
                <div className="calendar-day-date">{formatShortDay(dk)}</div>
                <div className="calendar-day-weekday">{weekdayOf(dk)}</div>
              </div>
            ))}
          </div>
        </div>

        {/* Station rows */}
        {stations.map(station => {
          const { primary, secondary } = stationLabel(station)
          return (
            <div key={station.id} className="calendar-row">
              <div className="calendar-station">
                <StationAvatar station={station} size={40} />
                <div className="calendar-station-text">
                  <div className="calendar-station-name">{primary}</div>
                  <div className="calendar-station-place">{secondary}</div>
                </div>
              </div>
              <div className="calendar-cells">
                {days.map(dk => {
                  const { active, retracted } = countSplit(buckets, station.id, dk)
                  const total = active + retracted
                  if (total === 0) {
                    return <div key={dk} className="calendar-cell calendar-cell-empty">—</div>
                  }
                  // §18.2.2 — retratadas mostradas riscadas. Tooltip explica.
                  const ariaLabel =
                    retracted > 0
                      ? `${primary}: ${active} veiculações (${retracted} retratada${retracted > 1 ? 's' : ''}) em ${formatShortDay(dk)}`
                      : `${primary}: ${active} veiculações em ${formatShortDay(dk)}`
                  const title =
                    retracted > 0
                      ? `${active} ativa${active === 1 ? '' : 's'} · ${retracted} retratada${retracted === 1 ? '' : 's'} (versão maior detectada)`
                      : undefined
                  return (
                    <button
                      key={dk}
                      type="button"
                      className="calendar-cell calendar-cell-hit"
                      onClick={() => onCellClick(station, dk)}
                      aria-label={ariaLabel}
                      title={title}
                    >
                      {active > 0 && (
                        <span className="detect-badge detect-badge--air">{active}</span>
                      )}
                      {retracted > 0 && (
                        <span
                          className="detect-badge detect-badge--air"
                          style={{
                            textDecoration: 'line-through',
                            opacity: 0.55,
                            marginLeft: active > 0 ? 2 : 0,
                            fontSize: 10,
                          }}
                        >
                          {retracted}
                        </span>
                      )}
                    </button>
                  )
                })}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
