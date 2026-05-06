// frontend/src/pages/detections/utils.js
const TZ = 'America/Sao_Paulo'

// Returns YYYY-MM-DD in São Paulo time for a given Date or ISO string.
export function dayKeyOf(dateOrIso) {
  const d = dateOrIso instanceof Date ? dateOrIso : new Date(dateOrIso)
  // 'en-CA' yields YYYY-MM-DD natively, and timeZone forces SP local date.
  return d.toLocaleDateString('en-CA', { timeZone: TZ })
}

// Returns array of day keys (YYYY-MM-DD) covering [start, end] inclusive,
// stepping by one day in São Paulo local time.
export function daysBetween(start, end) {
  const startKey = dayKeyOf(start)
  const endKey   = dayKeyOf(end)
  const out = []
  // Iterate by adding 24h to a UTC date anchored at noon UTC for each day to avoid DST jumps.
  const cursor = new Date(`${startKey}T12:00:00Z`)
  const stop   = new Date(`${endKey}T12:00:00Z`)
  while (cursor <= stop) {
    out.push(dayKeyOf(cursor))
    cursor.setUTCDate(cursor.getUTCDate() + 1)
  }
  return out
}

// Buckets detections into Map<stationId, Map<dayKey, Detection[]>>.
export function bucketDetections(detections) {
  const byStation = new Map()
  for (const d of detections) {
    const key = dayKeyOf(d.detected_at)
    let perStation = byStation.get(d.station_id)
    if (!perStation) {
      perStation = new Map()
      byStation.set(d.station_id, perStation)
    }
    const list = perStation.get(key)
    if (list) list.push(d)
    else perStation.set(key, [d])
  }
  return byStation
}

// Returns 0 if station has no detections that day, else the count.
export function countFor(buckets, stationId, dayKey) {
  return buckets.get(stationId)?.get(dayKey)?.length ?? 0
}

// Returns the array of detections for the cell, sorted by detected_at ASC.
export function detectionsFor(buckets, stationId, dayKey) {
  const list = buckets.get(stationId)?.get(dayKey)
  if (!list) return []
  return [...list].sort((a, b) => new Date(a.detected_at) - new Date(b.detected_at))
}

// Formats a YYYY-MM-DD key as "DD/MM".
export function formatShortDay(dayKey) {
  const [, m, d] = dayKey.split('-')
  return `${d}/${m}`
}

// Returns 3-letter pt-BR weekday for a YYYY-MM-DD key in SP timezone.
const WEEKDAY_FMT = new Intl.DateTimeFormat('pt-BR', { weekday: 'short', timeZone: TZ })
export function weekdayOf(dayKey) {
  const d = new Date(`${dayKey}T12:00:00Z`)
  return WEEKDAY_FMT.format(d).replace('.', '').toUpperCase().slice(0, 3)
}

// Formats "DD/MM/YYYY" for the modal header.
export function formatLongDay(dayKey) {
  const [y, m, d] = dayKey.split('-')
  return `${d}/${m}/${y}`
}

// Formats "HH:MM:SS" in São Paulo time from an ISO string.
const TIME_FMT = new Intl.DateTimeFormat('pt-BR', {
  hour: '2-digit', minute: '2-digit', second: '2-digit', timeZone: TZ,
})
export function formatTimeOnly(iso) {
  return TIME_FMT.format(new Date(iso))
}

// Builds the row label for a station. Examples:
//   "Jovem Pan - FM (94.1)" / "Itajaí / SC"
//   "ACME" / "—"   (when band/freq/city/state missing)
export function stationLabel(station) {
  const base = station.band ? `${station.name} - ${station.band}` : station.name
  const withFreq = station.frequency_mhz != null
    ? `${base} (${station.frequency_mhz})`
    : base
  const place = station.city && station.state ? `${station.city} / ${station.state}` : '—'
  return { primary: withFreq, secondary: place }
}
