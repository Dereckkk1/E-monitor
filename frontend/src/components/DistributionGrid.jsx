import { Fragment } from 'react'
import DayCell from './DayCell'
import TypeIconPill from './TypeIconPill'
import StationAvatar from './StationAvatar'

/**
 * Grid of station × material × day with distribution badges.
 *
 * Modes:
 *  - "edit"  → cells clickable, opens override popover (handler decides)
 *  - "view"  → cells clickable, opens day detail modal (Plan 3 will wire this)
 *
 * Data props:
 *  - month: Date (first of month being displayed)
 *  - campaignStart: ISO string
 *  - campaignEnd: ISO string
 *  - stations: Array<{id, name, frequency_mhz, city, ...}>
 *  - rows: Array<{
 *      stationId: uuid,
 *      materialId: uuid,
 *      materialTitle: string,
 *      typeColor: string,
 *      ruleSummary: string | null,
 *      extraRules: number
 *    }>
 *  - cellData: Map<key, {expected, in_slot, deficit, bonus, out_slot, out_date, hasOverride}>
 *               where key = `${stationId}|${materialId}|${dateISO}`
 *  - onCellClick?: (stationId, materialId, dateISO, rect) => void
 *  - mode: "edit" | "view"
 */
export default function DistributionGrid({
  month, campaignStart, campaignEnd, stations, rows, cellData,
  onCellClick, onStationClick, mode = 'edit',
}) {
  const year = month.getFullYear()
  const monthIdx = month.getMonth()
  const daysInMonth = new Date(year, monthIdx + 1, 0).getDate()
  const days = Array.from({ length: daysInMonth }, (_, i) => new Date(year, monthIdx, i + 1))
  const dayNames = ['DOM','SEG','TER','QUA','QUI','SEX','SÁB']

  const cStart = new Date(campaignStart)
  const cEnd = new Date(campaignEnd)
  const today = new Date()
  today.setHours(0,0,0,0)

  // Group rows by station for rendering
  const byStation = new Map()
  for (const r of rows) {
    if (!byStation.has(r.stationId)) byStation.set(r.stationId, [])
    byStation.get(r.stationId).push(r)
  }

  const gridTemplate = `220px 130px repeat(${daysInMonth}, 64px)`

  return (
    <div style={{ overflowX: 'auto', background: '#fff', borderTop: '1px solid #f1f5f9' }}>
      <div style={{ display: 'grid', gridTemplateColumns: gridTemplate, fontSize: 12, minWidth: 'fit-content' }}>

        {/* Header row */}
        <div style={headStation}>Emissora / Material</div>
        <div style={head}>Regra</div>
        {days.map((d, i) => {
          const wkd = d.getDay() === 0 || d.getDay() === 6
          const isToday = d.toDateString() === today.toDateString()
          return (
            <div key={`hd-${i}`} style={{
              ...head,
              color: wkd ? '#cbd5e1' : isToday ? '#E81E75' : '#64748b',
              background: isToday ? '#fdf2f8' : wkd ? '#f8fafc' : '#fafbfc',
            }}>
              <span style={{ display: 'block', fontSize: 9 }}>{dayNames[d.getDay()]}</span>
              <span style={{ display: 'block', fontSize: 13, color: '#0f172a', fontWeight: 700, marginTop: 2 }}>
                {String(d.getDate()).padStart(2, '0')}
              </span>
            </div>
          )
        })}

        {/* Station blocks + material rows */}
        {[...byStation.entries()].map(([stationId, stationRows]) => {
          const station = stations.find(s => s.id === stationId)
          if (!station) return null
          return (
            <Fragment key={`sb-${stationId}`}>
              {/* Station header (full-width) */}
              <div style={{
                gridColumn: '1 / -1', background: '#fff', borderBottom: '1px solid #e2e8f0',
                padding: '11px 14px', display: 'flex', alignItems: 'center', gap: 10,
                cursor: onStationClick ? 'pointer' : 'default',
              }} onClick={onStationClick ? () => onStationClick(station.id) : undefined}>
                <StationAvatar station={station} size={30} />
                <div>
                  <span style={{ fontWeight: 600, color: '#0f172a' }}>{station.name}</span>
                  <span style={{ color: '#64748b', fontSize: 11, marginLeft: 6 }}>
                    {station.band} {station.frequency_mhz ?? ''} · {station.city ?? ''}
                  </span>
                </div>
              </div>

              {stationRows.map(row => (
                <Fragment key={`row-${stationId}-${row.materialId}`}>
                  <div style={{
                    padding: '11px 14px 11px 24px', background: '#fafbfc', color: '#334155',
                    borderBottom: '1px solid #f1f5f9', borderRight: '1px solid #f1f5f9',
                    display: 'flex', alignItems: 'center', gap: 8, fontSize: 12, fontWeight: 500,
                  }}>
                    <TypeIconPill color={row.typeColor} />
                    {row.materialTitle}
                  </div>
                  <div style={{
                    padding: '8px 8px', background: '#fafbfc', color: '#be185d',
                    borderBottom: '1px solid #f1f5f9', borderRight: '1px solid #f1f5f9',
                    fontSize: 10, fontWeight: 600, lineHeight: 1.3,
                    display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 2,
                  }}>
                    {row.ruleSummary || <span style={{ color: '#94a3b8' }}>—</span>}
                    {row.extraRules > 0 && (
                      <span style={{ fontSize: 9, color: '#64748b', fontWeight: 700 }}>+{row.extraRules} regra</span>
                    )}
                  </div>
                  {days.map((d, i) => {
                    const dateISO = d.toISOString().slice(0, 10)
                    const key = `${row.stationId}|${row.materialId}|${dateISO}`
                    const cell = cellData.get(key) ?? {}
                    const isWeekend = d.getDay() === 0 || d.getDay() === 6
                    const isOutsideRange = d < cStart || d > cEnd
                    return (
                      <DayCell
                        key={`cell-${row.stationId}-${row.materialId}-${i}`}
                        expected={cell.expected}
                        inSlot={cell.in_slot}
                        deficit={cell.deficit}
                        bonus={cell.bonus}
                        outSlot={cell.out_slot}
                        outDate={cell.out_date}
                        isWeekend={isWeekend}
                        isOutsideRange={isOutsideRange}
                        hasOverride={!!cell.hasOverride}
                        onClick={(e) => onCellClick?.(row.stationId, row.materialId, dateISO, e.currentTarget.getBoundingClientRect())}
                        hint={`${row.materialTitle} · ${dateISO}`}
                      />
                    )
                  })}
                </Fragment>
              ))}
            </Fragment>
          )
        })}

      </div>
    </div>
  )
}

const head = {
  background: '#fafbfc',
  borderBottom: '2px solid #e2e8f0',
  borderRight: '1px solid #f1f5f9',
  padding: '9px 6px',
  fontSize: 10,
  fontWeight: 600,
  textAlign: 'center',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  position: 'sticky',
  top: 0,
}

const headStation = { ...head, textAlign: 'left', paddingLeft: 14, textTransform: 'none', letterSpacing: 0, fontSize: 11 }
