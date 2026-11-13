// Regras do editor de trechos (/admin/gravacoes/:id/editar). Funções puras,
// testadas em recordingEditor.test.mjs. Ver docs/features/recording-clip-editor.md.
//
// Dois eixos de tempo:
//  - posição no arquivo, em segundos (o que o player e o corte usam);
//  - hora do ar, em ms desde a época (o que a pessoa procura: "a entrevista das 14h30").
//
// O arquivo começa um pouco antes do horário marcado (pré-roll de até ~20s) e não tem
// silêncio no lugar dos buracos: depois de um buraco, a hora "anda" sem o arquivo andar.
// A conversão soma os buracos conhecidos e, por isso, é aproximada: a posição de um
// buraco tem erro de até 1 minuto, e falta abaixo de 30s pode não estar listada.

const TZ = 'America/Sao_Paulo'
const clockFmt = new Intl.DateTimeFormat('pt-BR', { timeZone: TZ, hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' })
const dayPartsFmt = new Intl.DateTimeFormat('en-CA', { timeZone: TZ, year: 'numeric', month: '2-digit', day: '2-digit' })

/** Pré-roll máximo do gravador (recording.Config.PreRoll), mais a conexão. */
export const MAX_PREROLL_S = 20

/** Gaps em ms, ordenados. */
function gapsMs(rec) {
  return (rec.gaps ?? [])
    .map(g => ({ from: Date.parse(g.from), to: Date.parse(g.to) }))
    .filter(g => g.to > g.from)
    .sort((a, b) => a.from - b.from)
}

function windowEndMs(rec) {
  let end = Date.parse(rec.ends_at)
  if (rec.canceled_at) end = Math.min(end, Date.parse(rec.canceled_at))
  return end
}

/**
 * Hora do ar (ms) do segundo 0 do arquivo. O pré-roll não fica guardado, então é
 * estimado: o que o arquivo tem a mais que a janela menos os buracos, entre 0 e 20s.
 */
export function audioStartMs(rec, durationS) {
  const start = Date.parse(rec.starts_at)
  const missing = gapsMs(rec).reduce((s, g) => s + (g.to - g.from), 0)
  const expected = (windowEndMs(rec) - start - missing) / 1000
  const preroll = Math.min(MAX_PREROLL_S, Math.max(0, durationS - expected))
  return start - preroll * 1000
}

/** Posição no arquivo (s) → hora do ar (ms). */
export function clockAt(rec, t, audioStart) {
  let c = audioStart + t * 1000
  for (const g of gapsMs(rec)) {
    if (c >= g.from) c += g.to - g.from
    else break
  }
  return c
}

/** Hora do ar (ms) → posição no arquivo (s). Dentro de um buraco, cai no ponto do corte. */
export function offsetAt(rec, clockMs, audioStart) {
  let skipped = 0
  for (const g of gapsMs(rec)) {
    if (clockMs >= g.to) skipped += g.to - g.from
    else if (clockMs > g.from) { skipped += clockMs - g.from; break }
    else break
  }
  return Math.max(0, (clockMs - audioStart - skipped) / 1000)
}

/** Onde cada buraco cai no arquivo: um ponto (o áudio emenda), com a duração que falta. */
export function gapMarkers(rec, audioStart) {
  return gapsMs(rec).map(g => ({ t: offsetAt(rec, g.from, audioStart), seconds: Math.round((g.to - g.from) / 1000) }))
}

/** Buracos que caem dentro do trecho [from, to]. */
export function gapsInside(markers, from, to) {
  return markers.filter(m => m.t > from && m.t < to)
}

/** "14:32:10", em horário de Brasília. */
export function formatClock(ms) {
  return clockFmt.format(new Date(ms))
}

/** "1:02:03.4" ou "2:03.4": posição no arquivo, com décimo. */
export function formatOffset(s) {
  const neg = s < 0
  const x = Math.abs(s)
  const h = Math.floor(x / 3600)
  const m = Math.floor((x % 3600) / 60)
  const sec = (x % 60).toFixed(1).padStart(4, '0')
  const body = h > 0 ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`
  return neg ? `-${body}` : body
}

/** "1m05s", "45s", "1h02m". */
export function formatSpan(s) {
  const x = Math.max(0, Math.round(s))
  const h = Math.floor(x / 3600)
  const m = Math.floor((x % 3600) / 60)
  const sec = x % 60
  if (h > 0) return `${h}h${String(m).padStart(2, '0')}m`
  if (m > 0) return `${m}m${String(sec).padStart(2, '0')}s`
  return `${sec}s`
}

/**
 * "14:30" ou "14:30:15" no dia da gravação (São Paulo) → ms. null se não for hora.
 * Usa o dia do início da gravação: a janela não atravessa a meia-noite.
 */
export function parseClockInput(rec, text) {
  const m = /^\s*(\d{1,2})[:h](\d{2})(?::(\d{2}))?\s*$/i.exec(text ?? '')
  if (!m) return null
  const [hh, mm, ss] = [Number(m[1]), Number(m[2]), Number(m[3] ?? 0)]
  if (hh > 23 || mm > 59 || ss > 59) return null
  const day = dayPartsFmt.format(new Date(rec.starts_at))
  // São Paulo não tem horário de verão desde 2019: -03:00 fixo.
  return Date.parse(`${day}T${String(hh).padStart(2, '0')}:${String(mm).padStart(2, '0')}:${String(ss).padStart(2, '0')}-03:00`)
}

/**
 * Nome padrão de um trecho: "Rádio X 14h32-14h47". No mesmo minuto ganha os
 * segundos ("14h08m00s-14h08m13s"), senão dois trechos curtos teriam o mesmo nome.
 */
export function defaultClipName(rec, fromClock, toClock) {
  const hm = ms => formatClock(ms).slice(0, 5).replace(':', 'h')
  const hms = ms => { const [h, m, s] = formatClock(ms).split(':'); return `${h}h${m}m${s}s` }
  const same = hm(fromClock) === hm(toClock)
  const f = same ? hms : hm
  return `${rec.station_name} ${f(fromClock)}-${f(toClock)}`
}

/** Nome do .zip com todos os trechos. */
export function zipName(rec) {
  const day = dayPartsFmt.format(new Date(rec.starts_at))
  return `Trechos ${rec.station_name} ${day}`
}

/** Passo da régua (s) para caber ~1 rótulo a cada `minPx` pixels. */
const STEPS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600]
export function rulerStep(spanS, widthPx, minPx = 90) {
  const want = (spanS / Math.max(1, widthPx)) * minPx
  return STEPS.find(s => s >= want) ?? 3600
}

let seq = 0
/** Trecho novo, com from < to garantido e preso à duração. */
export function makeClip(rec, from, to, duration, audioStart) {
  const a = Math.max(0, Math.min(from, to))
  const b = Math.min(duration, Math.max(from, to))
  seq += 1
  return {
    id: `${Date.now().toString(36)}-${seq}`,
    from: a,
    to: b,
    name: defaultClipName(rec, clockAt(rec, a, audioStart), clockAt(rec, b, audioStart)),
    named: false, // false = nome automático, acompanha o arraste das alças
  }
}

/** Ajusta um trecho; o nome automático acompanha os novos tempos. */
export function updateClip(rec, clip, patch, duration, audioStart) {
  let from = patch.from ?? clip.from
  let to = patch.to ?? clip.to
  from = Math.max(0, Math.min(from, duration))
  to = Math.max(0, Math.min(to, duration))
  if (from > to) [from, to] = [to, from]
  const next = { ...clip, ...patch, from, to }
  if (patch.name !== undefined) next.named = patch.name.trim() !== ''
  if (!next.named) next.name = defaultClipName(rec, clockAt(rec, from, audioStart), clockAt(rec, to, audioStart))
  return next
}

/** Máximo de cada coluna de pixel entre [viewStart, viewEnd]. */
export function bucketPeaks(peaks, pps, viewStart, viewEnd, width) {
  const out = new Uint8Array(width)
  if (!peaks || peaks.length === 0 || width <= 0) return out
  const per = ((viewEnd - viewStart) * pps) / width
  for (let x = 0; x < width; x++) {
    const a = Math.floor(viewStart * pps + x * per)
    const b = Math.max(a + 1, Math.floor(viewStart * pps + (x + 1) * per))
    let max = 0
    for (let i = Math.max(0, a); i < Math.min(peaks.length, b); i++) if (peaks[i] > max) max = peaks[i]
    out[x] = max
  }
  return out
}

const STORE_PREFIX = 'rec-editor:'

/** Trechos guardados no navegador. Lista vazia se não houver ou o storage falhar. */
export function loadClips(id, storage = globalThis.localStorage) {
  try {
    const raw = storage?.getItem(STORE_PREFIX + id)
    const parsed = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed) ? parsed.filter(c => Number.isFinite(c?.from) && Number.isFinite(c?.to)) : []
  } catch {
    return []
  }
}

export function saveClips(id, clips, storage = globalThis.localStorage) {
  try {
    if (clips.length === 0) storage?.removeItem(STORE_PREFIX + id)
    else storage?.setItem(STORE_PREFIX + id, JSON.stringify(clips))
  } catch {
    // Storage cheio ou bloqueado: a marcação só não sobrevive ao fechar a aba.
  }
}
