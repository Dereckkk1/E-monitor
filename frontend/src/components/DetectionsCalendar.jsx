import StationAvatar from './StationAvatar'
import {
  bucketDetections,
  daysBetween,
  countFor,
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
                  const count = countFor(buckets, station.id, dk)
                  if (count === 0) {
                    return <div key={dk} className="calendar-cell calendar-cell-empty">—</div>
                  }
                  return (
                    <button
                      key={dk}
                      type="button"
                      className="calendar-cell calendar-cell-hit"
                      onClick={() => onCellClick(station, dk)}
                      aria-label={`${primary}: ${count} veiculações em ${formatShortDay(dk)}`}
                    >
                      <span className="detect-badge detect-badge--air">{count}</span>
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
