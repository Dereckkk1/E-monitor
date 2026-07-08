// Parse a date-like value into a Date anchored at LOCAL midnight of the
// stated calendar day. Handles the two shapes the API returns:
//
//   "2026-05-14"               (Postgres DATE serialized as ISO date)
//   "2026-05-14T00:00:00Z"     (Postgres DATE serialized as full ISO at UTC)
//
// The native `new Date(str)` interprets both as UTC midnight. In timezones
// west of Greenwich (e.g. America/Sao_Paulo, UTC-3) that becomes 21:00 local
// of the PREVIOUS day, and a downstream setHours(0,0,0,0) then snaps to
// midnight of that wrong day — visible in the UI as "schedule starts a day
// before it should". This helper extracts the Y-M-D portion and constructs
// a local-midnight Date, sidestepping the timezone shift.
//
// Pass-through behavior:
//   - Date instances are returned as-is (callers can rely on identity).
//   - Anything else returns an invalid Date so the caller can guard.
export function parseLocalDate(value) {
  if (value == null || value === '') return new Date(NaN)
  if (value instanceof Date) return value
  const s = String(value).slice(0, 10)
  if (!/^\d{4}-\d{2}-\d{2}$/.test(s)) return new Date(NaN)
  const [y, m, d] = s.split('-').map(Number)
  return new Date(y, m - 1, d, 0, 0, 0, 0)
}

// ── Month / range helpers (shared by /detections and /materials) ──
// Extracted verbatim from DetectionsPage so both pages share one source.

export function pad2(n) { return String(n).padStart(2, '0') }

export function monthFromDate(d) {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}`
}

export function isoFromDate(d) {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`
}

export function monthToRange(ymStr) {
  const [y, m] = ymStr.split('-').map(Number)
  const start = new Date(y, m - 1, 1, 0, 0, 0, 0)
  const end   = new Date(y, m, 0, 23, 59, 59, 999)
  return { start, end }
}

export function monthLabel(ymStr) {
  const [y, m] = ymStr.split('-').map(Number)
  return new Date(y, m - 1, 1).toLocaleDateString('pt-BR', { month: 'long', year: 'numeric' })
}

export function rangeLabel(startISO, endISO) {
  if (!startISO || !endISO) return ''
  const a = new Date(`${startISO}T00:00:00`)
  const b = new Date(`${endISO}T00:00:00`)
  const sameMonth = a.getMonth() === b.getMonth() && a.getFullYear() === b.getFullYear()
  const monthShort = a.toLocaleDateString('pt-BR', { month: 'short' }).replace('.', '')
  const monthShortB = b.toLocaleDateString('pt-BR', { month: 'short' }).replace('.', '')
  if (sameMonth) {
    return a.getDate() === b.getDate()
      ? `${a.getDate()} de ${monthShort}`
      : `${a.getDate()}–${b.getDate()} ${monthShort}`
  }
  return `${a.getDate()} ${monthShort} – ${b.getDate()} ${monthShortB}`
}

// Intersection of (month range) ∩ (campaign range), returned as ISO date strings.
export function defaultRangeForCampaign(ymStr, campaign) {
  if (!ymStr || !campaign?.start_date || !campaign?.end_date) return { start: '', end: '' }
  const { start: monthStart, end: monthEnd } = monthToRange(ymStr)
  const cStart = parseLocalDate(campaign.start_date)
  const cEnd   = parseLocalDate(campaign.end_date)
  cEnd.setHours(23, 59, 59, 999)
  const start = cStart > monthStart ? cStart : monthStart
  const end   = cEnd   < monthEnd   ? cEnd   : monthEnd
  if (start > end) return { start: '', end: '' }
  return { start: isoFromDate(start), end: isoFromDate(end) }
}

// Full campaign span [start_date, end_date] as ISO YYYY-MM-DD strings. Unlike
// defaultRangeForCampaign (which intersects with a single month), this returns
// the WHOLE campaign — used as the min/max bounds of the date pickers so the
// user can extend the range across months. Empty strings when the campaign has
// no dates (bounds then fall back to unrestricted).
export function campaignRangeISO(campaign) {
  if (!campaign?.start_date || !campaign?.end_date) return { start: '', end: '' }
  const s = parseLocalDate(campaign.start_date)
  const e = parseLocalDate(campaign.end_date)
  if (isNaN(s.getTime()) || isNaN(e.getTime())) return { start: '', end: '' }
  return { start: isoFromDate(s), end: isoFromDate(e) }
}

// Local-midnight "today". Extracted so DistributionGrid and the /detections
// report compute the exact same "cap at today" cutoff.
export function startOfLocalToday() {
  const t = new Date()
  t.setHours(0, 0, 0, 0)
  return t
}

// Enumerate the visible day columns EXACTLY like DistributionGrid does, so the
// /detections report and the grid can never drift on which days are shown.
//
//   - visibleStart/visibleEnd set (/detections, /materials): render EXACTLY
//     that span (may cross months). It's already the intersection with the
//     campaign done upstream on the page.
//   - otherwise (wizard): month ∩ [campaignStart, campaignEnd].
//   - capAtToday (default true): never past today — future days have no real
//     data and would show up as phantom déficit.
//
// `today` is injectable for testing; defaults to local midnight now. Returns an
// array of local-midnight Date objects (possibly empty).
export function enumerateVisibleDays({
  month, campaignStart, campaignEnd,
  visibleStart, visibleEnd, capAtToday = true, today,
}) {
  const cutoff = today ?? startOfLocalToday()
  let firstVisible, lastVisible
  if (visibleStart && visibleEnd) {
    firstVisible = parseLocalDate(visibleStart)
    lastVisible  = parseLocalDate(visibleEnd)
  } else {
    const y = month.getFullYear()
    const m = month.getMonth()
    firstVisible = new Date(y, m, 1, 0, 0, 0, 0)
    lastVisible  = new Date(y, m + 1, 0, 0, 0, 0, 0)
    const cStart = parseLocalDate(campaignStart)
    const cEnd   = parseLocalDate(campaignEnd)
    if (cStart > firstVisible) firstVisible = cStart
    if (cEnd   < lastVisible)  lastVisible  = cEnd
  }
  if (capAtToday && cutoff < lastVisible) lastVisible = cutoff
  const days = []
  if (firstVisible <= lastVisible) {
    const cur = new Date(firstVisible)
    while (cur <= lastVisible) {
      days.push(new Date(cur))
      cur.setDate(cur.getDate() + 1)
    }
  }
  return days
}

// Format a campaign's [start_date, end_date] as pt-BR "dd-mm-aaaa – dd-mm-aaaa".
export function formatCampaignPeriod(startISO, endISO) {
  if (!startISO || !endISO) return ''
  const a = parseLocalDate(startISO)
  const b = parseLocalDate(endISO)
  if (isNaN(a.getTime()) || isNaN(b.getTime())) return ''
  const fmt = d => `${pad2(d.getDate())}-${pad2(d.getMonth() + 1)}-${d.getFullYear()}`
  return `${fmt(a)} – ${fmt(b)}`
}
