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
  onCellClick, onStationClick, onCellIncrement, onCellDecrement, mode = 'edit',
  // Pricing keyed por station_id. Cada entrada:
  //   { mode: 'consolidated' | 'per_insertion',
  //     consolidated_value?: number,
  //     per_type?: [{type_id, unit_value}] }
  // Quando vazio/null, o resumo da direita mostra "R$ —" igual antes.
  pricingByStation = {},
  // capAtToday: true (default) corta a grid em "hoje" — esperado em telas de
  // monitoramento (/detections) onde dias futuros ainda não têm dado real.
  // false mostra a campanha inteira até o end_date — esperado em telas de
  // CONFIGURAÇÃO (wizard step de distribuição) onde o operador precisa
  // planejar plays nos dias que ainda não chegaram.
  capAtToday = true,
}) {
  const year = month.getFullYear()
  const monthIdx = month.getMonth()
  const dayNames = ['DOM','SEG','TER','QUA','QUI','SEX','SÁB']

  const cStart = new Date(campaignStart)
  const cEnd = new Date(campaignEnd)
  cStart.setHours(0, 0, 0, 0)
  cEnd.setHours(0, 0, 0, 0)
  const today = new Date()
  today.setHours(0,0,0,0)

  // Visible day range = month ∩ campaign [∩ [-∞, today] se capAtToday]. Antes
  // começava sempre no dia 1 do mês mesmo quando a campanha começava no meio
  // dele — a grid ficava com 13 dias hatched antes do primeiro útil. Agora
  // arrancamos no primeiro dia útil da campanha dentro do mês.
  const monthFirst = new Date(year, monthIdx, 1, 0, 0, 0, 0)
  const monthLast  = new Date(year, monthIdx + 1, 0, 0, 0, 0, 0)
  let firstVisible = monthFirst
  let lastVisible  = monthLast
  if (cStart > firstVisible) firstVisible = cStart
  if (cEnd   < lastVisible)  lastVisible  = cEnd
  if (capAtToday && today < lastVisible) lastVisible = today
  const days = []
  if (firstVisible <= lastVisible) {
    const cur = new Date(firstVisible)
    while (cur <= lastVisible) {
      days.push(new Date(cur))
      cur.setDate(cur.getDate() + 1)
    }
  }

  // Group rows by station for rendering
  const byStation = new Map()
  for (const r of rows) {
    if (!byStation.has(r.stationId)) byStation.set(r.stationId, [])
    byStation.get(r.stationId).push(r)
  }

  // Layout do resumo dividido em DUAS colunas:
  //   200px → "row summary"  : badges (programada/veiculada/bônus/etc) + count
  //                            chip. Uma por material row.
  //   180px → "station total": pills de impactos + valor (+ bônus em R$).
  //                            UMA por estação, com grid-row span pra ocupar
  //                            todas as material rows do bloco.
  // Ambas sticky-right pra ficarem ancoradas durante scroll horizontal:
  //   - station total: right: 0
  //   - row summary:   right: 180px (logo à esquerda do station total)
  // O spacer 1fr antes garante que o resumo encoste na borda direita mesmo
  // com poucos dias visíveis.
  const ROW_SUMMARY_W = 200
  const STATION_TOTAL_W = 180
  const gridTemplate = `220px repeat(${days.length}, 88px) 1fr ${ROW_SUMMARY_W}px ${STATION_TOTAL_W}px`

  return (
    <div style={{ overflowX: 'auto', background: '#fff', borderTop: '1px solid #f1f5f9' }}>
      <div style={{ display: 'grid', gridTemplateColumns: gridTemplate, fontSize: 12, minWidth: 'fit-content' }}>

        {/* Header row */}
        <div style={headStation}>Emissora / Material</div>
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
        {/* Header de Resumo: span ambas as colunas (-3 a -1) com sticky-right.
            Visualmente lê "Resumo" cobrindo a área inteira. */}
        <div style={{ ...headSummary, gridColumn: '-3 / -1' }}>Resumo</div>

        {/* Station blocks + material rows */}
        {[...byStation.entries()].map(([stationId, stationRows]) => {
          const station = stations.find(s => s.id === stationId)
          if (!station) return null
          return (
            <Fragment key={`sb-${stationId}`}>
              {/* Station header (full-width) — inner content sticks to the
                  left so the station name stays visible while the user scrolls
                  the day columns horizontally. */}
              <div style={{
                gridColumn: '1 / -1', background: '#fff', borderBottom: '1px solid #e2e8f0',
                cursor: onStationClick ? 'pointer' : 'default',
              }} onClick={onStationClick ? () => onStationClick(station.id) : undefined}>
                <div style={{
                  position: 'sticky', left: 0,
                  width: 'fit-content',
                  padding: '11px 14px',
                  display: 'flex', alignItems: 'center', gap: 10,
                  background: '#fff',
                }}>
                  <StationAvatar station={station} size={30} />
                  <div>
                    <span style={{ fontWeight: 600, color: '#0f172a' }}>{station.name}</span>
                    <span style={{ color: '#64748b', fontSize: 11, marginLeft: 6 }}>
                      {station.band} {station.frequency_mhz ?? ''} · {station.city ?? ''}
                    </span>
                  </div>
                </div>
              </div>

              {stationRows.map((row, ri) => (
                <Fragment key={`row-${stationId}-${row.materialId}`}>
                  <div style={{
                    padding: '11px 14px 11px 24px', background: '#fafbfc', color: '#334155',
                    borderBottom: '1px solid #f1f5f9', borderRight: '1px solid #f1f5f9',
                    display: 'flex', alignItems: 'center', gap: 8, fontSize: 12, fontWeight: 500,
                    position: 'sticky', left: 0, zIndex: 2,
                  }}>
                    <TypeIconPill color={row.typeColor} />
                    {row.materialTitle}
                  </div>
                  {days.map((d, i) => {
                    const dateISO = d.toISOString().slice(0, 10)
                    const key = `${row.stationId}|${row.materialId}|${dateISO}`
                    const cell = cellData.get(key) ?? {}
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
                        isOutsideRange={isOutsideRange}
                        hasOverride={!!cell.hasOverride}
                        hasPendingDraft={!!cell.hasPendingDraft}
                        onClick={(e) => onCellClick?.(row.stationId, row.materialId, dateISO, e.currentTarget.getBoundingClientRect())}
                        onIncrement={onCellIncrement ? () => onCellIncrement(row.stationId, row.materialId, dateISO, cell.expected ?? 0) : undefined}
                        onDecrement={onCellDecrement ? () => onCellDecrement(row.stationId, row.materialId, dateISO, cell.expected ?? 0) : undefined}
                        hint={`${row.materialTitle} · ${dateISO}`}
                      />
                    )
                  })}
                  <RowSummaryCell
                    row={row}
                    days={days}
                    cellData={cellData}
                    stationTotalWidth={STATION_TOTAL_W}
                  />
                  {ri === 0 && (
                    <StationTotalCell
                      rows={stationRows}
                      days={days}
                      cellData={cellData}
                      pricing={pricingByStation[row.stationId] ?? null}
                      pmm={Number(station.pmm) || 0}
                    />
                  )}
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
  zIndex: 1,
}

const headStation = {
  ...head,
  textAlign: 'left',
  paddingLeft: 14,
  textTransform: 'none',
  letterSpacing: 0,
  fontSize: 11,
  left: 0,
  zIndex: 3,
}

const headSummary = {
  ...head,
  borderLeft: '2px solid #e2e8f0',
  borderRight: 'none',
  textAlign: 'left',
  paddingLeft: 14,
  fontSize: 11,
  textTransform: 'none',
  letterSpacing: 0,
  right: 0,
  zIndex: 3,
}

// Per-MATERIAL row summary: 6 mini-pills (programada/veiculada/bônus/déficit
// /fora-faixa/fora-data) + count chip. Os valores em R$ não vivem mais aqui —
// agora consolidados por estação no <StationTotalCell />.
//
// Sticky-right com offset = STATION_TOTAL_W pra ficar logo à esquerda das pills
// da estação ao rolar horizontalmente.
function RowSummaryCell({ row, days, cellData, stationTotalWidth }) {
  let expected = 0, inSlot = 0, deficit = 0, bonus = 0, outSlot = 0, outDate = 0
  for (const d of days) {
    const dateISO = d.toISOString().slice(0, 10)
    const c = cellData.get(`${row.stationId}|${row.materialId}|${dateISO}`) ?? {}
    expected += c.expected ?? 0
    inSlot   += c.in_slot  ?? 0
    deficit  += c.deficit  ?? 0
    bonus    += c.bonus    ?? 0
    outSlot  += c.out_slot ?? 0
    outDate  += c.out_date ?? 0
  }

  return (
    <div style={{
      gridColumn: '-3 / -2',
      borderBottom: '1px solid #f1f5f9',
      borderLeft: '2px solid #e2e8f0',
      background: '#fff',
      padding: '8px 10px',
      display: 'flex',
      alignItems: 'center',
      gap: 10,
      minHeight: 44,
      position: 'sticky',
      right: stationTotalWidth,
      zIndex: 2,
    }}>
      <div style={{ display: 'flex', gap: 3, alignItems: 'center', flex: 1, minWidth: 0 }}>
        <SumPill variant="dark"   value={expected} />
        <SumPill variant="green"  value={inSlot} dim={inSlot === 0} />
        <SumPill variant="blue"   value={bonus}   prefix="+" dim={bonus === 0} />
        <SumPill variant="red"    value={deficit} prefix="-" dim={deficit === 0} />
        <SumPill variant="yellow" value={outSlot} prefix="+" dim={outSlot === 0} />
        <SumPill variant="purple" value={outDate} prefix="+" dim={outDate === 0} />
      </div>
    </div>
  )
}

// Total CONSOLIDADO por estação: impactos + valor (+ bônus em R$). Renderizado
// uma vez por bloco de estação, com grid-row span igual à quantidade de
// material rows desse bloco — visualmente as pills ficam centradas verticalmente
// no bloco da estação. Sticky-right a 0 (encostado na borda).
//
// Cálculo:
//   • Impactos    = pmm × Σ in_slot (somando todos os materiais da emissora)
//   • Valor:
//       - consolidated  → consolidated_value (não depende das plays)
//       - per_insertion → Σ (unit_value_tipo × in_slot_tipo) por tipo
//   • Bônus em R$ (só per_insertion) → Σ (unit_value × bonus_tipo)
function StationTotalCell({ rows, days, cellData, pricing, pmm }) {
  // Agrega in_slot/bonus por material row e por type.
  let inSlotStation = 0, bonusStation = 0
  let valor = null, valorBonus = null, isConsolidated = false

  if (pricing?.mode === 'consolidated') {
    valor = Number(pricing.consolidated_value) || 0
    isConsolidated = true
  }
  const perType = pricing?.mode === 'per_insertion'
    ? Object.fromEntries((pricing.per_type ?? []).map(t => [t.type_id, Number(t.unit_value) || 0]))
    : null

  for (const row of rows) {
    let rowInSlot = 0, rowBonus = 0
    for (const d of days) {
      const dateISO = d.toISOString().slice(0, 10)
      const c = cellData.get(`${row.stationId}|${row.materialId}|${dateISO}`) ?? {}
      rowInSlot += c.in_slot ?? 0
      rowBonus  += c.bonus  ?? 0
    }
    inSlotStation += rowInSlot
    bonusStation  += rowBonus
    if (perType) {
      const unit = perType[row.materialId] ?? 0
      valor = (valor ?? 0) + unit * rowInSlot
      if (rowBonus > 0) valorBonus = (valorBonus ?? 0) + unit * rowBonus
    }
  }

  const impactos = pmm > 0 ? pmm * inSlotStation : null

  return (
    <div style={{
      gridColumn: '-2 / -1',
      gridRow: `span ${rows.length}`,
      borderBottom: '1px solid #f1f5f9',
      borderLeft: '1px solid #f1f5f9',
      background: '#fff',
      padding: '10px 12px',
      display: 'flex',
      flexDirection: 'column',
      alignItems: 'stretch',
      justifyContent: 'center',
      gap: 6,
      position: 'sticky',
      right: 0,
      zIndex: 2,
    }}>
      <ValuePill
        tone="pink"
        icon={<IconHeadset />}
        label={impactos != null ? fmtImpactos(impactos) : '—'}
        hint={impactos != null
          ? `${fmtInt(impactos)} impactos = PMM ${fmtInt(pmm)} × ${inSlotStation} veiculações na estação`
          : 'PMM não cadastrado pra essa emissora'}
      />
      <ValuePill
        tone="green"
        icon={<IconCash />}
        label={valor != null ? fmtBRL(valor) : 'R$ —'}
        hint={valor == null ? 'Pricing não cadastrado'
          : isConsolidated ? 'Valor consolidado da emissora na campanha'
          : `Σ (valor unitário × veiculações) por tipo · ${inSlotStation} inserções`}
      />
      {valorBonus != null && valorBonus > 0 && (
        <ValuePill
          tone="blue"
          icon={<IconBonus />}
          label={fmtBRL(valorBonus)}
          hint={`Bonificação total: ${bonusStation} inserções × valor unitário`}
        />
      )}
    </div>
  )
}

const BRL_FMT = new Intl.NumberFormat('pt-BR', {
  style: 'currency', currency: 'BRL',
  minimumFractionDigits: 2, maximumFractionDigits: 2,
})
function fmtBRL(n) { return BRL_FMT.format(n) }

const INT_FMT = new Intl.NumberFormat('pt-BR', { maximumFractionDigits: 0 })
function fmtInt(n) { return INT_FMT.format(Math.round(n)) }

// Impactos com sufixo K/M pra caber no pill quando grande.
function fmtImpactos(n) {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1).replace('.', ',')}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1).replace('.', ',')}K`
  return fmtInt(n)
}

function IconBonus() {
  // Presente: caixa + laço. Sugere "bonificação" sem usar o "+" genérico.
  return (
    <svg width="11" height="11" viewBox="0 0 16 16" fill="none" aria-hidden>
      <rect x="2" y="7" width="12" height="7" rx="0.8" stroke="currentColor" strokeWidth="1.3" />
      <path d="M2 7h12V5.5a0.5 0.5 0 0 0-0.5-0.5h-11a0.5 0.5 0 0 0-0.5 0.5V7z" stroke="currentColor" strokeWidth="1.3" strokeLinejoin="round" />
      <path d="M8 5v9" stroke="currentColor" strokeWidth="1.3" />
      <path d="M5.5 5c0-1.1.9-2 2-2 .5 0 .5 2 .5 2M10.5 5c0-1.1-.9-2-2-2-.5 0-.5 2-.5 2" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
    </svg>
  )
}

const SUM_VARIANT = {
  dark:   { bg: '#0f172a', color: '#fff' },
  green:  { bg: '#dcfce7', color: '#15803d' },
  blue:   { bg: '#dbeafe', color: '#1d4ed8' },
  red:    { bg: '#fee2e2', color: '#b91c1c' },
  yellow: { bg: '#fef3c7', color: '#b45309' },
  purple: { bg: '#ede9fe', color: '#6d28d9' },
}

function SumPill({ variant, value, prefix = '', dim }) {
  const v = SUM_VARIANT[variant]
  const display = (prefix && value > 0) ? `${prefix}${value}` : String(value)
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
      minWidth: 22, height: 18, padding: '0 5px', borderRadius: 4,
      background: dim ? '#f1f5f9' : v.bg,
      color: dim ? '#cbd5e1' : v.color,
      fontSize: 10, fontWeight: 700, fontVariantNumeric: 'tabular-nums',
    }}>
      {display}
    </span>
  )
}

const VALUE_PILL_PALETTE = {
  pink:  { bg: '#fce7f3', color: '#9d174d' },
  green: { bg: '#dcfce7', color: '#166534' },
  blue:  { bg: '#dbeafe', color: '#1d4ed8' },
}

function ValuePill({ tone, icon, label, hint }) {
  const palette = VALUE_PILL_PALETTE[tone] ?? VALUE_PILL_PALETTE.green
  return (
    <span
      title={hint}
      style={{
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        gap: 6,
        padding: '3px 10px', borderRadius: 999,
        background: palette.bg, color: palette.color,
        fontSize: 10.5, fontWeight: 600, whiteSpace: 'nowrap',
      }}>
      <span style={{ display: 'flex', alignItems: 'center', opacity: 0.85 }}>{icon}</span>
      {label}
    </span>
  )
}

function IconHeadset() {
  return (
    <svg width="11" height="11" viewBox="0 0 16 16" fill="none" aria-hidden>
      <path d="M3 11V9a5 5 0 0 1 10 0v2" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      <path d="M3 11h2v3H4a1 1 0 0 1-1-1v-2zM13 11h-2v3h1a1 1 0 0 0 1-1v-2z" fill="currentColor" />
    </svg>
  )
}

function IconCash() {
  return (
    <svg width="11" height="11" viewBox="0 0 16 16" fill="none" aria-hidden>
      <rect x="2" y="4" width="12" height="8" rx="1.2" stroke="currentColor" strokeWidth="1.3" />
      <circle cx="8" cy="8" r="1.6" stroke="currentColor" strokeWidth="1.3" />
    </svg>
  )
}
