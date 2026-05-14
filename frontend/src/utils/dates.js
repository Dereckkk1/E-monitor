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
